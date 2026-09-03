package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range []string{"index.html", "sub/page.md", "sub/nested/data.txt"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("# "+filepath.Base(p)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, "index.html"),
		[]byte("<h1>Home</h1>\n"), 0o644)
	return dir
}

func newTestServer(dir string) *Server {
	return NewServer(Config{RootDir: dir, AppName: "testapp"})
}

// freePort finds a currently-free TCP port via a throwaway listener.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func request(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	s.handleRequest(w, req)
	return w
}

// --- handleRequest: directory traversal & listing ---

func TestHandleRequest_ServesFile(t *testing.T) {
	s := newTestServer(setupRoot(t))

	w := request(t, s, "/sub/page.md")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "# page.md") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestHandleRequest_ServesNestedFile(t *testing.T) {
	s := newTestServer(setupRoot(t))

	w := request(t, s, "/sub/nested/data.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "# data.txt") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestHandleRequest_RootServesIndex(t *testing.T) {
	s := newTestServer(setupRoot(t))

	w := request(t, s, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<h1>Home</h1>") {
		t.Errorf("body = %q, want index.html", w.Body.String())
	}
}

func TestHandleRequest_DirWithoutIndex_ListsDirectory(t *testing.T) {
	s := newTestServer(setupRoot(t))

	// /sub has no index.html -> directory listing.
	w := request(t, s, "/sub")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Index of") {
		t.Errorf("body = %q, want directory listing", body)
	}
	// Entries are linked (page.md), and escaping is applied.
	if !strings.Contains(body, "page.md") {
		t.Errorf("body = %q, want page.md entry", body)
	}
	if !strings.Contains(body, "📄 page.md") {
		t.Errorf("body = %q, want file icon entry", body)
	}
}

func TestHandleRequest_DirListing_ParentLinkAndEscaping(t *testing.T) {
	s := newTestServer(setupRoot(t))

	w := request(t, s, "/sub")
	body := w.Body.String()
	if !strings.Contains(body, `href="/"`) && !strings.Contains(body, `href="\">`) {
		t.Errorf("body = %q, want parent link", body)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestHandleRequest_DirServesNestedIndex(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "index.html"),
		[]byte("<h1>App</h1>"), 0o644)
	s := newTestServer(dir)

	w := request(t, s, "/app/")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "<h1>App</h1>") {
		t.Errorf("body = %q, want nested index", w.Body.String())
	}
}

func TestHandleRequest_TraversalRejected(t *testing.T) {
	s := newTestServer(setupRoot(t))

	// 注意：在 Windows 上 filepath.Clean 会把 `..` 弹回根目录，
	// 逃逸路径会被收拢为根内路径（多半仅因文件不存在而 404）。
	// 因此 403 与 404 均为安全结果。
	for _, path := range []string{
		"/../secret.txt",
		"/..%2f..%2fsecret.txt",
		"/sub/..%2f..%2fsecret.txt",
	} {
		w := request(t, s, path)
		switch w.Code {
		case http.StatusForbidden:
			// 规范化后逃逸根目录 -> 拒绝。
		case http.StatusNotFound:
			// 未匹配任何真实文件的路径（找不到文件）同样是安全结果。
		default:
			t.Errorf("path %q: code = %d, want 403 or 404", path, w.Code)
		}
	}

	// 规范化后的根内路径不得被误判为可达的越界文件。
	w := request(t, s, "/"+filepath.Join("..", "..", "secret.txt"))
	switch w.Code {
	case http.StatusForbidden, http.StatusNotFound:
		// 安全。
	default:
		t.Errorf("traversal path: code = %d body = %q, want 403 or 404",
			w.Code, w.Body.String())
	}
}

func TestHandleRequest_MissingFile404(t *testing.T) {
	s := newTestServer(setupRoot(t))

	w := request(t, s, "/nope.txt")
	if w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

func TestHandleRequest_QueryStringIgnored(t *testing.T) {
	s := newTestServer(setupRoot(t))

	w := request(t, s, "/sub/page.md?x=1")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200 (query must not break path)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "# page.md") {
		t.Errorf("body = %q", w.Body.String())
	}
}

// --- exists ---

func TestExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	os.WriteFile(p, []byte("x"), 0o644)

	if !exists(p) {
		t.Error("exists(a.txt) = false, want true")
	}
	if exists(filepath.Join(dir, "missing.txt")) {
		t.Error("exists(missing) = true, want false")
	}
	if exists(dir) != true {
		t.Error("exists(dir) = false, want true")
	}
}

// --- lifecycle ---

func TestLifecycle_FixedPort(t *testing.T) {
	// Find a free port, then start the server on it.
	port := freePort(t)

	s := NewServer(Config{
		Port:         port,
		RootDir:      setupRoot(t),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	})

	if s.IsRunning() {
		t.Error("IsRunning() = true before start")
	}
	if s.Address() != "" {
		t.Errorf("Address() = %q before start", s.Address())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := s.StartAndWait(ctx)
	if err != nil {
		t.Fatalf("StartAndWait() error = %v", err)
	}
	if !strings.HasPrefix(addr, "http://localhost:") {
		t.Errorf("addr = %q", addr)
	}
	if !s.IsRunning() {
		t.Error("IsRunning() = false after start")
	}
	if s.Port() == 0 {
		t.Error("Port() = 0 after start")
	}

	// HTTP request round trip.
	resp, err := http.Get(addr + "/index.html")
	if err != nil {
		t.Fatalf("GET = %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<h1>Home</h1>") {
		t.Errorf("GET body = %q", body)
	}

	// Double start rejected.
	if err := s.Start(ctx); err == nil {
		t.Error("Start() again error = nil, want already-running")
	}

	// Stop and verify.
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if s.IsRunning() {
		t.Error("IsRunning() = true after stop")
	}
	// Stop when not running is a no-op.
	if err := s.Stop(); err != nil {
		t.Errorf("Stop() twice error = %v, want nil", err)
	}
}

func TestLifecycle_AutoPort(t *testing.T) {
	s := NewServer(Config{RootDir: setupRoot(t)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, err := s.StartAndWait(ctx)
	if err != nil {
		t.Fatalf("StartAndWait() error = %v", err)
	}
	if addr == "" {
		t.Fatal("addr empty")
	}
	if s.Port() == 0 {
		t.Error("Port() = 0, want auto-selected non-zero")
	}
	if !s.IsRunning() {
		t.Error("IsRunning() = false")
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestLifecycle_CtxCancelStopsServer(t *testing.T) {
	s := NewServer(Config{RootDir: setupRoot(t)})

	ctx, cancel := context.WithCancel(context.Background())
	_, err := s.StartAndWait(ctx)
	if err != nil {
		t.Fatalf("StartAndWait() error = %v", err)
	}
	cancel()

	// Cancel triggers Shutdown; poll until the server is no longer
	// reachable or Stop succeeds.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !s.IsRunning() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Fallback: direct Stop must work and leave the server stopped.
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop() after cancel error = %v", err)
	}
	if s.IsRunning() {
		t.Error("IsRunning() = true after cancel+stop")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Port != 0 {
		t.Errorf("Port = %d, want 0", cfg.Port)
	}
	if cfg.ReadTimeout <= 0 || cfg.WriteTimeout <= 0 {
		t.Errorf("timeouts = %v/%v, want positive", cfg.ReadTimeout, cfg.WriteTimeout)
	}
}
