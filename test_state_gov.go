package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/stealth"
)

func main() {
	testURL := "https://www.state.gov/biographies-list/"

	fmt.Println("Launching browser with stealth...")
	l := launcher.New()
	controlURL := l.MustLaunch()
	defer l.Cleanup()

	fmt.Println("Connecting to browser...")
	browser := rod.New().ControlURL(controlURL).MustConnect()
	defer browser.MustClose()

	fmt.Println("Creating stealth page...")
	page, err := stealth.Page(browser)
	if err != nil {
		fmt.Printf("stealth.Page error: %v\n", err)
		page = browser.MustPage("")
	}

	page = page.Timeout(120 * time.Second)

	fmt.Printf("Navigating to %s...\n", testURL)
	start := time.Now()
	err = page.Navigate(testURL)
	if err != nil {
		fmt.Printf("Navigate error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Waiting for page load...")
	err = page.WaitLoad()
	if err != nil {
		fmt.Printf("WaitLoad error (continuing anyway): %v\n", err)
	} else {
		fmt.Println("Page load event fired")
	}

	fmt.Println("Waiting for settle (3s)...")
	time.Sleep(3 * time.Second)

	elapsed := time.Since(start)
	fmt.Printf("Page loaded in %v\n", elapsed)

	title, _ := page.Element("title")
	titleText := ""
	if title != nil {
		titleText, _ = title.Text()
	}

	finalURL := page.MustEval(`() => window.location.href`).String()
	html, _ := page.HTML()

	fmt.Printf("Title: %s\n", titleText)
	fmt.Printf("Final URL: %s\n", finalURL)
	fmt.Printf("HTML size: %d bytes\n", len(html))

	isMaintenance := strings.Contains(strings.ToLower(html), "maintenance") ||
		strings.Contains(strings.ToLower(html), "technical difficulties") ||
		len(html) < 10000

	if isMaintenance {
		fmt.Println("Result: BLOCKED (maintenance/block page)")
		os.Exit(1)
	}

	fmt.Println("Result: SUCCESS (got real page content)")

	biographyCount := strings.Count(html, "biography-collection__link")
	fmt.Printf("Biography links found: %d\n", biographyCount)
}
