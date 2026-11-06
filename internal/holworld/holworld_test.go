package holworld

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"testing"

	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/testref"
)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// sectionSHA: first 16 hex digits of the SHA-256 of each part of the confirmed world (core after the 20-byte
// header, render, client sector data, objects, blind data), so a mismatch names the part that differs.
var sectionSHA = [5]string{"a6932859e4cb848d", "d0bbacd8f60a1708", "436cb6f57af1b561", "d867341952e21e4d", "49dd3b66dc4167e8"}

var sectionNames = [5]string{"core", "render", "sec1", "objects", "blind"}

func sections(b []byte) [5]string {
	var out [5]string
	if len(b) < 20 {
		return out
	}
	o := []int{20}
	for i := 1; i < 5; i++ {
		o = append(o, int(binary.LittleEndian.Uint32(b[4*i:])))
	}
	o = append(o, len(b))
	for i := range out {
		if o[i] <= o[i+1] && o[i+1] <= len(b) {
			out[i] = sha(b[o[i]:o[i+1]])[:16]
		}
	}
	return out
}

// refInputs loads the inputs from the reference copies:
//
//	FEAR_REF_X360    Xbox 360 disc root (Game/Worlds/Release/Holiday.wld/.wldclnt/.wldsrvr/.matCat)
//	FEAR_REF_PCGAME  a stock PC game folder with ALL *.Arch00 archives (FEAR.Arch00 holds the PC worlds)
func refInputs(t *testing.T) *Inputs {
	t.Helper()
	x360 := testref.Dir(t, "FEAR_REF_X360")
	for _, ext := range []string{"wld", "wldclnt", "wldsrvr", "matCat"} {
		if _, err := findCI(x360, "Game", "Worlds", "Release", "Holiday."+ext); err != nil {
			t.Skipf("FEAR_REF_X360 has no Holiday world: %v", err)
		}
	}
	game := testref.Dir(t, "FEAR_REF_PCGAME")
	fs, errs := pcfs.Open(game)
	if len(errs) > 0 {
		t.Skipf("FEAR_REF_PCGAME=%s: %v", game, errs[0])
	}
	t.Cleanup(fs.Close)
	for _, n := range PCWorldNames {
		if !fs.Has(`Worlds\Release\` + n + ".World00p") {
			t.Skipf("FEAR_REF_PCGAME has no %s.World00p (FEAR.Arch00 missing?)", n)
		}
	}
	if !fs.Has("GameServer.dll") {
		t.Skip("FEAR_REF_PCGAME has no GameServer.dll")
	}
	in, err := LoadInputs(x360, fs)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// TestGoldenHolidayWorld builds the world from the reference copies; it must equal the confirmed file.
func TestGoldenHolidayWorld(t *testing.T) {
	in := refInputs(t)
	info := &buildInfo{stage: func(name string, b []byte) { t.Logf("stage %-9s %s %d bytes", name, sha(b), len(b)) }}
	out, err := build(in, info)
	if err != nil {
		t.Fatal(err)
	}
	got := sections(out)
	for i := range got {
		if got[i] != sectionSHA[i] {
			t.Errorf("section %s: %s, want %s", sectionNames[i], got[i], sectionSHA[i])
		}
	}
	if h := sha(out); h != LiveSHA256 || len(out) != 24741664 {
		t.Fatalf("Build output %s (%d bytes), want the live world %s (24741664 bytes)", h, len(out), LiveSHA256)
	}

	// recipe facts: the shader-family table (hol_vtxinputs, X360 catalog only), the ClassDef classes,
	// all 11 patches applied
	var req []string
	for k, v := range info.extraRequire {
		var s []sem
		for x := range v {
			s = append(s, x)
		}
		sort.Slice(s, func(i, j int) bool { return s[i].use < s[j].use })
		req = append(req, k+" "+semString(s))
	}
	sort.Strings(req)
	want := []string{`materials\city\slums\black.mat00 (10,0)`, `materials\engineering\computermonitor.mat00 (6,0)(7,0)`}
	if len(req) != len(want) || req[0] != want[0] || req[1] != want[1] {
		t.Errorf("shader-family requirements %v, want %v", req, want)
	}
	sort.Strings(info.classDefAdded)
	t.Logf("ClassDef schema for %v; %d patches; %d AINode dependencies cleared", info.classDefAdded, info.patches, info.depCleared)
	if info.patches != 11 {
		t.Errorf("%d patches, want 11", info.patches)
	}

	// the normal-format guard: the recipe's run reports "agreement 0.849 over 30541 vertices"
	R := readRender(in.Clnt, 8, binary.BigEndian)
	dec, n := normalAgreement(R, dec3nToFloat3)
	ps3, _ := normalAgreement(R, cmpToFloat3)
	t.Logf("normal agreement: DEC3N %.3f, PS3 format %.3f over %d flat vertices", dec, ps3, n)
	if fmt.Sprintf("%.3f", dec) != "0.849" || n != 30541 || ps3 > 0.1 {
		t.Errorf("normal agreement DEC3N %.3f, PS3 %.3f (%d vertices)", dec, ps3, n)
	}
	t.Logf("GameServer.dll sha256 %s", sha(in.GameServer))

	out2, err := Build(in)
	if err != nil || !bytes.Equal(out, out2) {
		t.Fatalf("Build: %v (same bytes %v)", err, bytes.Equal(out, out2))
	}
}

func semString(s []sem) string {
	out := ""
	for _, x := range s {
		out += fmt.Sprintf("(%d,%d)", x.use, x.idx)
	}
	return out
}

// TestBadInputs: malformed input gives an error, not a panic.
func TestBadInputs(t *testing.T) {
	if _, err := Build(nil); err == nil {
		t.Fatal("nil inputs accepted")
	}
	in := &Inputs{Wld: []byte("x"), Clnt: []byte("x"), Srvr: []byte("x"), MatCat: []byte("x"),
		PCWorlds: map[string][]byte{}, GameServer: []byte("x")}
	for _, n := range PCWorldNames {
		in.PCWorlds[n] = make([]byte, 64)
	}
	in.Listing = nil
	if _, err := Build(in); err == nil {
		t.Fatal("missing listing accepted")
	}
	in.Listing = append(in.Listing, struct{ Arch, Path string }{"FEAR.Arch00", "x"})
	if _, err := Build(in); err == nil {
		t.Fatal("garbage world accepted")
	}
}

// TestEmbeddedRecipeData: the embedded JSON files hold what the build relies on (the object edits and the extra
// files the Holiday port adds).
func TestEmbeddedRecipeData(t *testing.T) {
	if p := loadPatches(); len(p) != 11 {
		t.Fatalf("%d patches, want 11", len(p))
	}
	ef := ExtraFiles()
	if len(ef) != 88 {
		t.Fatalf("%d extra files, want 88", len(ef))
	}
}

// TestNormalDecoders: DEC3N and the PS3 format on fixed words.
func TestNormalDecoders(t *testing.T) {
	// x = 511, y = -511 (0x201), z = 0, w = 3
	u := uint32(511) | uint32(0x201)<<10 | 3<<30
	if g := dec3nToFloat3(u); g != [3]float64{1, -1, 0} {
		t.Fatalf("dec3n %v", g)
	}
	if g := cmpToFloat3(1023); g != [3]float64{1, 0, 0} {
		t.Fatalf("cmp %v", g)
	}
}
