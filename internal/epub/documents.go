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
			XMLName  xml.Name
			ID       string `xml:"id,attr"`
			Refines  string `xml:"refines,attr"`
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
	input := utf8Input(b)
	d := xml.NewDecoder(bytes.NewReader(input))
	d.CharsetReader = charset.NewReaderLabel
	d.Entity = recognizedDoctypeEntities(input)
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

// encoding/xml does not expand declarations from internal or external DTDs.
// A complete named reference in a document declaring a DTD is therefore an
// incomplete native assessment; EPUBCheck must decide its validity and meaning.
func dtdEntityUnsupported(b []byte, parseErr error) bool {
	x, ok := parseErr.(*xml.SyntaxError)
	if !ok || !strings.HasPrefix(x.Msg, "invalid character entity &") || !strings.HasSuffix(x.Msg, ";") || strings.HasPrefix(x.Msg, "invalid character entity &#") {
		return false
	}
	d := decoder(b)
	for {
		t, err := d.Token()
		if err != nil {
			return false
		}
		if directive, ok := t.(xml.Directive); ok {
			text := strings.TrimSpace(string(directive))
			if strings.HasPrefix(text, "DOCTYPE ") || strings.HasPrefix(text, "DOCTYPE\n") || strings.HasPrefix(text, "DOCTYPE\t") || strings.HasPrefix(text, "DOCTYPE\r") {
				return true
			}
		}
	}
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
	idCounts      map[string]int
	refs          []reference
	words, pages  int
	nav, pageList bool
	rootNamespace string
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
	c := newContentCollector(name)
	err := a.inspectXMLContent(b, c)
	if err != nil {
		if a.ctx.Err() != nil {
			return contentInfo{ids: map[string]bool{}, idCounts: map[string]int{}}
		}
		if _, limited := err.(contentLimitError); limited {
			a.issue("CONTENT_COMPLEXITY", "error", "Content exceeds structural scan limits", err.Error()+" Word counts and references may be incomplete.", name, false)
			return contentInfo{ids: map[string]bool{}, idCounts: map[string]int{}}
		}
		// A source tokenizer supplies an approximate inspection of malformed or
		// DTD-dependent markup. It never creates/clones a DOM element, so repair
		// warnings refer only to identifiers actually present in the source.
		c = newContentCollector(name)
		if err = a.inspectHTMLSource(b, c); err != nil {
			if a.ctx.Err() == nil {
				a.issue("CONTENT_COMPLEXITY", "error", "Content exceeds structural scan limits", err.Error()+" Word counts and references may be incomplete.", name, false)
			}
			return contentInfo{ids: map[string]bool{}, idCounts: map[string]int{}}
		}
	}
	for _, issue := range c.issues {
		a.issueReference(issue.Code, issue.Severity, issue.Title, issue.Detail, name, issue.Reference, issue.Anchor)
	}
	c.info.words = wordCount(c.text.String())
	return c.info
}

type contentLimitError string

func (e contentLimitError) Error() string { return string(e) }

type contentFrame struct {
	tag                    string
	body, hidden, pageList bool
	style                  strings.Builder
}
type contentCollector struct {
	info      contentInfo
	text      strings.Builder
	stack     []contentFrame
	issues    []Issue
	name      string
	actualIDs map[string]int
}

func newContentCollector(name string) *contentCollector {
	return &contentCollector{name: name, actualIDs: map[string]int{}, info: contentInfo{ids: map[string]bool{}, idCounts: map[string]int{}}}
}

const contentBlocks = "address article aside blockquote br dd div dl dt figcaption figure footer h1 h2 h3 h4 h5 h6 header hr li main ol p pre section table td th tr ul"

