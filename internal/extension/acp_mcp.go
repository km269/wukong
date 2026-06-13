// Package extension provides MCP Bridge for ACP agent integration.
//
// The ACPMCPBridge exposes Wukong extensions as an MCP Server using
// tRPC-MCP-Go, enabling ACP agents to discover and call Wukong tools
// through the standard Model Context Protocol.
package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ACPMCPBridge exposes Wukong extensions as an MCP Server that
// ACP agents can connect to for tool discovery and invocation.
//
// The bridge implements a lightweight MCP-compatible HTTP endpoint
// that supports tools/list and tools/call JSON-RPC methods over
// standard HTTP POST, compatible with MCP clients.
type ACPMCPBridge struct {
	mu      sync.RWMutex
	cfg     *config.ACPMCPConfig
	addr    string
	mgr     *Manager
	server  *http.Server
	running bool
}

// MCPToolInfo describes a tool in MCP format.
type MCPToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// MCPListToolsResult is the response for tools/list.
type MCPListToolsResult struct {
	Tools []MCPToolInfo `json:"tools"`
}

// MCPCallToolRequest is the parameters for tools/call.
type MCPCallToolRequest struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// MCPCallToolResponse is the result for tools/call.
type MCPCallToolResponse struct {
	Content []MCPContentItem `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

// MCPContentItem represents a content item in an MCP response.
type MCPContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// MCPJSONRPCRequest is the standard MCP JSON-RPC request wrapper.
type MCPJSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// MCPJSONRPCResponse is the standard MCP JSON-RPC response wrapper.
type MCPJSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

// MCPError represents an MCP protocol error.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewACPMCPBridge creates an MCP bridge that registers all
// Wukong extension tools as MCP tools for ACP agent access.
func NewACPMCPBridge(
	mgr *Manager, cfg *config.ACPMCPConfig,
) (*ACPMCPBridge, error) {
	if !cfg.Enabled {
		util.Logger.Info("acp_mcp: bridge disabled by config")
		return nil, nil
	}

	address := cfg.Address
	if address == "" {
		address = ":3400"
	}

	bridge := &ACPMCPBridge{
		cfg:  cfg,
		addr: address,
		mgr:  mgr,
	}
	return bridge, nil
}

// Start begins listening for MCP connections from ACP agents.
func (b *ACPMCPBridge) Start() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return nil
	}

	mux := http.NewServeMux()
	path := b.cfg.Path
	if path == "" {
		path = "/mcp"
	}
	mux.HandleFunc(path, b.handleMCP)

	b.server = &http.Server{
		Addr:    b.addr,
		Handler: mux,
	}

	util.Logger.Info("acp_mcp: bridge starting",
		slog.String("address", b.addr),
		slog.String("path", path),
	)

	go func() {
		if err := b.server.ListenAndServe(); err != nil &&
			err != http.ErrServerClosed {
			util.Logger.Error("acp_mcp: server error",
				slog.String("error", err.Error()),
			)
		}
	}()

	b.running = true
	return nil
}

// Stop gracefully shuts down the MCP bridge.
func (b *ACPMCPBridge) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running || b.server == nil {
		return nil
	}

	util.Logger.Info("acp_mcp: bridge stopping")
	b.running = false
	return b.server.Shutdown(context.Background())
}

// IsRunning returns whether the bridge is active.
func (b *ACPMCPBridge) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.running
}

// ACPMCPAddr returns the full address that ACP agents should
// use to connect to the MCP bridge.
func (b *ACPMCPBridge) ACPMCPAddr() string {
	return "http://localhost" + b.addr + b.cfg.Path
}

// handleMCP processes incoming MCP JSON-RPC requests.
func (b *ACPMCPBridge) handleMCP(
	w http.ResponseWriter, r *http.Request,
) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req MCPJSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		b.writeError(w, nil, -32700, "parse error")
		return
	}

	switch req.Method {
	case "tools/list":
		b.handleListTools(w, req.ID)
	case "tools/call":
		b.handleCallTool(w, r, req.ID, req.Params)
	case "initialize":
		b.handleInitialize(w, req.ID)
	default:
		b.writeError(w, req.ID, -32601,
			fmt.Sprintf("method not found: %s", req.Method))
	}
}

// handleInitialize responds to the MCP initialize handshake.
func (b *ACPMCPBridge) handleInitialize(
	w http.ResponseWriter, id interface{},
) {
	b.writeResult(w, id, map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]string{
			"name":    "wukong-extensions",
			"version": "1.0.0",
		},
		"capabilities": map[string]interface{}{
			"tools": map[string]bool{},
		},
	})
}

// handleListTools returns all Wukong extension tools in MCP format.
func (b *ACPMCPBridge) handleListTools(
	w http.ResponseWriter, id interface{},
) {
	ctx := context.Background()
	var tools []MCPToolInfo

	for _, ts := range b.mgr.ToolSets() {
		if ts == nil {
			continue
		}
		for _, t := range ts.Tools(ctx) {
			decl := t.Declaration()
			if decl == nil {
				continue
			}
			info := MCPToolInfo{
				Name:        decl.Name,
				Description: decl.Description,
			}
			if decl.InputSchema != nil {
				schemaJSON, _ := json.Marshal(decl.InputSchema)
				info.InputSchema = schemaJSON
			}
			tools = append(tools, info)
		}
	}

	util.Logger.Debug("acp_mcp: tools/list",
		slog.Int("count", len(tools)),
	)

	b.writeResult(w, id, MCPListToolsResult{Tools: tools})
}

// handleCallTool executes a Wukong extension tool and returns
// the result in MCP format.
func (b *ACPMCPBridge) handleCallTool(
	w http.ResponseWriter, r *http.Request,
	id interface{}, params json.RawMessage,
) {
	var callReq MCPCallToolRequest
	if err := json.Unmarshal(params, &callReq); err != nil {
		b.writeError(w, id, -32602,
			fmt.Sprintf("invalid params: %v", err))
		return
	}

	// Find the tool across all extension tool sets.
	var targetTool tool.Tool
	ctx := r.Context()
	for _, ts := range b.mgr.ToolSets() {
		if ts == nil {
			continue
		}
		for _, t := range ts.Tools(ctx) {
			if decl := t.Declaration(); decl != nil &&
				decl.Name == callReq.Name {
				targetTool = t
				break
			}
		}
		if targetTool != nil {
			break
		}
	}

	if targetTool == nil {
		b.writeError(w, id, -32602,
			fmt.Sprintf("tool not found: %s", callReq.Name))
		return
	}

	// Check if the tool is callable.
	callable, ok := targetTool.(tool.CallableTool)
	if !ok {
		b.writeResult(w, id, MCPCallToolResponse{
			Content: []MCPContentItem{{
				Type: "text",
				Text: fmt.Sprintf(
					"tool %q is not callable", callReq.Name),
			}},
			IsError: true,
		})
		return
	}

	// Marshal arguments and call the tool.
	argsJSON, err := json.Marshal(callReq.Arguments)
	if err != nil {
		b.writeResult(w, id, MCPCallToolResponse{
			Content: []MCPContentItem{{
				Type: "text",
				Text: fmt.Sprintf("marshal args: %v", err),
			}},
			IsError: true,
		})
		return
	}

	result, callErr := callable.Call(ctx, argsJSON)
	if callErr != nil {
		b.writeResult(w, id, MCPCallToolResponse{
			Content: []MCPContentItem{{
				Type: "text",
				Text: fmt.Sprintf("tool error: %v", callErr),
			}},
			IsError: true,
		})
		return
	}

	resultJSON, _ := json.Marshal(result)
	resultText := string(resultJSON)
	if resultText == "" {
		resultText = fmt.Sprintf("%v", result)
	}

	util.Logger.Debug("acp_mcp: tools/call",
		slog.String("tool", callReq.Name),
		slog.Int("result_bytes", len(resultJSON)),
	)

	b.writeResult(w, id, MCPCallToolResponse{
		Content: []MCPContentItem{{
			Type: "text",
			Text: resultText,
		}},
	})
}

// writeResult writes a successful MCP JSON-RPC response.
func (b *ACPMCPBridge) writeResult(
	w http.ResponseWriter, id interface{}, result interface{},
) {
	b.writeJSON(w, http.StatusOK, MCPJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

// writeError writes an MCP JSON-RPC error response.
func (b *ACPMCPBridge) writeError(
	w http.ResponseWriter, id interface{}, code int, message string,
) {
	b.writeJSON(w, http.StatusOK, MCPJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &MCPError{
			Code:    code,
			Message: message,
		},
	})
}

// writeJSON serializes and writes a JSON response.
func (b *ACPMCPBridge) writeJSON(
	w http.ResponseWriter, statusCode int, data interface{},
) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}
