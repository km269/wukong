---
name: wukong-goose-replica
overview: 使用 tRPC-Agent-Go、tRPC-MCP-Go、tRPC-A2A-Go 三个框架，用 Go 语言复刻 Goose（Linux Foundation/AAIF 的本地 AI Agent 平台），实现 CLI 交互界面、MCP 扩展系统、会话管理、记忆系统、Todo/Summon 子系统、上下文优化引擎。
todos:
  - id: init-project
    content: 初始化项目结构：完善 go.mod 添加 tRPC 三件套及 Cobra/Bubbletea/Viper 等依赖，创建完整目录结构
    status: completed
  - id: config-system
    content: 实现配置管理系统：定义 Config 结构体，Viper YAML 加载器，创建 config.yaml 默认模板，支持 Provider/Extension/Session 配置
    status: completed
    dependencies:
      - init-project
  - id: provider-factory
    content: 实现 Model Provider 工厂：根据配置创建 OpenAI 兼容、Ollama 等模型实例，支持多 Provider 切换
    status: completed
    dependencies:
      - config-system
  - id: session-memory-store
    content: 实现 Session 和 Memory 存储层：基于 tRPC-Agent-Go 的 SQLite Session Service 和自动提取 Memory Service 封装
    status: completed
    dependencies:
      - init-project
  - id: extension-manager
    content: 实现 MCP 扩展管理系统：基于 tRPC-MCP-Go 的 MCP ToolSet 统一创建接口，实现内置开发者工具扩展和外部 MCP Server 接入
    status: completed
    dependencies:
      - init-project
  - id: todo-tool
    content: 实现 Todo 任务追踪系统：Function Tool 定义 create/update/complete/list 操作，SQLite 持久化存储
    status: completed
    dependencies:
      - init-project
  - id: agent-loop
    content: 实现核心 Agent 循环引擎：基于 tRPC-Agent-Go Runner + LLMAgent 构建交互式工具调用循环，集成上下文优化 Callback 和 Summon 子代理委托
    status: completed
    dependencies:
      - provider-factory
      - session-memory-store
      - extension-manager
      - todo-tool
  - id: cli-tui
    content: 使用 [skill:golang-pro] 实现 CLI 和 TUI 交互界面：Cobra 命令体系（session/configure），Bubbletea 三区布局 TUI 支持流式对话和工具调用状态展示
    status: completed
    dependencies:
      - agent-loop
  - id: integration-test
    content: 编写集成测试和示例配置：验证完整交互流程（用户输入→模型推理→工具调用→结果返回），使用 [mcp:GitHub MCP Server] 推送到 GitHub
    status: completed
    dependencies:
      - cli-tui
---

## 用户需求

基于 tRPC 团队开源的三个 Go 开发框架（tRPC-Agent-Go、tRPC-MCP-Go、tRPC-A2A-Go），复刻 Linux Foundation/AAIF 旗下的开源本地 AI Agent 平台 Goose（48k+ Stars），仅实现 CLI 端交互界面。

## 产品概述

Wukong 是一个本地优先、通用目的、可扩展的 AI Agent CLI 平台。用户通过终端与 LLM 模型交互，Agent 能够调用 MCP 扩展工具执行实际操作（文件读写、命令执行、浏览器控制等），并具备会话管理、长期记忆、任务追踪、子代理委托等能力。

## 核心功能

- **交互式 Agent 循环**：用户输入任务 → LLM 推理 → 工具调用执行 → 结果反馈 → 上下文优化 → 继续推理，错误自愈机制
- **MCP 扩展系统**：内置开发工具扩展 + 支持通过 stdio/HTTP 接入任意第三方 MCP Server，动态工具发现
- **多模型 Provider**：支持 OpenAI 兼容 API、Ollama 等至少 5 种主流模型接入
- **会话 & 记忆管理**：SQLite 本地持久化对话历史，自动提取用户偏好形成长期记忆
- **Todo 任务追踪**：Agent 可创建、更新、完成任务列表，支撑复杂多步骤工作流
- **Summon 子代理委托**：将子任务委托给专用子 Agent 处理，支持 Chain/Parallel 编排
- **上下文优化**：会话摘要、Token 限制、历史清理等策略控制长对话成本
- **配置管理**：YAML 配置文件管理模型、扩展、会话等全局设置

## 技术栈

### Go 语言运行环境

