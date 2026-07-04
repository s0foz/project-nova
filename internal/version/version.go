// Package version exposes build-time version metadata for Project:Nova.
// These values are injected via -ldflags during `make build`.
package version

import "fmt"

var (
	// Version is the semantic version, e.g. "0.1.0".
	Version = "0.0.0-dev"
	// Commit is the short git hash.
	Commit = "unknown"
	// BuildDate is the ISO-8601 UTC build timestamp.
	BuildDate = "unknown"
)

// Info returns a formatted, human-readable version string.
func Info() string {
	return fmt.Sprintf("nova version %s (commit %s, built %s)", Version, Commit, BuildDate)
}

// Short returns just the version number.
func Short() string {
	return Version
}
