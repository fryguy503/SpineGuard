package main

import (
	"net/http"
	"strings"
)

// The detached shell is served at the normal application index so Wails injects
// its native bridge. Book resources always use their separate, opaque routes.
func readerAssetMiddleware(book http.HandlerFunc, detached bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/__reader/") {
				book(w, r)
				return
			}
			if !detached {
				next.ServeHTTP(w, r)
				return
			}
			if r.URL.Path == "/" || r.URL.Path == "/index.html" {
				if r.Method != http.MethodGet && r.Method != http.MethodHead {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				data, err := assets.ReadFile("frontend/reader.html")
				if err != nil {
					http.Error(w, "The reader interface is unavailable.", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				if r.Method == http.MethodGet {
					_, _ = w.Write(data)
				}
				return
			}
			switch r.URL.Path {
			case "/favicon.ico":
				w.WriteHeader(http.StatusNoContent)
			case "/styles.css", "/reader.css", "/reader.js", "/reader-window.js",
				"/vendor/epubjs/epub.min.js", "/vendor/epubjs/LICENSE", "/vendor/epubjs/THIRD-PARTY-LICENSES.txt":
				next.ServeHTTP(w, r)
			default:
				http.NotFound(w, r)
			}
		})
	}
}
