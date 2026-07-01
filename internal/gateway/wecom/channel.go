package wecom

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/gateway"
	"github.com/km269/wukong/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// channelName is the unique identifier for the WeCom channel.
	channelName = "wecom"

	// channelPath is the base route path for WeCom callbacks.
	channelPath = "/wecom"
)

// WeComChannel implements gateway.Channel for the WeCom/企业微信
// platform. It supports two connection modes:
//
//  1. AI Bot callback (recommended): JSON-based message callback
//     with streaming support via response_url or stream API.
//  2. Enterprise app callback: Encrypted XML-based callback with
//     AES-256-CBC decryption and SHA1 signature verification.
type WeComChannel struct {
	cfg    *config.WeComChannelConfig
	loop   *agent.CoreLoop
	crypto *WeComCrypto
	sender *WeComSender
}

// NewWeComChannel creates a new WeCom channel instance.
func NewWeComChannel(
	cfg *config.WukongConfig,
	loop *agent.CoreLoop,
) *WeComChannel {
	return &WeComChannel{
		cfg:    &cfg.Gateway.WeCom,
		loop:   loop,
		crypto: NewWeComCrypto(&cfg.Gateway.WeCom),
		sender: NewWeComSender(&cfg.Gateway.WeCom),
	}
}

// Name returns "wecom".
func (wc *WeComChannel) Name() string {
	return channelName
}

// RoutePath returns "/wecom".
func (wc *WeComChannel) RoutePath() string {
	return channelPath
}

// VerifyRequest validates the incoming WeCom callback request.
//
// WeCom supports two verification modes:
//
//	A) AI Bot mode (no encryption): The body is plain JSON.
//	   No signature verification needed; just read the body.
//
//	B) Enterprise app mode (with encryption): URL query contains
//	   msg_signature, timestamp, nonce. Body is XML with <Encrypt>.
//	   Verifies SHA1(msg_signature) and decrypts body.
func (wc *WeComChannel) VerifyRequest(
	r *http.Request,
) ([]byte, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, fmt.Errorf("wecom: read body: %w", err)
	}

	// Check if this is an encrypted callback (has msg_signature).
	sig, ts, nonce, _ := parseCallbackParams(r.URL.Query())
	if sig != "" && wc.crypto.HasCryptoEnabled() {
		// Enterprise app encrypted callback.
		if err := wc.crypto.VerifySignature(
			sig, ts, nonce, body,
		); err != nil {
			return nil, fmt.Errorf(
				"wecom: signature verification: %w", err)
		}

		// Decrypt the message body.
		var encMsg WeComXMLMessage
		if err := xml.Unmarshal(body, &encMsg); err != nil {
			return nil, fmt.Errorf(
				"wecom: parse encrypted xml: %w", err)
		}

		decrypted, err := wc.crypto.DecryptMessage(
			encMsg.Encrypt,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"wecom: decrypt message: %w", err)
		}
		return decrypted, nil
	}

	// AI Bot callback (plain JSON) or unencrypted callback.
	return body, nil
}

// ParseMessage converts a WeCom callback body into a unified
// GatewayMessage.
//
// Supports:
//   - AI Bot JSON callbacks (aibot_msg_callback format)
//   - Enterprise app decrypted XML callbacks
func (wc *WeComChannel) ParseMessage(
	body []byte,
) (*gateway.GatewayMessage, error) {
	// Try AI Bot JSON format first.
	if json.Valid(body) {
		msg, err := parseAIBotMessage(body)
		if err != nil {
			return nil, err
		}
		if msg != nil {
			return msg, nil
		}
	}

	// Try enterprise app XML format.
	var plain WeComPlainMessage
	if err := xml.Unmarshal(body, &plain); err == nil &&
		plain.MsgType != "" {
		return parsePlainXMLMessage(body)
	}

	return nil, fmt.Errorf(
		"wecom: unable to parse message format")
}

// BuildUserID constructs a Wukong user ID from the WeCom user.
// Format: "wecom:userid".
func (wc *WeComChannel) BuildUserID(
	msg *gateway.GatewayMessage,
) string {
	if msg.PlatformUserID == "" {
		return "wecom:anonymous"
	}
	return "wecom:" + msg.PlatformUserID
}

