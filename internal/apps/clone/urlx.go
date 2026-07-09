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

// Kind classifies a URL as a page or asset.
type URLKind int

const (
	KindPage  URLKind = iota // HTML page that needs rendering and link rewriting.
	KindAsset                // Static resource (CSS, image, font, media).
)

// reservedPrefix is the directory where all downloaded assets reside.
const reservedPrefix = "_wukong"

// binaryExts lists extensions that indicate binary/document (non-HTML) content.
var binaryExts = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".xlsx": true,
	".zip": true, ".tar": true, ".gz": true, ".bz2": true,
	".7z": true, ".rar": true, ".exe": true, ".dmg": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".ico": true, ".webp": true, ".avif": true,
	".css": true, ".js": true, ".json": true, ".xml": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true,
	".eot": true, ".mp3": true, ".mp4": true, ".webm": true,
	".ogg": true, ".wav": true, ".flac": true, ".avi": true,
	".mov": true, ".m4v": true, ".m4a": true,
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
	case KindAsset:
		return localAssetPath(u)
	default:
		return localPagePath(seedHost, u)
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
	if !collapseIndex(&leaf) {
		// For non-index pages, apply query hash to filename.
		if u.RawQuery != "" {
			leaf = applyQueryHash(leaf, u.RawQuery)
		}
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
	ExcludePrefixes []string
}

func InScope(seed, u *url.URL, cfg ScopeConfig) bool {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}

	if !SameSite(seed, u, cfg.AllowSubdomains) {
		return false
	}

	if cfg.ScopePrefix != "" {
		if !matchesScopePrefix(u.Path, cfg.ScopePrefix) {
			basePrefix := cfg.ScopePrefix
			if strings.HasSuffix(basePrefix, "-list") {
				basePrefix = strings.TrimSuffix(basePrefix, "-list")
				if !matchesScopePrefix(u.Path, basePrefix) {
					return false
				}
			} else {
				return false
			}
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
	if path == prefix {
		return true
	}
	if strings.HasPrefix(path, prefix+"/") {
		return true
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
