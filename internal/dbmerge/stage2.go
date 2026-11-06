package dbmerge

import (
	"fmt"
	"sort"
	"strings"

	"fearinstaller/internal/gadb"
)

const (
	missionSFOD = "Bonus_SFOD-D"
	// UpperTree is the merged Player_Upper tree that carries the console-only weapon branches.
	UpperTree = `AnimationDatabase\AnimTrees\Player_Upper_SFODD.AnmTree00p`
)

var animProps = map[string]string{"WEAP_SM15": "WEAP_G00", "WEAP_DUALSM15": "WEAP_G01", "WEAP_AUTOSHOTGUN": "WEAP_G02"}

// radiiSibling: console-only sound record -> PC sibling whose hearing distances it takes (the console halves
// all weapon radii).
var radiiSibling = [][2]string{
	{"SM15_Fire", "SMG_Fire"}, {"SM15_Fire_AI", "SMG_Fire_AI"}, {"SM15_FireLoop", "SMG_FireLoop"},
	{"SM15_FireLoop_AI", "SMG_FireLoop_AI"}, {"SM15_Select", "SMG_Select"}, {"SM15_Deselect", "SMG_Deselect"},
	{"SM15_Reload1", "SMG_Reload1"}, {"SM15_Reload2", "SMG_Reload2"}, {"SM15_Reload3", "SMG_Reload3"},
	{"Watson_Fire", "Shotgun_Fire"}, {"Watson_Reload1", "Shotgun_Reload1"},
}

// textIDAttrs: string attributes whose value is a STRING-DATABASE key that merely equals a record name.
var textIDAttrs = map[[2]string]bool{{"Interface/Objective", "Objective"}: true, {"Interface/Credits/Entries", "Lines.0.Line"}: true}

// StringKeys is what the stage needs from the string database (to check that text ids are real keys).
type StringKeys interface{ HasKey(key string) bool }

// renameAdded renames the records the stock database lacks to Prefix+name, re-targets every link to them and
// moves them to the end of their category (sorted). Returns the (cat, old) -> new map in insertion order.
func renameAdded(db, stock *gadb.Database) (map[gadb.RecKey]string, []gadb.RecKey, error) {
	have := stock.RecMap()
	ren := map[gadb.RecKey]string{}
	var order []gadb.RecKey
	for _, c := range db.Cats {
		for _, r := range c.Records {
			k := gadb.RecKey{Cat: c.Name, Rec: r.Name}
			if _, ok := have[k]; !ok {
				ren[k] = Prefix + r.Name
				order = append(order, k)
			}
		}
	}
	for _, c := range db.Cats {
		for _, r := range c.Records {
			for _, a := range r.Attrs {
				if a.Type != gadb.TLink {
					continue
				}
				for i, v := range a.Values {
					if v.Null {
						continue
					}
					if n, ok := ren[gadb.RecKey{Cat: v.Cat, Rec: v.Rec}]; ok {
						a.Values[i] = gadb.Link(v.Cat, n)
					}
				}
			}
		}
		for _, r := range c.Records {
			if n, ok := ren[gadb.RecKey{Cat: c.Name, Rec: r.Name}]; ok {
				r.Name = n
			}
		}
		var stockRecs, newRecs []*gadb.Record
		for _, r := range c.Records {
			if strings.HasPrefix(r.Name, Prefix) {
				newRecs = append(newRecs, r)
			} else {
				stockRecs = append(stockRecs, r)
			}
		}
		sort.SliceStable(newRecs, func(i, j int) bool { return gadb.SKey(newRecs[i].Name) < gadb.SKey(newRecs[j].Name) })
		if len(newRecs) > 0 && len(stockRecs) > 0 && !(gadb.SKey(newRecs[0].Name) > gadb.SKey(stockRecs[len(stockRecs)-1].Name)) {
			return nil, nil, fmt.Errorf("%s: %s does not sort after %s", c.Name, newRecs[0].Name, stockRecs[len(stockRecs)-1].Name)
		}
		c.Records = append(stockRecs, newRecs...)
	}
	return ren, order, nil
}

type strHit struct{ cat, rec, attr, val string }

func scanStringRefs(db *gadb.Database, names map[string]bool) []strHit {
	var hits []strHit
	for _, c := range db.Cats {
		for _, r := range c.Records {
			for _, a := range r.Attrs {
				if a.Type != gadb.TString {
					continue
				}
				for _, v := range a.Values {
					if !v.Null && names[gadb.Lower(v.S)] {
						hits = append(hits, strHit{c.Name, r.Name, a.Name, v.S})
					}
				}
			}
		}
	}
	return hits
}

