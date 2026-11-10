package epub

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/language"
)

const dcNS = "http://purl.org/dc/elements/1.1/"

func canonicalLanguage(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 100 {
		return "", fmt.Errorf("enter a valid language tag such as en or en-US")
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return "", fmt.Errorf("use a language tag such as en or en-US, with hyphens rather than spaces or underscores")
		}
	}
	tag, err := language.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid language tag %q: %w", value, err)
	}
	return tag.String(), nil
}

func ValidateRepairOptions(choices RepairOptions) error {
	if choices.FixMetadata && strings.TrimSpace(choices.Language) != "" {
		_, err := canonicalLanguage(choices.Language)
		return err
	}
	return nil
}

func repairSuggestions(text map[string][]byte) RepairSuggestions {
	values := map[string][]string{}
	for _, name := range sortedTextNames(text) {
		elements, err := parseEditableXML(context.Background(), text[name])
		if err != nil || len(elements) == 0 {
			continue
		}
		root := elements[0]
		if root.Name.Local != "html" || root.Name.Space != xhtmlNS {
			continue
		}
		for _, at := range root.Attributes {
			if at.Name != "lang" && at.Name != "xml:lang" {
				continue
			}
			value, err := canonicalLanguage(at.Value)
			if err == nil {
				values[value] = append(values[value], name+" ("+at.Name+"="+at.Value+")")
			}
		}
	}
	if len(values) != 1 {
		return RepairSuggestions{}
	}
	for value, sources := range values {
		sort.Strings(sources)
		return RepairSuggestions{Language: value, LanguageSources: sources}
	}
	return RepairSuggestions{}
}

type metadataDoc struct {
	root, metadata         int
	identifiers, languages []int
	idCounts               map[string]int
	ref                    xmlAttributeSpan
	hasRef                 bool
}

func packageMetadata(elements []xmlElementSpan) metadataDoc {
	doc := metadataDoc{root: -1, metadata: -1, idCounts: map[string]int{}}
	if len(elements) == 0 || elements[0].Name.Local != "package" || elements[0].Name.Space != opfNS {
		return doc
	}
	doc.root = 0
	for _, at := range elements[0].Attributes {
		if at.Name == "unique-identifier" {
			doc.ref = at
			doc.hasRef = true
		}
	}
	for i, el := range elements {
		for _, at := range el.Attributes {
			if at.Name == "id" || at.Name == "xml:id" {
				doc.idCounts[at.Value]++
			}
		}
		if el.Parent == 0 && el.Name.Local == "metadata" && el.Name.Space == opfNS {
			if doc.metadata != -1 {
				doc.metadata = -2
			} else {
				doc.metadata = i
			}
		}
	}
	if doc.metadata < 0 {
		return doc
	}
	for i, el := range elements {
		if el.Parent != doc.metadata || el.Name.Space != dcNS {
			continue
		}
		switch el.Name.Local {
		case "identifier":
			doc.identifiers = append(doc.identifiers, i)
		case "language":
			doc.languages = append(doc.languages, i)
		}
	}
	return doc
}

func sourceElementText(src []byte, element xmlElementSpan) (string, bool) {
	d := xml.NewDecoder(bytes.NewReader(src[element.Start:element.End]))
	d.Entity = recognizedDoctypeEntities(src)
	depth := 0
	var value strings.Builder
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			if depth > 1 {
				return "", false
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 1 {
				value.Write(t)
			}
		}
	}
	return strings.TrimSpace(value.String()), true
}

