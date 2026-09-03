// adapter.go — bridges between the capability bus and the
// trpc-agent-go tool interfaces.
package capability

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Adapter is the generic Capability implementation: a fixed
// Descriptor plus an InvokeFunc. It lets any package (including
// internal/agent and internal/extension) expose logic on the bus
// without depending on this package's concrete adapters — e.g. the
// recipe sub-agent adapter (Phase B) lives in internal/agent and
// builds on NewAdapter to avoid an import cycle.
type Adapter struct {
	desc   Descriptor
	invoke InvokeFunc
}

// NewAdapter creates a Capability from a descriptor and an invoke
// function. The invoke function may be nil for declaration-only
// capabilities; Invoke then returns an error.
func NewAdapter(desc Descriptor, invoke InvokeFunc) *Adapter {
	return &Adapter{desc: desc, invoke: invoke}
}

// Address implements Capability.
func (a *Adapter) Address() string { return a.desc.Address }

// Descriptor implements Capability.
func (a *Adapter) Descriptor() Descriptor { return a.desc }

// Invoke implements Capability.
func (a *Adapter) Invoke(
	ctx context.Context, args json.RawMessage,
) (json.RawMessage, error) {
	if a.invoke == nil {
		return nil, fmt.Errorf(
			"capability %s: no invoke function", a.desc.Address)
	}
	return a.invoke(ctx, args)
}

// ToolMeta carries the per-tool permission metadata a registrar can
// declare on top of what the framework's tool interface exposes.
// It closes the gap that tool.Declaration has no security fields.
type ToolMeta struct {
	// Scopes declares permission domains, e.g. "shell", "fs.write",
	// "network". Consulted by the Guard seam (see
	// internal/agent/loop.go commandToolNeedsValidation).
	Scopes []string

	// ReadOnly marks a capability as side-effect free. When false
	// (the default), FromToolMeta keeps the conservative
	// Mutating=true.
	ReadOnly bool
}

// FromTool wraps a trpc-agent-go tool.Tool as a bus Capability under
// the given address (empty addr falls back to the declaration name),
// applying the conservative default metadata (Mutating=true, no
// Scopes). See FromToolMeta for declared metadata.
func FromTool(addr, source string, t tool.Tool) (Capability, error) {
	return FromToolMeta(addr, source, t, ToolMeta{})
}

// FromToolMeta is FromTool with explicitly declared permission
// metadata merged into the descriptor. Registrars that own their
// tools (built-in extensions) should declare scopes here so the
// Guard can decide from the descriptor instead of name heuristics.
func FromToolMeta(
	addr, source string, t tool.Tool, meta ToolMeta,
) (Capability, error) {
	c, err := fromTool(addr, source, t)
	if err != nil {
		return nil, err
	}
	a := c.(*Adapter)
	a.desc.Scopes = meta.Scopes
	a.desc.Mutating = !meta.ReadOnly
	return a, nil
}

// fromTool is the descriptor/adapter construction shared by
// FromTool and FromToolMeta.
func fromTool(addr, source string, t tool.Tool) (Capability, error) {
	if t == nil {
		return nil, fmt.Errorf("capability %s: tool is nil", addr)
	}
	decl := t.Declaration()
	if decl == nil {
		return nil, fmt.Errorf(
			"capability %s: tool declaration is nil", addr)
	}
	if addr == "" {
		addr = decl.Name
	}
	if decl.Name == "" {
		return nil, fmt.Errorf(
			"capability %s: tool declaration has no name", addr)
	}
	desc := Descriptor{
		Address:     addr,
		Name:        decl.Name,
		Description: decl.Description,
		Mutating:    true,
		Source:      source,
	}
	if decl.InputSchema != nil {
		b, err := json.Marshal(decl.InputSchema)
		if err != nil {
			return nil, fmt.Errorf(
				"capability %s: marshal input schema: %w", addr, err)
		}
		desc.Parameters = b
	}

	ct, callable := t.(tool.CallableTool)
	invoke := func(
		ctx context.Context, args json.RawMessage,
	) (json.RawMessage, error) {
		if !callable {
			return nil, fmt.Errorf(
				"capability %s: tool %q does not support Call",
				addr, decl.Name)
		}
		res, err := ct.Call(ctx, args)
		if err != nil {
			return nil, err
		}
		return normalizeResult(res)
	}
	return NewAdapter(desc, invoke), nil
}

// normalizeResult converts an arbitrary tool result into raw JSON.
// []byte / json.RawMessage pass through untouched; anything that
// fails to marshal degrades to its %v string form so callers always
// receive valid JSON text.
func normalizeResult(res any) (json.RawMessage, error) {
	switch v := res.(type) {
	case nil:
		return json.RawMessage("null"), nil
	case json.RawMessage:
		return v, nil
	case []byte:
		return json.RawMessage(v), nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return json.Marshal(fmt.Sprintf("%v", v))
		}
		return b, nil
	}
}

// capabilityTool adapts a Capability back into the trpc-agent-go
// tool interfaces so a registry can feed the LLM tool manifest
// exactly like the hand-aggregated toolsets do today.
type capabilityTool struct {
	cap Capability
}

// Declaration rebuilds a tool.Declaration from the descriptor. The
// InputSchema round-trips through the same tool.Schema type it was
// marshalled from, so it is equivalent to the source tool's
// declaration (verified byte-for-byte by the adapter tests).
func (t capabilityTool) Declaration() *tool.Declaration {
	desc := t.cap.Descriptor()
	decl := &tool.Declaration{
		Name:        desc.Name,
		Description: desc.Description,
	}
	if len(desc.Parameters) > 0 {
		var s tool.Schema
		if err := json.Unmarshal(desc.Parameters, &s); err == nil {
			decl.InputSchema = &s
		}
	}
	return decl
}

// Call implements tool.CallableTool by delegating to the capability.
// The JSON result is decoded back to any to match the framework's
// Call contract; payloads that fail to decode degrade to their raw
// string form.
func (t capabilityTool) Call(
	ctx context.Context, jsonArgs []byte,
) (any, error) {
	raw, err := t.cap.Invoke(ctx, json.RawMessage(jsonArgs))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw), nil
	}
	return v, nil
}

// ToolFromCapability exposes c as a trpc-agent-go CallableTool.
func ToolFromCapability(c Capability) tool.CallableTool {
	return capabilityTool{cap: c}
}

// AsTools returns every registered capability as a trpc-agent-go
// tool, sorted by address for a deterministic LLM tool manifest.
// Each element also implements tool.CallableTool. This is the seam
// CoreLoop switches its aggregation to in Phase B; equivalence with
// the current hand-aggregation is covered by the adapter tests.
func (r *Registry) AsTools() []tool.Tool {
	descs := r.List("")
	out := make([]tool.Tool, 0, len(descs))
	for _, d := range descs {
		if c, ok := r.Resolve(d.Address); ok {
			out = append(out, ToolFromCapability(c))
		}
	}
	return out
}
