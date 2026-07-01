// Package ard provides did:wba (Web-Based Authentication) DID method
// implementation for Wukong agent identity.
//
// did:wba extends the standard did:web method with cryptographic key
// binding. The public key fingerprint is embedded in the DID identifier
// itself (e1_<sha256>), providing self-certifying identity verification
// without external trust anchors.
//
// DID Format: did:wba:<domain>:<path>:e1_<43-char-base64url-fingerprint>
//
// Example:
//
//	did:wba:wukong.ai:agent:cortex:e1_A7bC3dE8fG2hI5jK1mN4pQ6rS9tU2vW0xY3z
//
// Key Features:
//   - Ed25519 signing key for assertions (proof signatures)
//   - X25519 agreement key for E2EE (key-2 in DID doc)
//   - DataIntegrityProof with eddsa-jcs-2022 cryptosuite
//   - Triple verification: proof sign → JWK Thumbprint → key authorization
//   - DID document served at /.well-known/did.json and path-based URL
package ard

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// DID Constants
// ============================================================================

const (
	// DIDWBAPrefix is the did:wba method prefix.
	DIDWBAPrefix = "did:wba:"

	// DIDWBAE1Marker separates the e1_ fingerprint segment in the DID.
	DIDWBAE1Marker = ":e1_"

	// DIDWBAKey1ID is the key ID for the signing (assertion) key.
	DIDWBAKey1ID = "#key-1"

	// DIDWBAKey2ID is the key ID for the key agreement (E2EE) key.
	DIDWBAKey2ID = "#key-2"

	// DIDWBAContext is the W3C DID v1.0 context URL.
	DIDWBAContext = "https://www.w3.org/ns/did/v1"

	// DIDWBAJWKType is the JWK key type for Ed25519.
	DIDWBAJWKType = "OKP"

	// DIDWBAJWKCrvEd25519 is the JWK curve for Ed25519.
	DIDWBAJWKCrvEd25519 = "Ed25519"

	// DIDWBAJWKCrvX25519 is the JWK curve for X25519.
	DIDWBAJWKCrvX25519 = "X25519"
)

// ============================================================================
// DID Document Types
// ============================================================================

// DIDDocument represents a W3C DID Document for the did:wba method.
// It contains the public keys, authentication methods, service
// endpoints, and a cryptographic proof of integrity.
type DIDDocument struct {
	Context           []string             `json:"@context"`
	ID                string               `json:"id"`
	AlsoKnownAs       []string             `json:"alsoKnownAs,omitempty"`
	VerificationMethod []VerificationMethod  `json:"verificationMethod"`
	Authentication    []string             `json:"authentication"`
	AssertionMethod   []string             `json:"assertionMethod"`
	KeyAgreement      []string             `json:"keyAgreement,omitempty"`
	Service            []DIDService        `json:"service,omitempty"`
	Proof              *DataIntegrityProof `json:"proof,omitempty"`
}

// VerificationMethod describes a cryptographic key in the DID document.
type VerificationMethod struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Controller   string `json:"controller"`
	PublicKeyJWK JWK    `json:"publicKeyJwk"`
}

// JWK is a JSON Web Key representation for Ed25519/X25519 keys.
type JWK struct {
	KTY string `json:"kty"`
	CRV string `json:"crv"`
	X   string `json:"x"`
}

// DIDService describes a service endpoint in the DID document.
type DIDService struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	ServiceEndpoint string `json:"serviceEndpoint"`
}

// ============================================================================
// DID Manager
// ============================================================================

// DIDManager manages W3C did:wba identities for the Wukong agent.
// It handles key generation, DID creation, document serving, and
// verification of remote DID identities.
type DIDManager struct {
	mu sync.RWMutex

	// Identity components
	domain       string
	path         string
	agentName    string
	did          string
	didDoc       *DIDDocument

	// Cryptographic keys
	signingKey   ed25519.PrivateKey   // Ed25519 for assertions/signing
	agreementKey *ecdh.PrivateKey      // X25519 for E2EE key agreement

	// DID document URL
	docURL       string
}

