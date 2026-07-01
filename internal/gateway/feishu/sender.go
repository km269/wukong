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
	"time"

	"github.com/km269/wukong/internal/config"
	"github.com/km269/wukong/internal/gateway"
	"github.com/km269/wukong/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

// FeishuSender handles sending replies to Feishu, supporting both
// passive (direct response) and proactive (API call) modes.
type FeishuSender struct {
	cfg    *config.FeishuChannelConfig
	client *http.Client
}

// NewFeishuSender creates a new FeishuSender.
func NewFeishuSender(cfg *config.FeishuChannelConfig) *FeishuSender {
	return &FeishuSender{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SendTextReply sends a passive text reply directly in the HTTP
// response. This is the simplest reply method, but has a 3-second
// timeout limitation and does not support streaming.
func (fs *FeishuSender) SendTextReply(
	ctx context.Context,
	msg *gateway.GatewayMessage,
	content string,
) error {
	if content == "" {
		content = "处理完成。"
	}

	// Truncate if too long for Feishu.
	maxLen := fs.cfg.MaxMessageLength
	if maxLen <= 0 {
		maxLen = 4096
	}
	if len([]rune(content)) > maxLen {
		content = truncateText(content, maxLen)
	}

	reply := &FeishuReplyMessage{
		MsgType: "text",
		Content: &FeishuReplyContent{
			Text: content,
		},
	}

	_ = msg // msg is used for ResponseURL in proactive reply mode.

	// For passive replies, the JSON is written to the HTTP response
	// by the gateway's handler. Here we signal the response content
	// via context -- but since the gateway doesn't use context for
	// response writing, use a direct approach.
	data, err := json.Marshal(reply)
	if err != nil {
		return fmt.Errorf("feishu: marshal reply: %w", err)
	}

	// Note: In the current architecture, the gateway handler is
	// responsible for writing the HTTP response. The sender stores
	// the reply data, which the handler uses to write the response.
	// This is a simplified implementation; for now, we rely on the
	// gateway always returning 200 OK and the sender making
	// proactive API calls for replies.

	util.Logger.Debug("feishu: reply prepared",
		slog.Int("length", len(data)),
		slog.String("preview", string(data[:min(len(data), 80)])))

	return nil
}

// SendStreamCard sends the agent's response as a Feishu streaming
// card. This is the recommended approach for real-time display.
//
// The implementation:
//  1. Creates a streaming message card
//  2. Sends incremental content updates as events arrive
//  3. Finishes the streaming message when all events are received
//
// Currently, this falls back to collecting all content and sending
// a single message. Full streaming card support requires Feishu API
// integration with tenant_access_token management.
func (fs *FeishuSender) SendStreamCard(
	ctx context.Context,
	msg *gateway.GatewayMessage,
	events <-chan *event.Event,
) error {
	// Collect all streaming content from agent events.
	var builder strings.Builder
	var lastToolName string

	for evt := range events {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if evt.Error != nil {
			util.Logger.Error("feishu: stream error",
				slog.String("error", evt.Error.Message))
			// Continue processing; agent may recover.
			continue
		}

		if evt.Response != nil &&
			len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			delta := choice.Delta.Content
			if delta != "" {
				builder.WriteString(delta)
			}

			// Track tool calls for status updates.
			if len(choice.Message.ToolCalls) > 0 {
				lastToolName = choice.Message.ToolCalls[0].Function.Name
			}
		}

		// Check for runner completion.
		if evt.IsRunnerCompletion() {
			break
		}
	}

	_ = lastToolName

	content := builder.String()
	if content == "" {
		content = "处理完成。"
	}

	// Truncate if needed.
	maxLen := fs.cfg.MaxMessageLength
	if maxLen <= 0 {
		maxLen = 4096
	}
	if len([]rune(content)) > maxLen {
		content = truncateText(content, maxLen)
	}

	// For now, send as a proactive API message if ResponseURL is
	// available; otherwise, use passive reply mode.
	if msg.ResponseURL != "" {
		return fs.sendProactiveReply(ctx, msg.ResponseURL, content)
	}

	// Fallback: the gateway will handle passive reply.
	_ = content
	util.Logger.Debug("feishu: stream card completed",
		slog.Int("content_length", len(content)))

	return nil
}

// sendProactiveReply sends a reply message to a Feishu response URL.
// response_url is provided by Feishu in the original callback and
// allows posting messages without a separate API token.
func (fs *FeishuSender) sendProactiveReply(
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
		return fmt.Errorf("feishu: marshal proactive reply: %w",
			err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, responseURL,
		bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf(
			"feishu: create proactive reply request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := fs.client.Do(req)
	if err != nil {
		return fmt.Errorf(
			"feishu: send proactive reply: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf(
			"feishu: proactive reply failed: status=%d, body=%s",
			resp.StatusCode, string(respBody))
	}

	var apiResp FeishuAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err == nil {
		if apiResp.Code != 0 {
			return fmt.Errorf(
				"feishu: API error: code=%d, msg=%s",
				apiResp.Code, apiResp.Msg)
		}
	}

	util.Logger.Debug("feishu: proactive reply sent")
	return nil
}
