package eval

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// fakeRunner implements runner.Runner by replaying pre-built events.
type fakeRunner struct {
	events []*event.Event
	err    error
	// captures the arguments of the last Run call
	gotUserID    string
	gotSessionID string
	gotMsg       string
}

func (f *fakeRunner) Run(
	ctx context.Context,
	userID, sessionID string,
	msg model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	f.gotUserID = userID
	f.gotSessionID = sessionID
	f.gotMsg = msg.Content
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan *event.Event, len(f.events))
	for _, evt := range f.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (f *fakeRunner) Close() error { return nil }

// deltaEvent builds a streaming chunk event with the given content.
func deltaEvent(content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Delta: model.Message{Content: content},
			}},
		},
	}
}

// toolCallEvent builds an event carrying a tool call with the given names.
func toolCallEvent(names ...string) *event.Event {
	var calls []model.ToolCall
	for _, n := range names {
		calls = append(calls, model.ToolCall{
			Function: model.FunctionDefinitionParam{Name: n},
		})
	}
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{ToolCalls: calls},
			}},
		},
	}
}

func closeEnough(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestToolTrajectoryScore_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		actual   []string
		expected []string
		want     float64
	}{
		{"empty expected always passes", []string{"search"}, nil, 1.0},
		{"nil actual with empty expected", nil, nil, 1.0},
		{"exact match", []string{"search", "read"}, []string{"search", "read"}, 1.0},
		{"order irrelevant", []string{"read", "search"}, []string{"search", "read"}, 1.0},
		{"substring match", []string{"web_search"}, []string{"search"}, 1.0},
		{"case insensitive", []string{"SEARCH"}, []string{"search"}, 1.0},
		{"partial match", []string{"search"}, []string{"search", "read"}, 0.5},
		{"no match", []string{"search"}, []string{"read"}, 0.0},
		{"missing actual", nil, []string{"search"}, 0.0},
		{"duplicate expected all match independently", []string{"search"}, []string{"search", "search"}, 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolTrajectoryScore(tt.actual, tt.expected)
			if !closeEnough(got, tt.want) {
				t.Errorf("toolTrajectoryScore(%v, %v) = %v, want %v",
					tt.actual, tt.expected, got, tt.want)
			}
		})
	}
}

func TestPatternMatchScore_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		response string
		pattern  string
		want     float64
	}{
		{"empty pattern always passes", "anything", "", 1.0},
		{"exact match", "the answer is 42", "answer is", 1.0},
		{"case insensitive", "Say HELLO world", "hello", 1.0},
		{"no match", "foo bar", "baz", 0.0},
		{"empty response no match", "", "baz", 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := patternMatchScore(tt.response, tt.pattern)
			if !closeEnough(got, tt.want) {
				t.Errorf("patternMatchScore(%q, %q) = %v, want %v",
					tt.response, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestMinLengthScore_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		response string
		minLen   int
		want     float64
	}{
		{"non-positive threshold always passes", "x", 0, 1.0},
		{"negative threshold always passes", "x", -1, 1.0},
		{"long enough", "hello world", 5, 1.0},
		{"exactly threshold", "hello", 5, 1.0},
		{"short response proportional", "hi", 10, 0.2},
		{"empty response zero", "", 10, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := minLengthScore(tt.response, tt.minLen)
			if !closeEnough(got, tt.want) {
				t.Errorf("minLengthScore(%q, %d) = %v, want %v",
					tt.response, tt.minLen, got, tt.want)
			}
		})
	}
}

func TestNotEmptyScore_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     float64
	}{
		{"non-empty passes", "hello", 1.0},
		{"whitespace only fails", "   \n\t ", 0.0},
		{"empty fails", "", 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := notEmptyScore(tt.response); got != tt.want {
				t.Errorf("notEmptyScore(%q) = %v, want %v",
					tt.response, got, tt.want)
			}
		})
	}
}

func TestExtractUserMessage_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		conversation []Turn
		want         string
	}{
		{"first user turn returned", []Turn{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		}, "hi"},
		{"assistant first then user", []Turn{
			{Role: "assistant", Content: "hello"},
			{Role: "user", Content: "question"},
		}, "question"},
		{"later user turns ignored", []Turn{
			{Role: "user", Content: "first"},
			{Role: "user", Content: "second"},
		}, "first"},
		{"no user turn", []Turn{{Role: "assistant", Content: "hello"}}, ""},
		{"empty conversation", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractUserMessage(tt.conversation); got != tt.want {
				t.Errorf("extractUserMessage(%v) = %q, want %q",
					tt.conversation, got, tt.want)
			}
		})
	}
}

