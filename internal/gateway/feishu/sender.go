package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/km269/wukong/internal/gateway"
	"github.com/km269/wukong/internal/util"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

const (
	// defaultMaxMessageLength is the default character limit
	// for messages sent to Feishu.
	defaultMaxMessageLength = 4096

	// defaultStreamUpdateInterval is the default interval for
	// incremental card content updates.
	defaultStreamUpdateInterval = 500 * time.Millisecond

	// senderHTTPTimeout is the HTTP client timeout for API requests.
	senderHTTPTimeout = 30 * time.Second
)

// FeishuSender handles sending replies to Feishu. It uses the Lark
// OpenAPI SDK (larksuite/oapi-sdk-go/v3) for authenticated API calls
// with automatic tenant_access_token management. Supports:
//   - Proactive text replies via SDK (auto token management)
//   - Incremental streaming cards (create → periodic update → finish)
//     via SDK for a "long connection" streaming experience
//   - Proactive response_url replies (when available, bypasses SDK)
type FeishuSender struct {
	cfg           *gateway.FeishuChannelConfig
	larkClient    *lark.Client
	respURLClient *http.Client
}

// NewFeishuSender creates a new FeishuSender with the Lark SDK client
// for authenticated API calls. The SDK handles token caching and
// auto-refresh internally.
func NewFeishuSender(
	cfg *gateway.FeishuChannelConfig,
) *FeishuSender {
	apiBase := cfg.APIBase
	if apiBase == "" {
		apiBase = "https://open.feishu.cn/open-apis"
	}

	larkClient := lark.NewClient(
		cfg.AppID, cfg.AppSecret,
		lark.WithOpenBaseUrl(apiBase),
	)

	return &FeishuSender{
		cfg:        cfg,
		larkClient: larkClient,
		respURLClient: &http.Client{
			Timeout: senderHTTPTimeout,
		},
	}
}

// Close releases the sender's resources. The Lark SDK client does not
// require explicit teardown; this closes idle connections held by the
// response_url HTTP client. It is safe to call multiple times.
func (fs *FeishuSender) Close() error {
	if fs.respURLClient != nil {
		fs.respURLClient.CloseIdleConnections()
	}
	return nil
}

// SendTextReply sends a text message via the Feishu Send Message API
// using the Lark SDK. This is used when stream_card_enabled is false
// and we want to send a single complete reply.
func (fs *FeishuSender) SendTextReply(
	ctx context.Context,
	msg *gateway.GatewayMessage,
	content string,
) error {
	if content == "" {
		content = "处理完成。"
	}

	maxLen := fs.cfg.MaxMessageLength
	if maxLen <= 0 {
		maxLen = defaultMaxMessageLength
	}
	content = sanitizeContent(content, maxLen)

	// If ResponseURL is available, use it (bypasses token need).
	if msg.ResponseURL != "" {
		return fs.sendResponseURLReply(ctx, msg.ResponseURL, content)
	}

	// Otherwise, send via Lark SDK with auto token management.
	return fs.sendTextViaAPI(ctx, msg, content)
}

