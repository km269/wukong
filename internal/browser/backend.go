package browser

import (
	"time"

	"github.com/km269/wukong/internal/browser/rodbackend"
	"github.com/km269/wukong/internal/browser/types"
	"github.com/km269/wukong/internal/config"
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
}

func NewBackend(backendType BackendType, opts BackendOptions) types.BrowserBackend {
	switch backendType {
	case BackendRod:
		rodBackend := rodbackend.New(rodbackend.Options{
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
		})
		return rodBackend
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
		})
	}
}

func NewBackendFromConfig(cfg *config.BrowserConfig) types.BrowserBackend {
	if cfg == nil {
		return nil
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
