// Package registry implements on-disk model storage and naming for Project:Nova.
//
// It manages the manifest tree under <models>/manifests and the content-addressed
// blob store under <models>/blobs. Names follow Ollama's convention:
//
//	[registry/][namespace/]model[:tag]
//
// Examples:
//
//	nova.run/library/llama3:8b
//	quantum/qwen2:latest
//	myalias:latest
package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/project-nova/nova/internal/env"
	"github.com/project-nova/nova/internal/model"
)

// DefaultRegistry is the registry used when none is specified in a name.
const DefaultRegistry = "registry.nova.ai"

// DefaultNamespace is the namespace used when none is specified.
const DefaultNamespace = "library"

// DefaultTag is the tag used when none is specified.
const DefaultTag = "latest"

// Name is a parsed model name.
type Name struct {
	Registry  string
	Namespace string
	Model     string
	Tag       string
}

// Parse converts a raw model name string into a structured Name.
// Accepts forms: model, model:tag, ns/model, ns/model:tag,
// registry/ns/model, registry/ns/model:tag.
func Parse(raw string) (Name, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Name{}, errors.New("empty model name")
	}

	tag := DefaultTag
	if i := strings.LastIndex(raw, ":"); i >= 0 {
		// Make sure the colon isn't part of a port-like registry. A registry
		// alone (no slash) followed by ":tag" is ambiguous, so we only treat
		// the final colon as a tag separator when there is at least one slash
		// OR the suffix looks like a tag (no slashes, no dots that resemble
		// a host:port). To keep things simple and Ollama-compatible we split
		// on the LAST colon only.
		tag = raw[i+1:]
		raw = raw[:i]
		if tag == "" {
			tag = DefaultTag
		}
	}

	parts := strings.Split(raw, "/")
	switch len(parts) {
	case 1:
		return Name{Registry: DefaultRegistry, Namespace: DefaultNamespace, Model: parts[0], Tag: tag}, nil
	case 2:
		return Name{Registry: DefaultRegistry, Namespace: parts[0], Model: parts[1], Tag: tag}, nil
	case 3:
		return Name{Registry: parts[0], Namespace: parts[1], Model: parts[2], Tag: tag}, nil
	default:
		return Name{}, fmt.Errorf("invalid model name %q", raw)
	}
}

// String renders the canonical name (without the default registry prefix).
func (n Name) String() string {
	s := n.Model
	if n.Namespace != "" && n.Namespace != DefaultNamespace {
		s = n.Namespace + "/" + s
	} else if n.Registry != "" && n.Registry != DefaultRegistry {
		s = n.Namespace + "/" + s
	}
	if n.Tag != "" {
		s += ":" + n.Tag
	}
	return s
}

// FullPath returns the relative path under the manifests root.
func (n Name) FullPath() string {
	return filepath.Join(n.Registry, n.Namespace, n.Model, n.Tag)
}

// ManifestPath returns the absolute path to the manifest file on disk.
func (n Name) ManifestPath() string {
	return filepath.Join(env.ManifestsDir(), n.FullPath())
}

// CreateManifest builds and persists a manifest for the given name and layers.
func CreateManifest(name Name, layers []model.Layer) (*model.Manifest, error) {
	now := time.Now().UTC()
	m := &model.Manifest{
		SchemaVersion: model.SchemaVersion,
		Name:          name.String(),
		Registry:      name.Registry,
		Created:       now,
		Modified:      now,
		Layers:        layers,
	}
	if err := m.Save(name.ManifestPath()); err != nil {
		return nil, err
	}
	return m, nil
}

// ReadManifest loads a manifest for the given name from disk.
func ReadManifest(name Name) (*model.Manifest, error) {
	m, err := model.LoadManifest(name.ManifestPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, model.ErrManifestNotFound
		}
		return nil, err
	}
	return m, nil
}

// DeleteManifest removes a manifest (and its parent tag dir if empty).
func DeleteManifest(name Name) error {
	p := name.ManifestPath()
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return model.ErrManifestNotFound
		}
		return err
	}
	// Clean up empty model dirs up the tree (best-effort).
	for d := filepath.Dir(p); d != env.ManifestsDir() && d != "." && d != "/"; d = filepath.Dir(d) {
		entries, err := os.ReadDir(d)
		if err != nil || len(entries) > 0 {
			break
		}
		_ = os.Remove(d)
	}
	return nil
}

