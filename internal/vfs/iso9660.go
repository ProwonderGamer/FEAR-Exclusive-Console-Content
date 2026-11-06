package vfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

const isoSector = 2048

// OpenISO9660 parses an ISO 9660 image (the format of PS3 disc images).
// A Joliet tree is preferred when present because it keeps long names and
// original case; otherwise the primary tree is used.
func OpenISO9660(img io.ReaderAt, c io.Closer) (FS, error) {
	var pvd, joliet []byte
	for lba := int64(16); lba < 48; lba++ {
		d := make([]byte, isoSector)
		if _, err := img.ReadAt(d, lba*isoSector); err != nil {
			break
		}
		if string(d[1:6]) != "CD001" {
			if lba == 16 {
				return nil, errors.New("no ISO 9660 signature")
			}
			continue
		}
		switch d[0] {
		case 1:
			if pvd == nil {
				pvd = d
			}
		case 2:
			esc := string(d[88:91])
			if esc == "%/@" || esc == "%/C" || esc == "%/E" {
				joliet = d
			}
		case 255:
			lba = 48
		}
	}
	if pvd == nil {
		return nil, errors.New("no primary volume descriptor")
	}
	size := imageSize(img)
	for _, v := range []struct {
		d  []byte
		ju bool
	}{{joliet, true}, {pvd, false}} {
		if v.d == nil {
			continue
		}
		st := &isoState{img: img, size: size, joliet: v.ju, seen: map[int64]bool{}}
		root, err := st.parseDir(v.d[156:156+34], 0)
		if err != nil {
			continue
		}
		root.name = ""
		kind := "ISO 9660"
		if v.ju {
			kind += " (Joliet)"
		}
		return &treeFS{img: img, root: root, kind: kind, c: c}, nil
	}
	return nil, errors.New("cannot read ISO 9660 directory tree")
}

// isoState carries the per-image parse state: a visited set shared by the
// whole walk (an extent is used by one directory only, so loops and DAGs end
// at once) and the entry budget.
type isoState struct {
	img    io.ReaderAt
	size   int64
	joliet bool
	seen   map[int64]bool
	budget budget
}

// parseDir builds a node from a directory record and, recursively, its children.
func (s *isoState) parseDir(rec []byte, depth int) (*node, error) {
	if depth > maxDepth {
		return nil, errors.New("directory nesting too deep")
	}
	lba := int64(binary.LittleEndian.Uint32(rec[2:6]))
	dsize := int64(binary.LittleEndian.Uint32(rec[10:14]))
	if s.seen[lba] || dsize <= 0 || dsize > maxDirBytes {
		return nil, ErrCorrupt
	}
	s.seen[lba] = true
	if err := s.budget.take(); err != nil {
		return nil, err
	}
	buf, err := readRange(s.img, lba*isoSector, dsize, s.size)
	if err != nil {
		return nil, err
	}
	dir := &node{dir: true}
	var last *node // for multi-extent continuation
	for pos := 0; pos < len(buf); {
		l := int(buf[pos])
		if l == 0 { // padding up to the next sector
			pos = (pos/isoSector + 1) * isoSector
			continue
		}
		if l < 34 || pos+l > len(buf) {
			break
		}
		r := buf[pos : pos+l]
		pos += l
		nl := int(r[32])
		if 33+nl > l {
			continue
		}
		raw := r[33 : 33+nl]
		if nl == 1 && (raw[0] == 0 || raw[0] == 1) { // "." and ".."
			continue
		}
		name := decodeISOName(raw, s.joliet)
		if !safeName(name) {
			last = nil
			continue
		}
		flags := r[25]
		eLBA := int64(binary.LittleEndian.Uint32(r[2:6]))
		eLen := int64(binary.LittleEndian.Uint32(r[10:14]))
		if flags&2 != 0 {
			sub, err := s.parseDir(r, depth+1)
			if err != nil {
				if s.budget.nodes > maxNodes {
					return nil, err // budget exhausted: stop the whole walk
				}
				continue
			}
			sub.name = name
			dir.children = append(dir.children, sub)
			continue
		}
		if err := s.budget.take(); err != nil {
			return nil, err
		}
		ex := extent{off: eLBA * isoSector, length: eLen}
		if last != nil && last.name == name { // continuation of a multi-extent file
			last.extents = append(last.extents, ex)
			last.size += eLen
		} else {
			last = &node{name: name, size: eLen, extents: []extent{ex}}
			dir.children = append(dir.children, last)
		}
		if flags&0x80 == 0 { // last extent of this file
			last = nil
		}
	}
	return dir, nil
}

func decodeISOName(raw []byte, joliet bool) string {
	var s string
	if joliet {
		u := make([]uint16, len(raw)/2)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(raw[2*i:])
		}
		s = string(utf16.Decode(u))
	} else {
		s = string(raw)
	}
	if i := strings.LastIndexByte(s, ';'); i >= 0 { // strip ";1" version
		s = s[:i]
	}
	return strings.TrimSuffix(s, ".")
}

// String implements fmt.Stringer (used in test failure messages).
func (n *node) String() string { return fmt.Sprintf("%s(%d)", n.name, n.size) }
