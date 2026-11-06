package model

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/testref"
)

// Golden SHA-256 of the Python converter's (mdl2model00p convert()) output on
// copies of the PS3 and X360 uberCatalog.cat. The PS3 rows and the two X360 SM15 rows are files the mod ships
// (the first three equal the confirmed mod); the other X360 rows are Python parity only: those X360 models store
// DEC3N normals the converter does not decode, so they are NOT usable PC models.
var convertGolden = []struct {
	env, name, sha string
}{
	{"FEAR_REF_PS3", `chars\models\player_SM15.mdl`, "523dc824e414273569db98a2e949abafb1153216468bb63f83aff079d351ce88"},
	{"FEAR_REF_PS3", `chars\models\player_dualSM15.mdl`, "aafa72e226d8f9e4f0136c8d50611743e03ef4391faec451303a23f7b86b4d4b"},
	{"FEAR_REF_PS3", `chars\models\corpse_office_bob_alive.mdl`, "f843ddea1e9368edc156c7db61841ee5458ff87f4f23d7e505a613dfbc78f7a1"},
	{"FEAR_REF_PS3", `chars\models\player_Deltaforce.mdl`, "7017fa1a5d9528bc9e84c5bfa9ef61e364d031ac196cb7ed425b36552c5a6ed4"},
	{"FEAR_REF_PS3", `guns\models_hh\WatsonSAS.mdl`, "7ca9dd64293fd65c8c2e80df2d30d52ba0ceabf49460f2bcedb90b500f7ded1a"},
	{"FEAR_REF_PS3", `chars\models\player_watson.mdl`, "ee5ed4976cc6bdfcb55148370bd9fdfeece3c14c12870675cd98172cfb740b2c"},
	{"FEAR_REF_X360", `chars\models\player_SM15.mdl`, "523dc824e414273569db98a2e949abafb1153216468bb63f83aff079d351ce88"},
	{"FEAR_REF_X360", `chars\models\player_dualSM15.mdl`, "aafa72e226d8f9e4f0136c8d50611743e03ef4391faec451303a23f7b86b4d4b"},
	// parity only (X360 DEC3N normals read as CMP, like the Python)
	{"FEAR_REF_X360", `chars\models\corpse_office_bob_alive.mdl`, "1dcf63968bd7c59e79349124a3dc6bdf8ff3ba14509bcdb2f617058f026d96c4"},
	{"FEAR_REF_X360", `chars\models\player_Deltaforce.mdl`, "8c6024e0f7549a0d1bb7ca0c19a281ff6060b18a946ebf538489eb60613d96a9"},
	{"FEAR_REF_X360", `guns\models_hh\WatsonSAS.mdl`, "a8e2e4b63d59e02d53be300fac895fa2c25e3a6301d047928d4139f309bc5559"},
	{"FEAR_REF_X360", `chars\models\player_watson.mdl`, "4902aad8a8a3ee2b2a3fa82bec3bd66858118cf8e1afefad18e81ef6df81f9f3"},
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

var (
	catMu   sync.Mutex
	catOpen = map[string]*catalog.Catalog{}
)

// consoleCatalog opens the console uberCatalog named by env (PS3: <game>/ubercatalog.cat, X360:
// <disc>/Game/uberCatalog.cat) or skips.
func consoleCatalog(t *testing.T, env string) *catalog.Catalog {
	t.Helper()
	dir := testref.Dir(t, env)
	catMu.Lock()
	defer catMu.Unlock()
	if c, ok := catOpen[env]; ok {
		return c
	}
	var cands []string
	if env == "FEAR_REF_X360" {
		cands = []string{filepath.Join(dir, "Game", "uberCatalog.cat")}
	} else {
		cands = []string{filepath.Join(dir, "ubercatalog.cat"), filepath.Join(dir, "uberCatalog.cat")}
	}
	for _, p := range cands {
		if _, err := os.Stat(p); err == nil {
			c, err := catalog.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			catOpen[env] = c
			return c
		}
	}
	t.Skipf("%s: no uberCatalog.cat under %s", env, dir)
	return nil
}

// consoleModel reads one model from a console catalog and returns its bytes and stored name.
func consoleModel(t *testing.T, env, name string) ([]byte, string) {
	t.Helper()
	c := consoleCatalog(t, env)
	e, ok := c.Lookup(name)
	if !ok {
		t.Fatalf("%s: %s not in the catalog", env, name)
	}
	b, err := c.Read(e)
	if err != nil {
		t.Fatal(err)
	}
	return b, e.Name
}

func TestConvertGolden(t *testing.T) {
	for _, g := range convertGolden {
		g := g
		t.Run(g.env+"/"+filepath.Base(filepath.FromSlash(g.name)), func(t *testing.T) {
			src, stored := consoleModel(t, g.env, g.name)
			// the parser must re-write the console file byte for byte
			m, err := Parse(src)
			if err != nil {
				t.Fatal(err)
			}
			rt, err := m.Serialize(m.BigEndian, m.Version)
			if err != nil {
				t.Fatal(err)
			}
			if sha(rt) != sha(src) {
				t.Errorf("console round trip differs")
			}
			out, st, err := Convert(src, stored, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if got := sha(out); got != g.sha {
				t.Errorf("sha256 %s, want %s (%d bytes)", got, g.sha, len(out))
			}
			if st.PCBytes != len(out) || st.PS3Bytes != len(src) {
				t.Errorf("stats sizes")
			}
			// and the PC output must round-trip through the parser as well
			pm, err := Parse(out)
			if err != nil {
				t.Fatal(err)
			}
			if rt2, _ := pm.Serialize(false, 33); sha(rt2) != sha(out) {
				t.Errorf("PC round trip differs")
			}
		})
	}
}

func TestChildNamePC(t *testing.T) {
	cases := []struct{ child, parent, rel string }{
		{`chars\models\Playerbase.mdl`, `chars\models\player_Deltaforce.Model00p`, `Playerbase.Model00p`},
		{`Chars\Models\x.MDL`, `chars\models\p.Model00p`, `x.Model00p`},
		{`guns\models_hh\a.mdl`, `chars\models\p.Model00p`, `..\..\guns\models_hh\a.Model00p`},
		{`chars\b.mdl`, `chars\models\p.Model00p`, `..\b.Model00p`},
		{`x.mdl`, `p.Model00p`, `x.Model00p`},
		{`chars/models/y.mdl`, `chars/models/p.Model00p`, `..\..\chars/models/y.Model00p`},
	}
	for _, c := range cases {
		if rel, _ := childNamePC(c.child, c.parent); rel != c.rel {
			t.Errorf("childNamePC(%q, %q) = %q, want %q", c.child, c.parent, rel, c.rel)
		}
	}
	if lowerLatin1("A\xC0\xDE\xD7\xDF\xB5") != "a\xE0\xFE\xD7\xDF\xB5" { // Latin-1 bytes, not UTF-8

		t.Errorf("lowerLatin1")
	}
}

func TestParseRejectsDamage(t *testing.T) {
	for _, d := range [][]byte{nil, []byte("MOD"), []byte("XXXXxxxx"), []byte("MODL\x21\x00\x00\x00"),
		append([]byte("LDOM\x00\x00\x00\x22"), make([]byte, 84)...)} {
		if _, err := Parse(d); err == nil {
			t.Errorf("Parse(%q) accepted", d)
		}
	}
}

// sensitiveS is the only squared length (x*x + y*y + z*z, float64) at which a 1-ulp change of its square root
// changes a stored float32 component; sensitiveCMP are the 16 CMP words that produce it (sign/axis
// permutations). Measured with the reference CPython 3.12 (Windows UCRT pow): there,
// s ** 0.5 == math.sqrt(s) for this s, and the Python and Go outputs are bit-identical for all 16 words
// (packed float32 bytes compared).
const sensitiveS = 1.1852542306935465

var sensitiveCMP = []uint32{0x66caf298, 0x66caf568, 0x66d4c15e, 0x66d4c6a2, 0x66eb415e, 0x66eb46a2, 0x66f51298,
	0x66f51568, 0x994af298, 0x994af568, 0x9954c15e, 0x9954c6a2, 0x996b415e, 0x996b46a2, 0x99751298, 0x99751568}

// TestCMPPowSqrtEquivalence: the Python takes the CMP vector length as (s ** 0.5), i.e. the C runtime's pow,
// which on Windows differs from the correctly rounded sqrt by 1 ulp for ~0.05% of the inputs (measured; never
// more than 1 ulp in 2M samples). Go uses math.Sqrt. This checks all 2^32 CMP words: with the length moved
// 1 ulp either way, the stored float32 components change only for the 16 words of sensitiveS, where the
// reference Python's pow equals sqrt. So Convert matches the Python for every CMP word.
// It takes ~10 s on 8+ cores, so it runs only with FEAR_MODEL_EXHAUSTIVE=1.
func TestCMPPowSqrtEquivalence(t *testing.T) {
	if os.Getenv("FEAR_MODEL_EXHAUSTIVE") == "" {
		t.Skip("FEAR_MODEL_EXHAUSTIVE not set (exhaustive 2^32 check)")
	}
	var xs [2048]float64
	for i := range xs {
		xs[i] = float64(signExt(uint32(i), 11)) / 1023.0
	}
	var zs [1024]float64
	for i := range zs {
		zs[i] = float64(signExt(uint32(i), 10)) / 511.0
	}
	var mu sync.Mutex
	sens := map[uint32]bool{}
	otherS := 0
	var wg sync.WaitGroup
	work := make(chan int)
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for zi := range work {
				z := zs[zi]
				for yi := 0; yi < 2048; yi++ {
					y := xs[yi]
					for xi := 0; xi < 2048; xi++ {
						x := xs[xi]
						s := float64(x*x) + float64(y*y) + float64(z*z)
						r := math.Sqrt(s)
						if r <= 1e-6 {
							continue
						}
						for _, r2 := range [2]float64{math.Nextafter(r, 0), math.Nextafter(r, 2)} {
							if !(r2 > 1e-6) ||
								float32(x/r2) != float32(x/r) || float32(y/r2) != float32(y/r) || float32(z/r2) != float32(z/r) {
								mu.Lock()
								sens[uint32(xi)|uint32(yi)<<11|uint32(zi)<<22] = true
								if s != sensitiveS {
									otherS++
								}
								mu.Unlock()
							}
						}
					}
				}
			}
		}()
	}
	for zi := 0; zi < 1024; zi++ {
		work <- zi
	}
	close(work)
	wg.Wait()
	if otherS != 0 || len(sens) != len(sensitiveCMP) {
		t.Fatalf("sensitive CMP words: %d (want %d), %d at another length: %v", len(sens), len(sensitiveCMP), otherS, sens)
	}
	for _, u := range sensitiveCMP {
		if !sens[u] {
			t.Errorf("expected sensitive word %#x not found", u)
		}
	}
}
