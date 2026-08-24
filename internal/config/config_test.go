package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/km269/wukong/internal/gateway"
)

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		isAbs bool
	}{
		{"absolute path", "/tmp/test.db", true},
		{"relative path", "test.db", false},
		{"empty path", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResolvePath(tt.input)
			if tt.isAbs {
				if !filepath.IsAbs(result) {
					t.Errorf(
						"expected absolute path, got %q",
						result,
					)
				}
			} else {
				if result == "" {
					t.Error("expected non-empty result")
				}
			}
		})
	}
}

func TestWukongConfig_FindProvider(t *testing.T) {
	cfg := &WukongConfig{
		Providers: []ProviderConfig{
			{Name: "openai", Model: "gpt-4o"},
			{Name: "ollama", Model: "llama3"},
		},
	}

	tests := []struct {
		name      string
		query     string
		wantFound bool
	}{
		{"existing provider", "openai", true},
		{"another existing", "ollama", true},
		{"non-existent", "deepseek", false},
		{"empty name", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cfg.FindProvider(tt.query)
			if tt.wantFound && result == nil {
				t.Errorf(
					"expected to find provider %q",
					tt.query,
				)
			}
			if !tt.wantFound && result != nil {
				t.Errorf(
					"expected nil for provider %q",
					tt.query,
				)
			}
		})
	}
}

func TestWukongConfig_DefaultProviderConfig(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "ollama",
		Providers: []ProviderConfig{
			{Name: "openai", Model: "gpt-4o"},
			{Name: "ollama", Model: "llama3"},
		},
	}

	result := cfg.DefaultProviderConfig()
	if result == nil {
		t.Fatal("expected non-nil default provider")
	}
	if result.Name != "ollama" {
		t.Errorf(
			"expected ollama, got %q", result.Name,
		)
	}

	// Missing default provider returns nil
	cfg2 := &WukongConfig{DefaultProvider: "nonexistent"}
	if result := cfg2.DefaultProviderConfig(); result != nil {
		t.Error("expected nil for non-existent default provider")
	}
}

func TestWukongConfig_EnabledExtensions(t *testing.T) {
	cfg := &WukongConfig{
		Extensions: []ExtensionConfig{
			{Name: "dev", Enabled: true},
			{Name: "web", Enabled: false},
			{Name: "mem", Enabled: true},
		},
	}

	result := cfg.EnabledExtensions()
	if len(result) != 2 {
		t.Errorf(
			"expected 2 enabled extensions, got %d",
			len(result),
		)
	}

	names := make(map[string]bool)
	for _, ext := range result {
		names[ext.Name] = true
	}
	if !names["dev"] || !names["mem"] {
		t.Error("expected dev and mem to be enabled")
	}
}

func TestWukongConfig_FindExtension(t *testing.T) {
	cfg := &WukongConfig{
		Extensions: []ExtensionConfig{
			{Name: "developer", Type: "builtin"},
			{Name: "filesystem", Type: "external"},
		},
	}

	tests := []struct {
		name      string
		query     string
		wantFound bool
		wantType  string
	}{
		{"builtin", "developer", true, "builtin"},
		{"external", "filesystem", true, "external"},
		{"missing", "nonexistent", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cfg.FindExtension(tt.query)
			if tt.wantFound {
				if result == nil {
					t.Fatalf(
						"expected to find extension %q",
						tt.query,
					)
				}
				if result.Type != tt.wantType {
					t.Errorf(
						"expected type %q, got %q",
						tt.wantType, result.Type,
					)
				}
			} else if result != nil {
				t.Errorf(
					"expected nil for extension %q",
					tt.query,
				)
			}
		})
	}
}

