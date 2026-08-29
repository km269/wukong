# Wukong 记忆系统架构

> 本文档基于源码深度扫描，描述 Wukong 的多层记忆与知识检索系统（记忆栈单主题深潜文档）。
> 所有结论均与源码逐行核对，附文件路径与行号引用。
> 搜索管线的完整参数与算法（SearchGenome 12 参数、调优引擎、五步分块）见 [WEB_OPERATIONS_ANALYSIS.md](WEB_OPERATIONS_ANALYSIS.md) 搜索管线章节，本文仅保留记忆栈视角的简表与交叉引用。
> 最后更新：2026-08-29

---

## 1. 系统全景

Wukong 的记忆系统是一个**多层、可配置、渐进增强**的智能记忆栈。设计哲学是：每一层都能独立工作，叠加后提供更强的语义能力；任何 LLM/向量化依赖点都有确定性回退路径，保证系统在降级条件下仍可用。

记忆栈由五个紧密协作的子系统组成：

| 子系统 | 源码位置 | 职责 | 底层存储 |
|--------|----------|------|----------|
| **CortexStore** | `internal/cortex/store.go` | 混合检索核心（FTS5 + HNSW + Rerank + MMR） | SQLite `chat_recall` + CortexDB HNSW |
| **MemoryFlowService** | `internal/cortex/memoryflow.go` | 对话转录、三层唤醒上下文、事实提升 | CortexDB MemoryFlow transcript |
| **GraphFlowService** | `internal/cortex/graphflow.go` | 实体/关系提取与知识图谱构建 | CortexDB GraphRAG (SPARQL) |
| **MemoryManager** | `internal/memory/store.go` | 跨会话用户偏好/事实持久化、智能淘汰 | tRPC memory + `memory_metadata` |
| **recall.Store** | `internal/recall/store.go` | 原生 SQLite FTS5 召回（CortexStore 的轻量替代/回退） | SQLite `chat_recall` + FTS5 |

```
┌─────────────────────────────────────────────────────────────────┐
│  Agent Loop (internal/agent/)                                   │
│  ┌────────────┐  ┌────────────┐  ┌──────────────────────────┐  │
│  │ recall_    │  │ memory_*   │  │ knowledge_graph_*        │  │
│  │ search工具 │  │ (6个工具)  │  │ graph_query/build工具    │  │
│  └─────┬──────┘  └─────┬──────┘  └────────────┬─────────────┘  │
├────────┼───────────────┼──────────────────────┼────────────────┤
│  Cortex 层 (HNSW + FTS5 + GraphRAG)                            │
│  ┌─────▼──────┐ ┌──────▼───────┐ ┌────────────▼────────────┐  │
│  │ CortexStore│ │MemoryFlow    │ │GraphFlow                │  │
│  │ (store.go) │ │Service       │ │Service                  │  │
│  └─────┬──────┘ └──────┬───────┘ └────────────┬────────────┘  │
│        │   SearchGenome│Chunking│Vertical│Tune (internal/search)│
├────────┼───────────────┼──────────────────────┼────────────────┤
│  存储                                                           │
│  ┌──────────┐  ┌──────────────┐  ┌────────────┐  ┌─────────┐  │
│  │ SQLite   │  │ CortexDB     │  │ tRPC mem   │  │ OKF     │  │
│  │(共享*sql │  │ (HNSW 向量+  │  │ +metadata  │  │(.md注入)│  │
│  │ .DB)     │  │  GraphRAG)   │  │  表        │  │         │  │
│  └──────────┘  └──────────────┘  └────────────┘  └─────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

---

## 2. CortexStore — 混合检索核心

源码：`internal/cortex/store.go`

### 2.1 双存储架构

CortexStore 采用**双路径存储**，两个存储后端共享同一个 `*sql.DB` 句柄（避免 SQLite 多连接事务冲突）：

- **`lexicalStore`（FTS5，权威数据源）**：消息**始终**写入 SQLite `chat_recall` 表，并由 FTS5 触发器同步到 `chat_recall_fts` 虚拟表。即使没有 embedder，词法检索永远可用。
- **CortexDB HNSW 向量索引（条件性）**：仅当配置了 embedder 时（`store.go:75` `if embedder != nil`），额外打开 CortexDB 并写入 HNSW 向量索引。

```go
// store.go:28-39
type CortexStore struct {
    cfg         *config.CortexConfig
    embedder    *Embedder
    reranker    *Reranker
    router      *vertical.Router       // 垂直域路由
    chunker     *chunking.Chunker      // 语义分块
    metrics     *metrics.SearchMetrics
    db          *cortexdb.DB           // 真实 CortexDB (HNSW + FTS5)
    lexical     *lexicalStore          // FTS5，共享 *sql.DB
    vectorCache *VectorCache
    genome      search.SearchGenome    // 检索策略参数
}
```

**共享 DB 管理要点**：
- `lexicalStore` 接收 `DatabasePool` 分配的共享 `*sql.DB`（`store.go:65`）。
- `noCloseDBWrapper` 防止共享 DB 被某一方误关闭。
- CortexDB（HNSW）虽是独立存储，但其 FTS5 部分与 `chat_recall` 表设计一致。

### 2.2 Search 方法 — 检索决策树

`CortexStore.Search()` 是整个检索的调度入口，按优先级形成决策树（`store.go` Search 方法）：

```
Search(query, userID, limit)
  │
  ├─ 1. 垂直路由优先（若命中意图）
  │     router.Search() 命中 → mergeVertical() 合并平台 API 与本地结果
  │     （store.go:245, 326-333）
  │
  ├─ 2. 按 SearchGenome 模式分发
  │     ├─ g.IsLexicalOnly() → 纯 FTS5 检索 (lexicalStore.Search)
  │     ├─ g.IsVectorOnly()  → 纯 HNSW 检索 (+ VectorCache)
  │     ├─ g.IsHybrid()      → 五阶段混合流水线 searchHybridCortex()
  │     └─ Fallback          → 向量优先，失败降级到词法
  │
  ├─ 3. (可选) SearchWithMemory 合并 tRPC memory 结果
  │     每条 memory 结果 Score=0.5 注入候选集 (store.go:730-750)
  │
  └─ 4. recordMetric() 记录可观测性指标
