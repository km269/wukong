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
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite"

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
	svc             memory.Service
	cfg             *config.MemoryConfig
	pool            *util.DatabasePool
	ownsPool        bool
	metadataManager *MetadataManager

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

	// Initialize metadata manager for enhanced scoring and reference tracking.
	if mm.pool != nil {
		db, err := mm.pool.GetDB()
		if err == nil {
			mm.metadataManager = NewMetadataManager(db)
			util.Logger.Info("memory: metadata manager initialized")
		} else {
			util.Logger.Warn("memory: failed to initialize metadata manager",
				slog.String("error", err.Error()))
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
// a background goroutine. Uses a timeout to avoid blocking
// startup if the database is locked.
func (m *MemoryManager) logMemoryHealth() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
// Uses dynamic TTL based on reference frequency when metadata manager is available.
// Deprecated: use SmartCleanup instead for capacity-aware eviction.
func (m *MemoryManager) CleanMemoriesByAge(
	ctx context.Context,
	userKey memory.UserKey,
	ttl time.Duration,
) (int, error) {
	entries, err := m.Service().ReadMemories(ctx, userKey, 0)
	if err != nil {
		return 0, fmt.Errorf("read for cleanup: %w", err)
	}

	now := time.Now()
	var deleted int
	for _, e := range entries {
		// Calculate dynamic TTL based on reference frequency.
		dynamicTTL := ttl
		if m.metadataManager != nil && m.cfg.DynamicTTL {
			dynamicTTL = m.metadataManager.AdjustTTLByReference(ctx, e.ID, userKey.UserID, ttl)
		}
		cutoff := now.Add(-dynamicTTL)

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
				if m.metadataManager != nil {
					_ = m.metadataManager.DeleteMetadata(ctx, e.ID, userKey.UserID)
				}
			}
		}
	}
	if deleted > 0 {
		util.Logger.Info("memory: cleaned old memories",
			"deleted", deleted,
			"base_ttl", ttl.String(),
		)
	}
	return deleted, nil
}

// RecordMemoryReference increments the reference count for the given memory.
// This should be called whenever a memory is read/recalled to track usage frequency.
func (m *MemoryManager) RecordMemoryReference(
	ctx context.Context,
	userKey memory.UserKey,
	memoryID string,
) error {
	if m.metadataManager == nil {
		return nil
	}
	return m.metadataManager.RecordReference(ctx, memoryID, userKey.UserID)
}

// BatchRecordMemoryReferences increments the reference count for multiple memories.
func (m *MemoryManager) BatchRecordMemoryReferences(
	ctx context.Context,
	userKey memory.UserKey,
	memoryIDs []string,
) error {
	if m.metadataManager == nil {
		return nil
	}
	return m.metadataManager.BatchRecordReferences(ctx, memoryIDs, userKey.UserID)
}

// MarkMemoryImportance sets the importance level for a memory.
// Valid importance values: "high", "medium", "low".
func (m *MemoryManager) MarkMemoryImportance(
	ctx context.Context,
	userKey memory.UserKey,
	memoryID string,
	importance string,
) error {
	if m.metadataManager == nil {
		return nil
	}
	return m.metadataManager.MarkImportance(ctx, memoryID, userKey.UserID, importance)
}

// MetadataManager returns the underlying metadata manager.
// Returns nil if not initialized.
func (m *MemoryManager) MetadataManager() *MetadataManager {
	return m.metadataManager
}

