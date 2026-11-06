package holworld

// Core (.wld) and blind-data conversion: copied from internal/sfodworld (a port of convert_core / convert_blind /
// build_assetlist, unchanged in the Holiday converter, and of the PC-side readers of the project's Python
// world library that the converter uses to read PC Admin's asset list). The Xbox 360 .wld uses the same
// big-endian layout as the PS3 one.
//
// Every block is walked the way the PC loaders read it (FEAR.exe addresses in the comments), reading the source
// byte order and writing little endian. Floats are moved as raw 32-bit words, nothing is rounded.

import (
	"encoding/binary"
	"fmt"
)

// xorPC is the count obfuscation key of PC worlds: 0x71 + sum("FEAR") (FEAR.exe 0x479390).
const xorPC = 0x71 + 'F' + 'E' + 'A' + 'R'

// errFormat is raised (as a panic value) by the walkers; Build turns it into an error.
type errFormat struct{ msg string }

func fail(f string, a ...any) { panic(errFormat{fmt.Sprintf(f, a...)}) }

// walker reads d from p in byte order in; when write is set it appends every value little endian to o.
// pcCounts: the source stores the core counts XOR'd (a PC world); otherwise they are plain (console).
type walker struct {
	d        []byte
	p        int
	o        []byte
	in       binary.ByteOrder
	write    bool
	pcCounts bool
}

func (x *walker) need(n int) {
	if n < 0 || x.p < 0 || x.p+n > len(x.d) {
		fail("truncated data at %#x (+%d)", x.p, n)
	}
}

// u32s converts n dwords and returns the first one.
func (x *walker) u32s(n int) uint32 {
	x.need(4 * n)
	var first uint32
	for i := 0; i < n; i++ {
		v := x.in.Uint32(x.d[x.p:])
		if i == 0 {
			first = v
		}
		x.p += 4
		if x.write {
			x.o = binary.LittleEndian.AppendUint32(x.o, v)
		}
	}
	return first
}

func (x *walker) u32() uint32 { return x.u32s(1) }

func (x *walker) u16s(n int) uint16 {
	x.need(2 * n)
	var first uint16
	for i := 0; i < n; i++ {
		v := x.in.Uint16(x.d[x.p:])
		if i == 0 {
			first = v
		}
		x.p += 2
		if x.write {
			x.o = binary.LittleEndian.AppendUint16(x.o, v)
		}
	}
	return first
}

// count reads one of the 8 core counts and returns its plain value; the PC output stores it XOR'd.
func (x *walker) count() uint32 {
	x.need(4)
	v := x.in.Uint32(x.d[x.p:])
	x.p += 4
	if x.pcCounts {
		v ^= xorPC
	}
	if x.write {
		x.o = binary.LittleEndian.AppendUint32(x.o, v^xorPC)
	}
	return v
}

func (x *walker) copyN(n int) {
	x.need(n)
	if x.write {
		x.o = append(x.o, x.d[x.p:x.p+n]...)
	}
	x.p += n
}

// cnt converts a count read from the file to int, refusing values that cannot fit in the remaining data.
func (x *walker) cnt(v uint32, unit int) int {
	if uint64(v)*uint64(unit) > uint64(len(x.d)) {
		fail("implausible count %d at %#x", v, x.p)
	}
	return int(v)
}

// tree: world tree @0x47abb0: bbox, u32 n, u32 ?, quadtree bit stream (bytes, identical on both platforms).
func (x *walker) tree() {
	x.u32s(6)
	x.u32s(2)
	start := x.p
	b, bit := byte(0), 8
	var node func()
	node = func() {
		if bit == 8 {
			x.need(1)
			b = x.d[x.p]
			x.p++
			bit = 0
		}
		v := (b >> bit) & 1
		bit++
		if v != 0 {
			for i := 0; i < 4; i++ {
				node()
			}
		}
	}
	node()
	end := x.p
	x.p = start
	x.copyN(end - start)
}

