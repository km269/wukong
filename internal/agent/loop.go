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
	"trpc.group/trpc-go/trpc-agent-go/planner/builtin"
	"trpc.group/trpc-go/trpc-agent-go/planner/react"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/promptinjection"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/promptinjection/review"
	"trpc.group/trpc-go/trpc-agent-go/plugin/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	todotool "trpc.group/trpc-go/trpc-agent-go/tool/todo"
)

// tracerName is the OpenTelemetry tracer name for the agent package.
const tracerName = "wukong/agent"

// CoreLoop implements the main interactive agent execution cycle.
// It orchestrates the Runner, Session, Memory, and Tool systems
// to provide a Goose-like agent experience.
type CoreLoop struct {
	agent          agent.Agent
	runner         runner.Runner
	sessionService session.Service
	factory        *provider.Factory
	cfg            *config.WukongConfig
	contextMgr     *ContextManager
	security       *security.Guard
	recallStore    *recall.Store
	closeFn        func() error

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
	// MemoryClose is called when the CoreLoop closes to stop memory
	// auto-extraction workers. The shared database connection is NOT
	// closed here — it is managed by DatabasePool.
	MemoryClose func() error
	// DBPoolClose is called when the CoreLoop closes to properly
	// close the shared database pool after all services have shut
	// down their workers. This ensures all pending writes are flushed
	// and WAL is checkpointed before the process exits.
	DBPoolClose func() error
	// WorkingDir is the current working directory (for templates).
	WorkingDir string
	// SessionID and UserID are for template variable substitution.
	SessionID string
	UserID    string
}

