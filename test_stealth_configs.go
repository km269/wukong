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

	tests := []struct {
		name    string
		setup   func(*launcher.Launcher) *launcher.Launcher
		stealth bool
	}{
		{
			name: "default rod + stealth",
			setup: func(l *launcher.Launcher) *launcher.Launcher {
				return l
			},
			stealth: true,
		},
		{
			name: "disable AutomationControlled + stealth",
			setup: func(l *launcher.Launcher) *launcher.Launcher {
				return l.Set("disable-blink-features", "AutomationControlled")
			},
			stealth: true,
		},
		{
			name: "full stealth flags",
			setup: func(l *launcher.Launcher) *launcher.Launcher {
				return l.
					Set("disable-blink-features", "AutomationControlled").
					Set("disable-web-security", "").
					Set("disable-features", "IsolateOrigins,site-per-process").
					Set("lang", "en-US").
					Set("disable-dev-shm-usage", "").
					Set("disable-gpu", "").
					Set("no-first-run", "").
					Set("no-default-browser-check", "").
					Set("start-maximized", "")
			},
			stealth: true,
		},
		{
			name: "custom UA + full flags",
			setup: func(l *launcher.Launcher) *launcher.Launcher {
				ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
				return l.
					Set("disable-blink-features", "AutomationControlled").
					Set("user-agent", ua).
					Set("lang", "en-US").
					Set("disable-dev-shm-usage", "").
					Set("no-first-run", "").
					Set("no-default-browser-check", "").
					Set("start-maximized", "")
			},
			stealth: true,
		},
	}

	for _, tt := range tests {
		fmt.Printf("\n=== Testing: %s ===\n", tt.name)

		l := launcher.New()
		l = tt.setup(l)
		controlURL := l.MustLaunch()

		browser := rod.New().ControlURL(controlURL).MustConnect()

		var page *rod.Page
		if tt.stealth {
			var err error
			page, err = stealth.Page(browser)
			if err != nil {
				fmt.Printf("  stealth.Page error: %v\n", err)
				page = browser.MustPage("")
			}
		} else {
			page = browser.MustPage("")
		}

		page = page.Timeout(30 * time.Second)

		err := page.Navigate(testURL)
		if err != nil {
			fmt.Printf("  Navigate error: %v\n", err)
			browser.MustClose()
			continue
		}

		page.MustWaitLoad()
		time.Sleep(3 * time.Second)

		html, err := page.HTML()
		if err != nil {
			fmt.Printf("  HTML error: %v\n", err)
			browser.MustClose()
			continue
		}

		title, _ := page.Element("title")
		titleText := ""
		if title != nil {
			titleText, _ = title.Text()
		}

		finalURL := page.MustEval(`() => window.location.href`).String()

		isMaintenance := strings.Contains(strings.ToLower(html), "maintenance") ||
			strings.Contains(strings.ToLower(html), "technical difficulties") ||
			strings.Contains(strings.ToLower(html), "forbidden") ||
			len(html) < 10000

		fmt.Printf("  Title: %s\n", titleText)
		fmt.Printf("  Final URL: %s\n", finalURL)
		fmt.Printf("  HTML size: %d bytes\n", len(html))
		if isMaintenance {
			fmt.Printf("  Result: BLOCKED (maintenance/block page)\n")
		} else {
			fmt.Printf("  Result: SUCCESS (got real page content)\n")
		}

		browser.MustClose()
		l.Cleanup()

		if !isMaintenance {
			fmt.Println("\n✓ Found working configuration! Stopping here.")
			os.Exit(0)
		}
	}

	fmt.Println("\nAll configurations were blocked.")
	os.Exit(1)
}
