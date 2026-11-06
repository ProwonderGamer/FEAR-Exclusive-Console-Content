// Package audio turns decoded console sound (PS3 ATRAC3, Xbox 360 XMA2) into the 44.1 kHz mono PCM16 WAV
// files the PC game plays, and compares rebuilt files with reference copies.
//
// The output steps are shared by every console decoder (ToGameWAV): drop the decoder delay, set polarity and
// gain, resample to 44100 Hz, add seeded TPDF dither, round and clip to 16 bits, and write a WAV with the
// canonical 44-byte header. They reproduce the project's original conversion recipe
// ("decode at 48 kHz, drop 1162-sample delay, invert polarity, soxr to 44.1 kHz, dither 16-bit"), with the
// details the recipe left open measured on the shipped files (see the comments at each step):
//
//   - the decoded samples stay float until the final 16-bit step (no 16-bit stage at the decoder rate):
//     WASfire1 matches only when its +4.7 dBFS peaks are clipped after resampling, not before;
//   - output length = round(n * 44100 / rate) samples (n = samples after the delay drop), all 55 checked files;
//   - dither = triangular (TPDF) noise of +-1 LSB peak, then round to nearest: the residual against the
//     shipped files is white with variance 0.25 LSB^2 = 1/6 (TPDF) + 1/12 (rounding).
//
// Byte equality with the shipped files is impossible (their dither noise is random); CompareWAV measures the
// match instead.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// OutputRate is the sample rate of every game WAV this package writes.
const OutputRate = 44100

// Options controls ToGameWAV.
type Options struct {
	// DropSamples is the number of leading input samples to discard (decoder delay; 1162 for the PS3 ATRAC3
	// files, measured as the cross-correlation lag against the shipped files).
	DropSamples int
	// Invert flips the polarity (the PS3 recipe inverts).
	Invert bool
	// Seed seeds the dither generator. The same input, options and seed always give the same bytes.
	Seed uint64
	// Gain multiplies the samples before resampling; 0 means 1.0 (unity, the PS3 recipe).
	Gain float64
	// Length forces the output sample count; 0 means round(n * 44100 / rate).
	Length int
	// NoDither rounds without dither (tests and diagnostics only; the shipped files are dithered).
	NoDither bool
}

// ToGameWAV converts decoded mono float samples (full scale +-1.0) at the given rate into the game's WAV
// format: 44100 Hz, mono, PCM16, 44-byte header.
func ToGameWAV(pcm []float32, rate int, opts Options) ([]byte, error) {
	s, err := ToPCM16(pcm, rate, opts)
	if err != nil {
		return nil, err
	}
	return WriteWAV(s, OutputRate), nil
}

// ToPCM16 runs the ToGameWAV steps and returns the 16-bit samples without the WAV header.
func ToPCM16(pcm []float32, rate int, opts Options) ([]int16, error) {
	if rate <= 0 || rate > 384000 {
		return nil, fmt.Errorf("audio: implausible sample rate %d", rate)
	}
	if opts.DropSamples < 0 {
		return nil, errors.New("audio: negative DropSamples")
	}
	if opts.DropSamples > len(pcm) {
		return nil, fmt.Errorf("audio: DropSamples %d exceeds the %d decoded samples", opts.DropSamples, len(pcm))
	}
	in := pcm[opts.DropSamples:]
	g := opts.Gain
	if g == 0 {
		g = 1
	}
	if opts.Invert {
		g = -g
	}
	x := make([]float64, len(in))
	for i, v := range in {
		x[i] = float64(v) * g
	}
	n := opts.Length
	if n == 0 {
		n = ResampledLength(len(in), rate, OutputRate)
	}
	if n < 0 {
		return nil, errors.New("audio: negative Length")
	}
	y, err := Resample(x, rate, OutputRate, n)
	if err != nil {
		return nil, err
	}
	return Quantize(y, opts.Seed, !opts.NoDither), nil
}

