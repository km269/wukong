// flow_toolset.go — compiles YAML flows into callable tools and
// "flow.*" capabilities, with recipe-style hot reload (P0-2).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

// FlowToolSet loads YAML flow definitions from a directory and
// exposes each as a callable tool (flow-<name>) plus a
// "flow.<name>" capability. Supports hot-reload via the shared
// fsnotify watcher.
type FlowToolSet struct {
	mu    sync.Mutex
	tools []tool.Tool
	flows map[string]*compiledFlow

	// onReload runs after a successful Reload with the fresh tool
	// slice (capability-registry sync). Called with the toolset
	// lock held; must not call back into the toolset.
	onReload func(tools []tool.Tool)

	factory  *provider.Factory
	agentCfg *config.AgentConfig
	caps     *capability.Registry
	reloader *hotReloader
}

// NewFlowToolSet scans the configured flow directory, compiles each
// valid flow into an executable tool, and returns the toolset.
// Returns nil when flows are disabled or no flows are found. Agent
// nodes require the provider factory; a flow containing agent nodes
// with a nil factory is skipped with a warning (capability-only
// flows still build).
func NewFlowToolSet(
	factory *provider.Factory,
	agentCfg *config.AgentConfig,
	caps *capability.Registry,
) *FlowToolSet {
	if !agentCfg.FlowEnabled {
		return nil
	}

	flowDir := resolveFlowDir(agentCfg.FlowDir)
	flows := loadFlowDir(flowDir)
	if len(flows) == 0 {
		return nil
	}

	ts := &FlowToolSet{
		flows:    make(map[string]*compiledFlow, len(flows)),
		factory:  factory,
		agentCfg: agentCfg,
		caps:     caps,
	}

	var defaultModel model.Model
	needModel := false
	for _, f := range flows {
		for _, n := range f.Nodes {
			if n.Type == FlowNodeAgent {
				needModel = true
			}
		}
	}
	if needModel {
		if factory == nil {
			util.Logger.Warn("flow: agent nodes need a provider factory; " +
				"no factory wired — capability-only flows still load")
		} else if mdl, err := factory.CreateDefaultModel(); err != nil {
			util.Logger.Warn("flow: default model creation failed; "+
				"agent nodes will be skipped",
				slog.String("error", err.Error()))
		} else {
			defaultModel = mdl
		}
	}

	for name, cfg := range flows {
		cf, err := compileFlow(cfg, factory, defaultModel, caps)
		if err != nil {
			util.Logger.Warn("flow: skipping flow that failed to compile",
				slog.String("flow", name),
				slog.String("error", err.Error()))
			continue
		}
		ts.flows[name] = cf
		ts.tools = append(ts.tools, newFlowTool(cf))
	}
	if len(ts.tools) == 0 {
		return nil
	}
	sortFlowTools(ts.tools)

	util.Logger.Info("flow: compiled flows",
		slog.Int("count", len(ts.tools)))

	ts.reloader = newHotReloader(
		factory, flowDir, func() { ts.Reload() },
	)
	return ts
}

// compileFlow validates cfg and builds the per-node executors.
func compileFlow(
	cfg *FlowConfig,
	factory *provider.Factory,
	defaultModel model.Model,
	caps *capability.Registry,
) (*compiledFlow, error) {
	order, err := validateFlowConfig(cfg)
	if err != nil {
		return nil, err
	}

	cf := &compiledFlow{
		cfg:           cfg,
		order:         order,
		execs:         make(map[string]flowNodeExec, len(cfg.Nodes)),
		whenTemplates: make(map[string]*template.Template, len(cfg.Edges)),
	}

	// Compile edge conditions.
	for _, e := range cfg.Edges {
		if e.When == "" {
			continue
		}
		tmpl, err := template.New(e.From + "→" + e.To).Parse(e.When)
		if err != nil {
			return nil, fmt.Errorf(
				"edge %s→%s: parse when template: %w", e.From, e.To, err)
		}
		cf.whenTemplates[e.From+"→"+e.To] = tmpl
	}

	// Build node executors.
	for i := range cfg.Nodes {
		n := cfg.Nodes[i]
		var exec flowNodeExec
		switch n.Type {
		case FlowNodeAgent:
			if factory == nil {
				return nil, fmt.Errorf(
					"agent node %q needs a provider factory", n.Name)
			}
			if defaultModel == nil {
				return nil, fmt.Errorf(
					"agent node %q: no default model available", n.Name)
			}
			exec, err = buildAgentNodeExec(cfg, n, factory, defaultModel)
			if err != nil {
				return nil, err
			}
		case FlowNodeCapability:
			var argTmpls map[string]*template.Template
			if len(n.Args) > 0 {
				argTmpls = make(map[string]*template.Template, len(n.Args))
				for k, v := range n.Args {
					tmpl, tErr := template.New(n.Name + "." + k).Parse(v)
					if tErr != nil {
						return nil, fmt.Errorf(
							"capability node %q: arg %q: %w",
							n.Name, k, tErr)
					}
					argTmpls[k] = tmpl
				}
			}
			exec = &capabilityNodeExec{
				reg:     caps,
				flow:    cfg.Name,
				args:    argTmpls,
				timeout: parseDurationOrDefault(n.Timeout, 0),
			}
		}
		cf.execs[n.Name] = exec
	}
	return cf, nil
}

