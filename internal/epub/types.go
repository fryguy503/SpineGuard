// Package epub inspects EPUB containers without extracting or executing book content.
// Its built-in checks are deliberately distinct from formal EPUBCheck validation.
package epub

import "time"

type Options struct {
	WordsPerPage int `json:"wordsPerPage"`
	ReadingWPM   int `json:"readingWPM"`
}

func (o Options) normalized() Options {
	if o.WordsPerPage <= 0 {
		o.WordsPerPage = 250
	}
	if o.ReadingWPM <= 0 {
		o.ReadingWPM = 250
	}
	return o
}

type Report struct {
	Path        string    `json:"path"`
	Filename    string    `json:"filename"`
	Title       string    `json:"title"`
	Author      string    `json:"author"`
	Series      string    `json:"series,omitempty"`
	SeriesIndex string    `json:"seriesIndex,omitempty"`
	Language    string    `json:"language"`
	EPUBVersion string    `json:"epubVersion"`
	Identifier  string    `json:"identifier"`
	FileBytes   int64     `json:"fileBytes"`
	ScannedAt   time.Time `json:"scannedAt"`
	DurationMS  int64     `json:"durationMs"`
	Status      string    `json:"status"`
	Errors      int       `json:"errors"`
	Warnings    int       `json:"warnings"`
	Infos       int       `json:"infos"`
	Stats       Stats     `json:"stats"`
	Issues      []Issue   `json:"issues"`
	CoverData   string    `json:"coverData,omitempty"`
}

type Stats struct {
	Words               int   `json:"words"`
	EstimatedPages      int   `json:"estimatedPages"`
	SourcePages         int   `json:"sourcePages"`
	Chapters            int   `json:"chapters"`
	Images              int   `json:"images"`
	Fonts               int   `json:"fonts"`
	ReadingMinutes      int   `json:"readingMinutes"`
	Files               int   `json:"files"`
	UncompressedBytes   int64 `json:"uncompressedBytes"`
	LargestChapterBytes int64 `json:"largestChapterBytes"`
}

type Issue struct {
	Code           string `json:"code"`
	Severity       string `json:"severity"`
	Title          string `json:"title"`
	Detail         string `json:"detail"`
	File           string `json:"file,omitempty"`
	Line           int    `json:"line,omitempty"`
	Reference      string `json:"reference,omitempty"`
	Anchor         string `json:"anchor,omitempty"`
	Repairable     bool   `json:"repairable"`
	RepairKind     string `json:"repairKind,omitempty"`
	RepairReason   string `json:"repairReason,omitempty"`
	OptionalRepair bool   `json:"optionalRepair,omitempty"`
}

type RepairResult struct {
	OutputPath string   `json:"outputPath"`
	Changes    []string `json:"changes"`
	Report     Report   `json:"report"`
}

// Recount updates aggregate issue counts and the native-check status after issues
// have been appended (for example, by the optional EPUBCheck adapter).
func Recount(r *Report) {
	r.Errors, r.Warnings, r.Infos = 0, 0, 0
	for _, i := range r.Issues {
		switch i.Severity {
		case "error":
			r.Errors++
		case "warning":
			r.Warnings++
		default:
			r.Infos++
		}
	}
	if r.Status == "unreadable" {
		return
	}
	switch {
	case r.Errors > 0:
		r.Status = "error"
	case r.Warnings > 0:
		r.Status = "warning"
	default:
		r.Status = "healthy"
	}
}
