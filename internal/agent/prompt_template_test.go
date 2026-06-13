package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestNewPromptTemplateManager_EmptyConfig(t *testing.T) {
	cfg := &config.WukongConfig{}
	// Agent.SystemPromptDir is empty string by default.
	mgr := NewPromptTemplateManager(cfg)
	if mgr == nil {
		t.Fatal("NewPromptTemplateManager returned nil")
	}

	result := mgr.LoadTemplates(TemplateVars{})
	if result != "" {
		t.Errorf("expected empty result for empty dir, got %q",
			result)
	}
}

func TestLoadTemplates_NoMdFiles(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.WukongConfig{}
	cfg.Agent.SystemPromptDir = dir

	mgr := NewPromptTemplateManager(cfg)
	result := mgr.LoadTemplates(TemplateVars{})
	if result != "" {
		t.Errorf("expected empty result for dir with no .md files, "+
			"got %q", result)
	}
}

func TestLoadTemplates_SingleFile(t *testing.T) {
	dir := t.TempDir()

	content := "You are a test assistant. Working dir: {{.WorkingDir}}."
	os.WriteFile(filepath.Join(dir, "00_test.md"), []byte(content), 0644)

	cfg := &config.WukongConfig{}
	cfg.Agent.SystemPromptDir = dir

	mgr := NewPromptTemplateManager(cfg)
	result := mgr.LoadTemplates(TemplateVars{
		WorkingDir: "/home/test/project",
	})

	if result == "" {
		t.Fatal("expected non-empty result")
	}
	if result != "You are a test assistant. Working dir: /home/test/project." {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestLoadTemplates_MultipleFilesSorted(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "20_second.md"),
		[]byte("Second template."), 0644)
	os.WriteFile(filepath.Join(dir, "10_first.md"),
		[]byte("First template."), 0644)

	cfg := &config.WukongConfig{}
	cfg.Agent.SystemPromptDir = dir

	mgr := NewPromptTemplateManager(cfg)
	result := mgr.LoadTemplates(TemplateVars{})

	// 10_first.md should come before 20_second.md.
	expected := "First template.\n\nSecond template."
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestLoadTemplates_VariableSubstitution(t *testing.T) {
	dir := t.TempDir()

	tmpl := `Provider: {{.ProviderName}}
Model: {{.ModelName}}
Session: {{.SessionID}}
User: {{.UserName}}
WorkingDir: {{.WorkingDir}}`
	os.WriteFile(filepath.Join(dir, "00_vars.md"),
		[]byte(tmpl), 0644)

	cfg := &config.WukongConfig{}
	cfg.Agent.SystemPromptDir = dir

	mgr := NewPromptTemplateManager(cfg)
	result := mgr.LoadTemplates(TemplateVars{
		ProviderName: "openai",
		ModelName:    "gpt-4o",
		SessionID:    "abc-123",
		UserName:     "alice",
		WorkingDir:   "/home/alice/proj",
	})

	expected := "Provider: openai\nModel: gpt-4o\n" +
		"Session: abc-123\nUser: alice\nWorkingDir: /home/alice/proj"
	if result != expected {
		t.Errorf("unexpected substitution result:\n  got:  %q\n  want: %q",
			result, expected)
	}
}

func TestLoadTemplates_EmptyVariableStripped(t *testing.T) {
	dir := t.TempDir()

	tmpl := "Hello {{.UserName}}. Working in {{.WorkingDir}}."
	os.WriteFile(filepath.Join(dir, "00_greet.md"),
		[]byte(tmpl), 0644)

	cfg := &config.WukongConfig{}
	cfg.Agent.SystemPromptDir = dir

	mgr := NewPromptTemplateManager(cfg)
	result := mgr.LoadTemplates(TemplateVars{
		UserName: "bob",
		// WorkingDir is empty — should be stripped, not left as {{.WorkingDir}}.
	})

	expected := "Hello bob. Working in ."
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestLoadTemplates_IgnoresNonMdFiles(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "00_base.md"),
		[]byte("Base template."), 0644)
	os.WriteFile(filepath.Join(dir, "notes.txt"),
		[]byte("Should be ignored."), 0644)
	os.WriteFile(filepath.Join(dir, "README"),
		[]byte("Also ignored."), 0644)

	cfg := &config.WukongConfig{}
	cfg.Agent.SystemPromptDir = dir

	mgr := NewPromptTemplateManager(cfg)
	result := mgr.LoadTemplates(TemplateVars{})

	if result != "Base template." {
		t.Errorf("expected only .md content, got %q", result)
	}
}

func TestLoadTemplates_NonexistentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	cfg := &config.WukongConfig{}
	cfg.Agent.SystemPromptDir = dir

	// NewPromptTemplateManager creates the dir, so it should succeed.
	mgr := NewPromptTemplateManager(cfg)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}

	result := mgr.LoadTemplates(TemplateVars{})
	if result != "" {
		t.Errorf("expected empty result for empty new dir, got %q",
			result)
	}
}
