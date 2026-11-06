// Package catalog reads the console (PS3 and Xbox 360) F.E.A.R. resource catalogs: *.cat, *.matcat and
// *.texcat. They are big endian.
//
//	header (0x20): u32 version; u32 headerSize(0x20); u32 numEntries; u32 strTabOfs; u32 strTabSize;
//	               u32 dataStart; u32 ?; u32 ?
//	entries at headerSize: {u32 nameOfs; u32 dataOfs; u32 size; u32 packedSize}   (packedSize != size => compressed)
//
// Compressed entries are deflate streams; the producer used zlib, raw deflate or gzip framing, so all three
// are tried (the result must have exactly "size" bytes). Names are Latin-1 and are returned as stored
// (backslash or slash separated, any case); use PathKey for case-insensitive matching.
package catalog

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Entry is one catalog entry.
type Entry struct {
	Name       string
	Offset     uint32
	Size       uint32
	PackedSize uint32
}

// Catalog is an open catalog.
type Catalog struct {
	Version   uint32
	DataStart uint32
	Entries   []Entry
	r         io.ReaderAt
	size      int64
	closer    io.Closer
	byKey     map[string]int
}

const maxEntries = 4 << 20

// Open opens a catalog file.
func Open(path string) (*Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	c, err := New(f, st.Size())
	if err != nil {
		f.Close()
		return nil, err
	}
	c.closer = f
	return c, nil
}

// New parses a catalog from r (size bytes long).
func New(r io.ReaderAt, size int64) (*Catalog, error) {
	h := make([]byte, 0x20)
	if _, err := r.ReadAt(h, 0); err != nil {
		return nil, fmt.Errorf("catalog: header: %w", err)
	}
	var v [8]uint32
	for i := range v {
		v[i] = binary.BigEndian.Uint32(h[4*i:])
	}
	ver, hsz, n, sofs, ssz, dstart := v[0], v[1], v[2], v[3], v[4], v[5]
	if hsz < 0x20 || int64(hsz) > size || n > maxEntries || int64(sofs)+int64(ssz) > size ||
		int64(hsz)+16*int64(n) > size {
		return nil, errors.New("catalog: not a catalog (implausible header)")
	}
	ent := make([]byte, 16*int(n))
	if _, err := r.ReadAt(ent, int64(hsz)); err != nil {
		return nil, fmt.Errorf("catalog: entry table: %w", err)
	}
	strtab := make([]byte, ssz)
	if _, err := r.ReadAt(strtab, int64(sofs)); err != nil {
		return nil, fmt.Errorf("catalog: string table: %w", err)
	}
	c := &Catalog{Version: ver, DataStart: dstart, r: r, size: size, byKey: make(map[string]int)}
	for i := 0; i < int(n); i++ {
		no := binary.BigEndian.Uint32(ent[16*i:])
		e := Entry{Offset: binary.BigEndian.Uint32(ent[16*i+4:]), Size: binary.BigEndian.Uint32(ent[16*i+8:]),
			PackedSize: binary.BigEndian.Uint32(ent[16*i+12:])}
		if int64(no) > int64(len(strtab)) {
			return nil, fmt.Errorf("catalog: name offset of entry %d outside the string table", i)
		}
		end := bytes.IndexByte(strtab[no:], 0)
		var name []byte
		if end < 0 { // version 2 catalogs do not terminate the last name
			name = strtab[no:]
		} else {
			name = strtab[no : int(no)+end]
		}
		e.Name = strings.TrimRight(string(name), "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f")
		if int64(e.Offset)+int64(e.PackedSize) > size {
			return nil, fmt.Errorf("catalog: entry %q lies outside the file", e.Name)
		}
		c.Entries = append(c.Entries, e)
		k := PathKey(e.Name)
		if _, dup := c.byKey[k]; !dup {
			c.byKey[k] = i
		}
	}
	return c, nil
}

// Close closes the file opened by Open.
func (c *Catalog) Close() error {
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}

// PathKey normalises a catalog path for lookups: lower case (ASCII), "/" -> "\".
func PathKey(p string) string {
	b := []byte(p)
	for i, ch := range b {
		switch {
		case ch >= 'A' && ch <= 'Z':
			b[i] = ch + 32
		case ch == '/':
			b[i] = '\\'
		}
	}
	return string(b)
}

// Lookup finds an entry by path, ignoring case and the kind of slash.
func (c *Catalog) Lookup(path string) (Entry, bool) {
	i, ok := c.byKey[PathKey(path)]
	if !ok {
		return Entry{}, false
	}
	return c.Entries[i], true
}

// maxRead bounds one decompressed entry.
const maxRead = 1 << 30

// Read returns the (decompressed) content of an entry.
func (c *Catalog) Read(e Entry) ([]byte, error) {
	if e.Size > maxRead || e.PackedSize > maxRead {
		return nil, errors.New("catalog: entry too large")
	}
	d := make([]byte, e.PackedSize)
	if _, err := c.r.ReadAt(d, int64(e.Offset)); err != nil {
		return nil, fmt.Errorf("catalog: read %q: %w", e.Name, err)
	}
	if e.PackedSize == e.Size {
		return d, nil
	}
	mk := []func() (io.Reader, error){
		func() (io.Reader, error) { return zlib.NewReader(bytes.NewReader(d)) },
		func() (io.Reader, error) { return flate.NewReader(bytes.NewReader(d)), nil },
		func() (io.Reader, error) { return gzip.NewReader(bytes.NewReader(d)) },
	}
	for _, f := range mk {
		r, err := f()
		if err != nil {
			continue
		}
		out, err := io.ReadAll(io.LimitReader(r, int64(e.Size)+1))
		if err == nil && len(out) == int(e.Size) {
			return out, nil
		}
	}
	return nil, fmt.Errorf("catalog: cannot decompress %q (first bytes %x)", e.Name, d[:min(8, len(d))])
}

// ReadByName reads the entry with the given path.
func (c *Catalog) ReadByName(path string) ([]byte, error) {
	e, ok := c.Lookup(path)
	if !ok {
		return nil, fmt.Errorf("catalog: %q not found", path)
	}
	return c.Read(e)
}

// SafeRelPath converts a catalog entry name (Latin-1, any slash style) into a relative path that is safe to
// join below an output folder: no "..", no drive letters or colons, no reserved Windows names, no trailing
// dots or spaces, no control characters. ok=false rejects the name. (Treat every name coming out of an image
// as hostile.)
func SafeRelPath(name string) (string, bool) {
	var parts []string
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == "." {
			continue
		}
		if seg == ".." || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return "", false
		}
		for _, c := range []byte(seg) {
			if c < 0x20 || strings.IndexByte(`<>:"|?*`, c) >= 0 {
				return "", false
			}
		}
		stem := strings.ToUpper(seg)
		if i := strings.IndexByte(stem, '.'); i >= 0 {
			stem = stem[:i]
		}
		switch stem {
		case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
			"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
			return "", false
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, string(os.PathSeparator)), true
}
