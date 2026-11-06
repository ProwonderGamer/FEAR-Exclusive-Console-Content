package loadart

// Anti-aliased outline rasterizer: an area-coverage scan converter that follows FreeType's "smooth" renderer
// (src/smooth/ftgrays.c) step for step, so that a glyph outline gives the same 8-bit coverage bytes as Pillow's
// bundled FreeType. Ported from FreeType 2.x ftgrays.c (gray_render_line, gray_render_conic, gray_sweep,
// gray_hline) and FT_Outline_Decompose (src/base/ftoutln.c). FreeType is distributed under the FreeType
// License (FTL, BSD-style with an acknowledgement clause) or GPL-2.0; this port is used under the FTL:
// "Portions of this software are copyright (C) The FreeType Project (www.freetype.org). All rights reserved."
//
// Units: outline points are 26.6 fixed point (1/64 pixel); the rasterizer works in 1/256 pixel (PIXEL_BITS 8),
// so every coordinate is shifted left by 2 (UPSCALE). y grows upwards; the bitmap's first row is the top.

const (
	pixelBits = 8
	onePixel  = 1 << pixelBits
)

type grays struct {
	w, h        int
	area, cover []int64 // per cell (row-major, row 0 = bottom)
	x, y        int64   // current point, 1/256 px
}

func (r *grays) add(ex, ey int64, cover, area int64) {
	if ex < 0 || ey < 0 || ex >= int64(r.w) || ey >= int64(r.h) {
		return // FreeType sends cells outside the clip box to its null cell
	}
	i := int(ey)*r.w + int(ex)
	r.cover[i] += cover
	r.area[i] += area
}

func trunc(v int64) int64 { return v >> pixelBits }
func fract(v int64) int64 { return v & (onePixel - 1) }

// udivPrep and udiv are FT_UDIVPREP / FT_UDIV: FreeType replaces the repeated a/b of a line walk by
// a * (0xFFFFFFFF / b) >> 32 in 64-bit arithmetic. This rounding variant (not the 32-bit-long one) is the one
// that reproduced FreeType 2.14.3 (matplotlib's build) on every unhinted glyph of the four pictures.
func udivPrep(c bool, b int64) uint64 {
	if !c {
		return 0
	}
	return 0xFFFFFFFF / uint64(b)
}

func udiv(a int64, r uint64) int64 { return int64((uint64(a) * r) >> 32) }

