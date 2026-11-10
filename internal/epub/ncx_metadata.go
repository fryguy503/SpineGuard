package epub

import (
	"context"
	"fmt"
	"strings"
)

const ncxNS = "http://www.daisy.org/z3986/2005/ncx/"

type packageNCXIdentity struct{ pkg, ncx, identifier string }

func packageNCXIdentities(ctx context.Context, text map[string][]byte) []packageNCXIdentity {
	identities := []packageNCXIdentity{}
	packages := repairPackageNames(text)
	for _, name := range sortedTextNames(text) {
		if !packages[name] {
			continue
		}
		if ctx.Err() != nil {
			return identities
		}
		elements, err := parseEditableXML(ctx, text[name])
		if err != nil {
			continue
		}
		metadata := packageMetadata(elements)
		if metadata.root < 0 {
			continue
		}
		var pkg packageDoc
		if decoder(text[name]).Decode(&pkg) != nil || !strings.HasPrefix(pkg.Version, "2.") || pkg.Spine.TOC == "" {
			continue
		}
		found := 0
		href := ""
		for _, item := range pkg.Manifest {
			if item.ID == pkg.Spine.TOC {
				found++
				if item.MediaType == "application/x-dtbncx+xml" {
					href = item.Href
				}
			}
		}
		if found != 1 || href == "" {
			continue
		}
		target, fragment, external, err := resolveReference(name, href)
		if err != nil || external || fragment != "" {
			continue
		}
		if _, ok := text[target]; !ok {
			continue
		}
		identifier := ""
		matches := 0
		if metadata.hasRef && metadata.ref.Value != "" {
			for _, index := range metadata.identifiers {
				at, ok := identifierAttribute(elements[index])
				if !ok || at.Value != metadata.ref.Value || metadata.idCounts[at.Value] != 1 {
					continue
				}
				value, ok := sourceElementText(text[name], elements[index])
				if ok && value != "" {
					identifier = value
					matches++
				}
			}
		}
		if matches != 1 {
			identifier = ""
		}
		identities = append(identities, packageNCXIdentity{name, target, identifier})
	}
	return identities
}

func rawElementName(src []byte, element xmlElementSpan) string {
	end := element.Start + 1
	for end < element.OpenEnd && !xmlSpace(src[end]) && src[end] != '>' && src[end] != '/' {
		end++
	}
	return string(src[element.Start+1 : end])
}

func appendNCXMetadataChild(src []byte, head xmlElementSpan, identifier string) xmlEdit {
	name := rawElementName(src, head)
	prefix := ""
	if i := strings.IndexByte(name, ':'); i >= 0 {
		prefix = name[:i+1]
	}
	child := `<` + prefix + `meta name="dtb:uid" content="` + string(escapeXMLAttribute(identifier, '"')) + `"/>`
	if !head.SelfClosing {
		edit, _ := appendMetadataChild(src, head, child)
		return edit
	}
	opening := src[head.Start:head.OpenEnd]
	p := len(opening) - 2
	for p > 0 && xmlSpace(opening[p]) {
		p--
	}
	replacement := string(opening[:p]) + ">" + child + "</" + name + ">"
	return xmlEdit{Start: head.Start, End: head.End, Replacement: []byte(replacement)}
}

// NCX navigation identity must match the active OPF publication identity. This
// changes neither the publication value nor any chapter/navigation destination.
func planNCXMetadata(ctx context.Context, text map[string][]byte, choices RepairOptions) (map[string][]byte, []RepairAction, []Issue) {
	modified := map[string][]byte{}
	actions := []RepairAction{}
	notes := []Issue{}
	pairs := packageNCXIdentities(ctx, text)
	byNCX := map[string][]packageNCXIdentity{}
	for _, pair := range pairs {
		byNCX[pair.ncx] = append(byNCX[pair.ncx], pair)
	}
	for _, name := range sortedTextNames(text) {
		references := byNCX[name]
		if len(references) == 0 {
			continue
		}
		if ctx.Err() != nil {
			return modified, actions, notes
		}
		expected := ""
		conflict := false
		for _, pair := range references {
			if pair.identifier == "" {
				conflict = true
			}
			if expected == "" {
				expected = pair.identifier
			} else if pair.identifier != expected {
				conflict = true
			}
		}
		if expected == "" {
			continue
		}
		src := text[name]
		elements, err := parseEditableXML(ctx, src)
		if err != nil || len(elements) == 0 || elements[0].Name.Local != "ncx" || elements[0].Name.Space != ncxNS {
			continue
		}
		heads := []int{}
		uids := []int{}
		for i, element := range elements {
			if element.Parent == 0 && element.Name.Local == "head" && element.Name.Space == ncxNS {
				heads = append(heads, i)
			}
		}
		if len(heads) == 1 {
			for i, element := range elements {
				if element.Parent != heads[0] || element.Name.Local != "meta" || element.Name.Space != ncxNS {
					continue
				}
				at, ok := spanAttribute(element, "name")
				if ok && at.Value == "dtb:uid" {
					uids = append(uids, i)
				}
			}
		}
		actual := ""
		if len(uids) == 1 {
			if at, ok := spanAttribute(elements[uids[0]], "content"); ok {
				actual = strings.TrimSpace(at.Value)
			}
		}
		if !conflict && len(heads) == 1 && len(uids) == 1 && actual == expected {
			continue
		}
		eligible := !conflict && len(heads) == 1 && len(uids) <= 1
		reason := "Synchronize NCX dtb:uid with the active package identifier; preserve the identifier value and all navigation points."
		if conflict {
			reason = "Multiple packages share this NCX with conflicting or unresolved identifiers. Restore their metadata before synchronizing navigation identity."
		} else if len(heads) != 1 || len(uids) > 1 {
			reason = "The NCX head or publication identifier is missing or ambiguous. Review its metadata manually."
		}
		detail := fmt.Sprintf("NCX dtb:uid %q must match the active OPF identifier %q.", actual, expected)
		notes = append(notes, Issue{Code: "NCX_IDENTIFIER_MISMATCH", File: name, Detail: detail, Repairable: eligible, RepairKind: "metadata", RepairReason: reason})
		if !eligible || !choices.FixMetadata {
			continue
		}
		var edit xmlEdit
		if len(uids) == 1 {
			edit = setSpanAttribute(src, elements[uids[0]], "content", expected)
		} else {
			edit = appendNCXMetadataChild(src, elements[heads[0]], expected)
		}
		candidate, err := applyXMLEdits(src, []xmlEdit{edit})
		if err != nil {
			notes[len(notes)-1].Repairable = false
			notes[len(notes)-1].RepairReason = "NCX metadata could not be edited without altering verified XML: " + err.Error()
			continue
		}
		modified[name] = candidate
		actions = append(actions, RepairAction{Kind: "metadata", File: name, Description: "Synchronize NCX dtb:uid with the reviewed active publication identifier; preserve chapter destinations and play order.", Count: 1})
	}
	return modified, actions, notes
}

func checkNCXIdentifiers(a *archive) {
	_, _, notes := planNCXMetadata(a.ctx, a.text, RepairOptions{})
	for _, note := range notes {
		a.issue(note.Code, "error", "NCX publication identifier does not match", note.Detail, note.File, note.Repairable)
	}
}
