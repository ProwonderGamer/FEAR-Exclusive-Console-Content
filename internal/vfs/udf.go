package vfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf16"
)

// OpenUDF reads a UDF volume read-only. Only what is needed to list
// directories and map file data to byte extents is implemented.
//
// Known limitations (an image using one of them is refused with an
// *UnsupportedError rather than misread):
//   - 2048-byte logical blocks only (the size of every optical disc);
//   - no Virtual (VAT) or Metadata partitions (UDF 2.01+ on rewritable or BD
//     media); sparable partitions are read as plain ones, no sparing table;
//   - no allocation-descriptor continuation chains (very fragmented files);
//   - no named streams, extended attributes or permissions.
//
// Extents that are allocated or free but "not recorded" read as zeros.
func OpenUDF(img io.ReaderAt, c io.Closer) (FS, error) {
	u := &udfReader{img: img, size: imageSize(img), bs: 2048, partStart: map[uint16]int64{}, dirs: map[[2]uint32]bool{}}
	// Anchor Volume Descriptor Pointer: sector 256.
	avdp, err := u.sector(256)
	if err != nil || binary.LittleEndian.Uint16(avdp) != 2 {
		return nil, errors.New("no UDF anchor")
	}
	vdsLen := int64(binary.LittleEndian.Uint32(avdp[16:]))
	vdsLoc := int64(binary.LittleEndian.Uint32(avdp[20:]))
	var fsd longAD
	var maps []uint16 // partition reference number -> partition number
	sawLVD := false
	for s := int64(0); s < vdsLen/2048 && s < 64; s++ {
		d, err := u.sector(vdsLoc + s)
		if err != nil {
			break
		}
		switch binary.LittleEndian.Uint16(d) {
		case 5: // partition descriptor
			num := binary.LittleEndian.Uint16(d[22:])
			u.partStart[num] = int64(binary.LittleEndian.Uint32(d[188:]))
		case 6: // logical volume descriptor
			sawLVD = true
			if bs := binary.LittleEndian.Uint32(d[212:]); bs != 2048 {
				return nil, &UnsupportedError{"UDF logical block size other than 2048"}
			}
			fsd = parseLongAD(d[248:])
			mapLen := int(binary.LittleEndian.Uint32(d[264:]))
			nMaps := int(binary.LittleEndian.Uint32(d[268:]))
			if mapLen < 0 || mapLen > len(d)-440 || nMaps < 0 || nMaps > 64 {
				return nil, ErrCorrupt
			}
			p := 440
			for i := 0; i < nMaps && p+2 <= 440+mapLen; i++ {
				typ, l := d[p], int(d[p+1])
				if l < 2 || p+l > 440+mapLen {
					return nil, ErrCorrupt
				}
				switch {
				case typ == 1 && l >= 6:
					maps = append(maps, binary.LittleEndian.Uint16(d[p+4:]))
				case typ == 2 && l >= 40:
					// Type 2 carries an identifier: sparable maps read like plain
					// ones; virtual and metadata maps need tables we do not read.
					id := d[p+5 : p+5+23]
					switch {
					case bytes.Contains(id, []byte("*UDF Virtual")):
						return nil, &UnsupportedError{"UDF virtual partition (VAT)"}
					case bytes.Contains(id, []byte("*UDF Metadata")):
						return nil, &UnsupportedError{"UDF metadata partition"}
					}
					maps = append(maps, binary.LittleEndian.Uint16(d[p+38:]))
				default:
					maps = append(maps, 0xFFFF)
				}
				p += l
			}
		case 8:
			s = 1 << 20
		}
	}
	if !sawLVD || len(u.partStart) == 0 || len(maps) == 0 {
		return nil, errors.New("incomplete UDF volume descriptors")
	}
	u.maps = maps
	fsdBuf, err := u.block(fsd.part, fsd.lbn)
	if err != nil || binary.LittleEndian.Uint16(fsdBuf) != 256 {
		return nil, errors.New("no UDF file set descriptor")
	}
	rootICB := parseLongAD(fsdBuf[400:])
	root, err := u.readEntry(rootICB, "", 0)
	if err != nil || !root.dir {
		var ue *UnsupportedError
		if errors.As(err, &ue) {
			return nil, err
		}
		return nil, errors.New("cannot read UDF root directory")
	}
	return &treeFS{img: img, root: root, kind: "UDF", c: c}, nil
}

type longAD struct {
	length uint32
	lbn    uint32
	part   uint16
}

func parseLongAD(b []byte) longAD {
	return longAD{binary.LittleEndian.Uint32(b) & 0x3FFFFFFF, binary.LittleEndian.Uint32(b[4:]), binary.LittleEndian.Uint16(b[8:])}
}

type udfReader struct {
	img       io.ReaderAt
	size      int64
	bs        int64
	partStart map[uint16]int64
	maps      []uint16
	dirs      map[[2]uint32]bool // directories already read (loop / DAG guard)
	budget    budget
}

func (u *udfReader) sector(n int64) ([]byte, error) {
	b := make([]byte, 2048)
	_, err := u.img.ReadAt(b, n*2048)
	return b, err
}

// abs converts (partition reference, logical block) to an absolute byte offset.
func (u *udfReader) abs(part uint16, lbn uint32) (int64, error) {
	if int(part) >= len(u.maps) {
		return 0, errors.New("bad partition reference")
	}
	st, ok := u.partStart[u.maps[part]]
	if !ok {
		return 0, errors.New("unknown partition")
	}
	return (st + int64(lbn)) * u.bs, nil
}

