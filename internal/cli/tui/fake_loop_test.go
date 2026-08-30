package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// --- Phase 4.2/4.3: fake loop.Run streaming tests ---

// fakeLoop implements loopRunner with pre-built events so tests can
// drive the whole sendMessage -> streamEvent -> tea.Msg pipeline
// without a real agent stack.
type fakeLoop struct {
	events []*event.Event
	err    error
}

func (f *fakeLoop) Run(ctx context.Context, userID, sessionID string, message model.Message) (<-chan *event.Event, error) {
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan *event.Event, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// Event constructors (same shapes used by repl_test.go).

func streamDeltaEvent(content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{Delta: model.Message{Content: content}},
			},
		},
	}
}

func streamToolCallEvent(name, args string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						ToolCalls: []model.ToolCall{
							{Function: model.FunctionDefinitionParam{
								Name:      name,
								Arguments: []byte(args),
							}},
						},
					},
				},
			},
		},
	}
}

func streamToolResultEvent(toolName, content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Object: "tool.response",
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:     "tool",
						ToolName: toolName,
						Content:  content,
					},
				},
			},
		},
	}
}

func streamCompletionEvent() *event.Event {
	return &event.Event{
		Response: &model.Response{
			Done:   true,
			Object: model.ObjectTypeRunnerCompletion,
		},
	}
}

func streamErrorEvent(message string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: message},
		},
	}
}

// pumpStream sends the input through sendMessage and drives the
// resulting tea.Cmd chain through Model.Update until the stream ends,
// returning every tea.Msg the TUI loop observed. It decouples the
// assertion from the streamCh bookkeeping.
func pumpStream(t *testing.T, m *Model, input string) []tea.Msg {
	t.Helper()
	cmd := m.sendMessage(input)
	if cmd == nil {
		t.Fatal("sendMessage returned nil cmd")
	}
	var msgs []tea.Msg
	for guard := 0; cmd != nil; guard++ {
		if guard > 200 {
			t.Fatal("stream pump did not terminate")
		}
		msg := cmd()
		msgs = append(msgs, msg)
		_, cmd = m.Update(msg)
	}
	return msgs
}

// newStreamModel returns a Model wired to the fake loop.
func newStreamModel(fake *fakeLoop) *Model {
	return &Model{
		loop:     fake,
		width:    80,
		height:   24,
		viewport: viewport.New(80, 40),
	}
}

// --- 4.2: fake loop.Run async stream routing ---

func TestStreamRoute_DeltaContent(t *testing.T) {
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamDeltaEvent("Hello"),
		streamDeltaEvent(" world"),
		streamCompletionEvent(),
	}})

	msgs := pumpStream(t, m, "hi")

	if len(msgs) != 3 {
		t.Fatalf("got %d msgs, want 3 (2 deltas + end)", len(msgs))
	}
	if d, ok := msgs[0].(streamingDeltaMsg); !ok || string(d) != "Hello" {
		t.Errorf("msgs[0] = %#v, want streamingDeltaMsg(Hello)", msgs[0])
	}
	if d, ok := msgs[1].(streamingDeltaMsg); !ok || string(d) != " world" {
		t.Errorf("msgs[1] = %#v, want streamingDeltaMsg( world)", msgs[1])
	}
	end, ok := msgs[2].(streamEndMsg)
	if !ok {
		t.Fatalf("msgs[2] = %#v, want streamEndMsg", msgs[2])
	}
	if end.Content != "Hello world" {
		t.Errorf("streamEndMsg.Content = %q, want accumulated content", end.Content)
	}
	if m.streaming {
		t.Error("streaming flag should be cleared after end")
	}
}

