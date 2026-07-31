package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/km269/wukong/internal/config"
)

func TestFriendlyError(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "timeout",
			input:    "context deadline exceeded",
			expected: "Request timed out — the model took too long to respond",
		},
		{
			name:     "connection refused",
			input:    "dial tcp 127.0.0.1:8080: connectex: No connection could be made",
			expected: "Cannot connect to model — check network/provider",
		},
		{
			name:     "no such host",
			input:    "lookup api.example.com: no such host",
			expected: "Cannot connect to model — check network/provider",
		},
		{
			name:     "401 unauthorized",
			input:    "HTTP 401 unauthorized",
			expected: "Authentication failed — check API key or credentials",
		},
		{
			name:     "429 rate limit",
			input:    "Too many requests, rate limit exceeded",
			expected: "Rate limited by provider — wait and retry",
		},
		{
			name:     "500 server error",
			input:    "HTTP 500 internal server error",
			expected: "Model service unavailable — provider may be experiencing issues",
		},
		{
			name:     "502 bad gateway",
			input:    "502 Bad Gateway",
			expected: "Model service unavailable — provider may be experiencing issues",
		},
		{
			name:     "503 service unavailable",
			input:    "503 Service Unavailable",
			expected: "Model service unavailable — provider may be experiencing issues",
		},
		{
			name:     "canceled",
			input:    "request canceled by client",
			expected: "Request cancelled by user",
		},
		{
			name:     "cancelled",
			input:    "cancelled",
			expected: "Request cancelled by user",
		},
		{
			name:     "unknown error passthrough",
			input:    "some random error",
			expected: "some random error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := friendlyError(tt.input)
			if result != tt.expected {
				t.Errorf("friendlyError(%q) = %q, want %q",
					tt.input, result, tt.expected)
			}
		})
	}
}

func TestFriendlyError_CaseInsensitive(t *testing.T) {
	input := "CONTEXT DEADLINE EXCEEDED"
	expected := "Request timed out — the model took too long to respond"
	result := friendlyError(input)
	if result != expected {
		t.Errorf("friendlyError(%q) = %q, want %q", input, result, expected)
	}
}

func TestFriendlyError_Contains(t *testing.T) {
	input := "error: Context Deadline Exceeded while processing request"
	expected := "Request timed out — the model took too long to respond"
	result := friendlyError(input)
	if result != expected {
		t.Errorf("friendlyError(%q) = %q, want %q", input, result, expected)
	}
}

func TestBuildStartupSummary(t *testing.T) {
	cfg := &config.WukongConfig{
		LogLevel: "info",
		Session: config.SessionConfig{
			Backend: "sqlite",
		},
		Memory: config.MemoryConfig{
			Backend: "sqlite",
		},
		Recall: config.RecallConfig{
			Enabled:    true,
			SearchMode: "hybrid",
		},
		Agent: config.AgentConfig{
			Streaming:         true,
			ParallelTools:     true,
			ToolSearchEnabled: true,
			ContextCompaction: true,
		},
		Security: config.SecurityConfig{
			PermissionMode:   config.PermissionAuto,
			GuardrailEnabled: true,
		},
	}

	summary := buildStartupSummary(cfg)

	checks := []string{
		"🟢 Wukong Ready",
		"Log:      info",
		"Session: sqlite",
		"Memory: sqlite",
		"Recall: on(hybrid)",
		"Agent:",
		"streaming",
		"context_compaction",
		"Security: permission=auto",
		"guardrail=on",
		"Tools:",
		"parallel=on",
		"tool_search=on",
	}

	for _, check := range checks {
		if !strings.Contains(summary, check) {
			t.Errorf("buildStartupSummary missing %q in output:\n%s", check, summary)
		}
	}
}

