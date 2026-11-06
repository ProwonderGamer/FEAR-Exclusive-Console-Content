package pcgame

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GOGGame is one entry of HKLM\SOFTWARE\WOW6432Node\GOG.com\Games\<id> (read in
// the 32-bit registry view). GOG's installers and Galaxy write one key per
// product; the values used here are gameID, gameName, path, exe and dependsOn.
// GameFinder confirms gameID, gameName, path, buildId and dependsOn; "exe" is
// used only as an extra hint (an absent value is fine).
type GOGGame struct {
	ID        string // key name (the numeric product id)
	Name      string // gameName
	Path      string // path: the install folder
	Exe       string // exe: full path or file name of the launcher
	DependsOn string // dependsOn: parent product id, for add-ons
}

// Install is one F.E.A.R. installation that was found automatically.
type Install struct {
	Path   string `json:"path"`
	Store  string `json:"store"`  // "steam" or "gog"
	Source string `json:"source"` // how it was found, for the user's information
}

// Sources are the places auto-detection looks at. SystemSources fills them in on
// the running machine; tests fill them by hand.
type Sources struct {
	SteamRoots []string  // Steam installation folders
	GOGGames   []GOGGame // registry keys of GOG products
	GOGRoots   []string  // folders that hold one sub-folder per GOG game (Galaxy library, "GOG Games")
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// normName lower-cases a title and keeps letters and digits, so that
// "F.E.A.R. Platinum" and "FEAR Platinum" compare equal.
func normName(s string) string { return nonWord.ReplaceAllString(strings.ToLower(s), " ") }

// looksLikeFEARName tells the base game's folder or product name from other
// things. "F.E.A.R. Platinum Collection", "F.E.A.R.", "FEAR Ultimate Shooter
// Edition" qualify; the expansions and the sequels do not.
func looksLikeFEARName(s string) bool {
	n := strings.TrimSpace(normName(s))
	n = strings.Join(strings.Fields(strings.ReplaceAll(n, "f e a r", "fear")), " ")
	if n != "fear" && !strings.HasPrefix(n, "fear ") {
		return false
	}
	for _, bad := range []string{"extraction point", "perseus mandate", " xp", " 2", " 3", "project origin", "online", "combat", "server"} {
		if strings.Contains(" "+n, bad) {
			return false
		}
	}
	return true
}

// isExpansionExe is true for the launchers of the two expansions.
func isExpansionExe(exe string) bool {
	b := strings.ToLower(filepath.Base(strings.ReplaceAll(exe, `\`, "/")))
	switch b {
	case "fearxp.exe", "fearxp2.exe", "configxp.exe", "configxp2.exe", "fearmp.exe", "fearserver.exe":
		return true
	}
	return false
}

// GOGProductID is the GOG product id of "F.E.A.R. Platinum Collection" (base game; the
// expansions are play tasks of the same product). Confirmed from goggame-1423058413.info
// of a real Galaxy install, whose primary play task is FEAR.exe.
const GOGProductID = "1423058413"

// gogCandidateDirs returns the folders worth probing for one registry entry, or
// nil when the entry is not the base game (an expansion launcher, another game).
func gogCandidateDirs(g GOGGame) []string {
	exeBase := strings.ToLower(filepath.Base(strings.ReplaceAll(g.Exe, `\`, "/")))
	if isExpansionExe(g.Exe) {
		return nil
	}
	if !(g.ID == GOGProductID || exeBase == "fear.exe" || looksLikeFEARName(g.Name)) {
		return nil
	}
	var out []string
	if g.Path != "" {
		out = append(out, g.Path)
	}
	if exeBase == "fear.exe" && (strings.Contains(g.Exe, `\`) || strings.Contains(g.Exe, "/")) {
		out = append(out, filepath.Dir(filepath.FromSlash(strings.ReplaceAll(g.Exe, `\`, "/"))))
	}
	return out
}

// resolveGameDir returns the folder that really holds FEAR.exe: dir itself; its
// parent when dir is an expansion folder; or a direct sub-folder named like the
// game (a library path that points one level too high). "" when there is none.
func resolveGameDir(dir string) string {
	if dir == "" {
		return ""
	}
	if findFile(dir, "FEAR.exe") != "" {
		return dir
	}
	if findFile(dir, "FEARXP.exe") != "" || findFile(dir, "FEARXP2.exe") != "" {
		if p := filepath.Dir(dir); p != dir && findFile(p, "FEAR.exe") != "" {
			return p
		}
	}
	if ents, err := os.ReadDir(dir); err == nil {
		n := 0
		for _, e := range ents {
			if n++; n > 500 {
				break
			}
			if e.IsDir() && looksLikeFEARName(e.Name()) && findFile(filepath.Join(dir, e.Name()), "FEAR.exe") != "" {
				return filepath.Join(dir, e.Name())
			}
		}
	}
	return ""
}

// FindGOGInstalls returns the F.E.A.R. folders of GOG installations. Registry
// entries are matched by game name or launcher; library roots are searched for
// sub-folders named like the game. Every result is checked by content (it must
// contain FEAR.exe), so a wrong guess costs nothing.
func FindGOGInstalls(games []GOGGame, roots []string) []Install {
	var out []Install
	seen := map[string]bool{}
	add := func(dir, source string) {
		d := resolveGameDir(dir)
		if d == "" {
			return
		}
		k := dedupeKey(d)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, Install{Path: filepath.Clean(d), Store: "gog", Source: source})
	}
	for _, g := range games {
		for _, d := range gogCandidateDirs(g) {
			add(d, "GOG registry")
		}
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		ents, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		n := 0
		for _, e := range ents {
			if n++; n > 2000 {
				break
			}
			if e.IsDir() && looksLikeFEARName(e.Name()) {
				add(filepath.Join(root, e.Name()), "GOG games folder")
			}
		}
	}
	return out
}

// dedupeKey identifies a folder for the purpose of listing it once.
func dedupeKey(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return strings.ToLower(filepath.Clean(p))
}

// DetectAll finds every installation: Steam first, then GOG, each listed once.
func DetectAll(s Sources) []Install {
	var out []Install
	seen := map[string]bool{}
	if d := FindSteamInstall(s.SteamRoots); d != "" {
		seen[dedupeKey(d)] = true
		out = append(out, Install{Path: filepath.Clean(d), Store: "steam", Source: "Steam library"})
	}
	for _, g := range FindGOGInstalls(s.GOGGames, s.GOGRoots) {
		if k := dedupeKey(g.Path); !seen[k] {
			seen[k] = true
			out = append(out, g)
		}
	}
	return out
}

// DefaultGOGRoots lists the usual folders that hold GOG games, built from the
// program-files folders and the drive roots given. libraryExtra are library
// folders learnt from the registry (Galaxy's client folder, library path).
func DefaultGOGRoots(programFiles []string, driveRoots []string, libraryExtra []string) []string {
	var roots []string
	for _, pf := range programFiles {
		if pf != "" {
			roots = append(roots, filepath.Join(pf, "GOG Galaxy", "Games"), filepath.Join(pf, "GOG Games"))
		}
	}
	for _, d := range driveRoots {
		if d == "" {
			continue
		}
		roots = append(roots, filepath.Join(d, "GOG Games"), filepath.Join(d, "GOG Galaxy", "Games"), filepath.Join(d, "Games", "GOG Games"), filepath.Join(d, "Games", "GOG Galaxy", "Games"))
	}
	roots = append(roots, libraryExtra...)
	// remove duplicates, keep order
	seen := map[string]bool{}
	var out []string
	for _, r := range roots {
		k := strings.ToLower(filepath.Clean(r))
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}
