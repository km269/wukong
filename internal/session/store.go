// Package session provides session storage management for wukong.
// It wraps tRPC-Agent-Go's session service to provide local
// persistent conversation history.
package session

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

// NewSessionService creates a new session service based on configuration.
func NewSessionService(cfg *config.SessionConfig) (session.Service, error) {
	switch cfg.Backend {
	case "sqlite":
		return newSQLiteService(cfg)
	case "memory":
		return newInMemoryService(cfg), nil
	default:
		return nil, fmt.Errorf(
			"unsupported session backend: %s", cfg.Backend,
		)
	}
}

// newSQLiteService creates a SQLite-backed session service.
func newSQLiteService(cfg *config.SessionConfig) (session.Service, error) {
	db, err := sql.Open("sqlite3", cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
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
	return svc, nil
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