// ResampledLength is the output length rule measured on the shipped files: round(n * out / in), halves up.
func ResampledLength(n, in, out int) int {
	if in == out {
		return n
	}
	num := int64(n) * int64(out)
	return int((2*num + int64(in)) / (2 * int64(in)))
}

// Quantize scales full-scale floats to 16 bits: adds TPDF dither (two uniform [0,1) draws subtracted, +-1 LSB
// peak) when dither is set, rounds to nearest (halves up) and clips to [-32768, 32767].
func Quantize(y []float64, seed uint64, dither bool) []int16 {
	rng := splitMix64{state: seed}
	out := make([]int16, len(y))
	for i, v := range y {
		v *= 32768
		if dither {
			v += rng.float64() - rng.float64()
		}
		r := math.Floor(v + 0.5)
		switch {
		case r > 32767:
			r = 32767
		case r < -32768:
			r = -32768
		case r != r: // NaN from damaged input: write silence
			r = 0
		}
		out[i] = int16(r)
	}
	return out
}

// splitMix64 is a small, fixed, platform-independent generator (Steele, Lea, Flood 2014), so the dither is
// reproducible byte for byte on every build.
type splitMix64 struct{ state uint64 }

func (s *splitMix64) next() uint64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// float64 returns a uniform value in [0, 1) with 53 random bits.
func (s *splitMix64) float64() float64 { return float64(s.next()>>11) * (1.0 / (1 << 53)) }

// SeedFor derives a dither seed from a name (FNV-1a 64 of the bytes), so every file gets its own noise.
func SeedFor(name string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(name); i++ {
		h ^= uint64(name[i])
		h *= 1099511628211
	}
	return h
}

// WriteWAV writes mono PCM16 samples with the canonical 44-byte header (RIFF, fmt (16 bytes), data).
func WriteWAV(s []int16, rate int) []byte {
	b := make([]byte, 44+2*len(s))
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(s)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], 1) // mono
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(s)))
	for i, v := range s {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return b
}

// WAVInfo is a parsed PCM16 WAV file.
type WAVInfo struct {
	Rate     int
	Channels int
	Samples  []int16 // interleaved when Channels > 1
}

// ReadWAV parses a PCM16 WAV by walking its RIFF chunks; only the fmt and data chunks are used, so trailing
// chunks (the shipped files carry a 5758-byte "C2PA" chunk after the data) are ignored.
func ReadWAV(b []byte) (*WAVInfo, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, errors.New("audio: not a RIFF WAVE file")
	}
	w := &WAVInfo{}
	var haveFmt, haveData bool
	for p := 12; p+8 <= len(b); {
		id := string(b[p : p+4])
		sz := int(binary.LittleEndian.Uint32(b[p+4:]))
		body := p + 8
		if sz < 0 || body+sz > len(b) {
			return nil, fmt.Errorf("audio: chunk %q runs past the end of the file", id)
		}
		switch id {
		case "fmt ":
			if sz < 16 {
				return nil, errors.New("audio: short fmt chunk")
			}
			if tag := binary.LittleEndian.Uint16(b[body:]); tag != 1 {
				return nil, fmt.Errorf("audio: WAV format tag %d is not PCM", tag)
			}
			w.Channels = int(binary.LittleEndian.Uint16(b[body+2:]))
			w.Rate = int(binary.LittleEndian.Uint32(b[body+4:]))
			if bits := binary.LittleEndian.Uint16(b[body+14:]); bits != 16 {
				return nil, fmt.Errorf("audio: %d-bit WAV, want 16-bit", bits)
			}
			haveFmt = true
		case "data":
			w.Samples = make([]int16, sz/2)
			for i := range w.Samples {
				w.Samples[i] = int16(binary.LittleEndian.Uint16(b[body+2*i:]))
			}
			haveData = true
		}
		p = body + sz + sz&1
	}
	if !haveFmt || !haveData {
		return nil, errors.New("audio: WAV without fmt or data chunk")
	}
	return w, nil
}
