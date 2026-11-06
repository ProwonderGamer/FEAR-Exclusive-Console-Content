package dbmerge

// Stage 4 ("live"): the changes the complete mod makes to the stage 3 (SFOD-D + Holiday) databases after the
// Holiday step. Ports of three of the project's Python recipes, each re-proven byte for byte by the golden test:
//
//	campaign player   the default run with the campaign player on: stock record Character/Models/Player gets
//	                  the merged upper-body tree and the console player model
//	Instant Action    the final state of the Instant Action database merge: 12 FEAR records + 4 FEARL load
//	                  screens, and the stock FEARL Interface/Screens/ScreenFailure layout edit
//	Watson reload     database part only: two Sound/Sound records and three reload sound slots for
//	                  zSFODD_AutoShotgun
//
// The Python recipes read and hash-assert an already installed mod folder; this port starts from the stage 3
// output, i.e. only from the console copies and the stock PC archives.
//
// Every new record is APPENDED and sorts last (zSFODD_ prefix, asserted), so no stock or earlier mod record moves;
// every category stays sorted by _stricmp name (asserted: the engine binary-searches it).

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"fearinstaller/internal/gadb"
)

const (
	// IAPrefix names the Instant Action records (sorts after every stock name and after the Holiday records).
	IAPrefix = Prefix + "IA_"

	campaignStockModel = `chars\models\player.Model00p`
	// CampaignModel is the console player model of the campaign player (stock model + the console child models).
	CampaignModel    = `chars\models\player_console.Model00p`
	campaignStockTop = `AnimationDatabase\AnimTrees\Player_Upper.AnmTree00p`

	watsonReload1 = Prefix + "Watson_Reload1"
)

// iaMaps are the four Instant Action maps in map-number order (loading screen IA_Load<n>.dds, level name key
// IDS_INSTANT_ACTION_MAP<n>, both from the X360 Loki.gamedb Missions/Levels/InstantAction<n>).
var iaMaps = []string{"Distribution", "Rooftop", "Construction", "Vault"}

// WatsonReloadSounds are the sound files the Watson reload split needs in the mod (split from
// guns\Snd\Shotgun\WASreload.WAV; part 1 keeps that name).
var WatsonReloadSounds = []string{`guns\Snd\Shotgun\WASreload2.WAV`, `guns\Snd\Shotgun\WASreload3.WAV`}

// LiveInputs are the inputs of stage 4.
type LiveInputs struct {
	PC, PCL                   []byte // stock FEAR.gamdb00p / FEARL.Gamdb00p (record index and value checks)
	HolidayFEAR, HolidayFEARL []byte // stage 3 output (SFOD-D + Holiday)
	// NoCampaignPlayer leaves the stock Character/Models/Player untouched (the Xbox-only Instant Action build).
	// The complete mod has the campaign player.
	NoCampaignPlayer bool
	// InstantAction adds the Instant Action records. Only together with the IA maps, loading screens and the
	// ConsoleText IA keys: the records name Worlds\Release\IA_<map>.World00p and Interface\IA\IA_Load<n>.dds.
	InstantAction bool
	// WatsonReload adds the Watson reload split. Needs WatsonReloadSounds and the reload-split player_watson model.
	WatsonReload bool
	Log          *Log

	// iaMission, when set, replaces the zSFODD_Holiday record as the template of the IA mission records
	// (Xbox-only build, see BuildIAOnly).
	iaMission *gadb.Record
}

