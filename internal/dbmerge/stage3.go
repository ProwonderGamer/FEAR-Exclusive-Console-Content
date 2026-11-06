package dbmerge

import (
	"fmt"
	"sort"
	"strings"

	"fearinstaller/internal/gadb"
)

const (
	holidayMissionOrder = Prefix + "Bonus_Holiday"
	holidayLayout       = Prefix + "Bonus_Holiday"
	bishopName          = Prefix + "X360_Bishop"
	// BishopTree is the Holiday-only soldier tree (Soldier_Holiday + StepAside/Run -> walkback).
	BishopTree  = `AnimationDatabase\AnimTrees\Soldier_Holiday2.AnmTree00p`
	bishopModel = `chars\models\IanHives_Holiday.Model00p`
	ps3ArtTex   = `Interface\menu\art\LoadScreens\load_BonusPS3.dds`
)

// reused: SFOD-D records the Holiday mission re-uses; the Xbox 360 record must equal the PS3 one.
var reused = [][3]string{
	{"Arsenal/Ammo", "zSFODD_MachinePistol", "MachinePistol"}, {"Arsenal/Ammo/AI", "zSFODD_MachinePistol", "MachinePistol"},
	{"Arsenal/Ammo/Default", "zSFODD_MachinePistol", "MachinePistol"}, {"Arsenal/Ammo/MultiPlayer", "zSFODD_MachinePistol", "MachinePistol"},
	{"AI/Weapons", "zSFODD_MachinePistol", "MachinePistol"},
	{"Arsenal/Weapons", "zSFODD_Machine Pistol", "Machine Pistol"}, {"Arsenal/Weapons/AI", "zSFODD_Machine Pistol", "Machine Pistol"},
	{"Arsenal/Weapons/Default", "zSFODD_Machine Pistol", "Machine Pistol"}, {"Arsenal/Weapons/Multiplayer", "zSFODD_Machine Pistol", "Machine Pistol"},
	{"Arsenal/Weapons", "zSFODD_Dual Machine Pistols", "Dual Machine Pistols"},
	{"Interface/Objective", "zSFODD_Objective46", "Objective46"}, {"Interface/Credits/Entries", "zSFODD_Objective46", "Objective46"},
	{"Interface/Credits/Order", "zSFODD_Objective46", "Objective46"},
}

// Stage3Inputs are the inputs of the Holiday stage (port of the reference Python stage).
type Stage3Inputs struct {
	PC, PCL     []byte // stock databases
	SFODFEAR    []byte // SFOD-D FEAR.gamdb00p (stage 2)
	SFODFEARL   []byte // SFOD-D FEARL.Gamdb00p (stage 2)
	X360DB      []byte // Xbox 360 Game/database/Loki.gamedb
	PS3DB       []byte // PS3 loki.gamedb: used only to assert that the re-used records are identical on both consoles (may be nil)
	TOC         TOC
	SFODPS3Art  bool // give SFOD-D its own FEARL layout with the PS3 loading screen (variant)
	BishopStock bool // keep the stock Bishop animation tree (variant)
	Log         *Log
}

// Stage3Result is the combined SFOD-D + Holiday database pair.
type Stage3Result struct {
	FEAR, FEARL []byte
	Renames     map[string]string
}

func sigEqual(a, b *gadb.Record) bool {
	if len(a.Attrs) != len(b.Attrs) {
		return false
	}
	for i := range a.Attrs {
		x, y := a.Attrs[i], b.Attrs[i]
		if x.Name != y.Name || x.Type != y.Type || x.Flags != y.Flags || !sameValues(x, y) {
			return false
		}
	}
	return true
}

func fixExt(v string) string {
	return strings.ReplaceAll(strings.ReplaceAll(v, ".mdl", ".Model00p"), ".mat", ".Mat00")
}

