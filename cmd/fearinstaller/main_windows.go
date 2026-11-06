//go:build windows

// Command fearinstaller is the F.E.A.R. Exclusive Console Content installer.
//
// It finds the game, checks the player's PlayStation 3 and Xbox 360 copies, builds the mod from them into the
// game folder (internal/app, internal/install: every change is undone on failure, and Uninstall puts the folder
// back), and removes it again on request. Besides the game folder it writes two things: the embedded browser's
// profile folder in the Windows temp folder (removed again when the browser has shut down, and any leftovers of
// an earlier crashed run at the next start), and its log next to the program (see openLog).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	webview "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"fearinstaller/internal/app"
	"fearinstaller/internal/pcgame"
	"fearinstaller/ui"
)

const (
	title         = "F.E.A.R. Exclusive Console Content — Installer"
	profilePrefix = "FEAR-Exclusive-Console-Content-WebView2"
	mutexName     = `Local\FEAR-Exclusive-Console-Content-Installer`
	// The page background (#080a0b), used for the window and the browser surface
	// before the page paints.
	bgR, bgG, bgB = 8, 10, 11
)

func main() {
	runtime.LockOSThread() // the UI and its native dialogs must stay on one thread
	os.Exit(run())
}

// run is main without os.Exit, so that every deferred cleanup (the profile
// folder, the instance mutex) really runs.
func run() (code int) {
	defer func() {
		if p := recover(); p != nil {
			messageBox(title, fmt.Sprintf("The installer hit an unexpected problem and has to close.\n\n(%v)\n\nNothing was changed on your computer.", p))
			code = 1
		}
	}()

	// Load libraries from the system folder only, and never from the current folder.
	_ = windows.SetDefaultDllDirectories(windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	_ = windows.SetDllDirectory("")
	setDPIAware()
	_ = windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)

	// One window at a time: a second start brings the first to the front.
	release, first := acquireSingleInstance()
	if !first {
		focusExistingWindow()
		return 0
	}
	defer release()

	if !webView2Installed() {
		messageBox(title, "The Microsoft Edge WebView2 Runtime is required but was not found.\n\n"+
			"It is included with Windows 11 and current Windows 10. Install it from Microsoft (search for \"WebView2 Runtime\") and start this program again.")
		return 1
	}

	// Errors the browser component would otherwise treat as fatal reach the user as a message box.
	webview.SetErrorHandler(func(msg string) { messageBox(title, msg) })
	webview.InternalErrorHandler = debugLog

	// The embedded browser needs a profile folder; keep it out of the game folder.
	// The name carries the process id so that two runs never share one.
	sweepStaleProfiles()
	profile := filepath.Join(os.TempDir(), profilePrefix+"-"+strconv.Itoa(os.Getpid()))
	defer removeProfile(profile)

	scale := dpiScale()
	w := webview.NewWithOptions(webview.WebViewOptions{
		DataPath:  profile,
		AutoFocus: true,
		WindowOptions: webview.WindowOptions{
			Title:         title,
			Width:         uint(1280 * scale), // clamped to the work area by the window code
			Height:        uint(900 * scale),
			IconId:        1, // the icon resource in app.rc
			Center:        true,
			HasBackground: true,
			BackgroundRGB: [3]uint8{bgR, bgG, bgB},
		},
	})
	if w == nil {
		if !webview.StartupCancelled { // closing the window while it opened is not an error
			messageBox(title, "The browser component (WebView2) could not be started.")
			return 1
		}
		return 0
	}
	defer w.Destroy()
	w.SetSize(int(960*scale), int(600*scale), webview.HintMin)

	logLine, closeLog := openLog()
	defer closeLog()
	internalError := func(msg string) {
		debugLog(msg)
		logLine("Internal error: " + msg)
	}

	// Back end: state changes are pushed to the page from any goroutine.
	backend := app.New(func(s app.State) {
		data, err := json.Marshal(s)
		if err != nil {
			return
		}
		w.Dispatch(func() { w.Eval("window.__fearState && window.__fearState(" + string(data) + ")") })
	})
	backend.SetLog(logLine)
	br := &app.Bridge{
		B:               backend,
		D:               nativeDialogs{owner: func() uintptr { return uintptr(w.Window()) }},
		Post:            w.Dispatch, // dialogs run on the UI thread, outside the browser callback
		Licenses:        string(ui.LicensesText),
		OnInternalError: internalError,
		OnQuit:          w.Destroy, // the Close button on the last step closes the window like the X does
		OpenURL:         func(url string) { openURL(uintptr(w.Window()), url) },
	}
	// Closing the window while the install job (or a check) runs cancels it and waits for it to stop, so no
	// goroutine is still reading a disc image when the process exits. Runs before w.Destroy (LIFO).
	defer backend.Shutdown()
	for name, fn := range map[string]any{
		"be_init":      br.Init,
		"be_setPC":     br.SetPC,
		"be_recheck":   br.Recheck,
		"be_browse":    br.Browse,
		"be_clear":     br.Clear,
		"be_plan":      br.Plan,
		"be_snapshot":  br.Snapshot,
		"be_install":   br.Install,
		"be_cancel":    br.Cancel,
		"be_uninstall": br.Uninstall,
		"be_quit":      br.Quit,
		"be_echopage":  br.OpenEchoPatchPage,
	} {
		if err := w.Bind(name, fn); err != nil {
			messageBox(title, "Internal error: "+err.Error())
			return 1
		}
	}
	w.SetHtml(string(ui.IndexHTML))
	w.Run()
	return 0
}

