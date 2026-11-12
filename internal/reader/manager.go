package reader

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

const Prefix = "/__reader/"
const maxEntry = 64 << 20
const maxText = 16 << 20
const maxExpanded = 1 << 30

type session struct {
	mu           sync.Mutex
	searchMu     sync.Mutex
	searchCancel context.CancelFunc
	searchIntent uint64
	zip          *zip.ReadCloser
	filename     string
	info         os.FileInfo
	files        map[string]*zip.File
	resources    map[string]*Resource
	byPath       map[string]*Resource
	book         Book
	language     string
	opf          []byte
	nav          []byte
	fontKeys     map[string][]byte
	fontBytes    map[string]int
	blocked      map[string]bool
	closed       bool
	ctx          context.Context
	cancel       context.CancelFunc
}

type Manager struct {
	mu         sync.Mutex
	openMu     sync.Mutex
	current    *session
	cancelOpen context.CancelFunc
	dir        string
	states     map[string]savedState
	intent     uint64
	closed     bool
}

func New(dir string) *Manager {
	m := &Manager{dir: dir, states: map[string]savedState{}}
	m.loadStates()
	return m
}

func safeName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00:") {
		return false
	}
	n := strings.TrimSuffix(name, "/")
	return n != "" && n != "." && n != ".." && !strings.HasPrefix(n, "../") && path.Clean(n) == n
}
func resolve(base, href string) (string, string, bool) {
	u, e := url.Parse(strings.TrimSpace(href))
	if e != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil {
		return "", "", false
	}
	if strings.ContainsAny(u.Path, "\\\x00") || strings.HasPrefix(u.Path, "/") {
		return "", "", false
	}
	target := base
	// A ZIP has no query execution: resolve a publisher's cache-busting query
	// to its known static path, just as the native scanner does. Opaque output
	// URLs never contain that query and the HTTP handler still rejects queries.
	if u.Path != "" {
		target = path.Join(path.Dir(base), u.Path)
	}
	return target, u.Fragment, safeName(target)
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if e := c.ctx.Err(); e != nil {
		return 0, e
	}
	return c.r.Read(p)
}

