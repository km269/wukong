package renderkit

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/km269/wukong/internal/browser/types"
)

func TestDispatcher_SubmitRunsJob(t *testing.T) {
	d := NewDispatcher(2)
	d.Start(2, func(idx int, job *RenderJob) {
		job.ResultCh <- RenderResultOrErr{
			Result: fakeRender{url: job.URL}.result(),
		}
	})
	defer d.Drain()

	res, err := d.Submit(context.Background(), "https://example.com/a", "https://example.com/")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.URL != "https://example.com/a" {
		t.Errorf("result URL = %q", res.URL)
	}
}

func TestDispatcher_ErrorPropagates(t *testing.T) {
	d := NewDispatcher(1)
	d.Start(1, func(idx int, job *RenderJob) {
		job.ResultCh <- RenderResultOrErr{Err: errFake}
	})
	defer d.Drain()

	_, err := d.Submit(context.Background(), "https://example.com", "")
	if err != errFake {
		t.Fatalf("err = %v, want errFake", err)
	}
}

func TestDispatcher_CancelWhileWaiting(t *testing.T) {
	d := NewDispatcher(1)
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	d.Start(1, func(idx int, job *RenderJob) {
		startedOnce.Do(func() { close(started) })
		<-release
		job.ResultCh <- RenderResultOrErr{Result: nil}
	})
	defer func() { close(release); d.Drain() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// Hold the only worker with a first job (submitted in the
	// background; its result only lands after release), then submit a
	// second that can only wait in the queue until ctx expires.
	go func() {
		_, _ = d.Submit(context.Background(), "https://example.com/first", "")
	}()
	<-started
	if _, err := d.Submit(ctx, "https://example.com/second", ""); err == nil {
		t.Fatal("expected ctx deadline error, got nil")
	}
}

func TestDispatcher_SubmitAfterClose(t *testing.T) {
	d := NewDispatcher(1)
	d.Start(1, func(idx int, job *RenderJob) {
		job.ResultCh <- RenderResultOrErr{Result: nil}
	})
	d.Drain()

	if !d.Closed() {
		t.Fatal("Closed() = false after Drain")
	}
	if _, err := d.Submit(context.Background(), "https://example.com", ""); err == nil || err.Error() != "pool closed" {
		t.Fatalf("Submit after Drain err = %v, want pool closed", err)
	}
	// Drain is idempotent.
	d.Drain()
}

func TestDispatcher_Concurrency(t *testing.T) {
	const workers = 4
	const jobs = 50

	d := NewDispatcher(workers)
	var mu sync.Mutex
	seen := make(map[string]bool)
	d.Start(workers, func(idx int, job *RenderJob) {
		mu.Lock()
		seen[job.URL] = true
		mu.Unlock()
		job.ResultCh <- RenderResultOrErr{Result: nil}
	})

	var wg sync.WaitGroup
	var okCount atomic.Int32
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			url := "https://example.com/" + string(rune('a'+i%26)) + string(rune('0'+i/26))
			if _, err := d.Submit(context.Background(), url, ""); err == nil {
				okCount.Add(1)
			}
		}(i)
	}
	wg.Wait()
	d.Drain()

	if got := int(okCount.Load()); got != jobs {
		t.Fatalf("ok submits = %d, want %d", got, jobs)
	}
	if len(seen) != jobs {
		t.Fatalf("distinct jobs seen = %d, want %d", len(seen), jobs)
	}
}

