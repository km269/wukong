// Package agent provides context revision engine for token optimization.
// Implements Goose's Context Revision strategies:
// - Smaller/faster LLM summarization
// - Algorithmic stale content pruning
// - Long command output truncation
// - "include all" vs semantic search strategies
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/config"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ContextRevisionEngine implements advanced context optimization
// strategies for managing token budgets in long conversations.
// It hooks into the tRPC session service to trigger asynchronous
// summarization when context exceeds configured thresholds.
type ContextRevisionEngine struct {
	mu  sync.RWMutex
	cfg *config.WukongConfig

	// Runtime statistics
	messageCount     int
	estimatedTokens  int
	lastSummarized   time.Time
	lastRevisionTime time.Time

	// Content tracking
	recentOutputs []string // ring buffer of recent outputs
	maxRecent     int

	// Revision model (smaller/faster LLM for summarization)
	revisionModel RevisionModel

	// Session service for triggering actual compression.
	// When set, the engine can call EnqueueSummaryJob to
	// offload summarization to the framework's async workers.
	sessionService session.Service
}

// RevisionModel is the interface for the summarization model.
type RevisionModel interface {
	Summarize(ctx context.Context, content string, maxTokens int) (string, error)
}

// NewContextRevisionEngine creates a new context revision engine.
func NewContextRevisionEngine(cfg *config.WukongConfig) *ContextRevisionEngine {
	return &ContextRevisionEngine{
		cfg:              cfg,
		lastSummarized:   time.Now(),
		lastRevisionTime: time.Now(),
		recentOutputs:    make([]string, 0, 100),
		maxRecent:        100,
	}
}

// SetRevisionModel sets the summarization model for context revision.
func (e *ContextRevisionEngine) SetRevisionModel(m RevisionModel) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.revisionModel = m
}

// SetSessionService injects the session service for triggering
// framework-level summarization when context revision is needed.
func (e *ContextRevisionEngine) SetSessionService(svc session.Service) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessionService = svc
}

// PrepareContext is called before each agent run to prepare the context.
// When revision is triggered, it enqueues an async summary job via the
// session service and returns a context with the revision signal.
func (e *ContextRevisionEngine) PrepareContext(
	ctx context.Context, sessionKey session.Key,
) context.Context {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.messageCount++

	// Apply revision if needed
	if e.cfg.Revision.Enabled && e.shouldRevise() {
		e.lastRevisionTime = time.Now()
		ctx = context.WithValue(ctx, ctxKeyRevision, true)

		// Trigger async summarization via session service.
		// This is non-blocking and lets the framework's async
		// workers compress old events into a summary.
		if e.sessionService != nil {
			sess, err := e.sessionService.GetSession(
				ctx, sessionKey,
				session.WithEventNum(
					e.cfg.Session.EventLimit,
				),
			)
			if err != nil {
				log.Warnf(
					"context revision: get session "+
						"failed: %v", err,
				)
			} else if sess != nil {
				// Enqueue async summary job. force=true
				// because we've already determined
				// revision is needed.
				if err := e.sessionService.EnqueueSummaryJob(
					ctx, sess,
					session.SummaryFilterKeyAllContents,
					true,
				); err != nil {
					log.Warnf(
						"context revision: enqueue "+
							"summary job failed: %v",
						err,
					)
				}
			}
		}

		// Reset counter after triggering revision so the
		// next cycle starts fresh
		e.messageCount = 0
		e.estimatedTokens = 0
	}

	return ctx
}

// AfterRun is called after each agent run to update token estimates
// and track recent outputs.
func (e *ContextRevisionEngine) AfterRun(
	ctx context.Context, response string, evts []event.Event,
) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Track recent outputs
	if len(response) > 0 {
		e.estimatedTokens += len(response) / 4
		e.addRecentOutput(response)
	}

	// Update token estimate from actual events for accuracy.
	// Each event carries its own token usage via Response.Usage.
	for _, evt := range evts {
		if evt.Response != nil && evt.Response.Usage != nil {
			e.estimatedTokens += evt.Response.Usage.TotalTokens
		}
	}
}

// SummarizeContent generates a summary of the given content using
// the smaller/faster revision model if available.
func (e *ContextRevisionEngine) SummarizeContent(
	ctx context.Context, content string,
) (string, error) {
	e.mu.RLock()
	model := e.revisionModel
	maxTokens := e.cfg.Revision.MaxContextTokens
	e.mu.RUnlock()

	if model == nil {
		return truncateContent(content, maxTokens), nil
	}

	summary, err := model.Summarize(ctx, content, maxTokens/4)
	if err != nil {
		return truncateContent(content, maxTokens), nil
	}
	return summary, nil
}

// TruncateCommandOutput truncates long command outputs to configured limit.
func (e *ContextRevisionEngine) TruncateCommandOutput(
	output string,
) string {
	e.mu.RLock()
	maxLen := e.cfg.Revision.MaxCommandOutput
	e.mu.RUnlock()

	if maxLen <= 0 {
		maxLen = 8000
	}

	if len(output) <= maxLen {
		return output
	}

	// Smart truncation: keep beginning and end
	half := maxLen / 2
	begin := output[:half]
	end := output[len(output)-half:]

	return fmt.Sprintf(
		"%s\n\n... [%d bytes truncated] ...\n\n%s",
		begin, len(output)-maxLen, end,
	)
}

