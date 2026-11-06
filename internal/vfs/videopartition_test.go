package vfs_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"fearinstaller/internal/isobuild"
	"fearinstaller/internal/vfs"
)

// A real Xbox 360 image may carry a DVD-Video "video partition" (ISO 9660/UDF) in front of the
// XDVDFS game partition. Simulate the minimum: a valid PVD at sector 16 with an empty root directory.
func videoPartitionImage(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "x.iso")
	if err := isobuild.WriteXDVDFS(p, smallFiles(), 0x0FD90000); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(p, os.O_RDWR, 0)
	pvd := make([]byte, 2048)
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	pvd[6] = 1
	root := pvd[156 : 156+34]
	root[0] = 34
	binary.LittleEndian.PutUint32(root[2:], 20) // LBA 20
	binary.LittleEndian.PutUint32(root[10:], 2048)
	root[25] = 2
	root[32] = 1
	f.WriteAt(pvd, 16*2048)
	term := make([]byte, 2048)
	term[0] = 255
	copy(term[1:], "CD001")
	f.WriteAt(term, 17*2048)
	f.Close()
	return p
}

// The XDVDFS-first order and the "keep the container whose root passes the
// layout check" rule both find the game behind a video partition; the old
// ISO-first order (still available as vfs.Open) does not, which is why the
// verifiers no longer use it for Xbox 360.
func TestVideoPartitionDoesNotHideXGD(t *testing.T) {
	p := videoPartitionImage(t)
	hasXex := func(f vfs.FS) bool { _, err := f.Stat("default.xex"); return err == nil }

	plain, err := vfs.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Kind() != "ISO 9660" || hasXex(plain) {
		t.Fatalf("test image no longer reproduces the finding (kind %q)", plain.Kind())
	}
	plain.Close()

	for name, order := range map[string][]vfs.Container{
		"xdvdfs first":                 {vfs.XDVDFS, vfs.ISO9660, vfs.UDF},
		"iso first with layout accept": vfs.DefaultOrder,
	} {
		t.Run(name, func(t *testing.T) {
			fsys, err := vfs.OpenOrdered(p, order, hasXex)
			if err != nil {
				t.Fatal(err)
			}
			defer fsys.Close()
			if fsys.Kind() != "XDVDFS (XGD2)" || !hasXex(fsys) {
				t.Fatalf("picked %q, default.xex visible=%v", fsys.Kind(), hasXex(fsys))
			}
		})
	}
	// If nothing passes the test, the first container that parsed is returned.
	fsys, err := vfs.OpenOrdered(p, vfs.DefaultOrder, func(vfs.FS) bool { return false })
	if err != nil || fsys.Kind() != "ISO 9660" {
		t.Fatalf("fallback: %v %v", fsys, err)
	}
	fsys.Close()
}
