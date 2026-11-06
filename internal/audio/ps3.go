package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fearinstaller/internal/audio/atrac3"
	"fearinstaller/internal/audio/msf"
	"fearinstaller/internal/catalog"
)

// PS3 ATRAC3 recipe constants, measured on the shipped files (all 55 whole-file conversions): the decoder
// output (FFmpeg, which does not trim ATRAC3's delay) leads the shipped audio by exactly 1162 samples at
// 48 kHz, with inverted polarity and unity gain.
const (
	PS3DropSamples = 1162
	PS3Invert      = true
)

// Console names a console copy the player must supply.
type Console string

const (
	ConsolePS3  Console = "ps3"
	ConsoleX360 Console = "x360"
)

// PS3Target is one game file rebuilt from the PS3 sound catalogs.
type PS3Target struct {
	LivePath string // path in the mod folder, as the coverage table writes it
	Catalog  string // catalog below the PS3 game dir
	Entry    string // MSF entry in that catalog
	Part     int    // 0 = whole file; 1..3 = Watson reload split part
	Length   int    // output samples; 0 = the length rule (ResampledLength)
}

// sm15LoopLength: the three SM15 fire loops of the confirmed install are not full-length conversions but the
// first N samples of one (measured: the common part matches at dither level, RMS 0.71 LSB, and every
// file ends 1379-1398 samples before the full 123118). The cut points differ per file and follow no rule found
// (not a zero crossing, a shot period or a seamless loop point; the MSF headers carry no loop data and are
// identical for all three), so the recipe keeps them as its own constants.
var sm15LoopLength = map[string]int{"fireloop.WAV": 121739, "fireloop_AI.WAV": 121729, "fireloop_dist.wav": 121720}

var ps3Voices = []string{
	"B2001", "B2001r", "B2002", "B2003", "B2003r", "B2004", "B2005", "B2005r", "B2006", "B2006r",
	"B2007", "B2007r", "B2008", "B2008r", "B2009", "B2010", "B2010r", "B2011", "B2012", "B2012r",
	"B2013", "B2013r", "B2014", "B2014r", "B2015", "B2015r", "B2016", "B2016r", "B2017", "B2017r",
	"B2018", "B2019", "B2020", "B2020r", "B2021", "B2021r", "B2022", "B2023", "B2023r", "B2024",
	"B2025", "B2025r", "B2026", "B2026r", "B2027", "B2027r", "B2028", "B2028r", "B2029", "B2029r",
	"B2030", "B2030r", "B2031",
}

// ps3SM15 are the 19 SM15 sound files as the mod folder names them (two loops keep the stock upper-case .WAV).
var ps3SM15 = []string{
	"deselect.wav", "fire.wav", "fire_01.wav", "fire_01_dist.wav", "fire_02.wav", "fire_02_dist.wav",
	"fire_03.wav", "fire_03_dist.wav", "fire_04.wav", "fire_04_dist.wav", "fire_05.wav", "fire_05_dist.wav",
	"fireloop.WAV", "fireloop_AI.WAV", "fireloop_dist.wav", "reload1.wav", "reload2.wav", "reload3.wav", "select.wav",
}

const (
	ps3VoiceCatalog = "snd/english.cat"
	ps3SfxCatalog   = "snd/sfx.cat"
	ps3ReloadEntry  = `snd\sfx\Guns\Shotgun\Shotgun_WASreload.wav`
)

