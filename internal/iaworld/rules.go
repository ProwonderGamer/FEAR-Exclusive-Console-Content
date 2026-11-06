package iaworld

// The Objects00p stage (the project's Python countdown builder with the world rules' apply) and the
// world-box check (world_rules.check_world_box).

import (
	"bytes"
	"encoding/binary"
	"math"
	"strconv"

	"fearinstaller/internal/wld"
)

// ---------------------------------------------------------------------------------------------- helpers

func isStrType(t uint32) bool { return t == wld.TString || t == wld.TCommand || t == wld.TStringID }

// fieldOf is the recipe's {n: v for n, t, v in o['props']}[name]: the LAST property of that name.
func fieldOf(o *wld.Object, name string) (*wld.Prop, bool) {
	for i := len(o.Props) - 1; i >= 0; i-- {
		if o.Props[i].Name == name {
			return &o.Props[i], true
		}
	}
	return nil, false
}

// strField: the last property of that name as a string; ok is false when it is missing or not a string.
func strField(o *wld.Object, name string) (string, bool) {
	p, ok := fieldOf(o, name)
	if !ok || !isStrType(p.Type) {
		return "", false
	}
	return p.S, true
}

// mustName is fields(o)['Name'] where the recipe would raise KeyError for a missing Name. A non-string Name
// compares unequal to every string, as in Python.
func mustName(o *wld.Object) string {
	p, ok := fieldOf(o, "Name")
	if !ok {
		fail("%s object without Name", o.Class)
	}
	if !isStrType(p.Type) {
		return "\x00non-string name"
	}
	return p.S
}

// firstName is world_rules' name(o): the first Name property, "" when absent.
func firstName(o *wld.Object) string {
	for _, p := range o.Props {
		if p.Name == "Name" {
			if isStrType(p.Type) {
				return p.S
			}
			return "\x00non-string name"
		}
	}
	return ""
}

// setStr is the recipe's edit(o, {name: s}) for a string value: every property of that name gets it.
func setStr(o *wld.Object, name, s string) {
	n := 0
	for i := range o.Props {
		if o.Props[i].Name == name {
			if !isStrType(o.Props[i].Type) {
				fail("%s.%s is not a string property", firstName(o), name)
			}
			o.Props[i].S = s
			n++
		}
	}
	_ = n // like edit(), a missing property is left alone
}

// setBool is edit(o, {name: b}) for a bool property.
func setBool(o *wld.Object, name string, b bool) {
	for i := range o.Props {
		if o.Props[i].Name == name {
			if o.Props[i].Type != wld.TBool {
				fail("%s.%s is not a bool property", firstName(o), name)
			}
			o.Props[i].U = 0
			if b {
				o.Props[i].U = 1
			}
		}
	}
}

// isTrue: the property is a bool and True (the recipe's `is True`).
func isTrue(o *wld.Object, name string) bool {
	p, ok := fieldOf(o, name)
	return ok && p.Type == wld.TBool && p.U != 0
}

func vecOf(p *wld.Prop) []float64 {
	out := make([]float64, len(p.N))
	for i, u := range p.N {
		out[i] = float64(math.Float32frombits(u))
	}
	return out
}

func f32Bits(v []float64) []uint32 { // world_rules._f32: store what the format can hold
	out := make([]uint32, len(v))
	for i, f := range v {
		out[i] = math.Float32bits(float32(f))
	}
	return out
}

// ------------------------------------------------------------------------------ Hind attachments

// hindExtra: the Hind fuselage (H_MainBody_WM) lists attachments the console world never placed; stock PC
// WTF_Exfil has them. They are copied from the player's own WTF_Exfil and re-based onto the port's fuselage.
var hindExtra = []string{"Int_LightFX01", "Int_Light01", "ExhaustR"}

// qmul is world_rules._qmul, evaluated left to right in float64 with every product rounded on its own (no FMA).
func qmul(a, b [4]float64) [4]float64 {
	ax, ay, az, aw := a[0], a[1], a[2], a[3]
	bx, by, bz, bw := b[0], b[1], b[2], b[3]
	return [4]float64{
		float64(float64(float64(aw*bx)+float64(ax*bw))+float64(ay*bz)) - float64(az*by),
		float64(float64(float64(aw*by)-float64(ax*bz))+float64(ay*bw)) + float64(az*bx),
		float64(float64(float64(aw*bz)+float64(ax*by))-float64(ay*bx)) + float64(az*bw),
		float64(float64(float64(aw*bw)-float64(ax*bx))-float64(ay*by)) - float64(az*bz),
	}
}

