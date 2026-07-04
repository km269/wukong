# Changelog

All changes after v0.1.14 baseline.

---

## [Unreleased] — 2026-07-02

### Configuration Code Refactor — Types Split & Browser Backend Switch

将配置代码从单一的 `types.go` 拆分为 6 个按功能模块组织的文件，优化类型定义和配置验证，并切换浏览器后端为 go-rod。

**types.go 拆分**（`internal/config/`）
- `types_agent.go` — AgentConfig、SecurityConfig 结构体定义
- `types_provider.go` — ProviderConfig、ExtensionConfig、ToolPermission 结构体定义
- `types_storage.go` — SessionConfig、MemoryConfig、TodoConfig、RecallConfig 结构体定义
- `types_cortex.go` — CortexConfig、MemoryFlowConfig、GraphFlowConfig、ImportFlowConfig 结构体定义
- `types_browser.go` — BrowserConfig、BrowserSearchConfig 结构体定义，新增 `BrowserBackendType` 类型（chromedp/rod）
- `types_orchestration.go` — ARDConfig、SummonConfig、ANPConfig、SkillConfig、EvolutionConfig、KnowledgeConfig、OKFConfig、DifyConfig、WorkflowConfig、SubAgentConfig、TeamMemberConfig 结构体定义

**类型优化**
- `BrowserBackendType` 统一定义为 `type BrowserBackendType string`，替代之前的字符串常量
- `WorkflowSubAgentConfig` 改为 `SubAgentConfig` 的类型别名，消除重复定义
- `TeamMemberConfig` 新增 `AllTools`、`AllowedTools`、`Instruction` 字段，支持工具权限控制
- 时间相关配置字段从 `string` 改为 `time.Duration`，利用 Viper 自动解析能力

**配置验证增强**（`internal/config/validate.go`）
- 新增浏览器后端类型验证：仅允许 `chromedp`、`rod` 或空值

**默认值更新**（`internal/config/defaults.go`）
- `browser.backend` 默认值设为 `rod`

**浏览器后端统一**（`internal/browser/backend.go`）
- 使用 `config.BrowserBackendType` 作为类型定义，移除重复的本地类型
- 常量 `BackendChromedp`、`BackendRod` 直接引用 config 包定义

**配置文件优化**（`config.yaml`）
- 更新 `browser.backend: "rod"`，默认启用 go-rod 后端
- 完整的 15 节逻辑分组结构（A-O），包含清晰的注释说明

**编译错误修复**
- 修复 `TodoConfig.Backend` 类型错误（bool → string）
- 修复 `TeamMemberConfig` 缺失字段错误
- 修复 `WorkflowSubAgentConfig` 缺失字段错误
- 修复 `ExtensionConfig.Timeout` 类型错误（string → time.Duration）

---

## [Unreleased] — 2026-07-02

### Gateway — 重构为飞书 WebSocket 长连接

将 Gateway Channel 从 HTTP Webhook（被动接收）重构为**飞书 WebSocket 长连接**（主动拨号），Wukong 作为 client 拨出连接飞书开放平台，**无需公网回调地址/域名/HTTPS**，本地或内网即可运行飞书机器人。

**架构转变**
- 旧：飞书 `POST /feishu/callback` → Wukong HTTP Server (:9093)（需要公网回调 + 验签 + 3s ACK 超时处理）。
- 新：Wukong `wss://` 拨号 → 飞书长连接网关，SDK 自动重连/心跳/合包；事件经 `dispatcher.OnP2MessageReceiveV1` 回调进入处理流水线。

**Channel 接口干净重设计**（`internal/gateway/types.go`）
- 移除 4 个 HTTP 耦合方法：`VerifyRequest`、`HandlePlatformEvent`、`RoutePath`、`ParseMessage([]byte)`。
- 新增 `Start(ctx, MessageHandler)` / `Stop(ctx)` 生命周期方法，Channel 自管入站传输。
- 保留协议无关的 `GatewayMessage` / `BuildUserID` / `BuildSessionID` / `SendReply`。

**GatewayServer 重写为 channel 编排器**（`internal/gateway/gateway.go`）
- 从 HTTP server 改为驱动所有注册 Channel 的编排器：每个 Channel 在独立 goroutine 中 `Start`，共享 `dispatch`（去重→建 ID→限流→会话→异步 agent→回复）。
- 移除 `ChannelRouter` / HTTP 中间件 / `handleMetrics` / `parsePlatformEvent`。
- `processMessage`（原 `processMessageAsync`）逻辑原样保留：agent 运行脱离入站 context，`release()` defer 释放并发槽。

