package holworld

// Object edits after conversion: the object hook of the project's Python Holiday world builder, with
// hol_patches.load (the parts the embedded patch file uses), hol_world2pc.apply_patches and
// hol_world2pc.fix_ainode_dependencies.

import (
	_ "embed"
	"encoding/json"
	"strings"

	"fearinstaller/internal/wld"
)

// holiday_world_patches.json: the 11 object edits (record renames, the objective, Bishop, the slow-mo locks).
// Names and command strings only.
//
//go:embed holiday_world_patches.json
var patchesJSON []byte

// patch is one normalised edit (hol_patches.load output).
type patch struct {
	ID, Class, Object, Property, Old, New string
}

// loadPatches is hol_patches.load for the embedded file with no database check (the installer builds the
// zSFODD_ records itself) and an empty patch_corrections.json: 'class' entries are taken as they are, an
// 'optional' entry is skipped (no ids are requested), and a descriptive "(...)" value is skipped.
func loadPatches() []patch {
	var raw struct {
		Patches []map[string]any `json:"patches"`
	}
	if err := json.Unmarshal(patchesJSON, &raw); err != nil {
		fail("embedded patch file: %v", err)
	}
	str := func(e map[string]any, k string) string {
		v, ok := e[k].(string)
		if !ok {
			fail("embedded patch %v: %s is not a string (only string edits are ported)", e["id"], k)
		}
		return v
	}
	var out []patch
	for _, e := range raw.Patches {
		if _, ok := e["class"]; !ok {
			fail("embedded patch %v: no 'class' (the \"Class.Name\" form is not ported)", e["id"])
		}
		prop, _ := e["property"].(string)
		if prop == "" {
			prop = str(e, "prop")
		}
		if p, _ := e["priority"].(string); p == "optional" {
			continue
		}
		nv := str(e, "new")
		if strings.HasPrefix(nv, "(") {
			continue
		}
		if strings.Contains(nv, " (NEW record") {
			nv, _, _ = strings.Cut(nv, " (") // new.split(' (')[0]
		}
		out = append(out, patch{ID: str(e, "id"), Class: str(e, "class"), Object: str(e, "object"), Property: prop,
			Old: str(e, "old"), New: nv})
	}
	return out
}

// objName is _obj_name: the value of the first "Name" property; ok is false when there is none or it is not a
// string (the recipe then compares a non-string with the patch's string and never matches).
func objName(o *wld.Object) (string, bool) {
	for _, p := range o.Props {
		if p.Name == "Name" {
			return p.S, isStr(p.Type)
		}
	}
	return "", false
}

func isStr(t uint32) bool { return t == wld.TString || t == wld.TCommand || t == wld.TStringID }

type rawKey struct{ class, object, prop string }

// rawStrings: (class, Name, property) -> every string value of the unconverted X360 objects (the recipe's `raw`).
func rawStrings(objs []wld.Object) map[rawKey][]string {
	raw := map[rawKey][]string{}
	for i := range objs {
		nm, ok := objName(&objs[i])
		if !ok {
			continue
		}
		for _, p := range objs[i].Props {
			if isStr(p.Type) {
				k := rawKey{objs[i].Class, nm, p.Name}
				raw[k] = append(raw[k], p.S)
			}
		}
	}
	return raw
}

// applyPatches: the hook's alias step plus apply_patches. Exactly one object of that class and Name must exist,
// the property must exist (first of that name) and hold 'old' exactly; the raw X360 value is accepted as an
// alias of 'old' (the hook then compares with the converted value). Any mismatch fails the build.
func applyPatches(objs []wld.Object, patches []patch, raw map[rawKey][]string) {
	for _, p := range patches {
		var cands []*wld.Object
		for i := range objs {
			if n, ok := objName(&objs[i]); ok && objs[i].Class == p.Class && n == p.Object {
				cands = append(cands, &objs[i])
			}
		}
		if len(cands) != 1 {
			fail("patch %s: %s %s found %d times", p.ID, p.Class, p.Object, len(cands))
		}
		o := cands[0]
		i := o.PropIndex(p.Property)
		if i < 0 {
			fail("patch %s: %s.%s absent from the converted object", p.ID, p.Object, p.Property)
		}
		v := &o.Props[i]
		if !isStr(v.Type) {
			fail("patch %s: %s.%s has type %d (only string edits are ported)", p.ID, p.Object, p.Property, v.Type)
		}
		old := p.Old
		if old != v.S {
			for _, r := range raw[rawKey{p.Class, p.Object, p.Property}] {
				if r == old {
					old = v.S // quoted the unconverted X360 value
					break
				}
			}
		}
		if v.S != old {
			fail("patch %s: %s.%s is %q, want %q", p.ID, p.Object, p.Property, v.S, p.Old)
		}
		v.S = p.New
	}
}

// fixAINodeDependencies (fix_ainode_dependencies): GameServer uses an AINode's Dependency object through
// AINodeSmartObject virtual calls without a class check (the SFOD-D crash). Every AINode* object whose
// non-empty Dependency names an object of this world that is not an AINodeSmartObject (or subclass) gets the
// Dependency cleared; a name that matches no object is left alone (the lookup yields nothing). Returns the
// number cleared.
func fixAINodeDependencies(objs []wld.Object, smart map[string]bool) int {
	by := map[string][]string{}
	for i := range objs {
		if n, ok := objName(&objs[i]); ok && n != "" {
			k := latin1Lower(n)
			by[k] = append(by[k], objs[i].Class)
		}
	}
	cleared := 0
	for i := range objs {
		o := &objs[i]
		if !strings.HasPrefix(o.Class, "AINode") {
			continue
		}
		j := o.PropIndex("Dependency")
		if j < 0 {
			continue
		}
		p := &o.Props[j]
		if !isStr(p.Type) {
			fail("%s.Dependency is not a string", o.Class)
		}
		if p.S == "" {
			continue
		}
		cl, ok := by[latin1Lower(p.S)]
		if !ok {
			continue
		}
		all := true
		for _, c := range cl {
			if !smart[c] {
				all = false
				break
			}
		}
		if all {
			continue
		}
		p.S = ""
		cleared++
	}
	return cleared
}
