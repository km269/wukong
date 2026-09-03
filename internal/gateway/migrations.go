// migrations.go — versioned schema migrations for the gateway
// session store (roadmap P0-3).
package gateway

import "github.com/km269/wukong/internal/migration"

// migrations carries the gateway session mapping table.
var migrations = []migration.Migration{
	{
		Name: "gateway.0001_sessions",
		Stmt: `
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
		`,
	},
}

// Migrations returns the gateway migration set (for
// `wukong migrate`).
func Migrations() []migration.Migration {
	return append([]migration.Migration{}, migrations...)
}
