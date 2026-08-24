// Package clone provides website cloning functionality.
package clone

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/browser"
	"github.com/km269/wukong/internal/browser/antibot"
	"github.com/km269/wukong/internal/browser/types"
	"github.com/km269/wukong/internal/util"
	"github.com/km269/wukong/pkg/httpclient"
	"github.com/km269/wukong/pkg/logutil"
)

// DefaultDownloadExts returns the default set of file extensions to download.
func DefaultDownloadExts() map[string]bool {
	return map[string]bool{
		".txt":  true,
		".csv":  true,
		".doc":  true,
		".docx": true,
		".xls":  true,
		".xlsx": true,
		".ppt":  true,
		".pptx": true,
		".pdf":  true,
	}
}

// DownloaderOptions configures the file downloader.
type DownloaderOptions struct {
	OutputDir   string
	MaxPages    int
	MaxDepth    int
	Workers     int
	Headless    bool
	Stealth     bool
	Antibot     bool
	Resume      bool
	Force       bool
	Refresh     bool
	FileExts    map[string]bool
	Timeout     time.Duration
	UserAgent   string
	MaxFileSize int64
	Concurrency int

	// InsecureTLS disables TLS certificate verification (opt-out for
	// intranet/.mil certificates). Strict verification is the default.
	InsecureTLS bool
	// TLSCACertPath supplies a PEM CA bundle (e.g. DoD Root CA package) so
	// .mil/.gov certificates verify while strict validation stays enabled.
	TLSCACertPath string
}

// DefaultDownloaderOptions returns sensible defaults for the downloader.
func DefaultDownloaderOptions() DownloaderOptions {
	home, _ := os.UserHomeDir()
	return DownloaderOptions{
		OutputDir:   filepath.Join(home, ".wukong", "apps", "downloads"),
		MaxPages:    50,
		MaxDepth:    0,
		Workers:     4,
		Headless:    true,
		Stealth:     true,
		Antibot:     true,
		Resume:      true,
		FileExts:    DefaultDownloadExts(),
		Timeout:     120 * time.Second,
		UserAgent:   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		MaxFileSize: 100 * 1024 * 1024,
		Concurrency: 4,
	}
}

// FileDownloadResult holds the outcome of a file download operation.
type FileDownloadResult struct {
	Success         bool
	SeedURL         string
	Host            string
	OutputDir       string
	FilesDownloaded int
	FilesSkipped    int
	FilesFailed     int
	TotalSize       int64
	Duration        time.Duration
	StartTime       time.Time
	EndTime         time.Time
	Files           []DownloadedFile
	Errors          []string
	AntibotStats    string
}

// DownloadedFile represents a single downloaded file for CLI display.
type DownloadedFile struct {
	URL         string
	FilePath    string
	FileName    string
	Size        int64
	ContentType string
	Extension   string
	Depth       int
	Error       string
}

// Downloader handles downloading files from websites.
type Downloader struct {
	opts       DownloaderOptions
	httpClient *httpclient.Client
	ctx        context.Context
	cancel     context.CancelFunc

	resultsMu sync.Mutex
	files     []DownloadedFile
	errors    []string

	statsMu    sync.Mutex
	downloaded int
	skipped    int
	failed     int
	totalSize  int64

	visitedMu  sync.Mutex
	visited    map[string]bool
	pagesCount int

	antibot      *antibot.Engine
	currentUA    *antibot.UAProfile
	requestDelay time.Duration

	browserPool types.BrowserBackend
	useBrowser  bool
}

// NewDownloader creates a new file downloader.
func NewDownloader(opts DownloaderOptions) *Downloader {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = opts.Workers
	}
	if opts.MaxFileSize <= 0 {
		opts.MaxFileSize = 100 * 1024 * 1024
	}
	if len(opts.FileExts) == 0 {
		opts.FileExts = DefaultDownloadExts()
	}

	abCfg := antibot.Config{
		Enabled:      opts.Antibot,
		AutoEscalate: true,
		InitialLevel: antibot.LevelNone,
		MaxLevel:     antibot.LevelAggressive,
		MaxRetries:   3,
		Cooldown:     30 * time.Second,
	}

	if opts.Stealth {
		abCfg.InitialLevel = antibot.LevelStealth
	}

	engine := antibot.New(abCfg)
	uaProfile := engine.GetRandomDesktopUA()

	d := &Downloader{
		opts: opts,
		httpClient: httpclient.New(httpclient.Options{
			Timeout:            opts.Timeout,
			InsecureSkipVerify: opts.InsecureTLS, //nolint:gosec // opt-in
			RootCAsPath:        opts.TLSCACertPath,
			ForceIPv4:          true, // avoid IPv6 issues on restricted networks
		}),
		visited:      make(map[string]bool),
		antibot:      engine,
		currentUA:    uaProfile,
		requestDelay: 0,
	}

	if opts.Stealth {
		d.requestDelay = 500 * time.Millisecond
	}

	return d
}

