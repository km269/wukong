package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/apps"
	"github.com/km269/wukong/internal/cli/tui"
	"github.com/km269/wukong/internal/codemode"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/extension"
	"github.com/km269/wukong/internal/extension/builtin"
	artifacts "github.com/km269/wukong/internal/artifact"
	"github.com/km269/wukong/internal/knowledge"
	"github.com/km269/wukong/internal/memory"
	"github.com/km269/wukong/internal/observability"
	"github.com/km269/wukong/internal/project"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/recall"
	"github.com/km269/wukong/internal/security"
	"github.com/km269/wukong/internal/server"
	wksession "github.com/km269/wukong/internal/session"
	"github.com/km269/wukong/internal/skill"
	"github.com/km269/wukong/internal/summon"
	"github.com/km269/wukong/internal/telemetry"
	"github.com/km269/wukong/internal/todo"
	"github.com/km269/wukong/internal/topofmind"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Start an interactive agent session",
		Long: `Start an interactive session with the AI agent.
The agent can call tools, browse the web, execute commands,
and complete tasks autonomously.
		
Examples:
  wukong session
  wukong session --provider openai
  wukong session --model gpt-4o
  wukong session --session-id resume-123`,
		RunE: runSession,
	}

	cmd.Flags().StringP("provider", "p", "",
		"Model provider to use (overrides config default)")
	cmd.Flags().StringP("session-id", "s", "",
		"Session ID to resume (creates new if not specified)")
	cmd.Flags().StringP("model", "m", "",
		"Model name to use (overrides provider default)")
	cmd.Flags().StringP("config", "c", "",
		"Path to config file (default: ~/.config/wukong/config.yaml)")
	cmd.Flags().Float64("temperature", -1,
		"Model temperature (0.0-2.0, overrides config)")
	cmd.Flags().Int("max-tokens", 0,
		"Maximum output tokens per LLM call (overrides config)")
	cmd.Flags().Bool("no-stream", false,
		"Disable streaming output")

	return cmd
}

func runSession(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	sessionID, _ := cmd.Flags().GetString("session-id")
	provider, _ := cmd.Flags().GetString("provider")
	modelName, _ := cmd.Flags().GetString("model")
	temperature, _ := cmd.Flags().GetFloat64("temperature")
	maxTokens, _ := cmd.Flags().GetInt("max-tokens")
	noStream, _ := cmd.Flags().GetBool("no-stream")

	// Build a reasonably unique user identifier.
	// Priority: USER env var (Unix), USERDOMAIN\USERNAME (Windows),
	// hostname fallback, "default" last resort.
	userID := os.Getenv("USER")
	if userID == "" {
		// On Windows, combine domain and username for uniqueness.
		userDomain := os.Getenv("USERDOMAIN")
		userName := os.Getenv("USERNAME")
		if userDomain != "" && userName != "" {
			userID = userDomain + "\\" + userName
		} else if userName != "" && userName != "SYSTEM" {
			userID = userName
		}
	}
	if userID == "" || userID == "SYSTEM" {
		// Fallback: use hostname so different machines get
		// different IDs even when running as SYSTEM.
		if hostname, err := os.Hostname(); err == nil {
			userID = hostname
		}
	}
	if userID == "" {
		userID = "default"
	}

	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// Get current working directory for project tracking.
	workingDir, _ := os.Getwd()

	// Report model overrides if any
	if provider != "" || modelName != "" {
		parts := []string{}
		if provider != "" {
			parts = append(parts, "provider="+provider)
		}
		if modelName != "" {
			parts = append(parts, "model="+modelName)
		}
		fmt.Printf("Overrides: %s\n", strings.Join(parts, ", "))
	}

	// Bootstrap the full system
	wukongCfg, loop, bootstrapState, err := bootstrapSession(
		configPath, userID, sessionID, provider, modelName,
		temperature, maxTokens, noStream,
	)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	// Set up OS signal handling for graceful shutdown.
	// On SIGINT/SIGTERM, the loop is closed and all resources
	// (session, memory, telemetry, A2A server, database pool)
	// are released via the defer cleanup below.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Printf("\nReceived signal %v, shutting down...\n", sig)
		// Shutdown A2A server if running
		if bootstrapState.A2AServer != nil {
			if err := bootstrapState.A2AServer.Stop(
				context.Background(),
			); err != nil {
				util.Logger.Warn("A2A server stop error",
					"error", err.Error())
			}
		}
		// Shutdown knowledge manager
		if bootstrapState.KnowledgeMgr != nil {
			if err := bootstrapState.KnowledgeMgr.Close(); err != nil {
				util.Logger.Warn("knowledge manager close error",
					"error", err.Error())
			}
		}
		// Close the agent loop, which triggers the full cleanup
		// chain: memory workers → runner → session → telemetry
		// → database pool. This ensures all pending writes are
		// flushed and the database is properly closed.
		loop.Close()
		// Do NOT use os.Exit(0) here — let the main goroutine
		// return naturally so defer cleanup and log flushing
		// can complete.
	}()

	// Ensure cleanup on return
	defer func() {
		if bootstrapState.A2AServer != nil {
			if err := bootstrapState.A2AServer.Stop(
				context.Background(),
			); err != nil {
				util.Logger.Warn("A2A server stop error",
					"error", err.Error())
			}
		}
		if bootstrapState.KnowledgeMgr != nil {
			if err := bootstrapState.KnowledgeMgr.Close(); err != nil {
				util.Logger.Warn("knowledge manager close error",
					"error", err.Error())
			}
		}
		loop.Close()
	}()

	// Track the working directory for session recovery.
	if bootstrapState.ProjectMgr != nil && workingDir != "" {
		bootstrapState.ProjectMgr.TrackProject(
			workingDir, sessionID, "")
	}

	p := wukongCfg.DefaultProviderConfig()
	modelDisplay := ""
	if p != nil {
		modelDisplay = p.Model
	}

	fmt.Printf(
		"Session: %s\nProject: %s\nProvider: %s\nModel: %s\n\n",
		sessionID[:8],
		workingDir,
		wukongCfg.DefaultProvider,
		modelDisplay,
	)

	// Start TUI — pass projectMgr for instruction tracking.
	return tui.StartTUI(
		wukongCfg, loop, userID, sessionID,
		workingDir, bootstrapState.ProjectMgr)
}

