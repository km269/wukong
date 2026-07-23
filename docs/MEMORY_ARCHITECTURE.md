# 记忆系统架构

> 三层记忆: 短期(Recall) → 中期(CortexStore) → 长期(tRPC Memory)
> 混合检索: FTS5 全文 + HNSW 向量 + 知识图谱
> 知识格式: OKF v0.1 | 发现协议: ARD | 自动丰富: EnrichmentAgent

---

## 目录

1. [系统概述](#1-系统概述)
2. [三层记忆架构](#2-三层记忆架构)
3. [短期记忆：Recall 系统](#3-短期记忆recall-系统)
4. [中期记忆：CortexStore](#4-中期记忆cortexstore)
5. [长期记忆：tRPC Memory](#5-长期记忆trpc-memory)
6. [记忆流服务：MemoryFlow](#6-记忆流服务memoryflow)
7. [知识图谱：GraphFlow](#7-知识图谱graphflow)
8. [知识格式：OKF](#8-知识格式okf)
9. [混合检索引擎](#9-混合检索引擎)
10. [配置参考](#10-配置参考)
11. [常见问题](#11-常见问题)

---

## 1. 系统概述

### 1.1 设计理念

借鉴人类记忆的三层结构，构建 **"输入 → 处理 → 存储 → 检索"** 的完整记忆流水线：

```
对话输入
    │
    ▼
┌──────────────────────────────┐
│  短期记忆 (Short-term)        │
│  Recall 系统                  │
│  ├── FTS5 全文检索            │
│  ├── 会话级上下文             │
│  └── 消息条数限制             │
└──────────────┬───────────────┘
               │  重要事实提升
               ▼
┌──────────────────────────────┐
│  中期记忆 (Mid-term)          │
│  CortexStore (CortexDB)       │
│  ├── HNSW 向量索引            │
│  ├── 语义相似度检索           │
│  └── 知识图谱实体关系         │
└──────────────┬───────────────┘
               │  持久化知识
               ▼
┌──────────────────────────────┐
│  长期记忆 (Long-term)         │
│  tRPC Memory Service          │
│  ├── 结构化事实存储           │
│  ├── 用户偏好记忆             │
│  └── 跨会话知识复用           │
└──────────────────────────────┘
```

### 1.2 核心组件

| 层级 | 组件 | 存储引擎 | 检索方式 | 保留时间 |
|------|------|---------|---------|---------|
| 短期 | Recall | SQLite FTS5 | 关键词全文 | 会话级 / N 条 |
| 中期 | CortexStore | CortexDB (HNSW + FTS5) | 语义向量 + 全文 | 中长期 |
| 长期 | tRPC Memory | tRPC 内存服务 | 结构化查询 | 永久 |
| 流 | MemoryFlow | CortexDB MemoryFlow | 分层唤醒上下文 | 会话驱动 |
| 图谱 | GraphFlow | CortexDB GraphRAG | SPARQL 图查询 | 永久 |

### 1.3 代码组织

```
internal/
    ├── recall/                  # 短期记忆系统
    │   ├── store.go             # FTS5 存储 + 混合搜索
    │   ├── tool.go              # 工具定义
    │   └── store_test.go        # 单元测试
    │
    ├── cortex/                  # 中期记忆 + 知识图谱
    │   ├── store.go             # CortexStore 向量 + 词法存储
    │   ├── recall_manager.go    # Recall 工具管理器
    │   ├── memoryflow.go        # MemoryFlow 记忆流服务
    │   ├── graphflow.go         # GraphFlow 知识图谱
    │   ├── embedder.go          # 向量嵌入器
    │   ├── planner.go           # 查询规划器
    │   ├── extractor.go         # 会话提取器
    │   ├── lexical.go           # 词法存储 (FTS5)
    │   ├── vector_cache.go      # 向量缓存
    │   ├── okf_enrichment.go    # OKF 知识丰富代理
    │   ├── okf_injector.go      # OKF 注入器
    │   ├── import_flow.go       # 导入流
    │   ├── import_tools.go      # 导入工具
    │   ├── json_generator.go    # JSON 生成器 (LLM)
    │   └── kg_tools.go          # 知识图谱工具
    │
    ├── extension/builtin/
    │   └── memory.go            # tRPC Memory 内置扩展
    │
    ├── okf/                     # OKF 知识格式
    │   ├── bundle.go            # Bundle 结构
    │   ├── concept.go           # 概念结构
    │   ├── frontmatter.go       # Frontmatter
    │   └── io.go                # 读写操作
    │
    └── ard/                     # 自动发现协议
        └── okf.go               # OKF Bundle 发现
```

---

## 2. 三层记忆架构

### 2.1 记忆流转机制

```
每轮对话
    │
    ├── 写入短期记忆 (Recall)
    │   └── 会话内快速检索
    │
    ├── 写入中期记忆 (CortexStore)
    │   ├── 向量化 + HNSW 索引
    │   └── 跨会话语义检索
    │
    └── MemoryFlow 处理
        ├── WakeUp: 构建上下文
        │   ├── Layer 1: Identity (角色)
        │   ├── Layer 2: Recalled memories (召回)
        │   └── Layer 3: Session context (会话)
        │
        └── PromoteFacts: 提升重要事实
            └── → 长期记忆 (tRPC Memory)
```

### 2.2 各层职责对比

| 维度 | 短期记忆 | 中期记忆 | 长期记忆 |
|------|---------|---------|---------|
| **目标** | 会话内上下文 | 语义记忆 + 知识图谱 | 持久化事实 |
| **数据** | 原始消息文本 | 消息向量 + 实体关系 | 结构化事实 |
| **检索** | 关键词 / BM25 | 向量相似度 + 全文 | 精确匹配 / 搜索 |
| **容量** | 有限 (N 条/会话) | 较大 (向量索引) | 大 (结构化存储) |
| **写入** | 每轮对话自动 | 每轮对话自动 | 手动 / 自动提升 |
| **丢失** | 会话结束 / 超限 | 数据库清理 | 手动删除 |
| **典型用例** | "我们刚才说的那个..." | "之前我们讨论过类似的..." | "记住我的偏好..." |

### 2.3 写入路径

```
用户/助手消息
    │
    ├──→ Recall.StoreMessage()  ──→ SQLite FTS5
    │                              (短期: 会话级)
    │
    ├──→ CortexStore.StoreMessage()
    │       ├── 词法表 (权威来源)
    │       └── 向量表 (HNSW 索引)
    │                              (中期: 语义级)
    │
    └──→ MemoryFlow.IngestTurn()
            └── 转录存储
                   │
                   └── 会话结束时 PromoteFacts
                           └──→ tRPC Memory (长期)
```

### 2.4 检索路径

```
用户查询
    │
    ├──→ recall_search 工具
    │       ├── CortexStore 向量搜索 (有 embedding 时)
    │       ├── 或 FTS5 全文搜索 (无 embedding 时)
    │       └── + 可选: tRPC Memory 交叉搜索
    │
    ├──→ memory_search 工具
    │       └── tRPC Memory 结构化搜索
    │
    └──→ MemoryFlow.WakeUp()
            ├── Layer 1: 身份/角色
            ├── Layer 2: 历史记忆召回
            └── Layer 3: 当前会话上下文
```

---

## 3. 短期记忆：Recall 系统

### 3.1 功能定位

Recall 系统负责 **会话级记忆**，类似人类的工作记忆：
- 存储当前及历史对话消息
- 支持快速全文检索
- 限制每会话消息数，防止无限增长

### 3.2 数据模型

```go
type ChatMessage struct {
    ID        int64      // 自增 ID
    SessionID string     // 会话 ID
    UserID    string     // 用户 ID
    Role      string     // user / assistant / tool
    Content   string     // 消息内容
    CreatedAt time.Time  // 创建时间
}
```

### 3.3 存储引擎：SQLite FTS5

**表结构**：

```sql
-- 主表
CREATE TABLE chat_recall (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- FTS5 虚拟表 (全文索引)
CREATE VIRTUAL TABLE chat_recall_fts USING fts5(
    content,
    content='chat_recall',
    content_rowid='id',
    tokenize='unicode61'
);

-- 同步触发器 (INSERT/DELETE/UPDATE)
CREATE TRIGGER recall_fts_insert AFTER INSERT ON chat_recall
BEGIN
    INSERT INTO chat_recall_fts(rowid, content)
    VALUES (new.id, new.content);
END;
```

### 3.4 检索方式

#### 方式一：FTS5 全文检索（默认）

```go
func (s *Store) Search(query, userID string, limit int) ([]SearchResult, error)
```

**特性**：
- **BM25 排序**：比简单 LIKE 更精准的相关性评分
- **前缀匹配**：每个词自动加 `*`，支持部分匹配
- **Unicode61 分词**：支持中英文等多语言
- **降级机制**：FTS5 不可用时自动回退到 LIKE

#### 方式二：混合搜索 (Hybrid Search)

```go
func (s *Store) SearchHybrid(
    ctx context.Context,
    query, userID string,
    limit int,
) ([]SearchResult, error)
```

**流程**：
```
Step 1: FTS5 检索 Top 50 候选
Step 2: Query 向量化
Step 3: 候选文本向量化
Step 4: 计算余弦相似度
Step 5: 混合评分 = 0.7 × 语义 + 0.3 × BM25
Step 6: 重排序返回 Top K
```

### 3.5 消息数量限制

每会话消息数超过 `MaxMessagesPerSession` 时，自动删除最旧的消息：

```sql
DELETE FROM chat_recall WHERE id IN (
    SELECT id FROM chat_recall
    WHERE session_id = ?
    ORDER BY created_at ASC
    LIMIT (SELECT MAX(0, COUNT(*) - ?) FROM chat_recall WHERE session_id = ?)
)
```

### 3.6 会话管理

| 方法 | 说明 |
|------|------|
| `StoreMessage(msg)` | 存储一条消息 |
| `Search(query, userID, limit)` | 全局搜索 |
| `SearchBySession(sessionID, query, limit)` | 会话内搜索 |
| `ListSessions(userID)` | 列出会话列表 |
| `DeleteSession(sessionID)` | 删除会话 |

---

## 4. 中期记忆：CortexStore

### 4.1 功能定位

CortexStore 是 **语义级记忆**，基于 CortexDB 实现：
- HNSW 向量索引 → 语义相似度检索
- FTS5 全文索引 → 关键词检索
- 共享数据库连接 → 避免 SQLite 事务冲突

### 4.2 双引擎架构

```
CortexStore
    ├── 词法引擎 (Lexical Store)
    │   ├── SQLite FTS5
    │   ├── 权威数据源
    │   └── 共享 *sql.DB 连接
    │
    └── 向量引擎 (Vector Store)
        ├── CortexDB HNSW 索引
        ├── 语义相似度搜索
        └── 独立 CortexDB 实例 (可共享)
```

### 4.3 共享连接设计

**问题**：多个独立 SQLite 连接同时操作同一文件会导致 "transaction has already been committed" 错误。

**解决方案**：
- 词法存储 (lexical) 使用共享的 `*sql.DB` (来自 DatabasePool)
- 向量存储使用独立的 CortexDB 实例
- 当 MemoryFlow 也启用时，两者共享同一个 CortexDB 实例

```go
// 共享 DB 模式 (CortexStore + MemoryFlow 共用一个 CortexDB)
func NewMemoryFlowWithDB(cfg, db, planner, extractor)
func (s *CortexStore) SetDB(db)
```

### 4.4 向量缓存

```go
type VectorCache struct { ... }
```

**作用**：避免重复计算向量嵌入，节省 LLM API 调用。

**缓存键**：
- 消息向量：`msg_{ID}`
- 查询向量：`query:{query}`

**获取或计算**：

```go
func (c *VectorCache) GetOrComputeMessageVector(
    ctx context.Context,
    key string,
    text string,
    computeFunc func(ctx, []string) ([][]float64, error),
) ([]float32, error)
```

### 4.5 写入流程

```
StoreMessage(msg)
    │
    ├── Step 1: 写入词法表 (权威)
    │   └── 获取自增 ID
    │
    └── Step 2: 有 Embedding 时写入向量
        ├── 检查向量缓存
        ├── 命中 → 使用缓存的向量
        ├── 未命中 → 调用 Embedder
        └── 写入 CortexDB HNSW 索引
```

### 4.6 检索流程

```
Search(query, userID, limit)
    │
    ├── 有 Embedding?
    │   ├── 是 → 向量搜索 (CortexDB HNSW)
    │   └── 否 → FTS5 全文搜索
    │
    └── 返回 SearchResult[]
        ├── Score: 相似度 / BM25
        └── Preview: 前 200 字符预览
```

### 4.7 交叉搜索：Recall + Memory

```go
func (s *CortexStore) SearchWithMemory(
    query, userID string,
    limit int,
    memoryReader MemoryReader,
) ([]recall.SearchResult, error)
```

**同时搜索两个来源**：
1. 对话历史 (CortexStore / Recall)
2. tRPC 持久化记忆

**结果合并**：
- 对话历史结果：正常显示
- 记忆结果：标记 `[Memory]` 前缀，固定得分 0.5

---

## 5. 长期记忆：tRPC Memory

### 5.1 功能定位

tRPC Memory 是 **持久化事实记忆**，通过 tRPC 协议与内存服务交互：

- 结构化事实存储
- 用户偏好记忆
- 跨会话知识复用
- 支持增删改查完整操作

### 5.2 工具集

通过 `MemoryToolSet` 暴露的工具：

| 工具名 | 说明 |
|--------|------|
| `memory_add` | 添加记忆 |
| `memory_search` | 搜索记忆 |
| `memory_delete` | 删除记忆 |
| `memory_update` | 更新记忆 |
| `memory_load` | 加载全部记忆 |
| `memory_clear` | 清空记忆 |

### 5.3 注入方式

```go
type MemoryToolSet struct {
    tools   []tool.Tool
    cfg     *config.WukongConfig
    svc     memory.Service    // tRPC 记忆服务
    userKey memory.UserKey    // {AppName, UserID}
}
```

**工作原理**：
1. 启动时创建空的 `MemoryToolSet`
2. tRPC 记忆服务就绪后调用 `SetMemoryService()`
3. 注入后 `Tools()` 返回标准 tRPC 记忆工具
4. 避免工具名冲突，确保一致性

### 5.4 与中期记忆的关系

```
中期记忆 (CortexStore)           长期记忆 (tRPC Memory)
─────────────────────           ─────────────────────
 对话消息的向量索引               结构化事实/偏好
 自动写入每轮对话                 手动/自动提升写入
 语义相似度检索                   精确/模糊搜索
 跨会话上下文                     持久化知识复用
         │                              ▲
         └──── PromoteFacts ────────────┘
              (重要事实提升)
```

---

## 6. 记忆流服务：MemoryFlow

### 6.1 功能定位

MemoryFlow 是 **上下文构建引擎**，负责从记忆中提取相关信息，组织成结构化的上下文层，注入到 system prompt 中。

借鉴 CortexDB 的 MemoryFlow Service 实现。

### 6.2 三层唤醒 (WakeUp)

```go
func (m *MemoryFlowService) WakeUp(
    ctx context.Context,
    identity string,     // 角色设定
    query string,        // 当前查询
    sessionID string,
    userID string,
) (string, error)
```

**三层上下文结构**：

```
Layer 1: Identity  (身份层)
  └── Agent 角色 / Persona

Layer 2: Recalled Memories  (回忆层)
  └── 从历史对话中召回的相关片段

Layer 3: Session Context  (会话层)
  └── 当前会话的上下文信息
```

**输出格式**：Markdown 格式，可直接拼接到 system prompt。

### 6.3 转录摄入 (IngestTurn)

```go
func (m *MemoryFlowService) IngestTurn(
    ctx context.Context,
    sessionID string,
    userID string,
    role string,
    content string,
) error
```

每轮对话后调用，记录到 transcript 存储中。

**Transcript 结构**：
```go
memoryflow.Transcript{
    SessionID: sessionID,
    UserID:    userID,
    Source:    "chat",
    Turns: []memoryflow.TranscriptTurn{
        {Role: role, Content: content},
    },
}
```

### 6.4 事实提升 (PromoteFacts)

```go
func (m *MemoryFlowService) PromoteFacts(
    ctx context.Context,
    sessionID string,
    userID string,
) ([]memoryflow.PromotionCandidate, error)
```

**流程**：
1. 从存储中获取会话转录
2. 使用 `SessionExtractor` 提取候选事实
3. 返回提升候选列表
4. （可选）写入 tRPC 长期记忆

**调用时机**：会话结束时 / 定期调用

### 6.5 依赖组件

| 组件 | 接口 | 说明 |
|------|------|------|
| **QueryPlanner** | `memoryflow.QueryPlanner` | LLM 驱动的查询规划 |
| **SessionExtractor** | `memoryflow.SessionExtractor` | 会话事实提取 |

---

## 7. 知识图谱：GraphFlow

### 7.1 功能定位

GraphFlow 负责 **知识图谱构建与查询**：
- 从对话中提取实体和关系
- 构建属性图 (Property Graph)
- 支持 SPARQL 图查询
- 用于知识增强的上下文构建

### 7.2 实体关系提取

```go
func (g *GraphFlowService) ExtractFromTranscript(
    ctx context.Context,
    sessionID string,
    transcriptText string,
) (*graphflow.ExtractionResult, error)
```

**两种提取器**：

| 提取器 | 实现 | 适用场景 |
|--------|------|---------|
| **LLM Extractor** | `graphflow.LLMExtractor` | 高质量，需要 LLM |
| **Heuristic Extractor** | `graphflow.HeuristicExtractor` | 轻量，无 LLM 时降级 |

### 7.3 图谱构建

```go
func (g *GraphFlowService) BuildGraph(
    ctx context.Context,
    result *graphflow.ExtractionResult,
) error
```

**持久化路径**：
```
ExtractionResult
    ├── Nodes (实体)
    │   └──→ CortexDB GraphRAG → ingest_document
    │
    └── Edges (关系)
        └──→ CortexDB GraphRAG → upsert_relations
```

### 7.4 SPARQL 查询

```go
func (g *GraphFlowService) QueryKnowledge(
    ctx context.Context,
    sparqlQuery string,
) (string, error)
```

**示例：关键词过滤查询**

```sparql
PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
SELECT DISTINCT ?subject ?predicate ?object WHERE {
    ?subject ?predicate ?object .
    FILTER(CONTAINS(LCASE(STR(?object)), "keyword"))
}
LIMIT 20
```

### 7.5 上下文增强

```go
func (g *GraphFlowService) BuildContext(
    ctx context.Context,
    keywords []string,
) (string, error)
```

从知识图谱中提取与关键词相关的三元组，注入到系统提示词中，增强 Agent 的知识广度。

---

## 8. 知识格式：OKF

### 8.1 什么是 OKF？

**OKF (Open Knowledge Format)** 是开放知识格式，用于结构化知识表示。

**版本**: v0.1

**核心概念**：
- **Concept (概念)**：知识的基本单元
- **Bundle (束)**：多个概念的集合
- **Frontmatter**：概念元数据

### 8.2 Concept 结构

```go
type Concept struct {
    ID          string       // 概念唯一标识
    FilePath    string       // 相对文件路径
    Frontmatter Frontmatter  // 元数据
    Body        string       // Markdown 正文
}
```

### 8.3 Frontmatter 元数据

```yaml
---
type: table              # 概念类型
title: users            # 标题
description: 用户表       # 描述
resource: ddl://users    # 来源资源
tags: [database, table] # 标签
timestamp: 2024-01-01T00:00:00Z
---
```

### 8.4 Bundle 结构

```go
type Bundle struct {
    RootDir  string
    Concepts []*Concept
}
```

### 8.5 丰富代理：EnrichmentAgent

自动从结构化数据源生成 OKF 概念文档：

| 数据源 | 方法 | 说明 |
|--------|------|------|
| **DDL** | `EnrichFromDDL()` | 从 CREATE TABLE 生成表概念 |
| **目录** | `EnrichFromDirectory()` | 从文件目录批量导入 |

**DDL 丰富示例**：
```
输入: CREATE TABLE users (id INT, name TEXT)
输出: concepts/tables/users.md
    ├── Frontmatter: type=table, tags=[database, table]
    └── Body: 表名 + 列清单 + 外键关系
```

### 8.6 ARD 发现协议

**ARD (Automatic Resource Discovery)** 自动资源发现协议支持 OKF Bundle：

```
MediaType: application/okf-bundle+json
Metadata:
  - okf_version: 版本号
  - concept_count: 概念数量
  - concept_types: 概念类型列表
  - bundle_path: Bundle 路径
```

---

## 9. 混合检索引擎

### 9.1 检索策略对比

| 策略 | 技术 | 精度 | 召回率 | 速度 | 适用场景 |
|------|------|------|--------|------|---------|
| **全文检索** | FTS5 BM25 | 中 | 中 | 快 | 关键词明确 |
| **向量检索** | HNSW 余弦相似度 | 高 | 高 | 中 | 语义模糊匹配 |
| **混合检索** | BM25 + 向量重排 | 高 | 高 | 中 | 通用场景 |
| **图检索** | SPARQL | 高 | 低 | 慢 | 实体关系查询 |
| **记忆检索** | tRPC Memory | 中 | 中 | 快 | 事实/偏好查询 |

### 9.2 RecallManager 工具

```go
func (m *RecallManager) Tools() []tool.Tool
```

暴露的 Agent 工具：

| 工具名 | 说明 |
|--------|------|
| `recall_search` | 搜索对话历史（语义向量 + 全文） |
| `recall_sessions` | 列出历史会话 |

### 9.3 交叉检索

```
用户查询: "上次我们讨论的那个数据库方案"
    │
    ├── recall_search
    │   ├── CortexStore 向量搜索 → 找到历史对话片段
    │   └── + tRPC Memory 搜索 → [Memory] 相关事实
    │
    └── 结果合并 → 返回给 Agent
```

---

## 10. 配置参考

### 10.1 Recall 配置

```yaml
recall:
  enabled: true
  db_path: "~/.wukong/recall.db"
  max_messages_per_session: 1000
  max_results: 10
  search_mode: fts5  # fts5 / hybrid
```

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `recall.enabled` | bool | `true` | 启用 Recall 系统 |
| `recall.db_path` | string | `~/.wukong/recall.db` | SQLite 数据库路径 |
| `recall.max_messages_per_session` | int | `1000` | 每会话最大消息数 |
| `recall.max_results` | int | `10` | 搜索返回最大结果数 |
| `recall.search_mode` | string | `fts5` | 搜索模式: fts5/hybrid |

### 10.2 Cortex 配置

```yaml
cortex:
  enabled: false
  db_path: "~/.wukong/cortex.db"
  max_results: 10
  embedding:
    provider: openai  # openai / ollama / none
    model: text-embedding-3-small
    dimensions: 1536
    api_key: ""
```

### 10.3 MemoryFlow 配置

```yaml
memoryflow:
  enabled: false
  db_path: "~/.wukong/memoryflow.db"
  namespace: default
  embedding_dimensions: 1536
```

### 10.4 GraphFlow 配置

```yaml
graphflow:
  enabled: false
  db_path: "~/.wukong/graphflow.db"
  max_chars_per_doc: 8000
```

---

## 11. 常见问题

### Q1: Recall 和 CortexStore 有什么区别？

| 维度 | Recall | CortexStore |
|------|--------|-------------|
| **定位** | 短期工作记忆 | 中期语义记忆 |
| **检索** | FTS5 关键词 | 向量语义搜索 |
| **存储** | 纯 SQLite | CortexDB (SQLite + HNSW) |
| **依赖** | 无外部依赖 | 需要 Embedder |
| **速度** | 快 | 中 (向量计算耗时) |

**使用建议**：
- 简单场景用 Recall (FTS5 足够)
- 需要语义理解时启用 CortexStore
- 两者可以同时启用，RecallManager 自动选择

### Q2: 为什么用共享数据库连接？

SQLite 的多连接事务处理有局限，多个独立 `*sql.DB` 同时操作同一文件会导致：
- "database is locked" 错误
- "transaction has already been committed" 错误

**解决方案**：
- 词法存储使用 DatabasePool 的共享连接
- CortexDB 实例在 MemoryFlow 和 CortexStore 之间共享
- 通过 `SetDB()` / `NewMemoryFlowWithDB()` 注入

### Q3: 混合搜索的评分公式是什么？

```
combined_score = 0.7 × cosine_similarity + 0.3 × bm25_normalized
```

其中：
- **余弦相似度** (0-1)：语义相关度，权重 70%
- **BM25 归一化** (1/(rank+1))：关键词相关度，权重 30%

> 语义权重更高，因为混合搜索的目的是利用向量的语义理解能力。

### Q4: 向量缓存有什么用？

- **节省 API 调用**：相同文本不会重复向量化
- **提升速度**：缓存命中时跳过 LLM 调用
- **一致性**：查询向量也缓存，相同查询直接用缓存
- **渐进式更新**：新消息增量向量化

### Q5: MemoryFlow 和 Recall 是什么关系？

**互补关系**：
- **Recall**：关键词搜索，快速查找历史消息
- **MemoryFlow**：分层上下文构建，智能组织记忆

**协作方式**：
1. Recall 存储所有对话消息（原始数据）
2. MemoryFlow 从转录中提取上下文（加工后的）
3. WakeUp 输出可直接注入 system prompt 的结构化文本
4. PromoteFacts 将重要事实提升到长期记忆

---

## 附录

### 相关文档

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | 系统架构详解 |
| [CONFIG.md](./CONFIG.md) | 配置参考手册 |
| [README.md](../README.md) | 项目主页 |

### 相关代码

- `internal/recall/` — 短期记忆系统 (3 文件, ~630 行)
- `internal/cortex/` — 中期记忆 + 知识图谱 (14 文件)
- `internal/okf/` — OKF 知识格式
- `internal/extension/builtin/memory.go` — tRPC Memory 扩展
- `internal/ard/okf.go` — ARD OKF 发现

---

> **版本**: v1.0 | **最后更新**: 2026-07-23 | **相关代码**: internal/recall/, internal/cortex/, internal/okf/
