package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/project"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ansiRE matches SGR escape sequences emitted by lipgloss styles.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes color escape codes from a rendered string.
func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

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

// ---------- A1: handleCommand branch coverage ----------

// fakeProjectLister implements the projectLister interface for tests.
type fakeProjectLister struct {
	records []project.ProjectRecord
}

func (f *fakeProjectLister) ListProjects() []project.ProjectRecord {
	return f.records
}

// fakeSessionLister implements the sessionLister interface for tests.
type fakeSessionLister struct {
	sessions  []*session.Session
	deleted   []string // session ids passed to DeleteSession
	listErr   error
	deleteErr error
}

func (f *fakeSessionLister) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
) ([]*session.Session, error) {
	return f.sessions, f.listErr
}

func (f *fakeSessionLister) DeleteSession(
	ctx context.Context,
	key session.Key,
) error {
	f.deleted = append(f.deleted, key.SessionID)
	return f.deleteErr
}

func TestHandleCommand_Sessions_NoManager(t *testing.T) {
	m := &Model{}
	m.handleCommand("/sessions")

	if len(m.messages) == 0 {
		t.Fatal("expected a system message")
	}
	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "Session management is not available") {
		t.Errorf("expected 'not available' message, got %q", lastMsg.Content)
	}
}

func TestHandleCommand_Sessions_OpensModal(t *testing.T) {
	m := &Model{
		width:  80,
		height: 24,
		userID: "user-1",
		sessionMgr: &fakeSessionLister{
			sessions: []*session.Session{
				{ID: "sess-aaaa", UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
				{ID: "sess-bbbb"},
			},
		},
	}
	m.handleCommand("/sessions")

	if m.modal == nil || m.modal.Type != ModalSessions {
		t.Fatalf("expected sessions modal open, got %+v", m.modal)
	}
	// New + 2 sessions + Back
	if len(m.modal.Items) != 4 {
		t.Fatalf("items = %v, want 4 (new + 2 sessions + back)", m.modal.Items)
	}
	if m.modal.Items[0] != "+ New session" {
		t.Errorf("first item = %q, want + New session", m.modal.Items[0])
	}
	if !strings.Contains(m.modal.Items[1], "sess-aaaa") {
		t.Errorf("session item = %q, want to contain sess-aaaa", m.modal.Items[1])
	}
	if m.modal.Items[len(m.modal.Items)-1] != "— Back" {
		t.Errorf("last item = %q, want — Back", m.modal.Items[len(m.modal.Items)-1])
	}
}

func TestHandleModalSelection_SessionsResume(t *testing.T) {
	m := &Model{
		width:  80,
		height: 24,
		userID: "user-1",
		sessionMgr: &fakeSessionLister{
			sessions: []*session.Session{{ID: "sess-x"}},
		},
	}
	m.handleCommand("/sessions")
	// Select the session row (index 1).
	m.modal.Selected = 1
	m.handleModalSelection()

	if m.modal != nil {
		t.Fatal("expected modal closed after selection")
	}
	if m.sessionID != "sess-x" {
		t.Errorf("sessionID = %q, want sess-x", m.sessionID)
	}
}

func TestHandleModalSelection_SessionsNew(t *testing.T) {
	m := &Model{
		width:      80,
		height:     24,
		userID:     "user-1",
		sessionID:  "old",
		sessionMgr: &fakeSessionLister{sessions: []*session.Session{{ID: "sess-x"}}},
	}
	m.handleCommand("/sessions")
	// Index 0 is + New session.
	m.handleModalSelection()

	if m.modal != nil {
		t.Fatal("expected modal closed after New")
	}
	if m.sessionID == "old" || m.sessionID == "" {
		t.Errorf("sessionID after New = %q, want fresh value", m.sessionID)
	}
}

func TestHandleModalSelection_SessionsBack(t *testing.T) {
	m := &Model{
		width:      80,
		height:     24,
		userID:     "user-1",
		sessionID:  "old",
		sessionMgr: &fakeSessionLister{sessions: []*session.Session{{ID: "sess-x"}}},
	}
	m.handleCommand("/sessions")
	m.modal.Selected = len(m.modal.Items) - 1 // — Back
	m.handleModalSelection()

	if m.modal != nil {
		t.Fatal("expected modal closed after Back")
	}
	if m.sessionID != "old" {
		t.Errorf("sessionID = %q, want unchanged old", m.sessionID)
	}
}

func TestSessionModal_DeleteSelected(t *testing.T) {
	fake := &fakeSessionLister{
		sessions: []*session.Session{
			{ID: "sess-1"},
			{ID: "sess-2"},
		},
	}
	m := &Model{
		width:      80,
		height:     24,
		userID:     "user-1",
		sessionMgr: fake,
	}
	m.handleCommand("/sessions")
	m.modal.Selected = 1 // sess-1

	m.deleteSelectedSession()

	if len(fake.deleted) != 1 || fake.deleted[0] != "sess-1" {
		t.Fatalf("deleted = %v, want [sess-1]", fake.deleted)
	}
	if m.modal == nil || m.modal.Type != ModalSessions {
		t.Fatal("expected sessions modal still open")
	}
	// Remaining: New + sess-2 + Back.
	if len(m.modal.Items) != 3 {
		t.Errorf("items after delete = %v, want 3", m.modal.Items)
	}
	if !strings.Contains(m.modal.Items[1], "sess-2") {
		t.Errorf("remaining item = %q, want sess-2", m.modal.Items[1])
	}
}

func TestSessionModal_DeleteLastCloses(t *testing.T) {
	fake := &fakeSessionLister{
		sessions: []*session.Session{{ID: "sess-only"}},
	}
	m := &Model{
		width:      80,
		height:     24,
		userID:     "user-1",
		sessionMgr: fake,
	}
	m.handleCommand("/sessions")
	m.modal.Selected = 1 // the only session

	m.deleteSelectedSession()

	if m.modal != nil {
		t.Fatal("expected modal closed when no sessions remain")
	}
	if m.status != "No stored sessions" {
		t.Errorf("status = %q, want No stored sessions", m.status)
	}
}

func TestHandleCommand_Projects_NoManager(t *testing.T) {
	m := &Model{}
	m.handleCommand("/projects")

	if len(m.messages) == 0 {
		t.Fatal("expected a system message")
	}
	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "Project tracking is not available") {
		t.Errorf("expected 'not available' message, got %q", lastMsg.Content)
	}
}

