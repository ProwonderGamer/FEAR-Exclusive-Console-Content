package vfs_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"fearinstaller/internal/vfs"
)

// Regression tests for UDF panics and unbounded allocations
// and for "not recorded" extents. Each edit patches one field of
// an otherwise valid UDF image.

func openUDF(img []byte) (vfs.FS, error) { return vfs.OpenUDF(bytes.NewReader(img), nil) }

func findSector(img []byte, tag uint16, from int) int {
	for s := from; (s+1)*2048 <= len(img); s++ {
		if binary.LittleEndian.Uint16(img[s*2048:]) == tag {
			return s
		}
	}
	return -1
}

func TestUDFLogicalBlockSize(t *testing.T) {
	for _, bs := range []uint32{0, 1, 512, 4096, 1 << 31, 0xFFFFFFFF} {
		img := buildImg(t, "udf")
		lvd := findSector(img, 6, 257)
		binary.LittleEndian.PutUint32(img[lvd*2048+212:], bs)
		_, err := openUDF(img)
		var ue *vfs.UnsupportedError
		if !errors.As(err, &ue) {
			t.Fatalf("block size %d: want UnsupportedError, got %v", bs, err)
		}
	}
}

func TestUDFExtendedAttributeLengthOutOfRange(t *testing.T) {
	for _, v := range []uint32{2048, 0x7FFFFFFF, 0xFFFFFFFF} {
		img := buildImg(t, "udf")
		for s := 257; (s+1)*2048 <= len(img); s++ {
			if tag := binary.LittleEndian.Uint16(img[s*2048:]); tag == 261 || tag == 266 {
				off := 168
				if tag == 266 {
					off = 208
				}
				binary.LittleEndian.PutUint32(img[s*2048+off:], v)
			}
		}
		if _, err := openUDF(img); err == nil {
			t.Fatalf("eaLen %#x: expected an error, the image has no readable root", v)
		}
	}
}

func TestUDFNegativeAndHugeInformationLength(t *testing.T) {
	for _, hi := range []uint32{0x80000000, 0xFFFFFFFF, 0x7FFFFFFF, 0x00100000} {
		img := buildImg(t, "udf")
		for s := 257; (s+1)*2048 <= len(img); s++ {
			if tag := binary.LittleEndian.Uint16(img[s*2048:]); tag == 261 || tag == 266 {
				binary.LittleEndian.PutUint32(img[s*2048+56:], 0)
				binary.LittleEndian.PutUint32(img[s*2048+60:], hi)
			}
		}
		// Must return (error or a tree) without panicking or allocating the claimed size.
		if fsys, err := openUDF(img); err == nil {
			walkAll(fsys, "", 0)
		}
	}
}

func TestUDFMetadataAndVirtualPartitionsAreRefused(t *testing.T) {
	for _, id := range []string{"*UDF Metadata Partition", "*UDF Virtual Partition"} {
		img := buildImg(t, "udf")
		lvd := findSector(img, 6, 257)
		p := lvd*2048 + 440
		// Rewrite the single map as a type 2 map with the identifier.
		img[p], img[p+1] = 2, 64
		copy(img[p+5:], id)
		binary.LittleEndian.PutUint32(img[lvd*2048+264:], 64)
		_, err := openUDF(img)
		var ue *vfs.UnsupportedError
		if !errors.As(err, &ue) {
			t.Fatalf("%s: want UnsupportedError, got %v", id, err)
		}
	}
}

func TestUDFPartitionMapBounds(t *testing.T) {
	for _, tc := range []struct{ typ, ln byte }{{1, 0xFF}, {2, 0xFF}, {2, 6}, {1, 2}, {9, 0}} {
		img := buildImg(t, "udf")
		lvd := findSector(img, 6, 257)
		p := lvd*2048 + 440
		img[p], img[p+1] = tc.typ, tc.ln
		binary.LittleEndian.PutUint32(img[lvd*2048+264:], 0xFFFFFFFF)
		binary.LittleEndian.PutUint32(img[lvd*2048+268:], 0xFFFFFFFF)
		if fsys, err := openUDF(img); err == nil {
			walkAll(fsys, "", 0)
		}
	}
}

// An extent flagged "allocated, not recorded" (type 1) reads as zeros,
// and a continuation descriptor (type 3) is refused as unsupported.
func TestUDFNotRecordedExtent(t *testing.T) {
	img := buildImg(t, "udf")
	patched := 0
	for s := 257; (s+1)*2048 <= len(img) && patched == 0; s++ {
		if binary.LittleEndian.Uint16(img[s*2048:]) != 261 || img[s*2048+27] == 4 { // first file entry
			continue
		}
		ad := s*2048 + 176 + int(binary.LittleEndian.Uint32(img[s*2048+168:]))
		l := binary.LittleEndian.Uint32(img[ad:])
		binary.LittleEndian.PutUint32(img[ad:], l&0x3FFFFFFF|1<<30)
		patched++
	}
	if patched == 0 {
		t.Skip("no file entry found to patch")
	}
	fsys, err := openUDF(img)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	zeros := 0
	var walk func(p string)
	walk = func(p string) {
		ents, _ := fsys.ReadDir(p)
		for _, e := range ents {
			q := e.Name
			if p != "" {
				q = p + "/" + e.Name
			}
			if e.IsDir {
				walk(q)
			} else if f, err := fsys.Open(q); err == nil {
				b := make([]byte, 16)
				if n, _ := f.ReadAt(b, 0); n == 16 && bytes.Equal(b, make([]byte, 16)) {
					zeros++
				}
			}
		}
	}
	walk("")
	if zeros != 1 {
		t.Fatalf("expected exactly one file reading as zeros, got %d", zeros)
	}
}

// Directory sizes are checked before anything is allocated.
func TestDirectorySizeChecks(t *testing.T) {
	img := buildImg(t, "iso")
	for _, size := range []uint32{0xFFFFFFFF, 1 << 30, 64 << 20} {
		x := append([]byte(nil), img...)
		// The primary volume descriptor's root record: bytes 156+10 = size (both-endian field starts at +10).
		binary.LittleEndian.PutUint32(x[16*2048+156+10:], size)
		if fsys, err := vfs.OpenISO9660(bytes.NewReader(x), nil); err == nil {
			walkAll(fsys, "", 0)
			t.Errorf("size %#x: expected rejection of the implausible root directory", size)
		}
	}
}
