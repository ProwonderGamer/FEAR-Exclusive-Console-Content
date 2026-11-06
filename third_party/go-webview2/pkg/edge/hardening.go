//go:build windows

package edge

import "unsafe"

// Interfaces that extend the ones upstream wraps. Each is reached with
// QueryInterface; if the installed WebView2 Runtime does not offer it, the call
// is skipped (the page has JavaScript fallbacks for every setting here).

// IIDs from WebView2.idl.
const (
	iidSettings3   = "{fdb5ab74-af33-4854-84f0-0a631deb5eba}" // ICoreWebView2Settings3 (AreBrowserAcceleratorKeysEnabled)
	iidController4 = "{97d418d5-a426-4e49-a151-e1a10f327d9e}" // ICoreWebView2Controller4 (AllowExternalDrop)
	iidController2 = "{c979903e-d4ca-4228-92eb-47ee3fa96eab}" // ICoreWebView2Controller2 (DefaultBackgroundColor)
)

// _ICoreWebView2Controller4Vtbl lists every slot of the interface chain
// Controller (23) + Controller2 (2) + Controller3 (8) + Controller4 (2) after IUnknown.
type _ICoreWebView2Controller4Vtbl struct {
	_IUnknownVtbl
	// ICoreWebView2Controller
	GetIsVisible                      ComProc
	PutIsVisible                      ComProc
	GetBounds                         ComProc
	PutBounds                         ComProc
	GetZoomFactor                     ComProc
	PutZoomFactor                     ComProc
	AddZoomFactorChanged              ComProc
	RemoveZoomFactorChanged           ComProc
	SetBoundsAndZoomFactor            ComProc
	MoveFocus                         ComProc
	AddMoveFocusRequested             ComProc
	RemoveMoveFocusRequested          ComProc
	AddGotFocus                       ComProc
	RemoveGotFocus                    ComProc
	AddLostFocus                      ComProc
	RemoveLostFocus                   ComProc
	AddAcceleratorKeyPressed          ComProc
	RemoveAcceleratorKeyPressed       ComProc
	GetParentWindow                   ComProc
	PutParentWindow                   ComProc
	NotifyParentWindowPositionChanged ComProc
	Close                             ComProc
	GetCoreWebView2                   ComProc
	// ICoreWebView2Controller2
	GetDefaultBackgroundColor ComProc
	PutDefaultBackgroundColor ComProc
	// ICoreWebView2Controller3
	GetRasterizationScale              ComProc
	PutRasterizationScale              ComProc
	GetShouldDetectMonitorScaleChanges ComProc
	PutShouldDetectMonitorScaleChanges ComProc
	AddRasterizationScaleChanged       ComProc
	RemoveRasterizationScaleChanged    ComProc
	GetBoundsMode                      ComProc
	PutBoundsMode                      ComProc
	// ICoreWebView2Controller4
	GetAllowExternalDrop ComProc
	PutAllowExternalDrop ComProc
}

type iController4 struct {
	vtbl *_ICoreWebView2Controller4Vtbl
}

// Compile-time layout check: IUnknown (3) + 23 + 2 + 8 + 2 = 38 slots; PutAllowExternalDrop is the last.
var _ [38 * 8]byte = [unsafe.Sizeof(_ICoreWebView2Controller4Vtbl{})]byte{}

// Harden applies the settings that keep the page a closed, single-purpose
// surface: no zoom, no browser shortcuts (reload, find, print...), no external
// file drop, no status bar, no dev tools or context menu unless debug. It is
// best effort: a setting the runtime does not support is skipped.
func (e *Chromium) Harden(debug bool) {
	if e.webview == nil || e.controller == nil {
		return
	}
	if s, err := e.webview.GetSettings(); err == nil && s != nil {
		_ = s.PutIsZoomControlEnabled(false)
		_ = s.PutIsStatusBarEnabled(false)
		_ = s.PutAreDefaultContextMenusEnabled(debug)
		_ = s.PutAreDevToolsEnabled(debug)
		// AreBrowserAcceleratorKeysEnabled lives on ICoreWebView2Settings3.
		var s3 *ICoreWebViewSettings
		if hr, _, _ := s.vtbl.QueryInterface.Call(uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(NewGUID(iidSettings3))), uintptr(unsafe.Pointer(&s3))); hr == 0 && s3 != nil {
			if !debug {
				_ = s3.PutAreBrowserAcceleratorKeysEnabled(false)
			}
			_, _, _ = s3.vtbl.Release.Call(uintptr(unsafe.Pointer(s3)))
		}
	}
	// Controller4: files dropped on the window must not navigate the page.
	var c4 *iController4
	if hr, _, _ := e.controller.vtbl.QueryInterface.Call(uintptr(unsafe.Pointer(e.controller)), uintptr(unsafe.Pointer(NewGUID(iidController4))), uintptr(unsafe.Pointer(&c4))); hr == 0 && c4 != nil {
		_, _, _ = c4.vtbl.PutAllowExternalDrop.Call(uintptr(unsafe.Pointer(c4)), 0)
		_, _, _ = c4.vtbl.Release.Call(uintptr(unsafe.Pointer(c4)))
	}
}

// SetBackground sets the color shown before the page paints (avoids a white flash).
func (e *Chromium) SetBackground(r, g, b uint8) {
	if e.controller == nil {
		return
	}
	if c2 := e.controller.GetICoreWebView2Controller2(); c2 != nil {
		_ = c2.PutDefaultBackgroundColor(COREWEBVIEW2_COLOR{A: 255, R: r, G: g, B: b})
		_, _, _ = c2.vtbl.Release.Call(uintptr(unsafe.Pointer(c2)))
	}
}

// Close releases the controller, which lets the browser process shut down.
func (e *Chromium) Close() {
	if e.controller != nil {
		_, _, _ = e.controller.vtbl.Close.Call(uintptr(unsafe.Pointer(e.controller)))
		_, _, _ = e.controller.vtbl.Release.Call(uintptr(unsafe.Pointer(e.controller)))
		e.controller = nil
	}
	if e.webview != nil {
		_, _, _ = e.webview.vtbl.Release.Call(uintptr(unsafe.Pointer(e.webview)))
		e.webview = nil
	}
}
