# Pillow side of imgops TestMatchesPillow: read the Go-written synthetic inputs, run Pillow, print hashes and
# compare with Go's outputs.  usage: pillow_check.py <dump dir>
import sys, os, hashlib
import numpy as np
from PIL import Image
d = sys.argv[1]
def load(n, w, h): return Image.frombytes('RGBA', (w, h), open(os.path.join(d, n + '.rgba'), 'rb').read())
a = load('a', 128, 64); b = load('b', 97, 61); c3 = load('c3', 40, 30); c4 = load('c4', 40, 30)
cases = {
    'lanczos_down': a.resize((96, 48), Image.LANCZOS),
    'lanczos_up': b.resize((150, 200), Image.LANCZOS),
    'box_half': a.resize((64, 32), Image.BOX),
    'premul_lanczos': Image.frombytes('RGBa', a.size, a.tobytes()).resize((77, 33), Image.LANCZOS),
    'composite': Image.alpha_composite(c3, c4),
}
for k, im in cases.items():
    p = im.tobytes()
    g = open(os.path.join(d, 'go_' + k + '.rgba'), 'rb').read()
    diff = np.abs(np.frombuffer(p, np.uint8).astype(int) - np.frombuffer(g, np.uint8).astype(int))
    print(k, hashlib.sha256(p).hexdigest(), 'same' if p == g else 'DIFF max %d count %d' % (diff.max(), (diff > 0).sum()))
