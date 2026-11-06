// Package install puts the mod into a F.E.A.R. folder and takes it out again.
//
// What an install changes in the game folder, and nothing else:
//   - one new folder, <Mod> (all converted content, plus CR_install.json, the record of this install);
//   - dinput8_hook.dll (this mod's hook; EchoPatch chain-loads it). A hook that was there before is kept as
//     dinput8_hook.dll.CR_backup;
//   - Default.archcfg gets <Mod> as its last mount (the original is kept once as Default.archcfg.CR_backup);
//   - only when the player agreed: an old dinput8_hook.ini is renamed to dinput8_hook.ini.CR_backup (an ini
//     overrides the hook's defaults, and this mod ships none).
//
// Order and safety: the content is built into <Mod>.CR_new first (same drive, so the swap is a rename), the
// game folder is only touched after the build succeeded, every step registers its undo, and any failure or
// cancel runs the undo list backwards, so the folder ends as it started. An update keeps the previous
// install as <Mod>.CR_old until the new one is complete. A <Mod> folder this installer did not make (no
// record inside) is never touched.
package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fearinstaller/internal/archcfg"
)

// Names in the game folder.
const (
	RecordFile  = "CR_install.json" // inside the mod folder
	HookName    = "dinput8_hook.dll"
	HookBackup  = "dinput8_hook.dll.CR_backup"
	IniName     = "dinput8_hook.ini"
	IniBackup   = "dinput8_hook.ini.CR_backup"
	stageSuffix = ".CR_new"
	oldSuffix   = ".CR_old"
	stageMarker = ".CR_staging" // first file of a staging folder: proves it is ours before it is deleted
	tmpSuffix   = ".CR_tmp"

	recordFormat = 1
)

// Error is a failure that can be shown to the player as it is (Msg), with detail for the log (Technical).
type Error struct {
	Msg       string
	Technical string
	Changed   bool // the game folder was left changed: some steps could not be undone
}

func (e *Error) Error() string {
	if e.Technical != "" {
		return e.Msg + " (" + e.Technical + ")"
	}
	return e.Msg
}

func fail(msg string, err error) *Error {
	e := &Error{Msg: msg}
	if err != nil {
		e.Technical = err.Error()
	}
	return e
}

// Request is one install.
type Request struct {
	GameDir string // the F.E.A.R. folder
	Mod     string // the mod folder name (one path element)
	// Build writes the mod's files into dir (the mod layout: dir\Worlds\..., dir\Database\...). It must check
	// ctx and return ctx.Err() when cancelled.
	Build func(ctx context.Context, dir string) error
	Hook  []byte // this mod's dinput8_hook.dll
	// RemoveIni: the player agreed to set an existing dinput8_hook.ini aside.
	RemoveIni bool
	// GameRunning reports whether F.E.A.R. is running (nil = not checked; tests).
	GameRunning func() bool
	Installer   string   // installer name and version, for the record
	Items       []string // what was chosen, for the record
	Log         func(string)
	Now         func() time.Time
}

