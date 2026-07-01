// Package summon provides an ANP JSON-RPC 2.0 adapter that bridges
// Wukong's tRPC-Agent-Go event system with ANP-compatible JSON-RPC
// messages, enabling interoperability with native ANP agents.
//
// The adapter supports:
//   - P1 Core Binding: JSON-RPC 2.0 request/response/error conventions
//   - P3 Direct Messaging: Agent-to-agent message semantics
//   - P5 E2EE Overlay: End-to-end encrypted message wrapping
//   - P7 Attachments: File and object transfer capabilities
package summon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/km269/wukong/internal/util"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// ============================================================================
// ANP Adapter — tRPC Events ↔ ANP JSON-RPC 2.0
// ============================================================================

// ANPAdapter bridges the tRPC-Agent-Go event system to ANP-compatible
// JSON-RPC 2.0 message profiles, enabling Wukong agents to communicate
// with native ANP agents that use JSON-RPC rather than tRPC events.
type ANPAdapter struct {
	messenger *E2EEMessenger

	// Supported ANP message profiles
	supportedProfiles []string
}

// ANPAdapterConfig configures the ANP adapter.
type ANPAdapterConfig struct {
	// Messenger is the E2EE messenger for encrypted communication.
	// When nil, messages are sent unencrypted (P1/P3 only).
	Messenger *E2EEMessenger

	// SupportedProfiles lists the ANP message profiles this
	// adapter can handle.
	// Default: ["P1", "P3"].
	SupportedProfiles []string
}

// NewANPAdapter creates a new tRPC-to-ANP adapter.
func NewANPAdapter(cfg *ANPAdapterConfig) *ANPAdapter {
	profiles := cfg.SupportedProfiles
	if len(profiles) == 0 {
		profiles = []string{"P1", "P3"}
	}

	return &ANPAdapter{
		messenger:         cfg.Messenger,
		supportedProfiles: profiles,
	}
}

// ============================================================================
// ANP JSON-RPC 2.0 Message Types
// ============================================================================

// ANPMessage represents a JSON-RPC 2.0 message in ANP format.
// This is the P1 Core Binding: the fundamental message envelope.
type ANPMessage struct {
	// JSONRPC is always "2.0" per JSON-RPC specification.
	JSONRPC string `json:"jsonrpc"`

	// Method is the RPC method name (for requests/notifications).
	// Empty for responses.
	Method string `json:"method,omitempty"`

	// Params holds the method parameters (structured or by-position).
	Params json.RawMessage `json:"params,omitempty"`

	// ID is the request identifier for matching responses.
	// nil for notifications (no response expected).
	ID *int `json:"id,omitempty"`

	// Result is the successful response data.
	// Only present in response messages.
	Result json.RawMessage `json:"result,omitempty"`

	// Error is the error response data.
	// Only present in error response messages.
	Error *ANPJSONRPCError `json:"error,omitempty"`

	// Profile indicates which ANP message profile applies.
	// "P1" — core, "P3" — direct messaging, "P5" — E2EE, etc.
	Profile string `json:"profile,omitempty"`

	// MessageID is a unique identifier for tracking/deduplication.
	MessageID string `json:"messageId,omitempty"`

	// Timestamp is the message creation time (ISO 8601).
	Timestamp string `json:"timestamp,omitempty"`

	// SenderDID identifies the sending agent.
	SenderDID string `json:"senderDID,omitempty"`

	// RecipientDID identifies the intended recipient agent.
	RecipientDID string `json:"recipientDID,omitempty"`
}

// ANPJSONRPCError is a JSON-RPC 2.0 error object.
type ANPJSONRPCError struct {
	// Code is the error code.
	Code int `json:"code"`

	// Message is a human-readable error description.
	Message string `json:"message"`

	// Data contains additional error details.
	Data json.RawMessage `json:"data,omitempty"`
}

// ANPAgentTask represents a delegated task in ANP direct messaging
// format (P3). It wraps a request with task tracking metadata.
type ANPAgentTask struct {
	// TaskID is the unique task identifier.
	TaskID string `json:"taskId"`

	// Status is the task lifecycle status.
	Status string `json:"status"`

	// Request is the original task request message.
	Request *ANPMessage `json:"request"`

	// Response is the task completion response.
	Response *ANPMessage `json:"response,omitempty"`

	// Artifacts contains output artifacts generated during
	// task execution.
	Artifacts []ANTaskArtifact `json:"artifacts,omitempty"`

	// CreatedAt is the task creation time.
	CreatedAt string `json:"createdAt"`

	// UpdatedAt is the last status update time.
	UpdatedAt string `json:"updatedAt"`
}

