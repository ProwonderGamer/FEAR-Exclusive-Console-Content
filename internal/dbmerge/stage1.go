package dbmerge

import (
	"fmt"
	"sort"

	"fearinstaller/internal/gadb"
	"fearinstaller/internal/wld"
)

// Prefix of every added record: sorts after every stock name of its category (asserted).
const Prefix = "zSFODD_"

// Stage1Inputs are the original files needed by the first stage (port of the reference Python stage).
type Stage1Inputs struct {
	PC, PCL  []byte   // stock FEAR.gamdb00p, FEARL.Gamdb00p (v1.08)
	PS3DB    []byte   // PS3 database/loki.gamedb
	PS3World []byte   // PS3 worlds/release/sfod_d.wldsrvr
	PCWorlds [][]byte // stock PC worlds used to learn property categories (Admin, Aftermath, Moody, ATC_Roof, Mapes_Elevator)
	TOC      TOC
	Log      *Log
}

// Stage1Result holds the merged intermediate databases (final, link-encoded files).
type Stage1Result struct {
	FEAR, FEARL []byte
	Added       int
	Missing     []string
}

var seedRecords = [][2]string{
	{"Missions/Missions", "Bonus_SFOD-D"}, {"Missions/Levels", "Bonus_SFOD-D"},
	{"Character/Models", "Player_DeltaForce"}, {"Character/Models", "Corpse_office_Bob_Alive"},
	{"Interface/Objective", "Objective46"}, {"Interface/Objective", "Objective47"},
}

var seedNamesAllCategories = map[string]bool{"AutoShotgun": true, "Machine Pistol": true, "MachinePistol": true}

// TemplatesSFODD are the explicit PC template records for the SFOD-D records.
func TemplatesSFODD() map[[2]string]string {
	return map[[2]string]string{
		{"*", "AutoShotgun"}: "Shotgun", {"*", "Machine Pistol"}: "Pistol", {"*", "MachinePistol"}: "Pistol",
		{"*", "Dual Machine Pistols"}: "Dual Pistols", {"AI/Weapons", "MachinePistol"}: "Submachinegun",
		{"Character/Models", "Player_DeltaForce"}: "Player", {"Character/Models", "Corpse_office_Bob_Alive"}: "Corpse_office_Bob",
		{"Missions/Missions", "Bonus_SFOD-D"}: "08_Admin", {"Missions/Levels", "Bonus_SFOD-D"}: "Admin",
		{"Interface/Objective", "*"}: "Objective45", {"Interface/Credits/Order", "*"}: "Objective45",
		{"Interface/Credits/Entries", "*"}: "Objective45",
	}
}

func linkIndex(vals []gadb.Value, v gadb.Value) int {
	for i, x := range vals {
		if gadb.Equal(gadb.TLink, x, v) {
			return i
		}
	}
	return -1
}

func readSym(b []byte) (*gadb.Database, error) {
	db, err := gadb.Read(b)
	if err != nil {
		return nil, err
	}
	if err := gadb.Symbolize(db); err != nil {
		return nil, err
	}
	return db, nil
}

func checkSortedErr(db *gadb.Database, label string) error {
	if bad := gadb.CheckSorted(db, label); len(bad) > 0 {
		return fmt.Errorf("%s", bad[0])
	}
	return nil
}

func encodeWrite(db *gadb.Database) ([]byte, error) {
	if err := gadb.EncodeLinks(db); err != nil {
		return nil, err
	}
	return gadb.Write(db, false)
}

