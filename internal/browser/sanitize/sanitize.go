package sanitize

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Options tune sanitization behaviors.
type Options struct {
	KeepNoscript    bool
	KeepMetaRefresh bool
	Banner          string
	MobileReadable  bool
}

// Report counts what was removed.
type Report struct {
	ScriptsRemoved      int
	HandlersRemoved     int
	NoscriptRemoved     int
	NoscriptUnwrapped   int
	JSURLsNeutralized   int
	MetaRefreshRemoved  int
	DeadLinksRemoved    int
	CondCommentsRemoved int
	CharsetAdded        bool
}

var jsURLAttrs = map[string]bool{
	"href": true, "src": true, "action": true, "formaction": true,
	"poster": true, "data": true, "background": true, "xlink:href": true,
}

// Strip removes all JavaScript from HTML and returns rewritten HTML.
func Strip(doc []byte, opts Options) ([]byte, Report, error) {
	root, err := html.Parse(bytes.NewReader(doc))
	if err != nil {
		return nil, Report{}, err
	}
	rep := CleanTree(root, opts)
	var buf bytes.Buffer
	if err := html.Render(&buf, root); err != nil {
		return nil, rep, err
	}
	return buf.Bytes(), rep, nil
}

// CleanTree removes JavaScript from parsed DOM in place.
func CleanTree(root *html.Node, opts Options) Report {
	var rep Report
	clean(root, opts, &rep)
	rep.CharsetAdded = ensureCharset(root)
	if opts.MobileReadable {
		ensureViewport(root)
		injectMobileCSS(root)
	}
	if opts.Banner != "" {
		insertBanner(root, opts.Banner)
	}
	return rep
}

func clean(n *html.Node, opts Options, rep *Report) {
	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling
		if c.Type == html.CommentNode {
			if isConditionalComment(c.Data) {
				n.RemoveChild(c)
				rep.CondCommentsRemoved++
			}
			continue
		}
		if c.Type == html.ElementNode {
			switch c.DataAtom {
			case atom.Script:
				n.RemoveChild(c)
				rep.ScriptsRemoved++
				continue
			case atom.Noscript:
				if opts.KeepNoscript {
					unwrapNoscript(n, c)
					rep.NoscriptUnwrapped++
				} else {
					n.RemoveChild(c)
					rep.NoscriptRemoved++
				}
				continue
			case atom.Meta:
				if isMetaRefresh(c) && (!opts.KeepMetaRefresh || isJSRefresh(c)) {
					n.RemoveChild(c)
					rep.MetaRefreshRemoved++
					continue
				}
			case atom.Link:
				if isDeadLink(c) {
					n.RemoveChild(c)
					rep.DeadLinksRemoved++
					continue
				}
			}
			stripHandlers(c, rep)
			neutralizeJSURLs(c, rep)
		}
		clean(c, opts, rep)
	}
}

func stripHandlers(n *html.Node, rep *Report) {
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		if len(a.Key) > 2 && strings.HasPrefix(strings.ToLower(a.Key), "on") {
			rep.HandlersRemoved++
			continue
		}
		kept = append(kept, a)
	}
	n.Attr = kept
}

func neutralizeJSURLs(n *html.Node, rep *Report) {
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		key := strings.ToLower(a.Key)
		if jsURLAttrs[key] && strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "javascript:") {
			rep.JSURLsNeutralized++
			if key == "href" {
				a.Val = "#"
				kept = append(kept, a)
			}
			continue
		}
		kept = append(kept, a)
	}
	n.Attr = kept
}

func isMetaRefresh(n *html.Node) bool {
	return strings.EqualFold(attr(n, "http-equiv"), "refresh")
}

func isJSRefresh(n *html.Node) bool {
	return strings.Contains(strings.ToLower(attr(n, "content")), "javascript:")
}

func isDeadLink(n *html.Node) bool {
	for _, r := range strings.Fields(strings.ToLower(attr(n, "rel"))) {
		switch r {
		case "preconnect", "dns-prefetch", "modulepreload":
			return true
		case "preload", "prefetch":
			as := strings.ToLower(attr(n, "as"))
			href := strings.ToLower(attr(n, "href"))
			if as == "script" || strings.HasSuffix(href, ".js") {
				return true
			}
		}
	}
	return false
}

