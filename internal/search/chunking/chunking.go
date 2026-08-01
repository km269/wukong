// Package chunking provides semantic-aware text chunking for RAG
// ingestion. Inspired by SurfSense's hierarchical chunking and
// insane-search's recursive splitter, it splits long documents
// into coherent chunks along natural boundaries (paragraphs,
// sentences) with configurable overlap — preserving context
// across chunk boundaries and improving retrieval recall.
package chunking

import (
	"strings"
	"unicode/utf8"
)

// Default chunking parameters. These are tuned for typical RAG
// use cases with embedding models that accept ~512 tokens.
const (
	DefaultChunkSize    = 1200 // max runes per chunk
	DefaultOverlap      = 200  // overlap runes between consecutive chunks
	DefaultMinChunkSize = 100  // chunks smaller than this are merged
)

// Chunk represents a single text chunk with positional metadata.
type Chunk struct {
	Text      string // the chunk content
	Index     int    // 0-based position in the chunk list
	StartChar int    // rune offset in the original document
	EndChar   int    // exclusive rune offset
}

// Chunker splits text into semantically coherent chunks.
// It prefers paragraph boundaries, then sentence boundaries,
// and falls back to word boundaries for unbreakable segments.
type Chunker struct {
	MaxSize int // max runes per chunk
	Overlap int // overlap runes between consecutive chunks
	MinSize int // merge chunks smaller than this into neighbours
}

// New creates a Chunker with default parameters.
func New() *Chunker {
	return &Chunker{
		MaxSize: DefaultChunkSize,
		Overlap: DefaultOverlap,
		MinSize: DefaultMinChunkSize,
	}
}

// WithMaxSize sets the max chunk size in runes.
func (c *Chunker) WithMaxSize(n int) *Chunker {
	if n > 0 {
		c.MaxSize = n
	}
	return c
}

// WithOverlap sets the overlap between chunks in runes.
func (c *Chunker) WithOverlap(n int) *Chunker {
	if n >= 0 {
		c.Overlap = n
	}
	return c
}

// WithMinSize sets the minimum chunk size; smaller chunks are merged.
func (c *Chunker) WithMinSize(n int) *Chunker {
	if n >= 0 {
		c.MinSize = n
	}
	return c
}

// Chunk splits the input text into chunks. The algorithm:
//  1. Split text into paragraphs (double-newline separated).
//  2. Greedily pack paragraphs into chunks up to MaxSize.
//  3. When a single paragraph exceeds MaxSize, split it by sentences.
//  4. When a single sentence exceeds MaxSize, split it by words.
//  5. Apply overlap between consecutive chunks.
//  6. Merge chunks smaller than MinSize into their neighbours.
func (c *Chunker) Chunk(text string) []Chunk {
	if c.MaxSize <= 0 {
		c.MaxSize = DefaultChunkSize
	}
	if c.Overlap < 0 {
		c.Overlap = 0
	}
	if c.Overlap >= c.MaxSize {
		c.Overlap = c.MaxSize / 4
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// Step 1: Split into segments at natural boundaries, tracking
	// the rune offset of each segment in the original text.
	segments := c.splitIntoSegments(text)
	if len(segments) == 0 {
		return nil
	}

	// Step 2: Greedily pack segments into chunks.
	rawChunks := c.packSegments(segments)
	if len(rawChunks) == 0 {
		return nil
	}

	// Step 3: Apply overlap between consecutive chunks.
	if c.Overlap > 0 && len(rawChunks) > 1 {
		rawChunks = c.applyOverlap(rawChunks, text)
	}

	// Step 4: Merge tiny chunks into neighbours.
	if c.MinSize > 0 {
		rawChunks = c.mergeSmall(rawChunks)
	}

	// Step 5: Assign final indices.
	out := make([]Chunk, len(rawChunks))
	for i, rc := range rawChunks {
		out[i] = Chunk{
			Text:      rc.text,
			Index:     i,
			StartChar: rc.start,
			EndChar:   rc.end,
		}
	}
	return out
}

// segment is a text unit (paragraph, sentence, or word run)
// with its rune offset in the original document.
type segment struct {
	text  string
	start int // rune offset
	end   int // exclusive
}

// splitIntoSegments breaks text into paragraphs, and each large
// paragraph into sentences, and each large sentence into word runs.
// The result is a flat list of segments that each fit within
// roughly MaxSize runes (or are unbreakable single words).
func (c *Chunker) splitIntoSegments(text string) []segment {
	var segs []segment

	paragraphs := splitParagraphs(text)
	for _, para := range paragraphs {
		if utf8.RuneCountInString(para.text) <= c.MaxSize {
			segs = append(segs, para)
			continue
		}
		// Paragraph too large: split into sentences.
		sentences := splitSentences(para.text, para.start)
		for _, sent := range sentences {
			if utf8.RuneCountInString(sent.text) <= c.MaxSize {
				segs = append(segs, sent)
				continue
			}
			// Sentence too large: split into word runs.
			words := splitWords(sent.text, sent.start, c.MaxSize)
			segs = append(segs, words...)
		}
	}
	return segs
}

// packSegments greedily packs segments into chunks up to MaxSize.
func (c *Chunker) packSegments(segs []segment) []rawChunk {
	var chunks []rawChunk
	var current []segment
	currentSize := 0

	for _, seg := range segs {
		segLen := utf8.RuneCountInString(seg.text)
		if currentSize+segLen > c.MaxSize && currentSize > 0 {
			chunks = append(chunks, buildChunk(current))
			current = nil
			currentSize = 0
		}
		current = append(current, seg)
		currentSize += segLen
	}
	if len(current) > 0 {
		chunks = append(chunks, buildChunk(current))
	}
	return chunks
}

// applyOverlap prepends the tail of each chunk to the next chunk.
func (c *Chunker) applyOverlap(chunks []rawChunk, fullText string) []rawChunk {
	out := make([]rawChunk, len(chunks))
	out[0] = chunks[0]
	for i := 1; i < len(chunks); i++ {
		prev := chunks[i-1]
		// Take the last Overlap runes of the previous chunk.
		prevText := prev.text
		prevRunes := []rune(prevText)
		overlapStart := len(prevRunes) - c.Overlap
		if overlapStart < 0 {
			overlapStart = 0
		}
		overlapText := string(prevRunes[overlapStart:])
		// Compute the new start offset.
		newStart := prev.end - utf8.RuneCountInString(overlapText)
		if newStart < 0 {
			newStart = 0
		}
		out[i] = rawChunk{
			text:  overlapText + chunks[i].text,
			start: newStart,
			end:   chunks[i].end,
		}
	}
	return out
}

// mergeSmall merges chunks whose text is shorter than MinSize
// into their preceding neighbour (or following, for the first).
func (c *Chunker) mergeSmall(chunks []rawChunk) []rawChunk {
	if len(chunks) <= 1 {
		return chunks
	}
	var out []rawChunk
	for _, ch := range chunks {
		if len(out) > 0 && utf8.RuneCountInString(ch.text) < c.MinSize {
			// Merge into previous.
			prev := &out[len(out)-1]
			prev.text = prev.text + "\n" + ch.text
			prev.end = ch.end
			continue
		}
		out = append(out, ch)
	}
	// If the first chunk ended up tiny, merge into the next.
	if len(out) > 1 && utf8.RuneCountInString(out[0].text) < c.MinSize {
		out[1].text = out[0].text + "\n" + out[1].text
		out[1].start = out[0].start
		out = out[1:]
	}
	return out
}

// rawChunk is an intermediate chunk before index assignment.
type rawChunk struct {
	text       string
	start, end int
}

// buildChunk joins segments and tracks offsets.
func buildChunk(segs []segment) rawChunk {
	if len(segs) == 0 {
		return rawChunk{}
	}
	var sb strings.Builder
	for i, s := range segs {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(s.text)
	}
	return rawChunk{
		text:  sb.String(),
		start: segs[0].start,
		end:   segs[len(segs)-1].end,
	}
}

// --- Boundary splitters ---

// splitParagraphs splits text on blank lines (one or more
// consecutive newlines with optional whitespace).
func splitParagraphs(text string) []segment {
	var segs []segment
	parts := strings.Split(text, "\n\n")
	offset := 0
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			offset += utf8.RuneCountInString(part) + 2 // \n\n
			continue
		}
		segs = append(segs, segment{
			text:  trimmed,
			start: offset,
			end:   offset + utf8.RuneCountInString(trimmed),
		})
		offset += utf8.RuneCountInString(part) + 2
	}
	return segs
}

