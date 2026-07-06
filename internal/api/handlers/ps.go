package handlers

import (
	"net/http"

	"github.com/project-nova/nova/internal/server"
)

// PSResponse is the wire shape of GET /api/ps.
type PSResponse struct {
	Models []PSModel `json:"models"`
}

// PSModel is one row in the loaded-models list.
type PSModel struct {
	Name      string           `json:"name"`
	Model     string           `json:"model"` // file digest
	Size      int64            `json:"size"`
	Digest    string           `json:"digest"`
	ExpiresAt string           `json:"expires_at"`
	SizeVRAM  uint64           `json:"size_vram"`
	Details   TagsModelDetails `json:"details"`
}

// PS returns an http.HandlerFunc implementing GET /api/ps. It lists the
// models currently resident in memory via srv.List().
func PS(srv *server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		loaded := srv.List()
		models := make([]PSModel, 0, len(loaded))
		for _, lm := range loaded {
			var size int64
			digest := ""
			if lm.Manifest != nil {
				for _, l := range lm.Manifest.Layers {
					size += l.Size
				}
				digest = lm.Manifest.Digest
			}
			models = append(models, PSModel{
				Name:      lm.Name.String(),
				Model:     digest,
				Size:      size,
				Digest:    digest,
				ExpiresAt: lm.ExpiresAt.UTC().Format("2006-01-02T15:04:05.999999Z"),
				SizeVRAM:  lm.SizeVRAM,
				Details: TagsModelDetails{
					Format:            "nova",
					Family:            "nova-stub",
					ParameterSize:     "unknown",
					QuantizationLevel: "q8_0",
				},
			})
		}
		writeJSON(w, http.StatusOK, PSResponse{Models: models})
	}
}
