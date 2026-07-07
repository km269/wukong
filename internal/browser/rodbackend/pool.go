package rodbackend

import (
	"context"
	"fmt"
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
		page = p.browser.MustPage("")
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

	go func() {
		<-ctx.Done()
		page.StopLoading()
	}()

	ua := p.getCurrentUA()

	headers := proto.NetworkHeaders{
		"Accept":                    gson.New("text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"),
		"Accept-Language":           gson.New("en-US,en;q=0.9"),
		"Accept-Encoding":           gson.New("gzip, deflate, br"),
		"Connection":                gson.New("keep-alive"),
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
	if job.referer != "" {
		headers["Referer"] = gson.New(job.referer)
		headers["Sec-Fetch-Site"] = gson.New("same-origin")
	}

	proto.NetworkSetExtraHTTPHeaders{
		Headers: headers,
	}.Call(page)

	if err := page.Navigate(job.url); err != nil {
		job.resultCh <- renderResultOrErr{Err: fmt.Errorf("navigate: %w", err)}
		return
	}

	page.MustWaitLoad()

	if p.opts.Settle > 0 {
		page.WaitRequestIdle(p.opts.Settle, nil, nil, nil)
	}

	// 如果启用了行为模拟
	if p.behaviorSimEnabled {
		// 模拟自然的滚动和鼠标移动
		page.MustEval(`
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
		page.MustEval(`
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
		page.MustEval(`
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

	finalURL = page.MustEval(`() => window.location.href`).String()

	contentType = page.MustEval(`() => document.contentType`).String()

	if contentType != "" && !isHTMLContentType(contentType) {
		job.resultCh <- renderResultOrErr{Err: &types.ErrNotHTML{URL: job.url, ContentType: contentType}}
		return
	}

	html, err := page.HTML()
	if err != nil {
		job.resultCh <- renderResultOrErr{Err: fmt.Errorf("get HTML: %w", err)}
		return
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

	job.resultCh <- renderResultOrErr{Result: &types.RenderResult{
		HTML:                html,
		URL:                 finalURL,
		Title:               title,
		ContentType:         contentType,
		CloudflareClearance: cfClearance,
		Referer:             job.referer,
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
			page := p.browser.MustPage("")
			// 注入我们增强的 stealth 脚本
			_, err := proto.PageAddScriptToEvaluateOnNewDocument{
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
