package archcfg

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

const mod = "FEAR_ConsoleRestoration"

func stockLines() []string {
	var l []string
	for _, b := range []string{"FEAR", "FEARA", "FEARL", "FEARE"} {
		l = append(l, b+".Arch00")
	}
	for i := 1; i <= 8; i++ {
		for _, b := range []string{"FEAR", "FEARA", "FEARL", "FEARE"} {
			l = append(l, b+"_"+string(rune('0'+i))+".Arch00")
		}
	}
	return l
}

// stock is the Steam file: CRLF, no break after the last line (557 bytes).
func stock() []byte { return []byte(strings.Join(stockLines(), "\r\n")) }

func mustCompute(t *testing.T, in []byte, existed bool) Result {
	t.Helper()
	r, err := Compute(in, existed, Options{Mod: mod, Stock: stockLines()})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStockSizeMatchesGOGListing(t *testing.T) {
	// gogdb lists the GOG root Default.archcfg as 557 B; the stock list without a final break is 557 B.
	if n := len(stock()); n != 557 {
		t.Fatalf("stock file is %d bytes", n)
	}
}

func TestAppendToStock(t *testing.T) {
	r := mustCompute(t, stock(), true)
	want := string(stock()) + "\r\n" + mod + "\r\n"
	if string(r.Content) != want {
		t.Fatalf("got %q", r.Content[len(r.Content)-80:])
	}
	if !r.Changed || r.Encoding != UTF8 || r.LineEnding != "CRLF" || r.Entries != 36 {
		t.Fatalf("%+v", r)
	}
	found := false
	for _, x := range r.Repairs {
		found = found || strings.Contains(x, "no line break at the end")
	}
	if !found {
		t.Fatalf("missing-final-break repair not reported: %v", r.Repairs)
	}
	if r.Summary != "Add "+mod+" as the last mount." {
		t.Fatalf("the missing final break alone must not make the summary talk about repairs: %q", r.Summary)
	}
}

func TestIdempotent(t *testing.T) {
	r1 := mustCompute(t, stock(), true)
	r2 := mustCompute(t, r1.Content, true)
	if !r1.Added || r2.Added || r2.Changed || !bytes.Equal(r1.Content, r2.Content) || !strings.Contains(r2.Summary, "already the last mount") {
		t.Fatalf("second run changed the file: %+v", r2)
	}
}

func TestGluedLineRepair(t *testing.T) {
	// a file where another mod's mount was glued onto the last line and then added again on its own line
	in := strings.Join(stockLines()[:35], "\r\n") + "\r\nFEARE_8.Arch00ButtonMod\r\nButtonMod\r\n"
	r := mustCompute(t, []byte(in), true)
	i := len(r.After)
	if r.After[i-1] != mod || r.After[i-2] != "ButtonMod" || r.After[i-3] != "ButtonMod" || r.After[i-4] != "FEARE_8.Arch00" {
		t.Fatalf("tail: %q", r.After[i-5:])
	}
	if len(r.Repairs) == 0 || !strings.Contains(r.Repairs[0], "Split the joined line") {
		t.Fatalf("repairs: %v", r.Repairs)
	}
	// the repaired file is then stable
	if again := mustCompute(t, r.Content, true); again.Changed {
		t.Fatal("not stable after the repair")
	}
	// the stock list and the repair do not eat a name that merely contains "Arch00"
	for _, keep := range []string{"FEAR.Arch00.bak", "Mod.Arch001", "FEAR.Arch00"} {
		out := mustCompute(t, []byte(keep+"\r\n"), true)
		if out.After[0] != keep {
			t.Errorf("%q became %q", keep, out.After[0])
		}
	}
	// two names glued in a row
	out := mustCompute(t, []byte("A.Arch00B.Arch00C\r\n"), true)
	if strings.Join(out.After, "|") != "A.Arch00|B.Arch00|C|"+mod {
		t.Fatalf("%q", out.After)
	}
}

func TestEncodingsAndLineEndings(t *testing.T) {
	lines := []string{"FEAR.Arch00", "FEARA.Arch00", "Extra_Mod"}
	utf16le := func(s string, bom bool) []byte {
		var b []byte
		if bom {
			b = []byte{0xFF, 0xFE}
		}
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u), byte(u>>8))
		}
		return b
	}
	utf16be := func(s string, bom bool) []byte {
		var b []byte
		if bom {
			b = []byte{0xFE, 0xFF}
		}
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u>>8), byte(u))
		}
		return b
	}
	crlf, lf := strings.Join(lines, "\r\n")+"\r\n", strings.Join(lines, "\n")+"\n"
	cases := []struct {
		name string
		in   []byte
		enc  Encoding
		eol  string
		want []byte
	}{
		{"utf8 crlf", []byte(crlf), UTF8, "CRLF", []byte(crlf + mod + "\r\n")},
		{"utf8 lf", []byte(lf), UTF8, "LF", []byte(lf + mod + "\n")},
		{"utf8 bom", append([]byte{0xEF, 0xBB, 0xBF}, crlf...), UTF8BOM, "CRLF", append([]byte{0xEF, 0xBB, 0xBF}, (crlf + mod + "\r\n")...)},
		{"utf16le bom", utf16le(crlf, true), UTF16LE, "CRLF", utf16le(crlf+mod+"\r\n", true)},
		{"utf16le no bom", utf16le(crlf, false), UTF16LEno, "CRLF", utf16le(crlf+mod+"\r\n", false)},
		{"utf16be bom", utf16be(lf, true), UTF16BE, "LF", utf16be(lf+mod+"\n", true)},
		{"utf16be no bom", utf16be(crlf, false), UTF16BEno, "CRLF", utf16be(crlf+mod+"\r\n", false)},
		{"lone CR becomes LF", []byte(strings.Join(lines, "\r") + "\r"), UTF8, "LF", []byte(lf + mod + "\n")},
		{"no final break", []byte(strings.Join(lines, "\n")), UTF8, "LF", []byte(lf + mod + "\n")},
		{"single line, no break", []byte("FEAR.Arch00"), UTF8, "CRLF", []byte("FEAR.Arch00\r\n" + mod + "\r\n")},
		{"trailing blanks and spaces", []byte("  FEAR.Arch00  \r\n\r\n\r\n"), UTF8, "CRLF", []byte("FEAR.Arch00\r\n" + mod + "\r\n")},
		{"ctrl-z at the end", []byte("FEAR.Arch00\r\n\x1a"), UTF8, "CRLF", []byte("FEAR.Arch00\r\n" + mod + "\r\n")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := mustCompute(t, c.in, true)
			if r.Encoding != c.enc || r.LineEnding != c.eol {
				t.Fatalf("encoding %q eol %q", r.Encoding, r.LineEnding)
			}
			if !bytes.Equal(r.Content, c.want) {
				t.Fatalf("got %q\nwant %q", r.Content, c.want)
			}
			if again := mustCompute(t, r.Content, true); again.Changed {
				t.Fatalf("not idempotent: %q", again.Content)
			}
		})
	}
	// majority wins when the endings are mixed
	r := mustCompute(t, []byte("A.Arch00\r\nB.Arch00\r\nC.Arch00\nD.Arch00\r\n"), true)
	if r.LineEnding != "CRLF" || !strings.Contains(strings.Join(r.Repairs, " "), "Mixed line endings") {
		t.Fatalf("%v %v", r.LineEnding, r.Repairs)
	}
}

