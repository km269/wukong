# Wukong 功能实现指南

## 1. CLI 命令实现

### 1.1 命令树 (root.go)

所有命令均在 `e:\myVibeCoding\km269\wukong\internal\cli\root.go` 的 `newRootCmd()` 函数中注册，共 30 个子命令：

| 命令名 | 创建函数名 | 功能描述 |
|--------|-----------|---------|
| `session` | `newSessionCmd()` | 启动交互式代理会话，包含子命令 list/delete/export/info/resume |
| `configure` | `newConfigureCmd()` | 交互式配置向导（引导式配置生成） |
| `version` | `newVersionCmd()` | 显示版本信息 |
| `extension` | `newExtensionCmd()` | 扩展管理（list/enable/disable/inspect） |
| `completion` | `newCompletionCmd()` | 生成 shell 自动补全脚本（bash/zsh/fish/powershell） |
| `eval` | `newEvalCmd()` | 评估/回归测试 |
| `project` | `newProjectCmd()` | 项目管理 |
| `projects` | `newProjectsCmd()` | 列出所有项目 |
| `run` | `newRunCmd()` | 单次执行模式（支持 --message/管道/参数输入和 --dialogue 多轮模式） |
| `config` | `newConfigCmd()` | 配置管理（查看/编辑/验证） |
| `server` | `newServerCmd()` | 无头服务器模式（启动所有协议端点） |
| `health` | `newHealthCmd()` | 健康检查 |
| `memory` | `newMemoryCmd()` | 记忆管理（list/search/delete/export） |
| `provider` | `newProviderCmd()` | 提供者管理（list/test） |
| `env` | `newEnvCmd()` | 环境变量管理 |
| `skill` | `newSkillCmd()` | 技能管理（list/create/edit/delete） |
| `recipe` | `newRecipeCmd()` | 配方管理（list/create/run） |
| `init` | `newInitCmd()` | 初始化工作区配置 |
| `knowledge` | `newKnowledgeCmd()` | 知识库管理（ingest/search/delete） |
| `ard` | `newARDCCmd()` | 资源发现管理（catalog/search/register） |
| `evolution` | `newEvolutionCmd()` | 技能进化管理（analyze/patch/history） |
| `cortex` | `newCortexCmd()` | CortexDB 管理（status/query/rebuild） |
| `todo` | `newTodoCmd()` | 待办事项管理 |
| `docs` | `newDocsCmd()` | 在浏览器中打开文档 |
| `stats` | `newStatsCmd()` | 系统统计仪表盘 |
| `bench` | `newBenchCmd()` | 基准测试 |
| `backup` | `newBackupCmd()` | 备份数据库和配置 |
| `system-check` | `newSystemCheckCmd()` | 系统依赖检查 |
| `apps` | `newAppsCmd()` | HTML 应用管理（clone/pack/list/create/view/delete/history/export） |
| `search` | `newSearchCmd()` | 搜索调优 |

### 1.2 关键命令实现详解

#### server 命令
位于 `e:\myVibeCoding\km269\wukong\internal\cli\server.go` 的 `newServerCmd()` 和 `runServer()` 函数。

**启动流程：**
1. 调用 `bootstrapSession()` 初始化所有子系统
2. 创建健康检查注册表 `health.NewRegistry(Version)`，注册所有组件检查器
3. 在 `:8086` 端口启动健康检查 HTTP 服务器（`/healthz`、`/readyz`、`/livez`）
4. 调用 `printServerStartup()` 打印各协议端点状态
5. 监听 OS 信号（`SIGINT`、`SIGTERM`、`SIGHUP`）
6. 收到信号后调用 `shutdownBootstrap()` 优雅关闭（15 秒超时）

**支持的协议端点：**
- A2A（`:9090`）— 代理间通信
- ACP（`:9091`）— 代理客户端协议
- AG-UI（`:8080/agui`）— Web UI SSE 流式
- ACP MCP（`:3400/mcp`）— 跨协议工具桥接
- ARD 注册表（可配置端口）— 资源发现
- 网关（WebSocket 出站）— 消息通道

#### run 命令
位于 `e:\myVibeCoding\km269\wukong\internal\cli\run.go` 的 `newRunCmd()` 和 `runOneShot()` 函数。

**三种输入模式：**
1. `-m` 标志：`wukong run -m "prompt"`
2. 位置参数：`wukong run "prompt"`
3. 标准输入管道：`echo "prompt" | wukong run`

**对话模式（`-d`/`--dialogue`）：**
- 多轮 REPL 交互
- 支持 `/exit`、`/quit`、`/session`、`/clear`、`/help` 命令
- 自动生成会话 ID，可通过 `-s` 指定

#### session 命令
位于 `e:\myVibeCoding\km269\wukong\internal\cli\session.go` 的 `newSessionCmd()` 和 `runSession()` 函数。

**启动流程：**
1. 解析用户 ID（环境变量 `USER` → `USERDOMAIN\USERNAME` → 主机名 → `"default"`）
2. 调用 `quickLoadConfig()` 快速预加载配置信息
3. 调用 `bootstrapSession()` 完整初始化
4. 设置 OS 信号处理（优雅关闭）
5. 调用 `tui.StartTUI()` 启动 TUI 界面

**子命令：** list、delete、export、info、resume

