// Derived from FFmpeg 7.1 (tag n7.1): libavcodec/get_bits.h (bit reader behaviour) and libavcodec/vlc.c
// (ff_vlc_init_from_lengths), as used by libavcodec/atrac3.c.
//
// Copyright of the FFmpeg files this file is derived from, as they state it:
//
//	libavcodec/get_bits.h: Copyright (c) 2004 Michael Niedermayer <michaelni@gmx.at>
//	libavcodec/vlc.c:      Copyright (c) 2000, 2001 Fabrice Bellard
//	                       Copyright (c) 2002-2004 Michael Niedermayer <michaelni@gmx.at>
//	                       Copyright (c) 2010 Loren Merritt
//
// MODIFIED VERSION: this file was translated from C to Go and changed for the F.E.A.R. console content
// installer in 2026-11 (a reader for MSB-first bits, and a VLC built from code lengths into a single-level
// 8-bit lookup table instead of FFmpeg's multi-level one).
//
// This file is part of a derivative of FFmpeg. FFmpeg is free software; you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by the Free Software Foundation;
// either version 2.1 of the License, or (at your option) any later version (LGPL-2.1-or-later). FFmpeg is
// distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty
// of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser General Public License for more
// details, LICENSE-FFMPEG.md in the parent audio package, and licenses/FFmpeg-LICENSE.txt.

package atrac3

// bitReader reads MSB-first bits. Like FFmpeg's safe bitstream reader, reads past the end return zero bits
// (FFmpeg packets carry zero padding) and the position is clamped, so a damaged frame cannot run away.
type bitReader struct {
	buf  []byte
	pos  int // bit position
	size int // size in bits
}

func newBitReader(b []byte) *bitReader { return &bitReader{buf: b, size: 8 * len(b)} }

func (r *bitReader) bit(p int) uint32 {
	if p >= r.size {
		return 0
	}
	return uint32(r.buf[p>>3]>>(7-uint(p&7))) & 1
}

// peek returns the next n bits (n <= 25) without consuming them.
func (r *bitReader) peek(n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		v = v<<1 | r.bit(r.pos+i)
	}
	return v
}

func (r *bitReader) skip(n int) {
	r.pos += n
	if r.pos > r.size+8 { // FFmpeg clamps at size_in_bits_plus8
		r.pos = r.size + 8
	}
}

func (r *bitReader) get(n int) int {
	v := r.peek(n)
	r.skip(n)
	return int(v)
}

func (r *bitReader) get1() int { return r.get(1) }

// getS reads n bits as a two's complement signed value (get_sbits).
func (r *bitReader) getS(n int) int {
	v := r.get(n)
	if v&(1<<(n-1)) != 0 {
		v -= 1 << n
	}
	return v
}

// vlcTable is an 8-bit single-level lookup table, enough for the ATRAC3 spectral codes (8 bits at most).
type vlcTable struct {
	sym [256]int8
	len [256]uint8
}

const vlcBits = 8

// buildVLC assigns canonical codes in table order: each entry takes the current code, then the code advances
// by 1 << (32 - len) (ff_vlc_init_from_lengths). Symbols are stored with FFmpeg's offset of -31.
// Unfilled slots decode as symbol -1 with length 0, as FFmpeg's table builder leaves them.
func buildVLC(tab [][2]uint8) vlcTable {
	var t vlcTable
	for i := range t.sym {
		t.sym[i] = -1
	}
	var code uint64
	for _, e := range tab {
		sym, l := int(e[0])-31, int(e[1])
		idx := int(code >> 24)
		for k := 0; k < 1<<(vlcBits-l); k++ {
			t.sym[idx+k] = int8(sym)
			t.len[idx+k] = uint8(l)
		}
		code += 1 << (32 - l)
	}
	return t
}

// vlc decodes one symbol (get_vlc2 with max depth 1).
func (r *bitReader) vlc(t *vlcTable) int {
	i := r.peek(vlcBits)
	r.skip(int(t.len[i]))
	return int(t.sym[i])
}
