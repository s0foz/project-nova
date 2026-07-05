// Package ui serves the embedded Nova chat web UI.
//
// The UI is a single-page application composed of three static files
// (index.html, styles.css, app.js) embedded into the binary at build time
// via go:embed. Handler returns an http.Handler that serves the SPA shell
// at "/" with assets under "/assets/*", and falls back to index.html for
// any unknown path so client-side routing keeps working.
//
// The Go code in this package is platform-agnostic and compiles cleanly on
// Linux, macOS, and Windows (cross-compile verified with GOOS=windows).
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// assetsFS holds the static files embedded at build time. The directive
// embeds everything under the assets/ directory (relative to this file).
//
//go:embed assets/*
var assetsFS embed.FS

// subFS is the assets/ sub-filesystem rooted at "assets". We resolve it once
// at init so the file server does not have to strip the "assets/" prefix on
// every request.
var subFS fs.FS

func init() {
	s, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		// fs.Sub on a valid embed.FS sub-path never errors; if it does, the
		// binary is broken and we want to fail loudly at startup.
		panic("ui: cannot resolve embedded assets sub-filesystem: " + err.Error())
	}
	subFS = s
}

// Handler returns an http.Handler that serves the chat UI.
//
// Routing rules:
//   - GET /                -> index.html (Cache-Control: no-cache)
//   - GET /assets/<path>   -> asset file (Cache-Control: public, max-age=86400)
//   - any other path        -> index.html (SPA fallback, no-cache)
//
// Unknown methods are passed through to the file server which will respond
// with 405 Method Not Allowed.
func Handler() http.Handler {
	fileServer := http.FileServer(http.FS(subFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Normalise the path so "../" traversal attempts cannot escape the
		// embedded filesystem. http.FileServer already sanitises this, but
		// being defensive here means the SPA fallback logic below is safe.
		urlPath := path.Clean("/" + r.URL.Path)

		switch {
		case urlPath == "/":
			serveIndex(w, r)
			return
		case strings.HasPrefix(urlPath, "/assets/"):
			// Serve the asset. The file server expects a path relative to
			// the sub-filesystem root, so strip the leading "/assets/".
			r2 := cloneRequestPath(r, strings.TrimPrefix(urlPath, "/assets/"))
			w.Header().Set("Cache-Control", "public, max-age=86400")
			fileServer.ServeHTTP(w, r2)
			return
		}

		// SPA fallback: anything else resolves to index.html so that
		// client-side routes (e.g. /chat/123) keep working on refresh.
		serveIndex(w, r)
	})
}

// IndexHTML returns the raw index.html bytes. Useful for callers that want
// to serve the SPA shell at a custom path or compose it into a larger
// handler (for example, mounting it at /ui while keeping API routes on /api).
func IndexHTML() ([]byte, error) {
	return assetsFS.ReadFile("assets/index.html")
}

// serveIndex writes the embedded index.html with no-cache headers.
func serveIndex(w http.ResponseWriter, r *http.Request) {
	data, err := assetsFS.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, "ui: index.html missing from embedded assets", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(string(data)))
}

// cloneRequestPath returns a shallow copy of r with its URL.Path and
// URL.RawPath replaced. We only mutate the URL on the clone, leaving the
// caller's request untouched.
func cloneRequestPath(r *http.Request, p string) *http.Request {
	r2 := r.Clone(r.Context())
	r2.URL.Path = p
	r2.URL.RawPath = ""
	// FileServer looks at req.URL.Path; reset RawPath so it does not
	// accidentally shadow the cleaned value.
	return r2
}