// splitSentences splits a paragraph into sentences at sentence-
// ending punctuation (. ! ? 。！？) followed by whitespace or EOF.
// startOffset is the rune offset of paraStart in the original doc.
func splitSentences(para string, paraStart int) []segment {
	var segs []segment
	var sb strings.Builder
	segStart := 0
	runeIdx := 0

	endSentence := func() {
		s := strings.TrimSpace(sb.String())
		if s != "" {
			segs = append(segs, segment{
				text:  s,
				start: paraStart + segStart,
				end:   paraStart + segStart + utf8.RuneCountInString(s),
			})
		}
		sb.Reset()
	}

	for _, r := range para {
		sb.WriteRune(r)
		if isSentenceEnd(r) {
			// Look ahead: if followed by whitespace or end, flush.
			endSentence()
			segStart = runeIdx + 1
		}
		runeIdx++
	}
	endSentence()
	return segs
}

// splitWords splits a long sentence into word-boundary segments,
// each at most maxRunes runes.
func splitWords(text string, startOffset, maxRunes int) []segment {
	var segs []segment
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	var sb strings.Builder
	segStart := 0
	currentLen := 0

	flush := func() {
		s := strings.TrimSpace(sb.String())
		if s != "" {
			segs = append(segs, segment{
				text:  s,
				start: startOffset + segStart,
				end:   startOffset + segStart + utf8.RuneCountInString(s),
			})
		}
		sb.Reset()
	}

	for _, w := range words {
		wLen := utf8.RuneCountInString(w)
		if currentLen > 0 && currentLen+1+wLen > maxRunes {
			flush()
			segStart += currentLen + 1
			currentLen = 0
		}
		if currentLen > 0 {
			sb.WriteString(" ")
			currentLen++
		}
		sb.WriteString(w)
		currentLen += wLen
	}
	flush()
	return segs
}

// isSentenceEnd reports whether r is sentence-ending punctuation.
func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', '。', '！', '？':
		return true
	}
	return false
}

// EstimateTokens returns a rough token estimate for the text.
// Uses ~4 chars/token for ASCII and ~1.5 chars/token for CJK.
// This is a heuristic; exact counts require a tokenizer.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	asciiCount := 0
	cjkCount := 0
	for _, r := range text {
		if r < 128 {
			asciiCount++
		} else {
			cjkCount++
		}
	}
	// ~4 chars per token for ASCII, ~1.5 for CJK.
	tokens := asciiCount/4 + cjkCount*2/3
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}