func TestHandleCommand_Projects_Empty(t *testing.T) {
	m := &Model{projectMgr: &fakeProjectLister{}}
	m.handleCommand("/projects")

	if len(m.messages) == 0 {
		t.Fatal("expected a system message")
	}
	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "No tracked projects found") {
		t.Errorf("expected 'No tracked projects found', got %q", lastMsg.Content)
	}
}

func TestHandleCommand_Projects_ListsRecords(t *testing.T) {
	m := &Model{projectMgr: &fakeProjectLister{
		records: []project.ProjectRecord{
			{Path: "/work/demo", SessionID: "abcdef123456", LastInstruction: "Build the feature"},
			{Path: "/work/other", SessionID: "xyz", LastInstruction: ""},
		},
	}}
	m.handleCommand("/projects")

	// C3: /projects opens the selectable ModalProjects instead of a
	// plain-text chat reply.
	if m.modal == nil || m.modal.Type != ModalProjects {
		t.Fatalf("expected ModalProjects open, got %+v", m.modal)
	}
	if len(m.modal.Items) != 2 {
		t.Fatalf("expected 2 items, got %d: %v", len(m.modal.Items), m.modal.Items)
	}
	if !strings.Contains(m.modal.Items[0], "/work/demo") {
		t.Errorf("expected project path in item, got %q", m.modal.Items[0])
	}
	// Long session IDs are truncated to 8 chars in the display item.
	if !strings.Contains(m.modal.Items[0], "abcdef12") {
		t.Errorf("expected truncated session id 'abcdef12', got %q", m.modal.Items[0])
	}
	if m.modal.Selected != 0 {
		t.Errorf("initial Selected = %d, want 0", m.modal.Selected)
	}
}

func TestHandleModalSelection_ProjectsResumes(t *testing.T) {
	m := &Model{
		sessionID: "old-session",
		projectMgr: &fakeProjectLister{
			records: []project.ProjectRecord{
				{Path: "/work/demo", SessionID: "full-session-id-001", LastInstruction: "x"},
			},
		},
	}
	m.handleCommand("/projects")
	if m.modal == nil || m.modal.Type != ModalProjects {
		t.Fatalf("expected ModalProjects open, got %+v", m.modal)
	}

	// Enter on the selected item resumes its full session id.
	m.handleModalSelection()
	if m.modal != nil {
		t.Fatal("expected modal to close after selection")
	}
	if m.sessionID != "full-session-id-001" {
		t.Errorf("sessionID = %q, want %q", m.sessionID, "full-session-id-001")
	}
	// The /resume path clears prior UI state and reports a resume.
	if len(m.messages) == 0 ||
		!strings.Contains(m.messages[len(m.messages)-1].Content, "[Session resumed:") {
		t.Errorf("expected resume notice message, got %d messages", len(m.messages))
	}
}