// initBrowser initializes the browser backend for JS-rendered page crawling.
// The browser pool's lifetime is bound to d.ctx: cancelling the download
// context releases the browser even if closeBrowser is somehow skipped.
func (d *Downloader) initBrowser() {
	if !d.opts.Headless {
		return
	}

	logutil.Info("initializing headless browser for page rendering...",
		slog.Int("workers", d.opts.Workers))

	var err error
	d.browserPool, err = browser.NewBackend(d.ctx, browser.BackendChromedp, browser.BackendOptions{
		Headless:         true,
		Workers:          d.opts.Workers,
		Settle:           2 * time.Second,
		RenderTimeout:    d.opts.Timeout,
		Scroll:           true,
		Stealth:          d.opts.Stealth,
		DisableDownloads: true,
	})
	if err != nil {
		logutil.Warn("failed to initialize browser", slog.String("error", err.Error()))
		return
	}
	d.useBrowser = true

	logutil.Info("headless browser ready for page rendering")
}

// closeBrowser closes the browser backend.
func (d *Downloader) closeBrowser() {
	if d.browserPool != nil {
		d.browserPool.Close()
		d.browserPool = nil
		d.useBrowser = false
	}
}

// Download downloads files from the given URL.
func (d *Downloader) Download(ctx context.Context, seedURL string) (*FileDownloadResult, error) {
	startTime := time.Now()
	d.ctx, d.cancel = context.WithCancel(ctx)
	defer d.cancel()

	parsedURL, err := url.Parse(seedURL)
	if err != nil {
		return nil, fmt.Errorf("parse seed URL: %w", err)
	}

	if parsedURL.Scheme == "" {
		parsedURL.Scheme = "https"
		seedURL = "https://" + seedURL
	}

	host := parsedURL.Host

	if d.opts.Force {
		os.RemoveAll(d.opts.OutputDir)
	}

	os.MkdirAll(d.opts.OutputDir, 0755)

	d.initBrowser()
	defer d.closeBrowser()

	var wg sync.WaitGroup
	sem := make(chan struct{}, d.opts.Concurrency)

	wg.Add(1)
	go d.processURL(seedURL, 0, sem, &wg)

	wg.Wait()

	endTime := time.Now()

	d.statsMu.Lock()
	result := &FileDownloadResult{
		Success:         d.failed == 0,
		SeedURL:         seedURL,
		Host:            host,
		OutputDir:       d.opts.OutputDir,
		FilesDownloaded: d.downloaded,
		FilesSkipped:    d.skipped,
		FilesFailed:     d.failed,
		TotalSize:       d.totalSize,
		Duration:        endTime.Sub(startTime),
		StartTime:       startTime,
		EndTime:         endTime,
		AntibotStats:    d.antibot.Stats(),
	}
	d.statsMu.Unlock()

	d.resultsMu.Lock()
	result.Files = make([]DownloadedFile, len(d.files))
	copy(result.Files, d.files)
	result.Errors = make([]string, len(d.errors))
	copy(result.Errors, d.errors)
	d.resultsMu.Unlock()

	return result, nil
}

func (d *Downloader) processURL(targetURL string, depth int, sem chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	if d.opts.MaxDepth > 0 && depth > d.opts.MaxDepth {
		return
	}

	d.visitedMu.Lock()
	if d.visited[targetURL] {
		d.visitedMu.Unlock()
		return
	}
	d.visited[targetURL] = true
	d.pagesCount++
	currentPage := d.pagesCount
	d.visitedMu.Unlock()

	if d.opts.MaxPages > 0 && currentPage > d.opts.MaxPages {
		return
	}

	sem <- struct{}{}
	defer func() { <-sem }()

	if d.ctx.Err() != nil {
		return
	}

	d.applyDelay()

	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return
	}

	ext := strings.ToLower(filepath.Ext(parsedURL.Path))
	if ext != "" && d.opts.FileExts[ext] {
		d.downloadFile(targetURL, parsedURL, depth)
	} else if ext == "" || !d.isDownloadableExt(ext) {
		d.crawlForLinks(targetURL, depth, sem, wg)
	}
}

