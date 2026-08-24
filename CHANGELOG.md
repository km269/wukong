# Changelog

All changes after v0.1.14 baseline.

---

## [0.3.0] — 2026-08-24

### CLI

- `wukong config validate` 改为调用 `loader.LoadAndValidate()`（与启动路径 `bootstrapSession()` 一致），执行 `validate.go` 全部致命规则（todo/mcp_server/sandbox/端口冲突等）并在终端列出全部非致命警告；原先独立的 12 项轻量校验 `runFullValidation()` 保留给 `bench`/`health` 命令使用

### 代码清理

- 连接超时排查日志埋点（全 Debug 级，默认静默）：`errsignal.Classify` 记录错误被判定为哪一类（timeout/DNS/限流/永久）+ 重试裁决 + 截断错误串，`RetryDelay` 记录退避分支（retry-after/限流默认/指数/封顶/跳过）；`pkg/httpclient` 构造期记录 TLS 模式（verify/insecure/custom_ca）与全部超时旋钮（client/dial/TLS handshake timeout、重试参数、ForceIPv4）；cloner `tlsConfigForClone` 记录生效策略与 CA bundle 加载结果；双浏览器池记录 TLS 模式（strict/disabled）。附带修复真实 bug：chromedp 池基础 flag 列表无条件携带 `ignore-certificate-errors`/`ignore-ssl-errors`，使 `InsecureTLS` 条件开关形同虚设（chromedp 后端始终忽略证书验证，rod 后端本就严格）——删除无条件项后双后端统一为严格默认 + 显式 opt-out

- P2-1 阶段六遗留收尾：①`browser.global_render_slots` 接线缺口——config 值原先只在 Controller 路径（`NewBackendFromConfig`）生效，clone/download 任务池直调 `NewBackend` 传 0 自动安装会在"配置了禁用"时静默覆盖配置；现 `EnsureGlobalBudget` 引入禁用标记语义（负值设禁用、正值显式启用清除、0=自动路径尊重禁用），并在 `bootstrapSession` config 验证后统一安装（先于任何池创建，禁用与定容全局生效）。②删除死配置 `apps.clone.browser_pages`（字段从未流入浏览器池，Workers 已承担渲染并发；清理 types/defaults/manager/cloner 默认值/config.yaml/两文档共 7 处）。③删除无调用方的 `browser.NewPoolFromConfig`（约 30 行，连带 config import）。④WEB_OPS §10.2 短板表更新：限速模型（P2-2/3/4 已修）与代码重复（P2-1 六阶段已收敛，rod 独有下载回退链属 CDP 能力差异有意保留）标记已解决
- 双浏览器后端统一（P2-1 阶段六完成，基于全局资源水位的全局调度器）：此前"每任务一池、池间零协调"——常驻 Controller 池 + 每个 clone/download 任务各一个 Chrome 进程池，进程内渲染并发总量 = Σ 各池 Workers，无上限也无资源信号反馈。新增 `renderkit.GlobalBudget` 进程级渲染槽位预算：所有池共享一个准入上限（双后端经共享 `Dispatcher` 骨架单点接入，worker 运行 job 前取全局槽位、运行后归还；未安装预算零开销直通），进程内同时运行的渲染总数被封顶；资源水位自适应——内置 heap 监测器（2s 采样 `HeapInuse` 相对 `GOMEMLIMIT`，未设回退 4GiB 标尺）映射三档压力（≥70%/≥85%），压力使有效预算收缩为上限 1/2、1/4（下限 1、不抢占已持有槽位、随在途渲染自然收敛），回落即恢复。等待用 broadcast channel（close+重建）而非 `sync.Cond` 以保证 `Acquire` 可被 ctx 取消且取消者不占槽位；全局阻塞传导为全链背压（槽位满 → worker 挂起 → 池队列满 → `Submit` 阻塞且可取消 → 上游停止生产）。单例 `EnsureGlobalBudget` 幂等安装（首次定容），`browser.NewBackend` 统一入口接入，config 新键 `browser.global_render_slots`（0=自动 max(4, NumCPU)，负值禁用）。与阶段四优先级正交：池内"谁先跑" vs 全进程"同时跑多少"。renderkit 15 测试 ×3 轮 -race、browser 全家（含真机冒烟）-race、clone -race、config 回归通过