// SendStreamCard implements incremental streaming for a "long
// connection" experience:
//
//  1. Creates an initial card message via the Lark SDK
//  2. Periodically patches the card with accumulated content
//     (interval: StreamCardUpdateInterval, default 500ms)
//  3. Sends a final update when the agent completes
//
// Falls back gracefully:
//   - ResponseURL available → collect-all + send via response_url
//   - Card creation fails → collect-all + send as text via SDK
//
// This provides real-time streaming display in the Feishu client
// without requiring WebSocket support.
func (fs *FeishuSender) SendStreamCard(
	ctx context.Context,
	msg *gateway.GatewayMessage,
	events <-chan *event.Event,
) error {
	// Card streaming requires valid credentials and no ResponseURL.
	if msg.ResponseURL != "" {
		// Fallback: drain all events and send as a single
		// reply via response_url.
		content := drainEventContent(ctx, events)
		if content == "" {
			content = "处理完成。"
		}
		return fs.sendResponseURLReply(
			ctx, msg.ResponseURL, content)
	}

	// Determine the receive_id and receive_id_type for sending
	// the message back to the same conversation.
	receiveID, receiveIDType := fs.resolveReceiveID(msg)

	// Create the initial card message via Lark SDK.
	initialCard := fs.buildCardJSON("思考中...", false)
	messageID, err := fs.createCardMessage(
		ctx, receiveID, receiveIDType, initialCard)
	if err != nil {
		util.Logger.Error("feishu: create card failed",
			slog.String("error", err.Error()))
		// Fallback: drain events and send as text via SDK.
		content := drainEventContent(ctx, events)
		if content == "" {
			content = "处理完成。"
		}
		return fs.sendTextViaAPI(ctx, msg, content)
	}

	// Start the streaming loop: read events and periodically
	// update the card.
	updateInterval := fs.cfg.StreamCardUpdateInterval
	if updateInterval <= 0 {
		updateInterval = defaultStreamUpdateInterval
	}

	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()

	var (
		builder        strings.Builder
		lastToolName   string
		contentDirty   bool
		streamFinished bool
		mu             sync.Mutex // guards builder & contentDirty
	)

	// updateCardBySDK safely reads the accumulated content and
	// sends a card patch via Lark SDK.
	updateCardBySDK := func(final bool) {
		mu.Lock()
		if !contentDirty && !final {
			mu.Unlock()
			return
		}
		content := builder.String()
		contentDirty = false
		mu.Unlock()

		if content == "" && !final {
			return
		}
		if final && content == "" {
			content = "处理完成。"
		}

		card := fs.buildCardJSON(content, final)
		if err := fs.patchCardMessage(ctx, messageID, card); err != nil {
			util.Logger.Warn("feishu: patch card failed",
				slog.String("error", err.Error()))
		}
	}

	// Main event loop.
	for {
		select {
		case <-ctx.Done():
			// Context cancelled; send final update.
			updateCardBySDK(true)
			return ctx.Err()

		case <-ticker.C:
			updateCardBySDK(false)

		case evt, ok := <-events:
			if !ok {
				// Channel closed; stream finished.
				updateCardBySDK(true)
				return nil
			}

			if evt.Error != nil {
				util.Logger.Warn("feishu: stream event error",
					slog.String("error", evt.Error.Message))
				continue
			}

			// Check for runner completion first.
			if evt.IsRunnerCompletion() {
				streamFinished = true
			}

			// Process delta content.
			if evt.Response != nil &&
				len(evt.Response.Choices) > 0 {
				choice := evt.Response.Choices[0]
				delta := choice.Delta.Content
				if delta != "" {
					mu.Lock()
					builder.WriteString(delta)
					contentDirty = true
					mu.Unlock()
				}

				// Track tool calls.
				if len(choice.Message.ToolCalls) > 0 {
					lastToolName = choice.Message.
						ToolCalls[0].Function.Name
					mu.Lock()
					contentDirty = true
					mu.Unlock()
				}
			}

			// Tool calls trigger a card update on the next
			// tick to show tool execution status.
			if lastToolName != "" {
				mu.Lock()
				fmt.Fprintf(&builder,
					"\n\n> 🔧 调用工具: %s",
					lastToolName)
				contentDirty = true
				lastToolName = ""
				mu.Unlock()
			}

			if streamFinished {
				updateCardBySDK(true)
				return nil
			}
		}
	}
}

// resolveReceiveID determines the correct receive_id and
// receive_id_type for sending a message back.
//
// Feishu rules:
//   - p2p (personal chat): receive_id = sender open_id,
//     receive_id_type = "open_id"
//   - group chat: receive_id = chat_id,
//     receive_id_type = "chat_id"
func (fs *FeishuSender) resolveReceiveID(
	msg *gateway.GatewayMessage,
) (string, string) {
	// Default: send back to the conversation.
	// If ConversationID looks like a chat ID (oc_ prefix), it's a
	// group chat. Otherwise, use the user's open_id.
	if msg.ConversationID != "" &&
		strings.HasPrefix(msg.ConversationID, "oc_") {
		return msg.ConversationID, "chat_id"
	}
	// Personal chat: use the sender's open_id.
	if msg.PlatformUserID != "" {
		return msg.PlatformUserID, "open_id"
	}
	// Fallback: use conversation as chat_id.
	return msg.ConversationID, "chat_id"
}

