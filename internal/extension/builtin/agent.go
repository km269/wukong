// Package builtin provides built-in extensions for wukong.
package builtin

import (
	"context"
	"log/slog"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// AgentToolSet provides wrapped sub-agents as tools.
// This enables the main agent to delegate tasks to expert
// sub-agents via standard tool calls, with streaming support
// for real-time user feedback.
type AgentToolSet struct {
	tools     []tool.Tool
	subAgents []agent.Agent
	inited    bool
	closed    bool
}

// NewAgentToolSet creates a tool set that wraps pre-built
// specialized sub-agents. Each sub-agent becomes a callable
// tool for the parent agent, enabling hierarchical delegation.
//
// The sub-agents use a shared model and lightweight configuration
// optimized for single-turn expert tasks.
//
// When agentCfg.AgentToolsStream is true, sub-agent responses are
// streamed to the user in real-time via WithStreamInner(true).
// When agentCfg.AgentToolsEnabled is false, an empty toolset is
// returned.
func NewAgentToolSet(
	factory *provider.Factory,
	agentCfg *config.AgentConfig,
) *AgentToolSet {
	if !agentCfg.AgentToolsEnabled {
		util.Logger.Info("agent_toolset: disabled by config")
		return &AgentToolSet{}
	}

	mdl, err := factory.CreateDefaultModel()
	if err != nil {
		util.Logger.Warn("agent_toolset: failed to create model for sub-agents",
			slog.String("error", err.Error()))
		return &AgentToolSet{}
	}

	ts := &AgentToolSet{}

	// Code Reviewer: specialized sub-agent for code quality review
	codeReviewer := llmagent.New("code-reviewer",
		llmagent.WithModel(mdl),
		llmagent.WithDescription("Expert code reviewer that analyzes "+
			"code for bugs, security issues, and improvements"),
		llmagent.WithInstruction(
			"You are an expert code reviewer. Given code or a file path, "+
				"analyze it thoroughly for: bugs, security vulnerabilities, "+
				"performance issues, style violations, edge cases, and "+
				"missing error handling. Be specific about what to fix and why. "+
				"Provide actionable suggestions in a clear, concise format.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   util.IntPtr(2048),
			Temperature: util.Float64Ptr(0.3),
			Stream:      false,
		}),
		llmagent.WithMaxLLMCalls(3),
	)

	// Summarizer: specialized sub-agent for condensing information
	summarizer := llmagent.New("summarizer",
		llmagent.WithModel(mdl),
		llmagent.WithDescription("Expert summarizer that condenses "+
			"long text into concise, structured summaries"),
		llmagent.WithInstruction(
			"You are an expert summarizer. Condense the given content "+
				"into a clear, structured summary. Preserve key facts, "+
				"decisions, and action items. Use bullet points for clarity. "+
				"Keep the summary focused and concise.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   util.IntPtr(1024),
			Temperature: util.Float64Ptr(0.3),
			Stream:      false,
		}),
		llmagent.WithMaxLLMCalls(2),
	)

	// Code Generator: specialized sub-agent for writing code
	codeGenerator := llmagent.New("code-generator",
		llmagent.WithModel(mdl),
		llmagent.WithDescription("Expert code generator that writes "+
			"clean, production-quality code"),
		llmagent.WithInstruction(
			"You are an expert code generator. Write clean, well-structured, "+
				"production-quality code. Include proper error handling, "+
				"documentation, and tests. Follow the language's best practices. "+
				"Return only the code with minimal explanation.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   util.IntPtr(4096),
			Temperature: util.Float64Ptr(0.2),
			Stream:      false,
		}),
		llmagent.WithMaxLLMCalls(3),
	)

	ts.subAgents = []agent.Agent{codeReviewer, summarizer, codeGenerator}

	// Wrap each sub-agent as a callable tool.
	// WithStreamInner: forward sub-agent events to parent (for TUI streaming).
	// WithResponseModeFinalOnly: only return the final complete message,
	//   avoiding intermediate planning/reasoning noise in tool results.
	streamInner := agentCfg.AgentToolsStream
	for _, ag := range ts.subAgents {
		t := agenttool.NewTool(ag,
			agenttool.WithSkipSummarization(false),
			agenttool.WithStreamInner(streamInner),
			agenttool.WithResponseMode(
				agenttool.ResponseModeFinalOnly,
			),
		)
		ts.tools = append(ts.tools, t)
	}

	util.Logger.Info("agent_toolset: registered sub-agent tools",
		slog.Int("count", len(ts.tools)),
		slog.Bool("streaming", streamInner))

	return ts
}

// Tools returns the sub-agent tools.
func (ts *AgentToolSet) Tools(_ context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *AgentToolSet) Name() string {
	return "agent_tools"
}

// Init initializes the tool set.
func (ts *AgentToolSet) Init(_ context.Context) error {
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *AgentToolSet) Close() error {
	ts.closed = true
	return nil
}
