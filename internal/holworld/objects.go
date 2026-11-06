package holworld

// Object conversion (console .wldsrvr objects -> PC object section) and PC resource names: copied from
// internal/sfodworld (a port of PCFiles, build_assetlist_pc, convert_objects and pc_schema_from_worlds, the same
// in the Holiday converter) plus the Holiday additions: .xsb is a console-only type, and the
// GameServer.dll ClassDef schema (schemaFromClassDefs, widenTypes).

import (
	"math"
	"strconv"
	"strings"

	"fearinstaller/internal/sfodworld"
	"fearinstaller/internal/wld"
)

// latin1Lower lower-cases a Latin-1 byte string the way Python's str.lower() does on the recipe's Latin-1
// decoded strings (A-Z and U+00C0-U+00DE except U+00D7).
func latin1Lower(s string) string {
	b := []byte(s)
	changed := false
	for i, c := range b {
		if ('A' <= c && c <= 'Z') || (0xC0 <= c && c <= 0xDE && c != 0xD7) {
			b[i] = c + 0x20
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(b)
}

// pcFiles is the case-insensitive index of PC resource paths (the recipe's PCFiles): archive listing in TOC
// order, then the files the mod itself adds; the last spelling wins.
type pcFiles map[string]string

func newPCFiles(listing []sfodworld.ArchiveFile, extra []string) pcFiles {
	l := append([]sfodworld.ArchiveFile(nil), listing...)
	sfodworld.SortListing(l)
	f := pcFiles{}
	for _, e := range l {
		f[latin1Lower(e.Path)] = e.Path
	}
	for _, p := range extra {
		f[latin1Lower(p)] = p
	}
	return f
}

func (f pcFiles) find(path string) (string, bool) {
	v, ok := f[latin1Lower(strings.ReplaceAll(path, "/", "\\"))]
	return v, ok
}

var ps3ToPCExt = map[string]string{".tex": ".dds", ".mat": ".Mat00", ".mdl": ".Model00p", ".txanm": ".TxAnm00", ".vid": ".bik"}

// ps3OnlyExt: console-only resource types dropped from the precache list (hol_world2pc PS3_ONLY_EXT adds .xsb;
// .xsb/.xwb are Xbox 360 XACT banks).
var ps3OnlyExt = map[string]bool{".fxo": true, ".gcmfx": true, ".mesh": true, ".xwb": true, ".xsb": true, ".lip": true}

// rpartition mirrors Python's str.rpartition.
func rpartition(s, sep string) (string, string, string) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], sep, s[i+len(sep):]
	}
	return "", "", s
}

func mapExt(ext string) string {
	if v, ok := ps3ToPCExt["."+latin1Lower(ext)]; ok {
		return v
	}
	return "." + ext
}

// resolve: console resource path -> existing PC path (PC spelling).
func (f pcFiles) resolve(path string) (string, bool) {
	p := strings.ReplaceAll(path, "/", "\\")
	base, dot, ext := rpartition(p, ".")
	var cands []string
	if dot != "" {
		cands = []string{base + mapExt(ext)}
	} else {
		cands = []string{p}
	}
	if strings.HasPrefix(latin1Lower(p), "prototype\\") {
		cands = append(cands, cands[0][len("prototype\\"):])
	}
	if latin1Lower(ext) == "model00c" { // sfod_d HealthMax00.H names a .Model00c; PC ships .Model00p
		cands = append(cands, base+".Model00p")
	}
	for _, c := range cands {
		if hit, ok := f.find(c); ok {
			return hit, true
		}
	}
	return "", false
}

// buildAssetListPC maps the console precache list (plus PC Admin's) to PC names, dropping console-only types and
// anything not present on PC.
func buildAssetListPC(pairs []assetPair, files pcFiles) []byte {
	var out []assetPair
	seen := map[[2]string]bool{}
	for _, pr := range pairs {
		ext := ""
		if strings.Contains(pr.file, ".") {
			_, _, e := rpartition(pr.file, ".")
			ext = "." + latin1Lower(e)
		}
		if ps3OnlyExt[ext] {
			continue
		}
		q := pr.file
		if pr.dir != "" {
			q = pr.dir + "\\" + pr.file
		}
		hit, ok := files.resolve(q)
		if !ok {
			continue
		}
		d2, _, f2 := rpartition(hit, "\\")
		k := [2]string{latin1Lower(d2), latin1Lower(f2)}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, assetPair{d2, f2})
	}
	return buildAssetList(out)
}

