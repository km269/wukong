// Package agent provides the core agent loop and context management.
// This implements the interactive tool-calling cycle similar to Goose,
// built on top of tRPC-Agent-Go's Runner and LLMAgent.
// Enhanced with Context Revision, Security Guard, and Recall integration.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/recall"
	"github.com/km269/wukong/internal/security"
	"github.com/km269/wukong/internal/util"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// tracerName is the OpenTelemetry tracer name for the agent package.
const tracerName = "wukong/agent"

// CoreLoop implements the main interactive agent execution cycle.
// It orchestrates the Runner, Session, Memory, and Tool systems
// to provide a Goose-like agent experience.
type CoreLoop struct {
	runner      runner.Runner
	factory     *provider.Factory
	cfg         *config.WukongConfig
	contextMgr  *ContextManager
	security    *security.Guard
	recallStore *recall.Store
	closeFn     func() error

	mu     sync.RWMutex
	closed bool
}

// CoreLoopConfig holds the dependencies for creating a CoreLoop.
type CoreLoopConfig struct {
	Config         *config.WukongConfig
	Factory        *provider.Factory
	SessionService session.Service
	MemoryService  memory.Service
	ArtifactService artifact.Service
	ToolSets       []tool.ToolSet
	FunctionTools  []tool.Tool
	SecurityGuard  *security.Guard
	RecallStore    *recall.Store
	RevisionModel  RevisionModel
	// TopOfMindInstructions is the formatted persistent instruction block.
	// If non-empty, it is injected into the system instruction.
	TopOfMindInstructions string
	// TelemetryShutdown is called when the CoreLoop closes to flush
	// and shut down the OpenTelemetry tracer provider.
	TelemetryShutdown func(context.Context) error
}

