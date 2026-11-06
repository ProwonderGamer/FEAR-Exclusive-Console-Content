package pcgame_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/pcgame"
)

// mkLTAR writes a minimal LTAR v3 archive (everything stored, one root directory).
func mkLTAR(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	var names []string
	for n := range files {
		names = append(names, n)
	}
	// sorted for a stable layout
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	str := []byte{0} // offset 0 = the empty directory name
	ofs := map[string]uint32{}
	for _, n := range names {
		ofs[n] = uint32(len(str))
		str = append(str, n...)
		str = append(str, 0)
	}
	hdr := 0x30 + len(str) + len(names)*32 + 16
	var body bytes.Buffer
	var ents bytes.Buffer
	for _, n := range names {
		off := uint64(hdr + body.Len())
		body.Write(files[n])
		le := binary.LittleEndian
		var e [32]byte
		le.PutUint32(e[0:], ofs[n])
		le.PutUint64(e[4:], off)
		le.PutUint64(e[12:], uint64(len(files[n])))
		le.PutUint64(e[20:], uint64(len(files[n])))
		ents.Write(e[:])
	}
	var h [0x30]byte
	copy(h[:], "LTAR")
	binary.LittleEndian.PutUint32(h[4:], 3)
	binary.LittleEndian.PutUint32(h[8:], uint32(len(str)))
	binary.LittleEndian.PutUint32(h[12:], 1)
	binary.LittleEndian.PutUint32(h[16:], uint32(len(names)))
	var d [16]byte
	binary.LittleEndian.PutUint32(d[12:], uint32(len(names)))
	var out bytes.Buffer
	out.Write(h[:])
	out.Write(str)
	out.Write(ents.Bytes())
	out.Write(d[:])
	out.Write(body.Bytes())
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gogFolder lays out what the GOG depot manifest lists for the base game (names only; content is made up).
func gogFolder(t *testing.T, root string) string {
	t.Helper()
	files := map[string]string{
		"FEAR.exe": "MZ", "FEAR.Arch00": "x", "FEAR_8.Arch00": "x", "FEARL_8.Arch00": "x", "Default.archcfg": "FEAR.Arch00",
		"dxwrapper.dll": "MZ dxwrapper", "dxwrapper.ini": "[Compatibility]\nD3d8to9 = 1\n[d3d9]\nLimitPerFrameFPS = 60\n", "d3d9.dll": "MZ stub",
		"dinput8.dll": "MZ stub that loads dxwrapper.dll", "FEARXP/FEARXP.exe": "MZ", "FEARXP/Default.archcfg": "FEAR_XP.Arch00",
		"FEARXP2/FEARXP2.exe": "MZ", "movies/almalive.bik": "x",
	}
	for n, c := range files {
		write(t, filepath.Join(root, filepath.FromSlash(n)), []byte(c))
	}
	return root
}

func TestGOGFolderBeforeEchoPatch(t *testing.T) {
	dir := gogFolder(t, filepath.Join(t.TempDir(), "GOG Games", "F.E.A.R. Platinum"))
	ck := pcgame.Inspect(dir)
	if !ck.Valid || ck.Edition != "gog" || ck.EditionLabel != "GOG" || !ck.Wrapper {
		t.Fatalf("%+v", ck)
	}
	if ck.EchoPatch != pcgame.EchoPatchWrapper || ck.Ready {
		t.Fatalf("GOG's dinput8.dll must not count as EchoPatch: %+v", ck)
	}
	if !strings.Contains(ck.EchoMessage, "GOG.com Input wrapper") || !strings.Contains(ck.EchoHelp, "Replace") || !strings.Contains(ck.EchoHelp, "Verify") {
		t.Fatalf("message/help: %q / %q", ck.EchoMessage, ck.EchoHelp)
	}
	if !strings.Contains(ck.Message, "GOG version") {
		t.Fatalf("message: %q", ck.Message)
	}
}

func TestGOGFolderWithEchoPatch(t *testing.T) {
	dir := gogFolder(t, filepath.Join(t.TempDir(), "F.E.A.R. Platinum Collection"))
	write(t, filepath.Join(dir, "dinput8.dll"), []byte("MZ ... EchoPatch ..."))
	write(t, filepath.Join(dir, "EchoPatch.ini"), []byte("[Fixes]"))
	ck := pcgame.Inspect(dir)
	if ck.EchoPatch != pcgame.EchoPatchOK || !ck.Ready || !strings.Contains(ck.EchoMessage, "EchoPatch by Wemino (GitHub)") {
		t.Fatalf("%+v", ck)
	}
}

func TestDxwrapperFilesAreNotEchoPatch(t *testing.T) {
	// dxwrapper.dll / .ini / d3d9.dll are never read as EchoPatch, even if they mention it
	dir := gogFolder(t, filepath.Join(t.TempDir(), "g"))
	os.Remove(filepath.Join(dir, "dinput8.dll"))
	write(t, filepath.Join(dir, "dxwrapper.ini"), []byte("; EchoPatch EchoPatch"))
	write(t, filepath.Join(dir, "d3d9.dll"), []byte("EchoPatch"))
	write(t, filepath.Join(dir, "dxwrapper.dll"), []byte("EchoPatch"))
	if ck := pcgame.Inspect(dir); ck.EchoPatch != pcgame.EchoPatchMissing || ck.Ready {
		t.Fatalf("%+v", ck)
	}
	// a dinput8.dll whose strings do not name dxwrapper is still the wrapper's when dxwrapper.dll is beside it
	write(t, filepath.Join(dir, "dinput8.dll"), []byte("MZ no strings"))
	if ck := pcgame.Inspect(dir); ck.EchoPatch != pcgame.EchoPatchWrapper {
		t.Fatalf("%+v", ck)
	}
	// and a foreign dinput8.dll on a folder without dxwrapper stays "foreign"
	steam := game(t, filepath.Join(t.TempDir(), "x"), map[string]string{"FEAR.exe": "MZ", "FEAR.Arch00": "x", "dinput8.dll": "ASI loader"})
	if ck := pcgame.Inspect(steam); ck.EchoPatch != pcgame.EchoPatchForeign {
		t.Fatalf("%+v", ck)
	}
	// UTF-16 mention of dxwrapper is recognised too
	u := "d\x00x\x00w\x00r\x00a\x00p\x00p\x00e\x00r\x00.\x00d\x00l\x00l\x00"
	steam2 := game(t, filepath.Join(t.TempDir(), "y"), map[string]string{"FEAR.exe": "MZ", "FEAR.Arch00": "x", "dinput8.dll": u})
	if ck := pcgame.Inspect(steam2); ck.EchoPatch != pcgame.EchoPatchWrapper {
		t.Fatalf("%+v", ck)
	}
}

func TestFolderValidationByContent(t *testing.T) {
	tmp := t.TempDir()
	// FEAR.exe without any game archive: not a complete installation
	a := game(t, filepath.Join(tmp, "a"), map[string]string{"FEAR.exe": "MZ", "FEARL.Arch00": "x"})
	if ck := pcgame.Inspect(a); ck.Valid || !strings.Contains(ck.Message, "data archives") {
		t.Fatalf("%+v", ck)
	}
	// archives without FEAR.exe
	b := game(t, filepath.Join(tmp, "b"), map[string]string{"FEAR.Arch00": "x"})
	if ck := pcgame.Inspect(b); ck.Valid || !strings.Contains(ck.Message, "FEAR.exe was not found") {
		t.Fatalf("%+v", ck)
	}
	// an expansion folder inside the GOG folder points one level up
	g := gogFolder(t, filepath.Join(tmp, "gog"))
	ck := pcgame.Inspect(filepath.Join(g, "FEARXP"))
	if ck.Valid || !strings.Contains(ck.Message, "expansion folder") || !strings.Contains(ck.Message, g) {
		t.Fatalf("%+v", ck)
	}
	if ck := pcgame.Inspect(filepath.Join(g, "FEARXP2")); ck.Valid || !strings.Contains(ck.Message, "expansion") {
		t.Fatalf("%+v", ck)
	}
	// parent of a game folder with any name
	p := filepath.Join(tmp, "parent")
	gogFolder(t, filepath.Join(p, "F.E.A.R. Platinum Collection"))
	if ck := pcgame.Inspect(p); ck.Valid || !strings.Contains(ck.Message, `"F.E.A.R. Platinum Collection"`) {
		t.Fatalf("%+v", ck)
	}
	// store does not matter: a Steam-named folder with GOG content is accepted
	if ck := pcgame.Inspect(gogFolder(t, filepath.Join(tmp, "steamapps", "common", "FEAR Ultimate Shooter Edition"))); !ck.Valid {
		t.Fatalf("%+v", ck)
	}
}

func TestEditionIsAHintFromContent(t *testing.T) {
	tmp := t.TempDir()
	cases := []struct {
		name  string
		dir   string
		files map[string]string
		want  string
	}{
		{"galaxy info file", "x1", map[string]string{"goggame-1423058413.info": "{}"}, "gog"},
		{"wrapper plus expansion folders", "x2", map[string]string{"dxwrapper.dll": "MZ", "FEARXP/FEARXP.exe": "MZ"}, "gog"},
		{"steamapps in the path", "steamapps/common/FEAR", nil, "steam"},
		{"steam marker file", "x3", map[string]string{"21090_install.vdf": "x"}, "steam"},
		{"nothing", "x4", nil, "unknown"},
		{"conflict", "steamapps/common/x5", map[string]string{"goggame-1.info": "{}"}, "unknown"},
		{"gog games folder name", "GOG Games/F.E.A.R.", nil, "gog"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(tmp, filepath.FromSlash(c.dir))
			base := map[string]string{"FEAR.exe": "MZ", "FEAR.Arch00": "x"}
			for k, v := range c.files {
				base[k] = v
			}
			game(t, dir, base)
			if ck := pcgame.Inspect(dir); ck.Edition != c.want || !ck.Valid {
				t.Fatalf("edition %q valid=%v", ck.Edition, ck.Valid)
			}
		})
	}
}

