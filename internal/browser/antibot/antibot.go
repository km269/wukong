// Package antibot provides automatic anti-bot detection and response
// for website cloning. It detects common blocking patterns and escalates
// stealth measures through five configurable levels.
//
// Quick start:
//
//	ab := antibot.New(antibot.DefaultConfig())
//	reason, desc := ab.CheckResponse(resp.StatusCode, resp.Header, html)
//	if reason != antibot.ReasonNone {
//	    retry, delay, level, msg := ab.Escalate(url, reason, resp.StatusCode)
//	    if retry {
//	        time.Sleep(delay)
//	        // Re-render with level-appropriate stealth.
//	    }
//	}
package antibot

import (
	"net/http"
	"strings"
	"time"
)

// Engine combines detection and auto-escalation into a single interface.
type Engine struct {
	Escalator *Escalator
}

// Config configures the anti-bot engine.
type Config struct {
	// Enabled enables the anti-bot detection and response system.
	Enabled bool

	// AutoEscalate enables automatic escalation on detection.
	AutoEscalate bool

	// InitialLevel is the starting stealth level (default LevelNone).
	InitialLevel Level

	// MaxLevel caps escalation (default LevelAggressive).
	MaxLevel Level

	// MaxRetries per URL before giving up (default 3).
	MaxRetries int

	// Cooldown between retries (default 30s).
	Cooldown time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:      true,
		AutoEscalate: true,
		InitialLevel: LevelNone,
		MaxLevel:     LevelAggressive,
		MaxRetries:   3,
		Cooldown:     30 * time.Second,
	}
}

// New creates a new anti-bot engine.
func New(cfg Config) *Engine {
	if !cfg.Enabled {
		return &Engine{
			Escalator: NewEscalator(EscalatorConfig{
				AutoEscalate: false,
			}),
		}
	}
	return &Engine{
		Escalator: NewEscalator(EscalatorConfig{
			InitialLevel: cfg.InitialLevel,
			MaxLevel:     cfg.MaxLevel,
			AutoEscalate: cfg.AutoEscalate,
			MaxRetries:   cfg.MaxRetries,
			Cooldown:     cfg.Cooldown,
		}),
	}
}

// CheckResponse analyses an HTTP response and page content for anti-bot
// indicators. Returns the detected reason and a human-readable description.
func (e *Engine) CheckResponse(statusCode int, headers http.Header,
	htmlContent string) (BlockReason, string) {
	return Detect(statusCode, headers, htmlContent)
}

// CheckError analyses an error for anti-bot indicators (timeout, connection
// refused, etc.).
func (e *Engine) CheckError(err error) (BlockReason, string) {
	if err == nil {
		return ReasonNone, ""
	}
	errStr := err.Error()
	// Common anti-bot error patterns.
	if containsAny(errStr, "timeout", "deadline exceeded",
		"context deadline exceeded") {
		return ReasonTimeout, "request timed out — possible bot wall"
	}
	if containsAny(errStr, "connection refused", "connection reset",
		"EOF", "broken pipe") {
		return ReasonUnavailable,
			"connection dropped — possible rate limiting"
	}
	return ReasonNone, ""
}

// Escalate evaluates a detected block and returns the recommended action.
func (e *Engine) Escalate(url string, reason BlockReason, statusCode int,
) (shouldRetry bool, retryDelay time.Duration, newLevel Level,
	message string) {
	return e.Escalator.Check(url, reason, statusCode)
}

// Level returns the current stealth level.
func (e *Engine) Level() Level {
	return e.Escalator.CurrentLevel
}

// NeedsStealthFlags returns true if the current level requires
// anti-detection Chrome flags.
func (e *Engine) NeedsStealthFlags() bool {
	return e.Escalator.CurrentLevel >= LevelFlags
}

// NeedsStealthScript returns true if the current level requires
// JavaScript anti-detection injection.
func (e *Engine) NeedsStealthScript() bool {
	return e.Escalator.CurrentLevel >= LevelStealth
}

// JitterDelay returns a random delay appropriate for the current level.
func (e *Engine) JitterDelay() time.Duration {
	return e.Escalator.JitterDelay()
}

// Wait blocks for the jitter delay of the current level.
func (e *Engine) Wait() {
	delay := e.JitterDelay()
	if delay > 0 {
		time.Sleep(delay)
	}
}

// Stats returns a diagnostic summary.
func (e *Engine) Stats() string {
	return e.Escalator.Stats()
}

// Reset clears escalation state.
func (e *Engine) Reset() {
	e.Escalator.Reset()
}

// RotateUserAgent returns a random realistic browser User-Agent profile.
func (e *Engine) RotateUserAgent() *UAProfile {
	return e.Escalator.RotateUserAgent()
}

// GetRandomDesktopUA returns a random desktop UA profile.
func (e *Engine) GetRandomDesktopUA() *UAProfile {
	return e.Escalator.GetRandomDesktopUA()
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
