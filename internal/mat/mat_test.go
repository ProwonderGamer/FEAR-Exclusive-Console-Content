package mat

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/testref"
)

// Golden SHA-256 of the .Mat00 files of the confirmed mod. src is the
// catalog entry without its ".mat"; the PC file is the same path with ".Mat00".
var golden = []struct {
	src, sha string
}{
	{`guns\Materials\SM15_Machine_Pistol`, "b36c0aaa648a6e3b90b7391f6472f7cb1d566f68d732db3d6983e4d8c39be37b"},
	{`guns\Materials\Watson_SAS-12`, "663b12d53a38721e077c274e7e92b778c2bfab5b1efdb10d86c1fb4d221cd22c"},
	{`Materials\Engineering\Black`, "f4d5373b19cc502aa399c3e4a07aa5b6cfb7b5e9ab3d81ac17847c74ff2aa755"},
	{`Prefabs\Test\ElevatorFloorT`, "3fd216186a26d19cead6bde5d02ec7adeebd71100ecc0ee7e93196c947d84b25"},
	{`Prefabs\Test\ElevatorRailT`, "f69055d42dfb17b969ce6c9e78eb370a3d6bf32fa69e3e02b5264c2da3e81079"},
	{`Prefabs\Test\ElevatorWallT`, "74af5a56ea9e3dc50a100a412d35e1d64016695e2711ea1e9fcddaff6efe8905"},
	{`Prefabs\Test\ElevatortopT`, "47a9fcbdfa67eb9220cd7856c0e6790d609900539bca569cfef9ea7b295d537b"},
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func check(t *testing.T, c *catalog.Catalog, label string) {
	t.Helper()
	for _, g := range golden {
		src, err := c.ReadByName(g.src + ".mat")
		if err != nil {
			t.Errorf("%s: %v", label, err)
			continue
		}
		out, err := Convert(src)
		if err != nil {
			t.Errorf("%s %s: %v", label, g.src, err)
			continue
		}
		if got := sum(out); got != g.sha {
			t.Errorf("%s %s: sha256 %s, want %s", label, g.src, got, g.sha)
		}
	}
}

func open(t *testing.T, p string) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestGoldenPS3(t *testing.T) {
	check(t, open(t, testref.File(t, "FEAR_REF_PS3", "ubercatalog.cat")), "PS3 ubercatalog")
}

func TestGoldenX360(t *testing.T) {
	check(t, open(t, testref.File(t, "FEAR_REF_X360", "Game", "uberCatalog.cat")), "X360 uberCatalog")
}

// The four elevator prefab materials are also in the PS3 SFOD-D world catalog (lower-case names there).
func TestGoldenPS3Matcat(t *testing.T) {
	c := open(t, testref.File(t, "FEAR_REF_PS3", "worlds", "release", "sfod_d.matcat"))
	for _, g := range golden[3:] {
		src, err := c.ReadByName(g.src + ".mat")
		if err != nil {
			t.Error(err)
			continue
		}
		out, err := Convert(src)
		if err != nil {
			t.Errorf("%s: %v", g.src, err)
			continue
		}
		if got := sum(out); got != g.sha {
			t.Errorf("sfod_d.matcat %s: sha256 %s, want %s", g.src, got, g.sha)
		}
	}
}

func TestErrors(t *testing.T) {
	for _, b := range [][]byte{nil, []byte("LTMI"), []byte("IMTL\x00\x00\x00\x01\x00\x05ab"),
		[]byte("IMTL\x00\x00\x00\x01\x00\x00\x00\x00\x00\x01\x00\x00\x00\x09\x00\x00")} {
		if _, err := Convert(b); err == nil {
			t.Errorf("Convert(%q) did not fail", b)
		}
	}
}
