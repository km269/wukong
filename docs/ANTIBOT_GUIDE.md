# 浏览器引擎与反反爬技术指南

> 双后端: Rod (优先) + Chromedp (备用) | 反爬升级: 5 级递进
> 隐身欺骗: 会话级稳定指纹 (16 类) | WAF 识别: 22 种签名 | UA 池: 浏览器上下文 Chrome-only
> 探测维度: 5 维并行 | TLS 指纹: 3 种真实配置 + WebRTC 防泄漏 | 行为模拟: 贝塞尔曲线鼠标 (trusted CDP 输入)

---

## 目录

0. [2026 检测三层战场与本系统对策](#0-2026-检测三层战场与本系统对策)
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

## 0. 2026 检测三层战场与本系统对策

现代反爬（Cloudflare/Akamai/DataDome 级）已不再依赖单一 JS 探针，而是在**三个互相独立的层面**同时采集证据。任何一层穿帮即整场失败：

### 0.1 第一层：CDP 协议指纹（"驾驶风格"）

**检测原理**：自动化驱动（Playwright/Puppeteer/rod）通过 CDP 控制浏览器，启动时会发送 `Runtime.enable`、`Target.setAutoAttach`、`Page.addScriptToEvaluateOnNewDocument` 等命令。这些命令的**时序、参数组合、发送节奏**构成"驾驶风格"指纹——服务端在 JS 挑战中采集 CDP 注入痕迹（如 `Runtime.enable` 引起的 console binding、`debugger` 语义变化），无需检查任何 `navigator` 属性即可判定自动化。

**本系统对策**：

| 对策 | 实现 |
|------|------|
| 最小 CDP 命令面 | rod 后端仅启用必要 domain（Network 资产追踪、Page 渲染），不发送 Playwright 式的全套 auto-attach |
| `AutomationControlled` blink 特性禁用 | `--disable-blink-features=AutomationControlled`（双后端统一，rod 侧 0.3.2 补齐），C++ 层抹除 webdriver 痕迹 |
| 注入脚本原生码掩码 | 所有补丁函数经 `_mask()` + WeakMap 包装，`Function.prototype.toString` 仍返回 `[native code]`，检测方无法从源码发现补丁 |
| 真实 Chrome 二进制 | `FindChromePath()` 优先系统 Chrome（非 Chromium），配合完整 UA/client-hints 身份 |

**残余风险**：CDP 协议层指纹无法被 JS 补丁修复，只能靠"少发命令 + 系统二进制"降低显著性。基准测试中 nodriver（绕过 CDP 直改源码）28/0 硬封优于 Playwright 补丁系（24/5），印证此层是当前天花板。

### 0.2 第二层：TLS / JA3 指纹

**检测原理**：Chromium 与系统 Chrome 的 TLS ClientHello（密码套件顺序、GREASE 值、扩展列表）与 HTTP/2 SETTINGS 帧存在微妙差异；Go 标准库 TLS 与 Chrome 差异更大。Cloudflare 在 **TCP 握手阶段**就完成首次分类——请求还没到 HTTP 层，JS 层伪装再完美也已出局。

**本系统对策**：

| 对策 | 实现 |
|------|------|
| HTTP 客户端 UTLS | `proxy_pool`/HTTP 抓取路径用 `utls.HelloChrome_Auto` 复制 Chrome ClientHello（见 §7/§12） |
| 系统 Chrome 渲染路径 | 浏览器上下文的 TLS 由 Chrome 二进制本身发出，天然一致 |
| 诚实边界 | launcher flag **无法**修复 Chromium↔Chrome 的 ClientHello 微差——`tls_profile.go` 的 3 个 profile 只做 TLS 版本/特性调节，0.3.2 已删除两个含伪造 feature 名（`AsyncTLS`/`EncryptedClientHello`）的假 profile。要 JA3 完全一致，用系统 Chrome（`browser.path` / `ChromeBin`） |

### 0.3 第三层：行为统计建模

**检测原理**：不再检测"鼠标是否移动"，而是对贝塞尔曲线特征、键盘停顿分布、滚动加速度模式做**统计级一致性检查**：完美直线是机器、每次完全相同的曲线也是机器、`isTrusted=false` 的事件更是机器。合成 `dispatchEvent` 一秒钟穿帮。

**本系统对策**：

| 对策 | 实现 |
|------|------|
| trusted 输入事件 | `simulateHumanBehavior()` 经 CDP `Input.dispatchMouseEvent` 发送鼠标/滚轮事件，页面侧 `isTrusted=true`（见 §11） |
| 贝塞尔 + 微颤 | `behavior` 包三次贝塞尔路径 + 逐点速度缓动 + 随机微停顿，路径与节奏均不重复 |
| 滚动加速度模型 | 加速→匀速→减速三角延迟 profile，滚轮以 80-120px/格的真实格距发送 |
| 阅读式停顿 | 页面加载后 150-500ms 随机悬停，模拟人类扫读 |

### 0.4 贯穿三层的横切原则：一致性

三层之上还有一条铁律——**任何自相矛盾的信号组合都比单一弱信号更致命**：

| 一致性维度 | 0.3.2 实现 |
|-----------|-----------|
| UA ↔ Client Hints ↔ Platform | `NetworkSetUserAgentOverride` 携带完整 `UserAgentMetadata`；脚本内 `navigator.platform`/`userAgentData` 与 UA 同步轮换（见 §6.4/§10） |
| 时区 ↔ 语言 ↔ Geo ↔ **代理出口** | `Fingerprint.Geo` persona 按 `browser.geo_region` 配置或代理解析选定（0.3.2 后续优化），`Intl`/`Date`/`Accept-Language` 全部跟随（DST 由 `Intl.DateTimeFormat` 实时推算）。选择优先级：**显式配置 > 代理 URL 推断 > 随机**——`stealth.GeoCodeFromProxyURL` 解析住宅代理凭据中的地区参数（`user-country-jp`/`cc=kr`/`region-de` 等）与网关主机 TLD（`.jp`/`.uk→gb`）；无法判断时宁可随机也不乱猜 |
| 指纹跨页稳定 | 会话级种子（Canvas/Audio/Font），不再每文档重掷 |
| UA 家族 ↔ 引擎 | 浏览器上下文 Chrome-only 池；Firefox/Safari persona 只给 HTTP 客户端 |
| 代理 IP ↔ 环境坐标 | 代理启用时叠加 WebRTC 防泄漏 flags，防 UDP 绕过代理暴露真实 IP |

### 0.5 残余风险攻坚（0.3.2 P0 已落地）

两个"JS/flag 不可修复"项的原子级拆解与攻坚状态：

| 子向量 | 可修性 | 状态 |
|--------|--------|------|
| localhost DevTools 端口探测（CDP 指纹中唯一页面 JS 可直接探测的向量） | 需 `--remote-debugging-pipe`，rod v0.116 **不支持** pipe 传输（已核实：transport 仅 websocket） | **接受残余**：随机高端口 + 仅监听 127.0.0.1 + Chrome 111+ Origin 校验，检测方须全端口扫描，成本高误报高，属概率型低风险。跟踪 rod 上游 pipe 支持 |
| CDP 命令时序"驾驶风格" | 命令面收敛 + 时机抖动 | **已落地**：rod 惰性 enable 架构使本系统命令面天然小于 Playwright（`WaitLoad` 走 `Runtime.evaluate` 而非 `Page.enable`；从不发送 `Runtime.enable`/`Target.setAutoAttach`）；renderJob 在 setup→navigate 之间加 80-250ms 随机抖动打散固定节奏 |
| UA↔二进制版本矛盾（版本可证伪） | 版本对齐引擎 | **已落地**：`New()` 经 `Browser.getVersion` 读取真实二进制版本（如 132.0.6834.83），`alignUAWithBinary` 把 persona 的 UA 串改写为 `Chrome/132.0.0.0`（真实 Chrome 冻结次版本号格式）+ `FullVersion` 采用完整真版本——伪装版本号不如采用真版本号，版本关联的 TLS/JS 行为无法说谎 |
| Chromium↔Chrome JA3 微差 + 编解码器差异 | 仅二进制替换可修 | **双重落地**：①启动告警——`isChromiumBinary` 检测发行版 Chromium（Alpine/Debian/snap 包）即 warn"JA3/codec 降级模式"并指引 `browser.path`/`CHROME_PATH`；②容器基座——Dockerfile 默认 Debian + google-chrome-stable 官方源（`CHROME_FLAVOR=slim` 保留发行版 Chromium 变体）。运行时自检（`runStealthSelfCheck`）用 `canPlayType('video/mp4; codecs="avc1"')` 复核——官方 Chrome 返回 `probably`、剥离编解码器的 Chromium 返回空串 |
| 发行版 Chromium 上的 Edge persona | persona 过滤 | **已落地**：`pickBrowserUA` 在 `binaryIsChromium` 时剔除 `Edg/` persona（该构建无法支撑 Edge 专有行为，声称即穿帮）；官方 Chrome 二进制上 Edge persona 照常可用 |
| CDP 命令面不可见 | 审计模式 | **已落地**：`WUKONG_CDP_AUDIT=1` 时 `auditClient`（[cdp_audit.go](../internal/browser/rodbackend/cdp_audit.go)）经 rod `Client()` 注入点记录每条命令的方法名/时长（debug 级，不记 payload）——命令序列即驾驶风格审计所需，先可见再收敛 |
| nodriver 级 CDP 绕过（28/0 硬封基准） | 自研 rod transport 替代品 | **明确不做**：工程量等于重写并永久维护协议层，收益仅在最严站点从 ~25→28；硬封站点走既有逃生通道（`--no-headless` 人工过 Turnstile + 住宅代理池） |

另：`no-pings`/`disable-component-update`/`disable-background-networking` 使浏览器零后台流量（真实 Chrome 会发 Safe Browsing ping 等）——"过分安静"是已知弱信号，属性能/可控性与仿真度的有意权衡，文档明示不改。

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
│  pool.go         │    │  (默认回退)          │
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

### 1.3 Rod 后端核心特性 (rodbackend/pool.go)

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
    Escalator *Escalator  // 自动升级器
}
```

Engine 持有升级器；阻塞检测以包级函数 `Detect()`（HTTP 层 + DOM 层）提供，`CheckResponse()`/`CheckError()` 作为统一入口封装。

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
    ├── 维护页? (maintenancePageMaxSize=15000 + 关键词) → ReasonUnavailable
    ├── 短阻断页? (<150 字节且含 forbidden/denied) → ReasonBlocked
    ├── Cloudflare Turnstile? → ReasonCloudflare
    ├── CAPTCHA 关键词? (≤ suspiciousPageMaxSize=50000 才检查) → ReasonCaptcha
    └── 无阻塞 → ReasonNone
    │
    ▼
    无阻塞 → ReasonNone
```

