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

func staleNCXCoverFixture(t *testing.T, root bool) []fixtureEntry {
	t.Helper()
	entries := fixtureEntries()
	opf, ncx, page, picture := "OPS/book.opf", "OPS/toc.ncx", "OPS/titlepage.xhtml", "OPS/cover.png"
	chapter, nav, missing := "chapter.xhtml#start", "nav.xhtml", "part0000.xhtml"
	if root {
		opf, ncx, page, picture = "content.opf", "toc.ncx", "titlepage.xhtml", "cover.png"
		chapter, nav, missing = "OPS/chapter.xhtml#start", "OPS/nav.xhtml", "OEBPS/part0000.xhtml"
		entries[1].body = strings.ReplaceAll(entries[1].body, "OPS/book.opf", opf)
		entries[2].body = strings.ReplaceAll(entries[2].body, `href="chapter.xhtml"`, `href="OPS/chapter.xhtml"`)
		entries[2].body = strings.ReplaceAll(entries[2].body, `href="nav.xhtml"`, `href="`+nav+`"`)
	}
	entries[2].name = opf
	entries[2].body = strings.Replace(entries[2].body, `version="3.0"`, `version="2.0"`, 1)
	entries[2].body = strings.Replace(entries[2].body, `</metadata>`, `<meta name="cover" content="cover-image"/></metadata>`, 1)
	entries[2].body = strings.Replace(entries[2].body, `</manifest>`, `<item id="cover-page" href="titlepage.xhtml" media-type="application/xhtml+xml"/><item id="cover-image" href="cover.png" media-type="image/png"/><item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest>`, 1)
	entries[2].body = strings.Replace(entries[2].body, `<spine>`, `<spine toc="ncx"><itemref idref="cover-page"/>`, 1)
	entries[2].body = strings.Replace(entries[2].body, `</package>`, `<guide><reference type="cover" title="Cover" href="titlepage.xhtml"/></guide></package>`, 1)
	navigation := `<?xml version="1.0"?><ncx xmlns="` + ncxNS + `" version="2005-1"><head><meta name="dtb:uid" content="urn:uuid:test"/></head><docTitle><text>Test Book</text></docTitle><navMap><navPoint id="cover" playOrder="1"><navLabel><text>Cover</text></navLabel><content src="` + missing + `"/></navPoint><navPoint id="chapter" playOrder="2"><navLabel><text>Chapter</text></navLabel><content src="` + chapter + `"/></navPoint></navMap></ncx>`
	wrapper := `<?xml version="1.0" encoding="utf-8"?><html xmlns="` + xhtmlNS + `"><head><meta name="calibre:cover" content="true"/><title>Cover</title><style>@page {padding:0; margin:0} body {text-align:center}</style></head><body><div><svg xmlns="` + svgNS + `" xmlns:xlink="http://www.w3.org/1999/xlink" width="100%" height="100%" viewBox="0 0 1 1"><image width="1" height="1" xlink:href="cover.png"/></svg></div></body></html>`
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return append(entries, fixtureEntry{ncx, navigation, zip.Deflate}, fixtureEntry{page, wrapper, zip.Deflate}, fixtureEntry{picture, pixels.String(), zip.Deflate})
}

func replaceCoverFixture(t *testing.T, entries []fixtureEntry, name, old, replacement string) []fixtureEntry {
	t.Helper()
	for i := range entries {
		if entries[i].name == name {
			if !strings.Contains(entries[i].body, old) {
				t.Fatalf("fixture %s lacks %q", name, old)
			}
			entries[i].body = strings.Replace(entries[i].body, old, replacement, 1)
			return entries
		}
	}
	t.Fatalf("fixture lacks %s", name)
	return nil
}

