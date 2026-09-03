// taskgraph.go: dependency-graph scheduling for clone tasks.
//
// Pages, assets, and their anti-bot retries form a task graph: a
// retry depends on its backoff window, a re-dispatched attempt
// depends on the retry decision, and so on. taskGraph gives that
// graph topological-release semantics — a node's work runs only once
// every dependency has resolved — while leaving resource-level
// ordering (which render a browser worker picks up first) to the
// renderkit priority dispatcher.

package clone

import (
	"fmt"
	"sync"
)

// graphNode is one task in the dependency graph.
type graphNode struct {
	deps []string
	// fn non-nil marks an executable node: fn runs (in its own
	// goroutine) once all deps resolve, and its returned error
	// becomes the node's own resolution for dependents. fn nil marks
	// a declared node: the caller resolves it via Resolve.
	fn func(depErr error) error
	// unresolved counts deps that have not resolved yet.
	unresolved int
	// dependents lists nodes waiting on this one.
	dependents []string
}

// taskGraph schedules tasks as a keyed dependency graph with
// topological release. Nodes come in two flavours:
//
//   - Declared nodes (Declare/Resolve): resolved externally, e.g. by
//     a backoff timer or an upstream completion.
//   - Submitted nodes (Submit): their fn runs once every dependency
//     has resolved. Dependencies that resolved with an error do NOT
//     skip fn — fn receives the first such error as depErr and
//     decides for itself (typically cleanup, then bail), so callers
//     never lose track of their own accounting.
//
// Submit's returned error resolves the node for its own dependents,
// giving transitive failure propagation. Duplicate keys and cycles
// (among unresolved nodes) are rejected at edge-creation time.
type taskGraph struct {
	mu       sync.Mutex
	nodes    map[string]*graphNode
	resolved map[string]error
}

func newTaskGraph() *taskGraph {
	return &taskGraph{
		nodes:    make(map[string]*graphNode),
		resolved: make(map[string]error),
	}
}

// Declare registers a node the caller resolves later with Resolve.
func (g *taskGraph) Declare(key string, deps ...string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addLocked(key, nil, deps)
}

// Submit registers fn to run once every dependency has resolved.
// Dependencies that were declared but never resolved keep fn waiting;
// dependencies that resolved with an error run fn with that error.
func (g *taskGraph) Submit(key string, fn func(depErr error) error, deps ...string) error {
	if fn == nil {
		return fmt.Errorf("taskgraph: nil fn for node %q", key)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addLocked(key, fn, deps)
}

// addLocked wires a node into the graph; the caller holds g.mu.
func (g *taskGraph) addLocked(key string, fn func(depErr error) error, deps []string) error {
	if _, dup := g.nodes[key]; dup {
		return fmt.Errorf("taskgraph: duplicate node %q", key)
	}
	if err := g.checkCycleLocked(key, deps); err != nil {
		return err
	}

	n := &graphNode{deps: deps, fn: fn}
	for _, dep := range deps {
		if _, done := g.resolved[dep]; done {
			continue // already satisfied (or failed — surfaced via depErr)
		}
		n.unresolved++
		if d, ok := g.nodes[dep]; ok {
			d.dependents = append(d.dependents, key)
		}
		// A dep key that is neither resolved nor declared simply
		// never satisfies; the contract is that deps are declared
		// alongside or before their dependents.
	}
	g.nodes[key] = n

	if n.unresolved == 0 && n.fn != nil {
		go g.run(key, n)
	}
	return nil
}

// Resolve completes a declared node with err (nil = success),
// releasing dependents whose dependencies are now all resolved.
func (g *taskGraph) Resolve(key string, err error) error {
	g.mu.Lock()
	n, ok := g.nodes[key]
	if !ok {
		g.mu.Unlock()
		return fmt.Errorf("taskgraph: unknown node %q", key)
	}
	if n.fn != nil {
		g.mu.Unlock()
		return fmt.Errorf("taskgraph: node %q is submitted, not declared", key)
	}
	if _, done := g.resolved[key]; done {
		g.mu.Unlock()
		return fmt.Errorf("taskgraph: node %q already resolved", key)
	}
	g.finishLocked(key, err)
	g.mu.Unlock()
	return nil
}

// checkCycleLocked rejects edges that would let key reach itself
// through unresolved nodes (resolved nodes are terminal).
func (g *taskGraph) checkCycleLocked(key string, deps []string) error {
	const visiting = 1
	const visited = 2
	state := map[string]int{key: visiting}
	var dfs func(k string) error
	dfs = func(k string) error {
		state[k] = visiting
		node := g.nodes[k]
		if node == nil {
			return nil
		}
		for _, dep := range node.deps {
			if _, done := g.resolved[dep]; done {
				continue
			}
			switch state[dep] {
			case visiting:
				return fmt.Errorf("taskgraph: cycle via %q", dep)
			case 0:
				if err := dfs(dep); err != nil {
					return err
				}
			}
		}
		state[k] = visited
		return nil
	}
	for _, dep := range deps {
		if _, done := g.resolved[dep]; done {
			continue
		}
		switch state[dep] {
		case visiting:
			return fmt.Errorf("taskgraph: cycle via %q", dep)
		case 0:
			if err := dfs(dep); err != nil {
				return err
			}
		}
	}
	return nil
}

// run executes a submitted node whose dependencies all resolved. It
// runs without holding g.mu (fn may touch the graph) and resolves the
// node with fn's error, cascading to dependents.
func (g *taskGraph) run(key string, n *graphNode) {
	depErr := g.depErr(key)
	err := n.fn(depErr)

	g.mu.Lock()
	g.finishLocked(key, err)
	g.mu.Unlock()
}

// depErr returns the first (in dependency order) non-nil resolution
// among key's dependencies.
func (g *taskGraph) depErr(key string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	node := g.nodes[key]
	if node == nil {
		return fmt.Errorf("taskgraph: unknown node %q", key)
	}
	for _, dep := range node.deps {
		if err, done := g.resolved[dep]; done && err != nil {
			return err
		}
	}
	return nil
}

// finishLocked records key's resolution and releases dependents whose
// dependency count drops to zero; the caller holds g.mu.
func (g *taskGraph) finishLocked(key string, err error) {
	g.resolved[key] = err
	n := g.nodes[key]
	for _, dep := range n.dependents {
		m := g.nodes[dep]
		if m == nil {
			continue
		}
		m.unresolved--
		if m.unresolved == 0 && m.fn != nil {
			go g.run(dep, m)
		}
	}
}
