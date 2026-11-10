package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

func TestSettingsPersistAndValidate(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if state := s.State("test"); state.Settings != DefaultSettings() || state.Warning != "" || state.Reports == nil {
		t.Fatalf("unexpected initial state: %+v", state)
	}
	want := DefaultSettings()
	want.WordsPerPage, want.ReadingWPM, want.Theme, want.Recursive = 300, 180, "light", false
	want.JavaPath, want.EPUBCheckJar = "  java  ", "  validator.jar  "
	saved, err := s.SaveSettings(want)
	if err != nil || saved.JavaPath != "java" || saved.EPUBCheckJar != "validator.jar" {
		t.Fatalf("preferences not normalized: %+v, %v", saved, err)
	}
	if state := New(dir).State("test"); state.Settings != saved || state.Warning != "" {
		t.Fatalf("saved preferences not restored: %+v", state)
	}
	// Replacing existing preferences must work on Windows as well as macOS.
	saved.Theme = "system"
	if _, err := s.SaveSettings(saved); err != nil {
		t.Fatalf("overwrite existing preferences: %v", err)
	}
	if got := New(dir).State("test").Settings; got.Theme != "system" {
		t.Fatalf("replacement preferences not restored: %+v", got)
	}
	bad := saved
	bad.WordsPerPage = 0
	if _, err := s.SaveSettings(bad); err == nil {
		t.Fatal("invalid preferences were saved")
	}
	if got := New(dir).State("test").Settings; got != saved {
		t.Fatalf("rejected preferences changed disk state: %+v", got)
	}
}

func TestSettingsBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*Settings)
	}{
		{"small page", func(s *Settings) { s.WordsPerPage = 99 }},
		{"large page", func(s *Settings) { s.WordsPerPage = 1001 }},
		{"small speed", func(s *Settings) { s.ReadingWPM = 79 }},
		{"large speed", func(s *Settings) { s.ReadingWPM = 1001 }},
		{"theme", func(s *Settings) { s.Theme = "invalid" }},
		{"missing Java", func(s *Settings) { s.JavaPath = "  " }},
		{"missing validator", func(s *Settings) { s.RunEPUBCheck = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := DefaultSettings()
			tc.modify(&s)
			if err := s.Validate(); err == nil {
				t.Fatal("invalid preferences accepted")
			}
		})
	}
	for _, n := range []int{100, 1000} {
		s := DefaultSettings()
		s.WordsPerPage = n
		if err := s.Validate(); err != nil {
			t.Fatalf("valid page boundary %d rejected: %v", n, err)
		}
	}
	for _, n := range []int{80, 1000} {
		s := DefaultSettings()
		s.ReadingWPM = n
		if err := s.Validate(); err != nil {
			t.Fatalf("valid speed boundary %d rejected: %v", n, err)
		}
	}
}

func TestBrokenPersistenceFallsBack(t *testing.T) {
	for _, file := range []string{"settings.json", "library.json"} {
		t.Run(file, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, filepath.Join(dir, file), "{broken")
			state := New(dir).State("test")
			if state.Warning == "" || state.Settings != DefaultSettings() || len(state.Reports) != 0 {
				t.Fatalf("corrupt saved data did not fall back: %+v", state)
			}
		})
	}
	t.Run("unavailable directory", func(t *testing.T) {
		s := New("")
		if state := s.State("test"); state.Warning == "" {
			t.Fatal("unavailable history not reported")
		}
		if _, err := s.SaveSettings(DefaultSettings()); err == nil {
			t.Fatal("saved preferences without a directory")
		}
	})
	t.Run("failed save leaves in memory preferences alone", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "is-a-file")
		writeTestFile(t, dir, "occupied")
		s := New(dir)
		settings := DefaultSettings()
		settings.Theme = "light"
		if _, err := s.SaveSettings(settings); err == nil {
			t.Fatal("save into a file should fail")
		}
		if state := s.State("test"); state.Settings != DefaultSettings() {
			t.Fatal("failed save changed active preferences")
		}
	})
}