func TestVersionCheck(t *testing.T) {
	ref := os.Getenv("FEAR_REF_PCGAME")
	mk := func(name string, gc []byte, others ...string) string {
		dir := game(t, filepath.Join(t.TempDir(), name), map[string]string{"FEAR.exe": "MZ", "FEAR.Arch00": "x", "FEAR_8.Arch00": "x"})
		if gc != nil {
			mkLTAR(t, filepath.Join(dir, "FEARE_8.Arch00"), map[string][]byte{"GameClient.dll": gc, "ClientFx.fxd": []byte("fx")})
		}
		return dir
	}
	// a different GameClient.dll: warning, still valid, install allowed
	ck := pcgame.Inspect(mk("diff", []byte("some other build")))
	if !ck.Valid || ck.Version.Status != pcgame.VersionDifferent || ck.Version.SHA256 == "" || ck.Version.Archive != "FEARE_8.Arch00" {
		t.Fatalf("%+v", ck.Version)
	}
	if len(ck.Warnings) != 1 || !strings.Contains(ck.Warnings[0], "has not been tested") || !strings.Contains(ck.Warnings[0], "may switch themselves off") {
		t.Fatalf("warnings %q", ck.Warnings)
	}
	// unreadable archive: same warning
	bad := mk("bad", nil)
	write(t, filepath.Join(bad, "FEARE_8.Arch00"), []byte("not an archive"))
	if ck := pcgame.Inspect(bad); ck.Version.Status != pcgame.VersionUnreadable || len(ck.Warnings) != 1 {
		t.Fatalf("%+v %q", ck.Version, ck.Warnings)
	}
	// archive without a GameClient.dll anywhere
	mkLTAR(t, filepath.Join(bad, "FEARE_8.Arch00"), map[string][]byte{"other.txt": []byte("x")})
	if ck := pcgame.Inspect(bad); ck.Version.Status != pcgame.VersionUnreadable {
		t.Fatalf("%+v", ck.Version)
	}
	// no FEARE_8.Arch00: nothing to compare, no warning
	if ck := pcgame.Inspect(mk("none", nil)); ck.Version.Status != pcgame.VersionUnchecked || len(ck.Warnings) != 0 || !ck.Valid {
		t.Fatalf("%+v %q", ck.Version, ck.Warnings)
	}
	// a copy in an older FEARE_n archive is used when FEARE_8 has none
	old := mk("older", nil)
	mkLTAR(t, filepath.Join(old, "FEARE_8.Arch00"), map[string][]byte{"x.txt": []byte("x")})
	mkLTAR(t, filepath.Join(old, "FEARE_7.Arch00"), map[string][]byte{"GAMECLIENT.DLL": []byte("seven")})
	if ck := pcgame.Inspect(old); ck.Version.Status != pcgame.VersionDifferent || ck.Version.Archive != "FEARE_7.Arch00" {
		t.Fatalf("%+v", ck.Version)
	}
	// the real Steam patch 1.08 archive is the reference
	if ref != "" {
		if _, err := os.Stat(filepath.Join(ref, "FEARE_8.Arch00")); err == nil {
			ck := pcgame.Inspect(ref)
			if ck.Version.Status != pcgame.VersionVerified || ck.Version.SHA256 != pcgame.GameClientV108SHA256 || ck.Version.Size != pcgame.GameClientV108Size || len(ck.Warnings) != 0 {
				t.Fatalf("real folder: %+v %q", ck.Version, ck.Warnings)
			}
			if !strings.Contains(ck.Message, "patch 1.08 confirmed") {
				t.Fatalf("message %q", ck.Message)
			}
		}
	}
}

