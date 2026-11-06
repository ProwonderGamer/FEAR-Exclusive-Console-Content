package strdb

import (
	"encoding/binary"
	"testing"
)

// A key whose terminator ends the blob at a length that is not a multiple of 4 pushes the aligned read
// position past the end of the blob; the next key must be reported as an error, not slice out of range.
func TestParseRejectsReadPastUnalignedBlob(t *testing.T) {
	const bsz = 3 // "ab\0": the aligned position (4) is beyond the blob
	d := make([]byte, 0x18+bsz+8*2)
	binary.LittleEndian.PutUint32(d[0:], magic)
	binary.LittleEndian.PutUint32(d[4:], 2)
	binary.LittleEndian.PutUint32(d[8:], 2)  // keys
	binary.LittleEndian.PutUint32(d[12:], 2) // strings (no texts)
	binary.LittleEndian.PutUint32(d[16:], 0) // voices
	binary.LittleEndian.PutUint32(d[20:], bsz)
	copy(d[0x18:], "ab")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Parse panicked: %v", r)
		}
	}()
	if _, err := Parse(d); err == nil {
		t.Fatal("Parse accepted a blob whose second key lies beyond its end")
	}
}
