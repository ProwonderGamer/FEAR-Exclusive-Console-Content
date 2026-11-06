// Derived from FFmpeg 7.1 (tag n7.1): libavcodec/vlc.c (ff_vlc_init_from_lengths) and libavcodec/get_bits.h
// (get_vlc2).
//
// Copyright of the FFmpeg files this file is derived from, as they state it:
//
//	libavcodec/vlc.c:      Copyright (c) 2000, 2001 Fabrice Bellard
//	                       Copyright (c) 2002-2004 Michael Niedermayer <michaelni@gmx.at>
//	                       Copyright (c) 2010 Loren Merritt
//	libavcodec/get_bits.h: Copyright (c) 2004 Michael Niedermayer <michaelni@gmx.at>
//
// MODIFIED VERSION: this file was translated from C to Go and changed for the F.E.A.R. console content
// installer in 2026-11 (the prefix code is kept as a sorted list of left-aligned codes and searched, instead
// of FFmpeg's multi-level lookup tables).
//
// This file is part of a derivative of FFmpeg. FFmpeg is free software; you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by the Free Software Foundation;
// either version 2.1 of the License, or (at your option) any later version (LGPL-2.1-or-later). FFmpeg is
// distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty
// of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser General Public License for more
// details, LICENSE-FFMPEG.md in this folder, and licenses/FFmpeg-LICENSE.txt.

package xma2

import (
	"fmt"
	"sort"
)

// vlc is a prefix code built like ff_vlc_init_from_lengths: codes are handed out in table order,
// each one the previous code plus one at its own length (left aligned in 32 bits).
type vlc struct {
	codes  []uint32 // left-aligned codes, ascending
	lens   []uint8
	syms   []int32
	maxLen int
}

func newVLC(lens []uint8, syms []int32, offset int32) (*vlc, error) {
	v := &vlc{}
	var code uint64
	for i, l := range lens {
		if l == 0 {
			continue
		}
		if l > 32 || code&((1<<uint(32-l))-1) != 0 {
			return nil, fmt.Errorf("xma2: invalid VLC length %d at %d", l, i)
		}
		sym := int32(i)
		if syms != nil {
			sym = syms[i]
		}
		v.codes = append(v.codes, uint32(code))
		v.lens = append(v.lens, l)
		v.syms = append(v.syms, sym+offset)
		if int(l) > v.maxLen {
			v.maxLen = int(l)
		}
		code += 1 << uint(32-l)
		if code > 1<<32 {
			return nil, fmt.Errorf("xma2: VLC code overflow at %d", i)
		}
	}
	return v, nil
}

// complete reports whether the code fills the whole code space (Kraft sum 1).
func (v *vlc) complete() bool {
	var sum uint64
	for _, l := range v.lens {
		sum += 1 << uint(32-l)
	}
	return sum == 1<<32
}

// read decodes one symbol (get_vlc2). ok is false for a bit pattern with no code.
func (v *vlc) read(g *bitReader) (sym int32, ok bool) {
	w := g.peek(32)
	i := sort.Search(len(v.codes), func(i int) bool { return v.codes[i] > w }) - 1
	if i < 0 {
		return -1, false
	}
	l := v.lens[i]
	if (w^v.codes[i])>>uint(32-l) != 0 {
		return -1, false
	}
	g.skip(int(l))
	return v.syms[i], true
}

func widen16(s []int16) []int32 {
	out := make([]int32, len(s))
	for i, x := range s {
		out[i] = int32(x)
	}
	return out
}

// Huffman tables of decode_init_static.
var (
	sfVLC, sfRLVLC, vec4VLC, vec2VLC, vec1VLC *vlc
	coefVLC                                   [2]*vlc
	vlcErr                                    error
)

func init() {
	mk := func(lens []uint8, syms []int32, off int32) *vlc {
		v, err := newVLC(lens, syms, off)
		if err != nil && vlcErr == nil {
			vlcErr = err
		}
		return v
	}
	sfVLC = mk(scaleLens[:], widen16(scaleSyms[:]), -60)
	sfRLVLC = mk(scaleRLLens[:], widen16(scaleRLSyms[:]), 0)
	coefVLC[0] = mk(coef0Lens[:], widen16(coef0Syms[:]), 0)
	coefVLC[1] = mk(coef1Lens[:], widen16(coef1Syms[:]), 0)
	vec4VLC = mk(vec4Lens[:], vec4Syms[:], -1)
	vec2VLC = mk(vec2Lens[:], widen16(vec2Syms[:]), -1)
	vec1VLC = mk(vec1Lens[:], widen16(vec1Syms[:]), 0)
}
