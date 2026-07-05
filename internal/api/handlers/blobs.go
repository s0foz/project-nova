package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/project-nova/nova/internal/registry"
)

// BlobStat returns an http.HandlerFunc implementing HEAD /api/blobs/{digest}.
//
// It reports 200 with Content-Length when the blob exists, or 404 otherwise.
// The digest must be of the form "sha256:...".
func BlobStat(w http.ResponseWriter, r *http.Request) {
	digest := r.PathValue("digest")
	if digest == "" {
		writeError(w, http.StatusBadRequest, "digest is required")
		return
	}
	digest = normaliseDigest(digest)
	fi, err := registry.BlobStat(digest)
	if err != nil {
		if os.IsNotExist(err) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Length", strconvInt64(fi.Size()))
	w.WriteHeader(http.StatusOK)
}

// BlobUpload returns an http.HandlerFunc implementing POST /api/blobs/{digest}.
//
// It accepts a raw blob body, verifies that the sha256 of the body matches the
// digest in the URL, and stores it via registry.CreateBlob. Returns 201 on
// success, 400 on digest mismatch, 409 if the blob already exists (still
// success-shaped to mirror Ollama which returns 200 in that case).
func BlobUpload(w http.ResponseWriter, r *http.Request) {
	digest := r.PathValue("digest")
	if digest == "" {
		writeError(w, http.StatusBadRequest, "digest is required")
		return
	}
	digest = normaliseDigest(digest)

	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 8<<30))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	h := sha256.Sum256(body)
	actual := "sha256:" + hex.EncodeToString(h[:])
	if !strings.EqualFold(actual, digest) {
		writeError(w, http.StatusBadRequest, "digest mismatch: expected "+digest+", got "+actual)
		return
	}

	// If the blob already exists, treat as success.
	if _, err := registry.BlobStat(digest); err == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Write via CreateBlob for dedup.
	if _, _, err := registry.CreateBlob(bytesReader(body)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// normaliseDigest ensures the digest carries a "sha256:" prefix. URLs may be
// sent without the colon (e.g. /api/blobs/sha256<hash>) which we tolerate.
func normaliseDigest(d string) string {
	d = strings.TrimSpace(d)
	if strings.HasPrefix(d, "sha256:") {
		return d
	}
	if strings.HasPrefix(d, "sha256") {
		return "sha256:" + d[len("sha256"):]
	}
	return "sha256:" + d
}

// bytesReader returns an io.Reader over b without pulling in bytes.NewReader
// at the call site (kept for symmetry with bytesReader below).
func bytesReader(b []byte) io.Reader { return &sliceReader{b: b} }

// sliceReader is a minimal io.Reader over a byte slice.
type sliceReader struct {
	b []byte
	i int
}

func (s *sliceReader) Read(p []byte) (int, error) {
	if s.i >= len(s.b) {
		return 0, io.EOF
	}
	n := copy(p, s.b[s.i:])
	s.i += n
	return n, nil
}

// strconvInt64 renders an int64 as a decimal string without importing strconv
// (kept self-contained to minimise the package's import surface).
func strconvInt64(n int64) string {
	if n == 0 {
		return "0"
	}
	var neg bool
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// errBlobMismatch is a sentinel used in tests (not currently asserted in the
// handler but kept for future use).
var errBlobMismatch = errors.New("digest mismatch")
