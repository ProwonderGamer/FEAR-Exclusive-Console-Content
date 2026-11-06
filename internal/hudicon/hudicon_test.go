package hudicon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/imgops"
)

// Golden: the in-game confirmed icons. The SM15 icons were made from the Xbox 360 art, the
// Watson (autoshot) icons from the PS3 art; each rebuilds byte for byte from that console's copy.
var golden = map[string]struct{ console, sha string }{
	"sm15":      {"x360", "03a82def745805eb3ee252316acf1e7344a9c748af487f46cf94e9a67fc2c8d5"},
	"sm15X":     {"x360", "63541b5eae62e4dba1c56b36cb9a7ba33c95767e1bf863befed1c9335aae67bc"},
	"dualsm15":  {"x360", "f0cc6059b9878b57d3ba9f931f5a484d1c6c846728e209b636f7316d737a510b"},
	"dualsm15X": {"x360", "9219a8e5100de3382bc08b736118c2423288bd91a4c347e2ec2e9d642ccdc65b"},
	"autoshot":  {"ps3", "354524354146511961f73c57cab86dd0a228a51aac2e1bc810c4b0446f15e635"},
	"autoshotX": {"ps3", "ac3eb2414bf1f3ba3e9908d5ce3d37588e9a3a42edf657cfe106432af03abdfa"},
}

func consoleCatalog(t *testing.T, console string) *catalog.Catalog {
	env, rels := "FEAR_REF_X360", []string{filepath.Join("Game", "uberCatalog.cat"), "uberCatalog.cat"}
	if console == "ps3" {
		env, rels = "FEAR_REF_PS3", []string{"ubercatalog.cat"}
	}
	v := os.Getenv(env)
	if v == "" {
		t.Skipf("%s not set (the %s copy is needed for this golden test)", env, console)
	}
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(v, rel)); err == nil {
			c, err := catalog.Open(filepath.Join(v, rel))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.Close() })
			return c
		}
	}
	t.Skipf("%s=%q holds no uberCatalog.cat", env, v)
	return nil
}

func TestGolden(t *testing.T) {
	for _, n := range Names {
		t.Run(n, func(t *testing.T) {
			g := golden[n]
			c := consoleCatalog(t, g.console)
			src, err := c.ReadByName(`Interface\hud\weapon\` + n + ".tex")
			if err != nil {
				t.Fatal(err)
			}
			out, err := Build(src, g.console == "ps3")
			if err != nil {
				t.Fatal(err)
			}
			s := sha256.Sum256(out)
			if h := hex.EncodeToString(s[:]); h != g.sha || len(out) != FileSize {
				t.Fatalf("%s from %s: %s (%d bytes), want %s", n, g.console, h, len(out), g.sha)
			}
			// The point of the icon fix: nothing drawn outside the stock icon area (x0-95, y0-47).
			lv, err := DecodeDDS(out)
			if err != nil {
				t.Fatal(err)
			}
			for y := 0; y < height; y++ {
				for x := 0; x < width; x++ {
					if (x >= artW || y >= artH) && lv[0].Pix[(y*width+x)*4+3] != 0 {
						t.Fatalf("alpha at (%d,%d), outside the 96x48 icon area", x, y)
					}
				}
			}
		})
	}
}

// TestOtherConsoleDiffers: each icon is built from the console its golden came from; the other console's
// art (the same picture, compressed differently by the console tools) gives other bytes.
func TestOtherConsoleDiffers(t *testing.T) {
	c := consoleCatalog(t, "ps3")
	src, err := c.ReadByName(`Interface\hud\weapon\sm15.tex`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Build(src, true)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(out)
	if hex.EncodeToString(s[:]) == golden["sm15"].sha {
		t.Fatal("PS3 sm15 gives the confirmed bytes too: the console choice in content/assets.go can be relaxed")
	}
}

func TestDeterministic(t *testing.T) {
	src := imgops.New(width, height)
	for i := range src.Pix {
		src.Pix[i] = byte(i * 7 % 251)
	}
	a, err := Levels(src)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Levels(src)
	for i := range a {
		if !bytes.Equal(imgops.EncodeDXT5(a[i]), imgops.EncodeDXT5(b[i])) {
			t.Fatal("not deterministic")
		}
	}
	if len(a) != mipLevels || a[mipLevels-1].W != 1 || a[mipLevels-1].H != 1 {
		t.Fatal("mip chain")
	}
}
