package okf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConcept_WithFrontmatter(t *testing.T) {
	content := []byte(`---
type: table
title: Orders Table
description: Customer order records
tags: [ecommerce, transactional]
---

# Orders Table

Contains all customer orders.

## Related
- [Customers](customers.md)
`)

	concept, err := ParseConcept(content, "tables/orders.md")
	if err != nil {
		t.Fatalf("ParseConcept failed: %v", err)
	}

	if concept.Frontmatter.Type != "table" {
		t.Errorf("type = %q, want %q",
			concept.Frontmatter.Type, "table")
	}
	if concept.Frontmatter.Title != "Orders Table" {
		t.Errorf("title = %q", concept.Frontmatter.Title)
	}
	if concept.ID != "tables/orders" {
		t.Errorf("ID = %q, want %q",
			concept.ID, "tables/orders")
	}
	if len(concept.Links) != 1 ||
		concept.Links[0] != "customers.md" {
		t.Errorf("Links = %v, want [customers.md]",
			concept.Links)
	}
	if concept.Body == "" {
		t.Error("Body should not be empty")
	}
}

func TestParseConcept_NoFrontmatter(t *testing.T) {
	// A file without frontmatter should still parse,
	// defaulting type to "concept".
	content := []byte("# Just a title\n\nNo frontmatter here.")
	concept, err := ParseConcept(content, "plain.md")
	if err != nil {
		t.Fatalf("ParseConcept failed: %v", err)
	}

	if concept.Frontmatter.Type != DefaultType {
		t.Errorf("type = %q, want %q (default)",
			concept.Frontmatter.Type, DefaultType)
	}
}

func TestParseConcept_NoTypeField(t *testing.T) {
	// SKILL.md compatibility: has frontmatter but no type.
	content := []byte(`---
name: code-reviewer
description: Reviews code
---

# Code Reviewer
`)
	concept, err := ParseConcept(content, "skills/reviewer.md")
	if err != nil {
		t.Fatalf("ParseConcept failed: %v", err)
	}

	// Should default to "concept" per OKF consumer tolerance.
	if concept.Frontmatter.Type != DefaultType {
		t.Errorf("type = %q, want %q (default for missing type)",
			concept.Frontmatter.Type, DefaultType)
	}

	// Extra fields should be preserved.
	if name, ok := concept.Frontmatter.Extra["name"]; ok {
		if name != "code-reviewer" {
			t.Errorf("extra.name = %v", name)
		}
	} else {
		t.Error("extra field 'name' not preserved")
	}
}

