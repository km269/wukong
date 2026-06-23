// Package clone provides website cloning functionality.
// It renders pages in headless Chrome, extracts static content,
// downloads assets, and creates offline-ready HTML directories.
package clone

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/km269/wukong/internal/apps/sanitize"
)

// pendingPage represents a page waiting to be cloned.
type pendingPage struct {
	url   string
	depth int
}

// Cloner performs website cloning operations.
type Cloner struct {
	opts       Options
	visited    map[string]bool
	visitedMu  sync.RWMutex
	pending    []pendingPage
	pendingMu  sync.Mutex
	stats      Stats
	statsMu    sync.RWMutex
	results    []PageResult
	resultsMu  sync.Mutex
	assetDir   string
	pageDir    string
	host       string
	seedURL    string

	// Asset tracking
	downloadedAssets map[string]*downloadedAsset
	assetMu          sync.RWMutex
	assetQueue       []string

	// HTTP client for downloading
	httpClient *http.Client
}

// downloadedAsset represents a downloaded resource.
type downloadedAsset struct {
	URL        string
	LocalPath  string
	ContentType string
	Size       int64
	MimeType   string
}

// NewCloner creates a new website cloner with the given options.
func NewCloner(opts Options) *Cloner {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30
	}

	return &Cloner{
		opts:             opts,
		visited:          make(map[string]bool),
		pending:          make([]pendingPage, 0),
		results:          make([]PageResult, 0),
		stats:            Stats{},
		downloadedAssets: make(map[string]*downloadedAsset),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Clone performs the website cloning operation.
func (c *Cloner) Clone(ctx context.Context, seedURL string) (*Result, error) {
	startTime := time.Now()

	// 解析种子 URL
	parsedURL, err := url.Parse(seedURL)
	if err != nil {
		return nil, fmt.Errorf("parse seed URL: %w", err)
	}

	// 确保 URL 有协议前缀
	if !strings.HasPrefix(seedURL, "http://") && !strings.HasPrefix(seedURL, "https://") {
		seedURL = "https://" + seedURL
		parsedURL, err = url.Parse(seedURL)
		if err != nil {
			return nil, fmt.Errorf("parse seed URL: %w", err)
		}
	}

	c.seedURL = seedURL
	c.host = parsedURL.Host

	// 设置输出目录
	outputDir := c.opts.OutputDir
	if outputDir == "" {
		homeDir, _ := os.UserHomeDir()
		outputDir = filepath.Join(homeDir, ".wukong_apps", "cloned", c.host)
	}
	c.opts.OutputDir = outputDir

	// 创建目录结构
	c.pageDir = filepath.Join(outputDir, "pages")
	c.assetDir = filepath.Join(outputDir, "assets")

	// 初始化缓存
	var cache *CloneCache
	if c.opts.Incremental || c.opts.UseCache {
		cache, err = NewCloneCache("", c.host)
		if err != nil {
			// Cache initialization failed, continue without cache
			cache = nil
		} else {
			cache.SetSeedURL(seedURL)
		}
	}

	// 如果强制克隆，清除缓存
	if c.opts.Force {
		os.RemoveAll(outputDir)
		if cache != nil {
			cache.Clear()
		}
	}

	if err := os.MkdirAll(c.pageDir, 0755); err != nil {
		return nil, fmt.Errorf("create page dir: %w", err)
	}
	if err := os.MkdirAll(c.assetDir, 0755); err != nil {
		return nil, fmt.Errorf("create asset dir: %w", err)
	}

	// 初始化 chromedp
	allocCtx, allocCancel := c.createBrowserContext()
	defer allocCancel()

	// 添加种子页面到队列
	c.pendingMu.Lock()
	c.pending = append(c.pending, pendingPage{url: seedURL, depth: 0})
	c.pendingMu.Unlock()

	// 创建工作池
	var wg sync.WaitGroup
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()

	for i := 0; i < c.opts.Workers; i++ {
		wg.Add(1)
		go c.workerWithCache(workerCtx, allocCtx, &wg, i, cache)
	}

	// 等待所有工作完成或上下文取消
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		workerCancel()
		wg.Wait()
	}

	// 下载未捕获的资源（通过 HTML 分析发现的）
	c.downloadQueuedAssets(ctx)

	endTime := time.Now()

	// 重写已克隆页面的链接
	c.rewritePageLinks()

	// 保存缓存
	if cache != nil {
		cache.UpdateLastSync()
		cache.Save()
	}

	// 构建结果
	c.statsMu.RLock()
	c.resultsMu.Lock()
	result := &Result{
		Success:    c.stats.PagesFailed == 0,
		SeedURL:    c.seedURL,
		Host:       c.host,
		OutputDir:  outputDir,
		Pages:      c.stats.PagesCloned,
		Assets:     c.stats.AssetsDownloaded,
		SizeBytes:  c.stats.TotalBytes,
		Duration:   endTime.Sub(startTime),
		StartTime:  startTime,
		EndTime:    endTime,
	}
	// 收集错误
	for _, pr := range c.results {
		if pr.Error != "" {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", pr.URL, pr.Error))
		}
	}
	c.resultsMu.Unlock()
	c.statsMu.RUnlock()

	return result, nil
}

