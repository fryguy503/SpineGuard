package reader

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

func readerFixture() map[string]string {
	return map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OPS/book.opf":           `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Reader &amp; Inspection</dc:title><dc:creator>A Writer</dc:creator><dc:identifier id="uid">urn:uuid:00112233-4455-6677-8899-aabbccddeeff</dc:identifier><meta property="rendition:layout">pre-paginated</meta></metadata><manifest><item id="a" href="index.html" media-type="application/xhtml+xml"/><item id="b" href="second.xhtml" media-type="application/xhtml+xml"/><item id="missing" href="missing.xhtml" media-type="application/xhtml+xml"/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="css" href="style.css" media-type="text/css"/><item id="svg" href="cover.svg" media-type="image/svg+xml"/></manifest><spine page-progression-direction="rtl"><itemref idref="b"/><itemref idref="missing"/><itemref idref="a"/></spine></package>`,
		"OPS/index.html":         `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Title Secretword</title><style>.Secretword{color:red}</style><link rel="stylesheet" href="style.css"/><script>Secretword;window.alert('bad')</script></head><body><p id="target">A red fox jumps. A red fox sleeps.</p><a href="missing.xhtml#bad">Broken reference</a><img src="cover.svg"/><img src="https://example.com/tracker"/></body></html>`,
		"OPS/second.xhtml":       `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Second</title></head><body><p>Second chapter has a red fox.</p></body></html>`,
		"OPS/nav.xhtml":          `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="second.xhtml">Second first</a><ol><li><a href="index.html#target">Nested first</a></li></ol></li><li><a href="missing.xhtml">Missing chapter</a></li></ol></nav></body></html>`,
		"OPS/style.css":          `body{background:url(https://example.com/tracker)}p{color:red}`,
		"OPS/cover.svg":          `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="red"/><script>alert('bad')</script></svg>`,
	}
}
func writeReaderFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "book.epub")
	f, e := os.Create(filename)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	for name, data := range files {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = io.WriteString(w, data); e != nil {
			t.Fatal(e)
		}
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return filename
}
func byPath(t *testing.T, b Book, name string) Resource {
	t.Helper()
	for _, r := range b.Resources {
		if r.Path == name {
			return r
		}
	}
	t.Fatalf("missing fixture resource %s", name)
	return Resource{}
}
func response(m *Manager, method, url string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest(method, url, nil))
	return w
}
func openFixture(t *testing.T, issues []epub.Issue) (*Manager, Book, string) {
	t.Helper()
	filename := writeReaderFixture(t, readerFixture())
	m := New(t.TempDir())
	t.Cleanup(m.Shutdown)
	b, e := m.Open(context.Background(), filename, issues)
	if e != nil {
		t.Fatal(e)
	}
	return m, b, filename
}

