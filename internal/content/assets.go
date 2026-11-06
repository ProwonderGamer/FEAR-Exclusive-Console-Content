package content

import (
	"fmt"
	"os"
	"strings"

	"fearinstaller/internal/anmtree"
	"fearinstaller/internal/catalog"
	"fearinstaller/internal/consolefont"
	"fearinstaller/internal/dbmerge"
	"fearinstaller/internal/holworld"
	"fearinstaller/internal/hudicon"
	"fearinstaller/internal/mat"
	"fearinstaller/internal/model"
	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/sfodworld"
	"fearinstaller/internal/tex"
)

// The "assets" stage: textures, materials, the console font and the models, converted from the console
// uberCatalogs (and, for player_console, the stock PC player), plus the animation trees and the two bonus worlds,
// built from loose console files and the stock PC archives. Every recipe here is proven byte-exact against the
// in-game confirmed file by the golden tests of its package (internal/tex, mat, consolefont, model, anmtree,
// sfodworld, holworld) and by TestAssetsMatchConfirmed.

// src is a set of console copies.
type src int

const (
	srcPS3 src = 1 << iota
	srcX360
)

// copies names the console copies in the set for a message: "the PlayStation 3 copy", or, when either one
// would do, "the Xbox 360 or the PlayStation 3 copy".
func (s src) copies() string {
	switch s {
	case srcPS3:
		return "the PlayStation 3 copy"
	case srcX360:
		return "the Xbox 360 copy"
	}
	return "the Xbox 360 or the PlayStation 3 copy"
}

// assetIn is what a conversion reads: the console catalog it was given and the stock PC archives.
type assetIn struct {
	cat   func() (*catalog.Catalog, error) // opens (once) the chosen console's uberCatalog; nil for PC-only assets
	isPS3 bool                             // the chosen console copy is the PlayStation 3 one
	root  string                           // the chosen console's data folder: PS3 ".../USRDIR/game", Xbox 360 "<disc>/Game"
	pc    *pcfs.FS
}

