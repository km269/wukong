// Package agent provides the core agent loop and context management.
// This implements the interactive tool-calling cycle similar to Goose,
// built on top of tRPC-Agent-Go's Runner and LLMAgent.
// Enhanced with Context Revision, Security Guard, and Recall integration.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/cortex"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/recall"
	"github.com/km269/wukong/internal/security"
	wksession "github.com/km269/wukong/internal/session"
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
	"trpc.group/trpc-go/trpc-agent-go/plugin"
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
func (l *CoreLoop) Hooks() *HookRegistry {
	if l == nil {
		return nil
	}
	return l.hooks
}

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
const preStepRejectTag = "pre_step_reject"

// newPreStepRejectStream returns an event stream carrying a single
// rejection marker and then closes. The marker event has
// Tag=preStepRejectTag and the rejecting hook name in Extensions,
// with no Response content and no Error — so it flows through
// RunStream without triggering the error path or emitting model
// content. This implements dsh's "a rejected claim closes a durable
// turn that spent no step" semantic: the turn is normally closed
// (nil error) but carries a visible rejection marker.
func newPreStepRejectStream(reason string) <-chan *event.Event {
	ch := make(chan *event.Event, 1)
	reasonJSON, _ := json.Marshal(
		map[string]string{"reason": reason},
	)
	ch <- &event.Event{
		Author:    "system",
		Tag:       preStepRejectTag,
		Timestamp: time.Now(),
		Extensions: map[string]json.RawMessage{
			"reject": reasonJSON,
		},
	}
	close(ch)
	return ch
}

// IsPreStepReject reports whether an event is a pre-step rejection
// marker emitted by newPreStepRejectStream. Consumers use this to
// surface "blocked by policy" to the user instead of treating the
// empty stream as a silent no-op.
func IsPreStepReject(evt *event.Event) bool {
	return evt != nil && evt.Tag == preStepRejectTag
}

