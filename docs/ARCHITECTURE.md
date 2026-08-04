# Wukong 系统架构文档

> 版本: 0.3.0 | 模块: `github.com/km269/wukong` | Go 版本: 1.26

---

## 1. 项目概述

Wukong 是一个**本地优先、可扩展的 AI Agent 平台**，基于以下核心框架构建：

- **tRPC-Agent-Go v1.10.0** — 提供 Agent 核心循环、Runner、LLM 模型、Session、Memory、Skill、Knowledge 等基础能力
- **tRPC-MCP-Go v0.0.16** — 提供 MCP (Model Context Protocol) 扩展系统，支持内置和外部扩展
- **tRPC-A2A-Go v0.2.5** — 提供 A2A (Agent-to-Agent) 协议支持，实现代理间通信

### 设计理念

- **本地优先 (Local-First)**：所有数据默认存储在本地 SQLite，无需外部服务即可运行
- **可扩展 (Extensible)**：通过 MCP 协议扩展系统，支持任意语言编写的扩展
- **多协议 (Multi-Protocol)**：同时支持 A2A、ACP、AG-UI、MCP 等多种协议
- **安全多层次 (Multi-Layer Security)**：权限模式 + 命令扫描 + 速率限制 + 认证

---

## 2. 整体架构图（ASCII Art）

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              CLI 层 (cobra)                                 │
│  session │ run │ server │ config │ provider │ memory │ search │ apps │ ...  │
└──────────────────────────┬──────────────────────────────────────────────────┘
                           │ cli.Execute()
                           ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          配置层 (Viper)                                      │
│  7级优先级: CLI标志 > 环境变量 > 配置文件 > 内置默认值                       │
│  支持 ${ENV_VAR} 和 ${VAR:-default} 环境变量展开                             │
│  验证: Validate() + Warnings()                                               │
└──────────────────────────┬──────────────────────────────────────────────────┘
                           │ bootstrapSession()
                           ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          核心 Agent 循环层                                    │
│                                                                             │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                       CoreLoop                                         │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────────┐  │   │
│  │  │ LLMAgent │  │ Runner   │  │ Session  │  │ Memory Service       │  │   │
│  │  │(tRPC)    │  │(tRPC)    │  │ Service  │  │(tRPC)                │  │   │
│  │  └──────────┘  └──────────┘  └──────────┘  └──────────────────────┘  │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────────┐  │   │
│  │  │Planner   │  │Context   │  │Session   │  │ Security Guard       │  │   │
│  │  │(react)   │  │Compaction│  │Recall    │  │(权限/命令扫描/超时)    │  │   │
│  │  └──────────┘  └──────────┘  └──────────┘  └──────────────────────┘  │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────────┐  │   │
│  │  │Revision  │  │MemoryFlow│  │GraphFlow │  │ Recipe / Workflow     │  │   │
│  │  │Model     │  │(CortexDB)│  │(CortexDB)│  │ (编排引擎)            │  │   │
│  │  └──────────┘  └──────────┘  └──────────┘  └──────────────────────┘  │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
└──────────────────────────┬──────────────────────────────────────────────────┘
                           │
          ┌────────────────┼────────────────┬──────────────────┐
          ▼                ▼                ▼                  ▼
┌─────────────────┐ ┌──────────┐ ┌──────────────┐ ┌──────────────────┐
│ 扩展系统 (MCP)   │ │ 提供者    │ │ 搜索系统      │ │ 浏览器自动化      │
│                 │ │ 系统     │ │              │ │                  │
│ Manager         │ │ Factory  │ │ SearchGenome │ │ Controller       │
│  ├─ Builtin(12) │ │  ├─openai│ │  ├─lexical   │ │  ├─ Rod (默认)  │
│  ├─ External    │ │  ├─anthropic│ ├─vector     │ │  ├─ Chromedp    │
│  └─ MCP Broker  │ │  ├─google │ │  ├─hybrid    │ │  └─ Stealth     │
│                 │ │  ├─deepseek│ │  ├─Vertical  │ │                  │
│ DeepLink 注册    │ │  ├─ollama │ │  ├─Reranker  │ │ 代理池/反检测    │
│ ARD 自动注册     │ │  ├─lmstudio│ │  └─AutoTune │ │                  │
└─────────────────┘ │  └─acp    │ └──────────────┘ └──────────────────┘
                    └──────────┘
          ┌────────────────┼────────────────┬──────────────────┐
          ▼                ▼                ▼                  ▼
┌─────────────────┐ ┌──────────┐ ┌──────────────┐ ┌──────────────────┐
│ 服务端点         │ │ 消息网关  │ │ A2A 代理间    │ │ 安全系统          │
│                 │ │         │ │ 通信          │ │                  │
│ ├─ AG-UI (SSE)  │ │ Gateway │ │ ├─ A2A Server │ │ Guard            │
│ ├─ ACP (5端点)  │ │ Server  │ │ ├─ A2A Agent  │ │  ├─ 权限模式(4)   │
│ ├─ ACP MCP      │ │  ├─去重  │ │ ├─ ANP        │ │  ├─ 恶意命令扫描  │
│ │  Bridge       │ │  ├─速率  │ │ │  (W3C DID)  │ │  ├─ 危险命令阻止  │
│ ├─ MCP Server   │ │  │ 限制  │ │ ├─ E2EE       │ │  ├─ 工具允许列表  │
│ ├─ Health       │ │  ├─会话  │ │ └─ HTTP签名   │ │  ├─ .wukongignore │
│ │ (/healthz)    │ │  │ 映射  │ │              │ │  └─ 超时控制     │
│ └─ ANP Server   │ │  └─Feishu│ │ Delegate 工具  │ │                  │
│                 │ │  Channel │ │              │ │ JWT / TLS / API Key│
└─────────────────┘ └──────────┘ └──────────────┘ └──────────────────┘

          ┌────────────────┼────────────────┬──────────────────┐
          ▼                ▼                ▼                  ▼
┌─────────────────┐ ┌──────────┐ ┌──────────────┐ ┌──────────────────┐
│ 存储层           │ │ 知识系统  │ │ 技能系统      │ │ 编排系统          │
│                 │ │         │ │              │ │                  │
│ ├─ SQLite       │ │ CortexDB│ │ Skill Manager│ │ Workflow         │
│ │  (session,    │ │  ├─HNSW  │ │  ├─ FS加载    │ │  ├─ single       │
│ │   memory,     │ │  │ 向量  │ │  ├─ SKILL.md  │ │  ├─ chain        │
│ │   todo,       │ │  │ 索引  │ │  └─ 热加载    │ │  ├─ parallel     │
│ │   recall)     │ │  ├─FTS5  │ │              │ │  ├─ cycle        │
│ ├─ Redis        │ │  │ 全文  │ │ Evolution    │ │  ├─ graph        │
│ │  (可选会话)    │ │  │ 搜索  │ │  Engine      │ │  └─ team         │
│ └─ CortexDB     │ │  ├─Memory│ │  ├─ 分析      │ │                  │
│   (HNSW + FTS5) │ │  │ Flow  │ │  ├─ 补丁      │ │ Recipe           │
│                 │ │  ├─Graph │ │  └─ 应用      │ │  ├─ 模板化       │
│                 │ │  │ Flow  │ │              │ │  ├─ 参数化       │
│                 │ │  ├─Import│ │ OKF 技能打包  │ │  ├─ 组合         │
│                 │ │  │ Flow  │ │              │ │  └─ 热加载       │
│                 │ │  └─OKF   │ │              │ │                  │
│                 │ │          │ │              │ │ Team             │
│                 │ │          │ │              │ │  ├─ Coordinator  │
│                 │ │          │ │              │ │  └─ Swarm        │
└─────────────────┘ └──────────┘ └──────────────┘ └──────────────────┘

          ┌────────────────┼────────────────┬──────────────────┐
          ▼                ▼                ▼                  ▼
