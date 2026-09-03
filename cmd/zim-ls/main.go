package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/km269/wukong/pkg/zim"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: zim-ls <zim-file>")
		os.Exit(1)
	}

	zpath := os.Args[1]
	info, err := os.Stat(zpath)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	if info.IsDir() {
		filepath.Walk(zpath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(strings.ToLower(path), ".zim") {
				listZim(path)
			}
			return nil
		})
	} else {
		listZim(zpath)
	}
}

func listZim(zpath string) {
	fmt.Printf("=== %s ===\n", zpath)
	r, err := zim.Open(zpath)
	if err != nil {
		fmt.Printf("Error opening: %v\n", err)
		return
	}
	defer r.Close()

	count := r.Count()
	fmt.Printf("Entries: %d\n\n", count)

	// Aggregate by namespace + mimetype
	type stat struct {
		ns       byte
		mime     string
		count    int
		redirect int
	}
	stats := map[string]*stat{}
	for i := uint32(0); i < count; i++ {
		e, err := r.EntryAt(i)
		if err != nil {
			continue
		}
		key := fmt.Sprintf("%c|%s", e.Namespace, e.MimeType)
		s, ok := stats[key]
		if !ok {
			s = &stat{ns: e.Namespace, mime: e.MimeType}
			stats[key] = s
		}
		if e.Redirect {
			s.redirect++
		} else {
			s.count++
		}
	}

	fmt.Println("--- By Namespace/MIME ---")
	for _, s := range stats {
		fmt.Printf("  [%c] mime=%q content=%d redirect=%d\n", s.ns, s.mime, s.count, s.redirect)
	}
	fmt.Println()

	// Show all HTML pages
	fmt.Println("--- HTML Pages ---")
	pageCount := 0
	for i := uint32(0); i < count; i++ {
		e, err := r.EntryAt(i)
		if err != nil {
			continue
		}
		if e.Redirect {
			continue
		}
		if strings.Contains(e.MimeType, "text/html") || strings.HasSuffix(e.URL, ".html") {
			fmt.Printf("  [%s] %s\n", string(e.Namespace), e.URL)
			pageCount++
		}
	}
	fmt.Printf("Total HTML pages: %d\n\n", pageCount)

	// Show non-HTML content entries (assets)
	fmt.Println("--- Non-HTML Assets ---")
	assetCount := 0
	for i := uint32(0); i < count; i++ {
		e, err := r.EntryAt(i)
		if err != nil {
			continue
		}
		if e.Redirect {
			continue
		}
		if strings.Contains(e.MimeType, "text/html") || strings.HasSuffix(e.URL, ".html") {
			continue
		}
		fmt.Printf("  [%s] %s  (mime=%s)\n", string(e.Namespace), e.URL, e.MimeType)
		assetCount++
		if assetCount >= 40 {
			fmt.Println("  ... (truncated)")
			break
		}
	}
	fmt.Printf("Total non-HTML assets: %d\n\n", assetCount)

	// Show redirects
	fmt.Println("--- Redirects ---")
	redirectCount := 0
	for i := uint32(0); i < count; i++ {
		e, err := r.EntryAt(i)
		if err != nil {
			continue
		}
		if e.Redirect {
			fmt.Printf("  [%s] %s -> [%s] %s\n",
				string(e.Namespace), e.URL,
				string(e.RedirectNamespace), e.RedirectURL)
			redirectCount++
		}
	}
	fmt.Printf("Total redirects: %d\n", redirectCount)
}
