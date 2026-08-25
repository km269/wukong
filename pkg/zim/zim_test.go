// Package zim provides ZIM file format creation and reading.
package zim

import (
	"os"
	"testing"
)

// TestZIMRoundtrip verifies the complete write-then-read cycle.
func TestZIMRoundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	zimPath := tmpDir + "/test.zim"

	// 1. Build ZIM archive.
	p := NewPacker()
	p.AddContent(NamespaceArticle, "pages/index.html", "Home", "text/html",
		[]byte("<html><body>Hello World</body></html>"))
	p.AddContent(NamespaceArticle, "pages/about/index.html", "About", "text/html",
		[]byte("<html><body>About Us</body></html>"))
	p.AddContent(NamespaceArticle, "assets/style.css", "styles", "text/css",
		[]byte("body{color:red;}"))
	p.AddContent(NamespaceArticle, "assets/logo.png", "logo", "image/png",
		[]byte{0x89, 0x50, 0x4E, 0x47, 0, 0, 0, 0}) // minimal PNG header
	p.AddMetadata("Title", "Test Archive")
	p.AddMetadata("Language", "eng")
	p.AddMetadata("Date", "2026-06-25")
	p.SetMainPage(NamespaceArticle, "pages/index.html")
	p.AddRedirect(NamespaceRedirect, "mainPage", "Main Page", NamespaceArticle, "pages/index.html")

	t.Logf("Packer article count before Build: %d", len(p.articles))
	for i, a := range p.articles {
		t.Logf("Article %d: ns=%c, url=%s, cluster=%d, blob=%d, dataLen=%d",
			i, a.Namespace, a.URL, a.Cluster, a.Blob, len(a.Data))
	}

	err := p.Build(zimPath, "TestApp", "roundtrip test", true)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// 2. Read ZIM archive.
	r, err := Open(zimPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	if r.Count() != 8 { // 4 content + 3 metadata + 1 redirect
		t.Errorf("Count = %d, want 8", r.Count())
	}

	// 3. Verify content articles via Get.
	tests := []struct {
		ns   byte
		url  string
		want string
	}{
		{NamespaceArticle, "pages/index.html", "<html><body>Hello World</body></html>"},
		{NamespaceArticle, "pages/about/index.html", "<html><body>About Us</body></html>"},
		{NamespaceArticle, "assets/style.css", "body{color:red;}"},
	}

	for _, tt := range tests {
		blob, err := r.Get(tt.ns, tt.url)
		if err != nil {
			t.Errorf("Get(%c/%s): %v", tt.ns, tt.url, err)
			continue
		}
		if blob.Data == nil {
			t.Errorf("Get(%c/%s) Data is nil", tt.ns, tt.url)
			continue
		}
		if string(blob.Data) != tt.want {
			t.Errorf("Get(%c/%s) = %q (len=%d), want %q (len=%d)",
				tt.ns, tt.url, string(blob.Data), len(blob.Data), tt.want, len(tt.want))
		}
	}

	// 4. Verify metadata.
	metaTests := []struct{ name, want string }{
		{"Title", "Test Archive"},
		{"Language", "eng"},
		{"Date", "2026-06-25"},
	}
	for _, mt := range metaTests {
		blob, err := r.Get('M', mt.name)
		if err != nil {
			t.Errorf("Get(M/%s): %v", mt.name, err)
			continue
		}
		if string(blob.Data) != mt.want {
			t.Errorf("Get(M/%s) = %q, want %q",
				mt.name, string(blob.Data), mt.want)
		}
	}

	// 5. Verify W/mainPage redirect resolves to content.
	blob, err := r.Get('W', "mainPage")
	if err != nil {
		t.Fatalf("Get(W/mainPage): %v", err)
	}
	if blob.URL != "pages/index.html" {
		t.Errorf("mainPage redirect = %q, want %q",
			blob.URL, "pages/index.html")
	}
	if string(blob.Data) != "<html><body>Hello World</body></html>" {
		t.Errorf("mainPage content = %q", string(blob.Data))
	}

	// 6. Verify image binary content roundtrip.
	pngBlob, err := r.Get(NamespaceArticle, "assets/logo.png")
	if err != nil {
		t.Errorf("Get(C/assets/logo.png): %v", err)
	} else if len(pngBlob.Data) < 8 {
		t.Errorf("PNG data too short: %d bytes", len(pngBlob.Data))
	}
}

