// Package anmtree reads and writes LithTech Jupiter EX animation trees (PC "ANMT" v3 little endian, console
// "TMNA" v4 big endian), splits the console combined container (database_anim/Anim.anmtree, "LTNA") and builds
// the mod's Player_Upper_SFODD tree. Byte-exact port of the project's Python animation tree tools (raw ANMT reader and
// writer, console container split, Player_Upper builder).
//
// Section layout (GameServer.dll v1.08 FUN_101a3ad0 / FUN_101a3d10): magic, u32 version, u32 strtabSize,
// strtab, then 32-bit words: prop groups and desc groups (3 words each), props, descs, transitions (desc vector
// + 8 words each), anim refs, anim lists (2 words), animations (desc vector + 13 words), nodes (prop vector +
// 3 words), trie (5 words). Every word is kept as an unsigned value so a re-write is exact.
package anmtree

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Tree is a parsed animation tree; every record is a slice of raw u32 words.
type Tree struct {
	BigEndian   bool
	Magic       [4]byte
	Ver         uint32
	Strtab      []byte
	PG, DG      [][]uint32 // [nameOfs, count, first]
	Props       []uint32
	Descs       []uint32
	TransDescs  []uint32
	Transitions [][]uint32 // 8 words
	AnimRefs    []uint32
	AnimLists   [][]uint32 // 2 words
	AnimDescs   []uint32
	Animations  [][]uint32 // 13 words
	NodeProps   []uint32
	Nodes       [][]uint32 // 3 words
	Trie        [][]uint32 // 5 words (signed): propIdx, groupIdx, nodeIdx, yes, no
}

func s32(x uint32) int64 { return int64(int32(x)) }
func u32(x int64) uint32 { return uint32(x) }

// maxCount bounds a section count before allocation (a damaged count must not exhaust memory).
func checkCount(n, w uint64, left int) error {
	if w == 0 || n > uint64(left)/4/w {
		return errors.New("anmtree: section count exceeds the file")
	}
	return nil
}

// Load parses a PC (ANMT) or console (TMNA) tree; the walk must end at the last byte.
func Load(d []byte) (*Tree, error) {
	t := &Tree{}
	if len(d) < 12 {
		return nil, errors.New("anmtree: file too short")
	}
	switch string(d[:4]) {
	case "ANMT":
	case "TMNA":
		t.BigEndian = true
	default:
		return nil, fmt.Errorf("anmtree: not an animation tree (magic %q)", d[:4])
	}
	copy(t.Magic[:], d[:4])
	var e binary.ByteOrder = binary.LittleEndian
	if t.BigEndian {
		e = binary.BigEndian
	}
	p := 4
	var err error
	U := func() uint32 {
		if err != nil {
			return 0
		}
		if p+4 > len(d) {
			err = errors.New("anmtree: read past end")
			return 0
		}
		v := e.Uint32(d[p:])
		p += 4
		return v
	}
	arr := func(n uint64) []uint32 {
		if err != nil {
			return nil
		}
		if err = checkCount(n, 1, len(d)-p); err != nil {
			return nil
		}
		v := make([]uint32, n)
		for i := range v {
			v[i] = e.Uint32(d[p:])
			p += 4
		}
		return v
	}
	recs := func(n, w uint64) [][]uint32 {
		if err != nil {
			return nil
		}
		if err = checkCount(n, w, len(d)-p); err != nil {
			return nil
		}
		v := arr(n * w)
		out := make([][]uint32, n)
		for i := range out {
			out[i] = v[uint64(i)*w : uint64(i+1)*w : uint64(i+1)*w]
		}
		return out
	}
	t.Ver = U()
	S := U()
	if err == nil && uint64(S) > uint64(len(d)-p) {
		err = errors.New("anmtree: string table exceeds the file")
	}
	if err != nil {
		return nil, err
	}
	t.Strtab = append([]byte(nil), d[p:p+int(S)]...)
	p += int(S)
	t.PG = recs(uint64(U()), 3)
	t.DG = recs(uint64(U()), 3)
	t.Props = arr(uint64(U()))
	t.Descs = arr(uint64(U()))
	nDG := uint64(len(t.DG))
	n := uint64(U())
	t.TransDescs = arr(n * nDG)
	t.Transitions = recs(n, 8)
	t.AnimRefs = arr(uint64(U()))
	t.AnimLists = recs(uint64(U()), 2)
	n = uint64(U())
	t.AnimDescs = arr(n * nDG)
	t.Animations = recs(n, 13)
	n = uint64(U())
	t.NodeProps = arr(n * uint64(len(t.PG)))
	t.Nodes = recs(n, 3)
	t.Trie = recs(uint64(U()), 5)
	if err != nil {
		return nil, err
	}
	if p != len(d) {
		return nil, fmt.Errorf("anmtree: walker ended at %#x of %#x", p, len(d))
	}
	return t, nil
}

