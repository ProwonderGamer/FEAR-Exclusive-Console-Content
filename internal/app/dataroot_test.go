package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"fearinstaller/internal/testref"
	"fearinstaller/internal/verify"
)

// The content build reads each console copy from the folder its check found (verify.Result.Root), not from the
// folder the player clicked: a PS3 disc folder holds its data in PS3_GAME/USRDIR/game, and an Xbox 360 pick of
// the Game folder belongs to the disc folder above it.

func TestDataRoot(t *testing.T) {
	cases := []struct {
		name string
		s    SourceState
		want string
	}{
		{"root found", SourceState{Path: `D:\disc`, Result: &verify.Result{Root: `D:\disc\PS3_GAME\USRDIR\game`}}, `D:\disc\PS3_GAME\USRDIR\game`},
		{"root empty", SourceState{Path: `D:\fear.iso`, Result: &verify.Result{}}, `D:\fear.iso`},
		{"not checked", SourceState{Path: `D:\disc`}, `D:\disc`},
		{"nothing selected", SourceState{}, ""},
	}
	for _, c := range cases {
		if got := dataRoot(c.s); got != c.want {
			t.Errorf("%s: dataRoot = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRequestForUsesDataRoots(t *testing.T) {
	st := State{
		PS3:  SourceState{Path: `D:\My FEAR disc`, Result: &verify.Result{Root: `D:\My FEAR disc\PS3_GAME\USRDIR\game`}},
		X360: SourceState{Path: `D:\F.E.A.R. Xbox 360\Game`, Result: &verify.Result{Root: `D:\F.E.A.R. Xbox 360`}},
	}
	st.PC.Path = `C:\Games\FEAR`
	cases := []struct {
		o         Options
		ps3, x360 string
	}{
		{Options{UsePS3: true, UseX360: true}, `D:\My FEAR disc\PS3_GAME\USRDIR\game`, `D:\F.E.A.R. Xbox 360`},
		{Options{UsePS3: true}, `D:\My FEAR disc\PS3_GAME\USRDIR\game`, ""},
		{Options{UseX360: true}, "", `D:\F.E.A.R. Xbox 360`},
		{Options{}, "", ""},
	}
	for _, c := range cases {
		req := requestFor(st, c.o)
		if req.PS3Path != c.ps3 || req.X360 != c.x360 || req.GameDir != st.PC.Path || req.Options != c.o {
			t.Errorf("%+v: got %+v, want PS3 %q, X360 %q, game %q", c.o, req, c.ps3, c.x360, st.PC.Path)
		}
	}

	// Without a found root (not checked, or a disc image) the selection itself goes to the build.
	st.PS3.Result, st.X360.Result = nil, &verify.Result{}
	req := requestFor(st, Options{UsePS3: true, UseX360: true})
	if req.PS3Path != st.PS3.Path || req.X360 != st.X360.Path {
		t.Errorf("no root: got PS3 %q, X360 %q, want the selections", req.PS3Path, req.X360)
	}
}

// TestDataRootRealPicks checks the real copies named by FEAR_REF_PS3 / FEAR_REF_X360 (any folder a player might
// pick: the copy, one above it or one inside it) and that the folder handed to the build holds the copy's data.
// It needs complete copies, so it runs with the full install test (the other reference sets are partial).
func TestDataRootRealPicks(t *testing.T) {
	if os.Getenv("FEAR_TEST_FULL_INSTALL") != "1" {
		t.Skip("set FEAR_TEST_FULL_INSTALL=1 (with FEAR_REF_PS3 and FEAR_REF_X360 = complete copies) to run it")
	}
	ps3, x360 := testref.Dir(t, "FEAR_REF_PS3"), testref.Dir(t, "FEAR_REF_X360")
	b, _ := newTest()
	var mu sync.Mutex
	var log []string
	b.SetLog(func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() })
	b.SetSource(PS3, ps3)
	b.SetSource(X360, x360)
	b.WaitIdle()
	st := b.Snapshot()
	req := requestFor(st, Options{UsePS3: true, UseX360: true})
	for _, c := range []struct {
		s    SourceState
		got  string
		file string // a file the content build reads from the data root
	}{
		{st.PS3, req.PS3Path, "ubercatalog.cat"},
		{st.X360, req.X360, "default.xex"},
	} {
		if c.s.Result == nil || c.s.Result.Status != verify.StatusOK {
			t.Fatalf("%s: not verified: %+v", c.s.Path, c.s.Result)
		}
		if _, err := os.Stat(filepath.Join(c.got, c.file)); err != nil {
			t.Errorf("%s: the build would read %q, which lacks %s", c.s.Path, c.got, c.file)
		}
		t.Logf("picked %s -> build reads %s", c.s.Path, c.got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, l := range log {
		if strings.Contains(l, "copy (") || strings.Contains(l, "Copy found in:") {
			t.Log(l)
		}
	}
}
