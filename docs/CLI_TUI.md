# Wukong CLI & TUI Architecture

## 目录

1. [概览与架构](#1-概览与架构)
2. [入口与启动流程](#2-入口与启动流程)
3. [CLI 命令树](#3-cli-命令树)
4. [TUI 架构](#4-tui-架构)
5. [Bootstrap 启动序列](#5-bootstrap-启动序列)
6. [流式传输与事件管道](#6-流式传输与事件管道)
7. [输入解析策略](#7-输入解析策略)
8. [错误处理架构](#8-错误处理架构)
9. [关键设计决策](#9-关键设计决策)

---

## 1. 概览与架构

Wukong 的 CLI/TUI 层是用户与 AI Agent 平台交互的主要界面，采用 **Cobra + Charmbracelet Bubble Tea** 双框架架构，提供命令行工具和终端 UI 两种交互模式。

### 技术栈

| 组件 | 技术 | 版本 | 作用 |
|------|------|------|------|
| CLI 框架 | `github.com/spf13/cobra` | v1.9.1 | 命令路由、参数解析、帮助生成 |
| TUI 框架 | `github.com/charmbracelet/bubbletea` | v1.3.10 | Elm 架构终端 UI |
| TUI 组件 | `github.com/charmbracelet/bubbles` | v0.21.0 | textarea、viewport 等组件 |
| 终端样式 | `github.com/charmbracelet/lipgloss` | v1.1.0 | 终端 ANSI 样式渲染 |
| 配置系统 | `github.com/spf13/viper` | v1.20.1 | 多层次配置加载 |
| UUID 生成 | `github.com/google/uuid` | v1.6.0 | Session ID 生成 |

### 代码组织结构

```
cmd/wukong/
  └── main.go                       # 入口点 (3行)

internal/cli/                        # CLI 命令包 (30 文件, ~2800 行)
  ├── root.go                        # 根命令定义 (27 子命令注册)
  ├── session.go                     # 主会话命令 + Bootstrap 引擎 (~1550 行)
  ├── server.go                      # 无头服务器模式
  ├── run.go                         # 单发/对话模式
  ├── config.go                      # config validate/show
  ├── configure.go                   # 交互式配置向导
  ├── init.go                        # 项目初始化
  ├── version.go                     # 版本信息
  ├── utils.go                       # 工具命令 (docs/stats/resume)
  ├── health.go                      # 健康检查
  ├── env.go                         # 运行环境信息
  ├── extension.go                   # MCP 扩展管理
  ├── session_mgmt.go                # 会话列表/删除
  ├── session_export.go              # 会话导出/信息
  ├── project.go                     # 项目追踪管理
  ├── bench.go / eval.go             # 基准测试 / 评估
  └── *_mgmt.go                      # 各类资源管理命令 (8 文件)
  └── tui/                           # Bubble Tea TUI 子包
      ├── model.go                   # TUI Model (状态 + 布局)
      ├── update.go                  # 事件处理 + 流式管道
      └── view.go                    # Lipgloss 渲染引擎
```

### 架构全景图

```
┌──────────────────────────────────────────────────┐
│              cmd/wukong/main.go                   │
│              cli.Execute()                        │
└─────────────────────┬────────────────────────────┘
                      │
┌─────────────────────▼────────────────────────────┐
│              internal/cli (Cobra)                 │
│  ┌──────────┐ ┌────────┐ ┌──────┐ ┌───────────┐ │
│  │ session  │ │ server │ │ run  │ │ config... │ │
│  │ (TUI)    │ │(headless)│(CLI) │ │ (27 total)│ │
│  └────┬─────┘ └───┬────┘ └──┬───┘ └───────────┘ │
│       │            │        │                     │
│       │     ┌──────┴────────┴──────┐              │
│       │     │  bootstrapSession()  │              │
│       │     │  (共享启动引擎)       │              │
│       │     └──────────┬───────────┘              │
└───────┼────────────────┼──────────────────────────┘
        │                │
┌───────▼────────────────▼──────────────────────────┐
│           internal/cli/tui (Bubble Tea)            │
│  ┌─────────────┐  ┌──────────┐  ┌──────────────┐ │
│  │ model.go    │  │update.go │  │ view.go      │ │
│  │ Model struct│  │事件分发   │  │ Lipgloss渲染 │ │
│  └─────────────┘  └──────────┘  └──────────────┘ │
└──────────────────────┬────────────────────────────┘
                       │
┌──────────────────────▼────────────────────────────┐
│           internal/agent (CoreLoop)                │
│  模型调用 → 工具执行 → 记忆管理 → 会话持久化        │
└────────────────────────────────────────────────────┘
```

---

## 2. 入口与启动流程

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

- 仅 3 行核心逻辑
- 错误通过 Cobra 内置机制格式化输出
- 退出码：0 = 成功，1 = 命令级错误

### 2.2 根命令 (root.go)

```go
func newRootCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:           "wukong",
        Short:         "Wukong - A local-first extensible AI agent platform",
        SilenceUsage:  true,
        SilenceErrors: true,
        PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
            // 全局标志：--debug / --quiet 控制日志级别
        },
    }
    // 注册 27 个子命令
    cmd.AddCommand(newSessionCmd())  // 默认交互模式
    cmd.AddCommand(newServerCmd())   // 无头服务器
    cmd.AddCommand(newRunCmd())      // 单发/对话
    // ... 24 more commands
}
```

**关键设计：SilenceUsage + SilenceErrors**
- `SilenceUsage: true` — 错误时不打印用法说明，避免日志噪音
- `SilenceErrors: true` — 错误已由 `main()` 中的 `fmt.Fprintf` 统一处理

### 2.3 全局标志

| 标志 | 类型 | 默认值 | 作用 |
|------|------|--------|------|
| `--debug` | bool | false | 启用 debug 级别日志 |
| `--quiet` | bool | false | 仅输出 warn/error 日志 |

---

## 3. CLI 命令树

Wukong 在 `root.go` 中注册了 **27 个子命令**，按功能可分为 6 大类：

### 3.1 交互会话类

| 命令 | 文件 | 行数 | 说明 |
|------|------|------|------|
| `wukong session` | session.go | ~300 | **默认模式**：启动 Bubble Tea TUI 交互会话 |
| `wukong server` | server.go | ~300 | 无头服务器模式，启动所有协议端点 |
| `wukong run` | run.go | ~360 | 单发执行（-m 管道）或多轮对话（-d） |

#### session 子命令树

```
wukong session [flags]              # 启动 TUI
  ├── list                          # 列出所有会话
  ├── delete <id> [rm]              # 删除会话
  ├── export <id> [-f markdown|json] # 导出会话
  ├── info <id>                     # 会话详情
  └── resume                        # 快速恢复最近会话
```

#### run 模式说明

| 模式 | 用法 | 说明 |
|------|------|------|
| **单发** | `wukong run -m "prompt"` | 发送单次请求，打印响应后退出 |
| **管道** | `echo "prompt" \| wukong run` | 从 stdin 读取输入 |
| **位置参数** | `wukong run explain this code` | 命令行位置参数拼接为 prompt |
| **对话** | `wukong run -d` | 多轮 REPL 对话模式，`/exit` 退出 |

**对话模式内置命令：**

| 命令 | 说明 |
|------|------|
| `/exit`, `/quit` | 退出对话 |
| `/session` | 显示当前 Session ID |
| `/clear` | ANSI 清屏 |
| `/help` | 帮助信息 |

### 3.2 配置管理类

| 命令 | 文件 | 说明 |
|------|------|------|
| `wukong config validate` | config.go | 加载并验证配置，报告错误/警告 |
| `wukong config show` | config.go | 显示合并后的有效配置 (YAML) |
| `wukong configure` | configure.go | 交互式配置向导 (5 步) |

**config validate 验证项 (12 项)：**
1. default_provider 必须设置
2. default_provider 必须存在于 providers 列表
3. Provider 必须有 model 配置
4. 云端 Provider 必须有 API Key
5. Provider type 必须有效 (7 种)
6. Planner 类型校验 (builtin/react)
7. lightweight_provider 必须存在于列表
8. Session backend 校验 (sqlite/memory/redis)
9. Memory backend 校验 (sqlite/redis)
10. Permission mode 校验 (auto/smart/manual/chat_only)
11. Workflow mode 校验 (10 种模式)
12. Artifact backend 校验 (inmemory/cos)

### 3.3 系统诊断类

| 命令 | 文件 | 说明 |
|------|------|------|
| `wukong health [--json]` | health.go | 系统健康检查，支持 JSON 输出 |
| `wukong env [--json]` | env.go | 运行环境信息，支持 JSON 输出 |
| `wukong version` | version.go | 版本、Git commit、构建日期 |
| `wukong stats` | utils.go | 系统统计仪表盘 |
| `wukong docs` | utils.go | 打开项目文档 (浏览器) |
| `wukong completion` | root.go | Shell 自动补全脚本 (bash/zsh/fish/powershell) |

### 3.4 项目管理类

| 命令 | 文件 | 说明 |
|------|------|------|
| `wukong init [dir]` | init.go | 初始化项目目录（含 config.yaml、.wukongignore 等） |
| `wukong project` | project.go | 交互式项目选择和会话恢复 |
| `wukong projects` | project.go | 列出所有追踪的项目目录 |
| `wukong bench` | bench.go | 基准测试 |

### 3.5 扩展管理类

| 命令 | 文件 | 说明 |
|------|------|------|
| `wukong extension install [url]` | extension.go | 从 deeplink URL 安装 MCP 扩展 |
| `wukong extension list` | extension.go | 列出所有已注册扩展 |
| `wukong extension enable/disable <name>` | extension.go | 启用/禁用扩展 |
| `wukong extension show <name>` | extension.go | 扩展详情 |
| `wukong extension remove <name>` | extension.go | 移除扩展 |

### 3.6 资源管理类

| 命令 | 对应文件 | 管理的子系统 |
|------|----------|-------------|
| `wukong memory` | memory_mgmt.go | 记忆管理 |
| `wukong provider` | provider_mgmt.go | Provider 管理 |
| `wukong skill` | skill_mgmt.go | 技能管理 |
| `wukong recipe` | recipe_mgmt.go | 配方管理 |
| `wukong knowledge` | knowledge_mgmt.go | 知识库管理 |
| `wukong ard` | ard_mgmt.go | ARD 资源发现 |
| `wukong evolution` | evolution_mgmt.go | 进化引擎管理 |
| `wukong cortex` | cortex_mgmt.go | CortexDB 管理 |
| `wukong todo` | todo_mgmt.go | 任务管理 |
| `wukong apps` | apps_mgmt.go | 应用管理 |
| `wukong eval` | eval.go | 评估测试 |

---

## 4. TUI 架构

### 4.1 Bubble Tea Elm 架构

TUI 采用 [Elm Architecture](https://guide.elm-lang.org/architecture/)：
- **Model** — 应用状态
- **Update** — 状态转换函数 (`Msg → Model → (Model, Cmd)`)
- **View** — 纯渲染函数 (`Model → string`)

```
         ┌─────── Msg ───────┐
         ▼                   │
    ┌─────────┐         ┌─────────┐
    │ Update  │────────▶│  Model  │
    └─────────┘         └─────────┘
         │                   │
         │ Cmd               │
         ▼                   ▼
    ┌─────────┐         ┌─────────┐
    │ Runtime │         │  View   │
    └─────────┘         └─────────┘
```

### 4.2 Model 结构 (model.go)

```go
type Model struct {
    viewport viewport.Model    // 对话滚动视口
    textarea textarea.Model    // 输入区域

    // 会话状态
    userID    string            // 用户标识
    sessionID string            // 会话ID (UUID)
    messages  []chatEntry       // 对话历史 (最多 500 条)
    status    string            // 状态栏文本

    // 工具调用显示
    toolCalls []toolCallEntry   // 活跃的工具调用列表

    // Agent 循环
    loop *agent.CoreLoop        // 核心 Agent 循环
    cfg  *config.WukongConfig  // 运行时配置

    // 流式状态
    streaming     bool          // 是否正在流式输出
    currentStream string        // 当前累积的流式内容
    streamCancel  func()        // 取消函数 (Ctrl+C 中断)
    streamCh      <-chan streamEvent  // 事件通道 (goroutine → TUI)

    // 布局
    width  int
    height int
    ready  bool
}
```

### 4.3 三区布局 (view.go)

```
┌─────────────────────────────────────────────────┐
│ ⚡ Wukong | abc12345 | deepseek/deepseek-chat   │ ← StatusBar
│                                      Ready      │
├─────────────────────────────────────────────────┤
│ Wukong: How can I help you today?              │ ← Viewport
│                                                 │   (可滚动对话区)
│ You: Write a sorting function                   │
│                                                 │
│ Wukong: Here's a quicksort implementation...    │
│                                                 │
├─────────────────────────────────────────────────┤
│ ◉ read_file  ● write_file  ○ search_code       │ ← ToolCall Bar
├─────────────────────────────────────────────────┤
│ Type your message... (Ctrl+D to send)           │ ← Textarea
│                                                 │   (输入区)
└─────────────────────────────────────────────────┘
```

**布局计算 (handleResize)：**
- Header (StatusBar): 3 行
- Footer (ToolCall + Textarea): 6 行
- Viewport: `(Height - HeaderHeight - FooterHeight)` × `(Width - 4)`

### 4.4 配色方案

| 元素 | 颜色 | 用途 |
|------|------|------|
| `colorUser` | `#87FF5F` (Green) | 用户消息前缀 |
| `colorAssistant` | `#FF87FF` (Pink) | Wukong 回复前缀 |
| `colorStatus` | `#5F5FFF` (Blue) | 状态栏背景 |
| `colorRunning` | `#FFFF5F` (Yellow) | 运行中的工具调用 |
| `colorDone` | `#00D75F` (Green) | 已完成的工具调用 |
| `colorError` | `#FF0000` (Red) | 错误的工具调用 |
| `colorDim` | `#585858` (Gray) | 次要信息 |

### 4.5 内置 TUI 命令

TUI 模式下，以 `/` 开头的输入被解释为内置命令：

| 命令 | 说明 |
|------|------|
| `/exit`, `/quit` | 退出 TUI |
| `/exts` | 列出已加载的扩展 |
| `/new` | 生成新 Session ID，清空当前对话 |
| `/clear` | 清空视口内容 |
| `/model` | 显示当前 Provider/Model |
| `/model <name>` | 动态切换模型 |
| `/help` | 显示帮助信息 |

### 4.6 快捷键

| 快捷键 | 上下文 | 行为 |
|--------|--------|------|
| `Ctrl+D` | 输入区有内容 | 发送消息 |
| `Ctrl+C` | 流式输出中 | 取消当前请求 |
| `Ctrl+C` | 空闲状态 | 退出 TUI |

---

## 5. Bootstrap 启动序列

`bootstrapSession()` 是所有交互模式（session、server、run）共享的启动引擎，位于 `session.go`，约 **1250 行**，按以下顺序初始化子系统：

### 5.1 启动时序图

```
Phase 1: 配置加载           (~50ms)
├── config.NewLoader(configPath)
├── loader.Load() → WukongConfig
└── validateConfig(warnings)

Phase 2: 基础设施           (~100ms)
├── telemetry.NewManager    (OpenTelemetry 分布式追踪)
├── builtin.RegisterBuiltins (注册内置扩展)
└── applyOverrides          (CLI 标志覆盖配置)

Phase 3: 模型与数据库       (~200ms)
├── provider.NewFactory      (LLM 工厂)
├── util.NewMultiPool        (SQLite 多连接池)
└── wksession.NewSessionService (会话持久化)

Phase 4: 记忆与安全         (~100ms)
├── memory.NewMemoryManager  (tRPC Memory)
├── memoryMgr.SmartCleanup   (启动时清理旧记忆)
└── security.NewGuard        (安全护栏)

Phase 5: 扩展与发现         (~500ms)
├── extension.NewManager     (MCP 扩展管理)
├── extMgr.Initialize        (启动所有扩展)
├── ard.NewToolSet           (ARD 资源发现)
├── acpMCPBridge.Start       (ACP MCP 桥接)
└── summon.NewSummonManager  (子代理委托)

Phase 6: 存储引擎           (~200ms)
├── cortex.NewStore          (CortexDB 向量数据库)
├── memoryFlowSvc            (MemoryFlow 对话转录)
├── graphFlowSvc             (GraphFlow 知识图谱)
├── todo.NewStore            (任务管理)
└── knowledge.NewManager     (RAG 知识库)

Phase 7: Agent 组装         (~100ms)
├── agent.NewCoreLoop        (核心代理循环)
│   ├── 合并所有 ToolSets + FunctionTools
│   ├── code_discover_tools (JS 代码发现工具)
│   └── revisionModel (上下文摘要)
└── project.NewManager       (项目追踪)

Phase 8: 协议服务器         (~100ms, goroutine)
├── summon.NewA2AServer      → :9090
├── server.NewAGUIServer     → :8080
├── server.NewACPServer      → :9091
├── ANP Protocol Stack       → :9092
│   ├── ard.NewDIDManager    (W3C DID 身份)
│   ├── summon.NewMetaProtocol (能力协商)
│   └── summon.NewE2EEMessenger (端到端加密)
└── sandbox.Probe            (沙箱检测)
```

### 5.2 BootstrapState 资源容器

```go
type BootstrapState struct {
    A2AServer     *summon.A2AServer     // A2A 协议服务器
    AGUIServer    *server.AGUIServer    // AG-UI SSE 服务器
    ACPServer     *server.ACPServer     // ACP 协议服务器
    ACPMCPBridge  *extension.ACPMCPBridge // ACP MCP 桥接
    ARDRegistry   *ard.RegistryServer   // ARD 注册服务器
    ANPServer     *http.Server          // ANP HTTP 服务器
    ANPMeta       *summon.MetaProtocol  // ANP 元协议引擎
    ANPMessenger  *summon.E2EEMessenger // ANP E2EE 消息
    KnowledgeMgr  *knowledge.Manager    // 知识库管理器
    ProjectMgr    *project.Manager      // 项目追踪管理器
}
```

### 5.3 优雅关闭

所有模式在退出时均执行统一关闭流程：

```
1. 接收信号 (SIGINT/SIGTERM)
2. 关闭 A2A Server
3. 关闭 AG-UI Server
4. 关闭 ACP Server
5. 关闭 ACP MCP Bridge
6. 关闭 ARD Registry
7. 关闭 ANP Server
8. 关闭 Knowledge Manager
9. loop.Close() 触发内部清理链：
   ├── 记忆 workers 停止
   ├── Runner 停止
   ├── 会话写入刷新
   ├── Telemetry 关闭
   └── 数据库连接池关闭
```

---

## 6. 流式传输与事件管道

### 6.1 流式架构

TUI 的流式传输通过 **Goroutine + Channel** 桥接模式实现：

```
┌─────────────────────────────────────────────────────┐
│                TUI Update Loop                       │
│  (主 goroutine, Bubble Tea Runtime)                  │
│                                                     │
│  sendMessage() ──▶ streamCh (chan streamEvent, 64)  │
│       │                    ▲                        │
│       │                    │                        │
│       ▼                    │                        │
│  readStreamEvent() ←───────┘                        │
│       │                                             │
│       ├── streamingDeltaMsg ──▶ 增量渲染            │
│       ├── toolCallStartMsg ──▶ 工具状态更新         │
│       ├── toolCallResultMsg ─▶ 工具完成标记         │
│       ├── streamingErrorMsg ─▶ 错误显示             │
│       └── streamEndMsg ──────▶ 流结束处理           │
│                                                     │
├─────────────────────────────────────────────────────┤
│                Agent Goroutine                       │
│  (独立 goroutine, context 控制生命周期)              │
│                                                     │
│  loop.Run(ctx, userID, sessionID, msg)              │
│       │                                             │
│       ├── Delta.Content ──▶ streamCh ← streamEvent  │
│       ├── ToolCalls ──────▶ streamCh ← streamEvent  │
│       ├── Error ──────────▶ streamCh ← streamEvent  │
│       └── IsRunnerCompletion ──▶ streamCh           │
│                              ← streamEvent(IsEnd)   │
│                                                     │
│  defer close(streamCh)                              │
│  defer cancel()  (context.CancelFunc)               │
└─────────────────────────────────────────────────────┘
```

### 6.2 事件类型

```go
type streamingDeltaMsg string           // 增量内容
type toolCallStartMsg struct {          // 工具调用开始
    Name string
    Args string
}
type toolCallResultMsg struct {         // 工具调用结果
    Result string
}
type streamingErrorMsg string           // 流式错误
type streamEndMsg struct {              // 流结束
    Content string                      // 完整内容
}
```

### 6.3 可中断流式传输

`Ctrl+C` 在流式输出期间取消请求但不退出 TUI：

```go
// update.go — Ctrl+C handling
case tea.KeyCtrlC:
    if m.streaming {
        if m.streamCancel != nil {
            m.streamCancel()  // 取消 agent goroutine 的 context
        }
        m.streaming = false
        m.status = "Cancelled by user"
        // 保留已生成的部分内容
    }
```

### 6.4 内容不丢失机制

`streamEndMsg` 处理中的 fallback 逻辑确保增量输出不丢失：

```go
case streamEndMsg:
    finalContent := msg.Content           // 优先使用结构化 Content
    if finalContent == "" && m.currentStream != "" {
        finalContent = m.currentStream    // 回退到累计的增量内容
    }
```

### 6.5 错误友好化

`friendlyError()` 将原始错误信息映射为用户可读的中文提示：

| 原始错误 | 友好提示 |
|----------|----------|
| `context deadline exceeded` | Request timed out — the model took too long to respond |
| `connection refused` | Cannot connect to model — check network/provider |
| `401` / `unauthorized` | Authentication failed — check API key or credentials |
| `429` / `rate limit` | Rate limited by provider — wait and retry |
| `500` / `502` / `503` | Model service unavailable — provider may be experiencing issues |
| `canceled` | Request cancelled by user |

---

## 7. 输入解析策略

### 7.1 run 命令输入优先级

`resolveInput()` 按以下优先级确定 prompt 文本：

```
1. --message / -m 标志         (最高优先级)
2. 位置参数拼接                 (args...)
3. stdin 管道输入               (非 TTY 模式)
4. 空字符串                     (触发 --dialogue 模式或报错)
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
        data, _ := io.ReadAll(os.Stdin)
        return strings.TrimSpace(string(data))
    }
    return ""
}
```

### 7.2 用户标识解析

`resolveUserID()` 跨平台处理用户识别：

| 平台 | 优先级 | 来源 |
|------|--------|------|
| Unix | 1 | `$USER` 环境变量 |
| Windows | 2 | `$USERDOMAIN\$USERNAME` |
| 通用 | 3 | `os.Hostname()` |
| 兜底 | 4 | `"default"` |

---

## 8. 错误处理架构

### 8.1 三层错误处理

```
Layer 1: Cobra Command
   └── RunE returns error → Cobra formats + prints

Layer 2: Bootstrap
   └── bootstrapSession() returns (config, loop, state, error)
       └── 每个子系统初始化失败都有独立 warn 日志

Layer 3: Agent Interaction
   └── loop.Run() returns (events, error)
       └── TUI: friendlyError() 转换 → 显示在对话区
       └── CLI: fmt.Fprintln(os.Stderr)
```

### 8.2 配置警告系统

`validateConfig()` 在启动时输出非致命警告：

| 场景 | 警告信息 |
|------|----------|
| default_provider 为空 | "no default_provider configured" |
| provider 不在列表 | "not found in providers list" |
| model 未配置 | "no model configured" |
| API Key 缺失 | "no API key configured" |
| planner 与 provider 不匹配 | "consider using 'react' planner" |
| 未知 planner | "unknown planner" |
| 本地模型自动提取 | "this may be slow" |

---

## 9. 关键设计决策

### 9.1 为什么使用 Cobra + Bubble Tea 双框架？

| 方面 | Cobra | Bubble Tea |
|------|-------|-------------|
| 用途 | CLI 命令路由、帮助生成 | 终端 UI 交互 |
| 优势 | 成熟的 CLI 生态、自动补全 | Elm 架构、声明式渲染 |
| 分离原因 | 管理命令（config/health/env）无需 TUI | 交互会话需要实时渲染 |

### 9.2 Quick Pre-load 模式

`session.go` 中的 `quickLoadConfig()` 在完整 bootstrap 之前快速加载并显示 Provider/Model 信息，让用户即刻获得反馈，无需等待所有子系统启动。

### 9.3 Session ID 设计

- 使用 UUID v4 (Google UUID)
- TUI 显示时截取前 8 位
- 支持 `--session-id` 恢复历史会话
- `/new` 命令生成新 Session ID，旧会话数据保留在数据库中

### 9.4 消息上限

- TUI 内存中最多保留 **500 条** chatEntry
- 超限时从最旧消息开始裁剪
- 服务端会话存储通过 `EventLimit` 配置控制

### 9.5 ACP MCP Bridge 注册时机

ACP MCP Bridge 在 extension manager 初始化后立即启动，确保 ACP Provider 能够发现 Wukong 的工具列表：

```go
factory.SetACPMCPAddr(acpMCPBridge.ACPMCPAddr())
```

### 9.6 工具列表一次性注入

为避免每次 LLM 调用时重复计算工具列表，所有工具在 bootstrap 阶段组装完毕后一次性注入 `codeExecutor.SetToolsForDiscovery()`，供 JS 代码沙箱中的 `code_discover_tools` 使用。

### 9.7 协议服务器统一生命周期

所有协议服务器（A2A/AG-UI/ACP/ANP）共享统一的 `BootstrapState` 和关闭流程，确保无论从 TUI 还是 server 模式退出，资源都能正确释放。

---

> **版本**: v0.2.0 | **最后更新**: 2026-07-01 | **文件数**: 30 CLI 文件 + 3 TUI 文件 | **总行数**: ~4500 行
