package rodbackend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/km269/wukong/internal/browser/antibot"
	"github.com/km269/wukong/internal/browser/behavior"
	"github.com/km269/wukong/internal/browser/renderkit"
	"github.com/km269/wukong/internal/browser/stealth"
	"github.com/km269/wukong/internal/browser/types"
	"github.com/km269/wukong/pkg/logutil"
	"github.com/ysmood/gson"
)

type Options struct {
	Headless         bool
	Workers          int
	Settle           time.Duration
	RenderTimeout    time.Duration
	Scroll           bool
	ChromeBin        string
	ControlURL       string
	Stealth          bool
	ProfileDir       string
	DisableDownloads bool
	Proxy            string // Proxy URL (http://user:pass@host:port or socks5://...)
	// InsecureTLS disables Chrome's certificate verification. Strict
	// verification is the default; enable only for intranet/.mil hosts
	// whose certs chain to a non-public root CA.
	InsecureTLS bool
}

type Pool struct {
	opts               Options
	browser            *rod.Browser
	lifeCancel         context.CancelFunc
	workers            []*worker
	disp               *renderkit.Dispatcher
	mu                 sync.Mutex
	behaviorSimEnabled bool
	behaviorSimulator  *behavior.Simulator
	escalator          *antibot.Escalator
	currentUA          *antibot.UAProfile

	refererMu    sync.Mutex
	refererUseMu sync.Mutex
	refererPages map[string]*rod.Page
}

// Compile-time capability assertions.
var (
	_ types.BrowserBackend   = (*Pool)(nil)
	_ types.UARotator        = (*Pool)(nil)
	_ types.AssetCollector   = (*Pool)(nil)
	_ types.PriorityRenderer = (*Pool)(nil)
)

type worker struct {
	idx  int
	page *rod.Page
}

// New creates a rod-backed browser pool whose lifetime is bound to
// ctx: when ctx is cancelled (parent task done, Ctrl-C, timeout) the
// pool drains in-flight renders and closes the rod browser
// automatically, so a caller that forgets Close() cannot leak browser
// processes. Close() remains the explicit cleanup path; it is
// idempotent and also releases the internal lifecycle goroutine.
func New(ctx context.Context, opts Options) (*Pool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Settle <= 0 {
		opts.Settle = 2 * time.Second
	}
	if opts.RenderTimeout <= 0 {
		opts.RenderTimeout = 60 * time.Second
	}

	var controlURL string
	if opts.ControlURL != "" {
		controlURL = opts.ControlURL
	} else {
		l := launcher.New()
		if opts.ChromeBin != "" {
			l = l.Bin(opts.ChromeBin)
		} else if chromePath := FindChromePath(); chromePath != "" {
			l = l.Bin(chromePath)
		}
		// Use new headless mode (Chrome 112+) which behaves much closer
		// to a real browser and is less likely to trigger detection.
		if opts.Headless {
			l = l.HeadlessNew(true)
		} else {
			l = l.Headless(false)
		}
		l = l.Set("start-maximized", "")
		l = l.Set("disable-ipv6", "")
		l = l.Set("disable-gpu", "")
		l = l.Set("disable-http-cache", "")
		// Only ignore certificate errors when InsecureTLS is enabled —
		// .mil/.gov sites use DoD certificates not in the standard store.
		// Otherwise Chrome performs strict (default) verification.
		if opts.InsecureTLS {
			l = l.Set("ignore-certificate-errors", "")
			logutil.Debug("[rod] browser TLS: certificate verification disabled (insecure_tls)")
		} else {
			logutil.Debug("[rod] browser TLS: strict certificate verification")
		}
		// Disable Safe Browsing to prevent ERR_BLOCKED_BY_CLIENT
		// when navigating directly to binary resources (images, etc.).
		l = l.Set("safebrowsing-disable-download-protection", "")
		l = l.Set("safebrowsing-disable-extension-blacklist", "")
		l = l.Set("safebrowsing-manual-protection-did-opt-out", "")
		l = l.Set("disable-features", "SafeBrowsing,IsolateOrigins")
		l = l.Set("disable-background-networking", "")
		l = l.Set("disable-component-update", "")
		l = l.Set("no-pings", "")
		l = l.Set("disable-breakpad", "")
		l = l.Set("disable-client-side-phishing-detection", "")
		l = l.Set("download-default-directory", "")
		// Important: Use a unique temporary user data directory to avoid conflicts
		// with existing Chrome instances. This is the most common cause of
		// "Failed to get the debug url" errors.
		if opts.ProfileDir != "" {
			l = l.UserDataDir(opts.ProfileDir)
		} else {
			// Let rod create a temp dir automatically, which handles isolation
			l = l.UserDataDir("")
		}
		if needNoSandbox() {
			l = l.NoSandbox(true)
		}
		l = l.Devtools(false)
		if opts.Proxy != "" {
			l = l.Proxy(opts.Proxy)
		}
		var err error
		// Retry launch up to 3 times with short delays. The most common failure
		// is "Failed to get the debug url" which happens when Chrome is slow to
		// start (e.g. many existing Chrome processes, system under load) or when
		// a previous rod instance left a stale lock file in the temp user-data-dir.
		maxLaunchRetries := 3
		for attempt := 1; attempt <= maxLaunchRetries; attempt++ {
			controlURL, err = l.Launch()
			if err == nil {
				break
			}
			if attempt < maxLaunchRetries {
				logutil.Warn("rod launch attempt failed, retrying",
					slog.Int("attempt", attempt),
					slog.Int("max", maxLaunchRetries),
					slog.Any("error", err))
				time.Sleep(2 * time.Second)
			}
		}
		if err != nil {
			// Provide helpful diagnostic information
			chromePath := opts.ChromeBin
			if chromePath == "" {
				chromePath = FindChromePath()
			}
			if chromePath == "" {
				return nil, fmt.Errorf("failed to launch Chrome via rod: %w. No Chrome/Chromium found on the system. Please install Chrome or set CHROME_PATH environment variable", err)
			}
			return nil, fmt.Errorf("failed to launch Chrome via rod (path: %s): %w", chromePath, err)
		}
	}

	browserInstance := rod.New().ControlURL(controlURL).MustConnect()

	lifeCtx, lifeCancel := context.WithCancel(ctx)

	escalator := antibot.NewEscalator(antibot.DefaultEscalatorConfig())
	currentUA := escalator.GetRandomDesktopUA()

	p := &Pool{
		opts:              opts,
		browser:           browserInstance,
		lifeCancel:        lifeCancel,
		disp:              renderkit.NewDispatcher(opts.Workers),
		behaviorSimulator: behavior.New(behavior.DefaultConfig()),
		escalator:         escalator,
		currentUA:         currentUA,
		refererPages:      make(map[string]*rod.Page),
	}

	for i := 0; i < opts.Workers; i++ {
		p.workers = append(p.workers, &worker{idx: i})
	}
	p.disp.Start(opts.Workers, func(idx int, job *renderkit.RenderJob) {
		p.renderJob(p.workers[idx], job)
	})

	// Bind the pool lifetime to ctx: cancellation drains the pool and
	// closes the rod browser. Close() cancels lifeCtx first, so after
	// an explicit Close the goroutine's re-entrant Close call hits the
	// Drain() guard and is a no-op.
	go func() {
		<-lifeCtx.Done()
		p.Close()
	}()

	return p, nil
}

func (p *Pool) getCurrentUA() *antibot.UAProfile {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.currentUA
}

func (p *Pool) RotateUA() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentUA = p.escalator.RotateUserAgent()
}

// CollectsAssets reports the render-time network tracking capability:
// every render captures subresource response bodies into
// RenderResult.CollectedAssets (and XHR/fetch endpoints into
// DiscoveredAPIs).
func (p *Pool) CollectsAssets() bool { return true }

