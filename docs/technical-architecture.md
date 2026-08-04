# Wukong 技术架构文档

## 1. 技术栈

### 1.1 编程语言与框架

- **Go 1.26**, 模块路径: `github.com/km269/wukong`
- **CLI 框架**: `spf13/cobra` v1.9.1 (30+ 子命令), `spf13/pflag` 标志解析
- **配置管理**: `spf13/viper` v1.20.1 (YAML + 环境变量 + CLI 标志三层覆盖)
- **核心 Agent 框架**: `trpc.group/trpc-go/trpc-agent-go` v1.10.0
  - Agent 模型: `agent/llmagent`, `agent/chainagent`, `agent/graphagent`, `agent/parallelagent`, `agent/cycleagent`
  - 编排: `graph`, `team`, `workflow`
  - 插件: `plugin/toolsearch`, `plugin/guardrail`
  - 规划器: `planner/builtin`, `planner/react`
  - 技能: `skill`
  - 知识: `knowledge`
  - 评估: `eval`
- **MCP 协议**: `trpc.group/trpc-go/trpc-mcp-go` v0.0.16（原生 MCP 客户端）
- **A2A 协议**: `trpc.group/trpc-go/trpc-a2a-go` v0.2.5（间接依赖，通过 `trpc-agent-go/server/a2a` 使用）

### 1.2 存储

| 类型 | 库 | 版本 | 用途 |
|------|-----|-------|------|
| 关系型 | `modernc.org/sqlite` | v1.38.2 | 纯 Go SQLite, 无 CGo 依赖, 会话/记忆/待办/召回共用 |
| 向量搜索 | `github.com/liliang-cn/cortexdb/v2` | v2.25.0 | HNSW 向量索引 + FTS5 全文搜索, 知识图谱, MemoryFlow |
| 缓存 | `redis/go-redis/v9` | v9.12.1 | 可选 Redis 会话后端 |
| 会话存储 | `trpc-agent-go/session/sqlite` | v1.10.0 | SQLite 持久化会话历史 |
| 记忆存储 | `trpc-agent-go/memory/sqlite` | v1.10.0 | SQLite 长时记忆持久化 |

### 1.3 浏览器自动化

| 组件 | 库 | 版本 | 用途 |
|------|-----|-------|------|
| 默认后端 | `go-rod/rod` | v0.116.2 | 高性能浏览器自动化 |
| 回退后端 | `chromedp/chromedp` | v0.15.1 | 备用浏览器自动化 |
| CDP 协议 | `chromedp/cdproto` | v0.0.0-20260321 | Chrome DevTools Protocol 类型 |
| TLS 指纹 | `refraction-networking/utls` | v1.5.0 | 浏览器 TLS 指纹模拟, 反检测 |
| 反检测引擎 | 内置 `internal/browser/antibot` | - | WAF/JS Challenge/速率限制/验证码/机器人检测与升级 |

### 1.4 LLM 提供商

- **OpenAI SDK**: `openai/openai-go` v1.12.0
- **框架级提供商**: `trpc-agent-go/model/openai`（OpenAI 兼容 API）
- **ACP Provider**: `internal/provider/acp.go`（通过 ACP 协议连接远程编码 Agent）
- **工厂模式**: `internal/provider/factory.go` 支持: openai, anthropic, google, deepseek, ollama, lmstudio, vllm, acp

### 1.5 安全

| 组件 | 库 | 版本 | 用途 |
|------|-----|-------|------|
| JWT | `golang-jwt/jwt/v5` | v5.3.1 | A2A/ACP 认证 |
| JOSE | `lestrrat-go/jwx/v2` | v2.1.4 | JWT 编解码 |
| 密码学 | `golang.org/x/crypto` | v0.51.0 | 通用加密 |
| 安全守卫 | 内置 `internal/security/guard.go` | - | 工具执行权限管理, 命令黑名单, 超时控制 |

### 1.6 通信协议

| 协议 | 库 | 版本 | 用途 |
|------|-----|-------|------|
| HTTP/2 | `golang.org/x/net` | v0.55.0 | HTTP 传输 |
| gRPC | `google.golang.org/grpc` | v1.81.1 | gRPC 通信 |
| QUIC | `quic-go/quic-go` | v0.37.4 | QUIC 传输 |
| WebSocket | `gobwas/ws` v1.4.0, `gorilla/websocket` v1.5.0 | - | 飞书长连接, TUI |
| SSE | 内置实现 | - | AG-UI, ACP 流式响应 |

