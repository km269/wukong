package config

import "time"

// ============================================================================
// CortexDB Memory Stack Configuration
// ============================================================================

// CortexConfig defines the CortexDB-based intelligent recall and
// knowledge storage settings.
type CortexConfig struct {
	Enabled              bool   `mapstructure:"enabled"`
	DBPath               string `mapstructure:"db_path"`
	MaxResults           int    `mapstructure:"max_results"`
	MaxMessagesPerSession int   `mapstructure:"max_messages_per_session"`
	EmbeddingBaseURL     string `mapstructure:"embedding_base_url"`
	EmbeddingAPIKey      string `mapstructure:"embedding_api_key"`
	EmbeddingModel       string `mapstructure:"embedding_model"`
}

// MemoryFlowConfig defines the CortexDB MemoryFlow settings for
// conversation transcript recording, wake-up context generation,
// and fact promotion.
type MemoryFlowConfig struct {
	Enabled             bool   `mapstructure:"enabled"`
	DBPath              string `mapstructure:"db_path"`
	Namespace           string `mapstructure:"namespace"`
	EmbeddingDimensions int    `mapstructure:"embedding_dimensions"`
	PlannerModel        string `mapstructure:"planner_model"`
	ExtractorModel      string `mapstructure:"extractor_model"`
}

// GraphFlowConfig defines the CortexDB GraphFlow settings for
// entity/relationship extraction and knowledge graph construction.
type GraphFlowConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	DBPath       string `mapstructure:"db_path"`
	ExtractorModel string `mapstructure:"extractor_model"`
	MaxCharsPerDoc int  `mapstructure:"max_chars_per_doc"`
	AutoExtract  bool   `mapstructure:"auto_extract"`
}

// ImportFlowConfig defines the CortexDB ImportFlow settings for
// structured data import (DDL -> KG mapping, CSV -> RAG+KG).
type ImportFlowConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	DBPath  string `mapstructure:"db_path"`
}

// ============================================================================
// Context Management Configuration
// ============================================================================

// RevisionConfig defines context window management and token optimization.
type RevisionConfig struct {
	Enabled                    bool    `mapstructure:"enabled"`
	RevisionProvider           string  `mapstructure:"revision_provider"`
	RevisionModel              string  `mapstructure:"revision_model"`
	EnableLLMSummarize         bool    `mapstructure:"enable_llm_summarize"`
	MaxCommandOutput           int     `mapstructure:"max_command_output"`
	EnableSemanticSearch       bool    `mapstructure:"enable_semantic_search"`
	SearchStrategy             string  `mapstructure:"search_strategy"`
	MaxContextTokens           int     `mapstructure:"max_context_tokens"`
	TrimRatio                  float64 `mapstructure:"trim_ratio"`
	SummaryCooldown            time.Duration `mapstructure:"summary_cooldown"`
	SummaryTimeout             time.Duration `mapstructure:"summary_timeout"`
}