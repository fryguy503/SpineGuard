package main

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fryguy503/SpineGuard/internal/reader"
)

func (a *App) OpenReader(filename string) (reader.Book, error) {
	requested, e := filepath.Abs(filename)
	if e != nil {
		return reader.Book{}, e
	}
	requested = filepath.Clean(requested)
	for _, report := range a.GetState().Reports {
		known, e := filepath.Abs(report.Path)
		if e != nil {
			continue
		}
		matches := filepath.Clean(known) == requested
		if runtime.GOOS == "windows" {
			matches = strings.EqualFold(filepath.Clean(known), requested)
		}
		if matches {
			return a.reader.Open(a.ctx, known, report.Issues)
		}
	}
	return reader.Book{}, errors.New("scan this book before opening it in the reader")
}
func (a *App) CancelReaderOpen()                 { a.reader.CancelOpen() }
func (a *App) CancelReaderSearch(session string) { a.reader.CancelSearch(session) }
func (a *App) CloseReader(session string) error  { return a.reader.Close(session) }
func (a *App) SaveReaderState(session string, state reader.ReadingState) error {
	return a.reader.SaveState(session, state)
}
func (a *App) SearchReader(session, query string) ([]reader.SearchResult, error) {
	return a.reader.Search(a.ctx, session, query, 100)
}
func (a *App) LocateReaderIssue(session string, index int) (reader.Location, error) {
	return a.reader.LocateIssue(a.ctx, session, index)
}
func (a *App) ReadReaderSource(session, id string, line, column int) (reader.Source, error) {
	return a.reader.Source(a.ctx, session, id, line, column, 200)
}
