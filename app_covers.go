package main

// GetBookCover loads a bounded thumbnail for a saved book without running a new
// health check or persisting image data in report history.
func (a *App) GetBookCover(path string) (string, error) {
	return a.service.BookCover(a.ctx, path)
}
