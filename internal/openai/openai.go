// Package openai implements OpenAI-compatible HTTP endpoints that ride on top
// of the Nova orchestrator. The routes are mounted alongside the native
// /api/* routes by api.Handler.
//
// Endpoints provided:
//
//	POST /v1/chat/completions
//	POST /v1/completions
//	POST /v1/embeddings
//	GET  /v1/models
//	GET  /v1/models/{model}
//
// Streaming responses use Server-Sent Events ("text/event-stream") with the
// OpenAI chunk schema and a terminal "data: [DONE]" sentinel. Non-streaming
// responses return a single JSON object with the OpenAI completion shape.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/project-nova/nova/internal/api/handlers"
	"github.com/project-nova/nova/internal/llm"
	"github.com/project-nova/nova/internal/model"
	"github.com/project-nova/nova/internal/registry"
	"github.com/project-nova/nova/internal/server"
)

// ChatMessage is the OpenAI chat-message shape.
type ChatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []llm.ToolCall `json:"tool_calls,omitempty"`
}

// ChatRequest is the body accepted by POST /v1/chat/completions.
type ChatRequest struct {
	Model       string          `json:"model"`
	Messages    []ChatMessage   `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Stop        json.RawMessage `json:"stop,omitempty"`
	User        string          `json:"user,omitempty"`
	N           int             `json:"n,omitempty"`
}

// CompletionRequest is the body accepted by POST /v1/completions (legacy).
type CompletionRequest struct {
	Model       string          `json:"model"`
	Prompt      json.RawMessage `json:"prompt"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Suffix      string          `json:"suffix,omitempty"`
	User        string          `json:"user,omitempty"`
	N           int             `json:"n,omitempty"`
}

// EmbeddingsRequest is the body accepted by POST /v1/embeddings.
type EmbeddingsRequest struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"`
}

// Register wires all OpenAI-compatible routes onto the given ServeMux. It is
// called by api.Handler during route setup.
func Register(mux *http.ServeMux, srv *server.Server) {
	mux.HandleFunc("POST /v1/chat/completions", ChatCompletions(srv))
	mux.HandleFunc("POST /v1/completions", Completions(srv))
	mux.HandleFunc("POST /v1/embeddings", Embeddings(srv))
	mux.HandleFunc("GET /v1/models", ListModels)
	mux.HandleFunc("GET /v1/models/{model}", GetModel)
}