func TestReaderPublicationOrderTOCAndOpaqueRoutes(t *testing.T) {
	m, b, _ := openFixture(t, nil)
	if b.Title != "Reader & Inspection" || b.Author != "A Writer" || b.Direction != "rtl" || b.Layout != "pre-paginated" {
		t.Fatalf("metadata: %+v", b)
	}
	if len(b.Spine) != 3 || b.Spine[0].Path != "OPS/second.xhtml" || !b.Spine[1].Missing || b.Spine[2].Path != "OPS/index.html" {
		t.Fatalf("spine: %+v", b.Spine)
	}
	if len(b.TOC) != 2 || b.TOC[0].Label != "Second first" || len(b.TOC[0].Children) != 1 || b.TOC[0].Children[0].Fragment != "target" || !b.TOC[1].Missing {
		t.Fatalf("toc: %+v", b.TOC)
	}
	for _, r := range b.Resources {
		if strings.Contains(r.URL, "index.html") || !strings.HasPrefix(r.URL, Prefix+b.SessionID+"/r/") {
			t.Fatalf("unsafe resource alias %s", r.URL)
		}
	}
	packageResponse := response(m, "GET", b.PackageURL)
	if packageResponse.Code != 200 {
		t.Fatalf("package %d %s", packageResponse.Code, packageResponse.Body)
	}
	var pkg packageInfo
	if e := xml.Unmarshal(packageResponse.Body.Bytes(), &pkg); e != nil {
		t.Fatal(e)
	}
	if len(pkg.Spine.Items) < 2 || pkg.Spine.Items[0].ID != b.Spine[0].ID || pkg.Spine.Items[1].ID != b.Spine[2].ID {
		t.Fatalf("synthetic spine: %+v", pkg.Spine)
	}
	chapter := byPath(t, b, "OPS/index.html")
	w := response(m, "GET", chapter.URL)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'none'") {
		t.Fatalf("chapter response: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "<script") || strings.Contains(w.Body.String(), `src="https://`) {
		t.Fatalf("active resource: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), byPath(t, b, "OPS/style.css").URL) {
		t.Fatal("local stylesheet not rewritten")
	}
	svg := byPath(t, b, "OPS/cover.svg")
	if !strings.Contains(w.Body.String(), Prefix+b.SessionID+"/r/"+svg.ID+".svg") {
		t.Fatal("SVG image alias missing")
	}
	svgPage := response(m, "GET", svg.URL)
	if !strings.Contains(svgPage.Header().Get("Content-Type"), "application/xhtml+xml") || !strings.Contains(svgPage.Body.String(), "<svg") {
		t.Fatalf("SVG page: %s", svgPage.Body)
	}
	svgImage := response(m, "GET", Prefix+b.SessionID+"/r/"+svg.ID+".svg")
	if !strings.Contains(svgImage.Header().Get("Content-Type"), "image/svg+xml") || strings.Contains(svgImage.Body.String(), "<html") {
		t.Fatalf("SVG image: %s", svgImage.Body)
	}
	css := response(m, "GET", byPath(t, b, "OPS/style.css").URL)
	if strings.Contains(css.Body.String(), "example.com") {
		t.Fatalf("remote CSS resource remained: %s", css.Body)
	}
	for _, url := range []string{Prefix + b.SessionID + "/index.html", Prefix + b.SessionID + "/", Prefix + b.SessionID + "/r/unknown.xhtml", Prefix + b.SessionID + "/r/" + chapter.ID + ".html", chapter.URL + "?raw=1", Prefix + "expired/package.opf"} {
		if w := response(m, "GET", url); w.Code != 404 {
			t.Fatalf("unknown route %s gave %d", url, w.Code)
		}
	}
	if w := response(m, "POST", chapter.URL); w.Code != 405 {
		t.Fatalf("POST = %d", w.Code)
	}
	if w := response(m, "HEAD", chapter.URL); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD = %d/%d", w.Code, w.Body.Len())
	}
}

