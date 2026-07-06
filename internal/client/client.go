package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/project-nova/nova/internal/env"
)

// Client is an HTTP client for a Nova server.
type Client struct {
	// BaseURL is the scheme+host+port of the Nova server (e.g. "http://127.0.0.1:11434").
	BaseURL string
	// HTTP is the underlying *http.Client used for outbound requests. Streaming
	// methods rely on a zero timeout so long-lived NDJSON streams are not killed.
	HTTP *http.Client
}

// New returns a Client targeting the given host (in "host:port" form). If host
// is empty, env.Host() is used.
func New(host string) *Client {
	if host == "" {
		host = env.Host()
	}
	return &Client{
		BaseURL: "http://" + host,
		HTTP:    &http.Client{},
	}
}

// Default returns a Client targeting env.Host().
func Default() *Client { return New("") }

// do builds and performs an HTTP request. body may be nil for GET/DELETE.
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/x-ndjson")
	}
	return c.HTTP.Do(req)
}

// check returns an error if the response status is not 2xx.
func check(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	snippet := strings.TrimSpace(string(b))
	if snippet == "" {
		return fmt.Errorf("nova: HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("nova: HTTP %d: %s", resp.StatusCode, snippet)
}

// doJSON performs a request and decodes a single JSON response into out.
// If out is nil, the body is read and discarded.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := check(resp); err != nil {
		return err
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// doStream performs a request and decodes NDJSON lines, calling fn for each
// parsed line. fn receives the raw line bytes; callers unmarshal into the
// appropriate type.
func (c *Client) doStream(ctx context.Context, method, path string, body any, fn func([]byte) error) error {
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := check(resp); err != nil {
		return err
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := fn(line); err != nil {
			return err
		}
	}
	return sc.Err()
}

// Generate calls POST /api/generate with NDJSON streaming. fn is invoked once
// per streamed token (and once more for the final "done" message).
func (c *Client) Generate(ctx context.Context, req GenerateRequest, fn func(GenerateResponse) error) error {
	return c.doStream(ctx, http.MethodPost, "/api/generate", req, func(b []byte) error {
		var r GenerateResponse
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		return fn(r)
	})
}

// Chat calls POST /api/chat with NDJSON streaming.
func (c *Client) Chat(ctx context.Context, req ChatRequest, fn func(ChatResponse) error) error {
	return c.doStream(ctx, http.MethodPost, "/api/chat", req, func(b []byte) error {
		var r ChatResponse
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		return fn(r)
	})
}

// Embed calls POST /api/embeddings and returns the embedding response.
func (c *Client) Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error) {
	var out EmbedResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/embeddings", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Pull calls POST /api/pull with NDJSON streaming progress.
func (c *Client) Pull(ctx context.Context, name string, fn func(ProgressResponse) error) error {
	return c.doStream(ctx, http.MethodPost, "/api/pull",
		map[string]any{"name": name, "stream": true},
		func(b []byte) error {
			var r ProgressResponse
			if err := json.Unmarshal(b, &r); err != nil {
				return err
			}
			return fn(r)
		})
}

// Push calls POST /api/push with NDJSON streaming progress.
func (c *Client) Push(ctx context.Context, name string, fn func(ProgressResponse) error) error {
	return c.doStream(ctx, http.MethodPost, "/api/push",
		map[string]any{"name": name, "stream": true},
		func(b []byte) error {
			var r ProgressResponse
			if err := json.Unmarshal(b, &r); err != nil {
				return err
			}
			return fn(r)
		})
}

// Create calls POST /api/create with NDJSON streaming progress.
func (c *Client) Create(ctx context.Context, req CreateRequest, fn func(ProgressResponse) error) error {
	return c.doStream(ctx, http.MethodPost, "/api/create", req, func(b []byte) error {
		var r ProgressResponse
		if err := json.Unmarshal(b, &r); err != nil {
			return err
		}
		return fn(r)
	})
}

// Show calls POST /api/show and returns model details.
func (c *Client) Show(ctx context.Context, req ShowRequest) (*ShowResponse, error) {
	var out ShowResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/show", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// List calls GET /api/tags and returns the installed models.
func (c *Client) List(ctx context.Context) (*ListResponse, error) {
	var out ListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/tags", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Running calls GET /api/ps and returns the currently-loaded models.
func (c *Client) Running(ctx context.Context) (*ListResponse, error) {
	var out ListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/ps", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Version calls GET /api/version and returns the server's version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/version", nil, &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// Delete calls DELETE /api/delete for the given model name.
func (c *Client) Delete(ctx context.Context, name string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/delete",
		map[string]string{"name": name}, nil)
}

// Copy calls POST /api/copy to duplicate source to destination.
func (c *Client) Copy(ctx context.Context, source, dest string) error {
	return c.doJSON(ctx, http.MethodPost, "/api/copy",
		map[string]string{"source": source, "destination": dest}, nil)
}
