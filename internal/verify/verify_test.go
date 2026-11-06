package verify_test

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/fixtures"
	"fearinstaller/internal/isobuild"
	"fearinstaller/internal/refdata"
	"fearinstaller/internal/verify"
)

// Thin aliases over the shared fixtures.
var (
	refRoots    = fixtures.RefRoots
	sparse      = fixtures.Sparse
	fakeSFO     = fixtures.FakeSFO
	ps3Files    = fixtures.PS3Files
	x360Files   = fixtures.X360Files
	materialize = fixtures.Materialize
)

func run(t *testing.T, fn func(context.Context, string, func(verify.Progress)) verify.Result, p string) verify.Result {
	t.Helper()
	r := fn(context.Background(), p, nil)
	t.Logf("%s -> %s | %s | %s", filepath.Base(p), r.Status, r.Message, r.Hint)
	return r
}

func want(t *testing.T, r verify.Result, s verify.Status) {
	t.Helper()
	if r.Status != s {
		t.Fatalf("status = %s (%s), want %s", r.Status, r.Message, s)
	}
}

func TestPS3Folders(t *testing.T) {
	ps3 := fixtures.RefPS3(t)
	tmp := t.TempDir()
	disc := filepath.Join(tmp, "disc")
	materialize(t, disc, ps3Files(t, ps3, tmp, refdata.PS3TitleID))

	// The region is part of the title only when PARAM.SFO was reachable: a pick inside the disc finds the disc
	// above it, so only a game folder copied out on its own has no region.
	alone := filepath.Join(t.TempDir(), "copied out", "game")
	materialize(t, alone, gameFolderFiles(ps3Files(t, ps3, tmp, refdata.PS3TitleID)))
	for name, c := range map[string]struct{ path, title string }{
		"whole disc":         {disc, "F.E.A.R. (USA, PlayStation 3)"},
		"PS3_GAME folder":    {filepath.Join(disc, "PS3_GAME"), "F.E.A.R. (USA, PlayStation 3)"},
		"USRDIR folder":      {filepath.Join(disc, "PS3_GAME", "USRDIR"), "F.E.A.R. (USA, PlayStation 3)"},
		"game folder":        {filepath.Join(disc, "PS3_GAME", "USRDIR", "game"), "F.E.A.R. (USA, PlayStation 3)"},
		"parent of the disc": {tmp, "F.E.A.R. (USA, PlayStation 3)"},
		"game folder alone":  {alone, "F.E.A.R. (PlayStation 3)"},
	} {
		t.Run(name, func(t *testing.T) {
			r := run(t, verify.VerifyPS3, c.path)
			want(t, r, verify.StatusOK)
			if r.Title != c.title || r.Message != "Verified \u2014 "+c.title {
				t.Fatalf("title %q, message %q", r.Title, r.Message)
			}
		})
	}
	// Disc artwork comes from the user's own copy and only when PARAM.SFO is reachable.
	r := run(t, verify.VerifyPS3, disc)
	if r.TitleID != "BLUS30003" || !strings.HasSuffix(strings.ToUpper(r.ArtPath), "PS3_GAME/ICON0.PNG") {
		t.Fatalf("titleID %q art %q", r.TitleID, r.ArtPath)
	}
}

func TestPS3RealParamSFO(t *testing.T) {
	ps3 := fixtures.RefPS3(t)
	b, err := os.ReadFile(filepath.Join(ps3, "..", "..", "PARAM.SFO"))
	if err != nil {
		t.Skip(err)
	}
	s, err := verify.ParseSFO(b)
	if err != nil || s.TitleID != "BLUS30003" {
		t.Fatalf("got %+v err %v", s, err)
	}
	t.Logf("real PARAM.SFO: title ID %s, title %q", s.TitleID, s.Title)
}

func TestPS3ISODecrypted(t *testing.T) {
	ps3 := fixtures.RefPS3(t)
	tmp := t.TempDir()
	files := ps3Files(t, ps3, tmp, refdata.PS3TitleID)
	iso := filepath.Join(tmp, "fear.iso")
	if err := isobuild.WriteISO9660(iso, files, isobuild.ISO9660Options{}); err != nil {
		t.Fatal(err)
	}
	r := run(t, verify.VerifyPS3, iso)
	want(t, r, verify.StatusOK)
	if r.Container != "ISO 9660" || r.ArtPath == "" {
		t.Fatalf("container %q art %q", r.Container, r.ArtPath)
	}
	// Same disc as a UDF image.
	udf := filepath.Join(tmp, "fear_udf.iso")
	if err := isobuild.WriteUDF(udf, files); err != nil {
		t.Fatal(err)
	}
	r = run(t, verify.VerifyPS3, udf)
	want(t, r, verify.StatusOK)
	if r.Container != "UDF" {
		t.Fatalf("container %q", r.Container)
	}
}

