// Package config default values.
//
// This file registers all built-in default values with Viper.
// These are used when no config file or environment variable
// provides a value. Defaults are organized by subsystem, matching
// the struct categories in the types_*.go files.
//
// NOTE: Viper's AutomaticEnv only resolves keys it already knows
// about (via SetDefault, the config file, or BindEnv). Registering
// a default here — even an empty-string one — is what makes the
// key overridable through WUKONG_* environment variables, so every
// documented key should have a corresponding SetDefault entry.
package config

import "github.com/km269/wukong/internal/gateway"

// setDefaults registers all built-in default values with Viper.
// These are used when no config file or environment variable
// provides a value.
func (l *Loader) setDefaults() {
	l.setGlobalDefaults()
	l.setAgentDefaults()
	l.setSecurityDefaults()
	l.setStorageDefaults()
	l.setCortexStackDefaults()
	l.setRevisionDefaults()
	l.setFeatureDefaults()
	l.setAppsDefaults()
	l.setOrchestrationDefaults()
	l.setServerDefaults()
	l.setGatewayDefaults()
	l.setObservabilityDefaults()
	l.setOKFDefaults()
}

// setGlobalDefaults registers global-level defaults.
func (l *Loader) setGlobalDefaults() {
	l.v.SetDefault("log_level", "info")
	l.v.SetDefault("project_dir", "~/.config/wukong/")
}

// setAgentDefaults registers agent subsystem defaults.
func (l *Loader) setAgentDefaults() {
	// LLM call limits
	l.v.SetDefault("agent.max_llm_calls", 50)
	l.v.SetDefault("agent.max_tool_iterations", 30)
	l.v.SetDefault("agent.max_run_duration", "900s")
	l.v.SetDefault("agent.tool_call_timeout", "120s")

	// Generation parameters
	l.v.SetDefault("agent.parallel_tools", true)
	l.v.SetDefault("agent.streaming", true)
	l.v.SetDefault("agent.temperature", 0.7)
	l.v.SetDefault("agent.max_tokens", 4096)

	// Tool retry
	l.v.SetDefault("agent.tool_retry_enabled", true)
	l.v.SetDefault("agent.tool_retry_max_attempts", 3)
	l.v.SetDefault("agent.tool_retry_initial_wait", "1s")
	l.v.SetDefault("agent.tool_retry_backoff_factor", 2.0)
	l.v.SetDefault("agent.enable_post_tool_prompt", true)

	// Planner
	l.v.SetDefault("agent.planner", "")

	// Command validation: hybrid = scope declarations authoritative
	// with legacy heuristic fallback (see types_agent.go).
	l.v.SetDefault("agent.command_validation_mode", "hybrid")

	// Declarative flow DSL (P0-2), opt-in like recipes.
	l.v.SetDefault("agent.flow_enabled", false)
	l.v.SetDefault("agent.flow_dir", "")

	// Tool search
	l.v.SetDefault("agent.tool_search_enabled", false)
	l.v.SetDefault("agent.tool_search_max_tools", 20)

	// Context compaction
	l.v.SetDefault("agent.context_compaction", false)
	l.v.SetDefault("agent.context_compaction_tool_result_max_tokens", 1024)
	l.v.SetDefault("agent.context_compaction_oversized_max_tokens", 0)
	l.v.SetDefault("agent.context_compaction_keep_recent", 1)

	// Session recall
	l.v.SetDefault("agent.session_recall_enabled", false)
	l.v.SetDefault("agent.session_recall_limit", 5)

	// JSON repair
	l.v.SetDefault("agent.json_repair_enabled", false)

	// Agent tools
	l.v.SetDefault("agent.agent_tools_enabled", true)
	l.v.SetDefault("agent.agent_tools_stream", false)

	// Prompt & recipe
	l.v.SetDefault("agent.system_prompt_dir",
		"~/.config/wukong/prompts/")
	l.v.SetDefault("agent.recipe_dir", ".wukong/recipes/")
	l.v.SetDefault("agent.recipe_enabled", true)
}

