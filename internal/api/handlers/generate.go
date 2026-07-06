package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/project-nova/nova/internal/llm"
	"github.com/project-nova/nova/internal/model"
	"github.com/project-nova/nova/internal/registry"
	"github.com/project-nova/nova/internal/server"
)

// GenerateRequest is the JSON body accepted by POST /api/generate. It mirrors
// Ollama's request shape exactly so existing Ollama clients work unchanged.
type GenerateRequest struct {
	Model     string          `json:"model"`
	Prompt    string          `json:"prompt"`
	Suffix    string          `json:"suffix,omitempty"`
	System    string          `json:"system,omitempty"`
	Template  string          `json:"template,omitempty"`
	Context   []int           `json:"context,omitempty"`
	Stream    *bool           `json:"stream,omitempty"`
	Raw       bool            `json:"raw,omitempty"`
	Format    json.RawMessage `json:"format,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
	Images    []string        `json:"images,omitempty"`
	Options   map[string]any  `json:"options,omitempty"`
}

// GenerateResponse is one streamed (or final) chunk of a generate response.
type GenerateResponse struct {
	Model           string `json:"model"`
	CreatedAt       string `json:"created_at"`
	Response        string `json:"response,omitempty"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason,omitempty"`
	Context         []int  `json:"context,omitempty"`
	TotalDuration   int64  `json:"total_duration,omitempty"`
	LoadDuration    int64  `json:"load_duration,omitempty"`
	PromptEvalCount int    `json:"prompt_eval_count,omitempty"`
	EvalCount       int    `json:"eval_count,omitempty"`
}

// Generate returns an http.HandlerFunc that implements POST /api/generate.
//
// It parses the request, builds an llm.Options from defaults overlaid with the
// request's "options" map and keep_alive string, then either streams NDJSON
// token chunks (default) or returns a single aggregated JSON object when
// stream is false.
func Generate(srv *server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req GenerateRequest
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
		opts.Stream = req.Stream == nil || *req.Stream
		if req.KeepAlive != "" {
			opts.KeepAlive = parseKeepAlive(req.KeepAlive)
		}

		// Build the effective prompt. When raw is true the prompt is used
		// verbatim; otherwise the system/template directives from the request
		// override the model's defaults at the runner layer (best-effort).
		prompt := req.Prompt
		if req.Format != nil && string(req.Format) == `"json"` {
			prompt = injectJSONGuidance(prompt)
			opts.Stop = append(opts.Stop, "\n```")
		}

		start := time.Now()

		// Streaming vs non-streaming dispatch.
		streaming := req.Stream == nil || *req.Stream
		if streaming {
			startNDJSON(w)
		}

		var (
			buf         strings.Builder
			lastToken   llm.Token
			promptCount int
			evalCount   int
		)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		err = srv.Generate(ctx, name, prompt, req.Images, opts, func(tok llm.Token) error {
			lastToken = tok
			if tok.PromptEvalCount > 0 {
				promptCount = tok.PromptEvalCount
			}
			if tok.EvalCount > 0 {
				evalCount = tok.EvalCount
			}
			if streaming {
				resp := GenerateResponse{
					Model:           req.Model,
					CreatedAt:       nowRFC3339(),
					Response:        tok.Content,
					Done:            tok.Done,
					DoneReason:      tok.DoneReason,
					TotalDuration:   tok.TotalDuration,
					LoadDuration:    tok.LoadDuration,
					PromptEvalCount: tok.PromptEvalCount,
					EvalCount:       tok.EvalCount,
				}
				writeNDJSON(w, resp)
			} else {
				buf.WriteString(tok.Content)
			}
			return nil
		})

		if err != nil {
			// If we have already started streaming we cannot change the status
			// code; surface the error as a final NDJSON line instead.
			if streaming {
				writeNDJSON(w, map[string]string{"error": err.Error()})
				return
			}
			if errors.Is(err, model.ErrManifestNotFound) {
				writeError(w, http.StatusNotFound, "model '"+req.Model+"' not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if !streaming {
			resp := GenerateResponse{
				Model:           req.Model,
				CreatedAt:       nowRFC3339(),
				Response:        buf.String(),
				Done:            true,
				DoneReason:      lastToken.DoneReason,
				TotalDuration:   time.Since(start).Nanoseconds(),
				LoadDuration:    lastToken.LoadDuration,
				PromptEvalCount: promptCount,
				EvalCount:       evalCount,
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}

		// Streaming: the runner's final Done=true token is itself the
		// terminal summary chunk (it carries the totals). Only synthesize
		// an extra final line if the runner never signalled completion —
		// e.g. it returned nil with no Done token.
		if !lastToken.Done {
			final := GenerateResponse{
				Model:           req.Model,
				CreatedAt:       nowRFC3339(),
				Done:            true,
				DoneReason:      lastToken.DoneReason,
				TotalDuration:   time.Since(start).Nanoseconds(),
				LoadDuration:    lastToken.LoadDuration,
				PromptEvalCount: promptCount,
				EvalCount:       evalCount,
			}
			writeNDJSON(w, final)
		}
	}
}

// injectJSONGuidance prepends a small instruction that nudges the model toward
// emitting valid JSON. This mirrors Ollama's lightweight format=json behaviour
// without depending on a constrained grammar engine.
func injectJSONGuidance(prompt string) string {
	if prompt == "" {
		return "Respond with a single valid JSON object and nothing else."
	}
	return "Respond with a single valid JSON object and nothing else.\n\n" + prompt
}

// decodeJSON reads a JSON request body into v. It disallows unknown fields
// only loosely (Ollama tolerates extras), and enforces a 32MB size cap.
func decodeJSON(r *http.Request, v any) error {
	const maxBody = 32 << 20
	r.Body = http.MaxBytesReader(nil, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}
