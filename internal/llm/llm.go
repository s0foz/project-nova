// Package llm defines the inference runner abstraction used by Project:Nova.
//
// Nova is designed so that the actual LLM runtime (llama.cpp, a remote
// endpoint, or a future native engine) can be plugged in behind the Runner
// interface. The default implementation provided here is a "stub" runner that
// echoes input — it lets the entire CLI/API surface be exercised end-to-end
// without a real model backend. Swap in a real Runner (e.g. one that spawns a
// llama.cpp subprocess) without touching the API or CLI layers.
package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/project-nova/nova/internal/model"
)

// Role constants for chat messages.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is a single chat message exchanged with a runner.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Images is a list of base64-encoded image payloads (multimodal).
	Images []string `json:"images,omitempty"`
	// ToolCalls is the list of tool invocations the assistant wants to make.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall describes a function call requested by the assistant.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Options are inference-time options passed to a runner. They mirror the
// Modelfile parameters but are concrete values (no pointers) so they can be
// overridden per-request.
type Options struct {
	Temperature      float64
	TopK             int
	TopP             float64
	NumCtx           int
	NumBatch         int
	NumThread        int
	NumGpu           int
	NumPredict       int
	RepeatPenalty    float64
	RepeatLastN      int
	Seed             int
	Stop             []string
	Mirostat         int
	MirostatTau      float64
	MirostatEta      float64
	PenalizeNewline  bool
	Stream           bool
	F16KV            bool
	LowVram          bool
	UseMLock         bool
	UseMMap          bool
	VocabOnly        bool
	FlashAttention   bool
	NumKeep          int
	TypicalP         float64
	PresencePenalty  float64
	FrequencyPenalty float64
	KeepAlive        time.Duration
}

// DefaultOptions returns a sensible default option set.
func DefaultOptions() Options {
	return Options{
		Temperature:     0.8,
		TopK:            40,
		TopP:            0.9,
		NumCtx:          4096,
		NumBatch:        512,
		NumPredict:      -1,
		RepeatPenalty:   1.1,
		RepeatLastN:     64,
		Seed:            -1,
		Mirostat:        0,
		Stream:          false,
		NumKeep:         0,
		PenalizeNewline: true,
		UseMMap:         true,
		FlashAttention:  false,
		KeepAlive:       5 * time.Minute,
	}
}

// FromParameters converts a Modelfile's Parameters into a concrete Options,
// applying defaults for any unset field.
func FromParameters(p model.Parameters) Options {
	o := DefaultOptions()
	if p.Temperature != nil {
		o.Temperature = *p.Temperature
	}
	if p.TopK != nil {
		o.TopK = *p.TopK
	}
	if p.TopP != nil {
		o.TopP = *p.TopP
	}
	if p.NumCtx != nil {
		o.NumCtx = *p.NumCtx
	}
	if p.NumBatch != nil {
		o.NumBatch = *p.NumBatch
	}
	if p.NumThread != nil {
		o.NumThread = *p.NumThread
	}
	if p.NumGpu != nil {
		o.NumGpu = *p.NumGpu
	}
	if p.NumPredict != nil {
		o.NumPredict = *p.NumPredict
	}
	if p.RepeatPenalty != nil {
		o.RepeatPenalty = *p.RepeatPenalty
	}
	if p.RepeatLastN != nil {
		o.RepeatLastN = *p.RepeatLastN
	}
	if p.Seed != nil {
		o.Seed = *p.Seed
	}
	if len(p.Stop) > 0 {
		o.Stop = append([]string(nil), p.Stop...)
	}
	if p.Mirostat != nil {
		o.Mirostat = *p.Mirostat
	}
	if p.MirostatTau != nil {
		o.MirostatTau = *p.MirostatTau
	}
	if p.MirostatEta != nil {
		o.MirostatEta = *p.MirostatEta
	}
	if p.PenalizeNewline != nil {
		o.PenalizeNewline = *p.PenalizeNewline
	}
	if p.FlashAttention != nil {
		o.FlashAttention = *p.FlashAttention
	}
	if p.UseMMap != nil {
		o.UseMMap = *p.UseMMap
	}
	if p.NumKeep != nil {
		o.NumKeep = *p.NumKeep
	}
	if p.TypicalP != nil {
		o.TypicalP = *p.TypicalP
	}
	if p.PresencePenalty != nil {
		o.PresencePenalty = *p.PresencePenalty
	}
	if p.FrequencyPenalty != nil {
		o.FrequencyPenalty = *p.FrequencyPenalty
	}
	return o
}

// Token is a single streamed generation fragment.
type Token struct {
	// Content is the generated text for this token.
	Content string
	// Done is true on the final token of a generation.
	Done bool
	// DoneReason explains why generation stopped ("stop", "length", "load").
	DoneReason string
	// PromptEvalCount / EvalCount are token accounting numbers.
	PromptEvalCount int
	EvalCount       int
	// LoadDuration / TotalDuration are nanosecond timings.
	LoadDuration  int64
	TotalDuration int64
}

