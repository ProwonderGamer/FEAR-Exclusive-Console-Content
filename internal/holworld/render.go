package holworld

// Render block conversion (console .wldclnt -> PC sections 0 and 1): copied from internal/sfodworld (a port of
// convert_render / _augment / convert_sec1 of the project's Python world converter and of its render reader).
// Holiday changes (the Holiday converter, platform 'x360'): packed
// normals are D3D DEC3N (dec3nToFloat3), and convertRender first checks that format against the geometry
// (verifyNormalFormat, normals.go).
// Loader map: FEAR.exe CRenderWorld::Load 0x50d0a0 (header 0x50ce70, mesh 0x512000/0x512f40, section record
// 0x5120e0, world block 0x50cf40/0x5144c0), client sector data 0x458610.
//
// The synthesised vertex inputs (_augment) are computed in float64 with the Python recipe's exact operation
// order (including CPython 3.12's compensated sum() for the dot products) and then rounded to float32, so the
// output is bit-identical to the recipe's.

import (
	"encoding/binary"
	"math"
	"sort"
)

// reader is a bounds-checked cursor in one byte order.
type reader struct {
	d  []byte
	p  int
	bo binary.ByteOrder
}

func (r *reader) need(n int) {
	if n < 0 || r.p+n > len(r.d) {
		fail("truncated render data at %#x (+%d)", r.p, n)
	}
}

func (r *reader) u32() uint32 {
	r.need(4)
	v := r.bo.Uint32(r.d[r.p:])
	r.p += 4
	return v
}

func (r *reader) u32n(n int) []uint32 {
	r.need(4 * n)
	v := make([]uint32, n)
	for i := range v {
		v[i] = r.bo.Uint32(r.d[r.p+4*i:])
	}
	r.p += 4 * n
	return v
}

func (r *reader) u16() uint16 {
	r.need(2)
	v := r.bo.Uint16(r.d[r.p:])
	r.p += 2
	return v
}

func (r *reader) raw(n int) []byte {
	r.need(n)
	v := r.d[r.p : r.p+n]
	r.p += n
	return v
}

func (r *reader) str() string { return string(r.raw(int(r.u16()))) } // u16 length + chars, no NUL

func (r *reader) count(unit int) int {
	v := r.u32()
	if uint64(v)*uint64(unit) > uint64(len(r.d)) {
		fail("implausible render count %d at %#x", v, r.p-4)
	}
	return int(v)
}

// element is a D3DVERTEXELEMENT9 {WORD Stream, WORD Offset, BYTE Type, Method, Usage, UsageIndex}.
type element struct {
	stream, off           uint16
	typ, meth, use, usage byte
}

type sem struct{ use, idx byte }

type mesh struct {
	hdr0, nmat uint32
	vtx, idx   []byte
	decls      [][]element
	secs       [][9]uint32
	extra      [][]byte
	materials  []string
}

type block struct {
	objs  [][9]uint32
	nodes [][][3]uint32
}

type renderData struct {
	h      []uint32
	root   uint32
	meshes []mesh
	groups [][]block
	end    int
}

func readDecl(raw []byte, bo binary.ByteOrder) []element {
	n := len(raw) / 8
	out := make([]element, n)
	for k := 0; k < n; k++ {
		b := raw[8*k:]
		out[k] = element{bo.Uint16(b), bo.Uint16(b[2:]), b[4], b[5], b[6], b[7]}
	}
	return out
}

