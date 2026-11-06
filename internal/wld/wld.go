// Package wld parses and writes the object section of F.E.A.R. worlds.
//
// The same section layout is used by the console server world files (.wldsrvr, magic "SDLW", big endian,
// objects at offset 8) and by the PC world (.World00p, little endian, section 3; header = version +
// 4 section offsets) and by the PC ".Objects00p" override file ("OBJT", u32 1, then the section):
//
//	u32 count; per object: u16 classLen, class, u32 nProps, u32 blobSize, blob, nProps x {u32 nameOfs, u32 type, u32 value|ofs}
//
// Property types: 0 string, 1 vector, 2 color, 3 float, 4 bool, 5 long int, 6 rotation, 7 command, 8 string id.
// Strings/vectors live in the blob (names first, then value, each NUL terminated / padded to 4); scalars are
// stored inline. Floats are kept as raw IEEE bit patterns so that nothing is rounded.
package wld

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Property types.
const (
	TString   = 0
	TVector   = 1
	TColor    = 2
	TFloat    = 3
	TBool     = 4
	TLongInt  = 5
	TRot      = 6
	TCommand  = 7
	TStringID = 8
)

// Prop is one object property.
type Prop struct {
	Name string
	Type uint32
	S    string   // types 0, 7, 8
	N    []uint32 // float bit patterns: 3 for types 1, 2; 4 for type 6
	U    uint32   // type 3 float bits, type 4 bool (0/1), other types the raw inline value
}

// Object is one world object.
type Object struct {
	Class string
	Props []Prop
}

// Name returns the value of the "Name" property ("" if absent).
func (o *Object) Name() string {
	for _, p := range o.Props {
		if p.Name == "Name" {
			return p.S
		}
	}
	return ""
}

// PropIndex returns the index of a property or -1.
func (o *Object) PropIndex(name string) int {
	for i, p := range o.Props {
		if p.Name == name {
			return i
		}
	}
	return -1
}

// Clone returns a deep copy.
func (o Object) Clone() Object {
	n := Object{Class: o.Class, Props: make([]Prop, len(o.Props))}
	for i, p := range o.Props {
		p.N = append([]uint32(nil), p.N...)
		n.Props[i] = p
	}
	return n
}

func cstr(blob []byte, off uint32) (string, error) {
	if uint64(off) >= uint64(len(blob)) {
		return "", errors.New("wld: string offset outside blob")
	}
	e := bytes.IndexByte(blob[off:], 0)
	if e < 0 {
		return "", errors.New("wld: unterminated string")
	}
	return string(blob[off : int(off)+e]), nil
}

// minObjectBytes is the size of the smallest possible object record: u16 class length, u32 property count, u32 blob size.
const minObjectBytes = 10