func (c *contentCollector) note(code, title, detail string) {
	if len(c.issues) <= maxIssues {
		c.issues = append(c.issues, Issue{Code: code, Severity: "warning", Title: title, Detail: detail, File: c.name})
	}
}
func (c *contentCollector) start(tag, space string, attrs map[string]string) error {
	if len(c.stack) >= 1024 {
		return contentLimitError("Nesting exceeds the 1,024-level content limit.")
	}
	frame := contentFrame{tag: tag}
	if len(c.stack) > 0 {
		p := &c.stack[len(c.stack)-1]
		frame.body = p.body
		frame.hidden = p.hidden
		frame.pageList = p.pageList
	} else {
		c.info.rootNamespace = space
	}
	frame.body = frame.body || tag == "body"
	_, hiddenAttribute := attrs["hidden"]
	style := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, attrs["style"])
	contentNamespace := space == xhtmlNS || space == "" || space == "http://www.w3.org/2000/svg"
	frame.hidden = frame.hidden || contentNamespace && tokenHas("script style nav noscript template", tag) || hiddenAttribute || strings.EqualFold(attrs["aria-hidden"], "true") || strings.Contains(style, "display:none")
	if tokenHas(contentBlocks, tag) {
		c.text.WriteByte(' ')
	}
	// id and a legacy name on the same element identify one destination.
	// An identifier repeated on a different source element is ambiguous.
	anchors := map[string]bool{}
	actualIDs := map[string]bool{}
	for _, id := range []string{attrs["id"], attrs["xml:id"]} {
		if id != "" {
			anchors[id] = true
			actualIDs[id] = true
		}
	}
	if tag == "a" && attrs["name"] != "" {
		anchors[attrs["name"]] = true
	}
	for id := range actualIDs {
		if c.actualIDs[id] > 0 {
			before := len(c.issues)
			c.note("CONTENT_DUPLICATE_ID", "Duplicate content identifier", "Links to this identifier may be ambiguous: "+id)
			if len(c.issues) == before+1 && c.issues[before].Code == "CONTENT_DUPLICATE_ID" {
				c.issues[before].Anchor = id
			}
		}
		c.actualIDs[id]++
	}
	for id := range anchors {
		c.info.ids[id] = true
		c.info.idCounts[id]++
	}
	scripted := tag == "script" && contentNamespace
	for key, value := range attrs {
		if key == "href" || key == "src" || key == "poster" || (key == "data" && tag == "object") {
			c.info.refs = append(c.info.refs, reference{value, tag + "@" + key})
		}
		if key == "style" {
			c.info.refs = append(c.info.refs, cssReferences([]byte(value))...)
		}
		if tokenHas("fill stroke filter clip-path mask marker marker-start marker-mid marker-end", key) {
			c.info.refs = append(c.info.refs, cssReferences([]byte(value))...)
		}
		if contentNamespace && eventHandlerName(key) {
			scripted = true
		}
		if contentNamespace && (key == "href" || key == "src" || key == "action" || key == "formaction") && javascriptURL(value) {
			scripted = true
		}
	}
	etype := attrs["epub:type"]
	if etype == "" {
		etype = attrs["type"]
	}
	if tag == "nav" && tokenHas(etype, "toc") {
		c.info.nav = true
	}
	if tag == "nav" && tokenHas(etype, "page-list") {
		c.info.pageList = true
		frame.pageList = true
	}
	if tokenHas(etype, "pagebreak") || attrs["role"] == "doc-pagebreak" {
		c.info.pages++
	}
	if frame.pageList && tag == "a" {
		c.info.pages++
	}
	if scripted {
		c.note("CONTENT_SCRIPT", "Scripted content", "Scripts and scripted actions can increase reader workload. Optional reader cleanup can remove them from a separate copy; interactive features may stop working.")
	}
	if tag == "iframe" {
		c.note("CONTENT_IFRAME", "Embedded frame", "Embedded frames may not be supported by all e-readers.")
	}
	c.stack = append(c.stack, frame)
	return nil
}
func (c *contentCollector) chars(value []byte) {
	if len(c.stack) == 0 {
		return
	}
	f := &c.stack[len(c.stack)-1]
	if f.body && !f.hidden {
		c.text.Write(value)
	}
	if f.tag == "style" {
		f.style.Write(value)
	}
}
func (c *contentCollector) end() {
	if len(c.stack) == 0 {
		return
	}
	f := &c.stack[len(c.stack)-1]
	if f.tag == "style" {
		c.info.refs = append(c.info.refs, cssReferences([]byte(f.style.String()))...)
	}
	if tokenHas(contentBlocks, f.tag) {
		c.text.WriteByte(' ')
	}
	c.stack = c.stack[:len(c.stack)-1]
}

