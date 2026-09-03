package agent

import (
	"testing"
)

func TestAppendToolCall(t *testing.T) {
	tests := []struct {
		name          string
		initialCalls  []map[string]string
		toolName      string
		toolArgs      string
		expectedCount int
		expectedLast  map[string]string
	}{
		{
			name:          "append to empty slice",
			initialCalls:  []map[string]string{},
			toolName:      "read_file",
			toolArgs:      `{"path": "/test/file.txt"}`,
			expectedCount: 1,
			expectedLast: map[string]string{
				"name": "read_file",
				"args": `{"path": "/test/file.txt"}`,
			},
		},
		{
			name: "append to existing slice",
			initialCalls: []map[string]string{
				{"name": "read_file", "args": `{"path": "/test/file.txt"}`},
			},
			toolName:      "write_file",
			toolArgs:      `{"path": "/test/out.txt"}`,
			expectedCount: 2,
			expectedLast: map[string]string{
				"name": "write_file",
				"args": `{"path": "/test/out.txt"}`,
			},
		},
		{
			name: "append multiple calls",
			initialCalls: []map[string]string{
				{"name": "tool1", "args": `{"a": 1}`},
				{"name": "tool2", "args": `{"b": 2}`},
			},
			toolName:      "tool3",
			toolArgs:      `{"c": 3}`,
			expectedCount: 3,
			expectedLast: map[string]string{
				"name": "tool3",
				"args": `{"c": 3}`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := appendToolCall(tc.initialCalls, tc.toolName, tc.toolArgs)

			if len(result) != tc.expectedCount {
				t.Errorf("expected %d calls, got %d", tc.expectedCount, len(result))
			}

			if tc.expectedCount > 0 {
				last := result[len(result)-1]
				if last["name"] != tc.expectedLast["name"] {
					t.Errorf("last call name: expected %q, got %q", tc.expectedLast["name"], last["name"])
				}
				if last["args"] != tc.expectedLast["args"] {
					t.Errorf("last call args: expected %q, got %q", tc.expectedLast["args"], last["args"])
				}
			}
		})
	}
}

func TestEvolutionTracker_Name(t *testing.T) {
	tracker := &evolutionTracker{}
	if tracker.Name() != "evolution_tracker" {
		t.Errorf("expected name 'evolution_tracker', got %q", tracker.Name())
	}
}
