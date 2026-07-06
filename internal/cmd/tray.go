package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/project-nova/nova/internal/api"
	"github.com/project-nova/nova/internal/env"
	"github.com/project-nova/nova/internal/server"
	"github.com/project-nova/nova/internal/tray"
	"github.com/project-nova/nova/internal/version"
)

// runTrayMode starts the Nova API server alongside the Windows system-tray
// icon. On non-Windows platforms tray.Run returns an error immediately, so
// `nova --tray` is effectively Windows-only.
//
// The HTTP server runs in a background goroutine. The tray message loop runs
// on the calling (main) goroutine, which is locked to its OS thread inside
// tray.Run. When the user selects "Quit" from the tray menu, the loop exits
// and this function shuts down the API server before returning.
func runTrayMode() error {
	if err := env.EnsureDirs(); err != nil {
		return fmt.Errorf("ensure dirs: %w", err)
	}

	srv := server.New(nil)
	apiSrv := api.New(srv)
	apiSrv.Addr = rootHost

	fmt.Fprintf(os.Stderr, "Nova %s (tray mode)\n", version.Short())
	fmt.Fprintf(os.Stderr, "Listening on http://%s\n", rootHost)
	fmt.Fprintf(os.Stderr, "Models directory: %s\n", env.ModelsDir())

	// Start the HTTP server in the background.
	go func() {
		if err := apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "api server error: %v\n", err)
			tray.Stop() // request the tray message loop to exit
		}
	}()

	// Treat Ctrl+C as a fallback shutdown trigger (e.g. when running from a
	// console window rather than the Windows GUI).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		tray.Stop()
	}()

	// Run the tray — blocks until Quit is selected.
	err := tray.Run(srv, rootHost)

	// Tray exited — shut everything down gracefully.
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = apiSrv.Shutdown(shutCtx)
	srv.Stop()
	return err
}
