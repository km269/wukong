// Package ard provides HTTP Message Signatures (RFC 9421) implementation
// for ANP-compatible agent authentication.
//
// HTTP Message Signatures allow agents to sign HTTP requests using their
// did:wba identity, providing:
//   - Request integrity verification (Content-Digest per RFC 9530)
//   - Non-repudiable agent identity (Signature-Input + Signature headers)
//   - Replay protection (created + expires + nonce parameters)
//   - Selective header coverage (@request-target, content-digest, etc.)
//
// The signing keyid references the DID document's key-1 assertion key
// for straightforward verification against the DID document.
package ard

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// HTTP Signature Constants
// ============================================================================

const (
	// SigHeaderSignatureInput is the HTTP header name for signature metadata.
	SigHeaderSignatureInput = "Signature-Input"

	// SigHeaderSignature is the HTTP header name for the signature value.
	SigHeaderSignature = "Signature"

	// SigHeaderContentDigest is the HTTP header name for content hash
	// (RFC 9530).
	SigHeaderContentDigest = "Content-Digest"

	// SigAlgorithmEd25519 is the Ed25519 signing algorithm.
	SigAlgorithmEd25519 = "ed25519"

	// SigParamKeyID identifies the signing key via DID reference.
	SigParamKeyID = "keyid"

	// SigParamAlg declares the signing algorithm.
	SigParamAlg = "alg"

	// SigParamCreated is the signature creation timestamp.
	SigParamCreated = "created"

	// SigParamExpires is the signature expiration timestamp.
	SigParamExpires = "expires"

	// SigParamNonce provides replay protection.
	SigParamNonce = "nonce"

	// SigParamTag is the signature label (e.g., "sig1").
	SigParamTag = "sig1"

	// SigSignatureTTL is the default signature validity duration.
	SigSignatureTTL = 5 * time.Minute
)

// ============================================================================
// HTTP Signer
// ============================================================================

// HTTPSigner signs HTTP requests using the agent's did:wba Ed25519
// signing key, per RFC 9421 HTTP Message Signatures.
//
// Usage:
//
//	signer := NewHTTPSigner(didManager)
//	err := signer.SignRequest(req, body)
//	// req now has Content-Digest, Signature-Input, Signature headers
type HTTPSigner struct {
	manager    *DIDManager
	signingKey ed25519.PrivateKey
	keyID      string
}

// NewHTTPSigner creates a new HTTP request signer backed by the
// DID manager's Ed25519 signing key.
func NewHTTPSigner(manager *DIDManager) *HTTPSigner {
	return &HTTPSigner{
		manager:    manager,
		signingKey: manager.signingKey,
		keyID:      manager.DID() + DIDWBAKey1ID,
	}
}

// SignRequest signs an HTTP request with RFC 9421 HTTP Message
// Signatures. It adds Content-Digest (SHA-256), Signature-Input,
// and Signature headers.
//
// The covered components include:
//   - "@method" — HTTP method
//   - "@target-uri" — full request URI
//   - "@authority" — host header
//   - "content-digest" — SHA-256 of body
//   - "content-type" — media type
//   - "content-length" — body size
func (s *HTTPSigner) SignRequest(
	req *http.Request,
	body []byte,
) error {
	if req == nil {
		return fmt.Errorf("http-sign: request is nil")
	}

	now := time.Now().UTC()
	expires := now.Add(SigSignatureTTL)

	// Generate nonce for replay protection
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)

	// Compute Content-Digest (SHA-256 of body)
	bodyHash := sha256.Sum256(body)
	contentDigest := "sha-256=:" +
		base64.StdEncoding.EncodeToString(bodyHash[:]) + ":"
	req.Header.Set(SigHeaderContentDigest, contentDigest)

	// Ensure content-length is set for coverage
	if req.ContentLength == 0 && len(body) > 0 {
		req.ContentLength = int64(len(body))
	}

	// Build Signature-Input header
	sigInput := s.buildSignatureInput(
		SigParamTag, now, expires, nonce)
	req.Header.Set(SigHeaderSignatureInput, sigInput)

	// Build signing base
	signingBase := s.buildSigningBase(req, SigParamTag)

	// Sign
	signature := ed25519.Sign(s.signingKey, []byte(signingBase))
	sigValue := SigParamTag + "=:" +
		base64.StdEncoding.EncodeToString(signature) + ":"
	req.Header.Set(SigHeaderSignature, sigValue)

	return nil
}

