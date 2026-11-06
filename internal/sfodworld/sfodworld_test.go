package sfodworld

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/testref"
)

// Hashes of every stage of the recipe chain (a0b05c5e = converted world, f9d8667e = after the death lure
// edit, 242d19e8 = after the intro delay, 803cf806 = after the finale catch-up). "raw" is the converter output
// before the conversion edits, as the Python converter produces it with the same inputs.
var stageSHA = map[string]string{
	StageRaw:        "5205e97faa88abe2afafb26805d54c15dbf7605ee23b48b319035f5a7c99d223",
	StageConverted:  "a0b05c5e242169459eedfbc34c140a756c31c54fd9afb6bae74a1ba0a267cc5d",
	StageDeathLure:  "f9d8667e99c90b7605cae73ec9044b9286bc7baaf1ffe209286abe5662df0e3a",
	StageIntroDelay: "242d19e8d70b9a7313c9035ed7ab7cc1b0e19bc8a3a5a090195be57dbaff2012",
	StageFinale:     LiveSHA256,
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// refInputs loads the inputs from the reference copies:
//
//	FEAR_REF_PS3       PS3 USRDIR/game folder (worlds/release/sfod_d.*)
//	FEAR_REF_PC_TOC    Arch00_TOC.txt listing of every stock PC archive
//	FEAR_REF_PCWORLDS  folder holding the stock Aftermath/Moody/Admin/ATC_Roof/Mapes_Elevator .World00p
func refInputs(t *testing.T) *Inputs {
	ps3 := testref.Dir(t, "FEAR_REF_PS3")
	tocPath := testref.Path(t, "FEAR_REF_PC_TOC")
	pcw := testref.Dir(t, "FEAR_REF_PCWORLDS")
	in := &Inputs{PCWorlds: map[string][]byte{}}
	var err error
	for _, f := range []struct {
		dst *[]byte
		ext string
	}{{&in.PS3Wld, "wld"}, {&in.PS3Clnt, "wldclnt"}, {&in.PS3Srvr, "wldsrvr"}} {
		p, err := findCI(ps3, "worlds", "release", "sfod_d."+f.ext)
		if err != nil {
			t.Skipf("PS3 world: %v", err)
		}
		if *f.dst, err = os.ReadFile(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range PCWorldNames {
		p, err := findCI(pcw, n+".World00p")
		if err != nil {
			t.Skipf("PC world: %v", err)
		}
		if in.PCWorlds[n], err = os.ReadFile(p); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(tocPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if in.Listing, err = ReadTOC(f); err != nil {
		t.Fatal(err)
	}
	return in
}

// TestGoldenSFODWorld builds the world from the reference copies and compares every stage with the recipe
// chain's hashes; the final world must equal the in-game confirmed file.
func TestGoldenSFODWorld(t *testing.T) {
	in := refInputs(t)
	got := map[string]string{}
	out, err := build(in, func(name string, b []byte) {
		got[name] = sha(b)
		t.Logf("stage %-11s %s %d bytes", name, got[name], len(b))
		if d := os.Getenv("SFODWORLD_DUMP"); d != "" { // debugging aid: write the stages to a scratch folder
			_ = os.WriteFile(filepath.Join(d, name+".World00p"), b, 0o644)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{StageRaw, StageConverted, StageDeathLure, StageIntroDelay, StageFinale} {
		if got[s] != stageSHA[s] {
			t.Errorf("stage %s: sha256 %s, want %s", s, got[s], stageSHA[s])
		}
	}
	if h := sha(out); h != LiveSHA256 {
		t.Fatalf("Build output %s, want the live world %s", h, LiveSHA256)
	}
	// Build (no stage callback) must give the same bytes
	out2, err := Build(in)
	if err != nil || sha(out2) != LiveSHA256 {
		t.Fatalf("Build: %v %s", err, sha(out2))
	}
}

func TestModFiles(t *testing.T) {
	m := ModFiles()
	if len(m) != 184 {
		t.Fatalf("%d mod files, want 184", len(m))
	}
	for _, p := range m {
		if latin1Lower(p) == latin1Lower(`Tex\Localization\Calibration_A0.dds`) {
			t.Fatal("Calibration_A0.dds must not be in the list (it changes the precache list)")
		}
	}
}

func TestSortListing(t *testing.T) {
	l := []ArchiveFile{{"FEARL_8.Arch00", "a"}, {"FEAR_7.Arch00", "b"}, {"FEARA.Arch00", "c"}, {"FEAR.Arch00", "d"},
		{"FEARE_1.Arch00", "e"}, {"FEAR_1.Arch00", "f"}, {"FEARL.Arch00", "g"}, {"FEAR_7.Arch00", "h"}}
	SortListing(l)
	var got string
	for _, e := range l {
		got += e.Path
	}
	if got != "dfbhcega" {
		t.Fatalf("order %s", got)
	}
	// the last listed spelling wins
	f := newPCFiles([]ArchiveFile{{"FEAR_7.Arch00", `ClientFx\World.Fx00p`}, {"FEAR.Arch00", `ClientFX\World.Fx00p`}}, nil)
	if v, _ := f.find(`clientfx/world.fx00p`); v != `ClientFx\World.Fx00p` {
		t.Fatalf("spelling %s", v)
	}
}

func TestSlowmoPatch(t *testing.T) {
	for _, c := range [][2]string{
		{"a;delay 2 (msg player (slowmo off));b;", "a;delay 2 (msg player (slowmo off));delay 2.05 (msg player (slowmo on noslowmo));b;"},
		{"5200|msg player (slowmo off);x;", "5200|msg player (slowmo off);delay 0.05 (msg player (slowmo on noslowmo));x;"},
		{"msg player (slowmo off)", "msg player (slowmo off);delay 0.05 (msg player (slowmo on noslowmo))"},
	} {
		if g := patchSlowmo(c[0]); g != c[1] {
			t.Errorf("%q -> %q, want %q", c[0], g, c[1])
		}
	}
}

// TestListArchives: the archive listing read from the archives themselves equals the TOC rows of the same
// archives, in the same order (FEAR_REF_PCGAME may hold only some archives, e.g. FEAR_8 and FEARL_8).
func TestListArchives(t *testing.T) {
	dir := testref.Dir(t, "FEAR_REF_PCGAME")
	f, err := os.Open(testref.Path(t, "FEAR_REF_PC_TOC"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	toc, err := ReadTOC(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ListArchives(dir)
	if err != nil {
		t.Fatal(err)
	}
	SortListing(got)
	have := map[string]bool{}
	for _, e := range got {
		have[latin1Lower(e.Arch)] = true
	}
	var want []ArchiveFile
	for _, e := range toc {
		if have[latin1Lower(e.Arch)] {
			want = append(want, e)
		}
	}
	if len(got) == 0 || len(got) != len(want) {
		t.Fatalf("%d entries, TOC has %d", len(got), len(want))
	}
	for i := range got {
		if !strings.EqualFold(got[i].Arch, want[i].Arch) || got[i].Path != want[i].Path {
			t.Fatalf("entry %d: %v, TOC %v", i, got[i], want[i])
		}
	}
	t.Logf("%d entries from %d archives match the TOC", len(got), len(have))
}
