package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/project-nova/nova/internal/model"
	"github.com/project-nova/nova/internal/registry"
)

// CreateRequest is the JSON body accepted by POST /api/create.
type CreateRequest struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Quantize  string `json:"quantize,omitempty"`
	Modelfile string `json:"modelfile,omitempty"`
	Stream    *bool  `json:"stream,omitempty"`
}

// CreateStatus is one streamed progress line for /api/create.
type CreateStatus struct {
	Status string `json:"status"`
}

// Create returns an http.HandlerFunc implementing POST /api/create.
//
// It accepts either an inline Modelfile string (modelfile field) or a path to
// a Modelfile on disk, parses it, writes the parsed template/system/params/
// license/messages as content-addressed blobs, and assembles a fresh manifest
// via registry.CreateManifest. The FROM directive is resolved against the
// local registry if it names an installed model, otherwise it is treated as
// a file path and ingested as a binary model layer.
func Create(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
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

	// Acquire the Modelfile source: inline string, else file path.
	var src io.Reader
	switch {
	case req.Modelfile != "":
		src = strings.NewReader(req.Modelfile)
	case req.Path != "":
		f, err := os.Open(req.Path)
		if err != nil {
			writeError(w, http.StatusBadRequest, "open modelfile: "+err.Error())
			return
		}
		defer f.Close()
		src = f
	default:
		writeError(w, http.StatusBadRequest, "modelfile or path is required")
		return
	}

	mf, err := model.ParseModelfile(src)
	if err != nil {
		writeError(w, http.StatusBadRequest, "parse modelfile: "+err.Error())
		return
	}

	streaming := req.Stream == nil || *req.Stream
	if streaming {
		startNDJSON(w)
	}
	emit := func(s CreateStatus) {
		if streaming {
			writeNDJSON(w, s)
		}
	}

	emit(CreateStatus{Status: "reading modelfile"})

	layers := make([]model.Layer, 0, 8)

	// Resolve FROM: try local manifest first, else treat as file path.
	if mf.From != "" {
		if fromName, perr := registry.Parse(mf.From); perr == nil && registry.Exists(fromName) {
			if base, rerr := registry.ReadManifest(fromName); rerr == nil {
				if l, ok := base.LayerByMediaType(model.MediaModelIndex); ok {
					layers = append(layers, model.Layer{
						MediaType: model.MediaModelIndex,
						Digest:    l.Digest,
						Size:      l.Size,
						From:      mf.From,
					})
				}
			}
		} else if mf.From != "" {
			// Try to ingest as a file on disk.
			if f, ferr := os.Open(mf.From); ferr == nil {
				digest, size, derr := registry.CreateBlob(f)
				f.Close()
				if derr != nil {
					if streaming {
						writeNDJSON(w, map[string]string{"error": "ingest FROM: " + derr.Error()})
						return
					}
					writeError(w, http.StatusInternalServerError, "ingest FROM: "+derr.Error())
					return
				}
				layers = append(layers, model.Layer{
					MediaType: model.MediaModelIndex,
					Digest:    digest,
					Size:      size,
					From:      mf.From,
				})
			}
		}
	}

	emit(CreateStatus{Status: "writing layers"})

	// Params layer (JSON-encoded model.Parameters).
	if hasParams(mf.Params) {
		b, _ := json.Marshal(mf.Params)
		digest, size, err := writeBlobFromReader(bytes.NewReader(b))
		if err == nil {
			layers = append(layers, model.Layer{
				MediaType: model.MediaModelParams,
				Digest:    digest,
				Size:      size,
			})
		}
	}

	// Template layer.
	if mf.Template != "" {
		digest, size, err := writeBlobFromReader(strings.NewReader(mf.Template))
		if err == nil {
			layers = append(layers, model.Layer{
				MediaType: model.MediaModelTemplate,
				Digest:    digest,
				Size:      size,
			})
		}
	}

	// System prompt layer.
	if mf.System != "" {
		digest, size, err := writeBlobFromReader(strings.NewReader(mf.System))
		if err == nil {
			layers = append(layers, model.Layer{
				MediaType: model.MediaSystemPrompt,
				Digest:    digest,
				Size:      size,
			})
		}
	}

	// License layer.
	if mf.License != "" {
		digest, size, err := writeBlobFromReader(strings.NewReader(mf.License))
		if err == nil {
			layers = append(layers, model.Layer{
				MediaType: model.MediaLicense,
				Digest:    digest,
				Size:      size,
			})
		}
	}

	// Messages layer.
	if len(mf.Messages) > 0 {
		b, _ := json.Marshal(mf.Messages)
		digest, size, err := writeBlobFromReader(bytes.NewReader(b))
		if err == nil {
			layers = append(layers, model.Layer{
				MediaType: model.MediaMessages,
				Digest:    digest,
				Size:      size,
			})
		}
	}

	// Adapter layers.
	for _, a := range mf.Adapters {
		if f, ferr := os.Open(a); ferr == nil {
			digest, size, derr := registry.CreateBlob(f)
			f.Close()
			if derr == nil {
				layers = append(layers, model.Layer{
					MediaType: model.MediaModelAdapter,
					Digest:    digest,
					Size:      size,
					From:      a,
				})
			}
		}
	}

	emit(CreateStatus{Status: "writing manifest"})

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_ = ctx

	if _, err := registry.CreateManifest(name, layers); err != nil {
		if streaming {
			writeNDJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	emit(CreateStatus{Status: "success"})

	if !streaming {
		writeJSON(w, http.StatusOK, CreateStatus{Status: "success"})
	}
}

// writeBlobFromReader is a thin wrapper around registry.CreateBlob that returns
// the digest and size of the written blob.
func writeBlobFromReader(r io.Reader) (string, int64, error) {
	return registry.CreateBlob(r)
}

// hasParams reports whether any parameter field is set on p.
func hasParams(p model.Parameters) bool {
	return p.Temperature != nil || p.TopK != nil || p.TopP != nil ||
		p.NumCtx != nil || p.NumBatch != nil || p.NumThread != nil ||
		p.NumGpu != nil || p.NumPredict != nil || p.RepeatPenalty != nil ||
		p.RepeatLastN != nil || p.Seed != nil || len(p.Stop) > 0 ||
		p.Mirostat != nil || p.MirostatTau != nil || p.MirostatEta != nil ||
		p.PenalizeNewline != nil || p.F16KV != nil || p.LowVram != nil ||
		p.UseMLock != nil || p.UseMMap != nil || p.VocabOnly != nil ||
		p.FlashAttention != nil || p.NumKeep != nil || p.TypicalP != nil ||
		p.PresencePenalty != nil || p.FrequencyPenalty != nil
}
