package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// stubChannel is a minimal Channel implementation for router tests,
// parametrized by name and route path. None of the handler methods
// are exercised by routing tests.
type stubChannel struct {
	name string
	path string
}

func (s *stubChannel) Name() string                 { return s.name }
func (s *stubChannel) RoutePath() string            { return s.path }
func (s *stubChannel) VerifyRequest(*http.Request) ([]byte, error) { return nil, nil }
func (s *stubChannel) ParseMessage([]byte) (*GatewayMessage, error) { return nil, nil }
func (s *stubChannel) BuildUserID(*GatewayMessage) string           { return "" }
func (s *stubChannel) BuildSessionID(*GatewayMessage) string        { return "" }
func (s *stubChannel) SendReply(context.Context, *GatewayMessage, <-chan *event.Event) error {
	return nil
}
func (s *stubChannel) HandlePlatformEvent(http.ResponseWriter, *PlatformEvent) ([]byte, error) {
	return nil, nil
}

// TestRouteExactAndSubpath verifies that a registered channel
// matches its exact path and a direct sub-path.
func TestRouteExactAndSubpath(t *testing.T) {
	r := NewChannelRouter()
	if err := r.Register(&stubChannel{name: "feishu", path: "/feishu"}); err != nil {
		t.Fatalf("register: %v", err)
	}

	cases := []struct {
		url      string
		wantName string
		wantSub  string
	}{
		{"/feishu", "feishu", "/"},
		{"/feishu/callback", "feishu", "/callback"},
		{"/feishu/callback/x", "feishu", "/callback/x"}, // deep sub-path ok
	}
	for _, c := range cases {
		ch, sub := r.Route(c.url)
		if ch == nil {
			t.Errorf("Route(%q) = nil, want channel %q", c.url, c.wantName)
			continue
		}
		if ch.Name() != c.wantName {
			t.Errorf("Route(%q) channel = %q, want %q", c.url, ch.Name(), c.wantName)
		}
		if sub != c.wantSub {
			t.Errorf("Route(%q) sub = %q, want %q", c.url, sub, c.wantSub)
		}
	}
}

// TestRouteRejectsNonSegmentPrefix is the key regression test: a
// channel registered at "/feishu" must NOT match "/feishuabc" or
// "/feishuxyz", which the old HasPrefix-based matcher accepted.
func TestRouteRejectsNonSegmentPrefix(t *testing.T) {
	r := NewChannelRouter()
	if err := r.Register(&stubChannel{name: "feishu", path: "/feishu"}); err != nil {
		t.Fatalf("register: %v", err)
	}

	nonMatches := []string{
		"/feishuabc",
		"/feishuxyz",
		"/feishu123",
		"/fe",        // shorter prefix, not a segment boundary
		"/feish",     // partial
		"/other/x",   // unrelated
		"/FEISHU/x",  // case sensitive
	}
	for _, url := range nonMatches {
		if ch, _ := r.Route(url); ch != nil {
			t.Errorf("Route(%q) matched channel %q, want no match", url, ch.Name())
		}
	}
}

// TestRouteTrailingSlashNormalized verifies trailing slashes on the
// incoming URL are normalized so "/feishu/" routes the same as
// "/feishu".
func TestRouteTrailingSlashNormalized(t *testing.T) {
	r := NewChannelRouter()
	if err := r.Register(&stubChannel{name: "feishu", path: "/feishu"}); err != nil {
		t.Fatalf("register: %v", err)
	}

	ch, sub := r.Route("/feishu/")
	if ch == nil || ch.Name() != "feishu" {
		t.Fatalf(`Route("/feishu/") = %v, want feishu`, ch)
	}
	if sub != "/" {
		t.Errorf(`Route("/feishu/") sub = %q, want "/"`, sub)
	}
}

// TestRouteMultipleChannelsDisambiguates ensures two channels with
// distinct prefixes do not shadow each other.
func TestRouteMultipleChannelsDisambiguates(t *testing.T) {
	r := NewChannelRouter()
	for _, c := range []stubChannel{
		{name: "feishu", path: "/feishu"},
		{name: "wecom", path: "/wecom"},
	} {
		if err := r.Register(&c); err != nil {
			t.Fatalf("register %s: %v", c.name, err)
		}
	}

	if ch, _ := r.Route("/wecom/callback"); ch == nil || ch.Name() != "wecom" {
		t.Errorf(`Route("/wecom/callback") = %v, want wecom`, ch)
	}
	if ch, _ := r.Route("/feishu/callback"); ch == nil || ch.Name() != "feishu" {
		t.Errorf(`Route("/feishu/callback") = %v, want feishu`, ch)
	}
}

