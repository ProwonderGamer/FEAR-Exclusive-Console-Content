package audio

import (
	"fmt"
	"math"
	"sync"
)

// Resampler design. The original conversion used soxr (HQ class: linear phase, about 20-bit precision, the
// transition band ending at the lower rate's Nyquist frequency). This is a linear-phase Kaiser-windowed sinc
// in polyphase form with the same corner choices:
//
//	passband edge   0.913 x the lower Nyquist (soxr's HQ passband end)
//	stopband edge   1.0   x the lower Nyquist (no aliasing into the output band)
//	attenuation     125 dB (above soxr HQ's ~120 dB; costs only taps)
//
// Measured against the shipped files the filter choice is invisible under the dither: every cutoff from 0.90
// to 1.0 gives the same SNR to 0.01 dB on the voices and gunshots (ATRAC3 at these bit rates carries little
// above 16 kHz). Output sample m sits at input time m * in / out with no extra delay (lag 0 against the shipped
// files). Samples outside the input count as zero.
const (
	rsPassband = 0.913
	rsStopband = 1.0
	rsAttenDB  = 125.0
)

type polyphase struct {
	l, m  int         // output/input rate ratio l/m in lowest terms
	half  int         // taps on each side of the centre, in input samples
	coefs [][]float64 // [phase][2*half] taps for input offsets -half+1 .. half
}

var (
	phaseCache   = map[[2]int]*polyphase{}
	phaseCacheMu sync.Mutex
)

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// kaiserBeta is Kaiser's empirical window parameter for a given stopband attenuation in dB.
func kaiserBeta(att float64) float64 {
	switch {
	case att > 50:
		return 0.1102 * (att - 8.7)
	case att >= 21:
		return 0.5842*math.Pow(att-21, 0.4) + 0.07886*(att-21)
	}
	return 0
}

// besselI0 is the modified Bessel function of the first kind, order 0 (power series).
func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	h := x / 2
	for k := 1; k < 500; k++ {
		term *= (h / float64(k)) * (h / float64(k))
		sum += term
		if term < sum*1e-17 {
			break
		}
	}
	return sum
}

func getPolyphase(in, out int) (*polyphase, error) {
	g := gcd(in, out)
	l, m := out/g, in/g
	if l > 8192 {
		return nil, fmt.Errorf("audio: resampling %d -> %d Hz needs %d filter phases (unsupported ratio)", in, out, l)
	}
	phaseCacheMu.Lock()
	defer phaseCacheMu.Unlock()
	if p, ok := phaseCache[[2]int{l, m}]; ok {
		return p, nil
	}
	// Frequencies as fractions of the INPUT Nyquist frequency.
	scale := math.Min(1, float64(l)/float64(m)) // lower Nyquist / input Nyquist
	pass, stop := rsPassband*scale, rsStopband*scale
	fc := (pass + stop) / 2                    // 6 dB point of the windowed sinc
	df := (stop - pass) / 2                    // transition width in cycles per input sample
	nTaps := (rsAttenDB - 7.95) / (14.36 * df) // Kaiser's length estimate
	half := int(math.Ceil(nTaps / 2))
	beta := kaiserBeta(rsAttenDB)
	i0b := besselI0(beta)
	p := &polyphase{l: l, m: m, half: half, coefs: make([][]float64, l)}
	for ph := 0; ph < l; ph++ {
		frac := float64(ph*m%l) / float64(l) // output time minus floor(output time), in input samples
		c := make([]float64, 2*half)
		sum := 0.0
		for j := range c {
			k := j - half + 1
			t := frac - float64(k)
			u := t / float64(half)
			if u <= -1 || u >= 1 {
				continue
			}
			w := besselI0(beta*math.Sqrt(1-u*u)) / i0b
			x := fc * t
			s := 1.0
			if x != 0 {
				s = math.Sin(math.Pi*x) / (math.Pi * x)
			}
			c[j] = fc * s * w
			sum += c[j]
		}
		for j := range c { // unity DC gain in every phase
			c[j] /= sum
		}
		p.coefs[ph] = c
	}
	phaseCache[[2]int{l, m}] = p
	return p, nil
}

// Resample converts x from rate in to rate out and returns exactly n output samples (see ResampledLength
// for the rule ToGameWAV uses). Equal rates copy the input (zero padded or cut to n).
func Resample(x []float64, in, out, n int) ([]float64, error) {
	if in <= 0 || out <= 0 {
		return nil, fmt.Errorf("audio: bad sample rates %d -> %d", in, out)
	}
	y := make([]float64, n)
	if in == out {
		copy(y, x)
		return y, nil
	}
	p, err := getPolyphase(in, out)
	if err != nil {
		return nil, err
	}
	h := p.half
	// Zero padding on both sides so every tap index is valid.
	xp := make([]float64, len(x)+4*h+2)
	off := 2*h + 1
	copy(xp[off:], x)
	for i := 0; i < n; i++ {
		num := int64(i) * int64(p.m)
		base := int(num / int64(p.l))
		ph := i % p.l
		c := p.coefs[ph]
		start := base - h + 1 + off
		if start < 0 || start+len(c) > len(xp) {
			continue // past the padded signal: silence
		}
		seg := xp[start : start+len(c)]
		acc := 0.0
		for j, v := range c {
			acc += v * seg[j]
		}
		y[i] = acc
	}
	return y, nil
}
