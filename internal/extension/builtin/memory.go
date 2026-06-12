// Package builtin provides the Memory built-in extension.
// When auto_extract is enabled, the memory tools from the tRPC memory
// service (memory_add, memory_search, memory_delete, memory_update,
// memory_load, memory_clear) are registered directly via function tools.
// The builtin MemoryToolSet here provides a fallback that delegates to
// the tRPC service when it's available.
package builtin

import (
	"context"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// MemoryToolSet wraps the tRPC memory.Service tools.
// When the memory service is injected via SetMemoryService, Tools()
// returns the standard tRPC memory tools instead of custom ones.
// This avoids tool name conflicts and ensures consistency.
type MemoryToolSet struct {
	tools   []tool.Tool
	cfg     *config.WukongConfig
	svc     memory.Service
	userKey memory.UserKey
	inited  bool
	closed  bool
}

// NewMemoryToolSet creates the memory built-in tool set.
// Initially it has no tools; after SetMemoryService is called,
// it returns the standard tRPC memory tools.
func NewMemoryToolSet(cfg *config.WukongConfig) *MemoryToolSet {
	return &MemoryToolSet{cfg: cfg}
}

// SetMemoryService injects the memory.Service and user key.
// After injection, Tools() will return the standard tRPC tools.
// The svc parameter is interface{} for loose coupling with the
// extension manager; it must be a memory.Service.
func (ts *MemoryToolSet) SetMemoryService(
	svc interface{}, appName, userID string,
) {
	memSvc, ok := svc.(memory.Service)
	if !ok {
		return
	}
	ts.svc = memSvc
	ts.userKey = memory.UserKey{
		AppName: appName,
		UserID:  userID,
	}
	// Use the standard tRPC memory tools
	ts.tools = memSvc.Tools()
}

// Tools returns the memory tools. If the service has been injected,
// returns the standard tRPC memory tools; otherwise returns nil.
func (ts *MemoryToolSet) Tools(ctx context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *MemoryToolSet) Name() string {
	return "memory"
}

// Init initializes the tool set.
func (ts *MemoryToolSet) Init(ctx context.Context) error {
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *MemoryToolSet) Close() error {
	ts.closed = true
	return nil
}
