package rodbackend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSmokeRealChrome launches a real headless Chrome via the rod pool
// and renders a local page — verifies the rod backend works on this
// machine (precondition for P2-1 phase 2 unification).
// Skipped in -short mode so normal test runs stay hermetic.
func TestSmokeRealChrome(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><html><head><title>smoke</title></head>
<body><a href="/page2">link</a><p>hello smoke</p></body></html>`))
	}))
	defer srv.Close()

	// Bind the pool lifetime to the test context: cancelling ctx would
	// drain the pool and close the rod browser automatically; defer
	// Close is the explicit path here.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := New(ctx, Options{
		ChromeBin:     `C:\Program Files\Google\Chrome\Application\chrome.exe`,
		Headless:      true,
		Workers:       1,
		Settle:        300 * time.Millisecond,
		RenderTimeout: 30 * time.Second,
		Stealth:       true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer pool.Close()

	renderCtx, renderCancel := context.WithTimeout(ctx, 60*time.Second)
	defer renderCancel()

	res, err := pool.Render(renderCtx, srv.URL)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if res == nil || len(res.HTML) == 0 {
		t.Fatalf("empty render result")
	}
	t.Logf("rendered %d bytes, %d links", len(res.HTML), len(res.ExtractedLinks))
	found := false
	for _, l := range res.ExtractedLinks {
		if l == srv.URL+"/page2" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected link %s/page2 in %v", srv.URL, res.ExtractedLinks)
	}
}
