package epub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReaderFindingReferencesPreserveExactValues(t *testing.T) {
	entries := fixtureEntries()
	entries[3].body = strings.Replace(entries[3].body, `</body>`, `<a href="missing%20%22chapter%22.xhtml#note%22%27%26tail">Missing document</a><a href="chapter.xhtml#absent%22%27%26tail">Missing fragment</a><a href="../../escape%22.xhtml">Invalid path</a><img src="https://example.invalid/quoted%22image.png?label=%27" alt="Remote"/></body>`, 1)
	report := scanFixture(t, entries)
	expected := map[string]struct{ reference, anchor string }{
		"LINK_MISSING":    {`missing%20%22chapter%22.xhtml#note%22%27%26tail`, `note"'&tail`},
		"LINK_FRAGMENT":   {`chapter.xhtml#absent%22%27%26tail`, `absent"'&tail`},
		"LINK_PATH":       {`../../escape%22.xhtml`, ""},
		"RESOURCE_REMOTE": {`https://example.invalid/quoted%22image.png?label=%27`, ""},
	}
	for code, want := range expected {
		found := false
		for _, issue := range report.Issues {
			if issue.Code != code || issue.File != "OPS/chapter.xhtml" {
				continue
			}
			found = true
			if issue.Reference != want.reference || issue.Anchor != want.anchor {
				t.Fatalf("%s location was normalized or guessed: %+v", code, issue)
			}
			data, e := json.Marshal(issue)
			if e != nil {
				t.Fatal(e)
			}
			var restored Issue
			if e = json.Unmarshal(data, &restored); e != nil {
				t.Fatal(e)
			}
			if restored.Reference != want.reference || restored.Anchor != want.anchor {
				t.Fatalf("%s location changed after report serialization", code)
			}
		}
		if !found {
			t.Fatalf("%s finding missing: %+v", code, report.Issues)
		}
	}
}

func TestReaderDuplicateIdentifierKeepsQuotedOriginal(t *testing.T) {
	entries := fixtureEntries()
	entries[3].body = strings.Replace(entries[3].body, `</body>`, `<p id="quote&quot;&apos;&amp;:value">First</p><p id="quote&quot;&apos;&amp;:value">Second</p></body>`, 1)
	report := scanFixture(t, entries)
	for _, issue := range report.Issues {
		if issue.Code == "CONTENT_DUPLICATE_ID" {
			if issue.File != "OPS/chapter.xhtml" || issue.Anchor != `quote"'&:value` || issue.Reference != "" {
				t.Fatalf("duplicate identifier changed: %+v", issue)
			}
			return
		}
	}
	t.Fatalf("duplicate finding missing: %+v", report.Issues)
}