// BuildStage3 adds the Xbox 360 Holiday mission (mission, level, player model, mission order, Bishop variant)
// on top of the SFOD-D databases. The Bonus mission menu offers both missions from ONE mounted database.
func BuildStage3(in Stage3Inputs) (*Stage3Result, error) {
	log := in.Log
	if log == nil {
		log = NewLog()
	}
	base, err := readSym(in.SFODFEAR)
	if err != nil {
		return nil, err
	}
	baseL, err := readSym(in.SFODFEARL)
	if err != nil {
		return nil, err
	}
	x360, err := readSym(in.X360DB)
	if err != nil {
		return nil, fmt.Errorf("Xbox 360 Loki.gamedb: %w", err)
	}
	stock, err := readSym(in.PC)
	if err != nil {
		return nil, err
	}
	stockL, err := readSym(in.PCL)
	if err != nil {
		return nil, err
	}
	base0, baseL0 := base.Clone(), baseL.Clone()

	// SM15 drop-on-death fix: CAI DropWeapons skips every weapon not listed in Arsenal/Global.PlayerWeapons.
	pw := base.Find("Arsenal/Global", "Global").Attr("PlayerWeapons")
	for _, w := range []string{Prefix + "Machine Pistol", Prefix + "Dual Machine Pistols"} {
		v := gadb.Link("Arsenal/Weapons", w)
		if base.Find("Arsenal/Weapons", w) == nil || linkIndex(pw.Values, v) >= 0 {
			return nil, fmt.Errorf("unexpected PlayerWeapons state for %s", w)
		}
		pw.Values = append(pw.Values, v)
	}
	xr, br := x360.RecMap(), base.RecMap()
	if in.PS3DB != nil {
		ps3, err := readSym(in.PS3DB)
		if err != nil {
			return nil, err
		}
		pr := ps3.RecMap()
		for _, r := range reused {
			if br[gadb.RecKey{Cat: r[0], Rec: r[1]}] == nil {
				return nil, fmt.Errorf("SFOD-D record %s/%s missing", r[0], r[1])
			}
			x, p := xr[gadb.RecKey{Cat: r[0], Rec: r[2]}], pr[gadb.RecKey{Cat: r[0], Rec: r[2]}]
			if x == nil || p == nil || !sigEqual(x, p) {
				return nil, fmt.Errorf("Xbox 360 != PS3 for %s/%s: the two discs do not carry the same record", r[0], r[2])
			}
		}
	} else {
		log.Add("notes", "PS3 database not given: the Xbox 360 == PS3 check of the re-used records was skipped")
	}

	// Holiday records converted with the shared merger (the Xbox 360 database plays the console database).
	tm := TemplatesSFODD()
	tm[[2]string{"Missions/Missions", "Bonus_Holiday"}] = "08_Admin"
	tm[[2]string{"Missions/Levels", "Bonus_Holiday"}] = "Admin"
	tm[[2]string{"Character/Models", "Player_Holiday"}] = "Player"
	m := NewMerger(base, x360, in.TOC, log, tm)
	seeds := [][2]string{{"Missions/Missions", "Bonus_Holiday"}, {"Missions/Levels", "Bonus_Holiday"}, {"Character/Models", "Player_Holiday"}}
	for _, s := range seeds {
		if err := m.Want(s[0], s[1], "Holiday seed"); err != nil {
			return nil, err
		}
	}
	if err := m.Run(); err != nil {
		return nil, err
	}
	rename := map[gadb.RecKey]string{
		{Cat: "Missions/Missions", Rec: "Bonus_Holiday"}: Prefix + "Holiday",
		{Cat: "Missions/Levels", Rec: "Bonus_Holiday"}:   Prefix + "Holiday",
		{Cat: "Character/Models", Rec: "Player_Holiday"}: Prefix + "Player_Holiday",
	}
	for k := range rename {
		if _, ok := m.Added[k]; !ok {
			return nil, fmt.Errorf("seed %s/%s was not added", k.Cat, k.Rec)
		}
	}
	for _, k := range m.AddedOrder {
		if _, ok := rename[k]; !ok {
			rename[k] = Prefix + k.Rec
			log.Add("closure records added (renamed with prefix)", k.Cat+"/"+k.Rec+" ("+m.Reason[k]+")")
		}
	}
	for _, r := range m.Added {
		for _, a := range r.Attrs {
			if a.Type != gadb.TLink {
				continue
			}
			for i, v := range a.Values {
				if v.Null {
					continue
				}
				if n, ok := rename[gadb.RecKey{Cat: v.Cat, Rec: v.Rec}]; ok {
					a.Values[i] = gadb.Link(v.Cat, n)
				} else {
					a.Values[i] = gadb.Link(v.Cat, v.Rec)
				}
			}
		}
	}
	final := map[gadb.RecKey]*gadb.Record{}
	for _, k := range m.AddedOrder {
		r := m.Added[k]
		r.Name = rename[k]
		final[gadb.RecKey{Cat: k.Cat, Rec: r.Name}] = r
	}
	mis := final[gadb.RecKey{Cat: "Missions/Missions", Rec: Prefix + "Holiday"}]
	lvl := final[gadb.RecKey{Cat: "Missions/Levels", Rec: Prefix + "Holiday"}]
	for _, an := range []string{"Layout", "BriefingLayout"} {
		a := mis.Attr(an)
		if a == nil || len(a.Values) != 1 || a.Values[0].S != "Bonus_Holiday" {
			return nil, fmt.Errorf("unexpected Holiday mission %s", an)
		}
		a.Values = []gadb.Value{gadb.Str(holidayLayout)}
	}
	if w := lvl.Attr("World"); w == nil || len(w.Values) != 1 || w.Values[0].S != `Worlds\Release\Holiday.World00p` {
		return nil, fmt.Errorf("unexpected Holiday level World")
	}
	pm := final[gadb.RecKey{Cat: "Character/Models", Rec: Prefix + "Player_Holiday"}]
	at := pm.Attr("AnimationTree")
	if at == nil || len(at.Values) < 3 || at.Values[2].S != `AnimationDatabase\AnimTrees\Player_Upper.AnmTree00p` {
		return nil, fmt.Errorf("unexpected Holiday player AnimationTree")
	}
	at.Values[2] = gadb.Str(UpperTree)
	if lv := mis.Attr("Levels"); lv == nil || len(lv.Values) != 1 || lv.Values[0].Cat != "Missions/Levels" || lv.Values[0].Rec != lvl.Name {
		return nil, fmt.Errorf("Holiday mission does not link its level")
	}
	if baseL.Find("Interface/LoadScreen", holidayLayout) == nil {
		return nil, fmt.Errorf("FEARL layout %s missing", holidayLayout)
	}
	// Bishop: PC Character/Models/Bishop + the Xbox 360 AIName and soldier tree, as a Holiday-only record.
	xb, pb := xr[gadb.RecKey{Cat: "Character/Models", Rec: "Bishop"}], br[gadb.RecKey{Cat: "Character/Models", Rec: "Bishop"}]
	if xb == nil || pb == nil {
		return nil, fmt.Errorf("Character/Models/Bishop missing")
	}
	var bdiff []string
	for _, a := range pb.Attrs {
		xa := xb.Attr(a.Name)
		differs := xa == nil || len(xa.Values) != len(a.Values)
		if !differs {
			for i, v := range xa.Values {
				if xa.Type == gadb.TString && !v.Null {
					v = gadb.Str(fixExt(v.S))
				}
				if !gadb.Equal(a.Type, v, a.Values[i]) || xa.Type != a.Type {
					differs = true
					break
				}
			}
		}
		if differs {
			bdiff = append(bdiff, a.Name)
		}
	}
	sort.Strings(bdiff)
	if strings.Join(bdiff, ",") != "AIName,AnimationTree" {
		return nil, fmt.Errorf("Bishop differs between Xbox 360 and PC in %v (expected AIName, AnimationTree)", bdiff)
	}
	if v := xb.Attr("AIName").Values; len(v) != 1 || v[0].Cat != "AI/Attributes" || v[0].Rec != "Alice" {
		return nil, fmt.Errorf("unexpected Xbox 360 Bishop AIName")
	}
	if v := xb.Attr("AnimationTree").Values; len(v) != 1 || v[0].S != "Soldier" {
		return nil, fmt.Errorf("unexpected Xbox 360 Bishop AnimationTree")
	}
	if base.Find("AI/Attributes", "Alice") == nil {
		return nil, fmt.Errorf("AI/Attributes/Alice missing on PC")
	}
	bish := pb.Copy()
	bish.Name = bishopName
	bish.Attr("AIName").Values = []gadb.Value{gadb.Link("AI/Attributes", "Alice")}
	if mf := bish.Attr("ModelFile"); mf == nil || len(mf.Values) != 1 || mf.Values[0].S != `chars\models\IanHives.Model00p` {
		return nil, fmt.Errorf("unexpected PC Bishop ModelFile")
	}
	bish.Attr("ModelFile").Values = []gadb.Value{gadb.Str(bishopModel)}
	if !in.BishopStock {
		bish.Attr("AnimationTree").Values = []gadb.Value{gadb.Str(BishopTree)}
	}
	final[gadb.RecKey{Cat: "Character/Models", Rec: bishopName}] = bish

	// SFOD-D mission/level records must have the same schema as the Holiday ones.
	smis, slvl := br[gadb.RecKey{Cat: "Missions/Missions", Rec: Prefix + missionSFOD}], br[gadb.RecKey{Cat: "Missions/Levels", Rec: Prefix + missionSFOD}]
	for _, p := range [][2]*gadb.Record{{smis, mis}, {slvl, lvl}} {
		if len(p[0].Attrs) != len(p[1].Attrs) {
			return nil, fmt.Errorf("SFOD-D and Holiday records differ in schema")
		}
		for i := range p[0].Attrs {
			if p[0].Attrs[i].Name != p[1].Attrs[i].Name {
				return nil, fmt.Errorf("SFOD-D and Holiday records differ in schema")
			}
		}
	}
	// insert: appended (sort-last asserted), the mission order record is inserted at its sorted position
	keys := make([]gadb.RecKey, 0, len(final))
	for k := range final {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if gadb.SKey(a.Cat) != gadb.SKey(b.Cat) {
			return gadb.SKey(a.Cat) < gadb.SKey(b.Cat)
		}
		return gadb.SKey(a.Rec) < gadb.SKey(b.Rec)
	})
	for _, k := range keys {
		r := final[k]
		ci := base.CatIndex(k.Cat)
		if ci < 0 {
			return nil, fmt.Errorf("category %s missing", k.Cat)
		}
		c := base.Cats[ci]
		if len(c.Records) > 0 && !(gadb.SKey(r.Name) > gadb.SKey(c.Records[len(c.Records)-1].Name)) {
			return nil, fmt.Errorf("%s/%s does not sort last (%s)", k.Cat, r.Name, c.Records[len(c.Records)-1].Name)
		}
		c.Records = append(c.Records, r)
	}
	order := br[gadb.RecKey{Cat: "Missions/MissionOrder", Rec: "MissionOrder"}].Copy()
	order.Name = holidayMissionOrder
	order.Attr("Missions").Values = []gadb.Value{gadb.Link("Missions/Missions", mis.Name)}
	if _, _, err := gadb.InsertSorted(base, "Missions/MissionOrder", order); err != nil {
		return nil, err
	}
	// FEARL
	if in.SFODPS3Art {
		c := baseL.Cats[baseL.CatIndex("Interface/LoadScreen")]
		lay := baseL.Find("Interface/LoadScreen", holidayLayout).Copy()
		lay.Name = Prefix + "Bonus_SFOD-D"
		lay.Attr("BackgroundTexture").Values = []gadb.Value{gadb.Str(ps3ArtTex)}
		if !(gadb.SKey(lay.Name) > gadb.SKey(c.Records[len(c.Records)-1].Name)) {
			return nil, fmt.Errorf("SFOD-D layout does not sort last")
		}
		c.Records = append(c.Records, lay)
		for _, an := range []string{"Layout", "BriefingLayout"} {
			smis.Attr(an).Values = []gadb.Value{gadb.Str(lay.Name)}
		}
	}
	xl, xd := xr[gadb.RecKey{Cat: "Interface/LoadScreen", Rec: "Bonus_Holiday"}], xr[gadb.RecKey{Cat: "Interface/LoadScreen", Rec: "Default"}]
	if xl == nil || xd == nil {
		return nil, fmt.Errorf("Xbox 360 load screen records missing")
	}
	var ldiff []string
	for _, a := range xl.Attrs {
		if b := xd.Attr(a.Name); b == nil || !sameValues(a, b) {
			ldiff = append(ldiff, a.Name)
		}
	}
	if strings.Join(ldiff, ",") != "BackgroundTexture" {
		return nil, fmt.Errorf("Xbox 360 Bonus_Holiday layout differs from Default in %v", ldiff)
	}
	// verify + write
	res := &Stage3Result{Renames: map[string]string{}}
	for k, n := range rename {
		res.Renames[k.Cat+"/"+k.Rec] = n
	}
	for _, tgt := range []struct {
		label       string
		d, ref, stk *gadb.Database
		out         *[]byte
	}{{"FEAR", base, base0, stock, &res.FEAR}, {"FEARL", baseL, baseL0, stockL, &res.FEARL}} {
		if err := checkSortedErr(tgt.d, tgt.label); err != nil {
			return nil, err
		}
		if len(tgt.d.Cats) != len(tgt.ref.Cats) {
			return nil, fmt.Errorf("%s: category count changed", tgt.label)
		}
		// stock record indices must be identical
		for ci, c := range tgt.stk.Cats {
			for ri, r := range c.Records {
				if tgt.d.Cats[ci].Records[ri].Name != r.Name {
					return nil, fmt.Errorf("%s: stock record %s/%s moved", tgt.label, c.Name, r.Name)
				}
			}
		}
		// the only SFOD-D record that may change index is its mission order record (+1)
		var moved, changed []string
		dr := tgt.d.RecMap()
		for ci, c := range tgt.ref.Cats {
			dn := map[string]int{}
			for i, r := range tgt.d.Cats[ci].Records {
				dn[r.Name] = i
			}
			for ri, r := range c.Records {
				if dn[r.Name] != ri {
					moved = append(moved, c.Name+"/"+r.Name)
				}
				if !sigEqual(dr[gadb.RecKey{Cat: c.Name, Rec: r.Name}], r) {
					changed = append(changed, c.Name+"/"+r.Name)
				}
			}
		}
		if tgt.label == "FEAR" {
			var stockChanged []string
			for _, c := range tgt.stk.Cats {
				for _, r := range c.Records {
					if !sigEqual(dr[gadb.RecKey{Cat: c.Name, Rec: r.Name}], r) {
						stockChanged = append(stockChanged, c.Name+"/"+r.Name)
					}
				}
			}
			if strings.Join(stockChanged, "|") != "Arsenal/Global/Global" {
				return nil, fmt.Errorf("unexpected changes to stock records: %v", stockChanged)
			}
		}
		wantMoved, wantChanged := "", ""
		if tgt.label == "FEAR" {
			wantMoved = "Missions/MissionOrder/" + Prefix + missionSFOD
			wantChanged = "Arsenal/Global/Global"
			if in.SFODPS3Art {
				wantChanged += "|Missions/Missions/" + Prefix + missionSFOD
			}
		}
		if strings.Join(moved, "|") != wantMoved || strings.Join(changed, "|") != wantChanged {
			return nil, fmt.Errorf("%s: unexpected changes versus the SFOD-D database: moved %v, changed %v", tgt.label, moved, changed)
		}
		data, err := encodeWrite(tgt.d)
		if err != nil {
			return nil, err
		}
		*tgt.out = data
	}
	return res, nil
}