// CopyManifest duplicates a manifest under a new name (nova cp).
func CopyManifest(src, dst Name) error {
	m, err := ReadManifest(src)
	if err != nil {
		return err
	}
	m.Name = dst.String()
	m.Registry = dst.Registry
	m.Modified = time.Now().UTC()
	return m.Save(dst.ManifestPath())
}

// ListEntry is one row of `nova list`.
type ListEntry struct {
	Name     string    `json:"name"`
	ID       string    `json:"id"`   // short digest
	Size     int64     `json:"size"` // total bytes of all layers
	Modified time.Time `json:"modified"`
	Digest   string    `json:"digest"` // full digest of manifest
	Details  Details   `json:"details"`
}

// Details summarises a model's family/format metadata (best-effort).
type Details struct {
	Format            string `json:"format,omitempty"`
	Family            string `json:"family,omitempty"`
	Families          string `json:"families,omitempty"`
	ParameterSize     string `json:"parameter_size,omitempty"`
	QuantizationLevel string `json:"quantization_level,omitempty"`
}

// List returns every model installed locally, sorted by name.
func List() ([]ListEntry, error) {
	root := env.ManifestsDir()
	var out []ListEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		// rel = registry/namespace/model/tag
		name, err := parseFromRel(rel)
		if err != nil {
			return nil
		}
		m, err := model.LoadManifest(path)
		if err != nil {
			return nil
		}
		var size int64
		for _, l := range m.Layers {
			size += l.Size
		}
		out = append(out, ListEntry{
			Name:     name.String(),
			ID:       shortDigest(manifestDigest(m)),
			Size:     size,
			Modified: m.Modified,
			Digest:   manifestDigest(m),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Exists reports whether a manifest is present for the name.
func Exists(name Name) bool {
	_, err := os.Stat(name.ManifestPath())
	return err == nil
}

// CreateBlob writes a reader's contents into the blob store and returns the
// digest ("sha256:..."). Existing blobs are deduplicated.
func CreateBlob(r io.Reader) (string, int64, error) {
	h := sha256.New()
	tmp, err := os.CreateTemp(env.TmpDir(), "blob-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	w := io.MultiWriter(tmp, h)
	n, err := io.Copy(w, r)
	tmp.Close()
	if err != nil {
		return "", 0, err
	}
	digest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	dst := blobPath(digest)
	if _, err := os.Stat(dst); err == nil {
		return digest, n, nil // already present
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", 0, err
	}
	return digest, n, os.Rename(tmpPath, dst)
}

// OpenBlob opens a blob for reading by digest.
func OpenBlob(digest string) (io.ReadCloser, error) {
	return os.Open(blobPath(digest))
}

// BlobPath returns the absolute path of a blob.
func BlobPath(digest string) string { return blobPath(digest) }

// BlobStat returns os.FileInfo for a blob, or os.ErrNotExist.
func BlobStat(digest string) (os.FileInfo, error) {
	return os.Stat(blobPath(digest))
}

func blobPath(digest string) string {
	// "sha256:abcd..." -> blobs/sha256/abcd...
	alg, hash, ok := splitDigest(digest)
	if !ok {
		return filepath.Join(env.BlobsDir(), digest)
	}
	return filepath.Join(env.BlobsDir(), alg, hash)
}

func splitDigest(d string) (string, string, bool) {
	i := strings.Index(d, ":")
	if i < 0 {
		return "", "", false
	}
	return d[:i], d[i+1:], true
}

func parseFromRel(rel string) (Name, error) {
	// rel = registry/namespace/model/tag (forward slashes)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 4 {
		return Name{}, fmt.Errorf("bad manifest path %q", rel)
	}
	return Name{Registry: parts[0], Namespace: parts[1], Model: parts[2], Tag: parts[3]}, nil
}

func manifestDigest(m *model.Manifest) string {
	b, _ := json.Marshal(m)
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func shortDigest(d string) string {
	_, h, ok := splitDigest(d)
	if !ok {
		return d
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
