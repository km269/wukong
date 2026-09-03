package security

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/km269/wukong/internal/config"
)

// fakeApprover is a synchronous Approver for testing. It records
// received requests and returns a canned response, optionally after
// a delay to simulate user think time.
type fakeApprover struct {
	mu    sync.Mutex
	calls []*ApprovalRequest
	resp  *ApprovalResponse
	err   error
	delay time.Duration
}

func (f *fakeApprover) RequestApproval(
	ctx context.Context, req *ApprovalRequest,
) (*ApprovalResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	resp, err := f.resp, f.err
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Return resp as-is (may be nil); the broker is responsible for
	// substituting a denied fallback when an approver returns nil.
	if resp != nil {
		resp.RequestID = req.ID
	}
	return resp, err
}

func (f *fakeApprover) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// waitPending polls until reqID is registered or times out.
func waitPending(t *testing.T, b *ApprovalBroker, id string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if b.GetPending(id) != nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("pending %q never registered", id)
}

func TestApprovalDecision_IsAllowed(t *testing.T) {
	if !ApprovalApproved.IsAllowed() {
		t.Error("approved should be allowed")
	}
	for _, d := range []ApprovalDecision{ApprovalDenied, ApprovalTimeout, ApprovalCanceled} {
		if d.IsAllowed() {
			t.Errorf("%q should not be allowed", d)
		}
	}
}

func TestApprovalContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if sid, uid := ApprovalContextFrom(ctx); sid != "" || uid != "" {
		t.Errorf("absent context should be empty: sid=%q uid=%q", sid, uid)
	}
	ctx2 := WithApprovalContext(ctx, "s2", "u2")
	sid, uid := ApprovalContextFrom(ctx2)
	if sid != "s2" || uid != "u2" {
		t.Errorf("round trip mismatch: sid=%q uid=%q", sid, uid)
	}
	// Original ctx is untouched (immutability).
	if sid, _ := ApprovalContextFrom(ctx); sid != "" {
		t.Errorf("original ctx mutated: sid=%q", sid)
	}
}

