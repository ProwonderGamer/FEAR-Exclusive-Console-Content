package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

var le = binary.LittleEndian

// ---- helpers that build a .res file ----

func idField(id ident) []byte {
	if !id.isName {
		return []byte{0xFF, 0xFF, byte(id.id), byte(id.id >> 8)}
	}
	var b []byte
	for _, c := range id.name {
		b = append(b, byte(c), byte(c>>8))
	}
	return append(b, 0, 0)
}

func num(id uint16) ident    { return ident{id: id} }
func str(s string) ident     { return ident{isName: true, name: utf16.Encode([]rune(s))} }
func padTo4(b []byte) []byte { return append(b, make([]byte, -len(b)&3)...) }

// resEntry encodes one .res entry.
func resEntry(typ, name ident, lang uint16, data []byte) []byte {
	hdr := append([]byte{0, 0, 0, 0, 0, 0, 0, 0}, idField(typ)...)
	hdr = append(hdr, idField(name)...)
	hdr = padTo4(hdr)
	suffix := make([]byte, 16)
	le.PutUint16(suffix[4:], 0x30) // MemoryFlags
	le.PutUint16(suffix[6:], lang)
	hdr = append(hdr, suffix...)
	le.PutUint32(hdr[0:], uint32(len(data)))
	le.PutUint32(hdr[4:], uint32(len(hdr)))
	return padTo4(append(hdr, data...))
}

var marker = resEntry(num(0), num(0), 0, nil)

func buildRes(entries ...[]byte) []byte {
	out := append([]byte(nil), marker...)
	for _, e := range entries {
		out = append(out, e...)
	}
	return out
}

func bytesOf(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i*7)
	}
	return b
}

// ---- an independent COFF reader ----

type coffSection struct {
	name                              string
	size, rawPtr, relPtr, nrel, flags uint32
	data                              []byte
}

type coffSymbol struct {
	name    string
	value   uint32
	section int16
	class   byte
	naux    byte
	aux     []byte
}

type coffReloc struct {
	addr, sym uint32
	typ       uint16
}

type coffFile struct {
	machine, characteristics uint16
	timestamp                uint32
	sections                 []coffSection
	symbols                  []coffSymbol // aux records are folded into their symbol
	relocs                   []coffReloc  // relocations of section 1
	strtab                   []byte
}

func readCOFF(t *testing.T, b []byte) *coffFile {
	t.Helper()
	f := &coffFile{}
	f.machine = le.Uint16(b[0:])
	nsec := int(le.Uint16(b[2:]))
	f.timestamp = le.Uint32(b[4:])
	symPtr := int(le.Uint32(b[8:]))
	nsym := int(le.Uint32(b[12:]))
	if le.Uint16(b[16:]) != 0 {
		t.Fatalf("optional header size %d", le.Uint16(b[16:]))
	}
	f.characteristics = le.Uint16(b[18:])
	for i := 0; i < nsec; i++ {
		h := b[20+40*i:]
		s := coffSection{name: strings.TrimRight(string(h[:8]), "\x00")}
		if le.Uint32(h[8:]) != 0 || le.Uint32(h[12:]) != 0 || le.Uint32(h[28:]) != 0 || le.Uint16(h[34:]) != 0 {
			t.Fatalf("section %s: unexpected virtual size/address or line numbers", s.name)
		}
		s.size, s.rawPtr, s.relPtr = le.Uint32(h[16:]), le.Uint32(h[20:]), le.Uint32(h[24:])
		s.nrel = uint32(le.Uint16(h[32:]))
		s.flags = le.Uint32(h[36:])
		s.data = b[s.rawPtr : s.rawPtr+s.size]
		f.sections = append(f.sections, s)
	}
	for i := 0; i < nsym; i++ {
		r := b[symPtr+18*i:]
		s := coffSymbol{
			name:    strings.TrimRight(string(r[:8]), "\x00"),
			value:   le.Uint32(r[8:]),
			section: int16(le.Uint16(r[12:])),
			class:   r[16],
			naux:    r[17],
		}
		for k := 0; k < int(s.naux); k++ {
			i++
			s.aux = append(s.aux, b[symPtr+18*i:symPtr+18*i+18]...)
		}
		f.symbols = append(f.symbols, s)
	}
	f.strtab = b[symPtr+18*nsym:]
	s1 := f.sections[0]
	for i := 0; i < int(s1.nrel); i++ {
		r := b[int(s1.relPtr)+10*i:]
		f.relocs = append(f.relocs, coffReloc{le.Uint32(r), le.Uint32(r[4:]), le.Uint16(r[8:])})
	}
	return f
}

