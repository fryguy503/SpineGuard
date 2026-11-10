package epub

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repairDocumentFixture(entries []fixtureEntry, name, body string) []fixtureEntry {
	entries[2].body = strings.Replace(entries[2].body, `</manifest>`, `<item id="extra`+string(rune('a'+len(entries)))+`" href="`+strings.TrimPrefix(name, "OPS/")+`" media-type="application/xhtml+xml"/></manifest>`, 1)
	return append(entries, fixtureEntry{name, body, zip.Deflate})
}
func archiveEntryBytes(t *testing.T, filename, name string, raw bool) []byte {
	t.Helper()
	z, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name == name {
			var reader io.Reader
			var closer io.Closer
			if raw {
				reader, err = f.OpenRaw()
			} else {
				var r io.ReadCloser
				r, err = f.Open()
				reader, closer = r, r
			}
			if err != nil {
				t.Fatal(err)
			}
			if closer != nil {
				defer closer.Close()
			}
			b, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
	t.Fatalf("missing ZIP entry %s", name)
	return nil
}
func countActions(plan RepairPlan, kind string) int {
	n := 0
	for _, a := range plan.Actions {
		if a.Kind == kind {
			n += a.Count
		}
	}
	return n
}

func TestRepairPlanUniqueLinkAndUnresolvedDestination(t *testing.T) {
	entries := fixtureEntries()
	entries[4].body = strings.Replace(entries[4].body, `chapter.xhtml#start`, `Text/#start`, 1)
	entries[3].body = strings.Replace(entries[3].body, `</body>`, `<a href="missing.xhtml#not-in-book">Keep this broken link visible</a></body>`, 1)
	input := writeFixture(t, entries)
	before, _ := sourceDigest(context.Background(), input)
	plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceSHA256 != before || plan.Options.ExpectedSHA256 != before || plan.Report.Errors != 2 || countActions(plan, "links") != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.Unresolved) != 1 || plan.Unresolved[0].Code != "LINK_MISSING" {
		t.Fatalf("preview did not retain unresolved finding: %+v", plan.Unresolved)
	}
	eligible := 0
	for _, i := range plan.Report.Issues {
		if i.Code == "LINK_MISSING" && i.Repairable {
			eligible++
			if i.RepairKind != "links" || i.RepairReason == "" {
				t.Fatalf("missing repair rationale: %+v", i)
			}
		}
	}
	if eligible != 1 {
		t.Fatalf("repair eligibility mismatch: %+v", plan.Report.Issues)
	}
	output := filepath.Join(t.TempDir(), "fixed.epub")
	fixed, err := RepairWithOptions(context.Background(), input, output, Options{}, plan.Options)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Report.Errors != 1 || fixed.Report.Stats.Words != plan.Report.Stats.Words {
		t.Fatalf("remaining finding or word statistics wrong: %+v", fixed.Report)
	}
	want := strings.Replace(entries[4].body, `Text/#start`, `chapter.xhtml#start`, 1)
	if string(archiveEntryBytes(t, output, "OPS/nav.xhtml", false)) != want {
		t.Fatal("link was not minimally repaired")
	}
	if !bytes.Equal(archiveEntryBytes(t, input, "OPS/chapter.xhtml", true), archiveEntryBytes(t, output, "OPS/chapter.xhtml", true)) {
		t.Fatal("unchanged compressed payload was rewritten")
	}
	after, _ := sourceDigest(context.Background(), input)
	if before != after {
		t.Fatal("original EPUB changed")
	}
}

