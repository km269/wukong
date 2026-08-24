package clone

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskGraph_TopologicalOrder(t *testing.T) {
	g := newTaskGraph()
	var mu sync.Mutex
	order := make([]string, 0, 3)
	record := func(name string) func(depErr error) error {
		return func(depErr error) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil
		}
	}

	// C depends on B, B on A: C's fn must not run before B's.
	done := make(chan struct{})
	if err := g.Submit("a", record("a")); err != nil {
		t.Fatal(err)
	}
	if err := g.Submit("b", record("b"), "a"); err != nil {
		t.Fatal(err)
	}
	if err := g.Submit("c", func(depErr error) error {
		record("c")(depErr)
		close(done)
		return nil
	}, "b"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("chain never ran")
	}
	want := []string{"a", "b", "c"}
	mu.Lock()
	defer mu.Unlock()
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want prefix %v", order, want)
		}
	}
}

func TestTaskGraph_WaitsForDeclaredDep(t *testing.T) {
	g := newTaskGraph()
	ran := make(chan struct{})
	if err := g.Declare("backoff"); err != nil {
		t.Fatal(err)
	}
	if err := g.Submit("job", func(depErr error) error {
		close(ran)
		return nil
	}, "backoff"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ran:
		t.Fatal("job ran before backoff resolved")
	case <-time.After(50 * time.Millisecond):
	}

	if err := g.Resolve("backoff", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("job never ran after resolve")
	}
}

func TestTaskGraph_FailurePropagatesToDependents(t *testing.T) {
	g := newTaskGraph()
	gotErr := make(chan error, 1)

	if err := g.Submit("a", func(depErr error) error {
		return errors.New("boom")
	}); err != nil {
		t.Fatal(err)
	}
	// b depends on a: fn still runs, but with a's error as depErr.
	if err := g.Submit("b", func(depErr error) error {
		gotErr <- depErr
		return depErr
	}, "a"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-gotErr:
		if err == nil || err.Error() != "boom" {
			t.Fatalf("depErr = %v, want boom", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("b never ran")
	}
}

func TestTaskGraph_CancelledBackoffRunsCleanup(t *testing.T) {
	// The cloner's retry shape: a backoff node resolved with a ctx
	// error must still run the retry fn so it can release its wg
	// count instead of leaking it.
	g := newTaskGraph()
	cleaned := make(chan struct{})
	ctxErr := errors.New("context canceled")

	if err := g.Declare("backoff:1"); err != nil {
		t.Fatal(err)
	}
	if err := g.Submit("retry:1", func(depErr error) error {
		if depErr != nil {
			close(cleaned)
		}
		return depErr
	}, "backoff:1"); err != nil {
		t.Fatal(err)
	}
	if err := g.Resolve("backoff:1", ctxErr); err != nil {
		t.Fatal(err)
	}

	select {
	case <-cleaned:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup never ran on failed dep")
	}
}

func TestTaskGraph_RejectsDuplicatesAndCycles(t *testing.T) {
	g := newTaskGraph()
	if err := g.Declare("a"); err != nil {
		t.Fatal(err)
	}
	if err := g.Declare("a"); err == nil {
		t.Fatal("duplicate Declare accepted")
	}
	if err := g.Submit("a", func(error) error { return nil }); err == nil {
		t.Fatal("duplicate Submit accepted")
	}

	// a -> b -> a cycle (a unresolved again via a fresh graph).
	g2 := newTaskGraph()
	if err := g2.Declare("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := g2.Declare("b", "a"); err == nil {
		t.Fatal("cycle accepted")
	}
	// Self-dependency.
	if err := g2.Declare("c", "c"); err == nil {
		t.Fatal("self-cycle accepted")
	}
	// Resolved nodes are terminal: no cycle through them.
	if err := g2.Resolve("a", nil); err != nil {
		t.Fatal(err)
	}
	if err := g2.Declare("d", "a"); err != nil {
		t.Fatalf("dep on resolved node rejected: %v", err)
	}
}

func TestTaskGraph_PreResolvedDepRunsImmediately(t *testing.T) {
	g := newTaskGraph()
	if err := g.Declare("parent"); err != nil {
		t.Fatal(err)
	}
	if err := g.Resolve("parent", nil); err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{})
	if err := g.Submit("child", func(depErr error) error {
		close(ran)
		return nil
	}, "parent"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("child never ran despite resolved dep")
	}
}

func TestTaskGraph_ConcurrentResolveAndSubmit(t *testing.T) {
	g := newTaskGraph()
	const n = 50
	var ran atomic.Int32
	var wg sync.WaitGroup

	// n independent backoff/job pairs, resolved and submitted from
	// concurrent goroutines.
	for i := 0; i < n; i++ {
		backoff := "backoff:" + strconv.Itoa(i)
		job := "job:" + strconv.Itoa(i)
		if err := g.Declare(backoff); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.Submit(job, func(depErr error) error {
				ran.Add(1)
				return nil
			}, backoff); err != nil {
				t.Errorf("submit: %v", err)
			}
		}()
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := g.Resolve("backoff:"+strconv.Itoa(i), nil); err != nil {
				t.Errorf("resolve: %v", err)
			}
		}(i)
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if int(ran.Load()) == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("jobs run = %d, want %d", ran.Load(), n)
}