func recNameSet(db *gadb.Database) map[string]bool {
	s := map[string]bool{}
	for _, c := range db.Cats {
		for _, r := range c.Records {
			s[r.Name] = true
		}
	}
	return s
}

// verifyStockUnchanged checks that every stock record keeps its index and returns the "cat/rec.attr" list of
// stock attributes whose value changed.
func verifyStockUnchanged(d, st *gadb.Database) ([]string, error) {
	if len(d.Cats) != len(st.Cats) {
		return nil, fmt.Errorf("category count changed")
	}
	var changed []string
	for ci, c := range st.Cats {
		dc := d.Cats[ci]
		if dc.Name != c.Name {
			return nil, fmt.Errorf("category %d is %s, expected %s", ci, dc.Name, c.Name)
		}
		for ri, r := range c.Records {
			if ri >= len(dc.Records) || dc.Records[ri].Name != r.Name {
				return nil, fmt.Errorf("stock record %s/%s moved", c.Name, r.Name)
			}
			dr := dc.Records[ri]
			if len(dr.Attrs) != len(r.Attrs) {
				return nil, fmt.Errorf("%s/%s: attribute count changed", c.Name, r.Name)
			}
			for i, a := range r.Attrs {
				b := dr.Attrs[i]
				if a.Name != b.Name || a.Type != b.Type || a.Flags != b.Flags || !sameValues(a, b) {
					changed = append(changed, c.Name+"/"+r.Name+"."+a.Name)
				}
			}
		}
	}
	return changed, nil
}

// Stage2Inputs are the inputs of the SFOD-D stage (port of the reference Python stage).
type Stage2Inputs struct {
	Stage1  *Stage1Result
	PC, PCL []byte     // stock databases
	Strings StringKeys // the SFOD-D string database (text ids must be keys)
	// DropFix lists the SM15 records in Arsenal/Global PlayerWeapons so AI carrying them drop them on death
	// (the SFOD-D "update 3" database). Holiday (stage 3) applies the same fix itself, on the unfixed base.
	DropFix bool
	Log     *Log
}

// Stage2Result are the SFOD-D databases.
type Stage2Result struct {
	FEAR, FEARL []byte
	RenamedFEAR map[gadb.RecKey]string
}

