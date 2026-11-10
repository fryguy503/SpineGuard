// Package epubcheck provides optional, local standards validation with the
// official EPUBCheck Java distribution. It never downloads tools or books.
package epubcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

const (
	maxReportBytes = 32 << 20
	maxLogBytes    = 64 << 10
	maxIssues      = 10000
	checkTimeout   = 2 * time.Minute
)

// Result keeps standards validation separate from SpineGuard's built-in checks.
// An unavailable checker must never be interpreted as a passing validation.
type Result struct {
	Status  string       `json:"status"`
	Version string       `json:"version"`
	Detail  string       `json:"detail"`
	Issues  []epub.Issue `json:"issues"`
}

// Check invokes a user-configured local EPUBCheck JAR. Keep the distribution's
// lib directory beside the JAR. An empty javaPath uses java on PATH. Validation
// errors return a failed Result and nil error; installation, execution, timeout,
// and malformed-report errors return unavailable and a non-nil error.
func Check(ctx context.Context, bookPath, jarPath, javaPath string) (Result, error) {
	result := Result{Status: "unavailable", Issues: []epub.Issue{}}
	fail := func(err error) (Result, error) {
		result.Detail = err.Error()
		return result, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return fail(fmt.Errorf("EPUBCheck cancelled: %w", err))
	}
	if strings.TrimSpace(jarPath) == "" {
		return fail(errors.New("EPUBCheck is not configured; select epubcheck.jar from its extracted distribution"))
	}
	jar, err := localFile(jarPath)
	if err != nil {
		return fail(fmt.Errorf("EPUBCheck JAR: %w", err))
	}
	if !strings.EqualFold(filepath.Ext(jar), ".jar") {
		return fail(errors.New("EPUBCheck path must point to a local .jar file"))
	}
	book, err := localFile(bookPath)
	if err != nil {
		return fail(fmt.Errorf("EPUB input: %w", err))
	}
	if strings.TrimSpace(javaPath) == "" {
		javaPath = "java"
	}
	java, err := exec.LookPath(javaPath)
	if err != nil {
		return fail(fmt.Errorf("Java is unavailable; install Java or choose its executable in settings: %w", err))
	}
	tempDir, err := os.MkdirTemp("", "spineguard-epubcheck-")
	if err != nil {
		return fail(fmt.Errorf("create EPUBCheck report directory: %w", err))
	}
	defer os.RemoveAll(tempDir)
	reportPath := filepath.Join(tempDir, "report.json")
	runCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	// No shell is used. Absolute file arguments cannot become command options.
	// Cap the JVM heap, execution time, logs and report read independently.
	cmd := exec.CommandContext(runCtx, java, "-Xmx512m", "-jar", jar,
		"--json", reportPath, "--maxOfEachMessage", "100", book)
	hideWindow(cmd)
	var output cappedLog
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = 2 * time.Second
	runErr := cmd.Run()
	if err := runCtx.Err(); err != nil {
		return fail(fmt.Errorf("EPUBCheck did not finish: %w", err))
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1 {
			return fail(fmt.Errorf("EPUBCheck could not run: %w%s", runErr, output.detail()))
		}
	}
	data, err := readReport(reportPath)
	if err != nil {
		return fail(fmt.Errorf("EPUBCheck did not produce a usable report: %w%s", err, output.detail()))
	}
	result, err = parseReport(data)
	if err != nil {
		return fail(fmt.Errorf("EPUBCheck returned an invalid report: %w%s", err, output.detail()))
	}
	if runErr != nil && result.Status != "failed" {
		result.Status = "unavailable"
		return fail(fmt.Errorf("EPUBCheck exited with an error without reporting a validation failure%s", output.detail()))
	}
	return result, nil
}

func localFile(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("a file path is required")
	}
	full, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path is not a regular file")
	}
	return full, nil
}

func readReport(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxReportBytes {
		return nil, errors.New("report is not a regular file within the 32 MiB limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxReportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxReportBytes {
		return nil, errors.New("report exceeds the 32 MiB limit")
	}
	return data, nil
}

type reportMessage struct {
	ID                  string `json:"ID"`
	Severity            string `json:"severity"`
	Message             string `json:"message"`
	Suggestion          string `json:"suggestion"`
	AdditionalLocations int    `json:"additionalLocations"`
	Locations           []struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	} `json:"locations"`
}

