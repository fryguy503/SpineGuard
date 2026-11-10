package epub

import (
	"bytes"
	"strings"
)

const svgNS = "http://www.w3.org/2000/svg"

// A unique case-only grammar match can be corrected; unknown alignment values
// cannot be inferred from an image's appearance and remain a manual finding.
func aspectRatioValue(value string) (canonical string, valid, repairable bool) {
	fields := strings.Fields(value)
	if len(fields) == 0 || len(fields) > 3 {
		return "", false, false
	}
	i := 0
	canonicalFields := []string{}
	if strings.EqualFold(fields[0], "defer") {
		canonicalFields = append(canonicalFields, "defer")
		i++
	}
	if i >= len(fields) {
		return "", false, false
	}
	align := fields[i]
	lower := strings.ToLower(align)
	if lower == "none" {
		canonicalFields = append(canonicalFields, "none")
	} else if svgAspectCase.MatchString(lower) {
		parts := strings.Split(lower[1:], "y")
		capital := func(s string) string { return strings.ToUpper(s[:1]) + s[1:] }
		canonicalFields = append(canonicalFields, "x"+capital(parts[0])+"Y"+capital(parts[1]))
	} else {
		return "", false, false
	}
	i++
	if i < len(fields) {
		mode := strings.ToLower(fields[i])
		if mode != "meet" && mode != "slice" {
			return "", false, false
		}
		canonicalFields = append(canonicalFields, mode)
		i++
	}
	if i != len(fields) {
		return "", false, false
	}
	canonical = strings.Join(canonicalFields, " ")
	valid = true
	for i, f := range fields {
		if f != canonicalFields[i] {
			valid = false
			break
		}
	}
	return canonical, valid, !valid
}

func emptyGuide(src []byte, elements []xmlElementSpan, index int) (empty, editable bool) {
	el := elements[index]
	if el.Name.Local != "guide" || el.Name.Space != opfNS || el.Parent != 0 {
		return false, false
	}
	for _, child := range elements {
		if child.Parent == index {
			return false, false
		}
	}
	value, ok := sourceElementText(src, el)
	if !ok || value != "" {
		return false, false
	}
	if len(el.Attributes) > 0 {
		return true, false
	}
	if el.SelfClosing {
		return true, true
	}
	end := bytes.LastIndexByte(src[el.OpenEnd:el.End], '<') + el.OpenEnd
	return true, end >= el.OpenEnd && len(bytes.TrimSpace(src[el.OpenEnd:end])) == 0
}

func planDocumentMarkup(name string, src []byte, elements []xmlElementSpan, choices RepairOptions) ([]xmlEdit, []RepairAction, []Issue) {
	var edits []xmlEdit
	actions := []RepairAction{}
	notes := []Issue{}
	packageRoot := len(elements) > 0 && elements[0].Name.Local == "package" && elements[0].Name.Space == opfNS
	for i, el := range elements {
		if packageRoot {
			if empty, eligible := emptyGuide(src, elements, i); empty {
				reason := "An empty optional guide container can be removed without changing navigation destinations."
				if !eligible {
					reason = "The guide contains attributes or nontrivial source content. Review it rather than deleting it automatically."
				}
				notes = append(notes, Issue{Code: "OPF_GUIDE_EMPTY", File: name, Repairable: eligible, RepairKind: "document_markup", RepairReason: reason})
				if eligible && choices.FixDocumentMarkup {
					edits = append(edits, xmlEdit{Start: el.Start, End: el.End})
					actions = append(actions, RepairAction{Kind: "document_markup", File: name, Description: "Remove an empty optional OPF guide container; preserve all reading-order and navigation resources.", Count: 1})
				}
			}
		}
		if el.Name.Space != svgNS {
			continue
		}
		for _, at := range el.Attributes {
			if at.Name != "preserveAspectRatio" {
				continue
			}
			value, valid, eligible := aspectRatioValue(at.Value)
			if valid {
				continue
			}
			reason := "The SVG alignment value is not a recognized exact grammar match. Choose a valid alignment manually."
			if eligible {
				reason = "Correct the unique case-only SVG alignment value to " + value + "."
			}
			notes = append(notes, Issue{Code: "SVG_PRESERVE_ASPECT_RATIO", File: name, Detail: at.Value, Repairable: eligible, RepairKind: "document_markup", RepairReason: reason})
			if eligible && choices.FixDocumentMarkup {
				edits = append(edits, xmlEdit{Start: at.Start, End: at.End, Replacement: escapeXMLAttribute(value, at.Quote)})
				actions = append(actions, RepairAction{Kind: "document_markup", File: name, Description: "Correct SVG preserveAspectRatio from " + at.Value + " to " + value + " using the unique canonical case match.", Count: 1})
			}
		}
	}
	return edits, actions, notes
}

func checkAdditionalDocuments(a *archive) {
	for _, name := range sortedTextNames(a.text) {
		if a.ctx.Err() != nil {
			return
		}
		src := a.text[name]
		elements, err := parseEditableXML(a.ctx, src)
		if err != nil {
			continue
		}
		doc := packageMetadata(elements)
		if doc.metadata >= 0 {
			for _, index := range doc.languages {
				value, ok := sourceElementText(src, elements[index])
				if !ok || value == "" {
					continue
				}
				if _, err := canonicalLanguage(value); err != nil {
					a.issue("METADATA_LANGUAGE_INVALID", "error", "Book language tag is invalid", value+": "+err.Error(), name, true)
				}
			}
		}
		for i, el := range elements {
			if doc.root >= 0 {
				if empty, eligible := emptyGuide(src, elements, i); empty {
					a.issue("OPF_GUIDE_EMPTY", "error", "Optional guide container is empty", "An OPF guide must contain reference entries when present. An empty container can be removed without inventing navigation.", name, eligible)
				}
			}
			if el.Name.Space != svgNS {
				continue
			}
			for _, at := range el.Attributes {
				if at.Name != "preserveAspectRatio" {
					continue
				}
				canonical, valid, eligible := aspectRatioValue(at.Value)
				if !valid {
					detail := at.Value
					if eligible {
						detail += " has the unique canonical spelling " + canonical
					} else {
						detail += " is not a recognized SVG alignment grammar value"
					}
					a.issue("SVG_PRESERVE_ASPECT_RATIO", "error", "SVG image alignment value is invalid", detail, name, eligible)
				}
			}
		}
	}
	checkNCXIdentifiers(a)
}
