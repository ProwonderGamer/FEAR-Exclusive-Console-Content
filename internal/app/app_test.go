package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"fearinstaller/internal/fixtures"
	"fearinstaller/internal/isobuild"
	"fearinstaller/internal/pcgame"
	"fearinstaller/internal/refdata"
	"fearinstaller/internal/verify"
)

// newTest creates a Backend that records every pushed state.
func newTest(steam ...string) (*Backend, *[]State) {
	var mu sync.Mutex
	var seen []State
	b := New(func(s State) { mu.Lock(); seen = append(seen, s); mu.Unlock() })
	b.sources = func() pcgame.Sources { return pcgame.Sources{SteamRoots: steam} }
	b.progressEvery = 0
	return b, &seen
}

func fakePC(t *testing.T, echo bool) string {
	dir := filepath.Join(t.TempDir(), "steamapps", "common", "FEAR Ultimate Shooter Edition")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "FEAR.exe"), []byte("MZ"), 0o644)
	os.WriteFile(filepath.Join(dir, "FEAR_8.Arch00"), []byte("x"), 0o644)
	if echo {
		os.WriteFile(filepath.Join(dir, "dinput8.dll"), []byte("EchoPatch"), 0o644)
	}
	return dir
}

func TestEndToEnd(t *testing.T) {
	ps3Ref, x360Ref := fixtures.RefRoots(t)
	tmp := t.TempDir()
	ps3 := filepath.Join(tmp, "ps3")
	fixtures.Materialize(t, ps3, fixtures.PS3Files(t, ps3Ref, tmp, refdata.PS3TitleID))
	x360ISO := filepath.Join(tmp, "x360.iso")
	if err := isobuild.WriteXDVDFS(x360ISO, fixtures.X360Files(t, x360Ref, tmp), 0x0FD90000); err != nil {
		t.Fatal(err)
	}

	pc := fakePC(t, true)
	steamRoot := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(pc))))
	b, seen := newTest(steamRoot)
	st := b.Init()
	if !st.PC.Valid || !st.PC.Ready {
		t.Fatalf("auto-detect failed: %+v", st.PC)
	}

	all := Options{UsePS3: true, UseX360: true}
	if p := b.Plan(all); p.CanContinue || len(p.Blockers) != 2 {
		t.Fatalf("nothing selected yet, expected two blockers: %+v", p.Blockers)
	}

	b.SetSource(PS3, ps3)
	if b.Snapshot().PS3.Phase != "checking" {
		t.Fatal("should be checking right away")
	}
	if p := b.Plan(all); !contains(p.Blockers, "Still checking") {
		t.Fatalf("expected a 'still checking' blocker: %v", p.Blockers)
	}
	b.SetSource(X360, x360ISO)
	b.WaitIdle()

	s := b.Snapshot()
	if s.PS3.Result == nil || !s.PS3.Result.Verified() || s.X360.Result == nil || !s.X360.Result.Verified() {
		t.Fatalf("both should verify: %+v / %+v", s.PS3.Result, s.X360.Result)
	}

	// Both copies verify, but the Xbox 360 one is a disc image: the content is built only from folders, so the
	// install step (and only it) blocks.
	p := b.Plan(all)
	if p.CanContinue || len(p.Blockers) != 1 || !contains(p.Gates.Install, "disc image") {
		t.Fatalf("blockers: %v", p.Blockers)
	}
	titles := []string{}
	for _, g := range p.Groups {
		titles = append(titles, g.Title)
	}
	t.Logf("groups: %v", titles)
	if len(p.Groups) != 9 {
		t.Fatalf("expected SFOD-D, Watson, Holiday, SM15, Instant Action, Xbox 360 HUD and the three both-console items; got %v", titles)
	}

	// Both copies are required.
	if p := b.Plan(Options{UsePS3: true}); !contains(p.Gates.Copies, "Select your Xbox 360 copy") || containsGroup(p, "Xbox 360 HUD") || containsGroup(p, "Bonus mission menu") {
		t.Fatalf("PS3 alone must block: %v", p.Blockers)
	}
	if p := b.Plan(Options{UseX360: true}); !contains(p.Gates.Copies, "Select your PlayStation 3 copy") {
		t.Fatalf("Xbox 360 alone must block: %v", p.Blockers)
	}
	if b.Plan(Options{}).CanContinue {
		t.Fatal("no source ticked must block")
	}

	// A bad PS3 selection replaces the good one and blocks only when it is ticked.
	b.SetSource(PS3, tmp+"/nonexistent")
	b.WaitIdle()
	if p := b.Plan(all); p.CanContinue || !contains(p.Blockers, "PlayStation 3 copy is not verified") {
		t.Fatalf("bad PS3 should block: %v", p.Blockers)
	}
	if p := b.Plan(Options{UseX360: true}); !contains(p.Blockers, "Select your PlayStation 3 copy") {
		t.Fatalf("unticked PS3 must still be asked for: %v", p.Blockers)
	}

	// Clearing resets the row.
	b.ClearSource(PS3)
	if s := b.Snapshot().PS3; s.Phase != "" || s.Result != nil {
		t.Fatalf("not cleared: %+v", s)
	}
	if len(*seen) < 6 {
		t.Fatalf("UI should have been notified repeatedly, got %d pushes", len(*seen))
	}
}

func TestEchoPatchBlocksContinue(t *testing.T) {
	b, _ := newTest()
	b.SetPC(fakePC(t, false))
	b.verifiers[PS3] = func(_ context.Context, _ string, _ func(verify.Progress)) verify.Result {
		return verify.Result{Source: PS3, Status: verify.StatusOK, Title: "x", Message: "ok"}
	}
	b.SetSource(PS3, "whatever")
	b.WaitIdle()
	p := b.Plan(Options{UsePS3: true})
	if p.CanContinue || !contains(p.Blockers, "EchoPatch is required") {
		t.Fatalf("missing EchoPatch must block: %v", p.Blockers)
	}
}

func TestNewerSelectionWins(t *testing.T) {
	b, _ := newTest()
	release := make(chan struct{})
	b.verifiers[PS3] = func(ctx context.Context, path string, _ func(verify.Progress)) verify.Result {
		if path == "slow" {
			<-ctx.Done() // cancelled by the second selection
			<-release
			return verify.Result{Status: verify.StatusCancelled}
		}
		return verify.Result{Source: PS3, Status: verify.StatusOK, Message: "fast"}
	}
	b.SetSource(PS3, "slow")
	b.SetSource(PS3, "fast")
	close(release)
	b.WaitIdle()
	if s := b.Snapshot().PS3; s.Path != "fast" || s.Result == nil || s.Result.Message != "fast" {
		t.Fatalf("stale result overwrote the newer one: %+v", s)
	}
}

func contains(l []string, sub string) bool {
	for _, s := range l {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func containsGroup(p Plan, title string) bool {
	for _, g := range p.Groups {
		if g.Title == title {
			return true
		}
	}
	return false
}