// buildSignatureInput constructs the Signature-Input header value
// according to RFC 9421 §4.1.
//
// Example:
//
//	sig1=("@method" "@target-uri" "content-digest" "content-type"); \
//	created=1618884473;keyid="did:wba:...:e1_<fp>#key-1";\
//	alg="ed25519";expires=1618888073;nonce="a1b2c3d4"
func (s *HTTPSigner) buildSignatureInput(
	tag string,
	created, expires time.Time,
	nonce string,
) string {
	coveredComponents := []string{
		`"@method"`,
		`"@target-uri"`,
		`"@authority"`,
		`"content-digest"`,
		`"content-type"`,
		`"content-length"`,
	}

	params := []string{
		fmt.Sprintf(`created=%d`, created.Unix()),
		fmt.Sprintf(`keyid="%s"`, s.keyID),
		fmt.Sprintf(`alg="%s"`, SigAlgorithmEd25519),
		fmt.Sprintf(`expires=%d`, expires.Unix()),
		fmt.Sprintf(`nonce="%s"`, nonce),
	}

	return fmt.Sprintf(
		`%s=(%s);%s`,
		tag,
		strings.Join(coveredComponents, " "),
		strings.Join(params, ";"),
	)
}

// buildSigningBase constructs the signing base string per RFC 9421 §3.1.
//
// The signing base is the concatenation of each covered component's
// canonical form, separated by newlines.
func (s *HTTPSigner) buildSigningBase(
	req *http.Request, tag string,
) string {
	var lines []string

	// Determine which components are covered from the Signature-Input
	// We know the fixed set from buildSignatureInput

	// "@method"
	lines = append(lines,
		`"@method": `+strings.ToUpper(req.Method))

	// "@target-uri"
	targetURI := req.URL.Path
	if req.URL.RawQuery != "" {
		targetURI += "?" + req.URL.RawQuery
	}
	lines = append(lines,
		`"@target-uri": `+targetURI)

	// "@authority"
	lines = append(lines,
		`"@authority": `+req.Host)

	// "content-digest"
	contentDigest := req.Header.Get(SigHeaderContentDigest)
	lines = append(lines,
		`"content-digest": `+contentDigest)

	// "content-type"
	contentType := req.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	lines = append(lines,
		`"content-type": `+contentType)

	// "content-length"
	contentLength := req.Header.Get("Content-Length")
	if contentLength == "" {
		if req.ContentLength > 0 {
			contentLength = strconv.FormatInt(req.ContentLength, 10)
		} else {
			contentLength = "0"
		}
	}
	lines = append(lines,
		`"content-length": `+contentLength)

	return strings.Join(lines, "\n")
}

// ============================================================================
// HTTP Verifier
// ============================================================================

// HTTPVerifier verifies HTTP Message Signatures on incoming requests
// using the sender's DID document. It performs:
//   1. Resolve the DID document from the keyid
//   2. Validate the signature parameters (time window, replay)
//   3. Verify the Content-Digest hash
//   4. Reconstruct the signing base and verify the Ed25519 signature
type HTTPVerifier struct {
	httpClient *http.Client
	seenNonces map[string]time.Time
	maxNonceAge time.Duration
}