```

### 2.3 混合检索五阶段流水线

`searchHybridCortex()`（`store.go:460+`）实现经典的"宽召回 → 融合 → 重排 → 多样性 → 截断"流水线：

```
┌──────────────────────────────────────────────────────────────────┐
│ 阶段 1：FTS5 词法召回                                              │
│   pool = EffectivePoolSize()（默认 50），宽候选池                  │
│   store.go:470 poolSize := g.EffectivePoolSize()                  │
├──────────────────────────────────────────────────────────────────┤
│ 阶段 2：HNSW 向量召回                                              │
│   查询向量经 VectorCache.GetOrComputeQueryVector 计算/复用         │
│   返回同 pool_size 的向量候选                                      │
├──────────────────────────────────────────────────────────────────┤
│ 阶段 3：融合（两策略，由 genome 选择）                             │
│   ① RRF (Reciprocal Rank Fusion):                                │
│      store.go:518  k := g.EffectiveRRFK()                        │
│      score += 1/(k + rank + 1)  —— 双通道命中者被显著加权         │
│   ② Weighted (加权融合):                                          │
│      store.go:561-580                                             │
│      score = DenseWeight × sim + TextWeight × (1/rank)           │
├──────────────────────────────────────────────────────────────────┤
│ 阶段 4a：冒泡排序                                                  │
│   store.go 按融合分数对候选排序                                    │
├──────────────────────────────────────────────────────────────────┤
│ 阶段 4b：Cross-Encoder 重排（可选，配置 reranker 时启用）          │
│   对 top-N 调用 reranker.Rerank()（/rerank 端点或本地模型）        │
│   失败则保留融合排序结果（降级）                                    │
├──────────────────────────────────────────────────────────────────┤
│ 阶段 4c：MMR 多样性（可选）                                        │
│   store.go:650  lambda := g.EffectiveMMRLambda()                 │
│   相似度采用 Jaccard（词集合），防止结果聚集                        │
├──────────────────────────────────────────────────────────────────┤
│ 阶段 5：截断到 top-K 返回                                          │
└──────────────────────────────────────────────────────────────────┘
```

### 2.4 SearchGenome — 检索策略参数（简表）

`cortexGenomeFromConfig()`（`store.go:780+`）将 `CortexConfig.SearchStrategy` 映射为 `internal/search.SearchGenome`。**默认混合模式权重 Dense 0.7 / Text 0.3**（`store.go:806-807`）。

| 参数 | 记忆栈视角要点 |
|------|----------------|
| `RecallMode` | lexical / vector / hybrid，决定 §2.2 决策树走哪条路径 |
| `PoolSize` | 每路召回宽候选池，默认 50（`EffectivePoolSize()`） |
| `DenseWeight` / `TextWeight` | 加权融合权重，默认 0.7 / 0.3 |
| `FusionMethod` + `RRFK` | weighted / rrf 融合策略与 RRF 常数 k（奖励双通道命中） |
| `MMRLambda` | MMR 多样性/相关性权衡（`EffectiveMMRLambda()`） |
| Reranker 开关 | Cross-Encoder 重排（可选，失败降级保留融合序） |

> SearchGenome 完整 12 参数定义、RobustScore 复合评分与 SPA 调优引擎详见 [WEB_OPERATIONS_ANALYSIS.md](WEB_OPERATIONS_ANALYSIS.md) 搜索管线章节（§5 搜索调优引擎）。

### 2.5 长消息语义分块（存储侧约定）

`StoreMessage` 调用 `chunker.Chunk()`（`internal/search/chunking`）对超长消息分块。与记忆栈直接相关的约定：

- **多 chunk 独立嵌入**：chunker 产出 >1 个 chunk 时，每个 chunk 单独 embed 并作为独立向量存储，向量 key 格式为 `msg_{id}_chunk_{i}`；单 chunk 消息则直接以 `msg_{id}` 为 key（§10 DB Schema 中的向量 key 约定同源）。
- 默认参数：MaxSize=1200 runes / Overlap=200 / MinSize=100。

> 五步分块算法（自然边界分割 → 贪婪打包 → 重叠 → 合并小块 → 定序）与 CJK 感知 token 估算详见 [WEB_OPERATIONS_ANALYSIS.md](WEB_OPERATIONS_ANALYSIS.md) 搜索管线章节（§7 语义分块）。

### 2.6 向量缓存 VectorCache

源码：`internal/cortex/vector_cache.go`

双层 LRU 缓存，避免对同一消息/查询重复调用 embedder：

| 属性 | 值 |
|------|----|
| 缓存层 | `messageCache` + `queryCache`（分离） |
| TTL | 5 分钟 |
| `maxEntries` | 1000 |
| 清理周期 | 后台 goroutine 每 1 分钟扫描过期条目 |
| 统计 | `HitRate()` 命中率 |

核心 API：
- `GetOrComputeMessageVector(msgID, text)` — 单条消息向量（带计算回填）
- `GetOrComputeQueryVector(query)` — 查询向量
- `BatchGetOrComputeMessageVectors(...)` — 批量计算，减少 embedder API 往返
- `Stop()` — 用 `isRunning` 守卫防止 double-close

### 2.7 RecallManager — recall 工具层

源码：`internal/cortex/recall_manager.go`

`RecallManager` 包装 `CortexStore`，向 agent 暴露 `recall_search` / `recall_sessions` 两个函数工具——与 `recall.RecallManager`（§6）同接口，但具备向量语义检索能力。通过 `SetMemoryReader` 注入 tRPC memory 读取回调后，`recall_search` 走 `SearchWithMemory` 交叉检索对话历史与持久记忆；未注入时退回普通 `Search`。

---

## 3. MemoryFlowService — 对话转录与唤醒

源码：`internal/cortex/memoryflow.go`

封装 CortexDB 的 `memoryflow.Service`，字段结构（`memoryflow.go:30-40`）：

```go
type MemoryFlowService struct {
    cfg       *config.CortexConfig
    db        *cortexdb.DB
    flow      memoryflow.Service     // 转录服务
    planner   *LLMQueryPlanner       // 查询规划（可选）
    extractor *LLMSessionExtractor   // 会话事实提取（可选）
}
```

### 3.1 核心方法

| 方法 | 功能 | 关键细节 |
|------|------|----------|
| `IngestTurn` | 记录单轮对话到转录存储 | `Scope=MemoryScopeSession`，`Source="chat"`（`memoryflow.go:91,99`） |
| `WakeUp` | 构建三层唤醒上下文 | 见 3.2 |
| `PromoteFacts` | 从转录提取可提升的事实候选 | `GetTranscript → SessionState → extractor.Extract` |

### 3.2 WakeUp 三层上下文

`WakeUp()`（`memoryflow.go:110-148`）调用 `flow.WakeUpLayers()`，返回的 `resp.Layers` 拼装为 **Markdown** 格式注入 agent：

```
WakeUp(userID, sessionID, identity)
  │
  ├─ Layer 1: Identity（身份层）
  │    └─ "You are assisting {userID}..."（agent 人设）
  │
  ├─ Layer 2: Recalled memories（回忆层）
  │    └─ 从历史转录提取的用户偏好/事实/决策
  │
  └─ Layer 3: Session context（会话层）
       └─ Scope=MemoryScopeSession，当前会话最近对话摘要
