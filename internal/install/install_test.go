package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"fearinstaller/internal/archcfg"
)

const mod = "FEAR_ConsoleRestoration"

// stockCfg is the Steam Default.archcfg shape: CRLF, no final line break, a glued third-party line.
const stockCfg = "FEAR.Arch00\r\nFEARA.Arch00\r\nFEARL.Arch00\r\nFEARE.Arch00\r\nFEAR_8.Arch00\r\nFEARE_8.Arch00ButtonMod"

var ourHook = []byte("MZ our hook zSFODD_Bonus_Menu")

// fakeGame makes a game folder with archives, Default.archcfg, EchoPatch, and optionally a hook and an ini.
func fakeGame(t *testing.T, hook, ini string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "FEAR Ultimate Shooter Edition")
	must(t, os.MkdirAll(filepath.Join(dir, "ButtonMod"), 0o755))
	files := map[string]string{
		"FEAR.exe": "MZ game", "FEAR.Arch00": "a", "FEARA.Arch00": "a", "FEARL.Arch00": "a", "FEARE.Arch00": "a",
		"FEAR_8.Arch00": "a", "FEARE_8.Arch00": "a", "Default.archcfg": stockCfg, "dinput8.dll": "MZ EchoPatch",
		"EchoPatch.ini": "[x]", "ButtonMod/Buttons.dds": "dds",
	}
	if hook != "" {
		files["dinput8_hook.dll"] = hook
	}
	if ini != "" {
		files["dinput8_hook.ini"] = ini
	}
	for n, c := range files {
		must(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(n)), []byte(c), 0o644))
	}
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// snapshot maps every file (to its SHA-256) and folder ("dir") below root.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	must(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			m[filepath.ToSlash(rel)+"/"] = "dir"
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		m[filepath.ToSlash(rel)] = hex.EncodeToString(h[:])
		return nil
	}))
	return m
}

func sameTree(t *testing.T, what string, want, got map[string]string) {
	t.Helper()
	if reflect.DeepEqual(want, got) {
		return
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s was %q, now %q", what, k, v, got[k])
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: %s is new", what, k)
		}
	}
	t.FailNow()
}