func TestLinkRepairAmbiguousAnchorsAndExistingDestinations(t *testing.T) {
	for _, sameDocument := range []bool{false, true} {
		entries := fixtureEntries()
		entries[4].body = strings.Replace(entries[4].body, `chapter.xhtml#start`, `missing.xhtml#start`, 1)
		if sameDocument {
			entries[3].body = strings.Replace(entries[3].body, `</body>`, `<p id="start">Second target</p></body>`, 1)
		} else {
			entries = repairDocumentFixture(entries, "OPS/extra.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Extra</title></head><body><p id="start">Second target</p></body></html>`)
		}
		plan, err := PlanRepair(context.Background(), writeFixture(t, entries), Options{}, DefaultRepairOptions())
		if err != nil {
			t.Fatal(err)
		}
		if countActions(plan, "links") != 0 {
			t.Fatal("ambiguous destination was guessed")
		}
		for _, i := range plan.Report.Issues {
			if i.Code == "LINK_MISSING" && (i.Repairable || !strings.Contains(i.RepairReason, "multiple")) {
				t.Fatalf("ambiguous repair should explain refusal: %+v", i)
			}
		}
	}
	entries := fixtureEntries()
	entries = repairDocumentFixture(entries, "OPS/extra.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Extra</title></head><body><p id="start">Another valid local ID</p></body></html>`)
	plan, err := PlanRepair(context.Background(), writeFixture(t, entries), Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("valid links should be preserved: %+v", plan.Actions)
	}
}

func TestLinkRepairFilenameCaseQueriesAndEscapedNames(t *testing.T) {
	entries := fixtureEntries()
	entries[4].body = strings.Replace(entries[4].body, `chapter.xhtml#start`, `CHAPTER.XHTML?mode=read&amp;x=a%26b#start`, 1)
	input := writeFixture(t, entries)
	output := filepath.Join(t.TempDir(), "case.epub")
	if _, err := Repair(context.Background(), input, output, Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(archiveEntryBytes(t, output, "OPS/nav.xhtml", false)), `chapter.xhtml?mode=read&amp;x=a%26b#start`) {
		t.Fatal("case correction changed query or XML escaping")
	}
	entries = fixtureEntries()
	entries[3].name = "OPS/Part one é.xhtml"
	entries[3].body = strings.Replace(entries[3].body, `id="start"`, `id="ch-β"`, 1)
	entries[2].body = strings.Replace(entries[2].body, `href="chapter.xhtml"`, `href="Part%20one%20%C3%A9.xhtml"`, 1)
	entries[4].body = strings.Replace(entries[4].body, `chapter.xhtml#start`, `Missing/?x=a%26b#ch-%CE%B2`, 1)
	input = writeFixture(t, entries)
	output = filepath.Join(t.TempDir(), "escaped.epub")
	fixed, err := Repair(context.Background(), input, output, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Report.Errors != 0 || fixed.Report.Warnings != 0 {
		t.Fatalf("escaped destination failed: %+v", fixed.Report.Issues)
	}
	if !strings.Contains(string(archiveEntryBytes(t, output, "OPS/nav.xhtml", false)), `Part%20one%20%C3%A9.xhtml?x=a%26b#ch-%CE%B2`) {
		t.Fatal("encoded filename/query/fragment were not preserved")
	}
}

func TestRepairPlanSelectionAndSourceRevision(t *testing.T) {
	entries := fixtureEntries()
	entries[4].body = strings.Replace(entries[4].body, `chapter.xhtml#start`, `Missing/#start`, 1)
	input := writeFixture(t, entries)
	choices := DefaultRepairOptions()
	choices.FixLinks = false
	plan, err := PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Actions == nil || len(plan.Actions) != 0 || len(plan.Unresolved) != 1 {
		t.Fatalf("unselected repair was applied: %+v", plan)
	}
	plan, err = PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(input, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("source changed"))
	f.Close()
	output := filepath.Join(t.TempDir(), "stale.epub")
	if _, err := RepairWithOptions(context.Background(), input, output, Options{}, plan.Options); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale preview accepted: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("stale preview published an output")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PlanRepair(ctx, input, Options{}, DefaultRepairOptions()); err == nil {
		t.Fatal("canceled preview continued")
	}
}

func TestRepairPlanReaderCleanupIsOptIn(t *testing.T) {
	entries := fixtureEntries()
	entries[3].body = strings.Replace(entries[3].body, `</head>`, `<script src="insertSpan.js"/></head>`, 1)
	input := writeFixture(t, entries)
	normal, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if countActions(normal, "reader_cleanup") != 0 || len(normal.Actions) != 0 {
		t.Fatalf("script removal was enabled by default: %+v", normal.Actions)
	}
	found := false
	for _, i := range normal.Report.Issues {
		if i.Code == "CONTENT_SCRIPT" {
			found = i.Repairable && i.OptionalRepair && i.RepairKind == "reader_cleanup"
		}
	}
	if !found {
		t.Fatalf("optional cleanup eligibility missing: %+v", normal.Report.Issues)
	}
	choices := DefaultRepairOptions()
	choices.RemoveScripts = true
	cleanup, err := PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if countActions(cleanup, "reader_cleanup") == 0 || len(cleanup.Unresolved) != 0 {
		t.Fatalf("cleanup preview wrong: %+v", cleanup)
	}
	output := filepath.Join(t.TempDir(), "reader.epub")
	fixed, err := RepairWithOptions(context.Background(), input, output, Options{}, cleanup.Options)
	if err != nil {
		t.Fatal(err)
	}
	if hasIssue(fixed.Report, "CONTENT_SCRIPT") || fixed.Report.Stats.Words != normal.Report.Stats.Words {
		t.Fatalf("cleanup altered readable text or retained script: %+v", fixed.Report)
	}
}

func TestRepairDoesNotInventAnUnprovenLinkTarget(t *testing.T) {
	entries := fixtureEntries()
	entries[4].body = strings.Replace(entries[4].body, `chapter.xhtml#start`, `Missing/#ghost`, 1)
	entries = repairDocumentFixture(entries, "OPS/extra.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:x="urn:unrelated"><head><title>Extra</title></head><body><p x:id="ghost">Not a fragment target</p></body></html>`)
	plan, err := PlanRepair(context.Background(), writeFixture(t, entries), Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "links") != 0 {
		t.Fatal("arbitrary namespaced attribute was used as an anchor")
	}
	entries = fixtureEntries()
	entries[4].body = strings.Replace(entries[4].body, `chapter.xhtml#start`, `Missing/#start`, 1)
	entries = repairDocumentFixture(entries, "OPS/legacy", `<!DOCTYPE html [<!ENTITY custom "value">]><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Unknown</title></head><body>&custom;</body></html>`)
	plan, err = PlanRepair(context.Background(), writeFixture(t, entries), Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "links") != 0 {
		t.Fatal("a unique anchor was claimed with unindexed content")
	}
}

// A hand-built valid ZIP permits deflated directory headers, which Go's writer
// normally normalizes itself. Some publisher tools emit this representation.
func writeDeflatedDirectoryFixture(t *testing.T) string {
	t.Helper()
	entries := append(fixtureEntries(), fixtureEntry{"OPS/", "", zip.Deflate})
	var body, central bytes.Buffer
	put := func(w io.Writer, values ...any) {
		for _, v := range values {
			if err := binary.Write(w, binary.LittleEndian, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, entry := range entries {
		raw := []byte(entry.body)
		compressed := raw
		if entry.method == zip.Deflate {
			var c bytes.Buffer
			z, _ := flate.NewWriter(&c, flate.DefaultCompression)
			z.Write(raw)
			z.Close()
			compressed = c.Bytes()
		}
		offset := uint32(body.Len())
		crc := crc32.ChecksumIEEE(raw)
		name := []byte(entry.name)
		put(&body, uint32(0x04034b50), uint16(20), uint16(0), entry.method, uint16(0), uint16(0), crc, uint32(len(compressed)), uint32(len(raw)), uint16(len(name)), uint16(0))
		body.Write(name)
		body.Write(compressed)
		put(&central, uint32(0x02014b50), uint16(20), uint16(20), uint16(0), entry.method, uint16(0), uint16(0), crc, uint32(len(compressed)), uint32(len(raw)), uint16(len(name)), uint16(0), uint16(0), uint16(0), uint16(0), uint32(0), offset)
		central.Write(name)
	}
	offset := uint32(body.Len())
	body.Write(central.Bytes())
	put(&body, uint32(0x06054b50), uint16(0), uint16(0), uint16(len(entries)), uint16(len(entries)), uint32(central.Len()), offset, uint16(0))
	filename := filepath.Join(t.TempDir(), "deflated-directory.epub")
	if err := os.WriteFile(filename, body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestRepairPreservesDeflatedEmptyDirectory(t *testing.T) {
	input := writeDeflatedDirectoryFixture(t)
	before, _ := sourceDigest(context.Background(), input)
	output := filepath.Join(t.TempDir(), "fixed.epub")
	fixed, err := Repair(context.Background(), input, output, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Report.Errors != 0 {
		t.Fatalf("directory repackaging failed: %+v", fixed.Report.Issues)
	}
	if len(archiveEntryBytes(t, output, "OPS/", false)) != 0 {
		t.Fatal("directory gained contents")
	}
	after, _ := sourceDigest(context.Background(), input)
	if before != after {
		t.Fatal("original directory EPUB changed")
	}
	plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("clean book offered routine repackaging as a repair: %+v", plan.Actions)
	}
}

func TestRepairPlanPreservesObfuscatedPackageMetadata(t *testing.T) {
	entries := fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, "Test Book", "Test & Book", 1)
	entries = append(entries, fixtureEntry{"META-INF/encryption.xml", `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><EncryptedData xmlns="http://www.w3.org/2001/04/xmlenc#"><EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/></EncryptedData></encryption>`, zip.Deflate})
	input := writeFixture(t, entries)
	plan, err := PlanRepair(context.Background(), input, Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 {
		t.Fatalf("obfuscated package was offered an edit: %+v", plan.Actions)
	}
	found := false
	for _, issue := range plan.Report.Issues {
		if issue.Code == "XML_INVALID" && issue.File == "OPS/book.opf" {
			found = true
			if issue.Repairable || !strings.Contains(issue.RepairReason, "font key") {
				t.Fatalf("incorrect metadata eligibility: %+v", issue)
			}
		}
	}
	if !found {
		t.Fatal("fixture did not exercise malformed package metadata")
	}
	output := filepath.Join(t.TempDir(), "repacked.epub")
	if _, err := Repair(context.Background(), input, output, Options{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archiveEntryBytes(t, input, "OPS/book.opf", true), archiveEntryBytes(t, output, "OPS/book.opf", true)) {
		t.Fatal("obfuscated package payload changed")
	}
}
