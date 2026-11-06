package vfs_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"fearinstaller/internal/vfs"
)

func TestXDVDFSSelfLoop(t *testing.T) {
	img := make([]byte, 40*2048)
	vd := img[32*2048:]
	copy(vd, "MICROSOFT*XBOX*MEDIA")
	binary.LittleEndian.PutUint32(vd[0x14:], 33)
	binary.LittleEndian.PutUint32(vd[0x18:], 2048)
	copy(vd[0x7EC:], "MICROSOFT*XBOX*MEDIA")
	d := img[33*2048 : 34*2048]
	for i := range d {
		d[i] = 0xFF
	}
	put := func(off int, left, right uint16, sec, size uint32, attr byte, name string) {
		binary.LittleEndian.PutUint16(d[off:], left)
		binary.LittleEndian.PutUint16(d[off+2:], right)
		binary.LittleEndian.PutUint32(d[off+4:], sec)
		binary.LittleEndian.PutUint32(d[off+8:], size)
		d[off+12] = attr
		d[off+13] = byte(len(name))
		copy(d[off+14:], name)
	}
	put(0, 0, 4, 33, 2048, 0x10, "a") // right link -> offset 16
	put(16, 0, 0, 33, 2048, 0x10, "b")
	start := time.Now()
	done := make(chan error, 1)
	go func() { _, err := vfs.OpenXDVDFS(bytes.NewReader(img), nil); done <- err }()
	select {
	case err := <-done:
		t.Logf("returned %v after %v", err, time.Since(start))
	case <-time.After(5 * time.Second):
		t.Fatalf("HANG: still running after 5s (self-referencing directory entries)")
	}
}

func TestUDFSelfLoop(t *testing.T) {
	img := buildImg(t, "udf")
	fsd := img[272*2048:]
	rootLbn := binary.LittleEndian.Uint32(fsd[400+4:])
	// find first sector with FIDs after the root entry and re-point every child at the root
	patched := 0
	for s := 272; s < len(img)/2048 && patched == 0; s++ {
		sec := img[s*2048 : (s+1)*2048]
		if binary.LittleEndian.Uint16(sec) != 257 {
			continue
		}
		for p := 0; p+38 <= 2048 && binary.LittleEndian.Uint16(sec[p:]) == 257; {
			chars := sec[p+18]
			fil := int(sec[p+19])
			liu := int(binary.LittleEndian.Uint16(sec[p+36:]))
			if chars&0x08 == 0 {
				binary.LittleEndian.PutUint32(sec[p+20+4:], rootLbn)
				patched++
			}
			p += (38 + liu + fil + 3) &^ 3
		}
	}
	t.Logf("patched %d child FIDs to point at the root directory", patched)
	start := time.Now()
	done := make(chan error, 1)
	go func() { _, err := vfs.OpenUDF(bytes.NewReader(img), nil); done <- err }()
	select {
	case err := <-done:
		t.Logf("returned %v after %v", err, time.Since(start))
	case <-time.After(5 * time.Second):
		t.Fatalf("HANG: OpenUDF still running after 5s (self-referencing directory entries)")
	}
}
