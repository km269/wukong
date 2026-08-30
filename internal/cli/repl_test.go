package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/km269/wukong/internal/config"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// --- P3.4: history ring ---

func TestHistory_AddDedupAndBound(t *testing.T) {
	h := newHistory(3)
	h.add("one")
	h.add("one") // duplicate of most recent → dropped
	h.add("two")
	h.add("three")
	h.add("four") // evicts "one"

	if h.len() != 3 {
		t.Fatalf("len = %d, want 3", h.len())
	}
	want := []string{"two", "three", "four"}
	for i, w := range want {
		if h.entries[i] != w {
			t.Errorf("entries[%d] = %q, want %q", i, h.entries[i], w)
		}
	}
}

func TestHistory_EmptyIgnored(t *testing.T) {
	h := newHistory(3)
	h.add("")
	h.add("   ")
	if h.len() != 0 {
		t.Errorf("empty lines must not be recorded, len = %d", h.len())
	}
}

func TestHistory_TrimmedNonDedup(t *testing.T) {
	h := newHistory(5)
	for _, s := range []string{"a", "b", "a"} {
		h.add(s)
	}
	if h.len() != 3 {
		t.Fatalf("non-consecutive duplicates are kept, len = %d", h.len())
	}
}

// --- C2: history persistence (REPL history survives across processes) ---

func TestHistoryWithFile_SavesAndLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	h1 := newHistoryWithFile(100, path)
	h1.add("first question")
	h1.add("second question")

	// A new instance (simulating a fresh process) must recover entries.
	h2 := newHistoryWithFile(100, path)
	if h2.len() != 2 {
		t.Fatalf("loaded len = %d, want 2", h2.len())
	}
	if h2.entries[0] != "first question" || h2.entries[1] != "second question" {
		t.Errorf("loaded entries = %v", h2.entries)
	}
}

func TestHistoryWithFile_MissingFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	h := newHistoryWithFile(100, path)
	if h.len() != 0 {
		t.Errorf("missing file must load as empty, got %d entries", h.len())
	}
}

func TestHistoryWithFile_LoadBoundedByMax(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	h := newHistoryWithFile(100, path)
	for i := 0; i < 120; i++ {
		h.add(fmt.Sprintf("line-%03d", i))
	}
	// In-memory ring is capped at max.
	if h.len() != 100 {
		t.Fatalf("in-memory len = %d, want 100", h.len())
	}

	// Reload: file should also be capped, keeping the newest entries.
	h2 := newHistoryWithFile(3, path)
	if h2.len() != 3 {
		t.Fatalf("reloaded len = %d, want 3", h2.len())
	}
	want := []string{"line-118", "line-119"}
	if h2.entries[1] != want[0] || h2.entries[2] != want[1] {
		t.Errorf("reloaded tail = %v, want tail %v", h2.entries, want)
	}
}

func TestHistoryWithFile_CRLFAndBlankLinesTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	// Hand-written file with CRLF endings, a blank line and a
	// trailing newline — must be tolerated on load.
	if err := os.WriteFile(path, []byte("one\r\n\r\ntwo\n"), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	h := newHistoryWithFile(100, path)
	if h.len() != 2 {
		t.Fatalf("loaded len = %d, want 2 (blank + CR stripped)", h.len())
	}
	if h.entries[0] != "one" || h.entries[1] != "two" {
		t.Errorf("entries = %v", h.entries)
	}
}

func TestHistoryWithFile_DeleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	h := newHistoryWithFile(100, path)
	h.add("keep me")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("history file should exist after add: %v", err)
	}

	h.deleteHistoryFile()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("history file should be deleted, err = %v", err)
	}
	// In-memory entries survive (the caller clears them separately).
	if h.len() != 1 {
		t.Errorf("deleteHistoryFile must not touch in-memory entries, len = %d", h.len())
	}

	// Deleting again is a no-op, not an error.
	h.deleteHistoryFile()
}

