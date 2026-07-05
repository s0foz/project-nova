package handlers

import (
	"net/http"
	"time"

	"github.com/project-nova/nova/internal/registry"
)

// PushRequest is the JSON body accepted by POST /api/push.
type PushRequest struct {
	Name     string `json:"name"`
	Insecure bool   `json:"insecure,omitempty"`
	Stream   *bool  `json:"stream,omitempty"`
}

// PushStatus is one streamed progress line for /api/push.
type PushStatus struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// Push returns an http.HandlerFunc implementing POST /api/push.
//
// NOTE ON THE STUB REGISTRY:
//
//	As with /api/pull, Nova currently has no upstream registry server to
//	push to. This handler therefore emits an Ollama-shaped progress stream
//	and, when the source model exists locally, ends with "success". When
//	the source model does not exist locally it returns 404
//	{"error":"model not found"}. The wire shape is correct so existing
//	clients keep working; only the network transport is missing.
func Push(w http.ResponseWriter, r *http.Request) {
	var req PushRequest
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

	emit := func(s PushStatus) {
		if streaming {
			writeNDJSON(w, s)
		}
	}

	if !registry.Exists(name) {
		if streaming {
			writeNDJSON(w, map[string]string{"error": "model not found"})
			return
		}
		writeError(w, http.StatusNotFound, "model '"+req.Name+"' not found")
		return
	}

	emit(PushStatus{Status: "pushing manifest"})
	emit(PushStatus{Status: "pushing " + shortID(name.String()), Digest: fakeDigest(name.String()), Total: 1024 * 1024, Completed: 0})
	for i := 1; i <= 4; i++ {
		time.Sleep(25 * time.Millisecond)
		emit(PushStatus{
			Status:    "pushing " + shortID(name.String()),
			Digest:    fakeDigest(name.String()),
			Total:     1024 * 1024,
			Completed: int64(i) * 256 * 1024,
		})
	}
	emit(PushStatus{Status: "success"})

	if !streaming {
		writeJSON(w, http.StatusOK, PushStatus{Status: "success"})
	}
}
