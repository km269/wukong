// Package summon provides ANP-06 meta-protocol implementation for
// dynamic capability exchange and protocol negotiation between agents.
//
// The meta-protocol enables Wukong agents to:
//   - Discover remote agent capabilities (get_capabilities)
//   - Negotiate optimal protocol/security configuration (negotiate)
//   - Establish mutually compatible communication sessions
//
// This is the key architectural improvement over the current
// fixed-protocol approach where both agents assume identical
// protocol stack support.
package summon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/km269/wukong/internal/ard"
	"github.com/km269/wukong/internal/util"
)

// ============================================================================
// MetaProtocol — Core Negotiation Engine
// ============================================================================

// MetaProtocol implements ANP-06 agent communication meta-protocol.
// It enables agents to negotiate protocol versions, capabilities,
// and security profiles before establishing a communication session.
//
// The negotiation flow:
//   1. Client calls GetCapabilities(remoteURL) to discover remote agent
//   2. Client selects best interface + security scheme
//   3. Client calls Negotiate(remoteURL, params) to propose configuration
//   4. Server accepts or rejects the proposal
//   5. If accepted, a session is established with negotiated parameters
type MetaProtocol struct {
	mu sync.RWMutex

	// Local agent's capabilities and identity
	localDID            string
	localBaseURL        string
	supportedInterfaces []ard.AgentInterface
	supportedProfiles   []string
	securityDefs        map[string]*ard.SecurityDefinition
	preferredSecurity   string

	// Active negotiation sessions
	sessions map[string]*NegotiationSession

	// HTTP client for remote capability queries
	httpClient *http.Client
}

// NegotiationSession tracks an established protocol negotiation session.
type NegotiationSession struct {
	SessionID              string
	RemoteDID              string
	RemoteURL              string
	SelectedInterface      *ard.AgentInterface
	SelectedMessageProfile string
	SecurityContext        *ard.SecurityContext
	CreatedAt              time.Time
	ExpiresAt              time.Time
}

// MetaProtocolConfig configures the meta-protocol engine.
type MetaProtocolConfig struct {
	// LocalDID is the W3C DID identifier for the local agent.
	LocalDID string

	// LocalBaseURL is the local agent's base URL for endpoint
	// construction.
	LocalBaseURL string

	// SupportedInterfaces lists all communication interfaces
	// the local agent supports (A2A, MCP, AG-UI, etc.).
	SupportedInterfaces []ard.AgentInterface

	// SupportedProfiles lists supported ANP message profiles
	// (P1-P9). Default: ["P1", "P3"].
	SupportedProfiles []string

	// SecurityDefinitions declares authentication schemes.
	SecurityDefinitions map[string]*ard.SecurityDefinition

	// PreferredSecurity is the default security scheme name.
	// Default: "didwba_sc".
	PreferredSecurity string

	// HTTPTimeout is the timeout for capability queries.
	// Default: 10s.
	HTTPTimeout time.Duration
}

// DefaultMetaProtocolConfig returns sensible defaults.
func DefaultMetaProtocolConfig() *MetaProtocolConfig {
	return &MetaProtocolConfig{
		SupportedProfiles: []string{"P1", "P3"},
		PreferredSecurity: "didwba_sc",
		HTTPTimeout:       10 * time.Second,
	}
}

// NewMetaProtocol creates a new meta-protocol engine.
func NewMetaProtocol(cfg *MetaProtocolConfig) *MetaProtocol {
	if cfg == nil {
		cfg = DefaultMetaProtocolConfig()
	}
	if len(cfg.SupportedProfiles) == 0 {
		cfg.SupportedProfiles = []string{"P1", "P3"}
	}
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}

	return &MetaProtocol{
		localDID:            cfg.LocalDID,
		localBaseURL:        cfg.LocalBaseURL,
		supportedInterfaces: cfg.SupportedInterfaces,
		supportedProfiles:   cfg.SupportedProfiles,
		securityDefs:        cfg.SecurityDefinitions,
		preferredSecurity:   cfg.PreferredSecurity,
		sessions:            make(map[string]*NegotiationSession),
		httpClient: &http.Client{
			Timeout: cfg.HTTPTimeout,
		},
	}
}

