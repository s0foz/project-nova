//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
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

// oleUninitialize releases the OLE apartment.
func oleUninitialize() {
	_, _, _ = procOleUninit.Call()
}

// fatalRecorder captures log.Fatalf / log.Panicf calls from the go-webview2
// library so we can recover from them instead of letting them os.Exit(1) the
// whole Nova process. The library uses log.Fatalf for environment/controller
// creation failures, which would kill the server along with the window.
type fatalRecorder struct {
	out      io.Writer
	gotFatal *string
}

// Write implements io.Writer. It scans each log line for the library's fatal
// markers; if found, it records the message and panics with a recoverable
// error (which our deferred recover() catches). Non-fatal lines are forwarded
// to the real log file.
func (f *fatalRecorder) Write(p []byte) (int, error) {
	s := string(p)
	// The library's fatal messages look like:
	//   "Creating environment failed with 80070002"
	//   "Creating controller failed with ..."
	if strings.Contains(s, "Creating environment failed") ||
		strings.Contains(s, "Creating controller failed") ||
		strings.Contains(s, "Error calling Webview2Loader") {
		msg := strings.TrimSpace(s)
		f.gotFatal = &msg
		// Panic so our deferred recover() can catch it and convert to an error.
		panic(panicFatal{msg: msg})
	}
	// Forward to the underlying writer (the log file).
	if f.out != nil {
		_, _ = f.out.Write(p)
	}
	return len(p), nil
}

// panicFatal is a sentinel panic value carrying a library fatal message.
type panicFatal struct{ msg string }

func (p panicFatal) Error() string { return p.msg }

// Open opens a native WebView2 window pointing at url and blocks until the
// user closes the window. It locks the OS thread for the window's lifetime
// because WebView2 (and Win32 message loops in general) require thread
// affinity.
//
// Robustness measures (the go-webview2 library has several bugs that would
// otherwise crash the whole Nova process):
//   - Calls OleInitialize before creating the webview (required by WebView2).
//   - Redirects the global logger through a fatalRecorder so the library's
//     log.Fatalf calls become recoverable panics instead of os.Exit(1).
//   - Recovers from any panic during webview creation (including the
//     nil-pointer dereference in Chromium.Init when the controller failed to
//     initialize) and returns them as errors.
//   - Sets an explicit DataPath (writable) so the WebView2 user-data folder
//     isn't created next to the (possibly read-only) executable.
//   - Detects immediate Run() return (< 2s) as a failure and returns an error.
func Open(url string, opts Options) (retErr error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Initialize OLE/COM on this thread. Without this, WebView2 environment
	// creation can silently fail.
	if err := oleInitialize(); err != nil {
		return fmt.Errorf("desktop: %w", err)
	}
	defer oleUninitialize()

	// Open a diagnostic log file that captures the library's log output.
	logFile, logFileErr := os.OpenFile(
		filepath.Join(env.LogsDir(), "webview2.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if logFileErr == nil {
		defer logFile.Close()
	}

	// Install a fatal recorder that intercepts log.Fatalf-style messages from
	// the library and converts them to recoverable panics. Non-fatal output
	// goes to the log file.
	recorder := &fatalRecorder{out: logFile}
	origLogOut := log.Writer()
	log.SetOutput(recorder)
	defer log.SetOutput(origLogOut)

	// Recover from any panic (library nil-deref, our fatal recorder, etc.)
	// and convert it to an error so callers can fall back to a browser.
	defer func() {
		if r := recover(); r != nil {
			if pf, ok := r.(panicFatal); ok {
				retErr = fmt.Errorf("webview2: %s", pf.msg)
				return
			}
			retErr = fmt.Errorf("webview2 panic: %v\n%s", r, debug.Stack())
		}
	}()

	// Use a writable data directory for WebView2's user data.
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
	// almost certainly failed to initialize.
	start := time.Now()
	w.Run()
	elapsed := time.Since(start)

	if elapsed < 2*time.Second {
		return fmt.Errorf("WebView2 window closed immediately after %s — the WebView2 runtime may not be fully installed; check %s for details", elapsed, filepath.Join(env.LogsDir(), "webview2.log"))
	}
	return nil
}

// Available reports whether the WebView2 runtime is present on this system.
// It performs a real (cheap) probe by attempting to create a webview and
// immediately destroying it. Any panic or nil return means the runtime is not
// usable.
func Available() bool {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := oleInitialize(); err != nil {
		return false
	}
	defer oleUninitialize()

	// Redirect the library's fatal logs to io.Discard during the probe so they
	// don't pollute stderr.
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)

	ok := true
	func() {
		defer func() {
			if r := recover(); r != nil {
				ok = false
			}
		}()
		w := webview.New(false)
		if w == nil {
			ok = false
			return
		}
		w.Destroy()
	}()
	return ok
}

// errWebView2Missing is returned when the runtime is not installed.
var errWebView2Missing = fmt.Errorf("WebView2 runtime not available")

// _ keeps unsafe imported (used by the library internally).
var _ = unsafe.Sizeof(0)
