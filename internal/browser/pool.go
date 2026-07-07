package browser

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
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
		chromedp.Flag("headless", opts.Headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("mute-audio", true),
	)

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
