# Wukong — 架构哲学与核心特性

> Go: 1.26 | 30 内部包 + 2 公共包
> 基于 tRPC-Agent-Go v1.10.0 · tRPC-MCP-Go v0.0.16 · tRPC-A2A-Go v0.2.5 · CortexDB v2.25.0 · OKF v0.1

---

## 1. 七大架构哲学

Wukong 遵循七大核心哲学, 决定所有工程决策:

### 1.1 记忆优先 (Memory-First)

**核心信念**: Agent 智能源于跨会话的知识积累, 而非单次对话的表现。

**实现方案**: 双引擎三层记忆架构
- **短期**: MemoryFlow — 转录记录 + WakeUp 上下文生成 (身份/回忆/会话 3 层)
- **中期**: CortexStore — HNSW 向量索引 + FTS5 全文搜索 + 本地余弦相似度
- **长期**: tRPC Memory — AutoExtract 异步 LLM 提取 + SmartCleanup 容量淘汰
- **结构化**: GraphFlow — 每轮对话自动提取实体/关系 -> RDF 知识图谱 -> SPARQL

### 1.2 框架组装 (Framework Assembly)

**核心信念**: 任何组件都应可替换, 不绑定特定实现。

**实现方案**: CoreLoop 依赖注入体系
- 全部子系统通过 CoreLoopConfig 注入, 而非硬编码
- Session/Memory 支持 SQLite、Redis、内存三种后端
- LLM 支持 7 种 Provider 统一接口
- 扩展系统通过 ToolSet 接口统一管理

### 1.3 多 Agent 原生 (Multi-Agent Native)

**核心信念**: 编排是第一公民, 单 Agent 只是多 Agent 的特例。

**实现方案**: 10 种编排模式 + HITL
- 5 种基础模式: single / chain / parallel / cycle / graph
- 2 种团队模式: team_coordinator / team_swarm
- 3 种外部集成: claude_code / codex / dify
- HITL 人机协同: 在关键决策点暂停等用户确认

### 1.4 进化智能 (Evolutionary Intelligence)

**核心信念**: 技能应从失败中学习, 持续自我改进。

**实现方案**: Evolution 引擎
- LLM 分析执行失败原因
- 自动生成修复补丁
- 版本管理系统追踪变更
- fsnotify 热重载新版本

### 1.5 双向发现 (Bidirectional Discovery)

**核心信念**: 发现别人, 也被人发现。

**实现方案**: ARD (Agentic Resource Discovery)
- Outbound: 联邦搜索远程 Registry (Agent/MCP/OKF Bundle)
- Inbound: RegistryServer 发布自身
- Auto: MCP 连接和 A2A Remote 自动注册

### 1.6 开放互通 (Open Interoperability)

**核心信念**: 标准化协议促进 Agent 生态互通。

**实现方案**: ANP (Agent Network Protocol)
- DID:wba 去中心化身份: Ed25519 签名 + X25519 密钥交换
- ADP 能力描述协议: 统一的多协议能力声明
- Meta-Protocol: JSON-RPC 2.0 能力协商引擎
- E2EE 加密: X25519 + ChaCha20-Poly1305 端到端加密
- HTTP 签名: RFC 9421 请求完整性保护

### 1.7 知识标准化 (Knowledge Standardization)

**核心信念**: 知识应有标准形状, 而非散落于各系统的专有格式。

**实现方案**: OKF (Open Knowledge Format) v0.1
- Markdown + YAML frontmatter 表示概念
- 文件路径即概念身份, Markdown 链接形成知识图谱
- index.md 渐进式探索, log.md 变更追踪
- 消费者容错: 跳过不合规文件、容忍未知类型、保留自定义字段

---

## 2. 核心特性详解

### 2.1 智能记忆系统

```
+-----------------------------------------------------------+
|                    Memory Stack                           |
+----------+----------+---------+--------------------------+
| 短期     | 中期     | 长期    | 结构化                   |
| MemoryFlow|CortexStore|tRPC    | GraphFlow                |
|          |          | Memory  |                          |
+----------+----------+---------+--------------------------+
| 转录记录 | HNSW 向量 | 关键事实| RDF 知识图谱             |
| WakeUp   | FTS5 全文 |AutoExtract| SPARQL 查询           |
| 3层上下文| 余弦相似度|SmartCleanup| auto_extract          |
+----------+----------+---------+--------------------------+
```

### 2.2 多 Agent 编排

