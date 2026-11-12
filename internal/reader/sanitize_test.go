package reader

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"strconv"
	"strings"
	"testing"
)

func testResolve(ref, role string) string {
	switch ref {
	case "chapter.xhtml#note":
		return "http://wails.localhost/__reader/test/r/chapter.xhtml#note"
	case "cover.png":
		return "http://wails.localhost/__reader/test/r/cover.png"
	case "style.css":
		return "http://wails.localhost/__reader/test/r/style.css"
	case "font.woff2":
		return "http://wails.localhost/__reader/test/r/font.woff2"
	}
	return ""
}

func assertReaderXML(t *testing.T, b []byte) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		_, err := d.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("invalid reader XML: %v\n%s", err, b)
		}
	}
}

func TestSanitizeHTMLPassiveContentAndLocalResources(t *testing.T) {
	src := []byte(`<?xml version="1.0"?><!DOCTYPE html><html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en"><head><title>A book</title><link href="style.css" rel="stylesheet"/><style>p { color: navy; background-image:url('cover.png') } @font-face {font-family:Book;src:url(font.woff2)}</style></head><body><h1 id="one">Café &amp; 日本語</h1><p style="font-family:Book; font-size:1.2em">Hello <em>world</em> &mdash;</p><table><tr><td colspan="2">Cell</td></tr></table><img src="cover.png" alt="Cover"/><a href="chapter.xhtml#note">Note</a><svg viewBox="0 0 100 100"><defs><linearGradient id="grad"><stop offset="0" stop-color="red"/></linearGradient></defs><rect width="100" height="100" fill="url(#grad)"/><image xmlns:xlink="http://www.w3.org/1999/xlink" xlink:href="cover.png"/></svg><math><mfrac><mi>a</mi><mn>2</mn></mfrac></math></body></html>`)
	out, err := SanitizeHTML(context.Background(), src, testResolve)
	if err != nil {
		t.Fatal(err)
	}
	assertReaderXML(t, out)
	s := string(out)
	for _, want := range []string{"Café", "日本語", "—", "colspan=\"2\"", "viewBox=\"0 0 100 100\"", "http://www.w3.org/2000/svg", "http://www.w3.org/1998/Math/MathML", "/__reader/test/r/cover.png", "/__reader/test/r/font.woff2", "data-sg-ref=\"chapter.xhtml#note\"", "xml:lang=\"en\"", "font-size:1.2em"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	head := strings.Index(s, "<head")
	csp := strings.Index(s, "Content-Security-Policy")
	style := strings.Index(s, "<link")
	if head < 0 || csp < head || style < csp {
		t.Errorf("CSP must precede publisher resources: %s", s)
	}
}

func TestSanitizeHTMLRemovesActiveMarkupAndNetwork(t *testing.T) {
	src := []byte(`<html data-sg-line="1"><head><base href="https://evil.test/"><meta http-equiv="refresh" content="0;url=https://evil.test/"><meta http-equiv="Content-Security-Policy" content="script-src *"><link rel="preload" href="https://evil.test/steal"><link rel="stylesheet" href="https://evil.test/steal"></head><body onload="alert(1)"><script src="chapter.xhtml#note">alert(1)</script><iframe srcdoc="&lt;script&gt;bad&lt;/script&gt;"></iframe><object data="cover.png">bad fallback</object><form action="chapter.xhtml#note"><input autofocus onfocus="alert(1)"></form><img src="https://evil.test/image" srcset="https://evil.test/set 2x" onerror="alert(1)" data-sg-ref="forged"><a href="java&#x73;cript:alert(1)" ping="https://evil.test/ping" target="_top">click</a><svg onload="alert(1)"><foreignObject><iframe src="chapter.xhtml#note"></iframe></foreignObject><animate attributeName="href" values="javascript:alert(1)"></animate><set attributeName="href" to="https://evil.test/"></set><use href="chapter.xhtml#note"/><image href="data:image/svg+xml;base64,PHN2Zy8+"/><rect fill="url(https://evil.test/a)" style="background:image-set('https://evil.test/a' 1x)"/></svg></body></html>`)
	out, err := SanitizeHTML(context.Background(), src, testResolve)
	if err != nil {
		t.Fatal(err)
	}
	assertReaderXML(t, out)
	d := xml.NewDecoder(bytes.NewReader(out))
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		name := strings.ToLower(start.Name.Local)
		if dropTags[name] || name == "foreignobject" {
			t.Errorf("active element survived: %s", name)
		}
		for _, a := range start.Attr {
			n := strings.ToLower(a.Name.Local)
			if strings.HasPrefix(n, "on") || n == "srcset" || n == "ping" || n == "target" || n == "data-sg-line" {
				t.Errorf("active/forged attr survived: %+v", a)
			}
			if n == "href" || n == "src" || n == "fill" || n == "style" {
				if strings.Contains(a.Value, "evil.test") || strings.Contains(a.Value, "javascript:") {
					t.Errorf("unsafe reference survived: %+v", a)
				}
			}
		}
	}
	if strings.Contains(string(out), "forged") {
		t.Fatal("forged reader marker survived")
	}
}