// iaMissionTemplate builds the IA mission template without the Holiday stage: stock Missions/Missions/08_Admin
// (the template stage 3 converts Bonus_Holiday with) with the Name key of the Xbox 360 Bonus_Holiday record.
// After the IA values are written, every IA mission attribute equals the stage 3 based one (asserted by
// TestIAOnlyEqualsFullBuild): of zSFODD_Holiday only its Name survives the IA overwrite.
func iaMissionTemplate(stock, x360 *gadb.Database) (*gadb.Record, error) {
	adm := stock.Find("Missions/Missions", "08_Admin")
	xb := x360.Find("Missions/Missions", "Bonus_Holiday")
	if adm == nil || xb == nil {
		return nil, fmt.Errorf("Missions/Missions 08_Admin (PC) or Bonus_Holiday (Xbox 360) missing")
	}
	xn := xb.Attr("Name")
	if xn == nil || xn.Type != gadb.TString || len(xn.Values) != 1 || xn.Values[0].Null {
		return nil, fmt.Errorf("unexpected Xbox 360 Bonus_Holiday Name")
	}
	t := adm.Copy()
	t.Name = Prefix + "Holiday"
	if err := setAttr(t, "Name", gadb.TString, xn.Values[0]); err != nil {
		return nil, err
	}
	return t, nil
}

// BuildIAOnly builds the Instant Action databases of an Xbox-only install: the stock PC databases plus the IA
// records and the ScreenFailure layout, from the stock PC databases and the Xbox 360 Loki.gamedb alone (no SFOD-D,
// Holiday, campaign player or Watson records, so no PS3 data). The IA records are the same as in the full build;
// their record INDICES differ (no SFOD-D/Holiday records before them), which matters only to code that stores
// indices (saves), not to lookups by name.
func BuildIAOnly(pc, pcl, x360db []byte) (*LiveResult, error) {
	stock, err := readSym(pc)
	if err != nil {
		return nil, err
	}
	x, err := readSym(x360db)
	if err != nil {
		return nil, fmt.Errorf("Xbox 360 Loki.gamedb: %w", err)
	}
	t, err := iaMissionTemplate(stock, x)
	if err != nil {
		return nil, err
	}
	return BuildLive(LiveInputs{PC: pc, PCL: pcl, HolidayFEAR: pc, HolidayFEARL: pcl, NoCampaignPlayer: true,
		InstantAction: true, iaMission: t})
}

// LiveResult is the database pair of the complete mod.
type LiveResult struct {
	FEAR, FEARL []byte
}

// f32 returns the IEEE single bit pattern of x (gadbw.bits).
func f32(x float32) uint32 { return math.Float32bits(x) }

func vec(xs ...float32) gadb.Value {
	v := gadb.Value{V: make([]uint32, len(xs))}
	for i, x := range xs {
		v.V[i] = f32(x)
	}
	return v
}

// setAttr replaces the values of an existing attribute after checking its type (no attribute is ever added).
func setAttr(r *gadb.Record, name string, typ uint32, vals ...gadb.Value) error {
	a := r.Attr(name)
	if a == nil {
		return fmt.Errorf("%s has no attribute %s", r.Name, name)
	}
	if a.Type != typ {
		return fmt.Errorf("%s.%s has type %d, expected %d", r.Name, name, a.Type, typ)
	}
	a.Values = append([]gadb.Value(nil), vals...)
	return nil
}

// appendCopy appends a copy of template under newName (the Python merge's append_copy). The new name must sort last.
func appendCopy(db *gadb.Database, cat, template, newName string) (*gadb.Record, error) {
	return appendRec(db, cat, template, nil, newName)
}

// appendRec appends a copy of tmplRec, or of the record named template when tmplRec is nil. The new name must
// sort last.
func appendRec(db *gadb.Database, cat, template string, tmplRec *gadb.Record, newName string) (*gadb.Record, error) {
	ci := db.CatIndex(cat)
	if ci < 0 {
		return nil, fmt.Errorf("category %s missing", cat)
	}
	c := db.Cats[ci]
	if !strings.HasPrefix(newName, Prefix) {
		return nil, fmt.Errorf("%s/%s: new records need the %s prefix", cat, newName, Prefix)
	}
	if c.Index(newName) >= 0 {
		return nil, fmt.Errorf("%s/%s already exists", cat, newName)
	}
	src := tmplRec
	if src == nil {
		ti := c.Index(template)
		if ti < 0 {
			return nil, fmt.Errorf("template %s/%s missing", cat, template)
		}
		src = c.Records[ti]
	}
	if len(c.Records) > 0 && !(gadb.SKey(newName) > gadb.SKey(c.Records[len(c.Records)-1].Name)) {
		return nil, fmt.Errorf("%s/%s does not sort last (%s)", cat, newName, c.Records[len(c.Records)-1].Name)
	}
	r := src.Copy()
	r.Name = newName
	c.Records = append(c.Records, r)
	return r, nil
}