// walkedRes is a resource found by walking the directory tree.
type walkedRes struct {
	typ, name string
	lang      uint16
	entryOff  uint32
	size      uint32
}

func ident2s(id ident) string {
	if id.isName {
		return "S:" + string(utf16.Decode(id.name))
	}
	return fmt.Sprintf("N:%d", id.id)
}

// walk lists the leaves of the .rsrc$01 tree and checks the ordering rules.
func walk(t *testing.T, sec []byte) []walkedRes {
	t.Helper()
	var out []walkedRes
	var rec func(off uint32, level int, path [2]string)
	rec = func(off uint32, level int, path [2]string) {
		nNamed := int(le.Uint16(sec[off+12:]))
		nID := int(le.Uint16(sec[off+14:]))
		if le.Uint32(sec[off:]) != 0 || le.Uint32(sec[off+4:]) != 0 || le.Uint32(sec[off+8:]) != 0 {
			t.Errorf("table at %#x: non-zero characteristics/timestamp/version", off)
		}
		var prevName string
		prevID := -1
		for i := 0; i < nNamed+nID; i++ {
			e := sec[off+16+8*uint32(i):]
			idv, ofs := le.Uint32(e), le.Uint32(e[4:])
			var label string
			if i < nNamed {
				if idv&0x80000000 == 0 {
					t.Errorf("table %#x entry %d: named entry without high bit", off, i)
				}
				so := idv & 0x7fffffff
				n := uint32(le.Uint16(sec[so:]))
				u := make([]uint16, n)
				for k := range u {
					u[k] = le.Uint16(sec[so+2+2*uint32(k):])
				}
				label = "S:" + string(utf16.Decode(u))
				if i > 0 && nameKey(u) <= prevName {
					t.Errorf("table %#x: names not strictly ascending", off)
				}
				prevName = nameKey(u)
			} else {
				if idv&0x80000000 != 0 {
					t.Errorf("table %#x entry %d: id entry with high bit", off, i)
				}
				if int(idv) <= prevID {
					t.Errorf("table %#x: ids not strictly ascending", off)
				}
				prevID = int(idv)
				label = fmt.Sprintf("N:%d", idv)
			}
			isDir := ofs&0x80000000 != 0
			switch level {
			case 0, 1:
				if !isDir {
					t.Errorf("level %d entry points at a data entry", level)
					continue
				}
				p := path
				p[level] = label
				rec(ofs&0x7fffffff, level+1, p)
			case 2:
				if isDir {
					t.Errorf("language entry points at a directory")
					continue
				}
				if label[0] != 'N' {
					t.Errorf("language entry with a string name")
				}
				var lang int
				fmt.Sscanf(label, "N:%d", &lang)
				d := sec[ofs:]
				if le.Uint32(d) != 0 || le.Uint32(d[8:]) != 0 || le.Uint32(d[12:]) != 0 {
					t.Errorf("data entry %#x: RVA, codepage or reserved not zero", ofs)
				}
				out = append(out, walkedRes{path[0], path[1], uint16(lang), ofs, le.Uint32(d[4:])})
			}
		}
	}
	rec(0, 0, [2]string{})
	return out
}

type want struct {
	typ, name ident
	lang      uint16
	data      []byte
}

func (w want) key() string {
	return fmt.Sprintf("%s|%s|%d", ident2s(w.typ), ident2s(w.name), w.lang)
}

// ---- tests ----

func testResources() []want {
	return []want{
		{num(24), num(1), 0x409, bytesOf(1451, 1)},
		{num(3), num(1), 0x409, bytesOf(732, 2)},
		{num(3), num(2), 0x409, bytesOf(8, 3)}, // exactly 8 bytes: no padding
		{num(3), num(3), 0x409, bytesOf(9, 4)},
		{num(14), str("APP"), 0x409, bytesOf(76, 5)},
		{num(16), num(1), 0x409, bytesOf(676, 6)},
		{num(16), num(1), 0x407, bytesOf(100, 7)}, // second language of one resource
		{str("CUSTOM"), str("ZED"), 0x409, bytesOf(3, 8)},
		{str("CUSTOM"), str("ALPHA"), 0x409, bytesOf(17, 9)},
		{str("CUSTOM"), num(7), 0, nil}, // empty data
		{str("ALPHA"), str("ZED"), 0x409, bytesOf(1, 10)},
		{num(10), str("ALPHA"), 0x409, bytesOf(5, 11)},
		{num(10), str("ÜBER"), 0x407, bytesOf(6, 12)}, // non-ASCII, odd UTF-16 length
		{num(0xFFF0), num(0xFFFF), 0xFFFF, bytesOf(2, 13)},
	}
}

