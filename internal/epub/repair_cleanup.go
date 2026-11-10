package epub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
)

// Event attributes are unprefixed HTML/SVG event handlers. Namespace-qualified
// metadata whose local name resembles an event is preserved.
func eventHandlerName(name string) bool {
	name = strings.ToLower(name)
	return !strings.Contains(name, ":") && len(name) > 2 && strings.HasPrefix(name, "on")
}

func javascriptURL(value string) bool {
	// Browsers discard ASCII whitespace/control characters around and within
	// URL schemes; recognize mixed-case and encoded-attribute variants too.
	value = strings.Map(func(r rune) rune {
		if r <= 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	return strings.HasPrefix(strings.ToLower(value), "javascript:")
}

type cleanupDocument struct {
	name     string
	src      []byte
	elements []xmlElementSpan
}

func cleanupContentDocument(elements []xmlElementSpan) bool {
	if len(elements) == 0 {
		return false
	}
	root := elements[0].Name
	return (root.Local == "html" && (root.Space == xhtmlNS || root.Space == "")) || (root.Local == "svg" && (root.Space == "http://www.w3.org/2000/svg" || root.Space == ""))
}

func cleanupExecutableNamespace(space string) bool {
	return space == xhtmlNS || space == "http://www.w3.org/2000/svg" || space == ""
}

// Some split conversions repeat the body's ID on an empty, zero-height first
// child. Keeping the body preserves every existing chapter-start destination.
func redundantChapterStart(doc cleanupDocument, indices []int) bool {
	if len(indices) < 2 {
		return false
	}
	body := doc.elements[indices[0]]
	if body.Name.Local != "body" || (body.Name.Space != xhtmlNS && body.Name.Space != "") {
		return false
	}
	for _, index := range indices[1:] {
		el := doc.elements[index]
		if el.Name.Local != "div" || el.Name.Space != body.Name.Space || el.Parent != indices[0] || len(bytes.TrimSpace(doc.src[body.OpenEnd:el.Start])) != 0 {
			return false
		}
		style := ""
		for _, at := range el.Attributes {
			switch at.Name {
			case "id", "xml:id":
			case "style":
				style = strings.ToLower(strings.Join(strings.Fields(at.Value), ""))
			default:
				return false
			}
		}
		style = strings.TrimSuffix(style, ";")
		if style != "height:0pt" && style != "height:0px" && style != "height:0" {
			return false
		}
		if !el.SelfClosing {
			closeOffset := bytes.LastIndexByte(doc.src[el.OpenEnd:el.End], '<')
			if closeOffset < 0 || len(bytes.TrimSpace(doc.src[el.OpenEnd:el.OpenEnd+closeOffset])) != 0 {
				return false
			}
		}
	}
	return true
}

// planCleanupDocuments changes only source spans in strictly parsed UTF-8 XML.
// It never removes archive resources or rewrites an entire document tree.
// Notes are eligibility assessments even when an optional action is not selected.
func planCleanupDocuments(ctx context.Context, text map[string][]byte, choices RepairOptions) (map[string][]byte, []RepairAction, []Issue) {
	modified := map[string][]byte{}
	actions := []RepairAction{}
	notes := []Issue{}
	keys := make([]string, 0, len(text))
	for name := range text {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	documents := []cleanupDocument{}
	cachedFiles := map[string]bool{}
	for name := range text {
		cachedFiles[name] = true
	}
	declaredMedia := buildRepairResourceCatalog(ctx, text, cachedFiles).media
	// Prove dependencies per identifier. Class-only styling does not depend on
	// ID spelling, while scripts and uninspectable resources remain blockers.
	styleSources := [][]byte{}
	styleSeen := map[string]bool{}
	styleUnknown := ""
	referencesUnknown := false
	addStyle := func(name string) {
		if styleSeen[name] {
			return
		}
		styleSeen[name] = true
		if b, ok := text[name]; ok {
			styleSources = append(styleSources, b)
		} else {
			styleUnknown = "A referenced stylesheet is missing or could not be inspected."
		}
	}
	dependentScripts := false
	fragmentRefs := map[string]bool{}
	idRefs := map[string]bool{}
	for _, name := range keys {
		if ctx.Err() != nil {
			return modified, actions, notes
		}
		src := text[name]
		ext := strings.ToLower(path.Ext(name))
		if (ext == ".css" || declaredMedia[name] == "text/css") && len(bytes.TrimSpace(src)) > 0 {
			addStyle(name)
		}
		if (ext == ".js" || strings.Contains(declaredMedia[name], "javascript") || strings.Contains(declaredMedia[name], "ecmascript")) && len(bytes.TrimSpace(src)) > 0 {
			dependentScripts = true
		}
		if bytes.Contains(src, []byte("application/javascript")) || bytes.Contains(src, []byte("text/javascript")) {
			dependentScripts = true
		}
		elements, err := parseEditableXML(ctx, src)
		if err != nil {
			if name != "mimetype" && ext != ".css" && ext != ".js" && declaredMedia[name] != "text/css" && !strings.Contains(declaredMedia[name], "javascript") && !strings.Contains(declaredMedia[name], "ecmascript") {
				referencesUnknown = true
			}
			if textResource(name) && ext != ".css" && bytes.Contains(bytes.ToLower(src), []byte("<script")) {
				notes = append(notes, Issue{Code: "CONTENT_SCRIPT", File: name, RepairKind: "reader_cleanup", OptionalRepair: true, RepairReason: "This document cannot be edited losslessly as UTF-8 XML. Run EPUBCheck or correct its XML/encoding first."})
			}
			continue
		}
		if hasRepairBase(elements) {
			referencesUnknown = true
		}
		for _, element := range elements {
			if element.Name.Local == "style" && element.End > element.OpenEnd {
				if css, ok := repairStyleText(ctx, src, element); ok {
					styleSources = append(styleSources, css)
				} else {
					styleUnknown = "An embedded stylesheet could not be inspected."
				}
			}
			if element.Name.Local == "script" {
				dependentScripts = true
			}
			// Manifest media types and stylesheet links retain their meaning even
			// when a resource uses no extension or is not cached for inspection.
			if element.Name.Local == "item" {
				href := repairAttribute(element, "href")
				for _, at := range element.Attributes {
					if at.Local == "media-type" {
						media := strings.ToLower(at.Value)
						if media == "text/css" {
							target, fragment, external, err := resolveReference(name, href)
							if err != nil || external || fragment != "" {
								styleUnknown = "A stylesheet declaration requires external or unsupported resource resolution."
							} else {
								addStyle(target)
							}
						}
						if strings.Contains(media, "javascript") || strings.Contains(media, "ecmascript") {
							dependentScripts = true
						}
					}
				}
			}
			if element.Name.Local == "link" {
				for _, at := range element.Attributes {
					if at.Local == "rel" && tokenHas(strings.ToLower(at.Value), "stylesheet") {
						target, fragment, external, err := resolveReference(name, repairAttribute(element, "href"))
						if err != nil || external || fragment != "" {
							styleUnknown = "A stylesheet link requires external or unsupported resource resolution."
						} else {
							addStyle(target)
						}
					}
				}
			}
			for _, at := range element.Attributes {
				if at.Name == "style" && strings.TrimSpace(at.Value) != "" {
					styleSources = append(styleSources, []byte(at.Value))
				}
				if eventHandlerName(at.Name) || javascriptURL(at.Value) {
					dependentScripts = true
				}
				if at.Local == "href" || at.Local == "src" || at.Local == "textref" || at.Local == "refines" {
					if u, err := url.Parse(strings.TrimSpace(at.Value)); err == nil && u.Fragment != "" {
						target, fragment, external, resolveErr := resolveReference(name, at.Value)
						if resolveErr != nil {
							referencesUnknown = true
						} else if !external {
							fragmentRefs[target+"\x00"+fragment] = true
						}
					}
				}
				if strings.Contains(strings.ToLower(at.Value), "url(") {
					styleSources = append(styleSources, []byte(at.Value))
				}
				if tokenHas("for headers list form contextmenu itemref popovertarget commandfor anchor", at.Name) || strings.HasPrefix(at.Name, "aria-") || at.Name == "begin" || at.Name == "end" {
					for _, id := range strings.Fields(at.Value) {
						idRefs[name+"\x00"+id] = true
						idRefs[id] = true
					}
				}
				if at.Name == "begin" || at.Name == "end" {
					for _, timing := range strings.Split(at.Value, ";") {
						if dot := strings.IndexByte(strings.TrimSpace(timing), '.'); dot > 0 {
							idRefs[name+"\x00"+strings.TrimSpace(timing)[:dot]] = true
							idRefs[strings.TrimSpace(timing)[:dot]] = true
						}
					}
				}
			}
		}
		if cleanupContentDocument(elements) {
			documents = append(documents, cleanupDocument{name, src, elements})
		}
	}
	styleProofCache := map[string]string{}
	styleProofBudget := 64 << 20
	styleReason := func(id string) string {
		if reason, ok := styleProofCache[id]; ok {
			return reason
		}
		reason := styleUnknown
		if reason == "" {
			for _, css := range styleSources {
				if ctx.Err() != nil {
					reason = "The identifier dependency check was canceled."
					break
				}
				if len(css) > styleProofBudget {
					reason = "The bounded CSS dependency check reached its work limit."
					break
				}
				styleProofBudget -= len(css)
				if depends, why := cssIDDependency(css, id); depends {
					reason = why
					break
				}
			}
		}
		styleProofCache[id] = reason
		return reason
	}
	for _, doc := range documents {
		if ctx.Err() != nil {
			return modified, actions, notes
		}
		var edits []xmlEdit
		removed := map[int]bool{}
		scriptCount, handlerCount, actionCount := 0, 0, 0
		for i, element := range doc.elements {
			parentRemoved := element.Parent >= 0 && removed[element.Parent]
			if parentRemoved {
				removed[i] = true
				continue
			}
			if element.Name.Local == "script" && (element.Name.Space == xhtmlNS || element.Name.Space == "http://www.w3.org/2000/svg" || element.Name.Space == "") {
				scriptCount++
				removed[i] = true
				if choices.RemoveScripts {
					edits = append(edits, xmlEdit{Start: element.Start, End: element.End})
				}
				continue
			}
			if !cleanupExecutableNamespace(element.Name.Space) {
				continue
			}
			for _, at := range element.Attributes {
				isHandler := eventHandlerName(at.Name)
				isAction := (at.Local == "href" || at.Local == "src" || at.Local == "action" || at.Local == "formaction") && javascriptURL(at.Value)
				if isHandler {
					handlerCount++
				} else if isAction {
					actionCount++
				}
				if choices.RemoveScripts && (isHandler || isAction) {
					edits = append(edits, xmlEdit{Start: at.FullStart, End: at.FullEnd})
				}
			}
		}
		if total := scriptCount + handlerCount + actionCount; total > 0 {
			notes = append(notes, Issue{Code: "CONTENT_SCRIPT", File: doc.name, Repairable: true, RepairKind: "reader_cleanup", OptionalRepair: true, RepairReason: "Optional reader cleanup removes script elements, inline event handlers, and javascript: actions. Interactive features may stop working; book resources are retained."})
			if choices.RemoveScripts {
				actions = append(actions, RepairAction{Kind: "reader_cleanup", File: doc.name, Description: fmt.Sprintf("Remove %d script elements, %d inline event handlers, and %d javascript: actions; retain prose, styles, images, and archive resources.", scriptCount, handlerCount, actionCount), Count: total, RequiresOptIn: true})
			}
		}
		// Analyze actual ID attributes in source order. Legacy a@name is a link
		// fallback, not another XML ID and not itself a duplicate-ID violation.
		type occurrence struct {
			element   int
			attribute xmlAttributeSpan
		}
		byID := map[string][]occurrence{}
		allIDs := map[string]bool{}
		legacyIDs := map[string][]int{}
		for i, element := range doc.elements {
			seenElementIDs := map[string]bool{}
			for _, at := range element.Attributes {
				if (at.Name == "id" || at.Name == "xml:id") && at.Value != "" && !seenElementIDs[at.Value] {
					byID[at.Value] = append(byID[at.Value], occurrence{i, at})
					allIDs[at.Value] = true
					seenElementIDs[at.Value] = true
				}
				if element.Name.Local == "a" && at.Name == "name" {
					allIDs[at.Value] = true
					legacyIDs[at.Value] = append(legacyIDs[at.Value], i)
				}
			}
		}
		dups := make([]string, 0)
		for id, occurrences := range byID {
			if len(occurrences) > 1 {
				dups = append(dups, id)
			}
		}
		sort.Strings(dups)
		if len(dups) > 0 {
			count := 0
			for _, id := range dups {
				reason := styleReason(id)
				if dependentScripts {
					reason = "Scripts may depend on identifier spelling. Duplicate identifiers need manual review."
				} else if referencesUnknown {
					reason = "Some document references or base URIs could not be fully inspected; identifier changes require manual review."
				}
				if idRefs[doc.name+"\x00"+id] || idRefs[id] {
					reason = "An ARIA, form, or timing reference uses this identifier spelling; its dependency needs manual review."
				}
				indices := []int{}
				indexSet := map[int]bool{}
				for _, occurrence := range byID[id] {
					indices = append(indices, occurrence.element)
					indexSet[occurrence.element] = true
				}
				if fragmentRefs[doc.name+"\x00"+id] && !redundantChapterStart(doc, indices) {
					reason = "A link depends on a repeated identifier and its intended target needs manual review."
				}
				for _, index := range legacyIDs[id] {
					if !indexSet[index] {
						reason = "A separate legacy named anchor shares this identifier; its target must be reviewed."
					}
				}
				eligible := reason == ""
				if eligible {
					reason = "CSS and source references were checked for this identifier. Preserve the first destination and rename only later independent or redundant chapter-start identifiers."
				}
				notes = append(notes, Issue{Code: "CONTENT_DUPLICATE_ID", File: doc.name, Detail: "Links to this identifier may be ambiguous: " + id, Repairable: eligible, RepairKind: "duplicate_ids", RepairReason: reason})
				if eligible && choices.FixDuplicateIDs {
					nextSuffix := 2
					for n, occurrence := range byID[id] {
						if n == 0 || removed[occurrence.element] && choices.RemoveScripts {
							continue
						}
						suffix := n + 1
						if suffix < nextSuffix {
							suffix = nextSuffix
						}
						replacement := ""
						base := id
						if !validXMLIdentifier(base) {
							base = "spineguard-" + id
							if !validXMLIdentifier(base) {
								hash := sha256.Sum256([]byte(id))
								base = fmt.Sprintf("spineguard-id-%x", hash[:6])
							}
						}
						for {
							replacement = fmt.Sprintf("%s-spineguard-%d", base, suffix)
							if !allIDs[replacement] {
								break
							}
							suffix++
						}
						allIDs[replacement] = true
						nextSuffix = suffix + 1
						edits = append(edits, xmlEdit{Start: occurrence.attribute.Start, End: occurrence.attribute.End, Replacement: escapeXMLAttribute(replacement, occurrence.attribute.Quote)})
						// Keep a same-element legacy alias consistent with a renamed ID.
						for _, at := range doc.elements[occurrence.element].Attributes {
							if (at.Name == "name" || at.Name == "id" || at.Name == "xml:id") && at.Value == id && at.Start != occurrence.attribute.Start {
								edits = append(edits, xmlEdit{Start: at.Start, End: at.End, Replacement: escapeXMLAttribute(replacement, at.Quote)})
							}
						}
						count++
					}
				}
			}
			if count > 0 {
				actions = append(actions, RepairAction{Kind: "duplicate_ids", File: doc.name, Description: fmt.Sprintf("Rename %d safely independent or redundant duplicate identifiers, preserving first destinations and document text.", count), Count: count})
			}
		}
		if len(edits) > 0 {
			candidate, err := applyXMLEdits(doc.src, edits)
			if err != nil {
				// No partial document edit is accepted. Remove its proposed actions
				// and make its findings explain why review is required.
				kept := actions[:0]
				for _, action := range actions {
					if action.File != doc.name {
						kept = append(kept, action)
					}
				}
				actions = kept
				for i := range notes {
					if notes[i].File == doc.name {
						notes[i].Repairable = false
						notes[i].RepairReason = "The proposed source edit did not pass XML verification: " + err.Error()
					}
				}
				continue
			}
			modified[doc.name] = candidate
		}
	}
	return modified, actions, notes
}
