package epubcheck

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const cleanReport = `{"checker":{"checkerVersion":"5.4.0","nFatal":0,"nError":0,"nWarning":0},"messages":[]}`
const failedReport = `{"checker":{"checkerVersion":"5.4.0","nFatal":0,"nError":1,"nWarning":0},"messages":[{"ID":"RSC-005","severity":"ERROR","message":"XML is malformed.","suggestion":"Correct the XML.","additionalLocations":2,"locations":[{"path":"OPS/chapter.xhtml","line":12,"column":3},{"path":"OPS/nav.xhtml","line":-1}]}]}`

func TestParseReport(t *testing.T) {
	t.Run("clean standards pass", func(t *testing.T) {
		r, err := parseReport([]byte(cleanReport))
		if err != nil || r.Status != "passed" || r.Version != "5.4.0" || len(r.Issues) != 0 || r.Issues == nil {
			t.Fatalf("unexpected report: %+v, %v", r, err)
		}
	})
	t.Run("errors and multiple locations", func(t *testing.T) {
		r, err := parseReport([]byte(failedReport))
		if err != nil || r.Status != "failed" || len(r.Issues) != 2 {
			t.Fatalf("unexpected report: %+v, %v", r, err)
		}
		issue := r.Issues[0]
		if issue.Code != "EPUBCHECK-RSC-005" || issue.Severity != "error" || issue.Line != 12 || issue.File != "OPS/chapter.xhtml" || issue.Repairable || !strings.Contains(issue.Detail, "Correct the XML") || !strings.Contains(issue.Detail, "2 additional") {
			t.Fatalf("lost report information: %+v", issue)
		}
		if r.Issues[1].Line != 0 {
			t.Fatalf("unavailable line should be zero: %+v", r.Issues[1])
		}
	})
	t.Run("warning only still passes standards", func(t *testing.T) {
		raw := strings.ReplaceAll(failedReport, `"nError":1,"nWarning":0`, `"nError":0,"nWarning":1`)
		raw = strings.ReplaceAll(raw, `"severity":"ERROR"`, `"severity":"WARNING"`)
		r, err := parseReport([]byte(raw))
		if err != nil || r.Status != "passed" || len(r.Issues) != 2 || r.Issues[0].Severity != "warning" {
			t.Fatalf("unexpected warning result: %+v, %v", r, err)
		}
	})
	t.Run("summary errors cannot disappear", func(t *testing.T) {
		raw := strings.Replace(cleanReport, `"nError":0`, `"nError":2`, 1)
		r, err := parseReport([]byte(raw))
		if err != nil || r.Status != "failed" || len(r.Issues) != 1 || r.Issues[0].Severity != "error" {
			t.Fatalf("summary errors lost: %+v, %v", r, err)
		}
	})
	t.Run("fatal message overrides inconsistent zero counters", func(t *testing.T) {
		raw := strings.Replace(failedReport, `"nError":1`, `"nError":0`, 1)
		raw = strings.Replace(raw, `"severity":"ERROR"`, `"severity":"FATAL"`, 1)
		r, err := parseReport([]byte(raw))
		if err != nil || r.Status != "failed" || r.Issues[0].Severity != "error" {
			t.Fatalf("fatal message lost: %+v, %v", r, err)
		}
	})
	t.Run("locationless usage message", func(t *testing.T) {
		raw := strings.Replace(cleanReport, `"messages":[]`, `"messages":[{"ID":"OPF-062","severity":"USAGE","message":"Adobe page map detected.","locations":[],"suggestion":null}]`, 1)
		r, err := parseReport([]byte(raw))
		if err != nil || r.Status != "passed" || len(r.Issues) != 1 || r.Issues[0].Severity != "info" {
			t.Fatalf("usage information lost: %+v, %v", r, err)
		}
	})
	for name, raw := range map[string]string{
		"empty": "", "non JSON": "not a report", "empty object": `{}`, "null": `null`,
		"missing messages": `{"checker":{"checkerVersion":"5.4.0","nFatal":0,"nError":0}}`,
		"negative count":   strings.Replace(cleanReport, `"nError":0`, `"nError":-1`, 1),
		"missing counts":   `{"checker":{"checkerVersion":"5.4.0"},"messages":[]}`,
		"unknown severity": strings.Replace(failedReport, `"severity":"ERROR"`, `"severity":"WHAT"`, 1),
		"missing ID":       strings.Replace(failedReport, `"ID":"RSC-005"`, `"ID":""`, 1),
		"wrong data type":  strings.Replace(cleanReport, `"nError":0`, `"nError":"zero"`, 1),
	} {
		t.Run("reject "+name, func(t *testing.T) {
			r, err := parseReport([]byte(raw))
			if err == nil || r.Status != "unavailable" {
				t.Fatalf("invalid report accepted: %+v, %v", r, err)
			}
		})
	}
}

