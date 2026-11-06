package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"fearinstaller/internal/archcfg"
	"fearinstaller/internal/pcgame"
)

// Options are what the UI has chosen. UsePS3/UseX360 say which console copies take part (the UI sets them
// when a copy is picked). This build installs the complete mod only, so both copies are required and every
// item (both bonus missions, both weapons, Instant Action) is installed.
type Options struct {
	UsePS3  bool `json:"usePs3"`
	UseX360 bool `json:"useX360"`
	// RemoveHookIni: the player agreed to set an existing dinput8_hook.ini aside (it overrides the hook's
	// defaults, and this mod ships none). Never done without this.
	RemoveHookIni bool `json:"removeHookIni"`
}

// PlanGroup is one block of the summary of the player's choice.
type PlanGroup struct {
	Title  string   `json:"title"`
	Source string   `json:"source"` // "PlayStation 3", "Xbox 360" or "Both consoles"
	Items  []string `json:"items"`
}

// Plan says whether CONTINUE is allowed and what WOULD be installed. Nothing
// is written: this is only a description.
type Plan struct {
	Rev         int64    `json:"rev"` // the state revision this plan was computed from
	CanContinue bool     `json:"canContinue"`
	Blockers    []string `json:"blockers"`
	// Gates sorts the blockers by the step they belong to, so each step shows and enforces only its own:
	// Game = step 01, Copies = step 02, Content = step 03, Install = step 04. Blockers is their concatenation.
	Gates   Gates       `json:"gates"`
	Groups  []PlanGroup `json:"groups"`
	Reads   []string    `json:"reads"`   // what would be read
	Changes []string    `json:"changes"` // what would be written
	// Warnings are notes about the game folder that do not block (untested game
	// version, another mod's dinput8_hook.dll).
	Warnings []string `json:"warnings"`
	// Archcfg is the dry run of the Default.archcfg edit; nil without a valid game folder.
	Archcfg *ArchcfgPreview `json:"archcfg"`
	// ModDir is the full path of the folder the install creates inside the game folder ("" without a valid game folder).
	ModDir string `json:"modDir"`
	// Preview is true when this build cannot install the choice yet (ReadyToInstall): INSTALL then only reads
	// and checks the copies. PreviewWhy says why, in plain words ("" when Preview is false).
	Preview    bool   `json:"preview"`
	PreviewWhy string `json:"previewWhy"`
	// HookIni: a dinput8_hook.ini in the game folder turns some of the hook's features off (the mod ships none;
	// an ini that only repeats the defaults does not count), so the UI offers to set it aside
	// (Options.RemoveHookIni); it is never moved without that.
	HookIni bool `json:"hookIni"`
}

// Gates are the blockers of each step (see Plan.Gates). Every slice is non-nil.
type Gates struct {
	Game    []string `json:"game"`
	Copies  []string `json:"copies"`
	Content []string `json:"content"`
	Install []string `json:"install"`
}

// ArchcfgPreview is the dry run of the Default.archcfg change: what the file looks
// like now and what the installer would make of it. Nothing is written.
type ArchcfgPreview struct {
	Summary    string   `json:"summary"`
	Exists     bool     `json:"exists"`
	Encoding   string   `json:"encoding"`
	LineEnding string   `json:"lineEnding"`
	Entries    int      `json:"entries"`
	ReadOnly   bool     `json:"readOnly"`
	Changed    bool     `json:"changed"`
	Notes      []string `json:"notes"` // repairs, warnings, read-only handling
	Tail       []string `json:"tail"`  // the last lines of the new file
	Added      string   `json:"added"` // the line that is added (also the last element of Tail)
	Error      string   `json:"error"` // set when the file cannot be used; blocks CONTINUE
}

// ModFolder is the name of the mod's folder inside the game folder (mounted last in Default.archcfg).
const ModFolder = "FEAR_ConsoleRestoration"

// verified reports whether a console row is verified and idle.
func (s SourceState) verified() bool {
	return s.Phase == "done" && s.Result != nil && s.Result.Verified()
}

