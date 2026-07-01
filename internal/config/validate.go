// Package config validation.
//
// This file provides configuration validation logic that runs
// after loading and env-var expansion. It checks for common
// misconfigurations such as missing default provider, invalid
// enum values, and out-of-range numeric parameters.
//
// Validate returns a slice of warnings (non-fatal issues) and
// an error for fatal issues that would prevent the agent from
// starting.
package config

import "fmt"

// Validate checks the loaded configuration for common errors and
// returns an error for fatal issues. Non-fatal issues are logged
// but do not prevent startup.
//
// Fatal errors:
//   - default_provider is set but not found in providers list
//   - agent.temperature is outside [0.0, 2.0]
//   - security.permission_mode is not a recognized value
//
// Non-fatal checks (logged as warnings by the caller):
//   - No providers configured
//   - Memory auto_extract enabled without a provider
//   - Cortex enabled without an embedding model
func (c *WukongConfig) Validate() error {
	// Check default provider exists.
	if c.DefaultProvider != "" {
		if p := c.FindProvider(c.DefaultProvider); p == nil {
			return fmt.Errorf(
				"default_provider %q not found in providers list",
				c.DefaultProvider,
			)
		}
	}

	// Validate temperature range.
	if c.Agent.Temperature < 0.0 || c.Agent.Temperature > 2.0 {
		return fmt.Errorf(
			"agent.temperature %.2f is out of range [0.0, 2.0]",
			c.Agent.Temperature,
		)
	}

	// Validate permission mode.
	switch c.Security.PermissionMode {
	case PermissionAuto, PermissionSmart,
		PermissionManual, PermissionChatOnly:
		// Valid.
	case "":
		// Empty is OK; will default to smart at runtime.
	default:
		return fmt.Errorf(
			"security.permission_mode %q is invalid; "+
				"use auto, smart, manual, or chat_only",
			c.Security.PermissionMode,
		)
	}

	// Validate max_tokens.
	if c.Agent.MaxTokens < 0 {
		return fmt.Errorf(
			"agent.max_tokens must be >= 0, got %d",
			c.Agent.MaxTokens,
		)
	}

	// Validate evolution min_confidence.
	if c.Evolution.Enabled {
		if c.Evolution.MinConfidence < 0.0 ||
			c.Evolution.MinConfidence > 1.0 {
			return fmt.Errorf(
				"evolution.min_confidence %.2f is out of range [0.0, 1.0]",
				c.Evolution.MinConfidence,
			)
		}
	}

	// Validate memory cleanup thresholds.
	if c.Memory.EnableSmartCleanup {
		if c.Memory.CleanupTriggerThreshold < 0.0 ||
			c.Memory.CleanupTriggerThreshold > 1.0 {
			return fmt.Errorf(
				"memory.cleanup_trigger_threshold %.2f is out of range [0.0, 1.0]",
				c.Memory.CleanupTriggerThreshold,
			)
		}
		if c.Memory.CleanupTargetThreshold < 0.0 ||
			c.Memory.CleanupTargetThreshold > 1.0 {
			return fmt.Errorf(
				"memory.cleanup_target_threshold %.2f is out of range [0.0, 1.0]",
				c.Memory.CleanupTargetThreshold,
			)
		}
		if c.Memory.CleanupTargetThreshold >=
			c.Memory.CleanupTriggerThreshold {
			return fmt.Errorf(
				"memory.cleanup_target_threshold (%.2f) must be "+
					"less than cleanup_trigger_threshold (%.2f)",
				c.Memory.CleanupTargetThreshold,
				c.Memory.CleanupTriggerThreshold,
			)
		}
	}

	// Validate telemetry sample rate.
	if c.Telemetry.Enabled {
		if c.Telemetry.SampleRate < 0.0 ||
			c.Telemetry.SampleRate > 1.0 {
			return fmt.Errorf(
				"telemetry.sample_rate %.2f is out of range [0.0, 1.0]",
				c.Telemetry.SampleRate,
			)
		}
	}

	// Validate ANP config.
	if c.ANP.Enabled {
		if c.ANP.Port < 0 || c.ANP.Port > 65535 {
			return fmt.Errorf(
				"anp.port %d is out of valid range [0, 65535]",
				c.ANP.Port,
			)
		}
		if c.ANP.MetaProtocolEnabled && c.ANP.Port <= 0 {
			return fmt.Errorf(
				"anp.meta_protocol_enabled requires a valid port (> 0), got %d",
				c.ANP.Port,
			)
		}
	}

	return nil
}

// Warnings returns non-fatal configuration issues as human-readable
// strings. These do not prevent startup but may indicate
// suboptimal configuration.
func (c *WukongConfig) Warnings() []string {
	var warnings []string

	if len(c.Providers) == 0 {
		warnings = append(warnings,
			"no providers configured; agent will not be able to call LLMs")
	}

	if c.Memory.AutoExtract && c.DefaultProvider == "" {
		warnings = append(warnings,
			"memory.auto_extract is enabled but no default_provider is set; "+
				"memory extraction will fail")
	}

	if c.Cortex.Enabled && c.Cortex.EmbeddingModel == "" {
		warnings = append(warnings,
			"cortex.enabled is true but embedding_model is empty; "+
				"semantic search will not work")
	}

	if c.Recall.SearchMode == "hybrid" &&
		c.Recall.EmbeddingProvider == "" &&
		c.DefaultProvider == "" {
		warnings = append(warnings,
			"recall.search_mode is hybrid but no embedding provider "+
				"or default_provider is configured")
	}

	if c.Agent.ContextCompaction &&
		c.Agent.ContextCompactionOversizedMaxTokens == 0 {
		warnings = append(warnings,
			"agent.context_compaction is enabled but "+
				"context_compaction_oversized_max_tokens is 0; "+
				"only Pass 1 (placeholder) will run, Pass 2 (truncate) is disabled")
	}

	if c.OKF.Enabled && c.OKF.BundleDir == "" {
		warnings = append(warnings,
			"okf.enabled is true but bundle_dir is empty; "+
				"OKF operations will use the default .wukong/okf")
	}

	if c.OKF.InjectorEnabled && !c.MemoryFlow.Enabled {
		warnings = append(warnings,
			"okf.injector_enabled is true but memoryflow.enabled is false; "+
				"knowledge index injection has no effect without MemoryFlow")
	}

	if c.OKF.EnrichmentEnabled && c.DefaultProvider == "" {
		warnings = append(warnings,
			"okf.enrichment_enabled is true but no default_provider is set; "+
				"LLM-driven enrichment will use deterministic fallback")
	}

	if c.ANP.Enabled && c.ANP.DIDDomain == "" {
		warnings = append(warnings,
			"anp.enabled is true but did_domain is empty; "+
				"DID identity will fall back to os.Hostname()")
	}

	if c.ANP.E2EEEnabled && !c.ANP.MetaProtocolEnabled {
		warnings = append(warnings,
			"anp.e2ee_enabled is true but meta_protocol_enabled is false; "+
				"E2EE key exchange requires meta-protocol for capability negotiation")
	}

	return warnings
}
