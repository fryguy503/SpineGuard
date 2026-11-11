package library

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

func writeBulkBook(t *testing.T, name string, damagedPackaging, scripted bool) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	add := func(name, body string, method uint16) {
		t.Helper()
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	container := `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="EPUB/package.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`
	if damagedPackaging {
		add("META-INF/container.xml", container, zip.Deflate)
		add("mimetype", "application/epub+zip", zip.Deflate)
	} else {
		add("mimetype", "application/epub+zip", zip.Store)
		add("META-INF/container.xml", container, zip.Deflate)
	}
	scriptItem, scriptContent, scriptProperty := "", "", ""
	if scripted {
		scriptItem = `<item id="script" href="script.js" media-type="text/javascript"/>`
		scriptContent = `<script src="script.js"></script>`
		scriptProperty = ` properties="scripted"`
	}
	add("EPUB/package.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="bookid"><metadata><dc:title>Bulk fixture</dc:title><dc:creator>Test Author</dc:creator><dc:language>en</dc:language><dc:identifier id="bookid">urn:uuid:ad9f2428-6c2a-475d-921e-29f2c92f0b00</dc:identifier><meta property="dcterms:modified">2026-11-10T00:00:00Z</meta></metadata><manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"`+scriptProperty+`/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`+scriptItem+`</manifest><spine><itemref idref="chapter"/></spine></package>`, zip.Deflate)
	add("EPUB/chapter.xhtml", `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title>`+scriptContent+`</head><body><h1 id="start">Chapter</h1><p>Book words remain exactly the same.</p></body></html>`, zip.Deflate)
	add("EPUB/nav.xhtml", `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml#start">Chapter</a></li></ol></nav></body></html>`, zip.Deflate)
	if scripted {
		add("EPUB/script.js", `window.fixture = true;`, zip.Deflate)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	b := archive.Bytes()
	if err := os.WriteFile(name, b, 0600); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), b...)
}

func bulkRequest(t *testing.T, name string) BulkRepairRequest {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256(b)
	choices := epub.DefaultRepairOptions()
	choices.ExpectedSHA256 = hex.EncodeToString(sha[:])
	return BulkRepairRequest{Path: name, Options: choices}
}

func requireNoBulkTemps(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".spineguard-") {
			t.Fatalf("partial repair file remains: %s", entry.Name())
		}
	}
}

func TestBulkPreviewUsesDefaultChoicesAndFreshSourceReports(t *testing.T) {
	dir := t.TempDir()
	broken, healthy := filepath.Join(dir, "broken.epub"), filepath.Join(dir, "healthy.epub")
	beforeBroken := writeBulkBook(t, broken, true, true)
	beforeHealthy := writeBulkBook(t, healthy, false, false)
	s := New(t.TempDir())
	var events []BulkProgress
	preview, err := s.PreviewBulkRepair(context.Background(), []string{broken, healthy}, func(p BulkProgress) { events = append(events, p) })
	if err != nil || preview.Cancelled || len(preview.Items) != 2 {
		t.Fatalf("unexpected bulk preview: %+v, %v", preview, err)
	}
	if len(preview.Items[0].Plan.Actions) == 0 || len(preview.Items[1].Plan.Actions) != 0 {
		t.Fatalf("ready/healthy books were not distinguished: %+v", preview.Items)
	}
	for i, item := range preview.Items {
		if item.Error != "" || item.Plan.Report.Path != item.Path || len(item.Plan.SourceSHA256) != 64 {
			t.Fatalf("incomplete preview at %d: %+v", i, item)
		}
		choices := item.Plan.Options
		if choices.ExpectedSHA256 != item.Plan.SourceSHA256 {
			t.Fatal("review options are not bound to the source hash")
		}
		choices.ExpectedSHA256 = ""
		if choices != epub.DefaultRepairOptions() {
			t.Fatalf("bulk preview enabled optional changes: %+v", choices)
		}
	}
	if len(events) != 4 || events[0].Index != 0 || events[1].Index != 1 || events[1].PreviewItem == nil || events[1].Report == nil || events[3].Index != 2 {
		t.Fatalf("missing usable preview progress: %+v", events)
	}
	for _, event := range events {
		if event.Phase != "preview" || event.Total != 2 {
			t.Fatalf("unexpected preview event: %+v", event)
		}
	}
	for name, want := range map[string][]byte{broken: beforeBroken, healthy: beforeHealthy} {
		got, _ := os.ReadFile(name)
		if !bytes.Equal(got, want) {
			t.Fatalf("preview changed the original: %s", name)
		}
	}
	if len(s.State("test").Reports) != 2 {
		t.Fatal("fresh source reports were not stored")
	}
	data, err := json.Marshal(preview)
	if err != nil || !bytes.Contains(data, []byte(`"sourceSha256"`)) || !bytes.Contains(data, []byte(`"report":{"path"`)) {
		t.Fatalf("native JSON bridge lost complete plans: %s, %v", data, err)
	}
	requireNoBulkTemps(t, dir)
}

