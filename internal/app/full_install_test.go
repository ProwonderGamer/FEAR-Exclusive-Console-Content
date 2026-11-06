package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"fearinstaller/internal/content"
	"fearinstaller/internal/install"
	"fearinstaller/internal/testref"
	"fearinstaller/internal/verify"
)

// TestFullInstallAndUninstall runs the whole installer the way a player does: it checks both real console
// copies, installs the complete mod into a COPY of a stock PC game folder, compares every mod file with the
// confirmed one, and uninstalls again, which must put the copy back exactly as it was.
//
// It copies the whole PC game (about 5 GB) into the test's temporary folder and builds the whole mod, so it
// runs only on request: FEAR_TEST_FULL_INSTALL=1 plus FEAR_REF_PCGAME (a complete stock game folder, read
// only), FEAR_REF_PS3 and FEAR_REF_X360 (see internal/testref). Use go test -timeout 60m.
func TestFullInstallAndUninstall(t *testing.T) {
	if os.Getenv("FEAR_TEST_FULL_INSTALL") != "1" {
		t.Skip("set FEAR_TEST_FULL_INSTALL=1 (with FEAR_REF_PCGAME, FEAR_REF_PS3, FEAR_REF_X360) to run the full install test")
	}
	stock := testref.File(t, "FEAR_REF_PCGAME", "FEAR.exe")
	ps3, x360 := testref.Dir(t, "FEAR_REF_PS3"), testref.Dir(t, "FEAR_REF_X360")

	game := filepath.Join(t.TempDir(), "FEAR Ultimate Shooter Edition")
	start := time.Now()
	copyTree(t, filepath.Dir(stock), game)
	t.Logf("copied the PC game in %v", time.Since(start).Round(time.Second))
	before := hashTree(t, game)

	b, _ := newTest()
	var mu sync.Mutex
	var log []string
	b.SetLog(func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() })
	b.SetPC(game)
	b.SetSource(PS3, ps3)
	b.SetSource(X360, x360)
	b.WaitIdle()
	st := b.Snapshot()
	if !st.PC.Valid || st.ModInstalled {
		t.Fatalf("game folder: valid %v, mod installed %v: %s", st.PC.Valid, st.ModInstalled, st.PC.Message)
	}
	for _, s := range []*SourceState{&st.PS3, &st.X360} {
		if s.Result == nil || s.Result.Status != verify.StatusOK {
			if s.Result == nil {
				t.Fatalf("%s copy: not checked", s.Path)
			}
			t.Fatalf("%s copy: %s: %s (%s)", s.Result.Source, s.Result.Status, s.Result.Message, s.Result.Technical)
		}
	}
	if p := b.Plan(bothCopies); len(p.Blockers) != 0 {
		t.Fatalf("plan blockers: %q", p.Blockers)
	}

	// Install: with both copies and nothing set by the test, the Backend picks the RealInstaller itself.
	start = time.Now()
	if r := b.StartInstall(bothCopies); !r.Started {
		t.Fatalf("install did not start: %s", r.Reason)
	}
	// The progress the page sees: the count of built files must climb through the build, not jump at the end.
	var seen []InstallProgress
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				if p := b.Snapshot().Install; p.State == JobRunning {
					seen = append(seen, p)
				}
			}
		}
	}()
	ip := waitFinished(t, b, 45*time.Minute)
	close(stop)
	<-sampled
	checkFileProgress(t, seen, ip, len(content.ModFiles(choiceOf(bothCopies, true, true))))
	if ip.State != "done" || ip.Preview {
		t.Fatalf("install ended %+v", ip)
	}
	t.Logf("installed in %v", time.Since(start).Round(time.Second))
	// The job reports "done" first and then inspects the game folder again (refreshPC), so ModInstalled
	// follows a moment later: wait for it rather than reading it in that gap.
	for deadline := time.Now().Add(30 * time.Second); !b.Snapshot().ModInstalled; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("ModInstalled is false after the install")
		}
	}

	// Every confirmed file is there with the confirmed hash; look-alike files (rebuilt sounds and pictures,
	// proven within a tolerance by their own golden tests) only need to be there. Nothing else is in the folder.
	mod := filepath.Join(game, ModFolder)
	want := map[string]content.ModFile{}
	for _, f := range content.ModFiles(choiceOf(bothCopies, true, true)) {
		want[strings.ToLower(filepath.FromSlash(f.Path))] = f
	}
	got := hashTree(t, mod)
	exact, alike := 0, 0
	for rel, h := range got {
		f, ok := want[strings.ToLower(rel)]
		switch {
		case strings.EqualFold(rel, install.RecordFile):
		case !ok:
			t.Errorf("unexpected file in the mod folder: %s", rel)
		case content.LookAlike(f.Path):
			alike++
		case !strings.EqualFold(h.sha, f.SHA256):
			t.Errorf("%s differs from the confirmed file", rel)
		default:
			exact++
		}
	}
	if exact+alike != len(want) {
		t.Errorf("%d of %d mod files are there", exact+alike, len(want))
	}
	t.Logf("mod files: %d byte-exact, %d look-alike", exact, alike)

	// The game folder: only the hook and Default.archcfg (and its one backup) changed; every stock file is untouched.
	after := hashTree(t, game)
	hook := install.HookSHA256 // the tested hook, filled in from the copies (BuildHook)
	for rel, h := range after {
		top := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		switch {
		case strings.EqualFold(top, ModFolder):
		case strings.EqualFold(rel, install.HookName):
			if h.sha != hook {
				t.Errorf("the installed hook is not the installer's own")
			}
		case strings.EqualFold(rel, "Default.archcfg"), strings.EqualFold(rel, "Default.archcfg.CR_backup"):
		case before[rel] != h:
			t.Errorf("stock file changed or added by the install: %s", rel)
		}
	}
	for rel := range before {
		if _, ok := after[rel]; !ok {
			t.Errorf("stock file removed by the install: %s", rel)
		}
	}

	// Uninstall puts the folder back exactly (names, sizes, contents).
	if r := b.Uninstall(); !r.Done {
		t.Fatalf("uninstall: %+v", r)
	}
	if b.Snapshot().ModInstalled {
		t.Fatal("ModInstalled is still true after the uninstall")
	}
	if d := diffTrees(before, hashTree(t, game)); d != "" {
		t.Fatalf("the uninstall did not restore the game folder:\n%s", d)
	}

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(log, "\n")
	for _, s := range []string{"Install 1 started.", "Install 1 finished.", "Uninstall finished"} {
		if !strings.Contains(joined, s) {
			t.Errorf("log lacks %q", s)
		}
	}
}