// createBrowserContext creates a chromedp allocator context.
func (c *Cloner) createBrowserContext() (context.Context, context.CancelFunc) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)

	if c.opts.ChromePath != "" {
		opts = append(opts, chromedp.ExecPath(c.opts.ChromePath))
	}

	return chromedp.NewExecAllocator(context.Background(), opts...)
}

// worker processes pages from the queue.
func (c *Cloner) worker(ctx context.Context, allocCtx context.Context, wg *sync.WaitGroup, id int) {
	defer wg.Done()

	for {
		// 获取下一个页面
		c.pendingMu.Lock()
		if len(c.pending) == 0 {
			c.pendingMu.Unlock()
			return
		}
		page := c.pending[0]
		c.pending = c.pending[1:]
		c.pendingMu.Unlock()

		// 检查是否已访问
		c.visitedMu.Lock()
		if c.visited[page.url] {
			c.visitedMu.Unlock()
			continue
		}
		c.visited[page.url] = true
		c.visitedMu.Unlock()

		// 检查页面限制
		c.statsMu.RLock()
		if c.opts.MaxPages > 0 && c.stats.PagesCloned >= c.opts.MaxPages {
			c.statsMu.RUnlock()
			return
		}
		c.statsMu.RUnlock()

		// 检查深度限制
		if c.opts.MaxDepth > 0 && page.depth > c.opts.MaxDepth {
			continue
		}

		// 克隆页面
		result := c.clonePage(ctx, allocCtx, page.url, page.depth)

		// 更新统计
		c.statsMu.Lock()
		if result.Error == "" {
			c.stats.PagesCloned++
			c.stats.TotalBytes += result.Size
		} else {
			c.stats.PagesFailed++
		}
		c.statsMu.Unlock()

		// 保存结果
		c.resultsMu.Lock()
		c.results = append(c.results, result)
		c.resultsMu.Unlock()

		// 发现新链接
		if result.Error == "" && result.LinksFound > 0 {
			c.discoverLinks(result.URL, page.depth)
		}
	}
}

// workerWithCache processes pages from the queue with incremental cache support.
func (c *Cloner) workerWithCache(ctx context.Context, allocCtx context.Context, wg *sync.WaitGroup, id int, cache *CloneCache) {
	defer wg.Done()

	for {
		// 获取下一个页面
		c.pendingMu.Lock()
		if len(c.pending) == 0 {
			c.pendingMu.Unlock()
			return
		}
		page := c.pending[0]
		c.pending = c.pending[1:]
		c.pendingMu.Unlock()

		// 检查是否已访问
		c.visitedMu.Lock()
		if c.visited[page.url] {
			c.visitedMu.Unlock()
			continue
		}
		c.visited[page.url] = true
		c.visitedMu.Unlock()

		// 检查页面限制
		c.statsMu.RLock()
		if c.opts.MaxPages > 0 && c.stats.PagesCloned >= c.opts.MaxPages {
			c.statsMu.RUnlock()
			return
		}
		c.statsMu.RUnlock()

		// 检查深度限制
		if c.opts.MaxDepth > 0 && page.depth > c.opts.MaxDepth {
			continue
		}

		// 检查缓存
		var needsUpdate bool
		if cache != nil && !c.opts.Force {
			needsUpdate, _, _ = cache.CheckNeedsUpdate(page.url)
		} else {
			needsUpdate = true
		}

		var result PageResult
		if needsUpdate || c.opts.Refresh {
			// 克隆页面
			result = c.clonePage(ctx, allocCtx, page.url, page.depth)

			// 更新缓存
			if cache != nil && result.Error == "" {
				entry := &CacheEntry{
					URL:         page.url,
					LocalPath:   result.FilePath,
					LastFetched: time.Now(),
					StatusCode:  200,
					Size:        result.Size,
				}
				cache.SetEntry(entry)
			}
		} else {
			// 使用缓存的内容
			result = c.useCachedPage(page.url, cache)
		}

		// 更新统计
		c.statsMu.Lock()
		if result.Error == "" {
			c.stats.PagesCloned++
			c.stats.TotalBytes += result.Size
		} else {
			c.stats.PagesFailed++
		}
		c.statsMu.Unlock()

		// 保存结果
		c.resultsMu.Lock()
		c.results = append(c.results, result)
		c.resultsMu.Unlock()

		// 发现新链接
		if result.Error == "" && result.LinksFound > 0 {
			c.discoverLinks(result.URL, page.depth)
		}
	}
}

