// Package cortex provides CortexDB-backed intelligent recall and
// knowledge storage for the wukong agent.
package cortex

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/recall"
	"github.com/km269/wukong/internal/search"

	"github.com/liliang-cn/cortexdb/v2/pkg/core"
	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
)

// CortexStore is a CortexDB-backed drop-in replacement for recall.Store.
// When an embedder is configured, it uses CortexDB's HNSW vector index
// for semantic search; otherwise falls back to FTS5.
// The lexical store shares the same *sql.DB as session/memory/todo/recall
// to avoid SQLite transaction conflicts.
type CortexStore struct {
	cfg         *config.CortexConfig
	embedder    *Embedder
	db          *cortexdb.DB // real CortexDB (HNSW + FTS5)
	lexical     *lexicalStore
	vectorCache *VectorCache
	genome      search.SearchGenome // search strategy parameters
}

// NewStore creates a CortexStore. If embedding is configured, opens
// a real CortexDB with HNSW vector index.
// sharedDB is the *sql.DB from the DatabasePool, used by the lexical
// store to avoid opening a separate connection to the same file.
func NewStore(
	cfg *config.CortexConfig,
	embedder *Embedder,
	sharedDB *sql.DB,
) (*CortexStore, error) {
	dbPath := config.ResolvePath(cfg.DBPath)

	cs := &CortexStore{
		cfg:      cfg,
		embedder: embedder,
		genome:   cortexGenomeFromConfig(cfg),
	}

	// Create lexical store using the shared DB connection to avoid
	// SQLite "transaction has already been committed" errors caused
	// by multiple independent connections to the same database file.
	lex, err := newLexicalStore(sharedDB)
	if err != nil {
		return nil, fmt.Errorf("cortex: init lexical: %w", err)
	}
	cs.lexical = lex

	// Initialize vector cache for incremental updates.
	cs.vectorCache = NewVectorCache()

	// Open real CortexDB when embedding is configured for HNSW search.
	if embedder != nil {
		dbCfg := cortexdb.DefaultConfig(dbPath)
		db, err := cortexdb.Open(dbCfg)
		if err != nil {
			return nil, fmt.Errorf("cortex: open cortexdb: %w", err)
		}
		cs.db = db
	}

	return cs, nil
}

// StoreMessage persists a chat message. With embedding, uses CortexDB's
// HNSW index; otherwise falls back to FTS5 lexical.
func (s *CortexStore) StoreMessage(msg recall.ChatMessage) error {
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}

	// Store in lexical table as authoritative source.
	// Get the auto-incremented ID for vector cache key.
	msgID, err := s.lexical.storeMessage(msg)
	if err != nil {
		return err
	}
	msg.ID = msgID

	if s.db != nil && s.embedder != nil {
		return s.storeCortexVector(msg)
	}
	return nil
}

func (s *CortexStore) storeCortexVector(msg recall.ChatMessage) error {
	embedText := msg.Content
	if len(embedText) > 8000 {
		embedText = embedText[:8000]
	}

	bgCtx, cancel := context.WithTimeout(
		context.Background(), 60*time.Second,
	)
	defer cancel()

	var vector []float32
	var err error

	// Use vector cache to avoid redundant embedding calls.
	cacheKey := fmt.Sprintf("msg_%d", msg.ID)
	if s.vectorCache != nil {
		vector, err = s.vectorCache.GetOrComputeMessageVector(
			bgCtx,
			cacheKey,
			embedText,
			func(ctx context.Context, texts []string) ([][]float64, error) {
				return s.embedder.Embed(ctx, texts)
			},
		)
		if err != nil {
			return nil
		}
	} else {
		vecs, err := s.embedder.Embed(bgCtx, []string{embedText})
		if err != nil {
			return nil
		}
		if len(vecs) == 0 || len(vecs[0]) == 0 {
			return nil
		}
		vector = vecToFloat32(vecs[0])
	}

	if vector == nil {
		return nil
	}

	// Store in CortexDB with HNSW vector index.
	return s.db.InsertTextWithVector(
		bgCtx,
		cacheKey,
		embedText,
		vector,
		map[string]string{
			"session_id": msg.SessionID,
			"user_id":    msg.UserID,
			"role":       msg.Role,
		},
	)
}

// Search uses CortexDB HNSW vector search when available, FTS5 otherwise.
// The SearchGenome controls mode selection, TopK, and hybrid weighting.
func (s *CortexStore) Search(
	query, userID string, limit int,
) ([]recall.SearchResult, error) {
	if limit <= 0 {
		limit = s.genome.EffectiveTopK()
	}

	g := s.genome.Normalized()
	switch {
	case g.IsLexicalOnly():
		return s.lexical.search(query, userID, limit)
	case g.IsVectorOnly() && s.db != nil && s.embedder != nil:
		return s.searchCortex(query, userID, limit)
	case g.IsHybrid() && s.db != nil && s.embedder != nil:
		return s.searchHybridCortex(query, userID, limit)
	default:
		// Fallback: vector if available, else lexical.
		if s.db != nil && s.embedder != nil {
			return s.searchCortex(query, userID, limit)
		}
		return s.lexical.search(query, userID, limit)
	}
}

