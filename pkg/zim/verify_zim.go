package zim

import (
	"fmt"
	"os"

	"github.com/km269/wukong/internal/util"
)

func VerifyZIMFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}

	r, err := Open(path)
	if err != nil {
		return fmt.Errorf("open reader: %w", err)
	}
	defer r.Close()

	if util.DebugEnabled {
		fmt.Printf("ZIM File: %s\n", path)
		fmt.Printf("  Size: %d bytes\n", fi.Size())
		fmt.Printf("  ArticleCount: %d\n", r.Count())
		fmt.Printf("  MainPage: %d\n", r.hdr.MainPage)
		fmt.Printf("  URLPtrPos: %d\n", r.hdr.URLPtrPos)
		fmt.Printf("  ClusterPtrPos: %d\n", r.hdr.ClusterPtrPos)
		fmt.Printf("  MimeListPos: %d\n", r.hdr.MimeListPos)
		fmt.Printf("  Version: %d.%d\n", r.hdr.MajorVersion, r.hdr.MinorVersion)

		if r.hdr.MainPage != noMainPage {
			entry, err := r.direntAtIndex(r.hdr.MainPage)
			if err != nil {
				return fmt.Errorf("read main page entry: %w", err)
			}
			fmt.Printf("  Main Page Entry: ns=%c, url=%s, title=%s, redirect=%v\n",
				entry.namespace, entry.url, entry.title, entry.redirect)

			if entry.redirect {
				fmt.Println("  WARNING: Main page is a redirect!")
				fmt.Printf("  Redirect target index: %d\n", entry.targetIndex)
				targetEntry, err := r.direntAtIndex(entry.targetIndex)
				if err != nil {
					return fmt.Errorf("read target entry: %w", err)
				}
				fmt.Printf("  Redirect Target: ns=%c, url=%s, title=%s, redirect=%v\n",
					targetEntry.namespace, targetEntry.url, targetEntry.title, targetEntry.redirect)
			} else {
				fmt.Println("  OK: Main page is a content article")
				fmt.Printf("  Cluster: %d, Blob: %d\n", entry.cluster, entry.blob)
			}
		} else {
			fmt.Println("  ERROR: No main page!")
		}

		fmt.Println("\n  Checking for index.html...")
		blob, err := r.Get(NamespaceContent, "index.html")
		if err != nil {
			fmt.Printf("  index.html not found: %v\n", err)
		} else {
			fmt.Printf("  index.html found: %d bytes\n", len(blob.Data))
		}
	}

	return nil
}
