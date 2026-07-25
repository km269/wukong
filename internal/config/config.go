// Package config provides configuration management for wukong.
//
// It defines the complete configuration structure for all subsystems
// (providers, extensions, agent, security, storage, etc.) and provides
// a Viper-based Loader for YAML configuration files with environment
// variable override support.
//
// # File Organization
//
// The config package is split across multiple files for maintainability:
//   - config.go   — WukongConfig root struct, Loader, query helpers
//   - types.go    — All sub-configuration struct type definitions
//   - defaults.go — Built-in default values (setDefaults)
//   - validate.go — Configuration validation (Validate, Warnings)
//
// # Configuration Priority
//
// Configuration is resolved in the following order (highest to lowest):
//  1. CLI flags (--provider, --model, --temperature, --max-tokens, --config)
//  2. Environment variables (WUKONG_ prefix, e.g. WUKONG_DEFAULT_PROVIDER)
//  3. YAML config file (--config flag or default search paths)
//  4. Built-in defaults (setDefaults())
//
// # Config File Search Paths
//
// When no --config flag is provided, the loader searches:
//  1. Current directory (./config.yaml)
//  2. ~/.config/wukong/config.yaml
//  3. /etc/wukong/config.yaml (non-Windows only)
//
// # Environment Variable Expansion
//
// API keys, secrets, URLs, models, and other configurable fields support
// ${ENV_VAR} syntax for runtime expansion via expandSecrets().
// Bash-style ${VAR:-default} fallback is also supported.
// This applies to:
//   - providers[].api_key, base_url, model
//   - summon.a2a_remotes[].api_key, jwt_secret, oauth_client_secret
//   - gateway.feishu.app_secret, encrypt_key, verification_token
//   - observability.langfuse_public_key, secret_key
//   - artifact.cos_secret_id, cos_secret_key
//   - acp_server.api_key
//   - cortex.embedding_api_key, embedding_base_url, embedding_model
//   - memoryflow.planner_model, extractor_model
//   - graphflow.extractor_model
//   - dify.api_secret
//   - session.redis_url
//   - browser.search.searxng.url, api_key
//   - browser.search.tavily.api_key
//   - browser.search.google.api_key, cse_id
//   - browser.search.bing.api_key
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/km269/wukong/internal/gateway"
	"github.com/spf13/viper"
)

// ============================================================================
// Path Utilities
// ============================================================================

// ResolvePath converts a relative path to an absolute path.
// If the path is already absolute, it is returned as-is.
// This ensures all modules sharing the same file (e.g. wukong.db)
// resolve to the same absolute location regardless of the working
// directory.
func ResolvePath(rawPath string) string {
	if filepath.IsAbs(rawPath) {
		return rawPath
	}
	absPath, err := filepath.Abs(rawPath)
	if err != nil {
		return rawPath
	}
	return absPath
}

// ============================================================================
// Top-Level Configuration
// ============================================================================

