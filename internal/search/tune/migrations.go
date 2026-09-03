// migrations.go — versioned schema migrations for the search tuning
// checkpoint store (roadmap P0-3).
package tune

import "github.com/km269/wukong/internal/migration"

// migrations carries the tuning runs table.
var migrations = []migration.Migration{
	{
		Name: "tune.0001_runs",
		Stmt: `
		CREATE TABLE IF NOT EXISTS tune_runs (
			run_id       TEXT PRIMARY KEY,
			status       TEXT NOT NULL,
			run_json     TEXT NOT NULL,
			created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_tune_status
			ON tune_runs(status);
		`,
	},
}

// Migrations returns the tuning migration set (for
// `wukong migrate`).
func Migrations() []migration.Migration {
	return append([]migration.Migration{}, migrations...)
}