// readLoose returns a loose console file (outside the uberCatalog) under the chosen console's data folder.
func (a assetIn) readLoose(rel string) ([]byte, error) {
	p, err := FindCI(a.root, rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// read returns a catalog entry's bytes and its name as stored (model.Convert needs the stored spelling).
func (a assetIn) read(path string) ([]byte, string, error) {
	if a.cat == nil {
		return nil, "", fmt.Errorf("%s: no console copy chosen", path)
	}
	k, err := a.cat()
	if err != nil {
		return nil, "", err
	}
	e, ok := k.Lookup(path)
	if !ok {
		return nil, "", fmt.Errorf("%s is not in the console catalog", path)
	}
	b, err := k.Read(e)
	return b, e.Name, err
}

type asset struct {
	rel  string // mod layout path, '/' separated (a row of coverage_table.go)
	from src    // the console copies it can be built from; 0 = the PC game alone. X360 is used when both are there.
	path string // console catalog entry, or for loose-file assets the main input under the console's data folder
	conv func(a assetIn, path string) ([]byte, error)
}

// playerWatson: the Watson first-person body (PS3): convert, reload fix (left hand fly-off), reload cue split and
// left-hand grip on the converted player_Deltaforce body. Golden: model.TestPlayerWatsonGolden.
func playerWatson(a assetIn, path string) ([]byte, error) {
	w, wn, err := a.read(path)
	if err != nil {
		return nil, err
	}
	body, bn, err := a.read(`chars\models\player_Deltaforce.mdl`)
	if err != nil {
		return nil, err
	}
	return model.PlayerWatson(w, wn, body, bn)
}

// playerUpperSFODD: the SFOD-D upper-body animation tree from the stock PC Player_Upper tree and the PS3
// Player_Upper tree (the Xbox 360 one is a different tree). Golden: anmtree.TestPlayerUpperSFODDGolden.
func playerUpperSFODD(a assetIn, path string) ([]byte, error) {
	container, err := a.readLoose(path)
	if err != nil {
		return nil, err
	}
	ps, err := anmtree.PS3PlayerUpper(container)
	if err != nil {
		return nil, err
	}
	pc, err := a.pc.Read(`AnimationDatabase\AnimTrees\Player_Upper.AnmTree00p`)
	if err != nil {
		return nil, err
	}
	return anmtree.BuildPlayerUpperSFODD(pc, ps)
}

// soldierHoliday: Holiday's soldier tree from the console Soldier tree (byte-identical on both consoles) and the
// prop names of the player's own stock GameServer.dll. second = Soldier_Holiday2 (runback3 -> walkback).
// Golden: anmtree.TestSoldierHolidayGolden.
func soldierHoliday(second bool) func(assetIn, string) ([]byte, error) {
	return func(a assetIn, path string) ([]byte, error) {
		container, err := a.readLoose(path)
		if err != nil {
			return nil, err
		}
		so, err := anmtree.Find(container, "Soldier")
		if err != nil {
			return nil, err
		}
		dll, err := a.pc.Read(`GameServer.dll`)
		if err != nil {
			return nil, err
		}
		props, err := anmtree.PCAnimPropEnum(dll)
		if err != nil {
			return nil, err
		}
		h, err := anmtree.BuildSoldierHoliday(so, props)
		if err != nil || !second {
			return h, err
		}
		return anmtree.BuildSoldierHoliday2(h)
	}
}

// sfodWorld: the SFOD-D world (PS3 sfod_d.wld/.wldclnt/.wldsrvr + stock PC worlds and archive listing), recipe
// chain (conversion, death lure, intro delay, finale catch-up). Golden: sfodworld.TestGoldenSFODWorld.
func sfodWorld(a assetIn, _ string) ([]byte, error) {
	in, err := sfodworld.LoadInputs(a.root, a.pc)
	if err != nil {
		return nil, err
	}
	return sfodworld.Build(in)
}

// holidayWorld: the Holiday world (Xbox 360 Holiday.wld/.wldclnt/.wldsrvr/.matCat + stock PC worlds,
// GameServer.dll and archive listing), the project's Python Holiday world builder with the recovered world patches.
// Golden: holworld.TestGoldenHolidayWorld.
func holidayWorld(a assetIn, _ string) ([]byte, error) {
	in, err := holworld.LoadInputs(a.root, a.pc)
	if err != nil {
		return nil, err
	}
	return holworld.Build(in)
}

// texture conversions, by the console the source comes from
func texWith(f func([]byte) ([]byte, error)) func(assetIn, string) ([]byte, error) {
	return func(a assetIn, path string) ([]byte, error) {
		b, _, err := a.read(path)
		if err != nil {
			return nil, err
		}
		return f(b)
	}
}

var (
	x360Tex    = texWith(tex.FromX360)
	x360Normal = texWith(tex.FromX360Normal)
	ps3Full    = texWith(func(b []byte) ([]byte, error) { return tex.FromPS3ARGB(b, true) })
	ps3Normal  = texWith(tex.FromPS3Normal)
	ps3DXT     = texWith(tex.FromPS3DXT)
	matConv    = texWith(mat.Convert)
)

// either: the same output from either console (golden-tested from both). X360 is preferred.
func either(x360, ps3 func(assetIn, string) ([]byte, error)) func(assetIn, string) ([]byte, error) {
	return func(a assetIn, path string) ([]byte, error) {
		if a.isPS3 {
			return ps3(a, path)
		}
		return x360(a, path)
	}
}

// hudIcon: a HUD weapon icon rebuilt from that console's icon art (HUD icon recipe, hudicon.Build; golden:
// hudicon.TestGolden, byte-exact). The SM15 icons come from the Xbox 360 art, the Watson icons from the PS3 art:
// the other console's art is the same picture compressed differently and gives other bytes.
func hudIcon(a assetIn, path string) ([]byte, error) {
	b, _, err := a.read(path)
	if err != nil {
		return nil, err
	}
	return hudicon.Build(b, a.isPS3)
}

// ps3Stored keeps the stored levels only (load screen, calibration image).
var ps3Stored = texWith(func(b []byte) ([]byte, error) { return tex.FromPS3ARGB(b, false) })

// convDEC3N converts an Xbox 360 model that stores DEC3N normals (model.X360Direct; golden:
// model.TestX360DirectGolden). Its PS3 copy gives different bytes, so these rows are Xbox 360 only.
func convDEC3N(a assetIn, path string) ([]byte, error) {
	b, name, err := a.read(path)
	if err != nil {
		return nil, err
	}
	out, _, err := model.Convert(b, name, model.Options{DEC3N: true})
	return out, err
}

// playerHoliday: Holiday's first-person body (X360): DEC3N convert, the Watson child (the project's Python script) and
// the PC left-hand socket. Golden: model.TestPlayerHolidayGolden.
func playerHoliday(a assetIn, path string) ([]byte, error) {
	b, name, err := a.read(path)
	if err != nil {
		return nil, err
	}
	pl, err := stockPlayer(a)
	if err != nil {
		return nil, err
	}
	return model.PlayerHoliday(b, name, pl)
}

// sm15Left: the left SM15 (X360): DEC3N convert, then the left-hand bake from the console socket (X360
// player_Deltaforce.mdl) to the PC socket. Golden: model.TestSM15LeftGolden.
func sm15Left(a assetIn, path string) ([]byte, error) {
	b, name, err := a.read(path)
	if err != nil {
		return nil, err
	}
	body, _, err := a.read(`chars\models\player_Deltaforce.mdl`)
	if err != nil {
		return nil, err
	}
	pl, err := stockPlayer(a)
	if err != nil {
		return nil, err
	}
	return model.SM15Left(b, name, body, pl)
}

func convModel(a assetIn, path string) ([]byte, error) {
	b, name, err := a.read(path)
	if err != nil {
		return nil, err
	}
	out, _, err := model.Convert(b, name, model.Options{})
	return out, err
}

// stockPlayer is the stock PC player model; pcfs returns the newest archive's copy (FEAR_7/FEAR_3), the only one
// that gives player_console (FEAR.Arch00 holds an older, different file).
func stockPlayer(a assetIn) ([]byte, error) {
	return a.pc.Read(`chars\models\player.Model00p`)
}

var assets = []asset{
	// SM15 (Xbox 360; the PS3 SM15 textures differ from the confirmed files)
	{"guns/skins_pv/SM15_Machine_Pistol_D.dds", srcX360, `guns\skins_pv\SM15_Machine_Pistol_D.tex`, x360Tex},
	{"guns/skins_pv/SM15_Machine_Pistol_S.dds", srcX360, `guns\skins_pv\SM15_Machine_Pistol_S.tex`, x360Tex},
	{"guns/skins_pv/SM15_Machine_Pistol_N.dds", srcX360, `guns\skins_pv\SM15_Machine_Pistol_N.tex`, x360Normal},
	{"guns/Materials/SM15_Machine_Pistol.Mat00", srcX360 | srcPS3, `guns\Materials\SM15_Machine_Pistol.mat`, matConv},
	{"chars/models/player_SM15.Model00p", srcX360 | srcPS3, `chars\models\player_SM15.mdl`, convModel},
	{"chars/models/player_dualSM15.Model00p", srcX360 | srcPS3, `chars\models\player_dualSM15.mdl`, convModel},
	{"guns/models_hh/SM15machinePistol.Model00p", srcX360, `guns\models_hh\SM15machinePistol.mdl`, convDEC3N},
	{"guns/models_hh/SM15_Right.Model00p", srcX360, `guns\models_hh\SM15_Right.mdl`, convDEC3N},
	{"guns/models_hh/SM15_Left.Model00p", srcX360, `guns\models_hh\SM15_Left.mdl`, sm15Left},

	// Watson SAS-12 (PlayStation 3; the Xbox 360 textures differ from the confirmed files)
	{"guns/skins_pv/Watson_SAS-12_D.dds", srcPS3, `guns\skins_pv\Watson_SAS-12_D.tex`, ps3Full},
	{"guns/skins_pv/Watson_SAS-12_S.dds", srcPS3, `guns\skins_pv\Watson_SAS-12_S.tex`, ps3Full},
	{"guns/skins_pv/Watson_SAS-12_N.dds", srcPS3, `guns\skins_pv\Watson_SAS-12_N.tex`, ps3Normal},
	{"guns/Materials/Watson_SAS-12.Mat00", srcX360 | srcPS3, `guns\Materials\Watson_SAS-12.mat`, matConv},
	{"guns/models_hh/WatsonSAS.Model00p", srcPS3, `guns\models_hh\WatsonSAS.mdl`, func(a assetIn, path string) ([]byte, error) {
		w, err := convModel(a, path)
		if err != nil {
			return nil, err
		}
		ref, _, err := a.read(`guns\models_hh\shotgun.mdl`) // the physics body the Watson gets (drop fix)
		if err != nil {
			return nil, err
		}
		return model.AddWatsonBody(w, ref)
	}},
	{rel: "chars/models/player_watson.Model00p", from: srcPS3, path: `chars\models\player_watson.mdl`, conv: playerWatson},

	// HUD weapon icons
	{"Interface/hud/weapon/sm15.dds", srcX360, `Interface\hud\weapon\sm15.tex`, hudIcon},
	{"Interface/hud/weapon/sm15X.dds", srcX360, `Interface\hud\weapon\sm15X.tex`, hudIcon},
	{"Interface/hud/weapon/dualsm15.dds", srcX360, `Interface\hud\weapon\dualsm15.tex`, hudIcon},
	{"Interface/hud/weapon/dualsm15X.dds", srcX360, `Interface\hud\weapon\dualsm15X.tex`, hudIcon},
	{"Interface/hud/weapon/autoshot.dds", srcPS3, `Interface\hud\weapon\autoshot.tex`, hudIcon},
	{"Interface/hud/weapon/autoshotX.dds", srcPS3, `Interface\hud\weapon\autoshotX.tex`, hudIcon},

	// SFOD-D (PlayStation 3)
	{"chars/models/corpse_office_bob_alive.Model00p", srcPS3, `chars\models\corpse_office_bob_alive.mdl`, convModel},
	{"chars/models/player_Deltaforce.Model00p", srcPS3, `chars\models\player_Deltaforce.mdl`, func(a assetIn, path string) ([]byte, error) {
		body, err := convModel(a, path)
		if err != nil {
			return nil, err
		}
		pl, err := stockPlayer(a)
		if err != nil {
			return nil, err
		}
		return model.SetLeftHandSocket(body, pl) // the PC left-hand socket (dual SM15 fix)
	}},
	{"Interface/hud/obj_example.dds", srcPS3, `Interface\hud\obj_example.tex`, ps3DXT},
	{"Materials/Engineering/black.dds", srcX360, `Materials\Engineering\black.tex`, x360Tex},
	{"Materials/Engineering/Black.Mat00", srcX360 | srcPS3, `Materials\Engineering\Black.mat`, matConv},
	{"Prefabs/Test/ElevatorFloorT.Mat00", srcX360 | srcPS3, `Prefabs\Test\ElevatorFloorT.mat`, matConv},
	{"Prefabs/Test/ElevatorRailT.Mat00", srcX360 | srcPS3, `Prefabs\Test\ElevatorRailT.mat`, matConv},
	{"Prefabs/Test/ElevatorWallT.Mat00", srcX360 | srcPS3, `Prefabs\Test\ElevatorWallT.mat`, matConv},
	{"Prefabs/Test/ElevatortopT.Mat00", srcX360 | srcPS3, `Prefabs\Test\ElevatortopT.mat`, matConv},
	{rel: "AnimationDatabase/AnimTrees/Player_Upper_SFODD.AnmTree00p", from: srcPS3, path: "database_anim/anim.anmtree", conv: playerUpperSFODD},
	{rel: "Worlds/Release/SFOD_D.World00p", from: srcPS3, path: "worlds/release/sfod_d.wld", conv: sfodWorld},

	// Holiday models (Xbox 360: DEC3N; the PS3 copies give different bytes)
	{"chars/models/player_holiday.Model00p", srcX360, `chars\models\player_Holiday.mdl`, playerHoliday},
	{"chars/models/Base_BishopEvac_BishopSitting.Model00p", srcX360, `chars\models\Base_BishopEvac_BishopSitting.mdl`, convDEC3N},
	{"chars/models/IanHives_Holiday.Model00p", srcX360, `chars\models\IanHives.mdl`, convDEC3N}, // IanHives.mdl under its own path

	// The Holiday world (Xbox 360 only; the build needs no PS3 copy)
	{rel: "Worlds/Release/Holiday.World00p", from: srcX360, path: "Worlds/Release/Holiday.wld", conv: holidayWorld},

	// Holiday (either console: the Soldier tree is the same on both)
	{rel: "AnimationDatabase/AnimTrees/Soldier_Holiday.AnmTree00p", from: srcX360 | srcPS3, path: "database_anim/anim.anmtree", conv: soldierHoliday(false)},
	{rel: "AnimationDatabase/AnimTrees/Soldier_Holiday2.AnmTree00p", from: srcX360 | srcPS3, path: "database_anim/anim.anmtree", conv: soldierHoliday(true)},

	// Bonus missions (either)
	{"Interface/menu/art/LoadScreens/load_bonusX360.dds", srcX360 | srcPS3, `Interface\menu\art\LoadScreens\load_bonusX360.tex`, either(x360Tex, ps3Stored)},
	{"Tex/Localization/Calibration_A0.dds", srcX360 | srcPS3, `Tex\Localization\Calibration_A0.tex`, either(x360Tex, ps3Stored)},
	{"Materials/grid/grid_e.dds", srcX360 | srcPS3, `Materials\grid\grid_e.tex`, either(x360Tex, ps3Full)},
	{"Materials/grid/grid_s.dds", srcX360 | srcPS3, `Materials\grid\grid_s.tex`, either(x360Tex, ps3Full)},
	{"Materials/grid/grid_n.dds", srcX360, `Materials\grid\grid_n.tex`, x360Normal},
	{"Materials/Grid_Floor_D.dds", srcX360, `Materials\Grid_Floor_D.tex`, x360Tex},

	// Every install: the console player body of the campaign (from the stock PC player)
	{"chars/models/player_console.Model00p", 0, "", func(a assetIn, _ string) ([]byte, error) {
		pl, err := stockPlayer(a)
		if err != nil {
			return nil, err
		}
		return model.MakePlayerConsole(pl)
	}},

	// Instant Action (Xbox 360): the console fonts
	{"Interface/IA/zSFODD_ConsoleFonts.bin", srcX360, consolefont.X360.CatalogPath(0), func(a assetIn, _ string) ([]byte, error) {
		var f [2][]byte
		for i := range f {
			var err error
			if f[i], _, err = a.read(consolefont.X360.CatalogPath(i)); err != nil {
				return nil, err
			}
		}
		return consolefont.Build(consolefont.X360, f[0], f[1])
	}},
}

// assetNeeds returns the console copies a file of the assets stage can be built from (ok = it is one).
func assetNeeds(rel string) (from src, ok bool) {
	for _, a := range assets {
		if strings.EqualFold(a.rel, rel) {
			return a.from, true
		}
	}
	return 0, false
}

// buildAssets runs the assets stage: every asset the choice needs whose console copy is there. A file that cannot
// be built from the given copies is logged and left out; the installer's completeness check then refuses to
// install (internal/app RealInstaller), so a partial mod is never written.
func (o *Options) buildAssets(w *writer, fs *pcfs.FS, log *dbmerge.Log) error {
	c := o.Choice
	if c == (Choice{}) {
		c = Choice{SFOD: o.PS3Dir != "", SAS12: o.PS3Dir != "", Holiday: o.X360Dir != "", SM15: o.X360Dir != ""}
	}
	c.HavePS3, c.HaveX360 = o.PS3Dir != "", o.X360Dir != ""
	var cats [2]*catalog.Catalog // PS3, X360
	defer func() {
		for _, k := range cats {
			if k != nil {
				k.Close()
			}
		}
	}()
	open := func(i int) (*catalog.Catalog, error) {
		if cats[i] != nil {
			return cats[i], nil
		}
		var p string
		var err error
		if i == 0 {
			p, err = FindCI(o.PS3Dir, "ubercatalog.cat")
		} else {
			p, err = FindCI(o.X360Dir, "Game", "uberCatalog.cat")
		}
		if err != nil {
			return nil, err
		}
		cats[i], err = catalog.Open(p)
		return cats[i], err
	}
	o.say("Converting textures, materials, models, animation trees and the bonus mission worlds")
	var err error
	for _, a := range assets {
		var item Item = -1
		for _, f := range modFiles {
			if strings.EqualFold(f.Path, a.rel) {
				item = f.Item
			}
		}
		if item < 0 {
			return fmt.Errorf("content: asset %s has no row in the mod file table", a.rel)
		}
		if !c.wants(item) {
			continue
		}
		in := assetIn{pc: fs}
		switch {
		case a.from == 0:
		case a.from&srcX360 != 0 && c.HaveX360:
			if in.root, err = FindCI(o.X360Dir, "Game"); err != nil {
				return fmt.Errorf("Xbox 360 Game folder: %w", err)
			}
			in.cat = func() (*catalog.Catalog, error) {
				k, err := open(1)
				if err != nil {
					return nil, fmt.Errorf("Xbox 360 uberCatalog.cat: %w", err)
				}
				return k, nil
			}
		case a.from&srcPS3 != 0 && c.HavePS3:
			in.root, in.isPS3 = o.PS3Dir, true
			in.cat = func() (*catalog.Catalog, error) {
				k, err := open(0)
				if err != nil {
					return nil, fmt.Errorf("PS3 ubercatalog.cat: %w", err)
				}
				return k, nil
			}
		default:
			log.Add("assets not built", a.rel+": needs "+a.from.copies())
			continue
		}
		data, err := a.conv(in, a.path)
		if err != nil {
			return fmt.Errorf("%s: %w", a.rel, err)
		}
		if err := w.write("Game/"+a.rel, data); err != nil {
			return err
		}
	}
	return nil
}
