// Package iaworld builds the eight Instant Action map files of the mod,
//
//	Worlds/Release/IA_{Distribution,Rooftop,Construction,Vault}.{World00p,Objects00p}
//
// from the player's own Xbox 360 copy (and, for Vault's two Watson pickups, the PlayStation 3 copy when it is
// there) plus the stock PC game. It is a Go port of the project's Python map builder (build_maps.py) with its world
// converter, classdef reader, countdown builder and world rules, which produced the in-game confirmed files:
//
//  1. Each X360 InstantAction<n>.wld/.wldclnt/.wldsrvr is converted to a PC .World00p (Xbox 360 packed normals
//     are 10:10:10). PC class schemas come from stock WTF_Exfil plus the ClassDef tables of the player's
//     GameServer.dll; PC-only property values from WTF_Exfil's objects of the same name.
//  2. Before conversion the recipe's object edits run: the X360 machine-pistol pickups become the mod's SM15
//     (zSFODD_Machine Pistol); with a PS3 copy, Vault's PS3 Watson pickups (zSFODD_AutoShotgun) take two X360
//     slots; the Rooftop, Construction and Distribution voice loops, the starting weapon commands, the Rooftop
//     gates, the Fettel/voice path fixes.
//  3. The Objects00p override = the world's objects with the world rules (Hind attachments, results triggers
//     77110-77113) and the stock PC countdown (IA_Countdown, IA_StartClock 77101-77104).
//
// Resource names resolve against the stock archives (in Default.archcfg mount order) and the mod's own file
// list (modfiles.txt, names only): the recipe indexed every mounted folder, so mod files the X360 worlds name
// (SM15 models/skins/icons, player_watson, WatsonSAS, black.dds) change the output when they are absent.
//
// No game data is embedded: only the recipe's fix decisions (names, commands, counts) and the mod file names.
package iaworld

import (
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/sfodworld"
	"fearinstaller/internal/wld"
)

// Labels are the four maps in the recipe's order (X360 InstantAction1..4).
var Labels = []string{"Distribution", "Rooftop", "Construction", "Vault"}

// WorldPath and ObjectsPath are the mod-relative output paths of a map.
func WorldPath(label string) string   { return "Worlds/Release/IA_" + label + ".World00p" }
func ObjectsPath(label string) string { return "Worlds/Release/IA_" + label + ".Objects00p" }

// LiveSHA256: the in-game confirmed files (built with a PS3 copy present: Vault has the Watson pickups).
var LiveSHA256 = map[string]string{
	"Worlds/Release/IA_Distribution.World00p":   "f479fe5d0556842930a52af31e0501db0462cf99392b2b218b405fc55b3f8958",
	"Worlds/Release/IA_Distribution.Objects00p": "a6db4860f5384ce30060262e44221a5e52fb1f7e0edf9a542401e3fda46c8a98",
	"Worlds/Release/IA_Rooftop.World00p":        "5741233593c55fc4d3c3e2826c2c1263345d6ee888f0d44cbb2639f7d0d4cc5c",
	"Worlds/Release/IA_Rooftop.Objects00p":      "7341ab00dc5a2582fffbcb9a355d1c5bd8edac82956dc2e5f82c8a034175c190",
	"Worlds/Release/IA_Construction.World00p":   "aa6ec558b5e387327a13039550e53480b4e678d2993c174c9afd97d769304cf6",
	"Worlds/Release/IA_Construction.Objects00p": "79ce100c94ad6033c4460e83c5a92a26f3d97bb417555f811e100f68d4735585",
	"Worlds/Release/IA_Vault.World00p":          "120f1e046e9da793834a77ae576092f11f0641ae5fa11197ada599292ebbeb66",
	"Worlds/Release/IA_Vault.Objects00p":        "189ac83cdc2cd0e31749814b03dd8dc35a0adbcc56e6b812ee43d0e5a8cd305c",
}

// XboxOnlySHA256: the recipe's output without a PS3 copy (its Xbox-only mode, same mod file list). Only Vault
// differs from the confirmed files (11 SM15 pickups, the stock shotgun). Not installed or tested in game.
var XboxOnlySHA256 = map[string]string{
	"Worlds/Release/IA_Vault.World00p":   "f231dee86a3e35378153e1a8f1a40cd92a920f046445549e9202ef03c89060bd",
	"Worlds/Release/IA_Vault.Objects00p": "8e98e7f47df1a35d33c33d23cc60e31b47d78211f7f186a32b43def04d1e94a8",
}

// modfiles.txt: the files of the complete mod's folder (185 names, backslash separated).
//
//go:embed modfiles.txt
var modFilesTxt string

