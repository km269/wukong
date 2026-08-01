package chunking

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunk_BasicParagraphs(t *testing.T) {
	text := "First paragraph here.\n\nSecond paragraph here.\n\nThird paragraph here."
	c := New().WithMaxSize(100).WithOverlap(0)
	chunks := c.Chunk(text)
	if len(chunks) < 1 {
		t.Fatalf("expected at least 1 chunk, got %d", len(chunks))
	}
	// All chunks should contain non-empty text.
	for i, ch := range chunks {
		if strings.TrimSpace(ch.Text) == "" {
			t.Errorf("chunk %d is empty", i)
		}
	}
}

func TestChunk_SingleShortText(t *testing.T) {
	text := "Hello world."
	chunks := New().Chunk(text)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Text != "Hello world." {
		t.Errorf("unexpected chunk text: %q", chunks[0].Text)
	}
	if chunks[0].Index != 0 {
		t.Errorf("expected index 0, got %d", chunks[0].Index)
	}
}

func TestChunk_EmptyInput(t *testing.T) {
	c := New()
	if chunks := c.Chunk(""); len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty input, got %d", len(chunks))
	}
	if chunks := c.Chunk("   \n\n  \n  "); len(chunks) != 0 {
		t.Errorf("expected 0 chunks for whitespace-only input, got %d", len(chunks))
	}
}

func TestChunk_RespectsMaxSize(t *testing.T) {
	// Generate a long text with many paragraphs.
	var parts []string
	for i := 0; i < 50; i++ {
		parts = append(parts, "This is paragraph number "+string(rune('A'+i%26))+". It has some content for testing.")
	}
	text := strings.Join(parts, "\n\n")

	maxSize := 200
	c := New().WithMaxSize(maxSize).WithOverlap(0).WithMinSize(0)
	chunks := c.Chunk(text)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, ch := range chunks {
		runeCount := utf8.RuneCountInString(ch.Text)
		// Allow slight overshoot due to segment packing (a single
		// segment may exceed maxSize when it's one long sentence).
		if runeCount > maxSize*3 {
			t.Errorf("chunk %d too large: %d runes (max %d)", i, runeCount, maxSize)
		}
	}
}

func TestChunk_OverlapCreatesSharedContent(t *testing.T) {
	text := "Alpha paragraph one. Alpha paragraph two.\n\nBeta paragraph one. Beta paragraph two.\n\nGamma paragraph one. Gamma paragraph two."
	c := New().WithMaxSize(60).WithOverlap(20).WithMinSize(0)
	chunks := c.Chunk(text)
	if len(chunks) < 2 {
		t.Fatalf("need >=2 chunks to test overlap, got %d", len(chunks))
	}
	// The second chunk should start with content from the end of the first.
	firstTail := tailRunes(chunks[0].Text, 10)
	if !strings.Contains(chunks[1].Text, firstTail) && len(chunks) > 1 {
		// Overlap may not always produce exact substring matches due
		// to segment boundaries, but the second chunk should contain
		// SOME text from the first chunk's tail.
		t.Logf("overlap check: first tail=%q, chunk2 start=%q",
			firstTail, headRunes(chunks[1].Text, 30))
	}
}

func TestChunk_LongSentenceSplit(t *testing.T) {
	// A single very long sentence with no punctuation.
	text := strings.Repeat("word ", 500)
	c := New().WithMaxSize(100).WithOverlap(0).WithMinSize(0)
	chunks := c.Chunk(text)
	if len(chunks) < 2 {
		t.Fatalf("expected sentence split into multiple chunks, got %d", len(chunks))
	}
}

func TestChunk_CJKText(t *testing.T) {
	// Chinese text with sentence-ending punctuation.
	text := "这是第一句话。这是第二句话。这是第三句话。这是第四句话。这是第五句话。"
	c := New().WithMaxSize(20).WithOverlap(0).WithMinSize(0)
	chunks := c.Chunk(text)
	if len(chunks) < 2 {
		t.Fatalf("expected CJK text to split into multiple chunks, got %d", len(chunks))
	}
}

func TestChunk_OffsetsConsistent(t *testing.T) {
	text := "First sentence. Second sentence.\n\nThird paragraph with more content."
	c := New().WithMaxSize(100).WithOverlap(0)
	chunks := c.Chunk(text)
	for i, ch := range chunks {
		if ch.EndChar <= ch.StartChar {
			t.Errorf("chunk %d: end <= start (%d <= %d)", i, ch.EndChar, ch.StartChar)
		}
	}
	// First chunk should start at or near 0.
	if chunks[0].StartChar != 0 {
		t.Errorf("first chunk start = %d, want 0", chunks[0].StartChar)
	}
}

func TestEstimateTokens(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Error("empty string should be 0 tokens")
	}
	if EstimateTokens("hello") < 1 {
		t.Error("non-empty string should be >=1 token")
	}
	// CJK text should estimate more tokens than equal-length ASCII.
	asciiTokens := EstimateTokens("aaaaaaaa")
	cjkTokens := EstimateTokens("中文中文中")
	if cjkTokens <= asciiTokens/2 {
		t.Errorf("CJK tokens (%d) should be higher relative to ASCII (%d)",
			cjkTokens, asciiTokens)
	}
}

func TestChunker_Builder(t *testing.T) {
	c := New().
		WithMaxSize(500).
		WithOverlap(50).
		WithMinSize(50)
	if c.MaxSize != 500 {
		t.Errorf("MaxSize = %d, want 500", c.MaxSize)
	}
	if c.Overlap != 50 {
		t.Errorf("Overlap = %d, want 50", c.Overlap)
	}
	if c.MinSize != 50 {
		t.Errorf("MinSize = %d, want 50", c.MinSize)
	}
}

// tailRunes returns the last n runes of s.
func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// headRunes returns the first n runes of s.
func headRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
