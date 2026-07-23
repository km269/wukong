# 反反爬技术指南

> 检测引擎: 8 种阻塞原因 | 升级策略: 5 级隐身等级 | UA 池: 161 个真实浏览器配置
> 延迟抖动: 等级自适应 | 自动升级: 检测→升级→重试 | Cloudflare 识别: 响应头 + DOM 特征

---

## 目录

1. [系统概述](#1-系统概述)
2. [阻塞检测引擎](#2-阻塞检测引擎)
3. [自动升级机制](#3-自动升级机制)
4. [5 级隐身等级详解](#4-5-级隐身等级详解)
5. [UA 指纹池](#5-ua-指纹池)
6. [资源下载反爬策略](#6-资源下载反爬策略)
7. [配置参考](#7-配置参考)
8. [实战案例](#8-实战案例)
9. [常见问题](#9-常见问题)

---

## 1. 系统概述

### 1.1 设计理念

反反爬系统采用 **"检测 → 升级 → 重试"** 的闭环机制：

```
请求失败 / 响应异常
      │
      ▼
┌─────────────────┐
│  阻塞检测引擎    │  8 种阻塞原因识别
│  (Detector)     │  HTTP 状态码 + 响应头 + DOM 内容
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  自动升级器      │  5 级隐身等级
│  (Escalator)    │  逐级增强反检测措施
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  重试执行        │  等级自适应延迟
│  (Retry)        │  UA 轮换 / Referer 伪造
└─────────────────┘
```

### 1.2 核心组件

| 组件 | 文件 | 职责 |
|------|------|------|
| **Engine** | `antibot.go` | 对外统一接口，封装检测 + 升级 |
| **Detector** | `detector.go` | 阻塞原因识别（HTTP + DOM 双层检测） |
| **Escalator** | `escalator.go` | 自动升级引擎，等级管理，UA 池 |

### 1.3 代码组织

```
internal/browser/antibot/
    ├── antibot.go           # Engine 主接口 (~160 行)
    ├── detector.go          # 阻塞检测器 (~300 行)
    ├── detector_test.go     # 检测器单元测试
    ├── escalator.go         # 自动升级器 (~470 行)
    └── escalator_test.go    # 升级器单元测试
```

---

## 2. 阻塞检测引擎

### 2.1 8 种阻塞原因

| 原因常量 | 触发条件 | 说明 |
|---------|---------|------|
| `ReasonForbidden` | HTTP 403 | 服务器明确拒绝访问 |
| `ReasonRateLimited` | HTTP 429 | 请求频率超限 |
| `ReasonUnavailable` | HTTP 503 / 维护页 | 服务暂时不可用 |
| `ReasonCloudflare` | cf-chl-* 头 / Turnstile | Cloudflare 挑战页 |
| `ReasonCaptcha` | CAPTCHA 关键词 | 人机验证页面 |
| `ReasonBlocked` | "access denied" 等 | 通用拦截页面 |
| `ReasonTimeout` | 请求超时 | 可能的 bot wall |
| `ReasonEmpty` | 空响应体 | 可疑的空页面 |

### 2.2 双层检测机制

#### 第一层：HTTP 层检测（快速）

```go
func DetectHTTP(statusCode int, headers http.Header) (BlockReason, string)
```

**检测逻辑**：
1. **状态码快速判断**：403 → Forbidden, 429 → RateLimited, 503 → 需进一步检查
2. **Cloudflare 挑战头检测**：503 + `cf-chl-bypass` / `cf-chl-out` / `cf-chl-proxied` → Cloudflare
3. **前置预检**：`HasCloudflareHeaders()` 可在 Chrome 启动前判断是否启用隐身

> **注意**：`cf-ray`、`cf-cache-status` 等普通 Cloudflare 头**不**表示阻塞，仅说明站点使用 Cloudflare CDN。

#### 第二层：DOM 层检测（深度）

```go
func DetectDOM(html string) (BlockReason, string)
```

**检测逻辑**：

```
空响应? → 是 → ReasonEmpty
    │
    否
    ▼
维护页? (小页面 + 维护关键词) → 是 → ReasonUnavailable
    │
    否
    ▼
超短页面 (<150字节) + forbidden/denied → 是 → ReasonBlocked
    │
    否
    ▼
Cloudflare Turnstile? (challenges.cloudflare.com 等) → 是 → ReasonCloudflare
    │
    否
    ▼
小页面 (<=50KB) + CAPTCHA 关键词 → 是 → ReasonCaptcha
    │
    否
    ▼
无阻塞 → ReasonNone
```

### 2.3 特征关键词库

#### CAPTCHA / 反爬关键词 (20+)

```
captcha, verify you are human, are you a human, please verify,
security check, cf-challenge, cf-browser-verification,
checking your browser, ddos protection, access denied, blocked,
your request has been blocked, please enable javascript,
javascript is required, browser check, automated access,
suspicious activity, ...
```

#### Cloudflare Turnstile 特征 (7 个)

```
challenges.cloudflare.com, challenges.cloudflare.com/turnstile,
cf-turnstile, turnstile, __cf_chl, cf_chl_
```

#### 维护页关键词 (7 个)

```
technical difficulties, under maintenance, currently unavailable,
service unavailable, site is down, down for maintenance, we'll be back soon
```

### 2.4 错误模式检测

除了 HTTP 响应检测，还能从网络错误中推断反爬：

```go
func (e *Engine) CheckError(err error) (BlockReason, string)
```

| 错误模式 | 推断原因 |
|---------|---------|
| timeout / deadline exceeded | `ReasonTimeout` — 可能的 bot wall |
| connection refused / reset / EOF / broken pipe | `ReasonUnavailable` — 可能的速率限制 |

---

## 3. 自动升级机制

### 3.1 升级流程

```
检测到阻塞
    │
    ▼
是否可重试? ──否──→ 放弃，记录错误
    │
    是
    ▼
当前 URL 重试次数 > MaxRetries? ──是──→ LevelBackoff，放弃
    │
    否
    ▼
升级等级 +1 (不超过 MaxLevel)
    │
    ▼
计算重试延迟（等级 × 原因系数 × 随机抖动）
    │
    ▼
返回: 重试=true, 延迟=X, 新等级=L, 消息=描述
```

### 3.2 升级决策函数

```go
func (e *Escalator) Check(
    url string,
    reason BlockReason,
    statusCode int,
) (
    shouldRetry bool,
    retryDelay time.Duration,
    newLevel Level,
    message string,
)
```

### 3.3 延迟计算规则

**基础延迟**：`Cooldown`（默认 30s）

**原因系数**：
- Cloudflare / RateLimited：× 2（这些服务追踪 IP，冷却更慢）
- 其他：× 1

**等级抖动**：
| 等级 | 延迟计算 | 示例 (基础 30s) |
|------|---------|----------------|
| LevelFlags | 基础 / 2 | 15s |
| LevelStealth | 基础 + 随机(0~基础) | 30s ~ 60s |
| LevelAggressive | 基础 + 随机(0~3×基础) | 30s ~ 120s |

**最终延迟** = 基础延迟 × 原因系数 × 等级抖动

### 3.4 可重试性判断

```go
func ShouldRetry(reason BlockReason) bool
```

| 阻塞原因 | 可重试? | 说明 |
|---------|---------|------|
| Forbidden (403) | ✅ 是 | 加强隐身可能绕过 |
| RateLimited (429) | ✅ 是 | 等待 + 降速 |
| Cloudflare | ✅ 是 | 隐身脚本可能通过挑战 |
| Captcha | ✅ 是 | 取决于 CAPTCHA 类型 |
| Blocked | ✅ 是 | 通用拦截，试试更强隐身 |
| Timeout | ✅ 是 | 超时重试 |
| Unavailable (503) | ✅ 是 | 临时不可用，重试可能恢复 |
| Empty | ❌ 否 | 空内容重试没用 |
| None | ❌ 否 | 没阻塞，不用重试 |

---

## 4. 5 级隐身等级详解

### 等级总览

| 等级 | 常量 | Chrome 标志 | 隐身 JS | 延迟抖动 | UA 轮换 |
|------|------|------------|---------|---------|---------|
| 0 | `LevelNone` | - | - | - | - |
| 1 | `LevelFlags` | ✅ | - | 0.5-2s | - |
| 2 | `LevelStealth` | ✅ | ✅ | 1-4s | - |
| 3 | `LevelAggressive` | ✅ | ✅ | 2-8s | ✅ |
| 4 | `LevelBackoff` | - | - | - | - |

### Level 0: None（无措施）

**默认行为**，纯直连。适用于无反爬的站点。

```
特征: 标准 Chrome 启动参数，无额外隐身措施
适用: 内部系统、文档站、明确允许爬取的站点
```

### Level 1: Flags（浏览器标志）

**仅启用 Chrome 反检测标志**，不注入 JS。

**关键标志**：
- `--disable-blink-features=AutomationControlled` — 移除 `navigator.webdriver`
- 其他自动化相关标志清理

```
效果: 基础级反检测，能骗过简单的 webdriver 检测
开销: 几乎为零，不影响性能
```

### Level 2: Stealth（完整隐身）

**Chrome 标志 + JS 隐身脚本注入**。

**注入方式**：`Page.addScriptToEvaluateOnNewDocument`

**隐身脚本通常覆盖**：
- `navigator.webdriver` → false
- `navigator.plugins` → 模拟真实插件
- `navigator.languages` → 真实语言设置
- `chrome.runtime` 对象模拟
- `Permissions` API 伪装
- WebGL 指纹一致性
- Canvas 指纹保护（可选）

```
效果: 中高级反检测，能绕过大部分指纹检测
开销: 页面加载时注入 JS，轻微性能影响
```

### Level 3: Aggressive（激进模式）

**完整隐身 + 随机延迟 + UA 轮换**。

**额外措施**：
- **随机延迟**：每次请求 2-8 秒随机等待
- **UA 轮换**：从 161 个真实 UA 池中随机选择
- **完整 Client Hints**：Sec-CH-UA 系列头与 UA 一致

```
效果: 最高级反检测，模拟真实用户行为模式
开销: 延迟降低爬取速度，约 2-8s/请求
```

### Level 4: Backoff（退避放弃）

**最终状态**，超过最大重试次数后进入。

- 不再重试当前 URL
- 记录错误日志
- 继续处理其他 URL

---

## 5. UA 指纹池

### 5.1 设计原则

**一致性原则**：User-Agent 和 Client Hints 必须匹配。

现代反爬系统会校验 `User-Agent` 与 `Sec-CH-UA`、`Sec-CH-UA-Mobile`、`Sec-CH-UA-Platform` 等头的一致性。不一致的指纹会被立即标记为 bot。

### 5.2 UAProfile 结构

```go
type UAProfile struct {
    UserAgent       string  // 完整 User-Agent
    SecChUa         string  // Sec-CH-UA: 品牌 + 版本
    SecChUaMobile   string  // Sec-CH-UA-Mobile: ?0 / ?1
    SecChUaPlatform string  // Sec-CH-UA-Platform: "Windows" / "macOS" 等
}
```

### 5.3 浏览器覆盖

| 浏览器 | 桌面 | 移动 | 说明 |
|--------|------|------|------|
| Chrome | ✅ Windows/macOS/Linux | ✅ Android | 多个版本 (128/129/130) |
| Firefox | ✅ Windows/macOS | - | v132 |
| Safari | ✅ macOS | ✅ iOS | v18 |
| Edge | ✅ Windows/macOS | - | v130 |
| Opera | ✅ Windows | - | v114 |
| Brave | ✅ Windows | - | v1.69 |

### 5.4 轮换策略

```go
// 从全部 UA 池中随机选
func (e *Escalator) RotateUserAgent() *UAProfile

// 仅从桌面 UA 池中随机选
func (e *Escalator) GetRandomDesktopUA() *UAProfile
```

**触发时机**：升级到 `LevelAggressive` 时自动轮换。

---

## 6. 资源下载反爬策略

### 6.1 4 层回退机制

与 [CLONE_GUIDE.md](./CLONE_GUIDE.md) 中的资源下载策略配合，形成多层次反爬防线：

```
Layer 1: HTTP 直连
    │   失败 (403 / 超时 / 连接被拒)
    ▼
Layer 2: CDP Network.loadNetworkResource
    │   用浏览器的 TLS 指纹 / Cookie 上下文
    ▼
Layer 3: img 标签加载 (带 Referer)
    │   最接近真实用户行为
    ▼
Layer 4: fetch API (浏览器内)
    │   完整浏览器上下文
    ▼
  彻底失败
```

### 6.2 Referer 策略

**Referer 覆盖机制**：对特定域名使用特殊 Referer。

**示例配置**：
- `media.defense.gov` → 使用来源页面 URL 作为 Referer
- `*.defense.gov` → 匹配所有子域名

**工作原理**：
1. 下载资源时检查域名匹配
2. 命中规则则使用覆盖 Referer
3. 未命中则使用默认 Referer（当前页面 URL）
4. 支持精确域名和后缀模式

### 6.3 全站随机延迟

**设计决策**：假设所有网站都有反爬措施，统一添加 300-1000ms 随机延迟。

```go
delay := time.Duration(300+rand.Intn(700)) * time.Millisecond
```

**为什么不分域名？**
- 避免误判导致被封
- 代码更简单，无需维护域名列表
- 对大多数站点影响可忽略
- 防御性编程，安全第一

---

## 7. 配置参考

### 7.1 主要配置项

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `antibot.enabled` | bool | `true` | 启用反反爬检测 |
| `antibot.auto_escalate` | bool | `true` | 自动升级隐身等级 |
| `antibot.initial_level` | string | `none` | 初始等级: none/flags/stealth/aggressive |
| `antibot.max_level` | string | `aggressive` | 最高等级 |
| `antibot.max_retries` | int | `3` | 单 URL 最大重试次数 |
| `antibot.cooldown` | duration | `30s` | 重试基础冷却时间 |

### 7.2 CLI 选项

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
```

### 7.3 推荐配置

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
  initial_level: stealth  # 直接从 stealth 开始
  max_level: aggressive
  max_retries: 5
  cooldown: 60s
```

#### 手动模式（调试用）

```yaml
antibot:
  enabled: true
  auto_escalate: false   # 不自动升级
  initial_level: aggressive  # 固定等级
  max_retries: 1
```

---

## 8. 实战案例

### 案例 1: media.defense.gov 图片 403

**现象**：HTTP 直连下载图片返回 403 Forbidden。

**分析**：
- 国防部媒体服务器校验 Referer
- 直接 HTTP 请求没有正确的 Referer 头

**解决方案**：
1. Referer 覆盖：`media.defense.gov` → 来源页面 URL
2. 回退到浏览器下载（4 层回退）
3. img-on-referer 策略：先打开来源页，再加载图片

**效果**：浏览器下载成功，绕过 Referer 校验。

### 案例 2: Cloudflare 保护站点

**现象**：页面返回 503，包含 `cf-chl-bypass` 头。

**检测流程**：
1. HTTP 层检测到 503 + Cloudflare 挑战头
2. 触发 `ReasonCloudflare`
3. 自动升级：None → Flags → Stealth

**解决方案**：
- LevelStealth 注入隐身脚本
- 增加重试延迟（Cloudflare × 2 系数）
- 等待 challenge 页面自动通过

### 案例 3: 速率限制 (429)

**现象**：连续请求后返回 429 Too Many Requests。

**检测**：HTTP 429 → `ReasonRateLimited`

**自动应对**：
1. 升级到更高等级
2. 延迟翻倍（rate-limit 系数 × 2）
3. 继续重试，逐步放慢速度

---

## 9. 常见问题

### Q1: 启用反反爬会变慢多少？

**等级 0 (None)**: 无影响

**等级 1 (Flags)**: 几乎无影响（仅 Chrome 启动参数）

**等级 2 (Stealth)**: 几乎无影响（JS 注入在页面加载前完成）

**等级 3 (Aggressive)**: 每次请求增加 2-8 秒随机延迟，速度明显下降

> **建议**：先用默认自动升级，遇到阻塞再升级，不要一开始就用 aggressive。

### Q2: 为什么不一开始就用最高等级？

1. **性能代价**：高等级有延迟，影响爬取速度
2. **过度伪装风险**：有些站点会检测"过于完美"的指纹
3. **按需升级**：只有检测到阻塞才升级，平衡速度和成功率
4. **IP 信誉**：频繁切换 UA 和指纹反而可能引起怀疑

### Q3: 能绕过所有反爬吗？

**不能**。以下场景无法绕过：
- 强 CAPTCHA（reCAPTCHA v3 高分数阈值、hCaptcha 严格模式）
- IP 黑名单
- 必须登录且有人工审核的站点
- 硬件指纹绑定（如 Trusted Platform Module）

**能绕过的场景**：
- 简单的 webdriver 检测
- 基础指纹检测
- Referer / UA 校验
- 轻度速率限制
- Cloudflare 中等难度挑战

### Q4: 如何判断用哪个等级？

**决策树**：

```
站点能正常爬吗？
    ├── 能 → LevelNone（默认即可）
    └── 不能
         ├── 403 Forbidden → 试试 LevelFlags
         ├── Cloudflare → 试试 LevelStealth
         ├── 429 限流 → 试试 LevelStealth + 降速
         └── 还是不行 → LevelAggressive
```

### Q5: 重试次数设多少合适？

- **普通站点**：3 次（默认）
- **Cloudflare 站点**：5 次（challenge 可能需要多次尝试）
- **调试阶段**：1 次（快速看到错误）
- **大规模爬取**：3-5 次，平衡成功率和时间

---

## 附录

### 相关文档

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | 系统架构详解 |
| [CLONE_GUIDE.md](./CLONE_GUIDE.md) | 网站克隆技术指南 |
| [CONFIG.md](./CONFIG.md) | 配置参考手册 |
| [README.md](../README.md) | 项目主页 |

### 相关代码

- `internal/browser/antibot/` — 反反爬引擎 (5 文件)
- `internal/apps/clone/enhanced_cloner.go` — 克隆引擎集成
- `internal/browser/rod/` — Rod 浏览器后端（隐身实现）

---

> **版本**: v1.0 | **最后更新**: 2026-07-23 | **相关代码**: internal/browser/antibot/ (5 文件, ~930 行)
