// Package msf parses Sony PS3 MSF audio files (the "MSF\x01" container the PS3 F.E.A.R. sound catalogs hold).
//
// This package is original code of this project, not derived from FFmpeg code. Only the header layout (a
// file-format fact) was checked against FFmpeg 7.1's libavformat/msf.c.
//
// Header layout (big endian, 0x40 bytes):
//
//	0x00 "MSF" + version byte
//	0x04 u32 codec      0/1 = PCM16 BE/LE, 3 = PS-ADPCM, 4/5/6 = ATRAC3 (96/152/192 bytes per channel and
//	                    frame; 4 is joint stereo), 7 = MP3
//	0x08 u32 channels
//	0x0C u32 data size  (bytes after the header)
//	0x10 u32 sample rate (0xFFFFFFFF = 48000, the PS3 default)
//	0x14 ...            flags / loop points, unused here
//
// The parser checks only what a decoder needs and treats every field as untrusted.
package msf

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// HeaderSize is the fixed MSF header length; audio data starts here.
const HeaderSize = 0x40

// Codec numbers used in the header.
const (
	CodecPCM16BE = 0
	CodecPCM16LE = 1
	CodecPSADPCM = 3
	CodecATRAC3a = 4 // 96 bytes per channel, joint stereo
	CodecATRAC3b = 5 // 152 bytes per channel
	CodecATRAC3c = 6 // 192 bytes per channel
	CodecMP3     = 7
)

// File is a parsed MSF file. Data aliases the input slice.
type File struct {
	Version    byte
	Codec      uint32
	Channels   int
	SampleRate int
	Data       []byte
}

// Parse reads an MSF file. The data size in the header must fit in the buffer.
func Parse(b []byte) (*File, error) {
	if len(b) < HeaderSize {
		return nil, errors.New("msf: file shorter than the 0x40-byte header")
	}
	if string(b[:3]) != "MSF" {
		return nil, errors.New("msf: missing MSF signature")
	}
	f := &File{Version: b[3]}
	f.Codec = binary.BigEndian.Uint32(b[4:])
	ch := binary.BigEndian.Uint32(b[8:])
	size := binary.BigEndian.Uint32(b[12:])
	sr := binary.BigEndian.Uint32(b[16:])
	if ch == 0 || ch > 8 {
		return nil, fmt.Errorf("msf: implausible channel count %d", ch)
	}
	f.Channels = int(ch)
	switch {
	case sr == 0xFFFFFFFF:
		f.SampleRate = 48000
	case sr == 0 || sr > 192000:
		return nil, fmt.Errorf("msf: implausible sample rate %d", sr)
	default:
		f.SampleRate = int(sr)
	}
	if uint64(size) > uint64(len(b)-HeaderSize) {
		return nil, fmt.Errorf("msf: data size %d exceeds the file (%d bytes after the header)", size, len(b)-HeaderSize)
	}
	f.Data = b[HeaderSize : HeaderSize+int(size)]
	return f, nil
}

// IsATRAC3 reports whether the file holds ATRAC3 data.
func (f *File) IsATRAC3() bool {
	return f.Codec == CodecATRAC3a || f.Codec == CodecATRAC3b || f.Codec == CodecATRAC3c
}

// JointStereo reports whether the ATRAC3 data uses joint stereo coding (codec 4, as FFmpeg's msf demuxer sets).
func (f *File) JointStereo() bool { return f.Codec == CodecATRAC3a }

// BlockAlign returns the ATRAC3 frame size in bytes for all channels together (0 for other codecs).
func (f *File) BlockAlign() int {
	switch f.Codec {
	case CodecATRAC3a:
		return 96 * f.Channels
	case CodecATRAC3b:
		return 152 * f.Channels
	case CodecATRAC3c:
		return 192 * f.Channels
	}
	return 0
}
