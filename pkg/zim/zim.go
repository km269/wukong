// Package zim provides ZIM file format creation following the
// ZIM specification (https://wiki.openzim.org/wiki/ZIM_file_format).
//
// This implementation creates ZIM archives compatible with Kiwix
// and other ZIM readers. Clusters are compressed with zstd, and a
// full-file MD5 checksum is appended for data integrity.
//
// Basic usage:
//
//	packer := zim.NewPacker()
//	packer.AddArticle("index.html", "Home", "text/html", htmlData)
//	packer.Build("output.zim", "MyApp", "Description", true)
//
// For reading:
//
//	r, _ := zim.Open("output.zim")
//	defer r.Close()
//	blob, _ := r.Get('A', "index.html")
package zim

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// Packer (write path)
// ---------------------------------------------------------------------------

// Packer creates ZIM archive files. It manages articles, MIME types,
// and produces a complete, well-formed ZIM file.
type Packer struct {
	articles  []article
	byKey     map[string]int // key → index in articles slice
	mimeTypes map[string]uint16

	// mainPageNS and mainPageURL are set via SetMainPage.
	// When empty, findMainPage auto-detects the main page.
	mainPageNS  byte
	mainPageURL string
}

// NewPacker creates a new ZIM packer ready to accept articles.
func NewPacker() *Packer {
	return &Packer{
		articles:  make([]article, 0),
		byKey:     make(map[string]int),
		mimeTypes: make(map[string]uint16),
	}
}

// SetMainPage marks an existing entry as the archive's main page.
// The namespace and URL must match a previously added article.
func (p *Packer) SetMainPage(namespace byte, url string) {
	p.mainPageNS = namespace
	p.mainPageURL = url
}

// AddContent adds a content article with explicit namespace support.
// If an article with the same namespace+URL already exists, it is
// replaced in place. Title defaults to URL if empty. MIME defaults
// to "text/html" if empty.
func (p *Packer) AddContent(namespace byte, url, title, mimeType string,
	data []byte) error {
	if url == "" {
		return errors.New("zim: article URL cannot be empty")
	}
	if title == "" {
		title = url
	}
	if mimeType == "" {
		mimeType = "text/html"
	}

	p.registerMimeType(mimeType)

	e := article{
		Title:       title,
		URL:         url,
		Namespace:   namespace,
		ArticleType: ArticleTypeArticle,
		MimeType:    p.mimeTypes[mimeType],
		Data:        data,
	}
	p.put(e)
	return nil
}

// AddMetadata adds a metadata entry in the M namespace.
// value is stored as text/plain.
func (p *Packer) AddMetadata(name, value string) {
	e := article{
		Title:       name,
		URL:         name,
		Namespace:   'M',
		ArticleType: ArticleTypeArticle,
		MimeType:    p.mimeTypes["text/plain"],
		Data:        []byte(value),
	}
	p.registerMimeType("text/plain")
	e.MimeType = p.mimeTypes["text/plain"]
	p.put(e)
}

// AddArticle adds a content article to the ZIM archive in namespace 'A'.
// Deprecated: prefer AddContent('A', url, title, mimeType, data).
func (p *Packer) AddArticle(url, title, mimeType string, data []byte) error {
	return p.AddContent('A', url, title, mimeType, data)
}

// AddRedirect adds a redirect article that points to another article
// by namespace and URL. The target entry will be resolved at build time.
func (p *Packer) AddRedirect(ns byte, url, title string,
	targetNS byte, targetURL string) error {
	if url == "" {
		return errors.New("zim: redirect URL cannot be empty")
	}
	if title == "" {
		title = url
	}

	e := article{
		Title:       title,
		URL:         url,
		Namespace:   ns,
		ArticleType: ArticleTypeRedirect,
		// Store target as string key for later resolution.
		Redirect: 0,
	}
	// Hijack the Data field to store the target key.
	e.Data = []byte(key(targetNS, targetURL))
	p.put(e)
	return nil
}

// ArticleCount returns the number of articles currently added.
func (p *Packer) ArticleCount() int {
	return len(p.articles)
}

// put inserts or replaces an entry keyed by namespace+URL.
func (p *Packer) put(e article) {
	k := key(e.Namespace, e.URL)
	if idx, ok := p.byKey[k]; ok {
		p.articles[idx] = e
		return
	}
	p.byKey[k] = len(p.articles)
	p.articles = append(p.articles, e)
}

