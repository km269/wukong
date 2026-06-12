package builtin

import (
	"context"
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestDeveloperToolSet_Tools(t *testing.T) {
	ts := NewDeveloperToolSet()

	ctx := context.Background()
	tools := ts.Tools(ctx)

	expectedTools := []string{
		"file_read",
		"file_write",
		"file_replace",
		"command_execute",
		"code_search",
		"directory_list",
	}

	if len(tools) != len(expectedTools) {
		t.Errorf(
			"expected %d tools, got %d",
			len(expectedTools), len(tools),
		)
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		decl := tool.Declaration()
		if decl == nil {
			t.Error("tool declaration should not be nil")
			continue
		}
		toolNames[decl.Name] = true
	}

	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestDeveloperToolSet_Init(t *testing.T) {
	ts := NewDeveloperToolSet()
	if ts.inited {
		t.Error("tool set should not be inited before Init")
	}

	err := ts.Init(context.Background())
	if err != nil {
		t.Errorf("Init should succeed: %v", err)
	}
	if !ts.inited {
		t.Error("tool set should be inited after Init")
	}
}

func TestDeveloperToolSet_Close(t *testing.T) {
	ts := NewDeveloperToolSet()
	err := ts.Close()
	if err != nil {
		t.Errorf("Close should succeed: %v", err)
	}
	if !ts.closed {
		t.Error("tool set should be closed after Close")
	}
}

func TestDeveloperToolSet_Name(t *testing.T) {
	ts := NewDeveloperToolSet()
	if ts.Name() != "developer" {
		t.Errorf(
			"expected name 'developer', got %q", ts.Name(),
		)
	}
}

func TestMemoryToolSet_New(t *testing.T) {
	cfg := &config.WukongConfig{}
	ts := NewMemoryToolSet(cfg)
	if ts == nil {
		t.Fatal("expected non-nil tool set")
	}
	if ts.Name() != "memory" {
		t.Errorf(
			"expected name 'memory', got %q", ts.Name(),
		)
	}

	// Before injection, Tools should return nil/empty
	tools := ts.Tools(context.Background())
	if len(tools) != 0 {
		t.Errorf(
			"expected 0 tools before injection, got %d",
			len(tools),
		)
	}
}

func TestMemoryToolSet_InitClose(t *testing.T) {
	cfg := &config.WukongConfig{}
	ts := NewMemoryToolSet(cfg)

	err := ts.Init(context.Background())
	if err != nil {
		t.Errorf("Init should succeed: %v", err)
	}
	if !ts.inited {
		t.Error("should be inited after Init")
	}

	err = ts.Close()
	if err != nil {
		t.Errorf("Close should succeed: %v", err)
	}
	if !ts.closed {
		t.Error("should be closed after Close")
	}
}

func TestMemoryToolSet_SetMemoryService_InvalidType(t *testing.T) {
	cfg := &config.WukongConfig{}
	ts := NewMemoryToolSet(cfg)

	// Pass an invalid type - should not panic
	ts.SetMemoryService("not a memory service", "app", "user")

	// Tools should still be empty
	tools := ts.Tools(context.Background())
	if len(tools) != 0 {
		t.Errorf(
			"expected 0 tools after invalid injection, got %d",
			len(tools),
		)
	}
}
