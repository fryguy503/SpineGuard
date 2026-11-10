package epub

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func inspectSource(t *testing.T, source string) (contentInfo, Report) {
	t.Helper()
	r := Report{Issues: []Issue{}}
	a := archive{ctx: context.Background(), report: &r}
	info := a.inspectContent("OPS/chapter.xhtml", []byte(source))
	return info, r
}

func TestXHTMLSourceSemanticsAvoidHTMLParserClones(t *testing.T) {
	source := `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Title</title></head><body><p><b id="source-id">One<div>two</div>three</b></p><table><p id="table-id">Four</p><tr><td>five</td></tr></table></body></html>`
	info, r := inspectSource(t, source)
	if hasIssue(r, "CONTENT_DUPLICATE_ID") || info.idCounts["source-id"] != 1 || info.idCounts["table-id"] != 1 {
		t.Fatalf("invented source identifiers: %+v %+v", info.idCounts, r.Issues)
	}
	if info.words != 5 {
		t.Fatalf("source text count=%d, want5", info.words)
	}
}

func TestSelfClosingXMLScriptDoesNotConsumeBookText(t *testing.T) {
	source := `<html xmlns="http://www.w3.org/1999/xhtml"><head><script src="insertSpan.js" type="text/javascript"/></head><body><h1>One two</h1><p>Three four five.</p></body></html>`
	info, r := inspectSource(t, source)
	if info.words != 5 || !hasIssue(r, "CONTENT_SCRIPT") {
		t.Fatalf("self-closing script changed source semantics: words=%d issues=%+v", info.words, r.Issues)
	}
	if len(info.refs) != 1 || info.refs[0].href != "insertSpan.js" {
		t.Fatalf("script reference lost: %+v", info.refs)
	}
}

func TestSourceIdentifierUniquenessAndLegacyNames(t *testing.T) {
	for _, tc := range []struct {
		body       string
		duplicates bool
		anchors    int
	}{
		{`<a id="x" name="x"/>`, false, 1},
		{`<a name="x"/><p id="x">Text</p>`, false, 2},
		{`<p id="x">First</p><p id="x">Second</p>`, true, 2},
		{`<p id="x" xml:id="x">One</p>`, false, 1},
	} {
		info, r := inspectSource(t, `<html xmlns="http://www.w3.org/1999/xhtml"><body>`+tc.body+`</body></html>`)
		if hasIssue(r, "CONTENT_DUPLICATE_ID") != tc.duplicates || info.idCounts["x"] != tc.anchors {
			t.Fatalf("unexpected uniqueness for %s: %+v %+v", tc.body, info.idCounts, r.Issues)
		}
	}
}

func TestXMLSiblingSelfClosingTagsAreNotNesting(t *testing.T) {
	info, r := inspectSource(t, `<html><body>`+strings.Repeat(`<span/>`, 1025)+`One two</body></html>`)
	if hasIssue(r, "CONTENT_COMPLEXITY") || info.words != 2 {
		t.Fatalf("selfclosing siblings treated as nesting: %+v %+v", info, r.Issues)
	}
}

func TestDTDSourceFallbackHandlesSelfClosingScript(t *testing.T) {
	info, r := inspectSource(t, `<!DOCTYPE html SYSTEM "book.dtd"><html><head><script src="book.js"/></head><body><b id="x">One<div>&custom;</div>three</b></body></html>`)
	if info.words != 3 || hasIssue(r, "CONTENT_DUPLICATE_ID") || info.idCounts["x"] != 1 {
		t.Fatalf("fallback invented DOM/source semantics: %+v %+v", info, r.Issues)
	}
}

func TestScriptedActionsAreDetectedWithoutScriptElements(t *testing.T) {
	for _, body := range []string{`<p onclick="run()">Words</p>`, `<a href="JaVa&#x9;ScRiPt:run()">Words</a>`} {
		_, r := inspectSource(t, `<html><body>`+body+`</body></html>`)
		if !hasIssue(r, "CONTENT_SCRIPT") {
			t.Fatalf("scripted action missed: %s", body)
		}
	}
	_, r := inspectSource(t, `<html xmlns:custom="urn:custom"><body><p custom:onload="metadata">Words</p></body></html>`)
	if hasIssue(r, "CONTENT_SCRIPT") {
		t.Fatal("namespaced metadata treated as an HTML event")
	}
}

