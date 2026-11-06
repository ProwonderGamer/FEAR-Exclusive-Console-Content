package iaworld

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/testref"
)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// sectionSHA: first 16 hex digits of the SHA-256 of each World00p part (core after the 20-byte header, render, client sector
// data, objects, blind data) of the recipe's output (the Python map builder run with
// the stock archives and the complete mod's file list mounted). Only Vault's object section differs without PS3.
var sectionSHA = map[string][5]string{
	"Distribution": {"7c30568441d598ee", "7ee954822954ece7", "936da3add6631b4e", "37d975d7037b499e", "80146c4ff7e48429"},
	"Rooftop":      {"85a14a6faad2bddd", "db0e765bd537b6b5", "4bcc18c31548a378", "892dc5431d6e9307", "d5f0504cc2092118"},
	"Construction": {"c606335dfc3ce488", "16a832ff4a4f8e48", "ac62c5bdc6ded8ee", "67fec089fd87a50b", "1eb309da3b507867"},
	"Vault":        {"3571f4ab3e7ba895", "d2ee03ddb3d397d8", "7af70c9b212fa34a", "9fc9f2909d10554d", "e442ca6481c9899f"},
	"Vault/xbox":   {"3571f4ab3e7ba895", "d2ee03ddb3d397d8", "7af70c9b212fa34a", "d9bf2fb939cef85b", "e442ca6481c9899f"},
}

// modUsed: the mod file names the X360 worlds resolve (recorded from the instrumented recipe run). Without
// PS3 the Watson pickups are not placed, so WatsonSAS.Model00p is not named; player_watson still is.
var modUsed = []string{
	`chars\models\player_dualSM15.Model00p`, `chars\models\player_SM15.Model00p`, `chars\models\player_watson.Model00p`,
	`guns\Materials\SM15_Machine_Pistol.Mat00`, `guns\models_hh\SM15_Left.Model00p`, `guns\models_hh\SM15_Right.Model00p`,
	`guns\models_hh\SM15machinePistol.Model00p`, `guns\models_hh\WatsonSAS.Model00p`, `guns\skins_pv\SM15_Machine_Pistol_D.dds`,
	`guns\skins_pv\SM15_Machine_Pistol_N.dds`, `guns\skins_pv\SM15_Machine_Pistol_S.dds`, `Interface\hud\weapon\dualsm15.dds`,
	`Interface\hud\weapon\sm15.dds`, `Interface\hud\weapon\sm15X.dds`, `Materials\Engineering\black.dds`,
}

var (
	refOnce sync.Once
	refFS   *pcfs.FS
	refErr  string
)

