package rodbackend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"github.com/km269/wukong/internal/browser/stealth"
	"github.com/km269/wukong/internal/browser/types"
	"github.com/km269/wukong/internal/util"
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
}

type Pool struct {
	opts               Options
	browser            *rod.Browser
	workers            []*worker
	queue              chan *renderJob
	wg                 sync.WaitGroup
	closed             bool
	mu                 sync.Mutex
	behaviorSimEnabled bool
	behaviorSimulator  *behavior.Simulator
	escalator          *antibot.Escalator
	currentUA          *antibot.UAProfile

	refererMu    sync.Mutex
	refererUseMu sync.Mutex
	refererPages map[string]*rod.Page
}

type worker struct {
	idx  int
	page *rod.Page
}

type renderJob struct {
	url      string
	referer  string
	resultCh chan<- renderResultOrErr
	ctx      context.Context
}

type renderResultOrErr struct {
	Result *types.RenderResult
	Err    error
}

func New(opts Options) *Pool {
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
		if opts.ProfileDir != "" {
			l = l.UserDataDir(opts.ProfileDir)
		}
		if needNoSandbox() {
			l = l.NoSandbox(true)
		}
		l = l.Devtools(false)
		if opts.Proxy != "" {
			l = l.Proxy(opts.Proxy)
		}
		controlURL = l.MustLaunch()
	}

	browserInstance := rod.New().ControlURL(controlURL).MustConnect()

	escalator := antibot.NewEscalator(antibot.DefaultEscalatorConfig())
	currentUA := escalator.GetRandomDesktopUA()

	p := &Pool{
		opts:              opts,
		browser:           browserInstance,
		queue:             make(chan *renderJob, opts.Workers*4),
		behaviorSimulator: behavior.New(behavior.DefaultConfig()),
		escalator:         escalator,
		currentUA:         currentUA,
		refererPages:      make(map[string]*rod.Page),
	}

	for i := 0; i < opts.Workers; i++ {
		p.workers = append(p.workers, &worker{idx: i})
		p.wg.Add(1)
		go p.workerLoop(p.workers[i])
	}

	return p
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

func (p *Pool) workerLoop(w *worker) {
	defer p.wg.Done()
	for job := range p.queue {
		p.renderJob(w, job)
	}
}