func TestReaderCleanupIsExplicitLosslessAndPreservesResources(t *testing.T) {
	source := []byte(`<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><!-- keep comment --><style>p { color: red }</style><script src='book.js'/><script><![CDATA[if (a < b) run();]]></script></head><body onload="run()"><p class='same'>Keep &amp; preserve text.</p><a href="java&#x9;script:run()" onclick='run()'>Click text</a><img src="cover.png"/></body></html>`)
	text := map[string][]byte{"OPS/chapter.xhtml": source, "OPS/book.js": []byte("original script bytes"), "OPS/style.css": []byte("p { color: red }")}
	modified, actions, notes := planCleanupDocuments(context.Background(), text, DefaultRepairOptions())
	if len(modified) != 0 || len(actions) != 0 || len(notes) == 0 || !notes[0].Repairable || !notes[0].OptionalRepair {
		t.Fatalf("cleanup was not opt-in: edits=%d actions=%+v notes=%+v", len(modified), actions, notes)
	}
	choices := DefaultRepairOptions()
	choices.RemoveScripts = true
	modified, actions, notes = planCleanupDocuments(context.Background(), text, choices)
	fixed := modified["OPS/chapter.xhtml"]
	if len(actions) != 1 || actions[0].Count != 5 || !actions[0].RequiresOptIn {
		t.Fatalf("cleanup accounting: %+v", actions)
	}
	for _, preserved := range []string{`<!-- keep comment -->`, `<style>p { color: red }</style>`, `<p class='same'>Keep &amp; preserve text.</p>`, `<img src="cover.png"/>`, `<a>Click text</a>`} {
		if !bytes.Contains(fixed, []byte(preserved)) {
			t.Fatalf("source changed outside requested edits: missing %s in %s", preserved, fixed)
		}
	}
	if bytes.Contains(fixed, []byte("<script")) || bytes.Contains(fixed, []byte("onclick")) || bytes.Contains(fixed, []byte("onload")) {
		t.Fatalf("scripts remain: %s", fixed)
	}
	if _, r := inspectSource(t, string(fixed)); hasIssue(r, "CONTENT_SCRIPT") {
		t.Fatalf("cleanup did not clear script finding: %+v", r.Issues)
	}
	if _, ok := modified["OPS/book.js"]; ok {
		t.Fatal("archive script resource was modified")
	}
	if string(text["OPS/book.js"]) != "original script bytes" || !bytes.Equal(source, text["OPS/chapter.xhtml"]) {
		t.Fatal("original source input modified")
	}
	if len(notes) == 0 || !notes[0].Repairable {
		t.Fatalf("missing cleanup eligibility: %+v", notes)
	}
}

func TestDuplicateIDRepairOnlyForUnusedIndependentIdentifiers(t *testing.T) {
	source := []byte(`<html><body><p id="x">First</p><a id='x' name='x'>Second</a><span id="x-spineguard-2">Existing</span></body></html>`)
	modified, actions, notes := planCleanupDocuments(context.Background(), map[string][]byte{"chapter.xhtml": source}, DefaultRepairOptions())
	fixed := modified["chapter.xhtml"]
	if len(actions) != 1 || actions[0].Kind != "duplicate_ids" || actions[0].Count != 1 || len(notes) != 1 || !notes[0].Repairable {
		t.Fatalf("unused duplicate not repairable: %+v %+v", actions, notes)
	}
	if !bytes.Contains(fixed, []byte(`<p id="x">First</p>`)) || !bytes.Contains(fixed, []byte(`<a id='x-spineguard-3' name='x-spineguard-3'>Second</a>`)) {
		t.Fatalf("first target/source syntax not preserved: %s", fixed)
	}
	if _, r := inspectSource(t, string(fixed)); hasIssue(r, "CONTENT_DUPLICATE_ID") {
		t.Fatalf("duplicate remains: %+v", r.Issues)
	}
	for name, dependency := range map[string]string{
		"linked.xhtml": `<html><body><a href="chapter.xhtml#x">Link</a></body></html>`,
		"styles.css":   `#x { color: red }`,
		"script.xhtml": `<html><head><script>lookup('x')</script></head><body/></html>`,
		"aria.xhtml":   `<html><body><p aria-labelledby="x">Text</p></body></html>`,
	} {
		modified, actions, notes = planCleanupDocuments(context.Background(), map[string][]byte{"chapter.xhtml": source, name: []byte(dependency)}, DefaultRepairOptions())
		if len(modified) != 0 || len(actions) != 0 {
			t.Fatalf("unsafe dependency rewritten for %s: %+v", name, actions)
		}
		found := false
		for _, note := range notes {
			if note.Code == "CONTENT_DUPLICATE_ID" && note.File == "chapter.xhtml" {
				found = true
				if note.Repairable || note.RepairReason == "" {
					t.Fatalf("dependency not explained: %+v", note)
				}
			}
		}
		if !found {
			t.Fatal("missing manual-review reason")
		}
	}
}

