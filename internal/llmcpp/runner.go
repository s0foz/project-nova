package llmcpp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/project-nova/nova/internal/env"
	"github.com/project-nova/nova/internal/llm"
	"github.com/project-nova/nova/internal/model"
	"github.com/project-nova/nova/internal/registry"
)

// Compile-time assertion that Runner satisfies the llm.Runner interface.
var _ llm.Runner = (*Runner)(nil)

// Env var overrides understood by this package.
const (
	envServerPath = "NOVA_LLAMA_SERVER_PATH"
	envServerArgs = "NOVA_LLAMA_SERVER_ARGS"
)

// Runner spawns llama.cpp's llama-server as a subprocess and proxies HTTP to
// its OpenAI-compatible API. The zero value is not usable; construct one with
// New. It implements the llm.Runner interface.
type Runner struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	baseURL    string // e.g. "http://127.0.0.1:52341"
	port       int
	manifest   *model.Manifest
	opts       llm.Options
	modelPath  string
	httpClient *http.Client
	closed     bool
	// logFile holds the os.File used as the subprocess's stdout/stderr; closed
	// in Close.
	logFile *os.File
}

// New returns a Runner ready to Load. The HTTP client is configured with a 30s
// timeout suitable for non-streaming requests; streaming requests use a fresh
// client without a timeout so long generations are not cut off.
func New() *Runner {
	return &Runner{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// FindServer locates the llama-server binary. It searches, in order:
// NOVA_LLAMA_SERVER_PATH, alongside the current executable (and a bin/
// subdirectory), then the system PATH. It returns the first match, or an
// error describing what was tried.
func FindServer() (string, error) {
	if p := os.Getenv(envServerPath); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, name := range []string{"llama-server", "llama-server.exe"} {
			c := filepath.Join(dir, name)
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
		for _, name := range []string{"llama-server", "llama-server.exe"} {
			c := filepath.Join(dir, "bin", name)
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
	}
	if p, err := exec.LookPath("llama-server"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("llama-server.exe"); err == nil {
		return p, nil
	}
	return "", errors.New("llama-server not found; set NOVA_LLAMA_SERVER_PATH or install llama.cpp")
}

// Available reports whether a llama-server binary can be located. It is a
// convenience wrapper around FindServer for callers that only need a boolean.
func Available() bool {
	_, err := FindServer()
	return err == nil
}

// Load spawns llama-server with the model's weights blob and waits for it to
// report ready on GET /health. The model's GGUF file is resolved via
// registry.BlobPath from the manifest's index layer. It is safe to call only
// once per Runner; call Close before reusing the Runner.
func (r *Runner) Load(ctx context.Context, m *model.Manifest, opts llm.Options) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("llmcpp: runner is closed")
	}

	// Find the index layer (model weights).
	var indexLayer *model.Layer
	for i := range m.Layers {
		if m.Layers[i].MediaType == model.MediaModelIndex {
			indexLayer = &m.Layers[i]
			break
		}
	}
	if indexLayer == nil {
		return errors.New("no model weights layer in manifest")
	}
	modelPath := registry.BlobPath(indexLayer.Digest)
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("llmcpp: model weights blob not found at %s: %w", modelPath, err)
	}

	serverPath, err := FindServer()
	if err != nil {
		return err
	}

	// Allocate a free TCP port on the loopback interface.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("llmcpp: allocate port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	args := buildServerArgs(modelPath, port, opts)

	// Prepare the log file before starting the process so we capture startup
	// errors (missing libs, bad flags, etc.) even if the process exits fast.
	if err := os.MkdirAll(env.LogsDir(), 0o755); err != nil {
		return fmt.Errorf("llmcpp: create logs dir: %w", err)
	}
	logPath := filepath.Join(env.LogsDir(), fmt.Sprintf("llama-server-%d.log", port))
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("llmcpp: create log file: %w", err)
	}

	cmd := exec.Command(serverPath, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("llmcpp: start llama-server: %w", err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitForReady(ctx, baseURL); err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		logFile.Close()
		return err
	}

	// Swap in the live state. Close any previous log file (e.g. on reload).
	r.cmd = cmd
	r.baseURL = baseURL
	r.port = port
	r.manifest = m
	r.opts = opts
	r.modelPath = modelPath
	if r.logFile != nil {
		_ = r.logFile.Close()
	}
	r.logFile = logFile
	return nil
}

// buildServerArgs constructs the llama-server CLI args from the options. Only
// options that are explicitly set (non-zero, with the documented exceptions)
// are emitted so llama.cpp's defaults are preserved for the rest.
func buildServerArgs(modelPath string, port int, opts llm.Options) []string {
	args := []string{
		"--model", modelPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
	}
	if opts.NumCtx > 0 {
		args = append(args, "--ctx-size", strconv.Itoa(opts.NumCtx))
	}
	if opts.NumThread > 0 {
		args = append(args, "--threads", strconv.Itoa(opts.NumThread))
	}
	if opts.NumGpu > 0 {
		args = append(args, "-ngl", strconv.Itoa(opts.NumGpu))
	}
	if opts.Temperature > 0 {
		args = append(args, "--temp", strconv.FormatFloat(opts.Temperature, 'f', -1, 64))
	}
	if opts.TopK > 0 {
		args = append(args, "--top-k", strconv.Itoa(opts.TopK))
	}
	if opts.TopP > 0 {
		args = append(args, "--top-p", strconv.FormatFloat(opts.TopP, 'f', -1, 64))
	}
	if opts.RepeatPenalty > 0 {
		args = append(args, "--repeat-penalty", strconv.FormatFloat(opts.RepeatPenalty, 'f', -1, 64))
	}
	if opts.Seed >= 0 {
		args = append(args, "--seed", strconv.Itoa(opts.Seed))
	}
	if opts.FlashAttention {
		args = append(args, "--flash-attn")
	}
	if !opts.UseMMap {
		args = append(args, "--no-mmap")
	}
	if extra := os.Getenv(envServerArgs); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}
	return args
}

