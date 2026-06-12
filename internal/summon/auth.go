// Package summon provides A2A credential rotation and lifecycle management.
// Credentials for remote A2A agents can be rotated automatically on a
// configurable schedule, reducing the risk of credential compromise.
package summon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/km269/wukong/internal/util"
)

// CredentialRotator manages automatic rotation of A2A credentials.
// It supports API key rotation, JWT secret rotation, and OAuth2 token
// refresh on a configurable interval.
type CredentialRotator struct {
	mu       sync.RWMutex
	configs  map[string]*RotatingCredential
	stopCh   chan struct{}
	interval time.Duration
	running  bool
}

// RotatingCredential holds the current and pending credentials for
// a single A2A remote agent, along with rotation metadata.
type RotatingCredential struct {
	// Name identifies the A2A remote agent.
	Name string

	// AuthType is the authentication type: jwt, api_key, oauth2.
	AuthType string

	// Current credentials (active)
	Current CredentialSet

	// Pending credentials (will become active at NextRotation)
	Pending CredentialSet

	// Rotation metadata
	LastRotation   time.Time
	NextRotation   time.Time
	RotationCount  int
	RotationErrors int
}

// CredentialSet holds the full set of auth credentials.
type CredentialSet struct {
	APIKey            string
	APIKeyHeader      string
	JWTSecret         string
	JWTAudience       string
	JWTIssuer         string
	OAuthTokenURL     string
	OAuthClientID     string
	OAuthClientSecret string
	OAuthAccessToken  string
	OAuthRefreshToken string
	OAuthExpiresAt    time.Time
}

// RotationResult contains the outcome of a credential rotation.
type RotationResult struct {
	Name      string
	Success   bool
	Error     error
	Timestamp time.Time
}

// CredentialGenerator is a function type that generates new credentials.
type CredentialGenerator func(ctx context.Context, authType string) (CredentialSet, error)

// NewCredentialRotator creates a new credential rotator with the
// specified rotation interval. If interval is zero, a default of
// 24 hours is used.
func NewCredentialRotator(interval time.Duration) *CredentialRotator {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	return &CredentialRotator{
		configs:  make(map[string]*RotatingCredential),
		stopCh:   make(chan struct{}),
		interval: interval,
	}
}

// Register adds a credential to the rotation schedule.
// The generator function is called to produce new credentials when
// rotation is triggered.
func (r *CredentialRotator) Register(
	ctx context.Context,
	name string,
	authType string,
	initial CredentialSet,
	generator CredentialGenerator,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.configs[name]; exists {
		return fmt.Errorf(
			"credential %q is already registered for rotation", name)
	}

	now := time.Now()
	rc := &RotatingCredential{
		Name:          name,
		AuthType:      authType,
		Current:       initial,
		LastRotation:  now,
		NextRotation:  now.Add(r.interval),
		RotationCount: 0,
	}

	// Generate initial pending credential if generator is provided
	if generator != nil {
		pending, err := generator(ctx, authType)
		if err != nil {
			util.Logger.Warn("failed to generate initial pending credential",
				slog.String("name", name),
				slog.String("error", err.Error()))
		} else {
			rc.Pending = pending
		}
	}

	r.configs[name] = rc
	util.Logger.Info("credential registered for rotation",
		slog.String("name", name),
		slog.String("auth_type", authType),
		slog.String("next_rotation", rc.NextRotation.Format(time.RFC3339)),
	)
	return nil
}

// Unregister removes a credential from the rotation schedule.
func (r *CredentialRotator) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.configs[name]; !exists {
		return fmt.Errorf(
			"credential %q is not registered for rotation", name)
	}
	delete(r.configs, name)
	util.Logger.Info("credential unregistered from rotation",
		slog.String("name", name),
	)
	return nil
}

// RotateNow immediately rotates credentials for the specified agent.
// If name is empty, all registered credentials are rotated.
func (r *CredentialRotator) RotateNow(
	ctx context.Context,
	name string,
	generator CredentialGenerator,
) ([]RotationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var results []RotationResult

	rotate := func(rc *RotatingCredential) {
		result := RotationResult{
			Name:      rc.Name,
			Timestamp: time.Now(),
		}

		if generator == nil {
			result.Error = fmt.Errorf(
				"no credential generator provided for %q", rc.Name)
			result.Success = false
			results = append(results, result)
			return
		}

		newCreds, err := generator(ctx, rc.AuthType)
		if err != nil {
			result.Error = fmt.Errorf(
				"generate credentials for %q: %w", rc.Name, err)
			result.Success = false
			rc.RotationErrors++
			results = append(results, result)
			return
		}

		// Rotate: pending becomes current, generate new pending
		rc.Current = rc.Pending
		if isEmptyCredentialSet(rc.Current) {
			rc.Current = newCreds
		}
		rc.Pending = newCreds
		rc.LastRotation = time.Now()
		rc.NextRotation = time.Now().Add(r.interval)
		rc.RotationCount++
		result.Success = true

		util.Logger.Info("credential rotated",
			slog.String("name", rc.Name),
			slog.Int("rotation_count", rc.RotationCount),
			slog.String("next_rotation",
				rc.NextRotation.Format(time.RFC3339)),
		)

		results = append(results, result)
	}

	if name != "" {
		rc, ok := r.configs[name]
		if !ok {
			return nil, fmt.Errorf(
				"credential %q not found for rotation", name)
		}
		rotate(rc)
	} else {
		for _, rc := range r.configs {
			rotate(rc)
		}
	}

	return results, nil
}

