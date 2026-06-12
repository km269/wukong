// Package config provides configuration management for wukong.
// It defines the configuration structure and provides a Viper-based
// loader for YAML configuration files.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

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

// WukongConfig is the top-level configuration structure.
type WukongConfig struct {
	DefaultProvider string            `mapstructure:"default_provider"`
	Providers       []ProviderConfig  `mapstructure:"providers"`
	Extensions      []ExtensionConfig `mapstructure:"extensions"`
	Session         SessionConfig     `mapstructure:"session"`
	Memory          MemoryConfig      `mapstructure:"memory"`
	Todo            TodoConfig        `mapstructure:"todo"`
	Agent           AgentConfig       `mapstructure:"agent"`
	Security        SecurityConfig    `mapstructure:"security"`
	Revision        RevisionConfig    `mapstructure:"revision"`
	Browser         BrowserConfig     `mapstructure:"browser"`
	Recall          RecallConfig      `mapstructure:"recall"`
	Visualiser      VisualiserConfig  `mapstructure:"visualiser"`
	Tutorial        TutorialConfig    `mapstructure:"tutorial"`
	TopOfMind       TopOfMindConfig   `mapstructure:"top_of_mind"`
	CodeMode        CodeModeConfig    `mapstructure:"code_mode"`
	Apps            AppsConfig        `mapstructure:"apps"`
	Summon          SummonConfig      `mapstructure:"summon"`
	Workflow        WorkflowConfig    `mapstructure:"workflow"`
	Telemetry       TelemetryConfig   `mapstructure:"telemetry"`
	Skill           SkillConfig       `mapstructure:"skill"`
	A2AServer       A2AServerConfig   `mapstructure:"a2a_server"`
}

