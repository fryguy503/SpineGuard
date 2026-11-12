package reader

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	maxHTMLBytes       = 16 << 20
	maxHTMLNodes       = 250000
	maxHTMLDepth       = 256
	maxRasterDataBytes = 2 << 20
)

// Resolver maps an original EPUB reference to a known, opaque resource URL in
// the current reader session. Returning an empty string blocks the reference.
// It must never return arbitrary filesystem or external network URLs.
type Resolver func(ref, role string) string

const defaultReaderCSP = "default-src 'none'; script-src 'none'; connect-src 'none'; object-src 'none'; frame-src 'none'; child-src 'none'; form-action 'none'; base-uri 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self'; media-src 'self'"

var xmlEncodingPattern = regexp.MustCompile(`(?i)^\s*<\?xml\s[^>]*encoding\s*=\s*["']([^"']+)["']`)

// SanitizeHTML produces well-formed, passive XHTML even when a damaged EPUB
// chapter contains ordinary HTML instead of valid XML. The optional trusted CSP
// may further restrict resource sources to a reader-session URL prefix.
func SanitizeHTML(ctx context.Context, src []byte, resolve Resolver, policies ...string) ([]byte, error) {
	doc, err := parseReaderHTML(ctx, src)
	if err != nil {
		return nil, err
	}
	root := findHTMLNode(doc, "html", "")
	if root == nil {
		return nil, errors.New("reader document has no HTML root")
	}
	s := sanitizer{ctx: ctx, resolve: resolve}
	nodes, err := s.node(root, 0)
	if err != nil {
		return nil, err
	}
	if len(nodes) != 1 {
		return nil, errors.New("reader document could not be sanitized")
	}
	root = nodes[0]
	head := findHTMLNode(root, "head", "")
	if head == nil {
		head = &html.Node{Type: html.ElementNode, Data: "head"}
		root.InsertBefore(head, root.FirstChild)
	}
	policy := defaultReaderCSP
	if len(policies) > 0 && strings.TrimSpace(policies[0]) != "" {
		policy = policies[0]
	}
	// This is inserted before all publisher content, including stylesheets.
	csp := &html.Node{Type: html.ElementNode, Data: "meta", Attr: []html.Attribute{{Key: "http-equiv", Val: "Content-Security-Policy"}, {Key: "content", Val: policy}}}
	head.InsertBefore(csp, head.FirstChild)
	head.InsertBefore(&html.Node{Type: html.ElementNode, Data: "meta", Attr: []html.Attribute{{Key: "charset", Val: "utf-8"}}}, csp.NextSibling)
	return serializeReaderXML(ctx, root)
}

