// Package eval provides a lightweight evaluation framework for
// testing agent behavior regression. It supports test case
// definition, metric evaluation, and result reporting.
//
// This is a Wukong-native implementation designed to be compatible
// with tRPC-Agent-Go's evaluation model (EvalSet, EvalMetric,
// Evaluator, Registry) for easy migration when available.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// EvalSet defines a collection of test cases for evaluation.
type EvalSet struct {
	Name      string     `json:"name"`
	Version   string     `json:"version"`
	TestCases []TestCase `json:"test_cases"`
}

// TestCase represents a single evaluation test case.
type TestCase struct {
	ID              string         `json:"id"`
	Description     string         `json:"description"`
	Conversation    []Turn         `json:"conversation"`
	ExpectedTools   []string       `json:"expected_tools,omitempty"`
	ExpectedPattern string         `json:"expected_pattern,omitempty"`
	MinResponseLen  int            `json:"min_response_len,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

// Turn represents a single turn in a conversation.
type Turn struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"` // Message content
}

// EvalMetric defines an evaluation metric with threshold.
type EvalMetric struct {
	Name      string  `json:"name"`
	Threshold float64 `json:"threshold"` // 0.0-1.0, must reach to pass
}

// EvalResult contains the evaluation result for a test case.
type EvalResult struct {
	TestCaseID string         `json:"test_case_id"`
	Passed     bool           `json:"passed"`
	Metrics    []MetricResult `json:"metrics"`
	Duration   time.Duration  `json:"duration"`
	Error      string         `json:"error,omitempty"`
}

// MetricResult contains the score for a single metric.
type MetricResult struct {
	MetricName string  `json:"metric_name"`
	Score      float64 `json:"score"`
	Threshold  float64 `json:"threshold"`
	Passed     bool    `json:"passed"`
}

// Evaluator runs evaluation tests against an agent runner.
type Evaluator struct {
	runner  runner.Runner
	metrics []EvalMetric
	results []EvalResult
}

// NewEvaluator creates a new evaluator.
func NewEvaluator(r runner.Runner, metrics []EvalMetric) *Evaluator {
	return &Evaluator{
		runner:  r,
		metrics: metrics,
	}
}

// Run evaluates all test cases and returns results.
func (e *Evaluator) Run(
	ctx context.Context, evalSet *EvalSet,
) ([]EvalResult, error) {
	var results []EvalResult

	for _, tc := range evalSet.TestCases {
		result := e.runTestCase(ctx, &tc)
		results = append(results, result)
	}

	e.results = results
	return results, nil
}

// runTestCase evaluates a single test case.
func (e *Evaluator) runTestCase(
	ctx context.Context, tc *TestCase,
) EvalResult {
	start := time.Now()
	result := EvalResult{
		TestCaseID: tc.ID,
		Passed:     true,
	}

	// Extract user message from conversation.
	userMsg := extractUserMessage(tc.Conversation)
	if userMsg == "" {
		result.Error = "no user message found"
		result.Passed = false
		result.Duration = time.Since(start)
		return result
	}

	// Run the agent.
	msg := model.NewUserMessage(userMsg)
	events, err := e.runner.Run(
		ctx, "eval-user", "eval-"+tc.ID, msg,
	)
	if err != nil {
		result.Error = fmt.Sprintf("runner error: %v", err)
		result.Passed = false
		result.Duration = time.Since(start)
		return result
	}

	// Collect response and tool calls.
	var responseText string
	var toolCalls []string
	for evt := range events {
		if evt.Error != nil {
			continue
		}
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			ch := evt.Response.Choices[0]
			responseText += ch.Delta.Content
			for _, tc := range ch.Message.ToolCalls {
				toolCalls = append(toolCalls,
					tc.Function.Name)
			}
		}
	}

	// Evaluate each metric.
	for _, metric := range e.metrics {
		mr := e.evaluateMetric(metric, responseText,
			toolCalls, tc)
		result.Metrics = append(result.Metrics, mr)
		if !mr.Passed {
			result.Passed = false
		}
	}

	result.Duration = time.Since(start)
	return result
}

