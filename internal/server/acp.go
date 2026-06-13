// Package server provides the Agent Client Protocol (ACP) server
// endpoint for Wukong.
//
// The ACPServer exposes the agent via ACP-compatible HTTP endpoints,
// enabling ACP client applications to natively connect to Wukong
// for conversational AI, tool discovery, and tool invocation.
//
// ACP Protocol Endpoints:
//
//	POST /acp/message/send          — Send user message, get agent response
//	GET  /acp/tools/list            — List available tools (Agent Card)
//	POST /acp/tools/call            — Direct tool invocation
//	GET  /acp/.well-known/agent.json — Agent capability discovery
//	GET  /acp/health                 — Health check
//
// Streaming is supported via Server-Sent Events (SSE) when
// EnableStreaming is true. Events follow the pattern:
//
//	event: text_delta
//	data: {"content":"..."}
//
//	event: tool_call
//	data: {"name":"...","arguments":{...}}
//
//	event: done
//	data: {"session_id":"...","full_text":"..."}
package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ACPServer provides an HTTP-based ACP endpoint that exposes
// the Wukong agent to ACP-compatible client applications.
type ACPServer struct {
	runner  runner.Runner
	agent   agent.Agent
	path    string
	mu      sync.RWMutex
	running bool
}

// ACPServerConfig configures the ACP server.
type ACPServerConfig struct {
	// Runner is the agent runner for processing messages.
	Runner runner.Runner
	// Agent is the agent instance for tool discovery.
	Agent agent.Agent
	// Path is the HTTP path prefix for ACP endpoints.
	Path string
	// EnableStreaming enables SSE streaming responses.
	EnableStreaming bool
}

// NewACPServer creates an ACP protocol server.
func NewACPServer(cfg *ACPServerConfig) (*ACPServer, error) {
	if cfg.Runner == nil {
		return nil, fmt.Errorf(
			"runner is required for ACP server")
	}
	path := cfg.Path
	if path == "" {
		path = "/acp"
	}
	return &ACPServer{
		runner: cfg.Runner,
		agent:  cfg.Agent,
		path:   path,
	}, nil
}

// Handler returns the HTTP handler for mounting.
func (s *ACPServer) Handler() http.Handler {
	mux := http.NewServeMux()

	// Core ACP endpoints.
	mux.HandleFunc(s.path+"/health", s.handleHealth)

	// message/send — the main conversation endpoint.
	mux.HandleFunc(s.path+"/message/send", s.handleMessageSend)

	// tools/list — Agent Card / tool discovery.
	mux.HandleFunc(s.path+"/tools/list", s.handleToolsList)

	// tools/call — direct tool invocation by ACP clients.
	mux.HandleFunc(s.path+"/tools/call", s.handleToolsCall)

	// .well-known — agent capability discovery.
	mux.HandleFunc(
		s.path+"/.well-known/agent.json",
		s.handleAgentCard,
	)

	return mux
}

// Start begins listening on the given address.
func (s *ACPServer) Start(addr string) error {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	slog.Info("ACP server starting",
		"address", addr,
		"path", s.path,
	)

	return http.ListenAndServe(addr, s.Handler())
}

// Stop signals shutdown.
func (s *ACPServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
}

// ==========================================================================
// ACP Data Types
// ==========================================================================

// ACPMessageSendRequest is the request body for message/send.
type ACPMessageSendRequest struct {
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// ACPToolInfo describes a single tool in the tools/list response.
type ACPToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ACPToolsListResponse is the response for tools/list.
type ACPToolsListResponse struct {
	AgentName    string         `json:"agent_name"`
	Description  string         `json:"description"`
	Tools        []ACPToolInfo  `json:"tools"`
	Capabilities []string       `json:"capabilities"`
}

// ACPToolCallRequest is the request body for tools/call.
type ACPToolCallRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ACPSSEEvent represents a single SSE event.
type ACPSSEEvent struct {
	EventType string      `json:"event"`
	Data      interface{} `json:"data"`
}

// ==========================================================================
// Handlers
// ==========================================================================

// handleHealth responds with health status.
func (s *ACPServer) handleHealth(
	w http.ResponseWriter, r *http.Request,
) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "wukong-acp",
	})
}