// ModFiles returns the embedded list of mod file names.
func ModFiles() []string {
	var out []string
	for _, l := range strings.Split(modFilesTxt, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// MapFiles are one X360 map's world files.
type MapFiles struct{ Wld, Clnt, Srvr []byte }

// Inputs holds everything Build reads.
type Inputs struct {
	X360       [4]MapFiles             // Game/Worlds/Release/InstantAction1..4 (Labels order)
	PS3Vault   []byte                  // PS3 worlds/release/instantaction4.wldsrvr; nil = Xbox-only build
	Exfil      []byte                  // stock PC Worlds\Release\WTF_Exfil.World00p
	GameServer []byte                  // stock PC GameServer.dll (the archive copy that wins the mount order)
	Listing    []sfodworld.ArchiveFile // every file of the stock PC archives (sfodworld.ListArchives)
	ModFiles   []string                // the mod's file names; nil = the embedded ModFiles()
}

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
			return "", fmt.Errorf("iaworld: %s not found in %s", part, cur)
		}
		cur = filepath.Join(cur, found)
	}
	return cur, nil
}

// LoadInputs reads the inputs. x360Dir is the Xbox 360 disc root (holding "Game") or its Game folder;
// ps3Dir is the PS3 "PS3_GAME/USRDIR/game" folder or "" for an Xbox-only build (a ps3Dir without
// worlds/release/instantaction4.wldsrvr is an error, not a silent Xbox-only build); fs the opened stock PC
// archives (pcfs.Open of the game folder).
func LoadInputs(x360Dir, ps3Dir string, fs *pcfs.FS) (*Inputs, error) {
	if fs == nil {
		return nil, errors.New("iaworld: no PC game")
	}
	in := &Inputs{}
	rel, err := findCI(x360Dir, "Game", "Worlds", "Release")
	if err != nil {
		if rel, err = findCI(x360Dir, "Worlds", "Release"); err != nil {
			return nil, fmt.Errorf("iaworld: Xbox 360 Worlds\\Release folder: %w", err)
		}
	}
	for i := range Labels {
		for _, f := range []struct {
			dst *[]byte
			ext string
		}{{&in.X360[i].Wld, "wld"}, {&in.X360[i].Clnt, "wldclnt"}, {&in.X360[i].Srvr, "wldsrvr"}} {
			p, err := findCI(rel, fmt.Sprintf("InstantAction%d.%s", i+1, f.ext))
			if err != nil {
				return nil, err
			}
			if *f.dst, err = os.ReadFile(p); err != nil {
				return nil, err
			}
		}
	}
	if ps3Dir != "" {
		p, err := findCI(ps3Dir, "worlds", "release", "instantaction4.wldsrvr")
		if err != nil {
			return nil, fmt.Errorf("iaworld: PS3 Vault objects: %w", err)
		}
		if in.PS3Vault, err = os.ReadFile(p); err != nil {
			return nil, err
		}
	}
	if in.Exfil, err = fs.Read(`Worlds\Release\WTF_Exfil.World00p`); err != nil {
		return nil, err
	}
	if in.GameServer, err = fs.Read(`GameServer.dll`); err != nil {
		return nil, err
	}
	if in.Listing, err = sfodworld.ListArchives(fs.Dir); err != nil {
		return nil, err
	}
	return in, nil
}

// Build returns the eight files, keyed by WorldPath / ObjectsPath.
func Build(in *Inputs) (map[string][]byte, error) {
	out, _, err := build(in, nil)
	return out, err
}

// BuildReport is Build plus the mod file names the build actually used (paths a world names that only the
// mod provides, or whose spelling the mod sets): the output changes if one of them is missing from ModFiles.
func BuildReport(in *Inputs) (map[string][]byte, []string, error) { return build(in, nil) }

// Stage names passed to build's stage callback (per map: label + "/" + stage).
const (
	stageWorld   = "World00p"
	stageObjects = "Objects00p"
)

