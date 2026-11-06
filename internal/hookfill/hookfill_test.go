package hookfill

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/gadb"
	"fearinstaller/internal/install"
	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/strdb"
	"fearinstaller/internal/testref"
)

// The shipped template parses, and every game value in it is already zero (it carries none).
func TestTemplate(t *testing.T) {
	rows, err := parseTable(install.HookTemplate)
	if err != nil {
		t.Fatal(err)
	}
	num := 0
	for _, r := range rows {
		if r.numeric() {
			num++
		}
	}
	if num != Rows-4 {
		t.Errorf("%d numeric rows, want %d (4 rows hold names)", num, Rows-4)
	}
	b, err := Blank(install.HookTemplate)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, install.HookTemplate) {
		t.Error("the template still holds game values")
	}
}

// Golden: filled in from the original game files, the template becomes the hook that was tested in game.
func TestFillMatchesTestedHook(t *testing.T) {
	x360 := testref.Dir(t, "FEAR_REF_X360")
	var in Inputs
	var err error
	if in.X360GameDB, err = os.ReadFile(testref.File(t, "FEAR_REF_X360", "Game", "database", "Loki.gamedb")); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(testref.File(t, "FEAR_REF_X360", "Game", "uberCatalog.cat"))
	if err != nil {
		t.Fatalf("%s: %v", x360, err)
	}
	in.X360Strings, err = c.ReadByName(`database_string\English\Loki.strdb`)
	c.Close()
	if err != nil {
		t.Fatal(err)
	}
	fs, _ := pcfs.Open(testref.Dir(t, "FEAR_REF_PCGAME"))
	defer fs.Close()
	if in.PCFEARL, err = fs.Read(`DatabaseLocalized\FEARL.Gamdb00p`); err != nil {
		t.Skipf("stock FEARL.Gamdb00p: %v", err)
	}
	if in.PCStrings, err = fs.Read(`StringDatabase\FEAR.Strdb00p`); err != nil {
		t.Skipf("stock FEAR.Strdb00p: %v", err)
	}
	in.PCModules = pcModules(t, fs)
	got, err := Fill(install.HookTemplate, in)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(got)
	if h := hex.EncodeToString(s[:]); h != install.HookSHA256 {
		t.Fatalf("filled hook is %s, want %s", h, install.HookSHA256)
	}
}

