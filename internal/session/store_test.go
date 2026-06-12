package session

import (
	"testing"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
)

func TestNewSessionService_MemoryBackend(t *testing.T) {
	cfg := &config.SessionConfig{
		Backend:       "memory",
		EventLimit:    100,
		TTL:           0,
	}
	svc, err := NewSessionService(cfg, nil)
	if err != nil {
		t.Fatalf("NewSessionService(memory) failed: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil SessionService")
	}
	if svc.Service == nil {
		t.Fatal("expected non-nil underlying Service")
	}

	// Close should succeed
	if err := svc.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestNewSessionService_UnsupportedBackend(t *testing.T) {
	cfg := &config.SessionConfig{
		Backend: "postgres",
	}
	_, err := NewSessionService(cfg, nil)
	if err == nil {
		t.Error("expected error for unsupported backend")
	}
}

func TestNewSessionService_SQLiteNoPool(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	cfg := &config.SessionConfig{
		Backend:    "sqlite",
		DBPath:     dbPath,
		EventLimit: 100,
	}
	svc, err := NewSessionService(cfg, nil)
	if err != nil {
		t.Fatalf("NewSessionService(sqlite) failed: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil SessionService")
	}

	// Close should succeed
	if err := svc.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestNewSessionService_SQLiteWithPool(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test_shared.db"
	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	cfg := &config.SessionConfig{
		Backend:    "sqlite",
		DBPath:     dbPath,
		EventLimit: 50,
	}
	svc, err := NewSessionService(cfg, pool)
	if err != nil {
		t.Fatalf("NewSessionService(sqlite+pool) failed: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil SessionService")
	}

	// The pool reference should be the same
	if svc.pool != pool {
		t.Error("expected shared pool reference")
	}

	if err := svc.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestNewSessionService_WithTTL(t *testing.T) {
	cfg := &config.SessionConfig{
		Backend:       "memory",
		EventLimit:    200,
		TTL:           3600 * 1000000000, // 1 hour in ns
	}
	svc, err := NewSessionService(cfg, nil)
	if err != nil {
		t.Fatalf("NewSessionService with TTL failed: %v", err)
	}
	if svc == nil {
		t.Fatal("expected non-nil SessionService")
	}
	if err := svc.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}
