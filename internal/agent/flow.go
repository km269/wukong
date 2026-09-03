// Package agent — flow.go
//
// Declarative flow DSL (roadmap P0-2): user-writable YAML flow
// definitions that compose agent (LLM step) and capability (bus
// address) nodes into a directed graph with conditional edges.
//
// A flow file lives in .wukong/flows/*.yaml and has the shape:
//
//	name: translate-summarize
//	description: Translate a text, then summarize the translation.
//	nodes:
//	  - name: translate
//	    type: agent
//	    instruction: Translate the text to {{.target_lang}}.
//	    prompt: "{{.input}}"
//	    temperature: 0.3
//	    max_tokens: 2048
//	    timeout: 60s
//	  - name: summarize
//	    type: agent
//	    instruction: Summarize text in 3 bullet points.
//	    prompt: "{{.translate}}"
//	  - name: save
//	    type: capability
//	    address: tools.developer.developer_file_write
//	    args:
//	      path: "out/summary.md"
//	      content: "{{.summarize}}"
//	edges:                          # optional; default = linear order
//	  - from: translate
//	    to: summarize
//	  - from: summarize
//	    to: save
//	  - from: classify
//	    to: save
//	    when: '{{if eq .classify "yes"}}go{{end}}'   # optional gate
//
// Execution semantics (flow_exec.go): nodes run in topological
// order from the entry node; each node's output is stored in the
// template context under its node name ("input" holds the flow
// invocation input). A node runs only when every incoming edge is
// active — its source executed and its optional "when" template
// rendered truthy. The flow result is the output of the last
// executed node.
//
// Flows are registered as callable tools (flow-<name>) and as
// "flow.<name>" capabilities, hot-reloaded via the same fsnotify
// watcher recipes use. The 10 hardcoded workflow modes remain as
// presets; flows are the user-declarative layer on top.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/km269/wukong/internal/util"
	"gopkg.in/yaml.v3"
	"log/slog"
)

// Flow node types.
const (
	FlowNodeAgent      = "agent"      // LLM step (instruction + prompt template)
	FlowNodeCapability = "capability" // capability-bus invocation
)

// FlowConfig is the YAML schema for a declarative flow definition.
type FlowConfig struct {
	// Name is the unique identifier; the tool name is flow-<name>
	// and the capability address is flow.<name>.
	Name string `yaml:"name"`
	// Description is shown to the main agent when deciding whether
	// to call this flow.
	Description string `yaml:"description"`
	// Nodes are the flow steps. Declaration order is the default
	// execution order when no edges are declared.
	Nodes []FlowNode `yaml:"nodes"`
	// Edges wire the graph. Optional; without edges the flow runs
	// linearly in declaration order.
	Edges []FlowEdge `yaml:"edges"`
	// Timeout caps the whole flow execution ("30s", "5m"...).
	// Zero/unset means no flow-level cap (per-node timeouts still
	// apply).
	Timeout string `yaml:"timeout"`
}

// FlowNode is a single step of a flow.
type FlowNode struct {
	// Name uniquely identifies the node inside the flow; its
	// output is exposed to templates as {{.<name>}}. Dots are not
	// allowed (they are reserved for capability addresses).
	Name string `yaml:"name"`
	// Type selects the executor: "agent" or "capability".
	Type string `yaml:"type"`

	// --- agent node fields ---
	// Instruction is the system prompt for the LLM step.
	Instruction string `yaml:"instruction"`
	// Prompt is the user-message template rendered against the
	// flow context. Defaults to "{{.input}}".
	Prompt string `yaml:"prompt"`
	// Model optionally overrides the provider model for this node.
	Model string `yaml:"model"`
	// Temperature / MaxTokens mirror the recipe fields.
	Temperature float64 `yaml:"temperature"`
	MaxTokens   int     `yaml:"max_tokens"`

	// --- shared fields ---
	// Timeout caps this node ("30s"). Unset = no per-node cap.
	Timeout string `yaml:"timeout"`
	// Retry wraps the node with exponential backoff (same shape as
	// recipe retry).
	Retry *RecipeRetryConfig `yaml:"retry"`

	// --- capability node fields ---
	// Address is the capability-bus address to invoke
	// (e.g. tools.developer.developer_file_write).
	Address string `yaml:"address"`
	// Args are the invocation arguments; every string value is a
	// Go template rendered against the flow context before the
	// call. Values are passed as strings (v1).
	Args map[string]string `yaml:"args"`
}

// FlowEdge wires From → To with an optional condition.
type FlowEdge struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
	// When is an optional Go template rendered against the flow
	// context after From executes. The edge is active only when the
	// rendered text is not one of: "", "false", "0", "no", "none",
	// "skip" (case-insensitive).
	When string `yaml:"when"`
}