// handleAgentCard responds with the agent's capabilities for
// ACP client discovery.
func (s *ACPServer) handleAgentCard(
	w http.ResponseWriter, r *http.Request,
) {
	agentInfo := s.agent.Info()

	card := map[string]interface{}{
		"name":        agentInfo.Name,
		"description": agentInfo.Description,
		"version":     "0.4.0",
		"protocols":   []string{"acp"},
		"capabilities": []string{
			"streaming",
			"tool_calling",
			"multi_turn",
		},
		"endpoints": map[string]string{
			"message_send": s.path + "/message/send",
			"tools_list":   s.path + "/tools/list",
			"tools_call":   s.path + "/tools/call",
			"health":       s.path + "/health",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(card)
}

// handleMessageSend processes an incoming user message and streams
// the agent response back via SSE or non-streaming JSON.
func (s *ACPServer) handleMessageSend(
	w http.ResponseWriter, r *http.Request,
) {
	s.setCORSHeaders(w)

	if r.Method == http.MethodOptions {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w,
			"method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ACPMessageSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w,
			"invalid request body", http.StatusBadRequest)
		return
	}

	if req.Message == "" {
		http.Error(w,
			"message is required", http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		req.UserID = "acp-user"
	}
	if req.SessionID == "" {
		req.SessionID = uuid.New().String()
	}

	ctx := r.Context()
	userMsg := model.NewUserMessage(req.Message)

	events, err := s.runner.Run(
		ctx, req.UserID, req.SessionID, userMsg)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError,
			map[string]string{"error": err.Error()})
		return
	}

	// Set SSE headers for streaming.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		// Fallback: collect and return as single JSON.
		var fullText string
		for evt := range events {
			if evt.Error != nil {
				s.writeJSON(w, http.StatusInternalServerError,
					map[string]string{
						"error": evt.Error.Message,
					})
				return
			}
			if evt.Response != nil &&
				len(evt.Response.Choices) > 0 {
				fullText += evt.Response.Choices[0].
					Delta.Content
			}
		}
		s.writeJSON(w, http.StatusOK,
			map[string]interface{}{
				"session_id": req.SessionID,
				"response":   fullText,
			})
		return
	}

	// Stream events via SSE.
	var fullText string
	for evt := range events {
		acpEvt := s.translateEvent(evt, &fullText)
		if acpEvt != nil {
			s.writeSSE(w, flusher, acpEvt)
		}
	}

	// Send completion event.
	s.writeSSE(w, flusher, &ACPSSEEvent{
		EventType: "done",
		Data: map[string]interface{}{
			"session_id": req.SessionID,
			"full_text":  fullText,
		},
	})
}

// handleToolsList returns the full list of available tools
// for ACP client tool discovery.
func (s *ACPServer) handleToolsList(
	w http.ResponseWriter, r *http.Request,
) {
	s.setCORSHeaders(w)

	if r.Method == http.MethodOptions {
		return
	}

	agentInfo := s.agent.Info()
	tools := s.agent.Tools()

	var toolInfos []ACPToolInfo
	for _, t := range tools {
		decl := t.Declaration()
		if decl == nil {
			continue
		}
		info := ACPToolInfo{
			Name:        decl.Name,
			Description: decl.Description,
		}
		if decl.InputSchema != nil {
			schemaJSON, _ := json.Marshal(decl.InputSchema)
			info.InputSchema = schemaJSON
		}
		toolInfos = append(toolInfos, info)
	}

	resp := ACPToolsListResponse{
		AgentName:   agentInfo.Name,
		Description: agentInfo.Description,
		Tools:       toolInfos,
		Capabilities: []string{
			"streaming",
			"tool_calling",
			"multi_turn",
		},
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// handleToolsCall handles direct tool invocation by ACP clients
// without going through the full agent conversation flow.
func (s *ACPServer) handleToolsCall(
	w http.ResponseWriter, r *http.Request,
) {
	s.setCORSHeaders(w)

	if r.Method == http.MethodOptions {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w,
			"method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ACPToolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "invalid request body"})
		return
	}

	// Find the tool by name.
	var targetTool tool.Tool
	for _, t := range s.agent.Tools() {
		if decl := t.Declaration(); decl != nil &&
			decl.Name == req.Name {
			targetTool = t
			break
		}
	}

	if targetTool == nil {
		s.writeJSON(w, http.StatusNotFound,
			map[string]string{
				"error": fmt.Sprintf(
					"tool %q not found", req.Name),
			})
		return
	}

	// Check callable.
	callable, ok := targetTool.(tool.CallableTool)
	if !ok {
		s.writeJSON(w, http.StatusBadRequest,
			map[string]string{
				"error": fmt.Sprintf(
					"tool %q is not directly callable",
					req.Name,
				),
			})
		return
	}

	argsJSON, err := json.Marshal(req.Arguments)
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "marshal arguments failed"})
		return
	}

	result, callErr := callable.Call(r.Context(), argsJSON)
	if callErr != nil {
		s.writeJSON(w, http.StatusInternalServerError,
			map[string]string{
				"error": fmt.Sprintf("tool call: %v", callErr),
			})
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"result":  result,
	})
}

