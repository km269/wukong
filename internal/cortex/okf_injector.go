// Package cortex OKF knowledge index injection for MemoryFlow.
//
// This file provides the KnowledgeIndexInjector — a helper that
// reads an OKF Bundle's index.md and injects a concise knowledge
// overview into the MemoryFlow wake-up context.
//
// This implements the OKF "progressive exploration" pattern:
// when an agent enters a new context, it first reads the index
// to understand what knowledge is available, then dives deeper
// only into relevant concepts.
//
// The injector is designed to be non-intrusive:
//   - If no OKF Bundle is found, WakeUp proceeds normally
//   - If index.md is missing, the concept list is auto-summarized
//   - The knowledge overview is prepended to the wake-up context
package cortex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/km269/wukong/internal/okf"
	"github.com/km269/wukong/internal/util"
)

// KnowledgeIndexInjector reads OKF Bundle index files and
// generates concise knowledge overviews for injection into
// agent wake-up context.
type KnowledgeIndexInjector struct {
	// bundlePath is the path to the OKF Bundle directory.
	// If empty, injection is disabled.
	bundlePath string

	// cachedIndex is the cached index content to avoid
	// re-reading on every WakeUp call.
	cachedIndex string

	// cachedModTime tracks when index.md was last modified
	// to enable cache invalidation.
	cachedModTime int64
}

// NewKnowledgeIndexInjector creates an injector for the given
// OKF Bundle path. If the path is empty, the injector is a no-op.
func NewKnowledgeIndexInjector(bundlePath string) *KnowledgeIndexInjector {
	return &KnowledgeIndexInjector{bundlePath: bundlePath}
}

// Inject prepends a knowledge overview to the given wake-up
// context. If no OKF Bundle is configured or the bundle has no
// content, the original context is returned unchanged.
//
// The overview format:
//
//	[Available Knowledge]
//	This knowledge base contains N concepts across M types:
//	- type1: concept_count
//	- type2: concept_count
//	...
//	Index: <first 500 chars of index.md body>
func (ki *KnowledgeIndexInjector) Inject(wakeCtx string) string {
	if ki == nil || ki.bundlePath == "" {
		return wakeCtx
	}

	overview := ki.getOverview()
	if overview == "" {
		return wakeCtx
	}

	if wakeCtx == "" {
		return overview
	}

	return overview + "\n\n" + wakeCtx
}

// getOverview returns the knowledge overview string, using
// a cached version when the index file hasn't changed.
func (ki *KnowledgeIndexInjector) getOverview() string {
	if ki.bundlePath == "" {
		return ""
	}

	// Check if index.md exists and get mod time.
	indexPath := filepath.Join(ki.bundlePath, okf.IndexFile)
	info, err := os.Stat(indexPath)
	if err != nil {
		// No index.md — try generating from bundle.
		return ki.generateOverviewFromBundle()
	}

	modTime := info.ModTime().Unix()
	if ki.cachedIndex != "" && modTime == ki.cachedModTime {
		return ki.cachedIndex
	}

	// Read and cache the index.
	content, err := os.ReadFile(indexPath)
	if err != nil {
		return ""
	}

	// Parse to extract the body (skip frontmatter).
	body := extractMarkdownBody(string(content))

	// Truncate body to keep context concise.
	overview := formatKnowledgeOverview(body, 500)

	ki.cachedIndex = overview
	ki.cachedModTime = modTime

	return overview
}

// generateOverviewFromBundle loads the bundle and generates
// a summary when no index.md exists.
func (ki *KnowledgeIndexInjector) generateOverviewFromBundle() string {
	bundle, warnings := okf.LoadBundle(ki.bundlePath)
	if len(bundle.Concepts) == 0 && len(warnings) > 0 {
		util.Logger.Debug("okf: no concepts in bundle for overview",
			"warnings", len(warnings))
		return ""
	}

	types := bundle.AllTypes()
	var builder strings.Builder
	builder.WriteString("[Available Knowledge]\n")
	builder.WriteString(fmt.Sprintf(
		"This knowledge base contains %d concepts across %d types:\n",
		len(bundle.Concepts), len(types)))

	for _, typeName := range types {
		concepts := bundle.ConceptsByType(typeName)
		builder.WriteString(fmt.Sprintf(
			"- %s: %d concept(s)\n",
			typeName, len(concepts)))
	}

	result := builder.String()
	ki.cachedIndex = result
	return result
}

// formatKnowledgeOverview formats a knowledge overview string
// with a header and truncated content.
func formatKnowledgeOverview(body string, maxChars int) string {
	var builder strings.Builder
	builder.WriteString("[Available Knowledge]\n")

	body = strings.TrimSpace(body)
	if len(body) <= maxChars {
		builder.WriteString(body)
	} else {
		builder.WriteString(body[:maxChars])
		builder.WriteString("\n... (truncated, see full index for more)")
	}

	return builder.String()
}

// extractMarkdownBody strips YAML frontmatter from a Markdown
// file and returns just the body content.
func extractMarkdownBody(content string) string {
	if !strings.HasPrefix(content, "---\n") &&
		!strings.HasPrefix(content, "---\r\n") {
		return content
	}

	rest := content
	if strings.HasPrefix(rest, "---\n") {
		rest = rest[4:]
	} else {
		rest = rest[5:]
	}

	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return content
	}

	bodyStart := endIdx + 4
	if bodyStart < len(rest) && rest[bodyStart] == '\n' {
		bodyStart++
	}

	return rest[bodyStart:]
}

// SetBundlePath updates the OKF Bundle path at runtime.
// This allows dynamic reconfiguration of the knowledge source.
func (ki *KnowledgeIndexInjector) SetBundlePath(path string) {
	if ki.bundlePath != path {
		ki.bundlePath = path
		ki.cachedIndex = ""
		ki.cachedModTime = 0
	}
}

// WakeUpWithKnowledgeIndex wraps the standard WakeUp method,
// injecting OKF knowledge index context before the normal
// wake-up layers.
//
// This is the primary integration point — called from the agent
// loop instead of the raw WakeUp method when OKF knowledge
// injection is desired.
func (m *MemoryFlowService) WakeUpWithKnowledgeIndex(
	ctx context.Context,
	identity string,
	query string,
	sessionID string,
	injector *KnowledgeIndexInjector,
) (string, error) {
	// Get the standard wake-up context (3 layers).
	wakeCtx, err := m.WakeUp(ctx, identity, query, sessionID)
	if err != nil {
		return "", err
	}

	// Inject OKF knowledge overview if available.
	if injector != nil {
		wakeCtx = injector.Inject(wakeCtx)
	}

	return wakeCtx, nil
}
