package config

import "time"

// ============================================================================
// Browser Configuration
// ============================================================================

// BrowserBackendType defines the browser automation backend.
type BrowserBackendType string

const (
	BackendChromedp BrowserBackendType = "chromedp"
	BackendRod      BrowserBackendType = "rod"
)

// BrowserConfig defines web automation and file caching settings.
type BrowserConfig struct {
	Enabled           bool               `mapstructure:"enabled"`
	BrowserType       string             `mapstructure:"browser_type"`
	Backend           BrowserBackendType `mapstructure:"backend"`
	Headless          bool               `mapstructure:"headless"`
	CacheDir          string             `mapstructure:"cache_dir"`
	MaxDownloadSize   int64              `mapstructure:"max_download_size"`
	Timeout           time.Duration      `mapstructure:"timeout"`
	BrowserPath       string             `mapstructure:"browser_path"`
	Stealth           bool               `mapstructure:"stealth"`
	Scroll            bool               `mapstructure:"scroll"`
	ControlURL        string             `mapstructure:"control_url"`
	Workers           int                `mapstructure:"workers"`
	GlobalRenderSlots int                `mapstructure:"global_render_slots"`
	ProfileDir        string             `mapstructure:"profile_dir"`
	ViewportWidth     int                `mapstructure:"viewport_width"`
	ViewportHeight    int                `mapstructure:"viewport_height"`
	// GeoRegion pins the browser fingerprint's geography (timezone,
	// languages, Accept-Language) to the proxy exit region: one of
	// cn, us-east, us-west, us-central, gb, de, fr, jp, kr, sg.
	// Empty = infer from the proxy URL when possible, else random.
	GeoRegion string `mapstructure:"geo_region"`
	Search    SearchConfig       `mapstructure:"search"`
	Proxy     ProxyConfig        `mapstructure:"proxy"`
}

// ProxyConfig defines proxy settings for browser automation.
type ProxyConfig struct {
	Enabled     bool     `mapstructure:"enabled"`
	Pool        []string `mapstructure:"pool"`
	RotateEvery int      `mapstructure:"rotate_every"`
	Current     int      `mapstructure:"-"`
}

// SearchConfig defines search engine configurations.
// Each backend is activated by its own Enabled field; the
// Backends list has been removed.
type SearchConfig struct {
	DuckDuckGo DuckDuckGoConfig `mapstructure:"duckduckgo"`
	SearXNG    SearXNGConfig    `mapstructure:"searxng"`
	Tavily     TavilyConfig     `mapstructure:"tavily"`
	Google     GoogleConfig     `mapstructure:"google"`
	Bing       BingConfig       `mapstructure:"bing"`
}

// DuckDuckGoConfig defines DuckDuckGo search configuration.
type DuckDuckGoConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	URL     string `mapstructure:"url"`
}

// SearXNGConfig defines SearXNG search configuration.
type SearXNGConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	URL     string `mapstructure:"url"`
	APIKey  string `mapstructure:"api_key"`
}

// TavilyConfig defines Tavily AI search configuration.
type TavilyConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIKey  string `mapstructure:"api_key"`
}

// GoogleConfig defines Google search configuration.
type GoogleConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIKey  string `mapstructure:"api_key"`
	CSEID   string `mapstructure:"cse_id"`
}

// BingConfig defines Bing search configuration.
type BingConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	APIKey  string `mapstructure:"api_key"`
}