// Runner is the abstraction an inference backend must implement.
//
// Load reads a model's layers from the provided manifest and prepares the
// runner for Generate/Chat/Embed calls. Close unloads the model and frees
// resources. Implementations must be safe to call from multiple goroutines
// for the same loaded runner.
type Runner interface {
	// Load prepares the runner to serve a model. Calling Load on an already
	// loaded runner should be a cheap no-op (or a reload if the manifest
	// changed).
	Load(ctx context.Context, m *model.Manifest, opts Options) error

	// Loaded reports whether the runner currently has a model in memory.
	Loaded() bool

	// Generate produces a completion for a single prompt and streams tokens
	// to fn. If opts.Stream is false, the implementation may still call fn
	// once with the full output. Returning an error aborts generation.
	Generate(ctx context.Context, prompt string, images []string, opts Options, fn func(Token) error) error

	// Chat produces a completion for a conversation and streams tokens to fn.
	Chat(ctx context.Context, messages []Message, opts Options, fn func(Token) error) error

	// Embed returns embedding vectors for the inputs.
	Embed(ctx context.Context, inputs []string, opts Options) ([][]float32, error)

	// Tokenize returns the token IDs for a text input.
	Tokenize(ctx context.Context, text string) ([]int, error)

	// Detokenize returns the text for a list of token IDs.
	Detokenize(ctx context.Context, tokens []int) (string, error)

	// Count returns the token count for a text input.
	Count(ctx context.Context, text string) (int, error)

	// Close unloads the model and releases resources.
	Close() error

	// Stats returns a snapshot of runtime statistics (memory, runners, etc.).
	Stats() Stats
}

// Stats summarises runner resource usage.
type Stats struct {
	LoadedModels  int
	TotalVram     uint64
	TotalVramFree uint64
}

// ErrNotLoaded is returned when an operation requires a loaded model.
var ErrNotLoaded = errors.New("model not loaded")

// StubRunner is a default Runner implementation that echoes its input. It is
// useful for development, CI, and demonstrating the full Nova surface without
// a real model backend. Replace it with a llama.cpp-backed runner in production.
type StubRunner struct {
	mu       sync.Mutex
	loaded   bool
	manifest *model.Manifest
	opts     Options
}

// NewStubRunner returns a fresh StubRunner.
func NewStubRunner() *StubRunner { return &StubRunner{} }

// Load marks the stub as loaded.
func (s *StubRunner) Load(ctx context.Context, m *model.Manifest, opts Options) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = true
	s.manifest = m
	s.opts = opts
	return nil
}

// Loaded reports whether the stub has a model loaded.
func (s *StubRunner) Loaded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loaded
}

// Generate streams an echo of the prompt token-by-token.
func (s *StubRunner) Generate(ctx context.Context, prompt string, images []string, opts Options, fn func(Token) error) error {
	if !s.Loaded() {
		return ErrNotLoaded
	}
	start := time.Now()
	words := strings.Fields(prompt)
	if len(words) == 0 {
		words = []string{"(empty)"}
	}
	promptCount := len(words)
	out := fmt.Sprintf("[Nova stub] Echo: %s", strings.Join(words, " "))
	tokens := strings.Fields(out)
	for i, tok := range tokens {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		content := tok
		if i < len(tokens)-1 {
			content += " "
		}
		if err := fn(Token{
			Content: content,
			Done:    false,
		}); err != nil {
			return err
		}
		time.Sleep(5 * time.Millisecond) // simulate streaming latency
	}
	return fn(Token{
		Done:            true,
		DoneReason:      "stop",
		PromptEvalCount: promptCount,
		EvalCount:       len(tokens),
		TotalDuration:   time.Since(start).Nanoseconds(),
	})
}

// Chat streams an echo of the last user message.
func (s *StubRunner) Chat(ctx context.Context, messages []Message, opts Options, fn func(Token) error) error {
	if !s.Loaded() {
		return ErrNotLoaded
	}
	var lastUser string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			lastUser = messages[i].Content
			break
		}
	}
	return s.Generate(ctx, lastUser, nil, opts, fn)
}

// Embed returns deterministic pseudo-embeddings for the inputs.
func (s *StubRunner) Embed(ctx context.Context, inputs []string, opts Options) ([][]float32, error) {
	if !s.Loaded() {
		return nil, ErrNotLoaded
	}
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		vec := make([]float32, 64)
		for j := 0; j < 64; j++ {
			vec[j] = float32((len(in)*31+j*7)%100) / 100.0
		}
		out[i] = vec
	}
	return out, nil
}

// Tokenize splits on whitespace as a stand-in.
func (s *StubRunner) Tokenize(ctx context.Context, text string) ([]int, error) {
	words := strings.Fields(text)
	out := make([]int, len(words))
	for i := range words {
		out[i] = i
	}
	return out, nil
}

// Detokenize is a no-op stand-in.
func (s *StubRunner) Detokenize(ctx context.Context, tokens []int) (string, error) {
	return fmt.Sprintf("[detok %d tokens]", len(tokens)), nil
}

// Count returns the whitespace token count.
func (s *StubRunner) Count(ctx context.Context, text string) (int, error) {
	return len(strings.Fields(text)), nil
}

// Close unloads the stub.
func (s *StubRunner) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded = false
	s.manifest = nil
	return nil
}

// Stats reports the stub's resource usage.
func (s *StubRunner) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	loaded := 0
	if s.loaded {
		loaded = 1
	}
	return Stats{LoadedModels: loaded}
}

// EnsureReaderAll drains an io.Reader fully so callers using io.Copy with a
// limit can detect truncated input. Not strictly required by the interface but
// handy for backend implementations.
func EnsureReaderAll(r io.Reader, n int64) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}