// waitForReady polls GET <baseURL>/health every 200ms up to 60s and returns
// nil once the server answers 200 OK. It honours ctx cancellation.
func waitForReady(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(60 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New("llmcpp: llama-server did not become ready within 60s")
		}
		<-ticker.C
	}
}

// Loaded reports whether the runner currently has a live llama-server process.
// A process is considered live if cmd is set and cmd.ProcessState is nil (i.e.
// Wait has not yet been called).
func (r *Runner) Loaded() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cmd != nil && r.cmd.ProcessState == nil
}

// Generate runs a single-prompt completion against llama-server's
// /v1/completions endpoint. If opts.Stream is true it streams Token chunks as
// they arrive; otherwise it returns the full text in a single Token.
func (r *Runner) Generate(ctx context.Context, prompt string, images []string, opts llm.Options, fn func(llm.Token) error) error {
	baseURL, ok := r.snapshot()
	if !ok {
		return llm.ErrNotLoaded
	}
	start := time.Now()
	body := buildCompletionBody(prompt, opts)
	if opts.Stream {
		return r.streamCompletion(ctx, baseURL+"/v1/completions", body, start, fn, false)
	}
	return r.completeOnce(ctx, baseURL+"/v1/completions", body, start, fn)
}

// Chat runs a conversation completion against /v1/chat/completions. Messages
// are mapped to llama-server's OpenAI-compatible wire format. Streaming
// behaviour mirrors Generate.
func (r *Runner) Chat(ctx context.Context, messages []llm.Message, opts llm.Options, fn func(llm.Token) error) error {
	baseURL, ok := r.snapshot()
	if !ok {
		return llm.ErrNotLoaded
	}
	start := time.Now()
	body := buildChatBody(messages, opts)
	if opts.Stream {
		return r.streamCompletion(ctx, baseURL+"/v1/chat/completions", body, start, fn, true)
	}
	return r.completeOnceChat(ctx, baseURL+"/v1/chat/completions", body, start, fn)
}

// Embed returns embedding vectors for inputs via POST /v1/embeddings. Vectors
// are returned in input order using the response's index field.
func (r *Runner) Embed(ctx context.Context, inputs []string, opts llm.Options) ([][]float32, error) {
	baseURL, ok := r.snapshot()
	if !ok {
		return nil, llm.ErrNotLoaded
	}
	body := map[string]interface{}{
		"model": "nova",
		"input": inputs,
	}
	resp, err := r.postJSON(ctx, baseURL+"/v1/embeddings", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, httpError(baseURL+"/v1/embeddings", resp)
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("llmcpp: decode embeddings response: %w", err)
	}
	vecs := make([][]float32, len(out.Data))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	for i := range vecs {
		if vecs[i] == nil {
			vecs[i] = []float32{}
		}
	}
	return vecs, nil
}

