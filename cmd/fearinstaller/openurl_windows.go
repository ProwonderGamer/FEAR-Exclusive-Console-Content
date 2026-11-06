//go:build windows

package main

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procShellExecute = shell32.NewProc("ShellExecuteW")

// openURL opens an https address in the user's default web browser. It is only
// called with the fixed EchoPatch address (app.Bridge.OpenEchoPatchPage); any
// other scheme is refused so ShellExecute can never be handed a program or file.
func openURL(owner uintptr, url string) {
	if !strings.HasPrefix(url, "https://") {
		return
	}
	verb, err1 := windows.UTF16PtrFromString("open")
	target, err2 := windows.UTF16PtrFromString(url)
	if err1 != nil || err2 != nil {
		return
	}
	const swShowNormal = 1
	procShellExecute.Call(owner, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, swShowNormal)
}
