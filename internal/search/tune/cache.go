package tune

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/km269/wukong/internal/migration"
)

// ----------------------------------------------------------------------------
// InMemoryLabelCache: simple concurrent map-based label cache
// ----------------------------------------------------------------------------

// InMemoryLabelCache provides an in-memory label cache for a
// single tuning run. Labels are keyed by labelCacheKey(query, docID).
type InMemoryLabelCache struct {
	mu     sync.RWMutex
	labels map[string]int
}

// NewInMemoryLabelCache creates a new empty label cache.
func NewInMemoryLabelCache() *InMemoryLabelCache {
	return &InMemoryLabelCache{
		labels: make(map[string]int),
	}
}

// Get returns the cached grade, or false if absent.
func (c *InMemoryLabelCache) Get(key string) (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	grade, ok := c.labels[key]
	return grade, ok
}

// Set stores a grade for the key.
func (c *InMemoryLabelCache) Set(key string, grade int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.labels[key] = grade
}

// Size returns the number of cached labels.
func (c *InMemoryLabelCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.labels)
}

// ----------------------------------------------------------------------------
// SQLiteCheckpointStore: persists TuneRun state for resume
// ----------------------------------------------------------------------------

// SQLiteCheckpointStore persists tuning run state to SQLite,
// enabling checkpoint/resume across process restarts.
type SQLiteCheckpointStore struct {
	db *sql.DB
	mu sync.Mutex
}

// NewSQLiteCheckpointStore creates a checkpoint store at the
// given SQLite database path. Creates the schema if needed.
func NewSQLiteCheckpointStore(dbPath string) (*SQLiteCheckpointStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: open db: %w", err)
	}
	store := &SQLiteCheckpointStore{db: db}
	if err := store.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("checkpoint: init schema: %w", err)
	}
	return store, nil
}

// NewSQLiteCheckpointStoreWithDB creates a checkpoint store
// using an existing *sql.DB connection (e.g. from DatabasePool).
func NewSQLiteCheckpointStoreWithDB(
	db *sql.DB,
) (*SQLiteCheckpointStore, error) {
	store := &SQLiteCheckpointStore{db: db}
	if err := store.initSchema(); err != nil {
		return nil, fmt.Errorf("checkpoint: init schema: %w", err)
	}
	return store, nil
}

// initSchema applies the versioned tuning migrations (P0-3).
func (s *SQLiteCheckpointStore) initSchema() error {
	return migration.Apply(context.Background(), s.db, Migrations())
}

// SaveRun persists the current run state.
func (s *SQLiteCheckpointStore) SaveRun(run *TuneRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("checkpoint: marshal run: %w", err)
	}

	run.Status = "running"
	run.UpdatedAt = time.Now()

	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO tune_runs (run_id, status, run_json, updated_at)
		 VALUES (?, ?, ?, ?)`,
		run.RunID, run.Status, string(data), run.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("checkpoint: save run: %w", err)
	}
	return nil
}

// LoadRun retrieves a run by ID.
func (s *SQLiteCheckpointStore) LoadRun(runID string) (*TuneRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var runJSON, status string
	var updatedAt time.Time
	err := s.db.QueryRow(
		`SELECT run_json, status, updated_at
		 FROM tune_runs WHERE run_id = ?`,
		runID,
	).Scan(&runJSON, &status, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint: load run: %w", err)
	}

	var run TuneRun
	if err := json.Unmarshal([]byte(runJSON), &run); err != nil {
		return nil, fmt.Errorf("checkpoint: unmarshal run: %w", err)
	}
	return &run, nil
}

// ListRuns returns all run IDs, ordered by most recent first.
func (s *SQLiteCheckpointStore) ListRuns() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(
		`SELECT run_id FROM tune_runs
		 ORDER BY updated_at DESC LIMIT 50`,
	)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: list runs: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// DeleteRun removes a run from the checkpoint store.
func (s *SQLiteCheckpointStore) DeleteRun(runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`DELETE FROM tune_runs WHERE run_id = ?`, runID)
	return err
}

// Close closes the database connection.
func (s *SQLiteCheckpointStore) Close() error {
	return s.db.Close()
}
