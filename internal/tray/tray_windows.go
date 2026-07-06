//go:build windows

// Package tray implements the Project:Nova Windows system-tray icon.
//
// On a real Windows machine this file builds a real notify-icon with a popup
// menu containing:
//
//   - "Project:Nova"          (header, inert)
//   - ---
//   - "Listening on <host>"   (disabled, informational)
//   - ---
//   - "Open Logs Folder"      (opens env.LogsDir() in Explorer)
//   - "Quit"                  (stops the server and exits the message loop)
//
// Right-clicking the tray icon shows the popup menu. Selecting "Quit" calls
// srv.Stop() and posts WM_QUIT, which causes Run() to return.
//
// The implementation uses raw syscalls into user32.dll / shell32.dll /
// kernel32.dll (rather than golang.org/x/sys/windows) so it is insensitive to
// signature drift between x/sys releases. The OS thread is locked for the
// lifetime of Run() so that window/message-loop affinity is preserved.
package tray

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/project-nova/nova/internal/env"
	"github.com/project-nova/nova/internal/server"
	"golang.org/x/sys/windows"
)

// Win32 window-message constants.
const (
	WM_DESTROY   = 0x0002
	WM_COMMAND   = 0x0111
	WM_LBUTTONUP = 0x0202
	WM_RBUTTONUP = 0x0205
	WM_APP       = 0x8000
	WM_TRAYICON  = WM_APP + 1
)

// Shell_NotifyIcon message constants.
const (
	nimAdd    = 0x00000000
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
)

// Popup-menu item flags.
const (
	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfGrayed    = 0x00000001
	mfDisabled  = 0x00000002

	tpmLeftAlign = 0x0000
	tpmReturnCmd = 0x0100
	tpmNoNotify  = 0x0080
)

// Popup-menu command IDs.
const (
	idMenuHeader   = 1000
	idMenuHost     = 1001
	idMenuOpenChat = 1004
	idMenuOpenLogs = 1002
	idMenuQuit     = 1003
)

// LoadIcon / ShellExecute constants.
const (
	idiApplication = 32512
	swShowNormal   = 1
)

// WS_EX_TOOLWINDOW style for the hidden window (keeps it out of the taskbar).
const wsExToolWindow = 0x00000080

// HWND_MESSAGE: a sentinel parent that creates a message-only window.
const hwndMessage = ^uintptr(2) // (HWND)(-3)

var (
	modUser32   = syscall.NewLazyDLL("user32.dll")
	modShell32  = syscall.NewLazyDLL("shell32.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW    = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW     = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW      = modUser32.NewProc("DefWindowProcW")
	procGetMessageW         = modUser32.NewProc("GetMessageW")
	procTranslateMessage    = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW    = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage     = modUser32.NewProc("PostQuitMessage")
	procPostMessageW        = modUser32.NewProc("PostMessageW")
	procCreatePopupMenu     = modUser32.NewProc("CreatePopupMenu")
	procAppendMenuW         = modUser32.NewProc("AppendMenuW")
	procTrackPopupMenu      = modUser32.NewProc("TrackPopupMenu")
	procDestroyMenu         = modUser32.NewProc("DestroyMenu")
	procDestroyWindow       = modUser32.NewProc("DestroyWindow")
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
	procGetCursorPos        = modUser32.NewProc("GetCursorPos")
	procLoadIconW           = modUser32.NewProc("LoadIconW")

	procShellNotifyIconW = modShell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW    = modShell32.NewProc("ShellExecuteW")

	procGetModuleHandleW = modKernel32.NewProc("GetModuleHandleW")
)

// WNDCLASSEX mirrors the Win32 WNDCLASSEXW structure.
type WNDCLASSEX struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     syscall.Handle
	HIcon         syscall.Handle
	HCursor       syscall.Handle
	HbrBackground syscall.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       syscall.Handle
}