func (p *Pool) renderJob(w *worker, job *renderJob) {
	ctx, cancel := context.WithTimeout(job.ctx, p.opts.RenderTimeout)
	defer cancel()

	var page *rod.Page
	if w.page != nil {
		page = w.page.Context(ctx)
	}

	if page == nil {
		var err error
		page, err = p.browser.Page(proto.TargetCreateTarget{})
		if err != nil {
			job.resultCh <- renderResultOrErr{Err: fmt.Errorf("create page: %w", err)}
			return
		}
		if p.opts.Stealth {
			// 注入我们增强的 stealth 脚本
			_, err := proto.PageAddScriptToEvaluateOnNewDocument{
				Source: stealth.Script,
			}.Call(page)
			if err != nil {
				fmt.Fprintf(nil, "[wukong/rod] stealth script injection warning: %v\n", err)
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
		requestID proto.NetworkRequestID
		url       string
		mimeType  string
		status    int
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
			requestID: e.RequestID,
			url:       e.Response.URL,
			mimeType:  e.Response.MIMEType,
			status:    int(e.Response.Status),
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

	if err := page.Navigate(job.url); err != nil {
		job.resultCh <- renderResultOrErr{Err: fmt.Errorf("navigate: %w", err)}
		return
	}

	if err := page.WaitLoad(); err != nil {
		job.resultCh <- renderResultOrErr{Err: fmt.Errorf("wait load: %w", err)}
		return
	}

	if p.opts.Settle > 0 {
		page.WaitRequestIdle(p.opts.Settle, nil, nil, nil)
	}

	// 如果启用了行为模拟
	if p.behaviorSimEnabled {
		// 模拟自然的滚动和鼠标移动
		page.Eval(`
			(async () => {
				// 随机滚动一小段距离
				const randomScroll = () => {
					const delta = Math.floor(Math.random() * 200) - 100;
					window.scrollBy(0, delta);
					return new Promise(r => setTimeout(r, 200 + Math.random() * 300));
				};
				await randomScroll();
			})()
		`)

		// 鼠标移动到随机位置
		page.Eval(`
			(async () => {
				// 模拟鼠标移动到随机位置
				const randomX = Math.random() * window.innerWidth;
				const randomY = Math.random() * window.innerHeight;
				// 触发鼠标移动事件
				const mouseEvent = new MouseEvent('mousemove', {
					clientX: randomX,
					clientY: randomY,
					bubbles: true
				});
				document.dispatchEvent(mouseEvent);
				// 模拟鼠标停留一会儿
				await new Promise(r => setTimeout(r, 150 + Math.random() * 350));
			})()
		`)
	}

	if p.opts.Scroll {
		page.Eval(`
			(async () => {
				for (let i = 0; i < 5; i++) {
					window.scrollBy(0, window.innerHeight);
					await new Promise(r => setTimeout(r, 500 + Math.random() * 300));
				}
			})()
		`)
		if p.opts.Settle > 0 {
			page.WaitRequestIdle(p.opts.Settle, nil, nil, nil)
		}
	}

	finalURLResult, err := page.Eval(`() => window.location.href`)
	if err == nil && finalURLResult != nil && !finalURLResult.Value.Nil() {
		finalURL = finalURLResult.Value.String()
	}
	if finalURL == "" {
		finalURL = job.url
	}

	contentTypeResult, err := page.Eval(`() => document.contentType`)
	if err == nil && contentTypeResult != nil && !contentTypeResult.Value.Nil() {
		contentType = contentTypeResult.Value.String()
	}

	if contentType != "" && !isHTMLContentType(contentType) {
		job.resultCh <- renderResultOrErr{Err: &types.ErrNotHTML{URL: job.url, ContentType: contentType}}
		return
	}

	// Collect assets by fetching them in the page context via JS.
	// This is more reliable than Network.getResponseBody which can hang.
	collectedAssets := make(map[string]*types.CollectedAsset)
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
			if t.url == finalURL || t.url == job.url {
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

		// Fetch each asset via JS eval with timeout control.
		for _, af := range toFetch {
			// Check context cancellation.
			select {
			case <-ctx.Done():
				goto doneCollecting
			default:
			}

			// Use a separate context with timeout for each fetch.
			assetCtx, cancel := context.WithTimeout(ctx, perAssetTimeout)

			// JS code to fetch the asset and return as base64.
			jsCode := fmt.Sprintf(`
				async (url, maxSize) => {
					try {
						const controller = new AbortController();
						const signal = controller.signal;
						const timeoutId = setTimeout(() => controller.abort(), %d);
						const resp = await fetch(url, {
							signal,
							credentials: 'include',
							mode: 'cors',
							cache: 'force-cache'
						}).catch(() => null);
						clearTimeout(timeoutId);
						if (!resp || !resp.ok) return null;
						const buf = await resp.arrayBuffer();
						if (buf.byteLength > maxSize) return null;
						const bytes = new Uint8Array(buf);
						let binary = '';
						for (let i = 0; i < bytes.length; i++) {
							binary += String.fromCharCode(bytes[i]);
						}
						return btoa(binary);
					} catch(e) {
						return null;
					}
				}
			`, int(perAssetTimeout.Milliseconds()))

			result, err := page.Context(assetCtx).Eval(jsCode, af.url, maxAssetSize)
			cancel()

			if err != nil || result == nil || result.Value.Nil() {
				failCount++
				continue
			}

			b64 := result.Value.String()
			if b64 == "" {
				failCount++
				continue
			}

			bodyBytes, err := base64.StdEncoding.DecodeString(b64)
			if err != nil || len(bodyBytes) == 0 {
				failCount++
				continue
			}

			if len(bodyBytes) <= maxAssetSize {
				collectedAssets[af.url] = &types.CollectedAsset{
					URL:         af.url,
					Body:        bodyBytes,
					ContentType: af.mimeType,
					StatusCode:  200,
				}
				successCount++
			} else {
				failCount++
			}
		}
	doneCollecting:

		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/clone] asset collection: %d tracked, %d to fetch, %d success, %d failed\n",
				len(allTracked), len(toFetch), successCount, failCount)
		}
	}

	html, err := page.HTML()
	if err != nil {
		job.resultCh <- renderResultOrErr{Err: fmt.Errorf("get HTML: %w", err)}
		return
	}

	// Extract all links from the rendered DOM using JavaScript.
	// This captures dynamically generated links that static HTML parsing may miss.
	var extractedLinks []string
	linksVal, evalErr := page.Eval(`() => {
		const links = new Set();
		// Get all anchor tags
		document.querySelectorAll('a[href]').forEach(a => {
			const href = a.getAttribute('href');
			if (href && !href.startsWith('#') && !href.startsWith('javascript:') && 
				!href.startsWith('mailto:') && !href.startsWith('tel:')) {
				links.add(a.href);
			}
		});
		// Get all area tags (image maps)
		document.querySelectorAll('area[href]').forEach(area => {
			const href = area.getAttribute('href');
			if (href && !href.startsWith('#') && !href.startsWith('javascript:')) {
				links.add(area.href);
			}
		});
		// Get iframe and frame sources
		document.querySelectorAll('iframe[src], frame[src]').forEach(f => {
			const src = f.getAttribute('src');
			if (src && !src.startsWith('javascript:')) {
				links.add(f.src);
			}
		});
		return JSON.stringify(Array.from(links));
	}`)
	if evalErr != nil {
		if util.DebugEnabled {
			fmt.Fprintf(os.Stderr, "[wukong/rod] eval links failed: %v\n", evalErr)
		}
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
			} else if util.DebugEnabled {
				fmt.Fprintf(os.Stderr, "[wukong/rod] failed to parse links JSON: %v (json: %s)\n", err, jsonStr[:100])
			}
		}
	}
	if util.DebugEnabled {
		fmt.Fprintf(os.Stderr, "[wukong/rod] extracted %d links from page\n", len(extractedLinks))
	}

	var title string
	titleEl, err := page.Element("title")
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
		finalURL = job.url
	}

	// Stop listening for network events
	// wait() - disabled for debugging

	job.resultCh <- renderResultOrErr{Result: &types.RenderResult{
		HTML:                html,
		URL:                 finalURL,
		Title:               title,
		ContentType:         contentType,
		CloudflareClearance: cfClearance,
		Referer:             job.referer,
		CollectedAssets:     collectedAssets,
		ExtractedLinks:      extractedLinks,
	}}
}

