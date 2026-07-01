// Package gateway provides multi-platform messaging channel support
// for Wukong. It defines the standard Channel interface that all
// external IM platform adapters (Feishu, WeCom, Slack, etc.) must
// implement, along with the unified GatewayMessage type used
// internally for routing and processing.
package gateway

import (
	"context"
	"encoding/json"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// Channel is the standard interface for all messaging platform
// adapters. Each platform (Feishu, WeCom, Slack, etc.) registers its
// own implementation with the ChannelRouter.
//
// To add a new platform:
//  1. Implement all 7 methods of this interface
//  2. Register with ChannelRouter.Register()
//  3. Add platform config to config.yaml under gateway section
type Channel interface {
	// Name returns the unique channel identifier
	// (e.g., "feishu", "wecom").
	Name() string

	// RoutePath returns the HTTP callback path prefix for this
	// channel. The actual callback endpoint will be
	// RoutePath() + "/callback".
	// Example: "/feishu" registers "/feishu/callback".
	RoutePath() string

	// VerifyRequest validates the authenticity of an incoming
	// HTTP request from the platform. Each platform has its own
	// verification mechanism:
	//   - Feishu: HMAC-SHA256 signature header
	//   - WeCom: SHA1 msg_signature + AES decrypt
	//   - Slack: Signing Secret verification
	//
	// Returns the verified/decrypted request body, or an error
	// if verification fails.
	VerifyRequest(r *http.Request) ([]byte, error)

	// ParseMessage converts platform-specific raw message data
	// into a unified GatewayMessage.
	ParseMessage(body []byte) (*GatewayMessage, error)

	// BuildUserID constructs a Wukong user identifier from the
	// platform message. This is used to isolate sessions and
	// memories per user.
	// Typical format: platformName + ":" + platformUserID.
	BuildUserID(msg *GatewayMessage) string

	// BuildSessionID constructs a Wukong session identifier from
	// the platform message. Sessions are per-conversation or
	// per-group-chat depending on the platform.
	// Typical format: platformName + "-" + conversationID.
	BuildSessionID(msg *GatewayMessage) string

	// SendReply sends the agent's response back to the platform.
	// The events channel emits agent events (streaming content,
	// tool calls, completion) that the channel should format
	// appropriately for its platform.
	//
	// Implementation notes:
	//   - For simple text replies, collect all streaming content
	//     and send as a single message.
	//   - For streaming-capable platforms, update the message
	//     incrementally.
	//   - ctx carries a timeout for the overall reply operation.
	SendReply(ctx context.Context, msg *GatewayMessage,
		events <-chan *event.Event) error

	// HandlePlatformEvent processes platform-specific events
	// that are not regular messages (e.g., URL verification,
	// card actions, app open events).
	//
	// Implementations should return the raw HTTP response body
	// for events that require an immediate response
	// (e.g., URL challenge).
	//
	// For events that don't need a special response, return nil
	// and nil to let the gateway proceed with normal processing.
	HandlePlatformEvent(w http.ResponseWriter,
		evt *PlatformEvent) ([]byte, error)
}

// GatewayMessage is the unified internal representation of a message
// from any platform. Each Channel parses its platform-specific format
// into this common type.
type GatewayMessage struct {
	// Platform is the platform identifier (e.g., "feishu", "wecom").
	Platform string `json:"-"`

	// PlatformUserID is the user ID on the source platform
	// (Feishu open_id, WeCom userid, Slack user ID).
	PlatformUserID string `json:"-"`

	// ConversationID is the conversation/chat/group ID on the
	// source platform.
	ConversationID string `json:"-"`

	// Content is the plain text content of the user's message.
	Content string `json:"-"`

	// ContentType describes the message format:
	// "text", "image", "file", "mixed".
	ContentType string `json:"-"`

	// ResponseURL is an optional URL provided by the platform for
	// sending proactive replies (used for streaming messages).
	ResponseURL string `json:"-"`

	// MessageID is the platform's unique message identifier,
	// used for deduplication.
	MessageID string `json:"-"`

	// Timestamp is the message creation time on the platform.
	Timestamp int64 `json:"-"`

	// RawData holds the original platform-specific message data
	// for Channel-internal use during response construction.
	RawData json.RawMessage `json:"-"`
}

// PlatformEvent represents a non-message platform event such as URL
// verification challenges, card interactions, or app lifecycle events.
type PlatformEvent struct {
	// Platform is the platform identifier (e.g., "feishu", "wecom").
	Platform string

	// Type identifies the event kind:
	//   - "url_verify": URL verification challenge
	//   - "card_action": Interactive card button click
	//   - "app_open": App opened event
	//   - "event_callback": Generic event callback
	Type string

	// Data contains the event payload in platform-specific format.
	Data json.RawMessage

	// Metadata holds additional platform-specific key-value pairs.
	Metadata map[string]string
}
