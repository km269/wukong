// Code split out of loop.go (P2-8) - same package, zero behavior change.
package agent

import (
	"context"
	"fmt"
	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/util"
	"strings"
	"time"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner/builtin"
	"trpc.group/trpc-go/trpc-agent-go/planner/react"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Ensure type compatibility check
var _ agent.Agent = (*llmagent.LLMAgent)(nil)

// NewSimpleLLMAgent creates a minimal LLMAgent for A2A server and
// other lightweight use cases. It uses default generation config
// and a basic system instruction without all the tool/security wiring
// of createSingleAgent.
// NewSimpleLLMAgent creates a minimal LLMAgent for A2A server and
// other lightweight use cases. It uses default generation config
// and a basic system instruction without all the tool/security wiring
// of createSingleAgent.
func NewSimpleLLMAgent(
	mdl model.Model,
	agentCfg *config.AgentConfig,
	name string,
) agent.Agent {
	genConfig := provider.GetDefaultGenerationConfig(agentCfg)
	opts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription(
			fmt.Sprintf("Wukong %s - A2A endpoint", name)),
		llmagent.WithInstruction(buildBaseInstruction("")),
		llmagent.WithAddCurrentTime(true),
	}
	return llmagent.New("wukong-a2a-"+name, opts...)
}

