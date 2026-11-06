package vfs

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Container names a disc image format.
type Container int

const (
	ISO9660 Container = iota
	XDVDFS
	UDF
)

// DefaultOrder probes ISO 9660, then XDVDFS, then UDF.
var DefaultOrder = []Container{ISO9660, XDVDFS, UDF}

func opener(c Container) func(io.ReaderAt, io.Closer) (FS, error) {
	switch c {
	case XDVDFS:
		return OpenXDVDFS
	case UDF:
		return OpenUDF
	}
	return OpenISO9660
}

// Open opens a user-selected path as a source: a directory becomes a DirFS, a
// file is probed as an ISO 9660, XDVDFS or UDF image (in that order).
func Open(path string) (FS, error) { return OpenOrdered(path, DefaultOrder, nil) }

// OpenOrdered is Open with a caller-chosen probing order and an optional
// acceptance test. A real Xbox 360 image can carry an ISO 9660 or UDF "video
// partition" in front of the XDVDFS game partition, so the first container that
// parses is not necessarily the one that holds the game. With accept set, every
// container is tried in order and the first whose tree passes accept wins; if
// none passes, the first one that parsed is returned so the caller can say
// what it found (for example "wrong disc"). With no container parsing at all
// the result is ErrUnknownImage, or an *UnsupportedError when the image uses a
// feature the readers lack.
func OpenOrdered(path string, order []Container, accept func(FS) bool) (FS, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot access %q: %w", path, err)
	}
	if st.IsDir() {
		return NewDirFS(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	var first FS
	var unsupported error
	for _, c := range order {
		fsys, err := opener(c)(f, f)
		if err != nil {
			var ue *UnsupportedError
			if errors.As(err, &ue) && unsupported == nil {
				unsupported = err
			}
			continue
		}
		if accept == nil || accept(fsys) {
			return fsys, nil
		}
		if first == nil {
			first = fsys // all candidates share f, so none is closed here
		}
	}
	if first != nil {
		return first, nil
	}
	f.Close()
	if unsupported != nil {
		return nil, unsupported
	}
	return nil, ErrUnknownImage
}

// ErrUnknownImage means the file is not a disc image this installer can read.
var ErrUnknownImage = errors.New("not a readable disc image (ISO 9660 / XDVDFS / UDF)")