// ============================================================================
// Client-Side: Remote Capability Discovery
// ============================================================================

// GetCapabilities queries a remote agent's ANP capabilities.
// It fetches the remote agent's capability document via its
// well-known ADP endpoint, without establishing a session.
func (m *MetaProtocol) GetCapabilities(
	ctx context.Context, remoteURL string,
) (*ard.CapabilitiesResult, error) {
	remoteURL = strings.TrimSuffix(remoteURL, "/")

	// Try ANP discovery endpoint first
	adpURL := remoteURL + ard.ANPWellKnownPath

	result, err := m.fetchCapabilities(ctx, adpURL)
	if err != nil {
		// Fall back to direct ADP document
		adpURL = remoteURL + "/agents/default/ad.json"
		result, err = m.fetchCapabilities(ctx, adpURL)
		if err != nil {
			return nil, fmt.Errorf(
				"meta-protocol: fetch capabilities: %w", err)
		}
	}

	return result, nil
}

// fetchCapabilities fetches and parses a remote ADP document to
// extract capability information.
func (m *MetaProtocol) fetchCapabilities(
	ctx context.Context, url string,
) (*ard.CapabilitiesResult, error) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, url, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"unexpected status: %d", resp.StatusCode)
	}

	var doc ard.ADPDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse ADP: %w", err)
	}

	// Convert ADP document to capabilities result
	return &ard.CapabilitiesResult{
		Interfaces:           doc.Interfaces,
		MessageProfiles:      []string{"P1", "P3"},
		SecurityDefinitions:  doc.SecurityDefinitions,
		ProtocolVersion:      doc.ProtocolVersion,
		AgentDID:             doc.DID,
	}, nil
}

// ============================================================================
// Client-Side: Protocol Negotiation
// ============================================================================

// NegotiateRequest is the client's negotiation proposal.
type NegotiateRequest struct {
	// RemoteURL is the remote agent's base URL.
	RemoteURL string

	// InterfaceIndex selects an interface from the remote agent's
	// capability list (0-based). Use -1 to auto-select.
	InterfaceIndex int

	// PreferredProfile selects a message profile (e.g., "P3").
	// Auto-selected if empty.
	PreferredProfile string

	// PreferredSecurity selects a security scheme by name.
	// Auto-selected if empty.
	PreferredSecurity string
}

// Negotiate performs protocol negotiation with a remote agent.
// It first discovers the remote's capabilities, then proposes
// an optimal protocol configuration.
func (m *MetaProtocol) Negotiate(
	ctx context.Context, req *NegotiateRequest,
) (*NegotiationSession, error) {
	// Phase 1: Discover remote capabilities
	remoteCaps, err := m.GetCapabilities(ctx, req.RemoteURL)
	if err != nil {
		return nil, fmt.Errorf(
			"negotiate: discover capabilities: %w", err)
	}

	// Phase 2: Select optimal configuration
	selection := m.selectConfiguration(remoteCaps, req)

	if selection.InterfaceIndex < 0 {
		return nil, fmt.Errorf(
			"negotiate: no compatible interface found")
	}

	// Phase 3: Send negotiation proposal via JSON-RPC
	negotiateParams := &ard.NegotiateParams{
		Interface:     selection.InterfaceIndex,
		MessageProfile: selection.MessageProfile,
		SecurityScheme: selection.SecurityScheme,
	}

	response, err := m.sendNegotiation(
		ctx, req.RemoteURL, negotiateParams)
	if err != nil {
		return nil, fmt.Errorf(
			"negotiate: send proposal: %w", err)
	}

	if response == nil || !response.Accepted {
		reason := "rejected"
		if response != nil {
			reason = response.Reason
		}
		return nil, fmt.Errorf("negotiate: proposal %s", reason)
	}

	// Phase 4: Create session
	session := &NegotiationSession{
		SessionID:              response.SessionID,
		RemoteDID:              remoteCaps.AgentDID,
		RemoteURL:              req.RemoteURL,
		SelectedInterface:      response.SelectedInterface,
		SelectedMessageProfile: response.SelectedMessageProfile,
		SecurityContext:        response.SecurityContext,
		CreatedAt:              time.Now(),
	}

	if response.SecurityContext != nil &&
		response.SecurityContext.ExpiresAt != "" {
		session.ExpiresAt, _ = time.Parse(
			time.RFC3339, response.SecurityContext.ExpiresAt)
	}

	// Store session
	m.mu.Lock()
	m.sessions[session.SessionID] = session
	m.mu.Unlock()

	util.Logger.Info("Meta-protocol: negotiation succeeded",
		slog.String("session_id", session.SessionID),
		slog.String("remote_did", session.RemoteDID),
		slog.String("interface",
			session.SelectedInterface.Protocol),
	)

	return session, nil
}

