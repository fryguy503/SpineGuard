package epub

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// Scene-divider replacement is an explicit visual substitution, never recovery
// of missing artwork. Classification needs agreeing structural and naming
// evidence; a missing image filename alone is insufficient.
func sceneDividerName(value string) bool {
	value = strings.ToLower(value)
	value = strings.NewReplacer("-", "", "_", "").Replace(value)
	switch value {
	case "star", "stars", "asterisk", "asterisks", "asterism", "ornament", "ornaments", "divider", "separator", "scenebreak", "scenedivider", "sceneseparator":
		return true
	}
	return false
}

func sceneDividerFilename(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp":
		return sceneDividerName(strings.TrimSuffix(path.Base(name), path.Ext(name)))
	}
	return false
}

func sceneUnambiguousAttributes(elements []xmlElementSpan) bool {
	for _, el := range elements {
		seen := map[string]bool{}
		for _, attr := range el.Attributes {
			if seen[attr.Name] {
				return false
			}
			seen[attr.Name] = true
		}
	}
	return true
}

func sceneWrapper(el xmlElementSpan, paragraph bool) bool {
	if el.Name.Space != xhtmlNS || (paragraph && el.Name.Local != "p") || (!paragraph && el.Name.Local != "span") {
		return false
	}
	semantic := false
	for _, attr := range el.Attributes {
		switch attr.Name {
		case "class":
			for _, token := range strings.Fields(attr.Value) {
				semantic = semantic || sceneDividerName(token)
			}
		case "id", "xml:id", "style", "lang", "xml:lang", "dir", "xmlns":
			// These are preserved on the wrapper. An image's own identifiers,
			// accessibility semantics, and interactive attributes are refused.
		default:
			return false
		}
	}
	return !paragraph || semantic
}

func sceneEmptyElement(src []byte, el xmlElementSpan) bool {
	if el.SelfClosing {
		return true
	}
	closing := bytes.LastIndexByte(src[el.OpenEnd:el.End], '<')
	return closing >= 0 && len(bytes.TrimSpace(src[el.OpenEnd:el.OpenEnd+closing])) == 0
}

func sceneOnlyChild(src []byte, parent, child xmlElementSpan) bool {
	if parent.SelfClosing || child.Start < parent.OpenEnd || child.End > parent.End {
		return false
	}
	closing := bytes.LastIndexByte(src[parent.OpenEnd:parent.End], '<')
	return closing >= 0 && child.End <= parent.OpenEnd+closing &&
		len(bytes.TrimSpace(src[parent.OpenEnd:child.Start])) == 0 &&
		len(bytes.TrimSpace(src[child.End:parent.OpenEnd+closing])) == 0
}

func sceneDividerImage(doc repairDocument, image int, href, target string) bool {
	el := doc.elements[image]
	if el.Name.Space != xhtmlNS || el.Name.Local != "img" || !sceneDividerFilename(target) || !sceneEmptyElement(doc.source, el) {
		return false
	}
	parsed, err := url.Parse(href)
	if err != nil || strings.TrimSpace(href) != href || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || parsed.Path == "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(href, "#") || strings.ContainsAny(href, "\\\x00") {
		return false
	}
	for _, attr := range el.Attributes {
		switch attr.Name {
		case "src", "class", "width", "height", "xmlns":
		case "alt":
			alt := strings.TrimSpace(attr.Value)
			if alt != "" && alt != path.Base(parsed.Path) {
				return false
			}
		case "role":
			if attr.Value != "presentation" && attr.Value != "none" {
				return false
			}
		default:
			return false
		}
	}
	child := image
	paragraph := -1
	for parent := el.Parent; parent >= 0; parent = doc.elements[parent].Parent {
		wrapper := doc.elements[parent]
		if wrapper.Name.Local == "p" {
			if !sceneWrapper(wrapper, true) || !sceneOnlyChild(doc.source, wrapper, doc.elements[child]) {
				return false
			}
			paragraph = parent
			break
		}
		if !sceneWrapper(wrapper, false) || !sceneOnlyChild(doc.source, wrapper, doc.elements[child]) {
			return false
		}
		child = parent
	}
	if paragraph < 0 {
		return false
	}
	for parent := doc.elements[paragraph].Parent; parent >= 0; parent = doc.elements[parent].Parent {
		ancestor := doc.elements[parent]
		if ancestor.Name.Space != xhtmlNS {
			return false
		}
		for _, attr := range ancestor.Attributes {
			if eventHandlerName(attr.Name) {
				return false
			}
		}
		switch ancestor.Name.Local {
		case "body":
			return ancestor.Parent == 0
		case "div", "section", "article", "main":
		default:
			return false
		}
	}
	return false
}

type sceneDividerGroup struct {
	file, detail, target string
	total                int
	edits                []xmlEdit
}

