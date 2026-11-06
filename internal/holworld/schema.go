package holworld

// GameServer.dll ClassDef schema for Holiday: a port of schema_from_classdefs and the ClassDef block of
// convert_world in the Holiday converter. It differs from iaworld's addClassDefs:
//   - a class gets a ClassDef schema only when an X360 object uses it and no PC reference world has it
//     (iaworld adds every ClassDef and appends missing properties to classes the PC world already has);
//   - a class the PC worlds do have keeps their property order and most-common values, but its accepted
//     (name, type) set is widened by its ClassDef, so a console property the PC class defines is kept even
//     when no PC world wrote it (Holiday: ConstraintWheel.MPClientOnly).

import "fearinstaller/internal/wld"

// classDefIndex is pc_classdefs.load's dict: class name -> ClassDef (loadClassDefs already lets a later
// record of the same name win).
func classDefIndex(defs []classDef) map[string]*classDef {
	m := make(map[string]*classDef, len(defs))
	for i := range defs {
		m[defs[i].name] = &defs[i]
	}
	return m
}

// applyClassDefs adds the ClassDef schemas of the console classes no PC world has (schema_from_classdefs:
// property order and types as the PC class registers them, first (name, type) wins, 'common' = the PropDef
// editor default) and widens the types of the PC-world classes. It returns the names given a ClassDef schema.
func applyClassDefs(schema map[string]*classSchema, cdefs map[string]*classDef, objs []wld.Object) []string {
	var added []string
	seen := map[string]bool{}
	for i := range objs {
		cls := objs[i].Class
		if seen[cls] {
			continue
		}
		seen[cls] = true
		cd := cdefs[cls]
		if schema[cls] != nil || cd == nil {
			continue
		}
		s := &classSchema{types: map[propKey]bool{}, common: map[propKey]wld.Prop{}, fromCD: true}
		for _, p := range cd.props {
			k := propKey{p.name, p.typ}
			if s.types[k] {
				continue
			}
			s.types[k] = true
			s.order = append(s.order, k)
			var bad string
			if s.common[k], bad = p.defaultValue(); bad != "" {
				if s.bad == nil {
					s.bad = map[propKey]string{}
				}
				s.bad[k] = bad
			}
		}
		schema[cls] = s
		added = append(added, cls)
	}
	for cls, s := range schema {
		cd := cdefs[cls]
		if cd == nil || s.fromCD {
			continue
		}
		for _, p := range cd.props {
			s.types[propKey{p.name, p.typ}] = true
		}
	}
	return added
}

// smartObjectClasses: AINodeSmartObject and every class whose ClassDef parent chain contains it.
func smartObjectClasses(cdefs map[string]*classDef) map[string]bool {
	out := map[string]bool{}
	for n, cd := range cdefs {
		if n == "AINodeSmartObject" {
			out[n] = true
			continue
		}
		for _, p := range cd.parents {
			if p == "AINodeSmartObject" {
				out[n] = true
				break
			}
		}
	}
	return out
}
