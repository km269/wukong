package pack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/km269/wukong/pkg/zim"
)

func TestAdjustHTMLPaths(t *testing.T) {
	tests := []struct {
		name     string
		levels   int
		input    string
		expected string
	}{
		{
			name:     "single level - asset path with assets/",
			levels:   1,
			input:    `<link rel="stylesheet" href="../../../assets/style.css">`,
			expected: `<link rel="stylesheet" href="../../assets/style.css">`,
		},
		{
			name:     "single level - page link NOT adjusted",
			levels:   1,
			input:    `<a href="../../biographies-list.html">Biographies</a>`,
			expected: `<a href="../../biographies-list.html">Biographies</a>`,
		},
		{
			name:     "single level - page link with ../ NOT adjusted",
			levels:   1,
			input:    `<a href="../other/page.html">link</a>`,
			expected: `<a href="../other/page.html">link</a>`,
		},
		{
			name:     "single level - src asset path",
			levels:   1,
			input:    `<img src="../../assets/images/logo.png">`,
			expected: `<img src="../assets/images/logo.png">`,
		},
		{
			name:     "single level - src page link NOT adjusted",
			levels:   1,
			input:    `<img src="../photo.jpg">`,
			expected: `<img src="../photo.jpg">`,
		},
		{
			name:     "single level - single quoted asset",
			levels:   1,
			input:    `<link rel="stylesheet" href='../assets/style.css'>`,
			expected: `<link rel="stylesheet" href='assets/style.css'>`,
		},
		{
			name:     "single level - absolute path unchanged",
			levels:   1,
			input:    `<link rel="stylesheet" href="/assets/style.css">`,
			expected: `<link rel="stylesheet" href="/assets/style.css">`,
		},
		{
			name:     "single level - external URL unchanged",
			levels:   1,
			input:    `<img src="https://example.com/image.png">`,
			expected: `<img src="https://example.com/image.png">`,
		},
		{
			name:     "single level - no parent dir unchanged",
			levels:   1,
			input:    `<img src="assets/logo.png">`,
			expected: `<img src="assets/logo.png">`,
		},
		{
			name:   "single level - mixed page links and assets",
			levels: 1,
			input: `<html>
<head>
    <link rel="stylesheet" href="../../../assets/style.css">
</head>
<body>
    <img src="../../assets/images/logo.png">
    <a href="../other/page.html">page link</a>
    <a href="../../biographies-list.html">Biographies</a>
    <img src="../photo.jpg">
</body>
</html>`,
			expected: `<html>
<head>
    <link rel="stylesheet" href="../../assets/style.css">
</head>
<body>
    <img src="../assets/images/logo.png">
    <a href="../other/page.html">page link</a>
    <a href="../../biographies-list.html">Biographies</a>
    <img src="../photo.jpg">
</body>
</html>`,
		},
		{
			name:     "two levels - asset path stripped twice",
			levels:   2,
			input:    `<link rel="stylesheet" href="../../../assets/style.css">`,
			expected: `<link rel="stylesheet" href="../assets/style.css">`,
		},
		{
			name:     "two levels - page link NOT adjusted",
			levels:   2,
			input:    `<a href="../../biographies-list.html">Biographies</a>`,
			expected: `<a href="../../biographies-list.html">Biographies</a>`,
		},
		{
			name:   "two levels - mixed",
			levels: 2,
			input: `<html>
<head>
    <link rel="stylesheet" href="../../../assets/style.css">
</head>
<body>
    <img src="../../assets/images/logo.png">
    <a href="../../biographies-list.html">Biographies</a>
</body>
</html>`,
			expected: `<html>
<head>
    <link rel="stylesheet" href="../assets/style.css">
</head>
<body>
    <img src="assets/images/logo.png">
    <a href="../../biographies-list.html">Biographies</a>
</body>
</html>`,
		},
		{
			name:     "zero levels - no change",
			levels:   0,
			input:    `<link rel="stylesheet" href="../../../assets/style.css">`,
			expected: `<link rel="stylesheet" href="../../../assets/style.css">`,
		},
		{
			name:     "single level - srcset multiple URLs",
			levels:   1,
			input:    `<img srcset="../assets/img1.jpg 239w, ../assets/img2.jpg 38w, ../assets/img3.jpg 819w">`,
			expected: `<img srcset="assets/img1.jpg 239w, assets/img2.jpg 38w, assets/img3.jpg 819w">`,
		},
		{
			name:     "single level - srcset with deeper paths",
			levels:   1,
			input:    `<img srcset="../../assets/img1.jpg 239w, ../../assets/img2.jpg 38w">`,
			expected: `<img srcset="../assets/img1.jpg 239w, ../assets/img2.jpg 38w">`,
		},
		{
			name:     "two levels - srcset multiple URLs",
			levels:   2,
			input:    `<img srcset="../../assets/img1.jpg 239w, ../../assets/img2.jpg 38w">`,
			expected: `<img srcset="assets/img1.jpg 239w, assets/img2.jpg 38w">`,
		},
		{
			name:     "single level - srcset mixed with page links (page links unchanged)",
			levels:   1,
			input:    `<source srcset="../assets/img1.jpg 1x, ../assets/img2.jpg 2x">`,
			expected: `<source srcset="assets/img1.jpg 1x, assets/img2.jpg 2x">`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := string(adjustHTMLPaths([]byte(tt.input), tt.levels))
			if result != tt.expected {
				t.Errorf("adjustHTMLPaths(levels=%d) = \n%v\nwant \n%v", tt.levels, result, tt.expected)
			}
		})
	}
}

func TestStripAssetsPrefixFromHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "relative path with assets/",
			input:    `<link rel="stylesheet" href="../../assets/css/style.css">`,
			expected: `<link rel="stylesheet" href="../../css/style.css">`,
		},
		{
			name:     "src attribute with assets/",
			input:    `<img src="../assets/images/logo.png">`,
			expected: `<img src="../images/logo.png">`,
		},
		{
			name:     "direct assets/ path",
			input:    `<img src="assets/logo.png">`,
			expected: `<img src="logo.png">`,
		},
		{
			name:     "page link without assets/ unchanged",
			input:    `<a href="../other/page.html">link</a>`,
			expected: `<a href="../other/page.html">link</a>`,
		},
		{
			name:     "absolute path unchanged",
			input:    `<link rel="stylesheet" href="/assets/style.css">`,
			expected: `<link rel="stylesheet" href="/style.css">`,
		},
		{
			name:     "external URL unchanged",
			input:    `<img src="https://example.com/assets/image.png">`,
			expected: `<img src="https://example.com/image.png">`,
		},
		{
			name:     "single quoted attribute",
			input:    `<link rel="stylesheet" href='../assets/style.css'>`,
			expected: `<link rel="stylesheet" href='../style.css'>`,
		},
		{
			name: "mixed page links and assets",
			input: `<html>
<head>
    <link rel="stylesheet" href="../../assets/css/style.css">
</head>
<body>
    <img src="../assets/images/logo.png">
    <a href="../other/page.html">page link</a>
    <a href="../../biographies-list.html">Biographies</a>
    <img src="../photo.jpg">
</body>
</html>`,
			expected: `<html>
<head>
    <link rel="stylesheet" href="../../css/style.css">
</head>
<body>
    <img src="../images/logo.png">
    <a href="../other/page.html">page link</a>
    <a href="../../biographies-list.html">Biographies</a>
    <img src="../photo.jpg">
</body>
</html>`,
		},
		{
			name:     "no assets prefix unchanged",
			input:    `<img src="../images/logo.png">`,
			expected: `<img src="../images/logo.png">`,
		},
		{
			name:     "srcset multiple URLs",
			input:    `<img srcset="../assets/img1.jpg 239w, ../assets/img2.jpg 38w, ../assets/img3.jpg 819w">`,
			expected: `<img srcset="../img1.jpg 239w, ../img2.jpg 38w, ../img3.jpg 819w">`,
		},
		{
			name:     "srcset with direct assets/",
			input:    `<img srcset="assets/img1.jpg 1x, assets/img2.jpg 2x">`,
			expected: `<img srcset="img1.jpg 1x, img2.jpg 2x">`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := string(stripAssetsPrefixFromHTML([]byte(tt.input)))
			if result != tt.expected {
				t.Errorf("stripAssetsPrefixFromHTML() = \n%v\nwant \n%v", result, tt.expected)
			}
		})
	}
}

