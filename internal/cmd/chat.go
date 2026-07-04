package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"github.com/project-nova/nova/internal/api"
	"github.com/project-nova/nova/internal/env"
	"github.com/project-nova/nova/internal/server"
	"github.com/project-nova/nova/internal/version"
	"github.com/spf13/cobra"
)

// chatNoBrowser disables opening a browser (useful for headless / CI).
var chatNoBrowser bool

// chatCmd is `nova chat` — it starts the API server in-process (exactly like
// `nova serve`) and then opens the embedded chat web UI in the user's default
// browser. It is the single-command way to get from "nova installed" to "I am
// chatting with a model in a window".
var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start the server and open the chat UI in your browser",
	Long: `Start the Nova API server (identical to ` + "`nova serve`" + `) and then
open the embedded chat web UI in your default browser.

The chat window talks to the local Nova server over HTTP and supports model
selection, streaming responses, system prompts, and markdown rendering. It is
served at http://<host>/ — you can also open it manually at any time while the
server is running.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := env.EnsureDirs(); err != nil {
			return fmt.Errorf("ensure dirs: %w", err)
		}

		srv := server.New(nil)
		apiSrv := api.New(srv)
		apiSrv.Addr = rootHost

		fmt.Fprintf(os.Stdout, "Nova %s\n", version.Short())
		fmt.Fprintf(os.Stdout, "Listening on http://%s (version %s)\n", rootHost, version.Short())
		fmt.Fprintf(os.Stdout, "Models directory: %s\n", env.ModelsDir())

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		// Open the browser shortly after the server starts listening, so the
		// page actually loads instead of hitting a connection-refused.
		if !chatNoBrowser {
			go func() {
				time.Sleep(700 * time.Millisecond)
				url := "http://" + rootHost + "/"
				fmt.Fprintf(os.Stdout, "Opening chat UI: %s\n", url)
				if err := openBrowser(url); err != nil {
					fmt.Fprintf(os.Stderr, "Could not open browser automatically: %v\n", err)
					fmt.Fprintf(os.Stderr, "Open %s manually in your browser.\n", url)
				}
			}()
		}

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
	chatCmd.Flags().BoolVar(&chatNoBrowser, "no-browser", false,
		"Start the server without opening a browser")
	rootCmd.AddCommand(chatCmd)
}

// openBrowser opens url in the user's default browser. It is best-effort: any
// error is reported by the caller. Platform-specific commands:
//
//	windows: cmd /c start "" <url>
//	darwin:  open <url>
//	linux:   xdg-open <url>
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
