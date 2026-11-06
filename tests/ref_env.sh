# Source this file to point the golden tests at your staged copies of the original game files:
#
#   FEAR_REF_ROOT=/path/to/reference-data . tests/ref_env.sh
#
# FEAR_REF_ROOT is a folder with this layout (every part is optional; tests whose input is missing skip):
#   ps3/                      the PS3_GAME/USRDIR/game folder of a decrypted copy       -> FEAR_REF_PS3
#   x360/                     the extracted Xbox 360 disc root (default.xex, Game/)     -> FEAR_REF_X360
#   pc/                       a PC game folder with the *.Arch00 archives               -> FEAR_REF_PCGAME
#   pcworlds/                 the stock PC *.World00p files                             -> FEAR_REF_PCWORLDS
#   pc_toc/Arch00_TOC.txt     listing of all PC archives (tab separated)                -> FEAR_REF_PC_TOC
#   ps3_extract/ubercatalog.cat/
#                             folder with files extracted from the PS3 ubercatalog.cat
#                             (database_string/English/Loki.strdb); english.cat_TOC.txt
#                             sits next to it, in ps3_extract/                          -> FEAR_REF_PS3_EXTRACT
#   confirmed_mod/            a complete, in-game confirmed mod folder (read only). The few files that
#                             cannot be rebuilt byte for byte (decoded sounds, loading pictures) are
#                             compared with it within a tolerance                       -> FEAR_REF_LIVEMOD
#   gog/                      a copy of the files of a GOG install (FEAR.exe, FEARE_8.Arch00,
#                             Default.archcfg, dinput8.dll, d3d9.dll, dxwrapper.ini)    -> FEAR_REF_GOG
#   arialbd.ttf               optional: the Arial Bold font the loading-picture tests
#                             use (default: %WINDIR%\Fonts\arialbd.ttf)                 -> FEAR_REF_ARIALBD
# The tests themselves never contain a machine-specific path; this is the one place that names a layout.
# Run "go test -v ./..." and count the SKIP lines: a test that skips has not checked anything.
: "${FEAR_REF_ROOT:?set FEAR_REF_ROOT to the folder that holds the reference data}"
export FEAR_REF_PS3="$FEAR_REF_ROOT/ps3"
export FEAR_REF_X360="$FEAR_REF_ROOT/x360"
export FEAR_REF_PCGAME="$FEAR_REF_ROOT/pc"
export FEAR_REF_PC_TOC="$FEAR_REF_ROOT/pc_toc/Arch00_TOC.txt"
export FEAR_REF_PCWORLDS="$FEAR_REF_ROOT/pcworlds"
export FEAR_REF_PS3_EXTRACT="$FEAR_REF_ROOT/ps3_extract/ubercatalog.cat"
export FEAR_REF_LIVEMOD="$FEAR_REF_ROOT/confirmed_mod"
# These two are read as given when set (a wrong path fails instead of skipping), so only set them when present.
if [ -d "$FEAR_REF_ROOT/gog" ]; then export FEAR_REF_GOG="$FEAR_REF_ROOT/gog"; fi
if [ -f "$FEAR_REF_ROOT/arialbd.ttf" ]; then export FEAR_REF_ARIALBD="$FEAR_REF_ROOT/arialbd.ttf"; fi
# Offline builds: the vendored sources in third_party/ are all that is needed.
export GOTOOLCHAIN=local GOPROXY=off