func (d *Downloader) applyDelay() {
	if d.requestDelay > 0 {
		delay := d.requestDelay + time.Duration(rand.Int63n(int64(d.requestDelay/2)))
		select {
		case <-time.After(delay):
		case <-d.ctx.Done():
		}
	}
}

func (d *Downloader) setupRequest(req *http.Request) {
	ua := d.currentUA
	if ua == nil {
		ua = &antibot.UAProfile{
			UserAgent:       d.opts.UserAgent,
			SecChUa:         `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`,
			SecChUaMobile:   "?0",
			SecChUaPlatform: `"Windows"`,
		}
	}

	req.Header.Set("User-Agent", ua.UserAgent)
	req.Header.Set("Sec-Ch-Ua", ua.SecChUa)
	req.Header.Set("Sec-Ch-Ua-Mobile", ua.SecChUaMobile)
	req.Header.Set("Sec-Ch-Ua-Platform", ua.SecChUaPlatform)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
}

func (d *Downloader) handleAntibotResponse(targetURL string, resp *http.Response, body []byte) (shouldRetry bool) {
	reason, desc := d.antibot.CheckResponse(resp.StatusCode, resp.Header, string(body))
	if reason == antibot.ReasonNone {
		return false
	}

	logutil.Warn("anti-bot detected",
		slog.String("url", targetURL),
		slog.String("reason", string(reason)),
		slog.String("description", desc),
		slog.Int("status", resp.StatusCode))

	retry, delay, newLevel, msg := d.antibot.Escalate(targetURL, reason, resp.StatusCode)
	logutil.Info("antibot escalation",
		slog.String("url", targetURL),
		slog.String("message", msg),
		slog.String("new_level", newLevel.String()))

	if retry {
		d.currentUA = d.antibot.RotateUserAgent()
		d.requestDelay = d.antibot.JitterDelay()

		logutil.Info("waiting before retry",
			slog.String("url", targetURL),
			slog.Duration("delay", delay))

		select {
		case <-time.After(delay):
		case <-d.ctx.Done():
			return false
		}
		return true
	}

	return false
}

func (d *Downloader) isDownloadableExt(ext string) bool {
	return d.opts.FileExts[strings.ToLower(ext)]
}

