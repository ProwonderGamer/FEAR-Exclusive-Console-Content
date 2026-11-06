package tex

import (
	"errors"
	"math"
)

// bc4Palette is the 8-entry BC4 palette with integer (floor) interpolation, the variant the mod's normal maps
// were decoded with (checked byte-exact on SM15_Machine_Pistol_N).
func bc4Palette(a0, a1 int) [8]int {
	p := [8]int{a0, a1}
	if a0 > a1 {
		for i := 1; i <= 6; i++ {
			p[i+1] = ((7-i)*a0 + i*a1) / 7
		}
	} else {
		for i := 1; i <= 4; i++ {
			p[i+1] = ((5-i)*a0 + i*a1) / 5
		}
		p[6], p[7] = 0, 255
	}
	return p
}

// bc4Block decodes one 8-byte BC4 block into 16 values (row major 4x4).
func bc4Block(b []byte) [16]int {
	p := bc4Palette(int(b[0]), int(b[1]))
	bits := uint64(b[2]) | uint64(b[3])<<8 | uint64(b[4])<<16 | uint64(b[5])<<24 | uint64(b[6])<<32 | uint64(b[7])<<40
	var v [16]int
	for i := range v {
		v[i] = p[(bits>>(3*i))&7]
	}
	return v
}

// normalZ rebuilds the third normal component from two stored ones: x = 2r/255 - 1, y likewise,
// z = sqrt(max(0, 1 - x^2 - y^2)), byte = round(z*127.5 + 127.5) (no exact halves occur; half up is used).
// The float64(...) conversions keep the compiler from fusing multiply-adds, so every platform rounds the same.
func normalZ(r, g int) byte {
	x := float64(float64(r)/255*2) - 1
	y := float64(float64(g)/255*2) - 1
	s := float64(1-float64(x*x)) - float64(y*y)
	z := math.Sqrt(math.Max(0, s))
	return byte(math.Floor(float64(z*127.5) + 127.5 + 0.5))
}

// DecodeDXN turns linear DXN (BC5) blocks of a w x h level into B,G,R,A texels: R = first block (X),
// G = second block (Y), B = rebuilt Z, A = 255 - the layout of the mod's A8R8G8B8 normal maps.
func DecodeDXN(blocks []byte, w, h int) ([]byte, error) {
	bw, bh := (w+3)/4, (h+3)/4
	if len(blocks) != bw*bh*16 {
		return nil, errors.New("tex: DXN level has the wrong size")
	}
	out := make([]byte, w*h*4)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			o := (by*bw + bx) * 16
			xs, ys := bc4Block(blocks[o:o+8]), bc4Block(blocks[o+8:o+16])
			for i := 0; i < 16; i++ {
				x, y := bx*4+i%4, by*4+i/4
				if x >= w || y >= h {
					continue
				}
				d := (y*w + x) * 4
				out[d], out[d+1], out[d+2], out[d+3] = normalZ(xs[i], ys[i]), byte(ys[i]), byte(xs[i]), 255
			}
		}
	}
	return out, nil
}

// PS3NormalDXT5 is the PS3 normal-map format 0x89: DXT5 blocks (stored as on PC, not swizzled) with X in the
// alpha block and Y in the colour block's green.
const PS3NormalDXT5 = 0x89

// DecodeDXT5Normal turns linear DXT5 blocks of a w x h level into B,G,R,A texels: R = alpha block (BC4 floor
// palette), G = green of the colour block (6-bit value bit-replicated to 8, the two in-between colours
// floor((2a+b)/3), floor((a+2b)/3); c0 <= c1 blocks use floor((a+b)/2) and 0), B = rebuilt Z, A = 255.
// Checked byte-exact on the Watson_SAS-12_N base level.
func DecodeDXT5Normal(blocks []byte, w, h int) ([]byte, error) {
	bw, bh := (w+3)/4, (h+3)/4
	if len(blocks) != bw*bh*16 {
		return nil, errors.New("tex: DXT5 level has the wrong size")
	}
	out := make([]byte, w*h*4)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			o := (by*bw + bx) * 16
			xs := bc4Block(blocks[o : o+8])
			c0 := int(blocks[o+8]) | int(blocks[o+9])<<8
			c1 := int(blocks[o+10]) | int(blocks[o+11])<<8
			idx := uint32(blocks[o+12]) | uint32(blocks[o+13])<<8 | uint32(blocks[o+14])<<16 | uint32(blocks[o+15])<<24
			g0, g1 := (c0>>5)&63, (c1>>5)&63
			e0, e1 := g0<<2|g0>>4, g1<<2|g1>>4
			p := [4]int{e0, e1, (2*e0 + e1) / 3, (e0 + 2*e1) / 3}
			if c0 <= c1 {
				p[2], p[3] = (e0+e1)/2, 0
			}
			for i := 0; i < 16; i++ {
				x, y := bx*4+i%4, by*4+i/4
				if x >= w || y >= h {
					continue
				}
				g := p[(idx>>(2*i))&3]
				d := (y*w + x) * 4
				out[d], out[d+1], out[d+2], out[d+3] = normalZ(xs[i], g), byte(g), byte(xs[i]), 255
			}
		}
	}
	return out, nil
}

