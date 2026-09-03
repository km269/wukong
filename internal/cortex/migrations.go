// migrations.go — versioned schema migrations for the CortexDB
// lexical store (roadmap P0-3). SQL verbatim from the legacy
// initSchema; IF NOT EXISTS keeps legacy databases compatible.
//
// Note: the lexical store's base tables mirror the recall package
// schema. On a shared database the IF NOT EXISTS statements are
// no-ops after either subsystem initialized first; migration names
// are per-subsystem so bookkeeping stays consistent on standalone
// databases too.
package cortex

import "github.com/km269/wukong/internal/migration"

var migrations = []migration.Migration{
	{
		Name: "cortex_lexical.0001_base",
		Stmt: `
		CREATE TABLE IF NOT EXISTS chat_recall (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_recall_session
			ON chat_recall(session_id);
		CREATE INDEX IF NOT EXISTS idx_recall_user
			ON chat_recall(user_id);
		CREATE INDEX IF NOT EXISTS idx_recall_created
			ON chat_recall(created_at);
		`,
	},
	{
		Name:     "cortex_lexical.0002_fts",
		Optional: true,
		Stmt: `
		CREATE VIRTUAL TABLE IF NOT EXISTS chat_recall_fts
			USING fts5(
				content,
				content='chat_recall',
				content_rowid='id',
				tokenize='unicode61'
			)
		`,
	},
	{
		Name:     "cortex_lexical.0003_fts_triggers",
		Optional: true,
		Stmt: `
		CREATE TRIGGER IF NOT EXISTS recall_fts_insert
		AFTER INSERT ON chat_recall
		BEGIN
			INSERT INTO chat_recall_fts(rowid, content)
			VALUES (new.id, new.content);
		END;
		CREATE TRIGGER IF NOT EXISTS recall_fts_delete
		AFTER DELETE ON chat_recall
		BEGIN
			INSERT INTO chat_recall_fts(
				chat_recall_fts, rowid, content)
			VALUES ('delete', old.id, old.content);
		END;
		CREATE TRIGGER IF NOT EXISTS recall_fts_update
		AFTER UPDATE ON chat_recall
		BEGIN
			INSERT INTO chat_recall_fts(
				chat_recall_fts, rowid, content)
			VALUES ('delete', old.id, old.content);
			INSERT INTO chat_recall_fts(rowid, content)
			VALUES (new.id, new.content);
		END
		`,
	},
	{
		Name: "cortex_lexical.0004_vec",
		Stmt: `
		CREATE TABLE IF NOT EXISTS chat_recall_vec (
			msg_id INTEGER PRIMARY KEY,
			session_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			vector TEXT NOT NULL,
			content_snippet TEXT NOT NULL,
			FOREIGN KEY (msg_id) REFERENCES chat_recall(id)
				ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_vec_session
			ON chat_recall_vec(session_id);
		CREATE INDEX IF NOT EXISTS idx_vec_user
			ON chat_recall_vec(user_id);
		`,
	},
}

// Migrations returns the lexical-store migration set (for
// `wukong migrate`).
func LexicalMigrations() []migration.Migration {
	return append([]migration.Migration{}, migrations...)
}
