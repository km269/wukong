# Wukong 系统架构

> Go: 1.26 | 30 内部包 + 2 公共包
> 配置: 34 结构体 (config.go + types.go + defaults.go + validate.go)
> CLI: 27 顶层 + 55+ 子命令
>
> 基于 tRPC-Agent-Go v1.10.0 · tRPC-MCP-Go v0.0.16 · tRPC-A2A-Go v0.2.5 · CortexDB v2.25.0 · OKF v0.1

---

## 1. 架构哲学

| 哲学 | 核心信念 | 关键工程决策 |
|------|----------|-------------|
| **记忆优先** | Agent 智能源于跨会话积累 | 双引擎三层记忆: tRPC Memory + CortexDB Stack |
| **框架组装** | 任何组件可替换 | CoreLoop 依赖注入, 全部子系统接口隔离 |
| **多 Agent 原生** | 编排是第一公民 | 10 种编排模式 + HITL + 子Agent委派 |
| **进化智能** | 技能自我改进 | LLM分析 -> 补丁 -> 版本 -> 热重载 |
| **双向发现** | 发现与被发现 | ARD 联邦搜索 + RegistryServer |
| **开放互通** | 标准化协议促进 Agent 生态互通 | ANP: DID + 能力协商 + E2EE + HTTP 签名 |
| **知识标准化** | 知识有标准形状 | OKF v0.1: Markdown + YAML frontmatter |

---

## 2. 系统全景

```
+----------------------------------------------------------------------+
|                      Wukong AI Agent Platform                         |
+----------------------------------------------------------------------+
| Entry: CLI(27cmd+55sub) | TUI | Gateway:9093 | A2A:9090 | ACP:9091   |
|        AG-UI:8080 | MCP:3400 | ANP:9092                              |
+----------------------------------------------------------------------+
| Core Engine: CoreLoop (agent/)                                        |
|   WorkflowBuilder(10 modes) · TeamBuilder · ContextManager(3-tier)    |
|   Security Guard(5-tier) · HITL · TodoEnforcer · PromptTemplate       |
+----------------------------------------------------------------------+
| ANP Protocol Stack:                                                   |
|   ard(did+adp+sign) · summon(meta+e2ee+adapter) · ANP HTTP Server     |
+----------------------------------------------------------------------+
| OKF Knowledge Layer:                                                  |
|   okf(Bundle v0.1) · Knowledge(Import/Export) · Skill(OKF兼容)         |
|   Cortex(Enrichment+Injector) · Evolution(log.md) · ARD(Bundle发现)   |
+----------------------------------------------------------------------+
| Gateway System:                                                       |
|   GatewayServer(9-step pipeline) · Feishu Channel · WeCom Channel     |
|   Dedup · RateLimiter · SessionStore · Router                        |
+----------------------------------------------------------------------+
| Agent Framework: tRPC-Agent-Go v1.10.0                                |
|   LLMAgent · ChainAgent · ParallelAgent · CycleAgent · GraphAgent      |
|   Planner · ToolSearch · ContextCompaction · Skill · Recipe           |
+----------------------------------------------------------------------+
| Memory Stack (Dual-Engine, 3-Tier):                                    |
|   Short: MemoryFlow — IngestTurn -> WakeUp(3 layers) -> PromoteFacts  |
|   Mid:   CortexStore — HNSW + FTS5                                    |
|   Long:  tRPC Memory — AutoExtract + SmartCleanup                      |
|   Graph: GraphFlow — auto_extract -> RDF -> SPARQL                    |
+----------------------------------------------------------------------+
| Capability Layer:                                                     |
|   Recipe(14) · 12内置扩展 · ARD(双向发现+7工具)                        |
|   Evolution · Summon(A2A) · CodeMode(goja) · Knowledge(RAG)            |
|   Browser(Chromedp) · Apps(8子命令:克隆+打包+预览)                     |
|   pkg/sandbox · pkg/zim                                                |
+----------------------------------------------------------------------+
| Infrastructure: 7 LLM · OpenTelemetry · Langfuse · MultiPool(SQLite)  |
+----------------------------------------------------------------------+
| Storage: wukong.db — all data in single SQLite WAL file                |
+----------------------------------------------------------------------+
```

