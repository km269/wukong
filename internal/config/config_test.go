package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestNewLoader_EnvVarExpansion verifies that ${ENV} references in
// secret fields are expanded during Load.
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

// TestNewLoader_ServerAuthEnvExpansion verifies that the nested
// server endpoint auth keys (acp_server.security.auth.api_key and
// mcp_server.security.auth.api_key) are env-expanded. The MCP path
// was historically missing from expandSecrets, silently leaving
// ${MCP_API_KEY} literals in the auth key.
func TestNewLoader_ServerAuthEnvExpansion(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	os.Setenv("TEST_ACP_KEY", "acp-key-value")
	os.Setenv("TEST_MCP_KEY", "mcp-key-value")
	defer func() {
		os.Unsetenv("TEST_ACP_KEY")
		os.Unsetenv("TEST_MCP_KEY")
	}()

	yamlContent := `
acp_server:
  enabled: true
  security:
    auth:
      type: "api_key"
      api_key: ${TEST_ACP_KEY}
mcp_server:
  enabled: true
  security:
    auth:
      type: "api_key"
      api_key: ${TEST_MCP_KEY}
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

	if got := cfg.ACPServer.Security.Auth.APIKey; got != "acp-key-value" {
		t.Errorf("acp_server.security.auth.api_key: expected expanded env var, got %q", got)
	}
	if got := cfg.MCPServer.Security.Auth.APIKey; got != "mcp-key-value" {
		t.Errorf("mcp_server.security.auth.api_key: expected expanded env var, got %q", got)
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

// --- P3.3 configure --edit: round-trip serialization ---

func TestMarshalYAML_RoundTrip(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "lmstudio",
		Providers: []ProviderConfig{
			{
				Name:          "lmstudio",
				Type:          "openai",
				BaseURL:       "http://localhost:1234/v1",
				APIKey:        "${LMSTUDIO_KEY}",
				Model:         "qwen3",
				MCPPort:       "8080",
				ContextWindow: 32768,
			},
		},
		Extensions: []ExtensionConfig{
			{Name: "developer", Type: "builtin", Enabled: true},
		},
		Agent: AgentConfig{
			MaxLLMCalls:    42,
			MaxRunDuration: 90 * time.Second,
		},
		Security: SecurityConfig{BlockDangerousCommands: true},
	}

	data, err := MarshalYAML(cfg)
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}

	// Keys must be snake_case, and durations must be strings.
	out := string(data)
	for _, want := range []string{
		"default_provider: lmstudio",
		"max_llm_calls: 42",
		"max_run_duration: 1m30s",
		"base_url: http://localhost:1234/v1",
		"mcp_port: \"8080\"",
		"context_window: 32768",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("marshaled YAML missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DefaultProvider") {
		t.Errorf("marshaled YAML must not contain Go field names:\n%s", out)
	}
	if strings.Contains(out, "APIKey") {
		t.Errorf("marshaled YAML must not contain Go field names for APIKey:\n%s", out)
	}
}

// TestMarshalYAML_RoundTripLoad verifies a config marshaled by
// MarshalYAML loads back losslessly via NewLoader (the exact flow
// the configure wizard uses for write + read).
func TestMarshalYAML_RoundTripLoad(t *testing.T) {
	cfg := &WukongConfig{
		DefaultProvider: "lmstudio",
		Providers: []ProviderConfig{
			{
				Name:          "lmstudio",
				Type:          "openai",
				BaseURL:       "http://localhost:1234/v1",
				APIKey:        "sk-test",
				Model:         "qwen3",
				MCPPort:       "8090",
				ContextWindow: 16384,
			},
		},
		Agent: AgentConfig{
			MaxLLMCalls:    42,
			MaxRunDuration: 90 * time.Second,
		},
	}

	data, err := MarshalYAML(cfg)
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loader, err := NewLoader(configPath)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	loaded, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.DefaultProvider != "lmstudio" {
		t.Errorf("default provider = %q, want lmstudio", loaded.DefaultProvider)
	}
	p := loaded.FindProvider("lmstudio")
	if p == nil {
		t.Fatal("provider lmstudio missing after reload")
	}
	if p.MCPPort != "8090" {
		t.Errorf("mcp_port = %q, want 8090 (snake_case key must round-trip)", p.MCPPort)
	}
	if p.ContextWindow != 16384 {
		t.Errorf("context_window = %d, want 16384", p.ContextWindow)
	}
	if loaded.Agent.MaxLLMCalls != 42 {
		t.Errorf("agent.max_llm_calls = %d, want 42", loaded.Agent.MaxLLMCalls)
	}
	if loaded.Agent.MaxRunDuration != 90*time.Second {
		t.Errorf("agent.max_run_duration = %v, want 90s", loaded.Agent.MaxRunDuration)
	}
}

// TestLoadUnresolved_KeepsEnvRefs verifies the --edit baseline does
// not expand ${ENV} references.
func TestLoadUnresolved_KeepsEnvRefs(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	os.Setenv("TEST_KEY_EDIT", "expanded-secret")
	defer os.Unsetenv("TEST_KEY_EDIT")

	yamlContent := `
default_provider: lmstudio
providers:
  - name: lmstudio
    type: openai
    base_url: http://localhost:1234/v1
    api_key: ${TEST_KEY_EDIT}
    model: qwen3
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loader, err := NewLoader(configPath)
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	cfg, err := loader.LoadUnresolved()
	if err != nil {
		t.Fatalf("LoadUnresolved: %v", err)
	}

	p := cfg.FindProvider("lmstudio")
	if p == nil {
		t.Fatal("provider lmstudio missing")
	}
	if p.APIKey != "${TEST_KEY_EDIT}" {
		t.Errorf("api_key = %q, want unexpanded ${TEST_KEY_EDIT}", p.APIKey)
	}
}

// TestMarshalYAML_NilFields verifies nil slices/maps and pointer
// fields serialize as null and reload as zero/nil values.
func TestMarshalYAML_NilFields(t *testing.T) {
	cfg := &WukongConfig{}
	cfg.Providers = nil
	cfg.Extensions = nil

	data, err := MarshalYAML(cfg)
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if !strings.Contains(string(data), "providers: null") {
		t.Errorf("expected explicit null providers:\n%s", data)
	}
	if !strings.Contains(string(data), "extensions: null") {
		t.Errorf("expected explicit null extensions:\n%s", data)
	}
}
