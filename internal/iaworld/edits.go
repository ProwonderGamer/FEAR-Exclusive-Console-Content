package iaworld

// The object edits build_maps.py makes on the Xbox 360 objects before conversion. Every edit checks the
// value it replaces (the recipe's assertions), so a different console copy fails instead of building a
// different map.

import (
	"math"
	"strings"

	"fearinstaller/internal/wld"
)

// Database records the mod adds (sort-last zSFODD_ names).
const (
	sm15Type   = "zSFODD_Machine Pistol"
	watsonType = "zSFODD_AutoShotgun"
)

func float32frombits(u uint32) float32 { return math.Float32frombits(u) }

// pyTruthy is Python truthiness of a property value (the recipe's `if t.get(k)` / `assert not v`).
func pyTruthy(p *wld.Prop) bool {
	switch {
	case isStrType(p.Type):
		return p.S != ""
	case p.Type == wld.TVector || p.Type == wld.TColor || p.Type == wld.TRot:
		return len(p.N) > 0 // a non-empty tuple
	case p.Type == wld.TFloat:
		return float32frombits(p.U) != 0
	case p.Type == wld.TBool:
		return p.U != 0
	}
	return true // ('raw', t, v) tuple
}

// mustStr is fields(o)[name] for a value the recipe uses as a string (KeyError / AttributeError otherwise).
func mustStr(o *wld.Object, name string) string {
	v, ok := strField(o, name)
	if !ok {
		fail("%s %s: %s missing or not a string", o.Class, firstName(o), name)
	}
	return v
}

// rooftopGates: Rooftop's two "clear the area" gates open when the area is
// cleared. Each gate's Locked trigger is unlocked by its death counter; the trigger's commands move into the
// counter's unlock event (same order, same delays) and the trigger is never unlocked again.
var rooftopGates = []struct {
	counter, trigger string
	cmds             []string
}{
	{"LastRoofEventCounter00", "Exit01_Tr01", []string{"Command1", "Command2"}},
	{"EndHall_DeathCounter", "Exit00_Tr00", []string{"Command2", "Command3"}},
}