func TestFindingsAreBounded(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(failedReport), &raw); err != nil {
		t.Fatal(err)
	}
	message := raw["messages"].([]any)[0].(map[string]any)
	locations := make([]map[string]any, maxIssues+10)
	for i := range locations {
		locations[i] = map[string]any{"path": "OPS/chapter.xhtml", "line": i + 1}
	}
	message["locations"] = locations
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	r, err := parseReport(data)
	if err != nil || len(r.Issues) != maxIssues || !strings.Contains(r.Detail, "10,000") {
		t.Fatalf("findings cap not enforced: %d issues, %q, %v", len(r.Issues), r.Detail, err)
	}
	// Summary-level failure must remain visible even when all detailed locations
	// are warning groups and the findings display has reached its limit.
	message["severity"] = "WARNING"
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	r, err = parseReport(data)
	if err != nil || r.Status != "failed" || len(r.Issues) != maxIssues || r.Issues[maxIssues-1].Severity != "error" {
		t.Fatalf("capped report lost summary failure: %d issues, %q, %v", len(r.Issues), r.Detail, err)
	}
}

func TestReadReportRejectsOversizedFile(t *testing.T) {
	name := filepath.Join(t.TempDir(), "large.json")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(maxReportBytes + 1)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("prepare report: %v, %v", err, closeErr)
	}
	if _, err := readReport(name); err == nil {
		t.Fatal("oversized report accepted")
	}
}

func TestCappedLogConsumesOverflow(t *testing.T) {
	var log cappedLog
	input := bytes.Repeat([]byte("X"), maxLogBytes+123)
	for i := 0; i < 2; i++ {
		n, err := log.Write(input)
		if n != len(input) || err != nil {
			t.Fatalf("overflow interrupted process output: %d, %v", n, err)
		}
	}
	if log.buffer.Len() != maxLogBytes || !log.truncated || len(log.detail()) > 2150 {
		t.Fatalf("unbounded tool log: %d bytes, %d detail bytes", log.buffer.Len(), len(log.detail()))
	}
}

// The test executable acts as a tiny Java stand-in on every supported platform.
// This exercises argv boundaries, report handling, exit classification and
// cancellation without depending on a locally installed Java runtime.
func TestMain(m *testing.M) {
	if os.Getenv("SPINEGUARD_TEST_JAVA") == "1" {
		fakeJava()
		return
	}
	os.Exit(m.Run())
}

