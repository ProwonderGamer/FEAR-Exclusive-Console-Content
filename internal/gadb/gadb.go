// Package gadb reads and writes the LithTech Jupiter EX game database "GADB" v3 (F.E.A.R.).
//
// PC files (.Gamdb00p, magic "GADB") are little-endian; console files (.gamedb, magic "BDAG") use the
// identical layout big-endian. The Go model is lossless (floats stay raw bit patterns) so that
// Read followed by Write reproduces every shipped file byte for byte.
//
// File layout (all fields u32 in file endianness):
//
//	char magic[4]; u32 version(=3); u32 poolSize;
//	u32 nCategories; u32 nRecords; u32 nAttributes; u32 nValues   (totals over the whole file)
//	u8  pool[poolSize]
//	u32 nCategories
//	per category: u32 nameOfs; u32 nRecords
//	  per record: u32 nameOfs; u32 nAttributes
//	    per attribute: u32 nameOfs; u32 type; u32 flags; u32 nValues; u32 value[nValues]
//
// Types: 1 bool, 2 float, 3 int32, 4 string (pool offset), 5 wide string (pool offset, UTF-16 in file
// endianness), 6/7/8 vector2/3/4 (pool offset to 2/3/4 floats), 9 record link ((cat<<16)|rec),
// 10 struct (value 0, nValues = number of elements). Flags occur on strings only (1 = file, 2 = ClientFX).
//
// Pool rules (verified by byte-exact round trip of every PC and console database): names, string values,
// wide strings and vector data share ONE pool; an entry is appended the first time its content is met in
// category -> record -> attribute -> values order; entries are de-duplicated by exact encoded bytes; each
// entry is zero padded to a multiple of 4 bytes.
package gadb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Attribute types.
const (
	TBool   = 1
	TFloat  = 2
	TInt    = 3
	TString = 4
	TWStr   = 5
	TVec2   = 6
	TVec3   = 7
	TVec4   = 8
	TLink   = 9
	TStruct = 10
)

const (
	nullRef = 0xFFFFFFFF
	// NoLink is the raw value of an empty record link.
	NoLink = 0xFFFFFFFF
)

// Value is one attribute value. Which fields are used depends on the attribute type:
// types 1,2,3,10 -> U; 4 -> S (or Null); 5 -> W (or Null); 6-8 -> V (or Null);
// 9 -> U (encoded link) or, after Symbolize, Sym with Cat/Rec (Null = no link).
type Value struct {
	U    uint32
	S    string // raw bytes (latin-1 semantics)
	W    []uint16
	V    []uint32
	Null bool
	Sym  bool
	Cat  string
	Rec  string
}

// Link returns a symbolic link value.
func Link(cat, rec string) Value { return Value{Sym: true, Cat: cat, Rec: rec} }

// NoLinkSym returns a symbolic "no link" value.
func NoLinkSym() Value { return Value{Sym: true, Null: true} }

// Str returns a string value.
func Str(s string) Value { return Value{S: s} }

// Equal compares two values of attribute type t.
func Equal(t uint32, a, b Value) bool {
	switch t {
	case TString:
		return a.Null == b.Null && a.S == b.S
	case TWStr:
		if a.Null != b.Null || len(a.W) != len(b.W) {
			return false
		}
		for i := range a.W {
			if a.W[i] != b.W[i] {
				return false
			}
		}
		return true
	case TVec2, TVec3, TVec4:
		if a.Null != b.Null || len(a.V) != len(b.V) {
			return false
		}
		for i := range a.V {
			if a.V[i] != b.V[i] {
				return false
			}
		}
		return true
	case TLink:
		if a.Sym != b.Sym {
			return false
		}
		if a.Sym {
			return a.Null == b.Null && a.Cat == b.Cat && a.Rec == b.Rec
		}
		return a.U == b.U
	}
	return a.U == b.U
}