func (p *Pool) Render(ctx context.Context, url string) (*types.RenderResult, error) {
	return p.RenderWithReferer(ctx, url, "")
}

func (p *Pool) RenderWithReferer(ctx context.Context, url, referer string) (*types.RenderResult, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool closed")
	}
	p.mu.Unlock()

	resultCh := make(chan renderResultOrErr, 1)
	select {
	case p.queue <- &renderJob{url: url, referer: referer, resultCh: resultCh, ctx: ctx}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case res := <-resultCh:
		return res.Result, res.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
				fmt.Fprintf(nil, "[wukong/rod] stealth script injection warning: %v\n", err)
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
	if p.closed {
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
			fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset stealth injection warning: %v\n", err)
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
	fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer1 direct nav: trying for %s\n", assetURL)
	l1Ctx, l1Cancel := context.WithTimeout(assetCtx, 20*time.Second)
	l1Page := page.Context(l1Ctx)
	result, err := p.downloadAssetViaNavigation(l1Page, assetURL, referer, ua)
	l1Cancel()
	if err == nil && len(result.Body) > 0 {
		fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer1 direct nav: success for %s (%d bytes)\n", assetURL, len(result.Body))
		return result, nil
	}
	firstErr = err
	fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer1 direct nav: failed for %s: %v\n", assetURL, err)

	// Layer 2: <img> tag on Referer page (realistic browser behavior)
	// Tries loading the image as a subresource of the Referer page, which
	// provides the most natural request context (proper Referer, cookies, etc.)
	if isImageURL(assetURL) && referer != "" {
		fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer2 img-on-referer: trying for %s\n", assetURL)
		l2Ctx, l2Cancel := context.WithTimeout(assetCtx, 25*time.Second)
		l2Page := page.Context(l2Ctx)
		result, err2 := p.downloadAssetViaImgOnRefererPage(l2Page, assetURL, referer, ua)
		l2Cancel()
		if err2 == nil && len(result.Body) > 0 {
			fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer2 img-on-referer: success for %s (%d bytes)\n", assetURL, len(result.Body))
			return result, nil
		}
		fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer2 img-on-referer: failed for %s: %v\n", assetURL, err2)
	} else if !isImageURL(assetURL) {
		fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer2 img-on-referer: skipped for non-image resource %s\n", assetURL)
	} else {
		fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer2 img-on-referer: skipped (no referer) for %s\n", assetURL)
	}

	// Layer 3: Network.loadNetworkResource (CDP direct network load)
	fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer3 Network.loadNetworkResource: trying for %s\n", assetURL)
	l3Ctx, l3Cancel := context.WithTimeout(assetCtx, 20*time.Second)
	l3Page := page.Context(l3Ctx)
	result, err3 := p.downloadAssetViaLoadNetworkResource(l3Page, assetURL, referer, ua)
	l3Cancel()
	if err3 == nil && len(result.Body) > 0 {
		fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer3 Network.loadNetworkResource: success for %s (%d bytes)\n", assetURL, len(result.Body))
		return result, nil
	}
	fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer3 Network.loadNetworkResource: failed for %s: %v\n", assetURL, err3)

	// Layer 4: JS fetch
	fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer4 fetch: trying for %s\n", assetURL)
	l4Ctx, l4Cancel := context.WithTimeout(assetCtx, 15*time.Second)
	l4Page := page.Context(l4Ctx)
	result, err4 := p.downloadAssetViaFetch(l4Page, assetURL, referer)
	l4Cancel()
	if err4 == nil && len(result.Body) > 0 {
		fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer4 fetch: success for %s (%d bytes)\n", assetURL, len(result.Body))
		return result, nil
	}
	fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset layer4 fetch: failed for %s: %v\n", assetURL, err4)

	// Fallback: try full-resolution URL variant for media.defense.gov assets
	// Sometimes the 300x300 thumbnail endpoint is blocked while full-resolution works
	if strings.Contains(assetURL, "media.defense.gov") && strings.Contains(assetURL, "/300/300/0/") {
		fullResURL := strings.Replace(assetURL, "/300/300/0/", "/-1/-1/0/", 1)
		fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset trying full-resolution variant: %s\n", fullResURL)

		l5Ctx, l5Cancel := context.WithTimeout(assetCtx, 20*time.Second)
		l5Page := page.Context(l5Ctx)
		result5, err5 := p.downloadAssetViaNavigation(l5Page, fullResURL, referer, ua)
		l5Cancel()
		if err5 == nil && len(result5.Body) > 0 {
			fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset full-resolution variant success: %s (%d bytes)\n", fullResURL, len(result5.Body))
			return result5, nil
		}
		fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset full-resolution variant failed: %v\n", err5)

		// Also try img-on-referer with full-resolution URL
		if referer != "" {
			l5bCtx, l5bCancel := context.WithTimeout(assetCtx, 25*time.Second)
			l5bPage := page.Context(l5bCtx)
			result5b, err5b := p.downloadAssetViaImgOnRefererPage(l5bPage, fullResURL, referer, ua)
			l5bCancel()
			if err5b == nil && len(result5b.Body) > 0 {
				fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset full-resolution variant (img-on-referer) success: %s (%d bytes)\n", fullResURL, len(result5b.Body))
				return result5b, nil
			}
			fmt.Fprintf(os.Stderr, "[wukong/rod] DownloadAsset full-resolution variant (img-on-referer) failed: %v\n", err5b)
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
		} else if s != "" && contentType != "" && isTextContent(contentType) {
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
		fmt.Fprintf(os.Stderr, "[wukong/rod]   img-on-referer: using cached referer page for %s\n", referer)
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
		fmt.Fprintf(os.Stderr, "[wukong/rod]   img-on-referer: navigating to referer page: %s\n", referer)
		navErr := page.Navigate(referer)
		if navErr != nil {
			fmt.Fprintf(os.Stderr, "[wukong/rod]   img-on-referer: referer nav error: %v\n", navErr)
			// If referer page fails to navigate, try about:blank instead
			if err := page.Navigate("about:blank"); err != nil {
				return nil, fmt.Errorf("navigate to about:blank fallback: %w", err)
			}
			page.WaitLoad()
		} else {
			if err := page.WaitLoad(); err != nil {
				fmt.Fprintf(os.Stderr, "[wukong/rod]   img-on-referer: referer waitLoad warning: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "[wukong/rod]   img-on-referer: fetch succeeded (type=%s), trying img element for body\n", fetchType)
		} else {
			fetchErrMsg := fetchResult.Value.Get("error").String()
			fmt.Fprintf(os.Stderr, "[wukong/rod]   img-on-referer: fetch failed: %s\n", fetchErrMsg)
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
			fmt.Fprintf(os.Stderr, "[wukong/rod]   img-on-referer: img.onerror: type=%s, msg=%s\n", errType, errMsg)
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
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	close(p.queue)
	p.wg.Wait()

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
		fmt.Fprintf(os.Stderr, "[wukong/rod]   img-on-referer: failed to pre-load referer page: %v\n", err)
		return nil
	}
	rp.WaitLoad()
	navCancel()

	rp = rp.Context(context.Background())

	p.refererPages[referer] = rp
	fmt.Fprintf(os.Stderr, "[wukong/rod]   img-on-referer: pre-loaded referer page for %s\n", referer)

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
			fmt.Fprintf(os.Stderr, "[wukong/rod] injectRefererCookies: failed to set cookies: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "[wukong/rod] injectRefererCookies: injected %d cookies from referer\n", len(cookieParams))
		}
	}
}

func isHTMLContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return true
	}
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return ct == "text/html" || ct == "application/xhtml+xml"
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

// isTextContent returns true if the MIME type represents a text-based resource
// that can be read as plain text (CSS, JavaScript, JSON, etc.).
func isTextContent(mimeType string) bool {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	if i := strings.Index(mt, ";"); i >= 0 {
		mt = mt[:i]
	}
	return strings.HasPrefix(mt, "text/") ||
		strings.HasPrefix(mt, "application/javascript") ||
		strings.HasPrefix(mt, "application/json") ||
		strings.HasPrefix(mt, "application/xml") ||
		strings.HasPrefix(mt, "application/xhtml+xml") ||
		strings.HasPrefix(mt, "image/svg+xml")
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
