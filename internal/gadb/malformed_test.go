package gadb

import (
	"encoding/binary"
	"runtime"
	"testing"
)

// An attribute that declares a huge value count (each value needs only 4 bytes of file) and then fails
// must not first reserve a Value array many times the size of the file.
func TestReadDoesNotReserveForFailedAttribute(t *testing.T) {
	const nv = 1 << 20
	le := binary.LittleEndian
	d := []byte("GADB")
	for _, v := range []uint32{3, 4, 1, 1, 1, nv} { // version, pool size, then the four header counts
		d = le.AppendUint32(d, v)
	}
	d = append(d, 0, 0, 0, 0)       // pool: one empty string
	d = le.AppendUint32(d, 1)       // categories
	d = le.AppendUint32(d, 0)       // category: name offset
	d = le.AppendUint32(d, 1)       // category: records
	d = le.AppendUint32(d, 0)       // record: name offset
	d = le.AppendUint32(d, 1)       // record: attributes
	d = le.AppendUint32(d, 0)       // attribute: name offset
	d = le.AppendUint32(d, TString) // attribute: type
	d = le.AppendUint32(d, 0)       // attribute: flags
	d = le.AppendUint32(d, nv)      // attribute: value count
	first := len(d)
	d = append(d, make([]byte, 4*nv)...)
	le.PutUint32(d[first:], 100) // the first value points outside the pool
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := Read(d)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("Read accepted a value outside the pool")
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > uint64(len(d)) {
		t.Fatalf("Read of a %d byte file allocated %d bytes before failing", len(d), grown)
	}
}