---

## 3. 目录结构

| 目录 | 文件数 | 用途 |
|------|--------|------|
| cmd/wukong/ | 1 | 应用入口 |
| internal/cli/ | 30 | CLI 命令 (Cobra) + TUI |
| internal/agent/ | 21 | Agent 循环、Recipe、工作流、HITL |
| internal/okf/ | 3 | OKF v0.1 核心 (Bundle 加载/写入/概念解析) |
| internal/ard/ | 22 | ARD 服务/客户端/注册表/联邦 + ANP DID/ADP/HTTP Sign |
| internal/summon/ | 9 | 子Agent委派 + A2A + ANP Meta-Protocol/E2EE/Adapter |
| internal/gateway/ | 11 | 多平台消息网关: 飞书/企微 Channel + Router + Dedup + RateLimit |
| internal/apps/ | 31 | 应用管理器: 网站克隆引擎 + 浏览器池 + 多格式打包 + MCP桥接 |
| internal/browser/ | 10 | 通用浏览器控制 + Stealth + Antibot + Settle |
| internal/extension/ | 25 | 扩展管理器 + MCP Broker + 13 内置工具集 |
| internal/config/ | 5 | 配置结构 + Viper 加载 + 验证 (含 ANP/OKF 检查) |
| internal/cortex/ | 14 | CortexDB 记忆栈 + OKF 注入器/增强器 |
| internal/evolution/ | 7 | 技能进化 + OKF log.md 变更追踪 |
| internal/knowledge/ | 2 | RAG 知识库 + OKF 导入/导出 |
| internal/skill/ | 2 | 技能管理 + OKF 兼容层 |
| internal/ (其他) | ~55 | 安全/会话/记忆/回调/健康等 |
| pkg/sandbox/ | 10 | 跨平台沙箱隔离 |
| pkg/zim/ | 6 | ZIM 格式读写 (Kiwix 兼容) |

### 配置包文件拆分

| 文件 | 职责 |
|------|------|
| config.go | 包文档 · ResolvePath · WukongConfig · Loader · 查询方法 |
| types.go | 34 个子配置结构体 (含 ANP/Gateway 通道/OKF) |
| defaults.go | setDefaults 按子系统拆分为 13 个方法 (含 Gateway/Clone/OKF) |
| validate.go | Validate() 致命错误检查 + Warnings() 非致命警告 |
| config_test.go | 9 个单元测试 (加载/默认值/env展开/查询) |

---

## 4. CoreLoop 中央编排

internal/agent/ (21 文件)

### 执行循环 (4 阶段)

```
Phase 1: Prepare — ContextManager + Recall/Cortex + WakeUp + OKF注入 + ReadMemories + KG
Phase 2: Execute — runner.Run -> LLM -> Tool Calls -> Guard.Check
Phase 3: Finalize — StoreMessage + IngestTurn + PromoteFacts + auto_extract
Phase 4: Return — contextMgr.AfterRun (token stats)
```

---

## 5. 多 Agent 编排 (10 模式)

| 模式 | 拓扑 | 底层 | 场景 |
|------|------|------|------|
| single | 单体 | LLMAgent | 日常对话 |
| chain | planner->executor->reviewer | ChainAgent | 流水线 |
| parallel | 3视角并发 | ParallelAgent | 多角度分析 |
| cycle | planner<->executor | CycleAgent | 自我迭代 |
| graph | 条件DAG | GraphAgent | 复杂决策 |
| team_coordinator | Leader委派 | TeamAgent | 团队协作 |
| team_swarm | 自动transfer | TeamAgent(swarm) | 自主委派 |
| claude_code | CLI进程 | exec.Cmd | 本地Claude |
| codex | CLI进程 | exec.Cmd | 本地Codex |
| dify | HTTP API | HTTP Client | 低代码 |

---

## 6. Gateway 多平台消息通道

internal/gateway/ (11 文件) — 统一消息入口, 插件式 Channel 架构

### 架构