// selectionResult holds the outcome of capability matching.
type selectionResult struct {
	InterfaceIndex int
	MessageProfile string
	SecurityScheme string
}

// selectConfiguration picks the best mutually compatible
// protocol stack from local and remote capabilities.
func (m *MetaProtocol) selectConfiguration(
	remote *ard.CapabilitiesResult,
	req *NegotiateRequest,
) *selectionResult {
	result := &selectionResult{InterfaceIndex: -1}

	// Select interface: prefer MCP, then A2A, then natural language
	preferOrder := []string{"MCP", "A2A", "YAML"}

	if req.InterfaceIndex >= 0 &&
		req.InterfaceIndex < len(remote.Interfaces) {
		result.InterfaceIndex = req.InterfaceIndex
	} else {
		for _, preferred := range preferOrder {
			for i, iface := range remote.Interfaces {
				if iface.Protocol == preferred {
					result.InterfaceIndex = i
					goto foundIface
				}
			}
		}
	foundIface:
	}

	// Select message profile: use requested, then P3, then P1
	result.MessageProfile = req.PreferredProfile
	if result.MessageProfile == "" {
		for _, p := range []string{"P3", "P1"} {
			for _, rp := range remote.MessageProfiles {
				if p == rp {
					result.MessageProfile = p
					goto foundProfile
				}
			}
		}
	foundProfile:
	}

	// Select security: use requested, then preferred, then
	// first available
	result.SecurityScheme = req.PreferredSecurity
	if result.SecurityScheme == "" {
		result.SecurityScheme = m.preferredSecurity
	}
	if _, ok := remote.SecurityDefinitions[result.SecurityScheme]; !ok {
		for key := range remote.SecurityDefinitions {
			result.SecurityScheme = key
			break
		}
	}

	return result
}