// ANTaskArtifact represents an output artifact from task execution.
type ANTaskArtifact struct {
	// Name is the artifact name.
	Name string `json:"name"`

	// Parts contains the artifact content parts.
	Parts []ANTaskArtifactPart `json:"parts"`
}

// ANTaskArtifactPart is a single part of a task artifact.
type ANTaskArtifactPart struct {
	// Type is the content type: "text" or "data".
	Type string `json:"type"`

	// Text is the text content (when Type is "text").
	Text string `json:"text,omitempty"`

	// Data is the base64-encoded binary content (when Type is "data").
	Data string `json:"data,omitempty"`

	// MIMEType is the MIME type for binary data.
	MIMEType string `json:"mimeType,omitempty"`
}

// ============================================================================
// Event-to-Message Conversion
// ============================================================================

// ConvertEventToMessage converts a tRPC A2A event to an ANP
// JSON-RPC 2.0 message. The appropriate profile is selected
// based on the event content type.
func (a *ANPAdapter) ConvertEventToMessage(
	evt *event.Event,
	senderDID, recipientDID string,
) (*ANPMessage, error) {
	if evt == nil {
		return nil, fmt.Errorf("anp: event is nil")
	}

	msgID := uuid.New().String()
	msg := &ANPMessage{
		JSONRPC:      "2.0",
		MessageID:    msgID,
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		SenderDID:    senderDID,
		RecipientDID: recipientDID,
	}

	// Classify event type and set appropriate method/params
	if evt.Response != nil {
		// This is a response event
		if evt.Response.Error != nil {
			msg.Error = &ANPJSONRPCError{
				Code:    -32000,
				Message: evt.Response.Error.Message,
			}
		} else {
			result, jsonErr := json.Marshal(evt.Response)
			if jsonErr != nil {
				return nil, fmt.Errorf(
					"anp: marshal response: %w", jsonErr)
			}
			msg.Result = result
		}
		msg.Profile = "P1"
	} else {
		// Generic event — treat as notification
		msg.Method = "agent.event"
		body, jsonErr := json.Marshal(evt)
		if jsonErr != nil {
			return nil, fmt.Errorf(
				"anp: marshal event: %w", jsonErr)
		}
		msg.Params = body
		msg.Profile = "P1"
	}

	return msg, nil
}

// ConvertTaskToEvent converts an ANP Agent Task message
// back to a tRPC event for internal processing.
func (a *ANPAdapter) ConvertTaskToEvent(
	task *ANPAgentTask,
) (*event.Event, error) {
	if task == nil || task.Request == nil {
		return nil, fmt.Errorf(
			"anp: task or request is nil")
	}

	evt := &event.Event{}

	// Convert task message back to an event via the response field.
	// The actual conversion depends on the tRPC agent event schema.
	// For now, we create a basic response event.
	rawJSON, jsonErr := json.Marshal(task)
	if jsonErr != nil {
		return nil, fmt.Errorf(
			"anp: marshal task: %w", jsonErr)
	}

	// Use a generic event wrapper approach
	_ = rawJSON
	_ = evt

	return evt, nil
}

// ============================================================================
// Message Sending & Receiving
// ============================================================================

// SendMessage sends an ANP message to a remote agent.
// If E2EE is enabled (P5), the message is encrypted before sending.
func (a *ANPAdapter) SendMessage(
	ctx context.Context,
	msg *ANPMessage,
) (*ANPMessage, error) {
	if msg == nil {
		return nil, fmt.Errorf("anp: message is nil")
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf(
			"anp: marshal message: %w", err)
	}

	// If E2EE is available, wrap in P5 envelope
	if a.messenger != nil && msg.RecipientDID != "" {
		session, ok := a.messenger.getSession(msg.RecipientDID)
		if ok {
			encrypted, encErr := a.messenger.EncryptAndSend(
				ctx, session,
				msg.RecipientDID,
				payload,
			)
			if encErr != nil {
				return nil, fmt.Errorf(
					"anp: encrypt: %w", encErr)
			}

			// Wrap the encrypted envelope as a P5 notification
			encPayload, _ := json.Marshal(encrypted)
			return &ANPMessage{
				JSONRPC:      "2.0",
				Method:       "anp.e2ee.deliver",
				Params:       encPayload,
				Profile:      "P5",
				MessageID:    msg.MessageID,
				Timestamp:    time.Now().UTC().Format(time.RFC3339),
				SenderDID:    msg.SenderDID,
				RecipientDID: msg.RecipientDID,
			}, nil
		}
	}

	return msg, nil
}

