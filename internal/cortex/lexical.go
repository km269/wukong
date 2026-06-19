// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements the lexical (non-vector) fallback storage
// using SQLite + FTS5, plus a vector index table for embedding
// similarity search. It mirrors the original recall/store.go
// implementation but adds a vector index layer.
package cortex

import (
	"database/sql"
	"fmt"
	"math"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"github.com/km269/wukong/internal/recall"
)

// lexicalStore is the SQLite-backed fallback that mirrors the
// original recall.Store. It adds a vector index table alongside
// the FTS5 full-text index for hybrid search.
type lexicalStore struct {
	db *sql.DB
}

// newLexicalStore opens (or creates) the SQLite database and
// initializes the schema (chat_recall + FTS5 + vector index).
func newLexicalStore(dbPath string) (*lexicalStore, error) {
	db, err := sql.Open("sqlite3",
		dbPath+"?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ls := &lexicalStore{db: db}
	if err := ls.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return ls, nil
}

// storeMessage inserts a chat message into the chat_recall table.
// Enforces MaxMessagesPerSession by pruning oldest messages.
func (ls *lexicalStore) storeMessage(
	msg recall.ChatMessage, maxPerSession ...int,
) error {
	_, err := ls.db.Exec(
		`INSERT INTO chat_recall (session_id, user_id, role, content, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		msg.SessionID, msg.UserID, msg.Role, msg.Content, msg.CreatedAt,
	)
	if err != nil {
		return err
	}

	// Enforce per-session message limit.
	limit := 200
	if len(maxPerSession) > 0 && maxPerSession[0] > 0 {
		limit = maxPerSession[0]
	}
	_, _ = ls.db.Exec(
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
		msg.SessionID, limit, msg.SessionID,
	)
	return nil
}

// search performs FTS5 full-text search.
func (ls *lexicalStore) search(
	query, userID string, limit int,
) ([]recall.SearchResult, error) {
	rows, err := ls.db.Query(
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
		return ls.searchLike(query, userID, limit)
	}
	defer rows.Close()

	return scanResults(rows)
}

// searchBySession performs FTS5 search scoped to a session.
func (ls *lexicalStore) searchBySession(
	sessionID, query string, limit int,
) ([]recall.SearchResult, error) {
	rows, err := ls.db.Query(
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
		return ls.searchLikeBySession(sessionID, query, limit)
	}
	defer rows.Close()

	return scanResults(rows)
}

// listSessions returns distinct session IDs.
func (ls *lexicalStore) listSessions(userID string) ([]string, error) {
	rows, err := ls.db.Query(
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

// deleteSession removes all messages for a session.
func (ls *lexicalStore) deleteSession(sessionID string) error {
	_, err := ls.db.Exec(
		`DELETE FROM chat_recall WHERE session_id = ?`,
		sessionID,
	)
	return err
}

// close closes the database connection.
func (ls *lexicalStore) close() error {
	return ls.db.Close()
}

// ---------------------------------------------------------------------------
// Vector index operations
// ---------------------------------------------------------------------------

// storeVector inserts an embedding vector for a stored message.
func (ls *lexicalStore) storeVector(
	msgID int64, sessionID, userID string,
	vec []float32, content string,
) error {
	// Serialize vector to JSON for SQLite storage.
	vecJSON := vecToJSON(vec)
	_, err := ls.db.Exec(
		`INSERT OR REPLACE INTO chat_recall_vec
		 (msg_id, session_id, user_id, vector, content_snippet)
		 VALUES (?, ?, ?, ?, ?)`,
		msgID, sessionID, userID, vecJSON,
		truncatePreview(content, 500),
	)
	return err
}

// searchVector performs cosine similarity search over stored vectors,
// combining semantic results with FTS5 BM25 ranking.
func (ls *lexicalStore) searchVector(
	queryVec []float32,
	queryText, userID, sessionID string,
	limit int,
) ([]recall.SearchResult, error) {
	// Step 1: Retrieve candidate vectors from DB.
	rows, err := ls.db.Query(
		`SELECT v.msg_id, v.session_id, v.user_id,
		        v.vector, v.content_snippet,
		        cr.role, cr.content, cr.created_at
		 FROM chat_recall_vec v
		 JOIN chat_recall cr ON cr.id = v.msg_id
		 ORDER BY cr.created_at DESC
		 LIMIT 200`,
	)
	if err != nil {
		// Fall back to lexical search.
		if sessionID != "" {
			return ls.searchBySession(sessionID, queryText, limit)
		}
		return ls.search(queryText, userID, limit)
	}
	defer rows.Close()

	// Step 2: Compute cosine similarity for each candidate.
	type candidate struct {
		msgID       int64
		sessionID   string
		userID      string
		role        string
		content     string
		similarity  float64
	}
	var candidates []candidate

	for rows.Next() {
		var msgID int64
		var sid, uid, vecJSON, snippet, role, content string
		var createdAt interface{}
		if err := rows.Scan(&msgID, &sid, &uid,
			&vecJSON, &snippet, &role, &content, &createdAt,
		); err != nil {
			continue
		}

		storedVec := jsonToVec(vecJSON)
		if len(storedVec) == 0 {
			continue
		}

		// Filter by user / session.
		if userID != "" && uid != userID {
			continue
		}
		if sessionID != "" && sid != sessionID {
			continue
		}

		sim := cosineSim(queryVec, storedVec)
		candidates = append(candidates, candidate{
			msgID:      msgID,
			sessionID:  sid,
			userID:     uid,
			role:       role,
			content:    content,
			similarity: sim,
		})
	}

	// Step 3: Sort by similarity descending (bubble sort, small N).
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].similarity > candidates[i].similarity {
				candidates[i], candidates[j] =
					candidates[j], candidates[i]
			}
		}
	}

	// Step 4: Return top-K results.
	results := make([]recall.SearchResult, 0, limit)
	for i, c := range candidates {
		if i >= limit {
			break
		}
		results = append(results, recall.SearchResult{
			Message: recall.ChatMessage{
				ID:        c.msgID,
				SessionID: c.sessionID,
				UserID:    c.userID,
				Role:      c.role,
				Content:   c.content,
			},
			Score:   c.similarity,
			Preview: truncatePreview(c.content, 200),
		})
	}

	// Step 5: If vector search returned too few results, supplement
	// with FTS5 results.
	if len(results) < limit {
		var ftsResults []recall.SearchResult
		var ftsErr error
		if sessionID != "" {
			ftsResults, ftsErr = ls.searchBySession(
				sessionID, queryText, limit-len(results),
			)
		} else {
			ftsResults, ftsErr = ls.search(
				queryText, userID, limit-len(results),
			)
		}
		if ftsErr == nil {
			results = append(results, ftsResults...)
		}
	}

	return results, nil
}

// ---------------------------------------------------------------------------
// Fallback LIKE search
// ---------------------------------------------------------------------------

func (ls *lexicalStore) searchLike(
	query, userID string, limit int,
) ([]recall.SearchResult, error) {
	searchTerm := "%" + strings.ToLower(query) + "%"
	rows, err := ls.db.Query(
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

	return scanResultsNoRank(rows, query)
}

func (ls *lexicalStore) searchLikeBySession(
	sessionID, query string, limit int,
) ([]recall.SearchResult, error) {
	searchTerm := "%" + strings.ToLower(query) + "%"
	rows, err := ls.db.Query(
		`SELECT id, session_id, user_id, role, content, created_at
		 FROM chat_recall
		 WHERE session_id = ? AND LOWER(content) LIKE ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		sessionID, searchTerm, limit,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"search by session: %w", err,
		)
	}
	defer rows.Close()

	return scanResultsNoRank(rows, query)
}

