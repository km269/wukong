// Package cortex provides CortexDB-backed intelligent recall and
// knowledge storage for the wukong agent.
package cortex

import (
	"context"
	"fmt"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/recall"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/core"
)

// CortexStore is a CortexDB-backed drop-in replacement for recall.Store.
// When an embedder is configured, it uses CortexDB's HNSW vector index
// for semantic search; otherwise falls back to FTS5.
type CortexStore struct {
	cfg      *config.CortexConfig
	embedder *Embedder
	db       *cortexdb.DB // real CortexDB (HNSW + FTS5)
	lexical  *lexicalStore
}

// NewStore creates a CortexStore. If embedding is configured, opens
// a real CortexDB with HNSW vector index.
func NewStore(
	cfg *config.CortexConfig,
	embedder *Embedder,
) (*CortexStore, error) {
	dbPath := config.ResolvePath(cfg.DBPath)

	cs := &CortexStore{
		cfg:      cfg,
		embedder: embedder,
	}

	// Always create lexical store for message table + FTS5.
	lex, err := newLexicalStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("cortex: init lexical: %w", err)
	}
	cs.lexical = lex

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
	if err := s.lexical.storeMessage(msg); err != nil {
		return err
	}

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

	vecs, err := s.embedder.Embed(
		context.Background(), []string{embedText})
	if err != nil {
		return nil // non-fatal
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil
	}

	// Store in CortexDB with HNSW vector index.
	return s.db.InsertTextWithVector(
		context.Background(),
		fmt.Sprintf("msg_%d", msg.ID),
		embedText,
		vecToFloat32(vecs[0]),
		map[string]string{
			"session_id": msg.SessionID,
			"user_id":    msg.UserID,
			"role":       msg.Role,
		},
	)
}

// Search uses CortexDB HNSW vector search when available, FTS5 otherwise.
func (s *CortexStore) Search(
	query, userID string, limit int,
) ([]recall.SearchResult, error) {
	if limit <= 0 {
		limit = s.cfg.MaxResults
	}
	if limit <= 0 {
		limit = 10
	}

	if s.db != nil && s.embedder != nil {
		return s.searchCortex(query, userID, limit)
	}
	return s.lexical.search(query, userID, limit)
}

func (s *CortexStore) searchCortex(
	query, userID string, limit int,
) ([]recall.SearchResult, error) {
	vecs, err := s.embedder.Embed(
		context.Background(), []string{query})
	if err != nil {
		return s.lexical.search(query, userID, limit)
	}
	if len(vecs) == 0 {
		return s.lexical.search(query, userID, limit)
	}

	// Use CortexDB's HNSW vector search.
	results, err := s.db.Vector().Search(
		context.Background(),
		vecToFloat32(vecs[0]),
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

// Close performs a clean shutdown.
func (s *CortexStore) Close() error {
	if s.db != nil {
		s.db.Close()
	}
	return s.lexical.close()
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

// Ensure types compile.
var _ core.Store
