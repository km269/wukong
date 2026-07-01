package gateway

import (
	"database/sql"
	"fmt"
	"sync"

	"github.com/km269/wukong/internal/util"
)

// GatewaySessionStore manages the mapping between external platform
// user/conversation identifiers and internal Wukong user/session
// identifiers. This ensures each platform user has a consistent
// session context across multiple messages.
//
// Storage: SQLite (shared wukong.db), fallback to in-memory.
type GatewaySessionStore struct {
	mu      sync.RWMutex
	db      *sql.DB
	enabled bool
}

// SessionMapping records the relationship between a platform identity
// and a Wukong identity.
type SessionMapping struct {
	Platform      string
	PlatformUser  string
	Conversation  string
	WukongUser    string
	WukongSession string
}

// NewGatewaySessionStore creates a new session store backed by the
// shared database pool. If pool is nil, operates in memory-only mode
// (mappings are not persisted across restarts).
func NewGatewaySessionStore(pool *util.DatabasePool) *GatewaySessionStore {
	store := &GatewaySessionStore{}
	if pool == nil {
		return store
	}

	db, err := pool.GetDB()
	if err != nil {
		util.Logger.Warn("gateway: get db failed, using in-memory mode",
			"error", err.Error())
		return store
	}

	store.db = db
	store.enabled = true
	store.ensureTable()
	return store
}

// GetOrCreateSession looks up an existing session mapping for the
// given platform user+conversation, or creates a new one if none
// exists.
//
// The wukongUser and wukongSession parameters are the pre-built
// identifiers from the Channel (via BuildUserID/BuildSessionID).
func (s *GatewaySessionStore) GetOrCreateSession(
	platform, platformUser, conversation string,
	wukongUser, wukongSession string,
) (*SessionMapping, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled {
		return &SessionMapping{
			Platform:      platform,
			PlatformUser:  platformUser,
			Conversation:  conversation,
			WukongUser:    wukongUser,
			WukongSession: wukongSession,
		}, nil
	}

	// Try to find an existing mapping.
	var existing SessionMapping
	err := s.db.QueryRow(
		`SELECT platform, platform_user, conversation_id,
		        wukong_user, wukong_session
		 FROM gateway_sessions
		 WHERE platform = ? AND platform_user = ?
		   AND conversation_id = ?`,
		platform, platformUser, conversation,
	).Scan(
		&existing.Platform, &existing.PlatformUser,
		&existing.Conversation, &existing.WukongUser,
		&existing.WukongSession,
	)
	if err == nil {
		return &existing, nil
	}

	// No existing mapping; insert a new one.
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO gateway_sessions
		 (platform, platform_user, conversation_id,
		  wukong_user, wukong_session)
		 VALUES (?, ?, ?, ?, ?)`,
		platform, platformUser, conversation,
		wukongUser, wukongSession,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"gateway session: insert mapping: %w", err)
	}

	return &SessionMapping{
		Platform:      platform,
		PlatformUser:  platformUser,
		Conversation:  conversation,
		WukongUser:    wukongUser,
		WukongSession: wukongSession,
	}, nil
}

// Close releases resources held by the session store.
func (s *GatewaySessionStore) Close() error {
	// The DB connection is owned by the DatabasePool and should
	// not be closed here.
	return nil
}

// ensureTable creates the gateway_sessions table if it doesn't exist.
func (s *GatewaySessionStore) ensureTable() {
	if s.db == nil {
		return
	}
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS gateway_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			platform TEXT NOT NULL,
			platform_user TEXT NOT NULL,
			conversation_id TEXT NOT NULL DEFAULT '',
			wukong_user TEXT NOT NULL,
			wukong_session TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(platform, platform_user, conversation_id)
		)
	`)
	if err != nil {
		util.Logger.Warn("gateway: failed to create sessions table",
			"error", err.Error())
	}
}
