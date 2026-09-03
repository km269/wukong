// Code split out of model.go (P2-8) - same package, zero behavior change.
package tui

import (
	"fmt"
	"github.com/km269/wukong/internal/util"
	"strings"
	"time"
)

func (m *Model) renderAssistantMessage(content string) string {
	content = util.RedactSecrets(content, m.secrets)
	rendered := RenderAssistantMessage(content)
	if m.mdRenderer == nil {
		return rendered
	}

	md, err := m.mdRenderer.Render(content)
	if err != nil {
		return rendered
	}
	if md == "" {
		return rendered
	}
	return assistantStyle.Render("Wukong: ") + md
}

// markdownStreamFlushThreshold: content below this size is rendered in
// full every frame — the incremental machinery is not worth the book-
// keeping for small messages (the render itself is fast).
// markdownStreamFlushThreshold: content below this size is rendered in
// full every frame — the incremental machinery is not worth the book-
// keeping for small messages (the render itself is fast).
const markdownStreamFlushThreshold = 2 * 1024

// markdownStreamReconcileEvery forces a full re-render every N deltas
// so any subtle block-boundary approximation self-corrects within a
// few frames.
// markdownStreamReconcileEvery forces a full re-render every N deltas
// so any subtle block-boundary approximation self-corrects within a
// few frames.
const markdownStreamReconcileEvery = 8

// renderStreamedMarkdown renders assistant content during streaming.
// It caches the rendered output of the stable document prefix (everything
// up to the last blank-line boundary, at which point the markdown blocks
// are independent) and only re-renders the still-growing final block.
//
// Safety: incremental reuse is ONLY attempted when the split point is
// provably independent — an even number of code fences above the split
// AND an even number inside the tail (no open block). Any other case
// falls back to a full render, which is always correct. A periodic full
// reconciliation further bounds any approximation.
// renderStreamedMarkdown renders assistant content during streaming.
// It caches the rendered output of the stable document prefix (everything
// up to the last blank-line boundary, at which point the markdown blocks
// are independent) and only re-renders the still-growing final block.
//
// Safety: incremental reuse is ONLY attempted when the split point is
// provably independent — an even number of code fences above the split
// AND an even number inside the tail (no open block). Any other case
// falls back to a full render, which is always correct. A periodic full
// reconciliation further bounds any approximation.
func (m *Model) renderStreamedMarkdown(content string) string {
	if m.mdRenderer == nil {
		m.invalidateStreamCache()
		return RenderAssistantMessage(content)
	}

	// Not the active streaming message: full render.
	if !m.streaming || m.currentStream != content {
		m.invalidateStreamCache()
		return m.renderAssistantMessage(content)
	}

	m.streamDeltas++

	// Cache was invalidated (message replaced / /new / /clear / /resume).
	if !m.streamCacheValid {
		m.streamCachePrefixIdx = 0
		m.streamCacheRendered = ""
		m.streamCacheValid = true
		return m.renderStreamedMarkdownFull(content)
	}

	if len(content) < markdownStreamFlushThreshold ||
		m.streamCachePrefixIdx == 0 ||
		m.streamDeltas%markdownStreamReconcileEvery == 0 {
		return m.renderStreamedMarkdownFull(content)
	}

	// Fast path: only the final block changed since last frame. Verify
	// the prefix is still valid, then render just the tail.
	prefixEnd := m.streamCachePrefixIdx
	if prefixEnd > len(content) ||
		!strings.HasPrefix(content, content[:prefixEnd]) {
		// Content was replaced: reset and render in full.
		m.invalidateStreamCache()
		return m.renderAssistantMessage(content)
	}

	tail := content[prefixEnd:]
	if !safeIncrementalTail(tail) {
		// Tail has an open code block or is empty: full render this
		// frame (incremental reuse will resume at the next safe split).
		m.invalidateStreamCache()
		return m.renderAssistantMessage(content)
	}

	rendered := m.streamCacheRendered + "\n\n" + m.renderMarkdownBody(tail)
	if rendered == "" {
		m.invalidateStreamCache()
		return m.renderAssistantMessage(content)
	}
	return assistantStyle.Render("Wukong: ") + rendered
}

// renderMarkdownBody renders a content fragment with the markdown
// renderer and returns the body without the "Wukong: " prefix or
// trailing newlines. Returns "" when the render fails.
// renderMarkdownBody renders a content fragment with the markdown
// renderer and returns the body without the "Wukong: " prefix or
// trailing newlines. Returns "" when the render fails.
func (m *Model) renderMarkdownBody(content string) string {
	if m.mdRenderer == nil {
		return ""
	}
	md, err := m.mdRenderer.Render(content)
	if err != nil || md == "" {
		return ""
	}
	return strings.TrimRight(md, "\n")
}

// safeIncrementalTail reports whether the trailing block can be rendered
// standalone and concatenated with the cached prefix without changing
// markdown semantics. Since the split is at a blank line, blocks are
// independent; the only risk is an odd number of fences (an open code
// block) whose tail would render differently in isolation.
// safeIncrementalTail reports whether the trailing block can be rendered
// standalone and concatenated with the cached prefix without changing
// markdown semantics. Since the split is at a blank line, blocks are
// independent; the only risk is an odd number of fences (an open code
// block) whose tail would render differently in isolation.
func safeIncrementalTail(tail string) bool {
	if tail == "" {
		return false
	}
	return fenceCount(tail)%2 == 0
}

