package library

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

// BookCover reloads a saved book's thumbnail without rescanning the publication
// or putting image payloads into history. Requests are read-only and may run in
// parallel; the frontend bounds its visible-cover loading queue.
func (s *Service) BookCover(ctx context.Context, filename string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path, err := filepath.Abs(filename)
	if err != nil {
		return "", err
	}
	if !supported(path) {
		return "", errors.New("choose a scanned EPUB book to view its cover")
	}
	s.mu.Lock()
	known := false
	for _, report := range s.reports {
		if samePath(report.Path, path) {
			known = true
			break
		}
	}
	s.mu.Unlock()
	if !known {
		return "", errors.New("scan this book before viewing its cover")
	}
	return epub.LoadCover(ctx, path)
}
