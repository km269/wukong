// Package config provides configuration management for wukong.
// It defines the configuration structure and provides a Viper-based
// loader for YAML configuration files.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// WukongConfig is the top-level configuration structure.
type WukongConfig struct {
	DefaultProvider string            `mapstructure:"default_provider"`
	Providers       []ProviderConfig  `mapstructure:"providers"`
	Extensions      []ExtensionConfig `mapstructure:"extensions"`
	Session         SessionConfig     `mapstructure:"session"`
	Memory          MemoryConfig      `mapstructure:"memory"`
	Todo            TodoConfig        `mapstructure:"todo"`
	Agent           AgentConfig       `mapstructure:"agent"`
}

// ProviderConfig defines a model provider configuration.
type ProviderConfig struct {
	Name    string `mapstructure:"name"`
	Type    string `mapstructure:"type"`
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}

// ExtensionConfig defines an MCP extension configuration.
type ExtensionConfig struct {
	Name      string   `mapstructure:"name"`
	Type      string   `mapstructure:"type"`
	Transport string   `mapstructure:"transport"`
	Command   string   `mapstructure:"command"`
	Args      []string `mapstructure:"args"`
	URL       string   `mapstructure:"url"`
	Enabled   bool     `mapstructure:"enabled"`
}

// SessionConfig defines session storage configuration.
type SessionConfig struct {
	Backend        string        `mapstructure:"backend"`
	DBPath         string        `mapstructure:"db_path"`
	EventLimit     int           `mapstructure:"event_limit"`
	TTL            time.Duration `mapstructure:"ttl"`
	EnableSummary  bool          `mapstructure:"enable_summary"`
	SummaryTrigger int           `mapstructure:"summary_trigger"`
}

// MemoryConfig defines memory storage configuration.
type MemoryConfig struct {
	Backend     string `mapstructure:"backend"`
	DBPath      string `mapstructure:"db_path"`
	MaxMemories int    `mapstructure:"max_memories"`
	AutoExtract bool   `mapstructure:"auto_extract"`
}

// TodoConfig defines todo storage configuration.
type TodoConfig struct {
	Backend string `mapstructure:"backend"`
	DBPath  string `mapstructure:"db_path"`
}

// AgentConfig defines agent behavior configuration.
type AgentConfig struct {
	MaxLLMCalls        int           `mapstructure:"max_llm_calls"`
	MaxToolIterations  int           `mapstructure:"max_tool_iterations"`
	ParallelTools      bool          `mapstructure:"parallel_tools"`
	Streaming          bool          `mapstructure:"streaming"`
	MaxRunDuration     time.Duration `mapstructure:"max_run_duration"`
}

// Loader handles loading configuration from YAML files.
type Loader struct {
	v      *viper.Viper
	config *WukongConfig
}

// NewLoader creates a new configuration loader.
// configPath is an optional path to a custom config file.
func NewLoader(configPath string) (*Loader, error) {
	v := viper.New()
	l := &Loader{v: v}

	v.SetConfigName("config")
	v.SetConfigType("yaml")

	// Search paths in order of priority
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.AddConfigPath(".")
		homeDir, err := os.UserHomeDir()
		if err == nil {
			v.AddConfigPath(filepath.Join(homeDir, ".config", "wukong"))
		}
		v.AddConfigPath("/etc/wukong")
	}

	// Enable environment variable overrides
	v.SetEnvPrefix("WUKONG")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Set defaults
	l.setDefaults()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// Config file not found, use defaults
	}

	return l, nil
}

// Load parses the configuration into a WukongConfig.
func (l *Loader) Load() (*WukongConfig, error) {
	if l.config != nil {
		return l.config, nil
	}

	var cfg WukongConfig
	if err := l.v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// Expand environment variables in API keys
	for i := range cfg.Providers {
		cfg.Providers[i].APIKey = os.ExpandEnv(cfg.Providers[i].APIKey)
	}

	l.config = &cfg
	return l.config, nil
}

// GetConfig returns the currently loaded configuration.
func (l *Loader) GetConfig() *WukongConfig {
	return l.config
}

// FindProvider returns the provider configuration by name.
func (c *WukongConfig) FindProvider(name string) *ProviderConfig {
	for i := range c.Providers {
		if c.Providers[i].Name == name {
			return &c.Providers[i]
		}
	}
	return nil
}

// DefaultProvider returns the default provider configuration.
func (c *WukongConfig) DefaultProviderConfig() *ProviderConfig {
	return c.FindProvider(c.DefaultProvider)
}

// EnabledExtensions returns only enabled extensions.
func (c *WukongConfig) EnabledExtensions() []ExtensionConfig {
	var result []ExtensionConfig
	for _, ext := range c.Extensions {
		if ext.Enabled {
			result = append(result, ext)
		}
	}
	return result
}

func (l *Loader) setDefaults() {
	l.v.SetDefault("session.backend", "sqlite")
	l.v.SetDefault("session.db_path", "wukong.db")
	l.v.SetDefault("session.event_limit", 500)
	l.v.SetDefault("session.ttl", "0s")
	l.v.SetDefault("session.enable_summary", true)
	l.v.SetDefault("session.summary_trigger", 50)

	l.v.SetDefault("memory.backend", "sqlite")
	l.v.SetDefault("memory.db_path", "wukong.db")
	l.v.SetDefault("memory.max_memories", 100)
	l.v.SetDefault("memory.auto_extract", true)

	l.v.SetDefault("todo.backend", "sqlite")
	l.v.SetDefault("todo.db_path", "wukong.db")

	l.v.SetDefault("agent.max_llm_calls", 50)
	l.v.SetDefault("agent.max_tool_iterations", 30)
	l.v.SetDefault("agent.parallel_tools", true)
	l.v.SetDefault("agent.streaming", true)
	l.v.SetDefault("agent.max_run_duration", "300s")
}
