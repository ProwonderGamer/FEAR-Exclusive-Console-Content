package content

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"fearinstaller/internal/audio"
	"fearinstaller/internal/audio/xma2"
	"fearinstaller/internal/holworld"
	"fearinstaller/internal/iaworld"
	"fearinstaller/internal/sfodworld"
	"fearinstaller/internal/strdb"
)

func TestCoverageTable(t *testing.T) {
	if len(modFiles) != 185 {
		t.Fatalf("%d mod files, want the 185 of the confirmed install", len(modFiles))
	}
	seen := map[string]bool{}
	for i, f := range modFiles {
		k := strings.ToLower(f.Path)
		if seen[k] {
			t.Fatalf("%s listed twice", f.Path)
		}
		seen[k] = true
		if len(f.SHA256) != 64 || strings.Trim(f.SHA256, "0123456789abcdef") != "" {
			t.Fatalf("%s: bad hash", f.Path)
		}
		if i > 0 && strings.ToLower(modFiles[i-1].Path) > k {
			t.Fatalf("not sorted at %s", f.Path)
		}
	}
	all := Choice{SFOD: true, Holiday: true, SAS12: true, SM15: true, IA: true}
	if b, n := Coverage(all); n != 185 || b != 185 {
		t.Fatalf("coverage %d/%d", b, n)
	}
	// Without the PS3 copy, nothing that needs it counts (databases, mod text, Watson, SFOD-D models).
	xb := Choice{Holiday: true, SM15: true, IA: true}
	for _, f := range ModFiles(xb) {
		if xb.buildable(f) && needs(f.Path) == srcPS3 {
			t.Fatalf("Xbox-only choice counts %s as buildable", f.Path)
		}
	}
	// A PS3-only SFOD-D choice cannot make the Xbox 360-only black.dds; with the Xbox copy too it can.
	sf := Choice{SFOD: true}
	for _, nb := range NotBuilt(sf) {
		if nb == "Materials/Engineering/black.dds" {
			goto found
		}
	}
	t.Fatal("black.dds counted without the Xbox 360 copy")
found:
	sf.HavePS3, sf.HaveX360 = true, true
	for _, nb := range NotBuilt(sf) {
		if nb == "Materials/Engineering/black.dds" {
			t.Fatal("black.dds not counted with both copies")
		}
	}
	if len(ModFiles(Choice{})) != 0 {
		t.Fatal("files for an empty choice")
	}
	// The Xbox-only choice must never need a PlayStation 3 item.
	for _, f := range ModFiles(Choice{Holiday: true, SM15: true, IA: true}) {
		if f.Item == ItemSFOD || f.Item == ItemSAS12 {
			t.Fatalf("Xbox-only choice needs %s", f.Path)
		}
	}
}

// The files marked Built carry the hashes the golden tests prove: a table row must never claim more than a test does.
func TestCoverageMatchesGoldens(t *testing.T) {
	if h := confirmedHash(strdb.ConsoleTextFile); h != "6f81f17098775ee28e1a4c61f4e7540ce4f2aefa09b93f39fbcdf02d0102c74f" {
		t.Fatalf("ConsoleText %s", h)
	}
	// The live databases: dbmerge's stage-4 goldens.
	dbsrc, err := os.ReadFile("../dbmerge/live_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for path, name := range map[string]string{"Database/FEAR.gamdb00p": "goldLiveFEAR", "DatabaseLocalized/FEARL.Gamdb00p": "goldLiveFEARL"} {
		m := regexp.MustCompile(name + `\s*=\s*"([0-9a-f]{64})"`).FindStringSubmatch(string(dbsrc))
		if m == nil || m[1] != confirmedHash(path) {
			t.Errorf("%s: table %s, dbmerge golden %v", path, confirmedHash(path), m)
		}
	}
	// The Objects00p goldens live in that package's test; read them from its source.
	src, err := os.ReadFile("../objects00p/objects00p_test.go")
	if err != nil {
		t.Fatal(err)
	}
	gold := map[string]string{}
	for _, m := range regexp.MustCompile(`"([A-Za-z_]+)":\s+"([0-9a-f]{64})"`).FindAllStringSubmatch(string(src), -1) {
		gold[m[1]] = m[2]
	}
	n := 0
	for _, f := range modFiles {
		if _, ia := iaMapNeeds(f.Path); !f.Built || !strings.HasSuffix(f.Path, ".Objects00p") || ia {
			continue // the Instant Action map files are checked in TestCoverageMediaRows
		}
		name := strings.TrimSuffix(strings.TrimPrefix(f.Path, "Worlds/Release/"), ".Objects00p")
		if gold[name] != f.SHA256 {
			t.Errorf("%s: table %s, objects00p golden %q", f.Path, f.SHA256, gold[name])
		}
		n++
	}
	if n != 19 {
		t.Fatalf("%d Objects00p marked built", n)
	}
	// The SFOD-D world, the Watson body and the animation trees: their packages' golden constants.
	if h := confirmedHash("Worlds/Release/SFOD_D.World00p"); h != sfodworld.LiveSHA256 {
		t.Errorf("SFOD_D.World00p: table %s, sfodworld golden %s", h, sfodworld.LiveSHA256)
	}
	if h := confirmedHash(holworld.OutputPath); h != holworld.LiveSHA256 {
		t.Errorf("%s: table %s, holworld golden %s", holworld.OutputPath, h, holworld.LiveSHA256)
	}
	for file, golds := range map[string]map[string]string{
		"../model/watson_test.go": {"chars/models/player_watson.Model00p": "goldenPlayerWatson"},
		"../model/x360_test.go": {
			"guns/models_hh/SM15machinePistol.Model00p":           "goldenSM15machinePistol",
			"guns/models_hh/SM15_Right.Model00p":                  "goldenSM15Right",
			"guns/models_hh/SM15_Left.Model00p":                   "goldenSM15Left",
			"chars/models/player_holiday.Model00p":                "goldenPlayerHoliday",
			"chars/models/Base_BishopEvac_BishopSitting.Model00p": "goldenBishopSitting",
			"chars/models/IanHives_Holiday.Model00p":              "goldenIanHivesHoliday",
		},
		"../anmtree/anmtree_test.go": {
			"AnimationDatabase/AnimTrees/Player_Upper_SFODD.AnmTree00p": "goldenPlayerUpperSFODD",
			"AnimationDatabase/AnimTrees/Soldier_Holiday.AnmTree00p":    "goldenSoldierHoliday",
			"AnimationDatabase/AnimTrees/Soldier_Holiday2.AnmTree00p":   "goldenSoldierHoliday2",
		},
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for path, name := range golds {
			m := regexp.MustCompile(name + `\s*=\s*"([0-9a-f]{64})"`).FindStringSubmatch(string(src))
			if m == nil || m[1] != confirmedHash(path) {
				t.Errorf("%s: table %s, golden %s %v", path, confirmedHash(path), name, m)
			}
		}
	}
}

