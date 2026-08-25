// Package security — approval.go
//
// Asynchronous Approval protocol. Upgrades the Guard's synchronous
// "NeedsApproval → immediate deny" gate into an interruptible,
// human-in-the-loop decision flow.
//
// Roles:
//   - Producer: the agent loop's BeforeTool callback. When
//     Guard.NeedsApproval==true and a broker is wired, it calls
//     Guard.RequestApproval, which builds an ApprovalRequest and
//     delegates to the broker.
//   - Broker: ApprovalBroker tracks pending requests and routes
//     decisions back. Supports two consumer shapes:
//     (a) Synchronous Approver (TUI): blocks inside
//     Approver.RequestApproval until the user decides.
//     (b) External resolver (HTTP server): no Approver wired;
//     the broker parks the request and an HTTP handler calls
//     Broker.Resolve(id, resp) to unblock it.
//   - Consumer: TUI or HTTP server that surfaces requests to a
//     human and returns decisions.
//
// Safety invariant: when no broker is configured on the Guard,
// RequestApproval returns a denied response immediately — the
// legacy synchronous deny behavior is preserved, so safety never
// regresses when the feature is off.
package security

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/km269/wukong/internal/config"
)

// ApprovalDecision is the outcome of an approval request. Only
// ApprovalApproved allows the triggering tool to proceed.
type ApprovalDecision string

const (
	ApprovalApproved ApprovalDecision = "approved"
	ApprovalDenied   ApprovalDecision = "denied"
	ApprovalTimeout  ApprovalDecision = "timeout"
	ApprovalCanceled ApprovalDecision = "canceled"
)

// IsAllowed reports whether the decision permits the tool to run.
func (d ApprovalDecision) IsAllowed() bool {
	return d == ApprovalApproved
}

// ApprovalRequest describes a tool invocation that requires human
// approval before execution. Produced by the Guard; consumed by
// approvers. The ID is globally unique (UUIDv4) so HTTP/TUI
// consumers can address pending requests across processes.
type ApprovalRequest struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	UserID     string    `json:"user_id"`
	ToolName   string    `json:"tool_name"`
	Arguments  []byte    `json:"arguments"`
	RiskReason string    `json:"risk_reason"` // why approval is needed
	CreatedAt  time.Time `json:"created_at"`
	Deadline   time.Time `json:"deadline"` // absolute; after = timeout
}

// Expired reports whether the request deadline has passed.
func (r *ApprovalRequest) Expired() bool {
	return !r.Deadline.IsZero() && time.Now().After(r.Deadline)
}

// ApprovalResponse is the human (or delegate) decision for a
// request. Carried back to the producer via the broker.
type ApprovalResponse struct {
	RequestID string           `json:"request_id"`
	Decision  ApprovalDecision `json:"decision"`
	Reason    string           `json:"reason"`   // human-given rationale
	Approver  string           `json:"approver"` // who decided (TUI user / HTTP client id)
	DecidedAt time.Time        `json:"decided_at"`
}

// Approver is a synchronous human-in-the-loop decision sink. The
// implementation (e.g. TUI) blocks inside RequestApproval until the
// user decides, the deadline expires, or ctx is canceled.
//
// For the HTTP-server consumer shape, leave Approver nil on the
// broker and resolve pending requests via Broker.Resolve instead.
type Approver interface {
	RequestApproval(
		ctx context.Context,
		req *ApprovalRequest,
	) (*ApprovalResponse, error)
}

// ApprovalBroker coordinates asynchronous approval between the agent
// loop (producer) and approvers (consumers). It tracks pending
// requests so external callers can resolve them by ID, and enforces
// the deadline/timeout contract when no synchronous Approver is set.
type ApprovalBroker struct {
	approver   Approver      // optional; nil → external-resolver mode
	defaultTTL time.Duration // per-request TTL when caller omits deadline

	mu      sync.Mutex
	pending map[string]*pendingApproval
}

type pendingApproval struct {
	req   *ApprovalRequest
	reply chan *ApprovalResponse // buffered 1; closed on resolve
}

// NewApprovalBroker creates a broker. If approver is non-nil, the
// broker delegates each request to it (synchronous consumer shape,
// e.g. TUI). If approver is nil, requests are parked until an
// external caller resolves them by ID (HTTP consumer shape).
// defaultTTL is used when a request omits an explicit deadline; if
// zero, a 60s default is applied.
func NewApprovalBroker(approver Approver, defaultTTL time.Duration) *ApprovalBroker {
	if defaultTTL <= 0 {
		defaultTTL = 60 * time.Second
	}
	return &ApprovalBroker{
		approver:   approver,
		defaultTTL: defaultTTL,
		pending:    make(map[string]*pendingApproval),
	}
}

