package zim

import (
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Shared zstd codec. EncodeAll and DecodeAll are safe for concurrent
// use, so one encoder and one decoder serve the whole process.
var (
	zstdEncOnce sync.Once
	zstdEnc     *zstd.Encoder
	zstdDecOnce sync.Once
	zstdDec     *zstd.Decoder
)

// getZstdEncoder returns a lazily-initialised shared zstd encoder.
func getZstdEncoder() *zstd.Encoder {
	zstdEncOnce.Do(func() {
		zstdEnc, _ = zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.SpeedDefault))
	})
	return zstdEnc
}

// getZstdDecoder returns a lazily-initialised shared zstd decoder.
func getZstdDecoder() *zstd.Decoder {
	zstdDecOnce.Do(func() {
		zstdDec, _ = zstd.NewReader(nil)
	})
	return zstdDec
}

// isTextMime reports whether content with this MIME type is worth
// compressing. Already-compressed media (images, fonts, audio, video,
// archives) is classified as non-text and best stored uncompressed.
func isTextMime(mime string) bool {
	switch mime {
	case "application/json", "application/xml",
		"application/javascript", "application/x-javascript":
		return true
	}
	if len(mime) >= 5 && mime[:5] == "text/" {
		return true
	}
	// Any structured +xml type: application/rss+xml, image/svg+xml, ...
	return len(mime) >= 4 && mime[len(mime)-4:] == "+xml"
}