// drainEventContent reads all remaining events from an event channel
// and returns the accumulated text content. If ch is nil, returns
// empty string.
func drainEventContent(
	ctx context.Context,
	ch <-chan *event.Event,
) string {
	if ch == nil {
		return ""
	}
	var builder strings.Builder
	for {
		select {
		case <-ctx.Done():
			return builder.String()
		case evt, ok := <-ch:
			if !ok {
				return builder.String()
			}
			if evt.Error != nil {
				continue
			}
			if evt.Response != nil &&
				len(evt.Response.Choices) > 0 {
				delta := evt.Response.
					Choices[0].Delta.Content
				if delta != "" {
					builder.WriteString(delta)
				}
			}
		}
	}
}

// sendTextViaAPI sends a text message via the Lark SDK's
// im/v1/messages Create API.
func (fs *FeishuSender) sendTextViaAPI(
	ctx context.Context,
	msg *gateway.GatewayMessage,
	content string,
) error {
	util.Logger.Info("feishu: sendTextViaAPI called",
		slog.String("user_id", msg.PlatformUserID),
		slog.String("conversation_id", msg.ConversationID),
		slog.Int("content_length", len(content)))

	if fs.larkClient == nil {
		util.Logger.Error("feishu: lark client not initialized, check AppID/AppSecret")
		return fmt.Errorf("feishu: lark client not initialized")
	}

	receiveID, receiveIDType := fs.resolveReceiveID(msg)
	util.Logger.Info("feishu: resolved receive ID",
		slog.String("receive_id", receiveID),
		slog.String("receive_id_type", receiveIDType),
		slog.String("original_conversation_id", msg.ConversationID),
		slog.String("original_user_id", msg.PlatformUserID))

	maxLen := fs.cfg.MaxMessageLength
	if maxLen <= 0 {
		maxLen = defaultMaxMessageLength
	}
	content = sanitizeContent(content, maxLen)
	util.Logger.Debug("feishu: content sanitized",
		slog.Int("final_length", len(content)),
		slog.String("content_preview", previewContent(content)))

	textContent := string(mustMarshal(map[string]string{
		"text": content,
	}))

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(receiveID).
			MsgType("text").
			Content(textContent).
			Build()).
		Build()

	util.Logger.Info("feishu: calling Lark SDK Im.V1.Message.Create",
		slog.String("receive_id", receiveID),
		slog.String("receive_id_type", receiveIDType))

	resp, err := fs.larkClient.Im.V1.Message.Create(ctx, req)
	if err != nil {
		util.Logger.Error("feishu: Lark SDK API call failed",
			slog.String("receive_id", receiveID),
			slog.String("error", err.Error()))
		return fmt.Errorf("feishu: send message: %w", err)
	}

	if !resp.Success() {
		util.Logger.Error("feishu: Lark SDK API returned error",
			slog.String("receive_id", receiveID),
			slog.Int("code", resp.Code),
			slog.String("msg", resp.Msg))
		return fmt.Errorf(
			"feishu: send message API error: code=%d, msg=%s",
			resp.Code, resp.Msg)
	}

	if resp.Data != nil && resp.Data.MessageId != nil {
		util.Logger.Info("feishu: message sent successfully",
			slog.String("message_id", *resp.Data.MessageId),
			slog.String("receive_id", receiveID))
	} else {
		util.Logger.Warn("feishu: message sent but no message_id returned",
			slog.String("receive_id", receiveID))
	}

	return nil
}

// previewContent returns a truncated preview of content for logging.
func previewContent(content string) string {
	if len(content) <= 100 {
		return content
	}
	return content[:100] + "..."
}

