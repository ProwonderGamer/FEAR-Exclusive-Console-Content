package xma2

// Original code of this project (not derived from FFmpeg).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fearinstaller/internal/audio"
	"fearinstaller/internal/audio/xwb"
)

// X360Paths lists the live paths BuildX360 makes: the targets whose shipped file came from the Xbox 360
// (Source "x360", the 28 Holiday voice lines). The SM15 targets are left out: their shipped files came from
// the PS3 disc (audio.PS3Targets) and the Xbox 360 waves do not reproduce them.
func X360Paths() []string {
	var p []string
	for _, t := range Targets() {
		if t.Source == ConsoleX360Name {
			p = append(p, t.LivePath)
		}
	}
	return p
}

// BuildX360 rebuilds the X360Paths files from an Xbox 360 copy: gameDir is the disc root (holding "Game") or
// its Game folder. Each wave is decoded and turned into the game's WAV with the Xbox 360 recipe (no delay to
// drop, same polarity, unity gain, dither seeded by the live path as in the golden test).
func BuildX360(gameDir string) (map[string][]byte, error) {
	game := gameDir
	if p, err := findDirCI(gameDir, "Game"); err == nil {
		game = p
	}
	banks := map[string]*xwb.Bank{}
	out := map[string][]byte{}
	for _, t := range Targets() {
		if t.Source != ConsoleX360Name {
			continue
		}
		b := banks[t.Bank]
		if b == nil {
			p, err := findFileCI(game, t.Bank)
			if err != nil {
				return nil, fmt.Errorf("xma2: Xbox 360 wave bank %s: %w", t.Bank, err)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			if b, err = xwb.Open(data); err != nil {
				return nil, fmt.Errorf("xma2: %s: %w", t.Bank, err)
			}
			banks[t.Bank] = b
		}
		res, err := DecodeEntry(b, t.Entry)
		if err != nil {
			return nil, fmt.Errorf("xma2: %s: %w", t.LivePath, err)
		}
		w, err := audio.ToGameWAV(res.Samples, res.SampleRate, audio.Options{
			DropSamples: X360DropSamples, Invert: X360Invert, Gain: X360Gain, Seed: audio.SeedFor(t.LivePath)})
		if err != nil {
			return nil, fmt.Errorf("xma2: %s: %w", t.LivePath, err)
		}
		out[t.LivePath] = w
	}
	return out, nil
}

// findDirCI returns root/name when it is a folder (any letter case).
func findDirCI(root, name string) (string, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, e := range ents {
		if e.IsDir() && strings.EqualFold(e.Name(), name) {
			return filepath.Join(root, e.Name()), nil
		}
	}
	return "", fmt.Errorf("%s not found in %s", name, root)
}

// findFileCI resolves a '/'-separated relative path below root, ignoring letter case.
func findFileCI(root, rel string) (string, error) {
	cur := root
	for _, seg := range strings.Split(rel, "/") {
		ents, err := os.ReadDir(cur)
		if err != nil {
			return "", err
		}
		found := ""
		for _, e := range ents {
			if strings.EqualFold(e.Name(), seg) {
				found = e.Name()
				break
			}
		}
		if found == "" {
			return "", fmt.Errorf("%s not found in %s", seg, cur)
		}
		cur = filepath.Join(cur, found)
	}
	return cur, nil
}