func TestBuildStartupSummary_WithPlanner(t *testing.T) {
	cfg := &config.WukongConfig{
		LogLevel: "warn",
		Session: config.SessionConfig{
			Backend: "memory",
		},
		Memory: config.MemoryConfig{
			Backend: "memory",
		},
		Recall: config.RecallConfig{
			Enabled:    false,
			SearchMode: "keyword",
		},
		Agent: config.AgentConfig{
			Planner:           "plan-and-execute",
			Streaming:         false,
			ParallelTools:     false,
			ToolSearchEnabled: false,
			ContextCompaction: false,
		},
		Security: config.SecurityConfig{
			PermissionMode:   config.PermissionSmart,
			GuardrailEnabled: false,
		},
	}

	summary := buildStartupSummary(cfg)

	if !strings.Contains(summary, "Planner:  plan-and-execute") {
		t.Error("buildStartupSummary missing planner info")
	}

	if !strings.Contains(summary, "Recall: off(keyword)") {
		t.Error("buildStartupSummary missing recall off state")
	}

	if !strings.Contains(summary, "guardrail=off") {
		t.Error("buildStartupSummary missing guardrail off state")
	}
}

func TestBuildStartupSummary_ThinkingEnabled(t *testing.T) {
	thinking := true
	cfg := &config.WukongConfig{
		LogLevel: "debug",
		Session: config.SessionConfig{
			Backend: "sqlite",
		},
		Memory: config.MemoryConfig{
			Backend: "sqlite",
		},
		Recall: config.RecallConfig{
			Enabled:    true,
			SearchMode: "vector",
		},
		Agent: config.AgentConfig{
			ThinkingEnabled:   &thinking,
			Streaming:         true,
			ParallelTools:     false,
			ToolSearchEnabled: false,
			ContextCompaction: false,
		},
		Security: config.SecurityConfig{
			PermissionMode:   config.PermissionManual,
			GuardrailEnabled: true,
		},
	}

	summary := buildStartupSummary(cfg)

	if !strings.Contains(summary, "thinking") {
		t.Error("buildStartupSummary missing thinking feature")
	}
}

func TestAddMessage_Trimming(t *testing.T) {
	m := &Model{
		messages: make([]chatEntry, 0),
	}

	for i := 0; i < maxMessages+10; i++ {
		m.addMessage("user", "message "+string(rune('0'+i%10)))
	}

	if len(m.messages) != maxMessages {
		t.Errorf("expected %d messages after trimming, got %d",
			maxMessages, len(m.messages))
	}
}

func TestAddMessage_NoTrimUnderLimit(t *testing.T) {
	m := &Model{
		messages: make([]chatEntry, 0),
	}

	for i := 0; i < 10; i++ {
		m.addMessage("assistant", "response")
	}

	if len(m.messages) != 10 {
		t.Errorf("expected 10 messages, got %d", len(m.messages))
	}
}

func TestHandleCommand_Exit(t *testing.T) {
	m := &Model{
		quitRequested: false,
	}
	m.handleCommand("/exit")
	if !m.quitRequested {
		t.Error("expected quitRequested to be true for /exit")
	}
}

func TestHandleCommand_Quit(t *testing.T) {
	m := &Model{
		quitRequested: false,
	}
	m.handleCommand("/quit")
	if !m.quitRequested {
		t.Error("expected quitRequested to be true for /quit")
	}
}

func TestHandleCommand_New(t *testing.T) {
	m := &Model{
		sessionID:     "old-session",
		messages:      []chatEntry{{Role: "user", Content: "old"}},
		toolCalls:     []toolCallEntry{{Name: "test", Status: "running"}},
		currentStream: "stream content",
		instrRecorded: true,
	}
	m.handleCommand("/new")

	if m.sessionID == "old-session" {
		t.Error("expected new session ID after /new")
	}
	if len(m.messages) != 0 {
		t.Error("expected messages to be cleared after /new")
	}
	if len(m.toolCalls) != 0 {
		t.Error("expected tool calls to be cleared after /new")
	}
	if m.currentStream != "" {
		t.Error("expected currentStream to be cleared after /new")
	}
	if m.instrRecorded {
		t.Error("expected instrRecorded to be reset to false after /new")
	}
}

