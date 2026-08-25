// Package clone provides website cloning functionality.
//
// archive_fallback.go: Wayback Machine (web.archive.org) fallback
// for dead links. When a page or asset cannot be fetched from the
// live site (404, 500, DNS failure, connection timeout), this
// module queries the Internet Archive's Availability API and, if
// an archived snapshot exists, fetches the archived content.
// Inspired by insane-search's archive.org integration.
package clone

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/km269/wukong/pkg/logutil"

	"golang.org/x/net/html"
)

// ArchiveFallback queries the Wayback Machine and fetches
// archived snapshots when live fetching fails.
type ArchiveFallback struct {
	client  *http.Client
	enabled bool
}

// NewArchiveFallback creates an ArchiveFallback. When enabled is
// false, all methods are no-ops returning false.
func NewArchiveFallback(enabled bool) *ArchiveFallback {
	return &ArchiveFallback{
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
		enabled: enabled,
	}
}

// Enabled reports whether archive fallback is active.
func (a *ArchiveFallback) Enabled() bool {
	return a != nil && a.enabled
}

// waybackAvailableResponse models the Wayback Machine
// Availability API response.
type waybackAvailableResponse struct {
	ArchivedSnapshots struct {
		Closest *struct {
			Available bool   `json:"available"`
			URL       string `json:"url"`
			Timestamp string `json:"timestamp"`
			Status    string `json:"status"`
		} `json:"closest"`
	} `json:"archived_snapshots"`
}

// FindSnapshot queries the Wayback Machine Availability API for
// the closest archived snapshot of the given URL. Returns the
// snapshot URL (with id_ suffix for raw content) and timestamp,
// or false if no snapshot is available.
func (a *ArchiveFallback) FindSnapshot(
	ctx context.Context, pageURL string,
) (snapshotURL, timestamp string, ok bool) {
	if !a.Enabled() {
		return "", "", false
	}

	apiURL := fmt.Sprintf(
		"https://archive.org/wayback/available?url=%s",
		url.QueryEscape(pageURL),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", "", false
	}
	req.Header.Set("User-Agent", "Wukong-Cloner/1.0 (archive-fallback)")

	resp, err := a.client.Do(req)
	if err != nil {
		logutil.Debug("wayback availability query failed",
			"url", pageURL, "err", err.Error())
		return "", "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", "", false
	}

	var avail waybackAvailableResponse
	if err := json.Unmarshal(body, &avail); err != nil {
		return "", "", false
	}
	snap := avail.ArchivedSnapshots.Closest
	if snap == nil || !snap.Available {
		return "", "", false
	}

	// Convert the snapshot URL to the "id_" variant to get the
	// original raw content without the Wayback Machine toolbar.
	// Example: http://web.archive.org/web/20210101120000/https://example.com
	//       -> http://web.archive.org/web/20210101120000id_/https://example.com
	rawURL := snap.URL
	// The URL format is: .../web/<timestamp>/<original_url>
	// Insert "id_" after the timestamp.
	parts := strings.SplitN(rawURL, "/web/", 2)
	if len(parts) == 2 {
		rest := parts[1]
		slashIdx := strings.Index(rest, "/")
		if slashIdx > 0 {
			timestamp = rest[:slashIdx]
			rawURL = parts[0] + "/web/" + timestamp + "id_/" + rest[slashIdx+1:]
		}
	}

	return rawURL, timestamp, true
}

// FetchArchivedPage fetches an archived page snapshot and returns
// its HTML content. This is the last-resort fallback when both
// browser rendering and HTTP fetching fail.
func (a *ArchiveFallback) FetchArchivedPage(
	ctx context.Context, pageURL string,
) (*httpPageResult, error) {
	if !a.Enabled() {
		return nil, fmt.Errorf("archive fallback disabled")
	}

	snapshotURL, timestamp, ok := a.FindSnapshot(ctx, pageURL)
	if !ok {
		return nil, fmt.Errorf("no archived snapshot for %s", pageURL)
	}

	logutil.Info("archive fallback: fetching snapshot",
		"url", pageURL,
		"timestamp", timestamp)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, snapshotURL, nil)
	if err != nil {
		return nil, fmt.Errorf("archive: create request: %w", err)
	}
	req.Header.Set("User-Agent", "Wukong-Cloner/1.0 (archive-fallback)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("archive: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archive: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16 MB cap
	if err != nil {
		return nil, fmt.Errorf("archive: read body: %w", err)
	}

	htmlStr := string(body)
	// Ensure the content looks like HTML.
	if !strings.Contains(htmlStr, "<") {
		return nil, fmt.Errorf("archive: content is not HTML")
	}

	// Strip Wayback Machine toolbar/wrapper if present (the id_
	// suffix usually avoids this, but just in case).
	htmlStr = stripWaybackToolbar(htmlStr)

	logutil.Info("archive fallback: succeeded",
		"url", pageURL,
		"timestamp", timestamp,
		"size", len(htmlStr))

	return &httpPageResult{
		HTML:        htmlStr,
		ContentType: resp.Header.Get("Content-Type"),
		StatusCode:  resp.StatusCode,
	}, nil
}

// FetchArchivedAsset fetches an archived binary asset (image, CSS,
// JS, etc.) and returns the raw bytes. Used when asset downloads
// fail due to dead links.
func (a *ArchiveFallback) FetchArchivedAsset(
	ctx context.Context, assetURL string,
) ([]byte, string, error) {
	if !a.Enabled() {
		return nil, "", fmt.Errorf("archive fallback disabled")
	}

	snapshotURL, _, ok := a.FindSnapshot(ctx, assetURL)
	if !ok {
		return nil, "", fmt.Errorf("no archived snapshot for %s", assetURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, snapshotURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("archive asset: create request: %w", err)
	}
	req.Header.Set("User-Agent", "Wukong-Cloner/1.0 (archive-fallback)")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("archive asset: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("archive asset: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, "", fmt.Errorf("archive asset: read body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	logutil.Info("archive asset fallback: succeeded",
		"url", assetURL, "size", len(body))

	return body, contentType, nil
}

// stripWaybackToolbar removes the Wayback Machine toolbar and
// rewrite scripts that get injected into archived pages. The id_
// URL suffix usually avoids this, but we strip defensively.
func stripWaybackToolbar(htmlStr string) string {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		// If parsing fails, return as-is — the content may still
		// be usable even with toolbar markup.
		return htmlStr
	}

	var cleaned strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// Skip Wayback-injected elements.
			if n.Data == "wb_extract" ||
				(n.Data == "script" && hasWaybackAttr(n)) {
				// Skip this node and its children.
				return
			}
		}
		if n.Type == html.TextNode {
			cleaned.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			// Re-emit closing tags for non-void elements.
			switch n.Data {
			case "meta", "link", "br", "hr", "img", "input":
				// void elements — no closing tag.
			default:
				// The text content is already emitted via walk;
				// closing tags are implicit in text reconstruction.
			}
		}
	}
	walk(doc)

	result := cleaned.String()
	if result == "" {
		return htmlStr
	}
	return result
}

// hasWaybackAttr checks if a script node has Wayback-specific
// attributes (src containing web.archive.org).
func hasWaybackAttr(n *html.Node) bool {
	for _, attr := range n.Attr {
		if attr.Key == "src" && strings.Contains(attr.Val, "web.archive.org") {
			return true
		}
	}
	return false
}