func TestRoundTrip(t *testing.T) {
	ws := testResources()
	var entries [][]byte
	for _, w := range ws {
		entries = append(entries, resEntry(w.typ, w.name, w.lang, w.data))
	}
	obj, err := convert(buildRes(entries...))
	if err != nil {
		t.Fatal(err)
	}
	f := readCOFF(t, obj)

	if f.machine != 0x8664 || f.characteristics != 0x0100 || f.timestamp != 0 {
		t.Errorf("file header: machine %#x characteristics %#x timestamp %d", f.machine, f.characteristics, f.timestamp)
	}
	if len(f.sections) != 2 || f.sections[0].name != ".rsrc$01" || f.sections[1].name != ".rsrc$02" {
		t.Fatalf("sections: %+v", f.sections)
	}
	for _, s := range f.sections {
		if s.flags != 0x40000040 {
			t.Errorf("%s flags %#x", s.name, s.flags)
		}
		if s.size%8 != 0 {
			t.Errorf("%s size %d not a multiple of 8", s.name, s.size)
		}
	}
	if f.sections[1].rawPtr%8 != 0 || f.sections[1].relPtr != 0 || f.sections[1].nrel != 0 {
		t.Errorf("section 2 placement: %+v", f.sections[1])
	}
	if !bytes.Equal(f.strtab, []byte{0, 0, 0, 0}) {
		t.Errorf("string table %v", f.strtab)
	}

	// Symbols.
	n := len(ws)
	if len(f.symbols) != 3+n {
		t.Fatalf("%d symbols, want %d", len(f.symbols), 3+n)
	}
	if s := f.symbols[0]; s.name != "@feat.00" || s.value != 0x11 || s.section != -1 || s.class != 3 || s.naux != 0 {
		t.Errorf("@feat.00: %+v", s)
	}
	for i, name := range []string{".rsrc$01", ".rsrc$02"} {
		s := f.symbols[1+i]
		sec := f.sections[i]
		if s.name != name || s.value != 0 || s.section != int16(i+1) || s.class != 3 || s.naux != 1 {
			t.Errorf("%s symbol: %+v", name, s)
		}
		if le.Uint32(s.aux[0:]) != sec.size || uint32(le.Uint16(s.aux[4:])) != sec.nrel ||
			le.Uint16(s.aux[6:]) != 0 || le.Uint32(s.aux[8:]) != 0 || le.Uint16(s.aux[12:]) != 0 || s.aux[14] != 0 {
			t.Errorf("%s aux record: % x", name, s.aux)
		}
	}

	// Symbols $R000000.. in .res order, data packed on 8-byte boundaries.
	wantOff := uint32(0)
	for i, w := range ws {
		s := f.symbols[3+i]
		if s.name != fmt.Sprintf("$R%06d", i) || s.section != 2 || s.class != 3 || s.naux != 0 || s.value != wantOff {
			t.Errorf("symbol %d: %+v (want offset %d)", i, s, wantOff)
		}
		got := f.sections[1].data[s.value : s.value+uint32(len(w.data))]
		if !bytes.Equal(got, w.data) {
			t.Errorf("resource %d data mismatch", i)
		}
		pad := f.sections[1].data[s.value+uint32(len(w.data)) : s.value+uint32((len(w.data)+7)&^7)]
		if !bytes.Equal(pad, make([]byte, len(pad))) {
			t.Errorf("resource %d: padding not zero", i)
		}
		wantOff += uint32((len(w.data) + 7) &^ 7)
	}
	if wantOff != f.sections[1].size {
		t.Errorf("section 2 size %d, want %d", f.sections[1].size, wantOff)
	}

	// Directory tree vs. relocations.
	leaves := walk(t, f.sections[0].data)
	if len(leaves) != n || len(f.relocs) != n {
		t.Fatalf("%d leaves and %d relocations, want %d", len(leaves), len(f.relocs), n)
	}
	byKey := map[string]want{}
	for _, w := range ws {
		byKey[w.key()] = w
	}
	reloc := map[uint32]coffReloc{}
	for i, r := range f.relocs {
		if r.typ != 3 {
			t.Errorf("relocation %d type %d", i, r.typ)
		}
		if r.sym != uint32(5+i) {
			t.Errorf("relocation %d targets symbol %d, want %d", i, r.sym, 5+i)
		}
		reloc[r.addr] = r
	}
	// Data entries lie after all directory tables, in 16-byte steps.
	seen := map[string]bool{}
	for _, l := range leaves {
		k := fmt.Sprintf("%s|%s|%d", l.typ, l.name, l.lang)
		w, ok := byKey[k]
		if !ok || seen[k] {
			t.Errorf("unexpected or repeated leaf %s", k)
			continue
		}
		seen[k] = true
		r, ok := reloc[l.entryOff]
		if !ok {
			t.Errorf("leaf %s: no relocation at its data entry %#x", k, l.entryOff)
			continue
		}
		s := f.symbols[int(r.sym)-2] // the file's symbol index counts aux records; the folded list does not
		got := f.sections[1].data[s.value : s.value+l.size]
		if l.size != uint32(len(w.data)) || !bytes.Equal(got, w.data) {
			t.Errorf("leaf %s: data mismatch (size %d, want %d)", k, l.size, len(w.data))
		}
	}
	if len(seen) != n {
		t.Errorf("only %d of %d resources found in the tree", len(seen), n)
	}

	// The string table holds one entry per distinct string node: types
	// CUSTOM and ALPHA; names APP, ZED and ALPHA (under CUSTOM), ZED (under
	// ALPHA), ALPHA and UBER (under type 10).
	secOne := f.sections[0].data
	treeSize := 0
	for _, l := range leaves {
		if int(l.entryOff)+16 > treeSize {
			treeSize = int(l.entryOff) + 16
		}
	}
	strs := 0
	for off := treeSize; off < len(secOne); {
		l := int(le.Uint16(secOne[off:]))
		if l == 0 {
			break // zero padding
		}
		off += 2 + 2*l
		strs++
	}
	if strs != 8 {
		t.Errorf("%d strings in the string table, want 8", strs)
	}
}