// Tokenize returns the token IDs for text via POST /tokenize. If llama-server
// is too old to provide /tokenize (404), it falls back to a whitespace split
// so callers (e.g. token counters) still get a usable estimate.
func (r *Runner) Tokenize(ctx context.Context, text string) ([]int, error) {
	baseURL, ok := r.snapshot()
	if !ok {
		return nil, llm.ErrNotLoaded
	}
	resp, err := r.postJSON(ctx, baseURL+"/tokenize", map[string]interface{}{"content": text})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return estimateTokens(text), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, httpError(baseURL+"/tokenize", resp)
	}
	var out struct {
		Tokens []int `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("llmcpp: decode tokenize response: %w", err)
	}
	return out.Tokens, nil
}

// Detokenize returns the text for a list of token IDs via POST /detokenize.
// On 404 (older llama-server builds) it returns a placeholder summary string.
func (r *Runner) Detokenize(ctx context.Context, tokens []int) (string, error) {
	baseURL, ok := r.snapshot()
	if !ok {
		return "", llm.ErrNotLoaded
	}
	resp, err := r.postJSON(ctx, baseURL+"/detokenize", map[string]interface{}{"tokens": tokens})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Sprintf("[%d tokens]", len(tokens)), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", httpError(baseURL+"/detokenize", resp)
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("llmcpp: decode detokenize response: %w", err)
	}
	return out.Content, nil
}

// Count returns the token count for text by delegating to Tokenize. The
// fallback in Tokenize means this also degrades gracefully on old
// llama-server builds.
func (r *Runner) Count(ctx context.Context, text string) (int, error) {
	toks, err := r.Tokenize(ctx, text)
	if err != nil {
		return 0, err
	}
	return len(toks), nil
}

// Close kills the llama-server subprocess and closes the log file. It is safe
// to call multiple times. The process is killed via Process.Kill which is
// portable across Linux and Windows.
func (r *Runner) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
		_ = r.cmd.Wait()
	}
	if r.logFile != nil {
		_ = r.logFile.Close()
		r.logFile = nil
	}
	r.cmd = nil
	return nil
}

// Stats returns a snapshot of runner resource usage. LoadedModels is 1 when a
// llama-server process is running, 0 otherwise; VRAM accounting is not yet
// wired up.
func (r *Runner) Stats() llm.Stats {
	if r.Loaded() {
		return llm.Stats{LoadedModels: 1}
	}
	return llm.Stats{}
}

// snapshot returns the baseURL and a loaded flag under the lock so callers can
// safely use the URL without holding the mutex during the HTTP round-trip.
func (r *Runner) snapshot() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd == nil || r.cmd.ProcessState != nil || r.closed {
		return "", false
	}
	return r.baseURL, true
}

// postJSON is a helper for non-streaming JSON POST requests. It uses the
// runner's httpClient (30s timeout) so slow requests still fail fast.
func (r *Runner) postJSON(ctx context.Context, url string, body interface{}) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return r.httpClient.Do(req)
}

// completeOnce handles non-streaming POST /v1/completions. It decodes the full
// JSON response and calls fn exactly once with the aggregated Token.
func (r *Runner) completeOnce(ctx context.Context, url string, body map[string]interface{}, start time.Time, fn func(llm.Token) error) error {
	resp, err := r.postJSON(ctx, url, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpError(url, resp)
	}
	var out struct {
		Choices []struct {
			Text         string `json:"text"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage usage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("llmcpp: decode completion response: %w", err)
	}
	text := ""
	doneReason := "stop"
	if len(out.Choices) > 0 {
		text = out.Choices[0].Text
		if out.Choices[0].FinishReason != "" {
			doneReason = out.Choices[0].FinishReason
		}
	}
	return fn(llm.Token{
		Content:         text,
		Done:            true,
		DoneReason:      doneReason,
		EvalCount:       out.Usage.CompletionTokens,
		PromptEvalCount: out.Usage.PromptTokens,
		TotalDuration:   time.Since(start).Nanoseconds(),
	})
}

// completeOnceChat handles non-streaming POST /v1/chat/completions. It reads
// choices[0].message.content and calls fn exactly once.
func (r *Runner) completeOnceChat(ctx context.Context, url string, body map[string]interface{}, start time.Time, fn func(llm.Token) error) error {
	resp, err := r.postJSON(ctx, url, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpError(url, resp)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage usage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("llmcpp: decode chat response: %w", err)
	}
	text := ""
	doneReason := "stop"
	if len(out.Choices) > 0 {
		text = out.Choices[0].Message.Content
		if out.Choices[0].FinishReason != "" {
			doneReason = out.Choices[0].FinishReason
		}
	}
	return fn(llm.Token{
		Content:         text,
		Done:            true,
		DoneReason:      doneReason,
		EvalCount:       out.Usage.CompletionTokens,
		PromptEvalCount: out.Usage.PromptTokens,
		TotalDuration:   time.Since(start).Nanoseconds(),
	})
}

