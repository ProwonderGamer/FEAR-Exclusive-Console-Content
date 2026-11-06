// Package holworld builds the Holiday mission world "Worlds\Release\Holiday.World00p" from the player's own
// Xbox 360 copy and the stock PC game. It is a Go port of the project's Python Holiday world builder
// (build) with its converter hol_world2pc.py, which produced the confirmed file (SHA-256 1c2d893d...):
//
//  1. hol_world2pc.convert_world(platform 'x360'): the SFOD-D converter (copied from internal/sfodworld) with
//     Xbox 360 DEC3N packed normals checked against the geometry (normals.go), .xsb as a console-only type, the
//     render vertex inputs of five PC worlds plus the shader-family table (vtxinputs.go), and the GameServer.dll
//     ClassDef schema for the classes no PC world has (schema.go).
//  2. The object hook: the 11 world patches (patches.go, exact match), then the AINode Dependency fix.
//
// Inputs (all read, nothing written):
//
//	X360  Game/Worlds/Release/Holiday.wld, .wldclnt, .wldsrvr, .matCat
//	PC    Worlds\Release\{Aftermath,Moody,Admin,ATC_Roof,Mapes_Elevator}.World00p, GameServer.dll and the file
//	      listing of every stock *.Arch00 (which resources exist on PC, and their spelling)
//
// No PS3 data is used (the recipe also read five PS3 material catalogs; they never change the output). The only
// embedded data are the recipe's own decisions: the patch list and the names of the 88 resources the Holiday
// content adds (holiday_world_patches.json, holiday_assets.json; names and command strings only).
package holworld

import (
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/sfodworld"
	"fearinstaller/internal/wld"
)

// OutputPath is the mod-relative path of the built world.
const OutputPath = "Worlds/Release/Holiday.World00p"

// LiveSHA256 is the hash of the confirmed world (24,741,664 bytes).
const LiveSHA256 = "1c2d893d60eaacd31bfd472fe26cedf84038ec1bc076ad562688872dee6339db"

// PCWorldNames are the stock PC worlds the converter reads (make_holiday_world PC_WORLD_NAMES): class schemas and
// render vertex inputs from all five; Admin is also the reference level (property values, precache entries).
var PCWorldNames = []string{"Aftermath", "Moody", "Admin", "ATC_Roof", "Mapes_Elevator"}

// holiday_assets.json: the PC paths of the X360 content the Holiday port adds (tree 'build') or replaces under
// a stock name ('optional_x360_diff').
//
//go:embed holiday_assets.json
var assetsJSON []byte

// ExtraFiles returns the 88 resource paths the converter treats as present on PC, in the recipe's order
// (the files the Holiday port adds: the 'build' files first, then the others). They join the PC file index; where a name differs
// from a stock one only in case, this spelling wins.
func ExtraFiles() []string {
	var h struct {
		Files []struct {
			PCPath string `json:"pc_path"`
			Tree   string `json:"tree"`
		} `json:"files"`
	}
	if err := json.Unmarshal(assetsJSON, &h); err != nil {
		panic("holworld: embedded holiday_assets.json: " + err.Error())
	}
	var build, opt []string
	for _, f := range h.Files {
		if f.Tree == "build" {
			build = append(build, f.PCPath)
		} else {
			opt = append(opt, f.PCPath)
		}
	}
	return append(build, opt...)
}

// Inputs holds everything Build reads.
type Inputs struct {
	Wld, Clnt, Srvr, MatCat []byte                  // X360 Game/Worlds/Release/Holiday.wld, .wldclnt, .wldsrvr, .matCat
	PCWorlds                map[string][]byte       // PCWorldNames -> stock Worlds\Release\<name>.World00p
	GameServer              []byte                  // stock PC GameServer.dll (the copy that wins the mount order)
	Listing                 []sfodworld.ArchiveFile // every file of the stock PC archives (sfodworld.ListArchives)
	ExtraFiles              []string                // nil: the embedded ExtraFiles()
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
			return "", fmt.Errorf("holworld: %s not found in %s", part, cur)
		}
		cur = filepath.Join(cur, found)
	}
	return cur, nil
}