// Request issues an approval request and blocks until a decision is
// made, the deadline expires, or ctx is canceled.
//
// Flow:
//  1. Register the request in the pending map (visible to
//     Pending()/Resolve()/Cancel()).
//  2. If a synchronous Approver is configured, delegate to it — it
//     owns the blocking wait and returns the decision. The pending
//     entry is still removed on return.
//  3. Otherwise, wait on the reply channel, a deadline timer, or
//     ctx.Done — the first to fire wins.
//
// The returned response is never nil. On timeout/cancel a response
// with the corresponding decision is returned (error is ctx.Err()
// only on cancel, so callers can distinguish).
func (b *ApprovalBroker) Request(
	ctx context.Context,
	req *ApprovalRequest,
) (*ApprovalResponse, error) {
	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	if req.CreatedAt.IsZero() {
		req.CreatedAt = time.Now()
	}
	if req.Deadline.IsZero() {
		req.Deadline = req.CreatedAt.Add(b.defaultTTL)
	}

	reply := make(chan *ApprovalResponse, 1)
	b.register(req.ID, &pendingApproval{req: req, reply: reply})
	defer b.unregister(req.ID)

	// Synchronous consumer (TUI): it owns the blocking wait.
	if b.approver != nil {
		resp, err := b.approver.RequestApproval(ctx, req)
		if resp == nil {
			resp = &ApprovalResponse{
				RequestID: req.ID,
				Decision:  ApprovalDenied,
				Reason:    "approver returned no response",
				DecidedAt: time.Now(),
			}
		}
		if resp.RequestID == "" {
			resp.RequestID = req.ID
		}
		return resp, err
	}

	// External-resolver consumer (HTTP): wait for Resolve() or
	// deadline/ctx.
	timer := time.NewTimer(time.Until(req.Deadline))
	defer timer.Stop()

	select {
	case resp := <-reply:
		if resp == nil {
			resp = &ApprovalResponse{
				RequestID: req.ID,
				Decision:  ApprovalCanceled,
				DecidedAt: time.Now(),
			}
		}
		if resp.RequestID == "" {
			resp.RequestID = req.ID
		}
		return resp, nil
	case <-timer.C:
		return &ApprovalResponse{
			RequestID: req.ID,
			Decision:  ApprovalTimeout,
			Reason:    "approval deadline exceeded",
			DecidedAt: time.Now(),
		}, nil
	case <-ctx.Done():
		return &ApprovalResponse{
			RequestID: req.ID,
			Decision:  ApprovalCanceled,
			Reason:    ctx.Err().Error(),
			DecidedAt: time.Now(),
		}, ctx.Err()
	}
}

// Resolve delivers a decision for a pending request by ID. Returns
// (true, nil) when a pending request was matched and the producer
// unblocked; (false, nil) when no matching pending request exists
// (already resolved, timed out, or never issued).
//
// Safe for concurrent use by HTTP handlers.
func (b *ApprovalBroker) Resolve(
	reqID string, resp *ApprovalResponse,
) (bool, error) {
	if resp == nil {
		return false, fmt.Errorf("approval: nil response")
	}
	resp.RequestID = reqID
	if resp.DecidedAt.IsZero() {
		resp.DecidedAt = time.Now()
	}

	b.mu.Lock()
	p, ok := b.pending[reqID]
	if !ok {
		b.mu.Unlock()
		return false, nil
	}
	delete(b.pending, reqID)
	b.mu.Unlock()

	select {
	case p.reply <- resp:
	default:
		// Channel already drained (producer gave up); drop silently.
	}
	return true, nil
}

// Cancel marks a pending request as canceled (e.g. session closed)
// and unblocks the producer with a canceled decision. No-op (returns
// false) if the request is not pending.
func (b *ApprovalBroker) Cancel(reqID, reason string) bool {
	return b.resolveLocked(reqID, &ApprovalResponse{
		Decision:  ApprovalCanceled,
		Reason:    reason,
		DecidedAt: time.Now(),
	})
}

// Pending returns a snapshot of outstanding approval requests. Used
// by HTTP servers to list await-decision items and by TUI to render
// the approval queue.
func (b *ApprovalBroker) Pending() []*ApprovalRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*ApprovalRequest, 0, len(b.pending))
	for _, p := range b.pending {
		out = append(out, p.req)
	}
	return out
}

// GetPending returns a pending request by ID, or nil if absent.
func (b *ApprovalBroker) GetPending(reqID string) *ApprovalRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.pending[reqID]; ok {
		return p.req
	}
	return nil
}

func (b *ApprovalBroker) register(id string, p *pendingApproval) {
	b.mu.Lock()
	b.pending[id] = p
	b.mu.Unlock()
}

func (b *ApprovalBroker) unregister(id string) {
	b.mu.Lock()
	delete(b.pending, id)
	b.mu.Unlock()
}

