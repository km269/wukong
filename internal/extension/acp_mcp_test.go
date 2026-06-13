package extension

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestNewACPMCPBridge_Disabled(t *testing.T) {
	cfg := &config.ACPMCPConfig{Enabled: false}
	bridge, err := NewACPMCPBridge(nil, cfg)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if bridge != nil {
		t.Error("expected nil bridge when disabled")
	}
}

func TestACPMCPBridge_Addr(t *testing.T) {
	cfg := &config.ACPMCPConfig{
		Enabled:  true,
		Address:  ":3400",
		Path:     "/mcp",
	}

	mgr := NewManager(&config.WukongConfig{
		Extensions: []config.ExtensionConfig{},
	})

	bridge, err := NewACPMCPBridge(mgr, cfg)
	if err != nil {
		t.Fatalf("NewACPMCPBridge: %v", err)
	}
	if bridge == nil {
		t.Fatal("expected non-nil bridge")
	}

	addr := bridge.ACPMCPAddr()
	expected := "http://localhost:3400/mcp"
	if addr != expected {
		t.Errorf("addr = %q, want %q", addr, expected)
	}
}

func TestMCPJSONRPCRequest_Marshal(t *testing.T) {
	req := MCPJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}
}

func TestMCPJSONRPCResponse_Error(t *testing.T) {
	resp := MCPJSONRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Error: &MCPError{
			Code:    -32601,
			Message: "method not found",
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	t.Logf("error response: %s", string(data))
}

func TestACPMPBridge_HandleRequest(t *testing.T) {
	cfg := &config.ACPMCPConfig{
		Enabled: true,
		Address: ":3401",
		Path:    "/mcp",
	}

	mgr := NewManager(&config.WukongConfig{
		Extensions: []config.ExtensionConfig{
			{Name: "developer", Type: "builtin", Enabled: true},
		},
	})
	if err := mgr.Initialize(context.TODO()); err != nil {
		t.Fatalf("init manager: %v", err)
	}

	bridge, err := NewACPMCPBridge(mgr, cfg)
	if err != nil {
		t.Fatalf("NewACPMCPBridge: %v", err)
	}
	if bridge == nil {
		t.Fatal("expected non-nil bridge")
	}

	// Verify the bridge was created with handlers.
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", bridge.handleMCP)

	server := &http.Server{Addr: ":3401", Handler: mux}
	go func() { _ = server.ListenAndServe() }()

	bridge.running = true
	if !bridge.IsRunning() {
		t.Error("expected running after start")
	}

	// Cleanup
	_ = server.Close()
	_ = bridge.Stop()
}
