package handlers

import (
	"net/http"

	"github.com/project-nova/nova/internal/registry"
)

// TagsResponse is the wire shape of GET /api/tags.
type TagsResponse struct {
	Models []TagsModel `json:"models"`
}

// TagsModel is one row in the tags list.
type TagsModel struct {
	Name       string           `json:"name"`
	ModifiedAt string           `json:"modified_at"`
	Size       int64            `json:"size"`
	Digest     string           `json:"digest"`
	Details    TagsModelDetails `json:"details"`
}

// TagsModelDetails mirrors Ollama's per-model details block.
type TagsModelDetails struct {
	ParentModel       string `json:"parent_model,omitempty"`
	Format            string `json:"format,omitempty"`
	Family            string `json:"family,omitempty"`
	Families          string `json:"families,omitempty"`
	ParameterSize     string `json:"parameter_size,omitempty"`
	QuantizationLevel string `json:"quantization_level,omitempty"`
}

// Tags returns an http.HandlerFunc implementing GET /api/tags. It lists every
// locally-installed model from the on-disk registry.
func Tags(w http.ResponseWriter, _ *http.Request) {
	entries, err := registry.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	models := make([]TagsModel, 0, len(entries))
	for _, e := range entries {
		models = append(models, TagsModel{
			Name:       e.Name,
			ModifiedAt: e.Modified.UTC().Format("2006-01-02T15:04:05.999999Z"),
			Size:       e.Size,
			Digest:     e.Digest,
			Details: TagsModelDetails{
				Format:            e.Details.Format,
				Family:            e.Details.Family,
				Families:          e.Details.Families,
				ParameterSize:     e.Details.ParameterSize,
				QuantizationLevel: e.Details.QuantizationLevel,
			},
		})
	}
	writeJSON(w, http.StatusOK, TagsResponse{Models: models})
}
