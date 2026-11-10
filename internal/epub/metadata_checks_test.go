package epub

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestLanguageRepairRequiresReviewedInputAndPreservesValidValues(t *testing.T) {
	entries := fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, `<dc:language>en</dc:language>`, "", 1)
	entries[3].body = strings.Replace(entries[3].body, `<html `, `<html xml:lang="en" `, 1)
	input := writeFixture(t, entries)
	choices := DefaultRepairOptions()
	plan, err := PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 0 || plan.Suggestions.Language != "en" || len(plan.Suggestions.LanguageSources) != 1 {
		t.Fatalf("language suggestion applied automatically or missing: %+v", plan)
	}
	choices.Language = "en-us"
	plan, err = PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "metadata") != 1 {
		t.Fatalf("explicit language not planned: %+v", plan.Actions)
	}
	output := filepath.Join(filepath.Dir(input), "language.epub")
	fixed, err := RepairWithOptions(context.Background(), input, output, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Report.Language != "en-US" || hasIssue(fixed.Report, "METADATA_LANGUAGE") {
		t.Fatalf("language not repaired: %+v", fixed.Report)
	}
	if string(archiveEntryBytes(t, input, "OPS/chapter.xhtml", false)) != string(archiveEntryBytes(t, output, "OPS/chapter.xhtml", false)) {
		t.Fatal("language metadata repair changed prose")
	}
	valid := fixtureEntries()
	modified, actions, _ := planMetadataDocuments(context.Background(), map[string][]byte{"book.opf": []byte(valid[2].body)}, choices)
	if len(modified) != 0 || len(actions) != 0 {
		t.Fatal("existing valid language was overwritten")
	}
	choices.Language = "English United States"
	if ValidateRepairOptions(choices) == nil {
		t.Fatal("invalid language accepted")
	}
	choices.FixMetadata = false
	if ValidateRepairOptions(choices) != nil {
		t.Fatal("inactive language field rejected")
	}
}

func TestLanguageChecksFlagInvalidTagAndConflictingSuggestions(t *testing.T) {
	entries := fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, `<dc:language>en</dc:language>`, `<dc:language>en_US</dc:language>`, 1)
	report := scanFixture(t, entries)
	if !hasIssue(report, "METADATA_LANGUAGE_INVALID") {
		t.Fatal("invalid language syntax not checked")
	}
	text := map[string][]byte{"en.xhtml": []byte(`<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en"><body/></html>`), "fr.xhtml": []byte(`<html xmlns="http://www.w3.org/1999/xhtml" lang="fr"><body/></html>`)}
	if suggestion := repairSuggestions(text); suggestion.Language != "" {
		t.Fatalf("conflicting languages guessed: %+v", suggestion)
	}
}

func TestRestoreExistingIdentifierWithoutChangingItsValue(t *testing.T) {
	entries := fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, ` id="uid"`, "", 1)
	input := writeFixture(t, entries)
	choices := DefaultRepairOptions()
	plan, err := PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "metadata") != 1 {
		t.Fatalf("existing identifier not recoverable: %+v", plan.Actions)
	}
	output := filepath.Join(filepath.Dir(input), "identified.epub")
	fixed, err := RepairWithOptions(context.Background(), input, output, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Report.Identifier != "urn:uuid:test" || hasIssue(fixed.Report, "METADATA_IDENTIFIER") {
		t.Fatalf("identifier value or reference wrong: %+v", fixed.Report)
	}
	want := entries[2].body
	actual := string(archiveEntryBytes(t, output, "OPS/book.opf", false))
	if strings.Replace(actual, ` id="uid"`, "", 1) != want {
		t.Fatalf("restoration changed unrelated metadata: %s", actual)
	}
}