func TestBulkRepairPreservesOriginalsAndSkipsExistingDestinationNames(t *testing.T) {
	source := t.TempDir()
	first, second := filepath.Join(source, "one", "book.epub"), filepath.Join(source, "two", "book.epub")
	wantFirst := writeBulkBook(t, first, true, true)
	wantSecond := writeBulkBook(t, second, true, false)
	output := t.TempDir()
	existing := filepath.Join(output, "book.repaired.epub")
	writeTestFile(t, existing, "keep this existing file")
	s := New(t.TempDir())
	preview, err := s.PreviewBulkRepair(context.Background(), []string{first, second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	requests := []BulkRepairRequest{{Path: first, Options: preview.Items[0].Plan.Options}, {Path: second, Options: preview.Items[1].Plan.Options}}
	var progress []BulkProgress
	result, err := s.RepairBooks(context.Background(), output, requests, func(p BulkProgress) { progress = append(progress, p) })
	if err != nil || result.Cancelled || result.Directory != output || len(result.Items) != 2 {
		t.Fatalf("unexpected bulk result: %+v, %v", result, err)
	}
	for i, item := range result.Items {
		if item.Status != "saved" || item.Error != "" || item.Report == nil || item.Report.Path != item.OutputPath || item.Report.Stats.Words != preview.Items[i].Plan.Report.Stats.Words {
			t.Fatalf("incomplete saved result: %+v", item)
		}
		wantName := []string{"book.repaired (2).epub", "book.repaired (3).epub"}[i]
		if filepath.Base(item.OutputPath) != wantName {
			t.Fatalf("unexpected collision-safe name: %s", item.OutputPath)
		}
		for _, issue := range item.Report.Issues {
			if strings.HasPrefix(issue.Code, "MIMETYPE_") {
				t.Fatalf("saved copy was not fixed and rescanned: %+v", issue)
			}
		}
	}
	if len(progress) != 4 || progress[3].Item == nil || progress[3].Report == nil || progress[3].Index != 2 || progress[3].Phase != "repair" {
		t.Fatalf("missing result progress: %+v", progress)
	}
	for name, want := range map[string][]byte{first: wantFirst, second: wantSecond, existing: []byte("keep this existing file")} {
		got, _ := os.ReadFile(name)
		if !bytes.Equal(got, want) {
			t.Fatalf("bulk repair changed an existing file: %s", name)
		}
	}
	foundScript := false
	for _, issue := range result.Items[0].Report.Issues {
		foundScript = foundScript || issue.Code == "CONTENT_SCRIPT"
	}
	if !foundScript {
		t.Fatal("recommended bulk repair removed optional scripted content")
	}
	if len(New(s.dir).State("test").Reports) != 4 {
		t.Fatal("completed copies or original reports were not persisted")
	}
	requireNoBulkTemps(t, output)
}

func TestBulkRepairContinuesAfterStaleAndBlockedBooks(t *testing.T) {
	dir := t.TempDir()
	stale, damaged, ready := filepath.Join(dir, "stale.epub"), filepath.Join(dir, "damaged.epub"), filepath.Join(dir, "ready.epub")
	writeBulkBook(t, stale, true, false)
	writeTestFile(t, damaged, "not a ZIP archive")
	writeBulkBook(t, ready, true, false)
	requests := []BulkRepairRequest{bulkRequest(t, stale), bulkRequest(t, damaged), bulkRequest(t, ready)}
	changed := append(writeBulkBook(t, stale, true, false), []byte("changed after review")...)
	if err := os.WriteFile(stale, changed, 0600); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	s := New(t.TempDir())
	result, err := s.RepairBooks(context.Background(), output, requests, nil)
	if err != nil || len(result.Items) != 3 || result.Cancelled {
		t.Fatalf("batch stopped instead of recording per-book failures: %+v, %v", result, err)
	}
	if result.Items[0].Status != "failed" || !strings.Contains(result.Items[0].Error, "changed") || result.Items[1].Status != "failed" || result.Items[1].Error == "" || result.Items[2].Status != "saved" {
		t.Fatalf("unexpected per-book statuses: %+v", result.Items)
	}
	if got, _ := os.ReadFile(stale); !bytes.Equal(got, changed) {
		t.Fatal("the changed source was overwritten")
	}
	entries, _ := os.ReadDir(output)
	if len(entries) != 1 || entries[0].Name() != "ready.repaired.epub" {
		t.Fatalf("failed books left copies behind: %+v", entries)
	}
	requireNoBulkTemps(t, output)
}

func TestBulkRequestsRejectUnsafeOrUnreviewedChoicesBeforeAnyCopies(t *testing.T) {
	book := filepath.Join(t.TempDir(), "source.epub")
	writeBulkBook(t, book, true, false)
	valid := bulkRequest(t, book)
	tests := []struct {
		name   string
		modify func(*BulkRepairRequest)
	}{
		{"scripts", func(r *BulkRepairRequest) { r.Options.RemoveScripts = true }},
		{"language", func(r *BulkRepairRequest) { r.Options.Language = "en" }},
		{"identifier", func(r *BulkRepairRequest) { r.Options.GenerateIdentifier = true }},
		{"entities", func(r *BulkRepairRequest) { r.Options.NormalizeEntities = true }},
		{"category off", func(r *BulkRepairRequest) { r.Options.FixLinks = false }},
		{"missing review", func(r *BulkRepairRequest) { r.Options.ExpectedSHA256 = "" }},
		{"invalid hash", func(r *BulkRepairRequest) { r.Options.ExpectedSHA256 = strings.Repeat("z", 64) }},
		{"directory", func(r *BulkRepairRequest) { r.Path = t.TempDir() }},
		{"invalid path", func(r *BulkRepairRequest) { r.Path = "invalid\x00.epub" }},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests, struct {
			name   string
			modify func(*BulkRepairRequest)
		}{"alternate data stream", func(r *BulkRepairRequest) { r.Path = book + ":hidden.epub" }})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := valid
			tc.modify(&request)
			output := t.TempDir()
			s := New(t.TempDir())
			if _, err := ValidateBulkRepairRequests([]BulkRepairRequest{request}); err == nil {
				t.Fatal("native-dialog preflight accepted an unsafe request")
			}
			if _, err := s.RepairBooks(context.Background(), output, []BulkRepairRequest{valid, request}, nil); err == nil {
				t.Fatal("batch accepted an unsafe request")
			}
			if entries, _ := os.ReadDir(output); len(entries) != 0 {
				t.Fatal("validation failure created a copy before checking all requests")
			}
			if err := s.Clear(); err != nil {
				t.Fatalf("validation failure retained the busy lock: %v", err)
			}
		})
	}
	if _, err := ValidateBulkRepairRequests([]BulkRepairRequest{valid, valid}); err == nil {
		t.Fatal("duplicate source paths were accepted")
	}
	if _, err := ValidateBulkRepairRequests(nil); err == nil {
		t.Fatal("an empty batch was accepted")
	}
	if _, err := ValidateBulkRepairRequests(make([]BulkRepairRequest, MaxBooks+1)); err == nil {
		t.Fatal("an oversized batch was accepted")
	}
	alias := filepath.Join(filepath.Dir(book), "alias.epub")
	if os.Link(book, alias) == nil {
		aliasRequest := valid
		aliasRequest.Path = alias
		if _, err := ValidateBulkRepairRequests([]BulkRepairRequest{valid, aliasRequest}); err == nil {
			t.Fatal("the same source selected through a hard-link alias was accepted twice")
		}
	}
}