// ---------------------------------------------------------------------------
// Schema initialization
// ---------------------------------------------------------------------------

func (ls *lexicalStore) initSchema() error {
	// Main recall table.
	_, err := ls.db.Exec(`
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

	// FTS5 virtual table for full-text search.
	_, err = ls.db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS chat_recall_fts
			USING fts5(
				content,
				content='chat_recall',
				content_rowid='id',
				tokenize='unicode61'
			)
	`)
	if err != nil {
		// FTS5 may not be available — non-fatal.
	}

	// FTS5 sync triggers.
	_, _ = ls.db.Exec(`
		CREATE TRIGGER IF NOT EXISTS recall_fts_insert
		AFTER INSERT ON chat_recall
		BEGIN
			INSERT INTO chat_recall_fts(rowid, content)
			VALUES (new.id, new.content);
		END
	`)
	_, _ = ls.db.Exec(`
		CREATE TRIGGER IF NOT EXISTS recall_fts_delete
		AFTER DELETE ON chat_recall
		BEGIN
			INSERT INTO chat_recall_fts(
				chat_recall_fts, rowid, content)
			VALUES ('delete', old.id, old.content);
		END
	`)
	_, _ = ls.db.Exec(`
		CREATE TRIGGER IF NOT EXISTS recall_fts_update
		AFTER UPDATE ON chat_recall
		BEGIN
			INSERT INTO chat_recall_fts(
				chat_recall_fts, rowid, content)
			VALUES ('delete', old.id, old.content);
			INSERT INTO chat_recall_fts(rowid, content)
			VALUES (new.id, new.content);
		END
	`)

	// Vector index table for embedding similarity search.
	_, err = ls.db.Exec(`
		CREATE TABLE IF NOT EXISTS chat_recall_vec (
			msg_id INTEGER PRIMARY KEY,
			session_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			vector TEXT NOT NULL,
			content_snippet TEXT NOT NULL,
			FOREIGN KEY (msg_id) REFERENCES chat_recall(id)
				ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_vec_session
			ON chat_recall_vec(session_id);
		CREATE INDEX IF NOT EXISTS idx_vec_user
			ON chat_recall_vec(user_id);
	`)
	if err != nil {
		return err
	}

	return nil
}

