// Package cmd implements the Nova command-line interface.
//
// The CLI is a thin wrapper around the API client (for commands like run, pull,
// list, etc.) and a `serve` command that brings up the API server in-process.
// A hidden `--tray` flag on the root command launches the desktop tray + server
// mode (Windows app mode); on other platforms tray.Run returns an error.
package cmd

import (
	"fmt"
	"os"

	"github.com/project-nova/nova/internal/desktop"
	"github.com/project-nova/nova/internal/env"
	"github.com/project-nova/nova/internal/version"
	"github.com/spf13/cobra"
)

// Package-level flag-backed variables for the root command.
var (
	rootHost      string
	rootShowVer   bool
	rootTray      bool
	rootWindowURL string // hidden --window flag: open a desktop window to a URL then exit
)

// rootCmd is the entry point of the Nova CLI.
var rootCmd = &cobra.Command{
	Use:   "nova",
	Short: "Project:Nova — run large language models locally",
	Long: `Project:Nova is a Windows-first desktop replica of Ollama.

Run, chat with, and manage large language models entirely on your own
machine. The Nova CLI talks to a Nova server (started with ` + "`nova serve`" + `)
over HTTP, mirroring the Ollama REST surface so existing tooling works
unchanged.

Common commands:
  nova serve            Start the Nova API server
  nova run MODEL        Run a model (or drop into a chat REPL)
  nova pull NAME        Pull a model from a registry
  nova list             List installed models
  nova ps               List running models
`,
	// SilenceUsage prevents cobra from dumping the full help text whenever a
	// RunE returns a runtime error (e.g. "model not found"). Users can still
	// get help with `nova --help` or by passing an invalid flag.
	SilenceUsage: true,
	// PersistentPreRunE runs for every subcommand. We use it to propagate
	// the --host flag into NOVA_HOST so that any code path (including
	// libraries that read env.Host() directly) honours the override.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// `serve` binds rather than dials; let it manage its own host handling.
		if cmd.Name() == "serve" {
			return nil
		}
		if cmd.Flags().Changed("host") {
			_ = os.Setenv(env.EnvHost, rootHost)
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if rootShowVer {
			fmt.Println(version.Info())
			return nil
		}
		// Hidden --window <url> mode: open a desktop window to an already-
		// running server and exit when the window closes. Used by the tray
		// app's "Open Chat" menu item to spawn a window without conflicting
		// with the server the tray is already running.
		if rootWindowURL != "" {
			return desktop.Open(rootWindowURL, desktop.DefaultOptions())
		}
		if rootTray {
			return runTrayMode()
		}
		return cmd.Help()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&rootHost, "host", "H", env.Host(),
		"Nova server host:port (env NOVA_HOST)")
	rootCmd.Flags().BoolVarP(&rootShowVer, "version", "v", false,
		"Print version information and exit")
	rootCmd.Flags().BoolVar(&rootTray, "tray", false,
		"Launch the desktop tray + server (Windows app mode)")
	_ = rootCmd.Flags().MarkHidden("tray")
	rootCmd.Flags().StringVar(&rootWindowURL, "window", "",
		"Open a desktop window to the given URL and exit when closed (internal)")
	_ = rootCmd.Flags().MarkHidden("window")
}

// Execute runs the root command and returns the process exit code. Errors are
// printed by cobra itself (with a leading "Error:" prefix); this function
// only translates "no error" / "error" into a 0 / 1 exit code.
func Execute() int {
	if err := rootCmd.Execute(); err != nil {
		return 1
	}
	return 0
}

// Root returns the root cobra command (useful for tests and embedding).
func Root() *cobra.Command { return rootCmd }
