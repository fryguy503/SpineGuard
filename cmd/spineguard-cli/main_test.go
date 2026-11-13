package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fryguy503/SpineGuard/internal/epub"
	"github.com/fryguy503/SpineGuard/internal/library"
)

// Tests exercise the real command dispatcher and output encoders. These are not
// parallel because the CLI intentionally writes to the process's stdout/stderr.
func captureCommand(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr; outR.Close(); outW.Close(); errR.Close(); errW.Close() }()
	outC, errC := make(chan []byte, 1), make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(outR); outC <- b }()
	go func() { b, _ := io.ReadAll(errR); errC <- b }()
	os.Stdout, os.Stderr = outW, errW
	code := run(args)
	os.Stdout, os.Stderr = oldOut, oldErr
	outW.Close()
	errW.Close()
	return code, string(<-outC), string(<-errC)
}

func cliBook(t *testing.T, dir, name, mimetype, bodySuffix string) string {
	t.Helper()
	filename := filepath.Join(dir, name)
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	entries := []struct{ name, body string }{
		{"mimetype", mimetype},
		{"META-INF/container.xml", `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OPS/package.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OPS/package.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>CLI Fixture</dc:title><dc:creator>Test Author</dc:creator><dc:identifier id="uid">urn:test:cli</dc:identifier><dc:language>en</dc:language><meta property="dcterms:modified">2026-11-10T12:00:00Z</meta></metadata><manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest><spine><itemref idref="chapter"/></spine></package>`},
		{"OPS/chapter.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><p id="chapter">One two three four five.</p>` + bodySuffix + `</body></html>`},
		{"OPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml#chapter">Chapter</a></li></ol></nav></body></html>`},
	}
	for i, entry := range entries {
		method := uint16(zip.Deflate)
		if i == 0 {
			method = zip.Store
		}
		h := &zip.FileHeader{Name: entry.name, Method: method}
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(w, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestCommandUsageAndVersion(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"scan"}, {"scan", "-format", "yaml", "book.epub"}, {"scan", "-words-per-page", "0", "book.epub"}, {"scan", "-reading-wpm", "0", "book.epub"}, {"scan", "-unknown"}, {"repair"}, {"repair", "-out", "bad.txt", "book.epub"}} {
		code, _, stderr := captureCommand(t, args...)
		if code != 2 || strings.TrimSpace(stderr) == "" {
			t.Fatalf("args %q: exit %d, stderr %q", args, code, stderr)
		}
	}
	for _, args := range [][]string{{"help"}, {"--help"}, {"version"}, {"--version"}} {
		code, stdout, stderr := captureCommand(t, args...)
		if code != 0 || !strings.Contains(stdout+stderr, "SpineGuard") {
			t.Fatalf("args %q: exit %d, stdout %q stderr %q", args, code, stdout, stderr)
		}
	}
}

func TestScanFormatsHealthyAndDamagedBooks(t *testing.T) {
	dir := t.TempDir()
	healthy := cliBook(t, dir, "healthy.epub", "application/epub+zip", "")
	damaged := cliBook(t, dir, "damaged.epub", "application/epub+zip\n", "")
	corrupt := filepath.Join(dir, "corrupt.epub")
	if err := os.WriteFile(corrupt, []byte("not a ZIP archive"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"text", "json", "csv"} {
		for _, tc := range []struct {
			label, filename string
			exit            int
		}{{"healthy", healthy, 0}, {"damaged", damaged, 1}, {"corrupt", corrupt, 1}} {
			t.Run(format+"/"+tc.label, func(t *testing.T) {
				code, stdout, stderr := captureCommand(t, "scan", "-format", format, tc.filename)
				if code != tc.exit || stderr != "" {
					t.Fatalf("exit %d expected %d; stderr %q", code, tc.exit, stderr)
				}
				switch format {
				case "text":
					if !strings.Contains(stdout, filepath.Base(tc.filename)) || !strings.Contains(stdout, "estimated pages") {
						t.Fatalf("missing text fields: %q", stdout)
					}
					if tc.label == "healthy" && !strings.Contains(stdout, "5 words") {
						t.Fatalf("incorrect statistics: %q", stdout)
					}
					if tc.label == "damaged" && !strings.Contains(stdout, "MIMETYPE_CONTENT") {
						t.Fatalf("missing diagnosis: %q", stdout)
					}
					if tc.label == "corrupt" && !strings.Contains(stdout, "ZIP_INVALID") {
						t.Fatalf("missing archive diagnosis: %q", stdout)
					}
				case "json":
					var export struct {
						Application string           `json:"application"`
						Reports     []library.Report `json:"reports"`
					}
					if err := json.Unmarshal([]byte(stdout), &export); err != nil {
						t.Fatal(err)
					}
					if export.Application != "SpineGuard" || len(export.Reports) != 1 || export.Reports[0].Filename != filepath.Base(tc.filename) {
						t.Fatalf("bad JSON export: %+v", export)
					}
					if (export.Reports[0].Errors > 0) != (tc.exit == 1) {
						t.Fatalf("error count inconsistent with exit: %+v", export.Reports[0])
					}
				case "csv":
					rows, err := csv.NewReader(strings.NewReader(stdout)).ReadAll()
					if err != nil {
						t.Fatal(err)
					}
					if len(rows) != 2 || rows[0][0] != "Filename" || rows[1][0] != filepath.Base(tc.filename) || len(rows[0]) != len(rows[1]) {
						t.Fatalf("bad CSV export: %#v", rows)
					}
				}
			})
		}
	}
}

func TestScanFolderSettingsAndToolFailure(t *testing.T) {
	dir := t.TempDir()
	cliBook(t, dir, "one.epub", "application/epub+zip", "")
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	cliBook(t, sub, "two.epub", "application/epub+zip", "")
	for _, tc := range []struct {
		recursive string
		count     int
	}{{"true", 2}, {"false", 1}} {
		code, stdout, stderr := captureCommand(t, "scan", "-format", "json", "-recursive="+tc.recursive, dir)
		if code != 0 || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		var export struct {
			Reports []library.Report `json:"reports"`
		}
		if err := json.Unmarshal([]byte(stdout), &export); err != nil {
			t.Fatal(err)
		}
		if len(export.Reports) != tc.count {
			t.Fatalf("recursive=%s returned %d books, expected %d", tc.recursive, len(export.Reports), tc.count)
		}
	}
	code, _, stderr := captureCommand(t, "scan", filepath.Join(dir, "absent.epub"))
	if code != 2 || stderr == "" {
		t.Fatalf("missing input should be tool failure, exit %d stderr %q", code, stderr)
	}
	code, stdout, stderr := captureCommand(t, "scan", "-format", "json", "-epubcheck", filepath.Join(dir, "missing.jar"), filepath.Join(dir, "one.epub"))
	if code != 2 || stderr != "" || !strings.Contains(stdout, `"status": "unavailable"`) {
		t.Fatalf("unavailable checker must not pass: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestRepairSeparateCopyNoOverwriteAndUnresolvedFindings(t *testing.T) {
	dir := t.TempDir()
	input := cliBook(t, dir, "input.epub", "application/epub+zip\n", "")
	original, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "repaired.epub")
	code, stdout, stderr := captureCommand(t, "repair", "-out", output, input)
	if code != 0 || stderr != "" {
		t.Fatalf("repair exit %d stderr %q", code, stderr)
	}
	var result epub.RepairResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.OutputPath != output || result.Report.Path != output || result.Report.Errors != 0 || len(result.Changes) == 0 {
		t.Fatalf("bad repair result: %+v", result)
	}
	if after, err := os.ReadFile(input); err != nil || !bytes.Equal(original, after) {
		t.Fatal("repair modified original")
	}
	repaired, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	code, _, stderr = captureCommand(t, "repair", "-out", output, input)
	if code != 2 || !strings.Contains(stderr, "already exists") {
		t.Fatalf("overwrite should fail: exit %d stderr %q", code, stderr)
	}
	if after, err := os.ReadFile(output); err != nil || !bytes.Equal(repaired, after) {
		t.Fatal("existing repaired file was changed")
	}
	code, _, stderr = captureCommand(t, "repair", "-out", input, input)
	if code != 2 || stderr == "" {
		t.Fatalf("original output should fail: exit %d stderr %q", code, stderr)
	}
	unresolved := cliBook(t, dir, "unresolved.epub", "application/epub+zip\n", `<img src="missing.png"/>`)
	code, stdout, stderr = captureCommand(t, "repair", "-out", filepath.Join(dir, "unresolved.repaired.epub"), unresolved)
	if code != 1 || stderr != "" || !strings.Contains(stdout, "LINK_MISSING") {
		t.Fatalf("unresolved book errors should remain visible: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestCanceledScanAndRepair(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Repair checks cancellation before publication, including while opening ZIPs.
	dir := t.TempDir()
	input := cliBook(t, dir, "cancel.epub", "application/epub+zip", "")
	if code := repair(ctx, []string{"-out", filepath.Join(dir, "out.epub"), input}); code != 130 {
		t.Fatalf("canceled repair exit %d", code)
	}
	if code := scan(ctx, []string{input}); code != 130 {
		t.Fatalf("canceled discovery exit %d", code)
	}
}

func TestWarningsDoNotFailCLIAndUppercaseRepairOutput(t *testing.T) {
	dir := t.TempDir()
	input := cliBook(t, dir, "warning.epub", "application/epub+zip", `<script type="text/javascript">void 0;</script>`)
	code, stdout, stderr := captureCommand(t, "scan", "-format", "json", input)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "CONTENT_SCRIPT") {
		t.Fatalf("warnings should exit 0: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, _, stderr = captureCommand(t, "repair", "-out", filepath.Join(dir, "copy.EPUB"), input)
	if code != 0 || stderr != "" {
		t.Fatalf("uppercase output extension rejected: exit %d stderr %q", code, stderr)
	}
}

func TestCLIOptionalMissingSceneDividerRepair(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat(`<p class="star"><span><img src="Images/star.jpg" alt="star.jpg"/></span></p>`, 2)
	input := cliBook(t, dir, "dividers.epub", "application/epub+zip", body)
	code, stdout, stderr := captureCommand(t, "repair", "-dry-run", input)
	if code != 0 || stderr != "" {
		t.Fatalf("default preview: %d %s", code, stderr)
	}
	var plain epub.RepairPlan
	if err := json.Unmarshal([]byte(stdout), &plain); err != nil {
		t.Fatal(err)
	}
	if plain.Options.ReplaceMissingSceneDividers || len(plain.Actions) != 0 || len(plain.Unresolved) != 2 {
		t.Fatalf("default CLI silently selected divider replacement: %+v", plain)
	}
	output := filepath.Join(dir, "with-markers.epub")
	code, stdout, stderr = captureCommand(t, "repair", "-replace-missing-scene-dividers", "-out", output, input)
	if code != 0 || stderr != "" {
		t.Fatalf("opted-in CLI repair: %d %s %s", code, stdout, stderr)
	}
	var result epub.RepairResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Report.Errors != 0 || result.OutputPath != output {
		t.Fatalf("CLI repair output: %+v %v", result, err)
	}
}