func TestApprovalBroker_ExternalResolve_UnblocksProducer(t *testing.T) {
	b := NewApprovalBroker(nil, 5*time.Second)
	req := &ApprovalRequest{
		ID: "req-1", SessionID: "s", UserID: "u", ToolName: "bash",
	}
	type result struct {
		r *ApprovalResponse
		e error
	}
	resCh := make(chan result, 1)
	go func() {
		r, e := b.Request(context.Background(), req)
		resCh <- result{r, e}
	}()

	waitPending(t, b, "req-1")

	ok, err := b.Resolve("req-1", &ApprovalResponse{
		Decision: ApprovalApproved,
		Reason:   "ok",
		Approver: "tester",
	})
	if err != nil || !ok {
		t.Fatalf("Resolve: ok=%v err=%v", ok, err)
	}

	select {
	case got := <-resCh:
		if got.e != nil {
			t.Fatalf("Request err: %v", got.e)
		}
		if got.r.Decision != ApprovalApproved {
			t.Errorf("decision=%v want approved", got.r.Decision)
		}
		if got.r.Reason != "ok" {
			t.Errorf("reason=%q want ok", got.r.Reason)
		}
		if got.r.Approver != "tester" {
			t.Errorf("approver=%q want tester", got.r.Approver)
		}
		if got.r.RequestID != "req-1" {
			t.Errorf("request id=%q want req-1", got.r.RequestID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not unblock after Resolve")
	}

	// After resolution the pending map should be empty.
	if b.GetPending("req-1") != nil {
		t.Error("pending entry not removed after Resolve")
	}
}

func TestApprovalBroker_Resolve_UnknownID_NoOp(t *testing.T) {
	b := NewApprovalBroker(nil, 5*time.Second)
	ok, err := b.Resolve("nope", &ApprovalResponse{Decision: ApprovalApproved})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Error("Resolve for unknown id should return false")
	}
}

func TestApprovalBroker_Timeout_ReturnsTimeoutDecision(t *testing.T) {
	b := NewApprovalBroker(nil, 30*time.Millisecond)
	req := &ApprovalRequest{ID: "req-to", ToolName: "bash"}
	start := time.Now()
	resp, err := b.Request(context.Background(), req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp == nil {
		t.Fatal("nil resp")
	}
	if resp.Decision != ApprovalTimeout {
		t.Errorf("decision=%v want timeout", resp.Decision)
	}
	if elapsed < 25*time.Millisecond {
		t.Errorf("returned too fast: %v", elapsed)
	}
}

func TestApprovalBroker_Cancel_MarksCanceled(t *testing.T) {
	b := NewApprovalBroker(nil, 5*time.Second)
	req := &ApprovalRequest{ID: "req-c", ToolName: "bash"}
	resCh := make(chan *ApprovalResponse, 1)
	go func() {
		r, _ := b.Request(context.Background(), req)
		resCh <- r
	}()

	waitPending(t, b, "req-c")

	if !b.Cancel("req-c", "session closed") {
		t.Fatal("Cancel returned false for pending request")
	}

	select {
	case got := <-resCh:
		if got.Decision != ApprovalCanceled {
			t.Errorf("decision=%v want canceled", got.Decision)
		}
		if got.Reason != "session closed" {
			t.Errorf("reason=%q want 'session closed'", got.Reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not unblock after Cancel")
	}
}

func TestApprovalBroker_SyncApprover_Delegates(t *testing.T) {
	fa := &fakeApprover{resp: &ApprovalResponse{
		Decision: ApprovalApproved, Reason: "sync-ok", Approver: "tui",
	}}
	b := NewApprovalBroker(fa, 5*time.Second)
	req := &ApprovalRequest{ID: "req-s", ToolName: "bash"}

	resp, err := b.Request(context.Background(), req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Decision != ApprovalApproved {
		t.Errorf("decision=%v want approved", resp.Decision)
	}
	if resp.Reason != "sync-ok" {
		t.Errorf("reason=%q want sync-ok", resp.Reason)
	}
	if fa.callCount() != 1 {
		t.Errorf("approver calls=%d want 1", fa.callCount())
	}
}

func TestApprovalBroker_SyncApprover_NilResponse_Denies(t *testing.T) {
	// An Approver returning a nil response must not crash; broker
	// substitutes a denied response.
	fa := &fakeApprover{resp: nil}
	b := NewApprovalBroker(fa, 5*time.Second)
	req := &ApprovalRequest{ID: "req-n", ToolName: "bash"}
	resp, err := b.Request(context.Background(), req)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp == nil || resp.Decision != ApprovalDenied {
		t.Errorf("expected denied fallback, got %+v", resp)
	}
}

func TestApprovalBroker_Pending_ListsOutstanding(t *testing.T) {
	b := NewApprovalBroker(nil, 5*time.Second)
	req := &ApprovalRequest{ID: "req-p", ToolName: "bash"}
	go func() {
		_, _ = b.Request(context.Background(), req)
	}()
	waitPending(t, b, "req-p")

	list := b.Pending()
	if len(list) != 1 {
		t.Fatalf("Pending len=%d want 1", len(list))
	}
	if list[0].ID != "req-p" {
		t.Errorf("Pending id=%q want req-p", list[0].ID)
	}
	b.Cancel("req-p", "done")
}

func TestApprovalBroker_Request_AutoFillsIDAndDeadline(t *testing.T) {
	b := NewApprovalBroker(nil, 40*time.Millisecond)
	req := &ApprovalRequest{ToolName: "bash"} // no ID, no timestamps
	go func() {
		_, _ = b.Request(context.Background(), req)
	}()
	// Poll until the broker registers the (auto-ID'd) request. The
	// ID is assigned inside Request, so we observe it via Pending().
	var pending *ApprovalRequest
	for i := 0; i < 200; i++ {
		if list := b.Pending(); len(list) > 0 {
			pending = list[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if pending == nil {
		t.Fatal("request never registered")
	}
	if pending.ID == "" {
		t.Error("ID not auto-filled")
	}
	if pending.CreatedAt.IsZero() {
		t.Error("CreatedAt not auto-filled")
	}
	if pending.Deadline.IsZero() {
		t.Error("Deadline not auto-filled")
	}
	if !pending.Deadline.After(pending.CreatedAt) {
		t.Error("Deadline should be after CreatedAt")
	}
	b.Cancel(pending.ID, "cleanup")
}

func TestGuard_RequestApproval_NoBroker_LegacyDeny(t *testing.T) {
	// Safety invariant: with no broker wired, approval is immediately
	// denied — the legacy synchronous-deny behavior must hold.
	g := NewGuard(&config.SecurityConfig{PermissionMode: config.PermissionSmart})
	resp, err := g.RequestApproval(
		context.Background(), "s", "u", "bash",
		[]byte(`{"command":"ls"}`),
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp == nil {
		t.Fatal("nil resp")
	}
	if resp.Decision != ApprovalDenied {
		t.Errorf("decision=%v want denied (legacy)", resp.Decision)
	}
	if resp.RequestID == "" {
		t.Error("empty request id")
	}
	if resp.Reason == "" {
		t.Error("legacy deny should carry a reason")
	}
}

func TestGuard_RequestApproval_WithBroker_Delegates(t *testing.T) {
	g := NewGuard(&config.SecurityConfig{PermissionMode: config.PermissionSmart})
	fa := &fakeApprover{resp: &ApprovalResponse{
		Decision: ApprovalApproved, Reason: "ok",
	}}
	g.SetApprovalBroker(NewApprovalBroker(fa, 5*time.Second))

	resp, err := g.RequestApproval(
		context.Background(), "s1", "u1", "bash",
		[]byte(`{"command":"ls"}`),
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Decision != ApprovalApproved {
		t.Errorf("decision=%v want approved", resp.Decision)
	}
	if resp.RequestID == "" {
		t.Error("empty request id")
	}
	if fa.callCount() != 1 {
		t.Fatalf("approver calls=%d want 1", fa.callCount())
	}
	fa.mu.Lock()
	got := fa.calls[0]
	fa.mu.Unlock()
	if got.SessionID != "s1" || got.UserID != "u1" {
		t.Errorf("attribution mismatch: sid=%q uid=%q",
			got.SessionID, got.UserID)
	}
	if got.ToolName != "bash" {
		t.Errorf("tool=%q want bash", got.ToolName)
	}
	if got.RiskReason == "" {
		t.Error("empty risk reason")
	}
	if string(got.Arguments) != `{"command":"ls"}` {
		t.Errorf("args=%q", string(got.Arguments))
	}
}

func TestGuard_ApprovalRiskReason(t *testing.T) {
	t.Run("manual", func(t *testing.T) {
		g := NewGuard(&config.SecurityConfig{PermissionMode: config.PermissionManual})
		got := g.approvalRiskReason("anything", nil)
		if got != "manual mode: all tool calls require approval" {
			t.Errorf("manual reason=%q", got)
		}
	})
	t.Run("chat_only", func(t *testing.T) {
		g := NewGuard(&config.SecurityConfig{PermissionMode: config.PermissionChatOnly})
		got := g.approvalRiskReason("anything", nil)
		if got != "chat_only mode: tool calls blocked" {
			t.Errorf("chat_only reason=%q", got)
		}
	})
	t.Run("smart_dangerous_command", func(t *testing.T) {
		g := NewGuard(&config.SecurityConfig{PermissionMode: config.PermissionSmart})
		got := g.approvalRiskReason("bash", []byte(`{"command":"rm -rf /"}`))
		if got != "dangerous command pattern detected" {
			t.Errorf("dangerous reason=%q", got)
		}
	})
	t.Run("smart_high_risk_tool", func(t *testing.T) {
		g := NewGuard(&config.SecurityConfig{PermissionMode: config.PermissionSmart})
		got := g.approvalRiskReason("file_delete", nil)
		if got != "high-risk tool call" {
			t.Errorf("high-risk reason=%q", got)
		}
	})
}

func TestGuard_HasApprovalBroker(t *testing.T) {
	g := NewGuard(&config.SecurityConfig{PermissionMode: config.PermissionSmart})
	if g.HasApprovalBroker() {
		t.Error("default should have no broker")
	}
	g.SetApprovalBroker(NewApprovalBroker(nil, time.Second))
	if !g.HasApprovalBroker() {
		t.Error("broker not detected after set")
	}
}
