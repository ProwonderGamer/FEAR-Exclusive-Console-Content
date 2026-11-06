// The resizing and alpha code in this file (coeffs, clip8, ResampleRaw, Resize, Premultiply, Unpremultiply
// and AlphaComposite) is derived from Pillow 12.1.1 (https://python-pillow.org, the friendly fork of the
// Python Imaging Library): src/libImaging/Resample.c (precompute_coeffs, normalize_coeffs_8bpc, the box and
// Lanczos filters, the 8-bit horizontal and vertical passes and clip8), src/libImaging/AlphaComposite.c
// (ImagingAlphaComposite) and src/libImaging/Convert.c (rgbA2rgba, rgba2rgbA).
//
// Pillow is Copyright (c) 1997-2011 by Secret Labs AB, Copyright (c) 1995-2011 by Fredrik Lundh and
// contributors, and Copyright (c) 2010 by Jeffrey A. Clark and contributors. It is licensed under the
// MIT-CMU License, whose permission notice and disclaimer are reproduced in licenses/Pillow-LICENSE.txt
// (also part of the installer's ui/THIRD_PARTY_LICENSES.txt).
//
// MODIFIED VERSION: the code was translated from C to Go and changed for the F.E.A.R. console content
// installer in 2026-11 (the resampling passes work on 4-channel 8-bit pictures only, and the pictures are
// plain Go slices instead of Pillow's Imaging structures).

// Package imgops holds the picture steps of the mod's Python recipes, rebuilt in plain Go: resizing as
// Pillow does it (Lanczos and box filters, 8-bit fixed point, RGBA through premultiplied alpha) and DXT5
// decoding and encoding. The resize code follows Pillow's Resample.c and Convert.c so that the same
// input gives the same bytes (checked against Pillow 12.1.1 in imgops_test.go); the DXT5 encoder follows
// ImageMagick's DDS writer, which the recipes used (dxt5enc.go; proven byte-exact on the six HUD icons).
package imgops

import (
	"errors"
	"math"
)

// RGBA is an 8-bit picture with 4 bytes per pixel in R, G, B, A order, rows top to bottom.
type RGBA struct {
	W, H int
	Pix  []byte
}

// New returns a transparent black picture.
func New(w, h int) *RGBA { return &RGBA{w, h, make([]byte, w*h*4)} }

// Clone returns a copy.
func (m *RGBA) Clone() *RGBA { return &RGBA{m.W, m.H, append([]byte(nil), m.Pix...)} }

// Paste copies src into m with its top-left corner at (x, y), replacing the pixels (Pillow's paste without a
// mask). Parts outside m are dropped.
func (m *RGBA) Paste(src *RGBA, x, y int) {
	for sy := 0; sy < src.H; sy++ {
		dy := y + sy
		if dy < 0 || dy >= m.H {
			continue
		}
		for sx := 0; sx < src.W; sx++ {
			dx := x + sx
			if dx < 0 || dx >= m.W {
				continue
			}
			copy(m.Pix[(dy*m.W+dx)*4:][:4], src.Pix[(sy*src.W+sx)*4:][:4])
		}
	}
}

// Premultiply converts straight alpha to premultiplied alpha (Pillow RGBA -> RGBa: c*a/255 rounded with the
// MULDIV255 rule).
func Premultiply(m *RGBA) *RGBA {
	o := m.Clone()
	for i := 0; i < len(o.Pix); i += 4 {
		a := uint32(o.Pix[i+3])
		for c := 0; c < 3; c++ {
			t := uint32(o.Pix[i+c])*a + 128
			o.Pix[i+c] = byte(((t >> 8) + t) >> 8)
		}
	}
	return o
}

// Unpremultiply converts premultiplied alpha back (Pillow RGBa -> RGBA: alpha 0 and 255 keep the colour,
// otherwise min(255, c*255/a) with integer division).
func Unpremultiply(m *RGBA) *RGBA {
	o := m.Clone()
	for i := 0; i < len(o.Pix); i += 4 {
		a := uint32(o.Pix[i+3])
		if a == 0 || a == 255 {
			continue
		}
		for c := 0; c < 3; c++ {
			o.Pix[i+c] = byte(min(255, uint32(o.Pix[i+c])*255/a))
		}
	}
	return o
}

// Filter is a Pillow resampling filter.
type Filter int

const (
	Lanczos Filter = iota // Pillow Image.LANCZOS: sinc(x)*sinc(x/3), support 3
	Box                   // Pillow Image.BOX: 1 on (-0.5, 0.5], support 0.5
)

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	x *= math.Pi
	return math.Sin(x) / x
}

func (f Filter) support() float64 {
	if f == Box {
		return 0.5
	}
	return 3
}

func (f Filter) eval(x float64) float64 {
	if f == Box {
		if x > -0.5 && x <= 0.5 {
			return 1
		}
		return 0
	}
	if -3 <= x && x < 3 {
		return sinc(x) * sinc(x/3)
	}
	return 0
}

// precisionBits is Pillow's fixed-point precision for 8-bit images (32 - 8 - 2).
const precisionBits = 22

