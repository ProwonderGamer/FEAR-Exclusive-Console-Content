package model

// Console (.mdl, LDOM v34, big endian) -> PC (.Model00p, MODL v33, little endian) model converter.
// Byte-exact port of the project's Python converter (mdl2model00p convert()). What changes between the versions:
//   - byte order of every field (each by its own width; the animation blob is swapped track by track);
//   - version 34 -> 33; child-model count = names + 1 on PC; child names become relative to the parent model's
//     folder with '.Model00p' (FEAR.exe 0x431730);
//   - render vertices: the PS3 'CMP' 11:11:10 normal/tangent/binormal (decl type 14) becomes a re-normalised
//     FLOAT3, elements are re-ordered into the PC order, an all-white COLOR0 is dropped.
// The Xbox 360 .mdl has the same container; its player_SM15 / player_dualSM15 are byte-identical to the PS3
// ones. Other X360 models store DEC3N normals: converted without Options.DEC3N their output is
// Python-identical but NOT a correct PC model (see the package tests); with it they match the shipped files
// (x360.go).

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// D3DDECLTYPE sizes of the types seen in F.E.A.R. model files; 14 = PS3 RSX 'CMP'.
var dtSize = map[uint8]int{0: 4, 1: 8, 2: 12, 3: 16, 4: 4, 14: 4}

const (
	tFloat3 = 2
	tCMP    = 14
	uPos    = 0
	uBW     = 1
	uBI     = 2
	uNrm    = 3
	uUV     = 5
	uTan    = 6
	uBin    = 7
	uCol    = 10
)

// pcOrder is the PC element order (POS, NRM, UV, TAN, BIN, COL, others, BW, BI).
var pcOrder = map[uint8]int{uPos: 0, uNrm: 1, uUV: 2, uTan: 3, uBin: 4, uCol: 5, uBW: 7, uBI: 8}

// maxOutVertexBytes bounds one converted vertex range (real models are below 2 MB).
const maxOutVertexBytes = 256 << 20

var endElement = Elem{Stream: 0xFF, Offset: 0, Type: 17}

// Options are the optional inputs of Convert.
type Options struct {
	KeepColor bool // keep PS3 COLOR0 even when it is all white (--keep-color)
	// DEC3N reads the packed normal/tangent/binormal words (decl type 14) as Xbox 360 DEC3N 10:10:10 instead
	// of PS3 CMP 11:11:10 (see x360.go). Set it for the X360 models that store DEC3N; the X360 player_SM15 and
	// player_dualSM15 store CMP like the PS3 and are converted without it.
	DEC3N bool
	// PCHas reports whether the stock PC archives hold a file (case-insensitive game path); only the report
	// uses it (child status), never the output bytes. May be nil.
	PCHas func(path string) bool
	// Converted lists the PC paths of the other models converted in the same run (report only).
	Converted []string
}

// Child describes one converted child-model name.
type Child struct {
	PS3, PC, File, Status string // Status: "pc", "converted" or "MISSING"
}

// Stats is the conversion report.
type Stats struct {
	Source             string
	PS3Bytes, PCBytes  int
	Warnings           []string
	Children           []Child
	Color0             []string
	IndexRebasedSecs   int
	UncoveredAnimBytes int
}

// lowerLatin1 is Python's str.lower() for Latin-1 text.
func lowerLatin1(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' || c >= 0xC0 && c <= 0xDE && c != 0xD7 {
			b[i] = c + 0x20
		}
	}
	return string(b)
}

func signExt(v uint32, bits uint) int64 {
	if v&(1<<(bits-1)) != 0 {
		return int64(v) - int64(1)<<bits
	}
	return int64(v)
}

// cmpToFloat3 decodes RSX CMP: x = bits 0-10, y = bits 11-21 (11-bit signed / 1023), z = bits 22-31
// (10-bit signed / 511), then re-normalises in float64 like the Python. The Python takes the length with
// `** 0.5` (C pow, up to 1 ulp off on Windows), Go with math.Sqrt (correctly rounded).
// TestCMPPowSqrtEquivalence checks all 2^32 inputs: a 1-ulp length difference changes the stored float32 only
// for 16 words that share one length, and there the reference Python's pow equals sqrt.
func cmpToFloat3(u uint32) (x, y, z float64) {
	x = float64(signExt(u&0x7FF, 11)) / 1023.0
	y = float64(signExt((u>>11)&0x7FF, 11)) / 1023.0
	z = float64(signExt(u>>22, 10)) / 511.0
	// explicit float64() conversions forbid fused multiply-add (Go may fuse x*y+z on FMA targets, e.g. GOAMD64=v3)
	ln := math.Sqrt(float64(x*x) + float64(y*y) + float64(z*z))
	if ln > 1e-6 {
		x, y, z = x/ln, y/ln, z/ln
	}
	return
}