// fileExts: string/command values ending in one of these are file references.
var fileExts = []string{".tex", ".mdl", ".mat", ".txanm", ".wav", ".model00p", ".model00c", ".dds", ".mat00"}

// editorOnly: PC properties only the editor reads; when PC Admin does not supply a value the PropDef default
// is written instead of another object's most common value.
var editorOnly = map[[2]string]string{{"CommandObject", "EventDialogInfo"}: ""}

type propKey struct {
	name string
	typ  uint32
}

// valueKey identifies a property value the way the recipe's repr(v) does (NaNs share one key).
func valueKey(p wld.Prop) string {
	fl := func(u uint32) string {
		if f := math.Float32frombits(u); f != f {
			return "nan"
		}
		return strconv.FormatUint(uint64(u), 16)
	}
	switch p.Type {
	case wld.TString, wld.TCommand, wld.TStringID:
		return "s" + p.S
	case wld.TVector, wld.TColor, wld.TRot:
		var b strings.Builder
		b.WriteString("v")
		for _, u := range p.N {
			b.WriteString(fl(u) + ",")
		}
		return b.String()
	case wld.TFloat:
		return "f" + fl(p.U)
	case wld.TBool:
		return "b" + strconv.FormatUint(uint64(p.U), 10)
	}
	return "r" + strconv.FormatUint(uint64(p.U), 10)
}

type classSchema struct {
	order  []propKey
	types  map[propKey]bool
	common map[propKey]wld.Prop
	bad    map[propKey]string // ClassDef defaults the recipe could not write (see propDef.defaultValue)
	fromCD bool               // built from the GameServer.dll ClassDef (schema_from_classdefs 'from_classdef')
}

// schemaFromWorlds: per class the property order as the PC editor wrote it and the most common value of each
// property (first-seen value wins a tie, the stored value of a key is the last one seen: Counter.most_common).
func schemaFromWorlds(worlds [][]wld.Object) map[string]*classSchema {
	type cnt struct {
		order []string
		n     map[string]int
		last  map[string]wld.Prop
	}
	sc := map[string]*classSchema{}
	vals := map[string]map[propKey]*cnt{}
	for _, objs := range worlds {
		for _, o := range objs {
			s := sc[o.Class]
			if s == nil {
				s = &classSchema{types: map[propKey]bool{}, common: map[propKey]wld.Prop{}}
				sc[o.Class] = s
				vals[o.Class] = map[propKey]*cnt{}
			}
			for _, p := range o.Props {
				k := propKey{p.Name, p.Type}
				if !s.types[k] {
					s.types[k] = true
					s.order = append(s.order, k)
				}
				c := vals[o.Class][k]
				if c == nil {
					c = &cnt{n: map[string]int{}, last: map[string]wld.Prop{}}
					vals[o.Class][k] = c
				}
				vk := valueKey(p)
				if _, ok := c.n[vk]; !ok {
					c.order = append(c.order, vk)
				}
				c.n[vk]++
				c.last[vk] = p
			}
		}
	}
	for cls, m := range vals {
		for k, c := range m {
			best := c.order[0]
			for _, vk := range c.order[1:] {
				if c.n[vk] > c.n[best] {
					best = vk
				}
			}
			sc[cls].common[k] = c.last[best]
		}
	}
	return sc
}

// nameKey: the object's first "Name" property as the recipe's lookup key ((class, Name) or (class, None)).
func nameKey(o *wld.Object) (string, bool) {
	for _, p := range o.Props {
		if p.Name == "Name" {
			return valueKey(p), true
		}
	}
	return "", false
}

type refKey struct {
	class, name string
	has         bool
}

// refProps: (class, Name) -> {prop: value} of the PC source level (PC Admin); first object of a key wins,
// inside an object the last property of a name wins.
func refProps(objs []wld.Object) map[refKey]map[string]wld.Prop {
	ref := map[refKey]map[string]wld.Prop{}
	for i := range objs {
		n, has := nameKey(&objs[i])
		k := refKey{objs[i].Class, n, has}
		if _, ok := ref[k]; ok {
			continue
		}
		m := map[string]wld.Prop{}
		for _, p := range objs[i].Props {
			m[p.Name] = p
		}
		ref[k] = m
	}
	return ref
}