func fakeJava() {
	args := os.Args[1:]
	var reportPath string
	for i := range args {
		if args[i] == "--json" && i+1 < len(args) {
			reportPath = args[i+1]
		}
	}
	if reportPath == "" || len(args) == 0 {
		os.Exit(2)
	}
	data, err := os.ReadFile(args[len(args)-1])
	if err != nil {
		os.Exit(2)
	}
	switch string(data) {
	case "passed":
		if err := os.WriteFile(reportPath, []byte(cleanReport), 0600); err != nil {
			os.Exit(2)
		}
	case "failed":
		_ = os.WriteFile(reportPath, []byte(failedReport), 0600)
		os.Exit(1)
	case "invalid":
		_ = os.WriteFile(reportPath, []byte(`{}`), 0600)
	case "exit2":
		fmt.Fprint(os.Stderr, "Could not find main class")
		os.Exit(2)
	case "inconsistent":
		_ = os.WriteFile(reportPath, []byte(cleanReport), 0600)
		os.Exit(1)
	case "wait":
		time.Sleep(5 * time.Second)
	case "flood":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("X"), maxLogBytes*3))
		_ = os.WriteFile(reportPath, []byte(cleanReport), 0600)
	case "noreport":
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestCheckProcess(t *testing.T) {
	t.Setenv("SPINEGUARD_TEST_JAVA", "1")
	java, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	jar := filepath.Join(dir, "EPUB Check with spaces.jar")
	if err := os.WriteFile(jar, []byte("test JAR placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mode, status string
		wantError    bool
	}{
		{"passed", "passed", false}, {"failed", "failed", false},
		{"invalid", "unavailable", true}, {"exit2", "unavailable", true},
		{"inconsistent", "unavailable", true}, {"noreport", "unavailable", true},
		{"flood", "passed", false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			book := filepath.Join(dir, "book with spaces & punctuation.epub")
			if err := os.WriteFile(book, []byte(tc.mode), 0600); err != nil {
				t.Fatal(err)
			}
			r, err := Check(context.Background(), book, jar, java)
			if (err != nil) != tc.wantError || r.Status != tc.status || r.Detail == "" {
				t.Fatalf("unexpected process result: %+v, %v", r, err)
			}
		})
	}
	t.Run("context cancels subprocess", func(t *testing.T) {
		book := filepath.Join(dir, "wait.epub")
		if err := os.WriteFile(book, []byte("wait"), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		r, err := Check(ctx, book, jar, java)
		if !errors.Is(err, context.DeadlineExceeded) || r.Status != "unavailable" || time.Since(start) > 3*time.Second {
			t.Fatalf("subprocess not cancelled: %+v, %v", r, err)
		}
	})
}

func TestCheckUnavailable(t *testing.T) {
	for _, tc := range []struct{ book, jar, java string }{
		{}, {book: "missing.epub", jar: "missing.jar"}, {book: t.TempDir(), jar: t.TempDir()},
	} {
		r, err := Check(context.Background(), tc.book, tc.jar, tc.java)
		if err == nil || r.Status != "unavailable" || r.Detail == "" {
			t.Fatalf("unavailable tool accepted: %+v, %v", r, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := Check(ctx, "", "", "")
	if !errors.Is(err, context.Canceled) || r.Status != "unavailable" {
		t.Fatalf("cancellation not preserved: %+v, %v", r, err)
	}
}

// Opt in when the official distribution and Java are installed. Ordinary unit
// tests remain entirely offline and do not require either tool.
func TestCheckOfficialDistribution(t *testing.T) {
	jar := os.Getenv("SPINEGUARD_EPUBCHECK_JAR")
	if jar == "" {
		t.Skip("set SPINEGUARD_EPUBCHECK_JAR to exercise the official EPUBCheck distribution")
	}
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprintf("malformed=%v", malformed), func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "sample book.epub")
			writeOfficialFixture(t, name, malformed)
			r, err := Check(context.Background(), name, jar, os.Getenv("SPINEGUARD_JAVA_PATH"))
			want := "passed"
			if malformed {
				want = "failed"
			}
			if err != nil || r.Status != want || r.Version == "" {
				t.Fatalf("official EPUBCheck integration: %+v, %v", r, err)
			}
			if malformed && len(r.Issues) == 0 {
				t.Fatal("official EPUBCheck errors were not imported")
			}
		})
	}
}

func writeOfficialFixture(t *testing.T, name string, malformed bool) {
	t.Helper()
	chapter := `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en"><head><title>Chapter One</title></head><body><h1>Chapter One</h1><p>Readable words for a standards validation test.</p></body></html>`
	if malformed {
		chapter = strings.Replace(chapter, "</p>", "", 1)
	}
	entries := []struct{ name, data string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="EPUB/package.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"EPUB/package.opf", `<?xml version="1.0" encoding="UTF-8"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="pub-id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="pub-id">urn:uuid:de33a68c-8bd8-4d1e-b39e-cc62c80f0a6e</dc:identifier><dc:title>SpineGuard Integration Fixture</dc:title><dc:language>en</dc:language><meta property="dcterms:modified">2026-11-10T00:00:00Z</meta></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="chapter"/></spine></package>`},
		{"EPUB/nav.xhtml", `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="en" lang="en"><head><title>Contents</title></head><body><nav epub:type="toc" id="toc"><h1>Contents</h1><ol><li><a href="chapter.xhtml">Chapter One</a></li></ol></nav></body></html>`},
		{"EPUB/chapter.xhtml", chapter},
	}
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, entry := range entries {
		method := uint16(zip.Deflate)
		if entry.name == "mimetype" {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: entry.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