// Key is a canonical comparable encoding of a value (used as map key).
func Key(t uint32, v Value) string {
	switch t {
	case TString:
		if v.Null {
			return "n"
		}
		return "s" + v.S
	case TWStr:
		if v.Null {
			return "n"
		}
		var sb strings.Builder
		sb.WriteByte('w')
		for _, c := range v.W {
			fmt.Fprintf(&sb, "%04x", c)
		}
		return sb.String()
	case TVec2, TVec3, TVec4:
		if v.Null {
			return "n"
		}
		var sb strings.Builder
		sb.WriteByte('v')
		for _, c := range v.V {
			fmt.Fprintf(&sb, "%08x", c)
		}
		return sb.String()
	case TLink:
		if v.Sym {
			if v.Null {
				return "ln"
			}
			return "l" + v.Cat + "\x00" + v.Rec
		}
	}
	return fmt.Sprintf("u%d", v.U)
}

// Attr is one named attribute of a record.
type Attr struct {
	Name   string
	Type   uint32
	Flags  uint32
	Values []Value
}

// Copy returns a copy with its own Values slice.
func (a *Attr) Copy() *Attr {
	b := *a
	b.Values = append([]Value(nil), a.Values...)
	return &b
}

// Record is a named record.
type Record struct {
	Name  string
	Attrs []*Attr
}

