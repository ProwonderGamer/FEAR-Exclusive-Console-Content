package objects00p

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/testref"
	"fearinstaller/internal/wld"
)

// SHA-256 of the confirmed mod files: 79 SM15 + 11 Watson placements over 19 stock PC campaign worlds.
// The content package's coverage test reads this table from this file.
var golden = map[string]string{
	"ATC_Roof":        "d1afc2d272833e30b5d623f2a66ed61cd06b4a37e7e8f0800b20e2daec3f8c2e",
	"Admin":           "71b98fccaccc5f9c3fc4b78879296611aeb22409b678c557fd98817e877fcaa2",
	"Alice":           "022ee500db16f7d46b18454f9956b3fb0bdaa53c99fc3d4b1640906b1232de1f",
	"Alma":            "c3d4584911e2da91c5ca65b321534a2953aae51e2321ad4effcd2039e38e9610",
	"Badge":           "2219d2a78378b8ddd5c44c13d490cb70f2517666a279cc7f041c04049f84d1df",
	"Bishop_Evac":     "9d2fb3cc9d48d48258ab7458ae9c5ab97e083d2762e06cb6c60410f6d34e018c",
	"Bishop_Rescue":   "6f9f6f6ecd47527675edc08f71383265abb830ee906482f0e80c8544b8d411aa",
	"Docks":           "70491d402fc47e007e9b287d4288d59f8b2fb96566ac7f771a4b590093e53fcc",
	"Facility_Bypass": "f2038d9bffb765246e3faffeedb135113af3240a51dd96630af8f95b2647b2df",
	"Facility_Upper":  "78f81d6880ac89d3f85c44cfadce2ad50842c57ee76bc3d79620bc7d54457ebd",
	"Factory":         "da89a2e3df779e06a19cdeb3abfd0a833333fb5c57961f4e16e886f9eef7124a",
	"Getting_Out":     "8c2f165a34e47804971f755a92b6eb8454a5a37656098787a9dd0f500d8568d7",
	"Hives":           "570fb05678dda2fa47fc9d42321c2f12b2b4b8349a517d4dadd06d7679d4ace9",
	"Mapes_Elevator":  "8fe607a31849266942ed948b9f04e606dd87a12a6d8cd0e8b47a1d0d14ffb113",
	"Moody":           "5523fd117137fe31313dc8d5b2e2d2ae7132762c811d412a22cff019d59f5a95",
	"Vault":           "20f496fd21d1aa73510ff93b3b43df35b45bdec9e51b07d96a699afb971ba155",
	"WTF_Ambush":      "40f5f9a0574dab38a101ef8405776b0c132c94ced0b68f6abe25ae10b7fd6ece",
	"WTF_Entry":       "8bbccd6522a22c560e751c2fa3eea4e4f48857229fbf2967557eeb0c819dc468",
	"WTF_Exfil":       "ee55a626361f48829bff79d3047210916a9f1dcbe0ff2f6c048dbb462f6f45bb",
}

// refConsole reads the console worlds from the reference copies (FEAR_REF_X360 = disc root holding Game\,
// FEAR_REF_PS3 = USRDIR\game) and skips the test when a file is missing.
func refConsole(t *testing.T) ConsoleReader {
	t.Helper()
	roots := map[string]string{X360: testref.Dir(t, "FEAR_REF_X360"), PS3: testref.Dir(t, "FEAR_REF_PS3")}
	return func(console string, rel []string, max int) ([]byte, error) {
		root, ok := roots[console]
		if !ok {
			return nil, errors.New("unknown console " + console)
		}
		p := filepath.Join(append([]string{root}, rel...)...)
		if _, err := os.Stat(p); err != nil {
			t.Skipf("reference file missing: %v", err)
		}
		return os.ReadFile(p)
	}
}

