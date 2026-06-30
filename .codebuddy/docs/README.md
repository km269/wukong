# Wukong — 架构哲学与核心特性

> **Go**: 1.26 | **文件**: 224 `.go` (51 `_test.go`) | **包**: 28 内部 + 2 公共 | **许可证**: GNU AGPL-3.0
>
> 基于 [tRPC-Agent-Go v1.10.0](https://github.com/trpc-group/trpc-agent-go) · [tRPC-MCP-Go v0.0.16](https://github.com/trpc-group/trpc-mcp-go) · [tRPC-A2A-Go v0.2.5](https://github.com/trpc-group/trpc-a2a-go) · [CortexDB v2.25.0](https://github.com/liliang-cn/cortexdb)

---

## 1. 五大架构哲学

Wukong 遵循五大核心哲学，决定所有工程决策：

### 1.1 记忆优先（Memory-First）

**核心信念**: Agent 智能源于跨会话的知识积累，而非单次对话的表现。

**实现方案**: 双引擎三层记忆架构
- **短期**: MemoryFlow — 转录记录 + WakeUp 上下文生成（身份/回忆/会话 3 层）
- **中期**: CortexStore — HNSW 向量索引 + FTS5 全文搜索 + 本地余弦相似度
- **长期**: tRPC Memory — AutoExtract 异步 LLM 提取 + SmartCleanup 容量淘汰
- **结构化**: GraphFlow — 每轮对话自动提取实体/关系 → RDF 知识图谱 → SPARQL

### 1.2 框架组装（Framework Assembly）

**核心信念**: 任何组件都应可替换，不绑定特定实现。

**实现方案**: CoreLoop 依赖注入体系
- 12 个子系统通过 `CoreLoopConfig` 注入，而非硬编码
- Session/Memory 支持 SQLite、Redis、内存三种后端
- LLM 支持 7 种 Provider 统一接口
- 扩展系统通过 ToolSet 接口统一管理

### 1.3 多 Agent 原生（Multi-Agent Native）

**核心信念**: 编排是第一公民，单 Agent 只是多 Agent 的特例。

**实现方案**: 10 种编排模式 + HITL
- 5 种基础模式: single / chain / parallel / cycle / graph
- 2 种团队模式: team_coordinator / team_swarm
- 3 种外部集成: claude_code / codex / dify
- HITL 人机协同: 在关键决策点暂停等用户确认

### 1.4 进化智能（Evolutionary Intelligence）

**核心信念**: 技能应从失败中学习，持续自我改进。

**实现方案**: Evolution 引擎
- LLM 分析执行失败原因
- 自动生成修复补丁
- 版本管理系统追踪变更
- `fsnotify` 热重载新版本

### 1.5 双向发现（Bidirectional Discovery）

**核心信念**: 发现别人，也被人发现。

**实现方案**: ARD (Agentic Resource Discovery)
- Outbound: 联邦搜索远程 Registry
- Inbound: RegistryServer 发布自身
- Auto: MCP 连接和 A2A Remote 自动注册

---

## 2. 核心特性详解

### 2.1 智能记忆系统

```
┌─────────────────────────────────────────────────────────┐
│                    Memory Stack                         │
├───────────┬───────────┬──────────┬──────────────────────┤
│  短期      │  中期      │  长期    │  结构化               │
│ MemoryFlow │ CortexStore│ tRPC     │ GraphFlow            │
│           │           │ Memory   │                      │
├───────────┼───────────┼──────────┼──────────────────────┤
│ 转录记录   │ HNSW 向量  │ 关键事实  │ RDF 知识图谱          │
│ WakeUp    │ FTS5 全文  │ AutoExtract│ SPARQL 查询         │
│ 3层上下文  │ 余弦相似度 │ SmartCleanup│ auto_extract       │
└───────────┴───────────┴──────────┴──────────────────────┘
```

### 2.2 多 Agent 编排

| 模式 | 拓扑结构 | 适用场景 | 实现 |
|------|----------|----------|------|
| `single` | 单体 Agent | 日常对话（默认） | LLMAgent |
| `chain` | planner → executor → reviewer | 流水线处理 | ChainAgent |
| `parallel` | 3 视角并发 | 多角度分析 | ParallelAgent |
| `cycle` | planner ↔ executor | 自我改进迭代 | CycleAgent |
| `graph` | 条件路由 DAG | 复杂决策流程 | GraphAgent |
| `team_coordinator` | Leader 委派 | 团队协作 | TeamAgent |
| `team_swarm` | 自动 transfer | 自主委派 | TeamAgent(swarm) |
| `claude_code` | Claude CLI 进程 | 本地 Claude | 外部 CLI |
| `codex` | Codex CLI 进程 | 本地 Codex | 外部 CLI |
| `dify` | Dify 平台 API | 低代码平台 | HTTP Client |

### 2.3 Recipe 子 Agent 系统

Recipe 是一个轻量级的子 Agent 定义系统，提供 14 项功能：

| 功能 | 说明 |
|------|------|
| 参数化 | 支持 `${param}` 模板变量 |
| 结构化输出 | JSON Schema 定义输出格式 |
| 子配方 | 配方间组合与嵌套 |
| 重试 | 指数退避自动重试 |
| 继承 | 父配方属性继承 |
| 内联 | 配方内容嵌入 |
| 模型覆盖 | 指定特定 LLM 模型 |
| 超时控制 | 执行时间限制 |
| 热重载 | `fsnotify` 文件变更自动加载 |
| 指标 | 执行统计与监控 |
| 工具包装 | 包装为 Agent Tool |
| 原始工具 | 直接使用 FunctionTool |
| 重试工具 | 带重试策略的 Tool 包装 |
| 超时工具 | 带超时的 Tool 包装 |

工具包装器链：
```
agenttool.NewTool → recipeTool(参数+模板) → retryTool(指数退避) → timeoutTool
```

### 2.4 五层安全纵深防御

```
┌──────────────────────────────────────────────────────┐
│  Layer 5: Guard 执行防护                              │
│  • auto/smart/manual/chat_only 四种权限模式             │
│  • 危险命令拦截（rm -rf /, dd, mkfs）                   │
│  • Prompt 注入检测（tRPC guardrail 插件）               │
│  • 细粒度工具 allowlist/denylist                      │
├──────────────────────────────────────────────────────┤
│  Layer 4: goja JS 沙箱                                │
│  • API 白名单（仅允许安全函数）                          │
│  • 128MB 内存限制                                     │
│  • 5 并发 goroutine 限制                              │
│  • ReDoS 正则防护                                     │
│  • 1MB 源代码限制                                     │
├──────────────────────────────────────────────────────┤
│  Layer 3: OS 级文件沙箱                                │
│  • Linux: Landlock（内核 5.13+）                       │
│  • macOS: sandbox-exec(1)                            │
│  • Windows: Low Integrity Level                       │
│  • 仅允许写入指定目录                                   │
├──────────────────────────────────────────────────────┤
│  Layer 2: .wukongignore 文件黑名单                     │
│  • gitignore 兼容语法                                 │
│  • 阻止 Agent 访问敏感文件                              │
├──────────────────────────────────────────────────────┤
│  Layer 1: OS 级别权限                                  │
│  • 非 root 用户运行                                    │
│  • ulimit 资源限制                                    │
└──────────────────────────────────────────────────────┘
```

### 2.5 扩展体系

#### 12 内置扩展

| 扩展名 | 功能描述 | 工具数 | 启用条件 |
|--------|----------|--------|----------|
| `developer` | 文件读写、命令执行、代码操作 | 多 | 始终 |
| `computer_controller` | Chromedp 浏览器自动化 | 多 | `browser.enabled` |
| `memory` | 记忆管理（搜索/添加/删除/更新） | 6 | 始终 |
| `auto_visualiser` | 自动图表/可视化生成 | 多 | `visualiser.enabled` |
| `tutorial` | 交互式教程 | 多 | `tutorial.enabled` |
| `top_of_mind` | 持久指令注入系统提示 | 0 | `top_of_mind.enabled` |
| `code_mode` | goja JavaScript 沙箱执行 | 多 | `code_mode.enabled` |
| `apps` | HTML 应用管理（克隆/打包/清理/服务） | 13 | `apps.enabled` |
| `web` | Web 工具 | 多 | 始终 |
| `agent_tools` | 子 Agent 包装工具 | 多 | 始终 |
| `ard` | ARD 资源发现（7 工具） | 7 | `ard.enabled` |
| `cortex` | CortexDB 知识图谱操作 | 多 | `cortex.enabled` |

#### MCP Broker

外部 MCP Server 可通过 MCP Broker 集成，提供 4 个工具：
- `mcp_list_servers` — 列出已连接的 MCP Server
- `mcp_list_tools` — 列出指定 Server 的工具
- `mcp_inspect_tools` — 检查工具参数
- `mcp_call` — 调用指定工具

#### ACP MCP Bridge

通过 ACP MCP Bridge（:3400），ACP 客户端可调用 MCP 工具。

### 2.6 多协议支持

Wukong 同时支持 4 种通信协议：

| 协议 | 端口 | 路径 | 用途 |
|------|------|------|------|
| A2A | 9090 | - | Agent-to-Agent 标准通信（tRPC-A2A-Go） |
| ACP | 9091 | `/acp` | Agent Client Protocol |
| AG-UI SSE | 8080 | `/agui` | Web UI 实时对话流 |
| ACP MCP | 3400 | `/mcp` | 跨协议工具桥接 |

### 2.7 LLM Provider 体系

| Provider | 配置类型 | SDK | 特点 |
|----------|----------|-----|------|
| OpenAI | `openai` | openai-go | GPT 系列 |
| Anthropic | `anthropic` | openai-go (兼容) | Claude 系列 |
| Google | `google` | openai-go (兼容) | Gemini 系列 |
| DeepSeek | `deepseek` | openai-go (兼容) | 国产性价比 |
| Ollama | `ollama` | openai-go (兼容) | 本地部署 |
| LMStudio | `lmstudio` | openai-go (兼容) | 本地部署 |
| ACP | `acp` | HTTP Client | 远程 ACP Agent |

**模型分工策略**:
- **主对话模型**: `default_provider` + `default_model`
- **后台任务模型**: `lightweight_provider` + `lightweight_model`（用于记忆提取、上下文压缩、KG 提取、检索规划）

### 2.8 CortexDB 记忆栈

CortexDB 提供完整的智能记忆栈（12 源文件）：

| 组件 | 文件 | 功能 |
|------|------|------|
| `CortexStore` | `store.go` | HNSW 向量 + FTS5 全文 + 余弦相似度 |
| `LexicalStore` | `lexical.go` | FTS5 语义词搜索 |
| `MemoryFlowService` | `memoryflow.go` | 转录记录 + WakeUp 唤醒 |
| `GraphFlowService` | `graphflow.go` | RDF 知识图谱构建 |
| `Embedder` | `embedder.go` | 文本嵌入向量生成 |
| `Extractor` | `extractor.go` | LLM 实体/关系提取 |
| `Planner` | `planner.go` | 检索计划生成 |
| `RecallManager` | `recall_manager.go` | 统一召回管理 |
| `ImportFlow` | `import_flow.go` | DDL → KG 结构化导入 |
| `ImportTools` | `import_tools.go` | 导入工具集 |
| `KGTools` | `kg_tools.go` | 知识图谱操作工具 |
| `JSONGenerator` | `json_generator.go` | JSON 格式输出 |

### 2.9 CLI 命令体系

| 命令 | 文件 | 功能 |
|------|------|------|
| `wukong session` | `session.go` (43.7 KB) | 交互式会话，28 步引导启动 |
| `wukong configure` | `configure.go` | 交互式配置向导 |
| `wukong run` | `run.go` | 单次执行/对话模式 |
| `wukong extension` | `extension.go` | MCP 扩展安装/列表/启用/禁用 |
| `wukong eval` | `eval.go` | Agent 评估测试 |
| `wukong project` | `project.go` | 项目追踪与会话恢复 |
| `wukong config` | `config.go` | 配置验证与展示 |
| `wukong version` | `version.go` | 版本信息 |
| `wukong apps` | `apps_mgmt.go` | HTML 应用 + 网站克隆 |

**TUI 界面**: Bubbletea + LipGloss，三区布局：
- 对话区：Agent 响应流式显示
- 工具状态区：工具调用实时状态
- 输入区：用户消息输入

---

## 3. 核心数据流

```
User Input
    │
    ▼
┌──────────────────────────────────────────────────┐
│ CoreLoop.Execute()                                │
│                                                  │
│ Phase 1: Prepare                                 │
│   ContextManager.Prepare()  ← 历史压缩            │
│   Recall/Cortex Store       ← 相关记忆检索         │
│   MemoryFlow.WakeUp()       ← 3层上下文唤醒        │
│   tRPC Memory.ReadMemories()← 长期记忆去重         │
│   GraphFlow                 ← 知识图谱增强         │
│                                                  │
│ Phase 2: Execute                                 │
│   runner.Run()              ← LLM 推理            │
│   Tool Calls                ← 工具调用             │
│   Guard.Check()             ← 安全检查             │
│   [HITL pause]              ← 人机协同             │
│                                                  │
│ Phase 3: Finalize                                │
│   StoreMessage()            ← 保存消息             │
│   MemoryFlow.IngestTurn()   ← 转录本轮对话          │
│   tRPC Memory.PromoteFacts()← 提取长期记忆          │
│   GraphFlow.auto_extract()  ← 自动提取 KG          │
│                                                  │
│ Phase 4: Return                                  │
│   ContextManager.AfterRun()  ← Token统计           │
└──────────────────────────────────────────────────┘
    │
    ▼
Agent Response
```

---

## 4. 存储架构

单文件 `wukong.db` (SQLite WAL 模式) 承载全栈数据：

```
wukong.db
├── sessions              # 会话记录（tRPC Session SQLite）
├── memories              # 长期记忆（tRPC Memory SQLite）
├── recall_fts            # FTS5 全文搜索索引
├── recall_messages       # 召回消息存储
├── todos                 # 任务管理
├── projects              # 项目追踪
├── cortex_*              # CortexDB HNSW 向量 + FTS5 + RDF
├── app_versions          # HTML 应用版本历史
├── evolution_*           # 技能进化记录
└── FTS5 + HNSW + vectors  # 内建索引
```

**MultiPool 数据库池配置**:
- WAL 模式写入
- MaxOpenConns = 4
- synchronous = NORMAL
- busy_timeout = 5000ms

---

## 5. 配置体系

### 加载优先级（4级）

```
1. CLI 参数（--provider, --model, --temperature, --max-tokens, --config）
2. 环境变量（WUKONG_ 前缀，如 WUKONG_DEFAULT_PROVIDER）
3. YAML 配置文件（--config 指定或默认搜索路径）
4. 内置默认值（internal/config/defaults.go）
```

环境变量支持 `${ENV_VAR}` 语法，运行时自动展开。

### 配置代码组织

| 文件 | 职责 | 行数 |
|------|------|------|
| `config.go` | 根结构体 + Loader + 查询方法 | ~280 |
| `types.go` | 43 个子配置结构体定义 | ~1060 |
| `defaults.go` | 内置默认值（按子系统分组） | ~270 |
| `validate.go` | 配置验证 + 非致命警告 | ~130 |

### 模型回退链

```
子系统.extractor_model → lightweight_model → default_provider
```

---

## 6. 测试覆盖

| 类别 | 数量 |
|------|------|
| `_test.go` 文件 | 51 |
| 有测试的包 | 21/28 |
| 测试较完善的包 | agent, ard, config, security, extension, apps |
| 测试欠缺的包 | cli, cortex, knowledge, eval |

---

## 7. 文档索引

| 文档 | 说明 |
|------|------|
| [系统架构](ARCHITECTURE.md) | 16 章架构、16 ADR、模块依赖、数据流 |
| [配置手册](CONFIG.md) | 44 结构体、全字段、推荐方案 |
