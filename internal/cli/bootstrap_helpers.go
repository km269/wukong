// Code split out of session.go (P2-8) - same package, zero behavior change.
package cli

import (
	"fmt"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/evolution"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/skill"
	"github.com/km269/wukong/internal/summon"
	"github.com/km269/wukong/internal/util"
	"log/slog"
	"strings"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// a2aRemoteToConfig converts a config A2ARemoteConfig to an A2AAgent.
// Uses the new A2AAgent implementation based on tRPC-Agent-Go's a2aagent.
func a2aRemoteToConfig(remote config.A2ARemoteConfig) *summon.A2AAgent {
	ag, err := summon.NewA2AAgentFromConfig(remote)
	if err != nil {
		util.Logger.Warn("failed to create A2A agent for remote",
			"name", remote.Name,
			"error", err.Error())
		return nil
	}
	util.Logger.Info("A2A remote agent configured",
		"name", remote.Name,
		"server_url", remote.ServerURL)
	return ag
}

// createExtractorModel creates a model for memory extraction.
// If the memory config specifies an extractor_provider, that provider
// is used; otherwise the default provider is used. This allows using
// a smaller/cheaper model (e.g., deepseek-chat) for memory extraction
// while keeping a more capable model for the main conversation.
// createExtractorModel creates a model for memory extraction.
// If the memory config specifies an extractor_provider, that provider
// is used; otherwise the default provider is used. This allows using
// a smaller/cheaper model (e.g., deepseek-chat) for memory extraction
// while keeping a more capable model for the main conversation.
func createExtractorModel(
	factory *provider.Factory,
	memCfg *config.MemoryConfig,
	wukongCfg *config.WukongConfig,
) (model.Model, error) {
	extractorProviderName := memCfg.ExtractorProvider
	if extractorProviderName == "" {
		extractorProviderName = wukongCfg.EffectiveLightweightProvider()
	}
	extractorModelName := memCfg.ExtractorModel
	if extractorModelName == "" {
		extractorModelName = wukongCfg.EffectiveLightweightModel()
	}

	extractorProvider := wukongCfg.FindProvider(extractorProviderName)
	if extractorProvider == nil {
		return nil, fmt.Errorf(
			"extractor provider %q not found in providers list",
			extractorProviderName,
		)
	}
	// Temporarily override the provider's model for extraction.
	originalModel := extractorProvider.Model
	extractorProvider.Model = extractorModelName
	defer func() {
		extractorProvider.Model = originalModel
	}()
	return factory.CreateModel(extractorProviderName)
}

// applyOverrides applies command-line overrides to config.
// applyOverrides applies command-line overrides to config.
func applyOverrides(
	cfg *config.WukongConfig,
	providerName string,
	modelName string,
	temperature float64,
	maxTokens int,
	noStream bool,
) {
	if providerName != "" {
		p := cfg.FindProvider(providerName)
		if p == nil {
			util.Logger.Warn("provider not found in config",
				slog.String("provider", providerName))
		} else {
			cfg.DefaultProvider = providerName
			if modelName != "" {
				p.Model = modelName
			}
		}
	} else if modelName != "" {
		p := cfg.DefaultProviderConfig()
		if p != nil {
			p.Model = modelName
		}
	}

	if temperature >= 0 {
		cfg.Agent.Temperature = temperature
	}
	if maxTokens > 0 {
		cfg.Agent.MaxTokens = maxTokens
	}
	if noStream {
		cfg.Agent.Streaming = false
	}
}

// validateConfig checks for common configuration mistakes and
// quickConfig holds minimal session info for immediate display.
// validateConfig checks for common configuration mistakes and
// quickConfig holds minimal session info for immediate display.
type quickConfig struct {
	provider string
	model    string
}

// quickLoadConfig performs a minimal config load to get provider
// and model info before the full bootstrap. This gives the user
// immediate feedback without waiting for all subsystems to start.
// quickLoadConfig performs a minimal config load to get provider
// and model info before the full bootstrap. This gives the user
// immediate feedback without waiting for all subsystems to start.
func quickLoadConfig(
	configPath, cliProvider, cliModel string,
) quickConfig {
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return quickConfig{provider: "unknown", model: "unknown"}
	}
	cfg, err := loader.Load()
	if err != nil {
		return quickConfig{provider: "unknown", model: "unknown"}
	}

	provider := cliProvider
	if provider == "" {
		provider = cfg.DefaultProvider
	}

	model := cliModel
	if model == "" {
		if p := cfg.FindProvider(provider); p != nil {
			model = p.Model
		}
	}

	return quickConfig{provider: provider, model: model}
}

