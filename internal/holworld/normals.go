package holworld

// Packed-normal format guard: a port of _normal_agreement / verify_normal_format of
// the Holiday converter. It only decides whether the build may run; it never changes the
// output. On the X360 Holiday world 84.9 % of 30,541 flat-adjacent vertices agree with DEC3N (the recipe's run
// reports the same) and 0.1 % with the PS3 11:11:10 format.

import (
	"encoding/binary"
	"math"
)

// normalAgreement: for up to maxSections sections of mesh 0 that carry a type-14 NORMAL element, every vertex
// whose adjacent triangles all share one face normal (flat-adjacent) has its decoded normal compared with that
// face normal. Returns the fraction that agrees (1.0 when nothing was tested) and the number tested.
func normalAgreement(R *renderData, decode func(uint32) [3]float64) (float64, int) {
	const maxSections = 400
	if len(R.meshes) == 0 {
		fail("render block has no mesh")
	}
	m := &R.meshes[0]
	be := binary.BigEndian
	idx := make([]int, len(m.idx)/2)
	for i := range idx {
		idx[i] = int(be.Uint16(m.idx[2*i:]))
	}
	ok, tot, used := 0, 0, 0
	for _, f := range m.secs {
		if int(f[8]) >= len(m.decls) {
			fail("section refers to a missing declaration")
		}
		var nrm, pos []element
		for _, e := range m.decls[f[8]] {
			if e.stream == 0xFF {
				continue
			}
			if e.use == 3 && e.typ == 14 {
				nrm = append(nrm, e)
			}
			if e.use == 0 {
				pos = append(pos, e)
			}
		}
		if len(nrm) == 0 || len(pos) == 0 || f[5] == 0 {
			continue
		}
		used++
		if used > maxSections {
			break
		}
		st, bv := int(f[2]), int(f[4])
		at := func(v, off, n int) int { // byte offset of n bytes of vertex v's element, bounds-checked
			o := (bv+v)*st + off
			if o < 0 || o+n > len(m.vtx) {
				fail("normal check: vertex outside the vertex buffer")
			}
			return o
		}
		P := func(v int) [3]float64 {
			o := at(v, int(pos[0].off), 12)
			return [3]float64{f32be(m.vtx[o:]), f32be(m.vtx[o+4:]), f32be(m.vtx[o+8:])}
		}
		type vacc struct {
			ns [][3]float64
		}
		acc := map[int]*vacc{}
		var order []int
		for t := 0; t < int(f[5]); t++ {
			i0 := int(f[3]) + 3*t
			if i0+3 > len(idx) {
				fail("normal check: index outside the index buffer")
			}
			a, b, c := idx[i0], idx[i0+1], idx[i0+2]
			pa, pb, pc := P(a), P(b), P(c)
			var e1, e2 [3]float64
			for i := range 3 {
				e1[i] = pb[i] - pa[i]
				e2[i] = pc[i] - pa[i]
			}
			n := [3]float64{e1[1]*e2[2] - e1[2]*e2[1], e1[2]*e2[0] - e1[0]*e2[2], e1[0]*e2[1] - e1[1]*e2[0]}
			l := math.Sqrt(pySum3(n[0]*n[0], n[1]*n[1], n[2]*n[2]))
			if l < 1e-9 {
				continue
			}
			n = [3]float64{n[0] / l, n[1] / l, n[2] / l}
			for _, v := range [3]int{a, b, c} {
				x := acc[v]
				if x == nil {
					x = &vacc{}
					acc[v] = x
					order = append(order, v)
				}
				x.ns = append(x.ns, n)
			}
		}
		dot := func(p, q [3]float64) float64 { return pySum3(p[0]*q[0], p[1]*q[1], p[2]*q[2]) }
		for _, v := range order {
			ns := acc[v].ns
			flat := true
			for _, q := range ns {
				if dot(ns[0], q) < 0.9995 {
					flat = false
					break
				}
			}
			if !flat {
				continue
			}
			o := at(v, int(nrm[0].off), 4)
			d := decode(be.Uint32(m.vtx[o:]))
			l := math.Sqrt(pySum3(d[0]*d[0], d[1]*d[1], d[2]*d[2]))
			tot++
			if l > 0.5 && dot(ns[0], d)/l > 0.99 {
				ok++
			}
		}
	}
	if tot == 0 {
		return 1.0, 0
	}
	return float64(ok) / float64(tot), tot
}

func f32be(b []byte) float64 { return float64(math.Float32frombits(binary.BigEndian.Uint32(b))) }

// verifyNormalFormat requires DEC3N to explain at least 60 % of the flat-adjacent vertices AND to beat the PS3
// format by a factor of 3 (min_agree 0.60, min_ratio 3.0), so a world from the wrong platform cannot convert
// silently. It returns the agreement and the vertex count (the recipe's report values).
func verifyNormalFormat(R *renderData) (float64, int) {
	frac, tot := normalAgreement(R, dec3nToFloat3)
	other, _ := normalAgreement(R, cmpToFloat3)
	if tot > 0 && (frac < 0.60 || frac < 3.0*other) {
		fail("packed-normal format dec3n does not fit this world: %.1f %% of %d flat vertices agree (other format %.1f %%)",
			100*frac, tot, 100*other)
	}
	return frac, tot
}