#### apps 命令
位于 `e:\myVibeCoding\km269\wukong\internal\cli\apps_mgmt.go`。

**克隆功能：**
- 使用 Rod/Chromedp 浏览器后端
- 支持 anti-bot 反检测机制
- 递归爬取、资源下载、CSS 重写
- 支持断点续爬

**打包功能：**
- 将应用打包为 ZIM 离线格式
- 支持压缩、增量

### 1.3 启动流程 (bootstrapSession)

`bootstrapSession()` 函数位于 `e:\myVibeCoding\km269\wukong\internal\cli\session.go:247`，是 Wukong 的核心启动函数，完整的步骤顺序如下：

1. **loadConfig(configPath)** — 通过 `config.NewLoader()` 创建 Viper 加载器，调用 `LoadAndValidate()` 加载并验证配置
2. **setupDatabase(cfg)** — 创建 `util.NewMultiPool()` 数据库连接池（所有子系统共享 `wukong.db`）
3. **setupSessionService(db)** — 通过 `wksession.NewSessionService()` 创建会话服务
4. **setupMemoryService(db)** — 通过 `memory.NewMemoryManager()` 创建记忆管理器（支持自动提取）
5. **setupRecallStore(db)** — 创建 `recall.NewStore()` 搜索召回存储（FTS5）
6. **setupCortexStore(cfg, db)** — 可选：创建 `cortex.NewStore()` CortexDB 知识存储（HNSW 向量 + FTS5 混合）
7. **setupMemoryFlowService(cortexDB)** — 可选：创建 `cortex.NewMemoryFlow()` MemoryFlow 服务（对话记录、唤醒上下文、事实提升）
8. **setupGraphFlowService(cortexDB)** — 可选：创建 `cortex.NewGraphFlow()` GraphFlow 服务（实体/关系提取、知识图谱构建）
9. **initProviderFactory(cfg)** — 通过 `provider.NewFactory()` 初始化提供者工厂
10. **setupSecurityGuard(cfg)** — 通过 `security.NewGuard()` 创建安全守卫
11. **registerBuiltinExtensions(cfg)** — 调用 `builtin.RegisterBuiltins()` 注册所有内置扩展
12. **initExtensionManager(cfg)** — 创建 `extension.NewManager()` 并调用 `Initialize()` 初始化
13. **createCoreLoop(cfg, factory, ...)** — 创建 `agent.NewCoreLoop()` 核心循环
14. **setupA2AServer(cfg, coreLoop)** — 可选：创建 `summon.NewA2AServer()` A2A 服务器
15. **setupACPServer(cfg, coreLoop)** — 可选：创建 `server.NewACPServer()` ACP 服务器
16. **setupAGUIServer(cfg, coreLoop)** — 可选：创建 `server.NewAGUIServer()` AG-UI 服务器
17. **setupACPMCPServer(cfg)** — 创建 `extension.NewACPMCPBridge()` ACP MCP 桥接
18. **setupGateway(cfg, coreLoop)** — 可选：创建 `gateway.NewGatewayServer()` 消息网关
19. **startServers(...)** — 启动所有已配置的服务器（A2A、ACP、AG-UI、ANP、网关）
20. **registerHealthCheckers(...)** — 注册所有组件的健康检查

## 2. 配置管理实现

### 2.1 Loader 实现

配置加载器位于 `e:\myVibeCoding\km269\wukong\internal\config\config.go`。

**NewLoader(configPath)**
- 创建 Viper 实例，设置配置名 `"config"`、类型 `"yaml"`
- 如果 configPath 为空，搜索路径：`./config.yaml` → `~/.config/wukong/config.yaml` → `/etc/wukong/config.yaml`（非 Windows）
- 设置环境变量前缀 `WUKONG`，自动覆盖

**setDefaults()**
调用一系列子函数设置默认值（位于 `defaults.go`）：
- `setGlobalDefaults()`：`log_level: info`, `project_dir: ~/.config/wukong/`
- `setAgentDefaults()`：`max_llm_calls: 50`, `max_tool_iterations: 30`, `streaming: true`, `temperature: 0.7` 等
- `setSecurityDefaults()`：`permission_mode: smart`, `malware_scan_enabled: true` 等
- `setStorageDefaults()`：session、memory、todo、recall 的默认值
- `setCortexStackDefaults()`：cortex、memoryflow、graphflow、importflow 的默认值
- `setRevisionDefaults()`：`revision.enabled: true`, `trim_ratio: 0.3` 等
- `setFeatureDefaults()`：browser、visualiser、tutorial、top_of_mind、code_mode 的默认值
- `setAppsDefaults()`：clone、pack 的默认值
- `setOrchestrationDefaults()`：ard、summon、skill、anp、evolution、knowledge、workflow、dify 的默认值
- `setServerDefaults()`：a2a_server、agui、acp_server、acp_mcp 的默认值
- `setObservabilityDefaults()`：eval、artifact、observability、telemetry 的默认值
- `setGatewayDefaults()`：委托给 `gateway.SetDefaults()`
- `setOKFDefaults()`：okf 的默认值

**Load()**
- 反序列化 YAML 到 `WukongConfig` 结构体
- 调用 `expandSecrets()` 展开所有 `${ENV_VAR}` 引用（支持 `:-default` 回退语法）
- 结果缓存，后续调用返回同一实例

