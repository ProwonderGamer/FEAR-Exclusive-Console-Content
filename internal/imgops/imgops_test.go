package imgops

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// synth returns a deterministic test picture with every alpha value, hard edges and smooth ramps.
func synth(w, h int, seed uint32) *RGBA {
	m := New(w, h)
	s := seed
	next := func() byte { s = s*1664525 + 1013904223; return byte(s >> 24) }
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := (y*w + x) * 4
			switch {
			case x < w/3: // noise
				m.Pix[o], m.Pix[o+1], m.Pix[o+2], m.Pix[o+3] = next(), next(), next(), next()
			case x < 2*w/3: // ramps
				m.Pix[o], m.Pix[o+1], m.Pix[o+2], m.Pix[o+3] = byte(x*7), byte(y*5), byte((x+y)*3), byte(y*255/max(1, h-1))
			default: // blocks with hard edges, some fully transparent
				v := byte(0)
				if (x/5+y/7)%2 == 0 {
					v = 255
				}
				m.Pix[o], m.Pix[o+1], m.Pix[o+2], m.Pix[o+3] = v, 255-v, 128, v
			}
		}
	}
	return m
}

func hashOf(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// Golden hashes from Pillow 12.1.1 on the same synthetic pictures (FEAR_IMGOPS_DUMP writes the inputs; the
// Python side is testdata/pillow_check.py): Image.frombytes('RGBA', ...) then the named call.
var pillowGold = map[string]string{
	"lanczos_down":   "86c91c0cf5bcf06865dabd78c0a7ee5236d9d3d5acac0a9cf8e23f342af2f6cb",
	"lanczos_up":     "aba61d825ae792a839eb87f9646bc946c13df328254d7d579a5acd1ee0f74bfb",
	"box_half":       "fdf8f7e55a561013111330802eff859c2c4f1933a4de5cf7ae4aa1785e8c327e",
	"premul_lanczos": "8a40d7b153b8173700d4240029523dcdd73e8e0e0de92d468c97083a0625ed21",
	"composite":      "29a5b63866b9a78402e17491f319057ab14ada21bca85c3b287140df67089770",
}

func pillowCases() map[string][]byte {
	a := synth(128, 64, 1)
	b := synth(97, 61, 2)
	c, _ := AlphaComposite(synth(40, 30, 3), synth(40, 30, 4))
	return map[string][]byte{
		"lanczos_down":   Resize(a, 96, 48, Lanczos).Pix,
		"lanczos_up":     Resize(b, 150, 200, Lanczos).Pix,
		"box_half":       Resize(a, 64, 32, Box).Pix,
		"premul_lanczos": ResampleRaw(a, 77, 33, Lanczos).Pix,
		"composite":      c.Pix,
	}
}

func TestMatchesPillow(t *testing.T) {
	if d := os.Getenv("FEAR_IMGOPS_DUMP"); d != "" {
		for name, m := range map[string]*RGBA{"a": synth(128, 64, 1), "b": synth(97, 61, 2), "c3": synth(40, 30, 3), "c4": synth(40, 30, 4)} {
			if err := os.WriteFile(filepath.Join(d, name+".rgba"), m.Pix, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for name, p := range pillowCases() {
			t.Logf("%s %s", name, hashOf(p))
			if err := os.WriteFile(filepath.Join(d, "go_"+name+".rgba"), p, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, p := range pillowCases() {
		if h := hashOf(p); h != pillowGold[name] {
			t.Errorf("%s: %s, Pillow gives %s", name, h, pillowGold[name])
		}
	}
}

func TestDXT5RoundTrip(t *testing.T) {
	for _, sz := range [][2]int{{128, 64}, {8, 8}, {2, 1}, {1, 1}} {
		a := synth(sz[0], sz[1], 5)
		enc := EncodeDXT5(a)
		if want := max(1, (sz[0]+3)/4) * max(1, (sz[1]+3)/4) * 16; len(enc) != want {
			t.Fatalf("%v: %d bytes, want %d", sz, len(enc), want)
		}
		d, err := DecodeDXT5(enc, sz[0], sz[1])
		if err != nil {
			t.Fatal(err)
		}
		if d.W != sz[0] || d.H != sz[1] {
			t.Fatalf("%v decoded as %dx%d", sz, d.W, d.H)
		}
	}
	// A smooth picture survives with a small error. Each block holds a 2-D gradient (red across, green down)
	// that a 4-colour line can only approximate: about half the block's span, up to 10 here.
	s := New(128, 64)
	for y := 0; y < 64; y++ {
		for x := 0; x < 128; x++ {
			copy(s.Pix[(y*128+x)*4:], []byte{byte(x * 2), byte(y * 4), 128, byte(255 - x)})
		}
	}
	d, _ := DecodeDXT5(EncodeDXT5(s), 128, 64)
	worst := 0
	for i := range s.Pix {
		worst = max(worst, abs(int(d.Pix[i])-int(s.Pix[i])))
	}
	if worst > 12 {
		t.Errorf("smooth picture error %d", worst)
	}
	// A flat opaque block is exact; a transparent one stays transparent.
	f := New(4, 4)
	for i := 0; i < 16; i++ {
		copy(f.Pix[i*4:], []byte{255, 0, 0, 255})
	}
	d, _ = DecodeDXT5(EncodeDXT5(f), 4, 4)
	if string(d.Pix) != string(f.Pix) {
		t.Errorf("flat red block %v", d.Pix[:4])
	}
	z := New(4, 4)
	d, _ = DecodeDXT5(EncodeDXT5(z), 4, 4)
	for i := 3; i < 64; i += 4 {
		if d.Pix[i] != 0 {
			t.Fatal("transparent block gained alpha")
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