// BuildSessionID constructs a Wukong session ID from the WeCom
// conversation. Format: "wecom-chatid".
func (wc *WeComChannel) BuildSessionID(
	msg *gateway.GatewayMessage,
) string {
	if msg.ConversationID == "" {
		return "wecom-unknown"
	}
	return "wecom-" + msg.ConversationID
}

// SendReply processes agent events and sends the response back to
// WeCom. It supports two modes:
//   - Streaming: When stream_enabled is true, sends incremental
//     stream updates via the WeCom stream API.
//   - Simple reply: Collects all content and sends as a single
//     text message.
func (wc *WeComChannel) SendReply(
	ctx context.Context,
	msg *gateway.GatewayMessage,
	events <-chan *event.Event,
) error {
	if wc.cfg.StreamEnabled {
		return wc.sender.SendStreamingUpdates(ctx, msg, events)
	}

	// Fallback: collect all content and send as a single reply.
	var builder strings.Builder
	for evt := range events {
		if evt.Error != nil {
			return fmt.Errorf(
				"wecom: agent error: %s", evt.Error.Message)
		}
		if evt.Response != nil &&
			len(evt.Response.Choices) > 0 {
			delta := evt.Response.Choices[0].Delta.Content
			if delta != "" {
				builder.WriteString(delta)
			}
		}
	}

	content := builder.String()
	if content == "" {
		content = "处理完成。"
	}

	return wc.sender.SendTextReply(ctx, msg, content)
}

// HandlePlatformEvent handles WeCom-specific platform events.
//
// Supported events:
//   - "url_verify": For enterprise app URL verification (GET
//     request with echostr). Decrypts echostr and returns the
//     plaintext for WeCom to verify the callback URL.
//
// AI Bot mode does not require manual URL verification; WeCom
// handles it automatically in the admin console.
func (wc *WeComChannel) HandlePlatformEvent(
	w http.ResponseWriter,
	evt *gateway.PlatformEvent,
) ([]byte, error) {
	switch evt.Type {
	case "url_verify":
		return wc.handleURLVerification(evt)

	default:
		// Unknown event type; ignore.
		_ = w // w is used by the gateway for status codes.
		return nil, nil
	}
}

// handleURLVerification processes the WeCom URL verification
// challenge for enterprise app mode.
//
// WeCom sends a GET request with:
//
//	msg_signature, timestamp, nonce, echostr
//
// We must:
//  1. Verify msg_signature
//  2. Decrypt echostr using AES key
//  3. Return the decrypted challenge string
func (wc *WeComChannel) handleURLVerification(
	evt *gateway.PlatformEvent,
) ([]byte, error) {
	sig, ts, nonce, echostr := parseCallbackParamsFromEvent(evt)
	if echostr == "" {
		return nil, fmt.Errorf(
			"wecom: URL verification missing echostr")
	}

	// Verify the signature.
	if !wc.crypto.VerifyURLSignature(sig, ts, nonce, echostr) {
		return nil, fmt.Errorf(
			"wecom: URL verification signature mismatch")
	}

	// Decrypt echostr.
	challenge, err := wc.crypto.DecryptEchostr(echostr)
	if err != nil {
		return nil, fmt.Errorf(
			"wecom: decrypt echostr: %w", err)
	}

	util.Logger.Info("wecom: URL verification completed",
		slog.String("challenge_preview",
			challenge[:min(8, len(challenge))]+"..."))

	return []byte(challenge), nil
}

// parseCallbackParamsFromEvent extracts msg_signature, timestamp,
// nonce, and echostr from a PlatformEvent's metadata.
func parseCallbackParamsFromEvent(
	evt *gateway.PlatformEvent,
) (sig, ts, nonce, echostr string) {
	if evt.Metadata == nil {
		return
	}
	sig = evt.Metadata["msg_signature"]
	ts = evt.Metadata["timestamp"]
	nonce = evt.Metadata["nonce"]
	echostr = evt.Metadata["echostr"]
	return
}

// Ensure Channel interface compliance.
var _ gateway.Channel = (*WeComChannel)(nil)

// Compile-time check for valid imports.
var _ = model.NewUserMessage("")