// TestZIMRoundtrip_NoCompress verifies uncompressed archives.
func TestZIMRoundtrip_NoCompress(t *testing.T) {
	tmpDir := t.TempDir()
	zimPath := tmpDir + "/test_noz.zim"

	p := NewPacker()
	p.AddContent(NamespaceArticle, "index.html", "Home", "text/html",
		[]byte("<h1>Uncompressed</h1>"))
	p.Build(zimPath, "Test", "no compress", false)

	r, err := Open(zimPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	blob, err := r.Get(NamespaceArticle, "index.html")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(blob.Data) != "<h1>Uncompressed</h1>" {
		t.Errorf("got %q", string(blob.Data))
	}

	// Verify file was actually written.
	fi, err := os.Stat(zimPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size() == 0 {
		t.Error("ZIM file is empty")
	}
}

// TestZIMRedirect verifies redirect resolution.
func TestZIMRedirect(t *testing.T) {
	tmpDir := t.TempDir()
	zimPath := tmpDir + "/test_redir.zim"

	p := NewPacker()
	p.AddContent(NamespaceArticle, "page2.html", "Page 2", "text/html",
		[]byte("page two"))
	p.AddRedirect(NamespaceArticle, "old.html", "Old Page", NamespaceArticle, "page2.html")
	p.Build(zimPath, "Test", "redirect", false)

	r, err := Open(zimPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	// Following redirect should return target content.
	blob, err := r.Get(NamespaceArticle, "old.html")
	if err != nil {
		t.Fatalf("Get(C/old.html): %v", err)
	}
	if string(blob.Data) != "page two" {
		t.Errorf("redirected content = %q, want %q",
			string(blob.Data), "page two")
	}
	if blob.URL != "page2.html" {
		t.Errorf("redirect URL = %q, want %q",
			blob.URL, "page2.html")
	}
}

// TestZIMCount verifies article count and entry iteration.
func TestZIMCount(t *testing.T) {
	tmpDir := t.TempDir()
	zimPath := tmpDir + "/test_count.zim"

	p := NewPacker()
	for i := 0; i < 10; i++ {
		url := string(rune('a'+i)) + ".html"
		p.AddContent(NamespaceArticle, url, url, "text/html", []byte(url))
	}

	t.Logf("Packer article count before Build: %d", len(p.articles))
	for i, a := range p.articles {
		t.Logf("Article %d: ns=%c, url=%s, title=%s, dataLen=%d",
			i, a.Namespace, a.URL, a.Title, len(a.Data))
	}

	p.Build(zimPath, "Count", "count test", false)

	r, err := Open(zimPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	if r.Count() != 10 {
		t.Errorf("Count = %d, want 10", r.Count())
	}

	t.Logf("URL pointers:")
	for i, ptr := range r.urlPtrs {
		t.Logf("  URL ptr %d: %d", i, ptr)
	}

	// Verify URL-sorted order.
	for i := uint32(0); i < r.Count(); i++ {
		entry, err := r.EntryAt(i)
		if err != nil {
			t.Errorf("EntryAt(%d): %v", i, err)
			continue
		}
		want := string(rune('a'+i)) + ".html"
		if entry.URL != want {
			t.Errorf("EntryAt(%d) URL = %q, want %q", i, entry.URL, want)
		}
	}
}

// TestZIMIncrementalCache verifies cluster re-use on rebuild.
func TestZIMIncrementalCache(t *testing.T) {
	tmpDir := t.TempDir()
	zimPath := tmpDir + "/test.zim"
	cachePath := tmpDir + "/test.wukongcache"

	p := NewPacker()
	p.AddContent(NamespaceArticle, "index.html", "Home", "text/html",
		[]byte("<h1>Hello</h1>"))
	p.AddContent(NamespaceArticle, "style.css", "CSS", "text/css",
		[]byte("body{margin:0;}"))

	// First build (should compress all clusters).
	stats, err := p.BuildWithStats(zimPath, BuildOptions{
		AppName:  "Test",
		Compress: true,
	}, cachePath, true)
	if err != nil {
		t.Fatalf("BuildWithStats: %v", err)
	}
	if stats.ClustersReused != 0 {
		t.Errorf("first build reused = %d, want 0", stats.ClustersReused)
	}

	// Rebuild with same content (should reuse all clusters).
	p2 := NewPacker()
	p2.AddContent(NamespaceArticle, "index.html", "Home", "text/html",
		[]byte("<h1>Hello</h1>"))
	p2.AddContent(NamespaceArticle, "style.css", "CSS", "text/css",
		[]byte("body{margin:0;}"))

	stats2, err := p2.BuildWithStats(zimPath, BuildOptions{
		AppName:  "Test",
		Compress: true,
	}, cachePath, true)
	if err != nil {
		t.Fatalf("BuildWithStats 2: %v", err)
	}
	if stats2.ClustersReused == 0 {
		t.Errorf("second build reused = %d, want > 0", stats2.ClustersReused)
	}

	// Verify content still readable.
	r, err := Open(zimPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	blob, _ := r.Get(NamespaceArticle, "index.html")
	if string(blob.Data) != "<h1>Hello</h1>" {
		t.Errorf("content = %q", string(blob.Data))
	}
}

// TestZIMEmptyArticles verifies empty articles and edge cases.
func TestZIMEmptyArticles(t *testing.T) {
	tmpDir := t.TempDir()
	zimPath := tmpDir + "/test_empty.zim"

	p := NewPacker()
	p.AddContent(NamespaceArticle, "empty.html", "Empty", "text/html", []byte{})
	p.AddMetadata("Description", "")
	p.Build(zimPath, "Empty", "test", false)

	r, err := Open(zimPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	blob, err := r.Get(NamespaceArticle, "empty.html")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(blob.Data) != 0 {
		t.Errorf("empty article data = %d bytes, want 0", len(blob.Data))
	}

	meta, err := r.Get('M', "Description")
	if err != nil {
		t.Fatalf("Get(M/Description): %v", err)
	}
	if len(meta.Data) != 0 {
		t.Errorf("empty metadata = %d bytes, want 0", len(meta.Data))
	}
}

// TestZIMCompressedText verifies text content is properly compressed.
func TestZIMCompressedText(t *testing.T) {
	tmpDir := t.TempDir()
	zimPath := tmpDir + "/test_compressed.zim"

	p := NewPacker()
	largeText := make([]byte, 10000)
	for i := range largeText {
		largeText[i] = 'a'
	}
	p.AddContent(NamespaceArticle, "large.html", "Large Text", "text/html", largeText)
	p.Build(zimPath, "Compressed", "compressed test", true)

	r, err := Open(zimPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	blob, err := r.Get(NamespaceArticle, "large.html")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(blob.Data) != 10000 {
		t.Errorf("Data length = %d, want 10000", len(blob.Data))
	}
	if string(blob.Data) != string(largeText) {
		t.Errorf("Data content mismatch")
	}
}

// TestZIMCompressedMixed verifies mixed text and binary content.
func TestZIMCompressedMixed(t *testing.T) {
	tmpDir := t.TempDir()
	zimPath := tmpDir + "/test_mixed.zim"

	p := NewPacker()
	p.AddContent(NamespaceArticle, "page.html", "Page", "text/html", []byte("<html>test</html>"))
	p.AddContent(NamespaceArticle, "style.css", "CSS", "text/css", []byte("body{color:red;}"))
	p.AddContent(NamespaceArticle, "image.png", "Image", "image/png", []byte{0x89, 0x50, 0x4E, 0x47, 0, 0, 0, 0})
	p.AddContent(NamespaceArticle, "font.woff", "Font", "font/woff", []byte{0x77, 0x4F, 0x46, 0x46})
	p.Build(zimPath, "Mixed", "mixed content test", true)

	r, err := Open(zimPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	if r.Count() != 4 {
		t.Errorf("Count = %d, want 4", r.Count())
	}

	t.Logf("ClusterCount: %d", r.hdr.ClusterCount)
	t.Logf("URLPtrPos: %d", r.hdr.URLPtrPos)
	t.Logf("ClusterPtrPos: %d", r.hdr.ClusterPtrPos)

	for i := uint32(0); i < r.hdr.ArticleCount; i++ {
		entry, err := r.direntAtIndex(i)
		if err != nil {
			t.Errorf("direntAtIndex(%d): %v", i, err)
			continue
		}
		t.Logf("Entry %d: ns=%c, url=%s, cluster=%d, blob=%d",
			i, entry.namespace, entry.url, entry.cluster, entry.blob)
	}

	for i := uint32(0); i < r.hdr.ClusterCount; i++ {
		if i < uint32(len(r.clusterPtrs)) {
			t.Logf("Cluster %d pointer: %d", i, r.clusterPtrs[i])
		}
	}

	textTests := []struct {
		url  string
		want string
	}{
		{"page.html", "<html>test</html>"},
		{"style.css", "body{color:red;}"},
	}
	for _, tt := range textTests {
		blob, err := r.Get(NamespaceArticle, tt.url)
		if err != nil {
			t.Errorf("Get(%s): %v", tt.url, err)
			continue
		}
		if string(blob.Data) != tt.want {
			t.Errorf("Get(%s) = %q, want %q", tt.url, string(blob.Data), tt.want)
		}
	}

	binaryTests := []struct {
		url  string
		want []byte
	}{
		{"image.png", []byte{0x89, 0x50, 0x4E, 0x47, 0, 0, 0, 0}},
		{"font.woff", []byte{0x77, 0x4F, 0x46, 0x46}},
	}
	for _, tt := range binaryTests {
		blob, err := r.Get(NamespaceArticle, tt.url)
		if err != nil {
			t.Errorf("Get(%s): %v", tt.url, err)
			continue
		}
		if len(blob.Data) != len(tt.want) {
			t.Errorf("Get(%s) length = %d, want %d", tt.url, len(blob.Data), len(tt.want))
			continue
		}
		for i := range tt.want {
			if blob.Data[i] != tt.want[i] {
				t.Errorf("Get(%s) byte %d = %d, want %d", tt.url, i, blob.Data[i], tt.want[i])
				break
			}
		}
	}
}

// TestZIMCompressedEmpty verifies empty content handling.
func TestZIMCompressedEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	zimPath := tmpDir + "/test_empty.zim"

	p := NewPacker()
	p.AddContent(NamespaceArticle, "empty.html", "Empty", "text/html", []byte{})
	p.Build(zimPath, "Empty", "empty test", true)

	r, err := Open(zimPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	blob, err := r.Get(NamespaceArticle, "empty.html")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(blob.Data) != 0 {
		t.Errorf("Data length = %d, want 0", len(blob.Data))
	}
}

// TestZIMClusterDebug is a debug test to check cluster functionality.
func TestZIMClusterDebug(t *testing.T) {
	tmpDir := t.TempDir()
	zimPath := tmpDir + "/test_cluster.zim"

	p := NewPacker()
	p.AddContent(NamespaceArticle, "test.html", "Test", "text/html",
		[]byte("<html>test</html>"))
	p.Build(zimPath, "Test", "cluster debug", true)

	r, err := Open(zimPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	t.Logf("ArticleCount: %d", r.Count())
	t.Logf("ClusterCount: %d", r.hdr.ClusterCount)
	t.Logf("URLPtrPos: %d", r.hdr.URLPtrPos)
	t.Logf("ClusterPtrPos: %d", r.hdr.ClusterPtrPos)

	if r.hdr.ClusterCount > 0 {
		t.Logf("Cluster 0 pointer: %d", r.clusterPtrs[0])

		// Read cluster data directly.
		clusterStart := r.clusterPtrs[0]
		clusterEnd := uint64(r.size) - 16 // minus MD5 checksum
		clusterSize := clusterEnd - clusterStart

		clusterData := make([]byte, clusterSize)
		if _, err := r.ra.ReadAt(clusterData, int64(clusterStart)); err != nil {
			t.Fatalf("Read cluster data: %v", err)
		}

		t.Logf("Cluster size: %d", clusterSize)
		t.Logf("Cluster data: %v", clusterData)
		t.Logf("Cluster first byte (compression): %d", clusterData[0])

		if clusterSize > 1 {
			t.Logf("Cluster remaining data: %v", clusterData[1:])
		}
	}

	for i := uint32(0); i < r.Count(); i++ {
		d, err := r.direntAtIndex(i)
		if err != nil {
			t.Errorf("direntAtIndex(%d): %v", i, err)
			continue
		}
		t.Logf("Entry %d: ns=%c, url=%s, title=%s, cluster=%d, blob=%d",
			i, d.namespace, d.url, d.title, d.cluster, d.blob)
	}

	blob, err := r.Get(NamespaceArticle, "test.html")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	t.Logf("Get result: data=%q (len=%d)", string(blob.Data), len(blob.Data))
}
