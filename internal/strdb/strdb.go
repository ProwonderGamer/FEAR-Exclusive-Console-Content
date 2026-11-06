// Package strdb reads and writes the F.E.A.R. runtime string database.
//
// PC: StringDatabase\FEAR.Strdb00p, magic "SKDB", version 2, little endian, UTF-16LE text.
// PS3: database_string\<Language>\Loki.strdb, magic "BDKS", version 3, big endian, UTF-16BE text.
// Both share one layout (proved from StringEditRuntime.dll 1.08, FUN_100010f0):
//
//	u32 magic 0x42444B53; u32 version; u32 numKeys; u32 numStrings (= numKeys + numTexts); u32 numVoices;
//	u32 blobSize; blob: keys (ASCII, NUL, padded to 4), texts (UTF-16, NUL, padded to 4), voice paths
//	(ASCII, NUL, padded to 4); then numKeys x {i32 textIndex; i32 voiceIndex} (-1 = none).
//
// Keys are looked up by a binary search with _stricmp, so the key list must stay sorted in ASCII
// case-insensitive byte order. The pools keep their read order, so read -> write is byte exact; new strings
// are appended and existing indices never move.
package strdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

const magic = 0x42444B53

// DB is an in-memory string database.
type DB struct {
	BigEndian bool
	Version   uint32
	Keys      []string
	Entries   [][2]int32 // textIndex, voiceIndex
	Texts     [][]uint16 // UTF-16 code units, without terminator
	Voices    []string

	tcache map[string]int32
	vcache map[string]int32
}

func align4(n int) int { return (n + 3) &^ 3 }

func unitsKey(u []uint16) string {
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return string(b)
}

// Units converts a Go string to UTF-16 code units.
func Units(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

// StringOf converts UTF-16 code units to a Go string (lone surrogates become U+FFFD).
func StringOf(u []uint16) string {
	var sb []rune
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		if c >= 0xD800 && c < 0xDC00 && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] < 0xE000 {
			c = 0x10000 + (c-0xD800)<<10 + rune(u[i+1]) - 0xDC00
			i++
		} else if c >= 0xD800 && c < 0xE000 {
			c = 0xFFFD
		}
		sb = append(sb, c)
	}
	return string(sb)
}

// Parse reads a string database.
func Parse(data []byte) (*DB, error) {
	if len(data) < 0x18 {
		return nil, errors.New("strdb: file too small")
	}
	var bo binary.ByteOrder
	switch {
	case binary.LittleEndian.Uint32(data) == magic:
		bo = binary.LittleEndian
	case binary.BigEndian.Uint32(data) == magic:
		bo = binary.BigEndian
	default:
		return nil, fmt.Errorf("strdb: bad magic %q (expected SKDB/BDKS)", data[:4])
	}
	ver := bo.Uint32(data[4:])
	nkeys, nstr, nvoc, bsz := int(bo.Uint32(data[8:])), int(bo.Uint32(data[12:])), int(bo.Uint32(data[16:])), int(bo.Uint32(data[20:]))
	if nstr < nkeys {
		return nil, errors.New("strdb: numStrings < numKeys")
	}
	if int64(nkeys) > int64(len(data)) || int64(bsz) > int64(len(data)) || int64(nvoc) > int64(len(data)) ||
		0x18+int64(bsz)+8*int64(nkeys) != int64(len(data)) {
		return nil, errors.New("strdb: size mismatch")
	}
	db := &DB{BigEndian: bo == binary.BigEndian, Version: ver}
	blob := data[0x18 : 0x18+bsz]
	p := 0
	ascii := func() (string, error) {
		// Aligning after the previous string can move p past the end of a blob whose length is not a multiple of 4.
		if p > len(blob) {
			return "", errors.New("strdb: string starts beyond the end of the blob")
		}
		e := bytes.IndexByte(blob[p:], 0)
		if e < 0 {
			return "", errors.New("strdb: unterminated string")
		}
		s := string(blob[p : p+e])
		p = align4(p + e + 1)
		return s, nil
	}
	for i := 0; i < nkeys; i++ {
		s, err := ascii()
		if err != nil {
			return nil, err
		}
		db.Keys = append(db.Keys, s)
	}
	for i := 0; i < nstr-nkeys; i++ {
		var u []uint16
		for {
			if p+2 > len(blob) {
				return nil, errors.New("strdb: unterminated UTF-16 string")
			}
			c := bo.Uint16(blob[p:])
			p += 2
			if c == 0 {
				break
			}
			u = append(u, c)
		}
		p = align4(p)
		db.Texts = append(db.Texts, u)
	}
	for i := 0; i < nvoc; i++ {
		s, err := ascii()
		if err != nil {
			return nil, err
		}
		db.Voices = append(db.Voices, s)
	}
	if p != bsz {
		return nil, fmt.Errorf("strdb: blob has %d unparsed bytes", bsz-p)
	}
	for i := 0; i < nkeys; i++ {
		ti := int32(bo.Uint32(data[0x18+bsz+8*i:]))
		vi := int32(bo.Uint32(data[0x18+bsz+8*i+4:]))
		if ti < -1 || int(ti) >= len(db.Texts) || vi < -1 || int(vi) >= len(db.Voices) {
			return nil, fmt.Errorf("strdb: entry %d index out of range", i)
		}
		db.Entries = append(db.Entries, [2]int32{ti, vi})
	}
	return db, nil
}

