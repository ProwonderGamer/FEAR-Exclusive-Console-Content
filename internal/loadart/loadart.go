// Package loadart rebuilds the four Instant Action loading pictures Interface/IA/IA_Load1..4.dds from the
// player's own Xbox 360 copy (the load_multi background and the InstantAction1..4 map photos in
// Game/uberCatalog.cat) and two fonts found on the player's PC: the game's NEUROPOX.TTF (FEARL.Arch00,
// interface\fonts) and Windows Arial Bold (arialbd.ttf). No game data or font ships with the installer.
//
// It is a Go port of the project's Python loading picture recipe (art + save_dds),
// which used Pillow 12.1.1 and its bundled FreeType 2.14.1. Re-running that recipe with the same
// Pillow reproduced the four in-game confirmed files byte for byte, so the Pillow steps are the reference
// for every rule here:
//   - imgops supplies Pillow's Lanczos resize and alpha_composite (proven byte-identical);
//   - font.go / raster.go / text.go read TrueType outlines and scan-convert them like FreeType's smooth
//     renderer; drawText reproduces ImageDraw.text's mask fill into an RGBA layer.
//
// FreeType hinting (its TrueType bytecode interpreter) is not reproduced: the glyph outlines are scaled but
// not grid-fitted, so text edges can differ slightly from the recipe's. The golden test measures that
// difference against the confirmed files (see loadart_test.go).
package loadart

import (
	"errors"
	"fmt"

	"fearinstaller/internal/imgops"
	"fearinstaller/internal/tex"
)

// CatalogReader reads a file from the X360 uberCatalog.cat (internal/catalog.Catalog satisfies it).
type CatalogReader interface {
	ReadByName(path string) ([]byte, error)
}

// Maps are the four Instant Action maps in IA_Load order (recipe: dds_only).
var Maps = []string{"Distribution", "Rooftop", "Construction", "Vault"}

// OutputPath returns the mod path of loading picture i (1..4).
func OutputPath(i int) string { return fmt.Sprintf("Interface/IA/IA_Load%d.dds", i) }

// Catalog paths of the X360 sources (recipe: tex()).
const (
	backgroundPath = `Interface\menu\art\LoadScreens\load_multi.tex`
	photoPathFmt   = `Worlds\Release\InstantAction%d.tex`
)

// Text colours (recipe): the console colour -1837582 = (227,245,242) and a (0,0,0,150) drop shadow at +2,+2.
var (
	textInk   = [4]byte{227, 245, 242, 255}
	shadowInk = [4]byte{0, 0, 0, 150}
)

// Build makes the four DDS files, keyed by OutputPath.
func Build(x360 CatalogReader, neuropox, arialBold []byte) (map[string][]byte, error) {
	np, err := ParseFont(neuropox)
	if err != nil {
		return nil, fmt.Errorf("NEUROPOX.TTF: %w", err)
	}
	ab, err := ParseFont(arialBold)
	if err != nil {
		return nil, fmt.Errorf("arialbd.ttf: %w", err)
	}
	bg, err := readX360RGB(x360, backgroundPath)
	if err != nil {
		return nil, err
	}
	title, help := NewFace(np, 89), NewFace(ab, 38)
	out := map[string][]byte{}
	for i, label := range Maps {
		photo, err := readX360RGB(x360, fmt.Sprintf(photoPathFmt, i+1))
		if err != nil {
			return nil, err
		}
		pic, err := Art(bg, photo, title, help, i+1, label)
		if err != nil {
			return nil, err
		}
		out[OutputPath(i+1)] = DDS(pic)
	}
	return out, nil
}

