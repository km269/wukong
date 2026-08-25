package config

import "time"

// ============================================================================
// CortexDB Memory Stack Configuration
// ============================================================================

// CortexConfig defines the CortexDB-based intelligent recall and
// knowledge storage settings.
type CortexConfig struct {
	Enabled               bool   `mapstructure:"enabled"`
	DBPath                string `mapstructure:"db_path"`
	MaxResults            int    `mapstructure:"max_results"`
	MaxMessagesPerSession int    `mapstructure:"max_messages_per_session"`
	EmbeddingBaseURL      string `mapstructure:"embedding_base_url"`
	EmbeddingAPIKey       string `mapstructure:"embedding_api_key"`
	EmbeddingModel        string `mapstructure:"embedding_model"`
	// Reranker config. When RerankerModel is empty, reranking is
	// disabled. RerankerBaseURL/APIKey default to embedding values.
	RerankerBaseURL string `mapstructure:"reranker_base_url"`
	RerankerAPIKey  string `mapstructure:"reranker_api_key"`
	RerankerModel   string `mapstructure:"reranker_model"`
	// SearchStrategy defines the retrieval parameter space.
	// When nil/empty, defaults to hybrid 70/30.
	SearchStrategy *SearchStrategyConfig `mapstructure:"search_strategy"`
	// VerticalRouting enables query-intent-based dispatch to
	// specialised backends (arXiv, GitHub, Wikipedia, Reddit).
	// When enabled, queries targeting a known vertical are sent
	// to the platform's search API in addition to local retrieval.
	VerticalRouting *VerticalRoutingConfig `mapstructure:"vertical_routing"`
	// Chunking controls how long messages are split into
	// semantically coherent chunks before embedding. When nil,
	// defaults to enabled with 1200-rune chunks and 200 overlap.
	Chunking *ChunkingConfig `mapstructure:"chunking"`
}

// ChunkingConfig controls semantic text chunking for RAG ingestion.
type ChunkingConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// MaxSize is the max runes per chunk.
	MaxSize int `mapstructure:"max_size"`
	// Overlap is the rune overlap between consecutive chunks.
	Overlap int `mapstructure:"overlap"`
	// MinSize is the minimum chunk size; smaller chunks merge.
	MinSize int `mapstructure:"min_size"`
}

// VerticalRoutingConfig controls vertical domain search routing.
type VerticalRoutingConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	TopN         int           `mapstructure:"top_n"`
	Timeout      time.Duration `mapstructure:"timeout"`
	GitHubAPIKey string        `mapstructure:"github_api_key"`
	// MergeMode controls how vertical results combine with local
	// retrieval: "prepend" (vertical first), "append" (local first),
	// "replace" (vertical only). Default "prepend".
	MergeMode string `mapstructure:"merge_mode"`
}

// SearchStrategyConfig mirrors search.SearchGenome for YAML config.
// All fields are optional; zero values fall back to defaults.
type SearchStrategyConfig struct {
	RecallMode          string  `mapstructure:"recall_mode"`
	DenseWeight         float64 `mapstructure:"dense_weight"`
	TextWeight          float64 `mapstructure:"text_weight"`
	KeywordMatchPercent float64 `mapstructure:"keyword_match_percent"`
	MaxRetrievedNum     int     `mapstructure:"max_retrieved_num"`
	FTS5PoolSize        int     `mapstructure:"fts5_pool_size"`
	FusionMethod        string  `mapstructure:"fusion_method"`
	RRFK                float64 `mapstructure:"rrf_k"`
	RerankerEnabled     bool    `mapstructure:"reranker_enabled"`
	RerankerTopN        int     `mapstructure:"reranker_top_n"`
	MMREnabled          bool    `mapstructure:"mmr_enabled"`
	MMRLambda           float64 `mapstructure:"mmr_lambda"`
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
	Enabled        bool   `mapstructure:"enabled"`
	DBPath         string `mapstructure:"db_path"`
	ExtractorModel string `mapstructure:"extractor_model"`
	MaxCharsPerDoc int    `mapstructure:"max_chars_per_doc"`
	AutoExtract    bool   `mapstructure:"auto_extract"`
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
	Enabled              bool          `mapstructure:"enabled"`
	RevisionProvider     string        `mapstructure:"revision_provider"`
	RevisionModel        string        `mapstructure:"revision_model"`
	EnableLLMSummarize   bool          `mapstructure:"enable_llm_summarize"`
	MaxCommandOutput     int           `mapstructure:"max_command_output"`
	EnableSemanticSearch bool          `mapstructure:"enable_semantic_search"`
	SearchStrategy       string        `mapstructure:"search_strategy"`
	MaxContextTokens     int           `mapstructure:"max_context_tokens"`
	TrimRatio            float64       `mapstructure:"trim_ratio"`
	SummaryCooldown      time.Duration `mapstructure:"summary_cooldown"`
	SummaryTimeout       time.Duration `mapstructure:"summary_timeout"`
}