// sendNegotiation sends a JSON-RPC negotiation proposal to
// the remote agent.
func (m *MetaProtocol) sendNegotiation(
	ctx context.Context,
	remoteURL string,
	params *ard.NegotiateParams,
) (*ard.NegotiateResult, error) {
	rpcReq := ard.NegotiateRequest{
		JSONRPC: "2.0",
		Method:  "anp.negotiate",
		ID:      1,
		Params:  params,
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimSuffix(remoteURL, "/") +
		"/anp/meta-protocol"

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, url,
		strings.NewReader(string(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"unexpected status: %d", resp.StatusCode)
	}

	var rpcResp ard.NegotiateResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf(
			"negotiation error: %s", rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// ============================================================================
// Server-Side: HTTP Handler for Meta-Protocol Endpoint
// ============================================================================

// MetaProtocolHandler serves the ANP meta-protocol endpoint,
// processing incoming get_capabilities and negotiate requests
// from remote agents.
type MetaProtocolHandler struct {
	meta *MetaProtocol
}

// NewMetaProtocolHandler creates a server-side meta-protocol handler.
func NewMetaProtocolHandler(meta *MetaProtocol) *MetaProtocolHandler {
	return &MetaProtocolHandler{meta: meta}
}

// ServeHTTP handles incoming meta-protocol JSON-RPC requests.
// Routes:
//   POST /anp/meta-protocol (body: JSON-RPC 2.0)
//   GET  /anp/capabilities     (returns local capabilities)
func (h *MetaProtocolHandler) ServeHTTP(
	w http.ResponseWriter, r *http.Request,
) {
	switch r.Method {
	case http.MethodGet:
		h.handleGetCapabilities(w, r)
	case http.MethodPost:
		h.handlePost(w, r)
	default:
		http.Error(
			w, "Method not allowed",
			http.StatusMethodNotAllowed,
		)
	}
}

// handleGetCapabilities serves GET /anp/capabilities —
// returns the local agent's capability set.
func (h *MetaProtocolHandler) handleGetCapabilities(
	w http.ResponseWriter, r *http.Request,
) {
	result := ard.CapabilitiesResult{
		Interfaces:           h.meta.supportedInterfaces,
		MessageProfiles:      h.meta.supportedProfiles,
		SecurityDefinitions:  h.meta.securityDefs,
		ProtocolVersion:      ard.ANPProtocolVersion,
		AgentDID:             h.meta.localDID,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handlePost processes JSON-RPC 2.0 requests for negotiation.
func (h *MetaProtocolHandler) handlePost(
	w http.ResponseWriter, r *http.Request,
) {
	var baseReq struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		ID      int             `json:"id"`
		Params  json.RawMessage `json:"params,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&baseReq); err != nil {
		h.writeError(w, 0, -32700, "parse error: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch baseReq.Method {
	case "anp.get_capabilities":
		h.handleJSONRPCGetCapabilities(w, baseReq.ID)
	case "anp.negotiate":
		h.handleJSONRPCNegotiate(w, r, baseReq.ID, baseReq.Params)
	default:
		h.writeError(
			w, baseReq.ID, -32601,
			"method not found: "+baseReq.Method,
		)
	}
}

// handleJSONRPCGetCapabilities returns local capabilities as a
// JSON-RPC response.
func (h *MetaProtocolHandler) handleJSONRPCGetCapabilities(
	w http.ResponseWriter, id int,
) {
	result := ard.CapabilitiesResult{
		Interfaces:           h.meta.supportedInterfaces,
		MessageProfiles:      h.meta.supportedProfiles,
		SecurityDefinitions:  h.meta.securityDefs,
		ProtocolVersion:      ard.ANPProtocolVersion,
		AgentDID:             h.meta.localDID,
	}

	resp := ard.CapabilitiesResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  &result,
	}

	json.NewEncoder(w).Encode(resp)
}

// handleJSONRPCNegotiate processes an incoming negotiation proposal.
func (h *MetaProtocolHandler) handleJSONRPCNegotiate(
	w http.ResponseWriter,
	r *http.Request,
	id int,
	rawParams json.RawMessage,
) {
	var params ard.NegotiateParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		h.writeError(
			w, id, -32602,
			"invalid params: "+err.Error(),
		)
		return
	}

	// Validate the proposal against local capabilities
	if params.Interface < 0 ||
		params.Interface >= len(h.meta.supportedInterfaces) {
		h.writeNegotiateResult(w, id, false,
			"interface index out of range", nil)
		return
	}

	// Accept the negotiation and generate a session
	sessionID := uuid.New().String()
	selectedIface := h.meta.supportedInterfaces[params.Interface]

	negotiateResult := &ard.NegotiateResult{
		Accepted:               true,
		SessionID:              sessionID,
		SelectedInterface:      &selectedIface,
		SelectedMessageProfile: params.MessageProfile,
		SecurityContext: &ard.SecurityContext{
			Scheme:    params.SecurityScheme,
			ExpiresAt: time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339),
		},
	}

	// Store session
	h.meta.mu.Lock()
	h.meta.sessions[sessionID] = &NegotiationSession{
		SessionID:              sessionID,
		RemoteURL:              r.RemoteAddr,
		SelectedInterface:      &selectedIface,
		SelectedMessageProfile: params.MessageProfile,
		SecurityContext:        negotiateResult.SecurityContext,
		CreatedAt:              time.Now(),
		ExpiresAt:              time.Now().Add(1 * time.Hour),
	}
	h.meta.mu.Unlock()

	util.Logger.Info("Meta-protocol: negotiation accepted",
		slog.String("session_id", sessionID),
		slog.String("from", r.RemoteAddr),
		slog.String("interface", selectedIface.Protocol),
	)

	h.writeNegotiateResult(w, id, true, "", negotiateResult)
}

// ============================================================================
// Session Management
// ============================================================================

// GetSession retrieves an active negotiation session by ID.
func (m *MetaProtocol) GetSession(
	sessionID string,
) (*NegotiationSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[sessionID]
	return session, ok
}

// RemoveSession removes and cleans up a negotiation session.
func (m *MetaProtocol) RemoveSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
}

// CleanExpiredSessions removes all expired sessions.
// Should be called periodically (e.g., every 5 minutes).
func (m *MetaProtocol) CleanExpiredSessions() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, session := range m.sessions {
		if !session.ExpiresAt.IsZero() &&
			now.After(session.ExpiresAt) {
			delete(m.sessions, id)
		}
	}
}

// ============================================================================
// Builder: Construct MetaProtocolConfig from Wukong config
// ============================================================================

// BuildMetaProtocolConfig creates a MetaProtocolConfig from
// Wukong's service endpoint configuration. This bridges the
// existing config format to the ANP meta-protocol configuration.
func BuildMetaProtocolConfig(
	localDID string,
	baseURL string,
	a2aEnabled bool, a2aPort int,
	acpEnabled bool, acpPort int,
	aguiEnabled bool, aguiPort int,
	securityMode string,
) *MetaProtocolConfig {
	cfg := DefaultMetaProtocolConfig()
	cfg.LocalDID = localDID
	cfg.LocalBaseURL = baseURL
	cfg.PreferredSecurity = securityMode

	// Build interface list from enabled services
	if a2aEnabled && a2aPort > 0 {
		cfg.SupportedInterfaces = append(
			cfg.SupportedInterfaces,
			ard.AgentInterface{
				Type:        "StructuredInterface",
				Protocol:    "A2A",
				URL:         fmt.Sprintf("%s:%d", baseURL, a2aPort),
				Version:     "1.0",
				Description: "Agent-to-Agent task delegation via Google A2A protocol",
			},
		)
	}

	if acpEnabled && acpPort > 0 {
		cfg.SupportedInterfaces = append(
			cfg.SupportedInterfaces,
			ard.AgentInterface{
				Type:        "StructuredInterface",
				Protocol:    "MCP",
				URL:         fmt.Sprintf("%s:%d/mcp", baseURL, acpPort),
				Version:     "2024-11-05",
				Description: "MCP server exposing agent tools",
			},
		)
	}

	if aguiEnabled && aguiPort > 0 {
		cfg.SupportedInterfaces = append(
			cfg.SupportedInterfaces,
			ard.AgentInterface{
				Type:        "NaturalLanguageInterface",
				Protocol:    "YAML",
				URL:         fmt.Sprintf("%s:%d/agui", baseURL, aguiPort),
				Version:     "1.0",
				Description: "SSE streaming chat interface",
			},
		)
	}

	// Always include natural language as fallback
	cfg.SupportedInterfaces = append(
		cfg.SupportedInterfaces,
		ard.AgentInterface{
			Type:        "NaturalLanguageInterface",
			Protocol:    "YAML",
			URL:         baseURL,
			Version:     "1.0",
			Description: "Primary natural language interface",
		},
	)

	// Build security definitions
	cfg.SecurityDefinitions = map[string]*ard.SecurityDefinition{
		"didwba_sc": {
			Type:        "didwba_sc",
			In:          "header",
			Name:        "Signature",
			Description: "Self-certifying via HTTP Message Signatures (RFC 9421)",
		},
		"api_key": {
			Type:        "apiKey",
			In:          "header",
			Name:        "X-API-Key",
			Description: "API key authentication",
		},
		"jwt": {
			Type:        "http",
			Scheme:      "bearer",
			In:          "header",
			Name:        "Authorization",
			Description: "JWT Bearer token authentication",
		},
	}

	return cfg
}

// ============================================================================
// Response Helpers
// ============================================================================

func (h *MetaProtocolHandler) writeError(
	w http.ResponseWriter, id int,
	code int, message string,
) {
	resp := ard.NegotiateResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &ard.ANPError{
			Code:    code,
			Message: message,
		},
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *MetaProtocolHandler) writeNegotiateResult(
	w http.ResponseWriter, id int,
	accepted bool, reason string,
	result *ard.NegotiateResult,
) {
	resp := ard.NegotiateResponse{
		JSONRPC: "2.0",
		ID:      id,
	}
	if accepted && result != nil {
		resp.Result = result
	} else {
		resp.Result = &ard.NegotiateResult{
			Accepted: false,
			Reason:   reason,
		}
	}
	json.NewEncoder(w).Encode(resp)
}
