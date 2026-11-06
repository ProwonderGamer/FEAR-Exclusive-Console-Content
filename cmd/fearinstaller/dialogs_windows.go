//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	ole32   = windows.NewLazySystemDLL("ole32.dll")
	user32  = windows.NewLazySystemDLL("user32.dll")

	procSHBrowseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
	procCoCreateInstance    = ole32.NewProc("CoCreateInstance")
	procMessageBox          = user32.NewProc("MessageBoxW")
)

// nativeDialogs implements app.Dialogs. The preferred dialog is the Vista-style
// IFileOpenDialog in folder mode (long paths, no MAX_PATH limit); the classic
// folder browser is the fallback if COM cannot create one. Both only read the
// user's choice; neither creates or changes anything, none adds to the Recent
// list, and none changes the working directory.
//
// They must run on the UI thread (a single-threaded COM apartment), which is why
// the back end hands them to the window's Dispatch queue instead of calling
// them inside the browser's message callback.
type nativeDialogs struct{ owner func() uintptr }

// ------------------------------------------------------- COM plumbing ---

// comCall calls slot `slot` of the vtable of COM object obj. The directive
// keeps pointer arguments converted to uintptr alive and unmoved for the call.
//
//go:uintptrescapes
func comCall(obj unsafe.Pointer, slot int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, slot*int(unsafe.Sizeof(uintptr(0)))))
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(obj))
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

// Slots (vtable indexes) of the interfaces used, from shobjidl_core.h. IFileOpenDialog
// derives from IFileDialog, which derives from IModalWindow: IUnknown (0-2), Show (3), and so on
// as numbered below.
const (
	slotRelease = 2
	// IModalWindow / IFileDialog
	slotShow          = 3
	slotSetOptions    = 9
	slotGetOptions    = 10
	slotSetTitle      = 17
	slotGetResult     = 20
	slotSetClientGuid = 24 // after GetResult 20, AddPlace 21, SetDefaultExtension 22, Close 23
	// IShellItem
	slotGetDisplayName = 5
)

const (
	fosNoChangeDir      = 0x00000008
	fosPickFolders      = 0x00000020
	fosForceFileSystem  = 0x00000040
	fosPathMustExist    = 0x00000800
	fosDontAddToRecent  = 0x02000000
	sigdnFileSysPath    = 0x80058000
	hrCancelled         = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)
	clsctxInprocServer  = 1
	clsidFileOpenDialog = "{DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7}"
	iidIFileOpenDialog  = "{D57C7288-D4AD-4768-BE02-9D969532D960}"
)

// Client GUIDs for IFileDialog::SetClientGuid. Windows keeps the last folder,
// view and size of a dialog per client GUID (per user, across runs), so each
// Browse button gets its own GUID and remembers its own folder. They are fixed
// identifiers generated once for this program; never change them, or every
// row forgets its folder.
const (
	guidPCFolder   = "{1522AD62-AE37-4FA6-A4F4-8EB9FA33B7CE}" // key "pc": game folder
	guidPS3Folder  = "{96A0E1BB-6B7E-4D72-947B-734C8A6D6372}" // key "ps3": PlayStation 3 disc folder
	guidX360Folder = "{EFE8233D-D451-4F3C-B39B-98E5787E5AD9}" // key "x360": Xbox 360 folder
)

// clientGuids maps the Dialogs key given by the app to its client GUID.
var clientGuids = map[string]string{
	"pc":   guidPCFolder,
	"ps3":  guidPS3Folder,
	"x360": guidX360Folder,
}

