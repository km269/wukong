// Package migration provides the versioned schema-migration layer
// for every Wukong-owned SQLite table (roadmap P0-3).
//
// Before this package, each subsystem ran ad-hoc
// "CREATE TABLE IF NOT EXISTS" statements at init time, so schema
// evolution had no history and no single place to look at. Now each
// subsystem owns a list of named migrations (see the migrations.go
// files across internal/) that are applied exactly once per
// database file and recorded in the wukong_schema_migrations
// bookkeeping table. `wukong migrate` applies every known migration
// set up front; runtime init still applies pending ones lazily so
// existing deployments keep working without the command.
//
// Conventions:
//   - Migration names are globally unique and namespaced by
//     subsystem with a sequence number: "recall.0001_base".
//   - Statements keep their original "IF NOT EXISTS" form so a
//     migration applied to a pre-migration database (tables already
//     present from the legacy init path) is a harmless no-op.
//   - Each migration runs inside a transaction (SQLite DDL is
//     transactional); a failure leaves no partial state and the
//     migration is not recorded.
//   - Optional=true marks migrations that may legitimately fail on
//     some builds (e.g. FTS5 availability). Failures are logged and
//     the migration is skipped but NOT recorded, so a later run on a
//     capable build applies it.
package migration

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
)

// Migration is a single named schema change.
type Migration struct {
	// Name is the globally unique migration identifier, namespaced
	// by subsystem with a sequence number (e.g. "todo.0001_tasks").
	Name string
	// Stmt is the SQL to apply. It may contain multiple
	// statements, including trigger bodies.
	Stmt string
	// Optional marks migrations whose failure is tolerable (e.g.
	// FTS5 unavailable in the SQLite build). Failures are logged
	// and skipped without recording; required migrations abort.
	Optional bool
}

// bookkeepingSchema creates the version table. It lives in every
// database file that hosts Wukong-owned tables; names are globally
// unique so one table serves all subsystems sharing a file.
const bookkeepingSchema = `
CREATE TABLE IF NOT EXISTS wukong_schema_migrations (
	name       TEXT PRIMARY KEY,
	applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`

// mu serializes Apply calls within the process. Subsystems sharing
// one SQLite file bootstrap sequentially today, but the bookkeeping
// table must not race even if that ever changes.
var mu sync.Mutex

// Apply applies every not-yet-recorded migration in order. Each
// migration runs in its own transaction together with its
// bookkeeping insert. Required-migration failures abort with the
// remaining migrations unapplied; optional-migration failures are
// logged and skipped (not recorded). Already-applied migrations are
// skipped, so Apply is idempotent and safe to call at every boot.
func Apply(ctx context.Context, db *sql.DB, migrations []Migration) error {
	mu.Lock()
	defer mu.Unlock()

	if _, err := db.ExecContext(ctx, bookkeepingSchema); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	applied := make(map[string]bool)
	rows, err := db.QueryContext(ctx,
		`SELECT name FROM wukong_schema_migrations`)
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return fmt.Errorf("scan applied migration: %w", err)
		}
		applied[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate applied migrations: %w", err)
	}
	rows.Close()

	for _, m := range migrations {
		if applied[m.Name] {
			continue
		}
		if err := applyOne(ctx, db, m); err != nil {
			if m.Optional {
				slog.Warn("migration: optional migration failed, skipping",
					slog.String("migration", m.Name),
					slog.String("error", err.Error()))
				continue
			}
			return fmt.Errorf("apply %s: %w", m.Name, err)
		}
		slog.Info("migration: applied",
			slog.String("migration", m.Name))
	}
	return nil
}

// applyOne runs a single migration and its bookkeeping insert in
// one transaction.
func applyOne(ctx context.Context, db *sql.DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.Stmt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO wukong_schema_migrations(name) VALUES (?)`,
		m.Name,
	); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return tx.Commit()
}

// Applied lists the migration names recorded in the database,
// sorted. Used by `wukong migrate` for reporting.
func Applied(ctx context.Context, db *sql.DB) ([]string, error) {
	if _, err := db.ExecContext(ctx, bookkeepingSchema); err != nil {
		return nil, fmt.Errorf("create schema_migrations table: %w", err)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT name FROM wukong_schema_migrations ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
