package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/project-nova/nova/internal/model"
)

// Progress is reported during a pull. Status is human-readable. When Total>0,
// Completed/Total give a byte-level progress bar. Digest is the blob digest
// once known.
type Progress struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// fetcher is a strategy for opening a model source as a byte stream.
//
// Implementations live in this file: hfFetcher (HuggingFace resolve URLs),
// httpFetcher (direct HTTP/HTTPS), and fileFetcher (local files).
type fetcher interface {
	// Open returns a reader for the model content, the total size in bytes
	// (-1 if unknown), and an error. The caller must close the reader when
	// finished.
	Open(ctx context.Context) (io.ReadCloser, int64, error)
	// Filename returns the base filename of the source, used to derive a
	// local model name when one is not supplied.
	Filename() string
	// String returns a human-readable identifier for the source.
	String() string
}

// httpUserAgent is the User-Agent header sent on every HTTP request. It
// identifies Nova to upstream model hosts so they can rate-limit / route
// appropriately.
const httpUserAgent = "Nova/0.1 (github.com/s0foz/project-nova)"

// httpClient is the shared HTTP client used for remote pulls. Models are
// large, so the timeout is generous; cancelled contexts still abort early.
var httpClient = &http.Client{
	Timeout: 30 * time.Minute,
}

// progressInterval is the minimum wall-clock gap between progress reports
// (unless the byte threshold is hit first).
const progressInterval = 250 * time.Millisecond

// progressByteThreshold is the minimum number of bytes that must flow between
// progress reports (unless the time threshold is hit first).
const progressByteThreshold = 256 * 1024

// PullModel downloads a model from source and registers it under name.
//
// source is one of:
//   - "hf:owner/repo/file.gguf"            -> HuggingFace resolve URL
//   - "https://host/path/model.gguf"        -> direct HTTPS
//   - "http://host/path/model.gguf"         -> direct HTTP (insecure)
//   - "file:///abs/path/model.gguf"         -> local file
//   - "/abs/path/model.gguf" or "C:\..."    -> local file (Windows path)
//
// name is the local model name to register (e.g. "llama3" or "myorg/qwen:7b").
// If name is empty, a name is derived from the source filename.
//
// progress is invoked with status updates as the pull proceeds; it may be nil.
// The returned Name is the local model name the manifest was registered under.
//
// All errors are wrapped with the originating source so callers can attribute
// failures: e.g. fmt.Errorf("pull %s: %w", source, err).
func PullModel(ctx context.Context, name string, source string, progress func(Progress)) (Name, error) {
	emit := func(p Progress) {
		if progress != nil {
			progress(p)
		}
	}

	f, err := parseSource(source)
	if err != nil {
		return Name{}, fmt.Errorf("pull %s: %w", source, err)
	}

	// Resolve the local model name.
	var n Name
	if name == "" {
		n, err = DeriveName(source)
		if err != nil {
			return Name{}, fmt.Errorf("pull %s: %w", source, err)
		}
	} else {
		n, err = Parse(name)
		if err != nil {
			return Name{}, fmt.Errorf("pull %s: %w", source, err)
		}
	}

	// Short-circuit if the model is already installed.
	if Exists(n) {
		emit(Progress{Status: "already exists"})
		return n, nil
	}

	emit(Progress{Status: "pulling manifest"})

	rc, size, err := f.Open(ctx)
	if err != nil {
		return Name{}, fmt.Errorf("pull %s: %w", source, err)
	}
	defer rc.Close()

	short := shortName(n.String())
	cr := &countingReader{
		r:          rc,
		total:      size,
		status:     "pulling " + short,
		progress:   emit,
		lastReport: time.Now(),
	}

	digest, written, err := CreateBlob(cr)
	if err != nil {
		return Name{}, fmt.Errorf("pull %s: %w", source, err)
	}

	// Flush a final progress update so UIs can render 100% before the
	// "verifying" stage takes over.
	if size > 0 {
		emit(Progress{Status: "pulling " + short, Total: size, Completed: written})
	}

	emit(Progress{Status: "verifying sha256", Digest: digest})
	emit(Progress{Status: "writing manifest"})

	layers := []model.Layer{{
		MediaType: model.MediaModelIndex,
		Digest:    digest,
		Size:      written,
		From:      source,
	}}
	if _, err := CreateManifest(n, layers); err != nil {
		return Name{}, fmt.Errorf("pull %s: %w", source, err)
	}

	emit(Progress{Status: "success"})
	return n, nil
}

