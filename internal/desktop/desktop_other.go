//go:build !windows

package desktop

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Open falls back to opening the URL in the system default browser on
// non-Windows platforms. This keeps `nova chat` usable during development on
// macOS/Linux, where there is no WebView2 equivalent in our dependency tree.
func Open(url string, opts Options) error {
	return openBrowser(url)
}

// Available always returns false on non-Windows platforms (no WebView2).
func Available() bool { return false }

// InstalledVersion always returns "" on non-Windows platforms (no WebView2).
func InstalledVersion() string { return "" }

// openBrowser opens url in the default browser. It mirrors the helper in
// internal/cmd/chat.go but is duplicated here to keep package desktop
// self-contained on every platform.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// _ keeps fmt imported even if future edits remove the only use.
var _ = fmt.Sprintf
