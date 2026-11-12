package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fryguy503/SpineGuard/internal/epub"
	"github.com/fryguy503/SpineGuard/internal/library"
)

func detachedFixture(t *testing.T) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "Reader.epub")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(file)
	for name, content := range map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Detached book</dc:title><dc:identifier id="uid">detached-book</dc:identifier><dc:language>en</dc:language></metadata><manifest><item id="one" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="one"/></spine></package>`,
		"chapter.xhtml":          `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>First chapter</title></head><body><h1>Read independently</h1></body></html>`,
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestReaderWindowPrivateModeAndBoundedPipe(t *testing.T) {
	if !isReaderWindow([]string{readerWindowFlag}) {
		t.Fatal("private reader mode not recognized")
	}
	for _, args := range [][]string{nil, {"book.epub"}, {readerWindowFlag, "book.epub"}, {"--other", readerWindowFlag}} {
		if isReaderWindow(args) {
			t.Fatalf("ordinary app arguments entered private mode: %v", args)
		}
	}
	message := readerWindowMessage{Launch: ReaderLaunch{Path: filepath.Join(t.TempDir(), "A & B.epub"), IssueIndex: 0, Theme: "light"}, ConfigDir: t.TempDir(), Title: "Quoted \"title\"\nNew line", Issues: []epub.Issue{{Code: "LINK_MISSING", Detail: "Quoted \"value\"\n{\"launch\":{}}"}}}
	first, err := encodeReaderMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	message.Launch.IssueIndex = -1
	second, err := encodeReaderMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	input := bufio.NewReader(bytes.NewReader(append(first, second...)))
	decoded, err := readReaderMessage(input)
	if err != nil || decoded.Launch.IssueIndex != 0 || decoded.Title != message.Title || decoded.Issues[0].Detail != message.Issues[0].Detail {
		t.Fatalf("escaped book data changed pipe framing: %+v %v", decoded, err)
	}
	decoded, err = readReaderMessage(input)
	if err != nil || decoded.Launch.IssueIndex != -1 {
		t.Fatalf("second control packet unavailable: %+v %v", decoded, err)
	}
	for _, invalid := range []string{strings.Repeat("x", maxReaderMessage+1) + "\n", "{}\n", string(first[:len(first)-1])} {
		if _, err := readReaderMessage(bufio.NewReader(strings.NewReader(invalid))); err == nil {
			t.Fatal("invalid or oversized pipe message accepted")
		}
	}
	message.Title = strings.Repeat("t", maxReaderMessage)
	if _, err := encodeReaderMessage(message); err == nil {
		t.Fatal("oversized parent message accepted")
	}
}

func TestDetachedWindowHasOnlySelectedBookAndDoesNotLoadLibrary(t *testing.T) {
	book := detachedFixture(t)
	dir := t.TempDir()
	libraryFile := filepath.Join(dir, "library.json")
	libraryBytes := []byte("intentionally unreadable library history; child does not need it")
	if err := os.WriteFile(libraryFile, libraryBytes, 0600); err != nil {
		t.Fatal(err)
	}
	packet, err := encodeReaderMessage(readerWindowMessage{Launch: ReaderLaunch{Path: book, IssueIndex: -1, Theme: "dark"}, ConfigDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	window, err := newReaderWindow(bytes.NewReader(packet))
	if err != nil {
		t.Fatal(err)
	}
	defer window.shutdown(context.Background())
	opened, err := window.OpenReader(book)
	if err != nil || opened.Title != "Detached book" || len(opened.Spine) != 1 {
		t.Fatalf("selected book: %+v %v", opened, err)
	}
	if _, err := window.OpenReader(detachedFixture(t)); err == nil || !strings.Contains(err.Error(), "only its selected book") {
		t.Fatalf("another book must be denied: %v", err)
	}
	window.readControls() // EOF must not close the independent reader.
	if _, err := window.ReadReaderSource(opened.SessionID, opened.Spine[0].ID, 1, 1); err != nil {
		t.Fatalf("parent EOF closed reader: %v", err)
	}
	after, err := os.ReadFile(libraryFile)
	if err != nil || !bytes.Equal(after, libraryBytes) {
		t.Fatal("detached reader changed library history")
	}
}

func TestDetachedWindowRejectsUnscannedBooksBeforeStartingProcess(t *testing.T) {
	a := &App{ctx: context.Background(), service: library.New(t.TempDir()), readerWindows: newReaderWindows(t.TempDir())}
	if err := a.OpenReaderWindow(detachedFixture(t), -1); err == nil || !strings.Contains(err.Error(), "scan this book") {
		t.Fatalf("unscanned launch: %v", err)
	}
	if len(a.readerWindows.windows) != 0 {
		t.Fatal("unscanned book started a process")
	}
}

func TestDetachedWindowStartsPortraitAndFitsNormalScreens(t *testing.T) {
	for _, screen := range [][2]int{{1920, 1080}, {1366, 768}, {1280, 720}, {1000, 900}} {
		width, height, minWidth, minHeight := readerWindowSize(screen[0], screen[1])
		if width >= height || width > screen[0]-64 || height > screen[1]-96 || minWidth > width || minHeight > height {
			t.Fatalf("screen %v -> %dx%d, minimum %dx%d", screen, width, height, minWidth, minHeight)
		}
	}
}
