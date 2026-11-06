// Derived from FFmpeg 7.1 (tag n7.1): libavcodec/wmaprodec.c (WMA 9/3/PRO compatible decoder, XMA1/XMA2
// paths) and libavcodec/wma.c (ff_wma_get_large_val, ff_wma_run_level_decode).
//
// Copyright of the FFmpeg files this file is derived from, as they state it:
//
//	libavcodec/wmaprodec.c: Copyright (c) 2007 Baptiste Coudurier, Benjamin Larsson, Ulion
//	                        Copyright (c) 2008 - 2011 Sascha Sommer, Benjamin Larsson
//	libavcodec/wma.c:       Copyright (c) 2002-2007 The FFmpeg Project
//
// MODIFIED VERSION: this file was translated from C to Go and changed for the F.E.A.R. console content
// installer in 2026-11. Only the float decoder for one XMA stream is kept; the parts XMA never reaches are
// left out (see below).
//
// This file is part of a derivative of FFmpeg. FFmpeg is free software; you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by the Free Software Foundation;
// either version 2.1 of the License, or (at your option) any later version (LGPL-2.1-or-later). FFmpeg is
// distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty
// of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser General Public License for more
// details, LICENSE-FFMPEG.md in this folder, and licenses/FFmpeg-LICENSE.txt.
//
// This is a port of the float decoder for one XMA stream (1 or 2 channels). Names follow the C source
// so the two can be read side by side. Parts that XMA never reaches (WMA Pro packet sequence numbers,
// LFE handling, decorrelation matrices for more than 2 channels) are left out.

package xma2

import (
	"errors"
	"math"
)

const (
	maxSubframes      = 32
	maxBands          = 29
	maxFramesize      = 32768
	blockMinBits      = 6
	blockMaxBits      = 13
	blockMaxSize      = 1 << blockMaxBits
	blockSizes        = blockMaxBits - blockMinBits + 1
	xmaMaxChanStream  = 2
	xmaBlockAlign     = 2048
	xmaSamplesPerFrm  = 512
	xmaDecodeFlags    = 0x10d6
	xmaBitsPerSample  = 16
	inputPaddingBytes = 64 // AV_INPUT_BUFFER_PADDING_SIZE
)

var errInvalid = errors.New("xma2: invalid bitstream")

// channelCtx is WMAProChannelCtx.
type channelCtx struct {
	prevBlockLen      int
	transmitCoefs     bool
	numSubframes      int
	subframeLen       [maxSubframes]int
	subframeOffset    [maxSubframes]int
	curSubframe       int
	decodedSamples    int
	grouped           bool
	quantStep         int
	reuseSF           bool
	scaleFactorStep   int
	maxScaleFactor    int
	savedScaleFactors [2][maxBands]int
	scaleFactorIdx    int
	scaleFactors      int // index into savedScaleFactors (the buffer used for decoding)
	tableIdx          int
	coeffs            int // offset of the subframe decode buffer inside out
	numVecCoeffs      int
	out               [blockMaxSize + blockMaxSize/2]float32
}

// chGroup is WMAProChannelGrp (2 channels at most for XMA).
type chGroup struct {
	numChannels   int
	transform     bool
	transformBand [maxBands]bool
	matrix        [4]float32
	channelData   [2]int // channel indexes
}

// wmaproStream is WMAProDecodeCtx for one XMA stream.
type wmaproStream struct {
	sampleRate int

	frameData [maxFramesize + inputPaddingBytes]byte
	pb        bitWriter
	tx        [blockSizes]*imdct
	tmp       [blockMaxSize]float32
	windows   [blockSizes][]float32

	decodeFlags             uint32
	lenPrefix               bool
	dynamicRangeCompression bool
	bitsPerSample           int
	samplesPerFrame         int
	trimStart, trimEnd      int
	log2FrameSize           int
	maxNumSubframes         int
	subframeLenBits         int
	maxSubframeLenBit       bool
	minSamplesPerSubframe   int
	numSfb                  [blockSizes]int
	sfbOffsets              [blockSizes][maxBands]int
	sfOffsets               [blockSizes][blockSizes][maxBands]int

	pgb             bitReader
	nextPacketStart int
	packetOffset    int
	numSavedBits    int
	frameOffset     int
	subframeOffset  int
	packetLoss      bool
	packetDone      bool
	eofDone         bool

	frameNum           uint32
	gb                 bitReader
	bufBitSize         int
	drcGain            int
	skipFrame          bool
	parsedAllSubframes bool
	skipPackets        int

	subframeLen                  int
	nbChannels                   int
	channelsForCurSubframe       int
	channelIndexesForCurSubframe [2]int
	numBands                     int
	transmitNumVecCoeffs         bool
	curSfbOffsets                int // table index into sfbOffsets
	tableIdx                     int
	escLen                       int

	numChgroups int
	chgroup     [2]chGroup
	channel     [2]channelCtx

	errors int // bitstream errors seen (FFmpeg logs them and goes on)
}