// Screenshot navigates to url in a fresh tab and captures a real pixel
// screenshot as PNG, written to outputPath. It applies the same UA override,
// stealth injection and settle-wait as Render so the captured page matches
// a real browsing session.
func (p *Pool) Screenshot(
	ctx context.Context, url string, outputPath string,
) (string, error) {
	p.mu.Lock()
	if p.disp.Closed() {
		p.mu.Unlock()
		return "", fmt.Errorf("pool closed")
	}
	p.mu.Unlock()

	ssCtx, cancel := context.WithTimeout(ctx, p.opts.RenderTimeout)
	defer cancel()

	page, err := p.browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return "", fmt.Errorf("create page: %w", err)
	}
	defer page.Close()
	page = page.Context(ssCtx)

	// Inject stealth scripts to hide automation indicators.
	if p.opts.Stealth {
		_, err := proto.PageAddScriptToEvaluateOnNewDocument{
			Source: stealth.Script,
		}.Call(page)
		if err != nil {
			logutil.Warn("screenshot stealth injection warning", slog.Any("error", err))
		}
	}

	ua := p.getCurrentUA()
	if ua != nil {
		uaOverride := proto.NetworkSetUserAgentOverride{
			UserAgent: ua.UserAgent,
		}
		uaOverride.Call(page)

		proto.NetworkSetExtraHTTPHeaders{
			Headers: proto.NetworkHeaders{
				"Accept":                    gson.New("text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"),
				"Upgrade-Insecure-Requests": gson.New("1"),
			},
		}.Call(page)
	}

	if err := page.Navigate(url); err != nil {
		return "", fmt.Errorf("screenshot navigate: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return "", fmt.Errorf("screenshot waitload: %w", err)
	}
	// Let the page reach network-idle before capturing.
	_ = page.WaitRequestIdle(p.opts.Settle, nil, nil, nil)

	// Capture a full-page real pixel PNG via Page.captureScreenshot.
	png, err := page.Screenshot(true, &proto.PageCaptureScreenshot{
		Format: proto.PageCaptureScreenshotFormatPng,
	})
	if err != nil {
		return "", fmt.Errorf("screenshot capture: %w", err)
	}
	if len(png) == 0 {
		return "", fmt.Errorf("screenshot capture: empty image data")
	}

	if err := os.WriteFile(outputPath, png, 0o644); err != nil {
		return "", fmt.Errorf("screenshot write: %w", err)
	}

	return outputPath, nil
}

