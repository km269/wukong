// Package memory provides long-term memory management for wukong.
// It wraps tRPC-Agent-Go's memory service to store user preferences
// and facts across sessions.
//
// The memory system supports two modes:
//   - Auto Extract: LLM-based automatic memory extraction from conversations
//   - Manual Tools: Agent-initiated memory_add/search/update/delete/load/clear
//
// Shutdown guarantees:
//   - Runner is closed first, preventing new extraction jobs
//   - Workers drain gracefully with a configurable timeout
//   - DB is checkpointed (WAL) before close to prevent data loss
package memory

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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

// shutdownTimeout is the maximum wait for in-flight extraction
// jobs to complete during graceful shutdown.
const shutdownTimeout = 5 * time.Second

// MemoryManager wraps the memory service with config-driven creation
// and graceful shutdown support.
type MemoryManager struct {
	svc      memory.Service
	cfg      *config.MemoryConfig
	pool     *util.DatabasePool
	ownsPool bool

	// shutdown coordination
	active    sync.WaitGroup // tracks in-flight extraction jobs
	shutdown  sync.WaitGroup // signals shutdown completion
	isClosing bool
	mu        sync.Mutex
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
	mm := &MemoryManager{
		cfg:      cfg,
		ownsPool: ownsPool,
	}

	svc, p, err := createService(cfg, extractorModel, pool)
	if err != nil {
		return nil, fmt.Errorf("create memory service: %w", err)
	}
	mm.svc = svc
	mm.pool = p

	util.Logger.Info("memory manager started",
		slog.String("backend", cfg.Backend),
		slog.Bool("auto_extract", cfg.AutoExtract),
		slog.Bool("extractor_model_ready", extractorModel != nil),
		slog.Int("max_memories", cfg.MaxMemories))

	// [记忆健康] 启动时报告现有记忆状态。
	go mm.logMemoryHealth()

	// Wrap extraction jobs to track in-flight count.
	if cfg.AutoExtract && extractorModel != nil {
		mm.svc = &trackingMemoryService{
			Service: svc,
			active:  &mm.active,
			closing: &mm.isClosing,
			mu:      &mm.mu,
		}
	}

	return mm, nil
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
func (m *MemoryManager) Tools() []tool.Tool {
	return m.svc.Tools()
}

// Close releases resources owned by the memory manager.
//
// Shutdown sequence:
//  1. Marks the manager as closing (rejects new extraction jobs)
//  2. Waits for in-flight jobs to complete (up to shutdownTimeout)
//  3. Closes the underlying service to stop worker goroutines
//  4. If this manager owns the DB pool, closes it
//
// In shared-pool mode, the raw service is NOT closed (it would close
// the shared DB). Instead, workers drain naturally after the runner
// stops producing new jobs. The shared DB is closed by the caller
// via DBPoolClose in the close chain.
func (m *MemoryManager) Close() error {
	if m.svc == nil {
		return nil
	}

	m.mu.Lock()
	m.isClosing = true
	m.mu.Unlock()

	// Wait for in-flight jobs to complete with timeout.
	done := make(chan struct{})
	go func() {
		m.active.Wait()
		close(done)
	}()
	select {
	case <-done:
		util.Logger.Debug("all in-flight memory extraction jobs completed")
	case <-time.After(shutdownTimeout):
		util.Logger.Warn("memory extraction shutdown timeout, " +
			"proceeding with close (pending jobs may be lost)")
	}

	var err error
	if m.ownsPool {
		// Owns the DB pool — safe to close service fully.
		err = m.svc.Close()
		if m.pool != nil {
			if poolErr := m.pool.Close(); poolErr != nil && err == nil {
				err = poolErr
			}
		}
	} else {
		// Shared pool — DON'T close the raw service (it would
		// close the shared DB). Use the noCloseDBWrapper instead
		// which is a no-op for Close(). Workers have already
		// drained thanks to the WaitGroup above.
		wrapped := m.Service()
		_ = wrapped.Close() // no-op in shared-pool mode
		util.Logger.Debug("memory service closed (shared-pool mode, " +
			"workers drained, DB preserved)")
	}

	m.shutdown.Wait()
	return err
}

// logMemoryHealth reports the current memory store health in
// a background goroutine. Reports total count and oldest/newest
// timestamps to help diagnose stale or missing memories.
func (m *MemoryManager) logMemoryHealth() {
	ctx := context.Background()
	entries, err := m.Service().ReadMemories(
		ctx,
		memory.UserKey{AppName: "wukong-app", UserID: "*"},
		0,
	)
	if err != nil {
		util.Logger.Debug("memory: health check failed",
			"error", err.Error())
		return
	}

	if len(entries) == 0 {
		util.Logger.Info("memory: store is empty (cold start)")
		return
	}

	var oldest, newest time.Time
	for i, e := range entries {
		if i == 0 {
			oldest = e.CreatedAt
			newest = e.UpdatedAt
		} else {
			if e.CreatedAt.Before(oldest) {
				oldest = e.CreatedAt
			}
			if e.UpdatedAt.After(newest) {
				newest = e.UpdatedAt
			}
		}
	}
	util.Logger.Info("memory: health report",
		"total", len(entries),
		"oldest", oldest.Format(time.RFC3339),
		"newest", newest.Format(time.RFC3339),
		"max", m.cfg.MaxMemories,
	)
}

// CleanMemoriesByAge removes memories older than the given TTL
// for the specified user. Returns the number of deleted entries.
func (m *MemoryManager) CleanMemoriesByAge(
	ctx context.Context,
	userKey memory.UserKey,
	ttl time.Duration,
) (int, error) {
	entries, err := m.Service().ReadMemories(ctx, userKey, 0)
	if err != nil {
		return 0, fmt.Errorf("read for cleanup: %w", err)
	}

	cutoff := time.Now().Add(-ttl)
	var deleted int
	for _, e := range entries {
		if e.UpdatedAt.Before(cutoff) {
			memKey := memory.Key{
				AppName:  userKey.AppName,
				UserID:   userKey.UserID,
				MemoryID: e.ID,
			}
			if err := m.Service().DeleteMemory(ctx, memKey); err != nil {
				util.Logger.Warn("memory: cleanup delete failed",
					"id", e.ID, "error", err.Error())
			} else {
				deleted++
			}
		}
	}
	if deleted > 0 {
		util.Logger.Info("memory: cleaned old memories",
			"deleted", deleted,
			"cutoff", cutoff.Format(time.RFC3339),
		)
	}
	return deleted, nil
}

// --- trackingMemoryService wraps a memory.Service to track active jobs ---

type trackingMemoryService struct {
	memory.Service
	active  *sync.WaitGroup
	closing *bool
	mu      *sync.Mutex
}

func (t *trackingMemoryService) EnqueueAutoMemoryJob(
	ctx context.Context, sess *session.Session,
) error {
	t.mu.Lock()
	if *t.closing {
		t.mu.Unlock()
		util.Logger.Debug("rejecting memory extraction job — manager is closing")
		return nil
	}
	t.mu.Unlock()

	t.active.Add(1)
	defer t.active.Done()

	// Use context.Background() to give extraction its own timeout
	// independent of the caller's (likely cancelled) context.
	return t.Service.EnqueueAutoMemoryJob(context.Background(), sess)
}

// --- noCloseDBWrapper (unchanged from original) ---

type noCloseDBWrapper struct {
	memory.Service
}

func (w *noCloseDBWrapper) Close() error {
	util.Logger.Debug("memory service Close() skipped to protect shared DB connection")
	return nil
}

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

// --- service creation ---

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

func newSQLiteService(
	cfg *config.MemoryConfig,
	extractorModel model.Model,
	pool *util.DatabasePool,
) (memory.Service, *util.DatabasePool, error) {
	ownsPool := pool == nil
	if ownsPool {
		dbPath := config.ResolvePath(cfg.DBPath)
		pool = util.NewDatabasePool(dbPath)
	}

	db, err := pool.GetDB()
	if err != nil {
		return nil, nil, fmt.Errorf("get db: %w", err)
	}

	opts := []memorysqlite.ServiceOpt{
		memorysqlite.WithMemoryLimit(cfg.MaxMemories),
	}

	// Always enable all memory tools so manual memory operations
	// work regardless of auto_extract status.
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

	if cfg.AutoExtract && extractorModel != nil {
		extOpts := []extractor.Option{}
		if cfg.ExtractorPrompt != "" {
			extOpts = append(extOpts,
				extractor.WithPrompt(cfg.ExtractorPrompt))
		}
		ext := extractor.NewExtractor(extractorModel, extOpts...)
		opts = append(opts, memorysqlite.WithExtractor(ext))

		timeout := cfg.ExtractTimeout
		if timeout <= 0 {
			timeout = 600 * time.Second // 10min for local models
		}
		opts = append(opts,
			memorysqlite.WithMemoryJobTimeout(timeout),
			memorysqlite.WithAsyncMemoryNum(3),
		)

		util.Logger.Info("auto memory extraction enabled",
			slog.String("backend", cfg.Backend),
			slog.Int("max_memories", cfg.MaxMemories),
			slog.String("job_timeout", timeout.String()),
		)
	} else if cfg.AutoExtract {
		util.Logger.Warn("auto memory extraction disabled: "+
			"no extractor model available. "+
			"Manual memory tools are still available.",
			slog.String("backend", cfg.Backend))
	}

	svc, err := memorysqlite.NewService(db, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create sqlite memory: %w", err)
	}
	return svc, pool, nil
}

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
		opts = append(opts, memoryinmemory.WithExtractor(ext))
	}

	return memoryinmemory.NewMemoryService(opts...)
}