### 5.2 关键检测参数

| 参数 | 值 | 说明 |
|------|-----|------|
| `maintenancePageMaxSize` | 15000 字节 | 超过此大小的页面不视为维护页 |
| `suspiciousPageMaxSize` | 50000 字节 | 超过此大小的页面跳过 CAPTCHA 关键词检测（大页面几乎不可能是拦截页），以提升性能 |

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

两套池分工（0.3.2 起浏览器上下文严格 Chrome-only）：

| 池 | 内容 | 使用方 |
|----|------|--------|
| `uaProfiles` (15 个) | Chrome/Firefox/Safari/Edge/Opera/Brave × 多平台 | HTTP 客户端（无 JS 引擎，无 TLS-引擎耦合约束） |
| `chromeUAProfiles` (5 个) | Win Chrome 130/129、Mac Chrome 130、Linux Chrome 130、Win Edge 130——均带 `FullVersion`/`PlatformVersion` | 浏览器上下文（rod + chromedp） |

```go
// 浏览器上下文轮换: RotateChromeUA() 只从 Chrome-only 池取
// (Gecko/WebKit UA 配 Chromium TLS/JS 引擎是即时矛盾, 见 §0.4)
func (e *Escalator) RotateChromeUA() *UAProfile

// 全量池轮换: HTTP 客户端专用
func (e *Escalator) RotateUserAgent() *UAProfile

// 桌面池随机: GetRandomDesktopUA()
```

