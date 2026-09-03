// flow_exec.go — the flow execution engine (roadmap P0-2).
//
// Nodes execute in topological order. A node runs only when every
// incoming edge is active: its source node executed and the edge's
// optional "when" template rendered truthy. Each node's output is
// stored in the shared template context under its node name; the
// flow input is exposed as {{.input}}.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/km269/wukong/internal/capability"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// compiledFlow is a validated flow with its node executors built
// and the execution order resolved. Safe for concurrent invocations
// (node executors are immutable; per-run state lives in flowRun).
type compiledFlow struct {
	cfg   *FlowConfig
	order []string // topological execution order
	execs map[string]flowNodeExec
	// whenTemplates caches the compiled "when" edge templates,
	// keyed "from→to".
	whenTemplates map[string]*template.Template
}

// nodeByName returns the node config by name, or nil.
func (cf *compiledFlow) nodeByName(name string) *FlowNode {
	for i := range cf.cfg.Nodes {
		if cf.cfg.Nodes[i].Name == name {
			return &cf.cfg.Nodes[i]
		}
	}
	return nil
}

// flowNodeExec executes one node and stores its output in the run
// context. Implementations are immutable across invocations.
type flowNodeExec interface {
	execute(ctx context.Context, run *flowRun, node FlowNode) error
}

// flowRun is the per-invocation execution state.
type flowRun struct {
	mu       sync.Mutex
	outputs  map[string]any // node name → output (input included)
	executed map[string]bool
	skipped  map[string]bool
	// orderRef is the compiled execution order, set by runFlow so
	// lastOutput walks nodes deterministically.
	orderRef []string
}

func newFlowRun(input any) *flowRun {
	return &flowRun{
		outputs:  map[string]any{"input": input},
		executed: map[string]bool{},
		skipped:  map[string]bool{},
	}
}

// contextSnapshot returns a copy of the outputs map for template
// rendering (templates must not observe concurrent writes).
func (r *flowRun) contextSnapshot() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := make(map[string]any, len(r.outputs))
	for k, v := range r.outputs {
		snap[k] = v
	}
	return snap
}

func (r *flowRun) setOutput(name string, out any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outputs[name] = out
	r.executed[name] = true
}

// lastOutput returns the most recently executed node's output.
func (r *flowRun) lastOutput() any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var last any
	for _, n := range r.orderRef {
		if r.executed[n] {
			last = r.outputs[n]
		}
	}
	return last
}

// renderTemplate renders tmpl against a snapshot of the run
// context.
func (r *flowRun) renderTemplate(tmpl *template.Template) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, r.contextSnapshot()); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// truthy classifies a rendered "when" value. Rendered text outside
// the false-y set activates the edge.
func truthy(rendered string) bool {
	switch strings.ToLower(strings.TrimSpace(rendered)) {
	case "", "false", "0", "no", "none", "skip":
		return false
	default:
		return true
	}
}

// decodeJSONish normalizes raw tool results into template-friendly
// values: JSON payloads become maps/slices/scalars, everything else
// passes through.
func decodeJSONish(v any) any {
	switch t := v.(type) {
	case json.RawMessage:
		var out any
		if err := json.Unmarshal(t, &out); err == nil {
			return out
		}
		return string(t)
	case []byte:
		var out any
		if err := json.Unmarshal(t, &out); err == nil {
			return out
		}
		return string(t)
	case string:
		// Capability results arrive as JSON text; decode so
		// templates can navigate {{.node.field}}.
		trimmed := strings.TrimSpace(t)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var out any
			if err := json.Unmarshal([]byte(trimmed), &out); err == nil {
				return out
			}
		}
		return t
	default:
		return v
	}
}

// runFlow executes the compiled flow with the given input and
// returns the output of the last executed node.
func runFlow(
	ctx context.Context, cf *compiledFlow, input any,
) (any, error) {
	run := newFlowRun(input)
	run.orderRef = cf.order

	for _, name := range cf.order {
		node := cf.nodeByName(name)
		if node == nil {
			return nil, fmt.Errorf("flow %q: node %q vanished", cf.cfg.Name, name)
		}

		// Gate on incoming edges: every edge whose source did not
		// execute (skipped or never reached) deactivates this node.
		active := true
		for _, e := range cf.cfg.Edges {
			if e.To != name {
				continue
			}
			if !run.executed[e.From] {
				active = false
				break
			}
			if e.When != "" {
				tmpl := cf.whenTemplates[e.From+"→"+e.To]
				rendered, err := run.renderTemplate(tmpl)
				if err != nil {
					return nil, fmt.Errorf(
						"flow %q: edge %s→%s when-template: %w",
						cf.cfg.Name, e.From, e.To, err)
				}
				if !truthy(rendered) {
					active = false
					break
				}
			}
		}
		if !active {
			run.skipped[name] = true
			continue
		}

		exec := cf.execs[name]
		if exec == nil {
			run.skipped[name] = true
			continue
		}
		if err := exec.execute(ctx, run, *node); err != nil {
			return nil, fmt.Errorf(
				"flow %q: node %q: %w", cf.cfg.Name, name, err)
		}
	}

	last := run.lastOutput()
	if last == nil {
		return nil, fmt.Errorf(
			"flow %q: no node executed (entry blocked by conditions)",
			cf.cfg.Name)
	}
	return last, nil
}

// agentNodeExec runs an LLM step through a prebuilt agenttool
// wrapper (isolated runner outside the agent loop; parent-invocation
// aware inside it).
type agentNodeExec struct {
	agentTool  tool.CallableTool
	promptTmpl *template.Template
}

func (e *agentNodeExec) execute(
	ctx context.Context, run *flowRun, node FlowNode,
) error {
	rendered := "{{.input}}"
	if e.promptTmpl != nil {
		out, err := run.renderTemplate(e.promptTmpl)
		if err != nil {
			return fmt.Errorf("prompt template: %w", err)
		}
		rendered = out
	}
	res, err := e.agentTool.Call(ctx, []byte(`{"request":`+
		mustJSONString(rendered)+`}`))
	if err != nil {
		return err
	}
	run.setOutput(node.Name, decodeJSONish(res))
	return nil
}

// capabilityNodeExec invokes a capability-bus address with
// template-rendered string arguments.
type capabilityNodeExec struct {
	reg     *capability.Registry
	flow    string
	args    map[string]*template.Template
	timeout time.Duration
}

func (e *capabilityNodeExec) execute(
	ctx context.Context, run *flowRun, node FlowNode,
) error {
	cap, ok := e.reg.Resolve(node.Address)
	if !ok {
		return fmt.Errorf(
			"capability %q not registered (is the extension enabled?)",
			node.Address)
	}
	args := make(map[string]any, len(e.args))
	for key, tmpl := range e.args {
		rendered, err := run.renderTemplate(tmpl)
		if err != nil {
			return fmt.Errorf("arg %q template: %w", key, err)
		}
		args[key] = rendered
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("marshal args: %w", err)
	}

	callCtx := ctx
	var cancel context.CancelFunc
	if e.timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	res, err := cap.Invoke(callCtx, raw)
	if err != nil {
		return err
	}
	run.setOutput(node.Name, decodeJSONish(res))
	return nil
}

// mustJSONString encodes s as a JSON string literal; template
// output is always valid UTF-8 text, so marshal cannot fail
// meaningfully — on error, a quoted empty string is returned.
func mustJSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		util.Logger.Warn("flow: json-encode prompt failed",
			slog.String("error", err.Error()))
		return `""`
	}
	return string(b)
}
