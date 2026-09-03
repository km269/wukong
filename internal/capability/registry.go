// registry.go — the in-process capability registry.
package capability

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ErrDuplicate is returned by Registry.Register when a capability is
// registered under an address that is already taken.
var ErrDuplicate = errors.New("capability address already registered")

// searchDefaultTopK is the result cap used by Search when topK <= 0.
const searchDefaultTopK = 10

// Registry is the in-process capability registry. It is safe for
// concurrent use.
type Registry struct {
	mu   sync.RWMutex
	caps map[string]Capability
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{caps: make(map[string]Capability)}
}

// Register adds c to the registry. It rejects nil capabilities,
// empty addresses, and addresses without a namespace ("<ns>.<name>"
// at minimum). Registering an already-taken address returns an error
// wrapping ErrDuplicate.
func (r *Registry) Register(c Capability) error {
	if c == nil {
		return errors.New("capability is nil")
	}
	addr := c.Address()
	if addr == "" {
		return errors.New("capability address is empty")
	}
	if Namespace(addr) == addr {
		return fmt.Errorf(
			"capability address %q lacks a namespace (<ns>.<name>)",
			addr,
		)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.caps[addr]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicate, addr)
	}
	r.caps[addr] = c
	return nil
}

// Unregister removes the capability registered under addr. It
// reports whether an entry was removed. This is the incremental
// update primitive for sources that rebuild their tool sets at
// runtime — e.g. recipe hot-reload re-syncs the "recipe.*"
// namespace by unregistering the stale entries and re-registering.
func (r *Registry) Unregister(addr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.caps[addr]; !ok {
		return false
	}
	delete(r.caps, addr)
	return true
}

// UnregisterNamespace removes every capability whose address starts
// with "<namespace>." and returns the removed addresses in sorted
// order. See Unregister for the intended use.
func (r *Registry) UnregisterNamespace(namespace string) []string {
	if namespace == "" {
		return nil
	}
	prefix := namespace + "."
	r.mu.Lock()
	removed := make([]string, 0, 4)
	for addr := range r.caps {
		if strings.HasPrefix(addr, prefix) {
			removed = append(removed, addr)
		}
	}
	for _, addr := range removed {
		delete(r.caps, addr)
	}
	r.mu.Unlock()
	sort.Strings(removed)
	return removed
}

// Resolve looks up a capability by exact address.
func (r *Registry) Resolve(addr string) (Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.caps[addr]
	return c, ok
}

// ResolveByName looks up a capability by its LLM-visible tool name
// (Descriptor.Name), which is how inbound tool calls are addressed.
// When several capabilities share a name, the one with the
// lexicographically smallest address wins (deterministic; matches
// the AsTools manifest order).
func (r *Registry) ResolveByName(name string) (Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if name == "" {
		return nil, false
	}
	bestAddr := ""
	var best Capability
	for addr, c := range r.caps {
		if c.Descriptor().Name != name {
			continue
		}
		if best == nil || addr < bestAddr {
			best, bestAddr = c, addr
		}
	}
	return best, best != nil
}

// Len returns the number of registered capabilities.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.caps)
}

// List returns descriptors sorted by address. When namespace is
// non-empty, only capabilities whose address starts with
// "<namespace>." are returned; a deeper prefix such as "tools.web"
// works as a filter too.
func (r *Registry) List(namespace string) []Descriptor {
	r.mu.RLock()
	descs := make([]Descriptor, 0, len(r.caps))
	for addr, c := range r.caps {
		if namespace != "" && !strings.HasPrefix(addr, namespace+".") {
			continue
		}
		descs = append(descs, c.Descriptor())
	}
	r.mu.RUnlock()
	sort.Slice(descs, func(i, j int) bool {
		return descs[i].Address < descs[j].Address
	})
	return descs
}

// Search scores registered descriptors against query and returns up
// to topK (default 10 when <= 0) best matches. An empty query
// returns nil. Ranking: address prefix > address contains > name
// contains > description contains; ties break by address for
// determinism. This is the seam the ToolSearch plugin will reuse in
// Phase B instead of ranking raw LLM tool declarations.
func (r *Registry) Search(query string, topK int) []Descriptor {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	if topK <= 0 {
		topK = searchDefaultTopK
	}
	const (
		scoreAddrPrefix   = 40
		scoreAddrContains = 30
		scoreNameContains = 20
		scoreDescContains = 10
	)
	type scored struct {
		desc  Descriptor
		score int
	}
	r.mu.RLock()
	matches := make([]scored, 0, len(r.caps))
	for addr, c := range r.caps {
		d := c.Descriptor()
		var s int
		switch {
		case strings.HasPrefix(addr, q):
			s = scoreAddrPrefix
		case strings.Contains(addr, q):
			s = scoreAddrContains
		case strings.Contains(strings.ToLower(d.Name), q):
			s = scoreNameContains
		case strings.Contains(strings.ToLower(d.Description), q):
			s = scoreDescContains
		default:
			continue
		}
		matches = append(matches, scored{desc: d, score: s})
	}
	r.mu.RUnlock()
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].desc.Address < matches[j].desc.Address
	})
	if len(matches) > topK {
		matches = matches[:topK]
	}
	out := make([]Descriptor, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.desc)
	}
	return out
}