**三方一致性原则**：UA 字符串 ↔ `Sec-CH-UA`/`Sec-CH-UA-Mobile`/`Sec-CH-UA-Platform` 头 ↔ `navigator.userAgentData`/`navigator.platform` 必须同时轮换。rod 后端经 `NetworkSetUserAgentOverride` 携带完整 `UserAgentMetadata`（Brands/FullVersionList/Platform/PlatformVersion/Architecture/Bitness），CDP 层与注入脚本层共享同一 `UAIdentity`，任何一处不一致都是高置信度 bot 信号。

### 6.5 每 URL 重试追踪

```go
type Escalator struct {
    retries map[string]int  // 每 URL 独立的重试计数
}
```

不同 URL 的重试互不影响，单个 URL 不会耗尽全局重试预算。

---

## 7. TLS 指纹配置 (tls_profile)

### 7.1 三种真实 TLS 配置

`tls_profile.go` 定义 3 种 TLS 版本/特性调节 profile（0.3.2 起口径）：

| 配置 | 说明 | 适用等级 |
|------|------|---------|
| `chrome_modern` | 现代 Chrome TLS 特性（TLS1.3 优先） | LevelStealth (默认) |
| `chrome_conservative` | 保守兼容指纹 | LevelFlags |
| `legacy_compatible` | 传统兼容指纹 | LevelNone |