// getRate is get_rate for XMA.
func getRate(sampleRate int) int {
	switch {
	case sampleRate > 44100:
		return 48000
	case sampleRate > 32000:
		return 44100
	case sampleRate > 24000:
		return 32000
	}
	return 24000
}

// newStream is decode_init for an XMA stream with nbChannels channels.
func newStream(sampleRate, nbChannels int) (*wmaproStream, error) {
	if vlcErr != nil {
		return nil, vlcErr
	}
	if sampleRate <= 0 {
		return nil, errors.New("xma2: invalid sample rate")
	}
	if nbChannels < 1 || nbChannels > xmaMaxChanStream {
		return nil, errors.New("xma2: invalid number of channels per XMA stream")
	}
	s := &wmaproStream{sampleRate: sampleRate, nbChannels: nbChannels}
	s.pb.buf = s.frameData[:maxFramesize]
	s.decodeFlags = xmaDecodeFlags
	s.bitsPerSample = xmaBitsPerSample
	s.log2FrameSize = avLog2(xmaBlockAlign) + 4
	s.skipFrame = true
	s.packetLoss = true
	s.lenPrefix = s.decodeFlags&0x40 != 0
	s.samplesPerFrame = xmaSamplesPerFrm

	log2MaxNumSubframes := int((s.decodeFlags & 0x38) >> 3)
	s.maxNumSubframes = 1 << uint(log2MaxNumSubframes)
	if s.maxNumSubframes == 16 || s.maxNumSubframes == 4 {
		s.maxSubframeLenBit = true
	}
	s.subframeLenBits = avLog2(uint32(log2MaxNumSubframes)) + 1
	numPossibleBlockSizes := log2MaxNumSubframes + 1
	s.minSamplesPerSubframe = s.samplesPerFrame / s.maxNumSubframes
	s.dynamicRangeCompression = s.decodeFlags&0x80 != 0

	for i := 0; i < s.nbChannels; i++ {
		s.channel[i].prevBlockLen = s.samplesPerFrame
	}

	// scale factor bands and their offsets for every possible block size
	rate := getRate(sampleRate)
	for i := 0; i < numPossibleBlockSizes; i++ {
		subframeLen := s.samplesPerFrame >> uint(i)
		band := 1
		s.sfbOffsets[i][0] = 0
		for x := 0; x < maxBands-1 && s.sfbOffsets[i][band-1] < subframeLen; x++ {
			offset := (subframeLen*2*int(criticalFreq[x]))/rate + 2
			offset &^= 3
			if offset > s.sfbOffsets[i][band-1] {
				s.sfbOffsets[i][band] = offset
				band++
			}
			if offset >= subframeLen {
				break
			}
		}
		s.sfbOffsets[i][band-1] = subframeLen
		s.numSfb[i] = band - 1
		if s.numSfb[i] <= 0 {
			return nil, errors.New("xma2: num_sfb invalid")
		}
	}
	// scale factor resample matrix
	for i := 0; i < numPossibleBlockSizes; i++ {
		for b := 0; b < s.numSfb[i]; b++ {
			offset := ((s.sfbOffsets[i][b] + s.sfbOffsets[i][b+1] - 1) << uint(i)) >> 1
			for x := 0; x < numPossibleBlockSizes; x++ {
				v := 0
				for s.sfbOffsets[x][v+1]<<uint(x) < offset {
					v++
					if v >= maxBands {
						return nil, errors.New("xma2: sf_offsets out of range")
					}
				}
				s.sfOffsets[i][x][b] = v
			}
		}
	}
	// MDCTs and sine windows for every block size
	for i := 0; i < blockSizes; i++ {
		scale := float64(float32(1.0 / float64(int(1)<<uint(blockMinBits+i-1)) / float64(int64(1)<<uint(s.bitsPerSample-1))))
		s.tx[i] = newIMDCT(1<<uint(blockMinBits+i), scale)
		s.windows[i] = sineWindow(1 << uint(blockMinBits+i))
	}
	return s, nil
}

