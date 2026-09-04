package httpserver

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
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

	// serveIndex serves dist/index.html directly via http.ServeContent rather than
	// http.ServeFileFS: ServeFileFS (and ServeFile) unconditionally 301-redirects any request
	// whose r.URL.Path ends in "/index.html" to "./", before it even looks at the `name`
	// argument — so a literal GET /index.html would bounce off that special case instead of
	// getting the file. ServeContent has no such special case.
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		f, err := staticFS.Open("index.html")
		if err != nil {
			http.Error(w, "index.html missing from embedded build", http.StatusInternalServerError)
			return
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			http.Error(w, "index.html missing from embedded build", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", info.ModTime(), f)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upath := r.URL.Path
		if !strings.HasPrefix(upath, "/") {
			upath = "/" + upath
		}
		name := strings.TrimPrefix(upath, "/")

		// A dotfile basename (e.g. /.gitignore) is tooling detritus that happens to be present
		// in the embedded dist/ tree, never a real app asset or route — refuse it outright
		// rather than let the exact-match branch below serve it.
		if base := path.Base(name); name != "" && strings.HasPrefix(base, ".") {
			http.NotFound(w, r)
			return
		}

		// index.html always goes through serveIndex, exact match or not, so a direct
		// GET /index.html gets the 200 + Cache-Control response instead of ServeFileFS's redirect.
		if name == "index.html" {
			serveIndex(w, r)
			return
		}

		isAsset := strings.HasPrefix(upath, "/assets/")

		if name != "" {
			if f, err := staticFS.Open(name); err == nil {
				info, statErr := f.Stat()
				_ = f.Close()
				if statErr == nil && !info.IsDir() {
					if isAsset {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					} else {
						w.Header().Set("Cache-Control", "no-cache")
					}
					http.ServeFileFS(w, r, sub, name)
					return
				}
			}
			if isAsset {
				// A miss under /assets/ is a missing built file, not a client-side route — a
				// real 404, never the SPA's index.html fallback.
				http.NotFound(w, r)
				return
			}
		}

		// Unknown path: serve index.html so client-side routing can take over.
		serveIndex(w, r)
	})
}

// apiNotFound is the fallback for any /api/ path that no more specific handler claimed — it
// returns the standard JSON error envelope instead of the SPA's index.html.
func apiNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"not found"}}`))
}
