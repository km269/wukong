# Wukong 系统架构

> 本文档基于全量源码深度扫描，描述 Wukong 的分层架构、启动流程、核心数据流与设计原则。
> 最后更新：2026-08-11

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
║  │  ┌───────────────────────────────────────────────────────────┐  │  ║
║  │  │                      CoreLoop                             │  │  ║
║  │  │  Run() → 4重上下文注入 → runner.Run() → 事件流 → 后处理   │  │  ║
║  │  │                                                          │  │  ║
║  │  │  4 重上下文注入：                                         │  │  ║
║  │  │  ├─ MemoryFlow.WakeUp()       [历史对话唤醒, 3层上下文]  │  │  ║
║  │  │  ├─ Recall/Cortex Search()    [FTS5/HNSW 跨会话检索]    │  │  ║
║  │  │  ├─ Memory.ReadMemories()     [持久记忆注入+去重]        │  │  ║
║  │  │  └─ ContextRevisionEngine     [令牌预算/异步摘要压缩]    │  │  ║
║  │  └───────────────────────────────────────────────────────────┘  │  ║
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
║  ╚══════════════════════════════════════════════════════════════════╝  ║
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

### 4.4 Agent 装配（createSingleAgent）

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

### 4.5 Agent 编排模式

| 模式 | 实现 | 说明 |
|------|------|------|
| `single` | LLMAgent | 标准 Agent + 工具调用循环（默认） |
| `chain` | ChainAgent | 顺序执行（planner→executor→reviewer） |
| `parallel` | ParallelAgent | 并发执行（code/doc/test-analyzer） |
| `cycle` | CycleAgent | 多轮自驱循环（planner↔executor 直到 `TASK_COMPLETE`） |
| `graph` | GraphAgent | 条件路由 DAG（analyze→{code\|search\|answer}→review） |
| `team_coordinator` | Team.New | 协调者通过 AgentTool 委派成员 |
| `team_swarm` | Team.NewSwarm | 群体控制转移（`transfer_to_agent`） |
| `claude_code` | ClaudeCode.New | Claude Code CLI 包装 |
| `codex` | Codex.New | OpenAI Codex CLI 包装 |
| `dify` | BuildDify | Dify 平台（阻塞/SSE 流式） |

### 4.6 Runner 插件

| 插件 | 钩子 | 作用 |
|------|------|------|
| `toolsearch` | runner | TopK 工具过滤（默认 20），降低 token 成本 |
| `guardrail` | runner | 独立轻量审查 Agent → promptinjection 检测 |
| `todoEnforcer` | AfterAgent | 检测未完成 todo（仅警告，不阻塞） |
| `evolutionTracker` | BeforeAgent + OnEvent | 记录执行轨迹（LLM/工具调用明细）供进化引擎分析 |
| `RecipeToolSet` | 工具集 | YAML Recipe 子 Agent + list/reload/stats 辅助工具 |

---

## 5. 协议层

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

### 5.1 安全中间件

所有协议端点共享安全防护（定义于 [server/security.go](../internal/server/security.go)）：
- **认证**：API Key（`subtle.ConstantTimeCompare` 防时序侧信道）或 JWT（HMAC 强制）
- **TLS**：TLS 1.2+，限定 ECDHE+AES-GCM 密码套件，可选 mTLS
- **CORS**：localhost-only（防恶意网页跨域调用，[cors.go](../internal/cors/cors.go)）
- **Guard 链**：`tools/call` 端点注入 `guardFn` 回调，镜像 Agent Loop 的 4 重校验（权限/审批/命令/文件路径）

### 5.2 Gateway 消息流水线

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

---

## 6. 记忆系统（双引擎三层）

Wukong 的记忆系统是其核心差异化能力，由 4 个子系统协同构成：

| 层级 | 子系统 | 职责 | 技术 |
|------|--------|------|------|
| 短期 | `memoryflow` | 会话级转录 + 3 层唤醒上下文 | CortexDB MemoryFlow |
| 中期 | `cortex.CortexStore` | 跨会话语义检索 | HNSW 向量 + FTS5 + RRF/MMR 融合 + Cross-Encoder 重排 |
| 长期 | `memory.MemoryManager` | 持久事实记忆 | tRPC Memory + LLM 自动提取 + SmartCleanup |
| 结构化 | `cortex.GraphFlowService` | 实体关系知识图谱 | LLM/启发式抽取 + SPARQL 查询 |

### 6.1 检索管线

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

### 6.2 SmartCleanup 四维评分

当记忆容量超 80% 时触发淘汰至 60%：

```
score = recency×0.4 + reference×0.3 + importance×0.2 + length×0.1
```

动态 TTL：引用频次 ≥5 → TTL×2；≥2 → TTL×1.5；==0 → TTL×0.5。

> 记忆系统详见 [记忆系统架构](MEMORY_ARCHITECTURE.md)。

---

## 7. 技能自进化系统

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

---

## 8. 扩展与发现系统

### 8.1 内置扩展（17 个）

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

### 8.2 Summon 子代理委派

`.wukong/skills/*.md` 每个文件成为一个 Delegate，包装为可调用工具：
- 独立 LLMAgent（MaxLLMCalls=10, MaxToolIterations=5, Temperature=0.3）
- 信号量并发限制（MaxConcurrent=5）
- 远程 A2A agent 经 `A2AAgent` 包装为 `RemoteDelegateTool`

