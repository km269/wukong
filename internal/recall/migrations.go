// migrations.go — versioned schema migrations for the recall store
// (roadmap P0-3). SQL is carried over verbatim from the legacy
// initSchema; the IF NOT EXISTS forms keep pre-migration databases
// compatible.
package recall

import "github.com/km269/wukong/internal/migration"

// migrations is the required migration set: the base recall table
// and its indexes.
var migrations = []migration.Migration{
	{
		Name: "recall.0001_base",
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
}

// ftsMigrations adds the FTS5 index and its sync triggers. They are
// Optional: SQLite builds without FTS5 skip them (and full-text
// search falls back to LIKE), matching the legacy lenient behavior.
var ftsMigrations = []migration.Migration{
	{
		Name:     "recall.0002_fts",
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
		Name:     "recall.0003_fts_triggers",
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
				INSERT INTO chat_recall_fts(chat_recall_fts, rowid, content)
				VALUES ('delete', old.id, old.content);
			END;
			CREATE TRIGGER IF NOT EXISTS recall_fts_update
			AFTER UPDATE ON chat_recall
			BEGIN
				INSERT INTO chat_recall_fts(chat_recall_fts, rowid, content)
				VALUES ('delete', old.id, old.content);
				INSERT INTO chat_recall_fts(rowid, content)
				VALUES (new.id, new.content);
			END
		`,
	},
}

// Migrations returns every recall migration, including the optional
// FTS set. Consumed by `wukong migrate` (which applies strictly) —
// runtime initSchema applies the optional set leniently.
func Migrations() []migration.Migration {
	return append(append([]migration.Migration{}, migrations...), ftsMigrations...)
}
