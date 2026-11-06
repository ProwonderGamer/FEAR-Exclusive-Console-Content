package content

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fearinstaller/internal/audio"
	"fearinstaller/internal/audio/xma2"
	"fearinstaller/internal/catalog"
	"fearinstaller/internal/dbmerge"
	"fearinstaller/internal/iaworld"
	"fearinstaller/internal/loadart"
	"fearinstaller/internal/pcfs"
)

// The "sounds" and "iamaps" stages: files that one recipe makes as a set (all PS3 sounds, all Xbox 360 voice
// lines, the eight Instant Action map files), so they do not fit the one-file rows of the assets table.
//
//   - PS3 sounds (audio.BuildPS3): 53 SFOD-D voice lines, the Watson fire and reload sounds and the 19 SM15
//     sounds, from the PS3 snd\english.cat and snd\sfx.cat.
//   - Xbox 360 voices (xma2.BuildX360): the 28 Holiday voice lines, from Game\Snd\VoiceEnglish.xwb.
//   - Instant Action maps (iaworld.Build): from the Xbox 360 InstantAction1-4 worlds and the stock PC archives.
//   - Instant Action loading pictures (loadart.Build, same stage): Xbox 360 art + NEUROPOX.TTF + Arial Bold.
//
// The sounds are LOOK-ALIKE builds (lookAlike): the confirmed files carry random dither and were resampled by a
// tool that cannot be reproduced exactly, so a rebuild can never be byte-identical. Their golden tests (audio.TestGoldenPS3,
// xma2.TestGoldenX360) prove each file matches the confirmed one to the dither level (same length, lag 0, same
// polarity, noise RMS ~0.71 LSB), and they are approximate (tolerance-tested) rebuilds. The loading
// pictures are look-alike too (text edges, loadart.TestGoldenLive). The map files are byte-exact
// (iaworld.TestGoldenLive).

// soundNeeds returns the console a sound file is rebuilt from (ok = it is one).
func soundNeeds(rel string) (from src, ok bool) {
	for p := range audio.PS3Needs() {
		if strings.EqualFold(p, rel) {
			return srcPS3, true
		}
	}
	for _, p := range xma2.X360Paths() {
		if strings.EqualFold(p, rel) {
			return srcX360, true
		}
	}
	return 0, false
}

// iaMapNeeds: the Instant Action map files need the Xbox 360 copy (ok = it is one). The PS3 copy only adds the
// Vault's two Watson pickups, and only when the Watson is installed too (iaPS3Dir).
func iaMapNeeds(rel string) (from src, ok bool) {
	for _, l := range iaworld.Labels {
		if strings.EqualFold(rel, iaworld.WorldPath(l)) || strings.EqualFold(rel, iaworld.ObjectsPath(l)) {
			return srcX360, true
		}
	}
	if isLoadPicture(rel) {
		return srcX360, true
	}
	return 0, false
}

// lookAlike reports a file whose rebuild matches the confirmed file within a measured tolerance instead of
// byte for byte (see the comment at the top of this file). Such a file counts as built, but is an
// approximate rebuild.
func lookAlike(rel string) bool {
	_, ok := soundNeeds(rel)
	return ok || isLoadPicture(rel)
}

// isLoadPicture reports one of the four IA loading pictures (loadart.Build).
func isLoadPicture(rel string) bool {
	for i := range loadart.Maps {
		if strings.EqualFold(rel, loadart.OutputPath(i+1)) {
			return true
		}
	}
	return false
}

// LookAlike is lookAlike for the installer's reports.
func LookAlike(rel string) bool { return lookAlike(rel) }

// wantedFile reports whether the choice installs the mod file rel.
func (c Choice) wantedFile(rel string) bool {
	for _, f := range modFiles {
		if strings.EqualFold(f.Path, rel) {
			return c.wants(f.Item)
		}
	}
	return false
}

// writeSet writes the files of a set that the choice installs; every one must be a mod file.
func writeSet(w *writer, c Choice, set map[string][]byte) error {
	rels := make([]string, 0, len(set))
	for rel := range set {
		rels = append(rels, rel)
	}
	sort.Strings(rels) // a fixed order, so the progress events do not vary between runs
	for _, rel := range rels {
		data := set[rel]
		if confirmedHash(rel) == "" {
			return fmt.Errorf("content: %s is not a mod file", rel)
		}
		if !c.wantedFile(rel) {
			continue
		}
		if err := w.write("Game/"+rel, data); err != nil {
			return err
		}
	}
	return nil
}

// anyWanted reports whether the choice installs at least one of the paths.
func (c Choice) anyWanted(paths []string) bool {
	for _, p := range paths {
		if c.wantedFile(p) {
			return true
		}
	}
	return false
}

