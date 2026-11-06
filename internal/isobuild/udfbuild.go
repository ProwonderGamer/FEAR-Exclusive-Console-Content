package isobuild

import (
	"encoding/binary"
	"os"
)

// WriteUDF writes a minimal UDF 1.02-style image: anchor at sector 256, one
// Type-1 partition, short allocation descriptors. Enough for a reader that
// walks anchor -> volume descriptors -> file set -> directories -> files.
func WriteUDF(outPath string, files []File) error {
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	const partStart = 272
	root, err := buildTree(files)
	if err != nil {
		return err
	}

	// Allocation: every node gets an entry block; directories then get FID
	// data blocks; files get data blocks. Partition block 0 is the FSD.
	next := int64(1)
	type info struct{ entry, data, blocks int64 }
	infos := map[*tnode]*info{}
	fidLen := func(name string) int64 { return (38 + 1 + int64(len(name)) + 3) &^ 3 }
	var alloc func(n *tnode)
	alloc = func(n *tnode) {
		in := &info{entry: next}
		next++
		infos[n] = in
		if n.dir {
			sz := int64(40) // parent FID
			for _, c := range n.children {
				sz += fidLen(c.name)
			}
			n.dsize = sz
			in.blocks = (sz + sec - 1) / sec
			in.data = next
			next += in.blocks
			for _, c := range n.children {
				alloc(c)
			}
		} else {
			in.blocks = (n.size + sec - 1) / sec
			in.data = next
			next += in.blocks
		}
	}
	alloc(root)

	tag := func(b []byte, id uint16) { binary.LittleEndian.PutUint16(b, id) }
	long := func(b []byte, lbn int64) {
		le32(b, sec)
		le32(b[4:], uint32(lbn))
	}
	var write func(n *tnode, parent *tnode) error
	write = func(n *tnode, parent *tnode) error {
		in := infos[n]
		fe := make([]byte, sec)
		tag(fe, 261)
		if n.dir {
			fe[27] = 4
		} else {
			fe[27] = 5
		}
		binary.LittleEndian.PutUint64(fe[56:], uint64(map[bool]int64{true: n.dsize, false: n.size}[n.dir]))
		var ads []byte
		length := n.size
		if n.dir {
			length = n.dsize
		}
		for off := int64(0); off < length; off += 0x3FFFF800 {
			l := length - off
			if l > 0x3FFFF800 {
				l = 0x3FFFF800
			}
			ad := make([]byte, 8)
			le32(ad, uint32(l))
			le32(ad[4:], uint32(in.data+off/sec))
			ads = append(ads, ad...)
		}
		le32(fe[172:], uint32(len(ads)))
		copy(fe[176:], ads)
		if _, err := out.WriteAt(fe, (partStart+in.entry)*sec); err != nil {
			return err
		}
		if !n.dir {
			return n.file.copyTo(out, (partStart+in.data)*sec)
		}
		buf := make([]byte, in.blocks*sec)
		p := 0
		// parent FID: characteristics 0x0A (directory | parent), empty name
		tag(buf[p:], 257)
		buf[p+18] = 0x0A
		long(buf[p+20:], infos[parent].entry)
		p += 40
		for _, c := range n.children {
			tag(buf[p:], 257)
			if c.dir {
				buf[p+18] = 0x02
			}
			buf[p+19] = byte(1 + len(c.name))
			long(buf[p+20:], infos[c].entry)
			buf[p+38] = 8
			copy(buf[p+39:], c.name)
			p += int(fidLen(c.name))
		}
		if _, err := out.WriteAt(buf, (partStart+in.data)*sec); err != nil {
			return err
		}
		for _, c := range n.children {
			if err := write(c, n); err != nil {
				return err
			}
		}
		return nil
	}
	if err := write(root, root); err != nil {
		return err
	}

	avdp := make([]byte, sec)
	tag(avdp, 2)
	le32(avdp[16:], 3*sec)
	le32(avdp[20:], 257)
	pd := make([]byte, sec)
	tag(pd, 5)
	le16(pd[22:], 0)
	le32(pd[188:], partStart)
	le32(pd[192:], uint32(next))
	lvd := make([]byte, sec)
	tag(lvd, 6)
	le32(lvd[212:], sec)
	long(lvd[248:], 0)
	le32(lvd[264:], 6)
	le32(lvd[268:], 1)
	lvd[440], lvd[441] = 1, 6
	term := make([]byte, sec)
	tag(term, 8)
	fsd := make([]byte, sec)
	tag(fsd, 256)
	long(fsd[400:], infos[root].entry)
	for i, b := range [][]byte{avdp} {
		_ = i
		if _, err := out.WriteAt(b, 256*sec); err != nil {
			return err
		}
	}
	for i, b := range [][]byte{pd, lvd, term} {
		if _, err := out.WriteAt(b, int64(257+i)*sec); err != nil {
			return err
		}
	}
	if _, err := out.WriteAt(fsd, partStart*sec); err != nil {
		return err
	}
	return out.Truncate((partStart + next) * sec)
}
