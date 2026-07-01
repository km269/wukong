package feishu

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/km269/wukong/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TestResolveReceiveIDGroupChat detects group chat (oc_ prefix).
func TestResolveReceiveIDGroupChat(t *testing.T) {
	sender := NewFeishuSender(makeFeishuConfig("", "", "", ""))
	msg := &gateway.GatewayMessage{
		ConversationID: "oc_abc123def",
		PlatformUserID: "ou_xyz789",
	}

	receiveID, receiveType := sender.resolveReceiveID(msg)
	if receiveID != "oc_abc123def" {
		t.Errorf("receiveID = %q, want %q", receiveID, "oc_abc123def")
	}
	if receiveType != "chat_id" {
		t.Errorf("receiveType = %q, want %q", receiveType, "chat_id")
	}
}

// TestResolveReceiveIDPersonalChat returns the user's open_id.
func TestResolveReceiveIDPersonalChat(t *testing.T) {
	sender := NewFeishuSender(makeFeishuConfig("", "", "", ""))
	msg := &gateway.GatewayMessage{
		ConversationID: "ou_user_single",
		PlatformUserID: "ou_xyz789",
	}

	receiveID, receiveType := sender.resolveReceiveID(msg)
	if receiveID != "ou_xyz789" {
		t.Errorf("receiveID = %q, want %q", receiveID, "ou_xyz789")
	}
	if receiveType != "open_id" {
		t.Errorf("receiveType = %q, want %q", receiveType, "open_id")
	}
}

// TestResolveReceiveIDFallback uses conversation ID as fallback.
func TestResolveReceiveIDFallback(t *testing.T) {
	sender := NewFeishuSender(makeFeishuConfig("", "", "", ""))
	msg := &gateway.GatewayMessage{
		ConversationID: "c123",
		PlatformUserID: "",
	}

	receiveID, receiveType := sender.resolveReceiveID(msg)
	if receiveID != "c123" {
		t.Errorf("receiveID = %q, want %q", receiveID, "c123")
	}
	if receiveType != "chat_id" {
		t.Errorf("receiveType = %q, want %q", receiveType, "chat_id")
	}
}

// TestResolveReceiveIDEmptyBoth returns empty values.
func TestResolveReceiveIDEmptyBoth(t *testing.T) {
	sender := NewFeishuSender(makeFeishuConfig("", "", "", ""))
	msg := &gateway.GatewayMessage{}

	receiveID, receiveType := sender.resolveReceiveID(msg)
	if receiveID != "" {
		t.Errorf("receiveID = %q, want empty", receiveID)
	}
	if receiveType != "chat_id" {
		t.Errorf("receiveType = %q, want %q", receiveType, "chat_id")
	}
}

// TestSanitizeContentWithinLimit returns original text.
func TestSanitizeContentWithinLimit(t *testing.T) {
	text := "hello world"
	result := sanitizeContent(text, 100)
	if result != text {
		t.Errorf("got %q, want %q", result, text)
	}
}

// TestSanitizeContentExceedsLimit truncates with ellipsis.
func TestSanitizeContentExceedsLimit(t *testing.T) {
	text := "hello world this is a very long text"
	result := sanitizeContent(text, 10)
	runes := []rune(result)
	if len(runes) > 10+3 {
		t.Errorf("truncated too long: %d runes: %q", len(runes), result)
	}
	if !strings.HasSuffix(result, "...") {
		t.Errorf("should end with ..., got %q", result)
	}
}

// TestSanitizeContentEmpty returns empty.
func TestSanitizeContentEmpty(t *testing.T) {
	result := sanitizeContent("", 100)
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}

// TestSanitizeContentWhitespace trimmed before truncation.
func TestSanitizeContentWhitespace(t *testing.T) {
	text := "  hello   "
	result := sanitizeContent(text, 100)
	if result != "hello" {
		t.Errorf("got %q, want %q", result, "hello")
	}
}

// TestBuildCardJSONStreaming builds a card with streaming indicator.
func TestBuildCardJSONStreaming(t *testing.T) {
	sender := NewFeishuSender(makeFeishuConfig("", "", "", ""))
	card := sender.buildCardJSON("thinking...", false)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(card), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Verify elements contain markdown and note.
	elements := parsed["elements"].([]any)
	if len(elements) < 2 {
		t.Fatalf("expected at least 2 elements, got %d", len(elements))
	}

	markdown := elements[0].(map[string]any)
	if markdown["tag"] != "markdown" {
		t.Errorf("first element tag = %q, want markdown", markdown["tag"])
	}

	note := elements[1].(map[string]any)
	if note["tag"] != "note" {
		t.Errorf("second element tag = %q, want note", note["tag"])
	}
}