// swapAnimData swaps the animation blob to little endian using the track sets (FEAR.exe 0x42e2e0 / 0x42e360
// / 0x42ec00): position = 3 x f32, or 3 x u16 when the anim's compressed flag is set; rotation = 4 x u16.
// Returns the swapped blob and the number of bytes no track references (copied unswapped).
func swapAnimData(m *Model) ([]byte, int, error) {
	src := m.AnimData
	out := append([]byte(nil), src...)
	cover := make([]uint8, len(src))
	multi := false
	sw := func(o int64, n int, width int) error {
		size := int64(n * width)
		if o < 0 || o+size > int64(len(src)) {
			return fmt.Errorf("model: animation track outside the data block (%#x+%d)", o, size)
		}
		for i := int64(0); i < int64(n); i++ {
			p := o + i*int64(width)
			for k := 0; k < width; k++ {
				out[p+int64(k)] = src[p+int64(width-1-k)]
			}
		}
		for i := o; i < o+size; i++ {
			if cover[i] != 0 {
				multi = true
			}
			cover[i] = 1
		}
		return nil
	}
	type key struct{ base, ts, nkeys, comp uint32 }
	done := map[key]bool{}
	nn := len(m.Nodes)
	for _, a := range m.Anims {
		h := a.H
		if uint64(h[6]) >= uint64(len(m.TrackSets)) {
			return nil, 0, fmt.Errorf("model: anim track set %d out of range", h[6])
		}
		ts := m.TrackSets[h[6]]
		base, nkeys := int64(h[7]), int64(h[9])
		k := key{h[7], h[6], h[9], h[8]}
		if done[k] { // several anims may share one data block
			continue
		}
		done[k] = true
		posWidth := 4
		if h[8] != 0 {
			posWidth = 2
		}
		for ni := 0; ni < nn; ni++ {
			for j, w := range [2]struct{ n, width int }{{3, posWidth}, {4, 2}} {
				ofs := ts.Tracks[2*ni+j]
				if ofs == 0xFFFF {
					continue
				}
				if ofs&0x8000 != 0 {
					if err := sw(base+int64(ofs&0x7FFF), w.n, w.width); err != nil {
						return nil, 0, err
					}
				} else {
					for kk := int64(0); kk < nkeys; kk++ {
						if err := sw(base+int64(ts.Const)+kk*int64(ts.Frame)+int64(ofs), w.n, w.width); err != nil {
							return nil, 0, err
						}
					}
				}
			}
		}
	}
	if multi {
		return nil, 0, errors.New("model: animation data: bytes referenced by more than one track")
	}
	unc := 0
	for _, c := range cover {
		if c == 0 {
			unc++
		}
	}
	return out, unc, nil
}

type layoutEl struct{ src, dst Elem }

// pcLayout maps a PS3 element list to the PC list. Decls containing CMP elements (PS3 visible meshes) are
// re-ordered into the PC order; others keep their order.
func pcLayout(decl []Elem, keep func(Elem) bool) ([]layoutEl, int, error) {
	var els, kept []Elem
	visible := false
	for _, x := range decl {
		if x.Stream != 0xFF {
			els = append(els, x)
			if x.Type == tCMP {
				visible = true
			}
		}
	}
	for _, x := range els {
		if keep(x) {
			kept = append(kept, x)
		}
	}
	if visible {
		rank := func(x Elem) int {
			if r, ok := pcOrder[x.Usage]; ok {
				return r
			}
			return 6
		}
		sort.SliceStable(kept, func(i, j int) bool {
			ri, rj := rank(kept[i]), rank(kept[j])
			if ri != rj {
				return ri < rj
			}
			return kept[i].UsageIdx < kept[j].UsageIdx
		})
	}
	var out []layoutEl
	ofs := 0
	for _, x := range kept {
		t := x.Type
		if t == tCMP {
			t = tFloat3
		}
		sz, ok := dtSize[t]
		if !ok {
			return nil, 0, fmt.Errorf("model: vertex element type %d not supported", t)
		}
		if ofs > 0xFFFF {
			return nil, 0, errors.New("model: vertex element offset overflow")
		}
		out = append(out, layoutEl{x, Elem{Stream: 0, Offset: uint16(ofs), Type: t, Method: x.Method, Usage: x.Usage, UsageIdx: x.UsageIdx}})
		ofs += sz
	}
	return out, ofs, nil
}