```
+-----------------------+
|  Feishu    WeCom      |  Channel 适配器
+-----------+----------+
|      ChannelRouter     |  路由 + 中间件链
+-----------+----------+
|     GatewayServer      |  9步消息流水线
+-----------+----------+
| Dedup | RateLimiter    |  防护层
+-----------+----------+
|   GatewaySessionStore  |  身份/会话映射
+-----------+----------+
|     agent.CoreLoop     |  Agent 执行
+-----------------------+
```

### 消息流水线 (9 步)

1. VerifyRequest — 签名验证
2. PlatformEvent — URL 验证 (echostr)
3. ParseMessage — 平台消息 -> 统一格式
4. Dedup — 消息去重 (MessageID + TTL)
5. BuildUserID — 身份映射
6. RateLimiter — 滑动窗口限流 + 并发控制
7. SessionStore — 会话持久化
8. CoreLoop.Run — Agent 执行
9. SendReply — 回复/流式推送

### 服务端点

- 端口: :9093
- 监控: GET /metrics (JSON)

---

## 7. ANP Agent 互通协议栈

### 架构

```
ANP Protocol Stack
    |
    +-- Identity Layer   — did:wba (Ed25519 + X25519)
    +-- Discovery Layer  — ADP + /.well-known/agent-descriptions
    +-- Negotiation Layer— Meta-Protocol (JSON-RPC 2.0 能力协商)
    +-- Security Layer   — E2EE (X25519+ChaCha20) + HTTP Sign (RFC 9421)
    +-- Bridge Layer     — ANPAdapter (JSON-RPC <-> A2A)
```

### 核心模块

| 模块 | 文件 | 功能 |
|------|------|------|
| DIDManager | ard/did.go | did:wba 身份管理: Ed25519 签名 + X25519 密钥交换 |
| ADPGenerator | ard/adp.go | ADP 文档生成: Agent Card + 接口描述 |
| ANPDiscovery | ard/anp_discovery.go | /.well-known/agent-descriptions 发现端点 |
| HTTPSign | ard/http_sign.go | RFC 9421 HTTP 消息签名 |
| MetaProtocol | summon/meta_protocol.go | JSON-RPC 2.0 引擎: capabilities.negotiate |
| E2EEMessenger | summon/e2ee.go | X25519 + ChaCha20-Poly1305 E2EE |
| ANPAdapter | summon/anp_adapter.go | ANP JSON-RPC 2.0 -> A2A 协议桥接 |

---

## 8. 记忆系统 (双引擎三层)

| 层级 | 引擎 | 机制 |
|------|------|------|
| 短期 | MemoryFlow | 转录 + 3层唤醒上下文 + OKF 知识索引注入 |
| 中期 | CortexStore | HNSW向量 + FTS5全文 |
| 长期 | tRPC Memory | AutoExtract + SmartCleanup |
| 结构化 | GraphFlow | RDF知识图谱 + SPARQL |

---

## 9. OKF 知识格式系统

### 架构

```
OKF Bundle (目录)
    |
    +-- index.md (渐进探索入口)
    +-- log.md (变更历史)
    +-- concepts/*.md (概念文件)
         |
         +-- YAML Frontmatter (type + title + description + tags + ...)
         +-- Markdown Body (正文 + 交叉链接)
```

### OKF 数据流

```
数据源 (DDL/目录/对话)
    |
    v
EnrichmentAgent -> OKF Bundle (concepts/*.md)
    |
    +-- index.md -> KnowledgeIndexInjector -> MemoryFlow.WakeUp -> Agent 上下文
    +-- log.md -> Evolution 变更追踪
    +-- CatalogEntry -> ARD 联邦发现 -> 其他 Agent
```

---

## 10. 安全防御 (5 层)

```
Layer 5: Guard — auto/smart/manual/chat_only + blocked_commands + Prompt注入
Layer 4: goja  — API白名单 + 128MB + 5并发 + ReDoS + 1MB限制
Layer 3: OS沙箱 — Landlock(Linux) / Seatbelt(macOS) / LowIL(Windows)
Layer 2: .wukongignore — gitignore兼容文件黑名单
Layer 1: OS权限 — 非root + ulimit
```

---

## 11. LLM Provider (7 种)

