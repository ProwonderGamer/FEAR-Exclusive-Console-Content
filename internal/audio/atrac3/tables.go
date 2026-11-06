// Derived from FFmpeg 7.1 (tag n7.1): libavcodec/atrac3data.h and libavcodec/atrac.c (tables only).
//
// Copyright of the FFmpeg files this file is derived from, as they state it:
//
//	libavcodec/atrac3data.h: Copyright (c) 2006-2007 Maxim Poliakovski
//	                         Copyright (c) 2006-2007 Benjamin Larsson
//	libavcodec/atrac.c:      Copyright (c) 2006-2013 Maxim Poliakovski
//	                         Copyright (c) 2006-2008 Benjamin Larsson
//
// MODIFIED VERSION: the tables were translated from C to Go and changed for the F.E.A.R. console content
// installer in 2026-11 (the spectral VLC tables are kept as {symbol + 31, code length} pairs).
//
// This file is part of a derivative of FFmpeg. FFmpeg is free software; you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by the Free Software Foundation;
// either version 2.1 of the License, or (at your option) any later version (LGPL-2.1-or-later). FFmpeg is
// distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty
// of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser General Public License for more
// details, LICENSE-FFMPEG.md in the parent audio package, and licenses/FFmpeg-LICENSE.txt.

package atrac3

import "math"

// huffTabs lists the spectral coefficient VLCs as {symbol + 31, code length}, codes assigned canonically in
// this order (FFmpeg ff_vlc_init_from_lengths). Selector s uses the huffTabSizes[s-1] entries after the
// previous selectors' entries.
var huffTabs = [...][2]uint8{
	// Spectral coefficient 1 - 9 entries
	{31, 1}, {32, 3}, {33, 3}, {34, 4}, {35, 4},
	{36, 5}, {37, 5}, {38, 5}, {39, 5},
	// Spectral coefficient 2 - 5 entries
	{31, 1}, {32, 3}, {30, 3}, {33, 3}, {29, 3},
	// Spectral coefficient 3 - 7 entries
	{31, 1}, {32, 3}, {30, 3}, {33, 4},
	{29, 4}, {34, 4}, {28, 4},
	// Spectral coefficient 4 - 9 entries
	{31, 1}, {32, 3}, {30, 3}, {33, 4}, {29, 4},
	{34, 5}, {28, 5}, {35, 5}, {27, 5},
	// Spectral coefficient 5 - 15 entries
	{31, 2}, {32, 3}, {30, 3}, {33, 4}, {29, 4},
	{34, 4}, {28, 4}, {38, 4}, {24, 4}, {35, 5},
	{27, 5}, {36, 6}, {26, 6}, {37, 6}, {25, 6},
	// Spectral coefficient 6 - 31 entries
	{31, 3}, {32, 4}, {30, 4}, {33, 4}, {29, 4}, {34, 4},
	{28, 4}, {46, 4}, {16, 4}, {35, 5}, {27, 5}, {36, 5},
	{26, 5}, {37, 5}, {25, 5}, {38, 6}, {24, 6}, {39, 6},
	{23, 6}, {40, 6}, {22, 6}, {41, 6}, {21, 6}, {42, 7},
	{20, 7}, {43, 7}, {19, 7}, {44, 7}, {18, 7}, {45, 7},
	{17, 7},
	// Spectral coefficient 7 - 63 entries
	{31, 3}, {62, 4}, {0, 4}, {32, 5}, {30, 5}, {33, 5},
	{29, 5}, {34, 5}, {28, 5}, {35, 5}, {27, 5}, {36, 5},
	{26, 5}, {37, 6}, {25, 6}, {38, 6}, {24, 6}, {39, 6},
	{23, 6}, {40, 6}, {22, 6}, {41, 6}, {21, 6}, {42, 6},
	{20, 6}, {43, 6}, {19, 6}, {44, 6}, {18, 6}, {45, 7},
	{17, 7}, {46, 7}, {16, 7}, {47, 7}, {15, 7}, {48, 7},
	{14, 7}, {49, 7}, {13, 7}, {50, 7}, {12, 7}, {51, 7},
	{11, 7}, {52, 8}, {10, 8}, {53, 8}, {9, 8}, {54, 8},
	{8, 8}, {55, 8}, {7, 8}, {56, 8}, {6, 8}, {57, 8},
	{5, 8}, {58, 8}, {4, 8}, {59, 8}, {3, 8}, {60, 8},
	{2, 8}, {61, 8}, {1, 8},
}

