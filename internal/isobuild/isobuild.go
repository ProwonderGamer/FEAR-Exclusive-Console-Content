// Package isobuild writes small, valid-enough ISO 9660, XDVDFS and UDF images.
// It exists only so the tests can build synthetic disc images from reference
// files without needing external tools (genisoimage, xorriso...). The installer
// itself never uses it.
package isobuild

import (
	"encoding/binary"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

// File is one file to place in an image: either a disk file (Src) or bytes (Data).
type File struct {
	Path string // e.g. "PS3_GAME/PARAM.SFO"
	Src  string
	Data []byte
}

const sec = 2048

type tnode struct {
	name     string
	dir      bool
	file     *File
	size     int64
	children []*tnode
	// layout results
	lba   int64 // first sector of directory data or file data
	dsize int64 // directory byte size
}

func (f *File) size() (int64, error) {
	if f.Src != "" {
		st, err := os.Stat(f.Src)
		if err != nil {
			return 0, err
		}
		return st.Size(), nil
	}
	return int64(len(f.Data)), nil
}

// copyTo streams the file content into w at offset off. All-zero 1 MiB chunks
// are skipped (leaving holes) so images containing big empty stand-in files
// stay sparse on disk; callers truncate the image to its full length.
func (f *File) copyTo(w io.WriterAt, off int64) error {
	if f.Src == "" {
		_, err := w.WriteAt(f.Data, off)
		return err
	}
	in, err := os.Open(f.Src)
	if err != nil {
		return err
	}
	defer in.Close()
	buf := make([]byte, 1<<20)
	for pos := int64(0); ; {
		n, rerr := in.Read(buf)
		if n > 0 {
			if !allZero(buf[:n]) {
				if _, err := w.WriteAt(buf[:n], off+pos); err != nil {
					return err
				}
			}
			pos += int64(n)
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

func buildTree(files []File) (*tnode, error) {
	root := &tnode{dir: true}
	for i := range files {
		f := &files[i]
		cur := root
		parts := strings.Split(strings.Trim(f.Path, "/"), "/")
		for j, p := range parts {
			var next *tnode
			for _, c := range cur.children {
				if c.name == p {
					next = c
				}
			}
			if next == nil {
				next = &tnode{name: p, dir: j < len(parts)-1}
				cur.children = append(cur.children, next)
			}
			cur = next
		}
		sz, err := f.size()
		if err != nil {
			return nil, err
		}
		cur.file, cur.size = f, sz
	}
	var sortRec func(n *tnode)
	sortRec = func(n *tnode) {
		sort.Slice(n.children, func(a, b int) bool {
			return strings.ToUpper(n.children[a].name) < strings.ToUpper(n.children[b].name)
		})
		for _, c := range n.children {
			sortRec(c)
		}
	}
	sortRec(root)
	return root, nil
}

func roundUp(n, a int64) int64 { return (n + a - 1) / a * a }

func le32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }
func le16(b []byte, v uint16) { binary.LittleEndian.PutUint16(b, v) }

// ------------------------------------------------------------ ISO 9660 ---

// ISO9660Options tweaks the generated image.
type ISO9660Options struct {
	SystemArea []byte // placed at byte 0 (e.g. a PS3 encryption region table)
	MaxExtent  int64  // if >0, split files into extents of this size (multi-extent test)
}

// WriteISO9660 writes an ISO 9660 image (PVD only, names stored as given).
func WriteISO9660(outPath string, files []File, opt ISO9660Options) error {
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	root, err := buildTree(files)
	if err != nil {
		return err
	}

	dirRecLen := func(name string) int { return int((33+len(name))+1) &^ 1 }
	// Directory sizes: pack records without crossing sectors.
	var sizeDir func(n *tnode)
	sizeDir = func(n *tnode) {
		pos := int64(0)
		add := func(l int) {
			if pos%sec+int64(l) > sec {
				pos = roundUp(pos, sec)
			}
			pos += int64(l)
		}
		add(34)
		add(34)
		for _, c := range n.children {
			ext := 1
			if !c.dir && opt.MaxExtent > 0 && c.size > opt.MaxExtent {
				ext = int((c.size + opt.MaxExtent - 1) / opt.MaxExtent)
			}
			for i := 0; i < ext; i++ {
				add(dirRecLen(c.name))
			}
			if c.dir {
				sizeDir(c)
			}
		}
		n.dsize = roundUp(pos, sec)
	}
	sizeDir(root)
	next := int64(20) // sectors 0..19 reserved: system area, PVD, terminator, spare
	var assign func(n *tnode)
	assign = func(n *tnode) {
		n.lba = next
		next += n.dsize / sec
		for _, c := range n.children {
			if c.dir {
				assign(c)
			}
		}
	}
	assign(root)
	var place func(n *tnode)
	place = func(n *tnode) {
		for _, c := range n.children {
			if c.dir {
				place(c)
			} else {
				c.lba = next
				next += (c.size + sec - 1) / sec
				if c.size == 0 {
					next++
				}
			}
		}
	}
	place(root)
	total := next

	rec := func(name string, lba, size int64, dir bool, more bool) []byte {
		l := dirRecLen(name)
		b := make([]byte, l)
		b[0] = byte(l)
		le32(b[2:], uint32(lba))
		binary.BigEndian.PutUint32(b[6:], uint32(lba))
		le32(b[10:], uint32(size))
		binary.BigEndian.PutUint32(b[14:], uint32(size))
		if dir {
			b[25] = 2
		}
		if more {
			b[25] |= 0x80
		}
		b[28] = 1
		b[32] = byte(len(name))
		copy(b[33:], name)
		return b
	}
	dot := func(lba, size int64, c byte) []byte {
		b := rec(string([]byte{c}), lba, size, true, false)
		return b
	}
	var writeDir func(n *tnode, parent *tnode) error
	writeDir = func(n *tnode, parent *tnode) error {
		buf := make([]byte, n.dsize)
		pos := 0
		put := func(r []byte) {
			if pos%sec+len(r) > sec {
				pos = (pos/sec + 1) * sec
			}
			copy(buf[pos:], r)
			pos += len(r)
		}
		put(dot(n.lba, n.dsize, 0))
		put(dot(parent.lba, parent.dsize, 1))
		for _, c := range n.children {
			if c.dir {
				put(rec(c.name, c.lba, c.dsize, true, false))
				continue
			}
			if opt.MaxExtent > 0 && c.size > opt.MaxExtent {
				for off := int64(0); off < c.size; off += opt.MaxExtent {
					l := opt.MaxExtent
					if c.size-off < l {
						l = c.size - off
					}
					put(rec(c.name, c.lba+off/sec, l, false, off+l < c.size))
				}
			} else {
				put(rec(c.name, c.lba, c.size, false, false))
			}
		}
		if _, err := out.WriteAt(buf, n.lba*sec); err != nil {
			return err
		}
		for _, c := range n.children {
			if c.dir {
				if err := writeDir(c, n); err != nil {
					return err
				}
			} else if err := c.file.copyTo(out, c.lba*sec); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeDir(root, root); err != nil {
		return err
	}

	pvd := make([]byte, sec)
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	pvd[6] = 1
	copy(pvd[8:], padRight("TEST", 32))
	copy(pvd[40:], padRight("TESTVOL", 32))
	le32(pvd[80:], uint32(total))
	binary.BigEndian.PutUint32(pvd[84:], uint32(total))
	le16(pvd[120:], 1)
	le16(pvd[124:], 1)
	le16(pvd[128:], sec)
	copy(pvd[156:], rec(string([]byte{0}), root.lba, root.dsize, true, false)[:34])
	pvd[156] = 34
	pvd[881] = 1
	if _, err := out.WriteAt(pvd, 16*sec); err != nil {
		return err
	}
	term := make([]byte, sec)
	term[0] = 255
	copy(term[1:], "CD001")
	term[6] = 1
	if _, err := out.WriteAt(term, 17*sec); err != nil {
		return err
	}
	if len(opt.SystemArea) > 0 {
		if _, err := out.WriteAt(opt.SystemArea, 0); err != nil {
			return err
		}
	}
	return out.Truncate(total * sec)
}

func padRight(s string, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	copy(b, s)
	return b
}

// -------------------------------------------------------------- XDVDFS ---

// WriteXDVDFS writes an Xbox 360 XDVDFS image whose game partition starts at
// partOffset (0 for raw, 0x02080000 XGD3, 0x0FD90000 XGD2).
func WriteXDVDFS(outPath string, files []File, partOffset int64) error {
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	root, err := buildTree(files)
	if err != nil {
		return err
	}

	entLen := func(n *tnode) int { return int(roundUp(int64(14+len(n.name)), 4)) }
	var sizeDir func(n *tnode)
	sizeDir = func(n *tnode) {
		s := int64(0)
		for _, c := range n.children {
			s += int64(entLen(c))
			if c.dir {
				sizeDir(c)
			}
		}
		n.dsize = roundUp(max64(s, 1), sec)
	}
	sizeDir(root)
	next := int64(33)
	var assign func(n *tnode)
	assign = func(n *tnode) {
		n.lba = next
		next += n.dsize / sec
		for _, c := range n.children {
			if c.dir {
				assign(c)
			}
		}
	}
	assign(root)
	var place func(n *tnode)
	place = func(n *tnode) {
		for _, c := range n.children {
			if c.dir {
				place(c)
			} else {
				c.lba = next
				next += (c.size + sec - 1) / sec
			}
		}
	}
	place(root)

	var writeDir func(n *tnode) error
	writeDir = func(n *tnode) error {
		buf := make([]byte, n.dsize)
		for i := range buf {
			buf[i] = 0xFF // unused space is 0xFF-filled
		}
		// Build a balanced binary search tree over the (sorted) children and
		// emit its nodes in pre-order, so the tree root sits at offset 0, which
		// is where readers start.
		offs := map[*tnode]int{}
		pos := 0
		var assignOffs func(sub []*tnode)
		assignOffs = func(sub []*tnode) {
			if len(sub) == 0 {
				return
			}
			m := len(sub) / 2
			offs[sub[m]] = pos
			pos += entLen(sub[m])
			assignOffs(sub[:m])
			assignOffs(sub[m+1:])
		}
		assignOffs(n.children)
		var emit func(sub []*tnode)
		emit = func(sub []*tnode) {
			if len(sub) == 0 {
				return
			}
			m := len(sub) / 2
			c := sub[m]
			o := offs[c]
			for i := o; i < o+entLen(c); i++ {
				buf[i] = 0
			}
			if m > 0 {
				le16(buf[o:], uint16(offs[sub[:m][len(sub[:m])/2]]/4))
			}
			if m < len(sub)-1 {
				r := sub[m+1:]
				le16(buf[o+2:], uint16(offs[r[len(r)/2]]/4))
			}
			le32(buf[o+4:], uint32(c.lba))
			if c.dir {
				le32(buf[o+8:], uint32(c.dsize))
				buf[o+12] = 0x10
			} else {
				le32(buf[o+8:], uint32(c.size))
				buf[o+12] = 0x20
			}
			buf[o+13] = byte(len(c.name))
			copy(buf[o+14:], c.name)
			emit(sub[:m])
			emit(sub[m+1:])
		}
		emit(n.children)
		if _, err := out.WriteAt(buf, partOffset+n.lba*sec); err != nil {
			return err
		}
		for _, c := range n.children {
			if c.dir {
				if err := writeDir(c); err != nil {
					return err
				}
			} else if err := c.file.copyTo(out, partOffset+c.lba*sec); err != nil {
				return err
			}
		}
		return nil
	}
	if err := writeDir(root); err != nil {
		return err
	}
	vd := make([]byte, sec)
	copy(vd, "MICROSOFT*XBOX*MEDIA")
	le32(vd[0x14:], uint32(root.lba))
	le32(vd[0x18:], uint32(root.dsize))
	copy(vd[0x7EC:], "MICROSOFT*XBOX*MEDIA")
	if _, err := out.WriteAt(vd, partOffset+32*sec); err != nil {
		return err
	}
	return out.Truncate(partOffset + next*sec)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// base name helper kept for symmetry with path package use in tests.
var _ = path.Base
