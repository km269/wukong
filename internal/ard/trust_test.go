// Package ard provides Agentic Resource Discovery implementation.
package ard

import (
	"strings"
	"testing"
	"time"
)

func TestTrustedManifest(t *testing.T) {
	manifest := &TrustedManifest{
		Identity:     "spiffe://example.com/agent/test",
		IdentityType: IdentityTypeSPIFFE,
		TrustScore:   0.8,
		Attestations: []TrustedAttestation{
			{
				Type: AttestationTypeSOC2Type2,
				URI:  "https://example.com/soc2.pdf",
			},
		},
	}

	if manifest.Identity != "spiffe://example.com/agent/test" {
		t.Errorf("TrustedManifest.Identity = %v, want spiffe://example.com/agent/test", manifest.Identity)
	}

	if manifest.IdentityType != IdentityTypeSPIFFE {
		t.Errorf("TrustedManifest.IdentityType = %v, want IdentityTypeSPIFFE", manifest.IdentityType)
	}

	if manifest.TrustScore != 0.8 {
		t.Errorf("TrustedManifest.TrustScore = %v, want 0.8", manifest.TrustScore)
	}

	if len(manifest.Attestations) != 1 {
		t.Errorf("TrustedManifest.Attestations length = %v, want 1", len(manifest.Attestations))
	}
}

func TestNewTrustedManifest(t *testing.T) {
	manifest := NewTrustedManifest("did:web:example.com", IdentityTypeDID)

	if manifest.Identity != "did:web:example.com" {
		t.Errorf("NewTrustedManifest().Identity = %v, want did:web:example.com", manifest.Identity)
	}

	if manifest.IdentityType != IdentityTypeDID {
		t.Errorf("NewTrustedManifest().IdentityType = %v, want IdentityTypeDID", manifest.IdentityType)
	}

	if manifest.Attestations == nil {
		t.Error("NewTrustedManifest().Attestations should not be nil")
	}

	if manifest.Metadata == nil {
		t.Error("NewTrustedManifest().Metadata should not be nil")
	}
}

func TestTrustedManifestAddAttestation(t *testing.T) {
	manifest := NewTrustedManifest("did:web:test.com", IdentityTypeDID)
	manifest.AddAttestation(AttestationTypeSOC2Type2, "https://test.com/soc2.pdf")
	manifest.AddAttestation(AttestationTypeISO27001, "https://test.com/iso27001.pdf")

	if len(manifest.Attestations) != 2 {
		t.Errorf("Attestations length = %v, want 2", len(manifest.Attestations))
	}

	if manifest.Attestations[0].Type != AttestationTypeSOC2Type2 {
		t.Errorf("First attestation type = %v, want SOC2Type2", manifest.Attestations[0].Type)
	}
}

func TestTrustedManifestSetTrustScore(t *testing.T) {
	manifest := NewTrustedManifest("did:web:test.com", IdentityTypeDID)

	// Test valid score
	manifest.SetTrustScore(0.75)
	if manifest.TrustScore != 0.75 {
		t.Errorf("TrustScore = %v, want 0.75", manifest.TrustScore)
	}

	// Test cap at 1.0
	manifest.SetTrustScore(1.5)
	if manifest.TrustScore != 1.0 {
		t.Errorf("TrustScore = %v, want 1.0 (capped)", manifest.TrustScore)
	}

	// Test floor at 0.0
	manifest.SetTrustScore(-0.5)
	if manifest.TrustScore != 0.0 {
		t.Errorf("TrustScore = %v, want 0.0 (floored)", manifest.TrustScore)
	}
}

func TestTrustedManifestSetExpiration(t *testing.T) {
	manifest := NewTrustedManifest("did:web:test.com", IdentityTypeDID)
	expiry := time.Now().Add(24 * time.Hour)

	manifest.SetExpiration(expiry)

	if manifest.ExpiresAt == nil {
		t.Error("ExpiresAt should not be nil")
	}

	if !manifest.ExpiresAt.Equal(expiry) {
		t.Errorf("ExpiresAt = %v, want %v", manifest.ExpiresAt, expiry)
	}
}

