package feishu

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/km269/wukong/internal/gateway"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// strPtr helper for tests: take a literal and return its address.
func sp(s string) *string { return &s }

// newFeishuChannelForTest builds a FeishuChannel with the given config
// without needing the full WukongConfig.
func newFeishuChannelForTest(cfg *gateway.FeishuChannelConfig) *FeishuChannel {
	if cfg == nil {
		cfg = &gateway.FeishuChannelConfig{}
	}
	return &FeishuChannel{cfg: cfg}
}

// ---------------------------------------------------------------------------
// extractTextContent
// ---------------------------------------------------------------------------

func TestExtractTextContentValid(t *testing.T) {
	if got := extractTextContent(`{"text":"hello world"}`); got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestExtractTextContentEmptyInput(t *testing.T) {
	if got := extractTextContent(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestExtractTextContentFallback(t *testing.T) {
	raw := "plain text without json"
	if got := extractTextContent(raw); got != raw {
		t.Errorf("got %q, want %q", got, raw)
	}
}

func TestExtractTextContentChinese(t *testing.T) {
	if got := extractTextContent(`{"text":"你好世界"}`); got != "你好世界" {
		t.Errorf("got %q, want %q", got, "你好世界")
	}
}

// ---------------------------------------------------------------------------
// cleanTextContent — @_user_N placeholder stripping
// ---------------------------------------------------------------------------

func TestCleanTextContentStripsMentionPlaceholder(t *testing.T) {
	in := "@_user_1 帮我写代码"
	if got := cleanTextContent(in); got != "帮我写代码" {
		t.Errorf("got %q, want %q", got, "帮我写代码")
	}
}

func TestCleanTextContentKeepsRegularAt(t *testing.T) {
	in := "email me at user@host.com"
	if got := cleanTextContent(in); got != "email me at user@host.com" {
		t.Errorf("regular @ must be preserved, got %q", got)
	}
}

func TestCleanTextContentTrims(t *testing.T) {
	if got := cleanTextContent("  hi  "); got != "hi" {
		t.Errorf("got %q, want %q", got, "hi")
	}
}

// ---------------------------------------------------------------------------
// parseP2MessageReceiveV1
// ---------------------------------------------------------------------------

func TestParseP2TextMessage(t *testing.T) {
	fc := newFeishuChannelForTest(nil)
	evt := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: sp("ou_123")},
			},
			Message: &larkim.EventMessage{
				MessageId:   sp("om_msg1"),
				ChatId:      sp("oc_chat1"),
				MessageType: sp("text"),
				Content:     sp(`{"text":"@_user_1 hello"}`),
			},
		},
	}
	gm := fc.parseP2MessageReceiveV1(evt)
	if gm == nil {
		t.Fatal("expected non-nil GatewayMessage")
	}
	if gm.PlatformUserID != "ou_123" {
		t.Errorf("user: got %q want ou_123", gm.PlatformUserID)
	}
	if gm.ConversationID != "oc_chat1" {
		t.Errorf("conv: got %q want oc_chat1", gm.ConversationID)
	}
	if gm.MessageID != "om_msg1" {
		t.Errorf("msgid: got %q want om_msg1", gm.MessageID)
	}
	if gm.ContentType != "text" {
		t.Errorf("ctype: got %q want text", gm.ContentType)
	}
	// @_user_1 placeholder must be stripped.
	if gm.Content != "hello" {
		t.Errorf("content: got %q want %q", gm.Content, "hello")
	}
	if len(gm.RawData) == 0 {
		t.Error("RawData should be populated")
	}
}

func TestParseP2ImageMessagePlaceholder(t *testing.T) {
	fc := newFeishuChannelForTest(nil)
	evt := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: sp("ou_1")},
			},
			Message: &larkim.EventMessage{
				MessageId:   sp("om_img"),
				MessageType: sp("image"),
			},
		},
	}
	gm := fc.parseP2MessageReceiveV1(evt)
	if !strings.Contains(gm.Content, "[图片:") {
		t.Errorf("image content placeholder, got %q", gm.Content)
	}
}

func TestParseP2NonUserSenderIgnored(t *testing.T) {
	fc := newFeishuChannelForTest(nil)
	evt := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderType: sp("app"),
				SenderId:   &larkim.UserId{OpenId: sp("ou_bot")},
			},
			Message: &larkim.EventMessage{
				MessageType: sp("text"),
				Content:     sp(`{"text":"hi"}`),
			},
		},
	}
	if gm := fc.parseP2MessageReceiveV1(evt); gm != nil {
		t.Errorf("non-user sender must be ignored, got %+v", gm)
	}
}

