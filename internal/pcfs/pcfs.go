// Package pcfs presents the stock PC game archives (*.Arch00 in the game folder) as one read-only virtual
// file system, the way the engine mounts them: the archive mounted last wins.
//
// The mount order is the stock Default.archcfg order (see ArchOrder). It also provides the "table of
// contents" (case-insensitive path -> canonical path) that the database merger uses to decide which
// resource files exist on PC.
package pcfs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fearinstaller/internal/ltar"
)

// ArchOrder is the mount order of the stock archives; the last archive providing a file wins.
var ArchOrder = func() []string {
	o := []string{"FEAR", "FEARA", "FEARL", "FEARE"}
	for i := 1; i <= 8; i++ {
		for _, b := range []string{"FEAR", "FEARA", "FEARL", "FEARE"} {
			o = append(o, fmt.Sprintf("%s_%d", b, i))
		}
	}
	return o
}()

func rankOf(arch string) int {
	a := strings.ToLower(strings.TrimSuffix(strings.ToLower(arch), ".arch00"))
	for i, n := range ArchOrder {
		if strings.ToLower(n) == a {
			return i
		}
	}
	return -1
}

type tocEntry struct {
	rank  int
	canon string
	arch  string
}

// TOC maps lower-case paths to the winning archive entry.
type TOC struct {
	files map[string]tocEntry
}

// NewTOC returns an empty table.
func NewTOC() *TOC { return &TOC{files: make(map[string]tocEntry)} }

// Add records one file of an archive (arch is the archive file name, e.g. "FEAR_8.Arch00").
func (t *TOC) Add(arch, path string) {
	k := rankOf(arch)
	lp := strings.ToLower(path)
	if e, ok := t.files[lp]; !ok || k >= e.rank {
		t.files[lp] = tocEntry{k, path, arch}
	}
}

// Find returns the canonical (archive) spelling of path, or "" if no archive has it.
func (t *TOC) Find(path string) string {
	if e, ok := t.files[strings.ToLower(path)]; ok {
		return e.canon
	}
	return ""
}

// Archive returns the name of the archive that wins for path ("" if absent).
func (t *TOC) Archive(path string) string {
	if e, ok := t.files[strings.ToLower(path)]; ok {
		return e.arch
	}
	return ""
}

// Len is the number of distinct paths.
func (t *TOC) Len() int { return len(t.files) }

// ReadTOCFile loads a tab separated listing "archive, size, flag, path" (the format of Arch00_TOC.txt).
func ReadTOCFile(path string) (*TOC, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t := NewTOC()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		parts := strings.Split(strings.TrimRight(sc.Text(), "\r\n"), "\t")
		if len(parts) < 4 {
			continue
		}
		t.Add(parts[0], parts[3])
	}
	return t, sc.Err()
}

// FS is a set of opened archives.
type FS struct {
	Dir      string
	archives []*mounted
	TOC      *TOC
	win      map[string]winner
}

type mounted struct {
	name string
	rank int
	ar   *ltar.Archive
}

type winner struct {
	m   *mounted
	ent ltar.Entry
}

// Open opens every *.Arch00 file in dir. Unreadable archives are reported in the error list but do not
// stop the others.
func Open(dir string) (*FS, []error) {
	var errs []error
	names, _ := filepath.Glob(filepath.Join(dir, "*.[Aa][Rr][Cc][Hh]00"))
	sort.Slice(names, func(i, j int) bool {
		ri, rj := rankOf(filepath.Base(names[i])), rankOf(filepath.Base(names[j]))
		if ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})
	fs := &FS{Dir: dir, TOC: NewTOC(), win: make(map[string]winner)}
	for _, n := range names {
		ar, err := ltar.Open(n)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(n), err))
			continue
		}
		m := &mounted{name: filepath.Base(n), rank: rankOf(n), ar: ar}
		fs.archives = append(fs.archives, m)
		for _, e := range ar.Entries {
			lp := strings.ToLower(e.Path)
			if w, ok := fs.win[lp]; !ok || m.rank >= w.m.rank {
				fs.win[lp] = winner{m, e}
			}
			fs.TOC.Add(m.name, e.Path)
		}
	}
	return fs, errs
}

// Close closes all archives.
func (fs *FS) Close() {
	for _, m := range fs.archives {
		m.ar.Close()
	}
}

// Read returns the content of path from the archive that wins the mount order.
func (fs *FS) Read(path string) ([]byte, error) {
	w, ok := fs.win[strings.ToLower(strings.ReplaceAll(path, "/", "\\"))]
	if !ok {
		return nil, fmt.Errorf("pcfs: %s: not in any archive", path)
	}
	return w.m.ar.Read(w.ent)
}

// Has reports whether path exists in some archive.
func (fs *FS) Has(path string) bool {
	_, ok := fs.win[strings.ToLower(strings.ReplaceAll(path, "/", "\\"))]
	return ok
}
