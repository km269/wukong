// Package okf implements the Open Knowledge Format (OKF) v0.1
// specification — a vendor-neutral, AI-agent-and-human-friendly
// standard for representing knowledge as Markdown files with
// YAML frontmatter.
//
// An OKF Bundle is a plain directory of .md files where:
//   - Each .md file represents a "concept" (table, API, metric, etc.)
//   - Each concept file has YAML frontmatter (metadata) + Markdown body
//   - The only mandatory frontmatter field is `type`
//   - File paths serve as concept identifiers (no separate ID system)
//   - Standard Markdown links between files form a knowledge graph
//
// Reserved filenames:
//   - index.md: directory index for progressive exploration
//   - log.md:   change history for the bundle
//
// Compliance requirements (OKF spec v0.1):
//  1. All non-reserved .md files contain parseable YAML frontmatter
//  2. Each frontmatter contains a non-empty `type` field
//  3. index.md and log.md (if present) follow their prescribed structure
//
// Consumer requirements (defensive design):
//   - Must tolerate unknown `type` values
//   - Must tolerate missing optional fields
//   - Must tolerate broken cross-file links
//   - A single non-compliant file must not invalidate the whole bundle
//
// Reference: https://github.com/GoogleCloudPlatform/okf
package okf

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// OKFVersion is the supported OKF specification version.
const OKFVersion = "0.1"

// Reserved filenames per OKF spec.
const (
	IndexFile = "index.md"
	LogFile   = "log.md"
)

// DefaultType is assigned to concept files that lack a `type`
// field. This implements Wukong's consumer-side tolerance: rather
// than rejecting non-OKF-compliant files, we default them so they
// remain usable. This allows SKILL.md files (which predate OKF
// adoption) to be loaded without modification.
const DefaultType = "concept"

// Frontmatter is the YAML metadata header of an OKF concept file.
// Only `Type` is mandatory per the OKF spec; all other fields are
// optional but recommended.
type Frontmatter struct {
	// Type is the concept type (mandatory).
	// Examples: table, api, metric, skill, runbook, concept.
	// Consumers must tolerate unknown type values.
	Type string `yaml:"type"`

	// Title is the human-readable concept title (recommended).
	Title string `yaml:"title,omitempty"`

	// Description is a short summary (recommended).
	Description string `yaml:"description,omitempty"`

	// Resource is a URI pointing to the source resource
	// (e.g., bigquery://project.dataset.orders).
	Resource string `yaml:"resource,omitempty"`

	// Tags are free-form labels for categorization.
	Tags []string `yaml:"tags,omitempty"`

	// Timestamp is the last-modified time (recommended).
	Timestamp string `yaml:"timestamp,omitempty"`

	// Extra holds unknown/custom fields. Per OKF spec, consumers
	// must preserve (not discard) unknown fields during round-trip.
	Extra map[string]any `yaml:",inline"`
}

// Concept represents a single OKF concept file: its frontmatter
// metadata, Markdown body, and file-system identity.
type Concept struct {
	// ID is the concept identifier, derived from the file path
	// relative to the bundle root (without .md extension).
	// Example: "tables/orders" for tables/orders.md.
	ID string

	// FilePath is the relative path from the bundle root
	// (with .md extension). Example: "tables/orders.md".
	FilePath string

	// Frontmatter is the parsed YAML metadata.
	Frontmatter Frontmatter

	// Body is the Markdown content below the frontmatter.
	Body string

	// Links are cross-references to other concepts, extracted
	// from Markdown links in the body. Each entry is a relative
	// path like "../tables/customers.md".
	Links []string
}

// Bundle represents an OKF knowledge bundle — a directory of
// concept files with optional index.md and log.md.
type Bundle struct {
	// RootDir is the absolute path to the bundle directory.
	RootDir string

	// Concepts is the list of all concept files in the bundle
	// (excluding index.md and log.md).
	Concepts []*Concept

	// Index is the parsed index.md content (may be empty).
	Index *Concept

	// Log is the parsed log.md content (may be empty).
	Log *Concept

	// Version is the OKF version declared in index.md (if any).
	Version string
}

