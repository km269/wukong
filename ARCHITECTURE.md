# Wukong 系统架构文档

> **版本**: v0.4.0 | **Go**: 1.26 | **总源文件**: 90+ (.go) + 26 (_test.go) | **~25,000 行**

---

## 目录

1. [分层架构](#1-分层架构)
2. [核心数据流](#2-核心数据流)
3. [工作流引擎](#3-工作流引擎)
4. [扩展系统](#4-扩展系统)
5. [Agent 引擎层](#5-agent-引擎层)
6. [安全体系](#6-安全体系)
7. [存储层](#7-存储层)
8. [浏览器与 Web 系统](#8-浏览器与-web-系统)
9. [可观测性与遥测](#9-可观测性与遥测)
10. [关闭与恢复](#10-关闭与恢复)
11. [模块依赖图](#11-模块依赖图)
12. [设计决策 (ADR)](#12-设计决策-adr)

---

## 1. 分层架构

```
┌──────────────────────────────────────────────────────────────────────┐
│ CLI 层 │ cmd/wukong + cli/ + tui/ │ 6 子命令                          │
│   session/configure/extension/eval/version/completion                 │
├──────────────────────────────────────────────────────────────────────┤
│ 引导层 │ cli/session.go::bootstrapSession() │ ~30 子系统串行初始化     │
├──────────────────────────────────────────────────────────────────────┤
│ 引擎层 │ agent/                                                       │
│   CoreLoop · WorkflowBuilder(10) · TeamBuilder · DifyAgent            │
│   ContextManager(RevisionEngine) · HITL · TodoEnforcer · Recipe       │
│   PromptTemplateManager · AgentCallbacks · ToolCallbacks              │
├──────────────────────────────────────────────────────────────────────┤
│ 服务层 │ internal/*/                                                  │
│   Provider(6) · Extension(10内置+MCP+Broker)                           │
│   Session(sqlite/redis) · Memory(GracefulShutdown)                     │
│   Knowledge(RAG) · Recall(FTS5) · Artifact(COS)                        │
│   Browser(HTTP+Chromedp) · CodeMode(goja JS沙箱)                       │
│   Server(AG-UI SSE) · Summon(A2A) · Skill · Todo                       │
│   Observability(Langfuse+OTel) · Eval                                  │
├──────────────────────────────────────────────────────────────────────┤
│ 存储层 │ wukong.db(WAL,FTS5) + Redis + COS                             │
└──────────────────────────────────────────────────────────────────────┘
```

### 1.1 目录结构

```
wukong/
├── cmd/wukong/main.go                 程序入口
├── config.yaml                        主配置文件（28段，453行）
├── internal/
│   ├── agent/                         核心引擎层（13文件）
│   │   ├── loop.go                    CoreLoop — 主执行循环
│   │   ├── context.go                 ContextRevisionEngine — 上下文修订
│   │   ├── workflow.go                WorkflowBuilder — 10种多模式编排
│   │   ├── team.go                    TeamBuilder — 多Agent团队
│   │   ├── dify.go                    DifyAgent — Dify AI平台集成
│   │   ├── hitl.go                    HITL — 人机回环中断
│   │   ├── prompt_template.go         PromptTemplate — 提示词模板
│   │   ├── recipe.go                  Recipe — YAML配方子代理
│   │   └── todo_enforcer.go           TodoEnforcer — 任务完成强制器
│   ├── apps/manager.go               HTML应用管理
│   ├── artifact/factory.go           制品存储（inmemory/COS）
│   ├── browser/controller.go         浏览器自动化（HTTP+Chromedp）
│   ├── cli/                          CLI命令层（11文件）
│   │   ├── root.go                   Cobra根命令
│   │   ├── session.go                交互会话引导
│   │   ├── run.go                    单次执行模式
│   │   ├── configure.go              配置向导
│   │   ├── extension.go              扩展管理命令
│   │   ├── project.go                项目追踪/恢复
│   │   ├── eval.go                   评估命令
│   │   ├── version.go                版本信息
│   │   └── tui/                      终端UI
│   │       ├── model.go              Bubbletea TUI模型
│   │       ├── update.go             事件更新+流式处理
│   │       └── view.go               视图渲染
│   ├── codemode/executor.go          goja JS沙箱执行器
│   ├── config/config.go              完整配置定义（55KB）
│   ├── eval/eval.go                  评估框架
│   ├── extension/                    MCP扩展系统（21文件）
│   │   ├── manager.go                扩展管理器
│   │   ├── factory.go                内置扩展工厂
│   │   ├── types.go                  扩展类型定义
│   │   ├── manager_tools.go          扩展管理工具（4个）
│   │   ├── mcp_client.go             MCP客户端（stdio/sse/streamable）
│   │   ├── deeplink.go               Deeplink URL解析安装
│   │   └── builtin/                  内置扩展实现（13文件）
│   │       ├── registry.go           内置扩展注册表（10个）
│   │       ├── developer.go          开发者工具（6个）
│   │       ├── computer_controller.go 计算机控制器（9个）
│   │       ├── memory.go             记忆工具（6个）
│   │       ├── auto_visualiser.go    自动可视化（3个）
│   │       ├── tutorial.go           交互式教程（3个）
│   │       ├── apps.go               应用管理（5个）
│   │       ├── codemode.go           代码模式（2个）
│   │       ├── topofmind.go          首要任务（4个）
│   │       ├── agent.go              子Agent工具（3个）
│   │       └── web.go                Web搜索工具
│   ├── health/health.go              健康检查
│   ├── knowledge/manager.go          RAG知识库管理
│   ├── memory/store.go               长期记忆（优雅关闭）
│   ├── observability/langfuse.go     Langfuse LLM追踪
│   ├── provider/factory.go           6种LLM Provider工厂
│   ├── recall/                       跨会话回溯
│   │   ├── store.go                  FTS5存储+索引
│   │   └── tool.go                   回溯工具（2个）
│   ├── security/                     安全防护
│   │   ├── guard.go                  4级权限+命令拦截
│   │   └── ignore.go                 .wukongignore文件黑名单
│   ├── server/agui.go                AG-UI SSE服务器
│   ├── session/                      会话管理（sqlite/redis）
│   ├── skill/manager.go              Agent Skill仓库
│   ├── summon/                       子代理调度
│   │   ├── delegate.go              子代理委托+并发控制
│   │   ├── a2a.go                    A2A远程代理
│   │   └── auth.go                   A2A认证（JWT/APIKey/OAuth2）
│   ├── telemetry/                    OTel分布式追踪
│   ├── todo/tool.go                  任务跟踪（5+1双重工具）
│   ├── topofmind/mind.go             持久化指令注入
│   ├── project/                      项目追踪
│   └── util/                         DB池(WAL) · 日志
```

---

## 2. 核心数据流

```
用户输入 → TUI
  → CoreLoop.RunStream(ctx, userID, sessionID, msg)
    ├─ OTel Trace Span 开始
    ├─ ContextManager.PrepareContext()           Token 预算检查 + 异步摘要触发
    ├─ RecallStore.StoreMessage(user)             写入 FTS5 索引
    ├─ Runner.Run()                               tRPC-Agent-Go 运行器
    │   ├─ Session 历史 + System Instruction
    │   │   ├─ Prompt Templates (自定义 .md 模板)
    │   │   ├─ TopOfMind (持久化指令)
    │   │   ├─ PreloadMemory (最多10条记忆预加载)
    │   │   └─ PreloadSessionRecall (跨会话上下文)
    │   ├─ LLM Agent (Planner + GenConfig)
    │   │   ├─ [可选] BuiltinPlanner (thinking模型)
    │   │   ├─ [可选] ReActPlanner (标签引导思考)
    │   │   └─ [可选] ToolSearch (TopK工具筛选)
    │   ├─ 工具循环执行:
    │   │   ├─ BeforeTool Callback → 安全检查 (权限/命令/.wukongignore)
    │   │   ├─ 工具执行 (并行/串行)
    │   │   ├─ [可选] ToolRetry (自动重试+退避)
    │   │   ├─ [可选] JSONRepair (修复非标准JSON)
    │   │   ├─ AfterTool Callback → 结果监控
    │   │   ├─ [可选] ContextCompaction (两遍压缩)
    │   │   └─ [可选] PostToolPrompt (工具后提示)
    │   ├─ TodoEnforcer → 检查待办任务完成状态
    │   ├─ Guardrail → [可选] Prompt注入检测
    │   └─ <-chan *event.Event → 流式事件通道
    ├─ 遍历事件流 → 文本聚合 + 工具调用统计
    ├─ RecallStore.StoreMessage(assistant)         写入助手回复
    ├─ ContextManager.AfterRun() → Token更新 + 摘要触发评估
    └─ OTel Span 结束 (event_count, tool_call_count, response_length)
```

### 2.1 系统启动流程 (bootstrapSession)

```
1. config.NewLoader() → 加载配置 (Viper + YAML + ENV覆盖)
2. validateConfig() → 配置验证 + 警告
3. telemetry.NewManager() → OTel初始化
4. builtin.RegisterBuiltins() → 注册10个内置扩展
5. applyOverrides() → CLI参数覆盖
6. provider.NewFactory() → LLM Provider工厂
7. util.NewDatabasePool() → 共享DB连接池 (WAL模式)
8. session.NewSessionService() → 会话服务 (sqlite/redis)
9. memory.NewMemoryManager() → 记忆管理器 (自动提取/手动工具)
10. security.NewGuard() → 安全守卫
11. extension.NewManager() → 扩展管理器
12. extMgr.Initialize() → 加载所有启用扩展 (内置+MCP)
13. extMgr.SetMemoryService() → 注入记忆服务到扩展
14. extension.NewManagerToolSet() → 扩展管理工具
15. recall.NewStore() → FTS5回溯存储
16. topofmind.NewManager() → 首要任务管理器
17. codemode.NewExecutor() → JS沙箱执行器
18. apps.NewManager() → HTML应用管理器
19. builtin.NewAgentToolSet() → 子Agent工具 (code-reviewer/summarizer/code-generator)
20. summon.NewSummonManager() → 子代理调度管理器
21. summonMgr.LoadSkills() → 加载Skill定义
22. skill.NewManager().Initialize() → Skill系统 (FSRepository)
23. todo.NewStore() → 任务存储 (SQLite)
24. knowledge.NewManager() → RAG知识库
25. factory.CreateRevisionModel() → 上下文修订模型
26. artifact.NewService() → 制品服务
27. observability.StartLangfuse() → Langfuse追踪
28. agent.NewCoreLoop() → 创建核心循环
29. project.NewManager() → 项目追踪
30. [可选] summon.NewA2AServer() → A2A服务启动
31. [可选] server.NewAGUIServer() → AG-UI SSE服务启动
```

---

## 3. 工作流引擎 (10种)

| # | 模式 | 实现 | 说明 |
|---|------|------|------|
| 1 | `single` | `llmagent.New()` | 标准单 Agent，带完整配置 |
| 2 | `chain` | `chainagent.New()` | 顺序流水线（planner→executor→reviewer） |
| 3 | `parallel` | `parallelagent.New()` | 并发多专家（code/doc/test分析） |
| 4 | `cycle` | `cycleagent.New()` | 迭代优化（default/code_review） |
| 5 | `graph` | `graphagent.New()` | 条件路由DAG（analyze→code/search/answer→review） |
| 6 | `team_coordinator` | `team.New()` | 协调者 + AgentTool 委托成员 |
| 7 | `team_swarm` | `team.NewSwarm()` | Agent直接transfer，独立成员历史，20次handoff限制 |
| 8 | `claude_code` | `claudecode.New()` | 本地 Claude Code CLI 包装 |
| 9 | `codex` | `codex.New()` | 本地 Codex CLI 包装（sandbox workspace-write） |
| 10 | `dify` | `DifyAgent`（自研） | Dify Chat API（blocking + SSE流式） |

### 3.1 工作流模式详解

**Single 模式** (默认):
- 完整的单Agent配置链：模型选择 → 工具注册 → Planner配置 → 回调注册
- 支持所有Agent配置特性（记忆预加载、上下文压缩、会话回溯、工具搜索等）

**Chain 模式**:
- 默认3个Agent流水线：planner → executor → reviewer
- 可通过 `workflow.sub_agents` 自定义子Agent和工具权限

**Parallel 模式**:
- 默认3个专家并行：code-analyzer、doc-analyzer、test-analyzer
- 各专家独立执行，结果合并

**Cycle 模式**:
- `default`：planner ↔ executor 循环，TASK_COMPLETE 关键字退出
- `code_review`：generator ↔ reviewer 循环，CODE_APPROVED 关键字退出

**Graph 模式**:
- analyze 节点分类路由 → code/search/answer → review 汇聚
- 支持 StateGraph + 条件边 + 状态Schema

**Team 模式**:
- **Coordinator**：协调者通过 AgentTool 委托给成员
- **Swarm**：Agent 通过 transfer_to_agent 直接传递控制权
- 默认成员：researcher、coder、reviewer

---

## 4. 扩展系统

### 4.1 扩展管理架构

```
extension.Manager
├── 内置扩展工厂 (factory.go)
│   ├── CreateBuiltinToolSet(name, cfg) → tool.ToolSet
│   │   ├── developer          → DeveloperToolSet (6工具)
│   │   ├── computer_controller → ComputerControllerToolSet (9工具)
│   │   ├── memory             → MemoryToolSet (6工具, 延迟注入)
│   │   ├── auto_visualiser    → VisualiserToolSet (3工具)
│   │   ├── tutorial           → TutorialToolSet (3工具)
│   │   ├── web                → WebToolSet (DuckDuckGo)
│   │   ├── agent_tools        → AgentToolSet (3子Agent, 延迟创建)
│   │   ├── apps               → nil (延迟注入, bootstrapSession创建)
│   │   ├── code_mode           → nil (延迟注入)
│   │   └── top_of_mind         → nil (延迟注入)
│   └── 外部MCP客户端 (mcp_client.go)
│       ├── stdio    → npx/uvx 子进程
│       ├── sse      → HTTP SSE长连接
│       └── streamable → HTTP流式
└── MCP Broker (mcpbroker.New())
    └── 按需工具发现: mcp_list_servers / mcp_list_tools / mcp_call
```

### 4.2 内置扩展完整清单

#### 功能性扩展（5个）

| 扩展 | 文件 | 工具 | 默认启用 |
|------|------|------|---------|
| **Developer** | `builtin/developer.go` | `file_read`, `file_write`, `file_replace`, `command_execute`, `code_search`, `directory_list` | ✅ |
| **Computer Controller** | `builtin/computer_controller.go` | `web_fetch`, `file_cache`, `cache_list`, `cache_clear`, `browser_navigate`, `browser_extract`, `browser_screenshot`, `browser_click`, `browser_fill` | ✅ (联动browser.enabled) |
| **Memory** | `builtin/memory.go` | `memory_add`, `memory_search`, `memory_delete`, `memory_update`, `memory_load`, `memory_clear` | ✅ |
| **Auto Visualiser** | `builtin/auto_visualiser.go` | `visualiser_chart`(bar/line/pie/scatter/flow SVG), `visualiser_diagram`(Mermaid), `visualiser_table`(HTML) | ✅ (联动visualiser.enabled) |
| **Tutorial** | `builtin/tutorial.go` | `tutorial_start`, `tutorial_list`, `tutorial_step` | ✅ (联动tutorial.enabled) |

#### 平台扩展（7个）

| 扩展 | 文件 | 工具 | 默认启用 |
|------|------|------|---------|
| **Apps** | `builtin/apps.go` | `app_create`, `app_list`, `app_get`, `app_update`, `app_delete` | ✅ |
| **Chat Recall** | `recall/tool.go` | `recall_search`(FTS5全文搜索), `recall_sessions` | ✅ |
| **Code Mode** | `builtin/codemode.go` | `code_execute`(goja JS沙箱), `code_discover_tools` | ✅ |
| **Extension Manager** | `extension/manager_tools.go` | `extension_list`, `extension_enable`, `extension_disable`, `extension_add_deeplink` | ✅ (始终启用) |
| **Summon** | `builtin/agent.go` + `summon/` | `code-reviewer`, `summarizer`, `code-generator` + Skills + A2A | ✅ |
| **Todo** | `todo/tool.go` | `todo_create`, `todo_update`, `todo_list`, `todo_complete`, `todo_delete` + tRPC `todo_write` + TodoEnforcer | ✅ |
| **Top of Mind** | `builtin/topofmind.go` | `tom_get`, `tom_set`, `tom_append`, `tom_clear` | ✅ |

### 4.3 外部MCP服务器配置

支持三种传输协议：
- **stdio**：通过子进程通信（npx/uvx/pip等）
- **sse**：HTTP Server-Sent Events
- **streamable**：HTTP流式传输

高级特性：
- **Tool Filter**：glob 模式工具包含/排除（`mcp_tool_filter`/`mcp_tool_exclude`）
- **Session Reconnect**：自动重连（`mcp_session_reconnect`，最多3次）
- **MCP Broker**：按需工具发现，避免工具列表臃肿
- **Deeplink**：`wukong://extension?name=...` 一键安装
- **Env Overrides**：为MCP子进程设置自定义环境变量

---

## 5. Agent 引擎层

### 5.1 CoreLoop 核心循环

```
CoreLoop
├── agent (LLMAgent/ChainAgent/...)
├── runner.Runner (tRPC-Agent-Go)
├── contextMgr (ContextManager → ContextRevisionEngine)
├── security.Guard (4层权限 + 命令拦截)
├── recallStore (FTS5回溯)
└── closeFn (5步关闭链)
```

**配置选项**：
- `MaxLLMCalls`: 50 (每次运行最大LLM调用次数)
- `MaxToolIterations`: 30 (最大工具迭代次数)
- `ParallelTools`: true (并行工具执行)
- `Streaming`: true (流式输出)
- `Temperature`: 0.7 / `MaxTokens`: 4096
- `ToolRetry`: 自动重试 + 指数退避 (3次, 1s初始, 2.0因子)
- `JSONRepair`: 修复非标准JSON工具参数
- `ContextCompaction`: 两遍压缩（占位符 + 截断）
- `SessionRecall`: 跨会话上下文预加载

### 5.2 ContextRevisionEngine 上下文修订引擎

三层修订策略：
1. **异步摘要** → 通过 Session Service 的 `EnqueueSummaryJob` 触发
2. **Token预算管理** → 64000 max_tokens / 0.3 trim_ratio
3. **命令输出截断** → 8000字节限制，保留首尾

触发条件：
- 估算Token超过阈值：`max_tokens × (1 - trim_ratio)`
- 消息数超过100条
- 距上次修订超过5分钟

### 5.3 Planner 配置

| Planner | 适用模型 | 特性 |
|---------|---------|------|
| `builtin` | Claude/Gemini/OpenAI o-series | 原生 thinking 模式，支持 ReasoningEffort/ThinkingTokens |
| `react` | DeepSeek/Ollama/LMStudio | 通过 `/*PLANNING*/` `/*REASONING*/` `/*ACTION*/` 标签引导 |
| 空（默认） | 所有模型 | 不启用planner，直接工具调用 |

### 5.4 回调体系

三层回调注册：
- **AgentCallbacks**: BeforeAgent/AfterAgent — 日志记录、调用统计
- **ToolCallbacks**: BeforeTool/AfterTool — 安全校验、命令拦截、.wukongignore检查
- **ModelCallbacks**: BeforeModel/AfterModel — Token使用追踪、请求监控

### 5.5 子代理系统

#### AgentToolSet（内置子代理）
- **code-reviewer**: Temperature 0.3, MaxTokens 2048, MaxLLMCalls 3
- **summarizer**: Temperature 0.3, MaxTokens 1024, MaxLLMCalls 2
- **code-generator**: Temperature 0.2, MaxTokens 4096, MaxLLMCalls 3

#### Summon（子代理调度）
- 从 `.wukong_skills/` 加载 Skill 定义（.md文件）
- 每个 Skill 自动创建为子Agent工具
- 并发控制：semaphore（默认5并发）
- A2A远程代理支持

#### Skill系统
- 基于 tRPC-Agent-Go 的 `FSRepository`
- SKILL.md 格式：YAML front matter + Markdown 指令体
- 自动加载 + 运行时刷新（`Refresh()`）

---

## 6. 安全体系

### 6.1 4层权限模型

```
Permission Mode:
  auto      → 所有工具自动执行，无需审批
  smart     → 仅高风险操作需要审批（默认）
  manual    → 每次工具调用都需要审批
  chat_only → 禁止所有工具，仅文本交互
```

### 6.2 高风险工具清单（smart模式需审批）

- **命令执行**: `bash`, `execute_command`, `run_command`, `shell`, `terminal`, `command`, `command_execute`
- **文件操作**: `file_write`, `file_replace`, `file_delete`
- **浏览器**: `browser_navigate`, `browser_screenshot`, `browser_click`, `browser_fill`
- **Web**: `web_fetch`

### 6.3 多层安全机制

| 层级 | 机制 | 说明 |
|------|------|------|
| 1 | Allowlist/Denylist | 工具级别白名单/黑名单（*通配符） |
| 2 | 命令模式拦截 | 内置危险命令列表（rm -rf /, dd, mkfs, fork bomb） |
| 3 | 恶意软件扫描 | 外部扩展命令/参数扫描（12种可疑模式） |
| 4 | Guardrail | tRPC Prompt Injection 检测（可选，增加延迟） |
| 5 | .wukongignore | gitignore兼容语法文件访问黑名单 |
| 6 | ToolPermission | 扩展级别细粒度工具权限控制 |

### 6.4 Guardrail 提示注入检测

```
用户输入 → guardrail-reviewer Agent (Temperature 0.0, MaxTokens 256)
  → promptinjection.Reviewer 审查
    → 通过: 继续正常Agent流程
    → 拒绝: 返回安全警告
```

---

## 7. 存储层

### 7.1 共享数据库池

```
wukong.db (WAL模式, MaxOpenConns=1)
├── Session (tRPC sqlite session.Service)
├── Memory (tRPC sqlite memory.Service)
├── Recall (FTS5全文搜索索引)
└── Todo (自定义SQLite任务表)
```

### 7.2 会话存储

| 后端 | 特性 |
|------|------|
| sqlite | 默认，WAL模式，事件限制500，自动摘要触发50 |
| redis | 分布式部署，go-redis/v9 |
| memory | 测试/开发用，无持久化 |

### 7.3 长期记忆

- **自动提取**: 3个异步worker，LLM自动从对话中提取记忆
- **手动工具**: 6个tRPC标准记忆工具
- **提取模型**: 可配置独立的小模型（deepseek-chat等）降低成本
- **自定义Prompt**: 支持精简提取Prompt（适合本地小模型）
- **优雅关闭**: WaitGroup + 5s超时 + isClosing标志拒绝新任务

### 7.4 会话回溯 (FTS5)

- `fts5` 模式：纯全文搜索
- `hybrid` 模式：语义搜索 + 全文搜索混合（需embedding provider）
- 每条用户/助手消息自动索引
- 支持按Session ID过滤

---

## 8. 浏览器与 Web 系统

### 8.1 双模式浏览器引擎

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

### 8.2 浏览器安全

- `allocCancel` 保存，Close时杀浏览器进程（修复僵尸Chrome泄漏）
- 视口配置：1280×720 headless chromium

### 8.3 Web搜索

- **DuckDuckGo**: 默认，`duckduckgo.NewTool()`（tRPC内置）
- **SearXNG**: 需配置 `search_backend_url`
- **Tavily**: 需配置 `search_api_key`

---

## 9. 可观测性与遥测

### 9.1 追踪层

```
请求追踪栈:
1. OTel分布式追踪 (span: agent.Run / agent.RunStream / agent.Close)
2. Langfuse LLM追踪 (专用UI: Run检查、Tool调用、Token使用、错误)
3. 结构化日志 (slog: Debug/Info/Warn/Error)
4. 健康检查 (/health endpoint in AG-UI)
```

### 9.2 遥测配置

```yaml
telemetry:
  enabled: false          # OTel分布式追踪
  exporter_type: console  # grpc | http | console
  service_name: wukong
  sample_rate: 1.0

observability:
  langfuse_enabled: false # Langfuse LLM追踪
  # langfuse_host/public_key/secret_key
```

---

## 10. 关闭与恢复

### 10.1 5步优雅关闭链

```
1. Runner.Close()  → 停止活跃运行，阻止新的EnqueueAutoMemoryJob
2. Memory.Close()  → WaitGroup等待进行中任务（5s超时），停止提取worker
3. Session.Close() → 停止摘要worker，关闭通道，释放会话资源
4. Telemetry.Close() → 刷新+关闭OTel + Langfuse追踪
5. DBPool.Close()  → WAL checkpoint + 关闭共享数据库连接
```

### 10.2 崩溃恢复

| 机制 | 实现 |
|------|------|
| 优雅关闭 | 5步链：Runner→Memory(WaitGroup 5s)→Session→Telemetry+Langfuse→DB(WAL checkpoint+Close) |
| Chromedp 泄漏 | allocCancel保存, Close时杀浏览器进程 |
| Ctrl+C 流式取消 | context.Cancel → goroutine退出 → 显示"[Request cancelled]" |
| 消息上限 | 500条自动trim |
| 崩溃恢复 | WAL下次打开自动replay |

---

## 11. 模块依赖图

```
cmd/main → cli.Execute → session.bootstrapSession
  ├── config.Loader         配置加载
  ├── telemetry.Manager     OTel初始化
  ├── builtin.Register      内置扩展注册
  ├── provider.Factory      6种LLM工厂
  ├── util.DatabasePool     共享DB池
  ├── session.Service       会话服务
  ├── memory.MemoryManager  记忆管理器
  ├── security.Guard        安全守卫
  ├── extension.Manager     扩展管理器
  ├── recall.Store          FTS5回溯
  ├── topofmind.Manager     首要任务
  ├── codemode.Executor     JS沙箱
  ├── apps.Manager          HTML应用
  ├── builtin.AgentToolSet  子Agent工具
  ├── summon.Manager        子代理调度
  ├── skill.Manager         Skill仓库
  ├── todo.Manager          任务跟踪
  ├── knowledge.Manager     RAG知识库
  ├── artifact.Service      制品存储
  ├── observability         Langfuse追踪
  ├── project.Manager       项目追踪
  ├── [可选] summon.A2AServer  A2A服务
  ├── [可选] server.AGUIServer AG-UI SSE
  └── → agent.NewCoreLoop()  核心循环创建
```

### 11.1 外部依赖

| 依赖 | 版本 | 用途 |
|------|------|------|
| `trpc.group/trpc-go/trpc-agent-go` | v1.10.0 | Agent核心框架 |
| `trpc.group/trpc-go/trpc-mcp-go` | v0.0.16 | MCP协议支持 |
| `trpc.group/trpc-go/trpc-a2a-go` | v0.2.5 | A2A协议支持 |
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI框架 |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | TUI样式 |
| `github.com/spf13/cobra` | v1.9.1 | CLI框架 |
| `github.com/spf13/viper` | v1.20.1 | 配置管理 |
| `github.com/chromedp/chromedp` | v0.15.1 | 浏览器自动化 |
| `github.com/dop251/goja` | v0.0.0-20260607 | JS沙箱引擎 |
| `github.com/mattn/go-sqlite3` | v1.14.32 | SQLite驱动 |
| `github.com/redis/go-redis/v9` | v9.12.1 | Redis客户端 |
| `go.opentelemetry.io/otel` | v1.29.0 | 可观测性 |
| `github.com/google/uuid` | v1.6.0 | UUID生成 |

---

## 12. 设计决策 (ADR)

| ADR | 决策 | 理由 |
|-----|------|------|
| 1 | tRPC-Agent-Go 生态 | Session/Memory/Tool 标准化，三件套统一 |
| 2 | SQLite 共享池 WAL | 零配置+并发+FTS5，MaxOpenConns=1 |
| 3 | Memory Tools+AutoExtract 分离 | 容错+可控，手动工具始终可用 |
| 4 | 安全默认 smart | 纵深防御4层，高风险操作审批 |
| 5 | ContextCompaction 两遍 | 占位符(Pass1) + 截断(Pass2)，细粒度控制 |
| 6 | Memory GracefulShutdown | WaitGroup+超时+isClosing，5s等待 |
| 7 | Ctrl+C 流式取消 | cancelCtx→goroutine退出，不丢优雅关闭 |
| 8 | Dify/Codex/ClaudeCode 自研 | v1.10.0 框架不含对应包 |
| 9 | web_fetch 高危标记 | 防 SSRF |
| 10 | allocCancel 杀进程 | 防 Chrome 僵尸进程 |
| 11 | 延迟注入模式 | apps/code_mode/top_of_mind 需要运行时依赖 |
| 12 | MCP Broker | 大量外部工具时按需发现，避免工具列表臃肿 |
| 13 | Project Tracking | 工作目录自动记录，会话快速恢复 |
