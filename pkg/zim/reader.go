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
//	blob, _ := r.Get('C', "index.html")
//	fmt.Println(string(blob.Data))
type Reader struct {
	ra     io.ReaderAt
	closer io.Closer
	size   int64

	hdr   Header
	mimes []string

	mu          sync.Mutex
	cache       map[uint32][]byte // cluster index → decompressed data
	urlPtrs     []uint64          // cached URL pointer list
	clusterPtrs []uint64          // cached cluster pointer list
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
	namespace   byte
	url         string
	title       string
	mimeIdx     uint16
	redirect    bool
	targetIndex uint32
	cluster     uint32
	blob        uint32
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

	// Read MIME type list (may include 8-byte alignment padding).
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
	data, err := r.readFromCluster(d.cluster, d.blob)
	if err != nil {
		return Entry{}, err
	}
	e.Data = data
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
	data, err := r.readFromCluster(d.cluster, d.blob)
	if err != nil {
		return Blob{}, err
	}
	return Blob{
		Namespace: d.namespace,
		URL:       d.url,
		Title:     d.title,
		MimeType:  mime,
		Data:      data,
	}, nil
}

// readFromCluster reads data from a cluster.
// clusterIdx is 0-based. blobIdx is the blob index (0, 1, 2, ...).
// Standard ZIM cluster format: [compression byte][offset0][offset1]...[offsetN][blob0][blob1]...[blobN]
// Where offsets are 4-byte little-endian uint32, offset[i] is the start offset of blob[i] relative to data area start.
// The data area starts after all offsets, and offset[N] = total data size.
func (r *Reader) readFromCluster(clusterIdx, blobIdx uint32) ([]byte, error) {
	if clusterIdx >= uint32(len(r.clusterPtrs)) {
		return nil, fmt.Errorf("zim: cluster index %d out of range (max=%d)", clusterIdx, len(r.clusterPtrs))
	}

	r.mu.Lock()
	if cached, ok := r.cache[clusterIdx]; ok {
		r.mu.Unlock()
		return r.blobDataFromCluster(cached, blobIdx)
	}
	r.mu.Unlock()

	clusterStart := r.clusterPtrs[clusterIdx]
	compByteBuf := make([]byte, 1)
	if _, err := r.ra.ReadAt(compByteBuf, int64(clusterStart)); err != nil {
		return nil, fmt.Errorf("zim: read compression byte: %w", err)
	}
	compression := compByteBuf[0]

	var clusterEnd uint64
	if clusterIdx+1 < uint32(len(r.clusterPtrs)) {
		clusterEnd = r.clusterPtrs[clusterIdx+1]
	} else {
		clusterEnd = uint64(r.size) - 16
	}

	dataStart := clusterStart + 1
	dataSize := clusterEnd - dataStart

	var clusterData []byte
	if compression == byte(CompressionZstd) {
		compressedBuf := make([]byte, dataSize)
		if _, err := r.ra.ReadAt(compressedBuf, int64(dataStart)); err != nil {
			return nil, fmt.Errorf("zim: read compressed cluster: %w", err)
		}
		decoder := getZstdDecoder()
		decompressed, err := decoder.DecodeAll(compressedBuf, nil)
		if err != nil {
			return nil, fmt.Errorf("zim: decompress cluster: %w", err)
		}
		clusterData = decompressed
	} else {
		clusterData = make([]byte, dataSize)
		if _, err := r.ra.ReadAt(clusterData, int64(dataStart)); err != nil {
			return nil, fmt.Errorf("zim: read uncompressed cluster: %w", err)
		}
	}

	r.mu.Lock()
	r.cache[clusterIdx] = clusterData
	r.mu.Unlock()

	return r.blobDataFromCluster(clusterData, blobIdx)
}

func (r *Reader) blobDataFromCluster(clusterData []byte, blobIdx uint32) ([]byte, error) {
	w := uint32(4)
	need := int((blobIdx + 2) * w)
	if need > len(clusterData) {
		return nil, fmt.Errorf("zim: blob %d out of range in cluster", blobIdx)
	}
	o0 := binary.LittleEndian.Uint32(clusterData[blobIdx*w:])
	o1 := binary.LittleEndian.Uint32(clusterData[(blobIdx+1)*w:])
	if o0 > o1 || int(o1) > len(clusterData) {
		return nil, fmt.Errorf("zim: bad blob offsets in cluster")
	}
	out := make([]byte, o1-o0)
	copy(out, clusterData[o0:o1])
	return out, nil
}

// direntAtIndex reads and parses the dirent at the given URL order index.
func (r *Reader) direntAtIndex(idx uint32) (dirent, error) {
	if idx >= r.hdr.ArticleCount {
		return dirent{}, fmt.Errorf("zim: index %d out of range", idx)
	}
	start := r.urlPtrs[idx]
	// Determine the end of this dirent.
	var end uint64
	if idx+1 < r.hdr.ArticleCount {
		end = r.urlPtrs[idx+1] // end of next URL pointer
	} else if r.hdr.ClusterCount > 0 {
		end = r.clusterPtrs[0] // start of first cluster
	} else {
		end = r.hdr.ChecksumPos // end of file (before MD5)
	}
	if start >= end || end > uint64(r.size) {
		return dirent{}, fmt.Errorf("zim: bad dirent bounds at %d: start=%d end=%d size=%d",
			idx, start, end, r.size)
	}
	b := make([]byte, end-start)
	if _, err := r.ra.ReadAt(b, int64(start)); err != nil {
		return dirent{}, err
	}
	return parseDirent(b)
}

func readCString(b []byte, start int) (string, int, bool) {
	if start > len(b) {
		return "", start, false
	}
	for i := start; i < len(b); i++ {
		if b[i] == 0 {
			return string(b[start:i]), i + 1, true
		}
	}
	return "", start, false
}

// parseDirent decodes a single directory entry from raw bytes using standard ZIM format.
func parseDirent(b []byte) (dirent, error) {
	if len(b) < 12 {
		return dirent{}, fmt.Errorf("zim: dirent too short: %d bytes", len(b))
	}
	var d dirent
	le := binary.LittleEndian
	d.mimeIdx = le.Uint16(b[0:])
	d.namespace = b[3]

	var p int
	if d.mimeIdx == redirectEntry {
		d.redirect = true
		d.targetIndex = le.Uint32(b[8:])
		p = 12
	} else {
		d.cluster = le.Uint32(b[8:])
		d.blob = le.Uint32(b[12:])
		p = 16
	}

	url, n1, ok := readCString(b, p)
	if !ok {
		return dirent{}, fmt.Errorf("zim: unterminated url")
	}
	title, _, ok := readCString(b, n1)
	if !ok {
		return dirent{}, fmt.Errorf("zim: unterminated title")
	}

	d.url, d.title = url, title
	return d, nil
}