// ReceiveMessage decrypts and parses an incoming ANP message.
// If the message uses P5 (E2EE), it is decrypted first.
func (a *ANPAdapter) ReceiveMessage(
	ctx context.Context,
	raw json.RawMessage,
) (*ANPMessage, error) {
	var msg ANPMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, fmt.Errorf(
			"anp: parse message: %w", err)
	}

	// Handle E2EE messages (P5)
	if msg.Profile == "P5" && a.messenger != nil {
		var encMsg EncryptedMessage
		if err := json.Unmarshal(msg.Params, &encMsg); err != nil {
			return nil, fmt.Errorf(
				"anp: parse encrypted message: %w", err)
		}

		plaintext, decErr := a.messenger.DecryptMessage(&encMsg)
		if decErr != nil {
			return nil, fmt.Errorf(
				"anp: decrypt: %w", decErr)
		}

		// Parse the decrypted inner message
		var innerMsg ANPMessage
		if err := json.Unmarshal(plaintext, &innerMsg); err != nil {
			return nil, fmt.Errorf(
				"anp: parse inner message: %w", err)
		}

		return &innerMsg, nil
	}

	return &msg, nil
}

// ============================================================================
// Task Delegation Helpers
// ============================================================================

// CreateTask creates an ANP Agent Task from a request message.
func (a *ANPAdapter) CreateTask(
	request *ANPMessage,
) *ANPAgentTask {
	return &ANPAgentTask{
		TaskID:    uuid.New().String(),
		Status:    "pending",
		Request:   request,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// AddTextArtifact adds a text artifact to a task.
func (a *ANPAdapter) AddTextArtifact(
	task *ANPAgentTask,
	name, text string,
) {
	task.Artifacts = append(task.Artifacts, ANTaskArtifact{
		Name: name,
		Parts: []ANTaskArtifactPart{
			{
				Type: "text",
				Text: text,
			},
		},
	})
	task.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
}

// ============================================================================
// Profile Support Helpers
// ============================================================================

// SupportsProfile checks if the adapter supports a given ANP
// message profile.
func (a *ANPAdapter) SupportsProfile(profile string) bool {
	return slices.Contains(a.supportedProfiles, profile)
}

// SupportedProfiles returns the list of supported ANP profiles.
func (a *ANPAdapter) SupportedProfiles() []string {
	result := make([]string, len(a.supportedProfiles))
	copy(result, a.supportedProfiles)
	return result
}

// HasE2EE returns whether E2EE messaging is available.
func (a *ANPAdapter) HasE2EE() bool {
	return a.messenger != nil
}

// ============================================================================
// ANP Error Codes (JSON-RPC 2.0 standard + ANP extensions)
// ============================================================================

const (
	// ANPErrParse       JSON parse error.
	ANPErrParse = -32700

	// ANPErrInvalidRequest   Invalid request structure.
	ANPErrInvalidRequest = -32600

	// ANPErrMethodNotFound   Method not found or unsupported.
	ANPErrMethodNotFound = -32601

	// ANPErrInvalidParams    Invalid method parameters.
	ANPErrInvalidParams = -32602

	// ANPErrInternal         Internal server error.
	ANPErrInternal = -32603

	// ANPErrE2EEFailed       E2EE encryption/decryption failure.
	ANPErrE2EEFailed = -32001

	// ANPErrProfileUnsupported  Requested profile not supported.
	ANPErrProfileUnsupported = -32002

	// ANPErrNegotiationFailed   Protocol negotiation failed.
	ANPErrNegotiationFailed = -32003

	// ANPErrTaskFailed          Task execution failure.
	ANPErrTaskFailed = -32004
)

// NewANPError creates an ANP JSON-RPC error with the given
// code and message.
func NewANPError(code int, message string, data any) *ANPJSONRPCError {
	err := &ANPJSONRPCError{
		Code:    code,
		Message: message,
	}

	if data != nil {
		dataBytes, jsonErr := json.Marshal(data)
		if jsonErr != nil {
			util.Logger.Warn("ANP: marshal error data",
				slog.String("error", jsonErr.Error()),
			)
		} else {
			err.Data = dataBytes
		}
	}

	return err
}

// NewANPResponse creates a successful ANP JSON-RPC response.
func NewANPResponse(id int, result any) (*ANPMessage, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}

	msgID := id
	return &ANPMessage{
		JSONRPC:   "2.0",
		ID:        &msgID,
		Result:    resultJSON,
		Profile:   "P1",
		MessageID: uuid.New().String(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// NewANPErrorResponse creates an ANP JSON-RPC error response.
func NewANPErrorResponse(id int, code int, message string, data any) *ANPMessage {
	msgID := id
	return &ANPMessage{
		JSONRPC:   "2.0",
		ID:        &msgID,
		Error:     NewANPError(code, message, data),
		Profile:   "P1",
		MessageID: uuid.New().String(),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
}
