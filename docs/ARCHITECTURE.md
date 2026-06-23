# Wukong 系统架构深度分析

> **版本**: v0.9.0 | **Go**: 1.26 | **文件**: 174+ `.go` | **包**: 28+ | **依赖**: 32+
>
> 基于 [tRPC-Agent-Go v1.10.0](https://github.com/trpc-group/trpc-agent-go) · [tRPC-MCP-Go v0.0.16](https://github.com/trpc-group/trpc-mcp-go) · [tRPC-A2A-Go v0.2.5](https://github.com/trpc-group/trpc-a2a-go) · [CortexDB v2.25.0](https://github.com/liliang-cn/cortexdb)

---

## 目录

1. [架构哲学](#1-架构哲学)
2. [系统全景图](#2-系统全景图)
3. [CoreLoop 中央编排引擎](#3-coreloop-中央编排引擎)
4. [多 Agent 编排系统](#4-多-agent-编排系统)
5. [Recipe 子 Agent 系统](#5-recipe-子-agent-系统)
6. [双引擎记忆系统](#6-双引擎记忆系统)
7. [扩展与工具系统](#7-扩展与工具系统)
8. [安全纵深防御体系](#8-安全纵深防御体系)
9. [LLM Provider 体系](#9-llm-provider-体系)
10. [技能自进化系统](#10-技能自进化系统)
11. [子代理委派与技能系统](#11-子代理委派与技能系统)
12. [配置系统](#12-配置系统)
13. [服务与协议层](#13-服务与协议层)
14. [存储架构](#14-存储架构)
15. [应用管理与辅助模块](#15-应用管理与辅助模块)
16. [关键设计决策 (ADR)](#16-关键设计决策adr)
17. [技术选型](#17-技术选型)
18. [模块依赖关系图](#18-模块依赖关系图)

---

## 1. 架构哲学

Wukong 遵循五大核心哲学，决定所有工程决策：

| 哲学 | 核心信念 | 关键工程决策 |
|------|----------|-------------|
| **记忆优先** | Agent 智能源于跨会话知识积累 | 双引擎三层记忆：tRPC Memory + CortexDB Stack (HNSW+FTS5+RDF) |
| **框架组装** | 任何组件都应可替换 | CoreLoop 依赖注入，12 子系统接口隔离 |
| **多 Agent 原生** | 编排是第一公民 | 10 种显式编排模式 + HITL 人机协同 |
| **进化智能** | 技能应从失败中学习 | LLM 分析 → 自动补丁 → 版本管理 → 热重载 |
| **纵深防御** | 安全是多层协同 | 5 层防御：Guard → goja JS沙箱 → OS沙箱 → .wukongignore → OS权限 |

---

## 2. 系统全景图

```
┌──────────────────────────────────────────────────────────────────────┐
│                       Wukong AI Agent Platform                         │
├──────────────────────────────────────────────────────────────────────┤
│ Entry Points: CLI (cobra+TUI) │ A2A :9090 │ ACP :9091 │ AG-UI :8080  │
├──────────────────────────────────────────────────────────────────────┤
│ Core Engine: CoreLoop — 中央编排器 (12 子系统)                         │
│   WorkflowBuilder(10模式) · TeamBuilder · ContextManager(3层压缩)     │
│   Security Guard(5层) · HITL(中断-恢复) · TodoEnforcer(强制完成)      │
├──────────────────────────────────────────────────────────────────────┤
│ Agent Framework: tRPC-Agent-Go v1.10.0                                │
│   LLMAgent / ChainAgent / ParallelAgent / CycleAgent / GraphAgent      │
│   Planner / ToolSearch / ContextCompaction / Skill / Recipe            │
│   Session / Memory / Artifact / Telemetry Service                      │
│   6 Callbacks: BeforeModel / AfterModel / AfterTool / CodeExecution    │
├──────────────────────────────────────────────────────────────────────┤
│ Memory Stack (双引擎三层):                                              │
│ ┌─ 短期: MemoryFlow ──────────────────────────────────────────────┐   │
│ │  IngestTurn(转录) → WakeUp(语义唤醒, 3层上下文) → PromoteFacts   │   │
│ ├─ 中期: CortexStore ─────────────────────────────────────────────┤   │
│ │  HNSW向量搜索 + FTS5全文搜索 + 本地余弦相似度排序                 │   │
│ ├─ 长期: tRPC Memory ─────────────────────────────────────────────┤   │
│ │  AutoExtract(异步LLM提取) + SmartCleanup(70%新鲜度+30%长度)      │   │
│ └─ 结构化: GraphFlow ──────────────────────────────────────────────┘   │
│    auto_extract(每轮对话) → RDF知识图谱 → SPARQL查询                    │
├──────────────────────────────────────────────────────────────────────┤
│ Capability: Recipe(14功能) · 11内置扩展 · Evolution · Summon           │
│   CodeMode(goja JS) · Browser(Chromedp) · Knowledge(RAG) · ARD         │
│   Apps(Clone/Pack/Sanitize/Server/History) · ZIM库 · OS沙箱            │
├──────────────────────────────────────────────────────────────────────┤
│ Infrastructure: 7 LLM backends · OpenTelemetry · Langfuse              │
│   MultiPool(SQLite WAL) · fsnotify · text/template · golang.org/x/net  │
├──────────────────────────────────────────────────────────────────────┤
│ Storage: wukong.db (单文件, shared MultiPool)                           │
│   sessions / memories / recall(FTS5) / todos / projects                │
│   cortex(episodes/entities/relations/HNSW/FTS5/vectors)                │
│   apps_versions / skill_versions / evolution_records                   │
└──────────────────────────────────────────────────────────────────────┘
```

---

## 3. CoreLoop 中央编排引擎

`CoreLoop` (`internal/agent/loop.go`, 1560行) 是系统中央编排器：

### 3.1 结构定义

```go
type CoreLoop struct {
    agent          agent.Agent           // tRPC Agent实例
    runner         runner.Runner         // tRPC Runner
    sessionService session.Service       // 会话管理
    memoryService  memory.Service        // 持久记忆
    factory        *provider.Factory     // LLM工厂
    cfg            *config.WukongConfig  // 完整配置
    contextMgr     *ContextManager       // 上下文压缩
    security       *security.Guard       // 安全守卫
    recallStore    *recall.Store         // FTS5搜索
    cortexStore    *cortex.CortexStore   // HNSW向量
    memoryFlow     *cortex.MemoryFlowService  // 转录回溯
    graphFlow      *cortex.GraphFlowService   // 知识图谱
    closeFn        func() error          // 组合关闭
    mu sync.RWMutex; closed bool; bgWg sync.WaitGroup
}
```

### 3.2 初始化流程 (NewCoreLoop, 8步)

```
1. 收集 FunctionTools (文件读写、命令执行等)
2. 加载 YAML Recipe 子Agent工具集 (5阶段流水线)
3. 添加 tRPC Todo 工具 (todo_write)
4. 选择 Agent 模式:
   ├── 非single → WorkflowBuilder.Build()
   └── single → createSingleAgent()
       ├── PromptTemplate → buildSystemInstruction()
       ├── TopOfMind 持久化指令注入
       ├── 记忆预加载 (ReadMemories + 30字符滑动窗口去重)
       ├── ToolSets (扩展+内置+Recipe+Todo)
       ├── Planner (builtin/react)
       └── 6 Callbacks (BeforeModel/AfterModel/AfterTool等)
5. 创建 Runner (注入Session/Memory/Artifact服务)
6. 配置插件: ToolSearch + PromptInjection + TodoEnforcer + ContextCompaction
7. 创建 ContextManager + ContextRevisionEngine
8. 组装分层 closeFn (6步严格顺序)
```

### 3.3 执行循环 (4阶段)

```
Phase 1: 对话前准备 (上下文注入)
  ├── ContextManager.PrepareContext()    → 上下文压缩
  ├── recallStore.StoreMessage(user)     → [FTS5+HNSW]
  ├── cortexStore.StoreMessage(user)     → [HNSW向量]
  ├── memoryFlow.IngestTurn(user)        → [CortexDB Episode]
  ├── memoryFlow.WakeUp()                → [3层语义唤醒]
  ├── memoryService.ReadMemories()       → [持久记忆+去重]
  └── graphFlow.BuildContext()           → [KG增强上下文]

Phase 2: Agent 执行
  └── runner.Run()
      ├── LLM推理 (主模型)
      ├── Tool Calls → Guard.Check() → 执行
      ├── AutoExtract (异步, 轻量模型)
      └── SummaryJob (异步, 上下文摘要)

Phase 3: 对话后收尾
  ├── recallStore.StoreMessage(*) / cortexStore.StoreMessage(*)
  ├── memoryFlow.IngestTurn(assistant) → PromoteFacts → tRPC Memory
  ├── graphFlow auto_extract → RDF实体/关系
  └── contextMgr.AfterRun() → token统计

Phase 4: 返回响应
  └── contextMgr.AfterRun() → token统计更新
```

### 3.4 上下文压缩引擎 (ContextManager, 3层)

| 层 | 机制 | 说明 |
|----|------|------|
| Layer 1 | tRPC ContextCompaction 插件 | Pass1:旧工具结果→占位符; Pass2:头尾截断 |
| Layer 2 | ContextRevisionEngine LLM摘要 | 辅助模型生成结构化摘要, 冷却期120s |
| Layer 3 | 渐进式截断 | Revision配置: max_context_tokens 64000, trim_ratio 0.3 |

### 3.5 关闭序列 (6步严格顺序)

```
1. bgWg.Wait()                → 等待后台goroutine
2. runner.Close()             → 停止Agent Runner
3. evolution.Close()          → 停止进化引擎
4. memory.Close() (5s超时)    → 停止AutoExtract
5. session.Close() + graphFlow.Close()
6. telemetry.Shutdown(10s) + dbPool.Close() → WAL checkpoint
```

---

## 4. 多 Agent 编排系统

### 4.1 WorkflowBuilder (workflow.go, 637行)

| 模式 | 拓扑 | Agent类型 | 适用场景 |
|------|------|-----------|----------|
| `single` | 单Agent | LLMAgent | 日常对话(默认) |
| `chain` | planner→executor→reviewer | ChainAgent | 多步流水线 |
| `parallel` | 3视角并发 | ParallelAgent | 多角度分析 |
| `cycle` | planner↔executor | CycleAgent | 自我改进 |
| `graph` | 条件路由DAG | GraphAgent | 复杂决策 |
| `team_coordinator` | Leader委派 | Team | 团队协作 |
| `team_swarm` | 自动transfer | Team(Swarm) | 自主委派 |
| `claude_code` | Claude CLI | claudecode | 本地Claude |
| `codex` | Codex CLI | codex | 本地Codex |
| `dify` | Dify平台 | DifyAgent | 低代码 |

### 4.2 TeamBuilder (team.go, 301行)

- **team_coordinator**: Coordinator通过AgentTool调度成员, `WithEnableParallelTools(true)`
- **team_swarm**: 成员间`transfer_to_agent`, `WithCrossRequestTransfer(true)`
- 默认成员: researcher(研究) + coder(编程) + reviewer(审查)

### 4.3 Graph 高级特性

| 特性 | 配置 | 说明 |
|------|------|------|
| 流式模式 | `stream_mode: hub` | 节点间流式通信 |
| 节点缓存 | `cache_enabled: true` | 纯函数节点避免重复计算 |
| 执行引擎 | `engine: bsp/dag` | BSP同步屏障 / DAG有向无环图 |

### 4.4 HITL 人机协同 (hitl.go, 179行)

```
graph.AddInterruptBefore("dangerous_op")
  → 执行到危险节点前暂停
  → 用户审批
  → ResumeInterrupted → runner.Run() + agent.WithResume(true)
  → 从checkpoint恢复继续执行
```

---

## 5. Recipe 子 Agent 系统

基于YAML的结构化子Agent定义系统，5文件协同实现(1878行):

### 5.1 五阶段加载流水线

```
Phase 1: 加载配置 (文件+内联) → map[name]*RecipeConfig
Phase 2: 解析继承链 (extends) → resolveAllExtends (递归合并)
Phase 3: 拓扑排序 → topoSortRecipes (DAG+循环检测)
Phase 4: 按序构建 → recipeTool→retryTool→timeoutTool
Phase 5: 注册发现/热重载/统计工具
```

### 5.2 功能矩阵 (14项)

| 阶段 | 功能 | YAML字段 | 实现 |
|------|------|----------|------|
| P0-A | 参数化模板 | `prompt`, `parameters` | Go text/template渲染 |
| P0-B | 结构化输出 | `response.json_schema` | WithStructuredOutputJSONSchema |
| P1-A | 子配方组合 | `tools: [recipe-xxx]` | 拓扑排序+DAG构建 |
| P1-B | 重试与校验 | `retry`, `validate_output` | 指数退避+JSON校验 |
| P2-A | 内联配方 | `agent.inline_recipes` | YAML round-trip转换 |
| P2-B | 配方继承 | `extends` | 递归继承合并 |
| P3-A | 模型覆盖 | `model` | CreateModelWithName |
| P3-B | 超时控制 | `timeout` | context.WithTimeout |
| P3-C | 配方发现 | `list_recipes`工具 | JSON返回配方列表 |
| P3-D | 热重载 | `reload_recipes`+fsnotify | 500ms防抖自动重建 |
| P4-A | 指令模板 | `instruction: "{{.var}}"` | 与prompt共同渲染 |
| P4-B | 执行指标 | 自动收集 | 包装器内CallCount/Success/Error |
| P4-C | 统计工具 | `recipe_stats` | 指标查询 |

### 5.3 工具包装器链 (从内到外)

```
agenttool.NewTool → recipeTool(参数+模板+指标)
  → retryTool(指数退避重试+输出校验)
    → timeoutTool(context.WithTimeout)
```

---

## 6. 双引擎记忆系统

### 6.1 引擎一: tRPC Memory (SQLite KV, memory/store.go)

| 功能 | 实现 |
|------|------|
| MemoryManager | 包装tRPC memory.Service, 优雅关闭 |
| trackingMemoryService | 装饰器, 追踪活跃提取作业 |
| noCloseDBWrapper | 防止Close()关闭共享DB |
| AutoExtract | 每轮对话后异步LLM(轻量模型)提取事实 |
| SmartCleanup | ≥80%容量触发评分淘汰→60%, 评分: 70%新鲜度+30%内容长度 |

**6个工具**: memory_add/search/update/delete/load/clear

### 6.2 引擎二: CortexDB Stack (12文件)

#### CortexStore (store.go, 281行)
HNSW向量搜索+FTS5全文搜索。双写策略: FTS5(权威源)→HNSW向量(有embedder时)。搜索: HNSW优先→失败回退FTS5。

#### lexicalStore (lexical.go, 587行)
SQLite FTS5词法搜索+向量索引。本地余弦相似度: 取最近200条向量→排序→top-K→不足时FTS5补充。
Schema: `chat_recall` + `chat_recall_fts`(FTS5) + `chat_recall_vec`(向量) + 3个触发器(AFTER INSERT/DELETE/UPDATE)

#### MemoryFlow (memoryflow.go, 256行)
- IngestTurn() → CortexDB Episode
- WakeUp() → 3层上下文(身份/回忆/会话)
- PromoteFacts() → LLM提取→桥接tRPC Memory

#### GraphFlow (graphflow.go, 255行)
- auto_extract: 每轮对话后自动执行
- 流程: BuildTranscript→ExtractEntities→BuildGraph(RDF)
- 支持SPARQL查询

#### 辅助组件

| 文件 | 功能 | 行数 |
|------|------|------|
| extractor.go | LLM+启发式双重提取(3层回退) | 439 |
| embedder.go | OpenAI兼容文本向量客户端 | 138 |
| planner.go | LLM检索策略规划器 | 221 |
| json_generator.go | 实体/关系JSON生成 | 101 |
| recall_manager.go | 跨系统搜索工具管理器 | 155 |
| import_flow.go | ImportFlow服务 | 134 |
| import_tools.go | DDL/CSV导入工具 | 267 |
| kg_tools.go | 知识图谱查询/分析工具 | 162 |

### 6.3 记忆去重机制

`isMemoryDuplicated()`: 30字符滑动窗口, ≥60%重叠视为重复, 避免上下文冗余注入。

---

## 7. 扩展与工具系统

### 7.1 ExtensionManager (extension/manager.go)

- 动态注册: Deeplink URL或YAML配置
- 状态机: loading→enabled/disabled/error
- 环境变量注入: `${VAR}`自动展开
- 内存服务注入: 接口断言避免循环依赖

### 7.2 MCP Client (extension/mcp_client.go)

| 传输模式 | 说明 |
|----------|------|
| stdio | 命令行子进程, 支持include/exclude glob过滤 |
| SSE | HTTP SSE流, 会话重连支持 |
| Streamable HTTP | Streamable HTTP, 环境变量覆盖 |

### 7.3 ACP MCP Bridge (extension/acp_mcp.go)

- JSON-RPC 2.0 over HTTP, `:3400/mcp`
- 支持 `tools/list`, `tools/call`, `initialize`
- 请求体限制 10MB (DoS防护)

### 7.4 12 个内置扩展

| 扩展名 | 功能 | 注册状态 |
|--------|------|----------|
| `developer` | 文件读写、命令执行 | ✅ 始终启用 |
| `computer_controller` | Chromedp浏览器自动化 | ✅ browser.enabled联动 |
| `memory` | 记忆管理(6 tools) | ✅ 始终启用 |
| `auto_visualiser` | 自动可视化 | ✅ visualiser.enabled联动 |
| `tutorial` | 交互式教程 | ✅ tutorial.enabled联动 |
| `top_of_mind` | 持久指令注入 | ✅ top_of_mind.enabled联动 |
| `code_mode` | goja JS沙箱 | ✅ code_mode.enabled联动 |
| `apps` | HTML应用管理 | ✅ apps.enabled联动 |
| `web` | Web工具 | ✅ 始终启用 |
| `agent_tools` | 子Agent包装 | ✅ 始终启用 |
| `ard` | ARD资源发现 | ✅ ard.enabled联动 |
| `cortex` | CortexDB知识图谱 | ✅ cortex.enabled联动 |

---

## 8. 安全纵深防御体系

### 8.1 5层防御模型

```
Layer 5: Guard      → auto/smart/manual/chat_only + 命令拦截 + Prompt注入检测
Layer 4: goja JS    → API白名单 + 128MB + 5并发 + ReDoS防护 + 1MB输入限制
Layer 3: OS沙箱     → Landlock(linux) / sandbox-exec(macOS) / Low IL(Windows)
Layer 2: .wukongignore → gitignore兼容文件访问黑名单
Layer 1: OS权限     → 非root + ulimit
```

### 8.2 Guard (security/guard.go)

4种权限模式: `auto`(全自动) / `smart`(智能决策,默认) / `manual`(全部审批) / `chat_only`(纯文本)

### 8.3 goja JS沙箱 (codemode/executor.go)

| 措施 | 实现 |
|------|------|
| API白名单 | console/JSON/Math/__output |
| 显式禁用 | eval/Function/setInterval/Date/RegExp |
| 内存限制 | debug.SetMemoryLimit(128MB, 配置值80%) |
| 超时控制 | context.WithTimeout(10s)+VM中断 |
| 并发控制 | channel semaphore (max 5) |
| JSON保护 | JSON.parse 1MB输入限制 |

### 8.4 OS级沙箱 (pkg/sandbox/)

与`os/exec`完全兼容的API。跨平台: Linux(Landlock LSM, kernel 5.13+), macOS(sandbox-exec+Seatbelt), Windows(Low Integrity Level), 其他(WARN日志)。

---

## 9. LLM Provider 体系

### 9.1 Factory (provider/factory.go)

| Provider | type | 基础URL | SDK |
|----------|------|---------|-----|
| OpenAI | `openai` | api.openai.com | openai-go |
| Anthropic | `anthropic` | api.anthropic.com | openai-go(兼容) |
| Google | `google` | 自动 | openai-go(Gemini) |
| DeepSeek | `deepseek` | api.deepseek.com | openai-go |
| Ollama | `ollama` | localhost:11434 | openai-go |
| LMStudio | `lmstudio` | localhost:1234 | openai-go |
| ACP | `acp` | agent_url | HTTP client |

### 9.2 模型分工

| 用途 | 配置 | 回退 |
|------|------|------|
| 主对话 | CLI --provider/--model | — |
| 记忆提取 | memory.extractor_model | → lightweight_model |
| 上下文压缩 | revision.revision_model | → lightweight_model |
| 知识图谱 | graphflow.extractor_model | → lightweight_model |
| 检索规划 | memoryflow.planner_model | → lightweight_model |
| Recipe覆盖 | recipe.Model | → — |

---

## 10. 技能自进化系统

### 10.1 EvolutionEngine (evolution/, 6文件)

```
Agent执行 → ExecutionTrace
  → 异步分析队列(后台goroutine, 不阻塞主循环)
  → LLM Analysis (置信度≥0.7)
  → PatchSuggestion
  → 备份SKILL.md (版本管理, 保留10个历史版本)
  → 应用补丁 (最大8KB)
  → SkillManager.Refresh (热重载)
```

### 10.2 约束机制

| 约束 | 值 |
|------|-----|
| 最小置信度 | 0.7 |
| 冷却时间 | 30min |
| 每日上限 | 10 |
| 最大补丁 | 8KB |
| 版本保留 | 10 |

---

## 11. 子代理委派与技能系统

### 11.1 SummonManager (summon/)

- 子代理包装为可调用工具 (llmagent构建)
- 温度0.3, 最大LLM调用10次
- 并发控制(max_concurrent: 5)
- A2A远程代理支持 (agenttool.NewTool包装)

### 11.2 SkillManager (skill/manager.go)

- 从.skills目录加载SKILL.md文件
- 自动加载(auto_load) + 热重载
- 通过SkillRefresher接口与Evolution联动

### 11.3 A2A Server (summon/a2a.go)

- tRPC-A2A-Go标准通信
- API Key认证(`X-API-Key`)
- A2A远程代理配置列表(`a2a_remotes`)

---

## 12. 配置系统

### 12.1 加载优先级 (7级)

```
1. CLI参数  2. 环境变量(WUKONG_前缀)  3. --config指定文件
4. ./config.yaml  5. ~/.config/wukong/config.yaml
6. /etc/wukong/config.yaml  7. 内置默认值
```

### 12.2 配置段分类 (30+, 38结构体)

| 类别 | 配置段 | 结构体数 |
|------|--------|----------|
| 全局 | log_level, default_provider, lightweight_*, providers | 2 |
| 核心 | agent(35+字段), security(12字段) | 3 |
| 存储 | session, memory, todo, recall | 4 |
| 记忆 | cortex, memoryflow, graphflow, importflow | 4 |
| 上下文 | revision(11字段) | 1 |
| 工具 | browser, visualiser, tutorial, top_of_mind, code_mode, apps, ard | 7 |
| 编排 | summon, skill, evolution, knowledge, workflow, dify | 6 |
| 服务 | a2a_server, agui, acp_server, acp_mcp | 4 |
| 观测 | telemetry, observability, eval, artifact | 4 |
| 其他 | extensions, project_dir | 2 |

---

## 13. 服务与协议层

### 13.1 协议端点

| 协议 | 端口 | 路径 | 用途 | 实现 |
|------|------|------|------|------|
| A2A | 9090 | / | Agent-to-Agent标准通信 | summon/a2a.go |
| ACP | 9091 | /acp | Agent Client Protocol | server/acp.go |
| AG-UI SSE | 8080 | /agui | Web UI实时对话(SSE流) | server/agui.go |
| ACP MCP | 3400 | /mcp | 跨协议工具桥接 | extension/acp_mcp.go |

### 13.2 CLI命令体系

| 命令 | 说明 | 文件 |
|------|------|------|
| `session` | 交互会话(BubbleTea TUI) | cli/session.go(1317行) |
| `run` | 非交互式单次执行 | cli/run.go |
| `configure` | 交互式配置向导 | cli/configure.go |
| `extension` | 扩展管理 | cli/extension.go |
| `eval` | 代理评估 | cli/eval.go |
| `project(s)` | 项目管理 | cli/project.go |
| `version` | 版本信息 | cli/version.go |
| `completion` | Shell自动补全 | cli/root.go |

### 13.3 Bootstrap 流程 (28步严格顺序)

```
1.配置加载→2.日志级别→3.配置验证→4.OpenTelemetry→5.内置扩展注册
→6.CLI覆盖→7.模型工厂→8.MultiPool→9.会话服务→10.记忆提取模型
→11.记忆管理器→12.SmartCleanup→13.安全守卫→14.扩展管理器
→15.记忆服务注入→16.扩展工具集→17.ACP MCP桥接→18.Recall/Cortex
→19.MemoryFlow→20.Recall管理器→21.GraphFlow→22.ImportFlow
→23.TopOfMind→24.CodeMode→25.Apps→26.Agent工具集→27.Summon→28.Skill
```

---

## 14. 存储架构

### 14.1 单文件数据库

```
wukong.db (SQLite WAL, shared MultiPool)
├── sessions              ← Session Service
├── memories              ← tRPC Memory Service
├── recall                ← FTS5全文搜索 Store
│   ├── chat_recall         (消息表)
│   ├── chat_recall_fts     (FTS5虚拟表, unicode61分词)
│   └── chat_recall_vec     (向量表, JSON格式)
├── todos                 ← Todo Store
├── projects              ← Project Manager
├── cortex_*              ← CortexDB (episodes/entities/relations/HNSW)
├── evolution_*           ← Evolution (skill_versions/evolution_records)
├── app_versions          ← Apps History (max 20版本)
└── extension_*           ← Extension Manager registry
```

### 14.2 DatabasePool / MultiPool

| 参数 | 值 | 说明 |
|------|-----|------|
| `_journal_mode` | WAL | 写前日志, 支持并发读 |
| `_synchronous` | NORMAL | 平衡性能与安全 |
| `_foreign_keys` | ON | 外键约束 |
| `_busy_timeout` | 5000ms | 忙等待超时 |
| `MaxOpenConns` | 4 | 最大打开连接 |
| `MaxIdleConns` | 2 | 最大空闲连接 |

关闭时执行 `PRAGMA wal_checkpoint(TRUNCATE)`。

---

## 15. 应用管理与辅助模块

### 15.1 Apps Manager (apps/, 5子目录)

| 子目录 | 文件 | 功能 |
|--------|------|------|
| `clone/` | 4文件 | Git克隆 + ZIP解压 + 缓存管理 |
| `pack/` | 4文件 | HTML打包(ZIP/ZIM格式) |
| `sanitize/` | 1文件 | HTML DOM清洗(golang.org/x/net/html) |
| `server/` | 1文件 | 本地HTTP预览服务 |
| `mcpapps/` | 4文件 | MCP Apps协议(JSON-RPC桥+沙箱) |
| 根 | manager.go + history.go | 生命周期 + 版本历史(max 20版本) |

### 15.2 ARD 系统 (ard/, 15文件)

Agentic Resource Discovery — AI Agent/MCP Server资源发现:
- URN解析与构建 (urn.go)
- 目录管理 (catalog.go, explore.go)
- 远程注册表 (registry.go)
- 联邦搜索 (federation.go)
- 密码学验证: Ed25519/ECDSA/RSA (trust/)
- 工具集 (tools.go)
- HTTP服务 (server.go, client.go)

### 15.3 ZIM 公共库 (pkg/zim/, 4文件)

- Packer: 构建ZIM文件(AddContent/Redirect/Metadata/SetMainPage/Build/WriteTo)
- Reader: 读取ZIM(Open/Get/EntryAt/MainPage/Count/MimeTypes)
- 压缩: zstd(文本簇), 不压缩(二进制簇)
- 校验和: 完整文件MD5, 确定性UUID

### 15.4 其他辅助模块

| 模块 | 功能 |
|------|------|
| telemetry/ | OpenTelemetry分布式追踪 |
| observability/ | Langfuse LLM追踪 |
| health/ | 健康检查 (/health/live/ready) |
| artifact/ | 制品存储(inmemory/COS) |
| eval/ | Agent评估与回归测试 |
| project/ | 工作目录追踪 |
| topofmind/ | 持久化指令注入 |
| util/ | DatabasePool/MultiPool/Logger |

---

## 16. 关键设计决策 (ADR)

| ADR | 决策 | 影响 |
|-----|------|------|
| ADR-1 | SQLite WAL共享池, MaxOpenConns=4 | 单文件部署+并发+跨系统查询零成本 |
| ADR-2 | 双引擎记忆(tRPC+CortexDB) | KV持久化+向量/图谱结构化 |
| ADR-3 | 轻量模型分工 | 节省主模型token |
| ADR-4 | CortexDB WAL共享 | 避免多连接冲突 |
| ADR-5 | MemoryFlow→tRPC Bridge | 转录事实自动提升为持久记忆 |
| ADR-6 | MCP Broker 4入口模式 | 防止工具泛滥 |
| ADR-7 | 冷启动友好降级(无Embedding→FTS5) | 渐进式增强 |
| ADR-8 | 单文件数据库 | 部署简单、备份方便 |
| ADR-9 | SmartCleanup(70%新鲜度+30%长度) | 合理淘汰策略 |
| ADR-10 | 记忆去重30字符滑动窗口 | 轻量高效 |
| ADR-11 | Tool消息完整索引(FTS5+HNSW) | 不丢失工具上下文 |
| ADR-12 | GraphFlow auto_extract | 每轮对话自动KG |
| ADR-13 | Extractor 3层回退链 | 专用模型→默认→启发式 |
| ADR-14 | 非阻塞Evolution(后台goroutine) | 不阻塞主循环 |
| ADR-15 | CoreLoop依赖注入 | 12子系统接口隔离 |
| ADR-16 | 四协议服务器(A2A/ACP/AG-UI/MCP) | 多场景覆盖 |
| ADR-17 | HTTP body 10MB限制 | DoS防护 |
| ADR-18 | goja JS多层沙箱 | API白名单+内存+并发+ReDoS |
| ADR-19 | Context超时覆盖 | 防止goroutine泄漏 |
| ADR-20 | bgWg后台goroutine管理 | 关闭前Wait() |
| ADR-21 | OS级沙箱跨平台(Landlock/Seatbelt/LowIL) | 无需Docker |
| ADR-22 | sandbox独立pkg/包 | 外部项目可复用 |
| ADR-23 | Recipe拓扑排序 | DAG依赖+循环检测 |
| ADR-24 | MultiPool命名池管理 | 子系统可独立或共享DB |
| ADR-25 | ARD联邦搜索 | 本地+远程去重合并 |
| ADR-26 | Sanitize DOM树遍历(含正则回退) | 安全HTML清洗 |
| ADR-27 | Apps版本历史(max 20) | 非破坏性更新 |
| ADR-28 | 提示模板分离(system_prompt_dir) | 自定义系统指令 |

---

## 17. 技术选型

| 类别 | 选择 | 版本 | 理由 |
|------|------|------|------|
| Agent框架 | tRPC-Agent-Go | v1.10.0 | 多Agent编排/Session/Memory/Planner/Skill/TodoEnforcer |
| MCP协议 | tRPC-MCP-Go | v0.0.16 | stdio/SSE/Streamable三传输 |
| A2A协议 | tRPC-A2A-Go | v0.2.5 | Agent间标准通信 |
| 智能记忆 | CortexDB | v2.25.0 | HNSW+FTS5+RDF/SPARQL |
| JS引擎 | goja | latest | 纯Go零CGO, 沙箱友好 |
| OS沙箱 | pkg/sandbox | 自维护 | Landlock/Seatbelt/LowIL |
| 数据库 | SQLite WAL | modernc/mattn | 单文件零配置 |
| TUI | BubbleTea+LipGloss | latest | 纯Go, 零外部依赖 |
| 浏览器 | Chromedp | latest | Chrome DevTools |
| 配置 | Viper+Cobra | latest | CLI>ENV>YAML |
| 可观测 | OpenTelemetry+Langfuse | latest | 全链路+LLM专用 |
| 文件监听 | fsnotify | v1.8.0 | Recipe热重载 |
| 模板 | text/template | stdlib | Recipe渲染 |
| HTML清洗 | golang.org/x/net/html | latest | DOM树安全遍历 |
| 压缩 | zstd(kla uspost) | v1.18.6 | ZIM文本簇 |
| UUID | google/uuid | v1.6.0 | 确定性UUID |
| 缓存 | go-redis/v9 | v9.12.1 | Session Redis(可选) |
| 语言 | Go | 1.26 | 跨平台单二进制 |

---

## 18. 模块依赖关系图

```
cmd/wukong/main.go
  └── cli/Execute()
        ├── cli/root.go (cobra 9子命令)
        ├── cli/session.go (bootstrapSession, 28步)
        │     ├── config/config.go (Viper配置, 38结构体)
        │     ├── provider/factory.go (7 LLM后端)
        │     ├── util/database.go (MultiPool)
        │     ├── session/ (会话存储)
        │     ├── memory/store.go (长期记忆)
        │     ├── security/guard.go (安全守卫)
        │     ├── extension/manager.go + builtin/* (12扩展)
        │     │     ├── extension/mcp_client.go
        │     │     └── extension/acp_mcp.go
        │     ├── recall/store.go + tool.go
        │     ├── cortex/{store,lexical,memoryflow,graphflow,...} (12文件)
        │     ├── topofmind/mind.go
        │     ├── codemode/executor.go
        │     ├── apps/{manager,history,clone,pack,sanitize,server}(5子目录)
        │     ├── summon/ (子代理委派+A2A)
        │     ├── skill/manager.go
        │     └── evolution/engine.go
        ├── agent/loop.go (CoreLoop)
        │     ├── agent/workflow.go (10种编排)
        │     ├── agent/team.go (多Agent团队)
        │     ├── agent/context.go (上下文压缩)
        │     ├── agent/hitl.go (人机协同)
        │     ├── agent/{prompt_template,dify,todo_enforcer}.go
        │     └── agent/recipe*.go (5文件Recipe系统)
        ├── server/{agui,acp}.go
        ├── knowledge/manager.go
        ├── browser/controller.go
        ├── eval/eval.go
        ├── telemetry/ + observability/
        ├── artifact/
        ├── health/health.go
        └── project/manager.go
              ↓
        pkg/sandbox/ (跨平台OS沙箱)
        pkg/zim/ (ZIM格式库)
```