// TestBuildCardJSONFinal builds a final card without note.
func TestBuildCardJSONFinal(t *testing.T) {
	sender := NewFeishuSender(makeFeishuConfig("", "", "", ""))
	card := sender.buildCardJSON("done!", true)

	var parsed map[string]any
	if err := json.Unmarshal([]byte(card), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	header := parsed["header"].(map[string]any)
	title := header["title"].(map[string]any)
	content := title["content"].(string)
	if !strings.Contains(content, "\u2705") {
		t.Errorf("header should show checkmark for final, got %q", content)
	}

	// Final card should NOT have a note element.
	elements := parsed["elements"].([]any)
	for _, el := range elements {
		elem := el.(map[string]any)
		if elem["tag"] == "note" {
			t.Error("final card should not have note element")
		}
	}
}

// TestBuildCardJSONStreamingHeader shows thinking indicator.
func TestBuildCardJSONStreamingHeader(t *testing.T) {
	sender := NewFeishuSender(makeFeishuConfig("", "", "", ""))
	card := sender.buildCardJSON("working", false)

	var parsed map[string]any
	json.Unmarshal([]byte(card), &parsed)
	header := parsed["header"].(map[string]any)
	title := header["title"].(map[string]any)
	content := title["content"].(string)
	if !strings.Contains(content, "\U0001F914") {
		t.Errorf("streaming header should show thinking emoji, got %q", content)
	}
}

// TestBuildCardJSONConfigPanel verifies card config structure.
func TestBuildCardJSONConfigPanel(t *testing.T) {
	sender := NewFeishuSender(makeFeishuConfig("", "", "", ""))
	card := sender.buildCardJSON("test", false)

	var parsed map[string]any
	json.Unmarshal([]byte(card), &parsed)
	cfg := parsed["config"].(map[string]any)
	if wsm, ok := cfg["wide_screen_mode"]; !ok || wsm != true {
		t.Errorf("wide_screen_mode should be true, got %v", wsm)
	}
}

// TestDrainEventContentDrainsChannel extracts all delta content.
func TestDrainEventContentDrainsChannel(t *testing.T) {
	ctx := context.Background()
	ch := make(chan *event.Event, 10)

	ch <- &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Delta: model.Message{Content: "hello "},
			}},
		},
	}
	ch <- &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Delta: model.Message{Content: "world"},
			}},
		},
	}
	close(ch)

	content := drainEventContent(ctx, ch)
	if content != "hello world" {
		t.Errorf("got %q, want %q", content, "hello world")
	}
}

// TestDrainEventContentNilChannel returns empty string.
func TestDrainEventContentNilChannel(t *testing.T) {
	content := drainEventContent(context.Background(), nil)
	if content != "" {
		t.Errorf("got %q, want empty string", content)
	}
}

// TestDrainEventContentSkipsErrors ignores events with errors.
func TestDrainEventContentSkipsErrors(t *testing.T) {
	ctx := context.Background()
	ch := make(chan *event.Event, 3)

	ch <- &event.Event{
		Response: &model.Response{
			Error: &model.ResponseError{Message: "oops"},
		},
	}
	ch <- &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{{
				Delta: model.Message{Content: "valid"},
			}},
		},
	}
	close(ch)

	content := drainEventContent(ctx, ch)
	if content != "valid" {
		t.Errorf("got %q, want %q", content, "valid")
	}
}

// TestDrainEventContentContextCancelled returns empty on cancel.
func TestDrainEventContentContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan *event.Event)

	// Cancel before draining.
	cancel()

	content := drainEventContent(ctx, ch)
	if content != "" {
		t.Errorf("expected empty string on cancel, got %q", content)
	}
}

// TestNewFeishuSenderDefaultAPIBase uses default URL when empty.
func TestNewFeishuSenderDefaultAPIBase(t *testing.T) {
	cfg := makeFeishuConfig("", "", "", "")
	cfg.APIBase = ""
	sender := NewFeishuSender(cfg)

	if sender == nil {
		t.Fatal("NewFeishuSender returned nil")
	}
	if sender.cfg == nil {
		t.Error("sender.cfg is nil")
	}
	if sender.larkClient == nil {
		t.Error("sender.larkClient is nil (SDK client required)")
	}
	if sender.respURLClient == nil {
		t.Error("sender.respURLClient is nil")
	}
	if sender.respURLClient.Timeout != senderHTTPTimeout {
		t.Errorf("respURLClient timeout = %v, want %v",
			sender.respURLClient.Timeout, senderHTTPTimeout)
	}
}

// TestNewFeishuSenderCustomAPIBase uses custom API base URL.
func TestNewFeishuSenderCustomAPIBase(t *testing.T) {
	cfg := makeFeishuConfig("", "", "", "")
	cfg.APIBase = "https://open.larksuite.com/open-apis"
	sender := NewFeishuSender(cfg)

	if sender == nil {
		t.Fatal("NewFeishuSender returned nil")
	}
}

// TestSenderConstants verifies default values are sensible.
func TestSenderConstants(t *testing.T) {
	if defaultMaxMessageLength != 4096 {
		t.Errorf("defaultMaxMessageLength = %d, want 4096",
			defaultMaxMessageLength)
	}
	if defaultStreamUpdateInterval != 500*time.Millisecond {
		t.Errorf("defaultStreamUpdateInterval = %v, want 500ms",
			defaultStreamUpdateInterval)
	}
	if senderHTTPTimeout != 30*time.Second {
		t.Errorf("senderHTTPTimeout = %v, want 30s",
			senderHTTPTimeout)
	}
}

// TestSendTextReplyNoCredentials handles nil credentials gracefully.
func TestSendTextReplyNoCredentials(t *testing.T) {
	cfg := makeFeishuConfig("", "", "", "")
	sender := NewFeishuSender(cfg)

	msg := &gateway.GatewayMessage{
		ConversationID: "oc_test",
		PlatformUserID: "ou_test",
	}

	ctx := context.Background()
	_ = sender.sendTextViaAPI(ctx, msg, "hello")
	// Expected to fail with SDK auth error; verifies no panic.
}
