// Package util provides shared utility types and helpers.
package util

import (
	"database/sql"
	"fmt"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// DatabasePool provides a shared SQLite connection pool that multiple
// subsystems (session, memory, todo, recall) can use without opening
// separate connections to the same database file.
//
// SQLite supports multiple connections, but each connection maintains
// its own cache and write-ahead log state. Sharing a single pool
// simplifies lifecycle management and reduces resource contention.
type DatabasePool struct {
	mu sync.Mutex
	db *sql.DB

	path string
}

// NewDatabasePool creates a new shared database pool.
// The underlying database connection is opened lazily on first use.
func NewDatabasePool(path string) *DatabasePool {
	return &DatabasePool{path: path}
}

// GetDB returns the shared database connection, opening it if necessary.
// This is safe for concurrent use. The caller must NOT close the returned
// *sql.DB; lifecycle is managed by DatabasePool.Close.
func (p *DatabasePool) GetDB() (*sql.DB, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db != nil {
		return p.db, nil
	}

	db, err := sql.Open("sqlite3", p.path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite pool %q: %w", p.path, err)
	}

	// Optimize SQLite for concurrent access from multiple goroutines.
	// WAL mode allows concurrent reads and a single writer.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Enable WAL mode for better concurrency
	_, _ = db.Exec("PRAGMA journal_mode=WAL")
	// Enable foreign keys
	_, _ = db.Exec("PRAGMA foreign_keys=ON")
	// Use normal synchronization for safety
	_, _ = db.Exec("PRAGMA synchronous=NORMAL")

	p.db = db
	return db, nil
}

// Close closes the shared database connection.
// It is safe to call multiple times.
func (p *DatabasePool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db == nil {
		return nil
	}

	err := p.db.Close()
	p.db = nil
	return err
}