func TestNewLoader_Defaults(t *testing.T) {
	// Create a temp directory with an empty config to avoid
	// picking up the project's real config.yaml while still
	// verifying defaults.
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Write minimal config with only provider info so
	// other sections fall back to defaults.
	yamlContent := `
default_provider: test
providers:
  - name: test
    type: openai
    base_url: http://localhost:8080/v1
    api_key: key
    model: gpt-4o
`
	if err := os.WriteFile(
		configPath, []byte(yamlContent), 0644,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loader, err := NewLoader(configPath)
	if err != nil {
		t.Fatalf("NewLoader failed: %v", err)
	}

	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify default values
	if cfg.Session.Backend != "sqlite" {
		t.Errorf(
			"expected sqlite session backend, got %q",
			cfg.Session.Backend,
		)
	}
	if cfg.Session.EventLimit != 500 {
		t.Errorf(
			"expected 500 event limit, got %d",
			cfg.Session.EventLimit,
		)
	}
	if cfg.Agent.MaxLLMCalls != 50 {
		t.Errorf(
			"expected 50 max LLM calls, got %d",
			cfg.Agent.MaxLLMCalls,
		)
	}
	if cfg.Agent.Temperature != 0.7 {
		t.Errorf(
			"expected 0.7 temperature, got %f",
			cfg.Agent.Temperature,
		)
	}
	if !cfg.Memory.AutoExtract {
		t.Error("expected auto_extract to be true")
	}
}

func TestNewLoader_WithConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
default_provider: test-provider
providers:
  - name: test-provider
    type: openai
    base_url: http://localhost:8080/v1
    api_key: test-key
    model: test-model
session:
  backend: memory
  event_limit: 100
`
	if err := os.WriteFile(
		configPath, []byte(yamlContent), 0644,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loader, err := NewLoader(configPath)
	if err != nil {
		t.Fatalf("NewLoader failed: %v", err)
	}

	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.DefaultProvider != "test-provider" {
		t.Errorf(
			"expected test-provider, got %q",
			cfg.DefaultProvider,
		)
	}
	if cfg.Session.Backend != "memory" {
		t.Errorf(
			"expected memory backend, got %q",
			cfg.Session.Backend,
		)
	}
	if cfg.Session.EventLimit != 100 {
		t.Errorf(
			"expected 100 event limit, got %d",
			cfg.Session.EventLimit,
		)
	}

	provider := cfg.FindProvider("test-provider")
	if provider == nil {
		t.Fatal("expected to find test-provider")
	}
	if provider.Model != "test-model" {
		t.Errorf(
			"expected test-model, got %q",
			provider.Model,
		)
	}
}

func TestNewLoader_EnvVarExpansion(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	os.Setenv("TEST_API_KEY", "expanded-key-value")
	defer os.Unsetenv("TEST_API_KEY")

	yamlContent := `
providers:
  - name: test
    type: openai
    base_url: http://localhost:8080/v1
    api_key: ${TEST_API_KEY}
    model: gpt-4o
`
	if err := os.WriteFile(
		configPath, []byte(yamlContent), 0644,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loader, err := NewLoader(configPath)
	if err != nil {
		t.Fatalf("NewLoader failed: %v", err)
	}

	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	provider := cfg.FindProvider("test")
	if provider == nil {
		t.Fatal("expected to find test provider")
	}
	if provider.APIKey != "expanded-key-value" {
		t.Errorf(
			"expected expanded env var, got %q",
			provider.APIKey,
		)
	}
}

func TestLoader_GetConfig(t *testing.T) {
	loader, err := NewLoader("")
	if err != nil {
		t.Fatalf("NewLoader failed: %v", err)
	}

	// Before Load, GetConfig returns nil
	if cfg := loader.GetConfig(); cfg != nil {
		t.Error("expected nil before Load")
	}

	_, err = loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// After Load, GetConfig returns the config
	if cfg := loader.GetConfig(); cfg == nil {
		t.Error("expected non-nil after Load")
	}
}

// TestValidate_DefaultProviderMissing verifies that a default_provider
// that does not match any configured provider is a fatal error.
func TestValidate_DefaultProviderMissing(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "nonexistent",
		Providers:       []ProviderConfig{{Name: "openai"}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing default provider")
	}
}

// TestValidate_DefaultProviderFound verifies that a matching provider
// passes validation.
func TestValidate_DefaultProviderFound(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "openai",
		Providers:       []ProviderConfig{{Name: "openai"}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

// TestValidate_TemperatureOutOfRange verifies the temperature range
// guard. Temperature must be within [0.0, 2.0].
func TestValidate_TemperatureOutOfRange(t *testing.T) {
	for _, temp := range []float64{-0.1, 2.1, 10.0} {
		cfg := &WukongConfig{
			Agent: AgentConfig{Temperature: temp},
		}
		if err := cfg.Validate(); err == nil {
			t.Errorf("expected error for temperature %v, got nil", temp)
		}
	}
	// Boundary values should pass.
	for _, temp := range []float64{0.0, 1.0, 2.0} {
		cfg := &WukongConfig{
			Agent: AgentConfig{Temperature: temp},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("expected no error for temperature %v, got %v", temp, err)
		}
	}
}

// TestValidate_BadPermissionMode verifies that an unknown
// permission_mode is rejected, and recognized modes pass.
func TestValidate_BadPermissionMode(t *testing.T) {
	cfg := &WukongConfig{
		Security: SecurityConfig{PermissionMode: "bogus"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for bogus permission mode")
	}

	for _, mode := range []PermissionMode{
		PermissionAuto, PermissionSmart, PermissionManual, PermissionChatOnly,
	} {
		cfg := &WukongConfig{
			Security: SecurityConfig{PermissionMode: mode},
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("expected no error for mode %q, got %v", mode, err)
		}
	}
}

// TestValidate_NegativeMaxTokens verifies that negative max_tokens is
// rejected.
func TestValidate_NegativeMaxTokens(t *testing.T) {
	cfg := &WukongConfig{
		Agent: AgentConfig{MaxTokens: -1},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for negative max_tokens")
	}
}

// TestWarnings_NoProviders verifies a non-fatal warning is produced
// when no providers are configured.
func TestWarnings_NoProviders(t *testing.T) {
	cfg := &WukongConfig{}
	warnings := cfg.Warnings()
	found := false
	for _, w := range warnings {
		if contains(w, "no providers configured") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'no providers' warning, got %v", warnings)
	}
}

// TestWarnings_FeishuMissingAppID verifies a warning is produced when
// the Feishu channel is enabled without an app_id.
func TestWarnings_FeishuMissingAppID(t *testing.T) {
	cfg := &WukongConfig{
		Gateway: gateway.GatewayConfig{
			Enabled: true,
			Feishu:  gateway.FeishuChannelConfig{Enabled: true}, // AppID empty
		},
	}
	warnings := cfg.Warnings()
	found := false
	for _, w := range warnings {
		if contains(w, "app_id is empty") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected feishu app_id warning, got %v", warnings)
	}
}

// contains is a small helper wrapping strings.Contains for readability
// in the warning-assertion tests above.
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// TestValidateURLField verifies the URL sanity-check helper used by
// Warnings() to catch malformed URLs in enabled subsystems.
func TestValidateURLField(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"empty allowed", "", false},
		{"valid https", "https://api.example.com/v1", false},
		{"valid redis", "redis://localhost:6379/0", false},
		{"missing scheme", "api.example.com/v1", true},
		{"missing host", "https:///v1", true},
		{"bare path", "/just/a/path", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := validateURLField(tt.raw, "test_field")
			if tt.wantErr && w == "" {
				t.Errorf("expected warning for %q, got none", tt.raw)
			}
			if !tt.wantErr && w != "" {
				t.Errorf("expected no warning for %q, got %q", tt.raw, w)
			}
		})
	}
}

// TestWarnings_MalformedRedisURL verifies a malformed redis_url produces
// a warning when set.
func TestWarnings_MalformedRedisURL(t *testing.T) {
	cfg := &WukongConfig{
		Session: SessionConfig{RedisURL: "not-a-url"},
	}
	warnings := cfg.Warnings()
	found := false
	for _, w := range warnings {
		if contains(w, "redis_url") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected redis_url warning for malformed URL, got %v", warnings)
	}
}