// MSG mirrors the Win32 MSG structure.
type MSG struct {
	HWnd    syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// NOTIFYICONDATA mirrors NOTIFYICONDATAW with all fields up to and including
// hBalloonIcon. The byte layout matches the Win32 struct on 32- and 64-bit.
type NOTIFYICONDATA struct {
	CbSize           uint32
	HWnd             syscall.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            syscall.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     syscall.Handle
}

// POINT mirrors the Win32 POINT structure.
type POINT struct{ X, Y int32 }

// trayState holds per-process tray state used by the window procedure.
var trayState struct {
	srv  *server.Server
	host string
	hwnd syscall.Handle
	nid  NOTIFYICONDATA
}

// Run starts the tray icon and runs the Win32 message loop. It blocks until
// the user selects "Quit" from the tray menu (or the message loop otherwise
// terminates). The OS thread is locked for the duration so that window/message
// affinity is preserved.
func Run(srv *server.Server, host string) error {
	if srv == nil {
		return errors.New("tray: server is nil")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	trayState.srv = srv
	trayState.host = host

	instance, _, _ := procGetModuleHandleW.Call(0)

	className, err := syscall.UTF16PtrFromString("NovaTrayClass")
	if err != nil {
		return fmt.Errorf("tray: utf16 class: %w", err)
	}
	windowName, err := syscall.UTF16PtrFromString("Nova Tray")
	if err != nil {
		return fmt.Errorf("tray: utf16 window: %w", err)
	}

	// Register the window class with our window procedure.
	wc := WNDCLASSEX{
		CbSize:        uint32(unsafe.Sizeof(WNDCLASSEX{})),
		LpfnWndProc:   windows.NewCallback(windowProc),
		HInstance:     syscall.Handle(instance),
		LpszClassName: className,
	}
	atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return fmt.Errorf("tray: RegisterClassExW failed: %v", callErr)
	}

	// Create a hidden, message-only window.
	hwnd, _, callErr := procCreateWindowExW.Call(
		wsExToolWindow,                      // dwExStyle
		uintptr(unsafe.Pointer(className)),  // lpClassName
		uintptr(unsafe.Pointer(windowName)), // lpWindowName
		0,                                   // dwStyle
		0, 0, 0, 0,                          // x, y, w, h
		hwndMessage,    // hWndParent (message-only)
		0, instance, 0, // hMenu, hInstance, lpParam
	)
	if hwnd == 0 {
		return fmt.Errorf("tray: CreateWindowExW failed: %v", callErr)
	}
	trayState.hwnd = syscall.Handle(hwnd)

	// Load a default application icon and add the notification icon.
	icon, _, _ := procLoadIconW.Call(0, uintptr(idiApplication))
	tipUTF16 := syscall.StringToUTF16("Project:Nova")

	trayState.nid = NOTIFYICONDATA{
		CbSize:           uint32(unsafe.Sizeof(NOTIFYICONDATA{})),
		HWnd:             trayState.hwnd,
		UID:              1,
		UFlags:           nifMessage | nifIcon | nifTip,
		UCallbackMessage: WM_TRAYICON,
		HIcon:            syscall.Handle(icon),
	}
	copy(trayState.nid.SzTip[:], tipUTF16)

	if ret, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&trayState.nid))); ret == 0 {
		return errors.New("tray: Shell_NotifyIconW failed")
	}

	// Message loop.
	var msg MSG
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if ret == 0 { // WM_QUIT
			break
		}
		if ret == ^uintptr(0) { // -1: error
			return errors.New("tray: GetMessage failed")
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}

	// Cleanup: remove the notification icon (best-effort).
	_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&trayState.nid)))
	trayState.hwnd = 0
	return nil
}

// Stop requests the tray message loop to exit by posting WM_QUIT to the hidden
// window's thread. It is safe to call from any goroutine.
func Stop() {
	if trayState.hwnd != 0 {
		_, _, _ = procPostMessageW.Call(uintptr(trayState.hwnd), 0x0012 /* WM_QUIT */, 0, 0)
	}
}