// NewHTTPVerifier creates a new HTTP signature verifier.
func NewHTTPVerifier(httpClient *http.Client) *HTTPVerifier {
	return &HTTPVerifier{
		httpClient:  httpClient,
		seenNonces:  make(map[string]time.Time),
		maxNonceAge: SigSignatureTTL * 2,
	}
}

// VerifyRequest verifies the HTTP Message Signatures on an incoming
// request. Returns the verified DID on success.
//
// The verification process:
//  1. Parse Signature-Input and extract keyid
//  2. Resolve the DID document from keyid
//  3. Verify signature parameters (time + nonce)
//  4. Verify Content-Digest matches body
//  5. Reconstruct signing base
//  6. Verify Ed25519 signature against DID doc's key-1
func (v *HTTPVerifier) VerifyRequest(
	req *http.Request, body []byte,
) (*DIDDocument, error) {
	if req == nil {
		return nil, fmt.Errorf("http-verify: request is nil")
	}

	sigInput := req.Header.Get(SigHeaderSignatureInput)
	if sigInput == "" {
		return nil, fmt.Errorf(
			"http-verify: missing Signature-Input header")
	}

	sigHeader := req.Header.Get(SigHeaderSignature)
	if sigHeader == "" {
		return nil, fmt.Errorf(
			"http-verify: missing Signature header")
	}

	// Parse signature parameters
	tag, params := parseSignatureInput(sigInput)
	if tag == "" {
		return nil, fmt.Errorf(
			"http-verify: invalid Signature-Input format")
	}

	// Extract and validate keyid
	keyID, ok := params[SigParamKeyID]
	if !ok {
		return nil, fmt.Errorf(
			"http-verify: missing keyid parameter")
	}

	// Extract signing algorithm
	alg, ok := params[SigParamAlg]
	if !ok || alg != SigAlgorithmEd25519 {
		return nil, fmt.Errorf(
			"http-verify: unsupported algorithm: %s", alg)
	}

	// Validate time window
	now := time.Now().UTC()
	createdStr, hasCreated := params[SigParamCreated]
	expiresStr, hasExpires := params[SigParamExpires]

	if hasCreated {
		createdUnix, err := strconv.ParseInt(createdStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf(
				"http-verify: invalid created: %w", err)
		}
		created := time.Unix(createdUnix, 0)
		if now.Before(created) {
			return nil, fmt.Errorf(
				"http-verify: signature created in future")
		}
		if now.Sub(created) > SigSignatureTTL*2 {
			return nil, fmt.Errorf(
				"http-verify: signature too old")
		}
	}

	if hasExpires {
		expiresUnix, err := strconv.ParseInt(expiresStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf(
				"http-verify: invalid expires: %w", err)
		}
		expires := time.Unix(expiresUnix, 0)
		if now.After(expires) {
			return nil, fmt.Errorf(
				"http-verify: signature expired")
		}
	}

	// Validate nonce for replay protection
	if nonce, ok := params[SigParamNonce]; ok {
		if err := v.checkNonce(nonce); err != nil {
			return nil, fmt.Errorf(
				"http-verify: nonce: %w", err)
		}
	}

	// Verify Content-Digest
	if err := v.verifyContentDigest(req, body); err != nil {
		return nil, fmt.Errorf(
			"http-verify: content-digest: %w", err)
	}

	// Resolve DID document
	did, err := extractDIDFromKeyID(keyID)
	if err != nil {
		return nil, fmt.Errorf(
			"http-verify: extract DID: %w", err)
	}

	doc, err := ResolveDID(v.httpClient, did)
	if err != nil {
		return nil, fmt.Errorf(
			"http-verify: resolve DID: %w", err)
	}

	// Find key-1 public key
	pubKey, err := extractEd25519Key(doc)
	if err != nil {
		return nil, fmt.Errorf(
			"http-verify: extract key: %w", err)
	}

	// Reconstruct signing base
	signingBase := v.reconstructSigningBase(req)

	// Parse signature value
	sigBytes, err := parseSignatureValue(sigHeader, tag)
	if err != nil {
		return nil, fmt.Errorf(
			"http-verify: parse signature: %w", err)
	}

	// Verify Ed25519 signature
	if !ed25519.Verify(pubKey, []byte(signingBase), sigBytes) {
		return nil, fmt.Errorf(
			"http-verify: invalid Ed25519 signature")
	}

	return doc, nil
}

