package app

import (
	"errors"
	"fmt"
	"sync"

	"fearinstaller/internal/install"
	"fearinstaller/internal/pcgame"
)

// UninstallResult is what the UI shows after Uninstall.
type UninstallResult struct {
	Done    bool     `json:"done"`
	Message string   `json:"message"` // plain language
	Notes   []string `json:"notes"`   // install.UninstallResult notes (never nil)
}

// uninstallFunc matches install.Uninstall for this mod folder; the browser preview replaces it.
type uninstallFunc func(gameDir string) (*install.UninstallResult, error)

func realUninstall(gameDir string) (*install.UninstallResult, error) {
	return install.Uninstall(gameDir, ModFolder, install.GameRunning)
}

// SetUninstaller replaces what Uninstall runs (the browser preview passes one that removes nothing). Call it
// before the page starts.
func (b *Backend) SetUninstaller(f func(gameDir string) (*install.UninstallResult, error)) {
	b.mu.Lock()
	b.uninstall = f
	b.mu.Unlock()
}

// Uninstall takes the mod out of the selected game folder. install.Uninstall does the work from the install
// record: it removes only what this installer added and puts the backed-up files back. It refuses while an
// install job or a file dialog is open, and an install cannot start while it runs.
func (b *Backend) Uninstall() (res UninstallResult) {
	res.Notes = []string{}
	b.mu.Lock()
	switch {
	case b.job.cur != nil || b.job.uninstalling:
		b.mu.Unlock()
		res.Message = "Wait for the installation to finish first."
		return res
	case b.state.Dialog != "":
		b.mu.Unlock()
		res.Message = "Close the file dialog first."
		return res
	case !b.state.PC.Valid:
		b.mu.Unlock()
		res.Message = "Select your F.E.A.R. folder first."
		return res
	}
	b.job.uninstalling = true
	dir, run := b.state.PC.Path, b.uninstall
	b.mu.Unlock()
	defer func() {
		if p := recover(); p != nil {
			b.logf("Uninstall: internal error: %v", p)
			res = UninstallResult{Message: "The uninstall hit an unexpected problem and stopped.", Notes: []string{}}
		}
		b.mu.Lock()
		b.job.uninstalling = false
		b.mu.Unlock()
		b.refreshPC()
	}()

	b.logf("Uninstall started.")
	r, err := run(dir)
	if err != nil {
		res.Message = "The uninstall could not be finished."
		var ie *install.Error
		if errors.As(err, &ie) {
			res.Message = ie.Msg
		}
		b.logf("Uninstall failed: %v", err)
		return res
	}
	res.Done, res.Message = true, "The mod was removed. Your game is back to how it was before."
	if r != nil && len(r.Notes) > 0 {
		res.Message = "The mod was removed. A few things were left as they are:"
	}
	if r != nil {
		res.Notes = append(res.Notes, r.Notes...)
		b.logf("Uninstall finished: %d mod files removed.", r.Removed)
		for _, n := range r.Notes {
			b.logf("  %s", n)
		}
	}
	return res
}

// modInstalled reports whether a game folder holds this mod's install record.
func modInstalled(ck pcgame.Check) bool {
	if !ck.Valid {
		return false
	}
	rec, err := install.ReadRecord(ck.Path, ModFolder)
	return err == nil && rec != nil
}

// refreshPC inspects the selected game folder again (after an install or uninstall changed it).
func (b *Backend) refreshPC() {
	path := b.Snapshot().PC.Path
	if path == "" {
		return
	}
	ck := inspectSafely(path)
	installed := modInstalled(ck)
	b.mu.Lock()
	if b.state.PC.Path == path { // a newer selection wins
		b.state.PC, b.state.ModInstalled = ck, installed
		b.touch()
	}
	b.mu.Unlock()
	b.push()
}

// ------------------------------------------------------------------ log ---

// logger holds the log sink (see SetLog). It has its own lock, so a line can be written while b.mu is held.
type logger struct {
	mu sync.Mutex
	f  func(string)
}

// SetLog sets where the installer's log lines go (the Windows host writes them to a file next to the
// program). Lines are plain language; logf hides the account name in every path (pcgame.MaskUser).
func (b *Backend) SetLog(f func(string)) {
	b.log.mu.Lock()
	b.log.f = f
	b.log.mu.Unlock()
}

func (b *Backend) logf(format string, a ...any) {
	b.log.mu.Lock()
	defer b.log.mu.Unlock()
	if b.log.f != nil {
		// every line, error texts included, has the account name in paths hidden
		b.log.f(pcgame.MaskUser(fmt.Sprintf(format, a...)))
	}
}

// logLine is logf for callers that pass ready-made lines (install.Request.Log).
func (b *Backend) logLine(s string) { b.logf("%s", s) }
