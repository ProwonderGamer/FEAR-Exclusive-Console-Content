package hookfill

// The hook draws the HUD with the Xbox 360 layout from a table of (record, attribute, value index) rows of the
// game database category Interface/HUD, each holding the Xbox 360 value and the stock PC value of that field.
//
// Table layout (32-bit DLL, .rdata): Rows entries of 44 bytes from TableOffset (file offset):
//
//	u32 record name (VA of a C string); u32 attribute name (VA); u8 value index; u8 kind; u8 class; u8 pad;
//	32-byte value union: ints i[0] = Xbox 360, i[1] = PC; floats f[0..n) = Xbox 360, f[n..2n) = PC,
//	n = 1 (float), 2 (vector2) or 4 (vector4).
//
// String rows (alignments) and font-link rows hold pointers to short names and are left as they are.
//
// Value rules (the same rules the table was first generated with):
//   - A pixel-class IconSize, and the pixel-class AdditionalPoint[2] of HUDWeapon and HUDGrenadeList, describe
//     the PC texture art: the Xbox 360 slot carries the PC value (the PC icons keep their own size and UV layout).
//   - Exception, HUDHealth and HUDArmor IconSize: the PC draws only the left AdditionalFloat part of its icon
//     texture where the Xbox 360 draws all of it, so the Xbox 360 slot is the PC IconSize * AF(360) / AF(PC).
//   - Every other row: the Xbox 360 slot is the Xbox 360 value, the PC slot the stock PC value.

import (
	"encoding/binary"
	"fmt"
	"math"

	"fearinstaller/internal/gadb"
)

// The table's place in the hook build of install.HookSHA256 (TestTemplate checks it against the template).
const (
	TableOffset = 0x17188
	Rows        = 152
	rowSize     = 44
	unionOffset = 12
	unionSize   = 32
	category    = "Interface/HUD"
)

// Row kinds and classes as the hook defines them.
const (
	kindInt = iota
	kindStr
	kindVec2
	kindVec4
	kindFloat
	kindLink
)

const classRaw = 0 // classes 1 and 2 are pixel sizes

// Row is one parsed table row.
type Row struct {
	Rec, Attr          string
	Index, Kind, Class int
	off                int // file offset of the row
}

// numeric reports whether the row holds numbers (and so is filled in).
func (r Row) numeric() bool {
	return r.Kind == kindInt || r.Kind == kindFloat || r.Kind == kindVec2 || r.Kind == kindVec4
}

// count is the number of values per game version (1 for ints and floats).
func (r Row) count() int {
	switch r.Kind {
	case kindVec2:
		return 2
	case kindVec4:
		return 4
	}
	return 1
}

// pe maps virtual addresses of a 32-bit PE image to file offsets.
type pe struct {
	base uint32
	secs [][4]uint32 // VA, virtual size, raw offset, raw size
}

func readPE(b []byte) (*pe, error) {
	le := binary.LittleEndian
	if len(b) < 0x40 || b[0] != 'M' || b[1] != 'Z' {
		return nil, fmt.Errorf("not a program file")
	}
	h := int(le.Uint32(b[0x3c:]))
	if h+24 > len(b) || string(b[h:h+4]) != "PE\x00\x00" {
		return nil, fmt.Errorf("no PE header")
	}
	n := int(le.Uint16(b[h+6:]))
	opt := int(le.Uint16(b[h+20:]))
	if le.Uint16(b[h+24:]) != 0x10b {
		return nil, fmt.Errorf("not a 32-bit image")
	}
	p := &pe{base: le.Uint32(b[h+24+28:])}
	for i := 0; i < n; i++ {
		s := h + 24 + opt + 40*i
		if s+40 > len(b) {
			return nil, fmt.Errorf("section table cut short")
		}
		p.secs = append(p.secs, [4]uint32{le.Uint32(b[s+12:]), le.Uint32(b[s+8:]), le.Uint32(b[s+20:]), le.Uint32(b[s+16:])})
	}
	return p, nil
}

func (p *pe) cstring(b []byte, va uint32) (string, bool) {
	rva := va - p.base
	for _, s := range p.secs {
		if rva >= s[0] && rva < s[0]+s[3] {
			o := int(s[2] + rva - s[0])
			for e := o; e < len(b) && e < o+64; e++ {
				if b[e] == 0 {
					return string(b[o:e]), true
				}
			}
			return "", false
		}
	}
	return "", false
}

// parseTable reads the HUD table rows of a hook image.
func parseTable(dll []byte) ([]Row, error) {
	p, err := readPE(dll)
	if err != nil {
		return nil, err
	}
	if TableOffset+Rows*rowSize > len(dll) {
		return nil, fmt.Errorf("hook file too small for its HUD table")
	}
	le := binary.LittleEndian
	rows := make([]Row, Rows)
	for i := range rows {
		o := TableOffset + i*rowSize
		rec, ok1 := p.cstring(dll, le.Uint32(dll[o:]))
		attr, ok2 := p.cstring(dll, le.Uint32(dll[o+4:]))
		if !ok1 || !ok2 || len(rec) < 4 || rec[:3] != "HUD" || attr == "" || dll[o+9] > kindLink {
			return nil, fmt.Errorf("HUD table row %d is not where the hook build puts it", i)
		}
		rows[i] = Row{Rec: rec, Attr: attr, Index: int(dll[o+8]), Kind: int(dll[o+9]), Class: int(dll[o+10]), off: o}
	}
	return rows, nil
}

