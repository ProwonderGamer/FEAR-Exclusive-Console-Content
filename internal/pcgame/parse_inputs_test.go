package pcgame

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Both libraryfolders.vdf formats, plus odd encodings, are understood.
func TestParseLibraryFoldersFormats(t *testing.T) {
	cases := []struct {
		name, vdf string
		want      []string
	}{
		{"new", "\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\"C:\\\\Program Files (x86)\\\\Steam\"\n\t\t\"apps\"\n\t\t{\n\t\t\t\"21090\"\t\t\"123\"\n\t\t}\n\t}\n\t\"1\"\n\t{\n\t\t\"path\"\t\t\"D:\\\\Spiele\\\\Stëam Bibliothek\"\n\t}\n}\n",
			[]string{`C:\Program Files (x86)\Steam`, `D:\Spiele\Stëam Bibliothek`}},
		{"pre-2021", "\"LibraryFolders\"\n{\n\t\"TimeNextStatsReport\"\t\t\"1470000000\"\n\t\"ContentStatsID\"\t\t\"-123\"\n\t\"1\"\t\t\"D:\\\\SteamLibrary\"\n\t\"2\"\t\t\"G:\\\\Library\\\\Steam\"\n}\n",
			[]string{`D:\SteamLibrary`, `G:\Library\Steam`}},
		{"unc", "\"libraryfolders\"{\"0\"{\"path\" \"\\\\\\\\NAS\\\\share\\\\steam\"}}", []string{`\\NAS\share\steam`}},
		{"bom", "\xef\xbb\xbf\"libraryfolders\"\n{\n\"0\"\n{\n\"path\"\t\"F:\\\\Steam\"\n}\n}", []string{`F:\Steam`}},
		{"comment", "// c\n\"libraryfolders\"\n{\n\"0\"\n{\n\"path\"\t\"G:\\\\Steam\" // trailing\n}\n}", []string{`G:\Steam`}},
		{"numbers are not paths", "\"libraryfolders\"\n{\n\"1\"\t\"1\"\n\"2\"\t\"123456\"\n\"3\"\t\"D:\\\\Lib\"\n}", []string{`D:\Lib`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "libraryfolders.vdf")
			os.WriteFile(p, []byte(c.vdf), 0o644)
			if got := ParseLibraryFolders(p); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSteamManifestCannotEscapeCommon(t *testing.T) {
	tmp := t.TempDir()
	steam := filepath.Join(tmp, "Steam")
	os.MkdirAll(filepath.Join(steam, "steamapps", "common"), 0o755)
	// A game folder reachable only through the traversal.
	os.MkdirAll(filepath.Join(tmp, "elsewhere"), 0o755)
	os.WriteFile(filepath.Join(tmp, "elsewhere", "FEAR.exe"), []byte("MZ"), 0o644)
	os.WriteFile(filepath.Join(steam, "steamapps", "appmanifest_21090.acf"),
		[]byte("\"AppState\"\n{\n\t\"installdir\"\t\t\"..\\\\..\\\\..\\\\elsewhere\"\n}\n"), 0o644)
	if got := FindSteamInstall([]string{steam}); got != "" {
		t.Fatalf("manifest escaped the common folder: %q", got)
	}
}

// What people type into the folder box.
func TestInspectTypedPaths(t *testing.T) {
	tmp := t.TempDir()
	game := filepath.Join(tmp, "My Games", "FEAR")
	os.MkdirAll(game, 0o755)
	os.WriteFile(filepath.Join(game, "FEAR.exe"), []byte("MZ"), 0o644)
	os.WriteFile(filepath.Join(game, "dinput8.dll"), []byte("EchoPatch"), 0o644)
	os.WriteFile(filepath.Join(game, "FEAR_8.Arch00"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(tmp, "notes.txt"), []byte("x"), 0o644)
	t.Setenv("FEARTEST_ROOT", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	type want struct {
		valid bool
		path  string // expected Path, "" = unchanged input
		msg   string // substring of Message
	}
	cases := map[string]want{
		`"` + game + `"`:                     {true, game, "found"},
		`  '` + game + `'  `:                 {true, game, "found"},
		"\u201c" + game + "\u201d":           {true, game, "found"},
		game + string(filepath.Separator):    {true, game, "found"},
		filepath.Join(game, "FEAR.exe"):      {true, game, "found"},
		filepath.Join(tmp, "notes.txt"):      {false, "", "That is a file, not a folder"},
		`%FEARTEST_ROOT%` + "/My Games/FEAR": {true, game, "found"},
		"~/My Games/FEAR":                    {true, game, "found"},
		"%THIS_IS_NOT_SET_ANYWHERE%/x":       {false, "", "is not defined"},
		"D:":                                 {false, "", "full path"},
		"relative/path":                      {false, "", "full path"},
		"":                                   {false, "", "Select the folder"},
	}
	for in, w := range cases {
		ck := Inspect(in)
		if ck.Valid != w.valid || !strings.Contains(ck.Message, w.msg) {
			t.Errorf("%q: valid=%v message=%q", in, ck.Valid, ck.Message)
		}
		if w.path != "" && ck.Path != w.path {
			t.Errorf("%q: path %q, want %q", in, ck.Path, w.path)
		}
	}
}
