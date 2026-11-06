//go:build windows
// +build windows

package webview2

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"sync"
	"unsafe"

	"github.com/jchv/go-webview2/internal/w32"
	"github.com/jchv/go-webview2/pkg/edge"

	"golang.org/x/sys/windows"
)

var (
	windowContext     = map[uintptr]interface{}{}
	windowContextSync sync.RWMutex
)

func getWindowContext(wnd uintptr) interface{} {
	windowContextSync.RLock()
	defer windowContextSync.RUnlock()
	return windowContext[wnd]
}

func setWindowContext(wnd uintptr, data interface{}) {
	windowContextSync.Lock()
	defer windowContextSync.Unlock()
	windowContext[wnd] = data
}

type browser interface {
	Embed(hwnd uintptr) bool
	Resize()
	Navigate(url string)
	NavigateToString(htmlContent string)
	Init(script string)
	Eval(script string)
	NotifyParentWindowPositionChanged() error
	Focus()
}

type webview struct {
	hwnd       uintptr
	mainthread uintptr
	browser    browser
	autofocus  bool
	maxsz      w32.Point
	minsz      w32.Point
	m          sync.Mutex
	bindings   map[string]interface{}
	dispatchq  []func()
}

type WindowOptions struct {
	Title  string
	Width  uint // outer window size in pixels; clamped to the work area (MODIFIED)
	Height uint
	IconId uint
	Center bool
	// BackgroundRGB, if HasBackground, paints the window class and the browser
	// surface before the page loads, so there is no white flash (MODIFIED).
	HasBackground bool
	BackgroundRGB [3]uint8
}

// StartupCancelled is true when NewWithOptions returned nil because the window
// was closed while the browser was still starting; there is nothing to report
// to the user then (MODIFIED).
var StartupCancelled bool

// ErrorHandler is told about errors that upstream treated as fatal. It should
// show a message; the process exits afterwards. MODIFIED: see third_party/NOTICE.md.
func SetErrorHandler(f func(msg string)) { edge.ErrorHandler = f }

func fatalf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if edge.ErrorHandler != nil {
		func() {
			defer func() { _ = recover() }()
			edge.ErrorHandler(msg)
		}()
	}
	log.Fatal(msg)
}

// InternalErrorHandler, if set, hears about panics recovered in bound calls and
// dispatched functions (for a log).
var InternalErrorHandler func(msg string)

type WebViewOptions struct {
	Window unsafe.Pointer
	Debug  bool

	// DataPath specifies the datapath for the WebView2 runtime to use for the
	// browser instance.
	DataPath string

	// AutoFocus will try to keep the WebView2 widget focused when the window
	// is focused.
	AutoFocus bool

	// WindowOptions customizes the window that is created to embed the
	// WebView2 widget.
	WindowOptions WindowOptions
}

// New creates a new webview in a new window.
func New(debug bool) WebView { return NewWithOptions(WebViewOptions{Debug: debug}) }

// NewWindow creates a new webview using an existing window.
//
// Deprecated: Use NewWithOptions.
func NewWindow(debug bool, window unsafe.Pointer) WebView {
	return NewWithOptions(WebViewOptions{Debug: debug, Window: window})
}

// NewWithOptions creates a new webview using the provided options.
func NewWithOptions(options WebViewOptions) WebView {
	w := &webview{}
	w.bindings = map[string]interface{}{}
	w.autofocus = options.AutoFocus

	chromium := edge.NewChromium()
	chromium.MessageCallback = w.msgcb
	chromium.DataPath = options.DataPath
	// MODIFIED: the page needs no browser permission (upstream allowed clipboard reading).
	chromium.SetGlobalPermission(edge.CoreWebView2PermissionStateDeny)

	w.browser = chromium
	w.mainthread, _, _ = w32.Kernel32GetCurrentThreadID.Call()
	StartupCancelled = false
	if !w.CreateWithOptions(options.WindowOptions) {
		StartupCancelled = chromium.QuitDuringInit
		return nil
	}

	// MODIFIED: context menu, dev tools, zoom, browser shortcuts and file drop
	// are all switched off (unless Debug); failures here are not fatal.
	chromium.Harden(options.Debug)
	if options.WindowOptions.HasBackground {
		c := options.WindowOptions.BackgroundRGB
		chromium.SetBackground(c[0], c[1], c[2])
	}

	return w
}

