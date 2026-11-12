package epub

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/url"
	"strings"
)

// A stale cover entry can survive a cover-page replacement. The proof is based
// on publication roles and the actual cover wrapper, never on filename order.
type ncxCoverRepair struct {
	document, original, page string
	element                  int
}

// XML syntax validation alone does not reject repeated attributes. Every
// identifying attribute used by this proof must be unique and unqualified.
func coverProofAttribute(el xmlElementSpan, name string) (string, bool) {
	value, count := "", 0
	for _, at := range el.Attributes {
		if at.Local == name {
			if at.Name != name {
				return "", false
			}
			value, count = at.Value, count+1
		}
	}
	return value, count == 1
}

func coverProofURL(base, href string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil || u.Path == "" || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return "", false
	}
	name, fragment, external, err := resolveReference(base, href)
	return name, err == nil && !external && fragment == "" && safeName(name)
}

func coverProofLabel(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func proveNCXCoverRepair(ctx context.Context, text map[string][]byte, index linkRepairIndex) *ncxCoverRepair {
	if !index.resources.complete || index.resources.cover == "" {
		return nil
	}
	media := index.resources.media[index.resources.cover]
	if media != "image/jpeg" && media != "image/png" && media != "image/gif" {
		return nil
	}
	container, err := parseEditableXML(ctx, text["META-INF/container.xml"])
	if err != nil || len(container) == 0 || container[0].Name != (xml.Name{Space: containerNS, Local: "container"}) || hasRepairBase(container) {
		return nil
	}
	rootfiles, opf := 0, ""
	for i, el := range container {
		if ctx.Err() != nil {
			return nil
		}
		if el.Parent == 0 && el.Name.Local == "rootfiles" {
			if el.Name.Space != containerNS {
				return nil
			}
			rootfiles++
			if rootfiles > 1 {
				return nil
			}
			for _, root := range container {
				if root.Parent != i || root.Name != (xml.Name{Space: containerNS, Local: "rootfile"}) {
					continue
				}
				full, ok := coverProofAttribute(root, "full-path")
				kind, kindOK := coverProofAttribute(root, "media-type")
				if opf != "" || !ok || !kindOK || kind != "application/oebps-package+xml" || !safeName(full) {
					return nil
				}
				opf = full
			}
		}
	}
	if rootfiles != 1 || opf == "" || !index.files[opf] {
		return nil
	}
	packageElements, err := parseEditableXML(ctx, text[opf])
	if err != nil || len(packageElements) == 0 || packageElements[0].Name != (xml.Name{Space: opfNS, Local: "package"}) || hasRepairBase(packageElements) {
		return nil
	}
	manifest, spine, guide := -1, -1, -1
	for i, el := range packageElements {
		if ctx.Err() != nil {
			return nil
		}
		attributes := map[string]bool{}
		for _, at := range el.Attributes {
			if attributes[at.Name] {
				return nil
			}
			attributes[at.Name] = true
			role := el.Name == (xml.Name{Space: opfNS, Local: "meta"}) && (at.Local == "name" || at.Local == "content") || el.Name == (xml.Name{Space: opfNS, Local: "item"}) && at.Local == "properties"
			if role {
				if _, valid := coverProofAttribute(el, at.Local); !valid {
					return nil
				}
			}
		}
		if el.Parent != 0 {
			continue
		}
		var destination *int
		switch el.Name.Local {
		case "manifest":
			destination = &manifest
		case "spine":
			destination = &spine
		case "guide":
			destination = &guide
		}
		if destination != nil {
			if el.Name.Space != opfNS || *destination != -1 {
				return nil
			}
			*destination = i
		}
	}
	if manifest < 0 || spine < 0 || guide < 0 {
		return nil
	}
	byID, declared := map[string]string{}, map[string]bool{}
	for _, el := range packageElements {
		if ctx.Err() != nil {
			return nil
		}
		if el.Parent != manifest {
			continue
		}
		if el.Name != (xml.Name{Space: opfNS, Local: "item"}) {
			return nil
		}
		id, idOK := coverProofAttribute(el, "id")
		href, hrefOK := coverProofAttribute(el, "href")
		kind, kindOK := coverProofAttribute(el, "media-type")
		name, local := coverProofURL(opf, href)
		if !idOK || id == "" || byID[id] != "" || !hrefOK || !local || !kindOK || kind == "" || declared[name] {
			return nil
		}
		byID[id], declared[name] = name, true
	}
	tocID, ok := coverProofAttribute(packageElements[spine], "toc")
	if !ok || index.resources.media[byID[tocID]] != "application/x-dtbncx+xml" {
		return nil
	}
	ncxName := byID[tocID]
	ncx, exists := index.documents[ncxName]
	if !exists || len(ncx.elements) == 0 || ncx.elements[0].Name != (xml.Name{Space: ncxNS, Local: "ncx"}) || hasRepairBase(ncx.elements) {
		return nil
	}
	page, title, references := "", "", 0
	for _, el := range packageElements {
		if ctx.Err() != nil {
			return nil
		}
		if el.Parent != guide {
			continue
		}
		if el.Name != (xml.Name{Space: opfNS, Local: "reference"}) {
			return nil
		}
		kind, kindOK := coverProofAttribute(el, "type")
		if !kindOK {
			return nil
		}
		if kind != "cover" {
			continue
		}
		references++
		href, hrefOK := coverProofAttribute(el, "href")
		label, labelOK := coverProofAttribute(el, "title")
		var local bool
		page, local = coverProofURL(opf, href)
		title = coverProofLabel(label)
		if !hrefOK || !local || !labelOK || title == "" {
			return nil
		}
	}
	if references != 1 || !declared[page] || !index.files[page] || index.resources.media[page] != "application/xhtml+xml" {
		return nil
	}
	inSpine := false
	for _, el := range packageElements {
		if ctx.Err() != nil {
			return nil
		}
		if el.Parent == spine {
			if el.Name != (xml.Name{Space: opfNS, Local: "itemref"}) {
				return nil
			}
			id, valid := coverProofAttribute(el, "idref")
			if !valid || byID[id] == "" {
				return nil
			}
			inSpine = inSpine || byID[id] == page
		}
	}
	cover, exists := index.documents[page]
	if !inSpine || !exists || !coverOnlyWrapper(ctx, cover, page, index.resources.cover) {
		return nil
	}
	navMap, matches, point := -1, 0, -1
	for i, el := range ncx.elements {
		if ctx.Err() != nil {
			return nil
		}
		if el.Parent == 0 && el.Name.Local == "navMap" {
			if el.Name.Space != ncxNS || navMap != -1 {
				return nil
			}
			navMap = i
		}
	}
	if navMap == -1 {
		return nil
	}
	children := make([][]int, len(ncx.elements))
	insideMap := make([]bool, len(ncx.elements))
	for i, el := range ncx.elements {
		if ctx.Err() != nil {
			return nil
		}
		if el.Parent >= 0 {
			children[el.Parent] = append(children[el.Parent], i)
		}
	}
	for i, el := range ncx.elements {
		if ctx.Err() != nil {
			return nil
		}
		if el.Name != (xml.Name{Space: ncxNS, Local: "navPoint"}) {
			continue
		}
		insideMap[i] = el.Parent == navMap || el.Parent >= 0 && insideMap[el.Parent]
		if !insideMap[i] {
			continue
		}
		count, valid := ncxCoverLabels(ctx, ncx, i, children, title)
		if !valid {
			return nil
		}
		if count > 0 {
			matches += count
			point = i
		}
	}
	if matches != 1 || point < 0 {
		return nil
	}
	_, candidate := ncxCoverPoint(ncx, point, children)
	if candidate < 0 {
		return nil
	}
	original, ok := coverProofAttribute(ncx.elements[candidate], "src")
	missing, local := coverProofURL(ncxName, original)
	if !ok || !local || index.files[missing] || declared[missing] {
		return nil
	}
	// Do not introduce a second destination for an already referenced cover page;
	// its existing play order and hierarchy must remain the publisher's choice.
	for i, el := range ncx.elements {
		if ctx.Err() != nil {
			return nil
		}
		if i == candidate || el.Name != (xml.Name{Space: ncxNS, Local: "content"}) {
			continue
		}
		href, valid := coverProofAttribute(el, "src")
		target, _, external, err := resolveReference(ncxName, href)
		if valid && err == nil && !external && target == page {
			return nil
		}
	}
	return &ncxCoverRepair{document: ncxName, element: candidate, original: original, page: page}
}

// Count labels before considering leaf eligibility. A second Cover label on a
// parent or otherwise non-leaf point still makes the intended role ambiguous.
func ncxCoverLabels(ctx context.Context, doc repairDocument, point int, children [][]int, title string) (int, bool) {
	matches := 0
	for _, child := range children[point] {
		if ctx.Err() != nil {
			return 0, false
		}
		el := doc.elements[child]
		if el.Name.Local != "navLabel" {
			continue
		}
		if el.Name.Space != ncxNS || len(children[child]) != 1 {
			return 0, false
		}
		label := doc.elements[children[child][0]]
		if label.Name != (xml.Name{Space: ncxNS, Local: "text"}) {
			return 0, false
		}
		value, valid := coverProofText(doc.source, label, nil)
		if !valid {
			return 0, false
		}
		if coverProofLabel(value) == title {
			matches++
		}
	}
	return matches, true
}

// A matching label must be a plain, unambiguous leaf navigation entry.
func ncxCoverPoint(doc repairDocument, point int, children [][]int) (string, int) {
	label, content := "", -1
	labels := 0
	for _, i := range children[point] {
		el := doc.elements[i]
		if el.Name.Space != ncxNS {
			return "", -1
		}
		switch el.Name.Local {
		case "content":
			if content != -1 {
				return "", -1
			}
			content = i
		case "navLabel":
			labels++
			texts := 0
			for _, child := range children[i] {
				text := doc.elements[child]
				texts++
				if text.Name != (xml.Name{Space: ncxNS, Local: "text"}) {
					return "", -1
				}
				var ok bool
				label, ok = coverProofText(doc.source, text, nil)
				if !ok {
					return "", -1
				}
			}
			if texts != 1 {
				return "", -1
			}
		default:
			return "", -1
		}
	}
	if labels != 1 || content == -1 {
		return "", -1
	}
	if value, valid := coverProofText(doc.source, doc.elements[content], nil); !valid || value != "" {
		return "", -1
	}
	return label, content
}

func coverOnlyWrapper(ctx context.Context, doc repairDocument, page, coverImage string) bool {
	if len(doc.elements) == 0 || doc.elements[0].Name != (xml.Name{Space: xhtmlNS, Local: "html"}) || hasRepairBase(doc.elements) {
		return false
	}
	body, head, images := -1, -1, 0
	entities := recognizedDoctypeEntities(doc.source)
	for i, el := range doc.elements {
		if err := ctx.Err(); err != nil {
			return false
		}
		attrs := map[string]bool{}
		for _, at := range el.Attributes {
			if attrs[at.Name] || strings.HasPrefix(strings.ToLower(at.Local), "on") || at.Local == "srcset" || at.Local == "poster" || at.Local == "data" {
				return false
			}
			attrs[at.Name] = true
			if at.Local == "style" && !passiveCoverStyle(at.Value) {
				return false
			}
		}
		if el.Parent == 0 {
			if el.Name == (xml.Name{Space: xhtmlNS, Local: "head"}) && head == -1 {
				head = i
			} else if el.Name == (xml.Name{Space: xhtmlNS, Local: "body"}) && body == -1 {
				body = i
			} else {
				return false
			}
			continue
		}
		if el.Parent < 0 {
			continue
		}
		if el.Parent == head {
			if el.Name.Space != xhtmlNS {
				return false
			}
			switch el.Name.Local {
			case "title":
				if _, valid := coverProofText(doc.source, el, entities); !valid {
					return false
				}
			case "meta":
				for _, at := range el.Attributes {
					if at.Local == "http-equiv" && !strings.EqualFold(at.Value, "Content-Type") {
						return false
					}
				}
			case "style":
				style, valid := coverProofText(doc.source, el, entities)
				if !valid || !passiveCoverStyle(style) {
					return false
				}
			default:
				return false
			}
			continue
		}
		if body < 0 || i < body || el.Start >= doc.elements[body].End {
			return false
		}
		switch el.Name {
		case xml.Name{Space: xhtmlNS, Local: "div"}, xml.Name{Space: svgNS, Local: "svg"}:
			parent := doc.elements[el.Parent].Name
			if parent != (xml.Name{Space: xhtmlNS, Local: "body"}) && parent != (xml.Name{Space: xhtmlNS, Local: "div"}) {
				return false
			}
		case xml.Name{Space: xhtmlNS, Local: "img"}, xml.Name{Space: svgNS, Local: "image"}:
			parent := doc.elements[el.Parent].Name
			if el.Name.Local == "image" && parent != (xml.Name{Space: svgNS, Local: "svg"}) || el.Name.Local == "img" && parent != (xml.Name{Space: xhtmlNS, Local: "body"}) && parent != (xml.Name{Space: xhtmlNS, Local: "div"}) {
				return false
			}
			images++
			imageHref := ""
			refs := 0
			for _, at := range el.Attributes {
				if at.Local == "src" || at.Local == "href" {
					if !referenceNamespaceSupported(doc.elements, i, at) || (el.Name.Local == "img" && at.Name != "src") || (el.Name.Local == "image" && at.Local != "href") {
						return false
					}
					refs++
					imageHref = at.Value
				}
			}
			target, valid := coverProofURL(page, imageHref)
			if refs != 1 || !valid || target != coverImage {
				return false
			}
		default:
			return false
		}
	}
	if head < 0 || body < 0 || images != 1 {
		return false
	}
	d := xml.NewDecoder(bytes.NewReader(doc.source))
	d.Entity = entities
	depth, bodyDepth := 0, 0
	for {
		if ctx.Err() != nil {
			return false
		}
		token, err := d.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 && token.Name == (xml.Name{Space: xhtmlNS, Local: "body"}) {
				bodyDepth = depth
			}
		case xml.EndElement:
			if depth == bodyDepth {
				bodyDepth = 0
			}
			depth--
		case xml.CharData:
			if bodyDepth != 0 && strings.TrimSpace(string(token)) != "" {
				return false
			}
		case xml.ProcInst:
			if token.Target != "xml" {
				return false
			}
		}
	}
}

// Decode only the selected leaf. The entity table is computed once per XHTML
// wrapper; NCX labels use only XML's built-in entities. Re-reading a large XML
// prolog for every navigation label would otherwise make indexing quadratic.
func coverProofText(src []byte, el xmlElementSpan, entities map[string]string) (string, bool) {
	d := xml.NewDecoder(bytes.NewReader(src[el.Start:el.End]))
	d.Entity = entities
	depth := 0
	var text strings.Builder
	for {
		token, err := d.Token()
		if err == io.EOF {
			return strings.TrimSpace(text.String()), true
		}
		if err != nil {
			return "", false
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			if depth > 1 {
				return "", false
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 1 {
				text.Write(token)
			}
		}
	}
}

func passiveCoverStyle(style string) bool {
	style = strings.ToLower(style)
	if strings.ContainsAny(style, "\\\"'") {
		return false
	}
	for _, active := range []string{"url", "import", "expression", "javascript", "behavior", "binding", "image-set", "content"} {
		if strings.Contains(style, active) {
			return false
		}
	}
	return true
}