// TestRegisterNormalizesPath verifies that registering with or
// without a leading slash, and with a trailing slash, canonicalizes
// to the same path so duplicate detection works.
func TestRegisterNormalizesPath(t *testing.T) {
	r := NewChannelRouter()
	// Register with trailing slash → normalized to "/feishu".
	if err := r.Register(&stubChannel{name: "feishu", path: "/feishu/"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Same logical path, different name → must be rejected as dup path.
	err := r.Register(&stubChannel{name: "feishu2", path: "/feishu"})
	if err == nil {
		t.Error("expected duplicate-path error, got nil")
	}
	// The normalized channel must still route correctly.
	if ch, _ := r.Route("/feishu/callback"); ch == nil || ch.Name() != "feishu" {
		t.Errorf("route after normalize = %v, want feishu", ch)
	}
}

// TestRegisterWithoutLeadingSlash verifies a path lacking the leading
// "/" is normalized on registration.
func TestRegisterWithoutLeadingSlash(t *testing.T) {
	r := NewChannelRouter()
	if err := r.Register(&stubChannel{name: "slack", path: "slack"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if ch, _ := r.Route("/slack/callback"); ch == nil || ch.Name() != "slack" {
		t.Errorf(`Route("/slack/callback") = %v, want slack`, ch)
	}
}

// TestUnregisterRemovesRoute verifies Unregister cleans up both the
// name and the (normalized) path lookup.
func TestUnregisterRemovesRoute(t *testing.T) {
	r := NewChannelRouter()
	ch := &stubChannel{name: "feishu", path: "/feishu/"}
	if err := r.Register(ch); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Unregister("feishu"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if got := r.Lookup("feishu"); got != nil {
		t.Errorf("Lookup after unregister = %v, want nil", got)
	}
	if c, _ := r.Route("/feishu/callback"); c != nil {
		t.Errorf("Route after unregister matched %q, want nil", c.Name())
	}
}

// TestHandler404OnUnknownPath verifies the HTTP handler returns 404
// for paths that don't match any channel.
func TestHandler404OnUnknownPath(t *testing.T) {
	r := NewChannelRouter()
	_ = r.Register(&stubChannel{name: "feishu", path: "/feishu"})

	handler := r.Handler(func(ch Channel, w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rr := newRequestRecorder(handler, http.MethodPost, "/feishuabc")
	if rr.statusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d for non-matching path",
			rr.statusCode, http.StatusNotFound)
	}

	rr2 := newRequestRecorder(handler, http.MethodPost, "/feishu/callback")
	if rr2.statusCode != http.StatusOK {
		t.Errorf("status = %d, want %d for matching path",
			rr2.statusCode, http.StatusOK)
	}
}

// TestRouteRootPrefixMatchesAll verifies a channel registered at "/"
// acts as a catch-all (special case).
func TestRouteRootPrefixMatchesAll(t *testing.T) {
	r := NewChannelRouter()
	if err := r.Register(&stubChannel{name: "catchall", path: "/"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	for _, url := range []string{"/", "/anything", "/a/b/c"} {
		if ch, _ := r.Route(url); ch == nil || ch.Name() != "catchall" {
			t.Errorf("Route(%q) = %v, want catchall", url, ch)
		}
	}
}

// --- helpers ---

// requestRecorder captures the status code written by a handler.
type requestRecorder struct {
	header     http.Header
	statusCode int
}

func (rr *requestRecorder) Header() http.Header {
	if rr.header == nil {
		rr.header = http.Header{}
	}
	return rr.header
}
func (rr *requestRecorder) WriteHeader(code int) { rr.statusCode = code }
func (rr *requestRecorder) Write([]byte) (int, error) {
	if rr.statusCode == 0 {
		rr.statusCode = http.StatusOK
	}
	return 0, nil
}

// newRequestRecorder runs handler against a synthetic request and
// returns the recorder capturing the response status.
func newRequestRecorder(handler http.Handler, method, path string) *requestRecorder {
	rr := &requestRecorder{}
	req, _ := http.NewRequest(method, path, nil)
	handler.ServeHTTP(rr, req)
	if rr.statusCode == 0 {
		rr.statusCode = http.StatusOK
	}
	return rr
}

// Compile-time guard: ensure unused imports in this test file are
// referenced. json is used implicitly via stubChannel methods which
// return json.RawMessage-typed GatewayMessage.RawData.
var _ = json.RawMessage(nil)