// WukongConfig is the root configuration structure containing all
// subsystem configurations for the wukong AI agent platform.
//
// Each field corresponds to a YAML section in config.yaml. Sub-config
// struct types are defined in types.go.
type WukongConfig struct {
	// DefaultProvider is the name of the default LLM provider.
	// Must match a ProviderConfig.Name in the Providers list.
	DefaultProvider string `mapstructure:"default_provider"`
	// LogLevel controls the logging verbosity: "debug", "info",
	// "warn", "error". Overridden by --debug/--quiet CLI flags.
	// Default: "info".
	LogLevel string `mapstructure:"log_level"`
	// LightweightProvider is the optional provider name for
	// background tasks (memory extraction, summarisation,
	// knowledge graph construction). Falls back to
	// DefaultProvider when empty.
	LightweightProvider string `mapstructure:"lightweight_provider"`
	// LightweightModel is the optional lightweight model for
	// background tasks. When set, it becomes the default model
	// for memory.extractor_model, memoryflow.*, graphflow.*,
	// and revision.revision_model if they are not explicitly
	// configured.
	LightweightModel string `mapstructure:"lightweight_model"`

	// Providers lists all available LLM backend configurations.
	Providers []ProviderConfig `mapstructure:"providers"`

	// Extensions defines all MCP extensions (built-in and
	// external).
	Extensions []ExtensionConfig `mapstructure:"extensions"`

	// Agent controls the core agent loop behavior and LLM
	// parameters.
	Agent AgentConfig `mapstructure:"agent"`

	// Security defines tool execution permissions and safety
	// policies.
	Security SecurityConfig `mapstructure:"security"`

	// Session configures conversation history storage.
	Session SessionConfig `mapstructure:"session"`

	// Memory configures long-term knowledge persistence.
	Memory MemoryConfig `mapstructure:"memory"`

	// Todo configures the task tracking subsystem.
	Todo TodoConfig `mapstructure:"todo"`

	// Recall configures cross-session chat history search.
	Recall RecallConfig `mapstructure:"recall"`

	// Cortex configures the CortexDB-based intelligent recall
	// and knowledge storage.
	Cortex CortexConfig `mapstructure:"cortex"`

	// MemoryFlow configures CortexDB MemoryFlow for
	// conversation transcript recording.
	MemoryFlow MemoryFlowConfig `mapstructure:"memoryflow"`

	// GraphFlow configures CortexDB GraphFlow for entity/relation
	// extraction and knowledge graph construction.
	GraphFlow GraphFlowConfig `mapstructure:"graphflow"`

	// ImportFlow configures CortexDB ImportFlow for structured
	// data import.
	ImportFlow ImportFlowConfig `mapstructure:"importflow"`

	// Revision configures context window management and token
	// optimization.
	Revision RevisionConfig `mapstructure:"revision"`

	// Browser configures web automation and file caching.
	Browser BrowserConfig `mapstructure:"browser"`

	// Visualiser configures chart/diagram generation.
	Visualiser VisualiserConfig `mapstructure:"visualiser"`

	// Tutorial configures the interactive tutorial system.
	Tutorial TutorialConfig `mapstructure:"tutorial"`

	// TopOfMind configures persistent instruction injection.
	TopOfMind TopOfMindConfig `mapstructure:"top_of_mind"`

	// CodeMode configures the JavaScript code execution sandbox.
	CodeMode CodeModeConfig `mapstructure:"code_mode"`

	// Apps configures custom HTML standalone applications.
	Apps AppsConfig `mapstructure:"apps"`

	// ARD configures Agentic Resource Discovery.
	ARD ARDConfig `mapstructure:"ard"`

	// Summon configures sub-agent delegation and A2A remotes.
	Summon SummonConfig `mapstructure:"summon"`

	// ANP configures Agent Network Protocol (ANP) support
	// including W3C DID identity, meta-protocol capability
	// negotiation, E2EE encryption, and RFC 9421 HTTP signing.
	ANP ANPConfig `mapstructure:"anp"`

	// Skill configures the tRPC Agent Skill repository system.
	Skill SkillConfig `mapstructure:"skill"`

	// Evolution configures the skill self-evolution system.
	Evolution EvolutionConfig `mapstructure:"evolution"`

	// Knowledge configures the RAG knowledge retrieval system.
	Knowledge KnowledgeConfig `mapstructure:"knowledge"`

	// OKF configures the Open Knowledge Format interoperability
	// system for knowledge bundle import/export, index injection,
	// and enrichment.
	OKF OKFConfig `mapstructure:"okf"`

	// Dify configures the Dify AI platform integration.
	Dify DifyConfig `mapstructure:"dify"`

	// Workflow configures multi-mode agent orchestration.
	Workflow WorkflowConfig `mapstructure:"workflow"`

	// Gateway configures the messaging gateway
	// (Feishu, etc.). Each channel owns its own inbound transport.
	// The type lives in internal/gateway; the root config embeds it.
	Gateway gateway.GatewayConfig `mapstructure:"gateway"`

	// A2AServer configures the local A2A protocol server.
	A2AServer A2AServerConfig `mapstructure:"a2a_server"`

	// AGUI configures the AG-UI SSE server for web-based chat
	// UIs.
	AGUI AGUIConfig `mapstructure:"agui"`

	// ACPServer configures the Agent Client Protocol server
	// endpoint.
	ACPServer ACPServerConfig `mapstructure:"acp_server"`

	// ACPMCP configures the MCP bridge that exposes extensions
	// as an MCP Server for ACP agents.
	ACPMCP ACPMCPConfig `mapstructure:"acp_mcp"`

	// MCPServer configures the standalone MCP server that exposes
	// Wukong extensions via the MCP JSON-RPC 2.0 protocol.
	MCPServer MCPServerConfig `mapstructure:"mcp_server"`

	// Telemetry configures OpenTelemetry observability.
	Telemetry TelemetryConfig `mapstructure:"telemetry"`

	// Eval configures the evaluation/regression testing system.
	Eval EvalConfig `mapstructure:"eval"`

	// Artifact configures artifact storage backend
	// settings.
	Artifact ArtifactConfig `mapstructure:"artifact"`

	// Observability configures enhanced observability
	// (Langfuse, etc.).
	Observability ObservabilityConfig `mapstructure:"observability"`

	// ProjectDir is the directory for project tracking data.
	// Default: ~/.config/wukong/ (resolved at runtime).
	ProjectDir string `mapstructure:"project_dir"`
}

