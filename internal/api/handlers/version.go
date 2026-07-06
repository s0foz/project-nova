package handlers

import (
	"net/http"

	"github.com/project-nova/nova/internal/version"
)

// VersionResponse is the wire shape of GET /api/version.
type VersionResponse struct {
	Version string `json:"version"`
}

// Version returns an http.HandlerFunc implementing GET /api/version. It
// reports the build-time version string injected via -ldflags.
func Version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, VersionResponse{Version: version.Version})
}
