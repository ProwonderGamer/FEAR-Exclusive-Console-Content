package imgops

import "math"

// EncodeDXT5 compresses a picture to DXT5 blocks following the rules of ImageMagick's DDS writer with its
// default options (range fit, no alpha weighting), which the mod's recipes used (`convert x.png -define
// dds:compression=dxt5 -define dds:mipmaps=0 x.dds`). That writer is a port of the squish library:
//
//   - per 4x4 block, only texels inside the picture count; texels past the edge (levels under 4 texels) get
//     alpha index 0 and colour index 3 (before the endpoint swap below);
//   - alpha: the 5-value mode (0 and 255 kept apart) and the 7-value mode are both fitted; the 7-value mode is
//     used only when its squared error is strictly smaller;
//   - colour: equal colours are merged (weight = sqrt(count)); one colour uses the single-colour tables
//     (singleColourTables), more use a range fit along the principal axis of the weighted covariance, with
//     float32 arithmetic and the endpoints rounded to the 565 grid;
//   - the endpoint with the larger 565 value is written first (indices remapped), equal endpoints get index 0.
//
// The rules were reconstructed from the squish design, not read from the ImageMagick source; they reproduce
// all six confirmed HUD icons byte for byte, every level down to 1x1 (internal/hudicon TestGolden), which
// covers range-fit blocks, single-colour blocks, both alpha modes and the masked texels of tiny levels.
func EncodeDXT5(m *RGBA) []byte {
	bw, bh := (m.W+3)/4, (m.H+3)/4
	out := make([]byte, 0, bw*bh*16)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			var alphas [16]int
			var mapping [16]int
			var pts []point
			min5, max5, min7, max7 := 255, 0, 255, 0
			for k := 0; k < 16; k++ {
				x, y := bx*4+k%4, by*4+k/4
				if x >= m.W || y >= m.H {
					alphas[k], mapping[k] = -1, -1
					continue
				}
				o := (y*m.W + x) * 4
				a := int(m.Pix[o+3])
				alphas[k] = a
				min7, max7 = min(min7, a), max(max7, a)
				if a != 0 && a < min5 {
					min5 = a
				}
				if a != 255 && a > max5 {
					max5 = a
				}
				p := point{float32(m.Pix[o]) / 255, float32(m.Pix[o+1]) / 255, float32(m.Pix[o+2]) / 255, 1}
				mapping[k] = len(pts)
				for i := range pts {
					if pts[i].x == p.x && pts[i].y == p.y && pts[i].z == p.z {
						pts[i].w += p.w
						mapping[k] = i
						break
					}
				}
				if mapping[k] == len(pts) {
					pts = append(pts, p)
				}
			}
			for i := range pts {
				pts[i].w = float32(math.Sqrt(float64(pts[i].w)))
			}
			out = append(out, encodeAlpha(&alphas, min5, max5, min7, max7)...)
			if len(pts) == 1 {
				out = append(out, singleColourBlock(pts[0], &mapping)...)
			} else {
				out = append(out, rangeFitBlock(pts, &mapping)...)
			}
		}
	}
	return out
}

type point struct{ x, y, z, w float32 }

func fixRange(lo, hi, steps int) (int, int) {
	if hi-lo < steps {
		hi = min(lo+steps, 255)
	}
	if hi-lo < steps {
		lo = max(0, hi-steps)
	}
	return lo, hi
}

// compressAlpha gives every texel the nearest code (first one on a tie); texels past the edge get index 0.
func compressAlpha(lo, hi, steps int, alphas *[16]int) (idx [16]int, err int) {
	var codes [8]int
	codes[0], codes[1], codes[6], codes[7] = lo, hi, 0, 255
	for i := 1; i < steps; i++ {
		codes[i+1] = ((steps-i)*lo + i*hi) / steps
	}
	for k, v := range alphas {
		if v < 0 {
			continue
		}
		least := math.MaxInt
		for j, c := range codes {
			if d := (v - c) * (v - c); d < least {
				least, idx[k] = d, j
			}
		}
		err += least
	}
	return
}