func (p *Pool) renderJob(w *worker, job *renderkit.RenderJob) {
	ctx, cancel := context.WithTimeout(job.Ctx, p.opts.RenderTimeout)
	defer cancel()

	var page *rod.Page
	if w.page != nil {
		page = w.page.Context(ctx)
	}

	if page == nil {
		var err error
		page, err = p.browser.Page(proto.TargetCreateTarget{})
		if err != nil {
			job.ResultCh <- renderkit.RenderResultOrErr{Err: fmt.Errorf("create page: %w", err)}
			return
		}
		if p.opts.Stealth {
			// 注入我们增强的 stealth 脚本
			_, err := proto.PageAddScriptToEvaluateOnNewDocument{
				Source: stealth.Script,
			}.Call(page)
			if err != nil {
				logutil.Warn("stealth script injection warning", slog.Any("error", err))
			}
		}
		if p.opts.DisableDownloads {
			// Use page-level download behavior instead of browser-level,
			// so that DownloadAsset pages can override it with allow.
			proto.PageSetDownloadBehavior{
				Behavior: proto.PageSetDownloadBehaviorBehaviorDeny,
			}.Call(page)
		}
		w.page = page
	}

	var contentType string
	var cfClearance string
	var finalURL string

	// Test: only enable event listening, no body collection
	// Track all network requests to collect their response bodies later.
	type trackedResponse struct {
		requestID    proto.NetworkRequestID
		url          string
		mimeType     string
		status       int
		resourceType string // CDP resource type: XHR, Fetch, Document, etc.
	}
	var tracked []*trackedResponse
	var trackedMu sync.Mutex

	// Enable Network domain and listen for responses.
	_ = page.EachEvent(func(e *proto.NetworkResponseReceived) {
		if e.Response == nil {
			return
		}
		trackedMu.Lock()
		tracked = append(tracked, &trackedResponse{
			requestID:    e.RequestID,
			url:          e.Response.URL,
			mimeType:     e.Response.MIMEType,
			status:       int(e.Response.Status),
			resourceType: string(e.Type),
		})
		trackedMu.Unlock()
	})

	go func() {
		<-ctx.Done()
		page.StopLoading()
	}()

	ua := p.getCurrentUA()

	// Simulate a real browser typing URL in the address bar.
	// Do NOT set Sec-Fetch-* headers manually — Chrome sets these automatically.
	// Setting them manually can trigger Chrome's security checks and cause
	// ERR_BLOCKED_BY_CLIENT. Use the same minimal-header pattern as
	// downloadAssetViaNavigation for consistency.
	uaOverride := proto.NetworkSetUserAgentOverride{
		UserAgent: ua.UserAgent,
	}
	if ua.SecChUa != "" {
		uaOverride.AcceptLanguage = "en-US,en;q=0.9"
	}
	uaOverride.Call(page)

	headers := proto.NetworkHeaders{
		"Accept":                    gson.New("text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"),
		"Upgrade-Insecure-Requests": gson.New("1"),
	}
	proto.NetworkSetExtraHTTPHeaders{
		Headers: headers,
	}.Call(page)

	if err := page.Navigate(job.URL); err != nil {
		job.ResultCh <- renderkit.RenderResultOrErr{Err: fmt.Errorf("navigate: %w", err)}
		return
	}

	if err := page.WaitLoad(); err != nil {
		job.ResultCh <- renderkit.RenderResultOrErr{Err: fmt.Errorf("wait load: %w", err)}
		return
	}

	if p.opts.Settle > 0 {
		page.WaitRequestIdle(p.opts.Settle, nil, nil, nil)
	}

	// 如果启用了行为模拟
	if p.behaviorSimEnabled {
		// 模拟自然的滚动和鼠标移动
		page.Eval(renderkit.BehaviorSimScrollJS)
		page.Eval(renderkit.BehaviorSimMouseJS)
	}

	if p.opts.Scroll {
		page.Eval(renderkit.ScrollJS)
		if p.opts.Settle > 0 {
			page.WaitRequestIdle(p.opts.Settle, nil, nil, nil)
		}
	}

	finalURLResult, err := page.Eval(`() => window.location.href`)
	if err == nil && finalURLResult != nil && !finalURLResult.Value.Nil() {
		finalURL = finalURLResult.Value.String()
	}
	if finalURL == "" {
		finalURL = job.URL
	}

	contentTypeResult, err := page.Eval(`() => document.contentType`)
	if err == nil && contentTypeResult != nil && !contentTypeResult.Value.Nil() {
		contentType = contentTypeResult.Value.String()
	}

	if contentType != "" && !renderkit.IsHTMLContentType(contentType) {
		job.ResultCh <- renderkit.RenderResultOrErr{Err: &types.ErrNotHTML{URL: job.URL, ContentType: contentType}}
		return
	}

	// Collect assets by fetching them in the page context via JS.
	// This is more reliable than Network.getResponseBody which can hang.
	collectedAssets := make(map[string]*types.CollectedAsset)
	var discoveredAPIs []types.DiscoveredAPI
	{
		trackedMu.Lock()
		allTracked := make([]*trackedResponse, len(tracked))
		copy(allTracked, tracked)
		trackedMu.Unlock()

		const maxAssetSize = 10 * 1024 * 1024 // 10 MB max per asset
		maxAssetsPerPage := 200               // cap to avoid too much time
		perAssetTimeout := 5 * time.Second

		// Build a list of asset URLs to fetch.
		type assetToFetch struct {
			url      string
			mimeType string
		}
		var toFetch []assetToFetch
		seen := make(map[string]bool)

		for _, t := range allTracked {
			if len(toFetch) >= maxAssetsPerPage {
				break
			}
			// Skip the main document
			if t.url == finalURL || t.url == job.URL {
				continue
			}
			// Only collect successful responses
			if t.status < 200 || t.status >= 300 {
				continue
			}
			// Filter by MIME type - be more permissive
			mt := strings.ToLower(t.mimeType)
			isAsset := strings.HasPrefix(mt, "image/") ||
				strings.HasPrefix(mt, "text/css") ||
				strings.HasPrefix(mt, "application/javascript") ||
				strings.HasPrefix(mt, "text/javascript") ||
				strings.HasPrefix(mt, "application/font") ||
				strings.HasPrefix(mt, "font/") ||
				strings.HasPrefix(mt, "application/json") ||
				strings.HasPrefix(mt, "image/svg+xml") ||
				strings.Contains(mt, "octet-stream")
			if !isAsset {
				// Also check by file extension
				lowURL := strings.ToLower(t.url)
				if strings.HasSuffix(lowURL, ".css") ||
					strings.HasSuffix(lowURL, ".js") ||
					strings.HasSuffix(lowURL, ".png") ||
					strings.HasSuffix(lowURL, ".jpg") ||
					strings.HasSuffix(lowURL, ".jpeg") ||
					strings.HasSuffix(lowURL, ".gif") ||
					strings.HasSuffix(lowURL, ".svg") ||
					strings.HasSuffix(lowURL, ".webp") ||
					strings.HasSuffix(lowURL, ".ico") ||
					strings.HasSuffix(lowURL, ".woff") ||
					strings.HasSuffix(lowURL, ".woff2") ||
					strings.HasSuffix(lowURL, ".ttf") ||
					strings.HasSuffix(lowURL, ".otf") {
					isAsset = true
				}
			}
			if !isAsset {
				continue
			}
			if seen[t.url] {
				continue
			}
			seen[t.url] = true
			toFetch = append(toFetch, assetToFetch{url: t.url, mimeType: t.mimeType})
		}

		var successCount, failCount int

		// Fetch assets concurrently inside a single page JS evaluation.
		// Each chunk issues one CDP Eval whose JS body runs Promise.allSettled
		// over the chunk's URLs, so the browser fetches them truly in parallel
		// (fetch has no hard browser-side concurrency cap and reuses the page's
		// cookies/credentials). Chunking bounds memory: we never hold more than
		// chunkSize base64 bodies in flight, and per-asset size is capped below.
		const chunkSize = 10
		for start := 0; start < len(toFetch); start += chunkSize {
			// Check context cancellation.
			select {
			case <-ctx.Done():
				goto doneCollecting
			default:
			}

			end := start + chunkSize
			if end > len(toFetch) {
				end = len(toFetch)
			}
			chunk := toFetch[start:end]

			// Build a JS async function that fetches every URL in the chunk
			// concurrently and returns [{url, b64|null}] as JSON. Each fetch
			// has its own AbortController so one hung asset cannot stall the
			// whole chunk beyond perAssetTimeout.
			var jsBuf strings.Builder
			jsBuf.WriteString("(async () => {\n")
			jsBuf.WriteString("  const fetchOne = async (url, maxSize) => {\n")
			jsBuf.WriteString("    const controller = new AbortController();\n")
			jsBuf.WriteString("    const timeoutId = setTimeout(() => controller.abort(), ")
			jsBuf.WriteString(strconv.Itoa(int(perAssetTimeout.Milliseconds())))
			jsBuf.WriteString(");\n")
			jsBuf.WriteString("    try {\n")
			jsBuf.WriteString("      const resp = await fetch(url, { signal: controller.signal, credentials: 'include', mode: 'cors', cache: 'force-cache' }).catch(() => null);\n")
			jsBuf.WriteString("      if (!resp || !resp.ok) return null;\n")
			jsBuf.WriteString("      const buf = await resp.arrayBuffer();\n")
			jsBuf.WriteString("      if (buf.byteLength > maxSize) return null;\n")
			jsBuf.WriteString("      const bytes = new Uint8Array(buf);\n")
			jsBuf.WriteString("      let binary = '';\n")
			jsBuf.WriteString("      for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);\n")
			jsBuf.WriteString("      return btoa(binary);\n")
			jsBuf.WriteString("    } catch (e) { return null; } finally { clearTimeout(timeoutId); }\n")
			jsBuf.WriteString("  };\n")
			jsBuf.WriteString("  return JSON.stringify(await Promise.all([\n")
			for i, af := range chunk {
				if i > 0 {
					jsBuf.WriteString(",\n")
				}
				jsBuf.WriteString("    fetchOne(")
				jsBuf.WriteString(strconv.Quote(af.url))
				jsBuf.WriteString(", ")
				jsBuf.WriteString(strconv.Itoa(maxAssetSize))
				jsBuf.WriteString(")")
			}
			jsBuf.WriteString("\n  ]));\n})")

			assetCtx, cancel := context.WithTimeout(ctx, perAssetTimeout+2*time.Second)
			result, evalErr := page.Context(assetCtx).Eval(jsBuf.String())
			cancel()

			if evalErr != nil || result == nil || result.Value.Nil() {
				// The whole chunk failed (e.g. page navigated away). Mark each
				// asset in this chunk as failed and move on.
				failCount += len(chunk)
				continue
			}

			type chunkAsset struct {
				URL string      `json:"url"`
				B64 interface{} `json:"b64"`
			}
			var chunkResults []chunkAsset
			if err := json.Unmarshal([]byte(result.Value.String()), &chunkResults); err != nil ||
				len(chunkResults) != len(chunk) {
				failCount += len(chunk)
				continue
			}

			for i, cr := range chunkResults {
				b64s, ok := cr.B64.(string)
				if !ok || b64s == "" {
					failCount++
					continue
				}
				bodyBytes, derr := base64.StdEncoding.DecodeString(b64s)
				if derr != nil || len(bodyBytes) == 0 || len(bodyBytes) > maxAssetSize {
					failCount++
					continue
				}
				collectedAssets[chunk[i].url] = &types.CollectedAsset{
					URL:         chunk[i].url,
					Body:        bodyBytes,
					ContentType: chunk[i].mimeType,
					StatusCode:  200,
				}
				successCount++
			}
		}
	doneCollecting:

		logutil.Info("asset collection", slog.Int("tracked", len(allTracked)), slog.Int("toFetch", len(toFetch)), slog.Int("success", successCount), slog.Int("failed", failCount))

		// Discover hidden API endpoints from tracked network responses.
		// Filters for XHR/Fetch requests returning structured data
		// (JSON/XML/GraphQL), excluding static assets and the main document.
		infos := make([]networkResponseInfo, len(allTracked))
		for i, t := range allTracked {
			infos[i] = networkResponseInfo{
				URL:          t.url,
				MimeType:     t.mimeType,
				Status:       t.status,
				ResourceType: t.resourceType,
			}
		}
		discoveredAPIs = discoverAPIs(infos, finalURL)
	}

	html, err := page.HTML()
	if err != nil {
		job.ResultCh <- renderkit.RenderResultOrErr{Err: fmt.Errorf("get HTML: %w", err)}
		return
	}

	// Extract all links from the rendered DOM using JavaScript.
	// This captures dynamically generated links that static HTML parsing may miss.
	var extractedLinks []string
	linksVal, evalErr := page.Eval(renderkit.CollectLinksJSONJS)
	if evalErr != nil {
		logutil.Debug("eval links failed", slog.Any("error", evalErr))
	} else if linksVal != nil && !linksVal.Value.Nil() {
		jsonStr := linksVal.Value.String()
		// The Value.String() adds quotes around strings, so we need to trim them.
		jsonStr = strings.Trim(jsonStr, "\"")
		// Also handle escaped quotes.
		if unquoted, err := strconv.Unquote(linksVal.Value.String()); err == nil {
			jsonStr = unquoted
		}
		if jsonStr != "" {
			var links []string
			if err := json.Unmarshal([]byte(jsonStr), &links); err == nil {
				extractedLinks = links
			} else {
				logutil.Debug("failed to parse links JSON", slog.Any("error", err), slog.String("json", jsonStr[:100]))
			}
		}
	}
	logutil.Debug("extracted links from page", slog.Int("count", len(extractedLinks)))

	var title string
	// Bound the element wait: pages without a <title> (minimal pages,
	// error pages) would otherwise block Element() forever and hang the
	// render until the caller's context expires.
	titleEl, err := page.Timeout(2 * time.Second).Element("title")
	if err == nil {
		title, err = titleEl.Text()
		if err != nil {
			title = ""
		}
	}

	cookies, err := proto.NetworkGetCookies{}.Call(page)
	if err == nil {
		for _, c := range cookies.Cookies {
			if c.Name == "cf_clearance" {
				cfClearance = c.Value
				break
			}
		}
	}

	if finalURL == "" {
		finalURL = job.URL
	}

	// Stop listening for network events
	// wait() - disabled for debugging

	job.ResultCh <- renderkit.RenderResultOrErr{Result: &types.RenderResult{
		HTML:                html,
		URL:                 finalURL,
		Title:               title,
		ContentType:         contentType,
		CloudflareClearance: cfClearance,
		Referer:             job.Referer,
		CollectedAssets:     collectedAssets,
		ExtractedLinks:      extractedLinks,
		DiscoveredAPIs:      discoveredAPIs,
	}}
}