// Build creates the ZIM archive and writes it to the output file.
// If compress is true, text cluster data is zstd-compressed and binary
// clusters are stored uncompressed. A full-file MD5 checksum is appended.
func (p *Packer) Build(outputPath, appName, appDescription string,
	compress bool) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("zim: create output file: %w", err)
	}
	defer file.Close()

	if _, err := p.WriteTo(file, compress); err != nil {
		return err
	}
	return nil
}

// WriteTo serialises the entire ZIM archive to w and returns the number
// of bytes written (before the appended MD5 checksum). It is the primary
// serialisation entry point; Build delegates to it.
func (p *Packer) WriteTo(w io.Writer, compress bool) (int64, error) {
	if len(p.articles) == 0 {
		return 0, errors.New("zim: no articles to pack")
	}

	// Sort articles by namespace+URL (ZIM spec requirement).
	sort.Slice(p.articles, func(i, j int) bool {
		a, b := p.articles[i], p.articles[j]
		ka := key(a.Namespace, a.URL)
		kb := key(b.Namespace, b.URL)
		return ka < kb
	})

	// Resolve redirect targets using the sorted order.
	if err := p.resolveRedirects(); err != nil {
		return 0, err
	}

	mimeList := p.buildMimeList()
	clusters := p.buildClusters(compress)

	// Calculate file layout.
	urlPtrListSize := uint64(len(p.articles) * 8)
	titlePtrListSize := uint64(len(p.articles) * 8)
	clusterPtrListSize := uint64(len(clusters) * 8)
	articleDataSize := p.calculateTotalArticleSize()
	mimeListSize := uint64(len(mimeList))

	urlPtrPos := uint64(HeaderSize)
	titlePtrPos := urlPtrPos + urlPtrListSize
	clusterPtrPos := titlePtrPos + titlePtrListSize
	mimeListPos := clusterPtrPos + clusterPtrListSize
	articlesPos := mimeListPos + mimeListSize
	clusterDataPos := articlesPos + articleDataSize

	checksumPos := clusterDataPos
	for _, cl := range clusters {
		checksumPos += uint64(len(cl.data))
	}

	// Determine main page index.
	mainPage := p.resolveMainPage()

	// Build the entire file content into a buffer so the MD5 can
	// cover everything except the checksum itself.
	var buf bytes.Buffer

	header := Header{
		Magic:         [4]byte{0x5a, 0x49, 0x4d, 0x04},
		MajorVersion:  6,
		MinorVersion:  0,
		UUID:          p.computeUUID(),
		ArticleCount:  uint32(len(p.articles)),
		ClusterCount:  uint32(len(clusters)),
		URLPtrPos:     urlPtrPos,
		TitlePtrPos:   titlePtrPos,
		ClusterPtrPos: clusterPtrPos,
		MimeListPos:   mimeListPos,
		MainPage:      mainPage,
		LayoutPage:    noMainPage,
		ChecksumPos:   checksumPos,
	}
	if err := writeHeader(&buf, &header); err != nil {
		return 0, fmt.Errorf("zim: write header: %w", err)
	}

	// Write URL pointer list.
	for i := range p.articles {
		pos := articlesPos + p.calculateArticleOffset(i)
		if err := binary.Write(&buf, binary.LittleEndian, pos); err != nil {
			return 0, fmt.Errorf("zim: write URL pointer: %w", err)
		}
	}

	// Write title pointer list (sorted by namespace+title).
	titleOrder := make([]int, len(p.articles))
	for i := range titleOrder {
		titleOrder[i] = i
	}
	sort.Slice(titleOrder, func(i, j int) bool {
		a, b := p.articles[titleOrder[i]], p.articles[titleOrder[j]]
		ka := key(a.Namespace, a.Title)
		kb := key(b.Namespace, b.Title)
		if ka != kb {
			return ka < kb
		}
		return titleOrder[i] < titleOrder[j]
	})
	for _, idx := range titleOrder {
		pos := articlesPos + p.calculateArticleOffset(idx)
		if err := binary.Write(&buf, binary.LittleEndian, pos); err != nil {
			return 0, fmt.Errorf("zim: write title pointer: %w", err)
		}
	}

	// Write cluster pointers.
	currentOffset := articlesPos + articleDataSize
	for i := range clusters {
		if err := binary.Write(&buf, binary.LittleEndian,
			currentOffset); err != nil {
			return 0, fmt.Errorf("zim: write cluster pointer: %w", err)
		}
		currentOffset += uint64(len(clusters[i].data))
	}

	// Write MIME type list.
	if _, err := buf.Write(mimeList); err != nil {
		return 0, fmt.Errorf("zim: write MIME list: %w", err)
	}

	// Write articles.
	for i := range p.articles {
		if err := writeArticle(&buf, &p.articles[i]); err != nil {
			return 0, fmt.Errorf("zim: write article %q: %w",
				p.articles[i].URL, err)
		}
	}

	// Write cluster data.
	for i := range clusters {
		if _, err := buf.Write(clusters[i].data); err != nil {
			return 0, fmt.Errorf("zim: write cluster %d: %w", i, err)
		}
	}

	// Compute MD5 checksum and write to w.
	checksum := md5.Sum(buf.Bytes())
	bodyLen := int64(buf.Len())

	if _, err := w.Write(buf.Bytes()); err != nil {
		return 0, fmt.Errorf("zim: write file body: %w", err)
	}
	if _, err := w.Write(checksum[:]); err != nil {
		return 0, fmt.Errorf("zim: write checksum: %w", err)
	}

	return bodyLen, nil
}