func TestReaderCleanupRefusesMalformedOrDTDDependentSource(t *testing.T) {
	for _, source := range []string{`<html><head><script/></head><body>Broken</html>`, `<!DOCTYPE html [<!ENTITY custom "words">]><html><head><script/></head><body>&custom;</body></html>`} {
		choices := DefaultRepairOptions()
		choices.RemoveScripts = true
		modified, actions, _ := planCleanupDocuments(context.Background(), map[string][]byte{"chapter.xhtml": []byte(source)}, choices)
		if len(modified) > 0 || len(actions) > 0 {
			t.Fatalf("unverified source edited: %s", source)
		}
	}
}

func TestCleanupPreservesForeignNamespaceMetadata(t *testing.T) {
	source := []byte(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:custom="urn:custom"><head><script/></head><body><custom:script onload="metadata">Keep custom text</custom:script><p custom:onload="also metadata">Keep prose</p></body></html>`)
	choices := DefaultRepairOptions()
	choices.RemoveScripts = true
	modified, actions, _ := planCleanupDocuments(context.Background(), map[string][]byte{"chapter.xhtml": source}, choices)
	fixed := modified["chapter.xhtml"]
	if len(actions) != 1 || actions[0].Count != 1 || !bytes.Contains(fixed, []byte(`<custom:script onload="metadata">Keep custom text</custom:script>`)) || !bytes.Contains(fixed, []byte(`custom:onload="also metadata"`)) {
		t.Fatalf("foreign metadata changed: %s %+v", fixed, actions)
	}
	if info, r := inspectSource(t, string(fixed)); hasIssue(r, "CONTENT_SCRIPT") || info.words != 5 {
		t.Fatalf("foreign namespace falsely treated as script: %+v %+v", info, r.Issues)
	}
}

func TestDuplicateIDRepairRecognizesExtensionlessAndSVGDependencies(t *testing.T) {
	source := []byte(`<html><body><p id="x">First</p><p id="x">Second</p></body></html>`)
	for name, dependency := range map[string]string{
		"book.opf":     `<package><manifest><item id="style" href="extensionless" media-type="text/css"/></manifest></package>`,
		"inline.xhtml": `<html><head><link rel="stylesheet" href="extensionless"/></head><body/></html>`,
		"graphic.svg":  `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="url(#x)"/></svg>`,
		"timing.svg":   `<svg xmlns="http://www.w3.org/2000/svg"><animate begin="x.click; elsewhere.end"/></svg>`,
		"form.xhtml":   `<html><body><input form="x"/></body></html>`,
	} {
		modified, actions, notes := planCleanupDocuments(context.Background(), map[string][]byte{"chapter.xhtml": source, name: []byte(dependency)}, DefaultRepairOptions())
		if len(modified) > 0 || len(actions) > 0 {
			t.Fatalf("dependent identifiers rewritten for %s: %+v", name, actions)
		}
		found := false
		for _, note := range notes {
			if note.Code == "CONTENT_DUPLICATE_ID" {
				found = true
				if note.Repairable {
					t.Fatalf("dependency missed for %s: %+v", name, note)
				}
			}
		}
		if !found {
			t.Fatal("missing dependency assessment")
		}
	}
}