func spanAttribute(element xmlElementSpan, name string) (xmlAttributeSpan, bool) {
	for _, at := range element.Attributes {
		if at.Name == name {
			return at, true
		}
	}
	return xmlAttributeSpan{}, false
}
func identifierAttribute(element xmlElementSpan) (xmlAttributeSpan, bool) {
	if at, ok := spanAttribute(element, "id"); ok {
		return at, true
	}
	return spanAttribute(element, "xml:id")
}
func validXMLIdentifier(value string) bool {
	for i, r := range value {
		if i == 0 {
			if !(unicode.IsLetter(r) || r == '_') {
				return false
			}
		} else if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return value != ""
}
func uniqueMetadataID(doc metadataDoc) string {
	value := "spineguard-publication-id"
	for n := 2; doc.idCounts[value] > 0; n++ {
		value = fmt.Sprintf("spineguard-publication-id-%d", n)
	}
	return value
}

func openingInsertion(src []byte, el xmlElementSpan) int {
	p := el.OpenEnd - 1
	for p > el.Start && xmlSpace(src[p-1]) {
		p--
	}
	if el.SelfClosing {
		p = el.OpenEnd - 2
		for p > el.Start && xmlSpace(src[p]) {
			p--
		}
	}
	return p
}
func setSpanAttribute(src []byte, el xmlElementSpan, name, value string) xmlEdit {
	if at, ok := spanAttribute(el, name); ok {
		return xmlEdit{Start: at.Start, End: at.End, Replacement: escapeXMLAttribute(value, at.Quote)}
	}
	p := openingInsertion(src, el)
	return xmlEdit{Start: p, End: p, Replacement: []byte(" " + name + "=\"" + string(escapeXMLAttribute(value, '"')) + "\"")}
}
func escapeXMLText(value string) []byte {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(value))
	return b.Bytes()
}
func replaceElementText(src []byte, el xmlElementSpan, value string) xmlEdit {
	if el.SelfClosing {
		opening := src[el.Start:el.OpenEnd]
		p := bytes.LastIndexByte(opening, '/')
		nameEnd := 1
		for nameEnd < len(opening) && !xmlSpace(opening[nameEnd]) && opening[nameEnd] != '/' && opening[nameEnd] != '>' {
			nameEnd++
		}
		name := opening[1:nameEnd]
		replacement := append([]byte{}, opening[:p]...)
		replacement = append(replacement, '>')
		replacement = append(replacement, escapeXMLText(value)...)
		replacement = append(replacement, []byte("</"+string(name)+">")...)
		return xmlEdit{Start: el.Start, End: el.End, Replacement: replacement}
	}
	end := bytes.LastIndexByte(src[el.OpenEnd:el.End], '<') + el.OpenEnd
	return xmlEdit{Start: el.OpenEnd, End: end, Replacement: escapeXMLText(value)}
}

func fillEmptyIdentifier(src []byte, el xmlElementSpan, id, value string) []xmlEdit {
	if !el.SelfClosing {
		return []xmlEdit{setSpanAttribute(src, el, "id", id), replaceElementText(src, el, value)}
	}
	attribute := setSpanAttribute(src, el, "id", id)
	opening := append([]byte{}, src[el.Start:attribute.Start]...)
	opening = append(opening, attribute.Replacement...)
	opening = append(opening, src[attribute.End:el.OpenEnd]...)
	end := bytes.LastIndexByte(opening, '/')
	nameEnd := 1
	for nameEnd < len(opening) && !xmlSpace(opening[nameEnd]) && opening[nameEnd] != '/' && opening[nameEnd] != '>' {
		nameEnd++
	}
	name := string(opening[1:nameEnd])
	replacement := append([]byte{}, opening[:end]...)
	replacement = append(replacement, '>')
	replacement = append(replacement, escapeXMLText(value)...)
	replacement = append(replacement, []byte("</"+name+">")...)
	return []xmlEdit{{Start: el.Start, End: el.End, Replacement: replacement}}
}
func appendMetadataChild(src []byte, el xmlElementSpan, child string) (xmlEdit, bool) {
	if el.SelfClosing {
		return xmlEdit{}, false
	}
	p := bytes.LastIndexByte(src[el.OpenEnd:el.End], '<')
	if p < 0 {
		return xmlEdit{}, false
	}
	p += el.OpenEnd
	return xmlEdit{Start: p, End: p, Replacement: []byte("\n    " + child + "\n  ")}, true
}