**飞书 Channel 改用 SDK 长连接**（`internal/gateway/feishu/`）
- `channel.go`：`larkws.NewClient` + `dispatcher.OnP2MessageReceiveV1` 接收消息；`Start` 在 goroutine 跑 `wsClient.Start` 并在 ctx 取消时返回。
- `message.go`：新增 `parseP2MessageReceiveV1`（从 SDK 强类型 `*larkim.P2MessageReceiveV1` 解析）；同时修复此前 `_@user_N` 提及占位符未清洗的问题。
- `sender.go`：**完全复用**（流式卡片/文本回复走 tenant_access_token，与接收方式无关），新增 `Close()`。
- 删除 `crypto.go`（长连接模式无 HTTP 验签；事件解密由 SDK 用 encryptKey 完成）。

**移除 WeCom**：用户无 Go 官方 WS SDK，纯 WebSocket 架构下无法适配，整个 `internal/gateway/wecom/` 目录删除；配置同步移除 `gateway.wecom` 段与 `WeComChannelConfig`。

**配置变更**
- 移除 `gateway.address`（纯 WS 无监听端口）。
- `gateway.feishu.verification_token` 标记 deprecated（长连接模式不校验）。

**装配与生命周期**（`internal/cli/session.go`）
- 修复既有 bug：信号处理 goroutine 与 defer cleanup 链此前漏调 `GatewayServer.Stop()`，现已补上（两处）。
- 移除 wecom 注册分支；Start 改用可取消的 lifecycle context。

**测试**：新增 `gateway_test.go`（dispatch 编排器、注册校验、channel 查找）、重写 `feishu/message_test.go`（`parseP2MessageReceiveV1` 各消息类型/非用户发送者过滤/空事件）；`sender_test.go` 内联 `makeFeishuConfig` 辅助（原定义在被删的 `crypto_test.go`）。

**依赖**：`go.mod` 新增飞书 SDK `ws` 子包的传递依赖（`gorilla/websocket`、`gogo/protobuf`），已 `go mod tidy`。

### 注意：WeCom 移除是破坏性变更

`gateway.wecom` 配置与 WeCom channel 代码已删除。如需恢复企业微信支持，需在 Channel 接口下重新实现一条入站传输路径。

---

## [0.2.0] — 2026-07-01

### Gateway — 飞书无响应根因修复

修复飞书机器人无响应的根因，并加固 Gateway 稳健性。

- **签名验证算法修正** (`internal/gateway/feishu/crypto.go`): HMAC-SHA256+appSecret+Base64 → **纯 SHA256+encryptKey+hex**，对齐飞书事件订阅官方规范。收紧空签名头校验：未配置 Encrypt Key 时跳过（明文模式），配置后强制校验。
- **异步 ACK 模式** (`internal/gateway/gateway.go`): handleChannel 改为先返回 200 再后台跑 agent，解决「agent 耗时 > 飞书 3s 回调超时 → 重试被去重丢弃 → 用户无响应」的死锁。agent 运行脱离 HTTP 生命周期，使用独立 context。
- **凭证 fail-fast 校验** (`internal/gateway/feishu/channel.go`, `internal/cli/session.go`): 新增 `Validate()`，app_id/app_secret 缺失时拒绝注册 channel 并明确报错，避免运行时静默失败。
- **限流参数放宽** (`config.yaml`, `defaults.go`, `types.go`, `gateway.go`): `rate_limit_window` 10s→60s，`rate_limit_per_user` 10→20；超限返回 200 而非 429，避免平台重试雪崩。
- **路由精确匹配** (`internal/gateway/router.go`): 路径段边界匹配替代 `HasPrefix`，`/feishu` 不再误吃 `/feishuabc`；注册路径归一化（补 leading `/`、去 trailing `/`），修正去重检测。新增 `router_test.go` (9 用例)。
- **文档同步** (`docs/GATEWAY_DEPLOY.md`, `docs/GATEWAY_CHANNEL_DESIGN.md`): 限流默认值、签名算法说明、文件职责表。

### Config & Documentation Overhaul

- **config.yaml 重构**: 35 未分组节 → 15 逻辑分组 (A-O)，包含清晰层次结构
  - A: 全局, B: 提供商, C: Agent, D: 安全, E: 存储 (E1-E4), F: CortexDB 栈 (F1-F4),
    G: 上下文管理, H: 功能工具 (H1-H6), I: 扩展, J: 服务端点 (J1-J5),
    K: Agent-to-Agent (K1-K4), L: 知识与技能 (L1-L4), M: 工作流,
    N: 可观测性 (N1-N4), O: 项目
- **config.go 扩展**: 环境变量展开从 2 个类别扩展到 10 个类别
  - 新增: 飞书密钥 (3 个)、企微密钥 (3 个)、可观测性 Secret、Artifact COS 密钥、ACP 服务器密钥、CortexDB 密码、Dify 密钥
