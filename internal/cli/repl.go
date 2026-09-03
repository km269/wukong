package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/util"
	"golang.org/x/term"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// ==========================================================================
// Dialogue-mode line editor
// ==========================================================================

// editorState tracks the input line, the cursor position (in runes),
// and the navigation index into the history ring. It is only used
// while the terminal is in raw mode (see editLine).
type editorState struct {
	line    []rune
	cursor  int
	histIdx int // -1 = typing a new line, else index in history
}

// history is a simple bounded ring of past dialogue inputs.
// When filePath is non-empty, entries are persisted to that file
// (C2: REPL history survives across processes).
type history struct {
	entries  []string
	max      int
	filePath string
}

func newHistory(max int) *history {
	if max <= 0 {
		max = 100
	}
	return &history{max: max}
}

// historyFilePath resolves the history persistence location.
// It follows the pattern used by project.Manager: ~ expansion +
// config.ResolvePath. Defaults to ~/.wukong/history.
func historyFilePath(cfg *config.WukongConfig) string {
	dir := "~/.wukong/"
	if cfg != nil && cfg.ProjectDir != "" {
		// Reuse the configured data directory when available so
		// users can relocate all wukong data together.
		dir = cfg.ProjectDir
	}
	if len(dir) >= 2 && dir[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	}
	return filepath.Join(config.ResolvePath(dir), "history")
}

// newHistoryWithFile creates a history ring backed by the given file.
// Existing entries are loaded (bounded by max) so Up/Down recall and
// Ctrl+R search survive across processes. A load failure is non-fatal:
// the ring simply starts empty.
func newHistoryWithFile(max int, path string) *history {
	h := newHistory(max)
	h.filePath = path
	_ = h.load()
	return h
}

// deleteHistoryFile removes the persisted history file. Used by
// /history-clear so both the in-memory ring and the on-disk copy
// are reset.
func (h *history) deleteHistoryFile() {
	if h.filePath == "" {
		return
	}
	if err := os.Remove(h.filePath); err != nil && !os.IsNotExist(err) {
		util.Logger.Warn("history: failed to delete history file",
			"error", err)
	}
}