func (u *udfReader) block(part uint16, lbn uint32) ([]byte, error) {
	off, err := u.abs(part, lbn)
	if err != nil {
		return nil, err
	}
	b := make([]byte, u.bs)
	_, err = u.img.ReadAt(b, off)
	return b, err
}

// readEntry reads the (Extended) File Entry at icb and returns a node.
func (u *udfReader) readEntry(icb longAD, name string, depth int) (*node, error) {
	if depth > maxDepth {
		return nil, errors.New("nesting too deep")
	}
	if err := u.budget.take(); err != nil {
		return nil, err
	}
	b, err := u.block(icb.part, icb.lbn)
	if err != nil {
		return nil, err
	}
	tag := binary.LittleEndian.Uint16(b)
	var eaLen, adLen, hdr int
	switch tag {
	case 261:
		eaLen, adLen, hdr = int(binary.LittleEndian.Uint32(b[168:])), int(binary.LittleEndian.Uint32(b[172:])), 176
	case 266:
		eaLen, adLen, hdr = int(binary.LittleEndian.Uint32(b[208:])), int(binary.LittleEndian.Uint32(b[212:])), 216
	default:
		return nil, errors.New("not a file entry")
	}
	if eaLen < 0 || adLen < 0 || hdr+eaLen > len(b) {
		return nil, ErrCorrupt
	}
	info := int64(binary.LittleEndian.Uint64(b[56:]))
	if info < 0 || info > 1<<44 { // negative as signed, or beyond any real file
		return nil, ErrCorrupt
	}
	ftype := b[27]
	allocType := binary.LittleEndian.Uint16(b[34:]) & 7
	ads := b[hdr+eaLen:]
	if adLen > len(ads) {
		adLen = len(ads)
	}
	ads = ads[:adLen]
	entryOff, _ := u.abs(icb.part, icb.lbn)

	var exts []extent
	switch allocType {
	case 0, 1: // short / long allocation descriptors
		step := 8
		if allocType == 1 {
			step = 16
		}
		for p := 0; p+step <= len(ads); p += step {
			l := binary.LittleEndian.Uint32(ads[p:])
			kind, length := l>>30, int64(l&0x3FFFFFFF)
			if kind == 3 {
				return nil, &UnsupportedError{"UDF allocation continuation"}
			}
			if length == 0 {
				break
			}
			if kind != 0 { // allocated/unallocated but not recorded: zeros
				exts = append(exts, extent{off: -1, length: length})
				continue
			}
			part := icb.part
			lbn := binary.LittleEndian.Uint32(ads[p+4:])
			if allocType == 1 {
				part = binary.LittleEndian.Uint16(ads[p+8:])
			}
			off, err := u.abs(part, lbn)
			if err != nil {
				return nil, err
			}
			exts = append(exts, extent{off: off, length: length})
		}
	case 2: // data embedded in the entry itself
		exts = []extent{{off: entryOff + int64(hdr+eaLen), length: int64(adLen)}}
	default:
		return nil, errors.New("unsupported UDF allocation type")
	}

	n := &node{name: name, dir: ftype == 4}
	if !n.dir {
		n.size = info
		// Clip extents to the logical size.
		rem := info
		for _, e := range exts {
			if e.length > rem {
				e.length = rem
			}
			if e.length > 0 {
				n.extents = append(n.extents, e)
			}
			rem -= e.length
		}
		return n, nil
	}

	// Directory: it is read once. A second reference is a loop or a shared
	// directory, which a sane disc does not have.
	key := [2]uint32{uint32(icb.part), icb.lbn}
	if u.dirs[key] {
		return nil, ErrCorrupt
	}
	u.dirs[key] = true
	var total int64
	for _, e := range exts {
		total += e.length
	}
	if info > total {
		info = total
	}
	if info > maxDirBytes {
		return nil, ErrCorrupt
	}
	f := &extentFile{img: u.img, ext: exts, size: info}
	data := make([]byte, 0, min(info, readChunk))
	for int64(len(data)) < info {
		c := min(info-int64(len(data)), readChunk)
		start := len(data)
		data = append(data, make([]byte, c)...)
		if got, err := f.ReadAt(data[start:], int64(start)); err != nil {
			if err != io.EOF || got < len(data)-start {
				data = data[:start+got]
				break
			}
		}
	}
	for p := 0; p+38 <= len(data); {
		if binary.LittleEndian.Uint16(data[p:]) != 257 {
			break
		}
		chars := data[p+18]
		fil := int(data[p+19])
		child := parseLongAD(data[p+20:])
		liu := int(binary.LittleEndian.Uint16(data[p+36:]))
		total := (38 + liu + fil + 3) &^ 3
		if p+38+liu+fil <= len(data) && chars&0x0C == 0 { // skip parent (0x08) and deleted (0x04) entries
			if nm := decodeUDFName(data[p+38+liu : p+38+liu+fil]); safeName(nm) {
				c, err := u.readEntry(child, nm, depth+1)
				if err == nil {
					n.children = append(n.children, c)
				} else if u.budget.nodes > maxNodes {
					return nil, err
				}
			}
		}
		p += total
	}
	return n, nil
}

// decodeUDFName decodes a dstring-like name with a leading compression ID.
func decodeUDFName(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	switch b[0] {
	case 8:
		return string(b[1:])
	case 16:
		u := make([]uint16, (len(b)-1)/2)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(b[1+2*i:])
		}
		return string(utf16.Decode(u))
	}
	return ""
}
