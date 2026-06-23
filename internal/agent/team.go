// Package agent provides Team-based multi-agent orchestration
// using tRPC-Agent-Go's team package. Supports two modes:
//   - Coordinator: one coordinator agent delegates to members via AgentTool
//   - Swarm: agents transfer control directly, no central coordinator
//
// Also provides Claude Code Agent integration and enhanced Graph workflow.
package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/claudecode"
	"trpc.group/trpc-go/trpc-agent-go/agent/codex"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/team"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TeamBuilder creates Team-based multi-agent orchestrations using
// tRPC-Agent-Go's team package.
type TeamBuilder struct {
	factory   *provider.Factory
	cfg       *config.WukongConfig
	model     model.Model
	genConfig model.GenerationConfig
	tools     []tool.Tool
	toolSets  []tool.ToolSet
}

// NewTeamBuilder creates a new Team workflow builder.
func NewTeamBuilder(
	factory *provider.Factory,
	cfg *config.WukongConfig,
	tools []tool.Tool,
	toolSets []tool.ToolSet,
) (*TeamBuilder, error) {
	mdl, err := factory.CreateDefaultModel()
	if err != nil {
		return nil, fmt.Errorf("create team model: %w", err)
	}
	return &TeamBuilder{
		factory:   factory,
		cfg:       cfg,
		model:     mdl,
		genConfig: provider.GetDefaultGenerationConfig(&cfg.Agent),
		tools:     tools,
		toolSets:  toolSets,
	}, nil
}

// BuildCoordinatorTeam creates a Coordinator-style Team where one
// coordinator agent delegates to member agents via AgentTool.
//
// The coordinator receives all member agents as tools and can
// call them sequentially or in parallel to synthesize results.
func (b *TeamBuilder) BuildCoordinatorTeam(
	ctx context.Context,
) (agent.Agent, error) {
	members := b.buildTeamMembers()
	if len(members) == 0 {
		return nil, fmt.Errorf(
			"team_coordinator mode requires at least one team member")
	}

	coordinatorOpts := []llmagent.Option{
		llmagent.WithModel(b.model),
		llmagent.WithGenerationConfig(b.genConfig),
		llmagent.WithDescription("Team coordinator that delegates " +
			"to specialized members and synthesizes their results"),
		llmagent.WithInstruction(
			"You are a team coordinator. Your job is to understand " +
				"the user's request, delegate subtasks to the most " +
				"appropriate team members, and synthesize their " +
				"results into a coherent final answer. " +
				"Call multiple members in parallel when their tasks " +
				"are independent. Always produce a final consolidated " +
				"response for the user."),
		llmagent.WithEnableParallelTools(true),
	}
	if b.cfg.Agent.ContextCompaction {
		coordinatorOpts = append(coordinatorOpts,
			llmagent.WithEnableContextCompaction(true),
		)
	}
	coordinator := llmagent.New("team-coordinator", coordinatorOpts...)

	tm, err := team.New(coordinator, members)
	if err != nil {
		return nil, fmt.Errorf("create coordinator team: %w", err)
	}

	util.Logger.Info("team_coordinator: initialized",
		slog.Int("members", len(members)))
	return tm, nil
}

// BuildSwarm creates a Swarm-style Team where agents transfer
// control directly via transfer_to_agent, without a central
// coordinator.
func (b *TeamBuilder) BuildSwarm(
	ctx context.Context,
) (agent.Agent, error) {
	members := b.buildTeamMembers()
	if len(members) < 2 {
		return nil, fmt.Errorf(
			"team_swarm mode requires at least 2 team members")
	}

	entryAgent := members[0].Info().Name

	tm, err := team.NewSwarm("wukong-swarm", entryAgent, members,
		team.WithCrossRequestTransfer(true),
		team.WithSwarmIndependentAgents(),
	)
	if err != nil {
		return nil, fmt.Errorf("create swarm team: %w", err)
	}

	util.Logger.Info("team_swarm: initialized",
		slog.Int("members", len(members)),
		slog.String("entry", entryAgent))
	return tm, nil
}

