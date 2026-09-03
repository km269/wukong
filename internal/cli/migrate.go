// migrate.go implements the "migrate" command: apply every known
// schema migration to the shared Wukong database up front
// (roadmap P0-3, `yao migrate` parity). Runtime init applies
// pending migrations lazily too, so running this command is always
// optional — it exists for ops workflows (CI pre-flight, deploy
// hooks) and for inspecting migration state.
package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/cortex"
	"github.com/km269/wukong/internal/evolution"
	"github.com/km269/wukong/internal/memory"
	"github.com/km269/wukong/internal/migration"
	"github.com/km269/wukong/internal/recall"
	"github.com/km269/wukong/internal/session"
	"github.com/km269/wukong/internal/todo"
	"github.com/km269/wukong/internal/util"
)

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply all pending schema migrations to the Wukong database",
		Long: `Apply every known schema migration to the shared Wukong
database (wukong.db) and print the resulting migration state.

Wukong-owned tables (recall, evolution, model events, todo, memory
metadata) are versioned in the wukong_schema_migrations bookkeeping
table. Subsystems apply pending migrations lazily at startup, so
running this command is optional; it is provided for deploy hooks
and CI pre-flight checks. Gateway and search-tuning stores manage
their own database files and initialize on demand.

Migrations are idempotent: already-applied ones are skipped.`,
		RunE: runMigrate,
	}
}

func runMigrate(cmd *cobra.Command, args []string) error {
	loader, err := config.NewLoader("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	pool := util.NewMultiPool(config.ResolvePath(cfg.Session.DBPath))
	defer func() { _ = pool.Close() }()
	db, err := pool.Shared().GetDB()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	all := [][]migration.Migration{
		recall.Migrations(),
		evolution.Migrations(),
		session.Migrations(),
		todo.Migrations(),
		memory.Migrations(),
		cortex.LexicalMigrations(),
	}

	ctx := context.Background()
	for _, set := range all {
		if err := migration.Apply(ctx, db, set); err != nil {
			return err
		}
	}

	names, err := migration.Applied(ctx, db)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Schema up to date (%d migrations applied):\n", len(names))
	for _, n := range names {
		fmt.Fprintf(out, "  ✓ %s\n", n)
	}
	return nil
}