// packedDecoder decodes one packed (decl type 14) word to a float3: cmpToFloat3 (PS3) or dec3nToFloat3 (X360).
type packedDecoder func(uint32) (x, y, z float64)

func convertRange(src []byte, sOff, sStride, count int64, layout []layoutEl, dStride int, dec packedDecoder) ([]byte, error) {
	out := make([]byte, int64(dStride)*count)
	for k := int64(0); k < count; k++ {
		s := sOff + k*sStride
		d := k * int64(dStride)
		for _, l := range layout {
			q := s + int64(l.src.Offset)
			p := d + int64(l.dst.Offset)
			if l.src.Type == tCMP {
				if q+4 > int64(len(src)) {
					return nil, errors.New("model: vertex read past the vertex buffer")
				}
				x, y, z := dec(binary.BigEndian.Uint32(src[q:]))
				binary.LittleEndian.PutUint32(out[p:], math.Float32bits(float32(x)))
				binary.LittleEndian.PutUint32(out[p+4:], math.Float32bits(float32(y)))
				binary.LittleEndian.PutUint32(out[p+8:], math.Float32bits(float32(z)))
			} else { // FLOATn and D3DCOLOR: dword-wise swap
				n := int64(dtSize[l.src.Type])
				if q+n > int64(len(src)) {
					return nil, errors.New("model: vertex read past the vertex buffer")
				}
				for i := int64(0); i < n; i += 4 {
					binary.LittleEndian.PutUint32(out[p+i:], binary.BigEndian.Uint32(src[q+i:]))
				}
			}
		}
	}
	return out, nil
}

