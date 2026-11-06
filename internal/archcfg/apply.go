package archcfg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// File and backup names.
const (
	FileName   = "Default.archcfg"
	BackupName = "Default.archcfg.CR_backup"
)

// Read loads Default.archcfg from the game folder (any letter case of the file
// name). A missing file is not an error: existed is false. It only reads.
func Read(gameDir string) (path string, content []byte, existed bool, readOnly bool, err error) {
	path = filepath.Join(gameDir, FileName)
	if ents, e := os.ReadDir(gameDir); e == nil {
		for _, en := range ents {
			if !en.IsDir() && strings.EqualFold(en.Name(), FileName) {
				path = filepath.Join(gameDir, en.Name())
			}
		}
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, nil, false, false, nil
		}
		return path, nil, false, false, err
	}
	defer f.Close()
	if st, e := f.Stat(); e == nil {
		if st.IsDir() {
			return path, nil, false, false, errors.New("Default.archcfg is a folder")
		}
		readOnly = st.Mode().Perm()&0o200 == 0
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return path, nil, true, readOnly, err
	}
	if len(b) > MaxSize {
		return path, nil, true, readOnly, ErrTooLarge
	}
	return path, b, true, readOnly, nil
}

// Preview reads the file in gameDir and computes what Apply would write. It
// never writes. The second result says whether the file is read-only.
func Preview(gameDir, mod string) (Result, bool, error) {
	_, b, existed, ro, err := Read(gameDir)
	if err != nil {
		return Result{}, ro, err
	}
	r, err := Compute(b, existed, Options{Mod: mod, Stock: StockOrder(archiveNames(gameDir))})
	return r, ro, err
}

func archiveNames(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".arch00") {
			out = append(out, e.Name())
		}
	}
	return out
}

// RemoveEntry returns orig without the mod's line(s) (the uninstaller uses it when Default.archcfg changed
// after the install, so the original cannot simply come back). Every other line, comment and blank line, the
// encoding and the dominant line ending are kept. changed is false when the mod was not listed.
func RemoveEntry(orig []byte, mod string) (out []byte, changed bool, err error) {
	if len(orig) > MaxSize {
		return nil, false, ErrTooLarge
	}
	text, enc, _, err := decode(orig)
	if err != nil {
		return nil, false, err
	}
	lines, eol, _ := splitLines(text)
	kept := lines[:0:0]
	for _, l := range lines {
		if t := trim(l); t != "" && !isComment(t) && sameEntry(t, mod) {
			changed = true
			continue
		}
		kept = append(kept, l)
	}
	if !changed {
		return orig, false, nil
	}
	sep := "\r\n"
	if eol == "LF" {
		sep = "\n"
	}
	return encode(strings.Join(kept, sep), enc), true, nil
}

// Apply writes the new Default.archcfg. The original is copied to
// Default.archcfg.CR_backup first, once:
// an existing backup is never overwritten, so it always holds the pristine
// file. A read-only attribute is cleared for the write and put back after.
// The new content is written to a temporary file next to the original and
// moved over it, so a failure leaves the old file in place.
func Apply(gameDir, mod string) (Result, error) {
	path, orig, existed, ro, err := Read(gameDir)
	if err != nil {
		return Result{}, err
	}
	r, err := Compute(orig, existed, Options{Mod: mod, Stock: StockOrder(archiveNames(gameDir))})
	if err != nil {
		return r, err
	}
	if !r.Changed {
		return r, nil
	}
	if existed {
		bak := filepath.Join(gameDir, BackupName)
		if _, e := os.Stat(bak); errors.Is(e, os.ErrNotExist) {
			if err := os.WriteFile(bak, orig, 0o644); err != nil {
				return r, fmt.Errorf("could not back up Default.archcfg: %w", err)
			}
		}
	}
	mode := os.FileMode(0o644)
	if st, e := os.Stat(path); e == nil {
		mode = st.Mode().Perm()
	}
	if ro {
		if err := os.Chmod(path, mode|0o200); err != nil {
			return r, fmt.Errorf("Default.archcfg is read-only and could not be made writable: %w", err)
		}
	}
	tmp := path + ".CR_tmp"
	if err := os.WriteFile(tmp, r.Content, mode|0o200); err != nil {
		if ro {
			os.Chmod(path, mode)
		}
		return r, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		if ro {
			os.Chmod(path, mode)
		}
		return r, err
	}
	if ro {
		os.Chmod(path, mode)
	}
	if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, r.Content) {
		return r, errors.New("Default.archcfg was written but reading it back did not match")
	}
	return r, nil
}
