package epub

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	opfNS       = "http://www.idpf.org/2007/opf"
	xhtmlNS     = "http://www.w3.org/1999/xhtml"
	containerNS = "urn:oasis:names:tc:opendocument:xmlns:container"
)

type xmlValue struct {
	XMLName xml.Name
	ID      string `xml:"id,attr"`
	Text    string `xml:",chardata"`
}
type manifestItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
	Fallback   string `xml:"fallback,attr"`
}
type packageDoc struct {
	XMLName          xml.Name
	Version          string `xml:"version,attr"`
	UniqueIdentifier string `xml:"unique-identifier,attr"`
	Metadata         struct {
		Titles      []xmlValue `xml:"title"`
		Creators    []xmlValue `xml:"creator"`
		Languages   []xmlValue `xml:"language"`
		Identifiers []xmlValue `xml:"identifier"`
		Meta        []struct {
			Name     string `xml:"name,attr"`
			Content  string `xml:"content,attr"`
			Property string `xml:"property,attr"`
			Value    string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest []manifestItem `xml:"manifest>item"`
	Spine    struct {
		TOC   string `xml:"toc,attr"`
		Items []struct {
			IDRef  string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

var utf16Declaration = regexp.MustCompile(`(?i)(encoding\s*=\s*["'])utf-16(?:le|be)?(["'])`)

func utf8Input(b []byte) []byte {
	label := ""
	if bytes.HasPrefix(b, []byte{0xff, 0xfe}) {
		label = "utf-16le"
		b = b[2:]
	} else if bytes.HasPrefix(b, []byte{0xfe, 0xff}) {
		label = "utf-16be"
		b = b[2:]
	} else if bytes.HasPrefix(b, []byte{'<', 0, '?', 0}) {
		label = "utf-16le"
	} else if bytes.HasPrefix(b, []byte{0, '<', 0, '?'}) {
		label = "utf-16be"
	}
	if label != "" {
		if r, e := charset.NewReaderLabel(label, bytes.NewReader(b)); e == nil {
			if decoded, e := io.ReadAll(r); e == nil {
				return utf16Declaration.ReplaceAll(decoded, []byte("${1}UTF-8${2}"))
			}
		}
	}
	return bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
}
func decoder(b []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(utf8Input(b)))
	d.CharsetReader = charset.NewReaderLabel
	return d
}

func validateXML(b []byte) (xml.Name, error) {
	d := decoder(b)
	var root xml.Name
	depth := 0
	roots := 0
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return root, err
		}
		switch t := t.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots > 1 {
					return root, fmt.Errorf("XML has multiple root elements")
				}
				root = t.Name
			}
			depth++
			if depth > 1024 {
				return root, fmt.Errorf("XML nesting exceeds the 1,024-level scan limit")
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(t)) != "" {
				return root, fmt.Errorf("text outside the XML document root")
			}
		}
	}
	if roots != 1 {
		return root, fmt.Errorf("XML root element is missing")
	}
	return root, nil
}

func resolveReference(base, href string) (target, fragment string, external bool, err error) {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return "", "", false, err
	}
	if u.Scheme != "" || u.Host != "" {
		return "", u.Fragment, true, nil
	}
	p := u.Path
	if strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") {
		return "", "", false, fmt.Errorf("resource path is absolute or contains forbidden characters")
	}
	if p == "" {
		p = base
	} else {
		p = path.Join(path.Dir(base), p)
	}
	if !safeName(p) {
		return "", "", false, fmt.Errorf("resource path escapes the EPUB container")
	}
	return p, u.Fragment, false, nil
}

type reference struct{ href, attribute string }
type contentInfo struct {
	ids           map[string]bool
	refs          []reference
	words, pages  int
	nav, pageList bool
	rootNamespace string
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}
func tokenHas(s, t string) bool {
	for _, f := range strings.Fields(s) {
		if f == t {
			return true
		}
	}
	return false
}