func TestHandleCommand_Clear(t *testing.T) {
	m := &Model{
		messages: []chatEntry{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
		toolCalls: []toolCallEntry{
			{Name: "test", Status: "done"},
		},
	}
	m.handleCommand("/clear")

	if len(m.messages) != 0 {
		t.Error("expected messages to be cleared after /clear")
	}
	if len(m.toolCalls) != 0 {
		t.Error("expected tool calls to be cleared after /clear")
	}
}

func TestHandleCommand_Help(t *testing.T) {
	m := &Model{}
	m.handleCommand("/help")

	if len(m.messages) == 0 {
		t.Error("expected help message to be added")
	}
	lastMsg := m.messages[len(m.messages)-1]
	if lastMsg.Role != "system" {
		t.Error("expected help message to be system role")
	}
}

func TestHandleCommand_Unknown(t *testing.T) {
	m := &Model{}
	m.handleCommand("/unknown")

	if len(m.messages) == 0 {
		t.Error("expected unknown command error message to be added")
	}
	lastMsg := m.messages[len(m.messages)-1]
	if lastMsg.Role != "system" {
		t.Error("expected unknown command message to be system role")
	}
	if !strings.Contains(lastMsg.Content, "Unknown command") {
		t.Error("expected 'Unknown command' in error message")
	}
}

func TestHandleCommand_ModelShow(t *testing.T) {
	cfg := &config.WukongConfig{
		DefaultProvider: "test-provider",
		Providers: []config.ProviderConfig{
			{Name: "test-provider", Model: "gpt-4"},
		},
	}
	m := &Model{cfg: cfg}
	m.handleCommand("/model")

	if len(m.messages) == 0 {
		t.Error("expected model info message to be added")
	}
	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "test-provider") {
		t.Error("expected model info to contain provider name")
	}
}

func TestHandleCommand_Exts(t *testing.T) {
	cfg := &config.WukongConfig{
		Extensions: []config.ExtensionConfig{
			{Name: "developer", Enabled: true},
			{Name: "memory", Enabled: false},
		},
	}
	m := &Model{cfg: cfg}
	m.handleCommand("/exts")

	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "developer") {
		t.Error("expected developer extension in listing")
	}
	if strings.Contains(lastMsg.Content, "memory") {
		t.Error("expected disabled extension to be excluded")
	}
}

func TestHandleCommand_Commands(t *testing.T) {
	m := &Model{}
	m.handleCommand("/commands")
	if m.modal == nil || m.modal.Type != ModalCommands {
		t.Error("expected commands modal to be opened")
	}
}

func TestHandleCommand_Skills(t *testing.T) {
	m := &Model{}
	m.handleCommand("/skills")
	if m.modal == nil || m.modal.Type != ModalSkills {
		t.Error("expected skills modal to be opened")
	}
}

func TestHandleCommand_Settings(t *testing.T) {
	cfg := &config.WukongConfig{
		LogLevel: "info",
		Session: config.SessionConfig{
			Backend: "sqlite",
		},
		Memory: config.MemoryConfig{
			Backend: "sqlite",
		},
		Recall: config.RecallConfig{
			Enabled:    true,
			SearchMode: "vector",
		},
	}
	m := &Model{
		cfg:          cfg,
		modelName:    "gpt-4",
		providerName: "openai",
	}
	m.handleCommand("/settings")
	if m.modal == nil || m.modal.Type != ModalSettings {
		t.Error("expected settings modal to be opened")
	}
}

func TestHandleCommand_HelpContent(t *testing.T) {
	m := &Model{}
	m.handleCommand("/help")

	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "Wukong Commands") {
		t.Error("expected help to contain 'Wukong Commands' header")
	}
	if !strings.Contains(lastMsg.Content, "/new") {
		t.Error("expected help to mention /new command")
	}
	if !strings.Contains(lastMsg.Content, "/exit") {
		t.Error("expected help to mention /exit command")
	}
}

