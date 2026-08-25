# Wukong 开发者指南

> 面向贡献者与二次开发者的深度技术指南。基于源码分析，系统讲解开发环境、项目结构、依赖规则、扩展机制（工具集/Recipe/Skill）与构建发布流程。

---

## 目录

1. [开发环境与技术栈](#1-开发环境与技术栈)
2. [项目结构](#2-项目结构)
3. [依赖规则与架构约束](#3-依赖规则与架构约束)
4. [核心架构概念](#4-核心架构概念)
5. [添加内置扩展（ToolSet）](#5-添加内置扩展toolset)
6. [添加 Recipe](#6-添加-recipe)
7. [添加 Skill](#7-添加-skill)
8. [添加 Provider](#8-添加-provider)
9. [添加 CLI 命令](#9-添加-cli-命令)
10. [添加编排模式](#10-添加编排模式)
11. [测试指南](#11-测试指南)
12. [调试技巧](#12-调试技巧)
13. [配置系统](#13-配置系统)
14. [构建与发布](#14-构建与发布)

---

## 1. 开发环境与技术栈

### 1.1 前置要求

| 依赖 | 最低版本 | 说明 |
|------|----------|------|
| **Go** | **1.26** | 编译与测试必需（见 [go.mod](../go.mod) 第 3 行） |
| Git | 任意 | 版本控制 |
| golangci-lint | latest | 代码静态检查（`make lint`） |
| Task（可选） | v3 | 任务运行器，[Taskfile.yaml](../Taskfile.yaml) |
| Chrome / Chromium | 任意 | **可选**，仅 `computer_controller` 浏览器功能需要 |

### 1.2 模块信息

```
module github.com/km269/wukong
go 1.26
```

[go.mod](../go.mod) 包含 **29 个直接依赖**，核心框架为：

| 框架 | 版本 | 用途 |
|------|------|------|
| `trpc.group/trpc-go/trpc-agent-go` | v1.10.0 | Agent 循环、Runner、LLMAgent、Planner、Tool |
| `trpc.group/trpc-go/trpc-mcp-go` | v0.0.16 | MCP 协议客户端 / Broker |
| `trpc.group/trpc-go/trpc-a2a-go` | v0.2.5（间接） | Agent-to-Agent 互操作 |
| `github.com/spf13/cobra` | v1.9.1 | CLI 命令框架 |
| `github.com/charmbracelet/bubbletea` | v1.3.10 | TUI 框架 |
| `modernc.org/sqlite` | v1.38.2 | 纯 Go SQLite（**零 CGO**） |
| `github.com/liliang-cn/cortexdb/v2` | v2.25.0 | 向量数据库 |
| `go.opentelemetry.io/otel` | v1.43.0 | 分布式追踪 |
| `github.com/fsnotify/fsnotify` | v1.8.0 | Recipe 热重载文件监听 |

### 1.3 关键设计原则

- **纯 Go，零 CGO**：使用 `modernc.org/sqlite` 替代 `mattn/go-sqlite3`，CI 以 `CGO_ENABLED=0` 构建。
- **Composition Root 模式**：`internal/cli/` 是唯一的依赖组装点，其他包只定义和接收依赖，不自行创建。
- **接口解耦**：LLM 抽象为 `model.Model`，工具集抽象为 `tool.ToolSet`，Agent 抽象为 `agent.Agent`，均来自 tRPC-Agent-Go。
- **事件流为核心**：Agent 运行结果通过 `<-chan *event.Event` 流式返回。

### 1.4 首次构建

```bash
git clone <repo-url> wukong && cd wukong
go mod download
go build -o wukong ./cmd/wukong
./wukong version   # 验证：Version: 0.3.1
```

---

## 2. 项目结构

### 2.1 目录概览

```
wukong/
├── cmd/                              # 程序入口（3 个）
│   ├── wukong/                       #   主程序（main.go）
│   ├── zim-check/                    #   ZIM 归档校验工具
│   └── zim-ls/                       #   ZIM 归档列出工具
│
├── internal/                         # 私有业务逻辑（33 个包）
│   ├── agent/                        # ★ Agent 核心循环与编排
│   ├── apps/                         # 应用克隆/打包/MCP 应用
│   ├── ard/                          # Agent 注册发现
│   ├── artifact/                     # 文件产物服务
│   ├── browser/                      # 浏览器后端与反检测
│   ├── cli/                          # ★ CLI + TUI（Composition Root）
│   │   └── tui/                      #   Bubble Tea 终端 UI
│   ├── codemode/                     # JS 代码执行沙箱
│   ├── config/                       # 配置加载、类型定义、校验
│   ├── cors/                         # CORS 中间件
│   ├── cortex/                       # 向量/图谱记忆（CortexDB）
│   ├── errsignal/                    # 错误信号处理
│   ├── eval/                         # 评测框架
│   ├── evolution/                    # 技能自演化引擎
│   ├── extension/                    # ★ 扩展/工具管理
│   │   └── builtin/                  #   12+ 内置工具集
│   ├── gateway/                      # 外部网关（飞书等）
│   ├── health/                       # 健康检查
│   ├── knowledge/                    # RAG 知识管理
│   ├── memory/                       # 持久化记忆
│   ├── observability/                # 可观测性（Langfuse）
│   ├── okf/                          # OKF 打包
│   ├── project/                      # 项目追踪
│   ├── provider/                     # ★ LLM Provider 工厂
│   ├── recall/                       # 对话召回
│   ├── search/                       # 搜索引擎（chunking/metrics/tune）
│   ├── security/                     # ★ 安全 Guard
│   ├── server/                       # ACP/A2A/AGUI 服务端
│   ├── session/                      # 会话存储
│   ├── skill/                        # 技能管理
│   ├── summon/                       # 远程 Agent 召唤
│   ├── telemetry/                    # OpenTelemetry 追踪
│   ├── todo/                         # Todo 工具
│   ├── topofmind/                    # Top-of-Mind 持久指令
│   └── util/                         # 通用工具（Logger/DB/指针/version）
│
├── pkg/                              # 可复用公共库（4 个包）
│   ├── httpclient/                   #   HTTP 客户端（DNS 缓存 + 限流）
│   ├── logutil/                      #   日志工具
│   ├── sandbox/                      #   跨平台文件沙箱
│   └── zim/                          #   ZIM 归档读写
│
├── docs/                             # 文档
├── .github/workflows/                # CI/CD（ci.yml, release.yml）
├── Dockerfile                        # Docker 构建
├── .goreleaser.yaml                  # GoReleaser 配置
├── Makefile                          # 构建 target
├── Taskfile.yaml                     # Task 运行器（镜像 Makefile）
└── go.mod                            # 模块定义
```

### 2.2 核心模块职责

| 模块 | 职责 | 关键文件 |
|------|------|----------|
| `agent` | CoreLoop 四阶段循环、上下文管理、10 种编排模式 | loop.go, workflow.go, recipe.go |
| `cli` | CLI 命令 + **Composition Root**（bootstrapSession ~1400 行） | session.go, root.go |
| `extension` | 工具集生命周期、MCP 外部进程、ACP-MCP 桥接 | manager.go, factory.go |
| `provider` | LLM 模型实例工厂（7 种 OpenAI 兼容 + ACP） | factory.go |
| `security` | 工具权限、命令校验、SSRF 防护、提示注入检测 | guard.go |
| `config` | 配置类型、加载、12 项校验 | config.go, validate.go |
| `cortex` | 向量存储、MemoryFlow 转录、GraphFlow 知识图谱 | store.go, memoryflow.go |
| `recall` | 对话召回（SQLite FTS5 或 CortexDB 向量增强） | store.go |

> **⚠️ 注意**：项目中**不存在** `internal/tools/`、`configs/`、`scripts/`、`pkg/cli/`、`pkg/utils/` 目录。工具逻辑在 `internal/extension/builtin/`，配置类型在 `internal/config/`，CLI 在 `internal/cli/`。

---

## 3. 依赖规则与架构约束

### 3.1 Composition Root

`internal/cli/` 是**唯一的 Composition Root**。[session.go](../internal/cli/session.go) 的 `bootstrapSession()` 函数（~1400 行）按顺序创建并串联所有组件：

```
配置加载 → 遥测 → 数据库 → 记忆 → 会话 → Provider 工厂
→ 安全 Guard → 扩展 Manager → CoreLoop → 协议服务器
```

其他 `internal/` 包**只定义和接收依赖**，不自行创建跨模块依赖。

### 3.2 依赖方向图

```
                    ┌─────────┐
                    │   cli   │ ← Composition Root（组装一切）
                    └────┬────┘
          ┌──────┬───────┼───────┬──────┐
          ▼      ▼       ▼       ▼      ▼
      ┌──────┐ ┌─────┐ ┌──────┐ ┌────┐ ┌────────┐
      │agent │ │server│ │gateway│ │apps│ │extension│
      └──┬───┘ └──┬──┘ └──┬───┘ └────┘ └────────┘
         │        │       │
    ┌────┼────┐   │       │
    ▼    ▼    ▼   ▼       ▼
┌──────┐┌────┐┌──────┐┌────────┐
│config││provider││security││cortex│
└──┬───┘└────┘└──────┘└───┬──┘
   │                       │
   ▼                       ▼
┌──────────────────────────────┐
│ gateway + server（嵌入类型）  │
└──────────────────────────────┘
```

### 3.3 关键依赖约束

| 规则 | 说明 |
|------|------|
| **cli/ 是唯一组装者** | 其他包不 import cli/ |
| **agent/ 依赖** | imports config/provider/security/cortex/recall |
| **gateway → agent** | 通过 `AgentRunner` 接口调用，不直接引用 CoreLoop |
| **server → security** | 通过 `ToolGuardCheck` 回调函数注入，不直接 import security |
| **config imports gateway + server** | config 包嵌入 gateway/server 的配置类型（embedded types） |
| **cortex + recall 共享数据** | 共享 `chat_recall` 表和同一个 SQLite 连接池 |
| **provider 无循环依赖** | 仅依赖 config + model 接口 |

### 3.4 接口解耦

Wukong **不定义自己的** Agent / Model / Tool 接口，复用 tRPC-Agent-Go 框架：

| 框架接口 | 签名 | Wukong 实现 |
|----------|------|-------------|
| `agent.Agent` | LLMAgent / ChainAgent | workflow.go 的编排 Agent |
| `model.Model` | `Info()` + `GenerateContent()` | provider.Factory 创建 |
| `tool.ToolSet` | `Tools(ctx)` + `Close()` | extension/builtin 的 12+ 工具集 |
| `runner.Runner` | 驱动 Agent 执行 | CoreLoop 内部持有 |
| `session.Service` | 会话状态持久化 | session.Store |
| `memory.Service` | 记忆读写 | memory.Manager |

---

## 4. 核心架构概念

### 4.1 CoreLoop 四阶段

CoreLoop 定义在 [loop.go](../internal/agent/loop.go)，每次 `Run` 调用经历：

```
┌─────────────────────────────────────────────────────────┐
│              CoreLoop.Run(ctx, userID, sessionID, msg)    │
└─────────────────────────────────────────────────────────┘
        │
        ▼
┌───────────────────┐    ┌───────────────────┐    ┌──────────────┐
│ 1. 上下文准备      │───▶│ 2. 记忆注入       │───▶│ 3. Runner    │
│ PrepareContext     │    │ MemoryFlow 唤醒   │    │ 执行 Agent   │
│ (ContextManager)   │    │ Recall 检索       │    │ 循环         │
└───────────────────┘    │ 持久记忆注入(去重) │    └──────┬───────┘
                          └───────────────────┘           │
                                                          ▼
┌──────────────────────────────────────────────────────────────┐
│ 4. 事件流返回 <-chan *event.Event                              │
│ （RunStream 消费事件后执行副作用：写入 Recall/Cortex/Memory）   │
└──────────────────────────────────────────────────────────────┘
```

### 4.2 Run 与 RunStream

```go
// 返回原始事件流（供 TUI/API 自行消费）
func (l *CoreLoop) Run(
    ctx context.Context, userID string, sessionID string,
    message model.Message,
) (<-chan *event.Event, error)

// 消费事件流，通过回调推送，返回最终文本（含记忆副作用）
func (l *CoreLoop) RunStream(
    ctx context.Context, userID string, sessionID string,
    message model.Message,
    onEvent func(evt *event.Event) error,
) (string, error)
```

> **注意**：`Run` 接收 **4 个参数**（ctx, userID, sessionID, message），不是 `Run(ctx, input)`。

### 4.3 依赖注入

CoreLoop 通过 `CoreLoopConfig` 结构体注入全部依赖：

```go
type CoreLoopConfig struct {
    Config              *config.WukongConfig
    Factory             *provider.Factory
    SessionService      session.Service
    MemoryService       memory.Service
    ArtifactService     artifact.Service
    ToolSets            []tool.ToolSet
    FunctionTools       []tool.Tool
    SecurityGuard       *security.Guard
    RecallStore         *recall.Store
    CortexStore         *cortex.CortexStore       // 可选
    RevisionModel       provider.RevisionModel
    MemoryFlowService   *cortex.MemoryFlowService
    GraphFlowService    *cortex.GraphFlowService  // 可选
    TopOfMindInstructions string
    TelemetryShutdown   func(context.Context) error
    MemoryClose         func() error
    EvolutionClose      func() error
    DBPoolClose         func() error
    WorkingDir          string
    SessionID           string
    UserID              string
}
```

---

## 5. 添加内置扩展（ToolSet）

Wukong 的扩展系统**没有自定义 `Extension` 接口**。所有扩展（无论内置还是外部 MCP）都实现 tRPC-Agent-Go 的 `tool.ToolSet` 接口。

### 5.1 五步流程

#### 步骤 1：创建工具集文件

在 `internal/extension/builtin/` 下新建文件：

```go
// internal/extension/builtin/mytool.go
package builtin

import (
    "context"

    "github.com/km269/wukong/internal/config"
    "trpc.group/trpc-go/trpc-agent-go/tool"
    "trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// MyToolSet 实现 tool.ToolSet 接口
type MyToolSet struct {
    tools []tool.Tool
    cfg   *config.WukongConfig
}

// myAction 是工具的实际执行函数
func (ts *MyToolSet) myAction(ctx context.Context, args *struct {
    Input string `json:"input" jsonschema:"description=输入文本"`
}) (string, error) {
    return "result: " + args.Input, nil
}

// NewMyToolSet 创建工具集。需要 config 的工具集接收 cfg 参数。
func NewMyToolSet(cfg *config.WukongConfig) (tool.ToolSet, error) {
    ts := &MyToolSet{cfg: cfg}
    ts.tools = []tool.Tool{
        function.NewFunctionTool(
            ts.myAction,
            function.WithName("my_action"),
            function.WithDescription("示例工具：处理输入文本"),
        ),
    }
    return ts, nil
}

func (ts *MyToolSet) Tools(_ context.Context) []tool.Tool { return ts.tools }
func (ts *MyToolSet) Close() error                         { return nil }
```

> 参考 [developer.go](../internal/extension/builtin/developer.go)，该文件用 `function.NewFunctionTool` 注册了 6 个开发工具。

#### 步骤 2：在 factory.go 注册

在 [factory.go](../internal/extension/factory.go) 的 `CreateBuiltinToolSet` switch 中添加 case：

```go
func CreateBuiltinToolSet(name string, cfg *config.WukongConfig) (tool.ToolSet, error) {
    switch name {
    // ... 已有 case
    case "my_tool":
        return builtin.NewMyToolSet(cfg)
    default:
        return nil, fmt.Errorf("unknown builtin extension: %s", name)
    }
}
```

#### 步骤 3：在 registry.go 注册

在 [registry.go](../internal/extension/builtin/registry.go) 的 `RegisterBuiltins` 中添加：

```go
func RegisterBuiltins(cfg *config.WukongConfig) {
    builtins := []config.ExtensionConfig{
        // ... 已有 12 项
        {Name: "my_tool", Type: "builtin", Enabled: true},
    }
    // ...
}
```

#### 步骤 4：运行时依赖注入（如需要）

**需要运行时依赖的工具集**（memory、cortex、browser、apps、code_mode、top_of_mind）在 factory 中返回 `nil`，由 `bootstrapSession()` 在启动时注入：

```go
// factory.go — 这些工具集返回 nil
case "agent_tools", "apps", "code_mode", "top_of_mind":
    // Created in bootstrapSession with runtime dependencies.
    return nil, nil
```

对应地，在 [session.go](../internal/cli/session.go) 的 `bootstrapSession()` 中创建并注入：

```go
// session.go bootstrapSession() 片段
if wukongCfg.Apps.Enabled {
    appsTS := builtin.NewAppsToolSet(appsManager, cfg)
    extMgr.AddToolSet("apps", appsTS)
}
```

#### 步骤 5：验证

```bash
./wukong session
# TUI 启动后输入 /exts 查看是否包含 my_tool
```

### 5.2 当前内置工具集

[registry.go](../internal/extension/builtin/registry.go) 注册了 **12 个**内置扩展：

| 名称 | 工厂返回 | 需运行时注入 | 说明 |
|------|----------|-------------|------|
| `developer` | ✓ | — | 文件操作、命令执行、代码搜索 |
| `computer_controller` | ✓ | — | 浏览器控制、Web 抓取 |
| `memory` | ✓ | ✓（memory service） | 记忆读写 |
| `auto_visualiser` | ✓ | — | 图表/表格生成 |
| `tutorial` | ✓ | — | 交互式教程 |
| `top_of_mind` | nil | ✓ | 持久指令注入 |
| `code_mode` | nil | ✓（executor） | JS 代码执行沙箱 |
| `apps` | nil | ✓（manager） | HTML 应用管理 |
| `web` | ✓ | — | Web 搜索与抓取 |
| `agent_tools` | nil | ✓ | Agent 内部工具 |
| `ard` | ✓ | — | ARD 资源发现 |
| `cortex` | ✓ | ✓（cortex store） | 向量存储查询 |

### 5.3 外部 MCP 进程（无需改源码）

在 `config.yaml` 中配置即可接入第三方 MCP Server：

```yaml
extensions:
  - name: github-server
    type: external
    transport: stdio          # 支持: stdio / sse / http
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "ghp_xxx"
    timeout: 30s
```

---

## 6. 添加 Recipe

Recipe 是预定义的子 Agent 工作流模板，支持参数化、重试、超时和热重载。

### 6.1 创建 Recipe 文件

在 `.wukong/recipes/` 下创建 YAML 文件：

```yaml
# .wukong/recipes/code-reviewer.yaml
name: code-reviewer
description: 审查代码变更并提供改进建议
instruction: |
  你是一位资深代码审查专家。审查给定的代码变更，
  关注：正确性、安全性、性能、可读性。
  以结构化的方式输出审查结果。

prompt: |
  请审查以下代码变更：
  {{.diff}}

parameters:
  - name: diff
    type: string
    description: 代码差异内容
    required: true

tools:
  - read_file
  - search_code

response:
  format: json
  schema:
    severity: string    # critical / warning / suggestion
    line: integer
    comment: string

retry:
  max_attempts: 3
  backoff: exponential

extends: base-reviewer   # 继承另一个 recipe 的字段

model: deepseek-chat     # 覆盖默认模型

timeout: 120s            # 最大执行时间
```

### 6.2 RecipeConfig 字段

定义在 [recipe.go](../internal/agent/recipe.go) 的 `RecipeConfig` 结构体：

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 唯一标识符 |
| `instruction` | string | 子 Agent 的系统提示词 |
| `prompt` | string | 参数化任务模板（Go template 语法） |
| `parameters` | []RecipeParameter | 动态输入参数定义 |
| `response` | *RecipeResponseConfig | 输出格式约束（JSON schema） |
| `retry` | *RecipeRetryConfig | 重试策略（指数退避） |
| `extends` | string | 继承另一个 Recipe 的字段 |
| `model` | string | 覆盖默认模型 |
| `tools` | []string | 授予子 Agent 的工具列表 |
| `timeout` | string | 最大执行时间 |

### 6.3 自动加载与热重载

Recipe 由 [recipe.go](../internal/agent/recipe.go) 的 `RecipeToolSet` 管理：

- **自动发现**：启动时扫描 `.wukong/recipes/*.yaml`，注册为可调用的 Agent 工具
- **热重载**：通过 [recipe_advance.go](../internal/agent/recipe_advance.go) 的 `hotReloader` 实现，使用 `fsnotify.Watcher` 监听目录变化
- **事件监听**：Create / Write / Remove / Rename 事件触发自动重载

```go
// recipe_advance.go — hotReloader 核心逻辑
type hotReloader struct {
    watcher  *fsnotify.Watcher
    onReload func()
}

func (hr *hotReloader) watchLoop() {
    for {
        select {
        case event, ok := <-hr.watcher.Events:
            if event.Has(fsnotify.Create) ||
                event.Has(fsnotify.Write) ||
                event.Has(fsnotify.Remove) ||
                event.Has(fsnotify.Rename) {
                hr.onReload()  // 触发重新加载
            }
        case <-hr.watcher.Errors:
            // ...
        }
    }
}
```

---

## 7. 添加 Skill

Skill 是基于 `SKILL.md` 文件的 Agent 能力声明，由 `skill.Manager` 加载并转换为 Summon Delegate（可召唤的子 Agent）。

### 7.1 创建 Skill 文件

在 `.wukong/skills/<name>/` 下创建 `SKILL.md`：

```markdown
---
type: skill
name: sql-optimizer
description: 优化 SQL 查询性能
tools:
  - read_file
  - write_file
model: deepseek-chat
---

# SQL 优化专家

你是一位数据库性能优化专家。当用户请求优化 SQL 时：

1. 分析查询计划
2. 识别性能瓶颈
3. 建议索引策略
4. 提供优化后的 SQL

## 约束
- 不修改生产环境数据库
- 所有建议需附带预期收益分析
```

### 7.2 SKILL.md 格式

- **YAML Frontmatter**（`---` 包裹）：`type: skill` + 元数据
- **Markdown Body**：Agent 指令正文

### 7.3 加载机制

定义在 [manager.go](../internal/skill/manager.go)：

```go
// SkillsDir 返回技能目录，默认 .wukong/skills
func SkillsDir() string { return ".wukong/skills" }

// Initialize 扫描目录中的所有 SKILL.md 文件
func (m *Manager) Initialize() error {
    repo, err := agentskill.NewFSRepository(skillsDir)
    // ...
}
```

- 使用 tRPC-Agent-Go 的 `agentskill.FSRepository` 扫描目录
- 每个 SKILL.md 成为一个 **Summon Delegate**（可被主 Agent 召唤的子 Agent）
- `Refresh()` 方法支持运行时重载（用于 Evolution 引擎技能进化后刷新）

### 7.4 技能进化

当 `evolution.enabled: true` 时，Evolution 引擎会分析技能执行轨迹，自动生成改进补丁并应用到 SKILL.md 文件。详见 [evolution](../internal/evolution/) 包。

---

## 8. 添加 Provider

### 8.1 大多数情况：仅改配置

7 种 OpenAI 兼容的 provider type 只需在 `config.yaml` 配置：

```yaml
providers:
  - name: my-local-llm
    type: openai              # openai/anthropic/google/deepseek/ollama/lmstudio/vllm
    base_url: "http://localhost:11434/v1"
    api_key: ""
    model: "qwen2.5:32b"
    context_window: 32768

default_provider: my-local-llm
```

| Provider Type | 默认 BaseURL | 工厂方法 |
|---------------|-------------|----------|
| openai | `https://api.openai.com/v1` | `createOpenAI` |
| anthropic | `https://api.anthropic.com/v1` | `createOpenAI` |
| google | `https://generativelanguage.googleapis.com/v1beta/openai` | `createOpenAI` |
| deepseek | `https://api.deepseek.com/v1` | `createOpenAI` |
| ollama | `http://localhost:11434/v1` | `createOpenAI` |
| lmstudio | `http://localhost:1234/v1` | `createOpenAI` |
| vllm | `http://localhost:8000/v1` | `createOpenAI` |
| acp | —（需 `agent_url`） | `createACP` |

### 8.2 特殊协议：修改 factory.go

只有需要非 OpenAI 兼容协议时，才需要改 [factory.go](../internal/provider/factory.go)：

```go
func (f *Factory) CreateModel(name string) (model.Model, error) {
    p := f.cfg.FindProvider(name)
    // ...
    switch p.Type {
    case "openai", "anthropic", "google", "deepseek",
        "ollama", "lmstudio", "vllm":
        return f.createOpenAI(p), nil
    case "acp":
        return f.createACP(p)
    case "my_new_protocol":         // ← 新增
        return f.createMyProtocol(p) // ← 新增
    }
}
```

LLM 抽象是 `model.Model` 接口（`Info() model.Info` + `GenerateContent(ctx, *model.Request) (<-chan *model.Response, error)`），不是 `Provider.Call`。

---

## 9. 添加 CLI 命令

### 9.1 步骤

**步骤 1**：在 `internal/cli/` 新建文件：

```go
// internal/cli/foo_mgmt.go
package cli

import "github.com/spf13/cobra"

func newFooCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "foo",
        Short: "A new foo command",
        RunE: func(cmd *cobra.Command, args []string) error {
            // 你的逻辑
            return nil
        },
    }
    cmd.Flags().StringP("bar", "b", "", "bar flag")
    return cmd
}
```

**步骤 2**：在 [root.go](../internal/cli/root.go) 的 `newRootCmd()` 中注册：

```go
cmd.AddCommand(newFooCmd())
```

### 9.2 命名规范

- 管理类命令：`*_mgmt.go`（如 `memory_mgmt.go`、`apps_mgmt.go`）
- 功能命令：`<name>.go`（如 `health.go`、`env.go`）
- 当前已有 30 个顶层命令，按此模式扩展

---

## 10. 添加编排模式

多模式编排定义在 [workflow.go](../internal/agent/workflow.go)。已支持 **10 种模式**：

| 模式 | 常量 | 说明 |
|------|------|------|
| single | `WorkflowSingle` | 单 Agent（默认） |
| chain | `WorkflowChain` | 顺序执行 |
| parallel | `WorkflowParallel` | 并行执行 |
| cycle | `WorkflowCycle` | 循环迭代 |
| graph | `WorkflowGraph` | 图编排 |
| team_coordinator | `WorkflowTeamCoordinator` | 团队协调 |
| team_swarm | `WorkflowTeamSwarm` | 群体智能 |
| claude_code | `WorkflowClaudeCode` | Claude Code 风格 |
| codex | `WorkflowCodex` | Codex 风格 |
| dify | `WorkflowDify` | Dify 风格 |

添加新模式时，在 `Build` 方法的 switch 中增加 case 并实现 `buildXxxAgent` 方法。模式通过 `config.yaml` 的 `workflow.mode` 选择。

```go
func (b *WorkflowBuilder) Build(ctx context.Context, wfCfg *OrchestrationConfig) (agent.Agent, error) {
    switch wfCfg.Mode {
    case WorkflowSingle:
        return b.buildSingleAgent(ctx, wfCfg)
    // ... 新增 case
    case WorkflowMyMode:
        return b.buildMyModeAgent(ctx, wfCfg)
    }
}
```

---

## 11. 测试指南

### 11.1 测试命令

| 命令 | 说明 | Makefile target |
|------|------|-----------------|
| `go test ./...` | 全量测试 | `make test` |
| `go test -short -race -count=1 ./internal/... ./pkg/...` | **CI 等价**（仅 internal + pkg） | `make test-short` |
| `go test -race -v -count=1 ./...` | 详细输出 | `make test-verbose` |
| `go test -coverprofile=coverage.out ./...` | 覆盖率 | `make test-coverage` |
| `go test -bench=. -benchmem ./...` | 基准测试 | `make bench` |

### 11.2 CI 测试范围

CI（见 [ci.yml](../.github/workflows/ci.yml)）执行：

```bash
go test -short -race -count=1 ./internal/... ./pkg/...
```

> CI **只测试 `internal/` 和 `pkg/`**，**不含 `cmd/`**。`cmd/` 是薄入口，逻辑全在 `internal/`。

### 11.3 关键测试文件

项目包含 **73+ 测试文件**，核心测试包括：

| 测试文件 | 覆盖范围 |
|----------|----------|
| `agent/loop_test.go` | CoreLoop 主循环 |
| `agent/context_test.go` | 上下文管理器 |
| `agent/workflow_test.go` | 10 种编排模式 |
| `agent/recipe_test.go` | Recipe 加载与执行 |
| `agent/recipe_advance_test.go` | Recipe 热重载 |
| `agent/evolution_tracker_test.go` | 进化追踪器 |
| `evolution/evolution_test.go` | 技能进化引擎 |
| `security/guard_test.go` | 安全护栏 |
| `config/config_test.go` | 配置校验 |
| `provider/factory_test.go` | Provider 工厂 |
| `extension/manager_test.go` | 扩展管理器 |
| `extension/builtin/toolset_test.go` | 内置工具集 |
| `extension/builtin/registry_test.go` | 注册表 |
| `cli/tui/model_test.go` | TUI Model |

### 11.4 表格驱动测试示例

```go
func TestGuard_CheckToolPermission(t *testing.T) {
    tests := []struct {
        name      string
        mode      config.PermissionMode
        tool      string
        wantError bool
    }{
        {"chat_only blocks all", config.PermissionChatOnly, "bash", true},
        {"auto allows all", config.PermissionAuto, "bash", false},
        {"smart allows safe", config.PermissionSmart, "memory_add", false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            g := security.NewGuard(&config.SecurityConfig{
                PermissionMode: tt.mode,
            })
            err := g.CheckToolPermission(tt.tool, nil)
            if (err != nil) != tt.wantError {
                t.Errorf("got err=%v, wantError=%v", err, tt.wantError)
            }
        })
    }
}
```

---

## 12. 调试技巧

### 12.1 日志系统

日志基于 Go `slog`，封装在 [logger.go](../internal/util/logger.go)。

| 全局 Flag | 短选项 | 生效函数 | 效果 |
|-----------|--------|----------|------|
| `--debug` | `-D` | `util.SetDebugMode()` | debug 级日志（最详细） |
| `--quiet` | — | `util.SetQuietMode()` | 仅 warn / error |

优先级：**`--debug` > `--quiet` > 配置文件 `log_level`**

```bash
./wukong -D session           # Debug 模式
./wukong --quiet session      # 安静模式
./wukong -D run -m "test"     # Debug 单发模式
```

### 12.2 关键日志前缀

| 前缀 | 含义 |
|------|------|
| `memory:` | 持久记忆读写与去重 |
| `memoryflow:` | CortexDB 记忆流唤醒与注入 |
| `recall:` | 对话召回检索 |
| `graphflow:` | 知识图谱自动抽取 |
| `cortex:` | 向量存储读写 |
| `agent:` | Agent 工具加载 |
| `config:` | 配置校验警告 |
| `recipe:` | Recipe 加载与热重载 |

### 12.3 Delve 调试

```bash
dlv debug ./cmd/wukong -- session
(dlv) break internal/agent/loop.go:126   # NewCoreLoop 入口断点
(dlv) continue
```

### 12.4 遥测

OpenTelemetry 分布式追踪通过 [telemetry/](../internal/telemetry/) 包实现。配置 `telemetry.enabled: true` 后，Agent 执行链路会自动上报到 OTLP 端点（gRPC 或 HTTP）。

---

## 13. 配置系统

> **完整配置参考**（7 级加载优先级、环境变量展开、40+ 配置段逐项说明、校验规则）见 [CONFIG.md](./CONFIG.md)，本章不再重复。本节仅覆盖开发者视角：类型文件的组织方式与新增配置项的流程。

### 13.1 类型文件组织

配置类型定义在 `internal/config/` 下，按子系统拆分为 10 个 `types_*.go`，根结构体 `WukongConfig` 汇总于 [config.go](../internal/config/config.go)：

| 文件 | 主要类型 |
|------|----------|
| `types_agent.go` | Agent、Security（含 Sandbox / SandboxLimits 进程级资源限额） |
| `types_provider.go` | Provider、Extension、ToolPermission |
| `types_storage.go` | Session、Memory、Todo、Recall |
| `types_cortex.go` | Cortex、Chunking、VerticalRouting、SearchStrategy、MemoryFlow、GraphFlow、ImportFlow、Revision |
| `types_apps.go` | Apps、CloneDefaults、PackDefaults |
| `types_browser.go` | Browser、Proxy、Search（DuckDuckGo / SearXNG / Tavily / Google / Bing） |
| `types_server.go` | A2AServer、AGUI、ACPServer、ACPMCP、MCPServer |
| `types_features.go` | Visualiser、Tutorial、TopOfMind、CodeMode |
| `types_observability.go` | Telemetry、Observability、Eval、EvalMetric、Artifact |
| `types_orchestration.go` | ARD、Summon、A2ARemote、ANP、Skill、Evolution、Knowledge、OKF、Dify、Workflow、SubAgent、TeamMember |

其余文件职责：`config.go`（`WukongConfig` 根结构体 + `Loader`）、`defaults.go`（`setDefaults()` 注册全部内置默认值，按子系统拆分为 `setXxxDefaults()`）、`validate.go`（`Validate()` 致命校验 + `Warnings()` 非致命警告）。

配套测试：`config_test.go`（加载与校验）、`sandbox_config_test.go` / `sandbox_validate_test.go`（`Security.Sandbox` 资源限额的默认零值与显式值校验）。

### 13.2 如何新增配置项

1. 在对应子系统的 `types_*.go` 中定义结构体，字段加 `mapstructure` 标签（YAML 键名）
2. 在 [config.go](../internal/config/config.go) 的 `WukongConfig` 中添加字段（对应 YAML 配置段）
3. 在 [defaults.go](../internal/config/defaults.go) 中新增 `setXxxDefaults()` 注册默认值，并在 `setDefaults()` 调用链中挂接
4. 如需校验，在 [validate.go](../internal/config/validate.go) 中添加致命校验（`Validate`）或非致命警告（`Warnings`）
5. 更新 [CONFIG.md](./CONFIG.md) 对应配置段
6. 运行 `wukong config validate` 验证（与启动路径一致，调用 `loader.LoadAndValidate()` 执行 `validate.go` 全部规则）

---

## 14. 构建与发布

### 14.1 Makefile target

[Makefile](../Makefile) 提供以下 target：

```bash
make build         # 当前平台构建 → build/wukong
make build-all     # 5 平台交叉编译（linux/darwin amd64+arm64, windows amd64）
make install       # 安装到 $GOPATH/bin
make test          # 全量测试
make test-short    # CI 等价（internal + pkg）
make test-coverage # 覆盖率报告 → coverage/coverage.html
make bench         # 基准测试
make lint          # golangci-lint
make fmt           # gofmt
make vet           # go vet
make verify        # fmt + vet + test
make tidy          # go mod tidy
make release       # 构建并打包发布归档
make docker-build  # Docker 镜像构建
make clean         # 清理构建产物
```

### 14.2 ldflags 版本注入

版本信息通过 ldflags 在构建时注入到 `internal/cli` 包：

```makefile
LDFLAGS := -s -w \
    -X github.com/km269/wukong/internal/cli.Version=$(VERSION) \
    -X github.com/km269/wukong/internal/cli.GitCommit=$(GIT_COMMIT) \
    -X github.com/km269/wukong/internal/cli.BuildDate=$(BUILD_DATE)
```

默认版本信息定义在 [version.go](../internal/util/version.go)：`Version = "0.3.1"`。

### 14.3 Taskfile.yaml

[Taskfile.yaml](../Taskfile.yaml) 镜像了全部 Makefile target，使用 [Task](https://taskfile.dev/) 运行器：

```bash
task build
task test
task lint
```

### 14.4 GoReleaser

[.goreleaser.yaml](../.goreleaser.yaml) 配置自动化发布，支持跨平台二进制和打包格式（tar.gz / zip）。

### 14.5 GitHub Actions CI/CD

| 工作流 | 文件 | 作用 |
|--------|------|------|
| CI | `.github/workflows/ci.yml` | lint → test → build（3 个 job） |
| Release | `.github/workflows/release.yml` | tag 触发自动发布 |

### 14.6 Docker

[Dockerfile](../Dockerfile) 支持容器化部署：

```bash
make docker-build    # 或
docker build -t wukong .
```

### 14.7 贡献流程

```
1. fork 仓库并创建分支
   git checkout -b feat/my-feature

2. 编写代码 + 测试（确保 make verify 通过）

3. 提交（遵循 Conventional Commits）
   git commit -m "feat(extension): add tavily search backend"

4. 推送并创建 Pull Request

5. CI 自动运行：lint → test → build
```

**提交规范（Conventional Commits）：**

```
<type>(<scope>): <description>

types: feat / fix / docs / refactor / test / chore / perf

示例：
feat(extension): add tavily search backend to web toolset
fix(security): block obfuscated rm -rf variants via token analysis
docs(agent): correct CoreLoop.Run signature in developer guide
```

---

## 相关文档

| 文档 | 内容 |
|------|------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | 系统整体架构设计 |
| [CLI_TUI.md](./CLI_TUI.md) | 命令行与 TUI 架构 |
| [CONFIG.md](./CONFIG.md) | 完整配置项参考 |
| [API_REFERENCE.md](./API_REFERENCE.md) | Go 接口/结构体参考（CoreLoop、Provider、Extension、Security 等扩展点，非 REST API） |
| [MEMORY_ARCHITECTURE.md](./MEMORY_ARCHITECTURE.md) | 记忆系统架构 |
| [DEPLOYMENT.md](./DEPLOYMENT.md) | 部署指南 |

---

> **版本**: v0.3.1 | **最后更新**: 2026-08-25 | **Go**: 1.26 | **直接依赖**: 29 | **测试文件**: 73+
