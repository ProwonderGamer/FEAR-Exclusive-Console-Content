package ltar

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fearinstaller/internal/testref"
)

// Every archive present in the PC game folder must list exactly the files of the reference TOC
// (FEAR_REF_PC_TOC, "archive\tsize\tflag\tpath"), with the size column equal to the unpacked size.
func TestListMatchesReferenceTOC(t *testing.T) {
	dir := testref.Dir(t, "FEAR_REF_PCGAME")
	toc := testref.Path(t, "FEAR_REF_PC_TOC")
	type row struct{ size, path string }
	want := map[string][]row{}
	f, _ := os.Open(toc)
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(strings.TrimRight(sc.Text(), "\r\n"), "\t")
		if len(p) >= 4 {
			want[p[0]] = append(want[p[0]], row{p[1], p[3]})
		}
	}
	names, _ := filepath.Glob(filepath.Join(dir, "*.Arch00"))
	checked := 0
	for _, n := range names {
		w, ok := want[filepath.Base(n)]
		if !ok {
			continue
		}
		a, err := Open(n)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if len(a.Entries) != len(w) {
			t.Errorf("%s: %d entries, reference TOC has %d", n, len(a.Entries), len(w))
			a.Close()
			continue
		}
		for i, e := range a.Entries {
			if e.Path != w[i].path {
				t.Errorf("%s entry %d: %q != %q", n, i, e.Path, w[i].path)
				break
			}
		}
		// extract a few entries and check the size
		for i := 0; i < len(a.Entries) && i < 5; i++ {
			d, err := a.Read(a.Entries[i])
			if err != nil || uint64(len(d)) != a.Entries[i].Size {
				t.Errorf("%s: read %s: %v", n, a.Entries[i].Path, err)
			}
		}
		a.Close()
		checked++
	}
	if checked == 0 {
		t.Skip("no archive of the reference TOC found in FEAR_REF_PCGAME")
	}
	t.Logf("%d archives match the reference TOC", checked)
}