// ParseObjects parses the object section at offset p. It returns the objects and the offset after the section.
func ParseObjects(d []byte, p int, big bool) ([]Object, int, error) {
	var bo binary.ByteOrder = binary.LittleEndian
	if big {
		bo = binary.BigEndian
	}
	need := func(n int) error {
		if p < 0 || n < 0 || p+n > len(d) {
			return errors.New("wld: truncated object section")
		}
		return nil
	}
	if err := need(4); err != nil {
		return nil, 0, err
	}
	cnt := bo.Uint32(d[p:])
	p += 4
	// An object takes at least 10 bytes (class length, property count, blob size), so this bounds the
	// reservation below by the section itself.
	if uint64(cnt) > uint64(len(d)-p)/minObjectBytes {
		return nil, 0, errors.New("wld: implausible object count")
	}
	objs := make([]Object, 0, cnt)
	for i := uint32(0); i < cnt; i++ {
		if err := need(2); err != nil {
			return nil, 0, err
		}
		cl := int(bo.Uint16(d[p:]))
		p += 2
		if err := need(cl); err != nil {
			return nil, 0, err
		}
		cls := string(d[p : p+cl])
		p += cl
		if err := need(8); err != nil {
			return nil, 0, err
		}
		npr, bs := int(bo.Uint32(d[p:])), int(bo.Uint32(d[p+4:]))
		p += 8
		if err := need(bs); err != nil {
			return nil, 0, err
		}
		blob := d[p : p+bs]
		p += bs
		if uint64(npr)*12 > uint64(len(d)) {
			return nil, 0, errors.New("wld: implausible property count")
		}
		if err := need(npr * 12); err != nil {
			return nil, 0, err
		}
		o := Object{Class: cls, Props: make([]Prop, npr)}
		for j := 0; j < npr; j++ {
			no, t, v := bo.Uint32(d[p:]), bo.Uint32(d[p+4:]), bo.Uint32(d[p+8:])
			p += 12
			name, err := cstr(blob, no)
			if err != nil {
				return nil, 0, err
			}
			pr := Prop{Name: name, Type: t}
			switch t {
			case TString, TCommand, TStringID:
				if pr.S, err = cstr(blob, v); err != nil {
					return nil, 0, err
				}
			case TVector, TColor, TRot:
				k := 3
				if t == TRot {
					k = 4
				}
				if uint64(v)+uint64(4*k) > uint64(len(blob)) {
					return nil, 0, errors.New("wld: vector outside blob")
				}
				pr.N = make([]uint32, k)
				for x := 0; x < k; x++ {
					pr.N[x] = bo.Uint32(blob[int(v)+4*x:])
				}
			case TBool:
				if v != 0 {
					pr.U = 1
				}
			default:
				pr.U = v
			}
			o.Props[j] = pr
		}
		objs = append(objs, o)
	}
	return objs, p, nil
}

// ParseConsoleServer parses a console ".wldsrvr" ("SDLW", big endian).
func ParseConsoleServer(d []byte) (version uint32, objs []Object, err error) {
	if len(d) < 12 || string(d[:4]) != "SDLW" {
		return 0, nil, errors.New("wld: not a .wldsrvr (magic SDLW)")
	}
	version = binary.BigEndian.Uint32(d[4:])
	objs, _, err = ParseObjects(d, 8, true)
	return
}

// ParsePCWorld parses the objects of a PC ".World00p" and returns the section's [start,end) offsets.
func ParsePCWorld(d []byte) (objs []Object, start, end int, err error) {
	if len(d) < 20 {
		return nil, 0, 0, errors.New("wld: PC world too small")
	}
	start = int(binary.LittleEndian.Uint32(d[12:]))
	objs, end, err = ParseObjects(d, start, false)
	return
}

// SerializeObjects writes the PC (little endian) object section.
func SerializeObjects(objs []Object) []byte {
	out := make([]byte, 4, 1<<16)
	binary.LittleEndian.PutUint32(out, uint32(len(objs)))
	pad := func(b []byte) []byte {
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
		return b
	}
	u32 := func(b []byte, v uint32) []byte {
		var t [4]byte
		binary.LittleEndian.PutUint32(t[:], v)
		return append(b, t[:]...)
	}
	for _, o := range objs {
		var blob []byte
		type ent struct{ no, t, vo uint32 }
		tab := make([]ent, 0, len(o.Props))
		for _, p := range o.Props {
			no := uint32(len(blob))
			blob = pad(append(blob, append([]byte(p.Name), 0)...))
			var vo uint32
			switch p.Type {
			case TString, TCommand, TStringID:
				vo = uint32(len(blob))
				blob = pad(append(blob, append([]byte(p.S), 0)...))
			case TVector, TColor, TRot:
				vo = uint32(len(blob))
				for _, x := range p.N {
					blob = u32(blob, x)
				}
			default:
				vo = p.U
			}
			tab = append(tab, ent{no, p.Type, vo})
		}
		var h [2]byte
		binary.LittleEndian.PutUint16(h[:], uint16(len(o.Class)))
		out = append(out, h[:]...)
		out = append(out, o.Class...)
		out = u32(out, uint32(len(tab)))
		out = u32(out, uint32(len(blob)))
		out = append(out, blob...)
		for _, e := range tab {
			out = u32(out, e.no)
			out = u32(out, e.t)
			out = u32(out, e.vo)
		}
	}
	return out
}

// String implements fmt.Stringer.
func (p Prop) String() string { return fmt.Sprintf("%s(type %d)", p.Name, p.Type) }
