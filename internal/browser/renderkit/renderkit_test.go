package renderkit

import (
	"strings"
	"testing"
)

func TestIsHTMLContentType(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"", true}, // undetermined counts as HTML
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		{"Text/HTML; Charset=UTF-8", true},
		{"  application/xhtml+xml  ", true},
		{"application/xhtml+xml;profile=HTML5", true},
		{"application/pdf", false},
		{"image/png", false},
		{"application/json", false},
	}
	for _, tc := range cases {
		if got := IsHTMLContentType(tc.ct); got != tc.want {
			t.Errorf("IsHTMLContentType(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}

func TestIsTextContent(t *testing.T) {
	cases := []struct {
		mime string
		want bool
	}{
		{"text/css", true},
		{"text/css; charset=utf-8", true},
		{"application/javascript", true},
		{"text/javascript", true},
		{"application/json", true},
		{"image/svg+xml", true},
		{"application/xml; charset=utf-8", true},
		{"image/png", false},
		{"application/octet-stream", false},
		{"font/woff2", false},
	}
	for _, tc := range cases {
		if got := IsTextContent(tc.mime); got != tc.want {
			t.Errorf("IsTextContent(%q) = %v, want %v", tc.mime, got, tc.want)
		}
	}
}

func TestJSSnippets(t *testing.T) {
	// Every snippet must reference the DOM APIs it drives, so an
	// accidental edit that breaks a snippet fails loudly here.
	mustContain := map[string][]string{
		BehaviorSimScrollJS: {"window.scrollBy", "async"},
		BehaviorSimMouseJS:  {"MouseEvent", "mousemove"},
		ScrollJS:            {"scrollHeight", "viewportHeight", "maxIterations"},
		CollectLinksArrayJS: {"querySelectorAll('a[href]')", "Array.from(links)"},
		CollectLinksJSONJS:  {"querySelectorAll('a[href]')", "JSON.stringify"},
	}
	for js, markers := range mustContain {
		for _, m := range markers {
			if !strings.Contains(js, m) {
				t.Errorf("snippet missing marker %q", m)
			}
		}
	}

	// The two collect-links wrappers must share the same body so both
	// backends collect identical link sets.
	if !strings.Contains(CollectLinksArrayJS, CollectLinksJSBody) {
		t.Error("CollectLinksArrayJS does not embed the shared body")
	}
	if !strings.Contains(CollectLinksJSONJS, CollectLinksJSBody) {
		t.Error("CollectLinksJSONJS does not embed the shared body")
	}
}
