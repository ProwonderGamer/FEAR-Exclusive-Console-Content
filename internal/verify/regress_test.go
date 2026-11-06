package verify_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/fixtures"
	"fearinstaller/internal/isobuild"
	"fearinstaller/internal/refdata"
	"fearinstaller/internal/verify"
	"fearinstaller/internal/vfs"
)

func hasCaveat(r verify.Result, sub string) bool {
	for _, c := range r.Caveats {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

// A dump that is encrypted only in some regions still has to be caught.
func TestEncryptedInOneRegionOnly(t *testing.T) {
	ps3 := fixtures.RefPS3(t)
	tmp := t.TempDir()
	files := ps3Files(t, ps3, tmp, refdata.PS3TitleID)
	disc := filepath.Join(tmp, "disc")
	for i := range files {
		if strings.HasSuffix(files[i].Path, "sfod_d.texcat") {
			p := sparse(t, tmp, "enc_tex", refdata.PS3Files[4].Size)
			f, _ := os.OpenFile(p, os.O_WRONLY, 0)
			b := make([]byte, 64<<10)
			rand.Read(b)
			f.WriteAt(b, 0)
			f.Close()
			files[i].Src = p
		}
	}
	materialize(t, disc, files)
	r := run(t, verify.VerifyPS3, disc)
	want(t, r, verify.StatusEncrypted)
}

// What was not checked is stated in the result.
func TestCaveatsAreStated(t *testing.T) {
	ps3, x360 := refRoots(t)
	tmp := t.TempDir()
	disc := filepath.Join(tmp, "disc")
	materialize(t, disc, ps3Files(t, ps3, tmp, refdata.PS3TitleID))

	r := run(t, verify.VerifyPS3, disc)
	want(t, r, verify.StatusOK)
	if !hasCaveat(r, "ubercatalog.cat was checked only for being present") || hasCaveat(r, "PARAM.SFO") {
		t.Fatalf("whole disc caveats: %q", r.Caveats)
	}
	// A game folder copied out on its own has no PARAM.SFO above it (one inside the disc finds the disc).
	alone := filepath.Join(t.TempDir(), "game")
	materialize(t, alone, gameFolderFiles(ps3Files(t, ps3, tmp, refdata.PS3TitleID)))
	r = run(t, verify.VerifyPS3, alone)
	want(t, r, verify.StatusOK)
	if !hasCaveat(r, "title ID (PARAM.SFO) was not checked") {
		t.Fatalf("game-folder caveats: %q", r.Caveats)
	}
	// An unreadable PARAM.SFO must not silently skip the title-ID check.
	os.Remove(filepath.Join(disc, "PS3_GAME", "PARAM.SFO"))
	os.WriteFile(filepath.Join(disc, "PS3_GAME", "PARAM.SFO"), []byte("garbage"), 0o644)
	r = run(t, verify.VerifyPS3, disc)
	want(t, r, verify.StatusOK)
	if !hasCaveat(r, "PARAM.SFO could not be read") {
		t.Fatalf("garbage SFO caveats: %q", r.Caveats)
	}
	os.Remove(filepath.Join(disc, "PS3_GAME", "PARAM.SFO"))
	r = run(t, verify.VerifyPS3, disc)
	want(t, r, verify.StatusOK)
	if !hasCaveat(r, "PARAM.SFO was not found") {
		t.Fatalf("missing SFO caveats: %q", r.Caveats)
	}

	xf := filepath.Join(tmp, "x360")
	materialize(t, xf, x360Files(t, x360, tmp))
	r = run(t, verify.VerifyX360, xf)
	want(t, r, verify.StatusOK)
	if !hasCaveat(r, "uberCatalog.cat was checked by its exact size (428,074,094 bytes) only") {
		t.Fatalf("x360 caveats: %q", r.Caveats)
	}
}

// When a reference hash for a big catalog exists, it is used.
func TestBigCatalogHashedWhenKnown(t *testing.T) {
	x360 := fixtures.RefX360(t)
	tmp := t.TempDir()
	xf := filepath.Join(tmp, "x360")
	materialize(t, xf, x360Files(t, x360, tmp))

	orig := refdata.X360Big[0]
	defer func() { refdata.X360Big[0] = orig }()
	h := sha256.New() // the stand-in catalog is all zeros
	io.CopyN(h, zeroReader{}, orig.Size)
	refdata.X360Big[0].SHA256 = hex.EncodeToString(h.Sum(nil))

	r := run(t, verify.VerifyX360, xf)
	want(t, r, verify.StatusOK)
	if hasCaveat(r, "uberCatalog.cat") {
		t.Fatalf("a hashed catalog must not carry a size-only caveat: %q", r.Caveats)
	}
	refdata.X360Big[0].SHA256 = strings.Repeat("0", 64)
	r = run(t, verify.VerifyX360, xf)
	want(t, r, verify.StatusMismatch)
	if !strings.Contains(r.Message, "uberCatalog.cat") {
		t.Fatalf("message should name the catalog: %s", r.Message)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// A truncated image says so, instead of "differs from the original disc".
func TestTruncatedImageIsNotReportedAsDifferentRelease(t *testing.T) {
	ps3, x360 := refRoots(t)
	tmp := t.TempDir()
	build := func(name string, w func(string) error, cut int64) string {
		p := filepath.Join(tmp, name)
		if err := w(p); err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(p)
		if err := os.Truncate(p, st.Size()-cut); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ps3f := ps3Files(t, ps3, tmp, refdata.PS3TitleID)
	for _, cut := range []int64{5 << 20, 60 << 20, 440 << 20} {
		p := build("ps3_trunc.iso", func(p string) error { return isobuild.WriteISO9660(p, ps3f, isobuild.ISO9660Options{}) }, cut)
		r := run(t, verify.VerifyPS3, p)
		want(t, r, verify.StatusUnreadable)
		if !strings.Contains(r.Message, "truncated or unreadable") || strings.Contains(r.Message, "differ") {
			t.Fatalf("cut %d: %s", cut, r.Message)
		}
	}
	xf := x360Files(t, x360, tmp)
	for _, cut := range []int64{5 << 20, 300 << 20} {
		p := build("x360_trunc.iso", func(p string) error { return isobuild.WriteXDVDFS(p, xf, 0x0FD90000) }, cut)
		r := run(t, verify.VerifyX360, p)
		want(t, r, verify.StatusUnreadable)
	}
}

// Raw Go errors never become the user-facing message.
func TestErrorsAreInPlainLanguage(t *testing.T) {
	for _, fn := range []func(context.Context, string, func(verify.Progress)) verify.Result{verify.VerifyPS3, verify.VerifyX360} {
		r := fn(context.Background(), filepath.Join(t.TempDir(), "does-not-exist.iso"), nil)
		want(t, r, verify.StatusError)
		for _, bad := range []string{"cannot access", "stat ", "no such file", "ENOENT", "open ", "%"} {
			if strings.Contains(r.Message, bad) || strings.Contains(r.Hint, bad) {
				t.Fatalf("raw error text %q leaked into %q / %q", bad, r.Message, r.Hint)
			}
		}
		if r.Technical == "" {
			t.Fatal("the raw error should be kept in Technical")
		}
	}
}

// A panic inside a reader becomes an error result.
func TestGuardTurnsPanicIntoError(t *testing.T) {
	r := verify.Result{Source: "ps3"}
	verify.Guard(&r, func() { panic("boom") })
	if r.Status != verify.StatusError || r.Message != "Could not read this file." || !strings.Contains(r.Technical, "boom") {
		t.Fatalf("%+v", r)
	}
}

// negFS is a file system whose files claim a negative size (a malformed UDF entry
// used to produce this): readTitleID must not panic.
type negFS struct{ vfs.FS }
type negFile struct{ vfs.File }

func (negFS) Open(string) (vfs.File, error) { return negFile{}, nil }
func (negFile) Size() int64                 { return -5 }
func (negFile) Close() error                { return nil }
func (negFile) ReadAt([]byte, int64) (int, error) {
	return 0, io.EOF
}

func TestReadTitleIDNegativeSize(t *testing.T) {
	if id, ok := verify.ReadTitleID(negFS{}, "x"); ok || id != "" {
		t.Fatalf("got %q %v", id, ok)
	}
}

// At the verifier level: an Xbox 360 image with a video partition in front is still verified.
func TestX360WithVideoPartition(t *testing.T) {
	x360 := fixtures.RefX360(t)
	tmp := t.TempDir()
	p := filepath.Join(tmp, "x360_video.iso")
	if err := isobuild.WriteXDVDFS(p, x360Files(t, x360, tmp), 0x0FD90000); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(p, os.O_RDWR, 0)
	pvd := make([]byte, 2048)
	pvd[0] = 1
	copy(pvd[1:], "CD001")
	pvd[6] = 1
	root := pvd[156 : 156+34]
	root[0], root[2], root[10+1], root[25], root[32] = 34, 20, 8, 2, 1 // LBA 20, 2048 bytes
	f.WriteAt(pvd, 16*2048)
	term := make([]byte, 2048)
	term[0] = 255
	copy(term[1:], "CD001")
	f.WriteAt(term, 17*2048)
	f.Close()
	r := run(t, verify.VerifyX360, p)
	want(t, r, verify.StatusOK)
	if !strings.HasPrefix(r.Container, "XDVDFS") {
		t.Fatalf("container %q", r.Container)
	}
}

// Consistent labels.
func TestVerifiedLabels(t *testing.T) {
	ps3, x360 := refRoots(t)
	tmp := t.TempDir()
	materialize(t, filepath.Join(tmp, "p"), ps3Files(t, ps3, tmp, refdata.PS3TitleID))
	materialize(t, filepath.Join(tmp, "x"), x360Files(t, x360, tmp))
	if r := run(t, verify.VerifyPS3, filepath.Join(tmp, "p")); r.Message != "Verified — F.E.A.R. (USA, PlayStation 3)" {
		t.Fatal(r.Message)
	}
	if r := run(t, verify.VerifyX360, filepath.Join(tmp, "x")); r.Message != "Verified — F.E.A.R. (Xbox 360)" {
		t.Fatal(r.Message)
	}
}
