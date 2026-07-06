//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	webview "github.com/jchv/go-webview2"
	"github.com/project-nova/nova/internal/env"
)

// ole32.dll is needed for OLE/COM initialization, which WebView2 requires on
// the thread that creates the webview. The go-webview2 library does not call
// OleInitialize itself, so we do it here.
var (
	modOle32      = syscall.NewLazyDLL("ole32.dll")
	procOleInit   = modOle32.NewProc("OleInitialize")
	procOleUninit = modOle32.NewProc("OleUninitialize")
)

// oleInitialize puts the current thread into an OLE STA (single-threaded
// apartment), which is required for WebView2 COM calls. It is idempotent per
// thread.
func oleInitialize() error {
	r, _, _ := procOleInit.Call(0)
	// S_OK (0) or S_FALSE (1) are both success. S_FALSE means already initialized.
	if r != 0 && r != 1 {
		return fmt.Errorf("OleInitialize failed: HRESULT 0x%08x", r)
	}
	return nil
}

// oleUninitialize releases the OLE apartment. We call it after the window
// closes to be a good citizen.
func oleUninitialize() {
	_, _, _ = procOleUninit.Call()
}

// Open opens a native WebView2 window pointing at url and blocks until the
// user closes the window. It locks the OS thread for the window's lifetime
// because WebView2 (and Win32 message loops in general) require thread
// affinity.
//
// Robustness measures:
//   - Calls OleInitialize before creating the webview (required by WebView2).
//   - Sets an explicit DataPath (writable) so the WebView2 user-data folder
//     isn't created next to the (possibly read-only) executable.
//   - Detects immediate Run() return (< 2s) as a failure and returns an error
//     so callers can fall back to a browser.
//   - Redirects the library's log output to the Nova logs directory.
func Open(url string, opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Initialize OLE/COM on this thread. Without this, WebView2 environment
	// creation can silently fail and the window dies immediately.
	if err := oleInitialize(); err != nil {
		return fmt.Errorf("desktop: %w", err)
	}
	defer oleUninitialize()

	// Redirect the go-webview2 library's log.Printf output to a file so we can
	// diagnose WebView2 failures. The library logs to the default logger.
	if logFile, err := os.OpenFile(
		filepath.Join(env.LogsDir(), "webview2.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	// Use a writable data directory for WebView2's user data (cache, cookies,
	// etc.). Without this, the library defaults to %AppData%/<exe-name> which
	// may not exist or be writable in all contexts.
	dataPath := filepath.Join(env.ModelsDir(), "webview-data")
	_ = os.MkdirAll(dataPath, 0o755)

	w := webview.NewWithOptions(webview.WebViewOptions{
		Debug:    opts.Debug,
		DataPath: dataPath,
		WindowOptions: webview.WindowOptions{
			Title:  opts.Title,
			Width:  uint(opts.Width),
			Height: uint(opts.Height),
			Center: true,
		},
	})
	if w == nil {
		return errors.New("WebView2 runtime not available; install the Microsoft Edge WebView2 Runtime from https://developer.microsoft.com/microsoft-edge/webview2/")
	}
	defer w.Destroy()

	w.Navigate(url)

	// Track how long Run() takes. If it returns in under 2 seconds, the window
	// almost certainly failed to initialize (WebView2 environment creation
	// failed asynchronously, causing an immediate WM_DESTROY → WM_QUIT).
	start := time.Now()
	w.Run()
	elapsed := time.Since(start)

	if elapsed < 2*time.Second {
		return fmt.Errorf("WebView2 window closed immediately after %s — the WebView2 runtime may not be fully installed; check %s for details", elapsed, filepath.Join(env.LogsDir(), "webview2.log"))
	}
	return nil
}

// Available reports whether the WebView2 runtime is present on this system.
// It is a cheap, non-blocking check used by the CLI to decide whether to open
// a native window or fall back to the browser.
func Available() bool {
	// We attempt a transient webview creation and immediately destroy it. This
	// is cheap (no window is shown) and reliably reports availability.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := oleInitialize(); err != nil {
		return false
	}
	defer oleUninitialize()
	w := webview.New(false)
	if w == nil {
		return false
	}
	w.Destroy()
	return true
}

// errWebView2Missing is returned when the runtime is not installed.
var errWebView2Missing = fmt.Errorf("WebView2 runtime not available")

// _ keeps unsafe imported (used by the library internally).
var _ = unsafe.Sizeof(0)
