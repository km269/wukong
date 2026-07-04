package browser

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/km269/wukong/internal/browser/settle"
	"github.com/km269/wukong/internal/browser/stealth"
	"github.com/km269/wukong/internal/browser/types"
	"github.com/km269/wukong/internal/config"
)

type Options struct {
	Headless      bool
	Workers       int
	Settle        time.Duration
	RenderTimeout time.Duration
	Scroll        bool
	ChromeBin     string
	ControlURL    string
	Stealth       bool
}

type Pool struct {
	opts     Options
	allocCtx context.Context
	allocCl  context.CancelFunc
	workers  []*worker
	queue    chan *renderJob
	wg       sync.WaitGroup
	closed   bool
	mu       sync.Mutex
}

type worker struct {
	idx    int
	ctx    context.Context
	cancel context.CancelFunc
}

type renderJob struct {
	url      string
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

	allocCtx, allocCl := chromedp.NewExecAllocator(context.Background(), allocOpts...)

	p := &Pool{
		opts:     opts,
		allocCtx: allocCtx,
		allocCl:  allocCl,
		queue:    make(chan *renderJob, opts.Workers*4),
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

	if err := chromedp.Run(tabCtx, chromedp.Navigate(job.url)); err != nil {
		job.resultCh <- renderResultOrErr{Err: fmt.Errorf("navigate: %w", err)}
		return
	}

	settle.Wait(tabCtx, p.opts.Settle)

	if p.opts.Scroll {
		var body string
		chromedp.Run(tabCtx,
			chromedp.Evaluate(`
				(async () => {
					for (let i = 0; i < 5; i++) {
						window.scrollBy(0, window.innerHeight);
						await new Promise(r => setTimeout(r, 500));
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

	if finalURL == "" {
		finalURL = job.url
	}

	job.resultCh <- renderResultOrErr{Result: &types.RenderResult{
		HTML:                html,
		URL:                 finalURL,
		Title:               title,
		ContentType:         contentType,
		CloudflareClearance: "",
	}}
}

func (p *Pool) Render(ctx context.Context, url string) (*types.RenderResult, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("pool closed")
	}
	p.mu.Unlock()

	resultCh := make(chan renderResultOrErr, 1)
	select {
	case p.queue <- &renderJob{url: url, resultCh: resultCh, ctx: ctx}:
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
		Headless:      cfg.Headless,
		Workers:       workers,
		Settle:        settleTimeout,
		RenderTimeout: cfg.Timeout,
		Scroll:        cfg.Scroll,
		ChromeBin:     cfg.BrowserPath,
		ControlURL:    cfg.ControlURL,
		Stealth:       cfg.Stealth,
	})
}