- 双浏览器后端统一（P2-1 阶段五完成，基于任务依赖图的拓扑调度）：新增 `clone/taskGraph` 依赖图调度器（`Declare` 声明外部事件节点 / `Submit` 提交依赖齐后自动运行的 fn 节点 / `Resolve` 外部完成通知；失败以 `depErr` 传给依赖方但 fn 仍运行以保证清理路径与计数释放；建边时 DFS 环检测与重复键拒绝）。反爬重试链改造为图节点：`schedulePageRetry`/`scheduleAssetRetry` 把退避窗口与重派声明为 `backoff→retry` 依赖边，worker 立即返回而非 `time.Sleep` 占住槽位度过整个冷却期（页面渲染错误/内容检测两条路径与资产路径全部接入）。修复两个既有生产 bug：①页面重试经 `enqueuePageWithReferer` 被 frontier 去重静默丢弃（失败尝试已占住 seen 槽而全仓库无一处为重试清 seen，页面重试从未真正发生）——重派现经 `dispatchPageJob` 直发 worker 池绕过去重，重试次数由 escalator `MaxRetries` 按 URL 封顶；②ctx 取消后 `wg.Wait` 永久挂死（page/asset worker 取消即 `return` 使缓冲区剩余 job 的计数泄漏、DFS 栈滞留、满队逃逸路径多处泄漏）——worker 取消后持续排水释放计数，`discardPageStack` 清扫 DFS 栈并置 `dispatcherDead` 使迟到压栈自行丢弃，`enqueuePageWithReferer` 增加 ctx 取消守卫，`drainStack` 取消分支释放已弹出 job。重试渲染并入 High 优先级（退避刚结束应立即渲染，否则冷却窗口被浪费）。taskgraph 7 单测 ×3 轮 -race、clone ×3 轮 -race、browser 全家 -race 回归通过