func (p *Pool) Render(ctx context.Context, url string) (*types.RenderResult, error) {
	return p.RenderWithReferer(ctx, url, "")
}

func (p *Pool) RenderWithReferer(ctx context.Context, url, referer string) (*types.RenderResult, error) {
	return p.disp.Submit(ctx, url, referer)
}

// RenderWithPriority implements types.PriorityRenderer: when browser
// workers are contended, a higher-priority render is dequeued before
// queued normal work (long-waiting jobs are aged up so they cannot
// starve). See renderkit.Dispatcher.
func (p *Pool) RenderWithPriority(ctx context.Context, url, referer string, prio types.Priority) (*types.RenderResult, error) {
	return p.disp.SubmitWithPriority(ctx, url, referer, prio)
}

func (p *Pool) SetSettle(d time.Duration) {
	p.opts.Settle = d
}

func (p *Pool) StealthEnabled() bool {
	return p.opts.Stealth
}

func (p *Pool) EnableStealth() error {
	if p.opts.Stealth {
		return nil
	}
	p.opts.Stealth = true
	for _, w := range p.workers {
		if w.page != nil {
			w.page.Close()
			page, err := p.browser.Page(proto.TargetCreateTarget{})
			if err != nil {
				continue
			}
			// 注入我们增强的 stealth 脚本
			_, err = proto.PageAddScriptToEvaluateOnNewDocument{
				Source: stealth.Script,
			}.Call(page)
			if err != nil {
				logutil.Warn("stealth script injection warning", slog.Any("error", err))
			}
			w.page = page
		}
	}
	return nil
}

func (p *Pool) SetBehaviorSimulation(enabled bool) {
	p.behaviorSimEnabled = enabled
}

// DownloadAsset downloads an asset using the browser's network stack.
// Uses a multi-layer fallback strategy:
//  1. Direct page navigation (most effective - top-level nav with correct Sec-Fetch headers)
//  2. <img> tag on Referer page (realistic subresource load with proper context)
//  3. CDP Network.loadNetworkResource (direct network stack access)
//  4. JavaScript fetch() (only works if server sends proper CORS headers)
func (p *Pool) DownloadAsset(ctx context.Context, assetURL string, referer string) (*types.AssetDownloadResult, error) {
	p.mu.Lock()
	if p.disp.Closed() {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool closed")
	}
	p.mu.Unlock()

	assetCtx, cancel := context.WithTimeout(ctx, 80*time.Second)
	defer cancel()

	page, err := p.browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}
	defer page.Close()
	page = page.Context(assetCtx)

	// Inject cookies from cached referer page if available
	if referer != "" {
		p.injectRefererCookies(page, referer)
	}

	// Inject stealth scripts to hide automation indicators.
	if p.opts.Stealth {
		_, err := proto.PageAddScriptToEvaluateOnNewDocument{
			Source: stealth.Script,
		}.Call(page)
		if err != nil {
			logutil.Warn("DownloadAsset stealth injection warning", slog.Any("error", err))
		}
	}

	ua := p.getCurrentUA()
	if ua != nil {
		proto.NetworkSetUserAgentOverride{
			UserAgent: ua.UserAgent,
		}.Call(page)
	}

	// Enable downloads for asset download pages.
	proto.PageSetDownloadBehavior{
		Behavior: proto.PageSetDownloadBehaviorBehaviorAllow,
	}.Call(page)

	var firstErr error

	// Layer 1: direct navigation (top-level nav, not subject to CORS)
	// This is currently the most effective approach for anti-bot bypass because:
	// - Uses correct Sec-Fetch headers for navigation (document/navigate)
	// - Bypasses CORS restrictions
	// - Bypasses subresource-level download restrictions
	logutil.Info("DownloadAsset layer1 direct nav: trying", slog.String("url", assetURL))
	l1Ctx, l1Cancel := context.WithTimeout(assetCtx, 20*time.Second)
	l1Page := page.Context(l1Ctx)
	result, err := p.downloadAssetViaNavigation(l1Page, assetURL, referer, ua)
	l1Cancel()
	if err == nil && len(result.Body) > 0 {
		logutil.Info("DownloadAsset layer1 direct nav: success", slog.String("url", assetURL), slog.Int("bytes", len(result.Body)))
		return result, nil
	}
	firstErr = err
	logutil.Warn("DownloadAsset layer1 direct nav: failed", slog.String("url", assetURL), slog.Any("error", err))

	// Layer 2: <img> tag on Referer page (realistic browser behavior)
	// Tries loading the image as a subresource of the Referer page, which
	// provides the most natural request context (proper Referer, cookies, etc.)
	if isImageURL(assetURL) && referer != "" {
		logutil.Info("DownloadAsset layer2 img-on-referer: trying", slog.String("url", assetURL))
		l2Ctx, l2Cancel := context.WithTimeout(assetCtx, 25*time.Second)
		l2Page := page.Context(l2Ctx)
		result, err2 := p.downloadAssetViaImgOnRefererPage(l2Page, assetURL, referer, ua)
		l2Cancel()
		if err2 == nil && len(result.Body) > 0 {
			logutil.Info("DownloadAsset layer2 img-on-referer: success", slog.String("url", assetURL), slog.Int("bytes", len(result.Body)))
			return result, nil
		}
		logutil.Warn("DownloadAsset layer2 img-on-referer: failed", slog.String("url", assetURL), slog.Any("error", err2))
	} else if !isImageURL(assetURL) {
		logutil.Info("DownloadAsset layer2 img-on-referer: skipped for non-image resource", slog.String("url", assetURL))
	} else {
		logutil.Info("DownloadAsset layer2 img-on-referer: skipped (no referer)", slog.String("url", assetURL))
	}

	// Layer 3: Network.loadNetworkResource (CDP direct network load)
	logutil.Info("DownloadAsset layer3 Network.loadNetworkResource: trying", slog.String("url", assetURL))
	l3Ctx, l3Cancel := context.WithTimeout(assetCtx, 20*time.Second)
	l3Page := page.Context(l3Ctx)
	result, err3 := p.downloadAssetViaLoadNetworkResource(l3Page, assetURL, referer, ua)
	l3Cancel()
	if err3 == nil && len(result.Body) > 0 {
		logutil.Info("DownloadAsset layer3 Network.loadNetworkResource: success", slog.String("url", assetURL), slog.Int("bytes", len(result.Body)))
		return result, nil
	}
	logutil.Warn("DownloadAsset layer3 Network.loadNetworkResource: failed", slog.String("url", assetURL), slog.Any("error", err3))

	// Layer 4: JS fetch
	logutil.Info("DownloadAsset layer4 fetch: trying", slog.String("url", assetURL))
	l4Ctx, l4Cancel := context.WithTimeout(assetCtx, 15*time.Second)
	l4Page := page.Context(l4Ctx)
	result, err4 := p.downloadAssetViaFetch(l4Page, assetURL, referer)
	l4Cancel()
	if err4 == nil && len(result.Body) > 0 {
		logutil.Info("DownloadAsset layer4 fetch: success", slog.String("url", assetURL), slog.Int("bytes", len(result.Body)))
		return result, nil
	}
	logutil.Warn("DownloadAsset layer4 fetch: failed", slog.String("url", assetURL), slog.Any("error", err4))

	// Fallback: try full-resolution URL variant for media.defense.gov assets
	// Sometimes the 300x300 thumbnail endpoint is blocked while full-resolution works
	if strings.Contains(assetURL, "media.defense.gov") && strings.Contains(assetURL, "/300/300/0/") {
		fullResURL := strings.Replace(assetURL, "/300/300/0/", "/-1/-1/0/", 1)
		logutil.Info("DownloadAsset trying full-resolution variant", slog.String("url", fullResURL))

		l5Ctx, l5Cancel := context.WithTimeout(assetCtx, 20*time.Second)
		l5Page := page.Context(l5Ctx)
		result5, err5 := p.downloadAssetViaNavigation(l5Page, fullResURL, referer, ua)
		l5Cancel()
		if err5 == nil && len(result5.Body) > 0 {
			logutil.Info("DownloadAsset full-resolution variant success", slog.String("url", fullResURL), slog.Int("bytes", len(result5.Body)))
			return result5, nil
		}
		logutil.Warn("DownloadAsset full-resolution variant failed", slog.Any("error", err5))

		// Also try img-on-referer with full-resolution URL
		if referer != "" {
			l5bCtx, l5bCancel := context.WithTimeout(assetCtx, 25*time.Second)
			l5bPage := page.Context(l5bCtx)
			result5b, err5b := p.downloadAssetViaImgOnRefererPage(l5bPage, fullResURL, referer, ua)
			l5bCancel()
			if err5b == nil && len(result5b.Body) > 0 {
				logutil.Info("DownloadAsset full-resolution variant (img-on-referer) success", slog.String("url", fullResURL), slog.Int("bytes", len(result5b.Body)))
				return result5b, nil
			}
			logutil.Warn("DownloadAsset full-resolution variant (img-on-referer) failed", slog.Any("error", err5b))
		}
	}

	// All failed — return the first error.
	return nil, firstErr
}