// Bytes serialises the database in the given byte order and version (PC file: false, 2).
func (db *DB) Bytes(bigEndian bool, version uint32) ([]byte, error) {
	var bo binary.ByteOrder = binary.LittleEndian
	if bigEndian {
		bo = binary.BigEndian
	}
	var blob []byte
	putASCII := func(s string) {
		blob = append(blob, s...)
		blob = append(blob, 0)
		for len(blob)%4 != 0 {
			blob = append(blob, 0)
		}
	}
	for _, k := range db.Keys {
		putASCII(k)
	}
	for _, t := range db.Texts {
		for _, c := range t {
			if c == 0 {
				return nil, errors.New("strdb: text contains NUL")
			}
			var b [2]byte
			bo.PutUint16(b[:], c)
			blob = append(blob, b[:]...)
		}
		blob = append(blob, 0, 0)
		for len(blob)%4 != 0 {
			blob = append(blob, 0)
		}
	}
	for _, v := range db.Voices {
		putASCII(v)
	}
	out := make([]byte, 0x18, 0x18+len(blob)+8*len(db.Entries))
	for i, x := range []uint32{magic, version, uint32(len(db.Keys)), uint32(len(db.Keys) + len(db.Texts)), uint32(len(db.Voices)), uint32(len(blob))} {
		bo.PutUint32(out[4*i:], x)
	}
	out = append(out, blob...)
	for _, e := range db.Entries {
		var b [8]byte
		bo.PutUint32(b[:], uint32(e[0]))
		bo.PutUint32(b[4:], uint32(e[1]))
		out = append(out, b[:]...)
	}
	return out, nil
}

// StrKey is the _stricmp sort key (ASCII case folding).
func StrKey(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// IndexOf emulates StringEditRuntime FUN_10001870 exactly (binary search with _stricmp). -1 if absent.
func (db *DB) IndexOf(key string) int {
	n := len(db.Keys)
	if n == 0 {
		return -1
	}
	q := StrKey(key)
	hi, mid, lo := n, n>>1, 0
	for {
		k := StrKey(db.Keys[mid])
		if q == k {
			return mid
		}
		newLo := mid
		if q < k {
			hi, newLo = mid, lo
		}
		nxt := ((hi - newLo) >> 1) + newLo
		if nxt == mid {
			return -1
		}
		mid, lo = nxt, newLo
	}
}

// Value is a resolved entry.
type Value struct {
	Text     []uint16
	HasText  bool
	Voice    string
	HasVoice bool
}

// Get resolves a key the way the runtime does. ok=false for a missing key.
func (db *DB) Get(key string) (v Value, ok bool) {
	if key == "<none>" {
		return Value{HasText: true, HasVoice: true}, true
	}
	i := db.IndexOf(key)
	if i < 0 {
		return Value{}, false
	}
	e := db.Entries[i]
	if e[0] >= 0 {
		v.Text, v.HasText = db.Texts[e[0]], true
	}
	if e[1] >= 0 {
		v.Voice, v.HasVoice = db.Voices[e[1]], true
	}
	return v, true
}

// IsSorted reports whether the keys are strictly sorted (this also rejects case-only duplicates).
func (db *DB) IsSorted() bool {
	for i := 0; i+1 < len(db.Keys); i++ {
		if StrKey(db.Keys[i]) >= StrKey(db.Keys[i+1]) {
			return false
		}
	}
	return true
}

// SetResult is the outcome of Set.
type SetResult string

// Results of Set.
const (
	Added    SetResult = "added"
	Kept     SetResult = "kept"
	Replaced SetResult = "replaced"
)

// Set adds a key (inserted in _stricmp order). An existing key (case-insensitive) is left untouched unless
// overwrite is true. A missing text or voice is stored as -1.
func (db *DB) Set(key string, text []uint16, hasText bool, voice string, hasVoice bool, overwrite bool) (SetResult, error) {
	if key == "" {
		return "", errors.New("strdb: empty key")
	}
	for i := 0; i < len(key); i++ {
		if key[i] > 127 || key[i] == 0 {
			return "", fmt.Errorf("strdb: keys must be non-empty ASCII: %q", key)
		}
	}
	if hasVoice && bytes.IndexByte([]byte(voice), 0) >= 0 {
		return "", errors.New("strdb: voice path contains NUL")
	}
	if db.tcache == nil {
		db.tcache = make(map[string]int32)
		for i, t := range db.Texts {
			db.tcache[unitsKey(t)] = int32(i)
		}
		db.vcache = make(map[string]int32)
		for i, v := range db.Voices {
			db.vcache[v] = int32(i)
		}
	}
	sk := StrKey(key)
	pos := sort.Search(len(db.Keys), func(i int) bool { return StrKey(db.Keys[i]) >= sk })
	exists := pos < len(db.Keys) && StrKey(db.Keys[pos]) == sk
	if exists && !overwrite {
		return Kept, nil
	}
	var entry [2]int32 = [2]int32{-1, -1}
	if hasText {
		k := unitsKey(text)
		i, ok := db.tcache[k]
		if !ok {
			i = int32(len(db.Texts))
			db.tcache[k] = i
			db.Texts = append(db.Texts, append([]uint16(nil), text...))
		}
		entry[0] = i
	}
	if hasVoice {
		i, ok := db.vcache[voice]
		if !ok {
			i = int32(len(db.Voices))
			db.vcache[voice] = i
			db.Voices = append(db.Voices, voice)
		}
		entry[1] = i
	}
	if exists {
		db.Entries[pos] = entry
		return Replaced, nil
	}
	db.Keys = append(db.Keys, "")
	copy(db.Keys[pos+1:], db.Keys[pos:])
	db.Keys[pos] = key
	db.Entries = append(db.Entries, [2]int32{})
	copy(db.Entries[pos+1:], db.Entries[pos:])
	db.Entries[pos] = entry
	return Added, nil
}

// HasKey reports whether the runtime would resolve the key.
func (db *DB) HasKey(key string) bool {
	_, ok := db.Get(key)
	return ok
}
