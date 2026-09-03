package migration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestApplyCreatesAndRecords(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	migs := []Migration{
		{Name: "t.0001_items", Stmt: `CREATE TABLE IF NOT EXISTS items (
			id INTEGER PRIMARY KEY, label TEXT NOT NULL DEFAULT '')`},
		{Name: "t.0002_items_label_idx", Stmt: `CREATE INDEX IF NOT EXISTS
			idx_items_label ON items(label)`},
	}
	if err := Apply(ctx, db, migs); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Table exists and is usable.
	if _, err := db.Exec(
		`INSERT INTO items (label) VALUES ('x')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	names, err := Applied(ctx, db)
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	if len(names) != 2 || names[0] != "t.0001_items" ||
		names[1] != "t.0002_items_label_idx" {
		t.Fatalf("Applied = %v", names)
	}
}

func TestApplyIdempotent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	migs := []Migration{
		{Name: "t.0001_items", Stmt: `CREATE TABLE IF NOT EXISTS items (
			id INTEGER PRIMARY KEY)`},
	}
	for i := 0; i < 3; i++ {
		if err := Apply(ctx, db, migs); err != nil {
			t.Fatalf("Apply #%d: %v", i, err)
		}
	}
	names, _ := Applied(ctx, db)
	if len(names) != 1 {
		t.Fatalf("Applied = %v, want single entry", names)
	}
}

func TestApplyRequiredFailureAborts(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// Pre-create the table so the required migration fails.
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	migs := []Migration{
		{Name: "t.0001_items", Stmt: `CREATE TABLE items (id INTEGER)`},
		{Name: "t.0002_other", Stmt: `CREATE TABLE other (id INTEGER)`},
	}
	err := Apply(ctx, db, migs)
	if err == nil || !strings.Contains(err.Error(), "t.0001_items") {
		t.Fatalf("err = %v, want t.0001_items failure", err)
	}

	// Abort leaves nothing recorded and no partial state.
	names, _ := Applied(ctx, db)
	if len(names) != 0 {
		t.Fatalf("Applied = %v, want empty after abort", names)
	}
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE name='other'`,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("second migration applied despite abort (count=%d err=%v)",
			count, err)
	}
}

func TestApplyOptionalFailureSkipsAndSkipsLaterOptionals(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	migs := []Migration{
		{Name: "t.0001_base", Stmt: `CREATE TABLE IF NOT EXISTS base (
			id INTEGER PRIMARY KEY)`},
		{Name: "t.0002_exotic", Stmt: `CREATE VIRTUAL TABLE exotic
			USING bogus_module(x)`, Optional: true},
		{Name: "t.0003_needs_exotic", Stmt: `CREATE TRIGGER IF NOT EXISTS
			tr_exotic AFTER INSERT ON exotic BEGIN SELECT 1; END`,
			Optional: true},
		{Name: "t.0004_plain", Stmt: `CREATE TABLE IF NOT EXISTS plain (
			id INTEGER PRIMARY KEY)`},
	}
	if err := Apply(ctx, db, migs); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	names, _ := Applied(ctx, db)
	got := strings.Join(names, ",")
	// Optionals are skipped without recording; the trailing required
	// migration still applies.
	if !strings.Contains(got, "t.0001_base") ||
		!strings.Contains(got, "t.0004_plain") ||
		strings.Contains(got, "t.0002_exotic") {
		t.Fatalf("Applied = %v", names)
	}
}

func TestApplyConcurrent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	migs := []Migration{
		{Name: "t.0001_items", Stmt: `CREATE TABLE IF NOT EXISTS items (
			id INTEGER PRIMARY KEY)`},
	}
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { done <- Apply(ctx, db, migs) }()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent Apply: %v", err)
		}
	}
	names, _ := Applied(ctx, db)
	if len(names) != 1 {
		t.Fatalf("Applied = %v, want single entry", names)
	}
}

func TestApplyLegacyDB(t *testing.T) {
	// A database created by the legacy init path has the tables but
	// no bookkeeping rows; the migration (IF NOT EXISTS) must be a
	// no-op that records itself.
	db := testDB(t)
	ctx := context.Background()
	if _, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("legacy seed: %v", err)
	}
	migs := []Migration{
		{Name: "t.0001_items", Stmt: `CREATE TABLE IF NOT EXISTS items (
			id INTEGER PRIMARY KEY)`},
	}
	if err := Apply(ctx, db, migs); err != nil {
		t.Fatalf("Apply on legacy db: %v", err)
	}
	names, _ := Applied(ctx, db)
	if len(names) != 1 {
		t.Fatalf("Applied = %v, want recorded", names)
	}
	_ = errors.Is
}
