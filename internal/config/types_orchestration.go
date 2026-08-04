package config

import "time"

// ============================================================================
// Orchestration & Discovery Configuration
// ============================================================================

// ARDConfig defines Agentic Resource Discovery settings.
type ARDConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	RegistryURL    string `mapstructure:"registry_url"`
	CatalogPath    string `mapstructure:"catalog_path"`
	PublishPort    int    `mapstructure:"publish_port"`
	PublishEnabled bool   `mapstructure:"publish_enabled"`
}

// SummonConfig defines sub-agent delegation and A2A remote agent settings.
type SummonConfig struct {
	Enabled       bool              `mapstructure:"enabled"`
	DelegatesDir  string            `mapstructure:"delegates_dir"`
	MaxConcurrent int               `mapstructure:"max_concurrent"`
	A2ARemotes    []A2ARemoteConfig `mapstructure:"a2a_remotes"`
}

// A2ARemoteConfig defines a remote A2A agent for sub-agent delegation.
type A2ARemoteConfig struct {
	Name              string `mapstructure:"name"`
	Description       string `mapstructure:"description"`
	ServerURL         string `mapstructure:"server_url"`
	AuthType          string `mapstructure:"auth_type"`
	APIKey            string `mapstructure:"api_key"`
	APIKeyHeader      string `mapstructure:"api_key_header"`
	JWTSecret         string `mapstructure:"jwt_secret"`
	JWTAudience       string `mapstructure:"jwt_audience"`
	JWTIssuer         string `mapstructure:"jwt_issuer"`
	OAuthTokenURL     string `mapstructure:"oauth_token_url"`
	OAuthClientID     string `mapstructure:"oauth_client_id"`
	OAuthClientSecret string `mapstructure:"oauth_client_secret"`
}

// ANPConfig defines Agent Network Protocol (ANP) integration settings.
type ANPConfig struct {
	Enabled             bool   `mapstructure:"enabled"`
	DIDDomain           string `mapstructure:"did_domain"`
	DIDPath             string `mapstructure:"did_path"`
	Port                int    `mapstructure:"port"`
	DiscoveryEnabled    bool   `mapstructure:"discovery_enabled"`
	MetaProtocolEnabled bool   `mapstructure:"meta_protocol_enabled"`
	E2EEEnabled         bool   `mapstructure:"e2ee_enabled"`
	A2AEnabled          bool   `mapstructure:"a2a_enabled"`
	AGUIEnabled         bool   `mapstructure:"agui_enabled"`
	HTTPEnabled         bool   `mapstructure:"http_sign_enabled"`
	MCPEnabled          bool   `mapstructure:"mcp_enabled"`
}

// SkillConfig defines the tRPC Agent Skill repository system settings.
type SkillConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	SkillsDir string `mapstructure:"skills_dir"`
	AutoLoad  bool   `mapstructure:"auto_load"`
	MaxSkills int    `mapstructure:"max_skills"`
}

// EvolutionConfig defines the skill self-evolution system settings.
type EvolutionConfig struct {
	Enabled          bool          `mapstructure:"enabled"`
	AutoPatch        bool          `mapstructure:"auto_patch"`
	AnalysisProvider string        `mapstructure:"analysis_provider"`
	AnalysisModel    string        `mapstructure:"analysis_model"`
	MinConfidence    float64       `mapstructure:"min_confidence"`
	CooldownPeriod   time.Duration `mapstructure:"cooldown_period"`
	MaxPatchesPerDay int           `mapstructure:"max_patches_per_day"`
	MaxVersionsKept  int           `mapstructure:"max_versions_kept"`
	MaxPatchSize     int           `mapstructure:"max_patch_size"`
	AnalysisTimeout  time.Duration `mapstructure:"analysis_timeout"`
	ExportJSON       bool          `mapstructure:"export_json"`
}

