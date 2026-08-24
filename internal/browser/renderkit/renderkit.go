// Package renderkit holds the render-pipeline pieces shared by the
// chromedp and rod browser backends: the JavaScript snippets injected
// during rendering (behavior simulation, scrolling, link extraction)
// and content-type classification. The two backends keep their own
// CDP plumbing — these constants are the contract that keeps their
// observable page behavior identical.
package renderkit

import "strings"

// BehaviorSimScrollJS scrolls a small random distance, mimicking a
// human nudging the wheel. Injected when behavior simulation is on.
const BehaviorSimScrollJS = `(async () => {
	// 随机滚动一小段距离
	const randomScroll = () => {
		const delta = Math.floor(Math.random() * 200) - 100;
		window.scrollBy(0, delta);
		return new Promise(r => setTimeout(r, 200 + Math.random() * 300));
	};
	await randomScroll();
})()`

// BehaviorSimMouseJS dispatches a mousemove event at a random viewport
// position and lingers briefly. Injected when behavior simulation is on.
const BehaviorSimMouseJS = `(async () => {
	// 模拟鼠标移动到随机位置
	const randomX = Math.random() * window.innerWidth;
	const randomY = Math.random() * window.innerHeight;
	// 触发鼠标移动事件
	const mouseEvent = new MouseEvent('mousemove', {
		clientX: randomX,
		clientY: randomY,
		bubbles: true
	});
	document.dispatchEvent(mouseEvent);
	// 模拟鼠标停留一会儿
	await new Promise(r => setTimeout(r, 150 + Math.random() * 350));
})()`

// ScrollJS scrolls the page one viewport at a time with randomized
// pauses (capped at 20 iterations), then jumps to the bottom if any
// remainder is left. Injected when the Scroll option is on.
const ScrollJS = `(async () => {
	const scrollHeight = document.documentElement.scrollHeight;
	const viewportHeight = window.innerHeight;
	let currentScroll = 0;
	const maxIterations = 20;
	let iterations = 0;

	while (currentScroll < scrollHeight - viewportHeight && iterations < maxIterations) {
		window.scrollBy(0, viewportHeight);
		currentScroll += viewportHeight;
		iterations++;
		await new Promise(r => setTimeout(r, 300 + Math.random() * 500));
	}

	if (currentScroll < scrollHeight - viewportHeight) {
		window.scrollTo(0, scrollHeight);
		await new Promise(r => setTimeout(r, 500));
	}
})()`

// CollectLinksJSBody collects every navigation-worthy link from the
// rendered DOM — anchors, image-map areas, and iframe/frame sources —
// into a Set named `links`. It is a body, not an expression: each
// backend wraps it with its own return statement.
//   - chromedp: CollectLinksArrayJS returns Array.from(links) and the
//     action decodes it into []string directly.
//   - rod: CollectLinksJSONJS returns JSON.stringify(Array.from(links))
//     because gson string handling is unrolled manually on that side.
const CollectLinksJSBody = `
	const links = new Set();
	document.querySelectorAll('a[href]').forEach(a => {
		const href = a.getAttribute('href');
		if (href && !href.startsWith('#') && !href.startsWith('javascript:') &&
			!href.startsWith('mailto:') && !href.startsWith('tel:')) {
			links.add(a.href);
		}
	});
	document.querySelectorAll('area[href]').forEach(area => {
		const href = area.getAttribute('href');
		if (href && !href.startsWith('#') && !href.startsWith('javascript:')) {
			links.add(area.href);
		}
	});
	document.querySelectorAll('iframe[src], frame[src]').forEach(f => {
		const src = f.getAttribute('src');
		if (src && !src.startsWith('javascript:')) {
			links.add(f.src);
		}
	});
`

// CollectLinksArrayJS wraps CollectLinksJSBody for chromedp's
// Evaluate, returning the link array for automatic JSON decoding.
const CollectLinksArrayJS = "(function() {" + CollectLinksJSBody + `
	return Array.from(links);
})()`

// CollectLinksJSONJS wraps CollectLinksJSBody for rod's Eval,
// returning the links as a JSON array string.
const CollectLinksJSONJS = "() => {" + CollectLinksJSBody + `
	return JSON.stringify(Array.from(links));
}`

// IsHTMLContentType reports whether a document content type is HTML.
// Empty counts as HTML (the caller could not determine it). The value
// is normalized: case-insensitive, surrounding whitespace trimmed, and
// any "; params" suffix stripped — so "Text/HTML; charset=utf-8"
// classifies as HTML just like "text/html".
func IsHTMLContentType(ct string) bool {
	ct = normalizeMIME(ct)
	if ct == "" {
		return true
	}
	return ct == "text/html" || ct == "application/xhtml+xml"
}

// IsTextContent reports whether a MIME type is textual (CSS, JS,
// JSON, XML, SVG, ...) and can therefore be read as plain text.
func IsTextContent(mimeType string) bool {
	mt := normalizeMIME(mimeType)
	return hasAnyPrefix(mt, "text/",
		"application/javascript",
		"application/json",
		"application/xml",
		"application/xhtml+xml",
		"image/svg+xml",
	)
}

func normalizeMIME(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if i := strings.Index(v, ";"); i >= 0 {
		v = v[:i]
	}
	return v
}

func hasAnyPrefix(v string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}