// applyCampaignPlayer: Point Man's first-person animations for the console weapons.
func applyCampaignPlayer(db *gadb.Database) error {
	cp := db.Find("Character/Models", "Player")
	if cp == nil {
		return fmt.Errorf("Character/Models/Player missing")
	}
	at := cp.Attr("AnimationTree")
	if at == nil || at.Type != gadb.TString || len(at.Values) < 3 || at.Values[2].S != campaignStockTop {
		return fmt.Errorf("unexpected Character/Models/Player AnimationTree")
	}
	at.Values[2] = gadb.Str(UpperTree)
	mf := cp.Attr("ModelFile")
	if mf == nil || mf.Type != gadb.TString || len(mf.Values) != 1 || mf.Values[0].S != campaignStockModel {
		return fmt.Errorf("unexpected Character/Models/Player ModelFile")
	}
	mf.Values[0] = gadb.Str(CampaignModel)
	return nil
}

// iaOrder returns the Instant Action maps in record order: sorted by name, so the appended records keep every
// category sorted.
func iaOrder() []string {
	m := append([]string(nil), iaMaps...)
	sort.Slice(m, func(i, j int) bool { return gadb.SKey(m[i]) < gadb.SKey(m[j]) })
	return m
}

func iaNumber(m string) int {
	for i, x := range iaMaps {
		if x == m {
			return i + 1
		}
	}
	panic("unknown IA map " + m)
}

// applyIAMain adds the Instant Action level, mission and mission order records (merge_ia_db.merge_main).
func applyIAMain(db *gadb.Database, misTmpl *gadb.Record) error {
	for _, m := range iaOrder() {
		key := IAPrefix + m
		lev, err := appendCopy(db, "Missions/Levels", "WTF_Exfil", key)
		if err != nil {
			return err
		}
		if err := setAttr(lev, "World", gadb.TString, gadb.Str(`Worlds\Release\IA_`+m+`.World00p`)); err != nil {
			return err
		}
		if err := setAttr(lev, "Briefing", gadb.TString, gadb.Str("")); err != nil {
			return err
		}
		if err := setAttr(lev, "Photo", gadb.TString, gadb.Str("")); err != nil {
			return err
		}
		if err := setAttr(lev, "DefaultWeapons", gadb.TLink); err != nil {
			return err
		}
		// the X360 level name key (served by ConsoleText as "Map <n> - <map>")
		if err := setAttr(lev, "Name", gadb.TString, gadb.Str(fmt.Sprintf("IDS_INSTANT_ACTION_MAP%d", iaNumber(m)))); err != nil {
			return err
		}
		mis, err := appendRec(db, "Missions/Missions", Prefix+"Holiday", misTmpl, key)
		if err != nil {
			return err
		}
		if err := setAttr(mis, "Levels", gadb.TLink, gadb.Link("Missions/Levels", key)); err != nil {
			return err
		}
		for _, an := range []string{"Layout", "BriefingLayout"} {
			if err := setAttr(mis, an, gadb.TString, gadb.Str(key)); err != nil {
				return err
			}
		}
		// The starting weapons replace the Holiday template's: the X360 Arsenal/Global.AdditionalDefaultWeapons,
		// given per mission (PC Global stays untouched), nothing selected by default; Construction draws the
		// shotgun at spawn for its rope ride (as stock 02_Docks).
		guns := []gadb.Value{gadb.Link("Arsenal/Weapons", "Pistol"), gadb.Link("Arsenal/Weapons", "Submachinegun"), gadb.Link("Arsenal/Weapons", "Frag Grenade")}
		sel := gadb.NoLinkSym()
		if m == "Construction" {
			guns = append(guns, gadb.Link("Arsenal/Weapons", "Shotgun"))
			sel = gadb.Link("Arsenal/Weapons", "Shotgun")
		}
		if err := setAttr(mis, "DefaultWeapons", gadb.TLink, guns...); err != nil {
			return err
		}
		if err := setAttr(mis, "SelectedWeapon", gadb.TLink, sel); err != nil {
			return err
		}
		if err := setAttr(mis, "ResetPlayer", gadb.TBool, gadb.Value{U: 1}); err != nil {
			return err
		}
		name := m + " - Instant Action" // wide string (ASCII text: one UTF-16 code unit per byte)
		w := make([]uint16, len(name))
		for i := 0; i < len(name); i++ {
			w[i] = uint16(name[i])
		}
		if err := setAttr(mis, "NameStr", gadb.TWStr, gadb.Value{W: w}); err != nil {
			return err
		}
		ord, err := appendCopy(db, "Missions/MissionOrder", "MissionOrder", key)
		if err != nil {
			return err
		}
		if err := setAttr(ord, "Missions", gadb.TLink, gadb.Link("Missions/Missions", key)); err != nil {
			return err
		}
	}
	return nil
}