> **0.3.2 清理**：删除了 `chrome_aggressive`/`chrome_privacy` 两个 profile——其 `AsyncTLS`/`EncryptedClientHello` 并非真实 Chrome feature 名，无效 flags 只增加命令行指纹噪音。

### 7.2 WebRTC 防泄漏 Flags

`WebRTCProtectionFlags()` 在代理启用时叠加到 launcher：

```
--force-webrtc-ip-handling-policy=disable_non_proxied_udp
--webrtc-ip-handling-policy=disable_non_proxied_udp
```

防止 WebRTC 经原始 UDP 直连绕过 HTTP 代理，把真实公网 IP 暴露给 STUN 探测（见 §0.4 一致性原则）。

### 7.3 诚实边界：launcher flag 无法修复 JA3

Chromium 二进制与系统 Chrome 的 TLS ClientHello（密码套件顺序、GREASE、HTTP/2 SETTINGS 帧）存在**编译层差异**，任何 `--flag` 都无法抹平。基准数据（Cloudflare 严管站点 30 URL）：

| 驱动 | 通过 | 硬封 |
|------|------|------|
| nodriver（CDP 绕过 + 源码级补丁） | 28 | 0 |
| CloakBrowser（humanize+geoip） | 26 | - |
| Patchright + `channel="chrome"`（系统 Chrome） | 25 | - |
| Playwright 原生 | 24 | 5 |

结论：**系统 Chrome 二进制 > 一切 JS 补丁**。本系统 `FindChromePath()` 默认优先系统安装的 Chrome（`browser.path` 可显式指定），这是 JA3 一致性的第一道保障；UTLS 只用于纯 HTTP 客户端路径（见 §12.4）。

### 7.4 UTLS 集成（HTTP 客户端路径）

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

### 10.1 架构：会话级稳定指纹 + 模板渲染

0.3.2 重构后，stealth 不再是静态脚本常量，而是**两级结构**：

```
Go 侧 (fingerprint.go)                      JS 侧 (stealth.go 模板)
─────────────────────                       ──────────────────────
GenerateFingerprint(rng, geo)               BuildScript(fp, uaIdentity)
  │  会话开始时生成一次                        │  strings.NewReplacer 烘入
  │  · Geo persona (10 地区)                  │  __CANVAS_SEED__ __TZ__
  │  · 屏幕/任务栏保留高度                     │  __LANGS__ __SCREEN_W__
  │  · GPU (6 个 ANGLE 真实格式)              │  __GPU_VENDOR__ __GPU_RENDERER__
  │  · 核数/内存/RTT/电池                      │  __UA_SECTION__ (可选身份段)
  │  · Canvas/Audio/Font 种子                  ▼
  ▼                                        Page.addScriptToEvaluateOnNewDocument
Pool.stealthFP (会话持有)                    (每个新文档加载前执行)
  │  UA 轮换只重建身份段,
  │  硬件指纹永不重掷
```