// BootstrapState holds resources created during bootstrap that need
// cleanup beyond the agent loop's scope (e.g., A2A server, AG-UI server).
type BootstrapState struct {
	A2AServer    *summon.A2AServer
	AGUIServer   *server.AGUIServer
	KnowledgeMgr *knowledge.Manager
	ProjectMgr   *project.Manager
}

// bootstrapSession initializes all components needed for a session.
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
	wukongCfg, err := loader.Load()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse config: %w", err)
	}

	// Apply log level from config (CLI --debug/--quiet overrides
	// are handled in PersistantPreRunE, so if neither is set,
	// the config value takes effect).
	if wukongCfg.LogLevel != "" {
		util.SetLogLevel(wukongCfg.LogLevel)
	}

	// Validate and warn about common config issues
	validateConfig(wukongCfg)

	// Initialize telemetry (OpenTelemetry distributed tracing).
	// This must be done early so all subsequent operations can
	// be traced. Shutdown is deferred until the agent loop closes.
	telMgr := telemetry.NewManager(wukongCfg.Telemetry)
	telShutdown, err := telMgr.Initialize(context.Background())
	if err != nil {
		util.Logger.Warn("telemetry init failed, continuing without tracing",
			"error", err.Error())
	}
	// Note: telShutdown will be called when the CoreLoop's closeFn runs.
	// The loop's closeFn is captured below after the loop is created.

	// Register all built-in extensions
	builtin.RegisterBuiltins(wukongCfg)

	// Apply command-line overrides to config
	applyOverrides(wukongCfg, providerName, modelName,
		temperature, maxTokens, noStream)

	// Create model factory
	factory := provider.NewFactory(wukongCfg)

	// Create shared database pool for all SQLite-backed subsystems.
	// All modules (session, memory, todo, recall) share the same
	// database connection, avoiding the overhead and lifecycle
	// complexity of multiple independent connections.
	// NOTE: The pool path is resolved from session.db_path (default:
	// "wukong.db"). Individual DBPath settings in memory/todo/recall
	// config blocks are ignored when the shared pool is used.
	// To use separate databases, subsystems must be configured with
	// their own pools (currently not implemented).
	dbPool := util.NewDatabasePool(
		config.ResolvePath(wukongCfg.Session.DBPath),
	)

	// Create session service
	sessionSvc, err := wksession.NewSessionService(
		&wukongCfg.Session, dbPool,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create session: %w", err)
	}

	// Create memory manager with auto-extract support.
	// If an extractor_provider or extractor_model is configured in
	// the memory block, use that instead of the default provider.
	// Using a smaller/faster model for memory extraction is recommended
	// to reduce latency and cost.
	var extractorModel model.Model
	if wukongCfg.Memory.AutoExtract {
		extractorModel, err = createExtractorModel(
			factory, &wukongCfg.Memory, wukongCfg,
		)
		if err != nil {
			util.Logger.Warn("auto memory extraction: "+
				"failed to create extractor model, "+
				"auto-extract will be disabled. "+
				"Manual memory tools remain available. "+
				"Check that default_provider is configured "+
				"correctly in config.yaml.",
				"provider", wukongCfg.DefaultProvider,
				"error", err.Error())
			extractorModel = nil
		}
	}
	memoryMgr, err := memory.NewMemoryManager(
		&wukongCfg.Memory, extractorModel, dbPool,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create memory: %w", err)
	}

	// Create security guard
	guard := security.NewGuard(&wukongCfg.Security)

	// Create extension manager and initialize
	extMgr := extension.NewManager(wukongCfg)
	if err := extMgr.Initialize(context.Background()); err != nil {
		return nil, nil, nil, fmt.Errorf("init extensions: %w", err)
	}

	// Inject memory service into the memory toolset
	if memoryMgr != nil {
		extMgr.SetMemoryService(
			memoryMgr.Service(), "wukong-app", userID,
		)
	}

	// Register Extension Manager tool set
	extToolSet := extension.NewManagerToolSet(extMgr, wukongCfg)

	// Create recall store
	var recallStore *recall.Store
	if wukongCfg.Recall.Enabled {
		recallStore, err = recall.NewStore(
			&wukongCfg.Recall, dbPool,
		)
		if err != nil {
			util.Logger.Warn("recall store init failed",
				slog.String("error", err.Error()))
			recallStore = nil
		}
	}

	// Create recall manager for tools
	var recallMgr *recall.RecallManager
	if recallStore != nil {
		recallMgr = recall.NewRecallManager(recallStore)
	}

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

	// Create Summon manager and register delegates as tools
	summonMdl, err := factory.CreateDefaultModel()
	if err != nil {
		util.Logger.Warn("failed to create summon model, "+
			"sub-agent delegation disabled",
			"error", err.Error())
	}
	summonMgr := summon.NewSummonManager(&wukongCfg.Summon, summonMdl)
	// Load skills if any
	if err := summonMgr.LoadSkills(context.Background()); err != nil {
		util.Logger.Warn("summon skills load failed",
			slog.String("error", err.Error()))
	}

	// Collect Summon delegate tools with concurrency control.
	// Each delegate tool is wrapped to acquire a slot from the summon
	// manager's semaphore before execution, enforcing MaxConcurrent.
	var summonTools []tool.Tool

	// Initialize Skill system using trpc-agent-go's FSRepository.
	// Skills are SKILL.md files that define specialized agent workflows.
	skillMgr := skill.NewManager(wukongCfg.Skill)
	if err := skillMgr.Initialize(context.Background()); err != nil {
		util.Logger.Warn("skill system init failed",
			"error", err.Error())
	}

	// Register Skill agents as Summon delegates so the main agent
	// can delegate to specialized skill agents. Each skill is
	// loaded as a sub-agent and wrapped with concurrency control.
	if skillMgr.SkillCount() > 0 {
		if summonMdl != nil {
			for _, s := range skillMgr.ListSummaries() {
				skillAgent, err := skillMgr.CreateSkillAgent(
					context.Background(), s.Name, summonMdl, nil,
				)
				if err != nil {
					util.Logger.Warn("skill agent creation failed",
						"skill", s.Name,
						"error", err.Error())
					continue
				}
				// Wrap the skill agent as a tool for Summon
				skillTool := summon.NewDelegateTool(
					skillAgent, "skill_"+s.Name, s.Description,
				)
				summonTools = append(summonTools,
					summonMgr.WrapTool(skillTool, s.Name),
				)
			}
		}
	}

	// Register Summon skill delegates as function tools
	for _, d := range summonMgr.ListDelegates() {
		summonTools = append(summonTools,
			summonMgr.WrapTool(d.Tool(), d.Name()),
		)
	}

	// Register A2A remote agents as summon delegates.
	// Each remote agent is configured with a server URL and auth,
	// and wrapped as a tool that the main agent can delegate to.
	for _, remote := range wukongCfg.Summon.A2ARemotes {
		a2aAgent := a2aRemoteToConfig(remote)
		if a2aAgent == nil {
			util.Logger.Warn("A2A remote agent init failed",
				"agent", remote.Name)
			continue
		}
		// Store the A2A agent for later use as a sub-agent.
		_ = a2aAgent.Agent()
		util.Logger.Info("A2A remote agent configured",
			"agent", remote.Name,
			"server_url", remote.ServerURL)
	}

	// Create todo manager
	todoStore, err := todo.NewStore(
		wukongCfg.Todo.DBPath, dbPool,
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
	if recallMgr != nil {
		functionTools = append(functionTools, recallMgr.Tools()...)
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
	if agentToolSet != nil && len(agentToolSet.Tools(nil)) > 0 {
		toolSets = append(toolSets, agentToolSet)
	}

	// Add Summon delegate tools
	if len(summonTools) > 0 {
		functionTools = append(functionTools, summonTools...)
	}

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
	artifactSvc, err := artifacts.NewService(&wukongCfg.ArtifactConfig)
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
			return fmt.Errorf("shutdown errors: %v", errs)
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
		SecurityGuard:         guard,
		RecallStore:           recallStore,
		RevisionModel:         revisionModel,
		TopOfMindInstructions: topOfMindInstructions,
		TelemetryShutdown:     combinedShutdown,
		MemoryClose:           memoryMgr.Close,
		DBPoolClose:           dbPool.Close,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create agent loop: %w", err)
	}

	// Initialize A2A server if enabled in config.
	// Uses tRPC-Agent-Go's server/a2a wrapper which provides
	// automatic protocol conversion, streaming, and session integration.
	// The main agent and runner are shared with the A2A endpoint
	// so remote clients get the full agent capabilities.
	// Create project manager for working directory tracking.
	projectMgr, prjErr := project.NewManager(wukongCfg)
	if prjErr != nil {
		util.Logger.Warn("project manager creation failed, "+
			"project tracking disabled",
			"error", prjErr.Error())
	}

	state := &BootstrapState{
		KnowledgeMgr: knowledgeMgr,
		ProjectMgr:   projectMgr,
	}
	if wukongCfg.A2AServer.Enabled {
		hostAddr := wukongCfg.A2AServer.Address
		if hostAddr == "" {
			hostAddr = ":9090"
		}

		a2aAgent := loop.GetAgent()
		a2aRunner := loop.GetRunner()
		a2aSessionSvc := loop.GetSessionService()

		a2aServerCfg := &summon.A2AServerConfig{
			Agent:          a2aAgent,
			Runner:         a2aRunner,
			SessionService: a2aSessionSvc,
			Name:           wukongCfg.A2AServer.AgentName,
			Description:    wukongCfg.A2AServer.AgentDescription,
			Host:           hostAddr,
			Streaming:      true,
		}

		a2aSrv, err := summon.NewA2AServer(a2aServerCfg)
		if err != nil {
			util.Logger.Warn("A2A server creation failed, "+
				"continuing without A2A server",
				"error", err.Error())
		} else {
			a2aSrv.Start(hostAddr)
			state.A2AServer = a2aSrv
		}
	}

	// Initialize AG-UI SSE server if enabled.
	if wukongCfg.AGUI.Enabled {
		aguiCfg := &server.AGUIConfig{
			Runner: loop.GetRunner(),
			Path:   wukongCfg.AGUI.Path,
		}
		aguiSrv, err := server.NewAGUIServer(aguiCfg)
		if err != nil {
			util.Logger.Warn("AG-UI server creation failed",
				"error", err.Error())
		} else {
			addr := wukongCfg.AGUI.Address
			if addr == "" {
				addr = ":8080"
			}
			go func() {
				if err := aguiSrv.Start(addr); err != nil {
					util.Logger.Warn("AG-UI server failed",
						"error", err.Error())
				}
			}()
			state.AGUIServer = aguiSrv
		}
	}

	return wukongCfg, loop, state, nil
}

