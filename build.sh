#!/bin/bash
# Build the Windows installer on Windows (Git Bash) or Linux. go.mod pins Go 1.24.7 and the script sets
# GOTOOLCHAIN=local, so that exact toolchain must be the one on PATH.
#
#   ./build.sh            format check, vet, unit tests, build twice (must be identical), validate the .exe
#   ./build.sh --fast     skip the unit tests
#
# Output: dist/FEAR-Exclusive-Console-Content-Installer.exe  (one self-contained
# Windows x64 file; the HTML UI, the licenses and the WebView2 loader are embedded).
# No network is needed: all Go dependencies are vendored in third_party/ (see
# third_party/NOTICE.md) and go.mod pins the toolchain. The build is reproducible:
# the same sources and toolchain give a byte-identical file.
#
# Reference data for the full test run (tests skip with a clear message without it): see README.md for the
# FEAR_REF_* test variables (tests/ref_env.sh sets them all from one folder).
#
# llvm-rc (from LLVM) is optional: with it the manifest, icon and version info are recompiled from
# cmd/fearinstaller/res; without it the checked-in cmd/fearinstaller/rsrc_windows_amd64.syso is used as it is.
set -euo pipefail
cd "$(dirname "$0")"

export GOPROXY=off GOTOOLCHAIN=local CGO_ENABLED=0 GOFLAGS=-buildvcs=false
OUT=dist/FEAR-Exclusive-Console-Content-Installer.exe
step() { printf '\n==> %s\n' "$*"; }
tmp="" second=""
trap 'rm -rf "$tmp" "$second"' EXIT   # temporary files go even when a step fails

step "Tool versions"
go version
# The pinned toolchain is what makes the build reproducible: another Go version gives another exe.
case "$(go version)" in
  *" go1.24.7 "*) ;;
  *) echo "Go 1.24.7 is required (see go.mod)"; exit 1 ;;
esac
command -v llvm-rc >/dev/null && echo "llvm-rc: found" || echo "llvm-rc: missing (the checked-in cmd/fearinstaller/rsrc_windows_amd64.syso is used as it is)"

step "Formatting (gofmt)"
unformatted=$(gofmt -l cmd internal ui)
if [ -n "$unformatted" ]; then echo "needs gofmt:"; echo "$unformatted"; exit 1; fi
echo "ok"

step "Static checks (go vet, linux and windows targets)"
go vet ./cmd/... ./internal/... ./ui/...
GOOS=windows GOARCH=amd64 go vet ./cmd/fearinstaller ./internal/...
echo "ok"

if [ "${1:-}" != "--fast" ]; then
  step "Unit tests"
  [ -n "${FEAR_REF_PS3:-}" ] && [ -n "${FEAR_REF_X360:-}" ] || echo "note: FEAR_REF_PS3 / FEAR_REF_X360 not set; tests that need the reference data will be skipped"
  go test -count=1 ./cmd/... ./internal/... ./ui/...
fi

step "Windows resources (manifest, icon, version info)"
# app.rc -> app.res (llvm-rc) -> COFF object (cmd/res2syso, which writes the same bytes as
# llvm-cvtres /MACHINE:X64 /TIMESTAMP:0; timestamp 0 keeps the object, and so the exe, reproducible).
# Without llvm-rc the checked-in .syso is used as it is.
if command -v llvm-rc >/dev/null; then
  tmp=$(mktemp -d)
  (cd cmd/fearinstaller/res && llvm-rc -FO "$tmp/app.res" app.rc)
  go run ./cmd/res2syso -machine x64 -in "$tmp/app.res" -out cmd/fearinstaller/rsrc_windows_amd64.syso
  echo "cmd/fearinstaller/rsrc_windows_amd64.syso"
fi

build() {
  GOOS=windows GOARCH=amd64 go build -trimpath -buildvcs=false -ldflags "-H windowsgui -s -w -buildid=" -o "$1" ./cmd/fearinstaller
}
step "Compile for windows/amd64 (GUI subsystem), twice"
mkdir -p dist
build "$OUT"
second=$(mktemp)
build "$second"
if cmp -s "$OUT" "$second"; then echo "reproducible: both builds are identical"; else echo "NOT reproducible: the two builds differ"; exit 1; fi
ls -l "$OUT"
sha256sum "$OUT"

step "Validate the executable"
go run ./cmd/pecheck "$OUT"