func qrot(q [4]float64, v [3]float64) [3]float64 {
	r := qmul(qmul(q, [4]float64{v[0], v[1], v[2], 0}), [4]float64{-q[0], -q[1], -q[2], q[3]})
	return [3]float64{r[0], r[1], r[2]}
}

func vecField(o *wld.Object, name string, n int) []float64 {
	p, ok := fieldOf(o, name)
	if !ok || len(p.N) != n {
		fail("%s.%s is not a %d-component vector", firstName(o), name, n)
	}
	return vecOf(p)
}

func addHindAttachments(objects []wld.Object, stock []wld.Object) []wld.Object {
	findFirst := func(objs []wld.Object, n string) *wld.Object {
		for i := range objs {
			if firstName(&objs[i]) == n {
				return &objs[i]
			}
		}
		fail("Hind: %s not found", n)
		return nil
	}
	body := findFirst(objects, "H_MainBody_WM")
	sbody := findFirst(stock, "H_MainBody_WM")
	bPos, bRot := vecField(body, "Pos", 3), vecField(body, "Rotation", 4)
	sPos0, sRot0 := vecField(sbody, "Pos", 3), vecField(sbody, "Rotation", 4)
	if sRot0[0] != 0 || sRot0[1] != 0 || sRot0[2] != 0 || sRot0[3] != 1 {
		fail("Hind: stock fuselage is rotated")
	}
	att, ok := strField(body, "Attachments")
	if !ok {
		fail("Hind: fuselage has no Attachments")
	}
	q := [4]float64{bRot[0], bRot[1], bRot[2], bRot[3]}
	for _, n := range hindExtra {
		if !bytes.Contains([]byte(att), []byte(n)) {
			fail("Hind: %s is not in the fuselage attachments", n)
		}
		for i := range objects {
			if firstName(&objects[i]) == n {
				fail("Hind: %s already exists", n)
			}
		}
		var src []*wld.Object
		for i := range stock {
			if firstName(&stock[i]) == n {
				src = append(src, &stock[i])
			}
		}
		if len(src) != 1 {
			fail("Hind: %s found %d times in stock WTF_Exfil", n, len(src))
		}
		o := src[0].Clone()
		sPos, sRot := vecField(&o, "Pos", 3), vecField(&o, "Rotation", 4)
		off := qrot(q, [3]float64{sPos[0] - sPos0[0], sPos[1] - sPos0[1], sPos[2] - sPos0[2]})
		pos := f32Bits([]float64{bPos[0] + off[0], bPos[1] + off[1], bPos[2] + off[2]})
		r := qmul(q, [4]float64{sRot[0], sRot[1], sRot[2], sRot[3]})
		rot := f32Bits(r[:])
		for i := range o.Props {
			switch o.Props[i].Name {
			case "Pos":
				o.Props[i].N = append([]uint32(nil), pos...)
			case "Rotation":
				o.Props[i].N = append([]uint32(nil), rot...)
			}
		}
		objects = append(objects, o)
	}
	return objects
}

// ------------------------------------------------------------------------- terminal events (world_rules)

// isPyWordByte: Python's \w for a Latin-1 character (str.isalnum() or '_').
func isPyWordByte(c byte) bool {
	switch {
	case c == '_', '0' <= c && c <= '9', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		return true
	case c == 0xAA, c == 0xB2, c == 0xB3, c == 0xB5, c == 0xB9, c == 0xBA, c == 0xBC, c == 0xBD, c == 0xBE:
		return true
	case 0xC0 <= c && c != 0xD7 && c != 0xF7:
		return true
	}
	return false
}

func isPyWSByte(c byte) bool {
	switch c {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x1c, 0x1d, 0x1e, 0x1f, 0x85, 0xa0:
		return true
	}
	return false
}

func hasFoldPrefix(s string, i int, w string) bool {
	if i+len(w) > len(s) {
		return false
	}
	for k := 0; k < len(w); k++ {
		c := s[i+k]
		if 'A' <= c && c <= 'Z' {
			c += 0x20
		}
		if c != w[k] {
			return false
		}
	}
	return true
}

