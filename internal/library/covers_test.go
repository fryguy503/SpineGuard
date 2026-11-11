package library

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

func libraryCoverBook(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cover.epub")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 24, 36))); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"OPS/book.opf":           `<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/><manifest><item id="cover" href="cover.png" media-type="image/png" properties="cover-image"/></manifest><spine/></package>`,
		"OPS/cover.png":          pixels.String(),
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBookCoverReloadsOldHistoryWithoutScanOrMutation(t *testing.T) {
	dir := t.TempDir()
	book := libraryCoverBook(t)
	s := New(dir)
	s.store(Report{Report: epub.Report{Path: book, Title: "Book", Series: "Series", SeriesIndex: "0.5", Stats: epub.Stats{Words: 999}}})
	s.persist()
	historyBefore, _ := os.ReadFile(filepath.Join(dir, "library.json"))
	bookBefore, _ := os.ReadFile(book)
	restored := New(dir)
	if report := restored.State("test").Reports[0]; report.CoverData != "" || report.Series != "Series" || report.SeriesIndex != "0.5" {
		t.Fatalf("history lost series metadata or persisted image: %+v", report)
	}
	data, err := restored.BookCover(context.Background(), book)
	if err != nil || !strings.HasPrefix(data, "data:image/jpeg;base64,") {
		t.Fatalf("saved report cover unavailable: %q,%v", data, err)
	}
	report := restored.State("test").Reports[0]
	if report.Stats.Words != 999 || !report.ScannedAt.IsZero() || report.CoverData != "" {
		t.Fatal("cover reload rescanned or changed the stored report")
	}
	historyAfter, _ := os.ReadFile(filepath.Join(dir, "library.json"))
	bookAfter, _ := os.ReadFile(book)
	if !bytes.Equal(historyBefore, historyAfter) || !bytes.Equal(bookBefore, bookAfter) {
		t.Fatal("cover loading modified history or the source book")
	}
}

func TestBookCoverReadOnlyConcurrencyAndFailures(t *testing.T) {
	book := libraryCoverBook(t)
	s := New(t.TempDir())
	if _, err := s.BookCover(context.Background(), book); err == nil {
		t.Fatal("cover read accepted a book outside the library")
	}
	s.store(Report{Report: epub.Report{Path: book}})
	s.mu.Lock()
	s.busy = true // Read-only covers do not take ownership of scan/repair state.
	s.mu.Unlock()
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cover, err := s.BookCover(context.Background(), book); err != nil || cover == "" {
				t.Errorf("concurrent read-only cover failed: %v", err)
			}
		}()
	}
	wg.Wait()
	s.mu.Lock()
	if !s.busy {
		t.Error("cover loading changed operation state")
	}
	s.busy = false
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.BookCover(ctx, book); !errors.Is(err, context.Canceled) {
		t.Fatalf("cover cancellation lost: %v", err)
	}
	if err := os.Remove(book); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BookCover(context.Background(), book); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file should remain a reviewable cover error: %v", err)
	}
}
