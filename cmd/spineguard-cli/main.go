// spineguard-cli makes the same engine available for batch jobs and scripts.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/fryguy503/SpineGuard/internal/epub"
	"github.com/fryguy503/SpineGuard/internal/library"
)

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Println("SpineGuard 1.0.0")
		return 0
	case "help", "--help", "-h":
		usage()
		return 0
	case "scan":
		return scan(ctx, args[1:])
	case "repair":
		return repair(ctx, args[1:])
	default:
		fmt.Fprintln(os.Stderr, "Unknown command:", args[0])
		usage()
		return 2
	}
}
func usage() {
	fmt.Fprintln(os.Stderr, `SpineGuard — EPUB health checks, reading statistics, and repairs

  spineguard-cli scan [options] book.epub [folder ...]
  spineguard-cli repair -out book.repaired.epub book.epub
  spineguard-cli version

Scan options:
  -format text|json|csv   Output format (default text)
  -recursive             Include subfolders (default true)
  -words-per-page 250    Estimate pages from this many words
  -reading-wpm 250       Estimate reading time
  -epubcheck path        Optional epubcheck.jar (Java required)
  -java java             Java executable name or full path

Exit codes: 0 no errors detected; 1 book errors found; 2 usage/tool failure;
130 canceled. Warnings do not cause exit 1. Built-in checks are not full
standards certification. Repair always creates a separate, new file.`)
}

func scan(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("scan", flag.ContinueOnError)
	settings := library.DefaultSettings()
	format := "text"
	flags.StringVar(&format, "format", "text", "text, json or csv")
	flags.BoolVar(&settings.Recursive, "recursive", true, "include subfolders")
	flags.IntVar(&settings.WordsPerPage, "words-per-page", 250, "words per estimated page")
	flags.IntVar(&settings.ReadingWPM, "reading-wpm", 250, "reading speed")
	flags.StringVar(&settings.EPUBCheckJar, "epubcheck", "", "epubcheck.jar path")
	flags.StringVar(&settings.JavaPath, "java", "java", "Java executable")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	settings.RunEPUBCheck = settings.EPUBCheckJar != ""
	if err := settings.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if format != "text" && format != "json" && format != "csv" {
		fmt.Fprintln(os.Stderr, "format must be text, json or csv")
		return 2
	}
	if flags.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "select at least one EPUB file or folder")
		return 2
	}
	files, err := library.Discover(ctx, flags.Args(), settings.Recursive)
	if err != nil {
		if ctx.Err() != nil {
			return 130
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	reports := make([]library.Report, 0, len(files))
	code := 0
	for _, path := range files {
		if ctx.Err() != nil {
			return 130
		}
		r := library.CheckFile(ctx, path, settings)
		reports = append(reports, r)
		if r.Errors > 0 && code == 0 {
			code = 1
		}
		if r.EPUBCheck != nil && r.EPUBCheck.Status == "unavailable" {
			code = 2
		}
		if format == "text" {
			fmt.Printf("%s — %s\n  %s · %s\n  %d words · %d estimated pages (%d words/page) · %d source page markers\n  %d min reading · %d chapters · %d images\n  %d errors · %d warnings\n", r.Title, r.Status, r.Author, r.Filename, r.Stats.Words, r.Stats.EstimatedPages, settings.WordsPerPage, r.Stats.SourcePages, r.Stats.ReadingMinutes, r.Stats.Chapters, r.Stats.Images, r.Errors, r.Warnings)
			for _, issue := range r.Issues {
				location := issue.File
				if issue.Line > 0 {
					location += fmt.Sprintf(":%d", issue.Line)
				}
				fmt.Printf("  [%s] %s: %s %s\n", issue.Severity, issue.Code, issue.Title, location)
			}
			if r.EPUBCheck != nil {
				fmt.Printf("  EPUBCheck: %s %s\n", r.EPUBCheck.Status, r.EPUBCheck.Detail)
			}
			fmt.Println()
		}
	}
	if ctx.Err() != nil {
		return 130
	}
	if format != "text" {
		if err := library.Export(os.Stdout, format, reports); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	return code
}

func repair(ctx context.Context, args []string) int {
	flags := flag.NewFlagSet("repair", flag.ContinueOnError)
	out := flags.String("out", "", "new repaired EPUB path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: spineguard-cli repair -out new-file.epub book.epub")
		return 2
	}
	if !strings.EqualFold(filepath.Ext(*out), ".epub") {
		fmt.Fprintln(os.Stderr, "output must have an .epub extension")
		return 2
	}
	r, err := epub.Repair(ctx, flags.Arg(0), *out, epub.Options{WordsPerPage: 250, ReadingWPM: 250})
	if ctx.Err() != nil {
		return 130
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if r.Report.Errors > 0 {
		return 1
	}
	return 0
}