// subExitLevel is re.subn(r'\bmsg\s+player\s+exitlevel\b', 'msg player (MISSIONFAILED 77110)', v, flags=re.I)
// on the recipe's Latin-1 decoded string.
func subExitLevel(s string) (string, int) {
	var out []byte
	n, last := 0, 0
	for i := 0; i < len(s); {
		end := matchExitLevel(s, i)
		if end < 0 {
			i++
			continue
		}
		out = append(out, s[last:i]...)
		out = append(out, "msg player (MISSIONFAILED 77110)"...)
		n++
		i, last = end, end
	}
	if n == 0 {
		return s, 0
	}
	return string(append(out, s[last:]...)), n
}

func matchExitLevel(s string, i int) int {
	if i > 0 && isPyWordByte(s[i-1]) {
		return -1
	}
	p := i
	for k, w := range []string{"msg", "player", "exitlevel"} {
		if k > 0 {
			q := p
			for q < len(s) && isPyWSByte(s[q]) {
				q++
			}
			if q == p {
				return -1
			}
			p = q
		}
		if !hasFoldPrefix(s, p, w) {
			return -1
		}
		p += len(w)
	}
	if p < len(s) && isPyWordByte(s[p]) {
		return -1
	}
	return p
}

// wantExitLevels: authored successful exits per map (the recipe's count assertion).
var wantExitLevels = map[string]int{"Distribution": 1, "Rooftop": 1, "Construction": 2, "Vault": 1}

// applyRules is world_rules.apply: Distribution's Hind attachments, then every authored exit becomes the
// MISSIONFAILED 77110 results trigger, Vault's PlayerTrigger01 commits first (77112), and the HealthMax /
// SlowMoMax pickups report 77113.
func applyRules(label string, objects []wld.Object, stockExfil []wld.Object) []wld.Object {
	if label == "Distribution" {
		objects = addHindAttachments(objects, stockExfil)
	}
	count := 0
	for oi := range objects {
		o := &objects[oi]
		gearType, _ := strField(o, "GearType")
		_, hasName := fieldOf(o, "Name")
		name := ""
		if hasName {
			name = mustName(o) // read before any change, like the recipe's d
		}
		pickup := o.Class == "GearItem"
		if pickup {
			g := latin1Lower(gearType)
			pickup = g == "healthmax" || g == "slowmomax"
		}
		for pi := range o.Props {
			p := &o.Props[pi]
			if !isStrType(p.Type) {
				continue
			}
			v, hits := subExitLevel(p.S)
			count += hits
			if label == "Vault" && !hasName {
				fail("Vault: %s object without Name", o.Class) // d['Name'] raises in the recipe
			}
			if label == "Vault" && name == "PlayerTrigger01" && p.Name == "Command1" {
				if !bytes.Contains([]byte(v), []byte("msg ExitTrigger on;")) {
					fail("Vault PlayerTrigger01.Command1 lacks the ExitTrigger command")
				}
				v = "msg player (MISSIONFAILED 77112);msg player (CANDAMAGE 0);" + v
			}
			if pickup && p.Name == "PickupCommand" {
				for len(v) > 0 && v[len(v)-1] == ';' {
					v = v[:len(v)-1]
				}
				v += ";msg player (MISSIONFAILED 77113);"
			}
			p.S = v
		}
	}
	if count != wantExitLevels[label] {
		fail("%s: %d exitlevel commands, want %d", label, count, wantExitLevels[label])
	}
	return objects
}

// --------------------------------------------------------------------------------- countdown (Objects00p)

// countdownIndex: MISSIONFAILED 77101..77104 starts the map's clock in the hook.
var countdownIndex = map[string]int{"Distribution": 0, "Rooftop": 1, "Construction": 2, "Vault": 3}

