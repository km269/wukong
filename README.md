# Wukong — 记忆优先 · 编排驱动 · 安全纵深 · 双向发现

> 本地优先、框架组装、可深度扩展的开源 AI Agent 平台
>
> Go 1.26 | 30 内部包 + 2 公共包 | 34 配置结构体
> CLI: 27 顶层 + 55+ 子命令 | 依赖: 29 direct + 105 indirect

---

## 架构哲学

| 哲学 | 核心信念 | 关键工程决策 |
|------|----------|-------------|
| **记忆优先** | Agent 智能源于跨会话知识积累 | 双引擎三层记忆: tRPC Memory + CortexDB Stack |
| **框架组装** | 任何组件都应可替换 | CoreLoop 依赖注入, 所有子系统接口隔离 |
| **多 Agent 原生** | 编排是第一公民 | 10 种显式编排模式 + HITL 人机协同 |
| **进化智能** | 技能应从失败中学习 | LLM 分析 -> 自动补丁 -> 版本管理 -> 热重载 |
| **双向发现** | 发现别人, 也被人发现 | ARD: 联邦搜索 + RegistryServer 发布 |
| **开放互通** | 标准化协议促进生态互通 | ANP: DID 身份 + 能力协商 + E2EE 加密 |
| **知识标准化** | 知识应有标准形状 | OKF v0.1: Markdown + YAML frontmatter 知识包 |

---

## 核心能力

| 维度 | 方案 |
|------|------|
| **编排模式** | 10 种: single / chain / parallel / cycle / graph / team_* / claude_code / codex / dify |
| **LLM 后端** | 7 种: OpenAI / Anthropic / Google / DeepSeek / Ollama / LMStudio / ACP |
| **记忆系统** | 双引擎三层: tRPC Memory x CortexDB (HNSW+FTS5+RDF) |
| **安全防御** | 5 层纵深: Guard -> goja JS 沙箱 -> OS 沙箱 -> .wukongignore -> OS 权限 |
| **网站克隆** | Chrome 渲染 -> Settle 等待 -> DOM 清理 -> 单次遍历重写+发现 -> 资源过滤 -> 去重 -> 断点续抓 -> 多格式打包 |
| **反反爬** | 10 层: Stealth / Preflight / Antibot 5级升级 / cf_clearance / 161 UA 池 / sec-ch-ua / Referer / ErrNotHTML 路由 / Settle 网络空闲等待 |
| **ZIM 打包** | Kiwix 兼容 (ZIM v6, zstd 编码 5): 元数据 + 图标 + 计数器 + 增量集群缓存 |
| **扩展体系** | 12 内置扩展 + MCP Broker + ACP MCP Bridge |
| **多协议** | 6 端点: Gateway (:9093) / A2A (:9090) / ACP (:9091) / AG-UI SSE (:8080) / ACP MCP (:3400) / ANP (:9092) |
| **消息平台** | 企业微信 + 飞书 (Gateway 通道插件架构) |
| **知识格式** | OKF v0.1: 6 包集成 (okf/ard/cortex/evolution/knowledge/skill) |
| **Agent 互通** | ANP: DID 身份 / 能力协商 / E2EE 加密 / HTTP 签名 |
| **配置系统** | 34 结构体 · 4级加载优先级 · 配置验证 · env var 展开 (8 类敏感字段) |
| **存储** | 单文件 wukong.db (SQLite WAL) |

---

## ANP — Agent Network Protocol

Wukong 实现了 ANP (Agent Network Protocol) 协议栈, 使 Agent 之间能够进行标准化的互通:

| 组件 | 包 | 功能 |
|------|---|------|
| **DID 身份** | internal/ard/did.go | did:wba 方法: Ed25519 签名 + X25519 密钥交换 |
| **ADP 发现** | internal/ard/adp.go + anp_discovery.go | Agent Description Protocol 文档 + 发现端点 |
| **Meta-Protocol** | internal/summon/meta_protocol.go | JSON-RPC 2.0 能力协商引擎 |
| **E2EE 加密** | internal/summon/e2ee.go | X25519 + ChaCha20-Poly1305 端到端加密 |
| **HTTP 签名** | internal/ard/http_sign.go | RFC 9421 HTTP 消息签名 |
| **A2A 桥接** | internal/summon/anp_adapter.go | JSON-RPC 2.0 -> A2A 协议适配器 |