### 1.7 可观测性

| 组件 | 库 | 版本 | 用途 |
|------|-----|-------|------|
| OpenTelemetry | `go.opentelemetry.io/otel` | v1.43.0 | 分布式追踪 |
| OTLP gRPC | `otlptracegrpc` | v1.29.0 | OTLP 导出器 |
| OTLP HTTP | `otlptracehttp` | v1.29.0 | OTLP HTTP 导出器 |
| Langfuse | `trpc-agent-go/telemetry/langfuse` | - | LLM 追踪 |
| 日志 | 标准库 `log/slog` + `go.uber.org/zap` v1.28.0 | - | 结构化日志 |

### 1.8 TUI 终端界面

- **Bubbletea** v1.3.10: Elm 架构的终端 UI 框架
- **Bubbles** v0.21.0: 预构建 UI 组件（textarea, viewport）
- **Lipgloss** v1.1.0: 样式定义
- **Glamour** v1.0.0: Markdown 渲染
- 三区布局: 对话区 / 工具审计区 / 输入区

### 1.9 其他关键依赖

| 组件 | 库 | 版本 | 用途 |
|------|-----|-------|------|
| 飞书 SDK | `larksuite/oapi-sdk-go/v3` | v3.9.7 | 飞书消息通道 |
| 压缩 | `klauspost/compress` | v1.18.6 | 数据压缩 |
| UUID | `google/uuid` | v1.6.0 | UUID 生成 |
| 速率限制 | `golang.org/x/time` | v0.15.0 | 令牌桶限速 |
| 并发池 | `panjf2000/ants/v2` | v2.10.0 | goroutine 池 |
| 机器人排除 | `temoto/robotstxt` | v1.1.2 | robots.txt 解析 |
| HTML→Markdown | `JohannesKaufmann/html-to-markdown/v2` | v2.5.2 | 网页内容转换 |
| HTML 解析 | `golang.org/x/net` | v0.55.0 | DOM 解析, Readability 算法, 搜索结果提取 |
| 语法高亮 | `alecthomas/chroma/v2` | v2.20.0 | 代码高亮 |
| 中文分词 | `go-ego/gse` | v1.0.2 | 中文分词搜索 |
| JS 运行时 | `dop251/goja` | v0.0.0 | 纯 Go JS 沙箱执行 |
| 腾讯云 COS | `tencentyun/cos-go-sdk-v5` | v0.7.69 | 对象存储 |

---

## 2. 包结构与依赖关系

### 2.1 包层次图