// loadFlowDir scans dir for .yaml flow files and returns the valid,
// validated flows keyed by name. Invalid files are warned and
// skipped. Missing directory returns an empty map (non-fatal).
func loadFlowDir(dir string) map[string]*FlowConfig {
	flows := make(map[string]*FlowConfig)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			util.Logger.Warn("flow: cannot read flow directory",
				slog.String("dir", dir),
				slog.String("error", err.Error()))
		}
		return flows
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext != ".yaml" && ext != ".yml" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names) // deterministic load/log order

	for _, name := range names {
		path := filepath.Join(dir, name)
		cfg, err := loadFlowFile(path)
		if err != nil {
			util.Logger.Warn("flow: skipping invalid flow file",
				slog.String("path", path),
				slog.String("error", err.Error()))
			continue
		}
		if cfg.Name == "" {
			util.Logger.Warn("flow: skipping unnamed flow",
				slog.String("path", path))
			continue
		}
		if _, err := validateFlowConfig(cfg); err != nil {
			util.Logger.Warn("flow: skipping invalid flow",
				slog.String("name", cfg.Name),
				slog.String("path", path),
				slog.String("error", err.Error()))
			continue
		}
		if _, dup := flows[cfg.Name]; dup {
			util.Logger.Warn("flow: duplicate flow name, first wins",
				slog.String("name", cfg.Name))
			continue
		}
		flows[cfg.Name] = cfg
		util.Logger.Info("flow: loaded flow",
			slog.String("name", cfg.Name),
			slog.Int("nodes", len(cfg.Nodes)))
	}
	return flows
}

// loadFlowFile reads and validates a single flow YAML file.
func loadFlowFile(path string) (*FlowConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	var flow FlowConfig
	if err := yaml.Unmarshal(data, &flow); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	return &flow, nil
}

// validateFlowConfig checks structural integrity: unique node
// names, known node types, per-type required fields, edge
// references, and acyclicity (Kahn). Returns the topological
// execution order.
func validateFlowConfig(f *FlowConfig) ([]string, error) {
	if len(f.Nodes) == 0 {
		return nil, fmt.Errorf("flow has no nodes")
	}
	names := make(map[string]bool, len(f.Nodes))
	for _, n := range f.Nodes {
		if n.Name == "" {
			return nil, fmt.Errorf("node with empty name")
		}
		if strings.Contains(n.Name, ".") {
			return nil, fmt.Errorf(
				"node %q: dots are not allowed in node names", n.Name)
		}
		if names[n.Name] {
			return nil, fmt.Errorf("duplicate node name %q", n.Name)
		}
		names[n.Name] = true
		switch n.Type {
		case FlowNodeAgent:
			if strings.TrimSpace(n.Instruction) == "" {
				return nil, fmt.Errorf(
					"agent node %q: instruction is required", n.Name)
			}
		case FlowNodeCapability:
			if strings.TrimSpace(n.Address) == "" {
				return nil, fmt.Errorf(
					"capability node %q: address is required", n.Name)
			}
		default:
			return nil, fmt.Errorf(
				"node %q: unknown type %q (use agent or capability)",
				n.Name, n.Type)
		}
	}

	indegree := make(map[string]int, len(f.Nodes))
	adjacency := make(map[string][]string, len(f.Nodes))
	for _, n := range f.Nodes {
		indegree[n.Name] = 0
	}
	seen := make(map[string]bool, len(f.Edges))
	for i, e := range f.Edges {
		if !names[e.From] {
			return nil, fmt.Errorf(
				"edge %d: unknown source node %q", i, e.From)
		}
		if !names[e.To] {
			return nil, fmt.Errorf(
				"edge %d: unknown target node %q", i, e.To)
		}
		if e.From == e.To {
			return nil, fmt.Errorf(
				"edge %d: self-loop on %q", i, e.From)
		}
		key := e.From + "→" + e.To
		if seen[key] {
			return nil, fmt.Errorf("duplicate edge %s", key)
		}
		seen[key] = true
		indegree[e.To]++
		adjacency[e.From] = append(adjacency[e.From], e.To)
	}

	// Kahn topological sort (also detects cycles).
	order := make([]string, 0, len(f.Nodes))
	queue := make([]string, 0, len(f.Nodes))
	for _, n := range f.Nodes { // declaration order for determinism
		if indegree[n.Name] == 0 {
			queue = append(queue, n.Name)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		order = append(order, cur)
		for _, next := range adjacency[cur] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(order) != len(f.Nodes) {
		return nil, fmt.Errorf(
			"flow graph contains a cycle (%d/%d nodes reachable)",
			len(order), len(f.Nodes))
	}
	return order, nil
}

// resolveFlowDir resolves the flow directory path to an absolute
// path, expanding ~ and falling back to .wukong/flows relative to
// the CWD if the configured path doesn't exist.
func resolveFlowDir(flowDir string) string {
	if flowDir == "" {
		flowDir = filepath.Join(".wukong", "flows")
	}
	if strings.HasPrefix(flowDir, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			flowDir = filepath.Join(home, flowDir[1:])
		}
	}
	if abs, err := filepath.Abs(flowDir); err == nil {
		flowDir = abs
	}
	if _, err := os.Stat(flowDir); err != nil {
		// Configured path missing → try the conventional location.
		if fallback := filepath.Join(".wukong", "flows"); flowDir != mustAbs(fallback) {
			if _, fbErr := os.Stat(fallback); fbErr == nil {
				return mustAbs(fallback)
			}
		}
	}
	return flowDir
}

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