---

## OKF — Open Knowledge Format v0.1

Wukong 实现了 Google 提出的 OKF v0.1 规范:

| 集成点 | 包 | 功能 |
|--------|---|------|
| **OKF 核心** | internal/okf/ | Bundle 加载/写入、Concept 解析、Frontmatter 处理 |
| **Skill 兼容** | internal/skill/ | SKILL.md 添加 type: skill 字段、OKF Bundle 导入/导出 |
| **Knowledge 互操作** | internal/knowledge/ | RAG 知识库与 OKF Bundle 互操作 |
| **知识索引注入** | internal/cortex/ | OKF index.md 注入 MemoryFlow 唤醒上下文 |
| **知识自动化生产** | internal/cortex/ | EnrichmentAgent 从 DDL/目录自动生成 OKF 概念 |
| **变更追踪** | internal/evolution/ | 通过 log.md 追踪知识文件变更历史 |
| **联邦发现** | internal/ard/ | OKF Bundle 注册为 ARD CatalogEntry |

---

## 快速开始

```bash
go install github.com/km269/wukong/cmd/wukong@latest

# 交互式配置
wukong configure

# 交互式会话
wukong session
wukong session --provider deepseek --model deepseek-chat

# 单次执行
wukong run --prompt "分析项目结构"

# 网站克隆
wukong apps clone https://example.com --max-pages 50 --max-depth 2

# 预览 & 打包
wukong apps view example.com
wukong apps pack example.com --format zim --compress

# 配置验证
wukong config validate
```

---

## 技术选型

| 类别 | 选择 | 版本 | 用途 |
|------|------|------|------|
| Agent 框架 | tRPC-Agent-Go | v1.10.0 | Agent 编排 |
| MCP 协议 | tRPC-MCP-Go | v0.0.16 | 模型上下文协议 |
| A2A 协议 | tRPC-A2A-Go | v0.2.5 | Agent 间通信 |
| Agent 互通 | ANP (Agent Network Protocol) | -- | DID + 能力协商 + E2EE |
| 记忆引擎 | CortexDB | v2.25.0 | HNSW+FTS5+RDF |
| 知识格式 | OKF (Open Knowledge Format) | v0.1 | 知识标准化 |
| CLI | Cobra + Viper | v1.9.1 / v1.20.1 | 命令行 |
| 浏览器 | Chromedp | v0.15.1 | 无头 Chrome |
| JS 沙箱 | goja | latest | 安全沙箱 |
| 数据库 | modernc.org/sqlite | v1.38.2 | 纯 Go SQLite |

---

## 配置系统

配置采用 4 级加载优先级:

```
1. CLI 参数 (--provider, --model, --temperature, --max-tokens, --config)
2. 环境变量 (WUKONG_ 前缀)
3. YAML 配置文件 (--config 指定或默认搜索路径)
4. 内置默认值 (internal/config/defaults.go)
```

配置代码按职责拆分为 4 个文件:

| 文件 | 职责 |
|------|------|
| config.go | 根结构体 WukongConfig + Loader + 查询方法 |
| types.go | 34 个子配置结构体定义 |
| defaults.go | 内置默认值 (按子系统分组, 13 个方法) |
| validate.go | 配置验证 (致命错误) + Warnings() (非致命警告) |

env var 展开覆盖 8 类敏感字段: providers API key, A2A remotes, Gateway channels, observability, artifact COS, ACPServer, CortexDB, Dify。

---

## 文档索引

| 文档 | 说明 |
|------|------|
| [架构哲学](docs/README.md) | 七大哲学 · 核心特性 · 数据流 |
| [系统架构](docs/ARCHITECTURE.md) | 19 章架构 · 19 ADR · 模块依赖 |
| [配置手册](docs/CONFIG.md) | 34 结构体 · 全字段 · 推荐方案 |
| [CLI & TUI 架构](docs/CLI_TUI.md) | 命令树 · TUI 架构 · 启动序列 |
| [Gateway 通道设计](docs/GATEWAY_CHANNEL_DESIGN.md) | 多平台消息通道架构 |
| [Gateway 部署](docs/GATEWAY_DEPLOY.md) | 飞书/企微接入 · 运维 |
| [变更日志](CHANGELOG.md) | 版本历史 |

---

## 许可证

[GNU AGPL-3.0](docs/LICENSE)
