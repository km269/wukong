package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/io"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/km269/wukong/internal/browser/antibot"
	"github.com/km269/wukong/internal/browser/behavior"
	"github.com/km269/wukong/internal/browser/settle"
	"github.com/km269/wukong/internal/browser/stealth"
	"github.com/km269/wukong/internal/browser/types"
	"github.com/km269/wukong/internal/config"
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
	allocCtx           context.Context
	allocCl            context.CancelFunc
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
	idx    int
	ctx    context.Context
	cancel context.CancelFunc
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

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("mute-audio", true),
		// Start with a maximized window for more realistic fingerprint
		chromedp.Flag("start-maximized", true),
		// Disable IPv6 to avoid connection issues on networks where
		// IPv6 is restricted (e.g. "access forbidden by access permissions"
		// errors when connecting to IPv6 addresses like defense.gov CDNs).
		chromedp.Flag("disable-ipv6", true),
	)

	// Use new headless mode (Chrome 112+) which behaves much closer
	// to a real browser and is less likely to trigger detection.
	// The old --headless flag has many differences from headed Chrome.
	if opts.Headless {
		allocOpts = append(allocOpts, chromedp.Flag("headless", "new"))
	}

	if opts.Proxy != "" {
		allocOpts = append(allocOpts, chromedp.ProxyServer(opts.Proxy))
	}

	if opts.DisableDownloads {
		allocOpts = append(allocOpts,
			chromedp.Flag("disable-features", "DownloadBubble,DownloadBubbleV2"),
			chromedp.Flag("safebrowsing-disable-auto-update", true),
		)
	}

	if opts.Stealth {
		allocOpts = append(allocOpts,
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
			chromedp.Flag("disable-infobars", true),
			chromedp.Flag("no-default-browser-check", true),
			chromedp.Flag("no-first-run", true),
			chromedp.Flag("disable-component-update", true),
			chromedp.Flag("window-size", "1920,1080"),
		)
	}

	if opts.ChromeBin != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(opts.ChromeBin))
	}

	if opts.ProfileDir != "" {
		allocOpts = append(allocOpts, chromedp.UserDataDir(opts.ProfileDir))
	}

	allocCtx, allocCl := chromedp.NewExecAllocator(context.Background(), allocOpts...)

	escalator := antibot.NewEscalator(antibot.DefaultEscalatorConfig())
	currentUA := escalator.GetRandomDesktopUA()

	p := &Pool{
		opts:              opts,
		allocCtx:          allocCtx,
		allocCl:           allocCl,
		queue:             make(chan *renderJob, opts.Workers*4),
		behaviorSimulator: behavior.New(behavior.DefaultConfig()),
		escalator:         escalator,
		currentUA:         currentUA,
	}

	for i := 0; i < opts.Workers; i++ {
		wCtx, wCancel := chromedp.NewContext(allocCtx)
		if opts.Stealth {
			stealth.Inject(wCtx)
		}
		p.workers = append(p.workers, &worker{idx: i, ctx: wCtx, cancel: wCancel})
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
	_, cancel := context.WithTimeout(job.ctx, p.opts.RenderTimeout)
	defer cancel()

	tabCtx, tabCancel := chromedp.NewContext(w.ctx)
	defer tabCancel()

	var html, title, finalURL, contentType string

	ua := p.getCurrentUA()

	if err := chromedp.Run(tabCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			headers := network.Headers{
				"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
				"Accept-Language":           "en-US,en;q=0.9",
				"Accept-Encoding":           "gzip, deflate, br",
				"Connection":                "keep-alive",
				"Sec-Ch-Ua":                 ua.SecChUa,
				"Sec-Ch-Ua-Mobile":          ua.SecChUaMobile,
				"Sec-Ch-Ua-Platform":        ua.SecChUaPlatform,
				"Sec-Fetch-Dest":            "document",
				"Sec-Fetch-Mode":            "navigate",
				"Sec-Fetch-Site":            "none",
				"Sec-Fetch-User":            "?1",
				"Upgrade-Insecure-Requests": "1",
				"User-Agent":                ua.UserAgent,
			}
			if job.referer != "" {
				headers["Referer"] = job.referer
				headers["Sec-Fetch-Site"] = "same-origin"
				headers["Sec-Fetch-User"] = "?1"
			}
			return network.SetExtraHTTPHeaders(headers).Do(ctx)
		}),
		chromedp.Navigate(job.url),
	); err != nil {
		job.resultCh <- renderResultOrErr{Err: fmt.Errorf("navigate: %w", err)}
		return
	}

	settle.Wait(tabCtx, p.opts.Settle)

	// 如果启用了行为模拟
	if p.behaviorSimEnabled {
		// 模拟自然的滚动和鼠标移动
		var body string
		chromedp.Run(tabCtx,
			// 首先滚动一小段
			chromedp.Evaluate(`
				(async () => {
					// 随机滚动一小段距离
					const randomScroll = () => {
						const delta = Math.floor(Math.random() * 200) - 100;
						window.scrollBy(0, delta);
						return new Promise(r => setTimeout(r, 200 + Math.random() * 300));
					};
					await randomScroll();
				})()`, &body),
		)

		// 鼠标移动到随机位置
		chromedp.Run(tabCtx,
			chromedp.Evaluate(`
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
				})()`, &body),
		)
	}

	if p.opts.Scroll {
		var body string
		chromedp.Run(tabCtx,
			chromedp.Evaluate(`
				(async () => {
					for (let i = 0; i < 5; i++) {
						window.scrollBy(0, window.innerHeight);
						await new Promise(r => setTimeout(r, 500 + Math.random() * 300));
					}
				})()`, &body),
		)
		settle.Wait(tabCtx, p.opts.Settle)
	}

	if err := chromedp.Run(tabCtx,
		chromedp.Title(&title),
		chromedp.OuterHTML("html", &html),
		chromedp.Evaluate(`window.location.href`, &finalURL),
		chromedp.Evaluate(`document.contentType`, &contentType),
	); err != nil {
		job.resultCh <- renderResultOrErr{Err: fmt.Errorf("render: %w", err)}
		return
	}

	if !isHTMLContentType(contentType) {
		job.resultCh <- renderResultOrErr{Err: &types.ErrNotHTML{URL: job.url, ContentType: contentType}}
		return
	}

	// Extract all links from the rendered DOM using JavaScript.
	var extractedLinks []string
	chromedp.Run(tabCtx,
		chromedp.Evaluate(`
			(function() {
				const links = new Set();
				document.querySelectorAll('a[href]').forEach(a => {
					const href = a.getAttribute('href');
					if (href && !href.startsWith('#') && !href.startsWith('javascript:') && 
						!href.startsWith('mailto:') && !href.startsWith('tel:')) {
						links.add(a.href);
					}
				});
				document.querySelectorAll('area[href]').forEach(area => {
					const href = area.getAttribute('href');
					if (href && !href.startsWith('#') && !href.startsWith('javascript:')) {
						links.add(area.href);
					}
				});
				document.querySelectorAll('iframe[src], frame[src]').forEach(f => {
					const src = f.getAttribute('src');
					if (src && !src.startsWith('javascript:')) {
						links.add(f.src);
					}
				});
				return Array.from(links);
			})()
		`, &extractedLinks),
	)

	var cfClearance string
	var cookies []*network.Cookie
	if err := chromedp.Run(tabCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cookies, err = network.GetCookies().WithURLs([]string{job.url}).Do(ctx)
			return err
		})); err == nil {
		for _, c := range cookies {
			if c.Name == "cf_clearance" {
				cfClearance = c.Value
				break
			}
		}
	}

	if finalURL == "" {
		finalURL = job.url
	}

	job.resultCh <- renderResultOrErr{Result: &types.RenderResult{
		HTML:                html,
		URL:                 finalURL,
		Title:               title,
		ContentType:         contentType,
		CloudflareClearance: cfClearance,
		Referer:             job.referer,
		ExtractedLinks:      extractedLinks,
	}}
}