func planPackageMetadata(name string, src []byte, elements []xmlElementSpan, choices RepairOptions) ([]xmlEdit, []RepairAction, []Issue) {
	doc := packageMetadata(elements)
	if doc.root < 0 || doc.metadata < 0 {
		return nil, nil, nil
	}
	var edits []xmlEdit
	actions := []RepairAction{}
	notes := []Issue{}
	missingLanguage := len(doc.languages) == 0
	badLanguage := []int{}
	for _, index := range doc.languages {
		value, ok := sourceElementText(src, elements[index])
		if !ok {
			continue
		}
		if _, err := canonicalLanguage(value); err != nil {
			badLanguage = append(badLanguage, index)
			if value == "" {
				missingLanguage = true
			}
		}
	}
	if missingLanguage || len(badLanguage) > 0 {
		code := "METADATA_LANGUAGE_INVALID"
		if missingLanguage {
			code = "METADATA_LANGUAGE"
		}
		reason := "Enter the book's correct language in the repair review. A document-language suggestion is evidence to review, not an automatic choice."
		notes = append(notes, Issue{Code: code, File: name, Repairable: true, RepairKind: "metadata", RepairReason: reason})
		if choices.FixMetadata && strings.TrimSpace(choices.Language) != "" {
			value, err := canonicalLanguage(choices.Language)
			if err == nil {
				count := 0
				if len(doc.languages) == 0 {
					child := `<dc:language xmlns:dc="` + dcNS + `">` + value + `</dc:language>`
					if edit, ok := appendMetadataChild(src, elements[doc.metadata], child); ok {
						edits = append(edits, edit)
						count++
					}
				} else {
					for _, index := range badLanguage {
						edits = append(edits, replaceElementText(src, elements[index], value))
						count++
					}
				}
				if count > 0 {
					actions = append(actions, RepairAction{Kind: "metadata", File: name, Description: "Set missing or invalid publication language to " + value + " from the reviewed language input.", Count: count})
				}
			}
		}
	}
	validRef := false
	nonempty := []int{}
	unreadableIdentifier := false
	for _, index := range doc.identifiers {
		value, ok := sourceElementText(src, elements[index])
		if !ok {
			unreadableIdentifier = true
			continue
		}
		if value == "" {
			continue
		}
		nonempty = append(nonempty, index)
		at, hasID := identifierAttribute(elements[index])
		if hasID && doc.hasRef && doc.ref.Value != "" && at.Value == doc.ref.Value && doc.idCounts[at.Value] == 1 {
			validRef = true
		}
	}
	if !validRef {
		reason := "The publication identifier needs manual review; existing values are not replaced or guessed."
		eligible := false
		generationBlocked := ""
		if len(nonempty) == 0 && len(doc.identifiers) == 1 {
			identifier := elements[doc.identifiers[0]]
			for _, at := range identifier.Attributes {
				if at.Local == "scheme" && strings.TrimSpace(at.Value) != "" && !strings.EqualFold(strings.TrimSpace(at.Value), "uuid") {
					generationBlocked = "The empty identifier declares a different identifier scheme. Review that metadata before inserting a UUID."
				}
			}
			if at, ok := identifierAttribute(identifier); ok && at.Value != "" && (!validXMLIdentifier(at.Value) || doc.idCounts[at.Value] != 1) {
				generationBlocked = "The empty identifier's ID is invalid or ambiguous. Correct that ID before generating a publication value."
			}
			if at, ok := spanAttribute(identifier, "xml:id"); ok && at.Value == "" {
				generationBlocked = "An empty XML ID needs manual correction before generating a publication value."
			}
		}
		var identifierEdits []xmlEdit
		description := ""
		requiresOptIn := false
		if unreadableIdentifier {
			reason = "Publication identifier metadata contains nested or unrecognized content. Review it manually rather than replacing its contents."
		} else if generationBlocked != "" {
			reason = generationBlocked
		} else if len(nonempty) == 1 {
			index := nonempty[0]
			identifier := elements[index]
			at, hasID := identifierAttribute(identifier)
			if hasID && validXMLIdentifier(at.Value) && doc.idCounts[at.Value] == 1 {
				identifierEdits = append(identifierEdits, setSpanAttribute(src, elements[doc.root], "unique-identifier", at.Value))
				eligible = true
				description = "Restore the package reference to its sole existing publication identifier, preserving the identifier value."
			} else if !hasID || at.Value == "" {
				id := doc.ref.Value
				if !validXMLIdentifier(id) || doc.idCounts[id] > 0 {
					id = uniqueMetadataID(doc)
				}
				identifierEdits = append(identifierEdits, setSpanAttribute(src, identifier, "id", id))
				if !doc.hasRef || doc.ref.Value != id {
					identifierEdits = append(identifierEdits, setSpanAttribute(src, elements[doc.root], "unique-identifier", id))
				}
				eligible = true
				description = "Restore the missing ID attribute on the sole existing publication identifier, preserving its value."
			}
		} else if len(nonempty) == 0 && choices.GenerateIdentifier {
			if choices.ExpectedSHA256 != "" && len(doc.identifiers) <= 1 {
				id := doc.ref.Value
				if !validXMLIdentifier(id) || doc.idCounts[id] > 0 {
					id = uniqueMetadataID(doc)
				}
				value := "urn:uuid:" + uuid.NewSHA1(uuid.NameSpaceURL, []byte("SpineGuard publication identity:"+strings.ToLower(choices.ExpectedSHA256))).String()
				if len(doc.identifiers) == 0 {
					child := `<dc:identifier xmlns:dc="` + dcNS + `" id="` + id + `">` + value + `</dc:identifier>`
					if edit, ok := appendMetadataChild(src, elements[doc.metadata], child); ok {
						identifierEdits = append(identifierEdits, edit)
						eligible = true
					}
				} else {
					identifier := elements[doc.identifiers[0]]
					at, hasID := identifierAttribute(identifier)
					if hasID && at.Value != "" {
						if validXMLIdentifier(at.Value) && doc.idCounts[at.Value] == 1 {
							id = at.Value
							identifierEdits = append(identifierEdits, replaceElementText(src, identifier, value))
							eligible = true
						}
					} else {
						identifierEdits = append(identifierEdits, fillEmptyIdentifier(src, identifier, id, value)...)
						eligible = true
					}
				}
				if eligible {
					identifierEdits = append(identifierEdits, setSpanAttribute(src, elements[doc.root], "unique-identifier", id))
					description = "Generate the explicitly requested publication identifier " + value + "; preview and saved copy use the same source-bound value."
					requiresOptIn = true
				}
			}
		}
		if eligible {
			reason = description
		} else if len(nonempty) == 0 && !unreadableIdentifier && generationBlocked == "" {
			reason = "No usable publication identifier exists. Choose Generate identifier to create a new source-bound UUID after review; protected font metadata is preserved."
		}
		notes = append(notes, Issue{Code: "METADATA_IDENTIFIER", File: name, Repairable: eligible || len(nonempty) == 0 && !unreadableIdentifier && generationBlocked == "", RepairKind: "metadata", OptionalRepair: len(nonempty) == 0 && !unreadableIdentifier && generationBlocked == "", RepairReason: reason})
		if eligible && choices.FixMetadata {
			edits = append(edits, identifierEdits...)
			actions = append(actions, RepairAction{Kind: "metadata", File: name, Description: description, Count: 1, RequiresOptIn: requiresOptIn})
		}
	}
	return edits, actions, notes
}