// setSecurityDefaults registers security subsystem defaults.
func (l *Loader) setSecurityDefaults() {
	l.v.SetDefault("security.malware_scan_enabled", true)
	l.v.SetDefault("security.default_timeout", "30s")
	l.v.SetDefault("security.max_timeout", "300s")
	l.v.SetDefault("security.block_dangerous_commands", true)
	l.v.SetDefault("security.blocked_commands",
		[]string{"rm -rf /", "dd if=/dev/zero", "mkfs.",
			"> /dev/sda", "fork bomb"})
	l.v.SetDefault("security.require_approval", false)
	l.v.SetDefault("security.permission_mode", "smart")
	l.v.SetDefault("security.guardrail_enabled", false)
	l.v.SetDefault("security.ignore_file_enabled", true)
	l.v.SetDefault("security.ignore_file", ".wukongignore")

	// Sandbox process-level resource limits and lifecycle binding.
	// All-zero defaults keep legacy behavior (no Job Object / no
	// setrlimit) so existing deployments are unaffected unless
	// operators explicitly enable caps in config.yaml.
	l.v.SetDefault("security.sandbox.kill_on_parent_exit", false)
	l.v.SetDefault("security.sandbox.limits.max_cpu_seconds", 0)
	l.v.SetDefault("security.sandbox.limits.max_memory_bytes", 0)
	l.v.SetDefault("security.sandbox.limits.max_file_bytes", 0)
	l.v.SetDefault("security.sandbox.limits.max_processes", 0)
}

// setStorageDefaults registers storage subsystem defaults
// (session, memory, todo, recall).
func (l *Loader) setStorageDefaults() {
	// Session
	l.v.SetDefault("session.backend", "sqlite")
	l.v.SetDefault("session.db_path", "wukong.db")
	l.v.SetDefault("session.event_limit", 500)
	l.v.SetDefault("session.ttl", "0h")
	l.v.SetDefault("session.enable_summary", true)
	l.v.SetDefault("session.summary_trigger", 50)
	// Registered (empty) so WUKONG_SESSION_REDIS_URL overrides work.
	l.v.SetDefault("session.redis_url", "")
	// Model-visible event log: records the messages the model
	// actually sees after context enrichment, enforcing the
	// "model-visible means logged" invariant. Distinct from the
	// framework session service's own event storage.
	l.v.SetDefault("session.enable_model_event_log", true)

	// Memory
	l.v.SetDefault("memory.backend", "sqlite")
	l.v.SetDefault("memory.db_path", "wukong.db")
	l.v.SetDefault("memory.max_memories", 100)
	l.v.SetDefault("memory.auto_extract", true)
	l.v.SetDefault("memory.extract_timeout", "300s")
	// Extractor LLM selection; empty falls back to the lightweight
	// provider/model. Registered so WUKONG_MEMORY_* overrides work.
	l.v.SetDefault("memory.extractor_provider", "")
	l.v.SetDefault("memory.extractor_model", "")
	l.v.SetDefault("memory.extractor_prompt", "")

	// Memory scoring weights
	l.v.SetDefault("memory.recency_weight", 0.4)
	l.v.SetDefault("memory.reference_weight", 0.3)
	l.v.SetDefault("memory.importance_weight", 0.2)
	l.v.SetDefault("memory.length_weight", 0.1)

	// Dynamic TTL
	l.v.SetDefault("memory.dynamic_ttl", true)

	// Smart cleanup
	l.v.SetDefault("memory.enable_smart_cleanup", true)
	l.v.SetDefault("memory.cleanup_trigger_threshold", 0.8)
	l.v.SetDefault("memory.cleanup_target_threshold", 0.6)
	l.v.SetDefault("memory.memory_ttl", "720h")

	// Todo
	l.v.SetDefault("todo.backend", "sqlite")
	l.v.SetDefault("todo.db_path", "wukong.db")
	l.v.SetDefault("todo.enable_native_todo", true)
	l.v.SetDefault("todo.enable_enforcer", true)

	// Recall
	l.v.SetDefault("recall.enabled", true)
	l.v.SetDefault("recall.backend", "sqlite")
	l.v.SetDefault("recall.db_path", "wukong.db")
	l.v.SetDefault("recall.max_results", 10)
	l.v.SetDefault("recall.max_messages_per_session", 200)
	l.v.SetDefault("recall.search_mode", "fts5")
}