// SanitizeSVG returns a passive, standalone SVG suitable for an image resource.
// Scripts, animation, foreign HTML, and non-session references are removed.
func SanitizeSVG(ctx context.Context, src []byte, resolve Resolver, policies ...string) ([]byte, error) {
	doc, err := parseReaderHTML(ctx, src)
	if err != nil {
		return nil, err
	}
	root := findHTMLNode(doc, "svg", "svg")
	if root == nil {
		return nil, errors.New("reader image has no SVG root")
	}
	s := sanitizer{ctx: ctx, resolve: resolve}
	nodes, err := s.node(root, 0)
	if err != nil {
		return nil, err
	}
	if len(nodes) != 1 {
		return nil, errors.New("reader SVG could not be sanitized")
	}
	return serializeReaderXML(ctx, nodes[0])
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func parseReaderHTML(ctx context.Context, src []byte) (*html.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(src) > maxHTMLBytes {
		return nil, errors.New("reader document exceeds size limit")
	}
	label := "utf-8"
	prefix := src
	if len(prefix) > 512 {
		prefix = prefix[:512]
	}
	if m := xmlEncodingPattern.FindSubmatch(prefix); len(m) == 2 {
		label = string(m[1])
	}
	// EPUB XML defaults to UTF-8. Unlike HTML's legacy fallback, this preserves
	// ordinary non-ASCII book text when a chapter has no charset meta element.
	r, err := charset.NewReader(bytes.NewReader(src), "application/xhtml+xml; charset="+label)
	if err != nil {
		return nil, fmt.Errorf("reader document encoding: %w", err)
	}
	decoded, err := io.ReadAll(io.LimitReader(contextReader{ctx, r}, maxHTMLBytes+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > maxHTMLBytes {
		return nil, errors.New("decoded reader document exceeds size limit")
	}
	// Bound token count before building the tree, and check cancellation while
	// scanning. html.Parse itself is bounded by the same input and node limits.
	z := html.NewTokenizer(bytes.NewReader(decoded))
	z.SetMaxBuf(maxHTMLBytes)
	for count := 0; ; count++ {
		if count > maxHTMLNodes {
			return nil, errors.New("reader document exceeds node limit")
		}
		if count%128 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		typ := z.Next()
		if typ == html.ErrorToken {
			if z.Err() != io.EOF {
				return nil, z.Err()
			}
			break
		}
		if typ == html.StartTagToken || typ == html.SelfClosingTagToken {
			_, more := z.TagName()
			for attrs := 0; more; attrs++ {
				if attrs >= 256 {
					return nil, errors.New("reader element exceeds attribute limit")
				}
				_, _, more = z.TagAttr()
			}
		}
	}
	return html.Parse(contextReader{ctx, bytes.NewReader(decoded)})
}

type sanitizer struct {
	ctx     context.Context
	resolve Resolver
	count   int
}

var htmlTags = wordSet("html head body title style link meta a abbr acronym address article aside b bdi bdo big blockquote br caption center cite code col colgroup dd del details dfn dialog div dl dt em figcaption figure font footer h1 h2 h3 h4 h5 h6 header hgroup hr i img ins kbd li main mark menu nav ol p picture pre q rb rp rt rtc ruby s samp section small source span strike strong sub summary sup table tbody td tfoot th thead time tr tt u ul var video audio wbr")
var dropTags = wordSet("script noscript iframe frame frameset object embed applet base basefont template form input button select option optgroup textarea keygen output portal fencedframe foreignobject animate animatemotion animatetransform set discard handler listener")
var svgTags = wordSet("svg g defs symbol use image title desc path rect circle ellipse line polyline polygon text tspan textpath lineargradient radialgradient stop clippath mask pattern marker switch a style")
var mathTags = wordSet("math mi mn mo ms mtext mrow mfrac msqrt mroot mstyle merror mpadded mphantom mfenced menclose msub msup msubsup munder mover munderover mmultiscripts mprescripts none mtable mlabeledtr mtr mtd maligngroup malignmark semantics annotation")
var globalAttrs = wordSet("id class title lang dir role hidden translate tabindex")
var htmlAttrs = wordSet("alt width height colspan rowspan headers scope span start reversed type align valign border cellpadding cellspacing color face size datetime cite open controls loop muted preload kind srclang label media sizes loading decoding")
var svgAttrs = wordSet("x y x1 x2 y1 y2 dx dy width height cx cy r rx ry d points viewbox preserveaspectratio transform gradienttransform gradientunits spreadmethod offset stop-color stop-opacity fill fill-opacity fill-rule stroke stroke-width stroke-opacity stroke-linecap stroke-linejoin stroke-miterlimit stroke-dasharray stroke-dashoffset opacity color font-family font-size font-style font-weight text-anchor dominant-baseline textlength lengthadjust rotate writing-mode direction unicode-bidi clip-path clip-rule mask marker-start marker-mid marker-end patternunits patterncontentunits patterntransform refx refy markerwidth markerheight markerunits orient overflow version display visibility vector-effect focusable")
var mathAttrs = wordSet("display mathvariant mathsize mathcolor mathbackground scriptlevel displaystyle scriptsizemultiplier scriptminsize dir accent accentunder align bevelled close columnalign columnlines columnspacing columnspan depth equalcolumns equalrows fence fontfamily fontstyle fontweight frame framespacing height linethickness lspace maxsize minsize movablelimits notation numalign open rowalign rowlines rowspacing rowspan rspace separator separators stretchy subscriptshift superscriptshift symmetric voffset width")
var svgCSSAttrs = wordSet("fill stroke clip-path mask marker-start marker-mid marker-end")

func wordSet(words string) map[string]bool {
	m := make(map[string]bool)
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

func (s *sanitizer) node(n *html.Node, depth int) ([]*html.Node, error) {
	s.count++
	if s.count > maxHTMLNodes || depth > maxHTMLDepth {
		return nil, errors.New("reader document exceeds complexity limit")
	}
	if s.count%128 == 0 {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
	}
	if n.Type == html.TextNode {
		return []*html.Node{{Type: html.TextNode, Data: cleanXMLText(n.Data)}}, nil
	}
	if n.Type != html.ElementNode {
		return nil, nil
	}
	tag := strings.ToLower(n.Data)
	if dropTags[tag] {
		return nil, nil
	}
	allowed := false
	switch n.Namespace {
	case "":
		allowed = htmlTags[tag]
	case "svg":
		allowed = svgTags[tag]
	case "math":
		allowed = mathTags[tag]
	}
	if !allowed {
		// Unknown markup is unwrapped, while explicitly active elements above
		// lose their entire subtree (including embedded fallback documents).
		var children []*html.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			cs, err := s.node(c, depth+1)
			if err != nil {
				return nil, err
			}
			children = append(children, cs...)
		}
		return children, nil
	}
	if tag == "link" && !strings.EqualFold(strings.TrimSpace(attrValue(n, "rel")), "stylesheet") {
		return nil, nil
	}
	if tag == "meta" && !strings.EqualFold(attrValue(n, "name"), "viewport") {
		return nil, nil
	}
	clean := &html.Node{Type: html.ElementNode, Data: n.Data, Namespace: n.Namespace}
	seen := map[string]bool{}
	var originalRef string
	for _, a := range n.Attr {
		name := strings.ToLower(a.Key)
		if a.Namespace != "" {
			name = strings.ToLower(a.Namespace) + ":" + name
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if strings.HasPrefix(name, "on") || strings.HasPrefix(name, "data-sg-") || strings.HasPrefix(name, "xmlns") {
			continue
		}
		value := cleanXMLText(a.Val)
		keep := globalAttrs[name] || strings.HasPrefix(name, "aria-")
		if name == "xml:lang" {
			clean.Attr = append(clean.Attr, html.Attribute{Key: "xml:lang", Val: value})
			continue
		}
		if name == "epub:type" {
			clean.Attr = append(clean.Attr, html.Attribute{Key: "epub:type", Val: value})
			continue
		}
		if name == "xml:space" && (value == "preserve" || value == "default") {
			clean.Attr = append(clean.Attr, html.Attribute{Key: "xml:space", Val: value})
			continue
		}
		if strings.Contains(name, ":") && name != "xlink:href" {
			// Undeclared or book-controlled namespace prefixes cannot be copied
			// into the generated, well-formed XHTML document.
			continue
		}
		switch name {
		case "style":
			css, err := sanitizeCSS(s.ctx, []byte(value), s.resolve, true)
			if err != nil {
				return nil, err
			}
			if len(css) > 0 {
				clean.Attr = append(clean.Attr, html.Attribute{Key: "style", Val: string(css)})
			}
			continue
		case "href", "xlink:href":
			role := ""
			if tag == "a" {
				role = "document"
			} else if tag == "link" {
				role = "stylesheet"
			} else if n.Namespace == "svg" && tag == "image" {
				role = "image"
			} else if n.Namespace == "svg" && (tag == "use" || tag == "textpath" || strings.HasSuffix(tag, "gradient")) && strings.HasPrefix(strings.TrimSpace(value), "#") {
				role = "asset"
			}
			if role != "" {
				mapped := safeResourceURL(value, role, s.resolve)
				if mapped != "" {
					clean.Attr = append(clean.Attr, html.Attribute{Key: name, Val: mapped})
				}
				if originalRef == "" {
					originalRef = value
				}
			}
			continue
		case "src":
			role := ""
			switch tag {
			case "img":
				role = "image"
			case "audio", "video", "source":
				role = "media"
			}
			if role != "" {
				mapped := safeResourceURL(value, role, s.resolve)
				if mapped != "" {
					clean.Attr = append(clean.Attr, html.Attribute{Key: "src", Val: mapped})
				}
				if originalRef == "" {
					originalRef = value
				}
			}
			continue
		case "poster":
			if tag == "video" {
				if mapped := safeResourceURL(value, "image", s.resolve); mapped != "" {
					clean.Attr = append(clean.Attr, html.Attribute{Key: name, Val: mapped})
				}
			}
			continue
		case "rel":
			keep = tag == "link"
			value = "stylesheet"
		case "name", "content":
			keep = tag == "meta" && strings.EqualFold(attrValue(n, "name"), "viewport")
		default:
			switch n.Namespace {
			case "":
				keep = keep || htmlAttrs[name]
			case "svg":
				keep = keep || svgAttrs[name]
			case "math":
				keep = keep || mathAttrs[name]
			}
		}
		if n.Namespace == "svg" && svgCSSAttrs[name] {
			p := []byte(name + ":" + value + ";")
			css, err := sanitizeCSS(s.ctx, p, s.resolve, true)
			if err != nil {
				return nil, err
			}
			v := string(css)
			if strings.HasPrefix(v, name+":") {
				value = strings.TrimSuffix(strings.TrimPrefix(v, name+":"), ";")
			} else {
				keep = false
			}
		}
		if keep && validXMLName(name) {
			clean.Attr = append(clean.Attr, html.Attribute{Key: a.Key, Val: value})
		}
	}
	if originalRef != "" {
		clean.Attr = append(clean.Attr, html.Attribute{Key: "data-sg-ref", Val: originalRef})
	}
	if tag == "style" {
		var text strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				text.WriteString(c.Data)
			}
		}
		css, err := SanitizeCSS(s.ctx, []byte(text.String()), s.resolve)
		if err != nil {
			return nil, err
		}
		clean.AppendChild(&html.Node{Type: html.TextNode, Data: string(css)})
	} else {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			children, err := s.node(c, depth+1)
			if err != nil {
				return nil, err
			}
			for _, cc := range children {
				clean.AppendChild(cc)
			}
		}
	}
	return []*html.Node{clean}, nil
}

func attrValue(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}
func findHTMLNode(n *html.Node, tag, ns string) *html.Node {
	if n.Type == html.ElementNode && strings.EqualFold(n.Data, tag) && n.Namespace == ns {
		return n
	}
	// Iterative traversal is safe even for a deeply malformed input tree.
	for cur := n.FirstChild; cur != nil; {
		if cur.Type == html.ElementNode && strings.EqualFold(cur.Data, tag) && cur.Namespace == ns {
			return cur
		}
		if cur.FirstChild != nil {
			cur = cur.FirstChild
			continue
		}
		for cur != n && cur.NextSibling == nil {
			cur = cur.Parent
		}
		if cur == n {
			break
		}
		cur = cur.NextSibling
	}
	return nil
}

func safeResourceURL(ref, role string, resolve Resolver) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || len(ref) > maxRasterDataBytes*2 {
		return ""
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return ""
		}
	}
	if strings.HasPrefix(ref, "#") {
		if role == "document" || role == "asset" || role == "image" {
			return ref
		}
		return ""
	}
	if strings.HasPrefix(strings.ToLower(ref), "data:") {
		if role == "image" || role == "asset" {
			return safeRasterData(ref)
		}
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(ref, "//") {
		return ""
	}
	if resolve == nil {
		return ""
	}
	mapped := resolve(ref, role)
	if mapped == "" {
		return ""
	}
	// The resolver is a trust boundary, but a typo must still not introduce an
	// active scheme or a non-reader path. Absolute same-app URLs support srcdoc.
	m, err := url.Parse(mapped)
	if err != nil || m.User != nil || (m.Scheme != "" && m.Scheme != "http" && m.Scheme != "https" && m.Scheme != "wails") {
		return ""
	}
	if !strings.HasPrefix(m.Path, "/__reader/") || strings.ContainsAny(mapped, "\r\n\t\\") {
		return ""
	}
	return mapped
}

