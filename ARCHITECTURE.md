# Wukong 系统架构文档

> **版本**: v0.6.0 | **Go**: 1.26 | **总源文件**: 106 (.go) + 31 (_test.go) | **~32,000 行**
>
> 基于 [tRPC-Agent-Go v1.10.0](https://github.com/trpc-group/trpc-agent-go) · [tRPC-MCP-Go v0.0.16](https://github.com/trpc-group/trpc-mcp-go) · [tRPC-A2A-Go v0.2.5](https://github.com/trpc-group/trpc-a2a-go)

---

## 目录

1. [分层架构](#1-分层架构)
2. [启动与核心数据流](#2-启动与核心数据流)
3. [Agent 引擎层](#3-agent-引擎层)
4. [工作流引擎](#4-工作流引擎)
5. [扩展系统](#5-扩展系统)
6. [子代理系统](#6-子代理系统)
7. [技能自进化](#7-技能自进化)
8. [安全体系](#8-安全体系)
9. [存储层](#9-存储层)
10. [上下文与记忆管理](#10-上下文与记忆管理)
11. [浏览器与 Web 系统](#11-浏览器与-web-系统)
12. [协议支持矩阵](#12-协议支持矩阵)
13. [可观测性与遥测](#13-可观测性与遥测)
14. [优雅关闭与恢复](#14-优雅关闭与恢复)
15. [模块依赖图](#15-模块依赖图)
16. [设计决策 (ADR)](#16-设计决策-adr)

---

## 1. 分层架构

```
┌──────────────────────────────────────────────────────────────────────┐
│ CLI 层 │ cmd/wukong + cli/ + tui/ │ 9 子命令                          │
│   session / run / configure / extension / eval / version / completion │
│   project / projects                                                   │
├──────────────────────────────────────────────────────────────────────┤
│ 引导层 │ cli/session.go::bootstrapSession() │ ~31 子系统串行初始化     │
├──────────────────────────────────────────────────────────────────────┤
│ 引擎层 │ agent/                                                       │
│   CoreLoop · WorkflowBuilder(10种模式) · TeamBuilder · DifyAgent      │
│   ContextRevisionEngine · HITL · TodoEnforcer · Recipe                │
│   PromptTemplateManager · AgentCallbacks · ToolCallbacks              │
├──────────────────────────────────────────────────────────────────────┤
│ 服务层 │ internal/*/                                                  │
│   Provider(7种LLM工厂,含ACP) · Extension(12内置+MCP+Broker+ACPMCPBridge)│
│   Session(sqlite/redis) · Memory(GracefulShutdown)                     │
│   Knowledge(RAG) · Recall(FTS5/Hybrid) · Artifact(inmemory/COS)        │
│   Browser(HTTP+Chromedp) · CodeMode(goja JS沙箱) · Apps(HTML管理)     │
│   Server(AG-UI SSE + ACP) · Summon(A2A+并发控制) · Skill(FSRepository) │
│   Evolution(自进化引擎) · Todo(双层任务) · TopOfMind(持久化指令)        │
│   Observability(Langfuse+OTel) · Eval · Health                         │
├──────────────────────────────────────────────────────────────────────┤
│ 存储层 │ SQLite(WAL+FTS5,共享连接池MaxOpenConns=1) + Redis + COS      │
└──────────────────────────────────────────────────────────────────────┘
```

### 1.1 目录结构

```
wukong/
├── cmd/wukong/main.go                 程序入口
├── config.yaml                        主配置文件（28段）
├── internal/
│   ├── agent/                         核心引擎层（9文件）
│   │   ├── loop.go                    CoreLoop — 主执行循环
│   │   ├── context.go                 ContextRevisionEngine — 上下文修订
│   │   ├── workflow.go                WorkflowBuilder — 10种多模式编排
│   │   ├── team.go                    TeamBuilder — 多Agent团队 + 外部CLI代理
│   │   ├── dify.go                    DifyAgent — Dify AI平台集成
│   │   ├── hitl.go                    HITL — 人机回环中断/恢复
│   │   ├── prompt_template.go         PromptTemplate — 提示词模板管理
│   │   ├── recipe.go                  Recipe — YAML配方子代理
│   │   └── todo_enforcer.go           TodoEnforcer — 任务完成强制器
│   ├── apps/manager.go               HTML应用管理 (CRUD + 文件系统)
│   ├── artifact/factory.go           制品存储（inmemory + COS双后端）
│   ├── browser/controller.go         浏览器自动化（HTTP + Chromedp CDP双模式）
│   ├── cli/                          CLI命令层（11文件）
│   │   ├── root.go                   Cobra根命令 + 9子命令注册
│   │   ├── session.go                交互TUI会话引导 + 31子系统初始化
│   │   ├── run.go                    单次执行(template/pipe/stdin) + 对话模式(-d)
│   │   ├── configure.go              配置向导
│   │   ├── extension.go              扩展管理命令
│   │   ├── project.go                项目追踪/恢复
│   │   ├── eval.go                   评估命令
│   │   ├── version.go                版本信息
│   │   └── tui/                      终端UI
│   │       ├── model.go              Bubbletea TUI模型
│   │       ├── update.go             事件更新 + 流式处理
│   │       └── view.go               视图渲染
│   ├── codemode/executor.go          goja JS沙箱执行器
│   ├── config/config.go              完整配置定义（~60KB，28段）
│   ├── eval/eval.go                  评估框架 (EvalSet/Metric/Evaluator)
│   ├── evolution/                    技能自进化系统（5文件）
│   │   ├── engine.go                 EvolutionEngine — 总控：分析通道 + 冷却期
│   │   ├── analyzer.go               EvolutionAnalyzer — LLM驱动的问题分析
│   │   ├── patcher.go                EvolutionPatcher — 备份 + 补丁应用 + 版本管理
│   │   ├── store.go                  VersionStore — SQLite版本存储 + 日限额查询
│   │   ├── types.go                  核心类型定义
│   │   └── evolution_test.go         16个测试用例
│   ├── extension/                    MCP扩展系统（8文件）
│   │   ├── manager.go                扩展管理器
│   │   ├── factory.go                内置扩展工厂
│   │   ├── types.go                  扩展类型定义
│   │   ├── manager_tools.go          扩展管理工具（4个）
│   │   ├── mcp_client.go             MCP客户端（stdio/sse/streamable）
│   │   ├── deeplink.go               Deeplink URL解析安装
│   │   ├── acp_mcp.go                ACP MCP Bridge（扩展→MCP Server透传）
│   │   └── builtin/                  内置扩展实现（11文件）
│   │       ├── registry.go           内置扩展注册表（10个）
│   │       ├── developer.go          开发者工具（file_read/write/replace/command_execute/code_search/directory_list）
│   │       ├── computer_controller.go 计算机控制器（web_fetch/file_cache/cache* + browser_*）
│   │       ├── memory.go             记忆工具（memory_add/search/delete/update/load/clear）
│   │       ├── auto_visualiser.go    自动可视化（chart/diagram/table）
│   │       ├── tutorial.go           交互式教程（start/list/step）
│   │       ├── apps.go               应用管理工具（app_create/list/get/update/delete）
│   │       ├── codemode.go           代码模式工具（code_execute + code_discover_tools）
│   │       ├── topofmind.go          首要任务工具（tom_get/set/append/clear）
│   │       ├── agent.go              子Agent工具集（code-reviewer/summarizer/code-generator）
│   │       └── web.go                Web搜索工具（DuckDuckGo/SearXNG/Tavily）
│   ├── health/health.go              健康检查
│   ├── knowledge/manager.go          RAG知识库管理
│   ├── memory/store.go               长期记忆（GracefulShutdown优雅关闭）
│   ├── observability/langfuse.go     Langfuse LLM追踪
│   ├── project/                      项目追踪
│   ├── provider/                     LLM Provider工厂
│   │   ├── factory.go                 工厂（openai/anthropic/google/deepseek/ollama/lmstudio/acp，7种）
│   │   └── acp.go                     ACP Provider（Agent Client Protocol）
│   ├── recall/                       跨会话回溯
│   │   ├── store.go                  FTS5存储 + 索引 + Hybrid模式
│   │   └── tool.go                   回溯工具（recall_search + recall_sessions）
│   ├── security/                     安全防护
│   │   ├── guard.go                  4级权限 + 12种命令拦截 + 恶意软件扫描
│   │   └── ignore.go                 .wukongignore 文件黑名单
│   ├── server/                       服务器层
│   │   ├── agui.go                    AG-UI SSE 服务器
│   │   └── acp.go                     ACP Server 端点
│   ├── session/                      会话管理（sqlite/redis/memory）
│   ├── skill/manager.go              Agent Skill 仓库（FSRepository + EvolutionHook）
│   ├── summon/                       子代理调度
│   │   ├── delegate.go               子代理委托 + 并发控制
│   │   ├── a2a.go                    A2A远程代理
│   │   └── auth.go                   A2A认证（JWT/APIKey/OAuth2）
│   ├── telemetry/                    OTel分布式追踪
│   ├── todo/tool.go                  任务跟踪（5+1双层工具 + TodoEnforcer）
│   ├── topofmind/mind.go             持久化指令注入 + 自动热重载
│   └── util/                         DB池(WAL) · Logger(slog) · Ptr辅助
```

---

## 2. 启动与核心数据流

### 2.1 系统启动流程 (bootstrapSession)

`bootstrapSession()` 是系统启动的中枢函数，按顺序初始化约 31 个子系统：

```
 1. config.NewLoader()               → 加载配置（Viper + YAML + ENV + CLI flags）
 2. validateConfig()                 → 配置验证 + 兼容性警告
 3. telemetry.NewManager()           → OpenTelemetry 初始化
 4. builtin.RegisterBuiltins()       → 注册 10 个内置扩展
 5. applyOverrides()                 → CLI 参数覆盖配置
 6. provider.NewFactory()            → LLM Provider 工厂（7种）
 7. util.NewDatabasePool()           → 共享 DB 连接池（WAL, MaxOpenConns=1）
 8. session.NewSessionService()      → 会话服务（sqlite/redis/memory）
 9. memory.NewMemoryManager()        → 记忆管理器（GracefulShutdown）
10. security.NewGuard()              → 安全守卫（4级权限）
11. extension.NewManager()           → 扩展管理器
12. extMgr.Initialize()              → 初始化所有启用扩展（内置+MCP+Broker+ACPMCPBridge）
13. extMgr.SetMemoryService()        → 注入记忆服务到扩展
14. extension.NewManagerToolSet()    → 扩展管理工具（extension_*）
15. recall.NewStore()                → FTS5 回溯存储
16. topofmind.NewManager()           → 首要任务管理器
17. codemode.NewExecutor()           → goja JS 沙箱执行器
18. apps.NewManager()                → HTML 应用管理器
19. builtin.NewAgentToolSet()        → 子 Agent 工具（code-reviewer/summarizer/code-generator）
20. summon.NewSummonManager()        → 子代理调度管理器
21. summonMgr.LoadSkills()           → 加载 Summon Skill 定义
22. skill.NewManager().Initialize()  → Agent Skill 系统（FSRepository）
23. Evolution Engine 初始化           → 技能自进化系统（配置 evolution.enabled）
     ├─ skillMgr.SetEvolutionHook()  → 注入进化钩子
     └─ evoEngine.SetRefresher()     → 设置热刷新回调
24. todo.NewStore()                  → 任务存储（SQLite）
25. knowledge.NewManager()           → RAG 知识库
26. factory.CreateRevisionModel()    → 上下文修订模型
27. artifact.NewService()            → 制品服务（inmemory/COS）
28. observability.StartLangfuse()    → Langfuse LLM 追踪
29. agent.NewCoreLoop()              → 创建核心循环
30. project.NewManager()             → 项目追踪
31. [可选] summon.NewA2AServer()     → A2A 服务启动
32. [可选] server.NewAGUIServer()    → AG-UI SSE 服务启动
33. [可选] server.NewACPServer()     → ACP 协议服务启动
```

### 2.2 TUI 模式 (`wukong session`)

```
用户输入 → TUI (Bubbletea)
  → CoreLoop.RunStream(ctx, userID, sessionID, msg)
    ├─ OTel Trace Span 开始
    ├─ ContextManager.PrepareContext()       Token 预算检查 + 异步摘要触发
    ├─ RecallStore.StoreMessage(user)         写入 FTS5 索引
    └─ Runner.Run() → 工具循环 → 流式事件 → TUI 渲染
```

### 2.3 单次执行模式 (`wukong run -m "..."`)

```
CLI参数/stdin/管道 → resolveInput() → bootstrapSession()
  → runOneShot() → RunStream() → streamToStdout() → 输出 → 退出
```

### 2.4 对话模式 (`wukong run -d`)

```
bootstrapSession() → 自动生成 sessionID
  → runDialogue() → REPL 循环
    ├─ "> " 提示 → 读取输入 → /exit 退出
    ├─ runOneShot() → 流式输出到 stdout
    └─ 同一 sessionID 保持上下文中
```

### 2.5 完整执行链路

```
用户输入 → TUI / runOneShot / Dialogue REPL
  → CoreLoop.RunStream(ctx, userID, sessionID, msg)
    ├─ OTel Trace Span 开始
    ├─ ContextManager.PrepareContext()          Token 预算检查 + 异步摘要触发
    ├─ RecallStore.StoreMessage(user)            写入 FTS5 索引
    ├─ Runner.Run()                              tRPC-Agent-Go 运行器
    │   ├─ Session 历史 + System Instruction
    │   │   ├─ Prompt Templates (自定义 .md 模板)
    │   │   ├─ TopOfMind (持久化指令)
    │   │   ├─ PreloadMemory (最多10条记忆预加载)
    │   │   └─ PreloadSessionRecall (跨会话上下文)
    │   ├─ LLM Agent (Planner + GenConfig)
    │   │   ├─ [可选] BuiltinPlanner (thinking模型: Claude/Gemini/OpenAI o-series)
    │   │   ├─ [可选] ReActPlanner (标签引导: DeepSeek/Ollama/LMStudio)
    │   │   └─ [可选] ToolSearch (TopK工具筛选)
    │   ├─ 工具循环执行:
    │   │   ├─ BeforeTool Callback → 安全检查 (权限/命令/.wukongignore)
    │   │   ├─ 工具执行 (并行/串行)
    │   │   │   ├─ Function Tools — 直接函数调用
    │   │   │   ├─ Skill Agents — 子代理工具（带Evolution Hook）
    │   │   │   └─ MCP Tools — 通过 Broker 或直接客户端调用
    │   │   ├─ [可选] ToolRetry (自动重试 + 指数退避: 3次, 1s初始, 2.0因子)
    │   │   ├─ [可选] JSONRepair (修复非标准JSON工具参数)
    │   │   ├─ AfterTool Callback → 结果监控
    │   │   │   ├─ [可选] ContextCompaction (两遍压缩)
    │   │   │   └─ [可选] PostToolPrompt (工具后提示)
    │   │   └─ [Evolution] 技能执行后轨迹采集（异步）
    │   ├─ TodoEnforcer → 检查待办任务完成状态
    │   ├─ Guardrail → [可选] Prompt注入检测
    │   └─ <-chan *event.Event → 流式事件通道
    ├─ 遍历事件流 → 文本聚合 + 工具调用统计
    ├─ RecallStore.StoreMessage(assistant)        写入助手回复
    ├─ ContextManager.AfterRun() → Token更新 + 摘要触发评估
    └─ OTel Span 结束 (event_count, tool_call_count, response_length)
```

---

## 3. Agent 引擎层

### 3.1 CoreLoop 核心循环

```
CoreLoop
├── agent (LLMAgent/ChainAgent/ParallelAgent/...)  ← 根据 workflow.mode 创建
├── runner.Runner (tRPC-Agent-Go)                   ← 标准运行器
├── contextMgr (ContextRevisionEngine)              ← Token预算 + 上下文修剪
├── security.Guard (4层权限 + 命令拦截)              ← 安全守卫
├── recallStore (FTS5回溯)                          ← 跨会话搜索
├── promptTemplateMgr (目录.md模板)                 ← 自定义系统提示词
├── todoTool (双层: SQLite + tRPC原生)              ← 任务跟踪
├── topOfMindMgr (持久化指令)                       ← 始终注入的指令
├── recipeBuilder (YAML配方子代理)                   ← 可组合子代理
└── closeFn (6步关闭链)                             ← 优雅关闭
```

**核心配置**：
| 参数 | 默认值 | 说明 |
|------|--------|------|
| `MaxLLMCalls` | 50 | 每次运行最大 LLM 调用次数（0=不限） |
| `MaxToolIterations` | 30 | 最大工具迭代次数 |
| `MaxRunDuration` | 300s | 单次运行超时 |
| `ParallelTools` | true | 并行工具执行 |
| `Streaming` | true | 流式输出 |
| `Temperature` | 0.7 | 采样温度 |
| `MaxTokens` | 4096 | 单次最大输出 token |
| `ToolRetry` | 3次, 1s初始, ×2.0退避 | 自动重试 + 指数退避 |
| `JSONRepair` | false | 修复非标准 JSON |
| `ContextCompaction` | true | 两遍压缩（占位符 + 截断） |
| `SessionRecall` | false | 跨会话上下文预加载 |

### 3.2 ContextRevisionEngine 上下文修订引擎

**三层修订策略**：
1. **异步摘要触发**: 通过 Session Service 的 `EnqueueSummaryJob` 在后台执行
2. **Token 预算管理**: 64000 max_tokens / 0.3 trim_ratio
3. **命令输出截断**: 8000 字节限制，保留首尾部分

**触发条件（三重检查）**：
- 估算 Token 超过阈值：`max_tokens × (1 - trim_ratio)`
- 消息数超过 100 条
- 距上次修订超过 5 分钟

### 3.3 Planner 配置

| Planner | 适用模型 | 工作机制 |
|---------|---------|---------|
| `builtin` | Claude/Gemini/OpenAI o-series | 原生 thinking 模式，支持 ReasoningEffort/ThinkingTokens |
| `react` | DeepSeek/Ollama/LMStudio | 通过 `/*PLANNING*/` `/*REASONING*/` `/*ACTION*/` XML 标签引导 |
| 空（默认） | 所有模型 | 不启用 planner，直接工具调用 |

### 3.4 回调体系

三层回调注册：

**AgentCallbacks (BeforeAgent/AfterAgent)**:
- 日志记录、调用统计
- **Evolution Hook**: 技能子代理执行后，AfterAgent 回调自动采集执行轨迹

**ToolCallbacks (BeforeTool/AfterTool)**:
- 安全校验：权限检查、命令拦截
- `.wukongignore` 文件路径黑名单检查
- 工具结果大小监控

**ModelCallbacks (BeforeModel/AfterModel)**:
- Token 使用追踪
- 请求/响应监控

---

## 4. 工作流引擎

Wukong 支持 10 种工作流模式，通过 `workflow.mode` 配置切换：

| # | 模式 | 实现 | 说明 |
|---|------|------|------|
| 1 | `single` | `llmagent.New()` | 标准单 Agent，带完整配置链 |
| 2 | `chain` | `chainagent.New()` | 顺序流水线（planner→executor→reviewer） |
| 3 | `parallel` | `parallelagent.New()` | 并发多专家（code/doc/test分析） |
| 4 | `cycle` | `cycleagent.New()` | 迭代优化（default/code_review） |
| 5 | `graph` | `graphagent.New()` | 条件路由DAG（analyze→code/search/answer→review） |
| 6 | `team_coordinator` | `team.New()` | 协调者 + AgentTool 委托成员 |
| 7 | `team_swarm` | `team.NewSwarm()` | Agent直接transfer，独立成员历史，20次handoff限制 |
| 8 | `claude_code` | `claudecode.New()` | 本地 Claude Code CLI 包装 |
| 9 | `codex` | `codex.New()` | 本地 Codex CLI 包装（sandbox workspace-write） |
| 10 | `dify` | `DifyAgent`（自研） | Dify Chat API（blocking + SSE流式） |

### 4.1 各模式详解

**Single 模式**（默认）:
- 完整的单 Agent 配置链：模型选择 → 工具注册 → Planner 配置 → 回调注册
- 支持所有 Agent 配置特性（记忆预加载、上下文压缩、会话回溯、工具搜索等）

**Chain 模式**:
- 默认 3 Agent 流水线：planner → executor → reviewer
- 可通过 `workflow.sub_agents` 自定义子 Agent 和工具权限

**Parallel 模式**:
- 默认 3 专家并行：code-analyzer、doc-analyzer、test-analyzer
- 各专家独立执行，结果合并

**Cycle 模式**:
- `default`: planner ↔ executor 循环，TASK_COMPLETE 关键字退出
- `code_review`: generator ↔ reviewer 循环，CODE_APPROVED 关键字退出

**Graph 模式**:
- analyze 节点分类路由 → code/search/answer → review 汇聚
- 支持 StateGraph + 条件边 + 状态 Schema
- 可选引擎：bsp、dag

**Team 模式**:
- **Coordinator**: 协调者通过 AgentTool 委托给成员（researcher/coder/reviewer）
- **Swarm**: Agent 通过 transfer_to_agent 直接传递控制权，24次 handoff 限制

---

## 5. 扩展系统

### 5.1 扩展管理架构

```
extension.Manager
├── 内置扩展工厂 (factory.go)
│   ├── CreateBuiltinToolSet(name, cfg) → tool.ToolSet
│   │   ├── developer          → DeveloperToolSet (6工具)
│   │   ├── computer_controller → ComputerControllerToolSet (9工具)
│   │   ├── memory             → MemoryToolSet (6工具, 延迟注入)
│   │   ├── auto_visualiser    → VisualiserToolSet (3工具)
│   │   ├── tutorial           → TutorialToolSet (3工具)
│   │   ├── web                → WebToolSet (DuckDuckGo/SearXNG/Tavily)
│   │   ├── agent_tools        → AgentToolSet (3子Agent, 延迟创建)
│   │   ├── apps               → nil (延迟注入, bootstrapSession创建)
│   │   ├── code_mode           → nil (延迟注入)
│   │   └── top_of_mind         → nil (延迟注入)
│   └── 外部MCP客户端 (mcp_client.go)
│       ├── stdio    → npx/uvx 子进程通信
│       ├── sse      → HTTP SSE 长连接
│       └── streamable → HTTP 流式传输
└── MCP Broker (mcpbroker.New())
    └── 按需工具发现: mcp_list_servers / mcp_list_tools / mcp_call
```

### 5.2 内置扩展清单

#### 功能性扩展（6个）

| 扩展 | 文件 | 工具数 | 默认启用 |
|------|------|--------|---------|
| **Developer** | `builtin/developer.go` | 6 | ✅ |
| **Computer Controller** | `builtin/computer_controller.go` | 9 | ✅ (联动browser.enabled) |
| **Memory** | `builtin/memory.go` | 6 | ✅ |
| **Auto Visualiser** | `builtin/auto_visualiser.go` | 3 | ✅ (联动visualiser.enabled) |
| **Tutorial** | `builtin/tutorial.go` | 3 | ✅ (联动tutorial.enabled) |
| **Web** | `builtin/web.go` | 1 | ✅ |

#### 平台扩展（6个）

| 扩展 | 工具数 | 默认启用 |
|------|--------|---------|
| **Apps** | 5 | ✅ |
| **Chat Recall** | 2 | ✅ |
| **Code Mode** | 2 | ✅ |
| **Extension Manager** | 4 | ✅ (始终启用) |
| **Summon** | 3+ | ✅ |
| **Todo** | 5+1 | ✅ |
| **Top of Mind** | 4 | ✅ |

### 5.3 外部 MCP 服务器配置

支持三种传输协议：
- **stdio**: 通过子进程通信（npx/uvx/pip 等）
- **sse**: HTTP Server-Sent Events
- **streamable**: HTTP 流式传输

高级特性：
- **Tool Filter**: glob 模式工具包含/排除（`mcp_tool_filter`/`mcp_tool_exclude`）
- **Session Reconnect**: 自动重连（`mcp_session_reconnect`，最多 3 次）
- **MCP Broker**: 按需工具发现，避免工具列表臃肿
- **Deeplink**: `wukong://extension?name=...` 一键安装
- **Env Overrides**: 为 MCP 子进程设置自定义环境变量

---

## 6. 子代理系统

Wukong 有三层子代理机制，从轻到重：

### 6.1 三层子代理对比

| 层级 | 实现 | 定义格式 | 存储位置 | 执行方式 | 并发控制 |
|------|------|---------|---------|---------|---------|
| **AgentToolSet** | `builtin/agent.go` | 硬编码 | 代码内 | LLMAgent 子代理 | 无 |
| **Summon** | `summon/delegate.go` | `.md` 文件 | `.wukong_skills/` | LLMAgent 子代理 | Semaphore(5) |
| **Skill** | `skill/manager.go` | `SKILL.md` YAML+Markdown | `.wukong_agent_skills/` | LLMAgent (FSRepository) | 无 |

### 6.2 AgentToolSet（内置子代理）

| 名称 | Temperature | MaxTokens | MaxLLMCalls | 用途 |
|------|------------|-----------|-------------|------|
| `code-reviewer` | 0.3 | 2048 | 3 | 代码质量审查专家 |
| `summarizer` | 0.3 | 1024 | 2 | 内容摘要专家 |
| `code-generator` | 0.2 | 4096 | 3 | 代码生成专家 |

### 6.3 Summon 子代理调度

- 从 `.wukong_skills/` 加载 Skill 定义（.md 文件）
- 每个 Skill 自动创建为子 Agent 工具
- 并发控制：`summon.max_concurrent` 信号量（默认 5）
- 支持 A2A 远程代理（JWT/APIKey/OAuth2 认证）

### 6.4 Skill 系统（FSRepository）

- 基于 tRPC-Agent-Go 的 `FSRepository` 格式
- `SKILL.md`: YAML front matter (name, description) + Markdown 指令体
- 自动加载 + 运行时热刷新（`Refresh()`）
- 每个 Skill 作为独立 LLMAgent 运行（Temperature=0.3, MaxTokens=2048, MaxLLMCalls=10）
- **支持 Evolution Hook**: 执行后自动采集轨迹用于技能进化

### 6.5 Recipe 配方子代理

- 从 `.wukong/recipes/*.yaml` 加载 YAML 定义
- 支持自定义 name、description、instruction、allowed_tools
- 通过 `summonMgr.WrapTool()` 注入并发控制
- 与 Summon Skill 共享工具包装机制

---

## 7. 技能自进化

### 7.1 进化闭环

```
技能执行 → AfterAgent回调捕获轨迹 → 异步分析通道 → LLM分析问题
  → 生成PatchSuggestion → 安全校验 → 备份 SKILL.vNNN.md
  → 追加补丁到 SKILL.md → SQLite记录版本 → 触发 Refresh() → 下次使用新版
```

### 7.2 模块组成

| 文件 | 职责 |
|------|------|
| `evolution/types.go` | ExecutionTrace、PatchSuggestion、EvolutionRecord、SkillVersion 等核心类型 |
| `evolution/engine.go` | EvolutionEngine: 异步 worker + 冷却期控制 + 日限额检查 |
| `evolution/analyzer.go` | EvolutionAnalyzer: 构造 Prompt → 调用 LLM → 解析 JSON → 生成 PatchSuggestion |
| `evolution/patcher.go` | EvolutionPatcher: 备份原文件 → SHA256 哈希 → YAML 追加 → 写入 → 清理旧版 |
| `evolution/store.go` | VersionStore: 2 张 SQLite 表（evolution_history + evolution_versions） |

### 7.3 安全控制

| 机制 | 配置项 | 默认值 |
|------|--------|--------|
| 冷却期 | `evolution.cooldown_period` | 30m |
| 日限额 | `evolution.max_patches_per_day` | 10 |
| 置信度阈值 | `evolution.min_confidence` | 0.7 |
| 补丁大小限制 | `evolution.max_patch_size` | 8192 字节 |
| 版本保留数 | `evolution.max_versions_kept` | 10 |
| 分析超时 | `evolution.analysis_timeout` | 60s |
| 自动/手动模式 | `evolution.auto_patch` | false（仅记录建议） |

### 7.4 集成方式

- `skill.Manager` 持有 `SkillEvolutionHook` 接口
- 在 `CreateSkillAgent()` 中注册 AfterAgent 回调
- `EvolutionEngine` 实现该接口，异步处理轨迹
- 补丁应用后触发 `skill.Manager.Refresh()` 热加载新版 SKILL.md
- EVOAdapter（`cli/session.go`）处理跨包类型转换，避免循环导入

---

## 8. 安全体系

### 8.1 4 层权限模型

```
Permission Mode:
  auto      → 所有工具自动执行，无需审批
  smart     → 仅高风险操作需要审批（默认，推荐）
  manual    → 每次工具调用都需要审批
  chat_only → 禁止所有工具，仅文本交互
```

### 8.2 高风险工具清单（smart模式需审批）

- **命令执行**: `bash`, `execute_command`, `run_command`, `shell`, `terminal`, `command`, `command_execute`
- **文件操作**: `file_write`, `file_replace`, `file_delete`
- **浏览器**: `browser_navigate`, `browser_screenshot`, `browser_click`, `browser_fill`
- **Web**: `web_fetch`

### 8.3 六层安全机制

| 层级 | 机制 | 说明 |
|------|------|------|
| 1 | Allowlist/Denylist | 工具级别白名单/黑名单（* 通配符） |
| 2 | 命令模式拦截 | 内置危险命令列表（rm -rf /, dd, mkfs, fork bomb） |
| 3 | 恶意软件扫描 | 外部扩展命令/参数扫描（12种可疑模式） |
| 4 | Guardrail | tRPC Prompt Injection 检测（可选，增加延迟） |
| 5 | .wukongignore | gitignore 兼容语法文件访问黑名单 |
| 6 | ToolPermission | 扩展级别细粒度工具权限控制 |

### 8.4 Guardrail 提示注入检测

```
用户输入 → guardrail-reviewer Agent (Temperature 0.0, MaxTokens 256)
  → promptinjection.Reviewer 审查
    → 通过: 继续正常 Agent 流程
    → 拒绝: 返回安全警告
```

---

## 9. 存储层

### 9.1 共享数据库池

```
wukong.db (WAL模式, MaxOpenConns=1)
├── Session Service (tRPC sqlite session.Service)  — 会话事件 + 摘要
├── Memory Service (tRPC sqlite memory.Service)     — 长期记忆存储
├── Todo Store (自定义 SQLite)                      — 任务跟踪表
├── Recall Store (FTS5 全文索引)                    — 跨会话搜索
├── Project Store                                    — 项目追踪数据
└── Evolution Store (SQLite)                         — 进化历史 + 版本管理
```

### 9.2 会话存储

| 后端 | 特性 |
|------|------|
| **sqlite** | 默认，WAL 模式，事件限制 500，自动摘要触发 50 |
| **redis** | 分布式部署，go-redis/v9 |
| **memory** | 测试/开发用，无持久化 |

### 9.3 会话回溯 (FTS5/Hybrid)

| 搜索模式 | 说明 |
|---------|------|
| `fts5` | 纯 FTS5 全文搜索（默认，零配置） |
| `hybrid` | 语义搜索 + FTS5 混合排序（需 embedding provider） |

- 每条用户/助手消息自动 FTS5 索引
- Hybrid 模式支持重排序（ReRanker）
- 支持按 Session ID 过滤

---

## 10. 上下文与记忆管理

### 10.1 长期记忆

**自动提取**:
- 3 个异步 worker，LLM 自动从对话中提取结构化记忆
- 可配置独立提取模型（如 deepseek-chat）降低成本
- 支持自定义提取 Prompt（适合本地小模型精简版）

**手动工具**（6 个 tRPC 标准记忆工具）:
- `memory_add`, `memory_search`, `memory_delete`, `memory_update`, `memory_load`, `memory_clear`

**自动预加载**: 每次对话开始将最多 10 条用户记忆注入系统提示词

**优雅关闭**: WaitGroup + 5s 超时 + isClosing 标志拒绝新任务

### 10.2 上下文压缩

两遍压缩策略：

**Pass 1 — 占位符替换**:
- 将旧的超大工具结果替换为内容占位符标记
- 可通过 `force_clean_tools` 强制指定工具
- 保护最近 N 个请求（`keep_recent`）

**Pass 2 — 内容截断**:
- 对剩余超大结果进行首尾截断
- 可通过 `keep_tools` 指定始终保留结果的工具

---

## 11. 浏览器与 Web 系统

### 11.1 双模式浏览器引擎

```
browser/controller.go
├── HTTP 模式 (net/http, 默认/回退)
│   └── web_fetch, file_cache
└── Chromedp 模式 (CDP协议, 真实浏览器)
    ├── browser_navigate → 导航+提取页面内容
    ├── browser_extract → 提取清洁文本
    ├── browser_screenshot → 保存HTML快照
    ├── browser_click → 点击元素 (CSS选择器)
    └── browser_fill → 填充表单 (CSS选择器+值)
```

### 11.2 安全特性

- `allocCancel` 保存，Close 时杀浏览器进程（修复僵尸 Chrome 泄漏）
- 视口配置：1280×720 headless chromium

### 11.3 Web搜索

| 后端 | 配置项 | 说明 |
|------|-------|------|
| **DuckDuckGo** | 默认 | tRPC 内置，无需 API Key |
| **SearXNG** | `search_backend_url` | 自托管搜索实例 |
| **Tavily** | `search_api_key` | 需要 API Key |

---

## 12. 协议支持矩阵

### 12.1 ACP（代理客户端协议）架构

```
┌────────────────────────────────────────────────────────────┐
│ ACP Provider (provider/acp.go)                              │
│ 将 ACP 兼容代理作为 LLM Provider 使用                        │
│   providers[].type = "acp"                                  │
│   → POST http://agent:4000/message/send                    │
│   → 响应转换为 tRPC model.Response                          │
├────────────────────────────────────────────────────────────┤
│ ACP Server (server/acp.go)                                  │
│ 让 ACP 兼容客户端原生连接到 Wukong                           │
│   POST /acp/message/send  — 用户消息 + SSE 流式响应         │
│   GET  /acp/tools/list    — Agent Card + 工具列表           │
│   POST /acp/tools/call    — 直接工具调用                    │
│   GET  /acp/.well-known/agent.json — 能力发现               │
│   GET  /acp/health        — 健康检查                        │
├────────────────────────────────────────────────────────────┤
│ ACP MCP Bridge (extension/acp_mcp.go)                       │
│ 将 Wukong 扩展透传为 MCP Server 供 ACP 代理调用             │
│   POST /mcp  — JSON-RPC: tools/list, tools/call             │
│   → 遍历 extension.Manager.ToolSets()                       │
│   → tool.Declaration() → MCP Tool Schema                    │
│   └─ 转发调用到 tool.CallableTool                           │
└────────────────────────────────────────────────────────────┘
```

### 12.2 协议支持矩阵

| 协议 | Provider（客户端） | Server（服务端） | 工具透传 | 流式 |
|------|-------------------|-----------------|---------|------|
| **A2A** | ✅ 客户端 | ✅ 服务端 | ✅ AgentTool | ✅ TaskArtifactUpdate |
| **MCP** | ✅ 客户端（stdio/sse/streamable） | ❌ | ✅ 工具消费 | ✅ POST SSE |
| **AG-UI** | — | ✅ 服务端（SSE） | — | ✅ SSE |
| **ACP** | ✅ Provider | ✅ 服务端 | ✅ MCP Bridge | ✅ SSE |

---

## 13. 可观测性与遥测

### 13.1 三级追踪体系

```
请求追踪栈:
1. OTel 分布式追踪 (span: agent.Run / agent.RunStream / agent.Close)
2. Langfuse LLM 追踪 (专用UI: Run检查、Tool调用、Token使用、错误)
3. 结构化日志 (slog: Debug/Info/Warn/Error)
4. 健康检查 (/health endpoint in AG-UI)
```

### 13.2 关键依赖

| 库 | 版本 | 用途 |
|----|------|------|
| `go.opentelemetry.io/otel` | v1.29.0 | 分布式追踪核心 |
| `go.opentelemetry.io/otel/exporters/otlp` | v1.29.0 | OTLP 导出器 |
| `trpc-agent-go` 内置 | — | Langfuse 集成 |

---

## 14. 优雅关闭与恢复

### 14.1 6 步优雅关闭链

```
1. Runner.Close()         → 停止活跃运行，阻止新的 EnqueueAutoMemoryJob
2. Evolution.Close()      → 停止引擎后台 worker，等待进行中分析任务
3. Memory.Close()         → WaitGroup 等待进行中任务（5s超时），停止提取 worker
4. Session.Close()        → 停止摘要 worker，关闭通道，释放会话资源
5. Telemetry.Close()      → 刷新+关闭 OTel + Langfuse 追踪
6. DBPool.Close()         → WAL checkpoint + 关闭共享数据库连接
```

### 14.2 崩溃恢复

| 机制 | 实现 |
|------|------|
| 优雅关闭 | 6步链：Runner→Evolution→Memory(Wg 5s)→Session→Telemetry+Langfuse→DB(WAL+Close) |
| Chromedp 泄漏 | allocCancel 保存, Close 时杀浏览器进程 |
| Ctrl+C 流式取消 | context.Cancel → goroutine 退出 → 显示 "[Request cancelled]" |
| 消息上限 | 500 条自动 trim |
| 崩溃恢复 | WAL 下次打开自动 replay |

---

## 15. 模块依赖图

### 15.1 启动依赖链

```
cmd/main → cli.Execute → session.bootstrapSession
  ├── config.Loader          配置加载
  ├── telemetry.Manager      OTel初始化
  ├── builtin.Register       内置扩展注册
  ├── provider.Factory       7种LLM工厂（含ACP）
  ├── util.DatabasePool      共享DB池
  ├── session.Service        会话服务
  ├── memory.MemoryManager   记忆管理器
  ├── security.Guard         安全守卫
  ├── extension.Manager      扩展管理器
  ├── extension.ACPMCPBridge ACP MCP Bridge（扩展→MCP透传）
  ├── recall.Store           FTS5回溯
  ├── topofmind.Manager      首要任务
  ├── codemode.Executor      JS沙箱
  ├── apps.Manager           HTML应用
  ├── builtin.AgentToolSet   子Agent工具
  ├── summon.Manager         子代理调度
  ├── skill.Manager          Skill仓库
  ├── evolution.Engine       技能自进化 ← 新增
  ├── todo.Manager           任务跟踪
  ├── knowledge.Manager      RAG知识库
  ├── artifact.Service       制品存储
  ├── observability          Langfuse追踪
  ├── project.Manager        项目追踪
  ├── [可选] summon.A2AServer  A2A服务
  ├── [可选] server.AGUIServer AG-UI SSE
  ├── [可选] server.ACPServer  ACP协议端点
  └── → agent.NewCoreLoop()  核心循环创建
```

### 15.2 外部核心依赖

| 依赖 | 版本 | 用途 |
|------|------|------|
| `trpc.group/trpc-go/trpc-agent-go` | v1.10.0 | Agent 核心框架（Agent/Runner/Tool/Session/Memory/Planner） |
| `trpc.group/trpc-go/trpc-mcp-go` | v0.0.16 | MCP 协议支持（Client/Server/Broker） |
| `trpc.group/trpc-go/trpc-a2a-go` | v0.2.5 | A2A 协议支持（Server/Client/Auth） |
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI 框架 |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | TUI 样式 |
| `github.com/spf13/cobra` | v1.9.1 | CLI 框架 |
| `github.com/spf13/viper` | v1.20.1 | 配置管理 |
| `github.com/chromedp/chromedp` | v0.15.1 | 浏览器自动化 |
| `github.com/dop251/goja` | v0.0.0-20260607 | JS 沙箱引擎 |
| `github.com/mattn/go-sqlite3` | v1.14.32 | SQLite 驱动 |
| `github.com/redis/go-redis/v9` | v9.12.1 | Redis 客户端 |
| `go.opentelemetry.io/otel` | v1.29.0 | 可观测性 |
| `github.com/google/uuid` | v1.6.0 | UUID 生成 |
| `github.com/tencentyun/cos-go-sdk-v5` | v0.7.69 | 腾讯云 COS 存储 |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML 解析 |

---

## 16. 设计决策 (ADR)

| ADR | 决策 | 理由 |
|-----|------|------|
| 1 | tRPC-Agent-Go 生态 | Session/Memory/Tool 标准化，三件套统一 |
| 2 | SQLite 共享池 WAL | 零配置+并发+FTS5，MaxOpenConns=1 避免锁竞争 |
| 3 | Memory Tools+AutoExtract 分离 | 容错+可控，手动工具始终可用 |
| 4 | 安全默认 smart | 纵深防御 6 层，高风险操作审批 |
| 5 | ContextCompaction 两遍 | 占位符(Pass1) + 截断(Pass2)，细粒度控制 |
| 6 | Memory GracefulShutdown | WaitGroup+超时+isClosing，5s 等待 |
| 7 | Ctrl+C 流式取消 | cancelCtx→goroutine 退出，不丢优雅关闭 |
| 8 | Dify/Codex/ClaudeCode 自研 | v1.10.0 框架不含对应包 |
| 9 | web_fetch 高危标记 | 防 SSRF |
| 10 | allocCancel 杀进程 | 防 Chrome 僵尸进程 |
| 11 | 延迟注入模式 | apps/code_mode/top_of_mind 需要运行时依赖 |
| 12 | MCP Broker | 大量外部工具时按需发现，避免工具列表臃肿 |
| 13 | Project Tracking | 工作目录自动记录，会话快速恢复 |
| 14 | ACP MCP Bridge | tRPC-MCP-Go Server 暴露扩展，ACP 代理透传调用 |
| 15 | ACP Server + Provider | 标准 ACP 协议端点 + Provider 类型，双向集成 |
| 16 | Dialogue Mode | `wukong run -d` 内置 REPL，复用 `runOneShot`，无需独立 TUI |
| 17 | Skill Evolution Hook | 在 CreateSkillAgent 注入 AfterAgent 回调，解耦 evolution 与 skill |
| 18 | Evolution 异步分析 | 专用通道 + 后台 worker，不阻塞主执行流程 |
| 19 | Evolution 版本备份 | 每次修改前完整备份 SKILL.md，保留最近 10 个版本 |
| 20 | Evolution 冷却期 | 同技能 30min 内不重复分析，防止频繁修补 |
| 21 | Evolution 专用模型 | 可选独立轻量模型做进化分析，不影响主 Agent 流程 |