// Start begins the background rotation loop. Credentials are
// automatically rotated at the configured interval.
func (r *CredentialRotator) Start(
	ctx context.Context,
	generator CredentialGenerator,
) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()

	go r.rotationLoop(ctx, generator)
	util.Logger.Info("credential rotation loop started",
		slog.String("interval", r.interval.String()),
	)
}

// Stop gracefully stops the background rotation loop.
func (r *CredentialRotator) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return
	}
	r.running = false
	close(r.stopCh)
	util.Logger.Info("credential rotation loop stopped")
}

// rotationLoop is the background goroutine that triggers periodic
// credential rotation.
func (r *CredentialRotator) rotationLoop(
	ctx context.Context,
	generator CredentialGenerator,
) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.performRotation(ctx, generator)
		}
	}
}

// performRotation rotates all credentials that are due.
func (r *CredentialRotator) performRotation(
	ctx context.Context,
	generator CredentialGenerator,
) {
	r.mu.Lock()
	now := time.Now()
	var due []*RotatingCredential
	for _, rc := range r.configs {
		if now.After(rc.NextRotation) || now.Equal(rc.NextRotation) {
			due = append(due, rc)
		}
	}
	r.mu.Unlock()

	for _, rc := range due {
		_, err := r.RotateNow(ctx, rc.Name, generator)
		if err != nil {
			util.Logger.Warn("scheduled credential rotation failed",
				slog.String("name", rc.Name),
				slog.String("error", err.Error()),
			)
		}
	}
}

// GetCredential returns the current credential set for an agent.
func (r *CredentialRotator) GetCredential(
	name string,
) (*RotatingCredential, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rc, ok := r.configs[name]
	if !ok {
		return nil, fmt.Errorf(
			"credential %q not registered", name)
	}
	return rc, nil
}

// GetAuthConfig returns the current A2A AuthConfig from the
// credential rotator, suitable for use with NewA2AClient.
func (r *CredentialRotator) GetAuthConfig(
	name string,
) (*AuthConfig, error) {
	rc, err := r.GetCredential(name)
	if err != nil {
		return nil, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	auth := &AuthConfig{
		Type: rc.AuthType,
	}
	switch rc.AuthType {
	case "api_key":
		auth.APIKey = rc.Current.APIKey
		auth.APIKeyHeader = rc.Current.APIKeyHeader
		if auth.APIKeyHeader == "" {
			auth.APIKeyHeader = "X-API-Key"
		}
	case "jwt":
		auth.JWTSecret = rc.Current.JWTSecret
		auth.JWTAudience = rc.Current.JWTAudience
		auth.JWTIssuer = rc.Current.JWTIssuer
	case "oauth2":
		auth.OAuthTokenURL = rc.Current.OAuthTokenURL
		auth.OAuthClientID = rc.Current.OAuthClientID
		auth.OAuthClientSecret = rc.Current.OAuthClientSecret
	}
	return auth, nil
}

// ListCredentials returns all registered rotating credentials.
func (r *CredentialRotator) ListCredentials() []*RotatingCredential {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*RotatingCredential, 0, len(r.configs))
	for _, rc := range r.configs {
		result = append(result, rc)
	}
	return result
}

// CredentialCount returns the number of registered credentials.
func (r *CredentialRotator) CredentialCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.configs)
}

// IsRunning returns whether the background rotation loop is active.
func (r *CredentialRotator) IsRunning() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.running
}

// GenerateAPIKey creates a cryptographically random API key.
// The key is 32 bytes, hex-encoded for a 64-character string.
func GenerateAPIKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate API key: %w", err)
	}
	return "wak_" + hex.EncodeToString(bytes), nil
}

// GenerateJWTSecret creates a cryptographically random JWT secret.
// The secret is 64 bytes, hex-encoded for a 128-character string.
func GenerateJWTSecret() (string, error) {
	bytes := make([]byte, 64)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate JWT secret: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// DefaultCredentialGenerator is a built-in generator that creates
// random credentials for the specified auth type.
func DefaultCredentialGenerator(
	ctx context.Context, authType string,
) (CredentialSet, error) {
	switch authType {
	case "api_key":
		key, err := GenerateAPIKey()
		if err != nil {
			return CredentialSet{}, err
		}
		return CredentialSet{
			APIKey:       key,
			APIKeyHeader: "X-API-Key",
		}, nil
	case "jwt":
		secret, err := GenerateJWTSecret()
		if err != nil {
			return CredentialSet{}, err
		}
		return CredentialSet{
			JWTSecret:   secret,
			JWTAudience: "wukong-a2a",
			JWTIssuer:   "wukong",
		}, nil
	case "oauth2":
		// OAuth2 credentials cannot be auto-generated; return empty
		// to signal that manual rotation is required.
		return CredentialSet{}, fmt.Errorf(
			"oauth2 credentials require manual rotation")
	default:
		return CredentialSet{}, fmt.Errorf(
			"unsupported auth type for auto-generation: %s", authType)
	}
}

// isEmptyCredentialSet checks if a credential set has no values.
func isEmptyCredentialSet(cs CredentialSet) bool {
	return cs.APIKey == "" &&
		cs.JWTSecret == "" &&
		cs.OAuthClientID == "" &&
		cs.OAuthAccessToken == ""
}