func TestReaderSearchAndConcernOriginSource(t *testing.T) {
	issues := []epub.Issue{{Code: "LINK_MISSING", File: "OPS/index.html", Reference: "missing.xhtml#bad", Detail: "missing.xhtml#bad resolves to OPS/missing.xhtml"}, {Code: "METADATA_LANGUAGE", File: "OPS/book.opf"}, {Code: "LINK_MISSING", File: "OPS/index.html", Detail: "missing.xhtml#bad resolves to OPS/missing.xhtml"}, {Code: "CONTENT_DUPLICATE_ID", File: "OPS/index.html", Detail: "Links to this identifier may be ambiguous: target"}}
	m, b, _ := openFixture(t, issues)
	hits, e := m.Search(context.Background(), b.SessionID, "red fox", 100)
	if e != nil || len(hits) != 3 {
		t.Fatalf("search: %+v %v", hits, e)
	}
	hits, e = m.Search(context.Background(), b.SessionID, "Secretword", 100)
	if e != nil || len(hits) != 0 {
		t.Fatalf("head/script/style searched: %+v %v", hits, e)
	}
	for _, index := range []int{0, 2} {
		loc, e := m.LocateIssue(context.Background(), b.SessionID, index)
		if e != nil || loc.ResourceID != byPath(t, b, "OPS/index.html").ID || loc.Mode != "page" || loc.Reference != "missing.xhtml#bad" || loc.Column < 100 {
			t.Fatalf("concern origin: %+v %v", loc, e)
		}
	}
	loc, e := m.LocateIssue(context.Background(), b.SessionID, 1)
	if e != nil || loc.Mode != "source" || loc.ResourceID != byPath(t, b, "OPS/book.opf").ID {
		t.Fatalf("metadata concern: %+v %v", loc, e)
	}
	loc, e = m.LocateIssue(context.Background(), b.SessionID, 3)
	if e != nil || loc.Fragment != "target" || !strings.HasSuffix(loc.Href, "#target") {
		t.Fatalf("duplicate locator: %+v %v", loc, e)
	}
	source, e := m.Source(context.Background(), b.SessionID, byPath(t, b, "OPS/index.html").ID, 1, 1, 200)
	if e != nil || !strings.Contains(source.Content, "<script>") || source.Truncated || source.StartColumn != 1 {
		t.Fatalf("original source: %+v %v", source, e)
	}
}

func TestReaderHugeSourceCentersAtColumn(t *testing.T) {
	files := readerFixture()
	files["OPS/index.html"] = `<html><head></head><body><p>` + strings.Repeat("x", 210000) + `</p><a href="missing.xhtml#bad">Concern</a>` + strings.Repeat("z", 210000) + `</body></html>`
	m := New("")
	defer m.Shutdown()
	b, e := m.Open(context.Background(), writeReaderFixture(t, files), []epub.Issue{{Code: "LINK_MISSING", File: "OPS/index.html", Reference: "missing.xhtml#bad"}})
	if e != nil {
		t.Fatal(e)
	}
	loc, e := m.LocateIssue(context.Background(), b.SessionID, 0)
	if e != nil || loc.Column < 210000 {
		t.Fatalf("location %+v %v", loc, e)
	}
	src, e := m.Source(context.Background(), b.SessionID, loc.ResourceID, loc.Line, loc.Column, 200)
	if e != nil || !src.Truncated || src.StartColumn <= 1 || len(src.Content) > 128<<10 || !strings.Contains(src.Content, "Concern") {
		t.Fatalf("centered source %+v %v", src, e)
	}
}

func TestReaderLifecycleStateAndNoBookMutations(t *testing.T) {
	m, b, filename := openFixture(t, nil)
	before, e := os.ReadFile(filename)
	if e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(filename)
	state := ReadingState{Location: "epubcfi(/6/2!/4/1:3)", Bookmarks: []Bookmark{{ID: "mark1", Location: "epubcfi(/6/2!/4/1:3)", Label: "Start", Note: "Read again", CreatedAt: time.Now().UTC().Format(time.RFC3339)}}}
	if e = m.SaveState(b.SessionID, state); e != nil {
		t.Fatal(e)
	}
	state.Bookmarks[0].Label = "mutated"
	if e = m.Close(b.SessionID); e != nil {
		t.Fatal(e)
	}
	if response(m, "GET", b.PackageURL).Code != 404 {
		t.Fatal("closed resource still available")
	}
	b2, e := m.Open(context.Background(), filename, nil)
	if e != nil {
		t.Fatal(e)
	}
	if b2.SessionID == b.SessionID || b2.BookKey != b.BookKey || b2.State.Bookmarks[0].Label != "Start" {
		t.Fatalf("resume: %+v", b2.State)
	}
	if e = m.Close(b.SessionID); e != nil {
		t.Fatal(e)
	}
	if response(m, "GET", b2.PackageURL).Code != 200 {
		t.Fatal("stale close affected new session")
	}
	m2 := New(m.dir)
	defer m2.Shutdown()
	b3, e := m2.Open(context.Background(), filename, nil)
	if e != nil || b3.State.Location != b2.State.Location {
		t.Fatalf("persisted resume: %+v %v", b3.State, e)
	}
	after, _ := os.ReadFile(filename)
	afterInfo, _ := os.Stat(filename)
	if !bytes.Equal(before, after) || !info.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("reader modified the original book")
	}
	if e = m.SaveState(b2.SessionID, ReadingState{Location: strings.Repeat("x", 4097)}); e == nil {
		t.Fatal("unbounded state accepted")
	}
	if e = os.Chtimes(filename, time.Now(), info.ModTime().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if w := response(m, "GET", b2.PackageURL); w.Code != http.StatusConflict {
		t.Fatalf("stale file request = %d", w.Code)
	}
}