// verifyContentDigest verifies the SHA-256 Content-Digest header
// against the actual request body.
func (v *HTTPVerifier) verifyContentDigest(
	req *http.Request, body []byte,
) error {
	digestHeader := req.Header.Get(SigHeaderContentDigest)
	if digestHeader == "" {
		// Content-Digest is optional if no body
		if len(body) == 0 {
			return nil
		}
		return fmt.Errorf("missing Content-Digest header")
	}

	// Parse "sha-256=:<base64>:"
	if !strings.HasPrefix(digestHeader, "sha-256=:") {
		return fmt.Errorf(
			"unsupported digest algorithm in: %s",
			digestHeader)
	}

	digestValue := digestHeader[len("sha-256=:") :
		len(digestHeader)-1] // Strip trailing ":"

	expectedHash, err := base64.StdEncoding.DecodeString(
		digestValue,
	)
	if err != nil {
		return fmt.Errorf(
			"decode Content-Digest: %w", err)
	}

	actualHash := sha256.Sum256(body)
	if sha512.Sum512_256(expectedHash) !=
		sha512.Sum512_256(actualHash[:]) {
		return fmt.Errorf("Content-Digest mismatch")
	}

	return nil
}

// checkNonce validates a nonce for replay protection.
func (v *HTTPVerifier) checkNonce(nonce string) error {
	if prevTime, seen := v.seenNonces[nonce]; seen {
		if time.Since(prevTime) < v.maxNonceAge {
			return fmt.Errorf("nonce already used")
		}
	}

	v.seenNonces[nonce] = time.Now()

	// Clean old nonces periodically
	if len(v.seenNonces) > 10000 {
		v.cleanNonces()
	}

	return nil
}

// cleanNonces removes expired nonce entries.
func (v *HTTPVerifier) cleanNonces() {
	cutoff := time.Now().Add(-v.maxNonceAge)
	for nonce, t := range v.seenNonces {
		if t.Before(cutoff) {
			delete(v.seenNonces, nonce)
		}
	}
}

// reconstructSigningBase rebuilds the signing base from the
// incoming request for verification.
func (v *HTTPVerifier) reconstructSigningBase(
	req *http.Request,
) string {
	var lines []string

	lines = append(lines,
		`"@method": `+strings.ToUpper(req.Method))

	targetURI := req.URL.Path
	if req.URL.RawQuery != "" {
		targetURI += "?" + req.URL.RawQuery
	}
	lines = append(lines,
		`"@target-uri": `+targetURI)

	lines = append(lines,
		`"@authority": `+req.Host)

	contentDigest := req.Header.Get(SigHeaderContentDigest)
	lines = append(lines,
		`"content-digest": `+contentDigest)

	contentType := req.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	lines = append(lines,
		`"content-type": `+contentType)

	contentLength := req.Header.Get("Content-Length")
	if contentLength == "" {
		contentLength = "0"
	}
	lines = append(lines,
		`"content-length": `+contentLength)

	return strings.Join(lines, "\n")
}

// ============================================================================
// Signature Parsing Helpers
// ============================================================================