func TestHistoryFilePath_DefaultHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	got := historyFilePath(nil)
	want := filepath.Join(home, ".wukong", "history")
	if got != want {
		t.Errorf("historyFilePath(nil) = %q, want %q", got, want)
	}
}

func TestHistoryFilePath_UsesProjectDir(t *testing.T) {
	cfg := &config.WukongConfig{ProjectDir: "~/custom-data/"}
	got := historyFilePath(cfg)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	if !strings.Contains(got, filepath.Join(home, "custom-data", "history")) {
		t.Errorf("historyFilePath with ProjectDir = %q, want it under custom-data", got)
	}
}

func TestHistoryFilePath_ResolvesRelative(t *testing.T) {
	got := historyFilePath(&config.WukongConfig{ProjectDir: "rel"})
	if got == "rel" {
		t.Error("historyFilePath must resolve relative ProjectDir to an absolute path")
	}
}

// --- P3.4: editor state operations ---

func TestInsertBackspaceDeleteAt(t *testing.T) {
	es := &editorState{}
	for _, r := range "hello" {
		insertRune(es, rune(r))
	}
	if got := string(es.line); got != "hello" {
		t.Fatalf("line = %q", got)
	}
	if es.cursor != 5 {
		t.Fatalf("cursor = %d, want 5", es.cursor)
	}

	// Backspace at end removes last rune.
	backspace(es)
	if got := string(es.line); got != "hell" {
		t.Errorf("after backspace line = %q, want hell", got)
	}

	// Move cursor to middle, insert.
	es.cursor = 1
	insertRune(es, rune('X'))
	if got := string(es.line); got != "hXell" {
		t.Errorf("after insert line = %q, want hXell", got)
	}

	// deleteAt removes the rune under the cursor.
	es.cursor = 1
	deleteAt(es)
	if got := string(es.line); got != "hell" {
		t.Errorf("after deleteAt line = %q, want hell", got)
	}

	// Cursor bounds: backspace at 0 is a no-op.
	es.cursor = 0
	backspace(es)
	if got := string(es.line); got != "hell" {
		t.Errorf("backspace at cursor 0 must not change line, got %q", got)
	}
}

func TestEditorState_MultiByteRunes(t *testing.T) {
	es := &editorState{}
	for _, r := range "你好wukong" {
		insertRune(es, r)
	}
	if es.cursor != len([]rune("你好wukong")) {
		t.Errorf("cursor in runes = %d", es.cursor)
	}
	// Backspace removes the Chinese character (single rune).
	backspace(es)
	if got := string(es.line); got != "你好wukon" {
		t.Errorf("line = %q, want 你好wukon", got)
	}
}

// --- P3.4: streaming tool-render helpers ---

func TestArgsSummary(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"{}", ""},
		{"null", ""},
		{`{"path":"/a/b"}`, `'{"path":"/a/b"}'`},
		{`{"very":"long"` + strings.Repeat("x", 60), "'" + `{"very":"long"` + strings.Repeat("x", 26) + "…'"},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := argsSummary(c.in); got != c.want {
			t.Errorf("argsSummary(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("  line one\nline two"); got != "line one" {
		t.Errorf("firstLine = %q, want line one", got)
	}
	if got := firstLine("\n\n  spaced\n"); got != "spaced" {
		t.Errorf("firstLine = %q, want spaced", got)
	}
	if got := firstLine("only"); got != "only" {
		t.Errorf("firstLine = %q, want only", got)
	}
}

