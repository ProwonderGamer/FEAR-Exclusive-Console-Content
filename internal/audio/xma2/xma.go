// Derived from FFmpeg 7.1 (tag n7.1): libavcodec/wmaprodec.c (xma_decode_packet, xma_decode_init) and
// libavcodec/decode.c (decoder-side skip_samples and packet consumption).
//
// Copyright of the FFmpeg files this file is derived from, as they state it:
//
//	libavcodec/wmaprodec.c: Copyright (c) 2007 Baptiste Coudurier, Benjamin Larsson, Ulion
//	                        Copyright (c) 2008 - 2011 Sascha Sommer, Benjamin Larsson
//	libavcodec/decode.c:    no copyright line of its own ("generic decoding-related code")
//
// MODIFIED VERSION: this file was translated from C to Go and changed for the F.E.A.R. console content
// installer in 2026-11 (the packet loop of a single XMA stream, without FFmpeg's decoder framework).
//
// This file is part of a derivative of FFmpeg. FFmpeg is free software; you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by the Free Software Foundation;
// either version 2.1 of the License, or (at your option) any later version (LGPL-2.1-or-later). FFmpeg is
// distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty
// of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Lesser General Public License for more
// details, LICENSE-FFMPEG.md in this folder, and licenses/FFmpeg-LICENSE.txt.

// Package xma2 decodes Xbox 360 XMA (XMA1/XMA2 bitstream, 2048-byte packets) to float samples. It is a
// Go port of FFmpeg 7.1's float "xma2" decoder (libavcodec/wmaprodec.c) for a single XMA stream of one
// or two channels, which covers every wave in the F.E.A.R. Xbox 360 banks (48 kHz mono).
//
// The output reproduces what FFmpeg's xma2 decoder hands out, sample for sample: the first decoded
// frame (512 samples) is not output, 64 more samples are skipped at the start (the decoder sets
// skip_samples = 64), a last half frame is flushed at end of stream, and the end is cut by
// trim_end + trim_start - 192 when the bitstream carries trim values.
package xma2

import (
	"errors"
	"fmt"

	"fearinstaller/internal/audio/xwb"
)

// Result is a decoded stream.
type Result struct {
	Samples    []float32 // full scale +-1.0; interleaved when Channels == 2
	Channels   int
	SampleRate int
	Frames     int // XMA frames decoded (512 samples each)
	Errors     int // bitstream errors FFmpeg would have logged and skipped
}

// Decode decodes raw XMA data (a whole number of 2048-byte packets, as stored in a wave bank) of one
// stream with the given channel count (1 or 2) and sample rate. It returns an error if the
// bitstream had any decoding error, so damaged data is never accepted silently.
func Decode(data []byte, channels, sampleRate int) (*Result, error) {
	r, err := DecodeLenient(data, channels, sampleRate)
	if err != nil {
		return nil, err
	}
	if r.Errors > 0 {
		return nil, fmt.Errorf("xma2: %d bitstream errors", r.Errors)
	}
	return r, nil
}

// DecodeEntry decodes entry i of a wave bank.
func DecodeEntry(b *xwb.Bank, i int) (*Result, error) {
	if i < 0 || i >= len(b.Entries) {
		return nil, fmt.Errorf("xma2: entry %d out of range", i)
	}
	e := b.Entries[i]
	if e.Codec != xwb.CodecXMA {
		return nil, fmt.Errorf("xma2: entry %d is %v, not XMA", i, e.Codec)
	}
	data, err := b.Data(i)
	if err != nil {
		return nil, err
	}
	return Decode(data, e.Channels, e.SampleRate)
}

// DecodeLenient is Decode that, like FFmpeg, logs nothing and carries on after bitstream errors,
// reporting their count in Result.Errors.
func DecodeLenient(data []byte, channels, sampleRate int) (*Result, error) {
	if channels < 1 || channels > xmaMaxChanStream {
		return nil, fmt.Errorf("xma2: %d channels: only single-stream XMA (1 or 2 channels) is supported", channels)
	}
	st, err := newStream(sampleRate, channels)
	if err != nil {
		return nil, err
	}
	x := &xmaCtx{st: st, skipSamples: 64}
	for c := 0; c < channels; c++ {
		x.frame[c] = make([]float32, xmaSamplesPerFrm)
	}

	pkt := make([]byte, xmaBlockAlign+inputPaddingBytes)
	for p := 0; p < len(data); p += xmaBlockAlign {
		n := copy(pkt, data[p:min(p+xmaBlockAlign, len(data))])
		for k := n; k < len(pkt); k++ {
			pkt[k] = 0
		}
		// decode.c: call the decoder until it has consumed the packet (or fails on it).
		pos := 0
		for guard := 0; pos < n; guard++ {
			if guard > 4*xmaBlockAlign {
				return nil, errors.New("xma2: decoder made no progress")
			}
			consumed, err := x.decode(pkt[pos:], n-pos)
			if err != nil {
				st.errors++
				break // the packet is dropped
			}
			pos += consumed
		}
	}
	// drain: empty packets until no more output
	for i := 0; i < 2; i++ {
		if _, err := x.decode(nil, 0); err != nil {
			st.errors++
		}
	}
	res := &Result{Channels: channels, SampleRate: sampleRate, Frames: int(st.frameNum), Errors: st.errors}
	if channels == 1 {
		res.Samples = x.out[0]
	} else {
		res.Samples = make([]float32, 2*len(x.out[0]))
		for i := range x.out[0] {
			res.Samples[2*i] = x.out[0][i]
			res.Samples[2*i+1] = x.out[1][i]
		}
	}
	return res, nil
}

// xmaCtx is XMADecodeCtx for one stream, plus the generic decoder's skip_samples and the output.
type xmaCtx struct {
	st                 *wmaproStream
	frame              [2][]float32
	fifo               [2][]float32
	trimStart, trimEnd int
	flushed            bool
	skipSamples        int
	out                [2][]float32
}

// decode is xma_decode_packet for a single stream. size 0 means end of stream.
func (x *xmaCtx) decode(data []byte, size int) (int, error) {
	st := x.st
	got := false
	ret := 0
	var err error
	if !st.eofDone {
		ret, got, err = st.decodePacket(data, size, x.frame[:st.nbChannels])
	}
	eof := false
	if size == 0 {
		eof = st.eofDone
	}
	if st.trimStart != 0 {
		x.trimStart = st.trimStart
	}
	if st.trimEnd != 0 {
		x.trimEnd = st.trimEnd
	}
	if got {
		for c := 0; c < st.nbChannels; c++ {
			x.fifo[c] = append(x.fifo[c], x.frame[c]...)
		}
	} else if err != nil {
		return ret, err
	}

	if st.packetDone || st.packetLoss {
		if st.skipPackets > 0 {
			st.skipPackets--
		}
		nb := len(x.fifo[0])
		if !eof && size != 0 {
			nb -= min(nb, 4096)
		}
		if (nb > 0 || eof || size == 0) && !x.flushed {
			if eof {
				nb -= clip(x.trimEnd+x.trimStart-128-64, 0, nb)
				x.flushed = true
			}
			x.emit(nb)
		}
	}
	return ret, err
}

// emit moves nb samples per channel from the FIFO to the output, applying skip_samples.
func (x *xmaCtx) emit(nb int) {
	if nb <= 0 {
		return
	}
	skip := min(x.skipSamples, nb)
	x.skipSamples -= skip
	for c := 0; c < x.st.nbChannels; c++ {
		x.out[c] = append(x.out[c], x.fifo[c][skip:nb]...)
		x.fifo[c] = x.fifo[c][nb:]
	}
}

func clip(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