func TestAdjustHTMLPathsStyleAttr(t *testing.T) {
	tests := []struct {
		name     string
		levels   int
		input    string
		expected string
	}{
		{
			name:     "style background-image with ../assets/",
			levels:   1,
			input:    `<div style="background-image: url('../assets/images/bg.jpg');">`,
			expected: `<div style="background-image: url('assets/images/bg.jpg');">`,
		},
		{
			name:     "style background with double quotes",
			levels:   1,
			input:    `<div style='background: url("../assets/images/bg.jpg");'>`,
			expected: `<div style='background: url("assets/images/bg.jpg");'>`,
		},
		{
			name:     "style multiple urls",
			levels:   1,
			input:    `<div style="background: url('../assets/images/bg.jpg') url('../assets/images/pattern.png');">`,
			expected: `<div style="background: url('assets/images/bg.jpg') url('assets/images/pattern.png');">`,
		},
		{
			name:     "style with no quotes",
			levels:   1,
			input:    `<div style="background: url(../assets/images/bg.jpg);">`,
			expected: `<div style="background: url(assets/images/bg.jpg);">`,
		},
		{
			name:     "style with external url unchanged",
			levels:   1,
			input:    `<div style="background: url('https://example.com/bg.jpg');">`,
			expected: `<div style="background: url('https://example.com/bg.jpg');">`,
		},
		{
			name:     "style with no parent dir unchanged",
			levels:   1,
			input:    `<div style="background: url('images/bg.jpg');">`,
			expected: `<div style="background: url('images/bg.jpg');">`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := string(adjustHTMLPaths([]byte(tt.input), tt.levels))
			if result != tt.expected {
				t.Errorf("adjustHTMLPaths() = \n%q\nwant \n%q", result, tt.expected)
			}
		})
	}
}

func TestStripAssetsPrefixFromHTMLStyleAttr(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "style background-image with ../assets/",
			input:    `<div style="background-image: url('../assets/images/bg.jpg');">`,
			expected: `<div style="background-image: url('../images/bg.jpg');">`,
		},
		{
			name:     "style with double quotes",
			input:    `<div style='background: url("../assets/images/bg.jpg");'>`,
			expected: `<div style='background: url("../images/bg.jpg");'>`,
		},
		{
			name:     "style with direct assets/",
			input:    `<div style="background: url(assets/images/logo.png);">`,
			expected: `<div style="background: url(images/logo.png);">`,
		},
		{
			name:     "style with no assets prefix unchanged",
			input:    `<div style="background: url('../images/bg.jpg');">`,
			expected: `<div style="background: url('../images/bg.jpg');">`,
		},
		{
			name:     "style with external url unchanged",
			input:    `<div style="background: url('https://example.com/assets/bg.jpg');">`,
			expected: `<div style="background: url('https://example.com/bg.jpg');">`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := string(stripAssetsPrefixFromHTML([]byte(tt.input)))
			if result != tt.expected {
				t.Errorf("stripAssetsPrefixFromHTML() = \n%q\nwant \n%q", result, tt.expected)
			}
		})
	}
}