**expandSecrets()**
展开的字段包括：
- 所有提供者的 `api_key`、`base_url`、`model`
- A2A 远程的 `api_key`、`jwt_secret`、`oauth_client_secret`
- 飞书通道的 `app_secret`、`encrypt_key`、`verification_token`
- Langfuse 的公钥/密钥
- 制品 COS 密钥
- ACP 服务器 API 密钥
- CortexDB 嵌入/重排序配置
- 垂直路由 GitHub API 密钥
- MemoryFlow/GraphFlow 模型设置
- Dify API 密钥
- Redis URL
- 搜索提供者密钥

### 2.2 验证实现 (validate.go)

位于 `e:\myVibeCoding\km269\wukong\internal\config\validate.go`。

**Validate() — 致命错误检查：**
- 默认提供者存在且可找到
- `agent.temperature` 在 `[0.0, 2.0]` 范围内
- `security.permission_mode` 有效值（auto/smart/manual/chat_only）
- 提供者类型有效（openai/anthropic/google/deepseek/ollama/lmstudio/vllm/acp）
- 浏览器后端有效（chromedp/rod）
- 工作流模式有效（single/chain/parallel/cycle/graph/team_coordinator/team_swarm/claude_code/codex/dify）
- `agent.max_tokens`、`evolution.min_confidence`、`telemetry.sample_rate` 范围
- ANP 端口有效
- 会话/记忆/待办/制品后端有效
- 记忆评分权重在 `[0, 1]` 范围内
- 清理阈值逻辑正确
- 应用克隆工作线程数 >= 1

**Warnings() — 非致命警告：**
- 无提供者配置
- 记忆自动提取但无默认提供者
- Cortex 启用但嵌入模型为空
- 回忆混合搜索模式但无嵌入模型
- 上下文压缩启用但阈值未配置
- OKF 配置警告
- ANP 配置警告（DID 域为空、E2EE 需要元协议）
- 网关通道配置警告
- URL 格式检查

### 2.3 查询助手

- `FindProvider(name)` — 按名称查找提供者
- `DefaultProviderConfig()` — 获取默认提供者
- `EffectiveLightweightModel()` — 获取有效轻量模型（`LightweightModel` → 默认提供者模型）
- `EffectiveLightweightProvider()` — 获取有效轻量提供者（`LightweightProvider` → `DefaultProvider`）
- `EnabledExtensions()` — 获取已启用的扩展
- `FindExtension(name)` — 按名称查找扩展

## 3. Agent 核心循环实现

### 3.1 CoreLoop 结构

位于 `e:\myVibeCoding\km269\wukong\internal\agent\loop.go`。

```go
type CoreLoop struct {
    agent          agent.Agent
    runner         runner.Runner
    sessionService session.Service
    memoryService  memory.Service
    factory        *provider.Factory
    cfg            *config.WukongConfig
    contextMgr     *ContextManager
    security       *security.Guard
    recallStore    *recall.Store
    cortexStore    *cortex.CortexStore  // 可选: HNSW 向量同步
    memoryFlow     *cortex.MemoryFlowService
    graphFlow      *cortex.GraphFlowService  // 可选: KG 自动提取
    closeFn        func() error
    // ... 同步保护字段
}
```

### 3.2 CoreLoopConfig 依赖注入

```go
type CoreLoopConfig struct {
    Config                *config.WukongConfig
    Factory               *provider.Factory
    SessionService        session.Service
    MemoryService         memory.Service
    ArtifactService       artifact.Service
    ToolSets              []tool.ToolSet
    FunctionTools         []tool.Tool
    SecurityGuard         *security.Guard
    RecallStore           *recall.Store
    CortexStore           *cortex.CortexStore
    RevisionModel         RevisionModel
    MemoryFlowService     *cortex.MemoryFlowService
    GraphFlowService      *cortex.GraphFlowService
    TopOfMindInstructions string
    TelemetryShutdown     func(context.Context) error
    MemoryClose           func() error
    EvolutionClose        func() error
    DBPoolClose           func() error
    WorkingDir            string
    SessionID             string
    UserID                string
}
```

### 3.3 核心方法

**NewCoreLoop(cfg) — 创建核心循环：**
1. 收集所有工具（FunctionTools + Recipe 子代理 + todo_write）
2. 根据工作流模式创建 Agent（单 Agent 或 WorkflowBuilder）
3. 创建 Runner，配置会话/记忆/制品服务
4. 可选：配置 ToolSearch 插件（自动工具过滤）
5. 可选：配置 Guardrail 插件（提示注入检测）
6. 可选：配置 TodoEnforcer 插件
7. 配置 EvolutionTracker 插件
8. 创建 ContextManager
9. 构建 closeFn 关闭链

**Run(ctx, userID, sessionID, message) — 执行代理循环：**
1. 检查循环是否已关闭
2. 创建 OTel 追踪 span
3. 调用 `contextMgr.PrepareContext()` 优化上下文
4. 存储用户消息到 recall/cortex 存储
5. 记录 MemoryFlow 对话（IngestTurn + WakeUp）
6. 搜索回忆历史并注入上下文
7. 注入持久记忆（tRPC Memory），与 MemoryFlow 唤醒上下文去重
8. 调用 `runner.Run()` 执行代理
9. 返回事件通道

