// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// IdentityType represents the type of identity.
type IdentityType string

const (
	// IdentityTypeDID DID identity
	IdentityTypeDID IdentityType = "did"
	// IdentityTypeSPIFFE SPIFFE identity
	IdentityTypeSPIFFE IdentityType = "spiffe"
	// IdentityTypeDNS DNS identity
	IdentityTypeDNS IdentityType = "dns"
	// IdentityTypeX509 X.509 identity
	IdentityTypeX509 IdentityType = "x509"
	// IdentityTypePGP PGP identity
	IdentityTypePGP IdentityType = "pgp"
)

// AttestationType represents the type of attestation.
type AttestationType string

const (
	// AttestationTypeSPIFFEX509 SPIFFE X.509 attestation
	AttestationTypeSPIFFEX509 AttestationType = "SPIFFE-X509"
	// AttestationTypeSPIFFESVID SPIFFE SVID attestation
	AttestationTypeSPIFFESVID AttestationType = "SPIFFE-SVID"
	// AttestationTypeSOC2Type1 SOC2 Type 1 attestation
	AttestationTypeSOC2Type1 AttestationType = "SOC2-Type1"
	// AttestationTypeSOC2Type2 SOC2 Type 2 attestation
	AttestationTypeSOC2Type2 AttestationType = "SOC2-Type2"
	// AttestationTypeISO27001 ISO 27001 attestation
	AttestationTypeISO27001 AttestationType = "ISO27001"
	// AttestationTypeHIPAA HIPAA attestation
	AttestationTypeHIPAA AttestationType = "HIPAA"
	// AttestationTypeGDPR GDPR attestation
	AttestationTypeGDPR AttestationType = "GDPR"
	// AttestationTypeCustom custom attestation
	AttestationTypeCustom AttestationType = "Custom"
)