**为什么指纹必须会话级稳定**：GPU 在两次导航之间"变化"的浏览器不存在。旧实现每个新文档重掷随机值，跨页对比即暴露"指纹跳变"——这本身就是高置信度自动化信号。噪声用 mulberry32 种子 PRNG：同一会话内确定可复现，噪声不再制造新的不一致。

### 10.2 16 类指纹伪造清单

`internal/browser/stealth/`（`fingerprint.go` + `stealth.go`）实现 16 类伪装：

```
注入方式: Page.addScriptToEvaluateOnNewDocument
(在每个新文档加载前执行, 确保 DOM 构建前完成)

┌──────────────────────┬───────────────────────────────────────┐
│ 0. 原生码掩码         │ _mask(fn,name) + WeakMap: 所有补丁函数 │
│    (§0 新增)         │ Function.prototype.toString 仍返回      │
│                      │ "[native code]", 源码级检测失效         │
├──────────────────────┼───────────────────────────────────────┤
│ 1. webdriver 隐藏    │ navigator.webdriver → undefined        │
├──────────────────────┼───────────────────────────────────────┤
│ 2. chrome.runtime    │ window.chrome.runtime 模拟             │
├──────────────────────┼───────────────────────────────────────┤
│ 3. plugins           │ navigator.plugins 模拟真实插件列表     │
│                      │ (PluginSet 由指纹种子选定, 会话稳定)     │
├──────────────────────┼───────────────────────────────────────┤
│ 4. languages         │ navigator.languages ← Geo persona      │
│                      │ (如 jp 地区 → ["ja-JP","ja","en-US"])  │
├──────────────────────┼───────────────────────────────────────┤
│ 5. permissions       │ Permissions API 查询返回正确结果        │
├──────────────────────┼───────────────────────────────────────┤
│ 6. hardware          │ navigator.hardwareConcurrency          │
│    Concurrency       │ ← 指纹核数池 (4/6/8/12/16)             │
├──────────────────────┼───────────────────────────────────────┤
│ 7. deviceMemory      │ navigator.deviceMemory ← 指纹内存池     │
├──────────────────────┼───────────────────────────────────────┤
│ 8. connection RTT    │ navigator.connection.rtt ← 指纹网络池   │
├──────────────────────┼───────────────────────────────────────┤
│ 9. screen size +     │ screen.* ← 指纹屏幕池 +                │
│    outerWidth/Height │ window.outerWidth/outerHeight          │
│    (§9 扩展)         │ (headless 下报 0 的经典泄漏点)          │
├──────────────────────┼───────────────────────────────────────┤
│ 10. canvas noise     │ toDataURL/getImageData 按种子确定性    │
│                      │ 扰动 (同会话同画布同噪声)               │
├──────────────────────┼───────────────────────────────────────┤
│ 11. WebGL vendor     │ UNMASKED_VENDOR/RENDERER ← 6 个 ANGLE  │
│                      │ 真实格式 GPU 配置, MAX_TEXTURE_SIZE 联动 │
├──────────────────────┼───────────────────────────────────────┤
│ 12. AudioContext     │ AnalyserNode 频率数据按种子确定性随机化  │
├──────────────────────┼───────────────────────────────────────┤
│ 13. 字体度量噪声      │ measureText.width 按 hash(font+text,   │
│    (§13 新增)        │ FontSeed) 加 ±0.02px 确定性抖动         │
│                      │ (字体枚举指纹此前完全裸奔)              │
├──────────────────────┼───────────────────────────────────────┤
│ 14. Intersection-    │ IntersectionObserver 模拟 (懒加载)      │
│     Observer/Battery │ + Battery API 合理读数                 │
├──────────────────────┼───────────────────────────────────────┤
│ 15. 时区 (DST 正确)  │ Intl.DateTimeFormat.formatToParts 实时 │
│    (§16b 重写)       │ 推算偏移, 而非静态表 (夏令时自动正确)    │
├──────────────────────┼───────────────────────────────────────┤
│ 16. UA 身份段        │ navigator.platform + userAgentData     │
│    (§16 新增)        │ (brands/mobile/platform/               │
│                      │  getHighEntropyValues 高熵完整实现)     │
└──────────────────────┴───────────────────────────────────────┘
```

