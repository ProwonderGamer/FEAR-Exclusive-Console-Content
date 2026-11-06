"""Synthesize the install bar's sounds from scratch (no samples, no game audio).

The bar (installer index.html, step 03 progress view) has 20 segments (5% each), throws sparks off its
leading edge for every new file (fx.set), and at the finish flashes and throws 140 sparks along its length.
Sounds made here, one per moment:
  bar_segment.wav   soft tick when the fill crosses into a new segment (played with a rising pitch per segment)
  bar_spark_1..4    faint crackles for new files (pick one at random, rate-limited by the page)
  bar_finish.wav    the 100% flash and spark burst
  bar_hum_loop.wav  optional quiet bed while the job runs (loops seamlessly)

Run:  python make_loading_sounds.py      (outputs go to .\\out, plus loading_preview.html)
"""
import base64
import io
import json
import os
import wave

import numpy as np

SR = 48000
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "out")
rng = np.random.default_rng(0)


# ---------- building blocks ----------
def t_axis(sec):
    return np.arange(int(SR * sec)) / SR


def env(sec, attack, decay):
    """Short linear attack, then exponential decay with time constant `decay` (s)."""
    t = t_axis(sec)
    return np.clip(t / max(attack, 1e-6), 0, 1) * np.exp(-np.maximum(t - attack, 0) / decay)


def gauss_env(sec, centre, sig_rise, sig_fall):
    """Smooth swell and fade (two half-Gaussians meeting at `centre`)."""
    t = t_axis(sec)
    return np.exp(-0.5 * ((t - centre) / np.where(t < centre, sig_rise, sig_fall)) ** 2)


def sine(sec, f):
    return np.sin(2 * np.pi * f * t_axis(sec))


def sine_f(freq):
    """Sine following a per-sample frequency curve (pitch glides)."""
    return np.sin(2 * np.pi * np.cumsum(freq) / SR)


def noise(n_or_sec):
    n = n_or_sec if isinstance(n_or_sec, (int, np.integer)) else int(SR * n_or_sec)
    return rng.standard_normal(n)


def seeded(seed):
    """Reseed per sound so changing one sound never changes another."""
    global rng
    rng = np.random.default_rng(seed)
    return noise


def biquad(x, kind, f, q=0.707):
    """RBJ cookbook biquad: 'lp', 'hp' or 'bp' (0 dB peak)."""
    w = 2 * np.pi * f / SR
    cw, sw = np.cos(w), np.sin(w)
    al = sw / (2 * q)
    b = {"lp": [(1 - cw) / 2, 1 - cw, (1 - cw) / 2],
         "hp": [(1 + cw) / 2, -(1 + cw), (1 + cw) / 2],
         "bp": [al, 0.0, -al]}[kind]
    a0 = 1 + al
    b0, b1, b2 = (v / a0 for v in b)
    a1, a2 = -2 * cw / a0, (1 - al) / a0
    y = np.zeros_like(x)
    x1 = x2 = y1 = y2 = 0.0
    for i, xi in enumerate(x):
        yi = b0 * xi + b1 * x1 + b2 * x2 - a1 * y1 - a2 * y2
        x2, x1, y2, y1 = x1, xi, y1, yi
        y[i] = yi
    return y


def mix(sec, *parts):
    n = int(SR * sec)
    return sum(np.pad(p, (0, max(0, n - p.size)))[:n] for p in parts)


def finish(x, peak_db):
    """Remove DC, fade the last 4 ms (no end click), normalise to an exact peak in dBFS."""
    x = x - np.mean(x)
    f = int(SR * 0.004)
    x[-f:] *= np.linspace(1, 0, f)
    return x / np.max(np.abs(x)) * 10 ** (peak_db / 20)


def wav_bytes(x):
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(SR)
        w.writeframes(np.round(np.clip(x, -1, 1) * 32767).astype("<i2").tobytes())
    return buf.getvalue()


def bar_segment():
    # Same family as the chosen A2 hover tick, a little softer and rounder (it repeats 20 times).
    noise = seeded(1405)
    s = 0.05
    tone = sine(s, 1450) * env(s, 0.0008, 0.007)
    body = sine(s, 290) * env(s, 0.001, 0.01) * 0.55
    snap = biquad(biquad(noise(s), "hp", 1800), "lp", 3200) * env(s, 0.0002, 0.0012) * 0.3
    return finish(mix(s, tone, body, snap), -22)