// DIDManagerConfig configures the DID manager.
type DIDManagerConfig struct {
	// Domain is the agent's hosting domain (e.g., "wukong.ai").
	// Used in: did:wba:<domain>:<path>:e1_<fp>
	Domain string

	// Path is the optional path segment within the domain
	// (e.g., "agent:cortex").
	Path string

	// AgentName is the human-readable agent name for metadata.
	AgentName string

	// SigningKey is an optional pre-existing Ed25519 private key.
	// If nil, a new key is generated.
	SigningKey ed25519.PrivateKey

	// KeyPath is the file path for persisting keys (optional).
	KeyPath string

	// BaseURL is the public base URL of the agent server.
	// Used as the service endpoint in the DID document.
	BaseURL string
}

// NewDIDManager creates a new DID:wba identity manager.
// It generates Ed25519 signing keys and X25519 agreement keys,
// computes the e1_ fingerprint, and builds the DID document.
func NewDIDManager(cfg *DIDManagerConfig) (*DIDManager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("did: config is required")
	}
	if cfg.Domain == "" {
		return nil, fmt.Errorf("did: Domain is required")
	}

	m := &DIDManager{
		domain:    cfg.Domain,
		path:      cfg.Path,
		agentName: cfg.AgentName,
	}

	// Generate or load Ed25519 signing key
	if cfg.SigningKey != nil {
		m.signingKey = cfg.SigningKey
	} else {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf(
				"did: generate signing key: %w", err)
		}
		m.signingKey = priv
	}

	// Generate X25519 key for E2EE agreement
	if err := m.generateAgreementKey(); err != nil {
		return nil, fmt.Errorf(
			"did: generate agreement key: %w", err)
	}

	// Compute the e1_ fingerprint and build the DID
	if err := m.buildDID(); err != nil {
		return nil, fmt.Errorf("did: build DID: %w", err)
	}

	// Build the DID document
	m.buildDIDDocument()

	// Set the document URL
	m.docURL = fmt.Sprintf(
		"https://%s/.well-known/did/%s/did.json",
		m.domain, m.did[len(DIDWBAPrefix):],
	)

	return m, nil
}

// ============================================================================
// DID Construction
// ============================================================================

// generateAgreementKey generates an X25519 key pair for E2EE.
func (m *DIDManager) generateAgreementKey() error {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	m.agreementKey = priv
	return nil
}

// buildDID constructs the full did:wba identifier with the
// embedded Ed25519 public key fingerprint.
//
// Format: did:wba:<domain>:<path>:e1_<base64url-sha256(jwk_thumbprint)>
func (m *DIDManager) buildDID() error {
	// Compute JWK Thumbprint of the Ed25519 public key
	fingerprint, err := computeJWKThumbprintEd25519(m.signingKey)
	if err != nil {
		return fmt.Errorf("compute fingerprint: %w", err)
	}

	// Build DID string
	parts := []string{"did", "wba", m.domain}
	if m.path != "" {
		parts = append(parts, m.path)
	}
	parts = append(parts, "e1_"+fingerprint)

	m.did = strings.Join(parts, ":")
	return nil
}

// buildDIDDocument constructs the full W3C DID Document with
// verification methods, authentication relationships, service
// endpoints, and a DataIntegrityProof.
func (m *DIDManager) buildDIDDocument() {
	pubKey := m.signingKey.Public().(ed25519.PublicKey)
	agreementPub := m.agreementKey.PublicKey().Bytes()

	// Key-1: Ed25519 signing key (for assertions)
	vmKey1 := VerificationMethod{
		ID:         m.did + DIDWBAKey1ID,
		Type:       "JsonWebKey2020",
		Controller: m.did,
		PublicKeyJWK: JWK{
			KTY: DIDWBAJWKType,
			CRV: DIDWBAJWKCrvEd25519,
			X:   base64.RawURLEncoding.EncodeToString(pubKey),
		},
	}

	// Key-2: X25519 agreement key (for E2EE)
	vmKey2 := VerificationMethod{
		ID:         m.did + DIDWBAKey2ID,
		Type:       "JsonWebKey2020",
		Controller: m.did,
		PublicKeyJWK: JWK{
			KTY: DIDWBAJWKType,
			CRV: DIDWBAJWKCrvX25519,
			X:   base64.RawURLEncoding.EncodeToString(agreementPub),
		},
	}

	doc := &DIDDocument{
		Context: []string{
			DIDWBAContext,
			"https://w3id.org/security/data-integrity/v2",
		},
		ID: m.did,
		AlsoKnownAs: []string{
			fmt.Sprintf("https://%s", m.domain),
		},
		VerificationMethod: []VerificationMethod{vmKey1, vmKey2},
		Authentication:     []string{vmKey1.ID},
		AssertionMethod:    []string{vmKey1.ID},
		KeyAgreement:       []string{vmKey2.ID},
	}

	// Add service endpoints
	if m.agentName != "" {
		doc.Service = []DIDService{
			{
				ID:              m.did + "#agent-service",
				Type:            "AIAgentService",
				ServiceEndpoint: fmt.Sprintf("https://%s/agents/%s/ad.json", m.domain, urlize(m.agentName)),
			},
		}
	}

	// Sign the DID document
	doc.Proof = m.signDIDDocument(doc)

	m.didDoc = doc
}