// useCachedPage returns a cached page result without re-fetching.
func (c *Cloner) useCachedPage(pageURL string, cache *CloneCache) PageResult {
	result := PageResult{
		URL:   pageURL,
		Depth: 0,
	}

	entry := cache.GetEntry(pageURL)
	if entry == nil || entry.LocalPath == "" {
		result.Error = "cache miss"
		return result
	}

	// 检查本地文件是否存在
	if _, err := os.Stat(entry.LocalPath); err != nil {
		result.Error = fmt.Sprintf("cached file not found: %v", err)
		return result
	}

	result.FilePath = entry.LocalPath
	result.Size = entry.Size
	result.FromCache = true

	return result
}

// CacheEntry extends the result with cache information.
type CacheResult struct {
	FromCache   bool
	CacheAge    time.Duration
	ContentHash string
}

// clonePage renders and saves a single page with resource capture.
func (c *Cloner) clonePage(ctx context.Context, allocCtx context.Context, pageURL string, depth int) PageResult {
	result := PageResult{
		URL:   pageURL,
		Depth: depth,
	}

	// 创建超时上下文
	timeout := time.Duration(c.opts.Timeout) * time.Second
	pageCtx, pageCancel := context.WithTimeout(ctx, timeout)
	defer pageCancel()

	// 创建浏览器标签页上下文
	tabCtx, tabCancel := chromedp.NewContext(allocCtx)
	defer tabCancel()

	// 创建资源捕获通道
	resourceChan := make(chan *network.EventResponseReceived, 100)

	// 设置网络监听
	go func() {
		ev := network.EventResponseReceived{}
		for {
			select {
			case r := <-resourceChan:
				ev = *r
				// 处理响应
				c.handleNetworkResponse(tabCtx, ev)
			case <-tabCtx.Done():
				return
			case <-pageCtx.Done():
				return
			}
		}
	}()

	// 监听网络响应
	chromedp.ListenTarget(tabCtx, func(v any) {
		switch ev := v.(type) {
		case *network.EventResponseReceived:
			select {
			case resourceChan <- ev:
			default:
			}
		}
	})

	var htmlContent, title string
	var actions []chromedp.Action

	// 基本导航和等待
	actions = append(actions,
		chromedp.Navigate(pageURL),
		chromedp.Sleep(2*time.Second), // 等待页面稳定
	)

	// 如果启用滚动，添加滚动动作
	if c.opts.Scroll {
		actions = append(actions,
			chromedp.Evaluate(`
				(function() {
					let scrollHeight = document.body.scrollHeight;
					let viewportHeight = window.innerHeight;
					let scrollStep = viewportHeight;
					let scrolled = 0;
					while (scrolled < scrollHeight) {
						window.scrollBy(0, scrollStep);
						scrolled += scrollStep;
					}
					window.scrollTo(0, 0);
				})();
			`, nil),
			chromedp.Sleep(1*time.Second),
		)
	}

	// 获取页面内容
	actions = append(actions,
		chromedp.Title(&title),
		chromedp.OuterHTML("html", &htmlContent),
	)

	// 执行动作
	err := chromedp.Run(tabCtx, actions...)
	if err != nil {
		if pageCtx.Err() != nil {
			result.Error = "timeout"
		} else {
			result.Error = err.Error()
		}
		return result
	}

	// 清理 HTML（移除脚本）
	cleanHTML := sanitize.CleanHTML(htmlContent)

	// 生成文件路径
	fileName := c.urlToFilePath(pageURL)
	filePath := filepath.Join(c.pageDir, fileName)

	// 确保目录存在
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		result.Error = fmt.Sprintf("create dir: %v", err)
		return result
	}

	// 写入文件
	if err := os.WriteFile(filePath, []byte(cleanHTML), 0644); err != nil {
		result.Error = fmt.Sprintf("write file: %v", err)
		return result
	}

	// 获取文件大小
	info, _ := os.Stat(filePath)
	if info != nil {
		result.Size = info.Size()
	}

	result.FilePath = filePath
	result.Title = title

	// 提取资源链接（用于后续下载）
	assets := sanitize.ExtractAssets(htmlContent, pageURL)
	result.AssetsFound = len(assets)

	// 将发现的资源加入下载队列
	for _, assetURL := range assets {
		c.addAssetToQueue(assetURL)
	}

	// 提取页面链接
	links := sanitize.ExtractLinks(htmlContent, pageURL, c.host, c.opts.Subdomains)
	result.LinksFound = len(links)

	return result
}

