// bootstrap_phases.go — phase functions extracted from
// bootstrapSession (P2-8 function-level staging). Each phase owns
// one initialization concern; warn-and-continue vs fatal semantics
// are preserved exactly as in the original inline code (fatal phases
// return an error; optional phases log a warning and return nil).
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/ard"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/cortex"
	"github.com/km269/wukong/internal/evolution"
	"github.com/km269/wukong/internal/extension"
	"github.com/km269/wukong/internal/gateway"
	"github.com/km269/wukong/internal/gateway/feishu"
	"github.com/km269/wukong/internal/memory"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/recall"
	"github.com/km269/wukong/internal/security"
	"github.com/km269/wukong/internal/server"
	wksession "github.com/km269/wukong/internal/session"
	"github.com/km269/wukong/internal/skill"
	"github.com/km269/wukong/internal/summon"
	"github.com/km269/wukong/internal/telemetry"
	"github.com/km269/wukong/internal/util"
	"github.com/km269/wukong/pkg/sandbox"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"

	tRPCMemory "trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// guardCheckFunc validates a tool call for non-interactive protocol
// endpoints (ACP / MCP): permission → command arguments → approval.
type guardCheckFunc func(toolName string, args map[string]any, argsJSON []byte) error

// initTelemetry initializes OpenTelemetry tracing. Returns the
// shutdown hook (nil when tracing is unavailable — non-fatal).
func initTelemetry(cfg *config.WukongConfig) func(context.Context) error {
	telMgr := telemetry.NewManager(cfg.Telemetry)
	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer initCancel()
	telShutdown, err := telMgr.Initialize(initCtx)
	if err != nil {
		util.Logger.Warn("telemetry init failed, continuing without tracing",
			"error", err.Error())
	}
	// Note: telShutdown is invoked from the CoreLoop's closeFn via the
	// combined shutdown chain.
	return telShutdown
}

