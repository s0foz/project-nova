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

// chatNoBrowser disables any UI window and just starts the server headless.
var chatNoBrowser bool

// chatBrowser forces the old browser-tab behaviour instead of a desktop window.
var chatBrowser bool

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
not installed or the window fails to open, Nova automatically falls back to
opening the URL in your default browser. Either way the server keeps running
until you press Ctrl+C.

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
		useDesktop := !chatNoBrowser && !(cmd.Flags().Changed("browser") && chatBrowser)

		if useDesktop {
			go func() {
				// Wait for the server to actually be listening before opening
				// the window. Poll /api/health instead of a fixed sleep.
				if !waitReady(url, 10*time.Second) {
					fmt.Fprintf(os.Stderr, "Server did not become ready; open %s manually.\n", url)
					return
				}

				if !desktop.Available() {
					fmt.Fprintf(os.Stderr, "WebView2 runtime not found; opening browser instead.\n")
					openBrowserSafely(url)
					return
				}

				fmt.Fprintf(os.Stdout, "Opening desktop window: %s\n", url)
				err := desktop.Open(url, desktop.DefaultOptions())
				if err != nil {
					// Desktop window failed — fall back to browser WITHOUT
					// shutting down the server. The user can still use the UI
					// in the browser and Ctrl+C when done.
					fmt.Fprintf(os.Stderr, "Desktop window unavailable: %v\n", err)
					fmt.Fprintf(os.Stderr, "Falling back to browser.\n")
					openBrowserSafely(url)
					return
				}
				// The window opened successfully and was closed by the user.
				// Shut everything down cleanly.
				fmt.Fprintln(os.Stdout, "Window closed; shutting down.")
				stop()
			}()
		} else if !chatNoBrowser {
			// --browser mode
			go func() {
				if !waitReady(url, 10*time.Second) {
					fmt.Fprintf(os.Stderr, "Server did not become ready; open %s manually.\n", url)
					return
				}
				fmt.Fprintf(os.Stdout, "Opening browser: %s\n", url)
				openBrowserSafely(url)
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
		"Start the server without opening any window (headless / CI)")
	chatCmd.Flags().BoolVar(&chatBrowser, "browser", false,
		"Open the UI in the system browser instead of a desktop window")
	rootCmd.AddCommand(chatCmd)
}

// waitReady polls the server's /api/health endpoint until it responds or the
// timeout elapses. Returns true if the server became ready.
func waitReady(baseURL string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// openBrowserSafely opens url in the default browser, logging any error but
// never crashing.
func openBrowserSafely(url string) {
	if err := openBrowser(url); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open browser: %v\n", err)
		fmt.Fprintf(os.Stderr, "Open %s manually.\n", url)
	}
}

// openBrowser opens url in the user's default browser. Platform-specific:
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
