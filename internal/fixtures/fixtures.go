// Package fixtures builds test inputs (folders and disc images) out of the
// reference copies of the console games. It is used by tests only.
package fixtures

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/isobuild"
	"fearinstaller/internal/refdata"
)

// TB is the part of testing.TB the builders need, so cmd/mkfixtures can use
// them too.
type TB interface {
	Helper()
	Fatal(args ...any)
}

// RefRoots returns both reference roots (see RefPS3 and RefX360); the test is skipped unless both are usable.
func RefRoots(t *testing.T) (ps3, x360 string) {
	t.Helper()
	return RefPS3(t), RefX360(t)
}

// RefPS3 returns the reference PS3 game root (the PS3_GAME/USRDIR/game folder of a decrypted copy), taken from
// the environment variable FEAR_REF_PS3. No path is built in. The test is skipped, with a message that says
// what to set or which files are missing, when the variable is unset, the folder does not exist, or the tree
// does not hold every file the verifier expects (a partial copy cannot build fixtures).
func RefPS3(t *testing.T) string {
	t.Helper()
	return refRoot(t, "FEAR_REF_PS3", "PS3_GAME/USRDIR/game folder", refdata.PS3Files)
}

// RefX360 is RefPS3 for the extracted Xbox 360 disc root (FEAR_REF_X360).
func RefX360(t *testing.T) string {
	t.Helper()
	return refRoot(t, "FEAR_REF_X360", "Xbox 360 disc root", refdata.X360Files)
}

func refRoot(t *testing.T, env, what string, items []refdata.Item) string {
	t.Helper()
	root := os.Getenv(env)
	if root == "" {
		t.Skipf("reference game data not configured: set %s (%s) to run this test", env, what)
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		t.Skipf("%s=%q is not a folder", env, root)
	}
	if missing := MissingRef(root, items); len(missing) > 0 {
		t.Skipf("%s is an incomplete copy (%d of %d expected files missing, first: %s); this test needs the whole reference tree",
			env, len(missing), len(items), missing[0])
	}
	return root
}

// MissingRef lists the expected files (relative paths) that do not exist below root.
func MissingRef(root string, items []refdata.Item) []string {
	var missing []string
	for _, it := range items {
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(it.Path))); err != nil || !st.Mode().IsRegular() {
			missing = append(missing, it.Path)
		}
	}
	return missing
}

// requireRef stops a fixture builder (a command, not a test) with a clear message instead of a panic or a
// confusing stat error from deeper down.
func requireRef(t TB, what, root string, items []refdata.Item) {
	t.Helper()
	if missing := MissingRef(root, items); len(missing) > 0 {
		t.Fatal(what + " reference tree at " + root + " is incomplete, missing: " + strings.Join(missing, ", "))
	}
}

// Sparse creates a zero-filled file of the given size (stand-in for the big
// catalogs that are only checked for presence and size).
func Sparse(t TB, dir, name string, size int64) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(size)
	f.Close()
	return p
}

// CopyFile copies src to a new file dst. Runs of zero bytes are skipped with a seek rather than written, so the
// large zero stand-ins made by Sparse stay cheap to copy; the size is set at the end.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	var size int64
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if bytes.Count(buf[:n], []byte{0}) == n {
				_, err = out.Seek(int64(n), io.SeekCurrent)
			} else {
				_, err = out.Write(buf[:n])
			}
			if err != nil {
				out.Close()
				return err
			}
			size += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	if err := out.Truncate(size); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// FakeSFO builds a minimal PARAM.SFO with the given TITLE_ID.
func FakeSFO(titleID string) []byte {
	keys := "TITLE\x00TITLE_ID\x00"
	vals := ("F.E.A.R.\x00\x00\x00\x00\x00\x00\x00\x00")[:16]
	b := make([]byte, 20+32)
	copy(b, "\x00PSF")
	le := binary.LittleEndian
	keyOff := uint32(len(b))
	dataOff := keyOff + uint32((len(keys)+3)&^3)
	le.PutUint32(b[8:], keyOff)
	le.PutUint32(b[12:], dataOff)
	le.PutUint32(b[16:], 2)
	le.PutUint16(b[20:], 0)     // TITLE: key offset 0
	le.PutUint32(b[20+12:], 0)  // data offset 0
	le.PutUint16(b[36:], 6)     // TITLE_ID: key offset 6
	le.PutUint32(b[36+12:], 16) // data offset 16
	b = append(b, keys...)
	for len(b) < int(dataOff) {
		b = append(b, 0)
	}
	b = append(b, vals...)
	return append(b, titleID+"\x00\x00\x00"...)
}

// PNG returns a small valid PNG (a plain gradient) standing in for the disc
// artwork; the real ICON0.PNG/PIC1.PNG are only ever read from the player's copy.
func PNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 160, 88))
	for y := 0; y < 88; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{uint8(40 + x), uint8(30 + y), 90, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// PS3Files lists the files of a decrypted PS3 disc (reference game files plus
// a stand-in ubercatalog.cat, PARAM.SFO and ICON0.PNG).
func PS3Files(t TB, ref, tmp, titleID string) []isobuild.File {
	t.Helper()
	requireRef(t, "PS3", ref, refdata.PS3Files)
	var fs []isobuild.File
	for _, it := range refdata.PS3Files {
		fs = append(fs, isobuild.File{Path: "PS3_GAME/USRDIR/game/" + it.Path, Src: filepath.Join(ref, filepath.FromSlash(it.Path))})
	}
	return append(fs,
		isobuild.File{Path: "PS3_GAME/USRDIR/game/ubercatalog.cat", Src: Sparse(t, tmp, "uber.ps3", 430_000_000)},
		isobuild.File{Path: "PS3_GAME/PARAM.SFO", Data: FakeSFO(titleID)},
		isobuild.File{Path: "PS3_GAME/ICON0.PNG", Data: PNG()},
	)
}

// X360Files lists the files of an Xbox 360 disc (reference files plus a
// stand-in uberCatalog.cat of the exact expected size).
func X360Files(t TB, ref, tmp string) []isobuild.File {
	t.Helper()
	requireRef(t, "Xbox 360", ref, refdata.X360Files)
	var fs []isobuild.File
	for _, it := range refdata.X360Files {
		fs = append(fs, isobuild.File{Path: it.Path, Src: filepath.Join(ref, filepath.FromSlash(it.Path))})
	}
	return append(fs, isobuild.File{Path: "Game/uberCatalog.cat", Src: Sparse(t, tmp, "uber.x360", 428_074_094)})
}

// Materialize writes a file list into a real folder. Src files are COPIED, never linked: a test that writes
// through a link would change the reference copy (project rule), and Windows needs a privilege for symlinks.
func Materialize(t TB, root string, files []isobuild.File) {
	t.Helper()
	for _, f := range files {
		dst := filepath.Join(root, filepath.FromSlash(f.Path))
		os.MkdirAll(filepath.Dir(dst), 0o755)
		if f.Src != "" {
			if err := CopyFile(f.Src, dst); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(dst, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
