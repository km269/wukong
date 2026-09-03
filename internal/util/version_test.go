package util

import "testing"

// TestResolvedVersion verifies the init-time fallback chain: under
// `go test` the main module version is "(devel)", so Version must
// degrade to "dev" while VCS metadata (present because tests run
// from the repo) fills commit and date.
func TestResolvedVersion(t *testing.T) {
	if Version == "" {
		t.Fatal("Version empty after init")
	}
	if GitCommit == "" || GitCommit == "fix commit" {
		t.Fatalf("GitCommit = %q, want resolved or unknown", GitCommit)
	}
	if BuildDate == "" {
		t.Fatal("BuildDate empty after init")
	}
	if len(GitCommit) > 12 {
		t.Errorf("GitCommit = %q, want short form (<=12 chars)", GitCommit)
	}
}

func TestShortCommit(t *testing.T) {
	if got := shortCommit(""); got != "unknown" {
		t.Errorf("shortCommit(\"\") = %q, want unknown", got)
	}
	full := "0123456789abcdef"
	if got := shortCommit(full); got != "0123456789ab" {
		t.Errorf("shortCommit(full) = %q", got)
	}
	if got := shortCommit("abc"); got != "abc" {
		t.Errorf("shortCommit(abc) = %q", got)
	}
}

func TestOrUnknown(t *testing.T) {
	if orUnknown("") != "unknown" {
		t.Error("empty should map to unknown")
	}
	if orUnknown("x") != "x" {
		t.Error("non-empty should pass through")
	}
}