// SmartCleanup performs capacity-aware memory eviction with
// importance scoring. Strategy:
//  1. If under 80% capacity: only delete expired (>TTL).
//  2. If over 80% capacity: evict lowest-score memories down to
//     60% capacity, using a combined score of:
//     - 40% recency (freshness)
//     - 30% reference frequency (how often the memory has been recalled)
//     - 20% importance marking (high/medium/low)
//     - 10% content length (substantive memories preserved)
//  3. Returns the number of deleted entries.
//
// This ensures transient or short memories are evicted first,
// while recently updated and frequently referenced memories are preserved.
func (m *MemoryManager) SmartCleanup(
	ctx context.Context,
	userKey memory.UserKey,
	ttl time.Duration,
) (int, error) {
	if m.cfg.MaxMemories <= 0 {
		return 0, nil
	}

	entries, err := m.Service().ReadMemories(ctx, userKey, 0)
	if err != nil {
		return 0, fmt.Errorf("smart cleanup read: %w", err)
	}

	total := len(entries)
	if total == 0 {
		return 0, nil
	}

	// Under 80% capacity: only delete expired.
	keepTarget := m.cfg.MaxMemories * 60 / 100
	softLimit := m.cfg.MaxMemories * 80 / 100

	if total <= softLimit {
		return m.CleanMemoriesByAge(ctx, userKey, ttl)
	}

	// Over 80% capacity: score all entries and evict lowest-scoring
	// down to 60% of max capacity.
	now := time.Now()
	maxAge := 365 * 24 * time.Hour
	if ttl > maxAge {
		maxAge = ttl
	}

	// Preload metadata for all entries if metadata manager is available.
	var metadataMap map[string]*MemoryMetadata
	if m.metadataManager != nil {
		memoryIDs := make([]string, 0, len(entries))
		for _, e := range entries {
			memoryIDs = append(memoryIDs, e.ID)
		}
		metadataMap, _ = m.metadataManager.BatchGetMetadata(ctx, memoryIDs, userKey.UserID)
	}

	type scored struct {
		entry      *memory.Entry
		score      float64
		recency    float64
		reference  float64
		importance float64
		length     float64
	}
	var scoredEntries []scored

	for _, e := range entries {
		// Get metadata for this entry.
		var md *MemoryMetadata
		if metadataMap != nil {
			md = metadataMap[e.ID]
		}
		if md == nil {
			md = &MemoryMetadata{
				MemoryID:       e.ID,
				UserID:         userKey.UserID,
				ReferenceCount: 0,
				Importance:     ImportanceLow,
			}
		}

		// Calculate dynamic TTL based on reference frequency.
		dynamicTTL := ttl
		if m.metadataManager != nil && m.cfg.DynamicTTL {
			dynamicTTL = m.metadataManager.AdjustTTLByReference(ctx, e.ID, userKey.UserID, ttl)
		}
		cutoff := now.Add(-dynamicTTL)

		// Always evict expired entries regardless of score.
		if e.UpdatedAt.Before(cutoff) {
			memKey := memory.Key{
				AppName:  userKey.AppName,
				UserID:   userKey.UserID,
				MemoryID: e.ID,
			}
			if delErr := m.Service().DeleteMemory(ctx, memKey); delErr != nil {
				util.Logger.Warn("memory: smart cleanup delete failed",
					"id", e.ID, "error", delErr.Error())
			}
			if m.metadataManager != nil {
				_ = m.metadataManager.DeleteMetadata(ctx, e.ID, userKey.UserID)
			}
			continue
		}

		// Recency: 1.0 for just-updated, decaying linearly over maxAge.
		age := now.Sub(e.UpdatedAt)
		recencyScore := 1.0 - (float64(age) / float64(maxAge))
		if recencyScore < 0 {
			recencyScore = 0
		}

		// Reference frequency: normalised to [0, 1], max at 10 references.
		referenceScore := float64(md.ReferenceCount) / 10.0
		if referenceScore > 1.0 {
			referenceScore = 1.0
		}

		// Importance marking: high=1.0, medium=0.5, low=0.2.
		var importanceScore float64
		if m.metadataManager != nil {
			importanceScore = m.metadataManager.GetImportanceScore(md.Importance)
		} else {
			switch md.Importance {
			case ImportanceHigh:
				importanceScore = 1.0
			case ImportanceMedium:
				importanceScore = 0.5
			default:
				importanceScore = 0.2
			}
		}

		// Content length: normalised to [0, 1], capped at 500 chars.
		var contentLen int
		if e.Memory != nil {
			contentLen = len(e.Memory.Memory)
		}
		lengthScore := float64(contentLen) / 500.0
		if lengthScore > 1.0 {
			lengthScore = 1.0
		}

		// Combined score using configurable weights.
		score := recencyScore*m.cfg.RecencyWeight +
			referenceScore*m.cfg.ReferenceWeight +
			importanceScore*m.cfg.ImportanceWeight +
			lengthScore*m.cfg.LengthWeight
		scoredEntries = append(scoredEntries, scored{
			entry:      e,
			score:      score,
			recency:    recencyScore,
			reference:  referenceScore,
			importance: importanceScore,
			length:     lengthScore,
		})
	}

	// Sort by score descending (highest importance first).
	sort.Slice(scoredEntries, func(i, j int) bool {
		return scoredEntries[i].score > scoredEntries[j].score
	})

	// Evict entries below the keep target (lowest scores first).
	var deleted int
	for i := keepTarget; i < len(scoredEntries); i++ {
		e := scoredEntries[i].entry
		memKey := memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: e.ID,
		}
		if delErr := m.Service().DeleteMemory(ctx, memKey); delErr != nil {
			util.Logger.Warn("memory: smart cleanup delete failed",
				"id", e.ID, "score", scoredEntries[i].score,
				"error", delErr.Error())
		} else {
			deleted++
			if m.metadataManager != nil {
				_ = m.metadataManager.DeleteMetadata(ctx, e.ID, userKey.UserID)
			}
		}
	}

	if deleted > 0 {
		util.Logger.Info("memory: smart cleanup evicted",
			"deleted", deleted,
			"total_before", total,
			"total_after", len(scoredEntries)-deleted,
			"max", m.cfg.MaxMemories,
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
