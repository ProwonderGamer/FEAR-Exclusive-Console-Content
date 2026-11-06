package consolefont

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"fearinstaller/internal/catalog"
	"fearinstaller/internal/testref"
)

// Interface\IA\zSFODD_ConsoleFonts.bin of the confirmed mod, built from the X360.
const goldenX360 = "8119a3ffc2550692543cfd5e7db3db8df1791db3ee38031f1faf84ef7a5b2b35"

// The PS3 fallback differs from the reference by design (602 NeuropoX_36 alpha texels, see the package comment).
// This is the hash the Python recipe (PS3 branch) gives, checked here so the
// port stays faithful. Never tested in game.
const goldenPS3 = "d85033b9b5bf42c5460e26d24c1e24b4b46712839715fe946bc8965fc677b8c6"

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func build(t *testing.T, c Console, cat string) []byte {
	t.Helper()
	k, err := catalog.Open(cat)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	var src [2][]byte
	for i := range src {
		if src[i], err = k.ReadByName(c.CatalogPath(i)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := Build(c, src[0], src[1])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGoldenX360(t *testing.T) {
	out := build(t, X360, testref.File(t, "FEAR_REF_X360", "Game", "uberCatalog.cat"))
	if got := sum(out); got != goldenX360 {
		t.Errorf("sha256 %s, want %s", got, goldenX360)
	}
}

func TestPS3(t *testing.T) {
	out := build(t, PS3, testref.File(t, "FEAR_REF_PS3", "ubercatalog.cat"))
	got := sum(out)
	t.Logf("PS3 build: %d bytes, sha256 %s", len(out), got)
	if goldenPS3 != "" && got != goldenPS3 {
		t.Errorf("sha256 %s, want %s", got, goldenPS3)
	}
}
