// Package sfodworld builds the SFOD-D bonus mission world "Worlds\Release\SFOD_D.World00p" from the player's
// own PlayStation 3 copy and the stock PC game. It is a Go port of the recipe chain that produced the in-game
// confirmed file (SHA-256 803cf806...):
//
//  1. Conversion: the PS3 world converted by the project's Python PS3 world converter with three edit sets:
//     the five zSFODD_ database renames, the slow-mo lock (computed here from the PS3 .wldsrvr, as the
//     project's Python slow-mo patch did) and the AINodeCover97 crash fix.
//  2. Death lure: PlayerDeathLure.AllowSwitchWeapon False -> True (one byte, file offset 0x1950F9D).
//  3. Intro delay: IntroDialog.Delay1 0.0 -> 1.0.
//  4. Finale catch-up for a Delta01 left behind (two commands extended, three objects appended).
//
// Inputs (all read, nothing written):
//
//	PS3   USRDIR/game/worlds/release/sfod_d.wld, sfod_d.wldclnt, sfod_d.wldsrvr
//	PC    Worlds\Release\{Aftermath,Moody,Admin,ATC_Roof,Mapes_Elevator}.World00p (stock FEAR.Arch00) and the
//	      file listing of every stock *.Arch00 (which resources exist on PC, and their spelling)
//
// No Xbox 360 data is used. The only embedded data are the recipe's own decisions: the list of files the mod
// adds (modfiles.txt, names only), the renames, the fixed edits and the finale catch-up object values.
package sfodworld

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/wld"
)

// OutputPath is the mod-relative path of the built world.
const OutputPath = "Worlds/Release/SFOD_D.World00p"

// LiveSHA256 is the hash of the in-game confirmed world (after the finale catch-up).
const LiveSHA256 = "803cf80652cb99a80b7988c66e71f63af6fa264501373eb28310f07c9720cc8c"

// PCWorldNames are the stock PC worlds the converter reads: class schemas from all five (in this order), and
// PC Admin (the level SFOD-D is built on) also for property values, precache entries and vertex inputs.
var PCWorldNames = []string{"Aftermath", "Moody", "Admin", "ATC_Roof", "Mapes_Elevator"}

// modfiles.txt: the resources the mod adds (names only), which the converter treats as present on PC
// (the recipe's --extra list: the confirmed mod folder's files except Tex\Localization\Calibration_A0.dds,
// which is not part of the SFOD-D content).
//
//go:embed modfiles.txt
var modFilesTxt string

