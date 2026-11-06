package dbmerge

import (
	"sort"
	"strings"

	"fearinstaller/internal/gadb"
	"fearinstaller/internal/wld"
)

// WorldWant is a console-only database record named by a console world.
type WorldWant struct {
	Cat, Value string
	Kind       string // "prop" or "cmd"
	Class      string
	Prop       string
}

var sentinels = map[string]bool{"": true, "none": true, "<none>": true, "default": true}

// propCategory: properties whose category cannot be inferred from the values alone.
var propCategory = map[string]string{"SurfaceFlagsOverride": "Surfaces/Surfaces", "SurfaceOverride": "Surfaces/Surfaces"}

// cmdArgCategory: command verb -> category the argument names ("" = not a database record).
var cmdArgCategory = map[string]string{
	"changeweapon": "Arsenal/Weapons", "acquireweapon": "Arsenal/Weapons", "acquiregear": "Arsenal/Gear",
	"acquireammo": "Arsenal/Ammo", "objective": "Interface/Objective", "slowmo": "SlowMo", "cineract": "", "fx": "",
}

type nameIndex map[string][]string // lower-case record name -> categories (sorted)

func indexDB(db *gadb.Database) nameIndex {
	idx := map[string]map[string]bool{}
	for _, c := range db.Cats {
		for _, r := range c.Records {
			k := gadb.Lower(r.Name)
			if idx[k] == nil {
				idx[k] = map[string]bool{}
			}
			idx[k][c.Name] = true
		}
	}
	out := nameIndex{}
	for k, s := range idx {
		for c := range s {
			out[k] = append(out[k], c)
		}
		sort.Strings(out[k])
	}
	return out
}

func tokenize(cmd string) []string {
	var out []string
	cur := []byte{}
	isSep := func(c byte) bool {
		switch c {
		case ' ', '\t', '\n', '\r', '\f', '\v', 0x1c, 0x1d, 0x1e, 0x1f, 0x85, 0xa0, ';', '(', ')':
			return true
		}
		return false
	}
	for i := 0; i < len(cmd); i++ {
		if isSep(cmd[i]) {
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = cur[:0]
			}
			continue
		}
		cur = append(cur, cmd[i])
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func slashes(s string) int { return strings.Count(s, "/") }

// WorldRefs lists every database record a console world (its objects) names that the console database has and
// the PC database lacks. ps3 and pc must be the UNsymbolized or symbolized databases (only names are used).
// pcWorlds are the stock PC worlds used to learn which category a property names.
func WorldRefs(objs []wld.Object, ps3, pc *gadb.Database, pcWorlds [][]wld.Object) []WorldWant {
	psx, pcx := indexDB(ps3), indexDB(pc)
	pcSet := map[string]map[string]bool{}
	for _, c := range pc.Cats {
		s := map[string]bool{}
		for _, r := range c.Records {
			s[gadb.Lower(r.Name)] = true
		}
		pcSet[c.Name] = s
	}
	pcHas := func(cat, name string) bool { return pcSet[cat][gadb.Lower(name)] }

	// categories learned from the PC worlds: (class, prop) -> category -> count
	type cp struct{ cls, prop string }
	learned := map[cp]map[string]int{}
	for _, wo := range pcWorlds {
		for _, o := range wo {
			for _, p := range o.Props {
				if (p.Type == wld.TString || p.Type == wld.TStringID) && p.Name != "Name" && !sentinels[gadb.Lower(p.S)] {
					for _, c := range pcx[gadb.Lower(p.S)] {
						k := cp{o.Class, p.Name}
						if learned[k] == nil {
							learned[k] = map[string]int{}
						}
						learned[k][c]++
					}
				}
			}
		}
	}
	// pass 1: string properties
	byProp := map[cp]map[string]bool{}
	for _, o := range objs {
		for _, p := range o.Props {
			if (p.Type == wld.TString || p.Type == wld.TStringID) && p.Name != "Name" {
				k := cp{o.Class, p.Name}
				if byProp[k] == nil {
					byProp[k] = map[string]bool{}
				}
				byProp[k][p.S] = true
			}
		}
	}
	keys := make([]cp, 0, len(byProp))
	for k := range byProp {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].cls != keys[j].cls {
			return keys[i].cls < keys[j].cls
		}
		return keys[i].prop < keys[j].prop
	})
	var out []WorldWant
	for _, k := range keys {
		vals := byProp[k]
		var vlist []string
		for v := range vals {
			vlist = append(vlist, v)
		}
		sort.Strings(vlist)
		score := map[string]int{}
		var scoreOrder []string
		for _, v := range vlist {
			if sentinels[gadb.Lower(v)] {
				continue
			}
			for _, c := range psx[gadb.Lower(v)] {
				if _, ok := score[c]; !ok {
					scoreOrder = append(scoreOrder, c)
				}
				score[c]++
			}
		}
		if len(score) == 0 {
			continue
		}
		if pcat, ok := propCategory[k.prop]; ok {
			score = map[string]int{pcat: len(vlist)}
			scoreOrder = []string{pcat}
		}
		cat, hits := "", -1
		for _, c := range scoreOrder {
			n := score[c]
			if hits < 0 || n > hits || (n == hits && slashes(c) < slashes(cat)) {
				cat, hits = c, n
			}
		}
		if _, special := propCategory[k.prop]; !special {
			if lc := learned[k]; len(lc) > 0 {
				top := 0
				for _, n := range lc {
					if n > top {
						top = n
					}
				}
				var tied []string
				for c, n := range lc {
					if n == top {
						tied = append(tied, c)
					}
				}
				sort.Strings(tied)
				pcCat := ""
				bs, bsl := -1, 0
				for _, c := range tied {
					s := score[c]
					if bs < 0 || s > bs || (s == bs && slashes(c) < bsl) {
						pcCat, bs, bsl = c, s, slashes(c)
					}
				}
				if top >= 2 || score[pcCat] > 0 {
					h := score[pcCat]
					if h < 1 {
						h = 1
					}
					cat, hits = pcCat, h
				}
			}
		}
		_ = hits
		for _, v := range vlist {
			if sentinels[gadb.Lower(v)] {
				continue
			}
			inPS3 := contains(psx[gadb.Lower(v)], cat)
			if inPS3 && !pcHas(cat, v) {
				out = append(out, WorldWant{Cat: cat, Value: v, Kind: "prop", Class: k.cls, Prop: k.prop})
			}
		}
	}
	// pass 2: commands
	for _, o := range objs {
		for _, p := range o.Props {
			if p.Type != wld.TCommand || p.S == "" {
				continue
			}
			toks := tokenize(p.S)
			for i, tok := range toks {
				lt := gadb.Lower(tok)
				cat, isVerb := cmdArgCategory[lt]
				if !isVerb || i+1 >= len(toks) {
					continue
				}
				j := i + 1
				if lt == "objective" && j < len(toks) {
					switch gadb.Lower(toks[j]) {
					case "add", "remove", "complete", "completed":
						j++
					}
				}
				if lt == "slowmo" {
					if gadb.Lower(toks[j]) != "on" {
						continue
					}
					j++
				}
				if j >= len(toks) {
					continue
				}
				arg := toks[j]
				if cat == "" {
					continue
				}
				inPS3 := contains(psx[gadb.Lower(arg)], cat)
				if inPS3 && !pcHas(cat, arg) {
					out = append(out, WorldWant{Cat: cat, Value: arg, Kind: "cmd", Class: o.Class, Prop: p.Name})
				}
			}
		}
	}
	return out
}
