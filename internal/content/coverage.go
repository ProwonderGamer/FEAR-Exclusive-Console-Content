package content

import (
	"path"
	"strings"
	"sync"

	"fearinstaller/internal/objects00p"
)

// Item says which content choice needs a mod file.
type Item int

const (
	ItemAny     Item = iota // every install (databases, mod text, campaign fixes)
	ItemSFOD                // bonus mission SFOD-D (PlayStation 3)
	ItemHoliday             // bonus mission Holiday (Xbox 360)
	ItemBonus               // either bonus mission (the grid and calibration textures; installed with SFOD-D or Holiday)
	ItemSAS12               // Watson SAS-12 (PlayStation 3)
	ItemSM15                // SM15 machine pistol (Xbox 360)
	ItemIA                  // Instant Action (Xbox 360)
)

// ModFile is one file of the complete mod as it is in game (in-game confirmed).
type ModFile struct {
	Path   string // mod layout, '/' separated
	Item   Item
	SHA256 string // the confirmed file
	Built  bool   // the content builder makes exactly this file today
}

// Choice is what the player picked, and which console copies the build may read. With neither HavePS3 nor
// HaveX360 set, the copies are those the items need (SFOD-D and the Watson: PS3; the rest: Xbox 360).
type Choice struct {
	SFOD, Holiday, SAS12, SM15, IA bool
	HavePS3, HaveX360              bool
}

// have returns the console copies the build may read.
func (c Choice) have() src {
	if !c.HavePS3 && !c.HaveX360 {
		c.HavePS3, c.HaveX360 = c.SFOD || c.SAS12, c.Holiday || c.SM15 || c.IA
	}
	var h src
	if c.HavePS3 {
		h |= srcPS3
	}
	if c.HaveX360 {
		h |= srcX360
	}
	return h
}

// needs returns the console copies a built file can be made from (any one of them is enough, except for the
// world object files: see objectsNeeds; 0 = the PC game alone). The databases and the mod text need the PS3
// copy (dbmerge.BuildAll). dbmerge.BuildIAOnly, an Xbox-only database build, exists and is tested, but the
// installer does not call it, because it requires both console copies.
func needs(path string) src {
	if from, ok := assetNeeds(path); ok {
		return from
	}
	if from, ok := soundNeeds(path); ok {
		return from
	}
	if from, ok := iaMapNeeds(path); ok {
		return from
	}
	if from, ok := objectsNeeds(path); ok {
		return from
	}
	l := strings.ToLower(path)
	switch {
	case strings.HasPrefix(l, "database/"), strings.HasPrefix(l, "databaselocalized/"), strings.HasPrefix(l, "stringdatabase/"):
		return srcPS3
	}
	return srcPS3 | srcX360
}

// buildable: the builder makes this file from the copies of the choice.
func (c Choice) buildable(f ModFile) bool {
	n := needs(f.Path)
	if _, ok := objectsNeeds(f.Path); ok {
		return f.Built && n&c.have() == n
	}
	return f.Built && (n == 0 || n&c.have() != 0)
}

var (
	objectsOnce sync.Once
	objectsSrc  map[string]src // lower-case world name -> console copies its weapon placements come from
)

// objectsNeeds returns the console copies a campaign world's object file (Worlds/Release/<World>.Objects00p)
// is built from: the placements of each weapon are read from that weapon's console worlds (objects00p.ConsoleOf),
// so a world that receives both weapons needs both copies.
func objectsNeeds(p string) (src, bool) {
	l := strings.ToLower(p)
	if !strings.HasSuffix(l, ".objects00p") {
		return 0, false
	}
	objectsOnce.Do(func() {
		objectsSrc = map[string]src{}
		spec, err := objects00p.EmbeddedSpec()
		if err != nil {
			return // the objects stage reports the same error
		}
		for w, ws := range spec {
			var n src
			for _, op := range ws.Ops {
				if op.Console == nil {
					continue
				}
				switch c, _ := objects00p.ConsoleOf(op.Weapon); c {
				case objects00p.X360:
					n |= srcX360
				case objects00p.PS3:
					n |= srcPS3
				}
			}
			objectsSrc[strings.ToLower(w)] = n
		}
	})
	return objectsSrc[strings.TrimSuffix(path.Base(l), ".objects00p")], true
}

// Complete reports the one choice whose every file is proven today: the whole mod (both bonus missions, both
// weapons and Instant Action) from both console copies. The databases and the mod text are built as one set
// for every choice (dbmerge.BuildAll adds the SFOD-D, Holiday, Instant Action and Watson records whenever the
// copies allow), and only that whole-mod build is golden-tested, so a smaller choice would install database
// records whose worlds and models it leaves out. The installer writes only a Complete choice until per-choice
// database and text builds exist and are proven.
func (c Choice) Complete() bool {
	return c.SFOD && c.Holiday && c.SAS12 && c.SM15 && c.IA && c.have() == srcPS3|srcX360
}

func (c Choice) wants(it Item) bool {
	switch it {
	case ItemAny:
		return c.SFOD || c.Holiday || c.SAS12 || c.SM15 || c.IA
	case ItemSFOD:
		return c.SFOD
	case ItemHoliday:
		return c.Holiday
	case ItemBonus:
		return c.SFOD || c.Holiday
	case ItemSAS12:
		return c.SAS12
	case ItemSM15:
		return c.SM15
	case ItemIA:
		return c.IA
	}
	return false
}

// ModFiles returns the confirmed mod files a choice needs.
func ModFiles(c Choice) []ModFile {
	var out []ModFile
	for _, f := range modFiles {
		if c.wants(f.Item) {
			out = append(out, f)
		}
	}
	return out
}

// NotBuilt returns the files a choice needs that the content builder cannot make (yet, or from these copies). The installer writes
// into a game folder only when this is empty: a partial mod (a bonus mission without its world, a weapon
// without its sounds) must never be installed.
func NotBuilt(c Choice) []string {
	var out []string
	for _, f := range ModFiles(c) {
		if !c.buildable(f) {
			out = append(out, f.Path)
		}
	}
	return out
}

// Coverage counts, for a choice, the needed files and how many of them the builder makes.
func Coverage(c Choice) (built, needed int) {
	for _, f := range ModFiles(c) {
		needed++
		if c.buildable(f) {
			built++
		}
	}
	return
}

// confirmedHash returns the confirmed SHA-256 of a mod file ("" when the path is not a mod file).
func confirmedHash(path string) string {
	for _, f := range modFiles {
		if strings.EqualFold(f.Path, path) {
			return f.SHA256
		}
	}
	return ""
}
