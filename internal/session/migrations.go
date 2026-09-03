// migrations.go — versioned schema migrations for Wukong-owned
// session-subsystem tables (roadmap P0-3). Note: the session and
// memory *service* tables are owned by trpc-agent-go and are out of
// scope here.
package session

import "github.com/km269/wukong/internal/migration"

// migrations carries the model-visible event log. SQL verbatim from
// the legacy inline schema.
var migrations = []migration.Migration{
	{
		Name: "session.0001_model_events",
		Stmt: `
CREATE TABLE IF NOT EXISTS wukong_model_events (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT    NOT NULL,
	user_id    TEXT    NOT NULL,
	seq        INTEGER NOT NULL,
	event_type TEXT    NOT NULL,
	source     TEXT    NOT NULL DEFAULT '',
	payload    TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_model_events_session
	ON wukong_model_events(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_model_events_type
	ON wukong_model_events(event_type);
`,
	},
}

// Migrations returns the session-subsystem migration set (for
// `wukong migrate`).
func Migrations() []migration.Migration {
	return append([]migration.Migration{}, migrations...)
}
