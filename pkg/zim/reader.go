package zim

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// ErrNotFound is returned by Get when no entry matches the namespace and URL.
var ErrNotFound = errors.New("zim: not found")

const maxRedirectHops = 16

// Reader provides random access to a ZIM file's entries.
// Usage:
//
//	r, _ := zim.Open("archive.zim")
//	defer r.Close()
//	blob, _ := r.Get('A', "index.html")
//	fmt.Println(string(blob.Data))
type Reader struct {
	ra     io.ReaderAt
	closer io.Closer
	size   int64

	hdr   Header
	mimes []string

	mu            sync.Mutex
	cache         map[uint32][]byte // cluster index → decompressed data
	urlPtrs       []uint64          // cached URL pointer list
	clusterPtrs   []uint64          // cached cluster pointer list
}

// Blob is the result of a lookup: the resolved entry's bytes and metadata.
type Blob struct {
	Namespace byte
	URL       string
	Title     string
	MimeType  string
	Data      []byte
}

// Entry is a single directory entry as stored, suitable for iteration
// and export. A redirect entry has Redirect=true and names its target;
// a content entry carries its bytes in Data and its MIME type in MimeType.
type Entry struct {
	Namespace byte
	URL       string
	Title     string
	MimeType  string
	// Redirect fields.
	Redirect          bool
	RedirectNamespace byte
	RedirectURL       string
	// Content field.
	Data []byte
}

// dirent is the parsed form of a single ZIM directory entry.
type dirent struct {
	namespace byte
	url       string
	title     string
	mimeIdx   uint16
	// articleType follows the ArticleType enum.
	articleType ArticleType
	// For redirect entries.
	redirect    bool
	targetIndex uint32
	// For content entries (cluster/blob are placeholders; data is inline).
	data []byte
}

// Open opens a ZIM file on disk. Close the returned reader when done.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	r, err := NewReader(f, fi.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	r.closer = f
	return r, nil
}

// NewReader reads the header and index structures from ra, which must
// hold size bytes. The caller must ensure ra remains valid for the
// lifetime of the Reader.
func NewReader(ra io.ReaderAt, size int64) (*Reader, error) {
	r := &Reader{
		ra:    ra,
		size:  size,
		cache: make(map[uint32][]byte),
	}

	// Read header.
	hb := make([]byte, HeaderSize)
	if _, err := ra.ReadAt(hb, 0); err != nil {
		return nil, fmt.Errorf("zim: read header: %w", err)
	}
	if err := parseHeaderBytes(hb, &r.hdr); err != nil {
		return nil, err
	}

	// Basic sanity check.
	if r.hdr.URLPtrPos < HeaderSize ||
		r.hdr.URLPtrPos > uint64(size) {
		return nil, fmt.Errorf("zim: inconsistent header offsets")
	}

	// Read MIME type list.
	mimeLen := int(r.hdr.URLPtrPos - r.hdr.MimeListPos)
	mb := make([]byte, mimeLen)
	if _, err := ra.ReadAt(mb, int64(r.hdr.MimeListPos)); err != nil {
		return nil, fmt.Errorf("zim: read mime list: %w", err)
	}
	r.mimes = parseMimeList(mb)

	// Cache URL pointer list.
	urlPtrsLen := int(r.hdr.ArticleCount) * 8
	r.urlPtrs = make([]uint64, r.hdr.ArticleCount)
	upb := make([]byte, urlPtrsLen)
	if _, err := ra.ReadAt(upb, int64(r.hdr.URLPtrPos)); err != nil {
		return nil, fmt.Errorf("zim: read URL pointers: %w", err)
	}
	for i := uint32(0); i < r.hdr.ArticleCount; i++ {
		r.urlPtrs[i] = binary.LittleEndian.Uint64(upb[i*8:])
	}

	// Cache cluster pointer list.
	clusterPtrsLen := int(r.hdr.ClusterCount) * 8
	r.clusterPtrs = make([]uint64, r.hdr.ClusterCount)
	cpb := make([]byte, clusterPtrsLen)
	if _, err := ra.ReadAt(cpb, int64(r.hdr.ClusterPtrPos)); err != nil {
		return nil, fmt.Errorf("zim: read cluster pointers: %w", err)
	}
	for i := uint32(0); i < r.hdr.ClusterCount; i++ {
		r.clusterPtrs[i] = binary.LittleEndian.Uint64(cpb[i*8:])
	}

	return r, nil
}

// Close releases the underlying file if Open created the Reader.
func (r *Reader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}

// Count returns the number of directory entries.
func (r *Reader) Count() uint32 { return r.hdr.ArticleCount }

// MimeTypes returns the archive's MIME-type list.
func (r *Reader) MimeTypes() []string { return r.mimes }

// MainPage returns the archive's entry point.
func (r *Reader) MainPage() (Blob, error) {
	if r.hdr.MainPage == noMainPage {
		return Blob{}, fmt.Errorf("zim: no main page")
	}
	return r.blobAtIndex(r.hdr.MainPage, 0)
}

