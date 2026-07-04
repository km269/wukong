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
	"github.com/km269/wukong/internal/browser/stealth"
	"github.com/km269/wukong/internal/browser/types"
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
	ProfileDir    string
}

type Pool struct {
	opts    Options
	browser *rod.Browser
	workers []*worker
	queue   chan *renderJob
	wg      sync.WaitGroup
	closed  bool
	mu      sync.Mutex
}

type worker struct {
	idx  int
	page *rod.Page
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
		l = l.NoSandbox(true).
			Devtools(false)
		controlURL = l.MustLaunch()
	}

	browserInstance := rod.New().ControlURL(controlURL).MustConnect()

	p := &Pool{
		opts:    opts,
		browser: browserInstance,
		queue:   make(chan *renderJob, opts.Workers*4),
	}

	for i := 0; i < opts.Workers; i++ {
		p.workers = append(p.workers, &worker{idx: i})
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
	ctx, cancel := context.WithTimeout(job.ctx, p.opts.RenderTimeout)
	defer cancel()

	var page *rod.Page
	if w.page != nil {
		page = w.page.Context(ctx)
	}

	if page == nil {
		page = p.browser.MustPage("")
		if p.opts.Stealth {
			page.MustEvalOnNewDocument(stealth.Script)
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

	if err := page.Navigate(job.url); err != nil {
		job.resultCh <- renderResultOrErr{Err: fmt.Errorf("navigate: %w", err)}
		return
	}

	page.MustWaitLoad()

	if p.opts.Settle > 0 {
		page.WaitRequestIdle(p.opts.Settle, nil, nil, nil)
	}

	if p.opts.Scroll {
		page.MustEval(`
			(async () => {
				for (let i = 0; i < 5; i++) {
					window.scrollBy(0, window.innerHeight);
					await new Promise(r => setTimeout(r, 500));
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
	for _, w := range p.workers {
		if w.page != nil {
			w.page.MustEvalOnNewDocument(stealth.Script)
		}
	}
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
