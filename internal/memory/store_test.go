package memory

import (
	"testing"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
)

func TestNewMemoryManager_MemoryBackend(t *testing.T) {
	cfg := &config.MemoryConfig{
		Backend:     "memory",
		MaxMemories: 50,
		AutoExtract: false,
	}
	mgr, err := NewMemoryManager(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewMemoryManager(memory) failed: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil MemoryManager")
	}
	if mgr.svc == nil {
		t.Fatal("expected non-nil Service")
	}
	if mgr.cfg != cfg {
		t.Error("cfg should match input config")
	}

	svc := mgr.Service()
	if svc == nil {
		t.Fatal("Service() returned nil")
	}

	tools := mgr.Tools()
	if tools == nil {
		t.Error("Tools() should not return nil")
	}

	if err := mgr.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestNewMemoryManager_UnsupportedBackend(t *testing.T) {
	cfg := &config.MemoryConfig{
		Backend: "redis",
	}
	_, err := NewMemoryManager(cfg, nil, nil)
	if err == nil {
		t.Error("expected error for unsupported backend")
	}
}

func TestNewMemoryManager_SQLiteNoPool(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/mem_test.db"

	cfg := &config.MemoryConfig{
		Backend:     "sqlite",
		DBPath:      dbPath,
		MaxMemories: 20,
		AutoExtract: false,
	}
	mgr, err := NewMemoryManager(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewMemoryManager(sqlite) failed: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil MemoryManager")
	}

	if err := mgr.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestNewMemoryManager_SQLiteWithPool(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/mem_shared.db"
	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	cfg := &config.MemoryConfig{
		Backend:     "sqlite",
		DBPath:      dbPath,
		MaxMemories: 30,
		AutoExtract: false,
	}
	mgr, err := NewMemoryManager(cfg, nil, pool)
	if err != nil {
		t.Fatalf("NewMemoryManager(sqlite+pool) failed: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil MemoryManager")
	}

	if mgr.pool != pool {
		t.Error("expected shared pool reference")
	}

	if err := mgr.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestNewMemoryManager_AutoExtractMemoryBackend(t *testing.T) {
	// AutoExtract with nil model should still work (no extractor)
	cfg := &config.MemoryConfig{
		Backend:     "memory",
		MaxMemories: 30,
		AutoExtract: true,
	}
	mgr, err := NewMemoryManager(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewMemoryManager(auto_extract, nil model) failed: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil MemoryManager")
	}
	if err := mgr.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}