// buildAgentNodeExec wraps an instruction-only LLMAgent as a tool
// (the recipe pattern): agenttool wrapper + optional retry +
// optional timeout.
func buildAgentNodeExec(
	cfg *FlowConfig, n FlowNode,
	factory providerModelFactory, defaultModel model.Model,
) (flowNodeExec, error) {
	recipe := &RecipeConfig{
		Name:        "flow-" + cfg.Name + "-" + n.Name,
		Description: fmt.Sprintf("Flow %s node %s", cfg.Name, n.Name),
		Instruction: n.Instruction,
		Temperature: n.Temperature,
		MaxTokens:   n.MaxTokens,
		Model:       n.Model,
	}
	mdl := createRecipeModel(factory, recipe, defaultModel)
	ag := createRecipeAgent(recipe, mdl, nil)

	var step tool.Tool = agenttool.NewTool(ag,
		agenttool.WithStreamInner(false),
		agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly),
	)
	if n.Retry != nil {
		ct, ok := step.(tool.CallableTool)
		if !ok {
			return nil, fmt.Errorf("agent node %q: not callable", n.Name)
		}
		step = newRetryTool(ct, n.Retry, nil)
	}
	if d := parseDurationOrDefault(n.Timeout, 0); d > 0 {
		ct, ok := step.(tool.CallableTool)
		if !ok {
			return nil, fmt.Errorf("agent node %q: not callable", n.Name)
		}
		step = newTimeoutTool(ct, d)
	}
	callable, ok := step.(tool.CallableTool)
	if !ok {
		return nil, fmt.Errorf("agent node %q: not callable", n.Name)
	}

	prompt := strings.TrimSpace(n.Prompt)
	var promptTmpl *template.Template
	if prompt != "" {
		tmpl, err := template.New(n.Name + ".prompt").Parse(prompt)
		if err != nil {
			return nil, fmt.Errorf(
				"agent node %q: prompt template: %w", n.Name, err)
		}
		promptTmpl = tmpl
	}
	return &agentNodeExec{
		agentTool:  callable,
		promptTmpl: promptTmpl,
	}, nil
}

// flowTool makes a compiled flow invocable as a trpc CallableTool.
type flowTool struct {
	cf      *compiledFlow
	timeout time.Duration
}

func newFlowTool(cf *compiledFlow) *flowTool {
	return &flowTool{
		cf:      cf,
		timeout: parseDurationOrDefault(cf.cfg.Timeout, 0),
	}
}

// Declaration describes the flow tool for the LLM manifest.
func (f *flowTool) Declaration() *tool.Declaration {
	steps := strings.Join(f.cf.order, " → ")
	desc := f.cf.cfg.Description
	if desc == "" {
		desc = fmt.Sprintf("Declarative flow %q", f.cf.cfg.Name)
	}
	if steps != "" {
		desc += "\nFlow steps: " + steps
	}
	return &tool.Declaration{
		Name:        "flow-" + f.cf.cfg.Name,
		Description: desc,
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Flow input; exposed to node templates as {{.input}}",
			Properties: map[string]*tool.Schema{
				"request": {
					Type:        "string",
					Description: "The input for the flow (text or JSON string)",
				},
			},
		},
	}
}

