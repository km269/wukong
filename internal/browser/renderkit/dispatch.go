package renderkit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/km269/wukong/internal/browser/types"
)

// RenderJob is a queued render request, backend-agnostic. The
// scheduling (queueing, worker dispatch, cancellation, close) lives in
// Dispatcher; the actual page interaction stays in each backend's
// render implementation.
type RenderJob struct {
	URL      string
	Referer  string
	ResultCh chan<- RenderResultOrErr
	Ctx      context.Context
	// Priority is the submitter's scheduling hint (see
	// types.PriorityRenderer). It does not change how the job runs,
	// only when a worker picks it up.
	Priority types.Priority
}

// RenderResultOrErr carries a render outcome back to the submitter.
type RenderResultOrErr struct {
	Result *types.RenderResult
	Err    error
}

// defaultAgingEvery controls dynamic priority promotion: for every
// full interval a job waits in the queue its effective priority is
// bumped one level, so a starved low-priority job eventually overtakes
// fresh normal work. This is the anti-starvation half of the
// priority scheduling strategy — strict priority alone would let a
// steady stream of high-priority renders delay background work
// indefinitely.
const defaultAgingEvery = 5 * time.Second

// queuedJob is a submitted job plus its scheduling metadata.
type queuedJob struct {
	job  *RenderJob
	prio types.Priority
	seq  int64     // FIFO tie-break within equal effective priority
	enq  time.Time // aging base
}

// Dispatcher is the shared render-job scheduling skeleton used by both
// the chromedp and rod backends: a bounded admission queue, a closed
// guard, and a worker pool whose loop delegates to a backend-provided
// runner. Jobs are dequeued by effective priority (declared priority
// plus aging promotion), FIFO among equals. Backends keep their own
// worker state (browser tabs, pages) and map the worker index passed
// to Start to it.
type Dispatcher struct {
	mu       sync.Mutex
	notEmpty *sync.Cond
	jobs     []*queuedJob
	seq      int64
	// sem bounds the queue (4x workers, matching both backends'
	// previous behaviour): a submit takes a token before queueing and
	// blocks while the queue is full. A channel (not a counter) so the
	// block stays ctx-selectable.
	sem chan struct{}
	wg  sync.WaitGroup

	agingEvery time.Duration
	closed     bool
}

// NewDispatcher creates a dispatcher with an admission capacity of
// 4x the worker count. workers <= 0 falls back to 4.
func NewDispatcher(workers int) *Dispatcher {
	if workers <= 0 {
		workers = 4
	}
	d := &Dispatcher{
		sem:        make(chan struct{}, workers*4),
		agingEvery: defaultAgingEvery,
	}
	d.notEmpty = sync.NewCond(&d.mu)
	return d
}

// Start launches the worker goroutines. Each worker calls run with its
// index for every job it takes off the queue; run blocks until the job
// resolves on job.ResultCh. Workers exit once the dispatcher is drained
// and every queued job has been run.
//
// When a global budget is installed (SetGlobalBudget/EnsureGlobalBudget),
// every worker takes a process-wide render slot around run: the total
// number of concurrently running renders across all pools in the
// process is capped, and resource watermarks can throttle that cap at
// runtime. Without a budget the loop is unchanged.
func (d *Dispatcher) Start(workers int, run func(workerIdx int, job *RenderJob)) {
	if workers <= 0 {
		workers = 4
	}
	for i := 0; i < workers; i++ {
		d.wg.Add(1)
		go func(idx int) {
			defer d.wg.Done()
			for {
				job := d.next()
				if job == nil {
					return
				}
				b := Budget()
				if b != nil {
					if err := b.Acquire(job.Ctx); err != nil {
						// The job's ctx died while waiting for a
						// global slot; its submitter has already
						// returned via ctx. Skip the run.
						continue
					}
				}
				run(idx, job)
				if b != nil {
					b.Release()
				}
			}
		}(i)
	}
}

// effPrio returns the job's effective priority at time now: the
// declared priority plus one level per full aging interval waited.
func (d *Dispatcher) effPrio(q *queuedJob, now time.Time) int {
	p := int(q.prio.Normalize())
	if d.agingEvery > 0 {
		if waited := now.Sub(q.enq); waited >= d.agingEvery {
			p += int(waited / d.agingEvery)
		}
	}
	return p
}

