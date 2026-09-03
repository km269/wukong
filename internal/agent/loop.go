// Package agent provides the core agent loop and context management.
// This implements the interactive tool-calling cycle similar to Goose,
// built on top of tRPC-Agent-Go's Runner and LLMAgent.
// Enhanced with Context Revision, Security Guard, and Recall integration.
package agent

import (
	"context"
	"errors"
	"fmt"
	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/cortex"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/recall"
	"github.com/km269/wukong/internal/security"
	wksession "github.com/km269/wukong/internal/session"
	"github.com/km269/wukong/internal/util"
	"go.opentelemetry.io/otel"
	"log/slog"
	"sync"
	"time"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
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
// CoreLoop implements the main interactive agent execution cycle.
// It orchestrates the Runner, Session, Memory, and Tool systems
// to provide a Goose-like agent experience.
type CoreLoop struct {
	agent          agent.Agent
	runner         runner.Runner
	sessionService session.Service
	memoryService  memory.Service
	factory        *provider.Factory
	cfg            *config.WukongConfig
	contextMgr     *ContextManager
	security       *security.Guard
	recallStore    *recall.Store
	cortexStore    *cortex.CortexStore // optional: HNSW vector sync
	memoryFlow     *cortex.MemoryFlowService
	graphFlow      *cortex.GraphFlowService // optional: KG auto-extract
	// modelEventLog records the messages the model actually sees
	// after enrichment, enforcing "model-visible means logged".
	modelEventLog *wksession.ModelEventLog
	// hooks is the waterfall registry for pre-step and
	// pre-tool-execute interception (dsh-style agent/pre-step
	// and tools/pre-execute extension points).
	hooks   *HookRegistry
	closeFn func() error

	mu     sync.RWMutex
	closed bool
	bgWg   sync.WaitGroup // tracks background goroutines for graceful shutdown
	runWg  sync.WaitGroup // tracks in-flight RunStream calls' synchronous
	// post-run side effects (recall/cortex/MemoryFlow writes) so
	// Close waits for them before closing the DB pool.
}

// CoreLoopConfig holds the dependencies for creating a CoreLoop.
// CoreLoopConfig holds the dependencies for creating a CoreLoop.
type CoreLoopConfig struct {
	Config          *config.WukongConfig
	Factory         *provider.Factory
	SessionService  session.Service
	MemoryService   memory.Service
	ArtifactService artifact.Service
	ToolSets        []tool.ToolSet
	FunctionTools   []tool.Tool
	// Capabilities is the unified capability registry (roadmap P0-1
	// Phase B). When non-nil it becomes the single aggregation
	// source for extension-sourced tools, replacing the
	// hand-assembled ToolSets (RecipeToolSets are preserved — see
	// effectiveToolSets). Nil keeps the legacy behaviour untouched.
	Capabilities  *capability.Registry
	SecurityGuard *security.Guard
	RecallStore   *recall.Store
	// CortexStore is an optional CortexDB-backed store for
	// HNSW vector indexing alongside FTS5 recall storage.
	CortexStore   *cortex.CortexStore
	RevisionModel provider.RevisionModel
	// MemoryFlowService provides CortexDB transcript recording
	// and wake-up context generation.
	MemoryFlowService *cortex.MemoryFlowService
	// GraphFlowService provides CortexDB GraphFlow for entity/
	// relationship extraction and knowledge graph construction.
	// When AutoExtract is enabled, extraction runs after each
	// conversation turn.
	GraphFlowService *cortex.GraphFlowService
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
	// EvolutionClose is called when the CoreLoop closes to stop the
	// skill evolution engine's background analysis worker. It must
	// be called before DBPoolClose since evolution uses the shared DB.
	EvolutionClose func() error
	// DBPoolClose is called when the CoreLoop closes to properly
	// close the shared database pool after all services have shut
	// down their workers. This ensures all pending writes are flushed
	// and WAL is checkpointed before the process exits.
	DBPoolClose func() error
	// ModelEventLog records model-visible events (the messages the
	// model actually sees after enrichment). When nil, the
	// "model-visible means logged" invariant is not enforced.
	ModelEventLog *wksession.ModelEventLog
	// Hooks is the waterfall registry for pre-step and
	// pre-tool-execute interception. When nil, an empty registry is
	// created and the loop runs without external pre-step/pre-tool
	// hooks (built-in enrichment still runs).
	Hooks *HookRegistry
	// WorkingDir is the current working directory (for templates).
	WorkingDir string
	// SessionID and UserID are for template variable substitution.
	SessionID string
	UserID    string
}

// NewCoreLoop creates a new agent core loop.
// NewCoreLoop creates a new agent core loop.
func NewCoreLoop(cfg CoreLoopConfig) (*CoreLoop, error) {
	// Initialize the waterfall hook registry early so it can be
	// threaded into createSingleAgent (for tool-callback wiring)
	// and attached to the loop. When the caller provides a
	// pre-populated registry (e.g. with external pre-step hooks),
	// it is reused as-is.
	hooks := cfg.Hooks
	if hooks == nil {
		hooks = NewHookRegistry()
	}
	cfg.Hooks = hooks

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

		// Phase C: register recipes as "recipe.*" capabilities and
		// keep the namespace in sync across hot reloads. With the
		// registry wired, the toolset itself is dropped from the
		// aggregation (see effectiveToolSets) so recipes appear in
		// the manifest exactly once.
		if cfg.Capabilities != nil {
			reg, timeout := cfg.Capabilities, cfg.Config.Agent.ToolCallTimeout
			SyncRecipeCapabilities(reg, recipeToolSet.tools, timeout)
			recipeToolSet.SetReloadCallback(func(tools []tool.Tool) {
				SyncRecipeCapabilities(reg, tools, timeout)
			})
		}
	}

	// P0-2: declarative flow DSL — YAML flows (agent + capability
	// nodes, conditional edges) become callable tools and "flow.*"
	// capabilities, hot-reloaded like recipes.
	flowToolSet := NewFlowToolSet(
		cfg.Factory, &cfg.Config.Agent, cfg.Capabilities)
	if flowToolSet != nil && len(flowToolSet.tools) > 0 {
		allTools = append(allTools, flowToolSet.tools...)
		cfg.ToolSets = append(cfg.ToolSets, flowToolSet)
		util.Logger.Info("flow: integrated declarative flows",
			"count", len(flowToolSet.tools))
		if cfg.Capabilities != nil {
			reg := cfg.Capabilities
			SyncFlowCapabilities(reg, flowToolSet.tools)
			flowToolSet.SetReloadCallback(func(tools []tool.Tool) {
				SyncFlowCapabilities(reg, tools)
			})
		}
	}

	// Add tRPC-native todo_write tool for structured task tracking.
	// Tasks persist in Session state and survive across conversation turns.
	// Uses session.State (temp: prefix) per invocation branch for isolation.
	if cfg.Config.Todo.EnableNativeTodo {
		todoTool := todotool.New()
		allTools = append(allTools, todoTool)
		util.Logger.Info("todo_write tool enabled (tRPC-native, session-persisted)")
	}

	// Apply a per-tool-call deadline so a single slow or hung tool
	// (e.g. a web fetch to a heavy site) cannot consume the entire
	// run budget and trigger a gateway-level context deadline. The
	// newTimeoutTool wrapper (defined in recipe_advance.go) is a
	// no-op when ToolCallTimeout <= 0.
	if cfg.Config.Agent.ToolCallTimeout > 0 {
		wrapped := 0
		for i, t := range allTools {
			if ct, ok := t.(tool.CallableTool); ok {
				allTools[i] = newTimeoutTool(ct, cfg.Config.Agent.ToolCallTimeout)
				wrapped++
			}
		}
		if wrapped > 0 {
			util.Logger.Info("agent: per-tool call timeout applied",
				slog.Int("tools", wrapped),
				slog.Duration("timeout", cfg.Config.Agent.ToolCallTimeout))
		}
	}

	// Create the agent based on workflow mode
	var ag agent.Agent
	workflowMode := cfg.Config.Workflow.Mode
	if workflowMode != "" && workflowMode != "single" {
		// Use WorkflowBuilder for multi-mode orchestration
		builder, err := NewWorkflowBuilder(
			cfg.Factory, cfg.Config, allTools, effectiveToolSets(cfg),
		)
		if err != nil {
			return nil, fmt.Errorf("create workflow builder: %w", err)
		}

		oc := &OrchestrationConfig{
			Mode:          WorkflowMode(workflowMode),
			MaxIterations: cfg.Config.Workflow.MaxIterations,
		}
		buildCtx, buildCancel := context.WithTimeout(
			context.Background(), 30*time.Second,
		)
		defer buildCancel()
		ag, err = builder.Build(buildCtx, oc)
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

	// Configure Tool Search plugin (trpc-agent-go v1.11 deferred-tools
	// model). When enabled, tools are hidden behind a tool_search
	// function plus a system-prompt catalog and loaded on demand —
	// the framework's replacement for the old LLM TopK compression.
	// Registered at runner level so it applies to all agents.
	if cfg.Config.Agent.ToolSearchEnabled {
		// Function tools (todo / recall / KG / import / summon) stay
		// preset — always visible to the model. Everything else
		// (capability-bus toolsets, recipes, flows) is deferred behind
		// tool_search. The two populations must be disjoint; a tool in
		// both makes the plugin drop its deferred registration with an
		// ERROR per call site.
		preset, deferred := buildToolsearchCandidates(cfg)
		maxTools := cfg.Config.Agent.ToolSearchMaxTools
		if maxTools <= 0 {
			maxTools = 20
		}
		ts := toolsearch.New(
			preset,
			toolsearch.WithMaxResults(maxTools),
			toolsearch.WithDeferredTools(deferred),
			toolsearch.WithEmbeddingFailOpen(),
		)
		runnerOpts = append(runnerOpts, runner.WithPlugins(ts))
		util.Logger.Info("toolsearch plugin enabled",
			slog.Int("max_results", maxTools),
			slog.Int("preset_tools", len(preset)),
			slog.Int("deferred_tools", len(deferred)),
		)
	}

	// Configure Prompt Injection guardrail.
	// When enabled, user inputs are reviewed for injection attempts
	// before being passed to the agent. This creates a lightweight
	// agent+runner for the reviewer, separate from the main agent.
	if cfg.Config.Security.GuardrailEnabled {
		if grPlugin := buildGuardrailPlugin(cfg.Factory); grPlugin != nil {
			runnerOpts = append(runnerOpts, runner.WithPlugins(grPlugin))
			util.Logger.Info(
				"guardrail plugin enabled (prompt injection detection)",
			)
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
	if cfg.Config.Todo.EnableEnforcer &&
		cfg.Config.Todo.EnableNativeTodo {
		runnerOpts = append(runnerOpts,
			runner.WithPlugins(newTodoEnforcer()),
		)
		util.Logger.Info(
			"todoenforcer plugin enabled (requires all todos completed)",
		)
	}

	runnerOpts = append(runnerOpts,
		runner.WithPlugins(newEvolutionTracker()),
	)

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
		memoryService:  cfg.MemoryService,
		factory:        cfg.Factory,
		cfg:            cfg.Config,
		contextMgr:     ctxMgr,
		security:       guard,
		recallStore:    cfg.RecallStore,
		cortexStore:    cfg.CortexStore,
		memoryFlow:     cfg.MemoryFlowService,
		graphFlow:      cfg.GraphFlowService,
		modelEventLog:  cfg.ModelEventLog,
		hooks:          hooks,
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
			// 2. Close evolution engine — stops background
			//    analysis worker and waits for in-flight jobs.
			if cfg.EvolutionClose != nil {
				if err := cfg.EvolutionClose(); err != nil {
					errs = append(errs, err)
				}
			}
			// 3. Close memory service — waits for in-flight
			//    extraction jobs to complete (up to 5s timeout),
			//    then stops auto-extract workers. The shared DB
			//    connection is NOT closed here; it is managed by
			//    the pool (step 6).
			if cfg.MemoryClose != nil {
				if err := cfg.MemoryClose(); err != nil {
					errs = append(errs, err)
				}
			}
			// 4. Close session service — stops summary workers,
			//    closes channels, releases session-level resources.
			if cfg.SessionService != nil {
				if closer, ok := any(cfg.SessionService).(interface{ Close() error }); ok {
					if err := closer.Close(); err != nil {
						errs = append(errs, err)
					}
				}
			}
			// 4b. Close GraphFlow service — stops the knowledge
			//     graph extraction engine and its CortexDB.
			if cfg.GraphFlowService != nil {
				if err := cfg.GraphFlowService.Close(); err != nil {
					errs = append(errs, err)
				}
			}
			// 5. Flush and shut down telemetry (including
			//    Langfuse if enabled). Use a short timeout to
			//    prevent shutdown from hanging on telemetry issues.
			if cfg.TelemetryShutdown != nil {
				tsCtx, tsCancel := context.WithTimeout(
					context.Background(), 10*time.Second,
				)
				defer tsCancel()
				if err := cfg.TelemetryShutdown(tsCtx); err != nil {
					errs = append(errs, err)
				}
			}
			// 6. Close the shared database pool LAST — after all
			//    services have stopped their workers and flushed
			//    their writes. This ensures no pending transactions
			//    are lost and the WAL is properly checkpointed.
			if cfg.DBPoolClose != nil {
				if err := cfg.DBPoolClose(); err != nil {
					errs = append(errs, err)
				}
			}
			if len(errs) > 0 {
				return fmt.Errorf("close errors: %w", errors.Join(errs...))
			}
			return nil
		},
	}

	return loop, nil
}

// Hooks returns the waterfall hook registry. Callers use this to
// register PreStepHook / PreToolExecuteHook extensions at runtime
// (e.g. from MCP servers, plugins, or product embedding layers),
// mirroring dsh's agent/pre-step and tools/pre-execute seams.
// Hooks returns the waterfall hook registry. Callers use this to
// register PreStepHook / PreToolExecuteHook extensions at runtime
// (e.g. from MCP servers, plugins, or product embedding layers),
// mirroring dsh's agent/pre-step and tools/pre-execute seams.
func (l *CoreLoop) Hooks() *HookRegistry {
	if l == nil {
		return nil
	}
	return l.hooks
}

// ModelEventLog returns the model-visible event log, or nil when
// disabled. Callers can replay a session's model-visible messages
// via ReplayMessages / ReplayModelMessage.
// ModelEventLog returns the model-visible event log, or nil when
// disabled. Callers can replay a session's model-visible messages
// via ReplayMessages / ReplayModelMessage.
func (l *CoreLoop) ModelEventLog() *wksession.ModelEventLog {
	if l == nil {
		return nil
	}
	return l.modelEventLog
}

// preStepRejectTag is the event.Tag value that marks a pre-step
// rejection in the event stream. Consumers (TUI/gateway) inspect
// this to distinguish a policy rejection from a normal (possibly
// empty) completion, and read the rejecting hook name from
// Extensions["reject"].
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

// waitWithTimeout waits for a WaitGroup up to the given timeout.
// If the timeout fires, it logs a warning and returns, allowing the
// caller to proceed with shutdown instead of hanging forever.
// waitWithTimeout waits for a WaitGroup up to the given timeout.
// If the timeout fires, it logs a warning and returns, allowing the
// caller to proceed with shutdown instead of hanging forever.
func waitWithTimeout(wg *sync.WaitGroup, timeout time.Duration, name string) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		util.Logger.Warn("shutdown wait timed out, proceeding anyway",
			slog.String("wait_group", name),
			slog.Duration("timeout", timeout))
	}
}

