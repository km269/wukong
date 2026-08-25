package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/km269/wukong/internal/util"
)

// newTestEventLog opens a fresh in-memory-backed ModelEventLog for
// testing. It uses a temp file because modernc.org/sqlite needs a
// path; the file is removed by t.Cleanup.
func newTestEventLog(t *testing.T) *ModelEventLog {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model_events_test.db")
	pool := util.NewDatabasePool(path)
	t.Cleanup(func() { _ = pool.Close() })

	db, err := pool.GetDB()
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	mel, err := NewModelEventLog(db)
	if err != nil {
		t.Fatalf("NewModelEventLog: %v", err)
	}
	return mel
}

func TestModelEventLog_AppendAndQuery(t *testing.T) {
	mel := newTestEventLog(t)
	ctx := context.Background()

	// Simulate one enriched turn: turn_start, user_message,
	// context_inject(wakeup), model_message, turn_end.
	steps := []struct {
		eventType, source, payload string
	}{
		{EventTurnStart, "", "hello"},
		{EventUserMessage, "", "hello"},
		{EventContextInject, "wakeup", "[Context from past]"},
		{EventModelMessage, "", "[Context from past]\n\n[Current user message]\nhello"},
		{EventTurnEnd, "assistant", "hi there"},
	}
	for _, s := range steps {
		if err := mel.Append(ctx, "sess-1", "user-1",
			s.eventType, s.source, s.payload); err != nil {
			t.Fatalf("Append %s: %v", s.eventType, err)
		}
	}

	events, err := mel.Query(ctx, "sess-1", 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(events) != len(steps) {
		t.Fatalf("expected %d events, got %d", len(steps), len(events))
	}
	// Verify monotonic seq.
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Errorf("event %d seq = %d, want %d", i, e.Seq, i+1)
		}
	}
	// Verify the model_message witness is the final enriched content.
	got, err := mel.ReplayModelMessage(ctx, "sess-1")
	if err != nil {
		t.Fatalf("ReplayModelMessage: %v", err)
	}
	want := "[Context from past]\n\n[Current user message]\nhello"
	if got != want {
		t.Errorf("replayed model message mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestModelEventLog_ReplayMessages_ReconstructsFinal(t *testing.T) {
	// The core invariant: replay must reconstruct exactly what the
	// model saw. The model_message event is authoritative, so
	// replay must yield it verbatim (not a sum of injects).
	mel := newTestEventLog(t)
	ctx := context.Background()

	turn := []struct {
		eventType, source, payload string
	}{
		{EventTurnStart, "", "what is x"},
		{EventUserMessage, "", "what is x"},
		{EventContextInject, "recall", "[Relevant history]\n1. x means y"},
		{EventContextInject, "persistent", "[Remembered facts]\n1. z is true"},
		{EventModelMessage, "", "[Relevant history]\n1. x means y\n\n[Remembered facts]\n1. z is true\n\n[User message]\nwhat is x"},
	}
	for _, s := range turn {
		if err := mel.Append(ctx, "s2", "u2",
			s.eventType, s.source, s.payload); err != nil {
			t.Fatalf("Append %s: %v", s.eventType, err)
		}
	}

	msgs, err := mel.ReplayMessages(ctx, "s2")
	if err != nil {
		t.Fatalf("ReplayMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 reconstructed message, got %d", len(msgs))
	}
	want := "[Relevant history]\n1. x means y\n\n[Remembered facts]\n1. z is true\n\n[User message]\nwhat is x"
	if msgs[0].Content != want {
		t.Errorf("replay mismatch:\n got: %q\nwant: %q", msgs[0].Content, want)
	}
}

func TestModelEventLog_VerifyInvariant_NoViolation(t *testing.T) {
	mel := newTestEventLog(t)
	ctx := context.Background()

	final := "enriched final content"
	if err := mel.Append(ctx, "s3", "u3",
		EventModelMessage, "", final); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Same content → no violation (warning is only logged on
	// mismatch; here we just assert no error is returned).
	if err := mel.VerifyInvariant(ctx, "s3", final); err != nil {
		t.Errorf("VerifyInvariant (match) returned err: %v", err)
	}
	// Mismatch → VerifyInvariant logs a warning but still returns
	// nil (the contract says callers should log, not fail).
	if err := mel.VerifyInvariant(ctx, "s3", "different content"); err != nil {
		t.Errorf("VerifyInvariant (mismatch) returned err: %v", err)
	}
}

func TestModelEventLog_EmptySessionReplay(t *testing.T) {
	mel := newTestEventLog(t)
	ctx := context.Background()

	// A session with no model_message events: replay falls back to
	// user_message events (best-effort inspection).
	if err := mel.Append(ctx, "s4", "u4",
		EventUserMessage, "", "raw input only"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	msgs, err := mel.ReplayMessages(ctx, "s4")
	if err != nil {
		t.Fatalf("ReplayMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "raw input only" {
		t.Errorf("expected fallback to user_message, got %+v", msgs)
	}

	// ReplayModelMessage returns "" when no model_message exists.
	got, err := mel.ReplayModelMessage(ctx, "s4")
	if err != nil {
		t.Fatalf("ReplayModelMessage: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty replay, got %q", got)
	}
}

func TestModelEventLog_SequenceIsPerSession(t *testing.T) {
	mel := newTestEventLog(t)
	ctx := context.Background()

	// Two independent sessions each start at seq 1.
	for _, sess := range []string{"a", "b"} {
		if err := mel.Append(ctx, sess, "u",
			EventUserMessage, "", "x"); err != nil {
			t.Fatalf("Append %s: %v", sess, err)
		}
	}
	// Second event for session "a" must be seq 2, not 3.
	if err := mel.Append(ctx, "a", "u",
		EventModelMessage, "", "y"); err != nil {
		t.Fatalf("Append a #2: %v", err)
	}
	events, err := mel.Query(ctx, "a", 0)
	if err != nil {
		t.Fatalf("Query a: %v", err)
	}
	if len(events) != 2 || events[1].Seq != 2 {
		t.Errorf("session a seq not independent: %+v", events)
	}
}