func readRender(d []byte, p int, bo binary.ByteOrder) *renderData {
	r := &reader{d, p, bo}
	R := &renderData{h: r.u32n(9)}
	R.root = r.u32()
	nm := int(R.h[3])
	if uint64(nm) > uint64(len(d)) {
		fail("implausible mesh count")
	}
	for i := 0; i < nm; i++ {
		var m mesh
		if r.u32() != 0 {
			fail("embedded render mesh (0x511d00) not supported")
		}
		v := r.u32n(4)
		m.hdr0, m.nmat = v[0], v[1]
		m.vtx = r.raw(int(v[2]))
		m.idx = r.raw(int(v[3]))
		nd := r.count(4)
		for k := 0; k < nd; k++ {
			m.decls = append(m.decls, readDecl(r.raw(r.count(1)), bo))
		}
		ns := r.count(36)
		for k := 0; k < ns; k++ {
			var f [9]uint32
			copy(f[:], r.u32n(9))
			if f[7] > 0x18 {
				fail("section extra size %d", f[7])
			}
			m.secs = append(m.secs, f)
			m.extra = append(m.extra, r.raw(int(f[7])))
		}
		if uint64(m.nmat)*2 > uint64(len(d)) {
			fail("implausible material count")
		}
		for k := uint32(0); k < m.nmat; k++ {
			m.materials = append(m.materials, r.str())
		}
		R.meshes = append(R.meshes, m)
	}
	if uint64(R.h[0])*4 > uint64(len(d)) {
		fail("implausible group count")
	}
	for g := uint32(0); g < R.h[0]; g++ {
		nb := r.count(8)
		grp := make([]block, nb)
		for b := range grp {
			no, nn := r.count(36), r.count(1)
			for k := 0; k < no; k++ {
				var o [9]uint32
				copy(o[:], r.u32n(9))
				grp[b].objs = append(grp[b].objs, o)
			}
			for k := 0; k < nn; k++ {
				r.need(1)
				n := int(r.d[r.p])
				r.p++
				pts := make([][3]uint32, n)
				for j := range pts {
					copy(pts[j][:], r.u32n(3))
				}
				grp[b].nodes = append(grp[b].nodes, pts)
			}
		}
		R.groups = append(R.groups, grp)
	}
	R.end = r.p
	return R
}

// renderRequirements: {lower-case PC material name: vertex semantics} as a PC world's render block feeds them.
func renderRequirements(pc []byte) map[string]map[sem]bool {
	if len(pc) < 20 {
		fail("PC world too small")
	}
	R := readRender(pc, int(binary.LittleEndian.Uint32(pc[4:])), binary.LittleEndian)
	req := map[string]map[sem]bool{}
	for _, m := range R.meshes {
		for _, f := range m.secs {
			if int(f[8]) >= len(m.decls) || int(f[6]) >= len(m.materials) {
				fail("PC reference section index out of range")
			}
			k := latin1Lower(m.materials[f[6]])
			s := req[k]
			if s == nil {
				s = map[sem]bool{}
				req[k] = s
			}
			for _, e := range m.decls[f[8]] {
				if e.stream != 0xFF {
					s[sem{e.use, e.usage}] = true
				}
			}
		}
	}
	return req
}

// D3DDECLTYPE sizes: 0 FLOAT1, 1 FLOAT2, 2 FLOAT3, 3 FLOAT4, 4 D3DCOLOR, 14 console packed normal (DEC3N on the Xbox 360).
var dtSize = map[byte]int{0: 4, 1: 8, 2: 12, 3: 16, 4: 4, 14: 4}

func dtPC(t byte) byte {
	if t == 14 { // packed normal/tangent/binormal -> FLOAT3
		return 2
	}
	return t
}

// pcDecl: console element list -> PC element list (packed sequentially) and PC stride.
func pcDecl(decl []element) ([]element, int) {
	out := make([]element, 0, len(decl))
	ofs := 0
	for _, e := range decl {
		if e.stream == 0xFF {
			e.off = 0
			out = append(out, e)
			continue
		}
		if _, ok := dtSize[e.typ]; !ok {
			fail("vertex element type %d", e.typ)
		}
		t2 := dtPC(e.typ)
		out = append(out, element{e.stream, uint16(ofs), t2, e.meth, e.use, e.usage})
		ofs += dtSize[t2]
	}
	return out, ofs
}

func sx(v uint32, bits uint) int64 {
	if v&(1<<(bits-1)) != 0 {
		return int64(v) - int64(1)<<bits
	}
	return int64(v)
}

// cmpToFloat3: PS3 RSX CMP x = bits 0-10, y = 11-21 (11-bit signed / 1023), z = 22-31 (10-bit signed / 511).
// Holiday uses it only as the control format of verifyNormalFormat.
func cmpToFloat3(u uint32) [3]float64 {
	return [3]float64{float64(sx(u&0x7FF, 11)) / 1023.0, float64(sx((u>>11)&0x7FF, 11)) / 1023.0, float64(sx(u>>22, 10)) / 511.0}
}