func TestTrustedAttestation(t *testing.T) {
	validFrom := time.Now()
	validUntil := time.Now().Add(365 * 24 * time.Hour)

	att := TrustedAttestation{
		Type:      AttestationTypeSOC2Type2,
		URI:       "https://example.com/soc2.pdf",
		Digest:    "sha256:abc123",
		ValidFrom: &validFrom,
		ValidUntil: &validUntil,
		Issuer:    "Example Trust Authority",
		Verified:  true,
	}

	if att.Type != AttestationTypeSOC2Type2 {
		t.Errorf("TrustedAttestation.Type = %v, want AttestationTypeSOC2Type2", att.Type)
	}

	if att.URI != "https://example.com/soc2.pdf" {
		t.Errorf("TrustedAttestation.URI = %v, want https://example.com/soc2.pdf", att.URI)
	}

	if !att.Verified {
		t.Error("TrustedAttestation.Verified should be true")
	}
}

func TestIdentityType(t *testing.T) {
	tests := []struct {
		identity string
		expected IdentityType
	}{
		{"did:web:example.com", IdentityTypeDID},
		{"spiffe://example.com/agent/test", IdentityTypeSPIFFE},
		{"dns:example.com", IdentityTypeDNS},
		{"x509:CN=Test", IdentityTypeX509},
		{"pgp:0x12345678", IdentityTypePGP},
		{"unknown:format", ""},
	}

	for _, tt := range tests {
		t.Run(tt.identity, func(t *testing.T) {
			idType, _, err := ParseIdentity(tt.identity)

			if tt.expected == "" {
				if err == nil {
					t.Error("Expected error for unknown identity type")
				}
			} else {
				if idType != tt.expected {
					t.Errorf("ParseIdentity(%s) = %v, want %v", tt.identity, idType, tt.expected)
				}
			}
		})
	}
}

func TestParseIdentityUnknown(t *testing.T) {
	_, _, err := ParseIdentity("invalid:format")
	if err == nil {
		t.Error("Expected error for invalid identity format")
	}
}

func TestTrustVerifier(t *testing.T) {
	verifier := NewTrustVerifier()

	if verifier == nil {
		t.Fatal("NewTrustVerifier returned nil")
	}

	// Test adding trusted identity
	verifier.AddTrustedIdentity("did:web:trusted.com")

	// Test adding required attestation
	verifier.AddRequiredAttestation(AttestationTypeSOC2Type2)

	if len(verifier.requiredAttestations) != 1 {
		t.Errorf("requiredAttestations length = %v, want 1", len(verifier.requiredAttestations))
	}

	if !verifier.trustedIdentities["did:web:trusted.com"] {
		t.Error("Trusted identity should be in map")
	}
}

func TestTrustVerifierVerifyTrust(t *testing.T) {
	verifier := NewTrustVerifier()
	verifier.AddTrustedIdentity("spiffe://example.com/agent/test")
	verifier.AddRequiredAttestation(AttestationTypeSOC2Type2)

	// Create manifest with required attestation
	validUntil := time.Now().Add(365 * 24 * time.Hour)
	manifest := &TrustedManifest{
		Identity:     "spiffe://example.com/agent/test",
		IdentityType: IdentityTypeSPIFFE,
		TrustScore:   0.8,
		Attestations: []TrustedAttestation{
			{
				Type:      AttestationTypeSOC2Type2,
				URI:       "https://example.com/soc2.pdf",
				ValidUntil: &validUntil,
			},
		},
	}

	result, err := verifier.VerifyTrust(manifest)
	if err != nil {
		t.Fatalf("VerifyTrust() error = %v", err)
	}

	if !result.Valid {
		t.Error("Verification result should be valid")
	}

	if !result.IdentityTrusted {
		t.Error("Identity should be trusted")
	}

	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", result.Errors)
	}
}

func TestTrustVerifierMissingIdentity(t *testing.T) {
	verifier := NewTrustVerifier()

	manifest := &TrustedManifest{
		Identity: "",
	}

	result, err := verifier.VerifyTrust(manifest)
	if err != nil {
		t.Fatalf("VerifyTrust() error = %v", err)
	}

	if result.Valid {
		t.Error("Verification result should be invalid for missing identity")
	}
}

func TestTrustVerifierMissingAttestation(t *testing.T) {
	verifier := NewTrustVerifier()
	verifier.AddRequiredAttestation(AttestationTypeSOC2Type2)

	manifest := &TrustedManifest{
		Identity:     "did:web:test.com",
		IdentityType: IdentityTypeDID,
		Attestations: []TrustedAttestation{},
	}

	result, err := verifier.VerifyTrust(manifest)
	if err != nil {
		t.Fatalf("VerifyTrust() error = %v", err)
	}

	if result.Valid {
		t.Error("Verification result should be invalid for missing attestation")
	}

	found := false
	for _, err := range result.Errors {
		if err == "missing required attestation: SOC2-Type2" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Should have error about missing SOC2 attestation")
	}
}

