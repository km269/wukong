package summon

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestGenerateAPIKey verifies API key generation produces valid keys.
func TestGenerateAPIKey(t *testing.T) {
	key, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	// Key should start with "wak_" prefix
	if len(key) < 4 || key[:4] != "wak_" {
		t.Errorf("API key should start with 'wak_', got: %s", key)
	}

	// Key should be 67 characters (4 prefix + 64 hex - 1)
	// Actually: "wak_" (4) + 64 hex chars = 68
	if len(key) != 68 {
		t.Errorf("expected API key length 68, got %d: %s",
			len(key), key)
	}

	// Generate another key and verify uniqueness
	key2, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey 2: %v", err)
	}
	if key == key2 {
		t.Error("two generated API keys should be different")
	}
}

// TestGenerateJWTSecret verifies JWT secret generation.
func TestGenerateJWTSecret(t *testing.T) {
	secret, err := GenerateJWTSecret()
	if err != nil {
		t.Fatalf("GenerateJWTSecret: %v", err)
	}

	// 64 bytes hex-encoded = 128 characters
	if len(secret) != 128 {
		t.Errorf("expected JWT secret length 128, got %d", len(secret))
	}

	// Verify uniqueness
	secret2, err := GenerateJWTSecret()
	if err != nil {
		t.Fatalf("GenerateJWTSecret 2: %v", err)
	}
	if secret == secret2 {
		t.Error("two generated JWT secrets should be different")
	}
}

// TestNewCredentialRotator verifies rotator creation with defaults.
func TestNewCredentialRotator(t *testing.T) {
	// Default interval
	r := NewCredentialRotator(0)
	if r == nil {
		t.Fatal("NewCredentialRotator returned nil")
	}
	if r.CredentialCount() != 0 {
		t.Errorf("expected 0 credentials, got %d", r.CredentialCount())
	}
	if r.IsRunning() {
		t.Error("rotator should not be running before Start")
	}

	// Custom interval
	r2 := NewCredentialRotator(1 * time.Hour)
	if r2.interval != 1*time.Hour {
		t.Errorf("expected interval 1h, got %v", r2.interval)
	}
}

// TestCredentialRotator_Register verifies credential registration.
func TestCredentialRotator_Register(t *testing.T) {
	r := NewCredentialRotator(1 * time.Hour)

	creds := CredentialSet{
		APIKey:       "test-key-123",
		APIKeyHeader: "X-API-Key",
	}

	err := r.Register(
		context.Background(), "agent1", "api_key", creds, nil,
	)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if r.CredentialCount() != 1 {
		t.Errorf("expected 1 credential, got %d", r.CredentialCount())
	}

	// Duplicate registration should fail
	err = r.Register(
		context.Background(), "agent1", "api_key", creds, nil,
	)
	if err == nil {
		t.Error("expected error for duplicate registration")
	}
}

// TestCredentialRotator_Unregister verifies credential removal.
func TestCredentialRotator_Unregister(t *testing.T) {
	r := NewCredentialRotator(1 * time.Hour)

	creds := CredentialSet{APIKey: "test-key"}
	r.Register(context.Background(), "agent1", "api_key", creds, nil)

	err := r.Unregister("agent1")
	if err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	if r.CredentialCount() != 0 {
		t.Errorf("expected 0 credentials after unregister, got %d",
			r.CredentialCount())
	}

	// Unregister non-existent should fail
	err = r.Unregister("nonexistent")
	if err == nil {
		t.Error("expected error for unregistering non-existent credential")
	}
}

// TestCredentialRotator_GetCredential verifies credential retrieval.
func TestCredentialRotator_GetCredential(t *testing.T) {
	r := NewCredentialRotator(1 * time.Hour)

	creds := CredentialSet{
		APIKey:       "my-api-key",
		APIKeyHeader: "X-Custom-Key",
	}
	r.Register(context.Background(), "agent1", "api_key", creds, nil)

	rc, err := r.GetCredential("agent1")
	if err != nil {
		t.Fatalf("GetCredential: %v", err)
	}
	if rc.Current.APIKey != "my-api-key" {
		t.Errorf("expected API key 'my-api-key', got %q",
			rc.Current.APIKey)
	}
	if rc.Current.APIKeyHeader != "X-Custom-Key" {
		t.Errorf("expected header 'X-Custom-Key', got %q",
			rc.Current.APIKeyHeader)
	}

	// Non-existent credential
	_, err = r.GetCredential("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent credential")
	}
}

