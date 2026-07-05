//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"runtime"

	webview "github.com/jchv/go-webview2"
)

// Open opens a native WebView2 window pointing at url and blocks until the
// user closes the window. It locks the OS thread for the window's lifetime
// because WebView2 (and Win32 message loops in general) require thread
// affinity.
//
// If the WebView2 runtime is not installed (rare on Windows 11, possible on
// older Windows 10 builds), webview.New returns nil and Open returns an error
// explaining how to install the runtime.
func Open(url string, opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := webview.New(opts.Debug)
	if w == nil {
		return errors.New("WebView2 runtime not available; install the Microsoft Edge WebView2 Runtime from https://developer.microsoft.com/microsoft-edge/webview2/")
	}
	defer w.Destroy()

	w.SetTitle(opts.Title)
	w.SetSize(opts.Width, opts.Height, webview.HintNone)
	w.Navigate(url)
	w.Run()
	return nil
}

// Available reports whether the WebView2 runtime is present on this system.
// It is a cheap, non-blocking check used by the CLI to decide whether to open
// a native window or fall back to the browser.
func Available() bool {
	// We attempt a transient webview creation and immediately destroy it. This
	// is cheap (no window is shown) and reliably reports availability.
	w := webview.New(false)
	if w == nil {
		return false
	}
	w.Destroy()
	return true
}

// errWebView2Missing is returned when the runtime is not installed.
var errWebView2Missing = fmt.Errorf("WebView2 runtime not available")