// BuildStage2 renames every added record to the sort-last "zSFODD_" name (so every stock record keeps its
// index), adds the bonus mission order, wires the player animation tree and the PC weapon sound radii.
func BuildStage2(in Stage2Inputs) (*Stage2Result, error) {
	stock, err := readSym(in.PC)
	if err != nil {
		return nil, err
	}
	db, err := readSym(in.Stage1.FEAR)
	if err != nil {
		return nil, err
	}
	stockL, err := readSym(in.PCL)
	if err != nil {
		return nil, err
	}
	dbl, err := readSym(in.Stage1.FEARL)
	if err != nil {
		return nil, err
	}
	oldNames := map[string]bool{}
	st := recNameSet(stock)
	for n := range recNameSet(db) {
		if !st[n] {
			oldNames[gadb.Lower(n)] = true
		}
	}
	renL, _, err := renameAdded(dbl, stockL)
	if err != nil {
		return nil, err
	}
	layoutRen := map[string]string{}
	for k, n := range renL {
		if k.Cat == "Interface/LoadScreen" {
			layoutRen[k.Rec] = n
			oldNames[gadb.Lower(k.Rec)] = true
		}
	}
	ren, _, err := renameAdded(db, stock)
	if err != nil {
		return nil, err
	}
	m := db.RecMap()
	mis := m[gadb.RecKey{Cat: "Missions/Missions", Rec: Prefix + missionSFOD}]
	if mis == nil {
		return nil, fmt.Errorf("Missions/Missions/%s%s missing", Prefix, missionSFOD)
	}
	// every string equal to an old record name must be a known reference
	hits := scanStringRefs(db, oldNames)
	for _, h := range hits {
		if textIDAttrs[[2]string{h.cat, h.attr}] {
			if in.Strings != nil && !in.Strings.HasKey(h.val) {
				return nil, fmt.Errorf("%v: not a string-database key", h)
			}
			continue
		}
		if h.cat == "Missions/Missions" && strings.Replace(h.rec, Prefix, "", 1) == missionSFOD && (h.attr == "Layout" || h.attr == "BriefingLayout") {
			continue
		}
		return nil, fmt.Errorf("unexpected string reference to a renamed record: %s/%s.%s = %q", h.cat, h.rec, h.attr, h.val)
	}
	for _, an := range []string{"Layout", "BriefingLayout"} {
		a := mis.Attr(an)
		if a == nil {
			return nil, fmt.Errorf("mission record lacks %s", an)
		}
		for i, v := range a.Values {
			if n, ok := layoutRen[v.S]; ok {
				a.Values[i] = gadb.Str(n)
			}
		}
	}
	// bonus mission order
	tmpl := m[gadb.RecKey{Cat: "Missions/MissionOrder", Rec: "MissionOrder"}]
	if tmpl == nil {
		return nil, fmt.Errorf("Missions/MissionOrder/MissionOrder missing")
	}
	order := tmpl.Copy()
	order.Name = Prefix + missionSFOD
	order.Attr("Missions").Values = []gadb.Value{gadb.Link("Missions/Missions", mis.Name)}
	oc := db.Cats[db.CatIndex("Missions/MissionOrder")]
	if !(gadb.SKey(order.Name) > gadb.SKey(oc.Records[len(oc.Records)-1].Name)) {
		return nil, fmt.Errorf("mission order record does not sort last")
	}
	oc.Records = append(oc.Records, order)
	// player animations (Player_Upper_SFODD tree)
	pm := m[gadb.RecKey{Cat: "Character/Models", Rec: Prefix + "Player_DeltaForce"}]
	if pm == nil {
		return nil, fmt.Errorf("player model record missing")
	}
	at := pm.Attr("AnimationTree")
	if at == nil || len(at.Values) < 3 || !strings.HasSuffix(gadb.Lower(at.Values[2].S), "player_upper.anmtree00p") {
		return nil, fmt.Errorf("unexpected player AnimationTree")
	}
	at.Values[2] = gadb.Str(UpperTree)
	n := 0
	for _, c := range db.Cats {
		for _, r := range c.Records {
			a := r.Attr("AnimationProperty")
			if strings.HasPrefix(r.Name, Prefix) && a != nil && len(a.Values) > 0 {
				if w, ok := animProps[a.Values[0].S]; ok {
					a.Values = []gadb.Value{gadb.Str(w)}
					n++
				}
			}
		}
	}
	if n != 8 {
		return nil, fmt.Errorf("expected 8 records with console-only weapon animation properties, found %d", n)
	}
	// PC sound radii
	snd := db.Cats[db.CatIndex("Sound/Sound")]
	byName := map[string]*gadb.Record{}
	for _, r := range snd.Records {
		byName[r.Name] = r
	}
	for _, p := range radiiSibling {
		r, s := byName[Prefix+p[0]], byName[p[1]]
		if r == nil || s == nil {
			return nil, fmt.Errorf("sound records %s / %s missing", p[0], p[1])
		}
		for _, f := range []string{"InnerRadius", "OuterRadius"} {
			ra, sa := r.Attr(f), s.Attr(f)
			if ra == nil || sa == nil {
				return nil, fmt.Errorf("%s.%s missing", p[0], f)
			}
			ra.Values = append([]gadb.Value(nil), sa.Values...)
		}
	}
	if in.DropFix {
		pw := db.Find("Arsenal/Global", "Global").Attr("PlayerWeapons")
		for _, w := range []string{Prefix + "Machine Pistol", Prefix + "Dual Machine Pistols"} {
			v := gadb.Link("Arsenal/Weapons", w)
			if db.Find("Arsenal/Weapons", w) == nil || linkIndex(pw.Values, v) >= 0 {
				return nil, fmt.Errorf("unexpected PlayerWeapons state for %s", w)
			}
			pw.Values = append(pw.Values, v)
		}
	}
	// verify + write
	res := &Stage2Result{RenamedFEAR: ren}
	for label, tgt := range map[string]struct {
		d, st *gadb.Database
		out   *[]byte
		want  []string
	}{
		"FEAR":  {db, stock, &res.FEAR, []string{"Arsenal/Global/Global.PlayerWeapons", "Arsenal/Global/Global.WeaponPriority"}},
		"FEARL": {dbl, stockL, &res.FEARL, nil},
	} {
		if err := checkSortedErr(tgt.d, label); err != nil {
			return nil, err
		}
		changed, err := verifyStockUnchanged(tgt.d, tgt.st)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		if strings.Join(changed, "|") != strings.Join(tgt.want, "|") {
			return nil, fmt.Errorf("%s: unexpected changes to stock records: %v", label, changed)
		}
		data, err := encodeWrite(tgt.d)
		if err != nil {
			return nil, err
		}
		*tgt.out = data
	}
	return res, nil
}