func (s *wmaproStream) decodeSubframeLength(offset int) (int, error) {
	if offset == s.samplesPerFrame-s.minSamplesPerSubframe {
		return s.minSamplesPerSubframe, nil
	}
	if s.gb.left() < 1 {
		return 0, errInvalid
	}
	frameLenShift := 0
	if s.maxSubframeLenBit {
		if s.gb.bit() != 0 {
			frameLenShift = 1 + int(s.gb.bits(s.subframeLenBits-1))
		}
	} else {
		frameLenShift = int(s.gb.bits(s.subframeLenBits))
	}
	subframeLen := s.samplesPerFrame >> uint(frameLenShift)
	if subframeLen < s.minSamplesPerSubframe || subframeLen > s.samplesPerFrame {
		return 0, errInvalid
	}
	return subframeLen, nil
}

func (s *wmaproStream) decodeTilehdr() error {
	var numSamples [2]int
	var containsSubframe [2]bool
	channelsForCurSubframe := s.nbChannels
	fixedChannelLayout := false
	minChannelLen := 0

	for c := 0; c < s.nbChannels; c++ {
		s.channel[c].numSubframes = 0
	}
	if s.maxNumSubframes == 1 || s.gb.bit() != 0 {
		fixedChannelLayout = true
	}
	for {
		for c := 0; c < s.nbChannels; c++ {
			if numSamples[c] == minChannelLen {
				if fixedChannelLayout || channelsForCurSubframe == 1 ||
					minChannelLen == s.samplesPerFrame-s.minSamplesPerSubframe {
					containsSubframe[c] = true
				} else {
					containsSubframe[c] = s.gb.bit() != 0
				}
			} else {
				containsSubframe[c] = false
			}
		}
		subframeLen, err := s.decodeSubframeLength(minChannelLen)
		if err != nil || subframeLen <= 0 {
			return errInvalid
		}
		minChannelLen += subframeLen
		for c := 0; c < s.nbChannels; c++ {
			ch := &s.channel[c]
			if containsSubframe[c] {
				if ch.numSubframes >= maxSubframes {
					return errInvalid
				}
				ch.subframeLen[ch.numSubframes] = subframeLen
				numSamples[c] += subframeLen
				ch.numSubframes++
				if numSamples[c] > s.samplesPerFrame {
					return errInvalid
				}
			} else if numSamples[c] <= minChannelLen {
				if numSamples[c] < minChannelLen {
					channelsForCurSubframe = 0
					minChannelLen = numSamples[c]
				}
				channelsForCurSubframe++
			}
		}
		if minChannelLen >= s.samplesPerFrame {
			break
		}
	}
	for c := 0; c < s.nbChannels; c++ {
		offset := 0
		for i := 0; i < s.channel[c].numSubframes; i++ {
			s.channel[c].subframeOffset[i] = offset
			offset += s.channel[c].subframeLen[i]
		}
	}
	return nil
}

func (s *wmaproStream) decodeChannelTransform() error {
	s.numChgroups = 0
	if s.nbChannels <= 1 {
		return nil // in the one channel case channel transforms are pointless
	}
	remaining := s.channelsForCurSubframe
	if s.gb.bit() != 0 {
		return errors.New("xma2: channel transform bit not supported")
	}
	for s.numChgroups = 0; remaining > 0 && s.numChgroups < s.channelsForCurSubframe; s.numChgroups++ {
		g := &s.chgroup[s.numChgroups]
		g.numChannels = 0
		g.transform = false
		// remaining_channels > 2 cannot happen with 2 channels per stream
		g.numChannels = remaining
		nd := 0
		for i := 0; i < s.channelsForCurSubframe; i++ {
			idx := s.channelIndexesForCurSubframe[i]
			if !s.channel[idx].grouped {
				g.channelData[nd] = idx
				nd++
			}
			s.channel[idx].grouped = true
		}
		if g.numChannels == 2 {
			if s.gb.bit() != 0 {
				if s.gb.bit() != 0 {
					return errors.New("xma2: unknown channel transform type")
				}
			} else {
				g.transform = true
				if s.nbChannels == 2 {
					g.matrix = [4]float32{1.0, -1.0, 1.0, 1.0}
				} else {
					g.matrix = [4]float32{0.70703125, -0.70703125, 0.70703125, 0.70703125}
				}
			}
		}
		if g.transform {
			if s.gb.bit() == 0 {
				for i := 0; i < s.numBands; i++ {
					g.transformBand[i] = s.gb.bit() != 0
				}
			} else {
				for i := 0; i < s.numBands; i++ {
					g.transformBand[i] = true
				}
			}
		}
		remaining -= g.numChannels
	}
	return nil
}