func TestClonedZIMLayout(t *testing.T) {
	input := `<html>
<head>
    <link rel="stylesheet" href="../../../assets/css/style.css">
</head>
<body>
    <img src="../../assets/images/logo.png">
    <a href="../../biographies-list/page/2/index.html">Page 2</a>
    <a href="../page/4/index.html">Page 4</a>
</body>
</html>`

	// Step 1: adjustHTMLPaths (remove 1 level of ../ from asset paths)
	step1 := adjustHTMLPaths([]byte(input), 1)

	// Step 2: stripAssetsPrefix (remove assets/ prefix from resource paths)
	result := stripAssetsPrefixFromHTML(step1)

	expected := `<html>
<head>
    <link rel="stylesheet" href="../../css/style.css">
</head>
<body>
    <img src="../images/logo.png">
    <a href="../../biographies-list/page/2/index.html">Page 2</a>
    <a href="../page/4/index.html">Page 4</a>
</body>
</html>`

	if string(result) != expected {
		t.Errorf("cloned layout transformation = \n%v\nwant \n%v", string(result), expected)
	}
}

func TestPackZIMClonedLayout(t *testing.T) {
	// Create a temporary directory structure that mimics a cloned app.
	tmpDir := t.TempDir()

	// Create pages/biographies/abram-paley/index.html
	pageDir := filepath.Join(tmpDir, "pages", "biographies", "abram-paley")
	err := os.MkdirAll(pageDir, 0755)
	if err != nil {
		t.Fatalf("create page dir: %v", err)
	}
	pageContent := []byte(`<!DOCTYPE html>
<html><head><title>Abram Paley</title></head>
<body>
    <link rel="stylesheet" href="../../../assets/css/style.css">
    <img src="../../assets/images/photo.jpg">
    <a href="../../biographies-list.html">Back to list</a>
</body></html>`)
	err = os.WriteFile(filepath.Join(pageDir, "index.html"), pageContent, 0644)
	if err != nil {
		t.Fatalf("write page: %v", err)
	}

	// Create pages/biographies-list.html
	listContent := []byte(`<!DOCTYPE html>
<html><head><title>Biographies</title></head>
<body>
    <link rel="stylesheet" href="../assets/css/style.css">
    <a href="biographies/abram-paley/index.html">Abram Paley</a>
</body></html>`)
	err = os.WriteFile(filepath.Join(tmpDir, "pages", "biographies-list.html"), listContent, 0644)
	if err != nil {
		t.Fatalf("write list page: %v", err)
	}

	// Create pages/index.html (root page)
	rootContent := []byte(`<!DOCTYPE html>
<html><head><title>Home</title></head>
<body>
    <link rel="stylesheet" href="assets/css/style.css">
    <a href="biographies-list.html">Biographies</a>
</body></html>`)
	err = os.WriteFile(filepath.Join(tmpDir, "pages", "index.html"), rootContent, 0644)
	if err != nil {
		t.Fatalf("write root page: %v", err)
	}

	// Create assets/ directory
	assetDir := filepath.Join(tmpDir, "assets", "css")
	err = os.MkdirAll(assetDir, 0755)
	if err != nil {
		t.Fatalf("create asset dir: %v", err)
	}
	err = os.WriteFile(filepath.Join(assetDir, "style.css"), []byte("body { color: red; }"), 0644)
	if err != nil {
		t.Fatalf("write asset: %v", err)
	}

	// Pack into ZIM
	outputPath := filepath.Join(tmpDir, "test.zim")
	opts := DefaultOptions()
	opts.Format = FormatZIM
	opts.OutputPath = outputPath
	opts.AppName = "testapp"
	opts.Compress = false

	packer := NewPacker(opts)
	result, err := packer.Pack(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !result.Success {
		t.Fatalf("pack not successful: %v", result.Errors)
	}

	// Open the ZIM and check entries
	reader, err := zim.Open(outputPath)
	if err != nil {
		t.Fatalf("open zim: %v", err)
	}
	defer reader.Close()

	// Collect all content namespace entries
	count := reader.Count()
	contentURLs := make(map[string]bool)
	for i := uint32(0); i < count; i++ {
		entry, err := reader.EntryAt(i)
		if err != nil {
			continue
		}
		if entry.Namespace == 'C' {
			contentURLs[entry.URL] = true
		}
	}

	// Verify expected paths exist
	expectedPaths := []string{
		"index.html",
		"biographies-list.html",
		"biographies/abram-paley/index.html",
		"css/style.css",
	}

	for _, path := range expectedPaths {
		if !contentURLs[path] {
			t.Errorf("expected path %q not found in ZIM", path)
		}
	}

	// Check that paths do NOT have pages/ prefix
	unexpectedPrefixes := []string{"pages/", "assets/"}
	for url := range contentURLs {
		for _, prefix := range unexpectedPrefixes {
			if strings.HasPrefix(url, prefix) {
				t.Errorf("path %q should not have %q prefix", url, prefix)
			}
		}
	}

	t.Logf("Found %d content entries", len(contentURLs))
	for url := range contentURLs {
		t.Logf("  - %s", url)
	}
}

func TestPackZIMMainPagePath(t *testing.T) {
	tmpDir := t.TempDir()

	pageDir := filepath.Join(tmpDir, "pages", "biographies", "abram-paley")
	err := os.MkdirAll(pageDir, 0755)
	if err != nil {
		t.Fatalf("create page dir: %v", err)
	}
	pageContent := []byte(`<!DOCTYPE html>
<html><head><title>Abram Paley</title></head>
<body>
    <link rel="stylesheet" href="../../../assets/css/style.css">
    <a href="../../biographies-list.html">Back</a>
</body></html>`)
	err = os.WriteFile(filepath.Join(pageDir, "index.html"), pageContent, 0644)
	if err != nil {
		t.Fatalf("write page: %v", err)
	}

	listContent := []byte(`<!DOCTYPE html>
<html><head><title>Biographies List</title></head>
<body>
    <link rel="stylesheet" href="../assets/css/style.css">
    <a href="biographies/abram-paley/index.html">Abram Paley</a>
</body></html>`)
	err = os.WriteFile(filepath.Join(tmpDir, "pages", "biographies-list.html"), listContent, 0644)
	if err != nil {
		t.Fatalf("write list page: %v", err)
	}

	assetDir := filepath.Join(tmpDir, "assets", "css")
	err = os.MkdirAll(assetDir, 0755)
	if err != nil {
		t.Fatalf("create asset dir: %v", err)
	}
	err = os.WriteFile(filepath.Join(assetDir, "style.css"), []byte("body { color: red; }"), 0644)
	if err != nil {
		t.Fatalf("write asset: %v", err)
	}

	outputPath := filepath.Join(tmpDir, "test.zim")
	opts := DefaultOptions()
	opts.Format = FormatZIM
	opts.OutputPath = outputPath
	opts.AppName = "testapp"
	opts.Compress = false
	opts.MainPagePath = "biographies-list.html"

	packer := NewPacker(opts)
	result, err := packer.Pack(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if !result.Success {
		t.Fatalf("pack not successful: %v", result.Errors)
	}

	reader, err := zim.Open(outputPath)
	if err != nil {
		t.Fatalf("open zim: %v", err)
	}
	defer reader.Close()

	mainBlob, err := reader.MainPage()
	if err != nil {
		t.Fatalf("main page: %v", err)
	}

	t.Logf("Main page: [%s] %s (%s)", string(mainBlob.Namespace), mainBlob.URL, mainBlob.Title)

	if mainBlob.URL != "biographies-list.html" {
		t.Errorf("expected main page to be biographies-list.html, got %s", mainBlob.URL)
	}

	if mainBlob.Title != "Biographies List" {
		t.Errorf("expected main page title 'Biographies List', got %s", mainBlob.Title)
	}
}