```

### 3.3 事实提升 PromoteFacts

`PromoteFacts`（`memoryflow.go:179+`）流程：`GetTranscript` → 构建 `SessionState` → `extractor.Extract`。提取器为 `LLMSessionExtractor`（详见 §8.2），LLM 不可用时回退到启发式关键词匹配。

### 3.4 共享 DB 模式

`NewMemoryFlowWithDB` 允许复用已打开的 CortexDB 句柄，避免重复打开同一向量库文件。

---

## 4. GraphFlowService — 知识图谱

源码：`internal/cortex/graphflow.go`

从对话转录中提取实体/关系并构建知识图谱，底层依赖 CortexDB GraphRAG。

### 4.1 提取器选择（`graphflow.go:38-57`）

```go
if jsonGen != nil {                        // 有 JSONGenerator（LLM）能力
    extractor = graphflow.LLMExtractor{...}
} else {
    extractor = graphflow.HeuristicExtractor{}   // 启发式回退
}
```

- **`LLMExtractor`**：通过 `JSONGenerator` 接口（`internal/cortex/json_generator.go`）让 LLM 输出结构化 JSON。
- **`HeuristicExtractor`**：无 LLM 时的确定性回退。

### 4.2 图谱构建 BuildGraph

`ExtractFromTranscript`（`graphflow.go:75-78`）将转录包装为 `SourceDocument{Type: "conversation"}`。`BuildGraph` 流程：

```
SourceDocument("conversation")
  │
  ├─ extractor.Extract → nodes + edges
  │
  ├─ nodes → []ToolEntityInput
  ├─ edges → []ToolRelationInput
  │
  └─ GraphRAGTools 持久化：
       ├─ Call("ingest_document", payload)   (graphflow.go:142)
       └─ Call("upsert_relations", payload)  (graphflow.go:163)