// Builds all 19 files from the stock PC worlds and the console worlds and compares them with the confirmed files.
func TestGoldenObjects00p(t *testing.T) {
	dir := testref.Dir(t, "FEAR_REF_PCWORLDS")
	console := refConsole(t)
	spec, err := EmbeddedSpec()
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Worlds()) != len(golden) {
		t.Fatalf("spec has %d worlds, golden table %d", len(spec.Worlds()), len(golden))
	}
	for _, w := range spec.Worlds() {
		world, err := os.ReadFile(filepath.Join(dir, w+".World00p"))
		if err != nil {
			t.Skipf("%v", err)
		}
		res, err := Build(w, world, spec[w].Ops, Options{Console: console})
		if err != nil {
			t.Fatalf("%s: %v", w, err)
		}
		if !res.Complete {
			t.Errorf("%s: not every operation was applied", w)
		}
		sum := sha256.Sum256(res.Data)
		if got := hex.EncodeToString(sum[:]); got != golden[w] {
			t.Errorf("%s: sha256 %s, reference %s", w, got, golden[w])
		} else {
			t.Logf("%-16s objects %d -> %d, %d ops, %d bytes: byte-exact", w, res.ObjectsIn, res.ObjectsOut, res.OpsApplied, len(res.Data))
		}
	}
}

// The object section writer must reproduce the stock PC worlds exactly (read -> write round trip).
func TestSectionRoundTrip(t *testing.T) {
	dir := testref.Dir(t, "FEAR_REF_PCWORLDS")
	for _, w := range []string{"Admin", "Aftermath", "Moody", "ATC_Roof", "Mapes_Elevator"} {
		d, err := os.ReadFile(filepath.Join(dir, w+".World00p"))
		if err != nil {
			t.Skip(err)
		}
		objs, start, end, err := wld.ParsePCWorld(d)
		if err != nil {
			t.Fatal(err)
		}
		if got := wld.SerializeObjects(objs); string(got) != string(d[start:end]) {
			t.Errorf("%s: re-serialised object section differs", w)
		}
	}
}

// Weapon filters must be disjoint (no object is touched by both weapons), otherwise a single-weapon
// install would not be a subset of the combined one.
func TestWeaponSubsetsAreDisjoint(t *testing.T) {
	spec, err := EmbeddedSpec()
	if err != nil {
		t.Fatal(err)
	}
	for w, ws := range spec {
		seen := map[int]string{}
		for _, o := range ws.Ops {
			if o.Op != "edit" {
				continue
			}
			if prev, ok := seen[o.Index]; ok && prev != o.Weapon {
				t.Errorf("%s: object %d edited by %s and %s", w, o.Index, prev, o.Weapon)
			}
			seen[o.Index] = o.Weapon
		}
	}
}

// The spec must not carry console placements: positions and rotations are only named, never given as values,
// and every operation's weapon maps to a console copy.
func TestSpecHasNoPlacements(t *testing.T) {
	spec, err := EmbeddedSpec()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for w, ws := range spec {
		for _, o := range ws.Ops {
			if _, err := ConsoleOf(o.Weapon); err != nil {
				t.Errorf("%s %s: %v", w, o.Name, err)
			}
			for _, p := range []string{"Pos", "Rotation"} {
				if _, ok := o.Edits[p]; ok {
					t.Errorf("%s %s: edits holds a %s value", w, o.Name, p)
				}
				if _, ok := o.Set[p]; ok {
					t.Errorf("%s %s: set holds a %s value", w, o.NewName, p)
				}
			}
			if o.Console != nil {
				n += len(o.Console.Props)
			}
		}
	}
	if n == 0 {
		t.Fatal("no operation reads console properties")
	}
}

func TestMismatchIsRejected(t *testing.T) {
	dir := testref.Dir(t, "FEAR_REF_PCWORLDS")
	d, err := os.ReadFile(filepath.Join(dir, "Admin.World00p"))
	if err != nil {
		t.Skip(err)
	}
	spec, _ := EmbeddedSpec()
	ops := append([]Op(nil), spec["Admin"].Ops...)
	for i := range ops {
		if ops[i].Op == "edit" {
			ops[i].Name = "does not exist"
			break
		}
	}
	if _, err := Build("Admin", d, ops, Options{}); err == nil {
		t.Fatal("expected an object mismatch error")
	}
}

// --- synthetic worlds (no reference data needed) ---

func strProp(name, v string) wld.Prop { return wld.Prop{Name: name, Type: wld.TString, S: v} }

func vecProp(name string, typ uint32, v ...float32) wld.Prop {
	p := wld.Prop{Name: name, Type: typ}
	for _, f := range v {
		p.N = append(p.N, math.Float32bits(f))
	}
	return p
}