// resolveMainPage returns the article index of the main page.
// If SetMainPage was called, it looks up the key; otherwise
// it falls back to auto-detection.
func (p *Packer) resolveMainPage() uint32 {
	if p.mainPageURL != "" {
		k := key(p.mainPageNS, p.mainPageURL)
		for i, a := range p.articles {
			if key(a.Namespace, a.URL) == k {
				return uint32(i)
			}
		}
	}
	return findMainPage(p.articles)
}

// resolveRedirects resolves redirect target keys to article indices
// after sorting. The target key is stored in the article's Data field
// during AddRedirect.
func (p *Packer) resolveRedirects() error {
	for i := range p.articles {
		a := &p.articles[i]
		if a.ArticleType != ArticleTypeRedirect || len(a.Data) == 0 {
			continue
		}
		targetKey := string(a.Data)
		found := false
		for j, b := range p.articles {
			if b.ArticleType == ArticleTypeRedirect {
				continue
			}
			if key(b.Namespace, b.URL) == targetKey {
				a.Redirect = uint32(j)
				a.Data = nil // Clear the key placeholder.
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf(
				"zim: redirect target %q not found", targetKey)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal writer helpers
// ---------------------------------------------------------------------------

func (p *Packer) registerMimeType(mime string) {
	if _, ok := p.mimeTypes[mime]; !ok {
		p.mimeTypes[mime] = uint16(len(p.mimeTypes))
	}
}

func (p *Packer) buildMimeList() []byte {
	var buf bytes.Buffer

	// Empty first entry (index 0 is always empty).
	buf.WriteByte(0)

	sortedMimes := make([]string, 0, len(p.mimeTypes))
	for mime := range p.mimeTypes {
		sortedMimes = append(sortedMimes, mime)
	}
	sort.Strings(sortedMimes)

	for _, mime := range sortedMimes {
		buf.WriteString(mime)
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

func (p *Packer) buildClusters(compress bool) []cluster {
	const maxClusterSize = 2 * 1024 * 1024 // 2 MiB per ZIM cluster

	// Build reverse map from MIME index to MIME string for
	// classifying content as text or binary.
	mimeByIndex := make(map[uint16]string, len(p.mimeTypes))
	for mime, idx := range p.mimeTypes {
		mimeByIndex[idx] = mime
	}

	// Separate buffers: text content goes to compressed clusters,
	// binary content (images, fonts, etc.) goes to uncompressed clusters.
	var textBuf, binaryBuf []byte
	var textClusters, binaryClusters []cluster

	for i := range p.articles {
		if p.articles[i].ArticleType != ArticleTypeArticle ||
			len(p.articles[i].Data) == 0 {
			continue
		}

		data := p.articles[i].Data
		mime := mimeByIndex[p.articles[i].MimeType]

		if isTextMime(mime) {
			// Text content — goes to a compressible cluster.
			if len(textBuf)+len(data)+4 > maxClusterSize {
				if len(textBuf) > 0 {
					textClusters = append(textClusters,
						cluster{data: textBuf})
				}
				textBuf = nil
			}
			blobHeader := make([]byte, 4)
			binary.LittleEndian.PutUint32(blobHeader, uint32(len(data)))
			textBuf = append(textBuf, blobHeader...)
			textBuf = append(textBuf, data...)
		} else {
			// Binary content — stored uncompressed.
			if len(binaryBuf)+len(data)+4 > maxClusterSize {
				if len(binaryBuf) > 0 {
					binaryClusters = append(binaryClusters,
						cluster{data: binaryBuf})
				}
				binaryBuf = nil
			}
			blobHeader := make([]byte, 4)
			binary.LittleEndian.PutUint32(blobHeader, uint32(len(data)))
			binaryBuf = append(binaryBuf, blobHeader...)
			binaryBuf = append(binaryBuf, data...)
		}
	}

	// Flush remaining buffers.
	if len(textBuf) > 0 {
		textClusters = append(textClusters, cluster{data: textBuf})
	}
	if len(binaryBuf) > 0 {
		binaryClusters = append(binaryClusters, cluster{data: binaryBuf})
	}

	// Use the shared zstd encoder for text clusters.
	enc := getZstdEncoder()

	// Text clusters: compress with zstd.
	for i := range textClusters {
		if compress && enc != nil {
			textClusters[i].data = enc.EncodeAll(
				textClusters[i].data, nil)
		}
		compByte := byte(CompressionZstd)
		if !compress || enc == nil {
			compByte = byte(CompressionNone)
		}
		textClusters[i].data = append([]byte{compByte},
			textClusters[i].data...)
	}

	// Binary clusters: always stored uncompressed.
	for i := range binaryClusters {
		binaryClusters[i].data = append([]byte{byte(CompressionNone)},
			binaryClusters[i].data...)
	}

	// Text clusters first, then binary clusters.
	return append(textClusters, binaryClusters...)
}

func (p *Packer) calculateTotalArticleSize() uint64 {
	var total uint64
	for i := range p.articles {
		total += p.calculateArticleOffset(i) - p.calculateSingleArticleSize(i)
	}
	for i := range p.articles {
		total += p.calculateSingleArticleSize(i)
	}
	return total
}

func (p *Packer) calculateSingleArticleSize(idx int) uint64 {
	a := &p.articles[idx]
	size := uint64(articleHeaderSize) +
		uint64(len(a.URL)) + uint64(len(a.Title))
	if a.ArticleType == ArticleTypeArticle {
		size += uint64(len(a.Data))
	}
	// 4-byte alignment includes the 8-byte extData.
	size += 8
	if size%4 != 0 {
		size += 4 - (size % 4)
	}
	return size
}

func (p *Packer) calculateArticleOffset(articleIndex int) uint64 {
	var offset uint64
	for i := 0; i < articleIndex; i++ {
		offset += p.calculateSingleArticleSize(i)
	}
	return offset
}

// writeArticle serialises a single article to w.
func writeArticle(w *bytes.Buffer, a *article) error {
	header := make([]byte, articleHeaderSize)
	binary.LittleEndian.PutUint16(header[0:2], uint16(len(a.Title)))
	binary.LittleEndian.PutUint16(header[2:4], uint16(len(a.URL)))
	header[4] = a.Namespace
	binary.LittleEndian.PutUint32(header[5:9], 0) // revision
	header[9] = byte(a.ArticleType)
	binary.LittleEndian.PutUint16(header[10:12], a.MimeType)
	binary.LittleEndian.PutUint32(header[12:16], a.Redirect)
	w.Write(header)

	// Extended data (8 bytes).
	extData := make([]byte, 8)
	if a.ArticleType == ArticleTypeRedirect {
		binary.LittleEndian.PutUint32(extData[0:4], a.Redirect)
	}
	w.Write(extData)

	// URL and title strings.
	w.WriteString(a.URL)
	w.WriteString(a.Title)

	// Pad to 4-byte alignment.
	if pad := (4 - (w.Len() % 4)) % 4; pad > 0 {
		w.Write(make([]byte, pad))
	}

	// Article data.
	if a.ArticleType == ArticleTypeArticle && len(a.Data) > 0 {
		w.Write(a.Data)
	}

	return nil
}

// ---------------------------------------------------------------------------
// UUID generation
// ---------------------------------------------------------------------------

// computeUUID derives a deterministic UUID from all article content.
// Identical input produces identical output, making ZIM builds reproducible.
func (p *Packer) computeUUID() [16]byte {
	h := md5.New()
	for i := range p.articles {
		a := &p.articles[i]
		h.Write([]byte(a.URL))
		h.Write([]byte(a.Title))
		var lenBuf [8]byte
		binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(a.Data)))
		h.Write(lenBuf[:])
		h.Write(a.Data)
	}
	var uuid [16]byte
	copy(uuid[:], h.Sum(nil))
	return uuid
}

// generateUUID creates a time-based UUID used as a fallback when there
// are no articles to hash.
func generateUUID() [16]byte {
	var uuid [16]byte
	timestamp := uint64(time.Now().UnixNano())
	binary.LittleEndian.PutUint64(uuid[0:8], timestamp)
	for i := 8; i < 16; i++ {
		uuid[i] = byte(time.Now().UnixNano() >> ((i - 8) * 8) & 0xFF)
	}
	return uuid
}
