# Wukong 涉网应用深度分析与升级建议

> 扫描范围: 10 个涉网子系统 | 分析维度: 实现深度、架构完整度、安全性、性能、可扩展性
> 代码行数: ~8500 行涉网核心代码 | 网络协议: HTTP/1.1, CDP/Chrome DevTools, WebSocket, SSE, JSON-RPC 2.0, MCP
> 扫描日期: 2026-07-24

---

## 目录

1. [架构总览](#1-架构总览)
2. [模块逐析](#2-模块逐析)
3. [跨系统问题](#3-跨系统问题)
4. [分级升级建议](#4-分级升级建议)
5. [实施路线图](#5-实施路线图)

---

## 1. 架构总览

### 1.1 涉网模块全景图

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        Wukong 涉网应用架构                                        │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  ┌─ 出站通信 (Outbound) ──────────────────────────────────────────────────┐    │
│  │                                                                         │    │
│  │  ┌──────────────────┐    ┌──────────────────┐    ┌──────────────────┐    │    │
│  │  │   HTTP Client    │    │  Browser Engine  │    │   MCP Client     │    │    │
│  │  │  pkg/httpclient  │    │  internal/browser │    │  internal/ext    │    │    │
│  │  │                  │    │                  │    │                  │    │    │
│  │  │  • 连接池        │    │  • Rod Backend   │    │  • stdio 传输     │    │    │
│  │  │  • 重试/重退     │    │  • Chromedp      │    │  • SSE 传输       │    │    │
│  │  │  • 指标收集      │    │  • Worker Pool   │    │  • Streamable HTTP│    │    │
│  │  │  • uTLS 指纹     │    │  • 4层下载回退   │    │  • 工具发现       │    │    │
│  │  │  • 代理池轮换    │    │  • 反反爬体系    │    │  • 会话管理       │    │    │
│  │  └────────┬─────────┘    └────────┬─────────┘    └────────┬─────────┘    │    │
│  │           │                       │                       │              │    │
│  │           ▼                       ▼                       ▼              │    │
│  │  ┌──────────────────────────────────────────────────────────────┐          │    │
│  │  │                    反反爬子系统 (Antibot)                       │          │    │
│  │  │  检测引擎 (8 种阻塞) → 升级器 (5 级隐身) → UA池 (161 个)      │          │    │
│  │  │  探针子系统 (HTTP Header / WAF / JS / Rate Limit / Robots)   │          │    │
│  │  └──────────────────────────────────────────────────────────────┘          │    │
│  │                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                 │
│  ┌─ 入站通信 (Inbound) ───────────────────────────────────────────────────┐    │
│  │                                                                         │    │
│  │  ┌──────────────────┐    ┌──────────────────┐    ┌──────────────────┐    │    │
│  │  │    Gateway        │    │  Server Endpoints│    │  ARD Registry    │    │    │
│  │  │  internal/gateway │    │  internal/server │    │  internal/ard    │    │    │
│  │  │                  │    │                  │    │                  │    │    │
│  │  │  • Feishu 渠道   │    │  • ACP Server    │    │  • Catalog API   │    │    │
│  │  │  • WS 长连接     │    │  • AG-UI Server  │    │  • Search API    │    │    │
│  │  │  • 消息去重      │    │  • SSE 流式      │    │  • Federation    │    │    │
│  │  │  • 速率限制      │    │  • Health Check  │    │  • ANP 发现      │    │    │
│  │  │  • OTel 追踪     │    │                  │    │                  │    │    │
│  │  └──────────────────┘    └──────────────────┘    └──────────────────┘    │    │
│  │                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                 │
│  ┌─ Agent 间通信 (Agent-to-Agent) ────────────────────────────────────────┐    │
│  │                                                                         │    │
│  │  ┌──────────────────┐    ┌──────────────────┐    ┌──────────────────┐    │    │
│  │  │   A2A Protocol   │    │   ANP Adapter    │    │   E2EE Messenger │    │    │
│  │  │  internal/summon │    │  internal/summon │    │  internal/summon │    │    │
│  │  │                  │    │                  │    │                  │    │    │
│  │  │  • tRPC-A2A      │    │  • JSON-RPC 2.0  │    │  • X25519 密钥   │    │    │
│  │  │  • AgentCard     │    │  • P1-P7 Profile │    │  • ChaCha20-Poly │    │    │
│  │  │  • Session 传播  │    │  • tRPC↔ANP 桥接 │    │  • DID 身份绑定  │    │    │    │
│  │  └──────────────────┘    └──────────────────┘    └──────────────────┘    │    │
│  │                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                 │
│  ┌─ 观测与存储 (Observability & Storage) ────────────────────────────────┐    │
│  │                                                                         │    │
│  │  ┌──────────────────┐    ┌──────────────────┐    ┌──────────────────┐    │    │
│  │  │  Langfuse        │    │   Redis Session  │    │  ARD Client      │    │    │
│  │  │  Observability   │    │  存储后端        │    │  联邦搜索客户端  │    │    │
│  │  │                  │    │                  │    │                  │    │    │
│  │  │  • OTLP HTTP    │    │  • Redis Lists   │    │  • ai-catalog   │    │    │
│  │  │  • LLM 追踪     │    │  • 事件存储      │    │  • /api/v1/*    │    │    │
│  │  │  • Host/密钥    │    │  • TTL 过期      │    │  • 信任策略     │    │    │
│  │  └──────────────────┘    └──────────────────┘    └──────────────────┘    │    │
│  │                                                                         │    │
│  └─────────────────────────────────────────────────────────────────────────┘    │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 1.2 涉网技术栈矩阵

| 领域 | 技术 | 版本 | 用途 | 成熟度 |
|------|------|------|------|--------|
| HTTP 客户端 | `net/http` + `uTLS` | Go stdlib | 出站请求 | ★★★★☆ |
| 浏览器自动化 | `rod` + `chromedp` | v0.x / v0.x | 页面渲染 + 资源下载 | ★★★★★ |
| 反反爬 | 自研 (8 检测 + 5 升级 + 161 UA) | - | 反检测体系 | ★★★★☆ |
| Agent 协议 | `tRPC-Agent-Go` (A2A) | v1.x | Agent 间通信 | ★★★★☆ |
| ANP 协议 | 自研 JSON-RPC 2.0 + DID | - | 原生 ANP 桥接 | ★★★☆☆ |
| E2EE | X25519 + ChaCha20-Poly1305 | - | 端到端加密 | ★★★★☆ |
| 网关 | 自研 (Feishu WS) | - | 消息渠道 | ★★★☆☆ |
| MCP | `trpc-mcp-go` | v1.x | 扩展协议 | ★★★★☆ |
| ARD | 自研 HTTP API | - | Agent 发现 | ★★★☆☆ |
| 观测 | `langfuse` via OTLP | v1.x | LLM 追踪 | ★★★★☆ |
| 会话 | `go-redis/v9` | v9.x | Redis 会话存储 | ★★★★☆ |
| TLS 指纹 | `refraction-networking/utls` | v1.x | Chrome TLS 模拟 | ★★★★☆ |

---

## 2. 模块逐析

### 2.1 HTTP 客户端 (`pkg/httpclient`)

**文件**: httpclient.go (~440 行)

#### 当前实现

- 完整的 HTTP 客户端封装，内置连接池、重试机制、指标收集、错误分类
- 支持 TLS 指纹模拟（通过 uTLS），ForceIPv4 选项
- 支持代理 URL 和代理池配置
- 自动设置完整浏览器请求头（Sec-Ch-Ua、Sec-Fetch-*、Accept-Language 等）
- 5 类错误分类（Network/Timeout/TLS/Server/Client）
- 14 项运行时指标（请求数、成功率、延迟、错误分布、URL 统计）

#### 优势

- ✅ 完善的指标收集，便于生产监控
- ✅ 智能重试逻辑，只对可恢复错误重试
- ✅ uTLS 指纹模拟，绕过 JA3/JA4 指纹检测
- ✅ 强制 IPv4，规避 Windows IPv6 权限问题

#### 问题与差距

| # | 问题 | 严重度 | 详情 |
|---|------|--------|------|
| 1 | **HTTP/2 被禁用** | 🔴 高 | `ForceAttemptHTTP2: false` 导致现代 HTTP/2 服务器降级到 HTTP/1.1，丢失多路复用和头部压缩性能 |
| 2 | **Keep-Alive 被禁用** | 🟡 中 | `DisableKeepAlives: true` 每次请求重建 TCP 连接，对高频请求场景性能影响显著 |
| 3 | **无 DNS 缓存** | 🟡 中 | 高频爬虫场景下重复 DNS 查询开销大 |
| 4 | **无请求速率限制** | 🟡 中 | 多协程并发时无全局速率控制，可能触发目标站点限流 |
| 5 | **ProxyRotateEvery 未使用** | 🟢 低 | Options 中定义了 `ProxyRotateEvery` 但从未在逻辑中引用 |
| 6 | **日志输出到 stderr** | 🟢 低 | 直接 `fmt.Fprintf(os.Stderr)` 不利于日志聚合 |

#### 升级建议

1. **P0 - 启用 HTTP/2 和 Keep-Alive**
```go
transport := &http.Transport{
    ForceAttemptHTTP2:   true,     // 启用 HTTP/2
    DisableKeepAlives:   false,    // 保持长连接
    MaxIdleConnsPerHost: 10,       // 每主机最大空闲连接
    IdleConnTimeout:     60 * time.Second,
    // ... 其他配置
}
```

2. **P1 - 增加 DNS 缓存层**
```go
type DNSCache struct {
    cache   map[string][]net.IP
    ttl     time.Duration
    mu      sync.RWMutex
    resolver *net.Resolver
}
```

3. **P1 - 全局速率限制器**
```go
type RateLimiter struct {
    tokens  chan struct{}
    maxRate int
}
```

---

### 2.2 浏览器引擎 (`internal/browser`)

**文件**: pool.go (~860 行), backend.go (~120 行), controller.go (~200 行), rodbackend/pool.go (~500 行)

#### 当前实现

双后端架构：
- **Chromedp 后端** (`browser/pool.go`): 经典 chromedp 驱动，完整实现 Worker Pool + 任务队列
- **Rod 后端** (`browser/rodbackend/pool.go`): 新一代 rod 驱动，支持 Referer 缓存
- 共享接口: `types.BrowserBackend` (Render / DownloadAsset / Close)

核心能力：
- Worker Pool 并发渲染（默认 4 并发）
- 4 层资源下载回退（直连导航 → img 标签 → fetch API → CDP loadNetworkResource）
- 反反爬集成（UA 轮换、隐身脚本注入、行为模拟）
- Stealth 模式（Chrome 标志 + JS 反检测脚本）
- 自动滚动和 Settle 等待
- Cookie 提取和 Cloudflare Clearance 捕获
- Referer 页面缓存（Rod 后端独有）
- 6 种 URL 分页识别

#### 优势

- ✅ 双后端可切换，适配不同场景
- ✅ 4 层下载回退策略非常完善，覆盖大多数反爬场景
- ✅ Referer 缓存机制有效减少重复导航错误
- ✅ Chrome 启动旗标针对防御部门等严苛环境优化
- ✅ 与反反爬系统深度集成

#### 问题与差距

| # | 问题 | 严重度 | 详情 |
|---|------|--------|------|
| 1 | **Chromedp 与 Rod 代码重复** | 🔴 高 | 两个后端实现了大量相同逻辑（UA 管理、下载回退、headers 设置），维护成本高 |
| 2 | **Chromedp 硬编码 Sec-Fetch-Headers** | 🟡 中 | `browser/pool.go#L196-216` 手动设置了完整 Sec-Fetch-* 头，与项目记忆中"不要手动设置 Sec-Fetch-*"的约束矛盾 |
| 3 | **行为模拟过于简单** | 🟡 中 | 随机滚动 + 随机鼠标移动，缺乏真实人类轨迹模型（如贝塞尔曲线鼠标轨迹、加速/减速滚动） |
| 4 | **硬编码代理 URL** | 🟡 中 | `controller.go` 中代理池仅使用 `ProxyPool[0]`，未实现轮换 |
| 5 | **无浏览器会话复用** | 🟢 低 | 每次渲染创建新 Tab，Cookie/会话状态在并发间不共享 |
| 6 | **缺少性能指标** | 🟢 低 | 无渲染耗时、资源下载成功率等关键指标的收集 |

#### 升级建议

1. **P0 - 统一后端抽象层**
```go
type UnifiedBackend interface {
    BrowserBackend          // 保留现有接口
    SetProxyPool([]string)  // 统一代理池管理
    Metrics() RenderMetrics // 性能指标收集
}

// 共享逻辑提取到 browser/common.go
type BasePool struct {
    // 共享的 UA 管理、headers 构建、下载回退逻辑
}
```

2. **P0 - 修正 Sec-Fetch-Headers 处理**
Chromedp 后端的 `renderJob` 应遵循与 Rod 后端相同的原则 — 不手动设置 Sec-Fetch-* 头，让 Chrome 自动处理。

3. **P1 - 升级行为模拟为真实轨迹**
```go
type HumanBehaviorSimulator struct {
    // 贝塞尔曲线鼠标轨迹生成
    // 加速/减速滚动模型
    // 随机停顿（阅读、思考时间）
    // 键盘输入节奏模拟
}
```

4. **P2 - 增加浏览器会话池**
为同一域名的请求复用 Cookie Jar 和页面上下文。

---

### 2.3 反反爬系统 (`internal/browser/antibot`)

**文件**: detector.go (~300 行), escalator.go (~470 行), antibot.go (~160 行), prober/* (~5 子模块)

#### 当前实现

**检测引擎**:
- 8 种阻塞原因识别（403/429/503/Cloudflare/CAPTCHA/Blocked/Timeout/Empty）
- 双层检测：HTTP 响应头 + DOM 内容分析
- Cloudflare 专项检测（cf-ray、Turnstile、5 种云盾标记）

**升级机制**:
- 5 级隐身等级（None → Flags → Stealth → Aggressive → Backoff）
- 自动升级：检测 → 升级 → 重试
- 每 URL 重试计数 + 冷却期
- 升级事件历史记录

**UA 池**:
- 161 个真实浏览器 UA Profile
- 包含 UserAgent、SecChUa、SecChUaMobile、SecChUaPlatform
- 轮换策略：随机选择 + 失败报告

**探针子系统**:
- HTTP 头探针、WAF 探针、JS 挑战探针、速率限制探针、Robots 探针

#### 优势

- ✅ 检测维度全面，覆盖主流反爬技术
- ✅ 5 级升级策略渐进式增强，避免过度暴露
- ✅ UA Profile 包含完整 Sec-Ch-Ua 指纹，现代浏览器级伪装
- ✅ 独立探针子系统可按需组合使用

#### 问题与差距

| # | 问题 | 严重度 | 详情 |
|---|------|--------|------|
| 1 | **无 TLS 指纹轮换** | 🔴 高 | JA3/JA4 指纹是现代反爬的重要手段，但系统仅在 HTTP Client 层使用 uTLS，浏览器层无 TLS 指纹轮换 |
| 2 | **无代理健康检查联动** | 🟡 中 | 检测到阻塞后不会自动切换代理，升级与代理池脱节 |
| 3 | **无浏览器指纹注入** | 🟡 中 | 仅依赖 Chrome `disable-blink-features=AutomationControlled`，缺少 WebGL、Canvas、AudioContext 指纹 |
| 4 | **UA 与实际能力不匹配** | 🟡 中 | UA 声称是 Chrome 130 但 Chrome 版本可能不同步，存在一致性破绽 |
| 5 | **无 Cookie 管理策略** | 🟢 低 | 缺少 Cookie 持久化、过期清理、域名隔离策略 |
| 6 | **Cloudflare 绕过不完整** | 🟢 低 | 仅捕获 cf_clearance Cookie，未处理 Turnstile/Managed Challenge 等高级挑战 |

#### 升级建议

1. **P0 - TLS 指纹轮换集成**
```go
type TLSFingerprintManager struct {
    profiles []tls.FingerprintProfile  // Chrome 不同版本的 TLS 指纹
    current  int
}
// 在每次升级到 LevelStealth 以上时轮换 TLS 指纹
```

2. **P1 - 浏览器指纹注入**
```go
func injectBrowserFingerprint(ctx context.Context, profile *BrowserProfile) {
    // WebGL 渲染器伪装
    // Canvas 指纹噪声注入
    // AudioContext 指纹伪装
    // Hardware Concurrency / Device Memory 匹配
}
```

3. **P1 - 升级与代理池联动**
```go
func (e *Escalator) OnBlockDetected(reason BlockReason) {
    // 检测到 Cloudflare 时自动切换到带 cf_clearance 的代理
    // 检测到 RateLimit 时切换代理 + 增加延迟
    e.proxyPool.Rotate()
}
```

---

### 2.4 Agent 间通信 (`internal/summon`)

**文件**: a2a.go (~200 行), anp_adapter.go (~250 行), e2ee.go (~200 行), auth.go (~150 行), meta_protocol.go (~100 行)

#### 当前实现

**A2A 协议**:
- 基于 `tRPC-Agent-Go` 官方 `server/a2a` 包
- 支持 AgentCard 自动生成、Session 传播、流式响应
- 认证配置支持 JWT / API Key / OAuth2 三种模式

**ANP 适配器**:
- tRPC 事件 ↔ ANP JSON-RPC 2.0 桥接
- 支持 P1（核心绑定）、P3（直接消息）、P5（E2EE 覆盖）、P7（附件）四个 Profile
- 消息类型：request / response / notification / error

**E2EE 加密**:
- X25519 密钥协商（基于 did:wba key-2）
- ChaCha20-Poly1305 认证加密
- 每远程会话独立密钥
- DID 身份绑定（key-1 签名 + key-2 加密）

**HTTP 签名**:
- RFC 9421 HTTP Message Signatures
- Ed25519 签名 + Content-Digest (RFC 9530)
- 重放保护（created + expires + nonce）

#### 优势

- ✅ 协议栈完整：A2A（tRPC 原生）→ ANP（JSON-RPC 桥接）→ E2EE（加密层）→ HTTP 签名（认证层）
- ✅ 加密实现专业：X25519 + ChaCha20-Poly1305 是业界标准组合
- ✅ DID 身份绑定确保了 Agent 间通信的可追溯性
- ✅ 支持流式传输和附件

#### 问题与差距

| # | 问题 | 严重度 | 详情 |
|---|------|--------|------|
| 1 | **无连接池/复用** | 🔴 高 | A2A Server 每次创建新的 `http.Server`，无连接复用机制 |
| 2 | **无消息队列缓冲** | 🟡 中 | 高并发场景下消息直接发送，无背压控制 |
| 3 | **心跳/Keep-Alive 缺失** | 🟡 中 | 长连接场景下无心跳机制，无法及时检测断连 |
| 4 | **ANP Profile 覆盖不全** | 🟡 中 | 仅实现 P1/P3/P5/P7，缺少 P2（传输层）、P4（流式）、P6（推送）、P8（状态）、P9（发现） |
| 5 | **auth.go 密钥管理分散** | 🟢 低 | 密钥生成、存储、轮换逻辑在多处，缺少统一的密钥管理服务 |
| 6 | **无消息审计日志** | 🟢 低 | Agent 间消息无可选审计日志功能 |

#### 升级建议

1. **P1 - 增加连接池和心跳机制**
```go
type AgentConnectionPool struct {
    pool     map[string][]*AgentConn  // 按远端 DID 分组
    interval time.Duration           // 心跳间隔
    timeout  time.Duration           // 连接超时
}
```

2. **P1 - 实现消息背压控制**
```go
type MessageQueue struct {
    bufferSize int
    overflowPolicy OverflowPolicy  // drop_oldest / block / expand
}
```

3. **P2 - 扩展 ANP Profile 覆盖**
实现 P4（流式传输）和 P9（服务发现），使 ANP 通信更完整。

---

### 2.5 网关系统 (`internal/gateway`)

**文件**: gateway.go (~300 行), feishu/channel.go (~200 行), feishu/sender.go (~150 行), ratelimit.go (~100 行), dedup.go (~80 行)

#### 当前实现

**架构设计**:
- 传输无关（transport-agnostic）: GatewayServer 不持有 HTTP 监听，每个 Channel 自带入站传输
- 管道化处理：去重 → 用户/会话映射 → 速率限制 → 会话持久化 → Agent 执行 → 回复派发
- OTel 追踪集成

**Feishu 渠道**:
- WebSocket 长连接接入（wss://）
- 无需公网回调 URL
- 支持流式交互卡片回复
- Lark SDK 自动管理认证/重连/心跳

**速率限制**:
- 每用户限制 + 全局并发闸门
- 可配置窗口和阈值

**消息去重**:
- 基于 MessageID 的幂等处理
- 处理平台 SDK 重试

#### 优势

- ✅ 传输无关的架构设计非常优雅，便于扩渠道
- ✅ OTel 全链路追踪，可观测性好
- ✅ 流式卡片回复用户体验好
- ✅ 消息幂等处理确保不重复执行

#### 问题与差距

| # | 问题 | 严重度 | 详情 |
|---|------|--------|------|
| 1 | **仅支持 Feishu 渠道** | 🔴 高 | 无 Slack / Discord / Telegram / WhatsApp 等主流即时通讯渠道 |
| 2 | **无 Web 聊天渠道** | 🔴 高 | 缺少 Web Widget/Chat 嵌入 SDK，无法嵌入第三方网站 |
| 3 | **速率限制器功能单一** | 🟡 中 | 仅支持令牌桶，缺少滑动窗口、漏桶等策略 |
| 4 | **无离线消息存储** | 🟡 中 | Agent 不在线时消息丢失，无排队存储 |
| 5 | **缺少多租户隔离** | 🟢 低 | 无租户级别的资源隔离和配额管理 |
| 6 | **无消息加密传输** | 🟢 低 | 仅依赖平台传输层加密，无应用层 E2EE |

#### 升级建议

1. **P0 - 增加 Slack/Discord 渠道**
```go
type SlackChannel struct { /* WebSocket + Socket Mode */ }
type DiscordChannel struct { /* Gateway Intent */ }
type TelegramChannel struct { /* Bot API */ }
```

2. **P1 - 增加 Web Chat Widget**
```go
type WebChatChannel struct {
    // SSE/WebSocket 双向通信
    // 可嵌入任何网站的 JS SDK
}
```

3. **P1 - 扩展速率限制策略**
```go
type RateLimiterStrategy interface {
    Allow(ctx context.Context, key string) (bool, time.Duration)
}
// 实现: TokenBucket / SlidingWindow / LeakyBucket / Adaptive
```

4. **P2 - 离线消息队列**
使用 Redis List 存储离线消息，Agent 上线后自动投递。

---

### 2.6 ARD 发现系统 (`internal/ard`)

**文件**: server.go (~200 行), client.go (~150 行), federation.go (~150 行), registry.go (~200 行), http_sign.go (~200 行)

#### 当前实现

**注册中心**:
- HTTP API: `/.well-known/ai-catalog.json`、`/api/v1/search`、`/api/v1/explore`、`/api/v1/agents`
- 可开关的搜索/探索/列表端点
- 健康检查 + CORS 中间件

**联邦搜索**:
- 多注册中心并行查询
- 超时控制 + 最大注册中心数
- 信任策略（Any/Known/Verified）
- 转介（Referral）深度限制
- 指标收集（延迟、错误统计）

**客户端**:
- 基于 `net/http.Client` 的简单实现
- FetchCatalog / Search / Explore / List 方法

**HTTP 签名**:
- RFC 9421 完整实现
- Ed25519 签名 + Content-Digest
- 重放保护

#### 优势

- ✅ 标准 API 设计，符合 A2A/ARD 生态
- ✅ 信任策略分级合理
- ✅ HTTP 签名实现专业
- ✅ 联邦搜索支持转介发现

#### 问题与差距

| # | 问题 | 严重度 | 详情 |
|---|------|--------|------|
| 1 | **客户端未用统一 HTTP Client** | 🔴 高 | `ard.Client` 使用裸 `http.Client`，无重试/指标/错误分类，与系统其他部分不一致 |
| 2 | **注册中心无速率限制** | 🔴 高 | 公共 `ai-catalog.json` 和搜索 API 无防滥用措施 |
| 3 | **无缓存层** | 🟡 中 | 高频查询时重复计算，缺少内存/Redis 缓存 |
| 4 | **联邦无熔断机制** | 🟡 中 | 远端注册中心持续失败时无熔断，每次都等待超时 |
| 5 | **语义搜索有限** | 🟢 低 | `semantic.go` 存在但向量搜索能力未与 CortexDB 深度集成 |
| 6 | **无 Prometheus 指标导出** | 🟢 低 | 有 FederationMetrics 但未暴露到 /metrics 端点 |

#### 升级建议

1. **P0 - 统一使用 `pkg/httpclient`**
```go
func NewClient(timeout time.Duration) *Client {
    return &Client{
        httpClient: httpclient.New(httpclient.Options{
            Timeout:    timeout,
            MaxRetries: 2,
        }),
        timeout: timeout,
    }
}
```

2. **P0 - 增加注册中心速率限制**
```go
func (s *RegistryServer) setupRoutes() {
    // 全局速率限制: 100 req/s
    s.mux = rateLimitMiddleware(s.mux, 100)
    // ...
}
```

3. **P1 - 增加熔断和缓存**
```go
type FederationWithBreaker struct {
    breaker *circuit.Breaker  // 熔断: 连续5次失败后30秒不再请求
    cache   *ttl.Cache        // 搜索结果缓存 30s
}
```

---

### 2.7 MCP 扩展协议 (`internal/extension`)

**文件**: mcp_client.go (~120 行), manager.go (~200 行), factory.go (~80 行), acp_mcp.go (~100 行)

#### 当前实现

**MCP 客户端**:
- 基于 `trpc-mcp-go` 的原生客户端封装
- 支持 stdio / SSE / Streamable HTTP 三种传输
- 工具发现 + 过滤器（glob 模式支持）
- 生命周期管理（创建/关闭/重连）

**扩展管理器**:
- 统一的扩展注册、启停、配置管理
- 内置扩展自动注册（12 个内置扩展）
- 支持动态启用/禁用

**ACP-MCP 桥接**:
- ACP 协议 ↔ MCP 协议转换层
- 使 ACP Client 能调用 MCP Server 工具

#### 优势

- ✅ 三种传输全覆盖，兼容所有 MCP Server 实现
- ✅ Glob 模式工具过滤，灵活控制工具可见性
- ✅ ACP-MCP 桥接打通了两套协议生态
- ✅ 内置扩展自动注册机制，零配置可用

#### 问题与差距

| # | 问题 | 严重度 | 详情 |
|---|------|--------|------|
| 1 | **无工具调用审计** | 🟡 中 | MCP 工具调用无日志记录，审计困难 |
| 2 | **无连接健康检查** | 🟡 中 | 长连接 MCP Server 无定期健康检查和自动重连 |
| 3 | **无工具权限控制** | 🟡 中 | 所有注册用户/会话看到相同的工具集，无细粒度权限 |
| 4 | **stdio 模式无超时** | 🟢 低 | stdio 子进程如果卡死，无超时强制终止机制 |
| 5 | **缺少 MCP Server 端实现** | 🟢 低 | 仅实现 Client 端，无法作为 MCP Server 被其他 Agent 调用 |

#### 升级建议

1. **P1 - 增加工具调用审计日志**
```go
type ToolAuditLog struct {
    SessionID   string
    Extension   string
    ToolName    string
    Args        json.RawMessage
    Duration    time.Duration
    Status      "success" | "error" | "timeout"
    ErrorMsg    string
}
```

2. **P1 - 增加连接健康检查和自动重连**
```go
func (c *MCPClient) startHealthCheck(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
    for {
        select {
        case <-ticker.C:
            if err := c.ping(); err != nil {
                c.reconnect()
            }
        case <-ctx.Done():
            return
        }
    }
}
```

3. **P2 - 实现 MCP Server 端**
使 Wukong 自身能作为 MCP Server 暴露工具，被其他 Agent 调用。

---

### 2.8 服务端点 (`internal/server`)

**文件**: acp.go (~200 行), agui.go (~150 行)

#### 当前实现

**ACP Server**:
- HTTP 端点: `POST /acp/message/send`、`GET /acp/tools/list`、`POST /acp/tools/call`、`GET /acp/.well-known/agent.json`、`GET /acp/health`
- SSE 流式响应（text_delta / tool_call / done 事件）
- 请求体大小限制 10MB

**AG-UI Server**:
- SSE 端点: `POST /agui`
- 轻量级 Web Chat UI 协议
- Wukong 原生实现（因 tRPC-Agent-Go v1.10.0 无 server/agui）

#### 优势

- ✅ ACP 协议实现完整，兼容主流 ACP Client
- ✅ SSE 流式响应用户体验好
- ✅ 请求体大小限制防止内存耗尽

#### 问题与差距

| # | 问题 | 严重度 | 详情 |
|---|------|--------|------|
| 1 | **无 TLS/HTTPS 支持** | 🔴 高 | 仅支持 HTTP，生产环境必须前置反向代理 |
| 2 | **无认证/授权** | 🔴 高 | ACP 和 AG-UI 端点完全开放，任何能访问的人都能调用 |
| 3 | **无速率限制** | 🟡 中 | 公共端点易被滥用 |
| 4 | **无 CORS 配置** | 🟡 中 | 跨域 Web 应用可能无法调用 |
| 5 | **无健康检查深度** | 🟢 低 | 仅返回 OK 状态，无依赖组件健康信息 |
| 6 | **Graceful Shutdown 简化** | 🟢 低 | 无连接排空和请求等待机制 |

#### 升级建议

1. **P0 - 增加 HTTPS/TLS 支持**
```go
type ServerTLSConfig struct {
    CertFile   string
    KeyFile    string
    CACertFile string  // 可选，mTLS
}
// 使用 http.Server 的 TLSConfig
```

2. **P0 - 增加认证中间件**
```go
func authMiddleware(next http.Handler) http.Handler {
    // JWT / API Key / OAuth2 令牌验证
    // 与 Gateway 认证配置统一
}
```

3. **P1 - 增加速率限制和 CORS**
```go
mux = cors.New(cors.Options{
    AllowedOrigins:   config.AllowedOrigins,
    AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
    AllowCredentials: true,
}).Handler(mux)

mux = rateLimitMiddleware(mux, 50)  // 50 req/s per IP
```

---

## 3. 跨系统问题

### 3.1 HTTP Client 碎片化

| 模块 | 使用的 HTTP 客户端 | 问题 |
|------|-------------------|------|
| `pkg/httpclient` | 自研封装 | ✅ 功能最全 |
| `internal/ard/client.go` | 裸 `http.Client` | ❌ 无重试/指标 |
| `internal/summon/a2a.go` | `http.Server` | ⚠️ 服务端 |
| `internal/server/acp.go` | `http.Server` | ⚠️ 服务端 |
| `internal/gateway` | Lark SDK 内置 | ✅ SDK 管理 |
| `internal/extension` | tRPC-MCP-Go 内置 | ✅ SDK 管理 |

**结论**: 应统一所有客户端出站请求到 `pkg/httpclient`，消除裸 `http.Client` 的使用。

### 3.2 代理池管理不一致

- `pkg/httpclient` 的 `ProxyPool` 定义了 `ProxyRotateEvery` 但未使用
- `internal/browser/proxy_pool.go` 实现了 `SmartProxyPool`（含健康检查、CF Clearance）
- `internal/browser/backend.go` 中的 `NewBackendFromConfig` 使用全局 `globalProxyPool`
- **缺失**: HTTP Client 层无代理池轮换，浏览器层有但与 HTTP 层不联动

**建议**: 将 `SmartProxyPool` 提升为系统级服务，HTTP Client 和浏览器引擎共享。

### 3.3 可观测性覆盖不均

| 模块 | 有指标收集 | 有 OTel 追踪 | 有日志 |
|------|-----------|-------------|--------|
| HTTP Client | ✅ 14 项 | ❌ | ✅ stderr |
| 浏览器引擎 | ❌ | ❌ | ✅ stderr |
| 反反爬 | ⚠️ 事件历史 | ❌ | ❌ |
| 网关 | ⚠️ OTel 部分 | ✅ | ❌ |
| ARD | ⚠️ FederationMetrics | ❌ | ❌ |
| A2A/ANP | ❌ | ❌ | ❌ |
| Server | ❌ | ❌ | ❌ |

**建议**: 统一接入 OTel 标准指标 + 追踪，替换所有 `fmt.Fprintf(os.Stderr)` 为结构化日志。

### 3.4 安全防线不均匀

| 安全层 | 覆盖的模块 | 缺失的模块 |
|--------|-----------|-----------|
| HTTPS/TLS | 浏览器引擎（Chrome） | Server 端点、ARD Registry |
| 认证 | Gateway（Feishu SDK）、A2A（JWT/API Key） | ACP Server、AG-UI Server、ARD Client |
| 速率限制 | Gateway | Server 端点、ARD Registry |
| 输入验证 | Server（10MB limit） | 全部 |
| 加密传输 | E2EE（Agent 间）、HTTPS（浏览器） | Server 端点间 |

### 3.5 错误处理不统一

- HTTP Client: 结构化错误分类 + 智能重试
- 浏览器引擎: `fmt.Fprintf` + 裸 error
- ARD: 裸 error
- Gateway: OTel 记录 + 返回
- 反反爬: 事件历史记录 + 裸 error
- **建议**: 统一错误包装规范，所有模块使用 `fmt.Errorf("operation: %w", err)` + 错误分类。

---

## 4. 分级升级建议

### P0 — 紧急（安全/性能关键）

| # | 升级项 | 影响模块 | 工作量 | 预期收益 |
|---|--------|---------|--------|---------|
| **P0-1** | 启用 HTTP/2 + Keep-Alive | `pkg/httpclient` | 小 | 高（性能提升 30-50%） |
| **P0-2** | Server 端点增加 HTTPS + 认证 | `internal/server` | 中 | 高（安全基线） |
| **P0-3** | Chromedp 后端修正 Sec-Fetch-* 头 | `internal/browser/pool.go` | 小 | 中（降低反爬检测） |
| **P0-4** | ARD Registry 增加速率限制 | `internal/ard/server.go` | 小 | 中（防滥用） |
| **P0-5** | 统一 `pkg/httpclient` 到所有出站请求 | `internal/ard`, `internal/summon` | 中 | 高（一致性+可观测性） |
| **P0-6** | 增加浏览器 TLS 指纹轮换 | `internal/browser/antibot` | 中 | 高（反检测升级） |

### P1 — 重要（架构完善）

| # | 升级项 | 影响模块 | 工作量 | 预期收益 |
|---|--------|---------|--------|---------|
| **P1-1** | 统一后端抽象层（去重 Chromedp/Rod） | `internal/browser` | 大 | 高（维护成本 -50%） |
| **P1-2** | 增加 DNS 缓存层 | `pkg/httpclient` | 小 | 中（高频爬取性能） |
| **P1-3** | 增加全局速率限制器 | `pkg/httpclient` | 小 | 中（防目标站点限流） |
| **P1-4** | 网关增加 Slack/Discord/Web Chat | `internal/gateway` | 大 | 高（用户覆盖扩大 5x） |
| **P1-5** | ARD 联邦增加熔断和缓存 | `internal/ard` | 中 | 中（稳定性提升） |
| **P1-6** | MCP 增加工具审计 + 健康检查 | `internal/extension` | 中 | 中（可观测性） |
| **P1-7** | 浏览器升级行为模拟（人类轨迹） | `internal/browser/behavior` | 中 | 中（反检测升级） |
| **P1-8** | 反反爬增加代理池联动 | `internal/browser/antibot` | 中 | 中（反爬成功率 +20%） |
| **P1-9** | 结构化日志替换 stderr 输出 | 全局 | 中 | 中（可观测性） |

### P2 — 优化（锦上添花）

| # | 升级项 | 影响模块 | 工作量 | 预期收益 |
|---|--------|---------|--------|---------|
| **P2-1** | 增加 MCP Server 端实现 | `internal/extension` | 大 | 高（生态扩展） |
| **P2-2** | 完善 ANP Profile 覆盖（P4/P9） | `internal/summon` | 中 | 中（协议完整性） |
| **P2-3** | A2A 增加连接池和心跳 | `internal/summon` | 中 | 中（稳定性） |
| **P2-4** | 浏览器会话复用（Cookie Jar） | `internal/browser` | 中 | 中（性能） |
| **P2-5** | ARD 暴露 Prometheus 指标 | `internal/ard` | 小 | 低（监控便利） |
| **P2-6** | 网关离线消息队列 | `internal/gateway` | 中 | 中（用户体验） |
| **P2-7** | 网关多租户隔离 | `internal/gateway` | 大 | 低（企业级） |
| **P2-8** | 完善 Browser 指纹注入 | `internal/browser/stealth` | 中 | 中（反检测升级） |
| **P2-9** | ARD 语义搜索与 CortexDB 集成 | `internal/ard/semantic.go` | 中 | 中（搜索质量） |
| **P2-10** | 统一错误分类规范 | 全局 | 中 | 低（开发体验） |

---

## 5. 实施路线图

### Phase 1: 安全与性能紧急修复（预计 1-2 周）

```
Week 1:
  ├── Day 1-2: HTTP/2 + Keep-Alive (P0-1)
  ├── Day 3-4: Server HTTPS + 认证 (P0-2)
  ├── Day 5:   Chromedp Sec-Fetch 修正 (P0-3)
Week 2:
  ├── Day 1-2: ARD 速率限制 (P0-4)
  ├── Day 3-4: HTTP Client 统一 (P0-5)
  └── Day 5:   TLS 指纹轮换 (P0-6) + 测试验证
```

### Phase 2: 架构完善（预计 3-4 周）

```
Week 3-4:
  ├── 统一后端抽象层设计 + 实现 (P1-1)
  ├── DNS 缓存 + 全局速率限制 (P1-2, P1-3)
Week 5-6:
  ├── 网关渠道扩展 (P1-4)
  ├── ARD 熔断 + 缓存 (P1-5)
Week 7-8:
  ├── MCP 审计 + 健康检查 (P1-6)
  ├── 行为模拟升级 (P1-7)
  └── 代理池联动 (P1-8)
```

### Phase 3: 生态扩展（预计 4-6 周）

```
Week 9-14:
  ├── MCP Server 端 (P2-1)
  ├── ANP Profile 完善 (P2-2)
  ├── A2A 连接池 (P2-3)
  ├── 会话复用 (P2-4)
  ├── 离线消息队列 (P2-6)
  └── 全链路 OTel 可观测性
```

---

## 附录：涉网模块代码统计

| 模块 | 文件数 | 代码行数 | 核心功能 |
|------|--------|---------|---------|
| `pkg/httpclient` | 1 | ~440 | HTTP 客户端封装 |
| `internal/browser` (含子包) | 16 | ~3500 | 浏览器引擎 + 反反爬 |
| `internal/summon` | 7 | ~1200 | Agent 间通信协议栈 |
| `internal/gateway` | 8 | ~800 | 消息网关 + Feishu |
| `internal/ard` | 12 | ~1500 | Agent 发现联邦网络 |
| `internal/extension` | 7 | ~600 | MCP 协议集成 |
| `internal/server` | 2 | ~350 | ACP/AG-UI 端点 |
| `internal/session` | 3 | ~250 | Redis 会话存储 |
| `internal/observability` | 1 | ~60 | Langfuse 追踪 |
| **合计** | **57** | **~8700** | |

---

*文档生成于 2026-07-24 | 基于代码快照分析 | 建议每季度重新扫描更新*