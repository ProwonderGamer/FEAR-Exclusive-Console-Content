// Package objects00p builds the "<World>.Objects00p" override files that restore the Xbox 360 SM15
// machine pistol and the PS3 Watson SAS-12 placements in the stock PC campaign.
//
// Format (confirmed from FEAR.exe 0x457f30 / EngineServer 0x100462e0 and the shipped MP Facility/Factory
// Objects00p): "OBJT", u32 version = 1, then the standard object section of a PC world. When the file is
// mounted next to the world the server reads its objects from it instead of the world. The order of existing
// objects never changes (blind data such as the nav mesh refer to objects by index); "append" operations add
// objects AFTER the last existing one.
//
// The edit list (spec/restore_spec_combined.json) holds object names, indices, the weapon records and model
// paths of the mod, and the names of the properties to take from the console worlds. Every placement and every
// other value the consoles chose is read at build time from the player's own console copy: SM15 operations
// from the Xbox 360 world, Watson operations from the PS3 world.
package objects00p

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"fearinstaller/internal/wld"
)

//go:embed spec/restore_spec_combined.json
var embeddedSpec []byte

// Op is one operation of the spec.
type Op struct {
	Op          string                        `json:"op"`
	Index       int                           `json:"index"`
	Class       string                        `json:"class"`
	Name        string                        `json:"name"`
	Weapon      string                        `json:"weapon"`
	NeedsAppend *string                       `json:"needs_append"`
	Edits       map[string][2]json.RawMessage `json:"edits"` // property -> [stock PC value, new value]
	BaseIndex   int                           `json:"base_index"`
	BaseName    string                        `json:"base_name"`
	NewName     string                        `json:"new_name"`
	Set         map[string]json.RawMessage    `json:"set"` // property -> new value
	Console     *ConsoleProps                 `json:"console"`
}

// ConsoleProps lists the properties an operation copies from the same object in the console world.
type ConsoleProps struct {
	Props []string `json:"props"`
	// Name is the console object's name when it differs from the PC one (edit: Name, append: NewName).
	Name string `json:"name,omitempty"`
	// World is the console world file's base name when it differs from the default (see ConsoleWorldFiles).
	World string `json:"world,omitempty"`
}

// WorldSpec is the list of operations for one world.
type WorldSpec struct {
	Ops []Op `json:"ops"`
}

// Spec maps a world name ("Admin") to its operations.
type Spec map[string]WorldSpec

// EmbeddedSpec returns the spec compiled into the program.
func EmbeddedSpec() (Spec, error) { return ParseSpec(embeddedSpec) }

// ParseSpec parses a spec JSON document.
func ParseSpec(b []byte) (Spec, error) {
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return s, nil
}

// Worlds lists the world names of the spec in sorted order.
func (s Spec) Worlds() []string {
	var w []string
	for k := range s {
		w = append(w, k)
	}
	sort.Strings(w)
	return w
}

// The console copies the operations read.
const (
	X360 = "x360" // Xbox 360 disc root (holds Game\)
	PS3  = "ps3"  // PS3 PS3_GAME\USRDIR\game folder
)

// ConsoleOf returns the console copy whose worlds hold the given weapon's placements.
func ConsoleOf(weapon string) (string, error) {
	switch weapon {
	case "sm15":
		return X360, nil
	case "watson":
		return PS3, nil
	}
	return "", fmt.Errorf("unknown weapon %q", weapon)
}

// ConsoleWorldFiles returns the paths, relative to the console copy's root, of the server world (.wldsrvr,
// the objects) and the world (.wld, whose header holds the world offset) that an operation of the PC world
// reads. The Xbox 360 names its worlds like the PC; the PS3 uses lower case and splits some worlds in two,
// so an operation may name its console world file explicitly.
func ConsoleWorldFiles(console, pcWorld string, op Op) (srvr, world []string, err error) {
	base := pcWorld
	if op.Console != nil && op.Console.World != "" {
		base = op.Console.World
	}
	switch console {
	case X360:
		return []string{"Game", "Worlds", "Release", base + ".wldsrvr"}, []string{"Game", "Worlds", "Release", base + ".wld"}, nil
	case PS3:
		base = strings.ToLower(base)
		return []string{"worlds", "release", base + ".wldsrvr"}, []string{"worlds", "release", base + ".wld"}, nil
	}
	return nil, nil, fmt.Errorf("unknown console %q", console)
}

