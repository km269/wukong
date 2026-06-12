package summon

import (
	"context"
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestNewSummonManager(t *testing.T) {
	cfg := &config.SummonConfig{
		Enabled:       true,
		SkillsDir:     ".test_skills",
		MaxConcurrent: 3,
	}
	mgr := NewSummonManager(cfg, nil)
	if mgr == nil {
		t.Fatal("expected non-nil SummonManager")
	}
	if mgr.MaxConcurrent() != 3 {
		t.Errorf("expected max concurrent 3, got %d",
			mgr.MaxConcurrent())
	}
	if mgr.AvailableSlots() != 3 {
		t.Errorf("expected 3 available slots, got %d",
			mgr.AvailableSlots())
	}
}

func TestSummonManager_DefaultMaxConcurrent(t *testing.T) {
	cfg := &config.SummonConfig{
		Enabled:       true,
		SkillsDir:     ".test_skills",
		MaxConcurrent: 0, // should default to 5
	}
	mgr := NewSummonManager(cfg, nil)
	if mgr.MaxConcurrent() != 5 {
		t.Errorf("expected default max concurrent 5, got %d",
			mgr.MaxConcurrent())
	}
}

func TestSummonManager_NilConfig(t *testing.T) {
	mgr := NewSummonManager(nil, nil)
	if mgr == nil {
		t.Fatal("expected non-nil SummonManager with nil config")
	}
	if mgr.MaxConcurrent() != 5 {
		t.Errorf("expected default max concurrent 5, got %d",
			mgr.MaxConcurrent())
	}
}

func TestSummonManager_AcquireSlot(t *testing.T) {
	cfg := &config.SummonConfig{
		MaxConcurrent: 1,
	}
	mgr := NewSummonManager(cfg, nil)

	// Acquire the only slot
	release, err := mgr.AcquireSlot(context.Background())
	if err != nil {
		t.Fatalf("AcquireSlot should succeed: %v", err)
	}
	if release == nil {
		t.Fatal("expected non-nil release function")
	}

	// Verify slots are exhausted
	if mgr.AvailableSlots() != 0 {
		t.Errorf("expected 0 available slots, got %d",
			mgr.AvailableSlots())
	}

	// Second acquire should block (use cancelled context)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = mgr.AcquireSlot(ctx)
	if err == nil {
		t.Error("expected error when slots exhausted and ctx cancelled")
	}

	// Release and verify slot is available again
	release()
	if mgr.AvailableSlots() != 1 {
		t.Errorf("expected 1 available slot after release, got %d",
			mgr.AvailableSlots())
	}
}

func TestSummonManager_ListDelegates_Empty(t *testing.T) {
	mgr := NewSummonManager(&config.SummonConfig{}, nil)
	delegates := mgr.ListDelegates()
	if len(delegates) != 0 {
		t.Errorf("expected 0 delegates, got %d", len(delegates))
	}
}

func TestSummonManager_GetDelegate_NotFound(t *testing.T) {
	mgr := NewSummonManager(&config.SummonConfig{}, nil)
	d, ok := mgr.GetDelegate("nonexistent")
	if ok {
		t.Error("expected false for nonexistent delegate")
	}
	if d != nil {
		t.Error("expected nil delegate")
	}
}

func TestSummonManager_Close(t *testing.T) {
	mgr := NewSummonManager(&config.SummonConfig{}, nil)
	if err := mgr.Close(); err != nil {
		t.Errorf("Close should succeed: %v", err)
	}
}

func TestSummonManager_DelegateCount(t *testing.T) {
	mgr := NewSummonManager(&config.SummonConfig{}, nil)
	if mgr.DelegateCount() != 0 {
		t.Errorf("expected 0 delegates, got %d", mgr.DelegateCount())
	}
}

func TestSummonManager_ListSkills_Empty(t *testing.T) {
	mgr := NewSummonManager(&config.SummonConfig{}, nil)
	skills := mgr.ListSkills()
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestExtractDescription_Simple(t *testing.T) {
	content := "# My Skill\n\nThis skill helps with testing."
	desc := extractDescription(content)
	if desc != "This skill helps with testing." {
		t.Errorf("unexpected description: %q", desc)
	}
}

func TestExtractDescription_Empty(t *testing.T) {
	desc := extractDescription("")
	if desc != "Skill" {
		t.Errorf("expected 'Skill' for empty, got %q", desc)
	}
}

func TestExtractDescription_OnlyHeading(t *testing.T) {
	desc := extractDescription("# Just a heading\n\n")
	if desc != "Skill" {
		t.Errorf("expected 'Skill' for heading only, got %q", desc)
	}
}

func TestExtractDescription_LongLine(t *testing.T) {
	long := "# Skill\n" +
		"This is a very long line that exceeds one hundred characters " +
		"and should be truncated by the extractDescription function " +
		"which limits output to 100 characters max"
	desc := extractDescription(long)
	if len(desc) > 103 { // 100 + "..."
		t.Errorf("description too long: %d chars: %q",
			len(desc), desc)
	}
}
