package anmtree

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Player_Upper_SFODD: a PC Player_Upper tree that answers exactly like the stock PC tree for every PC
// weapon and exactly like the PS3 tree for the three PS3-only weapons (SM15 machine pistol, dual SM15, Watson
// SAS-12). Port of the project's Python builder, which was validated against both trees.
//
//	trie[0] Weapon==WEAP_G00 ? <PS3 trie specialised to WEAP_SM15>        : trie[1]
//	trie[1] Weapon==WEAP_G01 ? <PS3 trie specialised to WEAP_DUALSM15>    : trie[2]
//	trie[2] Weapon==WEAP_G02 ? <PS3 trie specialised to WEAP_AUTOSHOTGUN> : trie[3]
//	trie[3..] the stock PC trie (indices shifted by 3, prop indices remapped)
//
// PS3 leaves reuse a PC node with the same content or are appended (props remapped by name, the three weapon
// props renamed to the compiled-in, otherwise unused PC props WEAP_G00/G01/G02; animation records copied with
// the PC player-tree blend convention: BlendDuration 0.0, weight set '' and flags 0). Strings and props are only
// appended; the stock PC part keeps its records and string offsets.

// newWeapons maps the PS3 weapon props to the PC props they take, in the order of the dispatch tests.
var newWeapons = [][2]string{{"WEAP_SM15", "WEAP_G00"}, {"WEAP_DUALSM15", "WEAP_G01"}, {"WEAP_AUTOSHOTGUN", "WEAP_G02"}}

func rn(p string) string {
	for _, nw := range newWeapons {
		if p == nw[0] {
			return nw[1]
		}
	}
	return p
}

// lowerLatin1 is Python's str.lower() for Latin-1 text.
func lowerLatin1(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' || c >= 0xC0 && c <= 0xDE && c != 0xD7 {
			b[i] = c + 0x20
		}
	}
	return string(b)
}

// pyIndex is Python list indexing (a negative index counts from the end).
func pyIndex(n int, i int64) (int, error) {
	if i < 0 {
		i += int64(n)
	}
	if i < 0 || i >= int64(n) {
		return 0, errors.New("anmtree: index out of range")
	}
	return int(i), nil
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

// view holds a tree's name tables.
type view struct {
	t                *Tree
	G, P, D, DG      []string
	gIndex, dgByName map[string]int
}

func newView(t *Tree) (*view, error) {
	v := &view{t: t}
	var err error
	if v.G, err = t.groupNames(t.PG); err != nil {
		return nil, err
	}
	if v.DG, err = t.groupNames(t.DG); err != nil {
		return nil, err
	}
	if v.P, err = t.names(t.Props); err != nil {
		return nil, err
	}
	if v.D, err = t.names(t.Descs); err != nil {
		return nil, err
	}
	for _, r := range t.Nodes {
		if uint64(r[0])+uint64(len(t.PG)) > uint64(len(t.NodeProps)) || uint64(r[1])+uint64(r[2]) > uint64(len(t.Animations)) {
			return nil, errors.New("anmtree: node record out of range")
		}
	}
	for _, w := range t.Animations {
		if uint64(w[4])+uint64(len(t.DG)) > uint64(len(t.AnimDescs)) {
			return nil, errors.New("anmtree: animation descriptor base out of range")
		}
	}
	return v, nil
}

type kv struct{ K, V string }

// nodeView is node_view: (props in group order, animation records) of node n.
func (v *view) nodeView(n int) ([]kv, [][]uint32, error) {
	r := v.t.Nodes[n]
	props := make([]kv, len(v.G))
	for g := range v.G {
		i, err := pyIndex(len(v.P), s32(v.t.NodeProps[int(r[0])+g]))
		if err != nil {
			return nil, nil, err
		}
		props[g] = kv{v.G[g], v.P[i]}
	}
	return props, v.t.Animations[r[1] : r[1]+r[2]], nil
}

func (v *view) desc(w []uint32, gi int) (string, error) {
	i, err := pyIndex(len(v.D), s32(v.t.AnimDescs[int(w[4])+gi]))
	if err != nil {
		return "", err
	}
	return v.D[i], nil
}

// content is the node's identity for reuse: props (lower case, 'None' dropped) and per animation (name,
// weight, rate, its non-None descriptors), as build_tc_upper.content.
func (v *view) content(n int, rename bool) (string, error) {
	props, anims, err := v.nodeView(n)
	if err != nil {
		return "", err
	}
	var pr []kv
	for _, p := range props {
		val := p.V
		if rename {
			val = rn(val)
		}
		if val != "None" {
			pr = append(pr, kv{p.K, lowerLatin1(val)})
		}
	}
	sort.Slice(pr, func(i, j int) bool { return pr[i].K < pr[j].K || pr[i].K == pr[j].K && pr[i].V < pr[j].V })
	type anim struct {
		Name       string
		Weight     uint32
		Rate       uint32
		Descriptor []kv
	}
	var al []anim
	for _, w := range anims {
		name, ok := v.t.Name(w[0])
		if !ok {
			return "", errors.New("anmtree: bad animation name offset")
		}
		var ds []kv
		for i := range v.DG {
			d, err := v.desc(w, i)
			if err != nil {
				return "", err
			}
			if d != "None" {
				ds = append(ds, kv{v.DG[i], d})
			}
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i].K < ds[j].K || ds[i].K == ds[j].K && ds[i].V < ds[j].V })
		al = append(al, anim{lowerLatin1(name), w[2], w[8], ds})
	}
	b, err := json.Marshal([]any{pr, al})
	return string(b), err
}

