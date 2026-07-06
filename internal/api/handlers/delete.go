package handlers

import (
	"errors"
	"net/http"

	"github.com/project-nova/nova/internal/model"
	"github.com/project-nova/nova/internal/registry"
)

// DeleteRequest is the JSON body accepted by DELETE /api/delete. Even though
// DELETE requests typically have no body, Ollama sends one and we honour that.
type DeleteRequest struct {
	Name string `json:"name"`
}

// Delete returns an http.HandlerFunc implementing DELETE /api/delete.
//
// It removes the named model's manifest from disk. Blobs are left in place
// (content-addressed, deduplicated) — they can be garbage-collected by a
// future `nova prune` command.
func Delete(w http.ResponseWriter, r *http.Request) {
	var req DeleteRequest
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
	if err := registry.DeleteManifest(name); err != nil {
		if errors.Is(err, model.ErrManifestNotFound) {
			writeError(w, http.StatusNotFound, "model '"+req.Name+"' not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}
