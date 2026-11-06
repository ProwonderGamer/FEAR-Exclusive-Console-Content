package pcgame_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/pcgame"
)

func write(t *testing.T, p string, b []byte) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// game builds a fake game folder.
func game(t *testing.T, dir string, files map[string]string) string {
	for n, c := range files {
		write(t, filepath.Join(dir, n), []byte(c))
	}
	return dir
}

func TestInspectEchoPatchStates(t *testing.T) {
	tmp := t.TempDir()
	cases := []struct {
		name  string
		files map[string]string
		valid bool
		echo  pcgame.EchoPatchState
		ready bool
	}{
		{"ok ascii", map[string]string{"FEAR.exe": "MZ", "FEAR_8.Arch00": "x", "dinput8.dll": "xx EchoPatch yy", "EchoPatch.ini": "[Fixes]"}, true, pcgame.EchoPatchOK, true},
		{"ok utf16 and odd case", map[string]string{"fear.EXE": "MZ", "Fear.ARCH00": "x", "DInput8.DLL": "E\x00c\x00h\x00o\x00P\x00a\x00t\x00c\x00h\x00"}, true, pcgame.EchoPatchOK, true},
		{"foreign dinput8", map[string]string{"FEAR.exe": "MZ", "FEAR_8.Arch00": "x", "dinput8.dll": "some other wrapper"}, true, pcgame.EchoPatchForeign, false},
		{"ini only", map[string]string{"FEAR.exe": "MZ", "FEAR_8.Arch00": "x", "EchoPatch.ini": "x"}, true, pcgame.EchoPatchIniOnly, false},
		{"nothing", map[string]string{"FEAR.exe": "MZ", "FEAR.Arch00": "x"}, true, pcgame.EchoPatchMissing, false},
		{"no exe", map[string]string{"dinput8.dll": "EchoPatch"}, false, pcgame.EchoPatchMissing, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ck := pcgame.Inspect(game(t, filepath.Join(tmp, strings.ReplaceAll(c.name, " ", "_")), c.files))
			t.Logf("%+v", ck)
			if ck.Valid != c.valid || ck.EchoPatch != c.echo || ck.Ready != c.ready {
				t.Fatalf("got valid=%v echo=%s ready=%v", ck.Valid, ck.EchoPatch, ck.Ready)
			}
			if c.valid && !c.ready && (!strings.Contains(ck.EchoHelp, "Verify") || ck.EchoMessage == "") {
				t.Fatalf("missing guidance: %q / %q", ck.EchoMessage, ck.EchoHelp)
			}
			if c.valid && c.ready && ck.EchoHelp != "" {
				t.Fatalf("no help text expected when EchoPatch is fine: %q", ck.EchoHelp)
			}
			// c3: the message must say what is actually there, not just "not found".
			switch c.echo {
			case pcgame.EchoPatchForeign:
				if !strings.Contains(ck.EchoMessage, "not EchoPatch") || strings.Contains(ck.EchoMessage, "not installed") {
					t.Fatalf("foreign dinput8 message: %q", ck.EchoMessage)
				}
			case pcgame.EchoPatchIniOnly:
				if !strings.Contains(ck.EchoMessage, "EchoPatch.ini is here") || !strings.Contains(ck.EchoMessage, "dinput8.dll is missing") {
					t.Fatalf("ini-only message: %q", ck.EchoMessage)
				}
			case pcgame.EchoPatchMissing:
				if c.valid && !strings.Contains(ck.EchoMessage, "not installed") {
					t.Fatalf("missing message: %q", ck.EchoMessage)
				}
			}
			if c.ready && !strings.Contains(ck.EchoMessage, "EchoPatch by Wemino (GitHub)") {
				t.Fatalf("c2: project name missing: %q", ck.EchoMessage)
			}
		})
	}
	if ck := pcgame.Inspect(filepath.Join(tmp, "does-not-exist")); ck.Valid || ck.Message == "" {
		t.Fatal("nonexistent folder must be invalid with a message")
	}
}

func TestInspectHintsAtSubfolder(t *testing.T) {
	tmp := t.TempDir()
	game(t, filepath.Join(tmp, pcgame.SteamFolder), map[string]string{"FEAR.exe": "MZ"})
	ck := pcgame.Inspect(tmp)
	if ck.Valid || !strings.Contains(ck.Message, pcgame.SteamFolder) {
		t.Fatalf("expected a hint about the subfolder: %+v", ck)
	}
}

func TestSteamDetection(t *testing.T) {
	tmp := t.TempDir()
	steam := filepath.Join(tmp, "Steam")
	lib2 := filepath.Join(tmp, "Games", "SteamLibrary")
	// F.E.A.R. lives in the second library, under a manifest-named folder.
	game(t, filepath.Join(lib2, "steamapps", "common", "FEAR Custom Dir"), map[string]string{"FEAR.exe": "MZ"})
	write(t, filepath.Join(lib2, "steamapps", "appmanifest_21090.acf"), []byte("\"AppState\"\n{\n\t\"appid\"\t\t\"21090\"\n\t\"installdir\"\t\t\"FEAR Custom Dir\"\n}\n"))
	vdf := "\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\"" + strings.ReplaceAll(steam, `\`, `\\`) + "\"\n\t}\n" +
		"\t\"1\"\n\t{\n\t\t\"path\"\t\t\"" + strings.ReplaceAll(lib2, `\`, `\\`) + "\"\n\t\t\"apps\"\n\t\t{\n\t\t\t\"21090\"\t\t\"123\"\n\t\t}\n\t}\n}\n"
	write(t, filepath.Join(steam, "steamapps", "libraryfolders.vdf"), []byte(vdf))

	libs := pcgame.ParseLibraryFolders(filepath.Join(steam, "steamapps", "libraryfolders.vdf"))
	if len(libs) != 2 || libs[1] != lib2 {
		t.Fatalf("libraries: %v", libs)
	}
	got := pcgame.FindSteamInstall([]string{"", steam})
	if got != filepath.Join(lib2, "steamapps", "common", "FEAR Custom Dir") {
		t.Fatalf("found %q", got)
	}
	// Default folder name works without a manifest; absent game gives "".
	if pcgame.FindSteamInstall([]string{filepath.Join(tmp, "nothing")}) != "" {
		t.Fatal("should not find anything")
	}
	game(t, filepath.Join(steam, "steamapps", "common", pcgame.SteamFolder), map[string]string{"FEAR.exe": "MZ"})
	os.RemoveAll(filepath.Join(lib2, "steamapps", "common"))
	if got := pcgame.FindSteamInstall([]string{steam}); !strings.HasSuffix(got, pcgame.SteamFolder) {
		t.Fatalf("default folder: %q", got)
	}
}

// The user's real game folder (read-only), from FEAR_REF_PCGAME.
func TestRealGameFolder(t *testing.T) {
	dir := os.Getenv("FEAR_REF_PCGAME")
	if dir == "" {
		t.Skip("set FEAR_REF_PCGAME to a real F.E.A.R. folder (with EchoPatch) to run this test")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skip("reference game folder not available: ", err)
	}
	ck := pcgame.Inspect(dir)
	t.Logf("%+v", ck)
	if !ck.Valid || !ck.Patch108 || ck.EchoPatch != pcgame.EchoPatchOK {
		t.Fatalf("real folder not recognised: %+v", ck)
	}
}
