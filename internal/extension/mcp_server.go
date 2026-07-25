package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/km269/wukong/pkg/logutil"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type ToolHandler func(ctx context.Context, args interface{}) (interface{}, error)

type MCPServer struct {
	server        *http.Server
	manager       *Manager
	auditLogger   *ToolAuditLogger
	healthChecker *MCPHealthChecker
	tools         map[string]ToolHandler
	toolInfos     map[string]MCPToolInfo
	mu            sync.RWMutex
	startTime     time.Time
}

func NewMCPServer(manager *Manager, addr string) *MCPServer {
	auditLogger := NewToolAuditLogger(10000)
	healthChecker := NewMCPHealthChecker(auditLogger)

	s := &MCPServer{
		manager:       manager,
		auditLogger:   auditLogger,
		healthChecker: healthChecker,
		tools:         make(map[string]ToolHandler),
		toolInfos:     make(map[string]MCPToolInfo),
		startTime:     time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleJSONRPC)
	mux.HandleFunc("/mcp/health", s.handleHealth)
	mux.HandleFunc("/mcp/tools", s.handleToolsList)

	s.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s
}

func (s *MCPServer) Start() error {
	logutil.Info("[mcp] server starting",
		"addr", s.server.Addr)

	errCh := make(chan error, 1)
	go func() {
		if err := s.server.ListenAndServe(); err != nil &&
			err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("mcp server listen failed: %w", err)
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}

func (s *MCPServer) Shutdown(ctx context.Context) error {
	logutil.Info("[mcp] server shutting down")
	return s.server.Shutdown(ctx)
}

func (s *MCPServer) RegisterTool(info MCPToolInfo, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[info.Name] = handler
	s.toolInfos[info.Name] = info
}

func (s *MCPServer) UnregisterTool(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tools, name)
	delete(s.toolInfos, name)
}