// blankTable sets every number of the HUD table of out to zero.
func blankTable(out []byte) error {
	rows, err := parseTable(out)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.numeric() {
			clear(out[r.off+unionOffset : r.off+unionOffset+unionSize])
		}
	}
	return nil
}

// hud returns the Interface/HUD records of a database by name.
func hud(data []byte, what string) (map[string]*gadb.Record, error) {
	db, err := gadb.Read(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	ci := db.CatIndex(category)
	if ci < 0 {
		return nil, fmt.Errorf("%s: no %s category", what, category)
	}
	m := map[string]*gadb.Record{}
	for _, r := range db.Cats[ci].Records {
		m[r.Name] = r
	}
	return m, nil
}

// field returns the 32-bit words of value index idx of rec.attr (n words for vectors, 1 for ints and floats).
func field(recs map[string]*gadb.Record, rec, attr string, idx, n int, what string) ([]uint32, error) {
	r := recs[rec]
	if r == nil {
		return nil, fmt.Errorf("%s: %s/%s missing", what, category, rec)
	}
	a := r.Attr(attr)
	if a == nil || idx >= len(a.Values) {
		return nil, fmt.Errorf("%s: %s.%s[%d] missing", what, rec, attr, idx)
	}
	v := a.Values[idx]
	switch a.Type {
	case gadb.TInt, gadb.TFloat:
		if n != 1 {
			return nil, fmt.Errorf("%s: %s.%s is a number, the hook expects %d values", what, rec, attr, n)
		}
		return []uint32{v.U}, nil
	case gadb.TVec2, gadb.TVec4:
		if v.Null || len(v.V) != n {
			return nil, fmt.Errorf("%s: %s.%s[%d] has %d values, the hook expects %d", what, rec, attr, idx, len(v.V), n)
		}
		return v.V, nil
	}
	return nil, fmt.Errorf("%s: %s.%s has type %d", what, rec, attr, a.Type)
}

// art reports whether the Xbox 360 slot of a pixel row carries the PC art size.
func art(r Row) bool {
	return r.Attr == "IconSize" || (r.Attr == "AdditionalPoint" && r.Index == 2 && (r.Rec == "HUDWeapon" || r.Rec == "HUDGrenadeList"))
}

// fillTable writes the HUD table numbers of out from the Xbox 360 Loki.gamedb and the stock PC FEARL.Gamdb00p.
func fillTable(out, x360DB, pcFEARL []byte) error {
	rows, err := parseTable(out)
	if err != nil {
		return err
	}
	x, err := hud(x360DB, "Xbox 360 Loki.gamedb")
	if err != nil {
		return err
	}
	p, err := hud(pcFEARL, "PC FEARL.Gamdb00p")
	if err != nil {
		return err
	}
	le := binary.LittleEndian
	for _, r := range rows {
		if !r.numeric() {
			continue
		}
		n := r.count()
		pv, err := field(p, r.Rec, r.Attr, r.Index, n, "PC FEARL.Gamdb00p")
		if err != nil {
			return err
		}
		var xv []uint32
		switch {
		case r.Class != classRaw && r.Attr == "IconSize" && (r.Rec == "HUDHealth" || r.Rec == "HUDArmor"):
			ax, err := field(x, r.Rec, "AdditionalFloat", 0, 1, "Xbox 360 Loki.gamedb")
			if err != nil {
				return err
			}
			ap, err := field(p, r.Rec, "AdditionalFloat", 0, 1, "PC FEARL.Gamdb00p")
			if err != nil {
				return err
			}
			fx, fp := float64(math.Float32frombits(ax[0])), float64(math.Float32frombits(ap[0]))
			if !(fp > 0 && fp < fx) {
				return fmt.Errorf("%s: the icon UV widths are not the expected ones", r.Rec)
			}
			xv = make([]uint32, n)
			for i, q := range pv {
				xv[i] = math.Float32bits(float32(float64(math.Float32frombits(q)) * fx / fp))
			}
		case r.Class != classRaw && art(r):
			xv = pv
		default:
			if xv, err = field(x, r.Rec, r.Attr, r.Index, n, "Xbox 360 Loki.gamedb"); err != nil {
				return err
			}
		}
		u := r.off + unionOffset
		for i := 0; i < n; i++ {
			le.PutUint32(out[u+4*i:], xv[i])
			le.PutUint32(out[u+4*(n+i):], pv[i])
		}
	}
	return nil
}