// getLargeVal is ff_wma_get_large_val.
func getLargeVal(g *bitReader) uint32 {
	nBits := 8
	if g.bit() != 0 {
		nBits += 8
		if g.bit() != 0 {
			nBits += 8
			if g.bit() != 0 {
				nBits += 7
			}
		}
	}
	return g.bits(nBits)
}

// fvalTab holds the integers 0..15 as float32 bit patterns.
var fvalTab = func() (t [16]uint32) {
	for i := range t {
		t[i] = math.Float32bits(float32(i))
	}
	return
}()

func (s *wmaproStream) decodeCoeffs(c int) error {
	ci := &s.channel[c]
	coeffs := ci.out[ci.coeffs:]
	rlMode := false
	curCoeff := 0
	numZeros := 0
	vlctable := s.gb.bit()
	v := coefVLC[vlctable]
	run, level := coef0Run[:], coef0Level[:]
	if vlctable != 0 {
		run, level = coef1Run[:], coef1Level[:]
	}

	for (s.transmitNumVecCoeffs || !rlMode) && curCoeff+3 < ci.numVecCoeffs {
		var vals [4]uint32
		idx, ok := vec4VLC.read(&s.gb)
		if !ok {
			return errInvalid
		}
		if idx < 0 {
			for i := 0; i < 4; i += 2 {
				idx, ok = vec2VLC.read(&s.gb)
				if !ok {
					return errInvalid
				}
				if idx < 0 {
					v0s, ok0 := vec1VLC.read(&s.gb)
					if !ok0 {
						return errInvalid
					}
					v0 := uint32(v0s)
					if v0 == uint32(len(vec1Lens)-1) {
						v0 += getLargeVal(&s.gb)
					}
					v1s, ok1 := vec1VLC.read(&s.gb)
					if !ok1 {
						return errInvalid
					}
					v1 := uint32(v1s)
					if v1 == uint32(len(vec1Lens)-1) {
						v1 += getLargeVal(&s.gb)
					}
					vals[i] = math.Float32bits(float32(v0))
					vals[i+1] = math.Float32bits(float32(v1))
				} else {
					vals[i] = fvalTab[idx>>4]
					vals[i+1] = fvalTab[idx&0xF]
				}
			}
		} else {
			vals[0] = fvalTab[idx>>12]
			vals[1] = fvalTab[(idx>>8)&0xF]
			vals[2] = fvalTab[(idx>>4)&0xF]
			vals[3] = fvalTab[idx&0xF]
		}
		for i := 0; i < 4; i++ {
			if vals[i] != 0 {
				sign := s.gb.bit() - 1 // 0 or 0xffffffff
				coeffs[curCoeff] = math.Float32frombits(vals[i] ^ sign<<31)
				numZeros = 0
			} else {
				coeffs[curCoeff] = 0
				numZeros++
				if numZeros > s.subframeLen>>8 {
					rlMode = true
				}
			}
			curCoeff++
		}
	}

	if curCoeff < s.subframeLen {
		for i := curCoeff; i < s.subframeLen; i++ {
			coeffs[i] = 0
		}
		return s.runLevelDecode(v, level, run, coeffs, curCoeff, s.subframeLen, s.subframeLen, s.escLen)
	}
	return nil
}

// runLevelDecode is ff_wma_run_level_decode with version 1.
func (s *wmaproStream) runLevelDecode(v *vlc, levelTable []float32, runTable []uint16, ptr []float32,
	offset, numCoefs, blockLen, frameLenBits int) error {
	coefMask := blockLen - 1
	g := &s.gb
	for ; offset < numCoefs; offset++ {
		code, ok := v.read(g)
		if !ok {
			return errInvalid
		}
		if code > 1 {
			offset += int(runTable[code])
			sign := g.bit() - 1
			ptr[offset&coefMask] = math.Float32frombits(math.Float32bits(levelTable[code]) ^ (sign & 0x80000000))
		} else if code == 1 {
			break // EOB
		} else {
			level := int32(getLargeVal(g))
			if g.bit() != 0 {
				if g.bit() != 0 {
					if g.bit() != 0 {
						return errInvalid // broken escape sequence
					}
					offset += int(g.bits(frameLenBits)) + 4
				} else {
					offset += int(g.bits(2)) + 1
				}
			}
			sign := int32(g.bit()) - 1
			ptr[offset&coefMask] = float32((level ^ sign) - sign)
		}
	}
	if offset > numCoefs {
		return errInvalid // overflow in spectral RLE
	}
	return nil
}

