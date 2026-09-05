package config

import "time"

// This file defines the messaging-gateway configuration types. They
// lived in internal/gateway for a while so the gateway package could
// own its config alongside its code, but that made internal/config
// depend on internal/gateway (the root WukongConfig embeds
// GatewayConfig) — a reverse dependency that forced the gateway and
// server packages into callback workarounds. The types now live here
// (dependency direction: gateway/server → config); the gateway
// package keeps type aliases so all existing references compile
// unchanged. mapstructure tags are unchanged, so YAML keys
// (gateway.*, gateway.feishu.*) and viper defaults behave exactly as
// before.

// GatewayConfig defines settings for the messaging gateway. The
// gateway drives all registered Channels, each of which owns its own
// inbound transport (e.g. a Feishu WebSocket long-connection). There
// is no HTTP listener — the gateway is transport-agnostic.
type GatewayConfig struct {
	// Enabled enables the gateway.
	// Default: false.
	Enabled bool `mapstructure:"enabled"`

	// DefaultTimeout is the maximum duration for an agent run
	// triggered by a platform message. Default: "900s".
	DefaultTimeout time.Duration `mapstructure:"default_timeout"`

	// MaxConcurrentSessions limits concurrent agent sessions
	// across all channels. Default: 100.
	MaxConcurrentSessions int `mapstructure:"max_concurrent_sessions"`

	// MessageDedupTTL is the deduplication window for platform
	// messages. Messages with the same MessageID within this
	// window are silently dropped. Default: "5m".
	MessageDedupTTL time.Duration `mapstructure:"message_dedup_ttl"`

	// RateLimitPerUser is the maximum number of agent runs per
	// user within the rate limit window. Default: 20.
	RateLimitPerUser int `mapstructure:"rate_limit_per_user"`

	// RateLimitWindow is the sliding window duration for per-user
	// rate limiting. Default: "60s".
	RateLimitWindow time.Duration `mapstructure:"rate_limit_window"`

	// Feishu contains the Feishu/Lark channel configuration.
	Feishu FeishuChannelConfig `mapstructure:"feishu"`
}

// FeishuChannelConfig defines settings for the Feishu/Lark channel,
// which receives messages over a WebSocket long-connection (so no
// public callback URL is required) and replies via the Lark Open API.
type FeishuChannelConfig struct {
	// Enabled enables the Feishu channel. Default: false.
	Enabled bool `mapstructure:"enabled"`

	// AppID is the Feishu application ID (cli_xxx).
	AppID string `mapstructure:"app_id"`

	// AppSecret is the Feishu application secret. Used both to
	// establish the WebSocket long-connection and to obtain the
	// tenant_access_token for sending replies.
	// Supports ${ENV_VAR} expansion.
	AppSecret string `mapstructure:"app_secret" envexpand:"true"`

	// APIBase is the base URL for the Feishu/Lark Open API.
	// Default: "https://open.feishu.cn/open-apis".
	// For Lark (international), use "https://open.larksuite.com/open-apis".
	APIBase string `mapstructure:"api_base"`

	// EncryptKey is the event encryption key from the Feishu app
	// settings. Required only if the app's encryption strategy is
	// enabled (the SDK uses it to decrypt event payloads received
	// over the long-connection). Supports ${ENV_VAR} expansion.
	EncryptKey string `mapstructure:"encrypt_key" envexpand:"true"`

	// VerificationToken is the legacy event subscription
	// verification token. In long-connection mode the SDK no
	// longer verifies it; the field is retained for backward
	// config compatibility. Supports ${ENV_VAR} expansion.
	// Deprecated: has no effect in long-connection mode.
	VerificationToken string `mapstructure:"verification_token" envexpand:"true"`

	// StreamCardEnabled enables streaming card replies for
	// real-time display. When disabled, a single text reply is
	// sent after the agent completes.
	// Default: true.
	StreamCardEnabled bool `mapstructure:"stream_card_enabled"`

	// StreamCardUpdateInterval controls how often the streaming
	// card content is updated. Default: "500ms".
	StreamCardUpdateInterval time.Duration `mapstructure:"stream_card_update_interval"`

	// MaxMessageLength is the maximum characters per message
	// sent to Feishu. Messages exceeding this are truncated.
	// Default: 4096.
	MaxMessageLength int `mapstructure:"max_message_length"`

	// EnableFileReceive controls whether file messages from
	// users are accepted. Default: false.
	EnableFileReceive bool `mapstructure:"enable_file_receive"`
}