func TestMaxMessagesConstant(t *testing.T) {
	if maxMessages != 500 {
		t.Errorf("expected maxMessages to be 500, got %d", maxMessages)
	}
}

func TestToolCallEntryFields(t *testing.T) {
	tc := toolCallEntry{
		Name:   "file_write",
		Args:   `{"path":"/tmp/test","content":"hello"}`,
		Status: "done",
		Result: "File written successfully",
	}

	if tc.Name != "file_write" {
		t.Errorf("expected Name 'file_write', got %q", tc.Name)
	}
	if tc.Status != "done" {
		t.Errorf("expected Status 'done', got %q", tc.Status)
	}
}

func TestChatEntryFields(t *testing.T) {
	ce := chatEntry{
		Role:    "assistant",
		Content: "Hello, how can I help?",
	}

	if ce.Role != "assistant" {
		t.Errorf("expected Role 'assistant', got %q", ce.Role)
	}
	if ce.Content != "Hello, how can I help?" {
		t.Errorf("unexpected content: %q", ce.Content)
	}
}

func TestTheme_DefaultDark(t *testing.T) {
	SetTheme(ThemeDark)
	if GetTheme() != ThemeDark {
		t.Error("expected default theme to be dark")
	}
}

func TestTheme_SwitchToLight(t *testing.T) {
	SetTheme(ThemeDark)
	SetTheme(ThemeLight)
	if GetTheme() != ThemeLight {
		t.Error("expected theme to be light after switch")
	}
}

func TestTheme_SwitchToClassic(t *testing.T) {
	SetTheme(ThemeClassic)
	if GetTheme() != ThemeClassic {
		t.Error("expected theme to be classic")
	}
}

func TestTheme_ParseTheme(t *testing.T) {
	tests := []struct {
		input    string
		expected ThemeType
	}{
		{"dark", ThemeDark},
		{"light", ThemeLight},
		{"classic", ThemeClassic},
		{"unknown", ThemeDark},
	}

	for _, tt := range tests {
		result := ParseTheme(tt.input)
		if result != tt.expected {
			t.Errorf("ParseTheme(%q) = %v, want %v",
				tt.input, result, tt.expected)
		}
	}
}

func TestThemeType_String(t *testing.T) {
	tests := []struct {
		theme    ThemeType
		expected string
	}{
		{ThemeDark, "dark"},
		{ThemeLight, "light"},
		{ThemeClassic, "classic"},
		{ThemeType(99), "dark"},
	}

	for _, tt := range tests {
		result := tt.theme.String()
		if result != tt.expected {
			t.Errorf("ThemeType(%d).String() = %q, want %q",
				tt.theme, result, tt.expected)
		}
	}
}

func TestHandleCommand_ThemeShow(t *testing.T) {
	m := &Model{}
	m.handleCommand("/theme")

	if len(m.messages) == 0 {
		t.Error("expected theme info message to be added")
	}
	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "Theme:") {
		t.Error("expected message to contain theme info")
	}
}

func TestHandleCommand_ThemeSwitch(t *testing.T) {
	SetTheme(ThemeDark)
	m := &Model{}
	m.handleCommand("/theme light")

	if GetTheme() != ThemeLight {
		t.Error("expected theme to be switched to light")
	}

	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "Theme changed") {
		t.Error("expected confirmation message for theme change")
	}
}

func TestHandleCommand_ThemeInHelp(t *testing.T) {
	m := &Model{}
	m.handleCommand("/help")

	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "/theme") {
		t.Error("expected help to mention /theme command")
	}
}