// ConsoleReader reads a file of a console copy: console is X360 or PS3, rel the path below the copy's root.
// max > 0 allows the reader to return only the first max bytes (only the header of a .wld is needed).
type ConsoleReader func(console string, rel []string, max int) ([]byte, error)

// Options select a subset of the spec and give access to the console copies.
type Options struct {
	// NoAppend drops every append operation and the edits that need one (a pure property-edit variant).
	NoAppend bool
	// Weapons keeps only operations of these weapons ("sm15", "watson"); empty = all.
	Weapons []string
	// Console reads the console worlds. It is needed when a selected operation copies console properties.
	Console ConsoleReader
}

// Result describes one built file.
type Result struct {
	Data       []byte
	ObjectsIn  int
	ObjectsOut int
	OpsApplied int
	// Complete is true when every operation of the world was applied (no weapon or append filter removed
	// one): only then is the file the mod's confirmed one.
	Complete bool
}

func keep(op Op, o Options) bool {
	if len(o.Weapons) == 0 {
		return true
	}
	for _, w := range o.Weapons {
		if w == op.Weapon {
			return true
		}
	}
	return false
}

// conv converts a JSON value into the in-memory property of type t. old is the current property.
func conv(t uint32, raw json.RawMessage, old wld.Prop) (wld.Prop, error) {
	p := old
	p.N = append([]uint32(nil), old.N...)
	switch t {
	case wld.TString, wld.TCommand, wld.TStringID:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return p, fmt.Errorf("expected a string: %w", err)
		}
		p.S = s
	case wld.TBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return p, fmt.Errorf("expected a bool: %w", err)
		}
		p.U = 0
		if b {
			p.U = 1
		}
	case wld.TFloat:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return p, err
		}
		p.U = math.Float32bits(float32(f))
	case wld.TVector, wld.TColor, wld.TRot:
		var fs []float64
		if err := json.Unmarshal(raw, &fs); err != nil {
			return p, err
		}
		p.N = make([]uint32, len(fs))
		for i, f := range fs {
			p.N[i] = math.Float32bits(float32(f))
		}
	default:
		// ("raw", type, value) or a plain integer
		var arr []json.RawMessage
		var num json.RawMessage
		if err := json.Unmarshal(raw, &arr); err == nil && len(arr) == 3 {
			num = arr[2]
		} else {
			num = raw
		}
		var n json.Number
		if err := json.Unmarshal(num, &n); err != nil {
			return p, fmt.Errorf("unsupported value for property type %d", t)
		}
		u, err := n.Int64()
		if err != nil {
			return p, err
		}
		p.U = uint32(u)
	}
	return p, nil
}

// sameValue reports whether the JSON value equals the current property (the guard against a different world build).
func sameValue(t uint32, raw json.RawMessage, cur wld.Prop) bool {
	n, err := conv(t, raw, wld.Prop{Type: t, N: make([]uint32, len(cur.N))})
	if err != nil {
		return false
	}
	switch t {
	case wld.TString, wld.TCommand, wld.TStringID:
		return n.S == cur.S
	case wld.TFloat:
		return float64(math.Float32frombits(cur.U)) == jsonFloat(raw)
	case wld.TBool:
		return n.U == cur.U
	case wld.TVector, wld.TColor, wld.TRot:
		var fs []float64
		_ = json.Unmarshal(raw, &fs)
		if len(fs) != len(cur.N) {
			return false
		}
		for i, f := range fs {
			if float64(math.Float32frombits(cur.N[i])) != f {
				return false
			}
		}
		return true
	}
	return n.U == cur.U
}

func jsonFloat(raw json.RawMessage) float64 {
	var f float64
	_ = json.Unmarshal(raw, &f)
	return f
}

// worldOffset reads the world offset (3 floats) from a world header. Positions are stored relative to it:
// editor position = stored position + offset. The console .wld keeps it big endian at 0x20, the PC .World00p
// little endian at 0x2C.
func worldOffset(d []byte, at int, bo binary.ByteOrder) ([3]float64, error) {
	var off [3]float64
	if len(d) < at+12 {
		return off, errors.New("world header too short")
	}
	for i := range off {
		off[i] = float64(math.Float32frombits(bo.Uint32(d[at+4*i:])))
	}
	return off, nil
}

// consoleWorld is one parsed console world.
type consoleWorld struct {
	objs   []wld.Object
	offset [3]float64
}