func isConditionalComment(data string) bool {
	d := strings.TrimSpace(data)
	return strings.HasPrefix(d, "[if") ||
		strings.HasPrefix(d, "<![endif]") ||
		strings.HasPrefix(d, "[endif]")
}

func unwrapNoscript(parent, ns *html.Node) {
	var raw strings.Builder
	for c := ns.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			raw.WriteString(c.Data)
		}
	}
	frag, err := html.ParseFragment(strings.NewReader(raw.String()), &html.Node{
		Type:     html.ElementNode,
		Data:     "body",
		DataAtom: atom.Body,
	})
	if err == nil {
		for _, fn := range frag {
			parent.InsertBefore(fn, ns)
		}
	}
	parent.RemoveChild(ns)
}

func ensureCharset(root *html.Node) bool {
	head := findElement(root, atom.Head)
	if head == nil {
		return false
	}
	if hasCharsetMeta(head) {
		return false
	}
	meta := &html.Node{
		Type:     html.ElementNode,
		Data:     "meta",
		DataAtom: atom.Meta,
		Attr:     []html.Attribute{{Key: "charset", Val: "utf-8"}},
	}
	head.InsertBefore(meta, head.FirstChild)
	return true
}

func hasCharsetMeta(head *html.Node) bool {
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.DataAtom != atom.Meta {
			continue
		}
		if attr(c, "charset") != "" {
			return true
		}
		if strings.EqualFold(attr(c, "http-equiv"), "content-type") &&
			strings.Contains(strings.ToLower(attr(c, "content")), "charset=") {
			return true
		}
	}
	return false
}

func findElement(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findElement(c, a); found != nil {
			return found
		}
	}
	return nil
}

const mobileCSS = `*{box-sizing:border-box}` +
	`:root{font-size:18px}` +
	`body{margin:0;padding:.75em 1em;line-height:1.7;font-family:Georgia,"Times New Roman",serif;overflow-x:hidden}` +
	`font{font-size:1rem!important;font-family:inherit!important;color:inherit!important}` +
	`[width]{width:auto!important;max-width:100%!important}` +
	`[height]{height:auto!important}` +
	`table{width:100%!important;max-width:100%!important;table-layout:auto!important;border-collapse:collapse!important;word-break:break-word}` +
	`td,th{width:auto!important;max-width:100%!important;padding:.35em .5em!important;vertical-align:top!important;overflow-wrap:break-word}` +
	`img{max-width:100%!important;height:auto!important}` +
	`img[usemap],map{display:none!important}` +
	`td:has(>img[usemap]),td:has(>map){display:none!important}` +
	`img[src*="trans_1x1"],img[src*="spacer"],img[height="1"],img[width="1"]{display:none!important}` +
	`td:has(>img[src*="trans_1x1"]:only-child),td:has(>img[height="1"]:only-child){display:none!important}`

func ensureViewport(root *html.Node) {
	head := findElement(root, atom.Head)
	if head == nil {
		return
	}
	for c := head.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Meta &&
			strings.EqualFold(attr(c, "name"), "viewport") {
			return
		}
	}
	meta := &html.Node{
		Type:     html.ElementNode,
		Data:     "meta",
		DataAtom: atom.Meta,
		Attr: []html.Attribute{
			{Key: "name", Val: "viewport"},
			{Key: "content", Val: "width=device-width, initial-scale=1"},
		},
	}
	head.InsertBefore(meta, head.FirstChild)
}

func injectMobileCSS(root *html.Node) {
	head := findElement(root, atom.Head)
	if head == nil {
		return
	}
	style := &html.Node{
		Type:     html.ElementNode,
		Data:     "style",
		DataAtom: atom.Style,
	}
	style.AppendChild(&html.Node{Type: html.TextNode, Data: mobileCSS})
	head.AppendChild(style)
}

func insertBanner(root *html.Node, text string) {
	c := &html.Node{Type: html.CommentNode, Data: " " + text + " "}
	if root.FirstChild != nil {
		root.InsertBefore(c, root.FirstChild)
	} else {
		root.AppendChild(c)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}
