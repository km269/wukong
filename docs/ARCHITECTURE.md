# Wukong 系统架构深度分析

> **版本**: v0.6.0 | **Go**: 1.26 | **总源文件**: 119 `.go` + 31 `_test.go` | **直接依赖**: 30
>
> 基于 [tRPC-Agent-Go v1.10.0](https://github.com/trpc-group/trpc-agent-go) · [tRPC-MCP-Go v0.0.16](https://github.com/trpc-group/trpc-mcp-go) · [tRPC-A2A-Go v0.2.5](https://github.com/trpc-group/trpc-a2a-go) · [CortexDB v2.25.0](https://github.com/liliang-cn/cortexdb)
>
> **记忆系统**: 双引擎 (tRPC Memory + CortexDB MemoryFlow) | HNSW 向量索引 | 智能上下文压缩 | 知识图谱 (RDF/SPARQL)

---

## 目录

1. [架构哲学](#1-架构哲学)
2. [核心优势](#2-核心优势)
3. [系统全景架构](#3-系统全景架构)
4. [Agent 执行引擎](#4-agent-执行引擎)
5. [记忆系统](#5-记忆系统)
6. [多 Agent 编排](#6-多-agent-编排)
7. [扩展与工具系统](#7-扩展与工具系统)
8. [安全架构](#8-安全架构)
9. [上下文管理](#9-上下文管理)
10. [LLM Provider 体系](#10-llm-provider-体系)
11. [服务与协议层](#11-服务与协议层)
12. [存储架构](#12-存储架构)
13. [技能自进化系统](#13-技能自进化系统)
14. [数据流](#14-数据流)
15. [技术选型](#15-技术选型)
16. [关键设计决策 (ADR)](#16-关键设计决策-adr)

---

## 1. 架构哲学

### 1.1 四条核心原则

Wukong 的架构设计围绕四条核心哲学展开，每一条都体现在具体的工程决策中：

#### 原则一：记忆优先（Memory-First）

> **设计信念**：Agent 的真正智能来源于跨会话的知识积累，而非单次对话的上下文窗口。

**工程体现**：
- **双引擎记忆**：tRPC Memory（结构化键值）+ CortexDB MemoryFlow（对话转录+语义唤醒），两者互补而非替代
- **完整记忆闭环**：记录（IngestTurn）→ 提取（AutoExtract + PromoteFacts）→ 召回（WakeUp + ReadMemories）→ 注入（message 前缀）
- **知识图谱**：从对话中自动提取实体关系，构建 RDF 图，支持 SPARQL 查询
- **HNSW 向量索引**：CortexDB 的 O(log N) 语义搜索，替代全表扫描

**与其他框架的关键差异**：大多数 Agent 框架将对话上下文视为瞬态（仅靠 Session 存储），或仅提供简单的键值记忆。Wukong 将记忆视为**基础设施级的核心能力**。

#### 原则二：框架组装，而非框架绑定（Framework-Assembled）

> **设计信念**：框架应该是能力的来源而非约束。系统应能在需要时替换任何组件。

**工程体现**：
- `CoreLoopConfig` 依赖注入：31 个子系统通过配置结构体注入，而非硬编码依赖
- 清晰的子系统接口边界：Security Guard、Context Engine、Extension Manager 各自独立
- `closeFn` 6步关闭链：关闭顺序明确可控（runner → evolution → memory → session → telemetry → DB pool）
- Provider Factory 抽象：7 种 LLM provider 通过统一接口接入
- Session 后端的可替换性：SQLite / Redis / InMemory 三种实现

#### 原则三：多 Agent 原生（Multi-Agent by Default）

> **设计信念**：复杂任务不应由单个 Agent 完成。多 Agent 编排应是一等公民，而非可选插件。

**工程体现**：
- 10 种显式编排模式，每种对应不同的任务拓扑
- `WorkflowBuilder` + `TeamBuilder` 两个独立的构造器
- HITL（人机回环）：Graph 工作流的中断和 checkpoint 恢复
- 外部 Agent 委托：Claude Code / Codex CLI 作为可调用工具
- Dify 集成：可视化工作流的桥接

#### 原则四：进化智能（Evolving Intelligence）

> **设计信念**：技能应该从执行失败中自动学习改进，而非等待人类维护。

**工程体现**：
- 完整的进化管线：执行追踪 → 异步分析 → 补丁生成 → 版本管理 → 热重载
- 非侵入式设计：分析在后台 goroutine 中运行，不阻塞主 Agent 循环
- 安全约束：置信度阈值、冷却时间、每日补丁上限、最大补丁大小
- 版本回滚：保持历史版本，支持追溯

---

## 2. 核心优势

### 2.1 优势全景

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Wukong 核心优势全景                            │
├───────────────────┬───────────────────┬─────────────────────────────┤
│    记忆系统        │  多Agent 编排     │      安全体系                │
│                   │                   │                             │
│ 双引擎记忆        │ 10种编排模式      │ 4级权限控制                  │
│ 知识图谱 RDF      │ HITL 人机回环     │ 危险命令检测                  │
│ HNSW 向量搜索     │ Graph 条件路由    │ .wukongignore 文件黑名单      │
│ 自动事实提取      │ Team Coordinator  │ Prompt 注入审查               │
│ 对话转录+唤醒     │ Team Swarm        │ 扩展恶意代码扫描              │
│ DDL→KG 导入       │ 外部Agent委托     │ 3层命令安全检查               │
├───────────────────┼───────────────────┼─────────────────────────────┤
│    上下文管理      │   协议与集成       │     进化与扩展                │
│                   │                   │                             │
│ 3层压缩策略       │ A2A (:9090)       │ 技能自进化引擎               │
│ 独立摘要模型      │ ACP (:9091)       │ LLM 分析 → 自动补丁          │
│ 渐进式压缩        │ AG-UI SSE (:8080) │ 版本管理+热重载              │
│ cooldown 机制     │ ACP MCP (:3400)   │ 12个内置扩展                 │
│ token 精确追踪    │ 7种 LLM Provider  │ MCP Broker 聚合              │
└───────────────────┴───────────────────┴─────────────────────────────┘
```

### 2.2 技术差异化

| 能力 | Wukong | 业界常见方案 | 优势 |
|------|--------|-------------|------|
| **长期记忆** | 双引擎 + 知识图谱 + HNSW | 仅 Session 或简单键值 | 完整的"记录→提取→召回→注入"闭环 |
| **记忆闭环** | 自动转录 → 抽取 → 唤醒 → 提升 | 需手动管理 | 全自动，首次运行后即拥有记忆 |
| **多 Agent 编排** | 10 种显式模式，原生支持 | 通常 2-3 种或需外部编排 | 图工作流、循环迭代、蜂群协作 |
| **上下文压缩** | 3层策略 + 独立摘要模型 | 通常仅算法截断 | LLM 智能摘要 + cooldown 避免浪费 |
| **安全模型** | 4级权限 + 4层管线 | 通常 2 级 | 文件级别的 gitignore 语法黑名单 |
| **技能进化** | 自动分析 → 补丁 → 热重载 | 无此能力 | 闭环自改进 |
| **数据库** | 单文件 wukong.db (所有表) | 多文件多库 | 跨系统查询可行，部署运维简单 |

---

## 3. 系统全景架构

### 3.1 层级视图

```
                          Wukong AI Agent Platform
┌─────────────────────────────────────────────────────────────────────┐
│                     Layer 6: 入口层                                  │
│  CLI (cobra) │ TUI (BubbleTea) │ API Server (A2A/ACP/AG-UI)        │
├─────────────────────────────────────────────────────────────────────┤
│                     Layer 5: 编排层                                  │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                    CoreLoop (中央编排器)                       │   │
│  │  ┌──────────────┐  ┌──────────────┐  ┌───────────────────┐  │   │
│  │  │ Workflow     │  │ TeamBuilder  │  │ ContextRevision   │  │   │
│  │  │ Builder      │  │ (coordinator │  │ Engine (token     │  │   │
│  │  │ (10 modes)   │  │  /swarm/     │  │  budget mgmt)    │  │   │
│  │  │              │  │  external)   │  │                   │  │   │
│  │  └──────────────┘  └──────────────┘  └───────────────────┘  │   │
│  └─────────────────────────────────────────────────────────────┘   │
├─────────────────────────────────────────────────────────────────────┤
│                     Layer 4: Agent 引擎层                            │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                  tRPC-Agent-Go Runner                        │   │
│  │  LLMAgent │ ChainAgent │ ParallelAgent │ CycleAgent │ Graph │   │
│  │  Session Service │ Memory Service │ Planner │ Tool │ Plugin │   │
│  └─────────────────────────────────────────────────────────────┘   │
├─────────────────────────────────────────────────────────────────────┤
│                     Layer 3: 能力层                                  │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────────────┐   │
│  │ Security │ │ Extension│ │ Evolution│ │ Memory + Recall +    │   │
│  │ Guard    │ │ Manager  │ │ Engine   │ │ CortexDB Stack       │   │
│  └──────────┘ └──────────┘ └──────────┘ └──────────────────────┘   │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────────────┐   │
│  │ Knowledge│ │ Browser  │ │ CodeMode │ │ Summon + Skill       │   │
│  │ (RAG)    │ │ (Chromdp)│ │ (goja)   │ │ (sub-agent dispatch) │   │
│  └──────────┘ └──────────┘ └──────────┘ └──────────────────────┘   │
├─────────────────────────────────────────────────────────────────────┤
│                     Layer 2: 基础设施层                              │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  Provider Factory (7 LLM backends + ACP)                    │   │
│  │  Config Loader (Viper: CLI > ENV > YAML > defaults)         │   │
│  │  DatabasePool (shared SQLite WAL, MaxOpenConns=1)           │   │
│  │  Telemetry (OpenTelemetry + Langfuse)                       │   │
│  │  Logger (slog structured logging)                           │   │
│  └─────────────────────────────────────────────────────────────┘   │
├─────────────────────────────────────────────────────────────────────┤
│                     Layer 1: 存储层                                  │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │              wukong.db (单文件 SQLite WAL)                    │   │
│  │  sessions │ memories │ chat_recall (FTS5) │ todos │          │   │
│  │  memoryflow_transcripts │ entities (RDF) │ relations │       │   │
│  │  skill_versions │ evolution_records │ HNSW vectors          │   │
│  └─────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────┘
```

### 3.2 子系统依赖关系

```
bootstrapSession() 初始化流程 (session.go, ~1200行)
    │
    ├── [1]  配置加载    → config.Loader (Viper)
    ├── [2]  遥测        → OpenTelemetry + Langfuse
    ├── [3]  数据库池    → util.DatabasePool (shared SQLite WAL)
    ├── [4]  Provider    → provider.Factory (7种LLM)
    ├── [5]  Session     → session/tRPC (SQLite/Redis/InMemory)
    ├── [6]  Memory      → memory.Store (tRPC, 键值记忆)
    ├── [7]  Security    → security.Guard (4级权限)
    ├── [8]  Extensions  → extension.Manager (12内置+外部MCP)
    ├── [9]  ACP MCP     → extension.ACPMCPBridge
    ├── [10] Recall      → recall.Store 或 cortex.Store (CortexDB)
    ├── [11] MemoryFlow  → cortex.MemoryFlowService
    ├── [12] GraphFlow   → cortex.GraphFlowService
    ├── [13] ImportFlow  → cortex.ImportFlowService
    ├── [14] TopOfMind   → topofmind.Manager
    ├── [15] CodeMode    → codemode.Executor
    ├── [16] Apps        → apps.Manager
    ├── [17] AgentTools  → agent_tools toolset
    ├── [18] Summon      → summon.Manager + Skill Manager
    ├── [19] Evolution   → evolution.Engine
    ├── [20] A2A Remotes → 注册远程 A2A 代理
    ├── [21] Todo        → todo.Manager
    ├── [22] Knowledge   → knowledge.Manager (RAG)
    ├── [23] Artifact    → artifact.Factory
    ├── [24] CoreLoop    → agent.NewCoreLoop (中央编排器)
    ├── [25] A2A Server  → server.StartA2AServer (:9090)
    ├── [26] AG-UI Server→ server.StartAGUIServer (:8080)
    └── [27] ACP Server  → server.StartACPServer (:9091)
```

---

## 4. Agent 执行引擎

### 4.1 CoreLoop — 中央编排器

`CoreLoop`（`internal/agent/loop.go`, 1280 行）是整个 Wukong 的中央编排器。它不直接执行 LLM 推理，而是作为指挥中心，协调 31 个子系统在一个完整的对话轮次中的协作。

**结构定义**：

```go
type CoreLoop struct {
    agent          agent.Agent        // tRPC LLMAgent 实例
    runner         runner.Runner      // tRPC Runner（管理执行流程）
    sessionService session.Service    // 会话存储
    memoryService  memory.Service     // tRPC 长期记忆
    factory        *provider.Factory  // LLM Provider 工厂
    cfg            *config.WukongConfig
    contextMgr     *ContextManager    // 上下文窗口管理器
    security       *security.Guard    // 安全守卫
    recallStore    *recall.Store      // 跨会话回溯
    memoryFlow     *cortex.MemoryFlowService // 对话转录+唤醒
    closeFn        func() error       // 6步关闭链
    mu             sync.RWMutex
    closed         bool
}
```

**依赖注入**（CoreLoopConfig）：

```
CoreLoopConfig 包含 27 个依赖项：
├── Config, Factory (基础)
├── SessionService, MemoryService, ArtifactService (tRPC 框架)
├── ToolSets, FunctionTools (工具系统)
├── SecurityGuard (安全层)
├── RecallStore (回溯)
├── RevisionModel (上下文压缩模型)
├── MemoryFlowService (转录+唤醒)
├── TopOfMindInstructions (持久指令)
├── TelemetryShutdown, MemoryClose, EvolutionClose, DBPoolClose (资源管理)
└── SystemPrompt, PromptTemplateFuncs, TaskInjector (提示词系统)
```

### 4.2 执行循环详解

每个对话轮次经历以下完整流程：

```
CoreLoop.Run(userID, sessionID, message) ／ RunStream()
│
├── Phase 1: 对话前准备 (Before LLM)
│   ├── PrepareContext()           ← ContextRevisionEngine
│   │   ├── shouldRevise()?        ← 检查 token/消息数/时间
│   │   └── EnqueueSummaryJob()    ← 异步压缩（如果触发）
│   │
│   ├── recallStore.StoreMessage() ← 存储消息到回溯索引
│   │
│   ├── memoryFlow.IngestTurn()    ← 转录用户消息
│   │
│   ├── memoryFlow.WakeUp()        ← 向量+FTS5 召回历史上下文
│   │   └── 构建分层唤醒文本：
│   │       ├── Identity (身份识别)
│   │       ├── Recalled memories (召回的历史转录)
│   │       └── Session context (当前会话上下文)
│   │
│   └── memoryService.ReadMemories() ← 读取持久记忆
│       └── 记忆文本注入 message 前缀
│
├── Phase 2: Agent 执行 (Core Engine)
│   ├── runner.Run(userID, sessionID, message)
│   │   ├── LLM 推理 (主模型, 26B gemma-4-26b-a4b)
│   │   ├── 工具调用 (50+ 可用工具, ToolSearch 自动筛选)
│   │   │   ├── BeforeTool: Security Guard 检查
│   │   │   │   ├── CheckToolPermission (allowlist/denylist/模式)
│   │   │   │   ├── ValidateCommand (危险命令检测)
│   │   │   │   └── CheckFilePath (.wukongignore 验证)
│   │   │   └── AfterTool: 结果收集
│   │   ├── AutoExtract: 异步记忆提取 (9B qwen3.5-9b)
│   │   └── EnqueueSummaryJob: 上下文压缩 (9B gemma-4-e4b-it)
│   │
│   └── 流式事件处理 (仅 RunStream)
│       ├── 提取最终响应文本
│       ├── 收集所有生成事件
│       └── 提取最终 assistant 消息
│
├── Phase 3: 对话后收尾 (After LLM)
│   ├── memoryFlow.IngestTurn()    ← 转录助手回复
│   │
│   ├── memoryFlow.PromoteFacts()  ← 提取候选事实
│   │   └── LLM 分析 → 识别重要事实 → 桥接到 tRPC Memory
│   │
│   ├── recallStore.StoreMessage() ← 存储助手回复到回溯索引
│   │
│   └── contextMgr.AfterRun()      ← 更新 token 计数
│       ├── 优先使用 Response.Usage (精确)
│       └── 回退字符数/4 (估算)
│
└── Phase 4: 返回响应
    └── 最终响应文本（Run）/ 事件流 channel（RunStream）
```

### 4.3 Agent 创建策略

`createSingleAgent()` 创建一个完整的 tRPC LLMAgent，配置以下管线：

```
Agent 创建管线
├── Tool Search Plugin        ← 自动过滤相关工具，减少 token 消耗
├── Prompt Template           ← 系统指令 + 变量替换 ({{.WorkingDir}})
├── Preload Memory            ← 启动时预加载长期记忆注入 System Instruction
├── Tool Retry                ← 工具调用失败自动重试 (max 3次, backoff 2x)
├── Parallel Tools            ← 支持并行工具调用
├── Context Compaction        ← tRPC 框架级双通道压缩
│   ├── Pass 1: 旧超大结果 → 占位符
│   └── Pass 2: 剩余超大结果 → 首尾截断
├── Session Recall            ← 跨会话上下文预加载
├── Planner (可选)             ← builtin (原生 thinking) 或 react (标签引导)
├── Todo Tool + Enforcer      ← 任务追踪 + 强制完成校验
├── Prompt Injection Guardrail← 独立轻量 Runner 审查输入
├── Tool Callbacks            ← BeforeTool: 安全校验
├── Model Callbacks           ← BeforeModel/AfterModel: token 追踪
└── Agent Callbacks           ← BeforeAgent/AfterAgent: 日志 + evolution
```

---

## 5. 记忆系统

### 5.1 双引擎架构

Wukong 的记忆系统由两套互补的引擎构成：

```
┌─────────────────────────────────────────────────────────────────────┐
│                         记忆系统全景                                  │
│                                                                     │
│  ┌─────────────────────────────┐  ┌─────────────────────────────┐   │
│  │    tRPC Memory (引擎一)      │  │  CortexDB Stack (引擎二)     │   │
│  │                             │  │                             │   │
│  │  存储: wukong.db            │  │  存储: wukong.db (同库)      │   │
│  │  模型: 键值 (key-value)     │  │  模型: 转录 + 向量 + 图谱   │   │
│  │                             │  │                             │   │
│  │  📝 记忆提取                │  │  📋 MemoryFlow              │   │
│  │   AutoExtract (异步)        │  │   IngestTurn (记录)         │   │
│  │   9B qwen3.5-9b             │  │   WakeUp (唤醒)             │   │
│  │   每轮对话后自动运行         │  │   PromoteFacts (提升)       │   │
│  │                             │  │                             │   │
│  │  🔧 6 个工具                │  │  🔗 GraphFlow               │   │
│  │   add / search / update     │  │   实体提取 → RDF 知识图谱   │   │
│  │   delete / load / clear     │  │   SPARQL 查询与分析         │   │
│  │                             │  │                             │   │
│  │  🎯 适用场景                │  │  📥 ImportFlow              │   │
│  │   用户偏好、事实、关系       │  │   DDL → 知识图谱            │   │
│  │   结构化、可查询的知识       │  │   CSV 数据导入              │   │
│  └─────────────────────────────┘  └─────────────────────────────┘   │
│                                                                     │
│                    两套引擎通过 PromoteFacts 桥接                      │
│                   MemoryFlow 提取 → tRPC Memory 存储                  │
└─────────────────────────────────────────────────────────────────────┘
```

### 5.2 tRPC Memory — 结构化键值记忆

**存储模型**：每个记忆是一个键值对，存储在 `wukong.db` 的 `memories` 表中。

**AutoExtract 机制**：
- 每次 `runner.Run()` 完成后，tRPC 框架自动触发异步记忆提取
- 使用独立的轻量模型（9B qwen3.5-9b）进行提取，不影响主 Agent 性能
- 提取超时：120s（可配至 600s）
- 提取 Prompt：内置 7 条规则（原子性、去重、特异性、情节vs事实等）

**记忆工具**（6个）：
| 工具 | 功能 |
|------|------|
| `memory_add` | 添加新记忆 |
| `memory_search` | 搜索记忆 |
| `memory_update` | 更新已有记忆 |
| `memory_delete` | 删除记忆 |
| `memory_load` | 加载所有记忆 |
| `memory_clear` | 清空记忆 |

### 5.3 CortexDB MemoryFlow — 对话转录与唤醒

**三个核心能力**：

1. **IngestTurn**（转录记录）：每轮对话自动记录到 `memoryflow_transcripts` 表
2. **WakeUp**（上下文唤醒）：运行前调用，返回分层上下文文本注入 message 前缀
3. **PromoteFacts**（事实提升）：从转录中提取重要事实 → 桥接到 tRPC Memory

**WakeUp 的上下文分层**：

```
WakeUp 返回的上下文结构
├── Identity Block (身份识别)
│   └── "Based on our conversation history, I recognize you as..."
├── Recalled Memories (召回的历史转录)
│   └── 向量+FTS5 搜索返回的最相关历史对话片段
└── Session Context (当前会话上下文)
    └── 当前会话的关键信息摘要
```

### 5.4 GraphFlow — 知识图谱

**管线**：
```
对话转录 → 实体识别（启发式/LLM）→ RDF 三元组构建 → 图数据库
                                                    │
                                                    ▼
                                            SPARQL 查询接口
                                            (knowledge_graph_query)
                                            (knowledge_graph_analyze)
```

**两种提取模式**：
- **启发式**：零成本，基于规则匹配
- **LLM**：配置 `extractor_model` 后使用 LLM 提取，更精确

### 5.5 ImportFlow — 结构化数据导入

支持将数据库 DDL 和 CSV 数据自动导入知识图谱：

```
CREATE TABLE employees (id INT, name TEXT, department TEXT)
                                    │
                                    ▼
                            DDL 解析 → 实体类型推断
                                    │
CSV: 1, Alice, Engineering          │
     2, Bob, Marketing               ▼
                            RDF 三元组: employee_1 rdf:type Employee
                                        employee_1 name "Alice"
                                        employee_1 department "Engineering"
```

**导入工具**（4个）：
| 工具 | 功能 |
|------|------|
| `importflow_ddl_parse` | 解析 DDL 语句 |
| `importflow_ddl_plan` | 生成导入计划（启发式） |
| `importflow_ddl_plan_ai` | 生成导入计划（LLM） |
| `importflow_csv` | 导入 CSV 数据 |

### 5.6 记忆 TTL 与维护

- **自动清理**：启动时清除 30 天前的旧记忆
- **容量限制**：每用户最大记忆数 100（可配）
- **记忆去重**：AutoExtract 内置去重逻辑

---

## 6. 多 Agent 编排

### 6.1 WorkflowBuilder — 10 种编排模式

`WorkflowBuilder`（`internal/agent/workflow.go`, 637 行）提供 10 种显式编排模式：

```
WorkflowBuilder.Build(mode)
│
├── single           → 标准单 LLMAgent（默认）
│
├── chain            → ChainAgent
│   └── planner → executor → reviewer（顺序执行）
│
├── parallel         → ParallelAgent
│   └── code-analyzer ∥ doc-analyzer ∥ test-analyzer（并发执行）
│
├── cycle            → CycleAgent
│   ├── default:      planner ↔ executor（最多10次迭代）
│   └── code_review:  generator ↔ reviewer（CODE_APPROVED 退出）
│
├── graph            → GraphAgent
│   └── StateGraph: analyze → code / search / answer → review
│       条件路由 + HITL 中断/恢复
│
├── team_coordinator → TeamBuilder.BuildCoordinatorTeam()
│   └── Coordinator Agent 将成员作为 AgentTool 并行调用
│
├── team_swarm       → TeamBuilder.BuildSwarm()
│   └── 成员间通过 transfer_to_agent 自主转移控制权
│
├── claude_code      → TeamBuilder.BuildClaudeCode()
│   └── 委托给本地 Claude Code CLI (bypassPermissions)
│
├── codex            → TeamBuilder.BuildCodex()
│   └── 委托给 OpenAI Codex CLI (workspace-write 沙箱)
│
└── dify             → DifyAgent
    └── Dify AI 平台可视化工作流
```

### 6.2 TeamBuilder — 多 Agent 团队

`TeamBuilder`（`internal/agent/team.go`, 315 行）提供三种团队模式：

#### Coordinator 模式

```
Coordinator Agent (主)
    │
    ├── AgentTool("researcher")  → 研究子任务
    ├── AgentTool("coder")       → 编码子任务
    └── AgentTool("reviewer")    → 审查子任务
    
成员作为工具调用，Coordinatior 决定何时调用谁，支持并行调用
```

#### Swarm 模式

```
Agent A (起始)
    │
    ├── transfer_to_agent("Agent B")  → 转移控制权
    │
Agent B
    │
    ├── transfer_to_agent("Agent C")  → 继续转移
    │
Agent C
    └── 返回最终结果
    
无需中央协调器，Agent 间直接协商
支持跨请求转移状态
```

#### 外部代理委托

```
Claude Code 委托:
  CoreLoop → ClaudeCodeAgent → claude CLI (bypassPermissions)
  
OpenAI Codex 委托:
  CoreLoop → CodexAgent → codex exec --json (workspace-write sandbox)
```

### 6.3 HITL — 人机回环

`HITL`（`internal/agent/hitl.go`, 149 行）提供 Graph 工作流的中断/恢复能力：

```
StateGraph 执行
    │
    ├── Node A → 执行
    ├── InterruptBefore("dangerous_op")  ← 在此中断，等待人类审批
    │   └── 暂停执行，保存 checkpoint
    │
    ├── 人类确认 ← graph.Interrupt() 返回值
    │
    └── ResumeInterrupted(checkpoint)  ← 从断点恢复继续
```

---

## 7. 扩展与工具系统

### 7.1 扩展管理器架构

`Extension Manager`（`internal/extension/manager.go`, 427 行）管理所有扩展的生命周期：

```
Extension Manager
│
├── Initialize()
│   ├── registerBuiltinLocked()    → 12 个内置扩展
│   │   ├── developer              → 6 tools
│   │   ├── computer_controller    → 9 tools
│   │   ├── memory                 → 6 tools
│   │   ├── auto_visualiser        → 3 tools
│   │   ├── tutorial               → 3 tools
│   │   ├── top_of_mind            → 4 tools
│   │   ├── code_mode              → 2 tools
│   │   ├── apps                   → 5 tools
│   │   ├── web                    → 1 tool
│   │   ├── agent_tools            → 3 tools
│   │   ├── cortex                 → 4 tools
│   │   └── mcp_broker             → 4 tools
│   │
│   └── registerExternalLocked()   → 外部 MCP servers
│       ├── stdio transport        → 本地进程通信
│       ├── sse transport          → HTTP SSE 连接
│       └── streamable transport   → HTTP streamable 连接
│
├── EnableExtension(name)          → 动态启用
├── DisableExtension(name)         → 动态禁用
├── RegisterFromDeeplink(url)      → wukong://extension?name=xxx
├── SetMemoryService(mem)          → 注入 memory service
│
├── ToolSets()                     → 聚合所有扩展工具
└── ManagerToolSet                 → 4 个扩展管理工具
    ├── extension_list
    ├── extension_enable
    ├── extension_disable
    └── extension_register
```

### 7.2 MCP Broker 模式

Wukong 独创的 MCP Broker 模式，将多个外部 MCP 扩展聚合为一个统一 toolset：

```
MCP Broker ToolSet
│
├── mcp_list_servers   → 列出所有已连接的 MCP 服务器
├── mcp_list_tools     → 列出指定服务器的所有工具
├── mcp_inspect_tools  → 查看工具的输入/输出 schema
└── mcp_call           → 代理调用指定工具（参数转发）
                        ├── 自动路由到对应的 MCP 服务器
                        ├── 超时控制
                        └── 错误处理
```

**设计优势**：Agent 不需要知道扩展来自哪个 MCP 服务器，只需要通过 Broker 统一接口调用。这避免了工具列表膨胀（50+ → 4个入口工具）。

### 7.3 内置扩展详解

| 扩展 | 核心工具 | 技术栈 | 安全措施 |
|------|----------|--------|----------|
| **developer** | read/write/edit/search/web/execute | OS 文件系统 + bash | Guard 检查 + .wukongignore |
| **computer_controller** | 鼠标/键盘/截图/剪贴板 | Chromedp | 权限级别检查 |
| **memory** | add/search/update/delete/load/clear | SQLite (wukong.db) | 数据隔离 |
| **auto_visualiser** | 图片/图表/流程图生成 | Mermaid + ECharts | 输出目录限制 |
| **tutorial** | 教程步骤控制 | 内置状态机 | 无风险 |
| **top_of_mind** | 持久指令 CRUD | 文件系统 (.md) | Guard 检查 |
| **code_mode** | JS 代码执行 | goja 沙箱 | 超时 + 内存限制 |
| **apps** | HTML 应用 CRUD | 文件系统 | Guard 检查 |
| **web** | Web 搜索 | DuckDuckGo/SearXNG | HTTP 超时 |
| **agent_tools** | code-reviewer/summarizer/generator | tRPC AgentTools | 子 Agent 隔离 |
| **cortex** | KG查询/分析/DDL解析/计划 | CortexDB SPARQL | 读操作 |
| **mcp_broker** | list_servers/tools/inspect/call | MCP 协议 | 转发管控 |

---

## 8. 安全架构

### 8.1 四层安全检查管线

```
每次工具调用前经历 4 层检查：

┌─────────────────────────────────────────────────────────────────────┐
│ Layer 1: Guard.CheckToolPermission()                                │
│                                                                     │
│  ┌─────────────────┐                                                │
│  │ allowlist 检查   │ → 在白名单中 → 直接放行                        │
│  └────────┬────────┘                                                │
│           │ 不在白名单                                               │
│  ┌────────▼────────┐                                                │
│  │ denylist 检查    │ → 在黑名单中 → 直接拒绝                        │
│  └────────┬────────┘                                                │
│           │ 不在黑名单                                               │
│  ┌────────▼────────┐                                                │
│  │ 权限模式判断     │ auto → 放行                                    │
│  │                  │ smart → 高风险需要批准                         │
│  │                  │ manual → 全部需要批准                          │
│  │                  │ chat_only → 全部拒绝                           │
│  └────────┬────────┘                                                │
│           │ 需要批准                                                 │
│  ┌────────▼────────┐                                                │
│  │ isHighRiskOp()   │ bash / file_write / delete / browser 操作      │
│  │ NeedsApproval()  │ 返回 true → 等待用户批准                       │
│  └─────────────────┘                                                │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 2: Guard.ValidateCommand() (仅 bin/bash 工具)                  │
│                                                                     │
│  检测模式:                                                           │
│  ├── "rm -rf /"           → 递归删除根目录                           │
│  ├── "sudo ..."           → 提权操作                                 │
│  ├── "chmod 777"          → 过度开放权限                             │
│  ├── "dd if=/dev/zero"    → 磁盘覆写                                 │
│  ├── "mkfs."              → 格式化文件系统                           │
│  ├── "curl ... | sh"      → 管道执行远程脚本                         │
│  ├── "fork bomb"          → 资源耗尽攻击                             │
│  └── blocked_commands[]   → 自定义黑名单                             │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 3: IgnoreMatcher.IsIgnored() (文件操作工具)                     │
│                                                                     │
│  .wukongignore 文件 (gitignore 兼容语法):                             │
│  ├── 搜索优先级: CWD > HOME > CWD/.wukong/                          │
│  ├── 支持 negate 规则 (!)                                           │
│  ├── 提取工具参数中的文件路径 (file_read/write/replace/delete)        │
│  └── 匹配则拒绝访问                                                  │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 4: Guardrail Prompt Injection (全局)                           │
│                                                                     │
│  独立轻量 Runner 审查用户输入:                                        │
│  ├── 检测 Prompt Injection 模式                                      │
│  ├── 不影响主 Agent 性能（独立 runner）                               │
│  └── 启用时增加一定延迟                                               │
└─────────────────────────────────────────────────────────────────────┘
```

### 8.2 权限模式详解

| 模式 | 行为 | 适用场景 |
|------|------|----------|
| **auto** | 所有工具自动允许 | 可信环境、个人开发 |
| **smart** | 低风险自动，高风险（bash/file_write/delete/browser）需批准 | 日常开发（默认） |
| **manual** | 所有工具调用需手动批准 | 生产环境、敏感仓库 |
| **chat_only** | 拒绝所有工具调用 | 纯对话场景、演示 |

### 8.3 扩展安全

- **恶意代码扫描**：加载扩展前扫描代码模式（危险模式、可疑路径、隐藏文件删除）
- **命令执行超时**：默认 30s，最大 300s
- **allowlist/denylist**：工具级别的黑白名单，支持通配符（`developer/*`）

---

## 9. 上下文管理

### 9.1 ContextRevisionEngine

`ContextRevisionEngine`（`internal/agent/context.go`, 559 行）独立管理 token 预算。

**三层压缩策略**：

```
Layer 1: LLM 智能摘要
├── 使用独立轻量模型 (9B gemma-4-e4b-it)
├── 两种模式：
│   ├── 新鲜摘要: 对完整对话生成摘要
│   └── 渐进式摘要: 合并现有摘要 + 新消息
├── cooldown: 120s（避免频繁调用）
├── timeout: 30s
└── 触发条件（任一满足）：
    ├── 估算 token > max_context_tokens × (1 - trim_ratio)
    ├── 消息数 > 100
    └── 距上次修订 > 5分钟

Layer 2: 框架级压缩 (tRPC context_compaction)
├── Pass 1: 旧超大结果 → 占位符 (结果 < tool_result_max_tokens)
├── Pass 2: 剩余超大结果 → 首尾截断 (结果 > oversized_max_tokens)
├── keep_recent: 保留最近 N 个请求不压缩
├── force_clean_tools: 强制占位符化的工具列表
└── keep_tools: 始终保留结果的工具列表（如 memory_search）

Layer 3: 算法截断 (TruncateCommandOutput)
├── 保留命令输出的头部和尾部
├── 中间替换为截断提示
└── LLM 不可用时的安全回退
```

### 9.2 Token 追踪

```
token 估算策略:
├── 优先级 1: Response.Usage.TotalTokens (精确，tRPC 框架提供)
├── 优先级 2: Response.Usage.PromptTokens + CompletionTokens
├── 回退 1:   len(text) / 4 (字符数估算)
├── 回退 2:   messageCount × 100 (消息数估算)
└── 回退 3:   1024 (安全默认值)
```

---

## 10. LLM Provider 体系

### 10.1 Provider Factory

`provider.Factory`（`internal/provider/factory.go`, 268 行）统一管理所有 LLM 后端：

```
Factory
│
├── CreateDefaultModel()        → 使用 default_provider 配置
├── CreateModel(name)           → 按名称创建模型实例
├── CreateRevisionModel()       → 创建独立摘要模型
│   └── revisionModelAdapter    → 两种摘要策略
│       ├── 新鲜摘要: 直接对内容生成摘要
│       └── 渐进摘要: 检测 "[Existing Summary]" 前缀
├── CreateACPModel()            → 创建 ACP 远程代理模型
└── fillDefaultBaseURL()        → 自动填充已知 Provider 的 base URL
```

### 10.2 支持的 Provider

| Type | Provider | Base URL 自动填充 | 特性 |
|------|----------|-------------------|------|
| `openai` | OpenAI | `https://api.openai.com/v1` | GPT-4o, GPT-4-turbo |
| `anthropic` | Anthropic | `https://api.anthropic.com` | Claude 系列 |
| `google` | Google | 自动 | Gemini 系列 |
| `deepseek` | DeepSeek | `https://api.deepseek.com` | DeepSeek-Chat |
| `ollama` | Ollama | `http://localhost:11434/v1` | 本地推理 |
| `lmstudio` | LMStudio | `http://localhost:1234/v1` | 本地推理 |
| `acp` | ACP 代理 | 动态 | 远程代理 |

### 10.3 模型分工策略

```
模型分工（推荐配置）：
┌────────────────────┬───────────────────────┬───────────────────────┐
│ 角色               │ 推荐模型              │ 原因                   │
├────────────────────┼───────────────────────┼───────────────────────┤
│ 主对话模型         │ gemma-4-26b-a4b       │ 26B，质量优先          │
│ 记忆提取模型       │ qwen3.5-9b            │ 9B，速度优先           │
│ 上下文摘要模型     │ gemma-4-e4b-it        │ 4B，成本优先           │
│ 进化分析模型       │ 可配（默认复用主模型） │ 按需                   │
└────────────────────┴───────────────────────┴───────────────────────┘
```

---

## 11. 服务与协议层

### 11.1 多协议服务器

Wukong 同时启动三个协议服务器：

```
┌─────────────────────────────────────────────────────────────────────┐
│                        协议服务器全景                                │
│                                                                     │
│  ┌─────────────────┐  ┌─────────────────┐  ┌──────────────────┐    │
│  │ A2A Server      │  │ ACP Server      │  │ AG-UI Server     │    │
│  │ :9090           │  │ :9091           │  │ :8080            │    │
│  │                 │  │                 │  │                  │    │
│  │ Agent-to-Agent  │  │ Agent Client    │  │ SSE 实时对话     │    │
│  │ 标准通信        │  │ Protocol        │  │                  │    │
│  │                 │  │                 │  │ CopilotKit       │    │
│  │ tRPC-A2A-Go     │  │ 自研实现        │  │ TDesign Chat     │    │
│  └────────┬────────┘  └────────┬────────┘  └────────┬─────────┘    │
│           │                    │                    │               │
│  ┌────────┴────────────────────┴────────────────────┴─────────┐    │
│  │              ACP MCP Bridge (:3400)                         │    │
│  │  Wukong 扩展 → MCP Server → ACP 代理工具发现                 │    │
│  └─────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────┘
```

### 11.2 A2A Server

- 基于 tRPC-A2A-Go 框架
- 自动处理协议转换和流式支持
- Agent 描述："Wukong AI Agent - A2A service endpoint"

### 11.3 ACP Server

- 自研 ACP 协议实现
- 支持 SSE 流式响应
- 可选认证方式：API Key / JWT
- 路径：`/acp`

### 11.4 AG-UI Server

- SSE 实时对话服务
- 支持 CopilotKit / TDesign Chat 等 AG-UI 协议客户端
- 路径：`/agui`

### 11.5 ACP MCP Bridge

将 Wukong 扩展暴露给 ACP 代理提供商：
- ACP 代理可以调用 Wukong 扩展工具
- 实现跨 Agent 的工具共享
- 路径：`/mcp`

---

## 12. 存储架构

### 12.1 统一单文件数据库

Wukong 的所有存储合并为一个 SQLite 文件 `wukong.db`：

```
wukong.db (SQLite WAL 模式)
│
├── sessions                   ← tRPC Session 表
├── session_events             ← 对话事件流
├── memories                   ← tRPC Memory 表
├── chat_recall                ← 回溯索引 (含 FTS5 全文索引)
├── chat_recall_fts            ← FTS5 虚拟表
├── todos                      ← 任务追踪
│
├── [CortexDB 表]
├── memoryflow_transcripts     ← 对话转录
├── entities                   ← 知识图谱实体
├── relations                  ← 知识图谱关系
├── HNSW 向量索引              ← 语义搜索
│
├── [Evolution 表]
├── skill_versions             ← 技能版本
├── evolution_records          ← 进化记录
│
└── [ImportFlow 表]
    └── import_*               ← 结构化导入数据
```

### 12.2 连接管理

- **DatabasePool**：共享数据库连接池
- **MaxOpenConns=1**：SQLite WAL 模式下避免锁竞争
- **WAL 模式**：支持并发读
- **共享策略**：所有子系统共享同一个 `*sql.DB`

### 12.3 Session 后端

| 后端 | 特点 | 适用场景 |
|------|------|----------|
| **sqlite**（默认） | 零配置，单文件持久化 | 本地使用 |
| **redis** | 高性能，分布式 | 多实例部署 |
| **memory** | 最快，重启丢失 | 测试开发 |

---

## 13. 技能自进化系统

### 13.1 Evolution Engine

`EvolutionEngine`（`internal/evolution/engine.go`, 400 行）实现技能的自进化闭环：

```
执行追踪采集 → 异步分析队列 → LLM 分析 → 补丁生成 → 版本管理 → 热重载
```

**核心设计**：

```go
type EvolutionEngine struct {
    analyzer   *EvolutionAnalyzer    // LLM 分析器
    patcher    *EvolutionPatcher     // 补丁应用器
    store      *VersionStore         // 版本存储
    refresher  SkillRefresher       // 热重载接口
    analysisCh chan *ExecutionTrace  // 分析队列 (buffer 64)
}
```

### 13.2 进化管线

```
RecordExecution(trace)
    │
    ├── 过滤: 仅处理 skill_ 前缀的追踪
    ├── 非阻塞发送到 analysisCh
    │
    ▼
analysisWorker()  [后台 goroutine]
    │
    ├── 质量检查: Success + ErrorCount==0 + QualityScore>0.8 → 跳过
    ├── 冷却检查: cooldown 未到 → 跳过
    ├── 每日限制: 超过 max_patches_per_day → 跳过
    ├── 读取当前 SKILL.md
    ├── LLM 分析: 获取 EvolutionSuggestion (置信度/问题类型/建议补丁)
    ├── 置信度检查: < min_confidence → 仅记录
    ├── 应用补丁: EvolutionPatcher.ApplyPatch()
    │   ├── 备份原文件 (版本管理)
    │   ├── 应用补丁内容
    │   └── 更新版本号
    ├── 热重载: SkillRefresher.Refresh()
    └── 记录进化: store.RecordEvolution()
```

### 13.3 安全约束

| 约束 | 默认值 | 说明 |
|------|--------|------|
| `min_confidence` | 0.7 | 接受补丁的最低置信度 |
| `cooldown_period` | 30m | 同技能两次修补最短间隔 |
| `max_patches_per_day` | 10 | 每技能每日最大修补数 |
| `max_patch_size` | 8192 | 补丁最大字节数 |
| `max_versions_kept` | 10 | 保留历史版本数 |
| `auto_patch` | false | 是否自动应用（false=仅记录建议） |

---

## 14. 数据流

### 14.1 完整对话处理流

```
用户输入 → CLI (session.go)
  │
  ├── [Bootstrap] bootstrapSession()
  │   └── 初始化 27 步子系统依赖
  │
  └── [Execute] CoreLoop.Run(userID, sessionID, message)
      │
      ├── [Memory Before]
      │   ├── PrepareContext()        ← 上下文压缩
      │   ├── recallStore.Store()     ← 存储消息
      │   ├── memoryFlow.IngestTurn() ← 转录用户
      │   ├── memoryFlow.WakeUp()     ← 唤醒上下文
      │   └── memoryService.ReadMem() ← 读取记忆
      │
      ├── [Agent Execute]
      │   ├── runner.Run()
      │   │   ├── LLM 推理 (26B gemma-4-26b-a4b)
      │   │   ├── Tool Calls (50+ tools)
      │   │   │   └── Security Guard (4层检查)
      │   │   ├── AutoExtract (9B, 异步)
      │   │   └── SummaryJob (9B, 异步)
      │   └── 流式事件处理
      │
      └── [Memory After]
          ├── memoryFlow.IngestTurn(asst) ← 转录助手
          ├── memoryFlow.PromoteFacts()   ← 事实提升
          ├── recallStore.Store(asst)     ← 存储回复
          └── contextMgr.AfterRun()       ← 更新 token
```

### 14.2 记忆系统闭环

```
┌─────────────────────────────────────────────────────────────┐
│          记忆系统数据流 (每次 Agent.Run)                      │
├───────────────┬─────────────────────────────────────────────┤
│ 记录层         │ IngestTurn(user + assistant)                │
│               │ → wukong.db (memoryflow_transcripts)         │
├───────────────┼─────────────────────────────────────────────┤
│ 提取层         │ AutoExtract (9B qwen3.5-9b, 异步)           │
│               │ → wukong.db (memories)                       │
│               │ PromoteFacts → 桥接                          │
├───────────────┼─────────────────────────────────────────────┤
│ 召回层         │ WakeUp: 向量+词汇搜索 memoryflow_transcripts │
│               │ ReadMemories: SQL 读取 memories              │
├───────────────┼─────────────────────────────────────────────┤
│ 注入层         │ WakeUp 文本 → message 前缀                   │
│               │ 持久记忆 → message 前缀                      │
└───────────────┴─────────────────────────────────────────────┘
```

### 14.3 关闭序列

```
CoreLoop.Close()
│
├── 1. runner.Close()           ← 停止 Agent Runner
├── 2. evolution.Close()        ← 停止进化引擎后台 worker
├── 3. memory.Close()           ← 停止记忆 AutoExtract worker
├── 4. session.Close()          ← 关闭会话存储
├── 5. telemetry.Shutdown()     ← 刷新 OTLP 数据
└── 6. dbPool.Close()           ← 关闭数据库连接池
    └── 确保 WAL checkpoint + 所有 pending writes 完成
```

---

## 15. 技术选型

| 类别 | 选择 | 版本 | 原因 |
|------|------|------|------|
| **Agent 框架** | tRPC-Agent-Go | v1.10.0 | 多 Agent 编排、工具调用、Session/Memory/Planner 抽象 |
| **MCP 框架** | tRPC-MCP-Go | v0.0.16 | stdio/sse/streamable 三传输，完整的 MCP 客户端 |
| **A2A 框架** | tRPC-A2A-Go | v0.2.5 | Agent-to-Agent 标准通信 |
| **智能记忆** | CortexDB | v2.25.0 | 单文件部署、向量+FTS5+KG+RDF/SPARQL |
| **LLM Provider** | OpenAI 兼容 API | — | 统一接口，支持 7 种 Provider |
| **数据库** | SQLite WAL | v1.14.32 | 零配置、单连接、FTS5 全文搜索、WAL 并发读 |
| **前端** | BubbleTea + LipGloss | v1.x | 终端 TUI 交互，原生 Go |
| **浏览器** | Chromedp | v0.15.1 | Chrome DevTools 协议 |
| **JS 引擎** | goja | latest | 纯 Go JavaScript 运行时，无需 cgo |
| **可观测** | OpenTelemetry + Langfuse | v1.43.0 | 分布式追踪 + LLM 调用分析 |
| **配置** | Viper + Cobra | v1.20.1 / v1.9.1 | CLI + ENV + YAML 多级覆盖 |
| **Redis** | go-redis | v9.12.1 | 可选 Session 后端 |

---

## 16. 关键设计决策 (ADR)

| ADR | 决策 | 理由 | 影响 |
|-----|------|------|------|
| **ADR-1** | SQLite 共享池 WAL，MaxOpenConns=1 | 零配置+并发读+FTS5，避免锁竞争 | 单进程友好，多进程需外部分布式锁 |
| **ADR-2** | 分离记忆系统（双引擎） | tRPC Memory (键值) + MemoryFlow (转录) 互补不替代 | 记忆闭环完整，但子系统复杂度增加 |
| **ADR-3** | 辅助模型摘要 | 独立轻量模型做上下文压缩，节省主模型 token | 需要额外下载模型，但成本效益显著 |
| **ADR-4** | CortexDB HNSW 索引 | 向量搜索 O(log N)，替代全表扫描余弦相似度 | 首次索引构建开销，但查询性能大幅提升 |
| **ADR-5** | 扩展管理器 + MCP Broker | 动态发现/启用/禁用扩展，统一代理接口 | 工具数量不膨胀（50+ → 4 入口），管理灵活 |
| **ADR-6** | 冷启动友好 | 无 embedding 时自动回退 FTS5，无 LLM 时回退启发式 | 开箱即用，渐进增强 |
| **ADR-7** | 单文件数据库 | 5 个 DB 合并为 wukong.db | 跨系统查询可行，部署简单，备份一个文件 |
| **ADR-8** | 记忆 TTL 自动清理 | 启动时清除 30 天前旧记忆 | 防止无限膨胀，可能丢失低频但有价值的信息 |
| **ADR-9** | Extractor 回退链 | 专用模型 → 默认模型 → 禁用 auto_extract | 保证核心功能在任何配置下可用 |
| **ADR-10** | 非阻塞 Evolution | Background goroutine + channel queue (buffer 64) | 不影响主 Agent 循环，丢失的追踪可接受 |
| **ADR-11** | CoreLoop 依赖注入 | 通过 CoreLoopConfig 注入 27 个依赖 | 可测试、可替换，但初始化代码较长 |
| **ADR-12** | 多协议服务器同时运行 | A2A + ACP + AG-UI 三端口 | 覆盖所有集成场景，端口管理需注意冲突 |