func content(files map[string]string) func(context.Context, string) error {
	return func(ctx context.Context, dir string) error {
		for n, c := range files {
			p := filepath.Join(dir, filepath.FromSlash(n))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
}

var modV1 = map[string]string{
	"Database/FEAR.gamdb00p":                  "db v1",
	"Worlds/Release/SFOD_D.World00p":          "world v1",
	"StringDatabase/ConsoleText.Strdb00p":     "text v1",
	"chars/models/player_Deltaforce.Model00p": "model",
}

func req(game string, files map[string]string) Request {
	return Request{GameDir: game, Mod: mod, Build: content(files), Hook: ourHook, Installer: "test 1",
		Items: []string{"sfod"}, GameRunning: func() bool { return false }}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	must(t, err)
	return string(b)
}

// A fresh install over a foreign hook and an old ini, then an uninstall that restores the folder exactly.
func TestInstallUninstallRoundTrip(t *testing.T) {
	game := fakeGame(t, "MZ somebody else's hook", "[X360HUD]\r\nEnabled=0\r\n")
	before := snapshot(t, game)
	r := req(game, modV1)
	r.RemoveIni = true
	rec, err := Install(context.Background(), r)
	must(t, err)

	if len(rec.Files) != 4 || rec.Hook.Found != "foreign" || !rec.Hook.BackupMade || !rec.Archcfg.BackupMade || !rec.Ini.Moved {
		t.Fatalf("record %+v", rec)
	}
	for n, c := range modV1 {
		if got := read(t, filepath.Join(game, mod, filepath.FromSlash(n))); got != c {
			t.Fatalf("%s = %q", n, got)
		}
	}
	if read(t, filepath.Join(game, HookName)) != string(ourHook) || read(t, filepath.Join(game, HookBackup)) != "MZ somebody else's hook" {
		t.Fatal("hook not replaced / backed up")
	}
	if exists(filepath.Join(game, IniName)) || !exists(filepath.Join(game, IniBackup)) {
		t.Fatal("ini not set aside")
	}
	cfg := read(t, filepath.Join(game, "Default.archcfg"))
	if !strings.HasSuffix(cfg, "ButtonMod\r\n"+mod+"\r\n") || read(t, filepath.Join(game, archcfg.BackupName)) != stockCfg {
		t.Fatalf("archcfg %q", cfg)
	}
	for _, p := range []string{mod + stageSuffix, mod + oldSuffix} {
		if exists(filepath.Join(game, p)) {
			t.Fatalf("%s left behind", p)
		}
	}
	got, err := ReadRecord(game, mod)
	must(t, err)
	if got == nil || got.Hook.SHA256 != sum(ourHook) {
		t.Fatalf("record on disk %+v", got)
	}

	res, err := Uninstall(game, mod, nil)
	must(t, err)
	if res.Removed != 4 || len(res.Notes) != 0 {
		t.Fatalf("uninstall %+v", res)
	}
	sameTree(t, "after uninstall", before, snapshot(t, game))
}

// Without an existing hook or ini the uninstall removes the hook and restores the archcfg.
func TestInstallClean(t *testing.T) {
	game := fakeGame(t, "", "")
	before := snapshot(t, game)
	rec, err := Install(context.Background(), req(game, modV1))
	must(t, err)
	if rec.Hook.Found != "absent" || rec.Hook.BackupMade || rec.Ini.Moved {
		t.Fatalf("%+v", rec)
	}
	_, err = Uninstall(game, mod, nil)
	must(t, err)
	sameTree(t, "after uninstall", before, snapshot(t, game))
}

// Every failure point leaves the game folder exactly as it was.
func TestInstallRollsBack(t *testing.T) {
	cases := map[string]func(game string, r *Request){
		"build fails": func(game string, r *Request) {
			r.Build = func(ctx context.Context, dir string) error {
				content(modV1)(ctx, dir)
				return errors.New("converter broke")
			}
		},
		"build writes nothing": func(game string, r *Request) { r.Build = content(nil) },
		"archcfg unusable": func(game string, r *Request) {
			must(t, os.WriteFile(filepath.Join(game, "Default.archcfg"), []byte{0, 1, 2, 3, 0, 0, 9}, 0o644))
		},
		"ini backup in the way": func(game string, r *Request) {
			must(t, os.WriteFile(filepath.Join(game, IniBackup), []byte("older"), 0o644))
		},
		"record name in content": func(game string, r *Request) {
			r.Build = content(map[string]string{RecordFile: "x"})
		},
		// Another mod's hook is never overwritten without its own backup.
		"hook backup in the way": func(game string, r *Request) {
			must(t, os.WriteFile(filepath.Join(game, HookBackup), []byte("MZ an even older hook"), 0o644))
		},
		// An unexpected failure is undone like any other.
		"build panics": func(game string, r *Request) {
			r.Build = func(ctx context.Context, dir string) error {
				content(modV1)(ctx, dir)
				panic("converter bug")
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			game := fakeGame(t, "MZ somebody else's hook", "[X360HUD]\r\nEnabled=0\r\n")
			r := req(game, modV1)
			r.RemoveIni = true
			setup(game, &r)
			before := snapshot(t, game)
			rec, err := Install(context.Background(), r)
			var ie *Error
			if err == nil || rec != nil || !errors.As(err, &ie) {
				t.Fatalf("want *Error, got %v / %+v", err, rec)
			}
			sameTree(t, "after "+name, before, snapshot(t, game))
		})
	}
}

// Cancel during the build: ctx.Err() comes back and nothing changed.
func TestInstallCancel(t *testing.T) {
	game := fakeGame(t, "", "")
	before := snapshot(t, game)
	ctx, cancel := context.WithCancel(context.Background())
	r := req(game, modV1)
	r.Build = func(c context.Context, dir string) error {
		content(modV1)(c, dir)
		cancel()
		return c.Err()
	}
	if _, err := Install(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	sameTree(t, "after cancel", before, snapshot(t, game))
}

// Refusals touch nothing: game running, a mod folder of the same name that is not ours, foreign leftovers.
func TestInstallRefuses(t *testing.T) {
	cases := map[string]func(game string, r *Request){
		"game running": func(game string, r *Request) { r.GameRunning = func() bool { return true } },
		"foreign mod folder": func(game string, r *Request) {
			must(t, os.MkdirAll(filepath.Join(game, mod, "Worlds"), 0o755))
			must(t, os.WriteFile(filepath.Join(game, mod, "Worlds", "x.World00p"), []byte("dev build"), 0o644))
		},
		"foreign staging name": func(game string, r *Request) {
			must(t, os.MkdirAll(filepath.Join(game, mod+stageSuffix), 0o755))
			must(t, os.WriteFile(filepath.Join(game, mod+stageSuffix, "keep.txt"), []byte("mine"), 0o644))
		},
		"bad mod name": func(game string, r *Request) { r.Mod = `..\x` },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			game := fakeGame(t, "", "")
			r := req(game, modV1)
			setup(game, &r)
			before := snapshot(t, game)
			built := false
			b := r.Build
			r.Build = func(c context.Context, d string) error { built = true; return b(c, d) }
			if _, err := Install(context.Background(), r); err == nil {
				t.Fatal("installed")
			}
			if built {
				t.Fatal("the build ran")
			}
			sameTree(t, "after refusal", before, snapshot(t, game))
		})
	}
}

// An update replaces the files (removed ones go), keeps what the first install found, and the uninstall
// still restores the original folder.
func TestUpdate(t *testing.T) {
	game := fakeGame(t, "MZ somebody else's hook", "")
	before := snapshot(t, game)
	_, err := Install(context.Background(), req(game, modV1))
	must(t, err)
	v2 := map[string]string{"Database/FEAR.gamdb00p": "db v2", "Interface/IA/IA_Load1.dds": "new"}
	r2 := req(game, v2)
	r2.Hook = []byte("MZ our hook zSFODD_Bonus_Menu, newer")
	rec, err := Install(context.Background(), r2)
	must(t, err)
	if rec.Hook.Found != "foreign" || !rec.Hook.BackupMade || !rec.Archcfg.BackupMade || len(rec.Files) != 2 {
		t.Fatalf("%+v", rec)
	}
	if exists(filepath.Join(game, mod, "Worlds")) || read(t, filepath.Join(game, mod, "Database", "FEAR.gamdb00p")) != "db v2" {
		t.Fatal("old content kept")
	}
	if read(t, filepath.Join(game, HookBackup)) != "MZ somebody else's hook" {
		t.Fatal("the backup must still hold the foreign hook")
	}
	if strings.Count(read(t, filepath.Join(game, "Default.archcfg")), mod) != 1 {
		t.Fatal("mod listed twice")
	}
	_, err = Uninstall(game, mod, nil)
	must(t, err)
	sameTree(t, "after update + uninstall", before, snapshot(t, game))
}

// A failed update puts the previous install back as it was.
func TestUpdateRollsBack(t *testing.T) {
	game := fakeGame(t, "", "")
	_, err := Install(context.Background(), req(game, modV1))
	must(t, err)
	before := snapshot(t, game)
	r2 := req(game, map[string]string{"a.dds": "x"})
	r2.Hook = []byte("MZ our hook zSFODD_Bonus_Menu, newer")
	must(t, os.WriteFile(filepath.Join(game, "Default.archcfg"), []byte{0, 1, 2, 0, 0, 0}, 0o644))
	before["Default.archcfg"] = snapshot(t, game)["Default.archcfg"]
	if _, err := Install(context.Background(), r2); err == nil {
		t.Fatal("installed over a broken archcfg")
	}
	sameTree(t, "after failed update", before, snapshot(t, game))
}

// Changes made by others after the install are left alone and named.
func TestUninstallKeepsOthersChanges(t *testing.T) {
	game := fakeGame(t, "", "")
	_, err := Install(context.Background(), req(game, modV1))
	must(t, err)
	cfg := filepath.Join(game, "Default.archcfg")
	must(t, os.WriteFile(cfg, []byte(read(t, cfg)+"OtherMod\r\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(game, HookName), []byte("MZ another mod's hook"), 0o644))
	must(t, os.WriteFile(filepath.Join(game, mod, "Worlds", "notes.txt"), []byte("mine"), 0o644))
	res, err := Uninstall(game, mod, nil)
	must(t, err)
	if len(res.Notes) != 3 {
		t.Fatalf("notes %q", res.Notes)
	}
	if got := read(t, cfg); strings.Contains(got, mod) || !strings.Contains(got, "ButtonMod\r\nOtherMod\r\n") {
		t.Fatalf("archcfg %q", got)
	}
	if read(t, filepath.Join(game, HookName)) != "MZ another mod's hook" {
		t.Fatal("another mod's hook was touched")
	}
	if read(t, filepath.Join(game, mod, "Worlds", "notes.txt")) != "mine" || exists(filepath.Join(game, mod, "Database")) {
		t.Fatal("mod folder not cleaned to the foreign file")
	}
}

// A read-only Default.archcfg is updated and restored, and keeps its read-only attribute.
func TestReadOnlyArchcfgRoundTrip(t *testing.T) {
	game := fakeGame(t, "", "")
	cfg := filepath.Join(game, "Default.archcfg")
	must(t, os.Chmod(cfg, 0o444))
	defer os.Chmod(cfg, 0o644) // so the test folder can be removed
	before := snapshot(t, game)
	_, err := Install(context.Background(), req(game, modV1))
	must(t, err)
	_, err = Uninstall(game, mod, nil)
	must(t, err)
	sameTree(t, "after uninstall", before, snapshot(t, game))
	if st, err := os.Stat(cfg); err != nil || st.Mode().Perm()&0o200 != 0 {
		t.Fatalf("read-only attribute not kept: %v %v", st, err)
	}
}

// A mod file that cannot be removed stops the uninstall before anything else changes; the record stays, so
// the folder is still seen as installed, and a second uninstall finishes the job.
func TestUninstallStopsOnStuckFile(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("an open file blocks its removal only on Windows")
	}
	game := fakeGame(t, "MZ somebody else's hook", "")
	before := snapshot(t, game)
	_, err := Install(context.Background(), req(game, modV1))
	must(t, err)
	f, err := os.Open(filepath.Join(game, mod, "Worlds", "Release", "SFOD_D.World00p"))
	must(t, err)
	_, err = Uninstall(game, mod, nil)
	f.Close()
	if err == nil {
		t.Fatal("uninstall reported success with a file it could not remove")
	}
	if rec, e := ReadRecord(game, mod); e != nil || rec == nil {
		t.Fatalf("the record is gone after a stopped uninstall: %v", e)
	}
	if read(t, filepath.Join(game, HookName)) != string(ourHook) || !strings.Contains(read(t, filepath.Join(game, "Default.archcfg")), mod) {
		t.Fatal("the hook or Default.archcfg changed although the uninstall stopped")
	}
	res, err := Uninstall(game, mod, nil)
	must(t, err)
	if len(res.Notes) != 0 {
		t.Fatalf("notes after the second uninstall: %q", res.Notes)
	}
	sameTree(t, "after the second uninstall", before, snapshot(t, game))
}

// Uninstall without a record removes nothing.
func TestUninstallNeedsRecord(t *testing.T) {
	game := fakeGame(t, string(ourHook), "")
	must(t, os.MkdirAll(filepath.Join(game, mod), 0o755))
	before := snapshot(t, game)
	if _, err := Uninstall(game, mod, nil); err == nil {
		t.Fatal("uninstalled without a record")
	}
	sameTree(t, "after refused uninstall", before, snapshot(t, game))
}

func TestRemoveEntry(t *testing.T) {
	in := "FEAR.Arch00\r\n; note\r\n.\\FEAR_ConsoleRestoration\\\r\nButtonMod\r\nfear_consolerestoration"
	out, changed, err := archcfg.RemoveEntry([]byte(in), mod)
	must(t, err)
	if !changed || string(out) != "FEAR.Arch00\r\n; note\r\nButtonMod" {
		t.Fatalf("%v %q", changed, out)
	}
	if _, changed, _ := archcfg.RemoveEntry([]byte("FEAR.Arch00\n"), mod); changed {
		t.Fatal("changed without the entry")
	}
}
