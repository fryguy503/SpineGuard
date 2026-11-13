package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const missingSceneImage = `<img alt="star.jpg" src="Images/star.jpg" class="calibre9"/>`

func sceneDividerFixture(t *testing.T, body string) []fixtureEntry {
	t.Helper()
	entries := fixtureEntries()
	entries[3].body = `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title><link rel="stylesheet" href="style.css"/></head><body><h1 id="start">Hello world</h1><p>One two three.</p>` + body + `</body></html>`
	entries[2].body = strings.Replace(entries[2].body, "</manifest>", `<item id="css" href="style.css" media-type="text/css"/></manifest>`, 1)
	return append(entries, fixtureEntry{"OPS/style.css", `.star {display:block;text-align:center;margin-top:2em;margin-bottom:2em}.calibre9{width:auto;height:auto}`, zip.Deflate})
}

func TestMissingSceneDividersAreOptionalAndPreserveOriginalAndOtherPayloads(t *testing.T) {
	body := `<p class="star" id="break-one"><span>` + missingSceneImage + `</span></p><p>Keep this paragraph.</p><p class="star" id="break-two"><span>` + missingSceneImage + `</span></p><img alt="Existing illustration" src="figure.png"/>`
	entries := sceneDividerFixture(t, body)
	entries[2].body = strings.Replace(entries[2].body, "</manifest>", `<item id="figure" href="figure.png" media-type="image/png"/></manifest>`, 1)
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	entries = append(entries, fixtureEntry{"OPS/figure.png", picture.String(), zip.Deflate})
	input := writeFixture(t, entries)
	original, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	defaults := DefaultRepairOptions()
	if defaults.ReplaceMissingSceneDividers {
		t.Fatal("missing scene-divider replacement must be opt-in")
	}
	unchecked, err := PlanRepair(context.Background(), input, Options{}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if countActions(unchecked, "scene_dividers") != 0 || len(unchecked.Unresolved) != 2 || unchecked.Report.Errors != 2 {
		t.Fatalf("unchecked option altered findings: %+v", unchecked)
	}
	for _, issue := range unchecked.Report.Issues {
		if issue.Code == "LINK_MISSING" && (!issue.Repairable || !issue.OptionalRepair || issue.RepairKind != "scene_dividers") {
			t.Fatalf("optional action not discoverable: %+v", issue)
		}
	}
	choices := defaults
	choices.FixLinks = false // The explicit option does not require link fixes.
	choices.ReplaceMissingSceneDividers = true
	selected, err := PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	if countActions(selected, "scene_dividers") != 2 || len(selected.Unresolved) != 0 {
		t.Fatalf("selected preview: %+v", selected)
	}
	for _, action := range selected.Actions {
		if action.Kind == "scene_dividers" && !action.RequiresOptIn {
			t.Fatal("divider action lost its opt-in marker")
		}
	}
	output := filepath.Join(t.TempDir(), "with-text-dividers.epub")
	result, err := RepairWithOptions(context.Background(), input, output, Options{}, selected.Options)
	if err != nil {
		t.Fatal(err)
	}
	if result.Report.Errors != 0 || result.Report.Warnings != 0 || result.Report.Stats.Words != unchecked.Report.Stats.Words || result.Report.Stats.Chapters != unchecked.Report.Stats.Chapters {
		t.Fatalf("repaired content differs unexpectedly: %+v", result.Report)
	}
	fixed := archiveEntryBytes(t, output, "OPS/chapter.xhtml", false)
	expected := strings.ReplaceAll(entries[3].body, missingSceneImage, "* * *")
	if string(fixed) != expected {
		t.Fatalf("edits exceeded exact image source spans: %s", fixed)
	}
	for _, entry := range entries {
		if entry.name == "OPS/chapter.xhtml" || entry.name == "mimetype" {
			continue
		}
		if !bytes.Equal(archiveEntryBytes(t, input, entry.name, true), archiveEntryBytes(t, output, entry.name, true)) {
			t.Fatalf("unrelated compressed payload changed: %s", entry.name)
		}
	}
	after, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("repair changed the original EPUB")
	}
	again, err := PlanRepair(context.Background(), output, Options{}, choices)
	if err != nil || countActions(again, "scene_dividers") != 0 {
		t.Fatalf("repeated replacement must be a no-op: %+v %v", again, err)
	}
}

func TestSceneDividerRepetitionCanSpanDeclaredChapters(t *testing.T) {
	entries := sceneDividerFixture(t, `<p class="scene-break"><span><img src="Images/scene-divider.png" alt=""/></span></p>`)
	entries = repairDocumentFixture(entries, "OPS/second.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Second</title></head><body><p class="scene-break"><img src="Images/scene-divider.png" alt="scene-divider.png"/></p></body></html>`)
	input := writeFixture(t, entries)
	choices := DefaultRepairOptions()
	choices.ReplaceMissingSceneDividers = true
	plan, err := PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil || countActions(plan, "scene_dividers") != 2 {
		t.Fatalf("repeated missing divider across chapters: %+v %v", plan, err)
	}
}

func TestMissingSceneDividerCancellationAndChangedSource(t *testing.T) {
	input := writeFixture(t, sceneDividerFixture(t, strings.Repeat(`<p class="star">`+missingSceneImage+`</p>`, 2)))
	choices := DefaultRepairOptions()
	choices.ReplaceMissingSceneDividers = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PlanRepair(ctx, input, Options{}, choices); err == nil {
		t.Fatal("canceled divider preview succeeded")
	}
	plan, err := PlanRepair(context.Background(), input, Options{}, choices)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(input, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("changed"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	output := filepath.Join(t.TempDir(), "must-not-write.epub")
	if _, err := RepairWithOptions(context.Background(), input, output, Options{}, plan.Options); err == nil {
		t.Fatal("changed source bypassed review binding")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("changed source produced a repair output")
	}
}