func TestExtraEntriesAndCommentsKept(t *testing.T) {
	in := "; my list\r\nFEAR.Arch00\r\n// note FEARA.Arch00GLUED\r\n\r\nSomeOtherMod\r\nFEAR.Arch00\r\n#end\r\n"
	r := mustCompute(t, []byte(in), true)
	want := []string{"; my list", "FEAR.Arch00", "// note FEARA.Arch00GLUED", "SomeOtherMod", "FEAR.Arch00", "#end", mod}
	if strings.Join(r.After, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", r.After)
	}
	if r.Entries != 3 {
		t.Fatalf("entries %d", r.Entries)
	}
}

func TestEarlierModEntriesMovedToTheEnd(t *testing.T) {
	for _, line := range []string{mod, strings.ToLower(mod), ".\\" + mod, mod + "\\", " " + mod + " "} {
		in := "FEAR.Arch00\r\n" + line + "\r\nOtherMod\r\n"
		r := mustCompute(t, []byte(in), true)
		if strings.Join(r.After, "|") != "FEAR.Arch00|OtherMod|"+mod {
			t.Errorf("%q -> %q", line, r.After)
		}
		if len(r.Repairs) == 0 {
			t.Errorf("%q: move not reported", line)
		}
	}
	// already last: untouched
	in := "FEAR.Arch00\r\n" + mod + "\r\n"
	if r := mustCompute(t, []byte(in), true); r.Changed {
		t.Fatalf("changed an up-to-date file: %q", r.Content)
	}
	// two copies
	r := mustCompute(t, []byte(mod+"\r\nFEAR.Arch00\r\n"+mod+"\r\n"), true)
	if strings.Join(r.After, "|") != "FEAR.Arch00|"+mod {
		t.Fatalf("%q", r.After)
	}
}

