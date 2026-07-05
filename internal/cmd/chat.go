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
	"github.com/project-nova/nova/internal/desktop"
	"github.com/project-nova/nova/internal/env"
	"github.com/project-nova/nova/internal/server"
	"github.com/project-nova/nova/internal/version"
	"github.com/spf13/cobra"
)

// chatNoBrowser disables the desktop window and forces the old browser-tab
// behaviour (useful for headless / CI / remote-dev scenarios).
var chatNoBrowser bool

// chatCmd is `nova chat` — it starts the API server in-process and then opens
// the embedded chat UI in a native desktop window (WebView2 on Windows). Use
// --browser to force the old behaviour of opening it in the system browser
// tab instead.
var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start the server and open the chat UI in a desktop window",
	Long: `Start the Nova API server (identical to ` + "`nova serve`" + `) and then
open the embedded chat UI in a native desktop window.

On Windows the UI renders inside a WebView2 window — a real application window
with no browser chrome (no address bar, no tabs). If the WebView2 runtime is
not installed, Nova falls back to opening http://<host>/ in the default
browser.

Pass --browser to skip the desktop window and always use the browser.
Pass --no-browser to start the server without opening any window (headless).`,
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

		url := "http://" + rootHost + "/"

		// Decide how to surface the UI.
		openDesktop := !chatNoBrowser
		if cmd.Flags().Changed("browser") && chatBrowser {
			openDesktop = false
		}

		// Open the window shortly after the server starts listening, so the
		// page actually loads instead of hitting a connection-refused.
		if openDesktop {
			go func() {
				time.Sleep(700 * time.Millisecond)
				if desktop.Available() {
					fmt.Fprintf(os.Stdout, "Opening desktop window: %s\n", url)
					if err := desktop.Open(url, desktop.DefaultOptions()); err != nil {
						fmt.Fprintf(os.Stderr, "Desktop window unavailable: %v\n", err)
						fmt.Fprintf(os.Stderr, "Falling back to browser. Open %s manually.\n", url)
						_ = openBrowser(url)
					}
					// When the window closes, shut everything down.
					stop()
				} else {
					fmt.Fprintf(os.Stderr, "WebView2 runtime not found; opening browser instead.\n")
					if err := openBrowser(url); err != nil {
						fmt.Fprintf(os.Stderr, "Could not open browser: %v\n", err)
						fmt.Fprintf(os.Stderr, "Open %s manually.\n", url)
					}
				}
			}()
		} else if !chatNoBrowser {
			// --browser mode
			go func() {
				time.Sleep(700 * time.Millisecond)
				fmt.Fprintf(os.Stdout, "Opening browser: %s\n", url)
				if err := openBrowser(url); err != nil {
					fmt.Fprintf(os.Stderr, "Could not open browser: %v\n", err)
					fmt.Fprintf(os.Stderr, "Open %s manually.\n", url)
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

// chatBrowser is bound to the --browser flag.
var chatBrowser bool

func init() {
	chatCmd.Flags().BoolVar(&chatNoBrowser, "no-browser", false,
		"Start the server without opening any window (headless / CI)")
	chatCmd.Flags().BoolVar(&chatBrowser, "browser", false,
		"Open the UI in the system browser instead of a desktop window")
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
