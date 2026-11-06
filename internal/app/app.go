// Package app is the installer's back end: it owns the selections, runs the
// (read-only) verification in the background and tells the UI what changed.
// It has no UI or Windows code, so it is fully testable on any OS.
//
// Every entry point is safe to call from any goroutine and never panics out:
// a malformed folder or image produces an error state, not a crash.
package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"fearinstaller/internal/pcgame"
	"fearinstaller/internal/verify"
)

// Source identifiers used by the UI.
const (
	PS3  = "ps3"
	X360 = "x360"
	PC   = "pc"
)

// Progress is the hashing progress of one source.
type Progress struct {
	Percent int    `json:"percent"`
	File    string `json:"file"`
	Index   int    `json:"index"`
	Count   int    `json:"count"`
}

// SourceState is everything the UI needs to draw one console row.
type SourceState struct {
	Path     string         `json:"path"`
	Name     string         `json:"name"`  // last component of Path, for display
	Phase    string         `json:"phase"` // idle | checking | done
	Progress *Progress      `json:"progress,omitempty"`
	Result   *verify.Result `json:"result,omitempty"`
}

// State is the full picture; it is pushed to the UI after every change.
type State struct {
	Rev    int64        `json:"rev"`    // increases with every change; the UI drops older states
	Dialog string       `json:"dialog"` // "" or the row ("pc", "ps3", "x360") whose file dialog is open
	PC     pcgame.Check `json:"pc"`
	// PCChoices lists the installations found automatically (Steam and/or GOG).
	// With two or more the user picks one; with one it is selected at start.
	PCChoices []PCChoice  `json:"pcChoices"`
	PS3       SourceState `json:"ps3"`
	X360      SourceState `json:"x360"`
	// Install is the install job's progress (see install.go); State "idle" until the first job starts.
	Install InstallProgress `json:"install"`
	// ModInstalled: the selected game folder holds this mod (its install record), so the UI offers Uninstall.
	ModInstalled bool `json:"modInstalled"`
}

// PCChoice is one automatically found installation, for the selector under the folder box.
type PCChoice struct {
	Path   string `json:"path"`
	Store  string `json:"store"`  // "steam" or "gog"
	Label  string `json:"label"`  // "Steam" or "GOG"
	Source string `json:"source"` // how it was found
}

// verifyFunc matches verify.VerifyPS3 / verify.VerifyX360.
type verifyFunc func(ctx context.Context, path string, progress func(verify.Progress)) verify.Result

// Backend holds the selections. Create it with New.
type Backend struct {
	mu     sync.Mutex
	state  State
	cancel map[string]context.CancelFunc
	gen    map[string]int
	wg     sync.WaitGroup
	emit   func(State)

	job           jobState
	uninstall     uninstallFunc
	log           logger
	verifiers     map[string]verifyFunc
	sources       func() pcgame.Sources
	progressEvery time.Duration
}

// SetSources replaces where the Backend looks for game folders (default: the registry and the usual folders of
// this machine). The browser preview uses it so a test run never finds the real installations. Call it before
// the page starts (before the first detection).
func (b *Backend) SetSources(f func() pcgame.Sources) { b.sources = f }

// New creates a Backend. emit is called (from any goroutine) with a snapshot
// whenever the state changes.
func New(emit func(State)) *Backend {
	b := &Backend{
		emit:          emit,
		cancel:        map[string]context.CancelFunc{},
		gen:           map[string]int{},
		verifiers:     map[string]verifyFunc{PS3: verify.VerifyPS3, X360: verify.VerifyX360},
		sources:       pcgame.SystemSources,
		progressEvery: 100 * time.Millisecond,
		uninstall:     realUninstall,
	}
	b.state.PC = pcgame.Inspect("")
	b.state.PCChoices = []PCChoice{}
	b.state.Install = InstallProgress{State: JobIdle}
	return b
}

// Snapshot returns the current state.
func (b *Backend) Snapshot() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// touch marks the state as changed. The caller holds b.mu.
func (b *Backend) touch() { b.state.Rev++ }

func (b *Backend) push() {
	if b.emit != nil {
		b.emit(b.Snapshot())
	}
}

func (b *Backend) src(kind string) *SourceState {
	if kind == PS3 {
		return &b.state.PS3
	}
	return &b.state.X360
}

func validSource(kind string) bool { return kind == PS3 || kind == X360 }

// Init looks for Steam and GOG installations and returns the first state. One
// installation is selected at once; with several (for example both stores) none
// is selected and the user chooses from PCChoices.
func (b *Backend) Init() State {
	choices := b.detect()
	b.mu.Lock()
	b.state.PCChoices = choices
	b.touch()
	b.mu.Unlock()
	switch len(choices) {
	case 0:
		return b.SetPC("")
	case 1:
		return b.SetPC(choices[0].Path)
	}
	b.SetPC("")
	b.mu.Lock()
	b.state.PC.Message = fmt.Sprintf("Found %d F.E.A.R. installations. Choose the one to use below, or Browse for another.", len(choices))
	b.touch()
	b.mu.Unlock()
	b.push()
	return b.Snapshot()
}

// detect looks for the game in the Steam libraries and the GOG places,
// recovering from any panic.
func (b *Backend) detect() (out []PCChoice) {
	out = []PCChoice{}
	defer func() {
		if recover() != nil {
			out = []PCChoice{}
		}
	}()
	for _, in := range pcgame.DetectAll(b.sources()) {
		label := "Steam"
		if in.Store == "gog" {
			label = "GOG"
		}
		out = append(out, PCChoice{Path: in.Path, Store: in.Store, Label: label, Source: in.Source})
	}
	return out
}

