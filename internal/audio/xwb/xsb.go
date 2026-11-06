package xwb

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Cue is one sound bank cue resolved to a wave.
type Cue struct {
	Index     int
	Name      string
	Simple    bool // the cue's sound plays a single wave (WaveBank/WaveIndex valid)
	WaveBank  int  // index into SoundBank.WaveBanks
	WaveIndex int  // entry index in that wave bank; -1 when not a single-wave sound
}

// SoundBank is the cue list of an XACT sound bank (.xsb).
type SoundBank struct {
	Version   uint16
	Name      string
	WaveBanks []string
	Cues      []Cue
}

// ParseSoundBank reads the cue names and their waves from an XACT 2 sound bank (version 40, the
// layout of the F.E.A.R. Xbox 360 banks Snd\*.xsb in uberCatalog.cat). Big endian "KBDS" (Xbox 360)
// and little endian "SDBK" are accepted. Layout as found in those files:
//
//	0x04 u16 version, 0x09 u16 simple cue count, 0x0B u16 complex cue count, 0x0F u16 cue name count,
//	0x11 u8 wave bank count, 0x12 u16 sound count, 0x1A u32 simple cue table, 0x22 u32 cue name
//	strings, 0x32 u32 wave bank names (64 bytes each), 0x3A u32 cue name index, 0x3E u32 sounds,
//	0x4A char name[64].
//	simple cue (5 bytes): u8 flags, u32 sound offset.
//	cue name index (6 bytes): u32 name offset, u16 hash link.
//	sound: u8 flags (bit 0 set = complex, several tracks), u16 category, u8 volume, u16 pitch,
//	u8 priority, u16 record size, then for a simple sound u16 wave index, u8 wave bank index.
//
// Complex cues and complex sounds are listed with WaveIndex -1 (not needed for the files we build).
func ParseSoundBank(data []byte) (*SoundBank, error) {
	if len(data) < 0x8A {
		return nil, errors.New("xsb: file too small")
	}
	var bo binary.ByteOrder
	switch string(data[:4]) {
	case "KBDS":
		bo = binary.BigEndian
	case "SDBK":
		bo = binary.LittleEndian
	default:
		return nil, errors.New("xsb: not a sound bank (bad magic)")
	}
	sb := &SoundBank{Version: bo.Uint16(data[4:])}
	if sb.Version != 40 {
		return nil, fmt.Errorf("xsb: sound bank version %d is not supported (only 40)", sb.Version)
	}
	u16 := func(o int) int { return int(bo.Uint16(data[o:])) }
	u32 := func(o int) int64 { return int64(bo.Uint32(data[o:])) }
	nSimple, nComplex, nNames := u16(0x09), u16(0x0B), u16(0x0F)
	nBanks := int(data[0x11])
	simpleOff, namesIdxOff, bankNamesOff := u32(0x1A), u32(0x3A), u32(0x32)
	sb.Name = cString(data[0x4A:0x8A])
	if nNames != nSimple+nComplex {
		return nil, fmt.Errorf("xsb: %d cue names for %d simple + %d complex cues", nNames, nSimple, nComplex)
	}
	inRange := func(off int64, n int64) bool { return off >= 0 && off+n <= int64(len(data)) }
	if !inRange(bankNamesOff, int64(nBanks)*64) || !inRange(simpleOff, int64(nSimple)*5) || !inRange(namesIdxOff, int64(nNames)*6) {
		return nil, errors.New("xsb: table offsets out of range")
	}
	for i := 0; i < nBanks; i++ {
		o := bankNamesOff + int64(i)*64
		sb.WaveBanks = append(sb.WaveBanks, cString(data[o:o+64]))
	}
	sb.Cues = make([]Cue, nNames)
	for i := range sb.Cues {
		no := u32(int(namesIdxOff) + 6*i)
		if !inRange(no, 1) {
			return nil, fmt.Errorf("xsb: cue %d name offset out of range", i)
		}
		end := no
		for end < int64(len(data)) && data[end] != 0 {
			end++
		}
		c := Cue{Index: i, Name: string(data[no:end]), WaveIndex: -1}
		if i < nSimple {
			so := u32(int(simpleOff) + 5*i + 1)
			if !inRange(so, 12) {
				return nil, fmt.Errorf("xsb: cue %d sound offset out of range", i)
			}
			if data[so]&1 == 0 { // simple sound: one wave
				c.Simple = true
				c.WaveIndex = int(bo.Uint16(data[so+9:]))
				c.WaveBank = int(data[so+11])
				if c.WaveBank >= nBanks {
					return nil, fmt.Errorf("xsb: cue %d names wave bank %d of %d", i, c.WaveBank, nBanks)
				}
			}
		}
		sb.Cues[i] = c
	}
	return sb, nil
}

// Cue returns the cue with the given name (exact match).
func (sb *SoundBank) Cue(name string) (Cue, bool) {
	for _, c := range sb.Cues {
		if c.Name == name {
			return c, true
		}
	}
	return Cue{}, false
}
