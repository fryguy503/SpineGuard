package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

func replaceCLIMetadata(t *testing.T, book, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(book)
	if err != nil {
		t.Fatal(err)
	}
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == "OPS/package.opf" {
			if !strings.Contains(string(body), old) {
				t.Fatalf("fixture metadata not found: %s", old)
			}
			body = []byte(strings.Replace(string(body), old, replacement, 1))
		}
		h := &zip.FileHeader{Name: f.Name, Method: f.Method}
		entry, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(book, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitLanguageCLIAndInactiveOptions(t *testing.T) {
	dir := t.TempDir()
	book := cliBook(t, dir, "missing-language.epub", "application/epub+zip", "")
	replaceCLIMetadata(t, book, `<dc:language>en</dc:language>`, "")
	original, _ := os.ReadFile(book)
	code, _, stderr := captureCommand(t, "repair", "-dry-run", "-language", "???", book)
	if code != 2 || !strings.Contains(strings.ToLower(stderr), "language") {
		t.Fatalf("invalid language: %d %s", code, stderr)
	}
	code, _, stderr = captureCommand(t, "repair", "-dry-run", "-fix-metadata=false", "-language", "???", book)
	if code != 0 {
		t.Fatalf("inactive metadata value was applied: %d %s", code, stderr)
	}
	out := filepath.Join(dir, "language-fixed.epub")
	code, stdout, stderr := captureCommand(t, "repair", "-language", "en", "-out", out, book)
	if code != 0 {
		t.Fatalf("language repair: %d %s", code, stderr)
	}
	var result epub.RepairResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.Report.Language != "en" || result.Report.Errors != 0 {
		t.Fatalf("language not restored: %+v", result.Report)
	}
	after, _ := os.ReadFile(book)
	if !bytes.Equal(original, after) {
		t.Fatal("language repair changed source")
	}
}

func TestMetadataReferenceAndExplicitIdentifierGenerationCLI(t *testing.T) {
	dir := t.TempDir()
	broken := cliBook(t, dir, "broken-pointer.epub", "application/epub+zip", "")
	replaceCLIMetadata(t, broken, `<dc:identifier id="uid">`, `<dc:identifier>`)
	code, stdout, stderr := captureCommand(t, "repair", "-out", filepath.Join(dir, "restored.epub"), broken)
	if code != 0 {
		t.Fatalf("pointer repair: %d %s", code, stderr)
	}
	var fixed epub.RepairResult
	if err := json.Unmarshal([]byte(stdout), &fixed); err != nil {
		t.Fatal(err)
	}
	if fixed.Report.Identifier != "urn:test:cli" {
		t.Fatalf("existing identifier value changed: %q", fixed.Report.Identifier)
	}
	missing := cliBook(t, dir, "no-identifier.epub", "application/epub+zip", "")
	replaceCLIMetadata(t, missing, `<dc:identifier id="uid">urn:test:cli</dc:identifier>`, "")
	code, stdout, stderr = captureCommand(t, "repair", "-dry-run", "-generate-identifier", missing)
	if code != 0 {
		t.Fatalf("identifier preview: %d %s", code, stderr)
	}
	var plan epub.RepairPlan
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatal(err)
	}
	optional := false
	for _, action := range plan.Actions {
		if action.Kind == "metadata" && action.RequiresOptIn {
			optional = true
		}
	}
	if !optional || plan.SourceSHA256 == "" {
		t.Fatalf("identifier generation was not explicitly reviewed: %+v", plan)
	}
	code, stdout, stderr = captureCommand(t, "repair", "-generate-identifier", "-expect-sha256", plan.SourceSHA256, "-out", filepath.Join(dir, "new-id.epub"), missing)
	if code != 0 {
		t.Fatalf("identifier save: %d %s", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &fixed); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fixed.Report.Identifier, "urn:uuid:") {
		t.Fatalf("identifier missing: %q", fixed.Report.Identifier)
	}
	matched := false
	for _, action := range plan.Actions {
		matched = matched || strings.Contains(action.Description, fixed.Report.Identifier)
	}
	if !matched {
		t.Fatal("saved identifier did not match reviewed value")
	}
}