// ============================================================================
// Configuration Loader
// ============================================================================

// Loader handles loading configuration from YAML files using Viper.
// It supports environment variable overrides with the WUKONG_ prefix.
type Loader struct {
	v      *viper.Viper
	config *WukongConfig
}

// NewLoader creates a new configuration loader.
//
// configPath is an optional path to a custom config file.
// If empty, searches in order:
//  1. Current directory (./config.yaml)
//  2. ~/.config/wukong/config.yaml
//  3. /etc/wukong/config.yaml (non-Windows only)
func NewLoader(configPath string) (*Loader, error) {
	v := viper.New()
	l := &Loader{v: v}

	v.SetConfigName("config")
	v.SetConfigType("yaml")

	if configPath != "" {
		// Distinguish between a file path and a directory path.
		// If configPath is a directory, use AddConfigPath to
		// search for "config.yaml" inside it. If it is a file
		// (or doesn't exist yet, which SetConfigFile handles),
		// use it directly.
		if info, err := os.Stat(configPath); err == nil && info.IsDir() {
			v.AddConfigPath(configPath)
		} else {
			v.SetConfigFile(configPath)
		}
	} else {
		v.AddConfigPath(".")
		homeDir, err := os.UserHomeDir()
		if err == nil {
			v.AddConfigPath(filepath.Join(homeDir, ".config", "wukong"))
		}
		// /etc/wukong is only valid on Unix-like systems.
		// On Windows, this path would be silently ignored.
		if runtime.GOOS != "windows" {
			v.AddConfigPath("/etc/wukong")
		}
	}

	// Environment variable overrides.
	// Example: WUKONG_DEFAULT_PROVIDER, WUKONG_AGENT_TEMPERATURE.
	v.SetEnvPrefix("WUKONG")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Set built-in defaults before reading config file.
	l.setDefaults()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// Config file not found is OK; use defaults.
	}

	return l, nil
}

// expandEnv expands ${ENV_VAR} and ${ENV_VAR:-default} syntax.
// Unlike os.ExpandEnv, it supports the bash-style ${VAR:-default} fallback.
func expandEnv(s string) string {
	return os.Expand(s, func(key string) string {
		if idx := strings.Index(key, ":-"); idx != -1 {
			varName := key[:idx]
			defaultVal := key[idx+2:]
			if val := os.Getenv(varName); val != "" {
				return val
			}
			return defaultVal
		}
		return os.Getenv(key)
	})
}

// expandSecrets expands ${ENV_VAR} references in all secret fields
// that support environment variable injection. This is a security
// measure that keeps secrets out of config files and version control.
func (l *Loader) expandSecrets(cfg *WukongConfig) {
	// Provider API keys, base URLs, and models.
	for i := range cfg.Providers {
		cfg.Providers[i].APIKey = expandEnv(cfg.Providers[i].APIKey)
		cfg.Providers[i].BaseURL = expandEnv(cfg.Providers[i].BaseURL)
		cfg.Providers[i].Model = expandEnv(cfg.Providers[i].Model)
	}

	// A2A remote secrets.
	for i := range cfg.Summon.A2ARemotes {
		cfg.Summon.A2ARemotes[i].APIKey =
			expandEnv(cfg.Summon.A2ARemotes[i].APIKey)
		cfg.Summon.A2ARemotes[i].JWTSecret =
			expandEnv(cfg.Summon.A2ARemotes[i].JWTSecret)
		cfg.Summon.A2ARemotes[i].OAuthClientSecret =
			expandEnv(cfg.Summon.A2ARemotes[i].OAuthClientSecret)
	}

	// Gateway Feishu channel secrets.
	cfg.Gateway.Feishu.AppSecret =
		expandEnv(cfg.Gateway.Feishu.AppSecret)
	cfg.Gateway.Feishu.EncryptKey =
		expandEnv(cfg.Gateway.Feishu.EncryptKey)
	cfg.Gateway.Feishu.VerificationToken =
		expandEnv(cfg.Gateway.Feishu.VerificationToken)

	// Observability (Langfuse) secrets.
	cfg.Observability.LangfusePublicKey =
		expandEnv(cfg.Observability.LangfusePublicKey)
	cfg.Observability.LangfuseSecretKey =
		expandEnv(cfg.Observability.LangfuseSecretKey)

	// Artifact COS credentials.
	cfg.Artifact.COSSecretID =
		expandEnv(cfg.Artifact.COSSecretID)
	cfg.Artifact.COSSecretKey =
		expandEnv(cfg.Artifact.COSSecretKey)

	// ACP Server API key (nested under Security.Auth).
	cfg.ACPServer.Security.Auth.APIKey =
		expandEnv(cfg.ACPServer.Security.Auth.APIKey)

	// CortexDB embedding settings.
	cfg.Cortex.EmbeddingAPIKey =
		expandEnv(cfg.Cortex.EmbeddingAPIKey)
	cfg.Cortex.EmbeddingBaseURL =
		expandEnv(cfg.Cortex.EmbeddingBaseURL)
	cfg.Cortex.EmbeddingModel =
		expandEnv(cfg.Cortex.EmbeddingModel)

	// MemoryFlow model settings.
	cfg.MemoryFlow.PlannerModel =
		expandEnv(cfg.MemoryFlow.PlannerModel)
	cfg.MemoryFlow.ExtractorModel =
		expandEnv(cfg.MemoryFlow.ExtractorModel)

	// GraphFlow model settings.
	cfg.GraphFlow.ExtractorModel =
		expandEnv(cfg.GraphFlow.ExtractorModel)

	// Dify API secret.
	cfg.Dify.APISecret =
		expandEnv(cfg.Dify.APISecret)

	// Session Redis URL.
	cfg.Session.RedisURL = expandEnv(cfg.Session.RedisURL)

	// Search provider secrets.
	cfg.Browser.Search.SearXNG.URL = expandEnv(cfg.Browser.Search.SearXNG.URL)
	cfg.Browser.Search.SearXNG.APIKey = expandEnv(cfg.Browser.Search.SearXNG.APIKey)
	cfg.Browser.Search.Tavily.APIKey = expandEnv(cfg.Browser.Search.Tavily.APIKey)
	cfg.Browser.Search.Google.APIKey = expandEnv(cfg.Browser.Search.Google.APIKey)
	cfg.Browser.Search.Google.CSEID = expandEnv(cfg.Browser.Search.Google.CSEID)
	cfg.Browser.Search.Bing.APIKey = expandEnv(cfg.Browser.Search.Bing.APIKey)
}