// setCortexStackDefaults registers CortexDB memory stack defaults
// (cortex, memoryflow, graphflow, importflow).
func (l *Loader) setCortexStackDefaults() {
	// Cortex
	l.v.SetDefault("cortex.enabled", false)
	l.v.SetDefault("cortex.db_path", "wukong.db")
	l.v.SetDefault("cortex.max_results", 10)
	l.v.SetDefault("cortex.max_messages_per_session", 200)
	l.v.SetDefault("cortex.embedding_base_url", "")
	l.v.SetDefault("cortex.embedding_api_key", "")
	l.v.SetDefault("cortex.embedding_model",
		"text-embedding-3-small")
	// Empty reranker_* disables cross-encoder reranking (base
	// URL/key reuse the embedding values); registered so env
	// overrides work.
	l.v.SetDefault("cortex.reranker_base_url", "")
	l.v.SetDefault("cortex.reranker_api_key", "")
	l.v.SetDefault("cortex.reranker_model", "")

	// MemoryFlow
	l.v.SetDefault("memoryflow.enabled", false)
	l.v.SetDefault("memoryflow.db_path", "wukong.db")
	l.v.SetDefault("memoryflow.namespace", "assistant")
	l.v.SetDefault("memoryflow.embedding_dimensions", 0)
	// Registered (empty) so WUKONG_MEMORYFLOW_* overrides work.
	l.v.SetDefault("memoryflow.planner_model", "")
	l.v.SetDefault("memoryflow.extractor_model", "")

	// GraphFlow
	l.v.SetDefault("graphflow.enabled", false)
	l.v.SetDefault("graphflow.db_path", "wukong.db")
	l.v.SetDefault("graphflow.max_chars_per_doc", 8000)
	l.v.SetDefault("graphflow.auto_extract", false)
	l.v.SetDefault("graphflow.extractor_model", "")

	// ImportFlow
	l.v.SetDefault("importflow.enabled", false)
	l.v.SetDefault("importflow.db_path", "wukong.db")
}

// setRevisionDefaults registers context revision defaults.
func (l *Loader) setRevisionDefaults() {
	l.v.SetDefault("revision.enabled", true)
	// Registered (empty) so WUKONG_REVISION_* overrides work.
	l.v.SetDefault("revision.revision_provider", "")
	l.v.SetDefault("revision.revision_model", "")
	l.v.SetDefault("revision.enable_llm_summarize", false)
	l.v.SetDefault("revision.summary_cooldown", "120s")
	l.v.SetDefault("revision.summary_timeout", "30s")
	l.v.SetDefault("revision.max_command_output", 8000)
	l.v.SetDefault("revision.enable_semantic_search", false)
	l.v.SetDefault("revision.search_strategy", "include_all")
	l.v.SetDefault("revision.max_context_tokens", 64000)
	l.v.SetDefault("revision.trim_ratio", 0.3)
}

// setFeatureDefaults registers feature tool defaults
// (browser, visualiser, tutorial, top_of_mind, code_mode).
func (l *Loader) setFeatureDefaults() {
	// Browser
	l.v.SetDefault("browser.enabled", true)
	l.v.SetDefault("browser.browser_type", "chromium")
	l.v.SetDefault("browser.backend", "rod")
	l.v.SetDefault("browser.headless", true)
	l.v.SetDefault("browser.stealth", false)
	l.v.SetDefault("browser.cache_dir", ".wukong/cache")
	l.v.SetDefault("browser.max_download_size", 104857600)
	l.v.SetDefault("browser.timeout", "60s")
	l.v.SetDefault("browser.viewport_width", 1280)
	l.v.SetDefault("browser.viewport_height", 720)
	l.v.SetDefault("browser.proxy.enabled", false)
	l.v.SetDefault("browser.proxy.pool", []string{})
	l.v.SetDefault("browser.proxy.rotate_every", 10)

	// Browser Search
	l.v.SetDefault("browser.search.duckduckgo.enabled", true)
	l.v.SetDefault("browser.search.duckduckgo.url", "https://api.duckduckgo.com/")
	l.v.SetDefault("browser.search.searxng.enabled", false)
	l.v.SetDefault("browser.search.searxng.url", "http://localhost:8080/")
	l.v.SetDefault("browser.search.searxng.api_key", "")
	l.v.SetDefault("browser.search.tavily.enabled", false)
	l.v.SetDefault("browser.search.tavily.api_key", "")
	l.v.SetDefault("browser.search.google.enabled", false)
	l.v.SetDefault("browser.search.google.api_key", "")
	l.v.SetDefault("browser.search.google.cse_id", "")
	l.v.SetDefault("browser.search.bing.enabled", false)
	l.v.SetDefault("browser.search.bing.api_key", "")

	// Browser automation extras
	l.v.SetDefault("browser.workers", 4)
	l.v.SetDefault("browser.browser_path", "")
	l.v.SetDefault("browser.scroll", false)
	l.v.SetDefault("browser.control_url", "")
	l.v.SetDefault("browser.profile_dir", "")
	l.v.SetDefault("browser.global_render_slots", 0)
	l.v.SetDefault("browser.geo_region", "")

	// Visualiser
	l.v.SetDefault("visualiser.enabled", true)
	l.v.SetDefault("visualiser.output_dir", ".wukong/visuals")
	l.v.SetDefault("visualiser.max_width", 1200)
	l.v.SetDefault("visualiser.max_height", 800)

	// Tutorial
	l.v.SetDefault("tutorial.enabled", true)
	l.v.SetDefault("tutorial.language", "zh")

	// Top of Mind
	l.v.SetDefault("top_of_mind.enabled", true)
	l.v.SetDefault("top_of_mind.instruction_file",
		".wukong/instructions.md")
	l.v.SetDefault("top_of_mind.max_length", 2000)

	// Code Mode
	l.v.SetDefault("code_mode.enabled", true)
	l.v.SetDefault("code_mode.timeout", "10s")
	l.v.SetDefault("code_mode.max_memory_mb", 128)
}

