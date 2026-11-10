package epub

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

const xhtml11DTD = `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.1//EN" "http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd">`

func TestKnownXHTMLDTDUsesOnlyStandardEntities(t *testing.T) {
	good := xhtml11DTD + `<html xmlns="http://www.w3.org/1999/xhtml" title="&mdash;"><head><title>Test</title></head><body><p>One&mdash;two&nbsp;three.</p></body></html>`
	if _, err := validateXML([]byte(good)); err != nil {
		t.Fatalf("standard XHTML entity rejected: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(good, "&mdash;", "&CounterClockwiseContourIntegral;", 1),
		strings.Replace(good, "http://www.w3.org/TR/xhtml11/DTD/xhtml11.dtd", "https://example.invalid/custom.dtd", 1),
		strings.Replace(good, `<html xmlns="http://www.w3.org/1999/xhtml"`, `<html xmlns="urn:unrelated"`, 1),
		strings.Replace(good, ">", ` [<!ENTITY mdash "different">]>`, 1),
		xhtml11DTD + `<!DOCTYPE html SYSTEM "custom.dtd">` + strings.TrimPrefix(good, xhtml11DTD),
	} {
		if _, err := validateXML([]byte(bad)); err == nil {
			t.Fatalf("unrecognized entity meaning accepted: %s", bad)
		}
	}
}

func TestStandardEntityNormalizationPreservesLiteralContexts(t *testing.T) {
	source := []byte("\xef\xbb\xbf" + xhtml11DTD + `<?note value="&mdash;"?><html xmlns="http://www.w3.org/1999/xhtml" title="&mdash;"><head><title>Test</title></head><body><!-- &nbsp; --><p>One&mdash;two&nbsp;three &amp; four.</p><![CDATA[&mdash; &nbsp;]]></body></html>`)
	modified, count, err := normalizeStandardEntities(context.Background(), source)
	if err != nil || count != 3 {
		t.Fatalf("normalization: %d %v", count, err)
	}
	for _, kept := range []string{"\xef\xbb\xbf", xhtml11DTD, `<?note value="&mdash;"?>`, `<!-- &nbsp; -->`, `<![CDATA[&mdash; &nbsp;]]>`, `&amp;`} {
		if !bytes.Contains(modified, []byte(kept)) {
			t.Fatalf("literal context changed: %q", kept)
		}
	}
	if !bytes.Contains(modified, []byte(`title="&#x2014;"`)) || !bytes.Contains(modified, []byte(`One&#x2014;two&#xA0;three`)) {
		t.Fatalf("named entities not normalized exactly: %s", modified)
	}
	if _, err := validateXML(modified); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{`<!DOCTYPE html [<!ENTITY custom "different">]><html>&custom;</html>`, `<!DOCTYPE html SYSTEM "custom.dtd"><html>&mdash;</html>`} {
		b, count, err := normalizeStandardEntities(context.Background(), []byte(unsafe))
		if err != nil || count != 0 || string(b) != unsafe {
			t.Fatalf("custom DTD source changed: %s", unsafe)
		}
	}
}

func TestKnownDTDScanAndDefaultRepairPreserveValidBook(t *testing.T) {
	entries := fixtureEntries()
	entries[3].body = xhtml11DTD + strings.TrimPrefix(entries[3].body, `<?xml version="1.0"?>`)
	entries[3].body = strings.Replace(entries[3].body, "One two three.", "One&mdash;two&nbsp;three.", 1)
	input := writeFixture(t, entries)
	report, err := Scan(context.Background(), input, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Errors != 0 || report.Warnings != 0 || report.Stats.Words != 5 {
		t.Fatalf("valid XHTML DTD overstated as damage: %+v", report)
	}
	plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("valid named characters were edited by default: %+v", plan.Actions)
	}
	choices := DefaultRepairOptions()
	choices.NormalizeEntities = true
	plan, err = PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "entities") != 2 {
		t.Fatalf("expected 2 named-reference edits: %+v", plan.Actions)
	}
	output := filepath.Join(filepath.Dir(input), "normalized.epub")
	choices.ExpectedSHA256 = plan.SourceSHA256
	fixed, err := RepairWithOptions(context.Background(), input, output, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Report.Stats.Words != report.Stats.Words || fixed.Report.Errors != 0 || fixed.Report.Warnings != 0 {
		t.Fatalf("numeric normalization changed meaning or validity: %+v", fixed.Report)
	}
	if !strings.Contains(string(archiveEntryBytes(t, input, "OPS/chapter.xhtml", false)), "&mdash;") {
		t.Fatal("original named reference was modified")
	}
}
