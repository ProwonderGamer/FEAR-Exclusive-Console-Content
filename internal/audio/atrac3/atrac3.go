// Derived from FFmpeg 7.1 (tag n7.1): libavcodec/atrac3.c, libavcodec/atrac.c and libavcodec/atrac.h.
//
// Copyright of the FFmpeg files this file is derived from, as they state it:
//
//	libavcodec/atrac3.c: Copyright (c) 2006-2008 Maxim Poliakovski
//	                     Copyright (c) 2006-2008 Benjamin Larsson
//	libavcodec/atrac.c:  Copyright (c) 2006-2013 Maxim Poliakovski
//	                     Copyright (c) 2006-2008 Benjamin Larsson
//	libavcodec/atrac.h:  Copyright (c) 2009-2013 Maxim Poliakovski
//	                     Copyright (c) 2009 Benjamin Larsson
//
// MODIFIED VERSION: this file was translated from C to Go and changed for the F.E.A.R. console content
// installer in 2026-11. Only the single-channels decoding path is kept; the numerics follow FFmpeg's float
// decoder (float32 arithmetic, products rounded before sums) because FFmpeg made the reference files this
// port is tested against.
//
// This file is part of a derivative of FFmpeg. FFmpeg is free software; you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by the Free Software Foundation;
// either version 2.1 of the License, or (at your option) any later version (LGPL-2.1-or-later). FFmpeg is
// distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty
// of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser General Public License for more
// details, LICENSE-FFMPEG.md in the parent audio package, and licenses/FFmpeg-LICENSE.txt.

// Package atrac3 decodes Sony ATRAC3 audio (the codec of the PS3 F.E.A.R. MSF sound files) to float samples.
//
// Scope: the "single channels" coding mode (MSF codecs 5 and 6, any channel count). Joint stereo (MSF codec 4)
// is not ported; New refuses it.
package atrac3

import (
	"errors"
	"fmt"
)

// SamplesPerFrame is the number of samples per channel in one ATRAC3 frame.
const SamplesPerFrame = 1024

// Delay is the decoder delay FFmpeg's extradata check requires (0x88E samples); FFmpeg does not trim it.
const Delay = 0x88E

type gainInfo struct {
	numPoints int
	levCode   [7]int
	locCode   [7]int
}

type gainBlock struct{ g [4]gainInfo }

type tonalComponent struct {
	pos      int
	numCoefs int
	coef     [8]float32
}

type channelUnit struct {
	bandsCoded    int
	numComponents int
	prevFrame     [SamplesPerFrame]float32
	gcBlkSwitch   int
	components    [64]tonalComponent
	gainBlock     [2]gainBlock
	spectrum      [SamplesPerFrame]float32
	imdctBuf      [SamplesPerFrame]float32
	delayBuf1     [46]float32
	delayBuf2     [46]float32
	delayBuf3     [46]float32
}

// Decoder holds the state of one ATRAC3 stream.
type Decoder struct {
	channels   int
	blockAlign int
	units      []channelUnit
	mdct       *imdct
	tempBuf    [1070]float32
}

// New returns a decoder for a stream with the given channel count and frame size in bytes (all channels).
func New(channels, blockAlign int, jointStereo bool) (*Decoder, error) {
	if channels < 1 || channels > 8 {
		return nil, fmt.Errorf("atrac3: unsupported channel count %d", channels)
	}
	if jointStereo {
		return nil, errors.New("atrac3: joint stereo streams are not supported by this port")
	}
	ok := false
	for _, b := range []int{96, 152, 192} {
		if blockAlign == b*channels {
			ok = true
		}
	}
	if !ok {
		return nil, fmt.Errorf("atrac3: unknown frame size %d for %d channel(s)", blockAlign, channels)
	}
	return &Decoder{channels: channels, blockAlign: blockAlign, units: make([]channelUnit, channels), mdct: newIMDCT()}, nil
}