### 10.3 Geo 一致 Persona

10 个地区 persona，语言/时区/UTC 偏移作为一个整体选定（`fingerprint.go`）：

| 地区代码 | 语言 | 时区 |
|---------|------|------|
| cn | zh-CN, zh, en-US | Asia/Shanghai |
| us-east / us-west / us-central | en-US, en | America/New_York 等 |
| gb | en-GB, en | Europe/London |
| de | de-DE, de, en-US | Europe/Berlin |
| fr | fr-FR, fr, en-US | Europe/Paris |
| jp | ja-JP, ja, en-US | Asia/Tokyo |
| kr | ko-KR, ko, en-US | Asia/Seoul |
| sg | en-SG, en, zh-CN | Asia/Singapore |

联动链路：`Fingerprint.Geo` → 脚本 `__LANGS__`/`__TZ__`（navigator.languages、Intl、Date）→ CDP `AcceptLanguage` → 与代理出口 IP 的地理期望对齐。地区选择：`browser.geo_region` 显式指定 > `GeoCodeFromProxyURL` 从代理 URL 推断（凭据内 `country=`/`cc-`/`region=` 参数、网关 TLD；`uk→gb`、`us→us-east` 归一化）> 随机。代理已启用却推断不出地区时打日志提示配置 `geo_region`。

### 10.4 WebGL GPU 配置 (6 种 ANGLE 真实格式)

```go
// 6 个 ANGLE 真实格式 GPU 配置 (fingerprint.go gpuSpecs),
// 会话开始时选定一个, MAX_TEXTURE_SIZE 联动
var gpuSpecs = []GPUSpec{
    {Vendor: "Google Inc. (Intel)",  Renderer: "ANGLE (Intel, Intel(R) UHD Graphics 620 Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 16384},
    {Vendor: "Google Inc. (Intel)",  Renderer: "ANGLE (Intel, Intel(R) Iris(R) Xe Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 16384},
    {Vendor: "Google Inc. (NVIDIA)", Renderer: "ANGLE (NVIDIA, NVIDIA GeForce GTX 1650 Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 16384},
    {Vendor: "Google Inc. (NVIDIA)", Renderer: "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 16384},
    {Vendor: "Google Inc. (AMD)",    Renderer: "ANGLE (AMD, AMD Radeon(TM) Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 8192},
    {Vendor: "Google Inc. (AMD)",    Renderer: "ANGLE (AMD, AMD Radeon RX 580 Direct3D11 vs_5_0 ps_5_0, D3D11)", MaxTex: 16384},
}
```

> 旧实现的 8 配置池混入了 macOS 专属的 `OpenGL Engine` renderer 与 `VMware SVGA II`（虚拟机显卡——在非虚拟机 persona 上是矛盾信号），且每次注入随机重选。新池统一为 Windows ANGLE/D3D11 格式（与 Windows 主流 persona 一致），会话级稳定。

### 10.5 Canvas 噪声原理（会话级种子）

```javascript
// Canvas 指纹噪声: 对 toDataURL/getImageData 输出按 CanvasSeed
// 确定性扰动 —— 同一会话内同一画布输出恒定 (跨页不跳变),
// 不同会话之间不同 (不可跨站关联)
const seeded = mulberry32(CANVAS_SEED);
// 像素级 ±1 微扰 + getImageData 读取扰动
```

> 与旧实现的区别：旧版用 `Math.random()`，同一会话每次读取结果都不同，页面重绘一次指纹就变——检测方仅需两次采样即可判定噪声注入。种子化后噪声在会话内是**常量函数**，与真实设备的确定性输出行为一致。

---

## 11. 行为模拟 (Behavior)

### 11.1 trusted 输入：合成 JS 事件 → CDP 输入通道（0.3.2）

**核心问题**：合成事件（`element.dispatchEvent(new MouseEvent(...))`）的 `isTrusted` 属性**永远为 false**，且不可伪造（浏览器内核只对真实输入通道置 true）。这是最著名的自动化 tell，任何依赖合成事件的"行为模拟"在 `isTrusted` 检查面前一秒钟穿帮。

**本系统实现**（`rodbackend/identity.go` 的 `simulateHumanBehavior()`）：