func TestBulkMissingOrMovedBookFailsIndividually(t *testing.T) {
	dir := t.TempDir()
	ready, missing, directory := filepath.Join(dir, "ready.epub"), filepath.Join(dir, "missing.epub"), filepath.Join(dir, "now-directory.epub")
	writeBulkBook(t, ready, true, false)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	s := New(t.TempDir())
	preview, err := s.PreviewBulkRepair(context.Background(), []string{missing, ready, directory}, nil)
	if err != nil || len(preview.Items) != 3 || preview.Items[0].Error == "" || len(preview.Items[1].Plan.Actions) == 0 || preview.Items[2].Error == "" {
		t.Fatalf("one unavailable source blocked other previews: %+v, %v", preview, err)
	}
	missingRequest := bulkRequest(t, ready)
	missingRequest.Path = missing
	directoryRequest := missingRequest
	directoryRequest.Path = directory
	output := t.TempDir()
	result, err := s.RepairBooks(context.Background(), output, []BulkRepairRequest{missingRequest, bulkRequest(t, ready), directoryRequest}, nil)
	if err != nil || len(result.Items) != 3 || result.Items[0].Status != "failed" || result.Items[1].Status != "saved" || result.Items[2].Status != "failed" {
		t.Fatalf("one unavailable source blocked other copies: %+v, %v", result, err)
	}
	if entries, _ := os.ReadDir(output); len(entries) != 1 || entries[0].Name() != "ready.repaired.epub" {
		t.Fatal("an unavailable input produced an output")
	}
	requireNoBulkTemps(t, output)
}

