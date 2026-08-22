# Wukong — 记忆优先 · 编排驱动 · 安全纵深 · 双向发现

> 本地优先、框架组装、可深度扩展的开源 AI Agent 平台
>
> Go 1.26 | 30+ 内部包 | 3 公共包 | 34 配置结构体
> CLI: 30 顶层命令 + 60+ 子命令 | 依赖: 29 direct + 105 indirect

---

## 目录

1. [架构哲学](#架构哲学)
2. [核心能力](#核心能力)
3. [快速开始](#快速开始)
4. [技术选型](#技术选型)
5. [子系统亮点](#子系统亮点)
6. [文档索引](#文档索引)
7. [许可证](#许可证)

---

## 架构哲学

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

## 核心能力

### 编排与 Agent

| 维度 | 方案 |
|------|------|
| **编排模式** | 10 种: `single` / `chain` / `parallel` / `cycle` / `graph` / `team_coordinator` / `team_swarm` / `claude_code` / `codex` / `dify` |
| **LLM 后端** | 7 种: OpenAI / Anthropic / Google / DeepSeek / Ollama / LMStudio / ACP |
| **CoreLoop** | 四阶段执行: Prepare → Execute → Finalize → Return |
| **Recipe 系统** | YAML 定义子 Agent + 热重载 + 版本进化 |
| **HITL** | 人机协同，决策点原生暂停 |

### 记忆与知识

| 维度 | 方案 |
|------|------|
| **记忆系统** | 双引擎三层: tRPC Memory × CortexDB (HNSW + FTS5 + RDF) |
| **短期记忆** | MemoryFlow: 会话转录 + 3 层唤醒 + OKF 注入 |
| **中期记忆** | CortexStore: HNSW 向量索引 + FTS5 全文检索 |
| **长期记忆** | tRPC Memory: AutoExtract + SmartCleanup |
| **结构化记忆** | GraphFlow: 实体抽取 → RDF 图谱 → SPARQL 查询 |
| **知识格式** | OKF v0.1: 6 包集成 (okf/ard/cortex/evolution/knowledge/skill) |

### 安全与沙箱

| 维度 | 方案 |
|------|------|
| **5 层纵深防御** | Guard → goja JS 沙箱 → OS 沙箱 → .wukongignore → OS 权限 |
| **权限模式** | 4 种: `auto` / `smart` / `manual` / `chat_only` |
| **JS 沙箱** | goja: API 白名单 + 128MB 内存 + 5 并发 + ReDoS 防护 |
| **OS 沙箱** | 跨平台: Linux Landlock / macOS Seatbelt / Windows LowIL |
| **Prompt 注入检测** | Guardrail review 模式 |

### 网站克隆与打包

| 维度 | 方案 |
|------|------|
| **克隆引擎** | Chrome 渲染 → Settle 等待 → DOM 清理 → 单次遍历重写+发现 → 资源过滤 → 去重 → 断点续抓 |
| **反反爬** | 10 层: Stealth / Preflight / Antibot 5级升级 / cf_clearance / 161 UA 池 / sec-ch-ua / Referer / ErrNotHTML 路由 / Settle 网络空闲等待 / Proxy Pool |
| **分页支持** | 6 种: 查询参数 / 路径式 / offset-limit / cursor / seek / token |
| **资源下载** | 4 层回退: HTTP 直连 → CDP Network.loadNetworkResource → img 标签 → fetch API |
| **ZIM 打包** | Kiwix 兼容 (ZIM v6, zstd 编码 5): 元数据 + 图标 + 计数器 + 增量集群缓存 |

### 扩展与互通

| 维度 | 方案 |
|------|------|
| **内置扩展** | 17 个: developer / computer_controller / memory / auto_visualiser / tutorial / top_of_mind / code_mode / apps / web / aggregate_search / agent_tools / ard / cortex / bing / google / searxng / tavily |
| **MCP 扩展** | MCP Broker + 独立 MCP Server (:3401) + ACP-MCP Bridge (:3400) |
| **多协议端点** | 7 个: A2A (:9090) / ACP (:9091) / AG-UI SSE (:8080) / ACP-MCP (:3400) / MCP Server (:3401) / ANP (:9092) / Gateway (飞书 WS) |
| **消息网关** | Gateway 插件式 Channel 架构: 飞书/企微 (内部 goroutine，无独立 HTTP 端口) |
| **Agent 互通** | ANP 协议栈: DID 身份 + 能力协商 + E2EE 加密 + HTTP 签名 |
| **双向发现** | ARD: 联邦搜索 + 本地 Catalog + RegistryServer 发布 |

### 配置与存储

| 维度 | 方案 |
|------|------|
| **配置系统** | 35+ 配置段 · 7级加载优先级 · 配置验证 · env var 展开 (20+ 类敏感字段) |
| **存储** | 单文件 `wukong.db` (SQLite WAL 模式) |
| **可选后端** | Redis (会话/记忆) / COS (制品) |

---

## 快速开始

### 安装

```bash
go install github.com/km269/wukong/cmd/wukong@latest
```

### 初始配置

```bash
# 交互式配置向导
wukong configure

# 验证配置
wukong config validate
```

### 日常使用

```bash
# 交互式会话 (TUI)
wukong session
wukong session --provider deepseek --model deepseek-chat

# 单次执行
wukong run --prompt "分析项目结构"

# 查看配置
wukong config show
```

### 网站克隆

```bash
# 克隆网站
wukong apps clone https://example.com --max-pages 50 --max-depth 2

# 预览克隆结果
wukong apps view example.com

# 打包为 ZIM (Kiwix 兼容)
wukong apps pack example.com --format zim --compress
```

### 技能进化

```bash
# 查看进化引擎状态
wukong evolution status

# 查看进化日志
wukong evolution log
wukong evolution log --json

# 重置进化历史
wukong evolution reset
```

### 服务模式

```bash
# 启动无头服务器
wukong server

# 启动指定服务
wukong server --a2a --gateway --agui
```

---

## 技术选型

### 核心框架

| 类别 | 选择 | 版本 | 用途 |
|------|------|------|------|
| Agent 框架 | tRPC-Agent-Go | v1.10.0 | Agent 编排、工具调用、会话管理 |
| MCP 协议 | tRPC-MCP-Go | v0.0.16 | Model Context Protocol |
| A2A 协议 | tRPC-A2A-Go | v0.2.5 | Agent-to-Agent 通信 |
| 记忆引擎 | CortexDB | v2.25.0 | HNSW 向量 + FTS5 全文 + RDF 图谱 |
| 知识格式 | OKF | v0.1 | 开放知识格式 |
| CLI 框架 | Cobra + Viper | v1.9.1 / v1.20.1 | 命令行 + 配置管理 |
| TUI 框架 | Bubble Tea + Bubbles | v1.3.10 / v0.21.0 | 终端用户界面 |

### 浏览器与自动化

| 类别 | 选择 | 版本 | 用途 |
|------|------|------|------|
| 浏览器驱动 | Rod | v0.116.2 | 无头 Chrome 控制 (主后端) |
| 浏览器驱动 | Chromedp | v0.15.1 | 备用 CDP 客户端 |
| 反指纹 | UTLS | v1.5.0 | TLS 指纹伪造 |
| robots.txt | robotstxt | v1.1.2 | robots.txt 解析 |

### 数据存储

| 类别 | 选择 | 版本 | 用途 |
|------|------|------|------|
| 数据库 | SQLite (modernc) | v1.38.2 | 纯 Go SQLite，无需 CGO |
| 向量索引 | CortexDB HNSW | v2.25.0 | 分层导航小世界图 |
| 全文检索 | FTS5 | - | SQLite 全文搜索扩展 |
| 知识图谱 | RDF / SPARQL | - | 资源描述框架 + 查询语言 |
| 缓存 | VectorCache | - | 向量增量缓存 |
| Redis | go-redis | v9.12.1 | 可选会话/记忆后端 |

### 安全与沙箱

| 类别 | 选择 | 版本 | 用途 |
|------|------|------|------|
| JS 沙箱 | goja | - | 纯 Go JavaScript 解释器 |
| Linux 沙箱 | Landlock | - | Linux 内核安全模块 |
| macOS 沙箱 | Seatbelt | - | macOS 沙箱框架 |
| Windows 沙箱 | Low Integrity Level | - | Windows 低完整性级别 |
| 密码学 | x/crypto | v0.51.0 | Ed25519 / X25519 / ChaCha20 |
| HTTP 签名 | RFC 9421 | - | HTTP 消息签名标准 |

### 可观测性

| 类别 | 选择 | 版本 | 用途 |
|------|------|------|------|
| 追踪 | OpenTelemetry | v1.43.0 | 分布式追踪标准 |
| 可观测平台 | Langfuse | - | LLM 应用可观测性 |
| 日志 | slog | - | Go 标准库结构化日志 |

---

## 子系统亮点

### CoreLoop 中央编排引擎

四阶段执行循环，统一协调所有子系统：

```
用户消息
    │
    ├─ Phase 1: Prepare (上下文准备)
    │   ├─ MemoryFlow.IngestTurn
    │   ├─ MemoryFlow.WakeUp (3 层)
    │   ├─ Recall/Cortex.Search
    │   ├─ tRPC Memory.ReadMemories
    │   ├─ OKF KnowledgeIndexInjector
    │   └─ GraphFlow (可选)
    │
    ├─ Phase 2: Execute (执行)
    │   ├─ runner.Run()
    │   ├─ LLM → Tool Calls
    │   ├─ Guard.Check (安全检查)
    │   ├─ ToolSearch (工具过滤)
    │   └─ EvolutionTracker (轨迹捕获)
    │
    ├─ Phase 3: Finalize (收尾)
    │   ├─ StoreMessage
    │   ├─ IngestTurn
    │   ├─ PromoteFacts
    │   ├─ GraphFlow.AutoExtract
    │   └─ Evolution Record
    │
    └─ Phase 4: Return (返回)
        └─ contextMgr.AfterRun
```

### Evolution 技能进化引擎

事件驱动的技能自我进化系统：

| 组件 | 位置 | 功能 |
|------|------|------|
| **EvolutionTracker** | `internal/agent/evolution_tracker.go` | Runner 事件插件，异步捕获执行轨迹 |
| **EvolutionEngine** | `internal/evolution/engine.go` | 异步分析调度 (冷却周期、每日限制) |
| **EvolutionAnalyzer** | `internal/evolution/analyzer.go` | LLM 分析执行轨迹，生成补丁建议 |
| **EvolutionPatcher** | `internal/evolution/patcher.go` | 补丁应用 (去重、版本备份、并发安全) |
| **VersionStore** | `internal/evolution/store.go` | SQLite 版本持久化与历史记录 |
| **OKF Log** | `internal/evolution/patcher.go` | Markdown + JSON 双格式变更日志 |

**关键特性**:
- 事件驱动追踪，不侵入主循环
- 哈希去重防止补丁无限增长
- 最多保留 5 个补丁 section
- 并发安全 (sync.Mutex)
- JSON 日志导出支持外部系统消费

### 双引擎三层记忆系统

```
┌─────────────────────────────────────────────────────────────┐
│  短期记忆 (Short-term) — MemoryFlow                          │
│  会话转录 + 3 层唤醒 + OKF 注入 | 生命周期: 会话内            │
├─────────────────────────────────────────────────────────────┤
│  中期记忆 (Mid-term) — CortexStore                           │
│  HNSW 向量 + FTS5 全文 | 生命周期: 跨会话，可被召回           │
├─────────────────────────────────────────────────────────────┤
│  长期记忆 (Long-term) — tRPC Memory                          │
│  AutoExtract + SmartCleanup | 生命周期: 永久，直到清理触发     │
├─────────────────────────────────────────────────────────────┤
│  结构化记忆 (Graph) — GraphFlow                              │
│  实体抽取 → RDF 图谱 → SPARQL | 生命周期: 永久                │
└─────────────────────────────────────────────────────────────┘
```

**智能清理策略 (SmartCleanup 四维评分)**:
- 评分: 近度 40% + 引用 30% + 重要度 20% + 长度 10%
- 动态 TTL: 引用频次 ≥5 → TTL×2；≥2 → TTL×1.5；==0 → TTL×0.5
- 触发: 80% 容量阈值
- 目标: 清理到 60% 容量

### ANP — Agent Network Protocol

完整的 Agent 互通协议栈：

| 层级 | 组件 | 位置 | 功能 |
|------|------|------|------|
| Bridge | ANPAdapter | `internal/summon/anp_adapter.go` | JSON-RPC 2.0 ↔ A2A 协议桥接 |
| Security | E2EE + HTTP Sign | `internal/summon/e2ee.go` + `internal/ard/http_sign.go` | X25519 + ChaCha20-Poly1305 / RFC 9421 |
| Negotiation | Meta-Protocol | `internal/summon/meta_protocol.go` | JSON-RPC 2.0 能力协商 |
| Discovery | ADP | `internal/ard/adp.go` | /.well-known/agent-descriptions |
| Identity | DID | `internal/ard/did.go` | did:wba (Ed25519 + X25519) |

### OKF — Open Knowledge Format v0.1

Google OKF 规范的完整实现与 6 大系统集成：

| 集成点 | 位置 | 功能 |
|--------|------|------|
| **OKF 核心** | `internal/okf/` | Bundle 加载/写入、Concept 解析 |
| **Skill 兼容** | `internal/skill/` | SKILL.md 添加 type: skill 字段 |
| **Knowledge 互操作** | `internal/knowledge/` | RAG 知识库与 OKF Bundle 互操作 |
| **知识索引注入** | `internal/cortex/` | OKF index.md 注入 MemoryFlow 唤醒上下文 |
| **变更追踪** | `internal/evolution/` | log.md + log.json 双格式 |
| **联邦发现** | `internal/ard/` | OKF Bundle 注册为 ARD CatalogEntry |

### Gateway 多平台消息网关

插件式 Channel 架构，统一入口 + 中间件栈：

```
Platform Channels (Feishu / WeCom...)
    │
    ▼
GatewayServer (transport-agnostic)
    ├─ 签名验证
    ├─ URL 验证 (echostr)
    ├─ 消息解析
    ├─ Dedup (MessageID + TTL)
    ├─ 身份映射
    ├─ RateLimiter (滑动窗口 + 并发控制)
    ├─ SessionStore
    ├─ CoreLoop.Run
    └─ 回复/流式推送
```

---

## 文档索引

### 核心文档

| 文档 | 说明 | 页数 |
|------|------|------|
| [系统架构](docs/ARCHITECTURE.md) | 20 章架构详解 · 24 ADR · 模块依赖 · 数据流 | ~1500 行 |
| [配置手册](docs/CONFIG.md) | 15 组配置 · 全字段说明 · 完整示例 | ~1000 行 |
| [CLI & TUI 架构](docs/CLI_TUI.md) | 命令树 · TUI 架构 · 启动序列 · 事件管道 | ~800 行 |
| [技术实现详解](docs/TECHNICAL_IMPLEMENTATION.md) | 核心模块实现 · 数据流 · 关键算法 | ~1200 行 |
| [API 参考](docs/API_REFERENCE.md) | CoreLoop · Provider · Extension · Security 接口 | ~800 行 |
| [开发者指南](docs/DEVELOPER_GUIDE.md) | 环境搭建 · 项目结构 · 常见开发任务 · 调试 | ~600 行 |
| [部署运维](docs/DEPLOYMENT.md) | Docker · 二进制 · 配置 · 健康检查 · 故障排查 | ~800 行 |

### 专题指南

| 文档 | 说明 |
|------|------|
| [网站克隆技术指南](docs/CLONE_GUIDE.md) | 克隆引擎架构 · 分页处理 · 资源下载策略 |
| [Web 操作深度分析](docs/WEB_OPERATIONS_ANALYSIS.md) | 浏览器/克隆/反爬/检索/HTTP 全链路剖析与优化 |
| [反反爬技术详解](docs/ANTIBOT_GUIDE.md) | 10 层反爬体系 · 5 级升级策略 · 探测技术 |
| [记忆系统架构](docs/MEMORY_ARCHITECTURE.md) | 三层记忆 · CortexDB 技术 · 智能清理算法 |
| [OKF 知识格式](docs/OKF_GUIDE.md) | OKF v0.1 规范 · Bundle 结构 · 6 大集成 |

---

## 许可证

[GNU AGPL-3.0](docs/LICENSE)
