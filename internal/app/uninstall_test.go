package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"fearinstaller/internal/install"
)

// writeRecord puts a minimal install record of this mod into a fake game folder.
func writeRecord(t *testing.T, gameDir string) {
	t.Helper()
	dir := filepath.Join(gameDir, ModFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"format":1,"mod":"` + ModFolder + `","items":[],"files":[]}`
	if err := os.WriteFile(filepath.Join(dir, install.RecordFile), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallIsRefusedWhenItCannotRun(t *testing.T) {
	ran := false
	stub := func(string) (*install.UninstallResult, error) { ran = true; return &install.UninstallResult{}, nil }

	b, _ := newTest()
	b.SetUninstaller(stub)
	if r := b.Uninstall(); r.Done || r.Message != "Select your F.E.A.R. folder first." {
		t.Fatalf("no game folder: %+v", r)
	}

	b.SetPC(fakePC(t, true))
	b.mu.Lock()
	b.state.Dialog = PS3
	b.mu.Unlock()
	if r := b.Uninstall(); r.Done || r.Message != "Close the file dialog first." {
		t.Fatalf("dialog open: %+v", r)
	}
	b.mu.Lock()
	b.state.Dialog = ""
	b.mu.Unlock()

	k := &blocker{started: make(chan struct{}), release: make(chan struct{})}
	b2, _ := jobBackend(t, t.TempDir(), k)
	b2.SetUninstaller(stub)
	if r := b2.StartInstall(bothCopies); !r.Started {
		t.Fatalf("job did not start: %+v", r)
	}
	if r := b2.Uninstall(); r.Done || r.Message != "Wait for the installation to finish first." {
		t.Fatalf("job running: %+v", r)
	}
	close(k.release)
	waitInstall(t, b2, "done", 5*time.Second)
	if ran {
		t.Fatal("the uninstaller ran although every call was refused")
	}
}

func TestUninstallRemovesTheModAndUpdatesTheState(t *testing.T) {
	b, _ := newTest()
	game := fakePC(t, true)
	writeRecord(t, game)
	b.SetPC(game)
	if !b.Snapshot().ModInstalled {
		t.Fatal("ModInstalled is false for a folder with the install record")
	}
	var got string
	b.SetUninstaller(func(dir string) (*install.UninstallResult, error) {
		got = dir
		return &install.UninstallResult{Removed: 3, Notes: []string{"note"}}, os.RemoveAll(filepath.Join(dir, ModFolder))
	})
	r := b.Uninstall()
	if !r.Done || len(r.Notes) != 1 || r.Notes[0] != "note" {
		t.Fatalf("result %+v", r)
	}
	if got != b.Snapshot().PC.Path {
		t.Fatalf("uninstalled %q, selected %q", got, b.Snapshot().PC.Path)
	}
	if b.Snapshot().ModInstalled {
		t.Fatal("ModInstalled is still true after the uninstall")
	}
}

// An installed mod is never installed over: step 01 stays open (the page goes on to Play), while the Install gate
// refuses until the mod is uninstalled.
func TestPlanRefusesInstallOverInstalledMod(t *testing.T) {
	const msg = "The mod is already installed. Uninstall it first to install it again."
	b, _ := newTest()
	game := fakePC(t, true)
	writeRecord(t, game)
	b.SetPC(game)
	p := b.Plan(bothCopies)
	if len(p.Gates.Game) != 0 {
		t.Fatalf("step 01 gates %q: an installed mod must not block Continue", p.Gates.Game)
	}
	if !slices.Contains(p.Gates.Install, msg) || p.CanContinue {
		t.Fatalf("install gates %q, can continue %v: want the installed-mod refusal", p.Gates.Install, p.CanContinue)
	}
	if err := os.RemoveAll(filepath.Join(game, ModFolder)); err != nil {
		t.Fatal(err)
	}
	b.Recheck()
	if g := b.Plan(bothCopies).Gates.Install; slices.Contains(g, msg) {
		t.Fatalf("install gates %q: the refusal stayed after the mod was removed", g)
	}
}

func TestUninstallFailureIsShownPlainly(t *testing.T) {
	b, _ := newTest()
	b.SetPC(fakePC(t, true))
	b.SetUninstaller(func(string) (*install.UninstallResult, error) {
		return nil, &install.Error{Msg: "Close F.E.A.R. first.", Technical: "FEAR.exe is running"}
	})
	if r := b.Uninstall(); r.Done || r.Message != "Close F.E.A.R. first." || r.Notes == nil {
		t.Fatalf("install.Error: %+v", r)
	}
	b.SetUninstaller(func(string) (*install.UninstallResult, error) { panic("boom") })
	if r := b.Uninstall(); r.Done || !strings.Contains(r.Message, "unexpected problem") {
		t.Fatalf("panic: %+v", r)
	}
	// the panic must not leave the backend thinking an uninstall still runs
	b.SetUninstaller(func(string) (*install.UninstallResult, error) { return &install.UninstallResult{}, nil })
	if r := b.Uninstall(); !r.Done {
		t.Fatalf("after a panic: %+v", r)
	}
}

func TestInstallIsRefusedWhileUninstalling(t *testing.T) {
	b, _ := jobBackend(t, t.TempDir(), &blocker{started: make(chan struct{}), release: make(chan struct{})})
	entered, release := make(chan struct{}), make(chan struct{})
	b.SetUninstaller(func(string) (*install.UninstallResult, error) {
		close(entered)
		<-release
		return &install.UninstallResult{}, nil
	})
	done := make(chan UninstallResult)
	go func() { done <- b.Uninstall() }()
	<-entered
	if r := b.StartInstall(bothCopies); r.Started || r.Reason != "Wait for the uninstall to finish first." {
		t.Fatalf("install during uninstall: %+v", r)
	}
	if r := b.Uninstall(); r.Done {
		t.Fatalf("second uninstall: %+v", r)
	}
	close(release)
	if r := <-done; !r.Done {
		t.Fatalf("first uninstall: %+v", r)
	}
}

func TestLogHidesTheAccountName(t *testing.T) {
	b, _ := newTest()
	var mu sync.Mutex
	var lines []string
	b.SetLog(func(s string) { mu.Lock(); lines = append(lines, s); mu.Unlock() })
	b.logf("open %s: %v", `C:\Users\Jane Doe\Games\FEAR`, `read C:/Users/jane/x: denied`)
	b.SetPC(fakePC(t, true))
	mu.Lock()
	defer mu.Unlock()
	if len(lines) < 2 {
		t.Fatalf("lines %q", lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "Jane") || strings.Contains(l, "jane") {
			t.Fatalf("account name in the log: %q", l)
		}
	}
	if !strings.Contains(lines[0], `C:\Users\<user>\Games\FEAR`) || !strings.HasPrefix(lines[1], "Game folder: ") {
		t.Fatalf("lines %q", lines)
	}
}
