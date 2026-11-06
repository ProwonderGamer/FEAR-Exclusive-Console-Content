// Package model reads and writes LithTech Jupiter EX model files: PC F.E.A.R. ".Model00p" (MODL v33,
// little endian) and the console ".mdl" (LDOM v34, big endian; PS3 and Xbox 360 use the same layout), and
// converts console models to PC models.
//
// It is a byte-exact Go port of the project's Python tools (model00p.py, mdl2model00p.py) and of
// the post-convert fix steps that start from their output; see convert.go and fixes.go. The file layout
// (decoded from FEAR.exe v1.08) is documented in model00p.py; the short form:
//
//	u32 magic 'MODL' ('LDOM' in big endian), u32 version, u32 counts[21], strings[counts[9]],
//	node tree (depth first), u32 global, track sets, anim data, anims, weight sets, sockets,
//	child-model names, u8 hasGeometry, [physics, pieces, render object].
//
// Field values are kept as raw integers (floats as their u32 bit patterns) so a re-write is exact.
package model

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// constraintExtra is the number of dwords after the 8-dword base of a physics constraint, by type.
var constraintExtra = map[uint32]int{0: 0, 1: 1, 2: 6, 3: 15, 4: 19, 5: 13, 6: 15}

// maxNodeDepth bounds the recursive node tree; Python's default recursion limit (1000) fails earlier on such
// files, and no real model comes close.
const maxNodeDepth = 900

// Node is one skeleton node.
type Node struct {
	Name      uint32 // offset into Strings
	Index     uint16
	Flags     uint8
	Xform     [7]uint32 // f32 pos[3], rot[4] (x,y,z,w)
	NChildren uint32
}

// TrackSet is one animation track set: per node a (pos, rot) offset pair.
type TrackSet struct {
	Const, Frame uint16
	Tracks       []uint16 // 2 * len(nodes)
}

// Anim is one animation: a 10-dword header and nKeys x (time, cmdStringOfs).
type Anim struct {
	H    [10]uint32 // dims[3], radius, nameOfs, interp, trackSet, dataOfs, compressed flag, nKeys
	Keys []uint32
}

// WeightSet is a named list of per-node weights (f32 bit patterns).
type WeightSet struct {
	Name uint32
	W    []uint32
}

// Shape is a Havok physics shape (factory FEAR.exe 0x44ccc0). Only the fields of its type are set.
type Shape struct {
	T        uint32
	Hdr      []uint32 // types 1, 4: 16 dwords
	Verts    []uint32 // types 1, 4
	Idx32    bool     // type 1: indices are u32 (nVerts >= 0x10000)
	Idx      []uint32 // type 1
	Mopp     []byte   // type 1: hkMoppCode blob, little endian on every platform, copied verbatim
	Planes   []uint32 // type 4
	V        []uint32 // types 2 (6), 3 (3), 7 (9)
	Xf       []uint32 // type 5 (7)
	Children []*Shape // type 5 (1), 6 (n), 8 (2)
}

// Body is one rigid physics body.
type Body struct {
	Node  uint8
	Xform [7]uint32
	V     [3]uint32
	Shape *Shape
}

// Constraint is one physics constraint.
type Constraint struct {
	T uint32
	V []uint32 // 8 + constraintExtra[T] dwords
}

// PhysWeight is one entry of a physics weight set (one per body).
type PhysWeight struct {
	A    uint8
	B, C uint32
}

// PhysWeightSet is a named physics weight set.
type PhysWeightSet struct {
	Name uint32
	W    []PhysWeight
}

// Physics is the physics block of a model with geometry.
type Physics struct {
	Root, F     uint32
	Bodies      []Body
	Constraints []Constraint
	WeightSets  []PhysWeightSet
}

// LOD is one level of detail of a piece.
type LOD struct {
	Dist   uint32
	B      [3]uint8
	Meshes []uint32
}

// Piece is one model piece.
type Piece struct {
	Name uint32
	LODs []LOD
}

