package audio

import (
	"errors"
	"math"
	"math/cmplx"
)

// Report is the result of CompareWAV.
type Report struct {
	SameLength bool    // same number of samples in the data chunks
	GotLen     int     // samples in got
	WantLen    int     // samples in want
	Lag        int     // got[i] best matches want[i+Lag] (0 = aligned)
	Polarity   int     // +1 same polarity, -1 inverted (0 when either file is silent)
	SNRdB      float64 // 10 log10(sum want^2 / sum (pol*got - want)^2) over the overlap at Lag; +Inf if identical
	PeakDiff   int     // largest |pol*got - want| over the overlap at Lag, in LSB
	BodyPeak   int     // PeakDiff without the first EdgeSamples samples of want (see EdgeSamples)
	NoiseRMS   float64 // RMS of pol*got - want over the overlap at Lag without the first EdgeSamples of want,
	// in LSB (two independent TPDF dithers alone give sqrt(1/2) = 0.707)
}

// EdgeSamples is how many samples at the start of a file count as its edge. The recipes cut the decoded
// recording at a fixed sample (the decoder delay), so a sound that starts loud starts with a step, and the
// first output samples then show the resampler's step response: the original soxr filter and ours differ
// there by up to 59 LSB (SM15 fire_05, samples 0-1, measured), while every later sample agrees to
// the dither (BodyPeak <= 4).
const EdgeSamples = 8

// MaxCompareLag bounds the lag search of CompareWAV (samples each way).
const MaxCompareLag = 4096

// CompareWAV compares two mono PCM16 WAV files (any RIFF layout; extra chunks are ignored): finds the lag and
// polarity of the best cross-correlation within +-MaxCompareLag samples, then measures the SNR and peak
// difference at that alignment. Used by the golden tests of the console decoders.
func CompareWAV(got, want []byte) (Report, error) {
	g, err := ReadWAV(got)
	if err != nil {
		return Report{}, err
	}
	w, err := ReadWAV(want)
	if err != nil {
		return Report{}, err
	}
	if g.Channels != 1 || w.Channels != 1 {
		return Report{}, errors.New("audio: CompareWAV needs mono files")
	}
	return ComparePCM(g.Samples, w.Samples), nil
}

// ComparePCM is CompareWAV on sample slices.
func ComparePCM(got, want []int16) Report {
	r := Report{SameLength: len(got) == len(want), GotLen: len(got), WantLen: len(want)}
	a := toFloat(got)
	b := toFloat(want)
	lag, c := bestLag(a, b, MaxCompareLag)
	r.Lag = lag
	switch {
	case c > 0:
		r.Polarity = 1
	case c < 0:
		r.Polarity = -1
	}
	pol := float64(r.Polarity)
	if pol == 0 {
		pol = 1
	}
	var sig, noise float64
	peak, body, bodyNoise := 0.0, 0.0, 0.0
	cnt := 0
	for i := range a {
		j := i + lag
		if j < 0 || j >= len(b) {
			continue
		}
		d := pol*a[i] - b[j]
		sig += b[j] * b[j]
		noise += d * d
		peak = math.Max(peak, math.Abs(d))
		if j >= EdgeSamples {
			body = math.Max(body, math.Abs(d))
			bodyNoise += d * d
			cnt++
		}
	}
	switch {
	case noise == 0:
		r.SNRdB = math.Inf(1)
	case sig == 0:
		r.SNRdB = math.Inf(-1)
	default:
		r.SNRdB = 10 * math.Log10(sig/noise)
	}
	r.PeakDiff, r.BodyPeak = int(peak), int(body)
	if cnt > 0 {
		r.NoiseRMS = math.Sqrt(bodyNoise / float64(cnt))
	}
	return r
}

func toFloat(s []int16) []float64 {
	f := make([]float64, len(s))
	for i, v := range s {
		f[i] = float64(v)
	}
	return f
}

// bestLag returns the lag L (|L| <= maxLag) maximising |sum_i a[i] b[i+L]| and that correlation value.
// Ties go to the smallest |L| (then the negative one), so identical files report lag 0.
func bestLag(a, b []float64, maxLag int) (int, float64) {
	n := 1
	for n < len(a)+len(b)+1 {
		n <<= 1
	}
	fa := make([]complex128, n)
	fb := make([]complex128, n)
	for i, v := range a {
		fa[i] = complex(v, 0)
	}
	for i, v := range b {
		fb[i] = complex(v, 0)
	}
	fft(fa, false)
	fft(fb, false)
	for i := range fa {
		fa[i] = fb[i] * cmplx.Conj(fa[i])
	}
	fft(fa, true) // fa[k] = n * sum_i a[i] b[i+k] (circular)
	best, bestV := 0, 0.0
	for d := 0; d <= maxLag; d++ {
		for _, L := range []int{d, -d} {
			if d == 0 && L < 0 {
				continue
			}
			v := real(fa[(L+n)%n]) / float64(n)
			if math.Abs(v) > math.Abs(bestV)*(1+1e-12)+1e-9 {
				best, bestV = L, v
			}
		}
	}
	return best, bestV
}

// fft is an in-place radix-2 complex FFT (len(x) a power of two); inverse uses e^{+i} and no scaling.
func fft(x []complex128, inverse bool) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j |= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	sign := -1.0
	if inverse {
		sign = 1
	}
	for size := 2; size <= n; size <<= 1 {
		ang := sign * 2 * math.Pi / float64(size)
		wl := complex(math.Cos(ang), math.Sin(ang))
		half := size >> 1
		tw := make([]complex128, half)
		tw[0] = 1
		for k := 1; k < half; k++ {
			if k%64 == 0 { // refresh from the exact value to bound the error of the running product
				a := ang * float64(k)
				tw[k] = complex(math.Cos(a), math.Sin(a))
			} else {
				tw[k] = tw[k-1] * wl
			}
		}
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				t := tw[k] * x[start+k+half]
				u := x[start+k]
				x[start+k] = u + t
				x[start+k+half] = u - t
			}
		}
	}
}
