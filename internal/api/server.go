// Package api wires together the Nova HTTP API: an Ollama-compatible surface
// mounted under /api/* and an OpenAI-compatible surface mounted under /v1/*.
//
// New returns an *http.Server bound to env.Host() with sensible timeouts.
// Handler returns just the http.Handler for callers that want to plug the
// Nova routes into their own listener (used by tests and by the tray app).
package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/project-nova/nova/internal/api/handlers"
	"github.com/project-nova/nova/internal/env"
	"github.com/project-nova/nova/internal/openai"
	"github.com/project-nova/nova/internal/server"
	"github.com/project-nova/nova/internal/ui"
)

// New returns an *http.Server wired with all Nova + OpenAI-compatible routes,
// backed by the given orchestrator. It listens on env.Host().
func New(srv *server.Server) *http.Server {
	return &http.Server{
		Addr:              env.Host(),
		Handler:           Handler(srv),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0, // streaming responses may take arbitrarily long
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

// Handler returns just the http.Handler (useful for custom listeners / testing).
// It uses the stdlib net/http ServeMux with Go 1.22 method-pattern routing and
// applies CORS, panic-recovery, and (when NOVA_DEBUG=1) request-logging
// middleware.
func Handler(srv *server.Server) http.Handler {
	mux := http.NewServeMux()

	// ---- Ollama-compatible routes ----
	mux.HandleFunc("POST /api/generate", handlers.Generate(srv))
	mux.HandleFunc("POST /api/chat", handlers.Chat(srv))
	mux.HandleFunc("POST /api/embeddings", handlers.Embeddings(srv))
	mux.HandleFunc("POST /api/embed", handlers.Embed(srv))
	mux.HandleFunc("GET /api/tags", handlers.Tags)
	mux.HandleFunc("GET /api/version", handlers.Version)
	mux.HandleFunc("POST /api/show", handlers.Show)
	mux.HandleFunc("POST /api/pull", handlers.Pull)
	mux.HandleFunc("POST /api/push", handlers.Push)
	mux.HandleFunc("POST /api/create", handlers.Create)
	mux.HandleFunc("DELETE /api/delete", handlers.Delete)
	mux.HandleFunc("POST /api/copy", handlers.Copy)
	mux.HandleFunc("GET /api/ps", handlers.PS(srv))

	// Blobs: HEAD checks existence, POST uploads.
	mux.HandleFunc("HEAD /api/blobs/{digest}", handlers.BlobStat)
	mux.HandleFunc("POST /api/blobs/{digest}", handlers.BlobUpload)

	// Programmatic health check (kept separate from the browser UI root).
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		handlers.WriteJSONPub(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": "nova",
		})
	})

	// ---- Embedded chat web UI ----
	// The SPA is served at the root so that visiting
	// http://127.0.0.1:11434/  in a browser opens the chat window. More
	// specific method+path patterns (the /api/* and /v1/* routes above)
	// take precedence over this catch-all, so the API continues to work.
	mux.Handle("/", ui.Handler())

	// ---- OpenAI-compatible routes ----
	openai.Register(mux, srv)

	var h http.Handler = mux
	h = recoverMiddleware(h)
	h = corsMiddleware(h, env.AllowedOrigins())
	if env.Debug() {
		h = loggingMiddleware(h)
	}
	return h
}

// corsMiddleware injects permissive CORS headers based on env.AllowedOrigins().
// The Origin header on the request is matched against the allowed list; if it
// matches (or "*" is allowed) we echo it back. Preflight OPTIONS requests are
// short-circuited with 204.
func corsMiddleware(next http.Handler, allowed []string) http.Handler {
	allowAll := contains(allowed, "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowAll || contains(allowed, hostOf(origin)) || contains(allowed, origin)) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Accept, X-Requested-With")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", "3600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// recoverMiddleware catches panics, logs them with a stack trace, and returns
// a 500 JSON error body so a single bad handler cannot take down the process.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("nova: panic handling %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				handlers.WriteJSONPub(w, http.StatusInternalServerError, map[string]string{
					"error": fmt.Sprintf("internal server error: %v", rec),
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs each request when NOVA_DEBUG=1.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)
		log.Printf("%s %s %d %s %s", r.Method, r.URL.Path, ww.status, time.Since(start), r.RemoteAddr)
	})
}

// statusRecorder captures the response status code for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status before delegating.
func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush unwraps to the underlying Flusher if present (keeps streaming working
// through the logging middleware).
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// contains reports whether the haystack contains the needle.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// hostOf extracts the host portion of an Origin/URL string. For
// "http://localhost:8080" it returns "localhost"; for "https://127.0.0.1"
// it returns "127.0.0.1".
func hostOf(origin string) string {
	s := origin
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	return s
}

// Shutdown gracefully shuts down the server with a 30-second deadline.
func Shutdown(s *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