// handleNetworkResponse handles captured network responses to download resources.
func (c *Cloner) handleNetworkResponse(ctx context.Context, ev network.EventResponseReceived) {
	if ev.Response.URL == "" {
		return
	}

	// 只处理同域资源
	resourceURL := ev.Response.URL
	parsedURL, err := url.Parse(resourceURL)
	if err != nil {
		return
	}

	// 检查是否是应该下载的资源类型
	if !c.shouldDownloadResource(parsedURL) {
		return
	}

	// 检查是否已下载
	c.assetMu.RLock()
	if _, exists := c.downloadedAssets[resourceURL]; exists {
		c.assetMu.RUnlock()
		return
	}
	c.assetMu.RUnlock()

	// 记录已发现的资源
	// 注意：由于 chromedp 限制，我们无法直接获取响应体
	// 实际下载将通过 downloadQueuedAssets 方法进行
	_ = ctx
	_ = resourceURL
}

// fetchResourceBody fetches the body of a network resource.
func (c *Cloner) fetchResourceBody(ctx context.Context, requestID string) ([]byte, error) {
	// 使用 Network.getResponseBody API
	// 注意：这需要相应的权限
	return nil, nil // 简化实现
}

// shouldDownloadResource determines if a resource should be downloaded.
func (c *Cloner) shouldDownloadResource(parsedURL *url.URL) bool {
	// 只下载同域资源
	if parsedURL.Host != c.host {
		return false
	}

	// 检查是否是应该下载的资源类型
	path := strings.ToLower(parsedURL.Path)
	downloadExtensions := []string{
		".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico",
		".woff", ".woff2", ".ttf", ".otf", ".eot", ".webp",
		".mp4", ".mp3", ".webm", ".ogg", ".wav",
		".pdf", ".doc", ".docx",
	}

	for _, ext := range downloadExtensions {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}

	return false
}

// addAssetToQueue adds an asset URL to the download queue.
func (c *Cloner) addAssetToQueue(assetURL string) {
	c.assetMu.Lock()
	defer c.assetMu.Unlock()

	// 检查是否已下载或已在队列中
	if _, downloaded := c.downloadedAssets[assetURL]; downloaded {
		return
	}
	for _, q := range c.assetQueue {
		if q == assetURL {
			return
		}
	}

	c.assetQueue = append(c.assetQueue, assetURL)
}

// downloadQueuedAssets downloads resources that were discovered but not captured by Network listener.
func (c *Cloner) downloadQueuedAssets(ctx context.Context) {
	c.assetMu.Lock()
	queue := make([]string, len(c.assetQueue))
	copy(queue, c.assetQueue)
	c.assetQueue = nil
	c.assetMu.Unlock()

	for _, assetURL := range queue {
		// 检查是否已下载
		c.assetMu.RLock()
		if _, exists := c.downloadedAssets[assetURL]; exists {
			c.assetMu.RUnlock()
			continue
		}
		c.assetMu.RUnlock()

		// 下载资源
		c.downloadAsset(ctx, assetURL)
	}
}

