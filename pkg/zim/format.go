// Package zim reads and writes the ZIM offline-archive format.
//
// ZIM is the open file format used by Kiwix for offline content
// (Wikipedia, Stack Exchange, etc.). This package provides a pure
// Go implementation following the ZIM v6 specification.
//
// Basic writer usage:
//
//	p := zim.NewPacker()
//	p.AddArticle("index.html", "Home", "text/html", data)
//	p.Build("out.zim", "App", "Desc", true)
//
// Basic reader usage:
//
//	r, _ := zim.Open("out.zim")
//	defer r.Close()
//	b, _ := r.Get('A', "index.html")
//	fmt.Println(string(b.Data))
package zim

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// ---------------------------------------------------------------------------
// ZIM file format constants
// ---------------------------------------------------------------------------

const (
	// HeaderSize is the fixed size of the ZIM header in bytes.
	HeaderSize = 80

	// MD5ChecksumSize is the size of the appended MD5 checksum.
	MD5ChecksumSize = 16

	// articleHeaderSize is the fixed-size header in each directory entry.
	articleHeaderSize = 16

	// noMainPage is the sentinel for mainPage/layoutPage meaning "none".
	noMainPage uint32 = 0xFFFFFFFF
)

// ---------------------------------------------------------------------------
// Article type
// ---------------------------------------------------------------------------

// ArticleType defines the type of a ZIM article.
type ArticleType uint8

const (
	// ArticleTypeRedirect points to another article (targetArticleIndex).
	ArticleTypeRedirect ArticleType = 0
	// ArticleTypeLinkFree is an article with metadata but no content.
	ArticleTypeLinkFree ArticleType = 1
	// ArticleTypeLinkTarget is the target of a redirect.
	ArticleTypeLinkTarget ArticleType = 2
	// ArticleTypeArticle is a standard content article.
	ArticleTypeArticle ArticleType = 3
)

// ---------------------------------------------------------------------------
// Compression type
// ---------------------------------------------------------------------------

// CompressionType defines the compression method for clusters.
// Codes follow the ZIM v5/v6 specification:
//
//	1 = stored (uncompressed)
//	5 = zstd
type CompressionType uint8

const (
	// CompressionNone stores cluster data uncompressed.
	CompressionNone CompressionType = 1
	// CompressionZstd stores cluster data compressed with zstd.
	CompressionZstd CompressionType = 5

	// extendedFlag is bit 4 of the cluster info byte: cluster offsets are uint64.
	extendedFlag uint8 = 0x10
)

// ---------------------------------------------------------------------------
// Sentinels stored in a directory entry's MIME type field to mark non-content
// entries. A redirect reuses the cluster slot to hold its target's URL index.
const (
	redirectEntry   uint16 = 0xffff
	linkTargetEntry uint16 = 0xfffe
	deletedEntry    uint16 = 0xfffd
)

// Namespace constants
// ---------------------------------------------------------------------------

const (
	// NamespaceContent is the standard namespace for content (pages and assets).
	NamespaceContent byte = 'C'
	// NamespaceMetadata is the standard namespace for metadata entries.
	NamespaceMetadata byte = 'M'
	// NamespaceWellKnown is the standard namespace for well-known entries.
	NamespaceWellKnown byte = 'W'
	// NamespaceArticle is an alias for NamespaceContent for compatibility.
	NamespaceArticle byte = 'C'
	// NamespaceRedirect is an alias for NamespaceWellKnown for compatibility.
	NamespaceRedirect byte = 'W'
)

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

// Header represents the fixed-size ZIM file header (80 bytes).
type Header struct {
	Magic         [4]byte  // "ZIM\x04" — ZIM magic number
	MajorVersion  uint16   // Major version, 6 for ZIM v6
	MinorVersion  uint16   // Minor version, 0
	UUID          [16]byte // Unique archive identifier
	ArticleCount  uint32   // Total number of articles
	ClusterCount  uint32   // Total number of clusters
	URLPtrPos     uint64   // File offset of URL pointer list
	TitlePtrPos   uint64   // File offset of title pointer list
	ClusterPtrPos uint64   // File offset of cluster pointer list
	MimeListPos   uint64   // File offset of MIME type list
	MainPage      uint32   // Article index of main page
	LayoutPage    uint32   // Article index of layout page
	ChecksumPos   uint64   // File offset of checksum data
}

// ---------------------------------------------------------------------------
// Article (public API)
// ---------------------------------------------------------------------------

// Article represents a single ZIM article entry in the public API.
type Article struct {
	Title       string      // Human-readable title
	URL         string      // Article URL (e.g., "index.html")
	Namespace   byte        // Namespace character ('C' for content)
	ArticleType ArticleType // Article type
	MimeType    string      // MIME type string (e.g., "text/html")
	Redirect    uint32      // Target article index (for redirects)
	Data        []byte      // Article content
}