// Attr finds an attribute by exact name (nil if absent).
func (r *Record) Attr(name string) *Attr {
	for _, a := range r.Attrs {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// Copy returns a deep copy.
func (r *Record) Copy() *Record {
	n := &Record{Name: r.Name, Attrs: make([]*Attr, len(r.Attrs))}
	for i, a := range r.Attrs {
		n.Attrs[i] = a.Copy()
	}
	return n
}

// Category is a named list of records.
type Category struct {
	Name    string
	Records []*Record
}

// Index returns the index of the record with the exact name, or -1.
func (c *Category) Index(name string) int {
	for i, r := range c.Records {
		if r.Name == name {
			return i
		}
	}
	return -1
}

// Database is a parsed GADB/BDAG file.
type Database struct {
	Cats    []*Category
	BigEnd  bool
	Version uint32
}

// CatIndex returns the index of the category with the exact name, or -1.
func (d *Database) CatIndex(name string) int {
	for i, c := range d.Cats {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// Find returns the record or nil.
func (d *Database) Find(cat, rec string) *Record {
	ci := d.CatIndex(cat)
	if ci < 0 {
		return nil
	}
	ri := d.Cats[ci].Index(rec)
	if ri < 0 {
		return nil
	}
	return d.Cats[ci].Records[ri]
}

// Counts returns (categories, records, attributes, values).
func (d *Database) Counts() (int, int, int, int) {
	nr, na, nv := 0, 0, 0
	for _, c := range d.Cats {
		nr += len(c.Records)
		for _, r := range c.Records {
			na += len(r.Attrs)
			for _, a := range r.Attrs {
				nv += len(a.Values)
			}
		}
	}
	return len(d.Cats), nr, na, nv
}

// Clone returns a deep copy of the database.
func (d *Database) Clone() *Database {
	n := &Database{BigEnd: d.BigEnd, Version: d.Version, Cats: make([]*Category, len(d.Cats))}
	for i, c := range d.Cats {
		nc := &Category{Name: c.Name, Records: make([]*Record, len(c.Records))}
		for j, r := range c.Records {
			nc.Records[j] = r.Copy()
		}
		n.Cats[i] = nc
	}
	return n
}

// RecKey identifies a record by category and name.
type RecKey struct{ Cat, Rec string }

// RecMap returns a map from (category, record) to record.
func (d *Database) RecMap() map[RecKey]*Record {
	m := make(map[RecKey]*Record)
	for _, c := range d.Cats {
		for _, r := range c.Records {
			m[RecKey{c.Name, r.Name}] = r
		}
	}
	return m
}

// ---------------------------------------------------------------------------------------------
// reading
// ---------------------------------------------------------------------------------------------

// Read parses a GADB/BDAG file. It validates header counts and the file end.
func Read(data []byte) (*Database, error) {
	if len(data) < 0x1C {
		return nil, errors.New("gadb: file too small")
	}
	var bo binary.ByteOrder
	big := false
	switch string(data[:4]) {
	case "GADB":
		bo = binary.LittleEndian
	case "BDAG":
		bo = binary.BigEndian
		big = true
	default:
		return nil, fmt.Errorf("gadb: not a GADB file (magic %q)", data[:4])
	}
	u32 := func(o int) (uint32, error) {
		if o < 0 || o+4 > len(data) {
			return 0, errors.New("gadb: truncated file")
		}
		return bo.Uint32(data[o:]), nil
	}
	ver, _ := u32(4)
	psz, _ := u32(8)
	hcat, _ := u32(12)
	hrec, _ := u32(16)
	hatt, _ := u32(20)
	hval, _ := u32(24)
	if ver != 3 {
		return nil, fmt.Errorf("gadb: unsupported version %d", ver)
	}
	if uint64(0x1C)+uint64(psz) > uint64(len(data)) {
		return nil, errors.New("gadb: pool size exceeds file")
	}
	pool := data[0x1C : 0x1C+int(psz)]
	cstr := func(o uint32) (string, error) {
		if uint64(o) >= uint64(len(pool)) {
			return "", errors.New("gadb: string offset outside pool")
		}
		for i := int(o); i < len(pool); i++ {
			if pool[i] == 0 {
				return string(pool[o:i]), nil
			}
		}
		return "", errors.New("gadb: unterminated pool string")
	}
	wstr := func(o uint32) ([]uint16, error) {
		var out []uint16
		for p := uint64(o); ; p += 2 {
			if p+2 > uint64(len(pool)) {
				return nil, errors.New("gadb: unterminated wide string")
			}
			c := bo.Uint16(pool[p:])
			if c == 0 {
				return out, nil
			}
			out = append(out, c)
		}
	}
	p := 0x1C + int(psz)
	n, err := u32(p)
	if err != nil {
		return nil, err
	}
	p += 4
	if n != hcat {
		return nil, fmt.Errorf("gadb: category count mismatch %d != %d", n, hcat)
	}
	db := &Database{BigEnd: big, Version: ver}
	for ci := uint32(0); ci < n; ci++ {
		cno, err1 := u32(p)
		nr, err2 := u32(p + 4)
		if err1 != nil || err2 != nil {
			return nil, errors.New("gadb: truncated category")
		}
		p += 8
		cname, err := cstr(cno)
		if err != nil {
			return nil, err
		}
		cat := &Category{Name: cname}
		for ri := uint32(0); ri < nr; ri++ {
			rno, err1 := u32(p)
			na, err2 := u32(p + 4)
			if err1 != nil || err2 != nil {
				return nil, errors.New("gadb: truncated record")
			}
			p += 8
			rname, err := cstr(rno)
			if err != nil {
				return nil, err
			}
			rec := &Record{Name: rname}
			for ai := uint32(0); ai < na; ai++ {
				if p+16 > len(data) {
					return nil, errors.New("gadb: truncated attribute")
				}
				ano := bo.Uint32(data[p:])
				t := bo.Uint32(data[p+4:])
				fl := bo.Uint32(data[p+8:])
				nv := bo.Uint32(data[p+12:])
				p += 16
				if uint64(p)+4*uint64(nv) > uint64(len(data)) {
					return nil, errors.New("gadb: truncated values")
				}
				aname, err := cstr(ano)
				if err != nil {
					return nil, err
				}
				// A Value is far larger than the 4 bytes of file it is read from, so the slice grows as values are
				// parsed instead of reserving the declared count up front (an attribute that fails would waste it).
				a := &Attr{Name: aname, Type: t, Flags: fl, Values: make([]Value, 0, min(nv, 256))}
				for vi := uint32(0); vi < nv; vi++ {
					raw := bo.Uint32(data[p:])
					p += 4
					v := Value{}
					switch t {
					case TString:
						if raw == nullRef {
							v.Null = true
						} else if v.S, err = cstr(raw); err != nil {
							return nil, err
						}
					case TWStr:
						if raw == nullRef {
							v.Null = true
						} else if v.W, err = wstr(raw); err != nil {
							return nil, err
						}
					case TVec2, TVec3, TVec4:
						if raw == nullRef {
							v.Null = true
						} else {
							k := int(t) - 4
							if uint64(raw)+uint64(4*k) > uint64(len(pool)) {
								return nil, errors.New("gadb: vector outside pool")
							}
							v.V = make([]uint32, k)
							for i := 0; i < k; i++ {
								v.V[i] = bo.Uint32(pool[int(raw)+4*i:])
							}
						}
					default:
						v.U = raw
					}
					a.Values = append(a.Values, v)
				}
				a.Values = slices.Clip(a.Values)
				rec.Attrs = append(rec.Attrs, a)
			}
			cat.Records = append(cat.Records, rec)
		}
		db.Cats = append(db.Cats, cat)
	}
	if p != len(data) {
		return nil, fmt.Errorf("gadb: trailing data: parsed to %d, file size %d", p, len(data))
	}
	c, r, a, v := db.Counts()
	if uint32(c) != hcat || uint32(r) != hrec || uint32(a) != hatt || uint32(v) != hval {
		return nil, fmt.Errorf("gadb: header counts (%d,%d,%d,%d) != parsed (%d,%d,%d,%d)", hcat, hrec, hatt, hval, c, r, a, v)
	}
	return db, nil
}

// ---------------------------------------------------------------------------------------------
// writing
// ---------------------------------------------------------------------------------------------

type pool struct {
	buf   []byte
	index map[string]uint32
}

func (p *pool) add(entry []byte) uint32 {
	if o, ok := p.index[string(entry)]; ok {
		return o
	}
	o := uint32(len(p.buf))
	p.index[string(entry)] = o
	p.buf = append(p.buf, entry...)
	for len(p.buf)&3 != 0 {
		p.buf = append(p.buf, 0)
	}
	return o
}

// Write serialises the database. bigEndian selects console ('BDAG') byte order.
func Write(db *Database, bigEndian bool) ([]byte, error) {
	var bo binary.ByteOrder = binary.LittleEndian
	magic := "GADB"
	if bigEndian {
		bo = binary.BigEndian
		magic = "BDAG"
	}
	pl := &pool{index: make(map[string]uint32)}
	nameOfs := func(s string) uint32 { return pl.add(append([]byte(s), 0)) }
	var body []byte
	put := func(v uint32) {
		var b [4]byte
		bo.PutUint32(b[:], v)
		body = append(body, b[:]...)
	}
	put(uint32(len(db.Cats)))
	for _, c := range db.Cats {
		put(nameOfs(c.Name))
		put(uint32(len(c.Records)))
		for _, r := range c.Records {
			put(nameOfs(r.Name))
			put(uint32(len(r.Attrs)))
			for _, a := range r.Attrs {
				put(nameOfs(a.Name))
				put(a.Type)
				put(a.Flags)
				put(uint32(len(a.Values)))
				for _, v := range a.Values {
					switch a.Type {
					case TString:
						if v.Null {
							put(nullRef)
						} else {
							put(pl.add(append([]byte(v.S), 0)))
						}
					case TWStr:
						if v.Null {
							put(nullRef)
						} else {
							b := make([]byte, 0, 2*len(v.W)+2)
							for _, cu := range v.W {
								var t [2]byte
								bo.PutUint16(t[:], cu)
								b = append(b, t[:]...)
							}
							b = append(b, 0, 0)
							put(pl.add(b))
						}
					case TVec2, TVec3, TVec4:
						if v.Null {
							put(nullRef)
						} else {
							if len(v.V) != int(a.Type)-4 {
								return nil, fmt.Errorf("gadb: vector size %d does not match type %d", len(v.V), a.Type)
							}
							b := make([]byte, 0, 4*len(v.V))
							for _, x := range v.V {
								var t [4]byte
								bo.PutUint32(t[:], x)
								b = append(b, t[:]...)
							}
							put(pl.add(b))
						}
					default:
						if v.Sym {
							return nil, fmt.Errorf("gadb: symbolic link in %s/%s.%s not encoded", c.Name, r.Name, a.Name)
						}
						put(v.U)
					}
				}
			}
		}
	}
	nc, nr, na, nv := db.Counts()
	head := make([]byte, 0x1C)
	copy(head, magic)
	for i, x := range []uint32{3, uint32(len(pl.buf)), uint32(nc), uint32(nr), uint32(na), uint32(nv)} {
		bo.PutUint32(head[4+4*i:], x)
	}
	if db.Version != 0 && db.Version != 3 {
		return nil, fmt.Errorf("gadb: unsupported version %d", db.Version)
	}
	out := append(head, pl.buf...)
	return append(out, body...), nil
}

// ---------------------------------------------------------------------------------------------
// symbolic links, ordering
// ---------------------------------------------------------------------------------------------

// Symbolize replaces every link value by a symbolic (category, record) reference, in place.
func Symbolize(db *Database) error {
	for _, c := range db.Cats {
		for _, r := range c.Records {
			for _, a := range r.Attrs {
				if a.Type != TLink {
					continue
				}
				for i, v := range a.Values {
					if v.Sym {
						continue
					}
					if v.U == NoLink {
						a.Values[i] = NoLinkSym()
						continue
					}
					ci, ri := int(v.U>>16), int(v.U&0xFFFF)
					if ci >= len(db.Cats) || ri >= len(db.Cats[ci].Records) {
						return fmt.Errorf("gadb: bad link %#x in %s/%s.%s", v.U, c.Name, r.Name, a.Name)
					}
					a.Values[i] = Link(db.Cats[ci].Name, db.Cats[ci].Records[ri].Name)
				}
			}
		}
	}
	return nil
}

// EncodeLinks is the inverse of Symbolize for the final category/record order. It fails on a dangling link.
func EncodeLinks(db *Database) error {
	idx := make(map[RecKey]uint32)
	for ci, c := range db.Cats {
		for ri, r := range c.Records {
			idx[RecKey{c.Name, r.Name}] = uint32(ci)<<16 | uint32(ri)
		}
	}
	for _, c := range db.Cats {
		for _, r := range c.Records {
			for _, a := range r.Attrs {
				if a.Type != TLink {
					continue
				}
				for i, v := range a.Values {
					if !v.Sym {
						continue
					}
					if v.Null {
						a.Values[i] = Value{U: NoLink}
						continue
					}
					u, ok := idx[RecKey{v.Cat, v.Rec}]
					if !ok {
						return fmt.Errorf("gadb: dangling link @%s/%s in %s/%s.%s", v.Cat, v.Rec, c.Name, r.Name, a.Name)
					}
					a.Values[i] = Value{U: u}
				}
			}
		}
	}
	return nil
}

// SKey is the sort key matching MSVC _stricmp in the C locale (ASCII case folding only).
func SKey(name string) string {
	b := []byte(name)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// Lower is an ASCII-only lower-casing.
func Lower(s string) string { return SKey(s) }

// CheckSorted reports ordering violations (the engine binary-searches names with _stricmp).
func CheckSorted(db *Database, label string) []string {
	var bad []string
	for i := 0; i+1 < len(db.Cats); i++ {
		if SKey(db.Cats[i].Name) >= SKey(db.Cats[i+1].Name) {
			bad = append(bad, label+": categories not strictly sorted")
			break
		}
	}
	for _, c := range db.Cats {
		for i := 0; i+1 < len(c.Records); i++ {
			if SKey(c.Records[i].Name) >= SKey(c.Records[i+1].Name) {
				bad = append(bad, fmt.Sprintf("%s: records of %s not strictly sorted", label, c.Name))
				break
			}
		}
		for _, r := range c.Records {
			for i := 0; i+1 < len(r.Attrs); i++ {
				if SKey(r.Attrs[i].Name) >= SKey(r.Attrs[i+1].Name) {
					bad = append(bad, fmt.Sprintf("%s: attributes of %s/%s not strictly sorted", label, c.Name, r.Name))
					break
				}
			}
		}
	}
	return bad
}

// InsertSorted inserts a record at its _stricmp position and returns (position, new length).
func InsertSorted(db *Database, cat string, rec *Record) (int, int, error) {
	ci := db.CatIndex(cat)
	if ci < 0 {
		return 0, 0, fmt.Errorf("gadb: no category %s", cat)
	}
	c := db.Cats[ci]
	k := SKey(rec.Name)
	pos := len(c.Records)
	for i, r := range c.Records {
		rk := SKey(r.Name)
		if rk == k {
			return 0, 0, fmt.Errorf("gadb: %s/%s already exists", cat, rec.Name)
		}
		if rk > k && pos == len(c.Records) {
			pos = i
		}
	}
	c.Records = append(c.Records, nil)
	copy(c.Records[pos+1:], c.Records[pos:])
	c.Records[pos] = rec
	return pos, len(c.Records), nil
}
