package antibot

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// Level defines the stealth escalation level.
type Level int

const (
	// LevelNone — no anti-bot measures, default behaviour.
	LevelNone Level = 0

	// LevelFlags — Chrome anti-detection flags only (no JS injection).
	// Adds --disable-blink-features=AutomationControlled and related flags.
	LevelFlags Level = 1

	// LevelStealth — full stealth JS injection + all Chrome flags.
	// Injects anti-detection scripts via Page.addScriptToEvaluateOnNewDocument.
	LevelStealth Level = 2

	// LevelAggressive — LevelStealth + randomised delays + UA rotation.
	// Adds 2-8s random delays between requests and rotates User-Agent.
	LevelAggressive Level = 3

	// LevelBackoff — exponential backoff + report failure.
	// Gives up on the current URL after retries, logs the incident.
	LevelBackoff Level = 4
)

// String returns a human-readable level name.
func (l Level) String() string {
	switch l {
	case LevelNone:
		return "none"
	case LevelFlags:
		return "flags"
	case LevelStealth:
		return "stealth"
	case LevelAggressive:
		return "aggressive"
	case LevelBackoff:
		return "backoff"
	default:
		return fmt.Sprintf("unknown(%d)", l)
	}
}

// EscalationEvent records a single escalation decision.
type EscalationEvent struct {
	Time     time.Time
	URL      string
	Reason   BlockReason
	OldLevel Level
	NewLevel Level
	Retry    bool
}

// ProxyPool is an interface for proxy rotation integration.
type ProxyPool interface {
	GetProxy() string
	ReportFailure(proxyURL string)
	RotatedIndex() int
	Count() int
}

// Escalator manages the auto-escalation of anti-bot measures
// based on detected blocking patterns.
type Escalator struct {
	mu sync.Mutex

	CurrentLevel Level
	MaxLevel     Level
	AutoEscalate bool
	retries      map[string]int
	MaxRetries   int
	Cooldown     time.Duration
	History      []EscalationEvent
	rng          *rand.Rand
	tlsManager   *TLSProfileManager
	proxyPool    ProxyPool
}

// EscalatorConfig configures the auto-escalation engine.
type EscalatorConfig struct {
	// InitialLevel is the starting stealth level (default LevelNone).
	InitialLevel Level

	// MaxLevel caps escalation (default LevelAggressive).
	MaxLevel Level

	// AutoEscalate enables automatic escalation (default true).
	AutoEscalate bool

	// MaxRetries per URL before giving up (default 3).
	MaxRetries int

	// Cooldown between retries of the same URL (default 30s).
	Cooldown time.Duration
}

// DefaultEscalatorConfig returns sensible defaults.
func DefaultEscalatorConfig() EscalatorConfig {
	return EscalatorConfig{
		InitialLevel: LevelNone,
		MaxLevel:     LevelAggressive,
		AutoEscalate: true,
		MaxRetries:   3,
		Cooldown:     30 * time.Second,
	}
}

// NewEscalator creates a new auto-escalation engine.
func NewEscalator(cfg EscalatorConfig) *Escalator {
	return &Escalator{
		CurrentLevel: cfg.InitialLevel,
		MaxLevel:     cfg.MaxLevel,
		AutoEscalate: cfg.AutoEscalate,
		retries:      make(map[string]int),
		MaxRetries:   cfg.MaxRetries,
		Cooldown:     cfg.Cooldown,
		History:      make([]EscalationEvent, 0, 64),
		rng:          rand.New(rand.NewSource(time.Now().UnixNano())),
		tlsManager:   NewTLSProfileManager(),
	}
}