func TestBulkPreviewCancellationAndBusyOperation(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.epub"), filepath.Join(dir, "second.epub")
	writeBulkBook(t, first, true, false)
	writeBulkBook(t, second, true, false)
	s := New(t.TempDir())
	preview, err := s.PreviewBulkRepair(context.Background(), []string{first, second}, func(p BulkProgress) {
		if p.PreviewItem != nil {
			s.Cancel()
		}
	})
	if err != nil || !preview.Cancelled || len(preview.Items) != 1 {
		t.Fatalf("preview did not stop between books: %+v, %v", preview, err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal("preview cancellation retained the busy lock")
	}
	ctx, _, err := s.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewBulkRepair(context.Background(), []string{first}, nil); err == nil {
		t.Fatal("bulk preview overlapped another operation")
	}
	if _, err := s.RepairBooks(context.Background(), t.TempDir(), []BulkRepairRequest{bulkRequest(t, first)}, nil); err == nil {
		t.Fatal("bulk repair overlapped another operation")
	}
	s.Cancel()
	if ctx.Err() == nil {
		t.Fatal("failed overlapping operation cleared the active cancellation handle")
	}
	s.finish()
}

func TestBulkRepairCancellationKeepsCompletedCopiesAndCleansPending(t *testing.T) {
	for _, cancelAfterSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "before first write", true: "after completed copy"}[cancelAfterSave], func(t *testing.T) {
			dir, output := t.TempDir(), t.TempDir()
			first, second := filepath.Join(dir, "first.epub"), filepath.Join(dir, "second.epub")
			firstOriginal := writeBulkBook(t, first, true, false)
			secondOriginal := writeBulkBook(t, second, true, false)
			s := New(t.TempDir())
			result, err := s.RepairBooks(context.Background(), output, []BulkRepairRequest{bulkRequest(t, first), bulkRequest(t, second)}, func(p BulkProgress) {
				if !cancelAfterSave && p.Index == 0 || cancelAfterSave && p.Item != nil && p.Item.Status == "saved" {
					s.Cancel()
				}
			})
			if err != nil || !result.Cancelled || len(result.Items) != 2 || result.Items[1].Status != "cancelled" {
				t.Fatalf("incomplete cancellation outcome: %+v, %v", result, err)
			}
			wantCopies, wantStatus := 0, "cancelled"
			if cancelAfterSave {
				wantCopies, wantStatus = 1, "saved"
			}
			if result.Items[0].Status != wantStatus {
				t.Fatalf("completed copy was lost: %+v", result.Items)
			}
			entries, _ := os.ReadDir(output)
			if len(entries) != wantCopies || len(New(s.dir).State("test").Reports) != wantCopies {
				t.Fatal("cancelled repairs retained partial files or lost completed history")
			}
			for name, want := range map[string][]byte{first: firstOriginal, second: secondOriginal} {
				if got, _ := os.ReadFile(name); !bytes.Equal(got, want) {
					t.Fatal("cancellation changed a source book")
				}
			}
			requireNoBulkTemps(t, output)
			if err := s.Clear(); err != nil {
				t.Fatal("cancelled bulk repair retained the busy lock")
			}
		})
	}
}

func TestBulkRepairDoesNotCreateUnnecessaryCopiesForHealthyBooks(t *testing.T) {
	book := filepath.Join(t.TempDir(), "healthy.epub")
	writeBulkBook(t, book, false, false)
	output := t.TempDir()
	result, err := New(t.TempDir()).RepairBooks(context.Background(), output, []BulkRepairRequest{bulkRequest(t, book)}, nil)
	if err != nil || len(result.Items) != 1 || result.Items[0].Status != "failed" || !strings.Contains(result.Items[0].Error, "no recommended") {
		t.Fatalf("healthy source was treated as a ready repair: %+v, %v", result, err)
	}
	if entries, _ := os.ReadDir(output); len(entries) != 0 {
		t.Fatal("a healthy book was needlessly repackaged")
	}
}