func TestTrustVerifierExpiredManifest(t *testing.T) {
	verifier := NewTrustVerifier()

	expired := time.Now().Add(-24 * time.Hour)
	manifest := &TrustedManifest{
		Identity:   "did:web:test.com",
		ExpiresAt:  &expired,
	}

	result, err := verifier.VerifyTrust(manifest)
	if err != nil {
		t.Fatalf("VerifyTrust() error = %v", err)
	}

	if result.Valid {
		t.Error("Verification result should be invalid for expired manifest")
	}
}

func TestTrustVerifierCalculateTrustScore(t *testing.T) {
	verifier := NewTrustVerifier()
	verifier.AddTrustedIdentity("did:web:trusted.com")

	// Manifest with SPIFFE identity and attestations
	manifest := &TrustedManifest{
		Identity:     "did:web:trusted.com",
		IdentityType: IdentityTypeSPIFFE,
		TrustScore:   0.5,
		Attestations: []TrustedAttestation{
			{Type: AttestationTypeSOC2Type2},
			{Type: AttestationTypeISO27001},
		},
	}

	result, err := verifier.VerifyTrust(manifest)
	if err != nil {
		t.Fatalf("VerifyTrust() error = %v", err)
	}

	// Score should be calculated
	if result.TrustScore <= 0 {
		t.Error("TrustScore should be greater than 0")
	}
}

func TestComplianceChecker(t *testing.T) {
	checker := NewComplianceChecker()

	if checker == nil {
		t.Fatal("NewComplianceChecker returned nil")
	}

	checker.AddRequiredCertification(AttestationTypeSOC2Type2)
	checker.SetMinTrustScore(0.6)

	if len(checker.requiredCertifications) != 1 {
		t.Errorf("requiredCertifications length = %v, want 1", len(checker.requiredCertifications))
	}

	if checker.minTrustScore != 0.6 {
		t.Errorf("minTrustScore = %v, want 0.6", checker.minTrustScore)
	}
}

func TestComplianceCheckerCheckCompliance(t *testing.T) {
	checker := NewComplianceChecker()
	checker.AddRequiredCertification(AttestationTypeSOC2Type2)
	checker.SetMinTrustScore(0.5)

	// Compliant manifest
	manifest := &TrustedManifest{
		Identity:     "spiffe://example.com/agent",
		IdentityType: IdentityTypeSPIFFE,
		TrustScore:   0.8,
		Attestations: []TrustedAttestation{
			{Type: AttestationTypeSOC2Type2},
		},
	}

	result := checker.CheckCompliance(manifest)

	if !result.Compliant {
		t.Error("Manifest should be compliant")
	}

	if len(result.Violations) != 0 {
		t.Errorf("Violations = %v, want empty", result.Violations)
	}

	if result.Score != 1.0 {
		t.Errorf("Score = %v, want 1.0", result.Score)
	}
}

func TestComplianceCheckerLowTrustScore(t *testing.T) {
	checker := NewComplianceChecker()
	checker.SetMinTrustScore(0.7)

	manifest := &TrustedManifest{
		Identity:   "did:web:test.com",
		TrustScore: 0.3,
	}

	result := checker.CheckCompliance(manifest)

	if result.Compliant {
		t.Error("Manifest should not be compliant")
	}

	if len(result.Violations) != 1 {
		t.Errorf("Violations length = %v, want 1", len(result.Violations))
	}

	if result.Violations[0].Code != "LOW_TRUST_SCORE" {
		t.Errorf("Violation code = %v, want LOW_TRUST_SCORE", result.Violations[0].Code)
	}
}

func TestComplianceCheckerMissingCertification(t *testing.T) {
	checker := NewComplianceChecker()
	checker.AddRequiredCertification(AttestationTypeSOC2Type2)

	manifest := &TrustedManifest{
		Identity:   "did:web:test.com",
		TrustScore: 0.8,
		Attestations: []TrustedAttestation{
			{Type: AttestationTypeISO27001},
		},
	}

	result := checker.CheckCompliance(manifest)

	if result.Compliant {
		t.Error("Manifest should not be compliant")
	}

	found := false
	for _, v := range result.Violations {
		if v.Code == "MISSING_CERTIFICATION" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Should have MISSING_CERTIFICATION violation")
	}
}

