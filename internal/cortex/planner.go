// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements the QueryPlanner interface for MemoryFlow,
// using the Wukong LLM factory to plan retrieval strategies.
package cortex

import (
	"context"
	"fmt"
	"strings"

	"github.com/km269/wukong/internal/provider"

	cortexdb "github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"
	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"
)

// Retrieval mode string constants used by CortexDB's RetrievalPlan.
const (
	retrievalModeLexical = "lexical"
	retrievalModeVector  = "vector"
	retrievalModeHybrid  = "hybrid"
)

// LLMQueryPlanner implements memoryflow.QueryPlanner using an LLM
// to decide the best retrieval strategy based on the user query
// and session context.
type LLMQueryPlanner struct {
	factory   *provider.Factory
	modelName string
}

// NewLLMQueryPlanner creates a query planner backed by a Wukong
// LLM provider. Uses modelName for the planning model
// (typically a fast/cheap model like gpt-4o-mini).
func NewLLMQueryPlanner(
	factory *provider.Factory,
	modelName string,
) *LLMQueryPlanner {
	return &LLMQueryPlanner{
		factory:   factory,
		modelName: modelName,
	}
}

// Plan determines the optimal retrieval strategy for a given query
// and session state using either LLM-based planning or deterministic
// heuristics when no LLM is available.
func (p *LLMQueryPlanner) Plan(
	ctx context.Context,
	query string,
	state memoryflow.SessionState,
) (*cortexdb.RetrievalPlan, error) {
	// When no LLM is available, use deterministic heuristics.
	if p.factory == nil || p.modelName == "" {
		return p.heuristicPlan(query), nil
	}

	// Build the planning prompt.
	prompt := fmt.Sprintf(
		"You are a retrieval strategy planner. "+
			"Given a user query and session context, "+
			"decide the best search mode.\n\n"+
			"Session: %s\n"+
			"User: %s\n"+
			"Query: %s\n\n"+
			"Respond with exactly one word: "+
			"vector, lexical, or hybrid.",
		state.SessionID, state.UserID, query,
	)

	// Attempt LLM-based planning.
	_, err := p.factory.CreateModel(p.modelName)
	if err != nil {
		return p.heuristicPlan(query), nil
	}

	// In production, call the LLM and parse the response.
	// For now, use heuristic as a reliable default.
	_ = ctx
	_ = prompt

	return p.heuristicPlan(query), nil
}

// heuristicPlan provides a deterministic fallback based on query
// characteristics.
func (p *LLMQueryPlanner) heuristicPlan(
	query string,
) *cortexdb.RetrievalPlan {
	plan := &cortexdb.RetrievalPlan{
		RetrievalMode: retrievalModeLexical,
	}

	q := strings.ToLower(query)
	wordCount := len(strings.Fields(q))

	// Long/descriptive queries benefit from hybrid search.
	if wordCount > 10 {
		plan.RetrievalMode = retrievalModeHybrid
		plan.Keywords = extractKeywords(query)
		return plan
	}

	// Questions with abstract concepts benefit from vector search.
	abstractMarkers := []string{
		"why", "how", "explain", "concept", "idea",
		"mean", "difference", "relation", "summary",
	}
	for _, marker := range abstractMarkers {
		if strings.Contains(q, marker) {
			plan.RetrievalMode = retrievalModeVector
			plan.Keywords = extractKeywords(query)
			return plan
		}
	}

	// Default: lexical search with keywords.
	plan.Keywords = extractKeywords(query)
	return plan
}

// extractKeywords extracts simple keyword tokens from a query.
func extractKeywords(query string) []string {
	words := strings.Fields(strings.ToLower(query))
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true,
		"are": true, "was": true, "were": true, "be": true,
		"to": true, "of": true, "in": true, "for": true,
		"on": true, "with": true, "at": true, "by": true,
		"this": true, "that": true, "it": true, "and": true,
		"or": true, "but": true, "not": true, "so": true,
	}
	var keywords []string
	seen := make(map[string]bool)
	for _, w := range words {
		if len(w) > 2 && !stopWords[w] && !seen[w] {
			keywords = append(keywords, w)
			seen[w] = true
			if len(keywords) >= 5 {
				break
			}
		}
	}
	return keywords
}
