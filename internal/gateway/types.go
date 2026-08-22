// Package gateway provides multi-platform messaging channel support
// for Wukong. It defines the standard Channel interface that all
// external IM platform adapters (Feishu, etc.) implement, along with
// the unified GatewayMessage type used internally for routing and
// processing.
//
// The gateway is transport-agnostic: each Channel owns its own inbound
// transport (e.g. a platform WebSocket long-connection client) and
// pushes normalized GatewayMessage values through a MessageHandler
// supplied by the GatewayServer. The gateway handles the shared
// cross-cutting concerns (deduplication, rate limiting, session
// mapping, agent execution, reply dispatch) that are independent of
// how a message was received.
package gateway

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// MessageHandler is supplied by the GatewayServer and invoked by a
// Channel for every inbound platform message. Implementations must
// return quickly (the agent run executes asynchronously inside the
// gateway); the return value is used only for logging/acknowledgement
// back to the platform SDK.
type MessageHandler func(ctx context.Context, msg *GatewayMessage)

// Channel is the standard interface for all messaging platform
// adapters. Each platform (Feishu, etc.) registers its own
// implementation with the GatewayServer.
//
// Unlike the previous HTTP-webhook design, this interface is
// transport-agnostic: a Channel is responsible for establishing and
// maintaining its own inbound connection (typically an outbound
// WebSocket long-connection to the platform) and for parsing the
// platform-specific event format into a unified GatewayMessage. The
// gateway then drives the shared processing pipeline.
//
// To add a new platform:
//  1. Implement this interface (establishing whatever transport the
//     platform requires inside Start).
//  2. Register with GatewayServer.RegisterChannel().
//  3. Add platform config to config.yaml under the gateway section.
type Channel interface {
	// Name returns the unique channel identifier
	// (e.g., "feishu").
	Name() string

	// Start establishes the platform connection and begins receiving
	// messages. For each inbound message it must invoke handle with a
	// populated *GatewayMessage. The call blocks until ctx is cancelled
	// or a fatal connection error occurs; the platform SDK is expected
	// to handle reconnection/heartbeat internally.
	//
	// handle is the gateway-supplied MessageHandler and runs the shared
	// pipeline (dedup → rate limit → session → agent → reply). It is
	// safe for Start to call handle synchronously: the gateway dispatch
	// layer runs the agent asynchronously.
	Start(ctx context.Context, handle MessageHandler) error

	// Stop gracefully closes the platform connection and releases any
	// resources (e.g. HTTP clients, file handles). It is invoked by the
	// gateway during shutdown.
	Stop(ctx context.Context) error

	// BuildUserID constructs a Wukong user identifier from the
	// platform message. This is used to isolate sessions and
	// memories per user.
	// Typical format: platformName + ":" + platformUserID.
	BuildUserID(msg *GatewayMessage) string

	// BuildSessionID constructs a Wukong session identifier from the
	// platform message. Sessions are per-conversation or per-group-chat
	// depending on the platform.
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
}

// GatewayMessage is the unified internal representation of a message
// from any platform. Each Channel parses its platform-specific format
// into this common type.
//
// All fields are tagged `json:"-"` because this type is never
// serialized over the wire — it is a purely internal value passed
// between the Channel and the gateway pipeline. It is therefore
// independent of the inbound transport (HTTP webhook or WebSocket).
type GatewayMessage struct {
	// Platform is the platform identifier (e.g., "feishu").
	Platform string `json:"-"`

	// PlatformUserID is the user ID on the source platform
	// (Feishu open_id, etc.).
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
	// sending proactive replies (used for streaming messages on
	// platforms that support it).
	ResponseURL string `json:"-"`

	// MessageID is the platform's unique message identifier,
	// used for deduplication.
	MessageID string `json:"-"`

	// Timestamp is the message creation time on the platform.
	Timestamp int64 `json:"-"`

	// RawData holds the original platform-specific message data
	// for Channel-internal use during response construction.
	RawData []byte `json:"-"`
}
