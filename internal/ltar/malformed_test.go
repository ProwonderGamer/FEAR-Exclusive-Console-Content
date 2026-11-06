package ltar

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"runtime"
	"testing"
)

// miniArchive builds an archive with one file "a" whose entry declares the given unpacked size.
func miniArchive(packed []byte, size uint64) []byte {
	le := binary.LittleEndian
	const strsz = 4 // "\0a\0\0": directory name "" at 0, file name "a" at 1
	dataOff := uint64(0x30 + strsz + 32 + 16)
	d := make([]byte, dataOff, dataOff+uint64(len(packed)))
	copy(d, "LTAR")
	le.PutUint32(d[4:], 3)
	le.PutUint32(d[8:], strsz)
	le.PutUint32(d[12:], 1)
	le.PutUint32(d[16:], 1)
	copy(d[0x30:], "\x00a\x00\x00")
	fe := d[0x30+strsz:]
	le.PutUint32(fe[0:], 1)
	le.PutUint64(fe[4:], dataOff)
	le.PutUint64(fe[12:], uint64(len(packed)))
	le.PutUint64(fe[20:], size)
	le.PutUint32(fe[28:], 1) // compressed
	// the single directory entry (name offset 0, no subdirectory, no sibling, one file) is all zero except:
	le.PutUint32(d[0x30+strsz+32+12:], 1)
	return append(d, packed...)
}

func zlibBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A tiny compressed entry that declares a gigabyte of content cannot be genuine (zlib never exceeds about
// 1032:1): it is refused without reserving the declared size.
func TestReadRejectsImplausibleUnpackedSize(t *testing.T) {
	arc := miniArchive(zlibBytes(t, []byte("hello")), 1<<30)
	a, err := New(bytes.NewReader(arc), int64(len(arc)))
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err = a.Read(a.Entries[0])
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("Read accepted a tiny entry declaring 1 GiB")
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 1<<20 {
		t.Fatalf("Read allocated %d bytes for a %d byte archive", grown, len(arc))
	}
}

// A declared size that passes the ratio check but is far above the real content must not be reserved
// up front either: the buffer only grows as data is decompressed.
func TestReadDoesNotReserveDeclaredSize(t *testing.T) {
	packed := zlibBytes(t, []byte("hello"))
	arc := miniArchive(packed, uint64(len(packed))*1000)
	a, err := New(bytes.NewReader(arc), int64(len(arc)))
	if err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err = a.Read(a.Entries[0])
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("Read accepted content shorter than declared")
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 1<<20 {
		t.Fatalf("Read allocated %d bytes for a %d byte archive", grown, len(arc))
	}
}

// Genuine highly compressible content (40 MiB of zeros, near the zlib ratio limit and above the initial
// buffer size) still reads back whole.
func TestReadHighlyCompressedEntry(t *testing.T) {
	raw := make([]byte, 40<<20)
	packed := zlibBytes(t, raw)
	t.Logf("ratio %d:1", len(raw)/len(packed))
	arc := miniArchive(packed, uint64(len(raw)))
	a, err := New(bytes.NewReader(arc), int64(len(arc)))
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Read(a.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("content differs (%d bytes)", len(got))
	}
}
