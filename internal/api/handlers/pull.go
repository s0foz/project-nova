package handlers

import (
	"net/http"
	"strings"

	"github.com/project-nova/nova/internal/registry"
)

// PullRequest is the JSON body accepted by POST /api/pull.
//
// The "name" field is overloaded: it may be either a local model name to pull
// from a registry (e.g. "llama3", which currently has no upstream registry and
// returns a helpful 404), OR a remote source specifier that Nova downloads
// directly:
//
//   - "hf:owner/repo/file.gguf"            -> HuggingFace resolve URL
//   - "https://host/path/model.gguf"        -> direct HTTPS
//   - "http://host/path/model.gguf"         -> direct HTTP
//   - "file:///abs/path/model.gguf"         -> local file
//   - "/abs/path/model.gguf"                -> local file (Unix)
//   - "C:\path\to\model.gguf"               -> local file (Windows)
//
// When a remote source is given, Nova derives a local model name from the
// filename (e.g. "llama-2-7b-chat.Q4_K_M.gguf" -> "llama-2-7b-chat.q4_k_m").
// To register under an explicit name, use the CLI:
//
//	nova pull myname:latest hf:owner/repo/file.gguf
//
// (the HTTP API derives the name automatically).
type PullRequest struct {
	Name     string `json:"name"`
	Insecure bool   `json:"insecure,omitempty"`
	Stream   *bool  `json:"stream,omitempty"`
}

// PullStatus is one streamed progress line for /api/pull. It mirrors the
// Ollama wire shape so existing pull UIs render Nova progress unchanged.
type PullStatus struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// Pull returns an http.HandlerFunc implementing POST /api/pull.
func Pull(w http.ResponseWriter, r *http.Request) {
	var req PullRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	streaming := req.Stream == nil || *req.Stream
	if streaming {
		startNDJSON(w)
	}

	emit := func(s PullStatus) {
		if streaming {
			writeNDJSON(w, s)
		}
	}

	// If the name is a remote source specifier, perform a real download.
	if isRemoteSource(req.Name) {
		emit(PullStatus{Status: "pulling manifest"})
		name, err := registry.PullModel(r.Context(), "", req.Name, func(p registry.Progress) {
			emit(PullStatus{
				Status:    p.Status,
				Digest:    p.Digest,
				Total:     p.Total,
				Completed: p.Completed,
			})
		})
		if err != nil {
			if streaming {
				writeNDJSON(w, map[string]string{"error": err.Error()})
				return
			}
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		emit(PullStatus{Status: "success"})
		if !streaming {
			writeJSON(w, http.StatusOK, PullStatus{Status: "success"})
		}
		_ = name
		return
	}

	// Otherwise it's a bare model name (e.g. "llama3"). Nova has no upstream
	// registry yet, so be honest: short-circuit to success if already present,
	// else explain how to pull a real model.
	name, err := registry.Parse(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	emit(PullStatus{Status: "pulling manifest"})
	if registry.Exists(name) {
		emit(PullStatus{Status: "success"})
		if !streaming {
			writeJSON(w, http.StatusOK, PullStatus{Status: "success"})
		}
		return
	}

	msg := "model not found; Nova has no upstream registry yet. " +
		"Pull a real model with a source specifier, e.g. " +
		"`nova pull hf:owner/repo/model.gguf` or " +
		"`nova pull https://example.com/model.gguf`."
	if streaming {
		writeNDJSON(w, map[string]string{"error": msg})
		return
	}
	writeError(w, http.StatusNotFound, msg)
}

// isRemoteSource reports whether s is a remote/local source specifier (as
// opposed to a bare registry model name like "llama3" or "library/qwen:7b").
func isRemoteSource(s string) bool {
	switch {
	case strings.HasPrefix(s, "hf:"),
		strings.HasPrefix(s, "http://"),
		strings.HasPrefix(s, "https://"),
		strings.HasPrefix(s, "file://"):
		return true
	}
	// Absolute local path: Unix "/..." or Windows "C:\..." / "C:/...".
	if strings.HasPrefix(s, "/") {
		return true
	}
	if len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') {
		return true
	}
	return false
}

// fakeDigest derives a deterministic-looking sha256-style digest from a model
// name. It is NOT a real hash and is only used to populate the digest field in
// the simulated push progress stream (push.go).
func fakeDigest(name string) string {
	// Simple FNV-like accumulator over the name.
	var h uint64 = 1469598103934665603
	for _, c := range name {
		h ^= uint64(c)
		h *= 1099511628211
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		h ^= h >> 7
		h *= 1099511628211
		out[i] = hex[int(h>>60)&0xf]
	}
	return "sha256:" + string(out)
}

// shortID trims a model name to a short identifier for progress messages.
func shortID(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, ":"); i >= 0 {
		name = name[:i]
	}
	return name
}