// evaluateMetric computes a metric score for a test case.
func (e *Evaluator) evaluateMetric(
	metric EvalMetric,
	response string,
	toolCalls []string,
	tc *TestCase,
) MetricResult {
	mr := MetricResult{
		MetricName: metric.Name,
		Threshold:  metric.Threshold,
	}

	switch metric.Name {
	case "tool_trajectory_match":
		mr.Score = toolTrajectoryScore(toolCalls, tc.ExpectedTools)
	case "response_contains_pattern":
		mr.Score = patternMatchScore(response,
			tc.ExpectedPattern)
	case "response_min_length":
		mr.Score = minLengthScore(response,
			tc.MinResponseLen)
	case "response_not_empty":
		mr.Score = notEmptyScore(response)
	default:
		mr.Score = 1.0 // Pass unknown metrics by default.
	}

	mr.Passed = mr.Score >= metric.Threshold
	return mr
}

// --- Metric scoring functions ---

func toolTrajectoryScore(
	actual, expected []string,
) float64 {
	if len(expected) == 0 {
		return 1.0
	}
	matches := 0
	for _, exp := range expected {
		for _, act := range actual {
			if strings.Contains(
				strings.ToLower(act),
				strings.ToLower(exp),
			) {
				matches++
				break
			}
		}
	}
	return float64(matches) / float64(len(expected))
}

func patternMatchScore(response, pattern string) float64 {
	if pattern == "" {
		return 1.0
	}
	if strings.Contains(
		strings.ToLower(response),
		strings.ToLower(pattern),
	) {
		return 1.0
	}
	return 0.0
}

func minLengthScore(response string, minLen int) float64 {
	if minLen <= 0 {
		return 1.0
	}
	if len(response) >= minLen {
		return 1.0
	}
	return float64(len(response)) / float64(minLen)
}

func notEmptyScore(response string) float64 {
	if len(strings.TrimSpace(response)) > 0 {
		return 1.0
	}
	return 0.0
}

// --- Result reporting ---

// Summary returns a human-readable summary of evaluation results.
func (e *Evaluator) Summary() string {
	total := len(e.results)
	passed := 0
	for _, r := range e.results {
		if r.Passed {
			passed++
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Evaluation: %d/%d passed (%.0f%%)\n",
		passed, total,
		float64(passed)/float64(max(total, 1))*100,
	))

	for _, r := range e.results {
		status := "✓ PASS"
		if !r.Passed {
			status = "✗ FAIL"
		}
		sb.WriteString(fmt.Sprintf(
			"  %s: %s (%s)\n",
			r.TestCaseID, status,
			r.Duration.Round(time.Millisecond),
		))
		for _, m := range r.Metrics {
			mStatus := "✓"
			if !m.Passed {
				mStatus = "✗"
			}
			sb.WriteString(fmt.Sprintf(
				"    %s %s: %.2f (threshold: %.2f)\n",
				mStatus, m.MetricName,
				m.Score, m.Threshold,
			))
		}
		if r.Error != "" {
			sb.WriteString(fmt.Sprintf(
				"    error: %s\n", r.Error))
		}
	}

	return sb.String()
}

// SaveResults writes evaluation results to a JSON file.
func (e *Evaluator) SaveResults(path string) error {
	data, err := json.MarshalIndent(e.results, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal results: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// --- EvalSet loading ---

// LoadEvalSet loads an EvalSet from a JSON file.
func LoadEvalSet(path string) (*EvalSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read evalset file: %w", err)
	}
	var evalSet EvalSet
	if err := json.Unmarshal(data, &evalSet); err != nil {
		return nil, fmt.Errorf("unmarshal evalset: %w", err)
	}
	return &evalSet, nil
}

// --- Helpers ---

func extractUserMessage(conversation []Turn) string {
	for _, turn := range conversation {
		if turn.Role == "user" {
			return turn.Content
		}
	}
	return ""
}
