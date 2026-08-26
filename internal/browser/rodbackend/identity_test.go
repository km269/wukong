package rodbackend

import (
	"strings"
	"testing"

	"github.com/km269/wukong/internal/browser/antibot"
	"github.com/km269/wukong/internal/browser/behavior"
)

func TestIsChromiumBinary(t *testing.T) {
	cases := map[string]bool{
		"/usr/bin/chromium":                                      true,
		"/usr/bin/chromium-browser":                              true,
		"/snap/bin/chromium":                                     true,
		`C:\Program Files\Chromium\chromium.exe`:                 true,
		"/usr/bin/google-chrome":                                 false,
		"/usr/bin/google-chrome-stable":                          false,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`:  false,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`: false,
		"": false,
	}
	for in, want := range cases {
		if got := isChromiumBinary(in); got != want {
			t.Errorf("isChromiumBinary(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPickBrowserUAExcludesEdgeOnChromium(t *testing.T) {
	esc := antibot.NewEscalator(antibot.DefaultEscalatorConfig())
	p := &Pool{
		escalator:         esc,
		behaviorSimulator: behavior.New(behavior.DefaultConfig()),
		binaryIsChromium:  true,
	}
	for i := 0; i < 200; i++ {
		ua := p.pickBrowserUA()
		if ua == nil {
			t.Fatal("pickBrowserUA returned nil")
		}
		if !ua.ChromeOnly() {
			t.Fatalf("non-Chrome-family persona returned: %s", ua.UserAgent)
		}
		if strings.Contains(ua.UserAgent, "Edg/") {
			t.Fatalf("Edge persona leaked on distro-Chromium binary: %s", ua.UserAgent)
		}
	}
}

func TestPickBrowserUAAllowsEdgeOnChrome(t *testing.T) {
	esc := antibot.NewEscalator(antibot.DefaultEscalatorConfig())
	p := &Pool{
		escalator:         esc,
		behaviorSimulator: behavior.New(behavior.DefaultConfig()),
		binaryIsChromium:  false,
	}
	sawEdge := false
	for i := 0; i < 200; i++ {
		ua := p.pickBrowserUA()
		if ua == nil {
			t.Fatal("pickBrowserUA returned nil")
		}
		if strings.Contains(ua.UserAgent, "Edg/") {
			sawEdge = true
		}
	}
	if !sawEdge {
		t.Log("Edge persona never drawn in 200 picks on a Chrome binary (statistically unlikely, not a failure)")
	}
}

func TestBinaryChromeVersion(t *testing.T) {
	cases := map[string]string{
		"HeadlessChrome/132.0.6834.83": "132.0.6834.83",
		"Chrome/131.0.6778.86":         "131.0.6778.86",
		"Chrome":                       "",
		"Chrome/":                      "",
		"Chrome/unknown":               "",
		"":                             "",
	}
	for in, want := range cases {
		if got := binaryChromeVersion(in); got != want {
			t.Errorf("binaryChromeVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAlignUAWithBinary(t *testing.T) {
	p := &Pool{realChromeVer: "132.0.6834.83"}

	ua := &antibot.UAProfile{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		SecChUaPlatform: `"Windows"`,
		FullVersion:     "130.0.6721.117",
		PlatformVersion: "15.0.0",
	}
	got := p.alignUAWithBinary(ua)
	if got == ua {
		t.Fatal("alignUAWithBinary should return a clone, got the same pointer")
	}
	if !strings.Contains(got.UserAgent, "Chrome/132.0.0.0") {
		t.Errorf("UA version not rewritten to binary version: %s", got.UserAgent)
	}
	if strings.Contains(got.UserAgent, "130") {
		t.Errorf("stale persona version remains in UA: %s", got.UserAgent)
	}
	if got.FullVersion != "132.0.6834.83" {
		t.Errorf("FullVersion = %q, want the binary version", got.FullVersion)
	}
	// The original persona must stay untouched (pool-owned static pool).
	if ua.FullVersion != "130.0.6721.117" {
		t.Errorf("input persona mutated: %s", ua.FullVersion)
	}

	// Edge persona: both Chrome/ and Edg/ tokens rewritten.
	edge := &antibot.UAProfile{
		UserAgent:   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0",
		FullVersion: "130.0.6721.117",
	}
	e := p.alignUAWithBinary(edge)
	if !strings.Contains(e.UserAgent, "Chrome/132.0.0.0") || !strings.Contains(e.UserAgent, "Edg/132.0.0.0") {
		t.Errorf("Edge persona version tokens not both rewritten: %s", e.UserAgent)
	}

	// Unknown binary version → persona passes through unchanged.
	p2 := &Pool{}
	if out := p2.alignUAWithBinary(ua); out != ua {
		t.Error("empty realChromeVer should return the persona as-is")
	}
	if out := p2.alignUAWithBinary(nil); out != nil {
		t.Error("nil UA should pass through")
	}
}

func TestAlignUAIdentityCoherence(t *testing.T) {
	// After alignment, uaIdentityFor must derive brands from the
	// binary version, not the stale persona version.
	p := &Pool{realChromeVer: "132.0.6834.83"}
	ua := p.alignUAWithBinary(&antibot.UAProfile{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		SecChUaPlatform: `"Windows"`,
	})
	id := uaIdentityFor(ua)
	if id == nil {
		t.Fatal("uaIdentityFor returned nil for aligned Chrome persona")
	}
	if id.FullVersion != "132.0.6834.83" {
		t.Errorf("identity FullVersion = %q, want binary version", id.FullVersion)
	}
	found := false
	for _, b := range id.Brands {
		if b[0] == "Google Chrome" && b[1] == "132" {
			found = true
		}
	}
	if !found {
		t.Errorf("brands missing Google Chrome/132: %+v", id.Brands)
	}
}
