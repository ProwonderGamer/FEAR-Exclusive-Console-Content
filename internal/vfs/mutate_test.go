package vfs_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"testing"
	"time"

	"fearinstaller/internal/isobuild"
	"fearinstaller/internal/vfs"
)

func smallFiles() []isobuild.File {
	var fl []isobuild.File
	for _, p := range []string{"PS3_GAME/PARAM.SFO", "PS3_GAME/USRDIR/game/worlds/release/a.wld", "PS3_GAME/USRDIR/game/b.cat", "x.txt", "default.xex", "Game/database/Loki.gamedb"} {
		fl = append(fl, isobuild.File{Path: p, Data: bytes.Repeat([]byte{byte(len(p))}, 3000)})
	}
	return fl
}

type opener func(r *bytes.Reader) (vfs.FS, error)

// tryOpen opens + walks the whole tree with panic recovery and a timeout.
func tryOpen(img []byte, open opener) (res string) {
	done := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				st := string(debug.Stack())
				// first frame inside our package
				loc := ""
				for _, l := range strings.Split(st, "\n") {
					if strings.Contains(l, "internal/vfs/") && !strings.Contains(l, "_test.go") && strings.Contains(l, ".go:") {
						loc = strings.TrimSpace(l)
						break
					}
				}
				done <- fmt.Sprintf("PANIC %v @ %s", r, loc)
			}
		}()
		fsys, err := open(bytes.NewReader(img))
		if err != nil {
			done <- "err"
			return
		}
		var walk func(p string, d int)
		walk = func(p string, d int) {
			ents, _ := fsys.ReadDir(p)
			for _, e := range ents {
				q := e.Name
				if p != "" {
					q = p + "/" + e.Name
				}
				if e.IsDir {
					if d < 30 {
						walk(q, d+1)
					}
				} else {
					if f, err := fsys.Open(q); err == nil {
						b := make([]byte, 16)
						f.ReadAt(b, 0)
					}
				}
			}
		}
		walk("", 0)
		done <- "ok"
	}()
	select {
	case r := <-done:
		return r
	case <-time.After(3 * time.Second):
		return "TIMEOUT/HANG"
	}
}

func mutateAll(t *testing.T, name string, img []byte, open opener, sectorOK func(sec []byte, n int) bool, sectors int) {
	found := map[string]int{}
	first := map[string]string{}
	for s := 0; s < sectors && (s+1)*2048 <= len(img); s++ {
		sec := img[s*2048 : (s+1)*2048]
		if !sectorOK(sec, s) {
			continue
		}
		for off := 0; off < 2048-3; off += 2 {
			orig := append([]byte(nil), sec[off:off+4]...)
			for _, v := range []uint32{0xFFFFFFFF, 0x80000000, 0x7FFFFFFF, 0x00010000, 0} {
				binary.LittleEndian.PutUint32(sec[off:], v)
				r := tryOpen(img, open)
				if strings.HasPrefix(r, "PANIC") || strings.HasPrefix(r, "TIMEOUT") {
					found[r]++
					if _, ok := first[r]; !ok {
						first[r] = fmt.Sprintf("sector %d off %#x val %#x", s, off, v)
					}
				}
			}
			copy(sec[off:], orig)
		}
	}
	var keys []string
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("%s: %s x%d (first: %s)", name, k, found[k], first[k])
	}
	if len(keys) == 0 {
		t.Logf("%s: no panics/hangs", name)
	} else {
		t.Errorf("%s: %d distinct panic/hang outcomes", name, len(keys))
	}
}

func buildImgF(t testing.TB, kind string) []byte {
	p := filepath.Join(t.TempDir(), "i.iso")
	var err error
	switch kind {
	case "iso":
		err = isobuild.WriteISO9660(p, smallFiles(), isobuild.ISO9660Options{})
	case "udf":
		err = isobuild.WriteUDF(p, smallFiles())
	case "xd":
		err = isobuild.WriteXDVDFS(p, smallFiles(), 0)
	}
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	return b
}

func buildImg(t *testing.T, kind string) []byte { return buildImgF(t, kind) }

func TestMutateUDF(t *testing.T) {
	img := buildImg(t, "udf")
	t.Logf("udf image %d bytes", len(img))
	mutateAll(t, "UDF", img, func(r *bytes.Reader) (vfs.FS, error) { return vfs.OpenUDF(r, nil) },
		func(s []byte, n int) bool {
			tag := binary.LittleEndian.Uint16(s)
			switch tag {
			case 2, 5, 6, 8, 256, 257, 261, 266:
				return true
			}
			return false
		}, 400)
}

func TestMutateISO(t *testing.T) {
	img := buildImg(t, "iso")
	mutateAll(t, "ISO9660", img, func(r *bytes.Reader) (vfs.FS, error) { return vfs.OpenISO9660(r, nil) },
		func(s []byte, n int) bool {
			return (n >= 16 && n < 20) || (s[0] != 0 && n >= 20 && n < 60)
		}, 60)
}

func TestMutateXDVDFS(t *testing.T) {
	img := buildImg(t, "xd")
	mutateAll(t, "XDVDFS", img, func(r *bytes.Reader) (vfs.FS, error) { return vfs.OpenXDVDFS(r, nil) },
		func(s []byte, n int) bool { return n >= 32 && n < 60 && s[0] != 0 }, 60)
}
