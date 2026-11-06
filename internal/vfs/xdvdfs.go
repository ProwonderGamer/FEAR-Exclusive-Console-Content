package vfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// XDVDFS (Xbox DVD file system, "GDF") is what Xbox 360 game discs use.
// The game partition does not start at byte 0 of a full disc image: the
// XGD generation decides where it begins. Extracted/trimmed ISOs start at 0.
const (
	xdSector = 2048
	xdMagic  = "MICROSOFT*XBOX*MEDIA"
	// The volume descriptor lives 32 sectors into the game partition.
	xdVDOffset = 32 * xdSector
)

// XDVDFSLayouts lists the known game-partition start offsets. The offsets are
// the commonly documented ones (XGD1 0x18300000, XGD2 0x0FD90000, XGD3
// 0x02080000); they have not been checked against a real retail image here.
var XDVDFSLayouts = []struct {
	Name   string
	Offset int64
}{
	{"raw", 0},
	{"XGD3", 0x02080000},
	{"XGD2", 0x0FD90000},
	{"XGD1", 0x18300000},
}

// OpenXDVDFS looks for an XDVDFS volume at every known partition offset.
func OpenXDVDFS(img io.ReaderAt, c io.Closer) (FS, error) {
	size := imageSize(img)
	var lastErr error
	for _, l := range XDVDFSLayouts {
		vd := make([]byte, xdSector)
		if _, err := img.ReadAt(vd, l.Offset+xdVDOffset); err != nil {
			continue
		}
		// Magic appears at the start and again at 0x7EC of the descriptor.
		if !bytes.Equal(vd[:20], []byte(xdMagic)) || !bytes.Equal(vd[0x7EC:0x7EC+20], []byte(xdMagic)) {
			continue
		}
		rootSec := int64(binary.LittleEndian.Uint32(vd[0x14:]))
		rootSize := int64(binary.LittleEndian.Uint32(vd[0x18:]))
		st := &xdState{img: img, part: l.Offset, size: size, dirs: map[int64]bool{}}
		root := &node{dir: true}
		if err := st.readDir(rootSec, rootSize, root, 0); err != nil {
			lastErr = err
			continue
		}
		return &treeFS{img: img, root: root, kind: "XDVDFS (" + l.Name + ")", c: c}, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("no XDVDFS volume descriptor found")
}

type xdState struct {
	img    io.ReaderAt
	part   int64
	size   int64
	dirs   map[int64]bool // directory sectors already loaded (whole image)
	budget budget
}

// readDir loads a directory. Entries form a binary search tree stored as
// dword offsets: {u16 left, u16 right, u32 sector, u32 size, u8 attr, u8 nameLen, name}.
func (s *xdState) readDir(sec, size int64, dir *node, depth int) error {
	if depth > maxDepth {
		return errors.New("directory nesting too deep")
	}
	if size == 0 {
		return nil
	}
	if size < 0 || size > maxDirBytes {
		return ErrCorrupt
	}
	if s.dirs[sec] { // a directory is referenced once; a second reference is a loop or a DAG
		return ErrCorrupt
	}
	s.dirs[sec] = true
	if err := s.budget.take(); err != nil {
		return err
	}
	buf, err := readRange(s.img, s.part+sec*xdSector, size, s.size)
	if err != nil {
		return err
	}
	visited := map[int]bool{}
	var walk func(off int) error
	walk = func(off int) error {
		if off < 0 || off+14 > len(buf) || visited[off] {
			return nil
		}
		visited[off] = true
		left := int(binary.LittleEndian.Uint16(buf[off:])) * 4
		right := int(binary.LittleEndian.Uint16(buf[off+2:])) * 4
		if left == 0xFFFF*4 && right == 0xFFFF*4 {
			// 0xFFFF in both links marks an empty (padding-filled) directory.
			return nil
		}
		eSec := int64(binary.LittleEndian.Uint32(buf[off+4:]))
		eSize := int64(binary.LittleEndian.Uint32(buf[off+8:]))
		attr := buf[off+12]
		nl := int(buf[off+13])
		if off+14+nl > len(buf) {
			return nil
		}
		name := string(buf[off+14 : off+14+nl])
		if safeName(name) {
			if err := s.budget.take(); err != nil {
				return err
			}
			n := &node{name: name}
			if attr&0x10 != 0 {
				n.dir = true
				if err := s.readDir(eSec, eSize, n, depth+1); err != nil {
					if s.budget.nodes > maxNodes {
						return err
					}
					n = nil // damaged sub-directory: leave it out
				}
			} else {
				n.size = eSize
				n.extents = []extent{{off: s.part + eSec*xdSector, length: eSize}}
			}
			if n != nil {
				dir.children = append(dir.children, n)
			}
		}
		if left != 0 {
			if err := walk(left); err != nil {
				return err
			}
		}
		if right != 0 {
			return walk(right)
		}
		return nil
	}
	return walk(0)
}