// dec3nToFloat3 (hol_world2pc dec3n_to_float3): Xbox 360 D3D DEC3N 10:10:10:2, x = bits 0-9, y = 10-19,
// z = 20-29 (10-bit signed / 511), w = bits 30-31 ignored.
func dec3nToFloat3(u uint32) [3]float64 {
	return [3]float64{float64(sx(u&0x3FF, 10)) / 511.0, float64(sx((u>>10)&0x3FF, 10)) / 511.0, float64(sx((u>>20)&0x3FF, 10)) / 511.0}
}

func putF32(b []byte, v float64) { binary.LittleEndian.PutUint32(b, math.Float32bits(float32(v))) }

// convertVertices re-encodes count big-endian console vertices (decl, stride) into the PC layout.
func convertVertices(src []byte, decl []element, stride, count int) ([]byte, []element, int) {
	pc, pstride := pcDecl(decl)
	if (count-1)*stride+stride > len(src) && count > 0 {
		fail("vertex block outside the vertex buffer")
	}
	out := make([]byte, pstride*count)
	for k := 0; k < count; k++ {
		s, d := k*stride, k*pstride
		for i, e := range decl {
			if e.stream == 0xFF {
				continue
			}
			q := s + int(e.off)
			o2 := d + int(pc[i].off)
			n := dtSize[e.typ]
			if q+n > len(src) {
				fail("vertex element outside the vertex buffer")
			}
			switch e.typ {
			case 14:
				f := dec3nToFloat3(binary.BigEndian.Uint32(src[q:])) // unpack_normal with NORMAL_FMT 'dec3n'
				for j := 0; j < 3; j++ {
					putF32(out[o2+4*j:], f[j])
				}
			default: // D3DCOLOR and floats: big-endian dwords -> little-endian dwords
				for j := 0; j < n; j += 4 {
					binary.LittleEndian.PutUint32(out[o2+j:], binary.BigEndian.Uint32(src[q+j:]))
				}
			}
		}
	}
	return out, pc, pstride
}

func packDecl(decl []element) []byte {
	out := make([]byte, 0, 8*len(decl))
	for _, e := range decl {
		out = binary.LittleEndian.AppendUint16(out, e.stream)
		out = binary.LittleEndian.AppendUint16(out, e.off)
		out = append(out, e.typ, e.meth, e.use, e.usage)
	}
	return out
}

func declEqual(a, b []element) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// matNamePC: console material reference -> PC (.mat -> .Mat00).
func matNamePC(n string) string {
	if len(n) >= 4 && latin1Lower(n[len(n)-4:]) == ".mat" {
		return n[:len(n)-4] + ".Mat00"
	}
	return n
}

// synType: element type used for a synthesised semantic (COLOR0 D3DCOLOR, NORMAL/TANGENT/BINORMAL FLOAT3).
func synType(s sem) byte {
	switch s {
	case sem{10, 0}:
		return 4
	case sem{3, 0}, sem{6, 0}, sem{7, 0}:
		return 2
	}
	return 1
}

// pySum3 is CPython 3.12's sum() of three floats starting from the int 0: 0 + a, then Neumaier-compensated
// additions of b and c, then the compensation is added when it is non-zero and finite.
func pySum3(a, b, c float64) float64 {
	f := 0.0 + a
	comp := 0.0
	for _, x := range [2]float64{b, c} {
		t := f + x
		if math.Abs(f) >= math.Abs(x) {
			comp += (f - t) + x
		} else {
			comp += (x - t) + f
		}
		f = t
	}
	if comp != 0 && !math.IsInf(comp, 0) && !math.IsNaN(comp) {
		f += comp
	}
	return f
}

// norm3 mirrors the recipe's _norm: (v / |v|) or nil when |v| <= 1e-12. |v| = (x*x + y*y + z*z) ** 0.5.
func norm3(v [3]float64) (r [3]float64, ok bool) {
	s := float64(float64(float64(v[0]*v[0])+float64(v[1]*v[1])) + float64(v[2]*v[2]))
	l := math.Sqrt(s)
	if !(l > 1e-12) {
		return r, false
	}
	return [3]float64{v[0] / l, v[1] / l, v[2] / l}, true
}

