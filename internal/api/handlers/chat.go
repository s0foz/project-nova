package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/project-nova/nova/internal/llm"
	"github.com/project-nova/nova/internal/model"
	"github.com/project-nova/nova/internal/registry"
	"github.com/project-nova/nova/internal/server"
)

// ChatMessage is the wire shape of a single message in /api/chat.
type ChatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Images    []string       `json:"images,omitempty"`
	ToolCalls []llm.ToolCall `json:"tool_calls,omitempty"`
}

// ChatRequest is the JSON body accepted by POST /api/chat.
type ChatRequest struct {
	Model     string          `json:"model"`
	Messages  []ChatMessage   `json:"messages"`
	Stream    *bool           `json:"stream,omitempty"`
	Format    json.RawMessage `json:"format,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
	Tools     []any           `json:"tools,omitempty"`
	Options   map[string]any  `json:"options,omitempty"`
}

// ChatResponse is one streamed (or final) chunk of a chat response.
type ChatResponse struct {
	Model           string      `json:"model"`
	CreatedAt       string      `json:"created_at"`
	Message         ChatMessage `json:"message,omitempty"`
	Done            bool        `json:"done"`
	DoneReason      string      `json:"done_reason,omitempty"`
	TotalDuration   int64       `json:"total_duration,omitempty"`
	LoadDuration    int64       `json:"load_duration,omitempty"`
	PromptEvalCount int         `json:"prompt_eval_count,omitempty"`
	EvalCount       int         `json:"eval_count,omitempty"`
}

// Chat returns an http.HandlerFunc that implements POST /api/chat.
//
// It maps the incoming messages to llm.Message values, calls srv.Chat, and
// streams NDJSON assistant-message chunks (default) or returns a single
// aggregated JSON object when stream is false.
func Chat(srv *server.Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
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

		// Map wire messages to llm.Message values.
		msgs := make([]llm.Message, 0, len(req.Messages))
		for _, m := range req.Messages {
			role := m.Role
			if role == "" {
				role = llm.RoleUser
			}
			msgs = append(msgs, llm.Message{
				Role:      role,
				Content:   m.Content,
				Images:    m.Images,
				ToolCalls: m.ToolCalls,
			})
		}

		// JSON-format guidance: prepend a system instruction.
		if string(req.Format) == `"json"` {
			msgs = append([]llm.Message{{
				Role:    llm.RoleSystem,
				Content: "Respond with a single valid JSON object and nothing else.",
			}}, msgs...)
			opts.Stop = append(opts.Stop, "\n```")
		}

		start := time.Now()
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

		err = srv.Chat(ctx, name, msgs, opts, func(tok llm.Token) error {
			lastToken = tok
			if tok.PromptEvalCount > 0 {
				promptCount = tok.PromptEvalCount
			}
			if tok.EvalCount > 0 {
				evalCount = tok.EvalCount
			}
			if streaming {
				resp := ChatResponse{
					Model:     req.Model,
					CreatedAt: nowRFC3339(),
					Message: ChatMessage{
						Role:    llm.RoleAssistant,
						Content: tok.Content,
					},
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
			resp := ChatResponse{
				Model:     req.Model,
				CreatedAt: nowRFC3339(),
				Message: ChatMessage{
					Role:    llm.RoleAssistant,
					Content: buf.String(),
				},
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
		// terminal summary chunk. Only synthesize one if the runner did
		// not already signal completion. The synthesized chunk still
		// carries role=assistant to keep clients happy.
		if !lastToken.Done {
			final := ChatResponse{
				Model:           req.Model,
				CreatedAt:       nowRFC3339(),
				Message:         ChatMessage{Role: llm.RoleAssistant},
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
