// caps.go implements the "caps" command group: read-only inspection
// of the unified capability registry (roadmap P0-1, Phase A).
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/apps"
	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/codemode"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/extension"
	"github.com/km269/wukong/internal/extension/builtin"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/topofmind"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newCapsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "caps",
		Short: "Inspect the capability registry",
		Long: `Inspect and invoke Wukong's unified capability registry.

Every callable capability carries a stable address:

  tools.<extension>.<tool>    built-in extension tools
  mcp.<server>.<tool>         external MCP server tools
  mcp.broker.<tool>           MCP Broker meta tools
  tools.extension_manager.*   extension management tools
  tools.top_of_mind.*         session-assembled toolsets
  tools.code_mode.*           (top_of_mind / code_mode /
  tools.apps.*                 apps / agent_tools)
  tools.agent_tools.*
  recipe.<name>               recipe sub-agents

The registry is the single tool-aggregation source for the agent
loop (capability roadmap P0-1, Phase B+). Engine function tools
(todo / recall / summon) still bypass the registry.

Examples:
  wukong caps list                 # all capabilities
  wukong caps list tools           # built-in extensions only
  wukong caps list mcp             # external MCP tools only
  wukong caps list tools.web       # a single extension
  wukong caps list --json
  wukong caps run tools.web.web_search --args '{"query":"go"}'`,
	}

	cmd.AddCommand(newCapsListCmd())
	cmd.AddCommand(newCapsRunCmd())

	return cmd
}

// newCapsRunCmd creates the "caps run" subcommand (roadmap P0-1
// Phase C): invoke a registered capability directly by address.
func newCapsRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <address>",
		Short: "Invoke a capability by its bus address",
		Long: `Invoke a registered capability directly by its bus address and
print the JSON result.

The capability registry is built exactly as in a live session, so
every address shown by "wukong caps list" is runnable — including
recipe sub-agents (recipe.<name>), which perform real LLM calls.

Notes:
  - The invocation is real: shell tools execute commands, web tools
    hit the network, recipe tools call the configured LLM.
  - Tools that depend on bootstrap-injected services (e.g. memory
    backends) may fail outside a live session.

Examples:
  wukong caps run tools.developer.developer_directory_list --args '{"path":"."}'
  wukong caps run tools.web.web_search --args '{"query":"golang"}'
  wukong caps run recipe.my-reviewer --args '{"code_path":"main.go"}'`,
		Args: cobra.ExactArgs(1),
		RunE: runCapsRun,
	}
	cmd.Flags().String("args", "", "JSON object with invocation arguments")
	return cmd
}

func runCapsRun(cmd *cobra.Command, args []string) error {
	address := args[0]
	argsJSON, _ := cmd.Flags().GetString("args")

	loader, err := config.NewLoader("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Mirror bootstrapSession registration (see runCapsList).
	builtin.RegisterBuiltins(wukongCfg)

	ctx := context.Background()
	extMgr := extension.NewManager(wukongCfg)
	if err := extMgr.Initialize(ctx); err != nil {
		return fmt.Errorf("init extension manager: %w", err)
	}
	defer extMgr.Close()

	reg := capability.NewRegistry()
	if _, err := extMgr.RegisterCapabilities(reg, ctx); err != nil {
		return fmt.Errorf("register capabilities: %w", err)
	}
	registerSessionToolsets(reg, wukongCfg)

	cap, ok := reg.Resolve(address)
	if !ok {
		return fmt.Errorf(
			"capability %q not found — run 'wukong caps list' "+
				"to see registered addresses", address)
	}

	if argsJSON == "" {
		argsJSON = "{}"
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, []byte(argsJSON), "", "  ") != nil {
		return fmt.Errorf("invalid --args JSON: %s", argsJSON)
	}

	result, err := cap.Invoke(ctx, json.RawMessage(argsJSON))
	if err != nil {
		return fmt.Errorf("invoke %s: %w", address, err)
	}

	out := cmd.OutOrStdout()
	var buf bytes.Buffer
	if json.Indent(&buf, result, "", "  ") != nil {
		buf.Reset()
		buf.Write(result)
	}
	_, _ = fmt.Fprintf(out, "# %s\n", cap.Descriptor().Address)
	_, _ = fmt.Fprintln(out, buf.String())
	return nil
}

// newCapsListCmd creates the "caps list" subcommand.
func newCapsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [namespace]",
		Short: "List registered capabilities",
		Long: `List capabilities registered in the capability registry.

The optional namespace argument filters by address prefix: "tools",
"mcp", or a deeper prefix such as "tools.web".`,
		Args: cobra.MaximumNArgs(1),
		RunE: runCapsList,
	}
	cmd.Flags().Bool("json", false, "Output as JSON")
	return cmd
}

