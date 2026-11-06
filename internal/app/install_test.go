package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"fearinstaller/internal/fixtures"
	"fearinstaller/internal/refdata"
	"fearinstaller/internal/verify"
)

// ------------------------------------------------------------ test helpers ---

// synthCopy writes a fake PS3 game folder (only the files named in sizes, with random contents) and returns
// its root and the InputFile list that describes it (sizes and hashes included).
func synthCopy(t *testing.T, sizes map[string]int) (root string, inputs []InputFile) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "ps3copy")
	// The copy has to look like a PS3 game folder (the readers recognise it by one of three entries).
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := sizes["minicatalog.cat"]; !ok {
		os.WriteFile(filepath.Join(root, "minicatalog.cat"), []byte("marker"), 0o644)
	}
	rng := rand.New(rand.NewSource(7))
	names := make([]string, 0, len(sizes))
	for n := range sizes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		data := make([]byte, sizes[n])
		rng.Read(data)
		p := filepath.Join(root, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		inputs = append(inputs, InputFile{Console: PS3, Rel: n, Size: int64(len(data)), Exact: true, SHA256: hex.EncodeToString(sum[:])})
	}
	return root, inputs
}

// jobBackend is a Backend with a valid game folder (EchoPatch present), a "verified" PS3 copy at ps3Path, a
// "verified" empty Xbox 360 folder (the verifiers are stubs: these tests are about the job, not about hashing)
// and the given installer.
func jobBackend(t *testing.T, ps3Path string, inst Installer) (*Backend, *[]State) {
	t.Helper()
	b, seen := newTest()
	b.SetPC(fakePC(t, true))
	okCopy(b, PS3, ps3Path, "folder")
	okCopy(b, X360, t.TempDir(), "folder")
	b.WaitIdle()
	b.SetInstaller(inst)
	return b, seen
}

// okCopy stubs the verifier of one console as a verified copy of the given container kind ("folder",
// "ISO 9660"...) and selects path for it.
func okCopy(b *Backend, console, path, kind string) {
	b.verifiers[console] = func(context.Context, string, func(verify.Progress)) verify.Result {
		return verify.Result{Source: console, Status: verify.StatusOK, Container: kind, Title: "x", Message: "ok"}
	}
	b.SetSource(console, path)
}

// bothCopies is the only complete selection: both console copies take part.
var bothCopies = Options{UsePS3: true, UseX360: true}

