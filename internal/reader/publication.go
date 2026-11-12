package reader

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

type packageItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	Media      string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}
type packageValue struct {
	ID   string `xml:"id,attr"`
	Text string `xml:",chardata"`
}
type packageMeta struct {
	Property string `xml:"property,attr"`
	Name     string `xml:"name,attr"`
	Content  string `xml:"content,attr"`
	Text     string `xml:",chardata"`
}
type packageInfo struct {
	XMLName    xml.Name
	Identifier string `xml:"unique-identifier,attr"`
	Metadata   struct {
		Titles      []packageValue `xml:"title"`
		Languages   []packageValue `xml:"language"`
		Authors     []packageValue `xml:"creator"`
		Identifiers []packageValue `xml:"identifier"`
		Meta        []packageMeta  `xml:"meta"`
	} `xml:"metadata"`
	Manifest []packageItem `xml:"manifest>item"`
	Spine    struct {
		TOC       string `xml:"toc,attr"`
		Direction string `xml:"page-progression-direction,attr"`
		Items     []struct {
			ID         string `xml:"idref,attr"`
			Linear     string `xml:"linear,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func decodeXML(ctx context.Context, src []byte, out any) error {
	if bytes.HasPrefix(src, []byte{0xff, 0xfe}) || bytes.HasPrefix(src, []byte{0xfe, 0xff}) {
		text := sourceText(src)
		if end := strings.Index(text, "?>"); end >= 0 && strings.HasPrefix(text, "<?xml") {
			text = text[end+2:]
		}
		src = []byte(text)
	}
	decoder := func() *xml.Decoder {
		d := xml.NewDecoder(ctxReader{ctx, bytes.NewReader(src)})
		d.CharsetReader = charset.NewReaderLabel
		d.Entity = xml.HTMLEntity
		return d
	}
	d := decoder()
	depth := 0
	for count := 0; ; count++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		if count > 200000 {
			return errors.New("publication XML exceeds the reader token limit")
		}
		token, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
			if depth > 128 {
				return errors.New("publication XML exceeds the reader depth limit")
			}
		case xml.EndElement:
			depth--
		}
	}
	return decoder().Decode(out)
}

func (s *session) publication(ctx context.Context) error {
	s.language = "und"
	names := make([]string, 0, len(s.files))
	for name, f := range s.files {
		if !f.FileInfo().IsDir() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		s.addResource(name, "")
	}
	container, e := s.read(ctx, "META-INF/container.xml", 1<<20)
	var c struct {
		XMLName xml.Name
		Roots   []struct {
			Path  string `xml:"full-path,attr"`
			Media string `xml:"media-type,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	opf := ""
	if e == nil && decodeXML(ctx, container, &c) == nil && c.XMLName == (xml.Name{Space: "urn:oasis:names:tc:opendocument:xmlns:container", Local: "container"}) {
		for _, root := range c.Roots {
			if root.Media == "application/oebps-package+xml" && safeName(root.Path) && s.files[root.Path] != nil {
				opf = root.Path
				break
			}
		}
	}
	var pkg packageInfo
	packageOK := false
	if opf != "" {
		data, err := s.read(ctx, opf, maxText)
		if err == nil && decodeXML(ctx, data, &pkg) == nil && pkg.XMLName == (xml.Name{Space: "http://www.idpf.org/2007/opf", Local: "package"}) {
			packageOK = true
		}
	}
	byID := map[string]*Resource{}
	linear := map[string]bool{}
	properties := map[string]string{}
	navPath, ncxPath, identifier := "", "", ""
	if packageOK {
		for _, v := range pkg.Metadata.Languages {
			if value := strings.TrimSpace(v.Text); value != "" {
				s.language = value
				break
			}
		}
		for _, v := range pkg.Metadata.Titles {
			if strings.TrimSpace(v.Text) != "" {
				s.book.Title = strings.TrimSpace(v.Text)
				break
			}
		}
		authors := []string{}
		for _, v := range pkg.Metadata.Authors {
			if strings.TrimSpace(v.Text) != "" {
				authors = append(authors, strings.TrimSpace(v.Text))
			}
		}
		s.book.Author = strings.Join(authors, ", ")
		for _, v := range pkg.Metadata.Identifiers {
			if v.ID == pkg.Identifier {
				identifier = strings.TrimSpace(v.Text)
			}
		}
		for _, v := range pkg.Metadata.Meta {
			if v.Property == "rendition:layout" && strings.TrimSpace(v.Text) == "pre-paginated" {
				s.book.Layout = "pre-paginated"
			}
		}
		if pkg.Spine.Direction == "rtl" {
			s.book.Direction = "rtl"
		}
		for _, item := range pkg.Manifest {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			target, fragment, valid := resolve(opf, item.Href)
			if !valid || fragment != "" || item.ID == "" {
				s.book.Warnings = appendUnique(s.book.Warnings, "Some external or invalid publication resources are unavailable in preview.")
				continue
			}
			if _, exists := byID[item.ID]; exists {
				s.book.Warnings = appendUnique(s.book.Warnings, "Duplicate publication identifiers may make navigation incomplete.")
				continue
			}
			media := strings.TrimSpace(strings.ToLower(strings.Split(item.Media, ";")[0]))
			if media == "" {
				media = guessMedia(target)
				s.book.Warnings = appendUnique(s.book.Warnings, "Some missing resource types were inferred for this preview.")
			}
			resource := s.addResource(target, media)
			resource.MediaType = media
			resource.Renderable = isDocument(media)
			resource.URL = Prefix + s.book.SessionID + "/r/" + resource.ID + extension(media)
			byID[item.ID] = resource
			if token(item.Properties, "nav") {
				navPath = target
			}
			if item.ID == pkg.Spine.TOC && media == "application/x-dtbncx+xml" {
				ncxPath = target
			}
		}
		seen := map[string]bool{}
		for _, item := range pkg.Spine.Items {
			r := byID[item.ID]
			if r == nil {
				continue
			}
			if seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			r.SpineIndex = len(s.book.Spine)
			s.book.Spine = append(s.book.Spine, *r)
			linear[r.ID] = item.Linear != "no"
			properties[r.ID] = safeSpineProperties(item.Properties)
		}
	} else {
		s.book.Warnings = append(s.book.Warnings, "The package metadata or reading order could not be read. Available files can still be inspected.")
	}
	if e = s.encryption(ctx, identifier); e != nil {
		s.book.Warnings = appendUnique(s.book.Warnings, e.Error())
	}
	for _, r := range s.resources {
		if s.blocked[r.Path] {
			r.Missing = true
		}
	}
	if navPath != "" {
		if data, err := s.read(ctx, navPath, maxText); err == nil {
			s.book.TOC = s.xhtmlTOC(ctx, navPath, data)
		}
	}
	if len(s.book.TOC) == 0 && ncxPath != "" {
		if data, err := s.read(ctx, ncxPath, maxText); err == nil {
			s.book.TOC = s.ncxTOC(ctx, ncxPath, data)
		}
	}
	if len(s.book.TOC) == 0 {
		for _, r := range s.book.Spine {
			s.book.TOC = append(s.book.TOC, TOCEntry{Label: r.Title, Href: r.URL, ResourceID: r.ID, Missing: r.Missing || s.blocked[r.Path], Children: []TOCEntry{}})
		}
	}
	for i, r := range s.book.Spine {
		if current := s.byPath[r.Path]; current != nil {
			s.book.Spine[i] = *current
		}
	}
	if len(s.book.Spine) == 0 {
		s.book.Warnings = appendUnique(s.book.Warnings, "No verified reading order is available. Choose an available document from Files.")
	}
	s.resourceList()
	s.makePackage(linear, properties)
	return nil
}
func token(value, want string) bool {
	for _, v := range strings.Fields(value) {
		if v == want {
			return true
		}
	}
	return false
}
func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}
func safeSpineProperties(value string) string {
	out := []string{}
	for _, p := range strings.Fields(value) {
		if strings.HasPrefix(p, "rendition:layout-") || strings.HasPrefix(p, "rendition:spread-") || strings.HasPrefix(p, "rendition:orientation-") || p == "page-spread-left" || p == "page-spread-right" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}
func (s *session) makePackage(linear map[string]bool, properties map[string]string) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid" prefix="rendition: http://www.idpf.org/vocab/rendition/#"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="uid">urn:spineguard:` + s.book.BookKey + `</dc:identifier><dc:title>` + html.EscapeString(s.book.Title) + `</dc:title><dc:creator>` + html.EscapeString(s.book.Author) + `</dc:creator><dc:language>` + html.EscapeString(s.language) + `</dc:language><meta property="rendition:layout">` + s.book.Layout + `</meta></metadata><manifest>`)
	for _, r := range s.book.Resources {
		if r.Missing || s.blocked[r.Path] || !allowedMedia(r.MediaType) {
			continue
		}
		media := r.MediaType
		if r.Renderable {
			media = "application/xhtml+xml"
		}
		b.WriteString(`<item id="` + r.ID + `" href="` + html.EscapeString(r.URL) + `" media-type="` + html.EscapeString(media) + `"/>`)
	}
	b.WriteString(`<item id="sg-nav" href="` + Prefix + s.book.SessionID + `/navigation.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest><spine page-progression-direction="` + s.book.Direction + `">`)
	added := map[string]bool{}
	for _, r := range s.book.Spine {
		if !r.Renderable || r.Missing || s.blocked[r.Path] {
			continue
		}
		added[r.ID] = true
		extra := ""
		if !linear[r.ID] {
			extra = ` linear="no"`
		}
		if properties[r.ID] != "" {
			extra += ` properties="` + html.EscapeString(properties[r.ID]) + `"`
		}
		b.WriteString(`<itemref idref="` + r.ID + `"` + extra + `/>`)
	}
	for _, r := range s.book.Resources {
		if r.Renderable && !r.Missing && !s.blocked[r.Path] && !added[r.ID] {
			b.WriteString(`<itemref idref="` + r.ID + `" linear="no"/>`)
		}
	}
	b.WriteString(`</spine></package>`)
	s.opf = []byte(b.String())
	var nav strings.Builder
	nav.WriteString(`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title><meta http-equiv="Content-Security-Policy" content="` + html.EscapeString(readerPolicy()) + `"/></head><body><nav epub:type="toc"><ol>`)
	var add func([]TOCEntry)
	add = func(entries []TOCEntry) {
		for _, item := range entries {
			nav.WriteString(`<li>`)
			if !item.Missing && item.Href != "" {
				nav.WriteString(`<a href="` + html.EscapeString(item.Href) + `">` + html.EscapeString(item.Label) + `</a>`)
			} else {
				nav.WriteString(`<span>` + html.EscapeString(item.Label) + `</span>`)
			}
			if len(item.Children) > 0 {
				nav.WriteString(`<ol>`)
				add(item.Children)
				nav.WriteString(`</ol>`)
			}
			nav.WriteString(`</li>`)
		}
	}
	add(s.book.TOC)
	nav.WriteString(`</ol></nav></body></html>`)
	s.nav = []byte(nav.String())
}

func (s *session) encryption(ctx context.Context, identifier string) error {
	if s.files["META-INF/encryption.xml"] == nil {
		return nil
	}
	data, e := s.read(ctx, "META-INF/encryption.xml", maxText)
	if e != nil {
		return errors.New("Some protected resources cannot be previewed.")
	}
	var enc struct {
		Items []struct {
			Method struct {
				Algorithm string `xml:"Algorithm,attr"`
			} `xml:"EncryptionMethod"`
			Reference struct {
				URI string `xml:"URI,attr"`
			} `xml:"CipherData>CipherReference"`
		} `xml:"EncryptedData"`
	}
	if e = decodeXML(ctx, data, &enc); e != nil {
		return errors.New("Protection metadata could not be read; publisher fonts may be unavailable.")
	}
	unsupported := false
	for _, item := range enc.Items {
		name, _, valid := resolve("encryption.xml", item.Reference.URI)
		if !valid {
			unsupported = true
			continue
		}
		r := s.byPath[name]
		if r == nil {
			continue
		}
		algorithm := item.Method.Algorithm
		if !(strings.HasPrefix(r.MediaType, "font/") || strings.Contains(r.MediaType, "font")) {
			s.blocked[name] = true
			unsupported = true
			continue
		}
		switch algorithm {
		case "http://www.idpf.org/2008/embedding":
			value := strings.Map(func(r rune) rune {
				if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
					return -1
				}
				return r
			}, identifier)
			if value != "" {
				sum := sha1.Sum([]byte(value))
				s.fontKeys[name] = sum[:]
				s.fontBytes[name] = 1040
			} else {
				s.blocked[name] = true
				unsupported = true
			}
		case "http://ns.adobe.com/pdf/enc#RC":
			value := strings.TrimPrefix(strings.TrimSpace(identifier), "urn:uuid:")
			value = strings.ReplaceAll(value, "-", "")
			key, err := hex.DecodeString(value)
			if err == nil && len(key) == 16 {
				s.fontKeys[name] = key
				s.fontBytes[name] = 1024
			} else {
				s.blocked[name] = true
				unsupported = true
			}
		default:
			s.blocked[name] = true
			unsupported = true
		}
	}
	if unsupported {
		return errors.New("Some encrypted resources or publisher fonts are unavailable in preview.")
	}
	return nil
}