// Elem is one D3DVERTEXELEMENT9.
type Elem struct {
	Stream, Offset                uint16
	Type, Method, Usage, UsageIdx uint8
}

// Section is one render-mesh section: minVtx, numVtx, stride, startIdx, baseVtx, numPrims, material, nBones,
// decl, followed by nBones palette bytes.
type Section struct {
	F     [9]uint32
	Bones []byte
}

// Mesh is one render mesh (or an external, named render resource).
type Mesh struct {
	External uint32
	A, B     uint32 // external only
	Name     string // external only, raw Latin-1 bytes
	Hdr0     uint32
	NMat     uint32
	Vtx      []byte
	Idx      []byte
	Decls    [][]Elem
	Sections []Section
}

// Render is the render object.
type Render struct {
	NObj, NSec uint32
	Meshes     []*Mesh
}

// Model is a parsed model file.
type Model struct {
	BigEndian   bool
	Version     uint32
	Counts      [21]uint32 // as read; the writer recomputes them
	Strings     []byte
	Nodes       []Node
	GlobalF     uint32
	TrackSets   []TrackSet
	AnimData    []byte
	Anims       []Anim
	WeightSets  []WeightSet
	Sockets     [][10]uint32 // node, nameOfs, rot[4], pos[3], scale
	Children    []string     // raw Latin-1 bytes
	HasGeometry uint8
	Physics     *Physics
	Pieces      []Piece
	Render      *Render
}