// DecodeAll decodes every whole frame of data and returns one sample slice per channel (full scale +-1.0).
// A trailing partial frame is ignored, as FFmpeg rejects it ("Frame too small").
func DecodeAll(data []byte, channels, blockAlign int, jointStereo bool) ([][]float32, error) {
	d, err := New(channels, blockAlign, jointStereo)
	if err != nil {
		return nil, err
	}
	n := len(data) / blockAlign
	out := make([][]float32, channels)
	for c := range out {
		out[c] = make([]float32, n*SamplesPerFrame)
	}
	frame := make([][]float32, channels)
	for i := 0; i < n; i++ {
		for c := range frame {
			frame[c] = out[c][i*SamplesPerFrame : (i+1)*SamplesPerFrame]
		}
		if err := d.DecodeFrame(data[i*blockAlign:(i+1)*blockAlign], frame); err != nil {
			return nil, fmt.Errorf("atrac3: frame %d: %w", i, err)
		}
	}
	return out, nil
}

// DecodeFrame decodes one frame (blockAlign bytes) into out[ch][0:1024].
func (d *Decoder) DecodeFrame(frame []byte, out [][]float32) error {
	if len(frame) < d.blockAlign {
		return errors.New("frame too small")
	}
	if len(out) < d.channels {
		return errors.New("not enough output channels")
	}
	per := d.blockAlign / d.channels
	for i := 0; i < d.channels; i++ {
		if len(out[i]) < SamplesPerFrame {
			return errors.New("output buffer too small")
		}
		// FFmpeg reads each channel's sound unit from its share of the frame; bits past it come from the next
		// channel's bytes there, which never matters for valid data. Here they read as zero.
		gb := newBitReader(frame[i*per : (i+1)*per])
		if err := d.decodeChannelSoundUnit(gb, &d.units[i], out[i]); err != nil {
			return err
		}
	}
	// iQMF synthesis: two 2-band stages, then the final 2-band stage.
	for i := 0; i < d.channels; i++ {
		u := &d.units[i]
		p := out[i]
		p1, p2, p3, p4 := p[0:], p[256:], p[512:], p[768:]
		iqmf(p1, p2, 256, p1, u.delayBuf1[:], d.tempBuf[:])
		iqmf(p4, p3, 256, p3, u.delayBuf2[:], d.tempBuf[:])
		iqmf(p1, p3, 512, p1, u.delayBuf3[:], d.tempBuf[:])
	}
	return nil
}

// readQuantSpectralCoeffs reads num mantissas coded with the given selector (constant or variable length).
func readQuantSpectralCoeffs(gb *bitReader, selector, codingFlag int, mantissas []int, num int) {
	if selector == 1 {
		num /= 2
	}
	if codingFlag != 0 {
		nb := clcLengthTab[selector]
		if selector > 1 {
			for i := 0; i < num; i++ {
				code := 0
				if nb != 0 {
					code = gb.getS(nb)
				}
				mantissas[i] = code
			}
		} else {
			for i := 0; i < num; i++ {
				code := 0
				if nb != 0 {
					code = gb.get(nb) // always 4 bits here
				}
				mantissas[2*i] = mantissaCLCTab[code>>2]
				mantissas[2*i+1] = mantissaCLCTab[code&3]
			}
		}
		return
	}
	t := &vlcTables[selector-1]
	if selector != 1 {
		for i := 0; i < num; i++ {
			mantissas[i] = gb.vlc(t)
		}
	} else {
		for i := 0; i < num; i++ {
			s := gb.vlc(t)
			if s < 0 { // invalid code: FFmpeg would index before the table; treat as zero pair
				s = 0
			}
			mantissas[2*i] = mantissaVLCTab[2*s]
			mantissas[2*i+1] = mantissaVLCTab[2*s+1]
		}
	}
}

