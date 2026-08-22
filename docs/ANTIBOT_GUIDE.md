# 浏览器引擎与反反爬技术指南

> 双后端: Rod (优先, 1927 行) + Chromedp (备用) | 反爬升级: 5 级递进
> 隐身欺骗: 15 类指纹伪造 | WAF 识别: 22 种签名 | UA 池: 16 种浏览器配置
> 探测维度: 5 维并行 | TLS 指纹: 5 种配置 | 行为模拟: 贝塞尔曲线鼠标

---

## 目录

1. [浏览器双后端架构](#1-浏览器双后端架构)
2. [Chrome 检测与容器适配](#2-chrome-检测与容器适配)
3. [API 发现与分页检测](#3-api-发现与分页检测)
4. [5 级反爬升级机制](#4-5-级反爬升级机制)
5. [阻塞检测器 (Detector)](#5-阻塞检测器-detector)
6. [自动升级器 (Escalator)](#6-自动升级器-escalator)
7. [TLS 指纹配置 (tls_profile)](#7-tls-指纹配置-tls_profile)
8. [多维并行探测器 (Prober)](#8-多维并行探测器-prober)
9. [WAF 签别库 (waf_probe)](#9-waf-签名库-waf_probe)
10. [隐身指纹伪造 (Stealth)](#10-隐身指纹伪造-stealth)
11. [行为模拟 (Behavior)](#11-行为模拟-behavior)
12. [智能代理池 (proxy_pool)](#12-智能代理池-proxy_pool)
13. [网络空闲检测 (settle)](#13-网络空闲检测-settle)
14. [资源下载 4 层回退](#14-资源下载-4-层回退)
15. [错误信号分类 (errsignal)](#15-错误信号分类-errsignal)
16. [配置参考](#16-配置参考)
17. [常见问题](#17-常见问题)

---

## 1. 浏览器双后端架构

### 1.1 BrowserBackend 接口

`internal/browser/` 定义了统一的浏览器后端接口（`types/types.go`），所有后端实现同一契约：

```go
type BrowserBackend interface {
    Render(url string) ([]byte, error)
    RenderWithReferer(url, referer string) ([]byte, error)
    SetSettle(d time.Duration)          // 网络空闲等待时间
    StealthEnabled() bool
    EnableStealth()                      // 启用隐身注入
    SetBehaviorSimulation(enabled bool)  // 行为模拟开关
    DownloadAsset(url, referer string) ([]byte, error)
    Screenshot(url string) ([]byte, error)
    Close() error
}
```

### 1.2 双后端架构

```
┌─────────────────────────────────────────────────┐
│              BrowserBackend 接口                 │
│            (types/types.go)                      │
└────────────────────┬────────────────────────────┘
                     │
         ┌───────────┴───────────┐
         ▼                       ▼
┌─────────────────┐    ┌─────────────────────┐
│  Rod 后端 (优先)  │    │  Chromedp 后端 (备用) │
│  rodbackend/     │    │  pool.go            │
│  pool.go (1927行)│    │  (默认回退)          │
└────────┬────────┘    └─────────────────────┘
         │
         ▼
   自动检测与切换:
   ┌─────────────────────────────────┐
   │ 1. 优先尝试 Rod 后端              │
   │ 2. Rod 初始化失败 → 自动回退      │
   │    到 Chromedp 后端               │
   │ 3. 运行时故障 → 切换后端重试       │
   └─────────────────────────────────┘
```

### 1.3 Rod 后端核心特性 (rodbackend/pool.go, 1927 行)

Rod 后端是**首选后端**，提供比 Chromedp 更丰富的功能：

| 特性 | 说明 |
|------|------|
| **Referer 页面缓存** | `refererPages map` 缓存已打开的来源页面，复用 Referer 上下文 |
| **5 层资源下载** | 比标准 4 层多一个 defense.gov 变体层 |
| **分块 JS 资源收集** | `Promise.allSettled` + `chunkSize=10`，防止大页超时 |

### 1.4 分块 JS 资源收集

```javascript
// Rod 后端注入的 JS 代码 (概念示意):
// 使用 Promise.allSettled 并发收集，分块避免超时

const chunkSize = 10;  // 每批最多 10 个并发
const results = [];

for (let i = 0; i < urls.length; i += chunkSize) {
    const chunk = urls.slice(i, i + chunkSize);
    const settled = await Promise.allSettled(
        chunk.map(url => fetch(url).then(r => r.text()))
    );
    results.push(...settled);
}
```

**设计原因**：大页面可能有数百个资源，一次性 `Promise.all` 会触发浏览器超时限制，分块处理确保稳定性。

---

## 2. Chrome 检测与容器适配

### 2.1 FindChromePath() 跨平台检测

`rodbackend/detect.go` 实现跨平台 Chrome 路径检测：

| 平台 | 检测路径 |
|------|---------|
| **Windows** | `C:\Program Files\Google\Chrome\...`<br>`C:\Program Files (x86)\Google\Chrome\...`<br>注册表查询 |
| **macOS** | `/Applications/Google Chrome.app/Contents/MacOS/Google Chrome`<br>`/Applications/Chromium.app/...` |
| **Linux** | `/usr/bin/google-chrome`<br>`/usr/bin/chromium`<br>`/usr/bin/chromium-browser`<br>`/snap/bin/chromium` |

### 2.2 容器化检测

`isContainerized()` 检测运行环境是否为容器：

```
检测方法:
  1. 检查 /.dockerenv 文件存在     → Docker 容器标志
  2. 检查 /run/.containerenv 文件  → Podman 容器标志
  3. 检查 /proc/1/cgroup 内容      → cgroup 中是否有容器标识
     (如 "docker"、"containerd"、"kubepods")
```

### 2.3 沙箱禁用决策

```go
func needNoSandbox() bool {
    // 以 root 用户运行 → 需要 --no-sandbox
    if os.Getuid() == 0 {
        return true
    }
    // 容器化环境 → 需要 --no-sandbox
    if isContainerized() {
        return true
    }
    return false
}
```

> **安全说明**：`--no-sandbox` 仅在 root 用户或容器环境中启用，桌面环境保持沙箱保护。

---

## 3. API 发现与分页检测

### 3.1 discoverAPIs()

`rodbackend/api_discovery.go` 通过拦截浏览器网络请求发现后端 API：

```
浏览器加载页面
    │
    ├── 监听所有 XHR/Fetch 请求
    │
    ├── 过滤条件:
    │   ├── 请求类型: XHR / Fetch
    │   └── 响应类型: JSON / XML / GraphQL
    │
    ├── 分析响应结构:
    │   ├── 包含数据数组? → 可能是列表 API
    │   ├── 包含分页字段? → 识别分页模式
    │   └── GraphQL? → 提取查询模式
    │
    └── 返回发现的 API 端点信息
```

### 3.2 detectPaginationKind() 分页模式识别

识别 API 响应中的 5 种分页模式：

| 模式 | 特征字段 | 示例 |
|------|---------|------|
| **query_param** | `page`, `p` 参数 | `?page=2` |
| **offset_limit** | `offset` + `limit` | `?offset=50&limit=25` |
| **cursor** | `cursor` / `next_cursor` | `?cursor=abc123` |
| **path_based** | URL 路径 `/page/N/` | `/posts/page/2/` |
| **none** | 无分页 | 单页 API |

### 3.3 GeneratePaginationURLs()

根据检测到的分页模式，生成后续页面的 URL：

```go
// 根据分页模式批量生成 URL
func GeneratePaginationURLs(baseURL string, kind PaginationKind, params map[string]string, count int) []string
```

---

## 4. 5 级反爬升级机制

### 4.1 升级层级总览

`internal/browser/antibot/` 实现了 5 级递进式反爬升级：

```
LevelNone (0)
    │  检测到阻塞
    ▼
LevelFlags (1)
    │  仍被阻塞
    ▼
LevelStealth (2)
    │  仍被阻塞
    ▼
LevelAggressive (3)
    │  超过最大重试
    ▼
LevelBackoff (4) ──→ 放弃当前 URL
```

### 4.2 Engine 组合模式

```go
type Engine struct {
    detector   *Detector    // 阻塞检测器
    escalator  *Escalator   // 自动升级器
}
```

Engine 将检测器和升级器组合为统一接口。

### 4.3 各等级详细对比

| 等级 | 常量 | 值 | Chrome 标志 | 隐身 JS | 延迟抖动 | UA 轮换 |
|------|------|-----|------------|---------|---------|---------|
| None | `LevelNone` | 0 | - | - | - | - |
| Flags | `LevelFlags` | 1 | ✅ | - | 0.5-2s | - |
| Stealth | `LevelStealth` | 2 | ✅ | ✅ | 1-4s | - |
| Aggressive | `LevelAggressive` | 3 | ✅ | ✅ | 2-8s | ✅ |
| Backoff | `LevelBackoff` | 4 | - | - | - | - |

### 4.4 FlagsForLevel 标志策略

`FlagsForLevel()` 只在 `LevelAggressive+` 返回额外的 Chrome 标志：

```go
func FlagsForLevel(level Level) []string {
    flags := baseFlags()  // 所有等级共有的基础标志
    if level >= LevelAggressive {
        flags = append(flags, aggressiveFlags()...)
        // 额外的反检测标志
    }
    return flags
}
```

**设计原因**：低等级时添加过多标志可能反而暴露自动化特征（"标志过多"本身就是指纹），只在激进模式才启用全套标志。

---

## 5. 阻塞检测器 (Detector)

### 5.1 双层检测机制

`detector.go` 使用 HTTP 状态码 + DOM 内容分析进行双层检测：

```
响应到达
    │
    ▼
第一层: HTTP 状态码 + 响应头 (快速)
    │
    ├── 403 → ReasonForbidden
    ├── 429 → ReasonRateLimited
    ├── 503 + cf-chl-* 头 → ReasonCloudflare
    └── 其他 → 需进一步检查
    │
    ▼
第二层: DOM 内容分析 (深度)
    │
    ├── 空响应? → ReasonEmpty
    ├── 维护页? (suspiciousPageMaxSize=50000 + 关键词) → ReasonUnavailable
    ├── Cloudflare Turnstile? → ReasonCloudflare
    ├── CAPTCHA 关键词? → ReasonCaptcha
    └── 其他拦截? → ReasonBlocked
    │
    ▼
    无阻塞 → ReasonNone
```

### 5.2 关键检测参数

| 参数 | 值 | 说明 |
|------|-----|------|
| `suspiciousPageMaxSize` | 50000 字节 | 超过此大小的页面通常不是拦截页，跳过 DOM 检测以提升性能 |

### 5.3 检测特征库

#### Cloudflare Turnstile 特征

```
challenges.cloudflare.com
challenges.cloudflare.com/turnstile
cf-turnstile
turnstile
__cf_chl
cf_chl_
```

#### CAPTCHA / 反爬关键词

```
captcha, verify you are human, are you a human, please verify,
security check, cf-challenge, cf-browser-verification,
checking your browser, ddos protection, access denied, blocked,
your request has been blocked, please enable javascript,
javascript is required, browser check, automated access,
suspicious activity, ...
```

#### 维护页关键词

```
technical difficulties, under maintenance, currently unavailable,
service unavailable, site is down, down for maintenance, we'll be back soon
```

### 5.4 错误模式检测

`CheckError()` 从网络错误中推断反爬：

| 错误模式 | 推断原因 | 说明 |
|---------|---------|------|
| timeout / deadline exceeded | `ReasonTimeout` | 可能的 bot wall |
| connection refused / reset | `ReasonUnavailable` | 可能的速率限制 |

---

## 6. 自动升级器 (Escalator)

### 6.1 升级流程

```
检测到阻塞 (reason)
    │
    ▼
ShouldRetry(reason)? ──否──→ 放弃
    │
    是
    ▼
当前 URL 重试次数 > MaxRetries? ──是──→ LevelBackoff, 放弃
    │
    否
    ▼
升级等级 +1 (不超过 MaxLevel)
    │
    ▼
计算重试延迟:
    基础延迟 × 原因系数 × 等级抖动
    │
    ▼
返回: shouldRetry=true, delay=X, newLevel=L
```

### 6.2 延迟抖动计算

不同等级的自适应延迟抖动：

| 等级 | 延迟计算 | 范围 |
|------|---------|------|
| `LevelFlags` | 基础 × 随机(0.5~2) | 0.5-2 秒 |
| `LevelStealth` | 基础 × 随机(1~4) | 1-4 秒 |
| `LevelAggressive` | 基础 × 随机(2~8) | 2-8 秒 |

### 6.3 延迟倍增器

特定阻塞原因触发延迟翻倍：

| 原因 | 倍增系数 | 原因 |
|------|---------|------|
| Cloudflare | × 2 | Cloudflare 追踪 IP，冷却需更慢 |
| RateLimited (429) | × 2 | 速率限制需更长冷却 |

### 6.4 UA 轮换

16 个真实浏览器配置的 UA 池：

| 浏览器 | 平台 | 版本示例 |
|--------|------|---------|
| Chrome | Windows / macOS / Linux | 128 / 129 / 130 |
| Firefox | Windows / macOS | 132 |
| Safari | macOS / iOS | 18 |
| Edge | Windows / macOS | 130 |
| Opera | Windows | 114 |
| Brave | Windows | 1.69 |

```go
// 轮换触发: 升级到 LevelAggressive 时自动轮换
func (e *Escalator) RotateUserAgent() *UAProfile
```

**一致性原则**：User-Agent 与 Sec-CH-UA、Sec-CH-UA-Mobile、Sec-CH-UA-Platform 头必须匹配，不一致的指纹会被反爬系统立即标记。

### 6.5 每 URL 重试追踪

```go
type Escalator struct {
    retries map[string]int  // 每 URL 独立的重试计数
}
```

不同 URL 的重试互不影响，单个 URL 不会耗尽全局重试预算。

---

## 7. TLS 指纹配置 (tls_profile)

### 7.1 五种 TLS 配置

`tls_profile.go` 定义 5 种 TLS ClientHello 指纹：

| 配置 | 说明 | 适用等级 |
|------|------|---------|
| `chrome_modern` | 现代 Chrome TLS 指纹 | LevelStealth (默认) |
| `chrome_conservative` | 保守 Chrome 指纹 | LevelFlags |
| `legacy_compatible` | 传统兼容指纹 | LevelNone |
| `aggressive` | 激进多变性指纹 | LevelAggressive |
| `privacy` | 隐私优化指纹 | 特殊场景 |

### 7.2 UTLS 集成

使用 `utls` 库模拟真实浏览器的 TLS ClientHello：

```go
// 使用 HelloChrome_Auto 指纹
tlsConfig := &tls.Config{...}
transport := &http.Transport{
    TLSClientConfig: tlsConfig,
    DialTLS: func(network, addr string) (net.Conn, error) {
        // 使用 utls 模拟 Chrome TLS 指纹
        uConn := utls.UClient(conn, cfg, utls.HelloChrome_Auto)
        return uConn, uConn.Handshake()
    },
}
```

> **为什么需要 TLS 指纹？** 反爬系统（如 Cloudflare）会检查 TLS ClientHello 包的指纹特征。Go 默认的 TLS 握手与 Chrome 显著不同，可被轻易识别为非浏览器客户端。

---

## 8. 多维并行探测器 (Prober)

### 8.1 五维并行探测

`antibot/prober/` 在克隆开始前对目标站点进行 5 维并行探测：

```
┌─────────────────────────────────────────┐
│           Prober (5 维并行)              │
│        并发限制: sem (容量 3)             │
├──────────┬──────────┬───────────────────┤
│          │          │                   │
▼          ▼          ▼          ▼        ▼
HTTP     Robots      WAF     JSChallenge  RateLimit
Header   检测       探测     检测         探测
探测                 (22 种)
│          │          │          │        │
└──────────┴────┬─────┴──────────┴────────┘
                │
                ▼
        analyzeResults()
                │
                ▼
        分类: None/Low/Medium/High/Critical
```

### 8.2 五个探测维度

| 维度 | 检测内容 | 方法 |
|------|---------|------|
| **HTTPHeader** | 反爬相关响应头 | 发送探测请求分析响应头 |
| **Robots** | robots.txt 限制规则 | 解析 Disallow/Crawl-delay |
| **WAF** | Web 应用防火墙 | 双探针对比 (见 §9) |
| **JSChallenge** | JavaScript 挑战 | 检测是否需要 JS 执行 |
| **RateLimit** | 速率限制策略 | 快速发送多个请求观察 429 |

### 8.3 并发控制

使用信号量控制并发度，避免探测本身触发封锁：

```go
sem := make(chan struct{}, 3)  // 最大 3 个并发探测
```

### 8.4 结果分析

`analyzeResults` 将 5 维探测结果综合分类：

| 等级 | 说明 | 建议行动 |
|------|------|---------|
| `LevelNone` | 无反爬措施 | 正常爬取 |
| `Low` | 轻度反爬 | LevelFlags 起步 |
| `Medium` | 中度反爬 | LevelStealth 起步 |
| `High` | 高度反爬 | LevelAggressive 起步 |
| `Critical` | 极端反爬 | 考虑跳过或使用代理 |

---

## 9. WAF 签名库 (waf_probe)

### 9.1 WAF 探测原理

`waf_probe.go` 使用**双探针对比**检测 WAF：

```
探针 1: 正常 User-Agent
    GET / HTTP/1.1
    User-Agent: Mozilla/5.0 (正常 Chrome)
    │
    └── 记录响应特征

探针 2: 挑战 User-Agent
    GET / HTTP/1.1
    User-Agent: curl/8.0.0  ← 已知的 bot UA
    │
    └── 记录响应特征

对比两个探针的响应:
    ├── 状态码不同? → 存在 WAF
    ├── 响应头不同? → 特定 WAF 签名匹配
    └── HTML 内容不同? → WAF 拦截页面
```

### 9.2 22 种 WAF 签名

| WAF | 检测特征 |
|-----|---------|
| **Cloudflare** | `cf-ray` 头、`__cf_bm` cookie、Cloudflare HTML 模式 |
| **Akamai** | `AkamaiGHost` server、`AKAMAI` 相关 cookie |
| **AWS WAF** | `AWS` 头、`x-amzn-ErrorType` |
| **Imperva** | `Incapsula` 头、`visid_incap` cookie |
| **DataDome** | `datadome` cookie、特定 JS 挑战 |
| **Sucuri** | `Sucuri` server 头 |
| **F5 BIG-IP** | `TS01` cookie、`BigIPServer` |
| **Citrix** | `NSC_` cookie 前缀 |
| ... | (共 22 种) |

### 9.3 每种签名的检测维度

每个 WAF 签名包含 4 个检测维度：

```go
type WAFSignature struct {
    Name           string
    Headers        map[string]string  // 特定响应头
    Cookies        []string           // 特定 cookie 名称
    HTMLPatterns   []string           // 拦截页 HTML 特征
    ServerPatterns []string           // Server 头匹配
}
```

### 9.4 置信度评分

```go
// 多维度匹配 → 更高置信度
confidence := 0
if matchHeaders(sig, resp)  { confidence += 0.3 }
if matchCookies(sig, resp)  { confidence += 0.3 }
if matchHTML(sig, body)     { confidence += 0.2 }
if matchServer(sig, resp)   { confidence += 0.2 }

// confidence > 0.5 → 确认 WAF 类型
```

---

## 10. 隐身指纹伪造 (Stealth)

### 10.1 15 类指纹伪造

`internal/browser/stealth/stealth.go` (490 行) 实现 15 类浏览器指纹伪造：

```
注入方式: Page.addScriptToEvaluateOnNewDocument
(在每个新文档加载前执行, 确保 DOM 构建前完成)

┌─────────────────────────────────────────────────────┐
│                  Stealth (15 类指纹)                 │
├──────────────┬──────────────────────────────────────┤
│ 1. webdriver │ navigator.webdriver → false          │
│    隐藏      │ (最基础的反检测)                       │
├──────────────┼──────────────────────────────────────┤
│ 2. chrome    │ window.chrome.runtime 模拟           │
│    .runtime  │ (真实 Chrome 有此对象)                │
├──────────────┼──────────────────────────────────────┤
│ 3. plugins   │ navigator.plugins 模拟真实插件列表     │
├──────────────┼──────────────────────────────────────┤
│ 4. languages │ navigator.languages 设置真实语言       │
├──────────────┼──────────────────────────────────────┤
│ 5. permissions│ Permissions API 查询返回正确结果      │
├──────────────┼──────────────────────────────────────┤
│ 6. hardware  │ navigator.hardwareConcurrency        │
│ Concurrency  │ 设置合理 CPU 核心数                    │
├──────────────┼──────────────────────────────────────┤
│ 7. device    │ navigator.deviceMemory               │
│    Memory    │ 设置合理内存大小                       │
├──────────────┼──────────────────────────────────────┤
│ 8. connection│ navigator.connection.rtt             │
│    RTT       │ 设置网络往返时间                       │
├──────────────┼──────────────────────────────────────┤
│ 9. screen    │ screen.width/height                  │
│    size      │ 设置常见屏幕分辨率                     │
├──────────────┼──────────────────────────────────────┤
│ 10. canvas   │ Canvas 指纹添加微小噪声                │
│     noise    │ (每次生成略有不同, 防指纹追踪)         │
├──────────────┼──────────────────────────────────────┤
│ 11. WebGL    │ WebGL vendor/renderer 伪装            │
│     vendor   │ (8 种 GPU 配置可选)                   │
├──────────────┼──────────────────────────────────────┤
│ 12. Audio    │ AudioContext 指纹随机化               │
│ Context      │ (修改采样结果)                        │
├──────────────┼──────────────────────────────────────┤
│ 13. Inter-   │ IntersectionObserver 模拟             │
│     section  │ (确保懒加载正常工作)                   │
│ Observer     │                                      │
├──────────────┼──────────────────────────────────────┤
│ 14. Battery  │ Battery API 模拟                     │
│     API      │ (提供合理的电池信息)                   │
├──────────────┼──────────────────────────────────────┤
│ 15. Timezone │ 时区一致性设置                         │
│              │ (与 IP 地理位置/语言匹配)              │
└──────────────┴──────────────────────────────────────┘
```

### 10.2 WebGL GPU 配置 (8 种)

```javascript
// 8 种常见 GPU 配置, 随机选择一种
const gpuConfigs = [
    {vendor: "Google Inc. (NVIDIA)", renderer: "ANGLE (NVIDIA GeForce RTX 3060)"},
    {vendor: "Google Inc. (NVIDIA)", renderer: "ANGLE (NVIDIA GeForce RTX 3070)"},
    {vendor: "Google Inc. (AMD)",    renderer: "ANGLE (AMD Radeon RX 6700 XT)"},
    {vendor: "Google Inc. (Intel)",  renderer: "ANGLE (Intel Iris Xe Graphics)"},
    {vendor: "Google Inc. (Intel)",  renderer: "ANGLE (Intel UHD Graphics 630)"},
    {vendor: "Google Inc. (Apple)",  renderer: "ANGLE (Apple M1)"},
    {vendor: "Google Inc. (Apple)",  renderer: "ANGLE (Apple M2)"},
    {vendor: "Google Inc. (Apple)",  renderer: "ANGLE (Apple M3)"},
];
```

### 10.3 Canvas 噪声原理

```javascript
// Canvas 指纹噪声: 对 toDataURL/toBlob 输出添加微小扰动
const originalToDataURL = HTMLCanvasElement.prototype.toDataURL;
HTMLCanvasElement.prototype.toDataURL = function(...args) {
    const ctx = this.getContext('2d');
    if (ctx) {
        // 获取原始像素数据
        const imageData = ctx.getImageData(0, 0, this.width, this.height);
        // 对少量像素添加 ±1 的微小变化
        for (let i = 0; i < imageData.data.length; i += 4) {
            // 随机选择的像素, 微小扰动
        }
        ctx.putImageData(imageData, 0, 0);
    }
    return originalToDataURL.apply(this, args);
};
```

---

## 11. 行为模拟 (Behavior)

### 11.1 贝塞尔曲线鼠标移动

`internal/browser/behavior/behavior.go` 使用三次贝塞尔曲线模拟人类鼠标移动：

```
起点 P0 ────────────控制点 P1
    ╲          ╱
     ╲        ╱     ← 三次贝塞尔曲线
      ╲      ╱        B(t) = (1-t)³P0 + 3(1-t)²tP1
       ╲    ╱                        + 3(1-t)t²P2 + t³P3
        ╲  ╱
         ╲╱
控制点 P2 ──── 终点 P3

特性:
  · 曲线自然, 非直线移动
  · 控制点随机偏移, 每次路径不同
  · 速度变化: 慢→快→慢 (符合人类习惯)
```

### 11.2 行为模拟组成

| 行为 | 实现 | 说明 |
|------|------|------|
| **鼠标移动** | 三次贝塞尔曲线 | 控制点随机，路径自然 |
| **打字延迟** | 每次按键随机延迟 | 模拟人类打字速度 |
| **随机停顿** | 页面浏览时随机暂停 | 模拟阅读/查看行为 |
| **页面探索** | 滚动和悬停 | 模拟浏览探索 |

---

## 12. 智能代理池 (proxy_pool)

### 12.1 SmartProxyPool

`proxy_pool.go` 实现智能代理池管理：

```
SmartProxyPool
    ├── TCP 拨号健康检查     // 使用前验证代理可用性
    ├── Cloudflare cf_clearance  // 令牌缓存与复用
    │   令牌缓存
    └── UTLS HTTP 客户端    // HelloChrome_Auto 指纹
```

### 12.2 TCP 拨号健康检查

```go
// 使用代理前先进行 TCP 拨号测试
func (p *SmartProxyPool) isHealthy(proxyURL string) bool {
    conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
    if err != nil {
        return false  // 代理不可达
    }
    conn.Close()
    return true
}
```

### 12.3 cf_clearance 令牌缓存

```
Cloudflare cf_clearance 令牌生命周期:

1. 浏览器通过 Cloudflare 挑战
   → 获得 cf_clearance cookie

2. SmartProxyPool 缓存 cf_clearance
   → 后续 HTTP 请求注入此令牌
   → 无需每次都通过浏览器挑战

3. 令牌过期/失效
   → 重新通过浏览器获取新令牌
```

### 12.4 UTLS HelloChrome_Auto

代理池的 HTTP 客户端使用 UTLS 模拟 Chrome TLS 指纹，确保代理请求与浏览器请求的 TLS 特征一致：

```go
transport := &http.Transport{
    DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
        // 使用 HelloChrome_Auto TLS 指纹
        uConn := utls.UClient(rawConn, cfg, utls.HelloChrome_Auto)
        return uConn, uConn.HandshakeContext(ctx)
    },
}
```

---

## 13. 网络空闲检测 (settle)

### 13.1 Settle 机制

`settle.go` 监控 CDP (Chrome DevTools Protocol) 事件判断页面是否"加载完成"：

```
4 个 CDP 事件监控:
  ┌─────────────────────────────────────┐
  │ 1. Network.loadingFinished          │ ← 请求完成
  │ 2. Network.dataReceived             │ ← 接收到数据
  │ 3. Network.requestWillBeSent        │ ← 新请求发出
  │ 4. Network.responseReceived         │ ← 收到响应
  └─────────────────────────────────────┘

判断逻辑:
  · 收到任何事件 → 重置 "安静计时器"
  · 200ms 心跳检查
  · 连续 quiet 时间无新事件 → 认为页面空闲
  · 总超时 = quiet + 10s
```

### 13.2 Settle 时序

```
时间轴 →

请求发出    数据到达    完成      新请求     完成
  │           │         │         │          │
  ▼           ▼         ▼         ▼          ▼
  ──────────────────────────────────────────────→
  requestWillBeSent → 重置    requestWillBeSent → 重置

                                                    │
                                                    ▼
                                            quiet 持续无事件
                                                    │
                                                    ▼
                                            页面空闲!
                                            (或超时 quiet+10s)
```

### 13.3 参数说明

| 参数 | 说明 |
|------|------|
| `quiet` | 连续无网络事件的时间阈值（默认 2000ms） |
| `heartbeat` | 检查间隔（200ms） |
| `timeout` | 最大等待时间 = quiet + 10s |

---

## 14. 资源下载 4 层回退

### 14.1 四层回退架构

每层使用**全新的浏览器标签上下文**，确保隔离性：

```
资源 URL
    │
    ▼
Layer 1: 浏览器导航 + Network.getResponseBody
    │   导航到资源 URL，获取响应体
    │   失败 ↓
    │
Layer 2: <img> 标签加载
    │   在页面中创建 <img src="URL">
    │   等待加载完成后从 canvas 读取
    │   失败 ↓
    │
Layer 3: Network.loadNetworkResource (CDP)
    │   通过 CDP 直接加载网络资源
    │   失败 ↓
    │
Layer 4: JS fetch + base64 编码
    │   在浏览器内执行 fetch(url) → base64
    │   失败 ↓
    │
  彻底失败
```

### 14.2 Rod 后端的第 5 层

Rod 后端在标准 4 层基础上增加 **defense.gov 变体层**：

```
Layer 5 (Rod 专有): defense.gov 变体
    │   针对 media.defense.gov 的特殊处理
    │   先打开来源页面建立 Referer 上下文
    │   再从该页面上下文中加载图片
    │   最后提取图片数据
    ▼
```

这解决了国防部媒体服务器严格校验 Referer 和会话的问题。

---

## 15. 错误信号分类 (errsignal)

### 15.1 七类错误分类

`internal/errsignal/` 将所有网络/浏览器错误分为 7 类：

| 错误类 | 说明 | 处理策略 |
|--------|------|---------|
| **BotDetection** | Cloudflare/Turnstile/CAPTCHA 触发 | 不重试，升级隐身等级 |
| **RateLimited** | 429 / 503 限流 | 遵守 Retry-After，最多 3 次重试 |
| **Forbidden** | 403 禁止访问 | 升级隐身，有限重试 |
| **NetworkError** | 连接失败/超时/DNS | 临时错误，自动重试 |
| **ServerError** | 5xx 服务器错误 | 临时错误，自动重试 |
| **ClientError** | 4xx 客户端错误 (非 403/429) | 不重试 |
| **Unknown** | 未分类错误 | 默认不重试 |

### 15.2 BotDetection 处理

```
检测到 Cloudflare / Turnstile / CAPTCHA
    │
    ├── 不重试当前请求 (重试无用, challenge 不会自己消失)
    │
    ├── 升级隐身等级:
    │   LevelNone → LevelFlags → LevelStealth
    │
    └── 使用更高等级重新请求
```

### 15.3 RateLimited 处理

```
HTTP 429 / 503 (速率限制)
    │
    ├── 检查 Retry-After 头
    │   ├── 有 → 等待指定时间
    │   └── 无 → 使用默认退避
    │
    ├── 最多 3 次重试
    │
    └── 每次重试延迟翻倍
```

---

## 16. 配置参考

### 16.1 反爬配置项

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `antibot.enabled` | bool | `true` | 启用反反爬检测 |
| `antibot.auto_escalate` | bool | `true` | 自动升级隐身等级 |
| `antibot.initial_level` | string | `none` | 初始等级: none/flags/stealth/aggressive |
| `antibot.max_level` | string | `aggressive` | 最高等级 |
| `antibot.max_retries` | int | `3` | 单 URL 最大重试次数 |
| `antibot.cooldown` | duration | `30s` | 重试基础冷却时间 |
| `browser.backend` | string | `rod` | 浏览器后端: rod/chromedp |
| `browser.stealth` | bool | `false` | 启用隐身注入 |
| `browser.behavior` | bool | `false` | 启用行为模拟 |
| `browser.settle` | duration | `2s` | 网络空闲阈值 |

### 16.2 CLI 选项

```bash
wukong apps clone <url> [flags]

反爬相关:
  --antibot              启用反反爬系统
  --no-antibot-auto      禁用自动升级（手动控制等级）
  --stealth              以 stealth 模式启动 (level 2)
  --aggressive           以 aggressive 模式启动 (level 3)
  --user-agent string    自定义 User-Agent
  --cookie-file string   导入浏览器 Cookie 文件
  --proxy string         代理地址
  --behavior             启用行为模拟
```

### 16.3 推荐配置

#### 温和模式（默认）

```yaml
antibot:
  enabled: true
  auto_escalate: true
  initial_level: none
  max_level: aggressive
  max_retries: 3
  cooldown: 30s
```

#### 激进模式（难爬站点）

```yaml
antibot:
  enabled: true
  auto_escalate: true
  initial_level: stealth
  max_level: aggressive
  max_retries: 5
  cooldown: 60s
browser:
  backend: rod
  stealth: true
  behavior: true
```

---

## 17. 常见问题

### Q1: Rod 和 Chromedp 有什么区别？

| 特性 | Rod | Chromedp |
|------|-----|---------|
| **优先级** | 首选 | 备用回退 |
| **Referer 缓存** | ✅ refererPages map | ❌ |
| **资源下载层** | 5 层 (含 defense.gov 变体) | 4 层 |
| **JS 资源收集** | ✅ 分块 Promise.allSettled | ❌ |
| **代码量** | 1927 行 (pool.go) | 较少 |

Rod 后端功能更丰富，但 Chromedp 作为可靠备用保证了系统健壮性。

### Q2: 为什么不用最高等级开始？

1. **性能代价**：LevelAggressive 每次请求增加 2-8 秒延迟
2. **过度伪装风险**：有些站点检测"过于完美"的指纹
3. **FlagsForLevel 设计**：低等级时额外标志为零，避免"标志过多"的指纹特征
4. **按需升级**：检测到阻塞才升级，平衡速度和成功率

### Q3: TLS 指纹为什么重要？

现代反爬系统（如 Cloudflare）在 TLS 握手阶段就能识别非浏览器客户端：

```
Go 默认 TLS ClientHello:
  · Cipher suites 顺序与 Chrome 不同
  · Extensions 列表与 Chrome 不同
  · Supported groups/curves 不同
  → 反爬系统立即识别为 "非浏览器"

UTLS HelloChrome_Auto:
  · 完全复制 Chrome 的 ClientHello
  · Cipher suites、extensions、curves 全部一致
  → 反爬系统认为 "这是 Chrome"
```

### Q4: cf_clearance 缓存如何工作？

```
1. 浏览器通过 Cloudflare 挑战 → 获得 cf_clearance cookie
2. SmartProxyPool 缓存此令牌
3. 后续 HTTP 直连请求注入 cf_clearance → 无需浏览器
4. 直到令牌过期 → 重新通过浏览器获取
```

### Q5: 能绕过所有反爬吗？

**不能**。以下场景无法绕过：
- 强 CAPTCHA（reCAPTCHA v3 高分数阈值、hCaptcha 严格模式）
- IP 黑名单
- 必须登录且有人工审核的站点
- 硬件指纹绑定（如 Trusted Platform Module）

**能绕过的场景**：
- 简单的 webdriver 检测
- 基础 TLS/指纹检测
- Referer / UA 校验
- 轻度速率限制
- Cloudflare 中等难度挑战

### Q6: Settle 等待时间设多少合适？

| 站点类型 | 建议 quiet | 说明 |
|---------|-----------|------|
| 静态站 | 1000ms | 快速完成 |
| SPA 站 | 3000ms | 需要更多 JS 执行时间 |
| 重 JS 站 | 5000ms | 复杂前端框架 |
| 懒加载站 | 5000ms | 等待懒加载触发 |

总超时 = quiet + 10s，确保不会无限等待。

---

## 附录

### 相关文档

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | 系统架构详解 |
| [CLONE_GUIDE.md](./CLONE_GUIDE.md) | 网站克隆引擎与 ZIM 打包技术指南 |
| [CONFIG.md](./CONFIG.md) | 配置参考手册 |
| [README.md](../README.md) | 项目主页 |

### 源码索引

| 模块 | 路径 |
|------|------|
| 浏览器接口 | `internal/browser/types/types.go` |
| Chromedp 后端 | `internal/browser/pool.go` |
| Rod 后端 | `internal/browser/rodbackend/pool.go` |
| Chrome 检测 | `internal/browser/rodbackend/detect.go` |
| API 发现 | `internal/browser/rodbackend/api_discovery.go` |
| 反爬引擎 | `internal/browser/antibot/antibot.go` |
| 阻塞检测器 | `internal/browser/antibot/detector.go` |
| 自动升级器 | `internal/browser/antibot/escalator.go` |
| TLS 指纹 | `internal/browser/antibot/tls_profile.go` |
| 多维探测器 | `internal/browser/antibot/prober/` |
| WAF 探测 | `internal/browser/antibot/prober/waf_probe.go` |
| 隐身注入 | `internal/browser/stealth/stealth.go` |
| 行为模拟 | `internal/browser/behavior/behavior.go` |
| 代理池 | `internal/browser/proxy_pool.go` |
| 网络空闲 | `internal/browser/settle.go` |
| 错误分类 | `internal/errsignal/` |

---

> **版本**: v2.0 | **最后更新**: 2026-08-11 | **相关代码**: internal/browser/ + internal/errsignal/
