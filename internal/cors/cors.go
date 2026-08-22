// Package cors provides localhost-only CORS middleware for local
// agent HTTP services.
package cors

import (
	"net/http"
	"strings"
)

// SetLocalhostOnly sets CORS headers that only allow localhost
// origins. This prevents malicious websites from making
// cross-origin requests to local agent services (AGUI, ACP,
// ARD, DID) while still allowing local development tools to
// function.
//
// If the request Origin is not a localhost variant, the
// Access-Control-Allow-Origin header is omitted entirely,
// causing the browser to block the cross-origin response.
func SetLocalhostOnly(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if isLocalhostOrigin(origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods",
			"GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers",
			"Content-Type, Authorization, X-API-Key")
		w.Header().Set("Access-Control-Max-Age", "86400")
	}
}

// isLocalhostOrigin checks if the given Origin header value
// refers to a localhost address (with any port).
func isLocalhostOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	lower := strings.ToLower(origin)
	for _, prefix := range []string{
		"http://localhost",
		"http://127.0.0.1",
		"http://[::1]",
		"https://localhost",
		"https://127.0.0.1",
		"https://[::1]",
	} {
		if lower == prefix || strings.HasPrefix(lower, prefix+":") {
			return true
		}
	}
	return false
}