// decodeSpectrum restores the quantised subband spectrum and returns the index of the last coded subband.
func decodeSpectrum(gb *bitReader, output []float32) int {
	var vlcIndex, sfIndex [32]int
	var mantissas [128]int
	numSubbands := gb.get(5)
	codingMode := gb.get1()
	for i := 0; i <= numSubbands; i++ {
		vlcIndex[i] = gb.get(3)
	}
	for i := 0; i <= numSubbands; i++ {
		if vlcIndex[i] != 0 {
			sfIndex[i] = gb.get(6)
		}
	}
	i := 0
	for ; i <= numSubbands; i++ {
		first, last := subbandTab[i], subbandTab[i+1]
		if vlcIndex[i] != 0 {
			readQuantSpectralCoeffs(gb, vlcIndex[i], codingMode, mantissas[:], last-first)
			scale := sfTable[sfIndex[i]] * invMaxQuant[vlcIndex[i]]
			for j := 0; first < last; first, j = first+1, j+1 {
				output[first] = float32(mantissas[j]) * scale
			}
		} else {
			clear(output[first:last])
		}
	}
	clear(output[subbandTab[i]:SamplesPerFrame])
	return numSubbands
}

var errInvalid = errors.New("invalid data")

// decodeTonalComponents restores the tonal components; returns their count.
func decodeTonalComponents(gb *bitReader, comps *[64]tonalComponent, numBands int) (int, error) {
	var bandFlags [4]int
	var mantissa [8]int
	count := 0
	nb := gb.get(5)
	if nb == 0 {
		return 0, nil
	}
	selector := gb.get(2)
	if selector == 2 {
		return 0, errInvalid
	}
	codingMode := selector & 1
	for i := 0; i < nb; i++ {
		for b := 0; b <= numBands; b++ {
			bandFlags[b] = gb.get1()
		}
		valuesPerComponent := gb.get(3)
		quantStep := gb.get(3)
		if quantStep <= 1 {
			return 0, errInvalid
		}
		if selector == 3 {
			codingMode = gb.get1()
		}
		for b := 0; b < (numBands+1)*4; b++ {
			if bandFlags[b>>2] == 0 {
				continue
			}
			coded := gb.get(3)
			for c := 0; c < coded; c++ {
				sf := gb.get(6)
				if count >= 64 {
					return 0, errInvalid
				}
				cmp := &comps[count]
				cmp.pos = b*64 + gb.get(6)
				n := min(SamplesPerFrame-cmp.pos, valuesPerComponent+1)
				scale := sfTable[sf] * invMaxQuant[quantStep]
				readQuantSpectralCoeffs(gb, quantStep, codingMode, mantissa[:], n)
				cmp.numCoefs = n
				for m := 0; m < n; m++ {
					cmp.coef[m] = float32(mantissa[m]) * scale
				}
				count++
			}
		}
	}
	return count, nil
}

// decodeGainControl reads the gain control points of the coded bands into blk.
func decodeGainControl(gb *bitReader, blk *gainBlock, numBands int) error {
	b := 0
	for ; b <= numBands; b++ {
		g := &blk.g[b]
		g.numPoints = gb.get(3)
		for j := 0; j < g.numPoints; j++ {
			g.levCode[j] = gb.get(4)
			g.locCode[j] = gb.get(5)
			if j > 0 && g.locCode[j] <= g.locCode[j-1] {
				return errInvalid
			}
		}
	}
	for ; b < 4; b++ {
		blk.g[b].numPoints = 0
	}
	return nil
}

func addTonalComponents(spectrum []float32, comps []tonalComponent) int {
	last := -1
	for i := range comps {
		c := &comps[i]
		last = max(c.pos+c.numCoefs, last)
		for j := 0; j < c.numCoefs; j++ {
			spectrum[c.pos+j] += c.coef[j]
		}
	}
	return last
}

// imlt is the 512-point IMDCT of one QMF band with the odd-band spectrum reversal, then the window.
func (d *Decoder) imlt(input, output []float32, oddBand bool) {
	if oddBand {
		for i := 0; i < 128; i++ {
			input[i], input[255-i] = input[255-i], input[i]
		}
	}
	d.mdct.transform(output, input)
	for i := 0; i < 512; i++ {
		output[i] *= mdctWindow[i]
	}
}

