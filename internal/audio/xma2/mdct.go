// Derived from FFmpeg 7.1 (tag n7.1): libavutil/tx_template.c (ff_tx_mdct_inv, ff_tx_mdct_gen_exp,
// ff_tx_mdct_naive_inv), libavcodec/sinewin_tablegen.h (ff_sine_window_init) and libavutil/float_dsp.c
// (vector_fmul_window, C version).
//
// Copyright of the FFmpeg files this file is derived from, as they state it:
//
//	libavutil/tx_template.c:         Copyright (c) Lynne
//	                                 Power of two FFT:
//	                                 Copyright (c) Lynne
//	                                 Copyright (c) 2008 Loren Merritt
//	                                 Copyright (c) 2002 Fabrice Bellard
//	                                 Partly based on libdjbfft by D. J. Bernstein
//	libavcodec/sinewin_tablegen.h:   Copyright (c) 2009 Reimar Döffinger <Reimar.Doeffinger@gmx.de>
//	libavutil/float_dsp.c:           Copyright 2005 Balatoni Denes
//	                                 Copyright 2006 Loren Merritt
//
// MODIFIED VERSION: this file was translated from C to Go and changed for the F.E.A.R. console content
// installer in 2026-11. The n/2-point FFT is a plain radix-2 FFT written for this port (FFmpeg uses
// split-radix codelets).
//
// This file is part of a derivative of FFmpeg. FFmpeg is free software; you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by the Free Software Foundation;
// either version 2.1 of the License, or (at your option) any later version (LGPL-2.1-or-later). FFmpeg is
// distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty
// of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser General Public License for more
// details, LICENSE-FFMPEG.md in this folder, and licenses/FFmpeg-LICENSE.txt.

package xma2

import "math"

// imdct is libavutil's half-length inverse float MDCT (AV_TX_FLOAT_MDCT, inverse): n coefficients in,
// n samples out (the middle half of the 2n-sample window), scaled by scale.
//
// The pre- and post-rotation follow ff_tx_mdct_inv exactly in float32. The n/2-point complex FFT in
// between is a plain radix-2 FFT in float32; FFmpeg uses split-radix (and SIMD) codelets, so the
// last bits of the output can differ (about 1e-7 relative), far below 16-bit resolution.
type imdct struct {
	n          int
	expRe      []float32 // gen_exp table (natural order), n/2 entries
	expIm      []float32
	twRe, twIm []float32 // FFT twiddles for n/2 points
	rev        []int     // bit reversal for n/2 points
	zRe, zIm   []float32 // work buffers
}

func newIMDCT(n int, scale float64) *imdct {
	m := &imdct{n: n}
	len4 := n >> 1 // gen_exp's "len4" is s->len >> 1
	theta := 1.0 / 8.0
	if scale < 0 {
		theta += float64(len4)
	}
	sc := math.Sqrt(math.Abs(scale))
	m.expRe = make([]float32, len4)
	m.expIm = make([]float32, len4)
	for i := 0; i < len4; i++ {
		alpha := math.Pi / 2 * (float64(i) + theta) / float64(len4)
		m.expRe[i] = float32(math.Cos(alpha) * sc)
		m.expIm[i] = float32(math.Sin(alpha) * sc)
	}
	half := n / 2
	m.twRe = make([]float32, half/2)
	m.twIm = make([]float32, half/2)
	for k := 0; k < half/2; k++ {
		a := 2 * math.Pi * float64(k) / float64(half)
		m.twRe[k] = float32(math.Cos(a))
		m.twIm[k] = float32(math.Sin(a)) // +sin: inverse FFT
	}
	bits := avLog2(uint32(half))
	m.rev = make([]int, half)
	for i := range m.rev {
		r := 0
		for b := 0; b < bits; b++ {
			if i&(1<<uint(b)) != 0 {
				r |= 1 << uint(bits-1-b)
			}
		}
		m.rev[i] = r
	}
	m.zRe = make([]float32, half)
	m.zIm = make([]float32, half)
	return m
}