// editObjects applies the recipe's edits to one map's X360 objects. It returns the positions of the SM15
// pickups (expected) and of the Watson pickups (wpos) for the post-conversion check.
func editObjects(label string, objs []wld.Object, ps3 []wld.Object, watson bool) (expected, wpos map[posKey]bool) {
	expected, wpos = map[posKey]bool{}, map[posKey]bool{}
	// The X360 maps place their own machine-pistol pickups (Platform_MachPist*): retype them in place to SM15.
	// Vault with a PS3 copy: the PS3's two Watson pickups take the X360 slots PlatformSMG07 and Shotgun01.
	for i := range objs {
		o := &objs[i]
		if o.Class != "WeaponItem" {
			continue
		}
		if strings.HasPrefix(mustStr(o, "WeaponType"), "Platform_MachPist") && !(watson && mustName(o) == "PlatformSMG07") {
			k := posOf(o)
			setStr(o, "WeaponType", sm15Type)
			expected[k] = true
		}
	}
	if watson {
		for i := range ps3 {
			o := ps3[i].Clone()
			if o.Class != "WeaponItem" {
				continue
			}
			if v, ok := strField(&o, "WeaponType"); !ok || v != "AutoShotgun" {
				continue
			}
			target, ok := map[string]string{"AutoShotgun": "PlatformSMG07", "AutoShotgun01": "Shotgun01"}[mustName(&o)]
			if !ok {
				fail("Vault: unexpected PS3 Watson pickup %q", mustName(&o))
			}
			k := posOf(&o)
			setStr(&o, "Name", target)
			setStr(&o, "WeaponType", watsonType)
			var slots []int
			for j := range objs {
				if objs[j].Class == "WeaponItem" && mustName(&objs[j]) == target {
					slots = append(slots, j)
				}
			}
			if len(slots) != 1 {
				fail("Vault: X360 slot %s found %d times", target, len(slots))
			}
			objs[slots[0]] = o
			wpos[k] = true
		}
	}
	one := func(name string) *wld.Object {
		var hit *wld.Object
		n := 0
		for j := range objs {
			if mustName(&objs[j]) == name {
				hit = &objs[j]
				n++
			}
		}
		if n != 1 {
			fail("%s: %s found %d times", label, name, n)
		}
		return hit
	}
	gate := map[string]int{}
	if label == "Rooftop" {
		for gi, g := range rooftopGates {
			gate[g.counter] = gi
			t := one(g.trigger)
			if !isTrue(t, "Locked") {
				fail("Rooftop: %s is not Locked", g.trigger)
			}
			for _, k := range g.cmds {
				if !strings.HasSuffix(mustStr(t, k), ";") {
					fail("Rooftop: %s.%s does not end with ';'", g.trigger, k)
				}
			}
			var set []string
			for _, k := range []string{"Command1", "Command2", "Command3", "Command4"} {
				if p, ok := fieldOf(t, k); ok && pyTruthy(p) {
					set = append(set, k)
				}
			}
			if strings.Join(set, ",") != strings.Join(g.cmds, ",") {
				fail("Rooftop: %s commands %v", g.trigger, set)
			}
			var refs []string
			lt := latin1Lower(g.trigger)
			for j := range objs {
				for _, p := range objs[j].Props {
					if p.Name != "Name" && isStrType(p.Type) && strings.Contains(latin1Lower(p.S), lt) {
						refs = append(refs, mustName(&objs[j]))
					}
				}
			}
			if len(refs) != 1 || refs[0] != g.counter {
				fail("Rooftop: %s is named by %v, want only %s", g.trigger, refs, g.counter)
			}
		}
	}
	for j := range objs {
		o := &objs[j]
		name := ""
		if label != "Vault" { // the recipe reads d0['Name'] for every object of maps 1-3
			name = mustName(o)
		}
		switch label {
		case "Rooftop":
			if gi, ok := gate[name]; ok {
				g := rooftopGates[gi]
				t := one(g.trigger)
				c := "msg " + g.trigger + " unlock;"
				ev := mustStr(o, "Event1IncToValCmd")
				if !strings.HasPrefix(ev, c) {
					fail("Rooftop: %s.Event1IncToValCmd does not start with %q", name, c)
				}
				nv := ev[len(c):]
				for _, k := range g.cmds {
					nv += mustStr(t, k)
				}
				setStr(o, "Event1IncToValCmd", nv)
			}
			if name == "HABCK_UP_CO" || name == "BCK_UP_CO" {
				p, ok := fieldOf(o, "EventT3E1")
				if !ok || pyTruthy(p) {
					fail("Rooftop: %s.EventT3E1 is set", name)
				}
				t6 := mustStr(o, "EventT6E0")
				setStr(o, "EventT3E1", t6)
				setStr(o, "EventT6E0", "")
			}
			// The opening line repeats on PC (X360 Loop=True); the PS3 plays it once (Loop=False).
			if name == "Start_SFX" {
				if !isTrue(o, "Loop") || mustStr(o, "Sound") != `voice\14093r.wav` {
					fail("Rooftop: Start_SFX is not the expected looping voice")
				}
				setBool(o, "Loop", false)
			}
		case "Construction":
			// The pilot's "Go!" is StartOn + Loop and is turned on again at 1.0 s: keep one scripted play.
			if name == "SoundFX00" {
				if !isTrue(o, "StartOn") || !isTrue(o, "Loop") || mustStr(o, "Sound") != `\Voice\14012r.wav` {
					fail("Construction: SoundFX00 is not the expected voice")
				}
				setBool(o, "StartOn", false)
				setBool(o, "Loop", false)
			}
			// The shotgun comes from the mission record; drop the script's own ACQUIREWEAPON.
			if name == "StartupCommand02" {
				c := "msg player (ACQUIREWEAPON  shotgun);"
				v := mustStr(o, "Command")
				if !strings.HasPrefix(v, c+"delay .05 (msg player (changeweapon shotgun));msg camera01 on;") {
					fail("Construction: StartupCommand02 is %q", v)
				}
				setStr(o, "Command", v[len(c):])
			}
		case "Distribution":
			// "Follow me!" repeats on PC (X360 Loop=True); the PS3 sets Loop=False.
			if name == "SoundFX00" && o.Class == "SoundFX" {
				if !isTrue(o, "StartOn") || !isTrue(o, "Loop") || mustStr(o, "Sound") != `Voice\21270.WAV` {
					fail("Distribution: SoundFX00 is not the expected voice")
				}
				setBool(o, "Loop", false)
			}
			// Re-acquire the X360 Arsenal/Global AdditionalDefaultWeapons right after the scripted reset.
			if name == "StartupCommand04" {
				c := "msg Player Resetinventory;"
				v := mustStr(o, "Command")
				if !strings.HasPrefix(v, c+"delay .1 (msg player (ACQUIREWEAPON Shotgun));") {
					fail("Distribution: StartupCommand04 is %q", v)
				}
				add := ""
				for _, w := range []string{"Pistol", "Submachinegun", "Frag Grenade"} {
					add += "msg player (ACQUIREWEAPON " + w + ");"
				}
				setStr(o, "Command", c+add+v[len(c):])
			}
		}
		// Fettel's model path and the leading-backslash voice path (the only one of its kind) as PC finds them.
		for pi := range o.Props {
			p := &o.Props[pi]
			if isStrType(p.Type) && (strings.HasSuffix(p.S, "Fettel..Mdl") || latin1Lower(p.S) == `\voice\14012r.wav`) {
				p.S = strings.TrimLeft(strings.ReplaceAll(p.S, "Fettel..Mdl", "Fettel.Mdl"), `\`)
			}
		}
	}
	return expected, wpos
}