func TestHookState(t *testing.T) {
	dir := game(t, filepath.Join(t.TempDir(), "h"), map[string]string{"FEAR.exe": "MZ", "FEAR.Arch00": "x"})
	if ck := pcgame.Inspect(dir); ck.Hook != "absent" || len(ck.Warnings) != 0 {
		t.Fatalf("%+v", ck)
	}
	write(t, filepath.Join(dir, "dinput8_hook.dll"), []byte("MZ ... zSFODD_Bonus_SFOD-D ..."))
	if ck := pcgame.Inspect(dir); ck.Hook != "ours" || len(ck.Warnings) != 0 {
		t.Fatalf("%+v", ck)
	}
	// Another mod's hook under a differently cased name. On a case-insensitive file system (Windows) that name is
	// the same file as ours, so it replaces it; only where the two names are different files is ours removed.
	ours, _ := os.Stat(filepath.Join(dir, "dinput8_hook.dll"))
	write(t, filepath.Join(dir, "DINPUT8_HOOK.DLL"), []byte("MZ somebody else's chain"))
	if theirs, err := os.Stat(filepath.Join(dir, "DINPUT8_HOOK.DLL")); err != nil || !os.SameFile(ours, theirs) {
		os.Remove(filepath.Join(dir, "dinput8_hook.dll"))
	}
	ck := pcgame.Inspect(dir)
	if ck.Hook != "foreign" || len(ck.Warnings) != 1 || !strings.Contains(ck.Warnings[0], "already in the folder") {
		t.Fatalf("%+v", ck)
	}
}

