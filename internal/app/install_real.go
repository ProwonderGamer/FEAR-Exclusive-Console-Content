package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"fearinstaller/internal/content"
	"fearinstaller/internal/install"
	"fearinstaller/internal/pcgame"
)

// RealInstaller is the Installer that writes: it builds the content from the player's copies straight into a
// work folder in the game folder and swaps it in (internal/install: undo on any failure, a record for update
// and uninstall). The Backend only uses it when the installer can make every file the choice needs
// (ReadyToInstall); otherwise the job only reads and checks (DryRunInstaller), so a partial mod is never written.
type RealInstaller struct {
	GameRunning func() bool // nil = install.GameRunning
	Hook        []byte      // nil = BuildHook from the player's copies
	Log         func(string)
}

// IsPreview reports that this installer writes.
func (*RealInstaller) IsPreview() bool { return false }

// choiceOf is the content a request installs: the complete mod, once both copies are given (the same rule as
// Plan). With only one copy the choice is empty, so ReadyToInstall never calls it ready.
func choiceOf(o Options, ps3, x360 bool) content.Choice {
	ps3, x360 = ps3 && o.UsePS3, x360 && o.UseX360
	if !ps3 || !x360 {
		return content.Choice{HavePS3: ps3, HaveX360: x360}
	}
	return content.Choice{SFOD: true, SAS12: true, Holiday: true, SM15: true, IA: true, HavePS3: true, HaveX360: true}
}

// ReadyToInstall says whether the installer can really install a request; the reason is plain
// language for the preview banner when it cannot.
func ReadyToInstall(req InstallRequest) (bool, string) {
	ch := choiceOf(req.Options, req.PS3Path != "", req.X360 != "")
	built, needed := content.Coverage(ch)
	if needed == 0 { // nothing chosen yet (or no verified copy): an empty choice is never "ready"
		return false, ""
	}
	if built < needed {
		return false, fmt.Sprintf("This version of the installer can build %d of the %d files your choice needs, so it only reads and checks your copies.", built, needed)
	}
	if !ch.Complete() {
		return false, "This version of the installer installs only the complete mod from both console copies. For a smaller choice it only reads and checks your copies."
	}
	for _, p := range []string{req.PS3Path, req.X360} {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			return false, "The installer cannot install from a disc image, so it only reads and checks your copies. Extract the disc to a folder and select that folder to install."
		}
	}
	return true, ""
}

// Run implements Installer.
func (r *RealInstaller) Run(ctx context.Context, req InstallRequest, report func(InstallProgress)) error {
	if ok, why := ReadyToInstall(req); !ok {
		return &InstallError{Msg: "This version of the installer cannot install your choice.", Technical: why}
	}
	ch := choiceOf(req.Options, req.PS3Path != "", req.X360 != "")
	// Progress is the number of mod files built so far out of the files the choice needs: the build is
	// nearly all of the work (moving the folder in, the hook and Default.archcfg take a moment). Every report
	// carries the count, so a stage message never resets the bar.
	total, built := len(content.ModFiles(ch)), 0
	rep := func(p InstallProgress) {
		if report != nil {
			p.Files, p.FilesDone = total, min(built, total)
			report(p)
		}
	}
	var weapons, items []string
	for _, w := range []struct {
		on        bool
		item, wpn string
	}{{ch.SFOD, "sfod", ""}, {ch.Holiday, "holiday", ""}, {ch.SAS12, "sas12", "watson"}, {ch.SM15, "sm15", "sm15"}, {ch.IA, "ia", ""}} {
		if w.on {
			items = append(items, w.item)
			if w.wpn != "" {
				weapons = append(weapons, w.wpn)
			}
		}
	}
	build := func(ctx context.Context, dir string) error {
		rep(InstallProgress{Phase: PhasePrepare, Message: "Building the content from your copies…"})
		res, err := content.Build(content.Options{PCDir: req.GameDir, PS3Dir: req.PS3Path, X360Dir: req.X360, OutDir: dir,
			ModLayout: true, Weapons: weapons, InstantActionText: ch.IA, WatsonReload: ch.SAS12, Choice: ch,
			Progress: func(s string) { rep(InstallProgress{Phase: PhasePrepare, Message: s + "…"}) },
			FileDone: func(rel string) { built++; rep(InstallProgress{Phase: PhasePrepare, File: rel}) }})
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Every file the choice needs must have been made.
		made := map[string]bool{}
		for _, f := range res.Files {
			made[strings.ToLower(f.Rel)] = true
		}
		var missing []string
		for _, f := range content.ModFiles(ch) {
			if !made[strings.ToLower(f.Path)] {
				missing = append(missing, f.Path)
			}
		}
		if len(missing) > 0 {
			return &install.Error{Msg: "Some of the content could not be built from your copies.",
				Technical: fmt.Sprintf("%d files missing, first %s", len(missing), missing[0])}
		}
		return nil
	}
	running := r.GameRunning
	if running == nil {
		running = install.GameRunning
	}
	rep(InstallProgress{Phase: PhasePrepare, Message: "Getting ready…"})
	hook := r.Hook
	if hook == nil {
		var err error
		if hook, err = BuildHook(req.GameDir, req.X360); err != nil {
			return &InstallError{Msg: "The content could not be built from your copies. Nothing was changed.", Technical: "hook: " + err.Error()}
		}
	}
	_, err := install.Install(ctx, install.Request{GameDir: req.GameDir, Mod: ModFolder, Build: build, Hook: hook,
		// The ini is moved only when the player agreed AND it still turns something off (the box is shown only then).
		RemoveIni: req.Options.RemoveHookIni && pcgame.HookIniTurnsOff(req.GameDir), GameRunning: running, Installer: "F.E.A.R. Exclusive Console Content installer",
		Items: items, Log: r.Log})
	if err == nil {
		rep(InstallProgress{Phase: PhaseFinish, Message: "Installed."})
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	var ie *install.Error
	if errors.As(err, &ie) {
		msg := ie.Msg
		if !ie.Changed { // every step was undone (or none had been made)
			msg += " Nothing was changed."
		}
		return &InstallError{Msg: msg, Technical: ie.Technical}
	}
	return &InstallError{Msg: "The installation stopped. Nothing was changed.", Technical: err.Error()}
}
