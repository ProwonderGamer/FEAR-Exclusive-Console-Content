package model

import (
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/ltar"
	"fearinstaller/internal/testref"
)

// Golden SHA-256 of the post-convert fix steps. Each equals the file in the confirmed mod and
// the Python recipe's output.
const (
	goldenWatsonBody    = "ede5a3949ebdfe0d5e89ef087b5b401a301d2a1ef3f43378c92343dd82681ca4" // guns\models_hh\WatsonSAS.Model00p
	goldenDeltaforce    = "50a3a94579fb47e8fa7edb713a093672081c91b6ea3ea1981e5e86f1256e2a4d" // chars\models\player_Deltaforce.Model00p
	goldenPlayerConsole = "e9cea18027f680828c84cdefecd06b39389ca4c70e65a884d518500569f61bea" // chars\models\player_console.Model00p
	goldenStockPlayer   = "554cd3689dc8eb29341999241f637eeb2284028935ea81185b47331f62fedeb5" // FEAR_7.Arch00 chars\models\player.Model00p
)

// stockPlayer reads chars\models\player.Model00p from <FEAR_REF_PCGAME>\FEAR_7.Arch00 (or skips).
func stockPlayer(t *testing.T) []byte {
	t.Helper()
	a, err := ltar.Open(testref.File(t, "FEAR_REF_PCGAME", "FEAR_7.Arch00"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, e := range a.Entries {
		if strings.EqualFold(e.Path, `chars\models\player.Model00p`) {
			b, err := a.Read(e)
			if err != nil {
				t.Fatal(err)
			}
			if sha(b) != goldenStockPlayer {
				t.Fatalf("stock player.Model00p sha256 %s, want %s", sha(b), goldenStockPlayer)
			}
			return b
		}
	}
	t.Fatalf("%s: player.Model00p not found", filepath.Join("FEAR_REF_PCGAME", "FEAR_7.Arch00"))
	return nil
}

func convertPS3(t *testing.T, name string) []byte {
	t.Helper()
	src, stored := consoleModel(t, "FEAR_REF_PS3", name)
	out, _, err := Convert(src, stored, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAddWatsonBodyGolden(t *testing.T) {
	w := convertPS3(t, `guns\models_hh\WatsonSAS.mdl`)
	ref, _ := consoleModel(t, "FEAR_REF_PS3", `guns\models_hh\shotgun.mdl`)
	out, err := AddWatsonBody(w, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha(out); got != goldenWatsonBody {
		t.Errorf("sha256 %s, want %s", got, goldenWatsonBody)
	}
	if _, err := AddWatsonBody(out, ref); err == nil {
		t.Errorf("a model that already has a body was accepted")
	}
}

func TestMakePlayerConsoleGolden(t *testing.T) {
	out, err := MakePlayerConsole(stockPlayer(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := sha(out); got != goldenPlayerConsole {
		t.Errorf("sha256 %s, want %s", got, goldenPlayerConsole)
	}
	if _, err := MakePlayerConsole(out); err == nil {
		t.Errorf("player_console accepted twice (children would be duplicated)")
	}
}

func TestSetLeftHandSocketGolden(t *testing.T) {
	stock := stockPlayer(t)
	body := convertPS3(t, `chars\models\player_Deltaforce.mdl`)
	pc, err := MakePlayerConsole(stock)
	if err != nil {
		t.Fatal(err)
	}
	// the recipe reads the socket from player_console; the stock player carries the same one
	for _, p := range [][]byte{pc, stock} {
		out, err := SetLeftHandSocket(body, p)
		if err != nil {
			t.Fatal(err)
		}
		if got := sha(out); got != goldenDeltaforce {
			t.Errorf("sha256 %s, want %s", got, goldenDeltaforce)
		}
	}
}
