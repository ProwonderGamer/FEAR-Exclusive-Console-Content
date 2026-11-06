// Command res2syso converts a Windows resource file (.res, as written by
// llvm-rc or rc.exe) into a COFF object file (.syso) holding the resources in
// the two-section ".rsrc$01" / ".rsrc$02" form that the Go linker embeds as the
// executable's .rsrc section. It is a stand-in for
//
//	llvm-cvtres /MACHINE:X64 /TIMESTAMP:0 /OUT:x.syso x.res
//
// and follows that tool's layout conventions so that the same .res produces the
// same bytes. The output is deterministic: every timestamp is zero.
//
// Usage:
//
//	res2syso -machine x64 -in app.res -out rsrc_windows_amd64.syso
//
// # Input: the .res format
//
// A 32-bit .res file is a sequence of entries, each DWORD aligned:
//
//	u32 DataSize, u32 HeaderSize,
//	TYPE, NAME            each 0xFFFF followed by a u16 id, or a NUL
//	                      terminated UTF-16 string
//	padding to a DWORD boundary,
//	u32 DataVersion, u16 MemoryFlags, u16 LanguageId,
//	u32 Version, u32 Characteristics,
//	DataSize bytes of data, then padding to a DWORD boundary.
//
// The first entry is always an empty 32-byte marker (all zero ids).
//
// # Output: the COFF object
//
// The object has two sections and no code:
//
//	.rsrc$01  the resource directory tree (type -> name -> language), then one
//	          16-byte data entry per resource, then the UTF-16 name strings,
//	          padded to a multiple of 8 bytes. Every data entry's RVA field has
//	          an IMAGE_REL_AMD64_ADDR32NB relocation against the symbol of the
//	          resource's data.
//	.rsrc$02  the raw data of every resource, each padded to 8 bytes, in the
//	          order the resources appear in the .res file.
//
// Directory tables are written breadth first. Within a table the entries
// named by string come first (sorted), followed by the entries named by id
// (ascending), as the PE format requires. Data entries are written in the order
// the tree walk reaches them, while the relocations and the "$R" symbols
// follow the .res order.
//
// The symbol table holds "@feat.00" (absolute, value 0x11), a section symbol
// with an auxiliary record for each section, and one "$R%06d" symbol per
// resource pointing at its data in .rsrc$02. The COFF string table is four zero
// bytes (no long names are needed).
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// COFF constants used by the writer.
const (
	machineAMD64 = 0x8664

	// The characteristics llvm-cvtres writes in the file header
	// (IMAGE_FILE_32BIT_MACHINE), even for AMD64 objects.
	fileCharacteristics = 0x0100

	// IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ.
	sectionCharacteristics = 0x40000040

	// IMAGE_REL_AMD64_ADDR32NB: 32-bit address without image base.
	relAMD64Addr32NB = 0x0003

	// Fixed record sizes of the COFF format.
	fileHeaderSize    = 20
	sectionHeaderSize = 40
	relocationSize    = 10
	symbolSize        = 18

	// Resource directory record sizes.
	dirTableSize     = 16
	dirEntrySize     = 8
	dataEntrySize    = 16
	highBit          = 0x80000000
	featSymbolValue  = 0x11 // "@feat.00" flags value written by llvm-cvtres
	firstResSymbol   = 5    // symbol index of "$R000000" (after @feat.00 and 2 sections)
	maxResources     = 999999
	maxDirEntries    = 0xFFFF
	absoluteSection  = -1
	resMarkerHdrSize = 32
)

// ident is a resource type or name: either a numeric id or a UTF-16 string.
type ident struct {
	isName bool
	id     uint16
	name   []uint16 // UTF-16 code units without the terminating NUL
}

// resource is one entry of the .res file.
type resource struct {
	typ, name ident
	lang      uint16
	data      []byte
}

