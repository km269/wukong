package gateway

import (
	"time"

	"github.com/spf13/viper"
)

// This file defines the gateway's own configuration types. They used
// to live in internal/config/types.go; they have been sunk into the
// gateway package so the gateway owns its config alongside its code,
// while the root WukongConfig embeds gateway.GatewayConfig.
//
// mapstructure tags are unchanged, so YAML keys (gateway.*,
// gateway.feishu.*) and viper defaults behave exactly as before.

// GatewayConfig defines settings for the messaging gateway. The
// gateway drives all registered Channels, each of which owns its own
// inbound transport (e.g. a Feishu WebSocket long-connection). There
// is no HTTP listener — the gateway is transport-agnostic.
type GatewayConfig struct {
	// Enabled enables the gateway.
	// Default: false.
	Enabled bool `mapstructure:"enabled"`

	// DefaultTimeout is the maximum duration for an agent run
	// triggered by a platform message. Default: "120s".
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

	// Slack contains the Slack channel configuration.
	Slack SlackChannelConfig `mapstructure:"slack"`

	// Discord contains the Discord channel configuration.
	Discord DiscordChannelConfig `mapstructure:"discord"`

	// WebChat contains the web-based chat channel configuration.
	WebChat WebChatChannelConfig `mapstructure:"web_chat"`
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
	AppSecret string `mapstructure:"app_secret"`

	// APIBase is the base URL for the Feishu/Lark Open API.
	// Default: "https://open.feishu.cn/open-apis".
	// For Lark (international), use "https://open.larksuite.com/open-apis".
	APIBase string `mapstructure:"api_base"`

	// EncryptKey is the event encryption key from the Feishu app
	// settings. Required only if the app's encryption strategy is
	// enabled (the SDK uses it to decrypt event payloads received
	// over the long-connection). Supports ${ENV_VAR} expansion.
	EncryptKey string `mapstructure:"encrypt_key"`

	// VerificationToken is the legacy event subscription
	// verification token. In long-connection mode the SDK no
	// longer verifies it; the field is retained for backward
	// config compatibility. Supports ${ENV_VAR} expansion.
	// Deprecated: has no effect in long-connection mode.
	VerificationToken string `mapstructure:"verification_token"`

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

// SlackChannelConfig defines settings for the Slack channel.
type SlackChannelConfig struct {
	Enabled           bool   `mapstructure:"enabled"`
	AppToken          string `mapstructure:"app_token"`
	BotToken          string `mapstructure:"bot_token"`
	VerificationToken string `mapstructure:"verification_token"`
	SigningSecret     string `mapstructure:"signing_secret"`
	APIBase           string `mapstructure:"api_base"`
}

// DiscordChannelConfig defines settings for the Discord channel.
type DiscordChannelConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	BotToken  string `mapstructure:"bot_token"`
	ServerID  string `mapstructure:"server_id"`
	ChannelID string `mapstructure:"channel_id"`
	APIBase   string `mapstructure:"api_base"`
}

// WebChatChannelConfig defines settings for the web-based chat channel.
type WebChatChannelConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	Address        string        `mapstructure:"address"`
	Path           string        `mapstructure:"path"`
	MaxSessions    int           `mapstructure:"max_sessions"`
	SessionTimeout time.Duration `mapstructure:"session_timeout"`
	CertFile       string        `mapstructure:"cert_file"`
	KeyFile        string        `mapstructure:"key_file"`
}

// SetDefaults registers the gateway's viper defaults. It is invoked
// by the central config loader's setGatewayDefaults.
func SetDefaults(v *viper.Viper) {
	// Gateway
	v.SetDefault("gateway.enabled", false)
	v.SetDefault("gateway.default_timeout", "900s")
	v.SetDefault("gateway.max_concurrent_sessions", 100)
	v.SetDefault("gateway.message_dedup_ttl", "5m")
	v.SetDefault("gateway.rate_limit_per_user", 20)
	v.SetDefault("gateway.rate_limit_window", "60s")

	// Feishu channel
	v.SetDefault("gateway.feishu.enabled", false)
	v.SetDefault("gateway.feishu.api_base",
		"https://open.feishu.cn/open-apis")
	v.SetDefault("gateway.feishu.stream_card_enabled", true)
	v.SetDefault("gateway.feishu.stream_card_update_interval", "500ms")
	v.SetDefault("gateway.feishu.max_message_length", 4096)
	v.SetDefault("gateway.feishu.enable_file_receive", false)

	// Slack channel
	v.SetDefault("gateway.slack.enabled", false)
	v.SetDefault("gateway.slack.api_base",
		"https://slack.com/api")

	// Discord channel
	v.SetDefault("gateway.discord.enabled", false)
	v.SetDefault("gateway.discord.api_base",
		"https://discord.com/api/v10")

	// Web Chat channel
	v.SetDefault("gateway.web_chat.enabled", false)
	v.SetDefault("gateway.web_chat.address", ":8080")
	v.SetDefault("gateway.web_chat.path", "/chat")
	v.SetDefault("gateway.web_chat.max_sessions", 100)
	v.SetDefault("gateway.web_chat.session_timeout", "30m")
}