func (s *wmaproStream) decodeScaleFactors() error {
	for i := 0; i < s.channelsForCurSubframe; i++ {
		c := s.channelIndexesForCurSubframe[i]
		ch := &s.channel[c]
		ch.scaleFactors = 1 - ch.scaleFactorIdx
		sf := ch.savedScaleFactors[ch.scaleFactors][:]

		if ch.reuseSF {
			offs := s.sfOffsets[s.tableIdx][ch.tableIdx]
			for b := 0; b < s.numBands; b++ {
				sf[b] = ch.savedScaleFactors[ch.scaleFactorIdx][offs[b]]
			}
		}

		if ch.curSubframe == 0 || s.gb.bit() != 0 {
			if !ch.reuseSF {
				ch.scaleFactorStep = int(s.gb.bits(2)) + 1
				val := 45 / ch.scaleFactorStep
				for b := 0; b < s.numBands; b++ {
					d, ok := sfVLC.read(&s.gb)
					if !ok {
						return errInvalid
					}
					val += int(d)
					sf[b] = val
				}
			} else {
				for b := 0; b < s.numBands; b++ {
					idx, ok := sfRLVLC.read(&s.gb)
					if !ok {
						return errInvalid
					}
					var skip, val int
					var sign int
					if idx == 0 {
						code := s.gb.bits(14)
						val = int(code >> 6)
						sign = int(code&1) - 1
						skip = int((code & 0x3f) >> 1)
					} else if idx == 1 {
						break
					} else {
						skip = int(scaleRLRun[idx])
						val = int(scaleRLLevel[idx])
						sign = int(s.gb.bit()) - 1
					}
					b += skip
					if b >= s.numBands {
						return errInvalid // invalid scale factor coding
					}
					sf[b] += (val ^ sign) - sign
				}
			}
			ch.scaleFactorIdx = 1 - ch.scaleFactorIdx
			ch.tableIdx = s.tableIdx
			ch.reuseSF = true
		}

		ch.maxScaleFactor = sf[0]
		for b := 1; b < s.numBands; b++ {
			if sf[b] > ch.maxScaleFactor {
				ch.maxScaleFactor = sf[b]
			}
		}
	}
	return nil
}

func (s *wmaproStream) inverseChannelTransform() {
	for i := 0; i < s.numChgroups; i++ {
		g := &s.chgroup[i]
		if !g.transform {
			continue
		}
		sfb := s.sfbOffsets[s.curSfbOffsets][:]
		d0 := s.channel[g.channelData[0]].out[s.channel[g.channelData[0]].coeffs:]
		d1 := s.channel[g.channelData[1]].out[s.channel[g.channelData[1]].coeffs:]
		for b := 0; b < s.numBands; b++ {
			end := sfb[b+1]
			if end > s.subframeLen {
				end = s.subframeLen
			}
			if g.transformBand[b] {
				m := g.matrix
				for y := sfb[b]; y < end; y++ {
					a0, a1 := d0[y], d1[y]
					d0[y] = float32(0+float32(a0*m[0])) + float32(a1*m[1])
					d1[y] = float32(0+float32(a0*m[2])) + float32(a1*m[3])
				}
			} else if s.nbChannels == 2 {
				const k = float32(181.0 / 128)
				for y := sfb[b]; y < end; y++ {
					d0[y] = d0[y] * k
					d1[y] = d1[y] * k
				}
			}
		}
	}
}

func (s *wmaproStream) window() {
	for i := 0; i < s.channelsForCurSubframe; i++ {
		c := s.channelIndexesForCurSubframe[i]
		ch := &s.channel[c]
		winlen := ch.prevBlockLen
		start := ch.coeffs - (winlen >> 1)
		if s.subframeLen < winlen {
			start += (winlen - s.subframeLen) >> 1
			winlen = s.subframeLen
		}
		win := s.windows[avLog2(uint32(winlen))-blockMinBits]
		winlen >>= 1
		vectorFmulWindow(ch.out[:], start, start, start+winlen, win, winlen)
		ch.prevBlockLen = s.subframeLen
	}
}

