// Package util provides shared utility types and helpers.
package util

import (
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
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

	db, err := sql.Open("sqlite", p.path+
		"?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=ON"+
		"&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite pool %q: %w", p.path, err)
	}

	// Allow up to 4 concurrent connections in WAL mode.
	// WAL supports multiple readers + one writer with page-level
	// locking. _busy_timeout=5000ms handles transient lock contention
	// by waiting instead of immediately returning SQLITE_BUSY.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)

	// Apply PRAGMAs (DSN params handle most, these are safety nets).
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		Logger.Warn("Failed to apply PRAGMA journal_mode", "error", err.Error())
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		Logger.Warn("Failed to apply PRAGMA foreign_keys", "error", err.Error())
	}
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		Logger.Warn("Failed to apply PRAGMA synchronous", "error", err.Error())
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		Logger.Warn("Failed to apply PRAGMA busy_timeout", "error", err.Error())
	}

	p.db = db
	return db, nil
}

// Close closes the shared database connection after performing a
// WAL checkpoint to flush all pending writes to the main database
// file. This ensures zero data loss on graceful shutdown.
//
// If the database has already been closed by another consumer
// (shared-pool mode), the checkpoint is silently skipped. WAL data
// is still safe and will be replayed on next open.
//
// It is safe to call multiple times; subsequent calls are no-ops.
func (p *DatabasePool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.db == nil {
		return nil
	}

	// Only checkpoint if the database is still open. In shared-pool
	// mode, another service (session/memory) may have already closed
	// the underlying *sql.DB. The WAL will be replayed automatically
	// when the database is next opened.
	if err := p.db.Ping(); err == nil {
		// Flush WAL to main database file for zero-loss shutdown.
		_, ckErr := p.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		if ckErr != nil {
			Logger.Warn("WAL checkpoint before close failed "+
				"(non-fatal: WAL will replay on next open)",
				"error", ckErr.Error())
		}
	}

	err := p.db.Close()
	p.db = nil
	return err
}

// ---------------------------------------------------------------------------
// MultiPool — named pools for independent database files
// ---------------------------------------------------------------------------

// MultiPool manages a collection of named DatabasePool instances,
// enabling subsystems to use independent database files while
// sharing a single management point for lifecycle operations.
//
// Usage:
//
//	mp := NewMultiPool("wukong.db")
//	sharedPool := mp.Shared()              // returns pool for "wukong.db"
//	memPool := mp.GetOrCreate("memory.db") // returns pool for "memory.db"
//	defer mp.Close()                       // closes all pools
type MultiPool struct {
	mu      sync.RWMutex
	pools   map[string]*DatabasePool
	baseDir string // directory for relative paths
}

// NewMultiPool creates a MultiPool with a shared default pool.
// The sharedPath is used for subsystems that don't specify their
// own database path.
func NewMultiPool(sharedPath string) *MultiPool {
	mp := &MultiPool{
		pools: make(map[string]*DatabasePool),
	}
	mp.pools["shared"] = NewDatabasePool(sharedPath)
	return mp
}

// Shared returns the default shared database pool.
// This is the pool for subsystems that use the common database.
func (mp *MultiPool) Shared() *DatabasePool {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.pools["shared"]
}

// GetOrCreate returns an existing pool by name, or creates a new
// one at the given path. This enables subsystems to have their own
// independent databases (e.g., "memory.db", "todos.db").
//
// If the pool already exists, the path argument is ignored.
func (mp *MultiPool) GetOrCreate(name, dbPath string) *DatabasePool {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if pool, ok := mp.pools[name]; ok {
		return pool
	}

	pool := NewDatabasePool(dbPath)
	mp.pools[name] = pool
	return pool
}

// Get returns the pool with the given name, or nil if it does not exist.
func (mp *MultiPool) Get(name string) *DatabasePool {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return mp.pools[name]
}

// Close closes all managed database pools, performing WAL
// checkpointing on each before closing.
func (mp *MultiPool) Close() error {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	var errs []error
	for name, pool := range mp.pools {
		if err := pool.Close(); err != nil {
			errs = append(errs,
				fmt.Errorf("close pool %q: %w", name, err))
		}
	}
	mp.pools = make(map[string]*DatabasePool)

	if len(errs) > 0 {
		return fmt.Errorf("multi-pool close errors: %v", errs)
	}
	return nil
}

// PoolCount returns the number of managed pools.
func (mp *MultiPool) PoolCount() int {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	return len(mp.pools)
}