func TestHandleCommand_Resume_NoArgs(t *testing.T) {
	m := &Model{messages: []chatEntry{{Role: "user", Content: "hi"}}}
	m.handleCommand("/resume")

	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "Usage: /resume <session-id>") {
		t.Errorf("expected usage message, got %q", lastMsg.Content)
	}
	// Messages from before must be untouched.
	if len(m.messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(m.messages))
	}
}

func TestHandleCommand_Resume_WithID(t *testing.T) {
	m := &Model{
		sessionID:     "old-session",
		messages:      []chatEntry{{Role: "user", Content: "old"}},
		toolCalls:     []toolCallEntry{{Name: "tool", Status: "running"}},
		auditLog:      []toolAuditEntry{{Name: "tool"}},
		currentStream: "streaming",
		streaming:     true,
	}
	m.handleCommand("/resume abc-def-123456")

	if m.sessionID != "abc-def-123456" {
		t.Errorf("sessionID = %q, want abc-def-123456", m.sessionID)
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected messages reset to 1 system entry, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].Content, "[Session resumed: abc-def-123456]") {
		t.Errorf("expected resumed marker, got %q", m.messages[0].Content)
	}
	if len(m.toolCalls) != 0 || len(m.auditLog) != 0 {
		t.Error("tool calls and audit log should be cleared on resume")
	}
	if m.currentStream != "" || m.streaming {
		t.Error("stream state should be reset on resume")
	}
	if !strings.HasPrefix(m.status, "Resumed session abc-def-1") {
		t.Errorf("status = %q, want Resumed session prefix", m.status)
	}
}

func TestHandleCommand_Resume_TrailingSpace(t *testing.T) {
	m := &Model{
		sessionID: "keep-me",
		messages:  []chatEntry{{Role: "user", Content: "old"}},
	}
	// "/resume " is trimmed to "/resume", so it falls into the usage
	// branch: the session must NOT be switched and a usage hint is
	// appended (the empty-id branch is defensive-only dead code).
	m.handleCommand("/resume ")

	if m.sessionID != "keep-me" {
		t.Error("sessionID should be untouched for resume with no id")
	}
	if len(m.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(m.messages))
	}
	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "Usage: /resume <session-id>") {
		t.Errorf("expected usage hint, got %q", lastMsg.Content)
	}
}

func TestHandleCommand_ModelSwitch(t *testing.T) {
	cfg := &config.WukongConfig{
		DefaultProvider: "test-provider",
		Providers: []config.ProviderConfig{
			{Name: "test-provider", Model: "gpt-4"},
		},
	}
	m := &Model{cfg: cfg, modelName: "gpt-4"}
	m.handleCommand("/model gpt-4o")

	p := cfg.DefaultProviderConfig()
	if p == nil || p.Model != "gpt-4o" {
		t.Errorf("cfg model = %v, want gpt-4o", p)
	}
	if m.modelName != "gpt-4o" {
		t.Errorf("modelName = %q, want gpt-4o", m.modelName)
	}
	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "Switched model: gpt-4 -> gpt-4o") {
		t.Errorf("expected switch confirmation, got %q", lastMsg.Content)
	}
}

func TestHandleCommand_ModelSwitch_NoProvider(t *testing.T) {
	cfg := &config.WukongConfig{
		DefaultProvider: "missing",
		Providers:       []config.ProviderConfig{{Name: "other", Model: "x"}},
	}
	m := &Model{cfg: cfg, modelName: "keep"}
	m.handleCommand("/model something")

	if m.modelName != "keep" {
		t.Errorf("modelName should be untouched, got %q", m.modelName)
	}
	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "No provider configured") {
		t.Errorf("expected 'No provider configured', got %q", lastMsg.Content)
	}
}

