// Package cli — approval_adapter.go
//
// approvalSinkAdapter bridges *security.ApprovalBroker to the
// server.ApprovalSink interface. It lives in the cli composition
// root to break the server → security import cycle: server cannot
// import security because config embeds server types, so the ACP
// server speaks in server.ApprovalRequestDTO and the cli adapter
// translates to/from security.ApprovalRequest/Response.
package cli

import (
	"encoding/json"
	"time"

	"github.com/km269/wukong/internal/security"
	"github.com/km269/wukong/internal/server"
)

type approvalSinkAdapter struct {
	broker *security.ApprovalBroker
}

// PendingRequests snapshots the broker's outstanding requests into
// the server-facing DTO. Arguments are copied so the DTO does not
// alias the broker's internal buffer.
func (a *approvalSinkAdapter) PendingRequests() []server.ApprovalRequestDTO {
	if a.broker == nil {
		return nil
	}
	src := a.broker.Pending()
	out := make([]server.ApprovalRequestDTO, 0, len(src))
	for _, r := range src {
		var args json.RawMessage
		if r.Arguments != nil {
			args = append(json.RawMessage(nil), r.Arguments...)
		}
		out = append(out, server.ApprovalRequestDTO{
			ID:         r.ID,
			SessionID:  r.SessionID,
			UserID:     r.UserID,
			ToolName:   r.ToolName,
			Arguments:  args,
			RiskReason: r.RiskReason,
			CreatedAt:  r.CreatedAt.Format(time.RFC3339),
			Deadline:   r.Deadline.Format(time.RFC3339),
		})
	}
	return out
}

// Resolve delivers an external (HTTP) decision to the broker. The
// decision string is validated by the HTTP handler before reaching
// here, so only "approved"/"denied" arrive.
func (a *approvalSinkAdapter) Resolve(
	reqID, decision, reason, approver string,
) (bool, error) {
	if a.broker == nil {
		return false, nil
	}
	resp := &security.ApprovalResponse{
		RequestID: reqID,
		Decision:  security.ApprovalDecision(decision),
		Reason:    reason,
		Approver:  approver,
	}
	return a.broker.Resolve(reqID, resp)
}