// The media rows: the HUD icons and the Instant Action maps carry their packages' byte-exact golden hashes, and
// the sound rows are exactly the files the two sound recipes make (their golden tests compare each one with the
// confirmed file to the dither level; see media.go).
func TestCoverageMediaRows(t *testing.T) {
	hsrc, err := os.ReadFile("../hudicon/hudicon_test.go")
	if err != nil {
		t.Fatal(err)
	}
	icons := regexp.MustCompile(`"(\w+)":\s+\{"(x360|ps3)", "([0-9a-f]{64})"\}`).FindAllStringSubmatch(string(hsrc), -1)
	if len(icons) != 6 {
		t.Fatalf("%d hudicon goldens", len(icons))
	}
	for _, m := range icons {
		path := "Interface/hud/weapon/" + m[1] + ".dds"
		if confirmedHash(path) != m[3] {
			t.Errorf("%s: table %s, hudicon golden %s", path, confirmedHash(path), m[3])
		}
		from, ok := assetNeeds(path)
		if want := map[string]src{"x360": srcX360, "ps3": srcPS3}[m[2]]; !ok || from != want {
			t.Errorf("%s: built from %v, the golden is from %s", path, from, m[2])
		}
	}
	if len(iaworld.LiveSHA256) != 8 {
		t.Fatalf("%d iaworld goldens", len(iaworld.LiveSHA256))
	}
	for path, h := range iaworld.LiveSHA256 {
		if confirmedHash(path) != h {
			t.Errorf("%s: table %s, iaworld golden %s", path, confirmedHash(path), h)
		}
		if from, ok := iaMapNeeds(path); !ok || from != srcX360 {
			t.Errorf("%s: not an Xbox 360 map row", path)
		}
	}
	sounds := map[string]bool{}
	for p := range audio.PS3Needs() {
		sounds[strings.ToLower(p)] = true
	}
	for _, p := range xma2.X360Paths() {
		if sounds[strings.ToLower(p)] {
			t.Errorf("%s made by both sound recipes", p)
		}
		sounds[strings.ToLower(p)] = true
	}
	n, pics := 0, 0
	for _, f := range modFiles {
		isWAV := strings.HasSuffix(strings.ToLower(f.Path), ".wav")
		if isWAV != sounds[strings.ToLower(f.Path)] {
			t.Errorf("%s: WAV row %v, made by a sound recipe %v", f.Path, isWAV, sounds[strings.ToLower(f.Path)])
		}
		if isWAV {
			n++
			if !f.Built || !lookAlike(f.Path) {
				t.Errorf("%s: a sound row must be built and look-alike", f.Path)
			}
		} else if isLoadPicture(f.Path) {
			pics++
			if from, ok := iaMapNeeds(f.Path); !f.Built || !lookAlike(f.Path) || !ok || from != srcX360 {
				t.Errorf("%s: a loading picture must be built, look-alike and from the Xbox 360 copy", f.Path)
			}
		} else if lookAlike(f.Path) {
			t.Errorf("%s: look-alike but not a sound or loading picture", f.Path)
		}
	}
	if pics != 4 {
		t.Errorf("%d loading picture rows, want 4", pics)
	}
	if n != 105 || len(sounds) != 105 {
		t.Fatalf("%d WAV rows, %d sound targets, want 105", n, len(sounds))
	}
	// Without the PS3 copy the SM15 sounds (converted from the PS3 disc) are not buildable.
	if nb := NotBuilt(Choice{SM15: true, HaveX360: true}); len(nb) == 0 || !strings.Contains(strings.Join(nb, " "), "guns/Snd/SM15/fire.wav") {
		t.Errorf("SM15 from the Xbox 360 copy alone: missing %v", nb)
	}
}
