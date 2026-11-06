package loadart

import (
	"encoding/binary"
	"os"
	"testing"

	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/testref"
)

// synthFont builds a minimal TrueType font in memory: glyph 0 empty, glyph 1 a 1000x1000-unit square (char
// 'A'), glyph 2 a quadratic "diamond" made of on-curve edge midpoints and off-curve corners (char 'B'),
// glyph 3 a composite of glyph 1 moved by (100, 50) (char 'C'). Units per em 2048, ascender 1900.
func synthFont() []byte {
	b16 := func(v ...int) []byte {
		o := make([]byte, 2*len(v))
		for i, x := range v {
			binary.BigEndian.PutUint16(o[2*i:], uint16(int16(x)))
		}
		return o
	}
	cat := func(p ...[]byte) []byte {
		var o []byte
		for _, x := range p {
			o = append(o, x...)
		}
		return o
	}
	// glyph 1: contour (0,0)->(0,1000)->(1000,1000)->(1000,0), all on-curve, word coordinates
	g1 := cat(b16(1, 0, 0, 1000, 1000), b16(3), b16(0), []byte{1, 1, 1, 1}, b16(0, 0, 1000, 0), b16(0, 1000, 0, -1000))
	// glyph 2: on (500,0), off (1000,0), on (1000,500), off (1000,1000), on (500,1000), off (0,1000), on (0,500), off (0,0)
	g2 := cat(b16(1, 0, 0, 1000, 1000), b16(7), b16(0), []byte{1, 0, 1, 0, 1, 0, 1, 0},
		b16(500, 500, 0, 0, -500, -500, 0, 0), b16(0, 0, 500, 500, 0, 0, -500, -500))
	// glyph 3: composite, ARG_1_AND_2_ARE_WORDS | ARGS_ARE_XY_VALUES
	g3 := cat(b16(-1, 100, 50, 1100, 1050), b16(0x0003, 1, 100, 50))
	pad := func(g []byte) []byte {
		if len(g)%2 == 1 {
			g = append(g, 0)
		}
		return g
	}
	g1, g2, g3 = pad(g1), pad(g2), pad(g3)
	glyf := cat(g1, g2, g3)
	loca := b16(0, 0, len(g1)/2, (len(g1)+len(g2))/2, len(glyf)/2)
	head := make([]byte, 54)
	binary.BigEndian.PutUint32(head, 0x00010000)
	binary.BigEndian.PutUint16(head[16:], 9)
	binary.BigEndian.PutUint16(head[18:], 2048)
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[4:], 1900)
	binary.BigEndian.PutUint16(hhea[6:], uint16(0x10000-400))
	binary.BigEndian.PutUint16(hhea[34:], 4)
	maxp := cat(b16(0, 0x5000), b16(4))
	hmtx := b16(500, 0, 1200, 0, 1100, 0, 1300, 100)
	// cmap format 4: segment 'A'..'C' -> 1..3 (idDelta 1-'A'), then the 0xFFFF end segment
	sub := cat(b16(4, 32, 0, 4, 4, 1, 0), b16('C', 0xFFFF), b16(0), b16('A', 0xFFFF), b16(1-'A', 1), b16(0, 0))
	cmap := cat(b16(0, 1), b16(3, 1), []byte{0, 0, 0, 12}, sub)
	kern := cat(b16(0, 1), b16(0, 6+8+6, 1), b16(1, 0, 0, 0), b16(1, 2, -205))
	tables := []struct {
		tag  string
		data []byte
	}{{"cmap", cmap}, {"glyf", glyf}, {"head", head}, {"hhea", hhea}, {"hmtx", hmtx}, {"kern", kern}, {"loca", loca}, {"maxp", maxp}}
	out := cat([]byte{0, 1, 0, 0}, b16(len(tables), 0, 0, 0))
	off := 12 + 16*len(tables)
	var body []byte
	for _, t := range tables {
		e := make([]byte, 16)
		copy(e, t.tag)
		binary.BigEndian.PutUint32(e[8:], uint32(off+len(body)))
		binary.BigEndian.PutUint32(e[12:], uint32(len(t.data)))
		out = append(out, e...)
		body = append(body, t.data...)
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
	}
	return append(out, body...)
}

