package library

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"
)

func Export(w io.Writer, format string, reports []Report) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(struct {
			Application string    `json:"application"`
			ExportedAt  time.Time `json:"exportedAt"`
			Reports     []Report  `json:"reports"`
		}{"SpineGuard", time.Now().UTC(), reports})
	case "csv":
		writer := csv.NewWriter(w)
		if err := writer.Write([]string{"Filename", "Title", "Author", "Language", "EPUB version", "Built-in result", "Errors", "Warnings", "Words", "Estimated pages", "Source page markers", "Reading minutes", "Chapters", "Images", "File bytes", "EPUBCheck", "Path", "Series", "Series index"}); err != nil {
			return err
		}
		for _, r := range reports {
			validation := "not run"
			if r.EPUBCheck != nil {
				validation = r.EPUBCheck.Status
			}
			row := []string{r.Filename, r.Title, r.Author, r.Language, r.EPUBVersion, r.Status, strconv.Itoa(r.Errors), strconv.Itoa(r.Warnings), strconv.Itoa(r.Stats.Words), strconv.Itoa(r.Stats.EstimatedPages), strconv.Itoa(r.Stats.SourcePages), strconv.Itoa(r.Stats.ReadingMinutes), strconv.Itoa(r.Stats.Chapters), strconv.Itoa(r.Stats.Images), strconv.FormatInt(r.FileBytes, 10), validation, r.Path, r.Series, r.SeriesIndex}
			for i, cell := range row {
				row[i] = safeCSV(cell)
			}
			if err := writer.Write(row); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	default:
		return fmt.Errorf("unsupported export format: %s", format)
	}
}

// Prevent a book title or filename becoming a spreadsheet formula on opening CSV.
func safeCSV(value string) string {
	if len(value) > 0 {
		switch value[0] {
		case '=', '+', '-', '@', '\t', '\r', '\n':
			return "'" + value
		}
	}
	return value
}