- Go 1.24+（go.mod 声明 go 1.26.3）
- 模块管理：go mod

### 核心依赖框架

| 框架 | 版本 | 用途 |
| --- | --- | --- |
| trpc-agent-go | v0.6+ | 核心 Agent 框架：LLMAgent、Runner、Session、Memory、Tool、Callback |
| trpc-mcp-go | latest | MCP 协议实现：STDIO/SSE/Streamable HTTP 三种传输方式 |
| trpc-a2a-go | latest | A2A 协议实现：子代理委托时的 Agent 间通信 |
| cobra | v1.8+ | CLI 命令行框架 |
| bubbletea | latest | 现代化 TUI 交互界面 |
| viper | latest | YAML 配置管理 |
| sqlite3 | latest | 本地持久化存储（驱动依赖） |


## 实现方法

### 架构思路

采用"薄 CLI 层 + 厚核心引擎"的分层架构：CLI 层（cobra + bubbletea）负责交互渲染，核心引擎层（基于 tRPC 三件套）负责 Agent 逻辑编排。充分利用 tRPC-Agent-Go 已有能力（Runner、Session、Memory、Tool 系统），避免重复造轮子，在已有原语之上组合 Goose 特色功能。

```
┌──────────────────────────────────────────────┐
│               CLI 交互层 (Bubbletea TUI)       │
├───────────────┬──────────────┬────────────────┤
│  chat mode    │ agent mode   │ config mode     │
├───────────────┴──────────────┴────────────────┤
│              Wukong Core Engine               │
├──────────────────────────────────────────────┤
│ Agent Loop  │ Context Mgr │ Extension Mgr     │
├──────────────────────────────────────────────┤
│          tRPC-Agent-Go Runner                 │
├──────┬──────┬──────┬───────┬─────────────────┤
│LLM   │Session│Memory│Tool   │ Callbacks/Plugin│
│Agent │Service│Service│System │                 │
├──────┴──────┴──────┼───────┴─────────────────┤
│ tRPC-MCP-Go        │ tRPC-A2A-Go             │
│ (MCP Client)       │ (Agent间通信)            │
└────────────────────┴─────────────────────────┘
```

### 关键设计决策

1. **Agent 循环**：基于 tRPC-Agent-Go 的 Runner + LLMAgent 实现。Runner 已内置事件流处理、Session 管理、工具调用等功能，在其基础上通过 `llmagent.WithCallbacks` 插入上下文优化逻辑（如工具调用后的摘要判断）

2. **MCP 扩展**：使用 `mcp.NewMCPToolSet` 统一管理内置扩展和外部扩展。内置扩展作为独立 Go 包注册到 MCP ToolSet；外部扩展通过 stdio 进程启动外部 MCP Server

3. **子代理委托**：使用 `agenttool.NewTool(wrappedAgent)` 将子 Agent 包装为工具，父 Agent 通过工具调用实现任务委托。可选使用 ChainAgent/ParallelAgent 实现确定性编排

4. **配置管理**：Viper 读取 YAML，映射到 Go 结构体，注入到 Agent 创建工厂

5. **CLI 界面**：Bubbletea TUI 提供三区布局（对话区、工具调用状态区、输入区），支持流式输出渲染

### 性能与可靠性

- Runner 内置并发安全的事件通道，支持单次 run 的 goroutine 生命周期管理
- SQLite Session Service 本地存储，支持事件限制和 TTL 自动淘汰
- Memory Service 自动提取模式使用异步 worker，不阻塞对话主流程
- 上下文优化使用 Callback 在 BeforeModel 阶段拦截，减少不必要的 token 消耗

## 实现细节

### 核心目录结构