func (b *ApprovalBroker) resolveLocked(
	reqID string, resp *ApprovalResponse,
) bool {
	b.mu.Lock()
	p, ok := b.pending[reqID]
	if !ok {
		b.mu.Unlock()
		return false
	}
	delete(b.pending, reqID)
	b.mu.Unlock()
	resp.RequestID = reqID
	select {
	case p.reply <- resp:
	default:
	}
	return true
}

// ---- Guard integration ----

// SetApprovalBroker wires an asynchronous approval sink. When set,
// NeedsApproval=true routes through the broker (human-in-the-loop)
// instead of an immediate deny. When nil, the legacy synchronous
// deny behavior is preserved. Safe to call before first use; not
// safe to call concurrently with RequestApproval.
func (g *Guard) SetApprovalBroker(b *ApprovalBroker) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.broker = b
}

// HasApprovalBroker reports whether an async approval sink is wired.
// The BeforeTool gate uses this to decide between the async path
// (broker present) and the legacy immediate-deny path.
func (g *Guard) HasApprovalBroker() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.broker != nil
}

// ApprovalTTL returns the configured default approval wait window,
// or 60s if no broker/override is set. Used to build request
// deadlines.
func (g *Guard) ApprovalTTL() time.Duration {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.broker != nil && g.broker.defaultTTL > 0 {
		return g.broker.defaultTTL
	}
	return 60 * time.Second
}

// RequestApproval asks the broker for a human decision on a tool
// call that NeedsApproval flagged. Blocks until the approver decides,
// the deadline expires, or ctx is canceled. The returned response
// is never nil.
//
// When no broker is configured, returns a denied response immediately
// (legacy synchronous-deny behavior) with reason set so callers can
// surface "requires approval" to the user.
func (g *Guard) RequestApproval(
	ctx context.Context,
	sessionID, userID, toolName string,
	args []byte,
) (*ApprovalResponse, error) {
	g.mu.RLock()
	broker := g.broker
	mode := g.cfg.PermissionMode
	g.mu.RUnlock()

	riskReason := g.approvalRiskReason(toolName, args)
	now := time.Now()
	req := &ApprovalRequest{
		ID:         uuid.New().String(),
		SessionID:  sessionID,
		UserID:     userID,
		ToolName:   toolName,
		Arguments:  args,
		RiskReason: riskReason,
		CreatedAt:  now,
		Deadline:   now.Add(g.ApprovalTTL()),
	}

	if broker == nil {
		// Legacy: no async sink → immediate deny. Callers translate
		// this into the same "requires user approval" error as
		// before, so the offline/TUI-less path is unchanged.
		return &ApprovalResponse{
			RequestID: req.ID,
			Decision:  ApprovalDenied,
			Reason: fmt.Sprintf(
				"tool %q requires user approval in %s mode "+
					"(no approval broker configured)",
				toolName, mode),
			DecidedAt: now,
		}, nil
	}

	return broker.Request(ctx, req)
}

// approvalRiskReason produces a human-readable explanation of why a
// tool call was flagged for approval, mirroring the isHighRiskOperation
// / isDangerousCommand classification. Drives the RiskReason field
// on ApprovalRequest so consumers (TUI/HTTP) can show context.
func (g *Guard) approvalRiskReason(toolName string, args []byte) string {
	g.mu.RLock()
	mode := g.cfg.PermissionMode
	g.mu.RUnlock()

	switch mode {
	case config.PermissionManual:
		return "manual mode: all tool calls require approval"
	case config.PermissionChatOnly:
		return "chat_only mode: tool calls blocked"
	}

	// smart / default: attribute to risk classification.
	if isCommandTool(toolName) && len(args) > 0 {
		cmd := extractCommandFromArgs(args)
		if cmd != "" && isDangerousCommand(cmd) {
			return "dangerous command pattern detected"
		}
	}
	if g.isHighRiskOperation(toolName, args) {
		return "high-risk tool call"
	}
	return "approval required by policy"
}

// ---- context plumbing ----

// approvalCtxKey carries session/user identity into the framework's
// BeforeTool callback so the security gate can build an
// ApprovalRequest bound to the running turn. The agent loop injects
// this value into the ctx passed to runner.Run.
type approvalCtxKey struct{}

type approvalCtxValue struct {
	sessionID string
	userID    string
}

// WithApprovalContext returns a ctx carrying session/user identity
// for the approval gate. Inject at the agent loop boundary, just
// before runner.Run.
func WithApprovalContext(
	ctx context.Context, sessionID, userID string,
) context.Context {
	return context.WithValue(ctx, approvalCtxKey{}, approvalCtxValue{
		sessionID: sessionID,
		userID:    userID,
	})
}

// ApprovalContextFrom extracts session/user identity from ctx. Used
// by the BeforeTool callback to attribute approval requests. Returns
// ("","") if absent — approval still works, just unattributed.
func ApprovalContextFrom(ctx context.Context) (sessionID, userID string) {
	v, _ := ctx.Value(approvalCtxKey{}).(approvalCtxValue)
	return v.sessionID, v.userID
}