func getF32(b []byte) float64 { return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))) }

// augment appends the synthesised elements need to count PC vertices (see the recipe's _augment: COLOR0 =
// 0xFFFFFFFF, NORMAL0 from area-weighted face normals, TANGENT0/BINORMAL0 from per-triangle UV gradients,
// Gram-Schmidt orthogonalised against the normal).
func augment(vb []byte, decl []element, stride, count int, need []sem, tris [][3]int) ([]byte, []element, int) {
	type ot struct {
		off int
		typ byte
	}
	el := map[sem]ot{}
	for _, e := range decl {
		if e.stream != 0xFF {
			el[sem{e.use, e.usage}] = ot{int(e.off), e.typ}
		}
	}
	pos, ok := el[sem{0, 0}]
	if !ok {
		fail("vertex block without POSITION")
	}
	if count*stride > len(vb) {
		fail("vertex block too short")
	}
	P := make([][3]float64, count)
	for k := range P {
		for i := 0; i < 3; i++ {
			P[k][i] = getF32(vb[k*stride+pos.off+4*i:])
		}
	}
	uvE, hasUV := el[sem{5, 0}]
	var UV [][2]float64
	if hasUV {
		UV = make([][2]float64, count)
		for k := range UV {
			for i := 0; i < 2; i++ {
				UV[k][i] = getF32(vb[k*stride+uvE.off+4*i:])
			}
		}
	}
	accN := make([][3]float64, count)
	accT := make([][3]float64, count)
	accB := make([][3]float64, count)
	for _, tr := range tris {
		a, b, c := tr[0], tr[1], tr[2]
		if a >= count || b >= count || c >= count || a < 0 || b < 0 || c < 0 {
			fail("triangle index outside its vertex block")
		}
		var e1, e2 [3]float64
		for i := 0; i < 3; i++ {
			e1[i] = P[b][i] - P[a][i]
			e2[i] = P[c][i] - P[a][i]
		}
		fn := [3]float64{
			float64(e1[1]*e2[2]) - float64(e1[2]*e2[1]),
			float64(e1[2]*e2[0]) - float64(e1[0]*e2[2]),
			float64(e1[0]*e2[1]) - float64(e1[1]*e2[0]),
		}
		for _, v := range [3]int{a, b, c} {
			for i := 0; i < 3; i++ {
				accN[v][i] += fn[i]
			}
		}
		if len(UV) > 0 {
			d1 := [2]float64{UV[b][0] - UV[a][0], UV[b][1] - UV[a][1]}
			d2 := [2]float64{UV[c][0] - UV[a][0], UV[c][1] - UV[a][1]}
			r := float64(d1[0]*d2[1]) - float64(d2[0]*d1[1])
			if math.Abs(r) > 1e-12 {
				var t, bt [3]float64
				for i := 0; i < 3; i++ {
					t[i] = (float64(e1[i]*d2[1]) - float64(e2[i]*d1[1])) / r
					bt[i] = (float64(e2[i]*d1[0]) - float64(e1[i]*d2[0])) / r
				}
				for _, v := range [3]int{a, b, c} {
					for i := 0; i < 3; i++ {
						accT[v][i] += t[i]
						accB[v][i] += bt[i]
					}
				}
			}
		}
	}
	N := make([][3]float64, count)
	if nE, ok := el[sem{3, 0}]; ok {
		for k := range N {
			for i := 0; i < 3; i++ {
				N[k][i] = getF32(vb[k*stride+nE.off+4*i:])
			}
		}
	} else {
		for k := range N {
			if n, ok := norm3(accN[k]); ok {
				N[k] = n
			} else {
				N[k] = [3]float64{0, 0, 1}
			}
		}
	}
	cross := func(a, b [3]float64) [3]float64 {
		return [3]float64{
			float64(a[1]*b[2]) - float64(a[2]*b[1]),
			float64(a[2]*b[0]) - float64(a[0]*b[2]),
			float64(a[0]*b[1]) - float64(a[1]*b[0]),
		}
	}
	T := make([][3]float64, count)
	B := make([][3]float64, count)
	for k := 0; k < count; k++ {
		n := N[k]
		t := accT[k]
		dt := pySum3(float64(t[0]*n[0]), float64(t[1]*n[1]), float64(t[2]*n[2]))
		tn, ok := norm3([3]float64{t[0] - float64(n[0]*dt), t[1] - float64(n[1]*dt), t[2] - float64(n[2]*dt)})
		if !ok { // no usable UV gradient: any perpendicular
			a := [3]float64{0, 1, 0}
			if math.Abs(n[0]) < 0.9 {
				a = [3]float64{1, 0, 0}
			}
			if tn, ok = norm3(cross(a, n)); !ok {
				fail("cannot build a tangent")
			}
		}
		b := accB[k]
		db := pySum3(float64(b[0]*n[0]), float64(b[1]*n[1]), float64(b[2]*n[2]))
		dbt := pySum3(float64(b[0]*tn[0]), float64(b[1]*tn[1]), float64(b[2]*tn[2]))
		var bv [3]float64
		for i := 0; i < 3; i++ {
			bv[i] = float64(b[i]-float64(n[i]*db)) - float64(tn[i]*dbt)
		}
		bn, ok := norm3(bv)
		if !ok {
			if bn, ok = norm3(cross(n, tn)); !ok {
				fail("cannot build a binormal")
			}
		}
		T[k], B[k] = tn, bn
	}
	ndecl := make([]element, 0, len(decl)+len(need))
	for _, e := range decl {
		if e.stream != 0xFF {
			ndecl = append(ndecl, e)
		}
	}
	ofs := stride
	sorted := append([]sem(nil), need...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].use != sorted[j].use {
			return sorted[i].use < sorted[j].use
		}
		return sorted[i].idx < sorted[j].idx
	})
	type add struct {
		s   sem
		typ byte
	}
	var adds []add
	for _, s := range sorted {
		typ := synType(s)
		ndecl = append(ndecl, element{0, uint16(ofs), typ, 0, s.use, s.idx})
		adds = append(adds, add{s, typ})
		ofs += dtSize[typ]
	}
	endFound := false
	for _, e := range decl {
		if e.stream == 0xFF {
			ndecl = append(ndecl, e)
			endFound = true
			break
		}
	}
	if !endFound {
		fail("vertex declaration without end marker")
	}
	out := make([]byte, 0, ofs*count)
	f3 := func(v [3]float64) {
		for i := 0; i < 3; i++ {
			out = binary.LittleEndian.AppendUint32(out, math.Float32bits(float32(v[i])))
		}
	}
	for k := 0; k < count; k++ {
		out = append(out, vb[k*stride:(k+1)*stride]...)
		for _, a := range adds {
			switch a.s {
			case sem{10, 0}:
				out = binary.LittleEndian.AppendUint32(out, 0xFFFFFFFF)
			case sem{3, 0}:
				f3(N[k])
			case sem{6, 0}:
				f3(T[k])
			case sem{7, 0}:
				f3(B[k])
			default: // e.g. a missing texture coordinate set
				out = append(out, make([]byte, dtSize[a.typ])...)
			}
		}
	}
	return out, ndecl, ofs
}