func TestToolCallEntry_CollapsedField(t *testing.T) {
	tc := toolCallEntry{
		Name:      "file_read",
		Args:      `{"path":"/tmp/test"}`,
		Status:    "done",
		Result:    "File content here",
		Collapsed: true,
	}

	if !tc.Collapsed {
		t.Error("expected Collapsed to be true")
	}
}

func TestAddToHistory(t *testing.T) {
	m := &Model{}
	m.addToHistory("test command")

	if len(m.cmdHistory) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(m.cmdHistory))
	}
	if m.cmdHistory[0] != "test command" {
		t.Errorf("expected history entry to be 'test command', got %q", m.cmdHistory[0])
	}
}

func TestAddToHistory_Duplicate(t *testing.T) {
	m := &Model{}
	m.addToHistory("cmd1")
	m.addToHistory("cmd1")

	if len(m.cmdHistory) != 1 {
		t.Errorf("expected duplicate to be rejected, got %d entries", len(m.cmdHistory))
	}
}

func TestSetLog(t *testing.T) {
	m := &Model{}
	m.setLog("test log message")

	if len(m.logBuffer) != 1 {
		t.Errorf("expected 1 log entry, got %d", len(m.logBuffer))
	}
}

func TestSetLog_EmptyMessage(t *testing.T) {
	m := &Model{}
	m.setLog("")

	if len(m.logBuffer) != 0 {
		t.Errorf("expected empty log to be ignored, got %d entries", len(m.logBuffer))
	}
}

func TestSetLog_RingBuffer(t *testing.T) {
	m := &Model{}
	for i := 0; i < 10; i++ {
		m.setLog(string(rune('0' + i)))
	}

	if len(m.logBuffer) != 5 {
		t.Errorf("expected log buffer to be capped at 5, got %d", len(m.logBuffer))
	}
}

func TestIncrementalRendering_InitialState(t *testing.T) {
	m := &Model{
		messages: []chatEntry{
			{Role: "user", Content: "hello"},
		},
	}

	if m.cachedMsgCount != 0 {
		t.Error("expected cachedMsgCount to be 0 initially")
	}
}

func TestIncrementalRendering_AfterViewportUpdate(t *testing.T) {
	m := &Model{
		messages: []chatEntry{
			{Role: "user", Content: "hello"},
		},
	}

	m.updateViewport()

	if m.cachedMsgCount != 1 {
		t.Errorf("expected cachedMsgCount to be 1 after update, got %d", m.cachedMsgCount)
	}
	if m.cachedMessages == "" {
		t.Error("expected cachedMessages to be non-empty after update")
	}
}

func TestIncrementalRendering_SeparateLayers(t *testing.T) {
	m := &Model{
		messages: []chatEntry{
			{Role: "user", Content: "hello"},
		},
	}

	m.updateViewport()
	firstMessages := m.cachedMessages
	if firstMessages == "" {
		t.Fatal("expected cachedMessages to be populated")
	}

	// Simulate a tool call status change (running -> done)
	m.toolCalls = append(m.toolCalls, toolCallEntry{
		Name:   "file_read",
		Status: "running",
	})
	m.updateViewport()

	// Now simulate status change only
	m.toolCalls[0].Status = "done"
	m.toolCalls[0].Result = "result here"
	m.updateViewport()

	// Messages cache should NOT have been rebuilt (still contains original)
	if m.cachedMsgCount != 1 {
		t.Errorf("cachedMsgCount should still be 1, got %d", m.cachedMsgCount)
	}

	// Tool cache should have been rebuilt
	if m.cachedToolCount != 1 {
		t.Errorf("cachedToolCount should be 1, got %d", m.cachedToolCount)
	}
	if m.cachedTools == "" {
		t.Error("expected cachedTools to be populated after tool change")
	}
}

