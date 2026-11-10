package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairDeclaredStylesheetAndCoverResourcePaths(t *testing.T) {
	entries := fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, `</metadata>`, `<meta name="cover" content="cover-image"/></metadata>`, 1)
	entries[2].body = strings.Replace(entries[2].body, `</manifest>`, `<item id="css" href="../stylesheet.css" media-type="text/css"/><item id="cover-image" href="../cover.png" media-type="image/png"/></manifest>`, 1)
	entries[3].body = `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Cover</title><link rel="stylesheet" type="text/css" href="css/stylesheet.css?theme=a&amp;b=2"/></head><body><p id="start">Keep book text.</p><img epub:type="cover" role="doc-cover" src="images/missing-isbn.jpg"/></body></html>`
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	entries = append(entries, fixtureEntry{"stylesheet.css", ".cover {height:100%}", zip.Deflate}, fixtureEntry{"cover.png", pixels.String(), zip.Deflate})
	input := writeFixture(t, entries)
	plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "links") != 2 {
		t.Fatalf("declared resource evidence not used: %+v", plan)
	}
	output := filepath.Join(t.TempDir(), "fixed.epub")
	result, err := RepairWithOptions(context.Background(), input, output, Options{}, plan.Options)
	if err != nil {
		t.Fatal(err)
	}
	if hasIssue(result.Report, "LINK_MISSING") || result.Report.Stats.Words != plan.Report.Stats.Words {
		t.Fatalf("resource fixes incomplete: %+v", result.Report)
	}
	fixed := archiveEntryBytes(t, output, "OPS/chapter.xhtml", false)
	for _, want := range []string{`href="../stylesheet.css?theme=a&amp;b=2"`, `src="../cover.png"`, `<p id="start">Keep book text.</p>`} {
		if !bytes.Contains(fixed, []byte(want)) {
			t.Fatalf("missing exact correction %s: %s", want, fixed)
		}
	}
	for _, name := range []string{"stylesheet.css", "cover.png"} {
		if !bytes.Equal(archiveEntryBytes(t, input, name, true), archiveEntryBytes(t, output, name, true)) {
			t.Fatalf("resource payload rewritten: %s", name)
		}
	}
}

