package epub

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
)

type anchorDestination struct {
	file  string
	count int
}
type repairDocument struct {
	source   []byte
	elements []xmlElementSpan
	anchors  map[string]int
}
type linkRepairIndex struct {
	documents map[string]repairDocument
	anchors   map[string][]anchorDestination
	files     map[string]bool
	folded    map[string][]string
	complete  bool
	resources repairResourceCatalog
	ncxCover  *ncxCoverRepair
}

func buildLinkRepairIndex(ctx context.Context, text map[string][]byte, files map[string]bool) linkRepairIndex {
	index := linkRepairIndex{documents: map[string]repairDocument{}, anchors: map[string][]anchorDestination{}, files: files, folded: map[string][]string{}, complete: true}
	index.resources = buildRepairResourceCatalog(ctx, text, files)
	declared, known := declaredRepairContent(text)
	if !known {
		index.complete = false
	}
	for name := range files {
		index.folded[strings.ToLower(name)] = append(index.folded[strings.ToLower(name)], name)
	}
	names := sortedTextNames(text)
	for _, name := range names {
		if ctx.Err() != nil {
			index.complete = false
			break
		}
		ext := strings.ToLower(path.Ext(name))
		isContent := name != "mimetype" && ext != ".opf" && ext != ".css" && (ext != ".xml" || !strings.HasPrefix(name, "META-INF/"))
		elements, err := parseEditableXML(ctx, text[name])
		if err != nil {
			if isContent {
				index.complete = false
			}
			continue
		}
		if len(elements) == 0 {
			continue
		}
		root := elements[0].Name
		if root.Local != "html" && root.Local != "svg" && root.Local != "ncx" {
			continue
		}
		if (root.Local == "html" && root.Space != xhtmlNS) || (root.Local == "svg" && root.Space != "http://www.w3.org/2000/svg") || (root.Local == "ncx" && root.Space != "http://www.daisy.org/z3986/2005/ncx/") {
			if declared[name] {
				index.complete = false
			}
			continue
		}
		anchors := map[string]int{}
		for _, el := range elements {
			onElement := map[string]bool{}
			for _, attr := range el.Attributes {
				if attr.Name == "id" || attr.Name == "xml:id" || (el.Name.Local == "a" && attr.Name == "name") {
					if attr.Value != "" && !onElement[attr.Value] {
						anchors[attr.Value]++
						onElement[attr.Value] = true
					}
				}
			}
		}
		index.documents[name] = repairDocument{source: text[name], elements: elements, anchors: anchors}
		for anchor, count := range anchors {
			if declared[name] {
				index.anchors[anchor] = append(index.anchors[anchor], anchorDestination{name, count})
			}
		}
	}
	index.ncxCover = proveNCXCoverRepair(ctx, text, index)
	return index
}

func declaredRepairContent(text map[string][]byte) (map[string]bool, bool) {
	declared := map[string]bool{}
	var c struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if decoder(text["META-INF/container.xml"]).Decode(&c) != nil || len(c.Rootfiles) == 0 {
		return declared, false
	}
	complete := true
	for _, root := range c.Rootfiles {
		var pkg packageDoc
		if decoder(text[root.FullPath]).Decode(&pkg) != nil {
			complete = false
			continue
		}
		for _, item := range pkg.Manifest {
			if item.MediaType != "application/xhtml+xml" && item.MediaType != "image/svg+xml" && item.MediaType != "application/x-dtbncx+xml" {
				continue
			}
			target, _, external, err := resolveReference(root.FullPath, item.Href)
			if err != nil || external {
				continue
			}
			declared[target] = true
			if _, ok := text[target]; !ok {
				complete = false
			}
		}
	}
	return declared, complete
}

