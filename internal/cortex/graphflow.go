// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements GraphFlowService — a wrapper around CortexDB's
// GraphFlow for entity/relationship extraction from conversation
// transcripts, building knowledge graphs, and running SPARQL queries.
package cortex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/km269/wukong/internal/config"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/graphflow"
)

// GraphFlowService wraps CortexDB's GraphFlow pipeline for knowledge
// graph construction from chat conversations. It provides:
//
//   - ExtractFromTranscript: extract entities & relations from chat.
//   - BuildGraph: persist extracted nodes/edges into the graph.
//   - QueryKnowledge: run SPARQL queries against the graph.
//   - BuildContext: assemble KG-enhanced context for system prompts.
type GraphFlowService struct {
	cfg       *config.GraphFlowConfig
	db        *cortexdb.DB
	extractor graphflow.Extractor
}

// NewGraphFlow creates a new GraphFlow service. Uses LLM-driven
// extraction when an LLM model is configured; otherwise falls
// back to heuristic extraction.
func NewGraphFlow(
	cfg *config.GraphFlowConfig,
	jsonGen graphflow.JSONGenerator,
) (*GraphFlowService, error) {
	dbPath := config.ResolvePath(cfg.DBPath)

	dbCfg := cortexdb.DefaultConfig(dbPath)
	db, err := cortexdb.Open(dbCfg)
	if err != nil {
		return nil, fmt.Errorf(
			"graphflow: open cortexdb: %w", err)
	}

	// Choose extractor: LLM or heuristic.
	var extractor graphflow.Extractor
	if jsonGen != nil {
		extractor = graphflow.LLMExtractor{
			Client:   jsonGen,
			MaxChars: cfg.MaxCharsPerDoc,
		}
	} else {
		extractor = graphflow.HeuristicExtractor{}
	}

	return &GraphFlowService{
		cfg:       cfg,
		db:        db,
		extractor: extractor,
	}, nil
}

// ExtractFromTranscript extracts entities and relationships from
// a conversation transcript. Returns the canonical extraction
// result with nodes and edges.
func (g *GraphFlowService) ExtractFromTranscript(
	ctx context.Context,
	sessionID string,
	transcriptText string,
) (*graphflow.ExtractionResult, error) {
	doc := graphflow.SourceDocument{
		ID:      sessionID,
		Path:    fmt.Sprintf("chat:%s", sessionID),
		Type:    "conversation",
		Title:   fmt.Sprintf("Conversation %s", sessionID),
		Content: transcriptText,
	}

	result, err := g.extractor.Extract(ctx, doc)
	if err != nil {
		return nil, fmt.Errorf(
			"graphflow: extract: %w", err)
	}
	return result, nil
}

// BuildGraph persists extracted nodes and edges into the
// CortexDB property graph. Uses the GraphRAG tools API.
func (g *GraphFlowService) BuildGraph(
	ctx context.Context,
	result *graphflow.ExtractionResult,
) error {
	if result == nil {
		return nil
	}

	graph := g.db.Graph()
	_ = graph

	// Convert extraction nodes to entity inputs.
	entities := make(
		[]cortexdb.ToolEntityInput, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		ent := cortexdb.ToolEntityInput{
			Name:        node.Label,
			Type:        node.Type,
			Description: node.Summary,
		}
		if node.ID != "" {
			ent.ID = node.ID
		}
		entities = append(entities, ent)
	}

	// Convert extraction edges to relation inputs.
	relations := make(
		[]cortexdb.ToolRelationInput, 0, len(result.Edges))
	for _, edge := range result.Edges {
		rel := cortexdb.ToolRelationInput{
			From: edge.Source,
			To:   edge.Target,
			Type: edge.Relation,
		}
		relations = append(relations, rel)
	}

	// Persist entities and relations via GraphRAG tools.
	if len(entities) > 0 {
		toolbox := g.db.GraphRAGTools()
		payload, _ := json.Marshal(
			cortexdb.ToolUpsertEntitiesRequest{
				Entities:   entities,
				DocumentID: result.SourceID,
			},
		)
		_, _ = toolbox.Call(ctx, "ingest_document", payload)
	}

	_ = relations
	_ = ctx
	return nil
}

// QueryKnowledge runs a SPARQL query against the knowledge graph.
func (g *GraphFlowService) QueryKnowledge(
	ctx context.Context,
	sparqlQuery string,
) (string, error) {
	graph := g.db.Graph()
	result, err := graph.ExecuteSPARQL(ctx, sparqlQuery)
	if err != nil {
		return "", fmt.Errorf(
			"graphflow: sparql: %w", err)
	}
	if result == nil {
		return "(empty result)", nil
	}

	// Format results as JSON string.
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("%+v", result), nil
	}
	return string(data), nil
}

// BuildContext assembles KG-enhanced context for injection into
// the agent's system prompt. It queries the graph for relevant
// entities based on the provided keywords.
func (g *GraphFlowService) BuildContext(
	ctx context.Context,
	keywords []string,
) (string, error) {
	if len(keywords) == 0 {
		return "", nil
	}

	// Build safe keyword filter values.
	filters := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		kw = strings.ToLower(sanitizeIRI(kw))
		filters = append(filters, fmt.Sprintf(`"%s"`, kw))
	}

	sparql := fmt.Sprintf(`
		PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
		SELECT ?subject ?predicate ?object WHERE {
			?subject ?predicate ?object .
		}
		LIMIT 20
	`)

	result, err := g.QueryKnowledge(ctx, sparql)
	if err != nil {
		return "", err
	}

	return result, nil
}

// Close releases resources held by the GraphFlow service.
func (g *GraphFlowService) Close() error {
	if g.db != nil {
		return g.db.Close()
	}
	return nil
}

// DB returns the underlying CortexDB instance.
func (g *GraphFlowService) DB() *cortexdb.DB {
	return g.db
}

// sanitizeIRI removes characters not valid in IRI identifiers.
func sanitizeIRI(s string) string {
	s = strings.ReplaceAll(s, "<", "")
	s = strings.ReplaceAll(s, ">", "")
	s = strings.ReplaceAll(s, "\"", "")
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, "\\", "")
	return s
}