// load reads entries from the persisted file. The file is a simple
// newline-separated list of raw inputs; timestamps are not stored, so
// ordering reflects insertion order (most recent at the end).
func (h *history) load() error {
	if h.filePath == "" {
		return nil
	}
	data, err := os.ReadFile(h.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh start, no file yet
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		l = strings.TrimSuffix(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		// Apply the same dedup/bound rules as add(), but without
		// triggering a save per line.
		if n := len(h.entries); n > 0 && h.entries[n-1] == l {
			continue
		}
		h.entries = append(h.entries, l)
	}
	if len(h.entries) > h.max {
		h.entries = h.entries[len(h.entries)-h.max:]
	}
	return nil
}

// save atomically writes the current entries to the history file
// using write-temp+rename to avoid corruption on partial writes.
func (h *history) save() error {
	if h.filePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(h.filePath), 0755); err != nil {
		return fmt.Errorf("create history dir: %w", err)
	}
	data := []byte(strings.Join(h.entries, "\n") + "\n")
	tmpPath := h.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, h.filePath); err != nil {
		_ = os.Remove(tmpPath) // cleanup
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// add records a non-empty line, dropping any duplicate of the most
// recent entry (common when recalling and re-entering the same line).
// When the history is file-backed, the entry is persisted immediately.
func (h *history) add(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	if n := len(h.entries); n > 0 && h.entries[n-1] == s {
		return
	}
	h.entries = append(h.entries, s)
	if len(h.entries) > h.max {
		h.entries = h.entries[len(h.entries)-h.max:]
	}
	if h.filePath != "" {
		if err := h.save(); err != nil {
			util.Logger.Warn("history: failed to save history",
				"error", err)
		}
	}
}

func (h *history) len() int { return len(h.entries) }

// editLine reads a single line of input with inline editing and
// history navigation. It puts the terminal in raw mode so each
// keystroke arrives without Enter; when stdin is not a terminal
// (pipes, tests) it falls back to a plain buffered read.
func editLine(in *os.File, out *os.File, hist *history) (string, error) {
	fd := int(in.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		// Not a TTY (or unsupported): plain line read.
		return readPlainLine(in)
	}
	defer func() {
		_ = term.Restore(fd, state)
	}()

	es := &editorState{histIdx: -1}
	var buf [1]byte
	for {
		n, readErr := in.Read(buf[:])
		if readErr != nil {
			if readErr == io.EOF {
				fmt.Fprintln(out) // move off the prompt line
				return "", io.EOF
			}
			return "", readErr
		}
		if n == 0 {
			continue
		}
		b := buf[0]

		switch b {
		case 13, 10: // Enter
			line := string(es.line)
			fmt.Fprintln(out)
			return line, nil
		case 127, 8: // DEL / Backspace
			backspace(es)
		case 3: // Ctrl+C
			fmt.Fprintln(out, "^C")
			return "", io.EOF
		case 4: // Ctrl+D: end of input (EOF)
			fmt.Fprintln(out)
			return "", io.EOF
		case 21: // Ctrl+U: kill line
			es.line = nil
			es.cursor = 0
			redraw(out, es)
		case 5: // Ctrl+E: end of line
			es.cursor = len(es.line)
			redraw(out, es)
		case 1: // Ctrl+A: start of line
			es.cursor = 0
			redraw(out, es)
		case 11: // Ctrl+K: kill to end
			es.line = es.line[:es.cursor]
			redraw(out, es)
		case 18: // Ctrl+R: history search
			searchHistory(in, out, es, hist)
		case 0x1b: // Escape sequence
			if handled, done, line, err := handleEscape(in, out, es, hist); err != nil {
				return "", err
			} else if done {
				fmt.Fprintln(out)
				return line, nil
			} else if handled {
				continue
			}
			// Unhandled escape: ignore
		default:
			if b >= 0x20 {
				insertRune(es, rune(b))
				redraw(out, es)
			}
		}
	}
}

// readPlainLine reads until newline using an unbuffered byte reader,
// applied when raw mode is unavailable (non-TTY stdin).
func readPlainLine(in *os.File) (string, error) {
	var sb strings.Builder
	var buf [1]byte
	for {
		n, err := in.Read(buf[:])
		if n > 0 {
			if buf[0] == '\n' {
				return strings.TrimSuffix(sb.String(), "\r"), nil
			}
			sb.WriteByte(buf[0])
		}
		if err != nil {
			if err == io.EOF {
				if sb.Len() == 0 {
					return "", io.EOF
				}
				return sb.String(), nil
			}
			return "", err
		}
	}
}

// handleEscape interprets cursor/editing escape sequences; returns
// (handled, done, line, err).
func handleEscape(in *os.File, out *os.File, es *editorState, hist *history) (bool, bool, string, error) {
	// We already consumed ESC; grab the next two bytes.
	bb := make([]byte, 2)
	n, err := io.ReadFull(in, bb)
	if err != nil || n < 2 {
		return false, false, "", err
	}
	if bb[0] != '[' {
		return false, false, "", nil
	}
	switch bb[1] {
	case 'A': // Up
		if hist.len() > 0 {
			if es.histIdx < 0 {
				es.histIdx = hist.len() - 1
			} else if es.histIdx > 0 {
				es.histIdx--
			}
			loadHistory(es, hist)
		}
	case 'B': // Down
		if es.histIdx >= 0 {
			es.histIdx++
			if es.histIdx >= hist.len() {
				es.histIdx = -1
				es.line, es.cursor = nil, 0
			} else {
				loadHistory(es, hist)
			}
		}
	case 'C': // Right
		if es.cursor < len(es.line) {
			es.cursor++
		}
	case 'D': // Left
		if es.cursor > 0 {
			es.cursor--
		}
	case 'H': // Home
		es.cursor = 0
	case 'F': // End
		es.cursor = len(es.line)
	default:
		return false, false, "", nil
	}
	redraw(out, es)
	return true, false, "", nil
}

func loadHistory(es *editorState, hist *history) {
	if es.histIdx >= 0 && es.histIdx < hist.len() {
		es.line = []rune(hist.entries[es.histIdx])
		es.cursor = len(es.line)
	}
}

// searchHistory implements a small Ctrl+R incremental search: each
// typed char narrows the most recent matching history line. Enter
// accepts, ESC cancels. Characters are read raw from in.
func searchHistory(in *os.File, out *os.File, es *editorState, hist *history) {
	if hist.len() == 0 {
		return
	}
	query := ""
	matched := -1
	var buf [1]byte
	fmt.Fprint(out, "\r\x1b[K")
	for {
		display := ""
		if matched >= 0 {
			display = hist.entries[matched]
		}
		fmt.Fprintf(out, "(reverse-i-search)`%s': %s", query, dim(display))
		n, err := in.Read(buf[:])
		if err != nil || n == 0 {
			break
		}
		c := buf[0]
		switch {
		case c == 13 || c == 10: // Enter accepts
			if matched >= 0 {
				es.line = []rune(hist.entries[matched])
				es.cursor = len(es.line)
			}
			fmt.Fprint(out, "\r\x1b[K")
			return
		case c == 27: // ESC cancels
			es.line, es.cursor = nil, 0
			fmt.Fprint(out, "\r\x1b[K")
			return
		case c == 127 || c == 8: // backspace
			if len(query) > 0 {
				query = query[:len(query)-1]
			}
		case c >= 0x20:
			query += string(rune(c))
		default:
			continue
		}
		// Re-find most recent match.
		matched = -1
		for i := hist.len() - 1; i >= 0; i-- {
			if strings.Contains(hist.entries[i], query) {
				matched = i
				break
			}
		}
	}
}

func insertRune(es *editorState, r rune) {
	es.line = append(es.line, 0)
	copy(es.line[es.cursor+1:], es.line[es.cursor:])
	es.line[es.cursor] = r
	es.cursor++
}

func backspace(es *editorState) {
	if es.cursor == 0 || len(es.line) == 0 {
		return
	}
	es.line = append(es.line[:es.cursor-1], es.line[es.cursor:]...)
	es.cursor--
}

// deleteAt removes the rune under the cursor.
func deleteAt(es *editorState) {
	if es.cursor >= len(es.line) {
		return
	}
	es.line = append(es.line[:es.cursor], es.line[es.cursor+1:]...)
}

// redraw reprints the current line content after an edit, moving the
// cursor back to the editing position. It assumes the prompt has
// already been printed.
func redraw(out *os.File, es *editorState) {
	fmt.Fprint(out, "\r\x1b[K> "+string(es.line))
	// Move cursor back to the logical position (accounting for the
	// 2-char prompt, without ANSI-aware measuring).
	moveBy := len(es.line) - es.cursor
	if moveBy > 0 {
		fmt.Fprintf(out, "\x1b[%dD", moveBy)
	}
}

// dim wraps a string in the terminal dim SGR codes (used for hints).
func dim(s string) string {
	return "\x1b[2m" + s + "\x1b[0m"
}

// ==========================================================================
// Streaming tool-render helpers
// ==========================================================================

// streamToolRenderer prints tool activity with a dim indicator line,
// mirroring what the TUI shows for tool calls.
type streamToolRenderer struct {
	out io.Writer
}

func (r *streamToolRenderer) toolCallStart(name string, args string) error {
	_, err := fmt.Fprintf(r.out, "\n%s %s %s\n",
		dim("◉"), name, dim(argsSummary(args)))
	return err
}

func (r *streamToolRenderer) toolCallResult(name string, result string) error {
	if result == "" {
		result = "(no content)"
	}
	oneLine := firstLine(result)
	if len(oneLine) > 80 {
		oneLine = oneLine[:80] + "…"
	}
	_, err := fmt.Fprintf(r.out, "  %s %s %s\n",
		dim("✔"), name, dim(oneLine))
	return err
}

func (r *streamToolRenderer) toolCallError(name string, errMsg string) error {
	_, err := fmt.Fprintf(r.out, "  %s %s %s\n",
		"\x1b[31m✖\x1b[0m", name, dim(errMsg))
	return err
}

// argsSummary condenses a JSON arguments blob to a short inline hint.
func argsSummary(args string) string {
	args = strings.TrimSpace(args)
	if args == "" || args == "{}" || args == "null" {
		return ""
	}
	const max = 40
	if len(args) > max {
		args = args[:max] + "…"
	}
	return "'" + args + "'"
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for _, ln := range strings.Split(strings.TrimSpace(s), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			return ln
		}
	}
	return s
}

