// Package handlers contains the HTTP handler implementations for the Nova
// REST API. Every endpoint is Ollama-compatible in its request/response
// payload shapes; the OpenAI-compatible surface lives in package openai and is
// mounted alongside these handlers in api.Handler.
//
// All handlers write errors as {"error": "..."} bodies with an appropriate
// HTTP status code, mirroring Ollama's wire format. Streaming endpoints emit
// newline-delimited JSON (NDJSON) with Content-Type
// "application/x-ndjson"; OpenAI streams (handled in package openai) use
// "text/event-stream".
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/project-nova/nova/internal/llm"
)

// writeJSON encodes v as JSON and writes it with the given status code. The
// Content-Type is application/json. Any encoding error is logged via the
// recover middleware but cannot be returned to the client at this point.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Best-effort: the status code and headers are already sent.
		_, _ = fmt.Fprintf(w, `{"error":"encode failed: %s"}`, err.Error())
	}
}

// writeError emits an Ollama-shaped error body {"error": msg} with the given
// HTTP status code.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeNDJSON marshals v as a single JSON object followed by a newline and
// flushes the response writer. It is intended for streaming endpoints. The
// caller is responsible for setting Content-Type once at the start of the
// stream.
func writeNDJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// writeSSE writes a Server-Sent-Events "data:" line. The payload is marshaled
// as JSON. The caller sets Content-Type: text/event-stream once at the start.
func writeSSE(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
		return
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// writeSSEDone writes the terminal "data: [DONE]\n\n" sentinel used by
// OpenAI-compatible streaming endpoints.
func writeSSEDone(w http.ResponseWriter) {
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// startNDJSON marks the response as an NDJSON stream and returns a writer
// that supports per-line flushing.
func startNDJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
}

// startSSE marks the response as a Server-Sent-Events stream.
func startSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
}

// parseKeepAlive converts an Ollama-style keep-alive string into a Duration.
//
// Accepted forms:
//   - "5m", "30s", "1h", "500ms" — standard Go durations.
//   - "60"  — a bare integer is treated as seconds.
//   - "-1"  — keep the model resident forever (no expiry). Represented as
//     a very large duration.
//   - "0"   — unload immediately after the request completes.
//
// Empty string returns the zero Duration which the caller can interpret as
// "use the default".
func parseKeepAlive(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if s == "-1" {
		// Effectively "forever": ~292 years. The server treats this as
		// "do not expire" via the ExpiresAt.IsZero() check.
		return time.Duration(1<<63 - 1)
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second
	}
	return 0
}

// applyOptionsMap overlays values from a request's "options" object onto an
// llm.Options struct. It is intentionally reflect-free: a type switch over the
// known Ollama option keys keeps the code transparent and panic-proof.
//
// Unknown keys are silently ignored to mirror Ollama's permissive behaviour.
func applyOptionsMap(opts llm.Options, m map[string]any) llm.Options {
	if len(m) == 0 {
		return opts
	}
	for k, v := range m {
		switch strings.ToLower(k) {
		case "temperature":
			if f, ok := toFloat(v); ok {
				opts.Temperature = f
			}
		case "top_k":
			if i, ok := toInt(v); ok {
				opts.TopK = i
			}
		case "top_p":
			if f, ok := toFloat(v); ok {
				opts.TopP = f
			}
		case "num_ctx":
			if i, ok := toInt(v); ok {
				opts.NumCtx = i
			}
		case "num_batch":
			if i, ok := toInt(v); ok {
				opts.NumBatch = i
			}
		case "num_thread":
			if i, ok := toInt(v); ok {
				opts.NumThread = i
			}
		case "num_gpu":
			if i, ok := toInt(v); ok {
				opts.NumGpu = i
			}
		case "num_predict":
			if i, ok := toInt(v); ok {
				opts.NumPredict = i
			}
		case "repeat_penalty":
			if f, ok := toFloat(v); ok {
				opts.RepeatPenalty = f
			}
		case "repeat_last_n":
			if i, ok := toInt(v); ok {
				opts.RepeatLastN = i
			}
		case "seed":
			if i, ok := toInt(v); ok {
				opts.Seed = i
			}
		case "stop":
			switch t := v.(type) {
			case string:
				opts.Stop = append(opts.Stop, t)
			case []any:
				for _, s := range t {
					if str, ok := s.(string); ok {
						opts.Stop = append(opts.Stop, str)
					}
				}
			case []string:
				opts.Stop = append(opts.Stop, t...)
			}
		case "mirostat":
			if i, ok := toInt(v); ok {
				opts.Mirostat = i
			}
		case "mirostat_tau":
			if f, ok := toFloat(v); ok {
				opts.MirostatTau = f
			}
		case "mirostat_eta":
			if f, ok := toFloat(v); ok {
				opts.MirostatEta = f
			}
		case "keep_alive":
			// Accept string durations ("5m") or numeric seconds.
			if s, ok := v.(string); ok {
				opts.KeepAlive = parseKeepAlive(s)
			} else if i, ok := toInt(v); ok {
				opts.KeepAlive = time.Duration(i) * time.Second
			}
		case "penalize_newline":
			if b, ok := v.(bool); ok {
				opts.PenalizeNewline = b
			}
		case "f16_kv":
			if b, ok := v.(bool); ok {
				opts.F16KV = b
			}
		case "low_vram":
			if b, ok := v.(bool); ok {
				opts.LowVram = b
			}
		case "use_mlock":
			if b, ok := v.(bool); ok {
				opts.UseMLock = b
			}
		case "use_mmap":
			if b, ok := v.(bool); ok {
				opts.UseMMap = b
			}
		case "vocab_only":
			if b, ok := v.(bool); ok {
				opts.VocabOnly = b
			}
		case "flash_attention":
			if b, ok := v.(bool); ok {
				opts.FlashAttention = b
			}
		case "num_keep":
			if i, ok := toInt(v); ok {
				opts.NumKeep = i
			}
		case "typical_p":
			if f, ok := toFloat(v); ok {
				opts.TypicalP = f
			}
		case "presence_penalty":
			if f, ok := toFloat(v); ok {
				opts.PresencePenalty = f
			}
		case "frequency_penalty":
			if f, ok := toFloat(v); ok {
				opts.FrequencyPenalty = f
			}
		case "stream":
			if b, ok := v.(bool); ok {
				opts.Stream = b
			}
		}
	}
	return opts
}

// toFloat coerces v into a float64. JSON numbers come through as float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	}
	return 0, false
}

// toInt coerces v into an int. JSON numbers come through as float64.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

// nowRFC3339 returns the current time formatted as RFC3339 with the local
// timezone, matching Ollama's CreatedAt field shape.
func nowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}

// ---- exported wrappers for use by package openai ----

// WriteJSONPub is the exported form of writeJSON, for cross-package use.
func WriteJSONPub(w http.ResponseWriter, code int, v any) { writeJSON(w, code, v) }

// WriteSSEPub is the exported form of writeSSE.
func WriteSSEPub(w http.ResponseWriter, v any) { writeSSE(w, v) }

// WriteSSEDonePub is the exported form of writeSSEDone.
func WriteSSEDonePub(w http.ResponseWriter) { writeSSEDone(w) }

// StartSSEPub is the exported form of startSSE.
func StartSSEPub(w http.ResponseWriter) { startSSE(w) }

// DecodeJSONPub is the exported form of decodeJSON.
func DecodeJSONPub(r *http.Request, v any) error { return decodeJSON(r, v) }