func (s *wmaproStream) decodeSubframe() error {
	offset := s.samplesPerFrame
	subframeLen := s.samplesPerFrame
	totalSamples := s.samplesPerFrame * s.nbChannels
	transmitCoeffs := false

	s.subframeOffset = s.gb.count()

	for i := 0; i < s.nbChannels; i++ {
		s.channel[i].grouped = false
		if offset > s.channel[i].decodedSamples {
			offset = s.channel[i].decodedSamples
			subframeLen = s.channel[i].subframeLen[s.channel[i].curSubframe]
		}
	}

	s.channelsForCurSubframe = 0
	for i := 0; i < s.nbChannels; i++ {
		cur := s.channel[i].curSubframe
		totalSamples -= s.channel[i].decodedSamples
		if offset == s.channel[i].decodedSamples && subframeLen == s.channel[i].subframeLen[cur] {
			totalSamples -= s.channel[i].subframeLen[cur]
			s.channel[i].decodedSamples += s.channel[i].subframeLen[cur]
			s.channelIndexesForCurSubframe[s.channelsForCurSubframe] = i
			s.channelsForCurSubframe++
		}
	}
	if totalSamples == 0 {
		s.parsedAllSubframes = true
	}

	s.tableIdx = avLog2(uint32(s.samplesPerFrame / subframeLen))
	s.numBands = s.numSfb[s.tableIdx]
	s.curSfbOffsets = s.tableIdx

	offset += s.samplesPerFrame >> 1
	for i := 0; i < s.channelsForCurSubframe; i++ {
		s.channel[s.channelIndexesForCurSubframe[i]].coeffs = offset
	}

	s.subframeLen = subframeLen
	s.escLen = avLog2(uint32(s.subframeLen-1)) + 1

	// skip extended header if any
	if s.gb.bit() != 0 {
		numFillBits := int(s.gb.bits(2))
		if numFillBits == 0 {
			l := int(s.gb.bits(4))
			numFillBits = int(s.gb.bits(l)) + 1
		}
		if numFillBits >= 0 {
			if s.gb.count()+numFillBits > s.numSavedBits {
				return errInvalid // invalid number of fill bits
			}
			s.gb.skip(numFillBits)
		}
	}
	// reserved bit
	if s.gb.bit() != 0 {
		return errors.New("xma2: reserved bit set")
	}
	if err := s.decodeChannelTransform(); err != nil {
		return err
	}
	for i := 0; i < s.channelsForCurSubframe; i++ {
		c := s.channelIndexesForCurSubframe[i]
		s.channel[c].transmitCoefs = s.gb.bit() != 0
		if s.channel[c].transmitCoefs {
			transmitCoeffs = true
		}
	}

	if transmitCoeffs {
		quantStep := 90 * s.bitsPerSample >> 4
		s.transmitNumVecCoeffs = s.gb.bit() != 0
		if s.transmitNumVecCoeffs {
			numBits := avLog2(uint32((s.subframeLen+3)/4)) + 1
			for i := 0; i < s.channelsForCurSubframe; i++ {
				c := s.channelIndexesForCurSubframe[i]
				n := int(s.gb.bits(numBits)) << 2
				if n > s.subframeLen {
					return errInvalid // num_vec_coeffs too large
				}
				s.channel[c].numVecCoeffs = n
			}
		} else {
			for i := 0; i < s.channelsForCurSubframe; i++ {
				s.channel[s.channelIndexesForCurSubframe[i]].numVecCoeffs = s.subframeLen
			}
		}
		step := int(s.gb.sbits(6))
		quantStep += step
		if step == -32 || step == 31 {
			sign := 0
			if step != 31 {
				sign = -1
			}
			quant := 0
			for s.gb.count()+5 < s.numSavedBits {
				step = int(s.gb.bits(5))
				if step != 31 {
					break
				}
				quant += 31
			}
			quantStep += ((quant + step) ^ sign) - sign
		}

		if s.channelsForCurSubframe == 1 {
			s.channel[s.channelIndexesForCurSubframe[0]].quantStep = quantStep
		} else {
			modifierLen := int(s.gb.bits(3))
			for i := 0; i < s.channelsForCurSubframe; i++ {
				c := s.channelIndexesForCurSubframe[i]
				s.channel[c].quantStep = quantStep
				if s.gb.bit() != 0 {
					if modifierLen != 0 {
						s.channel[c].quantStep += int(s.gb.bits(modifierLen)) + 1
					} else {
						s.channel[c].quantStep++
					}
				}
			}
		}
		if err := s.decodeScaleFactors(); err != nil {
			return err
		}
	}

	// parse coefficients (FFmpeg ignores decode_coeffs' result here)
	for i := 0; i < s.channelsForCurSubframe; i++ {
		c := s.channelIndexesForCurSubframe[i]
		ch := &s.channel[c]
		if ch.transmitCoefs && s.gb.count() < s.numSavedBits {
			if err := s.decodeCoeffs(c); err != nil {
				s.errors++
			}
		} else {
			co := ch.out[ch.coeffs : ch.coeffs+subframeLen]
			for k := range co {
				co[k] = 0
			}
		}
	}

	if transmitCoeffs {
		tx := s.tx[avLog2(uint32(subframeLen))-blockMinBits]
		s.inverseChannelTransform()
		for i := 0; i < s.channelsForCurSubframe; i++ {
			c := s.channelIndexesForCurSubframe[i]
			ch := &s.channel[c]
			sf := ch.savedScaleFactors[ch.scaleFactors][:]
			sfb := s.sfbOffsets[s.curSfbOffsets][:]
			co := ch.out[ch.coeffs:]
			for b := 0; b < s.numBands; b++ {
				end := sfb[b+1]
				if end > s.subframeLen {
					end = s.subframeLen
				}
				exp := ch.quantStep - (ch.maxScaleFactor-sf[b])*ch.scaleFactorStep
				quant := float32(exp10(float64(exp) / 20.0))
				for k := sfb[b]; k < end; k++ {
					s.tmp[k] = co[k] * quant
				}
			}
			tx.inverse(co[:subframeLen], s.tmp[:subframeLen])
		}
	}

	s.window()

	for i := 0; i < s.channelsForCurSubframe; i++ {
		c := s.channelIndexesForCurSubframe[i]
		if s.channel[c].curSubframe >= s.channel[c].numSubframes {
			return errInvalid // broken subframe
		}
		s.channel[c].curSubframe++
	}
	return nil
}

