package clone

import (
	"net/url"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		ref     string
		want    string
		wantErr bool
	}{
		{
			name: "absolute same origin",
			base: "https://example.com/",
			ref:  "https://example.com/page",
			want: "https://example.com/page",
		},
		{
			name: "relative path",
			base: "https://example.com/dir/",
			ref:  "other.html",
			want: "https://example.com/dir/other.html",
		},
		{
			name: "remove fragment",
			base: "https://example.com/",
			ref:  "https://example.com/page#section",
			want: "https://example.com/page",
		},
		{
			name: "remove default port 80",
			base: "https://example.com/",
			ref:  "http://example.com:80/page",
			want: "http://example.com/page",
		},
		{
			name: "remove default port 443",
			base: "https://example.com/",
			ref:  "https://example.com:443/page",
			want: "https://example.com/page",
		},
		{
			name:    "reject javascript",
			base:    "https://example.com/",
			ref:     "javascript:void(0)",
			wantErr: true,
		},
		{
			name:    "reject mailto",
			base:    "https://example.com/",
			ref:     "mailto:test@example.com",
			wantErr: true,
		},
		{
			name:    "reject data",
			base:    "https://example.com/",
			ref:     "data:text/plain,hello",
			wantErr: true,
		},
		{
			name: "clean path dots",
			base: "https://example.com/dir/",
			ref:  "../page",
			want: "https://example.com/page",
		},
		{
			name: "trailing slash preserve",
			base: "https://example.com/",
			ref:  "https://example.com/docs/",
			want: "https://example.com/docs/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.base, tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Normalize() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLocalPath_Page(t *testing.T) {
	seedHost := "example.com"

	tests := []struct {
		url  string
		want string
	}{
		{"https://example.com/", "index.html"},
		{"https://example.com/about/", "about/index.html"},
		{"https://example.com/about/team.html", "about/team.html"},
		{"https://example.com/about/team", "about/team.html"},
		{"https://sub.example.com/page", "sub.example.com/page.html"},
		{"https://sub.example.com/page/", "sub.example.com/page/index.html"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := LocalPath(seedHost, tt.url, KindPage)
			// Use forward slash for cross-platform testing.
			got = strings.ReplaceAll(got, "\\", "/")
			if got != tt.want {
				t.Errorf("LocalPath(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestLocalPath_Asset(t *testing.T) {
	tests := []struct {
		url        string
		wantPrefix string
	}{
		{
			url:        "https://example.com/css/style.css",
			wantPrefix: "_wukong/example.com/css/style.css",
		},
		{
			url:        "https://cdn.example.com/img/logo.png",
			wantPrefix: "_wukong/cdn.example.com/img/logo.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := LocalPath("", tt.url, KindAsset)
			got = strings.ReplaceAll(got, "\\", "/")
			if got != tt.wantPrefix {
				t.Errorf("LocalPath(%q) = %q, want %q", tt.url, got, tt.wantPrefix)
			}
		})
	}
}

func TestLikelyPage(t *testing.T) {
	pages := []string{"/", "/about", "/docs/", "/index.html"}
	assets := []string{"/style.css", "/img.png", "/doc.pdf", "/data.json"}

	for _, p := range pages {
		if !LikelyPage(p) {
			t.Errorf("LikelyPage(%q) = false, want true", p)
		}
	}
	for _, a := range assets {
		if LikelyPage(a) {
			t.Errorf("LikelyPage(%q) = true, want false", a)
		}
	}
}

func TestRel(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want string
	}{
		{"pages/index.html", "pages/about.html", "about.html"},
		{"pages/about/index.html", "pages/index.html", "../index.html"},
		{"pages/docs/index.html", "pages/docs/guide.html", "guide.html"},
		// from "pages/a/b/c.html" up to "pages/index.html" = 2 levels up
		{"pages/a/b/c.html", "pages/index.html", "../../index.html"},
		// same file: fromDir="pages", toFile="pages/index.html" → "index.html"
		{"pages/index.html", "pages/index.html", "index.html"},
	}

	for _, tt := range tests {
		t.Run(tt.from+"_"+tt.to, func(t *testing.T) {
			got := Rel(tt.from, tt.to)
			if got != tt.want {
				t.Errorf("Rel(%q, %q) = %q, want %q",
					tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestInScope_ListSuffix(t *testing.T) {
	tests := []struct {
		name     string
		seedURL  string
		checkURL string
		cfg      ScopeConfig
		want     bool
	}{
		{
			name:     "exact scope-prefix match",
			seedURL:  "https://www.state.gov/biographies-list",
			checkURL: "https://www.state.gov/biographies-list",
			cfg:      ScopeConfig{ScopePrefix: "/biographies-list"},
			want:     true,
		},
		{
			name:     "-list suffix matches base prefix",
			seedURL:  "https://www.state.gov/biographies-list",
			checkURL: "https://www.state.gov/biographies/john-doe",
			cfg:      ScopeConfig{ScopePrefix: "/biographies-list"},
			want:     true,
		},
		{
			name:     "-list suffix matches base prefix with trailing slash",
			seedURL:  "https://www.state.gov/biographies-list",
			checkURL: "https://www.state.gov/biographies/",
			cfg:      ScopeConfig{ScopePrefix: "/biographies-list"},
			want:     true,
		},
		{
			name:     "no -list suffix, exact match only",
			seedURL:  "https://www.state.gov/news",
			checkURL: "https://www.state.gov/news/latest",
			cfg:      ScopeConfig{ScopePrefix: "/news"},
			want:     true,
		},
		{
			name:     "no -list suffix, no match",
			seedURL:  "https://www.state.gov/news",
			checkURL: "https://www.state.gov/news2/latest",
			cfg:      ScopeConfig{ScopePrefix: "/news"},
			want:     false,
		},
		{
			name:     "-list suffix, no match for different base",
			seedURL:  "https://www.state.gov/biographies-list",
			checkURL: "https://www.state.gov/articles/john-doe",
			cfg:      ScopeConfig{ScopePrefix: "/biographies-list"},
			want:     false,
		},
		{
			name:     "base prefix matches -list path (bi-directional)",
			seedURL:  "https://www.state.gov/biographies",
			checkURL: "https://www.state.gov/biographies-list",
			cfg:      ScopeConfig{ScopePrefix: "/biographies"},
			want:     true,
		},
		{
			name:     "base prefix matches -list subpage (bi-directional)",
			seedURL:  "https://www.state.gov/biographies",
			checkURL: "https://www.state.gov/biographies-list/page/2/",
			cfg:      ScopeConfig{ScopePrefix: "/biographies"},
			want:     true,
		},
		{
			name:     "base prefix does not match unrelated -list path",
			seedURL:  "https://www.state.gov/news",
			checkURL: "https://www.state.gov/articles-list/page/2/",
			cfg:      ScopeConfig{ScopePrefix: "/news"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed, _ := url.Parse(tt.seedURL)
			check, _ := url.Parse(tt.checkURL)
			got := InScope(seed, check, tt.cfg)
			if got != tt.want {
				t.Errorf("InScope(%q, %q, %+v) = %v, want %v",
					tt.seedURL, tt.checkURL, tt.cfg, got, tt.want)
			}
		})
	}
}

func TestMatchesScopePrefix_TrailingSlash(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		prefix string
		want   bool
	}{
		{
			name:   "exact match without trailing slash",
			path:   "/news",
			prefix: "/news",
			want:   true,
		},
		{
			name:   "exact match with trailing slash on prefix",
			path:   "/news",
			prefix: "/news/",
			want:   true,
		},
		{
			name:   "exact match with trailing slash on path",
			path:   "/news/",
			prefix: "/news",
			want:   true,
		},
		{
			name:   "exact match with trailing slash on both",
			path:   "/news/",
			prefix: "/news/",
			want:   true,
		},
		{
			name:   "subpage match without trailing slash",
			path:   "/news/latest",
			prefix: "/news",
			want:   true,
		},
		{
			name:   "subpage match with trailing slash on prefix",
			path:   "/news/latest",
			prefix: "/news/",
			want:   true,
		},
		{
			name:   "subpage match with trailing slash on both",
			path:   "/news/latest/",
			prefix: "/news/",
			want:   true,
		},
		{
			name:   "no match for similar prefix without trailing slash",
			path:   "/news2/latest",
			prefix: "/news",
			want:   false,
		},
		{
			name:   "no match for similar prefix with trailing slash",
			path:   "/news2/latest",
			prefix: "/news/",
			want:   false,
		},
		{
			name:   "multi-level prefix with trailing slash",
			path:   "/About-DLA/Leaders/Biographies/John-Doe",
			prefix: "/About-DLA/Leaders/Biographies/",
			want:   true,
		},
		{
			name:   "multi-level exact match with trailing slash",
			path:   "/About-DLA/Leaders/Biographies/",
			prefix: "/About-DLA/Leaders/Biographies/",
			want:   true,
		},
		{
			name:   "root prefix matches everything",
			path:   "/anything/here",
			prefix: "/",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesScopePrefix(tt.path, tt.prefix)
			if got != tt.want {
				t.Errorf("matchesScopePrefix(%q, %q) = %v, want %v",
					tt.path, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestPagination_PageQueryParam(t *testing.T) {
	seedHost := "www.example.com"

	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "first page (no query)",
			url:  "https://www.example.com/list/",
			want: "list/index.html",
		},
		{
			name: "Page=2 (capital P)",
			url:  "https://www.example.com/list/?Page=2",
			want: "list/index_page_2.html",
		},
		{
			name: "page=3 (lowercase)",
			url:  "https://www.example.com/list/?page=3",
			want: "list/index_page_3.html",
		},
		{
			name: "p=5 (short form)",
			url:  "https://www.example.com/list/?p=5",
			want: "list/index_page_5.html",
		},
		{
			name: "pg=10 (another short form)",
			url:  "https://www.example.com/list/?pg=10",
			want: "list/index_page_10.html",
		},
		{
			name: "pageNum=7 (camelCase)",
			url:  "https://www.example.com/list/?pageNum=7",
			want: "list/index_page_7.html",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LocalPath(seedHost, tt.url, KindPage)
			got = strings.ReplaceAll(got, "\\", "/")
			if got != tt.want {
				t.Errorf("LocalPath(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestPagination_PathBased(t *testing.T) {
	seedHost := "www.example.com"

	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "first page",
			url:  "https://www.example.com/articles/",
			want: "articles/index.html",
		},
		{
			name: "page 2",
			url:  "https://www.example.com/articles/page/2/",
			want: "articles/page/2/index.html",
		},
		{
			name: "page 3",
			url:  "https://www.example.com/articles/page/3/",
			want: "articles/page/3/index.html",
		},
		{
			name: "page 10",
			url:  "https://www.example.com/articles/page/10/",
			want: "articles/page/10/index.html",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LocalPath(seedHost, tt.url, KindPage)
			got = strings.ReplaceAll(got, "\\", "/")
			if got != tt.want {
				t.Errorf("LocalPath(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestPagination_OffsetLimit(t *testing.T) {
	seedHost := "www.example.com"

	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "offset=0&limit=25",
			url:  "https://www.example.com/list/?offset=0&limit=25",
			want: "list/index_offset_0_25.html",
		},
		{
			name: "offset=50&limit=25",
			url:  "https://www.example.com/list/?offset=50&limit=25",
			want: "list/index_offset_50_25.html",
		},
		{
			name: "start=100&size=20",
			url:  "https://www.example.com/list/?start=100&size=20",
			want: "list/index_offset_100_20.html",
		},
		{
			name: "skip=50&count=25",
			url:  "https://www.example.com/list/?skip=50&count=25",
			want: "list/index_offset_50_25.html",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LocalPath(seedHost, tt.url, KindPage)
			got = strings.ReplaceAll(got, "\\", "/")
			if got != tt.want {
				t.Errorf("LocalPath(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestPagination_Cursor(t *testing.T) {
	seedHost := "www.example.com"

	tests := []struct {
		name      string
		url       string
		wantParam string
	}{
		{
			name:      "cursor param",
			url:       "https://www.example.com/list/?cursor=abc123def456",
			wantParam: "cursor",
		},
		{
			name:      "after_id param",
			url:       "https://www.example.com/list/?after_id=1000",
			wantParam: "after_id",
		},
		{
			name:      "next param",
			url:       "https://www.example.com/list/?next=eyJvZmZzZXQiOjEwMH0",
			wantParam: "next",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LocalPath(seedHost, tt.url, KindPage)
			got = strings.ReplaceAll(got, "\\", "/")

			expectedPrefix := "list/index_" + tt.wantParam + "_"
			if !strings.HasPrefix(got, expectedPrefix) {
				t.Errorf("LocalPath(%q) = %q, want prefix %q", tt.url, got, expectedPrefix)
			}
			if !strings.HasSuffix(got, ".html") {
				t.Errorf("LocalPath(%q) = %q, want .html suffix", tt.url, got)
			}
		})
	}
}

func TestPagination_Seek(t *testing.T) {
	seedHost := "www.example.com"

	tests := []struct {
		name      string
		url       string
		wantParam string
	}{
		{
			name:      "after timestamp",
			url:       "https://www.example.com/list/?after=2024-01-01T00:00:00Z",
			wantParam: "after",
		},
		{
			name:      "since timestamp",
			url:       "https://www.example.com/list/?since=1704067200",
			wantParam: "since",
		},
		{
			name:      "before timestamp",
			url:       "https://www.example.com/list/?before=2024-01-15",
			wantParam: "before",
		},
		{
			name:      "from timestamp",
			url:       "https://www.example.com/list/?from=2024-01-01",
			wantParam: "from",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LocalPath(seedHost, tt.url, KindPage)
			got = strings.ReplaceAll(got, "\\", "/")

			expectedPrefix := "list/index_" + tt.wantParam + "_"
			if !strings.HasPrefix(got, expectedPrefix) {
				t.Errorf("LocalPath(%q) = %q, want prefix %q", tt.url, got, expectedPrefix)
			}
			if !strings.HasSuffix(got, ".html") {
				t.Errorf("LocalPath(%q) = %q, want .html suffix", tt.url, got)
			}
		})
	}
}

func TestPagination_Token(t *testing.T) {
	seedHost := "www.example.com"

	tests := []struct {
		name      string
		url       string
		wantParam string
	}{
		{
			name:      "pageToken param",
			url:       "https://www.example.com/list/?pageToken=CjA2MTQwODIyRnN0YXRpY3",
			wantParam: "pagetoken",
		},
		{
			name:      "token param",
			url:       "https://www.example.com/list/?token=eyJhbGciOiJIUzI1NiJ9",
			wantParam: "token",
		},
		{
			name:      "continuation param",
			url:       "https://www.example.com/list/?continuation=abcdef123456",
			wantParam: "continuation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LocalPath(seedHost, tt.url, KindPage)
			got = strings.ReplaceAll(got, "\\", "/")

			expectedPrefix := "list/index_" + tt.wantParam + "_"
			if !strings.HasPrefix(got, expectedPrefix) {
				t.Errorf("LocalPath(%q) = %q, want prefix %q", tt.url, got, expectedPrefix)
			}
			if !strings.HasSuffix(got, ".html") {
				t.Errorf("LocalPath(%q) = %q, want .html suffix", tt.url, got)
			}
		})
	}
}

func TestPagination_Deduplication(t *testing.T) {
	seedHost := "www.example.com"

	tests := []struct {
		name     string
		url1     string
		url2     string
		wantSame bool
	}{
		{
			name:     "page 1 vs page 2 (query param)",
			url1:     "https://www.example.com/list/?Page=1",
			url2:     "https://www.example.com/list/?Page=2",
			wantSame: false,
		},
		{
			name:     "page 2 vs page 3 (path based)",
			url1:     "https://www.example.com/articles/page/2/",
			url2:     "https://www.example.com/articles/page/3/",
			wantSame: false,
		},
		{
			name:     "offset 0 vs offset 50",
			url1:     "https://www.example.com/list/?offset=0&limit=25",
			url2:     "https://www.example.com/list/?offset=50&limit=25",
			wantSame: false,
		},
		{
			name:     "cursor A vs cursor B",
			url1:     "https://www.example.com/list/?cursor=abc123",
			url2:     "https://www.example.com/list/?cursor=def456",
			wantSame: false,
		},
		{
			name:     "same page number, different param case (Page vs page)",
			url1:     "https://www.example.com/list/?Page=2",
			url2:     "https://www.example.com/list/?page=2",
			wantSame: true,
		},
		{
			name:     "same page number, different param name (page vs p)",
			url1:     "https://www.example.com/list/?page=2",
			url2:     "https://www.example.com/list/?p=2",
			wantSame: true,
		},
		{
			name:     "first page vs no query",
			url1:     "https://www.example.com/list/",
			url2:     "https://www.example.com/list/?Page=1",
			wantSame: false,
		},
		{
			name:     "complex query with sort",
			url1:     "https://www.example.com/list/?page=2&sort=date",
			url2:     "https://www.example.com/list/?page=2&sort=name",
			wantSame: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key1 := PageKey(seedHost, tt.url1)
			key2 := PageKey(seedHost, tt.url2)
			gotSame := key1 == key2

			if gotSame != tt.wantSame {
				t.Errorf("PageKey equality:\n  url1: %s\n  key1: %s\n  url2: %s\n  key2: %s\n  same: %v, want %v",
					tt.url1, key1, tt.url2, key2, gotSame, tt.wantSame)
			}
		})
	}
}

func TestPagination_FallbackToHash(t *testing.T) {
	seedHost := "www.example.com"

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "three query params",
			url:  "https://www.example.com/list/?page=2&sort=date&order=asc",
		},
		{
			name: "search query with page",
			url:  "https://www.example.com/search?q=test&page=2",
		},
		{
			name: "unknown single param",
			url:  "https://www.example.com/list/?foobar=123",
		},
		{
			name: "filter params only",
			url:  "https://www.example.com/list/?category=news&tag=breaking",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LocalPath(seedHost, tt.url, KindPage)
			got = strings.ReplaceAll(got, "\\", "/")

			if !strings.Contains(got, "__q-") {
				t.Errorf("LocalPath(%q) = %q, expected hash suffix __q-", tt.url, got)
			}
			if !strings.HasSuffix(got, ".html") {
				t.Errorf("LocalPath(%q) = %q, want .html suffix", tt.url, got)
			}
		})
	}
}