func TestHandleCommand_Exts_EmptyConfig(t *testing.T) {
	m := &Model{cfg: &config.WukongConfig{}}
	m.handleCommand("/exts")

	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "No extensions loaded.") {
		t.Errorf("expected 'No extensions loaded.', got %q", lastMsg.Content)
	}
}

func TestHandleCommand_Audit_Empty(t *testing.T) {
	m := &Model{}
	m.handleCommand("/audit")

	lastMsg := m.messages[len(m.messages)-1]
	if !strings.Contains(lastMsg.Content, "No tool audit entries yet.") {
		t.Errorf("expected empty audit message, got %q", lastMsg.Content)
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

// --- P3.1 incremental streaming-render helpers ---

func TestFenceCount(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"no fences", "hello\nworld", 0},
		{"one open", "```go\ncode", 1},
		{"closed", "```go\ncode\n```", 2},
		{"indented", "  ```\n  ~~~  \n", 2},
		{"tilde", "~~~\ntext\n~~~", 2},
	}
	for _, c := range cases {
		if got := fenceCount(c.in); got != c.want {
			t.Errorf("%s: fenceCount(%q) = %d, want %d", c.name, c.in, got, c.want)
		}
	}
}

func TestSafeIncrementalTail(t *testing.T) {
	cases := []struct {
		name string
		tail string
		want bool
	}{
		{"empty", "", false},
		{"plain text", "growing line", true},
		{"open fence", "```go\ncode", false},
		{"closed fence", "```go\ncode\n```", true},
		{"closed then text", "```\nx\n```\ngrowing", true},
	}
	for _, c := range cases {
		if got := safeIncrementalTail(c.tail); got != c.want {
			t.Errorf("%s: safeIncrementalTail(%q) = %v, want %v", c.name, c.tail, got, c.want)
		}
	}
}

func TestLastSafeSplit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"no blank line", "line1\nline2", 0},
		{"blank line with content after", "head\n\nbody", 6},
		{"multiple blanks picks last", "a\n\nb\n\nc", 6},
		{"split before open fence", "head\n\n```go\ncode", 0},
		{"split after closed fence", "head\n\n```go\ncode\n```\n\nmore", 22},
		{"blank inside open fence not safe", "```\n\ntext\n```", 0},
	}
	for _, c := range cases {
		if got := lastSafeSplit(c.in); got != c.want {
			t.Errorf("%s: lastSafeSplit(%q) = %d, want %d", c.name, c.in, got, c.want)
		}
	}
}

func TestLastSafeSplit_PrefixOddFence(t *testing.T) {
	// Blank line inside an open code block: the tail has an odd fence
	// count (open), prefix has odd too. Split must not happen at the
	// last blank because tail is odd; but an earlier split must be
	// considered. Here the only blank is inside the fence, so result 0.
	in := "```\n\ntext"
	if got := lastSafeSplit(in); got != 0 {
		t.Errorf("lastSafeSplit inside open fence = %d, want 0", got)
	}
}

func TestResetStreamCache_Invalidates(t *testing.T) {
	m := &Model{
		streamCachePrefixIdx: 12,
		streamCacheRendered:  "prefix",
		streamCacheValid:     true,
	}
	m.resetStreamCache()
	if m.streamCacheValid {
		t.Error("expected cache invalid after reset")
	}
	if m.streamCachePrefixIdx != 0 || m.streamCacheRendered != "" {
		t.Error("expected cache fields zeroed after reset")
	}
}

// --- P3.2 tool result collapse/expand ---

func TestToolCallStart_DefaultCollapsed(t *testing.T) {
	m := &Model{}

	_, _ = m.Update(toolCallStartMsg{
		Name: "file_read",
		Args: `{"path":"/tmp/x"}`,
	})

	if len(m.toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(m.toolCalls))
	}
	if !m.toolCalls[0].Collapsed {
		t.Error("new tool call should default to Collapsed=true")
	}
	if m.toolCalls[0].Status != "running" {
		t.Errorf("expected running status, got %q", m.toolCalls[0].Status)
	}
}