var huffTabSizes = [7]int{9, 5, 7, 9, 15, 31, 63}

// Selector tables.
var clcLengthTab = [8]int{0, 4, 3, 3, 4, 4, 5, 6}
var mantissaCLCTab = [4]int{0, 1, -2, -1}
var mantissaVLCTab = [18]int{0, 0, 0, 1, 0, -1, 1, 0, -1, 0, 1, 1, 1, -1, -1, 1, -1, -1}

// invMaxQuant: FFmpeg stores these as float constants computed in double.
var invMaxQuant = [8]float32{
	0.0, float32(1.0 / 1.5), float32(1.0 / 2.5), float32(1.0 / 3.5),
	float32(1.0 / 4.5), float32(1.0 / 7.5), float32(1.0 / 15.5), float32(1.0 / 31.5),
}

var subbandTab = [33]int{
	0, 8, 16, 24, 32, 40, 48, 56,
	64, 80, 96, 112, 128, 144, 160, 176,
	192, 224, 256, 288, 320, 352, 384, 416,
	448, 480, 512, 576, 640, 704, 768, 896,
	1024,
}

// qmf48TapHalf is the first half of the symmetric 48-tap QMF prototype (float in FFmpeg).
var qmf48TapHalf = [24]float32{
	-0.00001461907, -0.00009205479, -0.000056157569, 0.00030117269,
	0.0002422519, -0.00085293897, -0.0005205574, 0.0020340169,
	0.00078333891, -0.0042153862, -0.00075614988, 0.0078402944,
	-0.000061169922, -0.01344162, 0.0024626821, 0.021736089,
	-0.007801671, -0.034090221, 0.01880949, 0.054326009,
	-0.043596379, -0.099384367, 0.13207909, 0.46424159,
}

// Tables FFmpeg generates at start-up (atrac_generate_tables, init_imdct_window, gain compensation init with
// id2exp_offset 4 and loc_scale 3). Computed in double and stored as float, as in C.
var (
	sfTable    [64]float32
	qmfWindow  [48]float32
	mdctWindow [512]float32
	gainTab1   [16]float32
	gainTab2   [31]float32
	vlcTables  [7]vlcTable
)

const (
	gcID2ExpOffset = 4
	gcLocScale     = 3
	gcLocSize      = 1 << gcLocScale
)

func init() {
	for i := range sfTable {
		sfTable[i] = float32(math.Pow(2.0, float64(i-15)/3.0))
	}
	for i := 0; i < 24; i++ {
		// float s = qmf_48tap_half[i] * 2.0: the float constant is promoted to double, doubled, stored as float.
		s := float32(float64(qmf48TapHalf[i]) * 2.0)
		qmfWindow[i], qmfWindow[47-i] = s, s
	}
	for i, j := 0, 255; i < 128; i, j = i+1, j-1 {
		wi := float32(math.Sin(((float64(i)+0.5)/256.0-0.5)*math.Pi) + 1.0)
		wj := float32(math.Sin(((float64(j)+0.5)/256.0-0.5)*math.Pi) + 1.0)
		// float w = 0.5 * (wi * wi + wj * wj): float products, promoted to double for the 0.5 multiply.
		w := float32(0.5 * float64(float32(wi*wi)+float32(wj*wj)))
		mdctWindow[i], mdctWindow[511-i] = wi/w, wi/w
		mdctWindow[j], mdctWindow[511-j] = wj/w, wj/w
	}
	for i := range gainTab1 {
		gainTab1[i] = float32(math.Pow(2.0, float64(gcID2ExpOffset-i)))
	}
	for i := -15; i < 16; i++ {
		// powf(2.0, -1.0f / loc_size * i): exponent computed in float.
		e := float32(-1.0) / float32(gcLocSize) * float32(i)
		gainTab2[i+15] = float32(math.Pow(2.0, float64(e)))
	}
	off := 0
	for s := 0; s < 7; s++ {
		vlcTables[s] = buildVLC(huffTabs[off : off+huffTabSizes[s]])
		off += huffTabSizes[s]
	}
}
