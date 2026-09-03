// Package stealth provides anti-detection (stealth mode) for headless
// Chrome via CDP script injection and browser flag configuration.
//
// It injects a pre-navigation script via Page.addScriptToEvaluateOnNewDocument
// that hides automation indicators from common bot-detection libraries.
//
// 2026 reality check (see docs/ANTIBOT_GUIDE.md ch.18): production
// anti-bot (Cloudflare, Akamai, DataDome, PerimeterX) now detects at
// three layers JS injection cannot reach — the CDP protocol handshake,
// the TLS ClientHello, and statistical behavior modeling. JS-layer
// spoofing still defeats the long tail of fingerprinters
// (Sannysoft, CreepJS-class probes) and remains worth doing, but it
// must be CONSISTENT: a session-stable fingerprint (same GPU, screen,
// timezone across all documents), a UA that matches the real Chrome
// TLS stack, and timezone/locale that match the proxy exit region.
// This package implements those consistency guarantees:
//
//   - Fingerprint values are generated once per pool in Go and baked
//     into the script (no per-document re-randomisation — a "user"
//     whose GPU changes between navigations does not exist).
//   - Patched native functions are masked via Function.prototype.toString
//     so `toDataURL.toString()` still reports [native code].
//   - navigator.platform and navigator.userAgentData follow the UA
//     override instead of contradicting it.
//   - Font metrics (measureText) get stable per-session noise.
//
// Usage — Clone pool:
//
//	fp := stealth.GenerateFingerprint(rng, geo)
//	script := stealth.BuildScript(fp, uaIdentity)
//	pool := browser.NewPool(browser.PoolOptions{Stealth: true})
//
// Usage — General controller:
//
//	ctrl := browser.NewController(&config.BrowserConfig{Stealth: true})
package stealth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// UAIdentity carries the client-hints identity that must stay in sync
// with the User-Agent string override. When the UA is overridden via
// Network.setUserAgentOverride WITHOUT UserAgentMetadata, Chrome keeps
// sending its real Sec-CH-UA headers — the mismatch between a spoofed
// UA string and genuine client hints is a high-confidence bot signal.
type UAIdentity struct {
	// Brands is the Sec-CH-UA brand list: {"Chromium","130"},
	// {"Google Chrome","130"}, {"Not?A_Brand","99"}.
	Brands [][2]string
	// FullVersion e.g. "130.0.6721.117".
	FullVersion string
	// FullVersionList mirrors Brands with full versions.
	FullVersionList [][2]string
	// Platform: "Windows", "macOS", "Linux", "Android".
	Platform string
	// PlatformVersion e.g. "15.0.0" (Windows) or "10.15.7" (macOS).
	PlatformVersion string
	// Mobile is false for desktop personas.
	Mobile bool
	// Architecture / Bitness for getHighEntropyValues.
	Architecture string
	Bitness      string
}