- **defaults.go 扩展**:
  - 新增 `browser.stealth` 默认值 (之前缺失)
  - 新增 `apps.clone` 完整默认值 (30 个字段，之前全部缺失)
- **Bug 修复**: `config.yaml` 中 `okf.enabled: truee` → `true`
- **文档重构**:
  - `README.md`: 更新统计、新增 Gateway/CLI_TUI 文档链接、简化快速开始、新增环境变量展开覆盖表
  - `docs/README.md`: 更新统计、新增 ANP/Gateway 章节、重组文档链接
  - `docs/ARCHITECTURE.md`: 新增第 6 节 (Gateway 多平台消息通道，含 9 步流水线)、更新系统全景图、新增 ADR #20
  - `docs/CONFIG.md`: 完整重写以匹配 A-O 分组结构、34 结构体索引表、环境变量展开覆盖表、所有组完整 YAML 示例

### ANP Integration — Agent Network Protocol

- **新增 8 个 ANP 源文件**: 完整的 Agent Network Protocol 协议栈实现
  - `internal/ard/adp.go` — Agent Description Protocol (ADP) 文档生成器
  - `internal/ard/anp_types.go` — ANP-07 规范类型定义 (CollectionPage, InterfaceType)
  - `internal/ard/anp_discovery.go` — ANP 发现端点 (/.well-known/agent-descriptions, /agents/{name}/ad.json)
  - `internal/ard/did.go` — did:wba 方法实现 (Ed25519 + X25519, DataIntegrityProof)
  - `internal/ard/http_sign.go` — RFC 9421 HTTP 消息签名实现
  - `internal/summon/anp_adapter.go` — ANP 适配器 (JSON-RPC 2.0 → A2A 桥接)
  - `internal/summon/e2ee.go` — E2EE Messenger (X25519 + ChaCha20-Poly1305)
  - `internal/summon/meta_protocol.go` — Meta-Protocol 引擎 (能力协商 + 接口卡)
- **运行时集成** (`internal/cli/session.go`): BootstrapState 新增 ANP 运行时字段，启动时创建 DID/MetaProtocol/E2EE/ANP HTTP Server
- **服务端点**: ANP HTTP Server 在 `anp.port` (默认 9092) 上注册 `/anp/meta-protocol` (JSON-RPC 2.0) 和 `/anp/capabilities` (GET)

### Configuration System — ANP Config

- **新增 ANPConfig 字段** (`internal/config/types.go`): 10 个字段 (enabled/did_domain/did_path/port/discovery_enabled/meta_protocol_enabled/http_sign_enabled/e2ee_enabled/a2a_enabled/mcp_enabled/agui_enabled)
- **新增 ANP 默认值** (`internal/config/defaults.go`): 9 个默认值注册
- **新增 ANP 校验** (`internal/config/validate.go`): 端口范围检查 + 2 条警告 (DID 域名缺失 / E2EE 无 meta-protocol)
- **扩展 ANP 配置段** (`config.yaml`): 第 21 节，新增 a2a_enabled/mcp_enabled/agui_enabled 字段和详细注释
- **配置结构体总数**: 45 → 45 (ANPConfig 新增 3 字段，总数不变)

### Configuration Refactor

- **types.go**: 修复 ANPConfig 与 WukongConfig 中组件的缩进一致性（tab → 2-space）
- **defaults.go**: 修复 ANP 默认值缩进一致性
- **validate.go**: 新增 ANP 端口验证 + 2 条警告规则

### Documentation Refactor

- **README.md**: 更新统计 (241 .go / 52 _test.go / 29 包 / 45 结构体)，新增 ANP 章节
- **docs/README.md**: 更新统计，新增 ANP 协议特性章节 (2.8)
- **docs/ARCHITECTURE.md**: 更新统计，系统全景图补全 ANP 协议栈层，目录结构补全，新增 ADR #19
- **docs/CONFIG.md**: 更新统计，新增第 21 节 ANP 配置，配置索引新增 ANPConfig
- **CHANGELOG.md**: 记录 ANP 融合和配置系统重构

### Statistics Update

| 指标 | 之前 | 现在 |
|------|------|------|
| `.go` 文件 | 233 | 241 (+8 ANP 文件) |
| `_test.go` 文件 | 52 | 52 |
| 内部包 | 29 | 30 (+1 Gateway) |
| 配置结构体 | 45 | 34 (重组为 A-O 分层分组) |
| 配置分组 | 35 (平铺编号) | 15 (A-O 逻辑分组) |
| 服务端点 (协议) | 4 | 6 (+1 ANP, +1 Gateway :9093) |
| ADR 数 | 18 | 20 (+1 ANP, +1 Gateway) |
| 环境变量展开类别 | 2 | 10 (+8 类别) |

