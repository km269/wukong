package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLocalhostOrigin_TableDriven(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		{"empty origin rejected", "", false},
		{"plain http localhost", "http://localhost", true},
		{"http localhost with port", "http://localhost:8080", true},
		{"http localhost with large port", "http://localhost:65535", true},
		{"http 127.0.0.1", "http://127.0.0.1", true},
		{"http 127.0.0.1 with port", "http://127.0.0.1:3000", true},
		{"http IPv6 loopback", "http://[::1]", true},
		{"http IPv6 loopback with port", "http://[::1]:5173", true},
		{"https localhost", "https://localhost", true},
		{"https localhost with port", "https://localhost:8443", true},
		{"https 127.0.0.1", "https://127.0.0.1", true},
		{"https 127.0.0.1 with port", "https://127.0.0.1:443", true},
		{"https IPv6 loopback", "https://[::1]", true},
		{"https IPv6 loopback with port", "https://[::1]:9443", true},
		{"case insensitive scheme", "HTTP://LOCALHOST:8080", true},
		{"case insensitive host", "https://LOCALHOST:8443", true},
		{"mixed case complete", "Http://127.0.0.1:3000", true},
		{"remote host rejected", "http://example.com", false},
		{"remote host with port rejected", "http://example.com:8080", false},
		{"subdomain prefix not localhost", "http://localhost.evil.com", false},
		{"suffix not localhost", "http://localhostevil.com", false},
		{"hostname not exactly localhost", "http://notlocalhost", false},
		{"private LAN IP rejected", "http://192.168.1.1", false},
		{"ftp scheme rejected", "ftp://localhost", false},
		{"scheme missing rejected", "localhost:8080", false},
		{"trailing slash rejected", "http://localhost/", false},
		{"whitespace origin rejected", "  http://localhost  ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLocalhostOrigin(tt.origin); got != tt.want {
				t.Errorf("isLocalhostOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func TestSetLocalhostOnly_LocalhostOrigin_SetsAllHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api", nil)
	req.Header.Set("Origin", "http://localhost:8080")

	SetLocalhostOnly(rec, req)

	want := map[string]string{
		"Access-Control-Allow-Origin":  "http://localhost:8080",
		"Access-Control-Allow-Methods": "GET, POST, OPTIONS",
		"Access-Control-Allow-Headers": "Content-Type, Authorization, X-API-Key",
		"Access-Control-Max-Age":       "86400",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %q = %q, want %q", k, got, v)
		}
	}
}

func TestSetLocalhostOnly_RemoteOrigin_OmitsCORSHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api", nil)
	req.Header.Set("Origin", "https://evil.example.com")

	SetLocalhostOnly(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty (must block cross-origin response)", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Errorf("Access-Control-Allow-Methods = %q, want empty", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "" {
		t.Errorf("Access-Control-Allow-Headers = %q, want empty", got)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "" {
		t.Errorf("Access-Control-Max-Age = %q, want empty", got)
	}
}

func TestSetLocalhostOnly_MissingOrigin_OmitsCORSHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api", nil)
	// No Origin header: non-browser clients (CLI, curl) are unaffected.

	SetLocalhostOnly(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty when no Origin present", got)
	}
}
