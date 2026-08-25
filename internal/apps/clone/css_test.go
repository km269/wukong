package clone

import (
	"testing"
)

func TestRewriteCSS_URLs(t *testing.T) {
	cssBase := "https://example.com/css/style.css"

	handler := func(absURL string) string {
		// Map known URLs to local paths.
		mapping := map[string]string{
			"https://example.com/images/bg.png":      "images/bg.png",
			"https://example.com/fonts/roboto.woff2": "fonts/roboto.woff2",
		}
		if p, ok := mapping[absURL]; ok {
			return p
		}
		return ""
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "url double quotes",
			input: `body { background: url("../images/bg.png"); }`,
			want:  `body { background: url("images/bg.png"); }`,
		},
		{
			name:  "url single quotes",
			input: `@font-face { src: url('../fonts/roboto.woff2'); }`,
			want:  `@font-face { src: url("fonts/roboto.woff2"); }`,
		},
		{
			name:  "url no quotes",
			input: `body { background: url(bg.png); }`,
			want:  `body { background: url(bg.png); }`, // Handler returns "" for unknown.
		},
		{
			name:  "data uri unchanged",
			input: `body { background: url(data:image/png;base64,xxx); }`,
			want:  `body { background: url(data:image/png;base64,xxx); }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(RewriteCSS([]byte(tt.input), cssBase, handler))
			if got != tt.want {
				t.Errorf("RewriteCSS() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractCSSAssetRefs(t *testing.T) {
	cssBase := "https://example.com/css/main.css"
	css := `
		body { background: url("../images/bg.png"); }
		@font-face { src: url("../fonts/roboto.woff2"); }
		.icon { background: url('icons/star.svg'); }
	`

	refs := ExtractCSSAssetRefs([]byte(css), cssBase)
	if len(refs) != 3 {
		t.Errorf("expected 3 refs, got %d: %v", len(refs), refs)
	}

	expected := map[string]bool{
		"https://example.com/images/bg.png":      true,
		"https://example.com/fonts/roboto.woff2": true,
		"https://example.com/css/icons/star.svg": true,
	}

	for _, ref := range refs {
		if !expected[ref] {
			t.Errorf("unexpected ref: %s", ref)
		}
	}
}

func TestExtractCSSAssetRefs_SkipsDataURI(t *testing.T) {
	css := `body { background: url(data:image/png;base64,abc); }`
	refs := ExtractCSSAssetRefs([]byte(css), "https://example.com/")
	if len(refs) != 0 {
		t.Errorf("expected 0 refs for data URI, got %d", len(refs))
	}
}
