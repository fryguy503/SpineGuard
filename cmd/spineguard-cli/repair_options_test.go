package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

func TestRepairPreviewAndOptInCleanupCLI(t *testing.T) {
	dir := t.TempDir()
	book := cliBook(t, dir, "scripted.epub", "application/epub+zip", `<script type="text/javascript">window.example=1;</script><p onclick="example()">Readable text.</p>`)
	original, err := os.ReadFile(book)
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := captureCommand(t, "repair", "-dry-run", book)
	if code != 0 {
		t.Fatalf("preview: %d %s", code, stderr)
	}
	var safe epub.RepairPlan
	if err := json.Unmarshal([]byte(stdout), &safe); err != nil {
		t.Fatal(err)
	}
	if safe.SourceSHA256 == "" || safe.Options.RemoveScripts {
		t.Fatalf("unsafe defaults: %+v", safe)
	}
	for _, action := range safe.Actions {
		if action.RequiresOptIn {
			t.Fatalf("default preview selected optional action: %+v", action)
		}
	}
	code, stdout, stderr = captureCommand(t, "repair", "-dry-run", "-remove-scripts", book)
	if code != 0 {
		t.Fatalf("cleanup preview: %d %s", code, stderr)
	}
	var cleanup epub.RepairPlan
	if err := json.Unmarshal([]byte(stdout), &cleanup); err != nil {
		t.Fatal(err)
	}
	optional := false
	for _, action := range cleanup.Actions {
		optional = optional || action.RequiresOptIn
	}
	if !cleanup.Options.RemoveScripts || !optional {
		t.Fatalf("cleanup actions missing: %+v", cleanup)
	}
	output := filepath.Join(dir, "reader-copy.epub")
	code, stdout, stderr = captureCommand(t, "repair", "-remove-scripts", "-expect-sha256", cleanup.SourceSHA256, "-out", output, book)
	if code != 0 {
		t.Fatalf("cleanup: %d %s", code, stderr)
	}
	var repaired epub.RepairResult
	if err := json.Unmarshal([]byte(stdout), &repaired); err != nil {
		t.Fatal(err)
	}
	for _, issue := range repaired.Report.Issues {
		if issue.Code == "CONTENT_SCRIPT" {
			t.Fatalf("script survived cleanup: %+v", issue)
		}
	}
	after, err := os.ReadFile(book)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatal("CLI changed the original book")
	}
}
