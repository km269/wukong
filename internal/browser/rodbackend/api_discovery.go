// Package rodbackend provides the rod-based browser backend.
//
// api_discovery.go: Hidden API endpoint discovery from network
// responses intercepted during page rendering. Detects XHR/fetch
// requests returning structured data (JSON/XML/GraphQL) and
// classifies pagination patterns for follow-up crawling.
package rodbackend

import (
	"net/url"
	"strings"

	"github.com/km269/wukong/internal/browser/types"
)

// networkResponseInfo is the minimal data extracted from a CDP
// NetworkResponseReceived event, used for API discovery.
type networkResponseInfo struct {
	URL          string
	MimeType     string
	Status       int
	ResourceType string // CDP resource type: XHR, Fetch, Document, etc.
}

// discoverAPIs filters tracked network responses for hidden API
// endpoints. An API endpoint is a XHR/Fetch request that returns
// structured data (JSON, XML, GraphQL) — not a static asset,
// stylesheet, script, or the main document.
//
// The main document URL is excluded. Deduplication is performed
// by URL. Pagination patterns are detected and annotated.
func discoverAPIs(
	tracked []networkResponseInfo, mainDocURL string,
) []types.DiscoveredAPI {
	if len(tracked) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(tracked))
	var apis []types.DiscoveredAPI

	for _, t := range tracked {
		// Skip the main document.
		if t.URL == mainDocURL || t.URL == "" {
			continue
		}
		// Only successful responses.
		if t.Status < 200 || t.Status >= 300 {
			continue
		}
		// Deduplicate.
		if seen[t.URL] {
			continue
		}

		if !isAPIResponse(t.ResourceType, t.MimeType, t.URL) {
			continue
		}

		seen[t.URL] = true
		apis = append(apis, types.DiscoveredAPI{
			URL:            t.URL,
			ContentType:    t.MimeType,
			StatusCode:     t.Status,
			ResourceType:   t.ResourceType,
			PaginationKind: detectPaginationKind(t.URL),
		})
	}

	return apis
}

// isAPIResponse reports whether a network response looks like an
// API call (structured data via XHR/Fetch), not a static asset.
func isAPIResponse(resourceType, mimeType, rawURL string) bool {
	mt := strings.ToLower(mimeType)
	rt := strings.ToLower(resourceType)

	// Content type check: JSON, XML, or GraphQL.
	isStructuredCT :=
		strings.Contains(mt, "json") ||
			strings.Contains(mt, "xml") ||
			strings.Contains(mt, "graphql")

	// Resource type check: XHR or Fetch.
	isXHR := rt == "xhr" || rt == "fetch"

	// URL-based heuristics for API endpoints.
	lowURL := strings.ToLower(rawURL)
	hasAPIPath :=
		strings.Contains(lowURL, "/api/") ||
			strings.Contains(lowURL, "/graphql") ||
			strings.Contains(lowURL, "/rest/") ||
			strings.Contains(lowURL, "/v1/") ||
			strings.Contains(lowURL, "/v2/") ||
			strings.HasSuffix(lowURL, ".json") ||
			strings.HasSuffix(lowURL, ".xml")

	// An API response is:
	// 1. A structured content type (JSON/XML/GraphQL) via XHR/Fetch, OR
	// 2. A structured content type with an API-like URL path, OR
	// 3. An XHR/Fetch request to an API-like URL path
	if isStructuredCT && isXHR {
		return true
	}
	if isStructuredCT && hasAPIPath {
		return true
	}
	if isXHR && hasAPIPath {
		return true
	}
	return false
}