```
cmd/wukong/main.go
    |
    v
internal/cli/                      (cobra 命令树, 30 个子命令)
    |
    +---> internal/config/         (viper 配置管理, 12 个类型文件)
    |         |
    |         +---> internal/gateway/  (GatewayConfig 类型嵌入)
    |         +---> internal/server/   (ServerSecurityConfig 类型别名)
    |
    +---> internal/provider/       (LLM 工厂, ACP Provider)
    |         |
    |         +---> internal/config/
    |         +---> internal/util/
    |         +---> trpc-agent-go/model/openai
    |
    +---> internal/agent/          (CoreLoop, 上下文管理, 工作流编排)
    |         |
    |         +---> internal/config/
    |         +---> internal/provider/  (RevisionModel)
    |         +---> internal/cortex/    (CortexStore, MemoryFlow, GraphFlow)
    |         +---> internal/security/  (Guard)
    |         +---> internal/recall/
    |         +---> trpc-agent-go/agent/llmagent
    |         +---> trpc-agent-go/runner
    |         +---> trpc-agent-go/plugin/ (toolsearch, guardrail)
    |         +---> trpc-agent-go/planner/ (builtin, react)
    |         +---> trpc-agent-go/graph
    |         +---> trpc-agent-go/team
    |
    +---> internal/extension/      (MCP 扩展管理器)
    |         |
    |         +---> internal/extension/builtin/ (12 个内置扩展)
    |         +---> internal/ard/         (ARD 集成)
    |         +---> internal/config/
    |         +---> trpc-agent-go/tool/mcp
    |         +---> trpc-mcp-go
    |
    +---> internal/server/         (AG-UI, ACP, Security)
    |
    +---> internal/gateway/        (消息网关, 多平台通道)
    |         |
    |         +---> internal/gateway/feishu/ (飞书通道, WebSocket 长连接)
    |         +---> internal/agent/  (CoreLoop 作为 AgentRunner 接口)
    |
    +---> internal/cortex/         (CortexDB 知识栈, 20+ 文件)
    |         |
    |         +---> internal/recall/
    |         +---> internal/search/  (genome, vertical, chunking, metrics)
    |         +---> github.com/liliang-cn/cortexdb/v2
    |
    +---> internal/summon/         (A2A 代理间通信, 子代理委派)
    |         |
    |         +---> internal/config/
    |         +---> trpc-agent-go/server/a2a
    |         +---> trpc-agent-go/agent/a2aagent
    |
    +---> internal/security/       (安全守卫, 权限控制, .wukongignore)
    |
    +---> internal/ard/            (Agentic Resource Discovery, 20+ 文件)
    |         |
    |         +---> 联邦目录, 语义搜索, 信任验证, DID, URN, 注册表
    |
    +---> internal/evolution/      (技能自我进化, 追踪→分析→补丁→应用)
    |
    +---> internal/skill/          (技能管理, SKILL.md 加载, OKF 互操作)
    |
    +---> internal/knowledge/      (RAG 知识库管理, 文档源/嵌入/向量搜索)
    |
    +---> internal/okf/            (OKF 开放知识格式, 包/写入器)
    |
    +---> internal/health/         (健康检查端点)
    |
    +---> internal/telemetry/      (OpenTelemetry 管理器)
    |
    +---> internal/observability/  (Langfuse 增强可观测性)
    |
    +---> internal/eval/           (评估/回归测试框架)
    |
    +---> internal/project/        (工作目录追踪与会话恢复)
    |
    +---> internal/session/        (会话存储, 支持 sqlite/memory/redis)
    |
    +---> internal/memory/         (长时记忆存储, 自动提取/手动工具)
    |
    +---> internal/recall/         (跨会话聊天召回, FTS5 搜索)
    |
    +---> internal/todo/           (任务跟踪系统)
    |
    +---> internal/topofmind/      (持久指令注入, 文件监听+双检锁)
    |
    +---> internal/codemode/       (JS 沙箱执行, goja 引擎)
    |
    +---> internal/browser/        (浏览器自动化, 双后端)
    |         |
    |         +---> internal/browser/rodbackend/    (Rod 后端)
    |         +---> internal/browser/antibot/       (反检测引擎)
    |         +---> internal/browser/stealth/       (隐身脚本)
    |         +---> internal/browser/behavior/      (人类行为模拟)
    |         +---> internal/browser/settle/        (页面稳定检测)
    |         +---> internal/browser/retry/         (重试策略)
    |         +---> internal/browser/sanitize/      (内容清洗)
    |         +---> internal/browser/types/         (BrowserBackend 接口)
    |
    +---> internal/search/         (搜索策略与调优)
    |         |
    |         +---> internal/search/tune/       (AutoTune 自动调优)
    |         +---> internal/search/metrics/    (搜索可观测性)
    |         +---> internal/search/vertical/   (垂直搜索路由)
    |         +---> internal/search/chunking/   (语义分块)
    |
    +---> internal/apps/           (HTML 应用管理)
    |         |
    |         +---> internal/apps/clone/    (网站克隆)
    |         +---> internal/apps/pack/     (ZIM 打包)
    |         +---> internal/apps/sanitize/ (内容清洗)
    |         +---> internal/apps/mcpapps/  (MCP 应用桥接)
    |         +---> internal/apps/server/   (应用服务器)
    |
    +---> internal/util/           (共享工具: DatabasePool, 日志, 指针)
    |
    +---> internal/errsignal/      (错误信号分类, 爬虫/搜索错误处理)
    |
    +---> internal/artifact/       (制品存储工厂, 支持 inmemory/cos)

pkg/                              (可复用公共包)
    |
    +---> pkg/httpclient/          (HTTP 客户端, 连接池, DNS 缓存, 速率限制)
    +---> pkg/logutil/             (日志工具)
    +---> pkg/sandbox/             (沙箱, 各平台特定实现)
    +---> pkg/zim/                 (ZIM 文件格式, 阅读器/编解码/验证)
```

### 2.2 避免循环依赖的关键设计