// applyIALoc adds the Instant Action load screens and the ScreenFailure layout (merge_ia_db.merge_loc).
func applyIALoc(db *gadb.Database) error {
	// STOCK RECORD EDIT: the console's mission-failed layout, also used by the campaign.
	r := db.Find("Interface/Screens", "ScreenFailure")
	if r == nil {
		return fmt.Errorf("Interface/Screens/ScreenFailure missing")
	}
	sr, cw, fs := r.Attr("ScreenRect"), r.Attr("ColumnWidths"), r.Attr("FontSize")
	if sr == nil || sr.Type != gadb.TVec4 || len(sr.Values) != 1 || !gadb.Equal(gadb.TVec4, sr.Values[0], vec(76, 240, 585, 385)) {
		return fmt.Errorf("unexpected ScreenFailure.ScreenRect")
	}
	if cw == nil || cw.Type != gadb.TInt || len(cw.Values) != 2 || cw.Values[0].U != 500 || cw.Values[1].U != 200 {
		return fmt.Errorf("unexpected ScreenFailure.ColumnWidths")
	}
	if fs == nil || fs.Type != gadb.TInt || len(fs.Values) != 1 || fs.Values[0].U != 22 {
		return fmt.Errorf("unexpected ScreenFailure.FontSize")
	}
	sr.Values[0] = vec(44, 17, 600, 452)
	cw.Values[0] = gadb.Value{U: 544}
	fs.Values[0] = gadb.Value{U: 16}
	for _, m := range iaOrder() {
		key := IAPrefix + m
		rec, err := appendCopy(db, "Interface/LoadScreen", "Default", key)
		if err != nil {
			return err
		}
		if err := setAttr(rec, "BackgroundTexture", gadb.TString, gadb.Str(fmt.Sprintf(`Interface\IA\IA_Load%d.dds`, iaNumber(m)))); err != nil {
			return err
		}
		// the loading picture has the text baked in: hide the stock text and frames
		for _, a := range rec.Attrs {
			if !strings.HasSuffix(a.Name, "Pos") && !strings.HasSuffix(a.Name, "Rect") {
				continue
			}
			if a.Type < gadb.TVec2 || a.Type > gadb.TVec4 {
				return fmt.Errorf("LoadScreen/Default.%s is not a vector", a.Name)
			}
			for i, v := range a.Values {
				if v.Null {
					return fmt.Errorf("LoadScreen/Default.%s has an empty vector", a.Name)
				}
				nv := gadb.Value{V: make([]uint32, len(v.V))}
				for k := range nv.V {
					nv.V[k] = f32(-2000)
				}
				a.Values[i] = nv
			}
		}
		// the engine's live progress bar at the X360 DefaultMP rect (GameClient stretches it by W/640, H/480)
		if lt := rec.Attr("LoadingBarTexture"); lt == nil || len(lt.Values) != 1 || lt.Values[0].S != `Interface\hud\LoadingBar.dds` {
			return fmt.Errorf("unexpected LoadScreen/Default.LoadingBarTexture")
		}
		if err := setAttr(rec, "LoadingBarRect", gadb.TVec4, vec(200, 396, 440, 417)); err != nil {
			return err
		}
	}
	return nil
}