func TestStreamToolRenderer_Output(t *testing.T) {
	var buf bytes.Buffer
	r := &streamToolRenderer{out: &buf}

	if err := r.toolCallStart("read_file", `{"path":"/tmp/x"}`); err != nil {
		t.Fatalf("toolCallStart: %v", err)
	}
	if err := r.toolCallResult("read_file", "line1\nline2"); err != nil {
		t.Fatalf("toolCallResult: %v", err)
	}
	if err := r.toolCallResult("empty_result", ""); err != nil {
		t.Fatalf("toolCallResult empty: %v", err)
	}
	if err := r.toolCallError("bad_tool", "boom"); err != nil {
		t.Fatalf("toolCallError: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"◉", "read_file", "'{\"path\":\"/tmp/x\"}'",
		"✔", "line1", "(no content)",
		"✖", "bad_tool", "boom",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestStreamToolRenderer_ResultTruncation(t *testing.T) {
	var buf bytes.Buffer
	r := &streamToolRenderer{out: &buf}
	long := strings.Repeat("x", 200)
	_ = r.toolCallResult("tool", long)

	out := buf.String()
	if !strings.Contains(out, "…") {
		t.Error("long result should be truncated with ellipsis")
	}
	if strings.Contains(out, strings.Repeat("x", 81)+"x") {
		t.Error("result should be limited to 80 chars")
	}
}

func TestStreamToolRenderer_FirstLineCap(t *testing.T) {
	var buf bytes.Buffer
	r := &streamToolRenderer{out: &buf}
	// Multi-line result: only the first non-empty line is shown.
	_ = r.toolCallResult("tool", "first\nsecond\nthird")

	out := buf.String()
	if !strings.Contains(out, "first") {
		t.Error("first line should be present")
	}
	if strings.Contains(out, "second") || strings.Contains(out, "third") {
		t.Error("only first line should be rendered")
	}
}

// --- P3.4: streamToStdoutWith event discrimination ---

func newContentEvent(content string) *event.Event {
	return &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Delta: model.Message{Content: content},
				},
			},
		},
	}
}

