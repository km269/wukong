package extension

import (
	"log/slog"
	"sync"
	"time"

	"github.com/km269/wukong/pkg/logutil"
)

type ToolAuditEntry struct {
	ToolName   string    `json:"tool_name"`
	ArgsSize   int       `json:"args_size"`
	ResultSize int       `json:"result_size"`
	DurationMs int64     `json:"duration_ms"`
	IsError    bool      `json:"is_error"`
	ErrorMsg   string    `json:"error_msg,omitempty"`
	ClientIP   string    `json:"client_ip"`
	Timestamp  time.Time `json:"timestamp"`
}

type ToolAuditLogger struct {
	mu      sync.RWMutex
	entries []ToolAuditEntry
	maxSize int
}

func NewToolAuditLogger(maxEntries int) *ToolAuditLogger {
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	return &ToolAuditLogger{
		entries: make([]ToolAuditEntry, 0, maxEntries),
		maxSize: maxEntries,
	}
}

func (l *ToolAuditLogger) Record(entry ToolAuditEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.entries) >= l.maxSize {
		l.entries = l.entries[1:]
	}
	l.entries = append(l.entries, entry)

	logutil.Info("[mcp] tool audit",
		slog.String("tool", entry.ToolName),
		slog.Int("args_size", entry.ArgsSize),
		slog.Int("result_size", entry.ResultSize),
		slog.Int64("duration_ms", entry.DurationMs),
		slog.Bool("is_error", entry.IsError),
		slog.String("client_ip", entry.ClientIP),
	)
}

func (l *ToolAuditLogger) Entries() []ToolAuditEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	result := make([]ToolAuditEntry, len(l.entries))
	copy(result, l.entries)
	return result
}

func (l *ToolAuditLogger) Recent(n int) []ToolAuditEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if n > len(l.entries) {
		n = len(l.entries)
	}
	result := make([]ToolAuditEntry, n)
	for i := 0; i < n; i++ {
		result[i] = l.entries[len(l.entries)-1-i]
	}
	return result
}

func (l *ToolAuditLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = l.entries[:0]
}

type MCPHealthStatus struct {
	Running     bool             `json:"running"`
	ToolCount   int              `json:"tool_count"`
	TotalCalls  int64            `json:"total_calls"`
	ErrorCalls  int64            `json:"error_calls"`
	Uptime      string           `json:"uptime"`
	StartTime   time.Time        `json:"start_time"`
	RecentCalls []ToolAuditEntry `json:"recent_calls,omitempty"`
}

type MCPHealthChecker struct {
	mu         sync.RWMutex
	startTime  time.Time
	totalCalls int64
	errorCalls int64
	auditor    *ToolAuditLogger
}

func NewMCPHealthChecker(auditor *ToolAuditLogger) *MCPHealthChecker {
	return &MCPHealthChecker{
		startTime: time.Now(),
		auditor:   auditor,
	}
}

func (h *MCPHealthChecker) RecordCall(isError bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.totalCalls++
	if isError {
		h.errorCalls++
	}
}

func (h *MCPHealthChecker) Status(running bool, toolCount int) MCPHealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	recent := h.auditor.Recent(10)
	return MCPHealthStatus{
		Running:     running,
		ToolCount:   toolCount,
		TotalCalls:  h.totalCalls,
		ErrorCalls:  h.errorCalls,
		Uptime:      time.Since(h.startTime).String(),
		StartTime:   h.startTime,
		RecentCalls: recent,
	}
}