- **config 包使用类型别名**: `config.ServerSecurityConfig` 是 `server.ServerSecurityConfig` 的类型别名 (`type alias`), 而 `config.GatewayConfig` 直接嵌入 `gateway.GatewayConfig` 结构体。这保持了依赖方向为 `config → server` 和 `config → gateway`, 避免了循环导入。
- **gateway 使用 AgentRunner 接口**: `gateway.AgentRunner` 接口（仅依赖 `trpc-agent-go` 的 `model` 和 `event` 类型）由 `agent.CoreLoop` 实现, 避免了 `gateway → agent → config → gateway` 的循环。
- **extension/manager 使用接口断言**: 扩展管理器在运行时通过接口断言获取具体的工具集, 避免编译时循环。
- **evolution → skill 通过接口**: `evolution.EngineConfig` 依赖 `SkillRefresher` 接口（由 `skill.Manager` 实现）, 避免 `evolution → skill → evolution` 循环。
- **cortex → recall 通过接口**: `cortex.CortexStore` 实现 `recall.Embedder` 接口, 保持单向依赖。

---

## 3. 核心类型与接口

### 3.1 核心接口

#### `agent.Agent` (tRPC-Agent-Go)
```go
type Agent interface {
    Info() agent.Info                    // 返回 Agent 元信息
    Tools(ctx context.Context) []tool.Tool  // 返回可用工具
    SystemPrompt(ctx context.Context) string  // 返回系统提示词
}
```
实现: `llmagent.LLMAgent`, `chainagent.ChainAgent`, `graphagent.GraphAgent`, `parallelagent.ParallelAgent`, `cycleagent.CycleAgent`, `DifyAgent`, `ACPProvider`

#### `runner.Runner`
```go
type Runner interface {
    Run(ctx, userID, sessionID, message) <-chan *event.Event
}
```
实现: `agent.CoreLoop`（包装 `runner.Runner`）

#### `model.Model`
```go
type Model interface {
    GenerateContent(ctx, request) <-chan *model.Response
}
```
实现: `openai.Model`, `ACPProvider`

#### `tool.Tool`
```go
type Tool interface {
    Declaration() tool.Declaration
    Call(ctx, args) (*tool.Result, error)
}
```
子类型: `function.FunctionTool`, `agenttool.AgentTool`

#### `tool.ToolSet`
```go
type ToolSet interface {
    Tools(ctx) []tool.Tool
    Name() string
    Close() error
}
```
实现: 各内置扩展的 ToolSet, `mcpbroker.BrokerToolSet`, `MCPClient`

#### `gateway.Channel`
```go
type Channel interface {
    Name() string
    Start(ctx, handler MessageHandler) error
    BuildUserID(msg *GatewayMessage) string
    BuildSessionID(msg *GatewayMessage) string
    SendReply(ctx, msg *GatewayMessage, reply *event.Event) error
}
```
实现: `feishu.FeishuChannel`

#### `gateway.AgentRunner`
```go
type AgentRunner interface {
    Run(ctx, userID, sessionID, message) (<-chan *event.Event, error)
}
```
实现: `agent.CoreLoop`

#### `types.BrowserBackend`
```go
type BrowserBackend interface {
    Render(ctx, url) (*RenderResult, error)
    RenderWithReferer(ctx, url, referer) (*RenderResult, error)
    DownloadAsset(ctx, url) (*CollectedAsset, error)
    Close() error
}
```
实现: `rodbackend.RodBackend`, `chromedp.Backend`（通过 `browser.Controller` 封装）

### 3.2 核心结构体

#### `WukongConfig` — 根配置结构体
- 位置: `internal/config/config.go`
- 字段数: 40+ 子配置字段
- 加载方式: Viper YAML + 环境变量 `WUKONG_` 前缀 + CLI 标志

#### `CoreLoop` — 主循环
- 位置: `internal/agent/loop.go`
- 依赖: `agent.Agent`, `runner.Runner`, `session.Service`, `memory.Service`, `provider.Factory`, `config.WukongConfig`, `ContextManager`, `security.Guard`, `recall.Store`, `cortex.CortexStore`, `MemoryFlowService`, `GraphFlowService`
- 并发控制: `sync.RWMutex` + `sync.WaitGroup` (bgWg, runWg)

