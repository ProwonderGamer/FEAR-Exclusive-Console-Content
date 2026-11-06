package loadart

// A minimal TrueType reader: just what the loading-picture text needs (cmap format 4, loca/glyf outlines with
// simple and offset-only composite glyphs, hmtx advances, hhea ascender, the old-style 'kern' table format 0).
// Written from the OpenType specification (tables cmap, head, hhea, hmtx, loca, glyf, kern, maxp); no font
// library is used. Coordinates are scaled to 26.6 fixed point with FreeType's FT_MulFix rule, because the
// recipe rendered its text with Pillow's bundled FreeType and the port must land on the same pixels.

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var be = binary.BigEndian

// Font is a parsed TrueType font.
type Font struct {
	data       []byte
	tables     map[string][]byte
	upem       int
	locaLong   bool
	numGlyphs  int
	numHMetric int
	ascender   int    // hhea ascender, font units
	descender  int    // hhea descender, font units (negative)
	cmap       []byte // the chosen format 4 subtable
	kern       map[uint32]int16
}

// ParseFont reads the tables of a TrueType (glyf) font.
func ParseFont(b []byte) (*Font, error) {
	if len(b) < 12 {
		return nil, errors.New("loadart: font too short")
	}
	if v := be.Uint32(b); v != 0x00010000 && v != 0x74727565 {
		return nil, errors.New("loadart: not a TrueType font")
	}
	n := int(be.Uint16(b[4:]))
	if 12+16*n > len(b) {
		return nil, errors.New("loadart: font table directory truncated")
	}
	f := &Font{data: b, tables: map[string][]byte{}}
	for i := 0; i < n; i++ {
		r := b[12+16*i:]
		off, ln := int(be.Uint32(r[8:])), int(be.Uint32(r[12:]))
		if off < 0 || ln < 0 || off+ln > len(b) {
			return nil, fmt.Errorf("loadart: font table %q out of range", r[:4])
		}
		f.tables[string(r[:4])] = b[off : off+ln]
	}
	for _, t := range []string{"head", "hhea", "hmtx", "loca", "glyf", "cmap", "maxp"} {
		if f.tables[t] == nil {
			return nil, fmt.Errorf("loadart: font has no %s table", t)
		}
	}
	head, hhea, maxp := f.tables["head"], f.tables["hhea"], f.tables["maxp"]
	if len(head) < 54 || len(hhea) < 36 || len(maxp) < 6 {
		return nil, errors.New("loadart: font header tables truncated")
	}
	f.upem = int(be.Uint16(head[18:]))
	f.locaLong = be.Uint16(head[50:]) == 1
	f.numGlyphs = int(be.Uint16(maxp[4:]))
	f.ascender = int(int16(be.Uint16(hhea[4:])))
	f.descender = int(int16(be.Uint16(hhea[6:])))
	f.numHMetric = int(be.Uint16(hhea[34:]))
	if f.upem == 0 || f.numHMetric == 0 || len(f.tables["hmtx"]) < 4*f.numHMetric {
		return nil, errors.New("loadart: bad font metrics")
	}
	if err := f.pickCmap(); err != nil {
		return nil, err
	}
	f.readKern()
	return f, nil
}

// pickCmap chooses the Windows Unicode BMP (3,1) format 4 subtable, else a (0,x) one.
func (f *Font) pickCmap() error {
	c := f.tables["cmap"]
	if len(c) < 4 {
		return errors.New("loadart: cmap truncated")
	}
	n := int(be.Uint16(c[2:]))
	var best []byte
	for i := 0; i < n && 4+8*i+8 <= len(c); i++ {
		r := c[4+8*i:]
		pid, eid, off := be.Uint16(r), be.Uint16(r[2:]), int(be.Uint32(r[4:]))
		if off+4 > len(c) || be.Uint16(c[off:]) != 4 {
			continue
		}
		ln := int(be.Uint16(c[off+2:]))
		if off+ln > len(c) {
			continue
		}
		sub := c[off : off+ln]
		if pid == 3 && (eid == 1 || eid == 0) {
			best = sub
			break
		}
		if pid == 0 && best == nil {
			best = sub
		}
	}
	if best == nil {
		return errors.New("loadart: font has no Unicode format 4 cmap")
	}
	f.cmap = best
	return nil
}

