package catalog

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"fearinstaller/internal/testref"
)

// Reference digests produced by the reference Python catalog reader: SHA-256 over, for each entry in order,
// name + NUL + big-endian u64 length + the decompressed content.
var golden = []struct {
	file    string
	entries int
	digest  string
}{
	{"sfod_d.matcat", 668, "fbb37035c673fbeb99a3f1c6c9214c42a2807d2a4d59c3cc2724a61c30585939"},
	{"lipsenglish.cat", 868, "d5a3129315d6b41495b154c819a28d9d0903173988936a636b4bda43684dccca"},
	{"minicatalog.cat", 188, "b8be990fe9482cbabc917a87e86c2a68bf4624bd12ab43b34bd123ac99d8b578"},
	{"Holiday.matCat", 655, "5fb2c1333d4e3aee262d86e64051ce7b9713cd7f5abb548f81bc290eea58e6b4"},
	{"Holiday.texCat", 1625, "7d589744d85e128881e5915d08c7156637a8b931891dacbfb250063f4e96bfa1"},
	{"sfod_d.texcat", 1541, "0cff0562c38422444fe6a257e189516c32bb1d79741bc824f81bdf6d0e5e8b6b"},
	{"sfx.cat", 1803, "b9eb2857d3198fb241f1906cf6c15fc6c1dcfd5d2de4f49327a7a2530ca1aecc"},
	{"english.cat", 3337, "1258a238448e56c2463ba65113f9547cb0199bc36c677b665f97cb8f913f7aa4"},
}

func locate(t *testing.T, name string) string {
	t.Helper()
	cands := []struct{ env, rel string }{
		{"FEAR_REF_PS3", "worlds/release/" + strings.ToLower(name)},
		{"FEAR_REF_PS3", name},
		{"FEAR_REF_PS3", "snd/" + name},
		{"FEAR_REF_X360", "Game/Worlds/Release/" + name},
	}
	for _, c := range cands {
		if d := os.Getenv(c.env); d != "" {
			p := filepath.Join(d, filepath.FromSlash(c.rel))
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func digest(c *Catalog) (string, error) {
	h := sha256.New()
	for _, e := range c.Entries {
		d, err := c.Read(e)
		if err != nil {
			return "", err
		}
		h.Write([]byte(e.Name))
		h.Write([]byte{0})
		var l [8]byte
		binary.BigEndian.PutUint64(l[:], uint64(len(d)))
		h.Write(l[:])
		h.Write(d)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func TestGoldenCatalogs(t *testing.T) {
	testref.Dir(t, "FEAR_REF_PS3")
	n := 0
	for _, g := range golden {
		p := locate(t, g.file)
		if p == "" {
			t.Logf("skip %s (not available)", g.file)
			continue
		}
		c, err := Open(p)
		if err != nil {
			t.Fatalf("%s: %v", g.file, err)
		}
		if len(c.Entries) != g.entries {
			t.Errorf("%s: %d entries, reference %d", g.file, len(c.Entries), g.entries)
		}
		got, err := digest(c)
		c.Close()
		if err != nil {
			t.Fatalf("%s: %v", g.file, err)
		}
		if got != g.digest {
			t.Errorf("%s: digest %s, reference %s", g.file, got, g.digest)
		} else {
			t.Logf("%-16s %5d entries: byte-exact vs the reference reader", g.file, len(c.Entries))
			n++
		}
	}
	if n == 0 {
		t.Skip("no reference catalog available")
	}
}

// The listing of english.cat must equal the reference TOC listing (size, packed size, name).
func TestListingMatchesReferenceTOC(t *testing.T) {
	ps3 := testref.Dir(t, "FEAR_REF_PS3")
	p := filepath.Join(ps3, "snd", "english.cat")
	if _, err := os.Stat(p); err != nil {
		t.Skip(err)
	}
	toc := os.Getenv("FEAR_REF_PS3_EXTRACT")
	if toc == "" {
		t.Skip("FEAR_REF_PS3_EXTRACT not set")
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(toc), "english.cat_TOC.txt"))
	if err != nil {
		t.Skip(err)
	}
	c, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lines := strings.Split(strings.TrimRight(string(b), "\r\n"), "\n")
	if len(lines) != len(c.Entries) {
		t.Fatalf("%d entries, TOC has %d lines", len(c.Entries), len(lines))
	}
	for i, l := range lines {
		f := strings.SplitN(strings.TrimRight(l, "\r"), "\t", 3)
		e := c.Entries[i]
		if len(f) != 3 || f[2] != e.Name || f[0] != itoa(e.Size) || f[1] != itoa(e.PackedSize) {
			t.Fatalf("line %d: %q vs entry %+v", i, l, e)
		}
	}
}

func itoa(u uint32) string { return strconv.FormatUint(uint64(u), 10) }

func TestMalformed(t *testing.T) {
	for _, d := range [][]byte{nil, make([]byte, 0x20), append(make([]byte, 0x20), 1, 2, 3)} {
		_, _ = New(bytesReaderAt(d), int64(len(d)))
	}
}

type bytesReaderAt []byte

func (b bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b)) {
		return 0, os.ErrInvalid
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, os.ErrInvalid
	}
	return n, nil
}