func resourceProofFixture(manifest, body string) (map[string][]byte, map[string]bool) {
	text := map[string][]byte{
		"META-INF/container.xml": []byte(`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf"/></rootfiles></container>`),
		"book.opf":               []byte(`<package xmlns="http://www.idpf.org/2007/opf"><metadata><meta name="cover" content="cover"/></metadata><manifest><item id="doc" href="OPS/cover.xhtml" media-type="application/xhtml+xml"/>` + manifest + `</manifest></package>`),
		"OPS/cover.xhtml":        []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Cover</title></head><body>` + body + `</body></html>`),
	}
	files := map[string]bool{"META-INF/container.xml": true, "book.opf": true, "OPS/cover.xhtml": true}
	return text, files
}

func TestResourceRepairRefusesAmbiguityWrongMediaAndBaseURI(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, body string
		files                []string
	}{
		{"ambiguous basename", `<item id="a" href="A/style.css" media-type="text/css"/><item id="b" href="B/style.css" media-type="text/css"/>`, `<link rel="stylesheet" href="css/style.css"/>`, []string{"A/style.css", "B/style.css"}},
		{"ambiguous declaration missing payload", `<item id="a" href="A/style.css" media-type="text/css"/><item id="b" href="B/style.css" media-type="text/css"/>`, `<link rel="stylesheet" href="css/style.css"/>`, []string{"A/style.css"}},
		{"wrong media", `<item id="a" href="style.css" media-type="image/png"/>`, `<link rel="stylesheet" href="css/style.css"/>`, []string{"style.css"}},
		{"cover role required", `<item id="cover" href="cover.png" media-type="image/png"/>`, `<img src="images/unknown.jpg"/>`, []string{"cover.png"}},
		{"conflicting cover declarations", `<item id="cover" href="cover.png" media-type="image/png"/><item id="another" href="another.png" media-type="image/png" properties="cover-image"/>`, `<img role="doc-cover" src="images/unknown.jpg"/>`, []string{"cover.png", "another.png"}},
		{"xml base", `<item id="a" href="style.css" media-type="text/css"/>`, `<div xml:base="other/"><link rel="stylesheet" href="css/style.css"/></div>`, []string{"style.css"}},
		{"html base", `<item id="a" href="style.css" media-type="text/css"/>`, `<base href="other/"/><link rel="stylesheet" href="css/style.css"/>`, []string{"style.css"}},
		{"foreign context", `<item id="a" href="cover.png" media-type="image/png"/>`, `<custom:img xmlns:custom="urn:custom" src="elsewhere/cover.png"/>`, []string{"cover.png"}},
		{"foreign cover annotation", `<item id="cover" href="cover.png" media-type="image/png"/>`, `<img xmlns:epub="urn:foreign" epub:type="cover" src="images/unknown.jpg"/>`, []string{"cover.png"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, files := resourceProofFixture(tc.manifest, tc.body)
			for _, name := range tc.files {
				files[name] = true
			}
			modified, actions, _ := planLinkDocuments(context.Background(), text, files, DefaultRepairOptions())
			if len(modified) != 0 || len(actions) != 0 {
				t.Fatalf("unproven reference modified: %+v", actions)
			}
		})
	}
}

func TestMissingScriptReferenceExplainsOptionalCleanup(t *testing.T) {
	text, files := resourceProofFixture("", `<script src="../js/kobo.js"/>`)
	_, actions, notes := planLinkDocuments(context.Background(), text, files, DefaultRepairOptions())
	if len(actions) != 0 || len(notes) != 1 || !notes[0].Repairable || !notes[0].OptionalRepair || notes[0].RepairKind != "reader_cleanup" {
		t.Fatalf("script dependency explanation missing: %+v %+v", actions, notes)
	}
}

func TestCSSDependencyProofDistinguishesIDsAndClasses(t *testing.T) {
	for _, tc := range []struct {
		css, id string
		blocked bool
	}{
		{`.toc-chapter {color:#444} p{height:0pt}`, "toc", false},
		{`.toc-title {color:red} [epub|type~="toc"] {display:block}`, "toc", false},
		{`/* #toc ignored */ .toc-front {color:#fff}`, "toc", false},
		{`#toc {color:red}`, "toc", true},
		{`#t\6f c {color:red}`, "toc", true},
		{`#\000074oc {color:red}`, "toc", true},
		{`[i\64 ="toc"] {color:red}`, "toc", true},
		{`[id] {color:red}`, "toc", true},
		{`p{fill:url("#t%6Fc")}`, "toc", true},
		{`p{content:attr(id)}`, "toc", true},
		{`@import "extra.css"; p{color:red}`, "toc", true},
		{`p{color:red`, "toc", true},
		{`/* unterminated`, "toc", true},
		{`p{content:"unterminated}`, "toc", true},
	} {
		if got, why := cssIDDependency([]byte(tc.css), tc.id); got != tc.blocked {
			t.Fatalf("%s for %s blocked=%v want%v: %s", tc.css, tc.id, got, tc.blocked, why)
		}
	}
}

func TestDuplicateIDPerCSSProofAndCanonicalReplacement(t *testing.T) {
	for _, id := range []string{"toc", "3Q280-chapter", "bad:name", "bad id"} {
		source := []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><section id="` + id + `">First</section><section id="` + id + `">Second</section></body></html>`)
		text := map[string][]byte{"chapter.xhtml": source, "stylesheet.css": []byte(`.toc-front {color:#444} .toc-title{display:block}`)}
		modified, actions, _ := planCleanupDocuments(context.Background(), text, DefaultRepairOptions())
		if len(actions) != 1 {
			t.Fatalf("class-only styles blocked %q: %+v", id, actions)
		}
		elements, err := parseEditableXML(context.Background(), modified["chapter.xhtml"])
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, el := range elements {
			for _, at := range el.Attributes {
				if at.Name == "id" {
					ids = append(ids, at.Value)
				}
			}
		}
		if len(ids) != 2 || ids[0] != id || ids[1] == id || !validXMLIdentifier(ids[1]) {
			t.Fatalf("invalid canonical replacement for %q: %v", id, ids)
		}
	}
}

func TestRedundantChapterStartKeepsFirstTargetAndLinks(t *testing.T) {
	source := []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body id="3Q280-start"><div id="3Q280-start" style="height:0pt"></div><p>Keep all text.</p></body></html>`)
	refs := []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><a href="chapter.xhtml#3Q280-start">Chapter</a></body></html>`)
	text := map[string][]byte{"chapter.xhtml": source, "links.xhtml": refs, "stylesheet.css": []byte(`.calibre {color:#444}`)}
	modified, actions, _ := planCleanupDocuments(context.Background(), text, DefaultRepairOptions())
	if len(actions) != 1 {
		t.Fatalf("redundant anchor not repairable: %+v", actions)
	}
	if !bytes.Contains(modified["chapter.xhtml"], []byte(`<body id="3Q280-start">`)) || !bytes.Contains(modified["chapter.xhtml"], []byte(`<p>Keep all text.</p>`)) {
		t.Fatalf("first target or prose changed: %s", modified["chapter.xhtml"])
	}
	if _, ok := modified["links.xhtml"]; ok || !bytes.Equal(text["links.xhtml"], refs) {
		t.Fatal("existing links rewritten")
	}
	// A nonempty duplicate destination cannot use the chapter-start proof.
	text["chapter.xhtml"] = bytes.Replace(source, []byte(`></div>`), []byte(`>Other text</div>`), 1)
	modified, actions, _ = planCleanupDocuments(context.Background(), text, DefaultRepairOptions())
	if len(modified) > 0 || len(actions) > 0 {
		t.Fatal("nonempty referenced duplicate guessed")
	}
	// A same-spelled fragment that targets another file must not block this file.
	text["chapter.xhtml"] = []byte(`<html><body><p id="x">First</p><p id="x">Second</p></body></html>`)
	text["links.xhtml"] = []byte(`<html><body><a href="elsewhere.xhtml#x">Elsewhere</a></body></html>`)
	modified, actions, _ = planCleanupDocuments(context.Background(), text, DefaultRepairOptions())
	if len(actions) != 1 || len(modified["chapter.xhtml"]) == 0 {
		t.Fatal("cross-document fragment spelling conflated")
	}
}

func TestDuplicateIDProofInspectsDeclaredExtensionlessCSS(t *testing.T) {
	text, _ := resourceProofFixture(`<item id="style" href="styledata" media-type="text/css"/>`, `<link rel="stylesheet" href="../styledata"/><p id="x">First</p><p id="x">Second</p>`)
	text["styledata"] = []byte(`.toc-front {color:#444}`)
	modified, actions, _ := planCleanupDocuments(context.Background(), text, DefaultRepairOptions())
	if len(actions) != 1 || len(modified["OPS/cover.xhtml"]) == 0 {
		t.Fatalf("readable extensionless CSS blocked proof: %+v", actions)
	}
	text["styledata"] = []byte(`#\78 {color:red}`)
	modified, actions, _ = planCleanupDocuments(context.Background(), text, DefaultRepairOptions())
	if len(actions) != 0 || len(modified) != 0 {
		t.Fatal("escaped ID in extensionless CSS was missed")
	}
}
