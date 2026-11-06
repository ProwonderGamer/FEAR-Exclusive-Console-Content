package xwb

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/catalog"
)

// buildBank writes a small wave bank: entry 0 is XMA mono 48000 with a seek table, entry 1 is PCM
// stereo 22050 16-bit without one. headerVersion != 0 inserts the header version field (segments at 0x0C).
func buildBank(bo binary.ByteOrder, headerVersion uint32) []byte {
	segStart := 8
	if headerVersion != 0 {
		segStart = 0xC
	}
	bankOff := uint32(segStart + 40)
	metaOff := bankOff + 0x60
	seekOff := metaOff + 2*24
	// seek tables: offsets[2] then entry 0's table {count 3, 512, 1024, 1536}; entry 1 none
	seek := make([]byte, 8+16)
	bo.PutUint32(seek[0:], 0)
	bo.PutUint32(seek[4:], 0xFFFFFFFF)
	bo.PutUint32(seek[8:], 3)
	bo.PutUint32(seek[12:], 512)
	bo.PutUint32(seek[16:], 1024)
	bo.PutUint32(seek[20:], 1536)
	waveOff := seekOff + uint32(len(seek))
	wave := []byte("0123456789abcdefXYZ")

	d := make([]byte, int(waveOff)+len(wave))
	if bo == binary.BigEndian {
		copy(d, "DNBW")
	} else {
		copy(d, "WBND")
	}
	bo.PutUint32(d[4:], 40)
	if headerVersion != 0 {
		bo.PutUint32(d[8:], headerVersion)
	}
	segs := [5][2]uint32{{bankOff, 0x60}, {metaOff, 48}, {seekOff, uint32(len(seek))}, {waveOff, 0}, {waveOff, uint32(len(wave))}}
	for i, s := range segs {
		bo.PutUint32(d[segStart+8*i:], s[0])
		bo.PutUint32(d[segStart+8*i+4:], s[1])
	}
	b := d[bankOff:]
	bo.PutUint32(b[0:], FlagSeekTables)
	bo.PutUint32(b[4:], 2)
	copy(b[8:], "TestBank")
	bo.PutUint32(b[72:], 24)
	bo.PutUint32(b[76:], 0)
	bo.PutUint32(b[80:], 2048)
	m := d[metaOff:]
	// entry 0: flags 1, duration 1536; XMA, 1 ch, 48000 Hz, align 0, 16 bit flag clear
	bo.PutUint32(m[0:], 1536<<4|1)
	bo.PutUint32(m[4:], uint32(CodecXMA)|1<<2|48000<<5)
	bo.PutUint32(m[8:], 0)
	bo.PutUint32(m[12:], 16)
	bo.PutUint32(m[16:], 384)
	bo.PutUint32(m[20:], 1000)
	// entry 1: PCM, 2 ch, 22050 Hz, block align 4, 16 bit
	bo.PutUint32(m[24:], 7<<4)
	bo.PutUint32(m[28:], uint32(CodecPCM)|2<<2|22050<<5|4<<23|1<<31)
	bo.PutUint32(m[32:], 16)
	bo.PutUint32(m[36:], 3)
	copy(d[seekOff:], seek)
	copy(d[waveOff:], wave)
	return d
}

func TestOpenSynthetic(t *testing.T) {
	for _, c := range []struct {
		name string
		bo   binary.ByteOrder
		hv   uint32
	}{{"big endian v40", binary.BigEndian, 0}, {"little endian with header version", binary.LittleEndian, 44}} {
		t.Run(c.name, func(t *testing.T) {
			b, err := Open(buildBank(c.bo, c.hv))
			if err != nil {
				t.Fatal(err)
			}
			if b.BigEndian != (c.bo == binary.BigEndian) || b.Version != 40 || b.HeaderVersion != c.hv {
				t.Fatalf("header: big %v version %d header version %d", b.BigEndian, b.Version, b.HeaderVersion)
			}
			if b.Name != "TestBank" || b.Alignment != 2048 || len(b.Entries) != 2 {
				t.Fatalf("bank data: %q align %d entries %d", b.Name, b.Alignment, len(b.Entries))
			}
			e := b.Entries[0]
			if e.Codec != CodecXMA || e.Channels != 1 || e.SampleRate != 48000 || e.Flags != 1 || e.Duration != 1536 ||
				e.BitsPerSample != 8 || e.LoopStart != 384 || e.LoopLength != 1000 {
				t.Fatalf("entry 0: %+v", e)
			}
			if len(e.SeekTable) != 3 || e.SeekTable[0] != 512 || e.SeekTable[2] != 1536 {
				t.Fatalf("entry 0 seek table %v", e.SeekTable)
			}
			e = b.Entries[1]
			if e.Codec != CodecPCM || e.Channels != 2 || e.SampleRate != 22050 || e.BlockAlign != 4 || e.BitsPerSample != 16 ||
				e.Duration != 7 || e.SeekTable != nil {
				t.Fatalf("entry 1: %+v", e)
			}
			d0, err := b.Data(0)
			if err != nil || string(d0) != "0123456789abcdef" {
				t.Fatalf("Data(0) = %q, %v", d0, err)
			}
			d1, err := b.Data(1)
			if err != nil || string(d1) != "XYZ" {
				t.Fatalf("Data(1) = %q, %v", d1, err)
			}
			if _, err := b.Data(2); err == nil {
				t.Fatal("Data(2) accepted")
			}
		})
	}
}