func TestRun_CollectsResponseToolCallsAndMetrics(t *testing.T) {
	fake := &fakeRunner{
		events: []*event.Event{
			toolCallEvent("search", "read_file"),
			deltaEvent("The answer "),
			deltaEvent("is 42."),
		},
	}
	ev := NewEvaluator(fake, []EvalMetric{
		{Name: "tool_trajectory_match", Threshold: 1.0},
		{Name: "response_contains_pattern", Threshold: 1.0},
		{Name: "response_min_length", Threshold: 1.0},
		{Name: "response_not_empty", Threshold: 1.0},
	})
	evalSet := &EvalSet{
		Name: "smoke",
		TestCases: []TestCase{{
			ID:           "tc-1",
			Conversation: []Turn{{Role: "user", Content: "hi"}},
			ExpectedTools: []string{
				"search", "read_file",
			},
			ExpectedPattern: "answer is",
			MinResponseLen:  10,
		}},
	}

	results, err := ev.Run(context.Background(), evalSet)
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]

	// Argument passthrough to the runner.
	if fake.gotUserID != "eval-user" {
		t.Errorf("userID = %q, want %q", fake.gotUserID, "eval-user")
	}
	if fake.gotSessionID != "eval-tc-1" {
		t.Errorf("sessionID = %q, want %q", fake.gotSessionID, "eval-tc-1")
	}
	if fake.gotMsg != "hi" {
		t.Errorf("message = %q, want %q", fake.gotMsg, "hi")
	}

	if !r.Passed {
		t.Errorf("result.Passed = false, want true (error: %s)", r.Error)
	}
	wantScores := map[string]float64{
		"tool_trajectory_match":     1.0,
		"response_contains_pattern": 1.0,
		"response_min_length":       1.0,
		"response_not_empty":        1.0,
	}
	if len(r.Metrics) != len(wantScores) {
		t.Fatalf("got %d metrics, want %d", len(r.Metrics), len(wantScores))
	}
	for _, m := range r.Metrics {
		want := wantScores[m.MetricName]
		if !m.Passed {
			t.Errorf("metric %s not passed", m.MetricName)
		}
		if !closeEnough(m.Score, want) {
			t.Errorf("metric %s score = %v, want %v", m.MetricName, m.Score, want)
		}
		if m.Threshold != 1.0 {
			t.Errorf("metric %s threshold = %v, want 1.0", m.MetricName, m.Threshold)
		}
	}
}

func TestRun_NoUserMessage_SetsErrorAndFails(t *testing.T) {
	ev := NewEvaluator(&fakeRunner{}, []EvalMetric{
		{Name: "response_not_empty", Threshold: 1.0},
	})
	results, err := ev.Run(context.Background(), &EvalSet{
		TestCases: []TestCase{{
			ID:           "tc-1",
			Conversation: []Turn{{Role: "assistant", Content: "hi"}},
		}},
	})
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	r := results[0]
	if r.Passed {
		t.Error("result.Passed = true, want false")
	}
	if !strings.Contains(r.Error, "no user message found") {
		t.Errorf("error = %q, want contains %q", r.Error, "no user message found")
	}
}

func TestRun_RunnerError_PropagatesAsFail(t *testing.T) {
	fake := &fakeRunner{err: errBoom}
	ev := NewEvaluator(fake, []EvalMetric{
		{Name: "response_not_empty", Threshold: 1.0},
	})
	results, err := ev.Run(context.Background(), &EvalSet{
		TestCases: []TestCase{{
			ID:           "tc-1",
			Conversation: []Turn{{Role: "user", Content: "hi"}},
		}},
	})
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	r := results[0]
	if r.Passed {
		t.Error("result.Passed = true, want false")
	}
	if !strings.Contains(r.Error, "boom") {
		t.Errorf("error = %q, want contains %q", r.Error, "boom")
	}
}

func TestRun_ErrorEventsAreSkipped(t *testing.T) {
	fake := &fakeRunner{
		events: []*event.Event{
			{Response: &model.Response{Error: &model.ResponseError{
				Message: "stream failed",
			}}},
			deltaEvent("still delivered"),
		},
	}
	ev := NewEvaluator(fake, []EvalMetric{
		{Name: "response_contains_pattern", Threshold: 1.0},
	})
	results, err := ev.Run(context.Background(), &EvalSet{
		TestCases: []TestCase{{
			ID:              "tc-1",
			Conversation:    []Turn{{Role: "user", Content: "hi"}},
			ExpectedPattern: "delivered",
		}},
	})
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	r := results[0]
	if !r.Passed {
		t.Errorf("result.Passed = false, want true (error: %s)", r.Error)
	}
}