| 模式 | 拓扑结构 | 适用场景 | 实现 |
|------|----------|----------|------|
| single | 单体 Agent | 日常对话 (默认) | LLMAgent |
| chain | planner -> executor -> reviewer | 流水线处理 | ChainAgent |
| parallel | 3 视角并发 | 多角度分析 | ParallelAgent |
| cycle | planner <-> executor | 自我改进迭代 | CycleAgent |
| graph | 条件路由 DAG | 复杂决策流程 | GraphAgent |
| team_coordinator | Leader 委派 | 团队协作 | TeamAgent |
| team_swarm | 自动 transfer | 自主委派 | TeamAgent(swarm) |
| claude_code | Claude CLI 进程 | 本地 Claude | 外部 CLI |
| codex | Codex CLI 进程 | 本地 Codex | 外部 CLI |
| dify | Dify 平台 API | 低代码平台 | HTTP Client |

### 2.3 ANP Agent 互通协议

```
+--------------------------------------------------------------+
|                   ANP Protocol Stack                         |
+--------------------------------------------------------------+
| 身份层: did:wba -> Ed25519 签名 + X25519 密钥交换            |
| 发现层: ADP -> /.well-known/agent-descriptions               |
| 协商层: Meta-Protocol -> JSON-RPC 2.0 能力协商 + 接口卡      |
| 安全层: E2EE (X25519+ChaCha20-Poly1305) + HTTP Sign (RFC 9421)|
| 桥接层: ANPAdapter -> JSON-RPC 2.0 <-> A2A 协议适配         |
+--------------------------------------------------------------+
| 核心模块                                                    |
|   ard/did.go        — did:wba 身份管理                      |
|   ard/adp.go        — ADP 文档生成                          |
|   ard/anp_discovery.go — 发现端点 (.well-known)             |
|   ard/http_sign.go  — RFC 9421 HTTP 签名                    |
|   summon/meta_protocol.go — Meta-Protocol 引擎              |
|   summon/e2ee.go    — E2EE 端到端加密                       |
|   summon/anp_adapter.go — ANP <-> A2A 桥接                   |
+--------------------------------------------------------------+
| 端点: /anp/meta-protocol (JSON-RPC 2.0)                     |
|       /anp/capabilities (GET, 能力卡)                        |
| 端口: :9092 (anp.port)                                      |
+--------------------------------------------------------------+
```

### 2.4 OKF 知识格式系统

```
+---------------------------------------------------------+
|                   OKF Knowledge Layer                    |
+---------------------------------------------------------+
| 发现层: ARD 联邦搜索 OKF Bundle (application/okf-bundle)|
+---------------------------------------------------------+
| 标准层: internal/okf/                                    |
|   Bundle (目录) -> Concept (.md) -> Frontmatter (YAML)   |
|   index.md (渐进探索) | log.md (变更追踪)                |
|   文件路径 = 概念ID | Markdown 链接 = 知识图谱           |
+---------------------------------------------------------+
| 引擎层: 6 个集成点                                      |
|   skill: SKILL.md -> type:skill 合规                    |
|   knowledge: RAG <-> OKF Bundle 导入/导出               |
|   cortex: index.md -> WakeUp 注入 | DDL -> 概念生成     |
|   evolution: log.md 变更追踪                             |
|   ard: OKF Bundle -> CatalogEntry 联邦发现              |
+---------------------------------------------------------+
```

### 2.5 Recipe 子 Agent 系统

Recipe 是轻量级的子 Agent 定义系统, 提供 14 项功能。工具包装器链:

```
agenttool.NewTool -> recipeTool(参数+模板) -> retryTool(指数退避) -> timeoutTool
```

### 2.6 五层安全纵深防御

```
Layer 5: Guard — auto/smart/manual/chat_only + blocked_commands + Prompt注入
Layer 4: goja  — API白名单 + 128MB + 5并发 + ReDoS + 1MB限制
Layer 3: OS沙箱 — Landlock(Linux) / Seatbelt(macOS) / LowIL(Windows)
Layer 2: .wukongignore — gitignore兼容文件黑名单
Layer 1: OS权限 — 非root + ulimit
```

### 2.7 扩展体系

#### 12 内置扩展

| 扩展名 | 功能描述 | 工具数 | 启用条件 |
|--------|----------|--------|----------|
| developer | 文件读写、命令执行、代码操作 | 多 | 始终 |
| computer_controller | Chromedp 浏览器自动化 | 多 | browser.enabled |
| memory | 记忆管理 (搜索/添加/删除/更新) | 6 | 始终 |
| auto_visualiser | 自动图表/可视化生成 | 多 | visualiser.enabled |
| tutorial | 交互式教程 | 多 | tutorial.enabled |
| top_of_mind | 持久指令注入系统提示 | 0 | top_of_mind.enabled |
| code_mode | goja JavaScript 沙箱执行 | 多 | code_mode.enabled |
| apps | HTML 应用管理 (克隆/打包/清理/服务) | 13 | apps.enabled |
| web | Web 工具 | 多 | 始终 |
| agent_tools | 子 Agent 包装工具 | 多 | 始终 |
| ard | ARD 资源发现 (7 工具) | 7 | ard.enabled |
| cortex | CortexDB 知识图谱操作 | 多 | cortex.enabled |