**RunStream(ctx, userID, sessionID, message, onEvent) — 流式执行：**
1. 调用 `Run()` 获取事件通道
2. 遍历事件，调用 `onEvent` 回调
3. 收集流式内容和工具调用
4. 存储助手回复到 recall/cortex
5. 存储工具调用和响应到 recall/cortex
6. 记录 MemoryFlow 助手对话
7. MemoryFlow → tRPC Memory 事实提升（后台 goroutine）
8. GraphFlow 自动提取（后台 goroutine）
9. 调用 `contextMgr.AfterRun()` 优化上下文

**Close() — 优雅关闭：**
1. 等待所有 in-flight RunStream 完成
2. 等待所有后台 goroutine 完成
3. 调用 closeFn（关闭链：Runner → Evolution → Memory → Session → GraphFlow → Telemetry → DBPool）

### 3.4 上下文管理 (context.go)

位于 `e:\myVibeCoding\km269\wukong\internal\agent\context.go`。

**ContextManager** 管理上下文压缩和会话摘要：
- **PrepareContext()** — 运行前准备上下文（令牌计数、压缩触发检查）
- **AfterRun()** — 运行后优化（压缩会话历史、触发 LLM 摘要）
- 支持两种压缩策略：LLM 摘要和简单截断
- 冷却期控制（`summary_cooldown`）

### 3.5 Recipe 系统 (recipe*.go)

位于 `e:\myVibeCoding\km269\wukong\internal\agent\recipe*.go`。

- **RecipeToolSet** — 将 YAML 配方文件加载为子代理工具
- **串联模式** — 按顺序执行步骤
- **并联模式** — 并行执行步骤
- **组合模式** — 支持嵌套组合
- **指标收集** — `recipe_metrics.go` 记录执行时间、成功率等

### 3.6 工作流 (workflow.go)

位于 `e:\myVibeCoding\km269\wukong\internal\agent\workflow.go`。

- **WorkflowBuilder** — 根据模式构建多代理编排
- 支持模式：`single`、`chain`、`parallel`、`cycle`、`graph`、`team_coordinator`、`team_swarm`
- 团队协作模式允许多个代理协同工作

## 4. 扩展系统实现

### 4.1 Manager 结构

位于 `e:\myVibeCoding\km269\wukong\internal\extension\manager.go`。

```go
type Manager struct {
    toolSets map[string]tool.ToolSet
    status   map[string]ExtensionInfo
    cfg      *config.WukongConfig
    ardTS    *ard.ToolSet  // 可选 ARD 集成
}
```

### 4.2 扩展生命周期

**Initialize(ctx) — 初始化：**
1. 获取所有已启用扩展
2. 收集 MCP Broker 扩展（`ext.Type == "external" && ext.MCPBroker`）
3. 逐个注册非 Broker 扩展
4. 创建 MCP Broker（4 个工具：`mcp_list_servers`、`mcp_list_tools`、`mcp_inspect_tools`、`mcp_call`）

**注册流程：**
- `registerExtension()` → 根据类型分派
- `registerBuiltinLocked()` — 调用 `CreateBuiltinToolSet()` 创建内置工具集
- `registerExternalLocked()` — 创建 MCP 客户端连接外部服务器

**EnableExtension/DisableExtension** — 动态启用/禁用扩展

**Close()** — 关闭所有扩展

### 4.3 内置扩展工厂 (factory.go)

位于 `e:\myVibeCoding\km269\wukong\internal\extension\factory.go`。

`CreateBuiltinToolSet(name, cfg)` 按名称创建内置扩展：

| 名称 | 函数 | 说明 |
|------|------|------|
| `developer` | `NewDeveloperToolSet()` | 开发者工具（命令执行、文件读写） |
| `computer_controller` | `NewComputerControllerToolSet(cfg)` | 浏览器控制 |
| `memory` | `NewMemoryToolSet(cfg)` | 记忆管理 |
| `auto_visualiser` | `NewVisualiserToolSet(cfg)` | 可视化 |
| `tutorial` | `NewTutorialToolSet(cfg)` | 教程 |
| `web` | `NewWebToolSet(cfg)` | 网页搜索 |
| `ard` | `NewARDToolSet()` | 资源发现 |
| `cortex` | `NewCortexToolSet(cfg)` | 知识搜索 |
| `agent_tools` / `apps` / `code_mode` / `top_of_mind` | — | 返回 nil，在 bootstrapSession 中注入运行时依赖 |

### 4.4 内置扩展注册 (builtin/registry.go)

位于 `e:\myVibeCoding\km269\wukong\internal\extension\builtin\registry.go`。

`RegisterBuiltins(cfg)` 自动注册 12 个内置扩展到配置（如果未在 YAML 中显式配置）：
- `developer`（始终启用）
- `computer_controller`（取决于 `cfg.Browser.Enabled`）
- `memory`（始终启用）
- `auto_visualiser`（取决于 `cfg.Visualiser.Enabled`）
- `tutorial`（取决于 `cfg.Tutorial.Enabled`）
- `top_of_mind`（取决于 `cfg.TopOfMind.Enabled`）
- `code_mode`（取决于 `cfg.CodeMode.Enabled`）
- `apps`（取决于 `cfg.Apps.Enabled`）
- `web`（始终启用）
- `agent_tools`（始终启用）
- `ard`（取决于 `cfg.ARD.Enabled`）
- `cortex`（取决于 `cfg.Cortex.Enabled`）

