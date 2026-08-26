package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
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
	"github.com/km269/wukong/internal/browser/renderkit"
	"github.com/km269/wukong/internal/browser/settle"
	"github.com/km269/wukong/internal/browser/stealth"
	"github.com/km269/wukong/internal/browser/types"
	"github.com/km269/wukong/pkg/logutil"
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
	// GeoRegion pins the fingerprint geography to the proxy exit
	// region. Empty = infer from Proxy, else random.
	GeoRegion string
}

type Pool struct {
	opts               Options
	allocCtx           context.Context
	allocCl            context.CancelFunc
	lifeCancel         context.CancelFunc
	workers            []*worker
	disp               *renderkit.Dispatcher
	mu                 sync.Mutex
	behaviorSimEnabled bool
	behaviorSimulator  *behavior.Simulator
	escalator          *antibot.Escalator
	currentUA          *antibot.UAProfile
	// stealthFP is the session-stable fingerprint shared by every tab
	// (GPU/screen/canvas persona + geo). Generated once in New().
	stealthFP *stealth.Fingerprint
}

// stealthScript renders the session-stable stealth payload. The
// chromedp backend sets the UA via HTTP headers, so no client-hints
// identity section is baked in here (nil keeps genuine platform
// values, which stay coherent with the Chrome-only UA pool).
func (p *Pool) stealthScript() string {
	return stealth.BuildScript(p.stealthFP, nil)
}

// Compile-time capability assertions.
var (
	_ types.BrowserBackend   = (*Pool)(nil)
	_ types.UARotator        = (*Pool)(nil)
	_ types.PriorityRenderer = (*Pool)(nil)
)