// convertMesh converts one non-external render mesh in place (vertex buffer, decls, section records, indices).
func convertMesh(ms *Mesh, keepColor bool, dec packedDecoder, st *Stats) error {
	decls, secs, vtx := ms.Decls, ms.Sections, ms.Vtx
	keepCol := make([]bool, len(decls))
	white := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	for di, dcl := range decls {
		var cols []Elem
		for _, x := range dcl {
			if x.Stream != 0xFF && x.Usage == uCol {
				cols = append(cols, x)
			}
		}
		info := false
		for _, x := range cols {
			for _, s := range secs {
				if uint64(s.F[8]) != uint64(di) {
					continue
				}
				stv := uint64(s.F[2])
				base := uint64(s.F[0]) * stv
				for k := uint64(0); k < uint64(s.F[1]); k++ {
					a := base + k*stv + uint64(x.Offset)
					// a slice past the end is shorter than 4 bytes in Python, so it is "not white"
					if a+4 > uint64(len(vtx)) || string(vtx[a:a+4]) != string(white) {
						info = true
						break
					}
				}
				if info {
					break
				}
			}
			if info {
				break
			}
		}
		keepCol[di] = keepColor || info
		if len(cols) > 0 {
			what := "dropped (all 0xFFFFFFFF)"
			if info {
				what = "kept (non-white values)"
			} else if keepColor {
				what = "kept (--keep-color)"
			}
			st.Color0 = append(st.Color0, fmt.Sprintf("decl %d: %s", di, what))
		}
	}
	type lay struct {
		els    []layoutEl
		stride int
	}
	layouts := make([]lay, len(decls))
	for di, dcl := range decls {
		di := di
		els, stride, err := pcLayout(dcl, func(x Elem) bool { return x.Usage != uCol || keepCol[di] })
		if err != nil {
			return err
		}
		layouts[di] = lay{els, stride}
	}
	// Vertex ranges used by the sections, in byte order of the PS3 buffer: a section draws vertices
	// baseVtx + index with index in [minVtx, minVtx + numVtx).
	type rkey struct{ b0, st, di uint64 }
	keyOf := func(f *[9]uint32) rkey {
		return rkey{(uint64(f[4]) + uint64(f[0])) * uint64(f[2]), uint64(f[2]), uint64(f[8])}
	}
	ranges := map[rkey]uint64{}
	for i := range secs {
		k := keyOf(&secs[i].F)
		if n := uint64(secs[i].F[1]); n > ranges[k] {
			ranges[k] = n
		} else if _, ok := ranges[k]; !ok {
			ranges[k] = n
		}
	}
	keys := make([]rkey, 0, len(ranges))
	for k := range ranges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.b0 != b.b0 {
			return a.b0 < b.b0
		}
		if a.st != b.st {
			return a.st < b.st
		}
		return a.di < b.di
	})
	type ns struct{ start, stride uint64 }
	newstart := map[rkey]ns{}
	var out []byte
	var lastEnd uint64
	for _, k := range keys {
		n := ranges[k]
		if k.b0 < lastEnd {
			return errors.New("model: overlapping vertex ranges with different layouts")
		}
		if k.di >= uint64(len(layouts)) {
			return fmt.Errorf("model: section uses vertex declaration %d of %d", k.di, len(layouts))
		}
		l := layouts[k.di]
		srcSize := uint64(0)
		for _, x := range decls[k.di] {
			if x.Stream == 0xFF {
				continue
			}
			sz, ok := dtSize[x.Type]
			if !ok {
				return fmt.Errorf("model: vertex element type %d not supported", x.Type)
			}
			if e := uint64(x.Offset) + uint64(sz); e > srcSize {
				srcSize = e
			}
		}
		if srcSize > k.st {
			return fmt.Errorf("model: section stride %d smaller than decl %d (%d bytes)", k.st, k.di, srcSize)
		}
		if l.stride == 0 {
			return errors.New("model: empty vertex layout")
		}
		ds := uint64(l.stride)
		for uint64(len(out))%ds != 0 { // vertex positions are in units of the section stride
			out = append(out, 0)
		}
		newstart[k] = ns{uint64(len(out)) / ds, ds}
		// the last vertex must start inside the buffer (the Python fails reading it otherwise); this also
		// bounds the allocation below on damaged files
		if n > 0 && k.b0+(n-1)*k.st >= uint64(len(vtx)) {
			return errors.New("model: vertex range outside the vertex buffer")
		}
		if n*ds > maxOutVertexBytes { // only reachable with stride 0 (damaged file); the Python has no limit
			return errors.New("model: vertex range too large")
		}
		conv, err := convertRange(vtx, int64(k.b0), int64(k.st), int64(n), l.els, l.stride, dec)
		if err != nil {
			return err
		}
		out = append(out, conv...)
		lastEnd = k.b0 + n*k.st
	}
	if lastEnd != uint64(len(vtx)) {
		st.Warnings = append(st.Warnings, fmt.Sprintf("vertex bytes %d..%d not referenced by any section (dropped)", lastEnd, len(vtx)))
	}
	n := len(ms.Idx) / 2
	idx := make([]int64, n)
	for i := range idx {
		idx[i] = int64(binary.BigEndian.Uint16(ms.Idx[2*i:]))
	}
	for i := range secs {
		f := &secs[i].F
		nsv := newstart[keyOf(f)]
		delta := int64(nsv.start) - (int64(f[4]) + int64(f[0]))
		if delta != 0 {
			for j := uint64(f[3]); j < uint64(f[3])+3*uint64(f[5]); j++ {
				if j >= uint64(n) {
					return errors.New("model: section index range outside the index buffer")
				}
				idx[j] += delta
				if idx[j] < 0 || idx[j] >= 0x10000 {
					return errors.New("model: index overflow after re-layout")
				}
			}
			st.IndexRebasedSecs++
		}
		nf0 := int64(f[0]) + delta
		if nf0 < 0 || nf0 > math.MaxUint32 {
			return errors.New("model: section first vertex out of range after re-layout")
		}
		f[0] = uint32(nf0)
		f[2] = uint32(nsv.stride)
	}
	ib := make([]byte, 2*n)
	for i, v := range idx {
		binary.LittleEndian.PutUint16(ib[2*i:], uint16(v))
	}
	ms.Idx = ib
	ms.Vtx = out
	nd := make([][]Elem, len(layouts))
	for i, l := range layouts {
		d := make([]Elem, 0, len(l.els)+1)
		for _, e := range l.els {
			d = append(d, e.dst)
		}
		nd[i] = append(d, endElement)
	}
	ms.Decls = nd
	return nil
}

