// Original code of this project (not derived from FFmpeg).

package xma2_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"fearinstaller/internal/audio"
	"fearinstaller/internal/audio/xma2"
	"fearinstaller/internal/audio/xwb"
	"fearinstaller/internal/catalog"
)

// Golden thresholds for the X360-sourced files (Source "x360", the 28 Holiday voice lines), measured
// against the shipped WAVs: same sample count, lag 0, same polarity for all 28. The shipped
// files were resampled with a different resampler (soxr) and carry their own dither, so the SNR tracks
// the recording's loudness and how much of it was clipped (decoded peaks up to 1.23 are hard-clipped in both).
// Measured with audio.ToGameWAV: min 63.79 dB (B1030r), median 71.87, max 80.00 (B1006); files without
// clipping sit at the two-dither floor (RMS difference 0.75-0.79 LSB, peak 3-7 LSB). minX360SNRdB sits under
// the minimum.
const minX360SNRdB = 60.0

type x360Ref struct {
	game  string
	banks map[string]*xwb.Bank
	sbs   map[string]*xwb.SoundBank
}

var (
	refOnce sync.Once
	ref     *x360Ref
	refErr  error
)

func x360(t *testing.T) *x360Ref {
	t.Helper()
	dir := os.Getenv("FEAR_REF_X360")
	if dir == "" {
		t.Skip("golden test skipped: set FEAR_REF_X360 (Xbox 360 copy holding Game\\Snd\\*.xwb and Game\\uberCatalog.cat) to run it")
	}
	// a partial reference set (no catalog or wave banks) skips, as the other golden tests do
	g := dir
	if fi, err := os.Stat(filepath.Join(dir, "Game")); err == nil && fi.IsDir() {
		g = filepath.Join(dir, "Game")
	}
	for _, f := range []string{xma2.UberCatalog, xma2.VoiceBank, xma2.SFXBank} {
		if _, err := os.Stat(filepath.Join(g, filepath.FromSlash(f))); err != nil {
			t.Skipf("golden test skipped: %s not in FEAR_REF_X360", f)
		}
	}
	refOnce.Do(func() {
		game := dir
		if fi, err := os.Stat(filepath.Join(dir, "Game")); err == nil && fi.IsDir() {
			game = filepath.Join(dir, "Game")
		}
		r := &x360Ref{game: game, banks: map[string]*xwb.Bank{}, sbs: map[string]*xwb.SoundBank{}}
		cat, err := catalog.Open(filepath.Join(game, xma2.UberCatalog))
		if err != nil {
			refErr = err
			return
		}
		defer cat.Close()
		for _, p := range [][2]string{{xma2.VoiceBank, xma2.VoiceSoundBank}, {xma2.SFXBank, xma2.SFXSoundBank}} {
			data, err := os.ReadFile(filepath.Join(game, filepath.FromSlash(p[0])))
			if err != nil {
				refErr = err
				return
			}
			if r.banks[p[0]], err = xwb.Open(data); err != nil {
				refErr = err
				return
			}
			x, err := cat.ReadByName(p[1])
			if err != nil {
				refErr = err
				return
			}
			if r.sbs[p[1]], err = xwb.ParseSoundBank(x); err != nil {
				refErr = err
				return
			}
		}
		ref = r
	})
	if refErr != nil {
		t.Fatal(refErr)
	}
	return ref
}

func gameWAV(t *testing.T, res *xma2.Result, livePath string) []byte {
	t.Helper()
	w, err := audio.ToGameWAV(res.Samples, res.SampleRate, audio.Options{
		DropSamples: xma2.X360DropSamples, Invert: xma2.X360Invert, Gain: xma2.X360Gain, Seed: audio.SeedFor(livePath)})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// The mapping table must agree with the sound banks: every target's cue is a single-wave cue that plays
// the listed entry, and every entry is XMA mono 48 kHz.
func TestTargetsMatchSoundBanks(t *testing.T) {
	r := x360(t)
	tg := xma2.Targets()
	if len(tg) != 47 {
		t.Fatalf("%d targets, want 47", len(tg))
	}
	seen := map[string]bool{}
	for _, x := range tg {
		if seen[x.LivePath] {
			t.Fatalf("duplicate %s", x.LivePath)
		}
		seen[x.LivePath] = true
		c, ok := r.sbs[x.SoundBank].Cue(x.Cue)
		if !ok || !c.Simple {
			t.Fatalf("%s: cue %q not a single-wave cue in %s", x.LivePath, x.Cue, x.SoundBank)
		}
		if c.WaveIndex != x.Entry {
			t.Fatalf("%s: cue %q plays entry %d, table says %d", x.LivePath, x.Cue, c.WaveIndex, x.Entry)
		}
		e := r.banks[x.Bank].Entries[x.Entry]
		if e.Codec != xwb.CodecXMA || e.Channels != 1 || e.SampleRate != 48000 {
			t.Fatalf("%s: entry %d is %v %d ch %d Hz", x.LivePath, x.Entry, e.Codec, e.Channels, e.SampleRate)
		}
		if got, ok := xma2.TargetFor(x.LivePath); !ok || got != x {
			t.Fatalf("TargetFor(%s) = %+v", x.LivePath, got)
		}
	}
}

// All 47 waves decode without a bitstream error; decoding is deterministic (same samples, bit for bit, and
// the same WAV bytes for the same seed) and quick.
func TestDecodeAllDeterministic(t *testing.T) {
	r := x360(t)
	t0 := time.Now()
	total := 0
	for _, x := range xma2.Targets() {
		a, err := xma2.DecodeEntry(r.banks[x.Bank], x.Entry)
		if err != nil {
			t.Fatalf("%s: %v", x.LivePath, err)
		}
		total += len(a.Samples)
		// the decoder output length is the bank's loop length for every one of these waves
		if e := r.banks[x.Bank].Entries[x.Entry]; uint32(len(a.Samples)) != e.LoopLength {
			t.Errorf("%s: %d samples, bank loop length %d", x.LivePath, len(a.Samples), e.LoopLength)
		}
		b, err := xma2.DecodeEntry(r.banks[x.Bank], x.Entry)
		if err != nil {
			t.Fatal(err)
		}
		if sumF32(a.Samples) != sumF32(b.Samples) {
			t.Fatalf("%s: two decodes differ", x.LivePath)
		}
		if !bytes.Equal(gameWAV(t, a, x.LivePath), gameWAV(t, b, x.LivePath)) {
			t.Fatalf("%s: two WAV builds differ", x.LivePath)
		}
	}
	t.Logf("decoded 47 waves twice and built their WAVs twice in %v (%d samples per pass)", time.Since(t0), total)
}

func sumF32(s []float32) [32]byte {
	b := make([]byte, 4*len(s))
	for i, v := range s {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(v))
	}
	return sha256.Sum256(b)
}

func liveDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("FEAR_REF_LIVEMOD")
	if d == "" {
		t.Skip("golden test skipped: set FEAR_REF_LIVEMOD (mod folder with the shipped WAVs) and FEAR_REF_X360 to run it")
	}
	return d
}

// The X360-sourced shipped files (Holiday voice lines) are reproduced: same length, lag 0, same polarity,
// SNR above minX360SNRdB.
func TestGoldenX360(t *testing.T) {
	live := liveDir(t)
	r := x360(t)
	var snrs []float64
	t.Logf("%-28s %-18s %5s %7s %4s %3s %7s %6s %5s", "live path", "cue", "entry", "samples", "lag", "pol", "SNR dB", "rms", "peak")
	for _, x := range xma2.Targets() {
		if x.Source != xma2.ConsoleX360Name {
			continue
		}
		res, err := xma2.DecodeEntry(r.banks[x.Bank], x.Entry)
		if err != nil {
			t.Fatalf("%s: %v", x.LivePath, err)
		}
		want, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(x.LivePath)))
		if err != nil {
			t.Fatal(err)
		}
		rep, err := audio.CompareWAV(gameWAV(t, res, x.LivePath), want)
		if err != nil {
			t.Fatalf("%s: %v", x.LivePath, err)
		}
		t.Logf("%-28s %-18s %5d %7d %4d %3d %7.2f %6.3f %5d", x.LivePath, x.Cue, x.Entry, rep.GotLen, rep.Lag, rep.Polarity, rep.SNRdB, rep.NoiseRMS, rep.PeakDiff)
		if !rep.SameLength || rep.Lag != 0 || rep.Polarity != 1 || rep.SNRdB < minX360SNRdB {
			t.Errorf("%s: length %d want %d, lag %d, polarity %d, SNR %.2f dB (min %.1f)", x.LivePath, rep.GotLen, rep.WantLen, rep.Lag, rep.Polarity, rep.SNRdB, minX360SNRdB)
		}
		snrs = append(snrs, rep.SNRdB)
	}
	if len(snrs) != 28 {
		t.Fatalf("%d X360-sourced targets, want 28", len(snrs))
	}
	sort.Float64s(snrs)
	t.Logf("SNR over %d files: min %.2f, median %.2f, max %.2f dB", len(snrs), snrs[0], (snrs[13]+snrs[14])/2, snrs[len(snrs)-1])
}

// The shipped SM15 files were converted from the PS3 disc, not from these waves: the X360 encode is a
// different take of the same sound (different length, low correlation). This test only documents that
// (log) and makes sure no X360 rebuild is mistaken for the shipped file.
func TestX360SM15DiffersFromShipped(t *testing.T) {
	live := liveDir(t)
	r := x360(t)
	t.Logf("%-34s %7s %7s %6s %4s %7s", "live path", "x360", "shipped", "lag", "pol", "SNR dB")
	for _, x := range xma2.Targets() {
		if x.Source != xma2.ConsolePS3Name {
			continue
		}
		res, err := xma2.DecodeEntry(r.banks[x.Bank], x.Entry)
		if err != nil {
			t.Fatalf("%s: %v", x.LivePath, err)
		}
		want, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(x.LivePath)))
		if err != nil {
			t.Fatal(err)
		}
		rep, err := audio.CompareWAV(gameWAV(t, res, x.LivePath), want)
		if err != nil {
			t.Fatalf("%s: %v", x.LivePath, err)
		}
		t.Logf("%-34s %7d %7d %6d %4d %7.2f", x.LivePath, rep.GotLen, rep.WantLen, rep.Lag, rep.Polarity, rep.SNRdB)
		if rep.SameLength && rep.Lag == 0 && rep.SNRdB >= minX360SNRdB {
			t.Errorf("%s: the X360 wave reproduces the shipped file; its Source should be x360", x.LivePath)
		}
	}
}

// BuildX360 (what the installer runs) gives exactly the bytes TestGoldenX360 measured, for every X360 target.
func TestBuildX360SameAsGolden(t *testing.T) {
	r := x360(t)
	got, err := xma2.BuildX360(r.game)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 28 || len(xma2.X360Paths()) != 28 {
		t.Fatalf("built %d files, %d paths, want 28", len(got), len(xma2.X360Paths()))
	}
	for _, x := range xma2.Targets() {
		if x.Source != xma2.ConsoleX360Name {
			continue
		}
		res, err := xma2.DecodeEntry(r.banks[x.Bank], x.Entry)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got[x.LivePath], gameWAV(t, res, x.LivePath)) {
			t.Fatalf("%s: BuildX360 differs from the golden conversion", x.LivePath)
		}
	}
}