```
行为链路 (全部经 CDP Input domain, isTrusted=true):

1. 贝塞尔鼠标扫掠
   behavior.MouseMove(start, end) 生成路径点
   → 逐点 page.Mouse.MoveTo(pt)     [Input.dispatchMouseEvent]
   → 步进间隔 = 总时长/点数 (速度缓动)
   → 150-500ms 阅读式悬停

2. 缓动滚轮
   总滚动 220-600px → 2-6 格 (每格 80-120px, 真实滚轮格距)
   → page.Mouse.Scroll(0, deltaY)   [Input.dispatchMouseEvent]
   → 三角延迟 profile: 加速 → 匀速 → 减速
     delayMs = 40 + 90×(1-progress²) + jitter
```

### 11.2 贝塞尔曲线鼠标移动

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

> 0.3.2 之前该贝塞尔库从未被接线（实际只注入两段合成 JS 滚动/鼠标脚本）；现在经 trusted CDP 输入通道真正投入使用。

### 11.3 行为模拟组成

| 行为 | 实现 | 说明 |
|------|------|------|
| **鼠标移动** | 三次贝塞尔 + CDP trusted 输入 | isTrusted=true，控制点随机 |
| **滚动** | 三角延迟 profile 滚轮 | 加速→匀速→减速，真实格距 |
| **打字延迟** | 每次按键随机延迟 | 模拟人类打字速度 |
| **随机停顿** | 阅读式悬停 150-500ms | 模拟阅读/查看行为 |

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

`internal/browser/settle/settle.go` 的 `Wait()` 监控 CDP (Chrome DevTools Protocol) 事件判断页面是否"加载完成"，被克隆浏览器池与通用浏览器控制器共用：

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
| `quiet` | 连续无网络事件的时间阈值，由调用方传入（克隆场景取 `apps.clone.settle`，默认 1500ms） |
| `heartbeat` | 检查间隔（固定 200ms） |
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

`internal/errsignal/` 基于信号（HTTP 状态码、错误消息模式、网络状况）将所有网络/浏览器错误分为 7 类，常量为 `ClassUnknown` / `ClassTransient` / `ClassRateLimited` / `ClassAuthRequired` / `ClassBotDetection` / `ClassPermanent` / `ClassInvalid`：

| 错误类 | 触发信号 | 处理策略 |
|--------|---------|---------|
| **ClassBotDetection** | Cloudflare / Turnstile / CAPTCHA / WAF 拦截关键词（含 403 且带 cloudflare/captcha 特征） | 不简单重试（MaxRetries=0），升级隐身等级后重新请求 |
| **ClassRateLimited** | 429，或 503 且带 rate/limit/retry 关键词 | 遵守 Retry-After，否则 5s/10s/20s 退避，最多 3 次重试 |
| **ClassPermanent** | 404 / 410 / DNS 解析失败 ("no such host") | 跳过，不重试 |
| **ClassAuthRequired** | 401 / 403 | 升级凭据（Cookie/登录态）或跳过，不重试 |
| **ClassInvalid** | 400 / 422，格式错误响应 | 跳过并记录以便排查，不重试 |
| **ClassTransient** | 5xx / 超时 / 连接重置、拒绝、断开等网络错误 | 指数退避重试（1s/2s/4s...上限 30s），最多 3 次 |
| **ClassUnknown** | 无法归类的错误 | 带状态码则不重试；仅有错误消息时保守重试 1 次 |

> 注意：403 优先落入 ClassAuthRequired（除非伴随 Cloudflare/CAPTCHA 特征升格为 ClassBotDetection）；DNS 失败归入 ClassPermanent（跳过而非重试）。

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
HTTP 429 (或 503 + 限流特征)
    │
    ├── 检查 Retry-After 头
    │   ├── 有 → 等待指定时间
    │   └── 无 → 默认退避 5s / 10s / 20s
    │
    ├── 最多 3 次重试
    │
    └── Transient 类错误则用指数退避 1s / 2s / 4s (上限 30s)
