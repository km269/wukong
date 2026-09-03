// migrations.go — versioned schema migrations for the evolution
// engine store (roadmap P0-3).
package evolution

import "github.com/km269/wukong/internal/migration"

// migrations carries the evolution tables. SQL verbatim from the
// legacy initSchema; IF NOT EXISTS keeps legacy databases working.
var migrations = []migration.Migration{
	{
		Name: "evolution.0001_init",
		Stmt: `
		CREATE TABLE IF NOT EXISTS evolution_history (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			skill_name    TEXT NOT NULL,
			session_id    TEXT NOT NULL DEFAULT '',
			trace_json    TEXT NOT NULL DEFAULT '{}',
			has_issue     INTEGER NOT NULL DEFAULT 0,
			patch_applied INTEGER NOT NULL DEFAULT 0,
			patch_reason  TEXT NOT NULL DEFAULT '',
			patch_confidence REAL NOT NULL DEFAULT 0.0,
			version_before INTEGER NOT NULL DEFAULT 0,
			version_after  INTEGER NOT NULL DEFAULT 0,
			created_at    TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE INDEX IF NOT EXISTS idx_evolution_history_skill
			ON evolution_history(skill_name, created_at);

		CREATE TABLE IF NOT EXISTS evolution_versions (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			skill_name     TEXT NOT NULL,
			version_number INTEGER NOT NULL,
			backup_path    TEXT NOT NULL DEFAULT '',
			file_hash      TEXT NOT NULL DEFAULT '',
			patch_reason   TEXT NOT NULL DEFAULT '',
			created_at     TEXT NOT NULL DEFAULT (datetime('now'))
		);

		CREATE INDEX IF NOT EXISTS idx_evolution_versions_skill
			ON evolution_versions(skill_name, version_number);
		`,
	},
}

// Migrations returns the evolution migration set (for
// `wukong migrate`).
func Migrations() []migration.Migration {
	return append([]migration.Migration{}, migrations...)
}
