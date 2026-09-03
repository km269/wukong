// migrations.go — versioned schema migrations for the todo store
// (roadmap P0-3).
package todo

import "github.com/km269/wukong/internal/migration"

// migrations carries the tasks table. SQL verbatim from the legacy
// initSchema.
var migrations = []migration.Migration{
	{
		Name: "todo.0001_tasks",
		Stmt: `
			CREATE TABLE IF NOT EXISTS tasks (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				description TEXT DEFAULT '',
				status TEXT DEFAULT 'pending',
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)
		`,
	},
}

// Migrations returns the todo migration set (for `wukong migrate`).
func Migrations() []migration.Migration {
	return append([]migration.Migration{}, migrations...)
}