#### `Guard` — 安全守卫
- 位置: `internal/security/guard.go`
- 状态: `sync.RWMutex` + `atomic.Int64` (blockedCount)
- 功能: 权限模式控制 (auto/manual/smart/chat_only), 工具执行超时, 危险命令阻止, 允许/拒绝列表, `.wukongignore` 文件访问黑名单

#### `Manager` — 扩展管理器
- 位置: `internal/extension/manager.go`
- 状态: `sync.RWMutex` 保护 `toolSets map[string]tool.ToolSet` 和 `status map[string]ExtensionInfo`
- 功能: 内置扩展注册, 外部 MCP 服务器连接管理, 动态启用/禁用, 权限控制, 审计日志

#### `CortexStore` — 向量+全文搜索存储
- 位置: `internal/cortex/store.go`
- 依赖: `CortexDB` (HNSW + FTS5), `Embedder`, `Reranker`, `vertical.Router`, `chunking.Chunker`, `metrics.SearchMetrics`, `lexicalStore`, `VectorCache`
- 搜索回退链: HNSW → FTS5 → 空结果

#### `AGUIServer` — AG-UI 协议服务器
- 位置: `internal/server/agui.go`
- 协议: SSE (Server-Sent Events)
- 端点: `POST /{path}`

#### `ACPServer` — ACP 协议服务器
- 位置: `internal/server/acp.go`
- 端点: `POST /acp/message/send`, `GET /acp/tools/list`, `POST /acp/tools/call`, `GET /acp/.well-known/agent.json`, `GET /acp/health`

#### `GatewayServer` — 消息网关
- 位置: `internal/gateway/gateway.go`
- 管道: 去重 → 用户/会话标识构建 → 速率限制 → 会话映射持久化 → Agent 执行 → 回复发送

#### `A2AServer` — A2A 协议服务器
- 位置: `internal/summon/a2a.go`
- 包装: `trpc-agent-go/server/a2a`

#### `EvolutionEngine` — 技能进化引擎
- 位置: `internal/evolution/engine.go`
- 生命周期: 执行追踪收集 → LLM 分析 → 补丁生成 → 应用 → 热重载

---

## 4. 配置结构详解

### 4.1 WukongConfig 字段列表

`WukongConfig` 定义在 `internal/config/config.go`，包含以下字段：

| 字段 | 类型 | 配置键 | 用途 |
|------|------|--------|------|
| `DefaultProvider` | `string` | `default_provider` | 默认 LLM 提供商名称 |
| `LogLevel` | `string` | `log_level` | 日志级别 (debug/info/warn/error) |
| `LightweightProvider` | `string` | `lightweight_provider` | 后台任务轻量提供商 |
| `LightweightModel` | `string` | `lightweight_model` | 后台任务轻量模型 |
| `Providers` | `[]ProviderConfig` | `providers` | LLM 后端配置列表 |
| `Extensions` | `[]ExtensionConfig` | `extensions` | MCP 扩展配置列表 |
| `Agent` | `AgentConfig` | `agent` | 核心 Agent 循环参数 |
| `Security` | `SecurityConfig` | `security` | 安全策略 |
| `Session` | `SessionConfig` | `session` | 会话存储 |
| `Memory` | `MemoryConfig` | `memory` | 长时记忆 |
| `Todo` | `TodoConfig` | `todo` | 任务追踪 |
| `Recall` | `RecallConfig` | `recall` | 聊天召回 |
| `Cortex` | `CortexConfig` | `cortex` | CortexDB 知识栈 |
| `MemoryFlow` | `MemoryFlowConfig` | `memoryflow` | 对话转录记录 |
| `GraphFlow` | `GraphFlowConfig` | `graphflow` | 知识图谱构建 |
| `ImportFlow` | `ImportFlowConfig` | `importflow` | 结构化数据导入 |
| `Revision` | `RevisionConfig` | `revision` | 上下文窗口管理 |
| `Browser` | `BrowserConfig` | `browser` | 浏览器自动化 |
| `Visualiser` | `VisualiserConfig` | `visualiser` | 图表生成 |
| `Tutorial` | `TutorialConfig` | `tutorial` | 交互式教程 |
| `TopOfMind` | `TopOfMindConfig` | `top_of_mind` | 持久指令注入 |
| `CodeMode` | `CodeModeConfig` | `code_mode` | JS 沙箱执行 |
| `Apps` | `AppsConfig` | `apps` | HTML 应用管理 |
| `ARD` | `ARDConfig` | `ard` | Agentic Resource Discovery |
| `Summon` | `SummonConfig` | `summon` | 子代理委派 |
| `ANP` | `ANPConfig` | `anp` | Agent Network Protocol |
| `Skill` | `SkillConfig` | `skill` | 技能系统 |
| `Evolution` | `EvolutionConfig` | `evolution` | 技能进化 |
| `Knowledge` | `KnowledgeConfig` | `knowledge` | RAG 知识库 |
| `OKF` | `OKFConfig` | `okf` | 开放知识格式 |
| `Dify` | `DifyConfig` | `dify` | Dify 平台集成 |
| `Workflow` | `WorkflowConfig` | `workflow` | 多模式编排 |
| `Gateway` | `gateway.GatewayConfig` | `gateway` | 消息网关 |
| `A2AServer` | `A2AServerConfig` | `a2a_server` | A2A 服务器 |
| `AGUI` | `AGUIConfig` | `agui` | AG-UI SSE 服务器 |
| `ACPServer` | `ACPServerConfig` | `acp_server` | ACP 服务器 |
| `ACPMCP` | `ACPMCPConfig` | `acp_mcp` | ACP-MCP 桥接 |
| `MCPServer` | `MCPServerConfig` | `mcp_server` | MCP 服务器 |
| `Telemetry` | `TelemetryConfig` | `telemetry` | OpenTelemetry |
| `Eval` | `EvalConfig` | `eval` | 评估系统 |
| `Artifact` | `ArtifactConfig` | `artifact` | 制品存储 |
| `Observability` | `ObservabilityConfig` | `observability` | Langfuse 观测 |
| `ProjectDir` | `string` | `project_dir` | 项目跟踪数据目录 |

