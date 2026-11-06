package vfs_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/isobuild"
	"fearinstaller/internal/vfs"
)

// sampleFiles builds a small tree with random contents, including a file big
// enough to span several sectors and an empty directory-like nesting.
func sampleFiles(t *testing.T) ([]isobuild.File, map[string][]byte) {
	want := map[string][]byte{}
	var files []isobuild.File
	for _, p := range []string{"PS3_GAME/PARAM.SFO", "PS3_GAME/USRDIR/game/worlds/release/sfod_d.wld", "PS3_GAME/USRDIR/game/database/loki.gamedb", "a.txt", "Zeta/deep/er/file.bin", "PS3_GAME/USRDIR/game/lipsenglish.cat"} {
		n := 1 + len(p)*997
		if strings.HasSuffix(p, ".wld") {
			n = 5*2048 + 123
		}
		b := make([]byte, n)
		rand.Read(b)
		want[p] = b
		files = append(files, isobuild.File{Path: p, Data: b})
	}
	return files, want
}

func checkFS(t *testing.T, fsys vfs.FS, want map[string][]byte) {
	t.Helper()
	for p, b := range want {
		// look up with different case to prove case-insensitivity
		f, err := fsys.Open(strings.ToUpper(p))
		if err != nil {
			t.Fatalf("%s: open %s: %v", fsys.Kind(), p, err)
		}
		got := make([]byte, f.Size())
		if _, err := f.ReadAt(got, 0); err != nil && err != io.EOF {
			t.Fatal(err)
		}
		f.Close()
		if !bytes.Equal(got, b) {
			t.Fatalf("%s: content mismatch for %s (size %d vs %d)", fsys.Kind(), p, len(got), len(b))
		}
	}
	if _, err := fsys.Stat("nope/none"); err == nil {
		t.Fatal("expected not found")
	}
	ents, err := fsys.ReadDir("PS3_GAME")
	if err != nil || len(ents) != 2 {
		t.Fatalf("%s: ReadDir PS3_GAME = %v, %v", fsys.Kind(), ents, err)
	}
}

func TestImages(t *testing.T) {
	files, want := sampleFiles(t)
	dir := t.TempDir()
	cases := []struct {
		name string
		make func(p string) error
		kind string
	}{
		{"iso", func(p string) error { return isobuild.WriteISO9660(p, files, isobuild.ISO9660Options{}) }, "ISO 9660"},
		{"iso-multiextent", func(p string) error { return isobuild.WriteISO9660(p, files, isobuild.ISO9660Options{MaxExtent: 4096}) }, "ISO 9660"},
		{"udf", func(p string) error { return isobuild.WriteUDF(p, files) }, "UDF"},
		{"xdvdfs-raw", func(p string) error { return isobuild.WriteXDVDFS(p, files, 0) }, "XDVDFS (raw)"},
		{"xdvdfs-xgd3", func(p string) error { return isobuild.WriteXDVDFS(p, files, 0x02080000) }, "XDVDFS (XGD3)"},
		{"xdvdfs-xgd2", func(p string) error { return isobuild.WriteXDVDFS(p, files, 0x0FD90000) }, "XDVDFS (XGD2)"},
		{"xdvdfs-xgd1", func(p string) error { return isobuild.WriteXDVDFS(p, files, 0x18300000) }, "XDVDFS (XGD1)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(dir, c.name+".iso")
			if err := c.make(p); err != nil {
				t.Fatal(err)
			}
			fsys, err := vfs.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer fsys.Close()
			if fsys.Kind() != c.kind {
				t.Fatalf("kind = %q, want %q", fsys.Kind(), c.kind)
			}
			checkFS(t, fsys, want)
		})
	}
}

func TestUnknownImage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "junk.iso")
	b := make([]byte, 1<<20)
	rand.Read(b)
	if err := writeFile(p, b); err != nil {
		t.Fatal(err)
	}
	if _, err := vfs.Open(p); err != vfs.ErrUnknownImage {
		t.Fatalf("got %v, want ErrUnknownImage", err)
	}
}