func runCapsList(cmd *cobra.Command, args []string) error {
	jsonOut, _ := cmd.Flags().GetBool("json")
	namespace := ""
	if len(args) > 0 {
		namespace = args[0]
	}

	loader, err := config.NewLoader("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	wukongCfg, err := loader.Load()
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	// Mirror bootstrapSession: register built-in extensions that are
	// not explicitly listed in the YAML config, otherwise a config
	// without an extensions section would list nothing.
	builtin.RegisterBuiltins(wukongCfg)

	ctx := context.Background()

	// Same wiring as "wukong extension list": initializing the
	// manager connects external MCP servers so their tools can be
	// enumerated.
	extMgr := extension.NewManager(wukongCfg)
	if err := extMgr.Initialize(ctx); err != nil {
		return fmt.Errorf("init extension manager: %w", err)
	}
	defer extMgr.Close()

	// Build the registry the same way bootstrapSession does, so the
	// listing matches what a live session registers.
	reg := capability.NewRegistry()
	if _, err := extMgr.RegisterCapabilities(reg, ctx); err != nil {
		return fmt.Errorf("register capabilities: %w", err)
	}
	registerSessionToolsets(reg, wukongCfg)

	descs := reg.List(namespace)
	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(descs); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}
		return nil
	}

	if len(descs) == 0 {
		if namespace != "" {
			fmt.Printf("No capabilities under %q.\n", namespace)
		} else {
			fmt.Println("No capabilities registered.")
		}
		return nil
	}

	fmt.Printf("%-40s %-28s %-8s %-5s %s\n",
		"ADDRESS", "NAME", "SOURCE", "MUT", "DESCRIPTION")
	fmt.Println(strings.Repeat("-", 150))
	for _, d := range descs {
		mut := "yes"
		if !d.Mutating {
			mut = "no"
		}
		fmt.Printf("%-40s %-28s %-8s %-5s %s\n",
			d.Address,
			truncateRunes(d.Name, 28),
			d.Source,
			mut,
			truncateRunes(d.Description, 56),
		)
	}
	fmt.Printf(
		"\n%d capability(ies) shown. Recipe sub-agents appear under "+
			"recipe.* when configured; engine function tools "+
			"(todo/recall/summon) bypass the registry.\n",
		len(descs),
	)
	return nil
}

// registerSessionToolsets registers the toolsets that factory.go
// returns as placeholders and bootstrapSession assembles at runtime
// (top_of_mind, code_mode, apps, agent_tools). Construction mirrors
// bootstrapSession; the constructors are configuration wiring
// without network or database side effects, so it is safe in this
// read-only command.
func registerSessionToolsets(
	reg *capability.Registry, wukongCfg *config.WukongConfig,
) {
	ctx := context.Background()
	register := func(prefix string, ts tool.ToolSet) {
		if ts == nil {
			return
		}
		if _, err := extension.RegisterToolSet(
			reg, prefix, capability.SourceBuiltin, ts, ctx,
		); err != nil {
			util.Logger.Warn("capability: toolset registration failed",
				"prefix", prefix, "error", err.Error())
		}
	}

	register(extension.AddrTopOfMind, builtin.NewTopOfMindToolSet(
		topofmind.NewManager(&wukongCfg.TopOfMind)))

	register(extension.AddrCodeMode, builtin.NewCodeModeToolSet(
		codemode.NewExecutor(&wukongCfg.CodeMode)))

	appsMgr, err := apps.NewManager(&wukongCfg.Apps)
	if err != nil {
		util.Logger.Warn("apps manager init failed (caps listing)",
			"error", err.Error())
	}
	if appsMgr != nil {
		register(extension.AddrApps, builtin.NewAppsToolSet(appsMgr))
	}

	agentToolSet := builtin.NewAgentToolSet(
		provider.NewFactory(wukongCfg), &wukongCfg.Agent)
	if agentToolSet != nil && len(agentToolSet.Tools(ctx)) > 0 {
		register(extension.AddrAgentTools, agentToolSet)
	}

	// Recipe sub-agents register under "recipe.*" (Phase C). This
	// performs real construction (provider factory); invocation
	// additionally needs a reachable LLM endpoint.
	if wukongCfg.Agent.RecipeEnabled {
		rts := agent.NewRecipeToolSet(
			provider.NewFactory(wukongCfg), &wukongCfg.Agent, nil)
		if rts != nil {
			defer rts.Close() //nolint:errcheck // inspection command
			agent.SyncRecipeCapabilities(
				reg, rts.Tools(ctx), wukongCfg.Agent.ToolCallTimeout)
		}
	}

	// Declarative flows register under "flow.*" (P0-2). A registry
	// is required here so capability nodes can resolve addresses at
	// invocation time.
	if wukongCfg.Agent.FlowEnabled {
		fts := agent.NewFlowToolSet(
			provider.NewFactory(wukongCfg), &wukongCfg.Agent, reg)
		if fts != nil {
			defer fts.Close() //nolint:errcheck // inspection command
			agent.SyncFlowCapabilities(reg, fts.Tools(ctx))
		}
	}
}

// truncateRunes shortens s to max runes (including an ellipsis) so
// CJK descriptions never split mid-rune in the table output.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}