// Check evaluates whether anti-bot blocking was detected and returns
// the recommended action. It auto-escalates if configured.
//
// Returns:
//   - shouldRetry: whether the caller should retry the URL
//   - retryDelay: how long to wait before retrying
//   - newLevel: the new stealth level to use (caller should apply if
//     different from current)
//   - message: human-readable diagnostic message
func (e *Escalator) Check(url string, reason BlockReason,
	statusCode int) (shouldRetry bool, retryDelay time.Duration,
	newLevel Level, message string) {

	e.mu.Lock()
	defer e.mu.Unlock()

	oldLevel := e.CurrentLevel

	// Record the hit.
	e.retries[url]++

	if !e.AutoEscalate || !ShouldRetry(reason) {
		return false, 0, e.CurrentLevel,
			fmt.Sprintf("non-retryable block: %s (HTTP %d)", reason, statusCode)
	}

	// Check if we've exceeded max retries for this URL.
	if e.retries[url] > e.MaxRetries {
		e.CurrentLevel = LevelBackoff
		e.recordEvent(url, reason, oldLevel, LevelBackoff, false)
		return false, 0, LevelBackoff,
			fmt.Sprintf("max retries (%d) exceeded for %s", e.MaxRetries, url)
	}

	// Escalate one level (unless already at max).
	newLevel = oldLevel + 1
	if newLevel > e.MaxLevel {
		newLevel = e.MaxLevel
	}
	e.CurrentLevel = newLevel

	// Calculate retry delay. Cloudflare and rate-limit retries get longer
	// backoff because these services track IP/provenance and cool down slowly.
	delay := e.Cooldown
	if delay <= 0 {
		delay = 10 * time.Second // Safe default when unconfigured.
	}
	cfMultiplier := int64(1)
	if reason == ReasonCloudflare || reason == ReasonRateLimited {
		cfMultiplier = 2 // Double delay for Cloudflare/rate-limit.
	}
	switch newLevel {
	case LevelAggressive:
		jit := int64(delay) * 3
		if jit < 1 {
			jit = 1
		}
		delay = delay + time.Duration(e.rng.Int63n(jit))
	case LevelStealth:
		jit := int64(delay)
		if jit < 1 {
			jit = 1
		}
		delay = delay + time.Duration(e.rng.Int63n(jit))
	default:
		delay = delay / 2
	}
	delay *= time.Duration(cfMultiplier)

	e.recordEvent(url, reason, oldLevel, newLevel, true)

	if newLevel >= LevelAggressive {
		e.tlsManager.Rotate()
		if e.proxyPool != nil && e.proxyPool.Count() > 0 {
			proxy := e.proxyPool.GetProxy()
			e.proxyPool.ReportFailure(proxy)
		}
	}

	return true, delay, newLevel,
		fmt.Sprintf("escalated %s → %s for %s (retry %d/%d, delay %v)",
			oldLevel, newLevel, url, e.retries[url], e.MaxRetries, delay)
}

// TLSFlags returns Chrome TLS flags for the current escalation level.
// At LevelAggressive and above, TLS profile is rotated.
func (e *Escalator) TLSFlags() []ChromeFlag {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tlsManager.FlagsForLevel(e.CurrentLevel)
}

// SetProxyPool sets the proxy pool for rotation on escalation.
func (e *Escalator) SetProxyPool(pool ProxyPool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.proxyPool = pool
}

// RetryCount returns the number of retries for a URL.
func (e *Escalator) RetryCount(url string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.retries[url]
}

// UAProfile contains a complete set of browser identification headers.
// All headers must be consistent with each other to pass modern
// anti-bot fingerprinting checks (Cloudflare L3, FingerprintJS, etc.).
type UAProfile struct {
	UserAgent       string
	SecChUa         string
	SecChUaMobile   string
	SecChUaPlatform string
}

// RotateUserAgent returns a random realistic browser User-Agent profile.
// Used when escalation reaches LevelAggressive to diversify the
// fingerprint seen by anti-bot systems.
func (e *Escalator) RotateUserAgent() *UAProfile {
	e.mu.Lock()
	idx := e.rng.Intn(len(uaProfiles))
	e.mu.Unlock()
	return &uaProfiles[idx]
}

// GetRandomDesktopUA returns a random desktop UA profile.
func (e *Escalator) GetRandomDesktopUA() *UAProfile {
	e.mu.Lock()
	idx := e.rng.Intn(len(desktopUAProfiles))
	e.mu.Unlock()
	return &desktopUAProfiles[idx]
}