func TestMissingAndEmptyFile(t *testing.T) {
	r := mustCompute(t, nil, false)
	if r.Existed || !r.Changed || len(r.After) != 37 || r.After[36] != mod || !strings.Contains(r.Summary, "missing") || len(r.Warnings) == 0 {
		t.Fatalf("%+v", r)
	}
	r = mustCompute(t, []byte("\r\n \r\n"), true) // only blanks
	if len(r.After) != 37 || !strings.Contains(strings.Join(r.Warnings, " "), "has no entries") {
		t.Fatalf("%+v", r)
	}
	r = mustCompute(t, []byte("; only a comment\r\n"), true)
	if r.After[0] != "; only a comment" || len(r.After) != 38 {
		t.Fatalf("%q", r.After)
	}
	// nothing to rebuild from: an error, not an invented list
	if _, err := Compute(nil, false, Options{Mod: mod}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRefusesBinaryAndHuge(t *testing.T) {
	if _, err := Compute([]byte("MZ\x00\x01\x02binary\x00\x00"), true, Options{Mod: mod}); err != ErrNotText {
		t.Fatalf("binary: %v", err)
	}
	if _, err := Compute(bytes.Repeat([]byte("A.Arch00\r\n"), MaxSize/8), true, Options{Mod: mod}); err != ErrTooLarge {
		t.Fatalf("huge: %v", err)
	}
	for _, bad := range []string{"", "a\nb", "a\x00b"} {
		if _, err := Compute(stock(), true, Options{Mod: bad}); err == nil {
			t.Errorf("mod %q accepted", bad)
		}
	}
	// invalid UTF-8 (8-bit code page) is kept byte for byte
	in := []byte("FEAR.Arch00\r\nM\xf6d\r\n")
	r := mustCompute(t, in, true)
	if !bytes.Contains(r.Content, []byte("M\xf6d\r\n")) || len(r.Warnings) == 0 {
		t.Fatalf("%q %v", r.Content, r.Warnings)
	}
}

func TestStockOrder(t *testing.T) {
	got := StockOrder([]string{"fear_2.arch00", "FEARE.Arch00", "FEAR.Arch00", "FEARL_1.Arch00", "notes.txt", "FEAR_1.Arch00"})
	if strings.Join(got, "|") != "FEAR.Arch00|FEARE.Arch00|FEAR_1.Arch00|FEARL_1.Arch00|fear_2.arch00" {
		t.Fatalf("%q", got)
	}
}

func TestApplyAndPreview(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"FEAR.Arch00", "FEAR_8.Arch00"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	cfg := filepath.Join(dir, "Default.archcfg")
	os.WriteFile(cfg, []byte("FEAR.Arch00\r\nFEAR_8.Arch00"), 0o444) // read-only, no final break
	r, ro, err := Preview(dir, mod)
	if err != nil || !ro || !r.Changed {
		t.Fatalf("preview: %v ro=%v %+v", err, ro, r)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "FEAR.Arch00\r\nFEAR_8.Arch00" {
		t.Fatal("Preview wrote to the file")
	}
	if _, err := Apply(dir, mod); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg)
	if string(got) != "FEAR.Arch00\r\nFEAR_8.Arch00\r\n"+mod+"\r\n" {
		t.Fatalf("%q", got)
	}
	if st, _ := os.Stat(cfg); st.Mode().Perm()&0o200 != 0 {
		t.Fatalf("read-only attribute not restored: %v", st.Mode())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, BackupName)); string(b) != "FEAR.Arch00\r\nFEAR_8.Arch00" {
		t.Fatalf("backup: %q", b)
	}
	// second run: nothing to do, backup keeps the pristine file
	if r, err := Apply(dir, mod); err != nil || r.Changed {
		t.Fatalf("second run: %v %+v", err, r)
	}
	// a damaged state later: the backup is never overwritten
	os.Chmod(cfg, 0o644)
	os.WriteFile(cfg, []byte("FEAR.Arch00\r\nOther\r\n"), 0o644)
	if _, err := Apply(dir, mod); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, BackupName)); string(b) != "FEAR.Arch00\r\nFEAR_8.Arch00" {
		t.Fatalf("backup was overwritten: %q", b)
	}
	if _, err := os.Stat(cfg + ".CR_tmp"); err == nil {
		t.Fatal("temporary file left behind")
	}
}

func TestApplyMissingFileAndCaseInsensitiveName(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"FEAR.Arch00", "FEARL_8.Arch00"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	r, err := Apply(dir, mod)
	if err != nil || r.Existed {
		t.Fatalf("%v %+v", err, r)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "Default.archcfg"))
	if string(b) != "FEAR.Arch00\r\nFEARL_8.Arch00\r\n"+mod+"\r\n" {
		t.Fatalf("%q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, BackupName)); err == nil {
		t.Fatal("no backup expected when there was no file")
	}
	// upper-case file name on a case-sensitive file system is found and not duplicated
	d2 := t.TempDir()
	os.WriteFile(filepath.Join(d2, "DEFAULT.ARCHCFG"), []byte("FEAR.Arch00"), 0o644)
	if _, err := Apply(d2, mod); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(d2)
	if len(ents) != 2 { // the file and its backup
		t.Fatalf("%v", ents)
	}
}

func FuzzCompute(f *testing.F) {
	f.Add(stock())
	f.Add([]byte("\xff\xfeF\x00E\x00A\x00R\x00"))
	f.Add([]byte("A.Arch00B.Arch00C\r\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		r, err := Compute(in, true, Options{Mod: mod, Stock: stockLines()})
		if err != nil {
			return
		}
		if len(r.After) == 0 || r.After[len(r.After)-1] != mod {
			t.Fatalf("mod is not last: %q", r.After)
		}
		again, err := Compute(r.Content, true, Options{Mod: mod, Stock: stockLines()})
		if err != nil || again.Changed {
			t.Fatalf("not idempotent for %q: %v", in, err)
		}
	})
}
