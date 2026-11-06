package xwb

import (
	"encoding/binary"
	"testing"
)

// count * nameSize is computed in 64 bits: a pair whose 32-bit product wraps to a small number must not
// pass the name segment length check and send the name slice out of range.
func TestOpenNameSizeProductDoesNotWrap(t *testing.T) {
	d := buildBank(binary.BigEndian, 0)
	const bankOff = 8 + 40 // the segment table (5 x 8 bytes) follows the 8-byte start of the file
	binary.BigEndian.PutUint32(d[bankOff+76:], 0x80000000)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Open panicked: %v", r)
		}
	}()
	b, err := Open(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range b.Entries {
		if e.Name != "" {
			t.Fatalf("entry %d got a name from a name segment that cannot hold it: %q", e.Index, e.Name)
		}
	}
}