// TestCredentialRotator_GetAuthConfig verifies auth config conversion.
func TestCredentialRotator_GetAuthConfig(t *testing.T) {
	r := NewCredentialRotator(1 * time.Hour)

	// API key auth
	creds := CredentialSet{
		APIKey:       "key123",
		APIKeyHeader: "X-API-Key",
	}
	r.Register(context.Background(), "agent1", "api_key", creds, nil)

	auth, err := r.GetAuthConfig("agent1")
	if err != nil {
		t.Fatalf("GetAuthConfig: %v", err)
	}
	if auth.Type != "api_key" {
		t.Errorf("expected auth type 'api_key', got %q", auth.Type)
	}
	if auth.APIKey != "key123" {
		t.Errorf("expected API key 'key123', got %q", auth.APIKey)
	}

	// JWT auth
	jwtCreds := CredentialSet{
		JWTSecret:   "secret123",
		JWTAudience: "aud",
		JWTIssuer:   "iss",
	}
	r.Register(context.Background(), "agent2", "jwt", jwtCreds, nil)

	jwtAuth, err := r.GetAuthConfig("agent2")
	if err != nil {
		t.Fatalf("GetAuthConfig jwt: %v", err)
	}
	if jwtAuth.JWTSecret != "secret123" {
		t.Errorf("expected JWT secret 'secret123', got %q",
			jwtAuth.JWTSecret)
	}

	// OAuth2 auth
	oauthCreds := CredentialSet{
		OAuthTokenURL:     "https://auth.example.com/token",
		OAuthClientID:     "client1",
		OAuthClientSecret: "secret1",
	}
	r.Register(context.Background(), "agent3", "oauth2", oauthCreds, nil)

	oauthAuth, err := r.GetAuthConfig("agent3")
	if err != nil {
		t.Fatalf("GetAuthConfig oauth2: %v", err)
	}
	if oauthAuth.OAuthClientID != "client1" {
		t.Errorf("expected client ID 'client1', got %q",
			oauthAuth.OAuthClientID)
	}
}

