// Package feishu provides the Feishu/Lark channel implementation
// for the Wukong gateway.
//
// Transport: this channel receives messages over a Feishu WebSocket
// long-connection. Wukong acts as a client and dials out to the Feishu
// open platform (wss://), so no public callback URL, domain, or HTTPS
// ingress is required — it works from a local or intranet host. The
// Lark SDK (larksuite/oapi-sdk-go/v3) handles authentication,
// reconnection, heartbeat, and message-fragment reassembly internally.
//
// Event flow:
//   - larkws.Client receives im.message.receive_v1 frames
//   - the registered dispatcher calls onMessage with a typed
//     *larkim.P2MessageReceiveV1
//   - onMessage parses it into a *gateway.GatewayMessage and invokes
//     the gateway-supplied MessageHandler (which runs the agent
//     asynchronously)
//
// Replies still use the Feishu Open API via FeishuSender (create +
// periodically patch an interactive card for a streaming experience),
// authenticated with a tenant_access_token managed automatically by
// the Lark SDK client.
package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/km269/wukong/internal/gateway"
	"github.com/km269/wukong/internal/util"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

const (
	// channelName is the unique identifier for the Feishu channel.
	channelName = "feishu"
)

// FeishuChannel implements gateway.Channel for the Feishu/Lark
// platform using a WebSocket long-connection to receive events and
// the Lark Open API to send replies.
//
// Architecture:
//   - wsClient: outbound WebSocket long-connection to the Feishu
//     platform (receives events; SDK manages auth/reconnect/heartbeat).
//   - sender: FeishuSender for replies via the Lark SDK (auto
//     tenant_access_token management), supporting streaming cards.
type FeishuChannel struct {
	cfg      *gateway.FeishuChannelConfig
	sender   *FeishuSender
	wsClient *larkws.Client
}

// NewFeishuChannel creates a new Feishu channel instance.
func NewFeishuChannel(
	cfg *gateway.FeishuChannelConfig,
) *FeishuChannel {
	return &FeishuChannel{
		cfg:    cfg,
		sender: NewFeishuSender(cfg),
	}
}

// Validate checks that the credentials required to operate are present.
// It should be called before registering the channel; a non-nil error
// means the channel cannot function and should not be registered.
//
// Required (long-connection mode):
//   - AppID/AppSecret: dial the WebSocket gateway AND obtain the
//     tenant_access_token used to send replies.
//
// Optional but recommended:
//   - EncryptKey:        needed only if "encryption strategy" is
//     enabled on the Feishu app (the SDK decrypts
//     event payloads with it). Not used for HTTP
//     signature verification (none in WS mode).
//   - VerificationToken: legacy field, no longer verified by the SDK
//     in long-connection mode; kept for backward
//     config compatibility.
//
// Missing optional fields only produce a warning, since the channel
// can still operate without encryption.
func (fc *FeishuChannel) Validate() error {
	var missing []string
	if fc.cfg.AppID == "" {
		missing = append(missing, "app_id")
	}
	if fc.cfg.AppSecret == "" {
		missing = append(missing, "app_secret")
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"feishu: missing required credentials: %s "+
				"(check FEISHU_APP_ID / FEISHU_APP_SECRET and config)",
			strings.Join(missing, ", "))
	}

	if fc.cfg.EncryptKey == "" {
		util.Logger.Warn("feishu: encrypt_key not configured — " +
			"event payload decryption is disabled. Set FEISHU_ENCRYPT_KEY " +
			"if the encryption strategy is enabled on the Feishu app.")
	}
	return nil
}

// Name returns "feishu".
func (fc *FeishuChannel) Name() string {
	return channelName
}

// Start dials the Feishu WebSocket long-connection gateway and blocks
// until ctx is cancelled or a fatal connection error occurs. The Lark
// SDK reconnects and heartbeats automatically. For each inbound
// im.message.receive_v1 event it invokes handle (the gateway dispatch
// pipeline), which runs the agent asynchronously.
func (fc *FeishuChannel) Start(
	ctx context.Context, handle gateway.MessageHandler,
) error {
	// Build the event dispatcher. In long-connection mode the SDK does
	// NOT verify HTTP signatures (authentication happens at connection
	// establishment using the app secret). encryptKey is still used by
	// the dispatcher to decrypt event payloads when the app has
	// encryption enabled.
	disp := dispatcher.NewEventDispatcher(
		fc.cfg.VerificationToken, fc.cfg.EncryptKey)
	disp.OnP2MessageReceiveV1(func(
		_ context.Context, evt *larkim.P2MessageReceiveV1,
	) error {
		msg := fc.parseP2MessageReceiveV1(evt)
		if msg == nil {
			return nil
		}
		msg.Platform = channelName
		// Dispatch runs synchronously here; the gateway launches the
		// agent in a background goroutine, so this returns quickly and
		// does not stall the SDK's receive loop.
		handle(ctx, msg)
		return nil
	})

	fc.wsClient = larkws.NewClient(
		fc.cfg.AppID, fc.cfg.AppSecret,
		larkws.WithEventHandler(disp),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)

	util.Logger.Info("feishu: connecting long-connection gateway",
		slog.String("api_base", fc.cfg.APIBase))

	// wsClient.Start blocks (the SDK's Start does not return on ctx
	// cancellation in v3.9.7, so we run it in a goroutine and wait on
	// ctx here, returning context.Canceled when shutdown is requested).
	errCh := make(chan error, 1)
	go func() {
		errCh <- fc.wsClient.Start(ctx)
	}()

	select {
	case <-ctx.Done():
		util.Logger.Info("feishu: long-connection context cancelled")
		return ctx.Err()
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("feishu: long-connection exited: %w", err)
		}
		return nil
	}
}

// Stop releases channel resources. The WebSocket connection is torn
// down when Start's context is cancelled by the gateway; Stop only
// frees the reply sender's resources.
func (fc *FeishuChannel) Stop(_ context.Context) error {
	return fc.sender.Close()
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
//
//	Creates a message card via Feishu API, then periodically patches
//	it with accumulated content as the LLM generates tokens. This
//	provides a streaming experience.
//
// Text reply (StreamCardEnabled=false):
//
//	Collects all streaming content from the agent and sends a single
//	text message via the Feishu Send Message API using
//	tenant_access_token.
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

// Ensure Channel interface compliance.
var _ gateway.Channel = (*FeishuChannel)(nil)
