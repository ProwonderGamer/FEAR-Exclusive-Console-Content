// Tests for the FFmpeg-derived parts (LGPL-2.1-or-later, see LICENSE-FFMPEG.md).

package xma2

import (
	"math"
	"math/rand"
	"testing"
)

// The fast inverse MDCT must agree with FFmpeg's naive reference transform (ff_tx_mdct_naive_inv).
func TestIMDCTMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < blockSizes; i++ {
		n := 1 << uint(blockMinBits+i)
		if n > 2048 {
			break // the naive transform is O(n^2); XMA uses 128..512
		}
		scale := 1.0 / float64(int(1)<<uint(blockMinBits+i-1)) / 32768
		m := newIMDCT(n, scale)
		src := make([]float32, n)
		for k := range src {
			src[k] = float32(rng.NormFloat64() * 1000)
		}
		got := make([]float32, n)
		want := make([]float32, n)
		m.inverse(got, src)
		naiveIMDCT(want, src, scale)
		var errMax, ref float64
		for k := range got {
			errMax = math.Max(errMax, math.Abs(float64(got[k]-want[k])))
			ref = math.Max(ref, math.Abs(float64(want[k])))
		}
		if errMax > ref*1e-5 {
			t.Errorf("n=%d: max error %g (peak %g)", n, errMax, ref)
		}
	}
}

// Every Huffman table must be a complete prefix code, as FFmpeg's builder expects.
func TestVLCTablesComplete(t *testing.T) {
	if vlcErr != nil {
		t.Fatal(vlcErr)
	}
	for name, v := range map[string]*vlc{"sf": sfVLC, "sf_rl": sfRLVLC, "coef0": coefVLC[0], "coef1": coefVLC[1],
		"vec4": vec4VLC, "vec2": vec2VLC, "vec1": vec1VLC} {
		if !v.complete() {
			t.Errorf("%s: incomplete code", name)
		}
	}
}

func TestBitReaderWriter(t *testing.T) {
	buf := []byte{0xA5, 0x3C, 0xFF, 0x00, 0x81}
	r := newBitReader(buf, len(buf)*8)
	if v := r.bits(3); v != 5 {
		t.Fatalf("bits(3)=%d", v)
	}
	if v := r.bits(13); v != 0x053C {
		t.Fatalf("bits(13)=%#x", v)
	}
	if v := r.sbits(4); v != -1 {
		t.Fatalf("sbits(4)=%d", v)
	}
	if v := r.peek(32); v != 0xF0081000 {
		t.Fatalf("peek(32)=%#x", v)
	}
	r.skip(1000)
	if r.count() != 48 {
		t.Fatalf("index not capped at size+8: %d", r.count())
	}
	var w bitWriter
	w.buf = make([]byte, 8)
	w.put(3, 5)
	w.copyBits(buf[1:], 13)
	// 101 + 0011110011111 = 1010 0111 1001 1111
	if w.buf[0] != 0xA7 || w.buf[1] != 0x9F || w.pos != 16 {
		t.Fatalf("writer: % x pos %d", w.buf[:3], w.pos)
	}
}
