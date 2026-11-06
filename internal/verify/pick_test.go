package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for finding a console copy from whatever folder the player selects (pick.go, the searches in
// ps3.go / x360.go and OpenSource). Trees are tiny placeholders in t.TempDir(), so verification stops at
// sizes or hashes; the tests check which folder was found (Result.Root) and the kind of message.
// Folder names are deliberately odd so nothing depends on them.

// pickWrite creates the files (slash paths under base) with small distinct contents.
func pickWrite(t *testing.T, base string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("placeholder "+rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// pickMkdir creates an empty folder.
func pickMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

// samePath compares two folders the way Windows does (case-insensitive, after Clean).
func samePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

// pickX360Tree makes an Xbox 360 copy three odd folders below a temp folder and returns
// the three wrapper folders (outermost first) and the copy root.
func pickX360Tree(t *testing.T) (wraps []string, root string) {
	t.Helper()
	w1 := filepath.Join(t.TempDir(), "A & b (1)")
	w2 := filepath.Join(w1, "Ünïcode 2.0")
	w3 := filepath.Join(w2, "x.y z")
	root = filepath.Join(w3, "F.E.A.R. 360 (dump) & más")
	pickWrite(t, root,
		"default.xex",
		"Game/uberCatalog.cat",
		"Game/database/Loki.gamedb",
		"Game/Worlds/Release/Holiday.wld",
	)
	return []string{w1, w2, w3}, root
}

// pickPS3Tree makes a PS3 disc folder two odd folders below a temp folder and returns
// the two wrapper folders (outermost first) and the disc folder.
func pickPS3Tree(t *testing.T) (wraps []string, disc string) {
	t.Helper()
	w1 := filepath.Join(t.TempDir(), "Ünïcode & Söny (games)")
	w2 := filepath.Join(w1, "v1.0 copies")
	disc = filepath.Join(w2, "F.E.A.R. (PS3) (USA) & co")
	pickWrite(t, disc,
		"PS3_GAME/PARAM.SFO",
		"PS3_GAME/USRDIR/game/database/loki.gamedb",
		"PS3_GAME/USRDIR/game/minicatalog.cat",
		"PS3_GAME/USRDIR/game/worlds/release/sfod_d.wld",
	)
	return []string{w1, w2}, disc
}

func pickPS3Game(disc string) string { return filepath.Join(disc, "PS3_GAME", "USRDIR", "game") }

// checkFound fails unless r found a copy at want (anything but "not recognized" / "wrong disc").
func checkFound(t *testing.T, label string, r Result, want string) {
	t.Helper()
	if r.Status == StatusUnrecognized || r.Status == StatusWrongDisc {
		t.Errorf("%s: status %s (%s)", label, r.Status, r.Message)
	}
	if !samePath(r.Root, want) {
		t.Errorf("%s: Root = %q, want %q", label, r.Root, want)
	}
}

// Xbox 360: the root, folders inside it on the Game path, and folders 1-3 levels above it all find the root.
func TestPickX360FromAnyLevel(t *testing.T) {
	wraps, root := pickX360Tree(t)
	picks := map[string]string{
		"root":                root,
		"Game":                filepath.Join(root, "Game"),
		"Game/Worlds/Release": filepath.Join(root, "Game", "Worlds", "Release"),
		"1 above":             wraps[2],
		"2 above":             wraps[1],
		"3 above":             wraps[0],
	}
	for label, p := range picks {
		r := VerifyX360(context.Background(), p, nil)
		checkFound(t, label, r, root)
	}
	// The selection is matched without regard to case too.
	r := VerifyX360(context.Background(), filepath.Join(root, "gAmE", "worlds"), nil)
	checkFound(t, "gAmE/worlds", r, root)
}

// Xbox 360: a Game folder without default.xex next to it is reported as incomplete, and an empty folder
// is not recognized with a hint naming default.xex.
func TestPickX360MissingXexAndEmpty(t *testing.T) {
	copyDir := filepath.Join(t.TempDir(), "Half copy (no xex) & Ünï")
	pickWrite(t, copyDir,
		"Game/uberCatalog.cat",
		"Game/database/Loki.gamedb",
		"Game/Worlds/Release/Holiday.wld",
	)
	r := VerifyX360(context.Background(), filepath.Join(copyDir, "Game"), nil)
	if r.Status != StatusIncomplete || !strings.Contains(r.Message, "default.xex is missing") {
		t.Errorf("Game without default.xex: status %s, message %q", r.Status, r.Message)
	}
	if r.Root != "" {
		t.Errorf("Game without default.xex: Root = %q, want empty", r.Root)
	}

	empty := filepath.Join(t.TempDir(), "Empty. folder (&)")
	pickMkdir(t, empty)
	r = VerifyX360(context.Background(), empty, nil)
	if r.Status != StatusUnrecognized || !strings.Contains(r.Hint, "default.xex") {
		t.Errorf("empty folder: status %s, hint %q", r.Status, r.Hint)
	}
	if r.Root != "" {
		t.Errorf("empty folder: Root = %q, want empty", r.Root)
	}
}

// A folder inside the Xbox 360 root that is not on the Game path does not climb to the root.
func TestClimbX360SiblingStays(t *testing.T) {
	_, root := pickX360Tree(t)
	sib := filepath.Join(root, "Extras & stuff")
	deep := filepath.Join(sib, "Game") // named like the data folder, but under the wrong parent
	pickMkdir(t, deep)
	for _, p := range []string{sib, deep} {
		if got := climb(p, x360RootAt); got != p {
			t.Errorf("climb(%q) = %q, want it unchanged", p, got)
		}
	}
	// The root itself and its Game folder do climb (control).
	if got := climb(root, x360RootAt); got != root {
		t.Errorf("climb(root) = %q", got)
	}
	if got := climb(filepath.Join(root, "Game"), x360RootAt); !samePath(got, root) {
		t.Errorf("climb(Game) = %q, want %q", got, root)
	}
}

// PS3: every level inside the disc and the two folders above it find the game folder.
func TestPickPS3FromAnyLevel(t *testing.T) {
	wraps, disc := pickPS3Tree(t)
	game := pickPS3Game(disc)
	picks := map[string]string{
		"disc":                disc,
		"PS3_GAME":            filepath.Join(disc, "PS3_GAME"),
		"USRDIR":              filepath.Join(disc, "PS3_GAME", "USRDIR"),
		"game":                game,
		"game/worlds/release": filepath.Join(game, "worlds", "release"),
		"parent":              wraps[1],
		"grandparent":         wraps[0],
	}
	for label, p := range picks {
		r := VerifyPS3(context.Background(), p, nil)
		checkFound(t, label, r, game)
	}
}

// Both copies side by side: each verifier finds its own copy and never the other one.
func TestPickBothCopiesSideBySide(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "Console copies (all) & Ünï")
	// The Xbox copy sorts first and sits higher, so a breadth-first PS3 search meets its Game folder first.
	x360 := filepath.Join(parent, "A X360 copy")
	disc := filepath.Join(parent, "Sony stuff", "PS3 (USA) v.1")
	pickWrite(t, x360,
		"default.xex",
		"Game/uberCatalog.cat",
		"Game/database/Loki.gamedb",
		"Game/Worlds/Release/Holiday.wld",
	)
	pickWrite(t, disc,
		"PS3_GAME/PARAM.SFO",
		"PS3_GAME/USRDIR/game/database/loki.gamedb",
		"PS3_GAME/USRDIR/game/minicatalog.cat",
		"PS3_GAME/USRDIR/game/worlds/release/sfod_d.wld",
	)
	ctx := context.Background()
	checkFound(t, "PS3 on parent", VerifyPS3(ctx, parent, nil), pickPS3Game(disc))
	checkFound(t, "X360 on parent", VerifyX360(ctx, parent, nil), x360)

	if r := VerifyPS3(ctx, filepath.Join(x360, "Game"), nil); r.Status != StatusWrongDisc || !strings.Contains(r.Message, "Xbox 360") {
		t.Errorf("PS3 on the Xbox Game folder: status %s, message %q", r.Status, r.Message)
	}
	if r := VerifyPS3(ctx, x360, nil); r.Status != StatusWrongDisc {
		t.Errorf("PS3 on the Xbox root: status %s, message %q", r.Status, r.Message)
	}
	if r := VerifyX360(ctx, disc, nil); r.Status != StatusWrongDisc || !strings.Contains(r.Message, "PlayStation 3") {
		t.Errorf("X360 on the PS3 disc folder: status %s, message %q", r.Status, r.Message)
	}
}

func TestOnChain(t *testing.T) {
	cases := []struct {
		rel, chain []string
		want       bool
	}{
		{[]string{"Game"}, []string{"Game"}, true},
		{[]string{"gAME"}, []string{"Game"}, true},                                              // case-insensitive
		{[]string{"Game", "Worlds", "Release"}, []string{"Game"}, true},                         // deeper inside the data
		{[]string{"USRDIR", "game", "worlds"}, []string{"USRDIR", "game"}, true},                // PS3_GAME chain, deeper
		{[]string{"usrdir"}, []string{"USRDIR", "game"}, true},                                  // part of the chain
		{[]string{"Extras & stuff"}, []string{"Game"}, false},                                   // sibling
		{[]string{"Extras & stuff", "Game"}, []string{"Game"}, false},                           // wrong first part
		{[]string{"USRDIR", "Ünïcode"}, []string{"USRDIR", "game"}, false},                      // wrong second part
		{[]string{"PS3_GAME", "USRDIR", "gamе"}, []string{"PS3_GAME", "USRDIR", "game"}, false}, // Cyrillic e
		{nil, []string{"Game"}, true},
	}
	for _, c := range cases {
		if got := onChain(c.rel, c.chain); got != c.want {
			t.Errorf("onChain(%q, %q) = %v, want %v", c.rel, c.chain, got, c.want)
		}
	}
}

// climb reaches the top of the drive without a panic and leaves the selection unchanged.
func TestClimbStopsAtDriveTop(t *testing.T) {
	never := func(string) ([]string, bool) { return nil, false }
	top := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	if got := climb(top, never); got != top {
		t.Errorf("climb(drive top) = %q", got)
	}
	// A selection one folder below the top: the walk reaches the top and stops there.
	dir := t.TempDir()
	near := dir
	for filepath.Dir(filepath.Dir(near)) != filepath.Dir(near) {
		near = filepath.Dir(near)
	}
	var seen []string
	record := func(d string) ([]string, bool) { seen = append(seen, d); return nil, false }
	if got := climb(near, record); got != near {
		t.Errorf("climb(%q) = %q", near, got)
	}
	if len(seen) != 2 || !samePath(seen[1], top) {
		t.Errorf("climb(%q) looked at %q, want the selection and the drive top", near, seen)
	}
	// A deep selection looks at no more than maxClimb folders above it.
	seen = nil
	if got := climb(dir, record); got != dir {
		t.Errorf("climb(%q) = %q", dir, got)
	}
	if len(seen) > maxClimb+1 {
		t.Errorf("isRoot called %d times, want at most %d", len(seen), maxClimb+1)
	}
	// A root at the drive top whose chain does not hold the selection is not taken.
	wrong := func(d string) ([]string, bool) {
		if samePath(d, top) {
			return []string{"no such folder"}, true
		}
		return nil, false
	}
	if got := climb(dir, wrong); got != dir {
		t.Errorf("climb with a mismatched top root = %q", got)
	}
	// A file or a missing path comes back unchanged.
	f := filepath.Join(dir, "disc image.iso")
	pickWrite(t, dir, "disc image.iso")
	if got := climb(f, never); got != f {
		t.Errorf("climb(file) = %q", got)
	}
	missing := filepath.Join(dir, "gone (missing)")
	if got := climb(missing, never); got != missing {
		t.Errorf("climb(missing) = %q", got)
	}
}

// OpenSource from a folder inside the copy reads the same files as from the root.
func TestRootOpenSourceFromInside(t *testing.T) {
	_, disc := pickPS3Tree(t)
	_, x360 := pickX360Tree(t)
	cases := []struct {
		kind, root, inside, rel string
	}{
		{"ps3", disc, filepath.Join(disc, "PS3_GAME", "USRDIR"), "database/loki.gamedb"},
		{"x360", x360, filepath.Join(x360, "Game"), "Game/database/Loki.gamedb"},
	}
	for _, c := range cases {
		want, err := OpenSource(c.kind, c.root)
		if err != nil {
			t.Fatalf("%s root: %v", c.kind, err)
		}
		defer want.Close()
		got, err := OpenSource(c.kind, c.inside)
		if err != nil {
			t.Fatalf("%s inside: %v", c.kind, err)
		}
		defer got.Close()
		we, err := want.Stat(c.rel)
		if err != nil {
			t.Fatalf("%s root Stat(%s): %v", c.kind, c.rel, err)
		}
		ge, err := got.Stat(c.rel)
		if err != nil {
			t.Fatalf("%s inside Stat(%s): %v", c.kind, c.rel, err)
		}
		if we != ge || ge.IsDir || ge.Size == 0 {
			t.Errorf("%s: root %+v, inside %+v", c.kind, we, ge)
		}
	}
}

// TestPickPS3FolderOnXboxRow: any PS3 folder picked on the Xbox 360 row is named as the PS3 copy, never as an
// Xbox 360 Game folder that lacks default.xex (review finding).
func TestPickPS3FolderOnXboxRow(t *testing.T) {
	_, disc := pickPS3Tree(t)
	game := pickPS3Game(disc)
	for _, p := range []string{disc, filepath.Join(disc, "PS3_GAME"), filepath.Dir(game), game} {
		r := VerifyX360(context.Background(), p, nil)
		if r.Status != StatusWrongDisc || !strings.Contains(r.Message, "PlayStation 3") {
			t.Errorf("%s: %s (%s)", p, r.Status, r.Message)
		}
	}
}

// TestPickPS3SkipsIncompleteXboxCopy: an Xbox 360 copy without its Holiday world, sorted before the PS3 copy,
// is still recognised by its default.xex and never taken for the PS3 data (review finding).
func TestPickPS3SkipsIncompleteXboxCopy(t *testing.T) {
	top := filepath.Join(t.TempDir(), "Copies & more")
	pickWrite(t, filepath.Join(top, "A Xbox copy"), "default.xex", "Game/uberCatalog.cat", "Game/database/Loki.gamedb")
	disc := filepath.Join(top, "B PS3 copy")
	pickWrite(t, disc, "PS3_GAME/PARAM.SFO", "PS3_GAME/USRDIR/game/database/loki.gamedb", "PS3_GAME/USRDIR/game/minicatalog.cat")
	r := VerifyPS3(context.Background(), top, nil)
	if !samePath(r.Root, pickPS3Game(disc)) {
		t.Fatalf("root %q (%s: %s)", r.Root, r.Status, r.Message)
	}
}
