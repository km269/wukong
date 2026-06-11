package agent

import (
	"context"
	"sync"
	"time"

	"github.com/km269/wukong/internal/config"
)

// ContextManager handles token optimization and context revision.
// It implements strategies similar to Goose's context revision:
// - Summarization of long conversations
// - Token budget management
// - Stale context pruning
type ContextManager struct {
	mu sync.RWMutex

	cfg *config.WukongConfig

	// Runtime statistics
	messageCount   int
	estimatedTokens int
	lastSummarized time.Time
}

// NewContextManager creates a new context manager.
func NewContextManager(cfg *config.WukongConfig) *ContextManager {
	return &ContextManager{
		cfg:            cfg,
		lastSummarized: time.Now(),
	}
}

// PrepareContext is called before each agent run to prepare the context.
// It returns a possibly modified context with optimization signals.
func (m *ContextManager) PrepareContext(ctx context.Context) context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messageCount++

	// Estimate tokens for this message (rough: 4 chars ≈ 1 token)
	m.estimatedTokens += 100 // conservative estimate per message

	return ctx
}

// AfterRun is called after each agent run to analyze and potentially
// optimize the context for future runs.
func (m *ContextManager) AfterRun(ctx context.Context, response string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Estimate tokens from response
	if len(response) > 0 {
		m.estimatedTokens += len(response) / 4
	}

	// Check if summarization should be triggered
	if m.cfg.Session.EnableSummary &&
		m.messageCount >= m.cfg.Session.SummaryTrigger &&
		time.Since(m.lastSummarized) > 30*time.Second {

		m.lastSummarized = time.Now()
		// Note: Actual summarization is handled by the Session Service
		// via its built-in summarizer. This just tracks the trigger.
	}
}

// ShouldSummarize returns whether the context should be summarized.
func (m *ContextManager) ShouldSummarize() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.cfg.Session.EnableSummary {
		return false
	}
	return m.messageCount >= m.cfg.Session.SummaryTrigger
}

// GetEstimatedTokens returns the current token estimate.
func (m *ContextManager) GetEstimatedTokens() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.estimatedTokens
}

// GetMessageCount returns the number of messages processed.
func (m *ContextManager) GetMessageCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.messageCount
}

// Reset resets all statistics (e.g., on session restart).
func (m *ContextManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messageCount = 0
	m.estimatedTokens = 0
	m.lastSummarized = time.Now()
}