func TestToolCallResult_ToggleExpanded(t *testing.T) {
	m := &Model{}
	_, _ = m.Update(toolCallStartMsg{
		Name: "file_read",
		Args: `{"path":"/tmp/x"}`,
	})
	_, _ = m.Update(toolCallResultMsg{
		Name:   "file_read",
		Result: "file content here",
	})

	if m.toolCalls[0].Result == "" {
		t.Fatal("expected result attached")
	}
	if !m.toolCalls[0].Collapsed {
		t.Error("should start collapsed even with result")
	}

	// Enter expands.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.toolCalls[0].Collapsed {
		t.Error("expected Enter to expand the result")
	}
	if cmd != nil {
		t.Error("Enter on a tool should not submit input")
	}

	// Enter again collapses.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.toolCalls[0].Collapsed {
		t.Error("expected second Enter to collapse")
	}
	if cmd != nil {
		t.Error("toggle should not produce a command")
	}
}

func TestToolCallResult_EnterWithoutResult_Submits(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	ta.Focus()
	ta.SetValue("say hi")
	m := &Model{textarea: ta}
	_, _ = m.Update(toolCallStartMsg{
		Name: "file_read",
		Args: `{}`,
	})
	// toolCallStartMsg triggers readStreamEvent with a nil streamCh —
	// ensure no command is pending from it.

	// Running tool (Result == "") + Enter must NOT toggle; Enter is
	// not a submit key in this UI (Ctrl+D submits), so fall through
	// must leave the tool expanded-state untouched.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("expected no command (Enter is not the submit key)")
	}
	if !m.toolCalls[0].Collapsed {
		t.Error("running tool should remain collapsed")
	}
}

func TestToolCall_EnterTogglesSelected(t *testing.T) {
	m := &Model{}
	// Two tools; select the second one via Tab, then toggle.
	for _, name := range []string{"t1", "t2"} {
		_, _ = m.Update(toolCallStartMsg{Name: name, Args: `{}`})
		_, _ = m.Update(toolCallResultMsg{Name: name, Result: "res-" + name})
	}

	m.toolSelectedIdx = 1
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if m.toolCalls[1].Collapsed {
		t.Error("expected selected second tool to be expanded")
	}
	if !m.toolCalls[0].Collapsed {
		t.Error("unselected first tool must stay collapsed")
	}
	if cmd != nil {
		t.Error("expected no submit command")
	}
}

func TestToolCall_EnterNoTools_Submits(t *testing.T) {
	ta := textarea.New()
	ta.SetHeight(3)
	ta.Focus()
	ta.SetValue("hello")
	m := &Model{textarea: ta}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("Enter without tools should still not submit (Ctrl+D is submit)")
	}
}

func TestToolCall_TabNavigatesAndWraps(t *testing.T) {
	m := &Model{}
	// Two finished tools, default Collapsed=true.
	_, _ = m.Update(toolCallStartMsg{Name: "t1", Args: `{}`})
	_, _ = m.Update(toolCallResultMsg{Name: "t1", Result: "r1"})
	_, _ = m.Update(toolCallStartMsg{Name: "t2", Args: `{}`})
	_, _ = m.Update(toolCallResultMsg{Name: "t2", Result: "r2"})

	if m.toolSelectedIdx != 0 {
		t.Fatalf("initial selection should be 0, got %d", m.toolSelectedIdx)
	}

	// Tab moves forward (does NOT toggle collapse).
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.toolSelectedIdx != 1 {
		t.Errorf("Tab should move to index 1, got %d", m.toolSelectedIdx)
	}

	// Tab wraps to 0.
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.toolSelectedIdx != 0 {
		t.Errorf("Tab should wrap to 0, got %d", m.toolSelectedIdx)
	}

	// Shift+Tab moves back to the end.
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.toolSelectedIdx != 1 {
		t.Errorf("Shift+Tab should wrap to 1, got %d", m.toolSelectedIdx)
	}
}

func TestRenderToolCallResult_ExpandedNoTruncation(t *testing.T) {
	// Long result (>500 chars) must be fully present in the render,
	// wrapped at the given width — no silent cut.
	long := strings.Repeat("0123456789", 100) // 1000 chars
	out := RenderToolCallResult(toolCallEntry{
		Name:      "web_search",
		Args:      `{"q":"x"}`,
		Status:    "done",
		Result:    long,
		Collapsed: false,
	}, false, 40)

	// The renderer hard-wraps at width and pads each line, so the
	// long string is split across lines with inserted spaces. Strip
	// ANSI escapes, newlines and padding, then verify the complete
	// result content survives (no truncation like the old 500-char cut).
	plain := stripANSI(out)
	plain = strings.ReplaceAll(plain, "\n", "")
	plain = strings.ReplaceAll(plain, " ", "")
	if !strings.Contains(plain, long) {
		t.Error("expanded render must contain the full result (no truncation)")
	}
	if strings.Contains(plain, "...") {
		t.Error("expanded render should not contain truncation markers")
	}
	// Header, args preserved.
	if !strings.Contains(plain, "web_search") {
		t.Error("header with tool name missing")
	}
}

