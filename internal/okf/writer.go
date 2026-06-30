// Package okf writer — generates OKF Bundle files.
//
// This file provides the WriteBundle function and related helpers
// for exporting knowledge as OKF-compliant directories. It is the
// counterpart to bundle.go's LoadBundle.
package okf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// WriteOptions controls how a Bundle is written to disk.
type WriteOptions struct {
	// CompressFrontmatter removes empty optional fields from
	// the YAML output. Default: true.
	CompressFrontmatter bool

	// GenerateIndex auto-generates index.md if it doesn't exist.
	// The index lists all concepts grouped by type.
	// Default: true.
	GenerateIndex bool

	// GenerateLog auto-generates log.md if it doesn't exist.
	// Default: true.
	GenerateLog bool

	// OKFVersion is the version string written to index.md.
	// Default: "0.1".
	OKFVersion string
}

// DefaultWriteOptions returns standard write options.
func DefaultWriteOptions() WriteOptions {
	return WriteOptions{
		CompressFrontmatter: true,
		GenerateIndex:       true,
		GenerateLog:         true,
		OKFVersion:          OKFVersion,
	}
}

// WriteBundle writes a bundle to the given directory. Existing
// files are overwritten; the directory is created if needed.
//
// If the bundle has no Index and GenerateIndex is true, an
// index.md is auto-generated. Similarly for log.md.
func WriteBundle(bundle *Bundle, outputDir string, opts WriteOptions) error {
	if opts.OKFVersion == "" {
		opts.OKFVersion = OKFVersion
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create bundle dir: %w", err)
	}

	// Write all concept files.
	for _, concept := range bundle.Concepts {
		fullPath := filepath.Join(outputDir, concept.FilePath)
		parentDir := filepath.Dir(fullPath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			return fmt.Errorf("create concept dir: %w", err)
		}

		content := FormatConcept(concept, opts.CompressFrontmatter)
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %q: %w", concept.FilePath, err)
		}
	}

	// Write or generate index.md.
	if bundle.Index != nil {
		content := FormatConcept(bundle.Index, opts.CompressFrontmatter)
		path := filepath.Join(outputDir, IndexFile)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write index: %w", err)
		}
	} else if opts.GenerateIndex {
		content := GenerateIndexContent(bundle, opts.OKFVersion)
		path := filepath.Join(outputDir, IndexFile)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write generated index: %w", err)
		}
	}

	// Write or generate log.md.
	if bundle.Log != nil {
		content := FormatConcept(bundle.Log, opts.CompressFrontmatter)
		path := filepath.Join(outputDir, LogFile)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write log: %w", err)
		}
	} else if opts.GenerateLog {
		content := GenerateLogContent()
		path := filepath.Join(outputDir, LogFile)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write generated log: %w", err)
		}
	}

	return nil
}

// FormatConcept renders a Concept as a complete .md file string
// (YAML frontmatter + Markdown body).
func FormatConcept(concept *Concept, compress bool) string {
	var builder strings.Builder

	// Write frontmatter.
	builder.WriteString("---\n")
	fm := concept.Frontmatter

	// Type is always written (mandatory field).
	builder.WriteString(fmt.Sprintf("type: %s\n", fm.Type))

	if !compress || fm.Title != "" {
		builder.WriteString(fmt.Sprintf("title: %q\n", fm.Title))
	}
	if !compress || fm.Description != "" {
		builder.WriteString(fmt.Sprintf("description: %q\n", fm.Description))
	}
	if !compress || fm.Resource != "" {
		builder.WriteString(fmt.Sprintf("resource: %s\n", fm.Resource))
	}
	if !compress || len(fm.Tags) > 0 {
		if len(fm.Tags) > 0 {
			builder.WriteString("tags:\n")
			for _, tag := range fm.Tags {
				builder.WriteString(fmt.Sprintf("  - %s\n", tag))
			}
		} else if !compress {
			builder.WriteString("tags: []\n")
		}
	}
	if !compress || fm.Timestamp != "" {
		ts := fm.Timestamp
		if ts == "" {
			ts = FormatNow()
		}
		builder.WriteString(fmt.Sprintf("timestamp: %s\n", ts))
	}

	// Write unknown/extra fields.
	if len(fm.Extra) > 0 {
		extraYAML, err := yaml.Marshal(fm.Extra)
		if err == nil {
			builder.Write(extraYAML)
		}
	}

	builder.WriteString("---\n\n")

	// Write body.
	builder.WriteString(concept.Body)

	// Ensure body ends with newline.
	if !strings.HasSuffix(concept.Body, "\n") {
		builder.WriteString("\n")
	}

	return builder.String()
}

