package cli

import (
	"regexp"
	"testing"
)

// --- A5: session-startup decision helpers ---

func TestResolveUserID_PriorityOrder(t *testing.T) {
	t.Setenv("USER", "")
	t.Setenv("USERDOMAIN", "")
	t.Setenv("USERNAME", "")

	// 1. USER wins when set (Unix-style).
	t.Setenv("USER", "alice")
	if got := resolveUserID(); got != "alice" {
		t.Errorf("with USER set, got %q, want alice", got)
	}

	// 2. Without USER, Windows DOMAIN\\USERNAME combined.
	t.Setenv("USER", "")
	t.Setenv("USERDOMAIN", "CORP")
	t.Setenv("USERNAME", "bob")
	if got := resolveUserID(); got != `CORP\bob` {
		t.Errorf("with USERDOMAIN+USERNAME, got %q, want CORP\\bob", got)
	}

	// 3. USERNAME alone (no domain) is used next.
	t.Setenv("USERDOMAIN", "")
	if got := resolveUserID(); got != "bob" {
		t.Errorf("with USERNAME only, got %q, want bob", got)
	}

	// 4. SYSTEM user is rejected, falling back further.
	t.Setenv("USERNAME", "SYSTEM")
	if got := resolveUserID(); got == "SYSTEM" {
		t.Errorf("SYSTEM must not be used as user id, got %q", got)
	}
}

func TestResolveUserID_EmptyFallsBackToNonEmpty(t *testing.T) {
	t.Setenv("USER", "")
	t.Setenv("USERDOMAIN", "")
	t.Setenv("USERNAME", "")

	got := resolveUserID()
	if got == "" {
		t.Error("resolveUserID must never return empty (hostname/default fallback)")
	}
}

func TestResolveSessionID_GeneratesFreshUUID(t *testing.T) {
	a := resolveSessionID()
	b := resolveSessionID()

	if a == "" || b == "" {
		t.Fatal("resolveSessionID must return a non-empty id")
	}
	if a == b {
		t.Error("two calls must generate different session ids")
	}
	// UUID v4 shape: 8-4-4-4-12 hex.
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !re.MatchString(a) {
		t.Errorf("session id %q does not look like a UUID v4", a)
	}
}

func TestResolveWorkingDir_ReturnsGetwd(t *testing.T) {
	got := resolveWorkingDir()
	if got == "" {
		t.Fatal("resolveWorkingDir must return a non-empty dir in test context")
	}
}
