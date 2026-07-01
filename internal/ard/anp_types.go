// Package ard provides ANP (Agent Network Protocol) type definitions
// for unified agent description, discovery, and meta-protocol support.
//
// These types implement the ANP specification to enable Wukong agents
// to interoperate with ANP-compatible agent ecosystems. Key components:
//   - ADP (ANP-07): Unified Agent Description Protocol document
//   - CollectionPage: ANP-compatible agent listing for discovery
//   - Interface types: Natural language and structured interfaces
//   - Security definitions: Authentication scheme declarations
package ard

import "encoding/json"

// ============================================================================
// ANP Protocol Constants
// ============================================================================

const (
	// ANPProtocolType is the protocol identifier for ANP-compatible documents.
	ANPProtocolType = "ANP"

	// ANPProtocolVersion is the current ANP protocol version supported.
	ANPProtocolVersion = "1.0.0"

	// ANPWellKnownPath is the standard ANP discovery endpoint path.
	ANPWellKnownPath = "/.well-known/agent-descriptions"

	// ANPADPPathSuffix is the standard path suffix per-agent ADP documents.
	ANPADPPathSuffix = "/ad.json"
)

// ============================================================================
// ADP (Agent Description Protocol) — ANP-07 Compatible
// ============================================================================

// ADPDocument is the root document for ANP-07 Agent Description Protocol.
// It provides a unified, machine-readable description of an agent's
// capabilities, interfaces, and security requirements.
//
// The ADP document is hosted at a well-known URL and can be cryptographically
// signed via the Proof field to prevent tampering.
type ADPDocument struct {
	// ProtocolType identifies the protocol family — always "ANP".
	ProtocolType string `json:"protocolType"`

	// ProtocolVersion is the ANP protocol version.
	ProtocolVersion string `json:"protocolVersion"`

	// Type is the document type — always "AgentDescription".
	Type string `json:"type"`

	// URL is the canonical URL where this description is hosted.
	URL string `json:"url"`

	// Name is the human-readable display name of the agent.
	Name string `json:"name"`

	// DID is the W3C Decentralized Identifier for this agent.
	// Format: did:wba:<domain>:<path>:e1_<fingerprint>
	DID string `json:"did"`

	// Owner contains information about the agent's owning organization.
	Owner *Owner `json:"owner,omitempty"`

	// Description is a human-readable description of the agent's purpose.
	Description string `json:"description"`

	// Interfaces lists the communication interfaces this agent supports.
	// Each interface declares its type (NaturalLanguage or Structured)
	// and the specific protocol used.
	Interfaces []AgentInterface `json:"interfaces"`

	// SecurityDefinitions declares available authentication mechanisms.
	// Keys are security scheme names, values describe the scheme.
	SecurityDefinitions map[string]*SecurityDefinition `json:"securityDefinitions,omitempty"`

	// Security selects the active security scheme by name reference.
	// Must match a key in SecurityDefinitions.
	Security string `json:"security,omitempty"`

	// Infomations describes the knowledge and information resources
	// the agent can provide, using Schema.org vocabulary.
	Infomations []Information `json:"Infomations,omitempty"`

	// Tags are searchable labels for categorizing the agent.
	Tags []string `json:"tags,omitempty"`

	// Capabilities lists the tool and skill identifiers this agent exposes.
	Capabilities []string `json:"capabilities,omitempty"`

	// Version is the agent software version.
	Version string `json:"version,omitempty"`

	// Proof is a cryptographic proof of the document's integrity.
	// Uses DataIntegrityProof with eddsa-jcs-2022 cryptosuite.
	Proof *DataIntegrityProof `json:"proof,omitempty"`
}

// Owner describes the organization that owns or operates an agent.
type Owner struct {
	// Name is the organization display name.
	Name string `json:"name"`

	// URL is the organization's website.
	URL string `json:"url"`

	// DID is the owner's W3C Decentralized Identifier.
	DID string `json:"did,omitempty"`
}

