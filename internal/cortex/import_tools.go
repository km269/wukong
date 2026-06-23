// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements ImportFlow tools — exposing DDL parsing,
// mapping plan generation, and data import as callable tools
// for the agent.
package cortex

import (
	"context"
	"fmt"
	"strings"

	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// ImportToolManager wraps ImportFlowService and provides data import
// tools for the agent.
type ImportToolManager struct {
	importFlow *ImportFlowService
	jsonGen   graphflow.JSONGenerator
}

// NewImportToolManager creates an import tool manager.
// The jsonGen parameter enables LLM-driven DDL-to-entity mapping.
// Pass nil to use deterministic mapping only.
func NewImportToolManager(
	importFlow *ImportFlowService,
	jsonGen graphflow.JSONGenerator,
) *ImportToolManager {
	return &ImportToolManager{
		importFlow: importFlow,
		jsonGen:   jsonGen,
	}
}

// Tools returns data import function tools.
func (m *ImportToolManager) Tools() []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(
			m.parseDDL,
			function.WithName("importflow_ddl_parse"),
			function.WithDescription(
				"Parse CREATE TABLE DDL statements and "+
					"return the detected table schemas. "+
					"Use this to understand database schemas "+
					"before importing them into the knowledge graph.",
			),
		),
		function.NewFunctionTool(
			m.planFromDDL,
			function.WithName("importflow_ddl_plan"),
			function.WithDescription(
				"Generate a deterministic mapping plan from "+
					"CREATE TABLE DDL. Tables become entity "+
					"classes, PKs become entity IDs, FKs become "+
					"relations. No LLM required.",
			),
		),
		function.NewFunctionTool(
			m.planFromDDLAI,
			function.WithName("importflow_ddl_plan_ai"),
			function.WithDescription(
				"Generate an AI-enhanced mapping plan from "+
					"CREATE TABLE DDL. Uses LLM to enrich "+
					"semantic relation names and infer implicit "+
					"relationships. Falls back to deterministic "+
					"mapping if LLM is unavailable.",
			),
		),
		function.NewFunctionTool(
			m.importCSV,
			function.WithName("importflow_csv"),
			function.WithDescription(
				"Import CSV data into the knowledge graph "+
					"using a previously generated mapping plan. "+
					"The CSV will be processed row by row to "+
					"build entities and relations in the KG.",
			),
		),
	}
}

// --- DDL Parse ---

type DDLAnalyzeReq struct {
	DDL string `json:"ddl" jsonschema:"description=CREATE TABLE DDL statements (PostgreSQL/MySQL subset)"`
}

type DDLAnalyzeRsp struct {
	Success bool                   `json:"success"`
	Tables  []importflow.DDLTable  `json:"tables,omitempty"`
	Count   int                    `json:"count"`
	Error   string                 `json:"error,omitempty"`
}

