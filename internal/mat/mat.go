// Package mat converts console F.E.A.R. material files (.mat, magic 'IMTL', big endian; the PS3 and Xbox 360
// copies are identical) into PC .Mat00 files (magic 'LTMI', little endian). The output is byte-exact with the
// mod's material files tested in game (golden hashes in mat_test.go).
//
// Layout (both sides; integers big endian on the console, little endian on PC):
//
//	magic[4]; u32 version; str shader; u32 count; count x { u32 type; str name; value }
//	str   = u16 length + bytes (no terminator)
//	value = type 1: str (texture path; a trailing ".tex" becomes ".dds")
//	        type 4, 5: 4 bytes (float / vector component data), byte-reversed
//
// Any other type, or bytes left over, is an error (no guessing).
package mat

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Convert turns a console .mat into a PC .Mat00.
func Convert(src []byte) ([]byte, error) {
	r := &reader{b: src}
	if len(src) < 4 || string(src[:4]) != "IMTL" {
		return nil, errors.New("mat: not a console .mat (magic IMTL)")
	}
	r.p = 4
	var out bytes.Buffer
	out.WriteString("LTMI")
	u32 := func() uint32 {
		v := r.u32()
		binary.Write(&out, binary.LittleEndian, v)
		return v
	}
	str := func(tex bool) {
		s := r.str()
		if tex && len(s) >= 4 && bytes.EqualFold(s[len(s)-4:], []byte(".tex")) {
			s = append(append([]byte{}, s[:len(s)-4]...), ".dds"...)
		}
		if r.err == nil {
			binary.Write(&out, binary.LittleEndian, uint16(len(s)))
			out.Write(s)
		}
	}
	u32() // version
	str(false)
	n := u32()
	for i := uint32(0); i < n && r.err == nil; i++ {
		typ := u32()
		str(false)
		switch typ {
		case 1:
			str(true)
		case 4, 5:
			v := r.take(4)
			if v != nil {
				out.Write([]byte{v[3], v[2], v[1], v[0]})
			}
		default:
			return nil, fmt.Errorf("mat: parameter %d has unknown type %d", i, typ)
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.p != len(src) {
		return nil, fmt.Errorf("mat: %d bytes left after the last parameter", len(src)-r.p)
	}
	return out.Bytes(), nil
}

type reader struct {
	b   []byte
	p   int
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || r.p+n > len(r.b) {
		r.err = errors.New("mat: file ends early")
		return nil
	}
	v := r.b[r.p : r.p+n]
	r.p += n
	return v
}

func (r *reader) u32() uint32 {
	if v := r.take(4); v != nil {
		return binary.BigEndian.Uint32(v)
	}
	return 0
}

func (r *reader) str() []byte {
	v := r.take(2)
	if v == nil {
		return nil
	}
	return r.take(int(binary.BigEndian.Uint16(v)))
}
