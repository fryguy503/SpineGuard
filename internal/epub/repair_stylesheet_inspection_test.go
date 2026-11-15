package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const inspectionPageTemplate = `<ade:template xmlns:ade="http://ns.adobe.com/2006/ade" xmlns:fo="http://www.w3.org/1999/XSL/Format"><fo:layout-master-set><fo:simple-page-master master-name="single"><fo:region-body/></fo:simple-page-master></fo:layout-master-set><ade:style><ade:styling-rule selector=".title_box" display="adobe-other-region" adobe-region="xsl-region-before"/></ade:style></ade:template>`

func stylesheetInspectionFixture(template string, occurrences int) []fixtureEntry {
	entries := fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, `</manifest>`, `<item id="template" href="page-template.xpgt" media-type="application/vnd.adobe-page-template+xml"/><item id="css" href="styles.css" media-type="text/css"/></manifest>`, 1)
	entries[3].body = `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title><link rel="stylesheet" type="application/vnd.adobe-page-template+xml" href="page-template.xpgt"/><link rel="stylesheet" type="text/css" href="styles.css"/></head><body><h1 id="start">Hello world</h1>` + strings.Repeat(`<div class="calibre_pb_0" id="calibre_pb_0"></div>`, occurrences) + `<p>One two three.</p></body></html>`
	entries = append(entries,
		fixtureEntry{"OPS/page-template.xpgt", template, zip.Deflate},
		fixtureEntry{"OPS/styles.css", `.calibre_pb_0{display:block;page-break-after:always}.title_box{font-size:1.2em}`, zip.Deflate})
	return entries
}

func requireDuplicateAssessment(t *testing.T, report Report, id string, repairable bool) {
	t.Helper()
	found := 0
	for _, issue := range report.Issues {
		if issue.Code == "CONTENT_DUPLICATE_ID" && strings.HasSuffix(issue.Detail, ": "+id) {
			found++
			if issue.Repairable != repairable {
				t.Fatalf("%s repairable=%v, wanted %v: %+v", id, issue.Repairable, repairable, issue)
			}
		}
	}
	if found == 0 {
		t.Fatalf("missing duplicate assessment for %s: %+v", id, report.Issues)
	}
}

func TestDeclaredPageTemplateAllowsIndependentDuplicateRepairs(t *testing.T) {
	entries := stylesheetInspectionFixture(inspectionPageTemplate, 45)
	input := writeFixture(t, entries)
	original, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Report.Errors != 0 || countActions(plan, "duplicate_ids") != 44 {
		t.Fatalf("present page-template stylesheet prevented independent repairs: errors=%d warnings=%d actions=%+v", plan.Report.Errors, plan.Report.Warnings, plan.Actions)
	}
	requireDuplicateAssessment(t, plan.Report, "calibre_pb_0", true)
	output := filepath.Join(t.TempDir(), "repaired.epub")
	result, err := RepairWithOptions(context.Background(), input, output, Options{}, plan.Options)
	if err != nil {
		t.Fatal(err)
	}
	if hasIssue(result.Report, "CONTENT_DUPLICATE_ID") || result.Report.Stats.Words != plan.Report.Stats.Words || result.Report.Stats.Chapters != plan.Report.Stats.Chapters {
		t.Fatalf("duplicate repair changed book text/order or did not clear the warnings: %+v", result.Report)
	}
	fixed := archiveEntryBytes(t, output, "OPS/chapter.xhtml", false)
	want := entries[3].body
	first := strings.Index(want, `id="calibre_pb_0"`)
	if first < 0 {
		t.Fatal("fixture lacks the first destination")
	}
	prefixEnd := first + len(`id="calibre_pb_0"`)
	prefix, remainder := want[:prefixEnd], want[prefixEnd:]
	for suffix := 2; suffix <= 45; suffix++ {
		replacement := `id="calibre_pb_0-spineguard-` + strconv.Itoa(suffix) + `"`
		remainder = strings.Replace(remainder, `id="calibre_pb_0"`, replacement, 1)
	}
	if string(fixed) != prefix+remainder {
		t.Fatalf("repair changed bytes beyond the later ID values: %s", fixed)
	}
	for _, entry := range entries {
		if entry.name == "OPS/chapter.xhtml" || entry.name == "mimetype" {
			continue
		}
		if !bytes.Equal(archiveEntryBytes(t, input, entry.name, true), archiveEntryBytes(t, output, entry.name, true)) {
			t.Fatalf("unrelated compressed resource changed: %s", entry.name)
		}
	}
	after, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("original EPUB changed")
	}
	choices := DefaultRepairOptions()
	choices.FixDuplicateIDs = false
	disabled, err := PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil || countActions(disabled, "duplicate_ids") != 0 || len(disabled.Unresolved) != 44 {
		t.Fatalf("disabled duplicate repair did not remain a no-op: actions=%+v unresolved=%d error=%v", disabled.Actions, len(disabled.Unresolved), err)
	}
	unchangedOutput := filepath.Join(t.TempDir(), "duplicates-left-for-review.epub")
	unchanged, err := RepairWithOptions(context.Background(), input, unchangedOutput, Options{}, disabled.Options)
	if err != nil || !hasIssue(unchanged.Report, "CONTENT_DUPLICATE_ID") {
		t.Fatalf("disabled duplicate repair cleared its findings: %v", err)
	}
	if !bytes.Equal(archiveEntryBytes(t, input, "OPS/chapter.xhtml", true), archiveEntryBytes(t, unchangedOutput, "OPS/chapter.xhtml", true)) {
		t.Fatal("disabled duplicate repair changed the chapter payload")
	}
}

