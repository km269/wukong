package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewMCPServer(t *testing.T) {
	manager := NewManager(nil)
	addr := ":18080"
	server := NewMCPServer(manager, addr)

	if server == nil {
		t.Fatal("expected non-nil server")
	}
	if server.server == nil {
		t.Fatal("expected non-nil http.Server")
	}
	if server.auditLogger == nil {
		t.Fatal("expected non-nil audit logger")
	}
	if server.healthChecker == nil {
		t.Fatal("expected non-nil health checker")
	}
}

func TestMCPServer_RegisterTool(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	handlerCalled := false
	server.RegisterTool(MCPToolInfo{
		Name:        "test_tool",
		Description: "A test tool",
	}, func(ctx context.Context, args interface{}) (interface{}, error) {
		handlerCalled = true
		return "hello", nil
	})

	server.mu.RLock()
	_, ok := server.tools["test_tool"]
	server.mu.RUnlock()
	if !ok {
		t.Error("tool not registered")
	}

	server.mu.RLock()
	info, ok := server.toolInfos["test_tool"]
	server.mu.RUnlock()
	if !ok {
		t.Error("tool info not registered")
	}
	if info.Description != "A test tool" {
		t.Errorf("expected description 'A test tool', got %q", info.Description)
	}
	if !handlerCalled {
		_ = handlerCalled
	}
}

func TestMCPServer_Initialize(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	reqBody := `{"jsonrpc":"2.0","method":"initialize","id":1}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = newTestReadCloser([]byte(reqBody))

	w := httptest.NewRecorder()
	server.handleJSONRPC(w, req)

	var resp jsonRPCResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("expected result to be map")
	}
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("expected protocolVersion '2024-11-05', got %v", result["protocolVersion"])
	}
	if result["serverInfo"] == nil {
		t.Error("expected serverInfo in response")
	}
}

func TestMCPServer_ToolsList(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	server.RegisterTool(MCPToolInfo{
		Name:        "test_tool",
		Description: "A test tool",
	}, func(ctx context.Context, args interface{}) (interface{}, error) {
		return "hello", nil
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = newTestReadCloser([]byte(
		`{"jsonrpc":"2.0","method":"tools/list","id":2}`))

	w := httptest.NewRecorder()
	server.handleJSONRPC(w, req)

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	tools, ok := resp["tools"].([]interface{})
	if !ok {
		t.Fatal("expected tools array")
	}
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
}

func TestMCPServer_ToolsCall(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	server.RegisterTool(MCPToolInfo{
		Name:        "echo_tool",
		Description: "Echoes input",
	}, func(ctx context.Context, args interface{}) (interface{}, error) {
		if s, ok := args.(string); ok {
			return "echo: " + s, nil
		}
		return "echo: " + fmt.Sprint(args), nil
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = newTestReadCloser([]byte(
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"echo_tool","arguments":"test"},"id":3}`))

	w := httptest.NewRecorder()
	server.handleJSONRPC(w, req)

	var resp jsonRPCResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatal("expected result to be map")
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) != 1 {
		t.Fatal("expected single content item")
	}
	textMap, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected content item to be map")
	}
	if textMap["text"] != "echo: test" {
		t.Errorf("expected 'echo: test', got %v", textMap["text"])
	}
}

