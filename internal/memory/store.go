// Package memory provides long-term memory management for wukong.
// It wraps tRPC-Agent-Go's memory service to store user preferences
// and facts across sessions.
package memory

import (
	"context"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// MemoryManager wraps the memory service with config-driven creation.
type MemoryManager struct {
	svc  memory.Service
	cfg  *config.MemoryConfig
	pool *util.DatabasePool
}

// NewMemoryManager creates a new memory manager based on configuration.
// It accepts an optional shared DatabasePool; if nil and the backend is
// SQLite, it creates its own pool from the config path.
// If auto_extract is enabled, an extractor will be configured to
// automatically extract memories from conversations.
func NewMemoryManager(
	cfg *config.MemoryConfig,
	extractorModel model.Model,
	pool *util.DatabasePool,
) (*MemoryManager, error) {
	svc, p, err := createService(cfg, extractorModel, pool)
	if err != nil {
		return nil, fmt.Errorf("create memory service: %w", err)
	}
	return &MemoryManager{svc: svc, cfg: cfg, pool: p}, nil
}

// Service returns the underlying memory service.
func (m *MemoryManager) Service() memory.Service {
	return m.svc
}

// Tools returns the memory management tools from the tRPC memory service.
// These are the standard tools: memory_add, memory_search, memory_delete,
// memory_update, memory_load, memory_clear.
func (m *MemoryManager) Tools() []tool.Tool {
	return m.svc.Tools()
}

// EnqueueAutoMemoryJob triggers automatic memory extraction from the
// current session transcript. This should be called after each
// conversation turn when auto_extract is enabled.
func (m *MemoryManager) EnqueueAutoMemoryJob(
	ctx context.Context, sess *session.Session,
) error {
	return m.svc.EnqueueAutoMemoryJob(ctx, sess)
}

// Close releases resources owned by the memory manager.
// Note: the database connection is managed by the shared pool;
// only the service-level resources are released here.
func (m *MemoryManager) Close() error {
	if m.svc != nil {
		return m.svc.Close()
	}
	return nil
}

func createService(
	cfg *config.MemoryConfig,
	extractorModel model.Model,
	pool *util.DatabasePool,
) (memory.Service, *util.DatabasePool, error) {
	switch cfg.Backend {
	case "sqlite":
		return newSQLiteService(cfg, extractorModel, pool)
	case "memory":
		return newInMemoryService(cfg, extractorModel), nil, nil
	default:
		return nil, nil, fmt.Errorf(
			"unsupported memory backend: %s", cfg.Backend,
		)
	}
}

// newSQLiteService creates a SQLite-backed memory service.
// When auto_extract is enabled and an extractorModel is provided,
// it configures automatic memory extraction from conversations.
func newSQLiteService(
	cfg *config.MemoryConfig,
	extractorModel model.Model,
	pool *util.DatabasePool,
) (memory.Service, *util.DatabasePool, error) {
	if pool == nil {
		var err error
		dbPath := config.ResolvePath(cfg.DBPath)
		pool = util.NewDatabasePool(dbPath)
		defer func() {
			if err != nil {
				pool.Close()
			}
		}()
	}

	db, err := pool.GetDB()
	if err != nil {
		return nil, nil, fmt.Errorf("get db: %w", err)
	}

	opts := []memorysqlite.ServiceOpt{
		memorysqlite.WithMemoryLimit(cfg.MaxMemories),
	}

	// Enable auto memory extraction if configured
	if cfg.AutoExtract && extractorModel != nil {
		// Enable and expose the standard memory tools to the agent.
		// WithToolEnabled must come BEFORE WithExtractor because
		// ApplyAutoModeDefaults (triggered by WithExtractor) disables
		// memory_load and memory_clear by default. Explicitly enabling
		// them here marks them as user-set and prevents the override.
		opts = append(opts,
			memorysqlite.WithToolEnabled("memory_add", true),
			memorysqlite.WithToolEnabled("memory_search", true),
			memorysqlite.WithToolEnabled("memory_update", true),
			memorysqlite.WithToolEnabled("memory_delete", true),
			memorysqlite.WithToolEnabled("memory_load", true),
			memorysqlite.WithToolEnabled("memory_clear", true),
		)
		opts = append(opts,
			memorysqlite.WithAutoMemoryExposedTools(
				"memory_add", "memory_search", "memory_update",
				"memory_delete", "memory_load", "memory_clear",
			),
		)

		// Configure the extractor with a dedicated model
		ext := extractor.NewExtractor(extractorModel)
		opts = append(opts,
			memorysqlite.WithExtractor(ext),
		)

		// Set memory job timeout
		opts = append(opts,
			memorysqlite.WithMemoryJobTimeout(30*time.Second),
		)

		// Set async worker count
		opts = append(opts,
			memorysqlite.WithAsyncMemoryNum(3),
		)
	}

	svc, err := memorysqlite.NewService(db, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create sqlite memory: %w", err)
	}
	return svc, pool, nil
}

// newInMemoryService creates an in-memory memory service.
func newInMemoryService(
	cfg *config.MemoryConfig,
	extractorModel model.Model,
) memory.Service {
	opts := []memoryinmemory.ServiceOpt{
		memoryinmemory.WithMemoryLimit(cfg.MaxMemories),
	}

	if cfg.AutoExtract && extractorModel != nil {
		ext := extractor.NewExtractor(extractorModel)
		opts = append(opts,
			memoryinmemory.WithExtractor(ext),
		)
	}

	return memoryinmemory.NewMemoryService(opts...)
}
