// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements ImportFlowService — a wrapper around CortexDB's
// ImportFlow for structured data import (DDL → KG mapping, CSV → RAG+KG).
package cortex

import (
	"context"
	"fmt"
	"strings"

	"github.com/km269/wukong/internal/config"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
	"github.com/liliang-cn/cortexdb/v2/pkg/importflow"
)

// ImportFlowService wraps CortexDB's ImportFlow for importing
// structured data into the knowledge graph and RAG store.
// Supports:
//
//   - ParseDDL: parse CREATE TABLE statements.
//   - MappingFromDDL: deterministic DDL → MappingPlan.
//   - MappingFromDDLWithLLM: LLM-enhanced DDL → MappingPlan.
//   - ImportCSV: load CSV data through a MappingPlan into RAG+KG.
type ImportFlowService struct {
	cfg *config.ImportFlowConfig
	db  *cortexdb.DB
}

// NewImportFlow creates a new ImportFlow service.
func NewImportFlow(
	cfg *config.ImportFlowConfig,
) (*ImportFlowService, error) {
	dbPath := config.ResolvePath(cfg.DBPath)

	dbCfg := cortexdb.DefaultConfig(dbPath)
	db, err := cortexdb.Open(dbCfg)
	if err != nil {
		return nil, fmt.Errorf(
			"importflow: open cortexdb: %w", err)
	}

	return &ImportFlowService{
		cfg: cfg,
		db:  db,
	}, nil
}

// ParseDDL parses CREATE TABLE DDL statements and returns the
// detected table schemas.
func (s *ImportFlowService) ParseDDL(
	ddl string,
) ([]importflow.DDLTable, error) {
	tables, err := importflow.ParseDDL(ddl)
	if err != nil {
		return nil, fmt.Errorf(
			"importflow: parse ddl: %w", err)
	}
	return tables, nil
}

// MappingFromDDL generates a deterministic MappingPlan from DDL
// statements without using LLM. Tables become entity classes,
// primary keys become entity IDs, foreign keys become relations,
// and columns become RAG content + entity properties.
func (s *ImportFlowService) MappingFromDDL(
	ddl string,
	opts importflow.DDLMappingOptions,
) (importflow.MappingPlan, []importflow.DDLTable, error) {
	plan, tables, err := importflow.MappingFromDDL(ddl, opts)
	if err != nil {
		return plan, nil, fmt.Errorf(
			"importflow: mapping from ddl: %w", err)
	}
	return plan, tables, nil
}

// MappingFromDDLWithLLM generates an LLM-enhanced MappingPlan.
// It starts from the deterministic baseline and uses the LLM to
// enrich semantic relation names, infer implicit relations, and
// merge join tables as direct relations.
func (s *ImportFlowService) MappingFromDDLWithLLM(
	ctx context.Context,
	ddl string,
	jsonGen graphflow.JSONGenerator,
	opts importflow.DDLMappingOptions,
) (importflow.MappingPlan, []importflow.DDLTable, bool, error) {
	plan, tables, llmUsed, err := importflow.MappingFromDDLWithLLM(
		ctx, ddl, jsonGen, opts)
	if err != nil {
		return plan, nil, llmUsed, fmt.Errorf(
			"importflow: llm mapping from ddl: %w", err)
	}
	return plan, tables, llmUsed, nil
}

// ImportCSV loads CSV data and applies a MappingPlan to build
// RAG chunks and knowledge graph triples.
func (s *ImportFlowService) ImportCSV(
	ctx context.Context,
	csvData string,
	plan importflow.MappingPlan,
) (*importflow.Report, error) {
	// Create a CSV source from the data.
	src, err := importflow.NewCSVSource(
		strings.NewReader(csvData),
		importflow.CSVOptions{Table: "imported_table"},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"importflow: create csv source: %w", err)
	}
	defer src.Close()

	// Create importer and run with the provided plan.
	importer := importflow.New(s.db)
	return importer.Run(ctx, src, plan)
}

// Close releases resources.
func (s *ImportFlowService) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// DB returns the underlying CortexDB instance.
func (s *ImportFlowService) DB() *cortexdb.DB {
	return s.db
}
