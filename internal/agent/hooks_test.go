package agent

import (
	"context"
	"errors"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestHookRegistry_PreStepEmpty(t *testing.T) {
	r := NewHookRegistry()
	msg := model.Message{Role: model.RoleUser, Content: "hi"}
	out, reject, reason, err := r.RunPreStep(
		context.Background(), "s", "u", msg,
	)
	if err != nil || reject || reason != "" {
		t.Errorf("empty registry should pass-through, got reject=%v reason=%q err=%v",
			reject, reason, err)
	}
	if out.Content != "hi" {
		t.Errorf("message mutated by empty registry: %q", out.Content)
	}
}

func TestHookRegistry_PreStepWaterfallRewrite(t *testing.T) {
	r := NewHookRegistry()
	// Two hooks, each appends a marker to the message content.
	r.RegisterPreStep(PreStepFunc{
		N: "append-a",
		F: func(_ context.Context, _, _ string, m model.Message) (model.Message, bool, error) {
			m.Content = "[a] " + m.Content
			return m, false, nil
		},
	})
	r.RegisterPreStep(PreStepFunc{
		N: "append-b",
		F: func(_ context.Context, _, _ string, m model.Message) (model.Message, bool, error) {
			m.Content = "[b] " + m.Content
			return m, false, nil
		},
	})

	out, reject, _, err := r.RunPreStep(
		context.Background(), "s", "u",
		model.Message{Role: model.RoleUser, Content: "base"},
	)
	if err != nil || reject {
		t.Fatalf("unexpected reject/err: reject=%v err=%v", reject, err)
	}
	// Waterfall order: a sees "base" → "[a] base"; b sees that → "[b] [a] base".
	want := "[b] [a] base"
	if out.Content != want {
		t.Errorf("waterfall order wrong:\n got: %q\nwant: %q", out.Content, want)
	}
}

func TestHookRegistry_PreStepRejectStopsChain(t *testing.T) {
	r := NewHookRegistry()
	called := 0
	r.RegisterPreStep(PreStepFunc{
		N: "rejector",
		F: func(_ context.Context, _, _ string, m model.Message) (model.Message, bool, error) {
			called++
			return m, true, nil // reject; reason is the hook Name()
		},
	})
	// This second hook must NOT run because the first rejected.
	r.RegisterPreStep(PreStepFunc{
		N: "never",
		F: func(_ context.Context, _, _ string, m model.Message) (model.Message, bool, error) {
			called++
			return m, false, nil
		},
	})

	_, reject, reason, err := r.RunPreStep(
		context.Background(), "s", "u",
		model.Message{Role: model.RoleUser, Content: "x"},
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !reject {
		t.Fatal("expected reject=true")
	}
	if reason != "rejector" {
		t.Errorf("reason should be hook name %q, got %q", "rejector", reason)
	}
	if called != 1 {
		t.Errorf("reject should stop the chain; called=%d, want 1", called)
	}
}

func TestHookRegistry_PreStepErrorAborts(t *testing.T) {
	r := NewHookRegistry()
	r.RegisterPreStep(PreStepFunc{
		N: "failer",
		F: func(_ context.Context, _, _ string, m model.Message) (model.Message, bool, error) {
			return m, false, errors.New("boom")
		},
	})
	_, reject, _, err := r.RunPreStep(
		context.Background(), "s", "u",
		model.Message{Role: model.RoleUser, Content: "x"},
	)
	if err == nil {
		t.Fatal("expected error from failing hook")
	}
	if reject {
		t.Error("error should not set reject=true")
	}
}

func TestHookRegistry_PreToolFirstRejectWins(t *testing.T) {
	r := NewHookRegistry()
	called := 0
	r.RegisterPreToolExecute(toolHookFunc{
		n: "allow",
		f: func(_ context.Context, _ string, _ []byte) (bool, string, error) {
			called++
			return false, "", nil
		},
	})
	r.RegisterPreToolExecute(toolHookFunc{
		n: "block",
		f: func(_ context.Context, _ string, _ []byte) (bool, string, error) {
			called++
			return true, "dangerous", nil
		},
	})
	// This third hook must NOT run — the second already rejected.
	r.RegisterPreToolExecute(toolHookFunc{
		n: "never",
		f: func(_ context.Context, _ string, _ []byte) (bool, string, error) {
			called++
			return false, "", nil
		},
	})

	reject, reason, err := r.RunPreToolExecute(
		context.Background(), "bash", []byte(`{"cmd":"rm -rf /"}`),
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !reject {
		t.Fatal("expected reject=true")
	}
	if reason != "dangerous" {
		t.Errorf("reason: got %q, want %q", reason, "dangerous")
	}
	if called != 2 {
		t.Errorf("first-reject should stop chain; called=%d, want 2", called)
	}
}

// toolHookFunc adapts a plain function to PreToolExecuteHook.
type toolHookFunc struct {
	n string
	f func(context.Context, string, []byte) (bool, string, error)
}

func (h toolHookFunc) Name() string { return h.n }
func (h toolHookFunc) BeforeToolExecute(
	ctx context.Context, toolName string, args []byte,
) (bool, string, error) {
	return h.f(ctx, toolName, args)
}

func TestHookRegistry_HasHooksFlags(t *testing.T) {
	r := NewHookRegistry()
	if r.HasPreStepHooks() || r.HasPreToolHooks() {
		t.Fatal("empty registry should report no hooks")
	}
	r.RegisterPreStep(PreStepFunc{N: "x", F: func(context.Context, string, string, model.Message) (model.Message, bool, error) {
		return model.Message{}, false, nil
	}})
	if !r.HasPreStepHooks() {
		t.Error("HasPreStepHooks should be true after registering a pre-step hook")
	}
	if r.HasPreToolHooks() {
		t.Error("HasPreToolHooks should still be false")
	}
}

func TestPreStepRejectStream_MarkerCarriesReason(t *testing.T) {
	ch := newPreStepRejectStream("policy-guard")
	evt, ok := <-ch
	if !ok {
		t.Fatal("expected one marker event before close")
	}
	// Channel must close after the single marker.
	if _, stillOpen := <-ch; stillOpen {
		t.Error("stream should close after the marker event")
	}
	if !IsPreStepReject(evt) {
		t.Errorf("expected pre-step reject marker, got Tag=%q", evt.Tag)
	}
	if got := PreStepRejectReason(evt); got != "policy-guard" {
		t.Errorf("reason: got %q, want %q", got, "policy-guard")
	}
	// The marker is a pure control event: it must NOT carry an
	// embedded *model.Response (which would make RunStream treat it
	// as model content). RunStream short-circuits markers before
	// touching evt.Error/evt.Response precisely because Response is
	// nil here — accessing evt.Error would dereference the nil
	// embedded pointer. So we assert Response == nil, not evt.Error.
	if evt.Response != nil {
		t.Errorf("reject marker must not carry a Response (model content)")
	}
}

func TestPreStepRejectHelpers_NonMarkerSafe(t *testing.T) {
	// Non-marker events must be identified as non-rejects and
	// return empty reason, without panicking.
	if IsPreStepReject(nil) {
		t.Error("nil event should not be a reject marker")
	}
	if PreStepRejectReason(nil) != "" {
		t.Error("nil event reason should be empty")
	}
	plain := &event.Event{Tag: "something_else"}
	if IsPreStepReject(plain) {
		t.Error("non-reject Tag should not match")
	}
	if PreStepRejectReason(plain) != "" {
		t.Error("non-reject event reason should be empty")
	}
	// Marker without Extensions payload: still a reject, empty reason.
	bare := &event.Event{Tag: preStepRejectTag}
	if !IsPreStepReject(bare) {
		t.Error("bare marker should still be a reject")
	}
	if PreStepRejectReason(bare) != "" {
		t.Errorf("bare marker reason should be empty, got %q", PreStepRejectReason(bare))
	}
}