// TestCredentialRotator_RotateNow verifies immediate rotation.
func TestCredentialRotator_RotateNow(t *testing.T) {
	r := NewCredentialRotator(1 * time.Hour)

	initialCreds := CredentialSet{
		APIKey:       "old-key",
		APIKeyHeader: "X-API-Key",
	}
	r.Register(context.Background(), "agent1", "api_key", initialCreds, nil)

	// Custom generator that returns a fixed new key
	genCallCount := 0
	var genMu sync.Mutex
	customGen := func(ctx context.Context, authType string) (CredentialSet, error) {
		genMu.Lock()
		genCallCount++
		genMu.Unlock()
		return CredentialSet{
			APIKey:       "new-key",
			APIKeyHeader: "X-API-Key",
		}, nil
	}

	results, err := r.RotateNow(context.Background(), "agent1", customGen)
	if err != nil {
		t.Fatalf("RotateNow: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Success {
		t.Errorf("rotation should succeed, got error: %v",
			results[0].Error)
	}

	// Verify credential was updated
	rc, err := r.GetCredential("agent1")
	if err != nil {
		t.Fatalf("GetCredential after rotation: %v", err)
	}
	if rc.Current.APIKey != "old-key" {
		t.Errorf("expected current API key 'old-key', got %q",
			rc.Current.APIKey)
	}
	if rc.Pending.APIKey != "new-key" {
		t.Errorf("expected pending API key 'new-key', got %q",
			rc.Pending.APIKey)
	}
	if rc.RotationCount != 1 {
		t.Errorf("expected rotation count 1, got %d",
			rc.RotationCount)
	}
}

// TestCredentialRotator_RotateAll verifies rotating all credentials.
func TestCredentialRotator_RotateAll(t *testing.T) {
	r := NewCredentialRotator(1 * time.Hour)

	r.Register(context.Background(), "agent1", "api_key",
		CredentialSet{APIKey: "key1"}, nil)
	r.Register(context.Background(), "agent2", "jwt",
		CredentialSet{JWTSecret: "secret1"}, nil)

	gen := func(ctx context.Context, authType string) (CredentialSet, error) {
		if authType == "api_key" {
			return CredentialSet{APIKey: "rotated-key"}, nil
		}
		return CredentialSet{JWTSecret: "rotated-secret"}, nil
	}

	results, err := r.RotateNow(context.Background(), "", gen)
	if err != nil {
		t.Fatalf("RotateNow all: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, result := range results {
		if !result.Success {
			t.Errorf("rotation for %q should succeed: %v",
				result.Name, result.Error)
		}
	}
}

// TestCredentialRotator_StartStop verifies the background loop lifecycle.
func TestCredentialRotator_StartStop(t *testing.T) {
	r := NewCredentialRotator(100 * time.Millisecond)

	creds := CredentialSet{APIKey: "initial-key"}
	r.Register(context.Background(), "agent1", "api_key", creds, nil)

	rotationCount := 0
	var mu sync.Mutex
	gen := func(ctx context.Context, authType string) (CredentialSet, error) {
		mu.Lock()
		rotationCount++
		mu.Unlock()
		return CredentialSet{APIKey: "rotated-key"}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.Start(ctx, gen)

	if !r.IsRunning() {
		t.Error("rotator should be running after Start")
	}

	// Wait for at least one rotation
	time.Sleep(250 * time.Millisecond)

	r.Stop()

	if r.IsRunning() {
		t.Error("rotator should not be running after Stop")
	}

	mu.Lock()
	count := rotationCount
	mu.Unlock()
	if count < 1 {
		t.Error("expected at least 1 rotation during test period")
	}
}

// TestCredentialRotator_StartIdempotent verifies Start is idempotent.
func TestCredentialRotator_StartIdempotent(t *testing.T) {
	r := NewCredentialRotator(1 * time.Hour)
	creds := CredentialSet{APIKey: "key"}
	r.Register(context.Background(), "agent1", "api_key", creds, nil)

	ctx := context.Background()
	r.Start(ctx, nil)
	r.Start(ctx, nil) // Second call should be no-op

	if !r.IsRunning() {
		t.Error("rotator should be running")
	}

	r.Stop()
}

// TestDefaultCredentialGenerator verifies the built-in generator.
func TestDefaultCredentialGenerator(t *testing.T) {
	tests := []struct {
		name      string
		authType  string
		expectErr bool
	}{
		{"api_key", "api_key", false},
		{"jwt", "jwt", false},
		{"oauth2", "oauth2", true},
		{"unsupported", "unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := DefaultCredentialGenerator(
				context.Background(), tt.authType,
			)
			if tt.expectErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.expectErr {
				switch tt.authType {
				case "api_key":
					if creds.APIKey == "" {
						t.Error("expected non-empty API key")
					}
				case "jwt":
					if creds.JWTSecret == "" {
						t.Error("expected non-empty JWT secret")
					}
				}
			}
		})
	}
}

// TestCredentialRotator_ListCredentials verifies credential listing.
func TestCredentialRotator_ListCredentials(t *testing.T) {
	r := NewCredentialRotator(1 * time.Hour)

	r.Register(context.Background(), "agent1", "api_key",
		CredentialSet{APIKey: "k1"}, nil)
	r.Register(context.Background(), "agent2", "jwt",
		CredentialSet{JWTSecret: "s1"}, nil)

	list := r.ListCredentials()
	if len(list) != 2 {
		t.Errorf("expected 2 credentials, got %d", len(list))
	}

	names := make(map[string]bool)
	for _, rc := range list {
		names[rc.Name] = true
	}
	if !names["agent1"] || !names["agent2"] {
		t.Error("expected both agent1 and agent2 in list")
	}
}

// TestIsEmptyCredentialSet verifies empty check logic.
func TestIsEmptyCredentialSet(t *testing.T) {
	if !isEmptyCredentialSet(CredentialSet{}) {
		t.Error("empty set should return true")
	}
	if isEmptyCredentialSet(CredentialSet{APIKey: "x"}) {
		t.Error("set with API key should return false")
	}
	if isEmptyCredentialSet(CredentialSet{JWTSecret: "x"}) {
		t.Error("set with JWT secret should return false")
	}
	if isEmptyCredentialSet(CredentialSet{OAuthClientID: "x"}) {
		t.Error("set with OAuth client ID should return false")
	}
}
