package search

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ============================================================================
// Search Evaluation Case Format
//
// JSONL format for search quality evaluation, compatible with
// volcengine/SearchCLI's query-generate → validate → plan → run flow.
//
// Each line is a JSON object representing one evaluation query with
// optional relevance labels (for offline evaluation) or source item
// IDs (for silver-label fast pass).
// ============================================================================

// EvalCase represents a single search evaluation query.
type EvalCase struct {
	// Query is the search query string.
	Query string `json:"query"`

	// QueryType categorises the query for per-type analysis.
	// Examples: "keyword", "natural_language", "entity", "long_tail".
	QueryType string `json:"query_type,omitempty"`

	// RelevantIDs lists document IDs known to be relevant.
	// Used for binary relevance (Precision, Recall, MRR).
	RelevantIDs []string `json:"relevant_ids,omitempty"`

	// RelevanceGrades maps document IDs to graded relevance (0-3).
	// Used for NDCG. If empty, RelevantIDs is used with grade=1.
	RelevanceGrades map[string]int `json:"relevance_grades,omitempty"`

	// SourceItemIDs lists the source items that generated this query.
	// Used for silver-label fast pass: if a strategy can't recall
	// any source item, it's eliminated early without LLM judging.
	SourceItemIDs []string `json:"source_item_ids,omitempty"`

	// Session filters results to a specific session (optional).
	Session string `json:"session,omitempty"`
}

// HasLabels returns true if the case has any relevance labels
// (either grades or relevant IDs).
func (c EvalCase) HasLabels() bool {
	return len(c.RelevanceGrades) > 0 || len(c.RelevantIDs) > 0
}

// Labels converts the case's label fields into a RelevanceLabels map.
// If RelevanceGrades is populated, it's used directly. Otherwise,
// RelevantIDs are assigned grade 1.
func (c EvalCase) Labels() RelevanceLabels {
	if len(c.RelevanceGrades) > 0 {
		return RelevanceLabels(c.RelevanceGrades)
	}
	labels := make(RelevanceLabels, len(c.RelevantIDs))
	for _, id := range c.RelevantIDs {
		labels[id] = 1
	}
	return labels
}

// ----------------------------------------------------------------------------
// JSONL file I/O
// ----------------------------------------------------------------------------

// LoadEvalCases reads evaluation cases from a JSONL file.
// Each line must be a valid JSON object. Empty lines and lines
// starting with '#' are skipped.
func LoadEvalCases(path string) ([]EvalCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open eval cases: %w", err)
	}
	defer f.Close()

	var cases []EvalCase
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var ec EvalCase
		if err := json.Unmarshal([]byte(line), &ec); err != nil {
			return nil, fmt.Errorf(
				"parse line %d: %w", lineNum, err)
		}
		if ec.Query == "" {
			return nil, fmt.Errorf(
				"line %d: empty query", lineNum)
		}
		cases = append(cases, ec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return cases, nil
}

// SaveEvalCases writes evaluation cases to a JSONL file.
func SaveEvalCases(path string, cases []EvalCase) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create eval cases: %w", err)
	}
	defer f.Close()

	writer := bufio.NewWriter(f)
	for _, ec := range cases {
		data, err := json.Marshal(ec)
		if err != nil {
			return fmt.Errorf("marshal case: %w", err)
		}
		if _, err := writer.Write(data); err != nil {
			return fmt.Errorf("write case: %w", err)
		}
		if err := writer.WriteByte('\n'); err != nil {
			return fmt.Errorf("write newline: %w", err)
		}
	}
	return writer.Flush()
}

// ----------------------------------------------------------------------------
// Validation
// ----------------------------------------------------------------------------