// GlyphIndex maps a BMP character to its glyph (0 = missing), cmap format 4.
func (f *Font) GlyphIndex(r rune) int {
	c := f.cmap
	if r < 0 || r > 0xffff || len(c) < 14 {
		return 0
	}
	segX2 := int(be.Uint16(c[6:]))
	ends, starts := 14, 14+segX2+2
	deltas, ranges := starts+segX2, starts+2*segX2
	if ranges+segX2 > len(c) {
		return 0
	}
	for s := 0; s < segX2; s += 2 {
		end := rune(be.Uint16(c[ends+s:]))
		if r > end {
			continue
		}
		start := rune(be.Uint16(c[starts+s:]))
		if r < start {
			return 0
		}
		delta := int(be.Uint16(c[deltas+s:]))
		ro := int(be.Uint16(c[ranges+s:]))
		if ro == 0 {
			return (int(r) + delta) & 0xffff
		}
		p := ranges + s + ro + 2*int(r-start)
		if p+2 > len(c) {
			return 0
		}
		g := int(be.Uint16(c[p:]))
		if g == 0 {
			return 0
		}
		return (g + delta) & 0xffff
	}
	return 0
}

// readKern loads the horizontal pairs of a version 0 'kern' table, format 0 subtables (the table FreeType's
// FT_Get_Kerning reads; GPOS is not used by Pillow's basic layout).
func (f *Font) readKern() {
	k := f.tables["kern"]
	if len(k) < 4 || be.Uint16(k) != 0 {
		return
	}
	f.kern = map[uint32]int16{}
	n, p := int(be.Uint16(k[2:])), 4
	for i := 0; i < n && p+6 <= len(k); i++ {
		ln, cov := int(be.Uint16(k[p+2:])), be.Uint16(k[p+4:])
		if cov>>8 == 0 && cov&1 == 1 && p+14 <= len(k) { // format 0, horizontal
			np := int(be.Uint16(k[p+6:]))
			for j := 0; j < np && p+14+6*j+6 <= len(k); j++ {
				r := k[p+14+6*j:]
				key := uint32(be.Uint16(r))<<16 | uint32(be.Uint16(r[2:]))
				if _, dup := f.kern[key]; !dup { // FreeType binary-searches one table; first wins here
					f.kern[key] = int16(be.Uint16(r[4:]))
				}
			}
		}
		if ln < 6 {
			break
		}
		p += ln
	}
}

// HasKerning reports whether the font has a 'kern' table (FreeType's FT_HAS_KERNING).
func (f *Font) HasKerning() bool { return f.kern != nil }

// KernUnits returns the kern pair value in font units.
func (f *Font) KernUnits(l, r int) int { return int(f.kern[uint32(l)<<16|uint32(r)]) }

// AdvanceUnits returns the hmtx advance width of a glyph in font units.
func (f *Font) AdvanceUnits(g int) int {
	h := f.tables["hmtx"]
	if g >= f.numHMetric {
		g = f.numHMetric - 1
	}
	return int(be.Uint16(h[4*g:]))
}

// lsbUnits returns the hmtx left side bearing of a glyph in font units.
func (f *Font) lsbUnits(g int) int {
	h := f.tables["hmtx"]
	if g < f.numHMetric {
		return int(int16(be.Uint16(h[4*g+2:])))
	}
	p := 4*f.numHMetric + 2*(g-f.numHMetric)
	if p+2 > len(h) {
		return 0
	}
	return int(int16(be.Uint16(h[p:])))
}

// glyphData returns the glyf record of a glyph (nil for an empty glyph).
func (f *Font) glyphData(g int) ([]byte, error) {
	if g < 0 || g >= f.numGlyphs {
		return nil, fmt.Errorf("loadart: glyph %d out of range", g)
	}
	l := f.tables["loca"]
	var a, b int
	if f.locaLong {
		if 4*g+8 > len(l) {
			return nil, errors.New("loadart: loca truncated")
		}
		a, b = int(be.Uint32(l[4*g:])), int(be.Uint32(l[4*g+4:]))
	} else {
		if 2*g+4 > len(l) {
			return nil, errors.New("loadart: loca truncated")
		}
		a, b = 2*int(be.Uint16(l[2*g:])), 2*int(be.Uint16(l[2*g+2:]))
	}
	gl := f.tables["glyf"]
	if a > b || b > len(gl) {
		return nil, fmt.Errorf("loadart: glyph %d out of glyf", g)
	}
	if a == b {
		return nil, nil
	}
	return gl[a:b], nil
}

// Outline is a glyph outline in font units: points, on-curve flags and contour ends (TrueType order).
type Outline struct {
	X, Y  []int
	On    []bool
	Ends  []int // index of the last point of each contour
	XMin  int   // glyph header bounding box xMin (font units)
	Instr []byte
}

// LoadOutline reads a glyph's outline in font units. Composite glyphs are flattened when their components only
// carry offsets (ARGS_ARE_XY_VALUES without scale); other composites are refused.
func (f *Font) LoadOutline(g int) (*Outline, error) { return f.loadOutline(g, 0) }