// next hands the worker the job with the highest effective priority
// (FIFO among equals). The queue is bounded at 4x workers, so the
// linear scan is cheap and — unlike a heap whose keys mutate as jobs
// age — always consistent. It blocks while the queue is empty and the
// dispatcher is open, and returns nil once the dispatcher is drained
// and empty; queued jobs still run to completion during a drain,
// matching the previous channel semantics.
func (d *Dispatcher) next() *RenderJob {
	d.mu.Lock()
	for len(d.jobs) == 0 && !d.closed {
		d.notEmpty.Wait()
	}
	if len(d.jobs) == 0 {
		d.mu.Unlock()
		return nil
	}
	now := time.Now()
	best := 0
	bestEff := d.effPrio(d.jobs[0], now)
	for i := 1; i < len(d.jobs); i++ {
		if e := d.effPrio(d.jobs[i], now); e > bestEff {
			best, bestEff = i, e
		}
	}
	q := d.jobs[best]
	d.jobs = append(d.jobs[:best], d.jobs[best+1:]...)
	d.mu.Unlock()
	<-d.sem // release the submitter's admission token
	return q.job
}

// Submit enqueues a render job at normal priority and waits for its
// result. Returns an error if the dispatcher is closed or ctx is
// cancelled — either while queueing (job never runs) or while waiting
// (job may still run; its result is dropped, matching both backends'
// previous behaviour).
func (d *Dispatcher) Submit(ctx context.Context, url, referer string) (*types.RenderResult, error) {
	return d.SubmitWithPriority(ctx, url, referer, types.PriorityNormal)
}

// SubmitWithPriority is Submit with a scheduling hint: when workers
// are contended, higher-priority jobs are dequeued first, and jobs
// that waited a full aging interval are promoted one level (see
// defaultAgingEvery).
func (d *Dispatcher) SubmitWithPriority(ctx context.Context, url, referer string, prio types.Priority) (*types.RenderResult, error) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, fmt.Errorf("pool closed")
	}
	d.mu.Unlock()

	// Admission: take a queue token; blocks while the queue is full,
	// selectable against ctx (the previous buffered-channel backpressure).
	select {
	case d.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	resultCh := make(chan RenderResultOrErr, 1)
	d.mu.Lock()
	if d.closed { // drained while waiting for a token
		d.mu.Unlock()
		<-d.sem
		return nil, fmt.Errorf("pool closed")
	}
	d.seq++
	d.jobs = append(d.jobs, &queuedJob{
		job: &RenderJob{
			URL:      url,
			Referer:  referer,
			ResultCh: resultCh,
			Ctx:      ctx,
			Priority: prio,
		},
		prio: prio,
		seq:  d.seq,
		enq:  time.Now(),
	})
	d.mu.Unlock()
	d.notEmpty.Signal()

	select {
	case res := <-resultCh:
		return res.Result, res.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Drain stops admitting new jobs and waits for queued and in-flight
// jobs to finish. Idempotent: it reports whether this call performed
// the drain, so callers with non-idempotent cleanup (e.g. rod's
// browser.MustClose) only run it once — the caller that drained is
// the one whose workers have all exited when Drain returns. Backends
// call it first in Close() before releasing browser resources, so no
// job touches a dead browser.
func (d *Dispatcher) Drain() bool {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return false
	}
	d.closed = true
	d.mu.Unlock()

	d.notEmpty.Broadcast()
	d.wg.Wait()
	return true
}

// Closed reports whether Drain has been called. Backends use it to
// reject new non-render work (e.g. DownloadAsset) after close.
func (d *Dispatcher) Closed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

// semTokens reports how many admission tokens are occupied — a test
// helper for observing backpressure.
func (d *Dispatcher) semTokens() int { return len(d.sem) }

// queuedLen reports the number of jobs waiting to be dequeued — a
// test helper for sequencing submissions deterministically.
func (d *Dispatcher) queuedLen() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.jobs)
}
