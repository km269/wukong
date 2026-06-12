// Package session provides session storage management for wukong.
// It wraps tRPC-Agent-Go's session service to provide local
// persistent conversation history.
package session

import (
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// SessionService wraps session.Service with a database handle for
// proper cleanup when using SQLite backend.
type SessionService struct {
	session.Service
	pool *util.DatabasePool
}

// NewSessionService creates a new session service based on configuration.
// It accepts an optional shared DatabasePool; if nil and the backend is
// SQLite, it creates its own pool from the config path.
func NewSessionService(
	cfg *config.SessionConfig,
	pool *util.DatabasePool,
) (*SessionService, error) {
	switch cfg.Backend {
	case "sqlite":
		return newSQLiteService(cfg, pool)
	case "memory":
		return &SessionService{
			Service: newInMemoryService(cfg),
		}, nil
	default:
		return nil, fmt.Errorf(
			"unsupported session backend: %s", cfg.Backend,
		)
	}
}

// Close releases the underlying resources.
// When using SQLite with a shared DatabasePool, the database
// connection is NOT closed here — it is managed by the pool.
// Only non-DB resources (workers, channels) are released.
func (s *SessionService) Close() error {
	// Delegate to underlying service; the SQLite service will close
	// channels, stop workers, and close the DB handle. The DB handle
	// close is safe because sql.DB supports multiple Close() calls
	// (the second call is a no-op). However, to avoid any risk of
	// premature DB close while memory service is still running, we
	// let the DatabasePool manage the final DB lifecycle.
	if closer, ok := s.Service.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// newSQLiteService creates a SQLite-backed session service.
func newSQLiteService(
	cfg *config.SessionConfig,
	pool *util.DatabasePool,
) (*SessionService, error) {
	if pool == nil {
		var err error
		dbPath := config.ResolvePath(cfg.DBPath)
		pool = util.NewDatabasePool(dbPath)
		defer func() {
			if err != nil {
				pool.Close()
			}
		}()
	}

	db, err := pool.GetDB()
	if err != nil {
		return nil, fmt.Errorf("get db: %w", err)
	}

	opts := []sessionsqlite.ServiceOpt{
		sessionsqlite.WithSessionEventLimit(cfg.EventLimit),
	}
	if cfg.TTL > 0 {
		opts = append(opts, sessionsqlite.WithSessionTTL(cfg.TTL))
	}

	svc, err := sessionsqlite.NewService(db, opts...)
	if err != nil {
		return nil, fmt.Errorf("create sqlite session: %w", err)
	}

	// Note: db lifecycle is now managed by the pool, not by us.
	// We keep a reference to the pool for lifecycle awareness.
	return &SessionService{Service: svc, pool: pool}, nil
}

// newInMemoryService creates an in-memory session service.
func newInMemoryService(cfg *config.SessionConfig) session.Service {
	opts := []sessioninmemory.ServiceOpt{
		sessioninmemory.WithSessionEventLimit(cfg.EventLimit),
	}
	if cfg.TTL > 0 {
		opts = append(opts, sessioninmemory.WithSessionTTL(cfg.TTL))
	}
	return sessioninmemory.NewSessionService(opts...)
}