// NewCoreLoop creates a new agent core loop.
func NewCoreLoop(cfg CoreLoopConfig) (*CoreLoop, error) {
	// Collect all tools
	var allTools []tool.Tool
	allTools = append(allTools, cfg.FunctionTools...)

	// Load YAML recipe sub-agents from configured directory.
	// Recipes are defined as structured YAML files and become
	// callable tools for the main agent.
	recipeToolSet := NewRecipeToolSet(
		cfg.Factory, &cfg.Config.Agent, allTools)
	if recipeToolSet != nil && len(recipeToolSet.tools) > 0 {
		allTools = append(allTools, recipeToolSet.tools...)
		cfg.ToolSets = append(cfg.ToolSets, recipeToolSet)
		util.Logger.Info("recipe: integrated sub-agents",
			"count", len(recipeToolSet.tools))
	}

	// Add tRPC-native todo_write tool for structured task tracking.
	// Tasks persist in Session state and survive across conversation turns.
	// Uses session.State (temp: prefix) per invocation branch for isolation.
	if cfg.Config.Agent.TodoToolEnabled {
		todoTool := todotool.New()
		allTools = append(allTools, todoTool)
		util.Logger.Info("todo_write tool enabled (tRPC-native, session-persisted)")
	}

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
		var singleErr error
		ag, singleErr = createSingleAgent(cfg, allTools)
		if singleErr != nil {
			return nil, fmt.Errorf(
				"create single agent: %w", singleErr)
		}
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

	// Configure Tool Search plugin for automatic tool filtering.
	// When enabled, the toolsearch plugin compresses the candidate
	// tool list (TopK) before each model call to reduce token cost.
	// This is registered at runner level so it applies to all agents.
	if cfg.Config.Agent.ToolSearchEnabled {
		mdl, err := cfg.Factory.CreateDefaultModel()
		if err == nil {
			maxTools := cfg.Config.Agent.ToolSearchMaxTools
			if maxTools <= 0 {
				maxTools = 20
			}
			ts, tsErr := toolsearch.New(mdl,
				toolsearch.WithMaxTools(maxTools),
				toolsearch.WithFailOpen(),
			)
			if tsErr != nil {
				util.Logger.Warn(
					"toolsearch creation failed, continuing without auto tool filtering",
					slog.String("error", tsErr.Error()),
				)
			} else {
				runnerOpts = append(runnerOpts,
					runner.WithPlugins(ts),
				)
				util.Logger.Info("toolsearch plugin enabled",
					slog.Int("max_tools", maxTools),
				)
			}
		}
	}

	// Configure Prompt Injection guardrail.
	// When enabled, user inputs are reviewed for injection attempts
	// before being passed to the agent. This creates a lightweight
	// agent+runner for the reviewer, separate from the main agent.
	if cfg.Config.Security.GuardrailEnabled {
		guardModel, gErr := cfg.Factory.CreateDefaultModel()
		if gErr == nil && guardModel != nil {
			guardRunner, grErr := createGuardrailRunner(
				guardModel, cfg.Config,
			)
			if grErr == nil {
				piReviewer, rErr := review.New(guardRunner)
				if rErr == nil {
					piPlugin, pErr := promptinjection.New(
						promptinjection.WithReviewer(piReviewer),
					)
					if pErr == nil {
						grPlugin, gErr2 := guardrail.New(
							guardrail.WithPromptInjection(piPlugin),
						)
						if gErr2 == nil {
							runnerOpts = append(runnerOpts,
								runner.WithPlugins(grPlugin),
							)
							util.Logger.Info(
								"guardrail plugin enabled (prompt injection detection)",
							)
						}
					}
				}
			}
		}
	}

	// Configure Todo Enforcer plugin.
	// When enabled, the enforcer checks that all pending todos are
	// completed before the agent delivers its final answer. This
	// ensures the agent doesn't forget incomplete subtasks.
	//
	// Since tRPC-Agent-Go v1.10.0 does not yet ship an official
	// todoenforcer extension, we use a lightweight in-house
	// implementation as a runner plugin.
	if cfg.Config.Agent.TodoEnforcerEnabled &&
		cfg.Config.Agent.TodoToolEnabled {
		runnerOpts = append(runnerOpts,
			runner.WithPlugins(newTodoEnforcer()),
		)
		util.Logger.Info(
			"todoenforcer plugin enabled (requires all todos completed)",
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
		agent:          ag,
		runner:         r,
		sessionService: cfg.SessionService,
		factory:        cfg.Factory,
		cfg:            cfg.Config,
		contextMgr:     ctxMgr,
		security:       guard,
		recallStore:    cfg.RecallStore,
		closeFn: func() error {
			var errs []error
			// 1. Close runner first — stops active runs and
			//    prevents new EnqueueAutoMemoryJob calls.
			//    This is critical: the runner produces extraction
			//    jobs; without it stopped, new jobs would keep
			//    arriving while memory workers are shutting down.
			if err := r.Close(); err != nil {
				errs = append(errs, err)
			}
			// 2. Close memory service — waits for in-flight
			//    extraction jobs to complete (up to 5s timeout),
			//    then stops auto-extract workers. The shared DB
			//    connection is NOT closed here; it is managed by
			//    the pool (step 5).
			if cfg.MemoryClose != nil {
				if err := cfg.MemoryClose(); err != nil {
					errs = append(errs, err)
				}
			}
			// 3. Close session service — stops summary workers,
			//    closes channels, releases session-level resources.
			if cfg.SessionService != nil {
				if closer, ok := any(cfg.SessionService).(interface{ Close() error }); ok {
					if err := closer.Close(); err != nil {
						errs = append(errs, err)
					}
				}
			}
			// 4. Flush and shut down telemetry (including
			//    Langfuse if enabled).
			if cfg.TelemetryShutdown != nil {
				if err := cfg.TelemetryShutdown(context.Background()); err != nil {
					errs = append(errs, err)
				}
			}
			// 5. Close the shared database pool LAST — after all
			//    services have stopped their workers and flushed
			//    their writes. This ensures no pending transactions
			//    are lost and the WAL is properly checkpointed.
			if cfg.DBPoolClose != nil {
				if err := cfg.DBPoolClose(); err != nil {
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

	runOpts := []agent.RunOption{}
	if l.cfg.Agent.JSONRepairEnabled {
		runOpts = append(runOpts,
			agent.WithToolCallArgumentsJSONRepairEnabled(true),
		)
	}
	events, err := l.runner.Run(ctx, userID, sessionID, message, runOpts...)
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
	var textBuilder strings.Builder
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
				textBuilder.WriteString(choice.Delta.Content)
			}
			// Count tool calls in this response
			toolCallCount += len(choice.Message.ToolCalls)
		}

		// Check for runner completion
		if evt.IsRunnerCompletion() {
			// Extract final result from state delta if available
			if evt.StateDelta != nil {
				if lastResp, ok := evt.StateDelta["last_response"]; ok {
					textBuilder.Reset()
					textBuilder.WriteString(string(lastResp))
				}
			}
		}
	}

	responseText = textBuilder.String()

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

// GetAgent returns the underlying agent for A2A server usage.
func (l *CoreLoop) GetAgent() agent.Agent {
	return l.agent
}

// GetSessionService returns the session service for external usage.
func (l *CoreLoop) GetSessionService() session.Service {
	return l.sessionService
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
		llmagent.WithInstruction(buildBaseInstruction("")),
		llmagent.WithAddCurrentTime(true),
	}
	return llmagent.New("wukong-a2a-"+name, opts...)
}

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
		cfg.Config, cfg.TopOfMindInstructions,
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

	// Preload user memories into system prompt so the agent
	// automatically knows about stored preferences and facts
	// at the start of each conversation turn. With a budget of 10,
	// small memory sets are loaded in full; larger sets use
	// search-based retrieval. This is critical for memory to work.
	agentOpts = append(agentOpts,
		llmagent.WithPreloadMemory(10),
	)

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
	if cfg.Config.Agent.ContextCompaction {
		agentOpts = append(agentOpts,
			llmagent.WithEnableContextCompaction(true),
		)
		// Pass 1: Replace old oversized tool results with placeholder.
		// Default threshold is 1024 tokens if not configured.
		if cfg.Config.Agent.ContextCompactionToolResultMaxTokens > 0 {
			agentOpts = append(agentOpts,
				llmagent.WithContextCompactionToolResultMaxTokens(
					cfg.Config.Agent.ContextCompactionToolResultMaxTokens,
				),
			)
		}
		// Pass 2: Truncate head+tail of remaining large tool results.
		// Only active when explicitly configured (recommended: 8192).
		if cfg.Config.Agent.ContextCompactionOversizedMaxTokens > 0 {
			agentOpts = append(agentOpts,
				llmagent.WithContextCompactionOversizedToolResultMaxTokens(
					cfg.Config.Agent.ContextCompactionOversizedMaxTokens,
				),
			)
		}
		// Protect recent requests from Pass 1 placeholder replacement.
		if cfg.Config.Agent.ContextCompactionKeepRecentRequests > 0 {
			agentOpts = append(agentOpts,
				llmagent.WithContextCompactionKeepRecentRequests(
					cfg.Config.Agent.ContextCompactionKeepRecentRequests,
				),
			)
		}
		// Per-tool compaction configuration: force-clean noisy tools,
		// exclude critical tools from compaction.
		if len(cfg.Config.Agent.ContextCompactionForceCleanTools) > 0 ||
			len(cfg.Config.Agent.ContextCompactionKeepTools) > 0 {
			tcc := &llmagent.ToolResultCompactionConfig{}
			if len(cfg.Config.Agent.ContextCompactionForceCleanTools) > 0 {
				tcc.ForceCleanToolNames = cfg.Config.Agent.ContextCompactionForceCleanTools
			}
			if len(cfg.Config.Agent.ContextCompactionKeepTools) > 0 {
				tcc.KeepToolNames = cfg.Config.Agent.ContextCompactionKeepTools
			}
			agentOpts = append(agentOpts,
				llmagent.WithToolResultCompactionConfig(tcc),
			)
		}
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

	return llmagent.New("wukong", agentOpts...), nil
}

// buildSystemInstruction builds the complete system instruction.
// It combines prompt templates (if available), the base instruction,
// memory guidance, and optional Top of Mind persistent instructions.
// The framework placeholder {current_time} is injected via
// WithAddCurrentTime(true).
func buildSystemInstruction(
	cfg *config.WukongConfig,
	topOfMind string,
	templateText string,
) string {
	_ = cfg

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
		"You also have memory tools " +
		"(memory_add, memory_search, memory_update, " +
		"memory_delete, memory_load, memory_clear). " +
		"Use them proactively to remember important user " +
		"preferences, facts, decisions, and context across " +
		"sessions. When the user tells you something about " +
		"themselves (preferences, name, goals, projects, " +
		"constraints), store it with memory_add. " +
		"Search with memory_search when you need to find " +
		"specific remembered information."
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

			// For file-access tools, check against .wukongignore
			if security.IsFileAccessTool(args.ToolName) &&
				len(args.Arguments) > 0 {
				paths := security.ExtractFilePathFromArgs(
					args.Arguments)
				for _, p := range paths {
					if err := guard.CheckFilePath(p); err != nil {
						return nil, fmt.Errorf(
							"file access blocked by "+
								".wukongignore: %w", err,
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
	var data map[string]any
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

// createGuardrailRunner creates a minimal runner for the prompt
// injection guardrail reviewer.
func createGuardrailRunner(
	mdl model.Model,
	cfg *config.WukongConfig,
) (runner.Runner, error) {
	_ = cfg
	reviewAgent := llmagent.New("guardrail-reviewer",
		llmagent.WithModel(mdl),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(256),
			Temperature: float64Ptr(0.0),
			Stream:      false,
		}),
		llmagent.WithMaxLLMCalls(1),
	)
	return runner.NewRunner(
		"wukong-guardrail", reviewAgent,
	), nil
}
