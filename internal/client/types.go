// Package client provides a small, dependency-free HTTP client for talking to
// a running Nova server. It mirrors the Ollama REST surface so the same CLI
// can drive either server.
package client

import "time"

// GenerateRequest is the body of POST /api/generate.
type GenerateRequest struct {
	Model     string         `json:"model"`
	Prompt    string         `json:"prompt,omitempty"`
	Suffix    string         `json:"suffix,omitempty"`
	System    string         `json:"system,omitempty"`
	Template  string         `json:"template,omitempty"`
	Context   []int          `json:"context,omitempty"`
	Stream    *bool          `json:"stream,omitempty"`
	Raw       bool           `json:"raw,omitempty"`
	Format    any            `json:"format,omitempty"`
	KeepAlive string         `json:"keep_alive,omitempty"`
	Images    []string       `json:"images,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}

// GenerateResponse (a.k.a. TokenResponse) is one NDJSON line of the
// /api/generate stream.
type GenerateResponse struct {
	Model           string `json:"model"`
	CreatedAt       string `json:"created_at"`
	Response        string `json:"response"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason,omitempty"`
	Context         []int  `json:"context,omitempty"`
	TotalDuration   int64  `json:"total_duration,omitempty"`
	LoadDuration    int64  `json:"load_duration,omitempty"`
	PromptEvalCount int    `json:"prompt_eval_count,omitempty"`
	EvalCount       int    `json:"eval_count,omitempty"`
	EvalDuration    int64  `json:"eval_duration,omitempty"`
}

// TokenResponse is an alias for GenerateResponse (kept for clarity when
// iterating a streaming Generate call).
type TokenResponse = GenerateResponse

// Message is a single chat message exchanged with the model.
type Message struct {
	Role      string   `json:"role"`
	Content   string   `json:"content"`
	Images    []string `json:"images,omitempty"`
	ToolCalls []any    `json:"tool_calls,omitempty"`
}

// ChatRequest is the body of POST /api/chat.
type ChatRequest struct {
	Model     string         `json:"model"`
	Messages  []Message      `json:"messages"`
	Stream    *bool          `json:"stream,omitempty"`
	Format    any            `json:"format,omitempty"`
	KeepAlive string         `json:"keep_alive,omitempty"`
	Tools     []any          `json:"tools,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}

// ChatResponse is one NDJSON line of the /api/chat stream.
type ChatResponse struct {
	Model           string  `json:"model"`
	CreatedAt       string  `json:"created_at"`
	Message         Message `json:"message"`
	Done            bool    `json:"done"`
	DoneReason      string  `json:"done_reason,omitempty"`
	TotalDuration   int64   `json:"total_duration,omitempty"`
	LoadDuration    int64   `json:"load_duration,omitempty"`
	PromptEvalCount int     `json:"prompt_eval_count,omitempty"`
	EvalCount       int     `json:"eval_count,omitempty"`
	EvalDuration    int64   `json:"eval_duration,omitempty"`
}

// EmbedRequest is the body of POST /api/embeddings.
type EmbedRequest struct {
	Model     string         `json:"model"`
	Input     any            `json:"input,omitempty"` // string or []string
	Prompt    string         `json:"prompt,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
	KeepAlive string         `json:"keep_alive,omitempty"`
}

// EmbedResponse is the response from POST /api/embeddings.
type EmbedResponse struct {
	Model         string      `json:"model"`
	Embeddings    [][]float32 `json:"embeddings"`
	TotalDuration int64       `json:"total_duration,omitempty"`
	LoadDuration  int64       `json:"load_duration,omitempty"`
}

// ProgressResponse is one NDJSON line of the /api/pull, /api/push and
// /api/create streams.
type ProgressResponse struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// CreateRequest is the body of POST /api/create.
type CreateRequest struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Quantize  string `json:"quantize,omitempty"`
	Modelfile string `json:"modelfile,omitempty"`
	Stream    *bool  `json:"stream,omitempty"`
}

// ShowRequest is the body of POST /api/show.
type ShowRequest struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ModelDetails summarises a model's family/format metadata.
type ModelDetails struct {
	ParentModel       string `json:"parent_model,omitempty"`
	Format            string `json:"format,omitempty"`
	Family            string `json:"family,omitempty"`
	Families          string `json:"families,omitempty"`
	ParameterSize     string `json:"parameter_size,omitempty"`
	QuantizationLevel string `json:"quantization_level,omitempty"`
}

// ShowResponse is the response from POST /api/show.
type ShowResponse struct {
	License    string         `json:"license,omitempty"`
	Modelfile  string         `json:"modelfile,omitempty"`
	Parameters string         `json:"parameters,omitempty"`
	Template   string         `json:"template,omitempty"`
	System     string         `json:"system,omitempty"`
	Details    ModelDetails   `json:"details,omitempty"`
	ModelInfo  map[string]any `json:"model_info,omitempty"`
	ModifiedAt time.Time      `json:"modified_at,omitempty"`
}

// ListModel is one row of GET /api/tags or GET /api/ps.
type ListModel struct {
	Name       string       `json:"name"`
	ModifiedAt time.Time    `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    ModelDetails `json:"details"`
	ExpiresAt  time.Time    `json:"expires_at,omitempty"`
	SizeVRAM   uint64       `json:"size_vram,omitempty"`
}

// ListResponse is the response from GET /api/tags and GET /api/ps.
type ListResponse struct {
	Models []ListModel `json:"models"`
}
