// Package tex converts console F.E.A.R. textures (.tex from the PS3 or Xbox 360 catalogs) into the PC DDS files
// the mod installs. Every function takes the console file's bytes and returns the finished DDS bytes; the
// results are byte-exact with the files tested in game (golden hashes in tex_test.go).
//
// Console .tex layouts (both big endian):
//
//	PS3 (12-byte header): u8 0; u8 format; u8 mip count; u8 1; u16 width; u16 height; u32 payload size;
//	    then every mip level in turn, each swizzled on its own (Morton order, see ps3Morton).
//	    0x85 = A8R8G8B8 (texel bytes A,R,G,B), 0x86 = DXT1, 0x88 = DXT5 (blocks stored as on PC), 0x89 = normal map.
//	X360 (52-byte header): 28 fixed bytes, then the 6-dword GPU texture fetch constant (dword 0 bit 31 = tiled,
//	    dword 1 bits 0..5 = format, bits 6..7 = endian swap, dword 2 = (width-1) | (height-1) << 13), then the
//	    base level, Xenos-tiled at a width and height padded to 32 texels. Format 6 = k_8_8_8_8 with endian
//	    swap 2 (8in32): after the swap each texel is B,G,R,A.
//
// PC DDS output: 128-byte header (header flags 0xa1007, caps 0x401008, linear size = base level size, mip
// count always written), then the levels. A8R8G8B8 levels are stored B,G,R,A (DDS ARGB masks).
package tex

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// PS3 texture formats used here.
const (
	PS3ARGB = 0x85
	PS3DXT1 = 0x86
	PS3DXT5 = 0x88
)

// DDS header constants (copied from the stock textures and the files tested in game).
const (
	ddsFlags = 0xa1007  // CAPS|HEIGHT|WIDTH|PIXELFORMAT|MIPMAPCOUNT|LINEARSIZE
	ddsCaps  = 0x401008 // COMPLEX|TEXTURE|MIPMAP
)

// DDSHeaderARGB returns the 128-byte header of an uncompressed A8R8G8B8 DDS.
func DDSHeaderARGB(w, h, mips int) []byte {
	b := ddsCommon(w, h, mips, w*h*4)
	le := binary.LittleEndian
	le.PutUint32(b[80:], 0x41) // pixel format flags: ALPHAPIXELS|RGB
	le.PutUint32(b[88:], 32)   // bit count
	le.PutUint32(b[92:], 0xff0000)
	le.PutUint32(b[96:], 0xff00)
	le.PutUint32(b[100:], 0xff)
	le.PutUint32(b[104:], 0xff000000)
	return b
}

// DDSHeaderDXT returns the 128-byte header of a DXT1 or DXT5 DDS ("DXT1"/"DXT5").
func DDSHeaderDXT(w, h, mips int, fourcc string) []byte {
	block := 16
	if fourcc == "DXT1" {
		block = 8
	}
	b := ddsCommon(w, h, mips, max(1, w/4)*max(1, h/4)*block)
	binary.LittleEndian.PutUint32(b[80:], 0x4) // FOURCC
	copy(b[84:88], fourcc)
	return b
}

func ddsCommon(w, h, mips, linear int) []byte {
	b := make([]byte, 128)
	le := binary.LittleEndian
	copy(b, "DDS ")
	le.PutUint32(b[4:], 124)
	le.PutUint32(b[8:], ddsFlags)
	le.PutUint32(b[12:], uint32(h))
	le.PutUint32(b[16:], uint32(w))
	le.PutUint32(b[20:], uint32(linear))
	le.PutUint32(b[28:], uint32(mips))
	le.PutUint32(b[76:], 32) // pixel format size
	le.PutUint32(b[108:], ddsCaps)
	return b
}

// PS3Tex is a parsed PS3 .tex.
type PS3Tex struct {
	Format, Mips int
	W, H         int
	Data         []byte // all levels, as stored
}

// ParsePS3 checks and splits a PS3 .tex.
func ParsePS3(b []byte) (*PS3Tex, error) {
	if len(b) < 12 || b[0] != 0 || b[3] != 1 {
		return nil, errors.New("tex: not a PS3 .tex")
	}
	t := &PS3Tex{Format: int(b[1]), Mips: int(b[2]), W: int(binary.BigEndian.Uint16(b[4:])),
		H: int(binary.BigEndian.Uint16(b[6:])), Data: b[12:]}
	if int(binary.BigEndian.Uint32(b[8:])) != len(t.Data) || t.Mips < 1 || t.W < 1 || t.H < 1 {
		return nil, errors.New("tex: PS3 .tex header does not match its size")
	}
	return t, nil
}