func fenceCount(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") ||
			strings.HasPrefix(strings.TrimSpace(line), "~~~") {
			n++
		}
	}
	return n
}

// renderStreamedMarkdownFull renders the whole document and re-baselines
// the incremental cache at the last safe split point (if any).
// renderStreamedMarkdownFull renders the whole document and re-baselines
// the incremental cache at the last safe split point (if any).
func (m *Model) renderStreamedMarkdownFull(content string) string {
	full := m.renderAssistantMessage(content)

	// Re-baseline: find the last safe blank-line split.
	idx := lastSafeSplit(content)
	if idx > 0 {
		m.streamCachePrefixIdx = idx
		m.streamCacheRendered = strings.TrimRight(
			m.renderMarkdownBody(content[:idx]), "\n")
	} else {
		m.streamCachePrefixIdx = 0
		m.streamCacheRendered = ""
	}
	m.streamCacheValid = true
	return full
}

// lastSafeSplit returns the byte offset just after the last blank line
// whose prefix and suffix both have an even fence count. A split with an
// odd fence count anywhere would break rendering, so only provably safe
// boundaries are returned (0 when none exist).
// lastSafeSplit returns the byte offset just after the last blank line
// whose prefix and suffix both have an even fence count. A split with an
// odd fence count anywhere would break rendering, so only provably safe
// boundaries are returned (0 when none exist).
func lastSafeSplit(content string) int {
	lines := strings.Split(content, "\n")
	// start[i] = byte offset where line i begins.
	start := make([]int, len(lines))
	pos := 0
	for i, ln := range lines {
		start[i] = pos
		pos += len(ln) + 1
	}
	// Walk blank lines from the end; the split point is the byte just
	// after the blank line (the start of the next line). Only accept a
	// split whose prefix and tail both have an even fence count.
	for i := len(lines) - 2; i >= 0; i-- {
		if lines[i] != "" {
			continue
		}
		split := start[i+1]
		tail := content[split:]
		if tail == "" {
			continue
		}
		if fenceCount(tail)%2 != 0 {
			continue
		}
		if fenceCount(content[:split])%2 != 0 {
			continue
		}
		return split
	}
	return 0
}

// invalidateStreamCache forces the next render to re-baseline.
// invalidateStreamCache forces the next render to re-baseline.
func (m *Model) invalidateStreamCache() {
	m.streamCachePrefixIdx = 0
	m.streamCacheRendered = ""
	m.streamCacheValid = false
}

// resetStreamCache invalidates the incremental streaming-render cache.
// Called whenever currentStream is replaced or cleared.
// resetStreamCache invalidates the incremental streaming-render cache.
// Called whenever currentStream is replaced or cleared.
func (m *Model) resetStreamCache() {
	m.invalidateStreamCache()
}

func (m *Model) addToHistory(input string) {
	if len(m.cmdHistory) == 0 || m.cmdHistory[len(m.cmdHistory)-1] != input {
		m.cmdHistory = append(m.cmdHistory, input)
		if len(m.cmdHistory) > maxCommandHistory {
			m.cmdHistory = m.cmdHistory[1:]
		}
	}
}

func (m *Model) setLog(msg string) {
	if msg == "" {
		return
	}
	m.logBuffer = append(m.logBuffer, msg)
	if len(m.logBuffer) > 5 {
		m.logBuffer = m.logBuffer[1:]
	}
}

// recordAuditEntry logs a completed tool call into the bounded
// audit ring buffer used by the /audit command.
// recordAuditEntry logs a completed tool call into the bounded
// audit ring buffer used by the /audit command.
func (m *Model) recordAuditEntry(tc toolCallEntry) {
	var durationMs int64
	if !tc.StartTime.IsZero() {
		durationMs = time.Since(tc.StartTime).Milliseconds()
	}
	entry := toolAuditEntry{
		Name:       tc.Name,
		ArgsSize:   len(tc.Args),
		ResultSize: len(tc.Result),
		DurationMs: durationMs,
		IsError:    tc.Status == "error",
		Timestamp:  time.Now().Format("15:04:05"),
	}
	if len(m.auditLog) >= maxAuditEntries {
		m.auditLog = m.auditLog[1:]
	}
	m.auditLog = append(m.auditLog, entry)
}

// renderAuditPanel builds a string representation of the recent
// audit entries for display in the chat area.
// renderAuditPanel builds a string representation of the recent
// audit entries for display in the chat area.
func (m *Model) renderAuditPanel(limit int) string {
	if len(m.auditLog) == 0 {
		return "No tool audit entries yet."
	}
	if limit <= 0 || limit > len(m.auditLog) {
		limit = len(m.auditLog)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Tool Audit (last %d of %d):\n", limit, len(m.auditLog)))
	sb.WriteString(strings.Repeat("─", 50) + "\n")

	// Show most recent first
	start := len(m.auditLog) - limit
	for i := len(m.auditLog) - 1; i >= start; i-- {
		e := m.auditLog[i]
		statusIcon := "✓"
		if e.IsError {
			statusIcon = "✗"
		}
		sb.WriteString(fmt.Sprintf(
			" %s %-20s | args: %5dB | result: %5dB | %6dms | %s\n",
			statusIcon,
			e.Name,
			e.ArgsSize,
			e.ResultSize,
			e.DurationMs,
			e.Timestamp,
		))
	}
	return sb.String()
}
