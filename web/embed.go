package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:assets
var assetsFS embed.FS

// SPAHandler serves the embedded frontend assets.
// Any unrecognized path falls back to index.html (SPA client-side routing).
func SPAHandler() http.Handler {
	sub, _ := fs.Sub(assetsFS, "assets")
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")

		// Check if the file exists in the embedded FS
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err != nil {
			// File not found — serve index.html for SPA routing
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}