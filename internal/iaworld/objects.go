package iaworld

// Object conversion (console .wldsrvr objects -> PC object section) and PC resource names: a port of PCFiles,
// build_assetlist_pc, convert_objects and pc_schema_from_worlds of the project's Python world converter, and of
// the index the map builder builds from the folders Default.archcfg mounts.
//
// Differences from internal/sfodworld (which ports the SFOD-D converter): no DropToFloor -> MoveToFloor rule,
// no editor-only defaults, the resource index follows the Default.archcfg mount order (not the TOC file order),
// and the PC reference objects come from stock WTF_Exfil with the LAST object of a (class, Name) key winning.

import (
	"math"
	"strconv"
	"strings"

	"fearinstaller/internal/pcfs"
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

// pcFiles is the recipe's case-insensitive resource index: lower-case path -> spelling. Values keep the
// spelling of the last mount that has the path (archives in Default.archcfg order, then the mod folder).
// mod records which keys the mod list set, and hits which of those a lookup returned (see Report).
type pcFiles struct {
	idx  map[string]string
	mod  map[string]bool
	hits map[string]bool
}

// newPCFiles builds the index: the stock archives in mount order (pcfs.ArchOrder; archives the stock
// Default.archcfg does not mount are left out), each archive in its own entry order, then the mod's files.
func newPCFiles(listing []sfodworld.ArchiveFile, mod []string) *pcFiles {
	f := &pcFiles{idx: map[string]string{}, mod: map[string]bool{}, hits: map[string]bool{}}
	byRank := make([][]string, len(pcfs.ArchOrder))
	for _, e := range listing {
		r := archRank(e.Arch)
		if r >= 0 {
			byRank[r] = append(byRank[r], e.Path)
		}
	}
	for _, paths := range byRank {
		for _, p := range paths {
			f.idx[latin1Lower(p)] = p
		}
	}
	for _, p := range mod {
		p = strings.ReplaceAll(p, "/", "\\")
		k := latin1Lower(p)
		if f.idx[k] != p {
			f.mod[k] = true // only a mod name that adds a path or changes its spelling can change the output
		}
		f.idx[k] = p
	}
	return f
}

// archRank is the archive's position in the stock mount order, -1 when the stock Default.archcfg lacks it.
func archRank(name string) int {
	n := latin1Lower(strings.TrimSuffix(latin1Lower(name), ".arch00"))
	for i, a := range pcfs.ArchOrder {
		if latin1Lower(a) == n {
			return i
		}
	}
	return -1
}

func (f *pcFiles) find(path string) (string, bool) {
	k := latin1Lower(strings.ReplaceAll(path, "/", "\\"))
	v, ok := f.idx[k]
	if ok && f.mod[k] {
		f.hits[k] = true
	}
	return v, ok
}

var ps3ToPCExt = map[string]string{".tex": ".dds", ".mat": ".Mat00", ".mdl": ".Model00p", ".txanm": ".TxAnm00", ".vid": ".bik"}

var ps3OnlyExt = map[string]bool{".fxo": true, ".gcmfx": true, ".mesh": true, ".xwb": true, ".lip": true}

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
func (f *pcFiles) resolve(path string) (string, bool) {
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
	if latin1Lower(ext) == "model00c" {
		cands = append(cands, base+".Model00p")
	}
	for _, c := range cands {
		if hit, ok := f.find(c); ok {
			return hit, true
		}
	}
	return "", false
}

// buildAssetListPC maps the console precache list to PC names, dropping console-only types and anything not
// present on PC (build_assetlist_pc; the IA recipe adds no reference pairs).
func buildAssetListPC(pairs []assetPair, files *pcFiles) []byte {
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
	bad    map[propKey]string // GameServer.dll defaults the recipe could not write (see propDef.defaultValue)
}

// schemaFromWorld: per class the property order as the PC editor wrote it and the most common value of each
// property (first-seen value wins a tie, the stored value of a key is the last one seen: Counter.most_common).
func schemaFromWorld(objs []wld.Object) map[string]*classSchema {
	type cnt struct {
		order []string
		n     map[string]int
		last  map[string]wld.Prop
	}
	sc := map[string]*classSchema{}
	vals := map[string]map[propKey]*cnt{}
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

// addClassDefs extends the schema with the PropDefs of the player's GameServer.dll, as build_maps.py does:
// every (name, type) a class does not have yet is appended in PropDef order with the editor default.
func addClassDefs(sc map[string]*classSchema, defs []classDef) {
	for _, cd := range defs {
		s := sc[cd.name]
		if s == nil {
			s = &classSchema{types: map[propKey]bool{}, common: map[propKey]wld.Prop{}}
			sc[cd.name] = s
		}
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
	}
}

type refKey struct{ class, name string }

// refProps: (class, Name) -> {prop: value} of stock WTF_Exfil. The recipe's dict comprehension keys a nameless
// object as (class, "") and lets the LAST object of a key win; inside an object the last property of a name wins.
func refProps(objs []wld.Object) map[refKey]map[string]wld.Prop {
	ref := map[refKey]map[string]wld.Prop{}
	for i := range objs {
		n := "s" // '' when the object has no Name
		for _, p := range objs[i].Props {
			if p.Name == "Name" {
				n = valueKey(p)
				break
			}
		}
		m := map[string]wld.Prop{}
		for _, p := range objs[i].Props {
			m[p.Name] = p
		}
		ref[refKey{objs[i].Class, n}] = m
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

// convertObjects: console object list -> PC object list (convert_objects of the IA converter): properties the
// PC class lacks are dropped, PC-only properties are added in PC order (value from the PC reference object,
// else the schema's common/default value), file references get PC names. Like build_maps.py it refuses a class
// without schema and a file reference that does not resolve.
func convertObjects(objs []wld.Object, schema map[string]*classSchema, ref map[refKey]map[string]wld.Prop, files *pcFiles) []wld.Object {
	out := make([]wld.Object, 0, len(objs))
	for i := range objs {
		o := &objs[i]
		sc := schema[o.Class]
		if sc == nil {
			fail("class %s has no PC schema", o.Class)
		}
		name, hasName := "", false
		for _, p := range o.Props {
			if p.Name == "Name" {
				name, hasName = valueKey(p), true
				break
			}
		}
		have := &orderedProps{m: map[string]wld.Prop{}}
		for _, p := range o.Props {
			if !sc.types[propKey{p.Name, p.Type}] {
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
					hit, ok := files.resolve(p.S)
					if !ok {
						fail("%s.%s: file %q is not on PC", o.Class, p.Name, p.S)
					}
					p.S = hit
				}
			}
			p.N = append([]uint32(nil), p.N...)
			have.set(p)
		}
		var r map[string]wld.Prop
		if hasName {
			r = ref[refKey{o.Class, name}]
		}
		var props []wld.Prop
		for _, k := range sc.order {
			if p, ok := have.pop(k.name); ok {
				props = append(props, p)
				continue
			}
			var v wld.Prop
			if rp, ok := r[k.name]; r != nil && ok && rp.Type == k.typ {
				v = rp
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