// refInputs loads the inputs from the reference copies:
//
//	FEAR_REF_X360    Xbox 360 disc root (Game/Worlds/Release/InstantAction1..4.wld/.wldclnt/.wldsrvr)
//	FEAR_REF_PS3     PS3 USRDIR/game folder (worlds/release/instantaction4.wldsrvr); "" for the Xbox-only test
//	FEAR_REF_PCGAME  a stock PC game folder with ALL 36 *.Arch00 archives
func refInputs(t *testing.T, withPS3 bool) *Inputs {
	t.Helper()
	x360 := testref.Dir(t, "FEAR_REF_X360")
	if _, err := findCI(x360, "Game", "Worlds", "Release", "InstantAction4.wldsrvr"); err != nil {
		t.Skipf("FEAR_REF_X360 has no Instant Action worlds: %v", err)
	}
	ps3 := ""
	if withPS3 {
		ps3 = testref.Dir(t, "FEAR_REF_PS3")
		if _, err := findCI(ps3, "worlds", "release", "instantaction4.wldsrvr"); err != nil {
			t.Skipf("FEAR_REF_PS3 has no instantaction4.wldsrvr: %v", err)
		}
	}
	game := testref.Dir(t, "FEAR_REF_PCGAME")
	refOnce.Do(func() {
		fs, errs := pcfs.Open(game)
		switch {
		case len(errs) > 0:
			refErr = errs[0].Error()
		case !fs.Has(`Worlds\Release\WTF_Exfil.World00p`):
			refErr = "no WTF_Exfil.World00p (FEAR.Arch00 missing?)"
		default:
			refFS = fs
		}
	})
	if refFS == nil {
		t.Skipf("FEAR_REF_PCGAME=%s: %s", game, refErr)
	}
	in, err := LoadInputs(x360, ps3, refFS)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func checkSections(t *testing.T, key string, w []byte) {
	h := make([]int, 0, 6)
	h = append(h, 20)
	for i := 1; i < 5; i++ {
		h = append(h, int(binary.LittleEndian.Uint32(w[4*i:])))
	}
	h = append(h, len(w))
	for k := 0; k < 5; k++ {
		if got := sha(w[h[k]:h[k+1]])[:16]; got != sectionSHA[key][k] {
			t.Errorf("%s section %d: %s, want %s", key, k, got, sectionSHA[key][k])
		}
	}
}

func runGolden(t *testing.T, withPS3 bool) {
	in := refInputs(t, withPS3)
	want := map[string]string{}
	for k, v := range LiveSHA256 {
		want[k] = v
	}
	if !withPS3 {
		for k, v := range XboxOnlySHA256 {
			want[k] = v
		}
	}
	out, used, err := build(in, func(name string, b []byte) {
		t.Logf("%-25s %s %d bytes", name, sha(b), len(b))
		if d := os.Getenv("IAWORLD_DUMP"); d != "" { // debugging aid: write the outputs to a scratch folder
			_ = os.WriteFile(filepath.Join(d, strings.ReplaceAll(name, "/", "_")), b, 0o644)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 8 {
		t.Fatalf("%d files", len(out))
	}
	for _, l := range Labels {
		key := l
		if l == "Vault" && !withPS3 {
			key = "Vault/xbox"
		}
		checkSections(t, key, out[WorldPath(l)])
	}
	paths := make([]string, 0, len(want))
	for p := range want {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if got := sha(out[p]); got != want[p] {
			t.Errorf("%s: sha256 %s, want %s", p, got, want[p])
		}
	}
	wantUsed := []string{}
	for _, u := range modUsed {
		if withPS3 || !strings.HasSuffix(u, "WatsonSAS.Model00p") {
			wantUsed = append(wantUsed, u)
		}
	}
	sort.Strings(wantUsed)
	if strings.Join(used, "|") != strings.Join(wantUsed, "|") {
		t.Errorf("mod names used:\n %v\nwant\n %v", used, wantUsed)
	}
}

// TestGoldenLive builds the eight files with a PS3 copy present and compares them with the in-game
// confirmed files (and every World00p section with the recipe's).
func TestGoldenLive(t *testing.T) { runGolden(t, true) }

// TestGoldenXboxOnly builds without a PS3 copy: Vault keeps the X360 pickups (checked against the
// Python recipe's output), the other six files equal the confirmed ones.
func TestGoldenXboxOnly(t *testing.T) { runGolden(t, false) }

// TestModDependency: dropping a mod name the worlds use either fails the build (an object's file reference
// no longer resolves) or changes a world's precache list; dropping one they do not use changes nothing.
func TestModDependency(t *testing.T) {
	in := refInputs(t, false)
	base, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	drop := func(name string) []string {
		var l []string
		for _, f := range ModFiles() {
			if !strings.EqualFold(f, name) {
				l = append(l, f)
			}
		}
		return l
	}
	in.ModFiles = drop(`guns\Snd\SM15\fire.wav`) // in the mod, not named by any IA world
	out, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	for p := range base {
		if sha(out[p]) != sha(base[p]) {
			t.Errorf("%s changed without an unused mod file", p)
		}
	}
	in.ModFiles = drop(`chars\models\player_watson.Model00p`) // PS3-only file named by the X360 worlds
	out, err = Build(in)
	if err != nil {
		t.Logf("without player_watson the build fails: %v", err)
		return
	}
	changed := 0
	for p := range base {
		if sha(out[p]) != sha(base[p]) {
			changed++
			t.Logf("without player_watson: %s %s", p, sha(out[p]))
		}
	}
	if changed == 0 {
		t.Error("dropping player_watson.Model00p changed nothing")
	}
}

func TestModFiles(t *testing.T) {
	m := ModFiles()
	if len(m) != 185 {
		t.Fatalf("%d mod files, want 185", len(m))
	}
	have := map[string]bool{}
	for _, p := range m {
		have[p] = true
	}
	for _, u := range modUsed {
		if !have[u] {
			t.Errorf("%s is not in modfiles.txt with that spelling", u)
		}
	}
}

func TestSubExitLevel(t *testing.T) {
	for _, c := range []struct {
		in, out string
		n       int
	}{
		{"delay 2 (msg player exitlevel);", "delay 2 (msg player (MISSIONFAILED 77110));", 1},
		{"MSG\tPlayer  ExitLevel;msg player exitlevel", "msg player (MISSIONFAILED 77110);msg player (MISSIONFAILED 77110)", 2},
		{"xmsg player exitlevel;msg player exitlevels;msg player\xa0exitlevel\xe9", "xmsg player exitlevel;msg player exitlevels;msg player\xa0exitlevel\xe9", 0},
		{"_msg player exitlevel", "_msg player exitlevel", 0},
	} {
		if g, n := subExitLevel(c.in); g != c.out || n != c.n {
			t.Errorf("%q -> %q %d, want %q %d", c.in, g, n, c.out, c.n)
		}
	}
}

func TestQmulNoFMA(t *testing.T) {
	// identity and a 90-degree yaw: exact in float64
	s := 0.7071067811865476
	r := qmul([4]float64{0, s, 0, s}, [4]float64{0, 0, 0, 1})
	if r != [4]float64{0, s, 0, s} {
		t.Fatal(r)
	}
}
