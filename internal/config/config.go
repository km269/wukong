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
//   - config.go              — WukongConfig root struct, Loader, query helpers
//   - types_provider.go      — Provider & extension types
//   - types_agent.go         — Agent & security (incl. sandbox) types
//   - types_storage.go       — Session/memory/todo/recall types
//   - types_cortex.go        — Cortex stack & revision types
//   - types_browser.go       — Browser/search/proxy types
//   - types_features.go      — Feature-tool types (visualiser, code_mode, ...)
//   - types_apps.go          — Apps (clone & pack) types
//   - types_orchestration.go — Orchestration & discovery types
//   - types_server.go        — Service endpoint types (A2A/ACP/MCP/AG-UI)
//   - types_observability.go — Telemetry/eval/artifact types
//   - defaults.go            — Built-in default values (setDefaults)
//   - validate.go            — Configuration validation (Validate, Warnings)
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
// API keys, secrets, URLs, models, and other configurable string fields
// support ${ENV_VAR} syntax for runtime expansion via expandSecrets().
// Bash-style ${VAR:-default} fallback is also supported. Unresolved
// ${VAR} references (no fallback, VAR unset) are surfaced via Warnings()
// so typos like ${OEPNAI_API_KEY} are visible.
//
// Expansion is tag-driven: every string field whose struct tag includes
// envexpand:"true" participates automatically. The walk is recursive
// (nested structs, pointers to structs, and slices of structs), so a
// field opts in exactly once at its definition site — no separate
// field list to keep in sync. Currently tagged fields include
// providers[].{api_key,base_url,model}, summon.a2a_remotes[].secrets,
// gateway.feishu secrets, server endpoint security.auth.{api_key,
// jwt_secret}, cortex.{embedding,reranker,vertical_routing} settings,
// memoryflow/graphflow models, dify.{base_url,api_secret},
// session.redis_url, memory.extractor_*, and all browser.search
// backend keys.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
// struct types are defined in the types_*.go files.
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

	// unresolvedEnvVars tracks ${VAR} references (without :-default)
	// that could not be resolved because VAR is unset in the
	// environment. Populated by expandSecrets during Load() and
	// surfaced via Warnings() so users can spot typos like
	// ${OEPNAI_API_KEY}. Not populated from YAML directly.
	unresolvedEnvVars []string `mapstructure:"-"`
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

// unresolvedVarRE matches ${VAR} references that do NOT use the
// ${VAR:-default} fallback form. These references silently resolve
// to empty strings when VAR is unset, which usually indicates a
// typo or missing environment configuration.
var unresolvedVarRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnvTracked is expandEnv with unresolved-variable tracking.
// For each ${VAR} (without :-default) in the input where VAR is
// unset, a descriptive message is appended to *unresolved so the
// caller can surface it via Warnings().
func expandEnvTracked(s, field string, unresolved *[]string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	for _, m := range unresolvedVarRE.FindAllStringSubmatch(s, -1) {
		if os.Getenv(m[1]) == "" {
			*unresolved = append(*unresolved,
				fmt.Sprintf("%s references unset env var ${%s}", field, m[1]))
		}
	}
	return expandEnv(s)
}

// expandSecrets expands ${ENV_VAR} references in all string fields
// tagged envexpand:"true". This is a security measure that keeps
// secrets out of config files and version control.
//
// The walk is reflection-based and driven entirely by struct tags:
// adding envexpand:"true" to a new string field (including fields in
// nested structs, pointer structs, or slices of structs owned by other
// packages such as gateway and server) is sufficient — there is no
// parallel field list to maintain.
//
// Unresolved ${VAR} references (no :-default, VAR unset) are recorded
// in cfg.unresolvedEnvVars and surfaced via Warnings() so users can
// spot typos like ${OEPNAI_API_KEY}.
func (l *Loader) expandSecrets(cfg *WukongConfig) {
	expandEnvFields(reflect.ValueOf(cfg).Elem(), "",
		&cfg.unresolvedEnvVars)
}