type report struct {
	Checker *struct {
		Version  string `json:"checkerVersion"`
		Fatal    *int   `json:"nFatal"`
		Errors   *int   `json:"nError"`
		Warnings int    `json:"nWarning"`
	} `json:"checker"`
	Messages *[]reportMessage `json:"messages"`
}

func parseReport(data []byte) (Result, error) {
	r := Result{Status: "unavailable", Issues: []epub.Issue{}}
	if len(data) > maxReportBytes {
		return r, errors.New("report exceeds the 32 MiB limit")
	}
	var raw report
	if err := json.Unmarshal(data, &raw); err != nil {
		return r, err
	}
	if raw.Checker == nil || raw.Messages == nil || raw.Checker.Fatal == nil || raw.Checker.Errors == nil || strings.TrimSpace(raw.Checker.Version) == "" {
		return r, errors.New("missing EPUBCheck metadata, error counts, or messages")
	}
	if *raw.Checker.Fatal < 0 || *raw.Checker.Errors < 0 || raw.Checker.Warnings < 0 {
		return r, errors.New("negative EPUBCheck message count")
	}
	r.Version = raw.Checker.Version
	r.Status = "passed"
	if *raw.Checker.Fatal > 0 || *raw.Checker.Errors > 0 {
		r.Status = "failed"
	}
	hasError := false
	truncated := false
	for _, message := range *raw.Messages {
		severity := "info"
		switch strings.ToUpper(strings.TrimSpace(message.Severity)) {
		case "FATAL", "ERROR":
			severity, hasError, r.Status = "error", true, "failed"
		case "WARNING", "WARN":
			severity = "warning"
		case "INFO", "USAGE", "HINT":
		default:
			return Result{Status: "unavailable", Issues: []epub.Issue{}}, fmt.Errorf("unrecognized message severity %q", message.Severity)
		}
		if strings.TrimSpace(message.ID) == "" || strings.TrimSpace(message.Message) == "" {
			return Result{Status: "unavailable", Issues: []epub.Issue{}}, errors.New("message lacks an ID or description")
		}
		detail := message.Message
		if strings.TrimSpace(message.Suggestion) != "" {
			detail += " Suggested action: " + message.Suggestion
		}
		if message.AdditionalLocations > 0 {
			detail += fmt.Sprintf(" EPUBCheck omitted %d additional locations for this message.", message.AdditionalLocations)
		}
		base := epub.Issue{Code: "EPUBCHECK-" + message.ID, Severity: severity, Title: "EPUBCheck: " + message.ID, Detail: detail}
		if len(message.Locations) == 0 {
			if len(r.Issues) < maxIssues {
				r.Issues = append(r.Issues, base)
			} else {
				truncated = true
			}
		}
		for _, location := range message.Locations {
			if len(r.Issues) >= maxIssues {
				truncated = true
				break
			}
			issue := base
			issue.File, issue.Line = location.Path, max(0, location.Line)
			r.Issues = append(r.Issues, issue)
		}
	}
	if r.Status == "failed" && !hasError {
		summary := epub.Issue{Code: "EPUBCHECK-SUMMARY", Severity: "error", Title: "EPUBCheck reported validation errors", Detail: "The checker summary contains errors; individual error locations were not provided."}
		if len(r.Issues) == maxIssues {
			r.Issues[maxIssues-1], truncated = summary, true
		} else {
			r.Issues = append(r.Issues, summary)
		}
	}
	r.Detail = fmt.Sprintf("EPUBCheck %s: %d fatal, %d error, %d warning message groups.", r.Version, *raw.Checker.Fatal, *raw.Checker.Errors, raw.Checker.Warnings)
	if truncated {
		r.Detail += " Findings are limited to the first 10,000 locations."
	}
	return r, nil
}

// cappedLog consumes all writes to avoid blocking the subprocess while retaining
// a bounded prefix. Stdout and stderr can write concurrently.
type cappedLog struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	truncated bool
}

func (l *cappedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	remaining := maxLogBytes - l.buffer.Len()
	if len(p) > remaining {
		p, l.truncated = p[:remaining], true
	}
	_, _ = l.buffer.Write(p)
	return n, nil
}

func (l *cappedLog) detail() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	value := strings.TrimSpace(l.buffer.String())
	if len(value) > 2048 {
		value = value[:2048] + "…"
	}
	if value == "" {
		return ""
	}
	if l.truncated {
		value += " (tool output truncated)"
	}
	return ": " + value
}