// Serialize writes the tree in its own byte order, magic and version.
func (t *Tree) Serialize() ([]byte, error) {
	if len(t.Strtab)%4 != 0 {
		return nil, errors.New("anmtree: string table not 4-aligned")
	}
	nDG, nPG := len(t.DG), len(t.PG)
	if len(t.TransDescs) != len(t.Transitions)*nDG || len(t.AnimDescs) != len(t.Animations)*nDG ||
		len(t.NodeProps) != len(t.Nodes)*nPG {
		return nil, errors.New("anmtree: descriptor / prop vectors do not match their records")
	}
	var e binary.ByteOrder = binary.LittleEndian
	if t.BigEndian {
		e = binary.BigEndian
	}
	var b bytes.Buffer
	var w4 [4]byte
	W := func(xs ...uint32) {
		for _, x := range xs {
			e.PutUint32(w4[:], x)
			b.Write(w4[:])
		}
	}
	R := func(rs [][]uint32) {
		for _, r := range rs {
			W(r...)
		}
	}
	b.Write(t.Magic[:])
	W(t.Ver, uint32(len(t.Strtab)))
	b.Write(t.Strtab)
	W(uint32(len(t.PG)))
	R(t.PG)
	W(uint32(len(t.DG)))
	R(t.DG)
	W(uint32(len(t.Props)))
	W(t.Props...)
	W(uint32(len(t.Descs)))
	W(t.Descs...)
	W(uint32(len(t.Transitions)))
	W(t.TransDescs...)
	R(t.Transitions)
	W(uint32(len(t.AnimRefs)))
	W(t.AnimRefs...)
	W(uint32(len(t.AnimLists)))
	R(t.AnimLists)
	W(uint32(len(t.Animations)))
	W(t.AnimDescs...)
	R(t.Animations)
	W(uint32(len(t.Nodes)))
	W(t.NodeProps...)
	R(t.Nodes)
	W(uint32(len(t.Trie)))
	R(t.Trie)
	return b.Bytes(), nil
}

// Name returns the NUL-terminated string at ofs (ok=false when out of range).
func (t *Tree) Name(ofs uint32) (string, bool) {
	if uint64(ofs) >= uint64(len(t.Strtab)) {
		return "", false
	}
	i := bytes.IndexByte(t.Strtab[ofs:], 0)
	if i < 0 {
		return "", false
	}
	return string(t.Strtab[ofs : int(ofs)+i]), true
}

// Intern returns the offset of a NUL-terminated string equal to name, appending it (4-aligned) when missing.
func (t *Tree) Intern(name string) uint32 {
	b := append([]byte(name), 0)
	if bytes.HasPrefix(t.Strtab, b) {
		return 0
	}
	if i := bytes.Index(t.Strtab, append([]byte{0}, b...)); i >= 0 {
		return uint32(i + 1)
	}
	o := len(t.Strtab)
	t.Strtab = append(t.Strtab, b...)
	for len(t.Strtab)%4 != 0 {
		t.Strtab = append(t.Strtab, 0)
	}
	return uint32(o)
}

func (t *Tree) names(ofs []uint32) ([]string, error) {
	out := make([]string, len(ofs))
	for i, o := range ofs {
		s, ok := t.Name(o)
		if !ok {
			return nil, fmt.Errorf("anmtree: bad string offset %#x", o)
		}
		out[i] = s
	}
	return out, nil
}

func (t *Tree) groupNames(g [][]uint32) ([]string, error) {
	o := make([]uint32, len(g))
	for i, r := range g {
		o[i] = r[0]
	}
	return t.names(o)
}

// ------------------------------------------------------------------------------------------------ container

// Entry is one tree of a console Anim.anmtree container.
type Entry struct {
	Name string
	Data []byte
}

// Split splits the console combined container (PS3 database_anim/anim.anmtree, X360
// Game/database_anim/Anim.anmtree; big endian):
//
//	'LTNA'; u32 version; u32 nameEnd (names = bytes [12, 8+nameEnd)); 'XXXX'; u32 nTrees;
//	nTrees x {u32 nameOfs, u32 dataOfs, u32 size}; u32 totalSize; tree data (offsets from the data start).
func Split(d []byte) (version uint32, trees []Entry, err error) {
	be := binary.BigEndian
	if len(d) < 12 || string(d[:4]) != "LTNA" {
		return 0, nil, errors.New("anmtree: not an LTNA container")
	}
	version, nameEnd := be.Uint32(d[4:]), be.Uint32(d[8:])
	p := 8 + uint64(nameEnd)
	if p+8 > uint64(len(d)) || nameEnd < 4 {
		return 0, nil, errors.New("anmtree: container header out of range")
	}
	names := d[12:p]
	if string(d[p:p+4]) != "XXXX" {
		return 0, nil, errors.New("anmtree: container table marker missing")
	}
	n := uint64(be.Uint32(d[p+4:]))
	p += 8
	if n > (uint64(len(d))-p)/12 || p+12*n+4 > uint64(len(d)) {
		return 0, nil, errors.New("anmtree: container table exceeds the file")
	}
	total := uint64(be.Uint32(d[p+12*n:]))
	base := p + 12*n + 4
	if base+total != uint64(len(d)) {
		return 0, nil, errors.New("anmtree: container size mismatch")
	}
	for i := uint64(0); i < n; i++ {
		r := d[p+12*i:]
		no, off, sz := uint64(be.Uint32(r)), uint64(be.Uint32(r[4:])), uint64(be.Uint32(r[8:]))
		if no >= uint64(len(names)) || off+sz > total {
			return 0, nil, errors.New("anmtree: container entry out of range")
		}
		k := bytes.IndexByte(names[no:], 0)
		if k < 0 {
			return 0, nil, errors.New("anmtree: container name not terminated")
		}
		trees = append(trees, Entry{string(names[no : no+uint64(k)]), d[base+off : base+off+sz]})
	}
	return version, trees, nil
}

// Find returns the named tree of a container (exact name, as stored).
func Find(container []byte, name string) ([]byte, error) {
	_, ts, err := Split(container)
	if err != nil {
		return nil, err
	}
	for _, e := range ts {
		if e.Name == name {
			return e.Data, nil
		}
	}
	return nil, fmt.Errorf("anmtree: %s not in the container", name)
}
