package epub

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentMarkupChecksAndUniqueCaseRepairs(t *testing.T) {
	entries := fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, `</package>`, `<guide/></package>`, 1)
	entries[3].body = strings.Replace(entries[3].body, `</body>`, `<svg xmlns="http://www.w3.org/2000/svg" preserveAspectRatio="xMidyMid Meet"><image href="data:image/png;base64,AA=="/></svg></body>`, 1)
	input := writeFixture(t, entries)
	report, err := Scan(context.Background(), input, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(report, "OPF_GUIDE_EMPTY") || !hasIssue(report, "SVG_PRESERVE_ASPECT_RATIO") {
		t.Fatalf("markup errors not identified: %+v", report.Issues)
	}
	choices := DefaultRepairOptions()
	plan, err := PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "document_markup") != 2 {
		t.Fatalf("deterministic markup fixes missing: %+v", plan.Actions)
	}
	output := filepath.Join(filepath.Dir(input), "markup.epub")
	fixed, err := RepairWithOptions(context.Background(), input, output, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if hasIssue(fixed.Report, "OPF_GUIDE_EMPTY") || hasIssue(fixed.Report, "SVG_PRESERVE_ASPECT_RATIO") || fixed.Report.Stats.Words != report.Stats.Words {
		t.Fatalf("markup repair altered text or retained errors: %+v", fixed.Report)
	}
	if !strings.Contains(string(archiveEntryBytes(t, output, "OPS/chapter.xhtml", false)), `preserveAspectRatio="xMidYMid meet"`) {
		t.Fatal("SVG alignment not canonicalized")
	}
	if !strings.Contains(string(archiveEntryBytes(t, input, "OPS/book.opf", false)), `<guide/>`) {
		t.Fatal("original guide was changed")
	}
	choices.FixDocumentMarkup = false
	plan, err = PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "document_markup") != 0 {
		t.Fatal("disabled markup choice was applied")
	}
}

func TestSVGAspectRatioGrammarRefusesUnknownValues(t *testing.T) {
	for _, value := range []string{"none", "xMidYMid", "defer xMaxYMin slice", " xMinYMax\tmeet "} {
		_, valid, _ := aspectRatioValue(value)
		if !valid {
			t.Fatalf("valid alignment rejected: %q", value)
		}
	}
	for _, value := range []string{"fill", "xMiddleYMiddle Meet", "xMidYMid stretch", "defer", "xMidYMid meet extra", ""} {
		_, valid, repairable := aspectRatioValue(value)
		if valid || repairable {
			t.Fatalf("arbitrary alignment guessed: %q", value)
		}
	}
	for _, value := range []string{"XMINYMAX Slice", "NONE", "Defer xMaxyMin MEET"} {
		_, valid, repairable := aspectRatioValue(value)
		if valid || !repairable {
			t.Fatalf("unique case-only alignment not fixable: %q", value)
		}
	}
	text := []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Test</title></head><body><svg xmlns="http://www.w3.org/2000/svg" preserveAspectRatio="stretch"/><custom:svg xmlns:custom="urn:custom" preserveAspectRatio="XMidYMid Meet"/></body></html>`)
	modified, actions, notes := planMetadataDocuments(context.Background(), map[string][]byte{"chapter.xhtml": text}, DefaultRepairOptions())
	if len(modified) != 0 || len(actions) != 0 {
		t.Fatal("unknown grammar or foreign metadata was rewritten")
	}
	if len(notes) != 1 || notes[0].Repairable {
		t.Fatalf("manual alignment explanation missing: %+v", notes)
	}
}

func TestGuideRemovalPreservesNonemptyOrAnnotatedMetadata(t *testing.T) {
	for _, guide := range []string{`<guide><reference type="toc" title="Contents" href="nav.xhtml"/></guide>`, `<guide id="keep"/>`, `<guide><!-- editorial note --></guide>`, `<custom:guide xmlns:custom="urn:custom"/>`} {
		source := strings.Replace(fixtureEntries()[2].body, `</package>`, guide+`</package>`, 1)
		modified, actions, _ := planMetadataDocuments(context.Background(), map[string][]byte{"book.opf": []byte(source)}, DefaultRepairOptions())
		if len(modified) != 0 || len(actions) != 0 {
			t.Fatalf("nontrivial metadata discarded: %s", guide)
		}
	}
}

func TestCombinedExplicitMetadataInsertionsDoNotOverlap(t *testing.T) {
	entries := fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, `<dc:identifier id="uid">urn:uuid:test</dc:identifier>`, "", 1)
	entries[2].body = strings.Replace(entries[2].body, `<dc:language>en</dc:language>`, "", 1)
	input := writeFixture(t, entries)
	choices := DefaultRepairOptions()
	choices.Language = "en"
	choices.GenerateIdentifier = true
	plan, err := PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "metadata") != 2 {
		t.Fatalf("combined reviewed metadata lost: %+v", plan.Actions)
	}
	output := filepath.Join(filepath.Dir(input), "complete.epub")
	choices.ExpectedSHA256 = plan.SourceSHA256
	fixed, err := RepairWithOptions(context.Background(), input, output, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Report.Errors != 0 || fixed.Report.Language != "en" || !strings.HasPrefix(fixed.Report.Identifier, "urn:uuid:") {
		t.Fatalf("combined insertions invalid: %+v", fixed.Report)
	}
}