// readX360RGB decodes a one-level X360 A8R8G8B8 texture to an opaque RGBA picture (recipe tex(): the colour
// bytes only; the stored alpha is dropped because the recipe made an RGB image).
func readX360RGB(c CatalogReader, path string) (*imgops.RGBA, error) {
	b, err := c.ReadByName(path)
	if err != nil {
		return nil, err
	}
	t, err := tex.ParseX360(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if t.Format != tex.X360ARGB || t.Endian != 2 || !t.Tiled || len(t.Data) != t.W*t.H*4 {
		return nil, fmt.Errorf("%s: not the expected one-level tiled k_8_8_8_8 texture", path)
	}
	lv, err := tex.X360Levels(t)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m := imgops.New(t.W, t.H)
	for i := 0; i < t.W*t.H; i++ { // B,G,R,A after the swap
		m.Pix[4*i], m.Pix[4*i+1], m.Pix[4*i+2], m.Pix[4*i+3] = lv[0][4*i+2], lv[0][4*i+1], lv[0][4*i], 255
	}
	return m, nil
}

// textLine is one string of the recipe: its first glyph's ink is placed at (X, Top) of the 1920x1080 layer.
type textLine struct {
	x, top int
	s      string
	face   *Face
}

// Art composes one loading picture (recipe art()):
//   - the X360 load_multi background (1024x1024, shown stretched to 16:9 by the game);
//   - the square map photo Lanczos-resized to 360x480 (squeezed to 4:3) and pasted over the placeholder photo
//     window at (331,272) in native 1024 space;
//   - the text on a transparent 1920x1080 layer (the console draws text unstretched in screen space): title
//     "Instant Action" in NEUROPOX 89 at ink (141,57), "Map i - Label" in Arial Bold 38 at (196,115) and
//     "Loading. . ." at (604,866) (X360 DefaultMP HelpRect). Each is drawn twice: shadow at +2,+2, then
//     the text;
//   - the layer Lanczos-resized to 1024x1024 (RGBA, premultiplied) and alpha-composited over the background.
//
// The result is opaque (the recipe converted to RGB).
func Art(bg, photo *imgops.RGBA, title, help *Face, i int, label string) (*imgops.RGBA, error) {
	if bg.W != 1024 || bg.H != 1024 {
		return nil, errors.New("loadart: load_multi is not 1024x1024")
	}
	base := bg.Clone()
	// An RGB-mode resize: no premultiply step, so resample the channels directly (alpha stays 255).
	base.Paste(imgops.ResampleRaw(photo, 360, 480, imgops.Lanczos), 331, 272)
	layer := imgops.New(1920, 1080)
	for _, l := range []textLine{
		{141, 57, "Instant Action", title},
		{196, 115, fmt.Sprintf("Map %d - %s", i, label), help},
		{604, 866, "Loading. . .", help},
	} {
		bx, by, err := l.face.FirstInkOffset(l.s) // Pillow getbbox(text[0])[:2]
		if err != nil {
			return nil, err
		}
		x, y := l.x-bx, l.top-by
		if err := drawText(layer, l.face, l.s, x+2, y+2, shadowInk); err != nil {
			return nil, err
		}
		if err := drawText(layer, l.face, l.s, x, y, textInk); err != nil {
			return nil, err
		}
	}
	small := imgops.Resize(layer, 1024, 1024, imgops.Lanczos)
	comp, err := imgops.AlphaComposite(base, small)
	if err != nil {
		return nil, err
	}
	for p := 3; p < len(comp.Pix); p += 4 {
		comp.Pix[p] = 255
	}
	return comp, nil
}

// drawText draws s with its "la" anchor (left, ascender line) at (x, y), as ImageDraw.text into an RGBA
// image: the glyph masks are merged by maximum (FreeType bitmaps into Pillow's text mask), then each mask
// value m fills the pixel with Pillow's fill_mask_L rule for RGBA targets, checked exhaustively against
// Pillow 12.1.1 for every (shadow mask, text mask) pair: alpha = BLEND(m, a, inkA); colour channels =
// BLEND(m, c, inkC), except that a pixel whose alpha is still 0 takes the ink colour outright (m != 0).
func drawText(dst *imgops.RGBA, fc *Face, s string, x, y int, ink [4]byte) error {
	pl, err := fc.layout(s)
	if err != nil {
		return err
	}
	// Merge the glyphs into one mask over the text's pixel box first (overlapping glyphs take the maximum).
	x0, y0, x1, y1 := 0, 0, 0, 0
	first := true
	for _, p := range pl {
		if p.bm.W == 0 || p.bm.H == 0 {
			continue
		}
		if first {
			x0, y0, x1, y1, first = p.x, p.y, p.x+p.bm.W, p.y+p.bm.H, false
			continue
		}
		x0, y0 = min(x0, p.x), min(y0, p.y)
		x1, y1 = max(x1, p.x+p.bm.W), max(y1, p.y+p.bm.H)
	}
	if first {
		return nil
	}
	w, h := x1-x0, y1-y0
	mask := make([]byte, w*h)
	for _, p := range pl {
		for r := 0; r < p.bm.H; r++ {
			for c := 0; c < p.bm.W; c++ {
				v := p.bm.Pix[r*p.bm.W+c]
				i := (p.y-y0+r)*w + p.x - x0 + c
				if v > mask[i] {
					mask[i] = v
				}
			}
		}
	}
	for r := 0; r < h; r++ {
		dy := y + y0 + r
		if dy < 0 || dy >= dst.H {
			continue
		}
		for c := 0; c < w; c++ {
			dx := x + x0 + c
			if dx < 0 || dx >= dst.W {
				continue
			}
			fillPixel(dst.Pix[(dy*dst.W+dx)*4:], mask[r*w+c], ink)
		}
	}
	return nil
}

// fillPixel applies one mask value m with ink to an RGBA pixel (Pillow fill_mask_L, RGBA target; see drawText).
func fillPixel(px []byte, m byte, ink [4]byte) {
	if m == 0 {
		return
	}
	cm := uint32(m)
	if px[3] == 0 {
		cm = 255
	}
	for ch := 0; ch < 3; ch++ {
		px[ch] = blend(cm, px[ch], ink[ch])
	}
	px[3] = blend(uint32(m), px[3], ink[3])
}

// blend is Pillow's BLEND(mask, in1, in2): DIV255(in1*(255-mask) + in2*mask) with DIV255(a) =
// ((a+128)>>8 + a+128)>>8.
func blend(m uint32, a, b byte) byte {
	t := uint32(a)*(255-m) + uint32(b)*m + 128
	return byte(((t >> 8) + t) >> 8)
}

// DDS writes the stock loading-screen format (recipe save_dds): 1024x1024 A8R8G8B8 with 2 mip levels, the
// second level an RGB-mode Lanczos resize to 512x512; pixels as B,G,R,A with alpha 255.
func DDS(pic *imgops.RGBA) []byte {
	mip := imgops.ResampleRaw(pic, 512, 512, imgops.Lanczos)
	out := tex.DDSHeaderARGB(1024, 1024, 2)
	for _, m := range []*imgops.RGBA{pic, mip} {
		for i := 0; i < len(m.Pix); i += 4 {
			out = append(out, m.Pix[i+2], m.Pix[i+1], m.Pix[i], 255)
		}
	}
	return out
}
