package feishu

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/km269/wukong/internal/gateway"
	"github.com/km269/wukong/internal/util"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// FeishuTextContent is the parsed structure of Feishu message content.
// Content is a JSON string containing the text.
type FeishuTextContent struct {
	Text string `json:"text"`
}

// FeishuAPIResponse is the standard Feishu API response format.
type FeishuAPIResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data,omitempty"`
}

// mentionPlaceholderRe matches Feishu's in-text mention placeholders
// such as "@_user_1" that the platform injects when a user @-mentions
// someone (including the bot) in a group. They are stripped from the
// text before it reaches the agent so the agent sees clean input.
var mentionPlaceholderRe = regexp.MustCompile(`@_user_\d+\s*`)

// parseP2MessageReceiveV1 converts a typed Feishu
// im.message.receive_v1 event (delivered over the WebSocket
// long-connection) into a unified *gateway.GatewayMessage.
//
// Returns nil for messages that should be silently ignored (e.g.
// non-user senders). In all other cases a placeholder content is
// produced for unsupported types so the user still gets a coherent
// reply from the agent.
func (fc *FeishuChannel) parseP2MessageReceiveV1(
	evt *larkim.P2MessageReceiveV1,
) *gateway.GatewayMessage {
	if evt == nil || evt.Event == nil {
		return nil
	}
	data := evt.Event

	gm := &gateway.GatewayMessage{
		RawData: mustMarshal(evt),
	}

	// Message identity + conversation. The SDK exposes these as
	// *string pointers on *larkim.EventMessage.
	if msg := data.Message; msg != nil {
		gm.MessageID = strPtr(msg.MessageId)
		gm.ConversationID = strPtr(msg.ChatId)
		gm.ContentType = strPtr(msg.MessageType)

		switch gm.ContentType {
		case "text":
			gm.Content = cleanTextContent(
				extractTextContent(strPtr(msg.Content)))

		case "image":
			gm.Content = fmt.Sprintf("[图片: %s]", gm.MessageID)

		case "file":
			if fc.cfg.EnableFileReceive {
				gm.Content = fmt.Sprintf("[文件: %s]", gm.MessageID)
			} else {
				gm.Content = "[文件消息暂不支持]"
			}

		case "audio":
			gm.Content = "[语音消息]"

		case "media":
			gm.Content = "[富媒体消息]"

		default:
			if gm.ContentType == "" {
				gm.ContentType = "unknown"
			}
			gm.Content = fmt.Sprintf("[收到消息类型: %s]", gm.ContentType)
		}
	}

	// Sender identity.
	if data.Sender != nil {
		if data.Sender.SenderType != nil &&
			*data.Sender.SenderType != "" &&
			*data.Sender.SenderType != "user" {
			// Ignore messages not sent by a real user (e.g. other bots).
			util.Logger.Debug("feishu: ignoring non-user sender",
				slog.String("sender_type", *data.Sender.SenderType))
			return nil
		}
		if data.Sender.SenderId != nil {
			gm.PlatformUserID = strPtr(data.Sender.SenderId.OpenId)
		}
	}

	return gm
}

// extractTextContent parses a Feishu text message content JSON string
// and returns the text field.
//
// Feishu text message content format:
//
//	{"text": "hello world"}
func extractTextContent(content string) string {
	if content == "" {
		return ""
	}

	// The content could be a JSON string or a raw string.
	var tc FeishuTextContent
	if err := json.Unmarshal([]byte(content), &tc); err == nil {
		return tc.Text
	}

	// Fallback: treat the whole content as text.
	return content
}

// cleanTextContent trims whitespace and strips Feishu @-mention
// placeholders ("@_user_N") so the agent receives clean user input.
// In a group, when a user @-mentions the bot, Feishu prefixes the text
// with such a placeholder; without cleaning the agent would see it as
// part of the prompt.
func cleanTextContent(s string) string {
	s = mentionPlaceholderRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// mustMarshal marshals a value to JSON, returning an empty object on
// failure.
func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}

// truncateText truncates text to the specified max length, appending
// "..." if truncated.
//
// Used by external code that relies on this utility.
func truncateText(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return strings.TrimSpace(string(runes[:maxLen])) + "..."
}

// strPtr safely dereferences a *string, returning "" for nil.
func strPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
