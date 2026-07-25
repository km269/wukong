// Package server provides AG-UI (Agent-User Interaction) SSE server
// for web-based chat UIs. It implements a lightweight SSE protocol
// compatible with the AG-UI specification, enabling real-time
// streaming of agent responses, tool calls, and completion events.
//
// Since tRPC-Agent-Go v1.10.0 does not include server/agui, this is
// a Wukong-native implementation that follows the AG-UI event protocol.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// maxRequestBodySize limits incoming HTTP request body sizes to
// prevent memory exhaustion from malicious payloads.
const maxRequestBodySize = 10 << 20 // 10 MB

// AGUIServer provides an SSE-based HTTP server that exposes
// agent conversation capabilities to web-based chat UIs.
type AGUIServer struct {
	runner  runner.Runner
	path    string
	secCfg  ServerSecurityConfig
	mu      sync.RWMutex
	running bool
	server  *http.Server
}

// AGUIConfig configures the AG-UI server.
type AGUIConfig struct {
	Runner   runner.Runner
	Path     string
	Security ServerSecurityConfig
}

// NewAGUIServer creates an AG-UI protocol server.
func NewAGUIServer(cfg *AGUIConfig) (*AGUIServer, error) {
	if cfg.Runner == nil {
		return nil, fmt.Errorf("runner is required for AG-UI server")
	}
	path := cfg.Path
	if path == "" {
		path = "/agui"
	}
	return &AGUIServer{runner: cfg.Runner, path: path, secCfg: cfg.Security}, nil
}

// Handler returns the HTTP handler for mounting into an HTTP server.
func (s *AGUIServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.path, s.handleChat)
	mux.HandleFunc("/health", s.handleHealth)
	return mux
}

// Start begins listening on the given address with optional TLS,
// authentication, and rate limiting from the security config.
func (s *AGUIServer) Start(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	handler := s.Handler()
	securedHandler, tlsCfg := ApplySecurity(handler, s.secCfg)

	s.server = &http.Server{
		Addr:         addr,
		Handler:      securedHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	if tlsCfg != nil {
		s.server.TLSConfig = tlsCfg
	}

	s.running = true

	slog.Info("AG-UI server starting",
		"address", addr,
		"endpoint", s.path,
		"tls", tlsCfg != nil,
		"auth", s.secCfg.Auth.Type,
	)

	if tlsCfg != nil {
		return s.server.ListenAndServeTLS("", "")
	}
	return s.server.ListenAndServe()
}

// Stop gracefully shuts down the AG-UI server, waiting for active
// connections to finish before returning.
func (s *AGUIServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.server == nil {
		return nil
	}

	slog.Info("AG-UI server stopping")
	s.running = false
	return s.server.Shutdown(ctx)
}

// AGUIEvent represents a single SSE event in the AG-UI protocol.
type AGUIEvent struct {
	Type    string `json:"type"`
	Data    any    `json:"data,omitempty"`
	EventID string `json:"event_id,omitempty"`
}

// handleHealth responds with a simple health check.
func (s *AGUIServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleChat processes an incoming chat request and streams events via SSE.
func (s *AGUIServer) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.corsHeaders(w)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body with size limit to prevent memory exhaustion.
	var req struct {
		UserID    string `json:"user_id"`
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(
		io.LimitReader(r.Body, maxRequestBodySize),
	).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	// Default identifiers.
	if req.UserID == "" {
		req.UserID = "agui-user"
	}
	if req.SessionID == "" {
		req.SessionID = uuid.New().String()
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	s.corsHeaders(w)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Run agent and stream events.
	ctx := r.Context()
	userMsg := model.NewUserMessage(req.Message)
	events, err := s.runner.Run(ctx, req.UserID, req.SessionID, userMsg)
	if err != nil {
		s.writeSSE(w, flusher, "error", map[string]string{
			"message": err.Error(),
		})
		return
	}

	var fullText string
	for evt := range events {
		aguiEvt := s.translateEvent(evt, &fullText)
		if aguiEvt != nil {
			s.writeSSE(w, flusher, aguiEvt.Type, aguiEvt.Data)
		}
	}

	// Send completion event.
	s.writeSSE(w, flusher, "done", map[string]any{
		"session_id": req.SessionID,
		"full_text":  fullText,
	})
}

// translateEvent converts an internal event.Event to an AG-UI protocol event.
func (s *AGUIServer) translateEvent(evt *event.Event, fullText *string) *AGUIEvent {
	if evt == nil {
		return nil
	}

	if evt.Error != nil {
		return &AGUIEvent{
			Type: "error",
			Data: map[string]string{"message": evt.Error.Message},
		}
	}

	if evt.Response != nil && len(evt.Response.Choices) > 0 {
		choice := evt.Response.Choices[0]

		// Text delta (streaming content).
		if choice.Delta.Content != "" {
			*fullText += choice.Delta.Content
			return &AGUIEvent{
				Type: "text_delta",
				Data: map[string]string{
					"content": choice.Delta.Content,
				},
			}
		}

		// Tool calls.
		if len(choice.Message.ToolCalls) > 0 {
			tools := make([]map[string]any, 0,
				len(choice.Message.ToolCalls))
			for _, tc := range choice.Message.ToolCalls {
				tools = append(tools, map[string]any{
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})
			}
			return &AGUIEvent{
				Type: "tool_calls",
				Data: map[string]any{
					"tools": tools,
				},
			}
		}
	}

	return nil
}

// writeSSE sends a single SSE event.
func (s *AGUIServer) writeSSE(
	w http.ResponseWriter, flusher http.Flusher,
	eventType string, data any,
) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		slog.Warn("agui: marshal SSE event failed",
			"event", eventType,
			"error", err.Error())
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, jsonData)
	flusher.Flush()
}

// corsHeaders sets CORS headers for browser access.
func (s *AGUIServer) corsHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers",
		"Content-Type, Authorization")
}