// childNamePC maps a console root-relative child path to the PC name relative to the parent's folder
// (FEAR.exe 0x431730 resolves 'parentDir\name' and supports leading '..\'). The console spelling is kept.
func childNamePC(child, parent string) (rel, full string) {
	base := child
	if strings.HasSuffix(lowerLatin1(child), ".mdl") {
		base = child[:len(child)-4]
	}
	full = base + ".Model00p"
	var pdir []string
	pp := strings.ReplaceAll(parent, "/", "\\")
	if strings.Contains(pp, "\\") {
		pdir = strings.Split(pp[:strings.LastIndex(pp, "\\")], "\\")
	}
	parts := strings.Split(full, "\\")
	i := 0
	for i < len(pdir) && i < len(parts)-1 && lowerLatin1(pdir[i]) == lowerLatin1(parts[i]) {
		i++
	}
	var r []string
	for k := 0; k < len(pdir)-i; k++ {
		r = append(r, "..")
	}
	r = append(r, parts[i:]...)
	return strings.Join(r, "\\"), full
}

// Convert converts a console model (.mdl bytes) to a PC .Model00p. ps3Path is the game-relative console path
// as stored in the catalog (e.g. `chars\models\player_SM15.mdl`); it decides the child-model names.
func Convert(data []byte, ps3Path string, opt Options) ([]byte, *Stats, error) {
	m, err := Parse(data)
	if err != nil {
		return nil, nil, err
	}
	if !m.BigEndian || m.Version != 34 {
		return nil, nil, fmt.Errorf("model: input is not a console v34 model (big endian %v, version %d)", m.BigEndian, m.Version)
	}
	st := &Stats{Source: ps3Path, PS3Bytes: len(data)}
	ad, unc, err := swapAnimData(m)
	if err != nil {
		return nil, nil, err
	}
	m.AnimData = ad
	st.UncoveredAnimBytes = unc
	if unc != 0 {
		st.Warnings = append(st.Warnings, fmt.Sprintf("%d animation-data bytes not referenced by any track (copied unswapped)", unc))
	}
	pcParent := ps3Path
	if strings.HasSuffix(lowerLatin1(ps3Path), ".mdl") {
		pcParent = ps3Path[:len(ps3Path)-4] + ".Model00p"
	}
	conv := map[string]bool{}
	for _, c := range opt.Converted {
		conv[lowerLatin1(c)] = true
	}
	kids := make([]string, 0, len(m.Children))
	for _, c := range m.Children {
		rel, full := childNamePC(c, pcParent)
		status := "MISSING"
		if opt.PCHas != nil && opt.PCHas(full) {
			status = "pc"
		} else if conv[lowerLatin1(full)] {
			status = "converted"
		}
		kids = append(kids, rel)
		st.Children = append(st.Children, Child{PS3: c, PC: rel, File: full, Status: status})
	}
	m.Children = kids
	if m.HasGeometry != 0 {
		dec := packedDecoder(cmpToFloat3)
		if opt.DEC3N {
			dec = dec3nToFloat3
		}
		for i, ms := range m.Render.Meshes {
			if ms.External != 0 {
				st.Warnings = append(st.Warnings, fmt.Sprintf("mesh %d is an external render resource %q (name kept)", i, ms.Name))
				continue
			}
			if err := convertMesh(ms, opt.KeepColor, dec, st); err != nil {
				return nil, nil, err
			}
		}
	}
	out, err := m.Serialize(false, 33)
	if err != nil {
		return nil, nil, err
	}
	// self-check: the output must parse with the PC reader and give the same structure
	chk, err := Parse(out)
	if err != nil {
		return nil, nil, fmt.Errorf("model: converted model does not parse: %w", err)
	}
	if chk.BigEndian || chk.Version != 33 || len(chk.Nodes) != len(m.Nodes) || len(chk.Children) != len(m.Children) {
		return nil, nil, errors.New("model: converted model failed the self-check")
	}
	for i := range chk.Children {
		if chk.Children[i] != m.Children[i] {
			return nil, nil, errors.New("model: converted model failed the self-check (children)")
		}
	}
	st.PCBytes = len(out)
	return out, st, nil
}