// a2aRemoteToConfig converts a config A2ARemoteConfig to an A2AAgent.
// Uses the new A2AAgent implementation based on tRPC-Agent-Go's a2aagent.
func a2aRemoteToConfig(remote config.A2ARemoteConfig) *summon.A2AAgent {
	ag, err := summon.NewA2AAgentFromConfig(remote)
	if err != nil {
		util.Logger.Warn("failed to create A2A agent for remote",
			"name", remote.Name,
			"error", err.Error())
		return nil
	}
	util.Logger.Info("A2A remote agent configured",
		"name", remote.Name,
		"server_url", remote.ServerURL)
	return ag
}

// createExtractorModel creates a model for memory extraction.
// If the memory config specifies an extractor_provider, that provider
// is used; otherwise the default provider is used. This allows using
// a smaller/cheaper model (e.g., deepseek-chat) for memory extraction
// while keeping a more capable model for the main conversation.
func createExtractorModel(
	factory *provider.Factory,
	memCfg *config.MemoryConfig,
	wukongCfg *config.WukongConfig,
) (model.Model, error) {
	if memCfg.ExtractorProvider != "" {
		// Use the dedicated extractor provider
		extractorProvider := wukongCfg.FindProvider(
			memCfg.ExtractorProvider,
		)
		if extractorProvider == nil {
			return nil, fmt.Errorf(
				"extractor_provider %q not found in providers list",
				memCfg.ExtractorProvider,
			)
		}
		// If extractor_model is also set, temporarily override
		// the provider's default model for extraction.
		if memCfg.ExtractorModel != "" {
			originalModel := extractorProvider.Model
			extractorProvider.Model = memCfg.ExtractorModel
			defer func() {
				extractorProvider.Model = originalModel
			}()
		}
		return factory.CreateModel(memCfg.ExtractorProvider)
	}
	// Fall back to default provider
	return factory.CreateDefaultModel()
}