func (m *ImportToolManager) parseDDL(
	ctx context.Context, req DDLAnalyzeReq,
) (DDLAnalyzeRsp, error) {
	tables, err := m.importFlow.ParseDDL(req.DDL)
	if err != nil {
		return DDLAnalyzeRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return DDLAnalyzeRsp{
		Success: true,
		Tables:  tables,
		Count:   len(tables),
	}, nil
}

// --- DDL Plan (Deterministic) ---

type DDLPlanReq struct {
	DDL           string `json:"ddl" jsonschema:"description=CREATE TABLE DDL statements"`
	RelationStyle string `json:"relation_style,omitempty" jsonschema:"description=Relation naming style: empty, column, or reftable"`
}

type DDLPlanRsp struct {
	Success    bool                     `json:"success"`
	Plan       *importflow.MappingPlan  `json:"plan,omitempty"`
	TableCount int                      `json:"table_count"`
	Error      string                   `json:"error,omitempty"`
}

func (m *ImportToolManager) planFromDDL(
	ctx context.Context, req DDLPlanReq,
) (DDLPlanRsp, error) {
	opts := importflow.DDLMappingOptions{
		RelationStyle: req.RelationStyle,
	}
	plan, tables, err := m.importFlow.MappingFromDDL(
		req.DDL, opts)
	if err != nil {
		return DDLPlanRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return DDLPlanRsp{
		Success:    true,
		Plan:       &plan,
		TableCount: len(tables),
	}, nil
}

// --- DDL Plan (AI Enhanced) ---

type DDLPlanAIReq struct {
	DDL           string `json:"ddl" jsonschema:"description=CREATE TABLE DDL statements"`
	RelationStyle string `json:"relation_style,omitempty" jsonschema:"description=Relation naming style"`
}

type DDLPlanAIRsp struct {
	Success    bool                     `json:"success"`
	Plan       *importflow.MappingPlan  `json:"plan,omitempty"`
	TableCount int                      `json:"table_count"`
	LLMUsed    bool                     `json:"llm_used"`
	Error      string                   `json:"error,omitempty"`
}

func (m *ImportToolManager) planFromDDLAI(
	ctx context.Context, req DDLPlanAIReq,
) (DDLPlanAIRsp, error) {
	opts := importflow.DDLMappingOptions{
		RelationStyle: req.RelationStyle,
	}
	// Use LLM-driven DDL-to-entity mapping when jsonGen is available.
	// Falls back to deterministic mapping when jsonGen is nil.
	plan, tables, llmUsed, err :=
		m.importFlow.MappingFromDDLWithLLM(
			ctx, req.DDL, m.jsonGen, opts)
	if err != nil {
		return DDLPlanAIRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}
	return DDLPlanAIRsp{
		Success:    true,
		Plan:       &plan,
		TableCount: len(tables),
		LLMUsed:    llmUsed,
	}, nil
}

// --- CSV Import ---

type CSVImportReq struct {
	CSV       string                 `json:"csv" jsonschema:"description=CSV data to import (first row as headers)"`
	Plan      importflow.MappingPlan `json:"plan" jsonschema:"description=Mapping plan from importflow_ddl_plan or importflow_ddl_plan_ai"`
}

type CSVImportRsp struct {
	Success     bool     `json:"success"`
	RowsRead    int      `json:"rows_read,omitempty"`
	KGInserted  int      `json:"kg_inserted,omitempty"`
	RAGInserted int      `json:"rag_inserted,omitempty"`
	Errors      []string `json:"errors,omitempty"`
	Error       string   `json:"error,omitempty"`
}

func (m *ImportToolManager) importCSV(
	ctx context.Context, req CSVImportReq,
) (CSVImportRsp, error) {
	report, err := m.importFlow.ImportCSV(
		ctx, req.CSV, req.Plan)
	if err != nil {
		return CSVImportRsp{
			Success: false,
			Error:   err.Error(),
		}, nil
	}

	errStrs := make([]string, len(report.Errors))
	for i, e := range report.Errors {
		errStrs[i] = e.Error()
	}

	return CSVImportRsp{
		Success:     len(report.Errors) == 0,
		RowsRead:    report.RowsRead,
		KGInserted:  report.TriplesCreated,
		RAGInserted: report.ChunksIndexed,
		Errors:      errStrs,
	}, nil
}

// --- Utility for formatting DDL tables as readable text ---

// FormatDDLTables converts DDLTable list to a human-readable summary.
func FormatDDLTables(tables []importflow.DDLTable) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"Parsed %d table(s):\n", len(tables)))
	for _, t := range tables {
		b.WriteString(fmt.Sprintf(
			"\nTable: %s\n", t.Name))
		b.WriteString(fmt.Sprintf(
			"  Columns: %d", len(t.Columns)))
		if len(t.PrimaryKey) > 0 {
			b.WriteString(fmt.Sprintf(
				" (PK: %s)",
				strings.Join(t.PrimaryKey, ", ")))
		}
		b.WriteString("\n")
		for _, c := range t.Columns {
			b.WriteString(fmt.Sprintf(
				"    %s (%s)\n", c.Name, c.Type))
		}
		if len(t.ForeignKeys) > 0 {
			b.WriteString("  Foreign Keys:\n")
			for _, fk := range t.ForeignKeys {
				b.WriteString(fmt.Sprintf(
					"    %s → %s(%s)\n",
					fk.Column, fk.RefTable, fk.RefColumn))
			}
		}
	}
	return b.String()
}