func TestIncrementalRendering_MessageAppend(t *testing.T) {
	m := &Model{}
	m.updateViewport()

	// Append new message
	m.messages = append(m.messages, chatEntry{Role: "user", Content: "first"})
	m.updateViewport()
	if m.cachedMsgCount != 1 {
		t.Errorf("expected cachedMsgCount=1, got %d", m.cachedMsgCount)
	}
	cachedBefore := m.cachedMessages

	// Append another - should append to cache, not rebuild from scratch
	m.messages = append(m.messages, chatEntry{Role: "user", Content: "second"})
	m.updateViewport()

	if m.cachedMsgCount != 2 {
		t.Errorf("expected cachedMsgCount=2, got %d", m.cachedMsgCount)
	}
	if len(m.cachedMessages) <= len(cachedBefore) {
		t.Error("cachedMessages should have grown after appending")
	}
}

func TestIncrementalRendering_ResetOnClear(t *testing.T) {
	m := &Model{
		messages: []chatEntry{
			{Role: "user", Content: "hello"},
		},
		toolCalls: []toolCallEntry{
			{Name: "test", Status: "done"},
		},
	}

	m.updateViewport()

	// Clear - messages shrink
	m.messages = nil
	m.updateViewport()

	if m.cachedMsgCount != 0 {
		t.Errorf("expected cachedMsgCount=0 after clear, got %d", m.cachedMsgCount)
	}
	if m.cachedMessages != "" {
		t.Error("expected cachedMessages to be empty after clear")
	}
}

// ---------- Tool Audit Tests ----------

func TestRecordAuditEntry(t *testing.T) {
	m := &Model{}
	tc := toolCallEntry{
		Name:   "developer_search",
		Args:   `{"query":"test"}`,
		Result: "search result",
		Status: "done",
	}
	m.recordAuditEntry(tc)

	if len(m.auditLog) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(m.auditLog))
	}
	e := m.auditLog[0]
	if e.Name != "developer_search" {
		t.Errorf("expected name 'developer_search', got %q", e.Name)
	}
	if e.ArgsSize != len(tc.Args) {
		t.Errorf("expected args size %d, got %d", len(tc.Args), e.ArgsSize)
	}
	if e.ResultSize != len(tc.Result) {
		t.Errorf("expected result size %d, got %d", len(tc.Result), e.ResultSize)
	}
	if e.IsError {
		t.Error("expected no error for 'done' status")
	}
}

func TestRecordAuditEntry_ErrorStatus(t *testing.T) {
	m := &Model{}
	tc := toolCallEntry{
		Name:   "failed_tool",
		Status: "error",
	}
	m.recordAuditEntry(tc)

	if !m.auditLog[0].IsError {
		t.Error("expected error flag to be set for error status")
	}
}

func TestAuditLog_RingBuffer(t *testing.T) {
	m := &Model{}
	for i := 0; i < maxAuditEntries+10; i++ {
		m.recordAuditEntry(toolCallEntry{
			Name:   fmt.Sprintf("tool_%d", i),
			Status: "done",
		})
	}

	if len(m.auditLog) != maxAuditEntries {
		t.Errorf("expected audit log capped at %d, got %d",
			maxAuditEntries, len(m.auditLog))
	}
	// First entry should be the 11th (oldest dropped)
	if m.auditLog[0].Name != "tool_10" {
		t.Errorf("expected oldest entry 'tool_10', got %q",
			m.auditLog[0].Name)
	}
}

func TestRenderAuditPanel_Empty(t *testing.T) {
	m := &Model{}
	result := m.renderAuditPanel(10)

	if !strings.Contains(result, "No tool audit entries") {
		t.Errorf("expected empty state message, got %q", result)
	}
}

func TestRenderAuditPanel_WithEntries(t *testing.T) {
	m := &Model{}
	m.recordAuditEntry(toolCallEntry{
		Name:   "developer_search",
		Args:   "args",
		Result: "result",
		Status: "done",
	})
	m.recordAuditEntry(toolCallEntry{
		Name:   "memory_recall",
		Args:   "query",
		Status: "error",
	})

	result := m.renderAuditPanel(5)

	if !strings.Contains(result, "developer_search") {
		t.Error("expected panel to contain first tool name")
	}
	if !strings.Contains(result, "memory_recall") {
		t.Error("expected panel to contain second tool name")
	}
	if !strings.Contains(result, "Tool Audit") {
		t.Error("expected panel header 'Tool Audit'")
	}
}