// exp10 is ff_exp10 (libavutil/ffmath.h): exp2(M_LOG2_10 * x).
func exp10(x float64) float64 {
	const log2of10 = 3.32192809488736234787 // M_LOG2_10
	return math.Exp2(log2of10 * x)
}

// decodeFrame is decode_frame. It returns more_frames and whether a frame was produced into out.
func (s *wmaproStream) decodeFrame(out [][]float32) (moreFrames bool, got bool) {
	gb := &s.gb
	length := 0
	if s.lenPrefix {
		length = int(gb.bits(s.log2FrameSize))
	}
	if err := s.decodeTilehdr(); err != nil {
		s.packetLoss = true
		s.errors++
		return false, false
	}
	if s.nbChannels > 1 && gb.bit() != 0 {
		if gb.bit() != 0 {
			for i := 0; i < s.nbChannels*s.nbChannels; i++ {
				gb.skip(4)
			}
		}
	}
	if s.dynamicRangeCompression {
		s.drcGain = int(gb.bits(8))
	}
	if gb.bit() != 0 {
		if gb.bit() != 0 {
			s.trimStart = int(gb.bits(avLog2(uint32(s.samplesPerFrame * 2))))
		}
		if gb.bit() != 0 {
			s.trimEnd = int(gb.bits(avLog2(uint32(s.samplesPerFrame * 2))))
		}
	} else {
		s.trimStart, s.trimEnd = 0, 0
	}

	s.parsedAllSubframes = false
	for i := 0; i < s.nbChannels; i++ {
		s.channel[i].decodedSamples = 0
		s.channel[i].curSubframe = 0
		s.channel[i].reuseSF = false
	}
	for !s.parsedAllSubframes {
		if err := s.decodeSubframe(); err != nil {
			s.packetLoss = true
			s.errors++
			return false, false
		}
	}

	for i := 0; i < s.nbChannels; i++ {
		copy(out[i][:s.samplesPerFrame], s.channel[i].out[:s.samplesPerFrame])
	}
	for i := 0; i < s.nbChannels; i++ {
		// reuse second half of the IMDCT output for the next frame
		copy(s.channel[i].out[:s.samplesPerFrame>>1], s.channel[i].out[s.samplesPerFrame:s.samplesPerFrame+s.samplesPerFrame>>1])
	}

	if s.skipFrame {
		s.skipFrame = false
		got = false
	} else {
		got = true
	}

	if s.lenPrefix {
		if length != (gb.count()-s.frameOffset)+2 {
			s.packetLoss = true
			s.errors++
			return false, got
		}
		gb.skip(length - (gb.count() - s.frameOffset) - 1)
	} else {
		for gb.count() < s.numSavedBits && gb.bit() == 0 {
		}
	}
	moreFrames = gb.bit() != 0
	s.frameNum++
	return moreFrames, got
}