// encryptedLook builds same-size files whose first 64 KiB is random: what an
// encrypted dump looks like to the content check (sizes are preserved by encryption).
func encryptedPS3(t *testing.T, tmp string) []isobuild.File {
	var fs []isobuild.File
	for _, it := range refdata.PS3Files {
		p := sparse(t, tmp, strings.ReplaceAll(it.Path, "/", "_"), it.Size)
		f, _ := os.OpenFile(p, os.O_WRONLY, 0)
		n := int64(64 << 10)
		if it.Size < n {
			n = it.Size
		}
		b := make([]byte, n)
		rand.Read(b)
		f.WriteAt(b, 0)
		f.Close()
		fs = append(fs, isobuild.File{Path: "PS3_GAME/USRDIR/game/" + it.Path, Src: p})
	}
	fs = append(fs,
		isobuild.File{Path: "PS3_GAME/USRDIR/game/ubercatalog.cat", Src: sparse(t, tmp, "uber.enc", 430_000_000)},
		isobuild.File{Path: "PS3_GAME/PARAM.SFO", Data: fakeSFO(refdata.PS3TitleID)})
	return fs
}

func TestPS3ISOEncrypted(t *testing.T) {
	_ = fixtures.RefPS3(t)
	tmp := t.TempDir()
	// Region table as found in sector 0 of encrypted dumps: N, 0, then 2N ascending sector numbers.
	sys := make([]byte, 2048)
	binary.BigEndian.PutUint32(sys, 3)
	for i, v := range []uint32{0, 0x2000, 0x2001, 0x90000, 0x90001, 0x400000} {
		binary.BigEndian.PutUint32(sys[8+4*i:], v)
	}
	iso := filepath.Join(tmp, "encrypted.iso")
	if err := isobuild.WriteISO9660(iso, encryptedPS3(t, tmp), isobuild.ISO9660Options{SystemArea: sys}); err != nil {
		t.Fatal(err)
	}
	r := run(t, verify.VerifyPS3, iso)
	want(t, r, verify.StatusEncrypted)
	if !strings.Contains(r.Hint, "decrypt") {
		t.Fatalf("hint should tell the user to decrypt: %q", r.Hint)
	}
	// Same data without a region table is still caught by the content check.
	iso2 := filepath.Join(tmp, "encrypted2.iso")
	isobuild.WriteISO9660(iso2, encryptedPS3(t, tmp), isobuild.ISO9660Options{})
	want(t, run(t, verify.VerifyPS3, iso2), verify.StatusEncrypted)

	// An image whose file system is not readable but sector 0 has the region table.
	junk := filepath.Join(tmp, "unreadable.iso")
	b := make([]byte, 1<<20)
	rand.Read(b[4096:])
	copy(b, sys)
	os.WriteFile(junk, b, 0o644)
	// Sector numbers must fit the file size: scale the table down for this tiny file.
	for i, v := range []uint32{0, 100, 101, 200, 201, 400} {
		binary.BigEndian.PutUint32(b[8+4*i:], v)
	}
	os.WriteFile(junk, b, 0o644)
	want(t, run(t, verify.VerifyPS3, junk), verify.StatusEncrypted)
}

func TestFakeISOs(t *testing.T) {
	tmp := t.TempDir()
	junk := filepath.Join(tmp, "random.iso")
	b := make([]byte, 4<<20)
	rand.Read(b)
	os.WriteFile(junk, b, 0o644)
	want(t, run(t, verify.VerifyPS3, junk), verify.StatusUnrecognized)
	want(t, run(t, verify.VerifyX360, junk), verify.StatusUnrecognized)
	want(t, run(t, verify.VerifyPS3, filepath.Join(tmp, "missing")), verify.StatusError)
	empty := filepath.Join(tmp, "empty")
	os.Mkdir(empty, 0o755)
	want(t, run(t, verify.VerifyPS3, empty), verify.StatusUnrecognized)
	want(t, run(t, verify.VerifyX360, empty), verify.StatusUnrecognized)
}