// downloadAssetViaNavigation downloads an asset by directly navigating to it.
// This bypasses CORS because top-level navigation is not subject to CORS.
// It works like opening the image URL directly in a browser tab.
func (p *Pool) downloadAssetViaNavigation(page *rod.Page, assetURL, referer string, ua *antibot.UAProfile) (*types.AssetDownloadResult, error) {
	// Simulate a real browser typing URL in the address bar.
	// Do NOT set Sec-Fetch-* headers manually — Chrome sets these automatically.
	// Setting them manually can trigger Chrome's security checks and cause
	// ERR_BLOCKED_BY_CLIENT. Real browsers only send basic headers when
	// navigating via address bar.

	// Set User-Agent override via CDP (this is the correct way to set UA).
	uaOverride := proto.NetworkSetUserAgentOverride{
		UserAgent: ua.UserAgent,
	}
	if ua.SecChUa != "" {
		uaOverride.AcceptLanguage = "en-US,en;q=0.9"
	}
	uaOverride.Call(page)

	// Set minimal extra headers that a browser would naturally send.
	// Note: We intentionally do NOT set Sec-Fetch-* headers here.
	minimalHeaders := proto.NetworkHeaders{
		"Accept":                    gson.New("text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"),
		"Upgrade-Insecure-Requests": gson.New("1"),
	}
	proto.NetworkSetExtraHTTPHeaders{
		Headers: minimalHeaders,
	}.Call(page)

	// Track the response for the asset URL.
	type respInfo struct {
		requestID proto.NetworkRequestID
		status    int
		mimeType  string
	}
	var assetResp *respInfo
	var respMu sync.Mutex

	waitNavigation := page.EachEvent(func(e *proto.NetworkResponseReceived) {
		if e.Response == nil {
			return
		}
		respMu.Lock()
		defer respMu.Unlock()
		// Compare URLs after normalizing to handle encoding differences
		if normalizeURL(e.Response.URL) == normalizeURL(assetURL) {
			assetResp = &respInfo{
				requestID: e.RequestID,
				status:    int(e.Response.Status),
				mimeType:  e.Response.MIMEType,
			}
		}
	})
	defer waitNavigation()

	if err := page.Navigate(assetURL); err != nil {
		return nil, fmt.Errorf("navigate to asset: %w", err)
	}

	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("wait load: %w", err)
	}

	// Wait briefly for the response to be captured.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		respMu.Lock()
		found := assetResp != nil
		respMu.Unlock()
		if found {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	respMu.Lock()
	resp := assetResp
	respMu.Unlock()

	// Try to get content type and status from the page even if we didn't
	// capture the response event (e.g. for CSS/JS files rendered as text).
	contentType := ""
	statusCode := 0
	if resp != nil {
		contentType = resp.mimeType
		statusCode = resp.status
	} else {
		// Fallback: get content type from document
		ctResult, ctErr := page.Eval(`() => document.contentType`)
		if ctErr == nil && ctResult != nil && !ctResult.Value.Nil() {
			contentType = ctResult.Value.String()
		}
		// Assume 200 if page loaded successfully and we have content
		if contentType != "" {
			statusCode = 200
		}
	}

	if statusCode != 0 && (statusCode < 200 || statusCode >= 300) {
		return nil, fmt.Errorf("asset HTTP %d", statusCode)
	}

	// Try to get the response body via CDP.
	body, err := page.GetResource(assetURL)
	if err == nil && len(body) > 0 {
		return &types.AssetDownloadResult{
			URL:         assetURL,
			Body:        body,
			ContentType: contentType,
			StatusCode:  statusCode,
		}, nil
	}

	// Fallback: try extracting content from the page DOM.
	// For images: use canvas to get data URL.
	// For text resources (CSS, JS, etc.): read text content from pre/body.
	bodyStr, err := page.Eval(`() => {
		const img = document.querySelector('img');
		if (img) {
			const canvas = document.createElement('canvas');
			canvas.width = img.naturalWidth || img.width;
			canvas.height = img.naturalHeight || img.height;
			const ctx = canvas.getContext('2d');
			try {
				ctx.drawImage(img, 0, 0);
				return canvas.toDataURL('image/png');
			} catch(e) {
				return null;
			}
		}
		const pre = document.querySelector('pre');
		if (pre) return pre.textContent;
		if (document.body && document.body.textContent) return document.body.textContent;
		return null;
	}`)
	if err == nil && bodyStr != nil && !bodyStr.Value.Nil() {
		s := bodyStr.Value.String()
		if strings.HasPrefix(s, "data:") {
			// data URL — extract base64 portion
			if idx := strings.Index(s, "base64,"); idx >= 0 {
				b64 := s[idx+len("base64,"):]
				decoded, dErr := base64.StdEncoding.DecodeString(b64)
				if dErr == nil && len(decoded) > 0 {
					ct := "image/png"
					if ci := strings.Index(s, ";base64"); ci >= 0 {
						if strings.HasPrefix(s, "data:") {
							ct = s[5:ci]
						}
					}
					if contentType == "" {
						contentType = ct
					}
					return &types.AssetDownloadResult{
						URL:         assetURL,
						Body:        decoded,
						ContentType: contentType,
						StatusCode:  statusCode,
					}, nil
				}
			}
		} else if s != "" && contentType != "" && renderkit.IsTextContent(contentType) {
			// Text content (CSS, JS, etc.)
			return &types.AssetDownloadResult{
				URL:         assetURL,
				Body:        []byte(s),
				ContentType: contentType,
				StatusCode:  statusCode,
			}, nil
		}
	}

	if resp == nil {
		return nil, fmt.Errorf("no response captured for %s", assetURL)
	}
	return nil, fmt.Errorf("could not extract asset body from navigation")
}

