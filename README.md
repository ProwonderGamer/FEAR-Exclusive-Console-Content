# F.E.A.R. Exclusive Console Content

Restores console-exclusive content from the PlayStation 3 and Xbox 360 versions of F.E.A.R.: First Encounter Assault Recon to the PC version.

This includes the **SFOD-D bonus mission** and **Watson SAS-12 shotgun** from the PlayStation 3 version, along with the **Holiday bonus mission**, **SM15 machine pistol**, **Instant Action maps** and **Xbox 360 HUD** from the Xbox 360 version.

**No copyrighted game files are included in the download.** Instead, the installer builds the mod locally using files from your own copies of the game. You will need:

- **PC:** F.E.A.R. (Steam or GOG) with [EchoPatch](https://github.com/Wemino/EchoPatch) installed.
- **PlayStation 3:** your copy of the game as a decrypted disc folder.
- **Xbox 360:** your copy of the game as an extracted disc folder.

For complete requirements and step-by-step installation instructions, see **README.txt** included in the ZIP archive.

## About this repository

This is the source code of `FEAR-Exclusive-Console-Content-Installer.exe`, the Windows installer of the mod.
Neither this repository nor the release download contains files from the game. The installer reads the
player's own copies of the game (the PC game, a decrypted PlayStation 3 disc folder and an extracted Xbox 360
disc folder) and builds every mod file from them on the player's computer.

## What the installer does

1. Finds the PC game (Steam or GOG) and checks that EchoPatch is installed (`internal/pcgame`).
2. Checks the two console copies the player picks (disc folders), matched against the known files of the
   supported discs (`internal/verify`, `internal/vfs`, `internal/refdata`). The checks can also read disc
   images, but the content is built only from folders, so a disc image is only checked.
3. Builds the mod files from those copies into a work folder inside the game folder: worlds, models,
   textures, sounds, the game databases and the mod's text (`internal/content` and the converters it calls).
   `internal/content/coverage_table.go` lists every mod file with the hash of the file that was tested in
   game; golden tests prove that the builders reproduce it.
4. Fills in the game add-on (`dinput8_hook.dll`, see below), swaps the work folder in as
   `FEAR_ConsoleRestoration`, mounts it in `Default.archcfg` and writes an install record, undoing
   everything on any failure (`internal/install`).
5. Uninstall removes the mod and restores the files it changed.

The installer only writes when every file of the choice can be built from the copies given; otherwise the
Install step only reads and checks the copies.

Outside the game folder the installer writes only two things: a plain-text log next to the program, named
after it (`FEAR-Exclusive-Console-Content-Installer.log`; rewritten on every start, with the account name
hidden in paths), and the window's WebView2 profile in the temporary folder
(`FEAR-Exclusive-Console-Content-WebView2-<process id>`), which it removes when it closes; a profile left
behind by a crash is removed at the next start. It reads the registry only to find the game (the Steam and
GOG entries) and the installed WebView2 runtime, and writes nothing there.

## The game add-on (dinput8_hook.dll)

The mod's in-game features run in a DLL that EchoPatch loads into the game. Its source code is not part of
this repository. The installer carries it as a binary template,
`internal/install/payload/dinput8_hook_template.dll`, in which every value that comes from the game is zero:
the numbers of its Xbox 360 HUD layout table, a few short game texts, and the bytes it expects to find in
the game's program files before it patches them (its safety checks). At install time `internal/hookfill`
reads those values from the player's Xbox 360 copy and PC game and writes them back;
the result must match the SHA-256 of the build that was tested in game (`install.HookSHA256`), or the
install is refused. The game values come from the English game data, so a PC game in another language is
refused at this step.

## Where the converters come from

The mod was first built with a set of Python scripts that are not published. The Go converters here are
ports of those scripts, checked against the files they made (the golden tests). Comments that name a `.py`
file or "the recipe" refer to the script a converter was ported from, to explain why it does exactly what
it does.

## Layout