// pickModern shows an IFileOpenDialog in folder mode. handled is false when the dialog could not
// be created or configured (the caller then falls back to the classic dialog).
// key picks the dialog's remembered folder (see clientGuids).
func (d nativeDialogs) pickModern(key, title string) (path string, handled bool) {
	clsid, err1 := windows.GUIDFromString(clsidFileOpenDialog)
	iid, err2 := windows.GUIDFromString(iidIFileOpenDialog)
	if err1 != nil || err2 != nil {
		return "", false
	}
	var dlg unsafe.Pointer
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsid)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&dlg))); int32(hr) < 0 || dlg == nil {
		return "", false
	}
	defer comCall(dlg, slotRelease)

	var opts uint32
	if hr := comCall(dlg, slotGetOptions, uintptr(unsafe.Pointer(&opts))); int32(hr) < 0 {
		return "", false
	}
	opts |= fosPickFolders | fosForceFileSystem | fosPathMustExist | fosNoChangeDir | fosDontAddToRecent
	if hr := comCall(dlg, slotSetOptions, uintptr(opts)); int32(hr) < 0 {
		return "", false
	}
	// Optional: if the call fails the dialog just uses the shared default memory.
	if s, ok := clientGuids[key]; ok {
		if g, err := windows.GUIDFromString(s); err == nil {
			comCall(dlg, slotSetClientGuid, uintptr(unsafe.Pointer(&g)))
		}
	}
	if t, err := windows.UTF16PtrFromString(title); err == nil {
		comCall(dlg, slotSetTitle, uintptr(unsafe.Pointer(t)))
	}

	hr := comCall(dlg, slotShow, d.owner())
	if uint32(hr) == hrCancelled {
		return "", true
	}
	if int32(hr) < 0 {
		return "", true // the dialog opened and failed: do not pop a second one
	}
	var item unsafe.Pointer
	if hr := comCall(dlg, slotGetResult, uintptr(unsafe.Pointer(&item))); int32(hr) < 0 || item == nil {
		return "", true
	}
	defer comCall(item, slotRelease)
	var name *uint16
	if hr := comCall(item, slotGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&name))); int32(hr) < 0 || name == nil {
		return "", true // not a file-system item (a library or device): nothing usable
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(name)))
	return windows.UTF16PtrToString(name), true
}

func utf16Ptr(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }

// ------------------------------------------------------ classic dialogs ---

// browseInfo mirrors BROWSEINFOW (64-bit layout).
type browseInfo struct {
	Owner       uintptr
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Fn          uintptr
	LParam      uintptr
	Image       int32
}

const (
	bifReturnOnlyFSDirs = 0x0001
	bifEditBox          = 0x0010
	bifNewDialogStyle   = 0x0040
)

// Compile-time layout checks for the structures passed to the classic dialogs
// (offsets from the Windows SDK headers, 64-bit).
var (
	_ [64]byte = [unsafe.Sizeof(browseInfo{})]byte{}
	_ [16]byte = [unsafe.Offsetof(browseInfo{}.DisplayName)]byte{}
	_ [24]byte = [unsafe.Offsetof(browseInfo{}.Title)]byte{}
	_ [32]byte = [unsafe.Offsetof(browseInfo{}.Flags)]byte{}
	_ [56]byte = [unsafe.Offsetof(browseInfo{}.Image)]byte{}
)

// pickFolderClassic shows SHBrowseForFolder. Its path conversion is limited to
// MAX_PATH characters, which is why it is only the fallback.
func (d nativeDialogs) pickFolderClassic(title string) string {
	display := make([]uint16, windows.MAX_PATH)
	bi := browseInfo{
		Owner:       d.owner(),
		DisplayName: &display[0],
		Title:       utf16Ptr(title),
		Flags:       bifReturnOnlyFSDirs | bifEditBox | bifNewDialogStyle,
	}
	pidl, _, _ := procSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer procCoTaskMemFree.Call(pidl)
	buf := make([]uint16, windows.MAX_PATH)
	if ok, _, _ := procSHGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&buf[0]))); ok == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

// PickFolder shows the folder chooser. "" means cancelled. The classic fallback
// has no per-row folder memory, so it ignores key.
func (d nativeDialogs) PickFolder(key, title string) string {
	if p, ok := d.pickModern(key, title); ok {
		return p
	}
	return d.pickFolderClassic(title)
}

// messageBox shows a simple error box (used before the UI exists and for fatal errors).
func messageBox(title, text string) {
	procMessageBox.Call(0, uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(unsafe.Pointer(utf16Ptr(title))), 0x10|0x00010000) // MB_ICONERROR | MB_SETFOREGROUND
}