| Provider | type | SDK | 特点 |
|----------|------|-----|------|
| OpenAI | openai | openai-go | GPT 系列 |
| Anthropic | anthropic | openai-go | Claude 系列 |
| Google | google | openai-go | Gemini 系列 |
| DeepSeek | deepseek | openai-go | 国产性价比 |
| Ollama | ollama | openai-go | 本地开源 |
| LMStudio | lmstudio | openai-go | 本地服务 |
| ACP | acp | HTTP | 远程代理 |

---

## 12. 服务端点 (6 协议)

| 协议 | 端口 | 用途 |
|------|------|------|
| Gateway | 9093 | 多平台消息通道 (飞书/企微等) |
| A2A | 9090 | Agent-to-Agent 通信 |
| ACP | 9091 | Agent Client Protocol |
| AG-UI SSE | 8080 | Web UI 实时对话 |
| ACP MCP | 3400 | 跨协议工具桥接 |
| ANP | 9092 | DID + 能力协商 + E2EE |

---

## 13. CLI 命令体系

internal/cli/ (30 文件): 27 顶层命令 + 55+ 子命令

详见 [CLI & TUI 架构](./CLI_TUI.md)。

---

## 14. 网站克隆系统

internal/apps/clone/ (18 文件) — 完整网站离线镜像引擎, 10 层反爬

---

## 15. ZIM 打包系统

internal/apps/pack/ (4 文件) + pkg/zim/ (6 文件) — Kiwix 兼容 ZIM v6 打包

---

## 16. 技术栈

| 类别 | 技术 | 版本 |
|------|------|------|
| Agent 框架 | tRPC-Agent-Go | v1.10.0 |
| MCP 协议 | tRPC-MCP-Go | v0.0.16 |
| A2A 协议 | tRPC-A2A-Go | v0.2.5 |
| Agent 互通 | ANP (Agent Network Protocol) | -- |
| 智能记忆 | CortexDB | v2.25.0 |
| 知识格式 | OKF (Open Knowledge Format) | v0.1 |
| CLI | Cobra + Viper | v1.9.1 / v1.20.1 |
| 浏览器 | Chromedp | v0.15.1 |
| 数据库 | modernc.org/sqlite | v1.38.2 |

---

## 17. 关键设计决策 (ADRs)

| # | 决策 | 理由 |
|---|------|------|
| 1 | SQLite WAL 共享 MultiPool | 单文件部署 |
| 2 | 双引擎记忆 | tRPC 存事实, CortexDB 存语义/图谱 |
| 3 | 轻量模型分工 | 主模型对话, 轻量模型后台提取 |
| 4 | CoreLoop 依赖注入 | 所有子系统可替换/测试 |
| 5 | YAML Recipe + 热重载 | 文件变更即生效 |
| 6 | HITL 融入编排循环 | 决策点原生暂停 |
| 7 | SmartCleanup 容量淘汰 | 70%新鲜度+30%长度 |
| 8 | ACP + AG-UI 双协议 | ACP 客户端, AG-UI 浏览器 |
| 9 | MCP Broker 批量管理 | 外部 MCP 统一暴露 |
| 10 | goja 5层JS沙箱 | API白名单+内存+并发+ReDoS+代码长度 |
| 11 | OS级跨平台沙箱 | Landlock/Seatbelt/LowIL |
| 12 | ARD 双向发现 | 联邦搜索 + RegistryServer |
| 13 | Evolution 版本管理 | 每补丁保留版本 |
| 14 | 单文件 wukong.db | 简化部署 |
| 15 | Chrome 真实渲染克隆引擎 | Chrome渲染 + 资源本地化 + ZIM打包 |
| 16 | 浏览器标签池复用 | 单进程多Tab, 信号量控制并发 |
| 17 | 配置代码按职责拆分 | types/defaults/validate 分离 |
| 18 | 采用 OKF v0.1 作为知识表示标准 | 厂商中立、git 友好、渐进式探索、消费者容错 |
| 19 | 实现 ANP 协议栈促进 Agent 互通 | DID身份 + 能力协商 + E2EE + HTTP签名 |
| 20 | Gateway 插件式多平台 Channel 架构 | 统一入口 + 中间件栈 + 独立 Channel 适配器 |