func xmlContentAttributes(attrs []xml.Attr) map[string]string {
	values := make(map[string]string, len(attrs))
	for _, at := range attrs {
		key := at.Name.Local
		if at.Name.Space == "http://www.idpf.org/2007/ops" {
			key = "epub:" + key
		} else if at.Name.Space == "http://www.w3.org/XML/1998/namespace" {
			key = "xml:" + key
		} else if at.Name.Space != "" && at.Name.Space != "http://www.w3.org/1999/xlink" {
			key = "ns:" + key
		}
		values[key] = at.Value
	}
	return values
}
func (a *archive) inspectXMLContent(b []byte, c *contentCollector) error {
	d := decoder(b)
	for tokens := 0; ; tokens++ {
		if err := a.ctx.Err(); err != nil {
			return err
		}
		if tokens >= 200000 {
			return contentLimitError("More than 200,000 source XML tokens.")
		}
		t, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t := t.(type) {
		case xml.StartElement:
			if len(t.Attr) > 256 {
				return contentLimitError("An element has more than 256 attributes.")
			}
			if err := c.start(t.Name.Local, t.Name.Space, xmlContentAttributes(t.Attr)); err != nil {
				return err
			}
		case xml.EndElement:
			c.end()
		case xml.CharData:
			c.chars(t)
		}
	}
}
func (a *archive) inspectHTMLSource(b []byte, c *contentCollector) error {
	z := html.NewTokenizer(bytes.NewReader(utf8Input(b)))
	for tokens := 0; ; tokens++ {
		if err := a.ctx.Err(); err != nil {
			return err
		}
		if tokens >= 200000 {
			return contentLimitError("More than 200,000 source HTML tokens.")
		}
		t := z.Next()
		if t == html.ErrorToken {
			return nil
		}
		switch t {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			tag := string(name)
			attrs := map[string]string{}
			count := 0
			for more {
				key, value, next := z.TagAttr()
				more = next
				count++
				if count > 256 {
					return contentLimitError("An element has more than 256 attributes.")
				}
				attrs[string(key)] = string(value)
			}
			if err := c.start(tag, "", attrs); err != nil {
				return err
			}
			if t == html.SelfClosingTagToken || tokenHas("area base br col embed hr img input link meta param source track wbr", tag) {
				c.end()
				z.NextIsNotRawText()
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			for i := len(c.stack) - 1; i >= 0; i-- {
				if c.stack[i].tag == string(name) {
					for len(c.stack) > i {
						c.end()
					}
					break
				}
			}
		case html.TextToken:
			c.chars(z.Text())
		}
	}
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
			a.issueReference("LINK_PATH", "error", "Invalid resource path", ref.href+": "+err.Error(), name, ref.href, "")
			continue
		}
		if external {
			if !(ref.attribute == "a@href" || ref.attribute == "area@href") && !(strings.HasPrefix(ref.href, "data:") || strings.HasPrefix(ref.href, "mailto:")) {
				a.issueReference("RESOURCE_REMOTE", "warning", "Resource requires a network connection", ref.href, name, ref.href, "")
			}
			continue
		}
		if _, ok := a.files[target]; !ok {
			a.issueReference("LINK_MISSING", "error", "Linked resource is missing", ref.href+" resolves to "+target, name, ref.href, fragment)
			continue
		}
		if fragment != "" {
			if content, ok := contents[target]; ok && !content.ids[fragment] {
				a.issueReference("LINK_FRAGMENT", "warning", "Link target identifier is missing", ref.href, name, ref.href, fragment)
			}
		}
	}
}

// Keep machine-readable locations separate from the finding's explanatory text.
func (a *archive) issueReference(code, severity, title, detail, file, ref, anchor string) {
	before := len(a.report.Issues)
	a.issue(code, severity, title, detail, file, false)
	if len(a.report.Issues) == before+1 && a.report.Issues[before].Code == code {
		a.report.Issues[before].Reference = ref
		a.report.Issues[before].Anchor = anchor
	}
}
