package epub

import (
	"archive/zip"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func identityNCXFixture(uid string) []fixtureEntry {
	entries := fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, `version="3.0"`, `version="2.0"`, 1)
	entries[2].body = strings.Replace(entries[2].body, `<spine>`, `<spine toc="ncx">`, 1)
	entries[2].body = strings.Replace(entries[2].body, `</manifest>`, `<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest>`, 1)
	head := `<head/>`
	if uid != "" {
		head = `<head><meta name="dtb:uid" content="` + uid + `"/></head>`
	}
	return append(entries, fixtureEntry{"OPS/toc.ncx", `<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">` + head + `<docTitle><text>Test</text></docTitle><navMap><navPoint id="n1" playOrder="1"><navLabel><text>Chapter</text></navLabel><content src="chapter.xhtml#start"/></navPoint></navMap></ncx>`, zip.Deflate})
}

func TestNCXIdentityCheckAndRepairPreserveNavigation(t *testing.T) {
	for _, uid := range []string{"different", ""} {
		t.Run(uid, func(t *testing.T) {
			entries := identityNCXFixture(uid)
			input := writeFixture(t, entries)
			report, err := Scan(context.Background(), input, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if !hasIssue(report, "NCX_IDENTIFIER_MISMATCH") {
				t.Fatal("NCX identity mismatch not checked")
			}
			plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
			if err != nil {
				t.Fatal(err)
			}
			if countActions(plan, "metadata") != 1 {
				t.Fatalf("NCX metadata repair missing: %+v", plan.Actions)
			}
			output := filepath.Join(filepath.Dir(input), "ncx.epub")
			fixed, err := Repair(context.Background(), input, output, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if hasIssue(fixed.Report, "NCX_IDENTIFIER_MISMATCH") {
				t.Fatal("NCX identity still differs")
			}
			original := string(archiveEntryBytes(t, input, "OPS/toc.ncx", false))
			after := string(archiveEntryBytes(t, output, "OPS/toc.ncx", false))
			start := strings.Index(original, "<docTitle>")
			if !strings.HasSuffix(after, original[start:]) {
				t.Fatal("NCX identity repair changed navigation content, order or destinations")
			}
			if !strings.Contains(after, `name="dtb:uid" content="urn:uuid:test"`) {
				t.Fatalf("active identifier not copied: %s", after)
			}
			if fixed.Report.Identifier != "urn:uuid:test" || fixed.Report.Stats.Words != report.Stats.Words {
				t.Fatal("NCX repair altered publication identity or book text")
			}
		})
	}
}

func TestRestoredAndGeneratedPublicationIdentitySynchronizesNCX(t *testing.T) {
	for _, generate := range []bool{false, true} {
		t.Run(map[bool]string{false: "restored", true: "generated"}[generate], func(t *testing.T) {
			entries := identityNCXFixture("old-ncx-value")
			oldIdentifier := `<dc:identifier id="uid">urn:uuid:test</dc:identifier>`
			replacement := `<dc:identifier>urn:uuid:test</dc:identifier>`
			if generate {
				replacement = ""
			}
			entries[2].body = strings.Replace(entries[2].body, oldIdentifier, replacement, 1)
			input := writeFixture(t, entries)
			choices := DefaultRepairOptions()
			choices.GenerateIdentifier = generate
			plan, err := PlanRepair(context.Background(), input, Options{}, choices)
			if err != nil {
				t.Fatal(err)
			}
			if countActions(plan, "metadata") != 2 {
				t.Fatalf("publication and NCX were not planned together: %+v", plan.Actions)
			}
			choices.ExpectedSHA256 = plan.SourceSHA256
			output := filepath.Join(filepath.Dir(input), "identity.epub")
			fixed, err := RepairWithOptions(context.Background(), input, output, Options{}, choices)
			if err != nil {
				t.Fatal(err)
			}
			if hasIssue(fixed.Report, "METADATA_IDENTIFIER") || hasIssue(fixed.Report, "NCX_IDENTIFIER_MISMATCH") {
				t.Fatalf("identity consistency failure: %+v", fixed.Report.Issues)
			}
			if !strings.Contains(string(archiveEntryBytes(t, output, "OPS/toc.ncx", false)), `content="`+fixed.Report.Identifier+`"`) {
				t.Fatal("NCX did not receive reviewed publication identifier")
			}
			if !generate && fixed.Report.Identifier != "urn:uuid:test" {
				t.Fatal("restoration replaced existing publication value")
			}
		})
	}
}

func TestNCXIdentityRefusesAmbiguousPackagesAndDuplicateUIDEntries(t *testing.T) {
	entries := identityNCXFixture("different")
	entries = append(entries, fixtureEntry{"OPS/second.opf", strings.Replace(entries[2].body, "urn:uuid:test", "urn:uuid:other", 1), zip.Deflate})
	entries[1].body = strings.Replace(entries[1].body, `</rootfiles>`, `<rootfile full-path="OPS/second.opf" media-type="application/oebps-package+xml"/></rootfiles>`, 1)
	plan, err := PlanRepair(context.Background(), writeFixture(t, entries), Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "metadata") != 0 {
		t.Fatal("conflicting package identities guessed for shared NCX")
	}
	entries = identityNCXFixture("different")
	entries[len(entries)-1].body = strings.Replace(entries[len(entries)-1].body, `</head>`, `<meta name="dtb:uid" content="another"/></head>`, 1)
	plan, err = PlanRepair(context.Background(), writeFixture(t, entries), Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "metadata") != 0 {
		t.Fatal("duplicate NCX UID entries guessed")
	}
	choices := DefaultRepairOptions()
	choices.FixMetadata = false
	plan, err = PlanRepair(context.Background(), writeFixture(t, identityNCXFixture("different")), Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "metadata") != 0 {
		t.Fatal("disabled metadata repair still edited NCX")
	}
}
