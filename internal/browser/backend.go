package browser

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/km269/wukong/internal/browser/renderkit"
	"github.com/km269/wukong/internal/browser/rodbackend"
	"github.com/km269/wukong/internal/browser/types"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/pkg/logutil"
)

var globalProxyPool *SmartProxyPool

func InitGlobalProxyPool(proxyURLs []string, healthCheckInterval time.Duration) {
	globalProxyPool = NewSmartProxyPool(proxyURLs, healthCheckInterval)
}

func GlobalProxyPool() *SmartProxyPool {
	return globalProxyPool
}

type BackendType = config.BrowserBackendType

const (
	BackendChromedp = config.BackendChromedp
	BackendRod      = config.BackendRod
)

type BackendOptions struct {
	Headless         bool
	Workers          int
	Settle           time.Duration
	RenderTimeout    time.Duration
	Scroll           bool
	ChromeBin        string
	ControlURL       string
	Stealth          bool
	ProfileDir       string
	DisableDownloads bool
	Proxy            string // Proxy URL (http://user:pass@host:port or socks5://...)
	// InsecureTLS disables Chrome certificate verification (opt-out for
	// intranet/.mil certs). Strict verification is the default.
	InsecureTLS bool
	// GlobalRenderSlots sizes the process-wide render budget shared by
	// every pool (first caller to install it wins). 0 = auto
	// (max(4, NumCPU)); negative disables the global budget entirely.
	GlobalRenderSlots int
	// GeoRegion pins the fingerprint geography to the proxy exit
	// region (timezone/languages/Accept-Language coherence — see
	// stealth.GeoProfile). Empty = infer from Proxy, else random.
	GeoRegion string
}

// NewBackend creates the selected browser backend. The pool's
// lifetime is bound to ctx: cancelling ctx drains the pool and
// releases the browser process; Close() remains the explicit,
// idempotent cleanup path.
func NewBackend(ctx context.Context, backendType BackendType, opts BackendOptions) (types.BrowserBackend, error) {
	// Install the process-wide render budget before any pool exists so
	// every pool created from here on shares one admission ceiling and
	// its resource watermarks. Idempotent; first caller's size wins.
	renderkit.EnsureGlobalBudget(opts.GlobalRenderSlots)

	switch backendType {
	case BackendRod:
		rodBackend, err := rodbackend.New(ctx, rodbackend.Options{
			Headless:         opts.Headless,
			Workers:          opts.Workers,
			Settle:           opts.Settle,
			RenderTimeout:    opts.RenderTimeout,
			Scroll:           opts.Scroll,
			ChromeBin:        opts.ChromeBin,
			ControlURL:       opts.ControlURL,
			Stealth:          opts.Stealth,
			ProfileDir:       opts.ProfileDir,
			DisableDownloads: opts.DisableDownloads,
			Proxy:            opts.Proxy,
			InsecureTLS:      opts.InsecureTLS,
			GeoRegion:        opts.GeoRegion,
		})
		if err != nil {
			logutil.Warn("rod backend failed, falling back to chromedp",
				slog.String("error", err.Error()))
			// Fallback to chromedp if rod fails
			return New(ctx, Options{
				Headless:         opts.Headless,
				Workers:          opts.Workers,
				Settle:           opts.Settle,
				RenderTimeout:    opts.RenderTimeout,
				Scroll:           opts.Scroll,
				ChromeBin:        opts.ChromeBin,
				ControlURL:       opts.ControlURL,
				Stealth:          opts.Stealth,
				DisableDownloads: opts.DisableDownloads,
				Proxy:            opts.Proxy,
				InsecureTLS:      opts.InsecureTLS,
				GeoRegion:        opts.GeoRegion,
			}), nil
		}
		return rodBackend, nil
	default:
		return New(ctx, Options{
			Headless:         opts.Headless,
			Workers:          opts.Workers,
			Settle:           opts.Settle,
			RenderTimeout:    opts.RenderTimeout,
			Scroll:           opts.Scroll,
			ChromeBin:        opts.ChromeBin,
			ControlURL:       opts.ControlURL,
			Stealth:          opts.Stealth,
			DisableDownloads: opts.DisableDownloads,
			Proxy:            opts.Proxy,
			InsecureTLS:      opts.InsecureTLS,
			GeoRegion:        opts.GeoRegion,
		}), nil
	}
}

// NewBackendFromConfig builds a backend from config. Long-lived
// callers that manage cleanup via Close() (e.g. the Controller) pass
// context.Background(); task-scoped callers pass their task context.
func NewBackendFromConfig(ctx context.Context, cfg *config.BrowserConfig) (types.BrowserBackend, error) {
	if cfg == nil {
		return nil, fmt.Errorf("browser config is nil")
	}

	settleTimeout := 2 * time.Second
	if cfg.Timeout > 0 {
		settleTimeout = cfg.Timeout / 3
		if settleTimeout < 1*time.Second {
			settleTimeout = 1 * time.Second
		}
	}

	workers := 4
	if cfg.Workers > 0 {
		workers = cfg.Workers
	}

	backendType := BackendChromedp
	if cfg.Backend == BackendRod {
		backendType = BackendRod
	}

	if cfg.Proxy.Enabled && len(cfg.Proxy.Pool) > 0 && globalProxyPool == nil {
		globalProxyPool = NewSmartProxyPool(cfg.Proxy.Pool, 30*time.Second)
	}

	var proxy string
	if globalProxyPool != nil {
		proxy = globalProxyPool.GetProxy()
	}

	return NewBackend(ctx, backendType, BackendOptions{
		Headless:          cfg.Headless,
		Workers:           workers,
		Settle:            settleTimeout,
		RenderTimeout:     cfg.Timeout,
		Scroll:            cfg.Scroll,
		ChromeBin:         cfg.BrowserPath,
		ControlURL:        cfg.ControlURL,
		Stealth:           cfg.Stealth,
		ProfileDir:        cfg.ProfileDir,
		DisableDownloads:  true,
		Proxy:             proxy,
		GlobalRenderSlots: cfg.GlobalRenderSlots,
		GeoRegion:         cfg.GeoRegion,
	})
}