// jsonLiteral marshals v for embedding into JavaScript.
func jsonLiteral(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// BuildScript renders the stealth payload with the given fingerprint
// baked in. ua may be nil — then the userAgentData/platform section is
// skipped and the browser's genuine (already consistent) values are
// kept. The returned script is stable: identical inputs yield an
// identical payload, so re-injection after page recreation does not
// change the observable fingerprint.
func BuildScript(fp *Fingerprint, ua *UAIdentity) string {
	if fp == nil {
		fp = &Fingerprint{Geo: geoProfiles[0]}
	}

	langs, _ := json.Marshal(fp.Geo.Languages)
	lang0 := fp.Geo.Languages[0]
	if lang0 == "" {
		lang0 = "en-US"
	}

	uaSection := buildUASection(ua)

	r := strings.NewReplacer(
		"__CANVAS_SEED__", fmt.Sprint(int32(fp.CanvasSeed)),
		"__AUDIO_SEED__", fmt.Sprint(int32(fp.AudioSeed)),
		"__FONT_SEED__", fmt.Sprint(int32(fp.FontSeed)),
		"__TZ__", jsonLiteral(fp.Geo.Timezone),
		"__TZ_FALLBACK__", fmt.Sprint(fp.Geo.TZOffsetMinutes),
		"__LANGS__", string(langs),
		"__LANG0__", jsonLiteral(lang0),
		"__SCREEN_W__", fmt.Sprint(fp.ScreenWidth),
		"__SCREEN_H__", fmt.Sprint(fp.ScreenHeight),
		"__SCREEN_AW__", fmt.Sprint(fp.ScreenAvailWidth),
		"__SCREEN_AH__", fmt.Sprint(fp.ScreenAvailHeight),
		"__COLOR_DEPTH__", fmt.Sprint(fp.ColorDepth),
		"__CORES__", fmt.Sprint(fp.HardwareConcurrency),
		"__MEM__", fmt.Sprint(fp.DeviceMemoryGB),
		"__GPU_VENDOR__", jsonLiteral(fp.GPU.Vendor),
		"__GPU_RENDERER__", jsonLiteral(fp.GPU.Renderer),
		"__GPU_MAXTEX__", fmt.Sprint(fp.GPU.MaxTex),
		"__RTT__", fmt.Sprint(fp.ConnectionRTT),
		"__DOWNLINK__", fmt.Sprintf("%.1f", fp.ConnectionDown),
		"__CONNTYPE__", jsonLiteral(fp.ConnectionType),
		"__BATT_LEVEL__", fmt.Sprintf("%.3f", fp.BatteryLevel),
		"__BATT_CHARGING__", fmt.Sprint(fp.BatteryCharging),
		"__PLUGIN_SET__", fmt.Sprint(fp.PluginSet%3),
		"__UA_SECTION__", uaSection,
	)
	return r.Replace(scriptTemplate)
}

// buildUASection renders the platform / userAgentData spoof, or an
// empty string when ua is nil (keep genuine values).
func buildUASection(ua *UAIdentity) string {
	if ua == nil {
		return ""
	}
	type brand struct {
		Brand   string `json:"brand"`
		Version string `json:"version"`
	}
	brands := make([]brand, 0, len(ua.Brands))
	for _, b := range ua.Brands {
		brands = append(brands, brand{Brand: b[0], Version: b[1]})
	}
	fullList := make([]brand, 0, len(ua.FullVersionList))
	for _, b := range ua.FullVersionList {
		fullList = append(fullList, brand{Brand: b[0], Version: b[1]})
	}
	if len(fullList) == 0 {
		fullList = brands
	}
	arch := ua.Architecture
	if arch == "" {
		arch = "x86"
	}
	bits := ua.Bitness
	if bits == "" {
		bits = "64"
	}

	return strings.NewReplacer(
		"__UA_BRANDS__", jsonLiteral(brands),
		"__UA_FULLVLIST__", jsonLiteral(fullList),
		"__UA_FULLV__", jsonLiteral(ua.FullVersion),
		"__UA_PLATFORM__", jsonLiteral(ua.Platform),
		"__UA_PLATFORM_VERSION__", jsonLiteral(ua.PlatformVersion),
		"__UA_MOBILE__", fmt.Sprint(ua.Mobile),
		"__UA_ARCH__", jsonLiteral(arch),
		"__UA_BITS__", jsonLiteral(bits),
	).Replace(uaSectionTemplate)
}

const uaSectionTemplate = `
	// =========================================================
	// 16. Platform + userAgentData — must follow the UA override.
	// =========================================================
	try {
		Object.defineProperty(navigator, 'platform', {
			get: function() { return __UA_PLATFORM__; },
			configurable: true
		});
	} catch(e) {}
	try {
		var _brands = __UA_BRANDS__;
		var _fullList = __UA_FULLVLIST__;
		var _uaData = {
			brands: _brands,
			mobile: __UA_MOBILE__,
			platform: __UA_PLATFORM__
		};
		_uaData.getHighEntropyValues = function(hints) {
			var out = {
				architecture: __UA_ARCH__,
				bitness: __UA_BITS__,
				brands: _brands,
				fullVersionList: _fullList,
				fullVersion: __UA_FULLV__,
				mobile: __UA_MOBILE__,
				model: '',
				platform: __UA_PLATFORM__,
				platformVersion: __UA_PLATFORM_VERSION__,
				uaFullVersion: __UA_FULLV__,
				wow64: false
			};
			var res = {};
			for (var i = 0; i < hints.length; i++) {
				if (Object.prototype.hasOwnProperty.call(out, hints[i])) {
					res[hints[i]] = out[hints[i]];
				}
			}
			return Promise.resolve(res);
		};
		Object.defineProperty(navigator, 'userAgentData', {
			get: function() { return _uaData; },
			configurable: true
		});
	} catch(e) {}
`

// scriptTemplate is the JS payload. All fingerprint values arrive via
// __TOKEN__ replacement — the script itself never calls Math.random
// for identity decisions (only for behaviour jitter), so the identity
// is identical across every document of the session.
const scriptTemplate = `(function(){
	'use strict';

	// =========================================================
	// 0. Native-code masking — patched functions must keep reporting
	//    [native code] through Function.prototype.toString, otherwise
	//    one line of detection unmaskes every hook below.
	// =========================================================
	var _origToString = Function.prototype.toString;
	var _masked = new WeakMap();
	function _mask(fn, name) {
		try { _masked.set(fn, 'function ' + name + '() { [native code] }'); } catch(e) {}
		return fn;
	}
	Function.prototype.toString = function() {
		var spoof = _masked.get(this);
		if (spoof !== undefined) return spoof;
		return _origToString.call(this);
	};
	_mask(Function.prototype.toString, 'toString');

	// =========================================================
	// Session-stable seeded PRNG (mulberry32). Same seed → same
	// canvas/audio/font noise on every page of the session, which is
	// what a real machine does.
	// =========================================================
	var _seed = __CANVAS_SEED__ | 0;
	function _rand() {
		_seed |= 0; _seed = (_seed + 0x6D2B79F5) | 0;
		var t = Math.imul(_seed ^ (_seed >>> 15), 1 | _seed);
		t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	}
	function _randInt(min, max) {
		return Math.floor(_rand() * (max - min + 1)) + min;
	}
	function _hashStr(s, seed) {
		var h = seed | 0;
		for (var i = 0; i < s.length; i++) {
			h = (h ^ s.charCodeAt(i)) * 16777619;
			h = h >>> 0;
		}
		return h >>> 0;
	}

	// =========================================================
	// 1. navigator.webdriver — the classic bot flag.
	// =========================================================
	try {
		Object.defineProperty(navigator, 'webdriver', {
			get: function() { return undefined; },
			configurable: true
		});
		delete navigator.__proto__.webdriver;
		delete Object.getPrototypeOf(navigator).webdriver;
	} catch(e) {}

	// =========================================================
	// 2. Chrome runtime — remove automation extension indicators.
	// =========================================================
	window.chrome = {
		runtime: {},
		loadTimes: function() {},
		csi: function() {},
		app: {
			isInstalled: false,
			InstallState: 'not_installed',
			RunningState: 'cannot_run'
		}
	};

	// =========================================================
	// 3. Plugins — realistic fixed combination (stable per session).
	// =========================================================
	try {
		var pluginSets = [
			[
				{name:'Chrome PDF Plugin', filename:'internal-pdf-viewer', description:'Portable Document Format', length:1},
				{name:'Chrome PDF Viewer', filename:'mhjfbmdgcfjbbpaeojofohoefgiehjai', description:'', length:1},
				{name:'Native Client', filename:'internal-nacl-plugin', description:'', length:2}
			],
			[
				{name:'Chrome PDF Plugin', filename:'internal-pdf-viewer', description:'Portable Document Format', length:1},
				{name:'Chrome PDF Viewer', filename:'mhjfbmdgcfjbbpaeojofohoefgiehjai', description:'', length:1}
			],
			[
				{name:'Chrome PDF Plugin', filename:'internal-pdf-viewer', description:'Portable Document Format', length:1},
				{name:'Native Client', filename:'internal-nacl-plugin', description:'', length:2}
			]
		];
		var _pluginArr = pluginSets[__PLUGIN_SET__].slice();
		_pluginArr.item = _mask(function(i){ return this[i]; }, 'item');
		_pluginArr.namedItem = _mask(function(n){ return null; }, 'namedItem');
		_pluginArr.refresh = _mask(function(){}, 'refresh');
		Object.setPrototypeOf(_pluginArr, PluginArray.prototype);
		Object.defineProperty(navigator, 'plugins', {
			get: function() { return _pluginArr; },
			configurable: true
		});

		var _mimeArr = [
			{type:'application/pdf', suffixes:'pdf', description:'Portable Document Format'},
			{type:'text/pdf', suffixes:'pdf', description:''}
		];
		_mimeArr.item = _mask(function(i){ return this[i]; }, 'item');
		_mimeArr.namedItem = _mask(function(n){ return null; }, 'namedItem');
		Object.setPrototypeOf(_mimeArr, MimeTypeArray.prototype);
		Object.defineProperty(navigator, 'mimeTypes', {
			get: function() { return _mimeArr; },
			configurable: true
		});
	} catch(e) {}

	// =========================================================
	// 4. Languages — coherent with the session's geo persona.
	// =========================================================
	var _langs = __LANGS__;
	Object.defineProperty(navigator, 'languages', {
		get: function() { return _langs; },
		configurable: true
	});
	Object.defineProperty(navigator, 'language', {
		get: function() { return __LANG0__; },
		configurable: true
	});

	// =========================================================
	// 5. Permissions — override notifications query.
	// =========================================================
	try {
		var origQuery = window.navigator.permissions.query.bind(window.navigator.permissions);
		window.navigator.permissions.query = _mask(function(params) {
			if (params && params.name === 'notifications') {
				return Promise.resolve({
					state: Notification.permission,
					onchange: null
				});
			}
			return origQuery(params);
		}, 'query');
	} catch(e) {}

	// =========================================================
	// 6. Hardware concurrency.
	// =========================================================
	Object.defineProperty(navigator, 'hardwareConcurrency', {
		get: function() { return __CORES__; },
		configurable: true
	});

	// =========================================================
	// 7. Device memory.
	// =========================================================
	Object.defineProperty(navigator, 'deviceMemory', {
		get: function() { return __MEM__; },
		configurable: true
	});

	// =========================================================
	// 8. Connection — spoof realistic network info.
	// =========================================================
	try {
		if (navigator.connection) {
			Object.defineProperty(navigator.connection, 'rtt', {
				get: function() { return __RTT__; },
				configurable: true
			});
			Object.defineProperty(navigator.connection, 'downlink', {
				get: function() { return __DOWNLINK__; },
				configurable: true
			});
			Object.defineProperty(navigator.connection, 'effectiveType', {
				get: function() { return __CONNTYPE__; },
				configurable: true
			});
		}
	} catch(e) {}

	// =========================================================
	// 9. Screen dimensions + window outer size (headless reports 0).
	// =========================================================
	try {
		Object.defineProperty(screen, 'width', { get: function() { return __SCREEN_W__; }, configurable: true });
		Object.defineProperty(screen, 'height', { get: function() { return __SCREEN_H__; }, configurable: true });
		Object.defineProperty(screen, 'availWidth', { get: function() { return __SCREEN_AW__; }, configurable: true });
		Object.defineProperty(screen, 'availHeight', { get: function() { return __SCREEN_AH__; }, configurable: true });
		Object.defineProperty(screen, 'colorDepth', { get: function() { return __COLOR_DEPTH__; }, configurable: true });
		Object.defineProperty(screen, 'pixelDepth', { get: function() { return __COLOR_DEPTH__; }, configurable: true });
		Object.defineProperty(window, 'outerWidth', { get: function() { return __SCREEN_AW__; }, configurable: true });
		Object.defineProperty(window, 'outerHeight', { get: function() { return __SCREEN_AH__; }, configurable: true });
	} catch(e) {}

	// =========================================================
	// 10. Canvas fingerprinting resistance — seeded, session-stable.
	// =========================================================
	try {
		var origToDataURL = HTMLCanvasElement.prototype.toDataURL;
		var origToBlob = HTMLCanvasElement.prototype.toBlob;
		var origGetImageData = CanvasRenderingContext2D.prototype.getImageData;

		function _addCanvasNoise(imgData) {
			var data = imgData.data;
			var len = data.length;
			var numPixels = Math.max(1, Math.floor(len / 2667));
			for (var i = 0; i < numPixels; i++) {
				var idx = _randInt(0, len / 4 - 1) * 4;
				data[idx] = Math.max(0, Math.min(255, data[idx] + _randInt(-3, 3)));
				data[idx + 1] = Math.max(0, Math.min(255, data[idx + 1] + _randInt(-3, 3)));
				data[idx + 2] = Math.max(0, Math.min(255, data[idx + 2] + _randInt(-3, 3)));
			}
		}

		HTMLCanvasElement.prototype.toDataURL = _mask(function(type) {
			var ctx = this.getContext('2d');
			if (ctx && this.width > 32 && this.height > 32) {
				var imgData = ctx.getImageData(0, 0, this.width, this.height);
				_addCanvasNoise(imgData);
				var tempCanvas = document.createElement('canvas');
				tempCanvas.width = this.width;
				tempCanvas.height = this.height;
				var tempCtx = tempCanvas.getContext('2d');
				tempCtx.putImageData(imgData, 0, 0);
				return origToDataURL.apply(tempCanvas, arguments);
			}
			return origToDataURL.apply(this, arguments);
		}, 'toDataURL');

		HTMLCanvasElement.prototype.toBlob = _mask(function(callback, type, quality) {
			var ctx = this.getContext('2d');
			if (ctx && this.width > 32 && this.height > 32) {
				var imgData = ctx.getImageData(0, 0, this.width, this.height);
				_addCanvasNoise(imgData);
				var tempCanvas = document.createElement('canvas');
				tempCanvas.width = this.width;
				tempCanvas.height = this.height;
				var tempCtx = tempCanvas.getContext('2d');
				tempCtx.putImageData(imgData, 0, 0);
				return origToBlob.apply(tempCanvas, arguments);
			}
			return origToBlob.apply(this, arguments);
		}, 'toBlob');

		if (origGetImageData) {
			CanvasRenderingContext2D.prototype.getImageData = _mask(function(sx, sy, sw, sh) {
				var result = origGetImageData.apply(this, arguments);
				if (sw * sh > 1024) {
					_addCanvasNoise(result);
				}
				return result;
			}, 'getImageData');
		}
	} catch(e) {}

	// =========================================================
	// 11. WebGL vendor spoofing — one fixed GPU per session.
	// =========================================================
	try {
		var _gpuVendor = __GPU_VENDOR__;
		var _gpuRenderer = __GPU_RENDERER__;
		var _gpuMaxTex = __GPU_MAXTEX__;

		var getParam = WebGLRenderingContext.prototype.getParameter;
		WebGLRenderingContext.prototype.getParameter = _mask(function(p) {
			if (p === 37445) return _gpuVendor;          // UNMASKED_VENDOR_WEBGL
			if (p === 37446) return _gpuRenderer;        // UNMASKED_RENDERER_WEBGL
			if (p === 3379) return _gpuMaxTex;           // MAX_TEXTURE_SIZE
			if (p === 3386) return [8192, 8192];          // MAX_VIEWPORT_DIMS
			return getParam.call(this, p);
		}, 'getParameter');

		if (typeof WebGL2RenderingContext !== 'undefined') {
			var getParam2 = WebGL2RenderingContext.prototype.getParameter;
			WebGL2RenderingContext.prototype.getParameter = _mask(function(p) {
				if (p === 37445) return _gpuVendor;
				if (p === 37446) return _gpuRenderer;
				if (p === 3379) return _gpuMaxTex;
				return getParam2.call(this, p);
			}, 'getParameter');
		}

		var origGetExtensions = WebGLRenderingContext.prototype.getSupportedExtensions;
		if (origGetExtensions) {
			WebGLRenderingContext.prototype.getSupportedExtensions = _mask(function() {
				var extensions = origGetExtensions.call(this);
				var common = ['EXT_texture_filter_anisotropic', 'OES_texture_float', 'OES_standard_derivatives'];
				var result = extensions.slice();
				for (var j = 0; j < common.length; j++) {
					if (result.indexOf(common[j]) === -1) result.push(common[j]);
				}
				return result;
			}, 'getSupportedExtensions');
		}
	} catch(e) {}

	// =========================================================
	// 12. AudioContext fingerprint randomization (session-stable).
	// =========================================================
	try {
		if (typeof AudioContext !== 'undefined') {
			var origCreateOscillator = AudioContext.prototype.createOscillator;
			var origCreatePeriodicWave = AudioContext.prototype.createPeriodicWave;

			AudioContext.prototype.createOscillator = _mask(function() {
				var osc = origCreateOscillator.call(this);
				var origGetFrequency = Object.getOwnPropertyDescriptor(OscillatorNode.prototype, 'frequency').get;
				Object.defineProperty(osc, 'frequency', {
					get: function() {
						var val = origGetFrequency.call(this);
						val.value = val.value * (1 + (_rand() - 0.5) * 0.001);
						return val;
					}
				});
				return osc;
			}, 'createOscillator');

			AudioContext.prototype.createPeriodicWave = _mask(function(real, imag) {
				var noiseReal = new Float32Array(real.length);
				var noiseImag = new Float32Array(imag.length);
				for (var i = 0; i < real.length; i++) {
					noiseReal[i] = real[i] * (1 + (_rand() - 0.5) * 0.0005);
					noiseImag[i] = imag[i] * (1 + (_rand() - 0.5) * 0.0005);
				}
				return origCreatePeriodicWave.call(this, noiseReal, noiseImag);
			}, 'createPeriodicWave');
		}

		if (typeof OfflineAudioContext !== 'undefined') {
			var origOfflineCreateOsc = OfflineAudioContext.prototype.createOscillator;
			OfflineAudioContext.prototype.createOscillator = _mask(function() {
				var osc = origOfflineCreateOsc.call(this);
				var origGetFreq2 = Object.getOwnPropertyDescriptor(OscillatorNode.prototype, 'frequency').get;
				Object.defineProperty(osc, 'frequency', {
					get: function() {
						var val = origGetFreq2.call(this);
						val.value = val.value * (1 + (_rand() - 0.5) * 0.001);
						return val;
					}
				});
				return osc;
			}, 'createOscillator');
		}
	} catch(e) {}

	// =========================================================
	// 13. Font metrics noise — defeats text-width font enumeration.
	//     Deterministic per (font,text) pair and per session, so the
	//     same probe always measures the same lie (like real raster).
	// =========================================================
	try {
		var _fontSeed = __FONT_SEED__ | 0;
		var origMeasureText = CanvasRenderingContext2D.prototype.measureText;
		CanvasRenderingContext2D.prototype.measureText = _mask(function(text) {
			var metrics = origMeasureText.call(this, text);
			try {
				var h = _hashStr(String(this.font || '') + '|' + String(text), _fontSeed);
				var jitterPx = ((h % 2001) - 1000) / 50000; // within ±0.02px
				var realWidth = metrics.width;
				Object.defineProperty(metrics, 'width', {
					get: function() { return realWidth + jitterPx; },
					configurable: true
				});
			} catch(e) {}
			return metrics;
		}, 'measureText');
	} catch(e) {}

	// =========================================================
	// 14. IntersectionObserver — prevent detection of invisible
	//     automation elements.
	// =========================================================
	try {
		var origObserve = IntersectionObserver.prototype.observe;
		IntersectionObserver.prototype.observe = _mask(function(target) {
			try { origObserve.call(this, target); } catch(e) {}
		}, 'observe');
	} catch(e) {}

	// =========================================================
	// 15. Battery API spoofing with a stable reading.
	// =========================================================
	try {
		if (navigator.getBattery) {
			var _battLevel = __BATT_LEVEL__;
			var _battCharging = __BATT_CHARGING__;
			navigator.getBattery = _mask(function() {
				return Promise.resolve({
					charging: _battCharging,
					chargingTime: _battCharging ? 1800 : Infinity,
					dischargingTime: !_battCharging ? 10800 : Infinity,
					level: _battLevel,
					onchargingchange: null,
					onchargingtimechange: null,
					ondischargingtimechange: null,
					onlevelchange: null
				});
			}, 'getBattery');
		}
	} catch(e) {}

	// =========================================================
	// 16b. Timezone — coherent with the session geo persona, and
	// DST-correct (offset derived from the zone, not a constant).
	// =========================================================
	try {
		var _tz = __TZ__;
		var _origDateTimeFormat = Intl.DateTimeFormat;
		var _patchedDateTimeFormat = function(locales, options) {
			var opts = options || {};
			if (!opts.timeZone) opts.timeZone = _tz;
			return new _origDateTimeFormat(locales, opts);
		};
		_patchedDateTimeFormat.prototype = _origDateTimeFormat.prototype;
		Object.assign(_patchedDateTimeFormat, _origDateTimeFormat);
		_patchedDateTimeFormat.supportedLocalesOf = function() {
			return _origDateTimeFormat.supportedLocalesOf.apply(_origDateTimeFormat, arguments);
		};
		Intl.DateTimeFormat = _patchedDateTimeFormat;

		function _zoneOffsetMS(date) {
			try {
				var dtf = new _origDateTimeFormat('en-US', {
					timeZone: _tz, hour12: false,
					year: 'numeric', month: '2-digit', day: '2-digit',
					hour: '2-digit', minute: '2-digit', second: '2-digit'
				});
				var parts = dtf.formatToParts(date);
				var m = {};
				for (var i = 0; i < parts.length; i++) m[parts[i].type] = parts[i].value;
				var asUTC = Date.UTC(
					parseInt(m.year, 10), parseInt(m.month, 10) - 1, parseInt(m.day, 10),
					parseInt(m.hour, 10) % 24, parseInt(m.minute, 10), parseInt(m.second, 10));
				return asUTC - date.getTime();
			} catch(e) {
				return -(__TZ_FALLBACK__) * 60000;
			}
		}
		Date.prototype.getTimezoneOffset = _mask(function() {
			return -_zoneOffsetMS(this) / 60000;
		}, 'getTimezoneOffset');
	} catch(e) {}
__UA_SECTION__
})();
`

// Inject adds a stealth script to the browser context so it executes
// before any page loads. Must be called once per browser instance,
// after the browser is started but before any navigation. Pass the
// script produced by BuildScript for the pool's fingerprint.
func Inject(ctx context.Context, script string) error {
	if script == "" {
		return nil
	}
	_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
	if err != nil {
		return fmt.Errorf("inject stealth script: %w", err)
	}
	return nil
}

// InjectAction returns a chromedp.Action that injects a stealth script.
func InjectAction(script string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return Inject(ctx, script)
	})
}
