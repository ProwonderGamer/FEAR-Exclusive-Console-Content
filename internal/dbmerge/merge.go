// Package dbmerge ports the reference database pipeline (three Python stages: base merge, SFOD-D, Holiday) that merges the console-only records of the bonus missions
// into the stock PC game database FEAR.gamdb00p and the localized FEARL.Gamdb00p.
//
// Principles (all from the engine, GameDatabase.dll 0x10007020): categories, records and attributes are
// binary-searched with _stricmp, so they stay sorted; links are (category << 16 | record), so every link is
// handled symbolically and re-encoded last; saves and network messages store the record INDEX, so every added
// record gets the sort-last name "zSFODD_<name>" and is appended, which keeps every stock record's index.
//
// Every function here is deterministic and was written to reproduce the Python reference byte for byte.
package dbmerge

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"fearinstaller/internal/gadb"
)

// TOC answers which resource files exist on PC.
type TOC interface {
	// Find returns the archive spelling of a path (case-insensitive lookup) or "".
	Find(path string) string
}

// extMap maps console resource extensions to the PC ones.
var extMap = map[string]string{".mat": ".Mat00", ".mdl": ".Model00p", ".tex": ".dds", ".wld": ".World00p", ".vid": ".bik"}

// splitExt is Python's posixpath.splitext.
func splitExt(p string) (string, string) {
	sep := strings.LastIndexByte(p, '/')
	dot := strings.LastIndexByte(p, '.')
	if dot > sep {
		fi := sep + 1
		for fi < dot {
			if p[fi] != '.' {
				return p[:dot], p[dot:]
			}
			fi++
		}
	}
	return p, ""
}

// norm replaces struct element indices: "Ammo.10.Ammo" -> "Ammo.#.Ammo" (Python re.sub(r'\.\d+(?=\.)', '.#')).
func norm(name string) string {
	var sb strings.Builder
	i := 0
	for i < len(name) {
		if name[i] == '.' {
			j := i + 1
			for j < len(name) && name[j] >= '0' && name[j] <= '9' {
				j++
			}
			if j > i+1 && j < len(name) && name[j] == '.' {
				sb.WriteString(".#")
				i = j
				continue
			}
		}
		sb.WriteByte(name[i])
		i++
	}
	return sb.String()
}

var reIdx = regexp.MustCompile(`\.\d+\.`)

func zeroIdx(name string) string { return reIdx.ReplaceAllString(name, ".0.") }

// ---------------------------------------------------------------------------------------------
// log
// ---------------------------------------------------------------------------------------------

// Log collects human readable decisions per section.
type Log struct {
	order []string
	lines map[string][]string
}

// NewLog returns an empty log.
func NewLog() *Log { return &Log{lines: map[string][]string{}} }

// Add appends a line to a section.
func (l *Log) Add(section, text string) {
	if _, ok := l.lines[section]; !ok {
		l.order = append(l.order, section)
	}
	l.lines[section] = append(l.lines[section], text)
}

