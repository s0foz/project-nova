package cmd

import (
	"os"
	"os/signal"
	"syscall"
)

// installSignalHandler installs a SIGINT/SIGTERM handler that restores
// terminal state (a no-op in Nova since the REPL uses plain line reading
// rather than raw mode) and exits with the conventional 130 status code.
//
// It is invoked by the `run` REPL so that Ctrl+C at the prompt exits cleanly
// without leaving the terminal in an inconsistent state.
func installSignalHandler() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		restoreTerminal()
		os.Exit(130)
	}()
}

// restoreTerminal is a hook for future raw-mode support. Nova's REPL currently
// uses bufio line reading, so there is nothing to restore; the function is
// kept as an extension point.
func restoreTerminal() {
	// No-op: plain line reading does not alter terminal state.
}