// HalveNormal makes the next mip level of a B,G,R,A normal map (w, h even): per 2x2 block the byte means
// m = sum/4 of R, G, B give v = (m - 127.5) / 127.5; v is normalised (divided by sqrt((x*x + y*y) + z*z)) and
// each component stored as roundHalfEven(n*127.5 + 127.5); A is the rounded mean of the alphas (255 here).
// Checked byte-exact on all 10 generated levels of Watson_SAS-12_N. Float64 throughout, in this operation
// order (other orders, float32, or decoding before averaging each miss a few exact .5 ties).
func HalveNormal(src []byte, w, h int) []byte {
	nw, nh := max(1, w/2), max(1, h/2)
	out := make([]byte, nw*nh*4)
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			var s [4]int
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					o := ((2*y+dy)*w + 2*x + dx) * 4
					for k := 0; k < 4; k++ {
						s[k] += int(src[o+k])
					}
				}
			}
			var v [3]float64 // B, G, R order (as stored)
			for k := 0; k < 3; k++ {
				v[k] = (float64(s[k])/4 - 127.5) / 127.5
			}
			ln := math.Sqrt(float64(float64(v[2]*v[2])+float64(v[1]*v[1])) + float64(v[0]*v[0]))
			d := (y*nw + x) * 4
			for k := 0; k < 3; k++ {
				n := 0.0
				if ln > 0 { // never seen; a zero vector is stored as 128
					n = v[k] / ln
				}
				out[d+k] = byte(math.RoundToEven(float64(n*127.5) + 127.5))
			}
			out[d+3] = byte(divRoundEven(s[3], 4))
		}
	}
	return out
}

// FromPS3Normal converts a PS3 0x89 normal map (one stored level) into an A8R8G8B8 DDS with every level down
// to 1x1: the base level from DecodeDXT5Normal, the rest from HalveNormal (Watson_SAS-12_N).
func FromPS3Normal(src []byte) ([]byte, error) {
	t, err := ParsePS3(src)
	if err != nil {
		return nil, err
	}
	if t.Format != PS3NormalDXT5 || t.Mips != 1 || t.W != t.H || !pow2(t.W) || t.W < 4 {
		return nil, errors.New("tex: not a one-level square PS3 0x89 normal map")
	}
	base, err := DecodeDXT5Normal(t.Data, t.W, t.H)
	if err != nil {
		return nil, err
	}
	levels := [][]byte{base}
	for w := t.W; w > 1; w /= 2 {
		levels = append(levels, HalveNormal(levels[len(levels)-1], w, w))
	}
	return joinARGB(t.W, t.H, levels), nil
}

// FromX360Normal converts an X360 DXN normal map with all its stored levels into an A8R8G8B8 DDS
// (SM15_Machine_Pistol_N; see DecodeDXN).
func FromX360Normal(src []byte) ([]byte, error) {
	t, err := ParseX360(src)
	if err != nil {
		return nil, err
	}
	if t.Format != X360DXN {
		return nil, errors.New("tex: not an X360 DXN texture")
	}
	blocks, err := X360Levels(t)
	if err != nil {
		return nil, err
	}
	levels := make([][]byte, len(blocks))
	for i, b := range blocks {
		if levels[i], err = DecodeDXN(b, max(1, t.W>>i), max(1, t.H>>i)); err != nil {
			return nil, err
		}
	}
	return joinARGB(t.W, t.H, levels), nil
}