### 4.2 环境变量展开支持

所有支持 `${ENV_VAR}` 和 `${ENV_VAR:-default}` 语法展开的字段（来自 `expandSecrets` 方法，`internal/config/config.go` 第 342-430 行）：

- **LLM 提供商**: `providers[].api_key`, `base_url`, `model`
- **A2A 远程代理**: `summon.a2a_remotes[].api_key`, `jwt_secret`, `oauth_client_secret`
- **飞书网关**: `gateway.feishu.app_secret`, `encrypt_key`, `verification_token`
- **Langfuse 观测**: `observability.langfuse_public_key`, `langfuse_secret_key`
- **制品存储**: `artifact.cos_secret_id`, `cos_secret_key`
- **ACP 服务器**: `acp_server.api_key`
- **CortexDB 嵌入**: `cortex.embedding_api_key`, `embedding_base_url`, `embedding_model`
- **CortexDB 重排序**: `cortex.reranker_api_key`, `reranker_base_url`, `reranker_model`
- **垂直路由**: `cortex.vertical_routing.github_api_key`
- **MemoryFlow**: `memoryflow.planner_model`, `memoryflow.extractor_model`
- **GraphFlow**: `graphflow.extractor_model`
- **Dify**: `dify.api_secret`
- **会话**: `session.redis_url`
- **搜索引擎**:
  - `browser.search.searxng.url`, `api_key`
  - `browser.search.tavily.api_key`
  - `browser.search.google.api_key`, `cse_id`
  - `browser.search.bing.api_key`

### 4.3 配置优先级

配置按以下优先级解析（从高到低）:

1. CLI 标志 (`--provider`, `--model`, `--temperature`, `--max-tokens`, `--config`)
2. 环境变量 (`WUKONG_` 前缀, 如 `WUKONG_DEFAULT_PROVIDER`)
3. YAML 配置文件 (`--config` 标志或默认搜索路径)
4. 内置默认值 (`setDefaults()` 方法, 12 个子系统默认值函数)

### 4.4 配置文件搜索路径

当未提供 `--config` 标志时, 按顺序搜索:
1. 当前目录 (`./config.yaml`)
2. `~/.config/wukong/config.yaml`
3. `/etc/wukong/config.yaml` (仅 Unix 系统)

---

## 5. 并发模型

### 5.1 CoreLoop 并发控制

- **`sync.WaitGroup` (bgWg)**: 跟踪后台 goroutine, 用于优雅关闭
- **`sync.WaitGroup` (runWg)**: 跟踪正在执行的 RunStream 调用的同步后处理（recall/cortex/MemoryFlow 写入）, 确保关闭前完成
- **`sync.RWMutex`**: 保护 `closed` 状态标志