```

### 4.3 SPARQL 查询

- **`QueryKnowledge`**：直接执行 SPARQL 查询语句。
- **`BuildContext`**（`graphflow.go:198-222`）：根据关键词动态构造 SPARQL：

```sparql
# graphflow.go:212, 220-222
FILTER(CONTAINS(LCASE(STR(?object)), "关键词"))
...
LIMIT 20
```

`LCASE(STR(?object))` 实现大小写不敏感的子串匹配，结果上限 20 条。

### 4.4 KGToolManager — 知识图谱工具层

源码：`internal/cortex/kg_tools.go`

包装 `GraphFlowService`，向 agent 暴露两个工具：`knowledge_graph_query`（执行任意 SPARQL 查询，即 §4.3 `QueryKnowledge` 的工具化封装）与 `knowledge_graph_analyze`（图统计分析，返回实体数/边数与关键连接摘要）。

---

## 5. MemoryManager — 持久化记忆

源码：`internal/memory/store.go` + `internal/memory/metadata.go`

封装 tRPC-Agent-Go memory service，提供跨会话的用户偏好/事实持久化，并叠加智能淘汰与引用追踪。

### 5.1 双模式

| 模式 | 机制 | 细节 |
|------|------|------|
| **Auto Extract** | LLM 自动从对话提取记忆 | 3 个异步 worker，本地模型超时 600s（`store.go:642`） |
| **Manual Tools** | 6 个显式工具 | `add / search / update / delete / load / clear` |

`trackingMemoryService` 包装底层 `Service`，跟踪在途（in-flight）任务；worker 池自然排空而非强制关闭 DB（`store.go:139,182`），以保留共享 DB。

### 5.2 SmartCleanup — 容量感知淘汰（`store.go:330-369`）

```go
// store.go:361-362
keepTarget := m.cfg.MaxMemories * 60 / 100   // 淘汰目标：降到 60%
softLimit  := m.cfg.MaxMemories * 80 / 100   // 触发阈值：达到 80%
```

| 容量状态 | 策略 |
|----------|------|
| `< 80%` | 仅删除已过期（> TTL）的记忆 |
| `≥ 80%` | 按四维评分排序，淘汰最低分者直到降到 60% |

**四维评分公式**（`store.go:329-340, 437-477`）：

```
score = recency×0.4 + reference×0.3 + importance×0.2 + length×0.1