// detectPaginationKind classifies the pagination style of an API URL
// based on its query parameters and path structure. Returns "none"
// if no pagination pattern is detected.
//
// Supported patterns:
//   - query_param: ?page=2, ?p=2, ?pagenum=2, ?pageNo=2, ?paged=2
//   - offset_limit: ?offset=50&limit=25, ?skip=0&take=10
//   - cursor: ?cursor=abc, ?after=xyz, ?next=token, ?pageToken=val
//   - path_based: /page/2/, /p/2/, /items/2/
//   - none: no pagination detected
func detectPaginationKind(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "none"
	}

	q := u.Query()

	// Query parameter-based pagination (common param names).
	queryPageParams := []string{
		"page", "p", "pg", "pagenum", "pageno", "paged", "page_num",
	}
	for _, param := range queryPageParams {
		if q.Has(param) {
			return "query_param"
		}
	}

	// Offset/Limit pagination.
	if q.Has("offset") || q.Has("limit") ||
		q.Has("skip") || q.Has("take") ||
		q.Has("start") || q.Has("count") {
		return "offset_limit"
	}

	// Cursor/Seek/Token pagination.
	// Query param names are matched case-insensitively since APIs
	// use varying casing (pageToken vs pagetoken vs page_token).
	cursorParams := map[string]bool{
		"cursor": true, "after": true, "before": true,
		"next": true, "prev": true,
		"pagetoken": true, "page_token": true,
		"seek": true, "keyset": true, "continuation": true,
	}
	for param := range q {
		if cursorParams[strings.ToLower(param)] {
			return "cursor"
		}
	}

	// Path-based pagination: /page/2/, /p/2/, /items/2/
	pathParts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range pathParts {
		lowPart := strings.ToLower(part)
		if (lowPart == "page" || lowPart == "p") && i+1 < len(pathParts) {
			next := pathParts[i+1]
			if isNumeric(next) {
				return "path_based"
			}
		}
	}

	return "none"
}

// isNumeric reports whether s is a positive integer.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// GeneratePaginationURLs produces follow-up API URLs for a discovered
// paginated endpoint. Given the base URL and detected pagination kind,
// it generates up to maxPages additional URLs by incrementing the
// pagination parameter.
//
// This enables crawling all pages of an API endpoint discovered
// during page rendering.
func GeneratePaginationURLs(
	baseURL, paginationKind string, maxPages int,
) []string {
	if paginationKind == "none" || maxPages <= 0 {
		return nil
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}

	var result []string
	switch paginationKind {
	case "query_param":
		// Find the page parameter and increment it.
		q := u.Query()
		pageParam := ""
		for _, param := range []string{
			"page", "p", "pg", "pagenum", "pageno", "paged", "page_num",
		} {
			if q.Has(param) {
				pageParam = param
				break
			}
		}
		if pageParam == "" {
			return nil
		}
		currentPage := 1
		if v := q.Get(pageParam); isNumeric(v) {
			currentPage = 0
			for _, r := range v {
				currentPage = currentPage*10 + int(r-'0')
			}
		}
		for i := currentPage + 1; i <= currentPage+maxPages; i++ {
			q.Set(pageParam, intToStr(i))
			u2 := *u
			u2.RawQuery = q.Encode()
			result = append(result, u2.String())
		}

	case "offset_limit":
		q := u.Query()
		limit := 25
		if v := q.Get("limit"); isNumeric(v) {
			limit = strToInt(v)
		} else if v := q.Get("take"); isNumeric(v) {
			limit = strToInt(v)
		}
		currentOffset := 0
		if v := q.Get("offset"); isNumeric(v) {
			currentOffset = strToInt(v)
		} else if v := q.Get("skip"); isNumeric(v) {
			currentOffset = strToInt(v)
		}
		for i := 1; i <= maxPages; i++ {
			newOffset := currentOffset + limit*i
			if q.Has("offset") {
				q.Set("offset", intToStr(newOffset))
			} else if q.Has("skip") {
				q.Set("skip", intToStr(newOffset))
			} else {
				q.Set("offset", intToStr(newOffset))
			}
			u2 := *u
			u2.RawQuery = q.Encode()
			result = append(result, u2.String())
		}
	}

	return result
}

// intToStr converts a non-negative int to its decimal string.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// strToInt converts a decimal string to int. Returns 0 on error.
func strToInt(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
