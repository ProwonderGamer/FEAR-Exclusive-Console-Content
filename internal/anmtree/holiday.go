package anmtree

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Soldier_Holiday / Soldier_Holiday2 (Holiday mission, Bishop): the Xbox 360 Soldier tree converted to PC with
// the PC blend values. This recipe follows the project's Python converter (raw serialize + prune_and_rename)
// and reproduces both confirmed files byte for byte:
//
//	every animation and transition record: BlendDuration 0.0, the tree's own '' weight set, flags bit 1
//	(console-only AllowBlendFrom) cleared, i.e. the (0.0, '', 1) every stock PC Soldier record carries;
//	written as PC ANMT v3; then props that GameServer.dll does not know and the tree never references are
//	dropped (prune_and_rename with the GameServer.dll v1.08 prop enum).
//	Soldier_Holiday2 = Soldier_Holiday with the four ATVT_StepAside + MOV_Run records re-pointed from runback3
//	to walkback (a workaround for Bishop bumping into the player).

// consoleRename is ps3anmt2pc.DEFAULT_RENAME (the X360 Soldier tree has none of these props).
var consoleRename = map[string]string{"WEAP_SM15": "WEAP_G00", "WEAP_DUALSM15": "WEAP_G01", "WEAP_AUTOSHOTGUN": "WEAP_G02"}

// BuildSoldierHoliday builds AnimationDatabase\AnimTrees\Soldier_Holiday.AnmTree00p from the Xbox 360 Soldier
// tree (X360Soldier of Game/database_anim/Anim.anmtree) and the PC prop enum (PCAnimPropEnum of the stock
// GameServer.dll). Needs the Xbox 360 copy.
func BuildSoldierHoliday(consoleSoldier []byte, pcProps []string) ([]byte, error) {
	t, err := Load(consoleSoldier)
	if err != nil {
		return nil, err
	}
	if !t.BigEndian || t.Ver != 4 {
		return nil, errors.New("anmtree: expected a console v4 tree")
	}
	// the tree's own '' weight set (the records without blend weights use it)
	ws := -1
	for _, a := range t.Animations {
		if s, ok := t.Name(a[6]); ok && s == "" {
			if ws >= 0 && uint32(ws) != a[6] {
				return nil, errors.New("anmtree: more than one '' weight set")
			}
			ws = int(a[6])
		}
	}
	if ws < 0 {
		return nil, errors.New("anmtree: no '' weight set in the tree")
	}
	for _, a := range t.Animations {
		a[5], a[6], a[7] = 0, uint32(ws), a[7]&^2
	}
	for _, x := range t.Transitions {
		x[4], x[5], x[6] = 0, uint32(ws), x[6]&^2
	}
	t.BigEndian, t.Magic, t.Ver = false, [4]byte{'A', 'N', 'M', 'T'}, 3
	if err := pruneAndRename(t, pcProps, consoleRename); err != nil {
		return nil, err
	}
	return t.Serialize()
}

// pruneAndRename is ps3anmt2pc.prune_and_rename: drop props the PC code does not know that the tree never
// references, rename the PS3-only weapon props, merge same-name props of a group.
func pruneAndRename(t *Tree, pcProps []string, rename map[string]string) error {
	enum := map[string]bool{}
	for _, p := range pcProps {
		enum[lowerLatin1(p)] = true
	}
	names, err := t.names(t.Props)
	if err != nil {
		return err
	}
	ref := map[int64]bool{}
	for _, v := range t.NodeProps {
		if s32(v) >= 0 {
			ref[s32(v)] = true
		}
	}
	for _, r := range t.Trie {
		if s32(r[0]) >= 0 {
			ref[s32(r[0])] = true
		}
	}
	if len(t.Props) < 2 {
		return errors.New("anmtree: tree has no None/Any props")
	}
	newProps := []uint32{t.Props[0], t.Props[1]}
	old2new := map[int64]int64{0: 0, 1: 1}
	for _, g := range t.PG {
		first, cnt := uint64(g[2]), uint64(g[1])
		if first+cnt > uint64(len(t.Props)) {
			return errors.New("anmtree: prop group out of range")
		}
		newFirst := len(newProps)
		seen := map[string]int64{}
		for i := first; i < first+cnt; i++ {
			nm := names[i]
			tgt := nm
			if r, ok := rename[nm]; ok {
				tgt = r
			}
			key := lowerLatin1(tgt)
			if !enum[key] && !ref[int64(i)] {
				continue // dropped
			}
			if k, ok := seen[key]; ok {
				old2new[int64(i)] = k // merged with an earlier same-name prop
				continue
			}
			seen[key] = int64(len(newProps))
			old2new[int64(i)] = int64(len(newProps))
			if tgt == nm {
				newProps = append(newProps, t.Props[i])
			} else {
				newProps = append(newProps, t.Intern(tgt))
			}
		}
		g[1], g[2] = uint32(len(newProps)-newFirst), uint32(newFirst)
	}
	remap := func(x uint32) (uint32, error) {
		if s32(x) < 0 {
			return x, nil
		}
		v, ok := old2new[s32(x)]
		if !ok {
			return 0, errors.New("anmtree: reference to a prop outside every group")
		}
		return u32(v), nil
	}
	for i, v := range t.NodeProps {
		if t.NodeProps[i], err = remap(v); err != nil {
			return err
		}
	}
	for _, r := range t.Trie {
		if r[0], err = remap(r[0]); err != nil {
			return err
		}
	}
	t.Props = newProps
	return nil
}

