package library

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

func TestExportJSONAndCSV(t *testing.T) {
	reports := []Report{{Report: epub.Report{Path: "/books/sample.epub", Filename: "sample.epub", Title: "A title, with \"quotes\"\nand new lines", Author: "A. Writer", Language: "en", Series: "The Sequence", SeriesIndex: "1.5", EPUBVersion: "3.0", Status: "warning", Warnings: 1, FileBytes: 1234, Stats: epub.Stats{Words: 750, EstimatedPages: 3, SourcePages: 42, ReadingMinutes: 3, Chapters: 2, Images: 1}}, EPUBCheck: &Validation{Status: "passed", Version: "5.4.0"}}}
	t.Run("JSON retains full reports", func(t *testing.T) {
		var output bytes.Buffer
		if err := Export(&output, "json", reports); err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Application string    `json:"application"`
			ExportedAt  time.Time `json:"exportedAt"`
			Reports     []Report  `json:"reports"`
		}
		if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Application != "SpineGuard" || decoded.ExportedAt.IsZero() || len(decoded.Reports) != 1 || decoded.Reports[0].Title != reports[0].Title || decoded.Reports[0].Stats.Words != 750 || decoded.Reports[0].EPUBCheck.Status != "passed" || decoded.Reports[0].Series != "The Sequence" || decoded.Reports[0].SeriesIndex != "1.5" {
			t.Fatalf("export lost report data: %+v", decoded)
		}
	})
	t.Run("CSV quoting and statistics", func(t *testing.T) {
		var output bytes.Buffer
		if err := Export(&output, "csv", reports); err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(&output).ReadAll()
		if err != nil || len(rows) != 2 || len(rows[0]) != len(rows[1]) {
			t.Fatalf("bad CSV structure: %v, %v", rows, err)
		}
		values := map[string]string{}
		for i, name := range rows[0] {
			values[name] = rows[1][i]
		}
		for key, want := range map[string]string{"Title": reports[0].Title, "Words": "750", "Estimated pages": "3", "Source page markers": "42", "File bytes": "1234", "EPUBCheck": "passed", "Path": reports[0].Path, "Series": "The Sequence", "Series index": "1.5"} {
			if values[key] != want {
				t.Errorf("CSV %s = %q, want %q", key, values[key], want)
			}
		}
	})
}

func TestCSVFormulaInjection(t *testing.T) {
	for _, dangerous := range []string{"=1+1", "+SUM(A1)", "-10+1", "@SUM(A1)", "\t=CMD()", "\r=CMD()", "\n=CMD()"} {
		t.Run(dangerous, func(t *testing.T) {
			var output bytes.Buffer
			reports := []Report{{Report: epub.Report{Title: dangerous, Author: dangerous, Filename: dangerous, Path: dangerous, Series: dangerous, SeriesIndex: dangerous}}}
			if err := Export(&output, "csv", reports); err != nil {
				t.Fatal(err)
			}
			rows, err := csv.NewReader(&output).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range []int{0, 1, 2, 16, 17, 18} {
				if !strings.HasPrefix(rows[1][i], "'") {
					t.Fatalf("unsafe CSV cell %q", rows[1][i])
				}
			}
		})
	}
	if got := safeCSV("Normal title"); got != "Normal title" {
		t.Fatalf("safe metadata changed: %q", got)
	}
}

var errWrite = errors.New("intentional write failure")

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errWrite }

func TestExportErrors(t *testing.T) {
	for _, format := range []string{"json", "csv"} {
		if err := Export(failingWriter{}, format, nil); !errors.Is(err, errWrite) {
			t.Fatalf("%s export swallowed write failure: %v", format, err)
		}
	}
	if err := Export(io.Discard, "unknown", nil); err == nil {
		t.Fatal("unsupported format was accepted")
	}
}