// openLog creates (or replaces: it always describes the latest run) the installer's log next to the program,
// named after it (<program>.log), for support: what was found, what was checked, what was done. Every line has
// the account name in paths hidden. Where the folder is not writable there is simply no log.
func openLog() (logLine func(string), closeLog func()) {
	none := func(string) {}
	exe, err := os.Executable()
	if err != nil {
		return none, func() {}
	}
	f, err := os.Create(strings.TrimSuffix(exe, filepath.Ext(exe)) + ".log")
	if err != nil {
		return none, func() {}
	}
	var mu sync.Mutex
	closed := false
	logLine = func(s string) {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			line := time.Now().Format("15:04:05") + "  " + pcgame.MaskUser(s)
			_, _ = f.WriteString(strings.ReplaceAll(line, "\n", "\r\n") + "\r\n")
		}
	}
	logLine(title + ", started " + time.Now().Format("2006-01-02"))
	return logLine, func() {
		mu.Lock()
		defer mu.Unlock()
		closed = true
		_ = f.Close()
	}
}

var procOutputDebugString = windows.NewLazySystemDLL("kernel32.dll").NewProc("OutputDebugStringW")

// debugLog writes to the debugger output (DebugView). It deliberately creates no file.
func debugLog(msg string) {
	if p, err := windows.UTF16PtrFromString("fearinstaller: " + msg + "\n"); err == nil {
		procOutputDebugString.Call(uintptr(unsafe.Pointer(p)))
	}
}

// ------------------------------------------------- single instance ---

// acquireSingleInstance takes a per-session named mutex. first is false when
// another copy already holds it.
func acquireSingleInstance() (release func(), first bool) {
	name, _ := windows.UTF16PtrFromString(mutexName)
	h, err := windows.CreateMutex(nil, false, name)
	if h == 0 {
		return func() {}, true // cannot tell; do not block the user
	}
	if err == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(h)
		return func() {}, false
	}
	return func() { windows.CloseHandle(h) }, true
}

// focusExistingWindow brings the running copy's window to the front.
func focusExistingWindow() {
	class, _ := windows.UTF16PtrFromString("webview")
	t, _ := windows.UTF16PtrFromString(title)
	hwnd, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(t)))
	if hwnd == 0 {
		return
	}
	user32.NewProc("ShowWindow").Call(hwnd, 9) // SW_RESTORE
	user32.NewProc("SetForegroundWindow").Call(hwnd)
}

// --------------------------------------------------- profile folder ---

// removeProfile deletes the browser profile once the browser's own processes
// have let go of it. They keep running for a moment after the window closes, so
// deleting is retried for a few seconds; anything left is removed by the next
// start (sweepStaleProfiles).
func removeProfile(dir string) {
	if !strings.HasPrefix(filepath.Base(dir), profilePrefix) {
		return // never delete anything that is not ours
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		err := os.RemoveAll(dir)
		if err == nil || time.Now().After(deadline) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// sweepStaleProfiles removes profile folders that earlier runs left behind
// (a crash, or a browser that was slow to exit). A folder whose process id is
// still running is left alone.
func sweepStaleProfiles() {
	ents, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, profilePrefix) {
			continue
		}
		if pid, err := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(name, profilePrefix), "-")); err == nil && pid != os.Getpid() && pidRunning(uint32(pid)) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(os.TempDir(), name)) // best effort
	}
}

func pidRunning(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}

// ----------------------------------------------------------- misc ---

// webView2Installed checks the registry for the Evergreen runtime.
func webView2Installed() bool {
	const guid = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	for _, c := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + guid},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + guid},
		{registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\` + guid},
	} {
		k, err := registry.OpenKey(c.root, c.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("pv")
		k.Close()
		if err == nil && v != "" && v != "0.0.0.0" {
			return true
		}
	}
	return false
}

// setDPIAware opts in to per-monitor DPI so text is sharp on scaled displays.
// (The manifest already asks for it; this covers a manifest that was stripped.)
func setDPIAware() {
	if p := user32.NewProc("SetProcessDpiAwarenessContext"); p.Find() == nil {
		const perMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		if r, _, _ := p.Call(perMonitorV2); r != 0 {
			return
		}
	}
	user32.NewProc("SetProcessDPIAware").Call()
}

// dpiScale returns the system DPI scale (1.0 = 96 dpi, never below). It does not
// shrink for small screens: the window code clamps the window to the work area
// at every scale.
func dpiScale() float64 {
	p := user32.NewProc("GetDpiForSystem")
	if p.Find() != nil {
		return 1
	}
	dpi, _, _ := p.Call()
	if dpi < 96 {
		return 1
	}
	return float64(dpi) / 96
}
