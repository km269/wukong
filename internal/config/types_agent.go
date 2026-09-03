package config

import "time"

// ============================================================================
// Agent Configuration
// ============================================================================

// AgentConfig controls the core agent loop behavior, LLM generation
// parameters, and tool execution retry policies.
type AgentConfig struct {
	MaxLLMCalls       int           `mapstructure:"max_llm_calls"`
	MaxToolIterations int           `mapstructure:"max_tool_iterations"`
	ParallelTools     bool          `mapstructure:"parallel_tools"`
	Streaming         bool          `mapstructure:"streaming"`
	MaxRunDuration    time.Duration `mapstructure:"max_run_duration"`
	// ToolCallTimeout caps the execution time of a single tool call.
	// It prevents one slow or hung tool (e.g. a web fetch to a heavy
	// site) from consuming the entire run budget. Zero disables the
	// cap. Default 120s (see defaults.go).
	ToolCallTimeout                      time.Duration    `mapstructure:"tool_call_timeout"`
	Temperature                          float64          `mapstructure:"temperature"`
	MaxTokens                            int              `mapstructure:"max_tokens"`
	ToolRetryEnabled                     bool             `mapstructure:"tool_retry_enabled"`
	ToolRetryMaxAttempts                 int              `mapstructure:"tool_retry_max_attempts"`
	ToolRetryInitialWait                 time.Duration    `mapstructure:"tool_retry_initial_wait"`
	ToolRetryBackoffFactor               float64          `mapstructure:"tool_retry_backoff_factor"`
	EnablePostToolPrompt                 bool             `mapstructure:"enable_post_tool_prompt"`
	Planner                              string           `mapstructure:"planner"`
	ReasoningEffort                      string           `mapstructure:"reasoning_effort"`
	ThinkingEnabled                      *bool            `mapstructure:"thinking_enabled"`
	ThinkingTokens                       *int             `mapstructure:"thinking_tokens"`
	ToolSearchEnabled                    bool             `mapstructure:"tool_search_enabled"`
	ToolSearchMaxTools                   int              `mapstructure:"tool_search_max_tools"`
	ContextCompaction                    bool             `mapstructure:"context_compaction"`
	ContextCompactionToolResultMaxTokens int              `mapstructure:"context_compaction_tool_result_max_tokens"`
	ContextCompactionOversizedMaxTokens  int              `mapstructure:"context_compaction_oversized_max_tokens"`
	ContextCompactionKeepRecentRequests  int              `mapstructure:"context_compaction_keep_recent"`
	ContextCompactionForceCleanTools     []string         `mapstructure:"context_compaction_force_clean_tools"`
	ContextCompactionKeepTools           []string         `mapstructure:"context_compaction_keep_tools"`
	SessionRecallEnabled                 bool             `mapstructure:"session_recall_enabled"`
	SessionRecallLimit                   int              `mapstructure:"session_recall_limit"`
	JSONRepairEnabled                    bool             `mapstructure:"json_repair_enabled"`
	AgentToolsEnabled                    bool             `mapstructure:"agent_tools_enabled"`
	AgentToolsStream                     bool             `mapstructure:"agent_tools_stream"`
	SystemPromptDir                      string           `mapstructure:"system_prompt_dir"`
	RecipeDir                            string           `mapstructure:"recipe_dir"`
	RecipeEnabled                        bool             `mapstructure:"recipe_enabled"`
	InlineRecipes                        []map[string]any `mapstructure:"inline_recipes"`
	// FlowEnabled turns on the declarative flow DSL (P0-2): YAML
	// flow definitions under flow_dir become callable tools and
	// "flow.*" capabilities.
	FlowEnabled bool `mapstructure:"flow_enabled"`
	// FlowDir is the directory scanned for flow YAML files. Empty
	// falls back to .wukong/flows relative to the working directory.
	FlowDir string `mapstructure:"flow_dir"`
	// ScriptHooksEnabled turns on user JS hooks (P1-4): every
	// .js file in script_hooks_dir may define beforeStep /
	// beforeTool hook functions and register script tools via the
	// tool() global. Opt-in like flows.
	ScriptHooksEnabled bool `mapstructure:"script_hooks_enabled"`
	// ScriptHooksDir is the directory scanned for hook scripts.
	// Empty falls back to .wukong/hooks relative to the working
	// directory.
	ScriptHooksDir string `mapstructure:"script_hooks_dir"`
	// ScriptHooksTimeout caps each hook/tool invocation. A hit
	// deadline interrupts the JS runtime; hooks fail open (logged
	// and skipped), script tool calls fail with an error. Default
	// 5s.
	ScriptHooksTimeout time.Duration `mapstructure:"script_hooks_timeout"`
	// CommandValidationMode selects how the Guard decides which tool
	// calls need command-string validation (Guard.ValidateCommand):
	//
	//   - "hybrid" (default): capability scope declarations are
	//     authoritative; tools without declarations fall back to the
	//     legacy tool-name heuristic (isCommandTool).
	//   - "descriptor": declarations only. Tools without a "shell"
	//     scope declaration never validate — for setups that declare
	//     scopes for every command-executing extension.
	//   - "heuristic": legacy name matching only, declarations are
	//     ignored.
	CommandValidationMode string `mapstructure:"command_validation_mode"`
}