recency     = 1.0 - age/maxAge          (maxAge = 365 天, store.go:371)
              < 0 则截断为 0
reference   = ReferenceCount / 10.0      (上限 1.0, store.go:443)
importance  = high=1.0 / medium=0.5 / low=0.2   (store.go:449-459)
length      = contentLen / 500.0         (上限 1.0, store.go:468)
```

权重来自配置 `RecencyWeight / ReferenceWeight / ImportanceWeight / LengthWeight`，默认即 0.4/0.3/0.2/0.1。

### 5.3 动态 TTL（`AdjustTTLByReference`）

引用频率越高，记忆存活越久；零引用则加速过期：

| 引用次数 (`ReferenceCount`) | TTL 倍率 |
|------------------------------|----------|
| `≥ 5` | `× 2.0` |
| `≥ 2` | `× 1.5` |
| `== 0` | `× 0.5` |

### 5.4 MetadataManager 与 DB Schema

源码：`internal/memory/metadata.go`

```sql
-- metadata.go:43-57
CREATE TABLE IF NOT EXISTS memory_metadata (
    memory_id          TEXT PRIMARY KEY,
    user_id            TEXT,
    reference_count    INTEGER DEFAULT 0,
    importance         TEXT,           -- high/medium/low
    last_referenced_at TIMESTAMP,
    created_at         TIMESTAMP
);
-- 索引：user_id / reference_count DESC / importance
```

关键操作：
- **`RecordReference`**（`metadata.go:71-77`）：`INSERT OR REPLACE` + `COALESCE` 累加引用计数并保留原始 `created_at`。
- **`AdjustTTLByReference`**：动态 TTL 计算（见 5.3）。
- **`GetImportanceScore`**：将 high/medium/low 映射为 1.0/0.5/0.2。
- **`QueryHighImportance`**：按 importance 查询高重要性记忆。
- **`QueryLowReference`**（`metadata.go:320-324`）：`ORDER BY reference_count DESC, last_referenced_at DESC` 查低引用候选（供淘汰）。

---

## 6. recall.Store — 原生 FTS5 召回

源码：`internal/recall/store.go`

作为 CortexStore 的轻量级替代/回退方案，与 `cortex/lexical.go` **共享同一套 `chat_recall` + `chat_recall_fts` + 触发器设计**。

### 6.1 SQLite Schema

| 对象 | 用途 |
|------|------|
| `chat_recall` | 主消息表（id, session_id, user_id, role, content, created_at） |
| `chat_recall_fts` | FTS5 虚拟表（unicode61 tokenizer，content 自动同步） |
| `chat_recall_vec` | 向量索引表（msg_id, vector JSON, content_snippet） |
| 触发器 ×3 | INSERT / DELETE / UPDATE 保持 FTS5 与主表同步 |

### 6.2 检索与降级链

```
SearchHybrid (store.go)
  │
  ├─ FTS5 召回（store.go:165-182）
  │    SELECT ... FROM chat_recall_fts fts
  │    JOIN chat_recall cr ...
  │    WHERE chat_recall_fts MATCH ftsQuery(query)
  │    ORDER BY fts.rank
  │
  ├─ embed 查询 + 候选 → RRF / 加权融合
  │
  └─ 冒泡排序取 top-K
```

**`ftsQuery`**（`store.go:286-300`）：为每个词添加 `*` 前缀匹配（除非已含 `*"'()` 等通配/操作符），实现前缀自动补全。

**降级策略链**：FTS5 MATCH 失败 → `searchLike`（LIKE 搜索，`store.go:224`）→ 内存中 cosine 向量搜索。

### 6.3 消息限制裁剪

超限会话按时间删除最旧消息（`store.go:127`）：