┌─────────────────┐ ┌──────────┐ ┌──────────────┐ ┌──────────────────┐
│ ARD 资源发现     │ │ 工具系统  │ │ 可观测性      │ │ 沙箱/安全         │
│                 │ │         │ │              │ │                  │
│ ├─ 目录服务     │ │ 内置工具 │ │ OpenTelemetry │ │ Sandbox          │
│ ├─ 联邦搜索     │ │  (12个)  │ │  ├─ Tracing   │ │  ├─ 文件系统保护  │
│ ├─ 语义搜索     │ │ MCP外部  │ │  ├─ Metrics   │ │  └─ 平台适配     │
│ ├─ 信任评分     │ │ 工具     │ │  └─ OTLP导出  │ │                  │
│ ├─ 断路器      │ │ 工具搜索 │ │              │ │ pkg/             │
│ └─ ANP发现     │ │ 权限控制 │ │ Langfuse      │ │  ├─ httpclient   │
│                 │ │         │ │ 集成          │ │  ├─ logutil      │
│                 │ │ DeepLink│ │              │ │  ├─ sandbox      │
│                 │ │ 注册    │ │ Health        │ │  └─ zim          │
│                 │ │         │ │ 端点          │ │                  │
└─────────────────┘ └──────────┘ └──────────────┘ └──────────────────┘
```

---

## 3. 核心架构层次

### 3.1 入口层 (`cmd/wukong/main.go`)

主入口是极简设计，仅包含：

```go
func main() {
    if err := cli.Execute(); err != nil {
        fmt.Fprintf(os.Stderr, "wukong: %v\n", err)
        os.Exit(1)
    }
}
```

核心特点：
- **无框架初始化**：不直接初始化任何子系统
- **完全委托**：所有逻辑委托给 `internal/cli` 包
- **错误处理**：统一错误输出到 stderr，退出码 1

### 3.2 CLI 层 (`internal/cli/`)

基于 **cobra 框架**，约 30+ 子命令，是用户与系统交互的唯一入口。

#### 主要命令

| 命令 | 功能 | 说明 |
|------|------|------|
| `session` | 交互式会话 | TUI 模式，支持 resume |
| `run` | 单次执行 | 单次/对话模式，支持 stdin pipe |
| `server` | 服务模式 | 启动所有协议端点 |
| `config` | 配置管理 | 查看/编辑配置 |
| `provider` | 提供者管理 | 管理 LLM 提供者 |
| `memory` | 记忆管理 | 查看/管理长期记忆 |
| `search` | 搜索调优 | 搜索参数调优 |
| `apps` | 应用管理 | 网站克隆/打包 |
| `skill` | 技能管理 | 管理技能 |
| `recipe` | Recipe 管理 | 管理工作流模板 |
| `evolution` | 进化管理 | 管理技能自我进化 |
| `cortex` | Cortex 管理 | 管理知识存储 |
| `knowledge` | 知识管理 | 管理 RAG 知识库 |
| `ard` | ARD 管理 | 代理资源发现管理 |
| `health` | 健康检查 | 系统健康状态 |
| `eval` | 评估 | 回归测试 |
| `bench` | 基准测试 | 性能基准测试 |
| `extension` | 扩展管理 | 管理 MCP 扩展 |
| `todo` | 任务管理 | 管理待办事项 |
| `env` | 环境变量 | 查看环境变量 |
| `init` | 初始化 | 初始化项目配置 |
| `version` | 版本 | 显示版本信息 |

#### 全局标志

- `--debug` / `-D`：启用调试日志
- `--quiet`：静默模式（仅警告和错误）

#### 服务模式 (`wukong server`)

`runServer()` 函数启动所有协议端点：
1. 加载配置 → `bootstrapSession()` 初始化所有子系统
2. 构建健康检查注册表
3. 启动健康检查 HTTP 服务 (`:8086/healthz`, `/livez`, `/readyz`)
4. 启动配置的协议服务（A2A、ACP、AG-UI、ACP MCP）
5. 等待 OS 信号（SIGINT/SIGTERM）进行优雅关闭

### 3.3 配置层 (`internal/config/`)

基于 **Viper** 的配置管理，是整个系统的配置中枢。

#### 配置优先级（7 级，从高到低）

1. **CLI 标志**：`--provider`, `--model`, `--temperature`, `--max-tokens`, `--config`
2. **环境变量**：`WUKONG_` 前缀（如 `WUKONG_DEFAULT_PROVIDER`）
3. **指定配置文件**：`--config` 标志指定的文件
4. **当前目录**：`./config.yaml`
5. **用户配置目录**：`~/.config/wukong/config.yaml`
6. **系统配置目录**：`/etc/wukong/config.yaml`（仅 Unix）
7. **内置默认值**：`setDefaults()` 函数注册

#### 配置结构

`WukongConfig` 根结构包含约 30+ 子配置：

| 字段 | 类型 | 说明 |
|------|------|------|
| `DefaultProvider` | string | 默认 LLM 提供者名称 |
| `LogLevel` | string | 日志级别 (debug/info/warn/error) |
| `LightweightProvider` | string | 轻量级提供者（后台任务） |
| `LightweightModel` | string | 轻量级模型（后台任务） |
| `Providers` | []ProviderConfig | LLM 提供者列表 |
| `Extensions` | []ExtensionConfig | MCP 扩展配置 |
| `Agent` | AgentConfig | Agent 核心参数 |
| `Security` | SecurityConfig | 安全策略 |
| `Session` | SessionConfig | 会话存储 |
| `Memory` | MemoryConfig | 长期记忆 |
| `Todo` | TodoConfig | 任务跟踪 |
| `Recall` | RecallConfig | 跨会话搜索 |
| `Cortex` | CortexConfig | CortexDB 知识存储 |
| `MemoryFlow` | MemoryFlowConfig | 对话转录记录 |
| `GraphFlow` | GraphFlowConfig | 知识图谱构建 |
| `ImportFlow` | ImportFlowConfig | 结构化数据导入 |
| `Revision` | RevisionConfig | 上下文窗口管理 |
| `Browser` | BrowserConfig | 浏览器自动化 |
| `Visualiser` | VisualiserConfig | 图表生成 |
| `Tutorial` | TutorialConfig | 交互式教程 |
| `TopOfMind` | TopOfMindConfig | 持久指令注入 |
| `CodeMode` | CodeModeConfig | JS 代码执行沙箱 |
| `Apps` | AppsConfig | HTML 应用 |
| `ARD` | ARDConfig | 代理资源发现 |
| `Summon` | SummonConfig | 子代理委托 |
| `ANP` | ANPConfig | 代理网络协议 |
| `Skill` | SkillConfig | 技能系统 |
| `Evolution` | EvolutionConfig | 技能自我进化 |
| `Knowledge` | KnowledgeConfig | RAG 知识检索 |
| `OKF` | OKFConfig | 开放知识格式 |
| `Dify` | DifyConfig | Dify AI 平台集成 |
| `Workflow` | WorkflowConfig | 多模式编排 |
| `Gateway` | GatewayConfig | 消息网关 |
| `A2AServer` | A2AServerConfig | A2A 协议服务器 |
| `AGUI` | AGUIConfig | AG-UI SSE 服务器 |
| `ACPServer` | ACPServerConfig | ACP 协议服务器 |
| `ACPMCP` | ACPMCPConfig | MCP 桥接 |
| `MCPServer` | MCPServerConfig | 独立 MCP 服务器 |
| `Telemetry` | TelemetryConfig | OpenTelemetry 可观测性 |
| `Eval` | EvalConfig | 评估系统 |
| `Artifact` | ArtifactConfig | 制品存储 |
| `Observability` | ObservabilityConfig | Langfuse 集成 |

#### 环境变量展开

`expandSecrets()` 方法支持 `$ {ENV_VAR}` 和 `$ {VAR:-default}` 语法，应用于：
- 提供者 API Key、Base URL、Model
- A2A 远程密钥（API Key、JWT Secret、OAuth Secret）
- 飞书通道密钥（AppSecret、EncryptKey、VerificationToken）
- Langfuse 密钥
- 制品 COS 凭证
- ACP Server API Key
- CortexDB embedding/reranker 配置
- 垂直搜索 GitHub API Key
- MemoryFlow/GraphFlow 模型配置
- Dify API Secret
- Redis URL
- 搜索提供者密钥（SearXNG、Tavily、Google、Bing）

#### 配置加载流程

```
NewLoader(configPath)
  ├── 创建 Viper 实例
  ├── 设置配置文件搜索路径
  ├── 设置环境变量前缀 (WUKONG_)
  ├── setDefaults() 注册内置默认值
  │   ├── setGlobalDefaults()
  │   ├── setAgentDefaults()
  │   ├── setSecurityDefaults()
  │   ├── setStorageDefaults()
  │   ├── setCortexStackDefaults()
  │   ├── setRevisionDefaults()
  │   ├── setFeatureDefaults()
  │   ├── setAppsDefaults()
  │   ├── setOrchestrationDefaults()
  │   ├── setServerDefaults()
  │   ├── setGatewayDefaults()
  │   ├── setObservabilityDefaults()
  │   └── setOKFDefaults()
  └── ReadInConfig() 读取配置文件