// createSingleAgent creates the standard single LLMAgent with all
// configured options. This preserves the original agent creation logic.
// createSingleAgent creates the standard single LLMAgent with all
// configured options. This preserves the original agent creation logic.
func createSingleAgent(
	cfg CoreLoopConfig, allTools []tool.Tool,
) (agent.Agent, error) {
	mdl, err := cfg.Factory.CreateDefaultModel()
	if err != nil {
		return nil, fmt.Errorf("create default model: %w", err)
	}
	if mdl == nil {
		return nil, fmt.Errorf(
			"default model is nil, agent cannot be created")
	}

	genConfig := provider.GetDefaultGenerationConfig(&cfg.Config.Agent)

	// Load prompt templates from configured directory.
	// If templates exist, they replace the hardcoded base instruction.
	// Variables like {{.WorkingDir}} are substituted at load time.
	tmplMgr := NewPromptTemplateManager(cfg.Config)
	templateVars := TemplateVars{
		WorkingDir:   cfg.WorkingDir,
		ModelName:    mdl.Info().Name,
		ProviderName: cfg.Config.DefaultProvider,
		SessionID:    cfg.SessionID,
		UserName:     cfg.UserID,
	}
	templateText := tmplMgr.LoadTemplates(templateVars)

	instructions := buildSystemInstruction(
		cfg.TopOfMindInstructions,
		templateText,
	)

	agentOpts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription(
			"Wukong AI Agent - A local-first extensible AI " +
				"assistant that can use tools to read files, " +
				"execute commands, search code, browse the web, " +
				"remember preferences, and complete complex " +
				"tasks autonomously.",
		),
		llmagent.WithInstruction(instructions),
		llmagent.WithAddCurrentTime(true),
		llmagent.WithTimeFormat(time.RFC3339),
	}

	// Note: framework's built-in memory preload is disabled.
	// Memory injection is handled by our own logic in CoreLoop.Run()
	// (MemoryFlow WakeUp + tRPC Memory ReadMemories), which provides
	// more control over deduplication, error handling, and logging.

	// Warn agent when memory is near capacity so it can clean up.
	if cfg.Config.Memory.MaxMemories > 0 {
		// ReadMemories with limit 0 returns all entries count.
		// The preload already handles the content; this is just
		// a safety net in the system instruction.
		if cfg.MemoryService != nil {
			memCtx, memCancel := context.WithTimeout(
				context.Background(), 5*time.Second,
			)
			entries, _ := cfg.MemoryService.ReadMemories(
				memCtx,
				memory.UserKey{
					AppName: "wukong-app",
					UserID:  cfg.UserID,
				},
				0,
			)
			memCancel()
			if len(entries) >= cfg.Config.Memory.MaxMemories-5 {
				util.Logger.Warn("memory: near capacity",
					"current", len(entries),
					"max", cfg.Config.Memory.MaxMemories,
				)
			}
		}
	}

	if len(allTools) > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithTools(allTools),
		)
	}
	if len(cfg.ToolSets) > 0 || cfg.Capabilities != nil {
		toolSets := effectiveToolSets(cfg)
		agentOpts = append(agentOpts,
			llmagent.WithToolSets(toolSets),
		)
		// Diagnostic: log all tool names from ToolSets to verify
		// that memory tools are actually visible to the agent.
		discCtx, discCancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		var tsToolNames []string
		for _, ts := range toolSets {
			for _, t := range ts.Tools(discCtx) {
				if d := t.Declaration(); d != nil {
					tsToolNames = append(tsToolNames, d.Name)
				}
			}
		}
		util.Logger.Info("agent: ToolSet tools loaded",
			"toolset_count", len(toolSets),
			"tool_names", tsToolNames,
		)
		discCancel()
	}

	if cfg.Config.Agent.MaxLLMCalls > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithMaxLLMCalls(cfg.Config.Agent.MaxLLMCalls),
		)
	}
	if cfg.Config.Agent.MaxToolIterations > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithMaxToolIterations(cfg.Config.Agent.MaxToolIterations),
		)
	}
	if cfg.Config.Agent.ParallelTools {
		agentOpts = append(agentOpts,
			llmagent.WithEnableParallelTools(true),
		)
	}
	if cfg.Config.Agent.ToolRetryEnabled {
		retryPolicy := &tool.RetryPolicy{
			MaxAttempts:     cfg.Config.Agent.ToolRetryMaxAttempts,
			InitialInterval: time.Duration(cfg.Config.Agent.ToolRetryInitialWait),
			BackoffFactor:   cfg.Config.Agent.ToolRetryBackoffFactor,
			Jitter:          true,
		}
		agentOpts = append(agentOpts,
			llmagent.WithToolCallRetryPolicy(retryPolicy),
		)
	}
	if cfg.Config.Agent.EnablePostToolPrompt {
		agentOpts = append(agentOpts,
			llmagent.WithEnablePostToolPrompt(true),
		)
	}
	if cfg.Config.Agent.ContextCompaction {
		agentOpts = append(agentOpts,
			buildContextCompactionOptions(cfg.Config.Agent)...)
	}

	// Session recall: inject previous session context
	// into the system prompt for cross-session awareness.
	if cfg.Config.Agent.SessionRecallEnabled {
		limit := cfg.Config.Agent.SessionRecallLimit
		if limit <= 0 {
			limit = 5
		}
		agentOpts = append(agentOpts,
			llmagent.WithPreloadSessionRecall(limit),
		)
	}

	// Configure Planner for structured planning and reasoning.
	// BuiltinPlanner: for models with native thinking (Claude, Gemini)
	// ReActPlanner: for models without thinking (legacy OpenAI, local models)
	switch cfg.Config.Agent.Planner {
	case "builtin":
		plannerOpts := builtin.Options{}
		if cfg.Config.Agent.ReasoningEffort != "" {
			effort := cfg.Config.Agent.ReasoningEffort
			plannerOpts.ReasoningEffort = &effort
		}
		plannerOpts.ThinkingEnabled = cfg.Config.Agent.ThinkingEnabled
		plannerOpts.ThinkingTokens = cfg.Config.Agent.ThinkingTokens
		planner := builtin.New(plannerOpts)
		agentOpts = append(agentOpts, llmagent.WithPlanner(planner))
	case "react":
		planner := react.New()
		agentOpts = append(agentOpts, llmagent.WithPlanner(planner))
	}

	agentCallbacks := buildAgentCallbacks(cfg.Config)
	if agentCallbacks != nil {
		agentOpts = append(agentOpts,
			llmagent.WithAgentCallbacks(agentCallbacks),
		)
	}
	toolCallbacks := buildToolCallbacks(
		cfg.SecurityGuard, cfg.Hooks, cfg.Capabilities,
		cfg.Config.Agent.CommandValidationMode,
	)
	if toolCallbacks != nil {
		agentOpts = append(agentOpts,
			llmagent.WithToolCallbacks(toolCallbacks),
		)
	}
	modelCallbacks := buildModelCallbacks()
	if modelCallbacks != nil {
		agentOpts = append(agentOpts,
			llmagent.WithModelCallbacks(modelCallbacks),
		)
	}

	return llmagent.New("wukong", agentOpts...), nil
}

