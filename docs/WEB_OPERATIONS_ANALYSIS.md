# Wukong Web 操作深度分析

> 基于对 `internal/browser/`、`internal/apps/clone/`、`internal/search/`、`internal/search/tune/`、`internal/search/vertical/`、`internal/search/chunking/`、`internal/errsignal/`、`pkg/httpclient/` 等代码的逐文件精读，产出本专项分析。所有函数签名、行号、数据结构均为真实源码。

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
    escalator          *antibot.Escalator  // 反爬升级器
    currentUA          *antibot.UAProfile  // 当前 UA 配置
}
```

**renderJob** 流程（以 rod 为主线，`rodbackend/pool.go:214-663`）：

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
  ├─ 7. 资产内联收集: JS fetch→arrayBuffer→btoa 串行取回
  │      限制: 单资产 10MB, 超时 5s, 每页最多 200 个
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

### 3.1 完整克隆管线（`enhanced_cloner.go:458-830`）

```
Clone(ctx, seedURL) → CloneResult
  │
  ├─ 1. 解析 seed URL
  ├─ 2. 设置 Referer / RefererOverrides（域名级覆盖）
  ├─ 3. preflightCloudflareCheck()         ← Turnstile 预检测
  │      ⚠️ 默认 Stealth=true 时被跳过（见 §11 P0）
  ├─ 4. runAntibotProbe()                  ← 多维度探测
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

### 3.2 单页面处理（`processPage`，`enhanced_cloner.go:908-1257`）

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
  └─ Layer 4: (Wayback 兜底 — 实现完整但当前未被调用)
```

### 3.4 分页支持（6 种模式）

| 模式 | 探测方式 | URL 生成 |
|------|----------|----------|
| `query_param` | page/p/Page/pn 等参数 | 按 maxPagesPerGroup=100 生成 |
| `path_based` | `/base/page/2/` 路径模式 | 路径递增 |
| `offset/limit` | 步长参数 | 按步长生成 |
| `cursor/seek/token` | 仅保留已有链接 | 不自动生成 |

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
| **DuckDuckGo** | Instant Answer JSON | 否 | `newSearchHTTPClient`（utls+限流+重试） | — |
| **SearXNG** | `/search?format=json` | 可选 | `newSearchHTTPClient` | — |
| **Tavily** | `api.tavily.com/search` | 必填 | `newSearchHTTPClient` | — |
| **Google** | `customsearch/v1` | 必填 | `newSearchHTTPClient` | — |
| **Bing** | `v7.0/search` | 必填 | `newSearchHTTPClient` | — |

> **注意**：`newSearchHTTPClient`（`aggregate_search.go:97-113`）启用了 `TLSFingerprint: true`（utls）、`EnableRateLimit: true`（10/s）、`MaxRetries: 3`——所有搜索提供商共享一致的 Chrome 指纹与限流。

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

### 5.1 SearchGenome — 13 参数策略基因组（`internal/search/genome.go`）

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

### 7.3 EstimateTokens 估算（`chunking.go:375-400`）

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
| `internal/extension/builtin/aggregate_search.go` | 搜索引擎客户端（`newSearchHTTPClient`） |

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
shouldRetry(err, statusCode) → bool
  │
  ├─ 错误串匹配: connection refused/reset/timeout/TLS → 重试
  ├─ 5xx → 重试
  └─ 其他 → 不重试

退避: (attempt+1) × RetryDelay    ← 线性退避（无抖动）
```

> ⚠️ 主 httpclient 的重试是**线性退避**且**不消费 Retry-After**——`errsignal.RetryDelay` 提供了更优的指数退避+Retry-After 实现，但主 httpclient 尚未接入。

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
| **容灾能力** | 浏览器渲染→HTTP 回退→Wayback 三级容灾 + `errsignal` 信号化分类 |
| **反爬自适应** | Stealth/Preflight/Antibot 分级升级 → Aggressive 轮换 UA+TLS |
| **速度优化** | 平台 API 快捷通道（Reddit/HN/GitHub/Wikipedia/arXiv）10-50× 提速 |
| **搜索调优** | SPA 遗传算法 + 多保真度 3 阶段评估，大幅降低 LLM Judge 成本 |
| **DNS 容错** | 系统 DNS → 公共 DNS 回退 → DNS 缓存三层保护 |
| **DOM 质量** | 单遍 DOM 重写 + honeypot 识别 + lazy-load 解析 + 6 种分页模式 |
| **稳定性** | 断点续抓 + SHA-256 硬链接去重 + ETag 增量缓存 |

### 10.2 短板

| 维度 | 现状 |
|------|------|
| **HTTP 重试** | 线性退避、无抖动、不消费 `Retry-After`（`errsignal` 已有更好实现但未接入） |
| **TLS 校验** | 多处固定 `InsecureSkipVerify: true` + 浏览器 `ignore-certificate-errors` |
| **代码重复** | chromedp/rod 双后端的 worker 池/renderJob/DownloadAsset 骨架高度同构 |
| **资源收集** | rod 单页串行 btoa 收集最多 200 资产（每个 5s），最坏数十分钟 |
| **Wayback 兜底** | `ArchiveFallback.FetchArchivedAsset` 实现完整但从未被调用 |
| **内存** | ZIM 打包全量读入内存；base64 转码使内存翻倍 |
| **限速模型** | 资产每资产统一 300-1000ms 随机延迟，而非按域名限速 |

---

## 11. 优化建议

### P0（正确性）

1. **修正默认配置跳过反爬预检测**
   `enhanced_cloner.go:2745` `if !opts.AntibotEnabled || opts.Stealth { return }`——默认 `Stealth=true` 导致 Preflight 与 Probe 均不执行。建议 Stealth=true 时仍执行**轻量 Preflight**（HEAD/前 8KB 判定 Turnstile）。

2. **httpclient 重试升级为指数退避 + Retry-After**
   当前线性退避 `(attempt+1)*RetryDelay` 且不消费 `Retry-After`。建议接入已实现的 `errsignal.RetryDelay`（指数退避 + jitter + Retry-After 解析）。

### P1（体验与效率）

3. **统一 web 工具集 httpclient**
   当前 `newSearchHTTPClient` 已统一搜索提供商客户端，但各提供商仍各自创建实例。建议共享单一客户端避免连接池分裂。

4. **rod 资产收集改并发**
   `rodbackend/pool.go:479-550` 串行 btoa 回传 200 资产（最坏 200×5s）。建议并发（限 5）或用 `Network.getResponseBody` 流式读取。

5. **接入 Wayback 资产兜底**
   `ArchiveFallback.FetchArchivedAsset` 完整实现但从未被调用。建议在资产下载最终失败分支接入。

### P2（架构简化）

6. **统一 chromedp/rod 双后端调度框架**
   两套 worker 池/renderJob/RenderWithReferer/DownloadAsset 骨架逐行重复。建议提取共享接口 + 泛化调度，将 CDP 差异收敛到适配层。

7. **资产按域名 token-bucket 限速**
   当前每资产统一 300+rand(0,700)ms 延迟。建议改为按 host 限速（`pkg/httpclient` RateLimiter 已支持），仅对探测出限流的 host 降速。

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
| [TECHNICAL_IMPLEMENTATION.md](./TECHNICAL_IMPLEMENTATION.md) | 技术实现详解 |
| [CONFIG.md](./CONFIG.md) | 配置手册 |

---

> **版本**: v0.2.0 | **最后更新**: 2026-08-11 | **范围**: browser / apps(clone) / search(tune/vertical/chunking) / errsignal / pkg/httpclient