// worldModel @0x47b280.
func (x *walker) worldModel() {
	x.need(20)
	x.u32() // flags
	nv := x.cnt(x.u32(), 12)
	npoly := x.cnt(x.u32(), 1)
	x.u32() // nidx
	nnode := x.cnt(x.u32(), 12)
	x.u32s(6) // min, max
	x.u32()   // root
	x.need(npoly)
	counts := append([]byte(nil), x.d[x.p:x.p+npoly]...)
	x.copyN(npoly)
	for _, c := range counts {
		x.copyN(2) // u8, u8
		x.u16s(1)  // u16 surface flags
		x.u32s(2)  // normal index, dist
		if c != 0 {
			x.u32s(int(c))
		}
	}
	if nnode > 0 {
		x.u32s(3 * nnode)
	}
	if nv > 0 {
		x.u32s(3 * nv)
	}
}

// shape: physics shape factory @0x44ccc0.
func (x *walker) shape() {
	t := x.u32()
	if t == 0 || t > 8 {
		return
	}
	switch t {
	case 1: // mesh @0x43c820
		x.u32s(16)
		x.need(8)
		nv := x.cnt(x.u32(), 12)
		ni := x.cnt(x.u32(), 2)
		if nv > 0 {
			x.u32s(3 * nv)
		}
		if ni > 0 {
			if nv < 0x10000 {
				x.u16s(ni)
			} else {
				x.u32s(ni)
			}
		}
		x.need(20)
		msz := x.cnt(binary.LittleEndian.Uint32(x.d[x.p+16:]), 1) // hkMoppCode: little endian on both platforms
		x.copyN(20 + msz)
	case 2:
		x.u32s(6)
	case 3:
		x.u32s(3)
	case 4: // convex @0x417a40
		x.u32s(16)
		x.need(8)
		nv := x.cnt(x.u32(), 12)
		npl := x.cnt(x.u32(), 16)
		if nv > 0 {
			x.u32s(3 * nv)
		}
		if npl > 0 {
			x.u32s(4 * npl)
		}
	case 5:
		x.u32s(7)
		x.shape()
	case 6:
		n := x.cnt(x.u32(), 4)
		for i := 0; i < n; i++ {
			x.shape()
		}
	case 7:
		x.u32s(9)
	case 8:
		x.shape()
		x.shape()
	}
}

// coreToAssetList walks the core from the bounding box up to (not including) the asset list header.
func (x *walker) coreToAssetList() {
	x.u32s(6) // bbox
	x.u32s(3) // world offset
	x.tree()
	var c [8]uint32
	for i := range c {
		c[i] = x.count()
	}
	nnames, namebytes, nnormals, nwm := x.cnt(c[0], 8), x.cnt(c[1], 1), x.cnt(c[2], 12), x.cnt(c[3], 1)
	x.copyN(namebytes)
	if nnames > 0 {
		x.u32s(2 * nnames)
	}
	if nnormals > 0 {
		x.u32s(3 * nnormals)
	}
	for i := 0; i < nwm; i++ {
		x.worldModel()
	}
	// blockers @0x477e70
	n := x.cnt(x.u32(), 20)
	x.u32() // total vertices
	for i := 0; i < n; i++ {
		x.u32s(4)
		if k := x.cnt(x.u32(), 12); k > 0 {
			x.u32s(3 * k)
		}
	}
	// physics shapes @0x425650
	n = x.cnt(x.u32(), 32)
	for i := 0; i < n; i++ {
		x.u32s(7)
		x.shape()
	}
}

// assetPair is one precache entry (directory, file name), strings as stored (Latin-1 bytes).
type assetPair struct{ dir, file string }