// parseSignatureInput parses the Signature-Input header per
// RFC 9421 §4.1 format.
//
// Example input:
//
//	sig1=("@method" "@target-uri" "content-digest"); \
//	created=1618884473;keyid="did:wba:...";alg="ed25519"
//
// Returns (tag, params map).
func parseSignatureInput(header string) (string, map[string]string) {
	params := make(map[string]string)

	// Extract tag: everything before '=('
	eqIdx := strings.Index(header, "=(")
	if eqIdx < 0 {
		return "", nil
	}
	tag := header[:eqIdx]

	// Split into covered components and parameters
	rest := header[eqIdx+1:] // skip "=("
	_, rest, found := strings.Cut(rest, ");")
	if !found {
		return tag, nil // Covered components only, no params
	}

	// Parameters are after ");"
	paramPart := rest

	// Parse semicolon-separated parameters
	for param := range strings.SplitSeq(paramPart, ";") {
		key, value, ok := strings.Cut(param, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Strip quotes
		value = strings.Trim(value, `"`)
		params[key] = value
	}

	return tag, params
}

// parseSignatureValue extracts the base64 signature for a given tag
// from the Signature header per RFC 9421 §4.2.
//
// Example: "sig1=:CgFpcyB0aGlzIHRoZSBzaWduYXR1cmU=:"
func parseSignatureValue(header, tag string) ([]byte, error) {
	// Find the tag's entry
	prefix := tag + "=:"
	if !strings.HasPrefix(header, prefix) {
		return nil, fmt.Errorf(
			"signature tag %s not found in header", tag)
	}

	rest := header[len(prefix):]
	colonIdx := strings.LastIndex(rest, ":")
	if colonIdx < 0 {
		return nil, fmt.Errorf(
			"invalid signature value format")
	}

	b64Value := rest[:colonIdx]
	return base64.StdEncoding.DecodeString(b64Value)
}

// extractDIDFromKeyID extracts the DID from a keyid format like
// "did:wba:domain:agent:name:e1_<fp>#key-1".
func extractDIDFromKeyID(keyID string) (string, error) {
	// keyid = did + "#key-1"
	hashIdx := strings.LastIndex(keyID, "#")
	if hashIdx < 0 {
		// Maybe there's no key fragment, return as-is
		return keyID, nil
	}
	return keyID[:hashIdx], nil
}

// extractEd25519Key extracts the Ed25519 public key from a
// DID document's key-1 verification method.
func extractEd25519Key(doc *DIDDocument) (ed25519.PublicKey, error) {
	for i := range doc.VerificationMethod {
		vm := &doc.VerificationMethod[i]
		if strings.HasSuffix(vm.ID, DIDWBAKey1ID) {
			pubBytes, err := base64.RawURLEncoding.DecodeString(
				vm.PublicKeyJWK.X,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"decode public key: %w", err)
			}
			if len(pubBytes) != ed25519.PublicKeySize {
				return nil, fmt.Errorf(
					"invalid Ed25519 public key length: %d",
					len(pubBytes))
			}
			return ed25519.PublicKey(pubBytes), nil
		}
	}
	return nil, fmt.Errorf(
		"Ed25519 key-1 not found in DID document")
}

// ============================================================================
// HTTP Client with Automatic Signing
// ============================================================================

// SignedHTTPClient is an http.Client wrapper that automatically
// signs outgoing requests using the agent's did:wba identity.
type SignedHTTPClient struct {
	client *http.Client
	signer *HTTPSigner
}

// NewSignedHTTPClient creates an HTTP client that auto-signs
// requests with the DID identity.
func NewSignedHTTPClient(
	client *http.Client,
	signer *HTTPSigner,
) *SignedHTTPClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &SignedHTTPClient{
		client: client,
		signer: signer,
	}
}

// Do signs the request and then executes it via the inner
// HTTP client. The request body is read for signing, then
// re-created for the actual request.
func (c *SignedHTTPClient) Do(
	req *http.Request,
) (*http.Response, error) {
	// Read body for signing
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("sign: read body: %w", err)
	}
	req.Body.Close()

	// Sign the request
	if err := c.signer.SignRequest(req, body); err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	// Create new request with the signed body
	signedReq := req.Clone(req.Context())
	signedReq.Body = io.NopCloser(
		strings.NewReader(string(body)),
	)

	return c.client.Do(signedReq)
}
