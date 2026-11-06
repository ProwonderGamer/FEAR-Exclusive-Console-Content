package verify

import (
	"os"
	"path/filepath"
	"testing"

	"fearinstaller/internal/vfs"
)

// A parent folder holding several Xbox 360 games: another game's default.xex one level down must
// not be taken for F.E.A.R.; the F.E.A.R. dump three levels down must be found.
func TestFindX360RootSkipsOtherGames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Xbox 360 Games")
	mk := func(rel string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("Another Game/default.xex")
	mk("Another Game/Game/data.bin")
	mk("F.E.A.R. 1/FEAR 1 Xbox 360 Dump/F.E.A.R. Xbox 360/default.xex")
	mk("F.E.A.R. 1/FEAR 1 Xbox 360 Dump/F.E.A.R. Xbox 360/Game/uberCatalog.cat")
	fsys, err := vfs.OpenOrdered(root, x360Order, recognizedLayout)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()
	l, ok := findX360Root(fsys)
	if !ok || l.prefix != "F.E.A.R. 1/FEAR 1 Xbox 360 Dump/F.E.A.R. Xbox 360" {
		t.Fatalf("ok=%v prefix=%q", ok, l.prefix)
	}

	only := filepath.Join(t.TempDir(), "Games")
	if err := os.MkdirAll(filepath.Join(only, "Another Game"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(only, "Another Game", "default.xex"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs2, err := vfs.OpenOrdered(only, x360Order, recognizedLayout)
	if err != nil {
		t.Fatal(err)
	}
	defer fs2.Close()
	if _, ok := findX360Root(fs2); ok {
		t.Fatal("another game's default.xex was accepted as F.E.A.R.")
	}
}
