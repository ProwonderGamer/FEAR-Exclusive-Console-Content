package wld

import (
	"encoding/binary"
	"runtime"
	"testing"
)

// An object count that no section of this size could hold (every object needs at least 10 bytes) must be
// refused before a slice for that many objects is reserved.
func TestParseObjectsBoundsCount(t *testing.T) {
	d := make([]byte, 1<<20)
	binary.LittleEndian.PutUint32(d, uint32(len(d)))
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, _, err := ParseObjects(d, 0, false)
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("ParseObjects accepted an object count larger than the section can hold")
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > uint64(len(d)) {
		t.Fatalf("ParseObjects of a %d byte section allocated %d bytes before failing", len(d), grown)
	}
}

// A well-formed section made only of minimal objects (empty class, no properties) still parses.
func TestParseObjectsMinimal(t *testing.T) {
	d := make([]byte, 4+2*10)
	binary.LittleEndian.PutUint32(d, 2)
	objs, end, err := ParseObjects(d, 0, false)
	if err != nil || len(objs) != 2 || end != len(d) {
		t.Fatalf("got %d objects, end %d, err %v", len(objs), end, err)
	}
}