func (s *wmaproStream) remainingBits(g *bitReader) int { return s.bufBitSize - g.count() }

// saveBits is save_bits: fill the bit reservoir with a (partial) frame.
func (s *wmaproStream) saveBits(g *bitReader, length int, appendBits bool) {
	var buflen int
	if !appendBits {
		s.frameOffset = g.count() & 7
		s.numSavedBits = s.frameOffset
		s.pb.reset()
		buflen = (s.numSavedBits + length + 7) >> 3
	} else {
		buflen = (s.pb.pos + length + 7) >> 3
	}
	if length <= 0 || buflen > maxFramesize {
		s.packetLoss = true // "Too small input buffer"
		s.errors++
		return
	}
	s.numSavedBits += length
	if !appendBits {
		s.pb.copyBits(g.buf[g.count()>>3:], s.numSavedBits)
	} else {
		align := 8 - (g.count() & 7)
		if align > length {
			align = length
		}
		s.pb.put(align, g.bits(align))
		length -= align
		s.pb.copyBits(g.buf[g.count()>>3:], length)
	}
	g.skip(length)

	s.gb = newBitReader(s.frameData[:], s.numSavedBits)
	s.gb.skip(s.frameOffset)
}

// decodePacket is decode_packet for XMA. data is the remaining packet data (padded by the caller);
// size is its unpadded length (0 at end of stream). It returns the bytes consumed, whether a frame
// was written to out, and an error for a lost packet (as AVERROR_INVALIDDATA).
func (s *wmaproStream) decodePacket(data []byte, size int, out [][]float32) (int, bool, error) {
	gb := &s.pgb
	got := false
	bufSize := size

	if bufSize == 0 {
		s.packetDone = false
		if s.eofDone {
			return 0, false, nil
		}
		for i := 0; i < s.nbChannels; i++ {
			o := out[i][:s.samplesPerFrame]
			for k := range o {
				o[k] = 0
			}
			copy(o, s.channel[i].out[:s.samplesPerFrame>>1])
		}
		s.eofDone = true
		s.packetDone = true
		return 0, true, nil
	} else if s.packetDone || s.packetLoss {
		s.packetDone = false
		n := bufSize
		if n > xmaBlockAlign {
			n = xmaBlockAlign
		}
		s.nextPacketStart = bufSize - n
		bufSize = n
		s.bufBitSize = bufSize << 3

		*gb = newBitReader(data, bufSize*8)
		gb.bits(6) // number of frames (XMA2 packet header)
		numBitsPrevFrame := int(gb.bits(s.log2FrameSize))
		gb.skip(3)
		s.skipPackets = int(gb.bits(8))

		if numBitsPrevFrame > 0 {
			remainingPacketBits := s.bufBitSize - gb.count()
			if numBitsPrevFrame >= remainingPacketBits {
				numBitsPrevFrame = remainingPacketBits
				s.packetDone = true
			}
			s.saveBits(gb, numBitsPrevFrame, true)
			if !s.packetLoss {
				_, got = s.decodeFrame(out)
			}
		}
		if s.packetLoss {
			s.numSavedBits = 0
			s.packetLoss = false
		}
	} else {
		if size < s.nextPacketStart {
			s.packetLoss = true
			return 0, got, errInvalid
		}
		s.bufBitSize = (size - s.nextPacketStart) << 3
		*gb = newBitReader(data, s.bufBitSize)
		gb.skip(s.packetOffset)
		if s.lenPrefix && s.remainingBits(gb) > s.log2FrameSize {
			frameSize := int(gb.peek(s.log2FrameSize))
			if frameSize != 0 && frameSize <= s.remainingBits(gb) {
				s.saveBits(gb, frameSize, false)
				if !s.packetLoss {
					var more bool
					more, got = s.decodeFrame(out)
					s.packetDone = !more
				}
			} else {
				s.packetDone = true
			}
		} else {
			s.packetDone = true
		}
	}

	if s.remainingBits(gb) < 0 {
		s.packetLoss = true // overread
		s.errors++
	}
	if s.packetDone && !s.packetLoss && s.remainingBits(gb) > 0 {
		s.saveBits(gb, s.remainingBits(gb), false)
	}
	s.packetOffset = gb.count() & 7
	if s.packetLoss {
		return gb.count() >> 3, got, errInvalid
	}
	return gb.count() >> 3, got, nil
}