func TestPS3Negative(t *testing.T) {
	ps3 := fixtures.RefPS3(t)
	tmp := t.TempDir()
	good := ps3Files(t, ps3, tmp, refdata.PS3TitleID)

	build := func(name string, mut func([]isobuild.File) []isobuild.File) string {
		d := filepath.Join(tmp, name)
		materialize(t, d, mut(append([]isobuild.File(nil), good...)))
		return d
	}
	replace := func(fs []isobuild.File, path string, f isobuild.File) []isobuild.File {
		for i := range fs {
			if strings.EqualFold(fs[i].Path, path) {
				f.Path = fs[i].Path
				fs[i] = f
			}
		}
		return fs
	}
	drop := func(fs []isobuild.File, path string) (out []isobuild.File) {
		for _, f := range fs {
			if !strings.EqualFold(f.Path, path) {
				out = append(out, f)
			}
		}
		return
	}

	t.Run("one flipped byte is a mismatch", func(t *testing.T) {
		src, _ := os.ReadFile(filepath.Join(ps3, "worlds/release/sfod_d.matcat"))
		src[len(src)/2] ^= 1
		d := build("flip", func(fs []isobuild.File) []isobuild.File {
			return replace(fs, "PS3_GAME/USRDIR/game/worlds/release/sfod_d.matcat", isobuild.File{Data: src})
		})
		r := run(t, verify.VerifyPS3, d)
		want(t, r, verify.StatusMismatch)
		if !strings.Contains(r.Message, "sfod_d.matcat") {
			t.Fatalf("message should name the file: %s", r.Message)
		}
	})
	t.Run("missing world file", func(t *testing.T) {
		d := build("missing", func(fs []isobuild.File) []isobuild.File {
			return drop(fs, "PS3_GAME/USRDIR/game/worlds/release/sfod_d.wld")
		})
		r := run(t, verify.VerifyPS3, d)
		want(t, r, verify.StatusIncomplete)
	})
	t.Run("ubercatalog missing or truncated", func(t *testing.T) {
		d := build("nouber", func(fs []isobuild.File) []isobuild.File {
			return drop(fs, "PS3_GAME/USRDIR/game/ubercatalog.cat")
		})
		want(t, run(t, verify.VerifyPS3, d), verify.StatusIncomplete)
		d = build("smalluber", func(fs []isobuild.File) []isobuild.File {
			return replace(fs, "PS3_GAME/USRDIR/game/ubercatalog.cat", isobuild.File{Src: sparse(t, tmp, "small", 1000)})
		})
		want(t, run(t, verify.VerifyPS3, d), verify.StatusIncomplete)
	})
	t.Run("wrong title ID", func(t *testing.T) {
		d := build("wrongid", func(fs []isobuild.File) []isobuild.File {
			return replace(fs, "PS3_GAME/PARAM.SFO", isobuild.File{Data: fakeSFO("BLES00033")})
		})
		r := run(t, verify.VerifyPS3, d)
		want(t, r, verify.StatusWrongDisc)
		if r.TitleID != "BLES00033" {
			t.Fatal(r.TitleID)
		}
	})
	t.Run("size differs", func(t *testing.T) {
		d := build("size", func(fs []isobuild.File) []isobuild.File {
			return replace(fs, "PS3_GAME/USRDIR/game/database/loki.gamedb", isobuild.File{Data: []byte("short")})
		})
		// short file: wrong size and not random -> mismatch
		want(t, run(t, verify.VerifyPS3, d), verify.StatusMismatch)
	})
}

func TestCancelAndProgress(t *testing.T) {
	ps3 := fixtures.RefPS3(t)
	tmp := t.TempDir()
	disc := filepath.Join(tmp, "disc")
	materialize(t, disc, ps3Files(t, ps3, tmp, refdata.PS3TitleID))

	var last verify.Progress
	var calls int
	r := verify.VerifyPS3(context.Background(), disc, func(p verify.Progress) {
		if p.Done < last.Done {
			t.Fatalf("progress went backwards")
		}
		last = p
		calls++
	})
	want(t, r, verify.StatusOK)
	if last.Done != last.Total || last.Total == 0 || last.Index != last.Count || calls < 10 {
		t.Fatalf("progress did not finish: %+v after %d calls", last, calls)
	}
	t.Logf("progress: %d updates, final %+v", calls, last)

	ctx, cancel := context.WithCancel(context.Background())
	r = verify.VerifyPS3(ctx, disc, func(p verify.Progress) {
		if p.Done > p.Total/4 {
			cancel()
		}
	})
	want(t, r, verify.StatusCancelled)
}

