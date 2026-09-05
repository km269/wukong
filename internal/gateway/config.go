package gateway

import (
	"github.com/km269/wukong/internal/config"
	"github.com/spf13/viper"
)

// The gateway configuration types (GatewayConfig,
// FeishuChannelConfig) live in internal/config/types_gateway.go —
// the root WukongConfig embeds GatewayConfig, so keeping the types in
// the gateway package made internal/config depend on internal/gateway
// (a reverse dependency). This file keeps type aliases so every
// existing reference inside the gateway package (and in external
// call sites) compiles unchanged.
//
// mapstructure tags are unchanged, so YAML keys (gateway.*,
// gateway.feishu.*) and viper defaults behave exactly as before.

// GatewayConfig is an alias of config.GatewayConfig.
type GatewayConfig = config.GatewayConfig

// FeishuChannelConfig is an alias of config.FeishuChannelConfig.
type FeishuChannelConfig = config.FeishuChannelConfig

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
}