### 4.5 外部 MCP 扩展

位于 `e:\myVibeCoding\km269\wukong\internal\extension\mcp_client.go` 和 `mcp_server.go`。

**支持的传输方式：**
- **stdio** — 通过标准输入/输出启动子进程通信
- **HTTP/SSE** — 通过 HTTP 端点连接 MCP 服务器

**环境变量覆盖：**
- 外部扩展的 `env` 字段可设置环境变量
- MCP 连接配置包含命令、参数、环境变量

**MCP Broker 模式：**
- 4 个工具：`mcp_list_servers`、`mcp_list_tools`、`mcp_inspect_tools`、`mcp_call`
- 按需发现，避免将所有工具暴露给代理

**Deeplink 注册：**
- 格式：`wukong://extension?name=xxx&type=external&...`

### 4.6 ARD 自动注册

- 外部 MCP 服务器自动注册到 ARD 目录
- 构建 URN 标识符：`urn:air:wukong.local:mcp:{name}`

## 5. 服务端点实现

### 5.1 AG-UI Server (agui.go)

位于 `e:\myVibeCoding\km269\wukong\internal\server\agui.go`。

**协议：** SSE（Server-Sent Events）

**端点：**
- `POST /agui` — 聊天请求
- `GET /health` — 健康检查

**请求格式：**
```json
{
  "user_id": "optional",
  "session_id": "optional",
  "message": "required"
}
```

**事件类型：**
- `text_delta` — 流式文本内容
- `tool_calls` — 工具调用
- `error` — 错误信息
- `done` — 完成事件（包含 `session_id` 和 `full_text`）

**安全：** `ApplySecurity()` 包装（TLS + Auth + RateLimit）

### 5.2 ACP Server (acp.go)

位于 `e:\myVibeCoding\km269\wukong\internal\server\acp.go`。

**Agent Client Protocol 端点：**
- `GET /acp/health` — 健康检查
- `POST /acp/message/send` — 发送消息
- `GET /acp/tools/list` — 列出可用工具（Agent Card）
- `POST /acp/tools/call` — 直接调用工具
- `GET /acp/.well-known/agent.json` — 代理能力发现

**流式支持：** SSE 流式输出，回退非流式 JSON

### 5.3 安全层 (security.go)

位于 `e:\myVibeCoding\km269\wukong\internal\server\security.go`。

**TLS/mTLS 配置：**
- `ServerTLSConfig` 包含 `Enabled`、`CertFile`、`KeyFile`、`CACertFile`
- `BuildTLSConfig()` 加载证书，支持 CA 证书验证（mTLS）
- 最低 TLS 1.2，配置安全密码套件

**API Key 认证：**
- 从 `X-API-Key` 请求头或 `api_key` 查询参数读取
- `apiKeyMiddleware()` 比较预期值

**JWT 认证：**
- 从 `Authorization: Bearer <token>` 读取
- HMAC-SHA256 签名验证
- `validateJWTToken()` 解析和验证

**速率限制：**
- `TokenBucket` 实现（IP 粒度）
- 可配置 `MaxPerMinute` 和 `Window`
- 支持 `X-Forwarded-For` 头

**组合链：** `ApplySecurity()` 组合 RateLimit → Auth → TLS

## 6. 消息网关实现

### 6.1 GatewayServer 架构

位于 `e:\myVibeCoding\km269\wukong\internal\gateway\gateway.go`。

**传输无关设计：** 无 HTTP 监听器，每个通道拥有自己的入站传输。

**处理管道：**
1. **去重** — 基于 MessageID 的去重（防止平台 SDK 重试）
2. **ID 构建** — 通过通道构建 Wukong 用户/会话标识
3. **速率限制** — 每用户滑动窗口 + 全局并发门控
4. **会话映射** — 持久化会话映射
5. **异步 Agent 执行** — 后台 goroutine 执行代理
6. **回复发送** — 将回复发送回平台

**OTel 追踪：** 使用 `wukong/gateway` tracer

### 6.2 Feishu Channel (feishu/channel.go)

位于 `e:\myVibeCoding\km269\wukong\internal\gateway\feishu\channel.go`。

- WebSocket 长连接到飞书开放平台
- 无需公网回调 URL，支持内网部署
- Lark SDK 自动处理认证/重连/心跳
- 流式卡片回复（周期性更新）
- 文本回复（收集完整后发送）

### 6.3 速率限制 (ratelimit.go)

位于 `e:\myVibeCoding\km269\wukong\internal\gateway\ratelimit.go`。

- `RateLimiter` 结构：每用户滑动窗口 + 全局并发门控
- `maxPerUser` 和 `maxConcurrent` 可配置
- 支持上下文取消

### 6.4 消息去重 (dedup.go)

位于 `e:\myVibeCoding\km269\wukong\internal\gateway\dedup.go`。

- `MessageDeduplicator` 基于 `MessageID` 的 TTL 去重
- 防止平台 SDK 重试导致重复处理

## 7. 提供者系统实现

