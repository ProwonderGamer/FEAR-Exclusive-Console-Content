package strdb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/pcfs"
	"fearinstaller/internal/testref"
	"fearinstaller/internal/wld"
)

// Reference outputs (tested in game): the SFOD-D string database and the Holiday string database, which is built
// on top of it.
const (
	goldenSFODD   = "e25afa370b02720e561bb5c591ed99f4e26530df5f5f861d42917ffac8d696b4"
	goldenHoliday = "a8f1c558a4d31a5aa56d266ec0939fae656bd090e4ebaadfdb1274f0df0d173f"
	// ConsoleText.Strdb00p of the confirmed mod, the only text file the release writes.
	goldenConsoleText = "6f81f17098775ee28e1a4c61f4e7540ce4f2aefa09b93f39fbcdf02d0102c74f"
)

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func stockPCStrdb(t *testing.T) []byte {
	t.Helper()
	dir := testref.Dir(t, "FEAR_REF_PCGAME")
	fs, errs := pcfs.Open(dir)
	if len(errs) > 0 {
		t.Logf("archive warnings: %v", errs)
	}
	defer fs.Close()
	b, err := fs.Read(`StringDatabase\FEAR.Strdb00p`)
	if err != nil {
		t.Skipf("stock string database not available: %v", err)
	}
	return b
}

func ps3English(t *testing.T) []byte {
	t.Helper()
	p := testref.File(t, "FEAR_REF_PS3_EXTRACT", "database_string", "English", "Loki.strdb")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundTripShippedFiles(t *testing.T) {
	for _, b := range [][]byte{stockPCStrdb(t), ps3English(t)} {
		db, err := Parse(b)
		if err != nil {
			t.Fatal(err)
		}
		out, err := db.Bytes(db.BigEndian, db.Version)
		if err != nil || !bytes.Equal(out, b) {
			t.Fatalf("round trip differs (%v)", err)
		}
		if !db.IsSorted() {
			t.Error("not sorted")
		}
		// byte order conversion PS3 -> PC -> PS3
		pc, _ := db.Bytes(false, 2)
		db2, err := Parse(pc)
		if err != nil {
			t.Fatal(err)
		}
		back, _ := db2.Bytes(db.BigEndian, db.Version)
		if !bytes.Equal(back, b) {
			t.Error("endian flip differs")
		}
	}
}

func TestGoldenSFODDAndHoliday(t *testing.T) {
	pcBytes := stockPCStrdb(t)
	ps3Bytes := ps3English(t)
	pc, err := Parse(pcBytes)
	if err != nil {
		t.Fatal(err)
	}
	ps3, err := Parse(ps3Bytes)
	if err != nil {
		t.Fatal(err)
	}
	db, added, err := BuildSFODD(pc, ps3)
	if err != nil {
		t.Fatal(err)
	}
	out, err := db.Bytes(false, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := sum(out); got != goldenSFODD {
		t.Fatalf("SFOD-D strdb sha256 %s, reference %s", got, goldenSFODD)
	}
	t.Logf("SFOD-D FEAR.Strdb00p: %d keys added, %d bytes: byte-exact", added, len(out))

	// Holiday on top of the SFOD-D database
	x := testref.File(t, "FEAR_REF_X360", "Game", "Worlds", "Release", "Holiday.wldsrvr")
	wb, err := os.ReadFile(x)
	if err != nil {
		t.Fatal(err)
	}
	_, objs, err := wld.ParseConsoleServer(wb)
	if err != nil {
		t.Fatal(err)
	}
	ids := WorldDialogueIDs(objs)
	hdb, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	hadded, err := BuildHoliday(hdb, ps3, ids)
	if err != nil {
		t.Fatal(err)
	}
	hout, err := hdb.Bytes(false, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := sum(hout); got != goldenHoliday {
		t.Fatalf("Holiday strdb sha256 %s, reference %s", got, goldenHoliday)
	}
	t.Logf("Holiday FEAR.Strdb00p: %d dialogue ids, %d keys added, %d bytes: byte-exact", len(ids), hadded, len(hout))

	// ConsoleText = the keys the merged list adds to the stock one + the Xbox 360 Instant Action menu texts.
	// Reference: the confirmed mod's file (in-game confirmed).
	stock, err := Parse(pcBytes)
	if err != nil {
		t.Fatal(err)
	}
	con, err := Parse(x360English(t))
	if err != nil {
		t.Fatal(err)
	}
	ct, err := BuildConsoleText(stock, hdb, con)
	if err != nil {
		t.Fatal(err)
	}
	cout, err := ct.Bytes(false, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := sum(cout); got != goldenConsoleText {
		t.Fatalf("ConsoleText sha256 %s, reference %s", got, goldenConsoleText)
	}
	for _, k := range ct.Keys {
		if stock.IndexOf(k) >= 0 {
			t.Fatalf("ConsoleText repeats the stock key %s", k)
		}
	}
	t.Logf("ConsoleText.Strdb00p: %d keys, %d bytes: byte-exact", len(ct.Keys), len(cout))

	// The PS3 list gives the same Instant Action texts (an install without an Xbox 360 copy).
	ct2, err := BuildConsoleText(stock, hdb, ps3)
	if err != nil {
		t.Fatal(err)
	}
	if c2, _ := ct2.Bytes(false, 2); sum(c2) != goldenConsoleText {
		t.Fatal("ConsoleText built with the PS3 Instant Action texts differs")
	}
}

// x360English reads database_string\English\Loki.strdb from the Xbox 360 uberCatalog.cat.
func x360English(t *testing.T) []byte {
	t.Helper()
	c, err := catalog.Open(testref.File(t, "FEAR_REF_X360", "Game", "uberCatalog.cat"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	b, err := c.ReadByName(`database_string\English\Loki.strdb`)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestConsoleTextNoBonus(t *testing.T) {
	stock := &DB{Version: 2}
	stock.Set("IDS_OK", Units("OK"), true, "", false, false)
	con := &DB{Version: 2}
	for _, k := range InstantActionKeys {
		con.Set(k, Units(k+" text"), true, `Snd\voice\x.wav`, true, false)
	}
	// Xbox-only install: no bonus-mission list, only the Instant Action texts (no voice paths).
	ct, err := BuildConsoleText(stock, nil, con)
	if err != nil {
		t.Fatal(err)
	}
	if len(ct.Keys) != len(InstantActionKeys) || len(ct.Voices) != 0 || !ct.IsSorted() {
		t.Fatalf("%d keys, %d voices", len(ct.Keys), len(ct.Voices))
	}
	// a console list missing one of the texts is an error, never a silent gap
	short := &DB{Version: 2}
	short.Set("IDS_INSTANTACTION", Units("x"), true, "", false, false)
	if _, err := BuildConsoleText(stock, nil, short); err == nil {
		t.Fatal("missing Instant Action text accepted")
	}
	// an Instant Action key that the stock list already has would shadow nothing and must stop the build
	stock.Set("IDS_INSTANTACTION", Units("y"), true, "", false, false)
	if _, err := BuildConsoleText(stock, nil, con); err == nil {
		t.Fatal("stock key accepted")
	}
}