// parseRes splits a 32-bit .res file into its resources. The leading empty
// marker entry is checked and dropped.
func parseRes(b []byte) ([]resource, error) {
	var out []resource
	off := 0
	for off < len(b) {
		if len(b)-off < resMarkerHdrSize {
			return nil, fmt.Errorf("res: truncated entry header at offset %d", off)
		}
		dataSize := uint64(binary.LittleEndian.Uint32(b[off:]))
		hdrSize := uint64(binary.LittleEndian.Uint32(b[off+4:]))
		if hdrSize < resMarkerHdrSize || hdrSize%4 != 0 || uint64(off)+hdrSize > uint64(len(b)) {
			return nil, fmt.Errorf("res: bad header size %d at offset %d", hdrSize, off)
		}
		if uint64(off)+hdrSize+dataSize > uint64(len(b)) {
			return nil, fmt.Errorf("res: entry at offset %d runs past the end of the file", off)
		}
		hdr := b[off : off+int(hdrSize)]
		pos := 8
		typ, np, err := readIdent(hdr, pos)
		if err != nil {
			return nil, fmt.Errorf("res: entry at offset %d: type: %w", off, err)
		}
		name, np, err := readIdent(hdr, np)
		if err != nil {
			return nil, fmt.Errorf("res: entry at offset %d: name: %w", off, err)
		}
		pos = (np + 3) &^ 3
		if pos+16 > len(hdr) {
			return nil, fmt.Errorf("res: entry at offset %d: header too short", off)
		}
		lang := binary.LittleEndian.Uint16(hdr[pos+6:])
		dataOff := off + int(hdrSize)
		data := b[dataOff : dataOff+int(dataSize)]

		if off == 0 {
			if dataSize != 0 || hdrSize != resMarkerHdrSize || typ.isName || name.isName || typ.id != 0 || name.id != 0 {
				return nil, errors.New("res: file does not start with the empty marker entry (not a 32-bit .res file)")
			}
		} else {
			out = append(out, resource{typ: typ, name: name, lang: lang, data: data})
		}
		off = (dataOff + int(dataSize) + 3) &^ 3
	}
	if len(b) == 0 {
		return nil, errors.New("res: empty input")
	}
	return out, nil
}

// readIdent reads a TYPE or NAME field of a .res header starting at pos and
// returns the position just after it.
func readIdent(h []byte, pos int) (ident, int, error) {
	if pos+2 > len(h) {
		return ident{}, 0, errors.New("truncated")
	}
	if binary.LittleEndian.Uint16(h[pos:]) == 0xFFFF {
		if pos+4 > len(h) {
			return ident{}, 0, errors.New("truncated id")
		}
		return ident{id: binary.LittleEndian.Uint16(h[pos+2:])}, pos + 4, nil
	}
	var s []uint16
	for {
		if pos+2 > len(h) {
			return ident{}, 0, errors.New("unterminated string")
		}
		c := binary.LittleEndian.Uint16(h[pos:])
		pos += 2
		if c == 0 {
			break
		}
		s = append(s, c)
	}
	return ident{isName: true, name: s}, pos, nil
}

// node is a directory table of the resource tree, or a language leaf.
type node struct {
	names  map[string]*node // string-named children, keyed by nameKey
	ids    map[uint16]*node // id-named children
	strIdx int              // index into the string table (string-named nodes)
	leaf   bool
	res    int // index into the resource list (leaves)
}

// nameKey turns UTF-16 code units into a string whose byte order equals the
// code unit order, so sorting the keys sorts the names.
func nameKey(u []uint16) string {
	b := make([]byte, 0, 2*len(u))
	for _, c := range u {
		b = append(b, byte(c>>8), byte(c))
	}
	return string(b)
}

// child returns the child of n named by id, creating it (and registering a new
// string table entry for a string name) when it does not exist yet.
func (n *node) child(id ident, strs *[][]uint16) *node {
	if id.isName {
		k := nameKey(id.name)
		if c, ok := n.names[k]; ok {
			return c
		}
		if n.names == nil {
			n.names = map[string]*node{}
		}
		c := &node{strIdx: len(*strs)}
		*strs = append(*strs, id.name)
		n.names[k] = c
		return c
	}
	if c, ok := n.ids[id.id]; ok {
		return c
	}
	if n.ids == nil {
		n.ids = map[uint16]*node{}
	}
	c := &node{}
	n.ids[id.id] = c
	return c
}

// entry is one child of a directory table in output order.
type entry struct {
	isName bool
	id     uint16
	node   *node
}

// children returns the children of n in output order: names sorted, then ids
// ascending.
func (n *node) children() []entry {
	keys := make([]string, 0, len(n.names))
	for k := range n.names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]entry, 0, len(n.names)+len(n.ids))
	for _, k := range keys {
		out = append(out, entry{isName: true, node: n.names[k]})
	}
	ids := make([]int, 0, len(n.ids))
	for id := range n.ids {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, id := range ids {
		out = append(out, entry{id: uint16(id), node: n.ids[uint16(id)]})
	}
	return out
}

// treeSize returns the number of bytes the directory tables and data entries
// below (and including) n occupy.
func (n *node) treeSize() int {
	if n.leaf {
		return dataEntrySize
	}
	size := dirTableSize + dirEntrySize*(len(n.names)+len(n.ids))
	for _, c := range n.names {
		size += c.treeSize()
	}
	for _, c := range n.ids {
		size += c.treeSize()
	}
	return size
}

func align(n, a int) int { return (n + a - 1) &^ (a - 1) }

