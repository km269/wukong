package config

// ============================================================================
// Observability & Evaluation Configuration
// ============================================================================

// TelemetryConfig configures OpenTelemetry observability.
type TelemetryConfig struct {
	Enabled        bool    `mapstructure:"enabled"`
	ExporterType   string  `mapstructure:"exporter_type"`
	Endpoint       string  `mapstructure:"endpoint"`
	ServiceName    string  `mapstructure:"service_name"`
	ServiceVersion string  `mapstructure:"service_version"`
	Environment    string  `mapstructure:"environment"`
	SampleRate     float64 `mapstructure:"sample_rate"`
}

// ObservabilityConfig configures enhanced observability (Langfuse, etc.).
type ObservabilityConfig struct {
	LangfuseEnabled   bool   `mapstructure:"langfuse_enabled"`
	LangfuseHost      string `mapstructure:"langfuse_host"`
	LangfusePublicKey string `mapstructure:"langfuse_public_key" envexpand:"true"`
	LangfuseSecretKey string `mapstructure:"langfuse_secret_key" envexpand:"true"`
}

// EvalConfig configures the evaluation/regression testing system.
type EvalConfig struct {
	Enabled     bool               `mapstructure:"enabled"`
	EvalSetPath string             `mapstructure:"evalset_path"`
	ResultsPath string             `mapstructure:"results_path"`
	Metrics     []EvalMetricConfig `mapstructure:"metrics"`
}

// EvalMetricConfig defines an evaluation metric.
type EvalMetricConfig struct {
	Name      string  `mapstructure:"name"`
	Threshold float64 `mapstructure:"threshold"`
}

// ArtifactConfig configures artifact storage backend settings.
type ArtifactConfig struct {
	Backend      string `mapstructure:"backend"`
	COSBucketURL string `mapstructure:"cos_bucket_url"`
	COSSecretID  string `mapstructure:"cos_secret_id" envexpand:"true"`
	COSSecretKey string `mapstructure:"cos_secret_key" envexpand:"true"`
}
