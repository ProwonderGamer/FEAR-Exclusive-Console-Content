// Package vfs gives the installer one read-only view over the places a console
// game copy can live: an extracted folder, an ISO 9660 image (PS3), a UDF image
// or an Xbox 360 XDVDFS image. Nothing in this package ever writes.
package vfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when a path does not exist inside a source.
var ErrNotFound = errors.New("not found")

// Entry describes one file or directory inside a source.
type Entry struct {
	Name  string // final path component, original case
	Size  int64  // 0 for directories
	IsDir bool
}

// File is a random-access, read-only file.
type File interface {
	io.ReaderAt
	Size() int64
	Close() error
}

// FS is a read-only, case-insensitive file system. Paths use '/' separators
// and never start with '/'. The empty path is the root.
type FS interface {
	Open(path string) (File, error)
	Stat(path string) (Entry, error)
	ReadDir(path string) ([]Entry, error)
	// Kind names the container, e.g. "folder", "ISO 9660", "XDVDFS (XGD2)".
	Kind() string
	Close() error
}

// splitPath turns "a//b/" into ["a","b"].
func splitPath(p string) []string {
	p = strings.ReplaceAll(p, "\\", "/")
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------- folder ---

// DirFS is an FS rooted at a real directory. Lookups are case-insensitive so
// that "Game\Database\Loki.gamedb" matches however the user extracted it.
type DirFS struct{ root string }

// NewDirFS opens dir as a source. It fails if dir is not a directory.
func NewDirFS(dir string) (*DirFS, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, errors.New("not a directory")
	}
	return &DirFS{root: dir}, nil
}

func (d *DirFS) Kind() string { return "folder" }
func (d *DirFS) Close() error { return nil }

// resolve maps a case-insensitive virtual path to a real path.
func (d *DirFS) resolve(p string) (string, error) {
	cur := d.root
	for _, comp := range splitPath(p) {
		// Fast path: exact-case hit avoids listing big directories.
		if _, err := os.Lstat(filepath.Join(cur, comp)); err == nil {
			cur = filepath.Join(cur, comp)
			continue
		}
		ents, err := os.ReadDir(cur)
		if err != nil {
			return "", ErrNotFound
		}
		found := false
		for _, e := range ents {
			if strings.EqualFold(e.Name(), comp) {
				cur = filepath.Join(cur, e.Name())
				found = true
				break
			}
		}
		if !found {
			return "", ErrNotFound
		}
	}
	return cur, nil
}

// RealPath returns the file or folder on disk for a path inside the source, matched without regard to case
// ("" is the source folder itself).
func (d *DirFS) RealPath(p string) (string, error) { return d.resolve(p) }

type osFile struct {
	*os.File
	size int64
}

func (f osFile) Size() int64 { return f.size }

func (d *DirFS) Open(p string) (File, error) {
	rp, err := d.resolve(p)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(rp)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		return nil, ErrNotFound
	}
	return osFile{f, st.Size()}, nil
}

func (d *DirFS) Stat(p string) (Entry, error) {
	rp, err := d.resolve(p)
	if err != nil {
		return Entry{}, err
	}
	st, err := os.Stat(rp)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: st.Name(), Size: st.Size(), IsDir: st.IsDir()}, nil
}

func (d *DirFS) ReadDir(p string) ([]Entry, error) {
	rp, err := d.resolve(p)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(rp)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(ents))
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if e.Type()&fs.ModeSymlink != 0 { // follow links (and Windows junctions)
			if info, err = os.Stat(filepath.Join(rp, e.Name())); err != nil {
				continue
			}
		}
		sz := info.Size()
		if info.IsDir() {
			sz = 0
		}
		out = append(out, Entry{Name: e.Name(), Size: sz, IsDir: info.IsDir()})
	}
	return out, nil
}

// ------------------------------------------------- shared tree helper ---

// node is an in-memory directory tree node used by the image readers, which
// parse the whole directory structure once (a few thousand entries at most).
type node struct {
	name     string
	dir      bool
	size     int64
	extents  []extent // file data (ISO9660 may have several)
	children []*node
}

// extent is a run of bytes at an absolute image offset. A hole (UDF extents
// that are allocated or free but not recorded) has off < 0 and reads as zeros.
type extent struct{ off, length int64 }

func (e extent) hole() bool { return e.off < 0 }

// treeFS implements FS on top of a parsed node tree and an io.ReaderAt image.
type treeFS struct {
	img  io.ReaderAt
	root *node
	kind string
	c    io.Closer
}

func (t *treeFS) Kind() string { return t.kind }
func (t *treeFS) Close() error {
	if t.c != nil {
		return t.c.Close()
	}
	return nil
}

func (t *treeFS) lookup(p string) (*node, error) {
	cur := t.root
	for _, comp := range splitPath(p) {
		if !cur.dir {
			return nil, ErrNotFound
		}
		var next *node
		for _, c := range cur.children {
			if strings.EqualFold(c.name, comp) {
				next = c
				break
			}
		}
		if next == nil {
			return nil, ErrNotFound
		}
		cur = next
	}
	return cur, nil
}

func (t *treeFS) Stat(p string) (Entry, error) {
	n, err := t.lookup(p)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: n.name, Size: n.size, IsDir: n.dir}, nil
}

func (t *treeFS) ReadDir(p string) ([]Entry, error) {
	n, err := t.lookup(p)
	if err != nil || !n.dir {
		return nil, ErrNotFound
	}
	out := make([]Entry, 0, len(n.children))
	for _, c := range n.children {
		out = append(out, Entry{Name: c.name, Size: c.size, IsDir: c.dir})
	}
	return out, nil
}

func (t *treeFS) Open(p string) (File, error) {
	n, err := t.lookup(p)
	if err != nil || n.dir {
		return nil, ErrNotFound
	}
	return &extentFile{img: t.img, ext: n.extents, size: n.size}, nil
}

// extentFile reads a logical file stored as one or more image extents.
type extentFile struct {
	img  io.ReaderAt
	ext  []extent
	size int64
}

func (f *extentFile) Size() int64  { return f.size }
func (f *extentFile) Close() error { return nil }

func (f *extentFile) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= f.size {
		return 0, io.EOF
	}
	n := 0
	var base int64 // logical offset of the current extent
	for _, e := range f.ext {
		if n == len(p) {
			break
		}
		if off >= base+e.length {
			base += e.length
			continue
		}
		in := off - base // offset inside this extent
		want := int64(len(p) - n)
		if rem := e.length - in; want > rem {
			want = rem
		}
		if want > f.size-off {
			want = f.size - off
		}
		var got int
		var err error
		if e.hole() { // not-recorded extent: reads as zeros
			clear(p[n : n+int(want)])
			got = int(want)
		} else {
			got, err = f.img.ReadAt(p[n:n+int(want)], e.off+in)
		}
		n += got
		off += int64(got)
		if err != nil && !(err == io.EOF && got == int(want)) {
			return n, err
		}
		base += e.length
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