type blockKey struct{ base, decl uint32 }

// convertRender converts the X360 .wldclnt to PC section 0 (render) and section 1 (client sector data).
// require: vertex semantics per lower-case PC material (the PC worlds' union plus the shader-family table).
func convertRender(clnt []byte, require map[string]map[sem]bool) ([]byte, []byte) {
	if len(clnt) < 8 || string(clnt[:4]) != "CDLW" || binary.BigEndian.Uint32(clnt[4:]) != 0x71 {
		fail("not a console .wldclnt (magic CDLW, version 0x71)")
	}
	R := readRender(clnt, 8, binary.BigEndian)
	verifyNormalFormat(R) // refuse to convert with the wrong platform's normal format
	le := binary.LittleEndian
	var out []byte
	for _, v := range R.h {
		out = le.AppendUint32(out, v)
	}
	out = le.AppendUint32(out, R.root)
	for _, m := range R.meshes {
		mats := make([]string, len(m.materials))
		for i, n := range m.materials {
			mats[i] = matNamePC(n)
		}
		idx := make([]int, len(m.idx)/2)
		for i := range idx {
			idx[i] = int(binary.BigEndian.Uint16(m.idx[2*i:]))
		}
		for _, f := range m.secs {
			if int(f[8]) >= len(m.decls) || int(f[6]) >= len(mats) {
				fail("section refers to a missing declaration or material")
			}
			lo, hi := uint64(f[3]), uint64(f[3])+3*uint64(f[5])
			if hi > uint64(len(idx)) {
				hi = uint64(len(idx))
			}
			if lo < hi {
				mn, mx := idx[lo], idx[lo]
				for _, v := range idx[lo:hi] {
					mn, mx = min(mn, v), max(mx, v)
				}
				if int64(mn) < int64(f[0])-int64(f[4]) || int64(mx) >= int64(f[0])-int64(f[4])+int64(f[1]) {
					fail("index range of a section")
				}
			}
		}
		// vertex blocks: all sections sharing a base vertex (f[4]) and declaration form one block
		type span struct{ lo, hi uint32 }
		blocks := map[blockKey]span{}
		var keys []blockKey // insertion order
		bsecs := map[blockKey][][9]uint32{}
		for _, f := range m.secs {
			k := blockKey{f[4], f[8]}
			s, ok := blocks[k]
			if !ok {
				s = span{f[0], f[0] + f[1]}
				keys = append(keys, k)
			}
			blocks[k] = span{min(s.lo, f[0]), max(s.hi, f[0]+f[1])}
			bsecs[k] = append(bsecs[k], f)
		}
		decls := make([][]element, len(m.decls))
		for i, dc := range m.decls {
			decls[i], _ = pcDecl(dc)
		}
		strideOf := func(k blockKey) uint64 { return uint64(bsecs[k][0][2]) }
		order := append([]blockKey(nil), keys...)
		sort.SliceStable(order, func(i, j int) bool {
			return uint64(order[i].base)*strideOf(order[i]) < uint64(order[j].base)*strideOf(order[j])
		})
		type nb struct {
			base, stride, decl int
		}
		newbase := map[blockKey]nb{}
		var vbuf []byte
		prevEnd := uint64(0)
		for _, k := range order {
			st := int(strideOf(k))
			s := blocks[k]
			if s.lo < k.base || uint64(k.base)*uint64(st) < prevEnd {
				fail("overlapping vertex blocks")
			}
			prevEnd = uint64(s.hi) * uint64(st)
			if prevEnd > uint64(len(m.vtx)) {
				fail("vertex block outside the vertex buffer")
			}
			cnt := int(s.hi - k.base)
			vb, pcd, pst := convertVertices(m.vtx[int(k.base)*st:int(s.hi)*st], m.decls[k.decl], st, cnt)
			have := map[sem]bool{}
			for _, e := range pcd {
				if e.stream != 0xFF {
					have[sem{e.use, e.usage}] = true
				}
			}
			needSet := map[sem]bool{}
			for _, f := range bsecs[k] {
				for s := range require[latin1Lower(mats[f[6]])] {
					if !have[s] {
						needSet[s] = true
					}
				}
			}
			ndi := int(k.decl)
			if len(needSet) > 0 {
				var tris [][3]int
				for _, f := range bsecs[k] {
					for t := uint64(0); t < uint64(f[5]); t++ {
						o := uint64(f[3]) + 3*t
						if o+3 > uint64(len(idx)) {
							fail("triangle outside the index buffer")
						}
						tris = append(tris, [3]int{idx[o], idx[o+1], idx[o+2]})
					}
				}
				need := make([]sem, 0, len(needSet))
				for s := range needSet {
					need = append(need, s)
				}
				var nd []element
				vb, nd, pst = augment(vb, pcd, pst, cnt, need, tris)
				ndi = -1
				for i, dc := range decls {
					if declEqual(dc, nd) {
						ndi = i
						break
					}
				}
				if ndi < 0 {
					decls = append(decls, nd)
					ndi = len(decls) - 1
				}
			}
			for len(vbuf)%pst != 0 {
				vbuf = append(vbuf, 0)
			}
			newbase[k] = nb{len(vbuf) / pst, pst, ndi}
			vbuf = append(vbuf, vb...)
		}
		out = le.AppendUint32(out, 0)
		out = le.AppendUint32(out, m.hdr0)
		out = le.AppendUint32(out, m.nmat)
		out = le.AppendUint32(out, uint32(len(vbuf)))
		out = le.AppendUint32(out, uint32(2*len(idx)))
		out = append(out, vbuf...)
		for _, v := range idx {
			out = le.AppendUint16(out, uint16(v))
		}
		out = le.AppendUint32(out, uint32(len(decls)))
		for _, dc := range decls {
			raw := packDecl(dc)
			if len(raw) > 0x200 {
				fail("vertex declaration larger than the 0x200-byte stack buffer (FEAR.exe 0x512f40)")
			}
			out = le.AppendUint32(out, uint32(len(raw)))
			out = append(out, raw...)
		}
		out = le.AppendUint32(out, uint32(len(m.secs)))
		for i, f := range m.secs {
			b := newbase[blockKey{f[4], f[8]}]
			if b.stride >= 0x100 || f[6] >= 0x10000 {
				fail("stride/material exceed the u8/u16 section fields (0x5120e0)")
			}
			g := [9]uint32{uint32(b.base) + (f[0] - f[4]), f[1], uint32(b.stride), f[3], uint32(b.base), f[5], f[6], f[7], uint32(b.decl)}
			for _, v := range g {
				out = le.AppendUint32(out, v)
			}
			out = append(out, m.extra[i]...)
		}
		for _, n := range mats {
			out = le.AppendUint16(out, uint16(len(n)))
			out = append(out, n...)
		}
	}
	// world blocks (0x50cf40 / 0x5144c0): all 32-bit except the u8 node point count
	for _, g := range R.groups {
		out = le.AppendUint32(out, uint32(len(g)))
		for _, b := range g {
			out = le.AppendUint32(out, uint32(len(b.objs)))
			out = le.AppendUint32(out, uint32(len(b.nodes)))
			for _, o := range b.objs {
				for _, v := range o {
					out = le.AppendUint32(out, v)
				}
			}
			for _, nd := range b.nodes {
				out = append(out, byte(len(nd)))
				for _, v := range nd {
					for _, w := range v {
						out = le.AppendUint32(out, w)
					}
				}
			}
		}
	}
	return out, convertSec1(clnt, R.end)
}

