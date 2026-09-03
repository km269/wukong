# Wukong CLI & TUI 架构详解

> 基于源码深度分析的命令行与终端 UI 架构文档。涵盖命令树、持久化标志、启动序列、流式管道、TUI 渲染引擎与优雅关闭机制。

---

## 目录

1. [概览与技术栈](#1-概览与技术栈)
2. [入口与根命令](#2-入口与根命令)
3. [CLI 命令树全景](#3-cli-命令树全景)
4. [核心命令详解](#4-核心命令详解)
5. [Bootstrap 启动引擎](#5-bootstrap-启动引擎)
6. [TUI 架构（Bubble Tea）](#6-tui-架构bubble-tea)
7. [流式传输与事件管道](#7-流式传输与事件管道)
8. [输入解析与会话标识](#8-输入解析与会话标识)
9. [优雅关闭与看门狗](#9-优雅关闭与看门狗)
10. [版本与构建信息](#10-版本与构建信息)

---

## 1. 概览与技术栈

Wukong CLI/TUI 层是用户与 AI Agent 平台交互的主界面，采用 **Cobra + Charmbracelet Bubble Tea** 双框架架构。

### 技术栈

| 组件 | 技术 | 版本 | 作用 |
|------|------|------|------|
| CLI 框架 | `github.com/spf13/cobra` | v1.9.1 | 命令路由、参数解析、帮助生成 |
| TUI 框架 | `github.com/charmbracelet/bubbletea` | v1.3.10 | Elm 架构（Model-View-Update）终端 UI |
| TUI 组件 | `github.com/charmbracelet/bubbles` | v0.21.0 | textarea、viewport 等可复用组件 |
| 终端样式 | `github.com/charmbracelet/lipgloss` | v1.1.0 | ANSI 样式与布局 |
| Markdown 渲染 | `github.com/charmbracelet/glamour` | v1.0.0（间接依赖） | 对话区 Markdown 实时渲染 |
| 配置系统 | `github.com/spf13/viper` | v1.20.1 | YAML 配置加载 |
| UUID 生成 | `github.com/google/uuid` | v1.6.0 | Session ID 生成 |
| 文件监听 | `github.com/fsnotify/fsnotify` | v1.8.0 | Recipe 热重载 |

### 代码组织

```
cmd/wukong/
  └── main.go                              # 入口点，3 行核心逻辑

internal/cli/                               # CLI 命令包（29 源文件 + shutdown）
  ├── root.go                              # 根命令定义，30 个子命令注册
  ├── session.go                           # 会话命令 + bootstrapSession() 启动引擎
  ├── run.go                               # 单发/对话模式 + resolveInput()
  ├── server.go                            # 无头服务器模式
  ├── config.go                            # config validate/show（完整校验走 LoadAndValidate）
  ├── configure.go                         # 交互式 5 步配置向导
  ├── init.go                              # 项目初始化
  ├── health.go                            # 健康检查 + collectSystemInfo()
  ├── env.go                               # 环境信息 + buildEnvInfo()
  ├── version.go                           # 版本输出
  ├── extension.go                         # MCP 扩展管理
  ├── approval_adapter.go                  # ACP 人工审批适配（security.ApprovalBroker ↔ server.ApprovalSink）
  ├── shutdown.go                          # 统一幂等关闭（sync.Once + 15s 看门狗）
  ├── *_mgmt.go                            # 各子系统管理命令（11 个文件）
  └── tui/                                 # Bubble Tea TUI 子包（3 源文件）
      ├── model.go                         # Model 结构（~46 字段）+ StartTUI()
      ├── update.go                        # 事件分发 + sendMessage() 流式管道
      └── view.go                          # Lipgloss 渲染 + ThemeType + ColorPalette
```

### 架构全景

```
┌──────────────────────────────────────────────────────┐
│                cmd/wukong/main.go                      │
│                    cli.Execute()                       │
└────────────────────────┬─────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────┐
│              internal/cli (Cobra 根命令)                │
│                                                        │
│  PersistentFlags: --debug/-D, --quiet                  │
│  PersistentPreRunE: util.SetDebugMode / SetQuietMode   │
│                                                        │
│  ┌─────────┐ ┌────────┐ ┌─────┐ ┌──────────────────┐ │
│  │ session │ │ server │ │ run │ │ 27 其他命令       │ │
│  │ (TUI)   │ │(headless)│(CLI)│ │ (config/health..) │ │
│  └────┬────┘ └───┬────┘ └──┬──┘ └──────────────────┘ │
│       │           │        │                           │
│       └───────────┼────────┘                           │
│                   │                                     │
│         bootstrapSession()                              │
│         (共享启动引擎 ~1400 行)                          │
└───────────────────┼────────────────────────────────────┘
                    │
     ┌──────────────┼──────────────┐
     │              │              │
┌────▼────┐  ┌──────▼──────┐  ┌────▼──────────┐
│  TUI    │  │   Agent     │  │ BootstrapState │
│ Bubble  │  │  CoreLoop   │  │ (资源容器)      │
│  Tea    │  │             │  │                │
└─────────┘  └─────────────┘  └────────────────┘
```

---

## 2. 入口与根命令

### 2.1 main.go — 极简入口

```go
// cmd/wukong/main.go
func main() {
    if err := cli.Execute(); err != nil {
        fmt.Fprintf(os.Stderr, "wukong: %v\n", err)
        os.Exit(1)
    }
}
```

`cli.Execute()` 调用 `newRootCmd().Execute()`，由 Cobra 处理子命令路由。退出码：0 = 成功，1 = 命令级错误。

### 2.2 根命令 (root.go)

根命令定义在 [root.go](../internal/cli/root.go)：

```go
func newRootCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:           "wukong",
        Short:         "Wukong - A local-first extensible AI agent platform",
        SilenceUsage:  true,   // 错误时不打印用法，减少噪音
        SilenceErrors: true,   // 错误由 main() 统一格式化
        PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
            if debugEnabled {
                util.SetDebugMode()
            } else if quietEnabled {
                util.SetQuietMode()
            }
            return nil
        },
    }
    // 注册 30 个子命令（见下文完整树）
    cmd.AddCommand(newSessionCmd())
    // ...
    return cmd
}
```

**设计要点：**

- `SilenceUsage: true` + `SilenceErrors: true` — 避免重复输出，错误由 `main()` 中 `fmt.Fprintf(os.Stderr, ...)` 统一处理。
- `PersistentPreRunE` 在所有子命令执行前调用，优先级：`--debug` > `--quiet` > 配置文件 `log_level`。

### 2.3 持久化标志

| 标志 | 短选项 | 类型 | 默认值 | 生效函数 | 作用 |
|------|--------|------|--------|----------|------|
| `--debug` | `-D` | bool | `false` | `util.SetDebugMode()` | 启用 debug 级日志（最详细） |
| `--quiet` | — | bool | `false` | `util.SetQuietMode()` | 仅输出 warn/error（TUI 模式自动启用） |

```go
cmd.PersistentFlags().BoolVarP(&debugEnabled, "debug", "D", false, "Enable debug-level logging")
cmd.PersistentFlags().BoolVar(&quietEnabled, "quiet", false, "Suppress all log output (warn and errors only)")
```

---

## 3. CLI 命令树全景

`newRootCmd()` 注册了 **32 个顶层子命令**，按功能分为 7 大类。

### 3.1 完整命令树

```
wukong
├── session                    # 【默认】启动 Bubble Tea TUI 交互会话
│   ├── list                   #   列出所有会话
│   ├── delete <id> [rm]       #   删除会话
│   ├── export <id>            #   导出会话（-f markdown|json）
│   ├── info <id>              #   会话详情
│   └── resume                 #   恢复最近会话
├── run [flags] [message...]   # 单发执行或多轮对话（-d）
├── server                     # 无头服务器模式（A2A/ACP/AG-UI/MCP 端点）
├── configure                  # 交互式 5 步配置向导
├── config
│   ├── validate               #   12 项配置校验
│   └── show                   #   显示合并后有效配置（YAML）
├── migrate                    # 应用全部待执行 schema 迁移（P0-3）
├── version                    # 版本信息（Version + GitCommit + BuildDate）
├── init [dir]                 # 项目初始化（.wukong/ 结构 + config.yaml）
├── completion [shell]         # Shell 自动补全（bash/zsh/fish/powershell）
├── eval                       # 评估测试集
├── project                    # 交互式项目选择与会话恢复
├── projects                   # 列出所有追踪的项目目录
├── health [--json]            # 系统健康检查
├── env [--json]               # 运行环境信息
├── extension                  # MCP 扩展管理
│   ├── install [url]          #   从 deeplink 安装
│   ├── list                   #   列出扩展
│   ├── enable/disable <name>  #   启用/禁用
│   ├── show <name>            #   详情
│   └── remove <name>          #   移除
├── caps                       # 能力注册表（P0-1）
│   ├── list [namespace]       #   列出已注册能力（--json / 前缀过滤）
│   └── run <address>          #   按地址直接调用能力（--args JSON）
├── memory                     # 记忆管理
├── provider                   # Provider 管理
├── skill                      # 技能管理（list/show）
├── recipe                     # 配方管理
├── knowledge                  # 知识库管理
├── ard                        # ARD 资源发现（status/catalog）
├── evolution                  # 进化引擎（status/history/versions/rollback/diff/log）
├── cortex                     # CortexDB 管理（status）
├── todo                       # 任务管理
├── docs                       # 打开文档（浏览器）
├── stats                      # 系统统计仪表盘
├── bench                      # 基准测试
├── backup                     # 数据库备份
├── system-check               # 系统就绪诊断
├── apps                       # 应用管理（11 子命令）
│   ├── list / show / create / clone / probe / pack
│   ├── view / delete / history / export / download
└── search tune                # 搜索策略调优
    ├── validate / plan / run / report / compare
```

### 3.2 命令分类汇总表

| 分类 | 命令 | 对应文件 | 说明 |
|------|------|----------|------|
| **交互会话** | `session`, `server`, `run` | session.go, server.go, run.go | 三种运行模式，共享 `bootstrapSession()` |
| **配置管理** | `config`, `configure`, `init` | config.go, configure.go, init.go | 校验/向导/初始化 |
| **系统诊断** | `health`, `env`, `version`, `stats`, `docs`, `completion`, `system-check`, `backup` | health.go, env.go, version.go, utils.go, bench.go | 健康/环境/版本/统计 |
| **项目管理** | `project`, `projects`, `bench` | project.go, bench.go | 项目追踪与基准 |
| **扩展管理** | `extension`, `caps` | extension.go, caps.go | MCP 扩展生命周期 · 能力注册表查看 |
| **资源管理** | `memory`, `provider`, `skill`, `recipe`, `knowledge`, `ard`, `evolution`, `cortex`, `todo`, `apps`, `eval` | *_mgmt.go, eval.go | 各子系统 CRUD |
| **搜索调优** | `search tune` | search_tune.go | 搜索参数自迭代 |

---

## 4. 核心命令详解

### 4.1 `run` — 单发/对话模式

定义在 [run.go](../internal/cli/run.go)，用于终端管道集成和多轮 Shell 对话。

**标志表：**

| 标志 | 短选项 | 类型 | 默认值 | 说明 |
|------|--------|------|--------|------|
| `--config` | `-c` | string | `""` | 配置文件路径（默认自动发现） |
| `--message` | `-m` | string | `""` | Prompt 文本（最高优先级） |
| `--provider` | `-p` | string | `""` | Provider 名称（覆盖配置） |
| `--model` | — | string | `""` | 模型名称（覆盖 Provider 默认） |
| `--temperature` | — | float64 | `-1` | 温度（-1 = 用配置） |
| `--max-tokens` | — | int | `0` | 最大输出 token（0 = 用配置） |
| `--no-stream` | — | bool | `false` | 禁用流式输出 |
| `--session-id` | `-s` | string | `""` | 会话 ID（空则自动生成 UUID） |
| `--dialogue` | `-d` | bool | `false` | 进入多轮 Shell 对话模式 |

**运行模式：**

| 模式 | 用法 | 入口函数 |
|------|------|----------|
| 单发 | `wukong run -m "prompt"` | `runOneShot()` |
| 管道 | `echo "prompt" \| wukong run` | `runOneShot()`（stdin 读取） |
| 位置参数 | `wukong run explain this code` | `runOneShot()`（拼接 args） |
| 对话 | `wukong run -d` | `runDialogue()`（REPL 循环） |

**对话模式内置命令（`runDialogue()`）：**

| 命令 | 说明 |
|------|------|
| `/exit`, `/quit` | 退出对话 |
| `/session` | 显示当前 Session ID |
| `/clear` | ANSI 清屏（`\033[2J\033[H`） |
| `/help` | 帮助信息 |

### 4.2 `session` — 交互会话（默认模式）

定义在 [session.go](../internal/cli/session.go)，是 `wukong` 的默认行为。

**标志表：**

| 标志 | 短选项 | 类型 | 默认值 | 说明 |
|------|--------|------|--------|------|
| `--provider` | `-p` | string | `""` | Provider 名称 |
| `--session-id` | `-s` | string | `""` | 恢复指定会话 |
| `--model` | `-m` | string | `""` | 模型名称 |
| `--config` | `-c` | string | `""` | 配置文件路径 |
| `--temperature` | — | float64 | `-1` | 温度 |
| `--max-tokens` | — | int | `0` | 最大 token |
| `--no-stream` | — | bool | `false` | 禁用流式 |

**子命令树：**

```
wukong session [flags]              # 启动 TUI
  ├── list                          # 列出所有会话（session_mgmt.go）
  ├── delete <id> [rm]              # 删除会话
  ├── export <id> [-f markdown|json]# 导出会话（session_export.go）
  ├── info <id>                     # 会话详情
  └── resume                        # 快速恢复最近会话
```

`runSession()` 流程：解析标志 → `resolveUserID()` → `bootstrapSession()` → `StartTUI()` → `shutdownBootstrap()`。

### 4.3 `server` — 无头服务器模式

定义在 [server.go](../internal/cli/server.go)，启动所有协议端点，适合 API 集成和远程调用。

- 暴露端点：A2A、ACP、AG-UI、ACP-MCP、MCP Server
- 健康检查 HTTP 端口 `:8086`：`/healthz`（综合）、`/readyz`（就绪）、`/livez`（存活）
- `registerHealthCheckers()` 注册各子系统健康探针
- 不启动 TUI，不监听终端输入

### 4.4 `config` — 配置管理

定义在 [config.go](../internal/cli/config.go)。

**`config validate` — 与启动路径相同的完整校验：**

调用 `loader.LoadAndValidate()`（与 `bootstrapSession()` 同一入口），执行 `internal/config/validate.go` 的全部致命规则（todo/mcp_server/sandbox/端口冲突等，完整清单见 [CONFIG.md §3](./CONFIG.md#3-配置验证)），随后列出全部非致命警告（`Warnings()` + default_provider 缺失提示）。致命错误退出码 1；仅有警告时退出码 0。

> 另有轻量咨询性校验函数 `runFullValidation()`（`bench`/`health` 命令使用）：枚举与区间规则委托给 `config.WukongConfig.Validate()`（与启动路径同一份规则，永不漂移），另加 Validate 不视为致命的咨询项——默认 provider 缺 model/缺 api_key、ACP provider 缺 `agent_url`、非法 planner、`lightweight_provider` 回退链断裂。

### 4.5 `configure` — 交互式配置向导

定义在 [configure.go](../internal/cli/configure.go)，引导用户完成 5 步配置：

1. **Provider** — 选择默认 LLM 提供商
2. **Providers** — 配置多个 Provider 详情
3. **Extensions** — 启用/禁用扩展
4. **Agent** — Agent 参数（Planner、并行工具等）
5. **Security** — 权限模式与安全护栏

### 4.6 `init` — 项目初始化

定义在 [init.go](../internal/cli/init.go)，创建项目目录结构：

```
.wukong/
  ├── apps/                   # HTML 应用
  ├── cache/                  # 浏览器缓存
  ├── recipes/                # Recipe 定义
  ├── skills/                 # 技能定义
  └── visuals/                # 可视化输出
.wukongignore                 # 文件访问黑名单
.wukong/instructions.md       # 持久化 Agent 指令
config.yaml                   # 示例配置（仅在不存在时创建）
```

### 4.7 `health` 与 `env`

- `health [--json]`（[health.go](../internal/cli/health.go)）：`collectSystemInfo()` 收集系统信息，`printHealthTable()` 或 `printHealthJSON()` 输出。
- `env [--json]`（[env.go](../internal/cli/env.go)）：`buildEnvInfo()` 构建环境信息。

### 4.8 `apps` — 应用管理

定义在 [apps_mgmt.go](../internal/cli/apps_mgmt.go)，**11 个子命令**：

```
wukong apps
  ├── list       # 列出应用
  ├── show       # 应用详情
  ├── create     # 创建应用
  ├── clone      # 克隆网站为应用
  ├── probe      # 探测应用
  ├── pack       # 打包应用为 ZIM
  ├── view       # 查看应用
  ├── delete     # 删除应用
  ├── history    # 历史记录
  ├── export     # 导出应用
  └── download   # 下载应用
```

### 4.9 资源管理命令速查

| 命令 | 对应文件 | 子命令 |
|------|----------|--------|
| `evolution` | evolution_mgmt.go | status / history \<skill-name\> [--limit] / versions \<skill-name\> / rollback \<skill-name\> \<version\> [--force] / diff \<skill-name\> \<v1\> [v2] / log \<skill-name\> [--limit] |
| `ard` | ard_mgmt.go | status / catalog |
| `cortex` | cortex_mgmt.go | status |
| `skill` | skill_mgmt.go | list / show |
| `memory` | memory_mgmt.go | list / search / delete / clear |
| `provider` | provider_mgmt.go | list / test |
| `recipe` | recipe_mgmt.go | list / show / validate |
| `knowledge` | knowledge_mgmt.go | status |
| `todo` | todo_mgmt.go | status |
| `project` | project.go | clear（清空全部已追踪项目记录；父命令为交互式项目选择） |
| `search tune` | search_tune.go | validate / plan / run / report / compare |

**evolution status 输出分组：** `Status`、`[Analysis]`（auto_patch / min_confidence / analysis_timeout / analysis_provider / analysis_model）、`[Rate Limiting]`（cooldown_period / max_patches_per_day）、`[Version Control]`（max_versions_kept / max_patch_size）、`[Integration]`（export_json）、`[Problem Types Detected]`。

---

## 5. Bootstrap 启动引擎

`bootstrapSession()` 是所有交互模式（`session`、`server`、`run`）共享的启动引擎，位于 [session.go](../internal/cli/session.go)，约 **1400 行**。

### 5.1 签名

```go
func bootstrapSession(
    configPath, userID, sessionID, providerName, modelName string,
    temperature float64, maxTokens int, noStream bool,
) (*config.WukongConfig, *agent.CoreLoop, *BootstrapState, error)
```

### 5.2 启动时序（9 阶段）

```
Phase 1: 配置加载
├── config.NewLoader(configPath)
├── loader.LoadAndValidate() → WukongConfig
├── cfg.Warnings() → 非致命警告日志
├── validateConfig() → 二次校验
└── util.SetLogLevel()（仅 --debug/--quiet 未设置时）

Phase 2: 遥测与覆盖
├── telemetry.NewManager(cfg.Telemetry)
├── telMgr.Initialize(ctx) → telShutdown（延迟到 CoreLoop 关闭时调用）
├── builtin.RegisterBuiltins(cfg) → 注册 12 个内置扩展
└── applyOverrides(cfg, provider, model, temp, maxTokens, noStream)

Phase 3: 模型与数据库
├── provider.NewFactory(cfg) → LLM 工厂
├── util.NewMultiPool(session.DBPath) → SQLite 多连接池
│   └── dbPool.Shared() → 所有子系统共享同一个 wukong.db
├── wksession.NewSessionService() → 会话持久化
├── memory.NewMemoryManager() + extractorModel
└── memoryMgr.SmartCleanup() → 启动时清理低重要性记忆

Phase 4: 安全与扩展
├── security.NewGuard(cfg.Security) → 安全护栏
├── guardCheck 闭包 → 统一工具权限检查（ACP + MCP 共用）
├── extension.NewManager(cfg) → MCP 扩展管理器
├── extMgr.Initialize(ctx) → 启动所有外部 MCP 进程
├── extMgr.SetMemoryService() → 注入记忆服务
├── ard.NewToolSet() → ARD 资源发现（可选）
└── ard.PublishAndServe() → ARD 注册服务器（可选）

Phase 5: MCP 桥接
├── extension.NewACPMCPBridge() → ACP↔MCP 桥接
├── acpMCPBridge.Start()
├── factory.SetACPMCPAddr() → ACP Provider 发现工具
└── extension.NewMCPServerWithSecurity() → 标准 MCP Server（可选）

Phase 6: 记忆与召回
├── Cortex.Enabled → cortex.NewStore() + cortex.RecallStore()
├── Recall.Enabled → recall.NewStore()（SQLite FTS5）
├── cortex.NewMemoryFlowWithDB() → 对话转录与唤醒
├── cortex.NewGraphFlowService() → 知识图谱自动抽取（可选）
└── cortex.NewRecallManager() → 向量增强召回

Phase 7: 进化引擎
├── evolution.NewEngine()
│   ├── EvolutionAnalyzer（LLM 分析器）
│   ├── EvolutionPatcher（补丁应用器，哈希去重）
│   └── VersionStore（版本持久化）
└── evolutionEngine.Start() → 后台分析 Worker

Phase 8: Agent 组装
├── agent.NewCoreLoop(CoreLoopConfig)
│   ├── 合并所有 ToolSets + FunctionTools
│   ├── TopOfMindInstructions（持久指令注入）
│   ├── RevisionModel（上下文摘要）
│   ├── EvolutionTracker（执行轨迹捕获插件）
│   ├── guardrail + promptinjection（安全插件）
│   └── toolsearch（工具搜索插件）
└── project.NewManager() → 项目追踪

Phase 9: 协议服务器（goroutine）
├── summon.NewA2AServer()       → :9090
├── server.NewAGUIServer()      → :8080（SSE）
├── server.NewACPServer()       → :9091
├── ANP Protocol Stack          → :9092
│   ├── ard.NewDIDManager()（W3C DID 身份）
│   ├── summon.NewMetaProtocol()（能力协商）
│   └── summon.NewE2EEMessenger()（端到端加密）
├── GatewayServer               → 多平台消息入口（飞书等）
│   └── gateway.NewGatewayServer()（去重 / 限流 / 会话映射）
└── sandbox.Probe()             → 沙箱能力检测
```

### 5.3 BootstrapState 资源容器

`BootstrapState` 内嵌 `shutdownState`（`sync.Once` 幂等保护），是所有协议服务器和管理器的统一容器：

```go
type BootstrapState struct {
    shutdownState // sync.Once 幂等关闭保护

    A2AServer         *summon.A2AServer
    AGUIServer        *server.AGUIServer
    ACPServer         *server.ACPServer
    ACPMCPBridge      *extension.ACPMCPBridge
    MCPServer         *extension.MCPServer
    ARDRegistry       *ard.RegistryServer
    ANPServer         *http.Server
    ANPMeta           *summon.MetaProtocol
    ANPMessenger      *summon.E2EEMessenger
    CredentialRotator *summon.CredentialRotator
    ExtMgr            *extension.Manager
    KnowledgeMgr      *knowledge.Manager
    ProjectMgr        *project.Manager
    GatewayServer     *gateway.GatewayServer

    DBPing func(ctx context.Context) error // 共享 DB 池存活探针
}
```

### 5.4 applyOverrides — CLI 覆盖机制

`applyOverrides()` 在配置加载后应用 CLI 标志覆盖：

```go
applyOverrides(wukongCfg, providerName, modelName, temperature, maxTokens, noStream)
```

优先级：**CLI 标志 > 配置文件 > 默认值**。仅当 CLI 参数非零值时覆盖。

---

## 6. TUI 架构（Bubble Tea）

TUI 位于 [internal/cli/tui/](../internal/cli/tui/)，采用 Elm 架构（Model-View-Update）。

### 6.1 Elm 架构

```
         ┌─────── Msg ───────┐
         ▼                   │
    ┌─────────┐         ┌─────────┐
    │ Update  │────────▶│  Model  │
    │(事件转换)│         │ (状态)  │
    └────┬────┘         └────┬────┘
         │ Cmd               │
         ▼                   ▼
    ┌─────────┐         ┌─────────┐
    │ Runtime │         │  View   │
    │(异步执行)│         │(纯渲染) │
    └─────────┘         └─────────┘
```

### 6.2 Model 结构（model.go）

定义在 [model.go](../internal/cli/tui/model.go)，约 **46 个字段**：

```go
type Model struct {
    // 交互组件
    viewport viewport.Model    // 对话滚动视口
    textarea textarea.Model    // 输入区域

    // 会话状态
    userID    string
    sessionID string
    messages  []chatEntry      // 对话历史（上限 500 条）
    status    string           // 状态栏文本

    // 工具调用显示
    toolCalls []toolCallEntry  // 活跃工具调用列表
    auditLog  []toolAuditEntry // 审计日志（上限 50 条）

    // Agent 依赖
    loop *agent.CoreLoop
    cfg  *config.WukongConfig

    // 流式状态
    streaming     bool
    currentStream string
    streamCancel  func()       // context.CancelFunc（Ctrl+C 中断）
    streamCh      <-chan streamEvent
    streamDone    chan struct{} // goroutine 完成信号

    // 退出控制
    quitRequested bool
    cleanupOnce   sync.Once

    // 显示信息
    modelName    string
    providerName string
    toolCount    int
    skillName    string
    version      string

    // 布局
    width   int
    height  int
    ready   bool
    workingDir string

    // 项目追踪
    projectMgr    any   // *project.Manager（避免循环导入）
    instrRecorded bool

    // 日志与模态窗口
    logBuffer    []string
    modal        *modalState
    modalHeight  int
    modalWidth   int

    // Markdown 渲染器（glamour）
    mdRenderer *glamour.TermRenderer

    // 命令历史（Up/Down 导航）
    cmdHistory    []string
    cmdHistoryIdx int

    // 增量渲染缓存（双层独立，避免工具状态变化触发全量重建）
    cachedMessages      string
    cachedMsgCount      int
    cachedTools         string
    cachedToolCount     int
    cachedToolStatus    []string
    cachedToolCollapsed []bool
    cachedToolSelected  int

    // 导航状态
    toolSelectedIdx int
    autoScroll      bool   // false = 用户手动上滚
    cancelled       bool   // Ctrl+C 已按一次

    lastRenderTime time.Time // 16ms 防抖
}
```

**关键常量：**

```go
const maxMessages = 500          // 内存中保留的最大对话条数
const maxCommandHistory = 100    // 命令历史上限
const maxAuditEntries = 50       // 审计日志上限
```

### 6.3 ModalType — 模态窗口

```go
type ModalType int
const (
    ModalNone     ModalType = iota
    ModalCommands            // 命令菜单（/）
    ModalSkills              // 技能浏览（/skills）
    ModalSettings            // 设置面板（/settings）
    ModalProjects            // 项目选择（/projects，v0.3.3+）
    ModalSessions            // 会话管理（/sessions，v0.3.3+）
)
```

**v0.3.3 新增会话/项目管理模态：**

- **`/sessions`**（ModalSessions）：列出当前全部会话（会话 ID + 最近时间戳），支持 `+ New session` 新建、选中后 Enter 恢复（走 `/resume <sessionID>` 语义）、Backspace 删除选中会话。TUI 通过轻量 `sessionLister` 接口（`ListSessions`/`DeleteSession`）与 `wksession.SessionService` 解耦，不直接依赖 session 包。
- **`/projects`**（ModalProjects）：列出工作目录记录（路径 + 8 位截断会话 ID + 指令摘要），选中后通过 `mgr.ListProjects()` 重新查询完整记录并复用恢复语义。

### 6.4 三区布局（View 渲染）

```
┌─────────────────────────────────────────────────┐
│  Banner（版本 + Provider）                        │ ← RenderBanner
├─────────────────────────────────────────────────┤
│  Model: deepseek-chat | Tools: 8 | Ready         │ ← RenderStatusBarBottom
├─────────────────────────────────────────────────┤
│  🟢 Wukong Ready                                 │
│  User: Write a sorting function                  │ ← Viewport
│  Wukong: Here's a quicksort...                   │   (可滚动)
│  ◉ read_file ✓ write_file ● search_code          │   (工具面板)
├─────────────────────────────────────────────────┤
│  Type your message... (Ctrl+D to send)           │ ← Textarea
└─────────────────────────────────────────────────┘
```

`View()` 方法使用 `lipgloss.JoinVertical` 垂直拼接四层：

```go
func (m *Model) View() string {
    banner := RenderBanner(m.version, m.providerName, m.width)
    statusBar := RenderStatusBarBottom(...)
    conversation := m.viewport.View()
    inputArea := m.textarea.View()
    return lipgloss.JoinVertical(lipgloss.Top, banner, statusBar, conversation, inputArea)
}
```

**布局计算（`recalculateLayout()`）：**

```go
availableHeight := m.height - bannerHeight - statusBarHeight - inputHeight
if availableHeight < 5 { availableHeight = 5 }
m.viewport.Height = availableHeight
```

### 6.5 配色方案与多主题（view.go）

定义在 [view.go](../internal/cli/tui/view.go)。

**ThemeType 枚举：**

```go
type ThemeType int
const (
    ThemeDark    ThemeType = iota  // 默认
    ThemeLight
    ThemeClassic
)
```

**ColorPalette（12 字段）：**

| 字段 | Dark 主题 | Light 主题 | Classic 主题 | 用途 |
|------|-----------|------------|--------------|------|
| User | `120` (绿) | `22` | `34` | 用户消息前缀 |
| Assistant | `213` (粉) | `54` | `35` | Wukong 回复前缀 |
| Status | `63` (蓝) | `25` | `36` | 状态栏 |
| Running | `226` (黄) | `178` | `33` | 运行中工具 |
| Done | `42` (绿) | `28` | `32` | 已完成工具 |
| Error | `196` (红) | `160` | `31` | 错误工具 |
| Dim | `240` (灰) | `245` | `90` | 次要信息 |
| Accent | `147` | `29` | `37` | 强调色 |
| Border | `237` | `240` | `245` | 边框 |
| Banner | `234` | `252` | `240` | 横幅背景 |
| BannerFg | `255` | `0` | `235` | 横幅前景 |
| StatusBg | `237` | `252` | `240` | 状态栏背景 |

通过 `/theme [dark|light|classic]` 命令切换主题。

### 6.6 内置 TUI 命令

`handleCommand()` 处理以 `/` 开头的输入：

| 命令 | 说明 |
|------|------|
| `/exit`, `/quit` | 退出 TUI |
| `/new` | 生成新 Session ID（UUID），清空对话 |
| `/clear` | 清空视口内容（服务端会话数据保留） |
| `/model` | 显示当前 Provider/Model |
| `/model <name>` | 动态切换模型 |
| `/exts` | 列出已加载的扩展 |
| `/theme [name]` | 显示/切换主题 |
| `/audit [N]` | 显示工具审计日志（最近 N 条） |
| `/commands` | 打开命令菜单模态窗口 |
| `/skills` | 打开技能浏览模态窗口 |
| `/settings` | 打开设置面板模态窗口 |
| `/help` | 显示帮助 |

### 6.7 快捷键

| 快捷键 | 上下文 | 行为 |
|--------|--------|------|
| `Ctrl+D` | 输入区有内容 | 发送消息 |
| `Ctrl+D` | 输入区为空 | 无操作 |
| `Ctrl+D` | 流式输出中 | 忽略（提示按 Ctrl+C） |
| `Ctrl+C` | 流式输出中 | 取消当前请求（不退出） |
| `Ctrl+C` | 空闲状态 | 退出 TUI |
| `Ctrl+C` | 已取消后再次按 | 强制退出 |
| `PgUp` / `PgDown` | 任何时候 | 翻页滚动（关闭自动跟随） |
| `Alt+↑/↓` | 任何时候 | 逐行滚动 |
| `Alt+Home/End` | 任何时候 | 跳顶/跳底 |
| `Tab` | 有工具结果 | 展开/折叠工具详情 |
| `Shift+Tab` | 有工具 | 切换工具选择 |
| `↑/↓` | 非流式、单行输入 | 浏览命令历史 |

### 6.8 增量渲染优化

`updateViewport()` 采用双层独立缓存，避免工具状态变化触发全量消息重建：

```
┌──────────────────────────────────────────────────┐
│  cachedMessages (消息层)                          │
│  仅当 msgCount 变化时增量追加新消息                 │
├──────────────────────────────────────────────────┤
│  cachedTools (工具层)                             │
│  仅当 toolCount/Status/Collapsed 变化时重建       │
└──────────────────────────────────────────────────┘
```

流式增量更新（`streamingDeltaMsg`）通过 **16ms 防抖** 减少渲染抖动，仅结构性变化（消息/工具增减）立即渲染。

### 6.9 StartTUI 入口

```go
func StartTUI(cfg *config.WukongConfig, loop *agent.CoreLoop,
    userID, sessionID, workingDir string, projectMgr any, version string,
) error {
    util.SetQuietMode()  // TUI 模式自动静默日志
    m := NewModel(ModelConfig{...})
    p := tea.NewProgram(m, tea.WithAltScreen())
    _, err := p.Run()
    return err
}
```

---

## 7. 流式传输与事件管道

### 7.1 架构概览

TUI 的流式传输通过 **Goroutine + Channel** 桥接模式实现，定义在 [update.go](../internal/cli/tui/update.go)：

```
┌─────────────────────────────────────────────────────────┐
│                TUI Update Loop (主 goroutine)             │
│                                                           │
│  Ctrl+D → sendMessage(input)                              │
│               │                                           │
│               ├── addMessage("user", input)               │
│               ├── ctx, cancel := context.WithCancel(bg)   │
│               ├── streamCh := make(chan streamEvent, 64)  │
│               ├── streamDone := make(chan struct{})        │
│               │                                           │
│               ├── go func() { ──────────┐                 │
│               │     loop.Run(ctx,...)   │ Agent goroutine │
│               │     → events channel    │ (context 控制)  │
│               │     遍历 events:        │                 │
│               │       Delta → streamCh  │                 │
│               │       ToolCall → streamCh│                │
│               │       Error → streamCh  │                 │
│               │       End → streamCh    │                 │
│               │     close(streamCh)     │                 │
│               │     close(streamDone)   │                 │
│               │     cancel()            │                 │
│               │   }() ──────────────────┘                 │
│               │                                           │
│               └── readStreamEvent(streamCh) → tea.Cmd     │
│                                                           │
│  事件处理 (Update 方法):                                   │
│    streamingDeltaMsg → m.currentStream += delta           │
│    toolCallStartMsg  → m.toolCalls 追加                   │
│    toolCallResultMsg → 匹配 Name 更新状态                  │
│    streamingErrorMsg → 显示错误                           │
│    streamEndMsg      → 添加 assistant 消息，重置状态       │
└─────────────────────────────────────────────────────────┘
```

### 7.2 streamEvent 结构

```go
type streamEvent struct {
    Delta      string              // 增量文本
    Tool       *toolCallStartMsg   // 工具调用开始
    ToolResult *toolCallResultMsg  // 工具调用完成
    Err        string              // 错误信息
    IsEnd      bool                // 流结束标志
    Content    string              // 完整内容（fallback）
}
```

### 7.3 事件消息类型

```go
type streamingDeltaMsg string        // 增量内容
type toolCallStartMsg struct {       // 工具调用开始
    Name string
    Args string
}
type toolCallResultMsg struct {      // 工具调用完成
    Name   string                    // 匹配 toolCallEntry.Name
    Result string                    // 支持并发工具结果路由
}
type streamingErrorMsg string        // 流式错误
type streamEndMsg struct {           // 流结束
    Content string                   // 完整内容
}
```

### 7.4 sendMessage 可取消机制

`sendMessage()` 创建独立的 `context.WithCancel`，存储取消函数供 Ctrl+C 调用：

```go
func (m *Model) sendMessage(input string) tea.Cmd {
    ctx, cancel := context.WithCancel(context.Background())
    m.streamCancel = cancel

    streamCh := make(chan streamEvent, 64)
    m.streamCh = streamCh
    streamDone := make(chan struct{})
    m.streamDone = streamDone

    go func() {
        defer close(streamCh)
        defer close(streamDone)
        defer cancel()

        // 超时控制
        timeout := m.cfg.Agent.MaxRunDuration
        ctx, timeoutCancel := context.WithTimeout(ctx, timeout)
        defer timeoutCancel()

        events, err := m.loop.Run(ctx, m.userID, m.sessionID, msg)
        // ... 遍历 events，发送到 streamCh
    }()

    return readStreamEvent(m.streamCh)
}
```

### 7.5 Ctrl+C 中断处理

```go
case tea.KeyCtrlC:
    if m.streaming {
        if m.streamCancel != nil {
            m.streamCancel()  // 取消 agent goroutine 的 context
        }
        m.cancelled = true
        m.status = "Cancelling..."
        // 保留已生成的部分内容，等待 streamEndMsg
        return m, nil  // 继续消费 streamCh
    }
    // 空闲状态：退出 TUI
    return m, m.requestExit()
```

中断后，已生成的增量内容通过 `streamEndMsg` 保留为 assistant 消息。

### 7.6 内容不丢失机制

```go
case streamEndMsg:
    var finalContent string
    if m.currentStream != "" {
        finalContent = m.currentStream    // 优先使用累计增量
    } else if msg.Content != "" {
        finalContent = msg.Content         // 回退到结构化 Content
    }
```

### 7.7 错误友好化

`friendlyError()` 将原始错误映射为用户可读提示：

| 原始错误 | 友好提示 |
|----------|----------|
| `context deadline exceeded` | 请求超时——模型响应时间过长 |
| `connection refused` | 无法连接到模型——检查网络/Provider |
| `401` / `unauthorized` | 认证失败——检查 API Key |
| `429` / `rate limit` | 被 Provider 限流——稍后重试 |
| `500` / `502` / `503` | 模型服务不可用 |
| `canceled` | 用户取消了请求 |

---

## 8. 输入解析与会话标识

### 8.1 run 命令输入优先级

`resolveInput()`（[run.go](../internal/cli/run.go)）按优先级确定 prompt 文本：

```
1. --message / -m 标志         （最高优先级）
2. 位置参数拼接                 （args... 用空格连接）
3. stdin 管道输入               （非 TTY 时读取）
4. 空字符串                     （触发 --dialogue 模式或报错）
```

```go
func resolveInput(flagMsg string, args []string) string {
    if flagMsg != "" {
        return flagMsg
    }
    if len(args) > 0 {
        return strings.Join(args, " ")
    }
    stat, _ := os.Stdin.Stat()
    if (stat.Mode() & os.ModeCharDevice) == 0 {
        data, err := io.ReadAll(os.Stdin)
        if err == nil && len(data) > 0 {
            return strings.TrimSpace(string(data))
        }
    }
    return ""
}
```

### 8.2 用户标识解析

`resolveUserID()` 跨平台处理用户识别：

| 优先级 | 来源 | 平台 |
|--------|------|------|
| 1 | `$USER` 环境变量 | Unix |
| 2 | `$USERDOMAIN\$USERNAME` | Windows |
| 3 | `$USERNAME`（非 SYSTEM） | Windows |
| 4 | `os.Hostname()` | 通用 |
| 5 | `"default"` | 兜底 |

### 8.3 Session ID 设计

- 使用 UUID v4（`github.com/google/uuid`）
- TUI 显示截取前 8 位
- 支持 `--session-id` / `-s` 恢复历史会话
- `/new` 命令生成新 Session ID，旧数据保留在数据库
- `run -d` 对话模式同样支持 `-s` 恢复

---

## 9. 优雅关闭与看门狗

所有模式退出时委托 `shutdownBootstrap()`（[shutdown.go](../internal/cli/shutdown.go)），由 `sync.Once` 保护（幂等）。

### 9.1 15 秒硬看门狗

部分 `.Close()` 调用可能忽略 context 超时（如 knowledge 的 gse 词典加载、MCP 子进程 drain、telemetry flush）。看门狗 goroutine 确保进程不挂起：

```go
go func() {
    wdCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
    defer cancel()
    <-wdCtx.Done()
    util.Logger.Warn("shutdown watchdog: forcing exit")
    os.Exit(0)
}()
```

### 9.2 关闭顺序（`runShutdown`）

best-effort：个别错误被记录但不中断序列。

| 顺序 | 组件 | 说明 |
|------|------|------|
| 1 | Gateway Server | 先关入站通道，防止 teardown 期间触发新 run |
| 2 | A2A Server | `:9090` |
| 3 | AG-UI Server | `:8080` SSE |
| 4 | ACP Server | `:9091` |
| 5 | ACP MCP Bridge | ACP↔MCP 桥接 |
| 6 | MCP Server | 标准 MCP 端点 |
| 7 | ARD Registry | `:9092` 资源发现 |
| 8 | ANP Server | ANP HTTP 服务器 |
| 9 | Credential Rotator | 凭证轮换 |
| 10 | Extension Manager | **必须在 CoreLoop 之前**，关闭 MCP 子进程 |
| 11 | Knowledge Manager | 知识库 / RAG |
| 12 | CoreLoop | **最后关闭**，触发内部清理链 |

**CoreLoop.Close() 内部清理链：**

`Close()` 先以各 5s 超时（`waitWithTimeout`）等待 `runWg`（进行中 RunStream 的同步后置写入）与 `bgWg`（后台协程）退出，再执行 `closeFn`（[loop.go](../internal/agent/loop.go)）：

```
runner.Close()           → 停止 Agent Runner（最先，阻止新任务产生）
EvolutionClose()         → 停止进化引擎后台分析 Worker
MemoryClose()            → 停止记忆提取 Worker（等待 in-flight 任务，最长 5s）
SessionService.Close()   → 停止会话摘要 Worker、释放会话资源
GraphFlowService.Close() → 停止知识图谱抽取引擎
TelemetryShutdown        → flush + 关闭 OpenTelemetry / Langfuse（10s 超时）
DBPoolClose              → 最后关闭共享数据库连接池（PRAGMA wal_checkpoint(TRUNCATE)）
```

---

## 10. 版本与构建信息

版本信息定义在 [internal/util/version.go](../internal/util/version.go)，通过 ldflags 在构建时注入：

```go
package util

var (
    Version   = "0.3.3"
    GitCommit = "fix commit"
    BuildDate = "2026-08-29"
)
```

> ⚠️ **注意**：`GitCommit` 与 `BuildDate` 为占位符，CI 发布时会被 ldflags 覆盖。其中 `BuildDate` 应写作 `"2026-08-29"`（补齐两位数日）。

Makefile 中的 ldflags（[Makefile](../Makefile)）：

```makefile
LDFLAGS := -s -w \
    -X github.com/km269/wukong/internal/util.Version=$(VERSION) \
    -X github.com/km269/wukong/internal/util.GitCommit=$(GIT_COMMIT) \
    -X github.com/km269/wukong/internal/util.BuildDate=$(BUILD_DATE)
```

> ⚠️ **注意**：注入目标应为 `internal/util.*`（版本变量实际定义于 util 包）。Makefile 中若仍写 `internal/cli.*` 则不会生效，需同步修正。

`wukong version` 命令输出 Version、GitCommit、BuildDate 三项信息。

---

## 关键设计决策

### 双框架分离

| Cobra（CLI） | Bubble Tea（TUI） |
|-------------|-------------------|
| 管理命令（config/health/env/apps...） | 交互会话需要实时渲染 |
| 无需 TUI，纯参数→输出 | Elm 架构、声明式渲染 |
| 管道友好（run -m \| jq） | 全屏 AltScreen 模式 |

### Quick Pre-load 模式

`quickLoadConfig()` 在完整 bootstrap 之前快速加载配置，让用户即刻看到 Provider/Model 信息反馈，无需等待全部子系统启动。

### 工具列表一次性注入

所有工具在 bootstrap 阶段组装完毕后，一次性注入 `codeExecutor.SetToolsForDiscovery()`，供 JS 代码沙箱中的 `code_discover_tools` 使用，避免每次 LLM 调用重复计算。

### EvolutionTracker 事件驱动

EvolutionTracker 作为 Runner 级别插件，通过事件监听异步捕获执行轨迹，不侵入主循环。

---

## 附录：相关文档

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | 系统架构详解 |
| [DEVELOPER_GUIDE.md](./DEVELOPER_GUIDE.md) | 开发者指南 |
| [CONFIG.md](./CONFIG.md) | 配置参考手册 |
| [DEPLOYMENT.md](./DEPLOYMENT.md) | 部署运维指南 |
| [MEMORY_ARCHITECTURE.md](./MEMORY_ARCHITECTURE.md) | 记忆系统架构 |

---

> **版本**: v0.3.3 | **最后更新**: 2026-09-03 | **CLI 源文件**: 31 + TUI 3 = 34 | **顶层命令**: 32 | **命令定义**: 88
