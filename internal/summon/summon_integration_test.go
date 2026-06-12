package summon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestSummonManager_Integration_LoadSkills creates a temporary skills
// directory, writes skill files, and verifies they are loaded correctly.
func TestSummonManager_Integration_LoadSkills(t *testing.T) {
	// Create temporary skills directory
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	// Write a test skill file
	skillContent := "# Code Review Skill\n\n" +
		"This skill performs automated code review.\n" +
		"It checks for common issues and suggests improvements.\n"
	skillPath := filepath.Join(skillsDir, "code_review.md")
	if err := os.WriteFile(skillPath, []byte(skillContent), 0644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}

	// Write another skill file
	skill2Content := "# Test Generator\n\n" +
		"This skill generates unit tests.\n"
	skill2Path := filepath.Join(skillsDir, "test_gen.md")
	if err := os.WriteFile(skill2Path, []byte(skill2Content), 0644); err != nil {
		t.Fatalf("write skill2 file: %v", err)
	}

	cfg := &config.SummonConfig{
		Enabled:       true,
		SkillsDir:     skillsDir,
		MaxConcurrent: 5,
	}

	mgr := NewSummonManager(cfg, nil)

	// Load skills (no model, so delegates won't be created, but skills parsed)
	if err := mgr.LoadSkills(context.Background()); err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	// Verify skills were parsed
	skills := mgr.ListSkills()
	if len(skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(skills))
	}

	// Check skill details
	skillNames := make(map[string]SkillInfo)
	for _, s := range skills {
		skillNames[s.Name] = s
	}

	if cr, ok := skillNames["code_review"]; ok {
		if cr.Description != "This skill performs automated code review." {
			t.Errorf("unexpected description: %q", cr.Description)
		}
	} else {
		t.Error("code_review skill not found")
	}

	if tg, ok := skillNames["test_gen"]; ok {
		if tg.Description != "This skill generates unit tests." {
			t.Errorf("unexpected description: %q", tg.Description)
		}
	} else {
		t.Error("test_gen skill not found")
	}
}

// TestSummonManager_Integration_EmptyDir verifies behavior with an
// empty skills directory.
func TestSummonManager_Integration_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "empty_skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	cfg := &config.SummonConfig{
		Enabled:       true,
		SkillsDir:     skillsDir,
		MaxConcurrent: 3,
	}

	mgr := NewSummonManager(cfg, nil)
	if err := mgr.LoadSkills(context.Background()); err != nil {
		t.Fatalf("LoadSkills on empty dir: %v", err)
	}

	skills := mgr.ListSkills()
	if len(skills) != 0 {
		t.Errorf("expected 0 skills in empty dir, got %d", len(skills))
	}
}

// TestSummonManager_Integration_NonMarkdownFiles verifies that only
// .md files are loaded as skills.
func TestSummonManager_Integration_NonMarkdownFiles(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "mixed_skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	// Write a markdown skill
	os.WriteFile(filepath.Join(skillsDir, "valid.md"),
		[]byte("# Valid Skill\n\nA valid skill.\n"), 0644)

	// Write non-markdown files
	os.WriteFile(filepath.Join(skillsDir, "notes.txt"),
		[]byte("not a skill"), 0644)
	os.WriteFile(filepath.Join(skillsDir, "config.json"),
		[]byte(`{"key": "value"}`), 0644)

	// Create a subdirectory (should be ignored)
	os.MkdirAll(filepath.Join(skillsDir, "subdir"), 0755)

	cfg := &config.SummonConfig{
		Enabled:       true,
		SkillsDir:     skillsDir,
		MaxConcurrent: 3,
	}

	mgr := NewSummonManager(cfg, nil)
	if err := mgr.LoadSkills(context.Background()); err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	skills := mgr.ListSkills()
	if len(skills) != 1 {
		t.Errorf("expected 1 skill, got %d", len(skills))
	}
	if len(skills) > 0 && skills[0].Name != "valid" {
		t.Errorf("expected 'valid' skill, got %q", skills[0].Name)
	}
}