// streamCompletion handles SSE streaming for both /v1/completions and
// /v1/chat/completions. isChat selects choices[0].delta.content vs
// choices[0].text. Each non-empty content chunk is delivered to fn; on
// terminal `data: [DONE]` or stream end, a single Done=true Token is
// delivered carrying the accumulated usage counts.
func (r *Runner) streamCompletion(ctx context.Context, url string, body map[string]interface{}, start time.Time, fn func(llm.Token) error, isChat bool) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	// Fresh client without a Timeout so long generations are not cut off; the
	// request's context handles cancellation.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpError(url, resp)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var evalCount, promptCount int
	doneReason := "stop"
	emittedDone := false

	emitDone := func() error {
		if emittedDone {
			return nil
		}
		emittedDone = true
		return fn(llm.Token{
			Done:            true,
			DoneReason:      doneReason,
			EvalCount:       evalCount,
			PromptEvalCount: promptCount,
			TotalDuration:   time.Since(start).Nanoseconds(),
		})
	}

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return emitDone()
		}
		var chunk struct {
			Choices []struct {
				Text  string `json:"text"`
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Skip malformed chunks rather than aborting the whole stream.
			continue
		}
		if len(chunk.Choices) > 0 {
			c := chunk.Choices[0]
			text := c.Text
			if isChat {
				text = c.Delta.Content
			}
			if text != "" {
				if err := fn(llm.Token{Content: text}); err != nil {
					return err
				}
			}
			if c.FinishReason != "" {
				doneReason = c.FinishReason
			}
		}
		if chunk.Usage.PromptTokens > 0 {
			promptCount = chunk.Usage.PromptTokens
		}
		if chunk.Usage.CompletionTokens > 0 {
			evalCount = chunk.Usage.CompletionTokens
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return emitDone()
}

// usage is the OpenAI-compatible usage object shared across endpoints.
type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// chatMessage is the wire form of llm.Message sent to /v1/chat/completions. It
// carries images and tool_calls so future multimodal / tool-calling work can
// light them up without changing the wire shape.
type chatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Images    []string       `json:"images,omitempty"`
	ToolCalls []llm.ToolCall `json:"tool_calls,omitempty"`
}

// buildCompletionBody constructs the /v1/completions JSON request. seed is
// omitted when negative; max_tokens is set to -1 for unlimited generation and
// omitted entirely for the NumPredict==0 case; stop is omitted when empty.
func buildCompletionBody(prompt string, opts llm.Options) map[string]interface{} {
	body := map[string]interface{}{
		"prompt":      prompt,
		"stream":      opts.Stream,
		"temperature": opts.Temperature,
		"top_p":       opts.TopP,
	}
	applySampling(body, opts)
	return body
}

// buildChatBody constructs the /v1/chat/completions JSON request, mapping each
// llm.Message to a chatMessage.
func buildChatBody(messages []llm.Message, opts llm.Options) map[string]interface{} {
	msgs := make([]chatMessage, len(messages))
	for i, m := range messages {
		msgs[i] = chatMessage{
			Role:      m.Role,
			Content:   m.Content,
			Images:    m.Images,
			ToolCalls: m.ToolCalls,
		}
	}
	body := map[string]interface{}{
		"messages":    msgs,
		"stream":      opts.Stream,
		"temperature": opts.Temperature,
		"top_p":       opts.TopP,
	}
	applySampling(body, opts)
	return body
}

// applySampling adds max_tokens, stop, and seed to a request body when set.
// NumPredict == -1 maps to max_tokens = -1 (unlimited); NumPredict == 0 (or
// other negatives) omits max_tokens entirely; positive values are passed
// through. Seed is included only when >= 0.
func applySampling(body map[string]interface{}, opts llm.Options) {
	if opts.NumPredict == -1 {
		body["max_tokens"] = -1
	} else if opts.NumPredict > 0 {
		body["max_tokens"] = opts.NumPredict
	}
	if len(opts.Stop) > 0 {
		body["stop"] = opts.Stop
	}
	if opts.Seed >= 0 {
		body["seed"] = opts.Seed
	}
}

// estimateTokens returns a whitespace-split stand-in token list for use when
// /tokenize is unavailable. The returned IDs are not real token IDs, but the
// slice length matches the input's word count, which is all callers like Count
// need.
func estimateTokens(text string) []int {
	words := strings.Fields(text)
	out := make([]int, len(words))
	for i := range words {
		out[i] = i
	}
	return out
}

// httpError reads up to 4 KB of the response body and returns a defensive
// error including the URL and a snippet. If the body is empty it falls back to
// the HTTP status code.
func httpError(url string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	snippet := strings.TrimSpace(string(body))
	if snippet == "" {
		snippet = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	if len(snippet) > 512 {
		snippet = snippet[:512]
	}
	return fmt.Errorf("llama-server %s: %s", url, snippet)
}
