package model

import "testing"

// Golden SHA-256 of each player_watson stage: the Python recipes' output; the last
// equals the confirmed mod's file (left hand grip, in-game confirmed).
const (
	goldenWatsonConvert = "ee5ed4976cc6bdfcb55148370bd9fdfeece3c14c12870675cd98172cfb740b2c" // mdl2model00p.py
	goldenWatsonReload  = "7b76c14f0ea1d47b6b1d5468d0be4277fe027c2956681792694b0209d73b127f" // fix_watson_reload.py
	goldenWatsonCues    = "8aaab345334b8ac794358a624eeeb98c7fe087c89f76a1abc5f1f95b3a64c48a" // watson_reload_split.py
	goldenPlayerWatson  = "d9d9d6c04f8342833cc42743f9aff7fb68041b30c9792c354d30f8fcc1f49770" // watson_lefthand_grip.py
	goldenDeltaConvert  = "7017fa1a5d9528bc9e84c5bfa9ef61e364d031ac196cb7ed425b36552c5a6ed4"
)

func checkStage(t *testing.T, stage string, b []byte, want string) {
	t.Helper()
	got := sha(b)
	if len(want) < 64 {
		got = got[:len(want)]
	}
	if got != want {
		t.Fatalf("%s: sha256 %s, want %s (%d bytes)", stage, sha(b), want, len(b))
	}
	t.Logf("%s: %s (%d bytes)", stage, sha(b), len(b))
}

// TestPlayerWatsonGolden runs every stage from the PS3 catalog and checks each against its recorded hash, so a
// mismatch shows at its first stage.
func TestPlayerWatsonGolden(t *testing.T) {
	w := convertPS3(t, `chars\models\player_watson.mdl`)
	checkStage(t, "convert", w, goldenWatsonConvert)
	body := convertPS3(t, `chars\models\player_Deltaforce.mdl`)
	checkStage(t, "body convert", body, goldenDeltaConvert)
	w2, err := FixWatsonReload(w)
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "FixWatsonReload", w2, goldenWatsonReload)
	w3, err := SplitWatsonReloadCues(w2)
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "SplitWatsonReloadCues", w3, goldenWatsonCues)
	w4, err := WatsonLeftHandGrip(w3, body)
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "WatsonLeftHandGrip", w4, goldenPlayerWatson)

	// the LeftHand-socket fixed body (the shipped player_Deltaforce) defines the same gun frame
	pc := stockPlayer(t)
	fixed, err := SetLeftHandSocket(body, pc)
	if err != nil {
		t.Fatal(err)
	}
	w4b, err := WatsonLeftHandGrip(w3, fixed)
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "WatsonLeftHandGrip (shipped body)", w4b, goldenPlayerWatson)

	// one call, from the console bytes
	wm, wn := consoleModel(t, "FEAR_REF_PS3", `chars\models\player_watson.mdl`)
	bm, bn := consoleModel(t, "FEAR_REF_PS3", `chars\models\player_Deltaforce.mdl`)
	all, err := PlayerWatson(wm, wn, bm, bn)
	if err != nil {
		t.Fatal(err)
	}
	checkStage(t, "PlayerWatson", all, goldenPlayerWatson)

	// the steps refuse input they were not written for
	if _, err := SplitWatsonReloadCues(w3); err == nil {
		t.Error("SplitWatsonReloadCues accepted its own output")
	}
	if _, err := FixWatsonReload(append([]byte("LDOM"), w[4:]...)); err == nil {
		t.Error("FixWatsonReload accepted a non-PC model")
	}
}

func TestQuatToI16(t *testing.T) {
	v, err := quatToI16(quat{0, 0, 0, 1})
	if err != nil || v != [4]int16{0, 0, 0, 32767} {
		t.Fatalf("identity -> %v %v", v, err)
	}
	if _, err := quatToI16(quat{0, 0, 0, nanVal()}); err == nil {
		t.Fatal("NaN accepted")
	}
}

func nanVal() float64 { z := 0.0; return z / z }