func (d *Downloader) downloadFile(targetURL string, parsedURL *url.URL, depth int) {
	ext := strings.ToLower(filepath.Ext(parsedURL.Path))

	// Pre-compute file path so we can check Resume before making any
	// network request — saves bandwidth and avoids unnecessary downloads.
	// Extension may be refined later from Content-Type if URL has none.
	fileName := generateFileName(parsedURL.Path, parsedURL.Host, ext)
	filePath := filepath.Join(d.opts.OutputDir, fileName)

	if d.opts.Resume {
		if _, err := os.Stat(filePath); err == nil {
			d.statsMu.Lock()
			d.skipped++
			d.statsMu.Unlock()
			if util.DebugEnabled {
				logutil.Debug("skipping existing file", slog.String("url", targetURL), slog.String("path", filePath))
			}
			return
		}
	}

	maxRetries := 2
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			logutil.Info("retrying download after anti-bot detection",
				slog.String("url", targetURL),
				slog.Int("attempt", attempt))
		}

		req, err := http.NewRequestWithContext(d.ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			d.recordError(targetURL, err)
			return
		}

		d.setupRequest(req)
		req.Header.Set("Accept", "*/*")

		if util.DebugEnabled {
			logutil.Debug("downloading file", slog.String("url", targetURL))
		}

		resp, err := d.httpClient.Do(req)
		if err != nil {
			errReason, _ := d.antibot.CheckError(err)
			if errReason != antibot.ReasonNone && attempt < maxRetries {
				if d.handleAntibotResponse(targetURL, &http.Response{StatusCode: 0}, nil) {
					continue
				}
			}
			d.recordError(targetURL, err)
			return
		}

		// Check Content-Length before reading body — skip oversized files early.
		if resp.ContentLength > 0 && resp.ContentLength > d.opts.MaxFileSize {
			resp.Body.Close()
			d.statsMu.Lock()
			d.skipped++
			d.statsMu.Unlock()
			logutil.Info("skipped file (too large)",
				slog.String("url", targetURL),
				slog.Int64("size", resp.ContentLength),
				slog.Int64("max", d.opts.MaxFileSize))
			return
		}

		// Read body ONCE into memory (limited to MaxFileSize).
		// This fixes the critical bug where body was read twice:
		// first for anti-bot check, then for file writing — the second
		// read got 0 bytes because resp.Body was already consumed/closed.
		body, err := io.ReadAll(io.LimitReader(resp.Body, d.opts.MaxFileSize))
		resp.Body.Close()
		if err != nil {
			d.recordError(targetURL, err)
			return
		}

		// Anti-bot check using the body we just read.
		reason, _ := d.antibot.CheckResponse(resp.StatusCode, resp.Header, string(body))
		if reason != antibot.ReasonNone {
			if attempt < maxRetries && d.handleAntibotResponse(targetURL, resp, body) {
				continue
			}
			d.recordError(targetURL, fmt.Errorf("HTTP %d: %s", resp.StatusCode, reason))
			return
		}

		if resp.StatusCode >= 400 {
			d.recordError(targetURL, fmt.Errorf("HTTP %d", resp.StatusCode))
			return
		}

		// Refine extension from Content-Type if URL had none.
		if ext == "" {
			ext = inferExtFromContentType(resp.Header.Get("Content-Type"))
			fileName = generateFileName(parsedURL.Path, parsedURL.Host, ext)
			filePath = filepath.Join(d.opts.OutputDir, fileName)
		}

		// Write body to file via temp file for atomic writes.
		tmpPath := filePath + ".tmp"
		if err := os.WriteFile(tmpPath, body, 0644); err != nil {
			d.recordError(targetURL, err)
			return
		}

		if err := os.Rename(tmpPath, filePath); err != nil {
			os.Remove(tmpPath)
			d.recordError(targetURL, err)
			return
		}

		actualSize := int64(len(body))
		fileInfo := DownloadedFile{
			URL:         targetURL,
			FilePath:    filePath,
			FileName:    fileName,
			Size:        actualSize,
			ContentType: resp.Header.Get("Content-Type"),
			Extension:   ext,
			Depth:       depth,
		}

		d.resultsMu.Lock()
		d.files = append(d.files, fileInfo)
		d.resultsMu.Unlock()

		d.statsMu.Lock()
		d.downloaded++
		d.totalSize += actualSize
		d.statsMu.Unlock()

		fmt.Printf("  [%d] Downloaded: %s (%s)\n", d.downloaded, fileName, formatSizeInt64(actualSize))
		return
	}
}

func (d *Downloader) crawlForLinks(targetURL string, depth int, sem chan struct{}, wg *sync.WaitGroup) {
	if d.useBrowser && d.browserPool != nil {
		links, err := d.crawlViaBrowser(targetURL)
		if err != nil {
			logutil.Warn("browser rendering failed, falling back to HTTP",
				slog.String("url", targetURL),
				slog.Any("error", err))
		} else if len(links) > 0 {
			d.processExtractedLinks(links, targetURL, depth, sem, wg)
			return
		}
	}

	d.crawlViaHTTP(targetURL, depth, sem, wg)
}

func (d *Downloader) crawlViaBrowser(targetURL string) ([]string, error) {
	if util.DebugEnabled {
		logutil.Debug("browser rendering page", slog.String("url", targetURL))
	}

	// High priority: this render is the user-requested page itself,
	// not background crawl work.
	var renderResult *types.RenderResult
	var err error
	if pr, ok := d.browserPool.(types.PriorityRenderer); ok {
		renderResult, err = pr.RenderWithPriority(d.ctx, targetURL, "", types.PriorityHigh)
	} else {
		renderResult, err = d.browserPool.Render(d.ctx, targetURL)
	}
	if err != nil {
		return nil, err
	}

	var allLinks []string
	seen := make(map[string]bool)

	addLink := func(link string) {
		link = strings.TrimSpace(link)
		if link == "" || strings.HasPrefix(link, "#") ||
			strings.HasPrefix(link, "javascript:") ||
			strings.HasPrefix(link, "mailto:") ||
			strings.HasPrefix(link, "tel:") {
			return
		}
		if !seen[link] {
			seen[link] = true
			allLinks = append(allLinks, link)
		}
	}

	for _, link := range renderResult.ExtractedLinks {
		addLink(link)
	}

	htmlLinks := extractLinks(renderResult.HTML, targetURL)
	for _, link := range htmlLinks {
		addLink(link)
	}

	if util.DebugEnabled {
		logutil.Debug("browser rendered page",
			slog.String("url", targetURL),
			slog.Int("browser_links", len(renderResult.ExtractedLinks)),
			slog.Int("html_links", len(htmlLinks)),
			slog.Int("total_unique", len(allLinks)))
	}

	return allLinks, nil
}