// buildContextCompactionOptions translates the agent's context-compaction
// config fields into llmagent options. Returns an empty slice (with the
// enable flag) when compaction is on but no thresholds are configured,
// in which case only Pass 1 (placeholder) runs with the framework
// default. Extracted from createSingleAgent to flatten the nested
// conditional block there.
// buildContextCompactionOptions translates the agent's context-compaction
// config fields into llmagent options. Returns an empty slice (with the
// enable flag) when compaction is on but no thresholds are configured,
// in which case only Pass 1 (placeholder) runs with the framework
// default. Extracted from createSingleAgent to flatten the nested
// conditional block there.
func buildContextCompactionOptions(agentCfg config.AgentConfig) []llmagent.Option {
	var opts []llmagent.Option
	opts = append(opts, llmagent.WithEnableContextCompaction(true))

	// Pass 1: Replace old oversized tool results with placeholder.
	// Default threshold is 1024 tokens if not configured.
	if agentCfg.ContextCompactionToolResultMaxTokens > 0 {
		opts = append(opts,
			llmagent.WithContextCompactionToolResultMaxTokens(
				agentCfg.ContextCompactionToolResultMaxTokens,
			),
		)
	}
	// Pass 2: Truncate head+tail of remaining large tool results.
	// Only active when explicitly configured (recommended: 8192).
	if agentCfg.ContextCompactionOversizedMaxTokens > 0 {
		opts = append(opts,
			llmagent.WithContextCompactionOversizedToolResultMaxTokens(
				agentCfg.ContextCompactionOversizedMaxTokens,
			),
		)
	}
	// Protect recent requests from Pass 1 placeholder replacement.
	if agentCfg.ContextCompactionKeepRecentRequests > 0 {
		opts = append(opts,
			llmagent.WithContextCompactionKeepRecentRequests(
				agentCfg.ContextCompactionKeepRecentRequests,
			),
		)
	}
	// Per-tool compaction configuration: force-clean noisy tools,
	// exclude critical tools from compaction.
	if len(agentCfg.ContextCompactionForceCleanTools) > 0 ||
		len(agentCfg.ContextCompactionKeepTools) > 0 {
		tcc := &llmagent.ToolResultCompactionConfig{}
		if len(agentCfg.ContextCompactionForceCleanTools) > 0 {
			tcc.ForceCleanToolNames = agentCfg.ContextCompactionForceCleanTools
		}
		if len(agentCfg.ContextCompactionKeepTools) > 0 {
			tcc.KeepToolNames = agentCfg.ContextCompactionKeepTools
		}
		opts = append(opts, llmagent.WithToolResultCompactionConfig(tcc))
	}
	return opts
}

