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
	"runtime"
	"strings"
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

	// Validate agent planner.
	switch c.Agent.Planner {
	case "", "builtin", "react":
		// Valid. Empty disables the planner.
	default:
		return fmt.Errorf(
			"agent.planner %q is invalid; use builtin or react",
			c.Agent.Planner,
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

	// Validate summon config (only when enabled).
	if c.Summon.Enabled {
		if c.Summon.MaxConcurrent < 0 {
			return fmt.Errorf(
				"summon.max_concurrent must be >= 0, got %d",
				c.Summon.MaxConcurrent,
			)
		}
		for _, r := range c.Summon.A2ARemotes {
			if r.Name == "" {
				return fmt.Errorf(
					"summon.a2a_remotes[].name is required")
			}
			if r.ServerURL == "" {
				return fmt.Errorf(
					"summon.a2a_remotes[%q].server_url is required",
					r.Name,
				)
			}
			switch r.AuthType {
			case "", "api_key", "jwt", "oauth2":
				// Valid.
			default:
				return fmt.Errorf(
					"summon.a2a_remotes[%q].auth_type %q is invalid; "+
						"use api_key, jwt, or oauth2",
					r.Name, r.AuthType,
				)
			}
		}
	}

	// Validate browser search backends — check required fields
	// for each backend that has enabled: true.
	if c.Browser.Enabled {
		if c.Browser.Search.SearXNG.Enabled &&
			c.Browser.Search.SearXNG.URL == "" {
			return fmt.Errorf(
				"browser.search.searxng.enabled is true but " +
					"searxng.url is empty",
			)
		}
		if c.Browser.Search.Tavily.Enabled &&
			c.Browser.Search.Tavily.APIKey == "" {
			return fmt.Errorf(
				"browser.search.tavily.enabled is true but " +
					"tavily.api_key is empty",
			)
		}
		if c.Browser.Search.Google.Enabled {
			if c.Browser.Search.Google.APIKey == "" ||
				c.Browser.Search.Google.CSEID == "" {
				return fmt.Errorf(
					"browser.search.google.enabled is true but " +
						"google.api_key or google.cse_id is empty",
				)
			}
		}
		if c.Browser.Search.Bing.Enabled &&
			c.Browser.Search.Bing.APIKey == "" {
			return fmt.Errorf(
				"browser.search.bing.enabled is true but " +
					"bing.api_key is empty",
			)
		}
	}

	// Validate cortex.search_strategy ranges.
	if c.Cortex.Enabled && c.Cortex.SearchStrategy != nil {
		ss := c.Cortex.SearchStrategy
		if ss.DenseWeight < 0.0 || ss.DenseWeight > 1.0 {
			return fmt.Errorf(
				"cortex.search_strategy.dense_weight %.2f is out of range [0.0, 1.0]",
				ss.DenseWeight,
			)
		}
		if ss.TextWeight < 0.0 || ss.TextWeight > 1.0 {
			return fmt.Errorf(
				"cortex.search_strategy.text_weight %.2f is out of range [0.0, 1.0]",
				ss.TextWeight,
			)
		}
		if ss.MMRLambda < 0.0 || ss.MMRLambda > 1.0 {
			return fmt.Errorf(
				"cortex.search_strategy.mmr_lambda %.2f is out of range [0.0, 1.0]",
				ss.MMRLambda,
			)
		}
		if ss.FTS5PoolSize > 0 && ss.RerankerTopN > 0 &&
			ss.RerankerTopN > ss.FTS5PoolSize {
			return fmt.Errorf(
				"cortex.search_strategy.reranker_top_n (%d) must be <= "+
					"fts5_pool_size (%d); reranker cannot receive more "+
					"candidates than the pool provides",
				ss.RerankerTopN, ss.FTS5PoolSize,
			)
		}
	}

	// Validate sandbox resource limits. Zero values mean "unlimited"
	// and are always legal; only non-zero values that are too small
	// to ever let a shell start are fatal. These caps prevent
	// operators from shipping a config where every command is
	// killed before it can do useful work — the failure mode is
	// otherwise opaque (commands exit non-zero with no clear cause).
	const (
		// minSandboxMemoryBytes is the floor for MaxMemoryBytes.
		// Below 1 MiB even a bare cmd.exe / sh startup fails, so
		// the limit can never produce useful behavior.
		minSandboxMemoryBytes = 1 << 20 // 1 MiB
		// minSandboxFileBytes is the floor for MaxFileBytes.
		// 512 is one standard block; smaller caps make every
		// write fail instantly.
		minSandboxFileBytes = 512
	)
	sb := c.Security.Sandbox
	if sb.Limits.MaxMemoryBytes != 0 &&
		sb.Limits.MaxMemoryBytes < minSandboxMemoryBytes {
		return fmt.Errorf(
			"security.sandbox.limits.max_memory_bytes %d is too small; "+
				"shell processes need at least %d bytes (~1 MiB) to start. "+
				"Set to 0 for unlimited or >= %d",
			sb.Limits.MaxMemoryBytes,
			minSandboxMemoryBytes, minSandboxMemoryBytes,
		)
	}
	if sb.Limits.MaxFileBytes != 0 &&
		sb.Limits.MaxFileBytes < minSandboxFileBytes {
		return fmt.Errorf(
			"security.sandbox.limits.max_file_bytes %d is too small; "+
				"minimum is %d bytes (one block). Set to 0 for unlimited "+
				"or >= %d",
			sb.Limits.MaxFileBytes,
			minSandboxFileBytes, minSandboxFileBytes,
		)
	}

	// Validate service port conflicts. Collects (name, port) for all
	// enabled servers and fails if two services share the same port.
	// ANP uses a bare int Port field; others use Address like ":9090".
	type svcPort struct {
		name string
		port string
	}
	var ports []svcPort
	if c.A2AServer.Enabled && c.A2AServer.Address != "" {
		ports = append(ports, svcPort{"a2a_server", portFromAddr(c.A2AServer.Address)})
	}
	if c.AGUI.Enabled && c.AGUI.Address != "" {
		ports = append(ports, svcPort{"agui", portFromAddr(c.AGUI.Address)})
	}
	if c.ACPServer.Enabled && c.ACPServer.Address != "" {
		ports = append(ports, svcPort{"acp_server", portFromAddr(c.ACPServer.Address)})
	}
	if c.ACPMCP.Enabled && c.ACPMCP.Address != "" {
		ports = append(ports, svcPort{"acp_mcp", portFromAddr(c.ACPMCP.Address)})
	}
	if c.MCPServer.Enabled && c.MCPServer.Address != "" {
		ports = append(ports, svcPort{"mcp_server", portFromAddr(c.MCPServer.Address)})
	}
	if c.ANP.Enabled && c.ANP.Port > 0 {
		ports = append(ports, svcPort{"anp", fmt.Sprintf("%d", c.ANP.Port)})
	}
	seen := make(map[string]string, len(ports))
	for _, p := range ports {
		if p.port == "" {
			continue
		}
		if prev, ok := seen[p.port]; ok {
			return fmt.Errorf(
				"port conflict: %s and %s both bind to port %s",
				prev, p.name, p.port,
			)
		}
		seen[p.port] = p.name
	}

	return nil
}

// portFromAddr extracts the port suffix from a "host:port" or ":port"
// address. Returns empty string if no port can be identified.
func portFromAddr(addr string) string {
	if i := strings.LastIndex(addr, ":"); i != -1 {
		return addr[i+1:]
	}
	return ""
}

// isLoopbackAddr returns true if addr binds to the loopback
// interface. Recognised loopback hosts are: empty (defaults to all
// interfaces — treated as non-loopback for safety), "127.0.0.1",
// "localhost", and "::1". A bare ":port" binds to 0.0.0.0 and is
// therefore NOT loopback.
func isLoopbackAddr(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i != -1 {
		host = addr[:i]
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
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

	// Security check: warn when MCP/ACP servers are enabled with no
	// auth on non-loopback addresses. tools/call can execute arbitrary
	// commands via developer_command_execute, so missing auth on a
	// network-exposed interface is a Critical risk.
	if c.MCPServer.Enabled && c.MCPServer.Security.Auth.Type == "" &&
		!isLoopbackAddr(c.MCPServer.Address) {
		warnings = append(warnings,
			"mcp_server.enabled is true with empty security.auth.type "+
				"and non-loopback address "+c.MCPServer.Address+"; "+
				"tools/call is unauthenticated and can execute arbitrary "+
				"commands. Set security.auth.type=api_key or bind to 127.0.0.1")
	}
	if c.ACPServer.Enabled && c.ACPServer.Security.Auth.Type == "" &&
		!isLoopbackAddr(c.ACPServer.Address) {
		warnings = append(warnings,
			"acp_server.enabled is true with empty security.auth.type "+
				"and non-loopback address "+c.ACPServer.Address+"; "+
				"tools/call is unauthenticated and can bypass agent guard "+
				"chain. Set security.auth.type=api_key or bind to 127.0.0.1")
	}

	// URL sanity checks for enabled subsystems. These catch malformed
	// URLs (missing scheme/host, typos) before they reach a subsystem
	// that would fail opaquely at runtime. Only non-empty fields are
	// checked — empty means "use the default".
	// Context overflow check: warn when revision.max_context_tokens
	// exceeds the default provider's actual context window. Sending
	// prompts larger than the model's n_ctx yields 400 Bad Request.
	if c.Revision.MaxContextTokens > 0 && c.DefaultProvider != "" {
		if p := c.FindProvider(c.DefaultProvider); p != nil {
			effectiveWindow := p.EffectiveContextWindow()
			if effectiveWindow > 0 &&
				c.Revision.MaxContextTokens > effectiveWindow {
				warnings = append(warnings,
					fmt.Sprintf(
						"revision.max_context_tokens (%d) exceeds the "+
							"default provider %q effective context window (%d); "+
							"LLM requests will fail with 400 when prompt grows "+
							"past the model limit. Set providers[%s].context_window "+
							"to override or lower revision.max_context_tokens.",
						c.Revision.MaxContextTokens,
						c.DefaultProvider,
						effectiveWindow,
						c.DefaultProvider,
					))
			}
		}
	}

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
		// Warn when context_window is not set for local inference
		// servers. Their actual n_ctx varies by model and is easy
		// to misconfigure; the conservative default (8K) may be
		// either too low (truncating prematurely) or too high
		// (causing 400 when prompts exceed real n_ctx).
		if p.ContextWindow <= 0 {
			switch ProviderType(p.Type) {
			case ProviderVLLM, ProviderOllama, ProviderLMStudio:
				warnings = append(warnings,
					"providers["+p.Name+"].context_window is not set; "+
						"using conservative default 8000. Local inference "+
						"servers (vllm/ollama/lmstudio) vary widely — "+
						"set context_window to match `--max-model-len` / "+
						"num_ctx to avoid 400 Bad Request or premature "+
						"context truncation.")
			}
		}
	}
	for _, r := range c.Summon.A2ARemotes {
		if w := validateURLField(r.ServerURL,
			"summon.a2a_remotes["+r.Name+"].server_url"); w != "" {
			warnings = append(warnings, w)
		}
	}

	// Browser search backend required-field checks are handled above
	// alongside the per-backend enabled validation.

	// Cortex embedding base URL is required when cortex is enabled.
	if c.Cortex.Enabled && c.Cortex.EmbeddingBaseURL == "" {
		warnings = append(warnings,
			"cortex.enabled is true but embedding_base_url is empty; "+
				"semantic search will not work")
	}

	// Surface unresolved ${VAR} references so users can spot typos
	// like ${OEPNAI_API_KEY} that would silently resolve to empty.
	for _, msg := range c.unresolvedEnvVars {
		warnings = append(warnings, msg)
	}

	// apps.clone vs browser anti-crawl consistency. Per project hard
	// constraint, apps download and apps clone must use identical
	// anti-crawling measures. Warn when key fields diverge so users
	// notice the mismatch. Only Stealth/Headless divergence is checked
	// here; BrowserBackend string mismatch is covered when both are set.
	if c.Apps.Enabled && c.Browser.Enabled {
		if c.Apps.Clone.Stealth != c.Browser.Stealth {
			warnings = append(warnings,
				fmt.Sprintf(
					"apps.clone.stealth=%v but browser.stealth=%v; "+
						"anti-crawl measures should be identical per "+
						"project constraint",
					c.Apps.Clone.Stealth, c.Browser.Stealth))
		}
		if c.Apps.Clone.Headless != c.Browser.Headless {
			warnings = append(warnings,
				fmt.Sprintf(
					"apps.clone.headless=%v but browser.headless=%v; "+
						"anti-crawl measures should be identical per "+
						"project constraint",
					c.Apps.Clone.Headless, c.Browser.Headless))
		}
		if c.Apps.Clone.BrowserBackend != "" &&
			c.Browser.Backend != "" &&
			c.Apps.Clone.BrowserBackend != c.Browser.Backend {
			warnings = append(warnings,
				fmt.Sprintf(
					"apps.clone.browser_backend=%q but browser.backend=%q; "+
						"anti-crawl measures should be identical per "+
						"project constraint",
					c.Apps.Clone.BrowserBackend, c.Browser.Backend))
		}
	}

	// Sandbox resource-limit sanity. Fatal floor checks (too small to
	// ever start a shell) live in Validate; here we surface config
	// that is technically legal but likely to cause surprising
	// command failures at runtime.
	sb := c.Security.Sandbox
	const smallSandboxMemoryBytes = 1 << 24 // 16 MiB
	if sb.Limits.MaxMemoryBytes != 0 &&
		sb.Limits.MaxMemoryBytes < smallSandboxMemoryBytes {
		warnings = append(warnings,
			fmt.Sprintf(
				"security.sandbox.limits.max_memory_bytes %d is below "+
					"the recommended %d (~16 MiB); cmd/sh startup "+
					"footprint varies across platforms and may trip the "+
					"limit, causing every command to be killed with no "+
					"clear cause. Set to 0 for unlimited or >= %d unless "+
					"you have measured the target shell's resident set.",
				sb.Limits.MaxMemoryBytes,
				smallSandboxMemoryBytes, smallSandboxMemoryBytes))
	}
	// MaxProcesses==1 means the shell itself uses the only slot and
	// cannot fork any helper (e.g. `ls | head`, `cmd /C "a & b"`).
	// Almost every non-trivial shell pipeline needs >= 2.
	if sb.Limits.MaxProcesses == 1 {
		warnings = append(warnings,
			"security.sandbox.limits.max_processes=1 is too tight; "+
				"the shell process itself occupies the single slot and "+
				"cannot fork helpers (ls | head, cmd /C \"a & b\", etc.). "+
				"Set to 0 for unlimited or >= 2")
	}
	// Platform coverage gaps. RLIMIT_FSIZE is Linux-only; Windows
	// ignores it. macOS sandbox-exec does not enforce any of these
	// limits. Surface these so operators on other platforms don't
	// ship a config expecting enforcement that never happens.
	switch runtime.GOOS {
	case "windows":
		if sb.Limits.MaxFileBytes != 0 {
			warnings = append(warnings,
				"security.sandbox.limits.max_file_bytes is set but "+
					"Windows has no equivalent of RLIMIT_FSIZE; the "+
					"limit is ignored on this platform. Either remove "+
					"the field or run on Linux to enforce it.")
		}
	case "darwin":
		// macOS sandbox-exec enforces filesystem write protection but
		// not resource limits (CPU/memory/file-size/processes). Surface
		// any non-zero limit so operators know it is a no-op here.
		if sb.Limits.MaxCPUSeconds != 0 ||
			sb.Limits.MaxMemoryBytes != 0 ||
			sb.Limits.MaxFileBytes != 0 ||
			sb.Limits.MaxProcesses != 0 {
			warnings = append(warnings,
				"security.sandbox.limits are set but macOS sandbox-exec "+
					"does not enforce CPU/memory/file-size/process limits; "+
					"only filesystem write protection is applied on this "+
					"platform. Run on Linux (setrlimit) or Windows "+
					"(Job Object) to enforce resource caps.")
		}
	}

	return warnings
}