// expandEnvFields recursively walks a configuration struct and expands
// every settable string field tagged envexpand:"true". prefix is the
// dotted config path of v (empty at the root); it is used only to
// build human-readable diagnostics.
func expandEnvFields(v reflect.Value, prefix string, unresolved *[]string) {
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		expandEnvFields(v.Elem(), prefix, unresolved)
		return
	}
	if v.Kind() != reflect.Struct {
		return
	}

	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // unexported
		}
		tag, ok := field.Tag.Lookup("mapstructure")
		if !ok {
			continue
		}
		key := strings.Split(tag, ",")[0]
		if key == "" || key == "-" {
			continue
		}

		fv := v.Field(i)
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		if _, expand := field.Tag.Lookup("envexpand"); expand &&
			fv.Kind() == reflect.String && fv.CanSet() {
			fv.SetString(expandEnvTracked(fv.String(), path, unresolved))
			continue
		}

		switch fv.Kind() {
		case reflect.Struct:
			expandEnvFields(fv, path, unresolved)
		case reflect.Ptr, reflect.Slice:
			if fv.Type().Elem().Kind() != reflect.Struct {
				continue
			}
			if fv.Kind() == reflect.Ptr {
				expandEnvFields(fv, path, unresolved)
				continue
			}
			for j := 0; j < fv.Len(); j++ {
				elem := fv.Index(j)
				// Prefer a human-readable element identifier
				// (the element's Name field) over a bare index.
				id := fmt.Sprintf("%d", j)
				if n := elem.FieldByName("Name"); n.IsValid() &&
					n.Kind() == reflect.String && n.String() != "" {
					id = n.String()
				}
				expandEnvFields(elem,
					fmt.Sprintf("%s[%s]", path, id), unresolved)
			}
		}
	}
}

// Load parses the configuration into a WukongConfig.
// Results are cached; subsequent calls return the same instance.
func (l *Loader) Load() (*WukongConfig, error) {
	cfg, err := l.loadUnmarshaled()
	if err != nil {
		return nil, err
	}

	// Expand ${ENV_VAR} references in all secret fields.
	l.expandSecrets(cfg)

	l.config = cfg
	return l.config, nil
}

// loadUnmarshaled unmarshals the configuration from Viper's merged
// view (config file + env overrides + defaults) without expanding
// ${ENV_VAR} references. It caches nothing, so callers that need
// the raw (unexpanded) form, such as the `wukong configure --edit`
// wizard baseline, can use it without persisting expanded secrets.
func (l *Loader) loadUnmarshaled() (*WukongConfig, error) {
	var cfg WukongConfig
	if err := l.v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &cfg, nil
}

// LoadUnresolved returns the configuration exactly as stored on
// disk (plus defaults/env overrides), with ${ENV_VAR} secret
// references left untouched. Used by the configure --edit wizard:
// editing must round-trip the file's own values rather than the
// expanded secrets, otherwise editing any unrelated field would
// persist the expanded key material.
func (l *Loader) LoadUnresolved() (*WukongConfig, error) {
	return l.loadUnmarshaled()
}