// roundTenth rounds to one decimal place, correctly rounded from the exact binary value (ties to even).
func roundTenth(x float64) float64 {
	r, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 1, 64), 64)
	return r
}

// fromConsole returns the console value of property name for an object of the PC world. pcProp is the PC
// property it replaces (its type must match). A position is moved from the console world's frame to the PC
// world's (same editor position: console stored + console offset - PC offset, in float64, then stored as
// float32). Appended objects are placed at that position rounded to 0.1 unit, the precision the mod's
// placements were authored with; edits of existing pickups keep the exact console position. Rotations
// (quaternions) and every other value are copied bit for bit: both platforms use the same convention.
func fromConsole(co *wld.Object, cw *consoleWorld, pcOffset [3]float64, pcProp wld.Prop, round bool) (wld.Prop, error) {
	j := co.PropIndex(pcProp.Name)
	if j < 0 {
		return pcProp, fmt.Errorf("the console object has no property %s", pcProp.Name)
	}
	cp := co.Props[j]
	if cp.Type != pcProp.Type {
		return pcProp, fmt.Errorf("property %s has type %d on the console, %d on the PC", pcProp.Name, cp.Type, pcProp.Type)
	}
	p := pcProp
	p.S, p.U, p.N = cp.S, cp.U, append([]uint32(nil), cp.N...)
	if pcProp.Name == "Pos" && pcProp.Type == wld.TVector {
		for i := range p.N {
			v := float64(math.Float32frombits(cp.N[i])) + cw.offset[i] - pcOffset[i]
			if round {
				v = roundTenth(v)
			}
			p.N[i] = math.Float32bits(float32(v))
		}
	}
	return p, nil
}

// consoleObject finds the single object of the given class and name in the console world.
func consoleObject(cw *consoleWorld, class, name string) (*wld.Object, error) {
	var hit *wld.Object
	for i := range cw.objs {
		if cw.objs[i].Class == class && cw.objs[i].Name() == name {
			if hit != nil {
				return nil, fmt.Errorf("the console world has more than one %s %q", class, name)
			}
			hit = &cw.objs[i]
		}
	}
	if hit == nil {
		return nil, fmt.Errorf("the console world has no %s %q", class, name)
	}
	return hit, nil
}

// consoleLoader reads and caches the console worlds of one Build.
type consoleLoader struct {
	pcWorld string
	read    ConsoleReader
	cache   map[string]*consoleWorld
}

func (l *consoleLoader) world(op Op) (*consoleWorld, error) {
	console, err := ConsoleOf(op.Weapon)
	if err != nil {
		return nil, err
	}
	srvr, hdr, err := ConsoleWorldFiles(console, l.pcWorld, op)
	if err != nil {
		return nil, err
	}
	key := console + "/" + strings.Join(srvr, "/")
	if cw := l.cache[key]; cw != nil {
		return cw, nil
	}
	if l.read == nil {
		return nil, fmt.Errorf("the %s copy is needed (%s)", console, strings.Join(srvr, "/"))
	}
	sb, err := l.read(console, srvr, 0)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", console, strings.Join(srvr, "/"), err)
	}
	_, objs, err := wld.ParseConsoleServer(sb)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", console, strings.Join(srvr, "/"), err)
	}
	hb, err := l.read(console, hdr, 0x2C)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", console, strings.Join(hdr, "/"), err)
	}
	off, err := worldOffset(hb, 0x20, binary.BigEndian)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", console, strings.Join(hdr, "/"), err)
	}
	cw := &consoleWorld{objs: objs, offset: off}
	l.cache[key] = cw
	return cw, nil
}

// applyConsole copies the operation's console properties onto ob (the PC object being edited or appended).
func (l *consoleLoader) applyConsole(op Op, ob *wld.Object, name string, pcOffset [3]float64) error {
	if op.Console == nil || len(op.Console.Props) == 0 {
		return nil
	}
	cw, err := l.world(op)
	if err != nil {
		return err
	}
	cname := name
	if op.Console.Name != "" {
		cname = op.Console.Name
	}
	co, err := consoleObject(cw, op.Class, cname)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	for _, prop := range op.Console.Props {
		j := ob.PropIndex(prop)
		if j < 0 {
			return fmt.Errorf("%s: no property %s", name, prop)
		}
		np, err := fromConsole(co, cw, pcOffset, ob.Props[j], op.Op == "append")
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		ob.Props[j] = np
	}
	return nil
}