Load()
  ├── Unmarshal() 解析到 WukongConfig
  ├── expandSecrets() 展开环境变量
  └── 缓存结果

LoadAndValidate()
  ├── Load()
  ├── Validate() 致命验证
  └── Warnings() 非致命警告
```

### 3.4 Agent 核心层 (`internal/agent/`)

`CoreLoop` 结构体是 Agent 的核心，封装了 tRPC-Agent-Go 的关键组件。

#### CoreLoop 结构

```go
type CoreLoop struct {
    agent          agent.Agent       // LLMAgent 实例
    runner         runner.Runner     // 消息执行器
    sessionService session.Service   // 会话管理
    memoryService  memory.Service    // 记忆管理
    factory        *provider.Factory // 模型工厂
    cfg            *config.WukongConfig
    contextMgr     *ContextManager    // 上下文管理
    security       *security.Guard   // 安全守卫
    recallStore    *recall.Store     // 跨会话回忆
    cortexStore    *cortex.CortexStore // CortexDB 向量存储
    memoryFlow     *cortex.MemoryFlowService // 对话流
    graphFlow      *cortex.GraphFlowService  // 知识图谱
    closeFn        func() error
}
```

#### 核心功能

- **Planner**：支持 `builtin` 和 `react` 两种规划器
- **Context Compaction**：上下文压缩，控制 Token 使用
- **Session Recall**：跨会话回忆，检索历史对话
- **Tool Search**：工具搜索，动态发现可用工具
- **Security Guard**：安全守卫，权限控制
- **Revision Model**：修订模型，上下文摘要/压缩
- **CortexStore**：CortexDB 向量存储集成
- **MemoryFlow**：对话转录记录和唤醒上下文
- **GraphFlow**：实体/关系提取和知识图谱
- **Recipe 工作流**：模板化多步骤工作流

#### 工作流模式 (`WorkflowBuilder`)

支持多种工作流模式：

| 模式 | 说明 | 底层实现 |
|------|------|----------|
| `single` | 单 Agent 模式 | LLMAgent |
| `chain` | 链式模式 | ChainAgent |
| `parallel` | 并行模式 | ParallelAgent |
| `cycle` | 循环模式 | CycleAgent |
| `graph` | 图模式 | GraphAgent |
| `team_coordinator` | 协调者团队 | Team + AgentTool |
| `team_swarm` | Swarm 团队 | Team (无协调者) |
| `claude_code` | Claude Code 模式 | ClaudeCodeAgent |
| `codex` | Codex 模式 | CodexAgent |
| `dify` | Dify 集成模式 | DifyAgent |

#### Recipe 系统

Recipe 是 YAML 定义的结构化子 Agent 模板：

- **基础字段**：name, description, instruction, model, tools, temperature, max_tokens
- **参数化 (P0)**：prompt + parameters 模板化
- **结构化输出 (P0)**：json_schema 约束
- **重试 (P1-B)**：指数退避重试
- **子 Recipe 组合 (P1-A)**：recipe 引用其他 recipe
- **继承 (P2-B)**：extends 继承基础 recipe
- **内联 Recipe (P2-A)**：直接在 config.yaml 中定义
- **模型覆盖 (P3-A)**：每个 recipe 使用不同模型
- **超时控制 (P3-B)**：限制执行时间
- **热加载 (P3-D)**：fsnotify 文件监控

#### Team 系统

- **Coordinator 模式**：一个协调者 Agent 通过 AgentTool 委托给成员
- **Swarm 模式**：Agent 之间直接传递控制权，无中央协调者

### 3.5 扩展系统 (`internal/extension/`)

MCP (Model Context Protocol) 扩展管理器，管理所有扩展的生命周期。

#### Manager 结构

```go
type Manager struct {
    toolSets map[string]tool.ToolSet  // 扩展工具集
    status   map[string]ExtensionInfo  // 扩展状态
    cfg      *config.WukongConfig
    ardTS    *ard.ToolSet  // ARD 集成
}
```

#### 扩展类型

**内置扩展（12 个）**：

| 扩展名 | 功能 | 条件启用 |
|--------|------|----------|
| `developer` | 开发者工具（文件读写、命令执行） | 始终启用 |
| `computer_controller` | 浏览器自动化 | `browser.enabled` |
| `memory` | 长期记忆管理 | 始终启用 |
| `auto_visualiser` | 图表/图表生成 | `visualiser.enabled` |
| `tutorial` | 交互式教程 | `tutorial.enabled` |
| `top_of_mind` | 持久指令注入 | `top_of_mind.enabled` |
| `code_mode` | JS 代码执行沙箱 | `code_mode.enabled` |
| `apps` | 网站克隆/打包 | `apps.enabled` |
| `web` | 网页搜索抓取 | 始终启用 |
| `agent_tools` | 子 Agent 工具 | 始终启用 |
| `ard` | 代理资源发现 | `ard.enabled` |
| `cortex` | CortexDB 知识工具 | `cortex.enabled` |

**外部扩展**：
- 标准 MCP 服务器（通过 stdio/SSE 连接）
- MCP Broker 模式（批量注册，4 个 Broker 工具）

#### 扩展生命周期

```
Initialize(ctx)
  ├── 遍历所有启用的扩展
  ├── 收集 Broker 扩展 → 创建 MCP Broker
  ├── 注册普通扩展 → registerExtension()
  │   ├── builtin → 创建内置工具集
  │   └── external → 创建 MCP 客户端连接
  └── 返回错误或成功

Manager 工具:
  ├── extension_list     — 列出所有扩展
  ├── extension_enable   — 启用扩展
  ├── extension_disable  — 禁用扩展
  └── extension_info     — 扩展详情
```

#### ACP MCP Bridge (`acp_mcp.go`)

将 Wukong 扩展暴露为 MCP Server，供 ACP Agent 发现和调用工具。

- 协议：HTTP JSON-RPC 2.0
- 端点：`tools/list`, `tools/call`
- 审计日志：`ToolAuditLogger`
- 健康检查：`MCPHealthChecker`

### 3.6 服务端点 (`internal/server/`)

#### AG-UI Server (`agui.go`)

基于 SSE 的 Web 聊天 UI 服务。

- **协议**：Server-Sent Events (SSE)
- **端点**：`GET /agui`（聊天）, `GET /health`
- **安全**：TLS/mTLS、API Key、JWT、Token Bucket 速率限制
- **配置**：`agui.enabled`, `agui.address`, `agui.path`

#### ACP Server (`acp.go`)

Agent Client Protocol 服务，暴露 5 个 HTTP 端点：

| 端点 | 方法 | 说明 |
|------|------|------|
| `/acp/message/send` | POST | 发送用户消息，获取 Agent 响应 |
| `/acp/tools/list` | GET | 列出可用工具（Agent Card） |
| `/acp/tools/call` | POST | 直接调用工具 |
| `/acp/.well-known/agent.json` | GET | Agent 能力发现 |
| `/acp/health` | GET | 健康检查 |

- **流式支持**：SSE 事件流（text_delta, tool_call, done）
- **配置**：`acp_server.enabled`, `acp_server.address`, `acp_server.path`

#### 安全配置

服务器的安全层统一配置：

```go
type ServerSecurityConfig struct {
    TLS       ServerTLSConfig       // TLS/mTLS
    Auth      ServerAuthConfig      // API Key / JWT
    RateLimit ServerRateLimitConfig // Token Bucket 限流
}
```

- **TLS**：证书 + 私钥 + 可选 CA 证书（mTLS）
- **Auth**：API Key（Header 认证）或 JWT（HS256 签名）
- **Rate Limit**：基于 Token Bucket 的速率限制

### 3.7 消息网关 (`internal/gateway/`)

Transport-agnostic 的消息网关，支持多种消息平台集成。

#### 设计架构

```
GatewayServer
  ├── MessageDeduplicator  — 消息去重
  ├── RateLimiter          — 速率限制
  ├── GatewaySessionStore  — 平台 <-> Wukong 会话映射
  ├── AgentRunner          — Agent 执行器
  └── channels             — 注册的通道适配器
```

#### 消息处理管道

```
1. 去重 (MessageID)  →  2. 用户/会话构建  →  3. 速率限制
  →  4. 会话映射持久化  →  5. Agent 执行（后台 goroutine）
  →  6. 回复发送（错误转为用户友好消息）