// parseSource converts a source string into a fetcher. It is the single
// dispatch point that decides how a pull will be satisfied.
func parseSource(source string) (fetcher, error) {
	switch {
	case strings.HasPrefix(source, "hf:"):
		return newHFFetcher(source)
	case strings.HasPrefix(source, "https://"), strings.HasPrefix(source, "http://"):
		return &httpFetcher{raw: source}, nil
	case strings.HasPrefix(source, "file://"):
		return &fileFetcher{path: strings.TrimPrefix(source, "file://")}, nil
	}
	// Bare path: absolute Unix path or a Windows drive-letter path.
	if strings.HasPrefix(source, "/") || windowsDriveRegexp.MatchString(source) {
		return &fileFetcher{path: source}, nil
	}
	return nil, fmt.Errorf("unrecognised source %q (expected hf:, http(s)://, file://, or an absolute path)", source)
}

// windowsDriveRegexp matches a Windows drive-letter path prefix (e.g.
// "C:\..." or "C:/..."). It is also evaluated on non-Windows platforms so
// that a Windows-style source string is treated as a file path regardless of
// the host OS.
var windowsDriveRegexp = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// newHFFetcher builds an hfFetcher from an "hf:owner/repo/file" source.
//
// The part after "hf:" is split into owner, repo, and file (where file may
// itself contain subdirectory segments, e.g. "owner/repo/sub/file.gguf").
// The resulting URL is the canonical HuggingFace resolve endpoint:
//
//	https://huggingface.co/<owner>/<repo>/resolve/main/<file>
//
// If NOVA_HF_TOKEN or HF_TOKEN is set in the environment, an Authorization
// header will be attached to the request at Open time (helps with gated
// models).
func newHFFetcher(source string) (*hfFetcher, error) {
	spec := strings.TrimPrefix(source, "hf:")
	spec = strings.TrimPrefix(spec, "/")
	if spec == "" {
		return nil, errors.New("empty HuggingFace source")
	}
	parts := strings.SplitN(spec, "/", 3)
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("invalid HuggingFace source %q (want owner/repo/file)", source)
	}
	owner, repo, file := parts[0], parts[1], parts[2]
	u := &url.URL{
		Scheme: "https",
		Host:   "huggingface.co",
		Path:   fmt.Sprintf("/%s/%s/resolve/main/%s", owner, repo, file),
	}
	return &hfFetcher{
		raw:  source,
		url:  u.String(),
		file: filepath.Base(file),
	}, nil
}

// hfFetcher pulls a model from HuggingFace via the /resolve/main/ endpoint.
type hfFetcher struct {
	raw  string // original source string (for error attribution)
	url  string // fully-qualified resolve URL
	file string // base filename
}

// String returns the original source string.
func (h *hfFetcher) String() string { return h.raw }

// Filename returns the base filename of the model file.
func (h *hfFetcher) Filename() string { return h.file }

// Open issues a GET against the resolve URL and returns the response body.
// The size is taken from Content-Length (-1 if the host omits it).
func (h *hfFetcher) Open(ctx context.Context) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", httpUserAgent)
	if token := hfToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("huggingface returned %s", resp.Status)
	}
	return resp.Body, resp.ContentLength, nil
}

// hfToken returns a HuggingFace API token from the environment, preferring
// NOVA_HF_TOKEN over HF_TOKEN. Returns the empty string if neither is set.
func hfToken() string {
	if t := os.Getenv("NOVA_HF_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("HF_TOKEN")
}

// httpFetcher pulls a model from a direct HTTP or HTTPS URL.
type httpFetcher struct {
	raw string
}

// String returns the original URL.
func (h *httpFetcher) String() string { return h.raw }

// Filename returns the base name of the URL's path component.
func (h *httpFetcher) Filename() string {
	if u, err := url.Parse(h.raw); err == nil && u.Path != "" {
		if base := filepath.Base(u.Path); base != "" && base != "/" && base != "." {
			return base
		}
	}
	return filepath.Base(strings.TrimRight(h.raw, "/"))
}

// Open issues a GET against the URL and returns the response body. The size
// is taken from Content-Length (-1 if the host omits it).
func (h *httpFetcher) Open(ctx context.Context) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.raw, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", httpUserAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("HTTP %s", resp.Status)
	}
	return resp.Body, resp.ContentLength, nil
}

