package epub

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

const auditDividerImage = `<img alt="star.jpg" src="Images/star.jpg" class="calibre9"/>`

func auditDividerFixture() (map[string][]byte, map[string]bool) {
	text := map[string][]byte{
		"META-INF/container.xml": []byte(`<container xmlns="` + containerNS + `"><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`),
		"OPS/book.opf":           []byte(`<package xmlns="` + opfNS + `" version="2.0" unique-identifier="uid"><metadata xmlns:dc="` + dcNS + `"><dc:identifier id="uid">test-id</dc:identifier><dc:title>Divider audit</dc:title><dc:language>en</dc:language></metadata><manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="chapter"/></spine></package>`),
		"OPS/chapter.xhtml":      []byte(`<html xmlns="` + xhtmlNS + `"><head><title>Test</title></head><body><p id="story">Visible prose stays exactly here.</p><p class="star" id="first-divider"><span>` + auditDividerImage + `</span></p><p class="star" id="second-divider"><span>` + auditDividerImage + `</span></p><p>Final text stays here.</p></body></html>`),
	}
	files := map[string]bool{}
	for name := range text {
		files[name] = true
	}
	return text, files
}

func auditReplaceChapter(text map[string][]byte, old, replacement string) {
	text["OPS/chapter.xhtml"] = []byte(strings.ReplaceAll(string(text["OPS/chapter.xhtml"]), old, replacement))
}

func TestSceneDividerAuditPreservesWrappersAndProse(t *testing.T) {
	text, files := auditDividerFixture()
	original := append([]byte{}, text["OPS/chapter.xhtml"]...)
	choices := DefaultRepairOptions()
	if choices.ReplaceMissingSceneDividers {
		t.Fatal("scene-divider substitution must remain opt-in")
	}
	modified, actions, notes := planSceneDividerDocuments(context.Background(), text, files, choices)
	if len(modified) != 0 || len(actions) != 0 || len(notes) != 1 || !notes[0].OptionalRepair || !notes[0].Repairable {
		t.Fatalf("unselected eligibility: files=%d actions=%+v notes=%+v", len(modified), actions, notes)
	}
	choices.ReplaceMissingSceneDividers = true
	modified, actions, notes = planSceneDividerDocuments(context.Background(), text, files, choices)
	want := bytes.ReplaceAll(original, []byte(auditDividerImage), []byte("* * *"))
	if !bytes.Equal(modified["OPS/chapter.xhtml"], want) || !bytes.Equal(text["OPS/chapter.xhtml"], original) || len(actions) != 1 || actions[0].Count != 2 || !actions[0].RequiresOptIn {
		t.Fatalf("substitution must change only image spans: actions=%+v notes=%+v result=%s", actions, notes, modified["OPS/chapter.xhtml"])
	}
}

func TestSceneDividerAuditRefusesAmbiguousOrSubstantiveReferences(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string][]byte, map[string]bool)
	}{
		{"existing image", func(_ map[string][]byte, files map[string]bool) { files["OPS/Images/star.jpg"] = true }},
		{"case-mismatched image", func(_ map[string][]byte, files map[string]bool) { files["OPS/Images/STAR.JPG"] = true }},
		{"undeclared image elsewhere", func(_ map[string][]byte, files map[string]bool) { files["OPS/Pictures/star.jpg"] = true }},
		{"class substring", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `class="star"`, `class="movie-star-photo"`)
		}},
		{"non-ornament filename", func(text map[string][]byte, _ map[string]bool) { auditReplaceChapter(text, "star.jpg", "diagram.jpg") }},
		{"meaningful alt", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `alt="star.jpg"`, `alt="Diagram showing a star's life cycle"`)
		}},
		{"image identifier", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<img alt=`, `<img id="retained-destination" alt=`)
		}},
		{"image title", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<img alt=`, `<img title="Important illustration" alt=`)
		}},
		{"image ARIA semantics", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<img alt=`, `<img aria-label="Diagram" alt=`)
		}},
		{"image map", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<img alt=`, `<img usemap="#navigation" alt=`)
		}},
		{"image event handler", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<img alt=`, `<img onclick="openDiagram()" alt=`)
		}},
		{"image inline generated text", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<img alt=`, `<img style="content:'Important content'" alt=`)
		}},
		{"image inline existing artwork", func(text map[string][]byte, files map[string]bool) {
			auditReplaceChapter(text, `<img alt=`, `<img style="background-image:url(other.jpg)" alt=`)
			files["OPS/other.jpg"] = true
		}},
		{"nonempty image element", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, auditDividerImage, `<img alt="star.jpg" src="Images/star.jpg">Text inside malformed image</img>`)
		}},
		{"CDATA inside image", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, auditDividerImage, `<img alt="star.jpg" src="Images/star.jpg"><![CDATA[Keep this text]]></img>`)
		}},
		{"prose in divider wrapper", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<span><img`, `<span>Do not erase this caption<img`)
		}},
		{"linked divider", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<span>`+auditDividerImage+`</span>`, `<a href="chapter.xhtml#story">`+auditDividerImage+`</a>`)
		}},
		{"header context", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<body>`, `<body><header>`)
			auditReplaceChapter(text, `</body>`, `</header></body>`)
		}},
		{"foreign namespace context", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<span>`, `<span xmlns="urn:foreign">`)
		}},
		{"base element", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<head>`, `<head><base href="../"/>`)
		}},
		{"xml base", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<body>`, `<body xml:base="../">`)
		}},
		{"duplicate source attributes", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<img alt=`, `<img src="different.jpg" alt=`)
		}},
		{"duplicate wrapper attributes", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<p class="star"`, `<p class="figure" class="star"`)
		}},
		{"fragment URL", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `src="Images/star.jpg"`, `src="Images/star.jpg#detail"`)
		}},
		{"query URL", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `src="Images/star.jpg"`, `src="Images/star.jpg?edition=2"`)
		}},
		{"external URL", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `src="Images/star.jpg"`, `src="https://example.invalid/star.jpg"`)
		}},
		{"mixed inline CSS use", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<body>`, `<body><div style="background:url(Images/star.jpg)"></div>`)
		}},
		{"mixed stylesheet use", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<head>`, `<head><style>.figure{background:url(Images/star.jpg)}</style>`)
		}},
		{"mixed SVG use", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<body>`, `<body><svg xmlns="http://www.w3.org/2000/svg"><image href="Images/star.jpg"/></svg>`)
		}},
		{"mixed substantive image", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<body>`, `<body><p><img src="Images/star.jpg" alt="Scientific chart"/></p>`)
		}},
		{"mixed link use", func(text map[string][]byte, _ map[string]bool) {
			auditReplaceChapter(text, `<body>`, `<body><a href="Images/star.jpg">Keep illustration link</a>`)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			text, files := auditDividerFixture()
			test.mutate(text, files)
			before := append([]byte{}, text["OPS/chapter.xhtml"]...)
			choices := DefaultRepairOptions()
			choices.ReplaceMissingSceneDividers = true
			modified, actions, notes := planSceneDividerDocuments(context.Background(), text, files, choices)
			if len(modified) != 0 || len(actions) != 0 || !bytes.Equal(text["OPS/chapter.xhtml"], before) {
				t.Fatalf("ambiguous or substantive image must be preserved: files=%d actions=%+v", len(modified), actions)
			}
			for _, note := range notes {
				if note.Repairable {
					t.Fatalf("ineligible reference annotated as repairable: %+v", note)
				}
			}
		})
	}
}