// AgentInterface describes a single communication interface supported
// by an agent. An agent may expose multiple interfaces simultaneously
// (e.g., natural language chat + structured MCP tools).
type AgentInterface struct {
	// Type is the interface category: "NaturalLanguageInterface" or
	// "StructuredInterface".
	Type string `json:"type"`

	// Protocol is the specific protocol: "YAML", "openrpc", "MCP",
	// "A2A", "ACP", "WebRTC".
	Protocol string `json:"protocol"`

	// URL is the endpoint URL for this interface.
	URL string `json:"url"`

	// Version is the interface protocol version.
	Version string `json:"version,omitempty"`

	// Description explains the interface's role and usage.
	Description string `json:"description,omitempty"`

	// Methods lists the available methods for structured interfaces.
	// Each entry maps a method name to its OpenRPC-like schema.
	Methods map[string]InterfaceMethod `json:"methods,omitempty"`
}

// InterfaceMethod describes a method available on a structured agent interface.
type InterfaceMethod struct {
	// Description explains what the method does.
	Description string `json:"description"`

	// Params describes the input parameters schema.
	Params json.RawMessage `json:"params,omitempty"`

	// Result describes the output result schema.
	Result json.RawMessage `json:"result,omitempty"`
}

// SecurityDefinition describes an authentication mechanism available
// for communicating with this agent.
type SecurityDefinition struct {
	// Type is the security scheme type:
	// "didwba_sc" — DID:wba self-certifying (HTTP Message Signatures)
	// "http" — HTTP Bearer/JWT token
	// "apiKey" — API key in header
	// "oauth2" — OAuth 2.0 flow
	Type string `json:"type"`

	// In specifies where the credential is placed: "header", "query".
	In string `json:"in,omitempty"`

	// Name is the header or query parameter name for the credential.
	Name string `json:"name,omitempty"`

	// Scheme is the HTTP authentication scheme (e.g., "bearer").
	Scheme string `json:"scheme,omitempty"`

	// Flows describes OAuth2 authorization flows.
	Flows map[string]any `json:"flows,omitempty"`

	// Description provides human-readable details about the scheme.
	Description string `json:"description,omitempty"`
}

// Information describes a knowledge or information resource the agent
// provides, using Schema.org semantic vocabulary.
type Information struct {
	// Type is the Schema.org type (e.g., "Product", "Service", "Dataset").
	Type string `json:"type"`

	// Description explains what information the agent can provide.
	Description string `json:"description"`

	// URL is the canonical URL for this information resource.
	URL string `json:"url,omitempty"`

	// Subject is the topic or domain of this information.
	Subject string `json:"subject,omitempty"`
}

// ============================================================================
// ANP Discovery — CollectionPage
// ============================================================================

// CollectionPage represents a paginated list of agent descriptions,
// compliant with ANP discovery protocol and W3C Activity Streams 2.0.
// Served at /.well-known/agent-descriptions for active discovery.
type CollectionPage struct {
	// Context defines the JSON-LD context for semantic interpretation.
	Context []string `json:"@context"`

	// Type is "CollectionPage" per Activity Streams.
	Type string `json:"type"`

	// URL is the canonical URL of this collection page.
	URL string `json:"url"`

	// Items lists the agent entries on this page.
	Items []CollectionItem `json:"items"`

	// Next is the URL of the next page, or empty if this is the last page.
	Next string `json:"next,omitempty"`

	// Prev is the URL of the previous page, or empty if this is the first.
	Prev string `json:"prev,omitempty"`

	// TotalItems is the total count of items across all pages.
	TotalItems int `json:"totalItems,omitempty"`
}