```sql
DELETE FROM chat_recall WHERE id IN (
  SELECT id FROM chat_recall WHERE session_id = ?
  ORDER BY created_at ASC
  LIMIT (SELECT MAX(0, COUNT(*) - ?) FROM chat_recall WHERE session_id = ?)
)
```

### 6.4 向后兼容

`genomeFromConfig` 向后兼容：未配置 SearchStrategy 时回退到默认 genome，老配置可直接运行。

---

## 7. 知识导入与 OKF 增强

cortex 包中与记忆栈协作的结构化知识入口，均支持无 LLM 的确定性路径。

### 7.1 ImportFlowService（`internal/cortex/import_flow.go`）

封装 CortexDB ImportFlow，把结构化数据导入知识图谱与 RAG 存储：

- `ParseDDL`：解析 CREATE TABLE DDL（PostgreSQL/MySQL 子集）。
- `MappingFromDDL`：生成确定性映射计划——表 → 实体类、主键 → 实体 ID、外键 → 关系、列 → RAG 内容 + 实体属性，无需 LLM。
- `MappingFromDDLWithLLM`：LLM 增强版映射（语义化关系命名、推断隐式关系），LLM 不可用时回退确定性映射。
- `ImportCSV`：按映射计划逐行导入 CSV，构建实体与关系。

持有独立的 CortexDB 句柄（`config.ImportFlowConfig.DBPath` 解析），`Close()` 负责释放，`DB()` 可交出句柄供复用。

### 7.2 ImportToolManager（`internal/cortex/import_tools.go`）

把 ImportFlowService 能力封装为 4 个 agent 工具：`importflow_ddl_parse`（解析 DDL 表结构）、`importflow_ddl_plan`（确定性映射，无需 LLM）、`importflow_ddl_plan_ai`（LLM 增强映射，失败回退确定性映射）、`importflow_csv`（按既有计划导入 CSV）。构造时注入可选 `JSONGenerator`，传 nil 则仅用确定性映射。

### 7.3 EnrichmentAgent — OKF 富化（`internal/cortex/okf_enrichment.go`）

知识自动化生产者：扫描结构化数据源并生成 OKF Bundle 概念文档（参考 Google OKF 参考实现中面向 BigQuery 的 Enrichment Agent，泛化为多数据源）：

- `EnrichFromDDL`：每张表生成一个 concept（`tables/{name}.md`，含 Schema 表格与外键交叉链接），描述文字可由 LLM 富化。
- `EnrichFromDirectory`：遍历目录中的可富化文本文件，逐文件生成 `document` 类型 concept（正文原样导入）。

产出经 `okf.WriteBundle` 落盘后，由唤醒流的 `okf_injector.go` 注入系统提示（见 §9.3）。

---

## 8. LLM 组件与确定性回退

每个 LLM 依赖点都配有确定性启发式回退，保证无 LLM 时系统仍可运行。

### 8.1 LLMQueryPlanner（`internal/cortex/planner.go`）

查询路由规划：决定单次查询走 lexical / vector / hybrid。

| 策略 | 机制 |
|------|------|
| **LLM 主路径** | 单词输出（lexical/vector/hybrid），`MaxTokens=8`，`Temperature=0.0`（追求确定性） |
| **启发式回退** | `>10 词 → hybrid`；含抽象标记 → `vector`；否则 `lexical` |

### 8.2 LLMSessionExtractor（`internal/cortex/extractor.go`）

两级事实提取：

| 策略 | 机制 |
|------|------|
| **LLM 主路径** | 输出 JSON 格式 facts，`Temperature=0.3`，超时 120s |
| **启发式回退** | 中英文关键词匹配（preference/decision/note 等） |

### 8.3 LLMJSONGenerator（`internal/cortex/json_generator.go`）

供 GraphFlow 的 `LLMExtractor` 使用，约束 LLM 输出可解析 JSON。不可用则 GraphFlow 退化为 `HeuristicExtractor`。

---

## 9. 数据流总览

### 9.1 对话记忆写入流

