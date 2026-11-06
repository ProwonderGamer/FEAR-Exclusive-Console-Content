package tex

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/testref"
)

// Golden SHA-256 of the files of the confirmed mod.
var golden = []struct {
	out     string // path in the mod folder
	console string // "PS3" or "X360"
	src     string // catalog entry
	build   func([]byte) ([]byte, error)
	sha     string
}{
	{`guns\skins_pv\SM15_Machine_Pistol_D.dds`, "X360", `guns\skins_pv\SM15_Machine_Pistol_D.tex`, FromX360,
		"bf7f5c7df4ca3061704011bed68eb66b35d9c4dcfb56ad33f135781f50561600"},
	{`guns\skins_pv\SM15_Machine_Pistol_S.dds`, "X360", `guns\skins_pv\SM15_Machine_Pistol_S.tex`, FromX360,
		"4b5d145b9e006b98ebe1869ba4ec569b9841ffebd6087a62d10df1f1be017725"},
	{`guns\skins_pv\Watson_SAS-12_D.dds`, "PS3", `guns\skins_pv\Watson_SAS-12_D.tex`, ps3Full,
		"c5048292eae0d120f7149cf61efdf4f06d9c2c4bbf69ab0ba91bbd9f795330d9"},
	{`guns\skins_pv\Watson_SAS-12_S.dds`, "PS3", `guns\skins_pv\Watson_SAS-12_S.tex`, ps3Full,
		"5289954f937520f1779d9d7fcd602a040163defe3b778021497ef8abcff83545"},
	{`Interface\hud\obj_example.dds`, "PS3", `Interface\hud\obj_example.tex`, FromPS3DXT,
		"863db0ef7fc9b0eecb5c383ea6bf78c0a8cfee4b43a25d80975ad3fceb3bc6c1"},
	{`Interface\menu\art\LoadScreens\load_bonusX360.dds`, "X360", `Interface\menu\art\LoadScreens\load_bonusX360.tex`, FromX360,
		"cd6cd830b2c640112cc83fd4939e1223ef9849926705c8e8aa58cab255f65095"},
	{`Interface\menu\art\LoadScreens\load_bonusX360.dds`, "PS3", `Interface\menu\art\LoadScreens\load_bonusX360.tex`, ps3Stored,
		"cd6cd830b2c640112cc83fd4939e1223ef9849926705c8e8aa58cab255f65095"},
	{`Tex\Localization\Calibration_A0.dds`, "X360", `Tex\Localization\Calibration_A0.tex`, FromX360,
		"26580c46c4f1525a4c8b5366c05cf13be224c8b440564caec53501cba5535b3a"},
	{`Tex\Localization\Calibration_A0.dds`, "PS3", `Tex\Localization\Calibration_A0.tex`, ps3Stored,
		"26580c46c4f1525a4c8b5366c05cf13be224c8b440564caec53501cba5535b3a"},
	{`Materials\Engineering\black.dds`, "X360", `Materials\Engineering\black.tex`, FromX360,
		"c81dfe4de7f4845a114a63207e168ce4ea52b2f0f0daa0cd977415a238dc9b2a"},
	{`Materials\grid\grid_e.dds`, "PS3", `Materials\grid\grid_e.tex`, ps3Full,
		"d1e43c6f524468bca350bdc9b711c9120ba54ddfc48b623e1d425f6d4ebd2390"},
	{`Materials\grid\grid_s.dds`, "PS3", `Materials\grid\grid_s.tex`, ps3Full,
		"f4ce67c3ab6a02b73ba7612e006d74ad73138cd4594a90efad6e5cfda81b0e10"},
	{`Materials\grid\grid_e.dds`, "X360", `Materials\grid\grid_e.tex`, FromX360,
		"d1e43c6f524468bca350bdc9b711c9120ba54ddfc48b623e1d425f6d4ebd2390"},
	{`Materials\grid\grid_s.dds`, "X360", `Materials\grid\grid_s.tex`, FromX360,
		"f4ce67c3ab6a02b73ba7612e006d74ad73138cd4594a90efad6e5cfda81b0e10"},
	{`Materials\Grid_Floor_D.dds`, "X360", `Materials\Grid_Floor_D.tex`, FromX360,
		"77faefa25bcf0582714daf11a557d3523dbfd6e7cf5f555d8da25a1bf3c5ceb1"},
	// normal maps (A8R8G8B8 decoded from DXN / DXT5-nm, Z rebuilt)
	{`guns\skins_pv\SM15_Machine_Pistol_N.dds`, "X360", `guns\skins_pv\SM15_Machine_Pistol_N.tex`, FromX360Normal,
		"5aac8348b633661b9e6f45fb0730de8833ec43d75f0100c567c8e871f599c435"},
	{`Materials\grid\grid_n.dds`, "X360", `Materials\grid\grid_n.tex`, FromX360Normal,
		"06a4d24be71bfa34d09b99278a556504fe3e91eaacb6bba7627ed08826a6d792"},
	{`guns\skins_pv\Watson_SAS-12_N.dds`, "PS3", `guns\skins_pv\Watson_SAS-12_N.tex`, FromPS3Normal,
		"25d2fb0d75911247dfa9a617e3b098033970fa64359e4ee7b79b9679b1090d3d"},
}