// LoadBundle loads an OKF bundle from a directory. It follows
// the OKF consumer requirements: non-compliant files are skipped
// (with a warning logged via the error slice) rather than causing
// the entire load to fail.
//
// Returns the bundle and a slice of non-fatal warnings for
// skipped files.
func LoadBundle(rootDir string) (*Bundle, []string) {
	var warnings []string
	bundle := &Bundle{RootDir: rootDir}

	// Verify the root directory is accessible before recursing.
	if _, err := os.ReadDir(rootDir); err != nil {
		return bundle, []string{
			fmt.Sprintf("read bundle dir %q: %v", rootDir, err),
		}
	}

	loadDir(rootDir, "", bundle, &warnings)

	// Sort concepts by ID for deterministic ordering.
	sort.Slice(bundle.Concepts, func(i, j int) bool {
		return bundle.Concepts[i].ID < bundle.Concepts[j].ID
	})

	// Extract OKF version from index.md frontmatter.
	if bundle.Index != nil {
		if v, ok := bundle.Index.Frontmatter.Extra["okf_version"]; ok {
			if vs, ok := v.(string); ok {
				bundle.Version = vs
			}
		}
	}

	return bundle, warnings
}

// loadDir recursively loads .md files from a directory.
func loadDir(
	rootDir, relDir string,
	bundle *Bundle,
	warnings *[]string,
) {
	fullDir := filepath.Join(rootDir, relDir)
	entries, err := os.ReadDir(fullDir)
	if err != nil {
		*warnings = append(*warnings,
			fmt.Sprintf("read dir %q: %v", relDir, err))
		return
	}

	for _, entry := range entries {
		name := entry.Name()
		relPath := filepath.Join(relDir, name)

		if entry.IsDir() {
			loadDir(rootDir, relPath, bundle, warnings)
			continue
		}

		if !strings.HasSuffix(name, ".md") {
			continue
		}

		fullPath := filepath.Join(fullDir, name)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			*warnings = append(*warnings,
				fmt.Sprintf("read %q: %v", relPath, err))
			continue
		}

		concept, err := ParseConcept(content, relPath)
		if err != nil {
			// OKF consumer requirement: skip non-compliant files
			// without failing the entire bundle.
			*warnings = append(*warnings,
				fmt.Sprintf("skip %q: %v", relPath, err))
			continue
		}

		switch name {
		case IndexFile:
			bundle.Index = concept
		case LogFile:
			bundle.Log = concept
		default:
			bundle.Concepts = append(bundle.Concepts, concept)
		}
	}
}

// ParseConcept parses a single .md file content into a Concept.
// The relPath is used to derive the concept ID.
//
// Returns an error if:
//   - The file has no frontmatter (no `---` delimiter)
//   - The frontmatter YAML is unparseable
//
// Note: a missing `type` field is NOT an error — it defaults to
// DefaultType ("concept") to implement consumer-side tolerance.
func ParseConcept(content []byte, relPath string) (*Concept, error) {
	fm, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}

	// Default type for files without explicit type field.
	// This is Wukong's extension to OKF spec for backward
	// compatibility with existing SKILL.md files.
	if fm.Type == "" {
		fm.Type = DefaultType
	}

	// Derive concept ID from file path (without extension).
	id := strings.TrimSuffix(relPath, ".md")
	// Normalize path separators for cross-platform consistency.
	id = filepath.ToSlash(id)

	// Extract Markdown links from body.
	links := extractLinks(body)

	return &Concept{
		ID:          id,
		FilePath:    relPath,
		Frontmatter: fm,
		Body:        body,
		Links:       links,
	}, nil
}