func TestRenderToolCallResult_CollapsedHint(t *testing.T) {
	out := RenderToolCallResult(toolCallEntry{
		Name:      "web_search",
		Status:    "done",
		Result:    strings.Repeat("x", 123),
		Collapsed: true,
	}, false, 40)

	if strings.Contains(out, strings.Repeat("x", 123)) {
		t.Error("collapsed render must not include the result body")
	}
	if !strings.Contains(out, "123 chars hidden") {
		t.Errorf("collapsed render must show char count hint, got:\n%s", out)
	}
	if !strings.Contains(out, "Enter to expand") {
		t.Errorf("collapsed hint should mention Enter, got:\n%s", out)
	}
}

func TestRenderToolCallResult_RunningHeader(t *testing.T) {
	out := RenderToolCallResult(toolCallEntry{
		Name:      "file_read",
		Status:    "running",
		Collapsed: true,
	}, true, 40)

	if !strings.Contains(out, "file_read") {
		t.Error("running header should show tool name")
	}
	if !strings.Contains(out, "▌") {
		t.Error("selected indicator missing")
	}
}

// --- Phase 4.1: modal keyboard navigation ---

const testCommandsItems = 12 // openCommandsModal() item count

// openTestCommandsModal builds a model that can host the commands modal
// and opens it, returning the updated model.
func openTestCommandsModal() *Model {
	m := &Model{
		width:  80,
		height: 24,
	}
	m.openCommandsModal()
	return m
}

func TestCommandsModal_ArrowKeysNavigate(t *testing.T) {
	m := openTestCommandsModal()
	if m.modal == nil || m.modal.Type != ModalCommands {
		t.Fatal("expected commands modal to be open")
	}
	if m.modal.Selected != 0 {
		t.Fatalf("initial Selected = %d, want 0", m.modal.Selected)
	}

	// Down moves selection forward and stays inside bounds.
	for want := 1; want <= testCommandsItems-2; want++ {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		if cmd != nil {
			t.Fatalf("Down at %d produced a command, want nil", want)
		}
		if m.modal.Selected != want {
			t.Fatalf("Selected after Down #%d = %d, want %d",
				want, m.modal.Selected, want)
		}
	}

	// Up moves back.
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.modal.Selected != testCommandsItems-3 {
		t.Errorf("Selected after Up = %d, want %d",
			m.modal.Selected, testCommandsItems-3)
	}
}

