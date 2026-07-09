package clone

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestResolveLazyLoad_Basic(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSrc    string
		wantSrcset string
	}{
		{
			name:    "data-src only",
			input:   `<img data-src="https://example.com/image.jpg">`,
			wantSrc: "https://example.com/image.jpg",
		},
		{
			name:       "data-srcset only",
			input:      `<img data-srcset="https://example.com/img-320w.jpg 320w, https://example.com/img-640w.jpg 640w">`,
			wantSrcset: "https://example.com/img-320w.jpg 320w, https://example.com/img-640w.jpg 640w",
		},
		{
			name:    "data-lazy-src only",
			input:   `<img data-lazy-src="https://example.com/lazy.jpg">`,
			wantSrc: "https://example.com/lazy.jpg",
		},
		{
			name:    "data-original only",
			input:   `<img data-original="https://example.com/original.png">`,
			wantSrc: "https://example.com/original.png",
		},
		{
			name:       "data-src and data-srcset together",
			input:      `<img data-src="https://example.com/fallback.jpg" data-srcset="https://example.com/img-320w.jpg 320w, https://example.com/img-640w.jpg 640w">`,
			wantSrc:    "https://example.com/fallback.jpg",
			wantSrcset: "https://example.com/img-320w.jpg 320w, https://example.com/img-640w.jpg 640w",
		},
		{
			name:    "src already present, data-src overwrites",
			input:   `<img src="placeholder.gif" data-src="https://example.com/real.jpg">`,
			wantSrc: "https://example.com/real.jpg",
		},
		{
			name:       "srcset already present, data-srcset overwrites",
			input:      `<img srcset="old.jpg 1x" data-srcset="https://example.com/new-320w.jpg 320w">`,
			wantSrcset: "https://example.com/new-320w.jpg 320w",
		},
		{
			name:    "empty data-src ignored",
			input:   `<img data-src="" src="default.jpg">`,
			wantSrc: "default.jpg",
		},
		{
			name:       "empty data-srcset ignored",
			input:      `<img data-srcset="" srcset="default.jpg 1x">`,
			wantSrcset: "default.jpg 1x",
		},
		{
			name:    "no lazy attributes",
			input:   `<img src="normal.jpg">`,
			wantSrc: "normal.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := html.Parse(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("failed to parse HTML: %v", err)
			}

			var img *html.Node
			var findImg func(*html.Node)
			findImg = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "img" {
					img = n
					return
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					findImg(c)
				}
			}
			findImg(doc)

			if img == nil {
				t.Fatal("no img element found")
			}

			resolveLazyLoad(img)

			if tt.wantSrc != "" {
				src := getAttrCI(img, "src")
				if src != tt.wantSrc {
					t.Errorf("src = %q, want %q", src, tt.wantSrc)
				}
			}

			if tt.wantSrcset != "" {
				srcset := getAttrCI(img, "srcset")
				if srcset != tt.wantSrcset {
					t.Errorf("srcset = %q, want %q", srcset, tt.wantSrcset)
				}
			}
		})
	}
}

func TestResolveLazyLoad_DataSrcsetComplex(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSrcset string
	}{
		{
			name:       "data-srcset with pixel density descriptors",
			input:      `<img data-srcset="https://example.com/img.jpg 1x, https://example.com/img-2x.jpg 2x, https://example.com/img-3x.jpg 3x">`,
			wantSrcset: "https://example.com/img.jpg 1x, https://example.com/img-2x.jpg 2x, https://example.com/img-3x.jpg 3x",
		},
		{
			name:       "data-srcset with width descriptors and sizes",
			input:      `<img data-srcset="https://example.com/img-480w.jpg 480w, https://example.com/img-800w.jpg 800w" sizes="(max-width: 600px) 480px, 800px">`,
			wantSrcset: "https://example.com/img-480w.jpg 480w, https://example.com/img-800w.jpg 800w",
		},
		{
			name:       "data-srcset with mixed descriptors",
			input:      `<img data-srcset="https://example.com/img.jpg, https://example.com/img-2x.jpg 2x">`,
			wantSrcset: "https://example.com/img.jpg, https://example.com/img-2x.jpg 2x",
		},
		{
			name:       "data-srcset with multiple comma-separated entries",
			input:      `<img data-srcset="a.jpg 100w, b.jpg 200w, c.jpg 300w, d.jpg 400w">`,
			wantSrcset: "a.jpg 100w, b.jpg 200w, c.jpg 300w, d.jpg 400w",
		},
		{
			name:       "data-srcset with whitespace variations",
			input:      `<img data-srcset="  https://example.com/img1.jpg 1x ,  https://example.com/img2.jpg 2x  ">`,
			wantSrcset: "  https://example.com/img1.jpg 1x ,  https://example.com/img2.jpg 2x  ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := html.Parse(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("failed to parse HTML: %v", err)
			}

			var img *html.Node
			var findImg func(*html.Node)
			findImg = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "img" {
					img = n
					return
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					findImg(c)
				}
			}
			findImg(doc)

			if img == nil {
				t.Fatal("no img element found")
			}

			resolveLazyLoad(img)

			srcset := getAttrCI(img, "srcset")
			if srcset != tt.wantSrcset {
				t.Errorf("srcset = %q, want %q", srcset, tt.wantSrcset)
			}
		})
	}
}