### 8.3 ARD 双向发现

- **发布**：`RegistryServer` 暴露 `/.well-known/ai-catalog.json` + `/api/v1/search`
- **发现**：`Client`（CircuitBreaker + ResponseCache）查询远程注册表
- **联邦**：`Federator` BFS 遍历 Referral 链（MaxDepth=3, MaxRegistries=10）
- **身份**：`did:wba` 自认证身份（key-1 Ed25519 签名 + key-2 X25519 密钥协商）
- **安全**：HTTP 签名（RFC 9421）+ E2EE（X25519 ECDH + ChaCha20-Poly1305）

---

## 9. 目录结构

```
wukong/
├── cmd/                    # 编译入口
│   ├── wukong/             # 主程序 → cli.Execute()
│   ├── zim-check/          # ZIM 归档校验
│   └── zim-ls/             # ZIM 归档列举
├── internal/               # 业务代码（35+ 子系统）
│   ├── agent/              # ★ 编排层：CoreLoop + 10种编排模式 + Recipe + HITL
│   ├── cli/                # ★ CLI 命令层（Cobra）+ TUI（Bubbletea）
│   ├── config/             # 配置体系（Viper, 10 个类型文件）
│   ├── provider/           # LLM 提供商工厂（8 种）
│   ├── extension/          # ★ MCP 扩展管理 + 17个内置工具集 + ACP-MCP桥接
│   ├── browser/            # 双后端浏览器 + 5级反爬 + 15项stealth注入
│   ├── search/             # 搜索引擎（SPA调优/垂直路由/语义分块/指标）
│   ├── cortex/             # ★ CortexDB 知识引擎（向量+FTS5+GraphRAG+MemoryFlow+GraphFlow）
│   ├── recall/             # 原生 SQLite FTS5 召回（cortex 兜底）
│   ├── memory/             # 持久化记忆（tRPC Memory + SmartCleanup 四维评分）
│   ├── gateway/            # ★ 消息网关（飞书 WebSocket）
│   ├── server/             # ★ HTTP 协议端点（ACP/AG-UI/Security）
│   ├── summon/             # A2A/ANP 子Agent委派 + E2EE + 元协议协商
│   ├── ard/                # Agent 资源发现 + DID + 联邦 + HTTP签名
│   ├── session/            # 会话存储（SQLite/Redis/Memory）
│   ├── security/           # 安全防护（Guard 4模式/命令Token/SSRF/.wukongignore）
│   ├── apps/               # 应用管理（克隆/打包/消毒/MCP Apps）
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
├── pkg/                    # 可外部引用的公共库
│   ├── httpclient/         # HTTP 客户端（DNS缓存/限流/uTLS/DNS回退）
│   ├── logutil/            # 日志工具
│   ├── sandbox/            # 跨平台文件系统沙箱（Landlock/Seatbelt/Low IL）
│   └── zim/                # ZIM 归档读写（v6规范）
├── config.yaml             # 默认配置模板
├── go.mod / go.sum         # 依赖清单
└── docs/                   # 文档
```

---

## 10. 关键设计原则

1. **Composition Root 集中化**：`bootstrapSession()` 是唯一的组装点，1400+ 行完成全部依赖注入，无全局单例。

2. **接口解耦防循环**：gateway→agent 经 `AgentRunner` 接口；server→security 经 `ToolGuardCheck` 回调。

3. **多层存储共享**：同一 `wukong.db` 通过 `MultiPool.Shared()` 在 session/memory/todo/recall/cortex 间共享 `*sql.DB`，避免 SQLite 多连接冲突。子系统可经 `MultiPool.GetOrCreate()` 使用独立数据库文件。

4. **幂等关闭**：`sync.Once` 保证信号处理和 defer 不会重复关闭；严格的关闭顺序（入站→协议→扩展→CoreLoop→DB）避免数据丢失；每层 `waitWithTimeout` 防止挂起。

5. **渐进式降级**：CortexStore 失败→回退 RecallStore；MemoryFlow 失败→跳过；guardrail 失败→继续；垂直路由未命中→回退本地。几乎所有子系统初始化失败都是非致命的。

6. **多协议同源**：A2A / ACP / AG-UI / MCP 多个协议端点共享同一个 Agent 和 Runner 实例，行为一致。

7. **事件流为核心抽象**：全系统通过 `<-chan *event.Event` 解耦生产者与消费者，支持流式 delta、tool call、错误、状态增量。

8. **环境变量一等公民**：`${VAR:-default}` 语法贯穿全部含密钥配置项，`expandEnvTracked()` 返回被展开的密钥列表用于日志脱敏。

---

## 11. 相关文档

- [技术实现深度解析](TECHNICAL_IMPLEMENTATION.md) — 各子系统实现细节与算法
- [记忆系统架构](MEMORY_ARCHITECTURE.md) — 双引擎三层记忆 / 检索管线 / SmartCleanup
- [配置手册](CONFIG.md) — 全部配置项与字段说明
- [CLI & TUI 架构](CLI_TUI.md) — 命令树与终端 UI
- [部署指南](DEPLOYMENT.md) — 生产环境部署与健康检查
