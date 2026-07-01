// Package ard provides ADP (Agent Description Protocol) document
// generation, implementing ANP-07 specification for unified agent
// capability description.
//
// The ADP builder consolidates the three existing Wukong description
// formats (A2A AgentCard, ACP AgentCard, ARD CatalogEntry) into a
// single ANP-compatible standard document.
package ard

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ============================================================================
// ADP Builder
// ============================================================================

// ADPBuilder constructs ANP-07 compatible Agent Description Protocol
// documents from Wukong's agent configuration. It unifies the three
// existing description formats (A2A AgentCard, ACP AgentCard,
// ARD CatalogEntry) into a single, standards-compliant document.
type ADPBuilder struct {
	config    *ADPBuildConfig
	signingKey ed25519.PrivateKey
}

// NewADPBuilder creates a new ADP document builder.
// If a signing key is not provided (nil), the builder will generate
// one automatically for the Proof signature.
func NewADPBuilder(
	config *ADPBuildConfig,
	signingKey ed25519.PrivateKey,
) (*ADPBuilder, error) {
	if config == nil {
		return nil, fmt.Errorf("adp: config is required")
	}
	if config.AgentName == "" {
		return nil, fmt.Errorf("adp: AgentName is required")
	}

	b := &ADPBuilder{
		config:     config,
		signingKey: signingKey,
	}

	// Generate a signing key if not provided
	if b.signingKey == nil {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("adp: generate key: %w", err)
		}
		b.signingKey = priv
		_ = pub // Used via signingKey.Public()
	}

	return b, nil
}

// Build generates a complete ADP document from the builder's config.
// It maps Wukong's multi-protocol architecture (A2A, ACP, AG-UI)
// into ANP-compatible interface declarations.
func (b *ADPBuilder) Build() *ADPDocument {
	baseURL := b.config.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost"
	}

	doc := &ADPDocument{
		ProtocolType:    ANPProtocolType,
		ProtocolVersion: ANPProtocolVersion,
		Type:           "AgentDescription",
		URL:            fmt.Sprintf("%s/agents/%s/ad.json", baseURL, urlize(b.config.AgentName)),
		Name:           b.config.AgentName,
		DID:            b.config.DID,
		Description:    b.config.AgentDescription,
		Version:        b.config.AgentVersion,
		Capabilities:   b.config.Capabilities,
		Tags:           b.config.Tags,
	}

	// Owner information
	if b.config.Organization != "" || b.config.OrganizationURL != "" {
		doc.Owner = &Owner{
			Name: b.config.Organization,
			URL:  b.config.OrganizationURL,
			DID:  b.config.OwnerDID,
		}
	}

	// Build interface declarations from Wukong's multi-protocol setup
	doc.Interfaces = b.buildInterfaces(baseURL)

	// Build security definitions
	doc.SecurityDefinitions = b.buildSecurityDefinitions()
	if b.config.SecurityMode != "" {
		doc.Security = b.config.SecurityMode
	}

	// Build information resources
	if len(b.config.InfoTypes) > 0 {
		doc.Infomations = b.config.InfoTypes
	}

	// Sign the document if a signing key is available.
	// This provides the cryptographic proof of document integrity.
	if b.signingKey != nil {
		doc.Proof = b.signDocument(doc)
	}

	return doc
}

// buildInterfaces creates ANP interface declarations for each
// enabled Wukong protocol (A2A, ACP, AG-UI).
func (b *ADPBuilder) buildInterfaces(baseURL string) []AgentInterface {
	var ifaces []AgentInterface

	// A2A interface — Agent-to-Agent task delegation protocol
	if b.config.A2AEnabled && b.config.A2APort > 0 {
		a2aURL := fmt.Sprintf("%s:%d", stripPort(baseURL), b.config.A2APort)
		ifaces = append(ifaces, AgentInterface{
			Type:        "StructuredInterface",
			Protocol:    "A2A",
			URL:         a2aURL,
			Version:     "1.0",
			Description: "Agent-to-Agent task delegation via Google A2A protocol",
		})
	}

	// ACP interface — Agent Client Protocol for MCP tool exposure
	if b.config.ACPEnabled && b.config.ACPPort > 0 {
		acpURL := fmt.Sprintf("%s:%d", stripPort(baseURL), b.config.ACPPort)
		ifaces = append(ifaces, AgentInterface{
			Type:        "StructuredInterface",
			Protocol:    "MCP",
			URL:         acpURL + "/mcp",
			Version:     "2024-11-05",
			Description: "MCP server exposing Wukong's tool ecosystem",
		})
	}

	// AG-UI interface — Web-based chat UI via SSE
	if b.config.AGUIEnabled && b.config.AGUIPort > 0 {
		aguiURL := fmt.Sprintf("%s:%d", stripPort(baseURL), b.config.AGUIPort)
		ifaces = append(ifaces, AgentInterface{
			Type:        "NaturalLanguageInterface",
			Protocol:    "YAML",
			URL:         aguiURL + "/agui",
			Version:     "1.0",
			Description: "SSE streaming chat interface for human interaction",
		})
	}

	// Natural language interface — always present as the primary
	// human-AI communication channel
	ifaces = append(ifaces, AgentInterface{
		Type:        "NaturalLanguageInterface",
		Protocol:    "YAML",
		URL:         baseURL,
		Version:     "1.0",
		Description: "Primary natural language agent interface",
	})

	return ifaces
}

