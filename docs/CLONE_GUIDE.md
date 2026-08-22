# 网站克隆引擎与 ZIM 打包技术指南

> 克隆引擎: EnhancedCloner (3173 行) | 单遍 DOM 遍历重写 | 6 种分页模式
> 资源下载: 代理池轮换 + cf_clearance 注入 | 内容去重: SHA-256 + 硬链接
> 断点续抓: Frontier 原子写入 | ZIM: Kiwix v6 格式 | 归档回退: Wayback Machine

---

## 目录

1. [引擎架构总览](#1-引擎架构总览)
2. [EnhancedCloner 主引擎](#2-enhancedcloner-主引擎)
3. [克隆流水线详解](#3-克隆流水线详解)
4. [单遍 DOM 遍历重写](#4-单遍-dom-遍历重写)
5. [分页检测与生成](#5-分页检测与生成)
6. [URL 规范化与本地路径映射 (urlx)](#6-url-规范化与本地路径映射-urlx)
7. [Frontier 爬取队列与原子持久化](#7-frontier-爬取队列与原子持久化)
8. [资源下载策略 (AssetDownloader)](#8-资源下载策略-assetdownloader)
9. [HTML 重写与蜜罐检测 (rewrite)](#9-html-重写与蜜罐检测-rewrite)
10. [CSS URL 重写 (css)](#10-css-url-重写-css)
11. [内容去重引擎 (ContentDeduper)](#11-内容去重引擎-contentdeduper)
12. [条件缓存 (CloneCache)](#12-条件缓存-clonecache)
13. [robots.txt 与速率限制](#13-robotstxt-与速率限制)
14. [会话管理与 Cookie 持久化](#14-会话管理与-cookie-持久化)
15. [平台 API 拦截 (platform_api)](#15-平台-api-拦截-platform_api)
16. [归档回退 (archive_fallback)](#16-归档回退-archive_fallback)
17. [ZIM 打包器 (Packer)](#17-zim-打包器-packer)
18. [ZIM 文件格式 (pkg/zim)](#18-zim-文件格式-pkgzim)
19. [配置参考](#19-配置参考)
20. [常见问题](#20-常见问题)

---

## 1. 引擎架构总览

### 1.1 核心子系统协同图

```
                        ┌─────────────────────────────────────┐
                        │        EnhancedCloner (主引擎)       │
                        │     internal/apps/clone/             │
                        │     enhanced_cloner.go (3173 行)     │
                        └──────────────┬──────────────────────┘
                                       │
          ┌────────────┬──────────────┼──────────────┬─────────────┐
          ▼            ▼              ▼              ▼             ▼
   ┌──────────┐ ┌──────────┐  ┌────────────┐ ┌───────────┐ ┌───────────┐
   │ Frontier │ │ Browser  │  │   Asset    │ │  Deduper  │ │  Cache    │
   │ 爬取队列  │ │   Pool   │  │ Downloader │ │  去重引擎  │ │ ETag 缓存 │
   │          │ │ Rod/CDP  │  │            │ │           │ │           │
   └────┬─────┘ └────┬─────┘  └─────┬──────┘ └─────┬─────┘ └─────┬─────┘
        │            │              │              │             │
        │     ┌──────┴──────┐       │              │             │
        │     │             │       │              │             │
        ▼     ▼             ▼       ▼              ▼             ▼
   ┌─────────────────────────────────────────────────────────────────┐
   │                     辅助子系统                                   │
   ├──────────┬──────────┬──────────┬──────────┬───────────────────┤
   │  robots  │ rate     │ urlx     │ rewrite  │  archive_fallback │
   │  规则检查 │ limiter  │ URL 工具 │ DOM 重写 │  Wayback 回退      │
   └──────────┴──────────┴──────────┴──────────┴───────────────────┘
        │
        ▼
   ┌─────────────────────────────────────────────────────────────────┐
   │                     打包器 (Packer)                              │
   │              internal/apps/pack/packer.go                        │
   ├──────────┬──────────┬──────────┬───────────────────────────────┤
   │   HTML   │   ZIM    │ Binary   │           App                 │
   │ 目录复制  │ Kiwix    │ 自包含   │     macOS/Win/Linux           │
   └──────────┴──────────┴──────────┴───────────────────────────────┘
```

### 1.2 子系统职责对照表

| 子系统 | 源文件 | 核心职责 |
|--------|--------|---------|
| **EnhancedCloner** | `enhanced_cloner.go` (3173 行) | 主引擎，协调所有子系统 |
| **CloneSession** | `session.go` | Netscape cookie 持久化，浏览器实例 cookie 加载 |
| **Frontier** | `frontier.go` | 爬取队列，seen/visited 去重，JSON 原子持久化 |
| **urlx** | `urlx.go` | URL 规范化，LocalPath() 确定性映射，分页后缀方案 |
| **AssetDownloader** | `asset.go` | 代理池轮换，Referer 覆盖，gzip/deflate 解压 |
| **ContentDeduper** | `dedup.go` | SHA-256 哈希去重，硬链接节省磁盘空间 |
| **CloneCache** | `cache.go` | ETag/Last-Modified 条件请求，Manifest 持久化 |
| **robots** | `robots.go` | robots.txt 解析，sitemap 递归抓取，令牌桶限速 |
| **rewrite** | `rewrite.go` | DOM 重写，蜜罐检测，懒加载解析 |
| **css** | `css.go` | CSS url() 和 @import 重写 |
| **platform_api** | `platform_api.go` | 已知平台公共 API 拦截 (Reddit/HN/GitHub 等) |
| **archive_fallback** | `archive_fallback.go` | Wayback Machine 归档回退 |

---

## 2. EnhancedCloner 主引擎

### 2.1 引擎组合模式

`EnhancedCloner` 将以下组件组合为一个完整引擎：

```
EnhancedCloner
    ├── browserPool        // 浏览器池 (Rod 优先 / Chromedp 备用)
    ├── frontier           // 爬取队列 (BFS/DFS + 原子持久化)
    ├── assetDownloader    // 资源下载器 (代理池 + 4 层回退)
    ├── deduper            // 内容去重器 (SHA-256 + 硬链接)
    ├── cache              // 条件缓存 (ETag/Last-Modified)
    ├── robotsChecker      // robots.txt 检查器
    ├── rateLimiter        // 令牌桶速率限制器
    ├── antibot            // 反反爬引擎 (5 级升级)
    └── archiveFallback    // 归档回退 (Wayback Machine)
```

### 2.2 核心方法

| 方法 | 职责 | 说明 |
|------|------|------|
| `Clone()` | 克隆入口 | 协调整个克隆流程的主循环 |
| `processPage()` | 页面处理 | 浏览器渲染 → 提取 HTML → 重写 → 保存 |
| `processAsset()` | 资源处理 | 下载资源 → 去重 → 保存 |
| `rewriteAndDiscover()` | **单遍 DOM 遍历** | 链接重写 + 页面/资源发现同时进行 |
| `processBrowserExtractedLinks()` | 浏览器链接处理 | 处理浏览器端发现的链接 |
| `detectAndGeneratePagination()` | 分页检测生成 | 3 种模式：查询参数 / 路径 / 偏移量 |
| `preflightCloudflareCheck()` | Cloudflare 预检 | Chrome 启动前检测 Cloudflare |
| `runAntibotProbe()` | 反爬探测 | 运行多维并行探测 |

### 2.3 wantAsset 资源策略

`wantAsset()` 决定是否下载某个资源：

```
资源 URL
    │
    ▼
关键渲染资源? (CSS/字体/首屏图片)
    ├── 是 → 始终下载 (即使跨域)
    │
    否
    ▼
扩展名在 SkipAssetExts 中?
    ├── 是 → 跳过
    │
    否
    ▼
作用域内? (同域 / 子域 / 前缀匹配)
    ├── 是 → 下载
    └── 否 → 跨域资源判断
```

**关键设计决策**：
- **关键渲染资源始终跨域下载**：即使跨域，CSS/字体等影响渲染的资源也会下载，避免样式错乱
- **SkipAssetExts 过滤**：可配置的跳过扩展名列表（如 `.exe`、`.dmg` 等无关文件）

---

## 3. 克隆流水线详解

### 3.1 完整流水线

```
Seed URL (起始地址)
    │
    ▼
┌─────────────────────────────────┐
│  初始化阶段                      │
│  ├── robots.txt 获取与解析        │
│  ├── Frontier 加载历史状态        │
│  ├── CloneCache 加载 Manifest    │
│  ├── preflightCloudflareCheck()  │
│  └── runAntibotProbe() (可选)    │
└───────────────┬─────────────────┘
                │
                ▼
┌─────────────────────────────────┐
│  主循环 (Worker Pool N 并发)      │
│                                 │
│  while frontier.HasNext():      │
│    │                            │
│    ▼                            │
│    URL = frontier.Next()        │
│    │                            │
│    ├──→ processPage()           │
│    │    │                       │
│    │    ├── robots.txt 检查      │
│    │    ├── rateLimiter.Wait()  │
│    │    ├── platform_api 尝试    │
│    │    │    └── 成功? 直接保存   │
│    │    ├── 浏览器渲染           │
│    │    │    ├── Stealth 注入    │
│    │    │    ├── Settle 等待     │
│    │    │    └── 抗反爬检测      │
│    │    ├── rewriteAndDiscover() │
│    │    │    (单遍 DOM 遍历)      │
│    │    ├── detectAndGeneratePagination() │
│    │    ├── ContentDeduper 检查  │
│    │    └── 保存 HTML            │
│    │                            │
│    ├──→ processAsset() (每个资源)│
│    │    ├── CloneCache 条件请求  │
│    │    ├── AssetDownloader 下载 │
│    │    │    ├── HTTP 直连       │
│    │    │    └── 4 层浏览器回退  │
│    │    ├── ContentDeduper 去重  │
│    │    └── 保存资源             │
│    │                            │
│    └── frontier.MarkVisited()   │
│                                 │
│  (归档回退: 页面失败时)           │
│    └── archive_fallback          │
│        └── Wayback Machine       │
└───────────────┬─────────────────┘
                │
                ▼
┌─────────────────────────────────┐
│  完成阶段                        │
│  ├── Frontier 持久化             │
│  ├── CloneCache Manifest 保存    │
│  ├── ContentDeduper 统计报告     │
│  │    ├── DedupFiles (去重文件数) │
│  │    └── DedupBytesSaved (节省)  │
│  └── CloneSession cookie 保存    │
└─────────────────────────────────┘
```

### 3.2 遍历策略

| 模式 | 常量 | 说明 | 适用场景 |
|------|------|------|---------|
| **BFS** | `TraversalBFS` | 广度优先，逐层爬取 | 站点首页优先，重要页面先抓 |
| **DFS** | `TraversalDFS` | 深度优先，深入挖掘 | 特定栏目深度爬取 |

### 3.3 作用域控制

| 选项 | 类型 | 说明 |
|------|------|------|
| `Subdomains` | bool | 是否包含子域名 |
| `ScopePrefix` | string | 限制 URL 路径前缀 |
| `Exclude` | []string | 排除路径前缀列表 |
| `MaxDepth` | int | 最大链接深度 (0=无限制) |
| `MaxPages` | int | 最大页面数 (0=无限制) |

---

## 4. 单遍 DOM 遍历重写

### 4.1 单遍 vs 多遍

`rewriteAndDiscover()` 采用 **单遍 DOM 遍历**，替代了之前的 3 次扫描方案：

```
旧方案 (3 遍扫描):              新方案 (单遍遍历):
┌──────────────┐                ┌──────────────┐
│ 第1遍: 提取链接 │                │ 单遍: 同时完成 │
└──────┬───────┘                │  ├── 链接重写  │
       │                        │  ├── 页面发现  │
┌──────▼───────┐                │  ├── 资源发现  │
│ 第2遍: 重写链接 │                │  └── DOM 清理 │
└──────┬───────┘                └──────────────┘
       │                              │
┌──────▼───────┐                优势:
│ 第3遍: 发现资源 │                · DOM 只解析一次
└──────────────┘                · 内存占用减少 2/3
                                · 大页面性能提升显著
```

### 4.2 单遍遍历流程

```go
func rewriteAndDiscover(node *html.Node, sink RewriteSink) {
    // 遍历每个 DOM 节点，同时完成:
    // 1. 链接重写 (绝对 URL → 相对本地路径)
    // 2. 页面发现 (新 URL 加入 Frontier)
    // 3. 资源发现 (CSS/JS/图片/字体加入下载队列)
    // 4. 懒加载解析 (data-src → src)
    // 5. 蜜罐链接检测
}
```

### 4.3 RewriteSink 回调

`rewrite.go` 中的 `RewriteHTML()` 使用基于 DOM 的重写，通过 `RewriteSink` 回调通知调用方：

```go
type RewriteSink interface {
    // 发现新页面链接
    OnPage(url string)
    // 发现新资源链接
    OnAsset(url string, kind AssetKind)
    // 链接已重写
    OnRewrite(oldURL, newLocalPath string)
}
```

---

## 5. 分页检测与生成

### 5.1 三种分页检测模式

`detectAndGeneratePagination()` 识别并生成三种分页模式：

| 模式 | URL 特征 | 本地路径后缀 | 示例 |
|------|---------|-------------|------|
| **查询参数式** | `?Page=2` | `_page_N` | `index_page_2.html` |
| **路径式** | `/page/2/` | 保持原路径 | `page/2/index.html` |
| **偏移量式** | `?offset=50&limit=25` | `_offset_N_M` | `index_offset_50_25.html` |

### 5.2 游标分页后缀方案

对于无法解析的查询参数（cursor/token 等不透明分页），使用确定性哈希后缀：

```
分页类型     URL 参数                 本地路径后缀
─────────────────────────────────────────────────
游标式       ?cursor=abc123           _{cursorParam}_{hash}
                                   如: _cursor_e861b2.html
```

### 5.3 分页 URL 后缀方案对照

```
LocalPath() 确定性 URL→文件映射的分页后缀:

  _page_N          → ?Page=N 或 ?p=N
  _offset_N_M      → ?offset=N&limit=M
  _{cursor}_{hash} → ?{cursorParam}={cursorValue}
```

**设计目标**：不同分页 URL 生成不同文件名，确保可读性和确定性。

---

## 6. URL 规范化与本地路径映射 (urlx)

### 6.1 URL 规范化

`urlx.go` 提供完整的 URL 规范化功能：

```
URL 规范化步骤:
  1. scheme 和 host 转小写
  2. 移除默认端口 (80/http, 443/https)
  3. 路径清理 (移除 // 和 .)
  4. 查询参数排序 (确保参数顺序不影响去重)
  5. 查询参数解码
  6. 移除 fragment (#...)
```

### 6.2 LocalPath() 确定性映射

`LocalPath()` 是 URL 到本地文件路径的确定性映射函数：

```
URL                                    → 本地路径
──────────────────────────────────────────────────────
https://example.com/                   → index.html
https://example.com/about              → about/index.html
https://example.com/about/             → about/index.html
https://example.com/page.html          → page.html
https://example.com/?Page=2            → index_page_2.html
https://example.com/page/2/            → page/2/index.html
https://example.com/?offset=50&limit=25→ index_offset_50_25.html
https://cdn.example.com/img.png        → _wukong/cdn.example.com/img.png
```

**映射规则**：
- 域名目录化：`https://www.example.com/path` → `www.example.com/path`
- 以 `/` 结尾 → `index.html`
- 无扩展名 → `index.html`
- 查询参数 → 分页特殊处理或哈希后缀

### 6.3 SameRegistrableDomain

使用 `publicsuffix` 库判断两个域名是否属于同一注册域：

```go
// example.com 和 cdn.example.com → true (同一注册域 example.com)
// example.com 和 example.org     → false
func SameRegistrableDomain(u1, u2 *url.URL) bool
```

### 6.4 matchesScopePrefixWithList 双重后缀匹配

支持 `-list` 后缀的双重匹配模式：

```go
// matchesScopePrefixWithList 检查 URL 是否匹配作用域前缀列表
// 支持双重 "-list" 后缀匹配:
//   "/blog"     → 匹配 /blog, /blog/*
//   "/blog-list"→ 匹配列表页面变体
func matchesScopePrefixWithList(pageURL string, prefixes []string) bool
```

---

## 7. Frontier 爬取队列与原子持久化

### 7.1 数据结构

```go
type Frontier struct {
    seen    map[string]bool   // 所有曾入队的 URL (URL 去重)
    visited map[string]bool   // 已完成爬取的 URL
    queue   []FrontierItem    // 待爬取队列
    // ...
}
```

### 7.2 原子写入持久化

Frontier 的 JSON 状态持久化采用 **原子写入**（temp file + rename）：

```
原子写入流程:
  1. 写入临时文件 frontier.json.tmp
  2. fsync 确保数据落盘
  3. rename(frontier.json.tmp → frontier.json)
     (rename 是原子的，不会出现半写状态)

优势:
  · 断电/崩溃不会损坏状态文件
  · 断点续抓时能可靠恢复
  · 不会丢失已爬取进度
```

### 7.3 持久化内容

```json
{
  "seed_url": "https://www.example.com/",
  "visited": ["url1", "url2", "..."],
  "pending": [
    {"url": "...", "depth": 3}
  ]
}
```

---

## 8. 资源下载策略 (AssetDownloader)

### 8.1 核心特性

| 特性 | 说明 |
|------|------|
| **代理池轮换** | 多代理服务器轮换使用，分摊请求 |
| **Referer 覆盖** | 特定域名使用特殊 Referer |
| **cf_clearance 注入** | Cloudflare 清除令牌注入 |
| **gzip/deflate 自动解压** | 根据 Content-Encoding 解压 |
| **临时错误分类** | 区分可重试与不可重试错误 |

### 8.2 gzip/deflate 自动解压

```go
// 根据 Content-Encoding 头自动解压响应体
contentEncoding := resp.Header.Get("Content-Encoding")
switch contentEncoding {
case "gzip":
    gzReader, _ := gzip.NewReader(resp.Body)
    defer gzReader.Close()
    reader = gzReader
case "deflate":
    zlibReader, _ := zlib.NewReader(resp.Body)
    defer zlibReader.Close()
    reader = zlibReader
}
```

> **为什么需要手动解压？** 当代码手动设置 `Accept-Encoding` 头时，Go 的 `http.Client` 不会自动解压，必须手动处理。

### 8.3 Referer 覆盖机制

`RefererOverrides` 针对特定域名使用特殊 Referer：

| 域名模式 | Referer 策略 | 原因 |
|---------|-------------|------|
| `media.defense.gov` | 来源页面 URL | 国防部媒体服务器校验 Referer |
| `*.defense.gov` | 来源页面 URL | 所有国防部子域名 |

### 8.4 临时错误分类

错误分类决定是否重试：

| 错误类型 | 是否临时错误 | 处理 |
|---------|------------|------|
| 网络超时 | ✅ 临时 | 自动重试 |
| 连接重置 | ✅ 临时 | 自动重试 |
| 5xx 服务器错误 | ✅ 临时 | 自动重试 |
| **DNS 解析错误** | ❌ **非临时** | **不重试**（域名无法解析，重试无用） |
| 4xx 客户端错误 | ❌ 非临时 | 不重试 |

> **DNS 错误标记为非临时**：DNS 解析失败通常是持久性的（域名不存在或配置错误），重试不会改变结果，直接跳过节省时间。

### 8.5 4 层浏览器回退

当 HTTP 直连失败时，通过浏览器 4 层回退下载（每层使用全新标签上下文）：

```
Layer 1: 浏览器导航 + Network.getResponseBody
    │   使用浏览器的 TLS 指纹和 Cookie
    │   失败 ↓
Layer 2: <img> 标签加载
    │   在页面中创建 <img> 触发加载
    │   失败 ↓
Layer 3: Network.loadNetworkResource
    │   CDP 直接加载网络资源
    │   失败 ↓
Layer 4: JS fetch + base64
    │   通过 JavaScript fetch 下载并 base64 编码
    │   失败 ↓
  彻底失败
```

---

## 9. HTML 重写与蜜罐检测 (rewrite)

### 9.1 RewriteHTML() DOM 重写

`rewrite.go` 使用基于 DOM 的重写（而非正则替换），通过 `RewriteSink` 回调：

```
RewriteHTML(html, baseURL, sink)
    │
    ├── 解析 HTML 为 DOM 树
    │
    ├── 遍历 DOM 节点:
    │   ├── <a href>      → 重写链接 + OnPage() 回调
    │   ├── <link href>   → 重写 + OnAsset() 回调
    │   ├── <script src>  → 重写 + OnAsset() 回调
    │   ├── <img src>     → 重写 + 懒加载解析 + OnAsset()
    │   ├── <source src>  → 重写 + OnAsset()
    │   ├── <video poster>→ 重写 + OnAsset()
    │   ├── style 属性    → 提取 url() 引用
    │   └── 蜜罐检测
    │
    └── 序列化回 HTML 字符串
```

### 9.2 蜜罐链接检测

蜜罐链接是网站故意隐藏的链接，用于检测爬虫（正常用户看不到，爬虫会跟随）。检测以下隐藏特征：

```
蜜罐检测特征:
  · display:none         → 元素完全隐藏
  · visibility:hidden    → 元素不可见但占空间
  · opacity:0            → 完全透明
  · aria-hidden="true"   → 辅助技术忽略
  · class="sr-only"      → 屏幕阅读器专用 (视觉隐藏)
```

检测到蜜罐链接时，跳过不加入 Frontier，避免触发反爬。

### 9.3 懒加载解析

现代网站大量使用懒加载，真实图片 URL 在 `data-*` 属性中：

```
懒加载属性解析:
  data-src        → src     (最常见的懒加载)
  data-lazy-src   → src     (WordPress 等)
  data-original   → src     (jQuery Lazyload)
```

重写时自动将这些属性中的 URL 作为真实资源 URL 处理。

---

## 10. CSS URL 重写 (css)

### 10.1 RewriteCSS()

`css.go` 的 `RewriteCSS()` 重写 CSS 中的所有 URL 引用：

| CSS 语法 | 处理方式 |
|---------|---------|
| `url("path/to/file.png")` | 相对路径解析 + 重写 |
| `url('path/to/file.png')` | 同上 |
| `url(path/to/file.png)` | 同上 |
| `@import url("style.css")` | 同上 |
| `url(data:image/png;base64,...)` | 保留原样 |
| `url(#gradient)` | 保留原样 |

### 10.2 ExtractCSSAssetRefs()

提取 CSS 中引用的所有资源 URL，供 AssetDownloader 下载：

```go
// 提取 CSS 中所有资源引用
func ExtractCSSAssetRefs(cssContent string, baseURL string) []string
// 返回: ["images/logo.png", "fonts/font.woff2", "../bg.jpg"]
```

### 10.3 相对路径计算

```
CSS 文件位置: /css/main.css
CSS 内部 URL: url(../images/logo.png)
解析: /css/../images/logo.png → /images/logo.png
本地: _wukong/images/logo.png

CSS 内部 URL: @import url("responsive.css")
解析: /css/responsive.css
本地: _wukong/css/responsive.css
```

---

## 11. 内容去重引擎 (ContentDeduper)

### 11.1 工作原理

`dedup.go` 的 `ContentDeduper` 基于 SHA-256 哈希进行内容去重：

```
文件内容
    │
    ▼
SHA-256 哈希计算
    │
    ├── 哈希已在 map 中?
    │   ├── 是 → 创建硬链接指向首次出现的文件
    │   │         (不占额外磁盘空间)
    │   └── 否 → 保存新文件，记录 哈希→路径 映射
    │
    ▼
返回统计:
  DedupFiles:       去重的文件数
  DedupBytesSaved:  节省的磁盘字节数
```

### 11.2 硬链接优势

| 特性 | 说明 |
|------|------|
| **零额外磁盘占用** | 硬链接不复制数据，只是目录项引用 |
| **对文件系统透明** | 所有工具和应用看到的是正常文件 |
| **跨平台支持** | Windows NTFS、Linux ext4、macOS APFS 均支持 |
| **完全相同内容** | 只对字节级完全相同的内容有效 |

### 11.3 统计报告

去重完成后返回 `DedupFiles` 和 `DedupBytesSaved`，用于克隆报告：

```
去重报告示例:
  扫描文件: 1,250
  去重文件: 87 (DedupFiles)
  节省空间: 45.2 MB (DedupBytesSaved)
```

---

## 12. 条件缓存 (CloneCache)

### 12.1 条件请求

`cache.go` 的 `CloneCache` 使用 HTTP 条件请求避免重复下载：

```
首次请求:
  GET /style.css
  ← 200 OK
     ETag: "abc123"
     Last-Modified: Mon, 01 Jan 2026 00:00:00 GMT
  → 保存 ETag/Last-Modified 到 Manifest

后续请求 (续抓):
  HEAD /style.css
  If-None-Match: "abc123"
  If-Modified-Since: Mon, 01 Jan 2026 00:00:00 GMT
  ← 304 Not Modified
  → 跳过下载，使用本地缓存
```

### 12.2 速率限制

CloneCache 内置速率限制：**5 req/s，突发 10**（令牌桶算法）。

### 12.3 Manifest 持久化

缓存状态以 JSON Manifest 形式持久化，支持断点续抓时恢复缓存状态：

```json
{
  "https://example.com/style.css": {
    "etag": "\"abc123\"",
    "last_modified": "Mon, 01 Jan 2026 00:00:00 GMT",
    "local_path": "_wukong/css/style.css",
    "size": 45230
  }
}
```

---

## 13. robots.txt 与速率限制

### 13.1 FetchRobots()

`robots.go` 使用 `temoto/robotstxt` 库解析 robots.txt：

```
UA 匹配优先级 (高→低):
  1. Wukong-Cloner/2.0   → 精确版本匹配
  2. Wukong-Cloner       → 品牌名匹配
  3. *                   → 通配符 (默认规则)
```

### 13.2 FetchSitemaps()

支持 sitemapindex 和 urlset 的递归抓取：

```
robots.txt
    │
    ├── Sitemap: https://example.com/sitemap.xml
    │
    ▼
FetchSitemaps()
    │
    ├── <sitemapindex> (站点地图索引)
    │   ├── <sitemap><loc>sitemap-products.xml</loc></sitemap>
    │   │   └── 递归: FetchSitemaps(sitemap-products.xml)
    │   └── <sitemap><loc>sitemap-pages.xml</loc></sitemap>
    │       └── 递归: FetchSitemaps(sitemap-pages.xml)
    │
    └── <urlset> (URL 列表)
        ├── <url><loc>page1.html</loc></url>
        └── <url><loc>page2.html</loc></url>
        → 全部加入 Frontier
```

### 13.3 令牌桶速率限制

使用 `golang.org/x/time/rate` 实现令牌桶：

```go
// 每域名独立的速率限制器
type RateLimiter struct {
    limiters map[string]*rate.Limiter
    // rate: 每秒请求数
    // burst: 突发容量
}
```

---

## 14. 会话管理与 Cookie 持久化

### 14.1 CloneSession

`session.go` 的 `CloneSession` 负责 cookie 持久化：

```
CloneSession
    ├── cookiejar.Jar          // Go 标准库 cookie jar
    ├── publicsuffix.List      // 公共后缀列表 (正确判断 cookie 作用域)
    └── Netscape 格式持久化
```

### 14.2 Netscape Cookie 文件格式

cookie 以 Netscape cookies.txt 格式持久化（兼容 curl、浏览器扩展等工具）：

```
# Netscape HTTP Cookie File
.example.com    TRUE    /    FALSE    1735689600    session_id    abc123
.example.com    TRUE    /    TRUE     1735689600    cf_clearance  xyz789
```

### 14.3 浏览器实例 Cookie 加载

```go
// 将 cookie jar 中的 cookie 加载到浏览器实例
func (s *CloneSession) LoadCookiesToBrowser(browser BrowserBackend) error
// → 调用浏览器的 SetCookies 方法注入
```

这确保所有浏览器标签共享相同的会话状态（登录态、cf_clearance 等）。

---

## 15. 平台 API 拦截 (platform_api)

### 15.1 设计理念

`platform_api.go` 的 `TryPlatformAPI()` 在浏览器渲染之前拦截已知平台，通过公共 API 直接获取内容，速度提升 10-50 倍：

```
页面请求
    │
    ▼
TryPlatformAPI() 尝试
    │
    ├── 匹配已知平台?
    │   ├── 是 → 调用公共 API
    │   │        ├── 成功 → 直接保存 (跳过浏览器, 10-50x 更快)
    │   │        └── 失败 → 回退到浏览器渲染
    │   └── 否 → 浏览器渲染
    │
    ▼
```

### 15.2 支持的平台

| 平台 | API | 格式 | 说明 |
|------|-----|------|------|
| **Reddit** | `{url}.json` | JSON | Reddit 官方 JSON 端点 |
| **HackerNews** | Firebase API | JSON | `hacker-news.firebaseio.com` |
| **GitHub** | `api.github.com` | JSON | GitHub REST API |
| **Wikipedia** | REST API | JSON | `en.wikipedia.org/api/rest_v1/` |
| **arXiv** | Atom XML | XML | `export.arxiv.org/api/query` |

### 15.3 性能对比

```
浏览器渲染:  ~3-8 秒/页面 (启动浏览器 + 加载 + 渲染 + Settle)
平台 API:   ~0.1-0.3 秒/页面 (纯 HTTP 请求)

加速比: 10-50x
```

---

## 16. 归档回退 (archive_fallback)

### 16.1 ArchiveFallback

`archive_fallback.go` 在页面获取失败时回退到 Wayback Machine：

```
页面获取失败
    (404 / 超时 / 反爬阻断 / 连接错误)
    │
    ▼
FindSnapshot()
    │
    ├── 查询 Availability API:
    │   GET https://archive.org/wayback/available?url=...
    │
    ├── 返回最近快照 URL
    │   如: https://web.archive.org/web/20240101/https://example.com
    │
    ▼
转换为 id_ 变体 (获取原始内容):
    https://web.archive.org/web/20240101id_/https://example.com
    (id_ 后缀: 返回原始页面内容, 不包含 Wayback 工具栏)
    │
    ▼
FetchArchivedPage()
    │
    ├── 获取归档页面 (16 MB 上限)
    ├── stripWaybackToolbar() 移除注入的脚本
    └── 保存到本地
```

### 16.2 id_ 变体说明

Wayback Machine 的 URL 后缀控制返回内容：

```
标准 URL:    /web/20240101/https://example.com
             → 页面被 Wayback 重写, 注入工具栏和脚本

id_ 变体:   /web/20240101id_/https://example.com
             → 原始页面内容, 无 Wayback 修改
```

### 16.3 stripWaybackToolbar

即使使用 `id_` 变体，某些情况下仍有注入内容，`stripWaybackToolbar()` 会移除：
- Wayback Machine 注入的 `<script>` 标签
- Wayback 工具栏 HTML
- Wayback 资源引用

---

## 17. ZIM 打包器 (Packer)

### 17.1 四种打包格式

`internal/apps/pack/packer.go` 的 Packer 支持四种输出格式：

| 格式 | 说明 | 适用场景 |
|------|------|---------|
| **HTML** | 目录复制 | 直接用浏览器打开 |
| **ZIM** | Kiwix 格式 (最复杂) | Kiwix 阅读器、离线分发 |
| **Binary** | 自包含可执行文件 | 单文件分发 |
| **App** | 平台应用包 | macOS .app / Windows / Linux |

### 17.2 ZIM 格式打包流程 (最复杂)

```
克隆输出目录
    │
    ▼
┌─────────────────────────────────────────┐
│  1. 克隆布局检测                          │
│     ├── 检测 pages//assets/ 布局          │
│     ├── stripPrefix 移除路径前缀          │
│     └── adjustHTMLPaths 调整 HTML 路径    │
└──────────────────┬──────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────┐
│  2. 路径处理                             │
│     ├── stripAssetsPrefixFromHTML        │
│     │   移除 HTML 中资源路径前缀           │
│     └── 确保路径符合 ZIM 规范             │
└──────────────────┬──────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────┐
│  3. 主页面选择 (优先级)                   │
│     ├── 显式指定的主页面                  │
│     ├── 根目录 index.html                │
│     └── 自动生成的主页面                  │
└──────────────────┬──────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────┐
│  4. 目录索引重定向                       │
│     └── 为每个目录创建索引重定向条目       │
└──────────────────┬──────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────┐
│  5. 元数据写入                           │
│     ├── Title / Description / Creator    │
│     ├── Language / Date / Source         │
│     └── 源 URL 检测 (从 HTML 注释中提取)   │
└──────────────────┬──────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────┐
│  6. 图标处理                             │
│     ├── 48x48 favicon                    │
│     └── isPNG48x48 校验                  │
└──────────────────┬──────────────────────┘
                   │
                   ▼
┌─────────────────────────────────────────┐
│  7. 写入 ZIM 文件                        │
│     └── 调用 pkg/zim 写入器              │
└─────────────────────────────────────────┘
```

### 17.3 Binary 格式 (自包含可执行文件)

```
┌─────────────────────────────────┐
│  嵌入式浏览器 (Go 二进制)         │
├─────────────────────────────────┤
│  ---WUKONG_ZIM_BEGIN:{size}:    │
│  {appName}:{version}:{files}---  │  ← 标记分隔符
├─────────────────────────────────┤
│                                 │
│  ZIM 数据 (流式写入)              │
│                                 │
├─────────────────────────────────┤
│  ---WUKONG_ZIM_END---           │
└─────────────────────────────────┘

特性:
  · 流式写入, 内存占用恒定
  · 自包含, 无需额外依赖
  · 运行时自动启动本地 HTTP 服务器
```

### 17.4 App 格式 (平台应用包)

| 平台 | 结构 | 说明 |
|------|------|------|
| **macOS** | `App.app/Contents/`<br>`├── Info.plist`<br>`├── MacOS/{binary}`<br>`└── Resources/` | 标准 .app 包 |
| **Windows** | 单个二进制文件 | 与 Binary 格式相同 |
| **Linux** | AppDir 结构<br>`├── .desktop 文件`<br>`└── usr/share/` | 标准 AppDir |

### 17.5 源 URL 检测

Packer 会从克隆 HTML 的注释中检测原始源 URL：

```html
<!-- Wukong-Cloned-From: https://www.example.com/ -->
```

此信息写入 ZIM 元数据的 `Source` 字段。

---

## 18. ZIM 文件格式 (pkg/zim)

### 18.1 ZIM v6 规范

| 属性 | 值 |
|------|-----|
| **版本** | v6 |
| **Magic** | `0x5a5a494d04` ("ZZIM" + version) |
| **兼容** | Kiwix 阅读器 |

### 18.2 文件布局

```
ZIM 文件结构:

偏移     内容                      大小
─────────────────────────────────────────────
0        Header (文件头)             80 字节
80       MIME List (MIME 类型列表)   变长
         URL Pointer List (URL 指针)  N × 4/8 字节
         Title Pointer List (标题指针) N × 4/8 字节
         Cluster Pointer List (集群指针) C × 8 字节
         ...Articles... (文章数据)
         ...Clusters... (集群数据)
末尾-16  MD5 校验和                   16 字节
```

### 18.3 ArticleType 条目类型

| 类型常量 | 值 | 说明 |
|---------|-----|------|
| `Redirect` | 0 | 重定向条目 |
| `LinkFree` | 1 | 无链接条目 |
| `LinkTarget` | 2 | 链接目标 |
| `Article` | 3 | 普通文章 |

### 18.4 压缩类型

| 类型常量 | 值 | 说明 |
|---------|-----|------|
| `None` | 1 | 无压缩 |
| `Zstd` | 5 | Zstandard 压缩 (默认) |

### 18.5 命名空间

| 命名空间 | 用途 |
|---------|------|
| `C` | Content (主要内容) |
| `M` | Metadata (元数据) |
| `W` | Wellness/Redirect (重定向) |

### 18.6 集群构建

```
集群 (Cluster) 构建:
  maxClusterSize = 2 MiB (单集群最大 2MB)

  分类策略:
    ├── 文本 MIME (text/html, text/css, application/javascript...)
    │   → 文本集群 (可压缩)
    └── 二进制 MIME (image/png, font/woff2...)
        → 二进制集群 (通常不压缩)

  增量 SHA-256 缓存:
    每个集群内容计算 SHA-256
    增量更新, 避免重复计算
```

### 18.7 UUID 计算

`computeUUID` 使用确定性 MD5 生成 UUID，确保相同内容生成相同 UUID：

```go
// 基于 ZIM 内容确定性生成 UUID
func computeUUID(content []byte) [16]byte {
    h := md5.Sum(content)
    // 设置 UUID 版本和变体位
    h[6] = (h[6] & 0x0f) | 0x50  // 版本 5
    h[8] = (h[8] & 0x3f) | 0x80  // 变体
    return h
}
```

### 18.8 Reader 读取器

ZIM Reader 的核心特性：

| 特性 | 说明 |
|------|------|
| **二分搜索** | URL/标题查找使用二分搜索 |
| **重定向跳转** | `maxRedirectHops = 16`，防止无限重定向 |
| **集群解压缓存** | 解压后的集群数据缓存，避免重复解压 |

---

## 19. 配置参考

### 19.1 主要配置项

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `apps.clone.max_pages` | int | 0 | 最大页面数 (0=无限制) |
| `apps.clone.max_depth` | int | 0 | 最大深度 (0=无限制) |
| `apps.clone.traversal` | string | `bfs` | 遍历策略: bfs/dfs |
| `apps.clone.workers` | int | 3 | 爬取 Worker 数 |
| `apps.clone.asset_workers` | int | 5 | 资源下载 Worker 数 |
| `apps.clone.browser_pages` | int | 3 | 浏览器标签池大小 |
| `apps.clone.headless` | bool | true | 无头模式 |
| `apps.clone.stealth` | bool | false | 隐身模式 |
| `apps.clone.antibot_enabled` | bool | false | 启用反反爬 |
| `apps.clone.antibot_auto_escalate` | bool | false | 自动升级反爬等级 |
| `apps.clone.enable_resume` | bool | true | 启用断点续抓 |
| `apps.clone.dedup_content` | bool | false | 启用内容去重 |
| `apps.clone.settle` | int | 2000 | 网络空闲等待 (ms) |
| `apps.clone.respect_robots` | bool | true | 遵守 robots.txt |
| `apps.clone.subdomains` | bool | false | 包含子域名 |
| `apps.clone.browser_backend` | string | `rod` | 浏览器后端: rod/chromedp |
| `apps.clone.platform_api` | bool | true | 启用平台 API 拦截 |
| `apps.clone.archive_fallback` | bool | false | 启用归档回退 |

### 19.2 CLI 选项

```bash
wukong apps clone <url> [flags]

常用选项:
  --max-pages int        最大页面数
  --max-depth int        最大深度
  --workers int          Worker 数
  --headless             无头模式
  --stealth              隐身模式
  --antibot              启用反反爬
  --resume               断点续抓
  --force                强制重新开始
  --output string        输出目录
  --cookie-file string   导入 Cookie 文件

打包选项:
  --format string        打包格式 (html/zim/binary/app)
  --compress             ZIM 压缩
  --language string      ZIM 语言代码
  --creator string       ZIM 创建者
  --title string         ZIM 标题
```

---

## 20. 常见问题

### Q1: 页面样式错乱怎么办？

**可能原因**：
1. CSS 文件下载失败 → 检查网络和反爬设置
2. CSS 是 gzip 压缩格式未解压 → 确认 AssetDownloader 的 gzip/deflate 解压正常
3. CSS 中的相对路径解析错误 → 检查 RewriteCSS 的 baseURL 传入

**排查步骤**：用浏览器开发者工具查看哪些 CSS 返回 404，检查对应文件是否存在且内容正常。

### Q2: 图片下载失败 (HTTP 403)？

**原因**：站点有反爬保护，校验 Referer、Cookie 或 TLS 指纹。

**解决方案**：
1. AssetDownloader 的 RefererOverrides 会自动覆盖特定域名
2. 启用 `--antibot` 反反爬系统
3. 使用 `--cookie-file` 导入浏览器 Cookie（特别是 cf_clearance）
4. 浏览器 4 层回退会自动尝试

### Q3: 分页页面没被爬取？

**原因**：分页 URL 可能被识别为重复页面或不符合检测模式。

**解决方案**：确保 `detectAndGeneratePagination()` 的 3 种模式覆盖目标站点。对于不常见的分页参数，游标哈希后缀方案 (`_{cursorParam}_{hash}`) 会生成确定性文件名。

### Q4: Kiwix 中打开 ZIM 图片不显示？

**排查步骤**：
1. 检查 ZIM 中 `stripAssetsPrefixFromHTML` 是否正确移除了资源路径前缀
2. 确认 `adjustHTMLPaths` 正确调整了 HTML 中的路径引用
3. 检查集群中图片 MIME 类型是否正确分类
4. 验证 ZIM Reader 的二分搜索能否找到图片条目

### Q5: 如何使用归档回退？

```bash
# 启用归档回退，页面获取失败时自动查询 Wayback Machine
wukong apps clone https://example.com --archive-fallback
```

归档回退使用 `id_` 变体获取原始内容，并通过 `stripWaybackToolbar` 清理注入脚本。

### Q6: 如何加速已知平台的克隆？

平台 API 拦截默认启用（`platform_api: true`），会自动检测 Reddit、HackerNews、GitHub、Wikipedia、arXiv 等平台，通过公共 API 直接获取内容，比浏览器渲染快 10-50 倍。

---

## 附录

### 相关文档

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | 系统架构详解 |
| [ANTIBOT_GUIDE.md](./ANTIBOT_GUIDE.md) | 反反爬与浏览器引擎技术指南 |
| [CONFIG.md](./CONFIG.md) | 配置参考手册 |
| [README.md](../README.md) | 项目主页 |

### 源码索引

| 模块 | 路径 |
|------|------|
| 克隆引擎 | `internal/apps/clone/enhanced_cloner.go` |
| 会话管理 | `internal/apps/clone/session.go` |
| 爬取队列 | `internal/apps/clone/frontier.go` |
| URL 工具 | `internal/apps/clone/urlx.go` |
| 资源下载 | `internal/apps/clone/asset.go` |
| HTML 重写 | `internal/apps/clone/rewrite.go` |
| CSS 重写 | `internal/apps/clone/css.go` |
| 内容去重 | `internal/apps/clone/dedup.go` |
| 条件缓存 | `internal/apps/clone/cache.go` |
| robots | `internal/apps/clone/robots.go` |
| 平台 API | `internal/apps/clone/platform_api.go` |
| 归档回退 | `internal/apps/clone/archive_fallback.go` |
| 打包器 | `internal/apps/pack/packer.go` |
| ZIM 格式 | `pkg/zim/` |

---

> **版本**: v2.0 | **最后更新**: 2026-08-11 | **相关代码**: internal/apps/clone/ + internal/apps/pack/ + pkg/zim/
