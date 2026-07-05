package stealth

import (
	"strings"
	"testing"
)

func TestScriptContainsKeySpoofs(t *testing.T) {
	// Verify the stealth script contains all critical anti-detection measures.
	// 检查项以脚本中实际使用的语法为准 (defineProperty 形式).
	checks := []string{
		// Primary bot detection flag.
		"navigator.webdriver",
		"undefined",

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
		"zh-CN",
		"en-US",

		// Permissions override.
		"permissions.query",
		"notifications",

		// Hardware spoofing.
		"hardwareConcurrency",
		"deviceMemory",

		// Network connection spoofing.
		"navigator.connection",
		"effectiveType",

		// Screen dimensions (defineProperty form: screen, 'availWidth').
		"screen, 'availWidth'",
		"screen, 'colorDepth'",

		// Canvas fingerprinting.
		"HTMLCanvasElement.prototype.toDataURL",
		"getImageData",
		"putImageData",

		// WebGL spoofing.
		"WebGLRenderingContext.prototype.getParameter",
		"UNMASKED_VENDOR_WEBGL",
		"Intel",

		// IntersectionObserver protection.
		"IntersectionObserver.prototype.observe",

		// Battery API spoofing.
		"navigator.getBattery",
		"0.76",
	}

	for _, check := range checks {
		if !strings.Contains(Script, check) {
			t.Errorf("stealth script missing: %q", check)
		}
	}
}

func TestScriptIsValidJavaScript(t *testing.T) {
	// Basic structural checks.
	if !strings.HasPrefix(strings.TrimSpace(Script), "(function()") {
		t.Error("script should start with IIFE")
	}
	if !strings.HasSuffix(strings.TrimSpace(Script), ")();") {
		t.Error("script should end with IIFE invocation")
	}

	// Should not contain debugging statements.
	if strings.Contains(Script, "console.log") {
		t.Error("script should not contain console.log")
	}
	if strings.Contains(Script, "alert") {
		t.Error("script should not contain alert")
	}
}

func TestInjectAction(t *testing.T) {
	// InjectAction should return a non-nil chromedp.Action.
	action := InjectAction()
	if action == nil {
		t.Fatal("InjectAction() returned nil")
	}
	// The action itself can't be tested without a real browser,
	// but we can verify it's properly constructed.
}