func TestSanitizeCSSCanonicalizesEscapesAndRestrictsResources(t *testing.T) {
	src := []byte(`@import "style.css" screen; @font-face {font-family:Book; src:u\72l(font.woff2)} p {background:URL("cover.png"); color:navy; width:calc(100% - 2em)} a{background:u\72l(https\3a //evil.test/a)} b{background:im\61ge-set("https://evil.test/a" 1x)} i{width:expre\73sion(alert(1));-moz-binding:url(cover.png);color:red} @keyframes x {from{opacity:0}to{opacity:1}} @unknown { p { color:pink }} .x{animation:x 1s infinite;content:"A book"}`)
	out, err := SanitizeCSS(context.Background(), src, testResolve)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"@import", "/__reader/test/r/style.css", "/__reader/test/r/font.woff2", "/__reader/test/r/cover.png", "color:navy", "calc(100% - 2em)", "color:red", `content:"A book"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	for _, bad := range []string{"evil.test", "expression", "binding", "image-set", "keyframes", "@unknown", "pink", "animation"} {
		if strings.Contains(s, bad) {
			t.Errorf("unsafe CSS %q survived: %s", bad, s)
		}
	}
}

func TestSanitizeSVGStandaloneAndXMLFallback(t *testing.T) {
	src := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 800"><image href="cover.png" width="600" height="800"/><script>alert(1)</script><text x="20" y="40">Cover &amp; title</text></svg>`)
	out, err := SanitizeSVG(context.Background(), src, testResolve)
	if err != nil {
		t.Fatal(err)
	}
	assertReaderXML(t, out)
	if strings.Contains(string(out), "<html") || strings.Contains(string(out), "<script") || !strings.Contains(string(out), "/__reader/test/r/cover.png") {
		t.Fatalf("bad SVG: %s", out)
	}
	out, err = SanitizeHTML(context.Background(), []byte(`<p>Café &nbsp; &mdash; <em>unclosed <b>paragraph<img src="cover.png">`), testResolve)
	if err != nil {
		t.Fatal(err)
	}
	assertReaderXML(t, out)
	if !strings.Contains(string(out), "Café") || !strings.Contains(string(out), "unclosed") {
		t.Fatalf("fallback lost book text: %s", out)
	}
	legacy := append([]byte(`<?xml version="1.0" encoding="windows-1252"?><p>`), 0x93, 'H', 'i', 0x94)
	out, err = SanitizeHTML(context.Background(), legacy, testResolve)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "“Hi”") {
		t.Fatalf("legacy encoding: %s", out)
	}
}

func TestSanitizeCSSCustomProperties(t *testing.T) {
	src := []byte(`.x{--leak:url(https://outside.invalid/custom);background-image:var(--leak);--escape:u\72l(https\3a //outside.invalid/escaped);--image:im\61ge-set("https://outside.invalid/string" 1x);--BookInk:navy;color:var(--BookInk);--cover:url(cover.png);background:var(--cover);--Word:"publisher text";content:var(--Word)}`)
	out, err := SanitizeCSS(context.Background(), src, testResolve)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, bad := range []string{"outside.invalid", "--leak:", "--escape:", "--image:"} {
		if strings.Contains(s, bad) {
			t.Errorf("unsafe custom property survived: %s", s)
		}
	}
	for _, want := range []string{"--BookInk:navy", "var(--BookInk)", "--cover:url(\"http://wails.localhost/__reader/test/r/cover.png\")", `--Word:"publisher text"`} {
		if !strings.Contains(s, want) {
			t.Errorf("publisher variable %q was not preserved: %s", want, s)
		}
	}
}

func TestSanitizeDataImagesAndResolverGuard(t *testing.T) {
	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a2S8AAAAASUVORK5CYII="
	if safeResourceURL(png, "image", nil) == "" {
		t.Fatal("valid bounded PNG data image rejected")
	}
	for _, ref := range []string{"https://evil.test/a", "//evil.test/a", "javascript:alert(1)", "file:///etc/passwd", "data:text/html;base64,PHNjcmlwdD4=", "data:image/svg+xml;base64,PHN2Zy8+", "data:image/png;base64,PHNjcmlwdD4=", "\\\\evil.test\\a", "a\nb"} {
		if got := safeResourceURL(ref, "image", testResolve); got != "" {
			t.Errorf("unsafe image allowed: %s", got)
		}
	}
	if got := safeResourceURL("cover.png", "image", func(string, string) string { return "javascript:alert(1)" }); got != "" {
		t.Fatal("bad resolver scheme survived")
	}
	if got := safeResourceURL("cover.png", "image", func(string, string) string { return "http://wails.localhost/wails/runtime.js" }); got != "" {
		t.Fatal("non-reader path survived")
	}
}

func TestSanitizeLimitsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SanitizeHTML(ctx, []byte("<p>Book</p>"), testResolve); err != context.Canceled {
		t.Fatalf("HTML cancellation: %v", err)
	}
	if _, err := SanitizeCSS(ctx, []byte("p{color:red}"), testResolve); err != context.Canceled {
		t.Fatalf("CSS cancellation: %v", err)
	}
	if _, err := SanitizeHTML(context.Background(), make([]byte, maxHTMLBytes+1), testResolve); err == nil {
		t.Fatal("unbounded HTML accepted")
	}
	if _, err := SanitizeCSS(context.Background(), make([]byte, maxCSSBytes+1), testResolve); err == nil {
		t.Fatal("unbounded CSS accepted")
	}
	deep := strings.Repeat("<div>", maxHTMLDepth+10) + "Book" + strings.Repeat("</div>", maxHTMLDepth+10)
	if _, err := SanitizeHTML(context.Background(), []byte(deep), testResolve); err == nil {
		t.Fatal("deep document accepted")
	}
	var attrs strings.Builder
	attrs.WriteString("<p ")
	for i := 0; i < 257; i++ {
		attrs.WriteString("data-x" + strconv.Itoa(i) + "='a' ")
	}
	attrs.WriteString(">Book</p>")
	if _, err := SanitizeHTML(context.Background(), []byte(attrs.String()), testResolve); err == nil {
		t.Fatal("element with too many attributes accepted")
	}
}