// File is one file of the mod folder.
type File struct {
	Path   string `json:"path"` // '/' separated, below the mod folder
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Record is CR_install.json: what an install did, so an update or the uninstaller can undo exactly that.
type Record struct {
	Format    int      `json:"format"`
	Installer string   `json:"installer"`
	Date      string   `json:"date"`
	Mod       string   `json:"mod"`
	Items     []string `json:"items"`
	Files     []File   `json:"files"`
	Archcfg   struct {
		Created    bool   `json:"created"`    // Default.archcfg did not exist before the first install
		BackupMade bool   `json:"backupMade"` // Default.archcfg.CR_backup was made by this mod
		Written    string `json:"written"`    // SHA-256 of the file as this install left it
	} `json:"archcfg"`
	Hook struct {
		Found      string `json:"found"`      // before the first install: "absent", "ours" or "foreign"
		BackupMade bool   `json:"backupMade"` // dinput8_hook.dll.CR_backup was made by this mod
		SHA256     string `json:"sha256"`     // the hook this install wrote
	} `json:"hook"`
	Ini struct {
		Moved bool `json:"moved"` // dinput8_hook.ini was renamed to dinput8_hook.ini.CR_backup by this mod
	} `json:"ini"`
}

// ReadRecord returns the record of the install in gameDir\mod, or (nil, nil) when there is none.
func ReadRecord(gameDir, mod string) (*Record, error) {
	b, err := os.ReadFile(filepath.Join(gameDir, mod, RecordFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", RecordFile, err)
	}
	if r.Format != recordFormat || !strings.EqualFold(r.Mod, mod) {
		return nil, fmt.Errorf("%s is not a record this installer understands (format %d, mod %q)", RecordFile, r.Format, r.Mod)
	}
	return &r, nil
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func validMod(m string) bool {
	return m != "" && m != "." && m != ".." && !strings.ContainsAny(m, `\/:*?"<>|`+"\x00\r\n")
}

// findFile returns the path of name in dir in whatever letter case it exists ("" when absent).
func findFile(dir, name string) string {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.EqualFold(e.Name(), name) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// writeAtomic writes data next to path and renames it over path, then reads it back. A read-only attribute
// on path is cleared for the rename and put back after it (Windows refuses to replace a read-only file).
func writeAtomic(path string, data []byte) error {
	tmp := path + tmpSuffix
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if st, err := os.Lstat(path); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o200 == 0 {
		if err := os.Chmod(path, st.Mode().Perm()|0o200); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("%s is read-only and could not be made writable: %w", filepath.Base(path), err)
		}
		defer os.Chmod(path, st.Mode().Perm()) // put the attribute back, whatever happens below
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, data) {
		return errors.New("written but reading it back did not match")
	}
	return nil
}

// undoList runs registered undo steps in reverse order.
type undoList struct {
	steps []func() error
	names []string
	log   func(string)
}

func (u *undoList) add(name string, f func() error) {
	u.steps = append(u.steps, f)
	u.names = append(u.names, name)
}

// run undoes everything; it returns the steps that could not be undone.
func (u *undoList) run() []string {
	var failed []string
	for i := len(u.steps) - 1; i >= 0; i-- {
		if err := u.steps[i](); err != nil {
			failed = append(failed, u.names[i]+": "+err.Error())
			u.log("undo failed: " + u.names[i] + ": " + err.Error())
		} else {
			u.log("undone: " + u.names[i])
		}
	}
	return failed
}

// Install performs req. On success the record is returned; on any failure or cancel the game folder is put
// back as it was and the error says so (an *Error, or ctx.Err() when cancelled).
func Install(ctx context.Context, req Request) (rec *Record, err error) {
	logf := func(f string, a ...any) {
		if req.Log != nil {
			req.Log(fmt.Sprintf(f, a...))
		}
	}
	now := time.Now
	if req.Now != nil {
		now = req.Now
	}
	if !validMod(req.Mod) {
		return nil, fail("The installer is misconfigured (mod folder name).", fmt.Errorf("mod %q", req.Mod))
	}
	if req.Build == nil || len(req.Hook) == 0 {
		return nil, fail("The installer is misconfigured (no content builder or hook).", nil)
	}
	if st, e := os.Stat(req.GameDir); e != nil || !st.IsDir() {
		return nil, fail("The F.E.A.R. folder could not be found any more. Choose it again.", e)
	}
	if req.GameRunning != nil && req.GameRunning() {
		return nil, fail("F.E.A.R. is running. Close the game, then choose Install again.", nil)
	}

	game, mod := req.GameDir, req.Mod
	modDir := filepath.Join(game, mod)
	stage, old := modDir+stageSuffix, modDir+oldSuffix

	// An earlier install of this mod (update) or a folder of the same name that is not ours (refuse).
	var prev *Record
	if st, e := os.Lstat(modDir); e == nil {
		if !st.IsDir() {
			return nil, fail("There is a file named "+mod+" in the game folder. Rename or move it, then try again.", nil)
		}
		r, e := ReadRecord(game, mod)
		if e != nil || r == nil {
			return nil, fail("A folder named "+mod+" is already in the game folder, and it was not made by this installer. Rename or move it, then try again.", e)
		}
		prev = r
		logf("update: previous install from %s (%s)", r.Date, r.Installer)
	}
	for _, p := range []string{stage, old} {
		if !exists(p) {
			continue
		}
		// Leftovers of an interrupted run: ours only if they carry our marker or record.
		if exists(filepath.Join(p, stageMarker)) || exists(filepath.Join(p, RecordFile)) {
			if e := os.RemoveAll(p); e != nil {
				return nil, fail("A folder left over from an earlier installer run could not be removed: "+filepath.Base(p)+".", e)
			}
			logf("removed leftover %s", filepath.Base(p))
			continue
		}
		return nil, fail("A folder named "+filepath.Base(p)+" is in the game folder and was not made by this installer. Rename or move it, then try again.", nil)
	}

	u := &undoList{log: func(s string) { logf("%s", s) }}
	defer func() {
		// An unexpected failure (a panic) is undone like any other, so the folder still ends as it started.
		if p := recover(); p != nil {
			rec, err = nil, fail("The installer hit an unexpected problem and stopped.", fmt.Errorf("internal error: %v", p))
		}
		if err == nil {
			return
		}
		failed := u.run()
		if len(failed) > 0 {
			var ie *Error
			if errors.As(err, &ie) || errors.Is(err, context.Canceled) {
				err = &Error{Msg: "The installation stopped, and some of its changes could not be undone: " + strings.Join(failed, "; ") +
					". The installer's log, saved next to the installer, shows what happened.", Technical: err.Error(), Changed: true}
			}
		}
		rec = nil
	}()
	cancelled := func() error { return ctx.Err() }

	// 1. Build into the staging folder.
	if e := os.Mkdir(stage, 0o755); e != nil {
		return nil, fail("The installer could not create its work folder in the game folder. Check that the folder is not read-only and the drive has space.", e)
	}
	u.add("remove "+filepath.Base(stage), func() error { return os.RemoveAll(stage) })
	if e := os.WriteFile(filepath.Join(stage, stageMarker), nil, 0o644); e != nil {
		return nil, fail("The installer could not write in the game folder.", e)
	}
	logf("building into %s", filepath.Base(stage))
	if e := req.Build(ctx, stage); e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ie *Error
		if errors.As(e, &ie) {
			return nil, ie
		}
		return nil, fail("The content could not be built from your copies.", e)
	}
	if e := cancelled(); e != nil {
		return nil, e
	}
	if e := os.Remove(filepath.Join(stage, stageMarker)); e != nil {
		return nil, fail("The installer could not finish its work folder.", e)
	}
	files, e := listFiles(ctx, stage)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fail("The built content could not be checked.", e)
	}
	if len(files) == 0 {
		return nil, fail("No content was built from your copies.", nil)
	}
	for _, f := range files {
		if strings.EqualFold(f.Path, RecordFile) {
			return nil, fail("The installer is misconfigured (the content contains its record file).", nil)
		}
	}
	logf("built %d files", len(files))
	if e := cancelled(); e != nil {
		return nil, e
	}

	// From here on the game folder changes; every step has its undo.
	r := &Record{Format: recordFormat, Installer: req.Installer, Date: now().UTC().Format(time.RFC3339), Mod: mod,
		Items: append([]string{}, req.Items...), Files: files}
	if prev != nil { // what the first install found stays the truth for the uninstaller
		r.Archcfg.Created, r.Archcfg.BackupMade = prev.Archcfg.Created, prev.Archcfg.BackupMade
		r.Hook.Found, r.Hook.BackupMade = prev.Hook.Found, prev.Hook.BackupMade
		r.Ini.Moved = prev.Ini.Moved
	}

	// 2. Swap the mod folder in.
	if prev != nil {
		if e := os.Rename(modDir, old); e != nil {
			return nil, fail("The installed mod folder could not be replaced. Close any program that has a file from it open (or F.E.A.R.), then try again.", e)
		}
		u.add("put the previous "+mod+" back", func() error { return os.Rename(old, modDir) })
	}
	if e := os.Rename(stage, modDir); e != nil {
		return nil, fail("The new mod folder could not be put in place.", e)
	}
	u.add("take the new "+mod+" out", func() error { return os.Rename(modDir, stage) })
	logf("mod folder in place")

	// 3. The hook.
	hookPath := findFile(game, HookName)
	if hookPath == "" {
		hookPath = filepath.Join(game, HookName)
	}
	before, rerr := os.ReadFile(hookPath)
	had := rerr == nil
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return nil, fail("The existing dinput8_hook.dll could not be read.", rerr)
	}
	if prev == nil {
		switch {
		case !had:
			r.Hook.Found = "absent"
		case IsOurHook(before):
			r.Hook.Found = "ours"
		default:
			r.Hook.Found = "foreign"
		}
	}
	if !had || !bytes.Equal(before, req.Hook) {
		bak := filepath.Join(game, HookBackup)
		// Another mod's hook would be overwritten without a backup of its own: refuse instead.
		if had && prev == nil && !IsOurHook(before) && findFile(game, HookBackup) != "" {
			return nil, fail("dinput8_hook.dll belongs to another mod, and it cannot be backed up because "+HookBackup+
				" already exists. Move "+HookBackup+" out of the game folder, then try again.", nil)
		}
		if had && prev == nil && findFile(game, HookBackup) == "" {
			if e := os.WriteFile(bak, before, 0o644); e != nil {
				return nil, fail("The existing dinput8_hook.dll could not be backed up, so it was left alone.", e)
			}
			r.Hook.BackupMade = true
			u.add("remove "+HookBackup, func() error { return os.Remove(bak) })
			logf("backed up the existing hook (%s)", r.Hook.Found)
		}
		if e := writeAtomic(hookPath, req.Hook); e != nil {
			return nil, fail("dinput8_hook.dll could not be written. Close F.E.A.R. and any program using it, then try again.", e)
		}
		u.add("restore dinput8_hook.dll", func() error {
			if had {
				return writeAtomic(hookPath, before)
			}
			return os.Remove(hookPath)
		})
		logf("hook written")
	}
	r.Hook.SHA256 = sum(req.Hook)

	// 4. An old dinput8_hook.ini, only with the player's consent.
	if req.RemoveIni {
		if ini := findFile(game, IniName); ini != "" {
			bak := filepath.Join(game, IniBackup)
			if findFile(game, IniBackup) != "" {
				return nil, fail("dinput8_hook.ini could not be set aside: "+IniBackup+" already exists. Move one of them, then try again.", nil)
			}
			if e := os.Rename(ini, bak); e != nil {
				return nil, fail("dinput8_hook.ini could not be set aside.", e)
			}
			r.Ini.Moved = true
			u.add("put dinput8_hook.ini back", func() error { return os.Rename(bak, ini) })
			logf("set dinput8_hook.ini aside")
		}
	}

	// 5. Default.archcfg.
	cfgPath, cfgBefore, cfgExisted, _, e := archcfg.Read(game)
	if e != nil {
		return nil, fail("Default.archcfg could not be read.", e)
	}
	bakExisted := findFile(game, archcfg.BackupName) != ""
	res, e := archcfg.Apply(game, mod)
	if e != nil {
		return nil, fail("Default.archcfg could not be updated.", e)
	}
	if res.Changed {
		u.add("restore Default.archcfg", func() error {
			if !cfgExisted {
				return os.Remove(cfgPath)
			}
			return writeAtomic(cfgPath, cfgBefore)
		})
		if cfgExisted && !bakExisted {
			u.add("remove "+archcfg.BackupName, func() error { return os.Remove(filepath.Join(game, archcfg.BackupName)) })
		}
	}
	if prev == nil {
		r.Archcfg.Created = !cfgExisted
		r.Archcfg.BackupMade = cfgExisted && !bakExisted && res.Changed
	}
	_, cfgNow, _, _, e := archcfg.Read(game)
	if e != nil {
		return nil, fail("Default.archcfg could not be read back.", e)
	}
	r.Archcfg.Written = sum(cfgNow)
	logf("Default.archcfg: %s", res.Summary)
	if e := cancelled(); e != nil {
		return nil, e
	}

	// 6. The record, last: a mod folder with a record is a complete install.
	rb, _ := json.MarshalIndent(r, "", "  ")
	if e := writeAtomic(filepath.Join(modDir, RecordFile), append(rb, '\n')); e != nil {
		return nil, fail("The install record could not be written.", e)
	}
	if prev != nil {
		if e := os.RemoveAll(old); e != nil { // the install is complete; a leftover is removed by the next run
			logf("could not remove %s yet: %v", filepath.Base(old), e)
		}
	}
	logf("install complete: %d files", len(files))
	return r, nil
}

// listFiles returns every regular file below dir with size and SHA-256, sorted by path.
func listFiles(ctx context.Context, dir string) ([]File, error) {
	var out []File
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		rel, _ := filepath.Rel(dir, p)
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		out = append(out, File{Path: filepath.ToSlash(rel), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

// IsOurHook reports whether a dinput8_hook.dll is this mod's (any version): it carries the bonus-menu record
// names, which no other mod uses.
func IsOurHook(b []byte) bool { return bytes.Contains(b, []byte("zSFODD_Bonus_")) }