func isHTMLContentType(ct string) bool {
	if ct == "" {
		return true
	}
	return ct == "text/html" || ct == "application/xhtml+xml"
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
	return nil
}

func (p *Pool) SetBehaviorSimulation(enabled bool) {
	p.behaviorSimEnabled = enabled
}

// DownloadAsset downloads an asset using the browser's network stack.
// It uses a temporary tab to navigate to the asset URL and extracts the response body
// via CDP Network.getResponseBody.
func (p *Pool) DownloadAsset(ctx context.Context, assetURL string, referer string) (*types.AssetDownloadResult, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool closed")
	}
	p.mu.Unlock()

	// Create the tab context from the pool's allocCtx so it shares the same
	// browser instance as the worker pool. Creating from a non-chromedp context
	// would spawn a completely new browser instance per call, which is slow
	// and doesn't inherit any pool-level configuration.
	tabCtx, tabCancel := chromedp.NewContext(p.allocCtx)
	tabCtx, tabTimeoutCancel := context.WithTimeout(tabCtx, p.opts.RenderTimeout)
	defer tabTimeoutCancel()
	defer tabCancel()

	// Propagate cancellation from the caller's context to the tab context.
	// This ensures that if the caller cancels ctx, the tab operation also stops.
	go func() {
		select {
		case <-ctx.Done():
			tabTimeoutCancel()
			tabCancel()
		case <-tabCtx.Done():
		}
	}()

	// Inject stealth scripts to hide automation indicators.
	// Without stealth, headless Chrome is easily detected and may be
	// blocked by anti-bot protections or trigger ERR_BLOCKED_BY_CLIENT.
	if p.opts.Stealth {
		stealth.Inject(tabCtx)
	}

	var requestID network.RequestID
	var contentType string
	var statusCode int
	var navErr error

	ua := p.getCurrentUA()

	// First, navigate to the origin of the asset to establish a proper
	// browsing context, cookies, and session. Real users never navigate
	// directly to an image URL as their first request to a domain.
	// This step helps bypass anti-bot checks that look for direct image
	// access without a preceding page visit.
	if parsedURL, err := url.Parse(assetURL); err == nil {
		origin := parsedURL.Scheme + "://" + parsedURL.Host + "/"
		// Use a short timeout for the warm-up navigation — if it fails,
		// we still try the actual asset download.
		warmupCtx, warmupCancel := context.WithTimeout(tabCtx, 10*time.Second)
		defer warmupCancel()
		chromedp.Run(warmupCtx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				headers := network.Headers{
					"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
					"Accept-Language": "en-US,en;q=0.9",
					"User-Agent":      ua.UserAgent,
				}
				return network.SetExtraHTTPHeaders(headers).Do(ctx)
			}),
			chromedp.Navigate(origin),
		)
	}

	err := chromedp.Run(tabCtx,
		// Enable downloads — Chrome may block direct navigation to binary resources
		// when download restrictions are enabled (download_restrictions=3).
		// This can cause ERR_BLOCKED_BY_CLIENT for image files.
		chromedp.ActionFunc(func(ctx context.Context) error {
			return browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).Do(ctx)
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			// Determine Sec-Fetch-Site based on whether referer host matches asset host.
			secFetchSite := "same-origin"
			if referer != "" {
				if refURL, err := url.Parse(referer); err == nil {
					if assetURLParsed, err2 := url.Parse(assetURL); err2 == nil {
						if refURL.Host != assetURLParsed.Host {
							secFetchSite = "cross-site"
						}
					}
				}
			}
			headers := network.Headers{
				"Accept":             "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
				"Accept-Language":    "en-US,en;q=0.9",
				"Accept-Encoding":    "gzip, deflate, br",
				"Connection":         "keep-alive",
				"Sec-Ch-Ua":          ua.SecChUa,
				"Sec-Ch-Ua-Mobile":   ua.SecChUaMobile,
				"Sec-Ch-Ua-Platform": ua.SecChUaPlatform,
				"Sec-Fetch-Dest":     "image",
				"Sec-Fetch-Mode":     "no-cors",
				"Sec-Fetch-Site":     secFetchSite,
				"User-Agent":         ua.UserAgent,
			}
			if referer != "" {
				headers["Referer"] = referer
			}
			return network.SetExtraHTTPHeaders(headers).Do(ctx)
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			chromedp.ListenTarget(ctx, func(ev interface{}) {
				if resp, ok := ev.(*network.EventResponseReceived); ok {
					if resp.Response.URL == assetURL {
						requestID = resp.RequestID
						contentType = resp.Response.MimeType
						statusCode = int(resp.Response.Status)
					}
				}
			})
			return nil
		}),
		chromedp.Navigate(assetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
	)
	navErr = err

	var bodyBytes []byte
	if navErr == nil && requestID != "" {
		err = chromedp.Run(tabCtx,
			chromedp.ActionFunc(func(ctx context.Context) error {
				var err error
				bodyBytes, err = network.GetResponseBody(requestID).Do(ctx)
				return err
			}),
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer1 direct nav: get response body failed for %s: %v\n", assetURL, err)
			bodyBytes = nil
		} else if len(bodyBytes) > 0 {
			fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer1 direct nav: success for %s (%d bytes)\n", assetURL, len(bodyBytes))
		}
	} else {
		fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer1 direct nav: failed for %s (navErr=%v, requestID=%q)\n", assetURL, navErr, requestID)
	}

	// Fallback 2: Load the asset via an <img> tag in a page context.
	// Direct navigation to binary resources may trigger ERR_BLOCKED_BY_CLIENT
	// due to download restrictions, but loading as a subresource (via <img>)
	// behaves like a normal page load and bypasses those restrictions.
	if len(bodyBytes) == 0 {
		fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer2 img tag: trying for %s\n", assetURL)
		var imgRequestID network.RequestID
		var imgContentType string
		var imgStatusCode int

		// Navigate to about:blank first to have a clean page context
		err := chromedp.Run(tabCtx,
			chromedp.Navigate("about:blank"),
			chromedp.WaitReady("body", chromedp.ByQuery),
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer2 img tag: navigate to about:blank failed for %s: %v\n", assetURL, err)
		}
		if err == nil {
			// Listen for the image response
			err = chromedp.Run(tabCtx,
				chromedp.ActionFunc(func(ctx context.Context) error {
					chromedp.ListenTarget(ctx, func(ev interface{}) {
						if resp, ok := ev.(*network.EventResponseReceived); ok {
							if resp.Response.URL == assetURL {
								imgRequestID = resp.RequestID
								imgContentType = resp.Response.MimeType
								imgStatusCode = int(resp.Response.Status)
							}
						}
					})
					return nil
				}),
				// Create an img element and set its src to trigger the load
				chromedp.ActionFunc(func(ctx context.Context) error {
					jsExpr := fmt.Sprintf(`
						(() => {
							const img = new Image();
							img.onload = () => window.__imgLoaded = true;
							img.onerror = () => window.__imgLoaded = 'error';
							img.src = %q;
							document.body.appendChild(img);
						})()
					`, assetURL)
					return chromedp.Evaluate(jsExpr, nil).Do(ctx)
				}),
				// Wait for the image to load or fail (polling)
				chromedp.ActionFunc(func(ctx context.Context) error {
					deadline := time.Now().Add(15 * time.Second)
					ticker := time.NewTicker(100 * time.Millisecond)
					defer ticker.Stop()
					for time.Now().Before(deadline) {
						var loaded interface{}
						chromedp.Evaluate(`window.__imgLoaded`, &loaded).Do(ctx)
						if loaded != nil {
							return nil
						}
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-ticker.C:
						}
					}
					return nil
				}),
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer2 img tag: load image failed for %s: %v\n", assetURL, err)
			} else if imgRequestID == "" {
				fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer2 img tag: no request ID captured for %s\n", assetURL)
			}
			if err == nil && imgRequestID != "" {
				var respBody []byte
				respBody, err = network.GetResponseBody(imgRequestID).Do(tabCtx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer2 img tag: get response body failed for %s: %v\n", assetURL, err)
				} else if len(respBody) > 0 {
					fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer2 img tag: success for %s (%d bytes)\n", assetURL, len(respBody))
				}
				if err == nil && len(respBody) > 0 {
					bodyBytes = respBody
					contentType = imgContentType
					statusCode = imgStatusCode
				}
			}
		}
	}

	// Fallback 3: use JavaScript fetch with base64 encoding for binary safety.
	// This works even when direct navigation fails (e.g., ERR_BLOCKED_BY_CLIENT)
	// because fetch() runs in the page context and behaves differently.
	if len(bodyBytes) == 0 {
		fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer3 fetch: trying for %s\n", assetURL)
		// First navigate to the origin of the asset so that fetch() has
		// a proper same-origin context, reducing CORS issues.
		if navErr != nil {
			if parsedURL, err := url.Parse(assetURL); err == nil {
				origin := parsedURL.Scheme + "://" + parsedURL.Host + "/"
				navErr2 := chromedp.Run(tabCtx, chromedp.Navigate(origin))
				if navErr2 != nil {
					fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer3 fetch: navigate to origin %s failed: %v\n", origin, navErr2)
				}
			}
		}
		var result struct {
			Body        string `json:"body"`
			ContentType string `json:"contentType"`
			Status      int    `json:"status"`
			OK          bool   `json:"ok"`
		}
		jsExpr := fmt.Sprintf(`
			(async () => {
				const url = %q;
				const ref = %q;
				try {
					const controller = new AbortController();
					const signal = controller.signal;
					const timeoutId = setTimeout(() => controller.abort(), 15000);
					const fetchOpts = {
						signal,
						credentials: 'include',
						mode: 'cors',
						cache: 'force-cache',
						redirect: 'follow'
					};
					if (ref) {
						fetchOpts.referrer = ref;
						fetchOpts.referrerPolicy = 'no-referrer-when-downgrade';
					}
					const res = await fetch(url, fetchOpts);
					clearTimeout(timeoutId);
					if (!res.ok) {
						return { body: '', contentType: '', status: res.status, ok: false };
					}
					const buf = await res.arrayBuffer();
					const bytes = new Uint8Array(buf);
					let binary = '';
					for (let i = 0; i < bytes.byteLength; i++) {
						binary += String.fromCharCode(bytes[i]);
					}
					return {
						body: btoa(binary),
						contentType: res.headers.get('content-type') || '',
						status: res.status,
						ok: true
					};
				} catch(e) {
					return { body: '', contentType: '', status: 0, ok: false };
				}
			})()
		`, assetURL, referer)
		fetchErr := chromedp.Run(tabCtx,
			chromedp.Evaluate(jsExpr, &result),
		)
		if fetchErr != nil {
			fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer3 fetch: evaluate failed for %s: %v\n", assetURL, fetchErr)
		} else if !result.OK {
			fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer3 fetch: fetch not OK for %s (status=%d)\n", assetURL, result.Status)
		} else if result.Body == "" {
			fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer3 fetch: empty body for %s\n", assetURL)
		}
		if fetchErr == nil && result.OK && result.Body != "" {
			bodyBytes, err = base64.StdEncoding.DecodeString(result.Body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer3 fetch: base64 decode failed for %s: %v\n", assetURL, err)
				bodyBytes = nil
			} else if len(bodyBytes) > 0 {
				fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer3 fetch: success for %s (%d bytes)\n", assetURL, len(bodyBytes))
			}
			if contentType == "" {
				contentType = result.ContentType
			}
			if statusCode == 0 {
				statusCode = result.Status
			}
		}
	}

	if len(bodyBytes) == 0 {
		// Fallback 4: use Network.loadNetworkResource CDP method.
		// This directly loads the resource through Chrome's network stack,
		// bypassing page-level restrictions that cause ERR_BLOCKED_BY_CLIENT.
		// It requires a frame context, so we navigate to about:blank first.
		fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer4 Network.loadNetworkResource: trying for %s\n", assetURL)
		var frameID cdp.FrameID
		err = chromedp.Run(tabCtx,
			chromedp.Navigate("about:blank"),
			chromedp.WaitReady("body", chromedp.ByQuery),
			chromedp.ActionFunc(func(ctx context.Context) error {
				tree, err := page.GetFrameTree().Do(ctx)
				if err != nil {
					return err
				}
				frameID = tree.Frame.ID
				return nil
			}),
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer4 Network.loadNetworkResource: navigate/get frame failed for %s: %v\n", assetURL, err)
		}
		if err == nil && frameID != "" {
			result, err := network.LoadNetworkResource(assetURL, &network.LoadNetworkResourceOptions{
				DisableCache:       false,
				IncludeCredentials: true,
			}).WithFrameID(frameID).Do(tabCtx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer4 Network.loadNetworkResource: load failed for %s: %v\n", assetURL, err)
			} else if !result.Success {
				fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer4 Network.loadNetworkResource: not successful for %s (httpStatus=%d)\n", assetURL, result.HTTPStatusCode)
			} else if result.Stream == "" {
				fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer4 Network.loadNetworkResource: no stream for %s\n", assetURL)
			}
			if err == nil && result.Success && result.Stream != "" {
				// Read the stream content using IO.read
				var data []byte
				for {
					readData, eof, readErr := io.Read(result.Stream).Do(tabCtx)
					if readErr != nil {
						break
					}
					// Network.loadNetworkResource returns binary data as base64
					decoded, decodeErr := base64.StdEncoding.DecodeString(readData)
					if decodeErr == nil && len(decoded) > 0 {
						data = append(data, decoded...)
					} else {
						data = append(data, []byte(readData)...)
					}
					if eof {
						break
					}
				}
				io.Close(result.Stream).Do(tabCtx)
				if len(data) > 0 {
					fmt.Fprintf(os.Stderr, "[wukong/browser] DownloadAsset layer4 Network.loadNetworkResource: success for %s (%d bytes)\n", assetURL, len(data))
					bodyBytes = data
					if statusCode == 0 && result.HTTPStatusCode > 0 {
						statusCode = int(result.HTTPStatusCode)
					}
					// Try to extract content-type from headers
					if contentType == "" && result.Headers != nil {
						if ct, ok := result.Headers["Content-Type"]; ok {
							if ctStr, ok := ct.(string); ok {
								contentType = ctStr
							}
						}
					}
				}
			}
		}
	}

	if len(bodyBytes) == 0 {
		if navErr != nil {
			return nil, fmt.Errorf("navigate to asset: %w", navErr)
		}
		return nil, fmt.Errorf("failed to get asset body")
	}

	return &types.AssetDownloadResult{
		URL:         assetURL,
		Body:        bodyBytes,
		ContentType: contentType,
		StatusCode:  statusCode,
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
		w.cancel()
	}
	p.allocCl()
}

func (p *Pool) CloseWithError() error {
	p.Close()
	return nil
}

func NewPoolFromConfig(cfg *config.BrowserConfig) *Pool {
	if cfg == nil {
		return nil
	}

	settleTimeout := 2 * time.Second
	if cfg.Timeout > 0 {
		settleTimeout = cfg.Timeout / 3
		if settleTimeout < 1*time.Second {
			settleTimeout = 1 * time.Second
		}
	}

	workers := 4
	if cfg.Workers > 0 {
		workers = cfg.Workers
	}

	return New(Options{
		Headless:         cfg.Headless,
		Workers:          workers,
		Settle:           settleTimeout,
		RenderTimeout:    cfg.Timeout,
		Scroll:           cfg.Scroll,
		ChromeBin:        cfg.BrowserPath,
		ControlURL:       cfg.ControlURL,
		Stealth:          cfg.Stealth,
		ProfileDir:       cfg.ProfileDir,
		DisableDownloads: true, // 默认禁止浏览器自动下载,由 cloner 统一管理资源.
	})
}