func TestStreamRoute_ToolCallAndResult(t *testing.T) {
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamToolCallEvent("file_read", `{"path":"/tmp/x"}`),
		streamToolResultEvent("file_read", "file content here"),
		streamCompletionEvent(),
	}})

	msgs := pumpStream(t, m, "read it")

	var start *toolCallStartMsg
	var result *toolCallResultMsg
	for _, msg := range msgs {
		switch v := msg.(type) {
		case toolCallStartMsg:
			start = &v
		case toolCallResultMsg:
			result = &v
		}
	}
	if start == nil || start.Name != "file_read" ||
		start.Args != `{"path":"/tmp/x"}` {
		t.Errorf("toolCallStartMsg = %#v, want file_read with args", start)
	}
	if result == nil || result.Name != "file_read" ||
		result.Result != "file content here" {
		t.Errorf("toolCallResultMsg = %#v, want file_read result", result)
	}

	// TUI entry lifecycle: started running, then matched and done.
	if len(m.toolCalls) != 1 {
		t.Fatalf("toolCalls = %d, want 1", len(m.toolCalls))
	}
	if m.toolCalls[0].Name != "file_read" || m.toolCalls[0].Status != "done" {
		t.Errorf("toolCalls[0] = %#v, want done file_read entry", m.toolCalls[0])
	}
	if m.toolCalls[0].Result != "file content here" {
		t.Errorf("toolCalls[0].Result = %q", m.toolCalls[0].Result)
	}
	if len(m.auditLog) != 1 {
		t.Errorf("auditLog = %d entries, want 1", len(m.auditLog))
	}
}

func TestStreamRoute_ErrorEvent(t *testing.T) {
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamErrorEvent("boom something broke"),
		streamCompletionEvent(),
	}})

	msgs := pumpStream(t, m, "hi")

	var errMsg streamingErrorMsg
	found := false
	for _, msg := range msgs {
		if v, ok := msg.(streamingErrorMsg); ok {
			errMsg = v
			found = true
		}
	}
	if !found {
		t.Fatalf("no streamingErrorMsg in %#v", msgs)
	}
	if !strings.Contains(string(errMsg), "boom something broke") {
		t.Errorf("streamingErrorMsg = %q, want to contain the error", string(errMsg))
	}
	// Error is surfaced as a system message.
	if len(m.messages) == 0 ||
		m.messages[len(m.messages)-1].Role != "system" ||
		!strings.Contains(m.messages[len(m.messages)-1].Content,
			"boom something broke") {
		t.Errorf("system message with error missing: %#v", m.messages)
	}
}

func TestStreamRoute_LoopRunError(t *testing.T) {
	m := newStreamModel(&fakeLoop{err: context.DeadlineExceeded})

	msgs := pumpStream(t, m, "hi")

	found := false
	for _, msg := range msgs {
		if v, ok := msg.(streamingErrorMsg); ok &&
			strings.Contains(string(v), "timed out") {
			found = true
		}
	}
	if !found {
		t.Errorf("friendly timeout error not surfaced, msgs = %#v", msgs)
	}
}

func TestStreamRoute_ToolResultWithoutQueueIsSurfaced(t *testing.T) {
	// A tool.response arrives with no preceding tool call: must not be
	// silently dropped — NoToolResult streamEvent becomes a delta note.
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamToolResultEvent("orphan", "stray result"),
		streamCompletionEvent(),
	}})

	msgs := pumpStream(t, m, "hi")

	found := false
	for _, msg := range msgs {
		if d, ok := msg.(streamingDeltaMsg); ok &&
			strings.Contains(string(d), "Tool result empty") {
			found = true
		}
	}
	if !found {
		t.Errorf("NoToolResult note missing, msgs = %#v", msgs)
	}
}

func TestStreamRoute_ToolResultEmptyContent(t *testing.T) {
	// 1.2: empty tool results render "(no content)" instead of being
	// dropped.
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamToolCallEvent("file_read", `{}`),
		streamToolResultEvent("file_read", ""),
		streamCompletionEvent(),
	}})

	msgs := pumpStream(t, m, "read")

	var result *toolCallResultMsg
	for _, msg := range msgs {
		if v, ok := msg.(toolCallResultMsg); ok {
			result = &v
		}
	}
	if result == nil || result.Result != "(no content)" {
		t.Errorf("empty result = %#v, want (no content)", result)
	}
}