// downloadAssetViaImgOnRefererPage loads an image by first navigating to the
// Referer page, then creating an <img> element on that page. This is the most
// realistic browser behavior because:
//   - The image is loaded as a subresource of a real page
//   - Referer header is naturally set by the browser (not manually spoofed)
//   - The page context provides proper cookies and session state
//   - Sec-Fetch headers are naturally correct for an image subresource
func (p *Pool) downloadAssetViaImgOnRefererPage(page *rod.Page, assetURL, referer string, ua *antibot.UAProfile) (*types.AssetDownloadResult, error) {
	// Use cached referer page if available to avoid redundant navigation
	cachedPage := p.getOrCreateRefererPage(referer, ua)
	usingCache := cachedPage != nil
	if usingCache {
		p.refererUseMu.Lock()
		page = cachedPage
		logutil.Info("img-on-referer: using cached referer page", slog.String("referer", referer))
		defer p.refererUseMu.Unlock()

		// Clean up any previously injected img elements to keep the page state clean
		page.Eval(`() => {
			const imgs = document.querySelectorAll('img[data-wukong-injected]');
			imgs.forEach(img => img.remove());
		}`)
	} else {
		// Simulate a real browser typing URL in the address bar.
		// Do NOT set Sec-Fetch-* headers manually — Chrome sets these automatically.
		// Setting them manually can trigger Chrome's security checks and cause
		// ERR_BLOCKED_BY_CLIENT. Use the same minimal-header pattern as
		// downloadAssetViaNavigation for consistency.
		uaOverride := proto.NetworkSetUserAgentOverride{
			UserAgent: ua.UserAgent,
		}
		if ua.SecChUa != "" {
			uaOverride.AcceptLanguage = "en-US,en;q=0.9"
		}
		uaOverride.Call(page)

		minimalHeaders := proto.NetworkHeaders{
			"Accept":                    gson.New("text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"),
			"Upgrade-Insecure-Requests": gson.New("1"),
		}
		proto.NetworkSetExtraHTTPHeaders{
			Headers: minimalHeaders,
		}.Call(page)

		// Step 1: Navigate to the Referer page to establish proper context
		logutil.Info("img-on-referer: navigating to referer page", slog.String("referer", referer))
		navErr := page.Navigate(referer)
		if navErr != nil {
			logutil.Warn("img-on-referer: referer nav error", slog.Any("error", navErr))
			// If referer page fails to navigate, try about:blank instead
			if err := page.Navigate("about:blank"); err != nil {
				return nil, fmt.Errorf("navigate to about:blank fallback: %w", err)
			}
			page.WaitLoad()
		} else {
			if err := page.WaitLoad(); err != nil {
				logutil.Warn("img-on-referer: referer waitLoad warning", slog.Any("error", err))
			}
			// Add a small human-like delay after page load
			time.Sleep(time.Duration(800+_randInt(0, 1200)) * time.Millisecond)
		}

		// Cache the loaded referer page for future use
		p.cacheRefererPage(referer, page)
	}

	// Step 2: Track network events for the image
	type respInfo struct {
		requestID proto.NetworkRequestID
		status    int
		mimeType  string
	}
	var assetResp *respInfo
	var respMu sync.Mutex

	waitResponse := page.EachEvent(func(e *proto.NetworkResponseReceived) {
		if e.Response == nil {
			return
		}
		respMu.Lock()
		defer respMu.Unlock()
		if normalizeURL(e.Response.URL) == normalizeURL(assetURL) {
			assetResp = &respInfo{
				requestID: e.RequestID,
				status:    int(e.Response.Status),
				mimeType:  e.Response.MIMEType,
			}
		}
	})
	defer waitResponse()

	// Step 3: Try loading via fetch first (better error info, can bypass some CSP issues)
	// We try fetch before img because fetch gives us better error details
	fetchResult, fetchErr := page.Eval(`
		async (url) => {
			try {
				const resp = await fetch(url, {
					credentials: 'include',
					mode: 'no-cors',
					cache: 'force-cache',
					redirect: 'follow'
				});
				// With no-cors mode, we can't read the response body directly,
				// but we can check if the request succeeded (opaque response)
				return { ok: true, type: resp.type, status: resp.status };
			} catch(e) {
				return { ok: false, error: e.message || String(e) };
			}
		}
	`, assetURL)

	if fetchErr == nil && fetchResult != nil && !fetchResult.Value.Nil() {
		fetchOK := fetchResult.Value.Get("ok").Bool()
		if fetchOK {
			fetchType := fetchResult.Value.Get("type").String()
			logutil.Info("img-on-referer: fetch succeeded, trying img element for body", slog.String("type", fetchType))
		} else {
			fetchErrMsg := fetchResult.Value.Get("error").String()
			logutil.Warn("img-on-referer: fetch failed", slog.String("error", fetchErrMsg))
		}
	}

	// Step 4: Create an img element on the page to load the asset
	// (to get the actual image data via CDP response body)
	imgResult, imgErr := page.Eval(`
		(url) => {
			return new Promise((resolve) => {
				const img = new Image();
				img.setAttribute('data-wukong-injected', 'true');
				img.onload = () => resolve({
					ok: true,
					naturalWidth: img.naturalWidth,
					naturalHeight: img.naturalHeight,
					complete: img.complete
				});
				img.onerror = (e) => resolve({
					ok: false,
					errorType: e.type,
					errorMessage: e.message || 'unknown error',
					targetSrc: e.target ? e.target.src : null
				});
				img.src = url;
				document.body.appendChild(img);
				setTimeout(() => {
					if (!img.complete) {
						resolve({ ok: false, errorType: 'timeout', errorMessage: 'Image load timed out' });
					}
				}, 15000);
			});
		}
	`, assetURL)
	if imgErr != nil {
		return nil, fmt.Errorf("create img element: %w", imgErr)
	}

	// Step 5: Check img load result
	if imgResult != nil && !imgResult.Value.Nil() {
		imgOK := imgResult.Value.Get("ok").Bool()
		if !imgOK {
			errType := imgResult.Value.Get("errorType").String()
			errMsg := imgResult.Value.Get("errorMessage").String()
			logutil.Warn("img-on-referer: img.onerror", slog.String("type", errType), slog.String("message", errMsg))
		}
	}

	// Wait a bit more for response event to be fully captured
	time.Sleep(800 * time.Millisecond)

	respMu.Lock()
	resp := assetResp
	respMu.Unlock()

	if resp == nil {
		return nil, fmt.Errorf("no response captured for img load on referer page")
	}

	if resp.status < 200 || resp.status >= 300 {
		return nil, fmt.Errorf("img load HTTP %d", resp.status)
	}

	// Step 6: Get the response body via CDP
	body, err := page.GetResource(assetURL)
	if err == nil && len(body) > 0 {
		return &types.AssetDownloadResult{
			URL:         assetURL,
			Body:        body,
			ContentType: resp.mimeType,
			StatusCode:  resp.status,
		}, nil
	}

	// Fallback: extract via canvas (slightly degraded quality for JPEG, but works)
	canvasResult, err := page.Eval(`
		(url) => {
			const imgs = document.querySelectorAll('img');
			for (const img of imgs) {
				if (img.src === url && img.complete && img.naturalWidth > 0) {
					const canvas = document.createElement('canvas');
					canvas.width = img.naturalWidth || img.width;
					canvas.height = img.naturalHeight || img.height;
					const ctx = canvas.getContext('2d');
					try {
						ctx.drawImage(img, 0, 0);
						return canvas.toDataURL('image/png');
					} catch(e) {
						return null;
					}
				}
			}
			return null;
		}
	`, assetURL)
	if err == nil && canvasResult != nil && !canvasResult.Value.Nil() {
		s := canvasResult.Value.String()
		if strings.HasPrefix(s, "data:") {
			if idx := strings.Index(s, "base64,"); idx >= 0 {
				b64 := s[idx+len("base64,"):]
				decoded, dErr := base64.StdEncoding.DecodeString(b64)
				if dErr == nil && len(decoded) > 0 {
					ct := "image/png"
					if ci := strings.Index(s, ";base64"); ci >= 0 {
						if strings.HasPrefix(s, "data:") {
							ct = s[5:ci]
						}
					}
					return &types.AssetDownloadResult{
						URL:         assetURL,
						Body:        decoded,
						ContentType: ct,
						StatusCode:  resp.status,
					}, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("could not get img response body from referer page")
}

// downloadAssetViaImgTag loads an asset by creating an <img> element in a page.
// This loads the image as a subresource rather than via top-level navigation,
// which bypasses download restrictions that cause ERR_BLOCKED_BY_CLIENT.
func (p *Pool) downloadAssetViaImgTag(page *rod.Page, assetURL, referer string) (*types.AssetDownloadResult, error) {
	// Navigate to about:blank first to have a clean page context
	if err := page.Navigate("about:blank"); err != nil {
		return nil, fmt.Errorf("navigate to about:blank: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("wait about:blank load: %w", err)
	}

	// Track the response for the asset URL
	type respInfo struct {
		requestID proto.NetworkRequestID
		status    int
		mimeType  string
	}
	var assetResp *respInfo
	var respMu sync.Mutex

	waitNavigation := page.EachEvent(func(e *proto.NetworkResponseReceived) {
		if e.Response == nil {
			return
		}
		respMu.Lock()
		defer respMu.Unlock()
		// Compare URLs after normalizing to handle encoding differences
		if normalizeURL(e.Response.URL) == normalizeURL(assetURL) {
			assetResp = &respInfo{
				requestID: e.RequestID,
				status:    int(e.Response.Status),
				mimeType:  e.Response.MIMEType,
			}
		}
	})
	defer waitNavigation()

	// Create an img element and set its src to trigger the load
	jsExpr := `
		(url) => {
			const img = new Image();
			img.onload = () => window.__imgLoaded = true;
			img.onerror = () => window.__imgLoaded = 'error';
			img.src = url;
			document.body.appendChild(img);
		}
	`
	_, err := page.Eval(jsExpr, assetURL)
	if err != nil {
		return nil, fmt.Errorf("create img element: %w", err)
	}

	// Wait for the image to load or fail (polling)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		loadedVal, err := page.Eval(`() => window.__imgLoaded`)
		if err == nil && loadedVal != nil && !loadedVal.Value.Nil() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Wait a bit more for response event to be captured
	time.Sleep(500 * time.Millisecond)

	respMu.Lock()
	resp := assetResp
	respMu.Unlock()

	if resp == nil {
		return nil, fmt.Errorf("no response captured for img load")
	}

	if resp.status < 200 || resp.status >= 300 {
		return nil, fmt.Errorf("img load HTTP %d", resp.status)
	}

	// Get the response body via CDP
	body, err := page.GetResource(assetURL)
	if err == nil && len(body) > 0 {
		return &types.AssetDownloadResult{
			URL:         assetURL,
			Body:        body,
			ContentType: resp.mimeType,
			StatusCode:  resp.status,
		}, nil
	}

	return nil, fmt.Errorf("could not get img response body")
}

// downloadAssetViaLoadNetworkResource loads an asset using CDP Network.loadNetworkResource.
// This directly loads the resource through Chrome's network stack,
// bypassing page-level restrictions that cause ERR_BLOCKED_BY_CLIENT.
// It navigates to about:blank first to ensure a valid frame context.
func (p *Pool) downloadAssetViaLoadNetworkResource(page *rod.Page, assetURL, referer string, ua *antibot.UAProfile) (*types.AssetDownloadResult, error) {
	// Navigate to about:blank first to have a valid frame context
	if err := page.Navigate("about:blank"); err != nil {
		return nil, fmt.Errorf("navigate to about:blank: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return nil, fmt.Errorf("wait about:blank load: %w", err)
	}

	// Set headers appropriate for a subresource load
	extraHeaders := proto.NetworkHeaders{
		"Accept":          gson.New("image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"),
		"Accept-Language": gson.New("en-US,en;q=0.9"),
		"Sec-Fetch-Dest":  gson.New("image"),
		"Sec-Fetch-Mode":  gson.New("no-cors"),
		"Sec-Fetch-Site":  gson.New("cross-site"),
		"User-Agent":      gson.New(ua.UserAgent),
	}
	if ua.SecChUa != "" {
		extraHeaders["Sec-Ch-Ua"] = gson.New(ua.SecChUa)
		extraHeaders["Sec-Ch-Ua-Mobile"] = gson.New(ua.SecChUaMobile)
		extraHeaders["Sec-Ch-Ua-Platform"] = gson.New(ua.SecChUaPlatform)
	}
	if referer != "" {
		extraHeaders["Referer"] = gson.New(referer)
	}
	proto.NetworkSetExtraHTTPHeaders{
		Headers: extraHeaders,
	}.Call(page)

	frameID := page.FrameID
	if frameID == "" {
		return nil, fmt.Errorf("no valid frame ID after about:blank navigation")
	}

	params := proto.NetworkLoadNetworkResource{
		URL:     assetURL,
		FrameID: frameID,
		Options: &proto.NetworkLoadNetworkResourceOptions{
			DisableCache:       false,
			IncludeCredentials: true,
		},
	}

	result, err := params.Call(page)
	if err != nil {
		return nil, fmt.Errorf("Network.loadNetworkResource: %w", err)
	}

	if result == nil || result.Resource == nil || !result.Resource.Success {
		if result != nil && result.Resource != nil {
			status := 0
			if result.Resource.HTTPStatusCode != nil {
				status = int(*result.Resource.HTTPStatusCode)
			}
			return nil, fmt.Errorf("Network.loadNetworkResource not successful (status: %d, netError: %s)", status, result.Resource.NetErrorName)
		}
		return nil, fmt.Errorf("Network.loadNetworkResource returned nil result")
	}

	resource := result.Resource

	if resource.Stream == "" {
		return nil, fmt.Errorf("Network.loadNetworkResource no stream")
	}

	var data []byte
	for {
		readParams := proto.IORead{
			Handle: resource.Stream,
		}
		readResult, readErr := readParams.Call(page)
		if readErr != nil {
			break
		}
		if readResult.Base64Encoded {
			decoded, decodeErr := base64.StdEncoding.DecodeString(readResult.Data)
			if decodeErr == nil && len(decoded) > 0 {
				data = append(data, decoded...)
			} else {
				data = append(data, []byte(readResult.Data)...)
			}
		} else {
			data = append(data, []byte(readResult.Data)...)
		}
		if readResult.EOF {
			break
		}
	}

	closeParams := proto.IOClose{Handle: resource.Stream}
	closeParams.Call(page)

	if len(data) == 0 {
		return nil, fmt.Errorf("Network.loadNetworkResource returned empty data")
	}

	contentType := ""
	if resource.Headers != nil {
		if ct, ok := resource.Headers["Content-Type"]; ok {
			contentType = ct.String()
		}
	}

	statusCode := 0
	if resource.HTTPStatusCode != nil {
		statusCode = int(*resource.HTTPStatusCode)
	}

	return &types.AssetDownloadResult{
		URL:         assetURL,
		Body:        data,
		ContentType: contentType,
		StatusCode:  statusCode,
	}, nil
}

// downloadAssetViaFetch downloads an asset using JavaScript fetch().
// Only works if the server sends proper CORS headers.
func (p *Pool) downloadAssetViaFetch(page *rod.Page, assetURL, referer string) (*types.AssetDownloadResult, error) {
	parsed, err := url.Parse(assetURL)
	if err != nil {
		return nil, fmt.Errorf("parse asset URL: %w", err)
	}
	originURL := parsed.Scheme + "://" + parsed.Host + "/"

	if err := page.Navigate(originURL); err != nil {
		page.Navigate("about:blank")
	}
	page.WaitLoad()

	jsCode := `
		async (url, referer) => {
			try {
				const controller = new AbortController();
				const signal = controller.signal;
				const timeoutId = setTimeout(() => controller.abort(), 7000);
				const fetchOpts = {
					signal,
					credentials: 'include',
					mode: 'cors',
					cache: 'force-cache'
				};
				if (referer) {
					fetchOpts.referrer = referer;
				}
				const resp = await fetch(url, fetchOpts);
				clearTimeout(timeoutId);
				if (!resp.ok) {
					return { success: false, status: resp.status, statusText: resp.statusText };
				}
				const buf = await resp.arrayBuffer();
				const bytes = new Uint8Array(buf);
				let binary = '';
				for (let i = 0; i < bytes.length; i++) {
					binary += String.fromCharCode(bytes[i]);
				}
				return {
					success: true,
					body: btoa(binary),
					contentType: resp.headers.get('content-type') || '',
					status: resp.status
				};
			} catch(e) {
				return { success: false, error: e.message || String(e) };
			}
		}
	`

	result, err := page.Eval(jsCode, assetURL, referer)
	if err != nil {
		return nil, fmt.Errorf("fetch asset via JS: %w", err)
	}

	if result == nil || result.Value.Nil() {
		return nil, fmt.Errorf("empty result from JS fetch")
	}

	success := result.Value.Get("success").Bool()
	if !success {
		errMsg := result.Value.Get("error").String()
		status := int(result.Value.Get("status").Int())
		if errMsg != "" {
			return nil, fmt.Errorf("JS fetch failed: %s (status: %d)", errMsg, status)
		}
		return nil, fmt.Errorf("JS fetch failed with status %d", status)
	}

	bodyB64 := result.Value.Get("body").String()
	contentType := result.Value.Get("contentType").String()
	status := int(result.Value.Get("status").Int())

	if bodyB64 == "" {
		return nil, fmt.Errorf("empty body from JS fetch")
	}

	bodyBytes, err := base64.StdEncoding.DecodeString(bodyB64)
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}

	return &types.AssetDownloadResult{
		URL:         assetURL,
		Body:        bodyBytes,
		ContentType: contentType,
		StatusCode:  status,
	}, nil
}

func (p *Pool) Close() {
	// Drain the shared dispatcher first: close the job queue, wait for
	// in-flight renders, and reject new submits. Only the caller that
	// performed the drain runs the cleanup below — browser.MustClose is
	// not idempotent, and workers have all exited by the time Drain
	// returns true.
	if !p.disp.Drain() {
		return
	}

	for _, w := range p.workers {
		if w.page != nil {
			w.page.Close()
		}
	}

	// Close cached referer pages
	p.refererMu.Lock()
	for _, rp := range p.refererPages {
		if rp != nil {
			rp.Close()
		}
	}
	p.refererPages = nil
	p.refererMu.Unlock()

	p.browser.MustClose()

	// Release the lifecycle goroutine. It wakes up and re-enters Close,
	// where Drain() reports already-drained and the call returns.
	if p.lifeCancel != nil {
		p.lifeCancel()
	}
}

// getOrCreateRefererPage returns a cached referer page or creates a new one.
func (p *Pool) getOrCreateRefererPage(referer string, ua *antibot.UAProfile) *rod.Page {
	if referer == "" {
		return nil
	}

	p.refererMu.Lock()
	defer p.refererMu.Unlock()

	if rp, ok := p.refererPages[referer]; ok && rp != nil {
		// Verify the page is still valid by checking if target exists
		info, err := rp.Info()
		if err == nil && info.URL != "" {
			return rp
		}
		// Page became invalid, remove it
		delete(p.refererPages, referer)
	}

	// Create a new page for this referer
	rp, err := p.browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return nil
	}
	rp = rp.Context(context.Background())

	// Set UA override via CDP (correct way to set UA) and minimal headers.
	// Do NOT set Sec-Fetch-* headers manually — Chrome sets these automatically,
	// matching the pattern in downloadAssetViaNavigation for consistency.
	if ua != nil {
		uaOverride := proto.NetworkSetUserAgentOverride{
			UserAgent: ua.UserAgent,
		}
		if ua.SecChUa != "" {
			uaOverride.AcceptLanguage = "en-US,en;q=0.9"
		}
		uaOverride.Call(rp)
	}

	minimalHeaders := proto.NetworkHeaders{
		"Accept":                    gson.New("text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"),
		"Upgrade-Insecure-Requests": gson.New("1"),
	}
	proto.NetworkSetExtraHTTPHeaders{
		Headers: minimalHeaders,
	}.Call(rp)

	navCtx, navCancel := context.WithTimeout(context.Background(), 30*time.Second)
	rp = rp.Context(navCtx)
	if err := rp.Navigate(referer); err != nil {
		navCancel()
		rp.Close()
		logutil.Warn("img-on-referer: failed to pre-load referer page", slog.Any("error", err))
		return nil
	}
	rp.WaitLoad()
	navCancel()

	rp = rp.Context(context.Background())

	p.refererPages[referer] = rp
	logutil.Info("img-on-referer: pre-loaded referer page", slog.String("referer", referer))

	time.Sleep(time.Duration(500+_randInt(0, 500)) * time.Millisecond)

	return rp
}

// cacheRefererPage stores a referer page in the cache for future reuse.
func (p *Pool) cacheRefererPage(referer string, page *rod.Page) {
	if referer == "" || page == nil {
		return
	}
	p.refererMu.Lock()
	defer p.refererMu.Unlock()

	if _, exists := p.refererPages[referer]; !exists {
		p.refererPages[referer] = page
	}
}

// injectRefererCookies extracts cookies from the cached referer page and injects
// them into the target page, so that direct navigation (Layer 1) has proper session state.
func (p *Pool) injectRefererCookies(targetPage *rod.Page, referer string) {
	p.refererMu.Lock()
	sourcePage, ok := p.refererPages[referer]
	p.refererMu.Unlock()
	if !ok || sourcePage == nil {
		return
	}

	p.refererUseMu.Lock()
	defer p.refererUseMu.Unlock()

	// Extract cookies from the cached referer page
	cookiesResult, err := proto.NetworkGetCookies{}.Call(sourcePage)
	if err != nil || cookiesResult == nil || len(cookiesResult.Cookies) == 0 {
		return
	}

	// Build cookie list for the target page
	var cookieParams []*proto.NetworkCookieParam
	for _, c := range cookiesResult.Cookies {
		if c.Name == "" || c.Value == "" {
			continue
		}
		cookieParams = append(cookieParams, &proto.NetworkCookieParam{
			Name:   c.Name,
			Value:  c.Value,
			Domain: c.Domain,
			Path:   c.Path,
			Secure: c.Secure,
		})
	}

	if len(cookieParams) > 0 {
		err = proto.NetworkSetCookies{
			Cookies: cookieParams,
		}.Call(targetPage)
		if err != nil {
			logutil.Warn("injectRefererCookies: failed to set cookies", slog.Any("error", err))
		} else {
			logutil.Info("injectRefererCookies: injected cookies from referer", slog.Int("count", len(cookieParams)))
		}
	}
}

// normalizeURL normalizes a URL for comparison purposes by parsing and
// re-encoding it. This handles differences in percent-encoding (e.g. %20 vs space)
// that can cause direct string comparison to fail.
func normalizeURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	return parsed.String()
}

// isImageURL returns true if the URL likely points to an image resource
// based on file extension.
func isImageURL(u string) bool {
	low := strings.ToLower(u)
	// Remove query string for extension check
	if i := strings.Index(low, "?"); i >= 0 {
		low = low[:i]
	}
	return strings.HasSuffix(low, ".png") ||
		strings.HasSuffix(low, ".jpg") ||
		strings.HasSuffix(low, ".jpeg") ||
		strings.HasSuffix(low, ".gif") ||
		strings.HasSuffix(low, ".webp") ||
		strings.HasSuffix(low, ".svg") ||
		strings.HasSuffix(low, ".ico") ||
		strings.HasSuffix(low, ".bmp") ||
		strings.HasSuffix(low, ".tiff")
}

// _randInt returns a random integer in [min, max] (inclusive).
func _randInt(min, max int) int {
	if min >= max {
		return min
	}
	return min + rand.Intn(max-min+1)
}