// errFormat-carrying panics from the walkers and the recipe's assertions become errors here.
func build(in *Inputs, stage func(name string, b []byte)) (out map[string][]byte, used []string, err error) {
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
			out, used, err = nil, nil, errors.New("iaworld: "+e.msg)
		}
	}()
	if in == nil {
		return nil, nil, errors.New("iaworld: no inputs")
	}
	for i, m := range in.X360 {
		if len(m.Wld) == 0 || len(m.Clnt) == 0 || len(m.Srvr) == 0 {
			return nil, nil, fmt.Errorf("iaworld: Xbox 360 InstantAction%d world files missing", i+1)
		}
	}
	if len(in.Exfil) == 0 || len(in.GameServer) == 0 || len(in.Listing) == 0 {
		return nil, nil, errors.New("iaworld: stock PC WTF_Exfil, GameServer.dll or archive listing missing")
	}
	mod := in.ModFiles
	if mod == nil {
		mod = ModFiles()
	}
	files := newPCFiles(in.Listing, mod)
	exfil, _, _, perr := wld.ParsePCWorld(in.Exfil)
	if perr != nil {
		fail("WTF_Exfil objects: %v", perr)
	}
	schema := schemaFromWorld(exfil)
	addClassDefs(schema, loadClassDefs(in.GameServer))
	ref := refProps(exfil)
	reqBase := renderRequirements(in.Exfil)
	// Rooftop and Construction add COLOR0 for four city materials; the recipe adds them to its shared table
	// while building Rooftop, so Construction and Vault are converted with them too.
	reqMore := map[string]map[sem]bool{}
	for k, v := range reqBase {
		reqMore[k] = v
	}
	for _, m := range []string{`materials\city\buildings\build_int01.mat00`, `prefabs\city\buildings\bldg_tarpaper.mat00`,
		`prefabs\city\buildings\bldg_facade_01.mat00`, `prefabs\city\buildings\bldg_facade_02.mat00`} {
		s := map[sem]bool{{10, 0}: true}
		for x := range reqBase[m] {
			s[x] = true
		}
		reqMore[m] = s
	}
	var ps3 []wld.Object
	if in.PS3Vault != nil {
		_, objs, err := wld.ParseConsoleServer(in.PS3Vault)
		if err != nil {
			fail("PS3 instantaction4.wldsrvr: %v", err)
		}
		ps3 = objs
	}
	out = map[string][]byte{}
	for i, label := range Labels {
		m := in.X360[i]
		_, objs, err := wld.ParseConsoleServer(m.Srvr)
		if err != nil {
			fail("Xbox 360 InstantAction%d.wldsrvr: %v", i+1, err)
		}
		watson := ps3 != nil && label == "Vault"
		expected, wpos := editObjects(label, objs, ps3, watson)
		req := reqBase
		if i >= 1 {
			req = reqMore
		}
		cobj := convertObjects(objs, schema, ref, files)
		core, blindOfs := convertCore(m.Wld, func(p []assetPair) []byte { return buildAssetListPC(p, files) })
		render, sec1 := convertRender(m.Clnt, req)
		blind := convertBlind(m.Wld, blindOfs)
		osec := wld.SerializeObjects(cobj)
		o0 := 20 + len(core)
		o1 := o0 + len(render)
		o2 := o1 + len(sec1)
		o3 := o2 + len(osec)
		w := make([]byte, 0, o3+len(blind))
		for _, v := range []int{0x71, o0, o1, o2, o3} {
			w = binary.LittleEndian.AppendUint32(w, uint32(v))
		}
		w = append(append(append(append(append(w, core...), render...), sec1...), osec...), blind...)
		checkWorld(label, w, o0, o1, o3, expected, wpos, watson)
		checkWorldBox(label, w)
		if stage != nil {
			stage(label+"/"+stageWorld, w)
		}
		out[WorldPath(label)] = w
		ob := buildObjects00p(label, w, exfil)
		if stage != nil {
			stage(label+"/"+stageObjects, ob)
		}
		out[ObjectsPath(label)] = ob
	}
	for k := range files.hits {
		used = append(used, files.idx[k])
	}
	sort.Strings(used)
	return out, used, nil
}

type posKey [3]float32 // Python tuple equality: -0.0 == 0.0, NaN never equal (Go map keys behave the same)

func posOf(o *wld.Object) posKey {
	p, ok := fieldOf(o, "Pos")
	if !ok || len(p.N) != 3 {
		fail("%s %s has no Pos", o.Class, firstName(o))
	}
	var k posKey
	for i := range k {
		k[i] = float32frombits(p.N[i])
	}
	return k
}

// The recipe's assertions on the finished world (object counts, pickups, exits, loader re-read).
var (
	wantObjects = map[string]int{"Distribution": 1288, "Rooftop": 2108, "Construction": 2034, "Vault": 1650}
	wantSM15    = map[string]int{"Distribution": 6, "Rooftop": 6, "Construction": 8, "Vault": 11}
)

func checkWorld(label string, w []byte, o0, o1, o3 int, expected, wpos map[posKey]bool, watson bool) {
	if readRender(w, o0, binary.LittleEndian).end != o1 {
		fail("%s: the PC render block does not re-read", label)
	}
	oo, end, err := wld.ParseObjects(w, int(binary.LittleEndian.Uint32(w[12:])), false)
	if err != nil || end != o3 {
		fail("%s: the PC object section does not re-read", label)
	}
	wantSM, wantW := wantSM15[label], 0
	if watson {
		wantSM, wantW = 10, 2
	}
	if len(oo) != wantObjects[label] || len(expected) != wantSM || len(wpos) != wantW {
		fail("%s: %d objects, %d SM15 and %d Watson pickups", label, len(oo), len(expected), len(wpos))
	}
	sm, wat := map[posKey]bool{}, map[posKey]bool{}
	exit := false
	for i := range oo {
		o := &oo[i]
		if v, ok := strField(o, "WeaponType"); ok && v == sm15Type {
			sm[posOf(o)] = true
		} else if ok && v == watsonType {
			wat[posOf(o)] = true
		}
		for _, p := range o.Props {
			if strings.Contains(latin1Lower(p.Name), "exitlevel") || isStrType(p.Type) && strings.Contains(latin1Lower(p.S), "exitlevel") {
				exit = true
			}
		}
	}
	if !sameSet(sm, expected) || !sameSet(wat, wpos) {
		fail("%s: SM15/Watson pickups moved in conversion", label)
	}
	if !exit {
		fail("%s: no exitlevel command", label)
	}
}

func sameSet(a, b map[posKey]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