// waitFinished waits until the job leaves "running".
func waitFinished(t *testing.T, b *Backend, within time.Duration) InstallProgress {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		ip := b.Snapshot().Install
		if ip.State != "running" && ip.State != "idle" {
			return ip
		}
		if time.Now().After(deadline) {
			t.Fatalf("the install still runs after %v: %+v", within, ip)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type fileSum struct {
	size int64
	sha  string
}

// hashTree returns every file below root (relative path -> size and SHA-256), streaming large files.
func hashTree(t *testing.T, root string) map[string]fileSum {
	t.Helper()
	out := map[string]fileSum{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[rel] = fileSum{n, hex.EncodeToString(h.Sum(nil))}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func diffTrees(a, b map[string]fileSum) string {
	var d []string
	for k, v := range a {
		if w, ok := b[k]; !ok {
			d = append(d, "missing: "+k)
		} else if w != v {
			d = append(d, "changed: "+k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			d = append(d, "added: "+k)
		}
	}
	sort.Strings(d)
	return strings.Join(d, "\n")
}

// copyTree copies a folder (files and subfolders, keeping file dates). Reference data is always copied, never
// linked, so nothing the test does can reach the original.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a plain file", p)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		info, err := in.Stat()
		if err != nil {
			return err
		}
		return os.Chtimes(target, info.ModTime(), info.ModTime())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// checkFileProgress: every sample of a running install names the expected file total, the count of built files
// never goes back, and the bar stood at several points between empty and full (it moved during the build).
func checkFileProgress(t *testing.T, seen []InstallProgress, end InstallProgress, want int) {
	t.Helper()
	between := map[int]bool{}
	last := 0
	for _, p := range seen {
		if p.Files != want {
			t.Fatalf("progress names %d files, want %d", p.Files, want)
		}
		if p.FilesDone < last || p.FilesDone > p.Files {
			t.Fatalf("files done went from %d to %d (of %d)", last, p.FilesDone, p.Files)
		}
		last = p.FilesDone
		if p.FilesDone > 0 && p.FilesDone < p.Files {
			between[p.FilesDone] = true
		}
	}
	if len(between) < 3 {
		t.Errorf("the file count stood at only %d points between 0 and %d in %d samples", len(between), want, len(seen))
	}
	if end.Files != want || end.FilesDone != want {
		t.Errorf("finished with %d of %d files, want %d of %d", end.FilesDone, end.Files, want, want)
	}
	t.Logf("progress: %d samples, %d distinct counts during the build", len(seen), len(between))
}
