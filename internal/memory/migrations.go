// migrations.go — versioned schema migrations for Wukong-owned
// memory-subsystem tables (roadmap P0-3). The long-term memory
// entries themselves are owned by trpc-agent-go; only the
// Wukong-side metadata table lives here.
package memory

import "github.com/km269/wukong/internal/migration"

// migrations carries the memory metadata table.
var migrations = []migration.Migration{
	{
		Name: "memory.0001_metadata",
		Stmt: `
		CREATE TABLE IF NOT EXISTS memory_metadata (
			memory_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			reference_count INTEGER DEFAULT 0,
			importance TEXT DEFAULT 'low',
			last_referenced_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (memory_id, user_id)
		);
		CREATE INDEX IF NOT EXISTS idx_metadata_user
			ON memory_metadata(user_id);
		CREATE INDEX IF NOT EXISTS idx_metadata_ref_count
			ON memory_metadata(reference_count DESC);
		CREATE INDEX IF NOT EXISTS idx_metadata_importance
			ON memory_metadata(importance);
	`,
	},
}

// Migrations returns the memory-subsystem migration set (for
// `wukong migrate`).
func Migrations() []migration.Migration {
	return append([]migration.Migration{}, migrations...)
}
