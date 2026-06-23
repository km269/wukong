// Package zim provides a pure Go implementation of the ZIM file format
// for offline web content storage.
//
// ZIM is the open file format used by Kiwix, Wikipedia offline, and
// other offline content projects. This package supports both creating
// and reading ZIM archives with zstd compression, article management,
// and proper ZIM header/file structure.
//
// Basic writer usage:
//
//	p := zim.NewPacker()
//	p.AddContent('C', "index.html", "Home", "text/html", htmlData)
//	p.AddContent('C', "style.css", "Styles", "text/css", cssData)
//	p.SetMainPage('C', "index.html")
//	p.Build("output.zim", "MyApp", "Description", true)
//
// Basic reader usage:
//
//	r, _ := zim.Open("output.zim")
//	defer r.Close()
//	b, _ := r.Get('C', "index.html")
//	fmt.Println(string(b.Data))
//
// Features:
//   - Full ZIM v6 file format compliance (header, URL/title pointers,
//     clusters with blob indexing, MIME type list)
//   - Article types: regular content, redirects, metadata
//   - Compression: zstd for text clusters, uncompressed for binary
//   - Smart cluster splitting: text (zstd) / binary (uncompressed)
//   - MIME-aware content classification
//   - Full-file MD5 checksum for data integrity
//   - Deterministic UUID (content-derived, reproducible builds)
//   - Namespace-aware URL/title sorting
//   - Redirect resolution by namespace+URL key
//   - Key-based entry replacement (same ns+URL overwrites previous)
//   - 4-byte aligned data layout
//   - io.Writer output via WriteTo for streaming or in-memory builds
//
// ZIM specification reference: https://wiki.openzim.org/wiki/ZIM_file_format
package zim
