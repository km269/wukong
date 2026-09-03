package antibot

import (
	"testing"
	"time"
)

func TestEscalatorAutoEscalate(t *testing.T) {
	e := NewEscalator(EscalatorConfig{
		InitialLevel: LevelNone,
		MaxLevel:     LevelAggressive,
		AutoEscalate: true,
		MaxRetries:   3,
		Cooldown:     10 * time.Millisecond,
	})

	// First hit: should escalate from None → Flags.
	retry, delay, level, _ := e.Check(
		"https://example.com", ReasonForbidden, 403)
	if !retry {
		t.Fatal("expected retry on first hit")
	}
	if level != LevelFlags {
		t.Errorf("expected level Flags, got %s", level)
	}
	if delay == 0 {
		t.Error("expected non-zero delay")
	}

	// Second hit: Flags → Stealth.
	retry, _, level, _ = e.Check(
		"https://example.com", ReasonForbidden, 403)
	if !retry {
		t.Fatal("expected retry on second hit")
	}
	if level != LevelStealth {
		t.Errorf("expected level Stealth, got %s", level)
	}

	// Third hit: Stealth → Aggressive.
	retry, _, level, _ = e.Check(
		"https://example.com", ReasonForbidden, 403)
	if !retry {
		t.Fatal("expected retry on third hit")
	}
	if level != LevelAggressive {
		t.Errorf("expected level Aggressive, got %s", level)
	}

	// Fourth hit: MaxRetries exceeded (retry count 4 > 3), should backoff.
	retry, _, level, _ = e.Check(
		"https://example.com", ReasonForbidden, 403)
	if retry {
		t.Fatal("expected no retry when max retries exceeded")
	}
	if level != LevelBackoff {
		t.Errorf("expected level Backoff, got %s", level)
	}
}

func TestEscalatorBackoff(t *testing.T) {
	e := NewEscalator(EscalatorConfig{
		InitialLevel: LevelNone,
		MaxLevel:     LevelAggressive,
		AutoEscalate: true,
		MaxRetries:   2,
		Cooldown:     1 * time.Millisecond,
	})

	url := "https://blocked.example.com"

	// 3 hits (exceeds MaxRetries of 2).
	e.Check(url, ReasonForbidden, 403)                       // → LevelFlags, retry 1
	e.Check(url, ReasonForbidden, 403)                       // → LevelStealth, retry 2
	retry, _, level, _ := e.Check(url, ReasonForbidden, 403) // → LevelBackoff, retry 3

	if retry {
		t.Error("expected no retry after exceeding MaxRetries")
	}
	if level != LevelBackoff {
		t.Errorf("expected LevelBackoff, got %s", level)
	}
}

func TestEscalatorDisabled(t *testing.T) {
	e := NewEscalator(EscalatorConfig{
		AutoEscalate: false,
	})

	retry, _, _, _ := e.Check(
		"https://example.com", ReasonForbidden, 403)
	if retry {
		t.Error("expected no retry when auto-escalate disabled")
	}
}

func TestEscalatorReset(t *testing.T) {
	e := NewEscalator(EscalatorConfig{
		AutoEscalate: true,
		MaxLevel:     LevelAggressive,
		MaxRetries:   3,
		Cooldown:     10 * time.Millisecond,
	})

	e.Check("https://example.com", ReasonForbidden, 403)
	e.Check("https://example.com", ReasonForbidden, 403)

	if len(e.History) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(e.History))
	}

	e.Reset()

	if e.CurrentLevel != LevelNone {
		t.Errorf("expected LevelNone after reset, got %s", e.CurrentLevel)
	}
	if len(e.History) != 0 {
		t.Errorf("expected empty history after reset, got %d", len(e.History))
	}
}

func TestEscalatorJitterDelay(t *testing.T) {
	e := NewEscalator(EscalatorConfig{
		InitialLevel: LevelAggressive,
		MaxLevel:     LevelAggressive,
	})

	for i := 0; i < 10; i++ {
		d := e.JitterDelay()
		// Aggressive: 2-8s.
		if d < 2*time.Second || d > 8*time.Second {
			t.Errorf("jitter delay out of range: %v", d)
		}
	}
}

func TestEscalatorStats(t *testing.T) {
	e := NewEscalator(EscalatorConfig{
		AutoEscalate: true,
	})

	stats := e.Stats()
	if stats != "antibot: no blocking detected" {
		t.Errorf("expected no blocking stats, got %s", stats)
	}

	e.Check("https://example.com", ReasonForbidden, 403)

	stats = e.Stats()
	if stats == "antibot: no blocking detected" {
		t.Error("expected blocking stats after detection")
	}
}

func TestLevelStrings(t *testing.T) {
	if LevelNone.String() != "none" {
		t.Errorf("LevelNone = %s", LevelNone.String())
	}
	if LevelFlags.String() != "flags" {
		t.Errorf("LevelFlags = %s", LevelFlags.String())
	}
	if LevelStealth.String() != "stealth" {
		t.Errorf("LevelStealth = %s", LevelStealth.String())
	}
	if LevelAggressive.String() != "aggressive" {
		t.Errorf("LevelAggressive = %s", LevelAggressive.String())
	}
	if LevelBackoff.String() != "backoff" {
		t.Errorf("LevelBackoff = %s", LevelBackoff.String())
	}
}