// FilterIrrelevant removes obviously irrelevant content from context.
// This implements algorithmic stale content pruning.
func (e *ContextRevisionEngine) FilterIrrelevant(
	messages []string,
) []string {
	if len(messages) <= 10 {
		return messages
	}

	// Keep most recent messages, summarize older ones
	keepRecent := len(messages) / 2
	result := make([]string, 0, keepRecent+1)

	// Summarize older messages
	if keepRecent > 0 {
		olderContent := strings.Join(
			messages[:len(messages)-keepRecent], "\n",
		)
		summary := fmt.Sprintf(
			"[Previous conversation summary: %d messages, ~%d tokens]",
			len(messages)-keepRecent, len(olderContent)/4,
		)
		result = append(result, summary)
	}

	// Keep recent messages as-is
	result = append(
		result, messages[len(messages)-keepRecent:]...,
	)

	return result
}

// ShouldSummarize returns whether the context should be summarized.
func (e *ContextRevisionEngine) ShouldSummarize() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if !e.cfg.Session.EnableSummary {
		return false
	}
	return e.messageCount >= e.cfg.Session.SummaryTrigger
}

// GetEstimatedTokens returns the current token estimate.
func (e *ContextRevisionEngine) GetEstimatedTokens() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.estimatedTokens
}

// GetMessageCount returns the number of messages processed.
func (e *ContextRevisionEngine) GetMessageCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.messageCount
}

// GetSearchStrategy returns the configured search strategy.
func (e *ContextRevisionEngine) GetSearchStrategy() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.Revision.SearchStrategy
}

// IsSemanticSearchEnabled returns whether semantic search is enabled.
func (e *ContextRevisionEngine) IsSemanticSearchEnabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.Revision.EnableSemanticSearch
}

// Reset resets all statistics.
func (e *ContextRevisionEngine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.messageCount = 0
	e.estimatedTokens = 0
	e.lastSummarized = time.Now()
	e.lastRevisionTime = time.Now()
	e.recentOutputs = e.recentOutputs[:0]
}

// shouldRevise checks if context revision should be triggered.
func (e *ContextRevisionEngine) shouldRevise() bool {
	maxTokens := e.cfg.Revision.MaxContextTokens
	if maxTokens <= 0 {
		maxTokens = 64000
	}

	// Trigger revision when estimated tokens exceed threshold
	threshold := int(float64(maxTokens) * (1.0 - e.cfg.Revision.TrimRatio))
	if e.estimatedTokens > threshold {
		return true
	}

	// Or when too many messages accumulated
	if e.messageCount > 100 {
		return true
	}

	// Or when too much time has passed
	if time.Since(e.lastRevisionTime) > 5*time.Minute {
		return true
	}

	return false
}

// addRecentOutput adds output to the ring buffer.
func (e *ContextRevisionEngine) addRecentOutput(output string) {
	if len(e.recentOutputs) >= e.maxRecent {
		// Shift left
		e.recentOutputs = e.recentOutputs[1:]
	}
	e.recentOutputs = append(e.recentOutputs, output)
}

// Context key for revision signal.
type ctxKey string

const ctxKeyRevision ctxKey = "context_revision"

// truncateContent truncates content to max tokens (approximate).
func truncateContent(content string, maxTokens int) string {
	maxChars := maxTokens * 4
	if len(content) <= maxChars {
		return content
	}
	return content[:maxChars] + fmt.Sprintf(
		"\n... [truncated, original: %d chars]", len(content),
	)
}

// ContextManager is the legacy wrapper that delegates to
// ContextRevisionEngine. Maintains backward compatibility.
type ContextManager struct {
	engine *ContextRevisionEngine
	cfg    *config.WukongConfig
}

// NewContextManager creates a new context manager.
func NewContextManager(cfg *config.WukongConfig) *ContextManager {
	return &ContextManager{
		engine: NewContextRevisionEngine(cfg),
		cfg:    cfg,
	}
}

// PrepareContext is called before each agent run.
func (m *ContextManager) PrepareContext(
	ctx context.Context, sessionKey session.Key,
) context.Context {
	return m.engine.PrepareContext(ctx, sessionKey)
}

// AfterRun is called after each agent run.
func (m *ContextManager) AfterRun(
	ctx context.Context, response string, evts []event.Event,
) {
	m.engine.AfterRun(ctx, response, evts)
}

// ShouldSummarize returns whether summarization is needed.
func (m *ContextManager) ShouldSummarize() bool {
	return m.engine.ShouldSummarize()
}

// GetEstimatedTokens returns the current token estimate.
func (m *ContextManager) GetEstimatedTokens() int {
	return m.engine.GetEstimatedTokens()
}

// GetMessageCount returns the message count.
func (m *ContextManager) GetMessageCount() int {
	return m.engine.GetMessageCount()
}

// Reset resets all statistics.
func (m *ContextManager) Reset() {
	m.engine.Reset()
}

// GetEngine returns the underlying revision engine.
func (m *ContextManager) GetEngine() *ContextRevisionEngine {
	return m.engine
}

// SetSessionService injects the session service.
func (m *ContextManager) SetSessionService(svc session.Service) {
	m.engine.SetSessionService(svc)
}
