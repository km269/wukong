# Wukong Web 操作深度分析

> 基于对 `internal/browser/`、`internal/apps/clone/`、`internal/apps/pack/`、`internal/search/`、`internal/search/tune/`、`internal/search/vertical/`、`internal/search/chunking/`、`internal/errsignal/`、`pkg/httpclient/`、`pkg/zim/` 等代码的逐文件精读，产出本专项分析。所有函数签名、行号、数据结构均为真实源码。

---

## 目录

1. [全景架构](#1-全景架构)
2. [浏览器子系统](#2-浏览器子系统)
3. [克隆管线](#3-克隆管线)
4. [搜索管线](#4-搜索管线)
5. [搜索调优引擎](#5-搜索调优引擎)
6. [垂直搜索路由](#6-垂直搜索路由)
7. [语义分块](#7-语义分块)
8. [错误信号分类](#8-错误信号分类)
9. [HTTP 客户端](#9-http-客户端)
10. [技术特点综合评估](#10-技术特点综合评估)
11. [优化建议](#11-优化建议)
12. [相关文档](#12-相关文档)

---

## 1. 全景架构

Wukong 的 Web 操作子系统涵盖 **浏览器自动化、网页克隆、搜索引擎聚合、搜索策略调优、垂直搜索路由、语义分块、错误信号分类、HTTP 客户端** 八大能力。

```
┌──────────────────────────────────────────────────────────────────────────┐
│                         Web 操作全景架构                                    │
│                                                                          │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐    │
│  │  浏览器引擎   │  │  克隆管线    │  │  搜索聚合    │  │  垂直路由    │    │
│  │ rod/chromedp │  │ 8 阶段管线  │  │ 6 源并行    │  │ 4 后端      │    │
│  │ 双后端       │  │ 4 层资产回退 │  │ URL 去重    │  │ 意图检测    │    │
│  │ 反爬升级     │  │ ZIM 打包    │  │ 浏览器兜底   │  │ MergeMode   │    │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘    │
│         │                │                │                │            │
│  ┌──────┴────────────────┴────────────────┴────────────────┴──────┐    │
│  │                      HTTP 客户端 (pkg/httpclient)                  │    │
│  │  DNS 回退(公共 DNS) · utls 指纹 · 重试 · 限流(token-bucket)       │    │
│  └──────┬──────────────────────────────────────────────────────┬────┘    │
│         │                                                  │          │
│  ┌──────┴──────┐                              ┌─────────────┴────┐     │
│  │ errsignal   │                              │  搜索调优 (tune)   │     │
│  │ 7 类分类     │                              │ SPA 遗传算法      │     │
│  │ 重试策略     │                              │ 多保真度 3 阶段   │     │
│  └─────────────┘                              │ RobustScore      │     │
│                                               └──────────────────┘     │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                    语义分块 (chunking)                             │   │
│  │     段落→打包→句子→词→overlap+合并                                 │   │
│  └──────────────────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────────────────┘
```

**核心优势**：多级容灾 + 自适应反爬 + 平台 API 快捷通道 + 遗传算法搜索调优。

---

## 2. 浏览器子系统

### 2.1 双后端架构（`internal/browser/`）

```
┌─────────────────────────────────────────────────────────┐
│  BrowserBackend 接口 (types/types.go:69)                │
│  ├─ Render(ctx, url) → RenderResult                     │
│  ├─ RenderWithReferer(ctx, url, referer) → RenderResult │
│  ├─ SetSettle(dur)                                     │
│  ├─ StealthEnabled() / EnableStealth()                 │
│  ├─ SetBehaviorSimulation(bool)                         │
│  ├─ DownloadAsset(ctx, url) → AssetResult               │
│  ├─ Screenshot(ctx, url, path) → string                 │
│  └─ Close()                                             │
├─────────────────────────────────────────────────────────┤
│  chromedp 后端 (pool.go)      │  rod 后端 (rodbackend/) │
│  默认后端，成熟稳定            │  优先使用，更现代       │
│  DefaultExecAllocatorOptions  │  launcher.New()         │
│  + 20+ flags                  │  + ControlURL 复用      │
│                               │  + 启动重试 3 次        │
└─────────────────────────────────────────────────────────┘
```

`NewBackend(backendType, opts)`（`backend.go:45`）选择后端，rod 启动失败时自动回退 chromedp。

两后端共享 `internal/browser/renderkit`（2026-08-23 引入）：行为模拟/滚动/链接提取的注入 JS 与内容类型分类（`IsHTMLContentType`/`IsTextContent`）单点维护，保证两侧页面行为一致——chromedp 版内容类型判断随之从裸比较升级为大小写/参数归一化。

**BackendOptions** 结构控制后端行为：Headless / Workers / Settle / RenderTimeout / Scroll / ChromeBin / ControlURL / Stealth / ProfileDir / DisableDownloads 等。

### 2.2 Worker 池管理

chromedp 版（`pool.go`）与 rod 版（`rodbackend/pool.go`）采用同构的 worker 池架构：

```go
type Pool struct {
    opts               Options
    workers            []*worker           // 固定数量 worker（默认 4）
    queue              chan *renderJob     // 任务队列
    wg                 sync.WaitGroup      // 等待所有 worker
    behaviorSimulator  *behavior.Simulator // 行为模拟（随机滚动/mousemove）
    escalator          *antibot.Escalator  // 反爬升级器（5 级升级体系）
    currentUA          *antibot.UAProfile  // 当前 UA 配置
}
```

**renderJob** 流程（以 rod 为主线，`rodbackend/pool.go:298-772`）：

```
renderJob{url, referer, resultCh, ctx}
  │
  ├─ 1. 创建/复用 tab（缓存于 w.page）
  ├─ 2. UA/Header 注入
  │      NetworkSetUserAgentOverride + NetworkSetExtraHTTPHeaders
  │      仅 Accept + Upgrade-Insecure-Requests 两个最小头
  │      （注释强调不要手动设 Sec-Fetch-*，否则触发 ERR_BLOCKED_BY_CLIENT）
  ├─ 3. 导航 + 等待: Navigate → WaitLoad → settle.Wait(quiet)
  ├─ 4. 行为模拟（可选）: 随机滚动 + mousemove
  ├─ 5. 滚动加载: JS 循环滚动最多 20 次
  ├─ 6. 网络事件收集: NetworkResponseReceived 记录请求ID/URL/MIME/状态
  ├─ 7. 资产内联收集: 页面内 JS 并发 fetch→arrayBuffer→btoa 分批取回
  │      按 chunk=10 分批，每批一次 CDP Eval 内 Promise.all 真并发，
  │      每个 fetch 独立 AbortController 超时
  │      限制: 单资产 10MB, 单资产超时 5s, 每页最多 200 个
  ├─ 8. API 发现: discoverAPIs 从网络事件提取接口
  └─ 9. DOM 提取: page.HTML() + title/cookies/links

→ RenderResult{HTML, URL, Title, ContentType,
               CloudflareClearance, Referer,
               CollectedAssets, ExtractedLinks, DiscoveredAPIs}
```

### 2.3 Controller 双模式（`controller.go`）

`Controller` 封装了 HTTP 模式与浏览器模式的自动切换：

```
Controller
  ├─ HTTP 模式 (isBrowserMode=false)
  │   └─ Navigate/NavigateWithReferer → httpclient.Do → HTML/Title
  │
  └─ 浏览器模式 (isBrowserMode=true)
      └─ backend.Render/RenderWithReferer → RenderResult
          ├─ Screenshot: 先调 backend.Screenshot（CDP Page.captureScreenshot）
          │              失败 → screenshotWithHTTP（HTML 包装文件兜底）
          ├─ Click / Fill / ExtractText / ExtractLinks
          └─ DownloadAsset → backend.DownloadAsset
```

> **截图实现**：`Screenshot()`（`controller.go:359`）在浏览器模式下调用 `backend.Screenshot()`——如成功返回真实 PNG 路径；如失败则回退到 `screenshotWithHTTP()` 将 HTML 包装为自包含文件。HTTP 模式始终使用 HTML 包装文件。

### 2.4 Settle 网络空闲检测（`settle/settle.go`）

```
Wait(tabCtx, quiet time.Duration) error
  │
  ├─ 监听 4 个 CDP 事件刷新 lastActivity:
  │   LoadingFinished / DataReceived / RequestWillBeSent / ResponseReceived
  │
  ├─ ticker 每 200ms 检查:
  │   now - lastActivity >= quiet → 返回 nil（页面已稳定）
  │
  └─ 总上限 = quiet + 10s → 超时返回 nil（非致命："page may still be usable"）
```

### 2.5 容器环境感知（`rodbackend/detect.go`）

```go
func isContainerized() bool   // 检测 docker/K8s 环境
func needNoSandbox() bool     // 容器中需要 --no-sandbox
func FindChromePath() string  // 按 OS 候选路径 + CHROME_PATH 探测 Chrome/Edge/Chromium
```

---

## 3. 克隆管线

### 3.1 完整克隆管线（`enhanced_cloner.go:507-886`）

```
Clone(ctx, seedURL) → CloneResult
  │
  ├─ 1. 解析 seed URL
  ├─ 2. 设置 Referer / RefererOverrides（域名级覆盖）
  ├─ 3. preflightCloudflareCheck()         ← Turnstile 预检测
  │      GET（非 HEAD）+ 前 8KB body 判定 Turnstile 标记，
  │      无论 Stealth 是否开启均执行；检出 Turnstile → 关闭
  │      AntibotAutoEscalate（headless 无法解交互挑战，避免空转）
  │      未开 Stealth 且命中 cf 头 → 预启用 Stealth
  ├─ 4. runAntibotProbe()                  ← 多维度探测
  │      仅 AntibotEnabled && !Stealth 时执行（Stealth 已是高级别）
  │      prober.NewProber().Probe(): HTTP header / robots / WAF / rate-limit / JS chall
  ├─ 5. 建目录 pages/ assets/
  ├─ 6. 初始化:
  │      CloneCache(incremental) / CloneSession(cookie)
  │      httpClient(TLS Insecure + HTTP2) / robots / RateLimiter
  │      frontier.load(state.json)         ← 断点续抓
  │      browser.NewBackend(proxy pool)    ← 启动 headless Chrome
  │      antibot.Engine(LevelStealth)
  ├─ 7. traversalDispatcher + pageWorker×N + assetWorker×M
  ├─ 8. enqueuePage(seed) + sitemap seeds
  └─ 9. wg.Wait → frontier.save → cookies.Save → Result
```

**反爬 5 级升级体系**（`internal/browser/antibot/escalator.go`，渲染错误触发逐级 Escalate）：

| 级别 | 名称 | 措施 |
|------|------|------|
| 0 | `LevelNone` | 无反爬措施（默认行为） |
| 1 | `LevelFlags` | 仅 Chrome 反检测 flags（无 JS 注入） |
| 2 | `LevelStealth` | 全量 stealth JS 注入 + 全部 Chrome flags |
| 3 | `LevelAggressive` | Stealth + 随机延迟 + UA 轮换 |
| 4 | `LevelBackoff` | 指数退避 + 上报失败 |

### 3.2 单页面处理（`processPage`，`enhanced_cloner.go:959-1322`）

```
processPage(ctx, pageURL)
  │
  ├─ 1. 增量缓存检查 cache.CheckNeedsUpdate (ETag/Last-Modified)
  ├─ 2. robots 合规 + 限速 + 反爬抖动 antibot.Wait()
  ├─ 3. TryPlatformAPI(url)    ← 快捷通道: Reddit/HN/GitHub/Wikipedia/arXiv
  │      命中 → 免开浏览器（10-50× 提速）→ 直接处理
  ├─ 4. browserPool.RenderWithReferer(url, referer)
  │      ErrNotHTML → 路由到资产队列
  ├─ 5. 渲染错误 → antibot.CheckError → Escalate → errsignal.Classify
  │      → HTTP 回退 → Wayback 回退
  ├─ 6. 提取 cf_clearance cookie 复用
  ├─ 7. 收集浏览器内联资产存盘
  ├─ 8. antibot.CheckResponse → Turnstile 自动解决
  │      → SPA salvage（DOM<300字节 → 延长 settle 重渲）
  ├─ 9. sanitize.CleanHTMLWithOptions（去脚本/事件/危险URL）
  ├─ 10. rewriteAndDiscover（单遍 DOM 遍历）
  │      honeypot 链接识别 + lazy-load 解析 + 分页模式探测
  ├─ 11. ContentDeduper.TryDedup (SHA-256 → 硬链接)
  └─ 12. 写盘 → 缓存更新 → frontier 标记
```

### 3.3 资产下载 4 层回退策略

```
AssetDownloader.Download(url)
  │
  ├─ Layer 1: HTTP GET（指数退避重试 3 次）
  │   transient() 只重试: 403/408/425/429/5xx/网络错误
  │   DNS 错误 → 不可重试，转 Layer 3
  │
  ├─ Layer 2: 域名级 Referer 覆盖
  │   getRefererForURL: media.defense.gov、CDN 等按后缀改写
  │
  ├─ Layer 3: 浏览器回退（HTTP 失败 + dns/network/非 404 时）
  │   browserPool.DownloadAsset:
  │   ├─ chromedp: 导航→检查临时下载→<img>标签→JS fetch+base64→Network.loadNetworkResource
  │   └─ rod: 导航→<img> on-referer页→Network.loadNetworkResource→JS fetch→缩略图特化
  │
  └─ Layer 4: Wayback 兜底（HTTP/浏览器均失败后）
      页面级: archiveFallback.FetchArchivedPage
      资产级: archiveFallback.FetchArchivedAsset
      （Availability API → id_ 原始快照 → 16MB 上限 → 剥离工具栏）
```

### 3.4 分页支持（克隆层 3 种模式 + 游标兜底）

克隆层 `detectAndGeneratePagination`（`enhanced_cloner.go:2139`）从已提取链接中检测分页模式并补全生成：

| 模式 | 探测方式 | URL 生成 |
|------|----------|----------|
| `query_param` | page/Page/p/pg/pn/page_num/pageNum/page_number 等参数 | 按 maxPagesPerGroup=100 生成 |
| `path_based` | `/base/page/2/` 路径模式 | 路径递增（同样受 100 页上限） |
| `offset/limit` | offset/skip/start 步长参数 | 按步长生成 |
| `cursor/seek/token` | 非顺序游标参数 | **游标兜底**：仅保留已有链接，不自动生成 |

浏览器/rodbackend 的 API 发现层（`api_discovery.go`）另有独立的分页 kind 分类，共 **5 种**：`query_param` / `offset_limit` / `cursor` / `path_based` / `none`（`detectPaginationKind` 按 URL 参数与路径结构判定，标注在 `DiscoveredAPI.PaginationKind`）。其中 `query_param` 与 `offset_limit` 可经 `GeneratePaginationURLs` 续抓后续页，`cursor`/`path_based` 仅作标注。

### 3.5 断点续抓与去重

| 组件 | 机制 |
|------|------|
| `frontier.go` | `offer/markVisited/isVisited + load/save`（原子写 JSON，存 `_wukong/state.json`） |
| `dedup.go` | `ContentDeduper.TryDedup`: SHA-256 → 命中 `os.Link` 硬链接省磁盘 |
| `cache.go` | `CloneCache`: ETag/Last-Modified 增量，批量检查 10 并发 + rate.Limiter(5/s) |

### 3.6 ZIM 打包（`pack/packer.go` + `pkg/zim/`）

```
packZIM(cloneDir) → zimFile
  │
  ├─ 识别克隆布局（pages/ + assets/）
  ├─ filepath.Walk 全量读入 → AddArticle
  ├─ 自动加目录 index + .html 双向重定向
  ├─ 生成 Content Index、图标、Kiwix 元数据
  └─ Packer (zim.go):
      ├─ 增量打包（cluster 按未压缩 SHA-256 缓存）
      ├─ zstd 压缩
      ├─ 流式写出
      └─ 尾部 MD5 校验（ZIM v6, Kiwix 兼容）
```

**ZIM v6 文件格式细节**（`pkg/zim/format.go`，纯 Go 实现，Kiwix 兼容）：

- **Header（固定 80 字节，小端序）**：

| 偏移 | 字段 | 说明 |
|------|------|------|
| 0–3 | Magic | `0x5a 0x49 0x4d 0x04`（"ZIM\x04"） |
| 4–5 / 6–7 | MajorVersion / MinorVersion | 6 / 0 |
| 8–23 | UUID | 16 字节归档标识 |
| 24–27 / 28–31 | ArticleCount / ClusterCount | 文章/集群总数 |
| 32 / 40 / 48 / 56 | URLPtrPos / TitlePtrPos / ClusterPtrPos / MimeListPos | 各指针表文件偏移（uint64） |
| 64 / 68 | MainPage / LayoutPage | 文章索引，`0xFFFFFFFF` 表示无 |
| 72–79 | ChecksumPos | 尾部 MD5 偏移（uint64） |

- **文件布局**：Header → MIME List → URL Ptr → Title Ptr → Cluster Ptr → Articles → Clusters → MD5（16 字节）
- **目录项**：每项固定头 16 字节（`articleHeaderSize`）；ArticleType 四类——Redirect(0) / LinkFree(1) / LinkTarget(2) / Article(3)；MIME 槽位哨兵 `0xffff`(redirect) / `0xfffe`(linkTarget) / `0xfffd`(deleted)，redirect 复用 cluster 槽存放目标 URL 索引
- **CompressionType**：None(1，存储) / Zstd(5)；cluster info 字节 bit4（`extendedFlag=0x10`）置位表示集群偏移为 uint64
- **命名空间**：Content('C') / Metadata('M') / WellKnown('W')
- **集群（cluster）构建**（`zim.go:650`）：`maxClusterSize = 2 MiB`/集群，文本与二进制 MIME 分离成不同集群；**增量缓存**——压缩前对未压缩集群计算 SHA-256，命中即复用已压缩字节跳过 zstd；`computeUUID` 基于全部文章内容的 MD5 派生确定性 UUID，保证可重现构建
- **读取器**（`reader.go`）：`Get(namespace, url)` 对 URL 排序目录做**二分搜索**；`blobAtIndex` 跟随重定向链最多 `maxRedirectHops = 16` 跳；集群解压结果按 cluster 索引缓存
- **主页选择**（`findMainPage`）：root `index.html` > `index` > `main` > 首个 HTML 文章；深层页面不自动选为主页，且主页必须是内容文章（非重定向，Kiwix 要求）

> `internal/apps/pack/packer.go` 支持多种输出格式：HTML（目录复制）、ZIM（路径重写 stripPrefix + 目录索引重定向 + 丰富元数据 + 48×48 favicon）、Binary（自包含可执行文件，内嵌 `---WUKONG_ZIM_BEGIN:{size}:...---` 标记）、App（macOS .app / Windows .exe / Linux AppDir）。

---

## 4. 搜索管线

### 4.1 聚合搜索链（`internal/extension/builtin/aggregate_search.go`）

```
aggregate_search(query) → []searchResult
  │
  ├─ 并行查询 6 个来源:
  │   ├─ DuckDuckGo (Instant Answer JSON API, 无需 key)
  │   ├─ SearXNG (自建实例 /search?format=json, 可选 key)
  │   ├─ Tavily (POST api.tavily.com/search, 必填 key)
  │   ├─ Google (customsearch/v1, 必填 key+CSEID)
  │   ├─ Bing (v7.0/search + Ocp-Apim-Subscription-Key, 必填 key)
  │   └─ CortexStore (本地知识库, cortex://)
  │
  ├─ URL 去重
  ├─ 取 Top 20 结果
  │
  ├─ 内容抓取 (fetchAndConvertPage, max 3):
  │   ├─ 浏览器自动化 (ExtractText, JS 渲染 + 反检测)
  │   ├─ 本地 Readability (HTTP GET + 正文提取 + Markdown)
  │   └─ 简单 HTTP GET (兜底)
  │
  └─ 全部 API 失败 → 浏览器搜索兜底 (searchViaBrowser)
```

### 4.2 搜索提供商对比

| 提供商 | API 端点 | 需要 key? | HTTP 客户端 | 限制 |
|--------|----------|-----------|------------|------|
| **DuckDuckGo** | Instant Answer JSON | 否 | 共享 `searchHTTPClient()`，请求级 15s | — |
| **SearXNG** | `/search?format=json` | 可选 | 共享 `searchHTTPClient()`，请求级 15s | — |
| **Tavily** | `api.tavily.com/search` | 必填 | 共享 `searchHTTPClient()`，请求级 20s | — |
| **Google** | `customsearch/v1` | 必填 | 共享 `searchHTTPClient()`，请求级 15s | — |
| **Bing** | `v7.0/search` | 必填 | 共享 `searchHTTPClient()`，请求级 15s | — |

> **注意**：`searchHTTPClient()`（`aggregate_search.go`，进程级 `sync.Once` 单例）返回所有搜索后端与页面抓取共用的一个 `httpclient.Client`——单一 `http.Transport`（连接池共享）+ 单一限流器（10/s 全局，burst 20）+ `TLSFingerprint: true`（utls Chrome 指纹）+ `MaxRetries: 3`。client `Timeout` 为上限 30s（fetch 预算），更短的后端预算按请求经 `DoWithTimeout` 设置（原为 6 个独立客户端各建 Transport，连接池分裂——2026-08-23 已合并）。

### 4.3 浏览器搜索兜底（`searchViaBrowser`）

当所有 API 后端失败时，通过浏览器自动化直接访问搜索引擎页面：

```
searchViaBrowser(ctx, query)
  │
  └─ 按优先级尝试:
      Bing → Baidu → WeChat(搜狗) → Zhihu → DuckDuckGo → Google
      │
      └─ 浏览器导航到搜索 URL → 解析 HTML 结果页
         （配合多种 HTML 解析器提取标题/URL/摘要）
```

### 4.4 页面内容三级抓取

```
fetchAndConvertPage(url) → (title, text, markdown)
  │
  ├─ Level 1: 浏览器自动化
  │   browser.ExtractText → 完整文本
  │   browser.ExtractHTML → Markdown 转换
  │
  ├─ Level 2: 本地 Readability
  │   HTTP GET → 正文提取算法 → Markdown
  │
  └─ Level 3: 简单 HTTP GET (兜底)
      原始 HTML → 基础文本提取
```

---

## 5. 搜索调优引擎

### 5.1 SearchGenome — 12 参数策略基因组（`internal/search/genome.go`）

```go
type SearchGenome struct {
    RecallMode          string  // lexical / vector / hybrid
    DenseWeight         float64 // 语义权重 [0,1]
    TextWeight          float64 // 关键词权重 [0,1]（与 DenseWeight 归一化）
    KeywordMatchPercent float64 // 关键词最低匹配比例 [0,1]
    MaxRetrievedNum     int     // 最终 TopK
    FTS5PoolSize        int     // FTS5 召回候选池大小
    FusionMethod        string  // weighted / rrf
    RRFK                float64 // RRF 平滑常数（默认 60）
    RerankerEnabled     bool    // Cross-Encoder 重排
    RerankerTopN        int     // 重排 Top-N
    MMREnabled          bool    // MMR 多样性
    MMRLambda           float64 // MMR 权衡 [0,1]: 1=相关性 0=多样性
}
```

### 5.2 RobustScore 复合评分（`internal/search/metrics.go`）

```
RobustScore = NDCG@20 + α×MRR@10
            - β×zero_result_rate
            - γ×latency_penalty（超出预算部分）
            - δ×query_type_variance

默认权重: α=0.1, β=0.5, γ=0.001, δ=0.1
```

### 5.3 Optimizer SPA 启发式（`internal/search/tune/optimizer.go`）

灵感来自 volcengine/SearchCLI 的 SPA（Strategy Population Annealing）：

```
Optimizer.Run(baseline, config) → OptimizerResult
  │
  ├─ 1. 初始种群生成 generateInitialPopulation:
  │      ├─ baseline 本身
  │      ├─ KeywordOnly 边界 (DenseWeight=0, TextWeight=1)
  │      ├─ SemanticOnly 边界 (DenseWeight=1, TextWeight=0)
  │      ├─ DenseWeight 粗网格 (0.25, 0.5, 0.75)
  │      ├─ KeywordMatchPercent 变体 (0.3)
  │      └─ 候选大小变体 (PoolSize=100/TopK=20, PoolSize=30/TopK=5)
  │
  ├─ 2. 迭代进化（默认 3 轮）:
  │      for iteration in range(MaxIterations):
  │        ├─ 评估当前种群（调用 Searcher.SearchWithGenome）
  │        ├─ SelectElites → 保留精英
  │        ├─ crossover: 交叉两个父代的参数
  │        ├─ mutate: 随机扰动单个参数
  │        ├─ moveTowards: 向全局最优微调
  │        └─ 随机邻域填充（退火: AnnealingStart=0.8 → End=0.2）
  │
  └─ 3. 多视角精英选择 SelectElites:
         ├─ GlobalBest: RobustScore 最高
         ├─ StableBest: NDCG 方差最低（最一致）
         ├─ LowLatencyBest: 上半区中延迟最低
         ├─ BaselineImprover: 优于基线
         └─ Diversity: 确保参数空间覆盖
```

### 5.4 多保真度评估 3 阶段（`internal/search/tune/multifidelity.go`）

大幅降低 LLM Judge 成本——通过提前淘汰劣质策略：

```
RunMultiFidelity(strategies, cases) → MultiFidelityResult
  │
  ├─ Phase 1: Fast Pass (FidelityFast)
  │   ├─ 20% 查询子集
  │   ├─ silver 标签（源条目召回，无需 LLM）
  │   └─ 淘汰底部 40% 策略
  │
  ├─ Phase 2: Middle Pass (FidelityMiddle)
  │   ├─ 50% 查询子集
  │   ├─ 部分 LLM Judge
  │   └─ 再淘汰 30% 策略
  │
  └─ Phase 3: Confirm Pass (FidelityConfirm)
      ├─ 100% 查询全集
      ├─ 全量 LLM Judge（4 点量表: 0=无关, 3=高度相关）
      │   MaxTokens=8, Temperature=0.0, 15s 超时
      └─ 高置信度最终选择
```

### 5.5 AutoTuneService 闭环

```
Plan → Propose → Run → Report → Apply
                                │
                                └─ 安全边界: Apply 永不直接修改 live config
                                （dry-run → confirm → candidate slot）
```

---

## 6. 垂直搜索路由

### 6.1 IntentDetector（`internal/search/vertical/intent.go`）

**纯正则零延迟**意图检测器——无需 ML，保证确定性和零开销：

```go
type IntentDetector struct {
    academic   []*regexp.Regexp   // paper/arxiv/research/论文
    code       []*regexp.Regexp   // code/github/repo/snippet/代码
    encyclo    []*regexp.Regexp   // what is/who was/define/什么是
    discussion []*regexp.Regexp   // reddit/opinion/discussion/thread
}
```

5 种意图：

| Intent | 目标 | 示例查询 |
|--------|------|----------|
| `IntentGeneral` | 无匹配 → 本地知识库 | "hello world" |
| `IntentAcademic` | arXiv | "latest paper on transformer" |
| `IntentCode` | GitHub | "go http client example" |
| `IntentEncyclopedia` | Wikipedia | "what is quantum entanglement" |
| `IntentDiscussion` | Reddit | "reddit discussion on rust vs go" |

### 6.2 4 个垂直后端（`internal/search/vertical/backends.go`）

| 后端 | API | 响应格式 | 评分 |
|------|-----|----------|------|
| **arXiv** | Atom XML API | `<entry>` 解析 | 1/(i+1) |
| **GitHub** | JSON API (`api.github.com/search/repositories`) | JSON | 按 stars 归一化 |
| **Wikipedia** | OpenSearch API | JSON | 1/(i+1) |
| **Reddit** | `reddit.com/search.json` | JSON | 1/(i+1) |

### 6.3 MergeMode（`internal/search/vertical/router.go`）

垂直结果与本地检索结果的合并模式：

```go
const (
    MergePrepend = "prepend" // 垂直优先，本地补充（默认）
    MergeAppend  = "append"  // 本地优先，垂直补充
    MergeReplace = "replace" // 仅垂直结果
)
```

`mergeVertical` 实现（`internal/cortex/store.go`）：

```
mergeVertical(ctx, query, userID, limit, vResults)
  │
  ├─ mode == replace → 仅返回垂直结果
  │
  ├─ mode == prepend → 垂直结果 + 本地结果（混合检索）
  │
  ├─ mode == append → 本地结果 + 垂直结果
  │
  └─ 本地结果获取:
      ├─ hybrid → searchHybridCortex
      ├─ vector → searchCortex
      └─ default → lexical.search (FTS5)
```

---

## 7. 语义分块

### 7.1 Chunker 架构（`internal/search/chunking/chunking.go`）

```go
type Chunker struct {
    MaxSize int // 最大 rune 数/块（默认 1200）
    Overlap int // 相邻块重叠 rune 数（默认 200）
    MinSize int // 小于此值的块合并到邻居（默认 100）
}

type Chunk struct {
    Text      string // 块内容
    Index     int    // 0 基位置
    StartChar int    // 原文档中的 rune 偏移
    EndChar   int    // 排除性 rune 偏移
}
```

### 7.2 五步分块算法

```
Chunk(text) → []Chunk
  │
  ├─ Step 1: splitIntoSegments — 自然边界分割
  │   ├─ 按段落分割（双换行 \n\n）
  │   ├─ 段落 > MaxSize → 按句子分割
  │   │   句子结束符: . ! ? 。 ！ ？
  │   └─ 句子 > MaxSize → 按词分割（强制截断）
  │
  ├─ Step 2: packSegments — 贪婪打包
  │   累积段落直到达到 MaxSize
  │
  ├─ Step 3: applyOverlap — 重叠应用
  │   从前一 chunk 尾部取 Overlap 个 rune 预置到下一 chunk
  │
  ├─ Step 4: mergeSmall — 合并小 chunk
  │   rune 数 < MinSize 的 chunk 合并到前一邻居
  │
  └─ Step 5: 赋予最终 Index + 偏移量
```

### 7.3 EstimateTokens 估算（`chunking.go:381`）

```go
func EstimateTokens(text string) int {
    // ASCII: ~4 字符/token
    // CJK:   ~1.5 字符/token（即 2/3 token/字符）
    tokens := asciiCount/4 + cjkCount*2/3
    if tokens < 1 { tokens = 1 }
    return tokens
}
```

> 中英文混合文本的 token 估算通过逐字符分类（ASCII vs 非 ASCII）加权计算，无需 tokenizer。

### 7.4 配置

```yaml
cortex:
  chunking:
    enabled: true      # 默认启用
    max_size: 1200     # max runes/chunk
    overlap: 200       # overlap runes
    min_size: 100      # merge threshold
```

---

## 8. 错误信号分类

### 8.1 七类分类体系（`internal/errsignal/errsignal.go`）

```go
const (
    ClassUnknown      ErrorClass = iota // 默认：无法分类
    ClassTransient                      // 网络超时/连接重置/5xx → 指数退避重试
    ClassRateLimited                    // 429/503+Retry-After → 等待后重试
    ClassAuthRequired                   // 401/403 → 升级凭据或跳过
    ClassBotDetection                   // Cloudflare/CAPTCHA/WAF → 升级 Stealth
    ClassPermanent                      // 404/410/DNS failure → 跳过，不重试
    ClassInvalid                        // 400/422 → 跳过并记录
)
```

### 8.2 信号匹配逻辑（`Classify`）

```
Classify(err, statusCode) → Classification
  │
  ├─ BotDetection 信号:
  │   消息含 cloudflare/turnstile/captcha/challenge/blocked/cf-ray/cf-mitigated
  │   或 403 + cloudflare → ShouldEscalate=true
  │
  ├─ RateLimited 信号:
  │   429 或 503+rate/limit/retry → extractRetryAfter(err)
  │
  ├─ Permanent 信号:
  │   404 / 410 / "no such host" / "dns" / "name resolution"
  │
  ├─ AuthRequired 信号:
  │   401 / 403
  │
  ├─ Invalid 信号:
  │   400 / 422
  │
  ├─ Transient 信号:
  │   ≥500 或 timeout/reset/refused/closed/EOF/no route/unreachable
  │
  └─ Unknown（有 statusCode 但不匹配 → 不重试；无 statusCode → 默认重试 1 次）
```

### 8.3 重试延迟策略（`RetryDelay`）

```
RetryDelay(classification, attempt) → Duration
  │
  ├─ RateLimited:
  │   ├─ 有 RetryAfter → 返回 RetryAfter
  │   └─ 无 → 5×(1<<attempt): 5s, 10s, 20s
  │
  ├─ Transient / Unknown:
  │   指数退避: 2^attempt 秒
  │   1s → 2s → 4s → 8s → ... 上限 30s
  │
  └─ 其他 → 0（不重试）
```

### 8.4 辅助函数

```go
func ShouldEscalate(c Classification) bool  // ClassBotDetection → true（触发反爬升级）
func ShouldSkip(c Classification) bool      // Permanent/Invalid/AuthRequired → true（跳过不重试）
func extractRetryAfter(err error) Duration  // 从错误消息解析 "retry-after: N" 或 "retry after N seconds"
```

---

## 9. HTTP 客户端

### 9.1 架构（`pkg/httpclient/httpclient.go`）

```go
type Client struct {
    *http.Client                    // 内嵌标准库
    opts      Options
    userAgent string
    metrics   *metrics              // 请求/重试/成功率/延迟/错误分类
    dnsCache  *DNSCache             // 可选 DNS 缓存
    limiter   *RateLimiter          // 可选速率限制
}
```

### 9.2 DNS 双保险（`dialWithDNSFallback`）

```
dialWithDNSFallback(ctx, network, addr)
  │
  ├─ 1. 系统 DNS 解析
  │   成功 → 返回连接
  │   非 DNS 错误 → 返回错误
  │
  ├─ 2. 公共 DNS 回退（仅前 2 个服务器，3s 超时/个，总 ≤6s）
  │   8.8.8.8:53 (Google)     ← 尝试 1
  │   8.8.4.4:53 (Google)
  │   1.1.1.1:53 (Cloudflare) ← 尝试 2
  │   1.0.0.1:53 (Cloudflare)
  │   9.9.9.9:53 (Quad9)
  │   │
  │   └─ 用自定义 Resolver(PreferGo=true, Dial→指定 DNS)
  │      解析成功 → DialContext(IP:port) → 返回连接
  │
  └─ 3. 全部失败 → 返回原始错误
```

> 注释说明：DNS 被封的网络环境（ISP 级 UDP 53 封锁）中，试 5 个服务器浪费 25 秒无益，因此只试前 2 个。

### 9.3 可选 DNS 缓存（`DNSCache`）

```go
type DNSCache struct {
    entries  map[string]*dnsEntry  // host → {addrs, expireAt}
    ttl      time.Duration         // 默认 5 分钟
}
```

`WrapDialContext` 包装 `dialWithDNSFallback`：先查缓存 → 未过期直接用 → IP 变化时失效。

### 9.4 TLS 指纹伪造（utls）

```go
// Options.TLSFingerprint=true 时启用
transport.DialTLSContext = func(ctx, network, addr) {
    conn := dialWithDNSFallback(ctx, network, addr)
    uconn := utls.UClient(conn, &utls.Config{
        ServerName:         host,
        InsecureSkipVerify: opts.InsecureSkipVerify,
    }, utls.HelloChrome_Auto)
    uconn.HandshakeContext(ctx)
    return uconn
}
```

> **仅非代理路径启用**：`ProxyURL==""` && `ProxyPool` 为空时。代理路径的 utls 由 `internal/browser/proxy_pool.go` 独立处理。

**utls 使用位置全览**：

| 位置 | 场景 |
|------|------|
| `pkg/httpclient/httpclient.go` | 主 HTTP 客户端（`TLSFingerprint=true`） |
| `internal/browser/proxy_pool.go:254` | 反爬代理池客户端 |
| `internal/browser/antibot/prober/http_client.go:32` | WAF 探针客户端 |
| `internal/extension/builtin/aggregate_search.go` | 搜索引擎客户端（共享 `searchHTTPClient()` 单例） |

### 9.5 速率限制（`ratelimit.go`）

```go
type RateLimiter struct {
    tokens       float64           // 当前令牌
    maxTokens    float64           // 桶容量（burst）
    refillRate   float64           // 每秒补充速率
    blockedHosts map[string]time.Time // 熔断的主机 → 30s 封禁
}
```

- **令牌桶**：按 host 独立限流，支持突发（burst）
- **BlockHost**：连续失败后临时封禁主机（默认 30s）
- **RoundTripper 包装**：`limiter.RoundTripper(transport)` 在传输层拦截

### 9.6 自动浏览器头注入

`Do()` 方法自动补充默认请求头：

```
User-Agent                    // Chrome UA
Accept                        // text/html,...
Accept-Language               // en-US,en;q=0.9
Accept-Encoding               // gzip
sec-ch-ua                     // Chrome 一致性
sec-ch-ua-mobile
sec-ch-ua-platform
sec-fetch-mode / dest / site / user  // 完整 Sec-Fetch-* 集
Connection                    // keep-alive
```

### 9.7 代理管理

| 模式 | 行为 |
|------|------|
| `ProxyURL` | 单代理 URL，`http.ProxyURL` |
| `ProxyPool` | 取**第一个**（不做健康检查/轮询） |
| 环境变量 | `http.ProxyFromEnvironment` |

> `ProxyPool` 仅取第一个是已知限制——完整的代理轮询和健康检查在 `internal/browser/proxy_pool.go` 的 `SmartProxyPool` 中实现（30s TCP 健康检查、3 次失败禁用、`cf_clearance` 记忆）。

### 9.8 重试与错误分类

```
shouldRetry(err, statusCode) → bool          [网络错误路径]
  │
  ├─ 错误串匹配: connection refused/reset/timeout/TLS → 重试
  └─ 其他 → 不重试

retryStatusDelay(resp, attempt) → (delay, ok)   [响应状态路径]
  │
  ├─ 429 与 5xx → 重试
  └─ 其他 4xx → 不重试

退避: max(retryBackoff, Retry-After + jitter)
  - retryBackoff: base/2 + rand[0, base/2)，base = (attempt+1) × RetryDelay
    ← 线性退避 + equal jitter（防惊群；期望延迟 75%×base，下界 base/2）
  - Retry-After: 解析秒数/HTTP-date，另加 10%（上限 1s）抖动防对齐；
    超过 client Timeout 时不重试、直接把 429/503 响应交还调用方
```

> httpclient 的重试已于 2026-08-23 完成 equal jitter + Retry-After 消费（`retryBackoff` / `retryStatusDelay` / `parseRetryAfter`，429 原先不重试、现纳入）。`internal/errsignal.RetryDelay` 保持未接入（零调用方、硬编码 1s 起步会破坏 `Options.RetryDelay` 配置契约，且 pkg 层不宜反向依赖 internal）。

### 9.9 Metrics

按 host/URL 统计：

| 指标 | 说明 |
|------|------|
| `totalRequests` | 总请求数 |
| `totalRetries` | 总重试数 |
| `successCount` / `failureCount` | 成功/失败计数 |
| `totalLatency` | 累计延迟 |
| `networkErrors` / `timeoutErrors` / `tlsErrors` / `serverErrors` / `clientErrors` | 错误分类计数 |
| `requestCountByURL` | 按 URL 的请求计数 |

---

## 10. 技术特点综合评估

### 10.1 优势

| 维度 | 表现 |
|------|------|
| **容灾能力** | 浏览器渲染→HTTP 回退→Wayback 三级容灾（页面/资产均接入） + `errsignal` 信号化分类 |
| **反爬自适应** | 5 级升级体系（None→Flags→Stealth→Aggressive→Backoff）+ Preflight/Probe 预判，Aggressive 轮换 UA+TLS |
| **速度优化** | 平台 API 快捷通道（Reddit/HN/GitHub/Wikipedia/arXiv）10-50× 提速 |
| **搜索调优** | SPA 遗传算法 + 多保真度 3 阶段评估，大幅降低 LLM Judge 成本 |
| **DNS 容错** | 系统 DNS → 公共 DNS 回退 → DNS 缓存三层保护 |
| **DOM 质量** | 单遍 DOM 重写 + honeypot 识别 + lazy-load 解析 + 3 种分页模式 + 游标兜底 |
| **稳定性** | 断点续抓 + SHA-256 硬链接去重 + ETag 增量缓存 |
| **资产收集** | rod 页内 JS 分批并发（chunk=10 Promise.all），每资产独立超时 |

### 10.2 短板

| 维度 | 现状 |
|------|------|
| **HTTP 重试** | 线性退避 + equal jitter + Retry-After 消费（已修，见 §9.8）；`errsignal.RetryDelay` 仍未接入（见 P0-1 说明） |
| **TLS 校验** | ~~多处固定 `InsecureSkipVerify: true` + 浏览器 `ignore-certificate-errors`~~ 已收敛：HTTP 侧本就是严格默认 + 显式 opt-out（`insecure_tls`/`InsecureSkipVerify`/RootCAs bundle），2026-08-24 移除 chromedp 池基础 flag 列表中无条件的 `ignore-certificate-errors`/`ignore-ssl-errors`（此前 InsecureTLS 条件开关形同虚设，与 rod 后端不一致），双后端现统一为严格默认 + 显式 opt-out。全链 Debug 级埋点：`errsignal.Classify/RetryDelay`（错误分类/重试裁决/退避分支）、httpclient 构造期（TLS 模式 + client/dial/handshake 超时旋钮）、cloner TLS 策略与 CA 加载结果、双后端池 TLS 模式 |
| ~~代码重复~~ | 已由 P2-1 六阶段收敛（renderkit 注入 JS/内容类型 + Dispatcher 调度骨架 + 生命周期 + 优先级/老化 + 依赖图重试 + 全局渲染预算）；剩余 rod 独有下载回退链（网络跟踪/referer 缓存/5 级回退约 900 行）属 CDP 能力差异，有意保留在适配层 |
| **内存** | ZIM 打包全量读入内存；base64 转码使内存翻倍。缓解：全局渲染预算按 heap 水位收缩渲染并发（P2-1 阶段六），但 ZIM/base64 路径本身未改 |
| ~~限速模型~~ | 已修（P2-2/P2-3/P2-4）：per-host token-bucket（CrawlDelay → robots → 默认 100ms，跨 host 并行）+ 429/503 动态降速 + 白名单豁免 + IP 段惩罚传播 |

---

## 11. 优化建议

### P0（正确性）

1. **httpclient 重试消费 Retry-After** ✅ 已完成（2026-08-23）
   `pkg/httpclient/httpclient.go`：两处重试统一走 `retryStatusDelay()`——429 与 5xx 均重试；`Retry-After` 头（秒数/HTTP-date）被解析并叠加 10% 抖动（上限 1s）防并发对齐；超过 client `Timeout` 的 `Retry-After` 不重试、直接交还响应。配套 `retryBackoff`（equal jitter）与 8 个单测/集成测试。`errsignal.RetryDelay` 维持未接入（见 §9.8 说明）。

### P1（体验与效率）

2. **统一 web 工具集 httpclient** ✅ 已完成（2026-08-23）
   `aggregate_search.go` 的 6 个独立客户端（duckduckgo/searxng/google/bing 15s、tavily 20s、fetch 30s）合并为进程级单例 `searchHTTPClient()`——单一 `http.Transport` 连接池 + 单一 10/s 限流器；每后端超时改经新增的 `httpclient.Client.DoWithTimeout`（context 截止 + body 关闭释放定时器）按请求设置，语义不变。`tavily.go`/`searxng.go` 独立工具同步接入。语义变化：限流从每后端 10/s 收紧为全局 10/s（更保守、与文档原声称一致）。

### P2（架构简化）

3. **统一 chromedp/rod 双后端调度框架** ✅ 已完成（2026-08-24，六阶段）
   两套 worker 池/renderJob/RenderWithReferer/DownloadAsset 骨架逐行重复。建议提取共享接口 + 泛化调度，将 CDP 差异收敛到适配层。
   - **阶段一已完成**：`internal/browser/renderkit` 归一两后端逐字重复的注入 JS 与内容类型判断（约 150 行去重，3 个单测）；chromedp 版内容类型判断从裸比较升级为归一化匹配。
   - **阶段二已完成**：共享调度骨架 `renderkit.Dispatcher`（`RenderJob`/`Submit`/`Drain`/`Closed`）收敛两后端重复的队列/closed 守卫/双 select 取消/worker 循环/Close 排水（每侧约 -60 行，行为等价：workers×4 缓冲、ctx 取消丢弃结果、Drain 幂等且返回是否由本次调用排水——rod 的 `MustClose` 非幂等，清理只由排水者执行）；可选能力接口 `types.UARotator`（两后端）与 `types.AssetCollector`（rod：渲染时网络跟踪收集）形式化，`enhanced_cloner` 的匿名 `RotateUA` 断言改用命名接口，两后端补编译期断言。真实浏览器冒烟双绿（chromedp 1.94s / rod 1.44s），全量 -race 回归通过。
   - **阶段三已完成**：浏览器池生命周期绑定调用方 context——`New(ctx, opts)`（两后端同构）以 `lifeCtx` 派生浏览器 allocator/launcher，监听 goroutine 在 ctx 取消时自动排水并释放浏览器进程（此前任务 ctx 泄漏浏览器须等进程退出）。显式 `Close` 与自动 `Close` 经 `Drain()` 返回值单执行者守卫互斥（chromedp 的 cancel 闭包消费一次性信号量、rod 的 `MustClose` 非幂等，二次调用必死锁/panic）。工厂 `NewBackend`/`NewBackendFromConfig` 增加 ctx 转发；长生命周期组件（Controller）传 `Background` 自管 Close，任务型消费方（enhanced_cloner/downloader）传任务 ctx——取消任务即回收浏览器。附带修复 rod 既有生产 bug：无 `<title>` 页面（极简页/错误页）使 `Element("title")` 无限等待挂死渲染，现以 2s 超时限界（生命周期测试页刻意无 title 兼作回归用例）。生命周期测试 4 项全绿（chromedp 3 项无浏览器即可运行，rod 真机 1 项），全量 -race 回归通过。
   - **阶段四已完成**：基于资源优先级的动态调度——`Dispatcher` 从纯 FIFO 升级为优先级调度：`types.Priority`（Low/Normal/High，零值视作 Normal 保持旧调用语义）+ 可选能力接口 `types.PriorityRenderer`（`RenderWithPriority`，两后端实现并加编译期断言）；worker 争用时高优先级渲染插队，同级 FIFO，且每等待一个老化间隔（默认 5s）有效优先级升一级——饿死的低优先级任务最终反超新提交的普通任务，严格优先级的饥饿问题由老化动态提升消除。背压语义不变（4×workers 有界准入，经信号量通道实现保持 ctx 可取消；队列扫描 O(n) 于有界容量上取最优，规避了键值随老化变化的堆一致性陷阱）。调用点分级：种子页/Turnstile 自动解题（挑战令牌短时效）/downloader 用户请求页 = High，SPA 补救性重渲染（页面已渲染过一次的机会性质量提升）= Low，其余 = Normal。新增 3 个调度单测（插队+同级 FIFO、老化反超、满队背压+ctx 取消），renderkit 8 测试 ×5 轮 -race 稳定通过，双后端全量 -race 回归通过。
   - **阶段五已完成**：基于任务依赖图的拓扑调度——新增 `clone/taskGraph`（`Declare`/`Resolve`/`Submit`：声明式节点由外部事件 Resolve，提交式节点依赖齐后自动运行 fn；失败经 `depErr` 传给依赖方但 fn 仍运行以保证清理路径与计数释放；DFS 环检测与重复键在建边时拒绝）。反爬重试链改造为图节点：`schedulePageRetry`/`scheduleAssetRetry` 把退避窗口与重派生声明为 `backoff→retry` 依赖边，页面 worker 立即返回而非 `time.Sleep` 占住槽位度过整个冷却期（资产路径同构）。附带修复两个既有生产 bug：①页面重试经 `enqueuePageWithReferer` 被 frontier 去重静默丢弃（失败尝试已占住 seen 槽，全仓库无一处为重试清 seen——重派现直发 worker 池绕过去重，次数由 escalator `MaxRetries` 按 URL 封顶）；②ctx 取消后 `wg.Wait` 永久挂死（worker 提前 `return` 使缓冲区剩余 job 计数泄漏、DFS 栈滞留、BFS 满队逃逸路径多处泄漏）——worker 取消后持续排水释放计数、`discardPageStack` 置 `dispatcherDead` 关闭迟到压栈的 TOCTOU 窗口、`enqueuePageWithReferer` 增加 ctx 取消守卫。重试渲染并入 High 优先级（种子页同因：退避刚结束应立即渲染）。taskgraph 7 单测 ×3 轮 -race、clone ×3 轮 -race、browser 全家 -race 回归通过。
   - **阶段六已完成**：基于全局资源水位的全局调度器——此前架构是"每任务一池、池间零协调"（常驻 Controller 池 + 每个 clone/download 任务各一个 Chrome 进程池），进程内渲染并发总量 = Σ 各池 Workers，无上限也无资源信号反馈。新增 `renderkit.GlobalBudget` 进程级渲染槽位预算：所有池（双后端经共享 `Dispatcher` 骨架单点接入——worker 在运行 job 前取全局槽位、运行后归还，未安装预算时零开销直通）共享一个准入上限，进程内同时运行的渲染总数被封顶；资源水位自适应：内置 heap 监测器（默认 2s 采样 `HeapInuse` 相对 `GOMEMLIMIT` 软限制，未设置时回退 4GiB 标尺）将占用率映射为三档压力（≥70% 高压 / ≥85% 严重），压力使有效预算收缩为上限的 1/2、1/4（下限 1，不抢占已持有槽位，随在途渲染自然收敛），回落到高水位以下恢复全额。等待实现为 broadcast channel（close+重建）而非 `sync.Cond`，保证 `Acquire` 可被 ctx 取消（取消的等待者不占槽位）；预算阻塞传导为全链背压：全局槽位满 → worker 挂起不取新 job → 池内队列满 → 提交方 sem 满 → `Submit` 阻塞（ctx 可取消）→ 上游停止生产。单例经 `EnsureGlobalBudget` 幂等安装（首次调用者定容，`browser.NewBackend` 统一入口接入；config 键 `browser.global_render_slots`：0=自动 max(4, NumCPU)，负值显式禁用）。与阶段四正交：优先级决定池内"谁先跑"，全局预算决定全进程"同时跑多少"。renderkit 15 测试 ×3 轮 -race、browser 全家（含双后端真机冒烟）-race、clone -race、config 回归通过。

4. **资产按域名 token-bucket 限速** ✅ 已完成（2026-08-23）
   Per-host token-bucket 早已落地（`waitAssetRateLimit`/`hostLimiter`：CrawlDelay → robots crawl-delay → 默认 100ms/host，跨 host 并行；`300+rand(0,700)ms` 仅剩 URL 不可解析时的兜底）。本轮补齐"仅对探测出限流的 host 降速"：`RateLimiter.SlowDown`（每次 429/503 将该 host interval ×2，封顶 30s，本轮内不恢复），`processAsset` 在 `DownloadError.StatusCode` 为 429/503 时调用 `penalizeHost`（403 属 WAF 拦截、不惩罚，走浏览器回退）。

5. **IP 段动态限速惩罚传播** ✅ 已完成（2026-08-23）
   补齐 CDN 别名场景：`cdn1`/`cdn2` 常解析到同一 /24，per-host 桶各自限速但目标服务器承受叠加流量，且单 host 惩罚不会扩散到同段兄弟。实现（`clone/ip_penalty.go`）：惩罚时解析违规 host 的 IP（复用 `httpclient.DNSCache`，5min TTL）→ 按 CIDR 前缀（默认 v4 /24、v6 /64，可配）记录段级最小间隔；同段其他 host 下次 `waitAssetRateLimit` 时经 `RateLimiter.RaiseTo` 抬升到该地板（只升不降、不额外乘 2）。默认开启（`apps.clone.rate_limit_ip_segment: true`，`--no-ip-rate-limit` 关闭）；正常路径零 DNS 开销——段存储为空时 `segmentFloor` 直接返回 0 不触发解析。

### 已解决（2026-08-23 复核）

- ~~修正默认配置跳过反爬预检测~~（原 P0）：`preflightCloudflareCheck`（`enhanced_cloner.go:2850`）现在无条件调用（仅 `AntibotEnabled=false` 短路），GET + 前 8KB body 判定 Turnstile，**无论 Stealth 是否开启均执行**；检出 Turnstile 时关闭 `AntibotAutoEscalate` 避免空转。`runAntibotProbe` 仍仅在 `AntibotEnabled && !Stealth` 时执行（Stealth 已是较高级别，属合理分层）。
- ~~rod 资产收集改并发~~（原 P1）：`rodbackend/pool.go:562-659` 已改为 chunk=10 分批、每批单次 CDP Eval 内 `Promise.all` 真并发 fetch，每个 fetch 独立 `AbortController` 超时。
- ~~接入 Wayback 兜底~~（原 P1）：页面级 `FetchArchivedPage`（`enhanced_cloner.go:1068`）与资产级 `FetchArchivedAsset`（`enhanced_cloner.go:1833`）均已接入最终失败分支。

### 调优旋钮

基于分析，以下是关键性能/质量调旋钮：

```yaml
# 搜索策略调优
cortex:
  search_strategy:
    recall_mode: hybrid          # lexical / vector / hybrid
    dense_weight: 0.5            # 语义权重（0=纯关键词, 1=纯向量）
    max_retrieved_num: 10        # 最终 TopK
    fts5_pool_size: 50           # FTS5 候选池大小
    fusion_method: rrf           # weighted / rrf（尺度不变）
    rrf_k: 60                    # RRF 平滑常数
    mmr_enabled: false           # MMR 多样性
    mmr_lambda: 0.7              # 1=相关性, 0=多样性

  # 垂直搜索
  vertical_routing:
    enabled: false
    top_n: 5
    merge_mode: prepend          # prepend / append / replace

  # 语义分块
  chunking:
    enabled: true
    max_size: 1200               # runes/chunk
    overlap: 200                 # 重叠量
    min_size: 100                # 合并阈值

# 浏览器
browser:
  workers: 4                     # 并发 worker 数
  timeout: 60s                   # 渲染超时
  stealth: true                  # 反检测注入
  scroll: true                   # 滚动加载

# HTTP 客户端
httpclient:
  max_retries: 3
  retry_delay: 500ms
  enable_rate_limit: true
  rate_limit_per_second: 10
  tls_fingerprint: true          # utls Chrome 指纹
  enable_dns_cache: true
  dns_cache_ttl: 5m
```

---

## 12. 相关文档

| 文档 | 说明 |
|------|------|
| [CLONE_GUIDE.md](./CLONE_GUIDE.md) | 网站克隆技术指南 |
| [ANTIBOT_GUIDE.md](./ANTIBOT_GUIDE.md) | 反反爬技术详解 |
| [ARCHITECTURE.md](./ARCHITECTURE.md) | 系统架构全景 |
| [CONFIG.md](./CONFIG.md) | 配置手册 |

---

> **版本**: v0.3.3 | **最后更新**: 2026-08-29 | **范围**: browser / apps(clone,pack) / search(tune/vertical/chunking) / errsignal / pkg/httpclient / pkg/zim