// splitFrontmatter separates YAML frontmatter from Markdown body.
// The frontmatter is delimited by `---` at the start of the file.
func splitFrontmatter(content []byte) (Frontmatter, string, error) {
	var fm Frontmatter

	str := string(content)

	// Check for frontmatter delimiter.
	if !strings.HasPrefix(str, "---\n") &&
		!strings.HasPrefix(str, "---\r\n") {
		// No frontmatter — treat entire content as body.
		// This is non-compliant with OKF, but we tolerate it
		// for files like CLAUDE.md / AGENTS.md.
		return fm, str, nil
	}

	// Find closing delimiter.
	rest := str
	if strings.HasPrefix(rest, "---\n") {
		rest = rest[4:]
	} else {
		rest = rest[5:]
	}

	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return fm, str, fmt.Errorf("missing closing frontmatter delimiter")
	}

	yamlPart := rest[:endIdx]
	bodyStart := endIdx + 4 // skip "\n---"
	if bodyStart < len(rest) && rest[bodyStart] == '\n' {
		bodyStart++
	} else if bodyStart+1 < len(rest) &&
		rest[bodyStart] == '\r' && rest[bodyStart+1] == '\n' {
		bodyStart += 2
	}
	body := rest[bodyStart:]

	if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
		return fm, str, fmt.Errorf("parse YAML: %w", err)
	}

	return fm, body, nil
}

// extractLinks finds all Markdown links in the body that point
// to other .md files. Returns relative paths.
//
// Matches patterns like:
//   [text](path/to/file.md)
//   [text](../tables/orders.md)
func extractLinks(body string) []string {
	var links []string
	seen := make(map[string]bool)

	// Simple state machine to find [text](url) patterns.
	i := 0
	for i < len(body) {
		// Find opening '['.
		bracketStart := strings.IndexByte(body[i:], '[')
		if bracketStart < 0 {
			break
		}
		bracketStart += i

		// Find closing ']'.
		bracketEnd := strings.IndexByte(body[bracketStart:], ']')
		if bracketEnd < 0 {
			break
		}
		bracketEnd += bracketStart

		// Check for '(' immediately after ']'.
		if bracketEnd+1 >= len(body) || body[bracketEnd+1] != '(' {
			i = bracketEnd + 1
			continue
		}

		// Find closing ')'.
		parenEnd := strings.IndexByte(body[bracketEnd+2:], ')')
		if parenEnd < 0 {
			break
		}
		parenEnd += bracketEnd + 2

		url := body[bracketEnd+2 : parenEnd]
		if strings.HasSuffix(url, ".md") && !seen[url] {
			links = append(links, url)
			seen[url] = true
		}

		i = parenEnd + 1
	}

	return links
}

// ResolveLink resolves a relative Markdown link to a concept ID.
// Example: "../tables/orders.md" relative to "api/checkout" resolves
// to "tables/orders".
func ResolveLink(from, link string) string {
	dir := filepath.Dir(from)
	resolved := filepath.Join(dir, link)
	resolved = strings.TrimSuffix(resolved, ".md")
	return filepath.ToSlash(resolved)
}

// FindConcept returns the concept with the given ID, or nil.
func (b *Bundle) FindConcept(id string) *Concept {
	for _, c := range b.Concepts {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// ConceptsByType returns all concepts with the given type.
func (b *Bundle) ConceptsByType(typeName string) []*Concept {
	var result []*Concept
	for _, c := range b.Concepts {
		if c.Frontmatter.Type == typeName {
			result = append(result, c)
		}
	}
	return result
}

// AllTypes returns all distinct concept types in the bundle.
func (b *Bundle) AllTypes() []string {
	seen := make(map[string]bool)
	for _, c := range b.Concepts {
		seen[c.Frontmatter.Type] = true
	}
	var types []string
	for t := range seen {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// FormatNow returns the current time in OKF timestamp format
// (ISO 8601 / RFC 3339).
func FormatNow() string {
	return time.Now().Format(time.RFC3339)
}