// buildTree arranges the resources into the type/name/language tree and
// returns it with the string table of all string names, in creation order.
func buildTree(res []resource) (*node, [][]uint16, error) {
	root := &node{}
	var strs [][]uint16
	for i, r := range res {
		nn := root.child(r.typ, &strs).child(r.name, &strs)
		if _, dup := nn.ids[r.lang]; dup {
			return nil, nil, fmt.Errorf("duplicate resource: type %s, name %s, language 0x%04x", r.typ, r.name, r.lang)
		}
		if nn.ids == nil {
			nn.ids = map[uint16]*node{}
		}
		nn.ids[r.lang] = &node{leaf: true, res: i}
	}
	return root, strs, nil
}

func (i ident) String() string {
	if i.isName {
		return fmt.Sprintf("%q", string(utf16ToRunes(i.name)))
	}
	return fmt.Sprintf("#%d", i.id)
}

// utf16ToRunes decodes UTF-16, replacing unpaired surrogates (message use only).
func utf16ToRunes(u []uint16) []rune {
	var out []rune
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		switch {
		case c >= 0xD800 && c < 0xDC00 && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] < 0xE000:
			out = append(out, 0x10000+(c-0xD800)<<10+rune(u[i+1])-0xDC00)
			i++
		case c >= 0xD800 && c < 0xE000:
			out = append(out, 0xFFFD)
		default:
			out = append(out, c)
		}
	}
	return out
}

// convert turns the contents of a .res file into a COFF object for machine
// x64.
func convert(resData []byte) ([]byte, error) {
	res, err := parseRes(resData)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, errors.New("res: file holds no resources")
	}
	if len(res) > maxResources {
		return nil, fmt.Errorf("too many resources (%d, limit %d)", len(res), maxResources)
	}
	root, strs, err := buildTree(res)
	if err != nil {
		return nil, err
	}
	if err := checkFanout(root); err != nil {
		return nil, err
	}

	// ---- .rsrc$01: directory tree, data entries, name strings ----
	treeSize := root.treeSize()
	strOffsets := make([]int, len(strs))
	strBytes := 0
	for i, s := range strs {
		strOffsets[i] = treeSize + strBytes
		strBytes += 2*len(s) + 2
	}
	sec1 := make([]byte, treeSize+align(strBytes, 8))
	le := binary.LittleEndian
	dataEntryAt := make([]int, len(res)) // offset of each resource's data entry

	// Breadth-first walk. next is where the next table or data entry to be
	// reserved will start.
	next := dirTableSize + dirEntrySize*(len(root.names)+len(root.ids))
	cur := 0
	queue := []*node{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		// Characteristics, TimeDateStamp and version fields stay zero.
		le.PutUint16(sec1[cur+12:], uint16(len(n.names)))
		le.PutUint16(sec1[cur+14:], uint16(len(n.ids)))
		cur += dirTableSize
		for _, c := range n.children() {
			if c.isName {
				le.PutUint32(sec1[cur:], uint32(strOffsets[c.node.strIdx])|highBit)
			} else {
				le.PutUint32(sec1[cur:], uint32(c.id))
			}
			if c.node.leaf {
				le.PutUint32(sec1[cur+4:], uint32(next))
				dataEntryAt[c.node.res] = next
				next += dataEntrySize
			} else {
				le.PutUint32(sec1[cur+4:], uint32(next)|highBit)
				next += dirTableSize + dirEntrySize*(len(c.node.names)+len(c.node.ids))
				queue = append(queue, c.node)
			}
			cur += dirEntrySize
		}
	}
	if next != treeSize {
		return nil, fmt.Errorf("internal error: tree size %d, laid out %d", treeSize, next)
	}
	for i, r := range res {
		// The RVA field stays zero: the relocation fills it in.
		le.PutUint32(sec1[dataEntryAt[i]+4:], uint32(len(r.data)))
	}
	for i, s := range strs {
		o := strOffsets[i]
		le.PutUint16(sec1[o:], uint16(len(s)))
		for j, c := range s {
			le.PutUint16(sec1[o+2+2*j:], c)
		}
	}

	// ---- .rsrc$02: resource data, each padded to 8 bytes ----
	dataOffsets := make([]int, len(res))
	sec2Size := 0
	for i, r := range res {
		dataOffsets[i] = sec2Size
		sec2Size += align(len(r.data), 8)
	}
	sec2 := make([]byte, sec2Size)
	for i, r := range res {
		copy(sec2[dataOffsets[i]:], r.data)
	}

	// ---- file layout ----
	sec1Off := fileHeaderSize + 2*sectionHeaderSize
	relocOff := sec1Off + len(sec1)
	sec2Off := align(relocOff+relocationSize*len(res), 8)
	symOff := sec2Off + len(sec2)
	nSyms := 5 + len(res)
	total := symOff + nSyms*symbolSize + 4
	if uint64(total) > 0xFFFFFFFF {
		return nil, errors.New("output exceeds 4 GiB")
	}

	var out bytes.Buffer
	out.Grow(total)
	w32 := func(v uint32) { _ = binary.Write(&out, le, v) }
	w16 := func(v uint16) { _ = binary.Write(&out, le, v) }

	// File header.
	w16(machineAMD64)
	w16(2) // sections
	w32(0) // TimeDateStamp
	w32(uint32(symOff))
	w32(uint32(nSyms))
	w16(0) // optional header size
	w16(fileCharacteristics)

	// Section headers.
	sectionHeader := func(name string, size, raw, relocs, nreloc int) {
		var nb [8]byte
		copy(nb[:], name)
		out.Write(nb[:])
		w32(0) // VirtualSize
		w32(0) // VirtualAddress
		w32(uint32(size))
		w32(uint32(raw))
		w32(uint32(relocs))
		w32(0) // PointerToLinenumbers
		w16(uint16(nreloc))
		w16(0) // NumberOfLinenumbers
		w32(sectionCharacteristics)
	}
	sectionHeader(".rsrc$01", len(sec1), sec1Off, relocOff, len(res))
	sectionHeader(".rsrc$02", len(sec2), sec2Off, 0, 0)

	out.Write(sec1)

	// Relocations: one per resource, in .res order.
	for i := range res {
		w32(uint32(dataEntryAt[i]))
		w32(uint32(firstResSymbol + i))
		w16(relAMD64Addr32NB)
	}
	out.Write(make([]byte, sec2Off-out.Len()))
	out.Write(sec2)

	// Symbol table.
	symbol := func(name string, value uint32, section int16, aux byte) {
		var nb [8]byte
		copy(nb[:], name)
		out.Write(nb[:])
		w32(value)
		w16(uint16(section))
		w16(0)           // type: not a function
		out.WriteByte(3) // storage class: IMAGE_SYM_CLASS_STATIC
		out.WriteByte(aux)
	}
	sectionAux := func(length, nreloc int) {
		w32(uint32(length))
		w16(uint16(nreloc))
		w16(0)                        // NumberOfLinenumbers
		w32(0)                        // CheckSum
		w16(0)                        // Number
		out.Write([]byte{0, 0, 0, 0}) // Selection plus padding to 18 bytes
	}
	symbol("@feat.00", featSymbolValue, absoluteSection, 0)
	symbol(".rsrc$01", 0, 1, 1)
	sectionAux(len(sec1), len(res))
	symbol(".rsrc$02", 0, 2, 1)
	sectionAux(len(sec2), 0)
	for i := range res {
		symbol(fmt.Sprintf("$R%06d", i), uint32(dataOffsets[i]), 2, 0)
	}
	// String table: four zero bytes, as llvm-cvtres writes it. All symbol
	// names fit the 8-byte inline field, so no string is ever stored.
	w32(0)

	if out.Len() != total {
		return nil, fmt.Errorf("internal error: wrote %d bytes, expected %d", out.Len(), total)
	}
	return out.Bytes(), nil
}

