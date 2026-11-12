package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetachedReaderServesOnlyItsShellAndAssets(t *testing.T) {
	book := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusGone) })
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := readerAssetMiddleware(book, true)(next)
	for _, route := range []string{"/", "/index.html"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `data-reader-window="true"`) || strings.Contains(w.Body.String(), `src="app.js"`) {
			t.Fatalf("unexpected detached shell at %s: %d %s", route, w.Code, w.Body)
		}
	}
	for _, route := range []string{"/app.js", "/reader.html", "/frontend/index.html", "/x/../styles.css"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("unrelated asset %s returned %d", route, w.Code)
		}
	}
	for _, route := range []string{"/reader.js", "/reader-window.js", "/styles.css"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))
		if w.Code != http.StatusNoContent {
			t.Fatalf("reader asset %s returned %d", route, w.Code)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/__reader/unknown/r/book.xhtml", nil))
	if w.Code != http.StatusGone {
		t.Fatal("book route fell through to application assets")
	}
}
