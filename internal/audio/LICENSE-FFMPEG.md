# FFmpeg-derived code in this folder

The ATRAC3 decoder in `atrac3/` is a Go translation of parts of FFmpeg 7.1 (tag n7.1), changed for this
project (see "Modifications" below):

- `libavcodec/atrac3.c`, `libavcodec/atrac.c`, `libavcodec/atrac.h`, `libavcodec/atrac3data.h`,
- the inverse MDCT of `libavutil/tx_template.c`,
- the VLC and bit reader behaviour of `libavcodec/vlc.c` and `libavcodec/get_bits.h`.

Copyright of those FFmpeg files, as they state it:

| FFmpeg file | Copyright |
|---|---|
| `libavcodec/atrac3.c` | Copyright (c) 2006-2008 Maxim Poliakovski; Copyright (c) 2006-2008 Benjamin Larsson |
| `libavcodec/atrac.c` | Copyright (c) 2006-2013 Maxim Poliakovski; Copyright (c) 2006-2008 Benjamin Larsson |
| `libavcodec/atrac.h` | Copyright (c) 2009-2013 Maxim Poliakovski; Copyright (c) 2009 Benjamin Larsson |
| `libavcodec/atrac3data.h` | Copyright (c) 2006-2007 Maxim Poliakovski; Copyright (c) 2006-2007 Benjamin Larsson |
| `libavutil/tx_template.c` | Copyright (c) Lynne; Copyright (c) 2008 Loren Merritt; Copyright (c) 2002 Fabrice Bellard; partly based on libdjbfft by D. J. Bernstein |
| `libavcodec/vlc.c` | Copyright (c) 2000, 2001 Fabrice Bellard; Copyright (c) 2002-2004 Michael Niedermayer; Copyright (c) 2010 Loren Merritt |
| `libavcodec/get_bits.h` | Copyright (c) 2004 Michael Niedermayer |

These files are licensed under the **GNU Lesser General Public License, version 2.1 or (at your option) any
later version** (LGPL-2.1-or-later), the license of the FFmpeg sources they derive from. The full license text
is in `../../licenses/FFmpeg-LICENSE.txt` (as FFmpeg 7.1 ships it) and at
<https://www.gnu.org/licenses/old-licenses/lgpl-2.1.html>. Each derived file names its FFmpeg sources and
their copyright lines in its header.

## Modifications

These files are modified versions of the FFmpeg code: it was translated from C to Go and changed in 2026-11
for the F.E.A.R. console content installer. Each file's header says what was changed. In short: only the
"single channels" ATRAC3 coding mode is kept (joint stereo is not ported), the bit reader and VLC are
simplified, and the inverse MDCT uses a plain radix-2 FFT instead of FFmpeg's split-radix codelets.

## Files that are not FFmpeg code

The rest of the `audio` package is original code of this project and is not derived from FFmpeg code: the
resampler, dither, WAV writer, comparison, the PS3 build list, the Watson reload split, and `msf/`. The MSF
file parser in `msf/` was written for this project; FFmpeg's `libavformat/msf.c` was only consulted to learn
the layout of the MSF header, which is a file-format fact, and no code of it is used.
