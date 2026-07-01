package wecom

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/km269/wukong/internal/gateway"
)

// =========================================================================
// AI Bot JSON callback message types (recommended mode).
// =========================================================================

// WeComAIBotCallback is the top-level structure for WeCom AI Bot
// message callbacks. The JSON body is posted by WeCom servers.
type WeComAIBotCallback struct {
	MsgID    string          `json:"msgid"`
	AIBotID  string          `json:"aibotid"`
	ChatID   string          `json:"chatid"`
	ChatType string          `json:"chattype"`
	From     WeComFrom       `json:"from"`
	MsgType  string          `json:"msgtype"`
	Text     *WeComText      `json:"text,omitempty"`
	Image    *WeComImage     `json:"image,omitempty"`
	Stream   *WeComStreamRef `json:"stream,omitempty"`
}

// WeComFrom identifies the sender.
type WeComFrom struct {
	UserID string `json:"userid"`
}

// WeComText is the text message content.
type WeComText struct {
	Content string `json:"content"`
}

// WeComImage is the image message content.
type WeComImage struct {
	URL string `json:"url"`
}

// WeComStreamRef references a streaming message.
type WeComStreamRef struct {
	ID string `json:"id"`
}

// =========================================================================
// JSON reply message types (AI Bot mode).
// =========================================================================

// WeComReply is the passive JSON reply format for AI Bot callbacks.
type WeComReply struct {
	MsgType      string           `json:"msgtype"`
	Text         *WeComReplyText  `json:"text,omitempty"`
	Stream       *WeComStreamBody `json:"stream,omitempty"`
	Markdown     *WeComReplyMd    `json:"markdown,omitempty"`
	TemplateCard *WeComCard       `json:"template_card,omitempty"`
}

// WeComReplyText is the text reply content.
type WeComReplyText struct {
	Content string `json:"content"`
}

// WeComReplyMd is the markdown reply content.
type WeComReplyMd struct {
	Content string `json:"content"`
}

// WeComStreamBody is the streaming message reply body.
type WeComStreamBody struct {
	ID      string            `json:"id"`
	Content string            `json:"content,omitempty"`
	Finish  bool              `json:"finish"`
	MsgItem []WeComStreamItem `json:"msg_item,omitempty"`
}

// WeComStreamItem is a single item in a streaming message feed.
type WeComStreamItem struct {
	MsgType  string          `json:"msgtype"`
	Text     *WeComReplyText `json:"text,omitempty"`
	Markdown *WeComReplyMd   `json:"markdown,omitempty"`
}

// WeComCard is a template card (only for non-AI-Bot enterprise apps).
type WeComCard struct {
	CardType string `json:"card_type"`
	// MainTitle, SubTitleText, etc. can be added as needed.
}

// =========================================================================
// Encrypted XML callback message types (enterprise app mode).
// =========================================================================

// WeComXMLMessage is the encrypted XML body posted by WeCom for
// enterprise application callbacks.
type WeComXMLMessage struct {
	XMLName    xml.Name `xml:"xml"`
	ToUserName string   `xml:"ToUserName"`
	AgentID    string   `xml:"AgentID"`
	Encrypt    string   `xml:"Encrypt"`
}

// WeComPlainMessage is the decrypted inner XML of a WeCom callback.
type WeComPlainMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgID        string   `xml:"MsgId"`
	AgentID      int      `xml:"AgentID"`
}

// WeComEncryptedReply is the encrypted XML reply for enterprise app
// passive response.
type WeComEncryptedReply struct {
	XMLName      xml.Name `xml:"xml"`
	Encrypt      string   `xml:"Encrypt"`
	MsgSignature string   `xml:"MsgSignature"`
	TimeStamp    string   `xml:"TimeStamp"`
	Nonce        string   `xml:"Nonce"`
}

// =========================================================================
// Conversion functions.
// =========================================================================

// parseAIBotMessage converts an AI Bot JSON callback into a unified
// GatewayMessage.
func parseAIBotMessage(body []byte) (*gateway.GatewayMessage, error) {
	var cb WeComAIBotCallback
	// The AI Bot callback uses JSON format.
	if err := json.Unmarshal(body, &cb); err != nil {
		return nil, fmt.Errorf("wecom: parse ai bot callback: %w", err)
	}

	msg := &gateway.GatewayMessage{
		PlatformUserID: cb.From.UserID,
		ConversationID: cb.ChatID,
		MessageID:      cb.MsgID,
		ContentType:    cb.MsgType,
	}

	switch cb.MsgType {
	case "text":
		if cb.Text != nil {
			msg.Content = strings.TrimSpace(cb.Text.Content)
		}
	case "image":
		content := "[收到图片]"
		if cb.Image != nil && cb.Image.URL != "" {
			content = fmt.Sprintf("[图片: %s]", cb.Image.URL)
		}
		msg.Content = content
	case "stream":
		// Streaming message ref; content is in stream body.
		msg.Content = "[流式消息]"
	default:
		msg.Content = fmt.Sprintf(
			"[收到消息类型: %s]", cb.MsgType)
	}

	return msg, nil
}

// parsePlainXMLMessage converts a decrypted plaintext XML message
// into a unified GatewayMessage.
func parsePlainXMLMessage(xmlData []byte) (*gateway.GatewayMessage, error) {
	var plain WeComPlainMessage
	if err := xml.Unmarshal(xmlData, &plain); err != nil {
		return nil, fmt.Errorf("wecom: parse plain xml: %w", err)
	}

	msg := &gateway.GatewayMessage{
		PlatformUserID: plain.FromUserName,
		ConversationID: plain.ToUserName,
		MessageID:      plain.MsgID,
		ContentType:    plain.MsgType,
	}

	switch plain.MsgType {
	case "text":
		msg.Content = strings.TrimSpace(plain.Content)
	default:
		msg.Content = fmt.Sprintf(
			"[收到消息类型: %s]", plain.MsgType)
	}

	return msg, nil
}

// readBody reads and resets the request body for multiple reads.
func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("wecom: read body: %w", err)
	}
	// r.Body is consumed; for WeCom, we typically only read once
	// since the gateway re-reads via VerifyRequest.
	return body, nil
}

// truncateText truncates text to maxLen characters, appending "..."
// if truncated.
func truncateText(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return strings.TrimSpace(string(runes[:maxLen])) + "..."
}