func wordCount(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			n++
			inWord = false
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if !inWord {
				n++
			}
			inWord = true
		} else if (r == '\'' || r == '’' || r == '-') && inWord {
		} else {
			inWord = false
		}
	}
	return n
}

func (a *archive) inspectContent(name string, b []byte) contentInfo {
	info := contentInfo{ids: map[string]bool{}}
	input := utf8Input(b)
	// Count tokens before building a DOM. Checking the completed tree alone lets
	// a small file containing millions of tiny tags allocate excessive memory.
	tokenizer := html.NewTokenizer(bytes.NewReader(input))
	for tokens := 0; ; tokens++ {
		if a.ctx.Err() != nil { return info }
		if tokens >= 200000 {
			a.issue("CONTENT_COMPLEXITY", "error", "Content exceeds structural scan limits", "More than 200,000 HTML tokens. This document was not loaded into a DOM; word counts and references may be incomplete.", name, false)
			return info
		}
		if tokenizer.Next() == html.ErrorToken { break }
	}
	doc, err := html.Parse(bytes.NewReader(input))
	if err != nil {
		return info
	}
	var text strings.Builder
	nodes := 0
	truncated := false
	var walk func(*html.Node, bool, bool, bool, int)
	walk = func(n *html.Node, inBody, hidden, inPageList bool, depth int) {
		nodes++
		if nodes > 200000 || depth > 1024 {
			if !truncated {
				a.issue("CONTENT_COMPLEXITY", "error", "Content exceeds structural scan limits", "The scan stopped after 200,000 nodes or 1,024 nesting levels. Word counts and references may be incomplete.", name, false)
				truncated = true
			}
			return
		}
		if n.Type == html.ElementNode {
			if n.Data == "body" {
				inBody = true
			}
			hidden = hidden || n.Data == "script" || n.Data == "style" || n.Data == "nav" || n.Data == "noscript" || n.Data == "template" || hasAttr(n, "hidden") || attr(n, "aria-hidden") == "true" || strings.Contains(strings.ReplaceAll(strings.ToLower(attr(n, "style")), " ", ""), "display:none")
			if tokenHas("address article aside blockquote br dd div dl dt figcaption figure footer h1 h2 h3 h4 h5 h6 header hr li main ol p pre section table td th tr ul", n.Data) {
				text.WriteByte(' ')
			}
			if id := attr(n, "id"); id != "" {
				if info.ids[id] {
					a.issue("CONTENT_DUPLICATE_ID", "warning", "Duplicate content identifier", "Links to this identifier may be ambiguous: "+id, name, false)
				}
				info.ids[id] = true
			}
			if id := attr(n, "name"); id != "" && n.Data == "a" {
				info.ids[id] = true
			}
			for _, at := range n.Attr {
				if at.Key == "href" || at.Key == "src" || at.Key == "poster" || (at.Key == "data" && n.Data == "object") {
					info.refs = append(info.refs, reference{at.Val, n.Data + "@" + at.Key})
				}
				if at.Key == "style" {
					info.refs = append(info.refs, cssReferences([]byte(at.Val))...)
				}
			}
			etype := attr(n, "epub:type")
			if etype == "" {
				etype = attr(n, "type")
			}
			if n.Data == "nav" && tokenHas(etype, "toc") {
				info.nav = true
			}
			if n.Data == "nav" && tokenHas(etype, "page-list") {
				info.pageList = true
				inPageList = true
			}
			if tokenHas(etype, "pagebreak") || attr(n, "role") == "doc-pagebreak" {
				info.pages++
			}
			if inPageList && n.Data == "a" {
				info.pages++
			}
			if n.Data == "script" {
				a.issue("CONTENT_SCRIPT", "warning", "Scripted content", "Scripts can increase reader workload. This is a compatibility advisory, not a Kobo device limit.", name, false)
			}
			if n.Data == "iframe" {
				a.issue("CONTENT_IFRAME", "warning", "Embedded frame", "Embedded frames may not be supported by all e-readers.", name, false)
			}
		}
		if n.Type == html.TextNode && inBody && !hidden {
			text.WriteString(n.Data)
		}
		if n.Type == html.TextNode && n.Parent != nil && n.Parent.Data == "style" {
			info.refs = append(info.refs, cssReferences([]byte(n.Data))...)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inBody, hidden, inPageList, depth+1)
		}
		if n.Type == html.ElementNode && tokenHas("address article aside blockquote br dd div dl dt figcaption figure footer h1 h2 h3 h4 h5 h6 header hr li main ol p pre section table td th tr ul", n.Data) {
			text.WriteByte(' ')
		}
	}
	walk(doc, false, false, false, 0)
	info.words = wordCount(text.String())
	return info
}

