package xma2

// Original code of this project (not derived from FFmpeg).
//
// The game WAVs that can be rebuilt from the Xbox 360 wave banks, and the recipe that turns a decoded
// wave into the shipped file. Names and indices only; no game data.

// Xbox 360 recipe, measured on the 28 shipped Holiday voice WAVs: FFmpeg's xma2 output is used
// as is (no extra delay to drop: cross-correlation lag 0 at 44.1 kHz for every file), same polarity, unity
// gain (fitted gain 0.999-1.001), resampled 48000 -> 44100 with length round(n * 44100 / 48000), and peaks
// above full scale (decoded peaks reach 1.23) hard-clipped at the 16-bit step after resampling.
const (
	X360DropSamples = 0
	X360Invert      = false
	X360Gain        = 1.0
)

// Wave bank and sound bank locations in an Xbox 360 copy. The banks are plain files below the game folder
// ("Game" in a dump); the sound banks that name the waves sit in Game\uberCatalog.cat.
const (
	UberCatalog     = "uberCatalog.cat"
	VoiceBank       = "Snd/VoiceEnglish.xwb"
	VoiceSoundBank  = `Snd\VoiceEnglish.xsb`
	SFXBank         = "Snd/SFX.xwb"
	SFXSoundBank    = `Snd\SFX.xsb`
	ConsoleX360Name = "x360"
	ConsolePS3Name  = "ps3"
)

// Target is one game file that an Xbox 360 wave can rebuild.
type Target struct {
	LivePath  string // path in the mod folder (forward slashes, case as shipped)
	Bank      string // wave bank below the X360 Game folder (VoiceBank or SFXBank)
	SoundBank string // sound bank in uberCatalog.cat that names the cue
	Cue       string // cue name in that sound bank
	Entry     int    // wave bank entry the cue plays (checked against the sound bank by the golden test)
	// Source names the console the SHIPPED file was made from: "x360" (the X360 rebuild reproduces it) or
	// "ps3" (the shipped file came from the PS3 disc; the X360 wave is the same sound from a different
	// encode and does NOT reproduce the shipped file, so it may only stand in for an Xbox-only install).
	Source string
}

// holidayVoices are the Holiday mission voice cues and their VoiceEnglish.xwb entries. The PS3 disc carries
// the same lines (snd\english.cat, snd\voice\B1xxx.wav).
var holidayVoices = []struct {
	cue   string
	entry int
}{
	{"B1001", 3255}, {"B1002", 3256}, {"B1002r", 3331}, {"B1003", 3257}, {"B1003r", 3335},
	{"B1004", 3258}, {"B1004r", 3328}, {"B1005", 3259}, {"B1006", 3260}, {"B1006r", 3333},
	{"B1007", 3261}, {"B1007r", 3332}, {"B1020", 3262}, {"B1020r", 3334}, {"B1021", 3263},
	{"B1021r", 3324}, {"B1022", 3264}, {"B1022r", 3330}, {"B1030", 3265}, {"B1030r", 3326},
	{"B1031", 3266}, {"B1031r", 3329}, {"B1032", 3267}, {"B1032r", 3327}, {"B1033", 3268},
	{"B1033r", 3325}, {"B1036", 3269}, {"B1037", 3270},
}

// sm15Sounds are the SM15 weapon cues ("SM15_" + name) and their SFX.xwb entries, with the shipped file
// name (two keep the upper-case .WAV extension of the shipped files).
var sm15Sounds = []struct {
	name  string
	entry int
	file  string
}{
	{"deselect", 1587, "deselect.wav"}, {"fire", 1588, "fire.wav"}, {"fire_01", 1589, "fire_01.wav"},
	{"fire_01_dist", 1590, "fire_01_dist.wav"}, {"fire_02", 1591, "fire_02.wav"},
	{"fire_02_dist", 1592, "fire_02_dist.wav"}, {"fire_03", 1593, "fire_03.wav"},
	{"fire_03_dist", 1594, "fire_03_dist.wav"}, {"fire_04", 1595, "fire_04.wav"},
	{"fire_04_dist", 1577, "fire_04_dist.wav"}, {"fire_05", 1578, "fire_05.wav"},
	{"fire_05_dist", 1579, "fire_05_dist.wav"}, {"fireloop", 1580, "fireloop.WAV"},
	{"fireloop_AI", 1581, "fireloop_AI.WAV"}, {"fireloop_dist", 1582, "fireloop_dist.wav"},
	{"reload1", 1583, "reload1.wav"}, {"reload2", 1584, "reload2.wav"}, {"reload3", 1585, "reload3.wav"},
	{"select", 1586, "select.wav"},
}

// Targets lists the 47 game WAVs an Xbox 360 copy can provide: the 28 Holiday voice lines (Source "x360":
// the shipped files are these waves) and the 19 SM15 sounds (Source "ps3": the shipped SM15 files were
// converted from the PS3 disc, snd\sfx.cat snd\sfx\Guns\SM15\SM15_<name>.wav).
func Targets() []Target {
	var t []Target
	for _, v := range holidayVoices {
		t = append(t, Target{"Voice/" + v.cue + ".wav", VoiceBank, VoiceSoundBank, v.cue, v.entry, ConsoleX360Name})
	}
	for _, s := range sm15Sounds {
		t = append(t, Target{"guns/Snd/SM15/" + s.file, SFXBank, SFXSoundBank, "SM15_" + s.name, s.entry, ConsolePS3Name})
	}
	return t
}

// TargetFor returns the target for a live path (exact match, forward slashes).
func TargetFor(livePath string) (Target, bool) {
	for _, t := range Targets() {
		if t.LivePath == livePath {
			return t, true
		}
	}
	return Target{}, false
}
