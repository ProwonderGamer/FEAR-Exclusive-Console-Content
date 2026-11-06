// Package xwb reads XACT wave banks (.xwb), as used by the Xbox 360 F.E.A.R. (Game\Snd\*.xwb), and
// the matching XACT sound banks (.xsb) that name their cues.
//
// Wave bank layout (XACT 2/3; every field big endian on Xbox 360 ("DNBW"), little endian on PC ("WBND")):
//
//	0x00 magic, 0x04 u32 version, [u32 header version: only when the segment table starts at 0x0C],
//	then 5 segments {u32 offset, u32 length}: bank data, entry metadata, seek tables, entry names,
//	wave data.
//	bank data: u32 flags, u32 entry count, char name[64], u32 metadata element size, u32 name element
//	size, u32 alignment, u32 compact format, FILETIME build time.
//	entry metadata (24 bytes): u32 flags(4 bits)+duration(28 bits), u32 MINIWAVEFORMAT, u32 play
//	offset, u32 play length, u32 loop start, u32 loop length.
//	MINIWAVEFORMAT bits: 0-1 codec (0 PCM, 1 XMA, 2 ADPCM, 3 WMA), 2-4 channels, 5-22 sample rate,
//	23-30 block align, 31 bits per sample (1 = 16 bit).
//	seek tables (XMA): u32 offset per entry (relative to the end of that offset list), then at each
//	offset u32 count and count u32 values (cumulative decoded samples per XMA block).
//
// The F.E.A.R. Xbox 360 banks are version 40 (XACT 2), big endian, no entry names: the names come
// from the sound bank (see ParseSoundBank).
//
// This is our own code, written from the format description above (no FFmpeg code).
package xwb

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Codec is the MINIWAVEFORMAT codec tag.
type Codec int

// MINIWAVEFORMAT codec tags.
const (
	CodecPCM   Codec = 0
	CodecXMA   Codec = 1
	CodecADPCM Codec = 2
	CodecWMA   Codec = 3
)

func (c Codec) String() string {
	switch c {
	case CodecPCM:
		return "PCM"
	case CodecXMA:
		return "XMA"
	case CodecADPCM:
		return "ADPCM"
	case CodecWMA:
		return "WMA"
	}
	return fmt.Sprintf("codec(%d)", int(c))
}

// Bank flags.
const (
	FlagStreaming  = 0x00000001
	FlagEntryNames = 0x00010000
	FlagCompact    = 0x00020000
	FlagSeekTables = 0x00080000
)

// Entry is one wave in the bank.
type Entry struct {
	Index         int
	Flags         uint32 // low 4 bits of the first metadata word
	Duration      uint32 // samples, from the metadata
	Codec         Codec
	Channels      int
	SampleRate    int
	BlockAlign    int
	BitsPerSample int    // 8 or 16 (meaningful for PCM)
	Offset        uint32 // play region, relative to the wave data segment
	Length        uint32
	LoopStart     uint32 // loop region as stored (samples for XMA)
	LoopLength    uint32
	SeekTable     []uint32 // XMA only: cumulative samples per block (nil when the bank has none)
	Name          string   // only when the bank carries entry names
}

// Bank is an opened wave bank. It keeps a reference to the data passed to Open.
type Bank struct {
	BigEndian     bool
	Version       uint32
	HeaderVersion uint32 // 0 when the file has no header version field
	Flags         uint32
	Name          string
	Alignment     uint32
	Entries       []Entry

	data             []byte
	waveOff, waveLen uint32
}

type segment struct{ off, len uint32 }