// Databases or string lists without what the hook needs are refused, and so is a file that is not the hook.
func TestFillRefusesWrongInputs(t *testing.T) {
	empty, err := gadb.Write(&gadb.Database{Version: 3}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Fill(install.HookTemplate, Inputs{X360GameDB: empty, PCFEARL: empty}); err == nil {
		t.Error("databases without Interface/HUD were accepted")
	}
	one := &gadb.Database{Version: 3, Cats: []*gadb.Category{{Name: category, Records: []*gadb.Record{{Name: "HUDHealth"}}}}}
	partial, err := gadb.Write(one, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Fill(install.HookTemplate, Inputs{X360GameDB: partial, PCFEARL: partial}); err == nil {
		t.Error("databases missing the HUD fields were accepted")
	}
	if _, err := Fill([]byte("MZ not a hook"), Inputs{X360GameDB: partial, PCFEARL: partial}); err == nil {
		t.Error("a file that is not the hook was accepted")
	}
}

// fillText takes each text from its string list, cut and checked as the slot says.
func TestFillText(t *testing.T) {
	list := func(kv ...string) []byte {
		db := &strdb.DB{Version: 2}
		for i := 0; i < len(kv); i += 2 {
			if _, err := db.Set(kv[i], strdb.Units(kv[i+1]), true, "", false, false); err != nil {
				t.Fatal(err)
			}
		}
		b, err := db.Bytes(false, 2)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	pc := list("Training_Boards", "Break or shoot the loose boards to clear a route.")
	x := list("Objective_Header", "CURRENT OBJECTIVE:\n%1!s!",
		"Training_Boards", "Pull |Fire| to shoot the loose boards and clear a route.",
		"INSTANT_ACTION_RESULT_SUCCESSFUL", "Mission Completed!",
		"IDS_TITLE_INSTANTACTION_SUMMARY", "SUMMARY",
		"INSTANT_ACTION_RESULT_BOOSTER", "You found the Booster!")
	out := append([]byte(nil), install.HookTemplate...)
	if err := fillText(out, pc, x); err != nil {
		t.Fatal(err)
	}
	for _, s := range textSlots {
		u := make([]uint16, s.n+1)
		for i := range u {
			u[i] = uint16(out[s.off+2*i]) | uint16(out[s.off+2*i+1])<<8
		}
		if u[s.n] != 0 {
			t.Errorf("slot 0x%x lost its terminator", s.off)
		}
		if s.key == "INSTANT_ACTION_RESULT_SUCCESSFUL" && s.trim != "" && strdb.StringOf(u[:s.n]) != "Mission Completed" {
			t.Errorf("slot 0x%x = %q", s.off, strdb.StringOf(u[:s.n]))
		}
	}
	// A text of another length (another language, another edition) is refused, and so is a filled template.
	if err := fillText(append([]byte(nil), install.HookTemplate...), list("Training_Boards", "Brisez les planches."), x); err == nil {
		t.Error("a PC text of another length was accepted")
	}
	if err := fillText(out, pc, x); err == nil {
		t.Error("a template that is already filled was accepted")
	}
}

// pcModules reads the four game program files of the reference PC game (skips when one is missing).
func pcModules(t *testing.T, fs *pcfs.FS) [4][]byte {
	t.Helper()
	var m [4][]byte
	var err error
	for i, name := range moduleNames {
		if i < 2 {
			m[i], err = fs.Read(name)
		} else {
			m[i], err = os.ReadFile(filepath.Join(testref.Dir(t, "FEAR_REF_PCGAME"), name))
		}
		if err != nil {
			t.Skipf("%s: %v", name, err)
		}
	}
	return m
}

// The template's check tables are where the hook build puts them, every row has its blank bytes, and the
// tables hold the expected number of checked bytes.
func TestTemplateChecks(t *testing.T) {
	tables, err := parseChecks(install.HookTemplate)
	if err != nil {
		t.Fatal(err)
	}
	rows, n := 0, 0
	for _, tab := range tables {
		for _, r := range tab {
			rows++
			n += r.n
			for _, b := range install.HookTemplate[r.at : r.at+r.n] {
				if b != 0 {
					t.Fatalf("check row at 0x%x is not blank", r.at)
				}
			}
		}
	}
	if rows != 353 || n != 2360 {
		t.Errorf("%d check rows with %d bytes, want 353 with 2360", rows, n)
	}
}

// fillChecks copies each row's bytes from its game file at the row's address, and refuses game files that are
// not the expected ones (another load address, too short, or an address outside the file).
func TestFillChecks(t *testing.T) {
	// A minimal 32-bit image: one section at RVA 0x1000, raw data at file offset 0x200, filled with a pattern
	// so each row's bytes are known.
	mk := func(base uint32, size int) []byte {
		b := make([]byte, 0x200+size)
		b[0], b[1] = 'M', 'Z'
		binary.LittleEndian.PutUint32(b[0x3c:], 0x40)
		copy(b[0x40:], "PE\x00\x00")
		binary.LittleEndian.PutUint16(b[0x40+6:], 1)
		binary.LittleEndian.PutUint16(b[0x40+20:], 0xe0)
		binary.LittleEndian.PutUint16(b[0x40+24:], 0x10b)
		binary.LittleEndian.PutUint32(b[0x40+24+28:], base)
		s := 0x40 + 24 + 0xe0
		binary.LittleEndian.PutUint32(b[s+8:], uint32(size))  // virtual size
		binary.LittleEndian.PutUint32(b[s+12:], 0x1000)       // RVA
		binary.LittleEndian.PutUint32(b[s+16:], uint32(size)) // raw size
		binary.LittleEndian.PutUint32(b[s+20:], 0x200)        // raw offset
		for i := 0; i < size; i++ {
			b[0x200+i] = byte(i*7 + i>>8)
		}
		return b
	}
	tables, err := parseChecks(install.HookTemplate)
	if err != nil {
		t.Fatal(err)
	}
	var top uint32
	for _, tab := range tables {
		for _, r := range tab {
			top = max(top, r.addr+uint32(r.n))
		}
	}
	size := int(top - preferredBase - 0x1000)
	var mods [4][]byte
	for i := range mods {
		mods[i] = mk(preferredBase, size)
	}
	out := append([]byte(nil), install.HookTemplate...)
	if err := fillChecks(out, mods); err != nil {
		t.Fatal(err)
	}
	for ti, tab := range tables {
		m := mods[checkTables[ti].module]
		for _, r := range tab {
			want := m[0x200+int(r.addr-preferredBase-0x1000):][:r.n]
			if !bytes.Equal(out[r.at:r.at+r.n], want) {
				t.Fatalf("%s row at 0x%x not copied from its game file", checkTables[ti].name, r.at)
			}
		}
	}
	if b, err := Blank(out); err != nil || !bytes.Equal(b, install.HookTemplate) {
		t.Error("blanking a filled hook does not give back the template")
	}
	if err := fillChecks(out, mods); err == nil {
		t.Error("a template that is already filled was accepted")
	}
	for _, bad := range []struct {
		what string
		m    []byte
	}{
		{"another load address", mk(0x00400000, size)},
		{"a file too short for the checked places", mk(preferredBase, 0x100)},
		{"a file that is not a program", []byte("MZ")},
	} {
		m := mods
		m[gameServer] = bad.m
		if err := fillChecks(append([]byte(nil), install.HookTemplate...), m); err == nil {
			t.Errorf("%s was accepted", bad.what)
		}
	}
}