func TestPageTemplateIDSelectorBlocksOnlyItsIdentifier(t *testing.T) {
	entries := stylesheetInspectionFixture(strings.Replace(inspectionPageTemplate, `.title_box`, `#calibre_pb_0`, 1), 2)
	entries[3].body = strings.Replace(entries[3].body, `</body>`, `<p id="independent">Keep first.</p><p id="independent">Keep second.</p></body>`, 1)
	plan, err := PlanRepair(context.Background(), writeFixture(t, entries), Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	requireDuplicateAssessment(t, plan.Report, "calibre_pb_0", false)
	requireDuplicateAssessment(t, plan.Report, "independent", true)
	if countActions(plan, "duplicate_ids") != 1 {
		t.Fatalf("template selector proof was not per identifier: %+v", plan.Actions)
	}
}

func TestConflictingPageTemplateRolesLeaveDuplicateIdentifiersForReview(t *testing.T) {
	for _, firstMedia := range []string{pageTemplateMediaType, "text/css"} {
		t.Run(firstMedia, func(t *testing.T) {
			entries := stylesheetInspectionFixture(inspectionPageTemplate, 2)
			otherMedia := "text/css"
			if firstMedia == "text/css" {
				otherMedia = pageTemplateMediaType
				entries[2].body = strings.Replace(entries[2].body, pageTemplateMediaType, firstMedia, 1)
			}
			entries[2].body = strings.Replace(entries[2].body, `</manifest>`, `<item id="conflicting-template-role" href="page-template.xpgt" media-type="`+otherMedia+`"/></manifest>`, 1)
			// XML comments are passive in a page template but may contain active
			// selectors when the same payload is interpreted as a CSS resource.
			entries[5].body = strings.Replace(entries[5].body, `</ade:template>`, `<!-- {} #calibre_pb_0{} --></ade:template>`, 1)
			input := writeFixture(t, entries)
			plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
			if err != nil {
				t.Fatal(err)
			}
			requireDuplicateAssessment(t, plan.Report, "calibre_pb_0", false)
			if countActions(plan, "duplicate_ids") != 0 {
				t.Fatalf("conflicting resource roles allowed an identifier edit: %+v", plan.Actions)
			}
			output := filepath.Join(t.TempDir(), "left-for-review.epub")
			result, err := RepairWithOptions(context.Background(), input, output, Options{}, plan.Options)
			if err != nil {
				t.Fatal(err)
			}
			requireDuplicateAssessment(t, result.Report, "calibre_pb_0", false)
			for _, resource := range []string{"OPS/chapter.xhtml", "OPS/page-template.xpgt"} {
				if !bytes.Equal(archiveEntryBytes(t, input, resource, true), archiveEntryBytes(t, output, resource, true)) {
					t.Fatalf("manual-review resource changed: %s", resource)
				}
			}
		})
	}
}

func TestDeclaredStylesheetInspectionLoadsExtensionlessResourcesByMedia(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]fixtureEntry) []fixtureEntry
	}{
		{"extensionless page template", func(entries []fixtureEntry) []fixtureEntry {
			entries[5].name = "OPS/template-data"
			entries[2].body = strings.ReplaceAll(entries[2].body, "page-template.xpgt", "template-data")
			entries[3].body = strings.ReplaceAll(entries[3].body, "page-template.xpgt", "template-data")
			return entries
		}},
		{"normalized page template media", func(entries []fixtureEntry) []fixtureEntry {
			entries[5].name = "OPS/template-data"
			for _, index := range []int{2, 3} {
				entries[index].body = strings.ReplaceAll(entries[index].body, "page-template.xpgt", "template-data")
				entries[index].body = strings.ReplaceAll(entries[index].body, "application/vnd.adobe-page-template+xml", "APPLICATION/VND.ADOBE-PAGE-TEMPLATE+XML; charset=UTF-8")
			}
			return entries
		}},
		{"normalized extensionless CSS media", func(entries []fixtureEntry) []fixtureEntry {
			entries[6].name = "OPS/style-data"
			for _, index := range []int{2, 3} {
				entries[index].body = strings.ReplaceAll(entries[index].body, "styles.css", "style-data")
				entries[index].body = strings.ReplaceAll(entries[index].body, "text/css", "TEXT/CSS; charset=UTF-8")
			}
			return entries
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := tc.edit(stylesheetInspectionFixture(inspectionPageTemplate, 2))
			input := writeFixture(t, entries)
			scanned, err := Scan(context.Background(), input, Options{})
			if err != nil {
				t.Fatal(err)
			}
			requireDuplicateAssessment(t, scanned, "calibre_pb_0", true)
			plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
			if err != nil || countActions(plan, "duplicate_ids") != 1 {
				t.Fatalf("declared text resource stayed uninspectable: actions=%+v error=%v", plan.Actions, err)
			}
		})
	}
}