// ps3Morton returns the swizzled texel index of (x, y) in a power-of-two w x h PS3 (RSX) level: the bits of x
// and y are interleaved (x first) while both have bits left, the rest of the longer one follows.
func ps3Morton(x, y, w, h int) int {
	o, b := 0, 0
	xb, yb := log2(w), log2(h)
	for i, j := 0, 0; i < xb || j < yb; {
		if i < xb {
			o |= ((x >> i) & 1) << b
			b++
			i++
		}
		if j < yb {
			o |= ((y >> j) & 1) << b
			b++
			j++
		}
	}
	return o
}

func log2(v int) int {
	n := 0
	for 1<<n < v {
		n++
	}
	return n
}

func pow2(v int) bool { return v > 0 && v&(v-1) == 0 }

// PS3ARGBLevels decodes every stored level of a PS3 0x85 texture into linear B,G,R,A rows.
func PS3ARGBLevels(t *PS3Tex) ([][]byte, error) {
	if t.Format != PS3ARGB {
		return nil, fmt.Errorf("tex: PS3 format %#x, want 0x85", t.Format)
	}
	if !pow2(t.W) || !pow2(t.H) {
		return nil, errors.New("tex: PS3 texture is not a power of two")
	}
	var out [][]byte
	p := 0
	for i := 0; i < t.Mips; i++ {
		w, h := max(1, t.W>>i), max(1, t.H>>i)
		n := w * h * 4
		if p+n > len(t.Data) {
			return nil, errors.New("tex: PS3 texture shorter than its mip chain")
		}
		raw, lv := t.Data[p:p+n], make([]byte, n)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				s, d := ps3Morton(x, y, w, h)*4, (y*w+x)*4
				// A,R,G,B -> B,G,R,A (byte reverse per texel)
				lv[d], lv[d+1], lv[d+2], lv[d+3] = raw[s+3], raw[s+2], raw[s+1], raw[s]
			}
		}
		out = append(out, lv)
		p += n
	}
	if p != len(t.Data) {
		return nil, errors.New("tex: PS3 texture has data after its mip chain")
	}
	return out, nil
}

// X360Tex is the part of an Xbox 360 .tex header used here (fields of the GPU texture fetch constant).
type X360Tex struct {
	Format, Endian int
	Tiled          bool
	W, H           int
	MipMax         int  // dword 4 bits 6..9: last stored mip level (0 = base level only)
	PackedMips     bool // dword 5 bit 11: levels of 16 texels and below share one tile
	MipOffset      int  // dword 5 bits 12..31 << 12, minus the base address: where level 1 starts
	Data           []byte
}

// X360 GPU texture formats handled here.
const (
	X360ARGB = 6  // k_8_8_8_8, 8in32 swap: B,G,R,A after the swap
	X360DXT1 = 18 // k_DXT1, 8in16 swap: PC block bytes after the swap
	X360DXT5 = 20 // k_DXT4_5, 8in16 swap: PC block bytes after the swap (HUD weapon icons, 128x64, one level)
	X360DXN  = 49 // k_DXN (BC5: two BC4 blocks, X then Y), 8in16 swap
)

// x360Prefix is the fixed start of every X360 .tex seen (28 bytes before the fetch constant).
var x360Prefix = []byte{0, 0, 0, 3, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0, 0xff, 0xff, 0, 0}

// ParseX360 reads an Xbox 360 .tex header.
func ParseX360(b []byte) (*X360Tex, error) {
	if len(b) < 52 || string(b[:28]) != string(x360Prefix) {
		return nil, errors.New("tex: not an Xbox 360 .tex")
	}
	be := binary.BigEndian
	d := make([]uint32, 6)
	for i := range d {
		d[i] = be.Uint32(b[28+4*i:])
	}
	t := &X360Tex{Format: int(d[1] & 0x3f), Endian: int(d[1]>>6) & 3, Tiled: d[0]>>31 == 1,
		W: int(d[2]&0x1fff) + 1, H: int((d[2]>>13)&0x1fff) + 1,
		MipMax: int(d[4]>>6) & 15, PackedMips: (d[5]>>11)&1 == 1,
		MipOffset: int(d[5]>>12)<<12 - int(d[1]>>12)<<12, Data: b[52:]}
	if (d[5]>>9)&3 != 1 {
		return nil, errors.New("tex: X360 texture is not 2D")
	}
	return t, nil
}