func (m *Model) order() binary.ByteOrder {
	if m.BigEndian {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

// String returns the NUL-terminated string at ofs in the string blob (ok=false when out of range).
func (m *Model) String(ofs uint32) (string, bool) {
	if uint64(ofs) >= uint64(len(m.Strings)) {
		return "", false
	}
	s := m.Strings[ofs:]
	for i, b := range s {
		if b == 0 {
			return string(s[:i]), true
		}
	}
	return "", false // Python: str.index raises ValueError
}

// ------------------------------------------------------------------------------------------------ reader

var errShort = errors.New("model: read past end of data")

type reader struct {
	d []byte
	p int
	e binary.ByteOrder
}

func (r *reader) need(n uint64) error {
	if n > uint64(len(r.d)-r.p) {
		return fmt.Errorf("%w at %#x (need %d bytes)", errShort, r.p, n)
	}
	return nil
}

func (r *reader) u8() (uint8, error) {
	if err := r.need(1); err != nil {
		return 0, err
	}
	v := r.d[r.p]
	r.p++
	return v, nil
}

func (r *reader) u16() (uint16, error) {
	if err := r.need(2); err != nil {
		return 0, err
	}
	v := r.e.Uint16(r.d[r.p:])
	r.p += 2
	return v, nil
}

func (r *reader) u32() (uint32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	v := r.e.Uint32(r.d[r.p:])
	r.p += 4
	return v, nil
}

func (r *reader) u16s(n uint64) ([]uint16, error) {
	if err := r.need(2 * n); err != nil {
		return nil, err
	}
	v := make([]uint16, n)
	for i := range v {
		v[i] = r.e.Uint16(r.d[r.p:])
		r.p += 2
	}
	return v, nil
}

func (r *reader) u32s(n uint64) ([]uint32, error) {
	if err := r.need(4 * n); err != nil {
		return nil, err
	}
	v := make([]uint32, n)
	for i := range v {
		v[i] = r.e.Uint32(r.d[r.p:])
		r.p += 4
	}
	return v, nil
}

func (r *reader) u32a(dst []uint32) error {
	if err := r.need(4 * uint64(len(dst))); err != nil {
		return err
	}
	for i := range dst {
		dst[i] = r.e.Uint32(r.d[r.p:])
		r.p += 4
	}
	return nil
}

func (r *reader) raw(n uint64) ([]byte, error) {
	if err := r.need(n); err != nil {
		return nil, err
	}
	v := append([]byte(nil), r.d[r.p:r.p+int(n)]...)
	r.p += int(n)
	return v, nil
}

// str reads a u16 length + Latin-1 chars (no NUL).
func (r *reader) str() (string, error) {
	n, err := r.u16()
	if err != nil {
		return "", err
	}
	b, err := r.raw(uint64(n))
	return string(b), err
}

// Parse reads a model file ('MODL' little endian or 'LDOM' big endian, version 33 or 34).
func Parse(d []byte) (m *Model, err error) {
	if len(d) < 4 {
		return nil, errors.New("model: not a model file (too short)")
	}
	m = &Model{}
	switch string(d[:4]) {
	case "MODL":
	case "LDOM":
		m.BigEndian = true
	default:
		return nil, fmt.Errorf("model: not a model file (magic %q)", d[:4])
	}
	r := &reader{d: d, p: 4, e: m.order()}
	if m.Version, err = r.u32(); err != nil {
		return nil, err
	}
	if m.Version != 33 && m.Version != 34 {
		return nil, fmt.Errorf("model: unsupported model version %d", m.Version)
	}
	c := m.Counts[:]
	if err = r.u32a(c); err != nil {
		return nil, err
	}
	if m.Strings, err = r.raw(uint64(c[9])); err != nil {
		return nil, err
	}
	if err = m.readNode(r, 0); err != nil {
		return nil, err
	}
	if m.GlobalF, err = r.u32(); err != nil {
		return nil, err
	}
	nn := uint64(len(m.Nodes))
	for i := uint32(0); i < c[8]; i++ {
		var ts TrackSet
		if ts.Const, err = r.u16(); err != nil {
			return nil, err
		}
		if ts.Frame, err = r.u16(); err != nil {
			return nil, err
		}
		if ts.Tracks, err = r.u16s(2 * nn); err != nil {
			return nil, err
		}
		m.TrackSets = append(m.TrackSets, ts)
	}
	if m.AnimData, err = r.raw(uint64(c[20])); err != nil {
		return nil, err
	}
	for i := uint32(0); i < c[1]; i++ {
		var a Anim
		if err = r.u32a(a.H[:]); err != nil {
			return nil, err
		}
		if a.Keys, err = r.u32s(2 * uint64(a.H[9])); err != nil {
			return nil, err
		}
		m.Anims = append(m.Anims, a)
	}
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	for i := uint32(0); i < n; i++ {
		var w WeightSet
		if w.Name, err = r.u32(); err != nil {
			return nil, err
		}
		k, err := r.u32()
		if err != nil {
			return nil, err
		}
		if w.W, err = r.u32s(uint64(k)); err != nil {
			return nil, err
		}
		m.WeightSets = append(m.WeightSets, w)
	}
	if n, err = r.u32(); err != nil {
		return nil, err
	}
	for i := uint32(0); i < n; i++ {
		var s [10]uint32
		if err = r.u32a(s[:]); err != nil {
			return nil, err
		}
		m.Sockets = append(m.Sockets, s)
	}
	nch, err := r.u32()
	if err != nil {
		return nil, err
	}
	nnames := nch
	if m.Version < 34 {
		if nch > 0 {
			nnames = nch - 1
		}
	}
	for i := uint32(0); i < nnames; i++ {
		s, err := r.str()
		if err != nil {
			return nil, err
		}
		m.Children = append(m.Children, s)
	}
	if m.HasGeometry, err = r.u8(); err != nil {
		return nil, err
	}
	if m.HasGeometry != 0 {
		if err = m.readGeometry(r); err != nil {
			return nil, err
		}
	}
	if r.p != len(d) {
		return nil, fmt.Errorf("model: trailing data: parsed %#x of %#x bytes", r.p, len(d))
	}
	return m, nil
}

func (m *Model) readNode(r *reader, depth int) error {
	if depth > maxNodeDepth {
		return errors.New("model: node tree too deep")
	}
	var n Node
	var err error
	if n.Name, err = r.u32(); err != nil {
		return err
	}
	if n.Index, err = r.u16(); err != nil {
		return err
	}
	if n.Flags, err = r.u8(); err != nil {
		return err
	}
	if err = r.u32a(n.Xform[:]); err != nil {
		return err
	}
	if n.NChildren, err = r.u32(); err != nil {
		return err
	}
	m.Nodes = append(m.Nodes, n)
	for i := uint32(0); i < n.NChildren; i++ {
		if err = m.readNode(r, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func readShape(r *reader, depth int) (*Shape, error) {
	if depth > maxNodeDepth {
		return nil, errors.New("model: shape tree too deep")
	}
	t, err := r.u32()
	if err != nil {
		return nil, err
	}
	s := &Shape{T: t}
	switch t {
	case 1: // triangle mesh + MOPP
		if s.Hdr, err = r.u32s(16); err != nil {
			return nil, err
		}
		nv, err := r.u32()
		if err != nil {
			return nil, err
		}
		ni, err := r.u32()
		if err != nil {
			return nil, err
		}
		if s.Verts, err = r.u32s(3 * uint64(nv)); err != nil {
			return nil, err
		}
		s.Idx32 = nv >= 0x10000
		if s.Idx32 {
			if s.Idx, err = r.u32s(uint64(ni)); err != nil {
				return nil, err
			}
		} else {
			v, err := r.u16s(uint64(ni))
			if err != nil {
				return nil, err
			}
			s.Idx = make([]uint32, len(v))
			for i, x := range v {
				s.Idx[i] = uint32(x)
			}
		}
		if err = r.need(20); err != nil {
			return nil, err
		}
		msz := binary.LittleEndian.Uint32(r.d[r.p+16:]) // the MOPP blob is little endian on PS3 too
		if s.Mopp, err = r.raw(20 + uint64(msz)); err != nil {
			return nil, err
		}
	case 2:
		s.V, err = r.u32s(6)
	case 3:
		s.V, err = r.u32s(3)
	case 4: // convex vertices
		if s.Hdr, err = r.u32s(16); err != nil {
			return nil, err
		}
		nv, err := r.u32()
		if err != nil {
			return nil, err
		}
		np, err := r.u32()
		if err != nil {
			return nil, err
		}
		if s.Verts, err = r.u32s(3 * uint64(nv)); err != nil {
			return nil, err
		}
		s.Planes, err = r.u32s(4 * uint64(np))
	case 5:
		if s.Xf, err = r.u32s(7); err != nil {
			return nil, err
		}
		var ch *Shape
		if ch, err = readShape(r, depth+1); err != nil {
			return nil, err
		}
		s.Children = []*Shape{ch}
	case 6:
		n, err := r.u32()
		if err != nil {
			return nil, err
		}
		for i := uint32(0); i < n; i++ {
			ch, err := readShape(r, depth+1)
			if err != nil {
				return nil, err
			}
			s.Children = append(s.Children, ch)
		}
	case 7:
		s.V, err = r.u32s(9)
	case 8:
		for i := 0; i < 2; i++ {
			ch, err := readShape(r, depth+1)
			if err != nil {
				return nil, err
			}
			s.Children = append(s.Children, ch)
		}
	}
	// t == 0 or t > 8: the type word alone
	return s, err
}

func (m *Model) readGeometry(r *reader) error {
	var err error
	ph := &Physics{}
	if ph.Root, err = r.u32(); err != nil {
		return err
	}
	if ph.F, err = r.u32(); err != nil {
		return err
	}
	nb, err := r.u32()
	if err != nil {
		return err
	}
	for i := uint32(0); i < nb; i++ {
		var b Body
		if b.Node, err = r.u8(); err != nil {
			return err
		}
		if err = r.u32a(b.Xform[:]); err != nil {
			return err
		}
		if err = r.u32a(b.V[:]); err != nil {
			return err
		}
		if b.Shape, err = readShape(r, 0); err != nil {
			return err
		}
		ph.Bodies = append(ph.Bodies, b)
	}
	n, err := r.u32()
	if err != nil {
		return err
	}
	for i := uint32(0); i < n; i++ {
		t, err := r.u32()
		if err != nil {
			return err
		}
		extra, ok := constraintExtra[t]
		if !ok {
			return fmt.Errorf("model: constraint type %d at %#x", t, r.p-4)
		}
		v, err := r.u32s(uint64(8 + extra))
		if err != nil {
			return err
		}
		ph.Constraints = append(ph.Constraints, Constraint{T: t, V: v})
	}
	if n, err = r.u32(); err != nil {
		return err
	}
	for i := uint32(0); i < n; i++ {
		var ws PhysWeightSet
		if ws.Name, err = r.u32(); err != nil {
			return err
		}
		if err = r.need(9 * uint64(nb)); err != nil {
			return err
		}
		for j := uint32(0); j < nb; j++ {
			var w PhysWeight
			w.A, _ = r.u8()
			w.B, _ = r.u32()
			w.C, _ = r.u32()
			ws.W = append(ws.W, w)
		}
		ph.WeightSets = append(ph.WeightSets, ws)
	}
	m.Physics = ph
	m.Pieces = []Piece{}
	if n, err = r.u32(); err != nil {
		return err
	}
	for i := uint32(0); i < n; i++ {
		var p Piece
		if p.Name, err = r.u32(); err != nil {
			return err
		}
		nl, err := r.u32()
		if err != nil {
			return err
		}
		for j := uint32(0); j < nl; j++ {
			var l LOD
			if l.Dist, err = r.u32(); err != nil {
				return err
			}
			for k := range l.B {
				if l.B[k], err = r.u8(); err != nil {
					return err
				}
			}
			k, err := r.u32()
			if err != nil {
				return err
			}
			if l.Meshes, err = r.u32s(uint64(k)); err != nil {
				return err
			}
			p.LODs = append(p.LODs, l)
		}
		m.Pieces = append(m.Pieces, p)
	}
	rd := &Render{}
	nm, err := r.u32()
	if err != nil {
		return err
	}
	if rd.NObj, err = r.u32(); err != nil {
		return err
	}
	if rd.NSec, err = r.u32(); err != nil {
		return err
	}
	for i := uint32(0); i < nm; i++ {
		ms, err := readMesh(r)
		if err != nil {
			return err
		}
		rd.Meshes = append(rd.Meshes, ms)
	}
	m.Render = rd
	return nil
}

func readMesh(r *reader) (*Mesh, error) {
	ms := &Mesh{}
	var err error
	if ms.External, err = r.u32(); err != nil {
		return nil, err
	}
	if ms.External != 0 {
		if ms.A, err = r.u32(); err != nil {
			return nil, err
		}
		if ms.Name, err = r.str(); err != nil {
			return nil, err
		}
		ms.B, err = r.u32()
		return ms, err
	}
	if ms.Hdr0, err = r.u32(); err != nil {
		return nil, err
	}
	if ms.NMat, err = r.u32(); err != nil {
		return nil, err
	}
	vb, err := r.u32()
	if err != nil {
		return nil, err
	}
	ib, err := r.u32()
	if err != nil {
		return nil, err
	}
	if ms.Vtx, err = r.raw(uint64(vb)); err != nil {
		return nil, err
	}
	if ms.Idx, err = r.raw(uint64(ib)); err != nil {
		return nil, err
	}
	nd, err := r.u32()
	if err != nil {
		return nil, err
	}
	for i := uint32(0); i < nd; i++ {
		sz, err := r.u32()
		if err != nil {
			return nil, err
		}
		raw, err := r.raw(uint64(sz))
		if err != nil {
			return nil, err
		}
		if len(raw)%8 != 0 { // Python's struct.unpack_from on the last partial element fails
			return nil, fmt.Errorf("model: vertex declaration size %d is not a multiple of 8", len(raw))
		}
		decl := make([]Elem, 0, len(raw)/8)
		for o := 0; o < len(raw); o += 8 {
			decl = append(decl, Elem{Stream: r.e.Uint16(raw[o:]), Offset: r.e.Uint16(raw[o+2:]),
				Type: raw[o+4], Method: raw[o+5], Usage: raw[o+6], UsageIdx: raw[o+7]})
		}
		ms.Decls = append(ms.Decls, decl)
	}
	ns, err := r.u32()
	if err != nil {
		return nil, err
	}
	for i := uint32(0); i < ns; i++ {
		var s Section
		if err = r.u32a(s.F[:]); err != nil {
			return nil, err
		}
		if s.Bones, err = r.raw(uint64(s.F[7])); err != nil {
			return nil, err
		}
		ms.Sections = append(ms.Sections, s)
	}
	return ms, nil
}

// ------------------------------------------------------------------------------------------------ writer

// ComputedCounts returns the 21 header counts recomputed from the parsed data (used by the writer).
func (m *Model) ComputedCounts() [21]uint32 {
	var c [21]uint32
	for _, a := range m.Anims {
		c[0] += a.H[9]
	}
	c[1] = uint32(len(m.Anims))
	c[2] = uint32(len(m.Nodes))
	c[4] = uint32(len(m.Children) + 1)
	c[6] = uint32(len(m.Sockets))
	c[7] = uint32(len(m.WeightSets))
	c[8] = uint32(len(m.TrackSets))
	c[9] = uint32(len(m.Strings))
	c[20] = uint32(len(m.AnimData))
	if m.HasGeometry != 0 {
		c[3] = uint32(len(m.Pieces))
		for _, p := range m.Pieces {
			c[5] += uint32(len(p.LODs))
			for _, l := range p.LODs {
				c[12] += uint32(len(l.Meshes))
			}
		}
		c[10] = uint32(len(m.Physics.WeightSets))
		c[11] = uint32(len(m.Physics.Bodies))
		for _, k := range m.Physics.Constraints {
			c[13+k.T]++
		}
	}
	return c
}

type writer struct {
	b []byte
	e binary.AppendByteOrder
}

func (w *writer) u8(v uint8) { w.b = append(w.b, v) }
func (w *writer) u16(v ...uint16) {
	for _, x := range v {
		w.b = w.e.AppendUint16(w.b, x)
	}
}
func (w *writer) u32(v ...uint32) {
	for _, x := range v {
		w.b = w.e.AppendUint32(w.b, x)
	}
}
func (w *writer) raw(b []byte) { w.b = append(w.b, b...) }
func (w *writer) str(s string) error {
	if len(s) > 0xFFFF {
		return fmt.Errorf("model: string too long (%d bytes)", len(s))
	}
	w.u16(uint16(len(s)))
	w.b = append(w.b, s...)
	return nil
}

func writeShape(w *writer, s *Shape) error {
	w.u32(s.T)
	switch s.T {
	case 1:
		w.u32(s.Hdr...)
		w.u32(uint32(len(s.Verts)/3), uint32(len(s.Idx)))
		w.u32(s.Verts...)
		if s.Idx32 {
			w.u32(s.Idx...)
		} else {
			for _, x := range s.Idx {
				if x > 0xFFFF {
					return errors.New("model: 16-bit shape index out of range")
				}
				w.u16(uint16(x))
			}
		}
		w.raw(s.Mopp)
	case 2, 3, 7:
		w.u32(s.V...)
	case 4:
		w.u32(s.Hdr...)
		w.u32(uint32(len(s.Verts)/3), uint32(len(s.Planes)/4))
		w.u32(s.Verts...)
		w.u32(s.Planes...)
	case 5:
		w.u32(s.Xf...)
		return writeShape(w, s.Children[0])
	case 6:
		w.u32(uint32(len(s.Children)))
		for _, c := range s.Children {
			if err := writeShape(w, c); err != nil {
				return err
			}
		}
	case 8:
		for _, c := range s.Children[:2] {
			if err := writeShape(w, c); err != nil {
				return err
			}
		}
	}
	return nil
}

// Serialize writes the model with the given byte order and version (header counts are recomputed, as the
// Python writer does). Use m.BigEndian / m.Version to re-write it unchanged.
func (m *Model) Serialize(bigEndian bool, version uint32) ([]byte, error) {
	w := &writer{e: binary.LittleEndian}
	if bigEndian {
		w.e = binary.BigEndian
		w.raw([]byte("LDOM"))
	} else {
		w.raw([]byte("MODL"))
	}
	w.u32(version)
	c := m.ComputedCounts()
	w.u32(c[:]...)
	w.raw(m.Strings)
	for _, n := range m.Nodes {
		w.u32(n.Name)
		w.u16(n.Index)
		w.u8(n.Flags)
		w.u32(n.Xform[:]...)
		w.u32(n.NChildren)
	}
	w.u32(m.GlobalF)
	for _, t := range m.TrackSets {
		w.u16(t.Const, t.Frame)
		w.u16(t.Tracks...)
	}
	w.raw(m.AnimData)
	for _, a := range m.Anims {
		w.u32(a.H[:]...)
		w.u32(a.Keys...)
	}
	w.u32(uint32(len(m.WeightSets)))
	for _, s := range m.WeightSets {
		w.u32(s.Name, uint32(len(s.W)))
		w.u32(s.W...)
	}
	w.u32(uint32(len(m.Sockets)))
	for _, s := range m.Sockets {
		w.u32(s[:]...)
	}
	if version >= 34 {
		w.u32(uint32(len(m.Children)))
	} else {
		w.u32(uint32(len(m.Children) + 1))
	}
	for _, s := range m.Children {
		if err := w.str(s); err != nil {
			return nil, err
		}
	}
	w.u8(m.HasGeometry)
	if m.HasGeometry != 0 {
		ph := m.Physics
		w.u32(ph.Root, ph.F, uint32(len(ph.Bodies)))
		for _, b := range ph.Bodies {
			w.u8(b.Node)
			w.u32(b.Xform[:]...)
			w.u32(b.V[:]...)
			if err := writeShape(w, b.Shape); err != nil {
				return nil, err
			}
		}
		w.u32(uint32(len(ph.Constraints)))
		for _, k := range ph.Constraints {
			w.u32(k.T)
			w.u32(k.V...)
		}
		w.u32(uint32(len(ph.WeightSets)))
		for _, s := range ph.WeightSets {
			w.u32(s.Name)
			for _, x := range s.W {
				w.u8(x.A)
				w.u32(x.B, x.C)
			}
		}
		w.u32(uint32(len(m.Pieces)))
		for _, p := range m.Pieces {
			w.u32(p.Name, uint32(len(p.LODs)))
			for _, l := range p.LODs {
				w.u32(l.Dist)
				w.raw(l.B[:])
				w.u32(uint32(len(l.Meshes)))
				w.u32(l.Meshes...)
			}
		}
		rd := m.Render
		w.u32(uint32(len(rd.Meshes)), rd.NObj, rd.NSec)
		for _, ms := range rd.Meshes {
			w.u32(ms.External)
			if ms.External != 0 {
				w.u32(ms.A)
				if err := w.str(ms.Name); err != nil {
					return nil, err
				}
				w.u32(ms.B)
				continue
			}
			w.u32(ms.Hdr0, ms.NMat, uint32(len(ms.Vtx)), uint32(len(ms.Idx)))
			w.raw(ms.Vtx)
			w.raw(ms.Idx)
			w.u32(uint32(len(ms.Decls)))
			for _, dcl := range ms.Decls {
				w.u32(uint32(8 * len(dcl)))
				for _, x := range dcl {
					w.u16(x.Stream, x.Offset)
					w.raw([]byte{x.Type, x.Method, x.Usage, x.UsageIdx})
				}
			}
			w.u32(uint32(len(ms.Sections)))
			for _, s := range ms.Sections {
				w.u32(s.F[:]...)
				w.raw(s.Bones)
			}
		}
	}
	return w.b, nil
}
