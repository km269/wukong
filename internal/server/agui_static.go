// agui_static.go — serves the embedded AG-UI reference client
// (roadmap P1-6) at "/" on the AG-UI server. The page is the
// protocol's living documentation: a self-contained chat console
// (no build step, no external assets) that exercises the full SSE
// contract — text_delta streaming, tool_calls display, error
// surfacing, and session continuity via the done event.
package server

import (
	"embed"
	"net/http"
)

//go:embed agui.html
var aguiStatic embed.FS

// mountIndex registers the reference client on the mux at "/".
func (s *AGUIServer) mountIndex(mux *http.ServeMux) {
	page, err := aguiStatic.ReadFile("agui.html")
	if err != nil {
		// The file is embedded at build time; this is unreachable
		// unless the embed directive rots.
		page = []byte("agui reference client missing")
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// "/" is a catch-all; only answer the root itself so
		// unknown paths still 404 instead of returning the page.
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
}