func TestStreamRoute_DeltaAfterToolCall(t *testing.T) {
	// Full mixed flow: tool call, its result, then final text.
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamDeltaEvent("Let me check"),
		streamToolCallEvent("web_search", `{"q":"weather"}`),
		streamToolResultEvent("web_search", "sunny"),
		streamDeltaEvent(" done."),
		streamCompletionEvent(),
	}})

	msgs := pumpStream(t, m, "weather?")

	order := []string{}
	for _, msg := range msgs {
		switch v := msg.(type) {
		case streamingDeltaMsg:
			order = append(order, "delta:"+string(v))
		case toolCallStartMsg:
			order = append(order, "start:"+v.Name)
		case toolCallResultMsg:
			order = append(order, "result:"+v.Name)
		}
	}
	want := []string{
		"delta:Let me check",
		"start:web_search",
		"result:web_search",
		"delta: done.",
	}
	if len(order) != len(want) {
		t.Fatalf("stream order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("stream[%d] = %q, want %q", i, order[i], want[i])
		}
	}
}

// --- 4.3: tool.response FIFO resultQueue routing ---

func TestResultQueueFIFO_StartOrderReturn(t *testing.T) {
	// Both tools started before either returns: the queue binds the
	// first result to the first-started call (start order).
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamToolCallEvent("web_search", `{"q":"a"}`),
		streamToolCallEvent("file_read", `{"p":"/x"}`),
		streamToolResultEvent("web_search", "res-web"),
		streamToolResultEvent("file_read", "res-file"),
		streamCompletionEvent(),
	}})

	msgs := pumpStream(t, m, "go")

	var results []toolCallResultMsg
	for _, msg := range msgs {
		if v, ok := msg.(toolCallResultMsg); ok {
			results = append(results, v)
		}
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v, want 2", results)
	}
	if results[0].Name != "web_search" || results[0].Result != "res-web" {
		t.Errorf("results[0] = %#v, want web_search start-order bound", results[0])
	}
	if results[1].Name != "file_read" || results[1].Result != "res-file" {
		t.Errorf("results[1] = %#v, want file_read", results[1])
	}

	// Both TUI entries settle to done — no leftover running tools.
	for i, tc := range m.toolCalls {
		if tc.Status != "done" {
			t.Errorf("toolCalls[%d].Status = %q, want done", i, tc.Status)
		}
	}
}

func TestResultQueueFIFO_OutOfOrderCompletion(t *testing.T) {
	// The later-started tool returns first. FIFO must still dequeue in
	// start order (first-started call receives the first-arriving
	// result), and every call must settle to done — nothing dropped.
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamToolCallEvent("web_search", `{"q":"a"}`),
		streamToolCallEvent("file_read", `{"p":"/x"}`),
		streamToolResultEvent("file_read", "res-file-first"),
		streamToolResultEvent("web_search", "res-web-second"),
		streamCompletionEvent(),
	}})

	msgs := pumpStream(t, m, "go")

	var results []toolCallResultMsg
	for _, msg := range msgs {
		if v, ok := msg.(toolCallResultMsg); ok {
			results = append(results, v)
		}
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v, want 2", results)
	}
	// Strict FIFO: results are attributed in START order regardless of
	// arrival order, so the queue pops web_search first.
	if results[0].Name != "web_search" {
		t.Errorf("results[0].Name = %q, want web_search (FIFO start order)",
			results[0].Name)
	}
	if results[1].Name != "file_read" {
		t.Errorf("results[1].Name = %q, want file_read", results[1].Name)
	}

	// Every started call received exactly one result.
	if len(m.toolCalls) != 2 {
		t.Fatalf("toolCalls = %d, want 2", len(m.toolCalls))
	}
	for i, tc := range m.toolCalls {
		if tc.Status != "done" {
			t.Errorf("toolCalls[%d].Status = %q, want done", i, tc.Status)
		}
		if tc.Result == "" {
			t.Errorf("toolCalls[%d].Result empty — result lost", i)
		}
	}
}

