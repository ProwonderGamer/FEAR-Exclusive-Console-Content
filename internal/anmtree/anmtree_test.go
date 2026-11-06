package anmtree

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/ltar"
	"fearinstaller/internal/testref"
)

// Golden SHA-256 values of the confirmed files.
const (
	goldenPlayerUpperSFODD = "4488ad88bc6097585233d1769d88be15b620bd747c9ee60821c58022a67b2b91" // confirmed mod, in-game confirmed
	goldenPCPlayerUpper    = "68e1385c4d68cf80ebca0abee8967f4c75dba0071e488d90abe48fb2125f92f0" // FEAR_8.Arch00 (= FEAR_7.Arch00)
	goldenPS3Container     = "3548cdfe4b69cef521eb87752e756dfa0ed56f02e6af04c0ed0513cf043e2e99" // PS3 database_anim/anim.anmtree
)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// pcTree reads AnimationDatabase\AnimTrees\<name> from FEAR_8.Arch00 (or FEAR_7.Arch00) under FEAR_REF_PCGAME.
func pcTree(t *testing.T, name string) []byte {
	t.Helper()
	dir := testref.Dir(t, "FEAR_REF_PCGAME")
	for _, arch := range []string{"FEAR_8.Arch00", "FEAR_7.Arch00"} {
		p := filepath.Join(dir, arch)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		a, err := ltar.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		for _, e := range a.Entries {
			if strings.EqualFold(e.Path, `AnimationDatabase\AnimTrees\`+name) {
				b, err := a.Read(e)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
		}
	}
	t.Skipf("FEAR_REF_PCGAME: %s not found in FEAR_8 / FEAR_7", name)
	return nil
}

func ps3Container(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(testref.File(t, "FEAR_REF_PS3", "database_anim", "anim.anmtree"))
	if err != nil {
		t.Fatal(err)
	}
	if sha(b) != goldenPS3Container {
		t.Fatalf("PS3 anim.anmtree sha256 %s, want %s", sha(b), goldenPS3Container)
	}
	return b
}

func TestPlayerUpperSFODDGolden(t *testing.T) {
	pc := pcTree(t, "Player_Upper.AnmTree00p")
	if sha(pc) != goldenPCPlayerUpper {
		t.Fatalf("stock Player_Upper sha256 %s, want %s", sha(pc), goldenPCPlayerUpper)
	}
	ps, err := PS3PlayerUpper(ps3Container(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := BuildPlayerUpperSFODD(pc, ps)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha(out); got != goldenPlayerUpperSFODD {
		t.Fatalf("sha256 %s, want %s (%d bytes)", got, goldenPlayerUpperSFODD, len(out))
	}
	t.Logf("Player_Upper_SFODD: %s (%d bytes)", sha(out), len(out))
	// the output and both inputs round-trip through the reader/writer
	for _, b := range [][]byte{pc, ps, out} {
		tr, err := Load(b)
		if err != nil {
			t.Fatal(err)
		}
		rt, err := tr.Serialize()
		if err != nil || sha(rt) != sha(b) {
			t.Fatalf("round trip differs (%v)", err)
		}
	}
	// the builder refuses swapped inputs
	if _, err := BuildPlayerUpperSFODD(ps, pc); err == nil {
		t.Error("swapped inputs accepted")
	}
}

// TestContainerRoundTrip checks every tree of the console containers loads and re-writes byte for byte.
func TestContainerRoundTrip(t *testing.T) {
	for _, src := range []struct{ env, rel string }{{"FEAR_REF_PS3", "database_anim/anim.anmtree"},
		{"FEAR_REF_X360", "Game/database_anim/Anim.anmtree"}} {
		t.Run(src.env, func(t *testing.T) {
			b, err := os.ReadFile(testref.File(t, src.env, strings.Split(src.rel, "/")...))
			if err != nil {
				t.Fatal(err)
			}
			_, trees, err := Split(b)
			if err != nil {
				t.Fatal(err)
			}
			if len(trees) == 0 {
				t.Fatal("no trees")
			}
			for _, e := range trees {
				tr, err := Load(e.Data)
				if err != nil {
					t.Fatalf("%s: %v", e.Name, err)
				}
				rt, err := tr.Serialize()
				if err != nil || sha(rt) != sha(e.Data) {
					t.Fatalf("%s: round trip differs", e.Name)
				}
			}
			t.Logf("%d trees", len(trees))
		})
	}
}

func TestLoadRejectsDamage(t *testing.T) {
	for _, d := range [][]byte{nil, []byte("ANMT"), []byte("XXXX\x03\x00\x00\x00\x00\x00\x00\x00"),
		append([]byte("ANMT\x03\x00\x00\x00\x00\x00\x00\x00"), 0xff, 0xff, 0xff, 0x7f)} {
		if _, err := Load(d); err == nil {
			t.Errorf("accepted %q", d)
		}
	}
	if _, _, err := Split([]byte("LTNA\x00\x00\x00\x04\xff\xff\xff\xff")); err == nil {
		t.Error("damaged container accepted")
	}
}

const (
	goldenSoldierHoliday  = "656702605135cd01dc90c9566d548d4f26867cbf014bbb09f87d4065c372e15b" // confirmed mod
	goldenSoldierHoliday2 = "b53625846189a68c6167103407fba1a373275fd202050b0d01a1e6cc497ebcad" // confirmed mod
	goldenX360Container   = "3ba1caae5066e263ccbb25a9f3a409113bd9feebccd1ba95a346ba6a26263187" // X360 Game/database_anim/Anim.anmtree
)

// gameServerDLL reads GameServer.dll from FEARE_8.Arch00 under FEAR_REF_PCGAME (or skips).
func gameServerDLL(t *testing.T) []byte {
	t.Helper()
	a, err := ltar.Open(testref.File(t, "FEAR_REF_PCGAME", "FEARE_8.Arch00"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, e := range a.Entries {
		if strings.EqualFold(e.Path, `GameServer.dll`) {
			b, err := a.Read(e)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
	t.Skip("GameServer.dll not in FEARE_8.Arch00")
	return nil
}

func x360Container(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(testref.File(t, "FEAR_REF_X360", "Game", "database_anim", "Anim.anmtree"))
	if err != nil {
		t.Fatal(err)
	}
	if sha(b) != goldenX360Container {
		t.Fatalf("X360 Anim.anmtree sha256 %s, want %s", sha(b), goldenX360Container)
	}
	return b
}

func TestSoldierHolidayGolden(t *testing.T) {
	enum, err := PCAnimPropEnum(gameServerDLL(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(enum) != 321 {
		t.Errorf("GameServer.dll prop enum has %d names, want 321", len(enum))
	}
	so, err := X360Soldier(x360Container(t))
	if err != nil {
		t.Fatal(err)
	}
	h, err := BuildSoldierHoliday(so, enum)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha(h); got != goldenSoldierHoliday {
		t.Fatalf("Soldier_Holiday sha256 %s, want %s (%d bytes)", got, goldenSoldierHoliday, len(h))
	}
	h2, err := BuildSoldierHoliday2(h)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha(h2); got != goldenSoldierHoliday2 {
		t.Fatalf("Soldier_Holiday2 sha256 %s, want %s (%d bytes)", got, goldenSoldierHoliday2, len(h2))
	}
	t.Logf("Soldier_Holiday %d bytes, Soldier_Holiday2 %d bytes", len(h), len(h2))
	if _, err := BuildSoldierHoliday2(h2); err == nil {
		t.Error("BuildSoldierHoliday2 accepted its own output")
	}
	if _, err := PCAnimPropEnum([]byte("MZ not a dll")); err == nil {
		t.Error("PCAnimPropEnum accepted junk")
	}
}

// TestPlayerUpperFromX360 records that the Xbox 360 Player_Upper tree does NOT give the shipped file (it is a
// different tree), so an Xbox-only install cannot build Player_Upper_SFODD.
func TestPlayerUpperFromX360(t *testing.T) {
	pc := pcTree(t, "Player_Upper.AnmTree00p")
	xs, err := Find(x360Container(t), "Player_Upper")
	if err != nil {
		t.Fatal(err)
	}
	out, err := BuildPlayerUpperSFODD(pc, xs)
	if err != nil {
		t.Logf("X360 Player_Upper: build refused: %v", err)
		return
	}
	if sha(out) == goldenPlayerUpperSFODD {
		t.Fatal("X360 Player_Upper unexpectedly gives the shipped file: update the console requirement")
	}
	t.Logf("X360 Player_Upper gives a different file: %s (%d bytes)", sha(out), len(out))
}

// TestSoldierSameOnBothConsoles: the PS3 Soldier tree is byte-identical to the Xbox 360 one, so
// BuildSoldierHoliday works from either console copy.
func TestSoldierSameOnBothConsoles(t *testing.T) {
	x, err := X360Soldier(x360Container(t))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Find(ps3Container(t), "Soldier")
	if err != nil {
		t.Fatal(err)
	}
	if sha(x) != sha(p) {
		t.Fatalf("PS3 and X360 Soldier trees differ")
	}
}
