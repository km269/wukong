package topofmind

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestManager_New(t *testing.T) {
	cfg := &config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: ".wukong_instructions.md",
		MaxLength:       2000,
	}

	mgr := NewManager(cfg)
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	if mgr.cfg != cfg {
		t.Error("manager config not set correctly")
	}
}

func TestManager_GetSetAppend(t *testing.T) {
	cfg := &config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: ".wukong_instructions.md",
		MaxLength:       2000,
	}

	mgr := NewManager(cfg)

	// Initially empty
	if instructions := mgr.GetInstructions(); instructions != "" {
		t.Errorf("expected empty instructions, got %q", instructions)
	}

	// Set instructions
	mgr.SetInstructions("Always use Chinese to respond")
	if got := mgr.GetInstructions(); got != "Always use Chinese to respond" {
		t.Errorf("expected set instruction, got %q", got)
	}

	// Append instructions
	mgr.AppendInstructions("Be polite")
	if got := mgr.GetInstructions(); got != "Always use Chinese to respond\nBe polite" {
		t.Errorf("expected appended instructions, got %q", got)
	}

	// Clear
	mgr.ClearInstructions()
	if got := mgr.GetInstructions(); got != "" {
		t.Errorf("expected cleared instructions, got %q", got)
	}
}

func TestManager_MaxLength(t *testing.T) {
	cfg := &config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: ".wukong_instructions.md",
		MaxLength:       10,
	}

	mgr := NewManager(cfg)
	mgr.SetInstructions("This is a very long instruction that exceeds the max length")
	if got := mgr.GetInstructions(); len(got) > 10 {
		t.Errorf("expected truncated to 10 chars, got %d: %q", len(got), got)
	}
}

func TestManager_LoadFromFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test_instructions.md")
	content := "Be concise\nUse examples"

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	cfg := &config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: filePath,
		MaxLength:       2000,
	}

	mgr := NewManager(cfg)
	if got := mgr.GetInstructions(); got != content {
		t.Errorf("expected file content, got %q", got)
	}
}

func TestManager_FormatForPrompt(t *testing.T) {
	cfg := &config.TopOfMindConfig{
		Enabled:         true,
		InstructionFile: ".wukong_instructions.md",
		MaxLength:       2000,
	}

	mgr := NewManager(cfg)

	// Empty should return empty
	if formatted := mgr.FormatForPrompt(); formatted != "" {
		t.Errorf("expected empty format for empty instructions, got %q", formatted)
	}

	mgr.SetInstructions("Always be concise")
	formatted := mgr.FormatForPrompt()
	if formatted == "" {
		t.Error("expected non-empty format after set")
	}
	// Should contain the instruction
	if !contains(formatted, "Always be concise") {
		t.Errorf("formatted output missing instruction: %q", formatted)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
