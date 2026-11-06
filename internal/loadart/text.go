package loadart

// Text layout and drawing as the recipe's Pillow 12.1.1 did it (ImageFont.truetype + ImageDraw.text, basic
// layout because Pillow was built without raqm): FreeType sizes the face to an integer ppem, each glyph is
// loaded with FT_LOAD_DEFAULT, the pen advances by the glyph's rounded advance, and the old 'kern' table is
// applied the way Pillow's text_layout_fallback does it (see Face.layout). Pillow is under the MIT-CMU
// (HPND) licence; only its rules are reproduced here, no code is copied.

// Face is a font at one pixel size.
type Face struct {
	f     *Font
	ppem  int
	scale int64 // 16.16: 26.6 pixels per font unit (FT_DivFix(ppem*64, unitsPerEm))
	cache map[int]*glyph
}

type glyph struct {
	bm      *Bitmap
	advance int64 // 26.6, rounded to whole pixels (hinted TrueType horiAdvance)
	yMax    int64 // 26.6, highest outline point
}

// NewFace sizes a font to ppem pixels (Pillow's size argument; the fonts used have head flag 3 "integer
// ppem", so FreeType's scale is exactly ppem*64/unitsPerEm).
func NewFace(f *Font, ppem int) *Face {
	return &Face{f: f, ppem: ppem, scale: divFix(int64(ppem)*64, int64(f.upem)), cache: map[int]*glyph{}}
}

// Ascender returns FreeType's size metric ascender in whole pixels (scaled hhea ascender rounded up, as
// FreeType's scalable size metrics are), the line that Pillow's default anchor "la" puts at the given y.
func (fc *Face) Ascender() int {
	return int((mulFix(int64(fc.f.ascender), fc.scale) + 63) >> 6)
}

func pixRound(v int64) int64 { return (v + 32) &^ 63 }
func pixel(v int64) int64    { return (v + 32) >> 6 } // Pillow's PIXEL(): 26.6 to the nearest pixel

func (fc *Face) glyph(g int) (*glyph, error) {
	if gl, ok := fc.cache[g]; ok {
		return gl, nil
	}
	o, err := fc.f.LoadOutline(g)
	if err != nil {
		return nil, err
	}
	// Phantom point pp1 (x = xMin - lsb) is rounded to the pixel grid and the glyph moved with it; the
	// advance is the distance of the rounded phantom points (FreeType ttgload.c, hinted load).
	pp1 := mulFix(int64(o.XMin-fc.f.lsbUnits(g)), fc.scale)
	pp2 := mulFix(int64(o.XMin-fc.f.lsbUnits(g)+fc.f.AdvanceUnits(g)), fc.scale)
	shift := pixRound(pp1) - pp1
	p := make([]pt, len(o.X))
	for i := range o.X {
		p[i] = pt{mulFix(int64(o.X[i]), fc.scale) + shift, mulFix(int64(o.Y[i]), fc.scale)}
	}
	gl := &glyph{bm: rasterize(p, o.On, o.Ends), advance: pixRound(pp2) - pixRound(pp1)}
	for i, q := range p {
		if i == 0 || q.y > gl.yMax {
			gl.yMax = q.y
		}
	}
	fc.cache[g] = gl
	return gl, nil
}

// placed is one glyph bitmap at its pixel position relative to the text origin (x right, y down from the
// ascender line).
type placed struct {
	bm   *Bitmap
	x, y int
	adv  int64 // 26.6 pen advance after this glyph (kerning included)
}

// layout positions the glyphs of s as Pillow's basic layout and font_render do: the pen runs in 26.6, each
// glyph sits at PIXEL(pen) + bitmap_left, its top row at ascender - bitmap_top. Kerning: Pillow adds
// PIXEL(kern) - the kern distance in whole pixels - to the previous glyph's 26.6 advance, i.e. 1/64 pixel per
// pixel of kerning (its long-standing basic-layout behaviour, kept because the recipe ran with it).
func (fc *Face) layout(s string) ([]placed, error) {
	var out []placed
	var pen int64
	asc := fc.Ascender()
	var gs []int
	for _, r := range s {
		gs = append(gs, fc.f.GlyphIndex(r))
	}
	adv := make([]int64, len(gs))
	for i, g := range gs {
		gl, err := fc.glyph(g)
		if err != nil {
			return nil, err
		}
		if i > 0 && fc.f.HasKerning() && gs[i-1] != 0 && g != 0 {
			k := pixRound(mulFix(int64(fc.f.KernUnits(gs[i-1], g)), fc.scale)) // FT_KERNING_DEFAULT
			adv[i-1] += pixel(k)
		}
		adv[i] = gl.advance
	}
	for i, g := range gs {
		gl, _ := fc.glyph(g)
		px := int(pixel(pen))
		out = append(out, placed{gl.bm, px + gl.bm.Left, asc - gl.bm.Top, adv[i]})
		pen += adv[i]
	}
	return out, nil
}

// FirstInkOffset returns Pillow's getbbox(first character)[:2]: (min(0, left), ascender - max(0, top)).
// Pillow's top is the ceiling of the HINTED outline; hinting is not run here, so the glyph's highest outline
// point is rounded to the nearest pixel instead, which is roughly where TrueType hinting puts a flat cap or
// x-height edge. This is an approximation, chosen because it measured closer to the confirmed
// files (mip 0 rms 3.5 -> 2.1); with ceil() the whole Arial line sat one pixel lower than Pillow's.
func (fc *Face) FirstInkOffset(s string) (int, int, error) {
	for _, r := range s {
		gl, err := fc.glyph(fc.f.GlyphIndex(r))
		if err != nil {
			return 0, 0, err
		}
		x0, top := min(0, gl.bm.Left), max(0, int(pixel(gl.yMax)))
		if gl.bm.W == 0 || gl.bm.H == 0 {
			x0, top = 0, 0
		}
		return x0, fc.Ascender() - top, nil
	}
	return 0, 0, nil
}
