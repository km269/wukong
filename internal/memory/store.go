// Package memory provides long-term memory management for wukong.
// It wraps tRPC-Agent-Go's memory service to store user preferences
// and facts across sessions.
package memory

import (
	"context"
	"fmt"
	"log/slog"
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
	svc      memory.Service
	cfg      *config.MemoryConfig
	pool     *util.DatabasePool
	ownsPool bool
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
	ownsPool := pool == nil && cfg.Backend == "sqlite"
	svc, p, err := createService(cfg, extractorModel, pool)
	if err != nil {
		return nil, fmt.Errorf("create memory service: %w", err)
	}
	return &MemoryManager{
		svc:      svc,
		cfg:      cfg,
		pool:     p,
		ownsPool: ownsPool,
	}, nil
}

// Service returns the underlying memory service.
// When using a shared DatabasePool (ownsPool=false), the service is
// wrapped so that Close() on the wrapper does not close the shared
// database connection.
func (m *MemoryManager) Service() memory.Service {
	if m.cfg.Backend == "sqlite" && !m.ownsPool {
		return &noCloseDBWrapper{Service: m.svc}
	}
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
//
// IMPORTANT: The context passed by the tRPC runner may already be
// cancelled (deadline exceeded) by the time the async worker picks up
// the job. To give auto memory extraction its own independent timeout
// (configured via extract_timeout), we replace the caller's context
// with context.Background().
func (m *MemoryManager) EnqueueAutoMemoryJob(
	_ context.Context, sess *session.Session,
) error {
	return m.svc.EnqueueAutoMemoryJob(context.Background(), sess)
}

// Close releases resources owned by the memory manager.
// If the manager owns its database pool (created internally when no
// shared pool was provided), the service and DB connection are both
// closed. Otherwise, only the auto memory workers are stopped and
// the shared DB is left untouched.
func (m *MemoryManager) Close() error {
	if m.svc == nil {
		return nil
	}
	// Use the wrapper returned by Service() for closing. For shared-pool
	// setups, Service() returns a noCloseDBWrapper whose Close() is a
	// no-op (the underlying sqlite Service would close the shared DB if
	// we called it directly, because sqlite.NewService owns the passed-in
	// *sql.DB).
	//
	// The auto-memory workers will drain naturally because:
	// 1. The runner is already closed (no new EnqueueAutoMemoryJob calls)
	// 2. Pending jobs complete or time out on their own
	// 3. DBPoolClose runs last, ensuring all writes are flushed
	wrapped := m.Service()
	err := wrapped.Close()
	// If we own the pool, close it after the service is done.
	if m.ownsPool && m.pool != nil {
		if poolErr := m.pool.Close(); poolErr != nil && err == nil {
			err = poolErr
		}
	}
	return err
}

// noCloseDBWrapper wraps a memory.Service to prevent Close() from
// closing the shared database connection. The database is owned by
// the shared DatabasePool and must not be closed by individual
// service consumers.
//
// Close() is a no-op because:
//  1. The sqlite.Service was created with an external *sql.DB via
//     NewService(db, ...). According to its documentation: "The service
//     owns the passed-in db and will close it in Close()." Calling
//     Close() on the underlying service would close the shared DB,
//     breaking all other services (session, todo, recall).
//  2. The auto memory workers drain naturally after the runner stops
//     sending new EnqueueAutoMemoryJob calls. Pending jobs complete
//     or time out. The 100ms delay before DBPoolClose in the closeFn
//     ensures in-flight writes have a chance to complete.
//  3. DBPoolClose runs last and properly checkpoints the WAL.
//
// All other methods are delegated directly.
type noCloseDBWrapper struct {
	memory.Service
}

func (w *noCloseDBWrapper) Close() error {
	util.Logger.Debug("memory service Close() skipped to protect shared DB connection")
	return nil
}

// EnqueueAutoMemoryJob wraps the underlying call and logs errors at
// Warn level so extraction failures are visible with default log level.
// The tRPC runner logs these at Debug level, which is invisible to users.
//
// Uses context.Background() to give auto memory extraction its own
// independent timeout; the caller's context may already be cancelled
// by the time the async worker processes the job.
func (w *noCloseDBWrapper) EnqueueAutoMemoryJob(
	_ context.Context, sess *session.Session,
) error {
	err := w.Service.EnqueueAutoMemoryJob(context.Background(), sess)
	if err != nil {
		util.Logger.Warn("auto memory extraction failed",
			slog.String("error", err.Error()))
	}
	return err
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
	ownsPool := pool == nil
	if ownsPool {
		dbPath := config.ResolvePath(cfg.DBPath)
		pool = util.NewDatabasePool(dbPath)
		// NOTE: Do NOT defer pool.Close() here. The caller
		// (MemoryManager) takes ownership of the pool and will
		// close it via MemoryManager.Close(). Closing it here
		// would make the returned *sql.DB invalid.
	}

	db, err := pool.GetDB()
	if err != nil {
		return nil, nil, fmt.Errorf("get db: %w", err)
	}

	opts := []memorysqlite.ServiceOpt{
		memorysqlite.WithMemoryLimit(cfg.MaxMemories),
	}

	// Always enable and expose all memory tools so the agent can
	// manually call memory_add, memory_search, etc. regardless of
	// whether auto_extract is configured. This ensures manual memory
	// management always works even when the extractor model is
	// unavailable.
	//
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

	// Enable auto memory extraction if configured and a model is
	// available. Without an extractor model, auto extraction cannot
	// work, but manual memory tools remain available.
	if cfg.AutoExtract && extractorModel != nil {
		extOpts := []extractor.Option{}
		if cfg.ExtractorPrompt != "" {
			extOpts = append(extOpts,
				extractor.WithPrompt(cfg.ExtractorPrompt))
		}
		ext := extractor.NewExtractor(extractorModel, extOpts...)
		opts = append(opts,
			memorysqlite.WithExtractor(ext),
		)

		// Set memory job timeout from config (default: 60s).
		timeout := cfg.ExtractTimeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		opts = append(opts,
			memorysqlite.WithMemoryJobTimeout(timeout),
		)

		// Set async worker count
		opts = append(opts,
			memorysqlite.WithAsyncMemoryNum(3),
		)

		util.Logger.Info("auto memory extraction enabled",
			slog.String("backend", cfg.Backend),
			slog.Int("max_memories", cfg.MaxMemories))
	} else if cfg.AutoExtract {
		util.Logger.Warn("auto memory extraction disabled: "+
			"no extractor model available. "+
			"Manual memory tools (memory_add, memory_search) "+
			"are still available. Check provider configuration.",
			slog.String("backend", cfg.Backend))
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
		extOpts := []extractor.Option{}
		if cfg.ExtractorPrompt != "" {
			extOpts = append(extOpts,
				extractor.WithPrompt(cfg.ExtractorPrompt))
		}
		ext := extractor.NewExtractor(extractorModel, extOpts...)
		opts = append(opts,
			memoryinmemory.WithExtractor(ext),
		)
	}

	return memoryinmemory.NewMemoryService(opts...)
}
