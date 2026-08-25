package config

import "time"

// ============================================================================
// Feature Tools Configuration
// ============================================================================

// VisualiserConfig defines chart and diagram generation settings.
type VisualiserConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	OutputDir string `mapstructure:"output_dir"`
	MaxWidth  int    `mapstructure:"max_width"`
	MaxHeight int    `mapstructure:"max_height"`
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
	Enabled     bool          `mapstructure:"enabled"`
	Timeout     time.Duration `mapstructure:"timeout"`
	MaxMemoryMB int           `mapstructure:"max_memory_mb"`
}