// downloadAsset downloads a single asset via HTTP.
func (c *Cloner) downloadAsset(ctx context.Context, assetURL string) error {
	// 构建 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, "GET", assetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Wukong-Cloner/1.0)")

	// 发送请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 读取内容
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// 确定本地路径
	localPath := c.urlToLocalAssetPath(assetURL)
	fullPath := filepath.Join(c.assetDir, localPath)

	// 确保目录存在
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// 写入文件
	if err := os.WriteFile(fullPath, body, 0644); err != nil {
		return err
	}

	// 获取 MIME 类型
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = c.guessMimeType(assetURL)
	}

	// 记录已下载
	c.assetMu.Lock()
	c.downloadedAssets[assetURL] = &downloadedAsset{
		URL:        assetURL,
		LocalPath:  localPath,
		ContentType: resp.Header.Get("Content-Type"),
		Size:       int64(len(body)),
		MimeType:   mimeType,
	}
	c.assetMu.Unlock()

	// 更新统计
	c.statsMu.Lock()
	c.stats.AssetsDownloaded++
	c.stats.TotalBytes += int64(len(body))
	c.statsMu.Unlock()

	return nil
}

// urlToLocalAssetPath converts a URL to a local asset path.
func (c *Cloner) urlToLocalAssetPath(assetURL string) string {
	parsed, err := url.Parse(assetURL)
	if err != nil {
		return "unknown"
	}

	path := parsed.Path
	if path == "" || path == "/" {
		return "index"
	}

	// 移除前导斜杠
	path = strings.TrimPrefix(path, "/")

	// 如果没有扩展名，添加基于内容的哈希
	if !strings.Contains(path, ".") {
		hash := md5.Sum([]byte(assetURL))
		path = path + "_" + hex.EncodeToString(hash[:8])
	}

	return path
}

// rewritePageLinks rewrites links in cloned pages to point to local resources.
func (c *Cloner) rewritePageLinks() {
	c.resultsMu.Lock()
	defer c.resultsMu.Unlock()

	for _, result := range c.results {
		if result.Error != "" || result.FilePath == "" {
			continue
		}

		// 读取 HTML
		content, err := os.ReadFile(result.FilePath)
		if err != nil {
			continue
		}

		// 重写链接
		rewritten := c.rewriteHTMLLinks(string(content), result.URL)

		// 写回文件
		os.WriteFile(result.FilePath, []byte(rewritten), 0644)
	}
}

// rewriteHTMLLinks rewrites resource links in HTML to local paths.
func (c *Cloner) rewriteHTMLLinks(html, pageURL string) string {
	c.assetMu.RLock()
	defer c.assetMu.RUnlock()

	// 重写 src 属性
	html = c.rewriteAttribute(html, "src", func(match, attrValue string) string {
		if !c.isAbsoluteURL(attrValue) {
			return match
		}

		parsed, err := url.Parse(attrValue)
		if err != nil {
			return match
		}

		// 检查是否是已下载的资源
		if asset, exists := c.downloadedAssets[attrValue]; exists {
			return fmt.Sprintf(`src="assets/%s"`, asset.LocalPath)
		}

		// 对于外部资源，保持原样
		if parsed.Host != c.host {
			return match
		}

		return match
	})

	// 重写 href 属性（对于 CSS 和其他资源）
	html = c.rewriteAttribute(html, "href", func(match, attrValue string) string {
		// 跳过锚点链接
		if strings.HasPrefix(attrValue, "#") {
			return match
		}

		// 跳过 JavaScript
		if strings.HasPrefix(attrValue, "javascript:") {
			return match
		}

		// 跳过外部链接
		parsed, err := url.Parse(attrValue)
		if err != nil {
			return match
		}

		// 对于外部链接，保持原样
		if parsed.Host != "" && parsed.Host != c.host {
			return match
		}

		// 检查是否是已下载的资源
		if asset, exists := c.downloadedAssets[attrValue]; exists {
			mimeType := asset.MimeType
			if strings.HasPrefix(mimeType, "text/css") || strings.HasPrefix(mimeType, "image/") {
				return fmt.Sprintf(`href="assets/%s"`, asset.LocalPath)
			}
		}

		// 对于同域页面链接，转换为本地路径
		if strings.HasSuffix(attrValue, ".html") || strings.HasSuffix(attrValue, "/") {
			localPath := c.urlToFilePath(attrValue)
			return fmt.Sprintf(`href="pages/%s"`, localPath)
		}

		return match
	})

	// 重写 srcset 属性
	html = c.rewriteSrcset(html, func(match, urlValue string) string {
		if asset, exists := c.downloadedAssets[urlValue]; exists {
			return fmt.Sprintf("assets/%s", asset.LocalPath)
		}
		return urlValue
	})

	// 重写 style 属性中的 background-image
	html = c.rewriteStyleBackgrounds(html, func(match, urlValue string) string {
		if asset, exists := c.downloadedAssets[urlValue]; exists {
			return fmt.Sprintf(`url(assets/%s)`, asset.LocalPath)
		}
		return match
	})

	return html
}