// NewCoreLoop creates a new agent core loop.
func NewCoreLoop(cfg CoreLoopConfig) (*CoreLoop, error) {
	// Collect all tools
	var allTools []tool.Tool
	allTools = append(allTools, cfg.FunctionTools...)

	// Create the agent based on workflow mode
	var ag agent.Agent
	workflowMode := cfg.Config.Workflow.Mode
	if workflowMode != "" && workflowMode != "single" {
		// Use WorkflowBuilder for multi-mode orchestration
		builder, err := NewWorkflowBuilder(
			cfg.Factory, cfg.Config, allTools, cfg.ToolSets,
		)
		if err != nil {
			return nil, fmt.Errorf("create workflow builder: %w", err)
		}

		oc := &OrchestrationConfig{
			Mode:          WorkflowMode(workflowMode),
			MaxIterations: cfg.Config.Workflow.MaxIterations,
		}
		ag, err = builder.Build(context.Background(), oc)
		if err != nil {
			return nil, fmt.Errorf("build workflow agent: %w", err)
		}
	} else {
		// Standard single LLMAgent (existing behavior)
		ag = createSingleAgent(cfg, allTools)
	}

	if ag == nil {
		return nil, fmt.Errorf(
			"failed to create agent (workflow mode: %s)",
			workflowMode,
		)
	}

	// Create runner with session, memory, and artifact services
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
	if cfg.ArtifactService != nil {
		runnerOpts = append(runnerOpts,
			runner.WithArtifactService(cfg.ArtifactService),
		)
	}

	r := runner.NewRunner("wukong-app", ag, runnerOpts...)

	// Create context manager
	ctxMgr := NewContextManager(cfg.Config)

	// Wire revision model if provided
	if cfg.RevisionModel != nil {
		ctxMgr.GetEngine().SetRevisionModel(cfg.RevisionModel)
	}

	// Wire session service for context revision compression
	if cfg.SessionService != nil {
		ctxMgr.SetSessionService(cfg.SessionService)
	}

	// Use provided security guard or create default
	guard := cfg.SecurityGuard
	if guard == nil {
		guard = security.NewGuard(&cfg.Config.Security)
	}

	loop := &CoreLoop{
		runner:      r,
		factory:     cfg.Factory,
		cfg:         cfg.Config,
		contextMgr:  ctxMgr,
		security:    guard,
		recallStore: cfg.RecallStore,
		closeFn: func() error {
			var errs []error
			if err := r.Close(); err != nil {
				errs = append(errs, err)
			}
			if cfg.SessionService != nil {
				if closer, ok := interface{}(cfg.SessionService).(interface{ Close() error }); ok {
					if err := closer.Close(); err != nil {
						errs = append(errs, err)
					}
				}
			}
			// Flush and shut down telemetry
			if cfg.TelemetryShutdown != nil {
				if err := cfg.TelemetryShutdown(context.Background()); err != nil {
					errs = append(errs, err)
				}
			}
			if len(errs) > 0 {
				return fmt.Errorf("close errors: %v", errs)
			}
			return nil
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

	// Create a trace span for this agent run
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "agent.Run",
		trace.WithAttributes(
			attribute.String("user_id", userID),
			attribute.String("session_id", sessionID),
		),
	)
	defer span.End()

	// Apply context optimization before running
	ctx = l.contextMgr.PrepareContext(ctx, session.Key{
		AppName:   "wukong-app",
		UserID:    userID,
		SessionID: sessionID,
	})

	// Store user message for recall
	if l.recallStore != nil {
		content := extractMessageContent(message)
		_ = l.recallStore.StoreMessage(recall.ChatMessage{
			SessionID: sessionID,
			UserID:    userID,
			Role:      "user",
			Content:   content,
		})
	}

	events, err := l.runner.Run(ctx, userID, sessionID, message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
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
	// Create a span that wraps the full stream processing lifecycle
	tracer := otel.Tracer(tracerName)
	ctx, span := tracer.Start(ctx, "agent.RunStream",
		trace.WithAttributes(
			attribute.String("user_id", userID),
			attribute.String("session_id", sessionID),
		),
	)
	defer span.End()

	events, err := l.Run(ctx, userID, sessionID, message)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return "", err
	}

	var responseText string
	var allEvents []event.Event
	toolCallCount := 0
	var eventCount int

	for evt := range events {
		eventCount++
		allEvents = append(allEvents, *evt)

		// Notify callback
		if onEvent != nil {
			if err := onEvent(evt); err != nil {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
				return responseText, err
			}
		}

		// Check for errors
		if evt.Error != nil {
			span.SetStatus(codes.Error, evt.Error.Message)
			return responseText,
				fmt.Errorf("agent error: %s", evt.Error.Message)
		}

		// Collect streaming content
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			if choice.Delta.Content != "" {
				responseText += choice.Delta.Content
			}
			// Count tool calls in this response
			toolCallCount += len(choice.Message.ToolCalls)
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

	// Add metrics attributes to span
	span.SetAttributes(
		attribute.Int("event_count", eventCount),
		attribute.Int("tool_call_count", toolCallCount),
		attribute.Int("response_length", len(responseText)),
	)

	// Store assistant response for recall
	if l.recallStore != nil && responseText != "" {
		_ = l.recallStore.StoreMessage(recall.ChatMessage{
			SessionID: sessionID,
			UserID:    userID,
			Role:      "assistant",
			Content:   responseText,
		})
	}

	// Trigger context optimization after run with real events
	l.contextMgr.AfterRun(ctx, responseText, allEvents)

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

	// Create a span to track the shutdown process
	tracer := otel.Tracer(tracerName)
	_, span := tracer.Start(context.Background(), "agent.Close")
	defer span.End()

	if l.closeFn != nil {
		return l.closeFn()
	}
	return nil
}

// GetRunner returns the underlying runner for advanced usage.
func (l *CoreLoop) GetRunner() runner.Runner {
	return l.runner
}

// GetSecurityGuard returns the security guard.
func (l *CoreLoop) GetSecurityGuard() *security.Guard {
	return l.security
}

// GetContextManager returns the context manager.
func (l *CoreLoop) GetContextManager() *ContextManager {
	return l.contextMgr
}

// Ensure type compatibility check
var _ agent.Agent = (*llmagent.LLMAgent)(nil)

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
		llmagent.WithInstruction(buildBaseInstruction()),
		llmagent.WithAddCurrentTime(true),
	}
	return llmagent.New("wukong-a2a-"+name, opts...)
}

