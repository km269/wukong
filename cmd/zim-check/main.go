package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/km269/wukong/pkg/zim"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: zim-check <zim-file>")
		os.Exit(1)
	}

	zpath := os.Args[1]
	r, err := zim.Open(zpath)
	if err != nil {
		fmt.Printf("Error opening: %v\n", err)
		os.Exit(1)
	}
	defer r.Close()

	// Find a biography page URL
	var pageURL string
	for i := uint32(0); i < r.Count(); i++ {
		e, err := r.EntryAt(i)
		if err != nil {
			continue
		}
		if e.Redirect {
			continue
		}
		if strings.Contains(e.URL, "biographies/") && strings.HasSuffix(e.URL, "/index.html") {
			pageURL = e.URL
			break
		}
	}

	if pageURL == "" {
		fmt.Println("No biography page found")
		return
	}

	fmt.Printf("Page URL in ZIM: %s\n", pageURL)

	// Get the page data
	var pageData []byte
	for i := uint32(0); i < r.Count(); i++ {
		e, err := r.EntryAt(i)
		if err != nil {
			continue
		}
		if e.Redirect {
			continue
		}
		if e.URL == pageURL {
			pageData = e.Data
			break
		}
	}

	if pageData == nil {
		fmt.Println("Page data not found")
		return
	}

	content := string(pageData)

	fmt.Println("\n=== biographies-list links (page links, should NOT be changed) ===")
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.Contains(line, "biographies-list.html") {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) > 150 {
				trimmed = trimmed[:150] + "..."
			}
			fmt.Println(" ", trimmed)
		}
	}

	fmt.Println("\n=== Asset links (should have 2 fewer ../ than original) ===")
	count := 0
	for _, line := range lines {
		if strings.Contains(line, "assets/") && strings.Contains(line, "../") && count < 5 {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) > 150 {
				trimmed = trimmed[:150] + "..."
			}
			// Count ../
			depth := strings.Count(trimmed, "../")
			fmt.Printf("  (%d x ../) %s\n", depth, trimmed)
			count++
		}
	}

	fmt.Println("\n=== Analysis ===")
	fmt.Printf("  Page depth: %d levels (%s)\n", strings.Count(pageURL, "/"), pageURL)
	fmt.Println("  Original asset depth: 3 x ../ (from pages/biographies/name/index.html)")
	fmt.Println("  Expected after removing 2 prefixes: 1 x ../")
}