func TestRepairNCXCoverUsesGuideAndWrapperIdentity(t *testing.T) {
	for _, root := range []bool{false, true} {
		t.Run(map[bool]string{false: "inside OPS", true: "root NCX and package"}[root], func(t *testing.T) {
			entries := staleNCXCoverFixture(t, root)
			ncx, old := "OPS/toc.ncx", `src="part0000.xhtml"`
			if root {
				ncx, old = "toc.ncx", `src="OEBPS/part0000.xhtml"`
			}
			input := writeFixture(t, entries)
			before, err := sourceDigest(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
			if err != nil || countActions(plan, "links") != 1 {
				t.Fatalf("missing proven repair: %+v %v", plan, err)
			}
			if len(plan.Unresolved) != 0 {
				t.Fatalf("proven cover remains unresolved: %+v", plan.Unresolved)
			}
			found := false
			for _, issue := range plan.Report.Issues {
				if issue.Code == "LINK_MISSING" && issue.File == ncx {
					found = issue.Repairable && issue.RepairKind == "links" && strings.Contains(issue.RepairReason, "explicitly declared cover page")
				}
			}
			if !found {
				t.Fatalf("cover repair rationale not exposed: %+v", plan.Report.Issues)
			}
			output := filepath.Join(t.TempDir(), "cover-fixed.epub")
			fixed, err := RepairWithOptions(context.Background(), input, output, Options{}, plan.Options)
			if err != nil {
				t.Fatal(err)
			}
			want := bytes.Replace(archiveEntryBytes(t, input, ncx, false), []byte(old), []byte(`src="titlepage.xhtml"`), 1)
			if !bytes.Equal(archiveEntryBytes(t, output, ncx, false), want) {
				t.Fatal("NCX changes were not restricted to the stale src bytes")
			}
			if fixed.Report.Stats.Words != plan.Report.Stats.Words || hasIssue(fixed.Report, "LINK_MISSING") {
				t.Fatalf("word count changed or missing link remains: %+v", fixed.Report)
			}
			for _, entry := range entries {
				if entry.name != ncx && !bytes.Equal(archiveEntryBytes(t, input, entry.name, true), archiveEntryBytes(t, output, entry.name, true)) {
					t.Fatalf("unmodified compressed resource changed: %s", entry.name)
				}
			}
			after, err := sourceDigest(context.Background(), input)
			if err != nil || before != after {
				t.Fatal("original book was changed")
			}
			choices := DefaultRepairOptions()
			choices.FixLinks = false
			unselected, err := PlanRepair(context.Background(), input, Options{}, choices)
			if err != nil || len(unselected.Actions) != 0 || !hasIssue(unselected.Report, "LINK_MISSING") {
				t.Fatalf("disabled link repair was applied: %+v %v", unselected, err)
			}
		})
	}
}

func TestRepairNCXCoverRefusesUnprovenRoles(t *testing.T) {
	for _, tc := range []struct{ name, file, old, replacement string }{
		{"non-cover label", "OPS/toc.ncx", `<text>Cover</text>`, `<text>Chapter One</text>`},
		{"duplicate cover label", "OPS/toc.ncx", `</navMap>`, `<navPoint id="other" playOrder="3"><navLabel><text>Cover</text></navLabel><content src="chapter.xhtml"/></navPoint></navMap>`},
		{"nonleaf duplicate cover label", "OPS/toc.ncx", `</navMap>`, `<navPoint id="parent" playOrder="3"><navLabel><text>Cover</text></navLabel><content src="chapter.xhtml"/><navPoint id="nested" playOrder="4"><navLabel><text>Nested</text></navLabel><content src="chapter.xhtml#start"/></navPoint></navPoint></navMap>`},
		{"cover point has child", "OPS/toc.ncx", `<content src="part0000.xhtml"/>`, `<content src="part0000.xhtml"/><navPoint id="nested" playOrder="3"><navLabel><text>Nested</text></navLabel><content src="chapter.xhtml"/></navPoint>`},
		{"content has child", "OPS/toc.ncx", `<content src="part0000.xhtml"/>`, `<content src="part0000.xhtml"><navPoint id="nested" playOrder="3"><navLabel><text>Nested</text></navLabel><content src="chapter.xhtml"/></navPoint></content>`},
		{"content has text", "OPS/toc.ncx", `<content src="part0000.xhtml"/>`, `<content src="part0000.xhtml">Unexpected text</content>`},
		{"duplicate guide", "OPS/book.opf", `</guide>`, `<reference type="cover" title="Cover" href="titlepage.xhtml"/></guide>`},
		{"guide has no label", "OPS/book.opf", `title="Cover"`, `title=""`},
		{"conflicting guide label", "OPS/book.opf", `title="Cover"`, `title="Frontispiece"`},
		{"guide target missing", "OPS/book.opf", `title="Cover" href="titlepage.xhtml"`, `title="Cover" href="absent.xhtml"`},
		{"guide href duplicated", "OPS/book.opf", `title="Cover" href="titlepage.xhtml"`, `title="Cover" href="titlepage.xhtml" href="chapter.xhtml"`},
		{"guide foreign namespace", "OPS/book.opf", `<guide>`, `<guide xmlns="urn:foreign">`},
		{"manifest foreign namespace", "OPS/book.opf", `<manifest>`, `<manifest xmlns="urn:foreign">`},
		{"spine foreign namespace", "OPS/book.opf", `<spine toc="ncx">`, `<spine xmlns="urn:foreign" toc="ncx">`},
		{"manifest duplicate identity", "OPS/book.opf", `</manifest>`, `<item id="cover-page" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest>`},
		{"duplicate cover metadata content", "OPS/book.opf", `<meta name="cover" content="cover-image"/>`, `<meta name="cover" content="cover-image" content="other-image"/>`},
		{"duplicate cover metadata name", "OPS/book.opf", `<meta name="cover" content="cover-image"/>`, `<meta name="cover" name="other-role" content="cover-image"/>`},
		{"duplicate manifest properties", "OPS/book.opf", `href="cover.png" media-type="image/png"`, `href="cover.png" media-type="image/png" properties="" properties="cover-image"`},
		{"prefixed metadata role alias", "OPS/book.opf", `<meta name="cover" content="cover-image"/>`, `<meta xmlns:f="urn:foreign" name="cover" content="cover-image" f:content="other-image"/>`},
		{"prefixed manifest role alias", "OPS/book.opf", `href="cover.png" media-type="image/png"`, `xmlns:f="urn:foreign" href="cover.png" media-type="image/png" properties="cover-image" f:properties=""`},
		{"cover outside spine", "OPS/book.opf", `<itemref idref="cover-page"/>`, ``},
		{"nonactive NCX", "OPS/book.opf", `toc="ncx"`, `toc="missing"`},
		{"OPF base", "OPS/book.opf", `<package `, `<package xml:base="./" `},
		{"NCX base", "OPS/toc.ncx", `<ncx `, `<ncx xml:base="./" `},
		{"wrapper base", "OPS/titlepage.xhtml", `<html `, `<html xml:base="./" `},
		{"non-UTF8 NCX declaration", "OPS/toc.ncx", `<?xml version="1.0"?>`, `<?xml version="1.0" encoding="ISO-8859-1"?>`},
		{"non-UTF8 wrapper declaration", "OPS/titlepage.xhtml", `encoding="utf-8"`, `encoding="ISO-8859-1"`},
		{"foreign NCX", "OPS/toc.ncx", ncxNS, "urn:foreign"},
		{"foreign wrapper", "OPS/titlepage.xhtml", xhtmlNS, "urn:foreign"},
		{"missing fragment", "OPS/toc.ncx", `src="part0000.xhtml"`, `src="part0000.xhtml#missing"`},
		{"query ambiguity", "OPS/toc.ncx", `src="part0000.xhtml"`, `src="part0000.xhtml?mode=other"`},
		{"missing declared content", "OPS/book.opf", `</manifest>`, `<item id="missing-chapter" href="part0000.xhtml" media-type="application/xhtml+xml"/></manifest>`},
		{"another cover identity", "OPS/book.opf", `</manifest>`, `<item id="other-cover" href="cover2.png" media-type="image/png" properties="cover-image"/></manifest>`},
		{"wrapper has prose", "OPS/titlepage.xhtml", `<body>`, `<body>Not a cover-only page.`},
		{"wrapper has CDATA prose", "OPS/titlepage.xhtml", `<body>`, `<body><![CDATA[Chapter text]]>`},
		{"wrapper has another image", "OPS/titlepage.xhtml", `</div></body>`, `<img src="cover.png"/></div></body>`},
		{"wrapper image is not cover", "OPS/titlepage.xhtml", `xlink:href="cover.png"`, `xlink:href="chapter.xhtml"`},
		{"wrapper script", "OPS/titlepage.xhtml", `</head>`, `<script>window.changeCover()</script></head>`},
		{"wrapper event handler", "OPS/titlepage.xhtml", `<body>`, `<body onload="changeCover()">`},
		{"wrapper image srcset", "OPS/titlepage.xhtml", `<image `, `<image srcset="other.png 2x" `},
		{"wrapper external style", "OPS/titlepage.xhtml", `</head>`, `<link rel="stylesheet" href="https://example.invalid/style.css"/></head>`},
		{"wrapper CSS image", "OPS/titlepage.xhtml", `body {text-align:center}`, `body {background:url(other.png)}`},
		{"wrapper meta refresh", "OPS/titlepage.xhtml", `</head>`, `<meta http-equiv="refresh" content="0;url=chapter.xhtml"/></head>`},
		{"cover page already linked", "OPS/toc.ncx", `</navMap>`, `<navPoint id="second" playOrder="3"><navLabel><text>Front</text></navLabel><content src="titlepage.xhtml"/></navPoint></navMap>`},
		{"cover page already page-targeted", "OPS/toc.ncx", `</ncx>`, `<pageList><navLabel><text>Pages</text></navLabel><pageTarget id="p1" value="1" type="normal" playOrder="3"><navLabel><text>1</text></navLabel><content src="titlepage.xhtml"/></pageTarget></pageList></ncx>`},
		{"duplicate src attribute", "OPS/toc.ncx", `src="part0000.xhtml"`, `src="part0000.xhtml" src="chapter.xhtml"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := replaceCoverFixture(t, staleNCXCoverFixture(t, false), tc.file, tc.old, tc.replacement)
			plan, err := PlanRepair(context.Background(), writeFixture(t, entries), Options{}, DefaultRepairOptions())
			if err != nil {
				t.Fatal(err)
			}
			if countActions(plan, "links") != 0 {
				t.Fatalf("unproven cover role was repaired: %+v", plan.Actions)
			}
		})
	}
}

func TestRepairNCXCoverPreservesAlreadyValidAndCancelled(t *testing.T) {
	entries := replaceCoverFixture(t, staleNCXCoverFixture(t, false), "OPS/toc.ncx", `src="part0000.xhtml"`, `src="titlepage.xhtml"`)
	input := writeFixture(t, entries)
	plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
	if err != nil || len(plan.Actions) != 0 {
		t.Fatalf("valid cover was changed: %+v %v", plan.Actions, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = PlanRepair(ctx, input, Options{}, DefaultRepairOptions()); err == nil {
		t.Fatal("cancelled cover proof continued")
	}
}

func TestRepairNCXCoverLabelAndURLNormalization(t *testing.T) {
	entries := staleNCXCoverFixture(t, false)
	entries = replaceCoverFixture(t, entries, "OPS/toc.ncx", `<text>Cover</text>`, "<text>  COVER \n </text>")
	entries = replaceCoverFixture(t, entries, "OPS/book.opf", `href="titlepage.xhtml"`, `href="title%20page%20%C3%A9.xhtml"`)
	entries = replaceCoverFixture(t, entries, "OPS/book.opf", `href="titlepage.xhtml"`, `href="title%20page%20%C3%A9.xhtml"`)
	for i := range entries {
		if entries[i].name == "OPS/titlepage.xhtml" {
			entries[i].name = "OPS/title page é.xhtml"
		}
	}
	input := writeFixture(t, entries)
	output := filepath.Join(t.TempDir(), "encoded-cover.epub")
	fixed, err := Repair(context.Background(), input, output, Options{})
	if err != nil || hasIssue(fixed.Report, "LINK_MISSING") {
		t.Fatalf("encoded cover proof failed: %+v %v", fixed.Report.Issues, err)
	}
	ncx := string(archiveEntryBytes(t, output, "OPS/toc.ncx", false))
	if !strings.Contains(ncx, `src="title%20page%20%C3%A9.xhtml"`) || !strings.Contains(ncx, "<text>  COVER \n </text>") {
		t.Fatal("cover URL was not encoded or original label changed")
	}
}