// PS3Targets lists the 77 PS3-sourced game WAVs: 53 SFOD-D voice lines, the two Watson fire sounds, the
// three parts of the Watson reload and the 19 SM15 sounds.
func PS3Targets() []PS3Target {
	var t []PS3Target
	for _, v := range ps3Voices {
		t = append(t, PS3Target{"Voice/" + v + ".wav", ps3VoiceCatalog, `snd\voice\` + v + ".wav", 0, 0})
	}
	for _, f := range []string{"WASfire1", "WASfire2"} {
		t = append(t, PS3Target{"guns/Snd/Shotgun/" + f + ".wav", ps3SfxCatalog, `snd\sfx\Guns\Shotgun\Shotgun_` + f + ".wav", 0, 0})
	}
	for i, f := range []string{"WASreload.WAV", "WASreload2.WAV", "WASreload3.WAV"} {
		t = append(t, PS3Target{"guns/Snd/Shotgun/" + f, ps3SfxCatalog, ps3ReloadEntry, i + 1, 0})
	}
	// The SM15 sounds were converted from the PS3 recordings too (the Xbox 360 ones are different takes or
	// mixes: SNR -0.8 to 12.9 dB against the confirmed files), so an SM15 install needs the PS3 copy for them.
	for _, f := range ps3SM15 {
		base := strings.TrimSuffix(strings.TrimSuffix(f, ".wav"), ".WAV")
		t = append(t, PS3Target{"guns/Snd/SM15/" + f, ps3SfxCatalog, `snd\sfx\Guns\SM15\SM15_` + base + ".wav", 0, sm15LoopLength[f]})
	}
	return t
}

// PS3Needs maps every live path BuildPS3 makes to the console copy it needs (all PS3).
func PS3Needs() map[string]Console {
	m := map[string]Console{}
	for _, t := range PS3Targets() {
		m[t.LivePath] = ConsolePS3
	}
	return m
}

// DecodePS3MSF decodes an ATRAC3 MSF file to mono float samples at its own rate (48000 on the PS3 disc).
func DecodePS3MSF(b []byte) ([]float32, int, error) {
	f, err := msf.Parse(b)
	if err != nil {
		return nil, 0, err
	}
	if !f.IsATRAC3() {
		return nil, 0, fmt.Errorf("audio: MSF codec %d is not ATRAC3", f.Codec)
	}
	if f.Channels != 1 {
		return nil, 0, fmt.Errorf("audio: MSF has %d channels, the game files are mono", f.Channels)
	}
	pcm, err := atrac3.DecodeAll(f.Data, f.Channels, f.BlockAlign(), f.JointStereo())
	if err != nil {
		return nil, 0, err
	}
	return pcm[0], f.SampleRate, nil
}

// ConvertPS3MSF converts one PS3 ATRAC3 MSF file with the PS3 recipe; seedName picks the dither noise.
func ConvertPS3MSF(b []byte, seedName string) ([]int16, error) {
	return convertPS3MSF(b, seedName, 0)
}

// convertPS3MSF is ConvertPS3MSF with a forced output length (0 = the length rule). A shorter length only cuts
// the end: the samples before the cut and their dither are the same as in the full-length conversion.
func convertPS3MSF(b []byte, seedName string, length int) ([]int16, error) {
	pcm, rate, err := DecodePS3MSF(b)
	if err != nil {
		return nil, err
	}
	return ToPCM16(pcm, rate, Options{DropSamples: PS3DropSamples, Invert: PS3Invert, Seed: SeedFor(seedName), Length: length})
}

// BuildPS3 rebuilds the 77 PS3-sourced WAVs from a PS3 game folder (the folder holding snd/english.cat and
// snd/sfx.cat). The result maps each live path (PS3Targets) to the file bytes.
func BuildPS3(gameDir string) (map[string][]byte, error) {
	cats := map[string]*catalog.Catalog{}
	defer func() {
		for _, c := range cats {
			c.Close()
		}
	}()
	open := func(rel string) (*catalog.Catalog, error) {
		if c, ok := cats[rel]; ok {
			return c, nil
		}
		p := filepath.Join(gameDir, filepath.FromSlash(rel))
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("audio: PS3 catalog %s: %w", rel, err)
		}
		c, err := catalog.Open(p)
		if err != nil {
			return nil, fmt.Errorf("audio: PS3 catalog %s: %w", rel, err)
		}
		cats[rel] = c
		return c, nil
	}
	type job struct {
		t   PS3Target
		msf []byte
	}
	var jobs []job
	seen := map[string]bool{}
	for _, t := range PS3Targets() {
		if t.Part > 1 || seen[t.Entry] {
			continue // the reload recording is converted once (part 1 job) and split
		}
		seen[t.Entry] = true
		c, err := open(t.Catalog)
		if err != nil {
			return nil, err
		}
		b, err := c.ReadByName(t.Entry)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job{t, b})
	}

	out := make(map[string][]byte, len(PS3Targets()))
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := buildPS3Job(j.t, j.msf)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("audio: %s: %w", j.t.LivePath, err)
				}
				return
			}
			for k, v := range res {
				out[k] = v
			}
		}(j)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func buildPS3Job(t PS3Target, b []byte) (map[string][]byte, error) {
	if t.Part == 0 {
		// Seed from the live path, so B2001 and B2001r (different recordings) also get different noise.
		s, err := convertPS3MSF(b, strings.ToLower(t.LivePath), t.Length)
		if err != nil {
			return nil, err
		}
		return map[string][]byte{t.LivePath: WriteWAV(s, OutputRate)}, nil
	}
	s, err := ConvertPS3MSF(b, strings.ToLower("guns/Snd/Shotgun/WASreload.WAV"))
	if err != nil {
		return nil, err
	}
	parts, err := SplitWatsonReload(s)
	if err != nil {
		return nil, err
	}
	res := map[string][]byte{}
	for _, pt := range PS3Targets() {
		if pt.Entry == t.Entry && pt.Part > 0 {
			res[pt.LivePath] = WriteWAV(parts[pt.Part-1], OutputRate)
		}
	}
	return res, nil
}
