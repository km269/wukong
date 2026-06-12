// Package builtin provides built-in extensions for wukong.
package builtin

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
)

// WebToolSet provides web-related tools: DuckDuckGo instant answer
// search. This complements the browser automation tools by providing
// a lightweight search alternative.
type WebToolSet struct {
	tools  []tool.Tool
	inited bool
	closed bool
}

// NewWebToolSet creates a web tool set with search capability.
func NewWebToolSet() *WebToolSet {
	ts := &WebToolSet{}
	ts.tools = []tool.Tool{
		duckduckgo.NewTool(),
	}
	return ts
}

// Tools returns the web tools.
func (ts *WebToolSet) Tools(_ context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name.
func (ts *WebToolSet) Name() string {
	return "web"
}

// Init initializes the tool set.
func (ts *WebToolSet) Init(_ context.Context) error {
	ts.inited = true
	return nil
}

// Close releases resources.
func (ts *WebToolSet) Close() error {
	ts.closed = true
	return nil
}