// xenosTiled returns the element index of (x, y) in a Xenos 2D tiled surface aw elements wide with 2^l2 bytes
// per element (texels, or 4x4 blocks for DXT) - the XGAddress2DTiledOffset formula.
func xenosTiled(x, y, aw, l2 int) int {
	macro := ((x >> 5) + (y>>5)*(aw>>5)) << (l2 + 7)
	micro := ((x & 7) + ((y & 0xE) << 2)) << l2
	off := macro + ((micro &^ 0xF) << 1) + (micro & 0xF) + ((y & 1) << 4)
	return (((off &^ 0x1FF) << 3) + ((y & 16) << 7) + ((off & 0x1C0) << 2) + (((((y & 8) >> 2) + (x >> 3)) & 3) << 6) + (off & 0x3F)) >> l2
}

// x360Kind describes one supported X360 format: element size in texels and bytes, its endian swap.
type x360Kind struct {
	block, bytes, l2, endian int
	fourcc                   string // "" = A8R8G8B8
}

var x360Kinds = map[int]x360Kind{
	X360ARGB: {1, 4, 2, 2, ""},
	X360DXT1: {4, 8, 3, 1, "DXT1"},
	X360DXT5: {4, 16, 4, 1, "DXT5"},
	X360DXN:  {4, 16, 4, 1, "ATI2"}, // decoded to A8R8G8B8 by FromX360Normal; never written as ATI2
}

// X360Levels untiles every stored level (0..MipMax) of an X360 texture into linear PC element rows (B,G,R,A
// texels or DXT blocks), endian swap undone. Layout (checked byte-exact on grid_e, grid_s and Grid_Floor_D,
// every level): the base level at 0, level 1 at MipOffset, each level whose width is above 16 texels tiled on
// its own at a pitch padded to 32 elements (height too), then one 32x32-element tile holding every level of
// 16 texels and below ("packed mip tail"). In that tile packed level p (p = 0 for the 16-texel level) starts
// at texel x = 16>>p, y = 0 for p < 3 and at x = 0, y = 16>>(p-2) from p = 3 on (divided by the block size for
// DXT). Square power-of-two textures only, except a texture with only its base level, which may be any
// power-of-two rectangle: its pitch and height are each padded to 32 elements (the HUD icons, 128x64 DXT5 in
// a 32x32-block tile). No other layout is guessed.
func X360Levels(t *X360Tex) ([][]byte, error) {
	k, ok := x360Kinds[t.Format]
	if !ok || t.Endian != k.endian || !t.Tiled {
		return nil, fmt.Errorf("tex: X360 format %d endian %d tiled %v is not supported", t.Format, t.Endian, t.Tiled)
	}
	if t.MipMax == 0 && t.W != t.H && pow2(t.W) && pow2(t.H) && t.W >= k.block && t.H >= k.block {
		bw, bh := t.W/k.block, t.H/k.block
		aw, ah := (bw+31)&^31, (bh+31)&^31
		if aw*ah*k.bytes > len(t.Data) {
			return nil, errors.New("tex: X360 texture shorter than its base level")
		}
		lv := make([]byte, bw*bh*k.bytes)
		for y := 0; y < bh; y++ {
			for x := 0; x < bw; x++ {
				x360Element(lv[(y*bw+x)*k.bytes:], t.Data[xenosTiled(x, y, aw, k.l2)*k.bytes:], k)
			}
		}
		return [][]byte{lv}, nil
	}
	if t.W != t.H || !pow2(t.W) || t.W < k.block {
		return nil, errors.New("tex: X360 texture is not a square power of two")
	}
	n := t.MipMax + 1
	if n > FullMips(t.W, t.H) || (n > 1 && !t.PackedMips) {
		return nil, errors.New("tex: X360 mip chain layout not supported")
	}
	if n > 1 && t.W>>(n-1) < k.block {
		return nil, errors.New("tex: X360 mip levels below one DXT block are not supported")
	}
	var out [][]byte
	off := 0
	for i := 0; i < n; i++ {
		w := t.W >> i         // texels
		bw := w / k.block     // elements
		aw := (bw + 31) &^ 31 // padded pitch (and height) in elements
		x0, y0 := 0, 0
		if i == 1 {
			off = t.MipOffset
		}
		start := off
		if i > 0 && w <= 16 {
			p := i - max(0, log2(t.W)-4) // packed level index
			if p < 3 {
				x0 = (16 >> p) / k.block
			} else {
				y0 = (16 >> (p - 2)) / k.block
			}
		} else {
			off += aw * aw * k.bytes
		}
		if start < 0 || start+aw*aw*k.bytes > len(t.Data) {
			return nil, errors.New("tex: X360 texture shorter than its mip chain")
		}
		raw := t.Data[start:]
		lv := make([]byte, bw*bw*k.bytes)
		for y := 0; y < bw; y++ {
			for x := 0; x < bw; x++ {
				x360Element(lv[(y*bw+x)*k.bytes:], raw[xenosTiled(x+x0, y+y0, aw, k.l2)*k.bytes:], k)
			}
		}
		out = append(out, lv)
	}
	return out, nil
}