// setAppsDefaults registers apps (clone & pack) defaults.
func (l *Loader) setAppsDefaults() {
	l.v.SetDefault("apps.enabled", true)
	l.v.SetDefault("apps.app_dir", ".wukong/apps")

	// Clone defaults
	l.v.SetDefault("apps.clone.max_pages", 0)
	l.v.SetDefault("apps.clone.max_depth", 0)
	l.v.SetDefault("apps.clone.traversal", "bfs")
	l.v.SetDefault("apps.clone.subdomains", false)
	l.v.SetDefault("apps.clone.scope_prefix", "")
	l.v.SetDefault("apps.clone.workers", 4)
	l.v.SetDefault("apps.clone.asset_workers", 8)
	l.v.SetDefault("apps.clone.timeout", 300)
	l.v.SetDefault("apps.clone.render_timeout", 120)
	l.v.SetDefault("apps.clone.settle", 1500)
	l.v.SetDefault("apps.clone.scroll", false)
	l.v.SetDefault("apps.clone.respect_robots", true)
	l.v.SetDefault("apps.clone.crawl_delay", 0)
	l.v.SetDefault("apps.clone.rate_limit_whitelist", []string{})
	l.v.SetDefault("apps.clone.rate_limit_ip_segment", true)
	l.v.SetDefault("apps.clone.rate_limit_ip_prefix_v4", 24)
	l.v.SetDefault("apps.clone.rate_limit_ip_prefix_v6", 64)
	l.v.SetDefault("apps.clone.no_sitemap", false)
	l.v.SetDefault("apps.clone.dedup_content", true)
	l.v.SetDefault("apps.clone.mobile_readable", true)
	l.v.SetDefault("apps.clone.enable_resume", true)
	l.v.SetDefault("apps.clone.persist", true)
	l.v.SetDefault("apps.clone.incremental", false)
	l.v.SetDefault("apps.clone.cache_max_age", 86400)
	l.v.SetDefault("apps.clone.headless", true)
	l.v.SetDefault("apps.clone.stealth", true)
	l.v.SetDefault("apps.clone.chrome_profile",
		".wukong/chrome/profile")
	l.v.SetDefault("apps.clone.chrome_path", "")
	l.v.SetDefault("apps.clone.antibot_enabled", true)
	l.v.SetDefault("apps.clone.antibot_auto_escalate", true)
	l.v.SetDefault("apps.clone.asset_same_domain", true)
	l.v.SetDefault("apps.clone.max_asset_bytes", 52428800)
	l.v.SetDefault("apps.clone.cookie_file", "")
	l.v.SetDefault("apps.clone.user_agent", "")
	l.v.SetDefault("apps.clone.browser_backend", "rod")
	l.v.SetDefault("apps.clone.insecure_tls", false)
	l.v.SetDefault("apps.clone.tls_ca_cert_path", "")
	l.v.SetDefault("apps.clone.proxy_enabled", false)
	l.v.SetDefault("apps.clone.proxy_pool", []string{})
	l.v.SetDefault("apps.clone.proxy_rotate_every", 10)

	// Pack defaults
	l.v.SetDefault("apps.pack.compress", true)
	l.v.SetDefault("apps.pack.incremental", false)
	l.v.SetDefault("apps.pack.language", "eng")
	l.v.SetDefault("apps.pack.creator", "Wukong")
	l.v.SetDefault("apps.pack.format", "html")
}

