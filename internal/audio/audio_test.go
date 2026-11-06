package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func sine(n, rate int, freq, amp, phase float64) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)+phase)
	}
	return x
}

// A sine resampled 48000 -> 44100 must come out with the same amplitude and phase at the output times
// (linear phase, no delay), away from the edges.
func TestResampleSineAccuracy(t *testing.T) {
	for _, f := range []float64{100, 1000, 7000, 15000, 19000} {
		x := sine(48000, 48000, f, 0.5, 0.3)
		n := ResampledLength(len(x), 48000, 44100)
		y, err := Resample(x, 48000, 44100, n)
		if err != nil {
			t.Fatal(err)
		}
		want := sine(n, 44100, f, 0.5, 0.3)
		maxErr := 0.0
		for i := 2000; i < n-2000; i++ {
			maxErr = math.Max(maxErr, math.Abs(y[i]-want[i]))
		}
		if maxErr > 1e-5 { // -100 dB re 0.5 full scale, well under 1 LSB
			t.Errorf("%g Hz: max error %g", f, maxErr)
		}
	}
}

// Frequencies above the output Nyquist frequency must be removed, not aliased.
func TestResampleStopband(t *testing.T) {
	x := sine(48000, 48000, 23000, 0.9, 0)
	n := ResampledLength(len(x), 48000, 44100)
	y, _ := Resample(x, 48000, 44100, n)
	peak := 0.0
	for i := 2000; i < n-2000; i++ {
		peak = math.Max(peak, math.Abs(y[i]))
	}
	if db := 20 * math.Log10(peak/0.9); db > -110 {
		t.Errorf("23 kHz leaks at %.1f dB", db)
	}
}

func TestResampleOtherRatesAndDC(t *testing.T) {
	for _, in := range []int{22050, 24000, 32000, 48000} {
		x := make([]float64, 20000)
		for i := range x {
			x[i] = 0.25
		}
		n := ResampledLength(len(x), in, 44100)
		y, err := Resample(x, in, 44100, n)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(y[n/2]-0.25) > 1e-12 {
			t.Errorf("%d Hz: DC gain %g", in, y[n/2]/0.25)
		}
	}
	y, _ := Resample([]float64{1, 2, 3}, 44100, 44100, 4)
	if y[0] != 1 || y[2] != 3 || y[3] != 0 {
		t.Errorf("equal rates should copy: %v", y)
	}
}

func TestResampledLength(t *testing.T) {
	// Values measured on the shipped files: (frames*1024 - 1162) samples at 48 kHz -> data chunk length.
	for _, c := range [][2]int{{308086, 283054}, {257910, 236955}, {27510, 25275}, {155510, 142875}} {
		if got := ResampledLength(c[0], 48000, 44100); got != c[1] {
			t.Errorf("ResampledLength(%d) = %d, want %d", c[0], got, c[1])
		}
	}
	if ResampledLength(123, 44100, 44100) != 123 {
		t.Error("equal rates must keep the length")
	}
}

func TestQuantizeDitherAndClip(t *testing.T) {
	y := make([]float64, 100000)
	q := Quantize(y, 1, true)
	var sum, sq float64
	for _, v := range q {
		if v < -1 || v > 1 {
			t.Fatalf("TPDF dither of +-1 LSB produced %d", v)
		}
		sum += float64(v)
		sq += float64(v) * float64(v)
	}
	// Rounded TPDF on a zero signal: P(+-1) = 1/8 each, variance 1/4.
	if v := sq / float64(len(q)); math.Abs(v-0.25) > 0.01 || math.Abs(sum/float64(len(q))) > 0.01 {
		t.Errorf("dither variance %g mean %g", v, sum/float64(len(q)))
	}
	q = Quantize([]float64{1.2, -1.2, 0.5, math.NaN(), 32767.6 / 32768}, 0, false)
	if q[0] != 32767 || q[1] != -32768 || q[2] != 16384 || q[3] != 0 || q[4] != 32767 {
		t.Errorf("clip/round: %v", q)
	}
}

func TestToGameWAVOptions(t *testing.T) {
	pcm := make([]float32, 4800)
	pcm[1000] = 0.5
	o := Options{DropSamples: 1000, Invert: true, NoDither: true}
	b, err := ToGameWAV(pcm, 48000, o)
	if err != nil {
		t.Fatal(err)
	}
	w, err := ReadWAV(b)
	if err != nil {
		t.Fatal(err)
	}
	if w.Rate != 44100 || w.Channels != 1 || len(w.Samples) != ResampledLength(3800, 48000, 44100) {
		t.Fatalf("wav: rate %d ch %d len %d", w.Rate, w.Channels, len(w.Samples))
	}
	if w.Samples[0] >= 0 {
		t.Errorf("first sample %d: the impulse at the drop point should land inverted at sample 0", w.Samples[0])
	}
	o.Gain = 2
	b2, _ := ToGameWAV(pcm, 48000, o)
	w2, _ := ReadWAV(b2)
	if d := int(w2.Samples[0]) - 2*int(w.Samples[0]); d < -1 || d > 1 {
		t.Errorf("gain 2: %d vs %d", w2.Samples[0], w.Samples[0])
	}
	if _, err := ToGameWAV(pcm, 48000, Options{DropSamples: 5000}); err == nil {
		t.Error("DropSamples beyond the input accepted")
	}
}