func TestRenderAuditPanel_Limit(t *testing.T) {
	m := &Model{}
	for i := 0; i < 5; i++ {
		m.recordAuditEntry(toolCallEntry{
			Name:   fmt.Sprintf("tool_%d", i),
			Status: "done",
		})
	}

	result := m.renderAuditPanel(3)

	if !strings.Contains(result, "last 3 of 5") {
		t.Errorf("expected header to mention 'last 3 of 5', got %q", result)
	}
}

func TestHandleCommand_Audit(t *testing.T) {
	m := &Model{}
	m.recordAuditEntry(toolCallEntry{
		Name:   "test_tool",
		Status: "done",
	})
	m.handleCommand("/audit")

	if len(m.messages) == 0 {
		t.Fatal("expected audit message to be added")
	}
	lastMsg := m.messages[len(m.messages)-1]
	if lastMsg.Role != "system" {
		t.Error("expected audit message to be system role")
	}
	if !strings.Contains(lastMsg.Content, "test_tool") {
		t.Error("expected audit output to contain tool name")
	}
}

func TestHandleCommand_Audit_WithLimit(t *testing.T) {
	m := &Model{}
	for i := 0; i < 5; i++ {
		m.recordAuditEntry(toolCallEntry{
			Name:   fmt.Sprintf("tool_%d", i),
			Status: "done",
		})
	}
	m.handleCommand("/audit 3")

	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "last 3 of 5") {
		t.Errorf("expected limit to be applied, got %q", lastMsg.Content)
	}
}

// ---------- Exit / Cleanup Tests ----------

func TestRequestExit_NotStreaming(t *testing.T) {
	m := &Model{}
	cmd := m.requestExit()

	if !m.quitRequested {
		t.Error("expected quitRequested to be true")
	}
	if m.status != "Goodbye!" {
		t.Errorf("expected status 'Goodbye!', got %q", m.status)
	}
	if cmd == nil {
		t.Error("expected tea.Quit command, got nil")
	}
}

func TestRequestExit_Idempotent(t *testing.T) {
	m := &Model{}
	cmd1 := m.requestExit()
	cmd2 := m.requestExit()

	if !m.quitRequested {
		t.Error("expected quitRequested to be true after second call")
	}
	// Both should return non-nil tea.Quit
	if cmd1 == nil || cmd2 == nil {
		t.Error("expected both calls to return non-nil command")
	}
}

func TestCleanup_Idempotent(t *testing.T) {
	m := &Model{}

	// Call cleanup multiple times
	m.cleanup()
	m.cleanup()
	m.cleanup()

	// Should not panic or have issues
	if m.quitRequested {
		t.Error("cleanup should not set quitRequested")
	}
}

func TestCleanup_WithStreaming(t *testing.T) {
	m := &Model{
		streaming: true,
	}

	cancelled := false
	m.streamCancel = func() {
		cancelled = true
	}

	m.cleanup()

	if !cancelled {
		t.Error("expected streamCancel to be called during cleanup")
	}
}

func TestCleanup_WithStreamDone(t *testing.T) {
	m := &Model{
		streaming: true,
	}

	// Create a streamDone channel that is already closed
	streamDone := make(chan struct{})
	close(streamDone)
	m.streamDone = streamDone

	m.cleanup()

	// Should not block or panic
}

func TestHandleCommand_ExitCleansUp(t *testing.T) {
	m := &Model{
		streaming: true,
	}

	cancelled := false
	m.streamCancel = func() {
		cancelled = true
	}

	m.handleCommand("/exit")

	if !cancelled {
		t.Error("expected cleanup to cancel streaming on /exit")
	}
	if !m.quitRequested {
		t.Error("expected quitRequested to be set")
	}
}