func TestReaderNCXMissingAndMalformedPackageInspection(t *testing.T) {
	files := readerFixture()
	files["OPS/book.opf"] = `<package xmlns="http://www.idpf.org/2007/opf"><metadata/><manifest><item id="a" href="index.html" media-type="application/xhtml+xml"/><item id="n" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest><spine toc="n"><itemref idref="a"/></spine></package>`
	files["OPS/toc.ncx"] = `<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap><navPoint><navLabel><text>NCX chapter</text></navLabel><content src="index.html#target"/><navPoint><navLabel><text>Nested</text></navLabel><content src="second.xhtml"/></navPoint></navPoint></navMap></ncx>`
	m := New("")
	defer m.Shutdown()
	b, e := m.Open(context.Background(), writeReaderFixture(t, files), nil)
	if e != nil || len(b.TOC) != 1 || b.TOC[0].Label != "NCX chapter" || len(b.TOC[0].Children) != 1 {
		t.Fatalf("NCX %+v %v", b.TOC, e)
	}
	files["OPS/book.opf"] = "<package><broken"
	files["OPS/index.html"] = "<html><body><p>Malformed but inspectable"
	b, e = m.Open(context.Background(), writeReaderFixture(t, files), nil)
	if e != nil || len(b.Spine) != 0 || len(b.Warnings) == 0 {
		t.Fatalf("malformed package %+v %v", b, e)
	}
	if w := response(m, "GET", byPath(t, b, "OPS/index.html").URL); w.Code != 200 || !strings.Contains(w.Body.String(), "Malformed but inspectable") {
		t.Fatalf("malformed chapter: %d %s", w.Code, w.Body)
	}
}

func TestReaderEPUBCheckConcernUsesExactSourceLine(t *testing.T) {
	files := readerFixture()
	files["OPS/index.html"] = "<html><body>\n<p>First paragraph</p>\n<p>Inspect this source line</p>\n</body></html>"
	m := New("")
	defer m.Shutdown()
	b, err := m.Open(context.Background(), writeReaderFixture(t, files), []epub.Issue{{Code: "EPUBCHECK-RSC-005", File: "OPS/index.html", Line: 3}})
	if err != nil {
		t.Fatal(err)
	}
	loc, err := m.LocateIssue(context.Background(), b.SessionID, 0)
	if err != nil || loc.Mode != "source" || loc.Line != 3 {
		t.Fatalf("source concern: %+v %v", loc, err)
	}
	src, err := m.Source(context.Background(), b.SessionID, loc.ResourceID, loc.Line, loc.Column, 200)
	if err != nil || !strings.Contains(src.Content, "Inspect this source line") {
		t.Fatalf("original source: %+v %v", src, err)
	}
}

