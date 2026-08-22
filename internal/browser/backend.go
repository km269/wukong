package browser

import (
	"fmt"
	"log/slog"
	"time"

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
}

func NewBackend(backendType BackendType, opts BackendOptions) (types.BrowserBackend, error) {
	switch backendType {
	case BackendRod:
		rodBackend, err := rodbackend.New(rodbackend.Options{
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
		})
		if err != nil {
			logutil.Warn("rod backend failed, falling back to chromedp",
				slog.String("error", err.Error()))
			// Fallback to chromedp if rod fails
			return New(Options{
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
			}), nil
		}
		return rodBackend, nil
	default:
		return New(Options{
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
		}), nil
	}
}

func NewBackendFromConfig(cfg *config.BrowserConfig) (types.BrowserBackend, error) {
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

	return NewBackend(backendType, BackendOptions{
		Headless:         cfg.Headless,
		Workers:          workers,
		Settle:           settleTimeout,
		RenderTimeout:    cfg.Timeout,
		Scroll:           cfg.Scroll,
		ChromeBin:        cfg.BrowserPath,
		ControlURL:       cfg.ControlURL,
		Stealth:          cfg.Stealth,
		ProfileDir:       cfg.ProfileDir,
		DisableDownloads: true,
		Proxy:            proxy,
	})
}
