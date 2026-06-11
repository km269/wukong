// Package provider provides a factory for creating LLM model instances
// based on configuration. It supports OpenAI-compatible APIs, Ollama,
// and other model providers.
package provider

import (
	"fmt"

	"github.com/km269/wukong/internal/config"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Factory creates model instances from provider configuration.
type Factory struct {
	cfg *config.WukongConfig
}

// NewFactory creates a new model provider factory.
func NewFactory(cfg *config.WukongConfig) *Factory {
	return &Factory{cfg: cfg}
}

// CreateModel creates a model instance for the given provider name.
// If name is empty, the default provider is used.
func (f *Factory) CreateModel(name string) (model.Model, error) {
	p := f.cfg.FindProvider(name)
	if p == nil {
		if name == "" {
			p = f.cfg.DefaultProviderConfig()
		}
		if p == nil {
			return nil, fmt.Errorf(
				"provider %q not found and no default configured",
				name,
			)
		}
	}

	switch p.Type {
	case "openai":
		return f.createOpenAI(p), nil
	default:
		return nil, fmt.Errorf(
			"unsupported provider type: %s", p.Type,
		)
	}
}

// CreateDefaultModel creates a model instance for the default provider.
func (f *Factory) CreateDefaultModel() (model.Model, error) {
	return f.CreateModel("")
}

// createOpenAI creates an OpenAI-compatible model instance.
func (f *Factory) createOpenAI(p *config.ProviderConfig) model.Model {
	opts := []openai.Option{
		openai.WithBaseURL(p.BaseURL),
		openai.WithAPIKey(p.APIKey),
	}
	return openai.New(p.Model, opts...)
}

// GetDefaultGenerationConfig returns generation config from settings.
func GetDefaultGenerationConfig(
	cfg *config.AgentConfig,
) model.GenerationConfig {
	return model.GenerationConfig{
		Stream:      cfg.Streaming,
		MaxTokens:   intPtr(4096),
		Temperature: floatPtr(0.7),
	}
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