// convertSec1: client sector data (FEAR.exe 0x458610): header 8 u32 (H0 nB, H1 nA, H2 nC), A 0x458de0,
// B 0x458e80, C 0x458cf0. Every field is 16/32-bit except the volume names.
func convertSec1(d []byte, p int) []byte {
	x := &walker{d: d, p: p, in: binary.BigEndian, write: true}
	x.need(32)
	h := make([]uint32, 8)
	for i := range h {
		h[i] = binary.BigEndian.Uint32(d[p+4*i:])
	}
	x.u32s(8)
	for i := 0; i < x.cnt(h[1], 2); i++ {
		n := int(x.u16s(1))
		x.u32s(3*n + 4 + 3 + 1)
	}
	for i := 0; i < x.cnt(h[0], 2); i++ {
		x.need(2)
		n := int(binary.BigEndian.Uint16(d[x.p:]))
		x.u16s(1)
		x.copyN(n)
		x.u32s(6)
		if k := x.cnt(x.u32(), 16); k > 0 {
			x.u32s(4 * k)
		}
		if k := x.cnt(x.u32(), 4); k > 0 {
			x.u32s(k)
		}
		x.u32()
	}
	for i := 0; i < x.cnt(h[2], 4); i++ {
		if k := x.cnt(x.u32(), 4); k > 0 {
			x.u32s(k)
		}
		x.u32s(4)
	}
	if x.p != len(d) {
		fail("client sector data ends at %#x, file at %#x", x.p, len(d))
	}
	return x.o
}
