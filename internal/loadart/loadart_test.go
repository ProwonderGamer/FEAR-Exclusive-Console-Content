package loadart

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/testref"
)

// liveSHA256 are the in-game confirmed IA_Load files (Interface\IA of the confirmed mod),
// which the recipe reproduces byte for byte with Pillow 12.1.1 / FreeType 2.14.1 and this PC's arialbd.ttf
// (sha256 4d59199834e7439c67ea4949407639961060b21b173833f566eb60ac6fd8f5af).
var liveSHA256 = []string{
	"2395b9820affecc52bc9cf17559a4f4552294d35d21044acd9584dd0c6e4ebd1",
	"55c9d8b2a8f14c7b67a3aa37647c19e9e34900d616f913d5c2e25f52579743cd",
	"8b21cd14a40058dcfcbb43dc69fe43978308d02630c0db912df0602d95c85ae1",
	"b4c83e491b28d8c518257d61df66413f516ec6e49e65a968130f7e3130b10656",
}

// Tolerance against the confirmed files, per mip level and B/G/R channel, over all pixels. The only known source
// of difference is FreeType's TrueType hinting, which this port does not run: glyph edges sit up to about one
// pixel apart vertically before the 1920x1080 -> 1024x1024 resize. Measured on all four files:
// mip 0 rms <= 2.11, p99.9 <= 22, max 209, <= 0.80% of pixels differ; mip 1 rms <= 1.50, p99.9 <= 24,
// max 125, <= 1.43%. The limits sit a little above that so that any other change fails. Outside the three
// text boxes the pictures must be byte-identical (background, photo, resize, composite, mip, DDS: proven exact
// by swapping Pillow's own text layer into this pipeline, which gave the confirmed file's hash).
const (
	tolRMS   = 2.5
	tolP999  = 30
	tolMax   = 230
	tolShare = 0.016
)

// textRows are the rows (mip 0, 1024 space) that may differ: the title and map line, and "Loading. . .".
var textRows = [][2]int{{48, 152}, {812, 862}}

func inTextRows(y, level int) bool {
	for _, r := range textRows {
		if y >= r[0]>>level && y < (r[1]+1)>>level {
			return true
		}
	}
	return false
}

func arialBoldPath(t *testing.T) string {
	if p := os.Getenv("FEAR_REF_ARIALBD"); p != "" {
		return p
	}
	win := os.Getenv("WINDIR")
	if win == "" {
		t.Skip("WINDIR and FEAR_REF_ARIALBD not set (Arial Bold is needed)")
	}
	p := filepath.Join(win, "Fonts", "arialbd.ttf")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s: %v", p, err)
	}
	return p
}

func buildFromRefs(t *testing.T) map[string][]byte {
	t.Helper()
	cat := testref.File(t, "FEAR_REF_X360", "Game", "uberCatalog.cat")
	pc := testref.Dir(t, "FEAR_REF_PCGAME")
	ab, err := os.ReadFile(arialBoldPath(t))
	if err != nil {
		t.Fatal(err)
	}
	fs, errs := pcfs.Open(pc)
	if fs == nil {
		t.Fatalf("pc archives: %v", errs)
	}
	defer fs.Close()
	np, err := fs.Read(`interface\fonts\NEUROPOX.TTF`)
	if err != nil {
		t.Skipf("NEUROPOX.TTF: %v", err)
	}
	c, err := catalog.Open(cat)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := Build(c, np, ab)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type levelStats struct {
	rms     [3]float64
	p999    [3]int
	max     [3]int
	changed int
	outside int // differing pixels outside the text rows
	n       int
}

func compareLevel(got, want []byte, level int) levelStats {
	var s levelStats
	w := 1024 >> level
	var hist [3][256]int
	var sq [3]float64
	for i := 0; i < len(want); i += 4 {
		s.n++
		diff := false
		for c := 0; c < 3; c++ { // B, G, R
			d := int(got[i+c]) - int(want[i+c])
			if d < 0 {
				d = -d
			}
			hist[c][d]++
			sq[c] += float64(d * d)
			s.max[c] = max(s.max[c], d)
			diff = diff || d != 0
		}
		if diff {
			s.changed++
			if !inTextRows(i/4/w, level) {
				s.outside++
			}
		}
	}
	for c := 0; c < 3; c++ {
		s.rms[c] = math.Sqrt(sq[c] / float64(s.n))
		acc, lim := 0, int(math.Ceil(0.999*float64(s.n)))
		for d := 0; d < 256; d++ {
			acc += hist[c][d]
			if acc >= lim {
				s.p999[c] = d
				break
			}
		}
	}
	return s
}

// TestGoldenLive compares the four rebuilt pictures with the in-game confirmed files.
func TestGoldenLive(t *testing.T) {
	live := testref.Dir(t, "FEAR_REF_LIVEMOD")
	out := buildFromRefs(t)
	for i := 1; i <= 4; i++ {
		want, err := os.ReadFile(filepath.Join(live, "Interface", "IA", fmt.Sprintf("IA_Load%d.dds", i)))
		if err != nil {
			t.Skipf("live IA_Load%d.dds: %v", i, err)
		}
		if sum := sha256.Sum256(want); hex.EncodeToString(sum[:]) != liveSHA256[i-1] {
			t.Fatalf("live IA_Load%d.dds is not the confirmed file", i)
		}
		got := out[OutputPath(i)]
		if len(got) != len(want) || !bytes.Equal(got[:128], want[:128]) {
			t.Fatalf("IA_Load%d: size %d / header differ from the live file", i, len(got))
		}
		sum := sha256.Sum256(got)
		if hex.EncodeToString(sum[:]) == liveSHA256[i-1] {
			t.Logf("IA_Load%d: byte-identical to the live file", i)
			continue
		}
		for lv, r := range [][2]int{{128, 128 + 1024*1024*4}, {128 + 1024*1024*4, len(want)}} {
			s := compareLevel(got[r[0]:r[1]], want[r[0]:r[1]], lv)
			share := float64(s.changed) / float64(s.n)
			t.Logf("IA_Load%d mip %d: rms B/G/R %.3f/%.3f/%.3f  p99.9 %v  max %v  pixels differing %d (%.3f%%)",
				i, lv, s.rms[0], s.rms[1], s.rms[2], s.p999, s.max, s.changed, 100*share)
			for c := 0; c < 3; c++ {
				if s.rms[c] > tolRMS || s.p999[c] > tolP999 || s.max[c] > tolMax {
					t.Errorf("IA_Load%d mip %d channel %d outside tolerance", i, lv, c)
				}
			}
			if s.outside != 0 {
				t.Errorf("IA_Load%d mip %d: %d pixels differ outside the text rows", i, lv, s.outside)
			}
			if share > tolShare {
				t.Errorf("IA_Load%d mip %d: %.3f%% of pixels differ", i, lv, 100*share)
			}
			// Outside the text the picture must be exact: alpha is always 255.
			for p := r[0] + 3; p < r[1]; p += 4 {
				if got[p] != 255 {
					t.Fatalf("IA_Load%d: alpha not 255", i)
				}
			}
		}
	}
}

// TestDeterministic builds twice and compares.
func TestDeterministic(t *testing.T) {
	a := buildFromRefs(t)
	b := buildFromRefs(t)
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) != 4 {
		t.Fatalf("got %d files", len(keys))
	}
	for _, k := range keys {
		if !bytes.Equal(a[k], b[k]) {
			t.Errorf("%s differs between builds", k)
		}
	}
}