func TestWAVRoundTripAndChunks(t *testing.T) {
	s := []int16{0, 1, -1, 32767, -32768}
	b := WriteWAV(s, 44100)
	if len(b) != 44+10 || binary.LittleEndian.Uint32(b[40:]) != 10 || binary.LittleEndian.Uint32(b[4:]) != 46 {
		t.Fatalf("header: % x", b[:44])
	}
	// A trailing extra chunk (as the shipped files' C2PA chunk) must be ignored.
	ext := append(append([]byte(nil), b...), []byte("C2PA\x04\x00\x00\x00abcd")...)
	w, err := ReadWAV(ext)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Samples) != len(s) || w.Samples[3] != 32767 || w.Samples[4] != -32768 {
		t.Fatalf("samples: %v", w.Samples)
	}
	if _, err := ReadWAV(b[:30]); err == nil {
		t.Error("truncated WAV accepted")
	}
}

func TestCompareWAV(t *testing.T) {
	x := sine(30000, 44100, 440, 0.3, 0)
	a := Quantize(x, 1, true)
	b := Quantize(x, 2, true)
	r := ComparePCM(a, b)
	if !r.SameLength || r.Lag != 0 || r.Polarity != 1 || r.PeakDiff > 2 || math.Abs(r.NoiseRMS-math.Sqrt(0.5)) > 0.02 {
		t.Errorf("same signal, other dither: %+v", r)
	}
	// Shift and invert: got[i] = -want[i+5].
	inv := make([]int16, len(b)-5)
	for i := range inv {
		inv[i] = -b[i+5]
	}
	r = ComparePCM(inv, b)
	if r.SameLength || r.Lag != 5 || r.Polarity != -1 || !math.IsInf(r.SNRdB, 1) {
		t.Errorf("shifted, inverted: %+v", r)
	}
	r, err := CompareWAV(WriteWAV(a, 44100), WriteWAV(a, 44100))
	if err != nil || r.Lag != 0 || r.Polarity != 1 || !math.IsInf(r.SNRdB, 1) || r.PeakDiff != 0 {
		t.Errorf("identical: %+v %v", r, err)
	}
}

func TestSplitWithFades(t *testing.T) {
	pcm := make([]int16, 2000)
	for i := range pcm {
		pcm[i] = 1001
	}
	parts, err := SplitWithFades(pcm, []int{700, 1500}, 220)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts[0]) != 700 || len(parts[1]) != 800 || len(parts[2]) != 500 {
		t.Fatalf("lengths %d %d %d", len(parts[0]), len(parts[1]), len(parts[2]))
	}
	// First part: no fade in, fades out to 0 at its last sample.
	if parts[0][0] != 1001 || parts[0][699] != 0 || parts[0][699-220] != 1001 {
		t.Errorf("part 1 fade: %d %d %d", parts[0][0], parts[0][699], parts[0][479])
	}
	// Middle part: fade in from 0; 1001 * 110/220 = 500.5 rounds half to even -> 500.
	if parts[1][0] != 0 || parts[1][110] != 500 || parts[1][799] != 0 {
		t.Errorf("part 2 fade: %d %d %d", parts[1][0], parts[1][110], parts[1][799])
	}
	if parts[2][499] != 1001 || parts[2][0] != 0 {
		t.Errorf("part 3 fade: %d %d", parts[2][0], parts[2][499])
	}
	if _, err := SplitWithFades(pcm, []int{900, 800}, 220); err == nil {
		t.Error("descending cuts accepted")
	}
	if _, err := SplitWatsonReload(pcm); err == nil {
		t.Error("wrong-length reload accepted")
	}
}

func TestQuietestCut(t *testing.T) {
	// Loud noise with a quiet gap: the cut must land in the middle of the quietest 10 ms window.
	pcm := make([]int16, 44100)
	r := splitMix64{state: 3}
	for i := range pcm {
		pcm[i] = int16(r.next()>>52) - 2048
	}
	for i := 20000; i < 20600; i++ {
		pcm[i] = 0
	}
	cut, err := QuietestCut(pcm, 0.40, 0.50, -60)
	if err != nil {
		t.Fatal(err)
	}
	// First silent 441-sample window on the 44-sample grid from 17640: start 20016, middle 20236.
	if cut != 20236 {
		t.Errorf("cut %d, want 20236", cut)
	}
	if _, err := QuietestCut(pcm, 0.10, 0.20, -60); err == nil {
		t.Error("a range with no quiet window passed the limit")
	}
}

func TestPS3TargetsList(t *testing.T) {
	tg := PS3Targets()
	if len(tg) != 77 {
		t.Fatalf("%d targets, want 77", len(tg))
	}
	seen := map[string]bool{}
	for _, x := range tg {
		if seen[x.LivePath] {
			t.Errorf("duplicate %s", x.LivePath)
		}
		seen[x.LivePath] = true
	}
	if n := PS3Needs(); len(n) != 77 || n["Voice/B2001r.wav"] != ConsolePS3 || n["guns/Snd/Shotgun/WASreload3.WAV"] != ConsolePS3 {
		t.Errorf("needs: %d entries", len(n))
	}
	if !bytes.Contains([]byte(tg[57].Entry), []byte("Shotgun_WASreload")) || tg[57].Part != 3 {
		t.Errorf("last Watson target %+v", tg[57])
	}
	// The SM15 sounds: 19 PS3 files; only the three fire loops are cut short (sm15LoopLength).
	cut := 0
	for _, x := range tg[58:] {
		if !bytes.HasPrefix([]byte(x.LivePath), []byte("guns/Snd/SM15/")) || x.Part != 0 {
			t.Errorf("SM15 target %+v", x)
		}
		if x.Length != 0 {
			cut++
		}
	}
	if cut != 3 {
		t.Errorf("%d SM15 targets with a fixed length, want the 3 fire loops", cut)
	}
}