// CollectionItem represents a single agent entry in a CollectionPage.
type CollectionItem struct {
	// Type is "ad:AgentDescription" for ANP agent entries.
	Type string `json:"type"`

	// Name is the agent's display name.
	Name string `json:"name"`

	// ID is the URL to the agent's full ADP document.
	ID string `json:"id"`

	// DID is the agent's W3C Decentralized Identifier.
	DID string `json:"did,omitempty"`

	// Summary is a brief description of the agent's capabilities.
	Summary string `json:"summary,omitempty"`
}

// ============================================================================
// Cryptographic Proof Types
// ============================================================================

// DataIntegrityProof is a W3C Data Integrity proof attached to
// an ADP document to verify authenticity and integrity.
// Uses eddsa-jcs-2022 cryptosuite with Ed25519 keys.
type DataIntegrityProof struct {
	// Type is "DataIntegrityProof".
	Type string `json:"type"`

	// Cryptosuite identifies the cryptographic suite: "eddsa-jcs-2022".
	Cryptosuite string `json:"cryptosuite"`

	// Created is the ISO 8601 timestamp of proof creation.
	Created string `json:"created"`

	// VerificationMethod is the DID URL of the key used to verify.
	// e.g., did:wba:example.com:e1_<fp>#key-1
	VerificationMethod string `json:"verificationMethod"`

	// ProofPurpose declares the purpose: "assertionMethod".
	ProofPurpose string `json:"proofPurpose"`

	// ProofValue is the base64-encoded Ed25519 signature.
	ProofValue string `json:"proofValue"`
}

// ============================================================================
// ANP Meta-Protocol Types (ANP-06)
// ============================================================================

// CapabilitiesRequest is sent during the get_capabilities phase
// of ANP meta-protocol negotiation. It queries the remote agent's
// supported protocols, message profiles, and security capabilities.
type CapabilitiesRequest struct {
	// JSONRPC is always "2.0" per JSON-RPC specification.
	JSONRPC string `json:"jsonrpc"`

	// Method is "anp.get_capabilities" for capability discovery.
	Method string `json:"method"`

	// ID is the request identifier for matching responses.
	ID int `json:"id"`
}

// CapabilitiesResponse describes the capabilities a remote agent
// supports, returned in response to get_capabilities.
type CapabilitiesResponse struct {
	// JSONRPC is always "2.0".
	JSONRPC string `json:"jsonrpc"`

	// ID matches the request ID.
	ID int `json:"id"`

	// Result contains the capabilities data.
	Result *CapabilitiesResult `json:"result,omitempty"`

	// Error contains error information if the request failed.
	Error *ANPError `json:"error,omitempty"`
}

// CapabilitiesResult holds the actual capability information.
type CapabilitiesResult struct {
	// Interfaces lists supported communication interfaces with their
	// protocols and endpoint URLs.
	Interfaces []AgentInterface `json:"interfaces"`

	// MessageProfiles lists supported ANP message profiles (P1-P9).
	MessageProfiles []string `json:"messageProfiles,omitempty"`

	// SecurityDefinitions declares available authentication schemes.
	SecurityDefinitions map[string]*SecurityDefinition `json:"securityDefinitions,omitempty"`

	// ProtocolVersion is the ANP protocol version the agent supports.
	ProtocolVersion string `json:"protocolVersion"`

	// AgentDID is the agent's decentralized identifier.
	AgentDID string `json:"agentDID,omitempty"`
}

// NegotiateRequest is sent during the negotiation phase to select
// a specific protocol, profile, and security configuration.
type NegotiateRequest struct {
	// JSONRPC is always "2.0".
	JSONRPC string `json:"jsonrpc"`

	// Method is "anp.negotiate" for protocol negotiation.
	Method string `json:"method"`

	// ID is the request identifier.
	ID int `json:"id"`

	// Params contains the negotiation parameters.
	Params *NegotiateParams `json:"params"`
}

// NegotiateParams specifies the desired protocol configuration.
type NegotiateParams struct {
	// Interface is the index of the selected interface from
	// CapabilitiesResult.Interfaces (0-based).
	Interface int `json:"interface"`

	// MessageProfile selects a specific message profile (e.g., "P3").
	MessageProfile string `json:"messageProfile,omitempty"`

	// SecurityScheme selects the security definition key to use.
	SecurityScheme string `json:"securityScheme,omitempty"`
}