// checkFanout verifies that no table has more entries than its 16-bit count
// fields can hold.
func checkFanout(n *node) error {
	if n.leaf {
		return nil
	}
	if len(n.names) > maxDirEntries || len(n.ids) > maxDirEntries {
		return errors.New("resource directory has too many entries")
	}
	for _, c := range n.names {
		if err := checkFanout(c); err != nil {
			return err
		}
	}
	for _, c := range n.ids {
		if err := checkFanout(c); err != nil {
			return err
		}
	}
	return nil
}

// run implements the command line; it returns the process exit code.
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("res2syso", flag.ContinueOnError)
	fs.SetOutput(stderr)
	machine := fs.String("machine", "", "target machine (only x64 is supported)")
	in := fs.String("in", "", "input .res file")
	outPath := fs.String("out", "", "output .syso file")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: res2syso -machine x64 -in app.res -out rsrc_windows_amd64.syso")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *in == "" || *outPath == "" || *machine == "" || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	switch strings.ToLower(*machine) {
	case "x64", "amd64":
	default:
		fmt.Fprintf(stderr, "res2syso: unsupported machine %q: only x64 is supported\n", *machine)
		return 2
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintf(stderr, "res2syso: %v\n", err)
		return 1
	}
	obj, err := convert(data)
	if err != nil {
		fmt.Fprintf(stderr, "res2syso: %s: %v\n", *in, err)
		return 1
	}
	if err := os.WriteFile(*outPath, obj, 0o644); err != nil {
		fmt.Fprintf(stderr, "res2syso: %v\n", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}