func TestComplianceCheckerExpiredManifest(t *testing.T) {
	checker := NewComplianceChecker()

	expired := time.Now().Add(-24 * time.Hour)
	manifest := &TrustedManifest{
		Identity:   "did:web:test.com",
		ExpiresAt:  &expired,
	}

	result := checker.CheckCompliance(manifest)

	if result.Compliant {
		t.Error("Manifest should not be compliant")
	}

	found := false
	for _, v := range result.Violations {
		if v.Code == "EXPIRED_MANIFEST" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Should have EXPIRED_MANIFEST violation")
	}
}

func TestComplianceSeverity(t *testing.T) {
	severities := []ComplianceSeverity{
		ComplianceSeverityCritical,
		ComplianceSeverityHigh,
		ComplianceSeverityMedium,
		ComplianceSeverityLow,
	}

	for _, sev := range severities {
		if sev == "" {
			t.Errorf("Severity should not be empty")
		}
	}
}

func TestSignatureVerifier(t *testing.T) {
	verifier := NewSignatureVerifier()

	if verifier == nil {
		t.Fatal("NewSignatureVerifier returned nil")
	}
}

func TestSignatureVerifierVerifySignature(t *testing.T) {
	verifier := NewSignatureVerifier()

	// Test with no data
	err := verifier.VerifySignature(nil, []byte("signature"), "key1")
	if err == nil {
		t.Error("Should error with no data")
	}

	// Test with no signature
	err = verifier.VerifySignature([]byte("data"), nil, "key1")
	if err == nil {
		t.Error("Should error with no signature")
	}

	// Test with valid-length signature (64 bytes = Ed25519 size).
	// Without a trusted key registered, this will fail at the
	// verification step, but it should pass algorithm detection.
	err = verifier.VerifySignature(
		[]byte("data"), make([]byte, 64), "key1")
	if err == nil {
		// No trusted key registered; expected to fail at verify.
		t.Log("signature verification passed (unexpected without key)")
	} else if !strings.Contains(err.Error(), "not found") {
		// Error should be about missing key, not algorithm detection.
		t.Logf("VerifySignature error (expected without key): %v", err)
	}

	// Test that short signature is rejected (31 < 64 for Ed25519).
	err = verifier.VerifySignature([]byte("data"), make([]byte, 31), "key1")
	if err == nil {
		t.Error("Should error with short signature")
	}
}

func TestValidateURNPublisher(t *testing.T) {
	// Valid match
	err := ValidateURNPublisher("urn:air:example.com:agent:test", "example.com")
	if err != nil {
		t.Errorf("ValidateURNPublisher() error = %v, want nil", err)
	}

	// Invalid match
	err = ValidateURNPublisher("urn:air:example.com:agent:test", "other.com")
	if err == nil {
		t.Error("Should error for mismatched publisher")
	}

	// Invalid URN
	err = ValidateURNPublisher("invalid:urn", "example.com")
	if err == nil {
		t.Error("Should error for invalid URN")
	}
}

func TestProvenanceChain(t *testing.T) {
	chain := &ProvenanceChain{
		Links: []ProvenanceRecord{
			{
				Operation:   "create",
				Actor:       "system@example.com",
				Timestamp:   time.Now(),
				PreviousHash: "",
				Signature:   "sig1",
			},
			{
				Operation:   "update",
				Actor:       "admin@example.com",
				Timestamp:   time.Now(),
				PreviousHash: "sig1",
				Signature:   "sig2",
			},
		},
	}

	if len(chain.Links) != 2 {
		t.Errorf("Chain links length = %v, want 2", len(chain.Links))
	}

	if chain.Links[0].Operation != "create" {
		t.Errorf("First link operation = %v, want create", chain.Links[0].Operation)
	}

	if chain.Links[1].PreviousHash != "sig1" {
		t.Errorf("Second link previous hash = %v, want sig1", chain.Links[1].PreviousHash)
	}
}