// applyWatsonReload: Watson SAS-12 reload in three sound slots (database part).
func applyWatsonReload(db *gadb.Database) error {
	links := []gadb.Value{gadb.Link("Sound/Sound", watsonReload1)}
	for n, file := range WatsonReloadSounds {
		name := fmt.Sprintf("%sWatson_Reload%d", Prefix, n+2)
		r, err := appendCopy(db, "Sound/Sound", watsonReload1, name)
		if err != nil {
			return err
		}
		if err := setAttr(r, "SoundFile", gadb.TString, gadb.Str(file)); err != nil {
			return err
		}
		links = append(links, gadb.Link("Sound/Sound", name))
	}
	for _, cat := range []string{"Arsenal/Weapons/Default", "Arsenal/Weapons/AI", "Arsenal/Weapons/Multiplayer"} {
		r := db.Find(cat, Prefix+"AutoShotgun")
		if r == nil {
			return fmt.Errorf("%s/%sAutoShotgun missing", cat, Prefix)
		}
		a := r.Attr("LocalFireSound.0.ReloadSounds")
		if a == nil || a.Type != gadb.TLink || len(a.Values) != 3 || !gadb.Equal(gadb.TLink, a.Values[0], links[0]) ||
			!a.Values[1].Null || !a.Values[2].Null {
			return fmt.Errorf("%s/%sAutoShotgun: unexpected ReloadSounds", cat, Prefix)
		}
		a.Values = append([]gadb.Value(nil), links...)
	}
	return nil
}