func TestX360(t *testing.T) {
	ps3, x360 := refRoots(t)
	tmp := t.TempDir()
	files := x360Files(t, x360, tmp)

	folder := filepath.Join(tmp, "x360")
	materialize(t, folder, files)
	want(t, run(t, verify.VerifyX360, folder), verify.StatusOK)
	want(t, run(t, verify.VerifyX360, tmp), verify.StatusOK) // parent folder: searched one level down

	for name, off := range map[string]int64{"raw": 0, "xgd3": 0x02080000, "xgd2": 0x0FD90000} {
		t.Run(name, func(t *testing.T) {
			iso := filepath.Join(tmp, name+".iso")
			if err := isobuild.WriteXDVDFS(iso, files, off); err != nil {
				t.Fatal(err)
			}
			r := run(t, verify.VerifyX360, iso)
			want(t, r, verify.StatusOK)
			if !strings.HasPrefix(r.Container, "XDVDFS") {
				t.Fatal(r.Container)
			}
		})
	}

	// The Game folder of a complete copy finds the disc folder above it.
	gameOnly := filepath.Join(folder, "Game")
	r := run(t, verify.VerifyX360, gameOnly)
	want(t, r, verify.StatusOK)
	// A Game folder copied without default.xex -> helpful incomplete message.
	noXex := filepath.Join(t.TempDir(), "x360")
	var noXexFiles []isobuild.File
	for _, f := range files {
		if !strings.EqualFold(f.Path, "default.xex") {
			noXexFiles = append(noXexFiles, f)
		}
	}
	materialize(t, noXex, noXexFiles)
	r = run(t, verify.VerifyX360, filepath.Join(noXex, "Game"))
	want(t, r, verify.StatusIncomplete)

	// Tampered file.
	src, _ := os.ReadFile(filepath.Join(x360, "Game/Worlds/Release/Holiday.matCat"))
	src[100] ^= 0xFF
	var tampered []isobuild.File
	for _, f := range files {
		if strings.HasSuffix(f.Path, "Holiday.matCat") {
			f = isobuild.File{Path: f.Path, Data: src}
		}
		tampered = append(tampered, f)
	}
	iso := filepath.Join(tmp, "tampered.iso")
	isobuild.WriteXDVDFS(iso, tampered, 0x0FD90000)
	r = run(t, verify.VerifyX360, iso)
	want(t, r, verify.StatusMismatch)

	// uberCatalog.cat with the wrong size.
	var badUber []isobuild.File
	for _, f := range files {
		if strings.HasSuffix(f.Path, "uberCatalog.cat") {
			f.Src = sparse(t, tmp, "uber.bad", 1234)
		}
		badUber = append(badUber, f)
	}
	iso = filepath.Join(tmp, "baduber.iso")
	isobuild.WriteXDVDFS(iso, badUber, 0)
	want(t, run(t, verify.VerifyX360, iso), verify.StatusIncomplete)

	// uberCatalog.cat is only accepted at its expected location: a
	// same-named file somewhere else on the disc does not count.
	var moved []isobuild.File
	for _, f := range files {
		if strings.HasSuffix(f.Path, "uberCatalog.cat") {
			f.Path = "Game/Misc/uberCatalog.cat"
		}
		moved = append(moved, f)
	}
	iso = filepath.Join(tmp, "moved.iso")
	isobuild.WriteXDVDFS(iso, moved, 0x02080000)
	want(t, run(t, verify.VerifyX360, iso), verify.StatusIncomplete)

	// Cross-console mix-ups give a clear answer.
	ps3disc := filepath.Join(tmp, "ps3disc")
	materialize(t, ps3disc, ps3Files(t, ps3, tmp, refdata.PS3TitleID))
	want(t, run(t, verify.VerifyX360, ps3disc), verify.StatusWrongDisc)
	want(t, run(t, verify.VerifyPS3, folder), verify.StatusWrongDisc)
}

// gameFolderFiles keeps the files of a PS3 disc's PS3_GAME/USRDIR/game folder, as a folder of their own (a copy
// of the game data alone, with no PARAM.SFO above it).
func gameFolderFiles(files []isobuild.File) []isobuild.File {
	const game = "PS3_GAME/USRDIR/game/"
	var out []isobuild.File
	for _, f := range files {
		if strings.HasPrefix(f.Path, game) {
			f.Path = strings.TrimPrefix(f.Path, game)
			out = append(out, f)
		}
	}
	return out
}