func TestMCPServer_ToolsCall_NotFound(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = newTestReadCloser([]byte(
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"nonexistent"},"id":4}`))

	w := httptest.NewRecorder()
	server.handleJSONRPC(w, req)

	var resp jsonRPCResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent tool")
	}
	if resp.Error.Code != errMethodNotFound {
		t.Errorf("expected code %d, got %d", errMethodNotFound, resp.Error.Code)
	}
}

func TestMCPServer_MethodNotFound(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = newTestReadCloser([]byte(
		`{"jsonrpc":"2.0","method":"unknown/method","id":5}`))

	w := httptest.NewRecorder()
	server.handleJSONRPC(w, req)

	var resp jsonRPCResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != errMethodNotFound {
		t.Errorf("expected code %d, got %d", errMethodNotFound, resp.Error.Code)
	}
}

func TestMCPServer_Health(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	server.RegisterTool(MCPToolInfo{
		Name:        "test_tool",
		Description: "A test tool",
	}, func(ctx context.Context, args interface{}) (interface{}, error) {
		return "ok", nil
	})

	req := httptest.NewRequest(http.MethodGet, "/mcp/health", nil)
	w := httptest.NewRecorder()
	server.handleHealth(w, req)

	var status MCPHealthStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to parse health response: %v", err)
	}
	if !status.Running {
		t.Error("expected running to be true")
	}
	if status.ToolCount != 1 {
		t.Errorf("expected 1 tool, got %d", status.ToolCount)
	}
}

func TestMCPServer_Ping(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = newTestReadCloser([]byte(
		`{"jsonrpc":"2.0","method":"ping","id":6}`))

	w := httptest.NewRecorder()
	server.handleJSONRPC(w, req)

	var resp jsonRPCResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

func TestMCPServer_InvalidJSON(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = newTestReadCloser([]byte(`not json`))

	w := httptest.NewRecorder()
	server.handleJSONRPC(w, req)

	var resp jsonRPCResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected parse error")
	}
	if resp.Error.Code != errParseError {
		t.Errorf("expected code %d, got %d", errParseError, resp.Error.Code)
	}
}

func TestMCPServer_ToolsCall_ErrorResponse(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	server.RegisterTool(MCPToolInfo{
		Name:        "failing_tool",
		Description: "Always fails",
	}, func(ctx context.Context, args interface{}) (interface{}, error) {
		return nil, fmt.Errorf("tool execution failed: %v", args)
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = newTestReadCloser([]byte(
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"failing_tool","arguments":"test"},"id":7}`))

	w := httptest.NewRecorder()
	server.handleJSONRPC(w, req)

	var resp jsonRPCResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for failing tool")
	}
	if resp.Error.Code != errInternal {
		t.Errorf("expected code %d, got %d", errInternal, resp.Error.Code)
	}
}

func TestMCPServer_AuditLogRecording(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	server.RegisterTool(MCPToolInfo{
		Name:        "audit_tool",
		Description: "Test audit",
	}, func(ctx context.Context, args interface{}) (interface{}, error) {
		return "result", nil
	})

	args, _ := json.Marshal(map[string]string{"input": "test"})
	reqBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "audit_tool",
			"arguments": args,
		},
		"id": 8,
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = newTestReadCloser(reqBody)

	w := httptest.NewRecorder()
	server.handleJSONRPC(w, req)

	entries := server.auditLogger.Recent(5)
	if len(entries) < 1 {
		t.Fatal("expected at least 1 audit entry")
	}
	last := entries[0]
	if last.ToolName != "audit_tool" {
		t.Errorf("expected tool name 'audit_tool', got %q", last.ToolName)
	}
	if last.IsError {
		t.Error("expected success, got error")
	}
	if last.DurationMs < 0 {
		t.Error("expected non-negative duration")
	}
}

func TestMCPServer_Shutdown(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := server.Shutdown(ctx)
	if err != nil {
		t.Errorf("unexpected shutdown error: %v", err)
	}
}

func TestToolAuditLogger_RecordAndRecent(t *testing.T) {
	logger := NewToolAuditLogger(5)

	for i := 0; i < 3; i++ {
		logger.Record(ToolAuditEntry{
			ToolName:  fmt.Sprintf("tool_%d", i),
			ArgsSize:  10,
			Timestamp: time.Now(),
			ClientIP:  "127.0.0.1",
		})
	}

	entries := logger.Recent(2)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ToolName != "tool_2" {
		t.Errorf("expected tool_2, got %s", entries[0].ToolName)
	}
	if entries[1].ToolName != "tool_1" {
		t.Errorf("expected tool_1, got %s", entries[1].ToolName)
	}
}