// choiceFor completes the assets-stage choice as buildAssets does.
func (o *Options) choiceFor() Choice {
	c := o.Choice
	if c == (Choice{}) {
		c = Choice{SFOD: o.PS3Dir != "", SAS12: o.PS3Dir != "", Holiday: o.X360Dir != "", SM15: o.X360Dir != ""}
	}
	c.HavePS3, c.HaveX360 = o.PS3Dir != "", o.X360Dir != ""
	return c
}

// buildSounds runs the sounds stage. A set whose console copy is missing is logged and left out; the
// installer's completeness check then refuses to install.
func (o *Options) buildSounds(w *writer, log *dbmerge.Log) error {
	c := o.choiceFor()
	var ps3Paths []string
	for p := range audio.PS3Needs() {
		ps3Paths = append(ps3Paths, p)
	}
	if c.anyWanted(ps3Paths) {
		if !c.HavePS3 {
			log.Add("sounds not built", "the SFOD-D, Watson and SM15 sounds need the PlayStation 3 copy")
		} else {
			o.say("Converting the PlayStation 3 sounds")
			set, err := audio.BuildPS3(o.PS3Dir)
			if err != nil {
				return err
			}
			if err := writeSet(w, c, set); err != nil {
				return err
			}
		}
	}
	if c.anyWanted(xma2.X360Paths()) {
		if !c.HaveX360 {
			log.Add("sounds not built", "the Holiday voice lines need the Xbox 360 copy")
		} else {
			o.say("Converting the Xbox 360 voice lines")
			set, err := xma2.BuildX360(o.X360Dir)
			if err != nil {
				return err
			}
			if err := writeSet(w, c, set); err != nil {
				return err
			}
		}
	}
	return nil
}

// iaPS3Dir is the PS3 copy the map build reads: only when the Watson is installed too, since the PS3 Vault adds
// two Watson pickups (the confirmed maps); otherwise the Xbox-only Vault (iaworld.XboxOnlySHA256).
func (o *Options) iaPS3Dir(c Choice) string {
	if c.SAS12 && c.HavePS3 {
		return o.PS3Dir
	}
	return ""
}

// buildIAMaps runs the iamaps stage.
func (o *Options) buildIAMaps(w *writer, fs *pcfs.FS, log *dbmerge.Log) error {
	c := o.choiceFor()
	if !c.IA {
		return nil
	}
	if !c.HaveX360 {
		log.Add("maps not built", "the Instant Action maps need the Xbox 360 copy")
		return nil
	}
	o.say("Converting the Instant Action maps")
	in, err := iaworld.LoadInputs(o.X360Dir, o.iaPS3Dir(c), fs)
	if err != nil {
		return err
	}
	set, err := iaworld.Build(in)
	if err != nil {
		return err
	}
	if err := writeSet(w, c, set); err != nil {
		return err
	}
	return o.buildIAPictures(w, c, fs)
}

// arialBoldPath is the player's Windows Arial Bold, which the IA loading pictures' map lines use (the recipe
// drew them with it). Options.ArialBold overrides it.
func (o *Options) arialBoldPath() string {
	if o.ArialBold != "" {
		return o.ArialBold
	}
	win := os.Getenv("SystemRoot")
	if win == "" {
		win = os.Getenv("windir")
	}
	if win == "" {
		win = `C:\Windows` // neither variable is set: the default Windows folder
	}
	return filepath.Join(win, "Fonts", "arialbd.ttf")
}

// buildIAPictures makes the four IA loading pictures (loadart.Build) from the Xbox 360 uberCatalog, the game's
// own NEUROPOX.TTF (stock PC archives) and Arial Bold. LOOK-ALIKE: the text edges differ from the confirmed
// files by FreeType's hinting, which the port does not run (loadart.TestGoldenLive measures it).
func (o *Options) buildIAPictures(w *writer, c Choice, fs *pcfs.FS) error {
	o.say("Drawing the Instant Action loading pictures")
	p, err := FindCI(o.X360Dir, "Game", "uberCatalog.cat")
	if err != nil {
		return fmt.Errorf("Xbox 360 uberCatalog.cat: %w", err)
	}
	cat, err := catalog.Open(p)
	if err != nil {
		return fmt.Errorf("Xbox 360 uberCatalog.cat: %w", err)
	}
	defer cat.Close()
	np, err := fs.Read(`interface\fonts\NEUROPOX.TTF`)
	if err != nil {
		return fmt.Errorf("the game's NEUROPOX.TTF: %w", err)
	}
	ab, err := os.ReadFile(o.arialBoldPath())
	if err != nil {
		return fmt.Errorf("Windows Arial Bold (arialbd.ttf): %w", err)
	}
	set, err := loadart.Build(cat, np, ab)
	if err != nil {
		return err
	}
	return writeSet(w, c, set)
}
