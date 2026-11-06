package vfs

import (
	"errors"
	"io"
	"os"
	"strings"
)

// Limits that keep a damaged or hostile image from exhausting memory or time.
// A real game disc has a few thousand entries and directories of a few KiB.
const (
	maxDirBytes = 16 << 20 // largest directory the readers will load
	maxNodes    = 250_000  // entries (files + directories) per image
	maxDepth    = 24       // directory nesting
	readChunk   = 1 << 20  // directories are read in chunks, never allocated up front
)

// ErrCorrupt means the image's structures contradict themselves (bad offsets,
// sizes, loops). Callers show it as "the image is damaged".
var ErrCorrupt = errors.New("the disc image structure is damaged")

// UnsupportedError means the image uses a format feature this reader does not
// implement (for example UDF metadata partitions). The image may be fine.
type UnsupportedError struct{ What string }

func (e *UnsupportedError) Error() string { return "unsupported disc image feature: " + e.What }

// budget counts entries so that loops and fan-out cannot run forever.
type budget struct{ nodes int }

func (b *budget) take() error {
	b.nodes++
	if b.nodes > maxNodes {
		return ErrCorrupt
	}
	return nil
}

// imageSize returns the length of the image when the reader can tell, else -1.
func imageSize(img io.ReaderAt) int64 {
	switch v := img.(type) {
	case interface{ Size() int64 }: // bytes.Reader, io.SectionReader
		return v.Size()
	case *os.File:
		if st, err := v.Stat(); err == nil {
			return st.Size()
		}
	}
	return -1
}

// readRange reads exactly n bytes at off, in chunks, after bounds checks, so a
// lying size field produces an error rather than a giant allocation.
func readRange(img io.ReaderAt, off, n, size int64) ([]byte, error) {
	if off < 0 || n < 0 || n > maxDirBytes {
		return nil, ErrCorrupt
	}
	if size >= 0 && off+n > size {
		return nil, ErrCorrupt
	}
	out := make([]byte, 0, min(n, readChunk))
	for int64(len(out)) < n {
		c := min(n-int64(len(out)), readChunk)
		start := len(out)
		out = append(out, make([]byte, c)...)
		if _, err := img.ReadAt(out[start:], off+int64(start)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// safeName reports whether an entry name read from an image can be used as one
// path component: no separators, NULs, control characters, or dot names.
func safeName(s string) bool {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\\x00") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