// KnowledgeConfig defines the RAG knowledge retrieval system settings.
type KnowledgeConfig struct {
	Enabled          bool     `mapstructure:"enabled"`
	EmbedderProvider string   `mapstructure:"embedder_provider"`
	EmbedderModel    string   `mapstructure:"embedder_model"`
	Sources          []string `mapstructure:"sources"`
	SourceURLs       []string `mapstructure:"source_urls"`
	VectorStore      string   `mapstructure:"vector_store"`
	MaxResults       int      `mapstructure:"max_results"`
	EnableSourceSync bool     `mapstructure:"enable_source_sync"`
	RerankerEnabled  bool     `mapstructure:"reranker_enabled"`
	SearchToolName   string   `mapstructure:"search_tool_name"`
}

// OKFConfig defines the Open Knowledge Format interoperability system settings.
type OKFConfig struct {
	Enabled             bool   `mapstructure:"enabled"`
	BundleDir           string `mapstructure:"bundle_dir"`
	InjectorEnabled     bool   `mapstructure:"injector_enabled"`
	EnrichmentEnabled   bool   `mapstructure:"enrichment_enabled"`
	EnrichmentOutputDir string `mapstructure:"enrichment_output_dir"`
	AutoExport          bool   `mapstructure:"auto_export"`
	RegisterInARD       bool   `mapstructure:"register_in_ard"`
}

// DifyConfig defines the Dify AI platform integration settings.
type DifyConfig struct {
	Enabled         bool          `mapstructure:"enabled"`
	BaseURL         string        `mapstructure:"base_url"`
	APISecret       string        `mapstructure:"api_secret"`
	AgentName       string        `mapstructure:"agent_name"`
	EnableStreaming bool          `mapstructure:"enable_streaming"`
	Timeout         time.Duration `mapstructure:"timeout"`
}

// WorkflowMode defines the agent orchestration mode.
type WorkflowMode string

const (
	WorkflowModeSingle         WorkflowMode = "single"
	WorkflowModeChain          WorkflowMode = "chain"
	WorkflowModeParallel       WorkflowMode = "parallel"
	WorkflowModeCycle          WorkflowMode = "cycle"
	WorkflowModeGraph          WorkflowMode = "graph"
	WorkflowModeTeamCoordinator WorkflowMode = "team_coordinator"
	WorkflowModeTeamSwarm      WorkflowMode = "team_swarm"
	WorkflowModeClaudeCode     WorkflowMode = "claude_code"
	WorkflowModeCodex          WorkflowMode = "codex"
	WorkflowModeDify           WorkflowMode = "dify"
)

// WorkflowConfig defines multi-mode agent orchestration settings.
type WorkflowConfig struct {
	Mode          string             `mapstructure:"mode"`
	MaxIterations int                `mapstructure:"max_iterations"`
	CycleMode     string             `mapstructure:"cycle_mode"`
	StreamMode    string             `mapstructure:"stream_mode"`
	CacheEnabled  bool               `mapstructure:"cache_enabled"`
	Engine        string             `mapstructure:"engine"`
	SubAgents     []SubAgentConfig   `mapstructure:"sub_agents"`
	TeamMembers   []TeamMemberConfig `mapstructure:"team_members"`
	ClaudeCodeBin string             `mapstructure:"claude_code_bin"`
	CodexBin      string             `mapstructure:"codex_bin"`
}

// SubAgentConfig defines a custom sub-agent configuration.
type SubAgentConfig struct {
	Name         string   `mapstructure:"name"`
	Provider     string   `mapstructure:"provider"`
	Model        string   `mapstructure:"model"`
	Role         string   `mapstructure:"role"`
	Instruction  string   `mapstructure:"instruction"`
	AllTools     bool     `mapstructure:"all_tools"`
	AllowedTools []string `mapstructure:"allowed_tools"`
}

// WorkflowSubAgentConfig is an alias for SubAgentConfig for backward compatibility.
type WorkflowSubAgentConfig = SubAgentConfig

// TeamMemberConfig defines a team member configuration for team_coordinator/team_swarm modes.
type TeamMemberConfig struct {
	Name         string   `mapstructure:"name"`
	Provider     string   `mapstructure:"provider"`
	Model        string   `mapstructure:"model"`
	Instruction  string   `mapstructure:"instruction"`
	AllTools     bool     `mapstructure:"all_tools"`
	AllowedTools []string `mapstructure:"allowed_tools"`
}
