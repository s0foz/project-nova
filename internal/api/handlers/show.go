package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/project-nova/nova/internal/model"
	"github.com/project-nova/nova/internal/registry"
)

// ShowRequest is the JSON body accepted by POST /api/show.
type ShowRequest struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ShowResponse is the wire shape returned by POST /api/show.
type ShowResponse struct {
	License    string           `json:"license,omitempty"`
	Modelfile  string           `json:"modelfile"`
	Parameters string           `json:"parameters,omitempty"`
	Template   string           `json:"template,omitempty"`
	System     string           `json:"system,omitempty"`
	Details    TagsModelDetails `json:"details"`
	ModelInfo  map[string]any   `json:"model_info,omitempty"`
	ModifiedAt string           `json:"modified_at"`
}

// Show returns an http.HandlerFunc implementing POST /api/show.
//
// It reads the manifest for the named model, reconstructs a Modelfile string
// from the stored layers (template / system / params / license / messages),
// and returns it along with best-effort details. Unknown models yield 404.
func Show(w http.ResponseWriter, r *http.Request) {
	var req ShowRequest
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

	m, err := registry.ReadManifest(name)
	if err != nil {
		if errors.Is(err, model.ErrManifestNotFound) {
			writeError(w, http.StatusNotFound, "model '"+req.Name+"' not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := ShowResponse{
		ModifiedAt: m.Modified.UTC().Format("2006-01-02T15:04:05.999999Z"),
		ModelInfo:  map[string]any{},
	}

	// FROM line: first layer with a non-empty From field, else "unknown".
	from := "unknown"
	for _, l := range m.Layers {
		if l.From != "" {
			from = l.From
			break
		}
	}

	// Extract text/JSON layers from blobs.
	if l, ok := m.LayerByMediaType(model.MediaModelTemplate); ok {
		resp.Template = readBlobString(l.Digest)
	}
	if l, ok := m.LayerByMediaType(model.MediaSystemPrompt); ok {
		resp.System = readBlobString(l.Digest)
	}
	if l, ok := m.LayerByMediaType(model.MediaLicense); ok {
		resp.License = readBlobString(l.Digest)
	}
	if l, ok := m.LayerByMediaType(model.MediaModelParams); ok {
		raw := readBlobBytes(l.Digest)
		if len(raw) > 0 {
			resp.Parameters = formatParameters(raw)
		}
	}

	// Reconstruct the Modelfile text.
	var mf strings.Builder
	fmt.Fprintf(&mf, "FROM %s\n", from)
	if resp.Template != "" {
		fmt.Fprintf(&mf, "TEMPLATE %q\n", resp.Template)
	}
	if resp.System != "" {
		fmt.Fprintf(&mf, "SYSTEM %q\n", resp.System)
	}
	if resp.License != "" {
		fmt.Fprintf(&mf, "LICENSE %q\n", resp.License)
	}
	if resp.Parameters != "" {
		mf.WriteString(resp.Parameters)
	}
	if l, ok := m.LayerByMediaType(model.MediaMessages); ok {
		if msgs := readBlobBytes(l.Digest); len(msgs) > 0 {
			var list []model.Message
			if err := json.Unmarshal(msgs, &list); err == nil {
				for _, msg := range list {
					fmt.Fprintf(&mf, "MESSAGE %s %q\n", msg.Role, msg.Content)
				}
			}
		}
	}
	resp.Modelfile = mf.String()

	// Best-effort details from the manifest.
	for _, l := range m.Layers {
		if l.MediaType == model.MediaModelIndex {
			resp.Details.Format = "nova"
			resp.Details.Family = "nova-stub"
			resp.Details.ParameterSize = "unknown"
			resp.Details.QuantizationLevel = "q8_0"
		}
	}
	resp.Details.ParentModel = from

	writeJSON(w, http.StatusOK, resp)
}

// readBlobString opens a blob by digest and returns its contents as a string.
// On any error it returns the empty string.
func readBlobString(digest string) string {
	return string(readBlobBytes(digest))
}

// readBlobBytes opens a blob by digest and returns its contents. On any error
// it returns nil.
func readBlobBytes(digest string) []byte {
	if digest == "" {
		return nil
	}
	rc, err := registry.OpenBlob(digest)
	if err != nil {
		return nil
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return nil
	}
	return b
}

// formatParameters renders a params blob (JSON-encoded model.Parameters) as
// Modelfile PARAMETER directives.
func formatParameters(raw []byte) string {
	var p model.Parameters
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	var b strings.Builder
	if p.Temperature != nil {
		fmt.Fprintf(&b, "PARAMETER temperature %g\n", *p.Temperature)
	}
	if p.TopK != nil {
		fmt.Fprintf(&b, "PARAMETER top_k %d\n", *p.TopK)
	}
	if p.TopP != nil {
		fmt.Fprintf(&b, "PARAMETER top_p %g\n", *p.TopP)
	}
	if p.NumCtx != nil {
		fmt.Fprintf(&b, "PARAMETER num_ctx %d\n", *p.NumCtx)
	}
	if p.NumBatch != nil {
		fmt.Fprintf(&b, "PARAMETER num_batch %d\n", *p.NumBatch)
	}
	if p.NumThread != nil {
		fmt.Fprintf(&b, "PARAMETER num_thread %d\n", *p.NumThread)
	}
	if p.NumGpu != nil {
		fmt.Fprintf(&b, "PARAMETER num_gpu %d\n", *p.NumGpu)
	}
	if p.NumPredict != nil {
		fmt.Fprintf(&b, "PARAMETER num_predict %d\n", *p.NumPredict)
	}
	if p.RepeatPenalty != nil {
		fmt.Fprintf(&b, "PARAMETER repeat_penalty %g\n", *p.RepeatPenalty)
	}
	if p.RepeatLastN != nil {
		fmt.Fprintf(&b, "PARAMETER repeat_last_n %d\n", *p.RepeatLastN)
	}
	if p.Seed != nil {
		fmt.Fprintf(&b, "PARAMETER seed %d\n", *p.Seed)
	}
	for _, s := range p.Stop {
		fmt.Fprintf(&b, "PARAMETER stop %q\n", s)
	}
	if p.Mirostat != nil {
		fmt.Fprintf(&b, "PARAMETER mirostat %d\n", *p.Mirostat)
	}
	if p.MirostatTau != nil {
		fmt.Fprintf(&b, "PARAMETER mirostat_tau %g\n", *p.MirostatTau)
	}
	if p.MirostatEta != nil {
		fmt.Fprintf(&b, "PARAMETER mirostat_eta %g\n", *p.MirostatEta)
	}
	return b.String()
}
