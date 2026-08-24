package tune

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// LLMJudge uses an LLM to judge the relevance of a document
// to a query. Returns a graded relevance score (0-3).
//
// The judge prompt asks the LLM to rate the relevance on a
// 4-point scale:
//
//	0 = irrelevant
//	1 = marginally relevant
//	2 = relevant
//	3 = highly relevant
type LLMJudge struct {
	factory   *provider.Factory
	modelName string
	maxChars  int // truncate doc content to this length
}

// NewLLMJudge creates an LLM-based relevance judge.
func NewLLMJudge(
	factory *provider.Factory,
	modelName string,
) *LLMJudge {
	return &LLMJudge{
		factory:   factory,
		modelName: modelName,
		maxChars:  2000,
	}
}

// Judge evaluates the relevance of docContent to query.
// Returns a grade 0-3.
func (j *LLMJudge) Judge(
	ctx context.Context,
	query, docContent string,
) (int, error) {
	if j.factory == nil || j.modelName == "" {
		return 0, fmt.Errorf("llm judge: no model configured")
	}

	mdl, err := j.factory.CreateModel(j.modelName)
	if err != nil {
		return 0, fmt.Errorf("llm judge: create model: %w", err)
	}
	if mdl == nil {
		return 0, fmt.Errorf("llm judge: model is nil")
	}

	// Truncate document content to control token cost.
	doc := docContent
	if len(doc) > j.maxChars {
		doc = doc[:j.maxChars]
	}

	prompt := fmt.Sprintf(
		"You are a search relevance judge. "+
			"Rate the relevance of the document to the query "+
			"on a scale of 0-3:\n"+
			"  0 = irrelevant\n"+
			"  1 = marginally relevant\n"+
			"  2 = relevant\n"+
			"  3 = highly relevant\n\n"+
			"Query: %s\n\n"+
			"Document: %s\n\n"+
			"Respond with exactly one digit (0, 1, 2, or 3).",
		query, doc,
	)

	judgeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage(prompt),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:   util.IntPtr(8),
			Temperature: util.Float64Ptr(0.0),
			Stream:      false,
		},
	}

	respChan, err := mdl.GenerateContent(judgeCtx, req)
	if err != nil {
		return 0, fmt.Errorf("llm judge: generate: %w", err)
	}

	var response string
	for resp := range respChan {
		if resp.Error != nil {
			return 0, fmt.Errorf(
				"llm judge: API error: %s",
				resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			response += resp.Choices[0].Message.Content
		}
	}

	grade := parseGrade(response)
	return grade, nil
}

// parseGrade extracts a relevance grade (0-3) from the LLM response.
func parseGrade(raw string) int {
	raw = strings.TrimSpace(raw)
	for _, ch := range raw {
		if ch >= '0' && ch <= '3' {
			return int(ch - '0')
		}
	}
	return 0
}

// ----------------------------------------------------------------------------
// PreLabelledJudge: uses labels from the EvalCase (no LLM calls)
// ----------------------------------------------------------------------------

// PreLabelledJudge returns pre-defined relevance grades.
// This is used when EvalCase.RelevanceGrades is already populated
// and no LLM judging is needed.
type PreLabelledJudge struct {
	labels map[string]int // docID → grade
}

// NewPreLabelledJudge creates a judge from pre-existing labels.
func NewPreLabelledJudge(
	labels map[string]int,
) *PreLabelledJudge {
	return &PreLabelledJudge{labels: labels}
}

// Judge returns the pre-defined grade for the docContent (treated
// as doc ID), or 0 if not found.
func (j *PreLabelledJudge) Judge(
	ctx context.Context,
	query, docContent string,
) (int, error) {
	if grade, ok := j.labels[docContent]; ok {
		return grade, nil
	}
	return 0, nil
}