// BuildSoldierHoliday2 builds Soldier_Holiday2.AnmTree00p from the BuildSoldierHoliday output: the animation
// records of the ATVT_StepAside + MOV_Run nodes that play runback3 play walkback instead (exactly 4 records).
func BuildSoldierHoliday2(holiday []byte) ([]byte, error) {
	t, err := Load(holiday)
	if err != nil {
		return nil, err
	}
	v, err := newView(t)
	if err != nil {
		return nil, err
	}
	walk := t.Intern("walkback")
	n := 0
	for ni := range t.Nodes {
		props, anims, err := v.nodeView(ni)
		if err != nil {
			return nil, err
		}
		var act, mov bool
		for _, p := range props {
			act = act || p.K == "Activity" && p.V == "ATVT_StepAside"
			mov = mov || p.K == "Movement" && p.V == "MOV_Run"
		}
		if !act || !mov {
			continue
		}
		for _, a := range anims {
			if s, ok := t.Name(a[0]); ok && s == "runback3" {
				a[0] = walk
				n++
			}
		}
	}
	if n != 4 {
		return nil, fmt.Errorf("anmtree: %d StepAside+Run runback3 records, expected 4", n)
	}
	return t.Serialize()
}

// X360Soldier returns the Soldier tree of the Xbox 360 Game/database_anim/Anim.anmtree container.
func X360Soldier(container []byte) ([]byte, error) { return Find(container, "Soldier") }

// PCAnimPropEnum reads the animation prop names compiled into PC GameServer.dll v1.08 (FEARE_8.Arch00): a
// pointer table at VA 0x1022ed18 with its count at 0x1022f21c (FUN_10187f40 looks prop names up there and
// registers unknown ones at run time). Port of the project's Python enum reader. The table must start with
// None, Any and contain WEAP_G00, or the DLL is refused.
func PCAnimPropEnum(dll []byte) ([]string, error) {
	le := binary.LittleEndian
	bad := errors.New("anmtree: GameServer.dll is not the expected v1.08 build")
	if len(dll) < 0x40 || string(dll[:2]) != "MZ" {
		return nil, bad
	}
	pe := int(le.Uint32(dll[0x3c:]))
	if pe < 0 || pe+24+68 > len(dll) || string(dll[pe:pe+4]) != "PE\x00\x00" {
		return nil, bad
	}
	nsec := int(le.Uint16(dll[pe+6:]))
	optsz := int(le.Uint16(dll[pe+20:]))
	base := uint64(le.Uint32(dll[pe+24+28:]))
	type sec struct{ va, rs, ro uint64 }
	var secs []sec
	for i := 0; i < nsec; i++ {
		o := pe + 24 + optsz + 40*i
		if o+40 > len(dll) {
			return nil, bad
		}
		secs = append(secs, sec{uint64(le.Uint32(dll[o+12:])), uint64(le.Uint32(dll[o+16:])), uint64(le.Uint32(dll[o+20:]))})
	}
	va2off := func(v uint64) (int, bool) {
		for _, s := range secs {
			if base+s.va <= v && v < base+s.va+s.rs && s.ro+(v-base-s.va) < uint64(len(dll)) {
				return int(v - base - s.va + s.ro), true
			}
		}
		return 0, false
	}
	u32at := func(v uint64) (uint32, bool) {
		o, ok := va2off(v)
		if !ok || o+4 > len(dll) {
			return 0, false
		}
		return le.Uint32(dll[o:]), true
	}
	cstr := func(v uint64) (string, bool) {
		o, ok := va2off(v)
		if !ok {
			return "", false
		}
		for i := o; i < len(dll) && i < o+256; i++ {
			if dll[i] == 0 {
				return string(dll[o:i]), true
			}
		}
		return "", false
	}
	n, ok := u32at(0x1022f21c)
	if !ok || n < 3 || n > 4096 {
		return nil, bad
	}
	out := make([]string, n)
	hasG00 := false
	for i := range out {
		p, ok := u32at(0x1022ed18 + 4*uint64(i))
		if !ok {
			return nil, bad
		}
		if out[i], ok = cstr(uint64(p)); !ok {
			return nil, bad
		}
		hasG00 = hasG00 || out[i] == "WEAP_G00"
	}
	if out[0] != "None" || out[1] != "Any" || !hasG00 {
		return nil, bad
	}
	return out, nil
}
