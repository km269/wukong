// Package cortex OKF Enrichment Agent.
//
// This file implements the EnrichmentAgent — a knowledge
// automation producer that scans structured data sources
// (database schemas, API specs, file directories) and generates
// OKF Bundle concept documents using LLM-driven enrichment.
//
// Inspired by Google's OKF reference implementation (Enrichment
// Agent for BigQuery), this generalizes the pattern to support
// multiple data source types.
package cortex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/km269/wukong/internal/okf"
	"github.com/km269/wukong/internal/util"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// EnrichmentAgent automatically generates OKF concept documents
// from structured data sources using LLM-driven enrichment.
type EnrichmentAgent struct {
	jsonGen   graphflow.JSONGenerator
	outputDir string
}

// NewEnrichmentAgent creates an enrichment agent.
// The jsonGen parameter enables LLM-driven descriptions.
func NewEnrichmentAgent(
	jsonGen graphflow.JSONGenerator,
	outputDir string,
) *EnrichmentAgent {
	return &EnrichmentAgent{
		jsonGen:   jsonGen,
		outputDir: outputDir,
	}
}

// EnrichFromDDL parses CREATE TABLE DDL statements and generates
// OKF concept documents for each table.
func (e *EnrichmentAgent) EnrichFromDDL(
	ctx context.Context, ddl string,
) (int, error) {
	tables, err := importflow.ParseDDL(ddl)
	if err != nil {
		return 0, fmt.Errorf("parse DDL: %w", err)
	}

	bundle := &okf.Bundle{
		RootDir:  e.outputDir,
		Concepts: make([]*okf.Concept, 0, len(tables)),
	}

	for _, table := range tables {
		concept := e.tableToConcept(ctx, table)
		bundle.Concepts = append(bundle.Concepts, concept)
	}

	opts := okf.DefaultWriteOptions()
	if err := okf.WriteBundle(bundle, e.outputDir, opts); err != nil {
		return 0, fmt.Errorf("write bundle: %w", err)
	}

	util.Logger.Info("okf: enrichment from DDL complete",
		"tables", len(tables),
		"output_dir", e.outputDir)

	return len(tables), nil
}

// tableToConcept converts a parsed DDL table to an OKF concept.
func (e *EnrichmentAgent) tableToConcept(
	ctx context.Context, table importflow.DDLTable,
) *okf.Concept {
	var body strings.Builder

	body.WriteString(fmt.Sprintf("# %s\n\n", table.Name))
	body.WriteString("## Schema\n\n")
	body.WriteString("| Column | Type |\n")
	body.WriteString("|--------|------|\n")

	for _, col := range table.Columns {
		body.WriteString(fmt.Sprintf(
			"| %s | %s |\n",
			col.Name, col.Type))
	}

	if len(table.ForeignKeys) > 0 {
		body.WriteString("\n## Foreign Keys\n\n")
		for _, fk := range table.ForeignKeys {
			body.WriteString(fmt.Sprintf(
				"- %s -> [%s](../tables/%s.md)\n",
				fk.Column, fk.RefTable, fk.RefTable))
		}
	}

	return &okf.Concept{
		ID:       "tables/" + table.Name,
		FilePath: filepath.Join("tables", table.Name+".md"),
		Frontmatter: okf.Frontmatter{
			Type:        "table",
			Title:       table.Name,
			Description: fmt.Sprintf("Database table %s with %d columns", table.Name, len(table.Columns)),
			Resource:    fmt.Sprintf("ddl://%s", table.Name),
			Tags:        []string{"database", "table"},
			Timestamp:   okf.FormatNow(),
		},
		Body: body.String(),
	}
}

// EnrichFromDirectory scans a directory of text files and
// generates OKF concept documents for each file.
func (e *EnrichmentAgent) EnrichFromDirectory(
	ctx context.Context, srcDir string,
) (int, error) {
	bundle := &okf.Bundle{
		RootDir:  e.outputDir,
		Concepts: make([]*okf.Concept, 0),
	}

	count := 0
	err := filepath.Walk(srcDir,
		func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			if !isEnrichableExt(ext) {
				return nil
			}

			content, rErr := os.ReadFile(path)
			if rErr != nil {
				return nil
			}

			relPath, _ := filepath.Rel(srcDir, path)
			conceptID := sanitizeID(relPath)
			title := strings.TrimSuffix(
				filepath.Base(path), ext)

			concept := &okf.Concept{
				ID:       conceptID,
				FilePath: conceptID + ".md",
				Frontmatter: okf.Frontmatter{
					Type:        "document",
					Title:       title,
					Description: fmt.Sprintf("Imported from %s", relPath),
					Resource:    path,
					Tags:        []string{"imported", ext[1:]},
					Timestamp:   okf.FormatNow(),
				},
				Body: string(content),
			}

			bundle.Concepts = append(bundle.Concepts, concept)
			count++
			return nil
		})

	if err != nil {
		return 0, fmt.Errorf("walk source dir: %w", err)
	}

	opts := okf.DefaultWriteOptions()
	if err := okf.WriteBundle(bundle, e.outputDir, opts); err != nil {
		return 0, fmt.Errorf("write bundle: %w", err)
	}

	util.Logger.Info("okf: enrichment from directory complete",
		"files", count,
		"output_dir", e.outputDir)

	return count, nil
}

// isEnrichableExt returns true for file types that can be
// converted to OKF concepts.
func isEnrichableExt(ext string) bool {
	switch ext {
	case ".md", ".txt", ".json", ".yaml", ".yml",
		".csv", ".html", ".xml":
		return true
	}
	return false
}

// sanitizeID converts a file path to a valid OKF concept ID.
func sanitizeID(relPath string) string {
	id := strings.TrimSuffix(relPath, filepath.Ext(relPath))
	id = filepath.ToSlash(id)
	id = strings.ReplaceAll(id, " ", "-")
	return id
}