func newToolCallEvent(name string, args string) *event.Event {
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

func newToolResultEvent(toolName, content string) *event.Event {
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

func TestStreamToStdout_ContentOnly(t *testing.T) {
	var buf bytes.Buffer
	err := streamToStdoutWith(newContentEvent("hello world"), &buf, nil)
	if err != nil {
		t.Fatalf("streamToStdoutWith: %v", err)
	}
	if buf.String() != "hello world" {
		t.Errorf("content = %q, want hello world", buf.String())
	}
}

func TestStreamToStdout_ToolCallAndResult(t *testing.T) {
	var buf bytes.Buffer
	if err := streamToStdoutWith(newToolCallEvent("read_file", `{"p":"/x"}`), &buf, nil); err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if err := streamToStdoutWith(newToolResultEvent("read_file", "content"), &buf, nil); err != nil {
		t.Fatalf("tool result: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "◉") || !strings.Contains(out, "read_file") {
		t.Errorf("tool call marker missing:\n%s", out)
	}
	if !strings.Contains(out, "✔") {
		t.Errorf("tool result marker missing:\n%s", out)
	}
	if !strings.Contains(out, "content") {
		t.Errorf("result content missing:\n%s", out)
	}
}

func TestStreamToStdout_NilEvent(t *testing.T) {
	var buf bytes.Buffer
	if err := streamToStdoutWith(nil, &buf, nil); err != nil {
		t.Fatalf("nil event should be a no-op, got %v", err)
	}
	if buf.Len() != 0 {
		t.Error("nil event should produce no output")
	}
}

// --- P3.4: editLine plain fallback (non-TTY) ---

func TestReadPlainLine_ViaPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	go func() {
		defer w.Close()
		_, _ = w.WriteString("hello dial\n")
	}()

	line, err := readPlainLine(r)
	if err != nil {
		t.Fatalf("readPlainLine: %v", err)
	}
	if line != "hello dial" {
		t.Errorf("line = %q, want hello dial", line)
	}
}

func TestReadPlainLine_EOFWithoutNewline(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	go func() {
		defer w.Close()
		_, _ = w.WriteString("no newline")
	}()

	line, err := readPlainLine(r)
	if err != nil {
		t.Fatalf("readPlainLine: %v", err)
	}
	if line != "no newline" {
		t.Errorf("line = %q, want no newline", line)
	}
}

func TestReadPlainLine_EmptyPipeEOF(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	_ = w.Close()

	_, err = readPlainLine(r)
	if err == nil {
		t.Fatal("expected io.EOF for empty pipe")
	}
}

func TestEditLine_FallsBackOnNonTTY(t *testing.T) {
	// editLine on a pipe fd: term.MakeRaw fails → readPlainLine path.
	f, err := os.CreateTemp(t.TempDir(), "stdin-sim")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	_, _ = f.WriteString("pip\ned\n")
	_, _ = f.Seek(0, 0)
	// Note: file fds are not TTYs, so MakeRaw errors and the plain
	// path reads.
	line, err := editLine(f, os.Stdout, newHistory(5))
	if err != nil {
		t.Fatalf("editLine: %v", err)
	}
	if line != "pip" {
		t.Errorf("line = %q, want pip", line)
	}
}

// --- P3.4: readPlainLine shares the plain reader ---

func TestReadPlainLine_CRLFHandling(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	go func() {
		defer w.Close()
		_, _ = w.WriteString("line\r\n")
	}()

	line, err := readPlainLine(r)
	if err != nil {
		t.Fatalf("readPlainLine: %v", err)
	}
	if line != "line" {
		t.Errorf("CR must be stripped, got %q", line)
	}
}

var _ = filepath.Join // avoid unused import if helpers change

// --- Phase 4.4: runDialogue command branches ---

// runDialogueWithInput feeds `input` through os.Stdin, captures
// os.Stdout, and runs runDialogue with a nil loop — safe because the
// command branches (/exit, /session, /clear, /history-clear, /help,
// empty line) never reach runOneShot.
func runDialogueWithInput(t *testing.T, input string) (string, error) {
	t.Helper()
	return runDialogueWithCfg(t, input, nil)
}

// runDialogueWithCfg is runDialogueWithInput with an explicit config.
// The history data dir defaults to an isolated temp dir so tests do
// not touch the user's real ~/.wukong/history.
func runDialogueWithCfg(
	t *testing.T, input string, cfg *config.WukongConfig,
) (string, error) {
	t.Helper()

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	oldStdin, oldStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	defer func() {
		os.Stdin, os.Stdout = oldStdin, oldStdout
	}()

	// Writer goroutine writes the input then closes it so
	// runDialogue's readPlainLine sees EOF and terminates.
	go func() {
		_, _ = inW.WriteString(input)
		_ = inW.Close()
	}()
	defer func() {
		_ = inR.Close()
	}()

	outDone := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 512)
		for {
			n, err := outR.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil {
				break
			}
		}
		outDone <- string(buf)
	}()

	if cfg == nil {
		cfg = &config.WukongConfig{}
	}
	if cfg.ProjectDir == "" {
		cfg.ProjectDir = filepath.Join(t.TempDir(), ".wukong")
	}
	err = runDialogue(cfg, nil, "test-user", "test-session", true)

	_ = outW.Close()
	output := <-outDone
	_ = outR.Close()

	return output, err
}

func TestRunDialogue_ExitCommand(t *testing.T) {
	out, err := runDialogueWithInput(t, "/exit\n")
	if err != nil {
		t.Fatalf("runDialogue: %v", err)
	}
	if !strings.Contains(out, "Goodbye.") {
		t.Errorf("output missing Goodbye, got:\n%s", out)
	}
}

func TestRunDialogue_QuitAlias(t *testing.T) {
	out, err := runDialogueWithInput(t, "/quit\n")
	if err != nil {
		t.Fatalf("runDialogue: %v", err)
	}
	if !strings.Contains(out, "Goodbye.") {
		t.Errorf("output missing Goodbye, got:\n%s", out)
	}
}

func TestRunDialogue_EOFExits(t *testing.T) {
	// No input at all: readPlainLine hits EOF and the dialogue ends.
	out, err := runDialogueWithInput(t, "")
	if err != nil {
		t.Fatalf("runDialogue: %v", err)
	}
	if !strings.Contains(out, "Goodbye.") {
		t.Errorf("output missing Goodbye on EOF, got:\n%s", out)
	}
}

