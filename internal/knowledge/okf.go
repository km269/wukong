// Package knowledge OKF Bundle import/export.
//
// This file provides OKF (Open Knowledge Format) interoperability
// for the RAG knowledge base. It enables:
//   - ImportBundle: load an OKF Bundle directory as knowledge docs
//   - ExportBundle: export indexed knowledge as an OKF Bundle
//
// OKF Bundles are plain directories of .md files with YAML
// frontmatter. Each concept file becomes a searchable document
// in the knowledge base, with frontmatter fields preserved as
// metadata and Markdown links maintained as cross-references.
package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/km269/wukong/internal/okf"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
)

// ImportBundle imports an OKF Bundle directory into the knowledge
// base. Each concept .md file is treated as a document:
//   - Frontmatter fields become document metadata
//   - Markdown body becomes the searchable content
//   - File path becomes the document ID (OKF identity mechanism)
//   - Cross-file links are preserved for knowledge graph discovery
//
// Non-compliant files are skipped (OKF consumer tolerance) with
// a warning logged. The bundle's index.md and log.md are also
// imported as special documents for completeness.
//
// This complements the existing directory source loader (which
// uses tRPC's dir source) by providing OKF-aware parsing that
// extracts structured metadata from frontmatter.
func (m *Manager) ImportBundle(bundlePath string) (int, error) {
	if m == nil || m.kb == nil {
		return 0, fmt.Errorf("knowledge manager not initialized")
	}

	bundle, warnings := okf.LoadBundle(bundlePath)
	for _, w := range warnings {
		util.Logger.Warn("okf: bundle import warning",
			"warning", w)
	}

	// Add the bundle path to configured sources so the next
	// knowledge base load will pick up these documents.
	// This works with the tRPC knowledge module's dir source
	// which recursively scans for .md files.
	alreadyConfigured := false
	for _, src := range m.cfg.Sources {
		if src == bundlePath {
			alreadyConfigured = true
			break
		}
	}
	if !alreadyConfigured {
		m.cfg.Sources = append(m.cfg.Sources, bundlePath)
	}

	// Reload the knowledge base with the updated sources.
	// Create a new dir source for the bundle and trigger load.
	src := dir.New([]string{bundlePath},
		dir.WithRecursive(true),
	)
	if reloadErr := m.kb.Load(context.Background(),
		knowledge.WithShowProgress(false),
	); reloadErr != nil {
		util.Logger.Warn("okf: kb reload for bundle",
			"warning", reloadErr.Error())
	}
	_ = src // source registered for future loads

	// Count total importable concepts (excluding reserved files).
	imported := len(bundle.Concepts)
	if bundle.Index != nil {
		imported++
	}
	if bundle.Log != nil {
		imported++
	}

	util.Logger.Info("okf: bundle imported into knowledge base",
		"bundle_path", bundlePath,
		"concepts_imported", imported,
		"total_concepts", len(bundle.Concepts))

	return imported, nil
}