func TestRewriteHTML_LazyLoadIntegration(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCalls []string
	}{
		{
			name:      "img data-src is converted and passed to sink",
			input:     `<img data-src="https://example.com/image.jpg">`,
			wantCalls: []string{"https://example.com/image.jpg"},
		},
		{
			name:      "img data-srcset is converted and passed to sink",
			input:     `<img data-srcset="https://example.com/img-320w.jpg 320w, https://example.com/img-640w.jpg 640w">`,
			wantCalls: []string{"https://example.com/img-320w.jpg", "https://example.com/img-640w.jpg"},
		},
		{
			name:      "source data-srcset inside picture is converted",
			input:     `<picture><source data-srcset="https://example.com/webp.webp"><img data-src="https://example.com/fallback.jpg"></picture>`,
			wantCalls: []string{"https://example.com/webp.webp", "https://example.com/fallback.jpg"},
		},
		{
			name:      "source data-src inside video is converted",
			input:     `<video><source data-src="https://example.com/video.mp4"></video>`,
			wantCalls: []string{"https://example.com/video.mp4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			sink := func(absURL string, kind URLKind) string {
				if kind == KindAsset {
					calls = append(calls, absURL)
					return "local/" + strings.Split(absURL, "/")[len(strings.Split(absURL, "/"))-1]
				}
				return ""
			}

			doc, err := html.Parse(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("failed to parse HTML: %v", err)
			}

			base, _ := url.Parse("https://example.com/")
			RewriteHTML(doc, base, sink)

			if len(calls) != len(tt.wantCalls) {
				t.Errorf("got %d sink calls, want %d; got %v, want %v",
					len(calls), len(tt.wantCalls), calls, tt.wantCalls)
			}

			for i, want := range tt.wantCalls {
				if i >= len(calls) {
					break
				}
				if calls[i] != want {
					t.Errorf("call %d: got %q, want %q", i, calls[i], want)
				}
			}
		})
	}
}

func TestResolveLazyLoad_AttributeCaseInsensitive(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantSrc string
	}{
		{
			name:    "Data-Src (capital D)",
			input:   `<img Data-Src="https://example.com/image.jpg">`,
			wantSrc: "https://example.com/image.jpg",
		},
		{
			name:    "DATA-SRC (all caps)",
			input:   `<img DATA-SRC="https://example.com/image.jpg">`,
			wantSrc: "https://example.com/image.jpg",
		},
		{
			name:    "data-SRC (mixed case)",
			input:   `<img data-SRC="https://example.com/image.jpg">`,
			wantSrc: "https://example.com/image.jpg",
		},
		{
			name:    "Data-Srcset (capital D and S)",
			input:   `<img Data-Srcset="https://example.com/img.jpg 1x">`,
			wantSrc: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := html.Parse(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("failed to parse HTML: %v", err)
			}

			var img *html.Node
			var findImg func(*html.Node)
			findImg = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "img" {
					img = n
					return
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					findImg(c)
				}
			}
			findImg(doc)

			if img == nil {
				t.Fatal("no img element found")
			}

			resolveLazyLoad(img)

			if tt.wantSrc != "" {
				src := getAttrCI(img, "src")
				if src != tt.wantSrc {
					t.Errorf("src = %q, want %q", src, tt.wantSrc)
				}
			}

			if strings.Contains(tt.input, "Srcset") {
				srcset := getAttrCI(img, "srcset")
				expectedSrcset := strings.Split(tt.input, "\"")[1]
				if srcset != expectedSrcset {
					t.Errorf("srcset = %q, want %q", srcset, expectedSrcset)
				}
			}
		})
	}
}
