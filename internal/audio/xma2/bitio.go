// Derived from FFmpeg 7.1 (tag n7.1): libavcodec/get_bits.h, libavcodec/put_bits.h and
// libavcodec/bitstream.c (ff_copy_bits).
//
// Copyright of the FFmpeg files this file is derived from, as they state it:
//
//	libavcodec/get_bits.h:  Copyright (c) 2004 Michael Niedermayer <michaelni@gmx.at>
//	libavcodec/put_bits.h:  copyright (c) 2004 Michael Niedermayer <michaelni@gmx.at>
//	libavcodec/bitstream.c: Copyright (c) 2000, 2001 Fabrice Bellard
//	                        Copyright (c) 2002-2004 Michael Niedermayer <michaelni@gmx.at>
//	                        Copyright (c) 2010 Loren Merritt
//	                        alternative bitstream reader & writer by Michael Niedermayer <michaelni@gmx.at>
//
// MODIFIED VERSION: this file was translated from C to Go and changed for the F.E.A.R. console content
// installer in 2026-11 (the bit reader and writer are reduced to the operations the XMA decoder uses).
//
// This file is part of a derivative of FFmpeg. FFmpeg is free software; you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by the Free Software Foundation;
// either version 2.1 of the License, or (at your option) any later version (LGPL-2.1-or-later). FFmpeg is
// distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty
// of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser General Public License for more
// details, LICENSE-FFMPEG.md in this folder, and licenses/FFmpeg-LICENSE.txt.

package xma2

// bitReader is FFmpeg's GetBitContext with CONFIG_SAFE_BITSTREAM_READER: big-endian (MSB first) bit
// order, and the read position never advances past sizeBits+8. Bytes past the end of buf read as zero
// (FFmpeg's input padding).
type bitReader struct {
	buf      []byte
	index    int
	sizeBits int
}

func newBitReader(buf []byte, sizeBits int) bitReader {
	return bitReader{buf: buf, sizeBits: sizeBits}
}

// peek returns the next n (0..32) bits without moving.
func (g *bitReader) peek(n int) uint32 {
	if n == 0 {
		return 0
	}
	// Load 5 bytes from the byte holding the current position; enough for 32 bits at any bit offset.
	var w uint64
	b := g.index >> 3
	for i := 0; i < 5; i++ {
		w <<= 8
		if b+i >= 0 && b+i < len(g.buf) {
			w |= uint64(g.buf[b+i])
		}
	}
	// The next bit is bit 39-(index&7) of w; take n bits from there.
	return uint32((w >> uint(40-(g.index&7)-n)) & (1<<uint(n) - 1))
}

func (g *bitReader) skip(n int) {
	g.index += n
	if lim := g.sizeBits + 8; g.index > lim {
		g.index = lim
	}
}

// bits is get_bits / get_bits_long / get_bitsz (n may be 0).
func (g *bitReader) bits(n int) uint32 {
	v := g.peek(n)
	g.skip(n)
	return v
}

func (g *bitReader) bit() uint32 { return g.bits(1) }

// sbits is get_sbits: an n-bit two's complement value.
func (g *bitReader) sbits(n int) int32 {
	v := g.bits(n)
	return int32(v<<uint(32-n)) >> uint(32-n)
}

func (g *bitReader) count() int { return g.index }
func (g *bitReader) left() int  { return g.sizeBits - g.index }

// bitWriter is the part of FFmpeg's PutBitContext used by the WMA Pro bit reservoir: it writes bits
// MSB first into buf; flushed partial bytes are zero filled (flush_put_bits).
type bitWriter struct {
	buf []byte
	pos int // bits written
}

func (p *bitWriter) reset() { p.pos = 0 }

// put writes the low n bits of v (n <= 32).
func (p *bitWriter) put(n int, v uint32) {
	for i := n - 1; i >= 0; i-- {
		byteIdx := p.pos >> 3
		mask := byte(0x80 >> uint(p.pos&7))
		if p.pos&7 == 0 {
			p.buf[byteIdx] = 0 // a fresh byte starts zeroed, as flush_put_bits leaves it
		}
		if (v>>uint(i))&1 != 0 {
			p.buf[byteIdx] |= mask
		} else {
			p.buf[byteIdx] &^= mask
		}
		p.pos++
	}
}

// copyBits is ff_copy_bits: append length bits read MSB first from the byte-aligned src.
func (p *bitWriter) copyBits(src []byte, length int) {
	r := newBitReader(src, len(src)*8)
	for length > 0 {
		n := length
		if n > 16 {
			n = 16
		}
		p.put(n, r.bits(n))
		length -= n
	}
}

// avLog2 is av_log2 (floor(log2(v)), 0 for v == 0).
func avLog2(v uint32) int {
	n := 0
	for v > 1 {
		v >>= 1
		n++
	}
	return n
}
