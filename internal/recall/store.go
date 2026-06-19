// Package recall provides cross-session chat recall.
// It enables searching across all conversation histories,
// similar to Goose's Chat Recall feature.
package recall

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
)

// ChatMessage represents a stored chat message for recall.
type ChatMessage struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"` // user, assistant, tool
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// SearchResult represents a recall search result.
type SearchResult struct {
	Message     ChatMessage `json:"message"`
	Score       float64     `json:"score"`
	Preview     string      `json:"preview"`
}

// Embedder defines the interface for generating text embeddings.
// This allows recall to support semantic search via any embedder
// implementation (OpenAI, Ollama, local models, etc.).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// Store manages persistent storage for chat recall.
type Store struct {
	db       *sql.DB
	pool     *util.DatabasePool
	cfg      *config.RecallConfig
	embedder Embedder
}

// NewStore creates a new recall store using a shared database pool.
func NewStore(
	cfg *config.RecallConfig,
	pool *util.DatabasePool,
) (*Store, error) {
	if pool == nil {
		dbPath := config.ResolvePath(cfg.DBPath)
		pool = util.NewDatabasePool(dbPath)
	}

	db, err := pool.GetDB()
	if err != nil {
		return nil, fmt.Errorf("get db: %w", err)
	}

	s := &Store{db: db, pool: pool, cfg: cfg}
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return s, nil
}

// NewStoreWithDB creates a recall store using an already-open *sql.DB
// connection. Used when the database is shared across subsystems
// (e.g., CortexDB manages its own connection). Schema is initialized
// if not already present.
func NewStoreWithDB(
	db *sql.DB, maxMessagesPerSession int,
) (*Store, error) {
	cfg := &config.RecallConfig{
		MaxMessagesPerSession: maxMessagesPerSession,
	}
	s := &Store{db: db, pool: nil, cfg: cfg}
	if err := s.initSchema(); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return s, nil
}

// StoreMessage persists a chat message for future recall.
// Enforces MaxMessagesPerSession limit by deleting oldest messages
// when the limit is exceeded.
func (s *Store) StoreMessage(msg ChatMessage) error {
	_, err := s.db.Exec(
		`INSERT INTO chat_recall (session_id, user_id, role, content, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		msg.SessionID, msg.UserID, msg.Role, msg.Content, msg.CreatedAt,
	)
	if err != nil {
		return err
	}

	// Enforce per-session message limit
	if s.cfg.MaxMessagesPerSession > 0 {
		_, _ = s.db.Exec(
			`DELETE FROM chat_recall WHERE id IN (
				SELECT id FROM chat_recall
				WHERE session_id = ?
				ORDER BY created_at ASC
				LIMIT (
					SELECT MAX(0, COUNT(*) - ?)
					FROM chat_recall
					WHERE session_id = ?
				)
			)`,
			msg.SessionID,
			s.cfg.MaxMessagesPerSession,
			msg.SessionID,
		)
	}

	return nil
}

// Search searches across all stored messages using FTS5 full-text search.
// Falls back to LIKE search if FTS5 is not available.
func (s *Store) Search(
	query, userID string, limit int,
) ([]SearchResult, error) {
	if limit <= 0 {
		limit = s.cfg.MaxResults
	}
	if limit <= 0 {
		limit = 10
	}

	// Use FTS5 full-text search for better relevance and performance.
	// The FTS5 BM25 ranking provides much better scoring than naive LIKE.
	rows, err := s.db.Query(
		`SELECT cr.id, cr.session_id, cr.user_id, cr.role,
		        cr.content, cr.created_at,
		        fts.rank AS score
		 FROM chat_recall_fts fts
		 JOIN chat_recall cr ON cr.id = fts.rowid
		 WHERE chat_recall_fts MATCH ?
		 ORDER BY fts.rank
		 LIMIT ?`,
		ftsQuery(query), limit,
	)
	if err != nil {
		// If FTS5 fails (e.g., table not found), fall back to LIKE
		return s.searchLike(query, userID, limit)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var msg ChatMessage
		var score float64
		if err := rows.Scan(
			&msg.ID, &msg.SessionID, &msg.UserID,
			&msg.Role, &msg.Content, &msg.CreatedAt,
			&score,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		preview := msg.Content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}

		results = append(results, SearchResult{
			Message: msg,
			Score:   score,
			Preview: preview,
		})
	}

	return results, nil
}

// searchLike is the fallback LIKE-based search when FTS5 is unavailable.
func (s *Store) searchLike(
	query, userID string, limit int,
) ([]SearchResult, error) {
	searchTerm := "%" + strings.ToLower(query) + "%"
	rows, err := s.db.Query(
		`SELECT id, session_id, user_id, role, content, created_at
		 FROM chat_recall
		 WHERE user_id = ? AND LOWER(content) LIKE ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		userID, searchTerm, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var msg ChatMessage
		if err := rows.Scan(
			&msg.ID, &msg.SessionID, &msg.UserID,
			&msg.Role, &msg.Content, &msg.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		preview := msg.Content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}

		results = append(results, SearchResult{
			Message: msg,
			Score:   calculateScore(query, msg.Content),
			Preview: preview,
		})
	}

	return results, nil
}

