# 网站克隆技术指南

> 克隆引擎: EnhancedCloner | 浏览器后端: Rod + Chromedp | 分页: 6 种
> 资源下载: 4 层回退 | 内容去重: SHA-256 + 硬链接 | 断点续抓: Frontier 持久化

---

## 目录

1. [引擎架构](#1-引擎架构)
2. [克隆流水线](#2-克隆流水线)
3. [分页处理](#3-分页处理)
4. [资源下载策略](#4-资源下载策略)
5. [CSS 处理与 URL 重写](#5-css-处理与-url-重写)
6. [HTML 重写与链接发现](#6-html-重写与链接发现)
7. [内容去重](#7-内容去重)
8. [断点续抓](#8-断点续抓)
9. [ZIM 打包](#9-zim-打包)
10. [配置参考](#10-配置参考)
11. [常见问题](#11-常见问题)

---

## 1. 引擎架构

### 1.1 核心组件

```
EnhancedCloner (主引擎)
    ├── Frontier (爬取队列)
    │   ├── BFS / DFS 遍历
    │   ├── URL 去重
    │   └── 持久化 (断点续抓)
    │
    ├── BrowserPool (浏览器池)
    │   ├── Rod (默认后端)
    │   ├── Chromedp (备用后端)
    │   ├── Stealth 隐身脚本
    │   └── Proxy Pool 代理池
    │
    ├── Antibot (反反爬)
    │   ├── 5 级升级策略
    │   ├── UA 池 (161 个)
    │   └── Referer 伪造
    │
    ├── AssetDownloader (资源下载)
    │   ├── HTTP 直连 (主路径)
    │   ├── Browser 回退 (4 层)
    │   ├── gzip/zlib 解压
    │   └── 大小限制 + 重试
    │
    ├── CSSRewriter (CSS 重写)
    │   ├── URL 提取与重写
    │   └── 相对路径解析
    │
    ├── HTMLRewriter (HTML 重写)
    │   ├── 链接发现
    │   ├── URL → 本地路径映射
    │   └── DOM 清理
    │
    ├── DedupEngine (去重引擎)
    │   ├── URL 去重
    │   └── 内容去重 (SHA-256 + 硬链接)
    │
    └── Session (会话管理)
        ├── 进度持久化
        └── 统计报告
```

### 1.2 代码组织

| 文件 | 行数 | 职责 |
|------|------|------|
| `enhanced_cloner.go` | ~800 | 主引擎，协调所有子系统 |
| `asset.go` | ~400 | HTTP 资源下载器 (含 gzip 解压) |
| `css.go` | ~300 | CSS URL 重写器 |
| `css_test.go` | ~200 | CSS 重写单元测试 |
| `rewrite.go` | ~300 | HTML 重写 + 链接发现 |
| `rewrite_test.go` | ~150 | HTML 重写单元测试 |
| `urlx.go` | ~400 | URL 处理 + 分页支持 + 本地路径生成 |
| `urlx_test.go` | ~300 | URL 处理单元测试 |
| `frontier.go` | ~200 | 爬取队列 + 断点续抓 |
| `frontier_test.go` | ~100 | Frontier 单元测试 |
| `dedup.go` | ~150 | 内容去重引擎 |
| `dedup_test.go` | ~100 | 去重单元测试 |
| `session.go` | ~200 | 克隆会话管理 |
| `session_test.go` | ~100 | 会话单元测试 |
| `robots.go` | ~100 | robots.txt 检查 |
| `cache.go` | ~100 | ETag/Last-Modified 缓存 |
| `options.go` | ~100 | 选项定义 |
| `result.go` | ~50 | 结果统计 |

---

## 2. 克隆流水线

### 2.1 完整流程

```
Seed URL (起始地址)
    │
    ▼
┌─────────────────────────┐
│   Frontier 初始化        │
│   ├── 解析种子 URL       │
│   ├── 检查 robots.txt    │
│   └── 加载历史状态 (续抓) │
└───────────┬─────────────┘
            │
            ▼
┌─────────────────────────┐
│   Worker Pool (N 并发)   │
│                         │
│  从 Frontier 取 URL      │
│         │               │
│         ▼               │
│  浏览器渲染页面          │
│   ├── Stealth 注入       │
│   ├── Settle 等待        │
│   └── 自动滚动 (可选)    │
│         │               │
│         ▼               │
│  提取 HTML + 资源        │
│   ├── 发现新链接         │
│   ├── 发现 CSS/JS/图片   │
│   └── 加入 Frontier      │
│         │               │
│         ▼               │
│  资源下载 (4 层回退)     │
│   ├── HTTP 直连          │
│   ├── CDP 加载           │
│   ├── img 标签           │
│   └── fetch API          │
│         │               │
│         ▼               │
│  HTML/CSS URL 重写       │
│   ├── 绝对路径 → 相对路径 │
│   └── 分页 URL 处理      │
│         │               │
│         ▼               │
│  内容去重检查            │
│   └── SHA-256 + 硬链接   │
│         │               │
│         ▼               │
│  保存到本地文件系统       │
│                         │
└───────────┬─────────────┘
            │
            ▼
 Frontier 为空？ ──否──┐
            │         │
            是        └── 继续循环
            │
            ▼
┌─────────────────────────┐
│   完成                  │
│   ├── 保存会话状态       │
│   ├── 生成统计报告       │
│   └── 关闭浏览器池       │
└─────────────────────────┘
```

### 2.2 遍历策略

| 模式 | 常量 | 说明 | 适用场景 |
|------|------|------|---------|
| **BFS** | `TraversalBFS` | 广度优先，逐层爬取 | 站点首页优先，重要页面先抓 |
| **DFS** | `TraversalDFS` | 深度优先，一条路走到黑 | 特定栏目深度挖掘 |

### 2.3 作用域控制

| 选项 | 类型 | 说明 |
|------|------|------|
| `Subdomains` | bool | 是否包含子域名 |
| `ScopePrefix` | string | 限制 URL 路径前缀 |
| `Exclude` | []string | 排除路径前缀列表 |
| `MaxDepth` | int | 最大链接深度 (0=无限制) |
| `MaxPages` | int | 最大页面数 (0=无限制) |

---

## 3. 分页处理

### 3.1 支持的 6 种分页方式

| 类型 | 示例 URL | 生成本地路径 | 说明 |
|------|---------|-------------|------|
| **查询参数式** | `?Page=2` | `index_page_2.html` | 最常见，参数名不区分大小写 |
| **路径式** | `/page/2/` | `page/2/index.html` | WordPress 等 CMS 常用 |
| **Offset/Limit** | `?offset=50&limit=25` | `index_offset_50_25.html` | API 风格，偏移量 |
| **Cursor/Keyset** | `?cursor=abc123` | `index_cursor_e861b2.html` | 游标分页，短哈希后缀 |
| **Seek** | `?after=2024-01-01` | `index_seek_xxx.html` | 按字段值翻页 |
| **Token** | `?pageToken=xxx` | `index_token_xxx.html` | 不透明 token，Google API 风格 |

### 3.2 为什么需要特殊处理？

**问题**: 如果所有查询参数都用哈希后缀，会导致：
1. 分页页面被误认为重复页面而跳过
2. 文件名不直观，无法从文件名判断第几页
3. 不同顺序的查询参数生成不同哈希

**解决方案**: 识别常见分页参数，生成可读的文件名。

### 3.3 分页参数识别

**查询参数分页** — 识别以下参数名（不区分大小写）：
- `page`, `p`, `pg`, `pagenum`, `pageNo`, `paged`

**Offset/Limit 分页** — 识别参数组合：
- `offset` + `limit`
- `start` + `count`
- `skip` + `take`

**Cursor/Seek/Token 分页** — 识别参数名：
- Cursor: `cursor`, `after_cursor`, `before_cursor`
- Seek: `after`, `before`, `since`, `until`
- Token: `pageToken`, `nextToken`, `continuationToken`

### 3.4 去重与 PageKey

**PageKey** 是 URL 的确定性映射，用于去重判断：

```go
func PageKey(seedHost, pageURL string) string {
    return LocalPath(seedHost, pageURL, KindPage)
}
```

**关键设计**:
- 不同分页 URL 生成不同的 PageKey
- 即使查询参数顺序不同，规范化后生成相同 PageKey
- 查询参数规范化（排序、解码）

### 3.5 待实现的分页方式

以下分页方式标记为待实现（主要用于 API 爬取）：

| 类型 | 说明 | 难度 |
|------|------|------|
| **Header 式** | 分页信息在 HTTP 头中 (Link, X-Total-Count) | 高 |
| **Body 式** | POST 请求体中带分页参数 | 高 |

---

## 4. 资源下载策略

### 4.1 4 层回退机制

```
Layer 1: HTTP 直连 (AssetDownloader)
    │   快速、高效、并发度高
    │   失败 → Layer 2
    │
Layer 2: CDP Network.loadNetworkResource
    │   通过 Chrome DevTools Protocol 加载
    │   享有浏览器的 cookie / TLS 指纹
    │   失败 → Layer 3
    │
Layer 3: img 标签加载 (浏览器内)
    │   在页面中创建 <img> 标签触发加载
    │   最接近真实用户行为
    │   失败 → Layer 4
    │
Layer 4: fetch API (浏览器内)
    │   通过 JavaScript fetch 加载
    │   享有完整浏览器上下文
    │   失败 → 彻底失败
    ▼
  下载失败，记录错误
```

### 4.2 HTTP 下载器

**核心特性**:
- **大小限制**: 默认 50 MB，防止超大文件
- **重试机制**: 3 次重试，指数退避
- **重定向跟随**: 自动跟随 3xx 重定向
- **临时错误分类**: 网络错误、超时、5xx 等自动重试
- **gzip/zlib 解压**: 手动解压压缩响应，确保文件可读
- **ForceIPv4**: 强制 IPv4，避免 IPv6 权限问题

**gzip 解压逻辑** (asset.go L272-312):

```go
// 读取响应体时根据 Content-Encoding 自动解压
reader := resp.Body
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

> **为什么需要手动解压？**  
> 当代码手动设置 `Accept-Encoding: gzip, deflate` 头时，Go 的 `http.Client` 不会自动解压。必须手动处理，否则保存的 CSS/JS 文件是压缩的二进制，浏览器无法解析。

### 4.3 Referer 策略

**Referer 覆盖机制** (`RefererOverrides`):

| 域名模式 | Referer | 说明 |
|---------|---------|------|
| `media.defense.gov` | 来源页面 URL | 国防部媒体服务器需要正确 Referer |
| `*.defense.gov` | 来源页面 URL | 所有国防部子域名 |

**工作原理**:
1. 下载资源时，检查资源域名是否匹配覆盖规则
2. 匹配时使用覆盖的 Referer，否则使用默认 Referer（当前页面 URL）
3. 支持精确域名和后缀模式（`.example.com` 匹配所有子域名）

### 4.4 随机延迟

**所有域名**都添加 300-1000ms 的随机延迟：

```go
// 假设所有网站都有反爬措施，添加随机延迟
delay := time.Duration(300+rand.Intn(700)) * time.Millisecond
select {
case <-time.After(delay):
case <-ctx.Done():
    return ctx.Err()
}
```

> **设计决策**: 不再区分"有反爬"和"无反爬"的域名，统一添加延迟。好处：
> - 避免误判导致被封
> - 代码更简单，无需维护域名列表
> - 对大多数站点影响可忽略

### 4.5 资源类型

| 类型 | 常量 | 扩展名示例 |
|------|------|-----------|
| 页面 | `KindPage` | .html, .htm |
| CSS | `KindCSS` | .css |
| JS | `KindJS` | .js |
| 图片 | `KindImage` | .png, .jpg, .gif, .webp, .svg |
| 字体 | `KindFont` | .woff, .woff2, .ttf, .otf |
| 媒体 | `KindMedia` | .mp4, .mp3, .webm |
| 其他 | `KindAsset` | 所有其他资源 |

### 4.6 资源目录结构

```
cloned/www.example.com/
    ├── index.html              # 首页
    ├── about/
    │   └── index.html          # 关于页
    └── _wukong/                # 资源目录 (保留前缀)
        ├── css/
        │   └── style.css
        ├── js/
        │   └── main.js
        ├── images/
        │   └── logo.png
        ├── fonts/
        │   └── font.woff2
        └── media.example.com/  # 跨域资源按域名分目录
            └── images/
                └── banner.jpg
```

---

## 5. CSS 处理与 URL 重写

### 5.1 CSS URL 提取

CSS 中可能出现 URL 的地方：
- `url()` 函数: `background-image: url(...)`
- `@import` 规则: `@import url(...)`
- `src` 描述符: `src: url(...) format(...)`

**支持的语法**:
```css
url("path/to/file.css")      /* 双引号 */
url('path/to/file.css')      /* 单引号 */
url(path/to/file.css)        /* 无引号 */
url(  path/to/file.css  )    /* 空格 */
```

### 5.2 重写规则

| 原始 URL 类型 | 示例 | 处理方式 |
|-------------|------|---------|
| 绝对 URL (同域) | `url(/images/logo.png)` | 相对路径重写 |
| 绝对 URL (跨域) | `url(https://cdn.example.com/style.css)` | 下载到 `_wukong/cdn.example.com/` |
| 相对路径 | `url(../images/logo.png)` | 基于 CSS 文件位置解析 |
| data URI | `url(data:image/png;base64,...)` | 保留原样，不下载 |
| 锚点引用 | `url(#gradient)` | 保留原样 |

### 5.3 相对路径计算

HTML 中的 CSS 链接路径 → 本地 CSS 文件路径 → CSS 内部相对路径 → 正确的本地相对路径

```
HTML 位置:    /articles/2024/index.html
CSS 引用:     <link rel="stylesheet" href="/css/main.css">
CSS 位置:     /css/main.css
CSS 内部 URL: url(../images/logo.png)

解析结果:
  CSS 中的 ../images/logo.png
  = /css/../images/logo.png
  = /images/logo.png

本地路径:
  _wukong/images/logo.png

在 HTML 中重写为:
  _wukong/images/logo.png
```

---

## 6. HTML 重写与链接发现

### 6.1 链接发现

从 HTML 中提取以下类型的链接：

| 元素 | 属性 | 说明 |
|------|------|------|
| `<a>` | `href` | 页面链接（加入 Frontier） |
| `<link rel="stylesheet">` | `href` | CSS 样式表 |
| `<script>` | `src` | JavaScript 文件 |
| `<img>` | `src`, `srcset` | 图片资源 |
| `<source>` | `src`, `srcset` | picture/source 图片 |
| `<video>` | `src`, `poster` | 视频及封面 |
| `<audio>` | `src` | 音频文件 |
| `<iframe>` | `src` | 内嵌框架 |
| `<object>` | `data` | 嵌入对象 |
| CSS `style` 属性 | - | 内联样式中的 url() |

### 6.2 URL → 本地路径映射

**基本规则**:
- 域名目录化: `https://www.example.com/path/to/page.html` → `www.example.com/path/to/page.html`
- 以 `/` 结尾的路径 → `index.html`
- 无扩展名的路径 → `index.html`
- 查询参数 → 分页特殊处理或哈希后缀

**示例**:

| URL | 本地路径 |
|-----|---------|
| `https://example.com/` | `index.html` |
| `https://example.com/about` | `about/index.html` |
| `https://example.com/about/` | `about/index.html` |
| `https://example.com/page.html` | `page.html` |
| `https://example.com/?Page=2` | `index_page_2.html` |
| `https://example.com/page/2/` | `page/2/index.html` |
| `https://cdn.example.com/img.png` | `_wukong/cdn.example.com/img.png` |

### 6.3 DOM 清理

克隆时会清理以下内容：
- `<script>` 标签（移除动态脚本）
- 内联 `onclick`, `onload` 等事件处理器
- `<noscript>` 保留
- `integrity` / `nonce` 等 CSP 属性移除

---

## 7. 内容去重

### 7.1 URL 去重

**Frontier 层**：URL 入队前去重，避免同一 URL 被多次爬取。

- 规范化: 小写 scheme/host，移除默认端口，清理路径
- 去重集合: `map[string]bool`
- 持久化: 保存在 session 中，支持断点续抓

### 7.2 内容去重

**DedupEngine**：基于内容哈希的去重，相同内容的文件只存一份。

```
文件内容 → SHA-256 哈希
                │
                ├── 哈希已存在？
                │   ├── 是 → 创建硬链接指向现有文件
                │   └── 否 → 保存新文件，记录哈希
                ▼
           节省磁盘空间
```

**优点**:
- 完全相同的页面/资源只存一份
- 节省磁盘空间
- 硬链接对文件系统透明

**限制**:
- 需要文件系统支持硬链接（Windows NTFS、Linux ext4、macOS APFS 都支持）
- 只对完全相同的内容有效
- 修改一个硬链接会影响所有引用

---

## 8. 断点续抓

### 8.1 Frontier 持久化

**状态保存**:
- 已爬取的 URL 集合
- 待爬取的 URL 队列
- 当前深度计数
- 已下载的资源记录

**保存时机**:
- 每个页面爬取完成后
- 程序正常退出时
- 收到中断信号时

**恢复方式**:
- 启动时检查 session 文件
- 存在则加载历史状态继续
- 使用 `--force` 可清除历史重新开始

### 8.2 Session 文件

```
.wukong/apps/cloned/www.example.com/
    ├── .wukong-session.json     # 会话状态
    ├── index.html
    └── ...
```

Session JSON 结构:
```json
{
  "seed_url": "https://www.example.com/",
  "pages_cloned": 127,
  "pages_pending": 45,
  "assets_downloaded": 512,
  "started_at": "2024-01-01T00:00:00Z",
  "visited": ["url1", "url2", "..."],
  "frontier": [{"url": "...", "depth": 3}]
}
```

---

## 9. ZIM 打包

### 9.1 ZIM 格式

- **版本**: ZIM v6
- **压缩**: zstd (编码 5)
- **兼容**: Kiwix 阅读器
- **特性**: 元数据 + 图标 + 计数器 + 增量集群缓存

### 9.2 打包流程

```
克隆输出目录
    │
    ▼
┌─────────────────┐
│  收集文件列表     │
│  ├── HTML 页面   │
│  ├── 图片        │
│  ├── CSS/JS      │
│  └── 字体        │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  构建目录条目     │
│  ├── URL 编码    │
│  ├── MIME 类型   │
│  └── 标题提取    │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  写入 ZIM 文件   │
│  ├── Header     │
│  ├── URL 指针   │
│  ├── 标题索引    │
│  ├── 集群数据    │
│  └── Meta 数据   │
└─────────────────┘
```

### 9.3 打包命令

```bash
# 打包为 ZIM
wukong apps pack example.com --format zim --compress

# 打包为 ZIP
wukong apps pack example.com --format zip

# 设置语言和创建者
wukong apps pack example.com --format zim --language zh --creator "Wukong"
```

---

## 10. 配置参考

### 10.1 主要配置项

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
| `apps.clone.scroll` | bool | false | 自动滚动 |
| `apps.clone.respect_robots` | bool | true | 遵守 robots.txt |
| `apps.clone.subdomains` | bool | false | 包含子域名 |
| `apps.clone.browser_backend` | string | `rod` | 浏览器后端: rod/chromedp |

### 10.2 CLI 选项

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
  --refresh              刷新已存在的页面
  --output string        输出目录
  --format string        打包格式 (zim/zip)
```

---

## 11. 常见问题

### Q1: 页面样式错乱怎么办？

**可能原因**:
1. CSS 文件下载失败 → 检查网络和反爬设置
2. CSS 是 gzip 压缩格式 → 确保使用最新版本（已修复）
3. CSS 中的相对路径解析错误 → 检查 URL 重写

**排查步骤**:
1. 用浏览器开发者工具查看哪些 CSS 404
2. 检查对应 CSS 文件是否存在
3. 打开 CSS 文件看内容是否正常文本

### Q2: 图片下载失败 (HTTP 403)？

**原因**: 站点有反爬保护，检查 Referer、UA 等。

**解决方案**:
1. 启用 `--stealth` 隐身模式
2. 启用 `--antibot` 反反爬
3. 设置 `--user-agent` 模拟真实浏览器
4. 使用 `--cookie-file` 导入浏览器 Cookie

### Q3: 分页页面没被爬取？

**原因**: 分页 URL 可能被识别为重复页面。

**解决方案**:
- 确保使用支持分页识别的版本（已实现 6 种分页方式）
- 检查 URL 是否符合支持的分页模式
- 对于不常见的分页参数，可以先提 issue

### Q4: 克隆速度太慢？

**优化建议**:
1. 增加 `--workers` 并发数 (注意不要太高，避免被封)
2. 减少 `--settle` 等待时间
3. 关闭 `--scroll` 自动滚动
4. 设置 `--max-pages` 和 `--max-depth` 限制范围

### Q5: 如何断点续抓？

直接重新运行相同的克隆命令即可：
```bash
# 第一次运行
wukong apps clone https://example.com

# 中断后继续（自动检测 session）
wukong apps clone https://example.com

# 强制重新开始
wukong apps clone https://example.com --force
```

### Q6: Kiwix 中打开 ZIM 文件图片不显示？

**可能原因**:
1. 图片下载失败 → 检查克隆日志
2. ZIM 打包时路径编码问题 → 报告 bug
3. 跨域图片路径问题 → 检查 `_wukong` 目录结构

---

## 附录

### 相关文档

| 文档 | 说明 |
|------|------|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | 系统架构详解 |
| [ANTIBOT_GUIDE.md](./ANTIBOT_GUIDE.md) | 反反爬技术详解 |
| [CONFIG.md](./CONFIG.md) | 配置参考手册 |
| [README.md](../README.md) | 项目主页 |

---

> **版本**: v1.0 | **最后更新**: 2026-07-23 | **相关代码**: internal/apps/clone/ (18 文件)
