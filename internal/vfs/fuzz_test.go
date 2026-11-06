package vfs_test

import (
	"bytes"
	"testing"

	"fearinstaller/internal/vfs"
)

func walkAll(fsys vfs.FS, p string, d int) {
	ents, _ := fsys.ReadDir(p)
	for _, e := range ents {
		q := e.Name
		if p != "" {
			q = p + "/" + e.Name
		}
		if e.IsDir {
			if d < 30 {
				walkAll(fsys, q, d+1)
			}
		} else if f, err := fsys.Open(q); err == nil {
			b := make([]byte, 8)
			f.ReadAt(b, 0)
			f.ReadAt(b, f.Size())
			f.ReadAt(b, f.Size()-4)
		}
	}
}

func fz(f *testing.F, kind string, open func(r *bytes.Reader) (vfs.FS, error), cut int) {
	img := buildImgF(f, kind)
	if len(img) > cut {
		img = img[:cut]
	}
	f.Add(img)
	f.Fuzz(func(t *testing.T, data []byte) {
		if fsys, err := open(bytes.NewReader(data)); err == nil {
			walkAll(fsys, "", 0)
		}
	})
}

func FuzzISO(f *testing.F) {
	fz(f, "iso", func(r *bytes.Reader) (vfs.FS, error) { return vfs.OpenISO9660(r, nil) }, 70*2048)
}
func FuzzXDVDFS(f *testing.F) {
	fz(f, "xd", func(r *bytes.Reader) (vfs.FS, error) { return vfs.OpenXDVDFS(r, nil) }, 70*2048)
}

func FuzzUDF(f *testing.F) {
	fz(f, "udf", func(r *bytes.Reader) (vfs.FS, error) { return vfs.OpenUDF(r, nil) }, 600*2048)
}
