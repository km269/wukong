# Wukong — 记忆优先、编排驱动的 AI Agent 平台

> **版本**: v0.6.0 | **Go**: 1.26 | **源文件**: 119 `.go` + 31 `_test.go` | **直接依赖**: 30 | **许可证**: GNU AGPL-3.0
>
> 基于 [tRPC-Agent-Go v1.10.0](https://github.com/trpc-group/trpc-agent-go) · [tRPC-MCP-Go v0.0.16](https://github.com/trpc-group/trpc-mcp-go) · [tRPC-A2A-Go v0.2.5](https://github.com/trpc-group/trpc-a2a-go) · [CortexDB v2.25.0](https://github.com/liliang-cn/cortexdb)

---

## 目录

1. [架构哲学](#1-架构哲学)
2. [核心优势](#2-核心优势)
3. [系统概览](#3-系统概览)
4. [核心特性](#4-核心特性)
5. [快速开始](#5-快速开始)
6. [技术栈](#6-技术栈)
7. [项目结构](#7-项目结构)
8. [文档索引](#8-文档索引)

---

## 1. 架构哲学

Wukong 的设计围绕四条核心哲学展开：

### 1.1 记忆优先（Memory-First）

> "一个没有记忆的 Agent 只是一次性的对话工具。"

当前主流 Agent 框架将对话上下文视为**瞬态**——每次新会话都从头开始。Wukong 的核心信念是：Agent 的真正智能来源于跨会话的**知识积累**。为此，Wukong 构建了业界领先的**双引擎记忆系统**：

- **tRPC Memory**：结构化的键值记忆，存储用户偏好、事实、关系
- **CortexDB MemoryFlow**：对话转录 + 向量语义唤醒 + 知识图谱

这两套引擎互补而非替代，形成完整的**"记录 → 提取 → 召回 → 注入"**闭环。

### 1.2 框架组装，而非框架绑定（Framework-Assembled）

> "我们不是在框架上写应用，而是用框架组装能力。"

Wukong 基于 tRPC 三件套（tRPC-Agent-Go / tRPC-MCP-Go / tRPC-A2A-Go）构建，但不被任何一个框架约束。每个子系统都通过清晰的接口边界解耦：

- `CoreLoop` 作为中央编排器，协调 31 个子系统
- `Extension Manager` 管理所有工具集的生命周期
- `Security Guard` 作为独立的安全层，在任何工具调用前执行检查
- `ContextRevisionEngine` 独立管理 token 预算，完全与 Agent 循环解耦

这种设计使得替换任何一个子系统（如从 CortexDB 换成其他向量数据库）都是可能的。

### 1.3 多 Agent 原生（Multi-Agent by Default）

> "复杂任务不应该由单个 Agent 完成。"

Wukong 将多 Agent 编排设计为一等公民，而非事后补丁。提供 **10 种显式编排模式**：

| 模式 | 说明 | 适用场景 |
|------|------|----------|
| `single` | 标准单 Agent | 简单对话、单步任务 |
| `chain` | 顺序管道 | 分析→执行→审查 |
| `parallel` | 并发执行 | 多文件分析、多角度审查 |
| `cycle` | 迭代循环 | 规划→执行循环、代码生成→审查循环 |
| `graph` | 条件路由 | 复杂决策流、条件分支 |
| `team_coordinator` | 中央协调 | 一个协调者将成员作为工具调用 |
| `team_swarm` | 自主协作 | 成员间通过 transfer 自主转移控制权 |
| `claude_code` | 外部代理 | 委托给本地 Claude Code CLI |
| `codex` | 外部代理 | 委托给 OpenAI Codex CLI |
| `dify` | 可视化平台 | Dify AI 平台集成 |

### 1.4 进化智能（Evolving Intelligence）

> "技能应该从失败中学习，而非等待人类修复。"

Wukong 内置**技能自进化引擎**——当 Agent 执行一个 Skill 后发现失败或质量问题，系统会异步启动 LLM 分析流程，自动生成修补建议，在满足置信度阈值和冷却时间约束后自动应用补丁。

完整进化管线：`执行追踪 → LLM 分析 → 补丁生成 → 版本管理 → 热重载`

---

## 2. 核心优势

### 2.1 业界领先的记忆系统

Wukong 的记忆系统是其最显著的技术差异化优势。它由四个子系统协同构成：

```
┌──────────────────────────────────────────────────────────────┐
│                      记忆系统全景                              │
├───────────────┬──────────────────────────────────────────────┤
│ tRPC Memory   │ 键值记忆：偏好、事实、关系 (SQLite)             │
│               │ 6 个工具：add/search/update/delete/load/clear  │
│               │ AutoExtract：异步 LLM 提取 (9B 轻量模型)        │
├───────────────┼──────────────────────────────────────────────┤
│ MemoryFlow    │ 对话转录：每轮自动记录                          │
│               │ 唤醒上下文：向量+FTS5 召回历史转录              │
│               │ 事实提升：PromoteFacts → tRPC Memory 桥接      │
├───────────────┼──────────────────────────────────────────────┤
│ GraphFlow     │ 知识图谱：RDF/SPARQL 查询                      │
│               │ 实体关系提取：LLM 或启发式                      │
│               │ KG 工具：knowledge_graph_query/analyze         │
├───────────────┼──────────────────────────────────────────────┤
│ ImportFlow    │ 结构化导入：DDL 解析 → 知识图谱                 │
│               │ CSV 数据导入                                   │
│               │ 4 个导入工具                                   │
└───────────────┴──────────────────────────────────────────────┘
```

**记忆闭环**（每次 Agent.Run）：

```
Before Run                    After Run
┌─────────────┐               ┌──────────────┐
│ WakeUp()    │← 上下文注入    │ IngestTurn() │→ 转录记录
│ ReadMem()   │               │ PromoteFacts │→ 桥接 tRPC
└─────────────┘               └──────────────┘
        │                            │
        ▼                            ▼
    user message              assistant message
                                   │
                                   ▼
                           AutoExtract (异步)
                           → LLM 提取事实
                           → 写入 wukong.db
```

### 2.2 完整的 AGI 协议栈

Wukong 同时支持三种 Agent 通信协议，覆盖所有集成场景：

| 协议 | 端口 | 用途 | 框架 |
|------|------|------|------|
| **A2A** | `:9090` | Agent-to-Agent 标准通信 | tRPC-A2A-Go |
| **ACP** | `:9091` | Agent Client Protocol（编辑器集成） | 自研 |
| **AG-UI** | `:8080` | SSE 实时对话（Web UI 集成） | tRPC-Agent-Go |

**ACP MCP Bridge** (`:3400`)：将 Wukong 扩展作为 MCP Server 暴露给 ACP 代理提供商，实现跨 Agent 的工具共享。

### 2.3 多层安全防护

```
安全检查管线（每次工具调用前）
┌──────────────────────────────────────────────────────┐
│ 1. Guard.CheckToolPermission()                        │
│    ├── allowlist 检查 → 在白名单中直接放行              │
│    ├── denylist 检查 → 在黑名单中直接拒绝               │
│    ├── 权限模式判断 (auto/smart/manual/chat_only)      │
│    └── 高风险操作识别 (bash/file_write/delete/browser) │
│                                                       │
│ 2. Guard.ValidateCommand()                            │
│    ├── 危险模式匹配 (rm -rf/sudo/chmod 777/dd/mkfs)    │
│    └── blocked_commands 列表匹配                       │
│                                                       │
│ 3. IgnoreMatcher.IsIgnored()                          │
│    └── .wukongignore 文件路径验证 (gitignore 语法)      │
│                                                       │
│ 4. Guardrail (Prompt Injection)                       │
│    └── 独立轻量 Runner 审查用户输入                     │
└──────────────────────────────────────────────────────┘
```

### 2.4 智能上下文管理

```
三层压缩策略
┌──────────────────────────────────────────────────────────┐
│ Layer 1: LLM 智能摘要 (ContextRevisionEngine)             │
│   - 独立轻量模型 (9B) 生成结构化摘要                        │
│   - 触发条件：token 阈值 | 消息数>100 | 时间>5分钟          │
│                                                          │
│ Layer 2: 渐进式压缩 (ProgressiveSummarize)                 │
│   - 合并现有摘要与新消息                                    │
│   - cooldown 120s 避免频繁调用                             │
│                                                          │
│ Layer 3: 算法截断 (TruncateCommandOutput)                  │
│   - 首尾保留策略                                           │
│   - LLM 不可用时的安全回退                                  │
├──────────────────────────────────────────────────────────┤
│ tRPC-Agent-Go 框架级压缩 (context_compaction)              │
│   Pass 1: 旧超大工具结果 → 占位符 (<阈值)                   │
│   Pass 2: 剩余超大结果 → 首尾截断                            │
└──────────────────────────────────────────────────────────┘
```

---

## 3. 系统概览

```
                          Wukong AI Agent Platform
┌─────────────────────────────────────────────────────────────────────┐
│                        CLI Layer (cobra + BubbleTea)                 │
│  root │ session │ run │ configure │ extension │ eval │ project      │
├─────────────────────────────────────────────────────────────────────┤
│                     Core Agent Engine                                │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────────────────┐   │
│  │ CoreLoop    │  │ ContextRev   │  │ WorkflowBuilder (10 mode) │   │
│  │ (Run/Stream)│  │ Engine       │  │ TeamBuilder (multi-agent) │   │
│  └──────┬──────┘  └──────┬───────┘  └────────────┬─────────────┘   │
│         │                │                        │                 │
│  ┌──────┴────────────────┴────────────────────────┴──────────┐     │
│  │                   tRPC-Agent-Go Runner                     │     │
│  │     Session │ Memory │ Artifact │ Tool │ Planner │ Plugin   │     │
│  └────────────────────────────────────────────────────────────┘     │
├─────────────────────────────────────────────────────────────────────┤
│                     Storage Layer (SQLite WAL)                       │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                     wukong.db (单文件)                        │   │
│  │  Session / Memory / Recall / Todo / Vector+FTS5 /             │   │
│  │  Transcript+WakeUp / RDF+SPARQL / DDL→KG                      │   │
│  └──────────────────────────────────────────────────────────────┘   │
├─────────────────────────────────────────────────────────────────────┤
│                     Extension System (MCP)                           │
│  12 built-in extensions + external MCP servers (stdio/sse/stream)  │
├─────────────────────────────────────────────────────────────────────┤
│                     External Servers                                │
│  A2A (:9090) │ AG-UI SSE (:8080) │ ACP (:9091) │ ACP-MCP (:3400)  │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 4. 核心特性

### 4.1 LLM Provider

| 类型 | Provider |
|------|----------|
| 云服务 | OpenAI · Anthropic · Google · DeepSeek |
| 本地推理 | Ollama · LMStudio |
| 远程代理 | ACP（Agent Client Protocol） |

### 4.2 内置扩展（12 个）

| 扩展 | 工具数 | 能力 |
|------|--------|------|
| **developer** | 6 | read/write/edit/search/web/bash |
| **computer_controller** | 9 | 鼠标/键盘/截图/剪贴板 |
| **memory** | 6 | add/search/update/delete/load/clear |
| **auto_visualiser** | 3 | 图片/图表/流程图生成 |
| **tutorial** | 3 | 交互式教程控制 |
| **top_of_mind** | 4 | 持久指令 CRUD |
| **code_mode** | 2 | JS 沙箱执行 |
| **apps** | 5 | HTML 应用 CRUD |
| **web** | 1 | Web 搜索 |
| **agent_tools** | 3 | code-reviewer/summarizer/code-generator |
| **cortex** | 4 | KG 查询/分析/DDL 解析/计划 |
| **mcp_broker** | 4 | list_servers/list_tools/inspect/call |

### 4.3 附加能力

| 能力 | 说明 |
|------|------|
| **RAG 知识检索** | 向量存储 + 文档索引 (txt/md/pdf/csv/json/docx) |
| **浏览器自动化** | Chromedp 引擎，支持 DuckDuckGo/SearXNG/Tavily 搜索后端 |
| **JS 沙箱** | goja 运行时，安全执行 JavaScript |
| **技能系统** | Agent Skills 加载和热重载 |
| **技能自进化** | LLM 分析执行轨迹 → 自动修补 SKILL.md |
| **Dify 集成** | Dify AI 平台可视化工作流连接 |
| **制品存储** | InMemory / 腾讯云 COS |
| **可观测性** | OpenTelemetry 全链路追踪 + Langfuse LLM 分析 |

---

## 5. 快速开始

### 5.1 安装

```bash
go install github.com/km269/wukong/cmd/wukong@latest
```

### 5.2 配置

```bash
# 交互式配置向导
wukong configure

# 或直接编辑配置文件
vim config.yaml    # 当前目录
vim ~/.config/wukong/config.yaml  # 全局
```

最小配置示例 (LMStudio)：

```yaml
default_provider: "lmstudio"

providers:
  - name: "lmstudio"
    type: "lmstudio"
    base_url: "http://localhost:1234/v1"
    api_key: "lmstudio"
    model: "google/gemma-4-26b-a4b"
```

### 5.3 使用

```bash
# 交互式会话（TUI 界面）
wukong session

# 单次对话
wukong run "用 Go 写一个 HTTP 服务器"

# 使用指定模型
wukong session --provider deepseek --model deepseek-chat

# 调试模式
wukong session --debug
```

### 5.4 启动后的自动能力

启动 Wukong 会话后，系统**自动启用**以下能力：

- ✅ 对话历史持久化（SQLite）
- ✅ 对话转录记录、自动唤醒上下文（MemoryFlow）
- ✅ 知识图谱构建、结构化数据导入（GraphFlow / ImportFlow）
- ✅ 上下文智能压缩（独立摘要模型）
- ✅ 四层安全检查（权限 + 命令 + 文件 + Prompt 注入）
- ✅ YAML Recipe 子代理发现和加载
- ✅ 技能目录自动加载和热重载
- ✅ A2A / ACP / AG-UI 服务端点

---

## 6. 技术栈

| 类别 | 技术 | 版本 | 用途 |
|------|------|------|------|
| **语言** | Go | 1.26 | 核心语言 |
| **Agent 框架** | tRPC-Agent-Go | v1.10.0 | Agent 引擎、Runner、Session、Memory、Planner、Plugin |
| **MCP 协议** | tRPC-MCP-Go | v0.0.16 | MCP server/client (stdio/sse/streamable) |
| **A2A 协议** | tRPC-A2A-Go | v0.2.5 | Agent-to-Agent 通信 |
| **智能记忆** | CortexDB | v2.25.0 | HNSW 向量索引 + FTS5 全文搜索 + RDF/SPARQL |
| **数据库** | SQLite (mattn/go-sqlite3) | v1.14.32 | 单文件 WAL 模式，共享连接池 |
| **CLI 框架** | Cobra | v1.9.1 | 命令行解析 |
| **TUI** | BubbleTea + Bubbles + LipGloss | v1.x | 终端交互界面 |
| **配置** | Viper | v1.20.1 | 多级配置覆盖 (CLI > ENV > YAML) |
| **浏览器** | Chromedp | v0.15.1 | Chrome DevTools 协议 |
| **JS 引擎** | goja | latest | JavaScript 沙箱 |
| **LLM 追踪** | Langfuse | — | LLM 调用分析和成本追踪 |
| **可观测性** | OpenTelemetry | v1.43.0 | 分布式追踪 |
| **缓存** | Redis (go-redis) | v9.12.1 | 可选 Session 存储 |

---

## 7. 项目结构

```
wukong/
├── cmd/wukong/main.go            # 入口点 (3行)
├── config.yaml                   # 完整配置文件 (577行)
├── internal/
│   ├── agent/                    # 核心引擎 (14文件)
│   │   ├── loop.go               # 主执行循环 (1280行) ★
│   │   ├── context.go            # 上下文管理 (559行)
│   │   ├── workflow.go           # 10种编排模式 (637行)
│   │   ├── team.go               # 多Agent团队 (315行)
│   │   ├── hitl.go               # 人机回环 (149行)
│   │   ├── prompt_template.go    # 提示词模板
│   │   ├── recipe.go             # YAML配方子代理
│   │   ├── todo_enforcer.go      # 任务完成强制器
│   │   └── dify.go               # Dify AI平台集成
│   ├── extension/                # 扩展系统 (9文件)
│   │   ├── manager.go            # 扩展管理器 ★
│   │   ├── factory.go            # 内置扩展工厂
│   │   ├── acp_mcp.go            # ACP MCP 桥接
│   │   ├── mcp_client.go         # MCP 客户端
│   │   ├── manager_tools.go      # 扩展管理工具
│   │   └── builtin/              # 12个内置扩展实现
│   ├── cortex/                   # CortexDB 智能记忆 (12文件)
│   │   ├── store.go              # HNSW向量+FTS5混合存储 ★
│   │   ├── memoryflow.go         # 对话转录+唤醒
│   │   ├── graphflow.go          # 知识图谱构建
│   │   ├── import_flow.go        # 结构化数据导入
│   │   ├── kg_tools.go           # SPARQL查询工具
│   │   ├── planner.go            # LLM检索策略规划
│   │   ├── extractor.go          # LLM会话提取
│   │   └── ...
│   ├── security/                 # 安全层 (4文件)
│   │   ├── guard.go              # 多模式权限 + 命令验证
│   │   └── ignore.go             # .wukongignore 文件黑名单
│   ├── memory/                   # tRPC Memory 存储
│   ├── recall/                   # 跨会话回溯 (SQLite FTS5)
│   ├── session/                  # 会话存储 (SQLite/Redis/InMemory)
│   ├── provider/                 # LLM Provider 工厂 (7种)
│   ├── evolution/                # 技能自进化引擎 (6文件)
│   ├── summon/                   # 子代理调度 + A2A 协议
│   ├── skill/                    # Agent Skill 系统
│   ├── knowledge/                # RAG 知识检索
│   ├── browser/                  # Chromedp 浏览器自动化
│   ├── codemode/                 # goja JS 沙箱
│   ├── apps/                     # HTML 应用管理
│   ├── artifact/                 # 制品存储
│   ├── observability/            # Langfuse 集成
│   ├── telemetry/                # OpenTelemetry
│   ├── project/                  # 项目追踪
│   ├── todo/                     # 任务工具
│   ├── topofmind/                # 持久指令
│   ├── health/                   # 健康检查
│   ├── eval/                     # 评估框架
│   ├── cli/                      # CLI 命令 + TUI
│   │   ├── session.go            # 会话引导 (1196行) ★
│   │   ├── root.go/run.go/configure.go/...
│   │   └── tui/                  # BubbleTea 界面
│   ├── server/                   # A2A + ACP + AG-UI
│   ├── config/                   # 配置系统 (Viper)
│   └── util/                     # 共享工具 (DB池+日志)
├── docs/
│   ├── README.md                 # 本文档
│   ├── ARCHITECTURE.md           # 系统架构深度分析
│   ├── CONFIG.md                 # 配置参考手册
│   └── LICENSE                   # GNU AGPL-3.0
├── go.mod / go.sum
├── Makefile / Taskfile.yaml
└── .wukongignore                 # 文件访问黑名单
```

---

## 8. 文档索引

| 文档 | 说明 |
|------|------|
| [架构文档](ARCHITECTURE.md) | 系统架构深度分析、31个子系统设计、数据流、关键设计决策(ADR)、技术选型依据 |
| [配置手册](CONFIG.md) | 30 个配置段完整参考、加载优先级、环境变量、推荐配置 |
