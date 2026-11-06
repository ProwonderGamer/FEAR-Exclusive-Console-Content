package audio

import (
	"errors"
	"fmt"
	"math"
)

// Watson SAS-12 reload split.
//
// The PS3 reload is one recording, guns\Snd\Shotgun\WASreload.WAV (44.1 kHz, 142875 samples once converted).
// The split cuts it, never stretched or re-pitched, at its two silent dips into the engine's three reload-sound
// slots: part 1 (drum out) keeps the name WASreload.WAV, part 2 (drum in, lock) is WASreload2.WAV and part 3
// (drum spin) WASreload3.WAV. Each inner cut edge gets a 5 ms linear fade so no edge can click.
//
// The recipe finds each cut as the middle of the quietest 10 ms window (1 ms steps) in a search range
// (QuietestCut). On the converted recording it chose samples 80830 and 112142 (1.833 s and 2.543 s; the
// shipped part lengths 80830, 31312 and 30733 confirm them). Those two numbers are the fix decision and are
// used directly, so the cut can never move with the dither noise of a rebuild; the golden test checks the
// rule still lands on them for the rebuilt recording.
var WatsonReloadCuts = [2]int{80830, 112142}

// WatsonReloadSearch are the recipe's search ranges (seconds) and quietness limits (dB) for the two cuts.
var WatsonReloadSearch = [2]struct{ T0, T1, LimitDB float64 }{
	{1.78, 1.85, -50}, // gap before the drum goes back in
	{2.50, 2.56, -45}, // dip right before the spin clicks
}

const watsonReloadLen = 142875

// rmsDB is the recipe's level of the window starting at i: 20 log10(sqrt(mean(x^2)) / 32768), with a zero
// level read as 1e-9 (Python "or 1e-9").
func rmsDB(pcm []int16, i, win int) float64 {
	end := min(i+win, len(pcm))
	var s float64
	for _, v := range pcm[i:end] {
		s += float64(v) * float64(v)
	}
	r := math.Sqrt(s/float64(end-i)) / 32768
	if r == 0 {
		r = 1e-9
	}
	return 20 * math.Log10(r)
}

// QuietestCut is the recipe's cut rule at 44.1 kHz: the start of the quietest 10 ms window between t0 and t1
// seconds in 1 ms steps (first minimum wins), which must be below limit dB; the cut is the window's middle.
func QuietestCut(pcm []int16, t0, t1, limit float64) (int, error) {
	const sr = OutputRate
	win := sr / 100
	best, bestDB := -1, 0.0
	for i := int(t0 * sr); i < int(t1*sr); i += sr / 1000 {
		if i < 0 || i >= len(pcm) {
			break
		}
		if d := rmsDB(pcm, i, win); best < 0 || d < bestDB {
			best, bestDB = i, d
		}
	}
	if best < 0 {
		return 0, errors.New("audio: cut search range outside the recording")
	}
	if !(bestDB < limit) {
		return 0, fmt.Errorf("audio: quietest window at %d is %.1f dB, not below %.0f dB", best, bestDB, limit)
	}
	return best + win/2, nil
}

// SplitWithFades cuts pcm at the given ascending sample positions. Every part except the first fades in over
// the first fade samples and every part except the last fades out over its last fade samples; each fade
// rounds half to even, in the recipe's order (fade in, then fade out on the rounded result).
func SplitWithFades(pcm []int16, cuts []int, fade int) ([][]int16, error) {
	edges := append(append([]int{0}, cuts...), len(pcm))
	for i := 1; i < len(edges); i++ {
		if edges[i] <= edges[i-1] {
			return nil, errors.New("audio: cut points must be ascending and inside the recording")
		}
	}
	parts := make([][]int16, 0, len(edges)-1)
	for k := 0; k+1 < len(edges); k++ {
		a, b := edges[k], edges[k+1]
		s := append([]int16(nil), pcm[a:b]...)
		if a > 0 {
			for i, x := range s {
				s[i] = int16(math.RoundToEven(float64(x) * math.Min(1, float64(i)/float64(fade))))
			}
		}
		if b < len(pcm) {
			n := len(s)
			for i, x := range s {
				s[i] = int16(math.RoundToEven(float64(x) * math.Min(1, float64(n-1-i)/float64(fade))))
			}
		}
		parts = append(parts, s)
	}
	return parts, nil
}

// SplitWatsonReload cuts the converted Watson reload recording into its three parts.
func SplitWatsonReload(pcm []int16) ([3][]int16, error) {
	var out [3][]int16
	if len(pcm) != watsonReloadLen {
		return out, fmt.Errorf("audio: Watson reload has %d samples, the cuts were made on %d", len(pcm), watsonReloadLen)
	}
	parts, err := SplitWithFades(pcm, WatsonReloadCuts[:], OutputRate*5/1000)
	if err != nil {
		return out, err
	}
	copy(out[:], parts)
	return out, nil
}
