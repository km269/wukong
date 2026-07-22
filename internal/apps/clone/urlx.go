// Package clone provides website cloning functionality.
//
// urlx.go: Deterministic URL-to-local-path mapping.
// Converts any web URL to a unique, stable local file path that is safe for
// cross-platform filesystems.
package clone

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Kind classifies a URL as a page or a specific asset type.
type URLKind int

const (
	KindPage  URLKind = iota // HTML page that needs rendering and link rewriting.
	KindAsset                // Generic static resource (fallback).
	KindCSS                  // Stylesheet (.css or <link rel="stylesheet">).
	KindImage                // Image (<img>, <picture>, CSS url(), etc.).
	KindFont                 // Web font (.woff, .ttf, etc.).
	KindJS                   // JavaScript file.
	KindMedia                // Audio/video/media files.
)

// reservedPrefix is the directory where all downloaded assets reside.
const reservedPrefix = "_wukong"

// binaryExts lists extensions that indicate binary/document (non-HTML) content.
var binaryExts = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true, ".pps": true, ".ppsx": true,
	".rtf": true, ".txt": true, ".csv": true, ".tsv": true,
	".odt": true, ".ods": true, ".odp": true,
	".xlsb": true, ".xlsm": true, ".docm": true, ".dotm": true,
	".zip": true, ".tar": true, ".gz": true, ".bz2": true,
	".7z": true, ".rar": true, ".exe": true, ".dmg": true,
	".iso": true, ".img": true, ".msi": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".ico": true, ".webp": true, ".avif": true,
	".bmp": true, ".tiff": true, ".tif": true,
	".css": true, ".js": true, ".json": true, ".xml": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true,
	".eot": true, ".mp3": true, ".mp4": true, ".webm": true,
	".ogg": true, ".wav": true, ".flac": true, ".avi": true,
	".mov": true, ".m4v": true, ".m4a": true, ".wmv": true,
	".flv": true, ".swf": true,
}

// Normalize converts a URL into a canonical form suitable for deduplication.
// It resolves relative references against base, rejects non-fetchable schemes,
// and produces a stable string representation.
func Normalize(base, ref string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}

	refURL, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("parse ref URL: %w", err)
	}

	resolved := baseURL.ResolveReference(refURL)

	// Reject non-fetchable schemes.
	switch resolved.Scheme {
	case "javascript", "mailto", "tel", "data", "file":
		return "", fmt.Errorf("unsupported scheme: %s", resolved.Scheme)
	case "http", "https":
		// OK.
	default:
		return "", fmt.Errorf("unsupported scheme: %s", resolved.Scheme)
	}

	return canonical(resolved), nil
}

// canonical produces a normalized string representation of a URL.
//   - Scheme and host are lowercased.
//   - Fragment is removed.
//   - Default ports (80, 443) are stripped.
//   - Path is cleaned and preserves trailing slash for directories.
func canonical(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)

	// Strip default ports.
	host = stripDefaultPort(host, scheme)

	// Clean path, ensuring root is at least "/".
	p := u.Path
	if p == "" {
		p = "/"
	}
	p = path.Clean(p)
	if p != "/" && strings.HasSuffix(u.Path, "/") && !strings.HasSuffix(p, "/") {
		p += "/"
	}

	canon := scheme + "://" + host + p

	// Append query string if present, properly encoding unsafe chars.
	if u.RawQuery != "" {
		canon += "?" + encodeQuery(u.RawQuery)
	}

	return canon
}

// stripDefaultPort removes ":80" or ":443" from a host string.
func stripDefaultPort(host, scheme string) string {
	if scheme == "http" && strings.HasSuffix(host, ":80") {
		return host[:len(host)-3]
	}
	if scheme == "https" && strings.HasSuffix(host, ":443") {
		return host[:len(host)-4]
	}
	return host
}

