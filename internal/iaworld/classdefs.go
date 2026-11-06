package iaworld

// LithTech ClassDef / PropDef tables of the player's PC GameServer.dll: a port of
// the project's Python classdef reader (load, default_value). The map builder uses them to give the converted
// objects every PC property the editor would have written, with the PropDef default.
//
//	ClassDef (.data): {char* name, char* "", ClassDef* parent, u32 flags, ctor, dtor, ?, ?, u32 nProps, PropDef* props, ...}
//	PropDef (44 bytes): {char* name, char* defaultString, u32 type, f32 defaultVec[3], f32 defaultFloat, ?, ?, u32 flags, ?}

import (
	"bytes"
	"encoding/binary"
	"math"

	"fearinstaller/internal/wld"
)

type propDef struct {
	name   string
	str    string // default string; hasStr false when the pointer is 0 or not a printable string
	hasStr bool
	typ    uint32
	vec    [3]uint32 // float bits
	flt    uint32    // float bits
}

type classDef struct {
	name  string
	props []propDef // inherited chain, root class first
}

type peSection struct {
	name       string
	va, vs, rs uint32 // vs = max(VirtualSize, SizeOfRawData) as the recipe uses it
	ra         uint32
}

type peImage struct {
	d    []byte
	base uint32
	secs []peSection
}

func parsePE(d []byte) *peImage {
	le := binary.LittleEndian
	if len(d) < 0x40 {
		fail("GameServer.dll: too small")
	}
	pe := int(le.Uint32(d[0x3c:]))
	if pe < 0 || pe+24+96 > len(d) || string(d[pe:pe+4]) != "PE\x00\x00" {
		fail("GameServer.dll: no PE header")
	}
	ns := int(le.Uint16(d[pe+6:]))
	opt := int(le.Uint16(d[pe+20:]))
	img := &peImage{d: d, base: le.Uint32(d[pe+24+28:])}
	for i := 0; i < ns; i++ {
		o := pe + 24 + opt + 40*i
		if o+40 > len(d) {
			fail("GameServer.dll: truncated section table")
		}
		vs, va, rs, ra := le.Uint32(d[o+8:]), le.Uint32(d[o+12:]), le.Uint32(d[o+16:]), le.Uint32(d[o+20:])
		img.secs = append(img.secs, peSection{string(bytes.TrimRight(d[o:o+8], "\x00")), img.base + va, max(vs, rs), rs, ra})
	}
	return img
}

// off maps a virtual address to a file offset (within the raw data of its section).
func (p *peImage) off(v uint32) (int, bool) {
	for _, s := range p.secs {
		if s.va <= v && uint64(v) < uint64(s.va)+uint64(s.rs) {
			return int(v - s.va + s.ra), true
		}
	}
	return 0, false
}

// sec names the section holding v ("" if none).
func (p *peImage) sec(v uint32) string {
	for _, s := range p.secs {
		if s.va <= v && uint64(v) < uint64(s.va)+uint64(s.vs) {
			return s.name
		}
	}
	return ""
}

// cstr returns the NUL-terminated string at v when every byte is printable ASCII (32..126).
func (p *peImage) cstr(v uint32) (string, bool) {
	o, ok := p.off(v)
	if !ok || o > len(p.d) {
		return "", false
	}
	e := bytes.IndexByte(p.d[o:], 0)
	var s []byte
	if e < 0 { // Python's find() returned -1: d[o:-1]
		if o >= len(p.d)-1 {
			s = nil
		} else {
			s = p.d[o : len(p.d)-1]
		}
	} else {
		s = p.d[o : o+e]
	}
	for _, c := range s {
		if c < 32 || c >= 127 {
			return "", false
		}
	}
	return string(s), true
}

func (p *peImage) u32s(v uint32, n int) []uint32 {
	o, ok := p.off(v)
	if !ok || o+4*n > len(p.d) {
		fail("GameServer.dll: address %#x outside the file", v)
	}
	out := make([]uint32, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(p.d[o+4*i:])
	}
	return out
}

func isAlnumName(s string) bool { // name.replace('_', 'a').isalnum()
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9') {
			return false
		}
	}
	return true
}