// applyOverrides applies command-line overrides to config.
func applyOverrides(
	cfg *config.WukongConfig,
	providerName string,
	modelName string,
	temperature float64,
	maxTokens int,
	noStream bool,
) {
	if providerName != "" {
		p := cfg.FindProvider(providerName)
		if p == nil {
			util.Logger.Warn("provider not found in config",
				slog.String("provider", providerName))
		} else {
			cfg.DefaultProvider = providerName
			if modelName != "" {
				p.Model = modelName
			}
		}
	} else if modelName != "" {
		p := cfg.DefaultProviderConfig()
		if p != nil {
			p.Model = modelName
		}
	}

	if temperature >= 0 {
		cfg.Agent.Temperature = temperature
	}
	if maxTokens > 0 {
		cfg.Agent.MaxTokens = maxTokens
	}
	if noStream {
		cfg.Agent.Streaming = false
	}
}

// validateConfig checks for common configuration mistakes and
// emits warnings. This helps users diagnose issues before they
// encounter runtime errors during a session.
func validateConfig(cfg *config.WukongConfig) {
	if cfg.DefaultProvider == "" {
		util.Logger.Warn("no default_provider configured; " +
			"set it in config.yaml or use --provider flag")
		return
	}

	p := cfg.FindProvider(cfg.DefaultProvider)
	if p == nil {
		util.Logger.Warn("default_provider not found in providers list",
			slog.String("configured", cfg.DefaultProvider))
		return
	}

	if p.Model == "" {
		util.Logger.Warn("no model configured for default provider; " +
			"the provider may use a default model")
	}

	if p.APIKey == "" && p.Type != "ollama" && p.Type != "lmstudio" {
		util.Logger.Warn("no API key configured for " + cfg.DefaultProvider +
			"; set " + p.Name + ".api_key in config or via " +
			strings.ToUpper(p.Name) + "_API_KEY env var")
	}

	if cfg.Agent.Planner == "builtin" &&
		p.Type != "anthropic" && p.Type != "google" {
		util.Logger.Warn("builtin planner requires a model with native " +
			"thinking support (Claude/Gemini); current provider is " +
			p.Type + " — consider using 'react' planner instead")
	}

	switch cfg.Agent.Planner {
	case "builtin", "react":
		util.Logger.Info("planner enabled: " + cfg.Agent.Planner)
	default:
		if cfg.Agent.Planner != "" {
			util.Logger.Warn("unknown planner: " + cfg.Agent.Planner +
				"; supported: builtin, react")
		}
	}

	if cfg.Security.GuardrailEnabled {
		util.Logger.Info("guardrail enabled — prompt injection detection active")
	}

	if cfg.Memory.AutoExtract &&
		cfg.Memory.ExtractorProvider == "" &&
		cfg.Memory.ExtractorModel == "" {
		// Auto-extract uses the default provider; warn if that
		// provider may be slow or expensive for extraction.
		if p.Type == "lmstudio" || p.Type == "ollama" {
			util.Logger.Info("auto-extract uses local " + p.Type +
				" model — this may be slow; consider setting " +
				"memory.extractor_provider to a faster model")
		}
	}
}



