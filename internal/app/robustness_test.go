package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fearinstaller/internal/verify"
)

type fakeDialogs struct {
	mu      sync.Mutex
	answers []string
	calls   int
	panicOn bool
	entered chan struct{}
	release chan struct{}
}

func (d *fakeDialogs) next() string {
	d.mu.Lock()
	d.calls++
	if d.panicOn {
		d.mu.Unlock()
		panic("dialog exploded")
	}
	var a string
	if len(d.answers) > 0 {
		a, d.answers = d.answers[0], d.answers[1:]
	}
	d.mu.Unlock()
	if d.entered != nil {
		close(d.entered)
		<-d.release
	}
	return a
}
func (d *fakeDialogs) PickFolder(_, _ string) string { return d.next() }

// Browse returns at once; the dialog runs through Post (the UI thread in
// the real app) and the result arrives as a pushed state.
func TestBrowseReturnsBeforeTheDialogCloses(t *testing.T) {
	b, seen := newTest()
	b.verifiers[PS3] = func(context.Context, string, func(verify.Progress)) verify.Result {
		return verify.Result{Source: PS3, Status: verify.StatusOK, Message: "ok"}
	}
	d := &fakeDialogs{answers: []string{"/some/folder"}, entered: make(chan struct{}), release: make(chan struct{})}
	var posted []func()
	var mu sync.Mutex
	g := &Bridge{B: b, D: d, Post: func(fn func()) { mu.Lock(); posted = append(posted, fn); mu.Unlock() }}

	st := g.Browse(PS3, "folder")
	if st.Dialog != PS3 {
		t.Fatalf("state should say the PS3 dialog is open: %+v", st.Dialog)
	}
	if d.calls != 0 {
		t.Fatal("the dialog must not have run inside Browse")
	}
	// A second Browse while one is pending is ignored, not queued.
	g.Browse(X360, "folder")
	mu.Lock()
	n := len(posted)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("expected exactly one posted dialog, got %d", n)
	}
	// "UI thread" runs the posted work.
	done := make(chan struct{})
	go func() { posted[0](); close(done) }()
	<-d.entered
	if b.Snapshot().Dialog != PS3 {
		t.Fatal("dialog flag must stay set while the dialog is open")
	}
	close(d.release)
	<-done
	b.WaitIdle()
	s := b.Snapshot()
	if s.Dialog != "" || s.PS3.Path != "/some/folder" || s.PS3.Result == nil {
		t.Fatalf("after the dialog: %+v", s)
	}
	if len(*seen) < 3 {
		t.Fatal("the UI must be notified of the dialog opening, closing and the result")
	}
}

func TestBrowseCancelAndPanicRecover(t *testing.T) {
	b, _ := newTest()
	d := &fakeDialogs{}
	g := &Bridge{B: b, D: d}
	g.Browse(PS3, "folder") // cancelled: nothing selected
	if s := b.Snapshot(); s.Dialog != "" || s.PS3.Path != "" {
		t.Fatalf("cancel changed something: %+v", s)
	}
	var logged []string
	g.OnInternalError = func(s string) { logged = append(logged, s) }
	d.panicOn = true
	g.Browse(X360, "folder") // the dialog panics: the app lives, the flag is reset
	if b.Snapshot().Dialog != "" || len(logged) != 1 {
		t.Fatalf("panic handling: dialog=%q logged=%v", b.Snapshot().Dialog, logged)
	}
	// Unknown kinds or modes are ignored (they used to reach a nil function / wrong row).
	for _, c := range [][2]string{{"zzz", "folder"}, {PS3, "zzz"}, {PC, "file"}, {"", ""}} {
		g.Browse(c[0], c[1])
	}
	if d.calls != 2 { // the cancelled and the panicking dialog only
		t.Fatalf("invalid Browse calls opened dialogs: %d", d.calls)
	}
	if st := g.Clear("zzz"); st.Rev != b.Snapshot().Rev {
		t.Fatal("unknown kind must not change state")
	}
	b.SetSource("zzz", "x")
	if s := b.Snapshot(); s.X360.Phase != "" {
		t.Fatalf("unknown kind wrote into the X360 row: %+v", s.X360)
	}
}