// ftsQuery converts a user query to FTS5-compatible syntax.
// FTS5 supports boolean operators (AND, OR, NOT) and prefix searches.
func ftsQuery(query string) string {
	// Trim and lowercase
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return q
	}

	// Split into words and add prefix matching for each word.
	// This allows partial-word matches, similar to LIKE behavior
	// but with proper relevance ranking.
	words := strings.Fields(q)
	for i, w := range words {
		// Add prefix matching with * for each word
		if !strings.ContainsAny(w, "*\"'()") {
			words[i] = w + "*"
		}
	}
	return strings.Join(words, " ")
}

// SearchBySession searches within a specific session using FTS5.
func (s *Store) SearchBySession(
	sessionID, query string, limit int,
) ([]SearchResult, error) {
	if limit <= 0 {
		limit = s.cfg.MaxResults
	}

	// Use FTS5 with session_id filter
	rows, err := s.db.Query(
		`SELECT cr.id, cr.session_id, cr.user_id, cr.role,
		        cr.content, cr.created_at,
		        fts.rank AS score
		 FROM chat_recall_fts fts
		 JOIN chat_recall cr ON cr.id = fts.rowid
		 WHERE chat_recall_fts MATCH ? AND cr.session_id = ?
		 ORDER BY fts.rank
		 LIMIT ?`,
		ftsQuery(query), sessionID, limit,
	)
	if err != nil {
		// Fall back to LIKE search
		return s.searchLikeBySession(sessionID, query, limit)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var msg ChatMessage
		var score float64
		if err := rows.Scan(
			&msg.ID, &msg.SessionID, &msg.UserID,
			&msg.Role, &msg.Content, &msg.CreatedAt,
			&score,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		preview := msg.Content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}

		results = append(results, SearchResult{
			Message: msg,
			Score:   score,
			Preview: preview,
		})
	}

	return results, nil
}

// searchLikeBySession is the fallback LIKE search for session-scoped queries.
func (s *Store) searchLikeBySession(
	sessionID, query string, limit int,
) ([]SearchResult, error) {
	searchTerm := "%" + strings.ToLower(query) + "%"
	rows, err := s.db.Query(
		`SELECT id, session_id, user_id, role, content, created_at
		 FROM chat_recall
		 WHERE session_id = ? AND LOWER(content) LIKE ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		sessionID, searchTerm, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search by session: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var msg ChatMessage
		if err := rows.Scan(
			&msg.ID, &msg.SessionID, &msg.UserID,
			&msg.Role, &msg.Content, &msg.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		preview := msg.Content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}

		results = append(results, SearchResult{
			Message: msg,
			Score:   calculateScore(query, msg.Content),
			Preview: preview,
		})
	}

	return results, nil
}

// ListSessions returns distinct session IDs.
func (s *Store) ListSessions(userID string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT session_id FROM chat_recall
		 WHERE user_id = ? OR ? = ''
		 ORDER BY session_id DESC LIMIT 50`,
		userID, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		sessions = append(sessions, sid)
	}
	return sessions, nil
}

// DeleteSession removes all messages for a session.
func (s *Store) DeleteSession(sessionID string) error {
	_, err := s.db.Exec(
		`DELETE FROM chat_recall WHERE session_id = ?`,
		sessionID,
	)
	return err
}

// SetEmbedder configures an embedder for hybrid/semantic search.
// When set and search_mode is "hybrid", search results from FTS5
// are re-ranked using embedding cosine similarity for better
// semantic matching. Set to nil to disable hybrid search.
func (s *Store) SetEmbedder(e Embedder) {
	s.embedder = e
}

// HasHybridSearch returns true when both hybrid mode is configured
// and an embedder is available.
func (s *Store) HasHybridSearch() bool {
	return s.cfg.SearchMode == "hybrid" && s.embedder != nil
}