func TestRunDialogue_SessionCommand(t *testing.T) {
	out, err := runDialogueWithInput(t, "/session\n/exit\n")
	if err != nil {
		t.Fatalf("runDialogue: %v", err)
	}
	if !strings.Contains(out, "Session ID: test-session") {
		t.Errorf("output missing session id, got:\n%s", out)
	}
}

func TestRunDialogue_ClearCommand(t *testing.T) {
	out, err := runDialogueWithInput(t, "/clear\n/exit\n")
	if err != nil {
		t.Fatalf("runDialogue: %v", err)
	}
	// ANSI clear-screen escape.
	if !strings.Contains(out, "\x1b[2J\x1b[H") {
		t.Errorf("output missing clear-screen escape, got:\n%s", out)
	}
}

func TestRunDialogue_HelpCommand(t *testing.T) {
	out, err := runDialogueWithInput(t, "/help\n/exit\n")
	if err != nil {
		t.Fatalf("runDialogue: %v", err)
	}
	for _, want := range []string{
		"Commands:", "/exit", "/session", "/clear", "/help",
		"Session ID: test-session",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q:\n%s", want, out)
		}
	}
}

func TestRunDialogue_HistoryClearRemovesFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".wukong")
	path := historyFilePath(&config.WukongConfig{ProjectDir: dir})

	// Pre-populate the history file, as a previous dialogue run did.
	h := newHistoryWithFile(100, path)
	h.add("remembered line")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("history file should exist after add: %v", err)
	}

	// A fresh instance (simulating a new process) sees the entry.
	h2 := newHistoryWithFile(100, path)
	if h2.len() != 1 || h2.entries[0] != "remembered line" {
		t.Fatalf("cross-process load failed: %v", h2.entries)
	}

	// /history-clear in a dialogue run must remove the on-disk file.
	cfg := &config.WukongConfig{ProjectDir: dir}
	out, err := runDialogueWithCfg(t, "/history-clear\n/exit\n", cfg)
	if err != nil {
		t.Fatalf("runDialogue: %v", err)
	}
	if !strings.Contains(out, "History cleared.") {
		t.Errorf("output missing confirmation, got:\n%s", out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("history file should be deleted by /history-clear, err = %v", err)
	}
}

func TestRunDialogue_EmptyLineContinues(t *testing.T) {
	out, err := runDialogueWithInput(t, "\n\n/exit\n")
	if err != nil {
		t.Fatalf("runDialogue: %v", err)
	}
	if !strings.Contains(out, "Goodbye.") {
		t.Errorf("empty lines must not exit before /exit, got:\n%s", out)
	}
}

// --- Phase 4.4: streamToStdoutWith redact integration ---

