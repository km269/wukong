// Package agent provides the core agent loop and context management.
// This implements the interactive tool-calling cycle similar to Goose,
// built on top of tRPC-Agent-Go's Runner and LLMAgent.
package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// CoreLoop implements the main interactive agent execution cycle.
// It orchestrates the Runner, Session, Memory, and Tool systems
// to provide a Goose-like agent experience.
type CoreLoop struct {
	runner     runner.Runner
	factory    *provider.Factory
	cfg        *config.WukongConfig
	contextMgr *ContextManager
	closeFn    func() error

	mu     sync.RWMutex
	closed bool
}

// CoreLoopConfig holds the dependencies for creating a CoreLoop.
type CoreLoopConfig struct {
	Config         *config.WukongConfig
	Factory        *provider.Factory
	SessionService session.Service
	MemoryService  memory.Service
	ToolSets       []tool.ToolSet
	FunctionTools  []tool.Tool
}

// NewCoreLoop creates a new agent core loop.
func NewCoreLoop(cfg CoreLoopConfig) (*CoreLoop, error) {
	// Create model
	mdl, err := cfg.Factory.CreateDefaultModel()
	if err != nil {
		return nil, fmt.Errorf("create model: %w", err)
	}

	genConfig := provider.GetDefaultGenerationConfig(&cfg.Config.Agent)

	// Collect all tools
	var allTools []tool.Tool
	allTools = append(allTools, cfg.FunctionTools...)

	// Build agent options
	agentOpts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithDescription(
			"Wukong AI Agent - A local-first extensible AI " +
				"assistant that can use tools to read files, " +
				"execute commands, search code, and complete " +
				"complex tasks autonomously.",
		),
		llmagent.WithInstruction(
			"You are Wukong, a helpful and capable AI agent. " +
				"You have access to various tools that let you " +
				"interact with the user's system. " +
				"Use tools proactively to complete tasks. " +
				"If a tool call fails, analyze the error and " +
				"try a different approach. " +
				"Break complex tasks into smaller steps and " +
				"use the todo tools to track progress.",
		),
	}

	// Add tools if any
	if len(allTools) > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithTools(allTools),
		)
	}

	// Add tool sets if any
	if len(cfg.ToolSets) > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithToolSets(cfg.ToolSets),
		)
	}

	// Set limits
	if cfg.Config.Agent.MaxLLMCalls > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithMaxLLMCalls(
				cfg.Config.Agent.MaxLLMCalls,
			),
		)
	}
	if cfg.Config.Agent.MaxToolIterations > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithMaxToolIterations(
				cfg.Config.Agent.MaxToolIterations,
			),
		)
	}

	// Enable parallel tools if configured
	if cfg.Config.Agent.ParallelTools {
		agentOpts = append(agentOpts,
			llmagent.WithEnableParallelTools(true),
		)
	}

	// Create the agent
	ag := llmagent.New("wukong", agentOpts...)

	// Create runner with session and memory
	runnerOpts := []runner.Option{}
	if cfg.SessionService != nil {
		runnerOpts = append(runnerOpts,
			runner.WithSessionService(cfg.SessionService),
		)
	}
	if cfg.MemoryService != nil {
		runnerOpts = append(runnerOpts,
			runner.WithMemoryService(cfg.MemoryService),
		)
	}

	r := runner.NewRunner("wukong-app", ag, runnerOpts...)

	// Create context manager
	ctxMgr := NewContextManager(cfg.Config)

	loop := &CoreLoop{
		runner:     r,
		factory:    cfg.Factory,
		cfg:        cfg.Config,
		contextMgr: ctxMgr,
		closeFn: func() error {
			return r.Close()
		},
	}

	return loop, nil
}

// Run executes a single user message and returns the event stream.
// The returned channel emits events including tool calls, streaming
// content, and final completion.
func (l *CoreLoop) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
) (<-chan *event.Event, error) {
	l.mu.RLock()
	if l.closed {
		l.mu.RUnlock()
		return nil, fmt.Errorf("core loop is closed")
	}
	l.mu.RUnlock()

	// Apply context optimization before running
	ctx = l.contextMgr.PrepareContext(ctx)

	events, err := l.runner.Run(ctx, userID, sessionID, message)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}

	return events, nil
}

// RunStream processes streaming events and extracts the final response.
// Returns the complete assistant response text and calls onEvent
// for each event emitted.
func (l *CoreLoop) RunStream(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	onEvent func(evt *event.Event) error,
) (string, error) {
	events, err := l.Run(ctx, userID, sessionID, message)
	if err != nil {
		return "", err
	}

	var responseText string

	for evt := range events {
		// Notify callback
		if onEvent != nil {
			if err := onEvent(evt); err != nil {
				return responseText, err
			}
		}

		// Check for errors
		if evt.Error != nil {
			return responseText,
				fmt.Errorf("agent error: %s", evt.Error.Message)
		}

		// Collect streaming content
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			if choice.Delta.Content != "" {
				responseText += choice.Delta.Content
			}
		}

		// Check for runner completion
		if evt.IsRunnerCompletion() {
			// Extract final result from state delta if available
			if evt.StateDelta != nil {
				if lastResp, ok := evt.StateDelta["last_response"]; ok {
					responseText = string(lastResp)
				}
			}
		}
	}

	// Trigger context optimization after run
	l.contextMgr.AfterRun(ctx, responseText)

	return responseText, nil
}

// RunUserMessage is a convenience method that handles the complete
// lifecycle of a user message: prepare context, run agent, and
// return the final response text.
func (l *CoreLoop) RunUserMessage(
	ctx context.Context,
	userID string,
	sessionID string,
	content string,
) (string, error) {
	msg := model.NewUserMessage(content)
	return l.RunStream(ctx, userID, sessionID, msg, nil)
}

// Close shuts down the agent loop and releases resources.
func (l *CoreLoop) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true

	if l.closeFn != nil {
		return l.closeFn()
	}
	return nil
}

// GetRunner returns the underlying runner for advanced usage.
func (l *CoreLoop) GetRunner() runner.Runner {
	return l.runner
}

// Ensure type compatibility check
var _ agent.Agent = (*llmagent.LLMAgent)(nil)
