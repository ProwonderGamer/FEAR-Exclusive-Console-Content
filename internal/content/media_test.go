package content

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/audio"
	"fearinstaller/internal/iaworld"
	"fearinstaller/internal/testref"
)

// TestMediaStagesMatchConfirmed runs the sounds and iamaps stages the way the installer does (content.Build,
// whole mod, both console copies) and checks every written file against the confirmed install: the map files
// byte for byte, the sounds and loading pictures against the confirmed files within their golden tests' tolerance. With the
// PS3 copy left out it checks that the Xbox 360 voices and the Xbox-only Vault are made, and nothing PS3-sourced.
func TestMediaStagesMatchConfirmed(t *testing.T) {
	testref.File(t, "FEAR_REF_PS3", "snd", "sfx.cat")
	testref.File(t, "FEAR_REF_PS3", "worlds", "release", "instantaction4.wldsrvr")
	testref.File(t, "FEAR_REF_X360", "Game", "Snd", "VoiceEnglish.xwb")
	testref.File(t, "FEAR_REF_X360", "Game", "Worlds", "Release", "InstantAction4.wldsrvr")
	for _, a := range []string{"FEAR.Arch00", "FEAR_7.Arch00", "FEAR_8.Arch00", "FEARA_8.Arch00", "FEARE_8.Arch00", "FEARL_8.Arch00"} {
		testref.File(t, "FEAR_REF_PCGAME", a)
	}
	if _, err := os.Stat((&Options{}).arialBoldPath()); err != nil {
		t.Skipf("Arial Bold: %v", err)
	}
	live := testref.File(t, "FEAR_REF_LIVEMOD", "Voice", "B1001.wav")
	live = dirOf(live, 2)
	ps3, x360, pc := os.Getenv("FEAR_REF_PS3"), os.Getenv("FEAR_REF_X360"), os.Getenv("FEAR_REF_PCGAME")
	all := Choice{SFOD: true, Holiday: true, SAS12: true, SM15: true, IA: true}

	check := func(t *testing.T, ps3Dir string, want int) map[string]string {
		res, err := Build(Options{PCDir: pc, PS3Dir: ps3Dir, X360Dir: x360, OutDir: t.TempDir(), Stages: []string{"sounds", "iamaps"}, Choice: all})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Files) != want {
			t.Fatalf("%d files written, want %d", len(res.Files), want)
		}
		got := map[string]string{}
		for _, f := range res.Files {
			got[strings.TrimPrefix(f.Rel, "Game/")] = f.SHA256
		}
		return got
	}

	t.Run("both", func(t *testing.T) {
		out := t.TempDir()
		res, err := Build(Options{PCDir: pc, PS3Dir: ps3, X360Dir: x360, OutDir: out, Stages: []string{"sounds", "iamaps"}, Choice: all})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Files) != 105+8+4 {
			t.Fatalf("%d files written, want 117 (105 sounds, 8 map files, 4 loading pictures)", len(res.Files))
		}
		for _, f := range res.Files {
			rel := strings.TrimPrefix(f.Rel, "Game/")
			if !lookAlike(rel) {
				if h := confirmedHash(rel); h != f.SHA256 {
					t.Errorf("%s: built %s, confirmed %s", rel, f.SHA256, h)
				}
				continue
			}
			got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(f.Rel)))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(live, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatal(err)
			}
			if isLoadPicture(rel) {
				// the pixel tolerance is loadart.TestGoldenLive's; here: same DDS header and size, and only the
				// text edges differ (well under 2% of the bytes)
				diff := 0
				for i := range got {
					if i < len(want) && got[i] != want[i] {
						diff++
					}
				}
				if len(got) != len(want) || !bytes.Equal(got[:128], want[:128]) || diff*50 > len(got) {
					t.Errorf("%s: %d bytes (live %d), %d differ", rel, len(got), len(want), diff)
				}
				continue
			}
			r, err := audio.CompareWAV(got, want)
			if err != nil {
				t.Fatalf("%s: %v", rel, err)
			}
			// the sound golden tests' bounds: PS3 sounds as audio.TestGoldenPS3 (noise and peak at dither level,
			// edge samples excepted), Xbox 360 voices as xma2.TestGoldenX360 (SNR at least 60 dB)
			bad := !r.SameLength || r.Lag != 0 || r.Polarity != 1
			if from, _ := soundNeeds(rel); from == srcPS3 {
				bad = bad || r.SNRdB < 48 || r.NoiseRMS > 0.75 || r.BodyPeak > 4 || r.PeakDiff > 64
			} else {
				bad = bad || r.SNRdB < 60
			}
			if bad {
				t.Errorf("%s: %+v", rel, r)
			}
		}
	})

	t.Run("xbox-only", func(t *testing.T) {
		got := check(t, "", 28+8+4)
		for rel := range got {
			if from, _ := soundNeeds(rel); from == srcPS3 {
				t.Errorf("%s made without the PS3 copy", rel)
			}
		}
		for _, l := range iaworld.Labels {
			for _, p := range []string{iaworld.WorldPath(l), iaworld.ObjectsPath(l)} {
				want := iaworld.LiveSHA256[p]
				if x, ok := iaworld.XboxOnlySHA256[p]; ok {
					want = x
				}
				if got[p] != want {
					t.Errorf("%s: %s, want %s", p, got[p], want)
				}
			}
		}
	})
}
