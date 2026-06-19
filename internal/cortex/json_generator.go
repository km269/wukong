// Package cortex provides CortexDB-backed intelligent recall.
//
// This file implements the actual LLM JSON generation for
// graphflow.LLMExtractor entity extraction.
package cortex

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/km269/wukong/internal/provider"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// LLMJSONGenerator implements graphflow.JSONGenerator using Wukong's
// LLM provider factory for entity/relation extraction.
type LLMJSONGenerator struct {
	factory   *provider.Factory
	modelName string
}

// NewLLMJSONGenerator creates a JSON generator backed by a Wukong LLM.
func NewLLMJSONGenerator(
	factory *provider.Factory,
	modelName string,
) *LLMJSONGenerator {
	return &LLMJSONGenerator{
		factory:   factory,
		modelName: modelName,
	}
}

// GenerateJSON calls the LLM with system + user prompts and returns
// extracted entities/relations as JSON bytes.
func (g *LLMJSONGenerator) GenerateJSON(
	ctx context.Context,
	systemPrompt string,
	userPrompt string,
) ([]byte, error) {
	if g.factory == nil {
		return emptyResult(), nil
	}

	mdl, err := g.factory.CreateModel(g.modelName)
	if err != nil {
		return emptyResult(), nil
	}

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(systemPrompt),
			model.NewUserMessage(userPrompt),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: ptrFloat64(0.1),
			MaxTokens:   ptrInt(2048),
		},
	}

	respCh, err := mdl.GenerateContent(ctx, req)
	if err != nil {
		return emptyResult(), nil
	}

	var fullText strings.Builder
	for resp := range respCh {
		if resp.Error != nil {
			return emptyResult(), nil
		}
		if len(resp.Choices) > 0 {
			fullText.WriteString(
				resp.Choices[0].Message.Content)
		}
	}

	text := fullText.String()
	if text == "" {
		return emptyResult(), nil
	}

	// Extract JSON from the response (may be wrapped in markdown).
	jsonStart := strings.Index(text, "{")
	jsonEnd := strings.LastIndex(text, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		return []byte(text[jsonStart : jsonEnd+1]), nil
	}

	return []byte(text), nil
}

func emptyResult() []byte {
	data, _ := json.Marshal(map[string]interface{}{
		"nodes": []interface{}{},
		"edges": []interface{}{},
	})
	return data
}

func ptrFloat64(v float64) *float64 { return &v }
func ptrInt(v int) *int             { return &v }