### 5.2 扩展管理器

- **`sync.RWMutex`**: 保护 `toolSets map[string]tool.ToolSet` 和 `status map[string]ExtensionInfo`
- 读锁用于工具发现和状态查询, 写锁用于扩展注册/注销

### 5.3 安全 Guard

- **`sync.RWMutex`**: 保护 `approvedCommands map[string]bool`
- **`atomic.Int64`**: `blockedCount` 无锁原子计数

### 5.4 网关

- **`sync.WaitGroup`**: 跟踪通道 goroutine 生命周期
- 每个通道独立运行, 通过 `context.Context` 传播取消信号
- **`sync.RWMutex`**: 保护 `GatewayServer` 内部状态

### 5.5 速率限制

- **`TokenBucket`**: 使用 `sync.Mutex` 保护令牌桶状态
- 每个用户独立限速 + 全局并发门控

### 5.6 服务器

- **`sync.RWMutex`**: 保护服务器运行状态
- **`sync.Once`**: 确保只执行一次关闭逻辑

### 5.7 其他子系统

- **project.Manager**: `sync.RWMutex` 保护记录列表
- **EvolutionEngine**: `sync.Mutex` 保护补丁应用
- **EvolutionPatcher**: `sync.Mutex` 保护版本存储写入
- **VectorCache**: `sync.RWMutex` 保护缓存条目
- **TopOfMind**: `sync.RWMutex` 双检锁, 避免不必要文件重读
- **CodeMode Executor**: `sync.RWMutex` 保护执行器状态, 信号量限制最大并发 5
- **SmartProxyPool**: `sync.Mutex` 保护代理列表轮转

---

## 6. 错误处理模式

### 6.1 通用模式

- 所有公共函数返回 `error`
- 配置验证返回详细错误消息（URL 格式检查, 必填字段检查, 枚举值范围检查）
- 错误信号分类: `internal/errsignal` 包将错误分类为 7 类（Transient, RateLimited, Blocked, NoContent, InvalidInput, Permanent, Unknown）, 每类有推荐处理策略

### 6.2 搜索回退链

```
API 后端搜索 → 浏览器自动化搜索 → 空结果
```

**Web 搜索回退**（`aggregate_search.go`）：
1. 并发调用所有启用的 API 后端（DuckDuckGo/SearXNG/Tavily/Google/Bing + CortexStore）
2. 0 条结果 → `searchViaBrowser`：用浏览器自动化访问 6 个搜索引擎（Bing→Baidu→WeChat→Zhihu→DuckDuckGo→Google），解析 HTML DOM 提取结果
3. 仍无结果 → 返回错误

**本地知识搜索回退**（CortexStore）：
```
HNSW 向量搜索 → FTS5 全文搜索 → 空结果
```
当 HNSW 搜索返回结果不足时, 自动回退到 FTS5 搜索。

### 6.3 页面内容抓取回退链

```
Browser (JS渲染 + stealth) → Local Reader (Readability + Markdown) → HTTP GET + sanitize
```

三级回退确保页面内容抓取的高可用性：
1. **Browser**：chromedp/rod 后端，完整 JS 渲染 + 反检测，最慢但最完整
2. **Local Reader**：HTTP GET + 本地 Readability 算法提取主体内容 + HTML→Markdown 转换，无需外部 API
3. **HTTP GET**：简单 HTTP 请求 + `sanitize.ExtractMainContentMarkdown`，最后兜底

### 6.4 浏览器后端回退

```
Rod 后端 → Chromedp 后端
```

当 Rod 后端初始化失败时, 自动回退到 Chromedp 后端。

### 6.5 连接回退

```
浏览器渲染 → HTTP 客户端 → Wayback Machine
```

网页获取按优先级尝试: 浏览器渲染 → 基本 HTTP 获取 → Internet Archive Wayback Machine 回退。

### 6.6 网关错误回复

- 代理执行错误不会直接暴露给用户, 而是转为用户友好的错误消息
- 去重层丢弃平台 SDK 重试导致的重复消息（基于 MessageID）
- 速率限制拒绝返回友好提示

### 6.7 反检测升级

```
检测 → 分类 → 升级 → 重试
```