func TestResultQueueFIFO_ResultArrivesBeforeLaterCall(t *testing.T) {
	// A result may return while the queue has older unresolved calls:
	// it binds to the oldest outstanding call, never to the newest.
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamToolCallEvent("web_search", `{}`),
		streamToolResultEvent("web_search", "first-result"),
		streamToolCallEvent("file_read", `{}`),
		streamToolResultEvent("file_read", "second-result"),
		streamCompletionEvent(),
	}})

	msgs := pumpStream(t, m, "go")

	var results []toolCallResultMsg
	for _, msg := range msgs {
		if v, ok := msg.(toolCallResultMsg); ok {
			results = append(results, v)
		}
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v, want 2", results)
	}
	if results[0].Name != "web_search" || results[0].Result != "first-result" {
		t.Errorf("results[0] = %#v, want web_search", results[0])
	}
	if results[1].Name != "file_read" || results[1].Result != "second-result" {
		t.Errorf("results[1] = %#v, want file_read", results[1])
	}
}

func TestResultQueueFIFO_TUIDedupeByName(t *testing.T) {
	// Two concurrent calls of the SAME tool: the TUI side matches the
	// FIRST running entry with the same name, mirroring the queue.
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamToolCallEvent("same_tool", `{"n":1}`),
		streamToolCallEvent("same_tool", `{"n":2}`),
		streamToolResultEvent("same_tool", "r1"),
		streamToolResultEvent("same_tool", "r2"),
		streamCompletionEvent(),
	}})

	pumpStream(t, m, "go")

	if len(m.toolCalls) != 2 {
		t.Fatalf("toolCalls = %d, want 2", len(m.toolCalls))
	}
	if m.toolCalls[0].Result != "r1" || m.toolCalls[1].Result != "r2" {
		t.Errorf("same-name results misattributed: %#v", m.toolCalls)
	}
	if m.toolCalls[0].Status != "done" || m.toolCalls[1].Status != "done" {
		t.Errorf("entries must all settle to done: %#v", m.toolCalls)
	}
}

func TestResultQueueFIFO_RedactsSecrets(t *testing.T) {
	// P3.5 integration: secrets are stripped from tool args and results
	// before they reach the tea.Msg stream.
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamToolCallEvent("api_call", `{"key":"sk-12345","q":"x"}`),
		streamToolResultEvent("api_call", "using sk-12345 and sk-12345 again"),
		streamCompletionEvent(),
	}})
	m.secrets = []string{"sk-12345"}

	msgs := pumpStream(t, m, "go")

	for _, msg := range msgs {
		switch v := msg.(type) {
		case toolCallStartMsg:
			if strings.Contains(v.Args, "sk-12345") {
				t.Errorf("args leaked secret: %q", v.Args)
			}
			if !strings.Contains(v.Args, "[REDACTED]") {
				t.Errorf("args not redacted: %q", v.Args)
			}
		case toolCallResultMsg:
			if strings.Contains(v.Result, "sk-12345") {
				t.Errorf("result leaked secret: %q", v.Result)
			}
			if !strings.Contains(v.Result, "[REDACTED]") {
				t.Errorf("result not redacted: %q", v.Result)
			}
		}
	}

	// Stored message and audit must also be clean.
	if m.toolCalls[0].Args != `{"key":"[REDACTED]","q":"x"}` {
		t.Errorf("stored args not redacted: %q", m.toolCalls[0].Args)
	}
	if strings.Contains(m.toolCalls[0].Result, "sk-12345") {
		t.Errorf("stored result leaked secret: %q", m.toolCalls[0].Result)
	}
}

// --- A2: Ctrl+C cancellation full chain ---