// ModFiles returns the embedded list of mod-added resource paths.
func ModFiles() []string {
	var out []string
	for _, l := range strings.Split(modFilesTxt, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Inputs holds everything Build reads.
type Inputs struct {
	PS3Wld, PS3Clnt, PS3Srvr []byte            // PS3 worlds/release/sfod_d.wld, .wldclnt, .wldsrvr
	PCWorlds                 map[string][]byte // PCWorldNames -> stock Worlds\Release\<name>.World00p
	Listing                  []ArchiveFile     // every file of the stock PC archives (ListArchives or ReadTOC)
	ModFiles                 []string          // nil: the embedded ModFiles()
}

// findCI finds root/rel... with case-insensitive components.
func findCI(root string, rel ...string) (string, error) {
	cur := root
	for _, part := range rel {
		ents, err := os.ReadDir(cur)
		if err != nil {
			return "", err
		}
		found := ""
		for _, e := range ents {
			if strings.EqualFold(e.Name(), part) {
				found = e.Name()
				break
			}
		}
		if found == "" {
			return "", fmt.Errorf("sfodworld: %s not found in %s", part, cur)
		}
		cur = filepath.Join(cur, found)
	}
	return cur, nil
}

// LoadInputs reads the inputs: ps3GameDir is the PS3 "PS3_GAME/USRDIR/game" folder, fs the opened stock PC
// archives (pcfs.Open of the game folder; its archives are listed again with ListArchives(fs.Dir)).
func LoadInputs(ps3GameDir string, fs *pcfs.FS) (*Inputs, error) {
	in := &Inputs{PCWorlds: map[string][]byte{}}
	for _, f := range []struct {
		dst *[]byte
		ext string
	}{{&in.PS3Wld, "wld"}, {&in.PS3Clnt, "wldclnt"}, {&in.PS3Srvr, "wldsrvr"}} {
		p, err := findCI(ps3GameDir, "worlds", "release", "sfod_d."+f.ext)
		if err != nil {
			return nil, err
		}
		if *f.dst, err = os.ReadFile(p); err != nil {
			return nil, err
		}
	}
	for _, n := range PCWorldNames {
		b, err := fs.Read(`Worlds\Release\` + n + ".World00p")
		if err != nil {
			return nil, err
		}
		in.PCWorlds[n] = b
	}
	var err error
	if in.Listing, err = ListArchives(fs.Dir); err != nil {
		return nil, err
	}
	return in, nil
}

// Build returns the finished SFOD_D.World00p.
func Build(in *Inputs) ([]byte, error) { return build(in, nil) }

// Stage names passed to the stage callback of build, with the hashes the recipe chain produced.
const (
	StageRaw        = "raw"         // converter output without any edit (5205e97f...)
	StageConverted  = "converted"   // after the conversion edits: renames, slow-mo lock, cover-node fix (a0b05c5e...)
	StageDeathLure  = "death-lure"  // after the death lure fix (f9d8667e...)
	StageIntroDelay = "intro-delay" // after the intro delay fix (242d19e8...)
	StageFinale     = "finale"      // after the finale catch-up (803cf806..., the in-game confirmed world)
)

func build(in *Inputs, stage func(name string, b []byte)) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(errFormat)
			if !ok {
				re, isRT := r.(runtime.Error) // malformed input that slipped past a bounds check
				if !isRT {
					panic(r)
				}
				e = errFormat{re.Error()}
			}
			out, err = nil, errors.New("sfodworld: "+e.msg)
		}
	}()
	if in == nil || len(in.PS3Wld) == 0 || len(in.PS3Clnt) == 0 || len(in.PS3Srvr) == 0 {
		return nil, errors.New("sfodworld: PS3 sfod_d world files missing")
	}
	for _, n := range PCWorldNames {
		if len(in.PCWorlds[n]) == 0 {
			return nil, fmt.Errorf("sfodworld: PC world %s missing", n)
		}
	}
	if len(in.Listing) == 0 {
		return nil, errors.New("sfodworld: PC archive listing missing")
	}
	extra := in.ModFiles
	if extra == nil {
		extra = ModFiles()
	}
	files := newPCFiles(in.Listing, extra)
	admin := in.PCWorlds["Admin"]

	// --- conversion (ps3world2pc.convert_world) ---
	refPairs := pcAssetList(admin)
	core, blindOfs := convertCore(in.PS3Wld, func(p []assetPair) []byte {
		return buildAssetListPC(append(append([]assetPair(nil), p...), refPairs...), files)
	})
	render, sec1 := convertRender(in.PS3Clnt, renderRequirements(admin))
	var worlds [][]wld.Object
	for _, n := range PCWorldNames {
		worlds = append(worlds, pcObjects(in.PCWorlds[n]))
	}
	schema := schemaFromWorlds(worlds)
	ref := refProps(worlds[2]) // Admin
	_, ps3objs, perr := wld.ParseConsoleServer(in.PS3Srvr)
	if perr != nil {
		fail("PS3 .wldsrvr: %v", perr)
	}
	objs := convertObjects(ps3objs, schema, ref, files)
	blind := convertBlind(in.PS3Wld, blindOfs)
	assemble := func(o []wld.Object) []byte {
		sec := wld.SerializeObjects(o)
		o0 := 0x14 + len(core)
		o1 := o0 + len(render)
		o2 := o1 + len(sec1)
		o3 := o2 + len(sec)
		b := make([]byte, 0, o3+len(blind))
		for _, v := range []int{0x71, o0, o1, o2, o3} {
			b = binary.LittleEndian.AppendUint32(b, uint32(v))
		}
		b = append(b, core...)
		b = append(b, render...)
		b = append(b, sec1...)
		b = append(b, sec...)
		return append(b, blind...)
	}
	if stage != nil {
		stage(StageRaw, assemble(objs))
	}
	edits := conversionEdits(ps3objs)
	applyConversionEdits(objs, edits)
	converted := assemble(objs)
	if stage != nil {
		stage(StageConverted, converted)
	}

	// --- PlayerDeathLure.AllowSwitchWeapon False -> True; one byte at 0x1950F9D ---
	lure := getObj(objs, "PlayerDeathLure")
	if lure.Class != "PlayerLure" || propOf(lure, "AllowWeapon", wld.TBool).U != 0 {
		fail("death lure fix: PlayerDeathLure is not the expected PlayerLure with AllowWeapon=False")
	}
	if p := propOf(lure, "AllowSwitchWeapon", wld.TBool); p.U != 0 {
		fail("death lure fix: AllowSwitchWeapon is already True")
	} else {
		p.U = 1
	}
	withLure := assemble(objs)
	if d := diffOffsets(converted, withLure); len(d) != 1 || d[0] != 0x1950F9D {
		fail("death lure fix: the edit did not change exactly the byte at 0x1950F9D (%v)", d)
	}
	converted = nil
	if stage != nil {
		stage(StageDeathLure, withLure)
	}

	// --- IntroDialog.Delay1 0.0 -> 1.0 (4-byte float inline in the property table) ---
	intro := getObj(objs, "IntroDialog")
	if intro.Class != "Dialogue" {
		fail("intro delay fix: IntroDialog is not a Dialogue")
	}
	if p := propOf(intro, "Delay1", wld.TFloat); p.U != 0 {
		fail("intro delay fix: IntroDialog.Delay1 is %#x, want 0.0", p.U)
	} else {
		p.U = math.Float32bits(1.0)
	}
	withIntro := assemble(objs)
	if d := diffOffsets(withLure, withIntro); len(d) == 0 || len(d) > 4 || d[len(d)-1]-d[0] > 3 {
		fail("intro delay fix: unexpected byte changes %v", d)
	}
	withLure = nil
	if stage != nil {
		stage(StageIntroDelay, withIntro)
	}

	// --- finale catch-up ---
	objs = applyFinaleCatchUp(objs)
	finale := assemble(objs)
	if !bytes.Equal(finale[0x14:binary.LittleEndian.Uint32(withIntro[12:])], withIntro[0x14:binary.LittleEndian.Uint32(withIntro[12:])]) {
		fail("finale catch-up: data before the object section changed")
	}
	if stage != nil {
		stage(StageFinale, finale)
	}
	return finale, nil
}

func diffOffsets(a, b []byte) []int {
	if len(a) != len(b) {
		return []int{-1}
	}
	var d []int
	for i := range a {
		if a[i] != b[i] {
			d = append(d, i)
			if len(d) > 16 {
				break
			}
		}
	}
	return d
}

// nameOf: value of the object's first "Name" property when it is a string.
func nameOf(o *wld.Object) string {
	for _, p := range o.Props {
		if p.Name == "Name" {
			if p.Type == wld.TString || p.Type == wld.TCommand || p.Type == wld.TStringID {
				return p.S
			}
			return "\x00nonstring"
		}
	}
	return "\x00none"
}

func getObj(objs []wld.Object, name string) *wld.Object {
	var hit *wld.Object
	n := 0
	for i := range objs {
		if nameOf(&objs[i]) == name {
			hit = &objs[i]
			n++
		}
	}
	if n != 1 {
		fail("object %s found %d times", name, n)
	}
	return hit
}

// propOf returns the first property named name, which must have type t.
func propOf(o *wld.Object, name string, t uint32) *wld.Prop {
	i := o.PropIndex(name)
	if i < 0 || o.Props[i].Type != t {
		fail("%s.%s missing or not of type %d", nameOf(o), name, t)
	}
	return &o.Props[i]
}

// ---------------------------------------------------------------------------------------------- conversion edits

type editKey struct{ class, object, prop string }

type edit struct{ old, new string }

// zSFODDPrefix is the sort-last prefix of the database records the mod adds.
const zSFODDPrefix = "zSFODD_"

// conversionEdits returns the complete conversion edit set: renames of the five world references to PS3-only database
// records, the objective command, the AINodeCover97 crash fix and the slow-mo lock edits from the PS3 objects.
func conversionEdits(ps3 []wld.Object) map[editKey]edit {
	e := map[editKey]edit{}
	add := func(k editKey, old, new string) {
		if _, dup := e[k]; dup {
			fail("conversion edits: two edits for %v", k)
		}
		e[k] = edit{old, new}
	}
	for _, r := range []struct {
		class   string
		objects []string
		prop    string
		value   string
	}{
		{"GameStartPoint", []string{"GameStartPoint00"}, "ModelTemplate", "Player_DeltaForce"},
		{"CAI", []string{"CivilianCAI"}, "ModelTemplate", "Corpse_office_Bob_Alive"},
		{"CAI", []string{"FifthAI_00", "FourthAI_01", "FourthAI_03"}, "Weapon0", "AutoShotgun"},
		{"CAI", []string{"secondAI_04"}, "Weapon0", "Machine Pistol"},
		{"WeaponItem", []string{"Autoshot", "WeaponItem00"}, "WeaponType", "AutoShotgun"},
	} {
		for _, o := range r.objects {
			add(editKey{r.class, o, r.prop}, r.value, zSFODDPrefix+r.value)
		}
	}
	// objective command: record Interface/Objective/Objective47 -> zSFODD_Objective47
	add(editKey{"Dialogue", "IntroDialog", "CleanUpCommand"},
		"msg player (objective add objective47);", "msg player (objective add "+zSFODDPrefix+"Objective47);")
	// crash fix: PC GameServer treats a cover node's Dependency as an AINodeSmartObject; the only
	// non-AINodeFlipable dependency in sfod_d (a RotatingDoor) crashes it.
	add(editKey{"AINodeCover", "AINodeCover97", "Dependency"}, "Door-SingleRotate103.Door_WM", "")
	for _, s := range slowmoEdits(ps3) {
		add(s.key, s.old, s.new)
	}
	return e
}

// applyConversionEdits applies the conversion edit set (see conversionEdits) to the converted objects; every edit must apply exactly once to the
// expected old value.
func applyConversionEdits(objs []wld.Object, edits map[editKey]edit) {
	applied := map[editKey]int{}
	for i := range objs {
		o := &objs[i]
		nm := nameOf(o)
		for j := range o.Props {
			p := &o.Props[j]
			k := editKey{o.Class, nm, p.Name}
			ed, ok := edits[k]
			if !ok {
				continue
			}
			if p.Type != wld.TString && p.Type != wld.TCommand && p.Type != wld.TStringID || p.S != ed.old {
				fail("conversion edits: %v is %q, want %q", k, p.S, ed.old)
			}
			p.S = ed.new
			applied[k]++
		}
	}
	for k := range edits {
		if applied[k] != 1 {
			fail("conversion edits: edit %v applied %d times", k, applied[k])
		}
	}
}

// ------------------------------------------------------------------------------------------- slow-mo lock

// The slow-mo lock (a port of the project's Python slow-mo patch): an active scripted slow-mo record blocks
// player slow-mo on PC (GameServer v1.08 FUN_1012ec30); SlowMo/NoSlowMo has Period 0 and 1.0 time scales.
//  1. StartupCommand00.Command += "msg player (slowmo on noslowmo);"
//  2. every "msg player (slowmo off)" gets a re-lock 0.05 s later, as a sibling command at the same level.
//
// The regular expressions run on the recipe's Latin-1 decoded text: strings are widened to UTF-8 first so
// that \s has Python's meaning (including \v, \x1c-\x1f, \x85, \xa0).
const pyWS = `[\t\n\v\f\r \x{1c}-\x{1f}\x{85}\x{a0}]`

var (
	reOff        = regexp.MustCompile(`(?i)msg` + pyWS + `+player` + pyWS + `*\(` + pyWS + `*slowmo` + pyWS + `+off` + pyWS + `*\)`)
	reDelayedOff = regexp.MustCompile(`(?i)delay` + pyWS + `+([0-9.]+)` + pyWS + `*\(` + pyWS + `*msg` + pyWS + `+player` + pyWS + `*\(` + pyWS + `*slowmo` + pyWS + `+off` + pyWS + `*\)` + pyWS + `*\)` + pyWS + `*;?`)
	reRelock     = regexp.MustCompile(`(?i)msg` + pyWS + `+player` + pyWS + `*\(` + pyWS + `*slowmo` + pyWS + `+on` + pyWS + `+noslowmo` + pyWS + `*\)`)
)

const relock = "msg player (slowmo on noslowmo)"

func latin1ToUTF8(s string) string {
	b := make([]byte, 0, len(s)+8)
	for i := 0; i < len(s); i++ {
		b = utf8.AppendRune(b, rune(s[i]))
	}
	return string(b)
}

func utf8ToLatin1(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xFF {
			fail("non Latin-1 character in a patched command")
		}
		b = append(b, byte(r))
	}
	return string(b)
}

// pyFormatG formats like Python's "{:g}" for the small positive delay values used here.
func pyFormatG(f float64) string { return strconv.FormatFloat(f, 'g', 6, 64) }

func isPyWS(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x1c, 0x1d, 0x1e, 0x1f, 0x85, 0xa0:
		return true
	}
	return false
}

func patchSlowmo(v string) string {
	u := latin1ToUTF8(v)
	out := reDelayedOff.ReplaceAllStringFunc(u, func(m string) string {
		sm := reDelayedOff.FindStringSubmatch(m)
		t, err := strconv.ParseFloat(sm[1], 64)
		if err != nil {
			fail("slow-mo: bad delay %q", sm[1])
		}
		s := strings.TrimRightFunc(m, isPyWS)
		if !strings.HasSuffix(s, ";") {
			s += ";"
		}
		return s + "delay " + pyFormatG(t+0.05) + " (" + relock + ");"
	})
	if out != u {
		return utf8ToLatin1(out)
	}
	out = reOff.ReplaceAllStringFunc(u, func(m string) string { return m + ";delay 0.05 (" + relock + ")" })
	return utf8ToLatin1(strings.ReplaceAll(out, ");;", ");"))
}

type slowmoEdit struct {
	key      editKey
	old, new string
}

// slowmoEdits computes the slow-mo edits from the PS3 objects (slowmo_patch.py).
func slowmoEdits(ps3 []wld.Object) []slowmoEdit {
	var out []slowmoEdit
	for i := range ps3 {
		o := &ps3[i]
		name := nameOf(o)
		for _, p := range o.Props {
			if p.Type != wld.TString && p.Type != wld.TCommand && p.Type != wld.TStringID {
				continue
			}
			k := editKey{o.Class, name, p.Name}
			if o.Class == "StartupCommand" && name == "StartupCommand00" && p.Name == "Command" {
				nv := p.S
				if !strings.HasSuffix(nv, ";") {
					nv += ";"
				}
				out = append(out, slowmoEdit{k, p.S, nv + relock + ";"})
			} else if reOff.MatchString(latin1ToUTF8(p.S)) {
				nv := patchSlowmo(p.S)
				// sanity (the recipe's check): one re-lock per "slowmo off"
				if a, b := len(reOff.FindAllString(latin1ToUTF8(nv), -1)), len(reRelock.FindAllString(latin1ToUTF8(nv), -1)); a != b {
					fail("slow-mo: %v has %d offs and %d re-locks", k, a, b)
				}
				out = append(out, slowmoEdit{k, p.S, nv})
			}
		}
	}
	if len(out) == 0 {
		fail("slow-mo: no edits found")
	}
	return out
}

// ------------------------------------------------------------------------------------------------- finale catch-up

// Finale catch-up for a Delta01 left behind before the finale. Values are the recipe's, whose catch-up point
// was checked against the collision data.
const (
	catchUpTrigger = "zSFODD_CatchUpTrigger"
	catchUpZone    = "zSFODD_CatchUpZone"
	catchUpPoint   = "zSFODD_CatchUpPoint"
	finalSeqCmdOld = "msg BackUpDeltaTrigger On;msg delta01 (follow none);msg delta01 (goalset none);msg delta01 (senses 0);" +
		"delay 0.1 (msg delta01 (goto Delta01Goto));msg finalDeltaSpawner spawn;"
	deltaDeadCmdOld = "msg TDMTrigger remove; msg TDMDialog remove;msg EndScaryElevatorDialog remove;msg TDM2Trigger remove; " +
		"msg TDM2Dialog remove;msg Delta3dialog01 remove;msg Delta3dialog02 remove;msg BackUpDeltaTrigger Unlock;"
	finalSeqCmdNew  = finalSeqCmdOld + "delay 2 (msg " + catchUpTrigger + " on);"
	deltaDeadCmdNew = deltaDeadCmdOld + "msg " + catchUpTrigger + " lock;"
	catchUpCommand  = "msg delta01 (teleport " + catchUpPoint + ");delay 0.1 (msg delta01 (goto Delta01Goto));"
)

func vec(v ...float32) []uint32 {
	out := make([]uint32, len(v))
	for i, f := range v {
		out[i] = math.Float32bits(f)
	}
	return out
}

// setProp sets the first property called name, keeping its type (the recipe's setp). t is the expected
// type: string-like (0/7/8) for s, vector-like (1/2/6) with the same component count for n.
func setProp(o *wld.Object, name string, t uint32, s string, n []uint32) string {
	i := o.PropIndex(name)
	if i < 0 {
		fail("%s.%s missing", nameOf(o), name)
	}
	p := &o.Props[i]
	isStr := p.Type == wld.TString || p.Type == wld.TCommand || p.Type == wld.TStringID
	old := p.S
	if n != nil {
		if isStr || len(n) != len(p.N) {
			fail("%s.%s is not a %d-component vector", nameOf(o), name, len(n))
		}
		p.N = n
	} else {
		if !isStr {
			fail("%s.%s is not a string", nameOf(o), name)
		}
		p.S = s
	}
	_ = t
	return old
}

func applyFinaleCatchUp(objs []wld.Object) []wld.Object {
	if old := setProp(getObj(objs, "FinalSequenceTrigger"), "Command1", wld.TCommand, finalSeqCmdNew, nil); old != finalSeqCmdOld {
		fail("finale catch-up: FinalSequenceTrigger.Command1 is %q", old)
	}
	ddt := getObj(objs, "DeltaDeadTrigger")
	if old := setProp(ddt, "Command1", wld.TCommand, deltaDeadCmdNew, nil); old != deltaDeadCmdOld {
		fail("finale catch-up: DeltaDeadTrigger.Command1 is %q", old)
	}
	for i := range objs {
		n := latin1Lower(nameOf(&objs[i]))
		for _, x := range []string{catchUpTrigger, catchUpZone, catchUpPoint} {
			if n == latin1Lower(x) {
				fail("finale catch-up: %s already exists", x)
			}
		}
	}
	// rotation (0, sin 45, 0, sin 45): yaw +90, faces +X toward the finale room. sin(45 deg) as float32.
	s45 := float32(math.Sin(45 * (math.Pi / 180)))
	if math.Float32bits(s45) != 0x3F3504F3 {
		fail("finale catch-up: sin(45) rounding differs from the recipe")
	}
	trig := ddt.Clone() // plain Trigger, not touchable
	setProp(&trig, "Name", wld.TString, catchUpTrigger, nil)
	setProp(&trig, "Pos", wld.TVector, "", vec(12664, 2726, -4400)) // beside BackUpDeltaTrigger; not touchable
	setProp(&trig, "Command1", wld.TCommand, catchUpCommand, nil)
	setProp(&trig, "Command2", wld.TCommand, "", nil)
	zone := getObj(objs, "Trigger_HallMonitorSignal").Clone() // stock AI-named trigger
	setProp(&zone, "Name", wld.TString, catchUpZone, nil)
	setProp(&zone, "Pos", wld.TVector, "", vec(10300, 2768, -3600)) // corridor and finale room box
	setProp(&zone, "Dims", wld.TVector, "", vec(1500, 150, 750))
	setProp(&zone, "AITriggerName", wld.TString, "Delta01", nil)
	setProp(&zone, "Command1", wld.TCommand, "msg "+catchUpTrigger+" lock;", nil)
	point := getObj(objs, "FinalDeltaTeleportPoint").Clone()
	setProp(&point, "Name", wld.TString, catchUpPoint, nil)
	setProp(&point, "Pos", wld.TVector, "", vec(9470, 2716, -3152)) // floor 2666, ceiling 3016, wall 176 (recipe check)
	setProp(&point, "Rotation", wld.TRot, "", vec(0, s45, 0, s45))
	return append(objs, trig, zone, point)
}
