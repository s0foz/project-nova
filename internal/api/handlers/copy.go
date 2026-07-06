package handlers

import (
	"errors"
	"net/http"

	"github.com/project-nova/nova/internal/model"
	"github.com/project-nova/nova/internal/registry"
)

// CopyRequest is the JSON body accepted by POST /api/copy.
type CopyRequest struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

// Copy returns an http.HandlerFunc implementing POST /api/copy. It duplicates
// an existing manifest under a new name.
func Copy(w http.ResponseWriter, r *http.Request) {
	var req CopyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Source == "" || req.Destination == "" {
		writeError(w, http.StatusBadRequest, "source and destination are required")
		return
	}

	src, err := registry.Parse(req.Source)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dst, err := registry.Parse(req.Destination)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := registry.CopyManifest(src, dst); err != nil {
		if errors.Is(err, model.ErrManifestNotFound) {
			writeError(w, http.StatusNotFound, "model '"+req.Source+"' not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