def crackle(seed, sec, rate_hz, decay, lp=7000):
    """Random tiny clicks (Poisson), each a sub-millisecond burst of bright noise: the sound of sparks."""
    noise = seeded(seed)
    r = rng
    n = int(SR * sec)
    out = np.zeros(n)
    t = 0.0
    while True:
        t += r.exponential(1 / rate_hz)
        if t >= sec:
            break
        ln = int(SR * r.uniform(0.0002, 0.0012))
        i = int(t * SR)
        burst = noise(ln) * np.hanning(ln) * r.uniform(0.3, 1.0) * np.exp(-t / decay)
        out[i:i + ln] += burst[: n - i]
    return biquad(biquad(out, "hp", 2500), "lp", lp)


def bar_spark(seed):
    return finish(crackle(seed, 0.07, 120, 0.03), -26)


def bar_finish():
    # Low swell with a falling pitch (the chosen "bloop" family, longer and deeper), a soft flame
    # whoomp, then a shower of sparks that thins out over ~1.2 s.
    noise = seeded(2026)
    s = 1.4
    t = t_axis(s)
    swell = sine_f(55 + 120 * np.exp(-t / 0.05)) * gauss_env(s, 0.06, 0.03, 0.09)
    tone = sine(s, 330) * gauss_env(s, 0.07, 0.03, 0.12) * 0.18
    whoomp = biquad(noise(s), "lp", 500) * gauss_env(s, 0.07, 0.035, 0.12) * 0.35
    sparks = crackle(77, s, 260, 0.35) * np.clip(t / 0.04, 0, 1) * 1.6
    return finish(mix(s, swell, tone, whoomp, sparks), -10)


def bar_hum_loop():
    # 2 s bed: dark rumble plus a few sparse embers. Made seamless by crossfading its end into its start.
    noise = seeded(55)
    s, xf = 2.0, 0.25
    total = s + xf
    rumble = biquad(biquad(noise(total), "lp", 180), "lp", 180) * 2.5
    hum = sine(total, 55) * 0.12 * (1 + 0.15 * np.sin(2 * np.pi * 0.5 * t_axis(total)))
    embers = crackle(56, total, 6, 1e9, lp=5000) * 0.5
    x = rumble + hum + embers
    n, k = int(SR * s), int(SR * xf)
    loop = x[:n].copy()
    fade = np.linspace(0, 1, k)
    loop[:k] = loop[:k] * fade + x[n:n + k] * (1 - fade)
    loop -= loop.mean()
    return loop / np.max(np.abs(loop)) * 10 ** (-34 / 20)   # no end fade: it must loop


def main():
    os.makedirs(OUT, exist_ok=True)
    sounds = {"segment": bar_segment(), "finish": bar_finish(), "hum": bar_hum_loop()}
    for i in range(4):
        sounds[f"spark{i + 1}"] = bar_spark(900 + i)
    names = {"segment": "bar_segment", "finish": "bar_finish", "hum": "bar_hum_loop"}
    embedded = {}
    for key, x in sounds.items():
        data = wav_bytes(x)
        fname = names.get(key, f"bar_{key}") + ".wav"
        with open(os.path.join(OUT, fname), "wb") as f:
            f.write(data)
        embedded[key] = base64.b64encode(data).decode()
        print(f"{fname:20} {x.size / SR * 1000:7.1f} ms  peak {20 * np.log10(np.max(np.abs(x))):6.1f} dBFS  "
              f"rms {20 * np.log10(np.sqrt(np.mean(x ** 2))):6.1f} dBFS  clipped {int(np.sum(np.abs(x) >= 1))}")
    # loop seam check: jump between last and first sample vs the typical sample-to-sample step
    h = sounds["hum"]
    print(f"hum seam step {abs(h[0] - h[-1]):.5f} vs median step {np.median(np.abs(np.diff(h))):.5f}")
    here = os.path.dirname(os.path.abspath(__file__))
    if not os.path.exists(os.path.join(here, "loading_preview_template.html")):
        return  # the listening page is optional; the WAVs above are the deliverable
    with open(os.path.join(here, "loading_preview_template.html"), encoding="utf-8") as f:
        page = f.read().replace("/*SOUNDS*/{}", json.dumps(embedded))
    with open(os.path.join(OUT, "loading_preview.html"), "w", encoding="utf-8", newline="\n") as f:
        f.write(page)


if __name__ == "__main__":
    main()
