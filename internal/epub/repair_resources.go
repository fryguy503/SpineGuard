package epub

import (
	"context"
	"encoding/xml"
	"path"
	"strings"
)

type repairResourceCatalog struct {
	media    map[string]string
	files    map[string]bool
	cover    string
	complete bool
}

// Only manifest identities are used. Missing payloads are never fabricated.
func buildRepairResourceCatalog(ctx context.Context, text map[string][]byte, files map[string]bool) repairResourceCatalog {
	c := repairResourceCatalog{media: map[string]string{}, files: files}
	var container struct {
		XMLName   xml.Name
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if decoder(text["META-INF/container.xml"]).Decode(&container) != nil || len(container.Rootfiles) != 1 || container.XMLName.Local != "container" || container.XMLName.Space != containerNS {
		return c
	}
	root := container.Rootfiles[0].FullPath
	elements, err := parseEditableXML(ctx, text[root])
	if err != nil || len(elements) == 0 || elements[0].Name.Local != "package" || elements[0].Name.Space != opfNS {
		return c
	}
	manifest := []manifestItem{}
	coverMetadata := []string{}
	for _, el := range elements {
		if el.Parent < 0 || el.Name.Space != opfNS {
			continue
		}
		parent := elements[el.Parent]
		if parent.Parent != 0 || parent.Name.Space != opfNS {
			continue
		}
		if el.Name.Local == "item" && parent.Name.Local == "manifest" {
			manifest = append(manifest, manifestItem{ID: repairAttribute(el, "id"), Href: repairAttribute(el, "href"), MediaType: repairAttribute(el, "media-type"), Properties: repairAttribute(el, "properties")})
		}
		if el.Name.Local == "meta" && parent.Name.Local == "metadata" && repairAttribute(el, "name") == "cover" {
			coverMetadata = append(coverMetadata, repairAttribute(el, "content"))
		}
	}
	c.complete = true
	byID := map[string]string{}
	coverNames := map[string]bool{}
	coverInvalid := false
	for _, item := range manifest {
		name, fragment, external, err := resolveReference(root, item.Href)
		if err != nil || external || fragment != "" {
			c.complete = false
			continue
		}
		media := strings.ToLower(strings.TrimSpace(strings.Split(item.MediaType, ";")[0]))
		if old, exists := c.media[name]; exists && old != media {
			c.media[name] = ""
		} else {
			c.media[name] = media
		}
		if old, exists := byID[item.ID]; exists && old != name {
			byID[item.ID] = ""
		} else {
			byID[item.ID] = name
		}
		if tokenHas(item.Properties, "cover-image") {
			coverNames[name] = true
		}
	}
	for _, coverID := range coverMetadata {
		if name := byID[coverID]; name != "" {
			coverNames[name] = true
		} else {
			coverInvalid = true
		}
	}
	if len(coverNames) == 1 && !coverInvalid {
		for name := range coverNames {
			if strings.HasPrefix(c.media[name], "image/") && files[name] {
				c.cover = name
			}
		}
	}
	return c
}

func repairAttribute(element xmlElementSpan, name string) string {
	for _, a := range element.Attributes {
		if a.Name == name {
			return a.Value
		}
	}
	return ""
}

func hasRepairBase(elements []xmlElementSpan) bool {
	for _, el := range elements {
		if el.Name.Local == "base" && (el.Name.Space == xhtmlNS || el.Name.Space == "") {
			return true
		}
		for _, at := range el.Attributes {
			if at.Name == "xml:base" {
				return true
			}
		}
	}
	return false
}

// A resource's role must agree with its manifest media type before a path edit.
func referenceResourceKind(el xmlElementSpan, at xmlAttributeSpan) string {
	if el.Name.Space != xhtmlNS && el.Name.Space != "http://www.w3.org/2000/svg" && el.Name.Space != "http://www.daisy.org/z3986/2005/ncx/" {
		return ""
	}
	if at.Name != "href" && at.Name != "src" && at.Name != "poster" && at.Name != "data" && at.Name != "xlink:href" {
		return ""
	}
	switch el.Name.Local {
	case "a", "area":
		if at.Name == "href" {
			return "document"
		}
	case "content":
		if at.Name == "src" {
			return "document"
		}
	case "link":
		if at.Name == "href" && tokenHas(strings.ToLower(repairAttribute(el, "rel")), "stylesheet") {
			return "stylesheet"
		}
	case "img", "image":
		if at.Local == "src" || at.Local == "href" {
			return "image"
		}
	case "script":
		if at.Name == "src" || at.Local == "href" {
			return "script"
		}
	case "video":
		if at.Name == "poster" {
			return "image"
		}
		if at.Name == "src" {
			return "video"
		}
	case "audio":
		if at.Name == "src" {
			return "audio"
		}
	case "object":
		if at.Name == "data" && strings.TrimSpace(repairAttribute(el, "type")) != "" {
			return "type:" + strings.ToLower(strings.TrimSpace(repairAttribute(el, "type")))
		}
	}
	return ""
}

func resourceMediaCompatible(kind, media string) bool {
	switch kind {
	case "stylesheet":
		return media == "text/css"
	case "image":
		return strings.HasPrefix(media, "image/")
	case "script":
		return media == "application/javascript" || media == "text/javascript" || media == "application/ecmascript" || media == "text/ecmascript"
	case "document":
		return media == "application/xhtml+xml" || media == "image/svg+xml"
	case "audio":
		return strings.HasPrefix(media, "audio/")
	case "video":
		return strings.HasPrefix(media, "video/")
	default:
		return strings.HasPrefix(kind, "type:") && media == strings.TrimPrefix(kind, "type:")
	}
}

func (c repairResourceCatalog) replacement(target, kind string, coverRole bool) (string, string) {
	if !c.complete || kind == "" {
		return "", ""
	}
	if coverRole && kind == "image" && c.cover != "" {
		return c.cover, "Use the single existing image identified as the publication cover by its package metadata."
	}
	matches := []string{}
	for name, media := range c.media {
		if path.Base(name) == path.Base(target) && resourceMediaCompatible(kind, media) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 1 && c.files[matches[0]] {
		return matches[0], "Correct the resource path to the single manifest-declared entry with the same exact filename and compatible media type."
	}
	return "", ""
}

func isExplicitCoverImage(elements []xmlElementSpan, index int) bool {
	el := elements[index]
	if el.Name.Local != "img" || el.Name.Space != xhtmlNS {
		return false
	}
	if tokenHas(repairAttribute(el, "role"), "doc-cover") {
		return true
	}
	for _, at := range el.Attributes {
		if at.Local == "type" && tokenHas(at.Value, "cover") {
			if colon := strings.IndexByte(at.Name, ':'); colon >= 0 && repairNamespaceURI(elements, index, at.Name[:colon]) == "http://www.idpf.org/2007/ops" {
				return true
			}
		}
	}
	return false
}

func repairNamespaceURI(elements []xmlElementSpan, index int, prefix string) string {
	for index >= 0 {
		for _, at := range elements[index].Attributes {
			if at.Name == "xmlns:"+prefix {
				return at.Value
			}
		}
		index = elements[index].Parent
	}
	return ""
}

func referenceNamespaceSupported(elements []xmlElementSpan, index int, at xmlAttributeSpan) bool {
	if !strings.Contains(at.Name, ":") {
		return true
	}
	if at.Name != "xlink:href" {
		return false
	}
	return repairNamespaceURI(elements, index, "xlink") == "http://www.w3.org/1999/xlink"
}
