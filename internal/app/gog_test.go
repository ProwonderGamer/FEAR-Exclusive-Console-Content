package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/archcfg"
	"fearinstaller/internal/pcgame"
)

func wr(t *testing.T, p, c string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gogGame(t *testing.T, dir string, echo bool) string {
	for _, f := range []string{"FEAR.exe", "FEAR.Arch00", "FEAR_8.Arch00", "dxwrapper.dll", "d3d9.dll"} {
		wr(t, filepath.Join(dir, f), "MZ")
	}
	if echo {
		wr(t, filepath.Join(dir, "dinput8.dll"), "MZ EchoPatch")
	} else {
		wr(t, filepath.Join(dir, "dinput8.dll"), "MZ dxwrapper.dll")
	}
	wr(t, filepath.Join(dir, "Default.archcfg"), strings.Join(archcfg.StockOrder([]string{"FEAR.Arch00", "FEAR_8.Arch00"}), "\r\n"))
	return dir
}

func backendWith(src pcgame.Sources) *Backend {
	b := New(nil)
	b.sources = func() pcgame.Sources { return src }
	b.progressEvery = 0
	return b
}

func TestInitOneGOGInstallIsSelected(t *testing.T) {
	g := gogGame(t, filepath.Join(t.TempDir(), "GOG Games", "F.E.A.R. Platinum"), false)
	b := backendWith(pcgame.Sources{GOGRoots: []string{filepath.Dir(g)}})
	st := b.Init()
	if st.PC.Path != g || !st.PC.Valid || st.PC.Edition != "gog" || len(st.PCChoices) != 1 || st.PCChoices[0].Label != "GOG" {
		t.Fatalf("%+v %+v", st.PC, st.PCChoices)
	}
	p := b.Plan(Options{UsePS3: true})
	if !strings.Contains(strings.Join(p.Blockers, "|"), "GOG's own input wrapper, not EchoPatch") {
		t.Fatalf("blockers %q", p.Blockers)
	}
	if p.Archcfg == nil || p.Archcfg.Error != "" || p.Archcfg.Added != ModFolder || len(p.Archcfg.Tail) != 3 || p.Archcfg.Tail[2] != ModFolder {
		t.Fatalf("%+v", p.Archcfg)
	}
	if !strings.Contains(strings.Join(p.Reads, "|"), "F.E.A.R. folder (GOG)") {
		t.Fatalf("reads %q", p.Reads)
	}
}

func TestInitSeveralInstallsLeavesTheChoiceToTheUser(t *testing.T) {
	tmp := t.TempDir()
	steam := filepath.Join(tmp, "Steam")
	sg := filepath.Join(steam, "steamapps", "common", pcgame.SteamFolder)
	for _, f := range []string{"FEAR.exe", "FEAR.Arch00"} {
		wr(t, filepath.Join(sg, f), "MZ")
	}
	wr(t, filepath.Join(sg, "dinput8.dll"), "EchoPatch")
	g := gogGame(t, filepath.Join(tmp, "GOG Games", "F.E.A.R. Platinum"), true)
	b := backendWith(pcgame.Sources{SteamRoots: []string{steam}, GOGRoots: []string{filepath.Dir(g)}})
	st := b.Init()
	if st.PC.Path != "" || len(st.PCChoices) != 2 || st.PCChoices[0].Store != "steam" || st.PCChoices[1].Store != "gog" {
		t.Fatalf("%+v", st)
	}
	if !strings.Contains(st.PC.Message, "Found 2 F.E.A.R. installations") {
		t.Fatalf("message %q", st.PC.Message)
	}
	if p := b.Plan(Options{UsePS3: true}); len(p.Blockers) == 0 || !strings.Contains(p.Blockers[0], "Choose which F.E.A.R. installation") {
		t.Fatalf("blockers %q", p.Blockers)
	}
	// choosing one (the UI calls SetPC) keeps the list
	st = b.SetPC(g)
	if !st.PC.Valid || !st.PC.Ready || len(st.PCChoices) != 2 || st.PC.Edition != "gog" {
		t.Fatalf("%+v", st)
	}
	// Verify keeps the choice and refreshes the list (the Steam copy disappears)
	os.RemoveAll(sg)
	st = b.Recheck()
	if st.PC.Path != g || len(st.PCChoices) != 1 {
		t.Fatalf("%+v %+v", st.PC.Path, st.PCChoices)
	}
}

func TestInitNothingFound(t *testing.T) {
	b := backendWith(pcgame.Sources{})
	st := b.Init()
	if st.PC.Path != "" || len(st.PCChoices) != 0 || st.PCChoices == nil {
		t.Fatalf("%+v", st)
	}
	if p := b.Plan(Options{UsePS3: true}); !strings.Contains(p.Blockers[0], "Steam or GOG") {
		t.Fatalf("%q", p.Blockers)
	}
}

func TestPlanDryRunOfArchcfg(t *testing.T) {
	g := gogGame(t, filepath.Join(t.TempDir(), "F.E.A.R. Platinum"), true)
	b := backendWith(pcgame.Sources{})
	b.SetPC(g)
	cfg := filepath.Join(g, "Default.archcfg")
	before, _ := os.ReadFile(cfg)

	p := b.Plan(Options{})
	a := p.Archcfg
	if a == nil || !a.Changed || a.Encoding != "UTF-8" || a.LineEnding != "CRLF" || a.Entries != 2 || a.Error != "" {
		t.Fatalf("%+v", a)
	}
	if !strings.Contains(strings.Join(p.Changes, "|"), "Default.archcfg.CR_backup") {
		t.Fatalf("changes %q", p.Changes)
	}
	// glued line from earlier appends is announced
	wr(t, cfg, "FEAR.Arch00\r\nFEAR_8.Arch00ButtonMod\r\n")
	if a := b.Plan(Options{}).Archcfg; len(a.Notes) == 0 || !strings.Contains(a.Notes[0], "Split the joined line") {
		t.Fatalf("%+v", a)
	}
	// an unusable file blocks CONTINUE and says why
	wr(t, cfg, "MZ\x00\x00\x01\x02")
	p = b.Plan(Options{UsePS3: false})
	if p.Archcfg.Error == "" || p.CanContinue || !strings.Contains(strings.Join(p.Blockers, "|"), "Default.archcfg in the game folder cannot be used: it is not a text file") {
		t.Fatalf("%+v %q", p.Archcfg, p.Blockers)
	}
	// the dry run never writes
	wr(t, cfg, string(before))
	b.Plan(Options{})
	if after, _ := os.ReadFile(cfg); string(after) != string(before) {
		t.Fatal("the dry run changed Default.archcfg")
	}
	for _, n := range []string{"Default.archcfg.CR_backup", "Default.archcfg.CR_tmp"} {
		if _, err := os.Stat(filepath.Join(g, n)); err == nil {
			t.Fatalf("%s was created by a dry run", n)
		}
	}
}