// Open parses a wave bank held in memory.
func Open(data []byte) (*Bank, error) {
	if len(data) < 0x30 {
		return nil, errors.New("xwb: file too small")
	}
	var bo binary.ByteOrder
	switch string(data[:4]) {
	case "DNBW":
		bo = binary.BigEndian
	case "WBND":
		bo = binary.LittleEndian
	default:
		return nil, errors.New("xwb: not a wave bank (bad magic)")
	}
	b := &Bank{BigEndian: bo == binary.BigEndian, data: data}
	b.Version = bo.Uint32(data[4:])
	if b.Version < 2 {
		return nil, fmt.Errorf("xwb: XACT1 wave bank version %d is not supported", b.Version)
	}
	// The segment table starts at 0x08 (no header version: bank data then sits at 0x30) or at 0x0C.
	segStart := 8
	if bo.Uint32(data[8:]) != 0x30 && bo.Uint32(data[0xC:]) == 0x34 {
		b.HeaderVersion = bo.Uint32(data[8:])
		segStart = 0xC
	}
	var seg [5]segment
	for i := range seg {
		p := segStart + 8*i
		seg[i] = segment{bo.Uint32(data[p:]), bo.Uint32(data[p+4:])}
		if uint64(seg[i].off)+uint64(seg[i].len) > uint64(len(data)) {
			return nil, fmt.Errorf("xwb: segment %d (0x%x+0x%x) beyond end of file (0x%x)", i, seg[i].off, seg[i].len, len(data))
		}
	}
	bd := seg[0]
	if bd.len < 0x60 {
		return nil, fmt.Errorf("xwb: bank data segment too small (0x%x)", bd.len)
	}
	d := data[bd.off:]
	b.Flags = bo.Uint32(d[0:])
	count := bo.Uint32(d[4:])
	b.Name = cString(d[8:72])
	metaSize := bo.Uint32(d[72:])
	nameSize := bo.Uint32(d[76:])
	b.Alignment = bo.Uint32(d[80:])
	if b.Flags&FlagCompact != 0 {
		return nil, errors.New("xwb: compact wave banks are not supported")
	}
	if metaSize < 24 {
		return nil, fmt.Errorf("xwb: entry metadata size %d is not supported", metaSize)
	}
	if uint64(count)*uint64(metaSize) > uint64(seg[1].len) {
		return nil, fmt.Errorf("xwb: %d entries of %d bytes do not fit the metadata segment (0x%x)", count, metaSize, seg[1].len)
	}
	b.waveOff, b.waveLen = seg[4].off, seg[4].len

	b.Entries = make([]Entry, count)
	for i := range b.Entries {
		m := data[seg[1].off+uint32(i)*metaSize:]
		fd := bo.Uint32(m[0:])
		f := bo.Uint32(m[4:])
		e := Entry{
			Index:         i,
			Flags:         fd & 0xF,
			Duration:      fd >> 4,
			Codec:         Codec(f & 3),
			Channels:      int((f >> 2) & 7),
			SampleRate:    int((f >> 5) & 0x3FFFF),
			BlockAlign:    int((f >> 23) & 0xFF),
			BitsPerSample: 8,
			Offset:        bo.Uint32(m[8:]),
			Length:        bo.Uint32(m[12:]),
			LoopStart:     bo.Uint32(m[16:]),
			LoopLength:    bo.Uint32(m[20:]),
		}
		if f>>31 != 0 {
			e.BitsPerSample = 16
		}
		if uint64(e.Offset)+uint64(e.Length) > uint64(b.waveLen) {
			return nil, fmt.Errorf("xwb: entry %d data (0x%x+0x%x) beyond the wave data segment (0x%x)", i, e.Offset, e.Length, b.waveLen)
		}
		// The product is formed in 64 bits: a 32-bit one can wrap and pass for a name segment that is too small.
		if nameSize > 0 && uint64(seg[3].len) >= uint64(count)*uint64(nameSize) {
			e.Name = cString(data[seg[3].off+uint32(i)*nameSize : seg[3].off+uint32(i+1)*nameSize])
		}
		b.Entries[i] = e
	}

	// XMA seek tables
	if st := seg[2]; st.len >= 4*count && count > 0 {
		tab := data[st.off : st.off+st.len]
		base := 4 * count
		for i := range b.Entries {
			if b.Entries[i].Codec != CodecXMA {
				continue
			}
			o := bo.Uint32(tab[4*i:])
			if o == 0xFFFFFFFF {
				continue
			}
			p := uint64(base) + uint64(o)
			if p+4 > uint64(len(tab)) {
				return nil, fmt.Errorf("xwb: entry %d seek table offset 0x%x out of range", i, o)
			}
			n := uint64(bo.Uint32(tab[p:]))
			if p+4+4*n > uint64(len(tab)) {
				return nil, fmt.Errorf("xwb: entry %d seek table (%d values) out of range", i, n)
			}
			vals := make([]uint32, n)
			for k := range vals {
				vals[k] = bo.Uint32(tab[p+4+4*uint64(k):])
			}
			b.Entries[i].SeekTable = vals
		}
	}
	return b, nil
}

// Data returns the stored (encoded) data of entry i. It aliases the bank's buffer.
func (b *Bank) Data(i int) ([]byte, error) {
	if i < 0 || i >= len(b.Entries) {
		return nil, fmt.Errorf("xwb: entry %d out of range (bank has %d)", i, len(b.Entries))
	}
	e := b.Entries[i]
	start := b.waveOff + e.Offset
	return b.data[start : start+e.Length], nil
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
