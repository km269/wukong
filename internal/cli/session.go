package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/apps"
	"github.com/km269/wukong/internal/ard"
	artifacts "github.com/km269/wukong/internal/artifact"
	"github.com/km269/wukong/internal/cli/tui"
	"github.com/km269/wukong/internal/codemode"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/cortex"
	"github.com/km269/wukong/internal/evolution"
	"github.com/km269/wukong/internal/extension"
	"github.com/km269/wukong/internal/extension/builtin"
	"github.com/km269/wukong/internal/gateway"
	"github.com/km269/wukong/internal/gateway/feishu"
	"github.com/km269/wukong/internal/gateway/wecom"
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
	"github.com/km269/wukong/pkg/sandbox"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	tRPCMemory "trpc.group/trpc-go/trpc-agent-go/memory"
)

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Start an interactive agent session",
		Long: `Start an interactive session with the AI agent.
The agent can call tools, browse the web, execute commands,
and complete tasks autonomously.

Subcommands:
  list    List all saved sessions
  delete  Delete a session by ID
		
Examples:
  wukong session
  wukong session --provider openai
  wukong session --model gpt-4o
  wukong session --session-id resume-123
  wukong session list
  wukong session delete abc12345`,
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

	cmd.AddCommand(newSessionListCmd())
	cmd.AddCommand(newSessionDeleteCmd())
	cmd.AddCommand(newSessionExportCmd())
	cmd.AddCommand(newSessionInfoCmd())
	cmd.AddCommand(newSessionResumeCmd())

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

	// === Quick pre-load: show session info BEFORE full bootstrap ===
	// This gives the user immediate feedback while subsystems load.
	quickCfg := quickLoadConfig(configPath, provider, modelName)
	fmt.Printf(
		"Session: %s\nProject: %s\nProvider: %s\nModel: %s\n",
		sessionID[:8],
		workingDir,
		quickCfg.provider,
		quickCfg.model,
	)
	fmt.Println("Initializing subsystems...")

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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if bootstrapState.A2AServer != nil {
			if err := bootstrapState.A2AServer.Stop(shutdownCtx); err != nil {
				util.Logger.Warn("A2A server stop error",
					"error", err.Error())
			}
		}
		// Shutdown AG-UI server if running
		if bootstrapState.AGUIServer != nil {
			_ = bootstrapState.AGUIServer.Stop(shutdownCtx)
		}
		// Shutdown ACP server if running
		if bootstrapState.ACPServer != nil {
			_ = bootstrapState.ACPServer.Stop(shutdownCtx)
		}
		// Shutdown ACP MCP Bridge if running
		if bootstrapState.ACPMCPBridge != nil {
			if err := bootstrapState.ACPMCPBridge.Stop(); err != nil {
				util.Logger.Warn("acp mcp bridge stop error",
					"error", err.Error())
			}
		}
		// Shutdown ARD registry server if running
		if bootstrapState.ARDRegistry != nil {
			_ = bootstrapState.ARDRegistry.Shutdown(shutdownCtx)
		}
		// Shutdown ANP server if running
		if bootstrapState.ANPServer != nil {
			_ = bootstrapState.ANPServer.Shutdown(shutdownCtx)
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if bootstrapState.A2AServer != nil {
			if err := bootstrapState.A2AServer.Stop(shutdownCtx); err != nil {
				util.Logger.Warn("A2A server stop error",
					"error", err.Error())
			}
		}
		if bootstrapState.AGUIServer != nil {
			_ = bootstrapState.AGUIServer.Stop(shutdownCtx)
		}
		if bootstrapState.ACPServer != nil {
			_ = bootstrapState.ACPServer.Stop(shutdownCtx)
		}
		if bootstrapState.ACPMCPBridge != nil {
			if err := bootstrapState.ACPMCPBridge.Stop(); err != nil {
				util.Logger.Warn("acp mcp bridge stop error",
					"error", err.Error())
			}
		}
		// Shutdown ARD registry server if running
		if bootstrapState.ARDRegistry != nil {
			_ = bootstrapState.ARDRegistry.Shutdown(shutdownCtx)
		}
		// Shutdown ANP server if running
		if bootstrapState.ANPServer != nil {
			_ = bootstrapState.ANPServer.Shutdown(shutdownCtx)
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

	fmt.Println() // blank line after bootstrap logs

	// Start TUI — pass projectMgr for instruction tracking.
	return tui.StartTUI(
		wukongCfg, loop, userID, sessionID,
		workingDir, bootstrapState.ProjectMgr)
}

// BootstrapState holds resources created during bootstrap that need
// cleanup beyond the agent loop's scope (e.g., A2A server, AG-UI server).
type BootstrapState struct {
	A2AServer     *summon.A2AServer
	AGUIServer    *server.AGUIServer
	ACPServer     *server.ACPServer
	ACPMCPBridge  *extension.ACPMCPBridge
	ARDRegistry   *ard.RegistryServer
	ANPServer     *http.Server
	ANPMeta       *summon.MetaProtocol
	ANPMessenger  *summon.E2EEMessenger
	KnowledgeMgr  *knowledge.Manager
	ProjectMgr    *project.Manager
	GatewayServer *gateway.GatewayServer
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
	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer initCancel()
	telShutdown, err := telMgr.Initialize(initCtx)
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

	// Create session service
	sessionSvc, err := wksession.NewSessionService(
		&wukongCfg.Session, dbPool.Shared(),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create session: %w", err)
	}

	// Create memory manager with auto-extract support.
	// If an extractor_provider or extractor_model is configured in
	// the memory block, use that instead of the default provider.
	// Falls back to default model if the extractor model fails.
	var extractorModel model.Model
	if wukongCfg.Memory.AutoExtract {
		extractorModel, err = createExtractorModel(
			factory, &wukongCfg.Memory, wukongCfg,
		)
		if err != nil {
			util.Logger.Warn("auto memory extraction: "+
				"failed to create extractor model, "+
				"falling back to default model",
				"error", err.Error())
			// Fallback to default model for extraction
			extractorModel, err = factory.CreateDefaultModel()
			if err != nil {
				util.Logger.Warn("auto memory extraction: "+
					"fallback model also failed, "+
					"auto-extract disabled",
					"error", err.Error())
				extractorModel = nil
			} else {
				util.Logger.Info("auto memory extraction: " +
					"using default model as extractor fallback")
			}
		}
	}
	memoryMgr, err := memory.NewMemoryManager(
		&wukongCfg.Memory, extractorModel, dbPool.Shared(),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create memory: %w", err)
	}

	// Smart cleanup: evict low-importance memories when near capacity.
	if wukongCfg.Memory.MaxMemories > 0 {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanCancel()
		cleaned, _ := memoryMgr.SmartCleanup(
			cleanCtx,
			tRPCMemory.UserKey{
				AppName: "wukong-app",
				UserID:  userID,
			},
			30*24*time.Hour,
		)
		if cleaned > 0 {
			util.Logger.Info("memory: startup smart cleanup",
				"cleaned", cleaned)
		}
	}

	// Create security guard
	guard := security.NewGuard(&wukongCfg.Security)

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

	// If ARD is enabled, initialize the ARD ToolSet and wire
	// auto-discovery for MCP servers and A2A remote agents.
	// Also optionally start Wukong's own ARD registry server so
	// other ARD-compatible agents can discover Wukong on the network.
	var ardRegistryServer *ard.RegistryServer
	if wukongCfg.ARD.Enabled {
		ardTS, ardErr := ard.NewToolSet(
			wukongCfg.ARD.RegistryURL,
			wukongCfg.ARD.CatalogPath,
		)
		if ardErr != nil {
			util.Logger.Warn("ard: failed to create toolset",
				"error", ardErr.Error())
		} else {
			// Wire ARD to extension manager for MCP auto-registration.
			extMgr.SetARDToolSet(ardTS)

			// Auto-register A2A remote agents to ARD catalog.
			for _, remote := range wukongCfg.Summon.A2ARemotes {
				ard.RegisterA2AAgent(ardTS, remote.Name,
					remote.Description, remote.ServerURL)
			}
		}

		// Start Wukong's own ARD registry server for inbound discovery.
		if wukongCfg.ARD.PublishEnabled && wukongCfg.ARD.PublishPort > 0 {
			anpOpts := ard.ANPPublishOptions{
				Enabled: wukongCfg.ANP.Enabled &&
					wukongCfg.ANP.DiscoveryEnabled,
				BaseURL: fmt.Sprintf("http://localhost:%d",
					wukongCfg.ARD.PublishPort),
			}
			ardSrv, pubErr := ard.PublishAndServe(
				context.Background(),
				wukongCfg.ARD.PublishPort,
				wukongCfg.ARD.CatalogPath,
				&anpOpts,
			)
			if pubErr != nil {
				util.Logger.Warn("ard: failed to start registry server",
					"error", pubErr.Error())
			} else {
				ardRegistryServer = ardSrv
			}
		}
	}

	// Register Extension Manager tool set
	extToolSet := extension.NewManagerToolSet(extMgr, wukongCfg)

	// Initialize ACP MCP Bridge — exposes Wukong extensions as
	// an MCP Server for ACP agents to discover and call tools.
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
		} else {
			// Set MCP address on factory for ACP providers.
			factory.SetACPMCPAddr(acpMCPBridge.ACPMCPAddr())
		}
	}

	// Create recall store — supports both native SQLite FTS5 and
	// CortexDB (vector + FTS5 hybrid) backends.
	var recallStore *recall.Store
	var cortexStore *cortex.CortexStore
	if wukongCfg.Cortex.Enabled {
		// CortexDB-backed store with vector semantic search.
		var embedder *cortex.Embedder
		if wukongCfg.Cortex.EmbeddingBaseURL != "" &&
			wukongCfg.Cortex.EmbeddingAPIKey != "" {
			embedder = cortex.NewEmbedder(&wukongCfg.Cortex)
			util.Logger.Info("cortex: embedding enabled",
				"model", wukongCfg.Cortex.EmbeddingModel,
			)
		}
		// Get the shared *sql.DB from the pool to avoid opening
		// a separate connection to the same database file.
		// This prevents "transaction has already been committed"
		// errors from concurrent session/memory/cortex writes.
		sharedDB, dbErr := dbPool.Shared().GetDB()
		if dbErr != nil {
			util.Logger.Warn("cortex: get shared db failed",
				slog.String("error", dbErr.Error()))
		}
		cortexStore, err = cortex.NewStore(
			&wukongCfg.Cortex, embedder, sharedDB,
		)
		if err != nil {
			util.Logger.Warn("cortex store init failed, "+
				"falling back to recall",
				slog.String("error", err.Error()))
			cortexStore = nil
		} else {
			util.Logger.Info("cortex: store initialized",
				"db_path", wukongCfg.Cortex.DBPath,
			)
			// Create a recall.Store adapter sharing the same DB
			// so the agent loop can call StoreMessage() as before.
			recallStore, err = cortexStore.RecallStore()
			if err != nil {
				util.Logger.Warn("cortex: recall adapter failed",
					slog.String("error", err.Error()))
				recallStore = nil
			}
		}
	} else if wukongCfg.Recall.Enabled {
		// Native SQLite FTS5 recall store (default).
		recallStore, err = recall.NewStore(
			&wukongCfg.Recall, dbPool.Shared(),
		)
		if err != nil {
			util.Logger.Warn("recall store init failed",
				slog.String("error", err.Error()))
			recallStore = nil
		}
	}

	// Create MemoryFlow service for conversation transcript,
	// wake-up context, and fact promotion. When CortexStore is
	// also enabled, share the same CortexDB instance to avoid
	// opening conflicting connections to the same database file.
	var memoryFlowSvc *cortex.MemoryFlowService
	if wukongCfg.MemoryFlow.Enabled {
		var planner memoryflow.QueryPlanner
		var extractor memoryflow.SessionExtractor

		// Resolve planner/extractor models: explicit config first,
		// then fall back to global lightweight_model.
		plannerModel := wukongCfg.MemoryFlow.PlannerModel
		if plannerModel == "" {
			plannerModel = wukongCfg.EffectiveLightweightModel()
		}
		extractorModel := wukongCfg.MemoryFlow.ExtractorModel
		if extractorModel == "" {
			extractorModel = wukongCfg.EffectiveLightweightModel()
		}

		if plannerModel != "" {
			planner = cortex.NewLLMQueryPlanner(
				factory, plannerModel,
			)
		}
		if extractorModel != "" {
			extractor = cortex.NewLLMSessionExtractor(
				factory, extractorModel,
			)
		}

		// Share the CortexDB instance when CortexStore is active.
		if cortexStore != nil && cortexStore.DB() != nil {
			mfs, err := cortex.NewMemoryFlowWithDB(
				&wukongCfg.MemoryFlow, cortexStore.DB(),
				planner, extractor)
			if err != nil {
				util.Logger.Warn("memoryflow init failed "+
					"(shared db)",
					slog.String("error", err.Error()))
			} else {
				memoryFlowSvc = mfs
				util.Logger.Info("memoryflow: service initialized "+
					"(shared cortexdb)",
					"db_path", wukongCfg.MemoryFlow.DBPath,
				)
			}
		} else {
			mfs, err := cortex.NewMemoryFlow(
				&wukongCfg.MemoryFlow, planner, extractor)
			if err != nil {
				util.Logger.Warn("memoryflow init failed",
					slog.String("error", err.Error()))
			} else {
				memoryFlowSvc = mfs
				util.Logger.Info("memoryflow: service initialized",
					"db_path", wukongCfg.MemoryFlow.DBPath,
				)
				// When MemoryFlow created its own CortexDB,
				// share it back to CortexStore if it was
				// lexical-only (no embedder).
				if cortexStore != nil && cortexStore.DB() == nil {
					cortexStore.SetDB(memoryFlowSvc.DB())
					util.Logger.Info("cortex: shared cortexdb " +
						"from memoryflow")
				}
			}
		}
	}

	// Create recall manager for tools.
	// When cortex is enabled, recall tools use vector-enhanced search.
	var recallMgr *recall.RecallManager
	var cortexRecallMgr *cortex.RecallManager
	if cortexStore != nil && recallStore != nil {
		// Use CortexDB vector search for recall tools.
		cortexRecallMgr = cortex.NewRecallManager(cortexStore)
		// Wire tRPC memory reader so recall_search results
		// include persistent memories alongside conversation
		// history.
		cortexRecallMgr.SetMemoryReader(
			func(ctx context.Context, query string) ([]string, error) {
				userKey := tRPCMemory.UserKey{
					AppName: "wukong-app",
					UserID:  userID,
				}
				entries, err := memoryMgr.Service().SearchMemories(
					ctx, userKey, query)
				if err != nil {
					return nil, err
				}
				texts := make([]string, 0, len(entries))
				for _, e := range entries {
					if e.Memory != nil && e.Memory.Memory != "" {
						texts = append(texts, e.Memory.Memory)
					}
				}
				return texts, nil
			},
		)
		util.Logger.Info("recall_search: cross-searching " +
			"tRPC persistent memories")
	} else if recallStore != nil {
		recallMgr = recall.NewRecallManager(recallStore)
	}

	// Create GraphFlow service for knowledge graph construction.
	var kgToolMgr *cortex.KGToolManager
	var graphFlowSvc *cortex.GraphFlowService
	if wukongCfg.GraphFlow.Enabled {
		extractorModel := wukongCfg.GraphFlow.ExtractorModel
		if extractorModel == "" {
			extractorModel = wukongCfg.EffectiveLightweightModel()
		}
		var jsonGen graphflow.JSONGenerator
		if extractorModel != "" {
			jsonGen = cortex.NewLLMJSONGenerator(
				factory, extractorModel,
			)
		}
		gfs, err := cortex.NewGraphFlow(
			&wukongCfg.GraphFlow, jsonGen)
		if err != nil {
			util.Logger.Warn("graphflow init failed",
				slog.String("error", err.Error()))
		} else {
			kgToolMgr = cortex.NewKGToolManager(gfs)
			graphFlowSvc = gfs
			util.Logger.Info("graphflow: service initialized",
				"db_path", wukongCfg.GraphFlow.DBPath,
			)
			if wukongCfg.GraphFlow.AutoExtract {
				util.Logger.Info("graphflow: auto-extract enabled — " +
					"entities will be extracted after each turn")
			}
		}
	}

	// Create ImportFlow service for structured data import.
	var importToolMgr *cortex.ImportToolManager
	if wukongCfg.ImportFlow.Enabled {
		ifs, err := cortex.NewImportFlow(&wukongCfg.ImportFlow)
		if err != nil {
			util.Logger.Warn("importflow init failed",
				slog.String("error", err.Error()))
		} else {
			// Use lightweight model for LLM-enhanced DDL mapping,
			// same as GraphFlow extractor model resolution.
			importModel := wukongCfg.GraphFlow.ExtractorModel
			if importModel == "" {
				importModel = wukongCfg.EffectiveLightweightModel()
			}
			var jsonGen graphflow.JSONGenerator
			if importModel != "" {
				jsonGen = cortex.NewLLMJSONGenerator(
					factory, importModel,
				)
			}
			importToolMgr = cortex.NewImportToolManager(ifs, jsonGen)
			util.Logger.Info("importflow: service initialized",
				"db_path", wukongCfg.ImportFlow.DBPath,
				"llm_model", importModel,
			)
		}
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

	// Initialize the Skill Evolution engine.
	// When enabled, skill execution traces are captured and analyzed
	// by an LLM to detect issues and automatically patch SKILL.md files.
	var evoEngine *evolution.EvolutionEngine
	if wukongCfg.Evolution.Enabled {
		evoEngine, err = evolution.NewEngine(evolution.EngineConfig{
			Config:  wukongCfg,
			Factory: factory,
			DBPool:  dbPool.Shared(),
		})
		if err != nil {
			util.Logger.Warn("evolution engine init failed",
				"error", err.Error())
		} else {
			// Wire evolution hook into skill manager so traces
			// are captured when skill agents execute.
			// Adapter converts skill.SkillExecutionTrace to
			// evolution.ExecutionTrace.
			skillMgr.SetEvolutionHook(
				&skillEvoAdapter{engine: evoEngine},
			)
			// Set the skill manager as refresher so the engine
			// can trigger hot-reload after patches are applied.
			evoEngine.SetRefresher(skillMgr)
		}
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
	// Uses RemoteDelegateTool (agenttool.NewTool) to expose the
	// A2A agent as a callable function tool with concurrency control.
	for _, remote := range wukongCfg.Summon.A2ARemotes {
		a2aAgent := a2aRemoteToConfig(remote)
		if a2aAgent == nil {
			util.Logger.Warn("A2A remote agent init failed",
				"agent", remote.Name)
			continue
		}
		// Wrap the A2A agent as a tool for the main agent.
		remoteTool := summon.RemoteDelegateTool(
			"a2a_"+remote.Name,
			"Remote A2A agent: "+remote.ServerURL,
			a2aAgent.Agent(),
		)
		summonTools = append(summonTools,
			summonMgr.WrapTool(remoteTool, remote.Name),
		)
		util.Logger.Info("A2A remote agent registered as tool",
			"agent", remote.Name,
			"server_url", remote.ServerURL)
	}

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
		CortexStore:           cortexStore,
		RevisionModel:         revisionModel,
		MemoryFlowService:     memoryFlowSvc,
		GraphFlowService:      graphFlowSvc,
		TopOfMindInstructions: topOfMindInstructions,
		TelemetryShutdown:     combinedShutdown,
		MemoryClose:           memoryMgr.Close,
		EvolutionClose:        evoEngineClose(evoEngine),
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
		ARDRegistry:  ardRegistryServer,
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

	// Initialize ACP Server if enabled.
	// Exposes the agent via Agent Client Protocol endpoints
	// for ACP-compatible client applications.
	if wukongCfg.ACPServer.Enabled {
		acpCfg := &server.ACPServerConfig{
			Runner:          loop.GetRunner(),
			Agent:           loop.GetAgent(),
			Path:            wukongCfg.ACPServer.Path,
			EnableStreaming: wukongCfg.ACPServer.EnableStreaming,
		}
		acpSrv, acpErr := server.NewACPServer(acpCfg)
		if acpErr != nil {
			util.Logger.Warn("ACP server creation failed",
				"error", acpErr.Error())
		} else {
			acpAddr := wukongCfg.ACPServer.Address
			if acpAddr == "" {
				acpAddr = ":9091"
			}
			go func() {
				if err := acpSrv.Start(acpAddr); err != nil {
					util.Logger.Warn("ACP server failed",
						"error", err.Error())
				}
			}()
			state.ACPServer = acpSrv
		}
	}

	// Initialize ANP protocol stack if enabled.
	// Creates DID identity, meta-protocol engine, E2EE messenger,
	// and starts the ANP HTTP server for capability negotiation.
	if wukongCfg.ANP.Enabled {
		anpPort := wukongCfg.ANP.Port
		if anpPort <= 0 {
			anpPort = 9092
		}

		// Determine agent name and base URL for DID identity.
		anpAgentName := wukongCfg.A2AServer.AgentName
		if anpAgentName == "" {
			anpAgentName = "wukong"
		}
		anpBaseURL := fmt.Sprintf("http://localhost:%d", anpPort)

		// Resolve DID domain from config or use localhost as fallback.
		didDomain := wukongCfg.ANP.DIDDomain
		if didDomain == "" {
			hostname, _ := os.Hostname()
			didDomain = hostname
		}
		if didDomain == "" {
			didDomain = "localhost"
		}

		// Step 1: Create DID Manager for cryptographic identity.
		didMgr, didErr := ard.NewDIDManager(&ard.DIDManagerConfig{
			Domain:    didDomain,
			Path:      wukongCfg.ANP.DIDPath,
			AgentName: anpAgentName,
			BaseURL:   anpBaseURL,
		})
		if didErr != nil {
			util.Logger.Warn("ANP: DID manager creation failed",
				"error", didErr.Error())
		}

		// Step 2: Build meta-protocol engine for capability
		// negotiation.
		if didMgr != nil && wukongCfg.ANP.MetaProtocolEnabled {
			metaCfg := summon.BuildMetaProtocolConfig(
				didMgr.DID(),
				anpBaseURL,
				wukongCfg.A2AServer.Enabled,
				parsePort(wukongCfg.A2AServer.Address),
				wukongCfg.ACPServer.Enabled,
				parsePort(wukongCfg.ACPServer.Address),
				wukongCfg.AGUI.Enabled,
				parsePort(wukongCfg.AGUI.Address),
				"didwba_sc",
			)
			metaEngine := summon.NewMetaProtocol(metaCfg)
			state.ANPMeta = metaEngine

			util.Logger.Info("ANP: meta-protocol engine initialized",
				"did", didMgr.DID(),
				"port", anpPort)
		}

		// Step 3: Create E2EE messenger for encrypted
		// agent-to-agent messaging.
		if didMgr != nil && wukongCfg.ANP.E2EEEnabled {
			e2eeMgr := summon.NewE2EEMessenger(
				&summon.E2EEMessengerConfig{
					DIDManager: didMgr,
				},
			)
			state.ANPMessenger = e2eeMgr

			util.Logger.Info("ANP: E2EE messenger initialized",
				"did", didMgr.DID(),
				"active_sessions",
				e2eeMgr.ActiveSessions())
		}

		// Step 4: Start ANP HTTP server for meta-protocol
		// and capability discovery endpoints.
		if wukongCfg.ANP.MetaProtocolEnabled {
			anpMux := http.NewServeMux()

			// Register meta-protocol handler (JSON-RPC 2.0)
			if state.ANPMeta != nil {
				metaHandler := summon.NewMetaProtocolHandler(
					state.ANPMeta,
				)
				anpMux.Handle(
					"/anp/meta-protocol",
					metaHandler,
				)
				anpMux.HandleFunc(
					"/anp/capabilities",
					func(w http.ResponseWriter,
						r *http.Request) {
						metaHandler.ServeHTTP(w, r)
					},
				)
				util.Logger.Info(
					"ANP: meta-protocol endpoints registered",
					"endpoints",
					"/anp/meta-protocol, /anp/capabilities")
			}

			anpAddr := fmt.Sprintf(":%d", anpPort)
			anpSrv := &http.Server{
				Addr:         anpAddr,
				Handler:      anpMux,
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 30 * time.Second,
				IdleTimeout:  60 * time.Second,
			}
			state.ANPServer = anpSrv

			go func() {
				util.Logger.Info("ANP: server starting",
					"address", anpAddr)
				if err := anpSrv.ListenAndServe(); err != nil &&
					err != http.ErrServerClosed {
					util.Logger.Warn("ANP: server error",
						"error", err.Error())
				}
			}()
		}
	}

	// Report sandbox capability at startup so users know what
	// filesystem write protection is active.
	probe := sandbox.Probe()
	if probe.Sandboxed {
		util.Logger.Info("sandbox: filesystem write protection active",
			"backend", probe.Backend,
			"platform", probe.Platform,
		)
	} else {
		util.Logger.Warn("sandbox: filesystem write protection unavailable",
			"reason", sandbox.ReasonUnavailable(),
			"warning", probe.Warning,
		)
	}

	// Initialize Gateway server for multi-platform messaging
	// channels (Feishu, WeCom, Slack, etc.).
	if wukongCfg.Gateway.Enabled {
		gwStore := gateway.NewGatewaySessionStore(dbPool.Shared())
		state.GatewayServer = gateway.NewGatewayServer(
			wukongCfg, loop, gwStore,
		)

		// Register Feishu channel if enabled.
		if wukongCfg.Gateway.Feishu.Enabled {
			fc := feishu.NewFeishuChannel(wukongCfg, loop)
			if err := state.GatewayServer.RegisterChannel(fc); err != nil {
				util.Logger.Warn("gateway: register feishu failed",
					slog.String("error", err.Error()))
			} else {
				util.Logger.Info("gateway: feishu channel registered")
			}
		}

		// Register WeCom channel if enabled.
		if wukongCfg.Gateway.WeCom.Enabled {
			wc := wecom.NewWeComChannel(wukongCfg, loop)
			if err := state.GatewayServer.RegisterChannel(wc); err != nil {
				util.Logger.Warn("gateway: register wecom failed",
					slog.String("error", err.Error()))
			} else {
				util.Logger.Info("gateway: wecom channel registered")
			}
		}

		go func() {
			util.Logger.Info("gateway: server starting",
				"address", wukongCfg.Gateway.Address)
			if err := state.GatewayServer.Start(); err != nil &&
				err.Error() != "http: Server closed" {
				util.Logger.Warn("gateway: server error",
					"error", err.Error())
			}
		}()
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
	extractorProviderName := memCfg.ExtractorProvider
	if extractorProviderName == "" {
		extractorProviderName = wukongCfg.EffectiveLightweightProvider()
	}
	extractorModelName := memCfg.ExtractorModel
	if extractorModelName == "" {
		extractorModelName = wukongCfg.EffectiveLightweightModel()
	}

	extractorProvider := wukongCfg.FindProvider(extractorProviderName)
	if extractorProvider == nil {
		return nil, fmt.Errorf(
			"extractor provider %q not found in providers list",
			extractorProviderName,
		)
	}
	// Temporarily override the provider's model for extraction.
	originalModel := extractorProvider.Model
	extractorProvider.Model = extractorModelName
	defer func() {
		extractorProvider.Model = originalModel
	}()
	return factory.CreateModel(extractorProviderName)
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
// quickConfig holds minimal session info for immediate display.
type quickConfig struct {
	provider string
	model    string
}

// quickLoadConfig performs a minimal config load to get provider
// and model info before the full bootstrap. This gives the user
// immediate feedback without waiting for all subsystems to start.
func quickLoadConfig(
	configPath, cliProvider, cliModel string,
) quickConfig {
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return quickConfig{provider: "unknown", model: "unknown"}
	}
	cfg, err := loader.Load()
	if err != nil {
		return quickConfig{provider: "unknown", model: "unknown"}
	}

	provider := cliProvider
	if provider == "" {
		provider = cfg.DefaultProvider
	}

	model := cliModel
	if model == "" {
		if p := cfg.FindProvider(provider); p != nil {
			model = p.Model
		}
	}

	return quickConfig{provider: provider, model: model}
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

// evoEngineClose returns a close function for the evolution engine,
// or nil if the engine is nil (not enabled).
func evoEngineClose(engine *evolution.EvolutionEngine) func() error {
	if engine == nil {
		return nil
	}
	return engine.Close
}

// skillEvoAdapter converts skill.SkillExecutionTrace to
// evolution.ExecutionTrace and forwards it to the evolution engine.
// This avoids import cycles between the skill and evolution packages.
type skillEvoAdapter struct {
	engine *evolution.EvolutionEngine
}

func (a *skillEvoAdapter) RecordExecution(
	trace *skill.SkillExecutionTrace,
) {
	if a.engine == nil || trace == nil {
		return
	}
	a.engine.RecordExecution(&evolution.ExecutionTrace{
		SkillName:    trace.SkillName,
		SkillFile:    trace.SkillFile,
		SessionID:    trace.SessionID,
		UserID:       trace.UserID,
		StartTime:    trace.StartTime,
		EndTime:      trace.EndTime,
		Duration:     trace.Duration,
		LLMCalls:     trace.LLMCalls,
		Error:        trace.Error,
		ErrorCount:   trace.ErrorCount,
		FinalOutput:  trace.FinalOutput,
		OutputLength: trace.OutputLength,
		Success:      trace.Success,
	})
}

// parsePort extracts the numeric port from an address string like
// ":9090" or "localhost:9090". Returns the port or 0 if unparseable.
func parsePort(address string) int {
	if address == "" {
		return 0
	}
	// Strip the optional host prefix.
	colonIdx := strings.LastIndex(address, ":")
	if colonIdx < 0 {
		return 0
	}
	portStr := address[colonIdx+1:]
	p := 0
	for _, c := range portStr {
		if c < '0' || c > '9' {
			return 0
		}
		p = p*10 + int(c-'0')
	}
	return p
}
