# Wukong 系统架构文档

> **版本**: v0.1.0 | **语言**: Go 1.26 | **许可证**: Educational/Research

---

## 目录

1. [项目概述](#1-项目概述)
2. [技术栈](#2-技术栈)
3. [系统架构总览](#3-系统架构总览)
4. [核心子系统](#4-核心子系统)
5. [数据流](#5-数据流)
6. [目录结构](#6-目录结构)
7. [模块依赖关系](#7-模块依赖关系)
8. [设计决策](#8-设计决策)

---

## 1. 项目概述

**Wukong** 是一个本地优先、可扩展的 AI Agent 平台，使用 Go 语言构建。设计灵感来源于 [Goose](https://github.com/aaif-goose/goose)，基于 [tRPC](https://github.com/trpc-group) 生态系统的三个核心框架构建。

### 核心定位

- **本地优先**：所有数据存储在本地 SQLite，无需云端服务
- **可扩展**：通过 MCP 协议支持内置和外部扩展工具
- **多模型**：支持 OpenAI、Anthropic、Google、DeepSeek、Ollama、LMStudio 等
- **长期记忆**：自动或手动管理跨会话的知识持久化
- **子代理委派**：支持 A2A 协议的代理间通信

### 与 Goose 的关系

| 维度 | Goose (Rust) | Wukong (Go) |
|------|-------------|-------------|
| 语言 | Rust | Go |
| Agent 引擎 | 自定义 Rust 循环 | tRPC-Agent-Go Runner |
| MCP 客户端 | goose-mcp crate | tRPC-MCP-Go |
| Session | 自定义实现 | tRPC-Agent-Go Session |
| Memory | 内置扩展 | tRPC-Agent-Go Memory (SQLite) |
| TUI | Electron + Rust | Bubbletea (纯 Go) |
| Providers | 15+ Rust providers | OpenAI 兼容 API |

---

## 2. 技术栈

### 核心框架

| 框架 | 版本 | 用途 |
|------|------|------|
| [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go) | v1.10.0 | Agent 框架：Runner、Session、Memory、Tool 系统 |
| [tRPC-MCP-Go](https://github.com/trpc-group/trpc-mcp-go) | v0.0.16 | MCP 协议实现，用于扩展工具系统 |
| [tRPC-A2A-Go](https://github.com/trpc-group/trpc-a2a-go) | v0.2.5 | Agent-to-Agent 通信协议 |

### 关键依赖

| 库 | 用途 |
|----|------|
| Cobra + Viper | CLI 框架 + 配置管理 |
| Bubbletea + Lipgloss | TUI 终端界面 |
| Chromedp | 无头浏览器自动化 |
| goja | JavaScript 沙箱引擎 (纯 Go) |
| SQLite (mattn) | 本地持久化存储 |
| OpenTelemetry | 分布式追踪 |

---

## 3. 系统架构总览

```
┌──────────────────────────────────────────────────────────────────┐
│                     CLI 层 (cmd/wukong/main.go)                    │
├──────────────────────────────────────────────────────────────────┤
│                   Cobra CLI (internal/cli/)                       │
│   root.go    session.go    configure.go    extension.go          │
├──────────────────────────────────────────────────────────────────┤
│                Bubbletea TUI (internal/cli/tui/)                  │
│   model.go      view.go      update.go                           │
├──────────────────────────────────────────────────────────────────┤
│                    核心引擎层                                       │
├───────────────────┬──────────────────┬───────────────────────────┤
│   Agent Loop      │  Context Manager  │  Extension Manager        │
│   (agent/loop.go) │ (agent/context.go)│ (extension/manager.go)    │
├───────────────────┴──────────────────┴───────────────────────────┤
│                    tRPC-Agent-Go Runner                           │
├──────┬──────┬──────┼─────────┬────────┬──────────────────────────┤
│ LLM  │Session│Memory│  Tool   │Artifact│ Skill Repository         │
│Agent │Service│Service│ System  │Service │ (skill/manager.go)      │
├──────┴──────┴──────┼─────────┴────────┴──────────────────────────┤
│   tRPC-MCP-Go      │          tRPC-A2A-Go                        │
│   (MCP Client)     │       (Agent-to-Agent)                      │
├────────────────────┼─────────────────────────────────────────────┤
│   Provider Factory │  安全层 (security/guard.go)                  │
│ (provider/factory) │  权限模式 / 命令拦截 / 恶意软件扫描            │
├────────────────────┴─────────────────────────────────────────────┤
│                     存储层 (SQLite 连接池)                         │
├──────────┬──────────┬──────────┬──────────┬──────────────────────┤
│ Session  │ Memory   │  Recall  │   Todo   │    Apps / Skills      │
│ Store    │ Store    │  Store   │  Store   │    (文件系统)          │
└──────────┴──────────┴──────────┴──────────┴──────────────────────┘
```

---

## 4. 核心子系统

### 4.1 Agent 循环 (`internal/agent/`)

**入口**: `loop.go` → `CoreLoop`

这是系统的核心执行引擎，负责：

1. **接收用户输入** → 通过 `Run()` 或 `RunStream()` 方法
2. **LLM 思考** → 调用配置的 LLM 模型
3. **工具调用** → 执行模型请求的工具操作
4. **结果反馈** → 将工具结果送回模型继续推理
5. **响应输出** → 提取最终文本响应返回给用户

```
用户消息 → Runner.Run()
  ├── LLM Agent 推理
  ├── 工具调用 (通过 tRPC Tool 系统)
  │   ├── 内置扩展工具 (developer, memory, visualiser, etc.)
  │   └── 外部 MCP 服务器工具
  ├── 安全守卫检查 (CheckToolPermission)
  ├── 记忆自动提取 (auto_extract, 异步)
  ├── 回调链 (Agent/Tool/Model Callbacks)
  └── 事件流 → TUI 渲染
```

**关键组件**:
- `CoreLoop`：主循环控制器
- `ContextManager`：上下文窗口管理、Token 优化
- `WorkflowBuilder`：多模式工作流编排 (single/chain/parallel/cycle/graph)

### 4.2 扩展系统 (`internal/extension/`)

**入口**: `manager.go` → `Manager`

管理所有 MCP 扩展的生命周期：

```
扩展注册
├── 内置扩展 (builtin/)        ← 通过 factory.go 创建
│   ├── developer              ← 文件读写、命令执行、代码搜索
│   ├── computer_controller    ← Web 抓取、文件缓存、浏览器自动化
│   ├── memory                 ← 长期记忆工具 (封装 tRPC Memory Service)
│   ├── auto_visualiser        ← 图表/流程图/表格生成
│   ├── tutorial               ← 交互式教程
│   ├── top_of_mind            ← 持久化指令注入
│   ├── code_mode              ← JavaScript 沙箱执行
│   └── apps                   ← 自定义 HTML 应用
│
└── 外部扩展 (MCP Protocol)
    ├── stdio 传输             ← 本地进程通信
    ├── sse 传输               ← HTTP Server-Sent Events
    └── streamable 传输        ← HTTP 流式
```

**扩展注册流程**:
1. `RegisterBuiltins()` 根据配置自动注册启用的内置扩展
2. `RegisterFromDeeplink()` 从 `wukong://extension?` URL 安装外部扩展
3. `Manager.Initialize()` 加载所有扩展并创建 ToolSet
4. 运行时可通过 CLI 动态启用/禁用扩展

### 4.3 存储系统

所有持久化存储共享一个 SQLite 连接池 (`internal/util/database.go` → `DatabasePool`)：

```
DatabasePool (wukong.db)
├── Session Service    (tRPC session/sqlite)  → 对话历史
├── Memory Service     (tRPC memory/sqlite)   → 长期记忆
├── Recall Store       (FTS5 全文搜索)         → 跨会话聊天搜索
├── Todo Store         (SQLite CRUD)           → 任务跟踪
└── 自定义表 (Apps, etc.)
```

**连接池设计**:
- 所有模块共享同一个 `*sql.DB` 实例
- WAL 模式 + 外键约束
- `noCloseDBWrapper` 防止单个模块关闭共享连接
- 内存后端 (`memory`) 用于测试和临时场景

### 4.4 安全系统 (`internal/security/`)

**入口**: `guard.go` → `Guard`

四层安全防护：

```
1. 工具权限检查 (CheckToolPermission)
   ├── Allowlist 白名单 (非空时仅允许列表内工具)
   ├── Denylist 黑名单 (始终阻止)
   └── PermissionMode 权限模式
       ├── auto       → 全部自动执行
       ├── smart      → 高风险操作需审批 (推荐)
       ├── manual     → 全部需审批
       └── chat_only  → 纯文本模式

2. 命令安全验证 (ValidateCommand)
   └── 拦截危险命令模式 (rm -rf /, dd, mkfs, etc.)

3. 扩展恶意软件扫描 (ScanExtension)
   └── 三层检查：已知恶意模式 → 代码签名 → 用户确认

4. 超时保护
   └── DefaultTimeout (30s) / MaxTimeout (300s)
```

### 4.5 记忆系统 (`internal/memory/`)

**入口**: `store.go` → `MemoryManager`

```
MemoryManager
├── Auto Extract 模式 (auto_extract: true)
│   ├── Runner 完成对话后触发 enqueueAutoMemoryJob
│   ├── Extractor 模型分析对话提取记忆
│   └── 异步 Worker 池处理 (3 workers)
│
└── Manual 模式 (auto_extract: false 或无 extractor 模型)
    └── Agent 手动调用 memory_add/search/update/delete/load/clear 工具

工具列表 (来自 tRPC memory.Service):
├── memory_add      → 添加记忆
├── memory_search   → 搜索记忆
├── memory_update   → 更新记忆
├── memory_delete   → 删除记忆
├── memory_load     → 加载所有记忆
└── memory_clear    → 清空记忆
```

**关键设计**:
- 所有 6 个 memory 工具始终对 agent 可见（不受 auto_extract 影响）
- Auto extract 需要 extractor 模型（使用 default_provider 的模型）
- extractor 模型创建失败时，auto extract 静默降级，但手动工具仍可用

### 4.6 子代理委派 (`internal/summon/` + `internal/skill/`)

两个独立的子代理系统：

```
Summon 系统 (summon/auth.go)
├── 本地 Skills (summon.skills_dir: .wukong_skills)
│   └── 单个 .md 文件作为 recipe
├── 远程 A2A Agents (summon.a2a_remotes)
│   ├── JWT / API Key / OAuth2 认证
│   └── 凭证自动轮换 (CredentialRotator)
└── 并发控制 (max_concurrent: 5)

Skill 系统 (skill/manager.go)
├── FSRepository (skill.skills_dir: .wukong_agent_skills)
│   └── 目录结构，每个目录含 SKILL.md
└── CreateSkillAgent() → 创建独立 LLMAgent
```

### 4.7 可观测性 (`internal/telemetry/` + `internal/health/`)

```
Telemetry (OpenTelemetry)
├── Exporter: gRPC / HTTP / Console
├── Tracing: Agent Loop → LLM Call → Tool Call 全链路
├── Sampling: 0.0 - 1.0 采样率
└── Resource: service_name, version, environment

Health Checks
├── /health      → 完整健康检查 JSON
├── /live        → K8s Liveness Probe
├── /ready       → K8s Readiness Probe
└── Checkers: DB, Model, Extension, A2A Server
```

---

## 5. 数据流

### 5.1 单次对话完整流程

```
用户输入 (TUI)
  │
  ▼
CLI session.go::runSession()
  │
  ▼
CoreLoop.RunUserMessage(ctx, userMsg)
  │
  ├─[1] 加载 Session 历史
  │     sessionService.GetSession(ctx, sessionID)
  │
  ├─[2] 注入持久化指令 (Top of Mind)
  │     topOfMind.FormatForPrompt()
  │
  ├─[3] 召回相关历史 (Recall, 如果启用)
  │     recallStore.Search(query)
  │
  ├─[4] 构建 System Instruction
  │     buildSystemInstruction() → 包含 memory 引导
  │
  ├─[5] Runner.Run(ctx, userID, sessionID, userMsg)
  │     │
  │     ├── LLM Agent 推理 (LLM API 调用)
  │     │
  │     ├── 工具调用循环
  │     │   ├── Security Guard 检查权限
  │     │   ├── 执行工具 (通过 ToolSet)
  │     │   ├── Tool Callbacks (日志、遥测)
  │     │   └── 返回结果给模型
  │     │
  │     ├── Model Callbacks (遥测 span)
  │     │
  │     └── 返回事件流
  │
  ├─[6] 提取最终响应文本
  │     extractMessageContent()
  │
  ├─[7] 自动记忆提取 (如果启用)
  │     memoryService.EnqueueAutoMemoryJob()
  │
  └─[8] 返回响应
        TUI 渲染最终消息
```

### 5.2 工具调用流程

```
Agent 决定调用工具
  │
  ▼
Tool Callback (BeforeTool)
  │
  ├── 创建 OpenTelemetry Span
  │
  ▼
Security Guard.CheckToolPermission(toolName)
  │
  ├── Denylist 检查 → 阻止
  ├── Allowlist 检查 → 不在白名单 → 阻止
  ├── PermissionMode 检查
  │   ├── chat_only → 阻止所有工具
  │   ├── manual → 需要用户审批
  │   ├── smart → 高风险操作需要审批
  │   └── auto → 直接执行
  │
  ▼
执行工具 (tool.Call())
  │
  ▼
Tool Callback (AfterTool)
  │
  ├── 记录执行结果
  ├── 关闭 Span
  └── 返回结果给 Agent
```

---

## 6. 目录结构

```
wukong/
├── cmd/
│   └── wukong/
│       └── main.go                  # 程序入口
├── internal/
│   ├── agent/
│   │   ├── loop.go                  # 核心 Agent 循环
│   │   ├── context.go               # 上下文管理器
│   │   ├── loop_test.go             # 集成测试
│   │   └── workflow_test.go         # 工作流测试
│   ├── apps/
│   │   ├── manager.go               # HTML 应用管理
│   │   └── manager_test.go
│   ├── browser/
│   │   ├── controller.go            # 浏览器自动化 (HTTP + Chromedp)
│   │   └── controller_test.go
│   ├── cli/
│   │   ├── root.go                  # Cobra 根命令
│   │   ├── session.go               # Session 命令 + Bootstrap
│   │   ├── configure.go             # 交互式配置命令
│   │   ├── extension.go             # 扩展管理命令
│   │   └── tui/
│   │       ├── model.go             # Bubbletea TUI 模型
│   │       ├── view.go              # 视图渲染 + 样式
│   │       └── update.go            # 事件处理 + 流式桥接
│   ├── codemode/
│   │   ├── executor.go              # goja JS 沙箱
│   │   └── executor_test.go
│   ├── config/
│   │   ├── config.go                # Viper 配置加载 + 结构体定义
│   │   └── config_test.go
│   ├── extension/
│   │   ├── manager.go               # MCP 扩展生命周期管理
│   │   ├── deeplink.go              # wukong://extension URL 解析
│   │   ├── factory.go               # 内置扩展工厂
│   │   └── builtin/
│   │       ├── registry.go          # 内置扩展注册
│   │       ├── developer.go         # 开发者工具集
│   │       ├── memory.go            # 记忆工具集
│   │       ├── auto_visualiser.go   # 自动可视化
│   │       ├── tutorial.go          # 交互式教程
│   │       └── computer_controller.go # 计算机控制
│   ├── health/
│   │   ├── health.go                # 健康检查端点
│   │   └── health_test.go
│   ├── memory/
│   │   └── store.go                 # 记忆管理器 (SQLite/内存)
│   ├── provider/
│   │   ├── factory.go               # LLM 模型工厂
│   │   └── factory_test.go
│   ├── recall/
│   │   ├── store.go                 # 跨会话聊天搜索 (FTS5)
│   │   ├── tool.go                  # 召回工具
│   │   ├── store_test.go
│   │   ├── recall_test.go
│   │   └── tool.go                  # 召回管理器工具
│   ├── security/
│   │   ├── guard.go                 # 安全守卫
│   │   └── guard_test.go
│   ├── session/
│   │   └── store.go                 # 会话服务 (SQLite/内存)
│   ├── skill/
│   │   └── manager.go               # Agent Skill 系统
│   ├── summon/
│   │   ├── auth.go                  # A2A 凭证轮换
│   │   ├── auth_test.go
│   │   └── summon_integration_test.go
│   ├── telemetry/
│   │   ├── telemetry.go             # OpenTelemetry 管理
│   │   └── telemetry_test.go
│   ├── todo/
│   │   ├── tool.go                  # Todo 系统 (工具 + 存储)
│   │   └── tool_test.go
│   ├── topofmind/
│   │   ├── mind.go                  # 持久化指令注入
│   │   └── mind_test.go
│   └── util/
│       ├── database.go              # SQLite 连接池
│       ├── logger.go                # 结构化日志 (slog)
│       ├── ptr.go                   # 指针辅助函数
│       ├── util_test.go
│       └── logger_test.go
├── config.yaml                      # 默认配置文件
├── Makefile                         # Make 构建脚本
├── Taskfile.yaml                    # Task 构建脚本 (跨平台)
├── go.mod
├── go.sum
├── README.md
├── ARCHITECTURE.md                  # 本文档
└── CONFIG.md                        # 配置说明文档
```

---

## 7. 模块依赖关系

```
main.go
  └── cli/
      ├── config/          (配置加载)
      ├── provider/        (LLM 工厂)
      ├── session/         (会话存储)
      ├── memory/          (记忆存储)
      ├── agent/           (核心循环)
      │   ├── provider/
      │   ├── security/
      │   ├── recall/
      │   └── util/
      ├── extension/       (扩展系统)
      │   ├── builtin/     (内置工具)
      │   │   ├── browser/
      │   │   ├── codemode/
      │   │   └── memory/  (注入 MemoryService)
      │   └── config/
      ├── summon/          (子代理)
      │   └── skill/
      ├── todo/
      ├── topofmind/
      ├── apps/
      ├── health/
      ├── telemetry/
      └── tui/             (终端界面)
          └── agent/       (通过 CoreLoop)
```

**关键依赖方向**:
- `agent/` → 不依赖 `cli/`、`tui/` (可独立测试)
- `extension/` → 不依赖 `agent/` (可独立测试)
- `security/` → 不依赖任何业务模块 (纯安全逻辑)
- `provider/` → 不依赖任何业务模块 (纯 LLM 工厂)
- `util/` → 所有模块的底层工具库

---

## 8. 设计决策

### 8.1 为什么用 tRPC 框架而不是自建 Agent 循环？

- **Session 管理**：tRPC 提供完整的会话存储、摘要、事件管理
- **Memory 系统**：内置 auto-extract 模式，支持异步记忆提取
- **Tool 系统**：标准化的工具注册/调用/回调机制
- **Runner 抽象**：统一的事件流处理，支持多种 Agent 类型
- **生态整合**：与 tRPC-MCP-Go 和 tRPC-A2A-Go 无缝协作

### 8.2 为什么用 SQLite 而不是其他数据库？

- **零配置**：无需安装数据库服务器
- **本地优先**：数据文件随项目移动
- **WAL 模式**：支持并发读写
- **FTS5**：内置全文搜索（Recall 系统）
- **连接池共享**：所有模块共享一个 DB 文件

### 8.3 为什么分离 Memory Tools 和 Auto Extract？

- **容错性**：即使 extractor 模型不可用，agent 仍可手动管理记忆
- **可控性**：用户可以手动添加/删除特定记忆
- **灵活性**：auto extract 自动捕获隐含信息，manual tools 精确管理

### 8.4 安全设计原则

- **默认安全**：permission_mode 默认为 smart
- **纵深防御**：Allowlist + Denylist + PermissionMode + 命令拦截
- **最小权限**：扩展可通过 Permissions 限制特定工具
- **超时保护**：所有工具执行都有默认 30s 超时
