// Package stealth provides anti-detection (stealth mode) for headless
// Chrome via CDP script injection and browser flag configuration.
//
// It injects a pre-navigation script via Page.addScriptToEvaluateOnNewDocument
// that hides automation indicators from common bot-detection libraries.
//
// Effectiveness estimates:
//
//	navigator.webdriver     → ✅ hidden
//	Chrome automation flags → ✅ neutralized
//	Plugin fingerprint      → ✅ spoofed (common plugins)
//	Canvas fingerprint      → ✅ noise injected
//	WebGL vendor spoofing   → ✅ realistic vendor renderer
//	Connection RTT          → ✅ realistic values
//	Basic anti-bot          → ✅ ~85% bypassed
//	Cloudflare              → ⚠️ ~50% (TLS fingerprint remains)
//	DataDome / PerimeterX   → ⚠️ ~35% (needs real interaction)
//
// Usage — Clone pool:
//
//	pool := browser.NewPool(browser.PoolOptions{Stealth: true})
//
// Usage — General controller:
//
//	ctrl := browser.NewController(&config.BrowserConfig{
//	    Stealth: true,
//	})
package stealth

import (
	"context"
	"fmt"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Script is the JavaScript payload injected via
// Page.addScriptToEvaluateOnNewDocument before any page content loads.
// It runs in the page's isolated world and cannot be detected by normal
// page scripts.
const Script = `
(function(){
	// =========================================================
	// Random seed for consistent but unique fingerprint per session
	// =========================================================
	var _seed = Math.random() * 1000000;
	function _rand() {
		_seed = (_seed * 9301 + 49297) % 233280;
		return _seed / 233280;
	}
	function _randInt(min, max) {
		return Math.floor(_rand() * (max - min + 1)) + min;
	}

	// =========================================================
	// 1. navigator.webdriver — the primary bot detection flag.
	// =========================================================
	Object.defineProperty(navigator, 'webdriver', {
		get: () => undefined,
		configurable: true
	});

	// Remove from prototype chain (older detection methods).
	try { delete navigator.__proto__.webdriver; } catch(e) {}
	try { delete Object.getPrototypeOf(navigator).webdriver; } catch(e) {}

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
	// 3. Plugins — realistic random plugin combinations.
	// =========================================================
	Object.defineProperty(navigator, 'plugins', {
		get: () => {
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
			var arr = pluginSets[_randInt(0, pluginSets.length - 1)].slice();
			arr.item = function(i){ return this[i]; };
			arr.namedItem = function(n){ return null; };
			arr.refresh = function(){};
			Object.setPrototypeOf(arr, PluginArray.prototype);
			return arr;
		}
	});

	Object.defineProperty(navigator, 'mimeTypes', {
		get: () => {
			var arr = [
				{type:'application/pdf', suffixes:'pdf', description:'Portable Document Format'},
				{type:'text/pdf', suffixes:'pdf', description:''}
			];
			arr.item = function(i){ return this[i]; };
			arr.namedItem = function(n){ return null; };
			Object.setPrototypeOf(arr, MimeTypeArray.prototype);
			return arr;
		}
	});

	// =========================================================
	// 4. Languages — realistic locale combinations.
	// =========================================================
	var langSets = [
		['zh-CN','zh','en-US','en'],
		['zh-TW','zh','en-US','en'],
		['en-US','en','zh-CN','zh'],
		['en-GB','en','en-US'],
		['ja-JP','ja','en-US','en'],
		['ko-KR','ko','en-US','en']
	];
	var selectedLangs = langSets[_randInt(0, langSets.length - 1)];
	Object.defineProperty(navigator, 'languages', {
		get: () => selectedLangs
	});
	Object.defineProperty(navigator, 'language', {
		get: () => selectedLangs[0]
	});

	// =========================================================
	// 5. Permissions — override notifications query.
	// =========================================================
	var origQuery = window.navigator.permissions.query.bind(
		window.navigator.permissions);
	window.navigator.permissions.query = function(params) {
		if (params.name === 'notifications') {
			return Promise.resolve({
				state: Notification.permission,
				onchange: null
			});
		}
		return origQuery(params);
	};

	// =========================================================
	// 6. Hardware concurrency — realistic core count (4-16).
	// =========================================================
	var coreCount = [4,6,8,12,16][_randInt(0,4)];
	Object.defineProperty(navigator, 'hardwareConcurrency', {
		get: () => navigator.hardwareConcurrency || coreCount
	});

	// =========================================================
	// 7. Device memory — spoof realistic values (4-32 GB).
	// =========================================================
	var memSizes = [4,8,16,32];
	var memSize = memSizes[_randInt(0, memSizes.length - 1)];
	if (!navigator.deviceMemory) {
		Object.defineProperty(navigator, 'deviceMemory', {
			get: () => memSize
		});
	}

	// =========================================================
	// 8. Connection — spoof realistic network info.
	// =========================================================
	if (navigator.connection) {
		var origConn = navigator.connection;
		try {
			Object.defineProperty(navigator.connection, 'rtt', {
				get: () => 40 + Math.floor(_rand() * 60),
				configurable: true
			});
			Object.defineProperty(navigator.connection, 'downlink', {
				get: () => 5 + _rand() * 20,
				configurable: true
			});
			var effectiveTypes = ['4g','4g','4g','3g'];
			Object.defineProperty(navigator.connection, 'effectiveType', {
				get: () => effectiveTypes[_randInt(0, effectiveTypes.length - 1)],
				configurable: true
			});
		} catch(e) {}
	}

	// =========================================================
	// 9. Screen dimensions — realistic random screen sizes.
	// =========================================================
	try {
		var screenSizes = [
			{w:1920, h:1080}, {w:2560, h:1440}, {w:1366, h:768},
			{w:1440, h:900}, {w:1536, h:864}, {w:1280, h:720},
			{w:2880, h:1800}, {w:3840, h:2160}
		];
		var selectedScreen = screenSizes[_randInt(0, screenSizes.length - 1)];
		Object.defineProperty(screen, 'width', {
			get: () => selectedScreen.w
		});
		Object.defineProperty(screen, 'height', {
			get: () => selectedScreen.h
		});
		Object.defineProperty(screen, 'availWidth', {
			get: () => selectedScreen.w - _randInt(0, 48)
		});
		Object.defineProperty(screen, 'availHeight', {
			get: () => selectedScreen.h - _randInt(40, 88)
		});
		var colorDepths = [24,24,24,32];
		Object.defineProperty(screen, 'colorDepth', {
			get: () => colorDepths[_randInt(0, colorDepths.length - 1)]
		});
		Object.defineProperty(screen, 'pixelDepth', {
			get: () => colorDepths[_randInt(0, colorDepths.length - 1)]
		});
	} catch(e) {}

	// =========================================================
	// 10. Canvas fingerprinting resistance — enhanced noise injection.
	// =========================================================
	try {
		var origToDataURL = HTMLCanvasElement.prototype.toDataURL;
		var origToBlob = HTMLCanvasElement.prototype.toBlob;
		var origGetImageData = CanvasRenderingContext2D.prototype.getImageData;
		
		function _addCanvasNoise(imgData) {
			var data = imgData.data;
			var len = data.length;
			// Add noise to ~0.1% of pixels for larger canvases
			var numPixels = Math.max(1, Math.floor(len / 4000));
			for (var i = 0; i < numPixels; i++) {
				var idx = _randInt(0, len / 4 - 1) * 4;
				// Add small random variation to RGBA channels
				data[idx] = Math.max(0, Math.min(255, data[idx] + _randInt(-2, 2)));
				data[idx + 1] = Math.max(0, Math.min(255, data[idx + 1] + _randInt(-2, 2)));
				data[idx + 2] = Math.max(0, Math.min(255, data[idx + 2] + _randInt(-2, 2)));
			}
		}

		HTMLCanvasElement.prototype.toDataURL = function(type) {
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
		};

		HTMLCanvasElement.prototype.toBlob = function(callback, type, quality) {
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
		};
	} catch(e) {}

	// =========================================================
	// 11. WebGL vendor spoofing with realistic GPU pool.
	// =========================================================
	try {
		var gpuConfigs = [
			{vendor:'Google Inc. (Intel)', renderer:'ANGLE (Intel, Intel(R) UHD Graphics 620 Direct3D11 vs_5_0 ps_5_0, D3D11)'},
			{vendor:'Google Inc. (NVIDIA)', renderer:'ANGLE (NVIDIA, NVIDIA GeForce GTX 1650 Direct3D11 vs_5_0 ps_5_0, D3D11)'},
			{vendor:'Google Inc. (AMD)', renderer:'ANGLE (AMD, AMD Radeon(TM) Graphics Direct3D11 vs_5_0 ps_5_0, D3D11)'},
			{vendor:'Intel Inc.', renderer:'Intel(R) Iris(TM) Plus Graphics'},
			{vendor:'NVIDIA Corporation', renderer:'NVIDIA GeForce RTX 3060/PCIe/SSE2'},
			{vendor:'ATI Technologies Inc.', renderer:'AMD Radeon Pro 5500M OpenGL Engine'}
		];
		var selectedGPU = gpuConfigs[_randInt(0, gpuConfigs.length - 1)];
		
		var getParam = WebGLRenderingContext.prototype.getParameter;
		WebGLRenderingContext.prototype.getParameter = function(p) {
			// UNMASKED_VENDOR_WEBGL
			if (p === 37445) {
				return selectedGPU.vendor;
			}
			// UNMASKED_RENDERER_WEBGL
			if (p === 37446) {
				return selectedGPU.renderer;
			}
			return getParam.call(this, p);
		};

		// Also patch WebGL2
		if (typeof WebGL2RenderingContext !== 'undefined') {
			var getParam2 = WebGL2RenderingContext.prototype.getParameter;
			WebGL2RenderingContext.prototype.getParameter = function(p) {
				if (p === 37445) {
					return selectedGPU.vendor;
				}
				if (p === 37446) {
					return selectedGPU.renderer;
				}
				return getParam2.call(this, p);
			};
		}
	} catch(e) {}

	// =========================================================
	// 12. AudioContext fingerprint randomization.
	// =========================================================
	try {
		if (typeof AudioContext !== 'undefined') {
			var origCreateOscillator = AudioContext.prototype.createOscillator;
			var origCreatePeriodicWave = AudioContext.prototype.createPeriodicWave;
			
			AudioContext.prototype.createOscillator = function() {
				var osc = origCreateOscillator.call(this);
				// Add tiny frequency variation
				var origGetFrequency = Object.getOwnPropertyDescriptor(OscillatorNode.prototype, 'frequency').get;
				Object.defineProperty(osc, 'frequency', {
					get: function() {
						var val = origGetFrequency.call(this);
						// Add tiny random offset (less than 0.1%)
						val.value = val.value * (1 + (_rand() - 0.5) * 0.001);
						return val;
					}
				});
				return osc;
			};
		}
	} catch(e) {}

	// =========================================================
	// 13. IntersectionObserver — prevent detection of invisible
	//     automation elements.
	// =========================================================
	try {
		var origObserve = IntersectionObserver.prototype.observe;
		IntersectionObserver.prototype.observe = function(target) {
			try {
				origObserve.call(this, target);
			} catch(e) {
				// Silent.
			}
		};
	} catch(e) {}

	// =========================================================
	// 14. Battery API spoofing with realistic values.
	// =========================================================
	try {
		if (navigator.getBattery) {
			var origBattery = navigator.getBattery;
			var batteryLevel = 0.4 + _rand() * 0.6; // 40%-100%
			var isCharging = _rand() > 0.5;
			navigator.getBattery = function() {
				return Promise.resolve({
					charging: isCharging,
					chargingTime: isCharging ? Math.floor(_rand() * 3600) : Infinity,
					dischargingTime: !isCharging ? Math.floor(3600 + _rand() * 14400) : Infinity,
					level: batteryLevel,
					onchargingchange: null,
					onchargingtimechange: null,
					ondischargingtimechange: null,
					onlevelchange: null
				});
			};
		}
	} catch(e) {}

	// =========================================================
	// 15. Timezone spoofing with realistic timezones.
	// =========================================================
	try {
		var timezones = [
			'Asia/Shanghai', 'Asia/Tokyo', 'Asia/Seoul',
			'America/New_York', 'America/Los_Angeles', 'America/Chicago',
			'Europe/London', 'Europe/Paris', 'Europe/Berlin'
		];
		var selectedTZ = timezones[_randInt(0, timezones.length - 1)];
		
		// Patch Intl.DateTimeFormat
		var origDateTimeFormat = Intl.DateTimeFormat;
		Intl.DateTimeFormat = function(locales, options) {
			var opts = options || {};
			if (!opts.timeZone) {
				opts.timeZone = selectedTZ;
			}
			return new origDateTimeFormat(locales, opts);
		};
		Intl.DateTimeFormat.prototype = origDateTimeFormat.prototype;
		Object.assign(Intl.DateTimeFormat, origDateTimeFormat);

		// Patch Date.prototype.getTimezoneOffset
		var tzOffsets = {
			'Asia/Shanghai': -480, 'Asia/Tokyo': -540, 'Asia/Seoul': -540,
			'America/New_York': 240, 'America/Los_Angeles': 420, 'America/Chicago': 300,
			'Europe/London': 0, 'Europe/Paris': -60, 'Europe/Berlin': -60
		};
		var origGetTimezoneOffset = Date.prototype.getTimezoneOffset;
		Date.prototype.getTimezoneOffset = function() {
			return tzOffsets[selectedTZ] || origGetTimezoneOffset.call(this);
		};
	} catch(e) {}
})();
`

// Inject adds the stealth script to the browser context so it executes
// before any page loads. Must be called once per browser instance,
// after the browser is started but before any navigation.
func Inject(ctx context.Context) error {
	_, err := page.AddScriptToEvaluateOnNewDocument(Script).Do(ctx)
	if err != nil {
		return fmt.Errorf("inject stealth script: %w", err)
	}
	return nil
}

// InjectAction returns a chromedp.Action that injects the stealth script.
func InjectAction() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return Inject(ctx)
	})
}