func TestHandleCommand_QuitCleansUp(t *testing.T) {
	m := &Model{
		streaming: true,
	}

	cancelled := false
	m.streamCancel = func() {
		cancelled = true
	}

	m.handleCommand("/quit")

	if !cancelled {
		t.Error("expected cleanup to cancel streaming on /quit")
	}
}

func TestRequestExit_WithStreaming(t *testing.T) {
	m := &Model{
		streaming: true,
	}

	cancelled := false
	m.streamCancel = func() {
		cancelled = true
	}

	// Create a completed streamDone channel
	streamDone := make(chan struct{})
	close(streamDone)
	m.streamDone = streamDone

	cmd := m.requestExit()

	if !cancelled {
		t.Error("expected streamCancel to be called")
	}
	if cmd == nil {
		t.Error("expected tea.Quit command")
	}
}

func TestRequestExit_WithAgentLoop(t *testing.T) {
	m := &Model{}

	// nil loop should not cause panic (loop.Close is NOT called
	// by TUI — session.go handles it)
	m.loop = nil

	cmd := m.requestExit()

	if cmd == nil {
		t.Error("expected tea.Quit command even with nil loop")
	}
}

func TestRequestExit_DoesNotCloseLoop(t *testing.T) {
	// Verify that requestExit does NOT call loop.Close()
	// (session.go's shutdownBootstrap handles that)
	m := &Model{}

	cmd := m.requestExit()

	if cmd == nil {
		t.Error("expected tea.Quit command")
	}
	// If loop.Close() were called on a nil loop, it would panic.
	// The test passing means we didn't call it.
}

// TestCtrlD_ClearsTextareaBuffer verifies that after Ctrl+D sends a
// message, the textarea buffer is truly empty and NOT corrupted
// by the textarea library appending control rune (EOT=4).
// This reproduces the bug where Ctrl+D became unresponsive after
// the textarea received the Ctrl+D key via its own Update method.
func TestCtrlD_ClearsTextareaBuffer(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	ta.Focus()
	ta.SetValue("hello world")

	m := &Model{textarea: ta}

	keyMsg := tea.KeyMsg{Type: tea.KeyCtrlD}
	_, cmd := m.Update(keyMsg)

	if m.textarea.Value() != "" {
		t.Errorf("textarea should be empty after Ctrl+D, got %q",
			m.textarea.Value())
	}

	if cmd == nil {
		t.Error("expected sendMessage command for non-empty input")
	}
}

// TestCtrlD_EmptyInput_NoOp verifies that Ctrl+D with empty input
// is a no-op and does not corrupt the textarea buffer.
func TestCtrlD_EmptyInput_NoOp(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	ta.Focus()

	m := &Model{textarea: ta}

	keyMsg := tea.KeyMsg{Type: tea.KeyCtrlD}
	_, cmd := m.Update(keyMsg)

	if cmd != nil {
		t.Error("expected no command for empty Ctrl+D")
	}
	if m.textarea.Value() != "" {
		t.Errorf("textarea should remain empty, got %q",
			m.textarea.Value())
	}
}

// TestCtrlD_WhileStreaming_Ignores verifies that Ctrl+D during
// streaming does not corrupt the textarea buffer and sets status.
func TestCtrlD_WhileStreaming_Ignores(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	ta.Focus()
	ta.SetValue("partial input")

	m := &Model{textarea: ta, streaming: true}

	keyMsg := tea.KeyMsg{Type: tea.KeyCtrlD}
	_, cmd := m.Update(keyMsg)

	if cmd != nil {
		t.Error("expected no command when streaming")
	}

	if m.status == "" {
		t.Error("expected status message when Ctrl+D pressed during streaming")
	}
}