// x360Element copies one texel or DXT block from the tiled source e to d, undoing the endian swap.
func x360Element(d, e []byte, k x360Kind) {
	if k.endian == 2 { // 8in32
		for j := 0; j < k.bytes; j += 4 {
			d[j], d[j+1], d[j+2], d[j+3] = e[j+3], e[j+2], e[j+1], e[j]
		}
	} else { // 8in16
		for j := 0; j < k.bytes; j += 2 {
			d[j], d[j+1] = e[j+1], e[j]
		}
	}
}

// FromX360 converts an X360 texture (A8R8G8B8 or DXT1) with all its stored levels into a PC DDS: SM15 _D/_S,
// load_bonusX360, Calibration_A0, Engineering\black (one level each), grid_e/grid_s (10 levels, equal to the
// PS3-built files) and Grid_Floor_D (DXT1, 8 levels).
func FromX360(src []byte) ([]byte, error) {
	t, err := ParseX360(src)
	if err != nil {
		return nil, err
	}
	if t.Format == X360DXN {
		return nil, errors.New("tex: X360 DXN normal map: use FromX360Normal")
	}
	levels, err := X360Levels(t)
	if err != nil {
		return nil, err
	}
	var out []byte
	if f := x360Kinds[t.Format].fourcc; f != "" {
		out = DDSHeaderDXT(t.W, t.H, len(levels), f)
	} else {
		out = DDSHeaderARGB(t.W, t.H, len(levels))
	}
	for _, l := range levels {
		out = append(out, l...)
	}
	return out, nil
}

// HalveBGRA makes the next mip level: every channel is the mean of a 2x2 block, rounded half to even (the
// numpy np.round the original recipe used). w and h must be even (or 1 on one axis).
func HalveBGRA(src []byte, w, h int) ([]byte, int, int) {
	nw, nh := max(1, w/2), max(1, h/2)
	sx, sy := min(2, w), min(2, h)
	out := make([]byte, nw*nh*4)
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			for k := 0; k < 4; k++ {
				s, n := 0, 0
				for dy := 0; dy < sy; dy++ {
					for dx := 0; dx < sx; dx++ {
						s += int(src[((y*sy+dy)*w+x*sx+dx)*4+k])
						n++
					}
				}
				out[(y*nw+x)*4+k] = byte(divRoundEven(s, n))
			}
		}
	}
	return out, nw, nh
}

// divRoundEven is s/n rounded half to even (n is 1, 2 or 4, so the mean is exact in binary).
func divRoundEven(s, n int) int {
	q, r := s/n, s%n
	if 2*r > n || (2*r == n && q&1 == 1) {
		q++
	}
	return q
}

// FullMips returns the number of levels down to 1x1.
func FullMips(w, h int) int { return log2(max(w, h)) + 1 }

func joinARGB(w, h int, levels [][]byte) []byte {
	out := DDSHeaderARGB(w, h, len(levels))
	for _, l := range levels {
		out = append(out, l...)
	}
	return out
}

// FromPS3ARGB converts a PS3 0x85 texture into an A8R8G8B8 DDS: its stored levels are copied (unswizzled),
// then missing levels down to 1x1 are generated with HalveBGRA from the previous level. full=false keeps only
// the stored levels.
func FromPS3ARGB(src []byte, full bool) ([]byte, error) {
	t, err := ParsePS3(src)
	if err != nil {
		return nil, err
	}
	levels, err := PS3ARGBLevels(t)
	if err != nil {
		return nil, err
	}
	if full {
		levels = extendMips(levels, t.W, t.H)
	}
	return joinARGB(t.W, t.H, levels), nil
}

func extendMips(levels [][]byte, w, h int) [][]byte {
	n := FullMips(w, h)
	for len(levels) < n {
		i := len(levels) - 1
		nl, _, _ := HalveBGRA(levels[i], max(1, w>>i), max(1, h>>i))
		levels = append(levels, nl)
	}
	return levels
}

// FromPS3DXT copies a PS3 DXT1/DXT5 texture's levels verbatim behind a DDS header (obj_example).
func FromPS3DXT(src []byte) ([]byte, error) {
	t, err := ParsePS3(src)
	if err != nil {
		return nil, err
	}
	fourcc := map[int]string{PS3DXT1: "DXT1", PS3DXT5: "DXT5"}[t.Format]
	if fourcc == "" {
		return nil, fmt.Errorf("tex: PS3 format %#x is not DXT1/DXT5", t.Format)
	}
	return append(DDSHeaderDXT(t.W, t.H, t.Mips, fourcc), t.Data...), nil
}