func TestToolAuditLogger_Eviction(t *testing.T) {
	logger := NewToolAuditLogger(3)

	for i := 0; i < 5; i++ {
		logger.Record(ToolAuditEntry{
			ToolName:  fmt.Sprintf("tool_%d", i),
			ArgsSize:  5,
			Timestamp: time.Now(),
		})
	}

	all := logger.Entries()
	if len(all) != 3 {
		t.Fatalf("expected 3 entries after eviction, got %d", len(all))
	}
	if all[0].ToolName != "tool_2" {
		t.Errorf("expected oldest kept entry to be tool_2, got %s", all[0].ToolName)
	}
	if all[2].ToolName != "tool_4" {
		t.Errorf("expected newest entry to be tool_4, got %s", all[2].ToolName)
	}
}

func TestMCPHealthChecker_Status(t *testing.T) {
	audit := NewToolAuditLogger(100)
	health := NewMCPHealthChecker(audit)

	audit.Record(ToolAuditEntry{ToolName: "a1", IsError: false, Timestamp: time.Now()})
	audit.Record(ToolAuditEntry{ToolName: "a2", IsError: false, Timestamp: time.Now()})
	audit.Record(ToolAuditEntry{ToolName: "a3", IsError: true, Timestamp: time.Now()})
	health.RecordCall(false)
	health.RecordCall(false)
	health.RecordCall(true)

	status := health.Status(true, 5)
	if !status.Running {
		t.Error("expected running true")
	}
	if status.ToolCount != 5 {
		t.Errorf("expected 5 tools, got %d", status.ToolCount)
	}
	if status.TotalCalls != 3 {
		t.Errorf("expected 3 total calls, got %d", status.TotalCalls)
	}
	if status.ErrorCalls != 1 {
		t.Errorf("expected 1 error call, got %d", status.ErrorCalls)
	}
	if len(status.RecentCalls) < 3 {
		t.Errorf("expected at least 3 recent calls, got %d", len(status.RecentCalls))
	}
}

func TestMCPHealthStatus_JSONRoundTrip(t *testing.T) {
	status := MCPHealthStatus{
		Running:    true,
		ToolCount:  3,
		TotalCalls: 10,
		ErrorCalls: 1,
		Uptime:     "1h0m0s",
		StartTime:  time.Now(),
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded MCPHealthStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !decoded.Running || decoded.ToolCount != 3 {
		t.Errorf("decoded fields mismatched: %+v", decoded)
	}
}

func TestMCPServer_GetHealthStatus(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	server.RegisterTool(MCPToolInfo{Name: "t1"}, func(ctx context.Context, args interface{}) (interface{}, error) {
		return "ok", nil
	})
	server.RegisterTool(MCPToolInfo{Name: "t2"}, func(ctx context.Context, args interface{}) (interface{}, error) {
		return "ok", nil
	})

	status := server.HealthStatus()
	if !status.Running {
		t.Error("expected running")
	}
	if status.ToolCount != 2 {
		t.Errorf("expected 2 tools, got %d", status.ToolCount)
	}
}

func TestMCPServer_UnregisterTool(t *testing.T) {
	manager := NewManager(nil)
	server := NewMCPServer(manager, ":0")

	server.RegisterTool(MCPToolInfo{Name: "temp"}, func(ctx context.Context, args interface{}) (interface{}, error) {
		return nil, nil
	})
	server.UnregisterTool("temp")

	server.mu.RLock()
	_, ok := server.tools["temp"]
	server.mu.RUnlock()
	if ok {
		t.Error("expected tool to be removed")
	}

	server.mu.RLock()
	_, ok = server.toolInfos["temp"]
	server.mu.RUnlock()
	if ok {
		t.Error("expected tool info to be removed")
	}
}

type testReadCloser struct {
	data  []byte
	index int
}

func newTestReadCloser(data []byte) *testReadCloser {
	return &testReadCloser{data: data}
}

func (r *testReadCloser) Read(p []byte) (int, error) {
	if r.index >= len(r.data) {
		return 0, fmt.Errorf("EOF")
	}
	n := copy(p, r.data[r.index:])
	r.index += n
	return n, nil
}

func (r *testReadCloser) Close() error {
	return nil
}