// ==========================================================================
// The tool-aware streaming callback
// ==========================================================================

// streamToStdout prints streaming output to stdout. It now also
// renders tool activity (calls and results) as dim marker lines,
// interleaved with the model content stream, so the CLI user can
// follow what the agent is doing.
func streamToStdout(evt *event.Event) error {
	return streamToStdoutWith(evt, os.Stdout, nil)
}

// streamToStdoutWith is streamToStdout with an injectable writer
// and optional secret values to redact from the output (see
// config.WukongConfig.SecretValues).
func streamToStdoutWith(evt *event.Event, out io.Writer, secrets []string) error {
	if evt == nil {
		return nil
	}
	r := &streamToolRenderer{out: out}

	if evt.Response != nil && len(evt.Response.Choices) > 0 {
		ch := evt.Response.Choices[0]

		// Tool calls: the choice message carries ToolCalls either in
		// Message (non-streaming) or Delta (streaming chunks).
		var calls []model.ToolCall
		if len(ch.Message.ToolCalls) > 0 {
			calls = ch.Message.ToolCalls
		} else if len(ch.Delta.ToolCalls) > 0 {
			calls = ch.Delta.ToolCalls
		}
		for _, tc := range calls {
			name := tc.Function.Name
			if name == "" {
				continue
			}
			args := ""
			if tc.Function.Arguments != nil {
				args = util.RedactSecrets(
					string(tc.Function.Arguments), secrets)
			}
			_ = r.toolCallStart(name, args)
		}

		// Tool results: emitted as a tool-role message, optionally
		// flagged via the response object.
		if ch.Message.Role == "tool" ||
			evt.Response.Object == "tool.response" {
			if ch.Message.ToolName != "" {
				_ = r.toolCallResult(
					ch.Message.ToolName,
					util.RedactSecrets(ch.Message.Content, secrets))
			}
		}

		// Still stream plain content deltas as before.
		content := ch.Delta.Content
		if content != "" {
			fmt.Fprint(out, util.RedactSecrets(content, secrets))
		}
	}
	return nil
}