func (d *Downloader) crawlViaHTTP(targetURL string, depth int, sem chan struct{}, wg *sync.WaitGroup) {
	maxRetries := 2
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			logutil.Info("retrying page crawl after anti-bot detection",
				slog.String("url", targetURL),
				slog.Int("attempt", attempt))
		}

		req, err := http.NewRequestWithContext(d.ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			d.recordError(targetURL, err)
			return
		}

		d.setupRequest(req)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		if util.DebugEnabled {
			logutil.Debug("crawling for links (HTTP)", slog.String("url", targetURL), slog.Int("depth", depth))
		}

		resp, err := d.httpClient.Do(req)
		if err != nil {
			errReason, _ := d.antibot.CheckError(err)
			if errReason != antibot.ReasonNone && attempt < maxRetries {
				if d.handleAntibotResponse(targetURL, &http.Response{StatusCode: 0}, nil) {
					continue
				}
			}
			d.recordError(targetURL, err)
			return
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		resp.Body.Close()
		if err != nil {
			d.recordError(targetURL, err)
			return
		}

		reason, _ := d.antibot.CheckResponse(resp.StatusCode, resp.Header, string(body))
		if reason != antibot.ReasonNone {
			if attempt < maxRetries && d.handleAntibotResponse(targetURL, resp, body) {
				continue
			}
			if util.DebugEnabled {
				logutil.Debug("skip page due to anti-bot detection",
					slog.String("url", targetURL),
					slog.String("reason", string(reason)))
			}
			return
		}

		if resp.StatusCode >= 400 {
			if util.DebugEnabled {
				logutil.Debug("skip page due to status code", slog.String("url", targetURL), slog.Int("status", resp.StatusCode))
			}
			return
		}

		links := extractLinks(string(body), targetURL)

		if util.DebugEnabled {
			logutil.Debug("extracted links (HTTP)", slog.String("url", targetURL), slog.Int("count", len(links)))
		}

		d.processExtractedLinks(links, targetURL, depth, sem, wg)
		return
	}
}

func (d *Downloader) processExtractedLinks(links []string, baseURL string, depth int, sem chan struct{}, wg *sync.WaitGroup) {
	baseParsed, _ := url.Parse(baseURL)

	processedCount := 0
	filteredCount := 0
	for _, link := range links {
		if d.ctx.Err() != nil {
			return
		}

		absURL := resolveURL(baseParsed, link)
		if absURL == "" {
			filteredCount++
			continue
		}

		linkParsed, err := url.Parse(absURL)
		if err != nil {
			filteredCount++
			continue
		}

		ext := strings.ToLower(filepath.Ext(linkParsed.Path))

		// Downloadable files: allow cross-host downloads.
		// Many sites host files on CDNs (e.g. media.defense.gov),
		// so we must not filter them by host.
		if ext != "" && d.opts.FileExts[ext] {
			if util.DebugEnabled {
				logutil.Debug("found downloadable file", slog.String("url", linkParsed.String()), slog.String("ext", ext))
			}
			wg.Add(1)
			go d.processURL(linkParsed.String(), depth+1, sem, wg)
			processedCount++
			continue
		}

		// HTML pages: restrict to same host to prevent crawling
		// the entire internet.
		if !isSameHost(baseParsed.Host, linkParsed.Host) {
			if util.DebugEnabled {
				logutil.Debug("filtered cross-host page link",
					slog.String("base", baseParsed.Host),
					slog.String("link", linkParsed.Host),
					slog.String("url", absURL))
			}
			filteredCount++
			continue
		}

		if d.isHTMLPage(linkParsed) {
			if d.opts.MaxDepth == 0 || depth < d.opts.MaxDepth {
				wg.Add(1)
				go d.processURL(linkParsed.String(), depth+1, sem, wg)
				processedCount++
			} else {
				filteredCount++
			}
		} else {
			filteredCount++
		}
	}

	if util.DebugEnabled {
		logutil.Debug("processed links", slog.String("url", baseURL), slog.Int("processed", processedCount), slog.Int("filtered", filteredCount))
	}
}