// ---------------------------------------------------------------------------
// Result scanning helpers
// ---------------------------------------------------------------------------

func scanResults(rows *sql.Rows) ([]recall.SearchResult, error) {
	var results []recall.SearchResult
	for rows.Next() {
		var msg recall.ChatMessage
		var score float64
		if err := rows.Scan(
			&msg.ID, &msg.SessionID, &msg.UserID,
			&msg.Role, &msg.Content, &msg.CreatedAt,
			&score,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		results = append(results, recall.SearchResult{
			Message: msg,
			Score:   score,
			Preview: truncatePreview(msg.Content, 200),
		})
	}
	return results, nil
}

func scanResultsNoRank(
	rows *sql.Rows, query string,
) ([]recall.SearchResult, error) {
	var results []recall.SearchResult
	for rows.Next() {
		var msg recall.ChatMessage
		if err := rows.Scan(
			&msg.ID, &msg.SessionID, &msg.UserID,
			&msg.Role, &msg.Content, &msg.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		results = append(results, recall.SearchResult{
			Message: msg,
			Score:   calcScore(query, msg.Content),
			Preview: truncatePreview(msg.Content, 200),
		})
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Utility functions
// ---------------------------------------------------------------------------

// ftsQuery converts a user query to FTS5-compatible prefix syntax.
func ftsQuery(query string) string {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return q
	}
	words := strings.Fields(q)
	for i, w := range words {
		if !strings.ContainsAny(w, "*\"'()") {
			words[i] = w + "*"
		}
	}
	return strings.Join(words, " ")
}

// calcScore computes a simple relevance score for LIKE results.
func calcScore(query, content string) float64 {
	ql := strings.ToLower(query)
	cl := strings.ToLower(content)
	count := strings.Count(cl, ql)
	if count == 0 {
		for _, word := range strings.Fields(ql) {
			if strings.Contains(cl, word) {
				count++
			}
		}
	}
	if count == 0 {
		return 0.0
	}
	score := float64(count) / float64(len(cl)/100+1)
	if score > 1.0 {
		score = 1.0
	}
	return score
}

// cosineSim computes cosine similarity between two float32 vectors.
func cosineSim(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// vecToJSON serializes a float32 vector to a JSON string.
func vecToJSON(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteString("[")
	for i, val := range v {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(fmt.Sprintf("%.6f", val))
	}
	b.WriteString("]")
	return b.String()
}

// jsonToVec deserializes a JSON string to a float32 vector.
func jsonToVec(s string) []float32 {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil
	}
	s = s[1 : len(s)-1]
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	vec := make([]float32, 0, len(parts))
	for _, p := range parts {
		var val float64
		fmt.Sscanf(strings.TrimSpace(p), "%f", &val)
		vec = append(vec, float32(val))
	}
	return vec
}
