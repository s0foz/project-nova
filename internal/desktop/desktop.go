// Package desktop opens a native desktop window displaying the Nova chat UI.
//
// On Windows it uses WebView2 (Microsoft's Edge-based runtime, pre-installed on
// Windows 11 and available for Windows 10) to render the embedded chat SPA in a
// real native window — no browser tab, no address bar, just an app window.
//
// On non-Windows platforms (used during development) it falls back to opening
// the URL in the system default browser, so `nova chat` works everywhere.
package desktop

// Options controls the appearance and behaviour of a desktop window.
type Options struct {
	// Title is the window title bar text.
	Title string
	// Width and Height are the initial window size in pixels.
	Width  int
	Height int
	// Debug enables WebView2 developer tools (F12 / right-click → Inspect).
	Debug bool
}

// DefaultOptions returns sensible defaults for the Nova chat window:
// title "Project:Nova", 1200x800, debug off.
func DefaultOptions() Options {
	return Options{
		Title:  "Project:Nova",
		Width:  1200,
		Height: 800,
		Debug:  false,
	}
}