type worker struct {
	idx    int
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a chromedp-backed browser pool whose lifetime is bound
// to ctx: when ctx is cancelled (parent task done, Ctrl-C, timeout)
// the pool drains in-flight renders and releases Chrome automatically,
// so a caller that forgets Close() cannot leak browser processes.
// Close() remains the explicit cleanup path; it is idempotent and also
// releases the internal lifecycle goroutine.
func New(ctx context.Context, opts Options) *Pool {
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
		// Disable HTTP cache to ensure fresh connections
		chromedp.Flag("disable-http-cache", true),
		// Disable background networking that may interfere
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-client-side-phishing-detection", true),
		chromedp.Flag("disable-component-update", true),
		chromedp.Flag("disable-component-extensions-with-background-pages", true),
		chromedp.Flag("disable-default-apps", true),
		chromedp.Flag("disable-prompt-on-repost", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-search-geolocation-disclosure", true),
		// Aggressively disable SafeBrowsing — it causes ERR_BLOCKED_BY_CLIENT
		// on .mil CDN URLs such as media.defense.gov.
		chromedp.Flag("disable-features", "SafeBrowsing,SafeBrowsingDownloadProtection,SafeBrowsingCsd,SafeBrowsingReporting,IsolateOrigins,DownloadBubble,DownloadBubbleV2"),
		chromedp.Flag("safebrowsing-disable-auto-update", true),
		chromedp.Flag("safebrowsing-disable-download-protection", true),
		chromedp.Flag("safebrowsing-disable-extension-blacklist", true),
		chromedp.Flag("allow-insecure-localhost", true),
		// Reduce download/enterprise policy blocking
		chromedp.Flag("disable-policy-background-loading", true),
		chromedp.Flag("disable-extensions-except", ""),
		// Allow cross-origin and mixed content needed for defense.gov CDN
		chromedp.Flag("disable-web-security", false),
		chromedp.Flag("allow-running-insecure-content", true),
		chromedp.Flag("reduce-security-for-testing", false),
	)

	// Ignore certificate errors only when InsecureTLS is enabled — .mil/.gov
	// sites use DoD certificates not in the standard trust store. Otherwise
	// Chrome performs strict (default) certificate verification.
	if opts.InsecureTLS {
		allocOpts = append(allocOpts,
			chromedp.Flag("ignore-certificate-errors", true),
			chromedp.Flag("ignore-ssl-errors", true),
		)
		logutil.Debug("[chromedp] browser TLS: certificate verification disabled (insecure_tls)")
	} else {
		logutil.Debug("[chromedp] browser TLS: strict certificate verification")
	}

	// Use new headless mode (Chrome 112+) which behaves much closer
	// to a real browser and is less likely to trigger detection.
	// The old --headless flag has many differences from headed Chrome.
	if opts.Headless {
		allocOpts = append(allocOpts, chromedp.Flag("headless", "new"))
	}

	if opts.Proxy != "" {
		allocOpts = append(allocOpts, chromedp.ProxyServer(opts.Proxy))
	}

	// opts.DisableDownloads needs no extra flag here: the main
	// disable-features set above already includes the DownloadBubble
	// flags, and appending a lone flag would override that larger set.

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

	initialEscalator := antibot.NewEscalator(antibot.DefaultEscalatorConfig())

	if tlsFlags := initialEscalator.TLSFlags(); len(tlsFlags) > 0 {
		for _, f := range tlsFlags {
			allocOpts = append(allocOpts, chromedp.Flag(f.Name, f.Value))
		}
	}

	if opts.ChromeBin != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(opts.ChromeBin))
	}

	if opts.ProfileDir != "" {
		allocOpts = append(allocOpts, chromedp.UserDataDir(opts.ProfileDir))
	}

	lifeCtx, lifeCancel := context.WithCancel(ctx)
	allocCtx, allocCl := chromedp.NewExecAllocator(lifeCtx, allocOpts...)

	escalator := antibot.NewEscalator(antibot.DefaultEscalatorConfig())
	// Geo↔proxy coherence (see rodbackend.Pool.New): the persona's
	// timezone/languages must match the exit IP's geography.
	geoCode := stealth.ResolveGeoCode(opts.GeoRegion, opts.Proxy)
	// Session-stable fingerprint: one persona per browser lifetime.
	stealthFP := stealth.GenerateFingerprint(
		rand.New(rand.NewSource(time.Now().UnixNano())),
		stealth.GeoProfileByCode(geoCode))

	p := &Pool{
		opts:              opts,
		allocCtx:          allocCtx,
		allocCl:           allocCl,
		lifeCancel:        lifeCancel,
		disp:              renderkit.NewDispatcher(opts.Workers),
		behaviorSimulator: behavior.New(behavior.DefaultConfig()),
		escalator:         escalator,
		stealthFP:         stealthFP,
	}
	p.currentUA = escalator.GetRandomDesktopUA()

	for i := 0; i < opts.Workers; i++ {
		wCtx, wCancel := chromedp.NewContext(allocCtx)
		if opts.Stealth {
			stealth.Inject(wCtx, p.stealthScript())
		}
		p.workers = append(p.workers, &worker{idx: i, ctx: wCtx, cancel: wCancel})
	}
	p.disp.Start(opts.Workers, func(idx int, job *renderkit.RenderJob) {
		p.renderJob(p.workers[idx], job)
	})

	// Bind the pool lifetime to ctx: cancellation (or Close, which
	// cancels lifeCtx) drains the pool. Close's Drain guard makes the
	// watcher's re-entrant call after an explicit Close a no-op.
	go func() {
		<-lifeCtx.Done()
		p.Close()
	}()

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
	// Browser contexts rotate within the Chrome family only: a Firefox
	// or Safari persona on a Chromium TLS/JS engine is an instant
	// contradiction (JA3 mismatch + missing Gecko/WebKit JS quirks).
	p.currentUA = p.escalator.RotateChromeUA()
}

