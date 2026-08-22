# Wukong 技术实现深度解析

> 本文档基于全量源码深度扫描，描述 Wukong 各子系统的技术实现细节、核心算法与数据结构。
> 最后更新：2026-08-11

---

## 目录

1. [Agent 编排引擎](#1-agent-编排引擎)
2. [上下文管理与压缩](#2-上下文管理与压缩)
3. [Recipe 子 Agent 系统](#3-recipe-子-agent-系统)
4. [Provider 系统](#4-provider-系统)
5. [记忆系统](#5-记忆系统)
6. [技能自进化引擎](#6-技能自进化引擎)
7. [搜索系统](#7-搜索系统)
8. [浏览器引擎与反爬](#8-浏览器引擎与反爬)
9. [网站克隆引擎](#9-网站克隆引擎)
10. [ZIM 归档格式](#10-zim-归档格式)
11. [扩展与发现系统](#11-扩展与发现系统)
12. [Summon 子代理委派](#12-summon-子代理委派)
13. [ARD 资源发现](#13-ard-资源发现)
14. [安全系统](#14-安全系统)
15. [协议层实现](#15-协议层实现)
16. [Gateway 消息网关](#16-gateway-消息网关)
17. [会话与存储](#17-会话与存储)
18. [可观测性](#18-可观测性)
19. [基础设施](#19-基础设施)

---

## 1. Agent 编排引擎

### 1.1 双层循环架构

Wukong 采用**分层循环设计**：

- **内循环（trpc-agent-go）**：think → act → observe → decide，由 Planner 驱动
- **外循环（CoreLoop）**：上下文压缩 → 记忆注入 → 安全校验 → 后置记忆固化

定义于 [loop.go](../internal/agent/loop.go)。

### 1.2 CoreLoop.Run() — Prepare 阶段

`Run` 是一次用户消息的入口，负责**上下文增强**：

1. **OpenTelemetry span**（`agent.Run`，带 user_id/session_id 属性）
2. **PrepareContext**：令牌预算检查，超阈值触发 `sessionService.EnqueueSummaryJob` 异步摘要
3. **Recall 存储**：用户消息写入 `chat_recall` 表（Recall 或 Cortex 优先）
4. **MemoryFlow IngestTurn + WakeUp**：记录用户轮次 → 从历史对话生成 3 层唤醒上下文（Identity / Recalled memories / Session context）→ 注入 `[Context from past conversations]`
5. **主动 Recall 检索**：即使未调用 recall_search 工具，也 FTS5/HNSW 搜索 top 5 → 注入 `[Relevant conversation history]`
6. **持久记忆注入**：`Memory.ReadMemories(userKey, 5)` 读取 → `isMemoryDuplicated` 做 60% 滑动窗口（30 字符）去重 → 注入 `[Remembered facts]` → 异步 `BatchRecordMemoryReferences`（引用频次供 SmartCleanup）
7. **runner.Run()** → 返回 `<-chan *event.Event`

### 1.3 CoreLoop.RunStream() — Finalize 阶段

1. 调用 `Run` 获取事件通道
2. 遍历事件流：`onEvent` 回调 + 累积 `choice.Delta.Content`（跳过 tool role 避免 JSON 泄露）+ 统计 tool call 数
3. **后置同步写入**（`runWg` 保护）：
   - 存储 assistant 响应到 Recall/Cortex
   - 存储每次 tool_call/tool_response 到 Recall（丰富未来检索）
   - MemoryFlow.IngestTurn（15s 超时上限防首次 gse 字典加载阻塞）
4. **后置异步写入**（`bgWg` 保护，60s 超时）：
   - `MemoryFlow.PromoteFacts` → `Memory.AddMemory`（事实晋升为持久记忆）
   - `GraphFlow.ExtractFromTranscript` + `BuildGraph`（知识图谱自动构建）

### 1.4 三层 Callbacks

| Callback | 钩子 | 作用 |
|----------|------|------|
| Agent Callback | Before/After agent run | 执行日志 |
| Tool Callback | BeforeTool | **安全核心**：4 重校验（权限黑白名单 → 审批模式 → 命令校验 → .wukongignore 文件路径） |
| Model Callback | AfterModel | Token usage 记录 |

### 1.5 Agent 编排模式

| 模式 | 实现 | 默认子 Agent | 说明 |
|------|------|-------------|------|
| `single` | LLMAgent | — | 标准 Agent + 工具调用（默认） |
| `chain` | ChainAgent | planner→executor→reviewer | 顺序执行，每个接收上一个输出 |
| `parallel` | ParallelAgent | code/doc/test-analyzer | 并发执行 |
| `cycle` | CycleAgent | cycle-planner↔executor | 多轮自驱，`escalationFunc` 检测 `TASK_COMPLETE` 退出（maxIter=10）；code_review 模式 maxIter=5 检测 `CODE_APPROVED` |
| `graph` | GraphAgent | analyze→{code\|search\|answer}→review | 条件路由 DAG，`AddConditionalEdges` 按分类路由 |
| `team_coordinator` | Team.New | coordinator + researcher/coder/reviewer | 协调者经 AgentTool 委派，并行工具 |
| `team_swarm` | Team.NewSwarm | entry + members | 无中心，`transfer_to_agent` 转移，`WithCrossRequestTransfer` |
| `claude_code` | ClaudeCode.New | — | Claude CLI，`--permission-mode bypassPermissions`，StreamJSON 输出 |
| `codex` | Codex.New | — | Codex CLI，`--sandbox workspace-write` |
| `dify` | BuildDify | — | Dify 平台，阻塞/SSE 流式 |

子 Agent 工具过滤：`SubAgentConfig.AllTools=false && AllowedTools` 非空时仅授予列表中工具。

### 1.6 Runner 插件

| 插件 | 钩子 | 作用 |
|------|------|------|
| `toolsearch` | runner | TopK 工具过滤（默认 20），`WithMaxTools` + `WithFailOpen` |
| `guardrail` | runner | 独立轻量审查 Agent → review.New → promptinjection.New → guardrail.New |
| `todoEnforcer` | AfterAgent | 读 `temp:todos` state key，统计未完成项（仅警告） |
| `evolutionTracker` | BeforeAgent + OnEvent | 记录 `evo_start_at`/`evo_llm_calls`/`evo_tool_call_count`/`evo_tool_calls[]` |

---

## 2. 上下文管理与压缩

定义于 [context.go](../internal/agent/context.go)。`ContextRevisionEngine` 实现 Goose 风格的上下文压缩策略。

### 2.1 触发条件（shouldRevise）

满足其一即触发异步摘要：
- `estimatedTokens > maxTokens × (1.0 - TrimRatio)`
- `messageCount > 100`
- 距上次压缩超过 5 分钟

### 2.2 压缩策略

| 方法 | 策略 |
|------|------|
| `SummarizeContent` | 用 RevisionModel 生成摘要；无模型则 `truncateContent` |
| `TruncateCommandOutput` | 智能截断：保留头尾，中间插入 `[N bytes truncated]`（默认上限 8000） |
| `FilterIrrelevant` | 分两半：旧消息 LLM 摘要/占位符 + 近期消息保留 |
| `ProgressiveSummarize` | 增量合并：冷却门控 → LLM 合并（`[Existing Summary]`/`[New Messages]` 前缀）→ 失败回退 `algorithmicMerge` |
| `algorithmicMerge` | 非 LLM：拼接 + `--- Recent Activity ---` 分隔 + 截断 |

### 2.3 ContextCompaction 两阶段

- **Pass 1**：`WithContextCompactionToolResultMaxTokens`（默认 1024）— 用占位符替换旧的超大工具结果
- **Pass 2**：`WithContextCompactionOversizedToolResultMaxTokens`（推荐 8192）— 截断剩余大工具结果的头尾
- **保护近期**：`WithContextCompactionKeepRecentRequests`
- **按工具配置**：`ForceCleanToolNames`（强制清理噪声工具）/ `KeepToolNames`（排除关键工具）

### 2.4 RevisionModel 解析优先级

`revision.revision_provider` → `lightweight_provider` → `default_provider`。

`revisionModelAdapter` 内部 prompt 通过 `[Existing Summary]` 前缀自动切换"全新摘要"/"合并摘要"模式。

---

## 3. Recipe 子 Agent 系统

Recipe 是 YAML 定义的"结构化子 Agent"，从 `.wukong/recipes/*.yaml` 加载，注册为 `recipe-<name>` 工具。定义于 [recipe.go](../internal/agent/recipe.go) 等。

### 3.1 RecipeConfig 字段

| 字段 | 类型 | 说明 | 演进阶段 |
|------|------|------|---------|
| `name` | string | 唯一标识，工具名 `recipe-<name>` | 基础 |
| `description` | string | 主 Agent 选择工具的依据 | 基础 |
| `instruction` | string | 子 Agent 系统提示 | 基础 |
| `prompt` | string | Go text/template 参数化任务模板 | P0 |
| `parameters` | []RecipeParameter | 动态参数（string/number/boolean/select） | P0 |
| `response` | *RecipeResponseConfig | 结构化输出 JSON Schema | P0 |
| `retry` | *RecipeRetryConfig | 指数退避重试 | P1-B |
| `extends` | string | 继承另一个 Recipe | P2-B |
| `model` | string | 每 Recipe 独有 LLM 模型 | P3-A |
| `tools` | []string | 授予的工具名（可含 recipe 引用） | 基础/P1-A |
| `timeout` | string | 执行超时 | P3-B |

### 3.2 七阶段构建流水线

1. **Phase 1**：加载 Recipe 配置（磁盘 `.wukong/recipes/*.yaml` + 内联 `config.Agent.InlineRecipes`）
2. **Phase 2**：解析 extends 链（递归，`visiting` map 检测循环）
3. **Phase 3**：子 Recipe 依赖拓扑排序（Kahn 算法，保证确定性，检测循环依赖）
4. **Phase 4**：按序构建——模型覆盖 → 工具合并 → LLMAgent → agenttool 包装
5. **参数化包装**：`len(Parameters) > 0 && Prompt != ""` 时用 `recipeTool` 包装
6. **Retry 包装**：`recipe.Retry != nil` 时用 `retryTool` 包装
7. **Timeout 包装**：`recipe.Timeout != ""` 时用 `timeoutTool` 包装

### 3.3 辅助工具

- `list_recipes`：返回所有 Recipe 的 JSON 描述
- `reload_recipes`：手动触发磁盘重载
- `recipe_stats`：查询执行统计（CallCount/SuccessCount/TotalDuration）

### 3.4 热重载

`hotReloader` 使用 `fsnotify` 监视 Recipe 目录，500ms 去抖，监听 Create/Write/Remove/Rename 事件触发重建。

---

## 4. Provider 系统

### 4.1 统一 LLM 工厂

支持 8 种 provider 类型，定义于 [provider/factory.go](../internal/provider/factory.go)。除 ACP 外全部走 OpenAI 兼容 API：

| Type | Base URL | 实现 |
|------|----------|------|
| `openai` | api.openai.com/v1 | `openai.New` |
| `anthropic` | api.anthropic.com/v1 | `openai.New`（兼容层） |
| `google` | generativelanguage…/openai | `openai.New`（兼容层） |
| `deepseek` | api.deepseek.com | `openai.New` |
| `ollama` | localhost:11434/v1 | `openai.New` |
| `lmstudio` | localhost:1234/v1 | `openai.New` |
| `vllm` | localhost:8888/v1 | `openai.New` |
| `acp` | 自定义 agent_url | `NewACPProvider` |

### 4.2 ACP Provider

`ACPProvider` 实现 `model.Model`，对接 ACP 远程 agent（[acp.go](../internal/provider/acp.go)）：
- 提取最后一条 user 消息 → 构建 ACPRequest（含 MCPConfig.ServerURL）→ POST `/message/send`
- 工具调用映射：ACP ToolCalls → tRPC model.ToolCall
- 300s 超时，单向非流式

---

## 5. 记忆系统

记忆系统是 Wukong 的核心差异化能力，由 4 个子系统协同构成。

### 5.1 CortexStore — 混合检索（[cortex/store.go](../internal/cortex/store.go)）

**双存储架构**：词法层（`lexicalStore`）是权威数据源，所有消息先写入 `chat_recall` 表；当 embedder 可用时，额外写入 CortexDB HNSW 向量索引。两者共享同一个 `*sql.DB`。

**长消息分块向量化**：chunker 产生 >1 分块时，对每个 chunk 单独嵌入，存储为 `msg_{id}_chunk_{i}` 独立向量。

**Search 决策树**：
1. 垂直路由命中 → `mergeVertical()`
2. `IsLexicalOnly()` → 纯 FTS5
3. `IsVectorOnly()` → 纯 HNSW
4. `IsHybrid()` → 混合五步流水线

**混合检索五步流水线**（`searchHybridCortex`）：
1. FTS5 词法召回（pool = `EffectivePoolSize`，默认 50）
2. HNSW 向量召回（同样 pool 大小）
3. **融合**：
   - RRF 模式：`search.RRFFuse()`，`k = EffectiveRRFK()`，尺度不变，奖励双通道命中
   - 加权融合：`score = DenseWeight×sim + TextWeight×(1/rank)`
4. Cross-Encoder 重排（可选）：取 top-N 调用 `reranker.Rerank()` 替换融合分数
5. MMR 多样性（可选）：`search.MMRSelect()`，`lambda = EffectiveMMRLambda()`，Jaccard 文本相似度

### 5.2 MemoryFlow — 会话转录与唤醒（[cortex/memoryflow.go](../internal/cortex/memoryflow.go)）

**WakeUp 三层上下文**：
- Layer 1: Identity（智能体人格）
- Layer 2: Recalled memories（LLM 查询规划器决定 lexical/vector/hybrid）
- Layer 3: Session-level context

**PromoteFacts**：从会话转录提取候选事实 → `extractor.Extract()` → 持久化为长期记忆。

**LLM 查询规划器**（[cortex/planner.go](../internal/cortex/planner.go)）：优先 LLM 规划（单词响应，MaxTokens=8, Temperature=0.0），失败回退启发式（>10 词 → hybrid；含抽象标记 → vector；否则 lexical）。

**LLM 会话提取器**（[cortex/extractor.go](../internal/cortex/extractor.go)）：两级策略——LLM 提取（JSON 输出 `{"facts":[...]}`, Temperature=0.3, 120s 超时）→ 启发式回退（中英文关键词匹配，识别 preference/decision/note）。

### 5.3 GraphFlow — 知识图谱（[cortex/graphflow.go](../internal/cortex/graphflow.go)）

- 抽取器选择：有 JSONGenerator 时用 `LLMExtractor`，否则 `HeuristicExtractor`
- `ExtractFromTranscript`：会话文本 → `SourceDocument` → 实体/关系抽取
- `BuildGraph`：Nodes → ToolEntityInput，Edges → ToolRelationInput，经 GraphRAGTools 持久化
- `BuildContext`：SPARQL FILTER 查询（`CONTAINS(LCASE(STR(?object)), "kw")`），LIMIT 20

### 5.4 MemoryManager — 持久记忆与 SmartCleanup（[memory/store.go](../internal/memory/store.go)）

**双模式**：Auto Extract（LLM 自动提取，3 个异步 worker，600s 超时）+ Manual Tools（add/search/update/delete/load/clear）。

**SmartCleanup 四维评分**：
```
score = recency×0.4 + reference×0.3 + importance×0.2 + length×0.1
```
- Recency：`1.0 - age/maxAge`（线性衰减，maxAge 默认 365 天）
- Reference：`ReferenceCount/10.0`（上限 1.0）
- Importance：high=1.0, medium=0.5, low=0.2
- Length：`contentLen/500.0`（上限 1.0）

**淘汰策略**：容量 <80% 仅删过期；≥80% 评分淘汰至 60%。

**动态 TTL**：引用频次 ≥5 → TTL×2；≥2 → TTL×1.5；==0 → TTL×0.5。

### 5.5 向量缓存（[cortex/vector_cache.go](../internal/cortex/vector_cache.go)）

双层 LRU（messageCache + queryCache），默认 TTL=5min, maxEntries=1000，后台 goroutine 定期清理过期项。提供 hit/miss 原子计数和 HitRate 统计。

> 记忆系统详见 [记忆系统架构](MEMORY_ARCHITECTURE.md)。

---

## 6. 技能自进化引擎

定义于 [evolution/](../internal/evolution/)，LLM 驱动的闭环自我改进。

### 6.1 核心类型

- **ExecutionTrace**：技能执行完整轨迹（SkillName/ToolCalls/ErrorCount/QualityScore/Success）
- **PatchSuggestion**：LLM 补丁建议（ProblemType 枚举 5 类 + DiffContent + Confidence）
- **SkillVersion**：版本快照（BackupPath + FileHash SHA-256）

### 6.2 异步架构

`RecordExecution` 非阻塞——通过 `select-default` 向 `analysisCh`（缓冲 64）发送 trace，满则丢弃并告警。后台 `analysisWorker` goroutine 消费。

### 6.3 processTrace 全流水线

1. 跳过完美执行（`Success && ErrorCount==0 && QualityScore>0.8`）
2. 冷却检查（`CooldownPeriod`）
3. 每日限制检查（`MaxPatchesPerDay`）
4. LLM 分析（`Analyze`，60s 超时，MaxTokens=2048, Temperature=0.2）
5. 置信度门控（< `MinConfidence` 0.7 跳过）
6. 补丁大小门控（> `MaxPatchSize` 跳过）
7. `ApplyPatch` → `Refresh` 热重载

### 6.4 ApplyPatch 十步流程

1. 读取当前 SKILL.md
2. 确定新版本号
3. 创建版本备份 `SKILL.v{NNN}.md`
4. 计算 SHA-256 文件哈希
5. 追加补丁到 YAML frontmatter 之后
6. 安全验证 `validateContent`
7. 写入更新（失败则从备份恢复）
8. 记录版本到数据库
9. 修剪旧版本（`PruneOldVersions`）
10. 更新 OKF log.md / log.json

### 6.5 安全验证

- 非空检查 + 大小限制（100KB）
- **危险指令检测**（15+ 模式）：`rm -rf`、`sudo`、`system(`、`subprocess.`、`shutdown`、fork bomb 等
- **提示注入检测**（12 种模式）：`ignore previous`、`disregard prior`、`you are not`、`### system:` 等

---

## 7. 搜索系统

定义于 [search/](../internal/search/)，包含垂直路由、SPA 调优、语义分块与指标收集。

### 7.1 垂直搜索路由（[search/vertical/](../internal/search/vertical/)）

5 种意图（general/academic/code/encyclopedia/discussion）路由到 4 个后端（arXiv/GitHub/Wikipedia/Reddit）。

**IntentDetector**（[intent.go](../internal/search/vertical/intent.go)）：纯规则正则检测，零延迟、确定性，支持中英文模式。优先级 academic > code > encyclopedia > discussion。

**MergeMode**：`replace`（仅垂直）/ `prepend`（垂直优先，默认）/ `append`（本地优先）。

### 7.2 搜索调优（[search/tune/](../internal/search/tune/)）

volcengine/SearchCLI SPA 算法的轻量化实现。

**SearchGenome**：编码检索策略参数（13 字段：RecallMode/DenseWeight/TextWeight/KeywordMatchPercent/MaxRetrievedNum/FTS5PoolSize/FusionMethod/RRFK/RerankerEnabled/RerankerTopN/MMREnabled/MMRLambda）。

**Optimizer SPA 启发式**（[optimizer.go](../internal/search/tune/optimizer.go)）：
- 初始种群：baseline + KeywordOnly/SemanticOnly 边界 + DenseWeight 粗网格 + 候选大小变体
- 下一代：保留最优 + crossover + mutate + moveTowards + 随机邻域填充
- 多视角精英选择：全局最优/稳定最优/低延迟最优/基线改进者/多样性
- 退火：AnnealingStart=0.8 → AnnealingEnd=0.2

**多保真度评估**（[multifidelity.go](../internal/search/tune/multifidelity.go)）三阶段：
1. **Fast Pass**：20% 查询 + silver 标签 → 淘汰 40% 策略
2. **Middle Pass**：50% 查询 → 再淘汰 30%
3. **Confirm Pass**：100% 查询 + LLM Judge → 高置信度选择

**LLM Judge**（[llm_judge.go](../internal/search/tune/llm_judge.go)）：4 点量表（0=无关, 3=高度相关），MaxTokens=8, Temperature=0.0, 15s 超时。

**AutoTuneService**：Plan → Propose → Run → Report → Apply 闭环。安全边界：Apply 永不直接修改 live config（dry-run → confirm → candidate slot）。

### 7.3 语义分块（[search/chunking/](../internal/search/chunking/)）

五步算法：① 按段落分割 → ② 贪婪打包到 MaxSize → ③ 段落超限按句子分割 → ④ 句子超限按词分割 → ⑤ 应用 overlap 并合并小 chunk。

默认：MaxSize=1200, Overlap=200, MinSize=100。`EstimateTokens` 估算（ASCII ~4 字符/token，CJK ~1.5 字符/token）。

### 7.4 搜索指标（[search/metrics/](../internal/search/metrics/)）

线程安全收集器：NDCG@K / MRR@K / Precision@K / Recall@K + 7 桶延迟直方图（<10ms/<50ms/<100ms/<500ms/<1s/<5s/>=5s）+ 缓存命中率。

**RobustScore** 复合分数：`NDCG@20 + α×MRR@10 - β×zero_result_rate - γ×latency_penalty - δ×query_type_variance`。

---

## 8. 浏览器引擎与反爬

定义于 [browser/](../internal/browser/)。

### 8.1 双后端架构

- **chromedp**：默认后端，成熟稳定（[pool.go](../internal/browser/pool.go)）
- **go-rod**：优先使用，更现代的 CDP 封装（[rodbackend/pool.go](../internal/browser/rodbackend/pool.go)，1927 行），启动失败自动降级

`BrowserBackend` 接口（[types/types.go](../internal/browser/types/types.go)）：`Render`/`RenderWithReferer`/`SetSettle`/`StealthEnabled`/`EnableStealth`/`SetBehaviorSimulation`/`DownloadAsset`/`Screenshot`/`Close`。

### 8.2 五级反爬升级体系（[browser/antibot/](../internal/browser/antibot/)）

| 级别 | 措施 |
|------|------|
| `LevelNone` (0) | 默认行为 |
| `LevelFlags` (1) | Chrome 反检测 flag（`disable-blink-features=AutomationControlled`） |
| `LevelStealth` (2) | 完整 stealth JS 注入 + flag |
| `LevelAggressive` (3) | + 随机延迟（2-8s）+ UA 轮换（16 个浏览器配置） |
| `LevelBackoff` (4) | 指数退避，放弃当前 URL |

Cloudflare/rate-limit 场景获得 **2 倍延迟乘数**。

### 8.3 Stealth JS 注入（[browser/stealth/stealth.go](../internal/browser/stealth/stealth.go)）

覆盖 **15 个伪造类别**：webdriver 隐藏、chrome.runtime、plugins、languages、permissions、hardwareConcurrency、deviceMemory、connection RTT、screen 尺寸、canvas 噪声、WebGL vendor（8 种 GPU）、AudioContext 随机化、IntersectionObserver、Battery API、Timezone。

### 8.4 反爬探测器（[browser/antibot/prober/](../internal/browser/antibot/prober/)）

5 个探测维度并行执行（信号量 sem=3）：HTTPHeader / Robots / WAF / JSChallenge / RateLimit。

**WAF 识别**（[waf_probe.go](../internal/browser/antibot/prober/waf_probe.go)）：22 个已知 WAF 签名（Cloudflare/Akamai/AWS WAF/Imperva/DataDome 等），双重探测（正常 UA vs curl/8.0.0 挑战 UA）。

### 8.5 资产下载 4 层降级

每层使用全新 tab context（避免失败污染）：
1. 直接导航 + CDP `Network.getResponseBody`
2. `<img>` 标签加载（子资源语义）
3. CDP `Network.loadNetworkResource`（直接网络栈）
4. JS `fetch()` + base64（需 CORS）

go-rod 后端多一层：defense.gov 全分辨率 URL 变体回退。

### 8.6 行为模拟（[browser/behavior/behavior.go](../internal/browser/behavior/behavior.go)）

- **贝塞尔曲线鼠标移动**：三次插值生成自然轨迹
- **打字延迟模拟**：人类打字节奏
- **随机暂停**：模拟阅读/思考停顿

> 浏览器与反爬详见 [反反爬技术详解](ANTIBOT_GUIDE.md)。

---

## 9. 网站克隆引擎

定义于 [apps/clone/](../internal/apps/clone/)，完整爬虫 + 资产抓取 + 离线化管线。

### 9.1 EnhancedCloner 核心（[enhanced_cloner.go](../internal/apps/clone/enhanced_cloner.go)）

组合子系统：browserPool / frontier / assetDownloader / deduper / cache / robots / rateLimiter / antibot / archiveFallback。

**关键优化**：
- `rewriteAndDiscover()`：**单遍 DOM 遍历**，同时完成链接重写和页面/资源发现，替代了之前的 3 遍扫描
- `detectAndGeneratePagination()`：3 种分页模式（query-param / path-based `/page/N/` / offset-based）
- `preflightCloudflareCheck()`：Cloudflare 预检
- `runAntibotProbe()`：运行反爬探测器

### 9.2 增量克隆

- ETag/Last-Modified 条件 HEAD 请求（[cache.go](../internal/apps/clone/cache.go)）
- 速率限制：5 req/s，burst 10
- Manifest JSON 持久化

### 9.3 断点续爬

frontier JSON 状态持久化（[frontier.go](../internal/apps/clone/frontier.go)），**原子写入**（写入临时文件 + rename）保证完整性。

### 9.4 内容去重（[dedup.go](../internal/apps/clone/dedup.go)）

SHA-256 哈希 → 首次出现文件路径映射，重复文件创建**硬链接**节省磁盘空间。

### 9.5 蜜罐检测（[rewrite.go](../internal/apps/clone/rewrite.go)）

`RewriteHTML()` 基于 DOM，检测反爬陷阱：`display:none`、`visibility:hidden`、`opacity:0`、`aria-hidden`、`sr-only` 类名。懒加载解析：`data-src`/`data-lazy-src`/`data-original` → `src`。

### 9.6 平台 API 快捷路径（[platform_api.go](../internal/apps/clone/platform_api.go)）

拦截已知平台 URL，通过公开 API 获取内容（比启动浏览器快 10-50 倍）：Reddit（`.json`）/ HackerNews（Firebase API）/ GitHub（api.github.com）/ Wikipedia（REST API）/ arXiv（Atom XML）。

### 9.7 归档回退（[archive_fallback.go](../internal/apps/clone/archive_fallback.go)）

Wayback Machine 最后手段：查询 Availability API → 快照 URL 转 `id_` 变体获取原始内容（无工具栏）→ 16MB 上限 → `stripWaybackToolbar` 移除注入脚本。

> 克隆引擎详见 [网站克隆技术指南](CLONE_GUIDE.md)。

---

## 10. ZIM 归档格式

定义于 [pkg/zim/](../pkg/zim/)，实现 ZIM v6 规范（Kiwix 兼容）。

### 10.1 格式定义（[format.go](../pkg/zim/format.go)）

- Header：80 字节，Magic `0x5a 0x49 0x4d 0x04`（"ZIM\x04"）
- ArticleType：Redirect(0) / LinkFree(1) / LinkTarget(2) / Article(3)
- CompressionType：None(1) / Zstd(5)
- 命名空间：Content('C') / Metadata('M') / WellKnown('W')
- 文件布局：Header → MIME List → URL Ptr → Title Ptr → Cluster Ptr → Articles → Clusters → MD5

### 10.2 集群构建与缓存（[zim.go](../pkg/zim/zim.go)）

- `maxClusterSize = 2 MiB`/集群，文本与二进制 MIME 分离
- **增量缓存**：压缩前计算未压缩集群 SHA-256 → 缓存查找 → 命中复用已压缩字节，跳过 zstd；未命中压缩后存入缓存
- `computeUUID`：基于所有文章内容的 MD5 确定性 UUID，保证可重现构建

### 10.3 读取器（[reader.go](../pkg/zim/reader.go)）

- `Get(namespace, url)`：**二分搜索** URL 排序目录
- `blobAtIndex`：跟随重定向（最多 16 跳）
- 集群解压数据缓存（`cache map[uint32][]byte`）

### 10.4 打包（[apps/pack/packer.go](../internal/apps/pack/packer.go)）

4 种输出格式：
- **HTML**：目录复制
- **ZIM**：路径重写（stripPrefix 移除 pages/assets 前缀）+ 目录索引重定向 + 丰富元数据 + 48x48 favicon
- **Binary**：自包含可执行文件（标记 `---WUKONG_ZIM_BEGIN:{size}:...---`）
- **App**：跨平台桌面应用（macOS .app / Windows .exe / Linux AppDir）

---

## 11. 扩展与发现系统

定义于 [extension/](../internal/extension/)。

### 11.1 Manager（[manager.go](../internal/extension/manager.go)）

- `Initialize`：区分 MCP Broker 模式（多 MCP server 聚合为 4 个元工具）与独立注册模式
- `EnableExtension`/`DisableExtension`：动态启停
- 依赖注入：`SetMemoryService`/`SetCortexStore`（经接口断言避免循环依赖）
- ARD 自动注册：外部 MCP server 注册后自动生成 ARD catalog entry

### 11.2 MCP 客户端（[mcp_client.go](../internal/extension/mcp_client.go)）

三种传输：stdio（子进程）/ sse / streamable（HTTP）。工具过滤：`MCPToolFilter`（glob 包含）/ `MCPToolExclude`（排除）/ `MCPSessionReconnect`（重连，默认 3 次）。

### 11.3 MCP 服务器（[mcp_server.go](../internal/extension/mcp_server.go)）

JSON-RPC 2.0 over HTTP：
- `initialize`：返回 `protocolVersion: 2024-11-05`
- `tools/list`：返回工具元信息（带 InputSchema）
- `tools/call`：经 `guardFn` 校验后执行
- `ToolAuditLogger`（容量 10000）记录每次调用
- `MCPHealthChecker` 跟踪调用成功率

### 11.4 ACP-MCP 桥接（[acp_mcp.go](../internal/extension/acp_mcp.go)）

使 ACP agents 能通过标准 MCP 协议发现和调用 Wukong 扩展工具。请求体限制 10 MB。

### 11.5 Deep Link 路由（[deeplink.go](../internal/extension/deeplink.go)）

格式：`wukong://extension?name=xxx&type=external&transport=stdio&command=npx&args=...`，支持环境变量注入与 `${VAR}` 展开。

---

## 12. Summon 子代理委派

定义于 [summon/](../internal/summon/)。

### 12.1 Delegate（[delegate.go](../internal/summon/delegate.go)）

`.wukong/skills/*.md` 每个文件成为一个 Delegate：
- 独立 LLMAgent（`WithMaxLLMCalls(10)`, `WithMaxToolIterations(5)`, Temperature=0.3）
- 工具重试（2 次, 500ms, 2.0 退避）
- `agenttool.NewTool` 包装，`ResponseModeFinalOnly` 避免中间推理噪音
- 信号量并发限制（MaxConcurrent=5）

### 12.2 A2A 集成（[a2a.go](../internal/summon/a2a.go)）

- **A2AServer**：暴露本地 agent 为 A2A 端点，两种模式（WithAgent 自动创建 Runner / WithAgentCard+WithRunner 共享）
- **A2AAgent**：客户端代理，`a2aagent.New` 连接远程，支持流式 + 状态键传播 + 自定义认证
- `RemoteDelegateTool`：远程 A2A agent 包装为本地可调用工具

### 12.3 ANP 元协议（[meta_protocol.go](../internal/summon/meta_protocol.go)）

ANP-06 动态能力交换：
1. `GetCapabilities` 查询远程 ADP 文档
2. `selectConfiguration` 选择最优接口（优先级 MCP > A2A > YAML）
3. `sendNegotiation` 发送 JSON-RPC `anp.negotiate` 提案
4. 建立 NegotiationSession（有效期 1 小时）

### 12.4 E2EE（[e2ee.go](../internal/summon/e2ee.go)）

基于 X25519 ECDH + ChaCha20-Poly1305：
1. 解析远程 DID 文档获取 key-2 公钥
2. ECDH + HKDF-SHA256 派生 32 字节密钥
3. `chacha20poly1305.NewX()` AEAD 加密
4. 密文和 nonce base64url 编码为 EncryptedMessage

### 12.5 凭证轮换（[auth.go](../internal/summon/auth.go)）

三种认证类型自动轮换：api_key（`wak_` 前缀）/ jwt（64 字节）/ oauth2（client_credentials grant）。

---

## 13. ARD 资源发现

定义于 [ard/](../internal/ard/)。

### 13.1 CatalogEntry（[types.go](../internal/ard/types.go)）

核心资源条目：Identifier（URN `urn:air:<publisher>:<namespace>:<name>`）/ Type（IANA 媒体类型）/ URL|Data / Tags / Capabilities / RepresentativeQueries / TrustManifest。

### 13.2 RegistryServer（[server.go](../internal/ard/server.go)）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/.well-known/ai-catalog.json` | 全量目录 |
| POST | `/api/v1/search` | 语义搜索 |
| GET | `/api/v1/explore` | 探索式发现 |
| GET | `/api/v1/agents` | 分页列表 |
| GET | `/health` | 健康检查 |

### 13.3 Client 弹性（[client.go](../internal/ard/client.go)）

- CircuitBreaker（5 次失败阈值, 30s 重置）
- ResponseCache（5 分钟 TTL, SHA-256 键）
- 自动重试（2 次, 500ms）

### 13.4 联邦发现（[federation.go](../internal/ard/federation.go)）

`Federator` 跨多注册表并行搜索：BFS 遍历 Referral 链（MaxDepth=3, MaxRegistries=10），TrustPolicy 三档（Any/Known/Verified）。

### 13.5 DID:wba 身份（[did.go](../internal/ard/did.go)）

格式：`did:wba:<domain>:<path>:e1_<43-char-base64url-fingerprint>`。密钥双架构：key-1（Ed25519 签名）+ key-2（X25519 密钥协商）。三重验证：DID 指纹 → DataIntegrityProof 签名 → authentication 关系。

### 13.6 HTTP 签名（[http_sign.go](../internal/ard/http_sign.go)）

RFC 9421 实现：Content-Digest（RFC 9530, SHA-256）+ Signature-Input + Signature 头。签名参数：keyid/alg(ed25519)/created/expires(5min)/nonce（重放保护）。

---

## 14. 安全系统

定义于 [security/](../internal/security/)。

### 14.1 Guard 权限控制（[guard.go](../internal/security/guard.go)）

四种权限模式：

| 模式 | 行为 |
|------|------|
| `auto` | 全部自动批准 |
| `smart` | 高风险操作需审批（文件删除/命令执行/浏览器导航等） |
| `manual` | 所有写操作需审批 |
| `chat_only` | 仅允许对话，禁止所有工具 |

### 14.2 命令守卫（[command_tokens.go](../internal/security/command_tokens.go)）

Token 级命令分析（非子串匹配）：
- `tokenizeCommand`：分词
- `tokensToSet`：展开组合短标志（`-rf` → `-r` + `-f`），避免 `rm -rf /` 被 `rm -r -f /` 绕过
- `isGitPushForce`：检测 `git push --force`
- `isPipedToShell`：检测管道到 shell 的危险组合
- sudo 本身判定为危险

### 14.3 SSRF 防护（[ssrf.go](../internal/security/ssrf.go)）

`CheckURL` 拒绝：loopback（127.0.0.0/8）/ link-local（169.254.0.0/16，含 AWS metadata 169.254.169.254）/ private（10/172.16/192.168）/ unspecified（0.0.0.0）/ multicast（224.0.0.0/4）。

### 14.4 .wukongignore（[ignore.go](../internal/security/ignore.go)）

gitignore 兼容语法，从 cwd/home/.wukong 路径加载。`IsFileAccessTool`/`ExtractFilePathFromArgs`/`CheckFilePath` 综合路径检查。

---

## 15. 协议层实现

### 15.1 ACP 服务器（[server/acp.go](../internal/server/acp.go)）

| 方法 | 路径 | 用途 |
|------|------|------|
| POST | `/acp/message/send` | 主对话端点，SSE 流式 |
| GET | `/acp/tools/list` | Agent Card / 工具发现 |
| POST | `/acp/tools/call` | 直接工具调用（经 guardFn 校验） |
| GET | `/acp/.well-known/agent.json` | 能力声明 |
| GET | `/acp/health` | 健康检查 |

SSE 事件类型：`text_delta` / `tool_call` / `done`。

### 15.2 AG-UI 服务器（[server/agui.go](../internal/server/agui.go)）

Wukong 原生实现，轻量 SSE：单端点 `POST /agui`，10MB 请求体限制。

### 15.3 安全配置（[server/security.go](../internal/server/security.go)）

`ServerSecurityConfig`：TLS（1.2+, ECDHE 密码套件）/ Auth（api_key / jwt）/ RateLimit。`apiKeyMiddleware` 常量时间比较防时序侧信道。

---

## 16. Gateway 消息网关

定义于 [gateway/](../internal/gateway/)。

### 16.1 设计哲学

**传输无关 + 渠道自治**。Gateway 无 HTTP 监听器，每个 Channel 自带入站传输。

### 16.2 六步流水线（[gateway.go](../internal/gateway/gateway.go)）

去重 → 构建 ID → 限流（ctx 感知信号量）→ 会话映射 → 异步执行 → 回复分发。全链路 OTel 追踪。

### 16.3 飞书渠道（[gateway/feishu/](../internal/gateway/feishu/)）

- **WebSocket 长连接**（`larkws.Client`，无需公网回调 URL），SDK 处理认证/重连/心跳/分片重组
- **流式卡片**：创建交互式卡片 → 定期 patch 更新（500ms ticker）→ 完成
- **三种回复路径优先级**：ResponseURL > StreamCard > 文本
- 最大消息长度 4096 字符，API 重试 3 次

---

## 17. 会话与存储

### 17.1 三后端（[session/store.go](../internal/session/store.go)）

| 后端 | 用途 | 特点 |
|------|------|------|
| memory | 开发/测试 | 重启丢失 |
| sqlite | 单实例生产（默认） | 共享 DatabasePool 连接 |
| redis | 多实例共享 | Pipeline 批量化 + LTrim 滚动窗口 |

### 17.2 Redis 键空间（[session/redis.go](../internal/session/redis.go)）

```
wk:session:{app}:{user}:{sid}        # 前缀
  ...:events                          # LIST (JSON 事件流, RPush/LTrim)
  ...:meta                            # HASH (元数据)
wk:user_sessions:{app}:{user}         # SET (用户 session 索引)
```

`AppendEvent` Pipeline：RPush + LTrim + HSet + Expire 保证原子性。

### 17.3 DatabasePool（[util/database.go](../internal/util/database.go)）

DSN：`?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=ON&_busy_timeout=5000`，`SetMaxOpenConns(4)`/`SetMaxIdleConns(2)`。`Close()` 先 `PRAGMA wal_checkpoint(TRUNCATE)` 刷新 WAL。`MultiPool` 支持子系统使用独立数据库文件。

---

## 18. 可观测性

### 18.1 OpenTelemetry（[telemetry/telemetry.go](../internal/telemetry/telemetry.go)）

全链路追踪：`agent.RunStream` span 携带 user_id/session_id/event_count/tool_call_count/response_length。支持 gRPC/HTTP/console exporter，ParentBased + TraceIDRatioBased 采样，W3C TraceContext + Baggage 传播。

### 18.2 Langfuse（[observability/langfuse.go](../internal/observability/langfuse.go)）

LLM 专用 tracing，经 OTLP HTTP，凭证从 config 或 `LANGFUSE_*` 环境变量。

### 18.3 错误信号分类（[errsignal/errsignal.go](../internal/errsignal/errsignal.go)）

7 类错误（优先级 BotDetection > RateLimited > Permanent > AuthRequired > Invalid > Transient > Unknown），每类附带推荐处理策略（重试/退避/跳过/升级）。`RetryDelay` 指数退避 + 抖动，上限 30s。

### 18.4 健康检查（[health/health.go](../internal/health/health.go)）

K8s 兼容：liveness（恒 200）/ readiness（全 healthy 才 200）。Checker 工厂：DBChecker / ModelChecker / ExtensionChecker / A2AServerChecker。

---

## 19. 基础设施

### 19.1 配置体系（[config/](../internal/config/)）

**优先级**（高→低）：CLI flags → 环境变量（`WUKONG_` 前缀）→ YAML 文件 → 内置默认值。

**搜索路径**：`./config.yaml` → `~/.config/wukong/config.yaml` → `/etc/wukong/config.yaml`（非 Windows）。

**环境变量展开**：`${VAR}` 和 `${VAR:-default}`，`expandEnvTracked()` 返回被展开的密钥列表用于日志脱敏。

### 19.2 Code Mode（[codemode/executor.go](../internal/codemode/executor.go)）

goja 纯 Go JavaScript 引擎。安全限制：并发 5 / JSON 解析 1MB / 代码 1MB / 内存 128MB / 超时 10s。

### 19.3 OS 沙箱（[pkg/sandbox/](../pkg/sandbox/)）

跨平台文件系统沙箱：
- **Linux**：Landlock LSM（内核 5.13+），自执行助手模式，ABI 1-3 适配
- **macOS**：`sandbox-exec(1)`，`(deny file-write*)` + 显式可写目录
- **Windows**：Low Integrity Level（`S-1-16-4096`）+ Restricted Token

### 19.4 HTTP 客户端（[pkg/httpclient/](../pkg/httpclient/)）

- **DNS 回退**：系统 DNS 失败后尝试公共 DNS（Google 8.8.8.8 / Cloudflare 1.1.1.1 / Quad9 9.9.9.9）
- **TLS 指纹**：utls `HelloChrome_Auto` 模拟 Chrome ClientHello
- **速率限制**：令牌桶（per-host）+ `BlockHost` 临时封禁（默认 30s）
- 浏览器头自动设置（UA, Sec-Ch-Ua, Sec-Fetch-*）

### 19.5 TopOfMind（[topofmind/mind.go](../internal/topofmind/mind.go)）

持久指令注入，**双重检查锁定模式**（double-check locking）：RLock 检查文件修改时间 → 未变快速返回 → 变则升级 WLock → 再次检查防并发重复重载。

### 19.6 OKF（[okf/bundle.go](../internal/okf/bundle.go)）

Open Knowledge Format v0.1：YAML frontmatter + Markdown body，保留文件 index.md / log.md。Frontmatter 含 Type/Title/Description/Resource/Tags/Timestamp，`yaml:",inline"` 保留未知字段（规范要求消费者容错）。

---

## 相关文档

- [系统架构](ARCHITECTURE.md) — 分层设计与启动流程
- [记忆系统架构](MEMORY_ARCHITECTURE.md) — 双引擎三层记忆详解
- [配置手册](CONFIG.md) — 全部配置项
- [反反爬技术详解](ANTIBOT_GUIDE.md) — 浏览器与反爬
- [网站克隆技术指南](CLONE_GUIDE.md) — 克隆引擎与 ZIM
- [开发者指南](DEVELOPER_GUIDE.md) — 扩展开发
