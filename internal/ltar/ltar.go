// Package ltar reads the LithTech Jupiter EX "LTAR" v3 archives (.Arch00) of the PC game.
//
// Header (0x30): "LTAR", u32 ver=3, u32 strTabSize, u32 numDirs, u32 numFiles, u32 ?, u32 ?, u32 ?, u8 hash[16]
// Then the string table, the file entries {u32 nameOfs; u64 offset; u64 packedSize; u64 size; u32 compressed}
// (32 bytes, little endian, no padding) and the directory entries {u32 nameOfs; u32 firstSubDir;
// u32 nextSibling; u32 numFiles}. Files are listed per directory in order. Compressed files are zlib streams.
package ltar

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Entry is one file inside an archive.
type Entry struct {
	Path       string // directory + "\" + name, as stored
	Offset     uint64
	PackedSize uint64
	Size       uint64
	Compressed bool
}

// Archive is an open archive. The file is read lazily with ReadAt.
type Archive struct {
	Entries []Entry
	f       io.ReaderAt
	closer  io.Closer
}

// maxTable bounds the header tables so a damaged file cannot cause huge allocations.
const maxTable = 8 << 20

// Open opens an archive file.
func Open(path string) (*Archive, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	a, err := New(f, st.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	a.closer = f
	return a, nil
}

// New parses an archive from r (size bytes long).
func New(r io.ReaderAt, size int64) (*Archive, error) {
	h := make([]byte, 0x30)
	if _, err := r.ReadAt(h, 0); err != nil {
		return nil, fmt.Errorf("ltar: header: %w", err)
	}
	if string(h[:4]) != "LTAR" {
		return nil, errors.New("ltar: bad magic")
	}
	ver := binary.LittleEndian.Uint32(h[4:])
	strsz := int64(binary.LittleEndian.Uint32(h[8:]))
	ndirs := int64(binary.LittleEndian.Uint32(h[12:]))
	nfiles := int64(binary.LittleEndian.Uint32(h[16:]))
	if ver != 3 {
		return nil, fmt.Errorf("ltar: unsupported version %d", ver)
	}
	if strsz > maxTable*16 || ndirs > maxTable || nfiles > maxTable {
		return nil, errors.New("ltar: implausible table sizes")
	}
	need := int64(0x30) + strsz + nfiles*32 + ndirs*16
	if need > size {
		return nil, errors.New("ltar: tables exceed file size")
	}
	tab := make([]byte, strsz+nfiles*32+ndirs*16)
	if _, err := r.ReadAt(tab, 0x30); err != nil {
		return nil, fmt.Errorf("ltar: tables: %w", err)
	}
	strtab := tab[:strsz]
	fe := tab[strsz : strsz+nfiles*32]
	de := tab[strsz+nfiles*32:]
	cstr := func(o uint32) (string, error) {
		if int64(o) >= int64(len(strtab)) {
			return "", errors.New("ltar: name offset outside string table")
		}
		e := bytes.IndexByte(strtab[o:], 0)
		if e < 0 {
			return "", errors.New("ltar: unterminated name")
		}
		return string(strtab[o : int(o)+e]), nil
	}
	a := &Archive{f: r}
	fi := int64(0)
	for di := int64(0); di < ndirs; di++ {
		d := de[di*16:]
		dname, err := cstr(binary.LittleEndian.Uint32(d))
		if err != nil {
			return nil, err
		}
		cnt := int64(binary.LittleEndian.Uint32(d[12:]))
		for k := int64(0); k < cnt; k++ {
			if fi >= nfiles {
				return nil, errors.New("ltar: directory file counts exceed file table")
			}
			e := fe[fi*32:]
			fi++
			name, err := cstr(binary.LittleEndian.Uint32(e))
			if err != nil {
				return nil, err
			}
			full := name
			if dname != "" {
				full = dname + "\\" + name
			}
			ent := Entry{Path: full,
				Offset:     binary.LittleEndian.Uint64(e[4:]),
				PackedSize: binary.LittleEndian.Uint64(e[12:]),
				Size:       binary.LittleEndian.Uint64(e[20:]),
				Compressed: binary.LittleEndian.Uint32(e[28:]) != 0}
			if ent.Offset > uint64(size) || ent.PackedSize > uint64(size)-ent.Offset {
				return nil, fmt.Errorf("ltar: entry %q outside the archive", full)
			}
			a.Entries = append(a.Entries, ent)
		}
	}
	return a, nil
}

// Close closes the underlying file (if opened with Open).
func (a *Archive) Close() error {
	if a.closer != nil {
		return a.closer.Close()
	}
	return nil
}

// maxEntry is the largest file this reader will decompress into memory.
const maxEntry = 1 << 31

// maxRatio is the largest unpacked-to-packed size ratio a zlib stream can have. Deflate cannot encode more
// than 258 bytes (the longest match) in less than 2 bits, which gives 1032:1 at best; the zlib header and
// checksum only lower it. A compressed entry declaring more than this is damaged.
const maxRatio = 1032

// maxPrealloc caps the buffer reserved before decompression starts. Entries larger than this grow the
// buffer as data arrives, so a damaged declared size cannot reserve memory the stream never fills.
const maxPrealloc = 32 << 20

// Read returns the (decompressed) content of an entry.
func (a *Archive) Read(e Entry) ([]byte, error) {
	if e.PackedSize > maxEntry || e.Size > maxEntry {
		return nil, errors.New("ltar: entry too large")
	}
	if e.Compressed && e.Size > e.PackedSize*maxRatio {
		return nil, fmt.Errorf("ltar: %q: unpacked size %d is impossible for %d compressed bytes", e.Path, e.Size, e.PackedSize)
	}
	raw := make([]byte, e.PackedSize)
	if _, err := a.f.ReadAt(raw, int64(e.Offset)); err != nil {
		return nil, fmt.Errorf("ltar: read %q: %w", e.Path, err)
	}
	if !e.Compressed {
		return raw, nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("ltar: %q: %w", e.Path, err)
	}
	defer zr.Close()
	buf := bytes.NewBuffer(make([]byte, 0, min(e.Size, maxPrealloc)))
	if _, err := io.Copy(buf, io.LimitReader(zr, int64(e.Size)+1)); err != nil {
		return nil, fmt.Errorf("ltar: %q: %w", e.Path, err)
	}
	if uint64(buf.Len()) != e.Size {
		return nil, fmt.Errorf("ltar: %q: size %d, expected %d", e.Path, buf.Len(), e.Size)
	}
	return buf.Bytes(), nil
}
