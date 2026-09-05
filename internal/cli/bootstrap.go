// Code split out of session.go (P2-8) - same package, zero behavior change.
//
// bootstrapSession is the composition root shared by the session /
// server / run modes. The heavy lifting is delegated to phase
// functions in bootstrap_phases.go; this file keeps the orchestration
// sequence and the fatal-error boundaries visible in one place.
package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/apps"
	artifacts "github.com/km269/wukong/internal/artifact"
	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/codemode"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/extension"
	"github.com/km269/wukong/internal/extension/builtin"
	"github.com/km269/wukong/internal/knowledge"
	"github.com/km269/wukong/internal/observability"
	"github.com/km269/wukong/internal/project"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/scripthook"
	"github.com/km269/wukong/internal/security"
	"github.com/km269/wukong/internal/server"
	"github.com/km269/wukong/internal/todo"
	"github.com/km269/wukong/internal/topofmind"
	"github.com/km269/wukong/internal/util"
	"log/slog"
	"strings"
	"time"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// bootstrapSession initializes all components needed for a session.
//
// Orchestration sequence (phase functions live in bootstrap_phases.go):
//
//  1. config load + overrides        7. recall / memoryflow / graph stacks
//  2. telemetry                      8. skill / evolution / summon
//  3. session + model event log      9. tool universe assembly
//  4. memory stack                  10. CoreLoop
//  5. guard + extensions + ARD      11. protocol servers (A2A/ACP/AG-UI/ANP/GW)
//  6. session toolsets                 12. BootstrapState
func bootstrapSession(
	configPath, userID, sessionID, providerName, modelName string,
	temperature float64, maxTokens int, noStream bool,
) (*config.WukongConfig, *agent.CoreLoop, *BootstrapState, error) {
	// sessionID is used by the caller (runSession) for TUI initialization
	// and is forwarded here for consistency but not consumed internally.
	_ = sessionID

	// Load config
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.LoadAndValidate()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("config validation: %w", err)
	}

	// Surface non-fatal configuration warnings (these do not block
	// startup but indicate suboptimal or risky configuration). Fatal
	// issues were already rejected by LoadAndValidate above.
	for _, w := range wukongCfg.Warnings() {
		util.Logger.Warn("config: " + w)
	}

	// Apply log level from config. CLI --debug/--quiet flags take
	// precedence over config value and are already applied in
	// PersistentPreRunE. Only apply config value if neither flag
	// was set.
	if wukongCfg.LogLevel != "" && !debugEnabled && !quietEnabled {
		util.SetLogLevel(wukongCfg.LogLevel)
	}

	// Validate and warn about common config issues
	validateConfig(wukongCfg)

	// Initialize telemetry (OpenTelemetry distributed tracing).
	// This must be done early so all subsequent operations can
	// be traced. Shutdown is deferred until the agent loop closes.
	telShutdown := initTelemetry(wukongCfg)

	// Register all built-in extensions
	builtin.RegisterBuiltins(wukongCfg)

	// Apply command-line overrides to config
	applyOverrides(wukongCfg, providerName, modelName,
		temperature, maxTokens, noStream)

	// Create model factory
	factory := provider.NewFactory(wukongCfg)

	// Create multi-pool database manager for all SQLite-backed subsystems.
	// By default, all modules (session, memory, todo, recall, cortex,
	// evolution) share a single wukong.db via the "shared" pool.
	//
	// Subsystems with their own db_path config override will receive
	// an independent DatabasePool, enabling data isolation when needed
	// (e.g., a dedicated memory database for large-scale recall).
	dbPool := util.NewMultiPool(
		config.ResolvePath(wukongCfg.Session.DBPath),
	)

	// Session service + model-visible event log.
	sessionSvc, modelEventLog, err := initSessionStack(wukongCfg, dbPool)
	if err != nil {
		return nil, nil, nil, err
	}

	// Memory manager (tRPC long-term memory) with auto-extract support.
	memoryMgr, err := initMemoryStack(wukongCfg, factory, dbPool, userID)
	if err != nil {
		return nil, nil, nil, err
	}

	// Create security guard and the shared non-interactive check used
	// by the ACP and MCP servers.
	guard := security.NewGuard(&wukongCfg.Security)
	guardCheck := buildGuardCheck(guard)

	// Create extension manager and initialize
	extMgr := extension.NewManager(wukongCfg)
	extInitCtx, extInitCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer extInitCancel()
	if err := extMgr.Initialize(extInitCtx); err != nil {
		return nil, nil, nil, fmt.Errorf("init extensions: %w", err)
	}

	// Inject memory service into the memory toolset
	if memoryMgr != nil {
		extMgr.SetMemoryService(
			memoryMgr.Service(), "wukong-app", userID,
		)
	}

	// ARD toolset + optional registry server for inbound discovery.
	ardRegistryServer := initARDStack(wukongCfg, extMgr)

	// Register Extension Manager tool set
	extToolSet := extension.NewManagerToolSet(extMgr, wukongCfg)

	// Initialize ACP MCP Bridge — exposes Wukong extensions as
	// an MCP Server for ACP agents to discover and call tools.
	var acpMCPBridge *extension.ACPMCPBridge
	acpMCPBridge, acpMCPErr := extension.NewACPMCPBridge(
		extMgr, &wukongCfg.ACPMCP,
	)
	if acpMCPErr != nil {
		util.Logger.Warn("acp mcp bridge creation failed",
			"error", acpMCPErr.Error())
	} else if acpMCPBridge != nil {
		if err := acpMCPBridge.Start(); err != nil {
			util.Logger.Warn("acp mcp bridge start failed",
				"error", err.Error())
			acpMCPBridge = nil
		} else {
			// Set MCP address on factory for ACP providers.
			factory.SetACPMCPAddr(acpMCPBridge.ACPMCPAddr())
		}
	}

	// Initialize standalone MCP Server — exposes Wukong extensions
	// as a standards-compliant MCP JSON-RPC 2.0 endpoint for external
	// MCP clients (e.g. Claude Desktop, Cursor, etc.).
	var mcpServer *extension.MCPServer
	if wukongCfg.MCPServer.Enabled {
		addr := wukongCfg.MCPServer.Address
		if addr == "" {
			addr = ":9091"
		}
		mcpServer = extension.NewMCPServerWithSecurity(extMgr, addr,
			server.SecurityConfigApplier{Cfg: wukongCfg.MCPServer.Security},
			extension.ToolGuardCheck(guardCheck))
		if err := mcpServer.Start(); err != nil {
			util.Logger.Warn("mcp server start failed",
				"error", err.Error())
			mcpServer = nil
		} else {
			util.Logger.Info("mcp server started",
				slog.String("address", addr))
		}
	}

	// Recall stack: CortexDB hybrid store (when cortex.enabled) with
	// native SQLite FTS5 fallback, wired into the web toolset.
	recallStore, cortexStore := initRecallStack(
		wukongCfg, dbPool, extMgr, userID)

	// MemoryFlow: conversation transcript, wake-up context, fact
	// promotion (shares the CortexDB instance when available).
	memoryFlowSvc := initMemoryFlow(wukongCfg, factory, cortexStore)

	// Recall managers (vector-enhanced when cortex is active).
	recallMgr, cortexRecallMgr := initRecallManagers(
		wukongCfg, cortexStore, recallStore, memoryMgr, userID)

	// Knowledge-graph and structured-import stacks.
	kgToolMgr, graphFlowSvc, importToolMgr := initGraphAndImportStack(
		wukongCfg, factory)

	// Create Top of Mind manager
	tomMgr := topofmind.NewManager(&wukongCfg.TopOfMind)
	tomToolSet := builtin.NewTopOfMindToolSet(tomMgr)

	// Create Code Mode executor
	codeExecutor := codemode.NewExecutor(&wukongCfg.CodeMode)
	codeToolSet := builtin.NewCodeModeToolSet(codeExecutor)

	// Create Apps manager
	appsMgr, err := apps.NewManager(&wukongCfg.Apps)
	if err != nil {
		util.Logger.Warn("apps manager init failed",
			slog.String("error", err.Error()))
	}
	var appsToolSet *builtin.AppsToolSet
	if appsMgr != nil {
		appsToolSet = builtin.NewAppsToolSet(appsMgr)
	}

	// Create AgentToolSet — wraps specialized sub-agents (code-reviewer,
	// summarizer, code-generator) as tools callable by the main agent.
	// Configurable via agent.agent_tools_enabled and agent.agent_tools_stream.
	agentToolSet := builtin.NewAgentToolSet(factory, &wukongCfg.Agent)

	// Skill system + evolution engine (wired together when enabled).
	skillMgr, evoEngine := initSkillStack(wukongCfg, factory, dbPool)

	// Summon: sub-agent delegation (local delegates, skill-as-delegate,
	// A2A remotes) + OAuth2 credential rotator.
	summonTools, credRotator := initSummonStack(wukongCfg, factory, skillMgr)

	// Create todo manager
	todoStore, err := todo.NewStore(
		wukongCfg.Todo.DBPath, dbPool.Shared(),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create todo store: %w", err)
	}
	todoMgr := todo.NewTodoManager(todoStore)

	// Create Knowledge Manager for RAG (Retrieval-Augmented Generation).
	// When enabled, documents are loaded, embedded, and a search tool is
	// registered to the agent. Returns nil (no error) when disabled.
	knowledgeMgr, err := knowledge.NewManager(
		&wukongCfg.Knowledge, wukongCfg,
	)
	if err != nil {
		return nil, nil, nil,
			fmt.Errorf("create knowledge manager: %w", err)
	}

	// Collect all tool sets and function tools
	toolSets := extMgr.ToolSets()
	functionTools := todoMgr.Tools()

	// Add Extension Manager tools
	if extToolSet != nil {
		toolSets = append(toolSets, extToolSet)
	}

	// Add Recall tools
	if cortexRecallMgr != nil {
		functionTools = append(
			functionTools, cortexRecallMgr.Tools()...)
	} else if recallMgr != nil {
		functionTools = append(functionTools, recallMgr.Tools()...)
	}

	// Add Knowledge Graph tools
	if kgToolMgr != nil {
		functionTools = append(
			functionTools, kgToolMgr.Tools()...)
	}

	// Add ImportFlow tools
	if importToolMgr != nil {
		functionTools = append(
			functionTools, importToolMgr.Tools()...)
	}

	// Add Top of Mind tools
	if tomToolSet != nil {
		toolSets = append(toolSets, tomToolSet)
	}

	// Add Code Mode tools
	if codeToolSet != nil {
		toolSets = append(toolSets, codeToolSet)
	}

	// Add Apps tools
	if appsToolSet != nil {
		toolSets = append(toolSets, appsToolSet)
	}

	// Add Agent tools (code-reviewer, summarizer)
	if agentToolSet != nil && len(agentToolSet.Tools(context.TODO())) > 0 {
		toolSets = append(toolSets, agentToolSet)
	}

	// CortexDB tools are already registered as functionTools above
	// (KG query, KG analyze, import DDL/CSV). Do NOT add a duplicate
	// CortexToolSet — it causes massive tool list duplication that
	// wastes hundreds of tokens per LLM call.

	// Add Summon delegate tools
	if len(summonTools) > 0 {
		functionTools = append(functionTools, summonTools...)
	}

	// Capability bus (roadmap P0-1 Phase A, read-only): register
	// every extension-sourced tool under a stable bus address so
	// the caps CLI, Guard, protocol endpoints and the future flow
	// DSL share one registry. CoreLoop keeps the hand-aggregation
	// above unchanged; AsTools() equivalence is covered by tests.
	capsReg := capability.NewRegistry()
	regCtx := context.Background()
	if _, err := extMgr.RegisterCapabilities(capsReg, regCtx); err != nil {
		util.Logger.Warn("capability registry: manager registration failed",
			"error", err.Error())
	}
	registerCapsToolset := func(prefix string, ts tool.ToolSet) {
		if ts == nil {
			return
		}
		if _, err := extension.RegisterToolSet(
			capsReg, prefix, capability.SourceBuiltin, ts, regCtx,
		); err != nil {
			util.Logger.Warn("capability registry: registration failed",
				"prefix", prefix, "error", err.Error())
		}
	}
	registerCapsToolset(extension.AddrExtensionMgr, extToolSet)
	registerCapsToolset(extension.AddrTopOfMind, tomToolSet)
	registerCapsToolset(extension.AddrCodeMode, codeToolSet)
	registerCapsToolset(extension.AddrApps, appsToolSet)
	registerCapsToolset(extension.AddrAgentTools, agentToolSet)

	// P1-4: user JS hooks (.wukong/hooks/*.js, opt-in). beforeStep /
	// beforeTool functions join the waterfall HookRegistry; script
	// tools register as "script.*" capabilities.
	var scriptHooksReg *agent.HookRegistry
	if wukongCfg.Agent.ScriptHooksEnabled {
		shs, shErr := scripthook.Load(
			scripthook.ResolveDir(wukongCfg.Agent.ScriptHooksDir),
			wukongCfg.Agent.ScriptHooksTimeout,
		)
		if shErr != nil {
			util.Logger.Warn("scripthook: load failed, hooks disabled",
				"error", shErr.Error())
		} else {
			scriptHooksReg = agent.NewHookRegistry()
			for _, h := range shs.PreStepHooks() {
				scriptHooksReg.RegisterPreStep(h)
			}
			for _, h := range shs.PreToolHooks() {
				scriptHooksReg.RegisterPreToolExecute(h)
			}
			shs.SyncScriptTools(capsReg)
			util.Logger.Info("scripthook: enabled",
				"scripts", shs.Len())
		}
	}

	util.Logger.Info("capability registry ready",
		"capabilities", capsReg.Len())

	// Add Knowledge search tool (RAG)
	if knowledgeMgr != nil && knowledgeMgr.IsEnabled() {
		searchTool := knowledgeMgr.SearchTool()
		if searchTool != nil {
			functionTools = append(functionTools, searchTool)
		}
	}

	// Wire up code_discover_tools: inject the complete tool list
	// into the executor so JS code can discover and invoke tools.
	var discovered []codemode.DiscoveredTool
	for _, ts := range toolSets {
		for _, t := range ts.Tools(context.Background()) {
			decl := t.Declaration()
			if decl == nil {
				continue
			}
			discovered = append(discovered, codemode.DiscoveredTool{
				Name:        decl.Name,
				Description: decl.Description,
				Source:      "toolset",
			})
		}
	}
	for _, t := range functionTools {
		decl := t.Declaration()
		if decl == nil {
			continue
		}
		discovered = append(discovered, codemode.DiscoveredTool{
			Name:        decl.Name,
			Description: decl.Description,
			Source:      "function",
		})
	}
	codeExecutor.SetToolsForDiscovery(discovered)

	// Create revision model for context summarization
	revisionModel, err := factory.CreateRevisionModel()
	if err != nil {
		util.Logger.Warn("revision model init failed",
			slog.String("error", err.Error()))
	}

	// Format Top of Mind instructions for injection into system prompt
	topOfMindInstructions := tomMgr.FormatForPrompt()

	// Create artifact service for file versioning (visualiser outputs, etc.)
	// Supports inmemory (default) and cos (Tencent Cloud Object Storage).
	artifactSvc, err := artifacts.NewService(&wukongCfg.Artifact)
	if err != nil {
		return nil, nil, nil,
			fmt.Errorf("create artifact service: %w", err)
	}

	// Start Langfuse LLM tracing if enabled.
	// Langfuse provides a dedicated UI for inspecting agent runs,
	// tool calls, model requests, token usage, and errors.
	langfuseCleanup, err := observability.StartLangfuse(
		context.Background(), &wukongCfg.Observability)
	if err != nil {
		util.Logger.Warn("langfuse start failed, continuing without tracing",
			"error", err.Error())
		langfuseCleanup = func(_ context.Context) error { return nil }
	}

	// Merge Langfuse cleanup into telemetry shutdown chain.
	combinedShutdown := func(ctx context.Context) error {
		var errs []error
		if telShutdown != nil {
			if err := telShutdown(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if langfuseCleanup != nil {
			if err := langfuseCleanup(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("shutdown errors: %w", errors.Join(errs...))
		}
		return nil
	}

	// Create agent loop
	loop, err := agent.NewCoreLoop(agent.CoreLoopConfig{
		Config:                wukongCfg,
		Factory:               factory,
		SessionService:        sessionSvc,
		MemoryService:         memoryMgr.Service(),
		ArtifactService:       artifactSvc,
		ToolSets:              toolSets,
		FunctionTools:         functionTools,
		Capabilities:          capsReg,
		Hooks:                 scriptHooksReg,
		SecurityGuard:         guard,
		RecallStore:           recallStore,
		CortexStore:           cortexStore,
		RevisionModel:         revisionModel,
		MemoryFlowService:     memoryFlowSvc,
		GraphFlowService:      graphFlowSvc,
		TopOfMindInstructions: topOfMindInstructions,
		TelemetryShutdown:     combinedShutdown,
		MemoryClose:           memoryMgr.Close,
		EvolutionClose:        evoEngineClose(evoEngine),
		DBPoolClose:           dbPool.Close,
		ModelEventLog:         modelEventLog,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create agent loop: %w", err)
	}

	// Create project manager for working directory tracking.
	projectMgr, prjErr := project.NewManager(wukongCfg)
	if prjErr != nil {
		util.Logger.Warn("project manager creation failed, "+
			"project tracking disabled",
			"error", prjErr.Error())
	}

	state := &BootstrapState{
		KnowledgeMgr:      knowledgeMgr,
		ProjectMgr:        projectMgr,
		SessionSvc:        sessionSvc,
		ARDRegistry:       ardRegistryServer,
		ACPMCPBridge:      acpMCPBridge,
		MCPServer:         mcpServer,
		CredentialRotator: credRotator,
		ExtMgr:            extMgr,
		Caps:              capsReg,
		// Wire a real DB ping so the health DBChecker is no longer a
		// no-op. dbPool is the shared SQLite pool created above.
		DBPing: func(ctx context.Context) error {
			db, err := dbPool.Shared().GetDB()
			if err != nil {
				return err
			}
			return db.PingContext(ctx)
		},
	}

	// Protocol servers (A2A / AG-UI / ACP / ANP / sandbox probe /
	// Gateway) — all optional, all config-gated, all wired into state.
	startProtocolServers(wukongCfg, loop, guard, guardCheck, state, dbPool)

	return wukongCfg, loop, state, nil
}

// ensure strings stays used if future edits trim the gateway block.
var _ = strings.Join
