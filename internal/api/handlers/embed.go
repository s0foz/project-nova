package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/project-nova/nova/internal/llm"
	"github.com/project-nova/nova/internal/model"
	"github.com/project-nova/nova/internal/registry"
	"github.com/project-nova/nova/internal/server"
)

// EmbeddingsRequest is the legacy /api/embeddings body.
type EmbeddingsRequest struct {
	Model     string         `json:"model"`
	Prompt    string         `json:"prompt"`
	Options   map[string]any `json:"options,omitempty"`
	KeepAlive string         `json:"keep_alive,omitempty"`
}

// EmbeddingsResponse is the legacy single-vector response.
type EmbeddingsResponse struct {
	Embedding []float32 `json:"embedding"`
}

// EmbedRequest is the newer /api/embed body. Input may be a string or a list
// of strings; both are normalised to []string internally.
type EmbedRequest struct {
	Model     string          `json:"model"`
	Input     json.RawMessage `json:"input"`
	Options   map[string]any  `json:"options,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
}

// EmbedResponse is the newer multi-vector response.
type EmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

// Embeddings returns an http.HandlerFunc implementing POST /api/embeddings,
// the legacy single-prompt endpoint.
func Embeddings(srv *server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req EmbeddingsRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Model == "" {
			writeError(w, http.StatusBadRequest, "model is required")
			return
		}
		name, err := registry.Parse(req.Model)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		opts := llm.DefaultOptions()
		opts = applyOptionsMap(opts, req.Options)
		if req.KeepAlive != "" {
			opts.KeepAlive = parseKeepAlive(req.KeepAlive)
		}

		vecs, err := srv.Embed(r.Context(), name, []string{req.Prompt}, opts)
		if err != nil {
			if errors.Is(err, model.ErrManifestNotFound) {
				writeError(w, http.StatusNotFound, "model '"+req.Model+"' not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var vec []float32
		if len(vecs) > 0 {
			vec = vecs[0]
		}
		writeJSON(w, http.StatusOK, EmbeddingsResponse{Embedding: vec})
	}
}

// Embed returns an http.HandlerFunc implementing POST /api/embed, the newer
// multi-input endpoint that accepts a string or list of strings as input.
func Embed(srv *server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req EmbedRequest
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Model == "" {
			writeError(w, http.StatusBadRequest, "model is required")
			return
		}
		name, err := registry.Parse(req.Model)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		inputs, err := normaliseEmbedInput(req.Input)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		opts := llm.DefaultOptions()
		opts = applyOptionsMap(opts, req.Options)
		if req.KeepAlive != "" {
			opts.KeepAlive = parseKeepAlive(req.KeepAlive)
		}

		vecs, err := srv.Embed(r.Context(), name, inputs, opts)
		if err != nil {
			if errors.Is(err, model.ErrManifestNotFound) {
				writeError(w, http.StatusNotFound, "model '"+req.Model+"' not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, EmbedResponse{Model: req.Model, Embeddings: vecs})
	}
}

// normaliseEmbedInput accepts either a JSON string or a JSON array of strings
// and returns a []string. An empty input yields [""].
func normaliseEmbedInput(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return []string{""}, nil
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil
	}
	// Try []string.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		if len(arr) == 0 {
			return []string{""}, nil
		}
		return arr, nil
	}
	// Try []any and coerce each element to string.
	var anyArr []any
	if err := json.Unmarshal(raw, &anyArr); err == nil {
		out := make([]string, 0, len(anyArr))
		for _, v := range anyArr {
			out = append(out, toStr(v))
		}
		return out, nil
	}
	return nil, errors.New("input must be a string or array of strings")
}

// toStr is a permissive string coercion for heterogeneous JSON arrays.
func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// JSON numbers unmarshal as float64.
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
