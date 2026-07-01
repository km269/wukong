package config

import (
	"os"
	"path/filepath"
	"testing"
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