// ValidationReport summarises the validation results for a set of
// evaluation cases. Mirrors SearchCLI's `validate` command.
type ValidationReport struct {
	TotalCases       int            `json:"total_cases"`
	ValidCases       int            `json:"valid_cases"`
	DuplicateQueries int            `json:"duplicate_queries"`
	EmptyQueries     int            `json:"empty_queries"`
	TypeDistribution map[string]int `json:"type_distribution"`
	LabelCoverage    float64        `json:"label_coverage"`  // fraction with labels
	SourceCoverage   float64        `json:"source_coverage"` // fraction with source IDs
	Warnings         []string       `json:"warnings,omitempty"`
	Errors           []string       `json:"errors,omitempty"`
}

// ValidateCases checks a set of evaluation cases for common issues:
// empty queries, duplicates, type skew, and label coverage.
func ValidateCases(cases []EvalCase) ValidationReport {
	report := ValidationReport{
		TotalCases:       len(cases),
		TypeDistribution: make(map[string]int),
	}

	if len(cases) == 0 {
		report.Errors = append(report.Errors, "no cases provided")
		return report
	}

	seen := make(map[string]bool)
	labeledCount := 0
	sourceCount := 0

	for i, c := range cases {
		// Empty query check.
		if strings.TrimSpace(c.Query) == "" {
			report.EmptyQueries++
			report.Errors = append(report.Errors,
				fmt.Sprintf("case %d: empty query", i))
			continue
		}

		// Duplicate check.
		q := strings.ToLower(strings.TrimSpace(c.Query))
		if seen[q] {
			report.DuplicateQueries++
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("case %d: duplicate query %q", i, c.Query))
		}
		seen[q] = true

		// Type distribution.
		if c.QueryType == "" {
			report.TypeDistribution["untyped"]++
		} else {
			report.TypeDistribution[c.QueryType]++
		}

		// Label coverage.
		if c.HasLabels() {
			labeledCount++
		}
		if len(c.SourceItemIDs) > 0 {
			sourceCount++
		}

		report.ValidCases++
	}

	report.LabelCoverage = float64(labeledCount) / float64(len(cases))
	report.SourceCoverage = float64(sourceCount) / float64(len(cases))

	// Type skew warning: if one type > 60% of total.
	for typ, count := range report.TypeDistribution {
		if float64(count)/float64(len(cases)) > 0.6 && typ != "untyped" {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("type %q dominates (%d/%d = %.0f%%)",
					typ, count, len(cases),
					float64(count)/float64(len(cases))*100))
		}
	}

	return report
}

// ----------------------------------------------------------------------------
// Plan: budget prediction (mirrors SearchCLI's `plan` command)
// ----------------------------------------------------------------------------

// TunePlan predicts the cost of a tuning run before executing it.
type TunePlan struct {
	StrategyCount  int `json:"strategy_count"`
	QueryCount     int `json:"query_count"`
	SearchRequests int `json:"search_requests"` // strategy × query
	MaxLabels      int `json:"max_labels"`      // strategy × query × topK
	TopK           int `json:"top_k"`
	// EstimatedLLMCalls is the number of LLM Judge calls needed
	// (may be less than MaxLabels due to silver-label filtering
	// and label caching).
	EstimatedLLMCalls int `json:"estimated_llm_calls"`
}

// ComputePlan calculates the budget needed for a tuning run.
// searchRequests = strategyCount × queryCount
// maxLabels = strategyCount × queryCount × topK
func ComputePlan(
	strategyCount, queryCount, topK int,
	silverLabelFiltering bool,
) TunePlan {
	searchReqs := strategyCount * queryCount
	maxLabels := searchReqs * topK

	estimatedLLM := maxLabels
	if silverLabelFiltering {
		// Rough estimate: silver-label fast pass eliminates ~30%
		// of strategies before LLM judging.
		estimatedLLM = int(float64(maxLabels) * 0.7)
	}

	return TunePlan{
		StrategyCount:     strategyCount,
		QueryCount:        queryCount,
		SearchRequests:    searchReqs,
		MaxLabels:         maxLabels,
		TopK:              topK,
		EstimatedLLMCalls: estimatedLLM,
	}
}
