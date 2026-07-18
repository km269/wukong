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
