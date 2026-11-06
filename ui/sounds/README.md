# Install bar sounds

Sounds for the installer's install progress bar (the 20-segment gold meter in step 03, with sparks off its
leading edge and a spark burst at 100%).

All of them are **original**: built from scratch out of sine tones and seeded random noise by
`make_loading_sounds.py`. They contain no samples and no game audio, so they can ship with the installer.

Format: WAV, 48 kHz, 16-bit, mono. None of them clip.

## The sounds and when to play them

| File | Plays when | Length | Peak |
|---|---|---|---|
| `bar_segment.wav` | the fill crosses into a new segment (every 5%) | 50 ms | -22 dBFS |
| `bar_spark1.wav` ... `bar_spark4.wav` | a new file finishes (the moment the bar throws sparks) | 70 ms | -26 dBFS |
| `bar_finish.wav` | the job is done (the bar's flash and spark burst) | 1.4 s | -10 dBFS |
| `bar_hum_loop.wav` | optional: loops quietly while the job runs | 2.0 s loop | -34 dBFS |

### Segment tick: `bar_segment.wav`
- A soft, rounded tick: a 1450 Hz tone, a 290 Hz body and a muted noise snap, decaying in about 10 ms.
- Kept soft and low on purpose, because it plays nineteen times per install.
- **Raise the pitch as the bar fills:** play segment `s` (1..19) at playback rate `0.85 + 0.4 * (s / 19)`
  (from 0.85 up to 1.25). The bar "climbs" as it fills. Don't play a tick for segment 20: the finish
  sound covers 100%.
- Play it once per new segment, even if several files land in the same step.

### Spark crackle: `bar_spark1-4.wav`
- A few tiny random clicks of bright noise (2.5-7 kHz), thinning out over 70 ms: the sound of sparks.
- **Pick one of the four at random** each time, at a random rate between 0.9 and 1.15, so it never
  sounds like the same sample repeating.
- **Rate-limit it:** at most one every ~70 ms. Files often finish several at a time; without a limit it
  turns into a buzz.
- It is meant to be faint, sitting under the segment ticks.

### Finish: `bar_finish.wav`
- Three layers, timed to the bar's flash (which peaks about 0.3 s into its 0.9 s animation):
  - a low swell whose pitch falls from about 175 Hz to 55 Hz (a soft, deep "bloop"), with a quiet 330 Hz tone over it
  - a soft low "whoomp" of filtered noise, like a flame catching
  - a shower of spark crackles that thins out over about 1.2 s
- Play it once, the moment the job is done.

### Working hum: `bar_hum_loop.wav` (optional)
- A dark rumble (noise filtered below 180 Hz), a faint 55 Hz hum that slowly swells, and a few sparse
  embers crackling. The end is crossfaded into the start, so it loops without a seam.
- It may get tiresome over a long install, so it is **off by default** (the installer does not use it).
  If it is used: loop it, keep it well under the other sounds, fade it in over a second or two after the job
  starts, and fade it out just before the finish sound (never cut it off).
- If it still grates, a longer (~20 s), quieter version would repeat less noticeably.

## How the installer plays them

The installer's front end is a web page (WebView2) loaded as one string, so the sounds travel inside it:
`inline_sounds.py` writes the segment, spark and finish WAVs (about 165 KB, base64) into the page's
`#sfxdata` block. The page's `sfx` code (in `../index.html`, next to the `fx` effects) uses Web Audio: each
file is decoded once into an `AudioBuffer`, and every play starts a new `AudioBufferSourceNode`, which starts
with no delay and takes its pitch from `playbackRate`. `renderProgress` hands it every progress update:

- a new file finishes -> spark (rate-limited to one per 70 ms)
- `Math.floor(percent / 5)` goes up -> segment tick at the rising rate (segments 1..19)
- the job is done -> finish (once)

A job the page picks up part-way (after a reload) starts from where it is, so nothing plays for what had
already happened. The audio context starts on the INSTALL click (the WebView keeps audio locked until the
player acts). An install takes only seconds, so the page has no mute button; its `FX.sound` switch turns
the sounds off. The hum loop is not used.

After rebuilding the WAVs, copy the six that the page uses (not the hum loop) next to `inline_sounds.py` and
run `python inline_sounds.py` to refresh the page.

## Rebuilding or changing them

```
python make_loading_sounds.py
```

This writes all seven WAVs to an `out` folder next to the script. Every sound has its own fixed random seed,
so a rebuild is byte-identical, and changing one sound never changes the others. Each
sound is one short function in the script (`bar_segment`, `bar_spark`, `bar_finish`, `bar_hum_loop`), with
its levels and timings written in plainly. Needs Python 3 and numpy only.

## File hashes (SHA-256)

```
8504F1DD6E401F30B1D156B97D8625EA32EED5E4E505199DABFA9655F8646520  bar_finish.wav
53000CC8D805BC911951EAFA29CB19C79741C53D63CC480CF79FED632FACA90E  bar_hum_loop.wav
EF2E05835A3BA1AB6EE3B7E9F2728C24686BAA4130B6D99882ABBC0421B664DA  bar_segment.wav
5B31695D184373DD2245C68D3D2B2320BFC887E4E72E49044B2F842087B7E2F8  bar_spark1.wav
FF8B8720683E7AF17D6EFAE31955F4C0B31B6B1A815E3B6DC9EDA76A7C53F41F  bar_spark2.wav
7135F10F07958C21CC72BA8E58488B3AD67F9BF9973D2AF2C0D9BC3FBDB51978  bar_spark3.wav
B7A62407BE73EE9212F8F0111AF0406FF609A4DFA9C2CC83D2CD36414CE0844D  bar_spark4.wav
```