func (m *Manager) Open(parent context.Context, filename string, issues []epub.Issue) (Book, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Book{}, errors.New("reader is shutting down")
	}
	m.intent++
	intent := m.intent
	if m.cancelOpen != nil {
		m.cancelOpen()
	}
	m.mu.Unlock()
	m.openMu.Lock()
	defer m.openMu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	m.mu.Lock()
	if intent != m.intent || m.closed {
		m.mu.Unlock()
		return Book{}, context.Canceled
	}
	m.cancelOpen = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if intent == m.intent {
			m.cancelOpen = nil
		}
		m.mu.Unlock()
	}()
	abs, e := filepath.Abs(filename)
	if e != nil {
		return Book{}, e
	}
	info, e := os.Stat(abs)
	if e != nil {
		return Book{}, e
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<30 {
		return Book{}, errors.New("reader requires a regular EPUB no larger than 2 GiB")
	}
	if e = checkDirectory(ctx, abs, info.Size()); e != nil {
		return Book{}, e
	}
	z, e := zip.OpenReader(abs)
	if e != nil {
		return Book{}, fmt.Errorf("this EPUB archive cannot be opened: %w", e)
	}
	s := &session{zip: z, filename: abs, info: info, files: map[string]*zip.File{}, resources: map[string]*Resource{}, byPath: map[string]*Resource{}, fontKeys: map[string][]byte{}, fontBytes: map[string]int{}, blocked: map[string]bool{}}
	ok := false
	defer func() {
		if !ok {
			z.Close()
		}
	}()
	if len(z.File) > 20000 {
		return Book{}, errors.New("archive contains too many resources")
	}
	var total uint64
	for _, f := range z.File {
		if e := ctx.Err(); e != nil {
			return Book{}, e
		}
		if !safeName(f.Name) || s.files[f.Name] != nil || f.Mode()&os.ModeSymlink != 0 {
			return Book{}, errors.New("unsafe or duplicate archive paths prevent reader access")
		}
		if f.Flags&1 != 0 {
			return Book{}, errors.New("encrypted ZIP resources cannot be opened in the reader")
		}
		if f.UncompressedSize64 > maxEntry || total+f.UncompressedSize64 < total || total+f.UncompressedSize64 > maxExpanded {
			return Book{}, errors.New("archive exceeds reader resource limits")
		}
		if f.UncompressedSize64 > 1<<20 && (f.CompressedSize64 == 0 || f.UncompressedSize64/f.CompressedSize64 > 1000) {
			return Book{}, errors.New("archive compression ratio exceeds reader limits")
		}
		total += f.UncompressedSize64
		s.files[f.Name] = f
	}
	f, e := os.Open(abs)
	if e != nil {
		return Book{}, e
	}
	h := sha256.New()
	keyPath := filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		keyPath = strings.ToLower(keyPath)
	}
	h.Write([]byte(keyPath))
	h.Write([]byte{0})
	_, e = io.Copy(h, ctxReader{ctx, f})
	f.Close()
	if e != nil {
		return Book{}, e
	}
	token := make([]byte, 24)
	if _, e = rand.Read(token); e != nil {
		return Book{}, e
	}
	s.book = Book{SessionID: hex.EncodeToString(token), BookKey: hex.EncodeToString(h.Sum(nil)), Title: filepath.Base(abs), Spine: []Resource{}, Resources: []Resource{}, TOC: []TOCEntry{}, Issues: append([]epub.Issue{}, issues...), Warnings: []string{}, Direction: "ltr", Layout: "reflowable"}
	s.book.PackageURL = Prefix + s.book.SessionID + "/package.opf"
	if e = s.publication(ctx); e != nil {
		return Book{}, e
	}
	if e = s.check(); e != nil {
		return Book{}, e
	}
	m.mu.Lock()
	if intent != m.intent || m.closed || ctx.Err() != nil {
		m.mu.Unlock()
		return Book{}, context.Canceled
	}
	if state, exists := m.states[s.book.BookKey]; exists {
		s.book.State = cloneState(state.State)
	} else {
		s.book.State = ReadingState{Bookmarks: []Bookmark{}}
	}
	s.ctx, s.cancel = context.WithCancel(parent)
	old := m.current
	m.current = s
	m.mu.Unlock()
	if old != nil {
		old.close()
	}
	ok = true
	return s.book, nil
}

