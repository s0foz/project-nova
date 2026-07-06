package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/project-nova/nova/internal/api"
	"github.com/project-nova/nova/internal/env"
	"github.com/project-nova/nova/internal/server"
	"github.com/project-nova/nova/internal/version"
	"github.com/spf13/cobra"
)

var serveVersion bool

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Nova API server",
	Long: `Start the Nova API server, listening for client requests on the
configured host. The server holds loaded models in memory and serves
both the Nova and OpenAI-compatible HTTP APIs.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if serveVersion {
			fmt.Println(version.Info())
			return nil
		}

		if err := env.EnsureDirs(); err != nil {
			return fmt.Errorf("ensure dirs: %w", err)
		}

		srv := server.New(nil)
		apiSrv := api.New(srv)
		apiSrv.Addr = rootHost

		// Startup banner.
		fmt.Fprintf(os.Stdout, "Nova %s\n", version.Short())
		fmt.Fprintf(os.Stdout, "Listening on http://%s (version %s)\n", rootHost, version.Short())
		fmt.Fprintf(os.Stdout, "Models directory: %s\n", env.ModelsDir())

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		go func() {
			<-ctx.Done()
			fmt.Fprintln(os.Stderr, "\nshutting down...")
			shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = apiSrv.Shutdown(shutCtx)
			srv.Stop()
		}()

		log.Printf("Nova server starting on %s", rootHost)
		if err := apiSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	},
}

func init() {
	serveCmd.Flags().BoolVar(&serveVersion, "version", false,
		"Print version and exit")
	rootCmd.AddCommand(serveCmd)
}
