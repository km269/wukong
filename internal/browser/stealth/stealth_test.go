package stealth

import (
	"math/rand"
	"strings"
	"testing"
)

func buildTestScript() string {
	return BuildScript(GenerateFingerprint(rand.New(rand.NewSource(42)), nil), &UAIdentity{
		Brands: [][2]string{
			{"Chromium", "130"},
			{"Google Chrome", "130"},
			{"Not?A_Brand", "99"},
		},
		FullVersion:     "130.0.6721.117",
		Platform:        "Windows",
		PlatformVersion: "15.0.0",
	})
}

func TestScriptContainsKeySpoofs(t *testing.T) {
	// Verify the stealth script contains all critical anti-detection measures.
	script := buildTestScript()
	checks := []string{
		// Primary bot detection flag.
		"navigator.webdriver",

		// Chrome runtime spoof.
		"window.chrome",
		"loadTimes",
		"csi",

		// Plugin spoofing (defineProperty form: navigator, 'plugins').
		"navigator, 'plugins'",
		"Chrome PDF Plugin",
		"PluginArray.prototype",

		// MIME type spoofing (defineProperty form).
		"navigator, 'mimeTypes'",
		"MimeTypeArray.prototype",

		// Language spoofing.
		"navigator, 'languages'",

		// Permissions override.
		"permissions.query",
		"notifications",

		// Hardware spoofing.
		"hardwareConcurrency",
		"deviceMemory",

		// Network connection spoofing.
		"effectiveType",

		// Screen dimensions (defineProperty form: screen, 'availWidth').
		"screen, 'availWidth'",
		"screen, 'colorDepth'",
		"outerWidth",

		// Canvas fingerprinting.
		"_addCanvasNoise",
		"HTMLCanvasElement.prototype.toDataURL",
		"HTMLCanvasElement.prototype.toBlob",
		"CanvasRenderingContext2D.prototype.getImageData",
		"putImageData",

		// WebGL spoofing.
		"WebGLRenderingContext.prototype.getParameter",
		"WebGL2RenderingContext.prototype.getParameter",
		"37445",
		"3379",
		"getSupportedExtensions",
		"EXT_texture_filter_anisotropic",

		// AudioContext fingerprinting.
		"AudioContext.prototype.createOscillator",
		"AudioContext.prototype.createPeriodicWave",
		"OfflineAudioContext",

		// IntersectionObserver protection.
		"IntersectionObserver.prototype.observe",

		// Battery API spoofing.
		"navigator.getBattery",

		// Native-code masking of patched functions.
		"_mask",
		"[native code]",
		"Function.prototype.toString",

		// Font metrics noise.
		"measureText",

		// Timezone spoofing (DST-correct).
		"getTimezoneOffset",
		"formatToParts",

		// UA identity follows the UA override.
		"navigator, 'userAgentData'",
		"getHighEntropyValues",
		"navigator, 'platform'",
	}

	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Errorf("stealth script missing: %q", check)
		}
	}
}

func TestScriptIsValidJavaScript(t *testing.T) {
	script := buildTestScript()
	// Basic structural checks.
	if !strings.HasPrefix(strings.TrimSpace(script), "(function(){") {
		t.Error("script should start with IIFE")
	}
	if !strings.HasSuffix(strings.TrimSpace(script), "})();") {
		t.Error("script should end with IIFE invocation")
	}

	// Should not contain debugging statements.
	if strings.Contains(script, "console.log") {
		t.Error("script should not contain console.log")
	}
	if strings.Contains(script, "alert(") {
		t.Error("script should not contain alert")
	}

	// No unreplaced template tokens must leak into the payload.
	for _, tok := range []string{"__CANVAS_SEED__", "__TZ__", "__LANGS__", "__SCREEN_W__",
		"__GPU_VENDOR__", "__BATT_LEVEL__", "__PLUGIN_SET__", "__UA_SECTION__",
		"__UA_PLATFORM__", "__FONT_SEED__"} {
		if strings.Contains(script, tok) {
			t.Errorf("script contains unreplaced token %q", tok)
		}
	}
}

func TestBuildScriptStableAcrossCalls(t *testing.T) {
	// The same fingerprint must render byte-identical scripts so that
	// page recreation / re-injection never changes the identity.
	fp := GenerateFingerprint(rand.New(rand.NewSource(7)), GeoProfileByCode("jp"))
	a := BuildScript(fp, nil)
	b := BuildScript(fp, nil)
	if a != b {
		t.Fatal("BuildScript is not deterministic for the same fingerprint")
	}

	// UA identity participates in rendering.
	c := BuildScript(fp, &UAIdentity{Platform: "Windows"})
	if c == a {
		t.Fatal("UA identity should change the rendered script")
	}
}

func TestBuildScriptGeoCoherence(t *testing.T) {
	// With a proxy geo pinned to Tokyo, the baked languages/timezone
	// must be Japanese — this is the GeoIP-consistency guarantee.
	fp := GenerateFingerprint(rand.New(rand.NewSource(1)), GeoProfileByCode("jp"))
	script := BuildScript(fp, nil)
	if !strings.Contains(script, `"ja-JP"`) {
		t.Error("jp geo profile should bake ja-JP language")
	}
	if !strings.Contains(script, `"Asia/Tokyo"`) {
		t.Error("jp geo profile should bake Asia/Tokyo timezone")
	}
}

func TestGenerateFingerprintConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	fp := GenerateFingerprint(rng, nil)

	if fp.ScreenAvailHeight >= fp.ScreenHeight {
		t.Errorf("availHeight (%d) must be < screen height (%d)", fp.ScreenAvailHeight, fp.ScreenHeight)
	}
	if fp.ScreenAvailWidth != fp.ScreenWidth {
		t.Errorf("availWidth (%d) should equal width (%d) on desktop", fp.ScreenAvailWidth, fp.ScreenWidth)
	}
	if fp.HardwareConcurrency < 4 || fp.HardwareConcurrency > 16 {
		t.Errorf("implausible core count %d", fp.HardwareConcurrency)
	}
	if len(fp.Geo.Languages) == 0 || fp.Geo.Timezone == "" {
		t.Error("geo persona must carry languages and timezone")
	}

	// Determinism for a fixed seed.
	rng2 := rand.New(rand.NewSource(99))
	fp2 := GenerateFingerprint(rng2, nil)
	if fp.String() != fp2.String() {
		t.Error("GenerateFingerprint should be deterministic for the same seed")
	}
}

func TestGeoProfileByCode(t *testing.T) {
	if GeoProfileByCode("no-such-region") != nil {
		t.Error("unknown region should return nil")
	}
	for _, code := range []string{"cn", "us-east", "jp", "gb"} {
		if GeoProfileByCode(code) == nil {
			t.Errorf("region %q should resolve", code)
		}
	}
}

func TestInjectAction(t *testing.T) {
	// InjectAction should return a non-nil chromedp.Action.
	action := InjectAction(buildTestScript())
	if action == nil {
		t.Fatal("InjectAction() returned nil")
	}
	// Inject with an empty script is a no-op success.
	if err := Inject(nil, ""); err != nil {
		t.Fatalf("Inject with empty script should be a no-op, got %v", err)
	}
}
