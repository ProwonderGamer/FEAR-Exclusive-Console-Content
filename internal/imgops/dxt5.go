package imgops

import "errors"

// DXT5 (BC3) blocks: 8 alpha bytes (two endpoints, 16 3-bit indices) then a BC1 colour block (two RGB565
// endpoints, 16 2-bit indices, always the 4-colour mode).

func expand565(v uint16) [3]int {
	r, g, b := int(v>>11&31), int(v>>5&63), int(v&31)
	return [3]int{r<<3 | r>>2, g<<2 | g>>4, b<<3 | b>>2}
}

// alphaPalette returns the 8 alpha values of a block's endpoints (Pillow BcnDecode rules, integer division).
func alphaPalette(a0, a1 int) [8]int {
	if a0 > a1 {
		return [8]int{a0, a1, (6*a0 + a1) / 7, (5*a0 + 2*a1) / 7, (4*a0 + 3*a1) / 7, (3*a0 + 4*a1) / 7, (2*a0 + 5*a1) / 7, (a0 + 6*a1) / 7}
	}
	return [8]int{a0, a1, (4*a0 + a1) / 5, (3*a0 + 2*a1) / 5, (2*a0 + 3*a1) / 5, (a0 + 4*a1) / 5, 0, 255}
}

// DecodeDXT5 decodes DXT5 blocks (row-major, w and h multiples of 4 or smaller than one block) into RGBA, as
// Pillow's BC3 decoder does.
func DecodeDXT5(blocks []byte, w, h int) (*RGBA, error) {
	bw, bh := (w+3)/4, (h+3)/4
	if len(blocks) < bw*bh*16 {
		return nil, errors.New("imgops: DXT5 data too short")
	}
	m := New(w, h)
	for by := 0; by < bh; by++ {
		for bx := 0; bx < bw; bx++ {
			b := blocks[(by*bw+bx)*16:]
			ap := alphaPalette(int(b[0]), int(b[1]))
			abits := uint64(0)
			for i := 0; i < 6; i++ {
				abits |= uint64(b[2+i]) << (8 * i)
			}
			c0, c1 := uint16(b[8])|uint16(b[9])<<8, uint16(b[10])|uint16(b[11])<<8
			p0, p1 := expand565(c0), expand565(c1)
			var pal [4][3]int
			pal[0], pal[1] = p0, p1
			for c := 0; c < 3; c++ {
				pal[2][c] = (2*p0[c] + p1[c]) / 3
				pal[3][c] = (p0[c] + 2*p1[c]) / 3
			}
			cbits := uint32(b[12]) | uint32(b[13])<<8 | uint32(b[14])<<16 | uint32(b[15])<<24
			for k := 0; k < 16; k++ {
				x, y := bx*4+k%4, by*4+k/4
				if x >= w || y >= h {
					continue
				}
				o := (y*w + x) * 4
				col := pal[cbits>>(2*k)&3]
				m.Pix[o], m.Pix[o+1], m.Pix[o+2] = byte(col[0]), byte(col[1]), byte(col[2])
				m.Pix[o+3] = byte(ap[abits>>(3*k)&7])
			}
		}
	}
	return m, nil
}