// fft is an in-place inverse complex FFT (exponent +i, unscaled) of m.zRe/m.zIm.
func (m *imdct) fft() {
	re, im := m.zRe, m.zIm
	n := len(re)
	for i, r := range m.rev {
		if r > i {
			re[i], re[r] = re[r], re[i]
			im[i], im[r] = im[r], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		hs := size >> 1
		step := n / size
		for start := 0; start < n; start += size {
			for k := 0; k < hs; k++ {
				wr, wi := m.twRe[k*step], m.twIm[k*step]
				a, b := start+k, start+k+hs
				tr := float32(re[b]*wr) - float32(im[b]*wi)
				ti := float32(re[b]*wi) + float32(im[b]*wr)
				re[b] = re[a] - tr
				im[b] = im[a] - ti
				re[a] = re[a] + tr
				im[a] = im[a] + ti
			}
		}
	}
}

// inverse computes dst[0:n] from src[0:n] (ff_tx_mdct_inv with the identity sub-transform map).
func (m *imdct) inverse(dst, src []float32) {
	n := m.n
	len2 := n >> 1
	len4 := n >> 2
	// The extra float32() conversions keep each product rounded on its own (no fused multiply-add).
	for i := 0; i < len2; i++ {
		k := 2 * i
		tre, tim := src[n-1-k], src[k]
		er, ei := m.expRe[i], m.expIm[i]
		m.zRe[i] = float32(tre*er) - float32(tim*ei)
		m.zIm[i] = float32(tre*ei) + float32(tim*er)
	}
	m.fft()
	z := m.zRe
	zi := m.zIm
	for i := 0; i < len4; i++ {
		i0, i1 := len4+i, len4-i-1
		s1re, s1im := zi[i1], z[i1]
		s0re, s0im := zi[i0], z[i0]
		z[i1] = float32(s1re*m.expIm[i1]) - float32(s1im*m.expRe[i1])
		zi[i0] = float32(s1re*m.expRe[i1]) + float32(s1im*m.expIm[i1])
		z[i0] = float32(s0re*m.expIm[i0]) - float32(s0im*m.expRe[i0])
		zi[i1] = float32(s0re*m.expRe[i0]) + float32(s0im*m.expIm[i0])
	}
	for i := 0; i < len2; i++ {
		dst[2*i] = z[i]
		dst[2*i+1] = zi[i]
	}
}

// naiveIMDCT is ff_tx_mdct_naive_inv (double precision reference, used by the tests).
func naiveIMDCT(dst, src []float32, scale float64) {
	l := len(src) >> 1
	len2 := l * 2
	phase := math.Pi / (4.0 * float64(len2))
	for i := 0; i < l; i++ {
		var sumD, sumU float64
		iD := phase * float64(4*l-2*i-1)
		iU := phase * float64(3*len2+2*i+1)
		for j := 0; j < len2; j++ {
			a := float64(2*j + 1)
			sumD += math.Cos(a*iD) * float64(src[j])
			sumU += math.Cos(a*iU) * float64(src[j])
		}
		dst[i] = float32(sumD * scale)
		dst[i+l] = float32(-sumU * scale)
	}
}

// sineWindow is ff_sine_window_init: w[i] = sinf((i + 0.5) * (pi / (2n))).
func sineWindow(n int) []float32 {
	w := make([]float32, n)
	for i := range w {
		x := float32((float64(i) + 0.5) * (math.Pi / (2.0 * float64(n))))
		w[i] = float32(math.Sin(float64(x)))
	}
	return w
}

// vectorFmulWindow is AVFloatDSPContext.vector_fmul_window (C reference): dst may alias src0.
// dst[0:2*l] from src0[0:l], src1[0:l] and win[0:2*l]. Indices are given as offsets into buf.
func vectorFmulWindow(buf []float32, dst, src0, src1 int, win []float32, l int) {
	d := dst + l
	s0 := src0 + l
	w := l
	for i, j := -l, l-1; i < 0; i, j = i+1, j-1 {
		x0 := buf[s0+i]
		x1 := buf[src1+j]
		wi := win[w+i]
		wj := win[w+j]
		buf[d+i] = float32(x0*wj) - float32(x1*wi)
		buf[d+j] = float32(x0*wi) + float32(x1*wj)
	}
}