// buildObjects00p is build_countdowns for one map: the world's own objects with the world rules, plus the
// stock PC countdown (DisplayTimer IA_Countdown, StartupCommand IA_StartClock), as an OBJT override file.
func buildObjects00p(label string, world []byte, stockExfil []wld.Object) []byte {
	objs, _, end, err := wld.ParsePCWorld(world)
	if err != nil {
		fail("%s: re-reading the world objects: %v", label, err)
	}
	if end != int(binary.LittleEndian.Uint32(world[16:])) {
		fail("%s: object section does not end at the blind data", label)
	}
	var start []uint32
	for i := range objs {
		o := &objs[i]
		if n, _ := strField(o, "Name"); o.Class == "DisplayTimer" || n == "IA_Countdown" || n == "IA_StartClock" {
			fail("%s: countdown already present", label)
		}
		if start == nil && o.Class == "GameStartPoint" {
			p, ok := fieldOf(o, "Pos")
			if !ok || len(p.N) != 3 {
				fail("%s: GameStartPoint without Pos", label)
			}
			start = append([]uint32(nil), p.N...)
		}
	}
	if start == nil {
		fail("%s: no GameStartPoint", label)
	}
	common := func() []wld.Prop {
		return []wld.Prop{
			{Name: "Pos", Type: wld.TVector, N: append([]uint32(nil), start...)},
			{Name: "Rotation", Type: wld.TRot, N: []uint32{0, 0, 0, math.Float32bits(1)}},
			{Name: "Template", Type: wld.TBool},
			{Name: "IsGore", Type: wld.TBool},
			{Name: "ObjectLOD", Type: wld.TString},
		}
	}
	timer := wld.Object{Class: "DisplayTimer", Props: append(append([]wld.Prop{{Name: "Name", Type: wld.TString, S: "IA_Countdown"}}, common()...),
		wld.Prop{Name: "StartCommand", Type: wld.TCommand},
		wld.Prop{Name: "EndCommand", Type: wld.TCommand, S: "msg player (MISSIONFAILED 77111);"},
		wld.Prop{Name: "RemoveWhenDone", Type: wld.TBool, U: 1},
		wld.Prop{Name: "Team", Type: wld.TString, S: "NoTeam"})}
	startup := wld.Object{Class: "StartupCommand", Props: append(append([]wld.Prop{{Name: "Name", Type: wld.TString, S: "IA_StartClock"}}, common()...),
		wld.Prop{Name: "Command", Type: wld.TCommand, S: "msg IA_Countdown (ON 900);delay .1 (msg player (MISSIONFAILED " +
			strconv.Itoa(77101+countdownIndex[label]) + "));"},
		wld.Prop{Name: "RoundCondition", Type: wld.TString, S: "All"},
		wld.Prop{Name: "SafeExecute", Type: wld.TBool, U: 1})}
	objs = applyRules(label, objs, stockExfil)
	objs = append(objs, timer, startup)
	sec := wld.SerializeObjects(objs)
	out := append([]byte("OBJT\x01\x00\x00\x00"), sec...)
	// the recipe's round-trip check: the file parses back to the same objects
	back, stop, err := wld.ParseObjects(out, 8, false)
	if err != nil || stop != len(out) || !bytes.Equal(wld.SerializeObjects(back), sec) {
		fail("%s: Objects00p does not read back", label)
	}
	return out
}

// ----------------------------------------------------------------------------------- world box rule

// wantTracks: KeyFramer blind tracks per map.
var wantTracks = map[string]int{"Distribution": 16, "Rooftop": 1, "Construction": 6, "Vault": 3}

// checkWorldBox: every KeyFramer key must lie inside the world box (world header +0x14 min, +0x20 max):
// FEAR.exe hides a moving world model whose origin leaves it.
func checkWorldBox(label string, w []byte) {
	le := binary.LittleEndian
	f := func(o int) float32 { return math.Float32frombits(le.Uint32(w[o:])) }
	lo := [3]float32{f(0x14), f(0x18), f(0x1c)}
	hi := [3]float32{f(0x20), f(0x24), f(0x28)}
	p := int(le.Uint32(w[16:]))
	if p+8 > len(w) {
		fail("%s: no blind data", label)
	}
	cnt, bsz := int(le.Uint32(w[p:])), int(le.Uint32(w[p+4:]))
	blob := w[p+8 : p+8+bsz]
	et := p + 8 + bsz
	tracks := 0
	for i := 0; i < cnt; i++ {
		sz, t, o := int(le.Uint32(w[et+12*i:])), le.Uint32(w[et+12*i+4:]), int(le.Uint32(w[et+12*i+8:]))
		if t != blindKeyframer {
			continue
		}
		seg := blob[o : o+sz]
		n := int(le.Uint32(seg[4:]))
		tracks++
		for k := 0; k < n; k++ {
			q := 12 + 36*k + 8
			for a := 0; a < 3; a++ {
				v := math.Float32frombits(le.Uint32(seg[q+4*a:]))
				if !(lo[a] <= v && v <= hi[a]) {
					fail("%s: key outside the world box", label)
				}
			}
		}
	}
	if tracks != wantTracks[label] {
		fail("%s: %d KeyFramer tracks, want %d", label, tracks, wantTracks[label])
	}
}