### 2.8 多协议支持

| 协议 | 端口 | 路径 | 用途 |
|------|------|------|------|
| Gateway | 9093 | / | 多平台消息通道 (飞书/企微等) |
| A2A | 9090 | -- | Agent-to-Agent 标准通信 |
| ACP | 9091 | /acp | Agent Client Protocol |
| AG-UI SSE | 8080 | /agui | Web UI 实时对话流 |
| ACP MCP | 3400 | /mcp | 跨协议工具桥接 |
| ANP | 9092 | /anp/meta-protocol | DID + 能力协商 + E2EE |

### 2.9 LLM Provider 体系

| Provider | 配置类型 | SDK | 特点 |
|----------|----------|-----|------|
| OpenAI | openai | openai-go | GPT 系列 |
| Anthropic | anthropic | openai-go (兼容) | Claude 系列 |
| Google | google | openai-go (兼容) | Gemini 系列 |
| DeepSeek | deepseek | openai-go (兼容) | 国产性价比 |
| Ollama | ollama | openai-go (兼容) | 本地部署 |
| LMStudio | lmstudio | openai-go (兼容) | 本地部署 |
| ACP | acp | HTTP Client | 远程 ACP Agent |

---

## 3. 核心数据流

```
User Input
    |
    v
+----------------------------------------------------+
| CoreLoop.Execute()                                 |
|                                                    |
| Phase 1: Prepare                                   |
|   ContextManager.Prepare()  <- 历史压缩            |
|   Recall/Cortex Store       <- 相关记忆检索        |
|   MemoryFlow.WakeUp()       <- 3层上下文唤醒       |
|   OKF.Injector              <- 知识索引注入        |
|   tRPC Memory.ReadMemories()<- 长期记忆去重        |
|   GraphFlow                 <- 知识图谱增强        |
|                                                    |
| Phase 2: Execute                                   |
|   runner.Run()              <- LLM 推理            |
|   Tool Calls                <- 工具调用            |
|   Guard.Check()             <- 安全检查            |
|                                                    |
| Phase 3: Finalize                                  |
|   StoreMessage()            <- 保存消息            |
|   MemoryFlow.IngestTurn()   <- 转录本轮对话        |
|   tRPC Memory.PromoteFacts()<- 提取长期记忆        |
|   GraphFlow.auto_extract()  <- 自动提取 KG         |
|                                                    |
| Phase 4: Return                                    |
|   ContextManager.AfterRun()  <- Token统计          |
+----------------------------------------------------+
```

---

## 4. 存储架构

单文件 wukong.db (SQLite WAL 模式) 承载全栈数据:

```
wukong.db
+-- sessions              # 会话记录
+-- memories              # 长期记忆
+-- recall_fts            # FTS5 全文搜索索引
+-- recall_messages       # 召回消息存储
+-- todos                 # 任务管理
+-- projects              # 项目追踪
+-- cortex_*              # CortexDB HNSW 向量 + FTS5 + RDF
+-- app_versions          # HTML 应用版本历史
+-- evolution_*           # 技能进化记录

.wukong/okf/              # OKF Bundle 存储
+-- index.md              # 知识包索引
+-- log.md                # 变更历史
+-- tables/               # 表结构概念
+-- skills/               # 技能概念
+-- api/                  # API 概念
```

---

## 5. 配置体系

### 加载优先级 (4级)

```
1. CLI 参数
2. 环境变量 (WUKONG_ 前缀)
3. YAML 配置文件
4. 内置默认值
```

### 配置代码组织

| 文件 | 职责 |
|------|------|
| config.go | 根结构体 + Loader + 查询方法 |
| types.go | 34 个子配置结构体定义 |
| defaults.go | 内置默认值 (按子系统分组, 13 个方法) |
| validate.go | 配置验证 + 非致命警告 (含 ANP/OKF 检查) |

### env var 展开覆盖

providers api_key, A2A remotes, Gateway channels (app_secret/encrypt_key/token/encoding_aes_key), observability, artifact COS, ACPServer, CortexDB, Dify。

---

## 6. 文档索引

| 文档 | 说明 |
|------|------|
| [系统架构](ARCHITECTURE.md) | 19 章架构、19 ADR、模块依赖、数据流 |
| [配置手册](CONFIG.md) | 34 结构体、全字段、推荐方案 |
| [CLI & TUI 架构](CLI_TUI.md) | 命令树、TUI Elm 架构、8阶段启动 |
| [Gateway 通道设计](GATEWAY_CHANNEL_DESIGN.md) | 多平台消息通道架构 |
| [Gateway 部署](GATEWAY_DEPLOY.md) | 飞书/企微接入、Nginx、Docker、监控 |