// signDIDDocument creates a DataIntegrityProof for the DID document.
// The proof uses eddsa-jcs-2022 cryptosuite with the Ed25519 signing key.
func (m *DIDManager) signDIDDocument(doc *DIDDocument) *DataIntegrityProof {
	// Build canonical representation for signing.
	// In a full implementation, JCS (JSON Canonicalization Scheme,
	// RFC 8785) would be used. Here we use a simplified approach.
	payload := didDocumentSigningPayload(doc)
	signature := ed25519.Sign(m.signingKey, payload)

	return &DataIntegrityProof{
		Type:               "DataIntegrityProof",
		Cryptosuite:        "eddsa-jcs-2022",
		Created:            time.Now().UTC().Format(time.RFC3339),
		VerificationMethod: m.did + DIDWBAKey1ID,
		ProofPurpose:       "assertionMethod",
		ProofValue:         base64.RawURLEncoding.EncodeToString(signature),
	}
}

// ============================================================================
// Getters
// ============================================================================

// DID returns the full did:wba identifier string.
func (m *DIDManager) DID() string {
	return m.did
}

// DIDDocument returns the W3C DID Document.
func (m *DIDManager) DIDDocument() *DIDDocument {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.didDoc
}

// SigningPublicKey returns the Ed25519 public key for verification.
func (m *DIDManager) SigningPublicKey() ed25519.PublicKey {
	return m.signingKey.Public().(ed25519.PublicKey)
}

// AgreementPrivateKey returns the X25519 private key for E2EE.
func (m *DIDManager) AgreementPrivateKey() *ecdh.PrivateKey {
	return m.agreementKey
}

// AgreementPublicKey returns the X25519 public key for E2EE.
func (m *DIDManager) AgreementPublicKey() *ecdh.PublicKey {
	return m.agreementKey.PublicKey()
}

// DocumentURL returns the URL where the DID document is served.
func (m *DIDManager) DocumentURL() string {
	return m.docURL
}

// Domain returns the agent's hosting domain.
func (m *DIDManager) Domain() string {
	return m.domain
}

// ============================================================================
// Signing Operations
// ============================================================================

// Sign creates an Ed25519 signature over the given payload.
// Returns the raw 64-byte signature.
func (m *DIDManager) Sign(payload []byte) []byte {
	return ed25519.Sign(m.signingKey, payload)
}

// SignBase64 creates a base64-encoded Ed25519 signature over
// the given payload.
func (m *DIDManager) SignBase64(payload []byte) string {
	sig := ed25519.Sign(m.signingKey, payload)
	return base64.RawURLEncoding.EncodeToString(sig)
}

// ============================================================================
// DID Verification
// ============================================================================

// VerifyDIDDocument performs triple verification of a did:wba document:
//  1. Verify that the DID's e1_ fingerprint matches key-1's JWK thumbprint
//  2. Verify the DataIntegrityProof signature
//  3. Verify that key-1 is in the authentication relationship
func VerifyDIDDocument(doc *DIDDocument) error {
	if doc == nil {
		return fmt.Errorf("did: document is nil")
	}

	// 1. Extract e1_ fingerprint from DID
	e1Fp, err := extractE1Fingerprint(doc.ID)
	if err != nil {
		return fmt.Errorf("did: extract fingerprint: %w", err)
	}

	// 2. Find key-1 verification method
	var key1 *VerificationMethod
	for i := range doc.VerificationMethod {
		if strings.HasSuffix(
			doc.VerificationMethod[i].ID, DIDWBAKey1ID) {
			key1 = &doc.VerificationMethod[i]
			break
		}
	}
	if key1 == nil {
		return fmt.Errorf(
			"did: key-1 verification method not found")
	}

	// 3. Verify e1_ fingerprint matches key-1 JWK thumbprint
	computedFp, err := computeJWKThumbprint(&key1.PublicKeyJWK)
	if err != nil {
		return fmt.Errorf(
			"did: compute key-1 thumbprint: %w", err)
	}
	if computedFp != e1Fp {
		return fmt.Errorf(
			"did: e1_ fingerprint mismatch: expected %s, got %s",
			e1Fp, computedFp,
		)
	}

	// 4. Verify key-1 is in authentication list
	found := false
	for _, auth := range doc.Authentication {
		if auth == key1.ID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf(
			"did: key-1 not authorized for authentication")
	}

	// 5. Verify the DataIntegrityProof signature
	if doc.Proof != nil {
		if err := verifyProof(doc, doc.Proof); err != nil {
			return fmt.Errorf(
				"did: proof verification failed: %w", err)
		}
	}

	return nil
}