// pumpStreamWith runs the sendMessage stream like pumpStream, but after
// processing each message calls the after hook so tests can inject key
// presses (e.g. Ctrl+C) mid-stream. The hook may return a command (e.g.
// the one produced by the Ctrl+C branch of Update) to use for the next
// stream read, verifying the UI keeps consuming after cancellation.
func pumpStreamWith(t *testing.T, m *Model, input string, after func(i int, m *Model) tea.Cmd) []tea.Msg {
	t.Helper()
	cmd := m.sendMessage(input)
	if cmd == nil {
		t.Fatal("sendMessage returned nil cmd")
	}
	var msgs []tea.Msg
	for guard := 0; cmd != nil; guard++ {
		if guard > 200 {
			t.Fatal("stream pump did not terminate")
		}
		msg := cmd()
		msgs = append(msgs, msg)
		var next tea.Cmd
		_, next = m.Update(msg)
		cmd = next
		if after != nil {
			if c := after(len(msgs)-1, m); c != nil {
				cmd = c
			}
		}
	}
	return msgs
}

func TestCtrlC_WhileStreaming_SetsCancelledAndKeepsConsuming(t *testing.T) {
	// While streaming, Ctrl+C must cancel the request, mark cancelled,
	// inject a "Request cancelled" system message, return a
	// readStreamEvent command (so streamCh stays drained — P1.4), and
	// settle cleanly when the stream end arrives.
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamDeltaEvent("partial"),
		streamCompletionEvent(),
	}})

	var ctrlCmd tea.Cmd
	pumpStreamWith(t, m, "hi", func(i int, m *Model) tea.Cmd {
		if i == 0 { // first message (the delta)
			_, ctrlCmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			// Intermediate state, right after the key press.
			if !m.cancelled {
				t.Error("expected cancelled=true after Ctrl+C during streaming")
			}
			if m.status != "Cancelling..." {
				t.Errorf("status = %q, want Cancelling... after Ctrl+C", m.status)
			}
			found := false
			for _, e := range m.messages {
				if e.Role == "system" && strings.Contains(e.Content, "Request cancelled by user") {
					found = true
				}
			}
			if !found {
				t.Errorf("missing 'Request cancelled by user' system message: %#v", m.messages)
			}
		}
		return nil
	})

	if ctrlCmd == nil {
		t.Error("Ctrl+C during streaming must return a command that keeps consuming streamCh")
	}
	// Final settled state after the stream goroutine's end signal.
	if m.streaming {
		t.Error("streaming flag should be cleared after cancel+end")
	}
	if m.cancelled {
		t.Error("cancelled flag should be reset after the stream settled")
	}
	if m.status != "Cancelled" {
		t.Errorf("status = %q, want Cancelled after cancel+end", m.status)
	}
}

func TestCtrlC_DuringStream_StreamEndSettlesCancelled(t *testing.T) {
	// After Ctrl+C the stream goroutine observes the cancellation and
	// emits IsEnd; streamEndMsg must exit the cancel state and surface
	// the "Cancelled" status instead of deadlocking on "Cancelling...".
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamDeltaEvent("first"),
		streamCompletionEvent(),
	}})

	pumpStreamWith(t, m, "hi", func(i int, m *Model) tea.Cmd {
		if i == 0 {
			m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		}
		return nil
	})

	if m.streaming {
		t.Error("streaming flag should be false after stream end")
	}
	if m.status != "Cancelled" {
		t.Errorf("status = %q, want Cancelled after cancelled stream end", m.status)
	}
}

func TestCtrlC_SecondPress_ForceQuits(t *testing.T) {
	// While already in the cancelling state, a second Ctrl+C
	// force-quits (requestExit + tea.Quit).
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamDeltaEvent("one"),
		streamCompletionEvent(),
	}})

	// Enter streaming state (sendMessage returns the first read cmd).
	cmd := m.sendMessage("hi")
	if cmd == nil {
		t.Fatal("sendMessage returned nil cmd")
	}
	// First Ctrl+C cancels.
	_, cmd1 := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd1 == nil {
		t.Error("first Ctrl+C should return readStreamEvent command")
	}
	// Second Ctrl+C while cancelled force-quits.
	_, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if !m.quitRequested {
		t.Error("expected quitRequested after second Ctrl+C (force-quit)")
	}
	if m.status != "Goodbye!" {
		t.Errorf("status = %q, want Goodbye! after force-quit", m.status)
	}
	if cmd2 == nil {
		t.Error("expected tea.Quit command from second Ctrl+C")
	}
}

