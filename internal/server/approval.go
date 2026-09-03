// Package server — approval.go
//
// HTTP-facing approval endpoints for the ACP server. Lets external
// human approvers (HTTP clients) list pending approval requests and
// resolve them by ID, unblocking the agent loop's BeforeTool gate.
//
// The server package cannot import security directly
// (server → security → config → server would cycle, same reason
// ToolGuardCheck is a callback). So this file defines DTO + an
// ApprovalSink interface; the cli composition root injects an
// adapter over *security.ApprovalBroker that satisfies it.
//
// Endpoints (mounted under the ACP path, e.g. /acp):
//
//	GET  /approvals/list    — list pending approval requests
//	POST /approvals/resolve — resolve a pending request by ID
//
// Both endpoints inherit TLS/auth/rate-limit middleware applied by
// ACPServer.Start (ApplySecurity over Handler()), so they are
// protected identically to /message/send and /tools/call.
package server

import (
	"encoding/json"
	"net/http"

	"log/slog"
)

// ApprovalRequestDTO is the HTTP-facing shape of a pending approval
// request. Mirrors security.ApprovalRequest but lives in the server
// package to avoid the server → security import cycle.
type ApprovalRequestDTO struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"session_id"`
	UserID     string          `json:"user_id"`
	ToolName   string          `json:"tool_name"`
	Arguments  json.RawMessage `json:"arguments"`
	RiskReason string          `json:"risk_reason"`
	CreatedAt  string          `json:"created_at"` // RFC3339
	Deadline   string          `json:"deadline"`   // RFC3339
}

// Approval decisions accepted by /approvals/resolve. Mirror the
// string values of security.ApprovalDecision. External callers may
// only POST approved or denied; timeout/canceled are system-emitted.
const (
	ApprovalDecisionApproved = "approved"
	ApprovalDecisionDenied   = "denied"
)

// ApprovalResolveRequest is the body POSTed to /approvals/resolve.
type ApprovalResolveRequest struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"` // "approved" | "denied"
	Reason    string `json:"reason"`
	Approver  string `json:"approver"`
}

// ApprovalSink exposes pending approval requests and lets external
// HTTP callers resolve them by ID. Implemented by an adapter over
// *security.ApprovalBroker in the cli composition root; defined
// here as an interface to break the import cycle.
type ApprovalSink interface {
	// PendingRequests returns a snapshot of outstanding requests.
	PendingRequests() []ApprovalRequestDTO
	// Resolve delivers a decision for reqID. Returns (true, nil)
	// when a pending request was matched; (false, nil) when no
	// pending request exists for the ID.
	Resolve(reqID, decision, reason, approver string) (bool, error)
}

// handleApprovalsList handles GET {path}/approvals/list.
func (s *ACPServer) handleApprovalsList(
	w http.ResponseWriter, r *http.Request,
) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use GET",
		})
		return
	}
	if s.sink == nil {
		// No broker wired (e.g. approval feature off); report empty.
		s.writeJSON(w, http.StatusOK, map[string]any{
			"approvals": []any{},
			"enabled":   false,
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"approvals": s.sink.PendingRequests(),
		"enabled":   true,
	})
}

// handleApprovalsResolve handles POST {path}/approvals/resolve.
func (s *ACPServer) handleApprovalsResolve(
	w http.ResponseWriter, r *http.Request,
) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use POST",
		})
		return
	}
	if s.sink == nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "approval sink not configured on this server",
		})
		return
	}

	var req ApprovalResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid json body: " + err.Error(),
		})
		return
	}
	if req.RequestID == "" {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "request_id is required",
		})
		return
	}
	// Only approve/deny are valid external decisions; timeout/canceled
	// are system-emitted and must not be POSTable.
	switch req.Decision {
	case ApprovalDecisionApproved, ApprovalDecisionDenied:
	default:
		s.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "decision must be 'approved' or 'denied'",
		})
		return
	}

	ok, err := s.sink.Resolve(
		req.RequestID, req.Decision, req.Reason, req.Approver,
	)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}
	if !ok {
		s.writeJSON(w, http.StatusNotFound, map[string]string{
			"error":      "no pending approval for request_id",
			"request_id": req.RequestID,
		})
		return
	}

	slog.Info("approval resolved via HTTP",
		"request_id", req.RequestID,
		"decision", req.Decision,
		"approver", req.Approver,
	)
	s.writeJSON(w, http.StatusOK, map[string]string{
		"status":     "resolved",
		"request_id": req.RequestID,
		"decision":   req.Decision,
	})
}
