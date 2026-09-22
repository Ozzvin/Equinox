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
		// Never cache: the UI ships inside the binary and changes with every build, and a stale
		// copy in the WebView2 profile (which survives across restarts, unlike this process) is
		// worse than the odd instant of belt-and-braces here. "no-cache" alone still lets a client
		// store the response and revalidate it, and embedded files carry no ModTime/ETag for that
		// revalidation to use, so play it safe with "no-store".
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; media-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		h.ServeHTTP(w, r)
	})
}
