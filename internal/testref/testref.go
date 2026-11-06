// Package testref locates the original game files for golden tests. Nothing is hard coded: every
// location comes from an environment variable and a test skips with a clear message when it is not set.
//
//	FEAR_REF_PS3        the PS3 "PS3_GAME/USRDIR/game" folder (database/loki.gamedb, worlds/release/*, snd/*.cat)
//	FEAR_REF_X360       the Xbox 360 disc root (Game/database/Loki.gamedb, Game/uberCatalog.cat,
//	                    Game/Worlds/Release/*: Holiday, the Instant Action maps and the campaign worlds that
//	                    hold the SM15 placements)
//	FEAR_REF_PCGAME     a PC game folder containing the *.Arch00 archives (at least FEAR_8 and FEARL_8)
//	FEAR_REF_PC_TOC     tab separated listing of ALL PC archives (Arch00_TOC.txt format); replaces the archives
//	                    that are not available for the test (FEAR.Arch00 is 3.8 GB)
//	FEAR_REF_PCWORLDS   folder with the stock PC *.World00p files (extracted from FEAR.Arch00)
//	FEAR_REF_PS3_EXTRACT  folder holding files already extracted from the PS3 ubercatalog.cat
//	                    (database_string/English/Loki.strdb)
//	FEAR_REF_LIVEMOD    the confirmed mod folder (FEAR_ConsoleRestoration, read only): the files that cannot be
//	                    rebuilt byte for byte (decoded sounds, IA loading pictures) are compared with it within
//	                    a tolerance
//	FEAR_REF_ARIALBD    optional: the arialbd.ttf the loading-picture tests use (default %WINDIR%\Fonts)
package testref

import (
	"os"
	"path/filepath"
	"testing"
)

// Dir returns the folder named by the environment variable or skips the test.
func Dir(t testing.TB, env string) string {
	t.Helper()
	v := os.Getenv(env)
	if v == "" {
		t.Skipf("%s not set (original game files are needed for this golden test)", env)
	}
	st, err := os.Stat(v)
	if err != nil || !st.IsDir() {
		t.Skipf("%s=%q is not a folder", env, v)
	}
	return v
}

// File returns a file below the folder named by env, or skips the test when it does not exist.
func File(t testing.TB, env string, rel ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{Dir(t, env)}, rel...)...)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s: %v", p, err)
	}
	return p
}

// Path returns a plain file path named by the variable, or skips.
func Path(t testing.TB, env string) string {
	t.Helper()
	v := os.Getenv(env)
	if v == "" {
		t.Skipf("%s not set", env)
	}
	if _, err := os.Stat(v); err != nil {
		t.Skipf("%s: %v", v, err)
	}
	return v
}
