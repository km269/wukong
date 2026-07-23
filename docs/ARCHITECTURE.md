# Wukong 系统架构

> Go: 1.26 | 内部包: 30+ | 公共包: 2 | 配置结构体: 34
>
> 基于 tRPC-Agent-Go v1.10.0 · tRPC-MCP-Go v0.0.16 · tRPC-A2A-Go v0.2.5 · CortexDB v2.25.0 · OKF v0.1
>
> CLI: 27 顶层命令 + 55+ 子命令 | 直接依赖: 29 | 间接依赖: 105+

---

## 目录

1. [架构哲学](#1-架构哲学)
2. [系统全景](#2-系统全景)
3. [目录结构](#3-目录结构)
4. [技术栈详解](#4-技术栈详解)
5. [CoreLoop 中央编排引擎](#5-coreloop-中央编排引擎)
6. [多 Agent 编排系统](#6-多-agent-编排系统)
7. [双引擎三层记忆系统](#7-双引擎三层记忆系统)
8. [Evolution 技能进化引擎](#8-evolution-技能进化引擎)
9. [OKF 知识格式系统](#9-okf-知识格式系统)
10. [ANP Agent 互通协议栈](#10-anp-agent-互通协议栈)
11. [ARD 双向发现系统](#11-ard-双向发现系统)
12. [Gateway 多平台消息网关](#12-gateway-多平台消息网关)
13. [Extension 扩展系统](#13-extension-扩展系统)
14. [Security 五层安全防御](#14-security-五层安全防御)
15. [Browser 浏览器引擎](#15-browser-浏览器引擎)
16. [Apps 应用管理系统](#16-apps-应用管理系统)
17. [配置系统](#17-配置系统)
18. [服务端点](#18-服务端点)
19. [架构设计决策 (ADRs)](#19-架构设计决策-adrs)
20. [数据流与生命周期](#20-数据流与生命周期)

---

## 1. 架构哲学

Wukong 的设计围绕七大核心哲学展开，每一项都指导了具体的工程决策：

| 哲学 | 核心信念 | 关键工程决策 |
|------|----------|-------------|
| **记忆优先** | Agent 智能源于跨会话知识积累 | 双引擎三层记忆: tRPC Memory + CortexDB Stack |
| **框架组装** | 任何组件都应可替换 | CoreLoop 依赖注入，所有子系统接口隔离 |
| **多 Agent 原生** | 编排是第一公民 | 10 种显式编排模式 + HITL 人机协同 |
| **进化智能** | 技能应从失败中学习 | LLM 分析 → 自动补丁 → 版本管理 → 热重载 |
| **双向发现** | 发现别人，也被人发现 | ARD: 联邦搜索 + RegistryServer 发布 |
| **开放互通** | 标准化协议促进生态互通 | ANP: DID 身份 + 能力协商 + E2EE 加密 |
| **知识标准化** | 知识应有标准形状 | OKF v0.1: Markdown + YAML frontmatter 知识包 |

---

## 2. 系统全景

### 2.1 分层架构图

```
+======================================================================+
|                        Wukong AI Agent Platform                       |
+======================================================================+
|                        接入层 (Entry Layer)                           |
+----------------------------------------------------------------------+
|  CLI (27cmd+55sub)  |  TUI  |  Gateway:9093  |  A2A:9090  |  ACP:9091 |
|  AG-UI SSE:8080     |  MCP:3400       |  ANP:9092                    |
+======================================================================+
|                      编排层 (Orchestration Layer)                     |
+----------------------------------------------------------------------+
|  CoreLoop (中央编排引擎)                                               |
|  ├── WorkflowBuilder (10 种编排模式)                                   |
|  ├── TeamBuilder (团队协作)                                            |
|  ├── ContextManager (3 层上下文管理)                                   |
|  ├── Security Guard (5 层安全防御)                                     |
|  ├── HITL (人机协同)                                                   |
|  ├── TodoEnforcer (任务强制执行)                                       |
|  ├── PromptTemplate (提示词模板)                                       |
|  └── EvolutionTracker (Runner 插件)                                    |
+======================================================================+
|                      能力层 (Capability Layer)                        |
+----------------------------------------------------------------------+
|  Evolution Engine  |  OKF Knowledge  |  ANP Protocol  |  ARD Discovery |
|  Gateway System    |  Extension Mgr  |  Code Mode     |  Knowledge RAG  |
|  Browser Engine    |  Apps System    |  Skill Mgr     |  Recall Search  |
+======================================================================+
|                      框架层 (Framework Layer)                         |
+----------------------------------------------------------------------+
|  tRPC-Agent-Go v1.10.0                                                |
|  ├── LLMAgent / ChainAgent / ParallelAgent / CycleAgent / GraphAgent   |
|  ├── Planner (Builtin / ReAct)                                        |
|  ├── ToolSearch (TopK 工具过滤)                                        |
|  ├── ContextCompaction (上下文压缩)                                    |
|  ├── Skill / Recipe                                                   |
|  └── Runner / Session / Memory                                        |
+======================================================================+
|                      记忆层 (Memory Layer)                            |
+----------------------------------------------------------------------+
|  短期记忆: MemoryFlow — IngestTurn → WakeUp (3层) → PromoteFacts       |
|  中期记忆: CortexStore — HNSW 向量索引 + FTS5 全文检索                  |
|  长期记忆: tRPC Memory — AutoExtract + SmartCleanup                    |
|  结构化记忆: GraphFlow — 实体抽取 → RDF 图谱 → SPARQL 查询             |
+======================================================================+
|                      基础设施层 (Infrastructure Layer)                |
+----------------------------------------------------------------------+
|  7 LLM Providers  |  OpenTelemetry  |  Langfuse  |  MultiPool (SQLite)  |
|  Proxy Pool       |  Stealth Scripts  |  Antibot Escalation           |
+======================================================================+
|                      存储层 (Storage Layer)                           |
+----------------------------------------------------------------------+
|  wukong.db — 单文件 SQLite WAL 模式                                    |
|  ├── sessions (会话历史)                                               |
|  ├── memories (持久记忆)                                               |
|  ├── todos (任务跟踪)                                                  |
|  ├── recall (跨会话召回)                                               |
|  ├── skill_versions (技能版本)                                         |
|  ├── evolution_history (进化历史)                                      |
|  └── cortexdb (向量/全文/图谱)                                         |
+======================================================================+
```

### 2.2 核心数据流向

```
用户输入
    │
    ├─→ CLI / TUI / Gateway / A2A / ACP / AG-UI (接入层)
    │
    └─→ CoreLoop.Run()
         │
         ├─ Phase 1: Prepare (上下文准备)
         │   ├─ MemoryFlow.IngestTurn (转录)
         │   ├─ MemoryFlow.WakeUp (3层唤醒)
         │   ├─ Recall/Cortex.Search (语义召回)
         │   ├─ tRPC Memory.ReadMemories (持久记忆)
         │   ├─ OKF KnowledgeIndexInjector (知识注入)
         │   └─ GraphFlow (可选: 图谱上下文)
         │
         ├─ Phase 2: Execute (执行)
         │   ├─ runner.Run()
         │   ├─ LLM → Tool Calls
         │   ├─ Guard.Check (安全检查)
         │   ├─ ToolSearch (工具过滤)
         │   └── EvolutionTracker (轨迹捕获)
         │
         ├─ Phase 3: Finalize (收尾)
         │   ├─ StoreMessage (消息存储)
         │   ├─ IngestTurn (助手响应转录)
         │   ├─ PromoteFacts (事实提升)
         │   ├─ GraphFlow.AutoExtract (图谱抽取)
         │   └─ Evolution Record (进化记录)
         │
         └─ Phase 4: Return (返回)
             └─ contextMgr.AfterRun (token 统计)
```

---

## 3. 目录结构

### 3.1 顶层结构

```
wukong/
├── cmd/                      # 可执行程序入口
│   ├── wukong/              # 主 CLI 应用
│   ├── zim-check/           # ZIM 文件校验工具
│   └── zim-ls/              # ZIM 文件列表工具
├── internal/                 # 内部包 (不对外暴露)
│   ├── agent/               # CoreLoop 核心引擎
│   ├── apps/                # 应用管理系统
│   ├── ard/                 # ARD 双向发现 + ANP 协议
│   ├── browser/             # 浏览器控制引擎
│   ├── cli/                 # CLI 命令 + TUI
│   ├── codemode/            # Code Mode 执行器
│   ├── config/              # 配置管理
│   ├── cortex/              # CortexDB 记忆栈
│   ├── evolution/           # 技能进化引擎
│   ├── extension/           # MCP 扩展管理
│   ├── gateway/             # 多平台消息网关
│   ├── health/              # 健康检查
│   ├── knowledge/           # RAG 知识库
│   ├── memory/              # 记忆元数据
│   ├── observability/       # 可观测性 (Langfuse)
│   ├── okf/                 # OKF 知识格式核心
│   ├── project/             # 项目管理
│   ├── provider/            # LLM Provider 工厂
│   ├── recall/              # 跨会话召回
│   ├── security/            # 安全防御
│   ├── server/              # 服务端点 (ACP/AG-UI)
│   ├── session/             # 会话存储
│   ├── skill/               # 技能管理
│   ├── summon/              # 子 Agent 委派 + A2A
│   ├── telemetry/           # 遥测数据
│   ├── todo/                # 任务工具
│   ├── topofmind/           # 置顶指令
│   └── util/                # 通用工具
├── pkg/                      # 公共包 (可对外暴露)
│   ├── httpclient/          # HTTP 客户端封装
│   ├── sandbox/             # 跨平台沙箱隔离
│   └── zim/                 # ZIM 格式读写
├── docs/                     # 文档
├── .github/workflows/       # CI/CD 工作流
├── config.yaml               # 配置文件示例
├── ard.yaml                  # ARD 配置
├── .wukongignore             # 文件忽略规则
├── go.mod / go.sum          # Go 依赖管理
├── Makefile / Taskfile.yaml # 构建脚本
├── Dockerfile                # Docker 镜像
└── .goreleaser.yaml         # 发布配置
```

### 3.2 关键目录详解

| 目录 | 文件数 | 核心职责 | 关键类型 |
|------|--------|---------|---------|
| `internal/agent/` | 21 | CoreLoop 核心编排引擎 | CoreLoop, WorkflowBuilder, Recipe, EvolutionTracker |
| `internal/apps/` | 31 | 应用管理 (克隆/打包/预览) | Manager, EnhancedCloner, Packer |
| `internal/ard/` | 22 | ARD 发现 + ANP 协议栈 | AICatalog, DIDManager, HTTPSign |
| `internal/browser/` | 28 | 浏览器控制 + 反反爬 | Backend, Antibot, Stealth, Settle |
| `internal/cli/` | 30 | CLI 命令 + TUI 界面 | rootCmd, TUI Model |
| `internal/config/` | 9 | 配置加载 + 验证 | WukongConfig, Loader, Validator |
| `internal/cortex/` | 14 | CortexDB 记忆栈 | CortexStore, MemoryFlow, GraphFlow |
| `internal/evolution/` | 7 | 技能进化引擎 | EvolutionEngine, Analyzer, Patcher |
| `internal/extension/` | 25 | MCP 扩展管理 | Manager, MCP Broker |
| `internal/gateway/` | 11 | 多平台消息网关 | GatewayServer, FeishuChannel |
| `internal/okf/` | 3 | OKF 知识格式核心 | Bundle, Writer |
| `internal/summon/` | 9 | 子 Agent 委派 + A2A | A2AClient, MetaProtocol, E2EE |
| `internal/security/` | 4 | 五层安全防御 | Guard, IgnoreMatcher |
| `pkg/sandbox/` | 10 | 跨平台 OS 沙箱 | Sandbox, Landlock, Seatbelt, LowIL |
| `pkg/zim/` | 6 | ZIM 格式读写 | Reader, Writer, Codec |

---

## 4. 技术栈详解

### 4.1 核心框架

| 类别 | 技术 | 版本 | 用途 |
|------|------|------|------|
| Agent 框架 | tRPC-Agent-Go | v1.10.0 | Agent 编排、工具调用、会话管理 |
| MCP 协议 | tRPC-MCP-Go | v0.0.16 | Model Context Protocol 客户端/服务端 |
| A2A 协议 | tRPC-A2A-Go | v0.2.5 | Agent-to-Agent 通信 |
| 记忆引擎 | CortexDB | v2.25.0 | HNSW 向量 + FTS5 全文 + RDF 图谱 |
| 知识格式 | OKF | v0.1 | 开放知识格式 (Google 提案) |
| CLI 框架 | Cobra | v1.9.1 | 命令行界面 |
| 配置管理 | Viper | v1.20.1 | 配置加载、环境变量 |
| TUI 框架 | Bubble Tea | v1.3.10 | 终端用户界面 |
| TUI 组件 | Bubbles | v0.21.0 | TUI 组件库 |
| TUI 样式 | Lipgloss | v1.1.0 | 终端样式渲染 |

### 4.2 浏览器与自动化

| 类别 | 技术 | 版本 | 用途 |
|------|------|------|------|
| 浏览器驱动 | Rod | v0.116.2 | 无头 Chrome 控制 (主要后端) |
| 浏览器驱动 | Chromedp | v0.15.1 | 备用 CDP 客户端 |
| CDP 协议 | cdproto | - | Chrome DevTools Protocol |
| 反指纹 | UTLS | v1.5.0 | TLS 指纹伪造 |
| 机器人检测 | robotstxt | v1.1.2 | robots.txt 解析 |

### 4.3 数据存储

| 类别 | 技术 | 版本 | 用途 |
|------|------|------|------|
| 数据库 | SQLite (modernc) | v1.38.2 | 纯 Go SQLite，无需 CGO |
| 向量索引 | CortexDB HNSW | v2.25.0 | 分层导航小世界图 |
| 全文检索 | FTS5 | - | SQLite 全文搜索扩展 |
| 知识图谱 | RDF / SPARQL | - | 资源描述框架 + 查询语言 |
| 缓存 | VectorCache | - | 向量增量缓存 |
| Redis | go-redis | v9.12.1 | 可选会话/记忆后端 |

### 4.4 安全与沙箱

| 类别 | 技术 | 版本 | 用途 |
|------|------|------|------|
| JS 沙箱 | goja | - | 纯 Go JavaScript 解释器 |
| Linux 沙箱 | Landlock | - | Linux 内核安全模块 |
| macOS 沙箱 | Seatbelt | - | macOS 沙箱框架 |
| Windows 沙箱 | Low Integrity Level | - | Windows 低完整性级别 |
| 密码学 | x/crypto | v0.48.0 | Ed25519 / X25519 / ChaCha20 |
| HTTP 签名 | RFC 9421 | - | HTTP 消息签名标准 |

### 4.5 可观测性

| 类别 | 技术 | 版本 | 用途 |
|------|------|------|------|
| 追踪 | OpenTelemetry | v1.43.0 | 分布式追踪标准 |
| 指标 | OTLP Metrics | - | OpenTelemetry 指标协议 |
| 可观测平台 | Langfuse | - | LLM 应用可观测性 |
| 日志 | slog | - | Go 标准库结构化日志 |

### 4.6 其他关键依赖

| 类别 | 技术 | 版本 | 用途 |
|------|------|------|------|
| 压缩 | klauspost/compress | v1.18.6 | 高性能压缩 (zstd/gzip) |
| YAML | yaml.v3 | v3.0.1 | YAML 解析/序列化 |
| UUID | google/uuid | v1.6.0 | UUID 生成 |
| 飞书 SDK | oapi-sdk-go | v3.9.7 | 飞书开放平台 SDK |
| 腾讯云 COS | cos-go-sdk | v5 | 对象存储 (可选后端) |
| 中文分词 | gse | v1.0.2 | 中文分词 (FTS5 支持) |

---

## 5. CoreLoop 中央编排引擎

`internal/agent/` — 21 个文件

CoreLoop 是 Wukong 的核心编排引擎，负责协调所有子系统完成 Agent 交互循环。

### 5.1 架构概览

```go
type CoreLoop struct {
    agent          agent.Agent           // 底层 Agent (single/chain/parallel/...)
    runner         runner.Runner         // 执行运行器
    sessionService session.Service       // 会话服务
    memoryService  memory.Service        // 记忆服务
    factory        *provider.Factory     // LLM 工厂
    cfg            *config.WukongConfig  // 配置
    contextMgr     *ContextManager       // 上下文管理器
    security       *security.Guard       // 安全守卫
    recallStore    *recall.Store         // 召回存储
    cortexStore    *cortex.CortexStore   // CortexDB (可选)
    memoryFlow     *cortex.MemoryFlowService // 记忆流
    graphFlow      *cortex.GraphFlowService  // 知识图谱 (可选)
    closeFn        func() error          // 关闭函数
}
```

### 5.2 四阶段执行循环

#### Phase 1: Prepare — 上下文准备

**目标**: 在 LLM 调用前构建丰富的上下文环境

| 步骤 | 组件 | 功能 |
|------|------|------|
| 1 | ContextManager | 准备上下文优化策略 |
| 2 | MemoryFlow.IngestTurn | 记录用户消息到转录 |
| 3 | MemoryFlow.WakeUp | 3 层唤醒上下文生成 |
| 4 | Recall/Cortex.Search | 语义召回相关历史 |
| 5 | tRPC Memory.ReadMemories | 读取持久记忆 |
| 6 | OKF Injector | 注入知识索引 |
| 7 | GraphFlow | 可选: 图谱实体上下文 |
| 8 | 去重检测 | 30字符滑动窗口 + 60%重叠阈值 |

**记忆注入流水线**:

```
用户消息
    │
    v
MemoryFlow.IngestTurn (会话转录记录)
    │
    v
MemoryFlow.WakeUp (3 层上下文构建)
    ├── Identity Layer: 角色定义 + 身份
    ├── Compact Recall: 最近对话线索摘要
    └── Context Pack: 语义召回相关内容
    │
    v
Recall/Cortex.Search (TopK 相关历史)
    │
    v
tRPC Memory.ReadMemories (持久记忆事实)
    │
    v
去重检测 (30 字符滑动窗口, 60% 重叠阈值)
    │
    v
合并注入 → 用户消息前缀
```

#### Phase 2: Execute — 执行阶段

**目标**: 运行 LLM 推理 + 工具调用循环

| 组件 | 功能 |
|------|------|
| Runner | 管理 LLM 调用循环、工具执行、事件分发 |
| ToolSearch | TopK 工具自动过滤 (减少 token) |
| Guard | 安全检查 (权限/命令/文件) |
| TodoEnforcer | 确保待办任务完成 |
| Guardrail | Prompt 注入检测 |
| EvolutionTracker | 捕获执行轨迹 (事件驱动) |

**工具调用安全检查链** (在 `buildToolCallbacks` 中实现):

```
工具调用请求
    │
    ├─ CheckToolPermission (denylist/allowlist/mode)
    ├─ NeedsApproval (smart/manual 模式)
    ├─ ValidateCommand (命令执行工具)
    └─ CheckFilePath (文件访问工具: .wukongignore)
    │
    v
允许执行 / 拒绝
```

#### Phase 3: Finalize — 收尾阶段

**目标**: 持久化结果、更新记忆、触发后台任务

| 步骤 | 组件 | 功能 | 同步/异步 |
|------|------|------|----------|
| 1 | StoreMessage | 存储助手响应 | 同步 |
| 2 | MemoryFlow.IngestTurn | 记录助手响应 | 同步 |
| 3 | PromoteFacts | 事实提升到持久记忆 | 异步 (bgWg) |
| 4 | GraphFlow.AutoExtract | 实体/关系抽取 | 异步 (bgWg) |
| 5 | Evolution Record | 记录执行轨迹 | 异步 |
| 6 | ContextMgr.AfterRun | token 统计 + 优化 | 同步 |

#### Phase 4: Return — 返回阶段

返回最终响应文本，触发流式事件回调。

### 5.3 关键插件机制

#### EvolutionTracker

Runner 级别插件，事件驱动捕获执行轨迹：

```
事件流
    ├── BeforeAgent: 初始化状态
    │   └── evo_start_at, evo_llm_calls, evo_tool_call_count
    ├── OnEvent: 处理响应事件
    │   ├── 统计 LLM 调用次数
    │   ├── 统计工具调用次数
    │   └── 捕获工具调用详情 (名称、参数)
    └── AfterAgent: 记录到进化引擎
```

#### TodoEnforcer

确保所有待办任务完成后才允许 Agent 输出最终答案。

#### ToolSearch

自动 TopK 工具过滤，减少每次 LLM 调用的工具列表长度。

#### Guardrail

Prompt 注入检测，使用 review 模式检查用户输入。

### 5.4 优雅关闭序列

```
Close()
    │
    ├─ 等待 runWg (运行中的 RunStream)
    ├─ 等待 bgWg (后台 goroutine)
    │
    ├─ 1. Close Runner (停止活动运行)
    ├─ 2. Close Evolution Engine (停止分析 worker)
    ├─ 3. Close Memory Service (等待提取任务完成)
    ├─ 4. Close Session Service (停止 summary worker)
    ├─ 4b. Close GraphFlow (停止图谱引擎)
    ├─ 5. Flush Telemetry (刷新遥测数据)
    └─ 6. Close DB Pool (最后关闭数据库连接池)
```

> **关键**: 数据库连接池必须最后关闭，确保所有子系统的写入都已刷新。

---

## 6. 多 Agent 编排系统

### 6.1 10 种编排模式

| 模式 | 拓扑结构 | 底层实现 | 适用场景 |
|------|---------|---------|---------|
| **single** | 单体 | LLMAgent | 日常对话、简单任务 |
| **chain** | planner → executor → reviewer | ChainAgent | 流水线任务、代码评审 |
| **parallel** | 3 视角并发 | ParallelAgent | 多角度分析、头脑风暴 |
| **cycle** | planner ↔ executor (迭代) | CycleAgent | 自我迭代、优化任务 |
| **graph** | 条件 DAG | GraphAgent | 复杂决策、多分支流程 |
| **team_coordinator** | Leader 委派 | TeamAgent | 团队协作、任务分解 |
| **team_swarm** | 自动 transfer | TeamAgent(swarm) | 自主委派、动态协作 |
| **claude_code** | CLI 进程 | exec.Cmd | 本地 Claude Code |
| **codex** | CLI 进程 | exec.Cmd | 本地 Codex |
| **dify** | HTTP API | HTTP Client | 低代码平台集成 |

### 6.2 WorkflowBuilder

`internal/agent/workflow.go`

根据配置动态构建编排模式的工厂，支持运行时模式切换。

### 6.3 Recipe 系统

`internal/agent/recipe.go` — 14 种 YAML Recipe

Recipe 是基于 YAML 定义的子 Agent，可作为工具被主 Agent 调用：

| 类型 | 说明 |
|------|------|
| single | 单 Agent Recipe |
| chain | 链式 Recipe |
| parallel | 并行 Recipe |
| team | 团队 Recipe |
| ... | ... |

**特性**:
- 热重载: 文件变更自动生效 (fsnotify 监听)
- 依赖注入: 共享工具集、配置
- 版本管理: Evolution 引擎自动优化

---

## 7. 双引擎三层记忆系统

### 7.1 记忆层级架构

```
┌─────────────────────────────────────────────────────────────┐
│                     短期记忆 (Short-term)                    │
│  MemoryFlow: 会话转录 + 3 层唤醒 + OKF 注入                    │
│  存储: CortexDB 内存表                                         │
│  生命周期: 会话内                                               │
├─────────────────────────────────────────────────────────────┤
│                     中期记忆 (Mid-term)                       │
│  CortexStore: HNSW 向量 + FTS5 全文                            │
│  存储: CortexDB 持久化                                          │
│  生命周期: 跨会话，可被召回                                     │
├─────────────────────────────────────────────────────────────┤
│                     长期记忆 (Long-term)                      │
│  tRPC Memory: AutoExtract + SmartCleanup                       │
│  存储: SQLite wukong.db                                        │
│  生命周期: 永久，直到清理策略触发                                │
├─────────────────────────────────────────────────────────────┤
│                     结构化记忆 (Graph)                         │
│  GraphFlow: 实体抽取 → RDF 图谱 → SPARQL                        │
│  存储: CortexDB RDF                                            │
│  生命周期: 永久                                                 │
└─────────────────────────────────────────────────────────────┘
```

### 7.2 MemoryFlow (短期记忆)

`internal/cortex/memoryflow.go`

**核心功能**:
- **IngestTurn**: 记录每一轮对话到转录
- **WakeUp**: 3 层上下文唤醒
  - Identity: 角色定义、身份信息
  - Compact Recall: 最近对话摘要
  - Context Pack: 语义相关上下文
- **PromoteFacts**: 将重要事实提升到长期记忆

### 7.3 CortexStore (中期记忆)

`internal/cortex/store.go`

**双索引架构**:
- **HNSW 向量索引**: 语义相似度搜索 (需配置 embedding)
- **FTS5 全文索引**: 关键词搜索 (始终可用)

**Lexical Store**:
- 使用共享 *sql.DB 连接
- 避免多连接导致的 SQLite 事务冲突
- 作为权威数据源，向量索引是增量缓存

### 7.4 tRPC Memory (长期记忆)

`trpc.group/trpc-go/trpc-agent-go/memory`

**核心机制**:
- **AutoExtract**: 自动从对话中提取事实
- **SmartCleanup**: 智能容量管理
  - 70% 新鲜度评分 + 30% 长度评分
  - 80% 阈值触发清理
  - 清理到 60% 容量
- **Reference Tracking**: 记忆使用频率追踪

### 7.5 GraphFlow (结构化记忆)

`internal/cortex/graphflow.go`

**工作流程**:
```
对话转录
    │
    v
Entity/Relation Extractor (LLM 驱动)
    │
    v
RDF Triple 生成
    │
    v
CortexDB RDF Store
    │
    v
SPARQL 查询 (供 Agent 使用)
```

### 7.6 记忆去重策略

- **算法**: 30 字符滑动窗口
- **阈值**: 60% 重叠即视为重复
- **范围**: WakeUp 上下文 vs 持久记忆
- **目的**: 避免冗余信息重复注入

---

## 8. Evolution 技能进化引擎

`internal/evolution/` — 7 个文件

### 8.1 架构总览

```
Evolution Engine
    │
    ├── EvolutionTracker (agent/evolution_tracker.go)
    │   └── 事件监听捕获执行轨迹 (Runner Plugin)
    │
    ├── EvolutionEngine (engine.go)
    │   ├── 异步分析通道 (缓冲 64)
    │   ├── 冷却周期检查
    │   ├── 每日补丁限制
    │   └── 后台分析 Worker
    │
    ├── EvolutionAnalyzer (analyzer.go)
    │   ├── LLM 分析执行轨迹
    │   ├── 生成补丁建议 (PatchSuggestion)
    │   ├── 置信度过滤
    │   └── 补丁大小限制
    │
    ├── EvolutionPatcher (patcher.go)
    │   ├── 版本备份 (SKILL.vNNN.md)
    │   ├── 补丁去重 (哈希匹配)
    │   ├── 补丁数量限制 (最多 5 个 section)
    │   ├── 并发安全 (sync.Mutex)
    │   ├── OKF 日志更新
    │   └── 版本清理
    │
    └── VersionStore (store.go)
        ├── SQLite 持久化
        ├── skill_versions 表
        ├── evolution_history 表
        └── SmartCleanup
```

### 8.2 ExecutionTrace 执行轨迹

```go
type ExecutionTrace struct {
    SkillName     string           // 技能名称
    SkillFile     string           // SKILL.md 路径
    SessionID     string           // 会话 ID
    UserID        string           // 用户 ID
    StartTime     time.Time        // 开始时间
    EndTime       time.Time        // 结束时间
    Duration      time.Duration    // 执行时长
    ToolCalls     []ToolCallRecord // 工具调用序列
    LLMCalls      int              // LLM 调用次数
    Error         string           // 终端错误
    ErrorCount    int              // 错误总数
    FinalOutput   string           // 最终输出
    OutputLength  int              // 输出长度
    Success       bool             // 是否成功
    QualityScore  float64          // 质量评分 (0.0-1.0)
}
```

### 8.3 补丁去重机制

```
patchHash(reason + problem_type)
    │
    v
查找现有补丁标记 (<!-- EVOLUTION PATCH {hash} -->)
    │
    ├── 存在: 替换旧补丁内容
    └── 不存在: 追加新补丁
    │
    v
补丁数量检查 (最多 5 个 section)
    │
    └── 超出: 删除最旧的补丁
```

### 8.4 OKF 日志系统

**Markdown 格式 (log.md)**:
```markdown
# Change Log

## [v2] 2026-07-11 15:30
- **Type**: missing_prerequisite
- **Reason**: Skill forgot to check file existence
- **Confidence**: 0.85
```

**JSON 格式 (log.json)** — 外部系统消费:
```json
{
  "skill_name": "code-reviewer",
  "entries": [
    {
      "version": 2,
      "timestamp": "2026-07-11T15:30:00Z",
      "type": "missing_prerequisite",
      "reason": "Skill forgot to check file existence",
      "confidence": 0.85,
      "patch_hash": "abc12345"
    }
  ]
}
```

### 8.5 并发安全模型

EvolutionPatcher 使用 `sync.Mutex` 保护三类写入操作：
1. SKILL.md 文件写入
2. OKF 日志更新 (log.md + log.json)
3. 版本记录表更新

---

## 9. OKF 知识格式系统

### 9.1 OKF Bundle 结构

```
OKF Bundle (目录)
    ├── index.md          # 渐进探索入口
    ├── log.md            # 变更历史 (Markdown)
    ├── log.json          # 变更历史 (JSON)
    └── concepts/
        ├── concept-a.md  # 概念文件
        ├── concept-b.md
        └── ...
```

**概念文件格式**:
```markdown
---
type: concept
title: 概念标题
description: 简短描述
tags: [tag1, tag2]
created: 2026-01-01
updated: 2026-01-15
---

# 概念标题

正文内容...

## 相关概念
- [[concept-a]]
- [[concept-b]]
```

### 9.2 OKF 数据流

```
数据源 (DDL / 目录 / 对话)
    │
    v
EnrichmentAgent (LLM 驱动)
    │
    v
OKF Bundle (concepts/*.md)
    │
    ├── index.md → KnowledgeIndexInjector
    │                 → MemoryFlow.WakeUp
    │                 → Agent 上下文
    │
    ├── log.md → Evolution 变更追踪
    ├── log.json → Evolution JSON 导出
    └── CatalogEntry → ARD 联邦发现
```

### 9.3 OKF 集成点

| 模块 | 文件 | 集成方式 |
|------|------|---------|
| OKF 核心 | internal/okf/ | Bundle 加载/写入、概念解析 |
| Skill | internal/skill/ | SKILL.md 添加 type: skill 字段 |
| Knowledge | internal/knowledge/ | RAG 知识库与 OKF Bundle 互操作 |
| Cortex | internal/cortex/ | OKF index.md 注入 MemoryFlow |
| Evolution | internal/evolution/ | log.md + log.json 变更追踪 |
| ARD | internal/ard/ | OKF Bundle 注册为 CatalogEntry |

---

## 10. ANP Agent 互通协议栈

### 10.1 协议分层架构

```
┌─────────────────────────────────────────────────┐
│  Bridge Layer: ANPAdapter                       │
│  JSON-RPC 2.0 ↔ A2A 协议桥接                     │
├─────────────────────────────────────────────────┤
│  Security Layer: E2EE + HTTP Sign               │
│  X25519 + ChaCha20-Poly1305 / RFC 9421          │
├─────────────────────────────────────────────────┤
│  Negotiation Layer: Meta-Protocol               │
│  JSON-RPC 2.0 能力协商 (capabilities.negotiate)  │
├─────────────────────────────────────────────────┤
│  Discovery Layer: ADP                           │
│  /.well-known/agent-descriptions                 │
├─────────────────────────────────────────────────┤
│  Identity Layer: DID                            │
│  did:wba (Ed25519 签名 + X25519 密钥交换)        │
└─────────────────────────────────────────────────┘
```

### 10.2 核心模块

| 模块 | 文件 | 功能 |
|------|------|------|
| DIDManager | ard/did.go | did:wba 身份管理：Ed25519 签名 + X25519 密钥交换 |
| ADPGenerator | ard/adp.go | ADP 文档生成：Agent Card + 接口描述 |
| ANPDiscovery | ard/anp_discovery.go | /.well-known/agent-descriptions 发现端点 |
| HTTPSign | ard/http_sign.go | RFC 9421 HTTP 消息签名 |
| MetaProtocol | summon/meta_protocol.go | JSON-RPC 2.0 引擎：capabilities.negotiate |
| E2EEMessenger | summon/e2ee.go | X25519 + ChaCha20-Poly1305 端到端加密 |
| ANPAdapter | summon/anp_adapter.go | ANP JSON-RPC 2.0 → A2A 协议桥接 |

---

## 11. ARD 双向发现系统

`internal/ard/` — 22 个文件

### 11.1 核心概念

- **ai-catalog.json**: 能力清单，托管在 `/.well-known/ai-catalog.json`
- **URN 标识符**: `urn:air:<publisher>:<namespace>:<name>`
- **Search API**: POST /search 语义资源发现
- **Media Types**:
  - `application/a2a-agent-card+json`
  - `application/mcp-server-card+json`
  - `application/ai-catalog+json`
  - `application/ai-registry+json`

### 11.2 CatalogEntry 结构

```go
type CatalogEntry struct {
    Identifier           string            // URN 格式
    DisplayName          string            // 人类可读名称
    Type                 string            // IANA Media Type
    URL                  string            // 远程引用
    Data                 json.RawMessage   // 内嵌文档
    Description          string            // 描述
    Tags                 []string          // 标签
    Capabilities         []string          // 工具/技能名称
    RepresentativeQueries []string         // 2-5 个示例查询
    Version              string            // 版本
    UpdatedAt            string            // ISO 8601
    TrustManifest        *TrustManifest    // 信任清单
}
```

### 11.3 联邦搜索架构

```
本地 Agent
    │
    ├── 本地 Catalog (注册表)
    ├── 联邦搜索 → 已知 Registry Server
    │   └── 分布式搜索多个节点
    └── 直接发现 → /.well-known/ai-catalog.json
```

---

## 12. Gateway 多平台消息网关

`internal/gateway/` — 11 个文件

### 12.1 插件式 Channel 架构

```
┌──────────────┐  ┌──────────────┐
│ Feishu       │  │ WeCom (TODO) │   Channel 适配器
│ (入站传输)    │  │              │   (各自持有传输)
└──────┬───────┘  └──────┬───────┘
       │                 │
       └────────┬────────┘
                v
       ┌────────────────┐
       │ GatewayServer  │  transport-agnostic 消息流水线
       └───────┬────────┘
               │
    ┌──────────┴──────────┐
    │ Dedup | RateLimiter │  防护层
    └──────────┬──────────┘
               │
    ┌──────────┴──────────┐
    │ GatewaySessionStore │  身份/会话映射
    └──────────┬──────────┘
               │
    ┌──────────┴──────────┐
    │   agent.CoreLoop    │  Agent 执行
    └─────────────────────┘
```

### 12.2 9 步消息流水线

| 步骤 | 组件 | 功能 |
|------|------|------|
| 1 | VerifyRequest | 签名验证 |
| 2 | PlatformEvent | URL 验证 (echostr) |
| 3 | ParseMessage | 平台消息 → 统一格式 |
| 4 | Dedup | 消息去重 (MessageID + TTL) |
| 5 | BuildUserID | 身份映射 |
| 6 | RateLimiter | 滑动窗口限流 + 并发控制 |
| 7 | SessionStore | 会话持久化 |
| 8 | CoreLoop.Run | Agent 执行 |
| 9 | SendReply | 回复/流式推送 |

### 12.3 飞书通道

`internal/gateway/feishu/` — 5 个文件

| 文件 | 功能 |
|------|------|
| channel.go | 飞书 Channel 适配器 |
| message.go | 消息解析与格式化 |
| sender.go | 消息发送器 |
| message_test.go | 消息测试 |
| sender_test.go | 发送器测试 |

---

## 13. Extension 扩展系统

`internal/extension/` — 25 个文件

### 13.1 扩展类型

| 类型 | 说明 | 示例 |
|------|------|------|
| builtin | 内置扩展 (Go 实现) | developer, memory, browser, apps |
| external | 外部 MCP 服务器 | 自定义 MCP 服务 |
| mcp_broker | MCP Broker 批量管理 | 多个外部 MCP 统一暴露 |

### 13.2 13 个内置扩展

| 扩展 | 功能 |
|------|------|
| developer | 开发工具集 (文件/命令/搜索) |
| memory | 记忆管理工具 |
| browser | 浏览器工具 |
| apps | 应用管理工具 |
| ard | ARD 发现工具 |
| cortex | CortexDB 工具 |
| code mode | Code Mode 执行 |
| aggregate_search | 聚合搜索 |
| google / bing / searxng / tavily | 搜索引擎集成 |
| topofmind | 置顶指令 |
| tutorial | 教程引导 |
| auto_visualiser | 自动可视化 |

### 13.3 MCP Broker

当启用 MCP Broker 时，外部 MCP 服务器通过 4 个工具统一暴露：
- `mcp_list_servers`: 列出所有 MCP 服务器
- `mcp_list_tools`: 列出指定服务器的工具
- `mcp_inspect_tools`: 查看工具详情
- `mcp_call`: 调用 MCP 工具

### 13.4 ARD 集成

外部 MCP 服务器连接时自动注册到 ARD Catalog，支持联邦发现。

---

## 14. Security 五层安全防御

### 14.1 纵深防御架构

```
Layer 5: Guard 权限控制
  ├── auto / smart / manual / chat_only 四种模式
  ├── denylist / allowlist 细粒度控制
  ├── 危险命令拦截
  ├── Prompt 注入检测 (Guardrail)
  └── HITL 人工审批

Layer 4: goja JS 沙箱
  ├── API 白名单
  ├── 128MB 内存限制
  ├── 5 并发限制
  ├── ReDoS 防护
  └── 1MB 代码长度限制

Layer 3: OS 沙箱
  ├── Linux: Landlock LSM
  ├── macOS: Seatbelt Framework
  └── Windows: Low Integrity Level

Layer 2: .wukongignore
  └── gitignore 兼容文件黑名单

Layer 1: OS 权限
  ├── 非 root 运行
  └── ulimit 资源限制
```

### 14.2 Guard 权限模式

| 模式 | 行为 |
|------|------|
| auto | 自动批准所有工具调用 |
| smart | 高风险操作需要用户批准 |
| manual | 所有工具调用需要用户批准 |
| chat_only | 禁止所有工具调用 |

### 14.3 文件访问控制

通过 `.wukongignore` 文件实现，支持 gitignore 语法：
- 在工具调用前检查文件路径
- 支持 `IgnoreMatcher` 模式匹配
- 与 Guard 深度集成

---

## 15. Browser 浏览器引擎

`internal/browser/` — 28 个文件

### 15.1 后端架构

```
Browser Backend
    ├── Rod (默认)
    │   └── rodbackend.New()
    │       ├── Headless 模式
    │       ├── Worker 池 (标签池)
    │       ├── Stealth 脚本注入
    │       ├── Proxy 支持
    │       └── DownloadBehavior
    │
    └── Chromedp (备用)
        └── chromedp.New()
```

### 15.2 反反爬体系 (10 层)

| 层级 | 组件 | 功能 |
|------|------|------|
| 1 | Stealth | 反检测脚本注入 |
| 2 | Preflight | 请求前探测 |
| 3 | Antibot 5级升级 | passive → gentle → moderate → aggressive → extreme |
| 4 | cf_clearance | Cloudflare 挑战绕过 |
| 5 | UA 池 | 161 个 User-Agent 轮换 |
| 6 | sec-ch-ua | 客户端提示伪造 |
| 7 | Referer 伪造 | 来源页欺骗 |
| 8 | ErrNotHTML 路由 | 非 HTML 响应特殊处理 |
| 9 | Settle | 网络空闲等待 |
| 10 | Proxy Pool | 智能代理池 |

### 15.3 Antibot 升级策略

`internal/browser/antibot/escalator.go`

```
passive → gentle → moderate → aggressive → extreme
    ↑           ↑           ↑            ↑
  检测到     重试失败    重试失败      重试失败
  反爬特征    1 次       2 次          3 次
```

**探测类型** (`internal/browser/antibot/prober/`):
- `http_header_probe`: HTTP 头分析
- `js_challenge_probe`: JS 挑战检测
- `rate_limit_probe`: 速率限制检测
- `robots_probe`: robots.txt 检查
- `waf_probe`: WAF 检测

### 15.4 资源下载策略

**4 层回退机制**:
```
Layer 1: 直接导航 (img-on-referer)
    └── 失败 → Layer 2
Layer 2: Network.loadNetworkResource (CDP)
    └── 失败 → Layer 3
Layer 3: img 标签加载
    └── 失败 → Layer 4
Layer 4: fetch API
    └── 失败 → 彻底失败
```

### 15.5 代理池

`internal/browser/proxy_pool.go`

- 多代理 URL 管理
- 健康检查 (30 秒间隔)
- 自动故障转移
- 轮询/随机选择策略

---

## 16. Apps 应用管理系统

`internal/apps/` — 31 个文件

### 16.1 应用类型

| 类型 | 说明 |
|------|------|
| custom | 用户手动创建的应用 |
| cloned | 通过网站克隆创建的应用 |
| imported | 从外部导入的应用 |

### 16.2 网站克隆引擎

`internal/apps/clone/` — 18 个文件

**核心组件**:

| 组件 | 文件 | 功能 |
|------|------|------|
| EnhancedCloner | enhanced_cloner.go | 主克隆引擎 |
| AssetDownloader | asset.go | 资源下载器 (HTTP + Browser 双轨) |
| CSSRewriter | css.go | CSS URL 重写 |
| HTMLRewriter | rewrite.go | HTML 重写 + 链接发现 |
| URLUtils | urlx.go | URL 处理 + 分页支持 |
| Frontier | frontier.go | 爬取队列管理 + 断点续抓 |
| DedupEngine | dedup.go | 内容去重 (SHA-256 + 硬链接) |
| Session | session.go | 克隆会话管理 |
| RobotsChecker | robots.go | robots.txt 遵守 |
| CacheManager | cache.go | ETag/Last-Modified 缓存 |

**6 种分页方式支持**:
1. 查询参数式: `?Page=2` → `index_page_2.html`
2. 路径式: `/page/2/` → `page/2/index.html`
3. Offset/Limit: `?offset=50&limit=25` → `index_offset_50_25.html`
4. Cursor/Keyset: `?cursor=abc` → `index_cursor_e861b2.html`
5. Seek: `?after=2024-01-01` → `index_seek_xxx.html`
6. Token: `?pageToken=xxx` → `index_token_xxx.html`

**克隆流水线**:
```
Seed URL
    │
    v
Frontier (BFS/DFS + 去重)
    │
    v
Browser Render (Chrome + Stealth + Settle)
    │
    v
DOM Sanitize (清理脚本/事件)
    │
    v
Asset Discovery (CSS/JS/图片/字体)
    │
    v
Asset Download (HTTP → Browser 回退)
    │
    ├── CSS URL Rewriting
    ├── JS 本地化
    └── Image 下载
    │
    v
HTML Rewriting (链接 → 本地路径)
    │
    v
Content Dedup (SHA-256 + 硬链接)
    │
    v
保存到本地文件系统
```

### 16.3 ZIM 打包系统

`internal/apps/pack/` + `pkg/zim/`

**ZIM v6 格式特性**:
- Kiwix 兼容
- zstd 压缩 (编码 5)
- 元数据 + 图标 + 计数器
- 增量集群缓存

**核心文件**:
- `packer.go`: 打包器主逻辑
- `zim.go`: ZIM 写入器
- `pkg/zim/reader.go`: ZIM 读取器
- `pkg/zim/codec.go`: 编解码器

### 16.4 预览服务器

`internal/apps/server/server.go`

- 本地 HTTP 服务器预览克隆应用
- 自动处理静态文件路由
- 支持实时查看克隆效果

### 16.5 MCP Apps 桥接

`internal/apps/mcpapps/`

- 将克隆应用暴露为 MCP 资源
- 支持 MCP 客户端访问应用内容
- Bridge / Host / Manager / Resource 四层架构

---

## 17. 配置系统

`internal/config/` — 9 个文件

### 17.1 7 级加载优先级

```
1. CLI 参数 (--provider, --model, --temperature, ...)
2. 环境变量 (WUKONG_ 前缀)
3. --config CLI 指定文件
4. ./config.yaml (当前目录)
5. ~/.config/wukong/config.yaml
6. /etc/wukong/config.yaml (非 Windows)
7. 内置默认值 (internal/config/defaults.go)
```

### 17.2 配置代码结构

| 文件 | 职责 |
|------|------|
| config.go | 根结构体 WukongConfig + Loader + 查询方法 |
| types_agent.go | Agent / Security 配置结构体 |
| types_provider.go | Provider / Extension / ToolPermission 配置 |
| types_storage.go | Session / Memory / Todo / Recall 存储配置 |
| types_cortex.go | CortexDB / MemoryFlow / GraphFlow / ImportFlow |
| types_browser.go | Browser / BrowserSearch 配置 |
| types_features.go | 功能特性配置 |
| types_orchestration.go | ARD / Summon / ANP / Skill / Evolution / ... |
| types_observability.go | 可观测性配置 |
| types_server.go | 服务端点配置 |
| types_apps.go | Apps (克隆/打包) 配置 |
| defaults.go | 内置默认值 (13 个方法) |
| validate.go | Validate() 致命错误 + Warnings() 非致命警告 |

### 17.3 15 组配置 (A-O)

| 分组 | 内容 |
|------|------|
| A | 全局设置 (default_provider, log_level, ...) |
| B | Providers — LLM 后端 (7 种) |
| C | Agent — 核心行为 & 生成参数 |
| D | Security — 工具执行安全 & 访问控制 |
| E | Storage — SQLite 持久化层 |
| F | CortexDB Memory Stack — Vector + FTS5 + KG |
| G | Context Management — Token 优化 & Revision |
| H | Feature Tools — Browser / Visualiser / ... |
| I | Extensions — MCP 外部服务器 |
| J | Service Endpoints — 多协议服务端口 |
| K | Agent-to-Agent Communication — Summon/ANP/ARD |
| L | Knowledge & Skill Management — Knowledge/OKF/Skill |
| M | Agent Orchestration — Workflow |
| N | Observability & Evaluation — Telemetry/Eval |
| O | Project Directory |

### 17.4 环境变量展开

`${ENV_VAR}` 和 `${VAR:-default}` 语法，运行时自动展开。

覆盖 15 类敏感字段:
- Providers: api_key, base_url, model
- A2A Remotes: api_key, jwt_secret, oauth_client_secret
- Gateway Feishu: app_secret, encrypt_key, verification_token
- CortexDB: embedding_api_key, embedding_base_url, embedding_model
- MemoryFlow: planner_model, extractor_model
- GraphFlow: extractor_model
- Dify: api_secret
- Observability (Langfuse): public_key, secret_key
- Artifact (COS): cos_secret_id, cos_secret_key
- ACP Server: api_key
- Session: redis_url
- Browser Search: 各搜索引擎的 url/api_key

### 17.5 配置验证

**致命错误检查** (Validate):
- default_provider 存在性
- provider type 有效性
- temperature 范围 [0.0, 2.0]
- max_tokens, max_llm_calls, max_tool_iterations >= 0
- permission_mode 有效性
- memory.cleanup 阈值有效性
- evolution.min_confidence 范围
- workflow.mode 有效性 (10 种模式)
- anp.port 范围
- session/memory/recall backend 有效性
- ... 等等

**非致命警告** (Warnings):
- 无 providers 配置
- memory.auto_extract 启用但无 default_provider
- cortex.enabled 但无 embedding_model
- okf.enabled 但 bundle_dir 为空
- anp.enabled 但 did_domain 为空
- gateway.enabled 但无 channel 激活
- ... 等等

---

## 18. 服务端点

### 18.1 6 协议端点

| 协议 | 端口 | 用途 | 模块 |
|------|------|------|------|
| Gateway | 9093 | 多平台消息通道 | internal/gateway/ |
| A2A | 9090 | Agent-to-Agent 通信 | internal/summon/a2a.go |
| ACP | 9091 | Agent Client Protocol | internal/server/acp.go |
| AG-UI SSE | 8080 | Web UI 实时对话 | internal/server/agui.go |
| ACP MCP | 3400 | 跨协议工具桥接 | internal/server/acp.go |
| ANP | 9092 | DID + 能力协商 + E2EE | internal/ard/server.go |

### 18.2 服务架构

```
                    ┌─────────────────┐
                    │   CLI / TUI     │
                    └────────┬────────┘
                             │
┌────────────────────────────┼────────────────────────────┐
│                            │                            │
│  ┌──────────┐    ┌─────────▼─────────┐    ┌──────────┐ │
│  │ A2A:9090 │    │   Gateway:9093    │    │ ACP:9091 │ │
│  └────┬─────┘    └─────────┬─────────┘    └────┬─────┘ │
│       │                    │                   │       │
│  ┌────▼─────┐         ┌────▼────┐        ┌────▼─────┐ │
│  │  Summon  │         │  Feishu  │        │  ACP MCP │ │
│  │ (A2A)    │         │ Channel  │        │ (Bridge) │ │
│  └────┬─────┘         └────┬─────┘        └────┬─────┘ │
│       │                    │                   │       │
│       └────────────────────┼───────────────────┘       │
│                            │                           │
│  ┌──────────┐    ┌─────────▼─────────┐    ┌──────────┐ │
│  │ ANP:9092 │    │    CoreLoop       │    │AG-UI:8080│ │
│  └──────────┘    │   (中央引擎)       │    └──────────┘ │
│                  └───────────────────┘                 │
│                                                          │
│  ┌──────────┐    ┌───────────────────┐    ┌──────────┐ │
│  │ Evolution│    │  Memory / Cortex  │    │  ARD     │ │
│  │  Engine   │    │   (双引擎三层)    │    │ Registry │ │
│  └──────────┘    └───────────────────┘    └──────────┘ │
└──────────────────────────────────────────────────────────┘
```

---

## 19. 架构设计决策 (ADRs)

### 核心决策

| # | 决策 | 理由 | 权衡 |
|---|------|------|------|
| 1 | SQLite WAL 单文件部署 | 简化部署、零配置 | 并发写入性能限制 |
| 2 | 双引擎记忆架构 | tRPC 存事实，CortexDB 存语义/图谱 | 双系统同步复杂度 |
| 3 | 轻量模型分工 | 主模型对话，轻量模型后台提取 | 需配置额外模型 |
| 4 | CoreLoop 依赖注入 | 所有子系统可替换、可测试 | 初始化代码复杂 |
| 5 | YAML Recipe + 热重载 | 文件变更即生效 | 运行时错误风险 |
| 6 | HITL 融入编排循环 | 决策点原生暂停 | 增加交互复杂度 |
| 7 | SmartCleanup 容量淘汰 | 70% 新鲜度 + 30% 长度 | 可能误删重要记忆 |
| 8 | ACP + AG-UI 双协议 | ACP 客户端，AG-UI 浏览器 | 双端维护成本 |
| 9 | MCP Broker 批量管理 | 外部 MCP 统一暴露 | 增加调用层级 |
| 10 | goja 5 层 JS 沙箱 | API 白名单 + 内存 + 并发 + ReDoS + 长度 | 性能开销 |
| 11 | OS 级跨平台沙箱 | Landlock / Seatbelt / LowIL | 各平台实现差异 |
| 12 | ARD 双向发现 | 联邦搜索 + RegistryServer | 发现延迟 |
| 13 | Evolution 版本管理 | 每补丁保留版本备份 | 存储占用增长 |
| 14 | Chrome 真实渲染克隆引擎 | 完美还原动态页面 | 速度慢、资源消耗大 |
| 15 | 浏览器标签池复用 | 单进程多 Tab，信号量控制 | 状态隔离问题 |
| 16 | 配置代码按职责拆分 | types / defaults / validate 分离 | 文件数量多 |
| 17 | 采用 OKF v0.1 知识标准 | 厂商中立、Git 友好、渐进式探索 | 规范仍在演进 |
| 18 | 实现 ANP 协议栈 | DID 身份 + 能力协商 + E2EE | 协议复杂度高 |
| 19 | Gateway 插件式 Channel 架构 | 统一入口 + 中间件栈 | 新增平台需适配 |
| 20 | EvolutionTracker 事件驱动 | 不侵入主循环 | 异步数据可能延迟 |
| 21 | 补丁去重与数量限制 | 防止 SKILL.md 无限增长 | 可能丢失历史优化 |
| 22 | OKF 日志双格式输出 | Markdown 人读，JSON 机读 | 双份维护成本 |
| 23 | 进化引擎并发安全 | Mutex 保护关键写入 | 写入串行化 |
| 24 | 4 层资源下载回退 | 提高成功率（HTTP → CDP → img → fetch） | 重试耗时 |

---

## 20. 数据流与生命周期

### 20.1 完整会话生命周期

```
用户启动会话
    │
    ├─ 加载配置 (7 级优先级)
    ├─ 初始化 DatabasePool (SQLite WAL)
    ├─ 初始化 ProviderFactory
    ├─ 初始化 ExtensionManager
    ├─ 初始化 Session/Memory/Recall 服务
    ├─ 初始化 CortexDB (可选)
    ├─ 初始化 EvolutionEngine (可选)
    ├─ 创建 CoreLoop (依赖注入)
    │
    ├─ 用户消息到达
    │   ├─ CoreLoop.Run()
    │   │   ├─ Phase 1: Prepare
    │   │   ├─ Phase 2: Execute
    │   │   ├─ Phase 3: Finalize
    │   │   └─ Phase 4: Return
    │   └─ 返回响应
    │
    ├─ (循环) 更多消息...
    │
    └─ 用户结束会话
        ├─ 等待后台任务完成 (bgWg)
        ├─ 关闭 CoreLoop
        ├─ 关闭 EvolutionEngine
        ├─ 关闭 Memory / Session 服务
        ├─ 刷新遥测数据
        └─ 关闭 DatabasePool
```

### 20.2 克隆任务生命周期

```
开始克隆
    │
    ├─ 解析配置 & 选项
    ├─ 初始化浏览器后端 (Rod/Chromedp)
    ├─ 创建 Frontier (BFS/DFS 队列)
    ├─ 加载/创建 Session 状态
    ├─ 检查 robots.txt
    │
    ├─ Worker Pool 启动
    │   ├─ 从 Frontier 取 URL
    │   ├─ 浏览器渲染页面
    │   ├─ Settle 等待网络空闲
    │   ├─ 提取页面 HTML
    │   ├─ 发现资源 (CSS/JS/图片)
    │   ├─ 发现新链接 → 加入 Frontier
    │   ├─ 下载资源 (4 层回退)
    │   ├─ 重写 HTML/CSS URL
    │   ├─ 内容去重检查
    │   └─ 保存到本地
    │
    ├─ Frontier 为空？
    │   ├─ 是 → 完成
    │   └─ 否 → 继续
    │
    └─ 完成
        ├─ 保存 Session 状态
        ├─ 生成统计报告
        └─ 关闭浏览器池
```

---

## 附录

### A. 相关文档

| 文档 | 说明 |
|------|------|
| [README.md](../README.md) | 项目主页 |
| [CONFIG.md](./CONFIG.md) | 配置参考手册 |
| [CLI_TUI.md](./CLI_TUI.md) | CLI & TUI 架构 |
| [CLONE_GUIDE.md](./CLONE_GUIDE.md) | 网站克隆引擎技术指南 |
| [ANTIBOT_GUIDE.md](./ANTIBOT_GUIDE.md) | 反反爬技术详解 |
| [MEMORY_ARCHITECTURE.md](./MEMORY_ARCHITECTURE.md) | 记忆系统架构详解 |

### B. 外部资源

- [tRPC-Agent-Go 文档](https://trpc.group/)
- [CortexDB GitHub](https://github.com/liliang-cn/cortexdb)
- [OKF 规范](https://github.com/google/open-knowledge-format)
- [RFC 9421 HTTP 消息签名](https://www.rfc-editor.org/rfc/rfc9421)
