// Package evolution provides the skill self-evolution system.
package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/km269/wukong/internal/provider"
	"github.com/km269/wukong/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvolutionAnalyzer uses an LLM to analyze skill execution traces and
// generate patch suggestions for problematic skills.
type EvolutionAnalyzer struct {
	factory *provider.Factory
	config  *EvolutionConfig
	model   model.Model
}

// EvolutionConfig re-exported for internal use without import cycle.
// Matches config.EvolutionConfig with the fields the analyzer needs.
type EvolutionConfig struct {
	Enabled         bool
	AutoPatch       bool
	MinConfidence   float64
	MaxPatchSize    int
	AnalysisTimeout time.Duration
}

// NewEvolutionAnalyzer creates a new analyzer with the given model factory.
// It creates a dedicated model instance for analysis using the configured
// analysis provider/model, or falls back to the default provider.
func NewEvolutionAnalyzer(
	factory *provider.Factory,
	cfg *EvolutionConfig,
) (*EvolutionAnalyzer, error) {
	if factory == nil {
		return nil, fmt.Errorf("model factory is nil")
	}

	analyzer := &EvolutionAnalyzer{
		factory: factory,
		config:  cfg,
	}

	// Create the analysis model (cached)
	if err := analyzer.ensureModel(); err != nil {
		return nil, fmt.Errorf("create analysis model: %w", err)
	}

	return analyzer, nil
}

// ensureModel creates or reuses the analysis model instance.
func (a *EvolutionAnalyzer) ensureModel() error {
	if a.model != nil {
		return nil
	}

	mdl, err := a.factory.CreateDefaultModel()
	if err != nil {
		return fmt.Errorf("create analysis model: %w", err)
	}
	a.model = mdl
	return nil
}

// Analyze examines a skill execution trace and generates a patch
// suggestion if the skill instruction can be improved. Returns nil
// if no improvement is needed.
func (a *EvolutionAnalyzer) Analyze(
	ctx context.Context,
	trace *ExecutionTrace,
	skillContent string,
) (*PatchSuggestion, error) {
	if err := a.ensureModel(); err != nil {
		return nil, err
	}

	// Apply timeout if configured
	var cancel context.CancelFunc
	timeout := a.config.AnalysisTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := buildAnalysisPrompt(trace, skillContent)

	resp, err := a.callLLM(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("llm analysis failed: %w", err)
	}

	suggestion, err := parseAnalysisResponse(resp, trace.SkillName)
	if err != nil {
		return nil, fmt.Errorf("parse analysis response: %w", err)
	}

	// Validate the suggestion
	if suggestion == nil {
		return nil, nil // No improvement needed
	}
	if suggestion.Confidence < a.config.MinConfidence {
		util.Logger.Info("evolution: patch confidence too low, skipping",
			"skill", trace.SkillName,
			"confidence", suggestion.Confidence,
			"min_confidence", a.config.MinConfidence,
		)
		return nil, nil
	}

	// Validate patch size
	if a.config.MaxPatchSize > 0 &&
		len(suggestion.DiffContent) > a.config.MaxPatchSize {
		util.Logger.Warn("evolution: patch exceeds max size, skipping",
			"skill", trace.SkillName,
			"patch_size", len(suggestion.DiffContent),
			"max_size", a.config.MaxPatchSize,
		)
		return nil, nil
	}

	return suggestion, nil
}

// callLLM sends the analysis prompt to the LLM and returns the
// raw response text.
func (a *EvolutionAnalyzer) callLLM(
	ctx context.Context, prompt string,
) (string, error) {
	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage(prompt),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:   util.IntPtr(2048),
			Temperature: util.Float64Ptr(0.2),
			Stream:      false,
		},
	}

	respChan, err := a.model.GenerateContent(ctx, req)
	if err != nil {
		return "", fmt.Errorf("generate content: %w", err)
	}

	var response strings.Builder
	for resp := range respChan {
		if resp.Error != nil {
			return "", fmt.Errorf(
				"analysis error: %s", resp.Error.Message)
		}
		if len(resp.Choices) > 0 {
			response.WriteString(
				resp.Choices[0].Message.Content)
		}
	}

	return response.String(), nil
}