// Recheck inspects the current game folder again (the user may have just
// installed EchoPatch), or repeats the search when none is chosen. The list of
// found installations is refreshed too.
func (b *Backend) Recheck() State {
	b.mu.Lock()
	p := b.state.PC.Path
	b.mu.Unlock()
	if p == "" {
		return b.Init()
	}
	choices := b.detect()
	b.mu.Lock()
	b.state.PCChoices = choices
	b.touch()
	b.mu.Unlock()
	return b.SetPC(p)
}

// SetPC inspects a game folder chosen by the user (or auto-detection).
func (b *Backend) SetPC(input string) State {
	ck := inspectSafely(input)
	installed := modInstalled(ck)
	b.logPC(ck, installed)
	b.mu.Lock()
	b.state.PC, b.state.ModInstalled = ck, installed
	b.touch()
	b.mu.Unlock()
	b.push()
	return b.Snapshot()
}

// logPC writes the game folder check to the log (the folder with the account name hidden).
func (b *Backend) logPC(ck pcgame.Check, installed bool) {
	if ck.Path == "" {
		return
	}
	edition := ck.EditionLabel
	if edition == "" {
		edition = "unknown edition"
	}
	b.logf("Game folder: %s (%s). Valid: %v. EchoPatch ready: %v. Mod installed: %v.", pcgame.MaskUser(ck.Path), edition, ck.Valid, ck.Ready, installed)
	if ck.Message != "" {
		b.logf("  %s", ck.Message)
	}
}

// logSource writes a console copy's check result to the log.
func (b *Backend) logSource(kind string, r verify.Result) {
	b.logf("%s copy (%s): %s. %s", consoleName(kind), r.Container, r.Status, r.Message)
	if r.Root != "" {
		b.logf("  Copy found in: %s", r.Root)
	}
	if r.Technical != "" {
		b.logf("  %s", r.Technical)
	}
}

func inspectSafely(input string) (ck pcgame.Check) {
	defer func() {
		if p := recover(); p != nil {
			ck = pcgame.Check{Path: input, EchoPatch: pcgame.EchoPatchMissing, Message: "Could not read this folder."}
		}
	}()
	return pcgame.Inspect(input)
}

// SetSource starts verifying a console source in the background and returns
// immediately. A newer selection cancels the previous check of the same console.
func (b *Backend) SetSource(kind, path string) State {
	if !validSource(kind) {
		return b.Snapshot()
	}
	b.mu.Lock()
	if c := b.cancel[kind]; c != nil {
		c()
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel[kind] = cancel
	b.gen[kind]++
	gen := b.gen[kind]
	*b.src(kind) = SourceState{Path: path, Name: displayName(path), Phase: "checking"}
	b.touch()
	fn := b.verifiers[kind]
	b.mu.Unlock()
	b.push()

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		var last time.Time
		res := runVerifier(fn, ctx, kind, path, func(p verify.Progress) {
			if time.Since(last) < b.progressEvery {
				return
			}
			last = time.Now()
			b.mu.Lock()
			if b.gen[kind] == gen {
				pct := 0
				if p.Total > 0 {
					pct = int(p.Done * 100 / p.Total)
				}
				b.src(kind).Progress = &Progress{Percent: pct, File: p.File, Index: p.Index, Count: p.Count}
				b.touch()
			}
			b.mu.Unlock()
			b.push()
		})
		b.mu.Lock()
		if b.gen[kind] == gen && res.Status != verify.StatusCancelled {
			b.logSource(kind, res)
			s := b.src(kind)
			s.Phase, s.Progress, s.Result = "done", nil, &res
			b.touch()
		}
		b.mu.Unlock()
		b.push()
	}()
	return b.Snapshot()
}

// runVerifier calls the verifier and converts a panic into an error result, so
// a malformed image can never leave the row stuck on "Checking" or kill the app.
func runVerifier(fn verifyFunc, ctx context.Context, kind, path string, progress func(verify.Progress)) (res verify.Result) {
	defer func() {
		if p := recover(); p != nil {
			res = verify.Result{Source: kind, Status: verify.StatusError,
				Message:   "Could not read this file.",
				Hint:      "It may be damaged. Try another copy, or extract the disc to a folder.",
				Technical: fmt.Sprint("internal error: ", p)}
		}
	}()
	return fn(ctx, path, progress)
}

// ClearSource forgets a console selection (and cancels a running check).
func (b *Backend) ClearSource(kind string) State {
	if !validSource(kind) {
		return b.Snapshot()
	}
	b.mu.Lock()
	if c := b.cancel[kind]; c != nil {
		c()
	}
	b.gen[kind]++
	*b.src(kind) = SourceState{}
	b.touch()
	b.mu.Unlock()
	b.push()
	return b.Snapshot()
}

// beginDialog marks a native file dialog as open; it fails if one already is.
func (b *Backend) beginDialog(kind string) bool {
	b.mu.Lock()
	if b.state.Dialog != "" {
		b.mu.Unlock()
		return false
	}
	b.state.Dialog = kind
	b.touch()
	b.mu.Unlock()
	b.push()
	return true
}

func (b *Backend) endDialog() {
	b.mu.Lock()
	b.state.Dialog = ""
	b.touch()
	b.mu.Unlock()
	b.push()
}

// WaitIdle blocks until all background checks have finished (used by tests).
func (b *Backend) WaitIdle() { b.wg.Wait() }

// displayName is the last path component, or the whole path for a root.
func displayName(p string) string {
	if n := filepath.Base(p); n != "." && n != string(filepath.Separator) {
		return n
	}
	return p
}