// ChatCompletions returns an http.HandlerFunc implementing
// POST /v1/chat/completions.
func ChatCompletions(srv *server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := decodeJSON(r, &req); err != nil {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if req.Model == "" {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
			return
		}
		name, err := registry.Parse(req.Model)
		if err != nil {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}

		opts := llm.DefaultOptions()
		if req.Temperature != nil {
			opts.Temperature = *req.Temperature
		}
		if req.TopP != nil {
			opts.TopP = *req.TopP
		}
		if req.MaxTokens != nil {
			opts.NumPredict = *req.MaxTokens
		}
		if len(req.Stop) > 0 {
			opts.Stop = appendStops(opts.Stop, req.Stop)
		}
		opts.Stream = req.Stream

		msgs := make([]llm.Message, 0, len(req.Messages))
		for _, m := range req.Messages {
			role := m.Role
			if role == "" {
				role = llm.RoleUser
			}
			msgs = append(msgs, llm.Message{Role: role, Content: m.Content, ToolCalls: m.ToolCalls})
		}

		created := time.Now().Unix()
		id := "chatcmpl-" + randID()

		if req.Stream {
			startSSE(w)
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			err = srv.Chat(ctx, name, msgs, opts, func(tok llm.Token) error {
				chunk := chatChunk{
					ID:      id,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   req.Model,
					Choices: []chatChoice{{
						Index:        0,
						Delta:        chatDelta{Role: llm.RoleAssistant, Content: tok.Content},
						FinishReason: nil,
					}},
				}
				if tok.Done {
					reason := tok.DoneReason
					if reason == "" {
						reason = "stop"
					}
					chunk.Choices[0].FinishReason = &reason
					chunk.Choices[0].Delta.Content = ""
				}
				writeSSE(w, chunk)
				return nil
			})
			if err != nil {
				writeSSE(w, map[string]any{"error": err.Error()})
			}
			writeSSEDone(w)
			return
		}

		// Non-streaming: aggregate.
		start := time.Now()
		var buf strings.Builder
		var lastToken llm.Token
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		err = srv.Chat(ctx, name, msgs, opts, func(tok llm.Token) error {
			lastToken = tok
			buf.WriteString(tok.Content)
			return nil
		})
		if err != nil {
			if errors.Is(err, model.ErrManifestNotFound) {
				writeOAError(w, http.StatusNotFound, "invalid_request_error", "model '"+req.Model+"' not found")
				return
			}
			writeOAError(w, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		_ = start
		finish := "stop"
		if lastToken.DoneReason != "" {
			finish = lastToken.DoneReason
		}
		resp := chatCompletion{
			ID:      id,
			Object:  "chat.completion",
			Created: created,
			Model:   req.Model,
			Choices: []chatChoiceFull{{
				Index:        0,
				Message:      ChatMessage{Role: llm.RoleAssistant, Content: buf.String()},
				FinishReason: finish,
			}},
			Usage: usage{
				PromptTokens:     lastToken.PromptEvalCount,
				CompletionTokens: lastToken.EvalCount,
				TotalTokens:      lastToken.PromptEvalCount + lastToken.EvalCount,
			},
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// Completions returns an http.HandlerFunc implementing POST /v1/completions.
func Completions(srv *server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CompletionRequest
		if err := decodeJSON(r, &req); err != nil {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if req.Model == "" {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
			return
		}
		name, err := registry.Parse(req.Model)
		if err != nil {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}

		prompts, err := normalisePrompts(req.Prompt)
		if err != nil {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if len(prompts) == 0 {
			prompts = []string{""}
		}

		opts := llm.DefaultOptions()
		if req.Temperature != nil {
			opts.Temperature = *req.Temperature
		}
		if req.TopP != nil {
			opts.TopP = *req.TopP
		}
		if req.MaxTokens != nil {
			opts.NumPredict = *req.MaxTokens
		}
		opts.Stream = req.Stream

		created := time.Now().Unix()
		id := "cmpl-" + randID()

		if req.Stream {
			startSSE(w)
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			// Legacy completions only supports a single prompt in stream mode.
			err = srv.Generate(ctx, name, prompts[0], nil, opts, func(tok llm.Token) error {
				chunk := completionChunk{
					ID:      id,
					Object:  "text_completion",
					Created: created,
					Model:   req.Model,
					Choices: []completionChoice{{
						Index:        0,
						Text:         tok.Content,
						FinishReason: nil,
					}},
				}
				if tok.Done {
					reason := tok.DoneReason
					if reason == "" {
						reason = "stop"
					}
					chunk.Choices[0].FinishReason = &reason
				}
				writeSSE(w, chunk)
				return nil
			})
			if err != nil {
				writeSSE(w, map[string]any{"error": err.Error()})
			}
			writeSSEDone(w)
			return
		}

		// Non-streaming: aggregate.
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		var buf strings.Builder
		var lastToken llm.Token
		err = srv.Generate(ctx, name, prompts[0], nil, opts, func(tok llm.Token) error {
			lastToken = tok
			buf.WriteString(tok.Content)
			return nil
		})
		if err != nil {
			if errors.Is(err, model.ErrManifestNotFound) {
				writeOAError(w, http.StatusNotFound, "invalid_request_error", "model '"+req.Model+"' not found")
				return
			}
			writeOAError(w, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		finish := "stop"
		if lastToken.DoneReason != "" {
			finish = lastToken.DoneReason
		}
		resp := completionFull{
			ID:      id,
			Object:  "text_completion",
			Created: created,
			Model:   req.Model,
			Choices: []completionChoiceFull{{
				Index:        0,
				Text:         buf.String(),
				FinishReason: finish,
			}},
			Usage: usage{
				PromptTokens:     lastToken.PromptEvalCount,
				CompletionTokens: lastToken.EvalCount,
				TotalTokens:      lastToken.PromptEvalCount + lastToken.EvalCount,
			},
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// Embeddings returns an http.HandlerFunc implementing POST /v1/embeddings.
func Embeddings(srv *server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req EmbeddingsRequest
		if err := decodeJSON(r, &req); err != nil {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if req.Model == "" {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
			return
		}
		name, err := registry.Parse(req.Model)
		if err != nil {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}

		inputs, err := normalisePrompts(req.Input)
		if err != nil {
			writeOAError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		if len(inputs) == 0 {
			inputs = []string{""}
		}

		opts := llm.DefaultOptions()
		vecs, err := srv.Embed(r.Context(), name, inputs, opts)
		if err != nil {
			if errors.Is(err, model.ErrManifestNotFound) {
				writeOAError(w, http.StatusNotFound, "invalid_request_error", "model '"+req.Model+"' not found")
				return
			}
			writeOAError(w, http.StatusInternalServerError, "server_error", err.Error())
			return
		}

		data := make([]embeddingData, len(vecs))
		var total int
		for i, v := range vecs {
			data[i] = embeddingData{
				Object:    "embedding",
				Index:     i,
				Embedding: v,
			}
			total += len(v)
		}
		resp := embeddingsResponse{
			Object: "list",
			Data:   data,
			Model:  req.Model,
			Usage: usage{
				PromptTokens:     len(inputs),
				CompletionTokens: 0,
				TotalTokens:      len(inputs),
			},
		}
		_ = total
		writeJSON(w, http.StatusOK, resp)
	}
}

// ListModels implements GET /v1/models.
func ListModels(w http.ResponseWriter, r *http.Request) {
	entries, err := registry.List()
	if err != nil {
		writeOAError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	data := make([]modelObject, 0, len(entries))
	for _, e := range entries {
		data = append(data, modelObject{
			ID:      e.Name,
			Object:  "model",
			Created: e.Modified.Unix(),
			OwnedBy: "nova",
		})
	}
	writeJSON(w, http.StatusOK, listModelsResponse{Object: "list", Data: data})
}

// GetModel implements GET /v1/models/{model}.
func GetModel(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("model")
	if raw == "" {
		writeOAError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	name, err := registry.Parse(raw)
	if err != nil {
		writeOAError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	m, err := registry.ReadManifest(name)
	if err != nil {
		if errors.Is(err, model.ErrManifestNotFound) {
			writeOAError(w, http.StatusNotFound, "invalid_request_error", "model '"+raw+"' not found")
			return
		}
		writeOAError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, modelObject{
		ID:      name.String(),
		Object:  "model",
		Created: m.Created.Unix(),
		OwnedBy: "nova",
	})
}

// ---- response / wire shapes ----

type chatChunk struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Index        int       `json:"index"`
	Delta        chatDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type chatDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type chatCompletion struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []chatChoiceFull `json:"choices"`
	Usage   usage            `json:"usage"`
}

type chatChoiceFull struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type completionChunk struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []completionChoice `json:"choices"`
}

type completionChoice struct {
	Index        int     `json:"index"`
	Text         string  `json:"text"`
	FinishReason *string `json:"finish_reason"`
}

type completionFull struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []completionChoiceFull `json:"choices"`
	Usage   usage                  `json:"usage"`
}

type completionChoiceFull struct {
	Index        int    `json:"index"`
	Text         string `json:"text"`
	FinishReason string `json:"finish_reason"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type embeddingData struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type embeddingsResponse struct {
	Object string          `json:"object"`
	Data   []embeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  usage           `json:"usage"`
}

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type listModelsResponse struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

// ---- helpers ----

// writeOAError emits an OpenAI-shaped error body.
func writeOAError(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    kind,
			"code":    nil,
		},
	})
}

// writeJSON is the OpenAI package's local writeJSON (delegates to handlers').
func writeJSON(w http.ResponseWriter, code int, v any) { handlers.WriteJSONPub(w, code, v) }

// writeSSE delegates to handlers.writeSSE.
func writeSSE(w http.ResponseWriter, v any) { handlers.WriteSSEPub(w, v) }

// startSSE delegates to handlers.startSSE.
func startSSE(w http.ResponseWriter) { handlers.StartSSEPub(w) }

// writeSSEDone delegates to handlers.writeSSEDone.
func writeSSEDone(w http.ResponseWriter) { handlers.WriteSSEDonePub(w) }

// decodeJSON is the OpenAI package's local decoder.
func decodeJSON(r *http.Request, v any) error { return handlers.DecodeJSONPub(r, v) }

// normalisePrompts accepts a string or []string and returns []string.
func normalisePrompts(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return []string{""}, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}, nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		if len(arr) == 0 {
			return []string{""}, nil
		}
		return arr, nil
	}
	var anyArr []any
	if err := json.Unmarshal(raw, &anyArr); err == nil {
		out := make([]string, 0, len(anyArr))
		for _, v := range anyArr {
			out = append(out, toStr(v))
		}
		return out, nil
	}
	return nil, errors.New("prompt must be a string or array of strings")
}

// toStr is a permissive string coercion.
func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%g", t)
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

// appendStops merges the OpenAI "stop" field (string or []string) into opts.Stop.
func appendStops(existing []string, raw json.RawMessage) []string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return append(existing, s)
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return append(existing, arr...)
	}
	return existing
}

// randID returns a short pseudo-random ID. Good enough for correlation;
// not cryptographically unique.
func randID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