func TestLoadBundle(t *testing.T) {
	dir := t.TempDir()

	// Create test files.
	files := map[string]string{
		"index.md": "---\ntype: index\nokf_version: \"0.1\"\n---\n\n# Index\n",
		"log.md":   "---\ntype: changelog\n---\n\n# Log\n",
		"tables/orders.md": "---\ntype: table\ntitle: Orders\n---\n\n# Orders\n\nSee [customers](../tables/customers.md)\n",
		"tables/customers.md": "---\ntype: table\ntitle: Customers\n---\n\n# Customers\n",
		"api/checkout.md": "---\ntype: api\n---\n\n# Checkout API\n",
	}

	for path, content := range files {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	bundle, warnings := LoadBundle(dir)
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	if len(bundle.Concepts) != 3 {
		t.Errorf("concepts = %d, want 3",
			len(bundle.Concepts))
	}
	if bundle.Index == nil {
		t.Error("Index should not be nil")
	}
	if bundle.Log == nil {
		t.Error("Log should not be nil")
	}
	if bundle.Version != "0.1" {
		t.Errorf("Version = %q, want %q",
			bundle.Version, "0.1")
	}

	// Verify concepts sorted by ID.
	if bundle.Concepts[0].ID != "api/checkout" {
		t.Errorf("first concept ID = %q",
			bundle.Concepts[0].ID)
	}

	// Verify cross-file links.
	orders := bundle.FindConcept("tables/orders")
	if orders == nil {
		t.Fatal("tables/orders concept not found")
	}
	if len(orders.Links) != 1 {
		t.Errorf("orders links = %v", orders.Links)
	}

	// Resolve link.
	resolved := ResolveLink(orders.ID, orders.Links[0])
	if resolved != "tables/customers" {
		t.Errorf("resolved link = %q, want %q",
			resolved, "tables/customers")
	}

	// Verify ConceptsByType.
	tables := bundle.ConceptsByType("table")
	if len(tables) != 2 {
		t.Errorf("tables = %d, want 2", len(tables))
	}

	// Verify AllTypes.
	types := bundle.AllTypes()
	if len(types) != 2 {
		t.Errorf("types = %v, want 2 types", types)
	}
}

func TestLoadBundle_SkipsNonCompliantFile(t *testing.T) {
	dir := t.TempDir()

	// A file with broken YAML frontmatter.
	os.WriteFile(filepath.Join(dir, "broken.md"),
		[]byte("---\ntype: [invalid yaml\n---\n\nbody"), 0644)

	// A valid file.
	os.WriteFile(filepath.Join(dir, "valid.md"),
		[]byte("---\ntype: concept\n---\n\n# Valid"), 0644)

	bundle, warnings := LoadBundle(dir)

	// broken.md should be skipped, not cause a fatal error.
	if len(bundle.Concepts) != 1 {
		t.Errorf("concepts = %d, want 1 (broken file skipped)",
			len(bundle.Concepts))
	}
	if len(warnings) == 0 {
		t.Error("expected at least one warning for broken file")
	}
}

func TestWriteBundle_RoundTrip(t *testing.T) {
	srcDir := t.TempDir()

	// Create source bundle.
	os.WriteFile(filepath.Join(srcDir, "concept1.md"),
		[]byte("---\ntype: table\ntitle: Table 1\n---\n\n# Table 1\n"),
		0644)

	bundle, _ := LoadBundle(srcDir)
	if len(bundle.Concepts) != 1 {
		t.Fatalf("load: concepts = %d", len(bundle.Concepts))
	}

	// Write to new directory.
	dstDir := t.TempDir()
	err := WriteBundle(bundle, dstDir, DefaultWriteOptions())
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}

	// Verify index.md was generated.
	if _, err := os.Stat(filepath.Join(dstDir, IndexFile)); err != nil {
		t.Errorf("index.md not generated: %v", err)
	}

	// Verify log.md was generated.
	if _, err := os.Stat(filepath.Join(dstDir, LogFile)); err != nil {
		t.Errorf("log.md not generated: %v", err)
	}

	// Verify concept file exists.
	if _, err := os.Stat(filepath.Join(dstDir, "concept1.md")); err != nil {
		t.Errorf("concept1.md not written: %v", err)
	}

	// Reload and verify.
	bundle2, _ := LoadBundle(dstDir)
	if len(bundle2.Concepts) != 1 {
		t.Errorf("reload: concepts = %d", len(bundle2.Concepts))
	}
	if bundle2.Concepts[0].Frontmatter.Type != "table" {
		t.Errorf("reload: type = %q",
			bundle2.Concepts[0].Frontmatter.Type)
	}
}

func TestAppendLogEntry(t *testing.T) {
	dir := t.TempDir()

	// First entry (creates log.md).
	err := AppendLogEntry(dir, "Added", "tables/orders.md", "initial import")
	if err != nil {
		t.Fatalf("AppendLogEntry: %v", err)
	}

	// Second entry (appends to same date section).
	err = AppendLogEntry(dir, "Modified", "tables/orders.md", "updated schema")
	if err != nil {
		t.Fatalf("AppendLogEntry 2: %v", err)
	}

	// Read and verify.
	content, _ := os.ReadFile(filepath.Join(dir, LogFile))
	str := string(content)

	if !contains(str, "Added: tables/orders.md") {
		t.Error("first entry not found in log")
	}
	if !contains(str, "Modified: tables/orders.md") {
		t.Error("second entry not found in log")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(len(s) == 0 || indexOf(s, substr) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
