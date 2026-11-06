package verify_test

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/isobuild"
	"fearinstaller/internal/refdata"
	"fearinstaller/internal/verify"
)

// The European PS3 disc carries the same files as the US one (see refdata), so only its title ID differs: it
// verifies, with its region in the title. The German disc is turned away by its title ID, and any other title
// ID stays a wrong disc.
func TestPS3Regions(t *testing.T) {
	ps3, _ := refRoots(t)
	tmp := t.TempDir()
	for _, c := range []struct {
		id, title string
		status    verify.Status
		message   string
	}{
		{refdata.PS3TitleID, "F.E.A.R. (USA, PlayStation 3)", verify.StatusOK, "Verified"},
		{refdata.PS3TitleIDEurope, "F.E.A.R. (Europe, PlayStation 3)", verify.StatusOK, "Verified"},
		{refdata.PS3TitleIDGerman, "", verify.StatusWrongDisc, "German edition"},
		{"BLES00033", "", verify.StatusWrongDisc, "not a supported F.E.A.R. disc"},
	} {
		t.Run(c.id, func(t *testing.T) {
			files := ps3Files(t, ps3, tmp, refdata.PS3TitleID)
			for i := range files {
				if strings.EqualFold(files[i].Path, "PS3_GAME/PARAM.SFO") {
					files[i] = isobuild.File{Path: files[i].Path, Data: fakeSFO(c.id)}
				}
			}
			d := filepath.Join(tmp, c.id)
			materialize(t, d, files)
			r := run(t, verify.VerifyPS3, d)
			want(t, r, c.status)
			if r.Title != c.title || !strings.Contains(r.Message, c.message) || r.TitleID != c.id {
				t.Fatalf("title %q, message %q, title ID %q", r.Title, r.Message, r.TitleID)
			}
			if c.status != verify.StatusOK && !strings.Contains(r.Hint, refdata.PS3TitleIDEurope) {
				t.Fatalf("the hint should name the accepted releases: %q", r.Hint)
			}
		})
	}
}

// The Xbox 360 disc of the reference lists is the one sold in the US and Europe (media ID 0x33FBCD0B). A
// default.xex with the German disc's media ID is turned away by name; any other changed default.xex is an
// ordinary checksum mismatch.
func TestX360GermanEdition(t *testing.T) {
	_, x360 := refRoots(t)
	tmp := t.TempDir()
	xex, err := os.ReadFile(filepath.Join(x360, "default.xex"))
	if err != nil {
		t.Fatal(err)
	}
	at := mediaIDOffset(t, xex)
	if got := binary.BigEndian.Uint32(xex[at:]); got != 0x33FBCD0B {
		t.Fatalf("reference default.xex media ID %08X, want the US/Europe disc's 33FBCD0B", got)
	}
	for _, c := range []struct {
		name   string
		id     uint32
		status verify.Status
	}{
		{"german", refdata.X360MediaIDGerman, verify.StatusWrongDisc},
		{"other", 0x11111111, verify.StatusMismatch},
	} {
		t.Run(c.name, func(t *testing.T) {
			patched := append([]byte(nil), xex...)
			binary.BigEndian.PutUint32(patched[at:], c.id)
			files := x360Files(t, x360, tmp)
			for i := range files {
				if strings.EqualFold(files[i].Path, "default.xex") {
					files[i] = isobuild.File{Path: files[i].Path, Data: patched}
				}
			}
			d := filepath.Join(tmp, c.name)
			materialize(t, d, files)
			r := run(t, verify.VerifyX360, d)
			want(t, r, c.status)
			if c.status == verify.StatusWrongDisc && !strings.Contains(r.Message, "German edition") {
				t.Fatalf("message %q", r.Message)
			}
		})
	}
}

// mediaIDOffset finds the execution-info header (0x00040006) of an XEX2 file; the media ID is its first field.
func mediaIDOffset(t *testing.T, xex []byte) int {
	t.Helper()
	if string(xex[:4]) != "XEX2" {
		t.Fatal("not an XEX2 file")
	}
	n := int(binary.BigEndian.Uint32(xex[20:]))
	for i := 0; i < n; i++ {
		o := 24 + 8*i
		if binary.BigEndian.Uint32(xex[o:]) == 0x00040006 {
			return int(binary.BigEndian.Uint32(xex[o+4:]))
		}
	}
	t.Fatal("no execution info header")
	return 0
}