type extEntry struct {
	leaf     bool
	fromPC   bool // leaf: node is a PC node (else a PS3 node)
	node     int
	g, p     string // test
	yes, no_ int
}

// BuildPlayerUpperSFODD builds AnimationDatabase\AnimTrees\Player_Upper_SFODD.AnmTree00p from the stock PC
// AnimationDatabase\AnimTrees\Player_Upper.AnmTree00p (the FEAR_8.Arch00 copy, the last one mounted) and the
// PS3 Player_Upper tree (from database_anim/anim.anmtree, see PS3PlayerUpper). Needs the PS3 copy: the Xbox 360
// Player_Upper tree is a different file.
func BuildPlayerUpperSFODD(pcData, ps3Tree []byte) ([]byte, error) {
	pc, err := Load(pcData)
	if err != nil {
		return nil, fmt.Errorf("PC Player_Upper: %w", err)
	}
	ps, err := Load(ps3Tree)
	if err != nil {
		return nil, fmt.Errorf("PS3 Player_Upper: %w", err)
	}
	if pc.BigEndian || !ps.BigEndian {
		return nil, errors.New("anmtree: expected a PC tree and a console tree")
	}
	pcv, err := newView(pc)
	if err != nil {
		return nil, err
	}
	psv, err := newView(ps)
	if err != nil {
		return nil, err
	}
	if len(pcv.P) < 2 || pcv.P[0] != "None" || pcv.P[1] != "Any" || len(psv.P) < 2 || psv.P[0] != "None" ||
		psv.P[1] != "Any" || len(pcv.D) < 2 || pcv.D[0] != "None" || pcv.D[1] != "Any" {
		return nil, errors.New("anmtree: props/descs do not start with None, Any")
	}
	if len(pc.Transitions)+len(ps.Transitions)+len(pc.AnimLists)+len(ps.AnimLists) != 0 {
		return nil, errors.New("anmtree: player trees have no transitions")
	}

	// PC node content index, for reusing identical nodes (first node wins)
	pcByContent := map[string]int{}
	for n := range pc.Nodes {
		k, err := pcv.content(n, false)
		if err != nil {
			return nil, err
		}
		if _, ok := pcByContent[k]; !ok {
			pcByContent[k] = n
		}
	}

	// specialise the PS3 trie for each new weapon, symbolically
	var ext []extEntry
	type mk struct {
		i int
		x string
	}
	memo := map[mk]int{}
	var spec func(i int, X string, depth int) (int, error)
	spec = func(i int, X string, depth int) (int, error) {
		if depth > len(ps.Trie)+1 {
			return 0, errors.New("anmtree: PS3 trie has a cycle")
		}
		if r, ok := memo[mk{i, X}]; ok {
			return r, nil
		}
		e := ps.Trie[i]
		p, g, n, y, no := s32(e[0]), s32(e[1]), s32(e[2]), s32(e[3]), s32(e[4])
		var r int
		if p == -1 {
			ni, err := pyIndex(len(ps.Nodes), n)
			if err != nil {
				return 0, err
			}
			k, err := psv.content(ni, true)
			if err != nil {
				return 0, err
			}
			r = len(ext)
			if pn, ok := pcByContent[k]; ok {
				ext = append(ext, extEntry{leaf: true, fromPC: true, node: pn})
			} else {
				ext = append(ext, extEntry{leaf: true, node: ni})
			}
		} else {
			if y < 0 || y >= int64(len(ps.Trie)) || no < 0 || no >= int64(len(ps.Trie)) {
				return 0, errors.New("anmtree: PS3 trie is not complete")
			}
			if g < 0 || g >= int64(len(psv.G)) || p < 0 || p >= int64(len(psv.P)) {
				return 0, errors.New("anmtree: PS3 trie test out of range")
			}
			gname, pname := psv.G[g], psv.P[p]
			if gname == "Weapon" {
				next := int(no)
				if pname == X {
					next = int(y)
				}
				if r, err = spec(next, X, depth+1); err != nil {
					return 0, err
				}
			} else {
				if indexOf(pcv.G, gname) < 0 {
					return 0, fmt.Errorf("anmtree: test on a group the PC tree lacks: %s", gname)
				}
				r = len(ext)
				ext = append(ext, extEntry{})
				ry, err := spec(int(y), X, depth+1)
				if err != nil {
					return 0, err
				}
				rno, err := spec(int(no), X, depth+1)
				if err != nil {
					return 0, err
				}
				ext[r] = extEntry{g: gname, p: rn(pname), yes: ry, no_: rno}
			}
		}
		memo[mk{i, X}] = r
		return r, nil
	}
	if len(ps.Trie) == 0 {
		return nil, errors.New("anmtree: empty PS3 trie")
	}
	roots := map[string]int{}
	for _, nw := range newWeapons {
		if roots[nw[0]], err = spec(0, nw[0], 0); err != nil {
			return nil, err
		}
	}

	// PS3 nodes to append (deduplicated), and every (group, prop) the new parts need
	var newNodes []int
	seen := map[int]bool{}
	for _, e := range ext {
		if e.leaf && !e.fromPC && !seen[e.node] {
			seen[e.node] = true
			newNodes = append(newNodes, e.node)
		}
	}
	var need []kv
	for _, e := range ext {
		if !e.leaf {
			need = append(need, kv{e.g, e.p})
		}
	}
	nodeProps := map[int][]kv{}
	for _, n := range newNodes {
		props, _, err := psv.nodeView(n)
		if err != nil {
			return nil, err
		}
		nodeProps[n] = props
		for _, p := range props {
			need = append(need, kv{p.K, rn(p.V)})
		}
	}
	for _, nw := range newWeapons {
		need = append(need, kv{"Weapon", nw[1]})
	}

	// new prop table: PC props in PC order, new props appended at the end of their group
	members := make([][]uint32, len(pc.PG))
	lowerNames := make([][]string, len(pc.PG))
	total := 0
	for gi, r := range pc.PG {
		cnt, first := uint64(r[1]), uint64(r[2])
		if first+cnt > uint64(len(pc.Props)) {
			return nil, errors.New("anmtree: PC prop group out of range")
		}
		members[gi] = append([]uint32(nil), pc.Props[first:first+cnt]...)
		for _, o := range members[gi] {
			s, ok := pc.Name(o)
			if !ok {
				return nil, errors.New("anmtree: bad prop name offset")
			}
			lowerNames[gi] = append(lowerNames[gi], lowerLatin1(s))
		}
		total += len(members[gi])
	}
	if total+2 != len(pc.Props) {
		return nil, errors.New("anmtree: PC prop groups do not tile props[2:]")
	}
	for _, gp := range need {
		if gp.V == "None" || gp.V == "Any" {
			continue
		}
		gi := indexOf(pcv.G, gp.K)
		if gi < 0 {
			return nil, fmt.Errorf("anmtree: copied node uses group %s (%s) that the PC tree lacks", gp.K, gp.V)
		}
		if indexOf(lowerNames[gi], lowerLatin1(gp.V)) < 0 {
			members[gi] = append(members[gi], pc.Intern(gp.V))
			lowerNames[gi] = append(lowerNames[gi], lowerLatin1(gp.V))
		}
	}
	old2new := map[int64]int64{0: 0, 1: 1}
	newProps := []uint32{pc.Props[0], pc.Props[1]}
	newPG := make([][]uint32, len(pc.PG))
	for gi, r := range pc.PG {
		nf := len(newProps)
		for k, o := range members[gi] {
			if k < int(r[1]) {
				old2new[int64(r[2])+int64(k)] = int64(len(newProps))
			}
			newProps = append(newProps, o)
		}
		newPG[gi] = []uint32{r[0], uint32(len(members[gi])), uint32(nf)}
	}
	pidx := func(g, p string) (int64, error) {
		switch p {
		case "None":
			return 0, nil
		case "Any":
			return 1, nil
		}
		gi := indexOf(pcv.G, g)
		if gi < 0 {
			return 0, fmt.Errorf("anmtree: group %s not in the PC tree", g)
		}
		k := indexOf(lowerNames[gi], lowerLatin1(p))
		if k < 0 {
			return 0, fmt.Errorf("anmtree: prop %s:%s not in the PC tree", g, p)
		}
		return int64(newPG[gi][2]) + int64(k), nil
	}

	// assemble
	out := &Tree{Magic: [4]byte{'A', 'N', 'M', 'T'}, Ver: 3, PG: newPG, Props: newProps,
		Descs: append([]uint32(nil), pc.Descs...), AnimDescs: append([]uint32(nil), pc.AnimDescs...)}
	for _, r := range pc.DG {
		out.DG = append(out.DG, append([]uint32(nil), r...))
	}
	for _, r := range pc.Animations {
		out.Animations = append(out.Animations, append([]uint32(nil), r...))
	}
	for _, x := range pc.NodeProps {
		if s32(x) >= 0 {
			v, ok := old2new[s32(x)]
			if !ok {
				return nil, errors.New("anmtree: PC node prop outside every group")
			}
			out.NodeProps = append(out.NodeProps, u32(v))
		} else {
			out.NodeProps = append(out.NodeProps, x)
		}
	}
	for _, r := range pc.Nodes {
		out.Nodes = append(out.Nodes, append([]uint32(nil), r...))
	}
	// the weight set every stock animation uses ('' expected): most common word 6, first seen on a tie
	cnt := map[uint32]int{}
	var firstSeen []uint32
	for _, a := range pc.Animations {
		if cnt[a[6]] == 0 {
			firstSeen = append(firstSeen, a[6])
		}
		cnt[a[6]]++
	}
	if len(firstSeen) == 0 {
		return nil, errors.New("anmtree: PC tree has no animations")
	}
	wsEmpty := firstSeen[0]
	for _, v := range firstSeen {
		if cnt[v] > cnt[wsEmpty] {
			wsEmpty = v
		}
	}
	if s, ok := pc.Name(wsEmpty); !ok || s != "" {
		return nil, errors.New("anmtree: the common PC weight set is not ''")
	}
	nPG, nDG := len(pc.PG), len(pc.DG)
	ps3node2new := map[int]int{}
	const f0 = 0 // bit pattern of 0.0f
	for _, n := range newNodes {
		props, anims, err := psv.nodeView(n)
		if err != nil {
			return nil, err
		}
		pmap := map[string]string{}
		for _, p := range props {
			if indexOf(pcv.G, p.K) < 0 && p.V != "None" {
				return nil, fmt.Errorf("anmtree: PS3 node uses group %s the PC tree lacks", p.K)
			}
			pmap[p.K] = p.V // as the recipe's dict: the last group of a name wins
		}
		firstAnim := len(out.Animations)
		for _, w := range anims {
			for _, x := range w[9:13] {
				if s32(x) != -1 {
					return nil, errors.New("anmtree: PS3 player animation with transition links")
				}
			}
			pos := len(out.Animations)
			for _, gname := range pcv.DG { // descriptor vector in the PC desc-group order
				d := "None"
				if gi := indexOf(psv.DG, gname); gi >= 0 {
					if d, err = psv.desc(w, gi); err != nil {
						return nil, err
					}
				}
				k := indexOf(pcv.D, d)
				if k < 0 {
					return nil, fmt.Errorf("anmtree: PC tree lacks descriptor %s", d)
				}
				out.AnimDescs = append(out.AnimDescs, uint32(k))
			}
			for gi, gname := range psv.DG {
				if indexOf(pcv.DG, gname) < 0 {
					if d, err := psv.desc(w, gi); err != nil || d != "None" {
						return nil, fmt.Errorf("anmtree: PS3 descriptor group %s missing on PC", gname)
					}
				}
			}
			name, ok := ps.Name(w[0])
			if !ok {
				return nil, errors.New("anmtree: bad PS3 animation name")
			}
			out.Animations = append(out.Animations, []uint32{pc.Intern(name), uint32(pos), w[2], 0,
				uint32(pos * nDG), f0, wsEmpty, 0, w[8], u32(-1), u32(-1), u32(-1), u32(-1)})
		}
		ps3node2new[n] = len(out.Nodes)
		out.Nodes = append(out.Nodes, []uint32{uint32(len(out.Nodes) * nPG), uint32(firstAnim), uint32(len(anims))})
		for _, g := range pcv.G {
			v, ok := pmap[g]
			if !ok {
				v = "None"
			}
			pi, err := pidx(g, rn(v))
			if err != nil {
				return nil, err
			}
			out.NodeProps = append(out.NodeProps, u32(pi))
		}
	}
	// trie: 3 dispatch tests, PC trie shifted by 3, then the specialised PS3 parts
	const sh = 3
	base := int64(sh + len(pc.Trie))
	wg := indexOf(pcv.G, "Weapon")
	if wg < 0 {
		return nil, errors.New("anmtree: PC tree has no Weapon group")
	}
	var tr [][5]int64
	for k, nw := range newWeapons {
		pi, err := pidx("Weapon", nw[1])
		if err != nil {
			return nil, err
		}
		tr = append(tr, [5]int64{pi, int64(wg), -1, base + int64(roots[nw[0]]), int64(k + 1)})
	}
	for _, r := range pc.Trie {
		p, g, n, y, no := s32(r[0]), s32(r[1]), s32(r[2]), s32(r[3]), s32(r[4])
		if p == -1 {
			tr = append(tr, [5]int64{p, g, n, y, no})
			continue
		}
		np, ok := old2new[p]
		if !ok {
			return nil, errors.New("anmtree: PC trie test prop outside every group")
		}
		tr = append(tr, [5]int64{np, g, n, y + sh, no + sh})
	}
	for _, e := range ext {
		if e.leaf {
			n := int64(e.node)
			if !e.fromPC {
				n = int64(ps3node2new[e.node])
			}
			tr = append(tr, [5]int64{-1, -1, n, -1, -1})
			continue
		}
		pi, err := pidx(e.g, e.p)
		if err != nil {
			return nil, err
		}
		tr = append(tr, [5]int64{pi, int64(indexOf(pcv.G, e.g)), -1, base + int64(e.yes), base + int64(e.no_)})
	}
	for _, r := range tr {
		out.Trie = append(out.Trie, []uint32{u32(r[0]), u32(r[1]), u32(r[2]), u32(r[3]), u32(r[4])})
	}
	out.Strtab = pc.Strtab // already extended by Intern
	for len(out.Strtab)%4 != 0 {
		out.Strtab = append(out.Strtab, 0)
	}
	return out.Serialize()
}

// PS3PlayerUpper returns the Player_Upper tree of the PS3 database_anim/anim.anmtree container.
func PS3PlayerUpper(container []byte) ([]byte, error) { return Find(container, "Player_Upper") }