// ProviderConfig defines a model provider configuration.
type ProviderConfig struct {
	Name    string `mapstructure:"name"`
	Type    string `mapstructure:"type"`
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

// ExtensionConfig defines an MCP extension configuration.
type ExtensionConfig struct {
	Name      string            `mapstructure:"name"`
	Type      string            `mapstructure:"type"`
	Transport string            `mapstructure:"transport"`
	Command   string            `mapstructure:"command"`
	Args      []string          `mapstructure:"args"`
	URL       string            `mapstructure:"url"`
	Env       map[string]string `mapstructure:"env"`
	Enabled   bool              `mapstructure:"enabled"`
	Timeout   time.Duration     `mapstructure:"timeout"`
	// Deeplink URL for one-click extension installation
	Deeplink string `mapstructure:"deeplink"`
	// Permissions for fine-grained tool access control
	Permissions []ToolPermission `mapstructure:"permissions"`
}

// ToolPermission defines a permission rule for a specific tool.
type ToolPermission struct {
	Tool    string `mapstructure:"tool"`
	Allowed bool   `mapstructure:"allowed"`
}

// SessionConfig defines session storage configuration.
type SessionConfig struct {
	Backend        string        `mapstructure:"backend"`
	DBPath         string        `mapstructure:"db_path"`
	EventLimit     int           `mapstructure:"event_limit"`
	TTL            time.Duration `mapstructure:"ttl"`
	EnableSummary  bool          `mapstructure:"enable_summary"`
	SummaryTrigger int           `mapstructure:"summary_trigger"`
}

// MemoryConfig defines memory storage configuration.
type MemoryConfig struct {
	Backend     string `mapstructure:"backend"`
	DBPath      string `mapstructure:"db_path"`
	MaxMemories int    `mapstructure:"max_memories"`
	AutoExtract bool   `mapstructure:"auto_extract"`
}

// TodoConfig defines todo storage configuration.
type TodoConfig struct {
	Backend string `mapstructure:"backend"`
	DBPath  string `mapstructure:"db_path"`
}

// AgentConfig defines agent behavior configuration.
type AgentConfig struct {
	MaxLLMCalls       int           `mapstructure:"max_llm_calls"`
	MaxToolIterations int           `mapstructure:"max_tool_iterations"`
	ParallelTools     bool          `mapstructure:"parallel_tools"`
	Streaming         bool          `mapstructure:"streaming"`
	MaxRunDuration    time.Duration `mapstructure:"max_run_duration"`
	Temperature       float64       `mapstructure:"temperature"`
	MaxTokens         int           `mapstructure:"max_tokens"`
	// Tool retry configuration
	ToolRetryEnabled       bool          `mapstructure:"tool_retry_enabled"`
	ToolRetryMaxAttempts   int           `mapstructure:"tool_retry_max_attempts"`
	ToolRetryInitialWait   time.Duration `mapstructure:"tool_retry_initial_wait"`
	ToolRetryBackoffFactor float64       `mapstructure:"tool_retry_backoff_factor"`
	// Post-tool prompt
	EnablePostToolPrompt bool `mapstructure:"enable_post_tool_prompt"`
}

// PermissionMode defines the security permission level for tool execution.
type PermissionMode string

const (
	// PermissionAuto: all tools execute automatically without user approval.
	PermissionAuto PermissionMode = "auto"
	// PermissionManual: every tool call requires explicit user approval.
	PermissionManual PermissionMode = "manual"
	// PermissionSmart: only high-risk operations require user approval.
	PermissionSmart PermissionMode = "smart"
	// PermissionChatOnly: tools are disabled; only chat is allowed.
	PermissionChatOnly PermissionMode = "chat_only"
)

// SecurityConfig defines security settings for extensions.
type SecurityConfig struct {
	// Enable malware scanning for external extensions
	MalwareScanEnabled bool `mapstructure:"malware_scan_enabled"`
	// Default tool execution timeout
	DefaultTimeout time.Duration `mapstructure:"default_timeout"`
	// Maximum timeout any tool can use
	MaxTimeout time.Duration `mapstructure:"max_timeout"`
	// Block dangerous commands (e.g., rm -rf /)
	BlockDangerousCommands bool `mapstructure:"block_dangerous_commands"`
	// List of blocked command patterns
	BlockedCommands []string `mapstructure:"blocked_commands"`
	// Require user approval for destructive operations
	RequireApproval bool `mapstructure:"require_approval"`
	// Permission mode: auto, manual, smart, chat_only
	PermissionMode PermissionMode `mapstructure:"permission_mode"`
	// Tool-level allowlist (only these tools may run)
	Allowlist []string `mapstructure:"allowlist"`
	// Tool-level denylist (these tools are always blocked)
	Denylist []string `mapstructure:"denylist"`
}

// RevisionConfig defines context revision settings.
type RevisionConfig struct {
	// Enable automatic context revision
	Enabled bool `mapstructure:"enabled"`
	// Provider for the smaller/faster revision model
	RevisionProvider string `mapstructure:"revision_provider"`
	// Model name for revision (should be faster/cheaper)
	RevisionModel string `mapstructure:"revision_model"`
	// Max output tokens for command execution before truncation
	MaxCommandOutput int `mapstructure:"max_command_output"`
	// Enable semantic search for context retrieval
	EnableSemanticSearch bool `mapstructure:"enable_semantic_search"`
	// Strategy: "include_all" or "semantic"
	SearchStrategy string `mapstructure:"search_strategy"`
	// Maximum context window tokens
	MaxContextTokens int `mapstructure:"max_context_tokens"`
	// Trim ratio (0.0-1.0) - how much to trim when exceeding max
	TrimRatio float64 `mapstructure:"trim_ratio"`
}

// BrowserConfig defines browser automation settings.
type BrowserConfig struct {
	// Enable browser automation
	Enabled bool `mapstructure:"enabled"`
	// Browser type: "chromium", "firefox", "webkit"
	BrowserType string `mapstructure:"browser_type"`
	// Headless mode
	Headless bool `mapstructure:"headless"`
	// Cache directory for file downloads
	CacheDir string `mapstructure:"cache_dir"`
	// Max download size in bytes
	MaxDownloadSize int64 `mapstructure:"max_download_size"`
	// Request timeout
	Timeout time.Duration `mapstructure:"timeout"`
}

// RecallConfig defines chat recall settings.
type RecallConfig struct {
	// Enable cross-session chat recall
	Enabled bool `mapstructure:"enabled"`
	// Storage backend: sqlite, memory
	Backend string `mapstructure:"backend"`
	// Database path
	DBPath string `mapstructure:"db_path"`
	// Max search results
	MaxResults int `mapstructure:"max_results"`
	// Max stored messages per session for recall
	MaxMessagesPerSession int `mapstructure:"max_messages_per_session"`
}

// VisualiserConfig defines auto-visualiser settings.
type VisualiserConfig struct {
	// Enable auto visualisation
	Enabled bool `mapstructure:"enabled"`
	// Output directory for generated images
	OutputDir string `mapstructure:"output_dir"`
	// Max chart width in pixels
	MaxWidth int `mapstructure:"max_width"`
	// Max chart height in pixels
	MaxHeight int `mapstructure:"max_height"`
}

// TutorialConfig defines interactive tutorial settings.
type TutorialConfig struct {
	// Enable tutorial mode
	Enabled bool `mapstructure:"enabled"`
	// Tutorial language
	Language string `mapstructure:"language"`
}

// TopOfMindConfig defines persistent instruction injection settings.
type TopOfMindConfig struct {
	// Enable Top of Mind
	Enabled bool `mapstructure:"enabled"`
	// File path for persistent instructions
	InstructionFile string `mapstructure:"instruction_file"`
	// Max instruction length
	MaxLength int `mapstructure:"max_length"`
}

// CodeModeConfig defines JavaScript code execution settings.
type CodeModeConfig struct {
	// Enable Code Mode
	Enabled bool `mapstructure:"enabled"`
	// Execution timeout
	Timeout time.Duration `mapstructure:"timeout"`
	// Max memory usage in MB
	MaxMemoryMB int `mapstructure:"max_memory_mb"`
}

// AppsConfig defines custom HTML app settings.
type AppsConfig struct {
	// Enable Apps support
	Enabled bool `mapstructure:"enabled"`
	// Directory for app storage
	AppDir string `mapstructure:"app_dir"`
}

// SummonConfig defines sub-agent delegation settings.
type SummonConfig struct {
	// Enable Summon
	Enabled bool `mapstructure:"enabled"`
	// Skills/recipes directory
	SkillsDir string `mapstructure:"skills_dir"`
	// Max concurrent sub-agents
	MaxConcurrent int `mapstructure:"max_concurrent"`
	// A2A remote agents configuration
	A2ARemotes []A2ARemoteConfig `mapstructure:"a2a_remotes"`
}

// A2ARemoteConfig defines a remote A2A agent configuration.
type A2ARemoteConfig struct {
	// Name of the remote agent
	Name string `mapstructure:"name"`
	// Description of what the remote agent does
	Description string `mapstructure:"description"`
	// A2A server URL
	ServerURL string `mapstructure:"server_url"`
	// Authentication type: jwt, api_key, oauth2
	AuthType string `mapstructure:"auth_type"`
	// API key for api_key auth
	APIKey string `mapstructure:"api_key"`
	// API key header name (default: X-API-Key)
	APIKeyHeader string `mapstructure:"api_key_header"`
	// JWT secret for JWT auth
	JWTSecret string `mapstructure:"jwt_secret"`
	// JWT audience
	JWTAudience string `mapstructure:"jwt_audience"`
	// JWT issuer
	JWTIssuer string `mapstructure:"jwt_issuer"`
	// OAuth2 client credentials
	OAuthTokenURL     string `mapstructure:"oauth_token_url"`
	OAuthClientID     string `mapstructure:"oauth_client_id"`
	OAuthClientSecret string `mapstructure:"oauth_client_secret"`
}

// WorkflowConfig defines multi-mode agent orchestration settings.
type WorkflowConfig struct {
	// Execution mode: single, chain, parallel, cycle, graph
	Mode string `mapstructure:"mode"`
	// Max iterations for cycle/graph modes
	MaxIterations int `mapstructure:"max_iterations"`
	// Sub-agent definitions for workflow modes that use specialized agents.
	// When empty, default agents are used (planner/executor/reviewer, etc.).
	SubAgents []WorkflowSubAgentConfig `mapstructure:"sub_agents"`
}

// WorkflowSubAgentConfig defines a custom sub-agent for workflow modes.
type WorkflowSubAgentConfig struct {
	// Name of the sub-agent (e.g., "planner", "code-reviewer")
	Name string `mapstructure:"name"`
	// System instruction for this sub-agent
	Instruction string `mapstructure:"instruction"`
	// List of tool names this sub-agent is allowed to use (empty = all)
	AllowedTools []string `mapstructure:"allowed_tools"`
	// Whether this sub-agent has access to all tools
	AllTools bool `mapstructure:"all_tools"`
}

// A2AServerConfig defines the local A2A server settings for exposing
// the main agent as an A2A-compatible service to remote clients.
type A2AServerConfig struct {
	// Enable A2A server
	Enabled bool `mapstructure:"enabled"`
	// Listen address (e.g., ":8080")
	Address string `mapstructure:"address"`
	// Agent name exposed via A2A
	AgentName string `mapstructure:"agent_name"`
	// Agent description
	AgentDescription string `mapstructure:"agent_description"`
}

// TelemetryConfig defines OpenTelemetry observability settings.
type TelemetryConfig struct {
	// Enable distributed tracing
	Enabled bool `mapstructure:"enabled"`
	// Exporter type: grpc, http, console
	ExporterType string `mapstructure:"exporter_type"`
	// OTLP collector endpoint
	Endpoint string `mapstructure:"endpoint"`
	// Service name for resource attribution
	ServiceName string `mapstructure:"service_name"`
	// Service version
	ServiceVersion string `mapstructure:"service_version"`
	// Deployment environment: development, staging, production
	Environment string `mapstructure:"environment"`
	// Sampling rate (0.0-1.0)
	SampleRate float64 `mapstructure:"sample_rate"`
}

// SkillConfig defines Agent Skill system settings.
type SkillConfig struct {
	// Enable skill system
	Enabled bool `mapstructure:"enabled"`
	// Skills directory for SKILL.md files
	SkillsDir string `mapstructure:"skills_dir"`
	// Auto-load skills at startup
	AutoLoad bool `mapstructure:"auto_load"`
	// Maximum number of skills to load
	MaxSkills int `mapstructure:"max_skills"`
}

// Loader handles loading configuration from YAML files.
type Loader struct {
	v      *viper.Viper
	config *WukongConfig
}

// NewLoader creates a new configuration loader.
// configPath is an optional path to a custom config file.
func NewLoader(configPath string) (*Loader, error) {
	v := viper.New()
	l := &Loader{v: v}

	v.SetConfigName("config")
	v.SetConfigType("yaml")

	// Search paths in order of priority
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.AddConfigPath(".")
		homeDir, err := os.UserHomeDir()
		if err == nil {
			v.AddConfigPath(filepath.Join(homeDir, ".config", "wukong"))
		}
		v.AddConfigPath("/etc/wukong")
	}

	// Enable environment variable overrides
	v.SetEnvPrefix("WUKONG")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Set defaults
	l.setDefaults()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// Config file not found, use defaults
	}

	return l, nil
}

