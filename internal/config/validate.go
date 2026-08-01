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

import (
	"fmt"
	"net/url"
)

// validateURLField returns a non-empty warning string if raw is non-empty
// but does not parse as a valid URL with a scheme and host. An empty raw
// value is allowed (the field is optional). This is a sanity check, not
// a reachability probe — it catches typos like "htp://" or a missing
// scheme before the value reaches a subsystem that would fail opaquely.
func validateURLField(raw, field string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Sprintf("%s %q is not a valid URL: %v", field, raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Sprintf(
			"%s %q is missing a scheme or host (expected e.g. https://host)",
			field, raw)
	}
	return ""
}

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

	// Validate provider types.
	for _, p := range c.Providers {
		switch ProviderType(p.Type) {
		case ProviderOpenAI, ProviderAnthropic, ProviderGoogle,
			ProviderDeepSeek, ProviderOllama, ProviderLMStudio,
			ProviderVLLM, ProviderACP, "":
			// Valid.
		default:
			return fmt.Errorf(
				"providers[%q].type %q is invalid; "+
					"use openai, anthropic, google, deepseek, "+
					"ollama, lmstudio, vllm, or acp",
				p.Name, p.Type,
			)
		}
	}

	// Validate browser backend.
	switch c.Browser.Backend {
	case BackendChromedp, BackendRod, "":
		// Valid. Empty defaults to rod.
	default:
		return fmt.Errorf(
			"browser.backend %q is invalid; "+
				"use chromedp or rod",
			c.Browser.Backend,
		)
	}

	// Validate workflow mode.
	switch WorkflowMode(c.Workflow.Mode) {
	case WorkflowModeSingle, WorkflowModeChain, WorkflowModeParallel,
		WorkflowModeCycle, WorkflowModeGraph, WorkflowModeTeamCoordinator,
		WorkflowModeTeamSwarm, WorkflowModeClaudeCode, WorkflowModeCodex,
		WorkflowModeDify, "":
		// Valid. Empty defaults to single.
	default:
		return fmt.Errorf(
			"workflow.mode %q is invalid; "+
				"use single, chain, parallel, cycle, graph, "+
				"team_coordinator, team_swarm, claude_code, codex, or dify",
			c.Workflow.Mode,
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

	// Validate session backend.
	switch c.Session.Backend {
	case "sqlite", "memory", "redis", "":
		// Valid. Empty defaults to sqlite.
	default:
		return fmt.Errorf(
			"session.backend %q is invalid; use sqlite, memory, or redis",
			c.Session.Backend,
		)
	}

	// Validate memory backend.
	switch c.Memory.Backend {
	case "sqlite", "redis", "":
		// Valid. Empty defaults to sqlite.
	default:
		return fmt.Errorf(
			"memory.backend %q is invalid; use sqlite or redis",
			c.Memory.Backend,
		)
	}

	// Validate memory cleanup thresholds range.
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
		if c.Memory.CleanupTargetThreshold >= c.Memory.CleanupTriggerThreshold {
			return fmt.Errorf(
				"memory.cleanup_target_threshold (%.2f) must be less than "+
					"cleanup_trigger_threshold (%.2f)",
				c.Memory.CleanupTargetThreshold,
				c.Memory.CleanupTriggerThreshold,
			)
		}
	}

	// Validate recall search mode.
	switch c.Recall.SearchMode {
	case "fts5", "hybrid", "":
		// Valid. Empty defaults to fts5.
	default:
		return fmt.Errorf(
			"recall.search_mode %q is invalid; use fts5 or hybrid",
			c.Recall.SearchMode,
		)
	}

	// Validate artifact backend.
	switch c.Artifact.Backend {
	case "inmemory", "cos", "":
		// Valid. Empty defaults to inmemory.
	default:
		return fmt.Errorf(
			"artifact.backend %q is invalid; use inmemory or cos",
			c.Artifact.Backend,
		)
	}

	// Validate todo backend.
	switch c.Todo.Backend {
	case "sqlite", "":
		// Valid. Empty defaults to sqlite.
	default:
		return fmt.Errorf(
			"todo.backend %q is invalid; use sqlite",
			c.Todo.Backend,
		)
	}

	// Validate agent max_llm_calls.
	if c.Agent.MaxLLMCalls < 0 {
		return fmt.Errorf(
			"agent.max_llm_calls must be >= 0, got %d",
			c.Agent.MaxLLMCalls,
		)
	}

	// Validate agent max_tool_iterations.
	if c.Agent.MaxToolIterations < 0 {
		return fmt.Errorf(
			"agent.max_tool_iterations must be >= 0, got %d",
			c.Agent.MaxToolIterations,
		)
	}

	// Validate memory scoring weights sum to ~1.0.
	if c.Memory.RecencyWeight < 0 || c.Memory.RecencyWeight > 1 ||
		c.Memory.ReferenceWeight < 0 || c.Memory.ReferenceWeight > 1 ||
		c.Memory.ImportanceWeight < 0 || c.Memory.ImportanceWeight > 1 ||
		c.Memory.LengthWeight < 0 || c.Memory.LengthWeight > 1 {
		return fmt.Errorf(
			"memory weights must be in [0.0, 1.0]; "+
				"got recency=%.2f, reference=%.2f, importance=%.2f, length=%.2f",
			c.Memory.RecencyWeight, c.Memory.ReferenceWeight,
			c.Memory.ImportanceWeight, c.Memory.LengthWeight,
		)
	}

	// Validate memory max_memories.
	if c.Memory.MaxMemories < 0 {
		return fmt.Errorf(
			"memory.max_memories must be >= 0, got %d",
			c.Memory.MaxMemories,
		)
	}

	// Validate revision trim_ratio.
	if c.Revision.TrimRatio < 0.0 || c.Revision.TrimRatio > 1.0 {
		return fmt.Errorf(
			"revision.trim_ratio %.2f is out of range [0.0, 1.0]",
			c.Revision.TrimRatio,
		)
	}

	// Validate apps config (only when enabled).
	if c.Apps.Enabled {
		if c.Apps.Clone.Workers < 1 {
			return fmt.Errorf(
				"apps.clone.workers must be >= 1, got %d",
				c.Apps.Clone.Workers,
			)
		}
		if c.Apps.Clone.AssetWorkers < 1 {
			return fmt.Errorf(
				"apps.clone.asset_workers must be >= 1, got %d",
				c.Apps.Clone.AssetWorkers,
			)
		}
	}

	// Validate MCP server config (only when enabled).
	if c.MCPServer.Enabled {
		if c.MCPServer.Address == "" {
			return fmt.Errorf(
				"mcp_server.address is required when mcp_server.enabled is true",
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
		c.Recall.EmbeddingModel == "" &&
		c.DefaultProvider == "" {
		warnings = append(warnings,
			"recall.search_mode is hybrid but no embedding model "+
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

	if c.Gateway.Enabled {
		if c.Gateway.Feishu.Enabled && c.Gateway.Feishu.AppID == "" {
			warnings = append(warnings,
				"gateway.feishu.enabled is true but app_id is empty; "+
					"Feishu channel may fail to authenticate")
		}
		if !c.Gateway.Feishu.Enabled {
			warnings = append(warnings,
				"gateway.enabled is true but no channel (feishu) is enabled; "+
					"Gateway will start with no active message channels")
		}
	}

	// URL sanity checks for enabled subsystems. These catch malformed
	// URLs (missing scheme/host, typos) before they reach a subsystem
	// that would fail opaquely at runtime. Only non-empty fields are
	// checked — empty means "use the default".
	if w := validateURLField(c.Session.RedisURL, "session.redis_url"); w != "" {
		warnings = append(warnings, w)
	}
	if c.Cortex.Enabled {
		if w := validateURLField(c.Cortex.EmbeddingBaseURL,
			"cortex.embedding_base_url"); w != "" {
			warnings = append(warnings, w)
		}
	}
	if c.ARD.Enabled {
		if w := validateURLField(c.ARD.RegistryURL,
			"ard.registry_url"); w != "" {
			warnings = append(warnings, w)
		}
	}
	if c.Dify.Enabled {
		if w := validateURLField(c.Dify.BaseURL, "dify.base_url"); w != "" {
			warnings = append(warnings, w)
		}
	}
	for _, p := range c.Providers {
		if w := validateURLField(p.BaseURL,
			"providers["+p.Name+"].base_url"); w != "" {
			warnings = append(warnings, w)
		}
		if p.BaseURL == "" && p.Type != "acp" {
			warnings = append(warnings,
				"providers["+p.Name+"].base_url is empty; "+
					"LLM requests will fail. Set the environment variable "+
					"or configure base_url directly in config.yaml")
		}
	}
	for _, r := range c.Summon.A2ARemotes {
		if w := validateURLField(r.ServerURL,
			"summon.a2a_remotes["+r.Name+"].server_url"); w != "" {
			warnings = append(warnings, w)
		}
	}

	return warnings
}
