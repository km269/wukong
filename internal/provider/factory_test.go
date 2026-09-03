package provider

import (
	"testing"

	"github.com/km269/wukong/internal/config"
)

func TestNewFactory(t *testing.T) {
	cfg := &config.WukongConfig{
		DefaultProvider: "test",
		Providers: []config.ProviderConfig{
			{
				Name:    "test",
				Type:    "openai",
				BaseURL: "http://localhost:8080/v1",
				APIKey:  "test-key",
				Model:   "gpt-4o",
			},
		},
	}

	f := NewFactory(cfg)
	if f == nil {
		t.Fatal("expected non-nil factory")
	}
	if f.cfg != cfg {
		t.Error("factory should hold reference to config")
	}
}

func TestCreateModel_NotFound(t *testing.T) {
	cfg := &config.WukongConfig{
		DefaultProvider: "default",
	}
	f := NewFactory(cfg)

	// No providers and no default - should fail
	_, err := f.CreateModel("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent provider")
	}
}

func TestCreateModel_DefaultProvider(t *testing.T) {
	cfg := &config.WukongConfig{
		DefaultProvider: "default",
		Providers: []config.ProviderConfig{
			{
				Name:    "default",
				Type:    "openai",
				BaseURL: "http://localhost:8080/v1",
				APIKey:  "test-key",
				Model:   "gpt-4o",
			},
		},
	}
	f := NewFactory(cfg)

	// Empty name should use default
	mdl, err := f.CreateModel("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatal("expected non-nil model")
	}
}

func TestCreateModel_UnsupportedType(t *testing.T) {
	cfg := &config.WukongConfig{
		Providers: []config.ProviderConfig{
			{
				Name:  "bad",
				Type:  "unsupported",
				Model: "some-model",
			},
		},
	}
	f := NewFactory(cfg)

	_, err := f.CreateModel("bad")
	if err == nil {
		t.Error("expected error for unsupported provider type")
	}
}

func TestCreateDefaultModel(t *testing.T) {
	cfg := &config.WukongConfig{
		DefaultProvider: "default",
		Providers: []config.ProviderConfig{
			{
				Name:    "default",
				Type:    "openai",
				BaseURL: "http://localhost:8080/v1",
				APIKey:  "test-key",
				Model:   "gpt-4o",
			},
		},
	}
	f := NewFactory(cfg)

	mdl, err := f.CreateDefaultModel()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mdl == nil {
		t.Fatal("expected non-nil default model")
	}
}

func TestGetDefaultGenerationConfig(t *testing.T) {
	cfg := &config.AgentConfig{
		Streaming:   true,
		MaxTokens:   4096,
		Temperature: 0.7,
	}

	gc := GetDefaultGenerationConfig(cfg)
	if !gc.Stream {
		t.Error("expected streaming to be true")
	}
	if gc.MaxTokens == nil || *gc.MaxTokens != 4096 {
		t.Error("expected MaxTokens to be 4096")
	}
	if gc.Temperature == nil || *gc.Temperature != 0.7 {
		t.Error("expected Temperature to be 0.7")
	}
}

func TestGetDefaultGenerationConfig_ZeroValues(t *testing.T) {
	cfg := &config.AgentConfig{
		Streaming:   false,
		MaxTokens:   0,   // zero means not set
		Temperature: 0.0, // zero means not set
	}

	gc := GetDefaultGenerationConfig(cfg)
	if gc.Stream {
		t.Error("expected streaming to be false")
	}
	if gc.MaxTokens != nil {
		t.Error("expected MaxTokens to be nil when zero")
	}
	if gc.Temperature != nil {
		t.Error("expected Temperature to be nil when zero")
	}
}

func TestCreateModel_GeminiAlias(t *testing.T) {
	cfg := &config.WukongConfig{
		DefaultProvider: "g",
		Providers: []config.ProviderConfig{
			{Name: "g", Type: "gemini", APIKey: "k", Model: "gemini-2.0-flash"},
		},
	}
	f := NewFactory(cfg)

	// gemini resolves and inherits the Google default base URL.
	mdl, err := f.CreateModel("g")
	if err != nil || mdl == nil {
		t.Fatalf("CreateModel(gemini) = %v", err)
	}
	if cfg.Providers[0].BaseURL != GoogleBaseURL {
		t.Errorf("BaseURL = %q, want %q",
			cfg.Providers[0].BaseURL, GoogleBaseURL)
	}

	// Effective context window matches the google family default.
	if got := cfg.Providers[0].EffectiveContextWindow(); got != 32000 {
		t.Errorf("EffectiveContextWindow = %d, want 32000", got)
	}
}

func TestCreateModel_DeepSeekVariantAndEscapeHatches(t *testing.T) {
	cfg := &config.WukongConfig{
		DefaultProvider: "ds",
		Providers: []config.ProviderConfig{
			{
				Name:         "ds",
				Type:         "deepseek",
				APIKey:       "k",
				Model:        "deepseek-reasoner",
				ExtraHeaders: map[string]string{"X-Trace": "abc"},
				ExtraFields:  map[string]interface{}{"temperature_top": 0.5},
			},
		},
	}
	f := NewFactory(cfg)

	// DeepSeek gets its default base URL when unset; the model must
	// construct with the framework's DeepSeek variant and the
	// extra_headers / extra_fields escape hatches applied.
	mdl, err := f.CreateModel("ds")
	if err != nil || mdl == nil {
		t.Fatalf("CreateModel(deepseek) = %v", err)
	}
	if cfg.Providers[0].BaseURL != DeepSeekBaseURL {
		t.Errorf("BaseURL = %q, want %q",
			cfg.Providers[0].BaseURL, DeepSeekBaseURL)
	}
}

func TestValidateGeminiType(t *testing.T) {
	cfg := &config.WukongConfig{
		DefaultProvider: "g",
		Providers: []config.ProviderConfig{
			{Name: "g", Type: "gemini", Model: "gemini-2.0-flash"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate(gemini) = %v, want valid", err)
	}
}
