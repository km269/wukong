package rodbackend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
		if !opts.Headless {
			l = l.Headless(false)
		}
		if opts.ProfileDir != "" {
			l = l.UserDataDir(opts.ProfileDir)
		}
		if needNoSandbox() {
			l = l.NoSandbox(true)
		}
		l = l.Devtools(false)
		if opts.DisableDownloads {
			l = l.Set("download_restrictions", "3")
		}
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
			proto.BrowserSetDownloadBehavior{
				Behavior: proto.BrowserSetDownloadBehaviorBehaviorDeny,
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

	// Set extra HTTP headers for more realistic browser fingerprint.
	headers := proto.NetworkHeaders{
		"Accept":                    gson.New("text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"),
		"Accept-Language":           gson.New("en-US,en;q=0.9"),
		"Sec-Ch-Ua":                 gson.New(ua.SecChUa),
		"Sec-Ch-Ua-Mobile":          gson.New(ua.SecChUaMobile),
		"Sec-Ch-Ua-Platform":        gson.New(ua.SecChUaPlatform),
		"Sec-Fetch-Dest":            gson.New("document"),
		"Sec-Fetch-Mode":            gson.New("navigate"),
		"Sec-Fetch-Site":            gson.New("none"),
		"Sec-Fetch-User":            gson.New("?1"),
		"Upgrade-Insecure-Requests": gson.New("1"),
		"User-Agent":                gson.New(ua.UserAgent),
	}
	proto.NetworkSetExtraHTTPHeaders{
		Headers: headers,
	}.Call(page)
	proto.NetworkSetUserAgentOverride{
		UserAgent: ua.UserAgent,
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
// Uses a two-stage strategy:
//  1. First, try direct page navigation to the asset URL (like typing the
//     URL into the browser address bar). This bypasses CORS restrictions
//     because top-level navigation is not subject to CORS.
//  2. If that fails, fall back to JS fetch for CORS-enabled resources.
func (p *Pool) DownloadAsset(ctx context.Context, assetURL string, referer string) (*types.AssetDownloadResult, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool closed")
	}
	p.mu.Unlock()

	assetCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	page, err := p.browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}
	defer page.Close()
	page = page.Context(assetCtx)

	ua := p.getCurrentUA()
	if ua != nil {
		proto.NetworkSetUserAgentOverride{
			UserAgent: ua.UserAgent,
		}.Call(page)
	}

	// Stage 1: direct navigation — works for any resource, no CORS issues.
	// We navigate to the asset URL directly and capture the response body.
	result, err := p.downloadAssetViaNavigation(page, assetURL, referer)
	if err == nil && len(result.Body) > 0 {
		return result, nil
	}

	// Stage 2: JS fetch fallback for CORS-enabled resources.
	result, err2 := p.downloadAssetViaFetch(page, assetURL, referer)
	if err2 == nil && len(result.Body) > 0 {
		return result, nil
	}

	// Both failed — return the first error.
	if err != nil {
		return nil, err
	}
	return nil, err2
}

// downloadAssetViaNavigation downloads an asset by directly navigating to it.
// This bypasses CORS because top-level navigation is not subject to CORS.
// It works like opening the image URL directly in a browser tab.
func (p *Pool) downloadAssetViaNavigation(page *rod.Page, assetURL, referer string) (*types.AssetDownloadResult, error) {
	if referer != "" {
		proto.NetworkSetExtraHTTPHeaders{
			Headers: proto.NetworkHeaders{
				"Referer": gson.New(referer),
			},
		}.Call(page)
	}

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
		if e.Response.URL == assetURL {
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

	if resp == nil {
		return nil, fmt.Errorf("no response captured for %s", assetURL)
	}

	if resp.status < 200 || resp.status >= 300 {
		return nil, fmt.Errorf("asset HTTP %d", resp.status)
	}

	// Try to get the response body via CDP.
	body, err := page.GetResource(assetURL)
	if err == nil && len(body) > 0 {
		return &types.AssetDownloadResult{
			URL:         assetURL,
			Body:        body,
			ContentType: resp.mimeType,
			StatusCode:  resp.status,
		}, nil
	}

	// Fallback: use JS to read the content via canvas for images,
	// or via XMLHttpRequest with no-cors mode won't work for reading body.
	// Try extracting from the page if it's rendered as an image.
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

	return nil, fmt.Errorf("could not extract asset body from navigation")
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

	p.browser.MustClose()
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