// BuildStage1 merges the PS3-only records of the SFOD-D mission (weapons AutoShotgun / Machine Pistol, the
// player and civilian models, objectives, mission and level, and everything the world names) into the stock
// PC database, inserting them at their sorted positions, and adds the load screen layout to FEARL.
func BuildStage1(in Stage1Inputs) (*Stage1Result, error) {
	log := in.Log
	if log == nil {
		log = NewLog()
	}
	pc, err := readSym(in.PC)
	if err != nil {
		return nil, fmt.Errorf("stock FEAR.gamdb00p: %w", err)
	}
	ps3, err := readSym(in.PS3DB)
	if err != nil {
		return nil, fmt.Errorf("PS3 loki.gamedb: %w", err)
	}
	// The PC database must be sorted (the engine binary-searches it); the console file only has to be readable
	// (it is sorted by a different rule inside some records), so its state is just logged.
	if err := checkSortedErr(pc, "PC"); err != nil {
		return nil, fmt.Errorf("stock database not sorted: %w", err)
	}
	if bad := gadb.CheckSorted(ps3, "PS3"); len(bad) > 0 {
		log.Add("input checks", fmt.Sprintf("PS3 database is not strictly sorted (%d findings, first: %s)", len(bad), bad[0]))
	}
	m := NewMerger(pc, ps3, in.TOC, log, TemplatesSFODD())
	for _, s := range seedRecords {
		if err := m.Want(s[0], s[1], "seed (task brief / mission)"); err != nil {
			return nil, err
		}
	}
	for _, c := range ps3.Cats {
		for _, r := range c.Records {
			if seedNamesAllCategories[r.Name] {
				if err := m.Want(c.Name, r.Name, "seed (weapon record in every PS3 category)"); err != nil {
					return nil, err
				}
			}
		}
	}
	// every console-only record the world names
	_, wobjs, err := wld.ParseConsoleServer(in.PS3World)
	if err != nil {
		return nil, fmt.Errorf("sfod_d.wldsrvr: %w", err)
	}
	var pcw [][]wld.Object
	for _, b := range in.PCWorlds {
		o, _, _, err := wld.ParsePCWorld(b)
		if err != nil {
			return nil, fmt.Errorf("stock PC world: %w", err)
		}
		pcw = append(pcw, o)
	}
	for _, w := range WorldRefs(wobjs, ps3, pc, pcw) {
		if err := m.Want(w.Cat, w.Value, fmt.Sprintf("named by world %s.%s = %q", w.Class, w.Prop, w.Value)); err != nil {
			return nil, err
		}
	}
	if err := m.Run(); err != nil {
		return nil, err
	}
	// the world file name differs on PC
	lv := m.Added[gadb.RecKey{Cat: "Missions/Levels", Rec: "Bonus_SFOD-D"}]
	if lv == nil || lv.Attr("World") == nil {
		return nil, fmt.Errorf("Missions/Levels/Bonus_SFOD-D.World missing")
	}
	lv.Attr("World").Values = []gadb.Value{gadb.Str(`Worlds\Release\SFOD_D.World00p`)}
	delete(m.MissingFiles, `Worlds\Release\SFOD_D.World00p`)

	// the player can only own weapons listed in Arsenal/Global PlayerWeapons; the console lists AutoShotgun
	// there (and in WeaponPriority). Mirror the console placement on the PC lists.
	gpc, gps := m.pcrec[gadb.RecKey{Cat: "Arsenal/Global", Rec: "Global"}], m.ps3rc[gadb.RecKey{Cat: "Arsenal/Global", Rec: "Global"}]
	if gpc == nil || gps == nil {
		return nil, fmt.Errorf("Arsenal/Global/Global missing")
	}
	for _, an := range []string{"PlayerWeapons", "WeaponPriority"} {
		pa, sa := gpc.Attr(an), gps.Attr(an)
		if pa == nil || sa == nil {
			return nil, fmt.Errorf("Arsenal/Global/Global.%s missing", an)
		}
		var added []gadb.Value
		for _, v := range sa.Values {
			if v.Null {
				continue
			}
			if _, ok := m.Added[gadb.RecKey{Cat: v.Cat, Rec: v.Rec}]; ok {
				added = append(added, v)
			}
		}
		for _, v := range added {
			i := linkIndex(sa.Values, v)
			pos := len(pa.Values)
			if i > 0 {
				if j := linkIndex(pa.Values, sa.Values[i-1]); j >= 0 {
					pos = j + 1
				}
			} else if j := linkIndex(pa.Values, gadb.NoLinkSym()); j >= 0 {
				pos = j + 1 // Python: prev=None, found as the first <none> entry
			}
			pa.Values = append(pa.Values, gadb.Value{})
			copy(pa.Values[pos+1:], pa.Values[pos:])
			pa.Values[pos] = v
		}
	}
	// insert at the sorted position
	keys := append([]gadb.RecKey(nil), m.AddedOrder...)
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if gadb.SKey(a.Cat) != gadb.SKey(b.Cat) {
			return gadb.SKey(a.Cat) < gadb.SKey(b.Cat)
		}
		return gadb.SKey(a.Rec) < gadb.SKey(b.Rec)
	})
	for _, k := range keys {
		pos, n, err := gadb.InsertSorted(pc, k.Cat, m.Added[k])
		if err != nil {
			return nil, err
		}
		log.Add("added records", fmt.Sprintf("%s/%s (inserted at index %d of %d; %s)", k.Cat, k.Rec, pos, n, m.Reason[k]))
	}
	if err := checkSortedErr(pc, "merged"); err != nil {
		return nil, err
	}
	fear, err := encodeWrite(pc)
	if err != nil {
		return nil, err
	}

	// ---- FEARL: the load screen layout of the mission ----
	fl, err := readSym(in.PCL)
	if err != nil {
		return nil, fmt.Errorf("stock FEARL.Gamdb00p: %w", err)
	}
	const lcat = "Interface/LoadScreen"
	psB, psD := ps3.Find(lcat, "Bonus_Holiday"), ps3.Find(lcat, "Default")
	if psB == nil || psD == nil {
		return nil, fmt.Errorf("PS3 %s/Bonus_Holiday or Default missing", lcat)
	}
	var diff []string
	for _, a := range psB.Attrs {
		b := psD.Attr(a.Name)
		if b == nil || !sameValues(a, b) {
			diff = append(diff, a.Name)
		}
	}
	if len(diff) != 1 || diff[0] != "BackgroundTexture" {
		return nil, fmt.Errorf("PS3 Bonus_Holiday differs from PS3 Default in %v; review build_fearl", diff)
	}
	def := fl.Find(lcat, "Default")
	if def == nil {
		return nil, fmt.Errorf("FEARL %s/Default missing", lcat)
	}
	nr := def.Copy()
	nr.Name = "Bonus_Holiday"
	tex := psB.Attr("BackgroundTexture").Values[0].S
	base, ext := splitExt(tex)
	pcv := base + ext
	if rep, ok := extMap[gadb.Lower(ext)]; ok {
		pcv = base + rep
	}
	found := in.TOC.Find(pcv)
	if found == "" {
		m.addMissing(pcv, "FEARL "+lcat+"/Bonus_Holiday.BackgroundTexture")
		found = pcv
	}
	nr.Attr("BackgroundTexture").Values = []gadb.Value{gadb.Str(found)}
	if _, _, err := gadb.InsertSorted(fl, lcat, nr); err != nil {
		return nil, err
	}
	if err := checkSortedErr(fl, "FEARL merged"); err != nil {
		return nil, err
	}
	fearl, err := encodeWrite(fl)
	if err != nil {
		return nil, err
	}
	res := &Stage1Result{FEAR: fear, FEARL: fearl, Added: len(m.Added), Missing: m.MissingOrder}
	return res, nil
}

func sameValues(a, b *gadb.Attr) bool {
	if a.Type != b.Type || len(a.Values) != len(b.Values) {
		return false
	}
	for i := range a.Values {
		if !gadb.Equal(a.Type, a.Values[i], b.Values[i]) {
			return false
		}
	}
	return true
}
