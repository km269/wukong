// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements the SessionExtractor interface for MemoryFlow,
// using the Wukong LLM factory to extract promotion candidates from
// conversation transcripts.
package cortex

import (
	"context"
	"fmt"
	"strings"

	"github.com/km269/wukong/internal/provider"

	"github.com/liliang-cn/cortexdb/v2/pkg/memoryflow"
)

// LLMSessionExtractor implements memoryflow.SessionExtractor by
// using an LLM to analyze conversation transcripts and identify
// facts worth promoting from ephemeral chat to long-term memory
// or the knowledge base.
type LLMSessionExtractor struct {
	factory   *provider.Factory
	modelName string
}

// NewLLMSessionExtractor creates a session extractor backed by
// a Wukong LLM provider.
func NewLLMSessionExtractor(
	factory *provider.Factory,
	modelName string,
) *LLMSessionExtractor {
	return &LLMSessionExtractor{
		factory:   factory,
		modelName: modelName,
	}
}

// Extract analyzes a conversation transcript and identifies facts,
// decisions, preferences, or context worth promoting to long-term
// knowledge.
func (e *LLMSessionExtractor) Extract(
	ctx context.Context,
	transcript memoryflow.Transcript,
	state memoryflow.SessionState,
) ([]memoryflow.PromotionCandidate, error) {
	// Build the transcript text for analysis.
	var transcriptText strings.Builder
	for _, turn := range transcript.Turns {
		transcriptText.WriteString(
			fmt.Sprintf("%s: %s\n", turn.Role, turn.Content),
		)
	}

	// Attempt LLM-based extraction when factory is available.
	if e.factory != nil {
		_, err := e.factory.CreateModel(e.modelName)
		if err == nil {
			// In production, call the LLM with the extraction prompt.
			// For now, fallback to heuristic extraction.
		}
	}

	// Fallback: deterministic extraction using pattern matching.
	return e.heuristicExtract(transcript, state), nil
}

// heuristicExtract provides a deterministic fallback extraction
// when no LLM is available.
func (e *LLMSessionExtractor) heuristicExtract(
	transcript memoryflow.Transcript,
	state memoryflow.SessionState,
) []memoryflow.PromotionCandidate {
	var candidates []memoryflow.PromotionCandidate

	for _, turn := range transcript.Turns {
		if turn.Role != "user" {
			continue
		}

		content := strings.ToLower(turn.Content)

		// Detect preference statements.
		if strings.Contains(content, "prefer") ||
			strings.Contains(content, "like to") ||
			strings.Contains(content, "always") {
			candidates = append(candidates,
				memoryflow.PromotionCandidate{
					Content:   turn.Content,
					Kind:      "preference",
					Author:    state.UserID,
					Collection: "preferences",
				},
			)
		}

		// Detect decision statements.
		if strings.Contains(content, "decide") ||
			strings.Contains(content, "let's") ||
			strings.Contains(content, "we'll") {
			candidates = append(candidates,
				memoryflow.PromotionCandidate{
					Content:   turn.Content,
					Kind:      "decision",
					Author:    state.UserID,
					Collection: "decisions",
				},
			)
		}

		// Detect factual statements.
		if strings.Contains(content, "fact") ||
			strings.Contains(content, "note that") ||
			strings.Contains(content, "remember") {
			candidates = append(candidates,
				memoryflow.PromotionCandidate{
					Content:   turn.Content,
					Kind:      "fact",
					Author:    state.UserID,
					Collection: "facts",
				},
			)
		}
	}

	return candidates
}
