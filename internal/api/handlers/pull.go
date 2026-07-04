package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/project-nova/nova/internal/registry"
)

// PullRequest is the JSON body accepted by POST /api/pull.
type PullRequest struct {
	Name     string `json:"name"`
	Insecure bool   `json:"insecure,omitempty"`
	Stream   *bool  `json:"stream,omitempty"`
}

// PullStatus is one streamed progress line for /api/pull.
type PullStatus struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// Pull returns an http.HandlerFunc implementing POST /api/pull.
//
// NOTE ON THE STUB REGISTRY:
//
//	Nova ships with a local-only registry. There is no upstream model
//	registry server yet, so pull cannot fetch new models from the network.
//	This handler therefore implements an honest, Ollama-shaped progress
//	stream that:
//	  1. Reports "pulling manifest".
//	  2. If the model already exists locally, immediately reports "success".
//	  3. Otherwise, emits a small simulated layer-by-layer progress stream
//	     and then returns a 404 with {"error":"model not found in registry"}.
//
//	When a real registry transport is added, this handler is the only piece
//	that needs to change.
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

	name, err := registry.Parse(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	emit(PullStatus{Status: "pulling manifest"})

	if registry.Exists(name) {
		// Model already present — short-circuit to success.
		emit(PullStatus{Status: "success"})
		if !streaming {
			writeJSON(w, http.StatusOK, PullStatus{Status: "success"})
		}
		return
	}

	// Simulated progress for a non-existent model. We emit a couple of
	// layer-pull status lines so clients can render their progress UIs,
	// then return an honest 404.
	emit(PullStatus{Status: "pulling " + shortID(name.String()), Digest: fakeDigest(name.String()), Total: 1024 * 1024, Completed: 0})
	for i := 1; i <= 4; i++ {
		time.Sleep(25 * time.Millisecond)
		emit(PullStatus{
			Status:    "pulling " + shortID(name.String()),
			Digest:    fakeDigest(name.String()),
			Total:     1024 * 1024,
			Completed: int64(i) * 256 * 1024,
		})
	}

	// Surface the failure: in streaming mode as a final error line, in
	// non-streaming mode as a 404 JSON body.
	if streaming {
		writeNDJSON(w, map[string]string{"error": "model not found in registry"})
		return
	}
	writeError(w, http.StatusNotFound, "model not found in registry")
}

// fakeDigest derives a deterministic-looking sha256-style digest from a model
// name. It is NOT a real hash and is only used to populate the digest field
// in the simulated progress stream.
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
