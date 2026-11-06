package dbmerge

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/strdb"
	"fearinstaller/internal/testref"
)

// SHA-256 of the reference outputs. All of them are produced by the three-stage Python pipeline (base merge,
// SFOD-D, Holiday), and the SFOD-D and Holiday databases built from them were confirmed in game.
const (
	goldStage1FEAR      = "0210effce02b02114c396dcade8ef4376b194894a2a4cff82be21eefafbf91d5" // stage 1: base merge
	goldStage1FEARL     = "2fbd952085762ff2eddedb3138dd6a0aaec8e69b1c9d7588d5fd76237f8c85f3" // stage 1: base merge (localized)
	goldSFODFEARDropFix = "ec7c41f5fad1d0ce712de7f464b3544502697b4c77fef705abafdba62040118b" // SFOD-D database with the SM15 drop fix
	goldSFODFEAR        = "a635b09f9c3de68d4d77a87ffc33ec1cb8f9b5852ac17d2ea9f1817d2c595642" // SFOD-D database without the drop fix
	goldSFODFEARL       = "48e1e8dfa1b7927782f54bb5c00812cbbc08236eb68d92256d7eb19acc5fd2f2" // SFOD-D database (localized)
	goldHolFEAR         = "fd014774f3796931df2664b81dd0670f761afce0346db587e2e05ad0ea520337" // SFOD-D + Holiday database
	goldHolFEARL        = "48e1e8dfa1b7927782f54bb5c00812cbbc08236eb68d92256d7eb19acc5fd2f2" // SFOD-D + Holiday database (localized)
	goldArtFEAR         = "339730000b12cfbfdfdedbb15085ab8e979debff9391f6eef35c35027fd3d26f" // variant --sfod-ps3-art
	goldArtFEARL        = "25b3143414f79f6bf1b968b10887622ff03daabc7f800af197348a3c593f8f35"
)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func load(t *testing.T) Inputs {
	t.Helper()
	pcDir := testref.Dir(t, "FEAR_REF_PCGAME")
	fs, errs := pcfs.Open(pcDir)
	if len(errs) > 0 {
		t.Logf("archive warnings: %v", errs)
	}
	t.Cleanup(fs.Close)
	pc, err := fs.Read(`Database\FEAR.gamdb00p`)
	if err != nil {
		t.Skipf("stock database not available: %v", err)
	}
	pcl, err := fs.Read(`DatabaseLocalized\FEARL.Gamdb00p`)
	if err != nil {
		t.Skipf("stock FEARL not available: %v", err)
	}
	toc, err := pcfs.ReadTOCFile(testref.Path(t, "FEAR_REF_PC_TOC"))
	if err != nil {
		t.Fatal(err)
	}
	rd := func(env string, rel ...string) []byte {
		b, err := os.ReadFile(testref.File(t, env, rel...))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	in := Inputs{PC: pc, PCL: pcl, TOC: toc,
		PS3DB:    rd("FEAR_REF_PS3", "database", "loki.gamedb"),
		PS3World: rd("FEAR_REF_PS3", "worlds", "release", "sfod_d.wldsrvr"),
		X360DB:   rd("FEAR_REF_X360", "Game", "database", "Loki.gamedb")}
	for _, w := range []string{"Admin", "Aftermath", "Moody", "ATC_Roof", "Mapes_Elevator"} {
		in.PCWorlds = append(in.PCWorlds, rd("FEAR_REF_PCWORLDS", w+".World00p"))
	}
	// the SFOD-D string database (built by the strdb package from the same originals)
	sb, err := os.ReadFile(filepath.Join(os.Getenv("FEAR_REF_PS3_EXTRACT"), "database_string", "English", "Loki.strdb"))
	if err != nil {
		t.Skip(err)
	}
	ps3s, err := strdb.Parse(sb)
	if err != nil {
		t.Fatal(err)
	}
	sfs, err := fs.Read(`StringDatabase\FEAR.Strdb00p`)
	if err != nil {
		t.Skip(err)
	}
	pcs, err := strdb.Parse(sfs)
	if err != nil {
		t.Fatal(err)
	}
	db, _, err := strdb.BuildSFODD(pcs, ps3s)
	if err != nil {
		t.Fatal(err)
	}
	in.Strings = db
	return in
}

func check(t *testing.T, what string, got []byte, want string) {
	t.Helper()
	if s := sha(got); s != want {
		t.Errorf("%s: sha256 %s, reference %s (%d bytes)", what, s, want, len(got))
	} else {
		t.Logf("%-34s %8d bytes: byte-exact", what, len(got))
	}
}

func TestGoldenDatabasePipeline(t *testing.T) {
	in := load(t)
	in.NoDropFix = true
	out, err := BuildAll(in)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "stage 1 FEAR.gamdb00p (merge)", out.Stage1.FEAR, goldStage1FEAR)
	check(t, "stage 1 FEARL.Gamdb00p (merge)", out.Stage1.FEARL, goldStage1FEARL)
	check(t, "SFOD-D FEAR.gamdb00p", out.SFOD.FEAR, goldSFODFEAR)
	check(t, "SFOD-D FEARL.Gamdb00p", out.SFOD.FEARL, goldSFODFEARL)
	check(t, "SFOD-D+Holiday FEAR.gamdb00p", out.Holiday.FEAR, goldHolFEAR)
	check(t, "SFOD-D+Holiday FEARL.Gamdb00p", out.Holiday.FEARL, goldHolFEARL)

	// the shipped SFOD-D database = the base SFOD-D database + the SM15 drop fix
	in.NoDropFix = false
	fixed, err := BuildAll(in)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "SFOD-D FEAR.gamdb00p (update 3, drop fix)", fixed.SFOD.FEAR, goldSFODFEARDropFix)
	check(t, "SFOD-D FEARL.Gamdb00p (update 3)", fixed.SFOD.FEARL, goldSFODFEARL)
	check(t, "Holiday FEAR (unchanged with drop fix)", fixed.Holiday.FEAR, goldHolFEAR)

	in.SFODPS3Art = true
	art, err := BuildAll(in)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "variant sfod-ps3-art FEAR.gamdb00p", art.Holiday.FEAR, goldArtFEAR)
	check(t, "variant sfod-ps3-art FEARL.Gamdb00p", art.Holiday.FEARL, goldArtFEARL)
}

func TestNorm(t *testing.T) {
	for in, want := range map[string]string{"Ammo.10.Ammo": "Ammo.#.Ammo", "a.1.2.b": "a.#.#.b", "a.1": "a.1", "Lines.0.Line": "Lines.#.Line", "x.12": "x.12", ".5.": ".#."} {
		if got := norm(in); got != want {
			t.Errorf("norm(%q) = %q, want %q", in, got, want)
		}
	}
	if zeroIdx("a.1.2.b") != "a.0.2.b" {
		t.Error("zeroIdx must replace non-overlapping .digits. only (Python re.sub semantics)")
	}
}

func TestSplitExt(t *testing.T) {
	for in, want := range map[string][2]string{`a\b.mat`: {`a\b`, ".mat"}, "x": {"x", ""}, ".hidden": {".hidden", ""}, `a/.b`: {`a/.b`, ""}, "a.b.c": {"a.b", ".c"}} {
		b, e := splitExt(in)
		if b != want[0] || e != want[1] {
			t.Errorf("splitExt(%q) = %q,%q want %q", in, b, e, want)
		}
	}
}
