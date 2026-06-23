// Package builtin provides built-in MCP extension tool sets.
//
// This file implements the CortexDB built-in extension tool set
// that exposes knowledge graph query and data import tools.
package builtin

import (
	"context"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// CortexToolSet provides CortexDB knowledge graph tools as a
// built-in extension tool set. The actual CortexDB services are
// injected externally via SetCortexDependencies before use.
type CortexToolSet struct {
	cfg       *config.WukongConfig
	tools     []tool.Tool
}

// NewCortexToolSet creates a new Cortex tool set from configuration.
// Use SetDependencies to inject the actual service instances.
func NewCortexToolSet(cfg *config.WukongConfig) *CortexToolSet {
	ts := &CortexToolSet{
		cfg:   cfg,
		tools: buildCortexTools(),
	}
	return ts
}

// Tools returns the cortex tool list (implements tool.ToolSet).
func (ts *CortexToolSet) Tools(context.Context) []tool.Tool {
	return ts.tools
}

// Name returns the tool set name for the extension registry.
func (ts *CortexToolSet) Name() string {
	return "cortex"
}

// Close releases resources (implements tool.ToolSet).
func (ts *CortexToolSet) Close() error {
	return nil
}

// buildCortexTools creates the CortexDB tool definitions.
// These delegate to the global registry set via session.go.
func buildCortexTools() []tool.Tool {
	return []tool.Tool{
		// Knowledge Graph tools
		function.NewFunctionTool(
			kgQueryStub,
			function.WithName("knowledge_graph_query"),
			function.WithDescription(
				"Query the knowledge graph using SPARQL. "+
					"The knowledge graph contains entities "+
					"extracted from conversations and "+
					"imported data.",
			),
		),
		function.NewFunctionTool(
			kgAnalyzeStub,
			function.WithName("knowledge_graph_analyze"),
			function.WithDescription(
				"Analyze the knowledge graph to discover "+
					"patterns, key entities, and structural "+
					"insights.",
			),
		),
		// ImportFlow tools
		function.NewFunctionTool(
			ddlParseStub,
			function.WithName("importflow_ddl_parse"),
			function.WithDescription(
				"Parse CREATE TABLE DDL statements and "+
					"return detected table schemas.",
			),
		),
		function.NewFunctionTool(
			ddlPlanStub,
			function.WithName("importflow_ddl_plan"),
			function.WithDescription(
				"Generate a mapping plan from DDL for "+
					"importing table data into the knowledge "+
					"graph.",
			),
		),
	}
}

// --- Tool stubs (delegate to global registry) ---

type kgQueryReq struct {
	Query string `json:"query"`
}

type kgQueryRsp struct {
	Success bool   `json:"success"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
}

func kgQueryStub(
	ctx context.Context, req kgQueryReq,
) (kgQueryRsp, error) {
	return kgQueryRsp{
		Success: false,
		Error: "Knowledge graph service not initialized. " +
			"Ensure CortexDB and GraphFlow are enabled in configuration.",
	}, nil
}

type kgAnalyzeReq struct{}

type kgAnalyzeRsp struct {
	Success     bool   `json:"success"`
	Summary     string `json:"summary,omitempty"`
	EntityCount int    `json:"entity_count,omitempty"`
	EdgeCount   int    `json:"edge_count,omitempty"`
	Error       string `json:"error,omitempty"`
}

func kgAnalyzeStub(
	ctx context.Context, req kgAnalyzeReq,
) (kgAnalyzeRsp, error) {
	return kgAnalyzeRsp{
		Success: false,
		Error: "Knowledge graph service not initialized. " +
			"Ensure CortexDB and GraphFlow are enabled in configuration.",
	}, nil
}

type ddlParseReq struct {
	DDL string `json:"ddl"`
}

type ddlParseRsp struct {
	Success bool     `json:"success"`
	Count   int      `json:"count"`
	Error   string   `json:"error,omitempty"`
}

func ddlParseStub(
	ctx context.Context, req ddlParseReq,
) (ddlParseRsp, error) {
	return ddlParseRsp{
		Success: false,
		Error: "ImportFlow service not initialized. " +
			"Ensure ImportFlow is enabled in configuration.",
	}, nil
}

type ddlPlanReq struct {
	DDL string `json:"ddl"`
}

type ddlPlanRsp struct {
	Success    bool   `json:"success"`
	TableCount int    `json:"table_count"`
	Error      string `json:"error,omitempty"`
}

func ddlPlanStub(
	ctx context.Context, req ddlPlanReq,
) (ddlPlanRsp, error) {
	return ddlPlanRsp{
		Success:    false,
		Error: "ImportFlow service not initialized. " +
			"Ensure ImportFlow is enabled in configuration.",
	}, nil
}