func inspectXMLReferences(b []byte) contentInfo {
	info := contentInfo{ids: map[string]bool{}}
	d := decoder(b)
	for {
		t, err := d.Token()
		if err != nil {
			break
		}
		start, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local == "pageList" {
			info.pageList = true
		}
		if start.Name.Local == "pageTarget" {
			info.pages++
		}
		for _, at := range start.Attr {
			if at.Name.Local == "id" {
				info.ids[at.Value] = true
			}
			if at.Name.Local == "href" || at.Name.Local == "src" {
				info.refs = append(info.refs, reference{at.Value, start.Name.Local + "@" + at.Name.Local})
			}
			if at.Name.Local == "style" {
				info.refs = append(info.refs, cssReferences([]byte(at.Value))...)
			}
		}
	}
	return info
}

var cssURL = regexp.MustCompile(`(?i)url\(\s*["']?([^"')]+)["']?\s*\)`)
var cssImport = regexp.MustCompile(`(?i)@import\s+["']([^"']+)["']`)

func cssReferences(b []byte) []reference {
	var refs []reference
	b = stripCSSComments(b)
	for _, m := range cssURL.FindAllSubmatch(b, maxIssues) {
		refs = append(refs, reference{string(bytes.TrimSpace(m[1])), "css url"})
	}
	for _, m := range cssImport.FindAllSubmatch(b, maxIssues) {
		refs = append(refs, reference{string(m[1]), "css import"})
	}
	return refs
}

func stripCSSComments(b []byte) []byte {
	result := make([]byte, 0, len(b))
	quote := byte(0)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if quote != 0 {
			result = append(result, c)
			if c == '\\' && i+1 < len(b) {
				i++
				result = append(result, b[i])
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			result = append(result, c)
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '*' {
			end := bytes.Index(b[i+2:], []byte("*/"))
			if end < 0 {
				break
			}
			i += end + 3
			result = append(result, ' ')
			continue
		}
		result = append(result, c)
	}
	return result
}

func (a *archive) checkReferences(name string, refs []reference, contents map[string]contentInfo) {
	for _, ref := range refs {
		if strings.TrimSpace(ref.href) == "" {
			continue
		}
		target, fragment, external, err := resolveReference(name, ref.href)
		if err != nil {
			a.issue("LINK_PATH", "error", "Invalid resource path", ref.href+": "+err.Error(), name, false)
			continue
		}
		if external {
			if !(ref.attribute == "a@href" || ref.attribute == "area@href") && !(strings.HasPrefix(ref.href, "data:") || strings.HasPrefix(ref.href, "mailto:")) {
				a.issue("RESOURCE_REMOTE", "warning", "Resource requires a network connection", ref.href, name, false)
			}
			continue
		}
		if _, ok := a.files[target]; !ok {
			a.issue("LINK_MISSING", "error", "Linked resource is missing", ref.href+" resolves to "+target, name, false)
			continue
		}
		if fragment != "" {
			if content, ok := contents[target]; ok && !content.ids[fragment] {
				a.issue("LINK_FRAGMENT", "warning", "Link target identifier is missing", ref.href, name, false)
			}
		}
	}
}