// validateConfig checks for common configuration mistakes and
// emits warnings. This helps users diagnose issues before they
// encounter runtime errors during a session.
// validateConfig checks for common configuration mistakes and
// emits warnings. This helps users diagnose issues before they
// encounter runtime errors during a session.
func validateConfig(cfg *config.WukongConfig) {
	if cfg.DefaultProvider == "" {
		util.Logger.Warn("no default_provider configured; " +
			"set it in config.yaml or use --provider flag")
		return
	}

	p := cfg.FindProvider(cfg.DefaultProvider)
	if p == nil {
		util.Logger.Warn("default_provider not found in providers list",
			slog.String("configured", cfg.DefaultProvider))
		return
	}

	if p.Model == "" {
		util.Logger.Warn("no model configured for default provider; " +
			"the provider may use a default model")
	}

	if p.APIKey == "" && p.Type != "ollama" && p.Type != "lmstudio" && p.Type != "vllm" {
		util.Logger.Warn("no API key configured for " + cfg.DefaultProvider +
			"; set " + p.Name + ".api_key in config or via " +
			strings.ToUpper(p.Name) + "_API_KEY env var")
	}

	if cfg.Agent.Planner == "builtin" &&
		p.Type != "anthropic" && p.Type != "google" {
		util.Logger.Warn("builtin planner requires a model with native " +
			"thinking support (Claude/Gemini); current provider is " +
			p.Type + " — consider using 'react' planner instead")
	}

	switch cfg.Agent.Planner {
	case "builtin", "react":
		util.Logger.Info("planner enabled: " + cfg.Agent.Planner)
	default:
		if cfg.Agent.Planner != "" {
			util.Logger.Warn("unknown planner: " + cfg.Agent.Planner +
				"; supported: builtin, react")
		}
	}

	if cfg.Security.GuardrailEnabled {
		util.Logger.Info("guardrail enabled — prompt injection detection active")
	}

	if cfg.Memory.AutoExtract &&
		cfg.Memory.ExtractorProvider == "" &&
		cfg.Memory.ExtractorModel == "" {
		// Auto-extract uses the default provider; warn if that
		// provider may be slow or expensive for extraction.
		if p.Type == "lmstudio" || p.Type == "ollama" || p.Type == "vllm" {
			util.Logger.Info("auto-extract uses local " + p.Type +
				" model — this may be slow; consider setting " +
				"memory.extractor_provider to a faster model")
		}
	}
}

// evoEngineClose returns a close function for the evolution engine,
// or nil if the engine is nil (not enabled).
// evoEngineClose returns a close function for the evolution engine,
// or nil if the engine is nil (not enabled).
func evoEngineClose(engine *evolution.EvolutionEngine) func() error {
	if engine == nil {
		return nil
	}
	return engine.Close
}

// skillEvoAdapter converts skill.SkillExecutionTrace to
// evolution.ExecutionTrace and forwards it to the evolution engine.
// This avoids import cycles between the skill and evolution packages.
// skillEvoAdapter converts skill.SkillExecutionTrace to
// evolution.ExecutionTrace and forwards it to the evolution engine.
// This avoids import cycles between the skill and evolution packages.
type skillEvoAdapter struct {
	engine *evolution.EvolutionEngine
}

func (a *skillEvoAdapter) RecordExecution(
	trace *skill.SkillExecutionTrace,
) {
	if a.engine == nil || trace == nil {
		return
	}
	toolCalls := make([]evolution.ToolCallRecord, 0, len(trace.ToolCalls))
	for _, tc := range trace.ToolCalls {
		toolCalls = append(toolCalls, evolution.ToolCallRecord{
			Name:     tc.Name,
			Args:     tc.Args,
			Result:   tc.Result,
			Error:    tc.Error,
			Duration: tc.Duration,
			Sequence: tc.Sequence,
			Retried:  tc.Retried,
		})
	}
	a.engine.RecordExecution(&evolution.ExecutionTrace{
		SkillName:    trace.SkillName,
		SkillFile:    trace.SkillFile,
		SessionID:    trace.SessionID,
		UserID:       trace.UserID,
		StartTime:    trace.StartTime,
		EndTime:      trace.EndTime,
		Duration:     trace.Duration,
		LLMCalls:     trace.LLMCalls,
		Error:        trace.Error,
		ErrorCount:   trace.ErrorCount,
		FinalOutput:  trace.FinalOutput,
		OutputLength: trace.OutputLength,
		Success:      trace.Success,
		QualityScore: trace.QualityScore,
		ToolCalls:    toolCalls,
	})
}

// parsePort extracts the numeric port from an address string like
// ":9090" or "localhost:9090". Returns the port or 0 if unparseable.
// parsePort extracts the numeric port from an address string like
// ":9090" or "localhost:9090". Returns the port or 0 if unparseable.
func parsePort(address string) int {
	if address == "" {
		return 0
	}
	// Strip the optional host prefix.
	colonIdx := strings.LastIndex(address, ":")
	if colonIdx < 0 {
		return 0
	}
	portStr := address[colonIdx+1:]
	p := 0
	for _, c := range portStr {
		if c < '0' || c > '9' {
			return 0
		}
		p = p*10 + int(c-'0')
	}
	return p
}