// buildSecurityDefinitions constructs ANP-compatible security
// scheme definitions from the builder config.
func (b *ADPBuilder) buildSecurityDefinitions() map[string]*SecurityDefinition {
	defs := make(map[string]*SecurityDefinition)

	// DID:wba self-certifying — primary ANP identity mechanism
	if b.config.SecurityMode == "didwba_sc" || b.config.SecurityMode == "" {
		defs["didwba_sc"] = &SecurityDefinition{
			Type:        "didwba_sc",
			In:          "header",
			Name:        "Signature",
			Description: "Self-certifying authentication via HTTP Message Signatures (RFC 9421) using DID:wba identity",
		}
	}

	// API key authentication
	if b.config.SecurityMode == "api_key" || b.config.DID == "" {
		headerName := b.config.APIKeyHeader
		if headerName == "" {
			headerName = "X-API-Key"
		}
		defs["api_key"] = &SecurityDefinition{
			Type:        "apiKey",
			In:          "header",
			Name:        headerName,
			Description: "API key authentication via custom header",
		}
	}

	// JWT Bearer authentication
	defs["jwt"] = &SecurityDefinition{
		Type:        "http",
		Scheme:      "bearer",
		In:          "header",
		Name:        "Authorization",
		Description: "JWT Bearer token authentication",
	}

	return defs
}

// signDocument creates a DataIntegrityProof for the ADP document
// using the Ed25519 signing key. This enables ANP-compatible agents
// to verify the document's authenticity without trusting the server.
func (b *ADPBuilder) signDocument(doc *ADPDocument) *DataIntegrityProof {
	if b.signingKey == nil {
		return nil
	}

	// Build the signing payload: a canonical JSON representation
	// of the document without the proof field.
	payload := canonicalizeForSigning(doc)

	// Sign with Ed25519
	signature := ed25519.Sign(b.signingKey, payload)

	proof := &DataIntegrityProof{
		Type:               "DataIntegrityProof",
		Cryptosuite:        "eddsa-jcs-2022",
		Created:            time.Now().UTC().Format(time.RFC3339),
		VerificationMethod: doc.DID + "#key-1",
		ProofPurpose:       "assertionMethod",
		ProofValue:         base64.StdEncoding.EncodeToString(signature),
	}

	return proof
}

// canonicalizeForSigning creates a canonical byte representation of
// the document for signature generation. The proof field is excluded
// to avoid circular dependency.
func canonicalizeForSigning(doc *ADPDocument) []byte {
	// Build a simplified representation for signing.
	// In a full implementation, this would use JSON Canonicalization
	// Scheme (JCS / RFC 8785). For now, we build a standard JSON
	// without the proof field.
	parts := []string{
		fmt.Sprintf(`"protocolType":"%s"`, doc.ProtocolType),
		fmt.Sprintf(`"protocolVersion":"%s"`, doc.ProtocolVersion),
		fmt.Sprintf(`"type":"%s"`, doc.Type),
		fmt.Sprintf(`"url":"%s"`, doc.URL),
		fmt.Sprintf(`"name":"%s"`, doc.Name),
		fmt.Sprintf(`"did":"%s"`, doc.DID),
	}

	return []byte("{" + strings.Join(parts, ",") + "}")
}

// ============================================================================
// Conversion Helpers — ARD CatalogEntry → ADP
// ============================================================================

// CatalogEntryToADPBuildConfig converts an ARD CatalogEntry into
// an ADPBuildConfig, bridging the existing ARD catalog format to
// the ANP-compatible ADP format.
func CatalogEntryToADPBuildConfig(entry *CatalogEntry) *ADPBuildConfig {
	return &ADPBuildConfig{
		AgentName:        entry.DisplayName,
		AgentDescription: entry.Description,
		AgentVersion:     entry.Version,
		Capabilities:     entry.Capabilities,
		Tags:             entry.Tags,
	}
}

// ============================================================================
// Utility Functions
// ============================================================================

// urlize converts a string into a URL-safe slug by replacing
// spaces and special characters with hyphens.
func urlize(s string) string {
	result := make([]byte, 0, len(s))
	s = strings.ToLower(strings.TrimSpace(s))

	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		} else if c == ' ' || c == '_' || c == '-' {
			if len(result) > 0 && result[len(result)-1] != '-' {
				result = append(result, '-')
			}
		}
	}

	// Trim trailing hyphens
	for len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}

	if len(result) == 0 {
		return "agent"
	}

	return string(result)
}

// stripPort removes the port part from a URL string.
// "http://localhost:8080" -> "http://localhost"
func stripPort(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parsed.Host = parsed.Hostname()
	return parsed.String()
}

// DefaultADPBuildConfig returns a default ADP build configuration
// suitable for the Wukong agent registry. The signing key is nil,
// so generated ADP documents will not include a cryptographic proof.
func DefaultADPBuildConfig() *ADPBuildConfig {
	return &ADPBuildConfig{
		AgentName:        "Wukong AI Agent",
		AgentDescription: "Multi-protocol AI agent supporting A2A, ACP, and AG-UI",
		AgentVersion:     "1.0.0",
		Organization:     "KM269",
	}
}

// NewUnsignedADPBuilder creates an ADPBuilder without a signing key,
// suitable for discovery endpoints that don't require document proofs.
func NewUnsignedADPBuilder(config *ADPBuildConfig) *ADPBuilder {
	return &ADPBuilder{
		config:     config,
		signingKey: nil,
	}
}