// Plan evaluates the options against the current state.
func (b *Backend) Plan(o Options) Plan {
	st := b.Snapshot()
	var p Plan
	p.Rev = st.Rev
	p.Blockers = []string{}
	p.Groups = []PlanGroup{}
	p.Warnings = []string{}

	g := Gates{Game: []string{}, Copies: []string{}, Content: []string{}, Install: []string{}}
	switch {
	case !st.PC.Valid && st.PC.Path == "" && len(st.PCChoices) >= 2:
		g.Game = append(g.Game, "Choose which F.E.A.R. installation to use (Steam or GOG).")
	case !st.PC.Valid:
		g.Game = append(g.Game, "Select your F.E.A.R. installation folder (Steam or GOG; the one with FEAR.exe).")
	case !st.PC.Ready:
		g.Game = append(g.Game, echoBlocker(st.PC.EchoPatch))
	}
	// Both copies are required: the shared database lists are built from both (content.Choice.Complete).
	block := func(use bool, name string, s SourceState) {
		switch {
		case !use:
			g.Copies = append(g.Copies, "Select your "+name+" copy.")
		case s.Phase == "checking":
			g.Copies = append(g.Copies, "Still checking your "+name+" copy.")
		case s.Phase == "" || s.Phase == "idle":
			g.Copies = append(g.Copies, "Select your "+name+" copy.")
		case !s.verified():
			msg := "Your " + name + " copy is not verified"
			if s.Result != nil {
				msg += ": " + strings.TrimSuffix(s.Result.Message, ".") + "."
			}
			g.Copies = append(g.Copies, msg)
		}
	}
	block(o.UsePS3, "PlayStation 3", st.PS3)
	block(o.UseX360, "Xbox 360", st.X360)

	ps3 := o.UsePS3 && st.PS3.verified()
	x360 := o.UseX360 && st.X360.verified()
	// Disc images are checked like folders, but the content is built only from extracted folders.
	for _, c := range []struct {
		ok   bool
		name string
		s    SourceState
	}{{ps3, "PlayStation 3", st.PS3}, {x360, "Xbox 360", st.X360}} {
		if c.ok && c.s.Result.Container != "folder" {
			g.Install = append(g.Install, "Your "+c.name+" copy is a disc image. Extract it to a folder and select that folder.")
		}
	}
	if ps3 {
		p.Groups = append(p.Groups,
			PlanGroup{"Bonus mission: SFOD-D", "PlayStation 3", []string{"The SFOD-D level, its voices and its loading screen"}},
			PlanGroup{"Watson SAS-12 shotgun", "PlayStation 3", []string{"Found in 11 places in the campaign"}})
		p.Reads = append(p.Reads, "PlayStation 3 copy: "+filepath.FromSlash(dataRoot(st.PS3)))
	}
	if x360 {
		p.Groups = append(p.Groups,
			PlanGroup{"Bonus mission: Holiday", "Xbox 360", []string{"The Holiday level and its voices"}},
			PlanGroup{"SM15 machine pistol", "Xbox 360", []string{"Found in 79 places in the campaign"}},
			PlanGroup{"Instant Action", "Xbox 360", []string{"Four console maps"}},
			PlanGroup{"Xbox 360 HUD", "Xbox 360", []string{"Switch it in Options > Game > HUD layout"}})
		p.Reads = append(p.Reads, "Xbox 360 copy: "+filepath.FromSlash(dataRoot(st.X360)))
	}
	if ps3 && x360 { // built from the two copies together: shown as a third column on the review page
		p.Groups = append(p.Groups,
			PlanGroup{"Bonus mission menu", "Both consoles", []string{"Main Menu > Bonus mission"}},
			PlanGroup{"Console-style objectives", "Both consoles", []string{"Objectives shown as on the consoles"}},
			PlanGroup{"Bonus mission fixes", "Both consoles", []string{"Fixes for the two bonus missions"}})
	}
	if st.PC.Valid {
		g0 := filepath.FromSlash(st.PC.Path)
		where := "F.E.A.R. folder"
		if st.PC.EditionLabel != "" {
			where += " (" + st.PC.EditionLabel + ")"
		}
		p.Reads = append(p.Reads, where+": "+g0, "Default.archcfg in that folder (to preview the change)")
		p.Warnings = append(p.Warnings, st.PC.Warnings...)
		p.Archcfg = previewArchcfg(g0)
		if p.Archcfg.Error != "" {
			g.Install = append(g.Install, "Default.archcfg in the game folder cannot be used: "+p.Archcfg.Error)
		}
		// An installed mod is never installed over: the page takes the player from step 01 straight to Play, and
		// Uninstall is the way to a fresh install. This gate keeps StartInstall refusing even if step 03 is reached.
		if st.ModInstalled {
			g.Install = append(g.Install, "The mod is already installed. Uninstall it first to install it again.")
		}
		p.ModDir = filepath.Join(g0, ModFolder)
		// The mod ships the hook only, never a dinput8_hook.ini (every feature is on by default without one).
		hook := "Add dinput8_hook.dll next to EchoPatch's dinput8.dll (EchoPatch loads it)"
		switch st.PC.Hook {
		case "foreign":
			hook = "Back up the dinput8_hook.dll that is already there (dinput8_hook.dll.CR_backup), then add this mod's dinput8_hook.dll"
		case "ours":
			hook = "Replace this mod's dinput8_hook.dll with the current one"
		}
		p.HookIni = st.PC.HookIni
		bak := "Back up Default.archcfg (once, as Default.archcfg.CR_backup). " + p.Archcfg.Summary
		p.Changes = []string{
			"Create one new folder: " + filepath.Join(g0, ModFolder) + " (all converted game data goes here)",
			bak,
			hook,
		}
		// An existing ini is shown with its own consent checkbox (HookIni), so it is no warning here.
		if st.PC.HookIni && o.RemoveHookIni {
			p.Changes = append(p.Changes, "Move dinput8_hook.ini aside (dinput8_hook.ini.CR_backup) so its settings stop overriding the mod's defaults")
		}
		p.Changes = append(p.Changes, "The game's own files are not changed, apart from the Default.archcfg line and the backups above. Uninstalling puts everything back.")
	}
	p.Gates = g
	p.Blockers = append(p.Blockers, g.Game...)
	p.Blockers = append(p.Blockers, g.Copies...)
	p.Blockers = append(p.Blockers, g.Content...)
	p.Blockers = append(p.Blockers, g.Install...)
	p.CanContinue = len(p.Blockers) == 0
	if ok, why := ReadyToInstall(requestFor(st, o)); !ok {
		p.Preview, p.PreviewWhy = true, why
	}
	return p
}

