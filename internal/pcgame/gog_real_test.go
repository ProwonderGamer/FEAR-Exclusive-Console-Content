package pcgame

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/fixtures"
)

// TestRealGOGFolder runs the detection on a copy of a real GOG "F.E.A.R. Platinum" folder.
// FEAR_REF_GOG must point at a folder with at least FEAR.exe, FEARE_8.Arch00, Default.archcfg,
// dinput8.dll, d3d9.dll and dxwrapper.ini copied from the GOG install (archives may be the
// real ones; missing big archives are replaced by empty stand-ins in a temporary copy).
func TestRealGOGFolder(t *testing.T) {
	ref := os.Getenv("FEAR_REF_GOG")
	if ref == "" {
		t.Skip("FEAR_REF_GOG not set: no copy of a real GOG folder to test with")
	}
	dir := filepath.Join(t.TempDir(), "F.E.A.R. Platinum Collection")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		// copied, never linked (a link would let a test write into the reference copy)
		if err := fixtures.CopyFile(filepath.Join(ref, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"FEAR.Arch00", "dxwrapper.dll", "goggame-1423058413.info"} {
		if findFile(dir, n) == "" {
			if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, d := range []string{"FEARXP", "FEARXP2"} {
		_ = os.Mkdir(filepath.Join(dir, d), 0o755)
	}

	// The copy's dinput8.dll is either GOG's own input wrapper (a fresh GOG install) or EchoPatch (a GOG
	// install the player has already prepared).
	// Both are real cases; anything else means the reference copy is not a GOG folder.
	kind := classifyDinput8(filepath.Join(ref, "dinput8.dll"))
	if kind != dinputWrapper && kind != dinputEchoPatch {
		t.Fatalf("GOG dinput8.dll classified as %v, want the GOG input wrapper or EchoPatch", kind)
	}
	c := Inspect(dir)
	if !c.Valid || c.Edition != "gog" || c.Version.Status != VersionVerified {
		t.Fatalf("valid=%v edition=%q version=%q message=%q", c.Valid, c.Edition, c.Version.Status, c.Message)
	}
	if kind == dinputWrapper && (c.EchoPatch != EchoPatchWrapper || !strings.Contains(c.EchoMessage, "GOG.com Input wrapper")) {
		t.Fatalf("echo=%q %q", c.EchoPatch, c.EchoMessage)
	}
	if kind == dinputEchoPatch && c.EchoPatch != EchoPatchOK {
		t.Fatalf("EchoPatch in a GOG folder: echo=%q %q", c.EchoPatch, c.EchoMessage)
	}
	// GOG ships LimitPerFrameFPS = 60; a player may already have set it to 0 (no cap, no warning).
	warned := strings.Contains(strings.Join(c.Warnings, "|"), "limits the game to")
	if n := dxwrapperFPSCap(dir); (n > 0) != warned || (n > 0 && !strings.Contains(strings.Join(c.Warnings, "|"), fmt.Sprintf("limits the game to %d fps", n))) {
		t.Fatalf("dxwrapper frame cap %d, warnings %q", n, c.Warnings)
	}
}
