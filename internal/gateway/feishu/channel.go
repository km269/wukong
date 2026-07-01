// Package feishu provides the Feishu/Lark channel implementation
// for the Wukong gateway. It handles:
//   - URL challenge verification
//   - HMAC-SHA256 request signature verification
//   - AES-256-CBC event body decryption
//   - Event callback parsing (text, image, file messages)
//   - Incremental streaming card replies via tenant_access_token
//   - Proactive API reply via response_url (when available)
//
// The channel supports a "long connection" streaming mode where the
// agent's response is incrementally displayed via Feishu message cards
// that are periodically patched with new content as the LLM generates
// tokens.
package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/km269/wukong/internal/agent"
	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/gateway"
	"github.com/km269/wukong/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

const (
	// channelName is the unique identifier for the Feishu channel.
	channelName = "feishu"

	// channelPath is the base route path for Feishu callbacks.
	channelPath = "/feishu"
)

// FeishuChannel implements gateway.Channel for the Feishu/Lark
// platform. It handles event subscription callbacks, URL verification,
// and message processing with support for streaming card replies.
//
// Architecture:
//   - crypto: Request signature verification and AES decryption
//   - sender: Message reply with streaming card and API support via
//     the official Lark OpenAPI SDK (larksuite/oapi-sdk-go/v3).
//     The SDK handles tenant_access_token management automatically.
type FeishuChannel struct {
	cfg    *config.FeishuChannelConfig
	loop   *agent.CoreLoop
	crypto *FeishuCrypto
	sender *FeishuSender
}

// NewFeishuChannel creates a new Feishu channel instance.
func NewFeishuChannel(
	cfg *config.WukongConfig,
	loop *agent.CoreLoop,
) *FeishuChannel {
	return &FeishuChannel{
		cfg:    &cfg.Gateway.Feishu,
		loop:   loop,
		crypto: NewFeishuCrypto(&cfg.Gateway.Feishu),
		sender: NewFeishuSender(&cfg.Gateway.Feishu),
	}
}

// Name returns "feishu".
func (fc *FeishuChannel) Name() string {
	return channelName
}

// RoutePath returns "/feishu".
func (fc *FeishuChannel) RoutePath() string {
	return channelPath
}

// VerifyRequest validates the Feishu event callback signature.
//
// Feishu signs each callback with a HMAC-SHA256 digest of
// (timestamp + nonce + encrypt_key + body). The signature is sent
// in the X-Lark-Signature header.
//
// For encrypted events, the body is first AES-256-CBC decrypted.
func (fc *FeishuChannel) VerifyRequest(
	r *http.Request,
) ([]byte, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, fmt.Errorf("feishu: read body: %w", err)
	}

	if err := fc.crypto.VerifySignature(r.Header, body); err != nil {
		return nil, fmt.Errorf("feishu: signature verification: %w",
			err)
	}

	// If the body is encrypted, decrypt it.
	if fc.crypto.IsEncrypted(body) {
		decrypted, err := fc.crypto.Decrypt(body)
		if err != nil {
			return nil, fmt.Errorf("feishu: decrypt: %w", err)
		}
		return decrypted, nil
	}

	return body, nil
}

// ParseMessage converts a Feishu event callback JSON body into a
// unified GatewayMessage.
//
// Supported event types:
//   - im.message.receive_v1: text, image, file, audio, media messages
//   - Other event types are returned as nil (ignored silently)
func (fc *FeishuChannel) ParseMessage(
	body []byte,
) (*gateway.GatewayMessage, error) {
	var event FeishuEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("feishu: parse event: %w", err)
	}

	// Handle event wrapper (event_callback type).
	if event.Type == "event_callback" && event.Event != nil {
		return fc.parseEventCallback(&event), nil
	}

	return nil, fmt.Errorf(
		"feishu: unsupported event type: %s", event.Type)
}

