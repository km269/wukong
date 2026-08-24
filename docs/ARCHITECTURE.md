# Wukong 系统架构

> 本文档基于全量源码深度扫描，描述 Wukong 的分层架构、启动流程、核心数据流、各子系统的技术实现细节与设计原则。
> 最后更新：2026-08-23

---

## 目录

1. [项目定位](#1-项目定位)
2. [分层架构](#2-分层架构)
3. [启动流程](#3-启动流程)
4. [CoreLoop — 编排引擎核心](#4-coreloop--编排引擎核心)
5. [Provider 系统](#5-provider-系统)
6. [协议层](#6-协议层)
7. [记忆系统（双引擎三层）](#7-记忆系统双引擎三层)
8. [技能自进化系统](#8-技能自进化系统)
9. [扩展与发现系统](#9-扩展与发现系统)
10. [安全系统](#10-安全系统)
11. [会话与存储](#11-会话与存储)
12. [可观测性](#12-可观测性)
13. [基础设施](#13-基础设施)
14. [目录结构](#14-目录结构)
15. [关键设计原则](#15-关键设计原则)
16. [相关文档](#16-相关文档)

---

## 1. 项目定位

**Wukong（悟空）** 是一个**本地优先（local-first）、记忆驱动、多模式编排**的开源 AI Agent 平台，构建于三大腾讯开源框架之上：

| 框架 | 版本 | 用途 |
|------|------|------|
| `trpc-agent-go` | v1.10.0 | Agent 核心引擎（LLMAgent / Runner / Planner / Session / Memory / Tool） |
| `trpc-mcp-go` | v0.0.16 | MCP（Model Context Protocol）工具协议 |
| `trpc-a2a-go` | v0.2.5 | A2A（Agent-to-Agent）通信协议 |

- **模块路径**：`github.com/km269/wukong`
- **Go 版本**：1.26
- **配置体系**：Viper（YAML + 环境变量 `${VAR}` / `${VAR:-default}` 展开 + CLI 覆盖）
- **CLI 框架**：Cobra v1.9.1
- **TUI 框架**：Charmbracelet Bubbletea v1.3.10
- **记忆引擎**：CortexDB v2.25.0（HNSW 向量 + FTS5 + GraphRAG）

---

## 2. 分层架构

```
╔═══════════════════════════════════════════════════════════════════════╗
║                          Wukong 分层架构                               ║
╠═══════════════════════════════════════════════════════════════════════╣
║                                                                        ║
║  ┌─────────────────────────────────────────────────────────────────┐  ║
║  │                    入口层 / Entry                              │  ║
║  │   cmd/wukong/main.go → cli.Execute() → Cobra root command       │  ║
║  │                                                                 │  ║
║  │   三种运行模式：                                                 │  ║
║  │   • session  → TUI 交互（Bubbletea Model-View-Update）         │  ║
║  │   • server   → Headless 多协议服务（A2A/ACP/AG-UI/ANP/MCP）    │  ║
║  │   • run      → 单次/对话式 CLI                                  │  ║
║  └────────────────────────────┬────────────────────────────────────┘  ║
║                               │                                        ║
║  ┌────────────────────────────▼────────────────────────────────────┐  ║
║  │               协议层 / Protocol Gateway                        │  ║
║  │                                                                 │  ║
║  │  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐  │  ║
║  │  │  A2A    │ │ AG-UI   │ │  ACP    │ │ ACP-MCP │ │Gateway  │  │  ║
║  │  │ :9090   │ │ :8080   │ │ :9091   │ │ :3400   │ │(飞书WS) │  │  ║
║  │  │Agent↔Agt│ │ Web SSE │ │ Client  │ │ Bridge  │ │ IM 接入 │  │  ║
║  │  └────┬────┘ └────┬────┘ └────┬────┘ └────┬────┘ └────┬────┘  │  ║
║  │       └───────────┴───────────┴────┬────┴───────────┘         │  ║
║  │                          summon/  │  ard/                      │  ║
║  │                     (A2A/ANP委派)  │ (资源发现/联邦/DID/E2EE)  │  ║
║  └────────────────────────────┬──────┴────────────────────────────┘  ║
║                               │                                        ║
║  ┌────────────────────────────▼────────────────────────────────────┐  ║
║  │             编排层 / Orchestration (agent/)                    │  ║
║  │                                                                 │  ║
║  │  ┌───────────────────────────────────────────────────────────┐  ║
║  │  │                      CoreLoop                             │  ║
║  │  │  Run() → 4重上下文注入 → runner.Run() → 事件流 → 后处理   │  ║
║  │  │                                                          │  ║
║  │  │  4 重上下文注入：                                         │  ║
║  │  │  ├─ MemoryFlow.WakeUp()       [历史对话唤醒, 3层上下文]  │  ║
║  │  │  ├─ Recall/Cortex Search()    [FTS5/HNSW 跨会话检索]    │  ║
║  │  │  ├─ Memory.ReadMemories()     [持久记忆注入+去重]        │  ║
║  │  │  └─ ContextRevisionEngine     [令牌预算/异步摘要压缩]    │  ║
║  │  └───────────────────────────────────────────────────────────┘  ║
║  │                                                                 │  ║
║  │  Agent 类型：single | chain | parallel | cycle | graph          │  ║
║  │              | team_coordinator | team_swarm                    │  ║
║  │              | claude_code | codex | dify                       │  ║
║  │                                                                 │  ║
║  │  Runner 插件：toolsearch | guardrail | todoEnforcer             │  ║
║  │               | evolutionTracker | RecipeToolSet                │  ║
║  └────────────────────────────┬────────────────────────────────────┘  ║
║                               │                                        ║
║  ┌────────────────────────────▼────────────────────────────────────┐  ║
║  │                能力层 / Capabilities                           │  ║
║  │                                                                 │  ║
║  │  extension/   MCP 扩展管理 + 17 个内置工具集                    │  ║
║  │  browser/     双后端浏览器(chromedp+rod) + 5级反爬              │  ║
║  │  apps/        网站克隆 + ZIM打包 + HTML消毒 + MCP Apps          │  ║
║  │  search/      垂直搜索 + SPA调优 + 语义分块                     │  ║
║  │  codemode/    JS 沙箱(goja)                                     │  ║
║  │  skill/       SKILL.md 技能系统                                 │  ║
║  │  evolution/   LLM 驱动技能自进化引擎                            │  ║
║  │  knowledge/   RAG 知识管理                                      │  ║
║  │  topofmind/   持久指令注入                                      │  ║
║  └────────────────────────────┬────────────────────────────────────┘  ║
║                               │                                        ║
║  ┌────────────────────────────▼────────────────────────────────────┐  ║
║  │                记忆层 / Memory (双引擎三层)                    │  ║
║  │                                                                 │  ║
║  │  memoryflow   短期：会话转录 + 3层唤醒上下文                    │  ║
║  │  cortex/      中期：CortexStore (HNSW向量 + FTS5 + RRF/MMR)    │  ║
║  │  memory/      长期：tRPC Memory (自动提取 + SmartCleanup)      │  ║
║  │  graphflow    结构化：知识图谱自动抽取 (SPARQL)                 │  ║
║  │  recall/      兜底：原生 SQLite FTS5 召回                       │  ║
║  └────────────────────────────┬────────────────────────────────────┘  ║
║                               │                                        ║
║  ┌────────────────────────────▼────────────────────────────────────┐  ║
║  │             基础设施 / Infrastructure                          │  ║
║  │                                                                 │  ║
║  │  config/      WukongConfig + Viper + ${VAR:-default} 展开      │  ║
║  │  provider/    Factory (openai/anthropic/google/deepseek/..)    │  ║
║  │  security/    Guard (权限4模式/命令Token分析/SSRF/.wukongignore)│  ║
║  │  util/        Logger(slog) + MultiPool(SQLite WAL共享)         │  ║
║  │  telemetry/   OpenTelemetry 分布式追踪 (gRPC/HTTP/console)     │  ║
║  │  observability/ Langfuse LLM 追踪                              │  ║
║  │  health/      健康检查 (/healthz /livez /readyz, K8s兼容)     │  ║
║  │  session/     会话存储 (SQLite / Redis / Memory)               │  ║
║  │  errsignal/   信号驱动错误分类 (7类)                           │  ║
║  │  cors/        CORS 中间件 (localhost-only)                     │  ║
║  ╚════════════════════════════════════════════════════════════════╝  ║
║                                                                        ║
╚════════════════════════════════════════════════════════════════════════╝
```

### 2.1 依赖规则

代码依赖严格遵循**单向向下**原则：

- `cli/` 是唯一的 Composition Root（`bootstrapSession()`），可导入所有层
- `agent/` 导入 `config/`、`provider/`、`security/`、`cortex/`、`recall/`
- `gateway/` 通过 `AgentRunner` **接口**解耦，不导入 `agent/`
- `server/` 通过 `ToolGuardCheck` 回调避免导入 `security/`（打破环依赖）
- `config/` 导入 `gateway/` 与 `server/`（嵌入配置类型），因此二者不能反向导入 config
- `cortex/` 与 `recall/` 共享同一张 `chat_recall` 表（经 `MultiPool.Shared()` 共享 `*sql.DB`）

---

## 3. 启动流程

`session`、`server`、`run` 三个命令都通过同一个核心函数 `bootstrapSession()` 完成系统初始化（约 1400 行，位于 [session.go](../internal/cli/session.go)）。

### 3.1 初始化时序

```
┌─────────────────────────────────────────────────────────────────┐
│                    bootstrapSession() 时序                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. 配置加载     config.NewLoader() → LoadAndValidate()         │
│  2. 日志级别     util.SetLogLevel()                             │
│  3. 遥测初始化   telemetry.Manager.Initialize()                 │
│  4. 内置扩展注册 builtin.RegisterBuiltins(cfg)                  │
│  5. CLI 覆盖     applyOverrides()                               │
│  6. 模型工厂     provider.NewFactory(cfg)                       │
│  7. 数据库连接池 util.NewMultiPool(db_path)                     │
│  8. 会话服务     session.NewSessionService()                    │
│  9. 记忆管理器   memory.NewMemoryManager()                      │
│ 10. 安全防护     security.NewGuard()                            │
│ 11. 扩展管理器   extMgr.Initialize()                            │
│ 12. ARD 发现     ard.NewToolSet() → PublishAndServe()           │
│ 13. ACP-MCP 桥接 extension.NewACPMCPBridge()                    │
│ 14. 独立 MCP     extension.NewMCPServerWithSecurity()           │
│ 15. 存储/召回层  CortexStore / RecallStore / MemoryFlow         │
│ 16. 能力工具集   TopOfMind / CodeMode / Apps / Summon / Todo    │
│ 17. 进化引擎     skillMgr ↔ evolutionEngine                     │
│ 18. Artifact     文件版本化服务                                 │
│ 19. Langfuse     LLM 追踪                                       │
│ 20. ★ CoreLoop   agent.NewCoreLoop(CoreLoopConfig{...})         │
│ 21. 协议服务器   A2A / AGUI / ACP / ANP / Gateway (按需启用)    │
│ 22. 健康检查     :8086 (/healthz /readyz /livez)               │
│                                                                 │
│  → 返回 (cfg, loop, BootstrapState, nil)                        │
└─────────────────────────────────────────────────────────────────┘
```

### 3.2 BootstrapState

持有所有需要在关闭时清理的资源句柄：

```go
type BootstrapState struct {
    shutdownState          // 内嵌 sync.Once 幂等守卫
    A2AServer         *summon.A2AServer
    AGUIServer        *server.AGUIServer
    ACPServer         *server.ACPServer
    MCPServer         *extension.MCPServer
    ARDRegistry       *ard.RegistryServer
    ExtMgr            *extension.Manager
    GatewayServer     *gateway.GatewayServer
    CredentialRotator *summon.CredentialRotator
    // ...
}
```

### 3.3 优雅关闭

关闭流程通过 **`sync.Once` 保证幂等**，可从信号处理器和 defer 两处安全调用：

```
shutdownBootstrap(ctx, state, loop)
  └─ state.do() [sync.Once]
       └─ runShutdown() 按严格顺序：
            ├─ 1. GatewayServer.Stop()      [先关入站消息]
            ├─ 2. 协议服务器（A2A→AGUI→ACP→MCP→ARD→ANP）
            ├─ 3. CredentialRotator.Stop()
            ├─ 4. ExtMgr.Close()            [关闭 MCP 子进程]
            ├─ 5. KnowledgeMgr.Close()
            └─ 6. CoreLoop.Close()          [最后关闭]
                 ├─ waitWithTimeout(runWg, 5s)  [等待同步后置写入]
                 ├─ waitWithTimeout(bgWg, 5s)   [等待后台 goroutine]
                 └─ closeFn():
                      ├─ runner.Close()
                      ├─ EvolutionClose()
                      ├─ MemoryClose()
                      ├─ SessionService.Close()
                      ├─ GraphFlowService.Close()
                      ├─ TelemetryShutdown(10s)
                      └─ DBPoolClose()       [数据库最后关，含 WAL checkpoint]
```

> `waitWithTimeout` 防止无响应 LLM 调用导致 `/exit` 挂起；`DatabasePool.Close()` 执行 `PRAGMA wal_checkpoint(TRUNCATE)` 保证零数据丢失。

---

## 4. CoreLoop — 编排引擎核心

CoreLoop 是整个系统的**心脏**，封装了单次 Agent 交互的完整生命周期。定义于 [loop.go](../internal/agent/loop.go)。

Wukong 采用**双层循环设计**：

- **内循环（trpc-agent-go）**：think → act → observe → decide，由 Planner 驱动
- **外循环（CoreLoop）**：上下文压缩 → 记忆注入 → 安全校验 → 后置记忆固化

### 4.1 结构

```go
type CoreLoop struct {
    agent         agent.Agent       // 单 LLMAgent 或工作流复合 Agent
    runner        runner.Runner     // tRPC Runner，驱动执行
    sessionService session.Service
    memoryService  memory.Service   // tRPC 长期记忆
    contextMgr     *ContextManager  // 上下文压缩引擎
    security       *security.Guard
    recallStore    *recall.Store
    cortexStore    *cortex.CortexStore        // 可选 HNSW 升级后端
    memoryFlow     *cortex.MemoryFlowService  // 会话转录 + 唤醒
    graphFlow      *cortex.GraphFlowService   // 知识图谱
    bgWg, runWg    sync.WaitGroup   // 后台/同步 goroutine 跟踪
    // ...
}
```

### 4.2 Run() — Prepare 阶段（4 重上下文注入）

```
用户消息进入 Run()
    │
    ├─ [1] contextMgr.PrepareContext()
    │      令牌预算检查 → 超阈值触发 sessionService.EnqueueSummaryJob 异步摘要
    │
    ├─ [2] RecallStore / CortexStore 存储：用户消息写入 chat_recall 表
    │
    ├─ [3] MemoryFlow.IngestTurn() + WakeUp()
    │      IngestTurn 记录用户轮次；WakeUp 从历史生成 3 层唤醒上下文
    │      → 注入 [Context from past conversations] + [Current user message]
    │
    ├─ [4] 主动 Recall 检索（即使未调用 recall_search 工具）
    │      FTS5/HNSW 搜索 top 5 → 注入 [Relevant conversation history]
    │
    ├─ [5] Memory.ReadMemories(userKey, 5)
    │      读取持久化记忆 → isMemoryDuplicated 做 60% 滑动窗口去重
    │      → 注入 [Remembered facts from previous conversations]
    │      → 异步 BatchRecordMemoryReferences（引用频次，供 SmartCleanup 排序）
    │
    └─ runner.Run(ctx, userID, sessionID, enrichedMessage)
         └─ 返回 <-chan *event.Event  [事件流]
```

最终注入的消息结构：`[wakeup context] + [recall history] + [persistent memories] + [User message]`

### 4.3 RunStream() — 事件消费与 Finalize 阶段

```
事件流消费（遍历通道，调用 onEvent 回调）
    │  累积 choice.Delta.Content 到 textBuilder（跳过 tool role 避免 JSON 泄露）
    │  统计 toolCallCount
    │
    ├─ [同步, runWg 保护] 存储 assistant 响应到 Recall/Cortex
    ├─ [同步] 存储 tool_call / tool_response 到 Recall（丰富未来检索）
    ├─ [同步] MemoryFlow.IngestTurn（15s 超时上限防首次 gse 字典加载阻塞）
    ├─ [异步, bgWg, 60s 超时] MemoryFlow.PromoteFacts → Memory.AddMemory [事实晋升]
    ├─ [异步] GraphFlow.ExtractFromTranscript + BuildGraph [知识图谱自动构建]
    └─ [同步] contextMgr.AfterRun() [令牌统计]
```

### 4.4 三层 Callbacks

| Callback | 钩子 | 作用 |
|----------|------|------|
| Agent Callback | Before/After agent run | 执行日志 |
| Tool Callback | BeforeTool | **安全核心**：4 重校验（权限黑白名单 → 审批模式 → 命令校验 → .wukongignore 文件路径） |
| Model Callback | AfterModel | Token usage 记录 |

### 4.5 Agent 装配（createSingleAgent）

关键配置旋钮映射（均来自 `config.AgentConfig`）：

| 配置字段 | llmagent.Option | 说明 |
|----------|-----------------|------|
| `MaxLLMCalls` | `WithMaxLLMCalls` | 单次运行 LLM 调用上限 |
| `MaxToolIterations` | `WithMaxToolIterations` | 工具迭代上限 |
| `ParallelTools` | `WithEnableParallelTools` | 并发独立工具调用 |
| `ToolRetryEnabled` | `WithToolCallRetryPolicy` | 指数退避重试 |
| `ContextCompaction` | 两阶段压缩 | Pass1: 替换旧工具结果；Pass2: 截断大结果头尾 |
| `SessionRecallEnabled` | `WithPreloadSessionRecall(5)` | 预加载历史会话 |
| `Planner=builtin` | `builtin.New` | 内置规划器（ReasoningEffort/Thinking） |
| `Planner=react` | `react.New` | ReAct 规划器 |

### 4.6 Agent 编排模式

| 模式 | 实现 | 默认子 Agent | 说明 |
|------|------|-------------|------|
| `single` | LLMAgent | — | 标准 Agent + 工具调用循环（默认） |
| `chain` | ChainAgent | planner→executor→reviewer | 顺序执行，每个接收上一个输出 |
| `parallel` | ParallelAgent | code/doc/test-analyzer | 并发执行 |
| `cycle` | CycleAgent | cycle-planner↔executor | 多轮自驱，`escalationFunc` 检测 `TASK_COMPLETE` 退出（maxIter=10）；code_review 模式 maxIter=5 检测 `CODE_APPROVED` |
| `graph` | GraphAgent | analyze→{code\|search\|answer}→review | 条件路由 DAG，`AddConditionalEdges` 按分类路由 |
| `team_coordinator` | Team.New | coordinator + researcher/coder/reviewer | 协调者经 AgentTool 委派，并行工具 |
| `team_swarm` | Team.NewSwarm | entry + members | 无中心，`transfer_to_agent` 转移，`WithCrossRequestTransfer` 支持跨请求转移 |
| `claude_code` | ClaudeCode.New | — | Claude Code CLI 包装（`--permission-mode bypassPermissions`，StreamJSON 输出） |
| `codex` | Codex.New | — | OpenAI Codex CLI 包装（`--sandbox workspace-write`） |
| `dify` | BuildDify | — | Dify 平台（阻塞/SSE 流式） |

**子 Agent 工具过滤**：`SubAgentConfig.AllTools=false` 且 `AllowedTools` 非空时，仅授予列表中的工具。

### 4.7 Runner 插件

| 插件 | 钩子 | 作用 |
|------|------|------|
| `toolsearch` | runner | TopK 工具过滤（默认 20，`WithMaxTools` + `WithFailOpen`），降低 token 成本 |
| `guardrail` | runner | 独立轻量审查 Agent（review.New → promptinjection.New → guardrail.New）→ promptinjection 检测 |
| `todoEnforcer` | AfterAgent | 读 `temp:todos` state key，检测未完成 todo（仅警告，不阻塞） |
| `evolutionTracker` | BeforeAgent + OnEvent | 记录执行轨迹（`evo_start_at`/`evo_llm_calls`/`evo_tool_call_count`/`evo_tool_calls[]`）供进化引擎分析 |
| `RecipeToolSet` | 工具集 | YAML Recipe 子 Agent + list/reload/stats 辅助工具 |

### 4.8 上下文管理与压缩

定义于 [context.go](../internal/agent/context.go)。`ContextRevisionEngine` 实现 Goose 风格的上下文压缩策略。

#### 触发条件（shouldRevise）

满足其一即触发异步摘要：

- `estimatedTokens > maxTokens × (1.0 - TrimRatio)`
- `messageCount > 100`
- 距上次压缩超过 5 分钟

#### 压缩策略

| 方法 | 策略 |
|------|------|
| `SummarizeContent` | 用 RevisionModel 生成摘要；无模型则 `truncateContent` |
| `TruncateCommandOutput` | 智能截断：保留头尾，中间插入 `[N bytes truncated]`（默认上限 8000） |
| `FilterIrrelevant` | 分两半：旧消息 LLM 摘要/占位符 + 近期消息保留 |
| `ProgressiveSummarize` | 增量合并：冷却门控 → LLM 合并（`[Existing Summary]`/`[New Messages]` 前缀）→ 失败回退 `algorithmicMerge` |
| `algorithmicMerge` | 非 LLM：拼接 + `--- Recent Activity ---` 分隔 + 截断 |

#### ContextCompaction 两阶段

- **Pass 1**：`WithContextCompactionToolResultMaxTokens`（默认 1024）— 用占位符替换旧的超大工具结果
- **Pass 2**：`WithContextCompactionOversizedToolResultMaxTokens`（推荐 8192）— 截断剩余大工具结果的头尾
- **保护近期**：`WithContextCompactionKeepRecentRequests`
- **按工具配置**：`ForceCleanToolNames`（强制清理噪声工具）/ `KeepToolNames`（排除关键工具）

#### RevisionModel 解析优先级

`revision.revision_provider` → `lightweight_provider` → `default_provider`。

`revisionModelAdapter` 内部 prompt 通过 `[Existing Summary]` 前缀自动切换"全新摘要"/"合并摘要"模式。

### 4.9 Recipe 子 Agent 系统

Recipe 是 YAML 定义的"结构化子 Agent"，从 `.wukong/recipes/*.yaml` 加载，注册为 `recipe-<name>` 工具。定义于 [recipe.go](../internal/agent/recipe.go) 等。

#### RecipeConfig 字段

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

#### 七阶段构建流水线

1. **Phase 1**：加载 Recipe 配置（磁盘 `.wukong/recipes/*.yaml` + 内联 `config.Agent.InlineRecipes`）
2. **Phase 2**：解析 extends 链（递归，`visiting` map 检测循环）
3. **Phase 3**：子 Recipe 依赖拓扑排序（Kahn 算法，保证确定性，检测循环依赖）
4. **Phase 4**：按序构建——模型覆盖 → 工具合并 → LLMAgent → agenttool 包装
5. **参数化包装**：`len(Parameters) > 0 && Prompt != ""` 时用 `recipeTool` 包装
6. **Retry 包装**：`recipe.Retry != nil` 时用 `retryTool` 包装
7. **Timeout 包装**：`recipe.Timeout != ""` 时用 `timeoutTool` 包装

#### 辅助工具

- `list_recipes`：返回所有 Recipe 的 JSON 描述
- `reload_recipes`：手动触发磁盘重载
- `recipe_stats`：查询执行统计（CallCount/SuccessCount/TotalDuration）

#### 热重载

`hotReloader` 使用 `fsnotify` 监视 Recipe 目录，500ms 去抖，监听 Create/Write/Remove/Rename 事件触发重建。

---

## 5. Provider 系统

### 5.1 统一 LLM 工厂

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

### 5.2 ACP Provider

`ACPProvider` 实现 `model.Model`，对接 ACP 远程 agent（[acp.go](../internal/provider/acp.go)）：

- 提取最后一条 user 消息 → 构建 ACPRequest（含 MCPConfig.ServerURL）→ POST `/message/send`
- 工具调用映射：ACP ToolCalls → tRPC model.ToolCall
- 300s 超时，单向非流式

---

## 6. 协议层

多协议端点共享同一个 Agent 和 Runner 实例，保证行为一致性。

| 协议 | 端口 | 用途 | 实现 |
|------|------|------|------|
| **A2A** | :9090 | Agent-to-Agent 通信 | `summon.A2AServer` |
| **AG-UI** | :8080 | Web UI SSE 流式 | `server.AGUIServer` |
| **ACP** | :9091 | Agent Client Protocol | `server.ACPServer` |
| **ACP-MCP** | :3400 | ACP↔MCP 桥接 | `extension.ACPMCPBridge` |
| **独立 MCP** | :3401 | Model Context Protocol 服务端 | `extension.MCPServer` |
| **ANP** | :9092 | Agent Network Protocol（元协议+E2EE） | `summon.MetaProtocol` |
| **Gateway** | — | IM 消息渠道（飞书 WebSocket） | `gateway.GatewayServer` |

### 6.1 ACP 与 AG-UI 端点明细

ACP 服务器（[server/acp.go](../internal/server/acp.go)）：

| 方法 | 路径 | 用途 |
|------|------|------|
| POST | `/acp/message/send` | 主对话端点，SSE 流式 |
| GET | `/acp/tools/list` | Agent Card / 工具发现 |
| POST | `/acp/tools/call` | 直接工具调用（经 guardFn 校验） |
| GET | `/acp/.well-known/agent.json` | 能力声明 |
| GET | `/acp/health` | 健康检查 |

SSE 事件类型：`text_delta` / `tool_call` / `done`。

AG-UI 服务器（[server/agui.go](../internal/server/agui.go)）：Wukong 原生实现，轻量 SSE，单端点 `POST /agui`，10MB 请求体限制。

### 6.2 安全中间件

所有协议端点共享安全防护（定义于 [server/security.go](../internal/server/security.go)）：
- **认证**：API Key（`subtle.ConstantTimeCompare` 防时序侧信道）或 JWT（HMAC 强制）
- **TLS**：TLS 1.2+，限定 ECDHE+AES-GCM 密码套件，可选 mTLS
- **CORS**：localhost-only（防恶意网页跨域调用，[cors.go](../internal/cors/cors.go)）
- **Guard 链**：`tools/call` 端点注入 `guardFn` 回调，镜像 Agent Loop 的 4 重校验（权限/审批/命令/文件路径）

### 6.3 Gateway 消息流水线

```
飞书平台 → WebSocket 长连接（larkws.Client，无需公网回调 URL）
    │  im.message.receive_v1 事件
    ▼
GatewayServer.dispatch()  [6 步流水线，全链路 OTel 追踪]
    ├─ 1. MessageDeduplicator   [按 platform:messageID 去重, 5min TTL]
    ├─ 2. Channel.BuildUserID/BuildSessionID()  [平台 ID → Wukong ID]
    ├─ 3. RateLimiter.AllowCtx()  [per-user 滑动窗口 + 全局并发信号量, ctx 感知]
    ├─ 4. GatewaySessionStore  [会话映射持久化到 SQLite]
    ├─ 5. CoreLoop.Run()  [后台 goroutine, 持有并发槽]
    └─ 6. Channel.SendReply()  [流式卡片 500ms patch / 文本 / response_url]
```

#### 飞书渠道细节（[gateway/feishu/](../internal/gateway/feishu/)）

- **WebSocket 长连接**（`larkws.Client`，无需公网回调 URL），SDK 处理认证/重连/心跳/分片重组
- **流式卡片**：创建交互式卡片 → 定期 patch 更新（500ms ticker）→ 完成
- **回复路径优先级**：ResponseURL > StreamCard > 文本
- 最大消息长度 4096 字符，API 重试 3 次

---

## 7. 记忆系统（双引擎三层）

Wukong 的记忆系统是其核心差异化能力，由 4 个子系统协同构成：

| 层级 | 子系统 | 职责 | 技术 |
|------|--------|------|------|
| 短期 | `memoryflow` | 会话级转录 + 3 层唤醒上下文 | CortexDB MemoryFlow |
| 中期 | `cortex.CortexStore` | 跨会话语义检索 | HNSW 向量 + FTS5 + RRF/MMR 融合 + Cross-Encoder 重排 |
| 长期 | `memory.MemoryManager` | 持久事实记忆 | tRPC Memory + LLM 自动提取 + SmartCleanup |
| 结构化 | `cortex.GraphFlowService` | 实体关系知识图谱 | LLM/启发式抽取 + SPARQL 查询 |

### 7.1 检索管线

```
recall_search 工具调用 → CortexStore.Search()
    │
    ├─ [垂直路由命中?] → mergeVertical()  [arXiv/GitHub/Wikipedia/Reddit]
    │
    ├─ [lexical] → FTS5 BM25 词法召回
    ├─ [vector]  → HNSW 向量召回 (+ VectorCache LRU 缓存)
    ├─ [hybrid]  → FTS5 pool + HNSW pool
    │              → RRF / 加权融合
    │              → [可选] Cross-Encoder 重排
    │              → [可选] MMR 多样性选择
    │
    ├─ [+ memory] → tRPC Memory (Score=0.5, [Memory] 前缀)
    └─ → 返回 top-K
```

### 7.2 SmartCleanup 四维评分

当记忆容量超 80% 时触发淘汰至 60%：

```
score = recency×0.4 + reference×0.3 + importance×0.2 + length×0.1
```

动态 TTL：引用频次 ≥5 → TTL×2；≥2 → TTL×1.5；==0 → TTL×0.5。

> 记忆系统详见 [记忆系统架构](MEMORY_ARCHITECTURE.md)。

---

## 8. 技能自进化系统

LLM 驱动的闭环自我改进机制（定义于 [evolution/](../internal/evolution/)）：

```
技能执行完成 → evolutionTracker 记录 ExecutionTrace
    → EvolutionEngine.RecordExecution()  [非阻塞, channel 缓冲 64]
    → analysisWorker goroutine:
        ├─ [QualityScore>0.8 且无错误?] 跳过
        ├─ [冷却期内?] 跳过  [超每日补丁上限?] 跳过
        ├─ EvolutionAnalyzer.Analyze()  [LLM 分析, 60s 超时]
        │   → 返回 {has_issue, problem_type, reason, patch, confidence}
        ├─ [Confidence < 0.7?] 跳过  [PatchSize > Max?] 跳过
        ├─ EvolutionPatcher.ApplyPatch():
        │   ├─ 备份 SKILL.v{NNN}.md
        │   ├─ 安全验证 (15+ 危险指令模式 + 12 种注入模式)
        │   ├─ 追加补丁段 (去重, maxPatchSections=5)
        │   ├─ 记录版本到 evolution_versions 表
        │   └─ 更新 OKF log.md / log.json
        ├─ SkillRefresher.Refresh()  [热重载]
        └─ store.RecordEvolution()
```

### 8.1 核心类型

- **ExecutionTrace**：技能执行完整轨迹（SkillName/ToolCalls/ErrorCount/QualityScore/Success）
- **PatchSuggestion**：LLM 补丁建议（ProblemType 枚举 5 类 + DiffContent + Confidence）
- **SkillVersion**：版本快照（BackupPath + FileHash SHA-256）

### 8.2 ApplyPatch 十步流程

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

### 8.3 补丁安全验证

- 非空检查 + 大小限制（100KB）
- **危险指令检测**（15+ 模式）：`rm -rf`、`sudo`、`system(`、`subprocess.`、`shutdown`、fork bomb 等
- **提示注入检测**（12 种模式）：`ignore previous`、`disregard prior`、`you are not`、`### system:` 等

---

## 9. 扩展与发现系统

### 9.1 内置扩展（17 个）

| 扩展 | 功能 |
|------|------|
| `developer` | 文件读写、shell 命令、ripgrep 代码搜索、目录列举 |
| `computer_controller` | 网页抓取、文件缓存、浏览器自动化 |
| `memory` | 记忆增删改查（tRPC Memory 注入） |
| `auto_visualiser` | SVG 图表、Mermaid 图、HTML 表格 |
| `tutorial` | 交互式教程 |
| `web` / `aggregate_search` | 多搜索引擎聚合（DuckDuckGo/SearXNG/Tavily/Google/Bing + 浏览器回退） |
| `agent_tools` | code-reviewer/summarizer/code-generator 子代理 |
| `apps` | HTML 应用全生命周期 + 网站克隆 + 打包 |
| `ard` | ARD 资源发现与管理 |
| `code_mode` | goja JS 沙箱执行 |
| `cortex` | 知识图谱查询、数据导入 |
| `top_of_mind` | 持久化指令注入 |
| `bing` / `google` / `searxng` / `tavily` | 单引擎搜索 |

### 9.2 Summon 子代理委派

`.wukong/skills/*.md` 每个文件成为一个 Delegate，包装为可调用工具：
- 独立 LLMAgent（MaxLLMCalls=10, MaxToolIterations=5, Temperature=0.3）
- 工具重试（2 次, 500ms, 2.0 指数退避）
- `agenttool.NewTool` 包装，`ResponseModeFinalOnly` 避免中间推理噪音
- 信号量并发限制（MaxConcurrent=5）
- 远程 A2A agent 经 `A2AAgent` 包装为 `RemoteDelegateTool`
- **凭证轮换**（[auth.go](../internal/summon/auth.go)）：三种认证类型自动轮换——api_key（`wak_` 前缀）/ jwt（64 字节）/ oauth2（client_credentials grant），由 `CredentialRotator` 统一管理（BootstrapState 持有，关闭时 Stop）

### 9.3 ARD 双向发现

- **发布**：`RegistryServer` 暴露 `/.well-known/ai-catalog.json` + `/api/v1/search`
- **发现**：`Client`（CircuitBreaker + ResponseCache）查询远程注册表
- **联邦**：`Federator` BFS 遍历 Referral 链（MaxDepth=3, MaxRegistries=10）
- **身份**：`did:wba` 自认证身份（key-1 Ed25519 签名 + key-2 X25519 密钥协商）
- **安全**：HTTP 签名（RFC 9421）+ E2EE（X25519 ECDH + ChaCha20-Poly1305）

---

## 10. 安全系统

定义于 [security/](../internal/security/)。

### 10.1 Guard 权限控制（[guard.go](../internal/security/guard.go)）

四种权限模式：

| 模式 | 行为 |
|------|------|
| `auto` | 全部自动批准 |
| `smart` | 高风险操作需审批（文件删除/命令执行/浏览器导航等） |
| `manual` | 所有写操作需审批 |
| `chat_only` | 仅允许对话，禁止所有工具 |

### 10.2 命令守卫（[command_tokens.go](../internal/security/command_tokens.go)）

Token 级命令分析（非子串匹配）：
- `tokenizeCommand`：分词
- `tokensToSet`：展开组合短标志（`-rf` → `-r` + `-f`），避免 `rm -rf /` 被 `rm -r -f /` 绕过
- `isGitPushForce`：检测 `git push --force`
- `isPipedToShell`：检测管道到 shell 的危险组合
- sudo 本身判定为危险

### 10.3 SSRF 防护（[ssrf.go](../internal/security/ssrf.go)）

`CheckURL` 拒绝：loopback（127.0.0.0/8）/ link-local（169.254.0.0/16，含 AWS metadata 169.254.169.254）/ private（10/172.16/192.168）/ unspecified（0.0.0.0）/ multicast（224.0.0.0/4）。

### 10.4 .wukongignore（[ignore.go](../internal/security/ignore.go)）

gitignore 兼容语法，从 cwd/home/.wukong 路径加载。`IsFileAccessTool`/`ExtractFilePathFromArgs`/`CheckFilePath` 综合路径检查。

---

## 11. 会话与存储

### 11.1 三后端（[session/store.go](../internal/session/store.go)）

| 后端 | 用途 | 特点 |
|------|------|------|
| memory | 开发/测试 | 重启丢失 |
| sqlite | 单实例生产（默认） | 共享 DatabasePool 连接 |
| redis | 多实例共享 | Pipeline 批量化 + LTrim 滚动窗口 |

### 11.2 Redis 键空间（[session/redis.go](../internal/session/redis.go)）

```
wk:session:{app}:{user}:{sid}        # 前缀
  ...:events                          # LIST (JSON 事件流, RPush/LTrim)
  ...:meta                            # HASH (元数据)
wk:user_sessions:{app}:{user}         # SET (用户 session 索引)
```

`AppendEvent` Pipeline：RPush + LTrim + HSet + Expire 保证原子性。

### 11.3 DatabasePool（[util/database.go](../internal/util/database.go)）

DSN：`?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=ON&_busy_timeout=5000`，`SetMaxOpenConns(4)`/`SetMaxIdleConns(2)`。`Close()` 先 `PRAGMA wal_checkpoint(TRUNCATE)` 刷新 WAL。`MultiPool` 支持子系统使用独立数据库文件。

---

## 12. 可观测性

### 12.1 OpenTelemetry（[telemetry/telemetry.go](../internal/telemetry/telemetry.go)）

全链路追踪：`agent.RunStream` span 携带 user_id/session_id/event_count/tool_call_count/response_length。支持 gRPC/HTTP/console exporter，ParentBased + TraceIDRatioBased 采样，W3C TraceContext + Baggage 传播。

### 12.2 Langfuse（[observability/langfuse.go](../internal/observability/langfuse.go)）

LLM 专用 tracing，经 OTLP HTTP，凭证从 config 或 `LANGFUSE_*` 环境变量。

### 12.3 错误信号分类（[errsignal/errsignal.go](../internal/errsignal/errsignal.go)）

7 类错误（优先级 BotDetection > RateLimited > Permanent > AuthRequired > Invalid > Transient > Unknown），每类附带推荐处理策略（重试/退避/跳过/升级）。`RetryDelay` 指数退避 + 抖动，上限 30s。

### 12.4 健康检查（[health/health.go](../internal/health/health.go)）

K8s 兼容：liveness（恒 200）/ readiness（全 healthy 才 200）。Checker 工厂：DBChecker / ModelChecker / ExtensionChecker / A2AServerChecker。

---

## 13. 基础设施

### 13.1 配置体系（[config/](../internal/config/)）

**优先级**（高→低）：CLI flags → 环境变量（`WUKONG_` 前缀）→ YAML 文件 → 内置默认值。

**搜索路径**：`./config.yaml` → `~/.config/wukong/config.yaml` → `/etc/wukong/config.yaml`（非 Windows）。

**环境变量展开**：`${VAR}` 和 `${VAR:-default}`，`expandEnvTracked()` 返回被展开的密钥列表用于日志脱敏。

### 13.2 Code Mode（[codemode/executor.go](../internal/codemode/executor.go)）

goja 纯 Go JavaScript 引擎。安全限制：并发 5 / JSON 解析 1MB / 代码 1MB / 内存 128MB / 超时 10s。

### 13.3 OS 沙箱（[pkg/sandbox/](../pkg/sandbox/)）

跨平台文件系统沙箱：
- **Linux**：Landlock LSM（内核 5.13+），自执行助手模式，ABI 1-3 适配
- **macOS**：`sandbox-exec(1)`，`(deny file-write*)` + 显式可写目录
- **Windows**：Low Integrity Level（`S-1-16-4096`）+ Restricted Token

### 13.4 TopOfMind（[topofmind/mind.go](../internal/topofmind/mind.go)）

持久指令注入，**双重检查锁定模式**（double-check locking）：RLock 检查文件修改时间 → 未变快速返回 → 变则升级 WLock → 再次检查防并发重复重载。

### 13.5 OKF（[okf/bundle.go](../internal/okf/bundle.go)）

Open Knowledge Format v0.1：YAML frontmatter + Markdown body，保留文件 index.md / log.md。Frontmatter 含 Type/Title/Description/Resource/Tags/Timestamp，`yaml:",inline"` 保留未知字段（规范要求消费者容错）。

---

## 14. 目录结构

```
wukong/
├── cmd/                    # 编译入口
│   ├── wukong/             # 主程序 → cli.Execute()
│   ├── zim-check/          # ZIM 归档校验
│   └── zim-ls/             # ZIM 归档列举
├── internal/               # 业务代码（33 个子系统）
│   ├── agent/              # ★ 编排层：CoreLoop + 10种编排模式 + Recipe + HITL
│   ├── cli/                # ★ CLI 命令层（Cobra）+ TUI（Bubbletea）
│   ├── config/             # 配置体系（Viper, 10 个类型文件）
│   ├── provider/           # LLM 提供商工厂（8 种）
│   ├── extension/          # ★ MCP 扩展管理 + 17个内置工具集 + ACP-MCP桥接
│   ├── browser/            # 双后端浏览器 + 5级反爬 + 15项stealth注入（asset/retry/sanitize 子包）
│   ├── search/             # 搜索引擎（SPA调优/垂直路由/语义分块/指标）
│   ├── cortex/             # ★ CortexDB 知识引擎（向量+FTS5+GraphRAG+MemoryFlow+GraphFlow+ImportFlow+KG工具+召回管理）
│   ├── recall/             # 原生 SQLite FTS5 召回（cortex 兜底）
│   ├── memory/             # 持久化记忆（tRPC Memory + SmartCleanup 四维评分）
│   ├── gateway/            # ★ 消息网关（飞书 WebSocket）
│   ├── server/             # ★ HTTP 协议端点（ACP/AG-UI/Security）
│   ├── summon/             # A2A/ANP 子Agent委派 + E2EE + 元协议协商 + 凭证轮换
│   ├── ard/                # Agent 资源发现 + DID + 联邦 + HTTP签名
│   ├── session/            # 会话存储（SQLite/Redis/Memory）
│   ├── security/           # 安全防护（Guard 4模式/命令Token/SSRF/.wukongignore）
│   ├── apps/               # 应用管理（clone 克隆/pack 打包/sanitize 消毒/mcpapps MCP Apps/server 本地预览）
│   ├── skill/              # SKILL.md 技能系统
│   ├── evolution/          # LLM 驱动技能自进化引擎
│   ├── knowledge/          # RAG 知识管理
│   ├── okf/                # Open Knowledge Format v0.1
│   ├── topofmind/          # 持久指令注入（双重检查锁定）
│   ├── codemode/           # JS 沙箱执行器（goja）
│   ├── telemetry/          # OpenTelemetry 追踪
│   ├── observability/      # Langfuse LLM 追踪
│   ├── health/             # 健康检查注册表（K8s 兼容）
│   ├── errsignal/          # 信号驱动错误分类（7类）
│   ├── cors/               # CORS 中间件（localhost-only）
│   ├── eval/               # 评估框架
│   ├── todo/               # 任务跟踪
│   ├── artifact/           # 制品存储（inmemory/COS）
│   ├── project/            # 工作目录追踪
│   └── util/               # 通用工具（Logger/MultiPool/Version）
├── pkg/                    # 可外部引用的公共库（5 个包）
│   ├── capability/         # 开发者工具 fs/shell 执行接缝（FileService/ShellService 接口 + 沙箱后端）
│   ├── httpclient/         # HTTP 客户端（DNS缓存/限流/uTLS/DNS回退）
│   ├── logutil/            # 日志工具
│   ├── sandbox/            # 跨平台文件系统沙箱（Landlock/Seatbelt/Low IL）
│   └── zim/                # ZIM 归档读写（v6规范）
├── config.yaml             # 默认配置模板
├── go.mod / go.sum         # 依赖清单
└── docs/                   # 文档
```

---

## 15. 关键设计原则

1. **Composition Root 集中化**：`bootstrapSession()` 是唯一的组装点，1400+ 行完成全部依赖注入，无全局单例。

2. **接口解耦防循环**：gateway→agent 经 `AgentRunner` 接口；server→security 经 `ToolGuardCheck` 回调。

3. **多层存储共享**：同一 `wukong.db` 通过 `MultiPool.Shared()` 在 session/memory/todo/recall/cortex 间共享 `*sql.DB`，避免 SQLite 多连接冲突。子系统可经 `MultiPool.GetOrCreate()` 使用独立数据库文件。

4. **幂等关闭**：`sync.Once` 保证信号处理和 defer 不会重复关闭；严格的关闭顺序（入站→协议→扩展→CoreLoop→DB）避免数据丢失；每层 `waitWithTimeout` 防止挂起。

5. **渐进式降级**：CortexStore 失败→回退 RecallStore；MemoryFlow 失败→跳过；guardrail 失败→继续；垂直路由未命中→回退本地。几乎所有子系统初始化失败都是非致命的。

6. **多协议同源**：A2A / ACP / AG-UI / MCP 多个协议端点共享同一个 Agent 和 Runner 实例，行为一致。

7. **事件流为核心抽象**：全系统通过 `<-chan *event.Event` 解耦生产者与消费者，支持流式 delta、tool call、错误、状态增量。

8. **环境变量一等公民**：`${VAR:-default}` 语法贯穿全部含密钥配置项，`expandEnvTracked()` 返回被展开的密钥列表用于日志脱敏。

---

## 16. 相关文档

- [记忆系统架构](MEMORY_ARCHITECTURE.md) — 双引擎三层记忆 / 检索管线 / SmartCleanup
- [配置手册](CONFIG.md) — 全部配置项与字段说明
- [CLI & TUI 架构](CLI_TUI.md) — 命令树与终端 UI
- [部署指南](DEPLOYMENT.md) — 生产环境部署与健康检查
- [反反爬技术详解](ANTIBOT_GUIDE.md) — 浏览器引擎与五级反爬
- [网站克隆技术指南](CLONE_GUIDE.md) — 克隆引擎与 ZIM 归档
- [开发者指南](DEVELOPER_GUIDE.md) — 扩展开发
