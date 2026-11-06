package webviewloader

// MODIFIED for the F.E.A.R. Console Restoration installer (see third_party/NOTICE.md):
// upstream first tried LoadLibrary("WebView2Loader") with the default DLL search
// order (which includes the application and current folders) and fell back to the
// embedded copy. That lets a planted WebView2Loader.dll next to the .exe, or in
// the current folder, run instead. This version never touches the disk: the
// embedded, hash-pinned loader is always the one used.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"unsafe"

	"github.com/jchv/go-winloader"
	"golang.org/x/sys/windows"
)

// EmbeddedLoaderSHA256 is the SHA-256 of the embedded x64 WebView2Loader.dll
// (Microsoft WebView2 SDK, BSD 3-Clause). It is checked before the DLL is mapped.
const EmbeddedLoaderSHA256 = "c4674acf95f0800793a4a6d61132adf5dfa694c218e482d86093494c4e84100a"

var (
	memOnce                                         sync.Once
	memModule                                       winloader.Module
	memCreate                                       winloader.Proc
	memCompareBrowserVersions                       winloader.Proc
	memGetAvailableCoreWebView2BrowserVersionString winloader.Proc
	memErr                                          error
)

func loadFromMemory() error {
	memOnce.Do(func() {
		if sum := sha256.Sum256(WebView2Loader); hex.EncodeToString(sum[:]) != EmbeddedLoaderSHA256 {
			memErr = fmt.Errorf("embedded WebView2Loader.dll does not match its expected checksum")
			return
		}
		memModule, memErr = winloader.LoadFromMemory(WebView2Loader)
		if memErr != nil {
			memErr = fmt.Errorf("unable to load the embedded WebView2Loader.dll: %w", memErr)
			return
		}
		memCreate = memModule.Proc("CreateCoreWebView2EnvironmentWithOptions")
		memCompareBrowserVersions = memModule.Proc("CompareBrowserVersions")
		memGetAvailableCoreWebView2BrowserVersionString = memModule.Proc("GetAvailableCoreWebView2BrowserVersionString")
		if memCreate == nil || memCompareBrowserVersions == nil || memGetAvailableCoreWebView2BrowserVersionString == nil {
			memErr = fmt.Errorf("the embedded WebView2Loader.dll lacks an expected export")
		}
	})
	return memErr
}

// CompareBrowserVersions will compare the 2 given versions and return:
//
//	-1 = v1 < v2
//	 0 = v1 == v2
//	 1 = v1 > v2
func CompareBrowserVersions(v1 string, v2 string) (int, error) {
	_v1, err := windows.UTF16PtrFromString(v1)
	if err != nil {
		return 0, err
	}
	_v2, err := windows.UTF16PtrFromString(v2)
	if err != nil {
		return 0, err
	}
	if err := loadFromMemory(); err != nil {
		return 0, err
	}
	var result int
	_, _, err = memCompareBrowserVersions.Call(
		uint64(uintptr(unsafe.Pointer(_v1))),
		uint64(uintptr(unsafe.Pointer(_v2))),
		uint64(uintptr(unsafe.Pointer(&result))))
	if err != windows.ERROR_SUCCESS {
		return result, err
	}
	return result, nil
}

// GetInstalledVersion returns the installed version of the webview2 runtime.
// If there is no version installed, a blank string is returned.
func GetInstalledVersion() (string, error) {
	// GetAvailableCoreWebView2BrowserVersionString(PCWSTR browserExecutableFolder, LPWSTR *versionInfo)
	// returns an HRESULT (a 32-bit value, even on 64-bit systems).
	if err := loadFromMemory(); err != nil {
		return "", err
	}
	var result *uint16
	hr64, _, _ := memGetAvailableCoreWebView2BrowserVersionString.Call(
		uint64(uintptr(unsafe.Pointer(nil))),
		uint64(uintptr(unsafe.Pointer(&result))))
	hr := uintptr(hr64)
	defer windows.CoTaskMemFree(unsafe.Pointer(result)) // Safe even if result is nil
	if hr != uintptr(windows.S_OK) {
		if hr&0xFFFF == uintptr(windows.ERROR_FILE_NOT_FOUND) {
			// The runtime is not installed: a blank string, not an error.
			return "", nil
		}
		return "", fmt.Errorf("GetAvailableCoreWebView2BrowserVersionString returned HRESULT 0x%X", hr)
	}
	return windows.UTF16PtrToString(result), nil
}

// CreateCoreWebView2EnvironmentWithOptions calls the loader's routine of the same name.
func CreateCoreWebView2EnvironmentWithOptions(browserExecutableFolder, userDataFolder *uint16, environmentOptions uintptr, environmentCompletedHandle uintptr) (uintptr, error) {
	if err := loadFromMemory(); err != nil {
		return 0, err
	}
	res, _, _ := memCreate.Call(
		uint64(uintptr(unsafe.Pointer(browserExecutableFolder))),
		uint64(uintptr(unsafe.Pointer(userDataFolder))),
		uint64(environmentOptions),
		uint64(environmentCompletedHandle),
	)
	return uintptr(res), nil
}