```
用户消息
  │
  ├─→ Agent Loop
  │
  ├─→ recall.StoreMessage()                    [internal/recall/store.go]
  │     └─ chat_recall + FTS5 trigger 同步
  │
  ├─→ CortexStore.StoreMessage()               [internal/cortex/store.go]
  │     ├─ lexicalStore.storeMessage()         [SQLite chat_recall，权威源]
  │     └─ storeCortexVector()                 [HNSW，含 chunking:
  │            chunker.Chunk() → 每块 embed → msg_{id}_chunk_{i}]
  │
  └─→ MemoryFlowService.IngestTurn()           [internal/cortex/memoryflow.go]
        └─ transcript 记录, Scope=Session, Source="chat"
```

### 9.2 检索记忆流

```
recall_search 工具 / Run() 上下文注入
  │
  ├─→ CortexStore.Search()                     [internal/cortex/store.go]
  │     │
  │     ├─ 垂直路由命中? → mergeVertical()      [store.go:245,326]
  │     │
  │     ├─ IsLexicalOnly? → FTS5 检索
  │     ├─ IsVectorOnly?  → HNSW + VectorCache
  │     ├─ IsHybrid?      → searchHybridCortex()
  │     │     ├─ Step1 FTS5 召回 (pool=EffectivePoolSize 默认 50)
  │     │     ├─ Step2 HNSW 召回
  │     │     ├─ Step3 融合 (RRF k=EffectiveRRFK / Weighted Dense×sim+Text×1/rank)
  │     │     ├─ Step4a 冒泡排序
  │     │     ├─ Step4b (可选) Cross-Encoder 重排 (reranker.Rerank)
  │     │     ├─ Step4c (可选) MMR (lambda=EffectiveMMRLambda, Jaccard)
  │     │     └─ Step5 top-K
  │     │
  │     └─ recordMetric()
  │
  └─→ SearchWithMemory() (可选)                [store.go:730-750]
        └─ tRPC memory 结果合并 (每条 Score=0.5)
```

### 9.3 唤醒与知识注入流（会话启动）

```
会话启动
  │
  ├─→ WakeUpWithKnowledgeIndex()
  │     └─ KnowledgeIndexInjector.Inject()      [internal/cortex/okf_injector.go]
  │           └─ 解析 OKF bundle 的 index.md → 注入系统提示
  │
  └─→ MemoryFlowService.WakeUp()               [internal/cortex/memoryflow.go]
        └─ 三层上下文 (Identity → Recalled → Session) → Markdown 注入 system prompt
```

### 9.4 知识图谱构建流（异步）

```
对话结束后（异步）
  │
  ├─→ MemoryFlowService.PromoteFacts()
  │     └─ GetTranscript → SessionState → LLMSessionExtractor → PromotionCandidate
  │
  └─→ GraphFlowService.ExtractFromTranscript()
        └─ SourceDocument("conversation")
              └─ LLMExtractor / HeuristicExtractor → nodes/edges
              └─ BuildGraph → GraphRAG: ingest_document + upsert_relations
```

### 9.5 模型事件日志（model-visible means logged）

源码：`internal/session/eventlog.go`

`ModelEventLog` 是 Wukong 层的 append-only SQLite 日志，记录 agent 单轮中模型**实际看到**的消息。它与框架 `session.Service` 自有事件存储刻意分离：框架日志只记录原始用户输入，而 `CoreLoop.Run` 在交给 runner 前会用唤醒上下文、recall 结果与持久记忆增强输入——增强后的消息才是到达模型请求的内容，必须可重建。

**不变式："model-visible means logged"**——凡到达模型请求的内容，都必须能从该日志回放（`eventlog.go:13-14`）。

事件类型（描述单轮增强的生命周期）：

| 事件 | 含义 |
|------|------|
| `turn_start` | Run 调用开始，payload 为增强前的原始用户消息 |
| `user_message` | 未修改的原始用户消息文本 |
| `context_inject` | 一次增强注入（source: `wakeup` / `recall` / `persistent`） |
| `model_message` | 交给 `runner.Run` 的最终增强消息——不变式的见证 |
| `turn_end` | 轮次完成（或拒绝，payload 为原因） |

关键 API：

- `Append`：per-session 单调 `seq`（内存缓存 + 懒加载 `MAX(seq)`，`eventlog.go:112-133`）；写入失败不阻塞 agent loop（尽力而为）。
- `ReplayMessages`：读侧回放——按 seq 序以 `turn_start`/`user_message` 起底、`context_inject` 追加、`model_message` 冻结并 flush，重建模型可见消息序列；无 `model_message` 时回退 `user_message`。
- `ReplayModelMessage`：取最近一次 `model_message`，即模型上一轮确切所见。
- `VerifyInvariant`：调试辅助，比对最终内容与日志记录，不一致仅告警不失败。