// SetGenome configures the search strategy parameters.
func (s *CortexStore) SetGenome(g search.SearchGenome) {
	s.genome = g.Normalized()
}

// Genome returns the current search strategy parameters.
func (s *CortexStore) Genome() search.SearchGenome {
	return s.genome
}

func (s *CortexStore) searchCortex(
	query, userID string, limit int,
) ([]recall.SearchResult, error) {
	bgCtx, cancel := context.WithTimeout(
		context.Background(), 30*time.Second,
	)
	defer cancel()

	var queryVec []float32
	var err error

	// Use vector cache for query embeddings.
	if s.vectorCache != nil {
		queryVec, err = s.vectorCache.GetOrComputeQueryVector(
			bgCtx,
			query,
			func(ctx context.Context, texts []string) ([][]float64, error) {
				return s.embedder.Embed(ctx, texts)
			},
		)
		if err != nil {
			return s.lexical.search(query, userID, limit)
		}
	} else {
		vecs, err := s.embedder.Embed(bgCtx, []string{query})
		if err != nil {
			return s.lexical.search(query, userID, limit)
		}
		if len(vecs) == 0 {
			return s.lexical.search(query, userID, limit)
		}
		queryVec = vecToFloat32(vecs[0])
	}

	if queryVec == nil {
		return s.lexical.search(query, userID, limit)
	}

	// Use CortexDB's HNSW vector search.
	results, err := s.db.Vector().Search(
		bgCtx,
		queryVec,
		core.SearchOptions{TopK: limit},
	)
	if err != nil {
		return s.lexical.search(query, userID, limit)
	}

	out := make([]recall.SearchResult, 0, len(results))
	for _, r := range results {
		out = append(out, recall.SearchResult{
			Score:   r.Score,
			Preview: truncatePreview(r.Content, 200),
		})
	}
	return out, nil
}

// searchHybridCortex performs hybrid search: FTS5 lexical retrieval
// combined with HNSW vector search. Results are merged and re-ranked
// using DenseWeight (vector) + TextWeight (lexical) from the genome.
func (s *CortexStore) searchHybridCortex(
	query, userID string, limit int,
) ([]recall.SearchResult, error) {
	g := s.genome.Normalized()
	poolSize := g.EffectivePoolSize()

	// Step 1: FTS5 lexical retrieval (wider pool).
	lexResults, _ := s.lexical.search(query, userID, poolSize)

	// Step 2: HNSW vector search (wider pool).
	bgCtx, cancel := context.WithTimeout(
		context.Background(), 30*time.Second,
	)
	defer cancel()

	var queryVec []float32
	var err error
	if s.vectorCache != nil {
		queryVec, err = s.vectorCache.GetOrComputeQueryVector(
			bgCtx, query,
			func(ctx context.Context, texts []string) ([][]float64, error) {
				return s.embedder.Embed(ctx, texts)
			},
		)
	} else {
		vecs, e := s.embedder.Embed(bgCtx, []string{query})
		if e == nil && len(vecs) > 0 {
			queryVec = vecToFloat32(vecs[0])
		}
		err = e
	}

	var vecResults []recall.SearchResult
	if err == nil && queryVec != nil {
		rawResults, vErr := s.db.Vector().Search(
			bgCtx, queryVec,
			core.SearchOptions{TopK: poolSize},
		)
		if vErr == nil {
			for _, r := range rawResults {
				vecResults = append(vecResults, recall.SearchResult{
					Score:   r.Score,
					Preview: truncatePreview(r.Content, 200),
				})
			}
		}
	}

	// Step 3: Merge and re-rank using genome weights.
	merged := make(map[string]*recall.SearchResult)
	// Lexical results: score contribution = TextWeight × (1 / rank).
	for i, r := range lexResults {
		key := r.Preview
		if key == "" {
			key = fmt.Sprintf("lex_%d", i)
		}
		score := g.TextWeight * (1.0 / float64(i+1))
		if existing, ok := merged[key]; ok {
			existing.Score += score
		} else {
			r.Score = score
			merged[key] = &r
		}
	}
	// Vector results: score contribution = DenseWeight × similarity.
	for i, r := range vecResults {
		key := r.Preview
		if key == "" {
			key = fmt.Sprintf("vec_%d", i)
		}
		score := g.DenseWeight * r.Score
		if existing, ok := merged[key]; ok {
			existing.Score += score
		} else {
			r.Score = score
			merged[key] = &r
		}
	}

	// Step 4: Sort by combined score.
	all := make([]recall.SearchResult, 0, len(merged))
	for _, r := range merged {
		all = append(all, *r)
	}
	for i := 0; i < len(all)-1; i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].Score > all[i].Score {
				all[i], all[j] = all[j], all[i]
			}
		}
	}

	// Step 5: Return top-K.
	if limit > len(all) {
		limit = len(all)
	}
	return all[:limit], nil
}

