package extension

import (
	"context"
	"testing"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockToolSet implements tool.ToolSet for testing.
type mockToolSet struct {
	name  string
	tools []tool.Tool
}

func (m *mockToolSet) Name() string                          { return m.name }
func (m *mockToolSet) Tools(ctx context.Context) []tool.Tool { return m.tools }
func (m *mockToolSet) Init(ctx context.Context) error        { return nil }
func (m *mockToolSet) Close() error                          { return nil }

func TestNewManager(t *testing.T) {
	cfg := &config.WukongConfig{}
	m := NewManager(cfg)
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.toolSets == nil {
		t.Fatal("expected non-nil toolSets map")
	}
	if m.status == nil {
		t.Fatal("expected non-nil status map")
	}
}

func TestManager_ListExtensions_Empty(t *testing.T) {
	cfg := &config.WukongConfig{}
	m := NewManager(cfg)

	exts := m.ListExtensions()
	if len(exts) != 0 {
		t.Errorf("expected 0 extensions, got %d", len(exts))
	}
}

func TestManager_GetStatus_NotFound(t *testing.T) {
	cfg := &config.WukongConfig{}
	m := NewManager(cfg)

	_, ok := m.GetStatus("nonexistent")
	if ok {
		t.Error("expected false for non-existent extension")
	}
}

func TestManager_EnableExtension_NotFound(t *testing.T) {
	cfg := &config.WukongConfig{}
	m := NewManager(cfg)

	err := m.EnableExtension(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent extension")
	}
}

func TestManager_DisableExtension_NotFound(t *testing.T) {
	cfg := &config.WukongConfig{}
	m := NewManager(cfg)

	err := m.DisableExtension("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent extension")
	}
}

func TestManager_ToolSets_Empty(t *testing.T) {
	cfg := &config.WukongConfig{}
	m := NewManager(cfg)

	ts := m.ToolSets()
	if len(ts) != 0 {
		t.Errorf("expected 0 tool sets, got %d", len(ts))
	}
}

func TestManager_ToolSets_SkipsNil(t *testing.T) {
	cfg := &config.WukongConfig{
		Extensions: []config.ExtensionConfig{
			{Name: "builtin_a", Type: "builtin", Enabled: true},
			{Name: "builtin_b", Type: "builtin", Enabled: true},
		},
	}
	m := NewManager(cfg)

	// Simulate a nil placeholder (apps, code_mode, top_of_mind)
	m.mu.Lock()
	m.toolSets["builtin_a"] = nil
	m.toolSets["builtin_b"] = &mockToolSet{name: "builtin_b"}
	m.mu.Unlock()

	ts := m.ToolSets()
	if len(ts) != 1 {
		t.Errorf("expected 1 tool set (skipping nil), got %d", len(ts))
	}
}

func TestManager_Close(t *testing.T) {
	cfg := &config.WukongConfig{}
	m := NewManager(cfg)

	// Register a mock tool set
	m.mu.Lock()
	m.toolSets["test"] = &mockToolSet{name: "test"}
	m.mu.Unlock()

	err := m.Close()
	if err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	// After close, tool sets should be empty
	m.mu.RLock()
	count := len(m.toolSets)
	m.mu.RUnlock()
	if count != 0 {
		t.Errorf("expected 0 tool sets after close, got %d", count)
	}
}

func TestExtensionStatus_Constants(t *testing.T) {
	tests := []struct {
		status ExtensionStatus
		value  string
	}{
		{StatusEnabled, "enabled"},
		{StatusDisabled, "disabled"},
		{StatusError, "error"},
		{StatusLoading, "loading"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.value {
			t.Errorf(
				"expected %q, got %q", tt.value, tt.status,
			)
		}
	}
}