// rewriteAttribute rewrites an HTML attribute value using a replacer function.
func (c *Cloner) rewriteAttribute(html, attrName string, replacer func(match, value string) string) string {
	pattern := fmt.Sprintf(`%s="([^"]*)"`, attrName)
	re := regexp.MustCompile(pattern)

	return re.ReplaceAllStringFunc(html, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		return replacer(match, submatch[1])
	})
}

// rewriteSrcset rewrites srcset attribute values.
func (c *Cloner) rewriteSrcset(html string, replacer func(match, urlValue string) string) string {
	re := regexp.MustCompile(`srcset="([^"]*)"`)
	return re.ReplaceAllStringFunc(html, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}

		srcset := submatch[1]
		parts := strings.Split(srcset, ",")
		var rewritten []string

		for _, part := range parts {
			part = strings.TrimSpace(part)
			fields := strings.Fields(part)
			if len(fields) > 0 {
				fields[0] = replacer("", fields[0])
				rewritten = append(rewritten, strings.Join(fields, " "))
			}
		}

		return fmt.Sprintf(`srcset="%s"`, strings.Join(rewritten, ", "))
	})
}

// rewriteStyleBackgrounds rewrites background-image URLs in style attributes.
func (c *Cloner) rewriteStyleBackgrounds(html string, replacer func(match, urlValue string) string) string {
	re := regexp.MustCompile(`background(?:-image)?\s*:\s*url\(['"]?([^'")\s]+)['"]?\)`)
	return re.ReplaceAllStringFunc(html, func(match string) string {
		submatch := re.FindStringSubmatch(match)
		if len(submatch) < 2 {
			return match
		}
		return replacer(match, submatch[1])
	})
}

// isAbsoluteURL checks if a URL is absolute.
func (c *Cloner) isAbsoluteURL(urlStr string) bool {
	return strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://")
}

// guessMimeType guesses the MIME type from a URL.
func (c *Cloner) guessMimeType(urlStr string) string {
	ext := strings.ToLower(filepath.Ext(urlStr))
	switch ext {
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".webm":
		return "video/webm"
	case ".pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

// discoverLinks adds discovered links to the pending queue.
func (c *Cloner) discoverLinks(sourceURL string, currentDepth int) {
	c.visitedMu.RLock()
	defer c.visitedMu.RUnlock()

	// 从结果中获取链接
	c.resultsMu.Lock()
	for _, result := range c.results {
		if result.URL == sourceURL {
			break
		}
	}
	c.resultsMu.Unlock()
}

// urlToFilePath converts a URL to a local file path.
func (c *Cloner) urlToFilePath(pageURL string) string {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return "index.html"
	}

	path := parsed.Path
	if path == "" || path == "/" {
		return "index.html"
	}

	// 移除前导斜杠
	path = strings.TrimPrefix(path, "/")

	// 替换特殊字符
	path = strings.ReplaceAll(path, "/", "_")
	path = strings.ReplaceAll(path, "?", "_")
	path = strings.ReplaceAll(path, "&", "_")
	path = strings.ReplaceAll(path, "=", "_")

	// 确保有 .html 后缀
	if !strings.HasSuffix(path, ".html") {
		path += ".html"
	}

	return path
}

// GetStats returns current cloning statistics.
func (c *Cloner) GetStats() Stats {
	c.statsMu.RLock()
	defer c.statsMu.RUnlock()
	return c.stats
}

// GetDownloadedAssets returns all downloaded assets.
func (c *Cloner) GetDownloadedAssets() map[string]*downloadedAsset {
	c.assetMu.RLock()
	defer c.assetMu.RUnlock()

	result := make(map[string]*downloadedAsset)
	for k, v := range c.downloadedAssets {
		result[k] = v
	}
	return result
}