// Screenshot navigates to url in a fresh tab and captures a real pixel
// screenshot as PNG, written to outputPath. It injects the same headers,
// stealth and settle-wait as Render so the captured page matches what a
// real browsing session would present.
func (p *Pool) Screenshot(
	ctx context.Context, url string, outputPath string,
) (string, error) {
	p.mu.Lock()
	if p.disp.Closed() {
		p.mu.Unlock()
		return "", fmt.Errorf("pool closed")
	}
	p.mu.Unlock()

	tabCtx, tabCancel := chromedp.NewContext(p.allocCtx)
	defer tabCancel()

	tabCtx, timeoutCancel := context.WithTimeout(tabCtx, p.opts.RenderTimeout)
	defer timeoutCancel()

	// Propagate cancellation from the caller's context.
	go func() {
		select {
		case <-ctx.Done():
			timeoutCancel()
			tabCancel()
		case <-tabCtx.Done():
		}
	}()

	// Inject stealth once per fresh tab.
	if p.opts.Stealth {
		_ = stealth.Inject(tabCtx, p.stealthScript())
	}

	ua := p.getCurrentUA()

	// Capture the real pixel viewport as PNG via Page.captureScreenshot.
	var png []byte
	err := chromedp.Run(tabCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			headers := network.Headers{
				"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
				"Accept-Language":           "en-US,en;q=0.9",
				"Sec-Ch-Ua":                 ua.SecChUa,
				"Sec-Ch-Ua-Mobile":          ua.SecChUaMobile,
				"Sec-Ch-Ua-Platform":        ua.SecChUaPlatform,
				"Upgrade-Insecure-Requests": "1",
				"User-Agent":                ua.UserAgent,
			}
			return network.SetExtraHTTPHeaders(headers).Do(ctx)
		}),
		chromedp.Navigate(url),
		chromedp.WaitReady("body", chromedp.ByQuery),
	)
	if err != nil {
		return "", fmt.Errorf("screenshot navigate: %w", err)
	}

	// Let the page reach network-idle before capturing.
	_ = settle.Wait(tabCtx, p.opts.Settle)

	if err := chromedp.Run(tabCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			var cerr error
			png, cerr = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatPng).Do(ctx)
			return cerr
		}),
	); err != nil {
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
	_, cancel := context.WithTimeout(job.Ctx, p.opts.RenderTimeout)
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
				"Sec-Ch-Ua":                 ua.SecChUa,
				"Sec-Ch-Ua-Mobile":          ua.SecChUaMobile,
				"Sec-Ch-Ua-Platform":        ua.SecChUaPlatform,
				"Upgrade-Insecure-Requests": "1",
				"User-Agent":                ua.UserAgent,
			}
			if job.Referer != "" {
				headers["Referer"] = job.Referer
			}
			return network.SetExtraHTTPHeaders(headers).Do(ctx)
		}),
		chromedp.Navigate(job.URL),
	); err != nil {
		job.ResultCh <- renderkit.RenderResultOrErr{Err: fmt.Errorf("navigate: %w", err)}
		return
	}

	settle.Wait(tabCtx, p.opts.Settle)

	// 如果启用了行为模拟
	if p.behaviorSimEnabled {
		// 模拟自然的滚动和鼠标移动
		var body string
		chromedp.Run(tabCtx, chromedp.Evaluate(renderkit.BehaviorSimScrollJS, &body))
		chromedp.Run(tabCtx, chromedp.Evaluate(renderkit.BehaviorSimMouseJS, &body))
	}

	if p.opts.Scroll {
		var body string
		chromedp.Run(tabCtx, chromedp.Evaluate(renderkit.ScrollJS, &body))
		settle.Wait(tabCtx, p.opts.Settle)
	}

	if err := chromedp.Run(tabCtx,
		chromedp.Title(&title),
		chromedp.OuterHTML("html", &html),
		chromedp.Evaluate(`window.location.href`, &finalURL),
		chromedp.Evaluate(`document.contentType`, &contentType),
	); err != nil {
		job.ResultCh <- renderkit.RenderResultOrErr{Err: fmt.Errorf("render: %w", err)}
		return
	}

	if !renderkit.IsHTMLContentType(contentType) {
		job.ResultCh <- renderkit.RenderResultOrErr{Err: &types.ErrNotHTML{URL: job.URL, ContentType: contentType}}
		return
	}

	// Extract all links from the rendered DOM using JavaScript.
	var extractedLinks []string
	chromedp.Run(tabCtx, chromedp.Evaluate(renderkit.CollectLinksArrayJS, &extractedLinks))

	var cfClearance string
	var cookies []*network.Cookie
	if err := chromedp.Run(tabCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cookies, err = network.GetCookies().WithURLs([]string{job.URL}).Do(ctx)
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
		finalURL = job.URL
	}

	job.ResultCh <- renderkit.RenderResultOrErr{Result: &types.RenderResult{
		HTML:                html,
		URL:                 finalURL,
		Title:               title,
		ContentType:         contentType,
		CloudflareClearance: cfClearance,
		Referer:             job.Referer,
		ExtractedLinks:      extractedLinks,
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
	return nil
}

func (p *Pool) SetBehaviorSimulation(enabled bool) {
	p.behaviorSimEnabled = enabled
}

// DownloadAsset downloads an asset using the browser's network stack.
// It uses a temporary tab to navigate to the asset URL and extracts the response body
// via CDP Network.getResponseBody.
//
// Each fallback layer uses a FRESH chromedp context derived from p.allocCtx.
// This avoids "invalid context" errors: a failed navigation in one layer
// can corrupt the tab context and break all subsequent layers.
func (p *Pool) DownloadAsset(ctx context.Context, assetURL string, referer string) (*types.AssetDownloadResult, error) {
	p.mu.Lock()
	if p.disp.Closed() {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool closed")
	}
	p.mu.Unlock()

	// Create a temp directory for downloads — needed because Chrome's CDP
	// requires a download path when navigation triggers a binary download
	// (which happens for images, PDFs, etc.)
	downloadDir, _ := os.MkdirTemp("", "wukong-downloads-*")
	if downloadDir != "" {
		defer os.RemoveAll(downloadDir)
	}

	ua := p.getCurrentUA()

	// Helper: create a fresh chromedp tab context for each attempt.
	// Returns the cancel + cancel funcs.
	//
	// Using a fresh context per attempt is critical: failures in
	// chromedp.Run (e.g. ERR_BLOCKED_BY_CLIENT, failed navigations,
	// invalid context) would otherwise corrupt the tab and break
	// every subsequent call on a shared tabCtx.
	newTabCtx := func() (context.Context, context.CancelFunc, context.CancelFunc) {
		tc, tcCancel := chromedp.NewContext(p.allocCtx)
		tc, timeoutCancel := context.WithTimeout(tc, p.opts.RenderTimeout)

		// Propagate cancellation from the caller's context.
		go func() {
			select {
			case <-ctx.Done():
				timeoutCancel()
				tcCancel()
			case <-tc.Done():
			}
		}()

		// Inject stealth once per fresh tab.
		if p.opts.Stealth {
			stealth.Inject(tc, p.stealthScript())
		}

		return tc, tcCancel, timeoutCancel
	}

	var requestID network.RequestID
	var contentType string
	var statusCode int
	var bodyBytes []byte
	var navErr error

	// =============================================================
	// Layer 1: direct asset navigation with download behavior.
	// =============================================================
	{
		tabCtx, tabCancel, tabTimeoutCancel := newTabCtx()
		defer tabCancel()
		defer tabTimeoutCancel()

		// Warm-up: navigate to origin first for realistic browsing context.
		if parsedURL, err := url.Parse(assetURL); err == nil {
			origin := parsedURL.Scheme + "://" + parsedURL.Host + "/"
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
			chromedp.ActionFunc(func(ctx context.Context) error {
				return browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).
					WithDownloadPath(downloadDir).
					Do(ctx)
			}),
			chromedp.ActionFunc(func(ctx context.Context) error {
				headers := network.Headers{
					"Accept":             "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8",
					"Accept-Language":    "en-US,en;q=0.9",
					"Sec-Ch-Ua":          ua.SecChUa,
					"Sec-Ch-Ua-Mobile":   ua.SecChUaMobile,
					"Sec-Ch-Ua-Platform": ua.SecChUaPlatform,
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

		if navErr == nil && requestID != "" {
			_ = chromedp.Run(tabCtx,
				chromedp.ActionFunc(func(ctx context.Context) error {
					var err error
					bodyBytes, err = network.GetResponseBody(requestID).Do(ctx)
					return err
				}),
			)
		}
	}

	// Layer 1.5: check temp dir for binary downloads
	if len(bodyBytes) == 0 && downloadDir != "" {
		if files, readErr := os.ReadDir(downloadDir); readErr == nil {
			for _, f := range files {
				if !f.IsDir() {
					if data, readErr := os.ReadFile(filepath.Join(downloadDir, f.Name())); readErr == nil && len(data) > 0 {
						logutil.Info("DownloadAsset layer1.5: found downloaded file", slog.String("asset_url", assetURL), slog.Int("bytes", len(data)))
						bodyBytes = data
						break
					}
				}
			}
		}
	}

	// =============================================================
	// Layer 2: load via <img> tag in a clean page context.
	// =============================================================
	if len(bodyBytes) == 0 {
		tabCtx, tabCancel, tabTimeoutCancel := newTabCtx()
		defer tabCancel()
		defer tabTimeoutCancel()

		logutil.Info("DownloadAsset layer2 img tag: trying", slog.String("asset_url", assetURL))
		var imgRequestID network.RequestID
		var imgContentType string
		var imgStatusCode int

		err := chromedp.Run(tabCtx,
			chromedp.Navigate("about:blank"),
			chromedp.WaitReady("body", chromedp.ByQuery),
		)
		if err == nil {
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
				chromedp.ActionFunc(func(ctx context.Context) error {
					deadline := time.Now().Add(15 * time.Second)
					ticker := time.NewTicker(100 * time.Millisecond)
					defer ticker.Stop()
					for time.Now().Before(deadline) {
						var loaded interface{}
						_ = chromedp.Evaluate(`window.__imgLoaded`, &loaded).Do(ctx)
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
				logutil.Error("DownloadAsset layer2 img tag: load image failed", slog.String("asset_url", assetURL), slog.Any("error", err))
			} else if imgRequestID == "" {
				logutil.Warn("DownloadAsset layer2 img tag: no request ID captured", slog.String("asset_url", assetURL))
			}
			if err == nil && imgRequestID != "" {
				var respBody []byte
				respBody, err = network.GetResponseBody(imgRequestID).Do(tabCtx)
				if err != nil {
					logutil.Error("DownloadAsset layer2 img tag: get response body failed", slog.String("asset_url", assetURL), slog.Any("error", err))
				} else if len(respBody) > 0 {
					logutil.Info("DownloadAsset layer2 img tag: success", slog.String("asset_url", assetURL), slog.Int("bytes", len(respBody)))
				}
				if err == nil && len(respBody) > 0 {
					bodyBytes = respBody
					contentType = imgContentType
					statusCode = imgStatusCode
				}
			}
		} else {
			logutil.Error("DownloadAsset layer2 img tag: navigate to about:blank failed", slog.String("asset_url", assetURL), slog.Any("error", err))
		}
	}

	// =============================================================
	// Layer 3: JavaScript fetch() with base64 encoding.
	// =============================================================
	if len(bodyBytes) == 0 {
		tabCtx, tabCancel, tabTimeoutCancel := newTabCtx()
		defer tabCancel()
		defer tabTimeoutCancel()

		logutil.Info("DownloadAsset layer3 fetch: trying", slog.String("asset_url", assetURL))

		// Navigate to origin first so fetch() has proper browsing context.
		if parsedURL, err := url.Parse(assetURL); err == nil {
			origin := parsedURL.Scheme + "://" + parsedURL.Host + "/"
			_ = chromedp.Run(tabCtx, chromedp.Navigate(origin))
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
			logutil.Error("DownloadAsset layer3 fetch: evaluate failed", slog.String("asset_url", assetURL), slog.Any("error", fetchErr))
		} else if !result.OK {
			logutil.Warn("DownloadAsset layer3 fetch: fetch not OK", slog.String("asset_url", assetURL), slog.Int("status", result.Status))
		} else if result.Body == "" {
			logutil.Warn("DownloadAsset layer3 fetch: empty body", slog.String("asset_url", assetURL))
		}
		if fetchErr == nil && result.OK && result.Body != "" {
			decoded, err := base64.StdEncoding.DecodeString(result.Body)
			if err != nil {
				logutil.Error("DownloadAsset layer3 fetch: base64 decode failed", slog.String("asset_url", assetURL), slog.Any("error", err))
			} else if len(decoded) > 0 {
				logutil.Info("DownloadAsset layer3 fetch: success", slog.String("asset_url", assetURL), slog.Int("bytes", len(decoded)))
				bodyBytes = decoded
				if contentType == "" {
					contentType = result.ContentType
				}
				if statusCode == 0 {
					statusCode = result.Status
				}
			}
		}
	}

	// =============================================================
	// Layer 4: Network.loadNetworkResource CDP.
	// =============================================================
	if len(bodyBytes) == 0 {
		tabCtx, tabCancel, tabTimeoutCancel := newTabCtx()
		defer tabCancel()
		defer tabTimeoutCancel()

		logutil.Info("DownloadAsset layer4 Network.loadNetworkResource: trying", slog.String("asset_url", assetURL))
		var frameID cdp.FrameID

		err := chromedp.Run(tabCtx,
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
			logutil.Error("DownloadAsset layer4 Network.loadNetworkResource: navigate/get frame failed", slog.String("asset_url", assetURL), slog.Any("error", err))
		}
		if err == nil && frameID != "" {
			result, loadErr := network.LoadNetworkResource(assetURL, &network.LoadNetworkResourceOptions{
				DisableCache:       false,
				IncludeCredentials: true,
			}).WithFrameID(frameID).Do(tabCtx)
			if loadErr != nil {
				logutil.Error("DownloadAsset layer4 Network.loadNetworkResource: load failed", slog.String("asset_url", assetURL), slog.Any("error", loadErr))
			} else if !result.Success {
				logutil.Warn("DownloadAsset layer4 Network.loadNetworkResource: not successful", slog.String("asset_url", assetURL), slog.Float64("http_status", result.HTTPStatusCode))
			} else if result.Stream == "" {
				logutil.Warn("DownloadAsset layer4 Network.loadNetworkResource: no stream", slog.String("asset_url", assetURL))
			}
			if loadErr == nil && result.Success && result.Stream != "" {
				var data []byte
				for {
					readData, eof, readErr := io.Read(result.Stream).Do(tabCtx)
					if readErr != nil {
						break
					}
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
				_ = io.Close(result.Stream).Do(tabCtx)
				if len(data) > 0 {
					logutil.Info("DownloadAsset layer4 Network.loadNetworkResource: success", slog.String("asset_url", assetURL), slog.Int("bytes", len(data)))
					bodyBytes = data
					if statusCode == 0 && result.HTTPStatusCode > 0 {
						statusCode = int(result.HTTPStatusCode)
					}
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
	// Drain the shared dispatcher first: close the job queue, wait for
	// in-flight renders, and reject new submits. Only the caller that
	// performed the drain runs the cleanup below — chromedp context
	// cancels are one-shot (their cancelWait consumes a semaphore
	// token), so a second invocation on the same cancel deadlocks.
	// This also makes an explicit Close and the lifecycle watcher's
	// Close mutually exclusive.
	if !p.disp.Drain() {
		return
	}

	for _, w := range p.workers {
		w.cancel()
	}
	p.allocCl()

	// Release the lifecycle goroutine. It wakes up and re-enters Close,
	// which returns immediately via the Drain guard above.
	if p.lifeCancel != nil {
		p.lifeCancel()
	}
}

func (p *Pool) CloseWithError() error {
	p.Close()
	return nil
}