func encodeAlpha(alphas *[16]int, min5, max5, min7, max7 int) []byte {
	if min5 > max5 { // no texel between 0 and 255 exclusive
		min5 = max5
	}
	min5, max5 = fixRange(min5, max5, 5)
	i5, e5 := compressAlpha(min5, max5, 5, alphas)
	min7, max7 = fixRange(min7, max7, 7)
	i7, e7 := compressAlpha(min7, max7, 7, alphas)
	a0, a1, idx := min5, max5, i5
	if e7 < e5 {
		// 7-value mode is read when a0 > a1: write max first and remap (0<->1, 2..7 -> 9-i).
		a0, a1 = max7, min7
		for k, i := range i7 {
			switch i {
			case 0:
				idx[k] = 1
			case 1:
				idx[k] = 0
			default:
				idx[k] = 9 - i
			}
		}
	}
	b := []byte{byte(a0), byte(a1)}
	var bits uint64
	for k := 0; k < 16; k++ {
		bits |= uint64(idx[k]) << (3 * k)
	}
	for i := 0; i < 6; i++ {
		b = append(b, byte(bits>>(8*i)))
	}
	return b
}

// clampToLimit rounds v (C float to integer conversion after + 0.5) and clamps it to 0..limit.
func clampToLimit(v float32, limit int) int {
	r := int(v + 0.5)
	if v+0.5 < 0 {
		r = 0
	}
	return max(0, min(limit, r))
}

func colorTo565(x, y, z float32) uint16 {
	return uint16(clampToLimit(31*x, 31)<<11 | clampToLimit(63*y, 63)<<5 | clampToLimit(31*z, 31))
}

// writeColour writes the two endpoints and the 16 indices (masked texels already set to 3).
func writeColour(sx, sy, sz, ex, ey, ez float32, ind *[16]int) []byte {
	a, b := colorTo565(sx, sy, sz), colorTo565(ex, ey, ez)
	var bits uint32
	for k := 0; k < 16; k++ {
		i := ind[k]
		switch {
		case a < b:
			i = (i ^ 1) & 3
		case a == b:
			i = 0
		}
		bits |= uint32(i) << (2 * k)
	}
	if a < b {
		a, b = b, a
	}
	return []byte{byte(a), byte(a >> 8), byte(b), byte(b >> 8), byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24)}
}

func remap(mapping *[16]int, closest []int) (ind [16]int) {
	for k, m := range mapping {
		if m < 0 {
			ind[k] = 3
		} else {
			ind[k] = closest[m]
		}
	}
	return
}

func rangeFitBlock(pts []point, mapping *[16]int) []byte {
	// Weighted centroid and covariance (float32, operation by operation).
	var total, cx, cy, cz float32
	for _, p := range pts {
		total += p.w
		cx += float32(p.x * p.w)
		cy += float32(p.y * p.w)
		cz += float32(p.z * p.w)
	}
	if total > 1.192092896e-07 {
		cx, cy, cz = cx/total, cy/total, cz/total
	}
	var cov [6]float32
	for _, p := range pts {
		ax, ay, az := p.x-cx, p.y-cy, p.z-cz
		bx, by, bz := float32(p.w*ax), float32(p.w*ay), float32(p.w*az)
		cov[0] += float32(ax * bx)
		cov[1] += float32(ax * by)
		cov[2] += float32(ax * bz)
		cov[3] += float32(ay * by)
		cov[4] += float32(ay * bz)
		cov[5] += float32(az * bz)
	}
	// Principal component: 8 power-iteration steps from (1,1,1), each divided by the largest component.
	vx, vy, vz := float32(1), float32(1), float32(1)
	for i := 0; i < 8; i++ {
		wx := float32(cov[0] * vx)
		wy := float32(cov[1] * vx)
		wz := float32(cov[2] * vx)
		wx = float32(cov[1]*vy) + wx
		wy = float32(cov[3]*vy) + wy
		wz = float32(cov[4]*vy) + wz
		wx = float32(cov[2]*vz) + wx
		wy = float32(cov[4]*vz) + wy
		wz = float32(cov[5]*vz) + wz
		m := float64(max(wx, max(wy, wz)))
		var a float32
		if math.Abs(m) >= 1e-12 {
			a = float32(1 / m)
		} else if m < 0 {
			a = float32(-1 / 1e-12)
		} else {
			a = float32(1 / 1e-12)
		}
		vx, vy, vz = float32(wx*a), float32(wy*a), float32(wz*a)
	}
	dot := func(p point) float32 { return float32(float32(p.x*vx)+float32(p.y*vy)) + float32(p.z*vz) }
	start, end := pts[0], pts[0]
	lo, hi := dot(pts[0]), dot(pts[0])
	for _, p := range pts[1:] {
		if v := dot(p); v < lo {
			start, lo = p, v
		} else if v > hi {
			end, hi = p, v
		}
	}
	snap := func(v, grid float32) float32 {
		v = float32(math.Max(0, math.Min(1, float64(v))))
		return float32(math.Trunc(float64(float32(grid*v)+0.5))) * float32(1/grid)
	}
	sx, sy, sz := snap(start.x, 31), snap(start.y, 63), snap(start.z, 31)
	ex, ey, ez := snap(end.x, 31), snap(end.y, 63), snap(end.z, 31)
	const t1, t2 = float32(1.0 / 3.0), float32(2.0 / 3.0)
	codes := [4][3]float32{{sx, sy, sz}, {ex, ey, ez},
		{float32(sx*t2) + float32(ex*t1), float32(sy*t2) + float32(ey*t1), float32(sz*t2) + float32(ez*t1)},
		{float32(sx*t1) + float32(ex*t2), float32(sy*t1) + float32(ey*t2), float32(sz*t1) + float32(ez*t2)}}
	closest := make([]int, len(pts))
	for i, p := range pts {
		best := float32(1e37)
		for j, c := range codes {
			dx, dy, dz := p.x-c[0], p.y-c[1], p.z-c[2]
			if d := float32(float32(dx*dx)+float32(dy*dy)) + float32(dz*dz); d < best {
				best, closest[i] = d, j
			}
		}
	}
	ind := remap(mapping, closest)
	return writeColour(sx, sy, sz, ex, ey, ez, &ind)
}

