package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBaseConfig_Missing uses defaults when no file exists; the
// wizard-specific zeroing (provider/extensions cleared) is up to the
// caller, so baseConfig returns the defaults untouched.
func TestBaseConfig_Missing(t *testing.T) {
	cfg, existing, err := baseConfig(filepath.Join(t.TempDir(), "nope.yaml"), false)
	if err != nil {
		t.Fatalf("baseConfig: %v", err)
	}
	if existing {
		t.Error("no file should not count as existing")
	}
	if cfg == nil {
		t.Fatal("expected defaults config")
	}
}

// TestBaseConfig_EditWithNoFile falls back to defaults under --edit.
func TestBaseConfig_EditWithNoFile(t *testing.T) {
	cfg, existing, err := baseConfig(filepath.Join(t.TempDir(), "nope.yaml"), true)
	if err != nil {
		t.Fatalf("baseConfig: %v", err)
	}
	if existing {
		t.Error("--edit without a file should still report not existing")
	}
	if cfg == nil {
		t.Fatal("expected defaults config")
	}
}

// TestBaseConfig_LoadsExisting verifies an existing file is loaded
// with ${ENV} references left untouched, ready for round-tripping.
func TestBaseConfig_LoadsExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `
default_provider: lmstudio
providers:
  - name: lmstudio
    type: openai
    base_url: http://localhost:1234/v1
    api_key: ${KEEP_REF}
    model: qwen3
security:
  block_dangerous_commands: true
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, existing, err := baseConfig(path, false)
	if err != nil {
		t.Fatalf("baseConfig: %v", err)
	}
	if !existing {
		t.Fatal("expected the existing file to be detected")
	}
	if cfg.DefaultProvider != "lmstudio" {
		t.Errorf("default provider = %q, want lmstudio", cfg.DefaultProvider)
	}
	p := cfg.FindProvider("lmstudio")
	if p == nil {
		t.Fatal("provider missing")
	}
	if p.APIKey != "${KEEP_REF}" {
		t.Errorf("api_key = %q, want unexpanded ${KEEP_REF}", p.APIKey)
	}
	if !cfg.Security.BlockDangerousCommands {
		t.Error("security.block_dangerous_commands should be true")
	}
}

// TestBaseConfig_CorruptFile returns an error instead of silently
// falling back to defaults, so the user notices a broken file.
func TestBaseConfig_CorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("providers: [unclosed"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, _, err := baseConfig(path, false)
	if err == nil {
		t.Error("expected error for corrupt config")
	}
}

func TestYNPrompt(t *testing.T) {
	cases := []struct {
		in   string
		def  bool
		want bool
	}{
		{"y", true, true},
		{"", true, true}, // Enter keeps default
		{"n", true, false},
		{"N", false, false},
		{"", false, false}, // Enter keeps default
	}
	for _, c := range cases {
		r := bufio.NewReader(strings.NewReader(c.in + "\n"))
		def := yn(c.def)
		got := strings.ToLower(readLine(r, def)) != "n"
		if got != c.want {
			t.Errorf("ynPrompt(%q, %v) = %v, want %v", c.in, c.def, got, c.want)
		}
	}
}

func TestPromptValue_KeepsCurrentOnEnter(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	got := promptValue(r, "label", "current")
	if got != "current" {
		t.Errorf("promptValue with empty input = %q, want current", got)
	}
}

func TestPromptValue_ReturnsInput(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("new value\n"))
	got := promptValue(r, "label", "current")
	if got != "new value" {
		t.Errorf("promptValue = %q, want %q", got, "new value")
	}
}

func TestIsBuiltinExt(t *testing.T) {
	exts := []struct {
		name, desc string
	}{
		{"developer", "d"},
		{"memory", "m"},
	}
	if !isBuiltinExt(exts, "developer") {
		t.Error("developer should be builtin")
	}
	if isBuiltinExt(exts, "custom_ext") {
		t.Error("custom_ext should not be builtin")
	}
}
