// Package env centralises runtime configuration and on-disk layout for Project:Nova.
//
// It mirrors Ollama's directory conventions but rebranded for Nova:
//
//	Models directory  : %USERPROFILE%\.nova\models  (or $NOVA_MODELS)
//	Blobs directory   : <models>/blobs
//	Manifests root    : <models>/manifests
//	Sockets / runtime : <models>/.tmp
//	Logs               : <models>/logs
package env

import (
	"os"
	"path/filepath"
	"runtime"
)

// Defaults that can be overridden by environment variables.
const (
	EnvModelsDir   = "NOVA_MODELS"
	EnvHost        = "NOVA_HOST"    // e.g. "127.0.0.1:11434"
	EnvOrigin      = "NOVA_ORIGINS" // comma-separated allowed CORS origins
	EnvDebug       = "NOVA_DEBUG"   // "1" / "true" enables verbose logging
	EnvMaxRunners  = "NOVA_MAX_RUNNERS"
	EnvMaxVram     = "NOVA_MAX_VRAM"
	EnvFlashAttn   = "NOVA_FLASH_ATTENTION"
	EnvKeepAlive   = "NOVA_KEEP_ALIVE"
	EnvContextSize = "NOVA_NUM_CTX"
	DefaultHost    = "127.0.0.1:11434"
	DefaultPort    = 11434
)

// ModelsDir returns the root directory used to store Nova models, blobs,
// manifests and runtime artefacts. It honours NOVA_MODELS and otherwise
// falls back to a per-user directory under the home folder.
func ModelsDir() string {
	if dir := os.Getenv(EnvModelsDir); dir != "" {
		return dir
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}

	var base string
	switch runtime.GOOS {
	case "windows":
		base = filepath.Join(home, ".nova")
	case "darwin":
		base = filepath.Join(home, ".nova")
	default:
		// Linux / *nix: prefer a shared XDG-style location.
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			base = filepath.Join(xdg, "nova")
		} else {
			base = filepath.Join(home, ".nova")
		}
	}
	return filepath.Join(base, "models")
}

// BlobsDir is where raw content-addressed layer data is stored (sha256:...).
func BlobsDir() string { return filepath.Join(ModelsDir(), "blobs") }

// ManifestsDir is the root of the manifest tree:
// <manifests>/<registry>/<namespace>/<model>/<tag>.
func ManifestsDir() string { return filepath.Join(ModelsDir(), "manifests") }

// TmpDir holds runtime sockets, pid files, and per-run scratch space.
func TmpDir() string { return filepath.Join(ModelsDir(), ".tmp") }

// LogsDir holds rotating log files emitted by the server and runners.
func LogsDir() string { return filepath.Join(ModelsDir(), "logs") }

// Host returns the host:port the API server should bind to.
func Host() string {
	if h := os.Getenv(EnvHost); h != "" {
		return h
	}
	return DefaultHost
}

// AllowedOrigins returns the list of CORS origins the server permits.
func AllowedOrigins() []string {
	raw := os.Getenv(EnvOrigin)
	if raw == "" {
		return []string{"localhost", "127.0.0.1", "0.0.0.0"}
	}
	return splitAndTrim(raw)
}

// Debug reports whether verbose debug logging is enabled.
func Debug() bool {
	v := os.Getenv(EnvDebug)
	return v == "1" || v == "true" || v == "TRUE"
}

// EnsureDirs creates every required runtime directory if it does not yet exist.
func EnsureDirs() error {
	for _, dir := range []string{ModelsDir(), BlobsDir(), ManifestsDir(), TmpDir(), LogsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func splitAndTrim(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if t := trim(cur); t != "" {
				out = append(out, t)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if t := trim(cur); t != "" {
		out = append(out, t)
	}
	return out
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