// PreStepRejectReason extracts the rejecting hook name from a
// pre-step rejection marker event. Returns "" if the event is not a
// rejection marker or carries no reason.
func PreStepRejectReason(evt *event.Event) string {
	if evt == nil || evt.Tag != preStepRejectTag {
		return ""
	}
	if evt.Extensions == nil {
		return ""
	}
	raw, ok := evt.Extensions["reject"]
	if !ok {
		return ""
	}
	var m struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return m.Reason
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

	// Record the turn boundary and the ORIGINAL user message before
	// any enrichment. This enforces the "model-visible means logged"
	// invariant at the turn start: the raw input is durable. Logging
	// is best-effort — a log failure must never block the model.
	if l.modelEventLog != nil {
		origContent := extractMessageContent(message)
		if err := l.modelEventLog.Append(ctx, sessionID, userID,
			wksession.EventTurnStart, "", origContent); err != nil {
			util.Logger.Warn("model event log: turn_start failed",
				slog.String("error", err.Error()))
		}
		if err := l.modelEventLog.Append(ctx, sessionID, userID,
			wksession.EventUserMessage, "", origContent); err != nil {
			util.Logger.Warn("model event log: user_message failed",
				slog.String("error", err.Error()))
		}
	}

	// Store user message for recall.
	if l.recallStore != nil {
		content := extractMessageContent(message)
		msg := recall.ChatMessage{
			SessionID: sessionID,
			UserID:    userID,
			Role:      "user",
			Content:   content,
		}
		if l.cortexStore != nil {
			if err := l.cortexStore.StoreMessage(ctx, msg); err != nil {
				util.Logger.Warn("cortex: store user message failed",
					slog.String("error", err.Error()))
			}
		} else {
			if err := l.recallStore.StoreMessage(msg); err != nil {
				util.Logger.Warn("recall: store user message failed",
					slog.String("error", err.Error()))
			}
		}
	}

	// Record transcript in MemoryFlow for context enrichment.
	var wakeCtx string
	if l.memoryFlow != nil {
		content := extractMessageContent(message)
		util.Logger.Info("memoryflow: starting ingest and wakeup",
			"sess", sessionID[:min(8, len(sessionID))],
			"user", userID,
			"input_chars", len(content))

		if err := l.memoryFlow.IngestTurn(
			ctx, sessionID, userID, "user", content,
		); err != nil {
			util.Logger.Warn("memoryflow: ingest user turn failed",
				slog.String("error", err.Error()))
		} else {
			util.Logger.Debug("memoryflow: ingest user turn succeeded",
				"sess", sessionID[:min(8, len(sessionID))],
				"user", userID)
		}

		// [Fix 1] Build wake-up context from past conversations
		// and inject it into the message as additional system context.
		identity := "You are Wukong, an AI coding assistant."
		wc, wErr := l.memoryFlow.WakeUp(
			ctx, identity, content, sessionID, userID,
		)
		if wErr != nil {
			util.Logger.Warn("memoryflow: wakeup failed",
				"sess", sessionID[:min(8, len(sessionID))],
				slog.String("error", wErr.Error()))
		} else if wc == "" {
			util.Logger.Info("memoryflow: wakeup returned empty",
				"sess", sessionID[:min(8, len(sessionID))],
				"reason", "no prior conversation history yet")
		} else {
			wakeCtx = wc
			preview := wc
			if len(wakeCtx) > 200 {
				preview = wakeCtx[:200] + "..."
			}
			util.Logger.Info("memoryflow: wakeup injected successfully",
				"sess", sessionID[:min(8, len(sessionID))],
				"chars", len(wakeCtx),
				"preview", preview)
			// Record the wake-up context that will be injected into
			// the model-visible message (context_inject event).
			if l.modelEventLog != nil {
				if err := l.modelEventLog.Append(ctx, sessionID, userID,
					wksession.EventContextInject, "wakeup", wakeCtx); err != nil {
					util.Logger.Warn("model event log: context_inject wakeup failed",
						slog.String("error", err.Error()))
				}
			}
			if message.Role == model.RoleUser {
				message = model.Message{
					Role: "user",
					Content: fmt.Sprintf(
						"[Context from past conversations]\n%s\n\n"+
							"[Current user message]\n%s",
						wakeCtx, extractMessageContent(message),
					),
				}
				util.Logger.Debug("memoryflow: message updated with wakeup context",
					"total_chars", len(message.Content))
			}
		}
	}

	// [Fix 2] Actively search recall history and inject into context.
	// This ensures cross-session chat history is always available,
	// complementing the LLM-driven recall_search tool usage.
	if l.recallStore != nil {
		content := extractMessageContent(message)
		var searchResults []recall.SearchResult
		var searchErr error

		if l.cortexStore != nil {
			searchResults, searchErr = l.cortexStore.Search(ctx, content, userID, 5)
		} else {
			searchResults, searchErr = l.recallStore.Search(content, userID, 5)
		}

		if searchErr != nil {
			util.Logger.Warn("recall: search failed",
				"sess", sessionID[:min(8, len(sessionID))],
				slog.String("error", searchErr.Error()))
		} else if len(searchResults) > 0 {
			util.Logger.Info("recall: found relevant history",
				"sess", sessionID[:min(8, len(sessionID))],
				"count", len(searchResults))

			var recallCtx strings.Builder
			recallCtx.WriteString("[Relevant conversation history]\n")
			for i, result := range searchResults {
				if result.Preview != "" {
					fmt.Fprintf(&recallCtx, "%d. %s\n", i+1, result.Preview)
				}
			}

			// Record the recall context that will be injected
			// (context_inject event).
			if l.modelEventLog != nil {
				if err := l.modelEventLog.Append(ctx, sessionID, userID,
					wksession.EventContextInject, "recall",
					recallCtx.String()); err != nil {
					util.Logger.Warn("model event log: context_inject recall failed",
						slog.String("error", err.Error()))
				}
			}

			if message.Role == model.RoleUser {
				origContent := extractMessageContent(message)
				message = model.Message{
					Role: "user",
					Content: fmt.Sprintf("%s\n\n%s",
						recallCtx.String(), origContent,
					),
				}
				util.Logger.Debug("recall: message updated with search results",
					"total_chars", len(message.Content))
			}
		} else {
			util.Logger.Debug("recall: no relevant history found",
				"sess", sessionID[:min(8, len(sessionID))])
		}
	}

	// [Fix 4] Inject persistent memories from tRPC Memory store
	// into the conversation context before each agent run.
	// Deduplicates against MemoryFlow wake-up context to avoid
	// redundant information from being injected twice.
	if l.memoryService != nil {
		userKey := memory.UserKey{
			AppName: "wukong-app",
			UserID:  userID,
		}
		util.Logger.Info("memory: reading persistent memories",
			"sess", sessionID[:min(8, len(sessionID))],
			"user", userID,
			"limit", 5)

		memories, mErr := l.memoryService.ReadMemories(
			ctx, userKey, 5,
		)
		if mErr != nil {
			util.Logger.Warn("memory: read failed",
				"sess", sessionID[:min(8, len(sessionID))],
				slog.String("error", mErr.Error()))
		} else if len(memories) == 0 {
			util.Logger.Info("memory: no persistent memories found",
				"sess", sessionID[:min(8, len(sessionID))],
				"reason", "cold start — auto_extract not yet triggered")
		} else {
			util.Logger.Debug("memory: found persistent memories",
				"sess", sessionID[:min(8, len(sessionID))],
				"count", len(memories))
			for i, m := range memories {
				if m.Memory != nil && m.Memory.Memory != "" {
					memPreview := m.Memory.Memory
					if len(memPreview) > 100 {
						memPreview = memPreview[:100] + "..."
					}
					util.Logger.Debug("memory: memory entry",
						"index", i+1,
						"content", memPreview,
						"id", m.ID)
				}
			}

			var memCtx strings.Builder
			memCtx.WriteString(
				"[Remembered facts from previous conversations]\n")
			var deduped int
			var dedupedMemories []string
			idx := 1
			for _, m := range memories {
				if m.Memory == nil || m.Memory.Memory == "" {
					continue
				}
				// Skip memories that are already substantially
				// present in the MemoryFlow wake-up context.
				if wakeCtx != "" &&
					isMemoryDuplicated(m.Memory.Memory, wakeCtx) {
					deduped++
					dedupedMemories = append(dedupedMemories, m.Memory.Memory)
					continue
				}
				fmt.Fprintf(&memCtx, "%d. %s\n",
					idx, m.Memory.Memory)
				idx++
			}

			if deduped > 0 {
				for i, dm := range dedupedMemories {
					dmPreview := dm
					if len(dmPreview) > 100 {
						dmPreview = dmPreview[:100] + "..."
					}
					util.Logger.Info("memory: deduplicated (already in wakeup)",
						"sess", sessionID[:min(8, len(sessionID))],
						"index", i+1,
						"content", dmPreview)
				}
			}

			injected := idx - 1
			if injected == 0 {
				util.Logger.Info("memory: all memories already "+
					"covered by wake-up context",
					"sess", sessionID[:min(8, len(sessionID))],
					"total", len(memories),
					"deduped", deduped)
			} else {
				util.Logger.Info("memory: persistent memories injected",
					"sess", sessionID[:min(8, len(sessionID))],
					"injected", injected,
					"total", len(memories),
					"deduped", deduped,
					"memctx_chars", memCtx.Len())
				// Record the persistent-memory context that will be
				// injected (context_inject event).
				if l.modelEventLog != nil {
					if err := l.modelEventLog.Append(ctx, sessionID, userID,
						wksession.EventContextInject, "persistent",
						memCtx.String()); err != nil {
						util.Logger.Warn("model event log: context_inject persistent failed",
							slog.String("error", err.Error()))
					}
				}
				// Prepend persistent memories to the user message.
				if message.Role == model.RoleUser {
					origContent := extractMessageContent(message)
					message = model.Message{
						Role: "user",
						Content: fmt.Sprintf("%s\n\n[User message]\n%s",
							memCtx.String(), origContent,
						),
					}
					util.Logger.Debug("memory: message updated with persistent memories",
						"total_chars", len(message.Content))
				}
			}

			// [Optimization] Record memory references after injection to track usage frequency.
			// This helps the SmartCleanup algorithm prioritize frequently-used memories.
			if len(memories) > 0 {
				var injectedIDs []string
				for _, m := range memories {
					if m.Memory != nil && m.Memory.Memory != "" {
						if wakeCtx == "" || !isMemoryDuplicated(m.Memory.Memory, wakeCtx) {
							injectedIDs = append(injectedIDs, m.ID)
						}
					}
				}
				if len(injectedIDs) > 0 {
					go func(uk memory.UserKey, ids []string) {
						bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if mm, ok := l.memoryService.(interface {
							BatchRecordMemoryReferences(context.Context, memory.UserKey, []string) error
						}); ok {
							if err := mm.BatchRecordMemoryReferences(bgCtx, uk, ids); err != nil {
								util.Logger.Debug("memory: record references failed",
									slog.String("error", err.Error()))
							}
						}
					}(userKey, injectedIDs)
				}
			}
		}
	}

	// Log final message structure for debugging
	finalContent := extractMessageContent(message)
	var contextTags []string
	if strings.Contains(finalContent, "[Context from past conversations]") {
		contextTags = append(contextTags, "wakeup")
	}
	if strings.Contains(finalContent, "[Remembered facts from previous conversations]") {
		contextTags = append(contextTags, "persistent")
	}
	if strings.Contains(finalContent, "[User message]") {
		contextTags = append(contextTags, "user_input")
	}
	util.Logger.Info("memory: final message context structure",
		"sess", sessionID[:min(8, len(sessionID))],
		"context_types", contextTags,
		"total_chars", len(finalContent))

	// Pre-step hooks: extensible waterfall over the enriched message.
	// Runs AFTER built-in enrichment (wakeup/recall/persistent) and
	// BEFORE runner.Run. A hook may rewrite the message the model is
	// about to see, or reject the turn. A rejected turn closes with
	// no step (no model request), matching dsh's "a rejected claim
	// closes a durable turn that spent no step" — we still record a
	// turn_end so the attempt is durable.
	if l.hooks != nil && l.hooks.HasPreStepHooks() {
		rewritten, reject, reason, herr := l.hooks.RunPreStep(
			ctx, sessionID, userID, message,
		)
		if herr != nil {
			span.SetStatus(codes.Error, herr.Error())
			span.RecordError(herr)
			return nil, fmt.Errorf("pre-step hooks: %w", herr)
		}
		if reject {
			if l.modelEventLog != nil {
				_ = l.modelEventLog.Append(ctx, sessionID, userID,
					wksession.EventTurnEnd,
					"pre_step_reject:"+reason, "")
			}
			// dsh semantics: a rejected claim closes a durable
			// turn that spent no step. Return a reject-marked
			// EMPTY event stream (one synthetic marker event,
			// then closed) with nil error — so callers can
			// distinguish "blocked by policy" from "runner
			// failed". The marker carries Tag=pre_step_reject
			// and the rejecting hook name in Extensions; consumers
			// (TUI/gateway) inspect evt.Tag to surface it. Run
			// already recorded turn_end above, so RunStream skips
			// its own turn_end when it sees the marker.
			span.SetAttributes(attribute.String(
				"pre_step_reject", reason))
			return newPreStepRejectStream(reason), nil
		}
		message = rewritten
	}

	// Record the FINAL model-visible message — the authoritative
	// witness for "model-visible means logged". This is exactly
	// what the model is about to see (post-enrichment, post-hooks).
	if l.modelEventLog != nil {
		finalVisible := extractMessageContent(message)
		if err := l.modelEventLog.Append(ctx, sessionID, userID,
			wksession.EventModelMessage, "", finalVisible); err != nil {
			util.Logger.Warn("model event log: model_message failed",
				slog.String("error", err.Error()))
		}
	}

	runOpts := []agent.RunOption{}
	if l.cfg.Agent.JSONRepairEnabled {
		runOpts = append(runOpts,
			agent.WithToolCallArgumentsJSONRepairEnabled(true),
		)
	}

	// Inject session/user identity into the ctx so the framework's
	// BeforeTool callback can attribute approval requests to this
	// turn. The async Approval gate reads this via
	// security.ApprovalContextFrom.
	ctx = security.WithApprovalContext(ctx, sessionID, userID)

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

	// Track this in-flight RunStream so Close() waits for the
	// synchronous post-run side effects (recall/cortex/MemoryFlow
	// writes, contextMgr.AfterRun) to finish before closing the DB
	// pool. Add only after Run succeeded: a closed/errored Run never
	// reaches the post-run write path, so it must not be tracked.
	l.runWg.Add(1)
	defer l.runWg.Done()

	var responseText string
	var textBuilder strings.Builder
	// rejected tracks whether this turn was blocked by a pre-step
	// hook before any model step ran. Run already recorded turn_end
	// for such turns (source=pre_step_reject), so RunStream must
	// skip its own turn_end recording to avoid a duplicate.
	rejected := false
	var allEvents []event.Event
	toolCallCount := 0
	var eventCount int

	for evt := range events {
		eventCount++
		allEvents = append(allEvents, *evt)

		// Detect a pre-step rejection marker early and short-circuit:
		// the marker is a synthetic control event with no embedded
		// *model.Response, so the normal evt.Error / evt.Response
		// processing below would dereference a nil pointer. We still
		// deliver it to onEvent (so TUI/gateway can surface the
		// rejection) and flag the turn so the normal turn_end
		// recording is skipped (Run already recorded it).
		if IsPreStepReject(evt) {
			rejected = true
			if onEvent != nil {
				if err := onEvent(evt); err != nil {
					span.SetStatus(codes.Error, err.Error())
					span.RecordError(err)
					return textBuilder.String(), err
				}
			}
			continue
		}

		// Notify callback
		if onEvent != nil {
			if err := onEvent(evt); err != nil {
				span.SetStatus(codes.Error, err.Error())
				span.RecordError(err)
				return textBuilder.String(), err
			}
		}

		// Check for errors
		if evt.Error != nil {
			span.SetStatus(codes.Error, evt.Error.Message)
			return textBuilder.String(),
				fmt.Errorf("agent error: %s", evt.Error.Message)
		}

		// Collect streaming content (skip tool response events).
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			// Skip tool response JSON from leaking into output.
			if choice.Message.Role != "tool" {
				if choice.Delta.Content != "" {
					textBuilder.WriteString(choice.Delta.Content)
				}
			}
			// Count tool calls in this response
			toolCallCount += len(choice.Message.ToolCalls)
		}

		// Check for runner completion
		if evt.IsRunnerCompletion() {
			// Extract final result from state delta if available
			// but only override if it's non-empty to avoid
			// losing incrementally collected delta content.
			if evt.StateDelta != nil {
				if lastResp, ok := evt.StateDelta["last_response"]; ok {
					lastRespStr := string(lastResp)
					if lastRespStr != "" {
						textBuilder.Reset()
						textBuilder.WriteString(lastRespStr)
					}
				}
			}
		}
	}

	// Fallback: if no delta content was collected, try reading
	// the final response from the last completion event's Message.Content.
	responseText = textBuilder.String()
	if responseText == "" && len(allEvents) > 0 {
		lastEvt := allEvents[len(allEvents)-1]
		if lastEvt.Response != nil &&
			len(lastEvt.Response.Choices) > 0 {
			responseText = lastEvt.Response.Choices[0].Message.Content
		}
	}

	// Add metrics attributes to span
	span.SetAttributes(
		attribute.Int("event_count", eventCount),
		attribute.Int("tool_call_count", toolCallCount),
		attribute.Int("response_length", len(responseText)),
	)

	// Store assistant response for recall.
	if l.recallStore != nil && responseText != "" {
		msg := recall.ChatMessage{
			SessionID: sessionID,
			UserID:    userID,
			Role:      "assistant",
			Content:   responseText,
		}
		if l.cortexStore != nil {
			if err := l.cortexStore.StoreMessage(ctx, msg); err != nil {
				util.Logger.Warn("cortex: store assistant message failed",
					slog.String("error", err.Error()))
			}
		} else {
			if err := l.recallStore.StoreMessage(msg); err != nil {
				util.Logger.Warn("recall: store assistant message failed",
					slog.String("error", err.Error()))
			}
		}
	}

	// Store tool call and tool response messages for recall.
	// Tool interactions contain valuable context (web search results,
	// command outputs, file contents) that enrich future searches.
	if l.recallStore != nil {
		for _, evt := range allEvents {
			if evt.Response == nil ||
				len(evt.Response.Choices) == 0 {
				continue
			}
			choice := evt.Response.Choices[0]
			// Store tool call requests.
			for _, tc := range choice.Message.ToolCalls {
				toolContent := fmt.Sprintf(
					"Tool: %s\nArgs: %s",
					tc.Function.Name,
					string(tc.Function.Arguments),
				)
				toolMsg := recall.ChatMessage{
					SessionID: sessionID,
					UserID:    userID,
					Role:      "tool_call",
					Content:   toolContent,
				}
				if l.cortexStore != nil {
					if err := l.cortexStore.StoreMessage(ctx, toolMsg); err != nil {
						util.Logger.Debug(
							"cortex: store tool call failed",
							slog.String("error", err.Error()))
					}
				} else {
					if err := l.recallStore.StoreMessage(toolMsg); err != nil {
						util.Logger.Debug(
							"recall: store tool call failed",
							slog.String("error", err.Error()))
					}
				}
			}
			// Store tool response content.
			if choice.Message.Role == "tool" &&
				choice.Message.Content != "" {
				toolResp := recall.ChatMessage{
					SessionID: sessionID,
					UserID:    userID,
					Role:      "tool_response",
					Content:   choice.Message.Content,
				}
				if l.cortexStore != nil {
					if err := l.cortexStore.StoreMessage(ctx, toolResp); err != nil {
						util.Logger.Debug(
							"cortex: store tool response failed",
							slog.String("error", err.Error()))
					}
				} else {
					if err := l.recallStore.StoreMessage(toolResp); err != nil {
						util.Logger.Debug(
							"recall: store tool response failed",
							slog.String("error", err.Error()))
					}
				}
			}
		}
	}

	// [Fix 2] Record assistant response in MemoryFlow transcript.
	// Use a bounded timeout so a slow embedder/indexer (e.g. first-time
	// gse dictionary load) doesn't block RunStream's return for too long.
	if l.memoryFlow != nil && responseText != "" {
		ingestCtx, ingestCancel := context.WithTimeout(ctx, 15*time.Second)
		if err := l.memoryFlow.IngestTurn(
			ingestCtx, sessionID, userID, "assistant", responseText,
		); err != nil {
			util.Logger.Warn("memoryflow: ingest assistant turn failed",
				slog.String("error", err.Error()))
		}
		ingestCancel()
	}

	// [Fix 3] Bridge MemoryFlow → tRPC Memory: promote extracted
	// facts from conversation to the persistent memory store.
	if l.memoryFlow != nil && responseText != "" {
		l.bgWg.Add(1)
		go func() {
			defer l.bgWg.Done()
			defer func() {
				if r := recover(); r != nil {
					util.Logger.Warn("memoryflow: promote panic",
						"error", fmt.Sprint(r))
				}
			}()
			bgCtx, cancel := context.WithTimeout(
				context.Background(), 60*time.Second,
			)
			defer cancel()
			candidates, err := l.memoryFlow.PromoteFacts(
				bgCtx, sessionID, userID,
			)
			if err != nil {
				util.Logger.Debug("memoryflow: promote skipped",
					"reason", err.Error())
				return
			}
			// Write promoted facts to the tRPC persistent memory
			// store so they survive across sessions.
			userKey := memory.UserKey{
				AppName: "wukong-app",
				UserID:  userID,
			}
			var stored int
			for _, c := range candidates {
				if c.Content == "" {
					continue
				}
				if l.memoryService == nil {
					util.Logger.Debug("memoryflow: promoted fact (not stored)",
						"kind", c.Kind,
						"content", c.Content[:min(80, len(c.Content))],
					)
					continue
				}
				// Determine topics from the candidate's kind
				// and collection.
				topics := []string{
					string(c.Kind),
					c.Collection,
				}
				if err := l.memoryService.AddMemory(
					bgCtx, userKey, c.Content, topics,
				); err != nil {
					util.Logger.Warn(
						"memoryflow: store fact failed",
						"kind", c.Kind,
						"error", err.Error(),
					)
					continue
				}
				stored++
				util.Logger.Debug("memoryflow: promoted fact stored",
					"kind", c.Kind,
					"content", c.Content[:min(80, len(c.Content))],
				)
			}
			if stored > 0 {
				util.Logger.Info("memoryflow: facts stored",
					"stored", stored,
					"total_candidates", len(candidates),
				)
			}
		}()
	}

	// GraphFlow auto-extract: when enabled, extract entities and
	// relationships from the conversation transcript and persist
	// them into the knowledge graph after each turn.
	if l.graphFlow != nil &&
		l.cfg.GraphFlow.Enabled &&
		l.cfg.GraphFlow.AutoExtract &&
		responseText != "" {
		l.bgWg.Add(1)
		go func() {
			defer l.bgWg.Done()
			defer func() {
				if r := recover(); r != nil {
					util.Logger.Warn("graphflow: auto-extract panic",
						"error", fmt.Sprint(r))
				}
			}()
			bgCtx, cancel := context.WithTimeout(
				context.Background(), 60*time.Second,
			)
			defer cancel()

			// Build transcript from collected events.
			var transcriptText strings.Builder
			transcriptText.WriteString(
				fmt.Sprintf("User: %s\n",
					extractMessageContent(message)))
			transcriptText.WriteString(
				fmt.Sprintf("Assistant: %s\n", responseText))

			result, err := l.graphFlow.ExtractFromTranscript(
				bgCtx, sessionID, transcriptText.String())
			if err != nil {
				util.Logger.Debug("graphflow: extract failed",
					slog.String("error", err.Error()))
				return
			}
			if result == nil || len(result.Nodes) == 0 {
				return
			}
			if err := l.graphFlow.BuildGraph(bgCtx, result); err != nil {
				util.Logger.Warn("graphflow: build graph failed",
					slog.String("error", err.Error()))
				return
			}
			util.Logger.Info("graphflow: knowledge graph updated",
				"nodes", len(result.Nodes),
				"edges", len(result.Edges),
				"session", sessionID[:min(8, len(sessionID))],
			)
		}()
	}

	// Trigger context optimization after run with real events
	l.contextMgr.AfterRun(ctx, responseText, allEvents)

	// Record the turn_end event — the assistant response the model
	// produced. This closes the durable turn in the model-visible
	// event log. Best-effort: a logging failure is not fatal.
	// Skipped for pre-step rejections: Run already recorded turn_end
	// (source=pre_step_reject) when it emitted the marker stream.
	if l.modelEventLog != nil && !rejected {
		if err := l.modelEventLog.Append(ctx, sessionID, userID,
			wksession.EventTurnEnd, "assistant", responseText); err != nil {
			util.Logger.Warn("model event log: turn_end failed",
				slog.String("error", err.Error()))
		}
	}

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
func buildToolCallbacks(
	guard *security.Guard, hooks *HookRegistry,
	caps *capability.Registry, validationMode string,
) *tool.Callbacks {
	callbacks := tool.NewCallbacks()

	// BeforeTool: security validation before tool execution
	callbacks.RegisterBeforeTool(
		func(ctx context.Context, args *tool.BeforeToolArgs) (
			*tool.BeforeToolResult, error,
		) {
			if guard != nil {
				// Check tool permission (denylist, allowlist, permission mode)
				if err := guard.CheckToolPermission(
					args.ToolName, nil,
				); err != nil {
					return nil, fmt.Errorf(
						"tool %q blocked by security: %w",
						args.ToolName, err,
					)
				}

				// Check if this operation needs user approval. When an
				// approval broker is wired on the guard, route through
				// the async human-in-the-loop Approval protocol; when no
				// broker is set, RequestApproval returns an immediate
				// denied response (legacy synchronous-deny behavior, so
				// safety never regresses when the feature is off).
				if guard.NeedsApproval(args.ToolName, args.Arguments) {
					sessionID, userID := security.ApprovalContextFrom(ctx)
					resp, aerr := guard.RequestApproval(
						ctx, sessionID, userID,
						args.ToolName, args.Arguments,
					)
					if aerr != nil {
						return nil, fmt.Errorf(
							"approval for %q failed: %w",
							args.ToolName, aerr,
						)
					}
					if resp == nil || !resp.Decision.IsAllowed() {
						decision := security.ApprovalDenied
						reason := "no approval broker configured"
						if resp != nil {
							decision = resp.Decision
							reason = resp.Reason
						}
						return nil, fmt.Errorf(
							"tool %q blocked by approval: %s (%s)",
							args.ToolName, decision, reason,
						)
					}
					// approved → fall through to command/file checks.
				}

				// For command-execution tools, validate the command.
				// Scope declarations are authoritative; undeclared
				// tools follow the configured fallback mode (see
				// commandToolNeedsValidation).
				if commandToolNeedsValidation(
					caps, validationMode, args.ToolName,
				) && len(args.Arguments) > 0 {
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
			}

			// Pre-tool-execute hooks: extensible observe/reject
			// layer. Runs after the security hard gate. The first
			// hook to return reject=true blocks the call with its
			// reason. This mirrors dsh's tools/pre-execute seam.
			if hooks != nil && hooks.HasPreToolHooks() {
				reject, reason, err := hooks.RunPreToolExecute(
					ctx, args.ToolName, args.Arguments,
				)
				if err != nil {
					return nil, fmt.Errorf(
						"pre-tool hook error for %q: %w",
						args.ToolName, err,
					)
				}
				if reject {
					return nil, fmt.Errorf(
						"tool %q blocked by pre-tool hook: %s",
						args.ToolName, reason,
					)
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
		"command_execute",
		"developer_command_execute", // developer extension (ToolSet prefix)
	}
	for _, t := range commandTools {
		if strings.EqualFold(toolName, t) {
			return true
		}
	}
	return false
}

// scopeShell is the permission scope that marks a capability as a
// command-execution surface (declared in builtin/scopes.go).
const scopeShell = "shell"

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
func commandToolNeedsValidation(
	caps *capability.Registry, mode, toolName string,
) bool {
	switch mode {
	case CommandValidationHeuristic:
		return isCommandTool(toolName)
	case CommandValidationDescriptor:
		if caps != nil {
			if c, ok := caps.ResolveByName(toolName); ok {
				for _, s := range c.Descriptor().Scopes {
					if s == scopeShell {
						return true
					}
				}
			}
		}
		return false
	default: // hybrid
		if caps != nil {
			if c, ok := caps.ResolveByName(toolName); ok {
				scopes := c.Descriptor().Scopes
				if len(scopes) > 0 {
					for _, s := range scopes {
						if s == scopeShell {
							return true
						}
					}
					return false
				}
			}
		}
		return isCommandTool(toolName)
	}
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

// buildGuardrailPlugin assembles the prompt-injection guardrail
// runner plugin: a dedicated lightweight model → runner → reviewer →
// prompt-injection detector → guardrail plugin. It returns nil (with a
// warning log) if any step fails, since guardrail is a non-fatal
// enhancement — the agent still runs without it.
//
// This flattens what was previously a 6-level-deep `if err == nil`
// chain in NewCoreLoop into early returns.
func buildGuardrailPlugin(factory *provider.Factory) plugin.Plugin {
	mdl, err := factory.CreateDefaultModel()
	if err != nil || mdl == nil {
		util.Logger.Warn("guardrail: model unavailable, plugin disabled",
			slog.String("error", errOrEmpty(err)))
		return nil
	}

	reviewAgent := llmagent.New("guardrail-reviewer",
		llmagent.WithModel(mdl),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   util.IntPtr(256),
			Temperature: util.Float64Ptr(0.0),
			Stream:      false,
		}),
		llmagent.WithMaxLLMCalls(1),
	)
	guardRunner := runner.NewRunner("wukong-guardrail", reviewAgent)

	piReviewer, err := review.New(guardRunner)
	if err != nil {
		util.Logger.Warn("guardrail: reviewer init failed, plugin disabled",
			slog.String("error", err.Error()))
		return nil
	}

	piPlugin, err := promptinjection.New(
		promptinjection.WithReviewer(piReviewer),
	)
	if err != nil {
		util.Logger.Warn("guardrail: prompt-injection plugin init failed",
			slog.String("error", err.Error()))
		return nil
	}

	grPlugin, err := guardrail.New(
		guardrail.WithPromptInjection(piPlugin),
	)
	if err != nil {
		util.Logger.Warn("guardrail: guardrail plugin init failed",
			slog.String("error", err.Error()))
		return nil
	}
	return grPlugin
}

// errOrEmpty returns err.Error() or "" for a nil error. Used by
// buildGuardrailPlugin to format a possibly-nil model-creation error.
func errOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// isMemoryDuplicated checks whether a memory content string is
// already substantially present in the MemoryFlow wake-up context.
// Uses a sliding window of kMinDupeLen characters to detect
// substring matches of at least 60% overlap, which catches both
// exact duplicates and near-duplicates (e.g., slightly reworded).
const kMinDupeLen = 30

func isMemoryDuplicated(memory, wakeCtx string) bool {
	if len(memory) < kMinDupeLen || len(wakeCtx) < kMinDupeLen {
		return false
	}

	// Build a set of kMinDupeLen-char windows from the memory.
	windows := make(map[string]bool, len(memory)-kMinDupeLen+1)
	for i := 0; i <= len(memory)-kMinDupeLen; i++ {
		windows[memory[i:i+kMinDupeLen]] = true
	}

	// Count matching windows in the wake-up context.
	var matches int
	for i := 0; i <= len(wakeCtx)-kMinDupeLen; i++ {
		if windows[wakeCtx[i:i+kMinDupeLen]] {
			matches++
		}
	}

	// If >= 60% of the memory's windows appear in the wake-up
	// context, consider it a duplicate.
	totalWindows := len(windows)
	if totalWindows == 0 {
		return false
	}
	return float64(matches)/float64(totalWindows) >= 0.6
}