func (f *Font) loadOutline(g, depth int) (*Outline, error) {
	if depth > 4 {
		return nil, errors.New("loadart: composite glyph nesting too deep")
	}
	d, err := f.glyphData(g)
	if err != nil || d == nil {
		return &Outline{}, err
	}
	if len(d) < 10 {
		return nil, errors.New("loadart: glyph header truncated")
	}
	nc := int(int16(be.Uint16(d)))
	xmin := int(int16(be.Uint16(d[2:])))
	if nc < 0 {
		return f.loadComposite(d, xmin, depth)
	}
	o := &Outline{XMin: xmin}
	p := 10
	if p+2*nc+2 > len(d) {
		return nil, errors.New("loadart: glyph contours truncated")
	}
	np := 0
	for i := 0; i < nc; i++ {
		e := int(be.Uint16(d[p+2*i:]))
		if e < np-1 || (i > 0 && e <= o.Ends[i-1]) {
			return nil, errors.New("loadart: bad contour ends")
		}
		o.Ends = append(o.Ends, e)
		np = e + 1
	}
	p += 2 * nc
	il := int(be.Uint16(d[p:]))
	p += 2
	if p+il > len(d) {
		return nil, errors.New("loadart: glyph instructions truncated")
	}
	o.Instr = d[p : p+il]
	p += il
	flags := make([]byte, np)
	for i := 0; i < np; {
		if p >= len(d) {
			return nil, errors.New("loadart: glyph flags truncated")
		}
		fl := d[p]
		p++
		flags[i] = fl
		i++
		if fl&8 != 0 {
			if p >= len(d) {
				return nil, errors.New("loadart: glyph flags truncated")
			}
			rep := int(d[p])
			p++
			for ; rep > 0 && i < np; rep-- {
				flags[i] = fl
				i++
			}
		}
	}
	o.X, o.Y, o.On = make([]int, np), make([]int, np), make([]bool, np)
	for axis := 0; axis < 2; axis++ {
		short, same := byte(2), byte(16)
		dst := o.X
		if axis == 1 {
			short, same, dst = 4, 32, o.Y
		}
		v := 0
		for i := 0; i < np; i++ {
			fl := flags[i]
			switch {
			case fl&short != 0:
				if p >= len(d) {
					return nil, errors.New("loadart: glyph coordinates truncated")
				}
				if fl&same != 0 {
					v += int(d[p])
				} else {
					v -= int(d[p])
				}
				p++
			case fl&same == 0:
				if p+2 > len(d) {
					return nil, errors.New("loadart: glyph coordinates truncated")
				}
				v += int(int16(be.Uint16(d[p:])))
				p += 2
			}
			dst[i] = v
		}
	}
	for i, fl := range flags {
		o.On[i] = fl&1 != 0
	}
	return o, nil
}

func (f *Font) loadComposite(d []byte, xmin, depth int) (*Outline, error) {
	o := &Outline{XMin: xmin}
	p := 10
	for {
		if p+4 > len(d) {
			return nil, errors.New("loadart: composite glyph truncated")
		}
		fl, gi := be.Uint16(d[p:]), int(be.Uint16(d[p+2:]))
		p += 4
		var dx, dy int
		if fl&1 != 0 { // ARG_1_AND_2_ARE_WORDS
			if p+4 > len(d) {
				return nil, errors.New("loadart: composite glyph truncated")
			}
			dx, dy = int(int16(be.Uint16(d[p:]))), int(int16(be.Uint16(d[p+2:])))
			p += 4
		} else {
			if p+2 > len(d) {
				return nil, errors.New("loadart: composite glyph truncated")
			}
			dx, dy = int(int8(d[p])), int(int8(d[p+1]))
			p += 2
		}
		if fl&2 == 0 || fl&(8|0x40|0x80) != 0 {
			return nil, errors.New("loadart: composite glyph with point matching or scale is not supported")
		}
		c, err := f.loadOutline(gi, depth+1)
		if err != nil {
			return nil, err
		}
		base := len(o.X)
		for i := range c.X {
			o.X = append(o.X, c.X[i]+dx)
			o.Y = append(o.Y, c.Y[i]+dy)
			o.On = append(o.On, c.On[i])
		}
		for _, e := range c.Ends {
			o.Ends = append(o.Ends, e+base)
		}
		if fl&0x20 == 0 { // MORE_COMPONENTS
			break
		}
	}
	return o, nil
}

// mulFix is FreeType's FT_MulFix: (a*b + 0x8000) >> 16 on magnitudes, sign restored (rounds half away from 0).
func mulFix(a, b int64) int64 {
	s := int64(1)
	if a < 0 {
		a, s = -a, -s
	}
	if b < 0 {
		b, s = -b, -s
	}
	return s * ((a*b + 0x8000) >> 16)
}

// divFix is FreeType's FT_DivFix: (a << 16) / b rounded half away from zero.
func divFix(a, b int64) int64 {
	s := int64(1)
	if a < 0 {
		a, s = -a, -s
	}
	if b < 0 {
		b, s = -b, -s
	}
	if b == 0 {
		return s * 0x7fffffff
	}
	return s * (((a << 16) + (b >> 1)) / b)
}
