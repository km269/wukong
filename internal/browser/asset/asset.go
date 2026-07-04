package asset

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var assetRels = map[string]bool{
	"stylesheet": true, "icon": true,
	"apple-touch-icon": true, "apple-touch-icon-precomposed": true,
	"mask-icon": true, "manifest": true, "preload": true, "prefetch": true,
}

func linkRelDownloadable(rel string) bool {
	for _, tok := range strings.Fields(strings.ToLower(rel)) {
		if assetRels[tok] {
			return true
		}
	}
	return false
}

type RefSink func(u *url.URL, kind int) string

const (
	KindPage = iota
	KindAsset
)

func RewriteHTML(root *html.Node, base *url.URL, sink RefSink) {
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			rewriteElement(n, base, sink)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
}

func rewriteElement(n *html.Node, base *url.URL, sink RefSink) {
	switch n.DataAtom {
	case atom.A, atom.Area:
		rewriteAttr(n, "href", base, sink, pageOrAsset)
	case atom.Iframe, atom.Frame:
		rewriteAttr(n, "src", base, sink, pageOrAsset)
	case atom.Link:
		if linkRelDownloadable(attrVal(n, "rel")) {
			rewriteAttr(n, "href", base, sink, alwaysAsset)
		}
	case atom.Img:
		rewriteAttr(n, "src", base, sink, alwaysAsset)
		rewriteSrcset(n, base, sink)
	case atom.Source:
		rewriteAttr(n, "src", base, sink, alwaysAsset)
		rewriteSrcset(n, base, sink)
	case atom.Video:
		rewriteAttr(n, "src", base, sink, alwaysAsset)
		rewriteAttr(n, "poster", base, sink, alwaysAsset)
	case atom.Audio, atom.Track, atom.Embed:
		rewriteAttr(n, "src", base, sink, alwaysAsset)
	case atom.Object:
		rewriteAttr(n, "data", base, sink, alwaysAsset)
	case atom.Style:
		rewriteStyleText(n, base, sink)
	}
	rewriteInlineStyle(n, base, sink)
}

func alwaysAsset(*url.URL) int { return KindAsset }

func pageOrAsset(u *url.URL) int {
	if LikelyPage(u) {
		return KindPage
	}
	return KindAsset
}

func LikelyPage(u *url.URL) bool {
	ext := strings.ToLower(filepath.Ext(u.Path))
	pageExts := map[string]bool{
		".html": true, ".htm": true, ".php": true, ".asp": true,
		".aspx": true, ".jsp": true, ".cgi": true, ".pl": true,
		".py": true, ".rb": true, ".go": true, ".json": true,
	}
	if pageExts[ext] {
		return true
	}
	if ext == "" || ext == "/" {
		return true
	}
	return false
}

func rewriteAttr(n *html.Node, key string, base *url.URL, sink RefSink, kf func(*url.URL) int) {
	for i := range n.Attr {
		if !strings.EqualFold(n.Attr[i].Key, key) {
			continue
		}
		u, err := normalize(base, n.Attr[i].Val)
		if err != nil {
			return
		}
		n.Attr[i].Val = sink(u, kf(u))
		return
	}
}

func normalize(base *url.URL, ref string) (*url.URL, error) {
	return base.Parse(ref)
}

func rewriteSrcset(n *html.Node, base *url.URL, sink RefSink) {
	for i := range n.Attr {
		if !strings.EqualFold(n.Attr[i].Key, "srcset") {
			continue
		}
		n.Attr[i].Val = rewriteSrcsetValue(n.Attr[i].Val, base, sink)
		return
	}
}

func rewriteSrcsetValue(val string, base *url.URL, sink RefSink) string {
	parts := strings.Split(val, ",")
	for i, p := range parts {
		fields := strings.Fields(strings.TrimSpace(p))
		if len(fields) == 0 {
			continue
		}
		u, err := normalize(base, fields[0])
		if err != nil {
			continue
		}
		fields[0] = sink(u, KindAsset)
		parts[i] = strings.Join(fields, " ")
	}
	return strings.Join(parts, ", ")
}

func rewriteInlineStyle(n *html.Node, base *url.URL, sink RefSink) {
	for i := range n.Attr {
		if !strings.EqualFold(n.Attr[i].Key, "style") {
			continue
		}
		n.Attr[i].Val = string(RewriteCSS([]byte(n.Attr[i].Val), base, sink))
		return
	}
}

func rewriteStyleText(n *html.Node, base *url.URL, sink RefSink) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			c.Data = string(RewriteCSS([]byte(c.Data), base, sink))
		}
	}
}

func attrVal(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

var cssURLRe = regexp.MustCompile(`url\(\s*["']?([^"')]+)["']?\s*\)`)

func RewriteCSS(css []byte, base *url.URL, sink RefSink) []byte {
	return cssURLRe.ReplaceAllFunc(css, func(match []byte) []byte {
		rawURL := strings.TrimSpace(string(match[4 : len(match)-1]))
		if strings.HasPrefix(rawURL, "\"") && strings.HasSuffix(rawURL, "\"") {
			rawURL = rawURL[1 : len(rawURL)-1]
		} else if strings.HasPrefix(rawURL, "'") && strings.HasSuffix(rawURL, "'") {
			rawURL = rawURL[1 : len(rawURL)-1]
		}

		u, err := normalize(base, rawURL)
		if err != nil {
			return match
		}

		localPath := sink(u, KindAsset)
		return []byte("url(" + localPath + ")")
	})
}

type Downloader struct {
	ua            string
	timeout       time.Duration
	maxAssetBytes int64
}

type DownloadResult struct {
	Body  []byte
	IsCSS bool
}

var ErrTooLarge = fmt.Errorf("asset too large")

func NewDownloader(ua string, timeout time.Duration, maxAssetBytes int64) *Downloader {
	if maxAssetBytes <= 0 {
		maxAssetBytes = 10 * 1024 * 1024
	}
	return &Downloader{
		ua:            ua,
		timeout:       timeout,
		maxAssetBytes: maxAssetBytes,
	}
}

func (d *Downloader) Get(ctx context.Context, u *url.URL, referer string) (*DownloadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if d.ua != "" {
		req.Header.Set("User-Agent", d.ua)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	client := &http.Client{Timeout: d.timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.ContentLength > d.maxAssetBytes {
		return nil, ErrTooLarge
	}

	limitReader := io.LimitReader(resp.Body, d.maxAssetBytes+1)
	body, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, err
	}

	if int64(len(body)) > d.maxAssetBytes {
		return nil, ErrTooLarge
	}

	ext := strings.ToLower(filepath.Ext(u.Path))
	isCSS := ext == ".css"

	return &DownloadResult{Body: body, IsCSS: isCSS}, nil
}

func LocalPath(host string, u *url.URL, kind int, reserved string) string {
	path := u.Path
	if path == "" || path == "/" {
		path = "/index.html"
	}

	hash := sha256.Sum256([]byte(u.String()))
	hashStr := fmt.Sprintf("%x", hash[:8])

	ext := filepath.Ext(path)
	if ext == "" && kind == KindPage {
		ext = ".html"
	}

	dir := filepath.Dir(path)
	if dir == "." || dir == "/" {
		dir = ""
	}

	var filename string
	if kind == KindPage {
		filename = filepath.Base(path)
		if !strings.HasSuffix(filename, ".html") {
			filename += ".html"
		}
	} else {
		filename = hashStr + ext
	}

	if dir != "" {
		return filepath.Join(dir, filename)
	}
	return filename
}

func Dir(path string) string {
	return filepath.Dir(path)
}

func Rel(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}