// Bound the central directory before archive/zip allocates its file records.
// ZIP64 and multipart containers are unnecessary for the reader's size limits.
func checkDirectory(ctx context.Context, filename string, size int64) error {
	if size < 22 {
		return errors.New("the EPUB ZIP directory is incomplete")
	}
	f, e := os.Open(filename)
	if e != nil {
		return e
	}
	defer f.Close()
	n := int64(22 + 65535)
	if n > size {
		n = size
	}
	tail := make([]byte, n)
	if _, e = f.ReadAt(tail, size-n); e != nil {
		return e
	}
	for i := len(tail) - 22; i >= 0; i-- {
		if string(tail[i:i+4]) != "PK\x05\x06" {
			continue
		}
		record := tail[i:]
		if i+22+int(binary.LittleEndian.Uint16(record[20:22])) != len(tail) {
			continue
		}
		count := binary.LittleEndian.Uint16(record[10:12])
		dirSize := binary.LittleEndian.Uint32(record[12:16])
		offset := binary.LittleEndian.Uint32(record[16:20])
		if binary.LittleEndian.Uint16(record[4:6]) != 0 || binary.LittleEndian.Uint16(record[6:8]) != 0 || binary.LittleEndian.Uint16(record[8:10]) != count || count == 65535 || dirSize == 0xffffffff || offset == 0xffffffff {
			return errors.New("multipart or ZIP64 books are unavailable in the reader")
		}
		if count > 20000 || dirSize > 32<<20 || uint64(offset)+uint64(dirSize) > uint64(size-n+int64(i)) {
			return errors.New("archive directory exceeds reader safety limits")
		}
		end := int64(offset) + int64(dirSize)
		position := int64(offset)
		actual := 0
		header := make([]byte, 46)
		for position < end {
			if e := ctx.Err(); e != nil {
				return e
			}
			actual++
			if actual > int(count) || actual > 20000 {
				return errors.New("archive directory entry count is inconsistent")
			}
			if end-position < 46 {
				return errors.New("archive directory is truncated")
			}
			if _, e = f.ReadAt(header, position); e != nil {
				return e
			}
			if string(header[:4]) != "PK\x01\x02" {
				return errors.New("archive directory contains an invalid header")
			}
			length := int64(46) + int64(binary.LittleEndian.Uint16(header[28:30])) + int64(binary.LittleEndian.Uint16(header[30:32])) + int64(binary.LittleEndian.Uint16(header[32:34]))
			position += length
			if position > end {
				return errors.New("archive directory entry is truncated")
			}
		}
		if actual != int(count) {
			return errors.New("archive directory entry count is inconsistent")
		}
		return nil
	}
	return errors.New("the EPUB ZIP directory is incomplete")
}
func (m *Manager) CancelOpen() {
	m.mu.Lock()
	m.intent++
	if m.cancelOpen != nil {
		m.cancelOpen()
	}
	m.mu.Unlock()
}
func (s *session) check() error {
	if s.closed {
		return errors.New("reader session is closed")
	}
	info, e := os.Stat(s.filename)
	if e != nil || !os.SameFile(s.info, info) || info.Size() != s.info.Size() || !info.ModTime().Equal(s.info.ModTime()) {
		return errors.New("the book changed while open; close and reopen the reader")
	}
	return nil
}
func (s *session) close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.zip.Close()
	}
}
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	s := m.current
	if s == nil || s.book.SessionID != id {
		m.mu.Unlock()
		return nil
	}
	m.current = nil
	m.mu.Unlock()
	s.close()
	return nil
}
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.closed = true
	m.intent++
	if m.cancelOpen != nil {
		m.cancelOpen()
	}
	s := m.current
	m.current = nil
	m.mu.Unlock()
	if s != nil {
		s.close()
	}
}
func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil || m.current.book.SessionID != id {
		return nil, errors.New("reader session expired; reopen the book")
	}
	return m.current, nil
}
func (s *session) read(ctx context.Context, name string, limit int64) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	f := s.files[name]
	if f == nil || f.FileInfo().IsDir() {
		return nil, os.ErrNotExist
	}
	if f.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("resource exceeds reader size limit")
	}
	r, e := f.Open()
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(ctxReader{ctx, r}, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("resource exceeds reader size limit")
	}
	return b, nil
}
func (s *session) addResource(name, media string) *Resource {
	if existing := s.byPath[name]; existing != nil {
		if existing.MediaType == "application/octet-stream" && media != "" {
			existing.MediaType = media
			existing.Renderable = isDocument(media)
		}
		return existing
	}
	id := fmt.Sprintf("r%05d", len(s.resources)+1)
	if media == "" {
		media = guessMedia(name)
	}
	ext := extension(media)
	r := &Resource{ID: id, Path: name, URL: Prefix + s.book.SessionID + "/r/" + id + ext, MediaType: media, Title: path.Base(name), SpineIndex: -1, Missing: s.files[name] == nil, Renderable: isDocument(media)}
	s.resources[id] = r
	s.byPath[name] = r
	return r
}
func isDocument(media string) bool {
	return media == "application/xhtml+xml" || media == "text/html" || media == "image/svg+xml"
}
func extension(media string) string {
	switch media {
	case "application/xhtml+xml", "text/html", "image/svg+xml":
		return ".xhtml"
	case "text/css":
		return ".css"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "font/woff":
		return ".woff"
	case "font/woff2":
		return ".woff2"
	case "font/ttf", "application/x-font-ttf":
		return ".ttf"
	case "font/otf", "application/vnd.ms-opentype":
		return ".otf"
	default:
		return ".bin"
	}
}
func guessMedia(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".xhtml", ".html", ".htm":
		return "application/xhtml+xml"
	case ".css":
		return "text/css"
	case ".svg":
		return "image/svg+xml"
	case ".opf":
		return "application/oebps-package+xml"
	case ".ncx":
		return "application/x-dtbncx+xml"
	case ".xml":
		return "application/xml"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	}
	if v := mime.TypeByExtension(path.Ext(name)); v != "" {
		return strings.Split(v, ";")[0]
	}
	return "application/octet-stream"
}
func allowedMedia(media string) bool {
	return isDocument(media) || media == "text/css" || strings.HasPrefix(media, "image/") && media != "image/svg+xml" || strings.HasPrefix(media, "font/") || strings.Contains(media, "font") || strings.HasPrefix(media, "audio/") || strings.HasPrefix(media, "video/")
}
func (s *session) resolver(base string) Resolver {
	return func(ref, role string) string {
		name, fragment, ok := resolve(base, ref)
		if !ok {
			return ""
		}
		r := s.byPath[name]
		if r == nil || r.Missing || s.blocked[name] {
			return ""
		}
		compatible := false
		switch role {
		case "document":
			compatible = r.Renderable
		case "image":
			compatible = strings.HasPrefix(r.MediaType, "image/")
		case "stylesheet":
			compatible = r.MediaType == "text/css"
		case "font":
			compatible = strings.HasPrefix(r.MediaType, "font/") || strings.Contains(r.MediaType, "font")
		case "media":
			compatible = strings.HasPrefix(r.MediaType, "audio/") || strings.HasPrefix(r.MediaType, "video/")
		case "asset":
			compatible = allowedMedia(r.MediaType)
		}
		if !compatible {
			return ""
		}
		value := r.URL
		if r.MediaType == "image/svg+xml" && role != "document" {
			value = Prefix + s.book.SessionID + "/r/" + r.ID + ".svg"
		}
		if fragment != "" {
			value += "#" + url.PathEscape(fragment)
		}
		return value
	}
}
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.URL.Path, Prefix) || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, Prefix), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	s, e := m.get(parts[0])
	if e != nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e = s.check(); e != nil {
		http.Error(w, e.Error(), http.StatusConflict)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	var data []byte
	media := ""
	if len(parts) == 2 && parts[1] == "package.opf" {
		data = s.opf
		media = "application/oebps-package+xml"
	} else if len(parts) == 2 && parts[1] == "navigation.xhtml" {
		data = s.nav
		media = "application/xhtml+xml"
	} else if len(parts) == 3 && parts[1] == "r" {
		id := strings.TrimSuffix(parts[2], path.Ext(parts[2]))
		resource := s.resources[id]
		svgAsset := resource != nil && resource.MediaType == "image/svg+xml" && r.URL.Path == Prefix+s.book.SessionID+"/r/"+id+".svg"
		if resource == nil || resource.URL != r.URL.Path && !svgAsset || resource.Missing || s.blocked[resource.Path] || !allowedMedia(resource.MediaType) {
			http.NotFound(w, r)
			return
		}
		media = resource.MediaType
		limit := int64(maxEntry)
		if resource.Renderable {
			limit = maxHTMLBytes
		} else if media == "text/css" {
			limit = maxCSSBytes
		}
		data, e = s.read(ctx, resource.Path, limit)
		if e == nil {
			switch {
			case svgAsset:
				data, e = SanitizeSVG(ctx, data, s.resolver(resource.Path), readerPolicy())
			case resource.Renderable:
				data, e = SanitizeHTML(ctx, data, s.resolver(resource.Path), readerPolicy())
				media = "application/xhtml+xml"
			case media == "text/css":
				data, e = SanitizeCSS(ctx, data, s.resolver(resource.Path))
			default:
				if key := s.fontKeys[resource.Path]; len(key) > 0 {
					for i := 0; i < len(data) && i < s.fontBytes[resource.Path]; i++ {
						data[i] ^= key[i%len(key)]
					}
				}
			}
		}
	} else {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, "This resource cannot be previewed: "+e.Error(), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", media+charsetSuffix(media))
	w.Header().Set("Content-Security-Policy", readerPolicy())
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	if r.Method == "GET" {
		_, _ = w.Write(data)
	}
}
func charsetSuffix(media string) string {
	if strings.Contains(media, "xml") || strings.HasPrefix(media, "text/") {
		return "; charset=utf-8"
	}
	return ""
}
func readerPolicy() string {
	return "default-src 'none'; script-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; media-src 'self'; connect-src 'none'; frame-src 'none'; object-src 'none'; form-action 'none'; base-uri 'self'"
}
func (s *session) resourceList() {
	names := make([]string, 0, len(s.byPath))
	for name := range s.byPath {
		names = append(names, name)
	}
	sort.Strings(names)
	s.book.Resources = []Resource{}
	for _, name := range names {
		s.book.Resources = append(s.book.Resources, *s.byPath[name])
	}
}