// sourceBlock is one entry of a single-colour table: endpoints (5 or 6 bits) and the error to the target.
type sourceBlock struct{ start, end, err int }

// singleColourTables builds squish's lookup tables for 5 and 6 bits (4-colour mode): for every 8-bit target
// and code index (0 = endpoint, 1 = the 2/3-1/3 mix), the first endpoint pair that reaches it exactly, then
// the gaps filled from the neighbours (error + 1 per step) until stable.
func singleColourTables(bits int) [256][2]sourceBlock {
	var v [256][2]sourceBlock
	for t := range v {
		v[t][0].err, v[t][1].err = 255, 255
	}
	n := 1 << bits
	for v1 := 0; v1 < n; v1++ {
		for v2 := 0; v2 < n; v2++ {
			a := v1<<(8-bits) | v1>>(2*bits-8)
			b := v2<<(8-bits) | v2>>(2*bits-8)
			codes := [2]int{a, (2*a + b) / 3}
			for i, t := range codes {
				if s := &v[t][i]; s.err != 0 {
					*s = sourceBlock{v1, v2, 0}
				}
			}
		}
	}
	for {
		stable := true
		for i := 0; i < 2; i++ {
			for t := 0; t < 256; t++ {
				if t != 255 {
					if c, nx := &v[t][i], v[t+1][i]; c.err > nx.err+1 {
						*c = sourceBlock{nx.start, nx.end, nx.err + 1}
						stable = false
					}
				}
				if t != 0 {
					if c, pv := &v[t][i], v[t-1][i]; c.err > pv.err+1 {
						*c = sourceBlock{pv.start, pv.end, pv.err + 1}
						stable = false
					}
				}
			}
		}
		if stable {
			return v
		}
	}
}

var lookup5, lookup6 = singleColourTables(5), singleColourTables(6)

func singleColourBlock(p point, mapping *[16]int) []byte {
	col := [3]int{clampToLimit(255*p.x, 255), clampToLimit(255*p.y, 255), clampToLimit(255*p.z, 255)}
	tables := [3]*[256][2]sourceBlock{&lookup5, &lookup6, &lookup5}
	var src [3]sourceBlock
	index := 0
	maxErr := math.MaxInt
	for i := 0; i < 2; i++ {
		e := 0
		var s [3]sourceBlock
		for c := 0; c < 3; c++ {
			s[c] = tables[c][col[c]][i]
			e += s[c].err * s[c].err
		}
		if e > maxErr { // a tie goes to the later index
			continue
		}
		src, index, maxErr = s, 2*i, e
	}
	closest := []int{index}
	ind := remap(mapping, closest)
	return writeColour(float32(src[0].start)/31, float32(src[1].start)/63, float32(src[2].start)/31,
		float32(src[0].end)/31, float32(src[1].end)/63, float32(src[2].end)/31, &ind)
}
