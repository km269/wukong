package feishu

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestExtractTextContentValid parses valid Feishu text content JSON.
func TestExtractTextContentValid(t *testing.T) {
	content := `{"text":"hello world"}`
	result := extractTextContent(content)
	if result != "hello world" {
		t.Errorf("got %q, want %q", result, "hello world")
	}
}

// TestExtractTextContentEmptyInput returns empty string.
func TestExtractTextContentEmptyInput(t *testing.T) {
	result := extractTextContent("")
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}

// TestExtractTextContentEmptyText returns empty when text field is ""
func TestExtractTextContentEmptyText(t *testing.T) {
	result := extractTextContent(`{"text":""}`)
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}

// TestExtractTextContentFallback returns raw string on invalid JSON.
func TestExtractTextContentFallback(t *testing.T) {
	raw := "plain text without json"
	result := extractTextContent(raw)
	if result != raw {
		t.Errorf("got %q, want %q", result, raw)
	}
}

// TestExtractTextContentChinese handles Chinese characters in JSON.
func TestExtractTextContentChinese(t *testing.T) {
	content := `{"text":"你好世界"}`
	result := extractTextContent(content)
	if result != "你好世界" {
		t.Errorf("got %q, want %q", result, "你好世界")
	}
}

// TestExtractTextContentAt mentions within text.
func TestExtractTextContentAtMentions(t *testing.T) {
	content := `{"text":"@user help me please"}`
	result := extractTextContent(content)
	if !strings.Contains(result, "@user") {
		t.Errorf("should contain @user mention, got %q", result)
	}
}

// TestReadBodyReadsCompleteBody reads the full request body.
func TestReadBodyReadsCompleteBody(t *testing.T) {
	expected := []byte(`{"type":"event_callback","event":{}}`)
	r, _ := http.NewRequest("POST", "/feishu", bytes.NewReader(expected))

	body, err := readBody(r)
	if err != nil {
		t.Fatalf("readBody failed: %v", err)
	}
	if !bytes.Equal(body, expected) {
		t.Errorf("got %q, want %q", string(body), string(expected))
	}
}

// TestReadBodyResetsBody verifies body can be read again after reset.
func TestReadBodyResetsBody(t *testing.T) {
	expected := []byte(`{"type":"test"}`)
	r, _ := http.NewRequest("POST", "/feishu", bytes.NewReader(expected))

	// First read.
	body1, err := readBody(r)
	if err != nil {
		t.Fatalf("first readBody: %v", err)
	}

	// Second read after reset.
	body2, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}

	if !bytes.Equal(body1, expected) {
		t.Errorf("first read got %q, want %q", string(body1), string(expected))
	}
	if !bytes.Equal(body2, expected) {
		t.Errorf("second read got %q, want %q", string(body2), string(expected))
	}
}

// TestReadBodyEmptyBody handles empty request body.
func TestReadBodyEmptyBody(t *testing.T) {
	r, _ := http.NewRequest("POST", "/feishu", bytes.NewReader([]byte{}))
	body, err := readBody(r)
	if err != nil {
		t.Fatalf("readBody failed: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("expected empty body, got %q", string(body))
	}
}

// TestMustMarshalString marshals a string value correctly.
func TestMustMarshalString(t *testing.T) {
	result := mustMarshal("hello")
	var s string
	if err := json.Unmarshal(result, &s); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if s != "hello" {
		t.Errorf("got %q, want %q", s, "hello")
	}
}

// TestMustMarshalStruct marshals a struct value correctly.
func TestMustMarshalStruct(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	result := mustMarshal(TestStruct{Name: "test", Age: 42})

	var ts TestStruct
	if err := json.Unmarshal(result, &ts); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if ts.Name != "test" || ts.Age != 42 {
		t.Errorf("got %+v, want {test 42}", ts)
	}
}

// TestMustMarshalInvalid returns empty JSON object on failure.
func TestMustMarshalInvalid(t *testing.T) {
	// Channels and functions cannot be marshaled.
	result := mustMarshal(make(chan int))
	if string(result) != "{}" {
		t.Errorf("expected {}, got %s", string(result))
	}
}

// TestMustMarshalNil returns the JSON null representation.
func TestMustMarshalNil(t *testing.T) {
	result := mustMarshal(nil)
	if string(result) != "null" {
		t.Errorf("expected null, got %s", string(result))
	}
}

// TestTruncateTextWithinLimit returns the original text unchanged.
func TestTruncateTextWithinLimit(t *testing.T) {
	text := "hello world"
	result := truncateText(text, 100)
	if result != text {
		t.Errorf("got %q, want %q", result, text)
	}
}

// TestTruncateTextExactLimit returns the original text.
func TestTruncateTextExactLimit(t *testing.T) {
	text := "hello world"
	result := truncateText(text, len([]rune(text)))
	if result != text {
		t.Errorf("got %q, want %q", result, text)
	}
}

// TestTruncateTextExceedsLimit truncates and appends ellipsis.
func TestTruncateTextExceedsLimit(t *testing.T) {
	text := "hello world this is a long text"
	result := truncateText(text, 5)
	if !strings.HasSuffix(result, "...") {
		t.Errorf("truncated text should end with ..., got %q", result)
	}
}

// TestTruncateTextChinese handles multi-byte characters correctly.
func TestTruncateTextChinese(t *testing.T) {
	text := "你好世界这是一个很长的文本"
	result := truncateText(text, 5)
	runes := []rune(result)
	if len(runes) > 5+3 { // 5 chars + "..."
		t.Errorf("truncated length should be <= 8, got %d: %q", len(runes), result)
	}
}

// TestTruncateTextEmpty returns empty string.
func TestTruncateTextEmpty(t *testing.T) {
	result := truncateText("", 10)
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}

// TestTruncateTextWhitespacePreserved preserves whitespace when
// content is within the limit (trimming is handled by sanitizeContent
// in sender.go, not by truncateText itself).
func TestTruncateTextWhitespacePreserved(t *testing.T) {
	text := "  hello world  "
	result := truncateText(text, 100)
	if result != text {
		t.Errorf("got %q, want %q (whitespace preserved within limit)", result, text)
	}
}