func TestReaderUnsafeArchivesAndCancellation(t *testing.T) {
	for _, name := range []string{"../escape.xhtml", "/absolute.xhtml", "OPS/../alias.xhtml", `OPS\windows.xhtml`, "C:/drive.xhtml"} {
		t.Run(name, func(t *testing.T) {
			files := readerFixture()
			files[name] = "bad"
			m := New("")
			defer m.Shutdown()
			if _, e := m.Open(context.Background(), writeReaderFixture(t, files), nil); e == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
	filename := filepath.Join(t.TempDir(), "duplicate.epub")
	f, _ := os.Create(filename)
	z := zip.NewWriter(f)
	for i := 0; i < 2; i++ {
		w, _ := z.Create("same.xhtml")
		io.WriteString(w, "duplicate")
	}
	z.Close()
	f.Close()
	m := New("")
	defer m.Shutdown()
	if _, e := m.Open(context.Background(), filename, nil); e == nil {
		t.Fatal("duplicate path accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.Open(ctx, writeReaderFixture(t, readerFixture()), nil); e != context.Canceled {
		t.Fatalf("cancellation = %v", e)
	}
}

func TestReaderKnownFontObfuscationAndUnsupportedProtection(t *testing.T) {
	files := readerFixture()
	plain := bytes.Repeat([]byte{1, 3, 5, 7, 9}, 300)
	encoded := append([]byte{}, plain...)
	key := sha1.Sum([]byte("urn:uuid:00112233-4455-6677-8899-aabbccddeeff"))
	for i := 0; i < 1040; i++ {
		encoded[i] ^= key[i%len(key)]
	}
	files["OPS/font.ttf"] = string(encoded)
	files["META-INF/encryption.xml"] = `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><EncryptedData xmlns="http://www.w3.org/2001/04/xmlenc#"><EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/><CipherData><CipherReference URI="OPS/font.ttf"/></CipherData></EncryptedData></encryption>`
	m := New("")
	defer m.Shutdown()
	b, e := m.Open(context.Background(), writeReaderFixture(t, files), nil)
	if e != nil {
		t.Fatal(e)
	}
	font := byPath(t, b, "OPS/font.ttf")
	w := response(m, "GET", font.URL)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), plain) {
		t.Fatalf("font deobfuscation %d %d", w.Code, w.Body.Len())
	}
	files["META-INF/encryption.xml"] = strings.ReplaceAll(files["META-INF/encryption.xml"], "http://www.idpf.org/2008/embedding", "unknown-protection")
	b, e = m.Open(context.Background(), writeReaderFixture(t, files), nil)
	if e != nil || len(b.Warnings) == 0 || !byPath(t, b, "OPS/font.ttf").Missing {
		t.Fatalf("unsupported protection %+v %v", b.Warnings, e)
	}
	if response(m, "GET", byPath(t, b, "OPS/font.ttf").URL).Code != 404 {
		t.Fatal("encrypted unknown font served")
	}
}

func TestReaderLatestOpenIntentAndSearchCancellation(t *testing.T) {
	filename := writeReaderFixture(t, readerFixture())
	m := New("")
	defer m.Shutdown()
	m.openMu.Lock()
	first := make(chan error, 1)
	second := make(chan Book, 1)
	secondErr := make(chan error, 1)
	go func() { _, e := m.Open(context.Background(), filename, nil); first <- e }()
	waitIntent := func(want uint64) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			m.mu.Lock()
			n := m.intent
			m.mu.Unlock()
			if n >= want {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("open intent not registered")
	}
	waitIntent(1)
	go func() { b, e := m.Open(context.Background(), filename, nil); second <- b; secondErr <- e }()
	waitIntent(2)
	m.openMu.Unlock()
	if e := <-first; e != context.Canceled {
		t.Fatalf("stale open = %v", e)
	}
	b := <-second
	if e := <-secondErr; e != nil {
		t.Fatal(e)
	}
	s, e := m.get(b.SessionID)
	if e != nil {
		t.Fatal(e)
	}
	ctx, done := s.searchOperation(context.Background())
	m.CancelSearch(b.SessionID)
	if ctx.Err() != context.Canceled {
		t.Fatal("search did not cancel")
	}
	done()
	ctx, done = s.searchOperation(context.Background())
	ctx2, done2 := s.searchOperation(context.Background())
	if ctx.Err() != context.Canceled {
		t.Fatal("new search did not cancel old search")
	}
	done()
	m.Close(b.SessionID)
	select {
	case <-ctx2.Done():
	case <-time.After(time.Second):
		t.Fatal("close did not cancel search")
	}
	done2()
}

func TestReaderZIPHeaderGuardsCRCAndSymlinks(t *testing.T) {
	for _, kind := range []string{"symlink", "encrypted", "entrysize", "directorysize", "directorycount", "crc"} {
		t.Run(kind, func(t *testing.T) {
			filename := writeReaderFixture(t, readerFixture())
			if kind == "symlink" {
				f, e := os.Create(filename)
				if e != nil {
					t.Fatal(e)
				}
				z := zip.NewWriter(f)
				header := &zip.FileHeader{Name: "link.xhtml"}
				header.SetMode(os.ModeSymlink | 0777)
				w, e := z.CreateHeader(header)
				if e != nil {
					t.Fatal(e)
				}
				io.WriteString(w, "outside.xhtml")
				z.Close()
				f.Close()
			} else {
				data, e := os.ReadFile(filename)
				if e != nil {
					t.Fatal(e)
				}
				at := bytes.Index(data, []byte("PK\x01\x02"))
				if at < 0 {
					t.Fatal("missing directory")
				}
				switch kind {
				case "encrypted":
					binary.LittleEndian.PutUint16(data[at+8:at+10], 1)
				case "entrysize":
					binary.LittleEndian.PutUint32(data[at+24:at+28], maxEntry+1)
				case "directorysize":
					end := bytes.LastIndex(data, []byte("PK\x05\x06"))
					binary.LittleEndian.PutUint32(data[end+12:end+16], 33<<20)
				case "directorycount":
					end := bytes.LastIndex(data, []byte("PK\x05\x06"))
					binary.LittleEndian.PutUint16(data[end+8:end+10], 1)
					binary.LittleEndian.PutUint16(data[end+10:end+12], 1)
				case "crc":
					for at >= 0 {
						n := int(binary.LittleEndian.Uint16(data[at+28 : at+30]))
						if string(data[at+46:at+46+n]) == "OPS/index.html" {
							binary.LittleEndian.PutUint32(data[at+16:at+20], 0x12345678)
							break
						}
						next := bytes.Index(data[at+4:], []byte("PK\x01\x02"))
						if next < 0 {
							t.Fatal("chapter directory entry missing")
						}
						at += next + 4
					}
				}
				if e = os.WriteFile(filename, data, 0600); e != nil {
					t.Fatal(e)
				}
			}
			m := New("")
			defer m.Shutdown()
			b, e := m.Open(context.Background(), filename, nil)
			if kind != "crc" {
				if e == nil {
					t.Fatal("unsafe archive accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if w := response(m, "GET", byPath(t, b, "OPS/index.html").URL); w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("CRC resource = %d", w.Code)
			}
		})
	}
}

func TestReaderNavigationPreflightBounds(t *testing.T) {
	s := &session{}
	src := []byte(`<html><body><nav epub:type="toc"><ol>` + strings.Repeat(`<li></li>`, 150000) + `</ol></nav></body></html>`)
	if got := s.xhtmlTOC(context.Background(), "nav.xhtml", src); len(got) != 0 {
		t.Fatal("oversized navigation tree was accepted")
	}
}

func TestReaderLinkConcernDoesNotJumpToDestinationAnchorInSource(t *testing.T) {
	files := readerFixture()
	files["OPS/index.html"] = `<html><head></head><body><h1 id="note">Unrelated existing anchor</h1><a href="missing.xhtml#note">The broken source link</a></body></html>`
	m := New("")
	defer m.Shutdown()
	b, e := m.Open(context.Background(), writeReaderFixture(t, files), []epub.Issue{{Code: "LINK_MISSING", File: "OPS/index.html", Reference: "missing.xhtml#note", Anchor: "note"}})
	if e != nil {
		t.Fatal(e)
	}
	loc, e := m.LocateIssue(context.Background(), b.SessionID, 0)
	if e != nil || loc.Fragment != "" || strings.Contains(loc.Href, "#") || loc.Reference != "missing.xhtml#note" || loc.Column != strings.Index(files["OPS/index.html"], "missing.xhtml#note")+1 {
		t.Fatalf("source link confused with destination anchor: %+v %v", loc, e)
	}
}

func TestReaderSearchMalformedFragmentAndInlineWords(t *testing.T) {
	files := readerFixture()
	files["OPS/index.html"] = `<p>co<em>oper</em>ate with a silver <b>river</b> phrase</p>`
	m := New("")
	defer m.Shutdown()
	b, e := m.Open(context.Background(), writeReaderFixture(t, files), nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{"cooperate", "silver river phrase"} {
		results, e := m.Search(context.Background(), b.SessionID, query, 100)
		if e != nil || len(results) != 1 {
			t.Fatalf("fragment search %q: %+v %v", query, results, e)
		}
	}
}

func TestReaderSyntheticPackageKeepsPublicationLanguage(t *testing.T) {
	for _, language := range []string{"", "  fr-CA  ", "ar"} {
		t.Run(language, func(t *testing.T) {
			files := readerFixture()
			files["OPS/book.opf"] = strings.Replace(files["OPS/book.opf"], `</metadata>`, `<dc:language>`+language+`</dc:language></metadata>`, 1)
			m := New("")
			defer m.Shutdown()
			b, e := m.Open(context.Background(), writeReaderFixture(t, files), nil)
			if e != nil {
				t.Fatal(e)
			}
			var pkg packageInfo
			w := response(m, "GET", b.PackageURL)
			if e = xml.Unmarshal(w.Body.Bytes(), &pkg); e != nil {
				t.Fatal(e)
			}
			want := strings.TrimSpace(language)
			if want == "" {
				want = "und"
			}
			if len(pkg.Metadata.Languages) != 1 || pkg.Metadata.Languages[0].Text != want {
				t.Fatalf("synthetic language %+v; want %s", pkg.Metadata.Languages, want)
			}
		})
	}
}

func TestReaderShutdownCannotBeReopened(t *testing.T) {
	m := New("")
	m.Shutdown()
	if _, e := m.Open(context.Background(), writeReaderFixture(t, readerFixture()), nil); e == nil {
		t.Fatal("shutdown manager was reopened")
	}
}

func TestReaderLocalQueryAndLiteralPlusAnchor(t *testing.T) {
	files := readerFixture()
	files["OPS/index.html"] = strings.Replace(files["OPS/index.html"], `href="style.css"`, `href="style.css?cache=1&amp;mode=reader"`, 1)
	files["OPS/index.html"] = strings.Replace(files["OPS/index.html"], `id="target"`, `id="target+value"`, 1)
	files["OPS/nav.xhtml"] = strings.Replace(files["OPS/nav.xhtml"], `index.html#target`, `index.html#target%2Bvalue`, 1)
	m := New("")
	defer m.Shutdown()
	b, e := m.Open(context.Background(), writeReaderFixture(t, files), nil)
	if e != nil {
		t.Fatal(e)
	}
	w := response(m, "GET", byPath(t, b, "OPS/index.html").URL)
	if w.Code != 200 || !strings.Contains(w.Body.String(), byPath(t, b, "OPS/style.css").URL) {
		t.Fatal("known stylesheet with static query was lost")
	}
	entry := b.TOC[0].Children[0]
	if entry.Fragment != "target+value" || !strings.HasSuffix(entry.Href, "#target+value") {
		t.Fatalf("literal plus fragment changed: %+v", entry)
	}
}
