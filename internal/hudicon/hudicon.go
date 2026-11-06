// Package hudicon builds the six ported HUD weapon icons (sm15, autoshot, dualsm15 and their X variants) from
// the console icon textures, as the project's Python icon builder made them:
//
//  1. the console icon (128x64 DXT5, one level) decoded to RGBA;
//  2. resized to 96x48 with Pillow's Lanczos filter through premultiplied alpha and pasted at (0,0) on a
//     transparent 128x64 canvas, so the console frame (which fills the whole canvas) lands on the stock PC
//     icon area x0-95, y0-47 and the ammo bar (rows 42-47) sits in the frame's bottom strip;
//  3. a premultiplied box-filter mip chain down to 1x1 (8 levels);
//  4. every level compressed to DXT5 behind the stock icon header (11088 bytes, like the stock icons).
//
// Steps 1-3 follow Pillow exactly and step 4 follows ImageMagick's DDS writer (internal/imgops), so every icon
// rebuilds byte for byte (hudicon_test.go): the SM15 icons from the Xbox 360 art, the Watson (autoshot) icons
// from the PS3 art, as the confirmed files were made. The other console's art is the same picture compressed
// differently by the console tools and gives other bytes.
package hudicon

import (
	"errors"
	"fmt"

	"fearinstaller/internal/imgops"
	"fearinstaller/internal/tex"
)

// Names are the six icons, as the confirmed files are named (Interface\hud\weapon\<name>.dds; console .tex alike).
var Names = []string{"sm15", "sm15X", "autoshot", "autoshotX", "dualsm15", "dualsm15X"}

const (
	width, height = 128, 64 // console and PC canvas
	artW, artH    = 96, 48  // stock PC icon area (0.75 of the canvas)
	mipLevels     = 8       // 128x64 down to 1x1
)

// consoleBlocks returns the DXT5 blocks of a console icon (.tex from either catalog).
func consoleBlocks(src []byte, isPS3 bool) ([]byte, error) {
	if isPS3 {
		t, err := tex.ParsePS3(src)
		if err != nil {
			return nil, err
		}
		if t.Format != tex.PS3DXT5 || t.W != width || t.H != height || len(t.Data) < width*height {
			return nil, fmt.Errorf("hudicon: PS3 icon is format %#x %dx%d, want DXT5 128x64", t.Format, t.W, t.H)
		}
		return t.Data[:width*height], nil // 128x64 DXT5 = 8192 bytes, stored as on PC
	}
	t, err := tex.ParseX360(src)
	if err != nil {
		return nil, err
	}
	if t.Format != tex.X360DXT5 || t.W != width || t.H != height || t.MipMax != 0 {
		return nil, fmt.Errorf("hudicon: Xbox 360 icon is format %d %dx%d, want DXT5 128x64", t.Format, t.W, t.H)
	}
	lv, err := tex.X360Levels(t)
	if err != nil {
		return nil, err
	}
	return lv[0], nil
}

// Decode returns a console icon's pixels.
func Decode(src []byte, isPS3 bool) (*imgops.RGBA, error) {
	b, err := consoleBlocks(src, isPS3)
	if err != nil {
		return nil, err
	}
	return imgops.DecodeDXT5(b, width, height)
}

// Levels returns the 8 RGBA levels of the PC icon made from a console icon's pixels (steps 2 and 3).
func Levels(src *imgops.RGBA) ([]*imgops.RGBA, error) {
	if src.W != width || src.H != height {
		return nil, errors.New("hudicon: icon is not 128x64")
	}
	canvas := imgops.New(width, height)
	canvas.Paste(imgops.Resize(src, artW, artH, imgops.Lanczos), 0, 0)
	chain := []*imgops.RGBA{canvas}
	for last := canvas; last.W > 1 || last.H > 1; {
		last = imgops.Resize(last, max(1, last.W/2), max(1, last.H/2), imgops.Box)
		chain = append(chain, last)
	}
	if len(chain) != mipLevels {
		return nil, fmt.Errorf("hudicon: %d levels, want %d", len(chain), mipLevels)
	}
	return chain, nil
}

// Build makes the PC icon (.dds bytes) from a console icon .tex.
func Build(src []byte, isPS3 bool) ([]byte, error) {
	px, err := Decode(src, isPS3)
	if err != nil {
		return nil, err
	}
	chain, err := Levels(px)
	if err != nil {
		return nil, err
	}
	out := tex.DDSHeaderDXT(width, height, mipLevels, "DXT5")
	for _, m := range chain {
		out = append(out, imgops.EncodeDXT5(m)...)
	}
	return out, nil
}

// FileSize is the size of every built icon (and of the confirmed ones): header + 8 DXT5 levels.
const FileSize = 11088

// DecodeDDS returns the levels of a built (or confirmed) icon file, for comparison.
func DecodeDDS(b []byte) ([]*imgops.RGBA, error) {
	if len(b) != FileSize || string(b[:4]) != "DDS " || string(b[84:88]) != "DXT5" {
		return nil, errors.New("hudicon: not a 128x64 DXT5 icon")
	}
	var out []*imgops.RGBA
	off, w, h := 128, width, height
	for i := 0; i < mipLevels; i++ {
		n := max(1, (w+3)/4) * max(1, (h+3)/4) * 16
		m, err := imgops.DecodeDXT5(b[off:off+n], w, h)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
		off += n
		w, h = max(1, w/2), max(1, h/2)
	}
	return out, nil
}
