package library

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

func TestRepairPlanJSONKeepsCompleteReport(t *testing.T) {
	plan := RepairPlan{RepairPlan: epub.RepairPlan{Path: "book.epub", SourceSHA256: "abc", Actions: []epub.RepairAction{}, Report: epub.Report{Title: "native"}}, Report: Report{Report: epub.Report{Title: "formal", Issues: []epub.Issue{}}, EPUBCheck: &Validation{Status: "passed"}}}
	plan.Suggestions = epub.RepairSuggestions{Language: "en", LanguageSources: []string{"titlepage.xhtml"}}
	plan.Options = epub.DefaultRepairOptions()
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Path        string                 `json:"path"`
		Source      string                 `json:"sourceSha256"`
		Suggestions epub.RepairSuggestions `json:"suggestions"`
		Options     epub.RepairOptions     `json:"options"`
		Report      struct {
			Title     string      `json:"title"`
			EPUBCheck *Validation `json:"epubcheck"`
		} `json:"report"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "book.epub" || got.Source != "abc" || got.Report.Title != "formal" || got.Report.EPUBCheck == nil {
		t.Fatalf("native bridge lost review fields: %s", b)
	}
	if got.Suggestions.Language != "en" || len(got.Suggestions.LanguageSources) != 1 || !got.Options.FixMetadata || !got.Options.FixDocumentMarkup || got.Options.Language != "" || got.Options.GenerateIdentifier || got.Options.NormalizeEntities {
		t.Fatalf("metadata review defaults or suggestions were lost: %s", b)
	}
}

func TestPreviewFailureReleasesOperationAndPreservesHistory(t *testing.T) {
	s := New(t.TempDir())
	s.store(Report{Report: epub.Report{Path: "previous.epub"}})
	missing := filepath.Join(t.TempDir(), "missing.epub")
	_, err := s.PreviewRepair(context.Background(), missing, epub.DefaultRepairOptions())
	if err == nil {
		t.Fatal("missing book preview succeeded")
	}
	if len(s.State("test").Reports) != 1 {
		t.Fatal("preview failure dropped existing reports")
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("failed preview left service locked: %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("preview created an EPUB")
	}
}