func TestFontReader(t *testing.T) {
	f, err := ParseFont(synthFont())
	if err != nil {
		t.Fatal(err)
	}
	for r, want := range map[rune]int{'A': 1, 'B': 2, 'C': 3, 'D': 0, ' ': 0} {
		if g := f.GlyphIndex(r); g != want {
			t.Errorf("GlyphIndex(%q) = %d, want %d", r, g, want)
		}
	}
	if f.AdvanceUnits(2) != 1100 || f.lsbUnits(3) != 100 || f.KernUnits(1, 2) != -205 || !f.HasKerning() {
		t.Error("hmtx/kern values wrong")
	}
	o, err := f.LoadOutline(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.X) != 4 || o.X[2] != 1100 || o.Y[2] != 1050 || o.Ends[0] != 3 {
		t.Errorf("composite outline %+v", o)
	}
	o2, err := f.LoadOutline(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(o2.X) != 8 || o2.On[1] || !o2.On[2] || o2.X[1] != 1000 || o2.Y[3] != 1000 {
		t.Errorf("simple outline %+v", o2)
	}
	if _, err := ParseFont([]byte("not a font")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestFixedPoint(t *testing.T) {
	// 38 px at 2048 units/em: FT_DivFix(38*64, 2048) = 77824; 1466 units -> 1740.875 -> 1741 (26.6).
	if v := divFix(38*64, 2048); v != 77824 {
		t.Errorf("divFix = %d", v)
	}
	if v := mulFix(1466, 77824); v != 1741 {
		t.Errorf("mulFix = %d", v)
	}
	if v := mulFix(-1466, 77824); v != -1741 {
		t.Errorf("mulFix negative = %d (rounds half away from zero)", v)
	}
}

func TestRasterCoverage(t *testing.T) {
	// A clockwise (TrueType) square from x 0.25 to 2.25 px, y 0 to 2 px: columns covered 3/4, 1, 1/4.
	on := []bool{true, true, true, true}
	bm := rasterize([]pt{{16, 0}, {16, 128}, {144, 128}, {144, 0}}, on, []int{3})
	if bm.W != 3 || bm.H != 2 || bm.Left != 0 || bm.Top != 2 {
		t.Fatalf("bitmap %dx%d at %d,%d", bm.W, bm.H, bm.Left, bm.Top)
	}
	for row := 0; row < 2; row++ {
		// FreeType maps the doubled area a to a >> 9 (clockwise outlines give positive areas here): 3/4 -> 192.
		if got := bm.Pix[row*3 : row*3+3]; got[0] != 192 || got[1] != 255 || got[2] != 64 {
			t.Errorf("row %d coverage %v", row, got)
		}
	}
	// The same square counter-clockwise: negative area, mapped through ^c (gray_hline): 3/4 -> 191, 1/4 -> 63.
	bm = rasterize([]pt{{16, 0}, {144, 0}, {144, 128}, {16, 128}}, on, []int{3})
	if got := bm.Pix[:3]; got[0] != 191 || got[1] != 255 || got[2] != 63 {
		t.Errorf("ccw coverage %v", got)
	}
	// The conic diamond (1000 units = 125 px at 256 ppem, on whole pixels) is mirror-symmetric within a few levels (the arc flattening runs in contour order, so the two halves round differently).
	f, err := ParseFont(synthFont())
	if err != nil {
		t.Fatal(err)
	}
	gl, err := NewFace(f, 256).glyph(2)
	if err != nil {
		t.Fatal(err)
	}
	b := gl.bm
	if b.W != 125 || b.H != 125 {
		t.Fatalf("diamond bitmap %dx%d", b.W, b.H)
	}
	for y := 0; y < b.H; y++ {
		for x := 0; x < b.W; x++ {
			v, m := int(b.Pix[y*b.W+x]), int(b.Pix[y*b.W+b.W-1-x])
			if v-m < -3 || v-m > 3 {
				t.Fatalf("diamond not mirror-symmetric at %d,%d: %d vs %d", x, y, v, m)
			}
		}
	}
}

func TestBlendRule(t *testing.T) {
	// Values from Pillow 12.1.1: a transparent RGBA layer filled with ink (0,0,0,150) through mask s, then
	// with (227,245,242,255) through mask c (all 65536 (s, c) pairs were checked).
	cases := []struct {
		s, c byte
		want [4]byte
	}{
		{0, 1, [4]byte{227, 245, 242, 1}},
		{0, 128, [4]byte{227, 245, 242, 128}},
		{100, 1, [4]byte{1, 1, 1, 60}},
		{100, 128, [4]byte{114, 123, 121, 157}},
		{255, 128, [4]byte{114, 123, 121, 203}},
		{255, 1, [4]byte{1, 1, 1, 150}},
	}
	for _, k := range cases {
		px := []byte{0, 0, 0, 0}
		fillPixel(px, k.s, shadowInk)
		fillPixel(px, k.c, textInk)
		if [4]byte(px) != k.want {
			t.Errorf("s=%d c=%d: %v, want %v", k.s, k.c, px, k.want)
		}
	}
}

// TestLayoutWidths checks the pen positions (advances + Pillow's kerning rule) against Pillow 12.1.1's
// ImageFont.getlength for the recipe's strings, and the ascender against getmetrics.
// Horizontal positions are exact because FreeType's v40 hinting does not move points horizontally.
func TestLayoutWidths(t *testing.T) {
	ab, err := os.ReadFile(arialBoldPath(t))
	if err != nil {
		t.Fatal(err)
	}
	check := func(fc *Face, s string, want float64) {
		pl, err := fc.layout(s)
		if err != nil {
			t.Fatal(err)
		}
		var pen int64
		for _, p := range pl {
			pen += p.adv
		}
		if got := float64(pen) / 64; got != want {
			t.Errorf("%q: length %v, Pillow %v", s, got, want)
		}
	}
	f, err := ParseFont(ab)
	if err != nil {
		t.Fatal(err)
	}
	help := NewFace(f, 38)
	for s, w := range map[string]float64{"Map 1 - Distribution": 357, "Map 2 - Rooftop": 288,
		"Map 3 - Construction": 379, "Map 4 - Vault": 235.96875, "Loading. . .": 202} {
		check(help, s, w)
	}
	if help.Ascender() != 35 {
		t.Errorf("Arial Bold 38 ascender %d, Pillow 35", help.Ascender())
	}
	fs, _ := pcfs.Open(testref.Dir(t, "FEAR_REF_PCGAME"))
	if fs == nil {
		t.Skip("no PC archives")
	}
	defer fs.Close()
	np, err := fs.Read(`interface\fonts\NEUROPOX.TTF`)
	if err != nil {
		t.Skipf("NEUROPOX.TTF: %v", err)
	}
	nf, err := ParseFont(np)
	if err != nil {
		t.Fatal(err)
	}
	title := NewFace(nf, 89)
	check(title, "Instant Action", 535.90625)
	if title.Ascender() != 57 {
		t.Errorf("NEUROPOX 89 ascender %d, Pillow 57", title.Ascender())
	}
}