// Close shuts down the agent loop and releases resources.
// Close shuts down the agent loop and releases resources.
func (l *CoreLoop) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true

	// Wait for in-flight RunStream synchronous side effects (e.g.
	// recall/cortex/MemoryFlow post-run writes) to finish before any
	// resource below is torn down. New Run calls are already rejected
	// because l.closed is true, so this quiesces the remaining writes.
	// Without this wait, those writes could hit a closed DB pool inside
	// closeFn (step 6), causing "database is closed" errors or lost data.
	//
	// A timeout is necessary because an in-flight RunStream may be
	// blocked on an unresponsive LLM HTTP call that ignores context
	// cancellation. Without a deadline, /exit would hang indefinitely.
	waitWithTimeout(&l.runWg, 5*time.Second, "runWg")

	// Wait for background goroutines (e.g. PromoteFacts) to finish
	// before proceeding with shutdown. This prevents database
	// access after connection close.
	waitWithTimeout(&l.bgWg, 5*time.Second, "bgWg")

	// Create a span to track the shutdown process with a timeout
	// to avoid hanging on telemetry export issues.
	tracer := otel.Tracer(tracerName)
	spanCtx, spanCancel := context.WithTimeout(
		context.Background(), 5*time.Second,
	)
	defer spanCancel()
	_, span := tracer.Start(spanCtx, "agent.Close")
	defer span.End()

	if l.closeFn != nil {
		return l.closeFn()
	}
	return nil
}

// GetRunner returns the underlying runner for advanced usage.
// GetRunner returns the underlying runner for advanced usage.
func (l *CoreLoop) GetRunner() runner.Runner {
	return l.runner
}

// GetAgent returns the underlying agent for A2A server usage.
// GetAgent returns the underlying agent for A2A server usage.
func (l *CoreLoop) GetAgent() agent.Agent {
	return l.agent
}

// GetSessionService returns the session service for external usage.
// GetSessionService returns the session service for external usage.
func (l *CoreLoop) GetSessionService() session.Service {
	return l.sessionService
}

// GetSecurityGuard returns the security guard.
// GetSecurityGuard returns the security guard.
func (l *CoreLoop) GetSecurityGuard() *security.Guard {
	return l.security
}

// GetContextManager returns the context manager.
// GetContextManager returns the context manager.
func (l *CoreLoop) GetContextManager() *ContextManager {
	return l.contextMgr
}

// Ensure type compatibility check