// Load parses the configuration into a WukongConfig.
func (l *Loader) Load() (*WukongConfig, error) {
	if l.config != nil {
		return l.config, nil
	}

	var cfg WukongConfig
	if err := l.v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// Expand environment variables in API keys
	for i := range cfg.Providers {
		cfg.Providers[i].APIKey = os.ExpandEnv(cfg.Providers[i].APIKey)
	}

	l.config = &cfg
	return l.config, nil
}

// GetConfig returns the currently loaded configuration.
func (l *Loader) GetConfig() *WukongConfig {
	return l.config
}

// FindProvider returns the provider configuration by name.
func (c *WukongConfig) FindProvider(name string) *ProviderConfig {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i]
		}
	}
	return nil
}

// DefaultProvider returns the default provider configuration.
func (c *WukongConfig) DefaultProviderConfig() *ProviderConfig {
	return c.FindProvider(c.DefaultProvider)
}

// EnabledExtensions returns only enabled extensions.
func (c *WukongConfig) EnabledExtensions() []ExtensionConfig {
	var result []ExtensionConfig
	for _, ext := range c.Extensions {
		if ext.Enabled {
			result = append(result, ext)
		}
	}
	return result
}

// FindExtension returns an extension by name.
func (c *WukongConfig) FindExtension(name string) *ExtensionConfig {
	for i := range c.Extensions {
		if c.Extensions[i].Name == name {
			return &c.Extensions[i]
		}
	}
	return nil
}

