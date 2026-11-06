#!/bin/bash
# Run every test: the Go unit tests (with the race detector when a C compiler is available) and the
# headless-browser UI tests.
#
#   FEAR_REF_PS3=<PS3_GAME/USRDIR/game folder> FEAR_REF_X360=<extracted Xbox 360 disc root> \
#   [FEAR_REF_PCGAME=<PC game folder with EchoPatch>] [UI_SHOTS=<folder to keep the screenshots in>] \
#   tests/run_all.sh
#
# The other reference variables the golden tests read are described in README.md and set from one
# folder by tests/ref_env.sh. Without the two variables above the tests that need the original game data
# skip with a message and the browser tests are not run (they need fixtures built from that data).
# The browser tests need Node with Playwright + Chromium (the same engine as WebView2). They walk the five
# steps (game, console copies, content, install, play), run the install job (the preview server's job only
# reads and checks) and take a picture of every step; UI_SHOTS keeps them.
set -euo pipefail
cd "$(dirname "$0")/.."
export GOPROXY=off GOTOOLCHAIN=local
PW=${PLAYWRIGHT_MODULE:-$(npm root -g 2>/dev/null)/playwright/index.mjs}
export PLAYWRIGHT_MODULE=$PW

# The race detector needs cgo and so a C compiler; without one the tests run without it.
if command -v gcc >/dev/null || command -v cc >/dev/null || command -v clang >/dev/null; then
  echo "==> Go unit tests (-race)"
  CGO_ENABLED=1 go test -race -count=1 ./cmd/... ./internal/...
else
  echo "==> Go unit tests (no C compiler found, so no -race)"
  CGO_ENABLED=0 go test -count=1 ./cmd/... ./internal/...
fi

# GOG / dual-store UI tests build their own fake folders: no reference data needed (only Node + Playwright).
if [ -f "$PW" ]; then
  gwork=$(mktemp -d)
  CGO_ENABLED=0 go build -o "$gwork/uipreview" ./cmd/uipreview
  echo; echo "==> GOG and Default.archcfg UI tests (headless Chromium + real back end)"
  node tests/ui/gog_test.mjs "$gwork/uipreview" "$gwork/shots" || { rm -rf "$gwork"; exit 1; }
  rm -rf "$gwork"
else
  echo "SKIP GOG UI tests: Playwright not found at $PW (set PLAYWRIGHT_MODULE)"
fi

if [ -z "${FEAR_REF_PS3:-}" ] || [ -z "${FEAR_REF_X360:-}" ]; then
  echo "SKIP browser tests: set FEAR_REF_PS3 and FEAR_REF_X360 (they build their fixtures from the original game files)"
  exit 0
fi
[ -f "$PW" ] || { echo "SKIP browser tests: Playwright not found at $PW (set PLAYWRIGHT_MODULE)"; exit 0; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
CGO_ENABLED=0 go build -o "$work/uipreview" ./cmd/uipreview
go run ./cmd/mkfixtures -ps3 "$FEAR_REF_PS3" -x360 "$FEAR_REF_X360" -out "$work/fix"
bash tests/ui/mkfix2.sh "$work/fix" "$work/fix2" >/dev/null
mkdir -p "$work/shots"

echo; echo "==> UI tests (headless Chromium + real back end)"
node tests/ui/ui_test.mjs "$work/uipreview" "$work/fix" "$work/fix2" "$work/shots"

if [ -n "${UI_SHOTS:-}" ]; then
  mkdir -p "$UI_SHOTS" && cp "$work"/shots/*.png "$UI_SHOTS"/ && echo "screenshots kept in $UI_SHOTS"
else
  echo "screenshots: $work/shots (deleted when this script ends; set UI_SHOTS=<folder> to keep them)"
fi
