package epub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Scan performs bounded, offline checks. A successful built-in result is not a
// complete standards-conformance verdict and cannot predict firmware failures.
func Scan(ctx context.Context, filename string, opts Options) (report Report, err error) {
	start := time.Now()
	opts = opts.normalized()
	report = Report{Path: filename, Filename: filepath.Base(filename), ScannedAt: start.UTC(), Issues: []Issue{}}
	if abs, e := filepath.Abs(filename); e == nil {
		report.Path = abs
	}
	defer func() { report.DurationMS = time.Since(start).Milliseconds(); Recount(&report) }()
	st, e := os.Stat(filename)
	if e != nil {
		report.Status = "unreadable"
		report.Issues = append(report.Issues, Issue{Code: "FILE_READ", Severity: "error", Title: "Book cannot be opened", Detail: e.Error()})
		return report, e
	}
	report.FileBytes = st.Size()
	if !st.Mode().IsRegular() {
		report.Status = "unreadable"
		return report, fmt.Errorf("book is not a regular file")
	}
	a, e := openArchive(ctx, filename, &report)
	if e != nil {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		report.Status = "unreadable"
		report.Issues = append(report.Issues, Issue{Code: "ZIP_INVALID", Severity: "error", Title: "Invalid or truncated ZIP archive", Detail: e.Error()})
		return report, nil
	}
	defer a.z.Close()
	defer annotateRepairIssues(a, &report)
	if len(a.z.File) > maxEntries {
		return report, nil
	}
	a.inspectProtection()
	container, ok := a.text["META-INF/container.xml"]
	if !ok {
		a.issue("CONTAINER_MISSING", "error", "EPUB container is missing", "META-INF/container.xml is required to locate the package.", "META-INF/container.xml", false)
		return report, nil
	}
	var c struct {
		XMLName   xml.Name
		Rootfiles []struct {
			FullPath  string `xml:"full-path,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	root, parseErr := validateXML(container)
	if parseErr != nil {
		a.xmlError("META-INF/container.xml", container, parseErr)
		return report, nil
	}
	if root.Local != "container" || root.Space != containerNS {
		a.issue("CONTAINER_NAMESPACE", "error", "Invalid container root or namespace", "Expected container in "+containerNS, "META-INF/container.xml", false)
	}
	if e = decoder(container).Decode(&c); e != nil {
		return report, nil
	}
	opf := ""
	for _, rf := range c.Rootfiles {
		if rf.MediaType == "application/oebps-package+xml" && opf == "" {
			opf = rf.FullPath
		}
	}
	if opf == "" {
		a.issue("PACKAGE_LOCATION", "error", "Package document is not declared", "The container must declare an application/oebps-package+xml rootfile.", "META-INF/container.xml", false)
		return report, nil
	}
	if !safeName(opf) {
		a.issue("PACKAGE_PATH", "error", "Unsafe package path", opf, "META-INF/container.xml", false)
		return report, nil
	}
	opfBytes, readErr := a.cacheText(opf)
	if readErr != nil {
		a.issue("PACKAGE_MISSING", "error", "Package document is missing", opf, "META-INF/container.xml", false)
		return report, nil
	}
	root, parseErr = validateXML(opfBytes)
	if parseErr != nil {
		a.xmlError(opf, opfBytes, parseErr)
		return report, nil
	}
	if root.Local != "package" || root.Space != opfNS {
		a.issue("PACKAGE_NAMESPACE", "error", "Invalid package root or namespace", "Expected package in "+opfNS, opf, false)
	}
	var pkg packageDoc
	if e = decoder(opfBytes).Decode(&pkg); e != nil {
		a.xmlError(opf, opfBytes, e)
		return report, nil
	}
	report.EPUBVersion = pkg.Version
	if !strings.HasPrefix(pkg.Version, "2.") && !strings.HasPrefix(pkg.Version, "3.") {
		a.issue("PACKAGE_VERSION", "error", "Unsupported or missing EPUB version", pkg.Version, opf, false)
	}
	for _, v := range pkg.Metadata.Titles {
		if v.XMLName.Space == "http://purl.org/dc/elements/1.1/" && report.Title == "" {
			report.Title = strings.TrimSpace(v.Text)
		}
	}
	var authors []string
	for _, v := range pkg.Metadata.Creators {
		if v.XMLName.Space == "http://purl.org/dc/elements/1.1/" && strings.TrimSpace(v.Text) != "" {
			authors = append(authors, strings.TrimSpace(v.Text))
		}
	}
	report.Author = strings.Join(authors, ", ")
	for _, v := range pkg.Metadata.Languages {
		if v.XMLName.Space == "http://purl.org/dc/elements/1.1/" && report.Language == "" {
			report.Language = strings.TrimSpace(v.Text)
		}
	}
	for _, v := range pkg.Metadata.Identifiers {
		if v.XMLName.Space == "http://purl.org/dc/elements/1.1/" && v.ID == pkg.UniqueIdentifier && pkg.UniqueIdentifier != "" {
			report.Identifier = strings.TrimSpace(v.Text)
		}
	}
	if report.Title == "" {
		a.issue("METADATA_TITLE", "error", "Book title is missing", "A Dublin Core title is required.", opf, false)
	}
	if report.Language == "" {
		a.issue("METADATA_LANGUAGE", "error", "Book language is missing", "A Dublin Core language is required; repairs do not invent metadata.", opf, false)
	}
	if report.Identifier == "" {
		a.issue("METADATA_IDENTIFIER", "error", "Unique publication identifier is missing", "The package unique-identifier must reference a nonempty dc:identifier.", opf, false)
	}
	if strings.HasPrefix(pkg.Version, "3.") {
		modified := false
		for _, m := range pkg.Metadata.Meta {
			if m.Property == "dcterms:modified" && strings.TrimSpace(m.Value) != "" {
				modified = true
			}
		}
		if !modified {
			a.issue("METADATA_MODIFIED", "error", "Modification timestamp is missing", "EPUB 3 requires dcterms:modified metadata.", opf, false)
		}
	}
	manifest := map[string]manifestItem{}
	targets := map[string]string{}
	navNames := map[string]bool{}
	mediaTypes := map[string]string{}
	cover := ""
	for _, item := range pkg.Manifest {
		if item.ID == "" || item.Href == "" || item.MediaType == "" {
			a.issue("MANIFEST_ATTRIBUTES", "error", "Incomplete manifest item", "Every item requires id, href, and media-type.", opf, false)
			continue
		}
		if _, ok := manifest[item.ID]; ok {
			a.issue("MANIFEST_DUPLICATE_ID", "error", "Duplicate manifest identifier", item.ID, opf, false)
			continue
		}
		manifest[item.ID] = item
		target, _, external, er := resolveReference(opf, item.Href)
		if er != nil {
			a.issue("MANIFEST_PATH", "error", "Invalid manifest resource path", item.Href+": "+er.Error(), opf, false)
			continue
		}
		if external {
			a.issue("MANIFEST_REMOTE", "warning", "Manifest contains a remote resource", item.Href, opf, false)
			continue
		}
		if old, ok := targets[target]; ok && old != item.ID {
			a.issue("MANIFEST_DUPLICATE_RESOURCE", "warning", "Resource appears more than once in manifest", target, opf, false)
		}
		targets[target] = item.ID
		mediaTypes[target] = item.MediaType
		if item.MediaType == "application/xhtml+xml" || item.MediaType == "image/svg+xml" || item.MediaType == "application/x-dtbncx+xml" || item.MediaType == "text/css" {
			_, _ = a.cacheText(target)
		}
		if _, exists := a.files[target]; !exists {
			a.issue("MANIFEST_MISSING", "error", "Manifest resource is missing", item.Href+" resolves to "+target, opf, false)
		}
		if tokenHas(item.Properties, "nav") {
			navNames[target] = true
		}
		if tokenHas(item.Properties, "cover-image") {
			cover = target
		}
		if strings.HasPrefix(item.MediaType, "image/") {
			report.Stats.Images++
			a.checkImage(target, item.MediaType)
		}
		if strings.HasPrefix(item.MediaType, "font/") || strings.Contains(item.MediaType, "font") || strings.HasSuffix(strings.ToLower(target), ".ttf") || strings.HasSuffix(strings.ToLower(target), ".otf") {
			report.Stats.Fonts++
		}
	}
	for _, m := range pkg.Metadata.Meta {
		if m.Name == "cover" && cover == "" {
			if item, ok := manifest[m.Content]; ok {
				cover, _, _, _ = resolveReference(opf, item.Href)
			}
		}
	}
	if len(pkg.Manifest) == 0 {
		a.issue("MANIFEST_EMPTY", "error", "Manifest is empty", "The book must declare its publication resources.", opf, false)
	}
	if strings.HasPrefix(pkg.Version, "3.") && len(navNames) == 0 {
		a.issue("NAV_MISSING", "error", "EPUB 3 navigation document is missing", "A manifest item with the nav property is required.", opf, false)
	}
	if strings.HasPrefix(pkg.Version, "2.") {
		item, exists := manifest[pkg.Spine.TOC]
		if !exists || item.MediaType != "application/x-dtbncx+xml" {
			a.issue("NCX_MISSING", "error", "EPUB 2 NCX navigation is missing", "The spine toc must reference the NCX manifest item.", opf, false)
		}
	}
	// Validate all XML resources and inspect markup without executing it.
	contents := map[string]contentInfo{}
	keys := make([]string, 0, len(a.text))
	for name := range a.text {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		if name == opf || name == "META-INF/container.xml" {
			continue
		}
		b := a.text[name]
		ext := strings.ToLower(path.Ext(name))
		if ext == ".css" || mediaTypes[name] == "text/css" {
			continue
		}
		if ext != ".xml" && ext != ".xhtml" && ext != ".html" && ext != ".htm" && ext != ".svg" && ext != ".ncx" && mediaTypes[name] != "application/xhtml+xml" && mediaTypes[name] != "image/svg+xml" && mediaTypes[name] != "application/x-dtbncx+xml" {
			continue
		}
		root, er := validateXML(b)
		if er != nil {
			a.xmlError(name, b, er)
		}
		if ext == ".xhtml" || ext == ".html" || ext == ".htm" || mediaTypes[name] == "application/xhtml+xml" {
			if er == nil && (root.Local != "html" || root.Space != xhtmlNS) {
				a.issue("CONTENT_NAMESPACE", "error", "Invalid XHTML root or namespace", "EPUB XHTML documents require html in "+xhtmlNS, name, false)
			}
			contents[name] = a.inspectContent(name, b)
		} else if ext == ".svg" || ext == ".ncx" || mediaTypes[name] == "image/svg+xml" || mediaTypes[name] == "application/x-dtbncx+xml" {
			contents[name] = inspectXMLReferences(b)
		}
	}
	for name := range navNames {
		if c, ok := contents[name]; ok && !c.nav {
			a.issue("NAV_TOC_MISSING", "error", "Navigation table of contents is missing", "The navigation document must contain nav epub:type=\"toc\".", name, false)
		}
	}
	for name, c := range contents {
		a.checkReferences(name, c.refs, contents)
	}
	for name, b := range a.text {
		if strings.HasSuffix(strings.ToLower(name), ".css") || mediaTypes[name] == "text/css" {
			a.checkReferences(name, cssReferences(b), contents)
		}
	}
	seen := map[string]bool{}
	pageBreaks := 0
	pageListCount := 0
	for _, sp := range pkg.Spine.Items {
		item, ok := manifest[sp.IDRef]
		if !ok {
			a.issue("SPINE_MISSING", "error", "Reading order references an unknown item", sp.IDRef, opf, false)
			continue
		}
		target, _, external, _ := resolveReference(opf, item.Href)
		if external {
			a.issue("SPINE_REMOTE", "error", "Reading order contains a remote document", item.Href, opf, false)
			continue
		}
		if seen[target] {
			a.issue("SPINE_DUPLICATE", "warning", "Document repeats in the reading order", target, opf, false)
			continue
		}
		seen[target] = true
		if sp.Linear == "no" {
			continue
		}
		if c, ok := contents[target]; ok {
			report.Stats.Chapters++
			report.Stats.Words += c.words
			if !c.pageList {
				pageBreaks += c.pages
			}
			if f := a.files[target]; f != nil {
				size := int64(f.UncompressedSize64)
				if size > report.Stats.LargestChapterBytes {
					report.Stats.LargestChapterBytes = size
				}
				if size > 1<<20 {
					a.issue("CHAPTER_LARGE", "warning", "Large reading-order document", fmt.Sprintf("%.1f MiB of markup in one document. The 1 MiB advisory is a SpineGuard heuristic, not a Kobo limit.", float64(size)/(1<<20)), target, false)
				}
			}
		} else if item.MediaType != "image/svg+xml" {
			a.issue("SPINE_CONTENT", "warning", "Reading-order document could not be analyzed", "Word and page estimates may be incomplete: "+target, opf, false)
		}
	}
	for _, c := range contents {
		if c.pageList && c.pages > pageListCount {
			pageListCount = c.pages
		}
	}
	if pageListCount > 0 {
		report.Stats.SourcePages = pageListCount
	} else {
		report.Stats.SourcePages = pageBreaks
	}
	if len(pkg.Spine.Items) == 0 {
		a.issue("SPINE_EMPTY", "error", "Reading order is empty", "The spine must contain itemref entries.", opf, false)
	}
	report.Stats.EstimatedPages = int(math.Ceil(float64(report.Stats.Words) / float64(opts.WordsPerPage)))
	report.Stats.ReadingMinutes = int(math.Ceil(float64(report.Stats.Words) / float64(opts.ReadingWPM)))
	a.coverData(cover)
	return report, nil
}

func (a *archive) xmlError(name string, b []byte, err error) {
	if dtdEntityUnsupported(b, err) {
		a.issue("XML_DTD_CHECK_INCOMPLETE", "warning", "DTD entity checks require EPUBCheck", "The native XML parser does not expand DTD-defined entities. This document's checks and word statistics may be incomplete. Run EPUBCheck to evaluate validity. "+err.Error(), name, false)
		return
	}
	_, repair := sanitizeXML(b)
	line := 0
	if x, ok := err.(*xml.SyntaxError); ok {
		line = x.Line
	}
	a.issue("XML_INVALID", "error", "Malformed XML document", err.Error(), name, repair)
	if len(a.report.Issues) > 0 {
		last := &a.report.Issues[len(a.report.Issues)-1]
		if last.Code == "XML_INVALID" && last.File == name {
			last.Line = line
		}
	}
}

func (a *archive) inspectProtection() {
	if _, ok := a.files["META-INF/signatures.xml"]; ok {
		a.protected = true
		a.issue("SIGNED_BOOK", "info", "Digitally signed book", "Repairs are disabled because repackaging or editing can invalidate signatures.", "META-INF/signatures.xml", false)
	}
	b, exists := a.text["META-INF/encryption.xml"]
	if !exists {
		return
	}
	if _, err := validateXML(b); err != nil {
		a.protected = true
		a.issue("ENCRYPTION_METADATA", "warning", "Encryption metadata cannot be interpreted", "Repairs are disabled to preserve protected resources.", "META-INF/encryption.xml", false)
		return
	}
	d := decoder(b)
	known := false
	unknown := false
	for {
		t, err := d.Token()
		if err != nil {
			break
		}
		if s, ok := t.(xml.StartElement); ok && s.Name.Local == "EncryptionMethod" {
			alg := ""
			for _, at := range s.Attr {
				if at.Name.Local == "Algorithm" {
					alg = at.Value
				}
			}
			switch alg {
			case "http://www.idpf.org/2008/embedding", "http://ns.adobe.com/pdf/enc#RC":
				known = true
			default:
				unknown = true
			}
		}
	}
	if unknown || !known {
		a.protected = true
		a.issue("DRM_ENCRYPTION", "warning", "Encrypted or protected resources", "Content may require authorization from its original provider. Repairs are disabled; SpineGuard does not remove DRM.", "META-INF/encryption.xml", false)
	} else {
		a.obfuscated = true
		a.issue("FONT_OBFUSCATION", "info", "Obfuscated fonts detected", "Recognized EPUB font obfuscation is preserved, together with the publication identifier.", "META-INF/encryption.xml", false)
	}
}

func (a *archive) checkImage(name, media string) {
	f := a.files[name]
	if f == nil || f.UncompressedSize64 > maxEntryBytes {
		return
	}
	if media == "image/svg+xml" {
		return
	}
	r, err := f.Open()
	if err != nil {
		return
	}
	defer r.Close()
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		a.issue("IMAGE_HEADER", "warning", "Image dimensions could not be read", "The format may be unsupported by the built-in decoder or the image may be damaged.", name, false)
		return
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 6000 || cfg.Height > 6000 || int64(cfg.Width)*int64(cfg.Height) > 20000000 {
		a.issue("IMAGE_LARGE", "warning", "Large image may increase reader memory use", fmt.Sprintf("%d × %d pixels. The 20-megapixel / 6,000-pixel advisory is a SpineGuard heuristic, not a Kobo limit.", cfg.Width, cfg.Height), name, false)
		return
	}
	if f.UncompressedSize64 <= maxTextBytes {
		pixels, err := f.Open()
		if err != nil {
			return
		}
		_, _, err = image.Decode(contextReader{a.ctx, pixels})
		pixels.Close()
		if err != nil && a.ctx.Err() == nil {
			a.issue("IMAGE_DECODE", "error", "Image pixel data is damaged", err.Error(), name, false)
		}
	}
}

func (a *archive) coverData(name string) {
	f := a.files[name]
	if f == nil || f.UncompressedSize64 > 2<<20 {
		return
	}
	b, err := a.read(name)
	if err != nil {
		return
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 6000 || cfg.Height > 6000 || int64(cfg.Width)*int64(cfg.Height) > 20000000 {
		return
	}
	mime := ""
	switch format {
	case "jpeg":
		mime = "image/jpeg"
	case "png":
		mime = "image/png"
	case "gif":
		mime = "image/gif"
	}
	if mime == "" {
		return
	}
	a.report.CoverData = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}