func TestDeterministicAndSorted(t *testing.T) {
	ws := testResources()
	build := func(order []int) []byte {
		var e [][]byte
		for _, i := range order {
			e = append(e, resEntry(ws[i].typ, ws[i].name, ws[i].lang, ws[i].data))
		}
		o, err := convert(buildRes(e...))
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	order := make([]int, len(ws))
	for i := range order {
		order[i] = i
	}
	a, b := build(order), build(order)
	if !bytes.Equal(a, b) {
		t.Error("two conversions of the same input differ")
	}
	// A different input order changes symbols and data order but the walked
	// tree must still list the same resources in the same sorted order.
	rev := make([]int, len(order))
	for i := range order {
		rev[i] = order[len(order)-1-i]
	}
	treeOf := func(obj []byte) []string {
		f := readCOFF(t, obj)
		var keys []string
		for _, l := range walk(t, f.sections[0].data) {
			keys = append(keys, fmt.Sprintf("%s|%s|%d", l.typ, l.name, l.lang))
		}
		return keys
	}
	ta, tb := treeOf(a), treeOf(build(rev))
	if strings.Join(ta, ",") != strings.Join(tb, ",") {
		t.Errorf("tree order depends on input order:\n%v\n%v", ta, tb)
	}
}

func TestRejects(t *testing.T) {
	good := resEntry(num(10), num(1), 0x409, []byte("x"))
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", nil, "empty"},
		{"no marker", good, "marker"},
		{"marker only", buildRes(), "no resources"},
		{"truncated header", buildRes(good)[:len(marker)+10], "truncated"},
		{"truncated data", buildRes(good)[:len(buildRes(good))-4], "past the end"},
		{"duplicate", buildRes(good, good), "duplicate"},
		{"bad header size", func() []byte {
			b := buildRes(good)
			le.PutUint32(b[len(marker)+4:], 2)
			return b
		}(), "header size"},
	}
	for _, tc := range tests {
		_, err := convert(tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

func TestRunCommand(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "a.res")
	out := filepath.Join(dir, "a.syso")
	if err := os.WriteFile(in, buildRes(resEntry(num(24), num(1), 0x409, []byte("<x/>"))), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"-machine", "x64", "-in", in, "-out", out}, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	obj, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if readCOFF(t, obj).machine != 0x8664 {
		t.Error("wrong machine in output")
	}
	for _, m := range []string{"x86", "arm64", "arm", ""} {
		stderr.Reset()
		if code := run([]string{"-machine", m, "-in", in, "-out", out + "2"}, &stderr); code == 0 {
			t.Errorf("machine %q accepted", m)
		}
	}
	stderr.Reset()
	if code := run([]string{"-machine", "x64", "-in", in + ".missing", "-out", out}, &stderr); code != 1 {
		t.Errorf("missing input: exit %d", code)
	}
	if _, err := os.Stat(out + "2"); err == nil {
		t.Error("output written for a rejected machine")
	}
}