// Load parses the configuration into a WukongConfig.
// Results are cached; subsequent calls return the same instance.
func (l *Loader) Load() (*WukongConfig, error) {
	if l.config != nil {
		return l.config, nil
	}

	var cfg WukongConfig
	if err := l.v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// Expand ${ENV_VAR} references in all secret fields.
	l.expandSecrets(&cfg)

	l.config = &cfg
	return l.config, nil
}

// LoadAndValidate loads the configuration and then validates it.
// Returns an error if loading fails or if validation finds fatal
// issues.
func (l *Loader) LoadAndValidate() (*WukongConfig, error) {
	cfg, err := l.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}
	return cfg, nil
}

// GetConfig returns the currently loaded configuration.
// Returns nil if Load has not been called yet.
func (l *Loader) GetConfig() *WukongConfig {
	return l.config
}

// ============================================================================
// Configuration Query Helpers
// ============================================================================

// FindProvider returns the provider configuration by name.
// Returns nil if no provider with the given name exists.
func (c *WukongConfig) FindProvider(name string) *ProviderConfig {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i]
		}
	}
	return nil
}

// DefaultProviderConfig returns the configuration for the default
// provider. Returns nil if the default provider is not found.
func (c *WukongConfig) DefaultProviderConfig() *ProviderConfig {
	return c.FindProvider(c.DefaultProvider)
}

// EffectiveLightweightModel returns the effective lightweight model
// name. Falls back from LightweightModel to the default provider's
// model.
func (c *WukongConfig) EffectiveLightweightModel() string {
	if c.LightweightModel != "" {
		return c.LightweightModel
	}
	p := c.DefaultProviderConfig()
	if p != nil {
		return p.Model
	}
	return ""
}

// EffectiveLightweightProvider returns the effective lightweight
// provider name. Falls back from LightweightProvider to
// DefaultProvider.
func (c *WukongConfig) EffectiveLightweightProvider() string {
	if c.LightweightProvider != "" {
		return c.LightweightProvider
	}
	return c.DefaultProvider
}

// EnabledExtensions returns only the extensions that are enabled.
func (c *WukongConfig) EnabledExtensions() []ExtensionConfig {
	result := make([]ExtensionConfig, 0, len(c.Extensions))
	for _, ext := range c.Extensions {
		if ext.Enabled {
			result = append(result, ext)
		}
	}
	return result
}

// FindExtension returns an extension configuration by name.
// Returns nil if no extension with the given name exists.
func (c *WukongConfig) FindExtension(name string) *ExtensionConfig {
	for i := range c.Extensions {
		if c.Extensions[i].Name == name {
			return &c.Extensions[i]
		}
	}
	return nil
}
