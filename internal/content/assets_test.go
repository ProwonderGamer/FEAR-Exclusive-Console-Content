package content

import (
	"strings"
	"testing"

	"fearinstaller/internal/testref"
)

// TestAssetsMatchConfirmed builds the assets stage from the reference copies (both consoles, PS3 only, Xbox 360
// only) and checks every written file against the in-game confirmed hash of the mod file table. This is the proof
// behind the table's Built flag for these rows, independent of the converter packages' own golden constants.
func TestAssetsMatchConfirmed(t *testing.T) {
	ps3 := testref.File(t, "FEAR_REF_PS3", "ubercatalog.cat")
	x360 := testref.File(t, "FEAR_REF_X360", "Game", "uberCatalog.cat")
	pc := testref.File(t, "FEAR_REF_PCGAME", "FEAR_7.Arch00")
	// the loose-file assets (animation trees, SFOD-D world) and the complete stock archive set they read
	// (the world depends on the listing of every archive): skip on a partial reference set
	testref.File(t, "FEAR_REF_PS3", "database_anim", "anim.anmtree")
	testref.File(t, "FEAR_REF_PS3", "worlds", "release", "sfod_d.wldsrvr")
	testref.File(t, "FEAR_REF_X360", "Game", "Worlds", "Release", "Holiday.matCat")
	testref.File(t, "FEAR_REF_X360", "Game", "database_anim", "Anim.anmtree")
	for _, a := range []string{"FEAR.Arch00", "FEAR_8.Arch00", "FEARA_8.Arch00", "FEARE_8.Arch00", "FEARL_8.Arch00"} {
		testref.File(t, "FEAR_REF_PCGAME", a)
	}
	ps3, x360, pc = dirOf(ps3, 1), dirOf(x360, 2), dirOf(pc, 1)
	all := Choice{SFOD: true, Holiday: true, SAS12: true, SM15: true, IA: true}
	for _, tc := range []struct {
		name      string
		ps3, x360 string
		want      int
	}{
		{"both", ps3, x360, len(assets)},
		{"ps3", ps3, "", 0},
		{"x360", "", x360, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Build(Options{PCDir: pc, PS3Dir: tc.ps3, X360Dir: tc.x360, OutDir: t.TempDir(), Stages: []string{"assets"}, Choice: all})
			if err != nil {
				t.Fatal(err)
			}
			c := all
			c.HavePS3, c.HaveX360 = tc.ps3 != "", tc.x360 != ""
			// exactly the asset rows the coverage check calls buildable from these copies
			want := 0
			for _, f := range ModFiles(c) {
				if _, ok := assetNeeds(f.Path); ok && c.buildable(f) {
					want++
				}
			}
			if tc.want != 0 && want != tc.want {
				t.Fatalf("coverage says %d assets, table has %d", want, tc.want)
			}
			if len(res.Files) != want {
				t.Fatalf("%d files written, coverage expects %d", len(res.Files), want)
			}
			for _, f := range res.Files {
				rel := strings.TrimPrefix(f.Rel, "Game/")
				if h := confirmedHash(rel); h != f.SHA256 {
					t.Errorf("%s: built %s, confirmed %s", rel, f.SHA256, h)
				}
			}
		})
	}
}

// dirOf strips n trailing path elements.
func dirOf(p string, n int) string {
	for ; n > 0; n-- {
		i := strings.LastIndexAny(p, `\/`)
		p = p[:i]
	}
	return p
}

func TestAssetTable(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range assets {
		k := strings.ToLower(a.rel)
		if seen[k] {
			t.Fatalf("%s twice", a.rel)
		}
		seen[k] = true
		if confirmedHash(a.rel) == "" {
			t.Fatalf("%s is not a mod file", a.rel)
		}
		if (a.from == 0) != (a.path == "") {
			t.Fatalf("%s: a console source needs a catalog path and the reverse", a.rel)
		}
	}
	for _, f := range modFiles {
		if _, ok := assetNeeds(f.Path); ok && !f.Built {
			t.Fatalf("%s: an asset row must be marked built", f.Path)
		}
	}
}