// String renders the log.
func (l *Log) String() string {
	var sb strings.Builder
	for _, s := range l.order {
		fmt.Fprintf(&sb, "\n## %s (%d)\n", s, len(l.lines[s]))
		for _, t := range l.lines[s] {
			sb.WriteString(t + "\n")
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------------------------
// schema + translation tables
// ---------------------------------------------------------------------------------------------

type tf struct{ t, fl uint32 }

// schema: per category, normalised attribute pattern -> (type, flags) from the PC records.
type schema struct {
	pat      map[string]tf
	order    []string
	required map[string]bool
}

func newSchema(c *gadb.Category) *schema {
	s := &schema{pat: map[string]tf{}}
	for _, r := range c.Records {
		for _, a := range r.Attrs {
			p := norm(a.Name)
			if _, ok := s.pat[p]; !ok {
				s.pat[p] = tf{a.Type, a.Flags}
				s.order = append(s.order, p)
			}
		}
	}
	var req map[string]bool
	for _, r := range c.Records {
		tops := map[string]bool{}
		for _, a := range r.Attrs {
			if !strings.Contains(a.Name, ".") {
				tops[a.Name] = true
			}
		}
		if req == nil {
			req = tops
		} else {
			for k := range req {
				if !tops[k] {
					delete(req, k)
				}
			}
		}
	}
	if req == nil {
		req = map[string]bool{}
	}
	s.required = req
	return s
}

type transKey struct{ cat, pat string }

type transEntry struct {
	w gadb.Value
	n int
}

type affixRule struct {
	pre, suf string
	n        int
}

type wCount struct {
	w   gadb.Value
	key string
	n   int
}

type vEntry struct {
	v   gadb.Value
	key string
	ws  []*wCount
}

type seenEntry struct {
	t   uint32
	vs  []*vEntry
	idx map[string]*vEntry
}

// learnTranslations learns (category, attribute pattern) -> {console value: PC value} from the records that
// exist on both platforms. Only string and vector values; a console value enters the table only if every
// occurrence maps to the same PC value (strings need 2 records of evidence, vectors 3). An affix rule
// (PC = prefix + console + suffix) is learned when every differing string pair obeys one prefix/suffix.
func learnTranslations(pc, ps3 *gadb.Database) (map[transKey]map[string]transEntry, map[transKey]affixRule) {
	pcrec := pc.RecMap()
	seen := map[transKey]*seenEntry{}
	var order []transKey
	for _, c := range ps3.Cats {
		for _, r := range c.Records {
			p := pcrec[gadb.RecKey{Cat: c.Name, Rec: r.Name}]
			if p == nil {
				continue
			}
			pa := map[string]*gadb.Attr{}
			for _, a := range p.Attrs {
				pa[a.Name] = a
			}
			for _, a := range r.Attrs {
				b := pa[a.Name]
				if b == nil || a.Type != b.Type || (a.Type != gadb.TString && a.Type != gadb.TVec2 && a.Type != gadb.TVec3 && a.Type != gadb.TVec4) {
					continue
				}
				var pairs [][2]gadb.Value
				if len(a.Values) != len(b.Values) {
					if len(a.Values) > 0 && len(b.Values) > 0 {
						pairs = [][2]gadb.Value{{a.Values[0], b.Values[0]}}
					}
				} else {
					for i := range a.Values {
						pairs = append(pairs, [2]gadb.Value{a.Values[i], b.Values[i]})
					}
				}
				tk := transKey{c.Name, norm(a.Name)}
				se := seen[tk]
				if se == nil {
					se = &seenEntry{t: a.Type, idx: map[string]*vEntry{}}
					seen[tk] = se
					order = append(order, tk)
				}
				for _, pr := range pairs {
					vk := gadb.Key(a.Type, pr[0])
					ve := se.idx[vk]
					if ve == nil {
						ve = &vEntry{v: pr[0], key: vk}
						se.idx[vk] = ve
						se.vs = append(se.vs, ve)
					}
					wk := gadb.Key(a.Type, pr[1])
					var wc *wCount
					for _, x := range ve.ws {
						if x.key == wk {
							wc = x
							break
						}
					}
					if wc == nil {
						wc = &wCount{w: pr[1], key: wk}
						ve.ws = append(ve.ws, wc)
					}
					wc.n++
				}
			}
		}
	}
	table := map[transKey]map[string]transEntry{}
	affix := map[transKey]affixRule{}
	for _, tk := range order {
		se := seen[tk]
		// affix rule
		type pair struct{ v, w gadb.Value }
		var pairs []pair
		for _, ve := range se.vs {
			if se.t != gadb.TString || ve.v.Null || ve.v.S == "" {
				continue
			}
			for _, wc := range ve.ws {
				if !gadb.Equal(se.t, wc.w, ve.v) {
					pairs = append(pairs, pair{ve.v, wc.w})
				}
			}
		}
		if len(pairs) >= 3 && !pairs[0].w.Null {
			v0, w0 := pairs[0].v.S, pairs[0].w.S
			if i := strings.Index(w0, v0); i >= 0 {
				pre, suf := w0[:i], w0[i+len(v0):]
				ok := true
				for _, p := range pairs {
					if p.w.Null || p.w.S != pre+p.v.S+suf {
						ok = false
						break
					}
				}
				if ok {
					for _, ve := range se.vs {
						if se.t == gadb.TString && !ve.v.Null && ve.v.S != "" && len(ve.ws) != 1 {
							ok = false
							break
						}
					}
				}
				if ok {
					affix[tk] = affixRule{pre, suf, len(pairs)}
				}
			}
		}
		t := map[string]transEntry{}
		for _, ve := range se.vs {
			if len(ve.ws) != 1 {
				continue
			}
			wc := ve.ws[0]
			need := 2
			if se.t != gadb.TString {
				need = 3
			}
			if !gadb.Equal(se.t, wc.w, ve.v) && wc.n >= need {
				t[ve.key] = transEntry{wc.w, wc.n}
			}
		}
		if len(t) > 0 {
			table[tk] = t
		}
	}
	return table, affix
}

// ---------------------------------------------------------------------------------------------
// Merger
// ---------------------------------------------------------------------------------------------

// Merger converts console records to the PC schema of their category.
type Merger struct {
	pc, ps3 *gadb.Database
	toc     TOC
	log     *Log

	pccat map[string]*gadb.Category
	pcrec map[gadb.RecKey]*gadb.Record
	pcci  map[gadb.RecKey]string // (cat, lower name) -> name
	ps3rc map[gadb.RecKey]*gadb.Record
	ps3ci map[gadb.RecKey]string
	sch   map[string]*schema
	trans map[transKey]map[string]transEntry
	affix map[transKey]affixRule

	// Templates: (category or "*", record) -> PC template record name; mutable by the caller.
	Templates map[[2]string]string

	Added        map[gadb.RecKey]*gadb.Record
	AddedOrder   []gadb.RecKey
	Reason       map[gadb.RecKey]string
	MissingFiles map[string][]string
	MissingOrder []string
	FxNames      map[string][]string
	queue        []gadb.RecKey

	simCache map[string][]simRec
}

type simRec struct {
	name string
	set  map[string]bool
}

// NewMerger prepares a merge of console database ps3 into PC database pc. Both must be symbolized.
func NewMerger(pc, ps3 *gadb.Database, toc TOC, log *Log, templates map[[2]string]string) *Merger {
	m := &Merger{pc: pc, ps3: ps3, toc: toc, log: log,
		pccat: map[string]*gadb.Category{}, pcrec: pc.RecMap(), pcci: map[gadb.RecKey]string{},
		ps3rc: ps3.RecMap(), ps3ci: map[gadb.RecKey]string{}, sch: map[string]*schema{},
		Templates: templates, Added: map[gadb.RecKey]*gadb.Record{}, Reason: map[gadb.RecKey]string{},
		MissingFiles: map[string][]string{}, FxNames: map[string][]string{}, simCache: map[string][]simRec{}}
	for _, c := range pc.Cats {
		m.pccat[c.Name] = c
		m.sch[c.Name] = newSchema(c)
		for _, r := range c.Records {
			m.pcci[gadb.RecKey{Cat: c.Name, Rec: gadb.Lower(r.Name)}] = r.Name
		}
	}
	for _, c := range ps3.Cats {
		for _, r := range c.Records {
			m.ps3ci[gadb.RecKey{Cat: c.Name, Rec: gadb.Lower(r.Name)}] = r.Name
		}
	}
	m.trans, m.affix = learnTranslations(pc, ps3)
	return m
}

// Want queues a console record (and, via link closure, its dependencies) for conversion.
func (m *Merger) Want(cat, rec, why string) error {
	if n, ok := m.ps3ci[gadb.RecKey{Cat: cat, Rec: gadb.Lower(rec)}]; ok {
		rec = n
	}
	if n, ok := m.pcci[gadb.RecKey{Cat: cat, Rec: gadb.Lower(rec)}]; ok {
		rec = n
	}
	key := gadb.RecKey{Cat: cat, Rec: rec}
	if _, ok := m.pcrec[key]; ok {
		return nil
	}
	if _, ok := m.Added[key]; ok {
		return nil
	}
	if _, ok := m.Reason[key]; ok {
		return nil
	}
	if _, ok := m.ps3rc[key]; !ok {
		return fmt.Errorf("%s/%s is in neither database", cat, rec)
	}
	m.Reason[key] = why
	m.queue = append(m.queue, key)
	return nil
}

func (m *Merger) templateFor(cat, rec string) (*gadb.Record, string) {
	for _, k := range [][2]string{{cat, rec}, {"*", rec}, {cat, "*"}} {
		if t, ok := m.Templates[k]; ok {
			if r, ok := m.pcrec[gadb.RecKey{Cat: cat, Rec: t}]; ok {
				return r, "explicit"
			}
		}
	}
	c := m.pccat[cat]
	if c == nil || len(c.Records) == 0 {
		return nil, "none"
	}
	sims, ok := m.simCache[cat]
	if !ok {
		for _, r := range c.Records {
			sims = append(sims, simRec{r.Name, pairSet(r)})
		}
		m.simCache[cat] = sims
	}
	src := pairSet(m.ps3rc[gadb.RecKey{Cat: cat, Rec: rec}])
	bestN, bestName := -1, ""
	var best *gadb.Record
	for i, sr := range sims {
		n := 0
		for k := range src {
			if sr.set[k] {
				n++
			}
		}
		if n > bestN || (n == bestN && sr.name > bestName) {
			bestN, bestName, best = n, sr.name, c.Records[i]
		}
	}
	return best, "most-similar"
}

// pairSet builds the set of (name, str(values)) pairs of a record (Python: tuple(map(str, a.values))).
func pairSet(r *gadb.Record) map[string]bool {
	s := map[string]bool{}
	for _, a := range r.Attrs {
		var sb strings.Builder
		sb.WriteString(a.Name)
		sb.WriteByte(0)
		for _, v := range a.Values {
			sb.WriteString(pyStr(a.Type, v))
			sb.WriteByte(1)
		}
		s[sb.String()] = true
	}
	return s
}

func pyRepr(s string) string {
	q := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		q = '"'
	}
	var sb strings.Builder
	sb.WriteByte(q)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == q || c == '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		case c == '\n':
			sb.WriteString(`\n`)
		case c == '\t':
			sb.WriteString(`\t`)
		case c == '\r':
			sb.WriteString(`\r`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&sb, `\x%02x`, c)
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte(q)
	return sb.String()
}

// pyStr renders a value the way Python's str() shows the in-memory value of the reference tool.
func pyStr(t uint32, v gadb.Value) string {
	switch t {
	case gadb.TString:
		if v.Null {
			return "None"
		}
		return v.S
	case gadb.TWStr:
		if v.Null {
			return "None"
		}
		return string(rune(0)) + fmt.Sprint(v.W)
	case gadb.TVec2, gadb.TVec3, gadb.TVec4:
		if v.Null {
			return "None"
		}
		parts := make([]string, len(v.V))
		for i, x := range v.V {
			parts[i] = strconv.FormatUint(uint64(x), 10)
		}
		if len(parts) == 1 {
			return "(" + parts[0] + ",)"
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case gadb.TLink:
		if v.Sym {
			if v.Null {
				return "None"
			}
			return "('@', " + pyRepr(v.Cat) + ", " + pyRepr(v.Rec) + ")"
		}
	}
	return strconv.FormatUint(uint64(v.U), 10)
}

func (m *Merger) addMissing(path, where string) {
	if _, ok := m.MissingFiles[path]; !ok {
		m.MissingOrder = append(m.MissingOrder, path)
	}
	m.MissingFiles[path] = append(m.MissingFiles[path], where)
}

func (m *Merger) convertPath(where, v string) string {
	if v == "" {
		return v
	}
	base, ext := splitExt(v)
	pcv := v
	if rep, ok := extMap[gadb.Lower(ext)]; ok {
		pcv = base + rep
	}
	if strings.HasPrefix(gadb.Lower(pcv), `prototype\`) {
		pcv = pcv[len(`prototype\`):]
	}
	if found := m.toc.Find(pcv); found != "" {
		if found != v {
			m.log.Add("file-name conversions", fmt.Sprintf("%s: %q -> %q", where, v, found))
		}
		return found
	}
	if pcv != v {
		m.log.Add("file-name conversions", fmt.Sprintf("%s: %q -> %q (NOT ON PC)", where, v, pcv))
	}
	m.addMissing(pcv, where)
	return pcv
}

func (m *Merger) convertValue(where, cat, attr string, t, flags uint32, v gadb.Value) (gadb.Value, error) {
	if t == gadb.TLink {
		if v.Null {
			return v, nil
		}
		k := gadb.RecKey{Cat: v.Cat, Rec: v.Rec}
		if _, ok := m.pcrec[k]; ok {
			return v, nil
		}
		if alt, ok := m.pcci[gadb.RecKey{Cat: v.Cat, Rec: gadb.Lower(v.Rec)}]; ok {
			m.log.Add("links", fmt.Sprintf("%s: -> %s/%s resolved case-insensitively to PC %s/%s", where, v.Cat, v.Rec, v.Cat, alt))
			return gadb.Link(v.Cat, alt), nil
		}
		if _, ok := m.ps3rc[k]; ok {
			if err := m.Want(v.Cat, v.Rec, "linked from "+where); err != nil {
				return v, err
			}
			m.log.Add("links", fmt.Sprintf("%s: -> %s/%s (console record, added)", where, v.Cat, v.Rec))
			return v, nil
		}
		return v, fmt.Errorf("%s: dangling console link %s/%s", where, v.Cat, v.Rec)
	}
	if (t == gadb.TString || t == gadb.TVec2 || t == gadb.TVec3 || t == gadb.TVec4) && !v.Null {
		tk := transKey{cat, norm(attr)}
		if tt := m.trans[tk]; tt != nil {
			if e, ok := tt[gadb.Key(t, v)]; ok {
				m.log.Add("learned PS3->PC value translations", fmt.Sprintf("%s (unanimous in %d shared records)", where, e.n))
				w := e.w
				if t == gadb.TString && flags == 1 && w.S != "" && m.toc.Find(w.S) == "" {
					m.addMissing(w.S, where)
				}
				return w, nil
			}
		}
		if af, ok := m.affix[tk]; ok && t == gadb.TString && v.S != "" {
			w := af.pre + v.S + af.suf
			m.log.Add("learned PS3->PC value translations", fmt.Sprintf("%s: %q -> %q (affix rule from %d shared values)", where, v.S, w, af.n))
			if flags == 1 && m.toc.Find(w) == "" {
				m.addMissing(w, where)
			}
			return gadb.Str(w), nil
		}
		if t == gadb.TString && flags == 1 {
			return gadb.Str(m.convertPath(where, v.S)), nil
		}
		if t == gadb.TString && flags == 2 && v.S != "" {
			m.FxNames[v.S] = append(m.FxNames[v.S], where)
		}
	}
	return v, nil
}

// convert builds the PC record for a console record.
func (m *Merger) convert(cat, rec string) (*gadb.Record, error) {
	src := m.ps3rc[gadb.RecKey{Cat: cat, Rec: rec}]
	where0 := cat + "/" + rec
	if _, ok := m.pccat[cat]; !ok {
		return nil, fmt.Errorf("%s does not exist on PC (new categories are not supported without review)", cat)
	}
	sch := m.sch[cat]
	tmpl, how := m.templateFor(cat, rec)
	if tmpl != nil {
		m.log.Add("templates", fmt.Sprintf("%s: template PC %s/%s (%s)", where0, cat, tmpl.Name, how))
	}
	var out []*gadb.Attr
	have := map[string]bool{}
	for _, a := range src.Attrs {
		p := norm(a.Name)
		pt, ok := sch.pat[p]
		if !ok {
			m.log.Add("PS3-only attributes dropped", where0+"."+a.Name)
			continue
		}
		if a.Type != pt.t {
			m.log.Add("type mismatches (PS3 value dropped, template used)", fmt.Sprintf("%s.%s: PS3 type %d, PC type %d", where0, a.Name, a.Type, pt.t))
			continue
		}
		if a.Flags != pt.fl {
			m.log.Add("flag differences (PC flags used)", fmt.Sprintf("%s.%s: PS3 %d PC %d", where0, a.Name, a.Flags, pt.fl))
		}
		vals := make([]gadb.Value, len(a.Values))
		for i, v := range a.Values {
			nv, err := m.convertValue(where0+"."+a.Name, cat, a.Name, pt.t, pt.fl, v)
			if err != nil {
				return nil, err
			}
			vals[i] = nv
		}
		out = append(out, &gadb.Attr{Name: a.Name, Type: pt.t, Flags: pt.fl, Values: vals})
		have[a.Name] = true
	}
	fill := func(name string, t, fl uint32) {
		var srcAttr *gadb.Attr
		if tmpl != nil {
			srcAttr = tmpl.Attr(name)
			if srcAttr == nil {
				srcAttr = tmpl.Attr(zeroIdx(name))
			}
		}
		if srcAttr == nil {
			for _, r := range m.pccat[cat].Records {
				srcAttr = r.Attr(name)
				if srcAttr == nil {
					srcAttr = r.Attr(zeroIdx(name))
				}
				if srcAttr != nil {
					break
				}
			}
		}
		var vals []gadb.Value
		if srcAttr != nil {
			vals = append(vals, srcAttr.Values...)
		}
		tn := "?"
		if tmpl != nil {
			tn = tmpl.Name
		}
		m.log.Add("PC-only attributes filled from template", fmt.Sprintf("%s.%s (from %s/%s)", where0, name, cat, tn))
		out = append(out, &gadb.Attr{Name: name, Type: t, Flags: fl, Values: vals})
		have[name] = true
	}
	var req []string
	for n := range sch.required {
		req = append(req, n)
	}
	sort.Slice(req, func(i, j int) bool { return gadb.SKey(req[i]) < gadb.SKey(req[j]) })
	for _, name := range req {
		if !have[name] {
			pt := sch.pat[name]
			fill(name, pt.t, pt.fl)
		}
	}
	changed := true
	for changed {
		changed = false
		snapshot := append([]*gadb.Attr(nil), out...)
		for _, a := range snapshot {
			if a.Type != gadb.TStruct {
				continue
			}
			np := norm(a.Name)
			var members []string
			for _, p := range sch.order {
				if strings.HasPrefix(p, np+".#.") && !strings.Contains(p[len(np)+3:], ".") {
					members = append(members, p)
				}
			}
			for i := range a.Values {
				for _, mem := range members {
					name := fmt.Sprintf("%s.%d.%s", a.Name, i, mem[len(np)+3:])
					if !have[name] {
						pt := sch.pat[mem]
						fill(name, pt.t, pt.fl)
						changed = true
					}
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return gadb.SKey(out[i].Name) < gadb.SKey(out[j].Name) })
	return &gadb.Record{Name: rec, Attrs: out}, nil
}

// Run converts every queued record until the link closure is complete.
func (m *Merger) Run() error {
	for len(m.queue) > 0 {
		key := m.queue[0]
		m.queue = m.queue[1:]
		if _, ok := m.Added[key]; ok {
			continue
		}
		r, err := m.convert(key.Cat, key.Rec)
		if err != nil {
			return err
		}
		m.Added[key] = r
		m.AddedOrder = append(m.AddedOrder, key)
	}
	return nil
}