// coeffs returns, for every output position, the first input index, the tap count and the fixed-point weights
// (Pillow precompute_coeffs + normalize_coeffs_8bpc, box = whole input).
func coeffs(inSize, outSize int, f Filter) (bounds [][2]int, kk [][]int32) {
	scale := float64(inSize) / float64(outSize)
	filterscale := max(scale, 1)
	support := f.support() * filterscale
	bounds = make([][2]int, outSize)
	kk = make([][]int32, outSize)
	for xx := 0; xx < outSize; xx++ {
		center := (float64(xx) + 0.5) * scale
		ss := 1 / filterscale
		xmin := int(center - support + 0.5) // C (int) truncation; the value is >= -0.5 + 0.5 only when clamped below
		if center-support+0.5 < 0 {
			xmin = 0
		}
		xmax := int(center + support + 0.5)
		if xmax > inSize {
			xmax = inSize
		}
		xmax -= xmin
		w := make([]float64, xmax)
		ww := 0.0
		for x := 0; x < xmax; x++ {
			w[x] = f.eval((float64(x+xmin) - center + 0.5) * ss)
			ww += w[x]
		}
		k := make([]int32, xmax)
		for x := range w {
			if ww != 0 {
				w[x] /= ww
			}
			if w[x] < 0 {
				k[x] = int32(-0.5 + w[x]*(1<<precisionBits))
			} else {
				k[x] = int32(0.5 + w[x]*(1<<precisionBits))
			}
		}
		bounds[xx] = [2]int{xmin, xmax}
		kk[xx] = k
	}
	return
}

func clip8(v int64) byte {
	if v >= 1<<precisionBits<<8 {
		return 255
	}
	if v <= 0 {
		return 0
	}
	return byte(v >> precisionBits)
}

// ResampleRaw resizes the four 8-bit channels independently (Pillow's resample of a 4-band image such as
// RGBa): a horizontal pass, its result rounded to 8 bits, then a vertical pass. A pass whose size does not
// change is skipped, as in Pillow.
func ResampleRaw(m *RGBA, w, h int, f Filter) *RGBA {
	cur := m
	if w != m.W {
		b, k := coeffs(m.W, w, f)
		o := New(w, m.H)
		for y := 0; y < m.H; y++ {
			row := m.Pix[y*m.W*4:]
			for xx := 0; xx < w; xx++ {
				xmin, n := b[xx][0], b[xx][1]
				for c := 0; c < 4; c++ {
					s := int64(1 << (precisionBits - 1))
					for x := 0; x < n; x++ {
						s += int64(row[(x+xmin)*4+c]) * int64(k[xx][x])
					}
					o.Pix[(y*w+xx)*4+c] = clip8(s)
				}
			}
		}
		cur = o
	}
	if h != cur.H {
		b, k := coeffs(cur.H, h, f)
		o := New(cur.W, h)
		for yy := 0; yy < h; yy++ {
			ymin, n := b[yy][0], b[yy][1]
			for x := 0; x < cur.W; x++ {
				for c := 0; c < 4; c++ {
					s := int64(1 << (precisionBits - 1))
					for y := 0; y < n; y++ {
						s += int64(cur.Pix[((y+ymin)*cur.W+x)*4+c]) * int64(k[yy][y])
					}
					o.Pix[(yy*cur.W+x)*4+c] = clip8(s)
				}
			}
		}
		cur = o
	}
	if cur == m {
		cur = m.Clone()
	}
	return cur
}

// Resize resizes a straight-alpha RGBA picture as Pillow's Image.resize does for mode RGBA: premultiply,
// resample, unpremultiply.
func Resize(m *RGBA, w, h int, f Filter) *RGBA {
	return Unpremultiply(ResampleRaw(Premultiply(m), w, h, f))
}

// AlphaComposite lays src over dst (same size), as Pillow's Image.alpha_composite (AlphaComposite.c).
func AlphaComposite(dst, src *RGBA) (*RGBA, error) {
	if dst.W != src.W || dst.H != src.H {
		return nil, errors.New("imgops: alpha_composite needs pictures of the same size")
	}
	o := New(dst.W, dst.H)
	const shift = 7
	for i := 0; i < len(o.Pix); i += 4 {
		da, sa := uint32(dst.Pix[i+3]), uint32(src.Pix[i+3])
		if sa == 0 {
			copy(o.Pix[i:i+4], dst.Pix[i:i+4])
			continue
		}
		// blend = dst.a * (255 - src.a); outa255 = src.a * 255 + blend
		blend := da * (255 - sa)
		outa255 := sa*255 + blend
		coef1 := sa * 255 * 255 * (1 << shift) / outa255
		coef2 := 255*(1<<shift) - coef1
		for c := 0; c < 3; c++ {
			t := uint32(src.Pix[i+c])*coef1 + uint32(dst.Pix[i+c])*coef2
			t += 0x80 << shift
			o.Pix[i+c] = byte((((t >> 8) + t) >> 8) >> shift)
		}
		t := outa255 + 0x80
		o.Pix[i+3] = byte(((t >> 8) + t) >> 8)
	}
	return o, nil
}
