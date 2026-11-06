# FFmpeg-derived code in this folder

The XMA decoder in this folder (`xma.go`, `wmapro.go`, `mdct.go`, `vlc.go`, `bitio.go`, `tables.go` and the
internal test `xma2_internal_test.go`) is a Go translation of parts of FFmpeg 7.1 (tag n7.1), changed for this
project (see "Modifications" below):

- `libavcodec/wmaprodec.c` (WMA Pro / XMA decoder: `xma_decode_init`, `xma_decode_packet`, `decode_packet`,
  `decode_frame`, `decode_subframe`, `decode_tilehdr`, `decode_coeffs`, `decode_scale_factors`, the channel
  transform and windowing),
- `libavcodec/wmaprodata.h` (Huffman and scale tables, in `tables.go`),
- `libavcodec/wma.c` (`ff_wma_get_large_val`, `ff_wma_run_level_decode`),
- `libavcodec/sinewin_tablegen.h` (`ff_sine_window_init`), `libavutil/float_dsp.c` (`vector_fmul_window`),
- `libavutil/tx_template.c` (inverse MDCT and its FFT),
- `libavcodec/vlc.c` and `libavcodec/get_bits.h` / `put_bits.h` / `bitstream.c` (VLC construction and bit
  reader/writer behaviour),
- `libavcodec/decode.c` (decoder-side `skip_samples` handling and incremental packet consumption).

Copyright of those FFmpeg files, as they state it:

| FFmpeg file | Copyright |
|---|---|
| `libavcodec/wmaprodec.c` | Copyright (c) 2007 Baptiste Coudurier, Benjamin Larsson, Ulion; Copyright (c) 2008 - 2011 Sascha Sommer, Benjamin Larsson |
| `libavcodec/wmaprodata.h` | Copyright (c) 2007 Baptiste Coudurier, Benjamin Larsson, Ulion; Copyright (c) 2008 - 2009 Sascha Sommer |
| `libavcodec/wma.c` | Copyright (c) 2002-2007 The FFmpeg Project |
| `libavcodec/sinewin_tablegen.h` | Copyright (c) 2009 Reimar Doeffinger |
| `libavutil/float_dsp.c` | Copyright 2005 Balatoni Denes; Copyright 2006 Loren Merritt |
| `libavutil/tx_template.c` | Copyright (c) Lynne; Copyright (c) 2008 Loren Merritt; Copyright (c) 2002 Fabrice Bellard; partly based on libdjbfft by D. J. Bernstein |
| `libavcodec/vlc.c` | Copyright (c) 2000, 2001 Fabrice Bellard; Copyright (c) 2002-2004 Michael Niedermayer; Copyright (c) 2010 Loren Merritt |
| `libavcodec/bitstream.c` | Copyright (c) 2000, 2001 Fabrice Bellard; Copyright (c) 2002-2004 Michael Niedermayer; Copyright (c) 2010 Loren Merritt |
| `libavcodec/get_bits.h` | Copyright (c) 2004 Michael Niedermayer |
| `libavcodec/put_bits.h` | copyright (c) 2004 Michael Niedermayer |
| `libavcodec/decode.c` | none of its own |

(The file headers give the copyright lines exactly as the FFmpeg files write them, including the
non-ASCII spelling of a name that this file spells in ASCII.)

These files are licensed under the **GNU Lesser General Public License, version 2.1 or (at your option) any
later version** (LGPL-2.1-or-later), the license of the FFmpeg sources they derive from. The full license text
is in `../../../licenses/FFmpeg-LICENSE.txt` (as FFmpeg 7.1 ships it) and at
<https://www.gnu.org/licenses/old-licenses/lgpl-2.1.html>. Each derived file names its FFmpeg sources and
their copyright lines in its header.

## Modifications

These files are modified versions of the FFmpeg code: it was translated from C to Go and changed in 2026-11
for the F.E.A.R. console content installer. Each file's header says what was changed. In short: only the
float decoder for one XMA stream of one or two channels is kept (the parts XMA never reaches are left out),
the VLC and bit reader are simplified, and the inverse MDCT uses a plain radix-2 FFT instead of FFmpeg's
split-radix codelets. The tables in `tables.go` hold the values of `wmaprodata.h`, with each pair table split
into a symbol array and a code-length array.

## Files that are not FFmpeg code

`build.go`, `targets.go` and the external test `golden_test.go` are original code of this project and are not
derived from FFmpeg. The wave bank and sound bank reader in `../xwb` is also original code.
