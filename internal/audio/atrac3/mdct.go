// Derived from FFmpeg 7.1 (tag n7.1): libavutil/tx_template.c (ff_tx_mdct_gen_exp, ff_tx_mdct_inv,
// ff_tx_mdct_inv_full).
//
// Copyright of the FFmpeg file this file is derived from, as it states it:
//
//	libavutil/tx_template.c: Copyright (c) Lynne
//	                         Power of two FFT:
//	                         Copyright (c) Lynne
//	                         Copyright (c) 2008 Loren Merritt
//	                         Copyright (c) 2002 Fabrice Bellard
//	                         Partly based on libdjbfft by D. J. Bernstein
//
// MODIFIED VERSION: this file was translated from C to Go and changed for the F.E.A.R. console content
// installer in 2026-11. The sub-FFT is a plain radix-2 FFT written for this port (FFmpeg uses split-radix
// codelets; the transform is the same, only the float rounding order differs).
//
// This file is part of a derivative of FFmpeg. FFmpeg is free software; you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by the Free Software Foundation;
// either version 2.1 of the License, or (at your option) any later version (LGPL-2.1-or-later). FFmpeg is
// distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty
// of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser General Public License for more
// details, LICENSE-FFMPEG.md in the parent audio package, and licenses/FFmpeg-LICENSE.txt.

package atrac3

import "math"

// imdct is FFmpeg's AV_TX_FLOAT_MDCT inverse transform with AV_TX_FULL_IMDCT for 256 coefficients (512 output
// samples) and scale 1/32768, which atrac3.c initialises. Its definition (tx naive reference) is
//
//	y[n] = -scale * sum_k X[k] cos(2*pi/512 * (n + 1/2 + 128) * (k + 1/2)),  n = 0..511
//
// computed the fast way: pre-twiddle, 128-point complex inverse FFT (e^{+i}), post-twiddle, then the full
// output is unfolded from the 256-sample half.
type imdct struct {
	exp    [128]complex64 // twiddles: cos/sin(pi/2 * (i + 1/8) / 128) * sqrt(scale)
	fftTw  [64]complex64  // e^{+2 pi i k / 128}
	bitrev [128]int
	z      [128]complex64
}

const mdctScale = 1.0 / 32768

func newIMDCT() *imdct {
	m := &imdct{}
	s := math.Sqrt(mdctScale)
	for i := 0; i < 128; i++ {
		a := math.Pi / 2 * (float64(i) + 1.0/8.0) / 128
		m.exp[i] = complex(float32(math.Cos(a)*s), float32(math.Sin(a)*s))
	}
	for k := 0; k < 64; k++ {
		a := 2 * math.Pi * float64(k) / 128
		m.fftTw[k] = complex(float32(math.Cos(a)), float32(math.Sin(a)))
	}
	for i := 0; i < 128; i++ {
		r := 0
		for b := 0; b < 7; b++ {
			r |= ((i >> b) & 1) << (6 - b)
		}
		m.bitrev[i] = r
	}
	return m
}

// cmul is a complex float multiply with each product rounded to float (no fused multiply-add), as C float code.
func cmul(are, aim, bre, bim float32) (float32, float32) {
	return float32(are*bre) - float32(aim*bim), float32(are*bim) + float32(aim*bre)
}

// fft128 is an in-place unnormalised inverse FFT (e^{+i}) of 128 points.
func (m *imdct) fft128(z *[128]complex64) {
	for i := 0; i < 128; i++ {
		if j := m.bitrev[i]; j > i {
			z[i], z[j] = z[j], z[i]
		}
	}
	for size := 2; size <= 128; size <<= 1 {
		half, step := size>>1, 128/size
		for start := 0; start < 128; start += size {
			for k := 0; k < half; k++ {
				w := m.fftTw[k*step]
				b := z[start+k+half]
				tr, ti := cmul(real(b), imag(b), real(w), imag(w))
				a := z[start+k]
				z[start+k] = complex(real(a)+tr, imag(a)+ti)
				z[start+k+half] = complex(real(a)-tr, imag(a)-ti)
			}
		}
	}
}

// transform computes the 512 output samples of in[0:256] into out[0:512].
func (m *imdct) transform(out []float32, in []float32) {
	_ = out[511]
	_ = in[255]
	z := &m.z
	// Pre-twiddle (ff_tx_mdct_inv, identity input map): tmp = {in[255-2i], in[2i]}; z[i] = tmp * exp[i].
	for i := 0; i < 128; i++ {
		e := m.exp[i]
		re, im := cmul(in[255-2*i], in[2*i], real(e), imag(e))
		z[i] = complex(re, im)
	}
	m.fft128(z)
	// Post-twiddle, writing the 256-sample half into out[128:384] as interleaved re/im.
	var zr, zi [128]float32
	for i := range z {
		zr[i], zi[i] = real(z[i]), imag(z[i])
	}
	for i := 0; i < 64; i++ {
		i0, i1 := 64+i, 63-i
		s1re, s1im := zi[i1], zr[i1]
		s0re, s0im := zi[i0], zr[i0]
		e1, e0 := m.exp[i1], m.exp[i0]
		// CMUL(z[i1].re, z[i0].im, src1, exp[i1].im, exp[i1].re)
		a, b := cmul(s1re, s1im, imag(e1), real(e1))
		// CMUL(z[i0].re, z[i1].im, src0, exp[i0].im, exp[i0].re)
		c, d := cmul(s0re, s0im, imag(e0), real(e0))
		zr[i1], zi[i0] = a, b
		zr[i0], zi[i1] = c, d
	}
	half := out[128:384]
	for i := 0; i < 128; i++ {
		half[2*i], half[2*i+1] = zr[i], zi[i]
	}
	// ff_tx_mdct_inv_full unfold.
	for i := 0; i < 128; i++ {
		out[i] = -out[255-i]
		out[511-i] = out[256+i]
	}
}