```

#### 飞书 Channel (`internal/gateway/feishu/`)

- **连接**：WebSocket 长连接（Lark SDK 自动重连）
- **消息格式**：流式卡片回复
- **验证**：`Validate()` 启动时检查配置完整性
- **追踪**：OTel Span 追踪

### 3.8 提供者系统 (`internal/provider/`)

Factory 模式创建 LLM 模型实例。

#### 支持类型

| 类型 | 默认 Base URL | 说明 |
|------|---------------|------|
| `openai` | `https://api.openai.com/v1` | OpenAI 兼容 API |
| `anthropic` | `https://api.anthropic.com/v1` | Anthropic Claude |
| `google` | `https://generativelanguage.googleapis.com/v1beta/openai` | Google Gemini |
| `deepseek` | `https://api.deepseek.com/v1` | DeepSeek |
| `ollama` | `http://localhost:11434/v1` | Ollama 本地 |
| `lmstudio` | `http://localhost:1234/v1` | LM Studio 本地 |
| `vllm` | `http://localhost:8000/v1` | vLLM 本地 |
| `acp` | 动态 | ACP 远程 Agent |

注意：OpenAI 兼容的云提供商（如 Together AI、Groq 等）通过 `type: openai` + 自定义 `base_url` 路由。

#### 创建流程

```go
Factory.CreateModel(name)
  ├── 查找提供者配置
  ├── fillDefaultBaseURL() 填充默认 Base URL
  ├── 根据 type 分发
  │   ├── openai/anthropic/google/deepseek/ollama/lmstudio/vllm → createOpenAI()
  │   └── acp → createACP()
  └── 返回 model.Model 实例
```

#### Revision Model

用于上下文摘要/压缩的轻量级模型。通过 `factory.CreateRevisionModel()` 创建，使用配置中的 `revision.revision_model` 或 `lightweight_model`。

### 3.9 存储层

#### SQLite（默认）

所有子系统默认共享同一个 `wukong.db` 数据库文件：

| 子系统 | 表/功能 | 说明 |
|--------|---------|------|
| Session | 会话事件 | 对话历史记录 |
| Memory | 长期记忆 | 关键信息持久化 |
| Todo | 待办事项 | 任务跟踪 |
| Recall | FTS5 全文搜索 | 跨会话检索 |

- **共享连接**：`DatabasePool` 管理多连接，避免 "transaction has already been committed" 错误
- **多池支持**：可通过 `db_path` 配置独立数据库

#### Redis（可选）

- 会话后端替代 SQLite
- 配置：`session.backend: "redis"`, `session.redis_url`

#### CortexDB

基于 `github.com/liliang-cn/cortexdb/v2` 的智能存储：

- **HNSW 向量索引**：语义搜索
- **FTS5 全文搜索**：关键词搜索
- **混合搜索**：向量 + 全文融合

### 3.10 搜索系统 (`internal/search/` + `internal/extension/builtin/aggregate_search.go`)

#### Web 搜索聚合工具

`aggregate_search.go` 是系统的核心网络搜索工具，注册为 `web_search` function tool，LLM Agent 可直接调用。

**搜索流程：**

```
web_search(query, fetch_count)
  │
  ├─ 1. 并发调用所有启用的 API 后端 (sync.WaitGroup)
  │     ├─ DuckDuckGo API  → GET api.duckduckgo.com
  │     ├─ SearXNG API     → GET {searxng_url}/search
  │     ├─ Tavily API      → POST api.tavily.com/search
  │     ├─ Google CSE API  → GET googleapis.com/customsearch/v1
  │     ├─ Bing API        → GET api.bing.microsoft.com/v7.0/search
  │     └─ CortexStore     → 本地 FTS5 + HNSW 向量搜索
  │
  ├─ 2. 合并 + URL 去重，截断到 20 条
  │
  ├─ 3. 0 条结果？→ searchViaBrowser（浏览器自动化搜索回退）
  │     ├─ Bing      → www.bing.com/search
  │     ├─ Baidu     → www.baidu.com/s
  │     ├─ WeChat    → weixin.sogou.com/weixin
  │     ├─ Zhihu     → www.zhihu.com/search
  │     ├─ DuckDuckGo → html.duckduckgo.com/html/
  │     └─ Google    → www.google.com/search
  │     跨引擎去重，累计 >= 5 条提前返回
  │
  └─ 4. 抓取 Top-N 页面完整内容 (fetch_count, 默认 3)
        ├─ Browser (chromedp/rod + stealth + JS渲染)
        ├─ Local Reader (HTTP GET + Readability算法 + Markdown)
        └─ HTTP GET + sanitize (简单兜底)
```

**浏览器搜索回退（`searchViaBrowser`）：**

当所有 API 后端失败或返回 0 条结果时自动触发。使用浏览器自动化（stealth 模式）直接访问搜索引擎页面，解析 HTML DOM 提取结果。6 个引擎按优先级依次尝试，每个引擎有独立的 HTML 解析器：

| 解析器 | 目标平台 | DOM 选择器 |
|--------|---------|-----------|
| `parseBingSearchResults` | Bing | `<li class="b_algo">` |
| `parseBaiduSearchResults` | 百度 | `<div class="result">` / `<div class="c-container">` |
| `parseSogouWeChatResults` | 微信公众号 | `<div class="txt-box">` |
| `parseZhihuSearchResults` | 知乎 | `<a href="/question/...">` |
| `parseDuckDuckGoHTMLResults` | DuckDuckGo HTML | `<a class="result__a">` |
| `parseGoogleSearchResults` | Google | `<a>` 含 `<h3>` |

**本地 Readability 内容提取（`internal/apps/sanitize/readability.go`）：**

替代外部服务，完全本地实现的 Readability 算法，灵感来自 Mozilla Readability.js：

1. **移除非内容元素**：script/style/nav/footer/aside + 类名黑名单（advert/sidebar/comment 等）
2. **优先查找 `<article>` 或 `<main>` 标签**
3. **DOM 评分**：遍历 block 元素，评分公式 = textLength/100 + paragraphCount×3 - linkDensity_penalty + tagBonus + classBonus
4. **选最高分节点**，提取 HTML → Markdown 转换
5. **回退**：提取内容 < 200 字符时回退到 `ExtractMainContentMarkdown`

**依赖注入：**

- `WebToolSet` 在构造时创建 `browser.Controller`（与 `ComputerControllerToolSet` 相同方式）
- `WebToolSet.SetCortexStore()` 支持延迟注入 CortexStore
- `Manager.SetCortexStore()` 通过动态接口注入，避免循环依赖

#### SearchGenome

可调参数化搜索策略：

```go
type SearchGenome struct {
    RecallMode          string  // lexical / vector / hybrid
    DenseWeight         float64 // 语义权重 [0,1]
    TextWeight          float64 // 关键词权重 [0,1]
    KeywordMatchPercent float64 // 关键词匹配比例
    MaxRetrievedNum     int     // TopK 结果数
    FTS5PoolSize        int     // FTS5 候选池大小
    FusionMethod        string  // weighted / rrf
    RRFK                float64 // RRF 常数 (默认 60)
    RerankerEnabled     bool    // 是否启用重排序
    RerankerTopN        int     // 重排序 TopN
    MMREnabled          bool    // MMR 多样性
    MMRLambda           float64 // MMR 平衡参数
}
```

#### 搜索流程

```
Query
  ├── VerticalRouter（可选）— 垂直搜索路由
  │   ├── arXiv
  │   ├── GitHub
  │   ├── Wikipedia
  │   └── Reddit
  ├── 检索模式
  │   ├── Lexical — FTS5 / BM25
  │   ├── Vector — HNSW 向量搜索
  │   └── Hybrid — FTS5 + 向量重排序
  ├── 融合
  │   ├── Weighted — DenseWeight × sim + TextWeight × rank
  │   └── RRF — Reciprocal Rank Fusion
  ├── Cross-Encoder Reranker（可选）
  ├── MMR 多样性（可选）
  └── Results
```

#### 搜索调优 (`search/tune/`)

AutoTune 系统自动优化搜索参数：

- **LLM Judge**：LLM 评估搜索结果质量
- **Multi-Fidelity**：多保真度优化
- **Optimizer**：贝叶斯优化
- **Cache**：结果缓存

#### 语义分块 (`search/chunking/`)

智能文档分块，提升检索质量。

### 3.11 浏览器自动化 (`internal/browser/`)