### 7.1 Factory 创建模型

位于 `e:\myVibeCoding\km269\wukong\internal\provider\factory.go`。

**CreateModel(name)** — 按名称创建模型：
1. 查找提供者配置
2. 填充默认 Base URL（如未配置）
3. 根据 `type` 分派：
   - `openai`/`anthropic`/`google`/`deepseek`/`ollama`/`lmstudio`/`vllm` → `createOpenAI()`
   - `acp` → `createACP()`

**CreateModelWithName(providerName, modelName)** — 覆盖模型名创建

**CreateDefaultModel()** — 创建默认提供者模型（`CreateModel("")`）

**CreateRevisionModel()** — 创建摘要模型，解析顺序：
1. `revision.revision_provider` / `revision.revision_model`
2. `lightweight_provider` / `lightweight_model`
3. `default_provider` / 其模型

### 7.2 提供者路由

| 类型 | 路由实现 | 说明 |
|------|---------|------|
| `openai` | `openai.New()` | OpenAI 兼容 API |
| `anthropic` | `openai.New()` | Anthropic API（OpenAI 兼容） |
| `google` | `openai.New()` | Google Gemini API（OpenAI 兼容） |
| `deepseek` | `openai.New()` | DeepSeek API |
| `ollama` | `openai.New()` | Ollama 本地 API |
| `lmstudio` | `openai.New()` | LM Studio 本地 API |
| `vllm` | `openai.New()` | vLLM 本地 API |
| `acp` | `NewACPProvider()` | ACP 远程代理（通过 MCP 桥接） |

### 7.3 默认 Base URL

| 提供者 | 默认 Base URL |
|--------|--------------|
| openai | `https://api.openai.com/v1` |
| anthropic | `https://api.anthropic.com/v1` |
| google | `https://generativelanguage.googleapis.com/v1beta/openai` |
| deepseek | `https://api.deepseek.com/v1` |
| ollama | `http://localhost:11434/v1` |
| lmstudio | `http://localhost:1234/v1` |
| vllm | `http://localhost:8000/v1` |

### 7.4 ACP 提供者 (acp.go)

位于 `e:\myVibeCoding\km269\wukong\internal\provider\acp.go`。

- 连接到远程 ACP 兼容代理
- MCP 桥接：通过 MCP 服务器暴露远程代理的工具
- 通过 `SetACPMCPAddr()` 设置 MCP 桥接地址

## 8. 搜索系统实现

### 8.1 SearchGenome 参数

位于 `e:\myVibeCoding\km269\wukong\internal\search\genome.go`。

```go
type SearchGenome struct {
    RecallMode          string   // lexical | vector | hybrid
    DenseWeight         float64  // 语义权重
    TextWeight          float64  // 关键词权重
    KeywordMatchPercent float64  // 关键词匹配阈值
    MaxRetrievedNum     int      // 最终 TopK
    FTS5PoolSize        int      // 候选池大小
    FusionMethod        string   // weighted | rrf
    RRFK                float64  // RRF 平滑常数（默认 60）
    RerankerEnabled     bool     // Cross-Encoder 重排序
    RerankerTopN        int      // 重排序 TopN
    MMREnabled          bool     // MMR 多样性
    MMRLambda           float64  // MMR 多样性参数
}
```

**默认基因组：** 混合模式，70% 语义 + 30% BM25，候选池大小 50，TopK 10

### 8.2 CortexStore 搜索流程

位于 `e:\myVibeCoding\km269\wukong\internal\cortex\store.go`。

**Search() 流程：**
1. 垂直路由（可选）— 意图检测，路由到特定后端
2. 模式选择 — 根据 RecallMode 选择搜索策略
3. 执行搜索 — 词汇/向量/混合
4. 融合 — Weighted 或 RRF 融合
5. 重排序（可选）— Cross-Encoder 重排序
6. MMR（可选）— 多样性保证
7. 返回结果

### 8.3 混合搜索实现 (searchHybridCortex)

1. **FTS5 词汇检索** — 构建候选池（`FTS5PoolSize`）
2. **HNSW 向量搜索** — 使用嵌入模型进行语义搜索
3. **RRF 或 Weighted 融合** — 融合词汇和向量结果
4. **Cross-Encoder 重排序**（可选）— 精确相关性重排
5. **MMR 多样性**（可选）— 避免结果聚类
6. **排序返回 TopK**

### 8.4 垂直搜索 (vertical/)