// NegotiateResponse contains the negotiation result.
type NegotiateResponse struct {
	// JSONRPC is always "2.0".
	JSONRPC string `json:"jsonrpc"`

	// ID matches the request ID.
	ID int `json:"id"`

	// Result confirms the selected configuration.
	Result *NegotiateResult `json:"result,omitempty"`

	// Error contains error information if negotiation failed.
	Error *ANPError `json:"error,omitempty"`
}

// NegotiateResult confirms the negotiated protocol stack.
type NegotiateResult struct {
	// Accepted is true if the negotiation was successful.
	Accepted bool `json:"accepted"`

	// SessionID is a unique identifier for the established session.
	SessionID string `json:"sessionId,omitempty"`

	// SelectedInterface confirms the chosen interface configuration.
	SelectedInterface *AgentInterface `json:"selectedInterface,omitempty"`

	// SelectedMessageProfile confirms the chosen message profile.
	SelectedMessageProfile string `json:"selectedMessageProfile,omitempty"`

	// SecurityContext contains session-specific security parameters.
	SecurityContext *SecurityContext `json:"securityContext,omitempty"`

	// Reason provides explanation if negotiation was rejected.
	Reason string `json:"reason,omitempty"`
}

// SecurityContext holds session-level security configuration.
type SecurityContext struct {
	// Scheme is the security scheme in use for this session.
	Scheme string `json:"scheme"`

	// SessionKey is a derived symmetric key for E2EE (base64).
	SessionKey string `json:"sessionKey,omitempty"`

	// ExpiresAt is the ISO 8601 timestamp when the session expires.
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// ANPError represents a JSON-RPC error response.
type ANPError struct {
	// Code is the error code per JSON-RPC 2.0.
	Code int `json:"code"`

	// Message is a human-readable error description.
	Message string `json:"message"`

	// Data contains additional error details.
	Data any `json:"data,omitempty"`
}

// ============================================================================
// ADP Builder Configuration
// ============================================================================

// ADPBuildConfig configures the ADP document builder with
// the agent's identity, capabilities, and security settings.
type ADPBuildConfig struct {
	// AgentName is the human-readable display name.
	AgentName string

	// AgentDescription describes the agent's purpose.
	AgentDescription string

	// AgentVersion is the agent software version string.
	AgentVersion string

	// BaseURL is the root URL where the agent is hosted
	// (e.g., "https://wukong.example.com").
	BaseURL string

	// DID is the agent's W3C Decentralized Identifier.
	DID string

	// Organization is the owning organization name.
	Organization string

	// OrganizationURL is the organization's website URL.
	OrganizationURL string

	// OwnerDID is the owner's W3C Decentralized Identifier.
	OwnerDID string

	// A2APort is the A2A server port (0 if disabled).
	A2APort int

	// ACPPort is the ACP server port (0 if disabled).
	ACPPort int

	// AGUIPort is the AG-UI server port (0 if disabled).
	AGUIPort int

	// A2AEnabled enables A2A interface in the ADP document.
	A2AEnabled bool

	// ACPEnabled enables ACP interface in the ADP document.
	ACPEnabled bool

	// AGUIEnabled enables AG-UI interface in the ADP document.
	AGUIEnabled bool

	// SecurityMode selects the primary security scheme:
	// "didwba_sc", "api_key", "jwt", or "" (none).
	SecurityMode string

	// APIKeyHeader is the header name for API key auth.
	APIKeyHeader string

	// Capabilities lists the agent's tool/skill capabilities.
	Capabilities []string

	// Tags are searchable tags for discovery.
	Tags []string

	// InfoTypes lists the information resource types this agent
	// provides (e.g., "Product", "Dataset").
	InfoTypes []Information
}