#### 双后端架构

```go
type Controller struct {
    client      *httpclient.Client  // HTTP 模式
    backend     types.BrowserBackend // 浏览器后端
    // ...
}
```

| 后端 | 库 | 默认 | 特点 |
|------|-----|------|------|
| HTTP 模式 | net/http | 是 | 快速、轻量 |
| Chromedp | `chromedp/chromedp` | 否 | 完整 JS 渲染、截图 |
| Rod | `go-rod/rod` | 否 | 备用浏览器后端 |

#### 反检测功能

- **Stealth**：`stealth/` 包，反检测脚本
- **Antibot**：`antibot/` 包，WAF 检测和升级
- **TLS 指纹旋转**：`tls_profile.go`，使用 `utls` 库
- **智能代理池**：`proxy_pool.go`，IP 轮换

#### 搜索提供者集成

| 提供者 | 默认 URL | 启用条件 |
|--------|----------|----------|
| DuckDuckGo | `https://api.duckduckgo.com/` | 默认启用 |
| SearXNG | `http://localhost:8080/` | 配置启用 |
| Tavily | - | 配置启用 |
| Google | - | 配置启用（需 API Key + CSE ID） |
| Bing | - | 配置启用（需 API Key） |

### 3.12 安全系统 (`internal/security/`)

#### Guard 结构

```go
type Guard struct {
    cfg              *config.SecurityConfig
    approvedCommands map[string]bool
    blockedCount     atomic.Int64
    ignoreMatcher    *IgnoreMatcher
}
```

#### 权限模式（4 种）

| 模式 | 说明 |
|------|------|
| `auto` | 自动批准所有安全工具 |
| `smart` | 智能判断（默认），根据上下文决策 |
| `manual` | 所有操作需用户确认 |
| `chat_only` | 仅聊天，禁止所有工具执行 |

#### 安全功能

- **恶意命令扫描**：`malware_scan_enabled`，扫描命令中的恶意模式
- **危险命令阻止**：`block_dangerous_commands`，阻止 `rm -rf /` 等
- **工具允许列表/拒绝列表**：`ToolPermission` 细粒度控制
- **.wukongignore**：`IgnoreMatcher`，gitignore 兼容的文件访问控制
- **超时控制**：`default_timeout` (30s), `max_timeout` (300s)

### 3.13 A2A 代理间通信 (`internal/summon/`)

#### A2A Server

基于 tRPC-Agent-Go 的 `server/a2a` 包：

- 自动消息/事件协议转换
- 流式支持（TaskArtifactUpdate 或 Message 模式）
- Session 和 Memory 服务传播
- AgentCard 自动生成（含工具发现）

```go
type A2AServer struct {
    server   *http.Server
    a2aAgent agent.Agent
    address  string
}
```

#### A2A Agent

远程代理客户端，基于 `a2aagent` 包：

```go
type A2AAgent struct {
    agent agent.Agent  // 远程代理的本地代理
}
```

#### ANP (Agent Network Protocol)

ANP 是 Wukong 实现的代理间高级协议：

- **W3C DID**：去中心化身份（`ard.DIDManager`）
- **Meta-Protocol**：能力协商（JSON-RPC 2.0）
- **E2EE**：端到端加密（`E2EEMessenger`）
- **HTTP Signing**：RFC 9421 HTTP 消息签名

#### ANP Adapter

桥接 tRPC 事件系统和 ANP JSON-RPC 2.0 消息：

- **P1 Core Binding**：JSON-RPC 2.0 请求/响应/错误
- **P3 Direct Messaging**：Agent 间消息语义
- **P5 E2EE Overlay**：加密消息封装
- **P7 Attachments**：文件/对象传输

#### 认证配置

```go
type AuthConfig struct {
    Type              string   // jwt / api_key / oauth2
    APIKey            string
    JWTSecret         string
    JWTAudience       string
    OAuthTokenURL     string
    OAuthClientID     string
    OAuthClientSecret string
    OAuthScopes       []string
}
```

### 3.14 知识系统 (`internal/cortex/`)

#### CortexDB

基于 `github.com/liliang-cn/cortexdb/v2` 的知识存储：

```go
type CortexStore struct {
    db          *cortexdb.DB      // HNSW + FTS5
    embedder    *Embedder         // 嵌入模型
    reranker    *Reranker         // 重排序器
    router      *vertical.Router  // 垂直搜索路由
    chunker     *chunking.Chunker // 语义分块
    metrics     *metrics.SearchMetrics
    lexical     *lexicalStore     // FTS5 全文搜索
    vectorCache *VectorCache      // 向量缓存
    genome      search.SearchGenome
}
```

#### MemoryFlow

对话转录记录和唤醒上下文：

- **IngestTurn**：记录单个对话轮次
- **WakeUp**：构建上下文层
- **PromoteFacts**：事实提升到知识库

#### GraphFlow

知识图谱构建：

- **ExtractFromTranscript**：从对话中提取实体/关系
- **BuildGraph**：持久化节点/边
- **QueryKnowledge**：SPARQL 查询
- **BuildContext**：KG 增强上下文

#### ImportFlow

结构化数据导入：

- DDL 映射
- CSV 导入
- JSON 导入

#### OKF (Open Knowledge Format)

知识互操作格式：

- 知识包导入/导出
- 索引注入
- 知识丰富

### 3.15 技能系统 (`internal/skill/`, `internal/evolution/`)

#### Skill Manager

基于 tRPC-Agent-Go 的 `skill` 包：

```go
type Manager struct {
    repository *agentskill.FSRepository  // 文件系统仓库
    summaries  []agentskill.Summary      // 技能摘要
    evoHook    SkillEvolutionHook        // 进化钩子
}
```

- **SKILL.md 格式**：YAML front matter + Markdown 工作流指令
- **热加载**：运行时重载
- **进化钩子**：记录执行轨迹

#### Evolution Engine

技能自我进化系统：

```go
type EvolutionEngine struct {
    analyzer    *EvolutionAnalyzer  // LLM 分析
    patcher     *EvolutionPatcher   // 补丁生成
    store       *VersionStore       // 版本存储
    refresher   SkillRefresher      // 技能热加载
}
```

- **分析**：LLM 分析执行轨迹，识别问题
- **补丁生成**：生成 SKILL.md 补丁
- **自动应用**：可选自动补丁应用
- **版本控制**：最多保留 10 个版本

### 3.16 编排系统 (`internal/agent/`)

#### Workflow

多模式 Agent 编排：

| 模式 | 说明 |
|------|------|
| `single` | 单 Agent |
| `chain` | 链式执行 |
| `parallel` | 并行执行 |
| `cycle` | 循环执行 |
| `graph` | 有向图执行 |
| `team_coordinator` | 协调者团队 |
| `team_swarm` | Swarm 团队 |

#### Recipe

模板化工作流：

- 发现：`list_recipes` 工具自动注册
- 热加载：fsnotify 文件监控
- 参数化：Go 模板参数
- 组合：Recipe 间引用

#### Team

多代理协作：

- **Coordinator**：一个协调者 + 多个成员
- **Swarm**：无中心协调者，Agent 直接传递控制

#### HITL (Human-in-the-Loop)

人在回路机制：

- `hitl.go` 实现
- 需要用户确认的操作
- 审批流程

### 3.17 ARD 资源发现 (`internal/ard/`)

Agentic Resource Discovery 实现：

```go
type Registry struct {
    catalog      *AICatalog
    byType       map[string][]*CatalogEntry
    byCapability map[string][]*CatalogEntry
    byTag        map[string][]*CatalogEntry
    server       *RegistryServer
}
```

#### 功能

- **目录服务**：AI Agent 目录注册和发现
- **联邦搜索**：跨多个目录的联邦搜索
- **语义搜索**：基于能力的语义搜索
- **信任评分**：`TrustScorer` 评分系统
- **断路器**：`CircuitBreaker` 容错
- **速率限制**：`RateLimiter` 请求限制
- **ANP 发现集成**：与 ANP 协议集成

#### DID 身份

基于 W3C DID 的去中心化身份：

```go
type DIDManager struct {
    didDocument *DIDDocument
    privateKey  *ecdsa.PrivateKey
    did         string
}
```

### 3.18 工具系统

#### 内置工具（通过 MCP 扩展）