func (s *MCPServer) SyncToolsFromManager(ctx context.Context) {
	for _, ts := range s.manager.ToolSets() {
		if ts == nil {
			continue
		}
		for _, t := range ts.Tools(ctx) {
			decl := t.Declaration()
			if decl == nil {
				continue
			}
			if _, exists := s.tools[decl.Name]; exists {
				continue
			}

			callable, ok := t.(tool.CallableTool)
			if !ok {
				continue
			}

			info := MCPToolInfo{
				Name:        decl.Name,
				Description: decl.Description,
			}
			if decl.InputSchema != nil {
				schemaJSON, err := json.Marshal(decl.InputSchema)
				if err == nil {
					info.InputSchema = schemaJSON
				}
			}

			s.tools[decl.Name] = func(callCtx context.Context, args interface{}) (interface{}, error) {
				argsJSON, err := json.Marshal(args)
				if err != nil {
					return nil, err
				}
				result, err := callable.Call(callCtx, argsJSON)
				if err != nil {
					return nil, err
				}
				return result, nil
			}
			s.toolInfos[decl.Name] = info
		}
	}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
	ID      interface{}   `json:"id,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	errParseError     = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

func (s *MCPServer) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONRPCError(w, nil, errParseError, "Parse error")
		return
	}

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, r, &req)
	case "tools/list":
		s.handleToolsList(w, r)
	case "tools/call":
		s.handleToolsCall(w, r, &req)
	case "ping":
		writeJSON(w, jsonRPCResponse{
			JSONRPC: "2.0",
			Result:  map[string]string{"status": "ok"},
			ID:      req.ID,
		})
	default:
		writeJSONRPCError(w, req.ID, errMethodNotFound,
			fmt.Sprintf("Method not found: %s", req.Method))
	}
}

func (s *MCPServer) handleInitialize(w http.ResponseWriter, r *http.Request, req *jsonRPCRequest) {
	s.SyncToolsFromManager(r.Context())

	s.mu.RLock()
	toolCount := len(s.tools)
	s.mu.RUnlock()

	response := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "wukong-mcp-server",
			"version": "1.0.0",
		},
		"toolCount": toolCount,
	}

	writeJSON(w, jsonRPCResponse{
		JSONRPC: "2.0",
		Result:  response,
		ID:      req.ID,
	})
}

func (s *MCPServer) handleToolsList(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	needSync := len(s.tools) == 0
	s.mu.RUnlock()
	if needSync {
		s.SyncToolsFromManager(r.Context())
	}

	s.mu.RLock()
	tools := make([]MCPToolInfo, 0, len(s.toolInfos))
	for _, info := range s.toolInfos {
		tools = append(tools, info)
	}
	s.mu.RUnlock()

	writeJSON(w, map[string]interface{}{
		"tools": tools,
	})
}

func (s *MCPServer) handleToolsCall(w http.ResponseWriter, r *http.Request, req *jsonRPCRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, errInvalidParams,
			fmt.Sprintf("Invalid params: %s", err))
		return
	}

	s.mu.RLock()
	handler, ok := s.tools[params.Name]
	s.mu.RUnlock()

	argsSize := len(params.Arguments)
	var args interface{}
	if params.Arguments != nil {
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			writeJSONRPCError(w, req.ID, errInvalidParams,
				fmt.Sprintf("Invalid arguments: %s", err))
			return
		}
	}

	if !ok {
		clientIP := getClientIP(r)
		s.healthChecker.RecordCall(true)
		s.auditLogger.Record(ToolAuditEntry{
			ToolName:  params.Name,
			ArgsSize:  argsSize,
			IsError:   true,
			ErrorMsg:  "tool not found",
			ClientIP:  clientIP,
			Timestamp: time.Now(),
		})
		writeJSONRPCError(w, req.ID, errMethodNotFound,
			fmt.Sprintf("Tool not found: %s", params.Name))
		return
	}

	startTime := time.Now()
	clientIP := getClientIP(r)
	result, err := handler(r.Context(), args)
	duration := time.Since(startTime)

	if err != nil {
		s.healthChecker.RecordCall(true)
		s.auditLogger.Record(ToolAuditEntry{
			ToolName:   params.Name,
			ArgsSize:   argsSize,
			ResultSize: len(err.Error()),
			DurationMs: duration.Milliseconds(),
			IsError:    true,
			ErrorMsg:   err.Error(),
			ClientIP:   clientIP,
			Timestamp:  time.Now(),
		})
		writeJSONRPCError(w, req.ID, errInternal,
			fmt.Sprintf("Tool error: %s", err))
		return
	}

	s.healthChecker.RecordCall(false)
	var text string
	switch v := result.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	case json.RawMessage:
		text = string(v)
	default:
		resultBytes, _ := json.Marshal(result)
		text = string(resultBytes)
	}
	resultSize := len(text)
	s.auditLogger.Record(ToolAuditEntry{
		ToolName:   params.Name,
		ArgsSize:   argsSize,
		ResultSize: resultSize,
		DurationMs: duration.Milliseconds(),
		IsError:    false,
		ClientIP:   clientIP,
		Timestamp:  time.Now(),
	})

	writeJSON(w, jsonRPCResponse{
		JSONRPC: "2.0",
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": text,
				},
			},
		},
		ID: req.ID,
	})
}

func (s *MCPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	toolCount := len(s.tools)
	s.mu.RUnlock()

	status := s.healthChecker.Status(true, toolCount)
	status.StartTime = s.startTime

	writeJSON(w, status)
}

func (s *MCPServer) HealthStatus() MCPHealthStatus {
	s.mu.RLock()
	toolCount := len(s.tools)
	s.mu.RUnlock()
	status := s.healthChecker.Status(true, toolCount)
	status.StartTime = s.startTime
	return status
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeJSONRPCError(w http.ResponseWriter, id interface{}, code int, message string) {
	writeJSON(w, jsonRPCResponse{
		JSONRPC: "2.0",
		Error: &jsonRPCError{
			Code:    code,
			Message: message,
		},
		ID: id,
	})
}