func TestStreamToStdout_RedactsToolArgsAndResult(t *testing.T) {
	secrets := []string{"sk-supersecret"}
	var buf bytes.Buffer

	if err := streamToStdoutWith(
		newToolCallEvent("api_call", `{"api_key":"sk-supersecret","q":"x"}`),
		&buf, secrets,
	); err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if err := streamToStdoutWith(
		newToolResultEvent("api_call", "token sk-supersecret used"),
		&buf, secrets,
	); err != nil {
		t.Fatalf("tool result: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "sk-supersecret") {
		t.Errorf("secret leaked into output: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in output, got:\n%s", out)
	}
}

func TestStreamToStdout_RedactsDeltaContent(t *testing.T) {
	secrets := []string{"tok-123456"}
	var buf bytes.Buffer

	if err := streamToStdoutWith(
		newContentEvent("your token is tok-123456, keep it safe"),
		&buf, secrets,
	); err != nil {
		t.Fatalf("content: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "tok-123456") {
		t.Errorf("secret leaked into delta: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in delta, got:\n%s", out)
	}
}

func TestStreamToStdout_NilSecretsNoRedaction(t *testing.T) {
	var buf bytes.Buffer
	if err := streamToStdoutWith(
		newContentEvent("plain text stays"),
		&buf, nil,
	); err != nil {
		t.Fatalf("content: %v", err)
	}
	if buf.String() != "plain text stays" {
		t.Errorf("got %q, want untouched content", buf.String())
	}
}

// --- A4: runOneShot with injected fake runner ---

// fakeRunner implements the runner interface, recording the received
// message and invoking onEvent for each stashed event. It replaces the
// real agent loop so runOneShot can be tested without bootstrapping.
type fakeRunner struct {
	events    []*event.Event
	result    string
	err       error
	userID    string
	sessionID string
	msg       model.Message
	onEvent   func(evt *event.Event) error
}

// RunStream records the request and replays the stashed events.
func (f *fakeRunner) RunStream(
	ctx context.Context,
	userID, sessionID string,
	message model.Message,
	onEvent func(evt *event.Event) error,
) (string, error) {
	f.userID = userID
	f.sessionID = sessionID
	f.msg = message
	f.onEvent = onEvent
	for _, evt := range f.events {
		if err := onEvent(evt); err != nil {
			return f.result, err
		}
	}
	return f.result, f.err
}

// streamCfg returns a config with streaming enabled and an API key
// secret so redaction is exercised end to end.
func streamCfg(secret string) *config.WukongConfig {
	return &config.WukongConfig{
		Agent: config.AgentConfig{Streaming: true},
		Providers: []config.ProviderConfig{
			{Name: "test", Model: "m1", APIKey: secret},
		},
	}
}

func TestRunOneShot_Streaming_RendersEventsAndRedacts(t *testing.T) {
	secret := "sk-oneshot-secret"
	cfg := streamCfg(secret)
	fake := &fakeRunner{
		events: []*event.Event{
			newContentEvent("answer with " + secret + " inside"),
		},
		result: "ignored-in-stream",
	}

	out, err := captureStdout(t, func() error {
		return runOneShot(cfg, fake, "u1", "s1", "prompt", false)
	})
	if err != nil {
		t.Fatalf("runOneShot: %v", err)
	}
	if strings.Contains(out, secret) {
		t.Errorf("secret leaked into streamed output: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in streamed output, got %q", out)
	}
	if fake.sessionID != "s1" || fake.userID != "u1" {
		t.Errorf("runOneShot args: user=%q session=%q, want u1/s1", fake.userID, fake.sessionID)
	}
	if fake.msg.Role != "user" || fake.msg.Content != "prompt" {
		t.Errorf("msg = %+v, want user prompt", fake.msg)
	}
}

func TestRunOneShot_NonStream_PrintsRedactedResponse(t *testing.T) {
	secret := "sk-plain-secret"
	cfg := streamCfg(secret) // streaming flag on, but noStream forces plain path
	fake := &fakeRunner{
		result: "final answer " + secret,
	}

	out, err := captureStdout(t, func() error {
		return runOneShot(cfg, fake, "u1", "s1", "prompt", true)
	})
	if err != nil {
		t.Fatalf("runOneShot: %v", err)
	}
	if strings.Contains(out, secret) {
		t.Errorf("secret leaked into plain output: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in plain output, got %q", out)
	}
	if fake.onEvent != nil {
		t.Error("non-stream path must not register an onEvent callback")
	}
}

func TestRunOneShot_RunnerError_Propagates(t *testing.T) {
	cfg := streamCfg("") // streaming: onEvent registered
	boom := errors.New("loop exploded")
	fake := &fakeRunner{
		events: []*event.Event{newContentEvent("a")},
		err:    boom,
	}

	err := runOneShot(cfg, fake, "u", "s", "prompt", false)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

// captureStdout redirects os.Stdout during fn and returns all output
// written while fn runs.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	oldOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		tmp := make([]byte, 512)
		for {
			n, err := r.Read(tmp)
			buf.Write(tmp[:n])
			if err != nil {
				break
			}
		}
		done <- buf.String()
	}()

	fnErr := fn()

	os.Stdout = oldOut
	_ = w.Close()
	out := <-done
	_ = r.Close()

	return out, fnErr
}