func (d *Decoder) decodeChannelSoundUnit(gb *bitReader, u *channelUnit, output []float32) error {
	gain1 := &u.gainBlock[u.gcBlkSwitch]
	gain2 := &u.gainBlock[1-u.gcBlkSwitch]
	if gb.get(6) != 0x28 {
		return errors.New("sound unit id != 0x28")
	}
	u.bandsCoded = gb.get(2)
	if err := decodeGainControl(gb, gain2, u.bandsCoded); err != nil {
		return err
	}
	n, err := decodeTonalComponents(gb, &u.components, u.bandsCoded)
	if err != nil {
		return err
	}
	u.numComponents = n
	numSubbands := decodeSpectrum(gb, u.spectrum[:])
	lastTonal := addTonalComponents(u.spectrum[:], u.components[:n])

	// Number of QMF bands in use from the coded spectral lines (arithmetic shift: -1 when nothing is coded).
	numBands := (subbandTab[numSubbands] - 1) >> 8
	if lastTonal >= 0 {
		numBands = max((lastTonal+256)>>8, numBands)
	}
	for band := 0; band < 4; band++ {
		if band <= numBands {
			d.imlt(u.spectrum[band*256:], u.imdctBuf[:], band&1 == 1)
		} else {
			clear(u.imdctBuf[:512])
		}
		gainCompensation(u.imdctBuf[:], u.prevFrame[band*256:], &gain1.g[band], &gain2.g[band], 256,
			output[band*256:])
	}
	u.gcBlkSwitch ^= 1
	return nil
}

// gainCompensation applies the gain control envelope and the MDCT overlap (ff_atrac_gain_compensation with
// id2exp_offset 4, loc_scale 3).
func gainCompensation(in, prev []float32, now, next *gainInfo, num int, out []float32) {
	gcScale := float32(1.0)
	if next.numPoints != 0 {
		gcScale = gainTab1[next.levCode[0]]
	}
	if now.numPoints == 0 {
		for pos := 0; pos < num; pos++ {
			out[pos] = float32(in[pos]*gcScale) + prev[pos]
		}
	} else {
		pos := 0
		for i := 0; i < now.numPoints; i++ {
			lastpos := now.locCode[i] << gcLocScale
			lev := gainTab1[now.levCode[i]]
			nextLev := gcID2ExpOffset
			if i+1 < now.numPoints {
				nextLev = now.levCode[i+1]
			}
			gainInc := gainTab2[nextLev-now.levCode[i]+15]
			for ; pos < lastpos; pos++ {
				out[pos] = (float32(in[pos]*gcScale) + prev[pos]) * lev
			}
			for ; pos < lastpos+gcLocSize; pos++ {
				out[pos] = (float32(in[pos]*gcScale) + prev[pos]) * lev
				lev *= gainInc
			}
		}
		for ; pos < num; pos++ {
			out[pos] = float32(in[pos]*gcScale) + prev[pos]
		}
	}
	copy(prev[:num], in[num:2*num])
}

// iqmf is the quadrature mirror synthesis filter (ff_atrac_iqmf): combines a low and a high band of nIn samples
// each into 2*nIn output samples. In-place output over inlo is allowed.
func iqmf(inlo, inhi []float32, nIn int, out []float32, delayBuf, temp []float32) {
	copy(temp[:46], delayBuf[:46])
	p3 := temp[46:]
	for i := 0; i < nIn; i += 2 {
		p3[2*i+0] = inlo[i] + inhi[i]
		p3[2*i+1] = inlo[i] - inhi[i]
		p3[2*i+2] = inlo[i+1] + inhi[i+1]
		p3[2*i+3] = inlo[i+1] - inhi[i+1]
	}
	p1 := temp
	for j := 0; j < nIn; j++ {
		var s1, s2 float32
		for i := 0; i < 48; i += 2 {
			s1 += float32(p1[i] * qmfWindow[i])
			s2 += float32(p1[i+1] * qmfWindow[i+1])
		}
		out[2*j] = s2
		out[2*j+1] = s1
		p1 = p1[2:]
	}
	copy(delayBuf[:46], temp[nIn*2:nIn*2+46])
}