type rpcMessage struct {
	ID     int               `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func jsString(v interface{}) string { b, _ := json.Marshal(v); return string(b) }

// msgcb receives a call from the page. MODIFIED: it returns at once and the
// bound function runs on its own goroutine, so file I/O never freezes the
// window and a modal dialog never runs inside the browser's callback; the
// answer goes back through Dispatch (the UI thread). A panic in the bound
// function becomes a rejected promise instead of ending the program.
func (w *webview) msgcb(msg string) {
	d := rpcMessage{}
	if err := json.Unmarshal([]byte(msg), &d); err != nil {
		log.Printf("invalid RPC message: %v", err)
		return
	}
	go w.handleCall(d)
}

func (w *webview) handleCall(d rpcMessage) {
	id := strconv.Itoa(d.ID)
	reject := func(text string) {
		w.Dispatch(func() {
			w.Eval("window._rpc[" + id + "].reject(" + jsString(text) + "); window._rpc[" + id + "] = undefined")
		})
	}
	defer func() {
		if p := recover(); p != nil {
			if InternalErrorHandler != nil {
				InternalErrorHandler(fmt.Sprintf("call %s: %v", d.Method, p))
			}
			reject("internal error")
		}
	}()
	res, err := w.callbinding(d)
	if err != nil {
		reject(err.Error())
		return
	}
	b, err := json.Marshal(res)
	if err != nil {
		reject(err.Error())
		return
	}
	w.Dispatch(func() {
		w.Eval("window._rpc[" + id + "].resolve(" + string(b) + "); window._rpc[" + id + "] = undefined")
	})
}

func (w *webview) callbinding(d rpcMessage) (interface{}, error) {
	w.m.Lock()
	f, ok := w.bindings[d.Method]
	w.m.Unlock()
	if !ok {
		return nil, nil
	}

	v := reflect.ValueOf(f)
	isVariadic := v.Type().IsVariadic()
	numIn := v.Type().NumIn()
	if (isVariadic && len(d.Params) < numIn-1) || (!isVariadic && len(d.Params) != numIn) {
		return nil, errors.New("function arguments mismatch")
	}
	args := []reflect.Value{}
	for i := range d.Params {
		var arg reflect.Value
		if isVariadic && i >= numIn-1 {
			arg = reflect.New(v.Type().In(numIn - 1).Elem())
		} else {
			arg = reflect.New(v.Type().In(i))
		}
		if err := json.Unmarshal(d.Params[i], arg.Interface()); err != nil {
			return nil, err
		}
		args = append(args, arg.Elem())
	}

	errorType := reflect.TypeOf((*error)(nil)).Elem()
	res := v.Call(args)
	switch len(res) {
	case 0:
		// No results from the function, just return nil
		return nil, nil

	case 1:
		// One result may be a value, or an error
		if res[0].Type().Implements(errorType) {
			if res[0].Interface() != nil {
				return nil, res[0].Interface().(error)
			}
			return nil, nil
		}
		return res[0].Interface(), nil

	case 2:
		// Two results: first one is value, second is error
		if !res[1].Type().Implements(errorType) {
			return nil, errors.New("second return value must be an error")
		}
		if res[1].Interface() == nil {
			return res[0].Interface(), nil
		}
		return res[0].Interface(), res[1].Interface().(error)

	default:
		return nil, errors.New("unexpected number of return values")
	}
}

func wndproc(hwnd, msg, wp, lp uintptr) uintptr {
	if w, ok := getWindowContext(hwnd).(*webview); ok {
		switch msg {
		case w32.WMApp:
			w.drain()
		case w32.WMDpiChanged:
			// MODIFIED: move to the rectangle Windows suggests for the new monitor's DPI.
			if r := (*w32.Rect)(unsafe.Pointer(lp)); r != nil {
				_, _, _ = w32.User32SetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top),
					uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), w32.SWPNoZOrder|w32.SWPNoActivate)
			}
		case w32.WMMove, w32.WMMoving:
			_ = w.browser.NotifyParentWindowPositionChanged()
		case w32.WMNCLButtonDown:
			_, _, _ = w32.User32SetFocus.Call(w.hwnd)
			r, _, _ := w32.User32DefWindowProcW.Call(hwnd, msg, wp, lp)
			return r
		case w32.WMSize:
			w.browser.Resize()
		case w32.WMActivate:
			if wp == w32.WAInactive {
				break
			}
			if w.autofocus {
				w.browser.Focus()
			}
		case w32.WMClose:
			_, _, _ = w32.User32DestroyWindow.Call(hwnd)
		case w32.WMDestroy:
			if c, ok := w.browser.(interface{ Close() }); ok {
				c.Close() // MODIFIED: release the controller so the browser process can exit
			}
			w.Terminate()
		case w32.WMGetMinMaxInfo:
			lpmmi := (*w32.MinMaxInfo)(unsafe.Pointer(lp))
			if w.maxsz.X > 0 && w.maxsz.Y > 0 {
				lpmmi.PtMaxSize = w.maxsz
				lpmmi.PtMaxTrackSize = w.maxsz
			}
			if w.minsz.X > 0 && w.minsz.Y > 0 {
				// MODIFIED: never ask for a minimum size larger than the screen's work area.
				lpmmi.PtMinTrackSize = w.minsz
				if wa := workAreaFor(hwnd); wa.Right > wa.Left && wa.Bottom > wa.Top {
					lpmmi.PtMinTrackSize.X = min32(lpmmi.PtMinTrackSize.X, wa.Right-wa.Left)
					lpmmi.PtMinTrackSize.Y = min32(lpmmi.PtMinTrackSize.Y, wa.Bottom-wa.Top)
				}
			}
		default:
			r, _, _ := w32.User32DefWindowProcW.Call(hwnd, msg, wp, lp)
			return r
		}
		return 0
	}
	r, _, _ := w32.User32DefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func (w *webview) Create(debug bool, window unsafe.Pointer) bool {
	// This function signature stopped making sense a long time ago.
	// It is but legacy cruft at this point.
	return w.CreateWithOptions(WindowOptions{})
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// workAreaFor returns the work area (screen minus taskbar) of the monitor the
// window is on, or of the primary monitor when hwnd is 0. Zero Rect on failure.
func workAreaFor(hwnd uintptr) (r w32.Rect) {
	if hwnd != 0 {
		mon, _, _ := w32.User32MonitorFromWindow.Call(hwnd, w32.MonitorDefaultToNearest)
		if mon != 0 {
			mi := w32.MonitorInfo{CbSize: uint32(unsafe.Sizeof(w32.MonitorInfo{}))}
			if ok, _, _ := w32.User32GetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); ok != 0 {
				return mi.RcWork
			}
		}
	}
	if ok, _, _ := w32.User32SystemParametersInfoW.Call(w32.SPIGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0); ok == 0 {
		return w32.Rect{}
	}
	return r
}

func (w *webview) CreateWithOptions(opts WindowOptions) bool {
	var hinstance windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &hinstance)

	// Icons: the big one for the task switcher, the small one for the title bar.
	// MODIFIED: sizes come from the system metrics instead of one LR_DEFAULTSIZE image.
	loadIcon := func(cx, cy uintptr) uintptr {
		if opts.IconId == 0 {
			ico, _, _ := w32.User32LoadImageW.Call(uintptr(hinstance), 32512, cx, cy, 0) // IDI_APPLICATION
			return ico
		}
		ico, _, _ := w32.User32LoadImageW.Call(uintptr(hinstance), uintptr(opts.IconId), 1, cx, cy, w32.LR_SHARED)
		return ico
	}
	bigW, _, _ := w32.User32GetSystemMetrics.Call(w32.SystemMetricsCxIcon)
	bigH, _, _ := w32.User32GetSystemMetrics.Call(w32.SystemMetricsCyIcon)
	smW, _, _ := w32.User32GetSystemMetrics.Call(49) // SM_CXSMICON
	smH, _, _ := w32.User32GetSystemMetrics.Call(50) // SM_CYSMICON
	icon := loadIcon(bigW, bigH)
	iconSm := loadIcon(smW, smH)

	var brush uintptr
	if opts.HasBackground {
		c := opts.BackgroundRGB
		brush, _, _ = w32.Gdi32CreateSolidBrush.Call(uintptr(uint32(c[0]) | uint32(c[1])<<8 | uint32(c[2])<<16)) // COLORREF 0x00BBGGRR
	}

	className, _ := windows.UTF16PtrFromString("webview")
	wc := w32.WndClassExW{
		CbSize:        uint32(unsafe.Sizeof(w32.WndClassExW{})),
		HInstance:     hinstance,
		LpszClassName: className,
		HIcon:         windows.Handle(icon),
		HIconSm:       windows.Handle(iconSm),
		HbrBackground: windows.Handle(brush),
		LpfnWndProc:   windows.NewCallback(wndproc),
	}
	_, _, _ = w32.User32RegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	windowName, _ := windows.UTF16PtrFromString(opts.Title)

	windowWidth := int32(opts.Width)
	if windowWidth <= 0 {
		windowWidth = 640
	}
	windowHeight := int32(opts.Height)
	if windowHeight <= 0 {
		windowHeight = 480
	}

	// MODIFIED: signed arithmetic and a work-area clamp. Upstream computed the
	// centred position with unsigned numbers, which wrapped to a huge value when
	// the window was larger than the screen and put the title bar out of reach.
	posX, posY := int32(-0x80000000), int32(-0x80000000) // CW_USEDEFAULT
	if wa := workAreaFor(0); wa.Right > wa.Left && wa.Bottom > wa.Top {
		windowWidth = min32(windowWidth, wa.Right-wa.Left)
		windowHeight = min32(windowHeight, wa.Bottom-wa.Top)
		if opts.Center {
			posX = wa.Left + (wa.Right-wa.Left-windowWidth)/2
			posY = wa.Top + (wa.Bottom-wa.Top-windowHeight)/2
		}
	}

	w.hwnd, _, _ = w32.User32CreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0xCF0000, // WS_OVERLAPPEDWINDOW
		uintptr(uint32(posX)),
		uintptr(uint32(posY)),
		uintptr(windowWidth),
		uintptr(windowHeight),
		0,
		0,
		uintptr(hinstance),
		0,
	)
	if w.hwnd == 0 {
		return false
	}
	setWindowContext(w.hwnd, w)

	_, _, _ = w32.User32ShowWindow.Call(w.hwnd, w32.SWShow)
	_, _, _ = w32.User32UpdateWindow.Call(w.hwnd)
	_, _, _ = w32.User32SetFocus.Call(w.hwnd)

	if !w.browser.Embed(w.hwnd) {
		return false
	}
	w.browser.Resize()
	return true
}

func (w *webview) Destroy() {
	_, _, _ = w32.User32PostMessageW.Call(w.hwnd, w32.WMClose, 0, 0)
}

func (w *webview) Run() {
	var msg w32.Msg
	for {
		_, _, _ = w32.User32GetMessageW.Call(
			uintptr(unsafe.Pointer(&msg)),
			0,
			0,
			0,
		)
		if msg.Message == w32.WMApp {
			w.drain()
		} else if msg.Message == w32.WMQuit {
			return
		}
		r, _, _ := w32.User32GetAncestor.Call(uintptr(msg.Hwnd), w32.GARoot)
		r, _, _ = w32.User32IsDialogMessage.Call(r, uintptr(unsafe.Pointer(&msg)))
		if r != 0 {
			continue
		}
		_, _, _ = w32.User32TranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = w32.User32DispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// drain runs the functions queued by Dispatch. MODIFIED: a panic in one of them
// is recovered so the message loop survives.
func (w *webview) drain() {
	w.m.Lock()
	q := append([]func(){}, w.dispatchq...)
	w.dispatchq = []func(){}
	w.m.Unlock()
	for _, f := range q {
		func() {
			defer func() {
				if p := recover(); p != nil && InternalErrorHandler != nil {
					InternalErrorHandler(fmt.Sprintf("dispatched function: %v", p))
				}
			}()
			f()
		}()
	}
}

func (w *webview) Terminate() {
	_, _, _ = w32.User32PostQuitMessage.Call(0)
}

func (w *webview) Window() unsafe.Pointer {
	return unsafe.Pointer(w.hwnd)
}

func (w *webview) Navigate(url string) {
	w.browser.Navigate(url)
}

func (w *webview) SetHtml(html string) {
	w.browser.NavigateToString(html)
}

func (w *webview) SetTitle(title string) {
	_title, err := windows.UTF16FromString(title)
	if err != nil {
		_title, _ = windows.UTF16FromString("")
	}
	_, _, _ = w32.User32SetWindowTextW.Call(w.hwnd, uintptr(unsafe.Pointer(&_title[0])))
}

func (w *webview) SetSize(width int, height int, hints Hint) {
	index := w32.GWLStyle
	style := w32.GetWindowLong(w.hwnd, index)
	if hints == HintFixed {
		style &^= (w32.WSThickFrame | w32.WSMaximizeBox)
	} else {
		style |= (w32.WSThickFrame | w32.WSMaximizeBox)
	}
	w32.SetWindowLong(w.hwnd, index, style)

	if hints == HintMax {
		w.maxsz.X = int32(width)
		w.maxsz.Y = int32(height)
	} else if hints == HintMin {
		w.minsz.X = int32(width)
		w.minsz.Y = int32(height)
	} else {
		r := w32.Rect{}
		r.Left = 0
		r.Top = 0
		r.Right = int32(width)
		r.Bottom = int32(height)
		_, _, _ = w32.User32AdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), w32.WSOverlappedWindow, 0)
		_, _, _ = w32.User32SetWindowPos.Call(
			w.hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top),
			w32.SWPNoZOrder|w32.SWPNoActivate|w32.SWPNoMove|w32.SWPFrameChanged)
		w.browser.Resize()
	}
}

func (w *webview) Init(js string) {
	w.browser.Init(js)
}

func (w *webview) Eval(js string) {
	w.browser.Eval(js)
}

// Dispatch runs f on the UI thread. MODIFIED: the wake-up message is posted to
// the window instead of the thread. A thread message (hwnd NULL) is swallowed
// by the nested message loop of a modal dialog and the queue then stalls until
// the dialog closes; a window message is dispatched by every loop.
func (w *webview) Dispatch(f func()) {
	w.m.Lock()
	w.dispatchq = append(w.dispatchq, f)
	w.m.Unlock()
	if w.hwnd != 0 {
		_, _, _ = w32.User32PostMessageW.Call(w.hwnd, w32.WMApp, 0, 0)
		return
	}
	_, _, _ = w32.User32PostThreadMessageW.Call(w.mainthread, w32.WMApp, 0, 0)
}

func (w *webview) Bind(name string, f interface{}) error {
	v := reflect.ValueOf(f)
	if v.Kind() != reflect.Func {
		return errors.New("only functions can be bound")
	}
	if n := v.Type().NumOut(); n > 2 {
		return errors.New("function may only return a value or a value+error")
	}
	w.m.Lock()
	w.bindings[name] = f
	w.m.Unlock()

	w.Init("(function() { var name = " + jsString(name) + ";" + `
		var RPC = window._rpc = (window._rpc || {nextSeq: 1});
		window[name] = function() {
		  var seq = RPC.nextSeq++;
		  var promise = new Promise(function(resolve, reject) {
			RPC[seq] = {
			  resolve: resolve,
			  reject: reject,
			};
		  });
		  window.external.invoke(JSON.stringify({
			id: seq,
			method: name,
			params: Array.prototype.slice.call(arguments),
		  }));
		  return promise;
		}
	})()`)

	return nil
}