// readAssetList reads the asset list (@0x47d480) at x.p without writing: hdrWords is 12 on the consoles, 11 on PC.
func (x *walker) readAssetList(hdrWords int) []assetPair {
	x.need(4 * hdrWords)
	h := make([]uint32, hdrWords)
	for i := range h {
		h[i] = x.in.Uint32(x.d[x.p+4*i:])
	}
	x.p += 4 * hdrWords
	if h[3] != 0 || h[5] != 0 || h[8] != 0 {
		fail("asset-list sub-arrays present (streaming world)")
	}
	bs := x.cnt(h[0], 1)
	x.need(bs)
	blob := x.d[x.p : x.p+bs]
	x.p += bs
	x.need(4)
	np := x.cnt(x.in.Uint32(x.d[x.p:]), 8)
	x.p += 4
	x.need(8 * np)
	cs := func(o uint32) string {
		if uint64(o) >= uint64(len(blob)) {
			fail("asset-list string offset outside the blob")
		}
		for i := int(o); i < len(blob); i++ {
			if blob[i] == 0 {
				return string(blob[o:i])
			}
		}
		fail("unterminated asset-list string")
		return ""
	}
	pairs := make([]assetPair, np)
	for i := range pairs {
		a, b := x.in.Uint32(x.d[x.p:]), x.in.Uint32(x.d[x.p+4:])
		x.p += 8
		pairs[i] = assetPair{cs(a), cs(b)}
	}
	return pairs
}

// convertCore converts the console .wld core (bbox up to the trailer) to PC bytes. al builds the PC asset list from
// the console pairs. It returns the PC core bytes and the offset of the blind data in the .wld.
func convertCore(wld []byte, al func([]assetPair) []byte) ([]byte, int) {
	if len(wld) < 8 || string(wld[:4]) != "PDLW" || binary.BigEndian.Uint32(wld[4:]) != 0x71 {
		fail("not a console .wld (magic PDLW, version 0x71)")
	}
	x := &walker{d: wld, p: 8, in: binary.BigEndian, write: true}
	x.coreToAssetList()
	pairs := x.readAssetList(12)
	x.o = append(x.o, al(pairs)...)
	// particle blockers @0x478de0
	n := x.cnt(x.u32(), 20)
	for i := 0; i < n; i++ {
		x.u32s(4)
		if k := x.cnt(x.u32(), 12); k > 0 {
			x.u32s(3 * k)
		}
	}
	x.u32() // trailer
	return x.o, x.p
}

// pcAssetList returns the precache list of a PC .World00p (worldcore read_core / read_blockers / read_shapes /
// read_assetlist).
func pcAssetList(pc []byte) []assetPair {
	if len(pc) < 20 || binary.LittleEndian.Uint32(pc) != 0x71 {
		fail("not a PC .World00p (version 0x71)")
	}
	x := &walker{d: pc, p: 20, in: binary.LittleEndian, pcCounts: true}
	x.coreToAssetList()
	return x.readAssetList(11)
}

// buildAssetList writes the PC asset list @0x47d480: 11 x u32 header {blobSize,0,0,0,nPairs,0,0,0,0,-1,0},
// string blob (deduplicated, NUL-terminated, padded to 4), u32 n, n x (dirOfs, fileOfs).
func buildAssetList(pairs []assetPair) []byte {
	var blob []byte
	ofs := map[string]uint32{}
	put := func(s string) uint32 {
		if o, ok := ofs[s]; ok {
			return o
		}
		o := uint32(len(blob))
		ofs[s] = o
		blob = append(blob, s...)
		blob = append(blob, 0)
		for len(blob)%4 != 0 { // the blob stays 4-aligned, so this pads s+NUL to a multiple of 4
			blob = append(blob, 0)
		}
		return o
	}
	tab := make([][2]uint32, len(pairs))
	for i, p := range pairs {
		tab[i] = [2]uint32{put(p.dir), put(p.file)}
	}
	le := binary.LittleEndian
	var out []byte
	for _, v := range []uint32{uint32(len(blob)), 0, 0, 0, uint32(len(tab)), 0, 0, 0, 0, 0xFFFFFFFF, 0} {
		out = le.AppendUint32(out, v)
	}
	out = append(out, blob...)
	out = le.AppendUint32(out, uint32(len(tab)))
	for _, t := range tab {
		out = le.AppendUint32(out, t[0])
		out = le.AppendUint32(out, t[1])
	}
	return out
}

// Blind data types (FEAR.exe 0x479b20 consumers).
const (
	blindKeyframer = 0x6aaf0884 // GameServer 0x10112960
	blindBreakable = 0xa85      // GameClient 0x100fbcc0
	blindNavMesh   = 0x83f47c31 // GameServer 0x1005fe20 / 0x100612d0
)

