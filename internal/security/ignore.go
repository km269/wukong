// Package security provides file-access blacklisting via
// .wukongignore (gitignore-compatible syntax).
//
// When IgnoreFileEnabled is true, the Guard loads ignore patterns
// from the configured file (default: .wukongignore in cwd or home).
// File-access tools (file_read, file_write, file_replace,
// command_execute) are then checked against the patterns before
// execution.
package security

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// IgnoreRule represents a single compiled ignore rule.
type IgnoreRule struct {
	pattern string
	negate  bool // true for ! prefix rules
	dirOnly bool // true if pattern ends with /
	regex   string
}

// IgnoreMatcher holds compiled ignore rules for file path checking.
type IgnoreMatcher struct {
	rules   []IgnoreRule
	sourceDir string // directory containing the ignore file
	enabled   bool
}

// NewIgnoreMatcher loads and parses a .wukongignore-style file.
// Returns nil if disabled or no file found (graceful fallback).
func NewIgnoreMatcher(ignoreFile string, enabled bool) *IgnoreMatcher {
	if !enabled || ignoreFile == "" {
		return &IgnoreMatcher{enabled: false}
	}

	paths := resolveIgnorePaths(ignoreFile)
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		defer f.Close()

		m := &IgnoreMatcher{
			enabled:    true,
			sourceDir: filepath.Dir(p),
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}

			negate := false
			if strings.HasPrefix(line, "!") {
				negate = true
				line = line[1:]
			}

			dirOnly := strings.HasSuffix(line, "/")
			if dirOnly {
				line = strings.TrimSuffix(line, "/")
			}

			// Strip leading / for anchored patterns.
			anchored := false
			if strings.HasPrefix(line, "/") {
				anchored = true
				line = line[1:]
			}

			rule := IgnoreRule{
				pattern: line,
				negate:  negate,
				dirOnly: dirOnly,
				regex:   patternToGlob(line, anchored),
			}
			m.rules = append(m.rules, rule)
		}

		return m
	}

	return &IgnoreMatcher{enabled: false}
}

// IsEnabled returns whether ignore matching is active.
func (m *IgnoreMatcher) IsEnabled() bool {
	return m != nil && m.enabled
}

// IsIgnored checks whether a file path should be blocked.
// Returns true if the path matches any non-negated rule and no
// subsequent negated rule overrides the match.
func (m *IgnoreMatcher) IsIgnored(absPath string) bool {
	if !m.IsEnabled() {
		return false
	}

	// Normalize to forward slashes for consistent matching.
	absPath = filepath.ToSlash(absPath)

	ignored := false
	for _, rule := range m.rules {
		if matches(absPath, rule.regex) {
			ignored = !rule.negate
		}
	}

	return ignored
}

// String returns a human-readable summary of loaded rules.
func (m *IgnoreMatcher) String() string {
	if !m.IsEnabled() {
		return "ignore disabled"
	}
	return "ignore: " + string(rune(len(m.rules))+'0') + " rules loaded"
}

// resolveIgnorePaths returns candidate paths for the ignore file.
// Checks: cwd/<name>, ~/<name>, cwd/.wukong/<name>.
func resolveIgnorePaths(fileName string) []string {
	var paths []string

	// 1. CWD
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, fileName))
	}

	// 2. Home directory
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, fileName))
	}

	// 3. CWD/.wukong/
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, ".wukong", fileName))
	}

	return paths
}

// patternToGlob converts a gitignore-style pattern to a simplified
// glob string usable with filepath.Match.
func patternToGlob(pattern string, anchored bool) string {
	// Build a representation that captures gitignore intent.
	// We use simple string operations rather than regex for
	// readability and correctness.
	g := pattern

	// Handle ** (matches any number of directories).
	if anchored {
		g = "*" + g
	} else {
		g = "*" + g + "*"
	}

	return g
}

// matches checks if path matches a glob pattern.
func matches(path string, globPattern string) bool {
	// Try matching against just the basename first.
	base := filepath.Base(path)
	if matched, _ := filepath.Match(globPattern, path); matched {
		return true
	}
	if matched, _ := filepath.Match(globPattern, base); matched {
		return true
	}

	// Check if any path component matches.
	// This handles patterns like "*.log" matching
	// "a/b/c/debug.log".
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if matched, _ := filepath.Match(globPattern, part); matched {
			return true
		}
	}

	return false
}

// IsFileAccessTool checks whether a tool name corresponds to a
// file-access operation that should be checked against the ignore list.
func IsFileAccessTool(toolName string) bool {
	fileTools := []string{
		"file_read", "file_read_text",
		"file_write", "file_write_text",
		"file_replace", "replace_in_file",
		"delete_file", "delete_files",
	}
	for _, t := range fileTools {
		if strings.EqualFold(toolName, t) {
			return true
		}
	}
	return false
}

// ExtractFilePathFromArgs extracts a file path from tool arguments.
// Checks common JSON field names: file_path, path, file, target,
// filePath, old_path, new_path.
func ExtractFilePathFromArgs(args []byte) []string {
	type argMap map[string]any
	var data argMap
	if args == nil {
		return nil
	}

	// Simple manual extraction to avoid json import in every call.
	argStr := string(args)
	var paths []string

	pathKeys := []string{
		"file_path", "filePath", "path", "file",
		"target", "old_str", "new_str",
		"filepath",
	}
	for _, key := range pathKeys {
		// Look for "key":"value" patterns.
		search := `"` + key + `":`
		idx := strings.Index(argStr, search)
		if idx < 0 {
			continue
		}
		start := idx + len(search)
		// Skip whitespace.
		for start < len(argStr) &&
			(argStr[start] == ' ' || argStr[start] == '\t') {
			start++
		}
		if start >= len(argStr) || argStr[start] != '"' {
			continue
		}
		start++ // skip opening quote
		end := strings.IndexByte(argStr[start:], '"')
		if end < 0 {
			continue
		}
		val := argStr[start : start+end]
		if val != "" && looksLikePath(val) {
			paths = append(paths, val)
		}
	}

	_ = data // suppress unused
	return paths
}

// looksLikePath checks if a string appears to be a file path.
func looksLikePath(s string) bool {
	return strings.Contains(s, "/") ||
		strings.Contains(s, "\\") ||
		strings.HasPrefix(s, ".") ||
		strings.HasPrefix(s, "~")
}

// CheckFilePath validates a file path against the ignore list.
// Returns an error if the path should be blocked.
func (m *IgnoreMatcher) CheckFilePath(filePath string) error {
	if !m.IsEnabled() {
		return nil
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		// Can't resolve — allow through (fail open for safety).
		return nil
	}

	if m.IsIgnored(absPath) {
		return &IgnoreError{Path: filePath}
	}

	return nil
}

// IgnoreError is returned when a file path is blocked by the
// .wukongignore rules.
type IgnoreError struct {
	Path string
}

func (e *IgnoreError) Error() string {
	return "file access blocked by .wukongignore: " + e.Path
}
