// Package model implements the on-disk model representation used by Project:Nova.
//
// It is a faithful re-implementation of the manifest/layer/modelfile concepts
// from Ollama, rebranded and simplified so it can be vendored without external
// dependencies. A "model" in Nova is:
//
//   - a Manifest (JSON) naming the layers that make up the model
//   - a set of Layers (content-addressed blobs under <models>/blobs)
//   - an optional Modelfile-derived config layer holding prompt templates,
//     parameters, system message, adapters, license, etc.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the manifest schema this build understands.
const SchemaVersion = 2

// MediaType constants for the different layer kinds.
const (
	MediaModelIndex    = "application/vnd.nova.model+binary"
	MediaModelTemplate = "application/vnd.nova.model.template+json"
	MediaModelParams   = "application/vnd.nova.model.params+json"
	MediaModelAdapter  = "application/vnd.nova.model.adapter+binary"
	MediaSystemPrompt  = "application/vnd.nova.model.system+json"
	MediaLicense       = "application/vnd.nova.model.license+text"
	MediaMessages      = "application/vnd.nova.model.messages+json"
)

// Layer is a single content-addressed artefact in a model.
type Layer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	// From holds the originating source (e.g. a Modelfile directive) — optional.
	From string `json:"from,omitempty"`
}

// Digest computes the sha256 digest ("sha256:...") of a reader's contents.
func Digest(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}

// Manifest describes a named, tagged model. It is the JSON document stored
// under <models>/manifests/<registry>/<namespace>/<model>/<tag>.
type Manifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	Name          string `json:"name"`
	Registry      string `json:"registry,omitempty"`
	// Digest of the manifest itself (filled on read/write).
	Digest   string    `json:"digest,omitempty"`
	Created  time.Time `json:"created"`
	Modified time.Time `json:"modified"`
	Layers   []Layer   `json:"layers"`
	// Config is the head layer (params/template/system). Kept for convenience.
	Config *Layer `json:"config,omitempty"`
}

// AddLayer appends a layer to the manifest.
func (m *Manifest) AddLayer(l Layer) {
	m.Layers = append(m.Layers, l)
	m.Modified = time.Now().UTC()
}

// LayerByMediaType returns the first layer matching a media type.
func (m *Manifest) LayerByMediaType(media string) (*Layer, bool) {
	for i := range m.Layers {
		if m.Layers[i].MediaType == media {
			return &m.Layers[i], true
		}
	}
	return nil, false
}

// LoadManifest reads and decodes a manifest from disk.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	return &m, nil
}

// Save writes the manifest as pretty JSON to the given path, creating any
// missing parent directories.
func (m *Manifest) Save(path string) error {
	if m.SchemaVersion == 0 {
		m.SchemaVersion = SchemaVersion
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// ErrManifestNotFound is returned when a model name/tag does not exist on disk.
var ErrManifestNotFound = errors.New("manifest not found")