// BuildClaudeCode creates a Claude Code CLI agent that wraps
// the local Claude Code installation.
func (b *TeamBuilder) BuildClaudeCode() (agent.Agent, error) {
	bin := b.cfg.Workflow.ClaudeCodeBin
	if bin == "" {
		bin = "claude"
	}

	ag, err := claudecode.New(
		claudecode.WithName("claude-code"),
		claudecode.WithBin(bin),
		claudecode.WithExtraArgs(
			"--permission-mode", "bypassPermissions"),
		claudecode.WithOutputFormat(
			claudecode.OutputFormatStreamJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("create claude code agent: %w", err)
	}

	util.Logger.Info("claude_code agent: initialized",
		slog.String("bin", bin))
	return ag, nil
}

// BuildCodex creates an OpenAI Codex CLI agent that wraps
// the local Codex CLI installation.
//
// The Codex CLI reads/writes files and executes commands directly
// on the host via "codex exec --json". Supports sandbox modes,
// MCP tools, multi-turn resume, and raw output hooks.
//
// Config-driven via workflow.codex_bin (default: "codex").
func (b *TeamBuilder) BuildCodex() (agent.Agent, error) {
	bin := b.cfg.Workflow.CodexBin
	if bin == "" {
		bin = "codex"
	}

	ag, err := codex.New(
		codex.WithName("codex"),
		codex.WithBin(bin),
		codex.WithGlobalArgs(
			"--sandbox", "workspace-write",
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create codex agent: %w", err)
	}

	util.Logger.Info("codex agent: initialized",
		slog.String("bin", bin))
	return ag, nil
}

// buildTeamMembers creates agent.Agent instances from config.
func (b *TeamBuilder) buildTeamMembers() []agent.Agent {
	cfgMembers := b.cfg.Workflow.TeamMembers
	if len(cfgMembers) > 0 {
		return b.createConfigMembers(cfgMembers)
	}
	return b.defaultTeamMembers()
}

func (b *TeamBuilder) createConfigMembers(
	cfgs []config.TeamMemberConfig,
) []agent.Agent {
	var members []agent.Agent
	for _, mc := range cfgs {
		opts := []llmagent.Option{
			llmagent.WithModel(b.model),
			llmagent.WithDescription(
				fmt.Sprintf("Team member: %s", mc.Name)),
			llmagent.WithInstruction(mc.Instruction),
			llmagent.WithGenerationConfig(
				model.GenerationConfig{
					Stream:      false,
					MaxTokens:   util.IntPtr(2048),
					Temperature: util.Float64Ptr(0.3),
				}),
			llmagent.WithMaxLLMCalls(5),
		}
		// Filter tools if specified.
		if !mc.AllTools && len(mc.AllowedTools) > 0 {
			filtered := filterToolsByName(b.tools, mc.AllowedTools)
			if len(filtered) > 0 {
				opts = append(opts, llmagent.WithTools(filtered))
			}
		} else if len(b.tools) > 0 {
			opts = append(opts, llmagent.WithTools(b.tools))
		}
		if len(b.toolSets) > 0 {
			opts = append(opts, llmagent.WithToolSets(b.toolSets))
		}
		members = append(members,
			llmagent.New(mc.Name, opts...))
	}
	return members
}

func (b *TeamBuilder) defaultTeamMembers() []agent.Agent {
	return []agent.Agent{
		llmagent.New("researcher",
			llmagent.WithModel(b.model),
			llmagent.WithDescription("Research specialist"),
			llmagent.WithInstruction(
				"You are a research specialist. Find and "+
					"present relevant information clearly."),
			llmagent.WithGenerationConfig(
				model.GenerationConfig{
					Stream:      false,
					MaxTokens:   util.IntPtr(2048),
					Temperature: util.Float64Ptr(0.3),
				}),
			llmagent.WithMaxLLMCalls(5),
			llmagent.WithTools(b.tools),
			llmagent.WithToolSets(b.toolSets),
		),
		llmagent.New("coder",
			llmagent.WithModel(b.model),
			llmagent.WithDescription("Coding specialist"),
			llmagent.WithInstruction(
				"You are a coding specialist. Write clean, "+
					"correct code. Use tools to create/modify files."),
			llmagent.WithGenerationConfig(
				model.GenerationConfig{
					Stream:      false,
					MaxTokens:   util.IntPtr(2048),
					Temperature: util.Float64Ptr(0.2),
				}),
			llmagent.WithMaxLLMCalls(5),
			llmagent.WithTools(b.tools),
			llmagent.WithToolSets(b.toolSets),
		),
		llmagent.New("reviewer",
			llmagent.WithModel(b.model),
			llmagent.WithDescription("Code/quality reviewer"),
			llmagent.WithInstruction(
				"You are a reviewer. Examine work for correctness, "+
					"completeness, and quality. Be specific."),
			llmagent.WithGenerationConfig(
				model.GenerationConfig{
					Stream:      false,
					MaxTokens:   util.IntPtr(2048),
					Temperature: util.Float64Ptr(0.3),
				}),
			llmagent.WithMaxLLMCalls(5),
			llmagent.WithTools(b.tools),
			llmagent.WithToolSets(b.toolSets),
		),
	}
}

func filterToolsByName(tools []tool.Tool, allowed []string) []tool.Tool {
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}
	var filtered []tool.Tool
	for _, t := range tools {
		if decl := t.Declaration(); decl != nil {
			if allowedSet[decl.Name] {
				filtered = append(filtered, t)
			}
		}
	}
	return filtered
}
