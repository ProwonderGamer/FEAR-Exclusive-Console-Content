# Third-party code (vendored)

Everything the installer needs to build is in this folder; no network and no
package manager are used (see the `replace` lines in `../go.mod`). The full
license texts are in `../licenses/` (shipped in the release download) and,
joined, in `../ui/THIRD_PARTY_LICENSES.txt`, which is also embedded in the
executable.

| Component | Version | License | Status |
|---|---|---|---|
| github.com/jchv/go-webview2 (`go-webview2/`) | commit `56598839c808a2340edee99204db479f410e9bf4` | MIT (portions by Serge Zaitsev, MIT; `pkg/edge/guid.go` adapted from go-ole, MIT) | **modified** - see below |
| embedded `WebView2Loader.dll` (`go-webview2/webviewloader/sdk/x64`) | Microsoft WebView2 SDK, SHA-256 `c4674acf95f0800793a4a6d61132adf5dfa694c218e482d86093494c4e84100a` | BSD 3-Clause (Microsoft; `sdk/LICENSE.txt`) | unmodified binary, x64 only |
| github.com/jchv/go-winloader (`go-winloader/`) | commit `c1995be93bd1362cba4d3d0ba6bfe0efdd0bcaf8` | **ISC** (not MIT) | **modified** (one call, with a comment) |
| golang.org/x/sys (`sys/`, `windows` and `windows/registry` only) | v0.33.0 (`3d9a6b80792a3911da1fa665c959a5ede3abf476`) | BSD 3-Clause + patent grant (`PATENTS`) | unmodified, pruned |
| Go standard library and runtime (linked in) | go1.24.7 | BSD 3-Clause | - |

## Modifications

The exact changes are in `patches/` (unified diffs against the pristine commits
above; `patch -p1` inside a clean copy reproduces the vendored tree, after the
pruning listed below).

**go-webview2** (`patches/go-webview2.patch`)
- `webviewloader/module.go`: always loads the embedded loader from memory and
  checks its SHA-256 first; it no longer calls `NewLazyDLL("WebView2Loader")`
  (which used the default DLL search order and could be planted). The x86 and
  arm64 loaders and `module_386.go` / `module_arm64.go` were removed (x64 only).
- `pkg/edge/chromium.go`, `fatal.go`, `hardening.go`, and the two
  `ICoreWebView2NavigationStarting*` files: no `log.Fatal` (errors go to an error
  handler that shows a message box); closing the window during start-up no
  longer dereferences a nil browser; navigations are cancelled unless the host
  started them; zoom, status bar, browser accelerator keys, external drops,
  context menu and dev tools are turned off (dev tools only in debug builds);
  the controller is closed on exit so the browser process can end.
- `webview.go`: bound calls run on their own goroutine with `recover`; `Dispatch`
  posts to the window (not to the thread), so it is not swallowed by a modal
  loop; window placement is signed and clamped to the monitor work area;
  per-monitor DPI changes are handled; background color is set to avoid a white
  flash; all permission requests are denied; icon sizes come from system metrics.
- `internal/w32/*`: extra Win32 procs and structs with compile-time size assertions.

**go-winloader** (`patches/go-winloader.patch`): imports of the in-memory DLL are
resolved with `LoadLibraryEx(..., LOAD_LIBRARY_SEARCH_SYSTEM32)` instead of the
default search order.

## Pruned from the vendored copies (not needed to build, not shipped)

- go-webview2: `.github`, `cmd/` demos, x86 and arm64 loader DLLs.
- go-winloader: `cmd/`, `tinydll/` (a test DLL binary), `*_test.go`.
- x/sys: everything except `windows/` and `windows/registry/` (and their
  non-test sources); the test data (including `testdata/ev-signed-file.exe`),
  `svc`, `mkwinsyscall` and the generator scripts were removed.
