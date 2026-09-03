package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/km269/wukong/pkg/zim"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: zim-check <zim-file> <command> [args]")
		fmt.Println("Commands:")
		fmt.Println("  css             - list all CSS entries")
		fmt.Println("  entry <path>    - dump entry content")
		fmt.Println("  refs <path>     - extract asset references from an HTML entry")
		fmt.Println("  verify <path>   - verify all asset refs in an HTML entry resolve to ZIM entries")
		os.Exit(1)
	}
	zpath := os.Args[1]
	cmd := os.Args[2]

	r, err := zim.Open(zpath)
	if err != nil {
		fmt.Printf("Error opening: %v\n", err)
		os.Exit(1)
	}
	defer r.Close()

	count := r.Count()

	// Build a set of all content entry URLs for quick lookup
	entrySet := map[string]bool{}
	for i := uint32(0); i < count; i++ {
		e, err := r.EntryAt(i)
		if err != nil || e.Redirect {
			continue
		}
		entrySet[e.URL] = true
	}

	switch cmd {
	case "css":
		for i := uint32(0); i < count; i++ {
			e, err := r.EntryAt(i)
			if err != nil || e.Redirect {
				continue
			}
			if e.MimeType == "text/css" {
				fmt.Printf("  [C] %s\n", e.URL)
			}
		}
	case "entry":
		if len(os.Args) < 4 {
			fmt.Println("entry command needs <path>")
			os.Exit(1)
		}
		path := os.Args[3]
		blob, err := r.Get('C', path)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		os.Stdout.Write(blob.Data)
	case "refs":
		if len(os.Args) < 4 {
			fmt.Println("refs command needs <path>")
			os.Exit(1)
		}
		htmlPath := os.Args[3]
		blob, err := r.Get('C', htmlPath)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		html := string(blob.Data)
		re := regexp.MustCompile(`(?:href|src)=["']([^"']+)["']`)
		matches := re.FindAllStringSubmatch(html, -1)
		seen := map[string]bool{}
		var refs []string
		for _, m := range matches {
			u := m[1]
			if seen[u] {
				continue
			}
			seen[u] = true
			refs = append(refs, u)
		}
		sort.Strings(refs)
		for _, u := range refs {
			fmt.Println(u)
		}
	case "verify":
		if len(os.Args) < 4 {
			fmt.Println("verify command needs <html-path>")
			os.Exit(1)
		}
		htmlPath := os.Args[3]
		blob, err := r.Get('C', htmlPath)
		if err != nil {
			fmt.Printf("Error reading %s: %v\n", htmlPath, err)
			os.Exit(1)
		}
		html := string(blob.Data)
		// Extract href and src references
		re := regexp.MustCompile(`(?:href|src)=["']([^"']+)["']`)
		matches := re.FindAllStringSubmatch(html, -1)
		// Also extract url() from style attributes and <style> tags
		urlRe := regexp.MustCompile(`url\(["']?([^"')]+)["']?\)`)
		urlMatches := urlRe.FindAllStringSubmatch(html, -1)

		seen := map[string]bool{}
		var missing []string
		var ok int
		var skipped int

		checkRef := func(ref, htmlPath string) {
			if seen[ref] {
				return
			}
			seen[ref] = true
			// Skip external URLs, anchors, data URIs, mailto, etc.
			if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") ||
				strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "mailto:") ||
				strings.HasPrefix(ref, "tel:") || strings.HasPrefix(ref, "data:") ||
				strings.HasPrefix(ref, "javascript:") || ref == "" {
				skipped++
				return
			}
			// Resolve relative to the HTML page path
			resolved := path.Join(path.Dir(htmlPath), ref)
			resolved = filepath.ToSlash(resolved)
			// Clean up any ./ components
			resolved = path.Clean(resolved)
			if entrySet[resolved] {
				ok++
			} else {
				missing = append(missing, fmt.Sprintf("  MISSING: ref=%s -> resolved=%s", ref, resolved))
			}
		}

		for _, m := range matches {
			checkRef(m[1], htmlPath)
		}
		for _, m := range urlMatches {
			checkRef(m[1], htmlPath)
		}

		fmt.Printf("Verification for %s:\n", htmlPath)
		fmt.Printf("  OK:      %d\n", ok)
		fmt.Printf("  Missing: %d\n", len(missing))
		fmt.Printf("  Skipped: %d (external/anchor/etc)\n", skipped)
		if len(missing) > 0 {
			fmt.Println("Missing references:")
			for _, m := range missing {
				fmt.Println(m)
			}
		}
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}
