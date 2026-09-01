package httpserver

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// spaHandler serves the embedded static files built into dist/. Anything that isn't an exact
// file match falls back to index.html, because client-side routing owns those paths (e.g.
// /dashboard, /p/abc123 are not real files but real app routes).
func spaHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dist is embedded at build time via go:embed; a missing subtree means the binary
		// itself is broken, not something a request can work around.
		panic(err)
	}
	staticFS := http.FS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upath := r.URL.Path
		if !strings.HasPrefix(upath, "/") {
			upath = "/" + upath
		}
		name := strings.TrimPrefix(upath, "/")

		if name != "" {
			if f, err := staticFS.Open(name); err == nil {
				info, statErr := f.Stat()
				f.Close()
				if statErr == nil && !info.IsDir() {
					if strings.HasPrefix(upath, "/assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					http.ServeFileFS(w, r, sub, name)
					return
				}
			}
		}

		// Unknown path: serve index.html so client-side routing can take over.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, sub, "index.html")
	})
}

// apiNotFound is the fallback for any /api/ path that no more specific handler claimed — it
// returns the standard JSON error envelope instead of the SPA's index.html.
func apiNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"error":{"code":"not_found","message":"not found"}}`))
}