// gatedDispatcher returns a 1-worker dispatcher whose worker records
// the run order of every job and blocks on the job named "/first"
// until release is closed. waitStarted guarantees /first is the one
// holding the worker, and waitQueued(n) waits until n jobs have
// landed in the queue — together they make submission order
// deterministic despite the asynchronous submitter goroutines.
func gatedDispatcher(t *testing.T, aging time.Duration) (submit func(url string, prio types.Priority), waitStarted func(), waitQueued func(n int), release func(), order func() []string) {
	t.Helper()
	d := NewDispatcher(1)
	if aging > 0 {
		d.agingEvery = aging
	}
	started := make(chan struct{})
	releaseCh := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var runOrder []string
	d.Start(1, func(idx int, job *RenderJob) {
		mu.Lock()
		runOrder = append(runOrder, job.URL)
		mu.Unlock()
		if job.URL == "https://example.com/first" {
			once.Do(func() { close(started) })
			<-releaseCh
		}
		job.ResultCh <- RenderResultOrErr{Result: nil}
	})

	var wg sync.WaitGroup
	submit = func(url string, prio types.Priority) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = d.SubmitWithPriority(context.Background(), url, "", prio)
		}()
	}
	waitQueued = func(n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if d.queuedLen() == n {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		t.Fatalf("queued jobs never reached %d (have %d)", n, d.queuedLen())
	}
	return submit, func() { <-started }, waitQueued, func() {
			close(releaseCh)
			wg.Wait()
			d.Drain()
		}, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return runOrder
		}
}

func TestDispatcher_PriorityJumpsQueue(t *testing.T) {
	submit, waitStarted, waitQueued, release, order := gatedDispatcher(t, 0) // default aging: 5s, far beyond test time

	submit("https://example.com/first", types.PriorityNormal)
	waitStarted()
	submit("https://example.com/n1", types.PriorityNormal)
	waitQueued(1)
	submit("https://example.com/n2", types.PriorityNormal)
	waitQueued(2)
	submit("https://example.com/high", types.PriorityHigh)
	waitQueued(3)
	release()

	want := []string{
		"https://example.com/first",
		"https://example.com/high", // jumps both normals
		"https://example.com/n1",   // FIFO within equal priority
		"https://example.com/n2",
	}
	if got := order(); !reflect.DeepEqual(got, want) {
		t.Fatalf("run order = %v, want %v", got, want)
	}
}

func TestDispatcher_AgingPromotesStarvedLowJob(t *testing.T) {
	submit, waitStarted, waitQueued, release, order := gatedDispatcher(t, 10*time.Millisecond)

	submit("https://example.com/first", types.PriorityNormal)
	waitStarted()
	// The low job queues and ages; without aging the fresh normal job
	// submitted later would overtake it.
	submit("https://example.com/low", types.PriorityLow)
	waitQueued(1)
	time.Sleep(80 * time.Millisecond) // low gains several aging bumps
	submit("https://example.com/normal", types.PriorityNormal)
	waitQueued(2)
	release()

	want := []string{
		"https://example.com/first",
		"https://example.com/low", // aged past the fresh normal job
		"https://example.com/normal",
	}
	if got := order(); !reflect.DeepEqual(got, want) {
		t.Fatalf("run order = %v, want %v", got, want)
	}
}

func TestDispatcher_BackpressureBlocksAndCtxCancels(t *testing.T) {
	// workers=1 -> admission capacity 4: one job holds the worker,
	// four queued jobs consume every token, and a further submit can
	// only block on admission — until its ctx expires.
	d := NewDispatcher(1)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	d.Start(1, func(idx int, job *RenderJob) {
		once.Do(func() { close(started) })
		<-release
		job.ResultCh <- RenderResultOrErr{Result: nil}
	})
	var closeRelease sync.Once
	t.Cleanup(func() {
		closeRelease.Do(func() { close(release) })
		d.Drain()
	})

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ { // 1 running + 4 queued = capacity
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = d.Submit(context.Background(), "https://example.com/q", "")
		}()
	}
	<-started
	// Wait until the queue is full: the worker released one token by
	// popping its job, which the fifth submitter then took.
	deadline := time.Now().Add(2 * time.Second)
	for d.semTokens() < 4 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := d.semTokens(); got != 4 {
		t.Fatalf("admission tokens taken = %d, want 4", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := d.Submit(ctx, "https://example.com/extra", ""); err == nil {
		t.Fatal("expected ctx error while blocked on full queue, got nil")
	}

	closeRelease.Do(func() { close(release) })
	wg.Wait()
}