// pcWorld makes a minimal .World00p: version, 4 section offsets (objects in section 3), box, offset at 0x2C.
func pcWorld(off [3]float32, objs []wld.Object) []byte {
	h := make([]byte, 0x38)
	binary.LittleEndian.PutUint32(h[12:], 0x38)
	for i, f := range off {
		binary.LittleEndian.PutUint32(h[0x2C+4*i:], math.Float32bits(f))
	}
	return append(h, wld.SerializeObjects(objs)...)
}

// consoleServer makes a big endian .wldsrvr by byte-swapping the PC section writer's output field by field.
func consoleServer(objs []wld.Object) []byte {
	le := wld.SerializeObjects(objs)
	be := append([]byte(nil), le...)
	sw32 := func(p int) { binary.BigEndian.PutUint32(be[p:], binary.LittleEndian.Uint32(le[p:])) }
	sw32(0)
	p := 4
	for _, o := range objs {
		binary.BigEndian.PutUint16(be[p:], binary.LittleEndian.Uint16(le[p:]))
		p += 2 + len(o.Class)
		np, bs := int(binary.LittleEndian.Uint32(le[p:])), int(binary.LittleEndian.Uint32(le[p+4:]))
		sw32(p)
		sw32(p + 4)
		p += 8
		blob := p
		p += bs
		for j := 0; j < np; j++ {
			sw32(p)
			sw32(p + 4)
			sw32(p + 8)
			if t := binary.LittleEndian.Uint32(le[p+4:]); t == wld.TVector || t == wld.TColor || t == wld.TRot {
				vo := blob + int(binary.LittleEndian.Uint32(le[p+8:]))
				for k := range o.Props[j].N {
					sw32(vo + 4*k)
				}
			}
			p += 12
		}
	}
	return append([]byte{'S', 'D', 'L', 'W', 0, 0, 0, 1}, be...)
}

func consoleHeader(off [3]float32) []byte {
	h := make([]byte, 0x40)
	for i, f := range off {
		binary.BigEndian.PutUint32(h[0x20+4*i:], math.Float32bits(f))
	}
	return h
}