// loadClassDefs scans .data for ClassDef records and returns each class (by name; a later record of the same
// name replaces an earlier one, as the recipe's dict does) with its inherited property list, in the order
// of the recipe's dict (first appearance of each name).
func loadClassDefs(dll []byte) []classDef {
	pe := parsePE(dll)
	var data *peSection
	for i := range pe.secs {
		if pe.secs[i].name == ".data" {
			data = &pe.secs[i]
			break
		}
	}
	if data == nil {
		fail("GameServer.dll: no .data section")
	}
	type raw struct {
		name   string
		parent uint32
		props  []propDef
	}
	classes := map[uint32]*raw{}
	var addrs []uint32
	for v := uint64(data.va); v+48 < uint64(data.va)+uint64(data.rs); v += 4 {
		f := pe.u32s(uint32(v), 12)
		name := ""
		if pe.sec(f[0]) == ".rdata" {
			name, _ = pe.cstr(f[0])
		}
		if !isAlnumName(name) {
			continue
		}
		if f[2] != 0 && pe.sec(f[2]) != ".data" {
			continue
		}
		if f[1] == 0 {
			continue
		}
		if s, ok := pe.cstr(f[1]); !ok || s != "" {
			continue
		}
		nprops, pp := f[8], f[9]
		if nprops > 400 || (nprops != 0 && pe.sec(pp) != ".data") {
			continue
		}
		var props []propDef
		ok := true
		for k := uint32(0); k < nprops; k++ {
			p := pe.u32s(pp+44*k, 11)
			pn, isStr := pe.cstr(p[0])
			if !isStr {
				ok = false
				break
			}
			d := propDef{name: pn, typ: p[2], vec: [3]uint32{p[3], p[4], p[5]}, flt: p[6]}
			if p[1] != 0 {
				d.str, d.hasStr = pe.cstr(p[1])
			}
			props = append(props, d)
		}
		if !ok {
			continue
		}
		classes[uint32(v)] = &raw{name, f[2], props}
		addrs = append(addrs, uint32(v))
	}
	var out []classDef
	pos := map[string]int{}
	for _, v := range addrs {
		c := classes[v]
		var chain []uint32
		seen := map[uint32]bool{}
		for cur := v; cur != 0 && classes[cur] != nil && !seen[cur]; cur = classes[cur].parent {
			seen[cur] = true
			chain = append(chain, cur)
		}
		var all []propDef
		for i := len(chain) - 1; i >= 0; i-- {
			all = append(all, classes[chain[i]].props...)
		}
		cd := classDef{c.name, all}
		if i, ok := pos[c.name]; ok {
			out[i] = cd
		} else {
			pos[c.name] = len(out)
			out = append(out, cd)
		}
	}
	if len(out) == 0 {
		fail("GameServer.dll: no ClassDef records found")
	}
	return out
}

// defaultValue is pc_classdefs.default_value in the world-file representation. bad is set when the recipe could
// hold the value but not write it (an unknown type gives None; a long default outside u32 fails struct.pack):
// such a default only matters if a converted object needs it, so the caller fails then, not here.
func (p propDef) defaultValue() (v wld.Prop, bad string) {
	v = wld.Prop{Name: p.name, Type: p.typ}
	f := float64(math.Float32frombits(p.flt))
	switch p.typ {
	case wld.TString, wld.TCommand, wld.TStringID:
		v.S = p.str // `p['str'] or ''`
	case wld.TVector, wld.TColor:
		v.N = []uint32{p.vec[0], p.vec[1], p.vec[2]}
	case wld.TFloat:
		v.U = p.flt
	case wld.TBool:
		if f != 0 { // NaN != 0.0 is True, as in Python
			v.U = 1
		}
	case wld.TLongInt: // ('raw', 5, int(float)), packed as u32 when written
		if math.IsNaN(f) || math.IsInf(f, 0) {
			fail("PropDef %s: long default %v", p.name, f) // int() raises here in the recipe
		}
		if t := math.Trunc(f); t < 0 || t > math.MaxUint32 {
			return v, "long default out of range"
		}
		v.U = uint32(math.Trunc(f))
	case wld.TRot:
		v.N = []uint32{0, 0, 0, math.Float32bits(1)}
	default:
		return v, "unknown property type"
	}
	return v, ""
}
