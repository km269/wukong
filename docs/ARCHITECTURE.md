# Wukong 系统架构文档

> **版本**: v0.6.0 | **Go**: 1.26 | **总源文件**: 120 (.go) + 31 (_test.go) | **依赖**: 30 direct
>
> 基于 [tRPC-Agent-Go v1.10.0](https://github.com/trpc-group/trpc-agent-go) · [tRPC-MCP-Go v0.0.16](https://github.com/trpc-group/trpc-mcp-go) · [tRPC-A2A-Go v0.2.5](https://github.com/trpc-group/trpc-a2a-go) · [CortexDB v2.25.0](https://github.com/liliang-cn/cortexdb)
>
> **记忆系统**: 双引擎 (tRPC Memory + CortexDB MemoryFlow) | HNSW 向量索引 | 智能上下文压缩 | 知识图谱 (RDF/SPARQL)

---

## 1. 系统概览

```
                          Wukong AI Agent Platform
┌─────────────────────────────────────────────────────────────────────┐
│                        CLI Layer (cobra)                             │
│  root.go │ session.go │ run.go │ configure.go │ extension.go │ ...  │
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
└─────────────────────────────────────────────────────────────────────┘
```

---

## 2. 子系统架构

### 2.1 核心执行引擎 (`internal/agent/`)

| 文件 | 职责 | 关键类型 |
|------|------|----------|
| `loop.go` | 主执行循环 — Run/RunStream/RunUserMessage/Close | `CoreLoop`, `CoreLoopConfig` |
| `context.go` | 上下文窗口管理 — PrepareContext/FilterIrrelevant/ProgressiveSummarize | `ContextRevisionEngine`, `ContextManager` |
| `workflow.go` | 多模式编排 — single/sequential/parallel/loop/if/while/map/reduce/human/branch | `WorkflowBuilder`, `WorkflowMode` |
| `team.go` | 多 Agent 团队 — 并发子 Agent 协作 | `TeamBuilder` |
| `hitl.go` | 人机回环 — 中断/恢复/审批 | HITL hooks |
| `prompt_template.go` | 提示词模板 — 变量替换 `{{.WorkingDir}}` | `PromptTemplate` |
| `recipe.go` | YAML 配方子代理 — `.wukong/recipes/*.yaml` | `RecipeToolSet` |
| `todo_enforcer.go` | 任务完成强制器 — 确保 todos 在 final answer 前完成 | `todoEnforcer` |
| `dify.go` | Dify AI 平台集成 | `DifyAgent` |

**CoreLoop — 主执行循环：**

```
Run(userID, sessionID, message)
├── PrepareContext()           → 上下文窗口管理 + 摘要触发
├── recallStore.StoreMessage() → 存储消息到回溯索引
├── memoryFlow.IngestTurn()    → MemoryFlow 转录记录
├── memoryFlow.WakeUp()        → 唤醒上下文注入 message 前缀
├── memoryService.ReadMemories() → tRPC 持久记忆注入 message 前缀
├── runner.Run()               → tRPC Agent 执行（含 AutoExtract）
├── memoryFlow.IngestTurn(asst)→ 记录助手回复
├── memoryFlow.PromoteFacts()  → 事实提取→tRPC Memory 桥接
└── contextMgr.AfterRun()      → 更新 token 计数
```

### 2.2 配置系统 (`internal/config/`)

| 结构体 | 配置段 | 字段数 |
|--------|--------|--------|
| `WukongConfig` | 根 | 38 Config |
| `ProviderConfig` | `providers[]` | 30+ |
| `AgentConfig` | `agent` | 25 |
| `SecurityConfig` | `security` | 8 |
| `SessionConfig` | `session` | 8 |
| `MemoryConfig` | `memory` | 8 |
| `TodoConfig` | `todo` | 5 |
| `RecallConfig` | `recall` | 8 |
| `CortexConfig` | `cortex` | 8 |
| `MemoryFlowConfig` | `memoryflow` | 6 |
| `GraphFlowConfig` | `graphflow` | 5 |
| `ImportFlowConfig` | `importflow` | 3 |
| `RevisionConfig` | `revision` | 10 |
| `ExtensionConfig` | `extensions[]` | 16 |

**加载优先级**：CLI flags > ENV (`WUKONG_`) > `./config.yaml` > `~/.config/wukong/config.yaml` > 内置默认值

### 2.3 LLM Provider 工厂 (`internal/provider/`)

```
Factory
├── CreateDefaultModel()       → 主对话模型 (26B gemma-4-26b-a4b)
├── CreateModel(name)          → 按名称创建模型
├── CreateRevisionModel()      → 上下文压缩模型 (9B qwen3.5-9b)
├── CreateACPModel()           → ACP 协议模型
└── revisionModelAdapter       → 辅助模型摘要适配器
    └── Summarize()            → 分层压缩 Prompt (首次/渐进)
```

**支持 7 种 Provider**：openai, anthropic, google, deepseek, ollama, lmstudio, acp

### 2.4 记忆系统 (`internal/memory/` + `internal/cortex/`)

#### tRPC Memory (SQLite — 键值记忆)

```
MemoryManager
├── Service() → memory.Service 接口
│   ├── Tools() → 6 个工具 (add/search/update/delete/load/clear)
│   ├── EnqueueAutoMemoryJob() → 异步记忆提取
│   └── ReadMemories() → 读取持久记忆
├── AutoExtract
│   ├── 提取模型: 9B qwen3.5-9b (extractor_provider=lmstudio)
│   ├── 超时: 120s
│   └── 触发: 每次 runner.Run() 后由框架自动触发
└── trackingMemoryService → 优雅关闭包装
```

#### CortexDB MemoryFlow (CortexDB — 对话转录 + 唤醒)

```
MemoryFlowService
├── IngestTurn(sessionID, userID, role, content)
│   └── 写入 memoryflow.db → Transcript 表
├── WakeUp(identity, query, sessionID)
│   ├── 召回: SearchMemory → 向量/词汇搜索
│   └── 返回: 分层唤醒文本 → 注入 message 前缀
└── PromoteFacts(sessionID, userID)
    └── 提取候选事实 → 桥接到 tRPC Memory
```

### 2.5 CortexDB 智能回溯 (`internal/cortex/`)

| 文件 | 阶段 | 功能 |
|------|------|------|
| `store.go` + `lexical.go` | P1 | CortexStore — 向量+FTS5 混合搜索 |
| `recall_manager.go` | P1 | 回溯工具 (recall_search/recall_sessions) |
| `embedder.go` | P1 | OpenAI 兼容嵌入客户端 |
| `memoryflow.go` | P2 | MemoryFlowService — 转录+唤醒+提升 |
| `planner.go` | P2 | LLMQueryPlanner — 检索策略规划 |
| `extractor.go` | P2 | LLMSessionExtractor — 事实提取 |
| `graphflow.go` | P3 | GraphFlowService — 知识图谱构建 |
| `kg_tools.go` | P3 | KGToolManager — SPARQL查询/分析 |
| `json_generator.go` | P3 | LLMJSONGenerator — JSON 结构化输出 |
| `import_flow.go` | P4 | ImportFlowService — DDL→KG 导入 |
| `import_tools.go` | P4 | ImportToolManager — 4 个导入工具 |

### 2.6 扩展系统 (`internal/extension/`)

```
Manager
├── Initialize()
│   ├── registerBuiltinLocked()  → 12 个内置扩展
│   └── registerExternalLocked() → MCP 外部扩展 (stdio/sse/streamable)
├── ToolSets() → 聚合所有扩展工具
└── ManagerToolSet → 4 个扩展管理工具

12 内置扩展:
├── developer          → 6 tools (read/write/edit/search/web/execute)
├── computer_controller → 9 tools (鼠标/键盘/截图/剪贴板)
├── memory             → 6 tools (add/search/update/delete/load/clear)
├── auto_visualiser    → 3 tools (图片/图表/流程图生成)
├── tutorial           → 3 tools (教程控制)
├── top_of_mind        → 4 tools (持久指令 CRUD)
├── code_mode          → 2 tools (JS 沙箱)
├── apps               → 5 tools (HTML 应用 CRUD)
├── web                → 1 tool (网页搜索)
├── agent_tools        → 3 tools (code-reviewer/summarizer/code-generator)
├── cortex             → 4 tools (KG查询/分析/DDL解析/计划)
└── mcp_broker         → 4 tools (list_servers/list_tools/inspect/call)
```

### 2.7 回溯与召回 (`internal/recall/`)

```
Store (SQLite + FTS5 + Vector)
├── StoreMessage()      → 写入 chat_recall 表
├── Search()            → FTS5 全文搜索 (BM25)
├── SearchBySession()   → 会话范围搜索
├── SearchHybrid()      → FTS5 + 嵌入余弦相似度重排
├── ListSessions()      → 历史会话列表
└── DeleteSession()     → 删除会话

RecallManager → 2 tools (recall_search/recall_sessions)
```

### 2.8 其他子系统

| 子系统 | 文件 | 职责 |
|--------|------|------|
| **Session** | `internal/session/` | 对话历史存储 (SQLite/Redis/InMemory) |
| **Security** | `internal/security/` | 4级权限 + Prompt注入防护 |
| **Browser** | `internal/browser/` | Chromedp 浏览器自动化 |
| **Knowledge** | `internal/knowledge/` | RAG 知识检索 |
| **Evolution** | `internal/evolution/` | 技能自进化引擎 |
| **CodeMode** | `internal/codemode/` | goja JS 沙箱 |
| **Apps** | `internal/apps/` | HTML 应用管理 |
| **Artifact** | `internal/artifact/` | 制品存储 (InMemory/COS) |
| **Summon** | `internal/summon/` | 子代理调度 |
| **Observability** | `internal/observability/` | OpenTelemetry + Langfuse |
| **Project** | `internal/project/` | 项目追踪 |
| **Eval** | `internal/eval/` | 回归测试评估 |
| **TUI** | `internal/cli/tui/` | BubbleTea 交互界面 |
| **Util** | `internal/util/` | DatabasePool + Logger + ConfigLoader |

---

## 3. 数据流

### 3.1 对话处理流

```
用户输入 → CLI (session.go)
  ├── 创建 CoreLoop
  ├── 注入 31 个子系统依赖
  └── CoreLoop.Run(userID, sessionID, message)
      ├── 上下文管理 (PrepareContext)
      │   ├── shouldRevise()? → EnqueueSummaryJob (异步压缩)
      │   └── context signal → ctxKeyRevision
      ├── 记忆系统
      │   ├── ReadMemories() → 持久记忆注入 message 前缀
      │   ├── WakeUp() → 唤醒上下文注入 message 前缀
      │   └── IngestTurn(user) → 转录记录
      ├── runner.Run() → tRPC Agent 执行
      │   ├── LLM 推理 (26B gemma-4-26b-a4b)
      │   ├── 工具调用 (50+ tools)
      │   ├── AutoExtract: 异步记忆提取 (9B qwen3.5-9b)
      │   └── EnqueueSummaryJob: 上下文压缩 (9B qwen3.5-9b)
      ├── 记忆收尾
      │   ├── IngestTurn(assistant) → 转录记录
      │   └── PromoteFacts() → 事实提升 → tRPC Memory
      └── AfterRun() → token 估算更新
```

### 3.2 记忆系统闭环

```
┌─────────────────────────────────────────────────────────────┐
│          记忆系统数据流 (每次 Agent.Run)                      │
├───────────────┬─────────────────────────────────────────────┤
│ 记录层         │ IngestTurn(user + assistant)                │
│               │ → memoryflow.db (Transcript 表)              │
├───────────────┼─────────────────────────────────────────────┤
│ 提取层         │ AutoExtract (9B qwen3.5-9b, 异步)           │
│               │ → wukong.db (memories 表)                   │
│               │ PromoteFacts → 桥接                          │
├───────────────┼─────────────────────────────────────────────┤
│ 召回层         │ WakeUp: 向量+词汇搜索 memoryflow.db          │
│               │ ReadMemories: SQL 读取 wukong.db            │
├───────────────┼─────────────────────────────────────────────┤
│ 注入层         │ WakeUp 文本 → message 前缀                   │
│               │ 持久记忆 → message 前缀                      │
└───────────────┴─────────────────────────────────────────────┘
```

---

## 4. 技术选型

| 类别 | 选择 | 原因 |
|------|------|------|
| **Agent 框架** | tRPC-Agent-Go v1.10.0 | 多 Agent 编排、工具调用、Session/Memory/Planner 抽象 |
| **MCP 框架** | tRPC-MCP-Go v0.0.16 | stdio/sse/streamable 三传输，20+ built-in tools |
| **智能记忆** | CortexDB v2.25.0 | 单文件部署、向量+FTS5+KG+RDF/SPARQL |
| **LLM Provider** | OpenAI 兼容 API | 支持 openai/anthropic/google/deepseek/ollama/lmstudio |
| **数据库** | SQLite WAL | 零配置、单连接、FTS5 全文搜索、WAL 并发读 |
| **前端** | BubbleTea + LipGloss | 终端 TUI 交互 |
| **可观测** | OpenTelemetry + Langfuse | 分布式追踪 + LLM 调用分析 |
| **配置** | Viper + Cobra | CLI + ENV + YAML 三级覆盖 |

---

## 5. 关键设计决策 (ADR)

| ADR | 决策 | 理由 |
|-----|------|------|
| ADR-1 | SQLite 共享池 WAL | 零配置+并发+FTS5，MaxOpenConns=1 避免锁竞争 |
| ADR-2 | 分离记忆系统 | tRPC Memory (键值) + MemoryFlow (转录) 互补不替代 |
| ADR-3 | 辅助模型摘要 | 独立轻量模型做上下文压缩，节省主模型 token |
| ADR-4 | CortexDB HNSW 索引 | 向量搜索 O(log N)，替代全表扫描余弦相似度 |
| ADR-5 | 扩展管理器 | 动态发现/启用/禁用 MCP 扩展，4级权限控制 |
| ADR-6 | 冷启动友好 | 无 embedding 时自动回退 FTS5，无 LLM 时回退启发式 |
| ADR-7 | 单文件数据库 | 5 个 DB 合并为 wukong.db，跨系统查询可行 |
| ADR-8 | 记忆 TTL 自动清理 | 启动时清除 30 天前旧记忆，防止无限膨胀 |
| ADR-9 | extractor 回退链 | 专用模型 → 默认模型 → 禁用 auto_extract |