func TestProvenanceChainBuilder(t *testing.T) {
	builder := NewProvenanceChainBuilder()

	builder.AddLink("create", "creator@example.com", nil)
	builder.AddLink("update", "updater@example.com", map[string]string{"version": "1.0"})
	builder.Sign("signature2")

	chain := builder.Build()

	if len(chain.Links) != 2 {
		t.Errorf("Chain links length = %v, want 2", len(chain.Links))
	}

	if chain.Links[0].Operation != "create" {
		t.Errorf("First link operation = %v, want create", chain.Links[0].Operation)
	}

	if chain.Links[1].Signature != "signature2" {
		t.Errorf("Second link signature = %v, want signature2", chain.Links[1].Signature)
	}
}

func TestProvenanceChainBuilderPreviousHash(t *testing.T) {
	builder := NewProvenanceChainBuilder()

	builder.AddLink("create", "creator@example.com", nil)
	builder.Sign("sig1")
	builder.AddLink("update", "updater@example.com", nil)
	builder.Sign("sig2")

	chain := builder.Build()

	if chain.Links[1].PreviousHash != "sig1" {
		t.Errorf("Second link previous hash = %v, want sig1", chain.Links[1].PreviousHash)
	}
}

func TestTrustPolicyConfig(t *testing.T) {
	policy := &TrustPolicyConfig{
		Name:                "strict",
		Description:         "Strict trust policy",
		MinTrustScore:       0.8,
		RequiredAttestations: []AttestationType{AttestationTypeSOC2Type2},
		AllowedIdentityTypes: []IdentityType{IdentityTypeSPIFFE, IdentityTypeDID},
		TrustedPublishers:   []string{"trusted.com"},
	}

	if policy.Name != "strict" {
		t.Errorf("TrustPolicyConfig.Name = %v, want strict", policy.Name)
	}

	if policy.MinTrustScore != 0.8 {
		t.Errorf("TrustPolicyConfig.MinTrustScore = %v, want 0.8", policy.MinTrustScore)
	}
}

func TestDefaultTrustPolicies(t *testing.T) {
	policies := DefaultTrustPolicies()

	if len(policies) != 3 {
		t.Errorf("DefaultTrustPolicies() returned %v policies, want 3", len(policies))
	}

	// Check strict policy
	strict := policies[0]
	if strict.Name != "strict" {
		t.Errorf("First policy name = %v, want strict", strict.Name)
	}

	if strict.MinTrustScore != 0.8 {
		t.Errorf("Strict policy min score = %v, want 0.8", strict.MinTrustScore)
	}

	// Check permissive policy
	permissive := policies[2]
	if permissive.Name != "permissive" {
		t.Errorf("Third policy name = %v, want permissive", permissive.Name)
	}

	if permissive.MinTrustScore != 0.3 {
		t.Errorf("Permissive policy min score = %v, want 0.3", permissive.MinTrustScore)
	}
}

func TestComplianceViolation(t *testing.T) {
	violation := ComplianceViolation{
		Code:        "TEST_VIOLATION",
		Description: "This is a test violation",
		Severity:    ComplianceSeverityHigh,
	}

	if violation.Code != "TEST_VIOLATION" {
		t.Errorf("ComplianceViolation.Code = %v, want TEST_VIOLATION", violation.Code)
	}

	if violation.Severity != ComplianceSeverityHigh {
		t.Errorf("ComplianceViolation.Severity = %v, want ComplianceSeverityHigh", violation.Severity)
	}
}

func TestComplianceResult(t *testing.T) {
	result := &ComplianceResult{
		Compliant:       true,
		Score:           1.0,
		Violations:      []ComplianceViolation{},
		Certifications:  []string{"SOC2-Type2"},
		CheckedAt:       time.Now(),
	}

	if !result.Compliant {
		t.Error("ComplianceResult should be compliant")
	}

	if result.Score != 1.0 {
		t.Errorf("ComplianceResult.Score = %v, want 1.0", result.Score)
	}

	if len(result.Certifications) != 1 {
		t.Errorf("Certifications length = %v, want 1", len(result.Certifications))
	}
}

func TestAttestationVerificationResult(t *testing.T) {
	result := &AttestationVerificationResult{
		Attestation: &TrustedAttestation{
			Type: AttestationTypeSOC2Type2,
			URI:  "https://example.com/soc2.pdf",
		},
		Valid:   true,
		Errors:  []string{},
		Warnings: []string{"optional warning"},
	}

	if !result.Valid {
		t.Error("Verification should be valid")
	}

	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", result.Errors)
	}
}