// Call runs the flow. The invocation arguments become the flow
// input: a lone {"request": "..."} string is passed as-is, anything
// else is passed as a map (navigable via {{.input.key}}).
func (f *flowTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var input any = ""
	if len(jsonArgs) > 0 {
		var args map[string]any
		if err := json.Unmarshal(jsonArgs, &args); err == nil {
			if req, ok := args["request"].(string); ok && len(args) == 1 {
				input = req
			} else {
				input = args
			}
		} else {
			// Non-object payloads (array/string) pass through.
			var v any
			if json.Unmarshal(jsonArgs, &v) == nil {
				input = v
			}
		}
	}

	callCtx := ctx
	var cancel context.CancelFunc
	if f.timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, f.timeout)
		defer cancel()
	}
	return runFlow(callCtx, f.cf, input)
}

// Tools returns the flow tools.
func (ts *FlowToolSet) Tools(_ context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *FlowToolSet) Name() string { return "flow_tools" }

// Close releases the hot-reload watcher.
func (ts *FlowToolSet) Close() error {
	if ts.reloader != nil {
		return ts.reloader.Close()
	}
	return nil
}

// SetReloadCallback registers fn to run after every successful
// Reload with the fresh tool slice (capability-registry sync).
func (ts *FlowToolSet) SetReloadCallback(fn func(tools []tool.Tool)) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.onReload = fn
}

// Reload recompiles all flows from disk. Returns false when the
// reload produces no usable flow (the previous set stays active).
func (ts *FlowToolSet) Reload() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	util.Logger.Info("flow: reload triggered")

	flowDir := resolveFlowDir(ts.agentCfg.FlowDir)
	flows := loadFlowDir(flowDir)
	if len(flows) == 0 {
		util.Logger.Warn("flow: reload aborted, no flows found")
		return false
	}

	var defaultModel model.Model
	if ts.factory != nil {
		if mdl, err := ts.factory.CreateDefaultModel(); err == nil {
			defaultModel = mdl
		}
	}

	tools := make([]tool.Tool, 0, len(flows))
	built := make(map[string]*compiledFlow, len(flows))
	for name, cfg := range flows {
		cf, err := compileFlow(cfg, ts.factory, defaultModel, ts.caps)
		if err != nil {
			util.Logger.Warn("flow: reload skip",
				slog.String("flow", name),
				slog.String("error", err.Error()))
			continue
		}
		built[name] = cf
		tools = append(tools, newFlowTool(cf))
	}
	if len(tools) == 0 {
		util.Logger.Warn("flow: reload aborted, no flows compiled")
		return false
	}
	sortFlowTools(tools)

	ts.tools = tools
	ts.flows = built

	util.Logger.Info("flow: reload completed",
		slog.Int("tools", len(tools)))
	if ts.onReload != nil {
		ts.onReload(tools)
	}
	return true
}

// flowNamespace is the capability-bus namespace for flows.
const flowNamespace = "flow"

// SyncFlowCapabilities re-syncs the "flow.*" namespace of the
// registry with the given flow tool slice (reload-safe, mirrors
// SyncRecipeCapabilities).
func SyncFlowCapabilities(reg *capability.Registry, tools []tool.Tool) {
	reg.UnregisterNamespace(flowNamespace)
	registered := 0
	for _, t := range tools {
		if t == nil {
			continue
		}
		decl := t.Declaration()
		if decl == nil || decl.Name == "" {
			continue
		}
		addr := flowNamespace + "." +
			strings.TrimPrefix(decl.Name, "flow-")
		c, err := capability.FromToolMeta(
			addr, capability.SourceFlow, t, capability.ToolMeta{},
		)
		if err != nil {
			util.Logger.Warn("capability: flow adapter failed",
				"address", addr, "error", err.Error())
			continue
		}
		if err := reg.Register(c); err != nil {
			util.Logger.Warn("capability: flow registration skipped",
				"address", addr, "error", err.Error())
			continue
		}
		registered++
	}
	util.Logger.Info("capability: flow namespace synced",
		"capabilities", registered)
}

// sortFlowTools orders flow tools by declaration name so the tool
// manifest is deterministic.
func sortFlowTools(tools []tool.Tool) {
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Declaration().Name < tools[j].Declaration().Name
	})
}
