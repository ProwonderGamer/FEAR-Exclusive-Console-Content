package audio

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"fearinstaller/internal/catalog"
)

// Golden test thresholds:
//   - every file must have the shipped sample count, lag 0 and the same polarity;
//   - the difference must be dither noise only: the shipped files carry their own TPDF dither (error variance
//     1/4 LSB^2 with rounding) and so do ours, so two independent dithers give an RMS difference of
//     sqrt(1/2) = 0.707 LSB and peaks of 2 LSB, whatever the recording. Measured (58 files):
//     RMS 0.698-0.711 LSB, peak 2 LSB (3 on WASfire1);
//   - SNR therefore only tracks each recording's loudness: measured min 50.1 dB (B2031, the quietest line,
//     peak -23 dBFS), median 69.9, max 85.0 (the gunshots). minSNRdB sits 2 dB under the minimum.
const (
	minSNRdB    = 48.0
	maxNoiseRMS = 0.75
	maxPeakDiff = 4
	// maxEdgeDiff bounds the first EdgeSamples samples (the resampler's step response on sounds that start
	// loud; measured max 59 LSB on SM15 fire_05, about 0.2% of full scale and inaudible).
	maxEdgeDiff = 64
)

func refDirs(t *testing.T) (ps3, live string) {
	t.Helper()
	ps3, live = os.Getenv("FEAR_REF_PS3"), os.Getenv("FEAR_REF_LIVEMOD")
	if ps3 == "" || live == "" {
		t.Skip("golden test skipped: set FEAR_REF_PS3 (PS3 game dir with snd\\english.cat) and FEAR_REF_LIVEMOD (mod folder with the shipped WAVs) to run it")
	}
	// a partial reference set (no sound catalogs) skips, as the other golden tests do
	for _, c := range []string{ps3VoiceCatalog, ps3SfxCatalog} {
		if _, err := os.Stat(filepath.Join(ps3, filepath.FromSlash(c))); err != nil {
			t.Skipf("golden test skipped: %s not in FEAR_REF_PS3", c)
		}
	}
	return ps3, live
}

func TestGoldenPS3(t *testing.T) {
	ps3, live := refDirs(t)
	got, err := BuildPS3(ps3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(PS3Targets()) {
		t.Fatalf("built %d files, want %d", len(got), len(PS3Targets()))
	}
	var snrs []float64
	t.Logf("%-34s %-46s %8s %4s %3s %8s %6s %5s %5s", "live path", "PS3 entry", "samples", "lag", "pol", "SNR dB", "rms", "peak", "edge")
	for _, tg := range PS3Targets() {
		want, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(tg.LivePath)))
		if err != nil {
			t.Fatal(err)
		}
		b := got[tg.LivePath]
		if !bytes.Equal(b[:4], []byte("RIFF")) || len(b) < 44 {
			t.Fatalf("%s: bad WAV", tg.LivePath)
		}
		r, err := CompareWAV(b, want)
		if err != nil {
			t.Fatalf("%s: %v", tg.LivePath, err)
		}
		entry := tg.Entry
		if tg.Part > 0 {
			entry += " part " + string(rune('0'+tg.Part))
		}
		t.Logf("%-34s %-46s %8d %4d %+3d %8.2f %6.3f %5d %5d", tg.LivePath, entry, r.GotLen, r.Lag, r.Polarity, r.SNRdB, r.NoiseRMS, r.BodyPeak, r.PeakDiff)
		if !r.SameLength || r.Lag != 0 || r.Polarity != 1 || !(r.SNRdB >= minSNRdB) || r.NoiseRMS > maxNoiseRMS || r.BodyPeak > maxPeakDiff || r.PeakDiff > maxEdgeDiff {
			t.Errorf("%s: length %d vs %d, lag %d, polarity %d, SNR %.2f dB, rms %.3f, peak %d (edge %d) (want same length, lag 0, polarity +1, SNR >= %.0f, rms <= %.2f, peak <= %d, edge <= %d)",
				tg.LivePath, r.GotLen, r.WantLen, r.Lag, r.Polarity, r.SNRdB, r.NoiseRMS, r.BodyPeak, r.PeakDiff, minSNRdB, maxNoiseRMS, maxPeakDiff, maxEdgeDiff)
		}
		snrs = append(snrs, r.SNRdB)
	}
	sort.Float64s(snrs)
	t.Logf("SNR over %d files: min %.2f dB, median %.2f dB, max %.2f dB", len(snrs), snrs[0], snrs[len(snrs)/2], snrs[len(snrs)-1])
}

// TestGoldenWatsonCutRule checks that the reload split's cut rule, run on the rebuilt reload recording, still
// lands on the cut points the fix uses.
func TestGoldenWatsonCutRule(t *testing.T) {
	ps3, _ := refDirs(t)
	c, err := catalog.Open(filepath.Join(ps3, "snd", "sfx.cat"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, err := c.ReadByName(ps3ReloadEntry)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ConvertPS3MSF(b, "guns/snd/shotgun/wasreload.wav")
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != watsonReloadLen {
		t.Fatalf("reload recording has %d samples, want %d", len(s), watsonReloadLen)
	}
	for i, sr := range WatsonReloadSearch {
		cut, err := QuietestCut(s, sr.T0, sr.T1, sr.LimitDB)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("cut %d: rule gives %d (%.4f s), fix uses %d", i+1, cut, float64(cut)/OutputRate, WatsonReloadCuts[i])
		if cut != WatsonReloadCuts[i] {
			t.Errorf("cut %d: rule on the rebuilt recording gives %d, the split uses %d", i+1, cut, WatsonReloadCuts[i])
		}
	}
}

// TestDeterminismPS3 builds twice and requires identical bytes.
func TestDeterminismPS3(t *testing.T) {
	ps3, _ := refDirs(t)
	a, err := BuildPS3(ps3)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildPS3(ps3)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range a {
		if !bytes.Equal(v, b[k]) {
			t.Errorf("%s differs between two builds", k)
		}
	}
	if len(a) != len(b) {
		t.Errorf("builds made %d and %d files", len(a), len(b))
	}
}

// TestDeterminismSynthetic needs no reference data: same input and seed give the same bytes, another seed
// changes only the dither.
func TestDeterminismSynthetic(t *testing.T) {
	pcm := make([]float32, 48000)
	for i := range pcm {
		pcm[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	o := Options{DropSamples: 100, Invert: true, Seed: 7}
	a, err := ToGameWAV(pcm, 48000, o)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ToGameWAV(pcm, 48000, o)
	if !bytes.Equal(a, b) {
		t.Fatal("same input and seed gave different bytes")
	}
	o.Seed = 8
	c, _ := ToGameWAV(pcm, 48000, o)
	if bytes.Equal(a, c) {
		t.Fatal("a different seed gave identical dither")
	}
	r, err := CompareWAV(c, a)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lag != 0 || r.Polarity != 1 || r.SNRdB < 75 || r.PeakDiff > 2 {
		t.Fatalf("seed change should only move the dither: %+v", r)
	}
}