// uaProfiles contains a comprehensive set of real-world browser
// identification profiles covering Chrome, Firefox, Safari, Edge, Opera,
// Brave, Samsung Internet, and Vivaldi across desktop and mobile
// platforms. Each profile includes consistent User-Agent and client hint
// headers to pass modern anti-bot fingerprinting.
var uaProfiles = []UAProfile{
	// Chrome on Windows
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		SecChUa:         `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
		SecChUa:         `"Chromium";v="129", "Google Chrome";v="129", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36",
		SecChUa:         `"Chromium";v="128", "Google Chrome";v="128", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
	// Chrome on macOS
	{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		SecChUa:         `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"macOS"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
		SecChUa:         `"Chromium";v="129", "Google Chrome";v="129", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"macOS"`,
	},
	// Chrome on Linux
	{
		UserAgent:       "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		SecChUa:         `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Linux"`,
	},
	// Chrome on Android
	{
		UserAgent:       "Mozilla/5.0 (Linux; Android 14; Pixel 8 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.6721.117 Mobile Safari/537.36",
		SecChUa:         `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?1",
		SecChUaPlatform: `"Android"`,
	},
	// Firefox on Windows
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0",
		SecChUa:         `"Not A(Brand";v="8", "Chromium";v="132", "Firefox";v="132"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
	// Firefox on macOS
	{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:132.0) Gecko/20100101 Firefox/132.0",
		SecChUa:         `"Not A(Brand";v="8", "Chromium";v="132", "Firefox";v="132"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"macOS"`,
	},
	// Safari on macOS
	{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15",
		SecChUa:         `"Not_A Brand";v="8", "Chromium";v="130", "Safari";v="605.1.15"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"macOS"`,
	},
	// Safari on iPhone
	{
		UserAgent:       "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
		SecChUa:         `"Not_A Brand";v="8", "Chromium";v="130", "Safari";v="605.1.15"`,
		SecChUaMobile:   "?1",
		SecChUaPlatform: `"iOS"`,
	},
	// Edge on Windows
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0",
		SecChUa:         `"Chromium";v="130", "Microsoft Edge";v="130", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
	// Edge on macOS
	{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0",
		SecChUa:         `"Chromium";v="130", "Microsoft Edge";v="130", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"macOS"`,
	},
	// Opera on Windows
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 OPR/114.0.0.0",
		SecChUa:         `"Chromium";v="128", "Opera";v="114", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
	// Brave on Windows
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Brave/1.69.0",
		SecChUa:         `"Chromium";v="129", "Brave";v="1.69", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
}

// desktopUAProfiles contains only desktop browser profiles.
var desktopUAProfiles = []UAProfile{
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		SecChUa:         `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		SecChUa:         `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"macOS"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		SecChUa:         `"Chromium";v="130", "Google Chrome";v="130", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Linux"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0",
		SecChUa:         `"Not A(Brand";v="8", "Chromium";v="132", "Firefox";v="132"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15",
		SecChUa:         `"Not_A Brand";v="8", "Chromium";v="130", "Safari";v="605.1.15"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"macOS"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0",
		SecChUa:         `"Chromium";v="130", "Microsoft Edge";v="130", "Not?A_Brand";v="99"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
}

// Reset clears escalation state (useful when restarting a clone).
func (e *Escalator) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.CurrentLevel = LevelNone
	e.retries = make(map[string]int)
	e.History = e.History[:0]
}

// JitterDelay returns a random delay for the current level.
func (e *Escalator) JitterDelay() time.Duration {
	e.mu.Lock()
	level := e.CurrentLevel
	e.mu.Unlock()

	switch level {
	case LevelAggressive:
		return time.Duration(2+e.rng.Int63n(6)) * time.Second // 2-8s
	case LevelStealth:
		return time.Duration(1+e.rng.Int63n(3)) * time.Second // 1-4s
	case LevelFlags:
		return time.Duration(500+e.rng.Int63n(1500)) * time.Millisecond // 0.5-2s
	default:
		return 0
	}
}

// Stats returns a diagnostic summary of the escalation engine.
func (e *Escalator) Stats() string {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.History) == 0 {
		return "antibot: no blocking detected"
	}

	var reasons []string
	for _, ev := range e.History {
		reasons = append(reasons, string(ev.Reason))
	}
	reasonCounts := make(map[string]int)
	for _, r := range reasons {
		reasonCounts[r]++
	}

	return fmt.Sprintf(
		"antibot: level=%s, events=%d, blocked=%d URLs",
		e.CurrentLevel, len(e.History), len(e.retries),
	)
}

func (e *Escalator) recordEvent(url string, reason BlockReason,
	oldLevel, newLevel Level, retry bool) {
	e.History = append(e.History, EscalationEvent{
		Time:     time.Now(),
		URL:      url,
		Reason:   reason,
		OldLevel: oldLevel,
		NewLevel: newLevel,
		Retry:    retry,
	})
}

// Wait blocks for the jitter delay of the current level.
func (e *Escalator) Wait(ctx context.Context) error {
	delay := e.JitterDelay()
	if delay == 0 {
		return nil
	}
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