// encodeQuery ensures query string characters are safe for filename use.
func encodeQuery(rawQuery string) string {
	var b strings.Builder
	for _, r := range rawQuery {
		switch r {
		case ' ', '\t', '\n', '\r', '\\', '<', '>', '"', '|', '?', '*', ':':
			fmt.Fprintf(&b, "%%%02X", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// LikelyPage returns true if a URL reference likely points to an HTML page
// rather than a binary asset. Used when extracting links from parsed HTML.
func LikelyPage(ref string) bool {
	u, err := url.Parse(ref)
	if err != nil {
		return true
	}
	lower := strings.ToLower(u.Path)
	for ext := range binaryExts {
		if strings.HasSuffix(lower, ext) {
			return false
		}
	}
	return true
}

// LocalPath converts a canonical URL to a deterministic local file path.
// Pages are mapped to human-readable directories with index.html.
// Assets are placed under the reserved prefix, organized by host.
func LocalPath(seedHost, canonicalURL string, kind URLKind) string {
	u, err := url.Parse(canonicalURL)
	if err != nil {
		return fmt.Sprintf("unknown_%x", sha256Str(canonicalURL, 8))
	}

	switch kind {
	case KindPage:
		return localPagePath(seedHost, u)
	default:
		return localAssetPath(u)
	}
}

// localPagePath generates a local path for a page URL.
// Same-host pages: about/ → about/index.html, about/team → about/team/index__q-xxx.html
// Subdomain pages: sub.example.com/page → sub.example.com/page/index__q-xxx.html
func localPagePath(seedHost string, u *url.URL) string {
	p := u.Path
	if p == "" {
		p = "/"
	}

	// Split path into directory and leaf.
	dir, leaf := splitPath(p)
	if dir != "" {
		dir = strings.Trim(dir, "/")
	}

	// Collapse index.html into the directory itself.
	collapseIndex(&leaf)

	// Apply query parameter suffix to all pages that have query parameters,
	// including index pages (e.g. /list/?Page=2 must not collide with /list/).
	// Uses readable naming for common pagination params, falls back to hash
	// for complex/unknown query strings.
	if u.RawQuery != "" {
		leaf = applyPageQuerySuffix(leaf, u.RawQuery)
	}

	if strings.EqualFold(u.Host, seedHost) {
		// Same-host: use clean directory structure.
		if dir == "" {
			return leaf
		}
		return dir + "/" + leaf
	}

	// Subdomain: prefix with full hostname to avoid conflicts.
	hostDir := strings.ToLower(u.Host)
	if dir == "" {
		return hostDir + "/" + leaf
	}
	return hostDir + "/" + dir + "/" + leaf
}

// localAssetPath generates a local path for an asset URL.
// Assets go under _wukong/<host>/<dir>/<base>__q-<hash>.<ext>
func localAssetPath(u *url.URL) string {
	host := strings.ToLower(u.Host)
	p := u.Path
	if p == "" {
		p = "/"
	}

	dir, base := splitAsset(p)
	if u.RawQuery != "" {
		base = applyQueryHash(base, u.RawQuery)
	}

	if dir == "" {
		return reservedPrefix + "/" + host + "/" + base
	}
	return reservedPrefix + "/" + host + "/" + dir + "/" + base
}

// splitPath splits a URL path into directory and leaf (filename).
// "/" → ("", "index.html")
// "/docs/" → ("docs", "index.html")
// "/docs/guide.html" → ("docs", "guide.html")
// "/about" → ("", "about.html")
func splitPath(p string) (dir, leaf string) {
	// Preserve trailing slash before trimming to detect directories.
	trailingSlash := strings.HasSuffix(p, "/") && p != "/"
	p = strings.Trim(p, "/")
	if p == "" {
		return "", "index.html"
	}

	lastSlash := strings.LastIndex(p, "/")
	if lastSlash < 0 {
		// Single path component: if URL had trailing slash, it's a directory.
		if trailingSlash {
			return p, "index.html"
		}
		return "", p + ".html"
	}

	dir = p[:lastSlash]
	leaf = p[lastSlash+1:]
	if trailingSlash || !strings.Contains(leaf, ".") {
		// If URL ended with "/", the last component is a directory.
		if trailingSlash {
			dir = p
			leaf = "index.html"
		} else if !strings.Contains(leaf, ".") {
			leaf += ".html"
		}
	}
	return dir, leaf
}

// splitAsset splits a path into directory and base filename for assets.
func splitAsset(p string) (dir, base string) {
	p = strings.Trim(p, "/")
	if p == "" {
		return "", "index"
	}
	lastSlash := strings.LastIndex(p, "/")
	if lastSlash < 0 {
		return "", p
	}
	return p[:lastSlash], p[lastSlash+1:]
}

// collapseIndex folds "index.html" / "index.htm" into its parent directory.
// Returns true if the leaf was collapsed (making it just "index.html").
func collapseIndex(leaf *string) bool {
	l := strings.ToLower(*leaf)
	if l == "index.html" || l == "index.htm" || l == "" {
		*leaf = "index.html"
		return true
	}
	return false
}

// pageParamNames lists common query parameter names used for pagination.
// When a page URL has only one of these params, we use a readable suffix
// instead of a hash (e.g. index_page_2.html instead of index__q-xxx.html).
var pageParamNames = map[string]bool{
	"page":     true,
	"Page":     true,
	"p":        true,
	"pg":       true,
	"pagenum":  true,
	"pageNum":  true,
	"PageNum":  true,
	"pageno":   true,
	"pageNo":   true,
	"PageNo":   true,
	"paged":    true,
	"page_num": true,
}

// offsetParamNames lists common offset/limit-style pagination params.
var offsetParamNames = map[string]bool{
	"offset":    true,
	"start":     true,
	"skip":      true,
	"from":      true,
	"after":     true,
	"limit":     true,
	"size":      true,
	"count":     true,
	"per_page":  true,
	"perPage":   true,
	"page_size": true,
	"pageSize":  true,
}

// cursorParamNames lists common cursor/token/seek-style pagination params.
var cursorParamNames = map[string]bool{
	"cursor":       true,
	"next":         true,
	"after_id":     true,
	"afterId":      true,
	"pageToken":    true,
	"page_token":   true,
	"token":        true,
	"continuation": true,
	"since":        true,
	"before":       true,
	"until":        true,
	"from":         true,
	"after":        true,
}

// applyPageQuerySuffix appends a query parameter suffix to a page filename.
// For simple pagination URLs with only page-related params, uses readable
// names (e.g. ?Page=2 → index_page_2.html). For offset/limit pairs, uses
// offset-based names (e.g. ?offset=50&limit=25 → index_offset_50_25.html).
// For cursor/token-style params, uses the param name and a short hash.
// For complex query strings, falls back to a hash-based suffix.
func applyPageQuerySuffix(filename, query string) string {
	values, err := url.ParseQuery(query)
	if err != nil {
		return applyQueryHash(filename, query)
	}

	ext := path.Ext(filename)
	base := filename[:len(filename)-len(ext)]

	// Case 1: single page-number param → index_page_N.html
	// All page-like param names (page, Page, p, pg, pageNum, etc.) are
	// normalized to "_page_" for readability. In practice, a single site
	// will use one consistent param name, so collisions are unlikely.
	if len(values) == 1 {
		for key := range values {
			lowerKey := strings.ToLower(key)
			if pageParamNames[lowerKey] || pageParamNames[key] {
				vals := values[key]
				if len(vals) == 1 && vals[0] != "" {
					return base + "_page_" + vals[0] + ext
				}
			}
		}
	}

	// Case 2: offset+limit pair → index_offset_N_M.html
	// Handles common combinations: offset/limit, start/size, skip/count, etc.
	if len(values) == 2 {
		var offsetVal, limitVal string
		var hasOffset, hasLimit bool

		for key := range values {
			lowerKey := strings.ToLower(key)
			vals := values[key]
			if len(vals) != 1 || vals[0] == "" {
				continue
			}
			if isOffsetParam(lowerKey) {
				offsetVal = vals[0]
				hasOffset = true
			} else if isLimitParam(lowerKey) {
				limitVal = vals[0]
				hasLimit = true
			}
		}

		if hasOffset && hasLimit {
			return base + "_offset_" + offsetVal + "_" + limitVal + ext
		}
	}

	// Case 3: single cursor/token-style param → index_{param}_{shortHash}.html
	if len(values) == 1 {
		for key := range values {
			lowerKey := strings.ToLower(key)
			if cursorParamNames[lowerKey] || cursorParamNames[key] {
				vals := values[key]
				if len(vals) == 1 && vals[0] != "" {
					shortHash := sha256Str(vals[0], 6)
					return base + "_" + lowerKey + "_" + shortHash + ext
				}
			}
		}
	}

	// Fallback: hash-based suffix for everything else.
	return applyQueryHash(filename, query)
}

// isOffsetParam returns true if the query param name is an offset-style
// pagination parameter (skip, offset, start, from, after, etc.).
func isOffsetParam(name string) bool {
	return name == "offset" || name == "start" || name == "skip" ||
		name == "from" || name == "after" || name == "after_id"
}

// isLimitParam returns true if the query param name is a limit-style
// pagination parameter (limit, size, count, per_page, page_size, etc.).
func isLimitParam(name string) bool {
	return name == "limit" || name == "size" || name == "count" ||
		name == "per_page" || name == "perpage" || name == "page_size" ||
		name == "pagesize" || name == "pageSize"
}

// applyQueryHash appends a query parameter hash to a filename.
// "style.css?v=2" → "style__q-1a2b3c.css"
func applyQueryHash(filename, query string) string {
	hash := sha256Str(query, 6)
	ext := path.Ext(filename)
	base := filename[:len(filename)-len(ext)]
	return base + "__q-" + hash + ext
}

// sha256Str returns the first n hex characters of SHA-256 hash.
func sha256Str(s string, n int) string {
	h := sha256.Sum256([]byte(s))
	hex := fmt.Sprintf("%x", h)
	if n > len(hex) {
		n = len(hex)
	}
	return hex[:n]
}

// Rel computes a relative path from fromDir to toFile.
// fromDir is the directory path of the source file (or the file path itself,
// in which case the file component is stripped).
// Both paths use forward slash as separator.
func Rel(fromDir, toFile string) string {
	// Strip filename from fromDir if it has an extension (file path, not dir).
	if dotIdx := strings.LastIndex(path.Base(fromDir), "."); dotIdx >= 0 {
		fromDir = path.Dir(fromDir)
	}

	fromParts := splitRelPath(fromDir)
	toParts := splitRelPath(toFile)

	// Find common prefix.
	i := 0
	for i < len(fromParts) && i < len(toParts) && fromParts[i] == toParts[i] {
		i++
	}

	// Build result: go up for remaining fromParts, then down into toParts.
	var parts []string
	for j := i; j < len(fromParts); j++ {
		parts = append(parts, "..")
	}
	for j := i; j < len(toParts); j++ {
		parts = append(parts, toParts[j])
	}

	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

// splitRelPath splits a relative path into components.
func splitRelPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// SameSite checks whether u belongs to the same site as seed.
// If allowSub is true, subdomains of seed are considered in-scope.
func SameSite(seed, u *url.URL, allowSub bool) bool {
	seedHost := strings.ToLower(seed.Host)
	uHost := strings.ToLower(u.Host)

	if seedHost == uHost {
		return true
	}

	if !allowSub {
		return false
	}

	return strings.HasSuffix(uHost, "."+seedHost)
}

// SameRegistrableDomain checks whether seed and u share the same
// registrable domain (eTLD+1), e.g., "apple.com" matches "store.apple.com".
func SameRegistrableDomain(seed, u *url.URL) bool {
	seedDomain, err := publicsuffix.EffectiveTLDPlusOne(seed.Host)
	if err != nil {
		return false
	}
	uDomain, err := publicsuffix.EffectiveTLDPlusOne(u.Host)
	if err != nil {
		return false
	}
	return strings.EqualFold(seedDomain, uDomain)
}

// InScope checks whether a URL is within the configured crawl scope.
type ScopeConfig struct {
	AllowSubdomains bool
	ScopePrefix     string
	ScopeAnchor     string
	ExcludePrefixes []string
}

func InScope(seed, u *url.URL, cfg ScopeConfig) bool {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}

	if !SameSite(seed, u, cfg.AllowSubdomains) {
		return false
	}

	// Check scope anchor first (if specified, URL must have matching fragment)
	if cfg.ScopeAnchor != "" {
		if u.Fragment == "" {
			return false
		}
		if !strings.EqualFold(u.Fragment, cfg.ScopeAnchor) {
			return false
		}
	}

	if cfg.ScopePrefix != "" {
		matched := false
		if matchesScopePrefixWithList(u.Path, cfg.ScopePrefix) {
			matched = true
		}
		if !matched && u.Fragment != "" {
			cleanPrefix := strings.Trim(cfg.ScopePrefix, "/")
			parts := strings.Split(cleanPrefix, "/")
			if len(parts) > 0 {
				lastSegment := strings.ToLower(parts[len(parts)-1])
				if strings.ToLower(u.Fragment) == lastSegment {
					matched = true
				}
			}
		}
		if !matched {
			return false
		}
	}

	for _, excl := range cfg.ExcludePrefixes {
		if strings.HasPrefix(u.Path, excl) {
			return false
		}
	}

	return true
}

func matchesScopePrefix(path, prefix string) bool {
	// Normalize: strip trailing slashes to avoid double-slash issues
	// when prefix already ends with "/" (e.g. "/about/" + "/" = "/about//").
	cleanPrefix := strings.TrimRight(prefix, "/")
	cleanPath := strings.TrimRight(path, "/")

	if cleanPath == cleanPrefix {
		return true
	}
	if cleanPrefix == "" {
		// Empty prefix matches everything.
		return true
	}
	return strings.HasPrefix(cleanPath, cleanPrefix+"/")
}

// matchesScopePrefixWithList checks whether a path matches the scope prefix,
// also considering bi-directional "-list" suffix matching:
//   - prefix "/biographies-list" matches "/biographies/xxx" (strip -list)
//   - prefix "/biographies" matches "/biographies-list/..." (add -list)
//
// This handles the common pattern where a list page lives at /prefix-list/
// and detail pages live at /prefix/<slug>.
func matchesScopePrefixWithList(path, prefix string) bool {
	// Direct match.
	if matchesScopePrefix(path, prefix) {
		return true
	}

	cleanPrefix := strings.TrimRight(prefix, "/")

	// Direction 1: prefix ends with "-list" → try without "-list".
	// e.g. prefix="/biographies-list" matches "/biographies/xxx".
	if strings.HasSuffix(cleanPrefix, "-list") {
		basePrefix := strings.TrimSuffix(cleanPrefix, "-list")
		if matchesScopePrefix(path, basePrefix) {
			return true
		}
	}

	// Direction 2: prefix does NOT end with "-list" → try adding "-list".
	// e.g. prefix="/biographies" matches "/biographies-list/page/2/".
	if !strings.HasSuffix(cleanPrefix, "-list") {
		listPrefix := cleanPrefix + "-list"
		if matchesScopePrefix(path, listPrefix) {
			return true
		}
	}

	return false
}

// PageKey returns a deterministic key for a page URL used for deduplication.
// It combines the normalized URL and the expected local path.
func PageKey(seedHost, pageURL string) string {
	return LocalPath(seedHost, pageURL, KindPage)
}

// AssetKey returns a deterministic key for an asset URL.
func AssetKey(assetURL string) string {
	return LocalPath("", assetURL, KindAsset)
}

// PathExt returns the lowercased file extension of the URL's path component,
// including the leading dot (e.g., ".css", ".png"). Ignores query strings.
func PathExt(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	ext := path.Ext(u.Path)
	return strings.ToLower(ext)
}
