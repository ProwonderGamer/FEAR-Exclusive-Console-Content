package msf

import (
	"encoding/binary"
	"testing"
)

func header(codec, ch, size, rate uint32, extra int) []byte {
	b := make([]byte, HeaderSize+extra)
	copy(b, "MSF\x01")
	binary.BigEndian.PutUint32(b[4:], codec)
	binary.BigEndian.PutUint32(b[8:], ch)
	binary.BigEndian.PutUint32(b[12:], size)
	binary.BigEndian.PutUint32(b[16:], rate)
	for i := HeaderSize; i < len(b); i++ {
		b[i] = byte(i)
	}
	return b
}

func TestParseATRAC3(t *testing.T) {
	b := header(5, 1, 304, 0xFFFFFFFF, 310) // 6 trailing bytes beyond the data size are ignored
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 1 || f.Codec != 5 || f.Channels != 1 || f.SampleRate != 48000 {
		t.Fatalf("header: %+v", f)
	}
	if len(f.Data) != 304 || f.Data[0] != byte(HeaderSize) {
		t.Fatalf("data: len %d first %d", len(f.Data), f.Data[0])
	}
	if !f.IsATRAC3() || f.JointStereo() || f.BlockAlign() != 152 {
		t.Fatalf("ATRAC3 info: %v %v %d", f.IsATRAC3(), f.JointStereo(), f.BlockAlign())
	}
}

func TestParseVariants(t *testing.T) {
	for _, c := range []struct {
		codec, ch, rate uint32
		block           int
		joint           bool
	}{{4, 2, 44100, 192, true}, {6, 2, 48000, 384, false}, {1, 1, 22050, 0, false}} {
		f, err := Parse(header(c.codec, c.ch, 0, c.rate, 0))
		if err != nil {
			t.Fatal(err)
		}
		if f.BlockAlign() != c.block || f.JointStereo() != c.joint || f.SampleRate != int(c.rate) {
			t.Errorf("codec %d: block %d joint %v rate %d", c.codec, f.BlockAlign(), f.JointStereo(), f.SampleRate)
		}
	}
}

func TestParseRejects(t *testing.T) {
	bad := map[string][]byte{
		"short":         make([]byte, 10),
		"signature":     append([]byte("RIFF"), make([]byte, 60)...),
		"no channels":   header(5, 0, 0, 48000, 0),
		"many channels": header(5, 9, 0, 48000, 0),
		"zero rate":     header(5, 1, 0, 0, 0),
		"size too big":  header(5, 1, 1000, 48000, 100),
	}
	for name, b := range bad {
		if _, err := Parse(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
