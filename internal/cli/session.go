package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/cli/tui"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/extension"
	"github.com/km269/wukong/internal/memory"
	"github.com/km269/wukong/internal/provider"
	wksession "github.com/km269/wukong/internal/session"
	"github.com/km269/wukong/internal/todo"

	"trpc.group/trpc-go/trpc-agent-go/model"
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

	return cmd
}

func runSession(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	sessionID, _ := cmd.Flags().GetString("session-id")
	provider, _ := cmd.Flags().GetString("provider")
	modelName, _ := cmd.Flags().GetString("model")

	userID := os.Getenv("USER")
	if userID == "" {
		userID = os.Getenv("USERNAME")
	}
	if userID == "" {
		userID = "default"
	}

	if sessionID == "" {
		sessionID = uuid.New().String()
	}

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
	wukongCfg, loop, err := bootstrapSession(
		configPath, userID, sessionID, provider, modelName,
	)
	if err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}
	defer loop.Close()

	fmt.Printf(
		"Session: %s\nProvider: %s\nModel: %s\n\n",
		sessionID[:8],
		wukongCfg.DefaultProvider,
		wukongCfg.DefaultProviderConfig().Model,
	)

	// Start TUI
	return tui.StartTUI(wukongCfg, loop, userID, sessionID)
}

// bootstrapSession initializes all components needed for a session.
func bootstrapSession(
	configPath, userID, sessionID, providerName, modelName string,
) (*config.WukongConfig, *agent.CoreLoop, error) {
	// Load config
	loader, err := config.NewLoader(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("parse config: %w", err)
	}

	// Apply command-line overrides to config
	applyOverrides(wukongCfg, providerName, modelName)

	// Create model factory
	factory := provider.NewFactory(wukongCfg)

	// Create session service
	sessionSvc, err := wksession.NewSessionService(&wukongCfg.Session)
	if err != nil {
		return nil, nil, fmt.Errorf("create session: %w", err)
	}

	// Ensure session exists
	_ = sessionSvc

	// Create memory manager
	memoryMgr, err := memory.NewMemoryManager(&wukongCfg.Memory)
	if err != nil {
		return nil, nil, fmt.Errorf("create memory: %w", err)
	}

	// Create extension manager and initialize
	extMgr := extension.NewManager(wukongCfg)
	// TODO: use proper context
	if err := extMgr.Initialize(nil); err != nil {
		return nil, nil, fmt.Errorf("init extensions: %w", err)
	}

	// Create todo manager
	todoStore, err := todo.NewStore(wukongCfg.Todo.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("create todo store: %w", err)
	}
	todoMgr := todo.NewTodoManager(todoStore)

	// Create agent loop
	loop, err := agent.NewCoreLoop(agent.CoreLoopConfig{
		Config:         wukongCfg,
		Factory:        factory,
		SessionService: sessionSvc,
		MemoryService:  memoryMgr.Service(),
		ToolSets:       extMgr.ToolSets(),
		FunctionTools:  todoMgr.Tools(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create agent loop: %w", err)
	}

	return wukongCfg, loop, nil
}

// applyOverrides applies command-line overrides to config.
func applyOverrides(
	cfg *config.WukongConfig,
	providerName string,
	modelName string,
) {
	if providerName != "" {
		p := cfg.FindProvider(providerName)
		if p == nil {
			fmt.Fprintf(
				os.Stderr,
				"warning: provider %q not found in config\n",
				providerName,
			)
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
}

// Ensure model import is used
var _ = model.NewUserMessage
