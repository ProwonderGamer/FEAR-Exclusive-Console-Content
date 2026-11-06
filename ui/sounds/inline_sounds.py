"""Put the install bar sounds into the installer page.

The page is loaded as one string (WebView2 NavigateToString), so the sounds travel inside it: this script writes
the WAVs below, base64-encoded, into the page's <script type="application/json" id="sfxdata"> block, replacing
whatever that block held. The hum loop is not used by the page (optional in README.md) and is left out.

Run after make_loading_sounds.py has rebuilt the WAVs (and copied them next to this script):
    python inline_sounds.py            (edits ../index.html in place; prints each sound's size)
"""
import base64
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
PAGE = os.path.join(HERE, "..", "index.html")
SOUNDS = {                      # page name -> file
    "segment": "bar_segment.wav",
    "spark1": "bar_spark1.wav",
    "spark2": "bar_spark2.wav",
    "spark3": "bar_spark3.wav",
    "spark4": "bar_spark4.wav",
    "finish": "bar_finish.wav",
}
OPEN = '<script type="application/json" id="sfxdata">'
CLOSE = "</script>"


def main():
    data = {}
    for key, name in SOUNDS.items():
        with open(os.path.join(HERE, name), "rb") as f:
            raw = f.read()
        if raw[:4] != b"RIFF" or raw[8:12] != b"WAVE":
            raise SystemExit(f"{name} is not a WAV file")
        data[key] = base64.b64encode(raw).decode("ascii")
        print(f"{name:18} {len(raw):7} bytes")
    with open(PAGE, encoding="utf-8", newline="") as f:
        page = f.read()
    if page.count(OPEN) != 1:
        raise SystemExit("the page must hold exactly one sfxdata block")
    start = page.index(OPEN) + len(OPEN)
    end = page.index(CLOSE, start)
    page = page[:start] + json.dumps(data, separators=(",", ":")) + page[end:]
    with open(PAGE, "w", encoding="utf-8", newline="") as f:
        f.write(page)
    print(f"index.html now {len(page.encode('utf-8'))} bytes")


if __name__ == "__main__":
    main()