// ---------------------------------------------------------------------------
// Internal types
// ---------------------------------------------------------------------------

// article is the internal representation of a ZIM article.
type article struct {
	Title       string
	URL         string
	Namespace   byte
	ArticleType ArticleType
	MimeType    uint16
	Redirect    uint32
	Data        []byte
	Cluster     uint32
	Blob        uint32
}

// cluster holds compressed or uncompressed article data.
type cluster struct {
	data []byte
}

// ---------------------------------------------------------------------------
// Header serialisation
// ---------------------------------------------------------------------------

// writeHeader encodes the header to its 80-byte wire format and writes
// it to w.
func writeHeader(w io.Writer, h *Header) error {
	b := make([]byte, HeaderSize)
	le := binary.LittleEndian
	copy(b[0:4], h.Magic[:])
	le.PutUint16(b[4:], h.MajorVersion)
	le.PutUint16(b[6:], h.MinorVersion)
	copy(b[8:24], h.UUID[:])
	le.PutUint32(b[24:], h.ArticleCount)
	le.PutUint32(b[28:], h.ClusterCount)
	le.PutUint64(b[32:], h.URLPtrPos)
	le.PutUint64(b[40:], h.TitlePtrPos)
	le.PutUint64(b[48:], h.ClusterPtrPos)
	le.PutUint64(b[56:], h.MimeListPos)
	le.PutUint32(b[64:], h.MainPage)
	le.PutUint32(b[68:], h.LayoutPage)
	le.PutUint64(b[72:], h.ChecksumPos)
	_, err := w.Write(b)
	return err
}

// parseHeaderBytes decodes and validates an 80-byte header into h.
func parseHeaderBytes(b []byte, h *Header) error {
	if len(b) < HeaderSize {
		return fmt.Errorf("zim: short header: %d bytes", len(b))
	}
	le := binary.LittleEndian
	if b[0] != 0x5a || b[1] != 0x49 || b[2] != 0x4d || b[3] != 0x04 {
		return fmt.Errorf("zim: bad magic, not a ZIM file")
	}
	copy(h.Magic[:], b[0:4])
	h.MajorVersion = le.Uint16(b[4:6])
	h.MinorVersion = le.Uint16(b[6:8])
	copy(h.UUID[:], b[8:24])
	h.ArticleCount = le.Uint32(b[24:28])
	h.ClusterCount = le.Uint32(b[28:32])
	h.URLPtrPos = le.Uint64(b[32:40])
	h.TitlePtrPos = le.Uint64(b[40:48])
	h.ClusterPtrPos = le.Uint64(b[48:56])
	h.MimeListPos = le.Uint64(b[56:64])
	h.MainPage = le.Uint32(b[64:68])
	h.LayoutPage = le.Uint32(b[68:72])
	h.ChecksumPos = le.Uint64(b[72:80])
	return nil
}

// ---------------------------------------------------------------------------
// Utility
// ---------------------------------------------------------------------------

// parseMimeList splits a null-delimited MIME list into a string slice.
// MIME types are stored as null-terminated strings, ending with a double null.
func parseMimeList(b []byte) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c == 0 {
			if i > start {
				out = append(out, string(b[start:i]))
			}
			start = i + 1
			if i > 0 && b[i-1] == 0 {
				break
			}
		}
	}
	return out
}

// key builds a sortable key from namespace and URL for binary search.
func key(namespace byte, url string) string {
	return string([]byte{namespace}) + url
}

// findMainPage locates the main page article index or returns noMainPage.
// Ensures the main page is a content article (not a redirect), as required by Kiwix.
// Priority: root index.html > root index > root main > first HTML article.
// Deep pages like "biographies/xxx/index.html" are never chosen automatically,
// because they make confusing entry points for users.
func findMainPage(articles []article) uint32 {
	var firstHTML uint32 = noMainPage

	for i, a := range articles {
		if a.ArticleType != ArticleTypeArticle {
			continue
		}

		if a.Namespace != NamespaceContent {
			continue
		}

		if a.URL == "index.html" {
			return uint32(i)
		}
		if a.URL == "index" {
			return uint32(i)
		}
		if a.URL == "main" {
			return uint32(i)
		}

		if firstHTML == noMainPage && strings.HasSuffix(a.URL, ".html") {
			firstHTML = uint32(i)
		}
	}

	if firstHTML != noMainPage {
		return firstHTML
	}

	for i, a := range articles {
		if a.ArticleType == ArticleTypeArticle {
			return uint32(i)
		}
	}
	return noMainPage
}