// lineTo is gray_render_line (the 64-bit "prod" variant): it walks the cells the segment crosses and adds
// each piece's cover (dy) and area ((dy) * (fx1 + fx2), twice the trapezoid area) to its cell.
func (r *grays) lineTo(toX, toY int64) {
	ey0, ey1 := trunc(r.y), trunc(toY)
	if (ey0 >= int64(r.h) && ey1 >= int64(r.h)) || (ey0 < 0 && ey1 < 0) {
		r.x, r.y = toX, toY
		return
	}
	ex0, ex1 := trunc(r.x), trunc(toX)
	fx1, fy1 := fract(r.x), fract(r.y)
	dx, dy := toX-r.x, toY-r.y
	var cover, area int64 // the current cell's running sums
	flush := func() {
		r.add(ex0, ey0, cover, area)
		cover, area = 0, 0
	}
	switch {
	case ex0 == ex1 && ey0 == ey1:
	case dy == 0:
		// horizontal: only moves to the end cell
		flush()
		ex0, ey0 = ex1, ey1
	case dx == 0:
		if dy > 0 {
			for {
				fy2 := int64(onePixel)
				cover += fy2 - fy1
				area += (fy2 - fy1) * fx1 * 2
				fy1 = 0
				flush()
				ey0++
				if ey0 == ey1 {
					break
				}
			}
		} else {
			for {
				fy2 := int64(0)
				cover += fy2 - fy1
				area += (fy2 - fy1) * fx1 * 2
				fy1 = onePixel
				flush()
				ey0--
				if ey0 == ey1 {
					break
				}
			}
		}
	default:
		prod := dx*fy1 - dy*fx1
		dxr := udivPrep(ex0 != ex1, abs64(dx))
		dyr := udivPrep(ey0 != ey1, abs64(dy))
		for {
			var fx2, fy2 int64
			switch {
			case prod-dx*onePixel > 0 && prod <= 0: // left
				fx2 = 0
				fy2 = udiv(-prod, dxr)
				prod -= dy * onePixel
				cover += fy2 - fy1
				area += (fy2 - fy1) * (fx1 + fx2)
				fx1, fy1 = onePixel, fy2
				flush()
				ex0--
			case prod-dx*onePixel+dy*onePixel > 0 && prod-dx*onePixel <= 0: // up
				prod -= dx * onePixel
				fx2 = udiv(-prod, dyr)
				fy2 = onePixel
				cover += fy2 - fy1
				area += (fy2 - fy1) * (fx1 + fx2)
				fx1, fy1 = fx2, 0
				flush()
				ey0++
			case prod+dy*onePixel >= 0 && prod-dx*onePixel+dy*onePixel <= 0: // right
				prod += dy * onePixel
				fx2 = onePixel
				fy2 = udiv(prod, dxr)
				cover += fy2 - fy1
				area += (fy2 - fy1) * (fx1 + fx2)
				fx1, fy1 = 0, fy2
				flush()
				ex0++
			default: // down
				fx2 = udiv(prod, dyr)
				fy2 = 0
				prod += dx * onePixel
				cover += fy2 - fy1
				area += (fy2 - fy1) * (fx1 + fx2)
				fx1, fy1 = fx2, onePixel
				flush()
				ey0--
			}
			if ex0 == ex1 && ey0 == ey1 {
				break
			}
		}
	}
	fx2, fy2 := fract(toX), fract(toY)
	cover += fy2 - fy1
	area += (fy2 - fy1) * (fx1 + fx2)
	r.add(ex0, ey0, cover, area)
	r.x, r.y = toX, toY
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// conicTo is gray_render_conic: a quadratic arc split into 2^shift lines with exact 32.32 forward differences.
func (r *grays) conicTo(cx, cy, toX, toY int64) {
	p0x, p0y := r.x, r.y
	p1x, p1y := cx, cy
	p2x, p2y := toX, toY
	h := int64(r.h)
	if (trunc(p0y) >= h && trunc(p1y) >= h && trunc(p2y) >= h) || (trunc(p0y) < 0 && trunc(p1y) < 0 && trunc(p2y) < 0) {
		r.x, r.y = p2x, p2y
		return
	}
	bx, by := p1x-p0x, p1y-p0y
	ax, ay := p2x-p1x-bx, p2y-p1y-by
	d := abs64(ax)
	if e := abs64(ay); d < e {
		d = e
	}
	if d <= onePixel/4 {
		r.lineTo(p2x, p2y)
		return
	}
	shift := 0
	for {
		d >>= 2
		shift++
		if d <= onePixel/4 {
			break
		}
	}
	rx, ry := ax<<(33-2*shift), ay<<(33-2*shift)
	qx := bx<<(33-shift) + ax<<(32-2*shift)
	qy := by<<(33-shift) + ay<<(32-2*shift)
	px, py := p0x<<32, p0y<<32
	for n := 1 << shift; n > 0; n-- {
		px += qx
		py += qy
		qx += rx
		qy += ry
		r.lineTo(px>>32, py>>32)
	}
}

// coverageByte is gray_hline's mapping of a cell's doubled area (0..2*256*256) to a byte, non-zero winding.
func coverageByte(a int64) byte {
	c := a >> (pixelBits*2 + 1 - 8)
	if c < 0 {
		c = ^c
	}
	if c >= 256 {
		c = 255
	}
	return byte(c)
}

// Bitmap is an 8-bit coverage bitmap, rows top to bottom; Left/Top place it like FreeType's bitmap glyph
// (Left = pixel column of the first column, Top = pixel row above the baseline of the first row).
type Bitmap struct {
	W, H, Left, Top int
	Pix             []byte
}

// pt is an outline point in 26.6.
type pt struct{ x, y int64 }

// rasterize renders a 26.6 outline (contours as TrueType points) the way ft_smooth_render does: the control
// box is widened to whole pixels, the outline moved to the bitmap origin, and the cells swept row by row.
func rasterize(p []pt, on []bool, ends []int) *Bitmap {
	if len(p) == 0 {
		return &Bitmap{}
	}
	xmin, ymin, xmax, ymax := p[0].x, p[0].y, p[0].x, p[0].y
	for _, q := range p {
		xmin, xmax = min(xmin, q.x), max(xmax, q.x)
		ymin, ymax = min(ymin, q.y), max(ymax, q.y)
	}
	bx0, by0 := xmin>>6, ymin>>6
	bx1, by1 := (xmax+63)>>6, (ymax+63)>>6
	b := &Bitmap{W: int(bx1 - bx0), H: int(by1 - by0), Left: int(bx0), Top: int(by1)}
	b.Pix = make([]byte, b.W*b.H)
	if b.W == 0 || b.H == 0 {
		return b
	}
	r := &grays{w: b.W, h: b.H, area: make([]int64, b.W*b.H), cover: make([]int64, b.W*b.H)}
	q := make([]pt, len(p))
	for i, v := range p {
		q[i] = pt{v.x - bx0*64, v.y - by0*64}
	}
	decompose(q, on, ends, r)
	for y := 0; y < b.H; y++ {
		var c int64
		row := b.Pix[(b.H-1-y)*b.W:]
		for x := 0; x < b.W; x++ {
			i := y*b.W + x
			c += r.cover[i] * (onePixel * 2)
			row[x] = coverageByte(c - r.area[i])
		}
	}
	return b
}

// decompose is FT_Outline_Decompose for TrueType points (on-curve and conic off-curve points only), feeding
// the rasterizer with UPSCALE'd coordinates (<< 2).
func decompose(p []pt, on []bool, ends []int, r *grays) {
	up := func(v pt) (int64, int64) { return v.x << 2, v.y << 2 }
	first := 0
	for _, last := range ends {
		if last < first {
			continue
		}
		vStart, vLast := p[first], p[last]
		i, limit := first, last
		if !on[first] {
			if on[last] {
				vStart = vLast
				limit--
			} else {
				vStart = pt{(vStart.x + vLast.x) / 2, (vStart.y + vLast.y) / 2}
			}
			i--
		}
		r.x, r.y = up(vStart)
		closed := false
		for i < limit {
			i++
			if on[i] {
				r.lineTo(up(p[i]))
				continue
			}
			ctrl := p[i]
			for {
				if i < limit {
					i++
					v := p[i]
					cx, cy := up(ctrl)
					if on[i] {
						tx, ty := up(v)
						r.conicTo(cx, cy, tx, ty)
						break
					}
					mid := pt{(ctrl.x + v.x) / 2, (ctrl.y + v.y) / 2}
					tx, ty := up(mid)
					r.conicTo(cx, cy, tx, ty)
					ctrl = v
					continue
				}
				cx, cy := up(ctrl)
				tx, ty := up(vStart)
				r.conicTo(cx, cy, tx, ty)
				closed = true
				break
			}
			if closed {
				break
			}
		}
		if !closed {
			r.lineTo(up(vStart))
		}
		first = last + 1
	}
}