// createCardMessage creates an interactive card message via the Lark
// SDK and returns the message_id for subsequent patches.
func (fs *FeishuSender) createCardMessage(
	ctx context.Context,
	receiveID string,
	receiveIDType string,
	cardJSON string,
) (string, error) {
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(receiveID).
			MsgType("interactive").
			Content(cardJSON).
			Build()).
		Build()

	resp, err := fs.larkClient.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf(
			"feishu: create card: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf(
			"feishu: create card API error: code=%d, msg=%s",
			resp.Code, resp.Msg)
	}
	if resp.Data == nil || resp.Data.MessageId == nil {
		return "", fmt.Errorf(
			"feishu: create card returned no message_id")
	}

	messageID := *resp.Data.MessageId
	util.Logger.Debug("feishu: card created",
		slog.String("message_id", messageID))

	return messageID, nil
}

// patchCardMessage updates an existing card message with new content
// via the Lark SDK's im/v1/messages/:message_id PATCH API.
func (fs *FeishuSender) patchCardMessage(
	ctx context.Context,
	messageID string,
	cardJSON string,
) error {
	req := larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(cardJSON).
			Build()).
		Build()

	resp, err := fs.larkClient.Im.V1.Message.Patch(ctx, req)
	if err != nil {
		return fmt.Errorf("feishu: patch card: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf(
			"feishu: patch card API error: code=%d, msg=%s",
			resp.Code, resp.Msg)
	}

	return nil
}

// sendResponseURLReply sends a reply to a Feishu response_url. The
// response_url is a one-time URL provided in certain callback types
// (e.g., card actions). For standard message events, this is rarely
// available. This uses direct HTTP since response_url is a
// separate Feishu endpoint not managed by the Open API.
func (fs *FeishuSender) sendResponseURLReply(
	ctx context.Context,
	responseURL string,
	content string,
) error {
	if responseURL == "" || content == "" {
		return nil
	}

	reqBody := map[string]any{
		"msg_type": "text",
		"content": map[string]string{
			"text": content,
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf(
			"feishu: marshal response_url reply: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, responseURL,
		bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf(
			"feishu: create response_url request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := fs.respURLClient.Do(req)
	if err != nil {
		return fmt.Errorf(
			"feishu: send response_url reply: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf(
			"feishu: read response_url response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf(
			"feishu: response_url reply failed: status=%d, body=%s",
			resp.StatusCode, string(respBody))
	}

	// Parse API response to check code.
	var apiResp FeishuAPIResponse
	if err := json.Unmarshal(respBody, &apiResp); err == nil {
		if apiResp.Code != 0 {
			return fmt.Errorf(
				"feishu: response_url API error: code=%d, msg=%s",
				apiResp.Code, apiResp.Msg)
		}
	}

	util.Logger.Debug("feishu: response_url reply sent")
	return nil
}

// buildCardJSON builds a Feishu interactive card JSON string for
// displaying the agent's response.
//
// Card structure:
//   - header: Shows agent name and status
//   - elements: Markdown content with the response text
func (fs *FeishuSender) buildCardJSON(
	content string,
	isFinal bool,
) string {
	maxLen := fs.cfg.MaxMessageLength
	if maxLen <= 0 {
		maxLen = defaultMaxMessageLength
	}
	displayContent := sanitizeContent(content, maxLen)

	headerTitle := "Wukong AI 🤔"
	if isFinal {
		headerTitle = "Wukong AI ✅"
	}

	card := map[string]any{
		"config": map[string]any{
			"wide_screen_mode": true,
			"enable_forward":   true,
		},
		"header": map[string]any{
			"title": map[string]any{
				"tag":     "plain_text",
				"content": headerTitle,
			},
			"template": "blue",
		},
		"elements": []map[string]any{
			{
				"tag":     "markdown",
				"content": displayContent,
			},
		},
	}

	if !isFinal {
		card["elements"] = append(
			card["elements"].([]map[string]any),
			map[string]any{
				"tag": "note",
				"elements": []map[string]any{
					{
						"tag":     "plain_text",
						"content": "⏳ 正在生成回复...",
					},
				},
			},
		)
	}

	return string(mustMarshal(card))
}

// sanitizeContent ensures content fits within Feishu limits,
// trimming excess whitespace and truncating if necessary.
func sanitizeContent(content string, maxLen int) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= maxLen {
		return content
	}
	return strings.TrimSpace(string(runes[:maxLen])) + "..."
}