// SearchHybrid performs a hybrid search: FTS5 retrieval followed
// by embedding-based semantic re-ranking. Returns the top-K results
// ranked by combined score (BM25 + cosine similarity).
func (s *Store) SearchHybrid(
	ctx context.Context,
	query, userID string, limit int,
) ([]SearchResult, error) {
	if s.embedder == nil {
		return s.Search(query, userID, limit)
	}

	// Step 1: Retrieve candidates via FTS5 (wider pool for re-ranking)
	const fts5Pool = 50
	ftsResults, err := s.Search(query, userID, fts5Pool)
	if err != nil {
		return nil, fmt.Errorf("fts5 retrieval: %w", err)
	}
	if len(ftsResults) == 0 {
		return nil, nil
	}

	// Step 2: Embed the query
	queryVecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil {
		// Fall back to FTS5-only on embedder failure
		if limit < len(ftsResults) {
			return ftsResults[:limit], nil
		}
		return ftsResults, nil
	}
	if len(queryVecs) == 0 || len(queryVecs[0]) == 0 {
		return ftsResults[:min(limit, len(ftsResults))], nil
	}
	queryVec := queryVecs[0]

	// Step 3: Embed candidates and compute cosine similarity
	type scoredResult struct {
		result SearchResult
		score  float64
	}
	var hybrid []scoredResult
	for i, r := range ftsResults {
		text := r.Message.Content
		if len(text) > 500 {
			text = text[:500]
		}
		candVecs, err := s.embedder.Embed(ctx, []string{text})
		if err != nil {
			continue
		}
		if len(candVecs) == 0 || len(candVecs[0]) == 0 {
			continue
		}
		sim := cosineSimilarity(queryVec, candVecs[0])
		// Combined score: 70% semantic + 30% BM25 (normalized)
		combined := sim*0.7 + (1.0/float64(i+1))*0.3
		r.Score = combined
		hybrid = append(hybrid, scoredResult{result: r, score: combined})
	}

	// Step 4: Sort by combined score descending
	for i := 0; i < len(hybrid)-1; i++ {
		for j := i + 1; j < len(hybrid); j++ {
			if hybrid[j].score > hybrid[i].score {
				hybrid[i], hybrid[j] = hybrid[j], hybrid[i]
			}
		}
	}

	// Step 5: Return top-K
	results := make([]SearchResult, 0, limit)
	for i, sr := range hybrid {
		if i >= limit {
			break
		}
		results = append(results, sr.result)
	}
	return results, nil
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// Close closes the database connection.
func (s *Store) Close() error {
	if s.pool != nil {
		return s.pool.Close()
	}
	return nil
}

func (s *Store) initSchema() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS chat_recall (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_recall_session
			ON chat_recall(session_id);
		CREATE INDEX IF NOT EXISTS idx_recall_user
			ON chat_recall(user_id);
		CREATE INDEX IF NOT EXISTS idx_recall_created
			ON chat_recall(created_at);
	`)
	if err != nil {
		return err
	}

	// Create FTS5 virtual table for full-text search with BM25 ranking.
	// The content table is the external content table, so FTS5 is kept
	// in sync automatically via triggers. We include content as the
	// only indexed column since that's what we search against.
	_, err = s.db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS chat_recall_fts
			USING fts5(
				content,
				content='chat_recall',
				content_rowid='id',
				tokenize='unicode61'
			)
	`)
	if err != nil {
		// FTS5 may not be available in all SQLite builds.
		// This is non-fatal; search will fall back to LIKE.
		return nil
	}

	// Create triggers to keep FTS5 index in sync with chat_recall.
	// These are IF NOT EXISTS so they don't fail on re-runs.
	_, _ = s.db.Exec(`
		CREATE TRIGGER IF NOT EXISTS recall_fts_insert
		AFTER INSERT ON chat_recall
		BEGIN
			INSERT INTO chat_recall_fts(rowid, content)
			VALUES (new.id, new.content);
		END
	`)
	_, _ = s.db.Exec(`
		CREATE TRIGGER IF NOT EXISTS recall_fts_delete
		AFTER DELETE ON chat_recall
		BEGIN
			INSERT INTO chat_recall_fts(chat_recall_fts, rowid, content)
			VALUES ('delete', old.id, old.content);
		END
	`)
	_, _ = s.db.Exec(`
		CREATE TRIGGER IF NOT EXISTS recall_fts_update
		AFTER UPDATE ON chat_recall
		BEGIN
			INSERT INTO chat_recall_fts(chat_recall_fts, rowid, content)
			VALUES ('delete', old.id, old.content);
			INSERT INTO chat_recall_fts(rowid, content)
			VALUES (new.id, new.content);
		END
	`)

	return nil
}

// calculateScore computes a simple relevance score.
func calculateScore(query, content string) float64 {
	queryLower := strings.ToLower(query)
	contentLower := strings.ToLower(content)

	// Count occurrences
	count := strings.Count(contentLower, queryLower)
	if count == 0 {
		// Check for partial matches
		words := strings.Fields(queryLower)
		for _, word := range words {
			if strings.Contains(contentLower, word) {
				count++
			}
		}
	}

	// Normalize score
	if count == 0 {
		return 0.0
	}

	score := float64(count) / float64(len(contentLower)/100+1)
	if score > 1.0 {
		score = 1.0
	}
	return score
}