func waitInstall(t *testing.T, b *Backend, want string, within time.Duration) InstallProgress {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		ip := b.Snapshot().Install
		if ip.State == want {
			return ip
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for job state %q, still %+v", within, want, ip)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// snapshotTree describes every file and folder below root (names, sizes, modes, times, content hashes).
func snapshotTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Lstat, not d.Info(): on Windows d.Info() comes from the folder listing, whose copy of a subfolder's date
		// is updated late (NTFS), so a date set before the job could show up changed afterwards.
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%s|%v|%d|%d", strings.TrimPrefix(p, root), info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			s := sha256.Sum256(b)
			line += "|" + hex.EncodeToString(s[:8])
		}
		out = append(out, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ------------------------------------------------------------------- tests ---

func TestJobProgressIsMonotonicAndTotalsAreRight(t *testing.T) {
	sizes := map[string]int{"database/loki.gamedb": 2<<20 + 123, "minicatalog.cat": 77, "snd/sfx.cat": 3 << 20}
	root, inputs := synthCopy(t, sizes)
	want := int64(0)
	for _, n := range sizes {
		want += int64(n)
	}
	inst := &DryRunInstaller{Inputs: func(InstallRequest) []InputFile { return inputs }}
	b, seen := jobBackend(t, root, inst)
	before := len(*seen)

	if r := b.StartInstall(bothCopies); !r.Started || r.Job != 1 {
		t.Fatalf("job should start: %+v", r)
	}
	if st := b.Snapshot().Install; st.State != JobRunning || !st.Preview {
		t.Fatalf("a started job must be running at once, and say it is a preview: %+v", st)
	}
	b.WaitIdle()
	got := b.Snapshot().Install
	if got.State != JobDone || got.Error != "" {
		t.Fatalf("job should be done: %+v", got)
	}
	if got.TotalBytes != want || got.DoneBytes != want {
		t.Fatalf("totals: done %d total %d, want %d", got.DoneBytes, got.TotalBytes, want)
	}
	if got.Files != 3 || got.FilesDone != 3 {
		t.Fatalf("files: %d/%d", got.FilesDone, got.Files)
	}
	if !strings.Contains(got.Message, "nothing was written") {
		t.Fatalf("the done message must say that nothing was written: %q", got.Message)
	}

	var last InstallProgress
	var lastRev int64
	phases := []string{}
	for i, s := range (*seen)[before:] {
		ip := s.Install
		if ip.DoneBytes < last.DoneBytes {
			t.Fatalf("progress went backwards at push %d: %d -> %d", i, last.DoneBytes, ip.DoneBytes)
		}
		if s.Rev < lastRev {
			t.Fatalf("revision went backwards at push %d", i)
		}
		if ip.TotalBytes != 0 && ip.TotalBytes != want {
			t.Fatalf("total changed to %d at push %d", ip.TotalBytes, i)
		}
		if ip.DoneBytes > ip.TotalBytes && ip.TotalBytes != 0 {
			t.Fatalf("done %d beyond total %d", ip.DoneBytes, ip.TotalBytes)
		}
		if filepath.IsAbs(ip.File) || strings.Contains(ip.File, root) || strings.Contains(ip.Message+ip.Error, root) {
			t.Fatalf("a full path leaked into the progress: %+v", ip)
		}
		if n := len(phases); n == 0 || phases[n-1] != ip.Phase {
			phases = append(phases, ip.Phase)
		}
		last, lastRev = ip, s.Rev
	}
	if strings.Join(phases, ",") != "prepare,game,ps3,finish" {
		t.Fatalf("phases: %v", phases)
	}
	if last.State != JobDone {
		t.Fatalf("the last pushed state must be the finished one: %+v", last)
	}
}

func TestJobUsesTheRealInputListForFixturesAndPhrasesItPlainly(t *testing.T) {
	root, inputs := synthCopy(t, map[string]int{"minicatalog.cat": 10})
	b, seen := jobBackend(t, root, &DryRunInstaller{Inputs: func(InstallRequest) []InputFile { return inputs }})
	b.StartInstall(bothCopies)
	b.WaitIdle()
	sentence := map[string]string{}
	for _, s := range *seen {
		sentence[s.Install.Phase] = s.Install.Message
	}
	if sentence[PhasePS3] != "Reading your PlayStation 3 copy…" {
		t.Fatalf("phase sentence: %q", sentence[PhasePS3])
	}
}

func TestCancelStopsTheJobPromptly(t *testing.T) {
	root, inputs := synthCopy(t, map[string]int{"snd/sfx.cat": 6 << 20})
	b, _ := jobBackend(t, root, &DryRunInstaller{ChunkDelay: 30 * time.Millisecond, Inputs: func(InstallRequest) []InputFile { return inputs }})
	if r := b.StartInstall(bothCopies); !r.Started {
		t.Fatal(r.Reason)
	}
	for b.Snapshot().Install.DoneBytes == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	t0 := time.Now()
	b.CancelInstall()
	ip := waitInstall(t, b, JobCancelled, 2*time.Second)
	if d := time.Since(t0); d > 500*time.Millisecond {
		t.Fatalf("cancel took %v", d)
	}
	if ip.DoneBytes >= ip.TotalBytes || ip.Error != "" || !strings.Contains(ip.Message, "Nothing was changed") {
		t.Fatalf("cancelled state: %+v", ip)
	}
	// Cancel with nothing running is harmless, and a new job can start afterwards.
	b.CancelInstall()
	if r := b.StartInstall(bothCopies); !r.Started || r.Job != 2 {
		t.Fatalf("a second job after a cancelled one: %+v", r)
	}
	b.CancelInstall()
	b.WaitIdle()
}

// blocker is an Installer that waits until it is released or cancelled.
type blocker struct {
	started chan struct{}
	release chan struct{}
	err     error
}

func (k *blocker) Run(ctx context.Context, _ InstallRequest, report func(InstallProgress)) error {
	report(InstallProgress{Phase: PhasePS3, Message: "Reading your PlayStation 3 copy…", TotalBytes: 100})
	close(k.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-k.release:
		return k.err
	}
}

func TestSecondJobIsRefusedWhileOneRuns(t *testing.T) {
	k := &blocker{started: make(chan struct{}), release: make(chan struct{})}
	b, _ := jobBackend(t, t.TempDir(), k)
	if r := b.StartInstall(bothCopies); !r.Started {
		t.Fatal(r.Reason)
	}
	<-k.started
	r := b.StartInstall(bothCopies)
	if r.Started || !strings.Contains(r.Reason, "already running") {
		t.Fatalf("second job must be refused: %+v", r)
	}
	if b.Snapshot().Install.Job != 1 {
		t.Fatal("the refusal must not touch the running job")
	}
	close(k.release)
	b.WaitIdle()
	if ip := b.Snapshot().Install; ip.State != JobDone {
		t.Fatalf("%+v", ip)
	}
}

func TestJobIsRefusedWithBlockers(t *testing.T) {
	root, inputs := synthCopy(t, map[string]int{"minicatalog.cat": 10})
	inst := &DryRunInstaller{Inputs: func(InstallRequest) []InputFile { return inputs }}

	b, _ := jobBackend(t, root, inst)
	cases := []struct {
		name string
		o    Options
		want string
	}{
		{"no copy used", Options{}, "Select your PlayStation 3 copy"},
		{"xbox copy not used", Options{UsePS3: true}, "Select your Xbox 360 copy"},
		{"ps3 copy not used", Options{UseX360: true}, "Select your PlayStation 3 copy"},
	}
	for _, c := range cases {
		if r := b.StartInstall(c.o); r.Started || !strings.Contains(r.Reason, c.want) {
			t.Fatalf("%s: %+v", c.name, r)
		}
	}
	if ip := b.Snapshot().Install; ip.State != JobIdle || ip.Job != 0 {
		t.Fatalf("a refused job must leave no trace: %+v", ip)
	}

	// No EchoPatch: the game folder blocks.
	b2, _ := newTest()
	b2.SetPC(fakePC(t, false))
	b2.verifiers[PS3] = b.verifiers[PS3]
	b2.SetSource(PS3, root)
	b2.WaitIdle()
	b2.SetInstaller(inst)
	if r := b2.StartInstall(bothCopies); r.Started || !strings.Contains(r.Reason, "EchoPatch is required") {
		t.Fatalf("EchoPatch missing must refuse: %+v", r)
	}

	// A copy that is still being checked.
	b3, _ := newTest()
	b3.SetPC(fakePC(t, true))
	gate := make(chan struct{})
	b3.verifiers[PS3] = func(ctx context.Context, _ string, _ func(verify.Progress)) verify.Result {
		select {
		case <-gate:
		case <-ctx.Done():
			return verify.Result{Status: verify.StatusCancelled}
		}
		return verify.Result{Source: PS3, Status: verify.StatusOK, Container: "folder", Message: "ok"}
	}
	okCopy(b3, X360, t.TempDir(), "folder")
	b3.SetSource(PS3, root)
	if r := b3.StartInstall(bothCopies); r.Started || !strings.Contains(r.Reason, "Still checking") {
		t.Fatalf("a running check must refuse: %+v", r)
	}
	close(gate)
	b3.WaitIdle()
	if r := b3.StartInstall(bothCopies); !r.Started {
		t.Fatalf("after the check it must start: %+v", r)
	}
	b3.WaitIdle()

	// A copy that failed verification.
	b4, _ := newTest()
	b4.SetPC(fakePC(t, true))
	b4.verifiers[PS3] = func(context.Context, string, func(verify.Progress)) verify.Result {
		return verify.Result{Source: PS3, Status: verify.StatusMismatch, Message: "These files differ."}
	}
	b4.SetSource(PS3, root)
	b4.WaitIdle()
	if r := b4.StartInstall(bothCopies); r.Started || !strings.Contains(r.Reason, "not verified") {
		t.Fatalf("an unverified copy must refuse: %+v", r)
	}

	// A verified disc image: the content is built only from extracted folders.
	b5, _ := jobBackend(t, root, inst)
	okCopy(b5, X360, t.TempDir(), "XDVDFS")
	b5.WaitIdle()
	if r := b5.StartInstall(bothCopies); r.Started || !strings.Contains(r.Reason, "disc image") {
		t.Fatalf("a disc image must refuse: %+v", r)
	}
}

type panicker struct{}

func (panicker) Run(context.Context, InstallRequest, func(InstallProgress)) error {
	panic("the writer exploded")
}

func TestPanicInTheJobBecomesAnErrorState(t *testing.T) {
	b, seen := jobBackend(t, t.TempDir(), panicker{})
	if r := b.StartInstall(bothCopies); !r.Started {
		t.Fatal(r.Reason)
	}
	b.WaitIdle()
	ip := b.Snapshot().Install
	if ip.State != JobError || ip.Error == "" {
		t.Fatalf("%+v", ip)
	}
	if strings.Contains(ip.Error+ip.Message, "exploded") || strings.Contains(ip.Error, "goroutine") {
		t.Fatalf("the raw panic text must not reach the person: %q", ip.Error)
	}
	if got := (*seen)[len(*seen)-1].Install.State; got != JobError {
		t.Fatalf("the error state must be pushed: %s", got)
	}
	// The backend is still usable.
	b.SetInstaller(&blocker{started: make(chan struct{}), release: func() chan struct{} { c := make(chan struct{}); close(c); return c }()})
	if r := b.StartInstall(bothCopies); !r.Started || r.Job != 2 {
		t.Fatalf("a new job after a panic: %+v", r)
	}
	b.WaitIdle()
}

func TestInstallErrorsAreShownInPlainLanguage(t *testing.T) {
	root, inputs := synthCopy(t, map[string]int{"minicatalog.cat": 50})
	inputs[0].SHA256 = strings.Repeat("0", 64) // the file "changed" since it was checked
	b, _ := jobBackend(t, root, &DryRunInstaller{Inputs: func(InstallRequest) []InputFile { return inputs }})
	b.StartInstall(bothCopies)
	b.WaitIdle()
	ip := b.Snapshot().Install
	if ip.State != JobError || !strings.Contains(ip.Error, "minicatalog.cat") || !strings.Contains(ip.Error, "select the copy again") {
		t.Fatalf("%+v", ip)
	}

	// A file that vanished.
	root2, inputs2 := synthCopy(t, map[string]int{"minicatalog.cat": 50, "snd/sfx.cat": 10})
	os.Remove(filepath.Join(root2, "snd", "sfx.cat"))
	b2, _ := jobBackend(t, root2, &DryRunInstaller{Inputs: func(InstallRequest) []InputFile { return inputs2 }})
	b2.StartInstall(bothCopies)
	b2.WaitIdle()
	if ip := b2.Snapshot().Install; ip.State != JobError || !strings.Contains(ip.Error, "snd/sfx.cat") {
		t.Fatalf("%+v", ip)
	}

	// The game folder lost EchoPatch between the plan and the job.
	root3, inputs3 := synthCopy(t, map[string]int{"minicatalog.cat": 5})
	b3, _ := jobBackend(t, root3, &DryRunInstaller{Inputs: func(InstallRequest) []InputFile { return inputs3 }})
	os.Remove(filepath.Join(b3.Snapshot().PC.Path, "dinput8.dll"))
	b3.StartInstall(bothCopies)
	b3.WaitIdle()
	if ip := b3.Snapshot().Install; ip.State != JobError || !strings.Contains(ip.Error, "EchoPatch") {
		t.Fatalf("%+v", ip)
	}
}

func TestShutdownCancelsAndWaitsForTheJob(t *testing.T) {
	root, inputs := synthCopy(t, map[string]int{"snd/sfx.cat": 6 << 20})
	b, _ := jobBackend(t, root, &DryRunInstaller{ChunkDelay: 50 * time.Millisecond, Inputs: func(InstallRequest) []InputFile { return inputs }})
	b.StartInstall(bothCopies)
	for b.Snapshot().Install.DoneBytes == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	t0 := time.Now()
	b.Shutdown()
	if time.Since(t0) > time.Second {
		t.Fatalf("Shutdown took %v", time.Since(t0))
	}
	if ip := b.Snapshot().Install; ip.State != JobCancelled {
		t.Fatalf("the window closing must leave a cancelled job: %+v", ip)
	}
	b.mu.Lock()
	running := b.job.cur != nil
	b.mu.Unlock()
	if running {
		t.Fatal("job still registered after Shutdown")
	}
}

// The job must write nothing: not into the game folder, not into the copies, not a temporary file.
func TestJobWritesNothing(t *testing.T) {
	root, inputs := synthCopy(t, map[string]int{"database/loki.gamedb": 1 << 20, "snd/sfx.cat": 2<<20 + 5})
	tmpdir := t.TempDir()
	t.Setenv("TMPDIR", tmpdir)
	t.Setenv("TEMP", tmpdir)
	b, _ := jobBackend(t, root, &DryRunInstaller{Inputs: func(InstallRequest) []InputFile { return inputs }})
	game := b.Snapshot().PC.Path
	os.WriteFile(filepath.Join(game, "Default.archcfg"), []byte("FEAR.Arch00\r\nFEAR_1.Arch00"), 0o644)
	gameBefore, copyBefore := snapshotTree(t, game), snapshotTree(t, root)

	if r := b.StartInstall(bothCopies); !r.Started {
		t.Fatal(r.Reason)
	}
	b.WaitIdle()
	if ip := b.Snapshot().Install; ip.State != JobDone {
		t.Fatalf("%+v", ip)
	}
	if got := snapshotTree(t, game); strings.Join(got, "\n") != strings.Join(gameBefore, "\n") {
		t.Fatalf("the game folder changed:\nbefore %v\nafter  %v", gameBefore, got)
	}
	if got := snapshotTree(t, root); strings.Join(got, "\n") != strings.Join(copyBefore, "\n") {
		t.Fatalf("the console copy changed")
	}
	if ents, _ := os.ReadDir(tmpdir); len(ents) != 0 {
		t.Fatalf("the job left files in the temp folder: %v", ents)
	}
	if _, err := os.Stat(filepath.Join(game, ModFolder)); err == nil {
		t.Fatal("the mod folder must not be created by the check-only job")
	}
}

// -------------------------------------------------------- the input lists ---

func TestEveryInputFileIsInTheVerifiedLists(t *testing.T) {
	all := InstallRequest{Options: bothCopies, PS3Path: "p", X360: "x"}
	list := DefaultInputs(all)
	seen := map[string]bool{}
	var ps3, x360 int
	for _, f := range list {
		key := f.Console + "|" + strings.ToLower(f.Rel)
		if seen[key] {
			t.Errorf("%s listed twice", f.Rel)
		}
		seen[key] = true
		if f.Size == 0 {
			t.Errorf("%s %s: not in the reference lists, so it could not be checked", f.Console, f.Rel)
		}
		if f.Console == PS3 {
			ps3++
		} else {
			x360++
		}
		if f.SHA256 == "" && !strings.Contains(strings.ToLower(f.Rel), "ubercatalog") {
			t.Errorf("%s has no hash", f.Rel)
		}
	}
	if ps3 == 0 || x360 == 0 {
		t.Fatalf("both consoles should contribute: %d / %d", ps3, x360)
	}
	// A copy that does not take part adds no files.
	for _, f := range DefaultInputs(InstallRequest{Options: Options{UsePS3: true}, PS3Path: "p", X360: "x"}) {
		if f.Console != PS3 {
			t.Errorf("an unused Xbox 360 copy must not be read: %s", f.Rel)
		}
	}
	if got := DefaultInputs(InstallRequest{Options: bothCopies}); len(got) != 0 {
		t.Fatalf("no copy given, nothing read: %v", got)
	}
}

func TestPlanGates(t *testing.T) {
	root, _ := synthCopy(t, map[string]int{"minicatalog.cat": 5})
	b, _ := jobBackend(t, root, NewDryRunInstaller())
	p := b.Plan(bothCopies)
	if !p.CanContinue || len(p.Gates.Game)+len(p.Gates.Copies)+len(p.Gates.Content)+len(p.Gates.Install) != 0 {
		t.Fatalf("%+v", p.Gates)
	}
	if p.ModDir != filepath.Join(b.Snapshot().PC.Path, ModFolder) {
		t.Fatalf("ModDir %q", p.ModDir)
	}
	for _, g := range []string{"Bonus mission: SFOD-D", "Watson SAS-12 shotgun", "Bonus mission: Holiday", "SM15 machine pistol", "Instant Action", "Xbox 360 HUD", "Bonus mission menu", "Console-style objectives", "Bonus mission fixes"} {
		if !containsGroup(p, g) {
			t.Fatalf("no %q group: %+v", g, p.Groups)
		}
	}
	// One copy is not enough, and it is a step-02 blocker only.
	p = b.Plan(Options{UsePS3: true})
	if p.CanContinue || len(p.Gates.Copies) != 1 || !contains(p.Gates.Copies, "Xbox 360") || len(p.Gates.Install) != 0 || containsGroup(p, "Xbox 360 HUD") || containsGroup(p, "Bonus mission menu") {
		t.Fatalf("one copy: %+v / %+v", p.Gates, p.Groups)
	}
	// A disc image blocks at the install step only.
	okCopy(b, X360, t.TempDir(), "XDVDFS")
	b.WaitIdle()
	p = b.Plan(bothCopies)
	if p.CanContinue || len(p.Gates.Copies) != 0 || len(p.Gates.Install) != 1 || !contains(p.Gates.Install, "disc image") {
		t.Fatalf("disc image: %+v", p.Gates)
	}
	// The gates are concatenated in step order into Blockers, and the slices are never nil (JSON arrays).
	b2, _ := newTest()
	p = b2.Plan(Options{})
	if p.Gates.Content == nil || p.Gates.Install == nil || len(p.Gates.Game) != 1 || len(p.Gates.Copies) != 2 || len(p.Blockers) != 3 {
		t.Fatalf("%+v / %v", p.Gates, p.Blockers)
	}
}

// ------------------------------------------- with the real reference data ---

func TestInstallJobWithRealFixtures(t *testing.T) {
	ps3Ref, x360Ref := fixtures.RefRoots(t)
	tmp := t.TempDir()
	ps3 := filepath.Join(tmp, "ps3")
	fixtures.Materialize(t, ps3, fixtures.PS3Files(t, ps3Ref, tmp, refdata.PS3TitleID))
	x360ISO := filepath.Join(tmp, "x360") // an extracted folder: the content is built only from folders
	fixtures.Materialize(t, x360ISO, fixtures.X360Files(t, x360Ref, tmp))
	pc := fakePC(t, true)
	steamRoot := filepath.Dir(filepath.Dir(filepath.Dir(pc)))
	b, seen := newTest(steamRoot)
	b.SetInstaller(NewDryRunInstaller()) // the reading job; the real writer has its own fake-folder test
	b.Init()
	b.SetSource(PS3, ps3)
	b.SetSource(X360, x360ISO)
	b.WaitIdle()
	all := bothCopies
	if p := b.Plan(all); !p.CanContinue {
		t.Fatalf("fixtures should verify: %v", p.Blockers)
	}

	// Totals: what the job says it will read equals the sum of the real sizes of the files it lists (a
	// catalog that is only checked against a lower bound has its own size).
	var want int64
	srcs := map[string]*verify.Source{}
	for k, p := range map[string]string{PS3: ps3, X360: x360ISO} {
		s, err := verify.OpenSource(k, p)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		srcs[k] = s
	}
	for _, f := range DefaultInputs(InstallRequest{Options: all, PS3Path: ps3, X360: x360ISO}) {
		rel, size, err := locate(srcs[f.Console], f)
		if err != nil {
			t.Fatalf("%s: %v (%s)", f.Rel, err, rel)
		}
		want += size
	}
	gameBefore := snapshotTree(t, pc)
	if r := b.StartInstall(all); !r.Started {
		t.Fatal(r.Reason)
	}
	b.WaitIdle()
	ip := b.Snapshot().Install
	if ip.State != JobDone || ip.TotalBytes != want || ip.DoneBytes != want || want < 100<<20 {
		t.Fatalf("done %d / total %d / expected %d: %+v", ip.DoneBytes, ip.TotalBytes, want, ip)
	}
	var last int64
	sawPS3, sawX360 := false, false
	for _, s := range *seen {
		if s.Install.DoneBytes < last {
			t.Fatalf("progress went backwards")
		}
		last = s.Install.DoneBytes
		sawPS3 = sawPS3 || s.Install.Phase == PhasePS3 && s.Install.File != ""
		sawX360 = sawX360 || s.Install.Phase == PhaseX360 && s.Install.File != ""
		if strings.Contains(s.Install.File, tmp) {
			t.Fatalf("path leaked: %q", s.Install.File)
		}
	}
	if !sawPS3 || !sawX360 {
		t.Fatal("both consoles should have been read")
	}
	if strings.Join(snapshotTree(t, pc), "\n") != strings.Join(gameBefore, "\n") {
		t.Fatal("the game folder changed")
	}

	// A copy that changes after it was verified is caught by the job. The fixture files are symlinks to the
	// reference data, so the entry is replaced (never written through) with a damaged copy.
	f := filepath.Join(ps3, "PS3_GAME", "USRDIR", "game", "minicatalog.cat")
	orig, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	orig[100] ^= 0xFF
	if err := os.WriteFile(f, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	b.StartInstall(all)
	b.WaitIdle()
	if ip := b.Snapshot().Install; ip.State != JobError || !strings.Contains(ip.Error, "minicatalog.cat") {
		t.Fatalf("a changed file must stop the job: %+v", ip)
	}
}