func sortedTextNames(text map[string][]byte) []string {
	names := make([]string, 0, len(text))
	for name := range text {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (index linkRepairIndex) destination(name, href string, navigation bool, kind string, coverRole bool) (replacement, code, detail, reason string) {
	target, fragment, external, err := resolveReference(name, href)
	if err != nil || external {
		return "", "", "", ""
	}
	exists := index.files[target]
	if exists {
		if fragment == "" {
			return "", "", "", ""
		}
		doc, known := index.documents[target]
		if !known {
			return "", "", "", ""
		}
		count := doc.anchors[fragment]
		if count == 1 {
			return "", "", "", ""
		}
		if count > 1 {
			return "", "", "", ""
		} // Duplicate identifiers are handled separately.
		code, detail = "LINK_FRAGMENT", href
	} else {
		code, detail = "LINK_MISSING", href+" resolves to "+target
	}
	if matches := index.folded[strings.ToLower(target)]; !exists && len(matches) == 1 && strings.EqualFold(matches[0], target) && resourceMediaCompatible(kind, index.resources.media[matches[0]]) {
		candidate := matches[0]
		if fragment == "" {
			return relativeResourceURL(name, candidate, href), code, detail, "Correct the resource filename's case to the single matching archive entry."
		}
		if doc, ok := index.documents[candidate]; ok && doc.anchors[fragment] == 1 {
			return relativeResourceURL(name, candidate, href), code, detail, "Correct filename case; the destination contains one matching anchor."
		}
	}
	if !exists {
		if candidate, why := index.resources.replacement(target, kind, coverRole); candidate != "" {
			if fragment == "" {
				return relativeResourceURL(name, candidate, href), code, detail, why
			}
			if doc, ok := index.documents[candidate]; ok && doc.anchors[fragment] == 1 {
				return relativeResourceURL(name, candidate, href), code, detail, why + " The destination contains the requested anchor."
			}
		}
	}
	if fragment == "" {
		return "", code, detail, "No uniquely proven destination is available. A missing resource cannot be recreated automatically."
	}
	if !navigation {
		return "", code, detail, "This embedded-resource reference cannot be safely redirected by a text anchor."
	}
	if !index.complete {
		return "", code, detail, "Some content could not be fully indexed as editable XML; a unique destination cannot be proven."
	}
	destinations := index.anchors[fragment]
	if len(destinations) == 1 && destinations[0].count == 1 {
		candidate := destinations[0].file
		return relativeResourceURL(name, candidate, href), code, detail, "Redirect to the single source element with this exact anchor in " + candidate + "."
	}
	if len(destinations) > 0 {
		return "", code, detail, "The anchor occurs in multiple destinations or elements; automatic repair would have to guess."
	}
	return "", code, detail, "The requested anchor was not found in the indexed book. Restore the missing content or correct this link manually."
}

func relativeResourceURL(from, target, original string) string {
	base := strings.Split(path.Dir(from), "/")
	destination := strings.Split(target, "/")
	if len(base) == 1 && base[0] == "." {
		base = nil
	}
	common := 0
	for common < len(base) && common < len(destination) && base[common] == destination[common] {
		common++
	}
	parts := make([]string, 0, len(base)+len(destination))
	for i := common; i < len(base); i++ {
		parts = append(parts, "..")
	}
	parts = append(parts, destination[common:]...)
	u, _ := url.Parse(strings.TrimSpace(original))
	u.Path = strings.Join(parts, "/")
	u.RawPath = ""
	u.Scheme = ""
	u.Host = ""
	u.Opaque = ""
	return u.String()
}

func planLinkDocuments(ctx context.Context, text map[string][]byte, files map[string]bool, choices RepairOptions) (map[string][]byte, []RepairAction, []Issue) {
	modified := map[string][]byte{}
	actions := []RepairAction{}
	notes := []Issue{}
	index := buildLinkRepairIndex(ctx, text, files)
	for _, name := range sortedTextNames(text) {
		if ctx.Err() != nil {
			break
		}
		doc, ok := index.documents[name]
		if !ok {
			continue
		}
		var edits []xmlEdit
		var fileNotes []Issue
		baseUnknown := hasRepairBase(doc.elements)
		for elementIndex, el := range doc.elements {
			for _, attr := range el.Attributes {
				if attr.Local != "href" && attr.Local != "src" && attr.Local != "poster" && !(attr.Local == "data" && el.Name.Local == "object") {
					continue
				}
				navigation := (attr.Local == "href" && (el.Name.Local == "a" || el.Name.Local == "area")) || (el.Name.Local == "content" && attr.Local == "src")
				kind := referenceResourceKind(el, attr)
				replacement, code, detail, reason := index.destination(name, attr.Value, navigation, kind, isExplicitCoverImage(doc.elements, elementIndex))
				if cover := index.ncxCover; replacement == "" && code == "LINK_MISSING" && cover != nil && cover.document == name && cover.element == elementIndex && attr.Name == "src" && attr.Value == cover.original {
					replacement = relativeResourceURL(name, cover.page, attr.Value)
					reason = "Redirect the cover entry to the explicitly declared cover page, corroborated by its declared cover image: " + cover.page + "."
				}
				if code == "" {
					continue
				}
				note := Issue{Code: code, File: name, Detail: detail, Repairable: replacement != "", RepairKind: "links", RepairReason: reason}
				if kind == "" || !referenceNamespaceSupported(doc.elements, elementIndex, attr) {
					replacement = ""
					note.Repairable = false
					note.RepairReason = "This reference's namespace or resource role cannot be proven compatible with a replacement."
				}
				if baseUnknown {
					replacement = ""
					note.Repairable = false
					note.RepairReason = "This document declares a base URI. Its references need base-aware review before paths can be changed."
				}
				if code == "LINK_MISSING" && el.Name.Local == "script" && cleanupExecutableNamespace(el.Name.Space) && replacement == "" && !baseUnknown {
					note.Repairable = true
					note.OptionalRepair = true
					note.RepairKind = "reader_cleanup"
					note.RepairReason = "The script file is absent. Optional Remove scripts cleanup removes this referencing script element and its missing-resource finding; it does not recreate the script."
				}
				fileNotes = append(fileNotes, note)
				if choices.FixLinks && replacement != "" && replacement != attr.Value {
					edits = append(edits, xmlEdit{attr.Start, attr.End, escapeXMLAttribute(replacement, attr.Quote)})
				}
			}
		}
		if len(edits) > 0 {
			candidate, err := applyXMLEdits(doc.source, edits)
			if err == nil {
				modified[name] = candidate
				actions = append(actions, RepairAction{Kind: "links", File: name, Description: fmt.Sprintf("Correct %d internal link(s) using uniquely proven destinations.", len(edits)), Count: len(edits)})
			} else {
				for i := range fileNotes {
					fileNotes[i].Repairable = false
					fileNotes[i].RepairReason = "The proposed link edits did not pass XML verification; this document is preserved."
				}
			}
		}
		notes = append(notes, fileNotes...)
	}
	return modified, actions, notes
}