// initSessionStack creates the session service and the model-visible
// event log (both share the pooled SQLite database).
func initSessionStack(
	cfg *config.WukongConfig, dbPool *util.MultiPool,
) (*wksession.SessionService, *wksession.ModelEventLog, error) {
	sessionSvc, err := wksession.NewSessionService(
		&cfg.Session, dbPool.Shared(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create session: %w", err)
	}

	// Create the model-visible event log. This records the messages
	// the model ACTUALLY sees after context enrichment (wakeup/
	// recall/persistent), enforcing the "model-visible means logged"
	// invariant.
	var modelEventLog *wksession.ModelEventLog
	if cfg.Session.EnableModelEventLog {
		sharedDB, dbErr := dbPool.Shared().GetDB()
		if dbErr != nil {
			util.Logger.Warn("model event log: open shared db failed, "+
				"continuing without model-visible logging",
				"error", dbErr.Error())
		} else {
			mel, melErr := wksession.NewModelEventLog(sharedDB)
			if melErr != nil {
				util.Logger.Warn("model event log: init failed, "+
					"continuing without model-visible logging",
					"error", melErr.Error())
			} else {
				modelEventLog = mel
			}
		}
	}
	return sessionSvc, modelEventLog, nil
}

// initMemoryStack creates the tRPC long-term memory manager with
// auto-extract support and runs the startup SmartCleanup sweep.
func initMemoryStack(
	cfg *config.WukongConfig,
	factory *provider.Factory,
	dbPool *util.MultiPool,
	userID string,
) (*memory.MemoryManager, error) {
	// If an extractor_provider or extractor_model is configured in
	// the memory block, use that instead of the default provider.
	// Falls back to default model if the extractor model fails.
	var extractorModel model.Model
	var err error
	if cfg.Memory.AutoExtract {
		extractorModel, err = createExtractorModel(
			factory, &cfg.Memory, cfg,
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
		&cfg.Memory, extractorModel, dbPool.Shared(),
	)
	if err != nil {
		return nil, fmt.Errorf("create memory: %w", err)
	}

	// Smart cleanup: evict low-importance memories when near capacity.
	if cfg.Memory.MaxMemories > 0 {
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
	return memoryMgr, nil
}

// buildGuardCheck builds the single guard callback used by both the
// ACP and MCP servers. Centralising here keeps the non-interactive
// rejection policy consistent across both surfaces and avoids
// duplicating the command/permission/approval logic at each call site.
func buildGuardCheck(guard *security.Guard) guardCheckFunc {
	return func(toolName string, args map[string]any, argsJSON []byte) error {
		if err := guard.CheckToolPermission(toolName, nil); err != nil {
			return err
		}
		// Validate command arguments for shell-like tools.
		switch toolName {
		case "developer_command_execute", "bash", "shell", "command_execute":
			if cmdStr, _ := args["command"].(string); cmdStr != "" {
				if err := guard.ValidateCommand(cmdStr); err != nil {
					return err
				}
			}
		}
		// ACP/MCP are non-interactive: reject tools that require
		// human approval, since there is no client to confirm.
		if guard.NeedsApproval(toolName, argsJSON) {
			return fmt.Errorf("tool %q requires human approval; "+
				"non-interactive endpoint cannot confirm", toolName)
		}
		return nil
	}
}

// initARDStack initializes the ARD ToolSet (wired into the extension
// manager for MCP auto-registration) and optionally starts Wukong's
// own ARD registry server so other ARD-compatible agents can discover
// Wukong on the network.
func initARDStack(
	cfg *config.WukongConfig, extMgr *extension.Manager,
) *ard.RegistryServer {
	var ardRegistryServer *ard.RegistryServer
	if cfg.ARD.Enabled {
		ardTS, ardErr := ard.NewToolSet(
			cfg.ARD.RegistryURL,
			cfg.ARD.CatalogPath,
		)
		if ardErr != nil {
			util.Logger.Warn("ard: failed to create toolset",
				"error", ardErr.Error())
		} else {
			// Wire ARD to extension manager for MCP auto-registration.
			extMgr.SetARDToolSet(ardTS)

			// Auto-register A2A remote agents to ARD catalog.
			for _, remote := range cfg.Summon.A2ARemotes {
				ard.RegisterA2AAgent(ardTS, remote.Name,
					remote.Description, remote.ServerURL)
			}
		}

		// Start Wukong's own ARD registry server for inbound discovery.
		if cfg.ARD.PublishEnabled && cfg.ARD.PublishPort > 0 {
			anpOpts := ard.ANPPublishOptions{
				Enabled: cfg.ANP.Enabled &&
					cfg.ANP.DiscoveryEnabled,
				BaseURL: fmt.Sprintf("http://localhost:%d",
					cfg.ARD.PublishPort),
			}
			ardSrv, pubErr := ard.PublishAndServe(
				context.Background(),
				cfg.ARD.PublishPort,
				cfg.ARD.CatalogPath,
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
	return ardRegistryServer
}

// initRecallStack creates the recall stack: CortexDB hybrid store
// (vector + FTS5) when cortex.enabled, native SQLite FTS5 fallback
// when recall.enabled. The CortexStore is injected into the web
// toolset for internal index search.
func initRecallStack(
	cfg *config.WukongConfig,
	dbPool *util.MultiPool,
	extMgr *extension.Manager,
	userID string,
) (*recall.Store, *cortex.CortexStore) {
	var recallStore *recall.Store
	var cortexStore *cortex.CortexStore
	if cfg.Cortex.Enabled {
		// CortexDB-backed store with vector semantic search.
		var embedder *cortex.Embedder
		if cfg.Cortex.EmbeddingBaseURL != "" &&
			cfg.Cortex.EmbeddingAPIKey != "" {
			embedder = cortex.NewEmbedder(&cfg.Cortex)
			util.Logger.Info("cortex: embedding enabled",
				"model", cfg.Cortex.EmbeddingModel,
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
		var err error
		cortexStore, err = cortex.NewStore(
			&cfg.Cortex, embedder, sharedDB,
		)
		if err != nil {
			util.Logger.Warn("cortex store init failed, "+
				"falling back to recall",
				slog.String("error", err.Error()))
			cortexStore = nil
		} else {
			util.Logger.Info("cortex: store initialized",
				"db_path", cfg.Cortex.DBPath,
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
	} else if cfg.Recall.Enabled {
		// Native SQLite FTS5 recall store (default).
		var err error
		recallStore, err = recall.NewStore(
			&cfg.Recall, dbPool.Shared(),
		)
		if err != nil {
			util.Logger.Warn("recall store init failed",
				slog.String("error", err.Error()))
			recallStore = nil
		}
	}

	// Inject CortexStore into the web toolset for internal index search.
	if cortexStore != nil {
		extMgr.SetCortexStore(cortexStore, userID)
	}
	return recallStore, cortexStore
}

// initMemoryFlow creates the MemoryFlow service (transcript /
// wake-up / fact promotion). When CortexStore is also enabled, the
// same CortexDB instance is shared to avoid conflicting connections.
func initMemoryFlow(
	cfg *config.WukongConfig,
	factory *provider.Factory,
	cortexStore *cortex.CortexStore,
) *cortex.MemoryFlowService {
	var memoryFlowSvc *cortex.MemoryFlowService
	if !cfg.MemoryFlow.Enabled {
		return nil
	}
	var planner memoryflow.QueryPlanner
	var extractor memoryflow.SessionExtractor

	// Resolve planner/extractor models: explicit config first,
	// then fall back to global lightweight_model.
	plannerModel := cfg.MemoryFlow.PlannerModel
	if plannerModel == "" {
		plannerModel = cfg.EffectiveLightweightModel()
	}
	extractorModel := cfg.MemoryFlow.ExtractorModel
	if extractorModel == "" {
		extractorModel = cfg.EffectiveLightweightModel()
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
			&cfg.MemoryFlow, cortexStore.DB(),
			planner, extractor)
		if err != nil {
			util.Logger.Warn("memoryflow init failed "+
				"(shared db)",
				slog.String("error", err.Error()))
		} else {
			memoryFlowSvc = mfs
			util.Logger.Info("memoryflow: service initialized "+
				"(shared cortexdb)",
				"db_path", cfg.MemoryFlow.DBPath,
			)
		}
	} else {
		mfs, err := cortex.NewMemoryFlow(
			&cfg.MemoryFlow, planner, extractor)
		if err != nil {
			util.Logger.Warn("memoryflow init failed",
				slog.String("error", err.Error()))
		} else {
			memoryFlowSvc = mfs
			util.Logger.Info("memoryflow: service initialized",
				"db_path", cfg.MemoryFlow.DBPath,
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
	return memoryFlowSvc
}

// initRecallManagers creates the recall tool managers: vector-enhanced
// cross-search when cortex is active, plain FTS5 otherwise. The
// cortex variant also cross-searches tRPC persistent memories.
func initRecallManagers(
	cfg *config.WukongConfig,
	cortexStore *cortex.CortexStore,
	recallStore *recall.Store,
	memoryMgr *memory.MemoryManager,
	userID string,
) (*recall.RecallManager, *cortex.RecallManager) {
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
	return recallMgr, cortexRecallMgr
}

// initGraphAndImportStack creates the knowledge-graph (GraphFlow) and
// structured-import (ImportFlow) services. LLM extractors resolve to
// the global lightweight model when not explicitly configured.
func initGraphAndImportStack(
	cfg *config.WukongConfig,
	factory *provider.Factory,
) (*cortex.KGToolManager, *cortex.GraphFlowService, *cortex.ImportToolManager) {
	var kgToolMgr *cortex.KGToolManager
	var graphFlowSvc *cortex.GraphFlowService
	var importToolMgr *cortex.ImportToolManager

	// Create GraphFlow service for knowledge graph construction.
	if cfg.GraphFlow.Enabled {
		extractorModel := cfg.GraphFlow.ExtractorModel
		if extractorModel == "" {
			extractorModel = cfg.EffectiveLightweightModel()
		}
		var jsonGen graphflow.JSONGenerator
		if extractorModel != "" {
			jsonGen = cortex.NewLLMJSONGenerator(
				factory, extractorModel,
			)
		}
		gfs, err := cortex.NewGraphFlow(
			&cfg.GraphFlow, jsonGen)
		if err != nil {
			util.Logger.Warn("graphflow init failed",
				slog.String("error", err.Error()))
		} else {
			kgToolMgr = cortex.NewKGToolManager(gfs)
			graphFlowSvc = gfs
			util.Logger.Info("graphflow: service initialized",
				"db_path", cfg.GraphFlow.DBPath,
			)
			if cfg.GraphFlow.AutoExtract {
				util.Logger.Info("graphflow: auto-extract enabled — " +
					"entities will be extracted after each turn")
			}
		}
	}

	// Create ImportFlow service for structured data import.
	if cfg.ImportFlow.Enabled {
		ifs, err := cortex.NewImportFlow(&cfg.ImportFlow)
		if err != nil {
			util.Logger.Warn("importflow init failed",
				slog.String("error", err.Error()))
		} else {
			// Use lightweight model for LLM-enhanced DDL mapping,
			// same as GraphFlow extractor model resolution.
			importModel := cfg.GraphFlow.ExtractorModel
			if importModel == "" {
				importModel = cfg.EffectiveLightweightModel()
			}
			var jsonGen graphflow.JSONGenerator
			if importModel != "" {
				jsonGen = cortex.NewLLMJSONGenerator(
					factory, importModel,
				)
			}
			importToolMgr = cortex.NewImportToolManager(ifs, jsonGen)
			util.Logger.Info("importflow: service initialized",
				"db_path", cfg.ImportFlow.DBPath,
				"llm_model", importModel,
			)
		}
	}
	return kgToolMgr, graphFlowSvc, importToolMgr
}

// initSkillStack creates the skill manager (SKILL.md repository) and
// the evolution engine, wiring the execution-trace hook and the
// hot-reload refresher between them when evolution is enabled.
func initSkillStack(
	cfg *config.WukongConfig,
	factory *provider.Factory,
	dbPool *util.MultiPool,
) (*skill.Manager, *evolution.EvolutionEngine) {
	// Initialize Skill system using trpc-agent-go's FSRepository.
	// Skills are SKILL.md files that define specialized agent workflows.
	// Independent of Summon — skill agents are also usable without
	// sub-agent delegation enabled.
	skillMgr := skill.NewManager(cfg.Skill)
	if err := skillMgr.Initialize(context.Background()); err != nil {
		util.Logger.Warn("skill system init failed",
			"error", err.Error())
	}

	// Initialize the Skill Evolution engine.
	// When enabled, skill execution traces are captured and analyzed
	// by an LLM to detect issues and automatically patch SKILL.md files.
	var evoEngine *evolution.EvolutionEngine
	if cfg.Evolution.Enabled {
		var err error
		evoEngine, err = evolution.NewEngine(evolution.EngineConfig{
			Config:  cfg,
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
	return skillMgr, evoEngine
}

// initSummonStack initializes sub-agent delegation: local delegates,
// skill-as-delegate registration, A2A remote agents, and the OAuth2
// credential rotator. Returns the delegate tools and the rotator
// (nil when no OAuth2 remotes are configured or summon is disabled).
func initSummonStack(
	cfg *config.WukongConfig,
	factory *provider.Factory,
	skillMgr *skill.Manager,
) ([]tool.Tool, *summon.CredentialRotator) {
	// Collect Summon delegate tools with concurrency control.
	// Each delegate tool is wrapped to acquire a slot from the summon
	// manager's semaphore before execution, enforcing MaxConcurrent.
	var summonTools []tool.Tool
	// credRotator holds the A2A credential rotator when any OAuth2
	// remote agent is configured. Wired into BootstrapState so
	// shutdownBootstrap can stop the background loop cleanly.
	var credRotator *summon.CredentialRotator

	// Summon: sub-agent delegation. The entire subsystem (local
	// delegates, skill-as-delegate registration, A2A remote agents)
	// is skipped when summon.enabled is false.
	if !cfg.Summon.Enabled {
		return summonTools, credRotator
	}

	summonMdl, sErr := factory.CreateDefaultModel()
	if sErr != nil {
		util.Logger.Warn("failed to create summon model, "+
			"sub-agent delegation disabled",
			"error", sErr.Error())
	}
	summonMgr := summon.NewSummonManager(&cfg.Summon, summonMdl)
	if lErr := summonMgr.LoadDelegates(context.Background()); lErr != nil {
		util.Logger.Warn("summon delegates load failed",
			slog.String("error", lErr.Error()))
	}

	// Register Skill agents as Summon delegates so the main
	// agent can delegate to specialized skill agents.
	if skillMgr != nil && skillMgr.SkillCount() > 0 && summonMdl != nil {
		for _, s := range skillMgr.ListSummaries() {
			skillAgent, aErr := skillMgr.CreateSkillAgent(
				context.Background(), s.Name, summonMdl, nil,
			)
			if aErr != nil {
				util.Logger.Warn("skill agent creation failed",
					"skill", s.Name,
					"error", aErr.Error())
				continue
			}
			skillTool := summon.NewDelegateTool(
				skillAgent, "skill_"+s.Name, s.Description,
			)
			summonTools = append(summonTools,
				summonMgr.WrapTool(skillTool, s.Name),
			)
		}
	}

	// Register local Summon delegates as function tools.
	for _, d := range summonMgr.ListDelegates() {
		summonTools = append(summonTools,
			summonMgr.WrapTool(d.Tool(), d.Name()),
		)
	}

	// Register A2A remote agents as summon delegates.
	// OAuth2-authenticated remotes are also registered with a
	// CredentialRotator so their access tokens are refreshed
	// automatically before expiry (client_credentials grant).
	var oauthRemotes []config.A2ARemoteConfig
	for _, remote := range cfg.Summon.A2ARemotes {
		if remote.AuthType == "oauth2" &&
			remote.OAuthTokenURL != "" &&
			remote.OAuthClientID != "" {
			oauthRemotes = append(oauthRemotes, remote)
		}
	}
	if len(oauthRemotes) > 0 {
		// Default rotation interval: 1 hour. The rotator checks
		// each credential's NextRotation; refreshes when due.
		credRotator = summon.NewCredentialRotator(time.Hour)
		for _, remote := range oauthRemotes {
			gen := summon.NewOAuth2RefreshGenerator(
				summon.OAuth2RefreshOptions{
					TokenURL:     remote.OAuthTokenURL,
					ClientID:     remote.OAuthClientID,
					ClientSecret: remote.OAuthClientSecret,
				})
			initial := summon.CredentialSet{
				OAuthTokenURL:     remote.OAuthTokenURL,
				OAuthClientID:     remote.OAuthClientID,
				OAuthClientSecret: remote.OAuthClientSecret,
				// Access token left empty; the first rotation
				// tick will populate it. The rotator's pending
				// generator call in Register() also primes it.
			}
			if rErr := credRotator.Register(
				context.Background(),
				remote.Name, "oauth2", initial, gen,
			); rErr != nil {
				util.Logger.Warn("A2A OAuth2 rotator register failed",
					"agent", remote.Name,
					"error", rErr.Error())
			}
		}
		credRotator.Start(context.Background(), nil)
		util.Logger.Info("A2A OAuth2 credential rotator started",
			"registered", credRotator.CredentialCount())
	}

	for _, remote := range cfg.Summon.A2ARemotes {
		a2aAgent := a2aRemoteToConfig(remote)
		if a2aAgent == nil {
			util.Logger.Warn("A2A remote agent init failed",
				"agent", remote.Name)
			continue
		}
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
	return summonTools, credRotator
}

// startProtocolServers starts every config-gated protocol server
// (A2A / AG-UI / ACP / ANP / Gateway), reports the sandbox capability,
// and wires each server into state for the shutdown chain.
func startProtocolServers(
	cfg *config.WukongConfig,
	loop *agent.CoreLoop,
	guard *security.Guard,
	guardCheck guardCheckFunc,
	state *BootstrapState,
	dbPool *util.MultiPool,
) {
	// Initialize A2A server if enabled in config.
	// Uses tRPC-Agent-Go's server/a2a wrapper which provides
	// automatic protocol conversion, streaming, and session integration.
	// The main agent and runner are shared with the A2A endpoint
	// so remote clients get the full agent capabilities.
	if cfg.A2AServer.Enabled {
		hostAddr := cfg.A2AServer.Address
		if hostAddr == "" {
			hostAddr = ":9090"
		}

		a2aServerCfg := &summon.A2AServerConfig{
			Agent:          loop.GetAgent(),
			Runner:         loop.GetRunner(),
			SessionService: loop.GetSessionService(),
			Name:           cfg.A2AServer.AgentName,
			Description:    cfg.A2AServer.AgentDescription,
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
	if cfg.AGUI.Enabled {
		aguiCfg := &server.AGUIConfig{
			Runner: loop.GetRunner(),
			Path:   cfg.AGUI.Path,
		}
		aguiSrv, err := server.NewAGUIServer(aguiCfg)
		if err != nil {
			util.Logger.Warn("AG-UI server creation failed",
				"error", err.Error())
		} else {
			addr := cfg.AGUI.Address
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
	if cfg.ACPServer.Enabled {
		// Wire the asynchronous Approval protocol: a broker in HTTP
		// external-resolver mode lets the agent loop's BeforeTool
		// gate block on human decisions, which ACP clients then
		// resolve via /approvals/resolve. When ACP is disabled the
		// guard has no broker and falls back to the legacy
		// synchronous-deny path (safety never regresses).
		broker := security.NewApprovalBroker(nil, 0)
		guard.SetApprovalBroker(broker)
		acpCfg := &server.ACPServerConfig{
			Runner:          loop.GetRunner(),
			Agent:           loop.GetAgent(),
			GuardCheck:      server.ToolGuardCheck(guardCheck),
			ApprovalSink:    &approvalSinkAdapter{broker: broker},
			Path:            cfg.ACPServer.Path,
			EnableStreaming: cfg.ACPServer.EnableStreaming,
		}
		acpSrv, acpErr := server.NewACPServer(acpCfg)
		if acpErr != nil {
			util.Logger.Warn("ACP server creation failed",
				"error", acpErr.Error())
		} else {
			acpAddr := cfg.ACPServer.Address
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
	if cfg.ANP.Enabled {
		startANPStack(cfg, state)
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

	// Initialize Gateway server for messaging channels.
	// Each channel owns its own inbound transport (e.g. Feishu's
	// WebSocket long-connection); the gateway drives the shared
	// processing pipeline. There is no HTTP listener.
	if cfg.Gateway.Enabled {
		gwStore := gateway.NewGatewaySessionStore(dbPool.Shared())
		state.GatewayServer = gateway.NewGatewayServer(
			&cfg.Gateway, loop, gwStore,
		)

		// Register Feishu channel if enabled.
		if cfg.Gateway.Feishu.Enabled {
			fc := feishu.NewFeishuChannel(&cfg.Gateway.Feishu)
			// Fail-fast: refuse to register a misconfigured channel
			// rather than silently accepting messages it can never
			// reply to. This surfaces missing env vars (e.g.
			// FEISHU_APP_SECRET) at startup instead of at runtime.
			if err := fc.Validate(); err != nil {
				util.Logger.Error("gateway: feishu channel NOT registered",
					slog.String("reason", err.Error()))
			} else if err := state.GatewayServer.RegisterChannel(fc); err != nil {
				util.Logger.Warn("gateway: register feishu failed",
					slog.String("error", err.Error()))
			} else {
				util.Logger.Info("gateway: feishu channel registered")
			}
		}

		// Start the gateway in the background. Start blocks until the
		// context is cancelled; Stop() (invoked from the shutdown
		// chain) cancels the gateway's internal run context, which
		// propagates to each channel's Start and tears them down.
		go func() {
			util.Logger.Info("gateway: starting channels",
				slog.String("channels",
					strings.Join(state.GatewayServer.Channels(), ",")))
			if err := state.GatewayServer.Start(context.Background()); err != nil &&
				err != context.Canceled {
				util.Logger.Warn("gateway: server error",
					slog.String("error", err.Error()))
			}
		}()
	}
}

// startANPStack initializes the ANP protocol stack (DID identity,
// meta-protocol engine, E2EE messenger, HTTP endpoints).
func startANPStack(cfg *config.WukongConfig, state *BootstrapState) {
	anpPort := cfg.ANP.Port
	if anpPort <= 0 {
		anpPort = 9092
	}

	// Determine agent name and base URL for DID identity.
	anpAgentName := cfg.A2AServer.AgentName
	if anpAgentName == "" {
		anpAgentName = "wukong"
	}
	anpBaseURL := fmt.Sprintf("http://localhost:%d", anpPort)

	// Resolve DID domain from config or use localhost as fallback.
	didDomain := cfg.ANP.DIDDomain
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
		Path:      cfg.ANP.DIDPath,
		AgentName: anpAgentName,
		BaseURL:   anpBaseURL,
	})
	if didErr != nil {
		util.Logger.Warn("ANP: DID manager creation failed",
			"error", didErr.Error())
	}

	// Step 2: Build meta-protocol engine for capability
	// negotiation.
	if didMgr != nil && cfg.ANP.MetaProtocolEnabled {
		metaCfg := summon.BuildMetaProtocolConfig(
			didMgr.DID(),
			anpBaseURL,
			cfg.A2AServer.Enabled,
			parsePort(cfg.A2AServer.Address),
			cfg.ACPServer.Enabled,
			parsePort(cfg.ACPServer.Address),
			cfg.AGUI.Enabled,
			parsePort(cfg.AGUI.Address),
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
	if didMgr != nil && cfg.ANP.E2EEEnabled {
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
	if cfg.ANP.MetaProtocolEnabled {
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