```
wukong/
├── cmd/wukong/
│   └── main.go                  # [NEW] CLI 入口，初始化配置、创建 Runner、启动 TUI
├── internal/
│   ├── agent/
│   │   ├── loop.go              # [NEW] 交互式 Agent 循环核心逻辑
│   │   └── context.go           # [NEW] 上下文修订器：摘要触发、token 估计、历史清理
│   ├── config/
│   │   ├── config.go            # [NEW] 配置结构体定义（Provider/Extension/Session/Todo）
│   │   └── loader.go            # [NEW] 配置加载器：Viper 读取 YAML，默认值填充
│   ├── extension/
│   │   ├── manager.go           # [NEW] 扩展管理器：注册、初始化、卸载 MCP ToolSet
│   │   └── builtin/
│   │       ├── developer.go     # [NEW] 内置开发者工具：文件读写、命令执行、代码搜索
│   │       ├── browser.go       # [NEW] 内置浏览器控制工具
│   │       └── registry.go      # [NEW] 内置扩展注册表
│   ├── provider/
│   │   └── factory.go           # [NEW] Model Provider 工厂：统一创建 openai.New 模型实例
│   ├── cli/
│   │   ├── root.go              # [NEW] Cobra 根命令定义
│   │   ├── session.go           # [NEW] session 子命令：创建/切换/列出会话
│   │   ├── configure.go         # [NEW] configure 子命令：交互式配置模型和扩展
│   │   └── tui/
│   │       ├── model.go         # [NEW] Bubbletea Model：状态管理、事件驱动更新
│   │       ├── view.go          # [NEW] Bubbletea View：三区布局渲染（对话/状态/输入）
│   │       └── update.go        # [NEW] Bubbletea Update：消息分发与状态转换
│   ├── session/
│   │   └── store.go             # [NEW] Session 存储封装：SQLite Session Service 初始化
│   ├── memory/
│   │   └── store.go             # [NEW] Memory 存储封装：自动提取模式 + SQLite 后端
│   ├── todo/
│   │   ├── tool.go              # [NEW] Todo Function Tool：create/update/complete/list
│   │   └── store.go             # [NEW] Todo 本地持久化存储
│   └── summon/
│       └── delegate.go          # [NEW] Summon 子代理委托：基于 Agent Tool 的任务分发
├── config.yaml                  # [NEW] 默认配置文件模板
├── go.mod                       # [MODIFY] 添加所有依赖
└── go.sum                       # [NEW] 依赖锁文件
```

### 关键代码结构

```
// config/config.go - 配置结构体
type WukongConfig struct {
    DefaultProvider string              `mapstructure:"default_provider"`
    Providers       []ProviderConfig    `mapstructure:"providers"`
    Extensions      []ExtensionConfig   `mapstructure:"extensions"`
    Session         SessionConfig       `mapstructure:"session"`
    Todo            TodoConfig          `mapstructure:"todo"`
}

type ProviderConfig struct {
    Name     string `mapstructure:"name"`
    Type     string `mapstructure:"type"`     // openai, anthropic, ollama
    BaseURL  string `mapstructure:"base_url"`
    APIKey   string `mapstructure:"api_key"`
    Model    string `mapstructure:"model"`
}

type ExtensionConfig struct {
    Name      string   `mapstructure:"name"`
    Type      string   `mapstructure:"type"`      // builtin, external
    Transport string   `mapstructure:"transport"`  // stdio, sse, http
    Command   string   `mapstructure:"command"`
    Args      []string `mapstructure:"args"`
    URL       string   `mapstructure:"url"`
    Enabled   bool     `mapstructure:"enabled"`
}
```

```
// agent/loop.go - 交互式 Agent 循环
type AgentLoop struct {
    runner         runner.Runner
    sessionService session.Service
    memoryService  memory.Service
    toolSets       []tool.ToolSet
    contextMgr     *ContextManager
}

func (l *AgentLoop) Run(ctx context.Context, userID, sessionID string, 
    userMessage model.Message) (<-chan *event.Event, error) {
    // 1. 通过 runner.Run 发送消息，获取事件流
    // 2. 事件流中解析 tool_call -> tool_result -> assistant response
    // 3. Callback 触发上下文优化
}
```

## Agent 扩展

### Skill

- **golang-pro**: 用于编写 Go 并发代码、goroutine 管理、高性能 Agent 循环

### MCP

- **GitHub MCP Server**: 用于后续将项目推送到 GitHub 仓库，管理代码版本

## Agent Extensions

### Skill

- **golang-pro**
- Purpose：编写 Go 并发编程代码，包括 goroutine 生命周期管理、channel 事件流处理、context 超时控制等 Agent 核心循环的高性能并发场景
- Expected outcome：确保 Agent 循环的 goroutine 管理正确，事件通道不泄漏，context 取消安全

### MCP

- **GitHub MCP Server**
- Purpose：将 Wukong 项目初始化为 Git 仓库并推送到 GitHub，方便后续版本管理和开源协作
- Expected outcome：项目代码托管到 GitHub 仓库，具备完整的 commit 历史和项目结构