// setOrchestrationDefaults registers orchestration & discovery
// defaults (ard, summon, skill, evolution, knowledge, workflow,
// dify).
func (l *Loader) setOrchestrationDefaults() {
	// ARD
	l.v.SetDefault("ard.enabled", false)
	l.v.SetDefault("ard.registry_url", "")
	l.v.SetDefault("ard.catalog_path", ".wukong/ard/catalog.json")
	l.v.SetDefault("ard.publish_enabled", false)
	l.v.SetDefault("ard.publish_port", 0)

	// Summon
	l.v.SetDefault("summon.enabled", true)
	l.v.SetDefault("summon.delegates_dir", ".wukong/skills")
	l.v.SetDefault("summon.max_concurrent", 5)

	// Skill
	l.v.SetDefault("skill.enabled", true)
	l.v.SetDefault("skill.skills_dir", ".wukong/skills")

	// ANP
	l.v.SetDefault("anp.enabled", false)
	l.v.SetDefault("anp.port", 9092)
	l.v.SetDefault("anp.discovery_enabled", true)
	l.v.SetDefault("anp.meta_protocol_enabled", true)
	l.v.SetDefault("anp.e2ee_enabled", true)
	l.v.SetDefault("anp.a2a_enabled", true)
	l.v.SetDefault("anp.agui_enabled", true)
	l.v.SetDefault("anp.http_sign_enabled", true)
	l.v.SetDefault("anp.mcp_enabled", true)

	// Evolution
	l.v.SetDefault("evolution.enabled", false)
	l.v.SetDefault("evolution.auto_patch", false)
	l.v.SetDefault("evolution.analysis_provider", "")
	l.v.SetDefault("evolution.analysis_model", "")
	l.v.SetDefault("evolution.min_confidence", 0.7)
	l.v.SetDefault("evolution.cooldown_period", "30m")
	l.v.SetDefault("evolution.max_patches_per_day", 10)
	l.v.SetDefault("evolution.max_versions_kept", 10)
	l.v.SetDefault("evolution.max_patch_size", 8192)
	l.v.SetDefault("evolution.analysis_timeout", "60s")
	l.v.SetDefault("evolution.export_json", false)

	// Knowledge
	l.v.SetDefault("knowledge.enabled", false)
	l.v.SetDefault("knowledge.embedder_provider", "")
	l.v.SetDefault("knowledge.embedder_model",
		"text-embedding-3-small")
	l.v.SetDefault("knowledge.vector_store", "inmemory")
	l.v.SetDefault("knowledge.max_results", 5)
	l.v.SetDefault("knowledge.enable_source_sync", false)
	l.v.SetDefault("knowledge.search_tool_name",
		"knowledge_search")

	// Workflow
	l.v.SetDefault("workflow.mode", "single")
	l.v.SetDefault("workflow.max_iterations", 10)
	l.v.SetDefault("workflow.cycle_mode", "default")
	// Registered (empty) so WUKONG_WORKFLOW_* overrides work.
	l.v.SetDefault("workflow.claude_code_bin", "")
	l.v.SetDefault("workflow.codex_bin", "")

	// Dify
	l.v.SetDefault("dify.enabled", false)
	l.v.SetDefault("dify.agent_name", "dify")
	l.v.SetDefault("dify.enable_streaming", false)
	l.v.SetDefault("dify.timeout", "120s")
	// Registered (empty) so WUKONG_DIFY_* overrides work.
	l.v.SetDefault("dify.base_url", "")
	l.v.SetDefault("dify.api_secret", "")
}