// buildSystemInstruction builds the complete system instruction.
// It combines prompt templates (if available), the base instruction,
// memory guidance, and optional Top of Mind persistent instructions.
// The framework placeholder {current_time} is injected via
// WithAddCurrentTime(true).
// buildSystemInstruction builds the complete system instruction.
// It combines prompt templates (if available), the base instruction,
// memory guidance, and optional Top of Mind persistent instructions.
// The framework placeholder {current_time} is injected via
// WithAddCurrentTime(true).
func buildSystemInstruction(
	topOfMind string,
	templateText string,
) string {
	// Use template if provided; otherwise fall back to hardcoded base.
	var base string
	if templateText != "" {
		base = templateText + "\n\n"
		base += "You are Wukong, a helpful and capable AI agent. " +
			"You have access to various tools that let you " +
			"interact with the user's system. " +
			"Respect the instructions above when using tools.\n\n"
	} else {
		base = "You are Wukong, a helpful and capable AI agent. " +
			"You have access to various tools that let you " +
			"interact with the user's system. " +
			"Use tools proactively to complete tasks. " +
			"If a tool call fails, analyze the error and " +
			"try a different approach. " +
			"Break complex tasks into smaller steps and " +
			"use the todo tools to track progress. " +
			"Prefer file_replace over file_write for targeted edits. " +
			"When executing commands, check their output carefully.\n\n" +

			// Memory guidance
			"Your memory about the user is automatically loaded " +
			"into this prompt at the start of each conversation. " +
			"\n\n**IMPORTANT — Memory Tools**: " +
			"You have memory tools available: " +
			"memory_add, memory_search, memory_update, " +
			"memory_delete, memory_load, memory_clear. " +
			"\n- Use **memory_add** immediately when the user " +
			"shares preferences, personal details, project info, " +
			"decisions, or important context. Do NOT wait — store it now. " +
			"\n- Use **memory_search** when the user asks " +
			"\"what do you remember\", \"what do you know about me\", " +
			"or needs past context. " +
			"\n- Use **memory_update** to correct outdated memories. " +
			"\n- Use **memory_load** to review all stored memories. " +
			"\n- This is critical for providing personalized, " +
			"context-aware assistance across sessions."
	}

	// Inject Top of Mind persistent instructions if available
	if topOfMind != "" {
		base += "\n\n" + topOfMind
	}

	return base
}

