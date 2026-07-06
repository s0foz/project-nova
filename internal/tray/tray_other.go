//go:build !windows

// Package tray provides a Windows-only system-tray integration for Project:Nova.
// On non-Windows platforms the package compiles as a no-op stub so the rest of
// the codebase builds on Linux/macOS for development.
package tray

import (
	"errors"

	"github.com/project-nova/nova/internal/server"
)

// Run returns an error on non-Windows platforms — the system tray is a
// Windows-only feature. The arguments are accepted to keep the signature
// identical across build tags.
func Run(srv *server.Server, host string) error {
	_ = srv
	_ = host
	return errors.New("tray: not supported on this platform")
}

// Stop is a no-op on non-Windows platforms.
func Stop() {}