// Get resolves the entry at (namespace, URL), following redirects.
// It uses binary search over the URL-sorted directory.
func (r *Reader) Get(namespace byte, url string) (Blob, error) {
	target := key(namespace, url)
	lo, hi := uint32(0), r.hdr.ArticleCount
	for lo < hi {
		mid := lo + (hi-lo)/2
		d, err := r.direntAtIndex(mid)
		if err != nil {
			return Blob{}, err
		}
		k := key(d.namespace, d.url)
		switch {
		case k < target:
			lo = mid + 1
		case k > target:
			hi = mid
		default:
			return r.blobAtIndex(mid, 0)
		}
	}
	return Blob{}, fmt.Errorf("%w: %c/%s", ErrNotFound, namespace, url)
}

// EntryAt returns the directory entry at idx (0 <= idx < Count) in URL
// order. It exposes every entry exactly as stored, making it suitable
// for iteration and export.
func (r *Reader) EntryAt(idx uint32) (Entry, error) {
	d, err := r.direntAtIndex(idx)
	if err != nil {
		return Entry{}, err
	}
	e := Entry{
		Namespace: d.namespace,
		URL:       d.url,
		Title:     d.title,
	}
	if d.redirect {
		e.Redirect = true
		td, err := r.direntAtIndex(d.targetIndex)
		if err != nil {
			return Entry{}, fmt.Errorf(
				"zim: redirect target of %c/%s: %w",
				d.namespace, d.url, err)
		}
		e.RedirectNamespace = td.namespace
		e.RedirectURL = td.url
		return e, nil
	}
	if int(d.mimeIdx) < len(r.mimes) {
		e.MimeType = r.mimes[d.mimeIdx]
	}
	e.Data = make([]byte, len(d.data))
	copy(e.Data, d.data)
	return e, nil
}

// blobAtIndex follows redirects and returns the resolved Blob.
func (r *Reader) blobAtIndex(idx uint32, hop int) (Blob, error) {
	if hop > maxRedirectHops {
		return Blob{}, fmt.Errorf("zim: redirect loop")
	}
	d, err := r.direntAtIndex(idx)
	if err != nil {
		return Blob{}, err
	}
	if d.redirect {
		return r.blobAtIndex(d.targetIndex, hop+1)
	}
	mime := ""
	if int(d.mimeIdx) < len(r.mimes) {
		mime = r.mimes[d.mimeIdx]
	}
	return Blob{
		Namespace: d.namespace,
		URL:       d.url,
		Title:     d.title,
		MimeType:  mime,
		Data:      d.data,
	}, nil
}

// direntAtIndex reads and parses the dirent at the given URL order index.
func (r *Reader) direntAtIndex(idx uint32) (dirent, error) {
	if idx >= r.hdr.ArticleCount {
		return dirent{}, fmt.Errorf("zim: index %d out of range", idx)
	}
	start := r.urlPtrs[idx]
	// Determine the end of this dirent: next URL pointer or first cluster.
	var end uint64
	if idx+1 < r.hdr.ArticleCount {
		end = r.urlPtrs[idx+1]
	} else if r.hdr.ClusterCount > 0 {
		end = r.clusterPtrs[0]
	} else {
		end = r.hdr.ChecksumPos
	}
	if start >= end || end > uint64(r.size) {
		return dirent{}, fmt.Errorf("zim: bad dirent bounds at %d", idx)
	}
	b := make([]byte, end-start)
	if _, err := r.ra.ReadAt(b, int64(start)); err != nil {
		return dirent{}, err
	}
	return parseDirent(b)
}

// parseDirent decodes a single directory entry from raw bytes.
func parseDirent(b []byte) (dirent, error) {
	if len(b) < articleHeaderSize+8 {
		return dirent{}, fmt.Errorf("zim: dirent too short: %d bytes",
			len(b))
	}
	var d dirent
	le := binary.LittleEndian
	titleLen := int(le.Uint16(b[0:2]))
	urlLen := int(le.Uint16(b[2:4]))
	d.namespace = b[4]
	// [5:9] revision (ignored)
	d.articleType = ArticleType(b[9])
	d.mimeIdx = le.Uint16(b[10:12])

	// Extended data (8 bytes after 16-byte header).
	if d.articleType == ArticleTypeRedirect {
		d.redirect = true
		d.targetIndex = le.Uint32(b[16:20])
	} else {
		d.redirect = false
		// b[16:20] cluster, b[20:24] blob (both 0 in this impl).
	}

	pos := articleHeaderSize + 8 // 24 bytes fixed
	if pos+urlLen+titleLen > len(b) {
		return dirent{}, fmt.Errorf(
			"zim: dirent strings overflow: need %d, have %d",
			pos+urlLen+titleLen, len(b))
	}
	d.url = string(b[pos : pos+urlLen])
	pos += urlLen
	d.title = string(b[pos : pos+titleLen])
	pos += titleLen

	// 4-byte alignment padding.
	if pad := (4 - (pos % 4)) % 4; pad > 0 {
		pos += pad
	}

	// Remaining bytes are inline article data.
	if d.articleType == ArticleTypeArticle && pos < len(b) {
		d.data = make([]byte, len(b)-pos)
		copy(d.data, b[pos:])
	}

	return d, nil
}