// navArrays: element sizes (dwords) of the 12 counted nav-mesh arrays, GameServer 0x100612d0.
var navArrays = [...]int{18, 1, 1, 3, 18, 1, 7, 4, 1, 3, 3, 2}

func swapWords(dst, src []byte) {
	if len(src)%4 != 0 {
		fail("blind segment not a whole number of dwords")
	}
	for i := 0; i+4 <= len(src); i += 4 {
		binary.LittleEndian.PutUint32(dst[i:], binary.BigEndian.Uint32(src[i:]))
	}
}

// convertBlind converts the blind object data at p (u32 count, u32 blobSize, blob, count x {size, type, ofs}).
func convertBlind(wld []byte, p int) []byte {
	be := binary.BigEndian
	if p+8 > len(wld) {
		fail("truncated blind data")
	}
	cnt, bsz := uint64(be.Uint32(wld[p:])), uint64(be.Uint32(wld[p+4:]))
	if uint64(p)+8+bsz+12*cnt != uint64(len(wld)) {
		fail("blind data does not end at the end of the .wld")
	}
	blob := wld[p+8 : p+8+int(bsz)]
	out := append([]byte(nil), blob...)
	et := p + 8 + int(bsz)
	for i := 0; i < int(cnt); i++ {
		sz, t, o := uint64(be.Uint32(wld[et+12*i:])), be.Uint32(wld[et+12*i+4:]), uint64(be.Uint32(wld[et+12*i+8:]))
		if o+sz > uint64(len(blob)) {
			fail("blind segment outside the blob")
		}
		seg, dst := blob[o:o+sz], out[o:o+sz]
		switch t {
		case blindKeyframer: // {u32 ver, u32 nKeys, u32 keysEnd} + keys; trailing strings stay bytes
			if len(seg) < 12 {
				fail("short keyframer")
			}
			ver, n, end := be.Uint32(seg), uint64(be.Uint32(seg[4:])), uint64(be.Uint32(seg[8:]))
			if ver != 1 || end != 12+36*n || end > uint64(len(seg)) {
				fail("keyframer header %d %d %d", ver, n, end)
			}
			swapWords(dst, seg[:end])
		case blindBreakable: // u32 n; n x {9 x u32, u32 nv, nv x 5 x f32}: all dwords
			if len(seg) < 4 {
				fail("short breakable")
			}
			q := uint64(4)
			for k := uint32(0); k < be.Uint32(seg); k++ {
				if q+40 > uint64(len(seg)) {
					fail("breakable overruns")
				}
				q += 40 + 20*uint64(be.Uint32(seg[q+36:]))
			}
			if q != uint64(len(seg)) {
				fail("breakable size %d != %d", q, len(seg))
			}
			swapWords(dst, seg)
		case blindNavMesh: // all dwords except the AINode name-string block
			if len(seg) < 12 || be.Uint32(seg[4:]) != 6 {
				fail("nav mesh version")
			}
			q := uint64(12)
			for _, dw := range navArrays {
				if q+4 > uint64(len(seg)) {
					fail("nav mesh overruns")
				}
				q += 4 + 4*uint64(dw)*uint64(be.Uint32(seg[q:]))
			}
			if q+4 > uint64(len(seg)) {
				fail("nav mesh overruns")
			}
			m := uint64(be.Uint32(seg[q:]))
			s0, s1 := q+4, q+4+m
			if s1%4 != 0 || len(seg)%4 != 0 || s1 > uint64(len(seg)) {
				fail("nav mesh string block alignment")
			}
			swapWords(dst[:s0], seg[:s0])
			swapWords(dst[s1:], seg[s1:])
		default:
			fail("blind data type %#x", t)
		}
	}
	le := binary.LittleEndian
	res := make([]byte, 0, 8+len(out)+12*int(cnt))
	res = le.AppendUint32(res, uint32(cnt))
	res = le.AppendUint32(res, uint32(bsz))
	res = append(res, out...)
	for i := 0; i < int(cnt); i++ {
		for k := 0; k < 3; k++ {
			res = le.AppendUint32(res, be.Uint32(wld[et+12*i+4*k:]))
		}
	}
	return res
}