func TestBuildReadsConsoleProperties(t *testing.T) {
	pc := pcWorld([3]float32{100, 0, -50}, []wld.Object{
		{Class: "WeaponItem", Props: []wld.Prop{strProp("Name", "Rifle"), vecProp("Pos", wld.TVector, 1, 2, 3),
			vecProp("Rotation", wld.TRot, 0, 0, 0, 1), strProp("Filename", "rifle.Model00p")}},
		{Class: "CAI", Props: []wld.Prop{strProp("Name", "Base"), vecProp("Pos", wld.TVector, 0, 0, 0),
			vecProp("Rotation", wld.TRot, 0, 0, 0, 1), strProp("Weapon0", "<none>")}},
	})
	// console world offset (300, 10, -50): console stored + 200 / +10 / 0 = PC stored
	srv := consoleServer([]wld.Object{
		{Class: "CAI", Props: []wld.Prop{strProp("Name", "NewCAI"), vecProp("Pos", wld.TVector, 5.26, -7.04, 9),
			vecProp("Rotation", wld.TRot, 0, -0.7071, 0, 0.7071)}},
		{Class: "WeaponItem", Props: []wld.Prop{strProp("Name", "ConsoleRifle"), vecProp("Pos", wld.TVector, 1.25, 2, 3),
			vecProp("Rotation", wld.TRot, 0.5, 0.5, 0.5, 0.5)}},
	})
	files := map[string][]byte{
		"x360:Game/Worlds/Release/Test.wldsrvr": srv,
		"x360:Game/Worlds/Release/Test.wld":     consoleHeader([3]float32{300, 10, -50}),
	}
	read := func(console string, rel []string, max int) ([]byte, error) {
		b, ok := files[console+":"+strings.Join(rel, "/")]
		if !ok {
			return nil, os.ErrNotExist
		}
		return b, nil
	}
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	ops := []Op{
		{Op: "edit", Index: 0, Class: "WeaponItem", Name: "Rifle", Weapon: "sm15",
			Edits:   map[string][2]json.RawMessage{"Filename": {raw(`"rifle.Model00p"`), raw(`"sm15.Model00p"`)}},
			Console: &ConsoleProps{Props: []string{"Pos", "Rotation"}, Name: "ConsoleRifle"}},
		{Op: "append", Class: "CAI", BaseIndex: 1, BaseName: "Base", NewName: "NewCAI", Weapon: "sm15",
			Set:     map[string]json.RawMessage{"Name": raw(`"NewCAI"`), "Weapon0": raw(`"zSFODD_Machine Pistol"`)},
			Console: &ConsoleProps{Props: []string{"Pos", "Rotation"}}},
	}
	res, err := Build("Test", pc, ops, Options{Console: read})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Complete || res.ObjectsOut != 3 {
		t.Fatalf("complete %v, %d objects", res.Complete, res.ObjectsOut)
	}
	objs, _, err := wld.ParseObjects(res.Data, 8, false)
	if err != nil {
		t.Fatal(err)
	}
	vec := func(o wld.Object, name string) []float32 {
		var v []float32
		for _, b := range o.Props[o.PropIndex(name)].N {
			v = append(v, math.Float32frombits(b))
		}
		return v
	}
	eq := func(what string, got []float32, want ...float32) {
		t.Helper()
		for i := range want {
			if len(got) != len(want) || math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Errorf("%s = %v, want %v", what, got, want)
				return
			}
		}
	}
	// edit: exact console position moved into the PC frame, rotation copied, our model path set
	eq("edit Pos", vec(objs[0], "Pos"), 201.25, 12, 3)
	eq("edit Rotation", vec(objs[0], "Rotation"), 0.5, 0.5, 0.5, 0.5)
	if s := objs[0].Props[objs[0].PropIndex("Filename")].S; s != "sm15.Model00p" {
		t.Errorf("Filename %q", s)
	}
	// append: position rounded to 0.1 unit (float64(float32(5.26)) + 200 = 205.2599..., -7.04 + 10 = 2.96)
	eq("append Pos", vec(objs[2], "Pos"), float32(205.3), float32(3.0), 9)
	eq("append Rotation", vec(objs[2], "Rotation"), 0, -0.7071, 0, 0.7071)
	if objs[2].Name() != "NewCAI" || objs[2].Props[objs[2].PropIndex("Weapon0")].S != "zSFODD_Machine Pistol" {
		t.Errorf("appended object %q", objs[2].Name())
	}

	// a weapon filter that drops an operation gives an incomplete file
	if res, err := Build("Test", pc, ops[:1], Options{Console: read}); err != nil || !res.Complete {
		t.Errorf("subset as full list: %v", err)
	}
	if res, err := Build("Test", pc, ops, Options{Console: read, Weapons: []string{"watson"}}); err != nil || res.Complete {
		t.Errorf("filtered build: complete %v, %v", res != nil && res.Complete, err)
	}
	// without the console copy, or with a missing console object, the build fails
	if _, err := Build("Test", pc, ops, Options{}); err == nil {
		t.Error("expected an error without a console reader")
	}
	bad := append([]Op(nil), ops...)
	bad[0].Console = &ConsoleProps{Props: []string{"Pos"}, Name: "Missing"}
	if _, err := Build("Test", pc, bad, Options{Console: read}); err == nil {
		t.Error("expected an error for a missing console object")
	}
}

func TestConsoleWorldFiles(t *testing.T) {
	srv, hdr, err := ConsoleWorldFiles(PS3, "Factory", Op{Console: &ConsoleProps{World: "factoryb"}})
	if err != nil || strings.Join(srv, "/") != "worlds/release/factoryb.wldsrvr" || strings.Join(hdr, "/") != "worlds/release/factoryb.wld" {
		t.Errorf("PS3: %v %v %v", srv, hdr, err)
	}
	srv, _, err = ConsoleWorldFiles(PS3, "Facility_Upper", Op{})
	if err != nil || strings.Join(srv, "/") != "worlds/release/facility_upper.wldsrvr" {
		t.Errorf("PS3 default: %v %v", srv, err)
	}
	srv, _, err = ConsoleWorldFiles(X360, "ATC_Roof", Op{})
	if err != nil || strings.Join(srv, "/") != "Game/Worlds/Release/ATC_Roof.wldsrvr" {
		t.Errorf("X360: %v %v", srv, err)
	}
}