| Folder | What it holds |
|---|---|
| `cmd/fearinstaller` | the Windows program: window (WebView2), resources (`res/`: manifest, icon, version info) |
| `cmd/verifycli` | runs the copy checks from a command line (any OS) |
| `cmd/uipreview` | serves the installer page over HTTP with the real back end, for UI work in a browser |
| `cmd/buildcontent` | runs the content builders into an output folder (maintainer tool) |
| `cmd/makerelease` | packs the release ZIP (installer, `README.txt`, `Licenses\`) |
| `cmd/pecheck`, `cmd/res2syso` | validate the built .exe; turn the compiled resources into a Go `.syso` object |
| `cmd/genhashes`, `cmd/mkfixtures` | maintainer helpers (reference hashes, test folders and images) |
| `internal/app` | the back end: selections, plan, install and uninstall jobs |
| `internal/content` | the content-building stages and the list of mod files |
| `internal/install`, `internal/hookfill`, `internal/archcfg` | writing and removing the mod, the add-on, the archive mount |
| `internal/pcgame`, `internal/verify`, `internal/vfs`, `internal/refdata` | finding and checking the copies |
| other `internal/` packages | readers, writers and converters for the game's formats (archives, catalogs, databases, worlds, models, textures, materials, sounds, fonts) |
| `ui` | the installer page (`index.html`), its install-bar sounds (`ui/sounds`) and the joined license text |
| `licenses`, `LICENSE.txt` | third-party license texts (with their sources in `licenses/SOURCES.md`) and the mod's license |
| `release` | `README.txt` for the download |
| `third_party` | vendored Go dependencies (see `third_party/NOTICE.md`; local changes in `third_party/patches`) |
| `tests` | `run_all.sh`, `ref_env.sh` and the browser tests of the page (`tests/ui`) |

## Building

Requirements: Go 1.24.7 (`go.mod` pins it, and the build sets `GOTOOLCHAIN=local`, so that exact version
must be first on `PATH`) and a bash shell (Git Bash on Windows, or Linux). No network access is needed: all
dependencies are vendored. `llvm-rc` from LLVM is optional; with it the manifest, icon and version info are
recompiled from `cmd/fearinstaller/res`, without it the checked-in `cmd/fearinstaller/rsrc_windows_amd64.syso`
is used.

```
./build.sh          # format check, vet, unit tests, build twice (must be identical), validate the .exe
./build.sh --fast   # the same without the unit tests
```

The output is `dist/FEAR-Exclusive-Console-Content-Installer.exe`, one self-contained Windows x64 file. The
build is reproducible: the same sources and toolchain give a byte-identical file.

To pack the download (the source code link in `release/README.txt` must be filled in first):

```
go run ./cmd/makerelease -exe dist/FEAR-Exclusive-Console-Content-Installer.exe -date YYYY-MM-DD -out FEAR-Exclusive-Console-Content.zip
```

## Tests

`go test ./...` runs every unit test. Many tests are golden tests: they rebuild a mod file from the original
game data and compare it with the file that was tested in game. They read the game data from the
`FEAR_REF_*` environment variables (described in `internal/testref/testref.go`) and skip with a message when
a variable is not set. `tests/ref_env.sh` sets them all from one folder:

```
FEAR_REF_ROOT=/path/to/reference-data . tests/ref_env.sh
go test -count=1 -v ./... | grep -c SKIP     # a test that skips has checked nothing
```

`tests/run_all.sh` also runs the browser tests of the page, which need Node with Playwright and Chromium.
Fuzz tests for the disc readers: `go test ./internal/vfs -run '^$' -fuzz FuzzUDF -fuzztime 60s` (also
`FuzzISO` and `FuzzXDVDFS`, and `FuzzSFO` in `internal/verify`).

## License

The mod, including this source code, is under the license in `LICENSE.txt`: free to use, never for sale, and
not to be re-uploaded as is. Parts of the installer come from other authors and keep their own licenses
(`licenses/`): the sound decoders in `internal/audio/atrac3` and `internal/audio/xma2` are translated from
FFmpeg and stay under the GNU Lesser General Public License 2.1 or later; the text drawing in
`internal/loadart` follows FreeType, and the picture steps in `internal/imgops` follow Pillow.

The installer shows F.E.A.R. artwork and the PlayStation 3 and Xbox 360 logos, which belong to their owners
and are not covered by the mod's license. F.E.A.R. and Monolith are trademarks of Warner Bros. Entertainment
Inc. PlayStation is a trademark of Sony Interactive Entertainment Inc. Xbox 360 is a trademark of Microsoft
Corporation. This is an unofficial fan-made mod, not affiliated with or endorsed by Warner Bros., Sony
Interactive Entertainment or Microsoft.