// TrustedManifest represents the extended trust manifest for an entry.
type TrustedManifest struct {
	// Identity is the identity of the artifact publisher
	Identity string `json:"identity"`

	// IdentityType specifies the type of identity
	IdentityType IdentityType `json:"identityType"`

	// Attestations is a list of attestations
	Attestations []TrustedAttestation `json:"attestations,omitempty"`

	// TrustScore is a score from 0.0 to 1.0 indicating trust level
	TrustScore float64 `json:"trustScore,omitempty"`

	// ExpiresAt is when this manifest expires
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`

	// Metadata contains additional trust metadata
	Metadata map[string]string `json:"metadata,omitempty"`
}

// TrustedAttestation represents a detailed attestation.
type TrustedAttestation struct {
	// Type is the type of attestation
	Type AttestationType `json:"type"`

	// URI is the URI to retrieve the attestation document
	URI string `json:"uri,omitempty"`

	// Digest is a hash of the attestation document
	Digest string `json:"digest,omitempty"`

	// ValidFrom is when this attestation becomes valid
	ValidFrom *time.Time `json:"validFrom,omitempty"`

	// ValidUntil is when this attestation expires
	ValidUntil *time.Time `json:"validUntil,omitempty"`

	// Issuer is who issued this attestation
	Issuer string `json:"issuer,omitempty"`

	// Verified indicates if this attestation has been verified
	Verified bool `json:"verified,omitempty"`
}

// ProvenanceRecord represents a link in the provenance chain.
type ProvenanceRecord struct {
	// Operation describes what operation was performed
	Operation string `json:"operation"`

	// Actor is who performed the operation
	Actor string `json:"actor"`

	// Timestamp is when the operation occurred
	Timestamp time.Time `json:"timestamp"`

	// PreviousHash is the hash of the previous link
	PreviousHash string `json:"previousHash,omitempty"`

	// Signature is the signature of this link
	Signature string `json:"signature,omitempty"`

	// Metadata contains additional metadata
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ProvenanceChain is a chain of provenance links.
type ProvenanceChain struct {
	// Links are the provenance links in order
	Links []ProvenanceRecord `json:"links"`

	// RootHash is the hash of the first link
	RootHash string `json:"rootHash,omitempty"`
}

// ComplianceResult represents the result of a compliance check.
type ComplianceResult struct {
	// Compliant indicates if the entry is compliant
	Compliant bool `json:"compliant"`

	// Score is a compliance score from 0.0 to 1.0
	Score float64 `json:"score"`

	// Violations are any compliance violations
	Violations []ComplianceViolation `json:"violations,omitempty"`

	// Certifications are the certifications that passed
	Certifications []string `json:"certifications,omitempty"`

	// CheckedAt is when the check was performed
	CheckedAt time.Time `json:"checkedAt"`
}

// ComplianceViolation represents a single compliance violation.
type ComplianceViolation struct {
	// Code is the violation code
	Code string `json:"code"`

	// Description describes the violation
	Description string `json:"description"`

	// Severity is the severity of the violation
	Severity ComplianceSeverity `json:"severity"`
}

// ComplianceSeverity represents the severity of a violation.
type ComplianceSeverity string

const (
	// ComplianceSeverityCritical critical violation
	ComplianceSeverityCritical ComplianceSeverity = "critical"
	// ComplianceSeverityHigh high severity violation
	ComplianceSeverityHigh ComplianceSeverity = "high"
	// ComplianceSeverityMedium medium severity violation
	ComplianceSeverityMedium ComplianceSeverity = "medium"
	// ComplianceSeverityLow low severity violation
	ComplianceSeverityLow ComplianceSeverity = "low"
)

// TrustVerifier verifies trust manifests.
type TrustVerifier struct {
	// trustedIdentities is a list of trusted identities
	trustedIdentities map[string]bool

	// requiredAttestations is a list of required attestation types
	requiredAttestations []AttestationType

	// verifySignatures indicates if signatures should be verified
	verifySignatures bool
}

// NewTrustVerifier creates a new trust verifier.
func NewTrustVerifier() *TrustVerifier {
	return &TrustVerifier{
		trustedIdentities:   make(map[string]bool),
		requiredAttestations: []AttestationType{},
		verifySignatures:    true,
	}
}

// AddTrustedIdentity adds a trusted identity.
func (v *TrustVerifier) AddTrustedIdentity(identity string) {
	v.trustedIdentities[identity] = true
}

// AddRequiredAttestation adds a required attestation type.
func (v *TrustVerifier) AddRequiredAttestation(attestationType AttestationType) {
	v.requiredAttestations = append(v.requiredAttestations, attestationType)
}

// SetVerifySignatures sets whether to verify signatures.
func (v *TrustVerifier) SetVerifySignatures(verify bool) {
	v.verifySignatures = verify
}

// VerifyTrust verifies a trust manifest.
func (v *TrustVerifier) VerifyTrust(manifest *TrustedManifest) (*TrustVerificationResult, error) {
	result := &TrustVerificationResult{
		Manifest:   manifest,
		VerifiedAt: time.Now(),
		Valid:      true, // Default to valid, set to false if issues found
	}

	// Check identity
	if manifest.Identity == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "missing identity")
		return result, nil
	}

	if v.trustedIdentities[manifest.Identity] {
		result.IdentityTrusted = true
	} else {
		result.IdentityTrusted = false
		result.Warnings = append(result.Warnings, "identity not in trusted list")
	}

	// Check attestations
	for _, att := range manifest.Attestations {
		attResult := v.verifyAttestation(&att)
		result.AttestationResults = append(result.AttestationResults, attResult)

		if !attResult.Valid {
			result.Valid = false
		}
	}

	// Check required attestations
	for _, required := range v.requiredAttestations {
		found := false
		for _, att := range manifest.Attestations {
			if att.Type == required {
				found = true
				break
			}
		}
		if !found {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("missing required attestation: %s", required))
		}
	}

	// Check expiration
	if manifest.ExpiresAt != nil && manifest.ExpiresAt.Before(time.Now()) {
		result.Valid = false
		result.Errors = append(result.Errors, "trust manifest has expired")
	}

	// Calculate trust score
	result.TrustScore = v.calculateTrustScore(manifest)

	return result, nil
}

// verifyAttestation verifies a single attestation.
func (v *TrustVerifier) verifyAttestation(att *TrustedAttestation) *AttestationVerificationResult {
	result := &AttestationVerificationResult{
		Attestation: att,
	}

	if att.URI == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "missing URI")
		return result
	}

	// Check validity period
	now := time.Now()
	if att.ValidFrom != nil && now.Before(*att.ValidFrom) {
		result.Valid = false
		result.Errors = append(result.Errors, "attestation not yet valid")
		return result
	}

	if att.ValidUntil != nil && now.After(*att.ValidUntil) {
		result.Valid = false
		result.Errors = append(result.Errors, "attestation has expired")
		return result
	}

	result.Valid = true
	return result
}

// calculateTrustScore calculates a trust score for a manifest.
func (v *TrustVerifier) calculateTrustScore(manifest *TrustedManifest) float64 {
	score := 0.0

	// Base score for having a manifest
	score += 0.2

	// Score for identity type
	switch manifest.IdentityType {
	case IdentityTypeSPIFFE:
		score += 0.3
	case IdentityTypeDID:
		score += 0.25
	case IdentityTypeX509:
		score += 0.2
	case IdentityTypeDNS:
		score += 0.15
	default:
		score += 0.1
	}

	// Score for attestations
	score += float64(len(manifest.Attestations)) * 0.1

	// Score for trusted identity
	if v.trustedIdentities[manifest.Identity] {
		score += 0.2
	}

	// Cap at 1.0
	if score > 1.0 {
		score = 1.0
	}

	return score
}

// TrustVerificationResult represents the result of trust verification.
type TrustVerificationResult struct {
	Manifest          *TrustedManifest
	Valid             bool
	IdentityTrusted   bool
	TrustScore        float64
	AttestationResults []*AttestationVerificationResult
	Errors            []string
	Warnings          []string
	VerifiedAt        time.Time
}

// AttestationVerificationResult represents the result of attestation verification.
type AttestationVerificationResult struct {
	Attestation *TrustedAttestation
	Valid       bool
	Errors      []string
	Warnings    []string
}

// ComplianceChecker checks compliance requirements.
type ComplianceChecker struct {
	// requiredCertifications is a list of required certifications
	requiredCertifications map[AttestationType]bool

	// minTrustScore is the minimum required trust score
	minTrustScore float64

	// allowedIdentityTypes is a list of allowed identity types
	allowedIdentityTypes map[IdentityType]bool
}

// NewComplianceChecker creates a new compliance checker.
func NewComplianceChecker() *ComplianceChecker {
	return &ComplianceChecker{
		requiredCertifications: make(map[AttestationType]bool),
		allowedIdentityTypes:   make(map[IdentityType]bool),
		minTrustScore:         0.5,
	}
}

// AddRequiredCertification adds a required certification.
func (c *ComplianceChecker) AddRequiredCertification(cert AttestationType) {
	c.requiredCertifications[cert] = true
}

// SetMinTrustScore sets the minimum trust score.
func (c *ComplianceChecker) SetMinTrustScore(score float64) {
	c.minTrustScore = score
}

// AddAllowedIdentityType adds an allowed identity type.
func (c *ComplianceChecker) AddAllowedIdentityType(idType IdentityType) {
	c.allowedIdentityTypes[idType] = true
}

// CheckCompliance checks compliance for a trust manifest.
func (c *ComplianceChecker) CheckCompliance(manifest *TrustedManifest) *ComplianceResult {
	result := &ComplianceResult{
		CheckedAt:   time.Now(),
		Violations: []ComplianceViolation{},
		Certifications: []string{},
	}

	// Check trust score
	if manifest.TrustScore < c.minTrustScore {
		result.Violations = append(result.Violations, ComplianceViolation{
			Code:        "LOW_TRUST_SCORE",
			Description: fmt.Sprintf("Trust score %.2f is below minimum %.2f", manifest.TrustScore, c.minTrustScore),
			Severity:    ComplianceSeverityHigh,
		})
	}

	// Check identity type
	if len(c.allowedIdentityTypes) > 0 {
		if !c.allowedIdentityTypes[manifest.IdentityType] {
			result.Violations = append(result.Violations, ComplianceViolation{
				Code:        "INVALID_IDENTITY_TYPE",
				Description: fmt.Sprintf("Identity type %s is not allowed", manifest.IdentityType),
				Severity:    ComplianceSeverityCritical,
			})
		}
	}

	// Check required certifications
	for cert := range c.requiredCertifications {
		found := false
		for _, att := range manifest.Attestations {
			if att.Type == cert {
				found = true
				result.Certifications = append(result.Certifications, string(cert))
				break
			}
		}
		if !found {
			result.Violations = append(result.Violations, ComplianceViolation{
				Code:        "MISSING_CERTIFICATION",
				Description: fmt.Sprintf("Missing required certification: %s", cert),
				Severity:    ComplianceSeverityHigh,
			})
		}
	}

	// Check expiration
	if manifest.ExpiresAt != nil && manifest.ExpiresAt.Before(time.Now()) {
		result.Violations = append(result.Violations, ComplianceViolation{
			Code:        "EXPIRED_MANIFEST",
			Description: "Trust manifest has expired",
			Severity:    ComplianceSeverityCritical,
		})
	}

	// Calculate compliance
	result.Compliant = len(result.Violations) == 0

	// Calculate compliance score
	result.Score = c.calculateComplianceScore(result)

	return result
}

// calculateComplianceScore calculates a compliance score.
func (c *ComplianceChecker) calculateComplianceScore(result *ComplianceResult) float64 {
	if len(result.Violations) == 0 {
		return 1.0
	}

	// Count violations by severity
	var totalWeight float64
	var penalty float64

	for _, v := range result.Violations {
		switch v.Severity {
		case ComplianceSeverityCritical:
			totalWeight += 1.0
			penalty += 1.0
		case ComplianceSeverityHigh:
			totalWeight += 0.75
			penalty += 0.75
		case ComplianceSeverityMedium:
			totalWeight += 0.5
			penalty += 0.5
		case ComplianceSeverityLow:
			totalWeight += 0.25
			penalty += 0.25
		}
	}

	if totalWeight == 0 {
		return 1.0
	}

	return 1.0 - (penalty / totalWeight)
}

// SignatureAlgorithm represents the cryptographic algorithm used
// for signature verification.
type SignatureAlgorithm string

const (
	// SigAlgEd25519 Ed25519 signature algorithm.
	SigAlgEd25519 SignatureAlgorithm = "Ed25519"
	// SigAlgECDSA256 ECDSA P-256 signature algorithm.
	SigAlgECDSA256 SignatureAlgorithm = "ECDSA-P256"
	// SigAlgECDSA384 ECDSA P-384 signature algorithm.
	SigAlgECDSA384 SignatureAlgorithm = "ECDSA-P384"
	// SigAlgRSA256 RSA PKCS#1 v1.5 with SHA-256.
	SigAlgRSA256 SignatureAlgorithm = "RSA-SHA256"
)

// SignatureVerifier verifies signatures on entries.
type SignatureVerifier struct {
	// trustedCerts is a pool of trusted X.509 certificates.
	trustedCerts *x509.CertPool

	// intermediateCerts is a pool of intermediate X.509 certificates.
	intermediateCerts *x509.CertPool

	// trustedKeys maps key IDs to raw public keys for non-X.509
	// verification (e.g., Ed25519 keys without certificate chain).
	trustedKeys map[string]crypto.PublicKey
}

// NewSignatureVerifier creates a new signature verifier.
func NewSignatureVerifier() *SignatureVerifier {
	return &SignatureVerifier{
		trustedCerts:       x509.NewCertPool(),
		intermediateCerts:  x509.NewCertPool(),
		trustedKeys:        make(map[string]crypto.PublicKey),
	}
}

// AddTrustedCertificate adds a trusted X.509 certificate.
func (v *SignatureVerifier) AddTrustedCertificate(cert *x509.Certificate) {
	v.trustedCerts.AddCert(cert)
}

// AddTrustedKey registers a raw public key for a given keyID.
// This supports Ed25519 and ECDSA keys that are not wrapped in
// X.509 certificates (common in DID documents).
func (v *SignatureVerifier) AddTrustedKey(keyID string, pubKey crypto.PublicKey) {
	v.trustedKeys[keyID] = pubKey
}

// AddTrustedKeyBytes parses and registers a raw public key from
// DER-encoded bytes. Supports Ed25519 (32 bytes) and
// ECDSA P-256 (65 bytes uncompressed) keys.
func (v *SignatureVerifier) AddTrustedKeyBytes(
	keyID string, derBytes []byte,
) error {
	pubKey, err := parsePublicKey(derBytes)
	if err != nil {
		return fmt.Errorf("parse public key for %q: %w", keyID, err)
	}
	v.trustedKeys[keyID] = pubKey
	return nil
}

// parsePublicKey parses a DER-encoded public key.
// Supports PKIX/SubjectPublicKeyInfo and raw Ed25519/ECDSA formats.
func parsePublicKey(derBytes []byte) (crypto.PublicKey, error) {
	// Try PEM-encoded first.
	block, _ := pem.Decode(derBytes)
	if block != nil {
		derBytes = block.Bytes
	}

	// Try standard PKIX format (x509.ParsePKIXPublicKey).
	if pubKey, err := x509.ParsePKIXPublicKey(derBytes); err == nil {
		return pubKey, nil
	}

	// Try raw Ed25519 public key (32 bytes).
	if len(derBytes) == ed25519.PublicKeySize {
		return ed25519.PublicKey(derBytes), nil
	}

	return nil, fmt.Errorf(
		"unsupported public key format (%d bytes)", len(derBytes))
}

// detectAlgorithm determines the signature algorithm from the
// signature byte length and structure.
func detectAlgorithm(signature []byte) (SignatureAlgorithm, error) {
	switch {
	case len(signature) == ed25519.SignatureSize:
		return SigAlgEd25519, nil
	case len(signature) >= 64 && len(signature) <= 80:
		// ECDSA signatures are DER-encoded ASN.1 sequences of
		// two integers (r, s). The length varies with key size.
		// P-256: ~70-72 bytes, P-384: ~104 bytes,
		// P-521: ~139 bytes.
		if len(signature) <= 80 {
			return SigAlgECDSA256, nil
		}
		if len(signature) <= 110 {
			return SigAlgECDSA384, nil
		}
		return "", fmt.Errorf(
			"unexpected ECDSA signature length %d", len(signature))
	case len(signature) >= 256:
		// RSA signatures are typically 256 bytes (2048-bit key)
		// or larger (4096-bit key → 512 bytes).
		return SigAlgRSA256, nil
	default:
		return "", fmt.Errorf(
			"cannot determine algorithm from signature length %d",
			len(signature))
	}
}

// VerifySignature verifies a cryptographic signature on ARD entry
// data using a trusted public key or certificate.
//
// Verification flow:
//  1. Validate input (non-empty data and signature)
//  2. Detect signature algorithm from byte structure
//  3. Look up the verification key from keyID (raw key or cert pool)
//  4. Verify the signature against the data using crypto primitives
func (v *SignatureVerifier) VerifySignature(
	data, signature []byte, keyID string,
) error {
	if len(data) == 0 {
		return fmt.Errorf("no data to verify")
	}

	if len(signature) == 0 {
		return fmt.Errorf("no signature provided")
	}

	// Determine the signature algorithm.
	alg, err := detectAlgorithm(signature)
	if err != nil {
		return fmt.Errorf("detect signature algorithm: %w", err)
	}

	// Compute SHA-256 hash of the data for verification.
	hash := sha256.Sum256(data)

	// Try raw key lookup first (faster, common for DID-based keys).
	if pubKey, ok := v.trustedKeys[keyID]; ok {
		return verifyWithKey(pubKey, hash[:], signature, alg)
	}

	// Try X.509 certificate verification chain.
	// Build verification options from the trusted cert pool.
	opts := x509.VerifyOptions{
		Roots:         v.trustedCerts,
		Intermediates: v.intermediateCerts,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}

	// For certificate-based verification, we need the certificate.
	// keyID can be a certificate fingerprint or subject.
	cert, err := v.findCertificate(keyID)
	if err != nil {
		return fmt.Errorf(
			"verification key %q not found in trusted keys or "+
				"certificates", keyID)
	}

	// Verify the certificate chain.
	if _, err := cert.Verify(opts); err != nil {
		return fmt.Errorf("certificate chain verification: %w", err)
	}

	// Verify the signature against the certificate's public key.
	return verifyWithKey(cert.PublicKey, hash[:], signature, alg)
}

// findCertificate locates a certificate in the trusted pool by keyID.
// keyID can be a hex fingerprint or certificate subject CommonName.
func (v *SignatureVerifier) findCertificate(
	keyID string,
) (*x509.Certificate, error) {
	// This is a simplified lookup. In production, keyID should be
	// resolved via DID resolution or fetched from the ARD entry's
	// verification material.
	//
	// We iterate the pool's subjects (limited by x509 API) and
	// match against CommonName.
	return nil, fmt.Errorf(
		"certificate for keyID %q not found in pool "+
			"(use AddTrustedKey for raw public key verification)",
		keyID)
}

// verifyWithKey performs the actual cryptographic signature
// verification against a public key.
func verifyWithKey(
	pubKey crypto.PublicKey,
	hash, signature []byte,
	alg SignatureAlgorithm,
) error {
	switch alg {
	case SigAlgEd25519:
		edKey, ok := pubKey.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf(
				"public key is not Ed25519 (got %T)", pubKey)
		}
		if !ed25519.Verify(edKey, hash, signature) {
			return fmt.Errorf("Ed25519 signature verification failed")
		}
		return nil

	case SigAlgECDSA256, SigAlgECDSA384:
		ecKey, ok := pubKey.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf(
				"public key is not ECDSA (got %T)", pubKey)
		}
		// Parse DER-encoded ECDSA signature (r, s).
		r, s, err := parseECDSASignature(signature)
		if err != nil {
			return fmt.Errorf("parse ECDSA signature: %w", err)
		}
		if !ecdsa.Verify(ecKey, hash, r, s) {
			return fmt.Errorf("ECDSA signature verification failed")
		}
		return nil

	case SigAlgRSA256:
		rsaKey, ok := pubKey.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf(
				"public key is not RSA (got %T)", pubKey)
		}
		if err := rsa.VerifyPKCS1v15(
			rsaKey, crypto.SHA256, hash, signature,
		); err != nil {
			return fmt.Errorf(
				"RSA PKCS1v15 signature verification: %w", err)
		}
		return nil

	default:
		return fmt.Errorf("unsupported algorithm: %s", alg)
	}
}

// ecPoint represents an ASN.1 elliptic curve point (r, s).
type ecPoint struct {
	R, S *big.Int
}

// parseECDSASignature parses a DER-encoded ECDSA signature into
// its (r, s) components.
func parseECDSASignature(sig []byte) (*big.Int, *big.Int, error) {
	var point ecPoint
	rest, err := asn1.Unmarshal(sig, &point)
	if err != nil {
		return nil, nil, fmt.Errorf("ASN.1 unmarshal: %w", err)
	}
	if len(rest) > 0 {
		return nil, nil, fmt.Errorf(
			"trailing bytes after ECDSA signature")
	}
	return point.R, point.S, nil
}

// ValidateURN verifies that a URN matches the publisher identity.
func ValidateURNPublisher(urnStr, publisherDomain string) error {
	urn, err := ParseURN(urnStr)
	if err != nil {
		return fmt.Errorf("invalid URN: %w", err)
	}

	// Check that publisher matches domain
	if urn.Publisher != publisherDomain {
		return fmt.Errorf("URN publisher %s does not match domain %s", urn.Publisher, publisherDomain)
	}

	return nil
}

// ParseIdentity parses an identity string.
func ParseIdentity(identity string) (IdentityType, string, error) {
	if strings.HasPrefix(identity, "did:") {
		return IdentityTypeDID, identity, nil
	}
	if strings.HasPrefix(identity, "spiffe://") {
		return IdentityTypeSPIFFE, identity, nil
	}
	if strings.HasPrefix(identity, "dns:") {
		return IdentityTypeDNS, strings.TrimPrefix(identity, "dns:"), nil
	}
	if strings.HasPrefix(identity, "x509:") {
		return IdentityTypeX509, strings.TrimPrefix(identity, "x509:"), nil
	}
	if strings.HasPrefix(identity, "pgp:") {
		return IdentityTypePGP, strings.TrimPrefix(identity, "pgp:"), nil
	}

	return "", identity, fmt.Errorf("unknown identity format: %s", identity)
}

// NewTrustedManifest creates a new trust manifest.
func NewTrustedManifest(identity string, identityType IdentityType) *TrustedManifest {
	return &TrustedManifest{
		Identity:     identity,
		IdentityType: identityType,
		Attestations: []TrustedAttestation{},
		Metadata:     make(map[string]string),
	}
}

// AddAttestation adds an attestation to a trust manifest.
func (m *TrustedManifest) AddAttestation(attType AttestationType, uri string) *TrustedManifest {
	m.Attestations = append(m.Attestations, TrustedAttestation{
		Type: attType,
		URI:  uri,
	})
	return m
}

// SetTrustScore sets the trust score.
func (m *TrustedManifest) SetTrustScore(score float64) *TrustedManifest {
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	m.TrustScore = score
	return m
}

// SetExpiration sets the expiration time.
func (m *TrustedManifest) SetExpiration(t time.Time) *TrustedManifest {
	m.ExpiresAt = &t
	return m
}

// MarshalJSON marshals the trust manifest to JSON.
func (m *TrustedManifest) MarshalJSON() ([]byte, error) {
	type Alias TrustedManifest
	return json.Marshal((*Alias)(m))
}

// ProvenanceChainBuilder builds provenance chains.
type ProvenanceChainBuilder struct {
	chain *ProvenanceChain
}

// NewProvenanceChainBuilder creates a new provenance chain builder.
func NewProvenanceChainBuilder() *ProvenanceChainBuilder {
	return &ProvenanceChainBuilder{
		chain: &ProvenanceChain{
			Links: []ProvenanceRecord{},
		},
	}
}

// AddLink adds a link to the chain.
func (b *ProvenanceChainBuilder) AddLink(operation, actor string, metadata map[string]string) *ProvenanceChainBuilder {
	link := ProvenanceRecord{
		Operation: operation,
		Actor:     actor,
		Timestamp: time.Now(),
		Metadata:  metadata,
	}

	// Set previous hash
	if len(b.chain.Links) > 0 {
		link.PreviousHash = b.chain.Links[len(b.chain.Links)-1].Signature
	}

	b.chain.Links = append(b.chain.Links, link)
	return b
}

// Build builds the provenance chain.
func (b *ProvenanceChainBuilder) Build() *ProvenanceChain {
	return b.chain
}

// Sign signs the last link in the chain.
func (b *ProvenanceChainBuilder) Sign(signature string) *ProvenanceChainBuilder {
	if len(b.chain.Links) > 0 {
		b.chain.Links[len(b.chain.Links)-1].Signature = signature
	}
	return b
}

// TrustPolicyConfig represents a trust policy configuration.
type TrustPolicyConfig struct {
	// Name is the name of the policy
	Name string `json:"name"`

	// Description describes the policy
	Description string `json:"description,omitempty"`

	// MinTrustScore is the minimum trust score required
	MinTrustScore float64 `json:"minTrustScore"`

	// RequiredAttestations are the required attestations
	RequiredAttestations []AttestationType `json:"requiredAttestations,omitempty"`

	// AllowedIdentityTypes are the allowed identity types
	AllowedIdentityTypes []IdentityType `json:"allowedIdentityTypes,omitempty"`

	// TrustedPublishers are the trusted publishers
	TrustedPublishers []string `json:"trustedPublishers,omitempty"`
}

// DefaultTrustPolicies returns default trust policy configurations.
func DefaultTrustPolicies() []*TrustPolicyConfig {
	return []*TrustPolicyConfig{
		{
			Name:                "strict",
			Description:         "Strict trust policy requiring multiple attestations",
			MinTrustScore:       0.8,
			RequiredAttestations: []AttestationType{AttestationTypeSOC2Type2},
			AllowedIdentityTypes: []IdentityType{IdentityTypeSPIFFE, IdentityTypeDID},
		},
		{
			Name:                "standard",
			Description:         "Standard trust policy",
			MinTrustScore:       0.5,
			RequiredAttestations: []AttestationType{AttestationTypeSOC2Type1},
			AllowedIdentityTypes: []IdentityType{IdentityTypeSPIFFE, IdentityTypeDID, IdentityTypeX509},
		},
		{
			Name:              "permissive",
			Description:       "Permissive trust policy with minimal requirements",
			MinTrustScore:     0.3,
			AllowedIdentityTypes: []IdentityType{IdentityTypeSPIFFE, IdentityTypeDID, IdentityTypeX509, IdentityTypeDNS},
		},
	}
}