// createSingleAgent creates the standard single LLMAgent with all
// configured options. This preserves the original agent creation logic.
func createSingleAgent(
	cfg CoreLoopConfig, allTools []tool.Tool,
) agent.Agent {
	mdl, err := cfg.Factory.CreateDefaultModel()
	if err != nil {
		util.Logger.Warn("failed to create default model",
			"error", err.Error())
		return nil
	}
	if mdl == nil {
		util.Logger.Warn(
			"default model is nil, agent cannot be created")
		return nil
	}

	genConfig := provider.GetDefaultGenerationConfig(&cfg.Config.Agent)

	instructions := buildSystemInstruction(
		cfg.Config, cfg.TopOfMindInstructions,
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

	if len(allTools) > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithTools(allTools),
		)
	}
	if len(cfg.ToolSets) > 0 {
		agentOpts = append(agentOpts,
			llmagent.WithToolSets(cfg.ToolSets),
		)
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

	agentCallbacks := buildAgentCallbacks(cfg.Config)
	if agentCallbacks != nil {
		agentOpts = append(agentOpts,
			llmagent.WithAgentCallbacks(agentCallbacks),
		)
	}
	toolCallbacks := buildToolCallbacks(cfg.SecurityGuard)
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

	return llmagent.New("wukong", agentOpts...)
}

// buildSystemInstruction builds the complete system instruction.
// It combines the base instruction, memory guidance, and optional
// Top of Mind persistent instructions. The framework placeholder
// {current_time} is injected via WithAddCurrentTime(true).
func buildSystemInstruction(
	cfg *config.WukongConfig,
	topOfMind string,
) string {
	base := "You are Wukong, a helpful and capable AI agent. " +
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
		"You have access to memory tools " +
		"(memory_add, memory_search, memory_update, " +
		"memory_delete, memory_load, memory_clear). " +
		"Use them proactively to remember important user " +
		"preferences, facts, decisions, and context across " +
		"sessions. When the user tells you something about " +
		"themselves (preferences, name, goals, projects, " +
		"constraints), store it with memory_add. " +
		"At the start of each conversation, use memory_load " +
		"to recall what you already know about the user. " +
		"Search with memory_search when you need to find " +
		"specific remembered information."

	// Inject Top of Mind persistent instructions if available
	if topOfMind != "" {
		base += "\n\n" + topOfMind
	}

	return base
}

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
func buildAgentCallbacks(cfg *config.WukongConfig) *agent.Callbacks {
	if cfg == nil {
		return nil
	}
	callbacks := agent.NewCallbacks()
	// BeforeAgent: log the invocation start
	callbacks.RegisterBeforeAgent(
		func(ctx context.Context, args *agent.BeforeAgentArgs) (
			*agent.BeforeAgentResult, error,
		) {
			if args != nil && args.Invocation != nil {
				util.Logger.Debug("agent run starting",
					slog.String("invocation_id",
						args.Invocation.InvocationID),
				)
			}
			return nil, nil
		},
	)
	// AfterAgent: log completion and track metrics
	callbacks.RegisterAfterAgent(
		func(ctx context.Context, args *agent.AfterAgentArgs) (
			*agent.AfterAgentResult, error,
		) {
			if args != nil && args.Invocation != nil {
				util.Logger.Debug("agent run completed",
					slog.String("invocation_id",
						args.Invocation.InvocationID),
				)
				if args.Error != nil {
					util.Logger.Warn("agent run error",
						slog.String("invocation_id",
							args.Invocation.InvocationID),
						slog.String("error",
							args.Error.Error()),
					)
				}
			}
			return nil, nil
		},
	)
	return callbacks
}