// buildAnalysisPrompt creates a structured prompt for the LLM to
// analyze the skill execution trace and suggest improvements.
func buildAnalysisPrompt(
	trace *ExecutionTrace, skillContent string,
) string {
	traceJSON, _ := json.MarshalIndent(trace.ToolCalls, "", "  ")

	return fmt.Sprintf(`You are a Skill Evolution Analyzer. Your job is to examine a skill's execution trace and determine if the skill's instruction (SKILL.md) needs improvement to prevent future errors.

## Current SKILL.md Content
%s

## Execution Trace
- Skill: %s
- Success: %v
- Errors: %d
- Quality Score: %.2f
- Duration: %v
- Tool Calls:
%s

## Analysis Instructions
1. Examine the execution trace carefully. Look for:
   - Tool call failures and their root causes
   - Missing prerequisites (steps the skill forgot to mention)
   - Outdated or incorrect instructions
   - Ambiguous wording that could confuse the agent
   - Missing error-handling guidance
   - Steps that could be ordered better to prevent failures

2. If you find NO actionable issues, respond with:
   {"has_issue": false}

3. If you find issues that can be fixed by updating SKILL.md, respond with:
   {
     "has_issue": true,
     "problem_type": "missing_prerequisite|outdated_instruction|parameter_error|ambiguous_wording|missing_error_handling",
     "reason": "Brief explanation of why the patch is needed",
     "patch": "The exact Markdown text to ADD to the SKILL.md body (not YAML front matter). Format as clear, actionable instructions.",
     "confidence": 0.0-1.0
   }

4. Important rules:
   - The patch should be NEW instructions to ADD to the existing SKILL.md body
   - Do NOT modify the YAML front matter (name/description)
   - Be specific and actionable in your suggestions
   - Only suggest changes that directly address the observed failure
   - Confidence should reflect how certain you are the patch will prevent recurrence
   - If the failure is not caused by skill instruction flaws, do NOT suggest a patch

Respond ONLY with valid JSON, no other text.`,
		skillContent, trace.SkillName, trace.Success,
		trace.ErrorCount, trace.QualityScore,
		trace.Duration, string(traceJSON),
	)
}

// AnalysisResponse is the structured response from the LLM analyzer.
type AnalysisResponse struct {
	HasIssue    bool    `json:"has_issue"`
	ProblemType string  `json:"problem_type"`
	Reason      string  `json:"reason"`
	Patch       string  `json:"patch"`
	Confidence  float64 `json:"confidence"`
}

// parseAnalysisResponse parses the LLM's JSON response into a
// PatchSuggestion. Returns nil if no issue was found.
func parseAnalysisResponse(
	resp string, skillName string,
) (*PatchSuggestion, error) {
	// Clean the response - the LLM may wrap JSON in markdown code blocks
	resp = stripMarkdownCodeBlock(resp)

	var ar AnalysisResponse
	if err := json.Unmarshal([]byte(resp), &ar); err != nil {
		return nil, fmt.Errorf(
			"parse json response: %w (raw: %s)", err,
			truncate(resp, 200),
		)
	}

	if !ar.HasIssue {
		return nil, nil
	}

	if ar.Patch == "" {
		return nil, fmt.Errorf("has_issue is true but patch is empty")
	}

	if ar.Confidence < 0 {
		ar.Confidence = 0
	}
	if ar.Confidence > 1.0 {
		ar.Confidence = 1.0
	}

	return &PatchSuggestion{
		SkillName:   skillName,
		Reason:      ar.Reason,
		ProblemType: ar.ProblemType,
		DiffContent: ar.Patch,
		Confidence:  ar.Confidence,
		GeneratedAt: time.Now(),
	}, nil
}

// stripMarkdownCodeBlock removes markdown code block fences
// (```json ... ```) from LLM responses.
func stripMarkdownCodeBlock(s string) string {
	s = strings.TrimSpace(s)
	// Strip leading ```json or ```
	if strings.HasPrefix(s, "```") {
		nl := strings.Index(s, "\n")
		if nl > 0 {
			s = s[nl+1:]
		}
	}
	// Strip trailing ```
	if strings.HasSuffix(s, "```") {
		s = s[:len(s)-3]
	}
	return strings.TrimSpace(s)
}

// truncate cuts a string to maxLen characters, appending "..."
// if it was shortened.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