// ============================================================================
// HTTP Handler for DID Document Serving
// ============================================================================

// DIDHandler serves the W3C DID Document at its well-known URL.
// Route: /.well-known/did/<domain>/<path>/e1_<fp>/did.json
type DIDHandler struct {
	manager *DIDManager
}

// NewDIDHandler creates a DID document HTTP handler.
func NewDIDHandler(manager *DIDManager) *DIDHandler {
	return &DIDHandler{manager: manager}
}

// RegisterRoutes registers DID document routes on an HTTP mux.
func (h *DIDHandler) RegisterRoutes(mux *http.ServeMux) {
	// Main DID document endpoint
	mux.HandleFunc(
		"/.well-known/did/",
		h.handleDIDDocument,
	)

	// Also serve at the DID document URL directly
	mux.HandleFunc(
		"/.well-known/did.json",
		h.handleDIDDocument,
	)
}

// handleDIDDocument serves the DID document as JSON.
func (h *DIDHandler) handleDIDDocument(
	w http.ResponseWriter, r *http.Request,
) {
	if r.Method != http.MethodGet {
		http.Error(
			w, "Method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	doc := h.manager.DIDDocument()
	if doc == nil {
		http.Error(
			w, "DID document not available",
			http.StatusServiceUnavailable,
		)
		return
	}

	w.Header().Set("Content-Type", "application/did+json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	json.NewEncoder(w).Encode(doc)
}

// ============================================================================
// Future: DID Resolution
// ============================================================================

// ResolveDID resolves a did:wba identifier by fetching and
// verifying its DID document from the well-known endpoint.
// This is a placeholder for future full implementation.
func ResolveDID(httpClient *http.Client, did string) (*DIDDocument, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// Parse domain and path from the DID
	domain, path, err := parseDIDForResolution(did)
	if err != nil {
		return nil, fmt.Errorf("resolve: parse DID: %w", err)
	}

	// Build DID document URL
	docURL := fmt.Sprintf(
		"https://%s/.well-known/did/%s/did.json",
		domain, path,
	)

	resp, err := httpClient.Get(docURL)
	if err != nil {
		return nil, fmt.Errorf("resolve: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"resolve: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("resolve: read body: %w", err)
	}

	var doc DIDDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("resolve: parse: %w", err)
	}

	// Verify the document
	if err := VerifyDIDDocument(&doc); err != nil {
		return nil, fmt.Errorf("resolve: verify: %w", err)
	}

	return &doc, nil
}

// ============================================================================
// Cryptographic Helpers
// ============================================================================

// computeJWKThumbprintEd25519 computes the RFC 7638 JWK Thumbprint
// of an Ed25519 public key. The thumbprint is SHA-256 of the
// canonical JWK JSON, encoded as base64url without padding.
func computeJWKThumbprintEd25519(priv ed25519.PrivateKey) (string, error) {
	pub := priv.Public().(ed25519.PublicKey)
	jwk := JWK{
		KTY: DIDWBAJWKType,
		CRV: DIDWBAJWKCrvEd25519,
		X:   base64.RawURLEncoding.EncodeToString(pub),
	}
	return computeJWKThumbprint(&jwk)
}

// computeJWKThumbprint computes the RFC 7638 JWK Thumbprint
// of any JWK. The thumbprint is SHA-256 of the canonical
// JSON of the required members (kty, crv, x), encoded as
// base64url without padding.
func computeJWKThumbprint(jwk *JWK) (string, error) {
	// Build canonical representation per RFC 7638 §3
	canonical := fmt.Sprintf(
		`{"crv":"%s","kty":"%s","x":"%s"}`,
		jwk.CRV, jwk.KTY, jwk.X,
	)

	hash := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(hash[:]), nil
}

// extractE1Fingerprint extracts the e1_ fingerprint from a did:wba
// identifier. Returns the raw base64url-encoded fingerprint string.
func extractE1Fingerprint(did string) (string, error) {
	idx := strings.LastIndex(did, DIDWBAE1Marker)
	if idx < 0 {
		return "", fmt.Errorf(
			"no e1_ marker found in DID: %s", did)
	}
	return did[idx+len(DIDWBAE1Marker):], nil
}

// parseDIDForResolution parses a did:wba identifier into
// domain and path components for URL construction.
func parseDIDForResolution(did string) (domain, path string, err error) {
	if !strings.HasPrefix(did, DIDWBAPrefix) {
		return "", "", fmt.Errorf(
			"not a did:wba identifier: %s", did)
	}

	// Strip prefix and e1_ segment
	rest := did[len(DIDWBAPrefix):]
	e1Idx := strings.LastIndex(rest, DIDWBAE1Marker)
	if e1Idx >= 0 {
		rest = rest[:e1Idx]
	}

	// First ":" segment is domain, rest is path
	parts := strings.SplitN(rest, ":", 2)
	domain = parts[0]
	if len(parts) > 1 {
		path = parts[1]
	}

	return domain, path, nil
}

// verifyProof verifies the DataIntegrityProof signature on a
// DID document using the Ed25519 public key from key-1.
func verifyProof(doc *DIDDocument, proof *DataIntegrityProof) error {
	if proof.Cryptosuite != "eddsa-jcs-2022" {
		return fmt.Errorf(
			"unsupported cryptosuite: %s", proof.Cryptosuite)
	}

	// Find key-1
	var key1 *VerificationMethod
	for i := range doc.VerificationMethod {
		if strings.HasSuffix(
			doc.VerificationMethod[i].ID, DIDWBAKey1ID) {
			key1 = &doc.VerificationMethod[i]
			break
		}
	}
	if key1 == nil {
		return fmt.Errorf("key-1 not found for verification")
	}

	// Decode the public key
	pubKeyBytes, err := base64.RawURLEncoding.DecodeString(
		key1.PublicKeyJWK.X,
	)
	if err != nil {
		return fmt.Errorf(
			"decode public key: %w", err)
	}

	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf(
			"invalid Ed25519 public key length: %d",
			len(pubKeyBytes))
	}

	// Decode the signature
	sigBytes, err := base64.RawURLEncoding.DecodeString(
		proof.ProofValue,
	)
	if err != nil {
		return fmt.Errorf(
			"decode signature: %w", err)
	}

	// Build signing payload (document without proof)
	payload := didDocumentSigningPayload(doc)

	// Verify
	if !ed25519.Verify(
		ed25519.PublicKey(pubKeyBytes), payload, sigBytes) {
		return fmt.Errorf(
			"invalid Ed25519 signature")
	}

	return nil
}

// didDocumentSigningPayload builds a canonical representation of
// the DID document for signing. The proof field is excluded.
func didDocumentSigningPayload(doc *DIDDocument) []byte {
	// Build a deterministic representation without the proof.
	// In production, use JSON Canonicalization Scheme (RFC 8785).
	parts := []string{
		fmt.Sprintf(`"id":"%s"`, doc.ID),
	}

	// Include first verification method public key
	if len(doc.VerificationMethod) > 0 {
		vm := doc.VerificationMethod[0]
		parts = append(parts, fmt.Sprintf(
			`"key1":"%s"`, vm.PublicKeyJWK.X,
		))
	}

	return []byte("{" + strings.Join(parts, ",") + "}")
}

// ============================================================================
// Export/Import Key Functions
// ============================================================================

// ExportSigningKey exports the Ed25519 private key as base64.
func (m *DIDManager) ExportSigningKey() string {
	return base64.RawURLEncoding.EncodeToString(
		m.signingKey.Seed(),
	)
}

// ImportSigningKey imports an Ed25519 private key from a base64 seed.
// Returns a new DIDManagerConfig with the imported key for use with
// NewDIDManager.
func ImportSigningKey(encodedSeed string) (ed25519.PrivateKey, error) {
	seed, err := base64.RawURLEncoding.DecodeString(encodedSeed)
	if err != nil {
		return nil, fmt.Errorf("decode seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf(
			"invalid seed size: expected %d, got %d",
			ed25519.SeedSize, len(seed))
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