// fileFetcher reads a model from a local filesystem path.
type fileFetcher struct {
	path string
}

// String returns the local path.
func (f *fileFetcher) String() string { return f.path }

// Filename returns the base name of the path.
func (f *fileFetcher) Filename() string { return filepath.Base(f.path) }

// Open stats the file (for size) and returns an open *os.File for it.
func (f *fileFetcher) Open(ctx context.Context) (io.ReadCloser, int64, error) {
	fi, err := os.Stat(f.path)
	if err != nil {
		return nil, 0, err
	}
	rc, err := os.Open(f.path)
	if err != nil {
		return nil, 0, err
	}
	return rc, fi.Size(), nil
}

// DeriveName derives a model Name from a source string by taking the source
// filename, stripping common model extensions (.gguf, .bin, .safetensors),
// lowercasing, and replacing any character that is not a lowercase letter,
// digit, dot, underscore, or hyphen with "-". The result is parsed via Parse
// to fill in the default registry/namespace/tag.
func DeriveName(source string) (Name, error) {
	fname := sourceFilename(source)
	if fname == "" || fname == "." || fname == "/" {
		return Name{}, fmt.Errorf("cannot derive name from %q", source)
	}
	// Strip common model extensions (case-insensitive).
	lower := strings.ToLower(fname)
	for _, ext := range []string{".gguf", ".bin", ".safetensors"} {
		if strings.HasSuffix(lower, ext) {
			fname = fname[:len(fname)-len(ext)]
			break
		}
	}
	fname = strings.ToLower(fname)
	fname = invalidNameCharRegexp.ReplaceAllString(fname, "-")
	fname = strings.Trim(fname, "-")
	if fname == "" {
		return Name{}, fmt.Errorf("cannot derive name from %q", source)
	}
	return Parse(fname)
}

// sourceFilename extracts the base filename from a source string of any
// supported scheme.
func sourceFilename(source string) string {
	switch {
	case strings.HasPrefix(source, "hf:"):
		if h, err := newHFFetcher(source); err == nil {
			return h.file
		}
		return ""
	case strings.HasPrefix(source, "https://"), strings.HasPrefix(source, "http://"):
		if u, err := url.Parse(source); err == nil && u.Path != "" {
			return filepath.Base(u.Path)
		}
		return ""
	case strings.HasPrefix(source, "file://"):
		return filepath.Base(strings.TrimPrefix(source, "file://"))
	}
	return filepath.Base(source)
}

// invalidNameCharRegexp matches any character that is not permitted in a
// Nova model name (anything other than lowercase ASCII letters, digits,
// dots, underscores, and hyphens).
var invalidNameCharRegexp = regexp.MustCompile(`[^a-z0-9._-]`)

// countingReader wraps an io.Reader and periodically invokes a progress
// callback as bytes flow through. Reports are throttled to at most one every
// progressInterval OR every progressByteThreshold bytes, whichever comes
// first, to avoid flooding the callback on fast streams.
type countingReader struct {
	r        io.Reader
	total    int64
	status   string
	progress func(Progress)

	n          int64
	lastN      int64
	lastReport time.Time
}

// Read implements io.Reader. It forwards the underlying read and reports
// progress when a throttle window has elapsed.
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n += int64(n)
		c.maybeReport()
	}
	return n, err
}

// maybeReport invokes the progress callback if either the time or byte
// throttle has been exceeded. It is a no-op when no callback is set.
func (c *countingReader) maybeReport() {
	if c.progress == nil {
		return
	}
	now := time.Now()
	if c.n-c.lastN < progressByteThreshold && now.Sub(c.lastReport) < progressInterval {
		return
	}
	pr := Progress{Status: c.status, Completed: c.n}
	if c.total > 0 {
		pr.Total = c.total
	}
	c.progress(pr)
	c.lastN = c.n
	c.lastReport = now
}

// shortName trims a canonical model name down to the short identifier used in
// progress messages: everything after the last "/", truncated at the first
// ":". For example, "myorg/qwen:7b" -> "qwen".
func shortName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, ":"); i >= 0 {
		name = name[:i]
	}
	return name
}
