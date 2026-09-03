package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// fakeApprovalSink is an in-memory ApprovalSink for testing the ACP
// approval endpoints without wiring a real *security.ApprovalBroker.
type fakeApprovalSink struct {
	pending  []ApprovalRequestDTO
	known    map[string]bool // IDs Resolve will accept
	resolved map[string]string
	err      error
}

func (f *fakeApprovalSink) PendingRequests() []ApprovalRequestDTO {
	return f.pending
}

func (f *fakeApprovalSink) Resolve(
	reqID, decision, reason, approver string,
) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if !f.known[reqID] {
		return false, nil
	}
	if f.resolved == nil {
		f.resolved = map[string]string{}
	}
	f.resolved[reqID] = decision
	return true, nil
}

func newACPWithSink(t *testing.T, sink ApprovalSink) *ACPServer {
	t.Helper()
	mdl := openai.New("test-model")
	ag := llmagent.New("test-agent", llmagent.WithModel(mdl))
	r := runner.NewRunner("test-app", ag)
	srv, err := NewACPServer(&ACPServerConfig{
		Runner:       r,
		Agent:        ag,
		Path:         "/acp",
		ApprovalSink: sink,
	})
	if err != nil {
		t.Fatalf("NewACPServer: %v", err)
	}
	return srv
}

func TestACPServer_ApprovalsList_ReturnsPending(t *testing.T) {
	sink := &fakeApprovalSink{
		pending: []ApprovalRequestDTO{
			{ID: "req-1", ToolName: "bash", RiskReason: "high-risk"},
			{ID: "req-2", ToolName: "file_delete", RiskReason: "high-risk"},
		},
	}
	srv := newACPWithSink(t, sink)

	req := httptest.NewRequest(http.MethodGet, "/acp/approvals/list", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Approvals []ApprovalRequestDTO `json:"approvals"`
		Enabled   bool                 `json:"enabled"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Approvals) != 2 {
		t.Errorf("approvals len=%d want 2", len(body.Approvals))
	}
	if !body.Enabled {
		t.Error("enabled should be true when sink is wired")
	}
	if body.Approvals[0].ID != "req-1" {
		t.Errorf("first id=%q want req-1", body.Approvals[0].ID)
	}
}

func TestACPServer_ApprovalsList_NoSink_ReportsDisabled(t *testing.T) {
	srv := newACPWithSink(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/acp/approvals/list", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	var body struct {
		Approvals []ApprovalRequestDTO `json:"approvals"`
		Enabled   bool                 `json:"enabled"`
	}
	_ = json.NewDecoder(w.Body).Decode(&body)
	if body.Enabled {
		t.Error("enabled should be false with no sink")
	}
	if len(body.Approvals) != 0 {
		t.Errorf("approvals len=%d want 0", len(body.Approvals))
	}
}

func TestACPServer_ApprovalsList_MethodNotAllowed(t *testing.T) {
	srv := newACPWithSink(t, &fakeApprovalSink{})
	req := httptest.NewRequest(http.MethodPost, "/acp/approvals/list", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("code=%d want 405", w.Code)
	}
}

func TestACPServer_ApprovalsResolve_Success(t *testing.T) {
	sink := &fakeApprovalSink{known: map[string]bool{"req-1": true}}
	srv := newACPWithSink(t, sink)

	body := `{"request_id":"req-1","decision":"approved","reason":"ok","approver":"tester"}`
	req := httptest.NewRequest(
		http.MethodPost, "/acp/approvals/resolve",
		strings.NewReader(body),
	)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if sink.resolved["req-1"] != "approved" {
		t.Errorf("not resolved: %+v", sink.resolved)
	}
	var resp struct {
		Status    string `json:"status"`
		RequestID string `json:"request_id"`
		Decision  string `json:"decision"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "resolved" {
		t.Errorf("status=%q want resolved", resp.Status)
	}
	if resp.Decision != "approved" {
		t.Errorf("decision=%q want approved", resp.Decision)
	}
}

func TestACPServer_ApprovalsResolve_Deny(t *testing.T) {
	sink := &fakeApprovalSink{known: map[string]bool{"req-2": true}}
	srv := newACPWithSink(t, sink)
	body := `{"request_id":"req-2","decision":"denied","reason":"risky"}`
	req := httptest.NewRequest(http.MethodPost, "/acp/approvals/resolve", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d", w.Code)
	}
	if sink.resolved["req-2"] != "denied" {
		t.Errorf("not denied: %+v", sink.resolved)
	}
}

func TestACPServer_ApprovalsResolve_UnknownID_NotFound(t *testing.T) {
	sink := &fakeApprovalSink{known: map[string]bool{}} // no known IDs
	srv := newACPWithSink(t, sink)
	body := `{"request_id":"nope","decision":"approved"}`
	req := httptest.NewRequest(http.MethodPost, "/acp/approvals/resolve", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("code=%d want 404", w.Code)
	}
}

func TestACPServer_ApprovalsResolve_InvalidDecision_BadRequest(t *testing.T) {
	srv := newACPWithSink(t, &fakeApprovalSink{known: map[string]bool{"req-1": true}})
	body := `{"request_id":"req-1","decision":"maybe"}`
	req := httptest.NewRequest(http.MethodPost, "/acp/approvals/resolve", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code=%d want 400", w.Code)
	}
}

func TestACPServer_ApprovalsResolve_MissingRequestID_BadRequest(t *testing.T) {
	srv := newACPWithSink(t, &fakeApprovalSink{})
	body := `{"decision":"approved"}`
	req := httptest.NewRequest(http.MethodPost, "/acp/approvals/resolve", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("code=%d want 400", w.Code)
	}
}

func TestACPServer_ApprovalsResolve_NoSink_ServiceUnavailable(t *testing.T) {
	srv := newACPWithSink(t, nil)
	body := `{"request_id":"req-1","decision":"approved"}`
	req := httptest.NewRequest(http.MethodPost, "/acp/approvals/resolve", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("code=%d want 503", w.Code)
	}
}

func TestACPServer_ApprovalsResolve_MethodNotAllowed(t *testing.T) {
	srv := newACPWithSink(t, &fakeApprovalSink{})
	req := httptest.NewRequest(http.MethodGet, "/acp/approvals/resolve", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("code=%d want 405", w.Code)
	}
}