```

---

## 16. 配置参考

### 16.1 反爬相关配置项

配置文件中真实存在的反爬相关键（以 `internal/config/defaults.go` 与 `internal/config/types_apps.go` 为准）：

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `apps.clone.antibot_enabled` | bool | `true` | 启用反反爬检测与升级（克隆/下载共用） |
| `apps.clone.antibot_auto_escalate` | bool | `true` | 检测到阻塞后自动升级反爬等级 |
| `apps.clone.stealth` | bool | `true` | 克隆场景的隐身注入（克隆专用，优先于全局键） |
| `browser.backend` | string | `rod` | 浏览器后端: rod/chromedp（`apps.clone.browser_backend` 可覆盖） |
| `browser.stealth` | bool | `false` | 全局浏览器隐身开关（克隆场景以 `apps.clone.stealth` 为准） |
| `apps.clone.cookie_file` | string | `""` | Netscape 格式 Cookie 文件（导入登录态 / cf_clearance） |
| `apps.clone.user_agent` | string | `""` | 自定义 User-Agent |
| `apps.clone.proxy_enabled` | bool | `false` | 启用代理池 |
| `apps.clone.proxy_pool` | []string | `[]` | 代理地址列表 |
| `apps.clone.proxy_rotate_every` | int | 10 | 每 N 次请求轮换代理 |
| `apps.clone.settle` | int | 1500 | 网络空闲等待阈值 (ms)，即 §13 的 quiet |

> 升级体系的等级上限、每 URL 重试数、冷却时间等（MaxLevel=`LevelAggressive`、MaxRetries=3、Cooldown=30s、InitialLevel=`LevelNone`）是 `antibot.Config` 的 Go API 字段默认值，**没有对应的 YAML 配置键**，如需调整须在代码层传入。

### 16.2 CLI 选项

```bash
wukong apps clone <url> [flags]

反爬相关:
  --no-antibot           禁用反反爬检测与升级 (默认开启)
  --no-antibot-auto      仅检测阻塞, 跳过自动升级 (手动控制等级)
  --no-stealth           禁用隐身反检测 (默认开启)
  --no-headless          显示可见 Chrome 窗口 (手动过 Turnstile)
  --cookies string       导入 Netscape 格式 Cookie 文件
  --browser-backend str  浏览器后端: rod / chromedp

wukong apps download <url> [flags]

  --antibot              启用反反爬 (默认 true)
  --stealth              启用隐身反检测 (默认 true)
```

克隆相关的其余旗标（分页/作用域/Worker 等）见 [CLONE_GUIDE.md §19.2](./CLONE_GUIDE.md#19-配置参考)。

### 16.3 推荐配置

#### 温和模式（默认）

```yaml
apps:
  clone:
    antibot_enabled: true
    antibot_auto_escalate: true
    stealth: true
```

#### 激进模式（难爬站点）

```yaml
browser:
  backend: rod
apps:
  clone:
    antibot_enabled: true
    antibot_auto_escalate: true
    stealth: true
    settle: 3000          # SPA/重 JS 站加大网络空闲等待
    crawl_delay: 2000     # 主动降速 (ms)
    proxy_enabled: true
    proxy_pool:
      - "http://proxy1.example.com:8080"
      - "http://proxy2.example.com:8080"
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
| **代码量** | 约 1900 行 (pool.go) | 较少 |

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
| 浏览器接口 | `internal/browser/types/` |
| 浏览器后端与池 | `internal/browser/` (含 Chromedp `pool.go`) |
| Rod 后端 (含 Chrome 检测、API 发现、UA/行为身份层) | `internal/browser/rodbackend/` (含 `identity.go`) |
| 反爬引擎 (检测/升级/TLS 指纹) | `internal/browser/antibot/` |
| 多维探测器 (含 WAF 签名库) | `internal/browser/antibot/prober/` |
| 会话级指纹生成 | `internal/browser/stealth/fingerprint.go` |
| 隐身注入 (模板渲染) | `internal/browser/stealth/stealth.go` |
| 行为模拟 (贝塞尔库) | `internal/browser/behavior/` |
| 代理池 | `internal/browser/proxy_pool.go` |
| 网络空闲 | `internal/browser/settle/` |
| 错误分类 | `internal/errsignal/` |

---

> **版本**: v0.3.2 | **最后更新**: 2026-08-26 | **相关代码**: internal/browser/ + internal/errsignal/
