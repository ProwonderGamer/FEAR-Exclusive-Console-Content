//go:build windows

package edge

import "unsafe"

// ICoreWebView2NavigationStartingEventArgs: slot order from WebView2.idl.
type _ICoreWebView2NavigationStartingEventArgsVtbl struct {
	_IUnknownVtbl
	GetUri             ComProc
	GetIsUserInitiated ComProc
	GetIsRedirected    ComProc
	GetRequestHeaders  ComProc
	GetCancel          ComProc
	PutCancel          ComProc
	GetNavigationId    ComProc
}

type ICoreWebView2NavigationStartingEventArgs struct {
	vtbl *_ICoreWebView2NavigationStartingEventArgsVtbl
}

// PutCancel cancels (true) the navigation that is starting.
func (i *ICoreWebView2NavigationStartingEventArgs) PutCancel(cancel bool) {
	_, _, _ = i.vtbl.PutCancel.Call(uintptr(unsafe.Pointer(i)), uintptr(boolToInt(cancel)))
}
