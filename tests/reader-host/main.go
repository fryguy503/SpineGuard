// Command reader-host is a development-only browser-test host. It is never
// embedded in SpineGuard. It exposes only synthetic books created below.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fryguy503/SpineGuard/internal/epub"
	"github.com/fryguy503/SpineGuard/internal/reader"
)

type fixture struct {
	Kind   string
	Path   string
	Bytes  []byte
	Report epub.Report
}

type host struct {
	mu       sync.Mutex
	manager  *reader.Manager
	root     string
	frontend string
	books    map[string]*fixture
	server   *http.Server
	done     chan struct{}
}

func fatal(err error) {
	if err != nil {
		panic(err)
	}
}

func bookBytes(kind string) []byte {
	if kind == "invalid" {
		return []byte("This synthetic fixture is not a ZIP archive.")
	}
	legacy := kind == "legacy"
	title := map[string]string{"normal": "Reader Fixture", "legacy": "Legacy Reader Fixture", "hostile": "Hostile Reader Fixture", "malformed": "Malformed Reader Fixture"}[kind]
	version, language := "3.0", ""
	if legacy {
		version, language = "2.0", `<dc:language>en</dc:language>`
	}
	manifestNav := `<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`
	spineAttrs := ""
	if legacy {
		manifestNav, spineAttrs = `<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`, ` toc="ncx"`
	}
	opf := `<?xml version="1.0" encoding="UTF-8"?><package xmlns="http://www.idpf.org/2007/opf" version="` + version + `" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + title + `</dc:title><dc:creator>SpineGuard Test Fixtures</dc:creator><dc:identifier id="uid">urn:uuid:spineguard-reader-fixture</dc:identifier>` + language + `<meta property="dcterms:modified">2026-11-11T00:00:00Z</meta></metadata><manifest><item id="one" href="Text/chapter-one.xhtml" media-type="application/xhtml+xml"/><item id="two" href="Text/chapter-two.xhtml" media-type="application/xhtml+xml"/><item id="style" href="Styles/book.css" media-type="text/css"/><item id="image" href="Images/cover.png" media-type="image/png" properties="cover-image"/>` + manifestNav + `</manifest><spine` + spineAttrs + `><itemref idref="one"/><itemref idref="two"/></spine></package>`
	var prose strings.Builder
	for i := 0; i < 110; i++ {
		fmt.Fprintf(&prose, "<p class=\"publisher\">Paragraph %d. A reader can follow the trail through a quiet valley, past the old bridge, and into the next chapter. These words exist only in a synthetic test book.</p>\n", i+1)
	}
	head := `<title>First chapter</title><link rel="stylesheet" href="../Styles/book.css"/>`
	body := `<h1 id="one">First chapter</h1><p class="publisher" id="publisher-styled">Publisher styles are retained.</p><img id="local-image" src="../Images/cover.png" alt="Synthetic cover"/><p id="concern">A missing image follows.</p><img src="missing-image.png" alt="Unavailable image"/><p><a id="internal-link" href="chapter-two.xhtml#two">Continue to second chapter</a></p>` + prose.String()
	if kind == "hostile" {
		head += `<base href="https://outside.invalid/"/><meta http-equiv="refresh" content="0;url=https://outside.invalid/refresh"/><script>window.__bookExecuted=true;parent.__bookExecuted=true;parent.go.main.App.DangerousBookCall();</script><style>@import "https://outside.invalid/import.css";body{background-image:url(https://outside.invalid/background.png)}p{--leak:url(https://outside.invalid/custom.png);background-image:var(--leak)}</style>`
		body = `<h1 id="one">Hostile content remains readable</h1><p id="hostile-safe-text">This safe text must remain.</p><img src="../Images/cover.png" alt="Safe local image" onload="parent.__bookExecuted=true"/><img src="https://outside.invalid/leak.png" onerror="parent.__bookExecuted=true"/><a id="unsafe-action" href="javascript:parent.__bookExecuted=true">Unsafe action</a><a id="external-action" href="https://outside.invalid/navigation">External action</a><iframe src="https://outside.invalid/frame"></iframe><object data="https://outside.invalid/object"></object><form action="https://outside.invalid/post"><button>Submit</button></form><svg xmlns="http://www.w3.org/2000/svg" onload="parent.__bookExecuted=true"><script>parent.__bookExecuted=true</script><foreignObject><iframe xmlns="http://www.w3.org/1999/xhtml" src="https://outside.invalid/nested"></iframe></foreignObject><animate attributeName="href" values="javascript:parent.__bookExecuted=true"/></svg>`
	}
	one := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<html xmlns=\"http://www.w3.org/1999/xhtml\"><head>" + head + "</head>\n<body>" + body + "</body></html>"
	if kind == "malformed" {
		one = `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Broken source</title></head><body><h1 id="one">Broken original markup<p>Text survives a malformed document.<p id="concern">Original source stays unmodified.`
	}
	two := `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Second chapter</title><link rel="stylesheet" href="../Styles/book.css"/></head><body><h1 id="two">Second chapter</h1><p id="search-hit">The unique silver river phrase belongs in the second chapter.</p><p>Final words in the synthetic reader fixture.</p><a href="chapter-one.xhtml#one">Return to first chapter</a></body></html>`
	nav := `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="Text/chapter-one.xhtml#one">First chapter</a></li><li><a href="Text/chapter-two.xhtml#two">Second chapter</a></li></ol></nav></body></html>`
	ncx := `<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head><meta name="dtb:uid" content="urn:uuid:spineguard-reader-fixture"/></head><docTitle><text>Legacy Reader Fixture</text></docTitle><navMap><navPoint id="n1" playOrder="1"><navLabel><text>First chapter</text></navLabel><content src="Text/chapter-one.xhtml#one"/></navPoint><navPoint id="n2" playOrder="2"><navLabel><text>Second chapter</text></navLabel><content src="Text/chapter-two.xhtml#two"/></navPoint></navMap></ncx>`
	var picture bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 36, 54))
	for y := 0; y < 54; y++ {
		for x := 0; x < 36; x++ {
			img.Set(x, y, color.RGBA{R: 35, G: 122, B: 104, A: 255})
		}
	}
	fatal(png.Encode(&picture, img))
	entries := []struct {
		name    string
		content []byte
	}{
		{"mimetype", []byte("application/epub+zip")},
		{"META-INF/container.xml", []byte(`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)},
		{"OPS/book.opf", []byte(opf)},
		{"OPS/Text/chapter-one.xhtml", []byte(one)},
		{"OPS/Text/chapter-two.xhtml", []byte(two)},
		{"OPS/Styles/book.css", []byte(`.publisher{color:rgb(24,72,96);text-indent:1.5em}body{font-family:serif}h1{font-size:2em}img{max-width:50%;max-height:180px}`)},
		{"OPS/Images/cover.png", picture.Bytes()},
	}
	if legacy {
		entries = append(entries, struct {
			name    string
			content []byte
		}{"OPS/toc.ncx", []byte(ncx)})
	} else {
		entries = append(entries, struct {
			name    string
			content []byte
		}{"OPS/nav.xhtml", []byte(nav)})
	}
	var output bytes.Buffer
	z := zip.NewWriter(&output)
	for i, entry := range entries {
		method := uint16(zip.Deflate)
		if i == 0 {
			method = zip.Store
		}
		header := &zip.FileHeader{Name: entry.name, Method: method}
		header.SetMode(0644)
		w, err := z.CreateHeader(header)
		fatal(err)
		_, err = w.Write(entry.content)
		fatal(err)
	}
	fatal(z.Close())
	return output.Bytes()
}

func (h *host) reset() error {
	if h.manager != nil {
		h.manager.Shutdown()
	}
	state, err := os.MkdirTemp(h.root, "state-")
	if err != nil {
		return err
	}
	h.manager = reader.New(state)
	h.books = map[string]*fixture{}
	for _, kind := range []string{"normal", "legacy", "hostile", "malformed", "invalid"} {
		data := bookBytes(kind)
		name := filepath.Join(h.root, kind+".epub")
		if err := os.WriteFile(name, data, 0600); err != nil {
			return err
		}
		report, err := epub.Scan(context.Background(), name, epub.Options{})
		if err != nil {
			return err
		}
		if report.Title == "" {
			report.Title = "Unreadable Reader Fixture"
		}
		h.books[kind] = &fixture{Kind: kind, Path: name, Bytes: data, Report: report}
	}
	return nil
}

func jsonResult(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func decodeArgs(r *http.Request) (string, []json.RawMessage, error) {
	var request struct {
		Method string            `json:"method"`
		Args   []json.RawMessage `json:"args"`
	}
	err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&request)
	return request.Method, request.Args, err
}

func arg[T any](args []json.RawMessage, index int) (T, error) {
	var value T
	if index >= len(args) {
		return value, fmt.Errorf("missing argument %d", index)
	}
	err := json.Unmarshal(args[index], &value)
	return value, err
}

func (h *host) api(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	method, args, err := decodeArgs(r)
	if err != nil {
		jsonResult(w, nil, err)
		return
	}
	h.mu.Lock()
	m := h.manager
	var selected *fixture
	if method == "OpenReader" {
		path, parseErr := arg[string](args, 0)
		if parseErr != nil {
			h.mu.Unlock()
			jsonResult(w, nil, parseErr)
			return
		}
		for _, book := range h.books {
			if book.Path == path {
				selected = book
			}
		}
		if selected == nil {
			h.mu.Unlock()
			jsonResult(w, nil, fmt.Errorf("only test fixture books can be opened"))
			return
		}
	}
	h.mu.Unlock()
	ctx := r.Context()
	if method == "OpenReader" {
		// Desktop reader sessions outlive an individual bridge call. The helper
		// likewise must not attach an open session to its short HTTP request.
		value, callErr := m.Open(context.Background(), selected.Path, selected.Report.Issues)
		jsonResult(w, value, callErr)
		return
	}
	if method == "CancelReaderOpen" {
		m.CancelOpen()
		jsonResult(w, nil, nil)
		return
	}
	session, err := arg[string](args, 0)
	if err != nil {
		jsonResult(w, nil, err)
		return
	}
	switch method {
	case "CancelReaderSearch":
		m.CancelSearch(session)
		jsonResult(w, nil, nil)
	case "CloseReader":
		jsonResult(w, nil, m.Close(session))
	case "SaveReaderState":
		state, parseErr := arg[reader.ReadingState](args, 1)
		if parseErr == nil {
			parseErr = m.SaveState(session, state)
		}
		jsonResult(w, nil, parseErr)
	case "SearchReader":
		query, parseErr := arg[string](args, 1)
		if parseErr != nil {
			jsonResult(w, nil, parseErr)
			return
		}
		value, callErr := m.Search(ctx, session, query, 100)
		jsonResult(w, value, callErr)
	case "LocateReaderIssue":
		index, parseErr := arg[int](args, 1)
		if parseErr != nil {
			jsonResult(w, nil, parseErr)
			return
		}
		value, callErr := m.LocateIssue(ctx, session, index)
		jsonResult(w, value, callErr)
	case "ReadReaderSource":
		id, parseErr := arg[string](args, 1)
		line, lineErr := arg[int](args, 2)
		column, columnErr := arg[int](args, 3)
		if parseErr != nil || lineErr != nil || columnErr != nil {
			jsonResult(w, nil, fmt.Errorf("invalid source request"))
			return
		}
		value, callErr := m.Source(ctx, session, id, line, column, 200)
		jsonResult(w, value, callErr)
	default:
		jsonResult(w, nil, fmt.Errorf("unknown test API method"))
	}
}

func (h *host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/__reader/") {
		h.mu.Lock()
		m := h.manager
		h.mu.Unlock()
		m.ServeHTTP(w, r)
		return
	}
	switch r.URL.Path {
	case "/__test/api":
		h.api(w, r)
	case "/__test/state":
		h.mu.Lock()
		defer h.mu.Unlock()
		reports := []epub.Report{}
		paths := map[string]string{}
		for _, kind := range []string{"normal", "legacy", "hostile", "malformed", "invalid"} {
			reports = append(reports, h.books[kind].Report)
			paths[kind] = h.books[kind].Path
		}
		jsonResult(w, map[string]any{"version": "1.4.0", "reports": reports, "paths": paths, "settings": map[string]any{"wordsPerPage": 250, "readingWPM": 250, "recursive": true, "javaPath": "java", "epubcheckJar": "", "runEpubcheck": false, "theme": "dark"}}, nil)
	case "/__test/reset":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.mu.Lock()
		err := h.reset()
		h.mu.Unlock()
		jsonResult(w, nil, err)
	case "/__test/evidence":
		h.mu.Lock()
		defer h.mu.Unlock()
		result := map[string]any{}
		for kind, book := range h.books {
			now, err := os.ReadFile(book.Path)
			oldHash, newHash := sha256.Sum256(book.Bytes), sha256.Sum256(now)
			result[kind] = map[string]any{"unchanged": err == nil && oldHash == newHash, "sha256": hex.EncodeToString(newHash[:])}
		}
		jsonResult(w, result, nil)
	case "/__test/mutate":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.mu.Lock()
		book := h.books[r.URL.Query().Get("kind")]
		if book == nil {
			h.mu.Unlock()
			jsonResult(w, nil, fmt.Errorf("unknown fixture"))
			return
		}
		err := os.WriteFile(book.Path, append(append([]byte{}, book.Bytes...), '\n'), 0600)
		h.mu.Unlock()
		jsonResult(w, nil, err)
	case "/__test/shutdown":
		jsonResult(w, nil, nil)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = h.server.Shutdown(ctx)
			h.manager.Shutdown()
			close(h.done)
		}()
	case "/favicon.ico":
		w.WriteHeader(http.StatusNoContent)
	default:
		http.FileServer(http.Dir(h.frontend)).ServeHTTP(w, r)
	}
}

func main() {
	root, err := os.MkdirTemp("", "spineguard-reader-host-")
	fatal(err)
	absoluteRoot, err := filepath.Abs(root)
	fatal(err)
	absoluteTemp, err := filepath.Abs(os.TempDir())
	fatal(err)
	// This helper may recursively clean only its freshly created direct child of
	// the system temp directory, never a repository or user-selected directory.
	defer func() {
		if filepath.Dir(absoluteRoot) == absoluteTemp && strings.HasPrefix(filepath.Base(absoluteRoot), "spineguard-reader-host-") {
			_ = os.RemoveAll(absoluteRoot)
		}
	}()
	frontend, err := filepath.Abs("frontend")
	fatal(err)
	if len(os.Args) > 1 {
		frontend, err = filepath.Abs(os.Args[1])
		fatal(err)
	}
	h := &host{root: absoluteRoot, frontend: frontend, done: make(chan struct{})}
	fatal(h.reset())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	fatal(err)
	h.server = &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = h.server.Serve(listener) }()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"origin": "http://" + listener.Addr().String()})
	<-h.done
}
