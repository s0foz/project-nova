// Package main is the entry point for the Nova CLI binary.
//
// All command dispatch, server lifecycle, and tray integration live in
// internal/cmd; main is intentionally one line so the binary stays trivial to
// reason about. The hidden `--tray` flag (handled inside the root command) is
// what switches Nova from CLI mode into the Windows desktop-tray + server mode.
package main

import (
	"os"

	"github.com/project-nova/nova/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