// A verifier that panics must not leave the row on "Checking" or crash.
func TestVerifierPanicBecomesErrorRow(t *testing.T) {
	b, _ := newTest()
	b.verifiers[PS3] = func(context.Context, string, func(verify.Progress)) verify.Result { panic("reader bug") }
	b.SetSource(PS3, "x")
	b.WaitIdle()
	s := b.Snapshot().PS3
	if s.Phase != "done" || s.Result == nil || s.Result.Status != verify.StatusError || s.Result.Message != "Could not read this file." {
		t.Fatalf("%+v %+v", s, s.Result)
	}
	if !strings.Contains(s.Result.Technical, "reader bug") {
		t.Fatalf("technical text missing: %q", s.Result.Technical)
	}
}

// A malformed image given to the real verifiers ends in a result, never a panic.
func TestRealVerifiersSurviveGarbage(t *testing.T) {
	b, _ := newTest()
	p := filepath.Join(t.TempDir(), "x.iso")
	os.WriteFile(p, make([]byte, 1<<20), 0o644)
	b.SetSource(PS3, p)
	b.SetSource(X360, p)
	b.WaitIdle()
	for _, s := range []SourceState{b.Snapshot().PS3, b.Snapshot().X360} {
		if s.Phase != "done" || s.Result == nil || s.Result.Verified() {
			t.Fatalf("%+v", s)
		}
	}
}

// Rev increases with every change, so the UI can drop stale pushed states.
func TestRevisionIncreases(t *testing.T) {
	b, seen := newTest()
	b.verifiers[PS3] = func(_ context.Context, _ string, p func(verify.Progress)) verify.Result {
		p(verify.Progress{Done: 1, Total: 2})
		return verify.Result{Source: PS3, Status: verify.StatusOK}
	}
	b.SetPC(fakePC(t, true))
	b.SetSource(PS3, "x")
	b.WaitIdle()
	var last int64 = -1
	for _, s := range *seen {
		if s.Rev < last {
			t.Fatalf("revision went backwards: %d after %d", s.Rev, last)
		}
		last = s.Rev
	}
	if p := b.Plan(Options{UsePS3: true}); p.Rev != b.Snapshot().Rev {
		t.Fatalf("plan rev %d vs state rev %d", p.Rev, b.Snapshot().Rev)
	}
}

// Recheck sees EchoPatch appear.
func TestRecheckSeesEchoPatchInstalled(t *testing.T) {
	b, _ := newTest()
	pc := fakePC(t, false)
	if st := b.SetPC(pc); st.PC.Ready {
		t.Fatal("no EchoPatch yet")
	}
	os.WriteFile(filepath.Join(pc, "dinput8.dll"), []byte("EchoPatch"), 0o644)
	if st := b.Recheck(); !st.PC.Ready || st.PC.Path != pc {
		t.Fatalf("recheck: %+v", st.PC)
	}
}

// Progress pushes happen while hashing but Plan results do not depend on them.
func TestPlanIgnoresProgress(t *testing.T) {
	b, _ := newTest()
	b.SetPC(fakePC(t, true))
	gate := make(chan struct{})
	b.verifiers[PS3] = func(_ context.Context, _ string, p func(verify.Progress)) verify.Result {
		p(verify.Progress{Done: 1, Total: 4})
		<-gate
		return verify.Result{Source: PS3, Status: verify.StatusOK}
	}
	b.SetSource(PS3, "x")
	time.Sleep(20 * time.Millisecond)
	p1 := b.Plan(Options{UsePS3: true})
	if p1.CanContinue || !contains(p1.Blockers, "Still checking") {
		t.Fatalf("%v", p1.Blockers)
	}
	close(gate)
	b.WaitIdle()
}