// orderedProps is the recipe's `have` dict: insertion ordered, keyed by property name.
type orderedProps struct {
	names []string
	m     map[string]wld.Prop
}

func (h *orderedProps) set(p wld.Prop) {
	if _, ok := h.m[p.Name]; !ok {
		h.names = append(h.names, p.Name)
	}
	h.m[p.Name] = p
}

func (h *orderedProps) pop(n string) (wld.Prop, bool) {
	p, ok := h.m[n]
	if !ok {
		return p, false
	}
	delete(h.m, n)
	for i, x := range h.names {
		if x == n {
			h.names = append(h.names[:i], h.names[i+1:]...)
			break
		}
	}
	return p, true
}

func (h *orderedProps) list() []wld.Prop {
	out := make([]wld.Prop, 0, len(h.names))
	for _, n := range h.names {
		out = append(out, h.m[n])
	}
	return out
}

// convertObjects: console object list -> PC object list (see convert_objects in the recipe's converter):
// properties the PC class lacks are dropped, PC-only properties are added in PC order (value from the PC
// reference object, else the most common PC value), file references get PC names, and the console-only
// DropToFloor sets MoveToFloor.
func convertObjects(objs []wld.Object, schema map[string]*classSchema, ref map[refKey]map[string]wld.Prop, files pcFiles) []wld.Object {
	out := make([]wld.Object, 0, len(objs))
	for i := range objs {
		o := &objs[i]
		sc := schema[o.Class]
		n, hasName := nameKey(o)
		have := &orderedProps{m: map[string]wld.Prop{}}
		dropToFloor := false
		for _, p := range o.Props {
			if p.Name == "DropToFloor" && p.Type == wld.TBool && p.U != 0 {
				dropToFloor = true
			}
		}
		for _, p := range o.Props {
			if sc != nil && !sc.types[propKey{p.Name, p.Type}] {
				continue
			}
			if p.Type == wld.TString || p.Type == wld.TCommand {
				lv := latin1Lower(p.S)
				isFile := false
				for _, e := range fileExts {
					if strings.HasSuffix(lv, e) {
						isFile = true
						break
					}
				}
				if isFile && !strings.Contains(p.S, ";") {
					if hit, ok := files.resolve(p.S); ok {
						p.S = hit
					} else {
						base, _, ext := rpartition(p.S, ".")
						p.S = base + mapExt(ext)
					}
				}
			}
			p.N = append([]uint32(nil), p.N...)
			have.set(p)
		}
		if dropToFloor && sc != nil && sc.types[propKey{"MoveToFloor", wld.TBool}] {
			have.set(wld.Prop{Name: "MoveToFloor", Type: wld.TBool, U: 1})
		}
		if sc == nil {
			out = append(out, wld.Object{Class: o.Class, Props: have.list()})
			continue
		}
		r := ref[refKey{o.Class, n, hasName}]
		var props []wld.Prop
		for _, k := range sc.order {
			if p, ok := have.pop(k.name); ok {
				props = append(props, p)
				continue
			}
			var v wld.Prop
			if rp, ok := r[k.name]; r != nil && ok && rp.Type == k.typ {
				v = rp
			} else if d, ok := editorOnly[[2]string{o.Class, k.name}]; ok {
				v = wld.Prop{Name: k.name, Type: k.typ, S: d}
			} else {
				if why, isBad := sc.bad[k]; isBad {
					fail("%s.%s: GameServer.dll default cannot be written (%s)", o.Class, k.name, why)
				}
				v = sc.common[k]
			}
			v.N = append([]uint32(nil), v.N...)
			props = append(props, v)
		}
		props = append(props, have.list()...)
		out = append(out, wld.Object{Class: o.Class, Props: props})
	}
	return out
}

// pcObjects parses the object section of a PC .World00p.
func pcObjects(pc []byte) []wld.Object {
	objs, _, _, err := wld.ParsePCWorld(pc)
	if err != nil {
		fail("PC world objects: %v", err)
	}
	return objs
}