func TestOpenRejectsDamage(t *testing.T) {
	good := buildBank(binary.BigEndian, 0)
	mutate := func(f func(d []byte)) []byte {
		d := append([]byte(nil), good...)
		f(d)
		return d
	}
	cases := map[string][]byte{
		"short":               good[:0x20],
		"bad magic":           mutate(func(d []byte) { copy(d, "RIFF") }),
		"xact1":               mutate(func(d []byte) { binary.BigEndian.PutUint32(d[4:], 1) }),
		"segment past end":    mutate(func(d []byte) { binary.BigEndian.PutUint32(d[8+8*4+4:], 1<<20) }),
		"entry past wave":     mutate(func(d []byte) { binary.BigEndian.PutUint32(d[0x90+12:], 1<<16) }),
		"compact":             mutate(func(d []byte) { binary.BigEndian.PutUint32(d[0x30:], FlagCompact) }),
		"small metadata":      mutate(func(d []byte) { binary.BigEndian.PutUint32(d[0x30+72:], 20) }),
		"too many entries":    mutate(func(d []byte) { binary.BigEndian.PutUint32(d[0x30+4:], 3) }),
		"seek table overflow": mutate(func(d []byte) { binary.BigEndian.PutUint32(d[0xC0+8:], 1000) }),
	}
	for name, d := range cases {
		if _, err := Open(d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// buildSoundBank writes a v40 sound bank with two simple cues (one single-wave, one complex sound) and
// one complex cue.
func buildSoundBank(bo binary.ByteOrder) []byte {
	d := make([]byte, 0x200)
	if bo == binary.BigEndian {
		copy(d, "KBDS")
	} else {
		copy(d, "SDBK")
	}
	bo.PutUint16(d[4:], 40)
	bo.PutUint16(d[0x09:], 2)
	bo.PutUint16(d[0x0B:], 1)
	bo.PutUint16(d[0x0F:], 3)
	d[0x11] = 1
	bo.PutUint16(d[0x12:], 2)
	copy(d[0x4A:], "TestSounds")
	const bankNames, simple, nameIdx, strs, sounds = 0x100, 0x140, 0x150, 0x170, 0x1A0
	bo.PutUint32(d[0x32:], bankNames)
	bo.PutUint32(d[0x1A:], simple)
	bo.PutUint32(d[0x3A:], nameIdx)
	copy(d[bankNames:], "VoiceTest")
	// simple cue table: flags, sound offset
	bo.PutUint32(d[simple+1:], sounds)
	bo.PutUint32(d[simple+5+1:], sounds+12)
	names := []string{"A001", "A002r", "Complex"}
	p := strs
	for i, n := range names {
		bo.PutUint32(d[nameIdx+6*i:], uint32(p))
		bo.PutUint16(d[nameIdx+6*i+4:], 0xFFFF)
		copy(d[p:], n)
		p += len(n) + 1
	}
	// sound 0: simple, wave 3255 in bank 0; sound 1: complex
	d[sounds] = 0
	bo.PutUint16(d[sounds+7:], 12)
	bo.PutUint16(d[sounds+9:], 3255)
	d[sounds+11] = 0
	d[sounds+12] = 1
	return d
}

func TestParseSoundBankSynthetic(t *testing.T) {
	for _, bo := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
		sb, err := ParseSoundBank(buildSoundBank(bo))
		if err != nil {
			t.Fatal(err)
		}
		if sb.Name != "TestSounds" || len(sb.WaveBanks) != 1 || sb.WaveBanks[0] != "VoiceTest" || len(sb.Cues) != 3 {
			t.Fatalf("%v: %+v", bo, sb)
		}
		c, ok := sb.Cue("A001")
		if !ok || !c.Simple || c.WaveIndex != 3255 || c.WaveBank != 0 || c.Index != 0 {
			t.Fatalf("A001: %+v %v", c, ok)
		}
		if c, _ := sb.Cue("A002r"); c.Simple || c.WaveIndex != -1 {
			t.Fatalf("A002r (complex sound): %+v", c)
		}
		if c, _ := sb.Cue("Complex"); c.Simple || c.WaveIndex != -1 || c.Index != 2 {
			t.Fatalf("Complex cue: %+v", c)
		}
		if _, ok := sb.Cue("a001"); ok {
			t.Fatal("Cue lookup must be exact")
		}
	}
}

func TestParseSoundBankRejectsDamage(t *testing.T) {
	good := buildSoundBank(binary.BigEndian)
	mutate := func(f func(d []byte)) []byte {
		d := append([]byte(nil), good...)
		f(d)
		return d
	}
	cases := map[string][]byte{
		"short":         good[:0x40],
		"bad magic":     mutate(func(d []byte) { copy(d, "XXXX") }),
		"version":       mutate(func(d []byte) { binary.BigEndian.PutUint16(d[4:], 46) }),
		"name count":    mutate(func(d []byte) { binary.BigEndian.PutUint16(d[0x0F:], 4) }),
		"table offset":  mutate(func(d []byte) { binary.BigEndian.PutUint32(d[0x1A:], 0x7FFFFFF0) }),
		"sound offset":  mutate(func(d []byte) { binary.BigEndian.PutUint32(d[0x141:], 0x1F8) }),
		"wave bank idx": mutate(func(d []byte) { d[0x1A0+11] = 1 }),
	}
	for name, d := range cases {
		if _, err := ParseSoundBank(d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Golden check against a real Xbox 360 copy: FEAR_REF_X360 = the X360 game folder (holding Game\Snd\*.xwb
// and Game\uberCatalog.cat), as a Windows path.
func x360Game(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("FEAR_REF_X360")
	if dir == "" {
		t.Skip("golden test skipped: set FEAR_REF_X360 (Xbox 360 copy holding Game\\Snd\\*.xwb and Game\\uberCatalog.cat) to run it")
	}
	if fi, err := os.Stat(filepath.Join(dir, "Game")); err == nil && fi.IsDir() {
		dir = filepath.Join(dir, "Game")
	}
	// a partial reference set (no uberCatalog) skips, as the other golden tests do
	if _, err := os.Stat(filepath.Join(dir, "uberCatalog.cat")); err != nil {
		t.Skip("golden test skipped: uberCatalog.cat not in FEAR_REF_X360")
	}
	return dir
}

func TestGoldenX360Banks(t *testing.T) {
	game := x360Game(t)
	cat, err := catalog.Open(filepath.Join(game, "uberCatalog.cat"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	for _, c := range []struct {
		bank, xsb string
		entries   int
	}{{"VoiceEnglish.xwb", `Snd\VoiceEnglish.xsb`, 3336}, {"SFX.xwb", `Snd\SFX.xsb`, 1800}} {
		data, err := os.ReadFile(filepath.Join(game, "Snd", c.bank))
		if err != nil {
			t.Fatal(err)
		}
		b, err := Open(data)
		if err != nil {
			t.Fatalf("%s: %v", c.bank, err)
		}
		if !b.BigEndian || b.Version != 40 || len(b.Entries) != c.entries {
			t.Fatalf("%s: big %v version %d entries %d", c.bank, b.BigEndian, b.Version, len(b.Entries))
		}
		for i, e := range b.Entries {
			if e.Codec != CodecXMA || e.Channels != 1 || len(e.SeekTable) == 0 || e.Length%2048 != 0 {
				t.Fatalf("%s entry %d: %+v", c.bank, i, e)
			}
		}
		x, err := cat.ReadByName(c.xsb)
		if err != nil {
			t.Fatal(err)
		}
		sb, err := ParseSoundBank(x)
		if err != nil {
			t.Fatalf("%s: %v", c.xsb, err)
		}
		if len(sb.WaveBanks) != 1 || !strings.EqualFold(sb.WaveBanks[0]+".xwb", c.bank) {
			t.Fatalf("%s names wave banks %q", c.xsb, sb.WaveBanks)
		}
		simple := 0
		for _, cue := range sb.Cues {
			if cue.Simple {
				simple++
				if cue.WaveIndex >= len(b.Entries) {
					t.Fatalf("%s cue %q: wave %d of %d", c.xsb, cue.Name, cue.WaveIndex, len(b.Entries))
				}
			}
		}
		t.Logf("%s: %d entries; %s: %d cues (%d single-wave)", c.bank, len(b.Entries), c.xsb, len(sb.Cues), simple)
	}
}
