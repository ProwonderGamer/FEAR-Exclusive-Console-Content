package gadb

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"fearinstaller/internal/testref"
)

func roundTrip(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := Read(data)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	out, err := Write(db, db.BigEnd)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("%s: round trip differs (%d vs %d bytes)", path, len(out), len(data))
	}
	// endian flip
	other, err := Write(db, !db.BigEnd)
	if err != nil {
		t.Fatal(err)
	}
	db2, err := Read(other)
	if err != nil {
		t.Fatal(err)
	}
	back, _ := Write(db2, db.BigEnd)
	if !bytes.Equal(back, data) {
		t.Fatalf("%s: endian flip round trip differs", path)
	}
	// symbolic links round trip
	if err := Symbolize(db); err != nil {
		t.Fatal(err)
	}
	if err := EncodeLinks(db); err != nil {
		t.Fatal(err)
	}
	again, _ := Write(db, db.BigEnd)
	if !bytes.Equal(again, data) {
		t.Fatalf("%s: symbolize/encode round trip differs", path)
	}
	t.Logf("%s: %d bytes byte-exact", filepath.Base(path), len(data))
}

func TestRoundTripConsole(t *testing.T) {
	roundTrip(t, testref.File(t, "FEAR_REF_PS3", "database", "loki.gamedb"))
	roundTrip(t, testref.File(t, "FEAR_REF_PS3", "database_anim", "anim.gamedb"))
	roundTrip(t, testref.File(t, "FEAR_REF_PS3", "profiledatabase", "defaults.gamedb"))
}

func TestRoundTripX360(t *testing.T) {
	roundTrip(t, testref.File(t, "FEAR_REF_X360", "Game", "database", "Loki.gamedb"))
	roundTrip(t, testref.File(t, "FEAR_REF_X360", "Game", "database_anim", "Anim.gamedb"))
}

func TestSynthetic(t *testing.T) {
	db := &Database{Version: 3, Cats: []*Category{{Name: "A/B", Records: []*Record{{Name: "r1", Attrs: []*Attr{
		{Name: "f", Type: TFloat, Values: []Value{{U: 0x3f800000}}},
		{Name: "s", Type: TString, Flags: 1, Values: []Value{Str("x\\y"), {Null: true}}},
		{Name: "w", Type: TWStr, Values: []Value{{W: []uint16{0x41, 0xD83D}}}},
		{Name: "v", Type: TVec3, Values: []Value{{V: []uint32{1, 2, 3}}}},
		{Name: "l", Type: TLink, Values: []Value{{U: NoLink}, {U: 0}}},
	}}}}}}
	for _, be := range []bool{false, true} {
		b, err := Write(db, be)
		if err != nil {
			t.Fatal(err)
		}
		d2, err := Read(b)
		if err != nil {
			t.Fatal(err)
		}
		b2, _ := Write(d2, be)
		if !bytes.Equal(b, b2) {
			t.Fatal("synthetic round trip differs")
		}
	}
	// malformed input must not panic
	good, _ := Write(db, false)
	for i := 0; i < len(good); i++ {
		c := append([]byte(nil), good...)
		c[i] ^= 0xFF
		_, _ = Read(c)
		_, _ = Read(good[:i])
	}
}