| 工具集 | 提供者 | 功能 |
|--------|--------|------|
| `developer` | 内置 | 文件读写、命令执行、代码搜索 |
| `computer_controller` | 内置 | 浏览器自动化（截图、点击、导航） |
| `memory` | 内置 | 长期记忆存储和检索 |
| `web` | 内置 | 网页搜索、内容抓取 |
| `auto_visualiser` | 内置 | 图表生成（Mermaid、PlantUML） |
| `tutorial` | 内置 | 交互式教程 |
| `top_of_mind` | 内置 | 持久指令管理 |
| `code_mode` | 内置 | JavaScript 沙箱执行 |
| `apps` | 内置 | 网站克隆、ZIM 打包 |
| `agent_tools` | 内置 | 子 Agent 工具（代码审查、摘要） |
| `ard` | 内置 | ARD 资源发现 |
| `cortex` | 内置 | 知识图谱查询、知识导入 |

#### 外部工具

- MCP 标准协议工具
- 通过 stdio 或 SSE 连接
- MCP Broker 模式（批量管理）

#### 工具搜索

- `tool_search_enabled`：启用工具搜索
- `tool_search_max_tools`：最大工具数 (20)
- 动态发现和注册

#### 权限控制

- 工具允许列表/拒绝列表
- 细粒度 `ToolPermission` 配置
- Security Guard 统一管理

### 3.19 可观测性

#### OpenTelemetry (`internal/telemetry/`)

```go
type Manager struct {
    provider *sdktrace.TracerProvider
}
```

- **Tracing**：分布式追踪（OTLP gRPC/HTTP 导出）
- **Metrics**：指标收集
- **服务标识**：service.name, service.version, deployment.environment
- **采样率**：可配置采样率
- **传播器**：W3C Trace Context

#### Langfuse (`internal/observability/`)

- LLM 调用追踪 UI
- Token 使用统计
- 错误分析
- 工具调用记录

#### Health 端点 (`internal/health/`)

| 端点 | 说明 |
|------|------|
| `/healthz` | 完整健康检查（JSON） |
| `/livez` | 存活检查（始终 200） |
| `/readyz` | 就绪检查（同 healthz） |

健康检查组件：

- **database**：SQLite 连接 Ping
- **a2a_server**：A2A 服务状态
- **session**：会话后端状态
- **memory**：记忆后端状态
- 其他子系统状态

---

## 4. 数据流

### 4.1 CLI 交互模式（TUI）

```
User (TUI)
  │ 输入消息
  ▼
CLI (session.go)
  │
  ▼
bootstrapSession()
  │ 加载配置 → 初始化数据库 → 创建各子系统
  ▼
CoreLoop.Run()
  │
  ├─→ Runner.Run()
  │     ├─→ LLMAgent (LLM 调用)
  │     ├─→ Tool 执行（扩展系统）
  │     └─→ 事件流 (text_delta, tool_call, tool_result, done)
  │
  ├─→ 后处理
  │     ├─→ MemoryFlow.IngestTurn()  — 记录对话
  │     ├─→ GraphFlow.ExtractFromTranscript() — 知识图谱提取
  │     ├─→ RecallStore.StoreMessage() — 存储检索
  │     └─→ ContextManager  — 上下文管理
  │
  ▼
TUI (bubbletea)
  │ 渲染事件流
  ▼
User 看到回复
```

### 4.2 服务端模式

```
Client (ACP/AG-UI/A2A)
  │ HTTP 请求
  ▼
ACPServer / AGUIServer / A2AServer
  │
  ├─→ ApplySecurity()
  │     ├─→ TLS 握手
  │     ├─→ API Key / JWT 验证
  │     └─→ Token Bucket 限流
  │
  ├─→ Runner.Run()
  │     ├─→ CoreLoop
  │     └─→ LLM + Tools
  │
  ▼
SSE 事件流 或 JSON 响应
  │
  ▼
Client 接收回复
```

### 4.3 网关模式（飞书）

```
飞书用户
  │ 发送消息
  ▼
飞书服务器
  │ WebSocket 推送
  ▼
FeishuChannel
  │ 解析消息，构建 GatewayMessage
  ▼
GatewayServer.dispatch()
  │
  ├─→ 1. MessageDeduplicator.Deduplicate() — 去重
  ├─→ 2. 用户/会话构建
  ├─→ 3. RateLimiter.Allow() — 速率限制
  ├─→ 4. GatewaySessionStore.EnsureSession() — 会话映射
  ├─→ 5. CoreLoop.Run() — Agent 执行（后台 goroutine）
  └─→ 6. FeishuSender.Send() — 流式卡片回复
       │
       ▼
飞书服务器
  │ WebSocket 推送
  ▼
飞书用户看到回复
```

### 4.4 搜索流程

#### 4.4.1 Web 搜索聚合流程（`aggregate_search.go`）

```
LLM 调用 web_search(query, fetch_count)
  │
  ├─→ 1. 并发 API 搜索 (goroutine + WaitGroup)
  │     ├─→ DuckDuckGo API
  │     ├─→ SearXNG API
  │     ├─→ Tavily API
  │     ├─→ Google CSE API
  │     ├─→ Bing API
  │     └─→ CortexStore (本地 FTS5 + HNSW)
  │
  ├─→ 2. 合并 + URL 去重 → 截断 20 条
  │
  ├─→ 3. 0 条？→ searchViaBrowser (浏览器搜索回退)
  │     ├─→ Bing HTML   → parseBingSearchResults
  │     ├─→ Baidu HTML  → parseBaiduSearchResults
  │     ├─→ WeChat HTML → parseSogouWeChatResults
  │     ├─→ Zhihu HTML  → parseZhihuSearchResults
  │     ├─→ DDG HTML    → parseDuckDuckGoHTMLResults
  │     └─→ Google HTML → parseGoogleSearchResults
  │     (跨引擎去重, >= 5 条提前返回)
  │
  └─→ 4. 抓取 Top-N 页面内容 (fetch_count, 默认 3)
        ├─→ Browser: ExtractText() (JS渲染 + stealth)
        ├─→ Local Reader: HTTP GET + Readability + Markdown
        └─→ HTTP GET + sanitize.ExtractMainContentMarkdown
```

#### 4.4.2 本地知识搜索流程（CortexStore）

```
Query
  │
  ├─→ VerticalRouter（可选）
  │     ├─→ arXiv 搜索
  │     ├─→ GitHub 搜索
  │     ├─→ Wikipedia 搜索
  │     └─→ Reddit 搜索
  │
  ├─→ 语义分块 (Chunker)
  │
  ├─→ 检索模式
  │     ├─→ Lexical: FTS5 / BM25 全文搜索
  │     ├─→ Vector: HNSW 向量搜索（嵌入）
  │     └─→ Hybrid: FTS5 + 向量重排序
  │
  ├─→ 融合
  │     ├─→ Weighted: DenseWeight × sim + TextWeight × (1/rank)
  │     └─→ RRF: Σ 1/(k + rank)
  │
  ├─→ Cross-Encoder Reranker（可选）
  │
  ├─→ MMR 多样性（可选）
  │
  └─→ Results
```

### 4.5 ANP 代理间通信

```
Agent A (Wukong)
  │
  ├─→ ANPAdapter.Adapt()
  │     ├─→ tRPC Event → ANP JSON-RPC 2.0
  │     ├─→ E2EE 加密（可选）
  │     └─→ HTTP 签名
  │
  ▼
ANP HTTP 请求
  │
  ▼
Agent B (ANP 兼容)
  │
  ├─→ 验证签名
  ├─→ 解密
  └─→ 处理请求
       │
       ▼
    返回响应
```

---

## 5. 配置优先级与加载流程

```
CLI 标志 (--provider, --model, ...)
    │ 最高优先级
    ▼
环境变量 (WUKONG_DEFAULT_PROVIDER, ...)
    │
    ▼
指定配置文件 (--config)
    │
    ▼
./config.yaml
    │
    ▼
~/.config/wukong/config.yaml
    │
    ▼
/etc/wukong/config.yaml (Unix only)
    │
    ▼
内置默认值 (setDefaults())
    │ 最低优先级
    ▼
expandSecrets()  ← 展开 $ {ENV_VAR} 和 $ {VAR:-default}
    │
    ▼
Validate()  ← 致命验证（版本检查、必填字段）
    │
    ▼
Warnings()  ← 非致命警告（配置可疑项）
```

---

## 6. 启动流程 (bootstrapSession)