// GenerateIndexContent creates an index.md file listing all
// concepts in the bundle, grouped by type.
func GenerateIndexContent(bundle *Bundle, version string) string {
	var builder strings.Builder

	// Frontmatter.
	builder.WriteString("---\n")
	builder.WriteString("type: index\n")
	builder.WriteString(fmt.Sprintf("title: %s Knowledge Bundle\n",
		filepath.Base(bundle.RootDir)))
	builder.WriteString(fmt.Sprintf("okf_version: %s\n", version))
	builder.WriteString("---\n\n")

	// Body.
	builder.WriteString("# Knowledge Bundle Index\n\n")

	if version != "" {
		builder.WriteString(fmt.Sprintf(
			"> OKF Version: %s\n\n", version))
	}

	builder.WriteString(fmt.Sprintf(
		"This bundle contains %d concepts.\n\n",
		len(bundle.Concepts)))

	// Group concepts by type.
	types := bundle.AllTypes()
	for _, typeName := range types {
		builder.WriteString(fmt.Sprintf("## %s\n\n", typeName))
		concepts := bundle.ConceptsByType(typeName)
		for _, c := range concepts {
			title := c.Frontmatter.Title
			if title == "" {
				title = c.ID
			}
			builder.WriteString(fmt.Sprintf("- [%s](%s)",
				title, c.FilePath))
			if c.Frontmatter.Description != "" {
				builder.WriteString(" — ")
				builder.WriteString(c.Frontmatter.Description)
			}
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}

	return builder.String()
}

// GenerateLogContent creates a log.md file with an entry for
// the current export time.
func GenerateLogContent() string {
	var builder strings.Builder

	builder.WriteString("---\n")
	builder.WriteString("type: changelog\n")
	builder.WriteString("---\n\n")

	builder.WriteString("# Knowledge Bundle Changelog\n\n")

	builder.WriteString(fmt.Sprintf("## %s\n\n",
		time.Now().Format("2006-01-02")))
	builder.WriteString("- Bundle created/exported by Wukong\n")

	return builder.String()
}

// AppendLogEntry appends a change entry to an existing log.md
// file. If the file doesn't exist, it creates one.
// This implements the OKF log.md change-tracking convention.
//
// Valid actions: "Added", "Modified", "Removed".
func AppendLogEntry(bundleDir, action, filePath, reason string) error {
	logPath := filepath.Join(bundleDir, LogFile)
	now := time.Now().Format("2006-01-02")
	entry := fmt.Sprintf("- %s: %s (%s)", action, filePath, reason)

	content, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create new log.md.
			full := fmt.Sprintf(
				"---\ntype: changelog\n---\n\n"+
					"# Knowledge Bundle Changelog\n\n"+
					"## %s\n\n%s\n",
				now, entry)
			return os.WriteFile(logPath, []byte(full), 0644)
		}
		return fmt.Errorf("read log: %w", err)
	}

	str := string(content)
	dateHeader := fmt.Sprintf("## %s", now)

	var updated string
	if strings.Contains(str, dateHeader) {
		// Append to existing date section.
		updated = strings.Replace(str, dateHeader,
			dateHeader+"\n"+entry, 1)
	} else {
		// Add new date section at the end.
		updated = str + "\n## " + now + "\n\n" + entry + "\n"
	}

	return os.WriteFile(logPath, []byte(updated), 0644)
}