func TestParseP2NilEvent(t *testing.T) {
	fc := newFeishuChannelForTest(nil)
	if gm := fc.parseP2MessageReceiveV1(nil); gm != nil {
		t.Errorf("nil event must return nil, got %+v", gm)
	}
	if gm := fc.parseP2MessageReceiveV1(&larkim.P2MessageReceiveV1{}); gm != nil {
		t.Errorf("event with nil Data must return nil, got %+v", gm)
	}
}

func TestParseP2UnknownTypeFallback(t *testing.T) {
	fc := newFeishuChannelForTest(nil)
	evt := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: sp("ou_1")},
			},
			Message: &larkim.EventMessage{
				MessageType: sp("share_chat"),
			},
		},
	}
	gm := fc.parseP2MessageReceiveV1(evt)
	if !strings.Contains(gm.Content, "share_chat") {
		t.Errorf("unknown type fallback should echo type, got %q", gm.Content)
	}
	if gm.ContentType != "share_chat" {
		t.Errorf("ctype: got %q want share_chat", gm.ContentType)
	}
}

func TestParseP2FileDisabledByDefault(t *testing.T) {
	fc := newFeishuChannelForTest(&gateway.FeishuChannelConfig{})
	evt := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: sp("ou_1")},
			},
			Message: &larkim.EventMessage{MessageType: sp("file")},
		},
	}
	gm := fc.parseP2MessageReceiveV1(evt)
	if gm.Content != "[文件消息暂不支持]" {
		t.Errorf("file disabled default: got %q", gm.Content)
	}
}

// ---------------------------------------------------------------------------
// Validate
// ---------------------------------------------------------------------------

func TestValidateMissingCredentials(t *testing.T) {
	fc := newFeishuChannelForTest(&gateway.FeishuChannelConfig{})
	if err := fc.Validate(); err == nil {
		t.Error("missing app_id/app_secret must fail validation")
	}
}

func TestValidateOK(t *testing.T) {
	fc := newFeishuChannelForTest(&gateway.FeishuChannelConfig{
		AppID: "cli_x", AppSecret: "secret",
	})
	if err := fc.Validate(); err != nil {
		t.Errorf("valid creds should pass, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// BuildUserID / BuildSessionID
// ---------------------------------------------------------------------------

func TestBuildIDs(t *testing.T) {
	fc := newFeishuChannelForTest(nil)
	// These are set by the channel on the gateway side via dispatch;
	// exercise the builders directly here.
	gm := fc.parseP2MessageReceiveV1(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: sp("ou_42")}},
			Message: &larkim.EventMessage{ChatId: sp("oc_99")},
		},
	})
	if uid := fc.BuildUserID(gm); uid != "feishu:ou_42" {
		t.Errorf("uid: got %q want feishu:ou_42", uid)
	}
	if sid := fc.BuildSessionID(gm); sid != "feishu-oc_99" {
		t.Errorf("sid: got %q want feishu-oc_99", sid)
	}
}

func TestBuildIDsAnonymous(t *testing.T) {
	fc := newFeishuChannelForTest(nil)
	gm := fc.parseP2MessageReceiveV1(&larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{},
		},
	})
	if uid := fc.BuildUserID(gm); uid != "feishu:anonymous" {
		t.Errorf("uid: got %q want feishu:anonymous", uid)
	}
	if sid := fc.BuildSessionID(gm); sid != "feishu-unknown" {
		t.Errorf("sid: got %q want feishu-unknown", sid)
	}
}

// ---------------------------------------------------------------------------
// mustMarshal / truncateText utilities (retained)
// ---------------------------------------------------------------------------

func TestMustMarshalStruct(t *testing.T) {
	type TS struct {
		Name string `json:"name"`
	}
	var ts TS
	if err := json.Unmarshal(mustMarshal(TS{Name: "x"}), &ts); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ts.Name != "x" {
		t.Errorf("got %q want x", ts.Name)
	}
}

func TestMustMarshalInvalid(t *testing.T) {
	if got := mustMarshal(make(chan int)); string(got) != "{}" {
		t.Errorf("expected {}, got %s", got)
	}
}

func TestTruncateTextExceedsLimit(t *testing.T) {
	if got := truncateText("hello world this is long", 5); !strings.HasSuffix(got, "...") {
		t.Errorf("should end with ..., got %q", got)
	}
}
