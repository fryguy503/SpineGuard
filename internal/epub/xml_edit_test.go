package epub

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLosslessXMLAttributeAndElementSpans(t *testing.T) {
	src := []byte("\xef\xbb\xbf<?xml version='1.0' encoding='UTF-8'?><!-- <a href='fake'/> --><html xmlns='http://www.w3.org/1999/xhtml'><head><script src = 'a&amp;b.js'/></head><body><![CDATA[unchanged <tag>]]><a  href = 'missing.xhtml#start' data-x='keep'>Visible text</a></body></html>")
	elements, err := parseEditableXML(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	var edits []xmlEdit
	for _, el := range elements {
		if el.Name.Local == "script" {
			if !el.SelfClosing || !bytes.HasSuffix(src[el.Start:el.End], []byte("/>")) {
				t.Fatalf("wrong script span: %+v", el)
			}
			edits = append(edits, xmlEdit{Start: el.Start, End: el.End})
		}
		if el.Name.Local == "a" {
			for _, at := range el.Attributes {
				if at.Name == "href" {
					edits = append(edits, xmlEdit{at.Start, at.End, escapeXMLAttribute("chapter.xhtml?x=1&y=2#start", at.Quote)})
				}
			}
		}
	}
	got, err := applyXMLEdits(src, edits)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(string(src), "<script src = 'a&amp;b.js'/>", "", 1)
	want = strings.Replace(want, "missing.xhtml#start", "chapter.xhtml?x=1&amp;y=2#start", 1)
	if string(got) != want {
		t.Fatalf("unrelated XML bytes changed:\n%s", got)
	}
}

func TestLosslessXMLRejectsAmbiguityAndUnsupportedEncoding(t *testing.T) {
	for _, src := range []string{`<?xml version="1.0" encoding="ISO-8859-1"?><root/>`, `<root><a></root>`, `<!DOCTYPE root [<!ENTITY custom "value">]><root>&custom;</root>`} {
		if _, err := parseEditableXML(context.Background(), []byte(src)); err == nil {
			t.Fatalf("accepted unsupported source: %s", src)
		}
	}
	if _, err := applyXMLEdits([]byte(`<root a="v"/>`), []xmlEdit{{1, 5, []byte("bad")}, {2, 4, nil}}); err == nil {
		t.Fatal("overlapping edits were accepted")
	}
	if _, err := applyXMLEdits([]byte(`<root/>`), []xmlEdit{{0, 7, []byte(`<broken>`)}}); err == nil {
		t.Fatal("invalid edited XML was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := parseEditableXML(ctx, []byte(`<root/>`)); err == nil {
		t.Fatal("cancellation ignored")
	}
}
