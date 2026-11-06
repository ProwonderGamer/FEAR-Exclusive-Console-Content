package model

import (
	"encoding/binary"
	"testing"
)

// tinyConsoleModel builds a small valid console (big endian v34) model: one node, one anim with a constant
// rotation track, one child, and one render mesh with POS F3 + CMP normal + white COLOR0 (3 vertices).
func tinyConsoleModel(t testing.TB) []byte {
	vtx := make([]byte, 3*20)
	for k := 0; k < 3; k++ {
		binary.BigEndian.PutUint32(vtx[k*20+12:], 0x3FF<<22|0x7FF) // a CMP word
		binary.BigEndian.PutUint32(vtx[k*20+16:], 0xFFFFFFFF)      // white COLOR0
	}
	idx := []byte{0, 0, 0, 1, 0, 2}
	m := &Model{
		BigEndian: true, Version: 34,
		Strings:     []byte("root\x00\x00\x00\x00"),
		Nodes:       []Node{{Name: 0}},
		TrackSets:   []TrackSet{{Const: 8, Frame: 0, Tracks: []uint16{0xFFFF, 0x8000}}},
		AnimData:    []byte{0, 0, 0, 0, 0, 0, 0x7F, 0xFF},
		Anims:       []Anim{{H: [10]uint32{0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, Keys: []uint32{0, 0}}},
		Children:    []string{`chars\models\kid.mdl`},
		HasGeometry: 1,
		Physics:     &Physics{},
		Pieces:      []Piece{{LODs: []LOD{{Meshes: []uint32{0}}}}},
		Render: &Render{NObj: 1, NSec: 1, Meshes: []*Mesh{{
			Vtx: vtx, Idx: idx,
			Decls: [][]Elem{{{Offset: 0, Type: 2, Usage: uPos}, {Offset: 12, Type: tCMP, Usage: uNrm},
				{Offset: 16, Type: 4, Usage: uCol}, endElement}},
			Sections: []Section{{F: [9]uint32{0, 3, 20, 0, 0, 1, 0, 1, 0}, Bones: []byte{0}}},
		}}},
	}
	b, err := m.Serialize(true, 34)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestTinyConvert(t *testing.T) {
	out, st, err := Convert(tinyConsoleModel(t), `chars\models\p.mdl`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if m.Children[0] != "kid.Model00p" || len(m.Render.Meshes[0].Vtx) != 3*24 || m.Render.Meshes[0].Sections[0].F[2] != 24 {
		t.Errorf("unexpected result: children %q, %d vertex bytes", m.Children, len(m.Render.Meshes[0].Vtx))
	}
	if len(st.Color0) != 1 || st.UncoveredAnimBytes != 0 {
		t.Errorf("stats %+v", st)
	}
}

// FuzzConvert: damaged input must give an error, never a panic.
func FuzzConvert(f *testing.F) {
	f.Add(tinyConsoleModel(f))
	f.Fuzz(func(t *testing.T, d []byte) {
		if m, err := Parse(d); err == nil {
			_, _ = m.Serialize(m.BigEndian, m.Version)
		}
		_, _, _ = Convert(d, `chars\models\p.mdl`, Options{})
	})
}