// Build applies the operations of the PC world pcWorld ("Admin") to its stock world (World00p bytes) and
// returns the Objects00p file. Console properties are read through opt.Console.
func Build(pcWorld string, world []byte, ops []Op, opt Options) (*Result, error) {
	objs, _, _, err := wld.ParsePCWorld(world)
	if err != nil {
		return nil, err
	}
	pcOffset, err := worldOffset(world, 0x2C, binary.LittleEndian)
	if err != nil {
		return nil, err
	}
	cl := &consoleLoader{pcWorld: pcWorld, read: opt.Console, cache: map[string]*consoleWorld{}}
	n0 := len(objs)
	var sel []Op
	for _, o := range ops {
		if keep(o, opt) {
			sel = append(sel, o)
		}
	}
	if opt.NoAppend {
		dropped := map[string]bool{}
		for _, o := range sel {
			if o.Op == "append" {
				dropped[o.NewName] = true
			}
		}
		var s2 []Op
		for _, o := range sel {
			if o.Op == "edit" && !(o.NeedsAppend != nil && dropped[*o.NeedsAppend]) {
				s2 = append(s2, o)
			}
		}
		sel = s2
	}
	for _, o := range sel {
		if o.Op != "edit" {
			continue
		}
		if o.Index < 0 || o.Index >= len(objs) {
			return nil, fmt.Errorf("edit index %d outside the world (%d objects)", o.Index, len(objs))
		}
		ob := &objs[o.Index]
		if ob.Class != o.Class || ob.Name() != o.Name {
			return nil, fmt.Errorf("object mismatch at %d: world has %s %q, spec expects %s %q", o.Index, ob.Class, ob.Name(), o.Class, o.Name)
		}
		props := make([]string, 0, len(o.Edits))
		for k := range o.Edits {
			props = append(props, k)
		}
		sort.Strings(props)
		for _, prop := range props {
			ed := o.Edits[prop]
			j := ob.PropIndex(prop)
			if j < 0 {
				return nil, fmt.Errorf("%s: no property %s", o.Name, prop)
			}
			cur := ob.Props[j]
			if !sameValue(cur.Type, ed[0], cur) {
				return nil, fmt.Errorf("old value mismatch: %s.%s is not %s (a different world build?)", o.Name, prop, string(ed[0]))
			}
			np, err := conv(cur.Type, ed[1], cur)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", o.Name, prop, err)
			}
			ob.Props[j] = np
		}
		if err := cl.applyConsole(o, ob, o.Name, pcOffset); err != nil {
			return nil, err
		}
	}
	names := map[string]bool{}
	for i := range objs {
		names[objs[i].Name()] = true
	}
	for _, o := range sel {
		if o.Op != "append" {
			continue
		}
		if o.BaseIndex < 0 || o.BaseIndex >= n0 {
			return nil, fmt.Errorf("append base index %d outside the world", o.BaseIndex)
		}
		base := objs[o.BaseIndex]
		if base.Class != o.Class || base.Name() != o.BaseName {
			return nil, fmt.Errorf("base mismatch at %d: world has %q, spec expects %q", o.BaseIndex, base.Name(), o.BaseName)
		}
		if names[o.NewName] {
			return nil, fmt.Errorf("name already exists: %s", o.NewName)
		}
		names[o.NewName] = true
		nw := base.Clone()
		keys := make([]string, 0, len(o.Set))
		for k := range o.Set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			j := nw.PropIndex(k)
			if j < 0 {
				return nil, fmt.Errorf("append %s: base object lacks property %s", o.NewName, k)
			}
			np, err := conv(nw.Props[j].Type, o.Set[k], nw.Props[j])
			if err != nil {
				return nil, fmt.Errorf("append %s.%s: %w", o.NewName, k, err)
			}
			nw.Props[j] = np
		}
		if err := cl.applyConsole(o, &nw, o.NewName, pcOffset); err != nil {
			return nil, fmt.Errorf("append %w", err)
		}
		objs = append(objs, nw)
	}
	var buf bytes.Buffer
	buf.WriteString("OBJT")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(1))
	buf.Write(wld.SerializeObjects(objs))
	return &Result{Data: buf.Bytes(), ObjectsIn: n0, ObjectsOut: len(objs), OpsApplied: len(sel), Complete: len(sel) == len(ops)}, nil
}
