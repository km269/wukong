package config

import "time"

// ============================================================================
// Storage Configuration
// ============================================================================

// SessionConfig defines conversation history storage settings.
// Supports backends: sqlite (default), memory, redis.
type SessionConfig struct {
	Backend        string        `mapstructure:"backend"`
	DBPath         string        `mapstructure:"db_path"`
	EventLimit     int           `mapstructure:"event_limit"`
	TTL            time.Duration `mapstructure:"ttl"`
	EnableSummary  bool          `mapstructure:"enable_summary"`
	SummaryTrigger int           `mapstructure:"summary_trigger"`
	RedisURL       string        `mapstructure:"redis_url"`
	// EnableModelEventLog turns on the Wukong-level model-visible
	// event log (wukong_model_events table). This is distinct from
	// the framework session service's own event log: it records the
	// messages the model actually sees AFTER context enrichment
	// (wakeup/recall/persistent memory injection), enforcing the
	// "model-visible means logged" invariant. Default true.
	EnableModelEventLog bool `mapstructure:"enable_model_event_log"`
}

// MemoryConfig defines long-term knowledge persistence settings.
type MemoryConfig struct {
	Backend                 string        `mapstructure:"backend"`
	DBPath                  string        `mapstructure:"db_path"`
	MaxMemories             int           `mapstructure:"max_memories"`
	AutoExtract             bool          `mapstructure:"auto_extract"`
	ExtractTimeout          time.Duration `mapstructure:"extract_timeout"`
	ExtractorProvider       string        `mapstructure:"extractor_provider"`
	ExtractorModel          string        `mapstructure:"extractor_model"`
	ExtractorPrompt         string        `mapstructure:"extractor_prompt"`
	RecencyWeight           float64       `mapstructure:"recency_weight"`
	ReferenceWeight         float64       `mapstructure:"reference_weight"`
	ImportanceWeight        float64       `mapstructure:"importance_weight"`
	LengthWeight            float64       `mapstructure:"length_weight"`
	DynamicTTL              bool          `mapstructure:"dynamic_ttl"`
	EnableSmartCleanup      bool          `mapstructure:"enable_smart_cleanup"`
	CleanupTriggerThreshold float64       `mapstructure:"cleanup_trigger_threshold"`
	CleanupTargetThreshold  float64       `mapstructure:"cleanup_target_threshold"`
	MemoryTTL               time.Duration `mapstructure:"memory_ttl"`
}

// TodoConfig defines task tracking storage settings.
type TodoConfig struct {
	Backend          string `mapstructure:"backend"`
	DBPath           string `mapstructure:"db_path"`
	EnableNativeTodo bool   `mapstructure:"enable_native_todo"`
	EnableEnforcer   bool   `mapstructure:"enable_enforcer"`
}

// RecallConfig defines cross-session chat history search settings.
type RecallConfig struct {
	Enabled               bool   `mapstructure:"enabled"`
	Backend               string `mapstructure:"backend"`
	DBPath                string `mapstructure:"db_path"`
	MaxResults            int    `mapstructure:"max_results"`
	MaxMessagesPerSession int    `mapstructure:"max_messages_per_session"`
	SearchMode            string `mapstructure:"search_mode"`
	EmbeddingModel        string `mapstructure:"embedding_model"`
	// SearchStrategy overrides the hardcoded hybrid weights.
	// When nil, SearchMode field is used (backward compat).
	SearchStrategy *SearchStrategyConfig `mapstructure:"search_strategy"`
}
