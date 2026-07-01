package feishu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// FeishuEvent is the top-level event structure for Feishu/Lark event
// subscription callbacks.
type FeishuEvent struct {
	Schema string       `json:"schema"`
	Header *EventHeader `json:"header"`
	Type   string       `json:"type"`
	Event  *EventBody   `json:"event"`
	// Challenge is only present in URL verification requests.
	Challenge string `json:"challenge,omitempty"`
	Token     string `json:"token,omitempty"`
}

// EventHeader contains event metadata.
type EventHeader struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	CreateTime string `json:"create_time"`
	Token      string `json:"token"`
	AppID      string `json:"app_id"`
	TenantKey  string `json:"tenant_key"`
}

// EventBody contains the actual event payload.
type EventBody struct {
	Sender    *EventSender `json:"sender"`
	MessageID string       `json:"message_id"`
	ChatID    string       `json:"chat_id,omitempty"`
	ChatType  string       `json:"chat_type,omitempty"`
	Type      string       `json:"type,omitempty"`
	MsgType   string       `json:"msg_type,omitempty"`
	Text      string       `json:"text,omitempty"`
	Content   string       `json:"content,omitempty"`
}

// EventSender identifies the user who sent the message.
type EventSender struct {
	SenderID *SenderID `json:"sender_id"`
}

// SenderID holds user identification fields.
type SenderID struct {
	UnionID string `json:"union_id"`
	UserID  string `json:"user_id"`
	OpenID  string `json:"open_id"`
}

// FeishuTextContent is the parsed structure of Feishu message content.
// Content is a JSON string containing the text.
type FeishuTextContent struct {
	Text string `json:"text"`
}

// FeishuReplyMessage is the response format for passive message
// replies (returned directly in the HTTP response to the callback).
type FeishuReplyMessage struct {
	Content *FeishuReplyContent `json:"content,omitempty"`
	MsgType string              `json:"msg_type,omitempty"`
}

// FeishuReplyContent is the content portion of a reply message.
type FeishuReplyContent struct {
	Text string `json:"text,omitempty"`
}

// FeishuAPIResponse is the standard Feishu API response format.
type FeishuAPIResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data,omitempty"`
}

// extractTextContent parses a Feishu message content JSON string and
// returns the text field.
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

// readBody reads the full request body and resets it for subsequent
// reads.
func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// mustMarshal marshals a value to JSON, returning an empty array on
// failure.
func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return data
}

// truncateText truncates text to the specified max length, appending
// "..." if truncated.
func truncateText(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return strings.TrimSpace(string(runes[:maxLen])) + "..."
}