func TestCommandsModal_NavigationClampedAtEdges(t *testing.T) {
	m := openTestCommandsModal()

	// Overshoot the bottom: selection must clamp at the last item.
	for i := 0; i < testCommandsItems+5; i++ {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.modal.Selected != testCommandsItems-1 {
		t.Errorf("Selected after overshoot = %d, want %d",
			m.modal.Selected, testCommandsItems-1)
	}

	// Overshoot the top: clamp at first item.
	for i := 0; i < testCommandsItems+5; i++ {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.modal.Selected != 0 {
		t.Errorf("Selected after Up overshoot = %d, want 0",
			m.modal.Selected)
	}
}

func TestCommandsModal_EnterExecutesSelectedCommand(t *testing.T) {
	m := openTestCommandsModal()

	// Select /clear (index 1), press Enter: modal closes and the
	// command handler clears the conversation.
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.messages = append(m.messages, chatEntry{Role: "user", Content: "hello"})
	m.toolCalls = append(m.toolCalls, toolCallEntry{Name: "x"})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("Enter in commands modal should not produce a command")
	}
	if m.modal != nil {
		t.Error("Enter in commands modal should close it")
	}
	if len(m.messages) != 0 {
		t.Errorf("messages after /clear = %d, want 0", len(m.messages))
	}
	if len(m.toolCalls) != 0 {
		t.Errorf("toolCalls after /clear = %d, want 0", len(m.toolCalls))
	}
	if m.status != "Cleared" {
		t.Errorf("status after /clear = %q, want Cleared", m.status)
	}
}

func TestCommandsModal_EnterOnNewSwitchesSession(t *testing.T) {
	m := openTestCommandsModal()
	m.sessionID = "old-session"

	// Index 0 is /new.
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if m.modal != nil {
		t.Error("Enter on /new should close the modal")
	}
	if m.sessionID == "" || m.sessionID == "old-session" {
		t.Errorf("sessionID after /new = %q, want a fresh value", m.sessionID)
	}
	if m.status != "New session started" {
		t.Errorf("status after /new = %q", m.status)
	}
}

func TestCommandsModal_EscClosesWithoutAction(t *testing.T) {
	m := openTestCommandsModal()
	m.messages = append(m.messages, chatEntry{Role: "user", Content: "hello"})

	for i := 0; i < 3; i++ {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if cmd != nil {
		t.Error("Esc should not produce a command")
	}
	if m.modal != nil {
		t.Error("Esc should close the modal")
	}
	// No command was executed: conversation untouched.
	if len(m.messages) != 1 {
		t.Errorf("messages after Esc = %d, want 1 (untouched)",
			len(m.messages))
	}
}

func TestSkillsModal_SelectsSkill(t *testing.T) {
	m := &Model{
		width:  80,
		height: 24,
		cfg: &config.WukongConfig{
			Extensions: []config.ExtensionConfig{
				{Name: "file_reader", Enabled: true},
				{Name: "web_search", Enabled: true},
				{Name: "disabled_ext", Enabled: false},
			},
		},
	}
	m.openSkillsModal()
	if m.modal == nil || m.modal.Type != ModalSkills {
		t.Fatal("expected skills modal to be open")
	}
	if len(m.modal.Items) != 2 {
		t.Fatalf("items = %v, want only enabled extensions", m.modal.Items)
	}

	// Down to the second skill, Enter selects it.
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if m.modal != nil {
		t.Error("Enter should close the skills modal")
	}
	if m.skillName != "web_search" {
		t.Errorf("skillName = %q, want web_search", m.skillName)
	}
	if m.status != "Skill: web_search" {
		t.Errorf("status = %q, want Skill: web_search", m.status)
	}
}

func TestSkillsModal_NoSkillsFallback(t *testing.T) {
	m := &Model{width: 80, height: 24} // nil cfg → no extensions
	m.openSkillsModal()
	if len(m.modal.Items) != 1 || m.modal.Items[0] != "No skills loaded" {
		t.Errorf("fallback items = %v", m.modal.Items)
	}
}

func TestModal_SettingsEnterCloses(t *testing.T) {
	m := &Model{
		width:  80,
		height: 24,
		cfg: &config.WukongConfig{
			LogLevel: "info",
			Memory:   config.MemoryConfig{Backend: "sqlite"},
			Session:  config.SessionConfig{Backend: "memory"},
			Recall:   config.RecallConfig{Enabled: false},
		},
		providerName: "p",
		modelName:    "m",
	}
	m.openSettingsModal()
	if m.modal == nil || m.modal.Type != ModalSettings {
		t.Fatal("expected settings modal to be open")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("settings Enter should not produce a command")
	}
	if m.modal != nil {
		t.Error("settings Enter should close the modal")
	}
}

func TestModal_PgUpPgDownScroll(t *testing.T) {
	// Small window (modalHeight=6 → maxVisible=3) over 11 items so
	// Scroll actually moves.
	m := &Model{width: 40, height: 12}
	m.modal = &modalState{
		Type:  ModalCommands,
		Title: "cmd",
		Items: []string{
			"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10",
		},
	}
	m.layoutModal(testCommandsItems)
	if m.modalHeight != 6 {
		t.Fatalf("modalHeight = %d, want 6 (clamped)", m.modalHeight)
	}

	// PgDown advances the scroll offset.
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if m.modal.Scroll != 2 {
		t.Errorf("Scroll after 2×PgDown = %d, want 2", m.modal.Scroll)
	}

	// PgUp moves back.
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.modal.Scroll != 1 {
		t.Errorf("Scroll after PgUp = %d, want 1", m.modal.Scroll)
	}

	// PgUp clamps at 0.
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.modal.Scroll != 0 {
		t.Errorf("Scroll after PgUp overshoot = %d, want 0", m.modal.Scroll)
	}
}

func TestModal_SelectionFollowsIntoVisibleWindow(t *testing.T) {
	// maxVisible = 3 → selection beyond index 3 must pull Scroll along.
	m := &Model{width: 40, height: 12}
	items := make([]string, testCommandsItems)
	for i := range items {
		items[i] = fmt.Sprintf("cmd-%d", i)
	}
	m.modal = &modalState{Type: ModalCommands, Title: "cmd", Items: items}
	m.layoutModal(len(items)) // modalHeight = 6, maxVisible = 3

	// Drop down to index 8; Scroll must follow so the selection is
	// visible: Scroll = Selected - maxVisible + 1 = 6.
	for i := 0; i < 8; i++ {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.modal.Selected != 8 {
		t.Fatalf("Selected = %d, want 8", m.modal.Selected)
	}
	if m.modal.Scroll != 6 {
		t.Errorf("Scroll after tracking = %d, want 6", m.modal.Scroll)
	}

	// Jump back to the top; Scroll must collapse to keep it visible.
	for i := 0; i < 8; i++ {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.modal.Selected != 0 {
		t.Fatalf("Selected = %d, want 0", m.modal.Selected)
	}
	if m.modal.Scroll != 0 {
		t.Errorf("Scroll after returning to top = %d, want 0", m.modal.Scroll)
	}
}

func TestModal_PgUpDoesNotMoveSelection(t *testing.T) {
	m := &Model{width: 40, height: 12}
	items := make([]string, testCommandsItems)
	for i := range items {
		items[i] = fmt.Sprintf("cmd-%d", i)
	}
	m.modal = &modalState{Type: ModalCommands, Title: "cmd", Items: items}
	m.layoutModal(len(items))

	// Select item 5, then PgUp/Down: selection must not move.
	for i := 0; i < 5; i++ {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.modal.Selected != 5 {
		t.Errorf("PgUp/PgDown moved selection to %d, want 5",
			m.modal.Selected)
	}
}

func TestCommandsModal_UnchangedNonNavKey(t *testing.T) {
	m := openTestCommandsModal()
	// Ctrl+D (submit) while a modal is open must be swallowed, not
	// forwarded to the command/submit path.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd != nil {
		t.Error("Ctrl+D with modal open should not produce a command")
	}
	if m.modal == nil {
		t.Error("non-nav key should keep the modal open")
	}
}

func TestRenderModal_ScrollHint(t *testing.T) {
	items := make([]string, testCommandsItems)
	for i := range items {
		items[i] = fmt.Sprintf("cmd-%d", i)
	}
	modal := &modalState{
		Type:  ModalCommands,
		Title: "Available Commands",
		Items: items,
	}

	// 4 rows → maxVisible = 1 → only one item and a scroll hint.
	out := stripANSI(RenderModal(modal, 40, 4))
	if !strings.Contains(out, "cmd-0") {
		t.Error("first item missing from rendered modal")
	}
	if strings.Contains(out, "cmd-1") {
		t.Error("second item should be scrolled out at height 4")
	}
	if !strings.Contains(out, "[1/12") {
		t.Errorf("scroll hint missing, got:\n%s", out)
	}
}

func TestRenderModal_ClampsOverscroll(t *testing.T) {
	items := make([]string, testCommandsItems)
	for i := range items {
		items[i] = fmt.Sprintf("cmd-%d", i)
	}
	modal := &modalState{
		Type:     ModalCommands,
		Title:    "Available Commands",
		Items:    items,
		Selected: testCommandsItems - 1, // last item
		Scroll:   50,                    // invalid: beyond the last visible position
	}

	out := stripANSI(RenderModal(modal, 40, 4))
	if !strings.Contains(out, "cmd-11") {
		t.Errorf("overscrolled modal must clamp to the last item, got:\n%s", out)
	}
	if !strings.Contains(out, "[12/12") {
		t.Errorf("clamped scroll hint missing, got:\n%s", out)
	}
}

func TestRenderModal_NilSafe(t *testing.T) {
	if got := RenderModal(nil, 40, 10); got != "" {
		t.Errorf("RenderModal(nil) = %q, want empty", got)
	}
}

func TestRenderModal_ContentOnly(t *testing.T) {
	// Settings-style modal uses Content instead of Items.
	modal := &modalState{
		Type:    ModalSettings,
		Title:   "Settings",
		Content: "Provider: p\nModel: m",
	}
	out := stripANSI(RenderModal(modal, 40, 6))
	if !strings.Contains(out, "Provider: p") {
		t.Errorf("content modal should render Content, got:\n%s", out)
	}
}