// previewArchcfg is the read-only dry run of the Default.archcfg change.
func previewArchcfg(gameDir string) (pv *ArchcfgPreview) {
	pv = &ArchcfgPreview{Notes: []string{}, Tail: []string{}}
	defer func() {
		if r := recover(); r != nil {
			pv = &ArchcfgPreview{Notes: []string{}, Tail: []string{}, Error: "it could not be read."}
		}
	}()
	res, ro, err := archcfg.Preview(gameDir, ModFolder)
	if err != nil {
		switch {
		case errors.Is(err, archcfg.ErrNotText):
			pv.Error = "it is not a text file, so the installer will not touch it."
		case errors.Is(err, archcfg.ErrTooLarge):
			pv.Error = "it is much larger than expected, so the installer will not touch it."
		default:
			pv.Error = "it could not be read (" + strings.TrimSpace(err.Error()) + ")."
		}
		pv.Summary = "Default.archcfg cannot be used."
		return pv
	}
	pv.Summary, pv.Exists, pv.Encoding, pv.LineEnding = res.Summary, res.Existed, string(res.Encoding), res.LineEnding
	pv.Entries, pv.ReadOnly, pv.Changed = res.Entries, ro, res.Changed
	pv.Notes = append(pv.Notes, res.Repairs...)
	pv.Notes = append(pv.Notes, res.Warnings...)
	if ro {
		pv.Notes = append(pv.Notes, "The file is read-only; the installer clears that for the change and sets it back afterwards.")
	}
	if res.Existed {
		pv.Notes = append(pv.Notes, fmt.Sprintf("Kept as it is: %s, %s line breaks, %d existing entries.", res.Encoding, res.LineEnding, res.Entries))
	}
	n := len(res.After)
	from := n - 4
	if from < 0 {
		from = 0
	}
	pv.Tail = append(pv.Tail, res.After[from:]...)
	if res.Added {
		pv.Added = ModFolder
	}
	return pv
}

// echoBlocker is the one-line reason shown under CONTINUE; the details and the
// how-to are in the EchoPatch notice next to the folder box.
func echoBlocker(s pcgame.EchoPatchState) string {
	switch s {
	case pcgame.EchoPatchForeign:
		return "EchoPatch is required: the dinput8.dll in the game folder is not EchoPatch."
	case pcgame.EchoPatchIniOnly:
		return "EchoPatch is required: its dinput8.dll is missing from the game folder."
	case pcgame.EchoPatchWrapper:
		return "EchoPatch is required: the dinput8.dll in the game folder is GOG's own input wrapper, not EchoPatch."
	}
	return "EchoPatch is required: it is not installed in the game folder."
}
