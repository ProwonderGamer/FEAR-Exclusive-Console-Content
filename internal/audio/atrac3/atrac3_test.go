// Tests for the FFmpeg 7.1 derived ATRAC3 decoder (LGPL-2.1-or-later, see LICENSE-FFMPEG.md in the parent
// audio package).

package atrac3

import (
	"math"
	"math/rand"
	"testing"
)

// The fast IMDCT must equal FFmpeg's naive definition (tx_template.c ff_tx_mdct_naive_inv + full unfold):
// y[n] = -scale * sum_k X[k] cos(2 pi / 512 (n + 1/2 + 128)(k + 1/2)).
func TestIMDCTMatchesDefinition(t *testing.T) {
	m := newIMDCT()
	rng := rand.New(rand.NewSource(1))
	in := make([]float32, 256)
	for i := range in {
		in[i] = float32(rng.NormFloat64() * 32768)
	}
	out := make([]float32, 512)
	m.transform(out, append([]float32(nil), in...))
	maxErr, maxRef := 0.0, 0.0
	for n := 0; n < 512; n++ {
		s := 0.0
		for k := 0; k < 256; k++ {
			s += float64(in[k]) * math.Cos(2*math.Pi/512*(float64(n)+0.5+128)*(float64(k)+0.5))
		}
		ref := -s * mdctScale
		maxErr = math.Max(maxErr, math.Abs(float64(out[n])-ref))
		maxRef = math.Max(maxRef, math.Abs(ref))
	}
	if maxErr > 1e-5*maxRef {
		t.Fatalf("IMDCT error %g (peak %g)", maxErr, maxRef)
	}
}

// Every spectral VLC table must be a complete prefix code that decodes its own codes back.
func TestVLCTables(t *testing.T) {
	off := 0
	for s := 0; s < 7; s++ {
		tab := huffTabs[off : off+huffTabSizes[s]]
		off += huffTabSizes[s]
		kraft := 0.0
		var code uint32
		for _, e := range tab {
			l := int(e[1])
			kraft += math.Pow(2, -float64(l))
			// Write the code MSB-first into a byte buffer and decode it.
			buf := []byte{byte(code >> 24), byte(code >> 16)}
			r := newBitReader(buf)
			if got := r.vlc(&vlcTables[s]); got != int(e[0])-31 || r.pos != l {
				t.Fatalf("table %d: code %08x decoded %d (len %d), want %d (len %d)", s+1, code, got, r.pos, int(e[0])-31, l)
			}
			code += 1 << (32 - l)
		}
		if kraft != 1 {
			t.Errorf("table %d: Kraft sum %g, want 1", s+1, kraft)
		}
	}
}

func TestBitReader(t *testing.T) {
	r := newBitReader([]byte{0xA5, 0xF0})
	if v := r.get(4); v != 0xA {
		t.Fatalf("get(4) = %x", v)
	}
	if v := r.getS(4); v != 5 { // 0101
		t.Fatalf("getS(4) of 0101 = %d", v)
	}
	if v := r.getS(4); v != -1 {
		t.Fatalf("getS(4) of 1111 = %d", v)
	}
	if v := r.get(8); v != 0 { // 4 data bits (0000) then zero padding past the end
		t.Fatalf("read past end = %x", v)
	}
	if r.pos > r.size+8 {
		t.Fatalf("position %d not clamped", r.pos)
	}
}

// A frame whose only coded subband is zero decodes to silence, and a frame with a bad sound-unit id fails.
func TestDecodeSilentAndBadFrames(t *testing.T) {
	frame := make([]byte, 152)
	// id 0x28 (101000), bands_coded 0, gain points 0 (3 bits), tonal count 0 (5 bits), num_subbands 0 (5 bits),
	// coding mode 1, subband selector 0 (3 bits).
	bits := "101000" + "00" + "000" + "00000" + "00000" + "1" + "000"
	for i, c := range bits {
		if c == '1' {
			frame[i/8] |= 0x80 >> (i % 8)
		}
	}
	d, err := New(1, 152, false)
	if err != nil {
		t.Fatal(err)
	}
	out := [][]float32{make([]float32, SamplesPerFrame)}
	for k := 0; k < 3; k++ {
		if err := d.DecodeFrame(frame, out); err != nil {
			t.Fatal(err)
		}
	}
	for i, v := range out[0] {
		if v != 0 {
			t.Fatalf("sample %d = %g, want silence", i, v)
		}
	}
	if err := d.DecodeFrame(make([]byte, 152), out); err == nil {
		t.Fatal("frame with sound unit id 0 accepted")
	}
	if _, err := New(2, 192, true); err == nil {
		t.Fatal("joint stereo accepted")
	}
	if _, err := New(1, 100, false); err == nil {
		t.Fatal("unknown frame size accepted")
	}
}
