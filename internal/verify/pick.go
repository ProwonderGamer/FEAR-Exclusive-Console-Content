package verify

import (
	"os"
	"path/filepath"
	"strings"

	"fearinstaller/internal/vfs"
)

// Players select all sorts of folders for a console copy: the copy itself, a folder inside it (the Xbox 360
// Game folder, a PS3 USRDIR...) or a folder that holds it, at any depth and under any name. The copy is found
// from what is on disk only: the file names inside the copy itself, never a drive, a folder name the player
// chose or any other fixed location. Downward searches live in findX360Root / findPS3Root; climb looks upward.

// maxClimb is how many folders above the selection are looked at: enough for a pick as deep as
// PS3_GAME/USRDIR/game/worlds/release (five levels below the PS3 disc folder).
const maxClimb = 6

// climb returns the folder to check for a selection that lies inside a copy: the nearest folder above it that
// is a copy's root (isRoot) and holds the selection on that copy's own path (isRoot's chain, compared without
// regard to case; a selection deeper inside the data is fine too). When the selection is already a root, is a
// file (a disc image) or lies inside no copy, path comes back unchanged.
func climb(path string, isRoot func(dir string) (chain []string, ok bool)) string {
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return path
	}
	if _, ok := isRoot(path); ok {
		return path
	}
	sel := filepath.Clean(path)
	dir := sel
	for i := 0; i < maxClimb; i++ {
		up := filepath.Dir(dir)
		if up == dir { // reached the top of the drive
			break
		}
		dir = up
		chain, ok := isRoot(dir)
		if !ok {
			continue
		}
		if rel, err := filepath.Rel(dir, sel); err == nil && onChain(strings.Split(rel, string(filepath.Separator)), chain) {
			return dir
		}
	}
	return path
}

// onChain reports whether rel runs along chain: every part of rel that chain covers matches it.
func onChain(rel, chain []string) bool {
	for i, part := range rel {
		if i >= len(chain) {
			return true // deeper inside the data folder
		}
		if !strings.EqualFold(part, chain[i]) {
			return false
		}
	}
	return true
}

// x360RootAt reports whether dir is an Xbox 360 F.E.A.R. disc folder (default.xex next to F.E.A.R.'s Game data).
func x360RootAt(dir string) ([]string, bool) {
	fsys, err := vfs.NewDirFS(dir)
	if err != nil {
		return nil, false
	}
	if e, err := fsys.Stat("default.xex"); err != nil || e.IsDir || !looksLikeFEARX360(fsys, "") {
		return nil, false
	}
	return []string{"Game"}, true
}

// ps3RootAt reports whether dir is a PS3 F.E.A.R. disc folder or its PS3_GAME folder: the two levels that hold
// PARAM.SFO, so the disc's title ID can be checked.
func ps3RootAt(dir string) ([]string, bool) {
	fsys, err := vfs.NewDirFS(dir)
	if err != nil {
		return nil, false
	}
	for _, lay := range ps3Layouts[:2] {
		if isPS3Data(located{fsys: fsys, prefix: lay.base}) && !isX360Data(fsys, lay.base) {
			return strings.Split(lay.base, "/"), true
		}
	}
	return nil, false
}

// inPS3Copy reports whether a folder is a PS3 copy's disc or PS3_GAME folder, or lies inside one.
func inPS3Copy(path string) bool {
	if _, ok := ps3RootAt(path); ok {
		return true
	}
	return climb(path, ps3RootAt) != path
}

// CopyFolder is the folder the checks open for a selection: the copy's root when the selection lies inside a
// copy, else the selection itself. Paths inside a Result (ArtPath) are relative to it.
func CopyFolder(kind, path string) string {
	switch kind {
	case "ps3":
		return climb(path, ps3RootAt)
	case "x360":
		return climb(path, x360RootAt)
	}
	return path
}

// diskRoot is the folder on disk that holds the located data; "" for a disc image.
func diskRoot(fsys vfs.FS, prefix string) string {
	d, ok := fsys.(*vfs.DirFS)
	if !ok {
		return ""
	}
	p, err := d.RealPath(prefix)
	if err != nil {
		return ""
	}
	return p
}
