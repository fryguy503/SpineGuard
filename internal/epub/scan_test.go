package epub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

type fixtureEntry struct {
	name, body string
	method     uint16
}

func fixtureEntries() []fixtureEntry {
	return []fixtureEntry{
		{"mimetype", "application/epub+zip", zip.Store},
		{"META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`, zip.Deflate},
		{"OPS/book.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Test Book</dc:title><dc:creator>Jane Reader</dc:creator><dc:language>en</dc:language><dc:identifier id="uid">urn:uuid:test</dc:identifier><meta property="dcterms:modified">2026-11-10T12:00:00Z</meta></metadata><manifest><item id="ch1" href="chapter.xhtml" media-type="application/xhtml+xml"/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest><spine><itemref idref="ch1"/></spine></package>`, zip.Deflate},
		{"OPS/chapter.xhtml", `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Chapter</title><style>ignored style</style></head><body><h1 id="start">Hello world</h1><p>One two three.</p></body></html>`, zip.Deflate},
		{"OPS/nav.xhtml", `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml#start">Chapter</a></li></ol></nav></body></html>`, zip.Deflate},
	}
}
func writeFixture(t *testing.T, entries []fixtureEntry) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "test.epub")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: e.method}
		h.SetMode(0644)
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}
func hasIssue(r Report, code string) bool {
	for _, i := range r.Issues {
		if i.Code == code {
			return true
		}
	}
	return false
}
func scanFixture(t *testing.T, entries []fixtureEntry) Report {
	t.Helper()
	r, e := Scan(context.Background(), writeFixture(t, entries), Options{})
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestValidEPUB3Statistics(t *testing.T) {
	r := scanFixture(t, fixtureEntries())
	if r.Errors != 0 || r.Warnings != 0 {
		t.Fatalf("unexpected findings: %+v", r.Issues)
	}
	if r.Status != "healthy" || r.Title != "Test Book" || r.Author != "Jane Reader" || r.Language != "en" || r.EPUBVersion != "3.0" {
		t.Fatalf("metadata: %+v", r)
	}
	if r.Stats.Words != 5 || r.Stats.Chapters != 1 || r.Stats.EstimatedPages != 1 || r.Stats.ReadingMinutes != 1 || r.Stats.SourcePages != 0 {
		t.Fatalf("stats: %+v", r.Stats)
	}
}

func TestValidEPUB2NCX(t *testing.T) {
	e := fixtureEntries()
	e[2].body = strings.Replace(e[2].body, `version="3.0"`, `version="2.0"`, 1)
	e[2].body = strings.Replace(e[2].body, `<spine>`, `<spine toc="ncx">`, 1)
	e[2].body = strings.Replace(e[2].body, `</manifest>`, `<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest>`, 1)
	e = append(e, fixtureEntry{"OPS/toc.ncx", `<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head/><docTitle><text>Test</text></docTitle><navMap><navPoint id="n1" playOrder="1"><navLabel><text>Chapter</text></navLabel><content src="chapter.xhtml"/></navPoint></navMap></ncx>`, zip.Deflate})
	r := scanFixture(t, e)
	if r.Errors != 0 {
		t.Fatalf("EPUB2 errors: %+v", r.Issues)
	}
}

func TestMalformedMimetypeRepairPreservesOriginal(t *testing.T) {
	e := fixtureEntries()
	e[0].body = "\ufeffapplication/epub+zip\n"
	e[0].method = zip.Deflate
	e[0], e[1] = e[1], e[0]
	input := writeFixture(t, e)
	before, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Scan(context.Background(), input, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"MIMETYPE_CONTENT", "MIMETYPE_COMPRESSED", "MIMETYPE_ORDER"} {
		if !hasIssue(r, code) {
			t.Fatalf("missing %s in %+v", code, r.Issues)
		}
	}
	output := filepath.Join(filepath.Dir(input), "repaired.epub")
	fixed, err := Repair(context.Background(), input, output, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Report.Errors != 0 {
		t.Fatalf("repair errors: %+v", fixed.Report.Issues)
	}
	after, _ := os.ReadFile(input)
	if !bytes.Equal(before, after) {
		t.Fatal("original was modified")
	}
	if fixed.OutputPath != output || len(fixed.Changes) == 0 {
		t.Fatalf("bad result: %+v", fixed)
	}
	if _, err = Repair(context.Background(), input, output, Options{}); err == nil {
		t.Fatal("existing output was overwritten")
	}
	if _, err = Repair(context.Background(), input, input, Options{}); err == nil {
		t.Fatal("original accepted as output")
	}
	z, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if z.File[0].Name != "mimetype" || z.File[0].Method != zip.Store || len(z.File[0].Extra) != 0 {
		t.Fatal("mimetype packaging not normalized")
	}
}

func TestMissingMimetypeCanBeRepaired(t *testing.T) {
	e := fixtureEntries()[1:]
	input := writeFixture(t, e)
	out := filepath.Join(filepath.Dir(input), "fixed.epub")
	r, err := Repair(context.Background(), input, out, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Report.Errors > 0 {
		t.Fatalf("issues: %+v", r.Report.Issues)
	}
}

func TestXMLSanitation(t *testing.T) {
	for _, tc := range []struct {
		label, input string
		repair       bool
	}{
		{"ampersand", `<p>Rock & roll</p>`, true},
		{"named entity", `<p>A&nbsp;B &mdash; C</p>`, true},
		{"control", string([]byte{'<', 'p', '>', 1, 'a', '<', '/', 'p', '>'}), true},
		{"unclosed", `<p><b>test</p>`, false},
		{"unknown entity", `<p>&unrecognized;</p>`, false},
		{"custom DTD", `<!DOCTYPE p [<!ENTITY custom "x">]><p>&custom;</p>`, false},
		{"valid CDATA", `<p><![CDATA[A & B]]></p>`, false},
		{"comment preserved", `<p><!-- A & B -->A & B</p>`, true},
	} {
		t.Run(tc.label, func(t *testing.T) {
			b, ok := sanitizeXML([]byte(tc.input))
			if ok != tc.repair {
				t.Fatalf("repair=%v expected %v: %s", ok, tc.repair, b)
			}
			if ok {
				if _, err := validateXML(b); err != nil {
					t.Fatal(err)
				}
			}
			if tc.label == "comment preserved" && !bytes.Contains(b, []byte("<!-- A & B -->")) {
				t.Fatal("comment changed")
			}
		})
	}
}

func TestXMLRepairRescansAndLeavesUnresolvedIssues(t *testing.T) {
	e := fixtureEntries()
	e[3].body = strings.Replace(e[3].body, "One two three.", "One & two&nbsp;three.", 1)
	e[3].body = strings.Replace(e[3].body, "</body>", `<img src="missing.png"/></body>`, 1)
	input := writeFixture(t, e)
	r, err := Repair(context.Background(), input, filepath.Join(filepath.Dir(input), "fixed.epub"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if hasIssue(r.Report, "XML_INVALID") || !hasIssue(r.Report, "LINK_MISSING") {
		t.Fatalf("unexpected repair outcome %+v", r.Report.Issues)
	}
	if len(r.Changes) != 2 {
		t.Fatalf("changes %+v", r.Changes)
	}
}

func TestBrokenReferencesAndMetadata(t *testing.T) {
	e := fixtureEntries()
	e[2].body = strings.Replace(e[2].body, `<dc:language>en</dc:language>`, "", 1)
	e[2].body = strings.Replace(e[2].body, `idref="ch1"`, `idref="absent"`, 1)
	e[3].body = strings.Replace(e[3].body, `</body>`, `<img src="missing.png"/><a href="chapter.xhtml#absent">broken</a></body>`, 1)
	r := scanFixture(t, e)
	for _, code := range []string{"METADATA_LANGUAGE", "SPINE_MISSING", "LINK_MISSING", "LINK_FRAGMENT"} {
		if !hasIssue(r, code) {
			t.Fatalf("missing %s: %+v", code, r.Issues)
		}
	}
}

func TestUnsafeAndDuplicatePathsRefuseRepair(t *testing.T) {
	for _, bad := range []string{"../outside.xhtml", "/absolute.xhtml", "C:/evil.xhtml", `OPS\evil.xhtml`, "OPS/chapter.xhtml"} {
		t.Run(bad, func(t *testing.T) {
			e := append(fixtureEntries(), fixtureEntry{bad, "evil", zip.Store})
			input := writeFixture(t, e)
			r, err := Scan(context.Background(), input, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if !hasIssue(r, "ZIP_UNSAFE_PATH") && !hasIssue(r, "ZIP_DUPLICATE") {
				t.Fatalf("unsafe entry not reported: %+v", r.Issues)
			}
			if _, err = Repair(context.Background(), input, filepath.Join(filepath.Dir(input), "out.epub"), Options{}); err == nil {
				t.Fatal("unsafe archive repaired")
			}
		})
	}
}

func TestCRCFailureRefusesRepair(t *testing.T) {
	e := fixtureEntries()
	e[3].method = zip.Store
	input := writeFixture(t, e)
	z, err := zip.OpenReader(input)
	if err != nil {
		t.Fatal(err)
	}
	offset, err := z.File[3].DataOffset()
	z.Close()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(input, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt([]byte("X"), offset+8); err != nil {
		t.Fatal(err)
	}
	f.Close()
	r, err := Scan(context.Background(), input, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(r, "ZIP_CRC") {
		t.Fatalf("CRC not detected: %+v", r.Issues)
	}
	if _, err = Repair(context.Background(), input, filepath.Join(filepath.Dir(input), "out.epub"), Options{}); err == nil {
		t.Fatal("corrupt payload repaired")
	}
}

func TestProtectionAndFontObfuscation(t *testing.T) {
	for _, tc := range []struct {
		name, alg string
		allow     bool
	}{
		{"idpf", "http://www.idpf.org/2008/embedding", true},
		{"adobe", "http://ns.adobe.com/pdf/enc#RC", true},
		{"DRM", "http://www.w3.org/2001/04/xmlenc#aes128-cbc", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := append(fixtureEntries(), fixtureEntry{"META-INF/encryption.xml", fmt.Sprintf(`<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><EncryptedData xmlns="http://www.w3.org/2001/04/xmlenc#"><EncryptionMethod Algorithm="%s"/><CipherData><CipherReference URI="OPS/font.otf"/></CipherData></EncryptedData></encryption>`, tc.alg), zip.Deflate})
			input := writeFixture(t, e)
			r, err := Scan(context.Background(), input, Options{})
			if err != nil {
				t.Fatal(err)
			}
			code := "DRM_ENCRYPTION"
			if tc.allow {
				code = "FONT_OBFUSCATION"
			}
			if !hasIssue(r, code) {
				t.Fatalf("missing %s", code)
			}
			_, err = Repair(context.Background(), input, filepath.Join(filepath.Dir(input), "out.epub"), Options{})
			if (err == nil) != tc.allow {
				t.Fatalf("repair error %v; allow=%v", err, tc.allow)
			}
		})
	}
	e := append(fixtureEntries(), fixtureEntry{"META-INF/signatures.xml", `<signatures/>`, zip.Deflate})
	input := writeFixture(t, e)
	if _, err := Repair(context.Background(), input, filepath.Join(filepath.Dir(input), "out.epub"), Options{}); err == nil {
		t.Fatal("signed book repaired")
	}
}

func TestVisibleWordsUnicodeAndSourcePages(t *testing.T) {
	e := fixtureEntries()
	e[3].body = `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Ignored</title></head><body><p>Hel<b>lo</b> naïve reader's 世界 日本語</p><p hidden="hidden">Not counted</p><p aria-hidden="true">Not counted either</p><script>Not counted script</script><nav>Not counted nav</nav><span epub:type="pagebreak" id="p1"/></body></html>`
	r := scanFixture(t, e)
	if r.Stats.Words != 8 {
		t.Fatalf("words=%d; expected Hello naïve reader's + 5 CJK characters", r.Stats.Words)
	}
	if r.Stats.SourcePages != 1 {
		t.Fatalf("source pages=%d", r.Stats.SourcePages)
	}
	e[4].body = strings.Replace(e[4].body, `</body>`, `<nav epub:type="page-list"><ol><li><a href="chapter.xhtml#p1">1</a></li><li><a href="chapter.xhtml#p1">2</a></li></ol></nav></body>`, 1)
	r = scanFixture(t, e)
	if r.Stats.SourcePages != 2 {
		t.Fatalf("page-list count=%d", r.Stats.SourcePages)
	}
}

func TestResourcesWithoutExtensions(t *testing.T) {
	e := fixtureEntries()
	for i := range e {
		e[i].name = strings.ReplaceAll(e[i].name, "book.opf", "package")
		e[i].name = strings.ReplaceAll(e[i].name, "chapter.xhtml", "chapter")
		e[i].name = strings.ReplaceAll(e[i].name, "nav.xhtml", "contents")
		e[i].body = strings.ReplaceAll(e[i].body, "book.opf", "package")
		e[i].body = strings.ReplaceAll(e[i].body, "chapter.xhtml", "chapter")
		e[i].body = strings.ReplaceAll(e[i].body, "nav.xhtml", "contents")
	}
	r := scanFixture(t, e)
	if r.Errors > 0 || r.Stats.Words != 5 {
		t.Fatalf("extensionless resources failed: %+v %+v", r.Issues, r.Stats)
	}
}

func TestUTF16Content(t *testing.T) {
	e := fixtureEntries()
	src := strings.Replace(e[3].body, `version="1.0"`, `version="1.0" encoding="UTF-16"`, 1)
	units := utf16.Encode([]rune(src))
	buf := bytes.NewBuffer([]byte{0xff, 0xfe})
	for _, u := range units {
		if err := binary.Write(buf, binary.LittleEndian, u); err != nil {
			t.Fatal(err)
		}
	}
	e[3].body = buf.String()
	r := scanFixture(t, e)
	if r.Errors > 0 || r.Stats.Words != 5 {
		t.Fatalf("UTF16 content failed: %+v %+v", r.Issues, r.Stats)
	}
}

func TestArchiveEntryLimitAndCancellation(t *testing.T) {
	e := fixtureEntries()
	for i := len(e); i <= maxEntries; i++ {
		e = append(e, fixtureEntry{fmt.Sprintf("files/%d", i), "", zip.Store})
	}
	input := writeFixture(t, e)
	r, err := Scan(context.Background(), input, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(r, "ZIP_ENTRY_LIMIT") {
		t.Fatal("entry limit not enforced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Scan(ctx, writeFixture(t, fixtureEntries()), Options{})
	if err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestTruncatedArchiveIsUnreadable(t *testing.T) {
	name := filepath.Join(t.TempDir(), "bad.epub")
	if err := os.WriteFile(name, []byte("PK not a zip"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Scan(context.Background(), name, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "unreadable" || !hasIssue(r, "ZIP_INVALID") {
		t.Fatalf("unexpected result %+v", r)
	}
}

func TestCorruptRasterPayloadWithValidZIPCRC(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	corrupt := data.Bytes()[:data.Len()-10]
	e := fixtureEntries()
	e[2].body = strings.Replace(e[2].body, "</manifest>", `<item id="image" href="image.png" media-type="image/png"/></manifest>`, 1)
	e = append(e, fixtureEntry{"OPS/image.png", string(corrupt), zip.Deflate})
	r := scanFixture(t, e)
	if hasIssue(r, "ZIP_CRC") || !hasIssue(r, "IMAGE_DECODE") {
		t.Fatalf("pixel corruption should be detected after good archive CRC: %+v", r.Issues)
	}
}

func TestObfuscatedFontRepairPreservesPackageBytes(t *testing.T) {
	e := fixtureEntries()
	e[2].body = strings.Replace(e[2].body, "urn:uuid:test", "urn:uuid:test&key", 1)
	opf := e[2].body
	e = append(e, fixtureEntry{"META-INF/encryption.xml", `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><EncryptedData xmlns="http://www.w3.org/2001/04/xmlenc#"><EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/></EncryptedData></encryption>`, zip.Deflate})
	input := writeFixture(t, e)
	output := filepath.Join(filepath.Dir(input), "repaired.epub")
	if _, err := Repair(context.Background(), input, output, Options{}); err != nil {
		t.Fatal(err)
	}
	r := Report{}
	a, err := openArchive(context.Background(), output, &r)
	if err != nil {
		t.Fatal(err)
	}
	defer a.z.Close()
	if string(a.text["OPS/book.opf"]) != opf {
		t.Fatal("repair changed metadata used for font obfuscation")
	}
}

func TestStylesheetReferencesExcludeComments(t *testing.T) {
	e := fixtureEntries()
	e[2].body = strings.Replace(e[2].body, "</manifest>", `<item id="css" href="styles.css" media-type="text/css"/></manifest>`, 1)
	e = append(e, fixtureEntry{"OPS/styles.css", `/* background:url(missing.png) */ p { color: red; }`, zip.Deflate})
	r := scanFixture(t, e)
	if hasIssue(r, "LINK_MISSING") {
		t.Fatalf("commented URL flagged: %+v", r.Issues)
	}
	e[3].body = strings.Replace(e[3].body, "<body>", `<body style="background-image:url(missing.png)">`, 1)
	r = scanFixture(t, e)
	if !hasIssue(r, "LINK_MISSING") {
		t.Fatal("inline style resource was not checked")
	}
}