func TestFindGOGInstalls(t *testing.T) {
	tmp := t.TempDir()
	root := gogFolder(t, filepath.Join(tmp, "Games", "F.E.A.R. Platinum Collection"))
	other := gogFolder(t, filepath.Join(tmp, "Galaxy", "Games", "F.E.A.R. Platinum"))
	notGame := game(t, filepath.Join(tmp, "Galaxy", "Games", "Some Other Game"), map[string]string{"FEAR.exe": "MZ"})
	fear2 := game(t, filepath.Join(tmp, "Galaxy", "Games", "F.E.A.R. 2"), map[string]string{"FEAR2.exe": "MZ"})
	_, _ = notGame, fear2

	games := []pcgame.GOGGame{
		{ID: "1423058413", Name: "F.E.A.R. Platinum", Path: root, Exe: filepath.Join(root, "FEAR.exe")},
		// expansion launchers registered as their own keys: ignored
		{ID: "1", Name: "F.E.A.R. Extraction Point", Path: filepath.Join(root, "FEARXP"), Exe: "FEARXP.exe", DependsOn: "1423058413"},
		{ID: "2", Name: "F.E.A.R. Perseus Mandate", Path: filepath.Join(root, "FEARXP2"), Exe: filepath.Join(root, "FEARXP2", "FEARXP2.exe")},
		// some other product
		{ID: "3", Name: "Witcher", Path: filepath.Join(tmp, "w"), Exe: "w.exe"},
		// a path that points at the library, not at the game
		{ID: "4", Name: "FEAR Platinum", Path: filepath.Join(tmp, "Galaxy", "Games")},
	}
	got := pcgame.FindGOGInstalls(games, []string{filepath.Join(tmp, "Galaxy", "Games"), filepath.Join(tmp, "missing")})
	var paths []string
	for _, g := range got {
		if g.Store != "gog" {
			t.Fatalf("store %q", g.Store)
		}
		paths = append(paths, g.Path)
	}
	// root and "other" once each; expansions, F.E.A.R. 2 and the unrelated folder never
	if len(got) != 2 || paths[0] != root || paths[1] != other {
		t.Fatalf("%q", paths)
	}
	// a registry path that is an expansion folder resolves to the main folder
	got = pcgame.FindGOGInstalls([]pcgame.GOGGame{{Name: "F.E.A.R. Platinum", Path: filepath.Join(root, "FEARXP")}}, nil)
	if len(got) != 1 || got[0].Path != root {
		t.Fatalf("%v", got)
	}
	// nothing found
	if got := pcgame.FindGOGInstalls(nil, nil); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestDetectAllListsBothStores(t *testing.T) {
	tmp := t.TempDir()
	steam := filepath.Join(tmp, "Steam")
	game(t, filepath.Join(steam, "steamapps", "common", pcgame.SteamFolder), map[string]string{"FEAR.exe": "MZ", "FEAR.Arch00": "x"})
	gog := gogFolder(t, filepath.Join(tmp, "GOG Games", "F.E.A.R. Platinum"))
	all := pcgame.DetectAll(pcgame.Sources{SteamRoots: []string{steam},
		GOGGames: []pcgame.GOGGame{{Name: "F.E.A.R. Platinum", Path: gog}}, GOGRoots: []string{filepath.Join(tmp, "GOG Games")}})
	if len(all) != 2 || all[0].Store != "steam" || all[1].Store != "gog" || all[1].Path != gog {
		t.Fatalf("%+v", all)
	}
	// only one store: one entry; the same folder found twice is listed once
	one := pcgame.DetectAll(pcgame.Sources{GOGGames: []pcgame.GOGGame{{Name: "F.E.A.R. Platinum", Path: gog}}, GOGRoots: []string{filepath.Join(tmp, "GOG Games")}})
	if len(one) != 1 {
		t.Fatalf("%+v", one)
	}
	// a Steam folder that is also reachable as a GOG root is not listed twice
	dup := pcgame.DetectAll(pcgame.Sources{SteamRoots: []string{steam}, GOGRoots: []string{filepath.Join(steam, "steamapps", "common")}})
	if len(dup) != 1 || dup[0].Store != "steam" {
		t.Fatalf("%+v", dup)
	}
	if len(pcgame.DetectAll(pcgame.Sources{})) != 0 {
		t.Fatal("expected nothing")
	}
}

func TestDefaultGOGRoots(t *testing.T) {
	r := pcgame.DefaultGOGRoots([]string{`/pf86`, `/pf86`, ``}, []string{`/c`}, []string{`/lib`, `/c/GOG Games`})
	want := map[string]bool{"/pf86/GOG Galaxy/Games": true, "/pf86/GOG Games": true, "/c/GOG Games": true, "/c/GOG Galaxy/Games": true, "/lib": true}
	seen := map[string]int{}
	for _, x := range r {
		seen[filepath.ToSlash(x)]++
	}
	for k := range want {
		if seen[k] != 1 {
			t.Errorf("%s listed %d times in %q", k, seen[k], r)
		}
	}
}