// BuildLive applies stage 4 to the stage 3 databases and verifies the result: every category sorted, every stock
// and stage 3 record at its index, and only the intended stock / stage 3 records changed.
func BuildLive(in LiveInputs) (*LiveResult, error) {
	log := in.Log
	if log == nil {
		log = NewLog()
	}
	d, err := readSym(in.HolidayFEAR)
	if err != nil {
		return nil, err
	}
	dl, err := readSym(in.HolidayFEARL)
	if err != nil {
		return nil, err
	}
	stock, err := readSym(in.PC)
	if err != nil {
		return nil, err
	}
	stockL, err := readSym(in.PCL)
	if err != nil {
		return nil, err
	}
	ref, refL := d.Clone(), dl.Clone()

	// stock records the earlier stages changed (stage 3: Arsenal/Global/Global, the SM15 drop fix; their own
	// checks pin them) stay accepted; everything this stage changes is listed explicitly below.
	wantStock, wantStockL := changedRecords(ref, stock), changedRecords(refL, stockL)
	var wantRef, wantRefL []string
	if !in.NoCampaignPlayer {
		if err := applyCampaignPlayer(d); err != nil {
			return nil, fmt.Errorf("campaign player: %w", err)
		}
		wantStock = append(wantStock, "Character/Models/Player")
		wantRef = append(wantRef, "Character/Models/Player")
		log.Add("live", "campaign player: Character/Models/Player AnimationTree[2] = "+UpperTree+", ModelFile = "+CampaignModel)
	}
	if in.InstantAction {
		if err := applyIAMain(d, in.iaMission); err != nil {
			return nil, fmt.Errorf("Instant Action: %w", err)
		}
		if err := applyIALoc(dl); err != nil {
			return nil, fmt.Errorf("Instant Action (FEARL): %w", err)
		}
		wantStockL = append(wantStockL, "Interface/Screens/ScreenFailure")
		wantRefL = append(wantRefL, "Interface/Screens/ScreenFailure")
		log.Add("live", "Instant Action: 12 FEAR records, 4 FEARL load screens, ScreenFailure layout")
	}
	if in.WatsonReload {
		if err := applyWatsonReload(d); err != nil {
			return nil, fmt.Errorf("Watson reload: %w", err)
		}
		for _, c := range []string{"Arsenal/Weapons/AI", "Arsenal/Weapons/Default", "Arsenal/Weapons/Multiplayer"} {
			wantRef = append(wantRef, c+"/"+Prefix+"AutoShotgun")
		}
		log.Add("live", "Watson reload: Sound/Sound "+Prefix+"Watson_Reload2/3, "+Prefix+"AutoShotgun ReloadSounds x3")
	}

	res := &LiveResult{}
	for _, tgt := range []struct {
		label           string
		d, ref, stk     *gadb.Database
		wantStk, wantRf []string
		out             *[]byte
	}{{"FEAR", d, ref, stock, wantStock, wantRef, &res.FEAR}, {"FEARL", dl, refL, stockL, wantStockL, wantRefL, &res.FEARL}} {
		if err := checkSortedErr(tgt.d, tgt.label); err != nil {
			return nil, err
		}
		if err := checkKept(tgt.label, tgt.d, tgt.stk, tgt.wantStk); err != nil {
			return nil, fmt.Errorf("versus stock: %w", err)
		}
		if err := checkKept(tgt.label, tgt.d, tgt.ref, tgt.wantRf); err != nil {
			return nil, fmt.Errorf("versus stage 3: %w", err)
		}
		// every record beyond the stage 3 ones is new and carries the prefix
		for ci, c := range tgt.d.Cats {
			for _, r := range c.Records[len(tgt.ref.Cats[ci].Records):] {
				if !strings.HasPrefix(r.Name, Prefix) {
					return nil, fmt.Errorf("%s: added record %s/%s lacks the %s prefix", tgt.label, c.Name, r.Name, Prefix)
				}
			}
		}
		data, err := encodeWrite(tgt.d)
		if err != nil {
			return nil, err
		}
		*tgt.out = data
	}
	return res, nil
}

// checkKept asserts that d has base's categories in order, every base record at its index, and that exactly the
// records named in want (sorted "cat/rec") changed their values.
func checkKept(label string, d, base *gadb.Database, want []string) error {
	if len(d.Cats) != len(base.Cats) {
		return fmt.Errorf("%s: category count changed", label)
	}
	var changed []string
	for ci, c := range base.Cats {
		dc := d.Cats[ci]
		if dc.Name != c.Name {
			return fmt.Errorf("%s: category %d is %s, was %s", label, ci, dc.Name, c.Name)
		}
		if len(dc.Records) < len(c.Records) {
			return fmt.Errorf("%s: records removed from %s", label, c.Name)
		}
		for ri, r := range c.Records {
			if dc.Records[ri].Name != r.Name {
				return fmt.Errorf("%s: record %s/%s moved", label, c.Name, r.Name)
			}
			if !sigEqual(dc.Records[ri], r) {
				changed = append(changed, c.Name+"/"+r.Name)
			}
		}
	}
	w := append([]string(nil), want...)
	sort.Strings(w)
	sort.Strings(changed)
	if strings.Join(changed, "|") != strings.Join(w, "|") {
		return fmt.Errorf("%s: changed records %v, expected %v", label, changed, w)
	}
	return nil
}

// changedRecords lists ("cat/rec") the records of base whose values differ in d (same categories assumed).
func changedRecords(d, base *gadb.Database) []string {
	var out []string
	dr := d.RecMap()
	for _, c := range base.Cats {
		for _, r := range c.Records {
			if x := dr[gadb.RecKey{Cat: c.Name, Rec: r.Name}]; x == nil || !sigEqual(x, r) {
				out = append(out, c.Name+"/"+r.Name)
			}
		}
	}
	return out
}