func TestGenerateIdentifierIsExplicitAndStableAcrossReview(t *testing.T) {
	for _, identifier := range []string{"", `<dc:identifier/>`, `<dc:identifier id="uid"/>`, `<dc:identifier id=""/>`, `<dc:identifier> </dc:identifier>`} {
		t.Run(identifier, func(t *testing.T) {
			entries := fixtureEntries()
			entries[2].body = strings.Replace(entries[2].body, `<dc:identifier id="uid">urn:uuid:test</dc:identifier>`, identifier, 1)
			input := writeFixture(t, entries)
			choices := DefaultRepairOptions()
			normal, err := PlanRepair(context.Background(), input, Options{}, choices)
			if err != nil {
				t.Fatal(err)
			}
			if len(normal.Actions) != 0 {
				t.Fatal("identifier invented without explicit choice")
			}
			choices.GenerateIdentifier = true
			plan, err := PlanRepair(context.Background(), input, Options{}, choices)
			if err != nil {
				t.Fatal(err)
			}
			if countActions(plan, "metadata") != 1 {
				t.Fatalf("explicit generation not planned: %+v", plan.Actions)
			}
			choices.ExpectedSHA256 = plan.SourceSHA256
			output := filepath.Join(filepath.Dir(input), "generated.epub")
			fixed, err := RepairWithOptions(context.Background(), input, output, Options{}, choices)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(fixed.Report.Identifier, "urn:uuid:") || hasIssue(fixed.Report, "METADATA_IDENTIFIER") {
				t.Fatalf("generated identity invalid: %+v", fixed.Report)
			}
			found := false
			for _, action := range plan.Actions {
				if action.Kind == "metadata" && strings.Contains(action.Description, fixed.Report.Identifier) {
					found = true
				}
			}
			if !found {
				t.Fatal("preview and output generated different identifiers")
			}
		})
	}
}

func TestIdentifierRepairDoesNotGuessAmbiguousOrInvalidReferences(t *testing.T) {
	entries := fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, ` id="uid"`, "", 1)
	entries[2].body = strings.Replace(entries[2].body, `</metadata>`, `<dc:identifier>different</dc:identifier></metadata>`, 1)
	plan, err := PlanRepair(context.Background(), writeFixture(t, entries), Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if countActions(plan, "metadata") != 0 {
		t.Fatal("multiple identifiers guessed")
	}
	entries = fixtureEntries()
	entries[2].body = strings.Replace(entries[2].body, `unique-identifier="uid"`, `unique-identifier="123 bad"`, 1)
	entries[2].body = strings.Replace(entries[2].body, ` id="uid"`, "", 1)
	plan, err = PlanRepair(context.Background(), writeFixture(t, entries), Options{}, DefaultRepairOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) == 0 {
		t.Fatal("existing value could not receive a safe unique internal ID")
	}
	for _, action := range plan.Actions {
		if strings.Contains(action.Description, "123 bad") {
			t.Fatal("invalid ID copied into repair")
		}
	}
}

func TestGenerationDoesNotDiscardNestedIdentifierMetadata(t *testing.T) {
	entries := fixtureEntries()
	source := strings.Replace(entries[2].body, `<dc:identifier id="uid">urn:uuid:test</dc:identifier>`, `<dc:identifier id="uid"><meta>Keep this metadata</meta></dc:identifier>`, 1)
	choices := DefaultRepairOptions()
	choices.GenerateIdentifier = true
	choices.ExpectedSHA256 = strings.Repeat("a", 64)
	modified, actions, notes := planMetadataDocuments(context.Background(), map[string][]byte{"book.opf": []byte(source)}, choices)
	if len(modified) > 0 || len(actions) > 0 {
		t.Fatal("generation discarded existing nested metadata")
	}
	if len(notes) != 1 || notes[0].Repairable || !strings.Contains(notes[0].RepairReason, "nested") {
		t.Fatalf("nested metadata reason missing: %+v", notes)
	}
}

func TestGenerationDoesNotMislabelExistingIdentifierScheme(t *testing.T) {
	entries := fixtureEntries()
	source := strings.Replace(entries[2].body, `<dc:identifier id="uid">urn:uuid:test</dc:identifier>`, `<dc:identifier xmlns:opf="http://www.idpf.org/2007/opf" id="uid" opf:scheme="ISBN"/>`, 1)
	choices := DefaultRepairOptions()
	choices.GenerateIdentifier = true
	choices.ExpectedSHA256 = strings.Repeat("b", 64)
	modified, actions, notes := planMetadataDocuments(context.Background(), map[string][]byte{"book.opf": []byte(source)}, choices)
	if len(modified) > 0 || len(actions) > 0 {
		t.Fatal("generated UUID was mislabeled as another identifier scheme")
	}
	if len(notes) != 1 || notes[0].Repairable || !strings.Contains(notes[0].RepairReason, "scheme") {
		t.Fatalf("identifier scheme reason missing: %+v", notes)
	}
}
