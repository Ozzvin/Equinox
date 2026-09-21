// Package webui embeds the web interface so the daemon is a single executable.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var files embed.FS

// Handler serves the interface files.
func Handler() http.Handler {
	sub, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	h := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always revalidate: the UI ships inside the binary and changes with it.
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; media-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		h.ServeHTTP(w, r)
	})
}