// TestSummonManager_Integration_ConcurrencyLimit verifies that the
// semaphore-based concurrency control works correctly under load.
func TestSummonManager_Integration_ConcurrencyLimit(t *testing.T) {
	cfg := &config.SummonConfig{
		MaxConcurrent: 2,
	}
	mgr := NewSummonManager(cfg, nil)

	if mgr.MaxConcurrent() != 2 {
		t.Errorf("expected max concurrent 2, got %d", mgr.MaxConcurrent())
	}

	// Acquire both slots
	release1, err := mgr.AcquireSlot(context.Background())
	if err != nil {
		t.Fatalf("AcquireSlot 1: %v", err)
	}

	release2, err := mgr.AcquireSlot(context.Background())
	if err != nil {
		t.Fatalf("AcquireSlot 2: %v", err)
	}

	if mgr.AvailableSlots() != 0 {
		t.Errorf("expected 0 available slots, got %d", mgr.AvailableSlots())
	}

	// Third acquire should block
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = mgr.AcquireSlot(ctx)
	if err == nil {
		t.Error("expected timeout error when all slots taken")
	}

	// Release one slot
	release1()
	if mgr.AvailableSlots() != 1 {
		t.Errorf("expected 1 available slot, got %d", mgr.AvailableSlots())
	}

	// Should be able to acquire again
	release3, err := mgr.AcquireSlot(context.Background())
	if err != nil {
		t.Fatalf("AcquireSlot after release: %v", err)
	}
	release2()
	release3()

	if mgr.AvailableSlots() != 2 {
		t.Errorf("expected 2 available slots after all releases, got %d",
			mgr.AvailableSlots())
	}
}

// TestSummonManager_Integration_Lifecycle verifies the full lifecycle:
// create -> load skills -> list -> close.
func TestSummonManager_Integration_Lifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	skillsDir := filepath.Join(tmpDir, "lifecycle_skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("create skills dir: %v", err)
	}

	os.WriteFile(filepath.Join(skillsDir, "helper.md"),
		[]byte("# Helper\n\nGeneral purpose helper skill.\n"), 0644)

	cfg := &config.SummonConfig{
		Enabled:       true,
		SkillsDir:     skillsDir,
		MaxConcurrent: 5,
	}

	mgr := NewSummonManager(cfg, nil)

	// Load skills
	if err := mgr.LoadSkills(context.Background()); err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	// List skills
	skills := mgr.ListSkills()
	if len(skills) != 1 {
		t.Errorf("expected 1 skill, got %d", len(skills))
	}

	// Close
	if err := mgr.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// After close, delegates should be cleared
	if mgr.DelegateCount() != 0 {
		t.Errorf("expected 0 delegates after close, got %d",
			mgr.DelegateCount())
	}
}

// TestSummonManager_Integration_SlotWrapTool verifies that
// WrapTool correctly wraps a tool with slot control metadata.
func TestSummonManager_Integration_SlotWrapTool(t *testing.T) {
	cfg := &config.SummonConfig{
		MaxConcurrent: 3,
	}
	mgr := NewSummonManager(cfg, nil)

	// Create a mock tool
	innerTool := &mockSummonTool{
		name: "test_delegate",
		desc: "A test delegate tool",
	}

	wrapped := mgr.WrapTool(innerTool, "test_delegate")
	if wrapped == nil {
		t.Fatal("WrapTool returned nil")
	}

	decl := wrapped.Declaration()
	if decl == nil {
		t.Fatal("Declaration returned nil")
	}
	if decl.Name != "test_delegate" {
		t.Errorf("expected name 'test_delegate', got %q", decl.Name)
	}
}

// mockSummonTool is a minimal tool.Tool for summon integration tests.
type mockSummonTool struct {
	name string
	desc string
}

func (m *mockSummonTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: m.desc,
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"input": {Type: "string"},
			},
		},
	}
}

var _ tool.Tool = (*mockSummonTool)(nil)