// SecretValues returns all secret values that have been expanded
// into the loaded configuration, for output redaction. It collects:
//
//   - providers[].api_key
//   - extensions[].env values whose key looks like a secret
//     (contains "key", "secret", "token", "password", or "credential")
//   - summon.a2a_remotes[].{api_key, jwt_secret, oauth_client_secret}
//   - server endpoint security.auth.{api_key, jwt_secret}
//   - gateway AppSecret/EncryptKey/VerificationToken
//
// Values shorter than 2 characters are never yielded (they cannot be
// meaningfully redacted). The returned slice is intended to be passed
// to util.RedactSecrets before printing assistant/tool output.
func (c *WukongConfig) SecretValues() []string {
	var out []string
	add := func(s string) {
		if len(s) >= 2 {
			out = append(out, s)
		}
	}

	for _, p := range c.Providers {
		add(p.APIKey)
	}
	for _, ext := range c.Extensions {
		for k, v := range ext.Env {
			kl := strings.ToLower(strings.TrimSpace(k))
			if strings.Contains(kl, "key") ||
				strings.Contains(kl, "secret") ||
				strings.Contains(kl, "token") ||
				strings.Contains(kl, "password") ||
				strings.Contains(kl, "credential") {
				add(v)
			}
		}
	}
	for _, r := range c.Summon.A2ARemotes {
		add(r.APIKey)
		add(r.JWTSecret)
		add(r.OAuthClientSecret)
	}
	for _, sec := range []ServerSecurityConfig{
		c.AGUI.Security,
		c.ACPServer.Security,
		c.MCPServer.Security,
	} {
		add(sec.Auth.APIKey)
		add(sec.Auth.JWTSecret)
	}
	if w := c.Gateway.Feishu; w.Enabled {
		add(w.AppSecret)
		add(w.EncryptKey)
		add(w.VerificationToken)
	}
	return out
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

// ConfigFileUsed returns the path of the config file the loader
// actually read, or "" when running purely on built-in defaults.
// Delegates to Viper so callers (e.g. `wukong config show`) never
// need to re-implement the search-path priority.
func (l *Loader) ConfigFileUsed() string {
	return l.v.ConfigFileUsed()
}

// Defaults returns a WukongConfig populated exclusively from the
// built-in default values — no YAML file and no environment
// overrides are consulted. It is the single source of truth for
// code-level defaults; callers that need a "fresh default config"
// (e.g. the `wukong configure` wizard) must use it instead of
// hand-maintaining a second copy that would drift.
func Defaults() *WukongConfig {
	v := viper.New()
	l := &Loader{v: v}
	l.setDefaults()

	var cfg WukongConfig
	if err := v.Unmarshal(&cfg); err != nil {
		// Defaults are plain literals; unmarshal cannot fail.
		// Panic is the only sane reaction to a programming error
		// this early, before any caller could handle it.
		panic(fmt.Sprintf("config: unmarshal built-in defaults: %v", err))
	}
	return &cfg
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

// defaultContextWindowByType returns a conservative default context
// window for a provider type when ContextWindow is not explicitly set.
// Values reflect the lowest commonly-available tier for each family to
// avoid overflow on small-footprint deployments. Users should set
// ProviderConfig.ContextWindow explicitly when the actual model differs.
func defaultContextWindowByType(t ProviderType) int {
	switch t {
	case ProviderOpenAI:
		// gpt-4o family: 128K, but older gpt-3.5 tiers were 16K.
		// Conservative default: 16K; users with gpt-4o should override.
		return 16000
	case ProviderAnthropic:
		// claude-sonnet-4: 200K. Conservative default: 100K.
		return 100000
	case ProviderGoogle, ProviderGemini:
		// gemini-2.0-flash: 1M. Conservative default: 32K.
		return 32000
	case ProviderDeepSeek:
		// deepseek-chat: 64K.
		return 64000
	case ProviderOllama, ProviderLMStudio, ProviderVLLM:
		// Local inference servers vary widely. Default to 8K — a
		// floor that nearly all locally-served models exceed. Users
		// must set ContextWindow explicitly for accurate clamping.
		return 8000
	case ProviderACP:
		// ACP agents vary; default to 32K.
		return 32000
	default:
		return 32000
	}
}

// EffectiveContextWindow returns the effective context window for the
// provider. If ContextWindow is set explicitly, that value wins;
// otherwise fall back to defaultContextWindowByType. Returns 0 if p is nil.
func (p *ProviderConfig) EffectiveContextWindow() int {
	if p == nil {
		return 0
	}
	if p.ContextWindow > 0 {
		return p.ContextWindow
	}
	return defaultContextWindowByType(ProviderType(p.Type))
}

// EffectiveContextWindowForDefault returns the effective context window
// for the default provider. Falls back to Revision.MaxContextTokens when
// no default provider is configured. This is the value ContextRevisionEngine
// should clamp against.
func (c *WukongConfig) EffectiveContextWindowForDefault() int {
	if p := c.DefaultProviderConfig(); p != nil {
		return p.EffectiveContextWindow()
	}
	// No provider — use Revision as the configured global limit.
	if c.Revision.MaxContextTokens > 0 {
		return c.Revision.MaxContextTokens
	}
	return 32000
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
