package rodbackend

import "testing"

func TestIsAPIResponse(t *testing.T) {
	cases := []struct {
		name, rt, mt, url string
		want              bool
	}{
		{"JSON via XHR", "XHR", "application/json", "https://api.example.com/data", true},
		{"JSON via Fetch", "Fetch", "application/json", "https://example.com/api/list", true},
		{"JSON with API path", "Document", "application/json", "https://example.com/api/users", true},
		{"XHR with API path", "XHR", "text/html", "https://example.com/api/users", true},
		{"JSON file URL", "Other", "application/json", "https://example.com/data.json", true},
		{"XML via XHR", "XHR", "application/xml", "https://example.com/feed", true},
		{"Static CSS", "Stylesheet", "text/css", "https://example.com/style.css", false},
		{"Static JS", "Script", "application/javascript", "https://example.com/app.js", false},
		{"Image", "Image", "image/png", "https://example.com/logo.png", false},
		{"HTML document", "Document", "text/html", "https://example.com/page", false},
		{"Empty", "", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isAPIResponse(c.rt, c.mt, c.url)
			if got != c.want {
				t.Errorf("isAPIResponse(%q, %q, %q) = %v; want %v",
					c.rt, c.mt, c.url, got, c.want)
			}
		})
	}
}

func TestDetectPaginationKind(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://api.example.com/list?page=2", "query_param"},
		{"https://api.example.com/list?p=3", "query_param"},
		{"https://api.example.com/list?pageno=5", "query_param"},
		{"https://api.example.com/list?page_num=10", "query_param"},
		{"https://api.example.com/list?offset=50&limit=25", "offset_limit"},
		{"https://api.example.com/list?skip=0&take=10", "offset_limit"},
		{"https://api.example.com/list?cursor=abc123", "cursor"},
		{"https://api.example.com/list?after=xyz", "cursor"},
		{"https://api.example.com/list?pageToken=token", "cursor"},
		{"https://example.com/blog/page/2/", "path_based"},
		{"https://example.com/blog/page/10/", "path_based"},
		{"https://api.example.com/list", "none"},
		{"https://example.com/page/about", "none"}, // "about" is not numeric
		{"https://example.com/no/pagination", "none"},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			got := detectPaginationKind(c.url)
			if got != c.want {
				t.Errorf("detectPaginationKind(%q) = %q; want %q",
					c.url, got, c.want)
			}
		})
	}
}

func TestDiscoverAPIs(t *testing.T) {
	tracked := []networkResponseInfo{
		// Main document - should be excluded.
		{URL: "https://example.com/", MimeType: "text/html", Status: 200, ResourceType: "Document"},
		// JSON API via XHR.
		{URL: "https://api.example.com/users?page=2", MimeType: "application/json", Status: 200, ResourceType: "XHR"},
		// JSON API via Fetch with GraphQL path.
		{URL: "https://example.com/graphql", MimeType: "application/json", Status: 200, ResourceType: "Fetch"},
		// Failed response (404) - should be excluded.
		{URL: "https://api.example.com/missing", MimeType: "application/json", Status: 404, ResourceType: "XHR"},
		// CSS asset - should be excluded.
		{URL: "https://example.com/style.css", MimeType: "text/css", Status: 200, ResourceType: "Stylesheet"},
		// Duplicate URL - should be deduplicated.
		{URL: "https://api.example.com/users?page=2", MimeType: "application/json", Status: 200, ResourceType: "XHR"},
	}

	apis := discoverAPIs(tracked, "https://example.com/")
	if len(apis) != 2 {
		t.Fatalf("expected 2 APIs, got %d", len(apis))
	}

	// First API should be the users endpoint with query_param pagination.
	if apis[0].URL != "https://api.example.com/users?page=2" {
		t.Errorf("API[0] URL = %q", apis[0].URL)
	}
	if apis[0].PaginationKind != "query_param" {
		t.Errorf("API[0] PaginationKind = %q; want query_param", apis[0].PaginationKind)
	}

	// Second API should be the graphql endpoint.
	if apis[1].URL != "https://example.com/graphql" {
		t.Errorf("API[1] URL = %q", apis[1].URL)
	}
}

func TestDiscoverAPIs_Empty(t *testing.T) {
	if apis := discoverAPIs(nil, "https://example.com/"); apis != nil {
		t.Errorf("expected nil for empty input, got %v", apis)
	}
}

func TestGeneratePaginationURLs_QueryParam(t *testing.T) {
	base := "https://api.example.com/list?page=2"
	urls := GeneratePaginationURLs(base, "query_param", 3)
	if len(urls) != 3 {
		t.Fatalf("expected 3 URLs, got %d", len(urls))
	}
	// Should generate page=3, page=4, page=5.
	expected := []string{
		"https://api.example.com/list?page=3",
		"https://api.example.com/list?page=4",
		"https://api.example.com/list?page=5",
	}
	for i, u := range urls {
		if u != expected[i] {
			t.Errorf("URL[%d] = %q; want %q", i, u, expected[i])
		}
	}
}

func TestGeneratePaginationURLs_OffsetLimit(t *testing.T) {
	base := "https://api.example.com/list?offset=0&limit=25"
	urls := GeneratePaginationURLs(base, "offset_limit", 3)
	if len(urls) != 3 {
		t.Fatalf("expected 3 URLs, got %d", len(urls))
	}
	// Should generate offset=25, offset=50, offset=75.
	if urls[0] != "https://api.example.com/list?limit=25&offset=25" {
		t.Errorf("URL[0] = %q", urls[0])
	}
}

func TestGeneratePaginationURLs_None(t *testing.T) {
	urls := GeneratePaginationURLs("https://example.com/api", "none", 5)
	if urls != nil {
		t.Errorf("expected nil for none pagination, got %v", urls)
	}
}

func TestIntToStr(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{100, "100"},
	}
	for _, c := range cases {
		if got := intToStr(c.in); got != c.want {
			t.Errorf("intToStr(%d) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestStrToInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"42", 42},
		{"100", 100},
		{"abc", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := strToInt(c.in); got != c.want {
			t.Errorf("strToInt(%q) = %d; want %d", c.in, got, c.want)
		}
	}
}