func TestCtrlC_WhileNotStreaming_Quits(t *testing.T) {
	// Ctrl+C with no active stream exits immediately.
	m := newStreamModel(&fakeLoop{})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if !m.quitRequested {
		t.Error("expected quitRequested from Ctrl+C when idle")
	}
	if m.status != "Goodbye!" {
		t.Errorf("status = %q, want Goodbye!", m.status)
	}
	if cmd == nil {
		t.Error("expected tea.Quit command")
	}
}

func TestStreamEnd_Guard_NoOpWhenNotStreaming(t *testing.T) {
	// streamEndMsg with no active stream and no cancellation must be a
	// no-op (early return guard), not corrupt state.
	m := newStreamModel(&fakeLoop{})

	_, cmd := m.Update(streamEndMsg{Content: "stray end"})

	if cmd != nil {
		t.Error("expected no command from stray streamEndMsg")
	}
	if m.streaming {
		t.Error("streaming flag should stay false for stray end")
	}
}

// --- A3: streamingErrorMsg / streamEndMsg branch coverage ---

func TestStreamError_SettlesStateAndContinuesStream(t *testing.T) {
	// A streaming error mid-stream must be surfaced as a system
	// message, reset the streaming state (so the UI recovers), and
	// return a readStreamEvent command that keeps consuming streamCh.
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamDeltaEvent("before"),
		streamErrorEvent("boom mid-stream"),
		streamDeltaEvent("after"),
		streamCompletionEvent(),
	}})

	var settled bool
	var continued bool
	pumpStreamWith(t, m, "hi", func(i int, m *Model) tea.Cmd {
		if i == 1 { // error message processed
			// Error branch must have settled the streaming state.
			if m.streaming {
				t.Error("streaming flag should be false after error")
			}
			if m.status != "Error occurred" {
				t.Errorf("status = %q, want Error occurred", m.status)
			}
			settled = true
		}
		if i == 2 { // delta after the error processed
			// The stream must still be consumed after the error,
			// accumulating the late delta fresh.
			if !strings.Contains(m.currentStream, "after") {
				t.Errorf("stream continuation lost, currentStream = %q", m.currentStream)
			}
			continued = true
		}
		return nil
	})

	if !settled {
		t.Fatal("error branch was never visited")
	}
	if !continued {
		t.Fatal("post-error delta was never processed")
	}

	// The error must be in the message history.
	found := false
	for _, e := range m.messages {
		if e.Role == "system" && strings.Contains(e.Content, "boom mid-stream") {
			found = true
		}
	}
	if !found {
		t.Errorf("error system message missing: %#v", m.messages)
	}
}

func TestStreamError_EmptyContentAfterErrorConsumed(t *testing.T) {
	// After an error, currentStream is reset; the stream continues and
	// any late delta is accumulated fresh (not mixed with the
	// pre-error content).
	m := newStreamModel(&fakeLoop{events: []*event.Event{
		streamDeltaEvent("stale"),
		streamErrorEvent("fatal"),
		streamCompletionEvent(),
	}})

	pumpStream(t, m, "hi")

	if m.currentStream != "" {
		t.Errorf("currentStream = %q, want empty (reset after error)", m.currentStream)
	}
	// The stale delta must not be flushed as an assistant message.
	for _, e := range m.messages {
		if e.Role == "assistant" && strings.Contains(e.Content, "stale") {
			t.Errorf("stale pre-error content flushed to history: %#v", m.messages)
		}
	}
}