// parseEventCallback handles im.message.receive_v1 events.
// Supports text, image, file, audio, and media message types.
func (fc *FeishuChannel) parseEventCallback(
	event *FeishuEvent,
) *gateway.GatewayMessage {
	ev := event.Event

	if ev.Type != "im.message.receive_v1" {
		util.Logger.Debug("feishu: non-message event",
			slog.String("type", ev.Type))
		return nil
	}

	msg := &gateway.GatewayMessage{
		ContentType: ev.MsgType,
		MessageID:   ev.MessageID,
		Timestamp:   0,
		RawData:     mustMarshal(ev),
	}

	// Extract user and conversation IDs.
	if ev.Sender != nil && ev.Sender.SenderID != nil {
		msg.PlatformUserID = ev.Sender.SenderID.OpenID
	}
	msg.ConversationID = ev.ChatID

	// Feishu provides a response_url for certain interaction types
	// (card actions, etc.). For standard im.message.receive_v1,
	// response_url is not available; we use tenant_access_token
	// for proactive replies instead.
	if event.ResponseURL != "" {
		msg.ResponseURL = event.ResponseURL
	}

	// Extract content based on message type.
	switch ev.MsgType {
	case "text":
		content := extractTextContent(ev.Content)
		msg.Content = strings.TrimSpace(content)

	case "image":
		msg.Content = fmt.Sprintf(
			"[图片: %s]", ev.MessageID)

	case "file":
		if fc.cfg.EnableFileReceive {
			msg.Content = fmt.Sprintf(
				"[文件: %s]", ev.MessageID)
		} else {
			msg.Content = "[文件消息暂不支持]"
		}

	case "audio":
		msg.Content = "[语音消息]"

	case "media":
		msg.Content = "[富媒体消息]"

	default:
		msg.Content = fmt.Sprintf(
			"[收到消息类型: %s]", ev.MsgType)
	}

	return msg
}

// BuildUserID constructs a Wukong user ID from the Feishu user.
// Format: "feishu:ou_xxxx".
func (fc *FeishuChannel) BuildUserID(
	msg *gateway.GatewayMessage,
) string {
	if msg.PlatformUserID == "" {
		return "feishu:anonymous"
	}
	return "feishu:" + msg.PlatformUserID
}

// BuildSessionID constructs a Wukong session ID from the Feishu
// conversation. Format: "feishu-oc_xxxx".
func (fc *FeishuChannel) BuildSessionID(
	msg *gateway.GatewayMessage,
) string {
	if msg.ConversationID == "" {
		return "feishu-unknown"
	}
	return "feishu-" + msg.ConversationID
}

// SendReply processes agent events and sends the response back to
// Feishu. It supports two modes:
//
// Streaming card (StreamCardEnabled=true):
//   Creates a message card via Feishu API, then periodically patches
//   it with accumulated content as the LLM generates tokens. This
//   provides a "long connection" streaming experience.
//
// Text reply (StreamCardEnabled=false):
//   Collects all streaming content from the agent and sends a single
//   text message via the Feishu Send Message API using
//   tenant_access_token.
func (fc *FeishuChannel) SendReply(
	ctx context.Context,
	msg *gateway.GatewayMessage,
	events <-chan *event.Event,
) error {
	if fc.cfg.StreamCardEnabled {
		return fc.sender.SendStreamCard(ctx, msg, events)
	}

	// Non-streaming mode: collect all content, send as single reply.
	var builder strings.Builder
	for evt := range events {
		if evt.Error != nil {
			util.Logger.Warn("feishu: agent event error",
				slog.String("error", evt.Error.Message))
			continue
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

	return fc.sender.SendTextReply(ctx, msg, content)
}

// HandlePlatformEvent handles Feishu-specific platform events.
//
// Supported events:
//   - "url_verify": Returns the challenge string to complete URL
//     verification.
func (fc *FeishuChannel) HandlePlatformEvent(
	w http.ResponseWriter,
	evt *gateway.PlatformEvent,
) ([]byte, error) {
	switch evt.Type {
	case "url_verify":
		return fc.handleURLVerification(evt.Data)

	default:
		// Unknown event type; ignore.
		return nil, nil
	}
}

// handleURLVerification processes the Feishu URL challenge during
// event subscription setup.
func (fc *FeishuChannel) handleURLVerification(
	data json.RawMessage,
) ([]byte, error) {
	var challenge struct {
		Challenge string `json:"challenge"`
		Token     string `json:"token"`
		Type      string `json:"type"`
	}
	if err := json.Unmarshal(data, &challenge); err != nil {
		return nil, fmt.Errorf(
			"feishu: parse challenge: %w", err)
	}

	if challenge.Type != "url_verification" {
		return nil, fmt.Errorf(
			"feishu: unexpected challenge type: %s",
			challenge.Type)
	}

	// Verify token matches the configured verification token.
	if challenge.Token != "" &&
		challenge.Token != fc.cfg.VerificationToken {
		return nil, fmt.Errorf(
			"feishu: verification token mismatch")
	}

	response := map[string]string{
		"challenge": challenge.Challenge,
	}

	respBytes, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf(
			"feishu: marshal challenge response: %w", err)
	}

	util.Logger.Info("feishu: URL verification completed",
		slog.String("challenge", challenge.Challenge[:8]+"..."))

	return respBytes, nil
}

// Ensure Channel interface compliance.
var _ gateway.Channel = (*FeishuChannel)(nil)