func TestHistoryIsBoundedAndOmitsCoversOnDisk(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	book := filepath.Join(t.TempDir(), "book.epub")
	writeTestFile(t, book, "original file stays untouched")
	first := Report{Report: epub.Report{Path: book, Filename: "book.epub", Title: "First title", CoverData: "data:image/png;base64,AAA", Issues: []epub.Issue{}}}
	s.store(first)
	first.Title = "Latest title"
	s.store(first)
	s.persist()
	if got := s.State("test"); len(got.Reports) != 1 || got.Reports[0].CoverData == "" {
		t.Fatalf("memory history or duplicate replacement incorrect: %+v", got)
	}
	reloaded := New(dir).State("test")
	if len(reloaded.Reports) != 1 || reloaded.Reports[0].Title != "Latest title" || reloaded.Reports[0].CoverData != "" {
		t.Fatalf("unexpected persisted history: %+v", reloaded)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if len(New(dir).State("test").Reports) != 0 {
		t.Fatal("clear did not persist")
	}
	data, err := os.ReadFile(book)
	if err != nil || string(data) != "original file stays untouched" {
		t.Fatalf("clear touched source EPUB: %q, %v", data, err)
	}
	for i := 0; i < MaxBooks+2; i++ {
		s.store(Report{Report: epub.Report{Path: filepath.Join(dir, fmt.Sprintf("book-%04d.epub", i))}})
	}
	if got := len(s.State("test").Reports); got != MaxBooks {
		t.Fatalf("history limit not enforced: %d", got)
	}
}

func TestDiscoverFolders(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.epub", "b.EPUB", "c.kepub", "ignore.pdf", "nested/d.kepub.epub", ".hidden/hidden.epub"} {
		writeTestFile(t, filepath.Join(dir, filepath.FromSlash(name)), "fixture")
	}
	files, err := Discover(context.Background(), []string{dir, filepath.Join(dir, "a.epub")}, false)
	if err != nil || len(files) != 3 {
		t.Fatalf("flat discovery/deduplication: %v, %v", files, err)
	}
	files, err = Discover(context.Background(), []string{dir, dir}, true)
	if err != nil || len(files) != 4 {
		t.Fatalf("recursive discovery/deduplication: %v, %v", files, err)
	}
	for _, file := range files {
		if !filepath.IsAbs(file) || strings.Contains(file, ".hidden") {
			t.Fatalf("unexpected discovered path: %s", file)
		}
	}
	for _, input := range [][]string{nil, {filepath.Join(dir, "missing.epub")}, {filepath.Join(dir, "ignore.pdf")}, {t.TempDir()}} {
		if _, err := Discover(context.Background(), input, true); err == nil {
			t.Fatalf("unsupported input accepted: %v", input)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, []string{dir}, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery ignored cancellation: %v", err)
	}
}

func TestDiscoverCaseInsensitiveDuplicatesOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive Windows path identity")
	}
	book := filepath.Join(t.TempDir(), "MixedCase.epub")
	writeTestFile(t, book, "fixture")
	files, err := Discover(context.Background(), []string{book, strings.ToUpper(book)}, true)
	if err != nil || len(files) != 1 {
		t.Fatalf("Windows aliases produced duplicate books: %v, %v", files, err)
	}
}

func TestServiceOperationsAndProgress(t *testing.T) {
	s := New(t.TempDir())
	book := filepath.Join(t.TempDir(), "broken.epub")
	writeTestFile(t, book, "this is not a ZIP archive")
	var progress []Progress
	reports, err := s.Scan(context.Background(), []string{book}, func(p Progress) { progress = append(progress, p) })
	if err != nil || len(reports) != 1 || reports[0].Errors == 0 {
		t.Fatalf("corrupt book not reported: %+v, %v", reports, err)
	}
	if len(progress) != 2 || progress[0].Index != 0 || progress[0].Total != 1 || progress[1].Index != 1 || progress[1].Report == nil {
		t.Fatalf("unexpected progress: %+v", progress)
	}
	if len(s.State("test").Reports) != 1 || len(New(s.dir).State("test").Reports) != 1 {
		t.Fatal("scan did not store and persist history")
	}
	if _, err := s.Scan(context.Background(), []string{book}, nil); err != nil {
		t.Fatal(err)
	}
	if len(s.State("test").Reports) != 1 {
		t.Fatal("rescanning a book duplicated history")
	}
	ctx, _, err := s.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(context.Background(), []string{book}, nil); err == nil {
		t.Fatal("simultaneous operation was allowed")
	}
	if err := s.Clear(); err == nil {
		t.Fatal("history cleared during an operation")
	}
	s.Cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("operation cancellation did not propagate")
	}
	s.finish()
	if err := s.Clear(); err != nil {
		t.Fatalf("service remained busy after completion: %v", err)
	}
}

func TestRescanWindowsPathCasingReplacesHistory(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive Windows path identity")
	}
	s := New(t.TempDir())
	book := filepath.Join(t.TempDir(), "MixedCase.epub")
	writeTestFile(t, book, "broken EPUB for report identity test")
	for _, name := range []string{book, strings.ToUpper(book)} {
		if _, err := s.Scan(context.Background(), []string{name}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if reports := s.State("test").Reports; len(reports) != 1 {
		t.Fatalf("rescanning the same Windows file created %d history entries", len(reports))
	}
}

func TestUnreadableHistoryDoesNotDropPreferences(t *testing.T) {
	dir := t.TempDir()
	settings := DefaultSettings()
	settings.Theme = "light"
	data, _ := json.Marshal(settings)
	writeTestFile(t, filepath.Join(dir, "settings.json"), string(data))
	writeTestFile(t, filepath.Join(dir, "library.json"), "invalid")
	state := New(dir).State("test")
	if state.Settings.Theme != "light" || state.Warning == "" {
		t.Fatalf("history error discarded valid preferences: %+v", state)
	}
}

func writeTestFile(t *testing.T, name, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
