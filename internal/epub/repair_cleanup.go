package epub

import (
	"bytes"
	"context"
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
	// Duplicate-ID rewriting is deliberately conservative in this version. Any
	// CSS or executable code may depend on ID spelling, even through selectors
	// or dynamically assembled strings that a structural checker cannot prove.
	dependentStyling := false
	dependentScripts := false
	fragmentRefs := map[string]bool{}
	idRefs := map[string]bool{}
	for _, name := range keys {
		if ctx.Err() != nil {
			return modified, actions, notes
		}
		src := text[name]
		ext := strings.ToLower(path.Ext(name))
		if ext == ".css" && len(bytes.TrimSpace(src)) > 0 {
			dependentStyling = true
		}
		if ext == ".js" && len(bytes.TrimSpace(src)) > 0 {
			dependentScripts = true
		}
		if bytes.Contains(src, []byte("application/javascript")) || bytes.Contains(src, []byte("text/javascript")) {
			dependentScripts = true
		}
		elements, err := parseEditableXML(ctx, src)
		if err != nil {
			if textResource(name) && ext != ".css" && bytes.Contains(bytes.ToLower(src), []byte("<script")) {
				notes = append(notes, Issue{Code: "CONTENT_SCRIPT", File: name, RepairKind: "reader_cleanup", OptionalRepair: true, RepairReason: "This document cannot be edited losslessly as UTF-8 XML. Run EPUBCheck or correct its XML/encoding first."})
			}
			continue
		}
		for _, element := range elements {
			if element.Name.Local == "style" && element.End > element.OpenEnd {
				dependentStyling = true
			}
			if element.Name.Local == "script" {
				dependentScripts = true
			}
			// Manifest media types and stylesheet links retain their meaning even
			// when a resource uses no extension or is not cached for inspection.
			if element.Name.Local == "item" {
				for _, at := range element.Attributes {
					if at.Local == "media-type" {
						media := strings.ToLower(at.Value)
						if media == "text/css" {
							dependentStyling = true
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
						dependentStyling = true
					}
				}
			}
			for _, at := range element.Attributes {
				if at.Name == "style" && strings.TrimSpace(at.Value) != "" {
					dependentStyling = true
				}
				if eventHandlerName(at.Name) || javascriptURL(at.Value) {
					dependentScripts = true
				}
				if at.Local == "href" || at.Local == "src" || at.Local == "textref" || at.Local == "refines" {
					if u, err := url.Parse(strings.TrimSpace(at.Value)); err == nil && u.Fragment != "" {
						fragmentRefs[u.Fragment] = true
					}
				}
				if strings.Contains(strings.ToLower(at.Value), "url(") {
					dependentStyling = true
				}
				if tokenHas("for headers list form contextmenu itemref popovertarget commandfor anchor", at.Name) || strings.HasPrefix(at.Name, "aria-") || at.Name == "begin" || at.Name == "end" {
					for _, id := range strings.Fields(at.Value) {
						idRefs[id] = true
					}
				}
				if at.Name == "begin" || at.Name == "end" {
					for _, timing := range strings.Split(at.Value, ";") {
						if dot := strings.IndexByte(strings.TrimSpace(timing), '.'); dot > 0 {
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
				}
			}
		}
		dups := make([]string, 0)
		reason := ""
		for id, occurrences := range byID {
			if len(occurrences) > 1 {
				dups = append(dups, id)
				if fragmentRefs[id] || idRefs[id] {
					reason = "A link or ID-reference depends on a repeated identifier. Its intended destination cannot be inferred automatically."
				}
			}
		}
		sort.Strings(dups)
		if len(dups) > 0 {
			if dependentScripts {
				reason = "Scripts may depend on identifier spelling. Duplicate identifiers need manual review."
			} else if dependentStyling {
				reason = "Styles may depend on identifier spelling. Duplicate identifiers need manual review."
			}
			eligible := reason == ""
			if eligible {
				reason = "Unused duplicate identifiers can be renamed while preserving the first occurrence. No stylesheet, script, link, or ID-reference dependencies were found."
			}
			notes = append(notes, Issue{Code: "CONTENT_DUPLICATE_ID", File: doc.name, Repairable: eligible, RepairKind: "duplicate_ids", RepairReason: reason})
			if eligible && choices.FixDuplicateIDs {
				count := 0
				for _, id := range dups {
					for n, occurrence := range byID[id] {
						if n == 0 || removed[occurrence.element] && choices.RemoveScripts {
							continue
						}
						suffix := n + 1
						replacement := ""
						for {
							replacement = fmt.Sprintf("%s-spineguard-%d", id, suffix)
							if !allIDs[replacement] {
								break
							}
							suffix++
						}
						allIDs[replacement] = true
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
				if count > 0 {
					actions = append(actions, RepairAction{Kind: "duplicate_ids", File: doc.name, Description: fmt.Sprintf("Rename %d unused duplicate identifiers, preserving the first destination and all document text.", count), Count: count})
				}
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