// SearchBySession searches within a specific session.
func (s *CortexStore) SearchBySession(
	sessionID, query string, limit int,
) ([]recall.SearchResult, error) {
	if limit <= 0 {
		limit = s.cfg.MaxResults
	}
	if limit <= 0 {
		limit = 10
	}
	return s.lexical.searchBySession(sessionID, query, limit)
}

// ListSessions returns distinct session IDs.
func (s *CortexStore) ListSessions(userID string) ([]string, error) {
	return s.lexical.listSessions(userID)
}

// DeleteSession removes all messages for a session.
func (s *CortexStore) DeleteSession(sessionID string) error {
	return s.lexical.deleteSession(sessionID)
}

// Close performs a clean shutdown. The lexical store's shared DB
// is NOT closed here — the DatabasePool owner manages its lifecycle.
// When markOwned is false (shared mode), the CortexDB instance is
// NOT closed — the owner (e.g., MemoryFlowService) manages it.
func (s *CortexStore) Close() error {
	if s.vectorCache != nil {
		s.vectorCache.Stop()
	}
	if s.db != nil {
		s.db.Close()
	}
	return s.lexical.close() // no-op: DB managed by DatabasePool
}

// SetDB replaces the CortexDB instance. Call this to share an
// existing CortexDB (e.g., from MemoryFlowService) instead of
// opening a separate connection to the same database file.
func (s *CortexStore) SetDB(db *cortexdb.DB) {
	s.db = db
}

// DB returns the underlying CortexDB instance. Returns nil when
// no embedder is configured (lexical-only mode).
func (s *CortexStore) DB() *cortexdb.DB {
	return s.db
}

// RecallStore returns a *recall.Store adapter sharing the same DB.
func (s *CortexStore) RecallStore() (*recall.Store, error) {
	return recall.NewStoreWithDB(
		s.lexical.db, s.cfg.MaxMessagesPerSession)
}

// ---------------------------------------------------------------------------
// Cross-search: recall can also search tRPC memories
// ---------------------------------------------------------------------------

// SearchWithMemory extends recall search to also query the tRPC memory
// table. Returns combined results from both recall and memory stores.
func (s *CortexStore) SearchWithMemory(
	query, userID string, limit int,
	memoryReader func(ctx context.Context, query string) ([]string, error),
) ([]recall.SearchResult, error) {
	// Search recall messages.
	results, err := s.Search(query, userID, limit)
	if err != nil {
		results = nil
	}

	// Search tRPC memories via provided reader.
	if memoryReader != nil {
		memTexts, mErr := memoryReader(
			context.Background(), query)
		if mErr == nil {
			for _, text := range memTexts {
				results = append(results, recall.SearchResult{
					Preview: "[Memory] " + truncatePreview(text, 190),
					Score:   0.5,
				})
			}
		}
	}

	return results, nil
}

// ---------------------------------------------------------------------------
// Utility
// ---------------------------------------------------------------------------

func vecToFloat32(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, val := range v {
		out[i] = float32(val)
	}
	return out
}

func truncatePreview(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "..."
}

// cortexGenomeFromConfig builds a SearchGenome from CortexConfig.
// When SearchStrategy is nil, returns DefaultGenome with MaxResults.
func cortexGenomeFromConfig(cfg *config.CortexConfig) search.SearchGenome {
	if cfg == nil {
		return search.DefaultGenome()
	}
	if cfg.SearchStrategy != nil {
		return search.SearchGenome{
			RecallMode:          cfg.SearchStrategy.RecallMode,
			DenseWeight:         cfg.SearchStrategy.DenseWeight,
			TextWeight:          cfg.SearchStrategy.TextWeight,
			KeywordMatchPercent: cfg.SearchStrategy.KeywordMatchPercent,
			MaxRetrievedNum:     cfg.SearchStrategy.MaxRetrievedNum,
			FTS5PoolSize:        cfg.SearchStrategy.FTS5PoolSize,
		}.Normalized()
	}
	maxR := cfg.MaxResults
	if maxR <= 0 {
		maxR = 10
	}
	return search.SearchGenome{
		RecallMode:      search.RecallModeHybrid,
		DenseWeight:     0.7,
		TextWeight:      0.3,
		MaxRetrievedNum: maxR,
		FTS5PoolSize:    50,
	}
}

// Ensure types compile.
var _ core.Store
