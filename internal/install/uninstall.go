package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fearinstaller/internal/archcfg"
)

// UninstallResult says what Uninstall did. Notes are plain sentences for the player: things that were left
// alone on purpose (a file another program changed after the install, files someone added to the mod folder).
type UninstallResult struct {
	Removed int      // mod files removed
	Notes   []string // plain language
}

// Uninstall takes the mod out of gameDir, using its record: the mod folder's own files, this mod's
// dinput8_hook.dll (the hook that was there before comes back from its backup), the Default.archcfg entry
// (the original comes back when nobody changed the file since; otherwise only the mod's line is removed),
// and a dinput8_hook.ini that was set aside comes back. Anything that changed after the install and is not
// the mod's is left as it is and named in Notes.
//
// The mod's files go first. If one of them cannot be removed, Uninstall stops before it changes anything
// else and keeps the record, so the folder is still seen as installed and Uninstall can simply run again.
// The record itself goes last.
func Uninstall(gameDir, mod string, gameRunning func() bool) (*UninstallResult, error) {
	if !validMod(mod) {
		return nil, fail("The installer is misconfigured (mod folder name).", nil)
	}
	if gameRunning != nil && gameRunning() {
		return nil, fail("F.E.A.R. is running. Close the game, then choose Uninstall again.", nil)
	}
	rec, err := ReadRecord(gameDir, mod)
	if err != nil {
		return nil, fail("The record of the installation could not be read, so nothing was removed.", err)
	}
	if rec == nil {
		return nil, fail("The mod is not installed in this folder (or was not installed by this installer), so nothing was removed.", nil)
	}
	path, cur, existed, _, err := archcfg.Read(gameDir)
	if err != nil {
		return nil, fail("Default.archcfg could not be read, so nothing was removed.", err)
	}
	res := &UninstallResult{}
	note := func(s string) { res.Notes = append(res.Notes, s) }

	// 1. The mod folder's own files (the record stays until the end).
	modDir := filepath.Join(gameDir, mod)
	var stuck []string
	for _, f := range rec.Files {
		p := filepath.Join(modDir, filepath.FromSlash(f.Path))
		if !inside(p, modDir) {
			continue // a damaged record must never reach outside the mod folder
		}
		if err := os.Remove(p); err == nil {
			res.Removed++
		} else if !errors.Is(err, fs.ErrNotExist) {
			stuck = append(stuck, f.Path)
		}
	}
	if len(stuck) > 0 {
		return nil, fail(fmt.Sprintf("%d of the mod's files could not be removed (the first is %s). Close any program that "+
			"may be using them, then choose Uninstall again. Nothing else was changed yet.", len(stuck), stuck[0]), nil)
	}

	// 2. Default.archcfg.
	bak := filepath.Join(gameDir, archcfg.BackupName)
	switch {
	case !existed:
		note("Default.archcfg was missing; nothing to restore there.")
	case sum(cur) == rec.Archcfg.Written && rec.Archcfg.Created:
		if err := os.Remove(path); err != nil {
			return nil, fail("Default.archcfg could not be restored. Choose Uninstall again to finish.", err)
		}
	case sum(cur) == rec.Archcfg.Written && rec.Archcfg.BackupMade && exists(bak):
		orig, err := os.ReadFile(bak)
		if err != nil {
			return nil, fail("The backup of Default.archcfg could not be read. Choose Uninstall again to finish.", err)
		}
		if err := writeAtomic(path, orig); err != nil {
			return nil, fail("Default.archcfg could not be restored. Choose Uninstall again to finish.", err)
		}
		os.Remove(bak)
	default:
		out, changed, err := archcfg.RemoveEntry(cur, mod)
		if err != nil {
			return nil, fail("Default.archcfg could not be updated. Choose Uninstall again to finish.", err)
		}
		if changed {
			if err := writeAtomic(path, out); err != nil {
				return nil, fail("Default.archcfg could not be updated. Choose Uninstall again to finish.", err)
			}
		}
		kept := map[bool]string{true: "; the backup Default.archcfg.CR_backup was kept.", false: "."}[exists(bak)]
		switch {
		case sum(cur) == rec.Archcfg.Written: // as the install left it, but its backup is gone
		case changed:
			note("Default.archcfg was changed after the install, so only the " + mod + " line was removed from it" + kept)
		case exists(bak):
			note("Default.archcfg was changed after the install and no longer loads " + mod + ", so it was left as it is" + kept)
		}
	}

	// 3. The hook.
	if hp := findFile(gameDir, HookName); hp != "" {
		b, err := os.ReadFile(hp)
		switch {
		case err != nil:
			note("dinput8_hook.dll could not be read, so it was left as it is.")
		case sum(b) != rec.Hook.SHA256 && !IsOurHook(b):
			note("dinput8_hook.dll was replaced by another mod after the install, so it was left as it is.")
		case rec.Hook.BackupMade && findFile(gameDir, HookBackup) != "":
			old, err := os.ReadFile(findFile(gameDir, HookBackup))
			if err != nil {
				return nil, fail("The backup of dinput8_hook.dll could not be read. Choose Uninstall again to finish.", err)
			}
			if err := writeAtomic(hp, old); err != nil {
				return nil, fail("dinput8_hook.dll could not be restored. Close F.E.A.R., then choose Uninstall again to finish.", err)
			}
			os.Remove(findFile(gameDir, HookBackup))
		default:
			if err := os.Remove(hp); err != nil {
				return nil, fail("dinput8_hook.dll could not be removed. Close F.E.A.R., then choose Uninstall again to finish.", err)
			}
		}
	}

	// 4. A dinput8_hook.ini that was set aside.
	if rec.Ini.Moved {
		if b := findFile(gameDir, IniBackup); b != "" {
			if findFile(gameDir, IniName) == "" {
				if err := os.Rename(b, filepath.Join(gameDir, IniName)); err != nil {
					note("dinput8_hook.ini could not be put back; it is still named " + IniBackup + ".")
				}
			} else {
				note("A new dinput8_hook.ini is in the game folder, so the old one stays as " + IniBackup + ".")
			}
		}
	}

	// 5. The record, then empty folders. Anything else in the mod folder stays.
	if err := os.Remove(filepath.Join(modDir, RecordFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fail("The install record could not be removed. Choose Uninstall again to finish.", err)
	}
	removeEmptyDirs(modDir)
	if exists(modDir) {
		var left []string
		filepath.WalkDir(modDir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(modDir, p)
				left = append(left, filepath.ToSlash(rel))
			}
			return nil
		})
		sort.Strings(left)
		if len(left) > 0 {
			more := ""
			if len(left) > 5 {
				more, left = " and others", left[:5]
			}
			note("The folder " + mod + " was kept because it holds files that are not the mod's: " + strings.Join(left, ", ") + more + ".")
		}
	}
	return res, nil
}

// inside reports whether p is below dir.
func inside(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// removeEmptyDirs removes dir and every folder below it that is empty, deepest first.
func removeEmptyDirs(dir string) {
	var dirs []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		os.Remove(d) // fails, harmlessly, when not empty
	}
}