// buildToolCallbacks creates tool-level callbacks for security and
// observability. The security guard checks are performed here
// as a framework-level concern rather than in business logic.
func buildToolCallbacks(guard *security.Guard) *tool.Callbacks {
	callbacks := tool.NewCallbacks()

	// BeforeTool: security validation before tool execution
	callbacks.RegisterBeforeTool(
		func(ctx context.Context, args *tool.BeforeToolArgs) (
			*tool.BeforeToolResult, error,
		) {
			if guard == nil {
				return nil, nil
			}

			// Check tool permission (denylist, allowlist, permission mode)
			if err := guard.CheckToolPermission(
				args.ToolName, nil,
			); err != nil {
				return nil, fmt.Errorf(
					"tool %q blocked by security: %w",
					args.ToolName, err,
				)
			}

			// Check if this operation needs user approval
			if guard.NeedsApproval(args.ToolName, args.Arguments) {
				return nil, fmt.Errorf(
					"tool %q requires user approval in %s mode",
					args.ToolName, guard.GetPermissionMode(),
				)
			}

			// For command-execution tools, validate the command
			if isCommandTool(args.ToolName) && len(args.Arguments) > 0 {
				cmd := extractCommandFromArgs(args.Arguments)
				if cmd != "" {
					if err := guard.ValidateCommand(cmd); err != nil {
						return nil, fmt.Errorf(
							"command blocked by security: %w", err,
						)
					}
				}
			}

			return nil, nil
		},
	)

	// AfterTool: result size monitoring and truncation
	callbacks.RegisterAfterTool(
		func(ctx context.Context, args *tool.AfterToolArgs) (
			*tool.AfterToolResult, error,
		) {
			if guard == nil {
				return nil, nil
			}

			// Monitor tool execution errors for security events
			if args.Error != nil {
				return nil, nil
			}

			return nil, nil
		},
	)

	return callbacks
}

// isCommandTool checks if a tool name corresponds to a command execution tool.
func isCommandTool(toolName string) bool {
	commandTools := []string{
		"bash", "execute_command", "run_command",
		"shell", "terminal", "command",
		"command_execute", // developer extension tool
	}
	for _, t := range commandTools {
		if strings.EqualFold(toolName, t) {
			return true
		}
	}
	return false
}

// extractCommandFromArgs extracts a command string from tool arguments JSON.
func extractCommandFromArgs(args []byte) string {
	// Try to extract common command field names from JSON
	var data map[string]interface{}
	if err := json.Unmarshal(args, &data); err != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "shell", "script"} {
		if val, ok := data[key]; ok {
			if s, ok := val.(string); ok {
				return s
			}
		}
	}
	return ""
}

// buildModelCallbacks creates model-level callbacks for token usage
// tracking and cost estimation.
func buildModelCallbacks() *model.Callbacks {
	callbacks := model.NewCallbacks()
	// BeforeModel: request-level pre-processing
	callbacks.RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (
			*model.BeforeModelResult, error,
		) {
			if args != nil && args.Request != nil {
				util.Logger.Debug("model request",
					slog.Int("message_count",
						len(args.Request.Messages)),
				)
			}
			return nil, nil
		},
	)
	// AfterModel: response-level post-processing and metrics
	callbacks.RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (
			*model.AfterModelResult, error,
		) {
			if args != nil && args.Response != nil {
				util.Logger.Debug("model response",
					slog.String("model",
						args.Response.Model),
				)
				// Track token usage if available
				if args.Response.Usage != nil {
					util.Logger.Debug("token usage",
						slog.Int("prompt_tokens",
							args.Response.Usage.PromptTokens),
						slog.Int("completion_tokens",
							args.Response.Usage.CompletionTokens),
						slog.Int("total_tokens",
							args.Response.Usage.TotalTokens),
					)
				}
			}
			return nil, nil
		},
	)
	return callbacks
}