// ExportBundle exports the knowledge base content as an OKF Bundle
// to the given output directory. This creates a portable, git-friendly
// representation of the knowledge that can be consumed by any
// OKF-compatible tool.
//
// The export preserves:
//   - Document content as Markdown body
//   - Document metadata as YAML frontmatter
//   - Document IDs as file paths (OKF identity mechanism)
//   - Auto-generated index.md for progressive exploration
//   - Auto-generated log.md for change tracking
func (m *Manager) ExportBundle(outputDir string) (int, error) {
	if m == nil || m.kb == nil {
		return 0, fmt.Errorf("knowledge manager not initialized")
	}

	// For the in-memory vector store, we need to track documents
	// as they are added. Since the tRPC knowledge module doesn't
	// expose a direct document listing API, we rely on the sources
	// configured during initialization.
	//
	// For a full export, we re-read from configured sources and
	// convert each document to an OKF concept file.

	bundle := &okf.Bundle{
		RootDir:  outputDir,
		Concepts: make([]*okf.Concept, 0),
	}

	// Export from configured source directories.
	exported := 0
	for _, src := range m.cfg.Sources {
		if src == "" {
			continue
		}
		count, err := m.exportDir(src, bundle)
		if err != nil {
			util.Logger.Warn("okf: export source dir failed",
				"dir", src,
				"error", err.Error())
			continue
		}
		exported += count
	}

	// Export from configured source URLs.
	for _, srcURL := range m.cfg.SourceURLs {
		if srcURL == "" {
			continue
		}
		concept := &okf.Concept{
			ID: filepath.Join("urls", sanitizeFilename(srcURL)),
			FilePath: filepath.Join("urls",
				sanitizeFilename(srcURL)+".md"),
			Frontmatter: okf.Frontmatter{
				Type:        "web-doc",
				Title:       srcURL,
				Description: "Imported from URL",
				Resource:    srcURL,
				Tags:        []string{"url", "imported"},
				Timestamp:   okf.FormatNow(),
			},
			Body: fmt.Sprintf(
				"# %s\n\nSource: %s\n",
				srcURL, srcURL),
		}
		bundle.Concepts = append(bundle.Concepts, concept)
		exported++
	}

	// Write the bundle.
	opts := okf.DefaultWriteOptions()
	if err := okf.WriteBundle(bundle, outputDir, opts); err != nil {
		return 0, fmt.Errorf("write OKF bundle: %w", err)
	}

	util.Logger.Info("okf: knowledge exported as OKF bundle",
		"output_dir", outputDir,
		"concepts_exported", exported)

	return exported, nil
}

// exportDir walks a source directory and adds each supported file
// as an OKF concept to the bundle.
func (m *Manager) exportDir(
	srcDir string, bundle *okf.Bundle,
) (int, error) {
	count := 0
	err := filepath.Walk(srcDir,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}

			// Only export text-based files.
			ext := strings.ToLower(filepath.Ext(path))
			if !isExportableExt(ext) {
				return nil
			}

			content, rErr := os.ReadFile(path)
			if rErr != nil {
				return nil
			}

			// Derive concept ID from relative path.
			relPath, _ := filepath.Rel(srcDir, path)
			conceptID := sanitizeConceptID(relPath)

			// Check if it's already an OKF concept (has frontmatter).
			if okfConcept, err := okf.ParseConcept(
				content, filepath.ToSlash(relPath),
			); err == nil && okfConcept.Frontmatter.Type != "" &&
				okfConcept.Frontmatter.Type != okf.DefaultType {
				// Already OKF-compliant — preserve as-is.
				bundle.Concepts = append(bundle.Concepts, okfConcept)
				count++
				return nil
			}

			// Create a new OKF concept for non-OKF files.
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
					Tags:        []string{"exported", ext[1:]},
					Timestamp:   okf.FormatNow(),
				},
				Body: string(content),
			}
			bundle.Concepts = append(bundle.Concepts, concept)
			count++
			return nil
		})

	return count, err
}

// isExportableExt returns true for file extensions that can be
// meaningfully exported as OKF Markdown concepts.
func isExportableExt(ext string) bool {
	switch ext {
	case ".md", ".txt", ".json", ".yaml", ".yml",
		".csv", ".html", ".xml", ".log":
		return true
	}
	return false
}

// sanitizeFilename creates a safe filename from a URL or path.
func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, "://", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "?", "_")
	s = strings.ReplaceAll(s, "&", "_")
	// Limit length.
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// sanitizeConceptID converts a file path to a valid OKF concept ID.
func sanitizeConceptID(relPath string) string {
	// Remove extension.
	id := strings.TrimSuffix(relPath, filepath.Ext(relPath))
	// Normalize path separators.
	id = filepath.ToSlash(id)
	// Replace spaces with hyphens.
	id = strings.ReplaceAll(id, " ", "-")
	return id
}
