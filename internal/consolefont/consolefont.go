// Package consolefont builds Interface\IA\zSFODD_ConsoleFonts.bin, the console results-page fonts that the
// hook (confont.c) feeds through the engine's glyph path. Port of the project's Python console font builder;
// byte-exact with the confirmed file when built from the Xbox 360 copy (the reference).
//
// Sources: Interface\fonts\Arial_18 and NeuropoX_36 from the console uberCatalog: X360 ".xpr", PS3 ".ps3font"
// (fallback; for the 95 printable ASCII glyphs both have identical glyph records and Arial texels, but 602
// NeuropoX_36 alpha texels differ by one step, so a PS3 build is NOT byte-exact with the reference).
//
// .xpr (XPR2, big endian): USER block at file offset 0x80: u32 version 5, f32 cell height (0x84), f32 outline,
// f32 shadow, f32 base height (0x90); char map u16 at 0x94 + 2*code (glyph = value + 1); glyph count u16 at
// 0x1c0bc; 16-byte entries at 0x1c0be (x0, y0, x1, y1 u16, bearing, width, advance s16, pad); 512x512 u16 atlas
// at 0x1d00c, stored linearly (bits 15..12 alpha, 11..8 fill).
// .ps3font: same fields at cell 0x20, base 0x2c, map 0x30, count 0x1c05c, glyphs 0x1c05e, atlas 0x1cf9e.
//
// Output (little endian): 'ZCF2', u32 font count (2); per font: u32 id (0 Arial_18, 1 NeuropoX_36), f32 cell,
// f32 base height, 95 glyphs for 0x20..0x7E (u16 x0, y0, x1, y1, s16 bearing, s16 advance; missing chars use
// '?'), then 512*512 bytes atlas, byte = alpha << 4 | fill.
package consolefont

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Names are the two fonts, in output order; the catalog entry is `Interface\fonts\<name><ext>`.
var Names = [2]string{"Arial_18", "NeuropoX_36"}

// Console selects the source layout.
type Console int

const (
	X360 Console = iota // .xpr (reference)
	PS3                 // .ps3font
)

// Ext returns the font file extension of the console.
func (c Console) Ext() string {
	if c == PS3 {
		return ".ps3font"
	}
	return ".xpr"
}

// CatalogPath returns the catalog entry of font i (0 or 1).
func (c Console) CatalogPath(i int) string { return `Interface\fonts\` + Names[i] + c.Ext() }

type layout struct{ cell, base, cmap, count, table, atlas int }

var layouts = map[Console]layout{
	X360: {0x84, 0x90, 0x94, 0x1c0bc, 0x1c0be, 0x1d00c},
	PS3:  {0x20, 0x2c, 0x30, 0x1c05c, 0x1c05e, 0x1cf9e},
}

const atlasTexels = 512 * 512

// Build makes zSFODD_ConsoleFonts.bin from the two font files (Arial_18, NeuropoX_36) of one console.
func Build(c Console, arial, neuropox []byte) ([]byte, error) {
	out := []byte("ZCF2")
	out = binary.LittleEndian.AppendUint32(out, 2)
	for i, d := range [][]byte{arial, neuropox} {
		f, err := font(c, d)
		if err != nil {
			return nil, fmt.Errorf("consolefont: %s: %w", Names[i], err)
		}
		out = binary.LittleEndian.AppendUint32(out, uint32(i))
		out = append(out, f...)
	}
	return out, nil
}

func font(c Console, d []byte) ([]byte, error) {
	l, ok := layouts[c]
	if !ok {
		return nil, errors.New("unknown console")
	}
	be := binary.BigEndian
	if len(d) < l.atlas+2*atlasTexels {
		return nil, errors.New("file too short")
	}
	if c == X360 {
		// XPR2 holding exactly the TX2D texture + USER font data this layout expects
		if string(d[:4]) != "XPR2" || be.Uint32(d[12+0x74:]) != 5 || be.Uint32(d[8:]) != 0x80000 || len(d) != l.atlas+0x80000 {
			return nil, errors.New("not the expected XPR2 font layout")
		}
	}
	cell := math.Float32frombits(be.Uint32(d[l.cell:]))
	base := math.Float32frombits(be.Uint32(d[l.base:]))
	if !(8 <= base && base <= cell && cell < 128) {
		return nil, fmt.Errorf("implausible cell %v / base %v", cell, base)
	}
	n := int(be.Uint16(d[l.count:]))
	if l.table+16*n > l.atlas {
		return nil, errors.New("glyph table overlaps the atlas")
	}
	glyph := func(code int) []byte { // nil when missing
		i := int(be.Uint16(d[l.cmap+2*code:])) + 1 // 0xffff + 1 is past any table: missing
		if 0 < i && i < n {
			return d[l.table+16*i : l.table+16*i+16]
		}
		return nil
	}
	q := glyph('?')
	if q == nil {
		return nil, errors.New("no '?' glyph")
	}
	out := binary.LittleEndian.AppendUint32(nil, math.Float32bits(cell))
	out = binary.LittleEndian.AppendUint32(out, math.Float32bits(base))
	for code := 0x20; code < 0x7f; code++ {
		g := glyph(code)
		if g == nil {
			g = q
		}
		// x0, y0, x1, y1, bearing (s16 at 8), advance (s16 at 12); the width at 10 is dropped
		for _, o := range []int{0, 2, 4, 6, 8, 12} {
			out = binary.LittleEndian.AppendUint16(out, be.Uint16(g[o:]))
		}
	}
	for i := 0; i < atlasTexels; i++ {
		v := be.Uint16(d[l.atlas+2*i:])
		out = append(out, byte((v>>12)<<4|(v>>8)&15))
	}
	return out, nil
}