// ==========================================================================
// Event Translation (Agent Event → ACP SSE Event)
// ==========================================================================

// translateEvent converts an internal event.Event to an ACP SSE event.
func (s *ACPServer) translateEvent(
	evt *event.Event, fullText *string,
) *ACPSSEEvent {
	if evt == nil {
		return nil
	}

	if evt.Error != nil {
		return &ACPSSEEvent{
			EventType: "error",
			Data: map[string]string{
				"message": evt.Error.Message,
			},
		}
	}

	if evt.Response != nil && len(evt.Response.Choices) > 0 {
		choice := evt.Response.Choices[0]

		// Text delta (streaming content).
		if choice.Delta.Content != "" {
			*fullText += choice.Delta.Content
			return &ACPSSEEvent{
				EventType: "text_delta",
				Data: map[string]string{
					"content": choice.Delta.Content,
				},
			}
		}

		// Tool calls.
		if len(choice.Message.ToolCalls) > 0 {
			tools := make([]map[string]interface{}, 0,
				len(choice.Message.ToolCalls))
			for _, tc := range choice.Message.ToolCalls {
				tools = append(tools, map[string]interface{}{
					"id":        tc.ID,
					"name":      tc.Function.Name,
					"arguments": string(tc.Function.Arguments),
				})
			}
			return &ACPSSEEvent{
				EventType: "tool_calls",
				Data: map[string]interface{}{
					"tools": tools,
				},
			}
		}

		// Tool results.
		if choice.Message.Role == "tool" {
			return &ACPSSEEvent{
				EventType: "tool_result",
				Data: map[string]string{
					"content": choice.Message.Content,
				},
			}
		}
	}

	return nil
}

// ==========================================================================
// Utilities
// ==========================================================================

// writeSSE sends a single SSE event line.
func (s *ACPServer) writeSSE(
	w http.ResponseWriter, flusher http.Flusher,
	evt *ACPSSEEvent,
) {
	jsonData, err := json.Marshal(evt.Data)
	if err != nil {
		return
	}
	fmt.Fprintf(w,
		"event: %s\ndata: %s\n\n", evt.EventType, string(jsonData),
	)
	flusher.Flush()
}

// writeJSON writes a JSON response.
func (s *ACPServer) writeJSON(
	w http.ResponseWriter, statusCode int, data interface{},
) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// setCORSHeaders sets permissive CORS headers for browser access.
func (s *ACPServer) setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods",
		"GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers",
		"Content-Type, Authorization, X-API-Key")
}

// ==========================================================================
// Agent Card / Tool Discovery Helpers
// ==========================================================================

// BuildAgentCardFromAgent creates an agent card JSON from an
// agent.Agent instance for ACP discovery.
func BuildAgentCardFromAgent(
	ag agent.Agent, baseURL string,
) map[string]interface{} {
	info := ag.Info()

	card := map[string]interface{}{
		"name":        info.Name,
		"description": info.Description,
		"version":     "0.4.0",
		"base_url":    baseURL,
		"endpoints": map[string]string{
			"send":  "/message/send",
			"tools": "/tools/list",
			"call":  "/tools/call",
		},
		"capabilities": []string{
			"streaming",
			"tool_calling",
		},
	}

	// Add tool summaries for discovery.
	var toolNames []string
	for _, t := range ag.Tools() {
		if decl := t.Declaration(); decl != nil {
			toolNames = append(toolNames, decl.Name)
		}
	}
	card["tool_count"] = len(toolNames)
	card["tools"] = toolNames

	return card
}

// compile-time assertion — tool usage.
var _ tool.CallableTool