- 双浏览器后端统一（P2-1 阶段四完成，基于资源优先级的动态调度策略）：`renderkit.Dispatcher` 从纯 FIFO 升级为优先级调度——`types` 新增 `Priority`（Low/Normal/High，零值 `PriorityUnset` 归一为 Normal，既有调用语义不变）与可选能力接口 `PriorityRenderer`（`RenderWithPriority`，两后端实现并补编译期断言）；worker 争用时高优先级渲染插队、同级 FIFO，且任务每等待一个老化间隔（默认 5s）有效优先级升一级，防止严格优先级下低优先级任务饿死。背压保持 4×workers 有界准入且 ctx 可取消（信号量通道实现）；出队为有界队列上的 O(n) 扫描取最优，规避堆键值随老化漂移的一致性问题；Drain/关闭/取消语义与阶段二完全等价。调用点分级接线：克隆种子页、Turnstile 自动解题重渲染（挑战令牌短时效）、downloader 用户请求页 = High；SPA 补救性重渲染（机会性质量提升，不挤占新页面）= Low；其余 = Normal。新增 3 个调度单测（高优先级插队+同级 FIFO、老化反超新鲜普通任务、满队背压阻塞且 ctx 可取消），renderkit 8 测试 ×5 轮 -race 稳定通过，双后端全量 -race 回归通过
- 双浏览器后端统一（P2-1 阶段三完成，基于上下文的生命周期管理）：两后端 `New(ctx, opts)` 将池生命周期绑定到调用方 context——`lifeCtx` 派生浏览器 allocator/launcher，监听 goroutine 在 ctx 取消时自动排水并释放浏览器进程（此前任务 ctx 泄漏浏览器须等进程退出）；显式 `Close` 与自动 `Close` 经 `Drain()` 单执行者守卫互斥（chromedp cancel 闭包消费一次性信号量、rod `MustClose` 非幂等，重入必死锁/panic）；`Close` 末尾 `lifeCancel` 释放监听 goroutine。工厂 `NewBackend`/`NewBackendFromConfig` 增加 ctx 转发；Controller（长生命周期）传 `Background` 自管 Close，enhanced_cloner/downloader（任务型）传任务 ctx——取消任务即回收浏览器。修复 rod 既有生产 bug：无 `<title>` 页面（极简页/错误页）使 `Element("title")` 无限等待挂死渲染，现以 2s 超时限界。新增生命周期测试 4 项（chromedp 3 项不依赖浏览器：ctx 取消自动关/显式 Close 释放监听/nil ctx 回退 Background；rod 真机 1 项含关闭后 Render 拒绝与 Close 幂等，测试页刻意无 title 兼作回归用例），全量 -race 回归通过
- 双浏览器后端统一（P2-1 阶段二完成）：新增 `renderkit.Dispatcher` 共享调度骨架（`RenderJob`/`Submit`/`Drain`/`Closed`），chromedp 与 rod 两后端重复的队列/closed 守卫/双 select 取消/workerLoop/Close 排水骨架收敛为单点（行为等价：workers×4 缓冲、ctx 取消丢弃结果、Drain 幂等且返回是否由本次调用排水——rod 的 `browser.MustClose` 非幂等，清理只由完成排水的调用者执行）；`types` 新增可选能力接口 `UARotator`（两后端）与 `AssetCollector`（rod，`CollectsAssets`），`enhanced_cloner` 匿名 `RotateUA` 断言改用命名接口，两后端补编译期接口断言。新增 Dispatcher 5 单测（提交/错误传播/排队取消/关闭后提交与 Drain 幂等/并发），真实 Chrome 冒烟双绿（1.94s/1.44s），全量 -race 回归通过
- 新增 IP 段动态限速惩罚传播（P2-4）：429/503 惩罚发生时解析违规 host 的 IP（`httpclient.DNSCache` 缓存、可注入解析器便于测试），将惩罚间隔记录到段级（默认 v4 /24、v6 /64，可配 `apps.clone.rate_limit_ip_prefix_v4/v6`）；同段其他 host（CDN 别名）下次限速等待时经新增的 `RateLimiter.RaiseTo` 继承该最小间隔。默认开启（`apps.clone.rate_limit_ip_segment: true`，CLI `--no-ip-rate-limit` 关闭），正常路径零 DNS 开销（无惩罚记录时不触发解析）。新增 5 个单测（段键计算含 v4-mapped//32//48/越界回退、RaiseTo 只升不降、惩罚传播/不降级/解析失败容忍、与 penalizeHost 集成、32 goroutine 并发 -race）
- 修复 P2-3 遗留：`apps.clone.rate_limit_whitelist` 配置键在 manager.go config 路径的接线因编辑器写入竞态丢失（CLI 路径正常），已重新接上
- 新增域名白名单限速豁免（P2-3）：`apps.clone.rate_limit_whitelist` 配置键 + `--rate-limit-whitelist` CLI 旗标（可重复），豁免 host 完全跳过 per-host token bucket 且免疫 429/503 动态降速；匹配大小写不敏感、无端口条目匹配任意端口、带端口条目精确匹配。新增 4 个单测（构建/匹配语义、豁免不建 limiter、惩罚免疫）
- 资产按域名限流补齐动态降速（P2-2）：`clone.RateLimiter` 新增 `SlowDown`（429/503 触发该 host interval ×2，封顶 30s）；`processAsset` 探测到 `DownloadError.StatusCode` 429/503 时经 `penalizeHost` 收紧该 host 的 token bucket（403 属 WAF 拦截不惩罚）；`hostLimiter` 提取为 get-or-create 辅助。新增 4 个单测（递增封顶、Wait 真实生效、多 host 独立、并发 -race）
- 新增双浏览器后端真实 Chrome 冒烟测试（P2-1 阶段二前置解除）：`browser` 与 `rodbackend` 各新增 `TestSmokeRealChrome`（本地 httptest 页面，`-short` 自动跳过，不依赖外网），本机 Chrome 151 双绿（chromedp 1.89s / rod 1.39s，含启动、渲染、JS 链接提取）；复核确认两后端公开面 13 方法签名与 `Options` 字段逐一相同，阶段二剩余阻碍为架构性（rod 独有网络跟踪/referer 缓存/5 级下载回退链约 900 行）
- 双浏览器后端共享提取（P2-1 阶段一）：新增 `internal/browser/renderkit`——chromedp 与 rod 两后端逐字重复的注入 JS（行为模拟 2 段、整页滚动、链接提取）与 `isHTMLContentType`/`isTextContent` 归一到单点（约 150 行去重，3 个单测）；chromedp 版内容类型判断从裸比较升级为大小写/空白/`;` 参数归一化（`"Text/HTML; charset=utf-8"` 此前被误判非 HTML）
- 搜索工具集连接池合并：`aggregate_search.go` 的 6 个独立 `httpclient`（各建 `http.Transport`，连接池分裂）合并为进程级单例 `searchHTTPClient()`；`tavily.go`/`searxng.go` 独立工具同步接入。限流从每后端 10/s 收紧为全局 10/s
- `pkg/httpclient` 新增 `DoWithTimeout`：共享连接池上按请求设置超时（context 截止，body 关闭时释放定时器），用于保留搜索各后端原 15s/20s/30s 差异化预算
- `pkg/httpclient` 重试消费 `Retry-After` 响应头：新增 `retryStatusDelay`/`parseRetryAfter`，429 与 5xx 均重试（429 原先直接返回）；`Retry-After`（秒数/HTTP-date）解析后叠加 10%（上限 1s）抖动防并发对齐；超过 client `Timeout` 的等待不重试、直接交还 429/503 响应。与 jitter 改动合计新增 10 个单测/集成测试
- `pkg/httpclient` 重试退避加入 equal jitter（`retryBackoff`：`base/2 + rand[0, base/2)`，base 为原线性 `(attempt+1)×RetryDelay`），消除并发重试惊群；期望延迟为原线性基座的 75%，下界 base/2。修正 `internal/errsignal.RetryDelay` 注释失实声称（实现并无 jitter）

