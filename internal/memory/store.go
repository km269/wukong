// Package memory provides long-term memory management for wukong.
// It wraps tRPC-Agent-Go's memory service to store user preferences
// and facts across sessions.
package memory

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// MemoryManager wraps the memory service with config-driven creation.
type MemoryManager struct {
	svc memory.Service
	cfg *config.MemoryConfig
}

// NewMemoryManager creates a new memory manager based on configuration.
func NewMemoryManager(cfg *config.MemoryConfig) (*MemoryManager, error) {
	svc, err := createService(cfg)
	if err != nil {
		return nil, fmt.Errorf("create memory service: %w", err)
	}
	return &MemoryManager{svc: svc, cfg: cfg}, nil
}

// Service returns the underlying memory service.
func (m *MemoryManager) Service() memory.Service {
	return m.svc
}

// Tools returns the memory management tools for agent integration.
func (m *MemoryManager) Tools() []tool.Tool {
	return m.svc.Tools()
}

func createService(cfg *config.MemoryConfig) (memory.Service, error) {
	switch cfg.Backend {
	case "sqlite":
		return newSQLiteService(cfg)
	case "memory":
		return newInMemoryService(cfg), nil
	default:
		return nil, fmt.Errorf(
			"unsupported memory backend: %s", cfg.Backend,
		)
	}
}

// newSQLiteService creates a SQLite-backed memory service.
func newSQLiteService(cfg *config.MemoryConfig) (memory.Service, error) {
	db, err := sql.Open("sqlite3", cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	opts := []memorysqlite.ServiceOpt{
		memorysqlite.WithMemoryLimit(cfg.MaxMemories),
	}

	svc, err := memorysqlite.NewService(db, opts...)
	if err != nil {
		return nil, fmt.Errorf("create sqlite memory: %w", err)
	}
	return svc, nil
}

// newInMemoryService creates an in-memory memory service.
func newInMemoryService(cfg *config.MemoryConfig) memory.Service {
	return memoryinmemory.NewMemoryService(
		memoryinmemory.WithMemoryLimit(cfg.MaxMemories),
	)
}
