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
	"github.com/km269/wukong/internal/search/chunking"
	"github.com/km269/wukong/internal/search/metrics"
	"github.com/km269/wukong/internal/search/vertical"

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
	reranker    *Reranker
	router      *vertical.Router       // optional vertical domain routing
	chunker     *chunking.Chunker      // optional semantic chunker
	metrics     *metrics.SearchMetrics // search observability
	db          *cortexdb.DB           // real CortexDB (HNSW + FTS5)
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
		reranker: NewReranker(cfg),
		router:   vertical.NewRouter(verticalConfigFromCortex(cfg)),
		chunker:  chunkerFromConfig(cfg),
		metrics:  metrics.New(),
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
	// Use semantic chunking for long messages; short messages
	// skip the chunker entirely (single embedding).
	var chunks []chunking.Chunk
	if s.chunker != nil {
		chunks = s.chunker.Chunk(msg.Content)
	}
	if len(chunks) <= 1 {
		// Short message: single embedding (preserves prior behaviour).
		embedText := msg.Content
		if len(embedText) > 8000 {
			embedText = embedText[:8000]
		}
		return s.storeSingleVector(msg, embedText, fmt.Sprintf("msg_%d", msg.ID))
	}

	// Long message: embed each chunk and store as separate vectors.
	// This improves retrieval recall by allowing fine-grained
	// semantic matching against individual passages.
	bgCtx, cancel := context.WithTimeout(
		context.Background(), 90*time.Second,
	)
	defer cancel()

	chunkTexts := make([]string, len(chunks))
	for i, c := range chunks {
		chunkTexts[i] = c.Text
	}

	vecs, err := s.embedder.Embed(bgCtx, chunkTexts)
	if err != nil {
		// Fall back to single-vector storage on batch failure.
		embedText := msg.Content
		if len(embedText) > 8000 {
			embedText = embedText[:8000]
		}
		return s.storeSingleVector(msg, embedText, fmt.Sprintf("msg_%d", msg.ID))
	}

	metadata := map[string]string{
		"session_id": msg.SessionID,
		"user_id":    msg.UserID,
		"role":       msg.Role,
	}
	var lastErr error
	for i, vec := range vecs {
		if len(vec) == 0 {
			continue
		}
		key := fmt.Sprintf("msg_%d_chunk_%d", msg.ID, i)
		chunkMeta := metadata
		chunkMeta["chunk_idx"] = fmt.Sprintf("%d", i)
		chunkMeta["chunk_total"] = fmt.Sprintf("%d", len(chunks))
		if err := s.db.InsertTextWithVector(
			bgCtx, key, chunks[i].Text, vecToFloat32(vec), chunkMeta,
		); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// storeSingleVector embeds a single text and stores it in CortexDB.
// Used for short messages that don't need chunking.
func (s *CortexStore) storeSingleVector(
	msg recall.ChatMessage, embedText, cacheKey string,
) error {
	bgCtx, cancel := context.WithTimeout(
		context.Background(), 60*time.Second,
	)
	defer cancel()

	var vector []float32
	var err error

	// Use vector cache to avoid redundant embedding calls.
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
// When vertical routing is enabled and the query targets a known
// vertical (arXiv, GitHub, Wikipedia, Reddit), results from the
// platform's search API are merged with local retrieval according
// to the configured MergeMode.
func (s *CortexStore) Search(
	query, userID string, limit int,
) ([]recall.SearchResult, error) {
	start := time.Now()
	if limit <= 0 {
		limit = s.genome.EffectiveTopK()
	}

	// Vertical routing: dispatch to specialised backends when the
	// query targets a known vertical. Failures are non-fatal; we
	// fall through to local retrieval.
	if s.router != nil && s.router.Enabled() {
		ctx, cancel := context.WithTimeout(
			context.Background(), 15*time.Second,
		)
		vResults, vErr := s.router.Search(ctx, query, limit)
		cancel()
		if vErr == nil && len(vResults) > 0 {
			results := s.mergeVertical(query, userID, limit, vResults)
			s.recordMetric(metrics.ModeVertical, start, len(results),
				query, false, false, false, true,
				string(s.router.DetectIntent(query)), "", len(results))
			return results, nil
		}
	}

	g := s.genome.Normalized()
	var results []recall.SearchResult
	var err error
	var mode metrics.SearchMode

	switch {
	case g.IsLexicalOnly():
		mode = metrics.ModeLexical
		results, err = s.lexical.search(query, userID, limit)
	case g.IsVectorOnly() && s.db != nil && s.embedder != nil:
		mode = metrics.ModeVector
		results, err = s.searchCortex(query, userID, limit)
	case g.IsHybrid() && s.db != nil && s.embedder != nil:
		mode = metrics.ModeHybrid
		results, err = s.searchHybridCortex(query, userID, limit)
	default:
		// Fallback: vector if available, else lexical.
		if s.db != nil && s.embedder != nil {
			mode = metrics.ModeFallback
			results, err = s.searchCortex(query, userID, limit)
		} else {
			mode = metrics.ModeLexical
			results, err = s.lexical.search(query, userID, limit)
		}
	}

	// Record metrics for the search operation.
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	s.recordMetric(mode, start, len(results), query,
		g.IsRRF(), g.RerankerEnabled, g.MMREnabled, false,
		"", errStr, len(results))

	return results, err
}

// recordMetric records a search event in the metrics collector.
func (s *CortexStore) recordMetric(
	mode metrics.SearchMode, start time.Time,
	resultCount int, query string,
	usedRRF, usedReranker, usedMMR, usedVertical bool,
	verticalIntent, errMsg string,
	preRerankCount int,
) {
	if s.metrics == nil {
		return
	}
	s.metrics.Record(metrics.SearchEvent{
		Mode:           mode,
		Duration:       time.Since(start),
		ResultCount:    resultCount,
		QueryLen:       len(query),
		UsedRRF:        usedRRF,
		UsedReranker:   usedReranker,
		UsedMMR:        usedMMR,
		UsedVertical:   usedVertical,
		VerticalIntent: verticalIntent,
		Error:          errMsg,
		PreRerankCount: preRerankCount,
	})
}

// MetricsSnapshot returns the current search metrics for observability.
// Returns nil if metrics collection is not initialised.
func (s *CortexStore) MetricsSnapshot() metrics.Snapshot {
	if s.metrics == nil {
		return metrics.Snapshot{}
	}
	return s.metrics.Snapshot()
}

// mergeVertical combines vertical (platform API) results with local
// retrieval according to the configured MergeMode:
//   - replace: vertical results only
//   - prepend: vertical first, then local (default)
//   - append:  local first, then vertical
//
// The combined list is truncated to limit.
func (s *CortexStore) mergeVertical(
	query, userID string, limit int,
	vResults []vertical.Result,
) []recall.SearchResult {
	mode := vertical.MergePrepend
	if s.cfg != nil && s.cfg.VerticalRouting != nil {
		mode = vertical.Config{
			MergeMode: s.cfg.VerticalRouting.MergeMode,
		}.EffectiveMergeMode()
	}

	verticalSR := make([]recall.SearchResult, 0, len(vResults))
	for _, r := range vResults {
		preview := r.Preview
		if r.URL != "" {
			preview = fmt.Sprintf("[%s] %s\n%s", r.Source, r.Title, r.Preview)
		}
		verticalSR = append(verticalSR, recall.SearchResult{
			Score:   r.Score,
			Preview: preview,
			Message: recall.ChatMessage{
				Role:    "vertical",
				Content: fmt.Sprintf("%s\n%s", r.Title, r.URL),
			},
		})
	}

	if mode == vertical.MergeReplace {
		if len(verticalSR) > limit {
			verticalSR = verticalSR[:limit]
		}
		return verticalSR
	}

	// Fetch local results for prepend/append modes.
	var local []recall.SearchResult
	g := s.genome.Normalized()
	switch {
	case g.IsHybrid() && s.db != nil && s.embedder != nil:
		local, _ = s.searchHybridCortex(query, userID, limit)
	case g.IsVectorOnly() && s.db != nil && s.embedder != nil:
		local, _ = s.searchCortex(query, userID, limit)
	default:
		local, _ = s.lexical.search(query, userID, limit)
	}

	var combined []recall.SearchResult
	switch mode {
	case vertical.MergeAppend:
		combined = append(combined, local...)
		combined = append(combined, verticalSR...)
	default: // prepend
		combined = append(combined, verticalSR...)
		combined = append(combined, local...)
	}
	if len(combined) > limit {
		combined = combined[:limit]
	}
	return combined
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

	// Step 3: Merge and re-rank.
	merged := make(map[string]*recall.SearchResult)

	if g.IsRRF() {
		// Reciprocal Rank Fusion: scale-invariant, rewards
		// documents that appear in both channels.
		k := g.EffectiveRRFK()
		lexChannel := make([]search.RRFEntry, 0, len(lexResults))
		for i, r := range lexResults {
			key := r.Preview
			if key == "" {
				key = fmt.Sprintf("lex_%d", i)
			}
			lexChannel = append(lexChannel, search.RRFEntry{
				Key: key, Rank: i,
			})
		}
		vecChannel := make([]search.RRFEntry, 0, len(vecResults))
		for i, r := range vecResults {
			key := r.Preview
			if key == "" {
				key = fmt.Sprintf("vec_%d", i)
			}
			vecChannel = append(vecChannel, search.RRFEntry{
				Key: key, Rank: i,
			})
		}
		fused := search.RRFFuse(
			[][]search.RRFEntry{lexChannel, vecChannel}, k,
		)
		for i, r := range lexResults {
			key := r.Preview
			if key == "" {
				key = fmt.Sprintf("lex_%d", i)
			}
			r.Score = fused[key]
			merged[key] = &r
		}
		for i, r := range vecResults {
			key := r.Preview
			if key == "" {
				key = fmt.Sprintf("vec_%d", i)
			}
			if _, ok := merged[key]; !ok {
				r.Score = fused[key]
				merged[key] = &r
			}
		}
	} else {
		// Weighted fusion (original): DenseWeight×sim + TextWeight×(1/rank).
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

	// Step 4b: Cross-Encoder reranking (optional).
	// Re-scores the top-N candidates using a dedicated rerank model,
	// replacing fusion scores with cross-encoder relevance scores.
	if s.reranker != nil && g.RerankerEnabled && len(all) > 1 {
		rerankN := g.EffectiveRerankerTopN()
		if rerankN > len(all) {
			rerankN = len(all)
		}
		// Build document texts for reranking.
		docs := make([]string, rerankN)
		for i := 0; i < rerankN; i++ {
			docs[i] = all[i].Preview
		}
		bgCtx2, cancel2 := context.WithTimeout(
			context.Background(), 30*time.Second,
		)
		defer cancel2()
		indices, scores, err := s.reranker.Rerank(
			bgCtx2, query, docs, limit,
		)
		if err == nil && len(indices) > 0 {
			reranked := make([]recall.SearchResult, 0, len(indices))
			for i, idx := range indices {
				if idx < 0 || idx >= len(all) {
					continue
				}
				r := all[idx]
				r.Score = scores[i]
				reranked = append(reranked, r)
			}
			if len(reranked) > 0 {
				all = reranked
			}
		}
	}

	// Step 4c: MMR diversity (optional).
	// Promotes diversity in top-K using Maximal Marginal Relevance,
	// preventing all results from clustering around one document.
	if g.MMREnabled && len(all) > limit {
		items := make([]search.MMRItem, len(all))
		for i, r := range all {
			items[i] = search.MMRItem{
				Text:  r.Preview,
				Score: r.Score,
				Index: i,
			}
		}
		selected := search.MMRSelect(
			items, limit, g.EffectiveMMRLambda(),
			search.TextJaccardSimilarity,
		)
		diverse := make([]recall.SearchResult, 0, len(selected))
		for _, item := range selected {
			diverse = append(diverse, all[item.Index])
		}
		all = diverse
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
			FusionMethod:        cfg.SearchStrategy.FusionMethod,
			RRFK:                cfg.SearchStrategy.RRFK,
			RerankerEnabled:     cfg.SearchStrategy.RerankerEnabled,
			RerankerTopN:        cfg.SearchStrategy.RerankerTopN,
			MMREnabled:          cfg.SearchStrategy.MMREnabled,
			MMRLambda:           cfg.SearchStrategy.MMRLambda,
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

// verticalConfigFromCortex builds a vertical.Config from CortexConfig.
// Returns a disabled config when VerticalRouting is nil.
func verticalConfigFromCortex(cfg *config.CortexConfig) vertical.Config {
	if cfg == nil || cfg.VerticalRouting == nil {
		return vertical.Config{Enabled: false}
	}
	vc := cfg.VerticalRouting
	return vertical.Config{
		Enabled:      vc.Enabled,
		TopN:         vc.TopN,
		Timeout:      vc.Timeout,
		GitHubAPIKey: vc.GitHubAPIKey,
		MergeMode:    vc.MergeMode,
	}
}

// chunkerFromConfig builds a chunking.Chunker from CortexConfig.
// Returns nil (disabled) when Chunking is nil or not enabled.
// When Chunking is nil, chunking defaults to enabled with sensible
// defaults; set enabled: false to disable.
func chunkerFromConfig(cfg *config.CortexConfig) *chunking.Chunker {
	if cfg == nil {
		return nil
	}
	// Default: enabled when not explicitly configured.
	if cfg.Chunking == nil {
		return chunking.New()
	}
	if !cfg.Chunking.Enabled {
		return nil
	}
	c := chunking.New()
	if cfg.Chunking.MaxSize > 0 {
		c.WithMaxSize(cfg.Chunking.MaxSize)
	}
	if cfg.Chunking.Overlap >= 0 {
		c.WithOverlap(cfg.Chunking.Overlap)
	}
	if cfg.Chunking.MinSize >= 0 {
		c.WithMinSize(cfg.Chunking.MinSize)
	}
	return c
}