```
bootstrapSession(configPath, userID, sessionID, ...)
  │
  ├─ 1. LoadConfig
  │     ├─ config.NewLoader(configPath)
  │     ├─ loader.LoadAndValidate()
  │     └─ cfg.Warnings() 输出非致命警告
  │
  ├─ 2. SetupTelemetry
  │     ├─ telemetry.NewManager()
  │     └─ telMgr.Initialize()
  │
  ├─ 3. RegisterBuiltins
  │     └─ builtin.RegisterBuiltins(cfg)
  │
  ├─ 4. ApplyOverrides
  │     └─ applyOverrides(provider, model, temperature, ...)
  │
  ├─ 5. CreateFactory
  │     └─ provider.NewFactory(cfg)
  │
  ├─ 6. SetupDatabase
  │     └─ util.NewMultiPool(dbPath)
  │
  ├─ 7. InitSession
  │     └─ wksession.NewSessionService(&cfg.Session, dbPool)
  │
  ├─ 8. InitMemory
  │     ├─ memory.NewMemoryManager(&cfg.Memory, extractorModel, dbPool)
  │     └─ memoryMgr.SmartCleanup()
  │
  ├─ 9. InitSecurity
  │     └─ security.NewGuard(&cfg.Security)
  │
  ├─10. InitExtensions
  │     ├─ extension.NewManager(cfg)
  │     └─ extMgr.Initialize(ctx)
  │
  ├─11. InitARD (if enabled)
  │     ├─ ard.NewToolSet()
  │     ├─ extMgr.SetARDToolSet()
  │     └─ ard.PublishAndServe()
  │
  ├─12. InitACPMCPBridge
  │     ├─ extension.NewACPMCPBridge()
  │     └─ acpMCPBridge.Start()
  │
  ├─13. InitRecall & Cortex
  │     ├─ cortex.NewStore() 或 recall.NewStore()
  │     ├─ memoryFlowSvc = cortex.NewMemoryFlow()
  │     └─ graphFlowSvc = cortex.NewGraphFlow()
  │
  ├─14. InitSubsystems
  │     ├─ topofmind.NewManager()
  │     ├─ codemode.NewExecutor()
  │     ├─ apps.NewManager()
  │     ├─ summon.NewSummonManager()
  │     ├─ skill.NewManager()
  │     ├─ evolution.NewEngine()
  │     ├─ knowledge.NewManager()
  │     └─ todo.NewTodoManager()
  │
  ├─15. CollectTools
  │     ├─ extMgr.ToolSets()  — MCP 扩展工具
  │     ├─ functionTools — 功能工具
  │     ├─ summonTools — 委托工具
  │     └─ codeExecutor.SetToolsForDiscovery()
  │
  ├─16. CreateCoreLoop
  │     └─ agent.NewCoreLoop(CoreLoopConfig{...})
  │
  ├─17. StartServers
  │     ├─ A2AServer (if enabled)
  │     ├─ AGUIServer (if enabled)
  │     ├─ ACPServer (if enabled)
  │     ├─ ANPServer (if enabled)
  │     └─ GatewayServer (if enabled)
  │
  └─18. Return (cfg, loop, state)
```

---

## 7. 关键设计决策

### 7.1 本地优先 (Local-First)

- **决策**：所有数据默认存储在本地 SQLite，无需外部服务
- **理由**：用户隐私、离线可用、低延迟
- **影响**：Session、Memory、Todo、Recall 共享同一个 `wukong.db`
- **备选**：Redis 可选用于会话存储

### 7.2 MCP 扩展系统

- **决策**：使用 MCP (Model Context Protocol) 作为扩展标准
- **理由**：开放标准、语言无关、社区生态系统
- **影响**：12 个内置扩展 + 任意数量外部扩展
- **支持**：MCP Broker 模式批量管理

### 7.3 多协议支持

- **决策**：同时支持 A2A、ACP、AG-UI、MCP 四种协议
- **理由**：最大化互操作性，适应不同客户端
- **影响**：启动时根据配置选择性启动各协议服务器
- **端口分配**：A2A (:9090), ACP (:9091), AG-UI (:8080), ACP MCP (:3400), ANP (:9092)

### 7.4 消息平台集成

- **决策**：通过 Gateway 抽象层集成消息平台
- **理由**：Transport-agnostic 设计，平台无关
- **影响**：每个 Channel 拥有自己的传输层，共享处理管道
- **当前实现**：飞书 Channel（WebSocket 长连接）

### 7.5 搜索可调优

- **决策**：SearchGenome 参数化搜索策略
- **理由**：不同场景需要不同的搜索策略
- **影响**：Lexical/Vector/Hybrid 三种模式，Weighted/RRF 融合
- **自动调优**：AutoTune 系统使用 LLM 评估优化参数

### 7.6 安全多层次

- **决策**：多层次安全模型
- **理由**：Agent 自主执行需要完善的安全保障
- **层次**：
  1. 权限模式（auto/smart/manual/chat_only）
  2. 恶意命令扫描
  3. 危险命令阻止
  4. 工具允许/拒绝列表
  5. `.wukongignore` 文件访问控制
  6. 超时控制
  7. 服务器层 TLS/mTLS + API Key + JWT + 速率限制

### 7.7 浏览器双后端

- **决策**：Rod 默认 + Chromedp 回退
- **理由**：Rod 提供更好的 API 和性能，Chromedp 作为可靠备选
- **影响**：浏览器控制器自动选择可用后端
- **反检测**：Stealth 模式、TLS 指纹旋转、代理池

### 7.8 提供者抽象

- **决策**：Factory 模式统一管理 LLM 提供者
- **理由**：支持多种 LLM 提供者，切换零成本
- **影响**：8 种提供者类型，OpenAI 兼容 API 路由
- **轻量级模型**：独立配置，后台任务使用更经济的模型

### 7.9 配置多级优先级

- **决策**：7 级配置优先级
- **理由**：灵活配置，适应不同环境
- **影响**：CLI 标志 > 环境变量 > 配置文件 > 默认值
- **安全**：`$ {ENV_VAR}` 注入，密钥不落盘

### 7.10 知识系统分层

- **决策**：CortexDB + SQLite FTS5 双层知识存储
- **理由**：向量搜索 + 全文搜索互补
- **影响**：CortexDB 提供 HNSW 向量索引，SQLite FTS5 提供关键词搜索
- **MemoryFlow/GraphFlow/ImportFlow**：三个独立但协作的 CortexDB 服务

---

## 附录 A：项目目录结构