// windowProc is the Win32 window procedure for the hidden tray window. It
// handles tray-icon mouse events and popup-menu commands. The signature uses
// raw uintptrs to match syscall.NewCallback's requirements.
func windowProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch uint32(msg) {
	case WM_TRAYICON:
		switch uint32(lparam) & 0xFFFF {
		case WM_RBUTTONUP:
			showContextMenu(syscall.Handle(hwnd))
		}
		return 0
	case WM_COMMAND:
		switch uint32(wparam) & 0xFFFF {
		case idMenuQuit:
			doQuit(syscall.Handle(hwnd))
		case idMenuOpenChat:
			openChatURL()
		case idMenuOpenLogs:
			openLogsFolder()
		}
		return 0
	case WM_DESTROY:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

// showContextMenu builds and displays the tray popup menu at the cursor.
func showContextMenu(hwnd syscall.Handle) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendMenu(menu, mfString, idMenuHeader, "Project:Nova")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString|mfGrayed|mfDisabled, idMenuHost, "Listening on "+trayState.host)
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, idMenuOpenChat, "Open Chat")
	appendMenu(menu, mfString, idMenuOpenLogs, "Open Logs Folder")
	appendMenu(menu, mfString, idMenuQuit, "Quit")

	// TrackPopupMenu needs the owner to be the foreground window, otherwise
	// the menu won't dismiss when the user clicks outside.
	_, _, _ = procSetForegroundWindow.Call(uintptr(hwnd))

	var pt POINT
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	cmd, _, _ := procTrackPopupMenu.Call(
		menu,
		tpmLeftAlign|tpmReturnCmd|tpmNoNotify,
		uintptr(pt.X), uintptr(pt.Y),
		0, uintptr(hwnd), 0,
	)
	if cmd != 0 {
		// Dispatch the selected command through the window proc.
		_, _, _ = procPostMessageW.Call(uintptr(hwnd), WM_COMMAND, cmd, 0)
	}
}

// appendMenu wraps AppendMenuW; an empty text means a separator / null item.
func appendMenu(menu uintptr, flags uint32, id uintptr, text string) {
	var textPtr *uint16
	if text != "" {
		textPtr, _ = syscall.UTF16PtrFromString(text)
	}
	_, _, _ = procAppendMenuW.Call(menu, uintptr(flags), id, uintptr(unsafe.Pointer(textPtr)))
}

// doQuit unloads the server and posts WM_QUIT to terminate the message loop.
// Per the spec, Quit calls srv.Stop() then PostQuitMessage(0).
func doQuit(hwnd syscall.Handle) {
	if trayState.srv != nil {
		trayState.srv.Stop()
	}
	_, _, _ = procDestroyWindow.Call(uintptr(hwnd))
	_, _, _ = procPostQuitMessage.Call(0)
}

// openLogsFolder opens env.LogsDir() in Windows Explorer.
func openLogsFolder() {
	dir := env.LogsDir()
	_ = os.MkdirAll(dir, 0o755)
	verb, _ := syscall.UTF16PtrFromString("open")
	path, _ := syscall.UTF16PtrFromString(dir)
	_, _, _ = procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(path)),
		0, 0,
		uintptr(swShowNormal),
	)
}

// openChatURL opens the Nova chat UI in a native desktop window. It spawns a
// fresh nova.exe process with the hidden --window flag so the tray's own
// message loop is never blocked by the WebView2 window. The child process
// exits when the user closes the window.
func openChatURL() {
	url := "http://" + trayState.host + "/"
	exe, err := os.Executable()
	if err != nil {
		// Fall back to the shell "open" verb on the URL (opens default browser).
		verb, _ := syscall.UTF16PtrFromString("open")
		file, _ := syscall.UTF16PtrFromString(url)
		_, _, _ = procShellExecuteW.Call(0,
			uintptr(unsafe.Pointer(verb)),
			uintptr(unsafe.Pointer(file)),
			0, 0, uintptr(swShowNormal))
		return
	}
	// Detach: the child runs independently; we don't wait for it.
	cmd := exec.Command(exe, "--window", url)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Hide any console window for the child (it's a GUI-only spawn).
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		// Last-resort fallback: open in the default browser via ShellExecuteW.
		verb, _ := syscall.UTF16PtrFromString("open")
		file, _ := syscall.UTF16PtrFromString(url)
		_, _, _ = procShellExecuteW.Call(0,
			uintptr(unsafe.Pointer(verb)),
			uintptr(unsafe.Pointer(file)),
			0, 0, uintptr(swShowNormal))
	}
}

// hideWindow configures an exec.Cmd so that, if Windows would normally attach
// a console window to the child process (e.g. when nova.exe is the console
// subsystem build), that window is hidden. The GUI-subsystem build (nova-tray)
// never shows a console anyway, so this is a no-op for it.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