// setServerDefaults registers service endpoint defaults
// (a2a_server, agui, acp_server, acp_mcp).
func (l *Loader) setServerDefaults() {
	// A2A server
	l.v.SetDefault("a2a_server.enabled", false)
	l.v.SetDefault("a2a_server.address", ":9090")
	l.v.SetDefault("a2a_server.agent_name", "wukong")
	l.v.SetDefault("a2a_server.agent_description",
		"Wukong AI Agent - A2A service endpoint")

	// AG-UI
	l.v.SetDefault("agui.enabled", false)
	l.v.SetDefault("agui.address", ":8080")
	l.v.SetDefault("agui.path", "/agui")

	// ACP Server
	l.v.SetDefault("acp_server.enabled", false)
	l.v.SetDefault("acp_server.address", ":9091")
	l.v.SetDefault("acp_server.path", "/acp")
	l.v.SetDefault("acp_server.enable_streaming", true)
	// Security defaults — must match ACPServerConfig.Security
	// (ServerSecurityConfig). Previous code set the top-level key
	// "acp_server.auth_type" which has no corresponding mapstructure
	// tag and was silently dropped, leaving auth middleware
	// disabled even when users followed the docs.
	l.v.SetDefault("acp_server.security.auth.type", "")
	l.v.SetDefault("acp_server.security.auth.api_key", "")

	// ACP MCP Bridge
	l.v.SetDefault("acp_mcp.enabled", true)
	l.v.SetDefault("acp_mcp.address", ":3400")
	l.v.SetDefault("acp_mcp.path", "/mcp")

	// Standalone MCP server (exposes extensions via JSON-RPC 2.0).
	// Security defaults to empty auth — users exposing this on a
	// non-loopback interface MUST set security.auth.type explicitly
	// to prevent unauthorized tools/call access.
	l.v.SetDefault("mcp_server.enabled", false)
	l.v.SetDefault("mcp_server.address", ":3401")
	l.v.SetDefault("mcp_server.security.auth.type", "")
	l.v.SetDefault("mcp_server.security.auth.api_key", "")
}

// setObservabilityDefaults registers observability & evaluation
// defaults (eval, artifact, observability, telemetry).
func (l *Loader) setObservabilityDefaults() {
	// Eval
	l.v.SetDefault("eval.enabled", false)
	l.v.SetDefault("eval.evalset_path",
		".wukong/evals/default.evalset.json")
	l.v.SetDefault("eval.results_path",
		".wukong/evals/results.json")

	// Artifact
	l.v.SetDefault("artifact.backend", "inmemory")
	l.v.SetDefault("artifact.cos_bucket_url", "")
	l.v.SetDefault("artifact.cos_secret_id", "")
	l.v.SetDefault("artifact.cos_secret_key", "")

	// Observability
	l.v.SetDefault("observability.langfuse_enabled", false)
	l.v.SetDefault("observability.langfuse_host", "")
	l.v.SetDefault("observability.langfuse_public_key", "")
	l.v.SetDefault("observability.langfuse_secret_key", "")

	// Telemetry
	l.v.SetDefault("telemetry.enabled", false)
	l.v.SetDefault("telemetry.exporter_type", "console")
	l.v.SetDefault("telemetry.endpoint", "localhost:4317")
	l.v.SetDefault("telemetry.service_name", "wukong")
	l.v.SetDefault("telemetry.service_version", "1.0.0")
	l.v.SetDefault("telemetry.environment", "development")
	l.v.SetDefault("telemetry.sample_rate", 1.0)
}

// setGatewayDefaults registers Gateway and channel defaults. The
// concrete defaults live in the gateway package (which owns the config
// types); here we delegate to gateway.SetDefaults.
func (l *Loader) setGatewayDefaults() {
	gateway.SetDefaults(l.v)
}

// setOKFDefaults registers Open Knowledge Format (OKF)
// interoperability defaults.
func (l *Loader) setOKFDefaults() {
	l.v.SetDefault("okf.enabled", false)
	l.v.SetDefault("okf.bundle_dir", ".wukong/okf")
	l.v.SetDefault("okf.injector_enabled", false)
	l.v.SetDefault("okf.enrichment_enabled", false)
	l.v.SetDefault("okf.enrichment_output_dir", "")
	l.v.SetDefault("okf.auto_export", false)
	l.v.SetDefault("okf.register_in_ard", false)
}