func (l *Loader) setDefaults() {
	l.v.SetDefault("session.backend", "sqlite")
	l.v.SetDefault("session.db_path", "wukong.db")
	l.v.SetDefault("session.event_limit", 500)
	l.v.SetDefault("session.ttl", "0s")
	l.v.SetDefault("session.enable_summary", true)
	l.v.SetDefault("session.summary_trigger", 50)

	l.v.SetDefault("memory.backend", "sqlite")
	l.v.SetDefault("memory.db_path", "wukong.db")
	l.v.SetDefault("memory.max_memories", 100)
	l.v.SetDefault("memory.auto_extract", true)

	l.v.SetDefault("todo.backend", "sqlite")
	l.v.SetDefault("todo.db_path", "wukong.db")

	l.v.SetDefault("agent.max_llm_calls", 50)
	l.v.SetDefault("agent.max_tool_iterations", 30)
	l.v.SetDefault("agent.parallel_tools", true)
	l.v.SetDefault("agent.streaming", true)
	l.v.SetDefault("agent.max_run_duration", "300s")
	l.v.SetDefault("agent.temperature", 0.7)
	l.v.SetDefault("agent.max_tokens", 4096)
	l.v.SetDefault("agent.tool_retry_enabled", true)
	l.v.SetDefault("agent.tool_retry_max_attempts", 3)
	l.v.SetDefault("agent.tool_retry_initial_wait", "1s")
	l.v.SetDefault("agent.tool_retry_backoff_factor", 2.0)
	l.v.SetDefault("agent.enable_post_tool_prompt", true)

	// Security defaults
	l.v.SetDefault("security.malware_scan_enabled", true)
	l.v.SetDefault("security.default_timeout", "30s")
	l.v.SetDefault("security.max_timeout", "300s")
	l.v.SetDefault("security.block_dangerous_commands", true)
	l.v.SetDefault("security.blocked_commands",
		[]string{"rm -rf /", "dd if=/dev/zero", "mkfs.",
			"> /dev/sda", "fork bomb"})
	l.v.SetDefault("security.require_approval", false)
	l.v.SetDefault("security.permission_mode", "smart")

	// Revision defaults
	l.v.SetDefault("revision.enabled", true)
	l.v.SetDefault("revision.max_command_output", 8000)
	l.v.SetDefault("revision.enable_semantic_search", false)
	l.v.SetDefault("revision.search_strategy", "include_all")
	l.v.SetDefault("revision.max_context_tokens", 64000)
	l.v.SetDefault("revision.trim_ratio", 0.3)

	// Browser defaults
	l.v.SetDefault("browser.enabled", true)
	l.v.SetDefault("browser.browser_type", "chromium")
	l.v.SetDefault("browser.headless", true)
	l.v.SetDefault("browser.cache_dir", ".wukong_cache")
	l.v.SetDefault("browser.max_download_size", 104857600) // 100MB
	l.v.SetDefault("browser.timeout", "60s")

	// Recall defaults
	l.v.SetDefault("recall.enabled", true)
	l.v.SetDefault("recall.backend", "sqlite")
	l.v.SetDefault("recall.db_path", "wukong.db")
	l.v.SetDefault("recall.max_results", 10)
	l.v.SetDefault("recall.max_messages_per_session", 200)

	// Visualiser defaults
	l.v.SetDefault("visualiser.enabled", true)
	l.v.SetDefault("visualiser.output_dir", ".wukong_visuals")
	l.v.SetDefault("visualiser.max_width", 1200)
	l.v.SetDefault("visualiser.max_height", 800)

	// Tutorial defaults
	l.v.SetDefault("tutorial.enabled", true)
	l.v.SetDefault("tutorial.language", "zh")

	// Top of Mind defaults
	l.v.SetDefault("top_of_mind.enabled", true)
	l.v.SetDefault("top_of_mind.instruction_file",
		".wukong_instructions.md")
	l.v.SetDefault("top_of_mind.max_length", 2000)

	// Code Mode defaults
	l.v.SetDefault("code_mode.enabled", true)
	l.v.SetDefault("code_mode.timeout", "10s")
	l.v.SetDefault("code_mode.max_memory_mb", 128)

	// Apps defaults
	l.v.SetDefault("apps.enabled", true)
	l.v.SetDefault("apps.app_dir", ".wukong_apps")

	// Summon defaults
	l.v.SetDefault("summon.enabled", true)
	l.v.SetDefault("summon.skills_dir", ".wukong_skills")
	l.v.SetDefault("summon.max_concurrent", 5)

	// Workflow defaults
	l.v.SetDefault("workflow.mode", "single")
	l.v.SetDefault("workflow.max_iterations", 10)

	// Telemetry defaults
	l.v.SetDefault("telemetry.enabled", false)
	l.v.SetDefault("telemetry.exporter_type", "console")
	l.v.SetDefault("telemetry.endpoint", "localhost:4317")
	l.v.SetDefault("telemetry.service_name", "wukong")
	l.v.SetDefault("telemetry.service_version", "1.0.0")
	l.v.SetDefault("telemetry.environment", "development")
	l.v.SetDefault("telemetry.sample_rate", 1.0)

	// Skill defaults
	// NOTE: The Skill system uses a separate directory from Summon's
	// skills_dir to avoid conflicts. Skill (trpc-agent-go FSRepository)
	// expects directories with SKILL.md files, while Summon expects
	// individual .md files.
	l.v.SetDefault("skill.enabled", true)
	l.v.SetDefault("skill.skills_dir", ".wukong_agent_skills")
	l.v.SetDefault("skill.auto_load", true)
	l.v.SetDefault("skill.max_skills", 20)

	// A2A server defaults
	l.v.SetDefault("a2a_server.enabled", false)
	l.v.SetDefault("a2a_server.address", ":9090")
	l.v.SetDefault("a2a_server.agent_name", "wukong")
	l.v.SetDefault("a2a_server.agent_description",
		"Wukong AI Agent - A2A service endpoint")
}