func TestEvaluateMetric_UnknownMetricDefaultsToPass(t *testing.T) {
	ev := NewEvaluator(&fakeRunner{}, nil)
	mr := ev.evaluateMetric(
		EvalMetric{Name: "future_metric", Threshold: 0.5},
		"any response", nil, &TestCase{},
	)
	if !mr.Passed {
		t.Error("unknown metric should default to a passing score of 1.0")
	}
	if mr.Score != 1.0 {
		t.Errorf("score = %v, want 1.0", mr.Score)
	}
}

func TestSummary_FormatsPassRateAndFailures(t *testing.T) {
	ev := &Evaluator{results: []EvalResult{
		{TestCaseID: "p1", Passed: true,
			Metrics: []MetricResult{{MetricName: "m1", Score: 1, Threshold: 0.5, Passed: true}}},
		{TestCaseID: "p2", Passed: true},
		{TestCaseID: "f1", Passed: false, Error: "runner error: boom",
			Metrics: []MetricResult{{MetricName: "m2", Score: 0, Threshold: 0.5, Passed: false}}},
	}}

	summary := ev.Summary()
	for _, want := range []string{
		"2/3 passed (67%)",
		"✓ PASS",
		"✗ FAIL",
		"f1",
		"error: runner error: boom",
		"m2: 0.00 (threshold: 0.50)",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("Summary() missing %q:\n%s", want, summary)
		}
	}
}

func TestSummary_EmptyResultsNoPanic(t *testing.T) {
	ev := &Evaluator{}
	summary := ev.Summary()
	if !strings.Contains(summary, "0/0 passed") {
		t.Errorf("empty Summary() = %q, want contains %q", summary, "0/0 passed")
	}
}

func TestSaveResults_CreatesDirsAndWritesJSON(t *testing.T) {
	dir := t.TempDir()
	ev := &Evaluator{results: []EvalResult{
		{TestCaseID: "tc-1", Passed: true,
			Metrics: []MetricResult{{MetricName: "m", Score: 1, Threshold: 0.5, Passed: true}}},
	}}
	path := filepath.Join(dir, "nested", "results.json")

	if err := ev.SaveResults(path); err != nil {
		t.Fatalf("SaveResults() error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read results file: %v", err)
	}
	var decoded []EvalResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("results file is not valid JSON: %v", err)
	}
	if len(decoded) != 1 || decoded[0].TestCaseID != "tc-1" || !decoded[0].Passed {
		t.Errorf("decoded results = %+v, want single passed tc-1", decoded)
	}
}

func TestLoadEvalSet_LoadsFromJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evalset.json")
	content := `{
  "name": "demo",
  "version": "1.0",
  "test_cases": [
    {"id": "tc-1", "description": "d", "conversation": [{"role": "user", "content": "hi"}],
     "expected_tools": ["search"], "expected_pattern": "ok", "min_response_len": 5}
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write evalset: %v", err)
	}

	es, err := LoadEvalSet(path)
	if err != nil {
		t.Fatalf("LoadEvalSet() error: %v", err)
	}
	if es.Name != "demo" || es.Version != "1.0" {
		t.Errorf("header = %s/%s, want demo/1.0", es.Name, es.Version)
	}
	if len(es.TestCases) != 1 {
		t.Fatalf("got %d test cases, want 1", len(es.TestCases))
	}
	tc := es.TestCases[0]
	if tc.ID != "tc-1" || tc.ExpectedPattern != "ok" ||
		tc.MinResponseLen != 5 || len(tc.ExpectedTools) != 1 ||
		len(tc.Conversation) != 1 || tc.Conversation[0].Role != "user" {
		t.Errorf("test case = %+v, want fully populated", tc)
	}
}

func TestLoadEvalSet_MissingFile_ReturnsError(t *testing.T) {
	if _, err := LoadEvalSet(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("LoadEvalSet() missing file: expected error, got nil")
	}
}

func TestLoadEvalSet_InvalidJSON_ReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatalf("write bad file: %v", err)
	}
	if _, err := LoadEvalSet(path); err == nil {
		t.Error("LoadEvalSet() invalid JSON: expected error, got nil")
	}
}

func TestEvaluator_Run_EmptyEvalSet_EmptyResults(t *testing.T) {
	ev := NewEvaluator(&fakeRunner{}, nil)
	results, err := ev.Run(context.Background(), &EvalSet{})
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

var errBoom = errors.New("boom")
