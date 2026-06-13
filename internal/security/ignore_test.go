package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewIgnoreMatcher_Disabled(t *testing.T) {
	m := NewIgnoreMatcher("", false)
	if m.IsEnabled() {
		t.Error("expected disabled when enabled=false")
	}
	if m.IsIgnored("/etc/passwd") {
		t.Error("expected not ignored when disabled")
	}
}

func TestNewIgnoreMatcher_NoFile(t *testing.T) {
	m := NewIgnoreMatcher(".nonexistent_wukongignore_xyz", true)
	if m.IsEnabled() {
		t.Error("expected disabled when file not found")
	}
}

func TestIgnoreMatcher_BasicPatterns(t *testing.T) {
	m := matcherWithRules(t, []string{
		"*.log",
		".env",
		"/secrets/",
	})

	if !m.IsEnabled() {
		t.Fatal("expected enabled")
	}
	if !m.IsIgnored("/tmp/debug.log") {
		t.Error("expected *.log to match")
	}
	if !m.IsIgnored("/project/.env") {
		t.Error("expected .env to match")
	}
	if !m.IsIgnored("/secrets/api.key") {
		t.Error("expected /secrets/ dir to match")
	}
	if m.IsIgnored("/tmp/readme.md") {
		t.Error("expected .md not to match")
	}
}

func TestCheckFilePath_Ignored(t *testing.T) {
	m := matcherWithRules(t, []string{"*.secret"})

	secretFile := filepath.Join(t.TempDir(), "test.secret")
	os.WriteFile(secretFile, []byte("secret"), 0644)

	err := m.CheckFilePath(secretFile)
	if err == nil {
		t.Error("expected CheckFilePath to block *.secret")
	}

	normalFile := filepath.Join(t.TempDir(), "test.txt")
	os.WriteFile(normalFile, []byte("normal"), 0644)
	err = m.CheckFilePath(normalFile)
	if err != nil {
		t.Errorf("expected CheckFilePath to allow .txt: %v", err)
	}
}

func TestIsFileAccessTool(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"file_read", true},
		{"file_write", true},
		{"file_replace", true},
		{"delete_file", true},
		{"code_search", false},
		{"directory_list", false},
		{"command_execute", false},
	}
	for _, tt := range tests {
		if got := IsFileAccessTool(tt.name); got != tt.expected {
			t.Errorf("IsFileAccessTool(%q) = %v, want %v",
				tt.name, got, tt.expected)
		}
	}
}

func TestExtractFilePathFromArgs(t *testing.T) {
	args := []byte(`{"file_path":"/tmp/test.log","command":"ls"}`)
	paths := ExtractFilePathFromArgs(args)
	if len(paths) == 0 {
		t.Error("expected file_path extraction")
	}
	found := false
	for _, p := range paths {
		if p == "/tmp/test.log" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected /tmp/test.log in %v", paths)
	}
}

// matcherWithRules creates an IgnoreMatcher with the given pattern
// lines. This is a test helper that simulates reading from a
// .wukongignore file.
func matcherWithRules(t *testing.T, lines []string) *IgnoreMatcher {
	t.Helper()

	m := &IgnoreMatcher{
		enabled:    true,
		sourceDir: "/",
	}

	for _, line := range lines {
		negate := false
		l := line
		if len(l) > 0 && l[0] == '!' {
			negate = true
			l = l[1:]
		}

		dirOnly := false
		if len(l) > 0 && l[len(l)-1] == '/' {
			dirOnly = true
			l = l[:len(l)-1]
		}

		anchored := false
		if len(l) > 0 && l[0] == '/' {
			anchored = true
			l = l[1:]
		}

		m.rules = append(m.rules, IgnoreRule{
			pattern: l,
			negate:  negate,
			dirOnly: dirOnly,
			regex:   patternToGlob(l, anchored),
		})
	}

	return m
}