type ncxPoint struct {
	Label   string `xml:"navLabel>text"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Children []ncxPoint `xml:"navPoint"`
}

func (s *session) tocEntry(base, label, href string) TOCEntry {
	entry := TOCEntry{Label: strings.TrimSpace(label), Missing: true, Children: []TOCEntry{}}
	if entry.Label == "" {
		entry.Label = "Untitled section"
	}
	name, fragment, valid := resolve(base, href)
	if !valid {
		return entry
	}
	r := s.byPath[name]
	if r == nil {
		return entry
	}
	entry.ResourceID = r.ID
	entry.Fragment = fragment
	entry.Href = r.URL
	if fragment != "" {
		entry.Href += "#" + urlFragment(fragment)
	}
	entry.Missing = r.Missing || !r.Renderable || s.blocked[name]
	if r.Title == path.Base(r.Path) && !entry.Missing {
		r.Title = entry.Label
	}
	return entry
}
func urlFragment(value string) string { return url.PathEscape(value) }
func (s *session) ncxTOC(ctx context.Context, base string, data []byte) []TOCEntry {
	var doc struct {
		Points []ncxPoint `xml:"navMap>navPoint"`
	}
	if decodeXML(ctx, data, &doc) != nil {
		return nil
	}
	var convert func([]ncxPoint, int) []TOCEntry
	count := 0
	convert = func(points []ncxPoint, depth int) []TOCEntry {
		out := []TOCEntry{}
		if depth > 64 {
			return out
		}
		for _, p := range points {
			count++
			if count > 5000 {
				break
			}
			entry := s.tocEntry(base, p.Label, p.Content.Src)
			entry.Children = convert(p.Children, depth+1)
			out = append(out, entry)
		}
		return out
	}
	return convert(doc.Points, 0)
}
func (s *session) xhtmlTOC(ctx context.Context, base string, data []byte) []TOCEntry {
	doc, e := parseReaderHTML(ctx, data)
	if e != nil {
		return nil
	}
	var nav *xhtml.Node
	stack := []*xhtml.Node{doc}
	visited := 0
	for len(stack) > 0 && nav == nil {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visited++
		if visited > 200000 || ctx.Err() != nil {
			return nil
		}
		if n.Type == xhtml.ElementNode && n.Data == "nav" {
			for _, a := range n.Attr {
				if (a.Key == "type" || a.Key == "epub:type") && token(a.Val, "toc") {
					nav = n
					break
				}
			}
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	if nav == nil {
		return nil
	}
	count := 0
	text := func(n *xhtml.Node) string {
		var b strings.Builder
		nodes := []*xhtml.Node{n}
		for len(nodes) > 0 && b.Len() < 2048 {
			current := nodes[len(nodes)-1]
			nodes = nodes[:len(nodes)-1]
			if current.Type == xhtml.TextNode {
				b.WriteString(current.Data)
			}
			for c := current.LastChild; c != nil; c = c.PrevSibling {
				nodes = append(nodes, c)
			}
		}
		return b.String()
	}
	var list func(*xhtml.Node, int) []TOCEntry
	list = func(n *xhtml.Node, depth int) []TOCEntry {
		out := []TOCEntry{}
		if depth > 64 || ctx.Err() != nil {
			return out
		}
		for li := n.FirstChild; li != nil; li = li.NextSibling {
			if li.Type != xhtml.ElementNode || li.Data != "li" {
				continue
			}
			count++
			if count > 5000 {
				break
			}
			var anchor, sub *xhtml.Node
			for c := li.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == xhtml.ElementNode && (c.Data == "a" || c.Data == "span") && anchor == nil {
					anchor = c
				}
				if c.Type == xhtml.ElementNode && (c.Data == "ol" || c.Data == "ul") {
					sub = c
				}
			}
			if anchor == nil {
				continue
			}
			href := ""
			for _, a := range anchor.Attr {
				if a.Key == "href" {
					href = a.Val
				}
			}
			entry := s.tocEntry(base, text(anchor), href)
			if sub != nil {
				entry.Children = list(sub, depth+1)
			}
			out = append(out, entry)
		}
		return out
	}
	for c := nav.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.ElementNode && (c.Data == "ol" || c.Data == "ul") {
			return list(c, 0)
		}
	}
	return nil
}