func (d *Downloader) isHTMLPage(u *url.URL) bool {
	ext := strings.ToLower(filepath.Ext(u.Path))
	if ext == "" || ext == ".html" || ext == ".htm" || ext == ".php" || ext == ".asp" || ext == ".aspx" || ext == ".jsp" {
		return true
	}
	return false
}

func (d *Downloader) recordError(url string, err error) {
	d.statsMu.Lock()
	d.failed++
	d.statsMu.Unlock()

	errMsg := fmt.Sprintf("%s: %v", url, err)
	d.resultsMu.Lock()
	d.errors = append(d.errors, errMsg)
	d.resultsMu.Unlock()

	logutil.Warn("download failed", slog.String("url", url), slog.Any("error", err))
}

func inferExtFromContentType(contentType string) string {
	contentType = strings.Split(contentType, ";")[0]
	switch contentType {
	case "application/pdf":
		return ".pdf"
	case "application/msword":
		return ".doc"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.ms-excel":
		return ".xls"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/vnd.ms-powerpoint":
		return ".ppt"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	default:
		return ".bin"
	}
}

func generateFileName(urlPath, host, ext string) string {
	base := filepath.Base(urlPath)
	if base == "/" || base == "." || base == "" {
		base = "index"
	}

	base = strings.ReplaceAll(base, "/", "_")
	base = strings.ReplaceAll(base, "\\", "_")
	base = strings.ReplaceAll(base, "?", "_")
	base = strings.ReplaceAll(base, "#", "_")
	base = strings.ReplaceAll(base, ":", "_")
	base = strings.ReplaceAll(base, "*", "_")
	base = strings.ReplaceAll(base, "\"", "_")
	base = strings.ReplaceAll(base, "<", "_")
	base = strings.ReplaceAll(base, ">", "_")
	base = strings.ReplaceAll(base, "|", "_")

	if len(base) > 100 {
		base = base[:100]
	}

	if ext != "" && !strings.HasSuffix(strings.ToLower(base), ext) {
		base += ext
	}

	return base
}

func isSameHost(host1, host2 string) bool {
	if host1 == host2 {
		return true
	}

	if strings.HasPrefix(host1, "www.") && host1[4:] == host2 {
		return true
	}
	if strings.HasPrefix(host2, "www.") && host2[4:] == host1 {
		return true
	}

	return false
}

func resolveURL(base *url.URL, link string) string {
	if link == "" || strings.HasPrefix(link, "#") || strings.HasPrefix(link, "javascript:") || strings.HasPrefix(link, "mailto:") || strings.HasPrefix(link, "tel:") || strings.HasPrefix(link, "data:") {
		return ""
	}

	if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
		return link
	}

	if base == nil {
		return ""
	}

	ref, err := url.Parse(link)
	if err != nil {
		return ""
	}

	resolved := base.ResolveReference(ref)
	return resolved.String()
}

// Pre-compiled regex patterns for link extraction.
// Previously these were recompiled on every call to extractLinks,
// which is wasteful for large HTML pages crawled across thousands of pages.
var (
	hrefPattern   = regexp.MustCompile(`href\s*=\s*["']([^"']+)["']`)
	srcPattern    = regexp.MustCompile(`src\s*=\s*["']([^"']+)["']`)
	actionPattern = regexp.MustCompile(`action\s*=\s*["']([^"']+)["']`)
	// data-src and data-lazy-src for lazy-loaded images
	dataSrcPattern     = regexp.MustCompile(`data-src\s*=\s*["']([^"']+)["']`)
	dataLazySrcPattern = regexp.MustCompile(`data-lazy-src\s*=\s*["']([^"']+)["']`)
)

func extractLinks(html, baseURL string) []string {
	var links []string
	seen := make(map[string]bool)

	patterns := []*regexp.Regexp{
		hrefPattern,
		srcPattern,
		actionPattern,
		// Extract lazy-loaded image URLs
		dataSrcPattern,
		dataLazySrcPattern,
	}

	for _, re := range patterns {
		matches := re.FindAllStringSubmatch(html, -1)
		for _, match := range matches {
			if len(match) > 1 {
				link := strings.TrimSpace(match[1])
				if link == "" || strings.HasPrefix(link, "#") || strings.HasPrefix(link, "javascript:") {
					continue
				}
				if !seen[link] {
					seen[link] = true
					links = append(links, link)
				}
			}
		}
	}

	return links
}

func formatSizeInt64(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}