func planSceneDividerDocuments(ctx context.Context, text map[string][]byte, files map[string]bool, choices RepairOptions) (map[string][]byte, []RepairAction, []Issue) {
	modified := map[string][]byte{}
	actions := []RepairAction{}
	notes := []Issue{}
	index := buildLinkRepairIndex(ctx, text, files)
	declared, complete := declaredRepairContent(text)
	if !complete || !index.complete || ctx.Err() != nil {
		return modified, actions, notes
	}
	basenames := map[string]bool{}
	for filename := range files {
		basenames[strings.ToLower(path.Base(filename))] = true
	}
	groups := map[string]*sceneDividerGroup{}
	order := []string{}
	for _, filename := range sortedTextNames(text) {
		if ctx.Err() != nil {
			return modified, actions, notes
		}
		doc, ok := index.documents[filename]
		if !ok || !declared[filename] || len(doc.elements) == 0 || doc.elements[0].Name.Space != xhtmlNS || doc.elements[0].Name.Local != "html" {
			continue
		}
		unambiguous := !hasRepairBase(doc.elements) && sceneUnambiguousAttributes(doc.elements)
		// Use the scanner's source collector for occurrence totals, including
		// CSS URLs and SVG references that share an issue's exact identity.
		collector := newContentCollector(filename)
		inspector := &archive{ctx: ctx}
		if inspector.inspectXMLContent(doc.source, collector) != nil {
			continue
		}
		referenceCounts := map[string]int{}
		for _, ref := range collector.info.refs {
			target, fragment, external, err := resolveReference(filename, ref.href)
			if err == nil && !external && fragment == "" && !files[target] {
				referenceCounts[filename+"\x00"+ref.href+" resolves to "+target]++
			}
		}
		for i, el := range doc.elements {
			if ctx.Err() != nil {
				return modified, actions, notes
			}
			for _, attr := range el.Attributes {
				if attr.Local != "src" && attr.Local != "href" && attr.Local != "poster" && !(el.Name.Local == "object" && attr.Local == "data") {
					continue
				}
				kind := referenceResourceKind(el, attr)
				replacement, code, detail, _ := index.destination(filename, attr.Value, false, kind, false)
				if code != "LINK_MISSING" {
					continue
				}
				target, fragment, external, err := resolveReference(filename, attr.Value)
				if err != nil || external || fragment != "" || files[target] {
					continue
				}
				key := filename + "\x00" + detail
				if referenceCounts[key] == 0 {
					continue
				}
				group := groups[key]
				if group == nil {
					group = &sceneDividerGroup{file: filename, detail: detail, target: target, total: referenceCounts[key]}
					groups[key] = group
					order = append(order, key)
				}
				if replacement != "" || !unambiguous || attr.Name != "src" || kind != "image" || basenames[strings.ToLower(path.Base(target))] || !sceneDividerImage(doc, i, attr.Value, target) {
					continue
				}
				group.edits = append(group.edits, xmlEdit{Start: el.Start, End: el.End, Replacement: []byte("* * *")})
			}
		}
	}
	// A repeated, isolated use corroborates the divider role. A mixed group is
	// refused because scan annotation keys intentionally combine equal refs.
	repeated := map[string]int{}
	for _, key := range order {
		group := groups[key]
		if group.total == len(group.edits) {
			repeated[group.target] += len(group.edits)
		}
	}
	fileEdits := map[string][]xmlEdit{}
	fileNotes := map[string][]Issue{}
	for _, key := range order {
		group := groups[key]
		if group.total != len(group.edits) || repeated[group.target] < 2 {
			continue
		}
		note := Issue{Code: "LINK_MISSING", File: group.file, Detail: group.detail, Repairable: true, RepairKind: "scene_dividers", OptionalRepair: true, RepairReason: "Optional scene-divider repair replaces repeated missing decorative images with * * * text markers. Surrounding book text and existing resources are preserved; the original artwork is not recovered."}
		fileNotes[group.file] = append(fileNotes[group.file], note)
		fileEdits[group.file] = append(fileEdits[group.file], group.edits...)
	}
	for _, filename := range sortedTextNames(text) {
		if ctx.Err() != nil {
			return modified, actions, notes
		}
		these := fileNotes[filename]
		if len(these) == 0 {
			continue
		}
		if choices.ReplaceMissingSceneDividers {
			candidate, err := applyXMLEdits(text[filename], fileEdits[filename])
			if err != nil {
				for i := range these {
					these[i].Repairable = false
					these[i].RepairReason = "The proposed divider replacement did not pass XML verification; this document is preserved."
				}
			} else {
				modified[filename] = candidate
				actions = append(actions, RepairAction{Kind: "scene_dividers", File: filename, Description: fmt.Sprintf("Replace %d missing scene-divider image(s) with * * * text markers, preserving surrounding text and divider spacing.", len(fileEdits[filename])), Count: len(fileEdits[filename]), RequiresOptIn: true})
			}
		}
		notes = append(notes, these...)
	}
	return modified, actions, notes
}