```
e:\myVibeCoding\km269\wukong/
├── cmd/
│   ├── wukong/main.go          # 主入口
│   ├── zim-check/main.go        # ZIM 文件检查工具
│   └── zim-ls/main.go           # ZIM 文件列表工具
├── internal/
│   ├── agent/                   # Agent 核心循环
│   │   ├── loop.go              # CoreLoop 实现
│   │   ├── context.go           # 上下文管理
│   │   ├── workflow.go          # 工作流编排
│   │   ├── recipe.go            # Recipe 系统
│   │   ├── recipe_advance.go    # Recipe 高级功能
│   │   ├── recipe_compose.go    # Recipe 组合
│   │   ├── team.go              # Team 多代理协作
│   │   ├── hitl.go              # 人在回路
│   │   ├── dify.go              # Dify 集成
│   │   ├── prompt_template.go   # 提示模板
│   │   └── todo_enforcer.go     # 待办强制执行
│   ├── apps/                    # 网站克隆和应用
│   │   ├── clone/               # 网站克隆器
│   │   ├── pack/                # ZIM 打包器
│   │   ├── sanitize/            # 内容清理
│   │   │   ├── cleaner.go       # 基础 HTML 清理
│   │   │   ├── enhanced.go      # 增强清理 (CleanHTMLWithOptions)
│   │   │   ├── markdown.go      # HTML→Markdown 转换
│   │   │   └── readability.go   # 本地 Readability 算法
│   │   ├── mcpapps/             # MCP 应用桥接
│   │   ├── server/              # 应用服务器
│   │   ├── manager.go           # 应用管理器
│   │   └── history.go           # 历史记录
│   ├── ard/                     # 代理资源发现
│   │   ├── registry.go          # 目录注册表
│   │   ├── federation.go        # 联邦搜索
│   │   ├── semantic.go          # 语义搜索
│   │   ├── trust.go             # 信任评分
│   │   ├── circuit_breaker.go   # 断路器
│   │   ├── did.go               # W3C DID 身份
│   │   ├── server.go            # 注册表服务器
│   │   ├── client.go            # 发现客户端
│   │   ├── anp_discovery.go     # ANP 发现集成
│   │   └── tools.go             # ARD 工具
│   ├── artifact/                # 制品存储工厂
│   ├── browser/                 # 浏览器自动化
│   │   ├── controller.go        # 主控制器
│   │   ├── backend.go           # 后端抽象
│   │   ├── pool.go              # 浏览器池
│   │   ├── proxy_pool.go        # 代理池
│   │   ├── stealth/             # 反检测
│   │   ├── antibot/             # 反爬虫
│   │   ├── rodbackend/          # Rod 后端
│   │   └── types/               # 类型定义
│   ├── cli/                     # CLI 层
│   │   ├── root.go              # 根命令
│   │   ├── session.go           # 交互式会话 + bootstrap
│   │   ├── run.go               # 单次执行
│   │   ├── server.go            # 服务模式
│   │   ├── shutdown.go          # 优雅关闭
│   │   ├── config.go            # 配置管理命令
│   │   ├── configure.go         # 配置向导
│   │   ├── tui/                 # TUI 界面
│   │   ├── extension.go         # 扩展管理
│   │   ├── provider_mgmt.go     # 提供者管理
│   │   ├── memory_mgmt.go       # 记忆管理
│   │   ├── cortex_mgmt.go       # Cortex 管理
│   │   ├── knowledge_mgmt.go    # 知识管理
│   │   ├── skill_mgmt.go        # 技能管理
│   │   ├── evolution_mgmt.go    # 进化管理
│   │   ├── recipe_mgmt.go       # Recipe 管理
│   │   ├── search_tune.go       # 搜索调优
│   │   ├── apps_mgmt.go         # 应用管理
│   │   └── ...                  # 其他命令
│   ├── codemode/                # JS 代码执行
│   ├── config/                  # 配置管理
│   │   ├── config.go            # Loader + WukongConfig
│   │   ├── defaults.go          # 内置默认值
│   │   ├── validate.go          # 验证
│   │   └── types_*.go           # 子配置类型
│   ├── cortex/                  # 知识系统
│   │   ├── store.go             # CortexDB 存储
│   │   ├── memoryflow.go        # 对话流
│   │   ├── graphflow.go         # 知识图谱
│   │   ├── import_flow.go       # 数据导入
│   │   ├── embedder.go          # 嵌入器
│   │   ├── reranker.go          # 重排序器
│   │   ├── lexical.go           # 全文搜索
│   │   ├── extractor.go         # 实体提取器
│   │   ├── planner.go           # 查询规划器
│   │   ├── recall_manager.go    # 回忆管理器
│   │   ├── kg_tools.go          # 知识图谱工具
│   │   ├── import_tools.go      # 导入工具
│   │   ├── okf_injector.go      # OKF 注入
│   │   ├── okf_enrichment.go    # OKF 丰富
│   │   └── json_generator.go    # JSON 生成器
│   ├── errsignal/               # 错误信号
│   ├── eval/                    # 评估系统
│   ├── evolution/               # 技能进化
│   │   ├── engine.go            # 进化引擎
│   │   ├── analyzer.go          # 分析器
│   │   ├── patcher.go           # 补丁器
│   │   ├── store.go             # 版本存储
│   │   └── okf.go               # OKF 导出
│   ├── extension/               # 扩展系统
│   │   ├── manager.go           # 扩展管理器
│   │   ├── factory.go           # 扩展工厂
│   │   ├── types.go             # 类型定义
│   │   ├── mcp_client.go        # MCP 客户端
│   │   ├── mcp_server.go        # MCP 服务器
│   │   ├── acp_mcp.go           # ACP MCP 桥接
│   │   ├── deeplink.go          # DeepLink 注册
│   │   ├── manager_tools.go     # 管理工具
│   │   ├── mcp_audit.go         # 审计日志
│   │   └── builtin/             # 内置扩展实现
│   ├── gateway/                 # 消息网关
│   │   ├── gateway.go           # GatewayServer
│   │   ├── dedup.go             # 消息去重
│   │   ├── ratelimit.go         # 速率限制
│   │   ├── session.go           # 会话映射
│   │   ├── types.go             # 类型定义
│   │   ├── config.go            # 配置
│   │   └── feishu/              # 飞书通道
│   ├── health/                  # 健康检查
│   ├── knowledge/               # RAG 知识系统
│   │   ├── manager.go           # 知识管理器
│   │   └── okf.go               # OKF 集成
│   ├── memory/                  # 记忆系统
│   ├── observability/           # Langfuse 集成
│   ├── okf/                     # 开放知识格式
│   ├── project/                 # 项目管理
│   ├── provider/                # 提供者系统
│   │   ├── factory.go           # 模型工厂
│   │   └── acp.go               # ACP 提供者
│   ├── recall/                  # 回忆存储
│   ├── search/                  # 搜索系统
│   │   ├── genome.go            # SearchGenome
│   │   ├── metrics.go           # 搜索指标
│   │   ├── evalcase.go          # 评估用例
│   │   ├── chunking/            # 语义分块
│   │   ├── vertical/            # 垂直搜索
│   │   ├── metrics/             # 可观测性指标
│   │   └── tune/                # 搜索调优
│   ├── security/                # 安全系统
│   │   ├── guard.go             # 安全守卫
│   │   └── ignore.go            # .wukongignore
│   ├── server/                  # 服务端点
│   │   ├── acp.go               # ACP Server
│   │   ├── agui.go              # AG-UI Server
│   │   └── security.go          # 安全配置
│   ├── session/                 # 会话存储
│   ├── skill/                   # 技能系统
│   ├── summon/                  # A2A 通信
│   │   ├── a2a.go               # A2A Server
│   │   ├── delegate.go          # 委托工具
│   │   ├── auth.go              # 认证
│   │   ├── anp_adapter.go       # ANP 适配器
│   │   ├── e2ee.go              # 端到端加密
│   │   ├── meta_protocol.go     # 元协议
│   │   └── http_sign.go         # HTTP 签名
│   ├── telemetry/               # OpenTelemetry
│   ├── todo/                    # 待办事项
│   ├── topofmind/               # 持久指令
│   └── util/                    # 工具函数
│       ├── database.go          # 数据库池
│       └── logger.go            # 日志
└── pkg/                         # 公共包
    ├── httpclient/              # HTTP 客户端
    ├── logutil/                 # 日志工具
    ├── sandbox/                 # 沙箱
    └── zim/                     # ZIM 文件格式
```

## 附录 B：依赖关系

```
cmd/wukong/main.go
  └── internal/cli/  (cobra)
       ├── internal/config/  (viper)
       ├── internal/telemetry/  (OpenTelemetry)
       ├── internal/agent/
       │    ├── trpc.group/trpc-go/trpc-agent-go/agent/llmagent
       │    ├── trpc.group/trpc-go/trpc-agent-go/runner
       │    ├── trpc.group/trpc-go/trpc-agent-go/session
       │    ├── trpc.group/trpc-go/trpc-agent-go/memory
       │    ├── internal/config/
       │    ├── internal/provider/
       │    ├── internal/security/
       │    ├── internal/recall/
       │    ├── internal/cortex/  (cortexdb)
       │    └── internal/search/
       ├── internal/extension/
       │    ├── internal/config/
       │    ├── internal/ard/
       │    ├── trpc.group/trpc-go/trpc-mcp-go
       │    └── trpc.group/trpc-go/trpc-agent-go/tool/mcpbroker
       ├── internal/server/
       │    ├── internal/security/
       │    └── golang-jwt/jwt/v5
       ├── internal/gateway/
       │    ├── internal/agent/
       │    ├── internal/gateway/feishu/  (larksuite oapi-sdk)
       │    └── go.opentelemetry.io/otel
       ├── internal/summon/
       │    ├── trpc.group/trpc-go/trpc-agent-go/server/a2a
       │    ├── trpc.group/trpc-go/trpc-agent-go/agent/a2aagent
       │    ├── internal/ard/
       │    └── internal/config/
       ├── internal/browser/
       │    ├── go-rod/rod
       │    ├── chromedp/chromedp
       │    └── refraction-networking/utls
       ├── internal/skill/
       │    └── trpc.group/trpc-go/trpc-agent-go/skill
       ├── internal/evolution/
       │    └── internal/provider/
       ├── internal/knowledge/
       │    └── trpc.group/trpc-go/trpc-agent-go/knowledge
       ├── internal/health/
       └── internal/observability/
            └── langfuse (自定义)
```

---

*本文档基于 Wukong v0.3.0 代码库自动生成，最后更新于 2026-08-03。*