func safeRasterData(ref string) string {
	comma := strings.IndexByte(ref, ',')
	if comma < 0 {
		return ""
	}
	header := strings.ToLower(ref[:comma])
	format := ""
	switch header {
	case "data:image/png;base64":
		format = "png"
	case "data:image/jpeg;base64":
		format = "jpeg"
	case "data:image/gif;base64":
		format = "gif"
	default:
		return ""
	}
	encoded := ref[comma+1:]
	if base64.StdEncoding.DecodedLen(len(encoded)) > maxRasterDataBytes {
		return ""
	}
	b, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return ""
	}
	cfg, actual, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || actual != format || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 16000 || cfg.Height > 16000 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
		return ""
	}
	return header + "," + base64.StdEncoding.EncodeToString(b)
}

func cleanXMLText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == 9 || r == 10 || r == 13 || (r >= 0x20 && r <= 0xd7ff) || (r >= 0xe000 && r <= 0xfffd) || (r >= 0x10000 && r <= utf8.MaxRune) {
			return r
		}
		return utf8.RuneError
	}, s)
}
func validXMLName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '.' || r == ':')) {
			return false
		}
	}
	return true
}

func serializeReaderXML(ctx context.Context, root *html.Node) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n")
	var write func(*html.Node, string, int) error
	write = func(n *html.Node, parentNS string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > maxHTMLDepth {
			return errors.New("reader XML exceeds depth limit")
		}
		if n.Type == html.TextNode {
			return xml.EscapeText(&b, []byte(n.Data))
		}
		if n.Type != html.ElementNode || !validXMLName(n.Data) {
			return nil
		}
		b.WriteByte('<')
		b.WriteString(n.Data)
		ns := "http://www.w3.org/1999/xhtml"
		if n.Namespace == "svg" {
			ns = "http://www.w3.org/2000/svg"
		} else if n.Namespace == "math" {
			ns = "http://www.w3.org/1998/Math/MathML"
		}
		if ns != parentNS {
			b.WriteString(" xmlns=\"")
			b.WriteString(ns)
			b.WriteByte('"')
		}
		if parentNS == "" {
			b.WriteString(" xmlns:xlink=\"http://www.w3.org/1999/xlink\" xmlns:epub=\"http://www.idpf.org/2007/ops\"")
		}
		for _, a := range n.Attr {
			if !validXMLName(a.Key) {
				continue
			}
			b.WriteByte(' ')
			b.WriteString(a.Key)
			b.WriteString("=\"")
			if err := xml.EscapeText(&b, []byte(a.Val)); err != nil {
				return err
			}
			b.WriteByte('"')
		}
		if n.FirstChild == nil {
			b.WriteString(" />")
			return nil
		}
		b.WriteByte('>')
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if err := write(c, ns, depth+1); err != nil {
				return err
			}
		}
		b.WriteString("</")
		b.WriteString(n.Data)
		b.WriteByte('>')
		if b.Len() > maxHTMLBytes*3 {
			return errors.New("reader XML output exceeds size limit")
		}
		return nil
	}
	if err := write(root, "", 0); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
