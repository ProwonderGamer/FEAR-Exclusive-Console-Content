package app

import (
	"fmt"

	"fearinstaller/internal/pcgame"
)

// Dialogs is the native folder picker; the Windows build implements it with the
// system dialogs, the dev preview with a scripted queue. PickFolder blocks
// until the user answers, so it must not be called from the browser's
// message callback (see Bridge.Post).
//
// key names the row that opened the dialog (the row kind: "pc", "ps3" or
// "x360"), so an implementation can give each row its own remembered starting
// folder.
type Dialogs interface {
	PickFolder(key, title string) string // "" when cancelled
}

// Bridge is the set of calls the web UI makes.
//
// Threading: the host runs every call on its own goroutine, never on the UI
// thread, so file reads here cannot freeze the window. A call that needs the UI
// thread (a modal dialog) is handed to Post, which runs it there outside of
// any browser callback; Browse itself returns at once and the result arrives
// as a pushed state.
type Bridge struct {
	B *Backend
	D Dialogs
	// Post runs fn on the UI thread, later and outside the current call. Nil
	// means "run it right now" (tests).
	Post func(fn func())
	// Licenses is the third-party license text (ui.LicensesText). The page never asks for it; the host sets it
	// so the text stays embedded in the program file.
	Licenses string
	// OnInternalError, if set, is told about a recovered panic (for a log).
	OnInternalError func(err string)
	// OnQuit, if set, closes the program's window (the Close button on the last step).
	OnQuit func()
	// OpenURL, if set, opens a web address in the user's browser. Only OpenEchoPatchPage calls it, with a fixed address.
	OpenURL func(url string)
}

// protect runs fn and, if it panics, returns the current state instead.
func (g *Bridge) protect(name string, fn func() State) (st State) {
	defer func() {
		if p := recover(); p != nil {
			if g.OnInternalError != nil {
				g.OnInternalError(fmt.Sprintf("%s: %v", name, p))
			}
			st = g.B.Snapshot()
		}
	}()
	return fn()
}

// Init auto-detects the game folder and returns the first state.
func (g *Bridge) Init() State { return g.protect("init", g.B.Init) }

// SetPC inspects a typed or picked game folder.
func (g *Bridge) SetPC(path string) State {
	return g.protect("setPC", func() State { return g.B.SetPC(path) })
}

// Recheck repeats the game-folder (and EchoPatch) check, for "Verify"
// and when the window regains focus.
func (g *Bridge) Recheck() State { return g.protect("recheck", g.B.Recheck) }

// Browse opens a native picker and returns immediately with the current state
// (its Dialog field says which row is waiting). kind is "pc", "ps3" or "x360";
// mode must be "folder": any other value opens no dialog. Cancelling leaves everything unchanged.
func (g *Bridge) Browse(kind, mode string) State {
	return g.protect("browse", func() State {
		if kind != PC && !validSource(kind) || mode != "folder" {
			return g.B.Snapshot()
		}
		if !g.B.beginDialog(kind) {
			return g.B.Snapshot()
		}
		run := func() {
			defer g.B.endDialog()
			defer func() {
				if p := recover(); p != nil && g.OnInternalError != nil {
					g.OnInternalError(fmt.Sprintf("dialog: %v", p))
				}
			}()
			var path string
			switch {
			case kind == PC:
				path = g.D.PickFolder(kind, "Select the F.E.A.R. folder (the one containing FEAR.exe)")
			case kind == PS3:
				path = g.D.PickFolder(kind, "Select the decrypted PlayStation 3 disc folder")
			default:
				path = g.D.PickFolder(kind, "Select the extracted Xbox 360 folder (the one containing default.xex)")
			}
			if path == "" {
				return
			}
			// File reads leave the UI thread at once.
			if kind == PC {
				go g.protect("setPC", func() State { return g.B.SetPC(path) })
			} else {
				g.B.SetSource(kind, path) // returns immediately; hashing runs in a goroutine
			}
		}
		if g.Post != nil {
			g.Post(run)
		} else {
			run()
		}
		return g.B.Snapshot()
	})
}

// Clear forgets a console selection (and stops its check).
func (g *Bridge) Clear(kind string) State {
	return g.protect("clear", func() State { return g.B.ClearSource(kind) })
}

// Plan evaluates the tick boxes against the current state.
func (g *Bridge) Plan(o Options) (p Plan) {
	defer func() {
		if recover() != nil {
			p = Plan{Blockers: []string{"Something went wrong while checking the options. Please try again."}, Groups: []PlanGroup{}}
		}
	}()
	return g.B.Plan(o)
}

// Install starts the install job for the chosen items and returns at once; progress arrives as pushed
// states (State.Install). It refuses (Started false, with the reason) when a job is already running or the
// choices have a blocker.
func (g *Bridge) Install(o Options) (r StartResult) {
	defer func() {
		if p := recover(); p != nil {
			if g.OnInternalError != nil {
				g.OnInternalError(fmt.Sprintf("install: %v", p))
			}
			r = StartResult{Reason: "The installation could not be started."}
		}
	}()
	return g.B.StartInstall(o)
}

// Cancel stops the running install job (it returns at once; the job ends at its next read).
func (g *Bridge) Cancel() State { return g.protect("cancel", g.B.CancelInstall) }

// Uninstall takes the mod out of the selected game folder (see Backend.Uninstall).
func (g *Bridge) Uninstall() (r UninstallResult) {
	defer func() {
		if p := recover(); p != nil {
			if g.OnInternalError != nil {
				g.OnInternalError(fmt.Sprintf("uninstall: %v", p))
			}
			r = UninstallResult{Message: "The uninstall could not be finished.", Notes: []string{}}
		}
	}()
	return g.B.Uninstall()
}

// OpenEchoPatchPage opens EchoPatch's GitHub page in the user's web browser (the EchoPatch panel on step 01).
// The address is fixed in pcgame.EchoPatchURL; the page cannot ask for any other one.
func (g *Bridge) OpenEchoPatchPage() {
	defer func() {
		if p := recover(); p != nil && g.OnInternalError != nil {
			g.OnInternalError(fmt.Sprintf("open EchoPatch page: %v", p))
		}
	}()
	if g.OpenURL == nil {
		return
	}
	open := func() { g.OpenURL(pcgame.EchoPatchURL) }
	if g.Post != nil {
		g.Post(open) // outside the browser callback, like the dialogs
	} else {
		open()
	}
}

// Quit asks the host to close the window.
func (g *Bridge) Quit() {
	defer func() {
		if p := recover(); p != nil && g.OnInternalError != nil {
			g.OnInternalError(fmt.Sprintf("quit: %v", p))
		}
	}()
	if g.OnQuit != nil {
		g.OnQuit()
	}
}

// Snapshot returns the current state. The UI calls it now and then while a
// check is running, as a safety net in case a pushed update was swallowed.
func (g *Bridge) Snapshot() State { return g.B.Snapshot() }