// LoadInputs reads the inputs. x360Dir is the Xbox 360 disc root (holding "Game") or its Game folder; fs the
// opened stock PC archives (pcfs.Open of the game folder; its archives are listed again with ListArchives).
func LoadInputs(x360Dir string, fs *pcfs.FS) (*Inputs, error) {
	if fs == nil {
		return nil, errors.New("holworld: no PC game")
	}
	rel, err := findCI(x360Dir, "Game", "Worlds", "Release")
	if err != nil {
		if rel, err = findCI(x360Dir, "Worlds", "Release"); err != nil {
			return nil, fmt.Errorf("holworld: Xbox 360 Worlds\\Release folder: %w", err)
		}
	}
	in := &Inputs{PCWorlds: map[string][]byte{}}
	for _, f := range []struct {
		dst *[]byte
		ext string
	}{{&in.Wld, "wld"}, {&in.Clnt, "wldclnt"}, {&in.Srvr, "wldsrvr"}, {&in.MatCat, "matCat"}} {
		p, err := findCI(rel, "Holiday."+f.ext)
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
	if in.GameServer, err = fs.Read(`GameServer.dll`); err != nil {
		return nil, err
	}
	if in.Listing, err = sfodworld.ListArchives(fs.Dir); err != nil {
		return nil, err
	}
	return in, nil
}

// Build returns the finished Holiday.World00p.
func Build(in *Inputs) ([]byte, error) { return build(in, nil) }

// Stage names passed to build's stage callback.
const (
	stageUnpatched = "unpatched" // converter output before the object hook
	stageFinal     = "final"
)

// buildInfo collects what the recipe decided (shader-family table, ClassDef classes, cleared dependencies)
// and optionally receives the world before and after the object hook.
type buildInfo struct {
	stage         func(name string, b []byte)
	extraRequire  map[string]map[sem]bool
	classDefAdded []string
	depCleared    int
	patches       int
}

func build(in *Inputs, info *buildInfo) (out []byte, err error) {
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
			out, err = nil, errors.New("holworld: "+e.msg)
		}
	}()
	if in == nil || len(in.Wld) == 0 || len(in.Clnt) == 0 || len(in.Srvr) == 0 || len(in.MatCat) == 0 {
		return nil, errors.New("holworld: Xbox 360 Holiday world files missing")
	}
	for _, n := range PCWorldNames {
		if len(in.PCWorlds[n]) == 0 {
			return nil, fmt.Errorf("holworld: PC world %s missing", n)
		}
	}
	if len(in.GameServer) == 0 || len(in.Listing) == 0 {
		return nil, errors.New("holworld: stock PC GameServer.dll or archive listing missing")
	}
	if info == nil {
		info = &buildInfo{}
	}
	extra := in.ExtraFiles
	if extra == nil {
		extra = ExtraFiles()
	}
	files := newPCFiles(in.Listing, extra)
	admin := in.PCWorlds["Admin"]

	// core with the precache list: X360 pairs, then PC Admin's own (convert_world pc_refs[:1])
	refPairs := pcAssetList(admin)
	core, blindOfs := convertCore(in.Wld, func(p []assetPair) []byte {
		return buildAssetListPC(append(append([]assetPair(nil), p...), refPairs...), files)
	})

	// render vertex inputs: union over the five PC worlds (+ Admin again as the reference level), then the
	// shader-family table (make_holiday_world.build: extra_require)
	req := map[string]map[sem]bool{}
	merge := func(add map[string]map[sem]bool) {
		for k, v := range add {
			s := req[k]
			if s == nil {
				s = map[sem]bool{}
				req[k] = s
			}
			for x := range v {
				s[x] = true
			}
		}
	}
	for _, n := range PCWorldNames {
		merge(renderRequirements(in.PCWorlds[n]))
	}
	info.extraRequire = shaderFamilyRequirements(holidayFeed(in.Clnt), req, consoleShaders(in.MatCat))
	merge(info.extraRequire)
	render, sec1 := convertRender(in.Clnt, req)

	// objects: schema from the five PC worlds plus the GameServer ClassDefs, values from PC Admin
	var worlds [][]wld.Object
	for _, n := range PCWorldNames {
		worlds = append(worlds, pcObjects(in.PCWorlds[n]))
	}
	schema := schemaFromWorlds(worlds)
	_, xobjs, perr := wld.ParseConsoleServer(in.Srvr)
	if perr != nil {
		fail("Xbox 360 Holiday.wldsrvr: %v", perr)
	}
	cdefs := classDefIndex(loadClassDefs(in.GameServer))
	info.classDefAdded = applyClassDefs(schema, cdefs, xobjs)
	ref := refProps(worlds[2]) // Admin
	objs := convertObjects(xobjs, schema, ref, files)
	blind := convertBlind(in.Wld, blindOfs)
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
	if info.stage != nil {
		info.stage(stageUnpatched, assemble(objs))
	}

	// object hook: the world patches, then the AINode Dependency fix
	patches := loadPatches()
	applyPatches(objs, patches, rawStrings(xobjs))
	info.patches = len(patches)
	info.depCleared = fixAINodeDependencies(objs, smartObjectClasses(cdefs))
	out = assemble(objs)
	if info.stage != nil {
		info.stage(stageFinal, out)
	}
	return out, nil
}