func ps3Full(b []byte) ([]byte, error)   { return FromPS3ARGB(b, true) }
func ps3Stored(b []byte) ([]byte, error) { return FromPS3ARGB(b, false) }

var (
	catMu sync.Mutex
	cats  = map[string]*catalog.Catalog{}
)

// consoleCatalog opens the PS3 ubercatalog.cat (FEAR_REF_PS3) or the X360 Game\uberCatalog.cat (FEAR_REF_X360),
// or skips.
func consoleCatalog(t *testing.T, console string) *catalog.Catalog {
	t.Helper()
	catMu.Lock()
	defer catMu.Unlock()
	if c := cats[console]; c != nil {
		return c
	}
	var p string
	if console == "PS3" {
		p = testref.File(t, "FEAR_REF_PS3", "ubercatalog.cat")
	} else {
		p = testref.File(t, "FEAR_REF_X360", "Game", "uberCatalog.cat")
	}
	c, err := catalog.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	cats[console] = c
	return c
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestGolden(t *testing.T) {
	for _, g := range golden {
		t.Run(g.console+"/"+filepath.Base(g.out), func(t *testing.T) {
			src, err := consoleCatalog(t, g.console).ReadByName(g.src)
			if err != nil {
				t.Fatal(err)
			}
			out, err := g.build(src)
			if err != nil {
				t.Fatal(err)
			}
			dump(t, g.console, g.out, out)
			if got := sum(out); got != g.sha {
				t.Errorf("%s from %s: sha256 %s, want %s", g.out, g.console, got, g.sha)
			}
		})
	}
}

// dump writes a result below FEAR_TEX_DUMP when that variable is set (for comparing failures by hand).
func dump(t *testing.T, console, rel string, b []byte) {
	d := os.Getenv("FEAR_TEX_DUMP")
	if d == "" {
		return
	}
	p := filepath.Join(d, console, filepath.FromSlash(filepath.ToSlash(rel)))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Log(err)
	}
}

func TestDivRoundEven(t *testing.T) {
	for _, c := range []struct{ s, n, want int }{{2, 4, 0}, {6, 4, 2}, {10, 4, 2}, {14, 4, 4}, {3, 4, 1}, {5, 4, 1}, {1, 2, 0}, {3, 2, 2}, {7, 1, 7}} {
		if got := divRoundEven(c.s, c.n); got != c.want {
			t.Errorf("divRoundEven(%d,%d) = %d, want %d", c.s, c.n, got, c.want)
		}
	}
}

func TestHeaderTemplate(t *testing.T) {
	// header of the stock-format files tested in game (SM15_Machine_Pistol_D.dds, 512x512, 1 level)
	const want = "444453207c00000007100a0000020000000200000000100000000000010000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000200000004100000000000000200000000000ff0000ff0000ff000000000000ff0810400000000000000000000000000000000000"
	if got := hex.EncodeToString(DDSHeaderARGB(512, 512, 1)); got != want {
		t.Errorf("A8R8G8B8 header\n got %s\nwant %s", got, want)
	}
	// obj_example.dds (64x64 DXT5, 1 level)
	const wantDXT = "444453207c00000007100a004000000040000000001000000000000001000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000020000000040000004458543500000000000000000000000000000000000000000810400000000000000000000000000000000000"
	if got := hex.EncodeToString(DDSHeaderDXT(64, 64, 1, "DXT5")); got != wantDXT {
		t.Errorf("DXT5 header\n got %s\nwant %s", got, wantDXT)
	}
}