var svgAspectCase = regexp.MustCompile(`^x(?:min|mid|max)y(?:min|mid|max)$`)

func planMetadataDocuments(ctx context.Context, text map[string][]byte, choices RepairOptions) (map[string][]byte, []RepairAction, []Issue) {
	modified := map[string][]byte{}
	actions := []RepairAction{}
	notes := []Issue{}
	for _, name := range sortedTextNames(text) {
		if ctx.Err() != nil {
			break
		}
		src := text[name]
		working := src
		localActions := []RepairAction{}
		if choices.NormalizeEntities {
			candidate, count, err := normalizeStandardEntities(ctx, working)
			if err == nil && count > 0 {
				working = candidate
				localActions = append(localActions, RepairAction{Kind: "entities", File: name, Description: fmt.Sprintf("Normalize %d standard XHTML named character references to numeric Unicode references; preserve their text meaning.", count), Count: count, RequiresOptIn: true})
			}
		}
		elements, err := parseEditableXML(ctx, working)
		if err != nil {
			continue
		}
		edits, metadataActions, metadataNotes := planPackageMetadata(name, working, elements, choices)
		notes = append(notes, metadataNotes...)
		localActions = append(localActions, metadataActions...)
		markupEdits, markupActions, markupNotes := planDocumentMarkup(name, working, elements, choices)
		edits = append(edits, markupEdits...)
		localActions = append(localActions, markupActions...)
		notes = append(notes, markupNotes...)
		if len(edits) > 0 {
			candidate, err := applyXMLEdits(working, edits)
			if err != nil {
				for i := range notes {
					if notes[i].File == name {
						notes[i].Repairable = false
						notes[i].RepairReason = "The proposed metadata or markup edit did not pass XML verification: " + err.Error()
					}
				}
				continue
			}
			working = candidate
		}
		if !bytes.Equal(src, working) {
			modified[name] = working
			actions = append(actions, localActions...)
		}
	}
	ncxText := make(map[string][]byte, len(text))
	for name, b := range text {
		ncxText[name] = b
	}
	for name, b := range modified {
		ncxText[name] = b
	}
	ncxModified, ncxActions, ncxNotes := planNCXMetadata(ctx, ncxText, choices)
	for name, b := range ncxModified {
		modified[name] = b
	}
	actions = append(actions, ncxActions...)
	notes = append(notes, ncxNotes...)
	return modified, actions, notes
}