`antibot.Engine` 检测到反爬措施后, 通过 `Escalator` 升级到更高的隐身级别（5 个级别）, 包含延迟等待和重试。

---

## 7. 测试策略

### 7.1 单元测试

单元测试遍布各包:

| 包 | 测试文件 | 数量 |
|----|----------|------|
| `internal/agent` | `loop_test.go`, `context_test.go`, `prompt_template_test.go`, `recipe_test.go`, `recipe_advance_test.go`, `recipe_compose_test.go`, `recipe_metrics_test.go`, `evolution_tracker_test.go`, `workflow_test.go` | 9 个 |
| `internal/security` | `guard_test.go`, `ignore_test.go` | 2 个 |
| `internal/extension` | `manager_test.go`, `acp_mcp_test.go`, `deeplink_test.go`, `mcp_server_test.go` | 4 个 |
| `internal/extension/builtin` | `registry_test.go`, `toolset_test.go` | 2 个 |
| `internal/config` | `config_test.go` | 1 个 |
| `internal/session` | `store_test.go` | 1 个 |
| `internal/memory` | `store_test.go` | 1 个 |
| `internal/recall` | `recall_test.go`, `store_test.go` | 2 个 |
| `internal/cortex` | - | 通过 recall 测试覆盖 |
| `internal/search` | `metrics_test.go`, `genome_test.go` | 2 个 |
| `internal/search/chunking` | `chunking_test.go` | 1 个 |
| `internal/search/tune` | `autotune_test.go`, `optimizer_test.go`, `demo_test.go` | 3 个 |
| `internal/search/vertical` | `router_test.go` | 1 个 |
| `internal/browser` | `controller_test.go`, `pool_test.go` | 2 个 |
| `internal/browser/rodbackend` | `detect_test.go`, `api_discovery_test.go` | 2 个 |
| `internal/browser/antibot` | `detector_test.go`, `escalator_test.go` | 2 个 |
| `internal/browser/stealth` | `stealth_test.go` | 1 个 |
| `internal/summon` | `summon_test.go`, `auth_test.go` | 2 个 |
| `internal/ard` | `ard_test.go`, `federation_test.go`, `semantic_test.go`, `trust_test.go` | 4 个 |
| `internal/evolution` | `evolution_test.go` | 1 个 |
| `internal/gateway` | `gateway_test.go`, `ratelimit_test.go` | 2 个 |
| `internal/gateway/feishu` | `message_test.go`, `sender_test.go` | 2 个 |
| `internal/health` | `health_test.go` | 1 个 |
| `internal/project` | `manager_test.go` | 1 个 |
| `internal/todo` | `tool_test.go` | 1 个 |
| `internal/topofmind` | `mind_test.go` | 1 个 |
| `internal/codemode` | `executor_test.go` | 1 个 |
| `internal/telemetry` | `telemetry_test.go` | 1 个 |
| `internal/errsignal` | `errsignal_test.go` | 1 个 |
| `internal/provider` | `factory_test.go` | 1 个 |
| `internal/apps` | `manager_test.go` | 1 个 |
| `internal/apps/clone` | `bench_test.go`, `dedup_test.go`, `frontier_test.go`, `rewrite_test.go`, `session_test.go`, `urlx_test.go`, `archive_fallback_test.go`, `css_test.go` | 8 个 |
| `internal/apps/pack` | `packer_test.go` | 1 个 |
| `internal/apps/sanitize` | `cleaner_test.go` | 1 个 |
| `internal/okf` | `bundle_test.go` | 1 个 |
| `internal/util` | `util_test.go`, `logger_test.go` | 2 个 |
| `pkg/httpclient` | `bench_test.go` | 1 个 |
| `pkg/zim` | `zim_test.go`, `verify_zim_test.go` | 2 个 |

### 7.2 集成测试

- `internal/summon/summon_integration_test.go`: A2A 远程代理集成测试
- `internal/ard/ard_integration_test.go`: ARD 联调集成测试

### 7.3 基准测试

- `pkg/httpclient/bench_test.go`: HTTP 客户端基准测试
- `internal/ard/bench_test.go`: ARD 性能基准测试
- `internal/apps/clone/bench_test.go`: 网站克隆基准测试

### 7.4 配置测试

- `internal/config/config_test.go`: 配置加载、验证、环境变量展开、默认值测试