// ============================================================================
// Security Configuration
// ============================================================================

// PermissionMode defines the security permission level for tool execution.
type PermissionMode string

const (
	PermissionAuto     PermissionMode = "auto"
	PermissionSmart    PermissionMode = "smart"
	PermissionManual   PermissionMode = "manual"
	PermissionChatOnly PermissionMode = "chat_only"
)

// SecurityConfig defines tool execution safety policies, command
// blocking, and fine-grained access control.
type SecurityConfig struct {
	MalwareScanEnabled     bool           `mapstructure:"malware_scan_enabled"`
	DefaultTimeout         time.Duration  `mapstructure:"default_timeout"`
	MaxTimeout             time.Duration  `mapstructure:"max_timeout"`
	BlockDangerousCommands bool           `mapstructure:"block_dangerous_commands"`
	BlockedCommands        []string       `mapstructure:"blocked_commands"`
	RequireApproval        bool           `mapstructure:"require_approval"`
	PermissionMode         PermissionMode `mapstructure:"permission_mode"`
	Allowlist              []string       `mapstructure:"allowlist"`
	Denylist               []string       `mapstructure:"denylist"`
	GuardrailEnabled       bool           `mapstructure:"guardrail_enabled"`
	IgnoreFileEnabled      bool           `mapstructure:"ignore_file_enabled"`
	IgnoreFile             string         `mapstructure:"ignore_file"`
	// Sandbox configures process-level resource limits and lifecycle
	// binding for shell commands executed by the developer toolset.
	// Defaults to all-zero (unlimited, no lifecycle binding) so
	// existing behavior is unchanged unless explicitly enabled.
	Sandbox SandboxConfig `mapstructure:"sandbox"`
}

// SandboxConfig configures the process-level sandbox applied to
// shell execution. Resource caps map onto Windows Job Object limits
// and Linux setrlimit. Zero values mean "unlimited" — leaving the
// field unset keeps the legacy unsandboxed-from-limits behavior.
type SandboxConfig struct {
	Limits           SandboxLimitsConfig `mapstructure:"limits"`
	KillOnParentExit bool                `mapstructure:"kill_on_parent_exit"`
}

// SandboxLimitsConfig mirrors sandbox.ResourceLimits without
// importing pkg/sandbox, keeping the config package decoupled from
// the runtime sandbox implementation. Zero = unlimited.
//
// Platform notes:
//
//	MaxCPUSeconds   — Windows: JOB_OBJECT_LIMIT_PROCESS_TIME
//	                  Linux:   RLIMIT_CPU
//	MaxMemoryBytes  — Windows: JOB_OBJECT_LIMIT_PROCESS_MEMORY
//	                  Linux:   RLIMIT_AS
//	MaxFileBytes    — Linux only (RLIMIT_FSIZE); ignored on Windows
//	MaxProcesses    — Windows: JOB_OBJECT_LIMIT_ACTIVE_PROCESS
//	                  Linux:   RLIMIT_NPROC
type SandboxLimitsConfig struct {
	MaxCPUSeconds  uint64 `mapstructure:"max_cpu_seconds"`
	MaxMemoryBytes uint64 `mapstructure:"max_memory_bytes"`
	MaxFileBytes   uint64 `mapstructure:"max_file_bytes"`
	MaxProcesses   uint64 `mapstructure:"max_processes"`
}