DB 句柄由 `util.DatabasePool` 共享管理，本类型不关闭（`Close()` 为 no-op）。

---

## 10. DB Schema 汇总

| 表/对象 | 位置 | 关键字段 |
|---------|------|----------|
| `chat_recall` | SQLite（recall/cortex 共享） | id, session_id, user_id, role, content, created_at |
| `wukong_model_events` | SQLite（`internal/session/eventlog.go:87`） | id(PK), session_id, user_id, seq, event_type, source, payload, created_at；索引 `(session_id, seq)` 与 `event_type` |
| `chat_recall_fts` | SQLite FTS5 虚拟表 | content（unicode61），触发器同步 |
| `chat_recall_vec` | SQLite | msg_id, vector(JSON), content_snippet |
| `memory_metadata` | SQLite（`internal/memory/metadata.go:43`） | memory_id(PK), user_id, reference_count, importance, last_referenced_at, created_at |
| CortexDB HNSW | CortexDB（条件性，embedder 启用时） | 向量 key: `msg_{id}` / `msg_{id}_chunk_{i}` |
| MemoryFlow transcript | CortexDB | Scope=Session, Source="chat" |
| GraphRAG | CortexDB | 实体（ingest_document）/ 关系（upsert_relations） |

---

## 11. 关键设计决策

### 11.1 渐进降级

每一层都内置降级路径，单点故障不会击穿整个记忆栈：

| 故障点 | 降级路径 |
|--------|----------|
| FTS5 不可用 | LIKE 搜索（`searchLike`） |
| Embedder 失败 | 纯词法检索 |
| 向量结果不足 | FTS5 补充候选 |
| Reranker 失败 | 保留融合排序结果 |
| MMR 失败 | 保留重排结果 |
| LLM 不可用 | 启发式提取/规划（planner/extractor/graphflow 三处） |
| JSONGenerator 缺失 | GraphFlow 退化为 HeuristicExtractor |

### 11.2 共享连接管理

SQLite 单文件多连接会导致 "transaction has already been committed" 错误。统一通过 `util.DatabasePool` 管理：

- `lexicalStore` 接收共享 `*sql.DB`（`store.go:65`）。
- `CortexStore.SetDB()` 允许复用 MemoryFlow 的 CortexDB 句柄。
- `noCloseDBWrapper` 防止共享 DB 被误关闭。
- `NewMemoryFlowWithDB` 复用已打开的 CortexDB。

### 11.3 LLM + 启发式双策略

每个 LLM 依赖点都有确定性回退，且 LLM 调用参数追求稳定输出：

| 组件 | LLM 参数 | 回退 |
|------|----------|------|
| QueryPlanner | MaxTokens=8, Temp=0.0 | 词数/抽象标记启发式 |
| SessionExtractor | Temp=0.3, 120s 超时 | 中英文关键词匹配 |
| GraphFlow LLMExtractor | 经 JSONGenerator | HeuristicExtractor |

### 11.4 权威源单一化

`lexicalStore`（FTS5 → `chat_recall`）始终是**权威数据源**：无论是否启用向量索引，消息必经此路径写入。HNSW 向量索引是**增强层**而非必需，这保证了向量库损坏/缺失时数据零丢失。

### 11.5 双通道奖励机制

混合检索的 RRF 融合（`k=EffectiveRRFK`）天然奖励**同时被 FTS5 与 HNSW 召回**的候选——双通道命中者在 `Σ 1/(k+rank+1)` 中累加两次，排名显著提升，这正是混合检索优于单通道的核心所在。

---

## 12. 相关文档

- [项目 README](../README.md) — Wukong 项目总览
- [系统架构](ARCHITECTURE.md) — 分层设计与启动流程
- [Web 操作与搜索管线](WEB_OPERATIONS_ANALYSIS.md) — SearchGenome 完整参数、调优引擎、语义分块算法细节
- [OKF 指南](OKF_GUIDE.md) — Open Knowledge Format（唤醒流注入的 bundle 格式）
- [配置参考](CONFIG.md) — Cortex/Memory/Recall/SearchStrategy 配置项