位于 `e:\myVibeCoding\km269\wukong\internal\search\vertical\`。

- **意图检测** — 中文/英文关键词匹配
- **后端** — arXiv、GitHub、Wikipedia、Reddit
- **合并模式** — `prepend`、`append`、`replace`

### 8.5 语义分块 (chunking/)

位于 `e:\myVibeCoding\km269\wukong\internal\search\chunking\`。

- 段落 → 句子 → 词边界
- 滑动窗口重叠
- 小分块合并
- CJK 支持

### 8.6 搜索调优 (tune/)

位于 `e:\myVibeCoding\km269\wukong\internal\search\tune\`。

- **AutoTune** — 自动搜索参数优化
- **多保真度优化** — 多种评估策略
- **LLM 评判** — 使用 LLM 评估搜索结果质量
- **评估用例** — 可配置的评估用例集

## 9. 浏览器自动化实现

### 9.1 双后端架构

- **Rod 后端**（默认）— 更丰富的 API，更好的稳定性（`rodbackend/`）
- **Chromedp 后端**（回退）— Rod 失败时自动切换（`backend.go`）
- **智能代理池** — `pool.go` 管理浏览器实例池

### 9.2 反检测机制

- **Stealth** — 隐藏自动化痕迹（`stealth/stealth.go`）
  - 修改 navigator.webdriver、chrome 等属性
  - 覆盖 WebGL 指纹、字体指纹
- **Antibot** — WAF 绕过（`antibot/`）
  - Prober 检测：HTTP 头探测、JS Challenge 探测、WAF 探测、速率限制探测、Robots 探测
  - Escalator：自动升级策略
  - TLS 指纹旋转（utls）
- **浏览器启动标志** — 禁用 Safe Browsing、禁用组件更新等

### 9.3 页面渲染

- `Render()`、`RenderWithReferer()` — 页面渲染
- 资源收集（CSS、JS、图片）
- 链接提取（包括动态生成）
- API 发现（XHR/Fetch 拦截）
- 渲染结算（Settle）

## 10. 安全系统实现

### 10.1 Guard 结构

位于 `e:\myVibeCoding\km269\wukong\internal\security\guard.go`。

```go
type Guard struct {
    cfg              *config.SecurityConfig
    approvedCommands map[string]bool
    blockedCount     atomic.Int64
    ignoreMatcher    *IgnoreMatcher
}
```

**4 种权限模式：**
- `auto` — 自动允许（完全信任）
- `smart` — 智能模式（默认，根据上下文判断）
- `manual` — 手动审批（每次操作前询问）
- `chat_only` — 仅聊天（禁止所有工具）

**核心方法：**
- `CheckToolPermission()` — 工具权限检查（允许/拒绝列表）
- `ValidateCommand()` — 命令验证（危险模式检查）
- `NeedsApproval()` — 是否需要审批
- `CheckFilePath()` — 文件路径检查（`.wukongignore`）

### 10.2 命令验证

- `isDangerousCommand()` — 检测 12+ 种危险模式：
  - `rm -rf /`、`dd if=/dev/zero`、`mkfs.`、`> /dev/sda`、fork bomb 等
- `ScanExtension()` — 3 层恶意软件扫描
- `isHighRiskOperation()` — 高风险工具分类
- `.wukongignore` 文件访问控制（gitignore 兼容语法）

## 11. 知识系统实现

### 11.1 CortexDB 栈

位于 `e:\myVibeCoding\km269\wukong\internal\cortex\`。

- **FTS5 全文搜索** — SQLite 内置全文搜索
- **HNSW 向量索引** — CortexDB（`github.com/liliang-cn/cortexdb/v2`）
- **混合搜索策略** — 词汇 + 向量融合
- **嵌入器** — `embedder.go` 调用嵌入 API
- **重排序器** — `reranker.go` Cross-Encoder 重排序

### 11.2 MemoryFlow

位于 `e:\myVibeCoding\km269\wukong\internal\cortex\memoryflow.go`。

**功能：**
- **对话记录** — `IngestTurn()` 记录每轮对话
- **唤醒上下文生成** — `WakeUp()` 构建跨会话上下文
- **事实提升** — `PromoteFacts()` 从对话中提取事实并提升到持久记忆

### 11.3 GraphFlow

位于 `e:\myVibeCoding\km269\wukong\internal\cortex\graphflow.go`。

**功能：**
- **实体/关系提取** — 使用 LLM 提取实体和关系
- **知识图谱构建** — `BuildGraph()` 构建图谱
- **自动提取** — 每轮对话后自动提取（`AutoExtract` 配置）
- **KG 工具** — `kg_tools.go` 提供查询和分析工具

### 11.4 OKF 集成

位于 `e:\myVibeCoding\km269\wukong\internal\okf\` 和 `cortex/okf_*.go`。

- **知识包导入/导出** — `bundle.go` 打包 OKF 格式
- **索引注入** — `okf_injector.go` 注入知识索引到 MemoryFlow
- **富化** — `okf_enrichment.go` LLM 驱动的知识富化

## 12. A2A 代理间通信实现

### 12.1 A2A Server

位于 `e:\myVibeCoding\km269\wukong\internal\summon\a2a.go`。

**基于 tRPC-Agent-Go 的 server/a2a：**
- `NewA2AServer()` 创建 A2A 服务器
- 两种模式：`WithAgent`（自动创建 Runner）、`WithAgentCard` + `WithRunner`
- 流式支持：`TaskArtifactUpdate` 事件
- AgentCard 自动生成（包含工具发现）

### 12.2 A2A Agent（客户端）

- `NewA2AAgentFromConfig()` 从配置创建远程代理代理
- 认证支持：API Key、JWT、OAuth2
- 状态传递

### 12.3 ANP 协议

位于 `e:\myVibeCoding\km269\wukong\internal\summon\anp_adapter.go` 和 `ard/anp_*.go`。

- **W3C DID 身份** — `DIDManager` 创建和管理 DID
- **元协议能力协商** — `MetaProtocol` 能力协商
- **E2EE 加密** — `E2EEMessenger` 端到端加密
- **RFC 9421 HTTP 签名** — `http_sign.go` HTTP 消息签名

## 13. 技能与进化系统实现

### 13.1 Skill Manager

位于 `e:\myVibeCoding\km269\wukong\internal\skill\manager.go`。

- 加载技能目录中的 SKILL.md 文件
- 自动加载
- 技能作为子代理注册到 Summon 系统

### 13.2 Evolution Engine

位于 `e:\myVibeCoding\km269\wukong\internal\evolution\engine.go`。

**架构：**
- **Analyzer（分析器）** — 分析技能使用模式，LLM 驱动的问题检测
- **Patcher（修补器）** — 生成和应用补丁到 SKILL.md 文件
- **Store（存储）** — 版本管理，SQLite 存储
- **OKF 导出** — 将进化结果导出为 OKF 格式

**生命周期：**
1. `RecordExecution()` 记录技能执行跟踪
2. 后台分析器分析跟踪数据
3. 检测到问题时生成补丁
4. 应用补丁后热重载技能

## 14. ARD 资源发现实现

### 14.1 Registry Server

位于 `e:\myVibeCoding\km269\wukong\internal\ard\server.go`。

**HTTP API：**
- `GET /.well-known/ai-catalog.json` — 目录入口
- `GET /api/v1/search` — 搜索资源
- `GET /api/v1/explore` — 浏览资源
- `GET /api/v1/agents` — 列出代理
- `GET /health` — 健康检查

**特性：**
- ANP 发现集成（`/.well-known/agent-descriptions`）
- Token Bucket 速率限制
- 联邦搜索（`federation.go`）
- 语义搜索（`semantic.go`）
- 信任评分（`trust.go`）
- 熔断器（`circuit_breaker.go`）

### 14.2 关键类型

- `CatalogEntry` — 目录条目
- `Registry` — 注册表
- `Federation` — 联邦配置
- `TrustScore` — 信任评分
- `CircuitBreaker` — 熔断器

## 15. 工具系统实现

### 15.1 内置工具详解

所有内置扩展位于 `e:\myVibeCoding\km269\wukong\internal\extension\builtin\`。

| 扩展名 | 文件 | 提供工具 |
|--------|------|---------|
| **developer** | `developer.go` | 命令执行、文件读写、代码搜索、文件编辑 |
| **computer_controller** | `computer_controller.go` | 浏览器导航、截图、点击、填写、选择、悬停、上传文件 |
| **memory** | `memory.go` | `memory_add`、`memory_search`、`memory_update`、`memory_delete`、`memory_load`、`memory_clear` |
| **auto_visualiser** | `auto_visualiser.go` | 图表/可视化生成 |
| **tutorial** | `tutorial.go` | 交互式教程 |
| **web** | `web.go` | 网页搜索（DuckDuckGo、SearXNG、Tavily、Google、Bing） |
| **code_mode** | `codemode.go` | JavaScript 代码执行沙箱 |
| **apps** | `apps.go` | 应用克隆、打包、管理 |
| **top_of_mind** | `topofmind.go` | 持久指令注入 |
| **agent_tools** | `agent.go` | 子代理工具（code-reviewer、summarizer、code-generator） |
| **ard** | `ard.go` | 资源发现工具 |
| **cortex** | `cortex.go` | 知识搜索工具 |
| **aggregate_search** | `aggregate_search.go` | 聚合搜索 |
| **google** | `google.go` | Google 搜索 |
| **bing** | `bing.go` | Bing 搜索 |
| **searxng** | `searxng.go` | SearXNG 搜索 |
| **tavily** | `tavily.go` | Tavily 搜索 |

### 15.2 MCP 外部工具

- 通过 MCP 协议连接外部服务器（stdio 或 HTTP/SSE）
- MCP Broker 模式：4 个 Broker 工具用于按需发现
- Deeplink 注册

## 16. 数据持久化

### 16.1 SQLite 数据库

- **所有子系统共享 `wukong.db`** — 会话、记忆、待办、回忆、CortexDB、进化
- **WAL 模式** — 写前日志，提高并发性能
- **数据库池** — `util.DatabasePool` 管理连接
- **FTS5 全文搜索** — 用于回忆和知识搜索

### 16.2 Redis（可选）

- 会话存储后端
- 记忆存储后端

## 17. 可观测性

### 17.1 OpenTelemetry

位于 `e:\myVibeCoding\km269\wukong\internal\telemetry\telemetry.go`。

- **Tracer** — `wukong/agent`（代理循环）、`wukong/gateway`（网关）
- **OTLP 导出** — 支持 gRPC 和 HTTP 导出
- **Span 属性** — `user_id`、`session_id`、`channel`、`message_id`
- **配置** — 采样率、导出端点、服务名

### 17.2 Health 端点

位于 `e:\myVibeCoding\km269\wukong\internal\health\health.go`。

- `/healthz` — 完整健康检查
- `/readyz` — 就绪检查（与 healthz 相同）
- `/livez` — 存活检查（始终 200）
- 端口 `:8086`

### 17.3 Langfuse

位于 `e:\myVibeCoding\km269\wukong\internal\observability\langfuse.go`。

- 可选集成
- 提供 LLM 调用追踪 UI
- 与 OpenTelemetry 关闭链合并