// extractMessageContent extracts text content from a model.Message.
// For single-content messages it returns msg.Content directly.
// For multi-part messages it concatenates all text parts.
// extractMessageContent extracts text content from a model.Message.
// For single-content messages it returns msg.Content directly.
// For multi-part messages it concatenates all text parts.
func extractMessageContent(msg model.Message) string {
	if msg.Content != "" {
		return msg.Content
	}
	var parts []string
	for _, part := range msg.ContentParts {
		if part.Text != nil && *part.Text != "" {
			parts = append(parts, *part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// buildAgentCallbacks creates agent-level callbacks for observability.
// These fire before and after each agent run, providing hooks for
// logging, metrics collection, and security auditing.
// effectiveToolSetsTools flattens the tools of the effective
// toolsets — used as the deferred population for the toolsearch
// plugin.
func effectiveToolSetsTools(cfg CoreLoopConfig) []tool.Tool {
	var out []tool.Tool
	ctx := context.Background()
	for _, ts := range effectiveToolSets(cfg) {
		out = append(out, ts.Tools(ctx)...)
	}
	return out
}

// recipeNamespace is the capability-bus namespace for recipe
// sub-agents (roadmap §6.3): "recipe.<name>", where <name> is the
// LLM tool name minus its "recipe-" prefix.
// recipeNamespace is the capability-bus namespace for recipe
// sub-agents (roadmap §6.3): "recipe.<name>", where <name> is the
// LLM tool name minus its "recipe-" prefix.
const recipeNamespace = "recipe"

// SyncRecipeCapabilities re-syncs the "recipe.*" namespace of the
// registry with the given recipe tool slice: stale entries are
// unregistered, then every tool is re-registered (timeout-wrapped
// like the allTools path so per-call deadlines survive the move
// from hand-aggregation to the registry). Used at startup and by
// the hot-reload callback (Phase C). Callers pass the fresh tool
// slice explicitly — during reload the toolset lock is held by
// Reload, so this must not read ts.tools.
// SyncRecipeCapabilities re-syncs the "recipe.*" namespace of the
// registry with the given recipe tool slice: stale entries are
// unregistered, then every tool is re-registered (timeout-wrapped
// like the allTools path so per-call deadlines survive the move
// from hand-aggregation to the registry). Used at startup and by
// the hot-reload callback (Phase C). Callers pass the fresh tool
// slice explicitly — during reload the toolset lock is held by
// Reload, so this must not read ts.tools.
func SyncRecipeCapabilities(
	reg *capability.Registry, tools []tool.Tool, timeout time.Duration,
) {
	reg.UnregisterNamespace(recipeNamespace)
	registered := 0
	for _, t := range tools {
		if t == nil {
			continue
		}
		decl := t.Declaration()
		if decl == nil || decl.Name == "" {
			continue
		}
		addr := recipeNamespace + "." +
			strings.TrimPrefix(decl.Name, "recipe-")
		var wrapped tool.Tool = t
		if timeout > 0 {
			if ct, ok := t.(tool.CallableTool); ok {
				wrapped = newTimeoutTool(ct, timeout)
			}
		}
		c, err := capability.FromToolMeta(
			addr, capability.SourceRecipe, wrapped, capability.ToolMeta{},
		)
		if err != nil {
			util.Logger.Warn("capability: recipe adapter failed",
				"address", addr, "error", err.Error())
			continue
		}
		if err := reg.Register(c); err != nil {
			util.Logger.Warn("capability: recipe registration skipped",
				"address", addr, "error", err.Error())
			continue
		}
		registered++
	}
	util.Logger.Info("capability: recipe namespace synced",
		"capabilities", registered)
}

// effectiveToolSets resolves the toolsets the agent framework
// consumes (roadmap P0-1). When the capability registry is wired it
// is the single aggregation source: extension tools, the
// session-assembled toolsets, and recipes (synced into the
// "recipe.*" namespace by SyncRecipeCapabilities) all appear in its
// snapshot, so the hand-assembled toolsets are dropped to avoid
// double registration. A nil Capabilities keeps the legacy
// behaviour byte-for-byte.
// effectiveToolSets resolves the toolsets the agent framework
// consumes (roadmap P0-1). When the capability registry is wired it
// is the single aggregation source: extension tools, the
// session-assembled toolsets, and recipes (synced into the
// "recipe.*" namespace by SyncRecipeCapabilities) all appear in its
// snapshot, so the hand-assembled toolsets are dropped to avoid
// double registration. A nil Capabilities keeps the legacy
// behaviour byte-for-byte.
func effectiveToolSets(cfg CoreLoopConfig) []tool.ToolSet {
	if cfg.Capabilities == nil {
		return cfg.ToolSets
	}
	return []tool.ToolSet{capability.RegistryToolSet(cfg.Capabilities)}
}

// buildToolCallbacks creates tool-level callbacks for security and
// observability. The security guard checks are performed here
// as a framework-level concern rather than in business logic.
// The hooks registry adds an extensible pre-tool-execute layer
// (observe/reject) on top of the built-in security gate; security
// runs first as a hard gate, then registered hooks run in order and
// the first to reject blocks the call.
// Command validation modes (agent.command_validation_mode, Phase C).
const (
	// CommandValidationHybrid: scope declarations are authoritative;
	// tools without declarations fall back to the legacy name
	// heuristic. The default — no security regression for setups
	// that do not declare scopes.
	CommandValidationHybrid = "hybrid"
	// CommandValidationDescriptor: declarations only. Tools without
	// a "shell" scope never validate — for fully declared setups.
	CommandValidationDescriptor = "descriptor"
	// CommandValidationHeuristic: legacy name matching only.
	CommandValidationHeuristic = "heuristic"
)

// commandToolNeedsValidation reports whether a tool call's command
// string must pass Guard.ValidateCommand (roadmap P0-1). The mode
// comes from agent.command_validation_mode ("" = hybrid):
//
//   - hybrid: capability scope declarations are authoritative
//     (declared non-shell tools are exempt from the heuristic);
//     undeclared tools fall back to the legacy isCommandTool
//     heuristic so external MCP tools keep coverage.
//   - descriptor: declarations only; undeclared tools never
//     validate. For setups that declare scopes for every
//     command-executing extension (external tools declare via
//     extensions[].tool_scopes).
//   - heuristic: legacy name matching only; declarations ignored.