---

## [Previous] — 2026-06-30

### OKF Integration — Open Knowledge Format v0.1

- **新增 `internal/okf/` 包** (3 文件): OKF v0.1 核心实现
  - `bundle.go` — Bundle 加载、Concept 解析、Frontmatter 处理、Markdown 链接提取
  - `writer.go` — Bundle 写入、概念格式化、index.md/log.md 自动生成
  - `bundle_test.go` — 7 个单元测试
- **ARD OKF 联邦发现** (`internal/ard/okf.go`): OKF Bundle 注册为 CatalogEntry
  - 新增 MediaType: `application/okf-bundle+json`
  - URN 格式: `urn:air:wukong.ai:knowledge:<bundle-name>`
- **Cortex OKF EnrichmentAgent** (`internal/cortex/okf_enrichment.go`): 从 DDL/目录自动生成 OKF 概念文档
- **Cortex OKF KnowledgeIndexInjector** (`internal/cortex/okf_injector.go`): OKF 知识索引注入 MemoryFlow 唤醒上下文
- **Evolution OKF 变更追踪** (`internal/evolution/okf.go`): 通过 log.md 追踪知识文件变更
- **Knowledge OKF 导入/导出** (`internal/knowledge/okf.go`): RAG 知识库与 OKF Bundle 互操作
- **Skill OKF 兼容层** (`internal/skill/okf.go`): SKILL.md 文件 OKF 合规 (`type: skill`)

### Configuration System — OKF Config

- **新增 `OKFConfig` 结构体** (`internal/config/types.go`): 7 个字段 (enabled/bundle_dir/injector_enabled/enrichment_enabled/enrichment_output_dir/auto_export/register_in_ard)
- **新增 `setOKFDefaults()`** (`internal/config/defaults.go`): OKF 默认值注册
- **新增 OKF 校验** (`internal/config/validate.go`): 3 条 OKF 相关警告检查
- **新增 `okf:` 配置段** (`config.yaml`): 第 25 节，含详细注释
- **配置结构体总数**: 44 → 45

### Documentation Refactor

- **README.md**: 统计更新 (233 .go / 52 _test.go / 29 包 / 45 结构体)，新增 OKF 章节，新增"知识标准化"哲学
- **docs/README.md**: 统计更新，新增 OKF 特性章节 (2.3)，数据流图新增 OKF 注入步骤，新增第六大哲学
- **docs/ARCHITECTURE.md**: 统计更新，系统全景图新增 OKF 知识层，目录结构新增 `internal/okf/`，新增第 6 章 OKF 知识格式系统，新增 ADR #18
- **docs/CONFIG.md**: 统计更新，新增第 19 节 OKF 配置，配置索引新增 OKFConfig，新增 OKF 推荐配置
- **CHANGELOG.md**: 记录 OKF 融合和配置系统更新

---

## [Previous] — 2026-06-30 (Config Refactor)

### Configuration System Refactor

- **config.go 拆分**: 1613 行单文件拆分为 4 个按职责分离的文件
  - `config.go` — 包文档、`WukongConfig` 根结构体、`Loader`、查询方法
  - `types.go` — 43 个子配置结构体定义
  - `defaults.go` — `setDefaults` 按子系统拆分为 11 个方法
  - `validate.go` — `Validate()` 致命错误检查 + `Warnings()` 非致命警告
- **新增 MemoryConfig 字段**: `enable_smart_cleanup`、`cleanup_trigger_threshold`、`cleanup_target_threshold`、`memory_ttl`
- **新增 CloneDefaults 字段**: `scope_prefix`
- **新增 PackDefaults 结构体**: `apps.pack` 配置段
- **新增配置验证**: `Validate()` + `LoadAndValidate()`
- **config.yaml 重构**: 统一路径约定、替换硬编码 IP、补齐缺失字段

---

## [Previous] — 2026-06-27

### Clone Engine — Chrome 渲染快照

- Settle 网络空闲等待、autoScroll 动态高度、ErrNotHTML 路由
- BFS/DFS 遍历、external robots.txt、AssetWorkers 4→8

### 反反爬体系 — 5 层深度防御

- Stealth + Preflight CF 检测 + 5 级自动升级 + cf_clearance + 161 UA 池

### ZIM 修复

- 集群缓存 key 匹配修复

### CI/CD

- GoReleaser 6 平台 + Homebrew + Scoop + Docker multi-arch
- GitHub Actions: Lint + Test(race) + Cross-build
