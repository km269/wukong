package config

import "time"

// ============================================================================
// Browser & Feature Tools Configuration
// ============================================================================

// BrowserBackendType defines the browser automation backend.
type BrowserBackendType string

const (
	BackendChromedp BrowserBackendType = "chromedp"
	BackendRod      BrowserBackendType = "rod"
)

// BrowserConfig defines web automation and file caching settings.
type BrowserConfig struct {
	Enabled         bool              `mapstructure:"enabled"`
	BrowserType     string            `mapstructure:"browser_type"`
	Backend         BrowserBackendType `mapstructure:"backend"`
	Headless        bool              `mapstructure:"headless"`
	CacheDir        string            `mapstructure:"cache_dir"`
	MaxDownloadSize int64             `mapstructure:"max_download_size"`
	Timeout         time.Duration     `mapstructure:"timeout"`
	BrowserPath     string            `mapstructure:"browser_path"`
	Stealth         bool              `mapstructure:"stealth"`
	Scroll          bool              `mapstructure:"scroll"`
	ControlURL      string            `mapstructure:"control_url"`
	Workers         int               `mapstructure:"workers"`
	ProfileDir      string            `mapstructure:"profile_dir"`
	ViewportWidth   int               `mapstructure:"viewport_width"`
	ViewportHeight  int               `mapstructure:"viewport_height"`
	Search          SearchConfig      `mapstructure:"search"`
	Proxy           ProxyConfig       `mapstructure:"proxy"`
}

// ProxyConfig defines proxy settings for browser automation.
type ProxyConfig struct {
	Enabled bool     `mapstructure:"enabled"`
	// Pool is a list of proxy URLs (http://user:pass@host:port or socks5://...)
	Pool []string `mapstructure:"pool"`
	// RotateEvery specifies how many requests to make before rotating proxy (0 = never rotate)
	RotateEvery int `mapstructure:"rotate_every"`
	// Current is the index of the current proxy in use
	Current int `mapstructure:"-"`
}

// SearchConfig defines search engine configurations.
type SearchConfig struct {
	Backends   []string       `mapstructure:"backends"`
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

// VisualiserConfig defines chart and diagram generation settings.
type VisualiserConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	OutputDir  string `mapstructure:"output_dir"`
	MaxWidth   int    `mapstructure:"max_width"`
	MaxHeight  int    `mapstructure:"max_height"`
}

// TutorialConfig defines interactive tutorial settings.
type TutorialConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Language string `mapstructure:"language"`
}

// TopOfMindConfig defines persistent instruction injection settings.
type TopOfMindConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	InstructionFile string `mapstructure:"instruction_file"`
	MaxLength       int    `mapstructure:"max_length"`
}

// CodeModeConfig defines JavaScript code execution sandbox settings.
type CodeModeConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	Timeout      time.Duration `mapstructure:"timeout"`
	MaxMemoryMB  int           `mapstructure:"max_memory_mb"`
}