- 移除 `internal/browser/pool.go` 中残留的空 `append`（vet: append with no values）——DownloadBubble 旗标已并入主 disable-features 集合
- 修复 `internal/util/version.go` 文件头格式错误（空行 + 缩进的 package 声明）
- 全仓库 .go 文件行尾归一为 LF（gofmt 全量通过），新增 `.gitattributes` 锁定 `*.go`/`*.mod`/`*.sum` 为 LF、`*.bat`/`*.cmd` 为 CRLF，防止回归

### 文档重构

**文档结构重组**

- 删除 `SYSTEM_OVERVIEW.md`，内容并入根 `README.md`
- 删除 `docs/TECHNICAL_IMPLEMENTATION.md`：独有章节并入 `docs/ARCHITECTURE.md`（现为 857 行、16 章），ZIM 格式细节并入 `docs/WEB_OPERATIONS_ANALYSIS.md`
- 删除 `docs/implementation-guide.md`（孤儿存根）
- `internal/gateway/README.md` 保留原地（包级开发指南），并在 `docs/README.md` 索引中补充链接

**内容修正**

- `docs/CONFIG.md`: 补 `security.sandbox.*`、`session.enable_model_event_log` 等缺失配置键及验证规则
- `docs/CLI_TUI.md` / `docs/API_REFERENCE.md` / `docs/DEPLOYMENT.md`: 统一 CoreLoop 关闭链顺序、版本号对齐 0.2.9、修正 Redis 配置结构
- `docs/ANTIBOT_GUIDE.md` / `docs/CLONE_GUIDE.md`: 默认值与配置键对齐 `internal/config/defaults.go`、删除虚构 CLI 旗标、errsignal 分类名更正
- `docs/OKF_GUIDE.md`: 删除未实现命令

**口径统一**

- 分页：克隆层 3 种检测模式 + 游标兜底，浏览器 API 发现层 5 种 kind（替代原"6 种分页模式"表述）
- 反爬：5 级反爬升级体系（替代原"10 层"表述）
- 统计数字以代码为唯一真相源：`internal/` 33 个包、`pkg/` 5 个包（capability、httpclient、logutil、sandbox、zim）、内置扩展 17 个、30 个顶层 CLI 命令

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