func TestUnknownPageTemplateDependenciesStillBlockDuplicateRepairs(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]fixtureEntry) []fixtureEntry
	}{
		{"missing payload", func(entries []fixtureEntry) []fixtureEntry { return append(entries[:5], entries[6:]...) }},
		{"unsupported namespace", func(entries []fixtureEntry) []fixtureEntry {
			entries[5].body = strings.Replace(entries[5].body, "http://ns.adobe.com/2006/ade", "urn:unknown-template", 1)
			return entries
		}},
		{"conflicting template media", func(entries []fixtureEntry) []fixtureEntry {
			entries[2].body = strings.Replace(entries[2].body, "application/vnd.adobe-page-template+xml", "text/css", 1)
			return entries
		}},
		{"malformed XML", func(entries []fixtureEntry) []fixtureEntry {
			entries[5].body = strings.TrimSuffix(entries[5].body, `</ade:template>`)
			return entries
		}},
		{"base URI", func(entries []fixtureEntry) []fixtureEntry {
			entries[5].body = strings.Replace(entries[5].body, `<ade:template `, `<ade:template xml:base="other/" `, 1)
			return entries
		}},
		{"document base URI", func(entries []fixtureEntry) []fixtureEntry {
			entries[3].body = strings.Replace(entries[3].body, `<body>`, `<body xml:base="other/">`, 1)
			return entries
		}},
		{"unknown structure", func(entries []fixtureEntry) []fixtureEntry {
			entries[5].body = strings.Replace(entries[5].body, `</ade:template>`, `<ade:unknown/></ade:template>`, 1)
			return entries
		}},
		{"external stylesheet", func(entries []fixtureEntry) []fixtureEntry {
			entries[3].body = strings.Replace(entries[3].body, `href="page-template.xpgt"`, `href="https://example.invalid/page-template.xpgt"`, 1)
			return entries
		}},
		{"uninspectable import", func(entries []fixtureEntry) []fixtureEntry {
			entries[6].body = `@import "missing.css";.calibre_pb_0{page-break-after:always}`
			return entries
		}},
		{"local CSS import remains unsupported", func(entries []fixtureEntry) []fixtureEntry {
			entries[6].body = `@import "other.css";.calibre_pb_0{page-break-after:always}`
			entries[2].body = strings.Replace(entries[2].body, `</manifest>`, `<item id="other-css" href="other.css" media-type="text/css"/></manifest>`, 1)
			return append(entries, fixtureEntry{"OPS/other.css", `.other{color:#444}`, zip.Deflate})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := writeFixture(t, tc.edit(stylesheetInspectionFixture(inspectionPageTemplate, 2)))
			plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
			if err != nil {
				t.Fatal(err)
			}
			requireDuplicateAssessment(t, plan.Report, "calibre_pb_0", false)
			if countActions(plan, "duplicate_ids") != 0 {
				t.Fatalf("unknown stylesheet dependency was guessed safe: %+v", plan.Actions)
			}
		})
	}
}
